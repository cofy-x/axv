//go:build linux

package guest

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"golang.org/x/sys/unix"
)

func checkTaskState(root, machine string, markerUID uint32) error {
	parent := filepath.Dir(root)
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || stat.Uid != markerUID || info.Mode().Perm() != 0755 {
		return errors.New("the guest task parent must be a real root-owned mode-0755 directory")
	}
	markerPath := filepath.Join(parent, "tasks.owner")
	marker, err := os.OpenFile(markerPath, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("guest task ownership is missing; do not recreate established state: %w", err)
	}
	defer marker.Close()
	info, err = marker.Stat()
	if err != nil {
		return err
	}
	stat, ok = info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Uid != markerUID || stat.Nlink != 1 || info.Mode().Perm() != 0644 || info.Size() > 1024 {
		return errors.New("the guest task owner marker is unsafe")
	}
	var owner struct {
		Version   int    `json:"version"`
		MachineID string `json:"machineId"`
	}
	data := make([]byte, info.Size())
	if _, err := marker.ReadAt(data, 0); err != nil {
		return err
	}
	if decodeTaskState(data, &owner) != nil || owner.Version != 1 || owner.MachineID != machine {
		return errors.New("the guest task store belongs to another machine or baseline")
	}
	info, err = os.Lstat(root)
	if err != nil {
		return err
	}
	stat, ok = info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm() != 0700 {
		return errors.New("the guest task directory must be private and owned by the guest account")
	}
	if info, err = os.Lstat(filepath.Join(root, ".lock")); err == nil {
		stat, ok = info.Sys().(*syscall.Stat_t)
		if !ok || !info.Mode().IsRegular() || stat.Nlink != 1 || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm() != 0600 {
			return errors.New("the guest task lock is unsafe")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func openTaskRegular(name string, flags int) (*os.File, error) {
	// Validate the descriptor before performing I/O. A corrupt FIFO would
	// otherwise block this open indefinitely before its type could be checked.
	file, err := os.OpenFile(name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Nlink != 1 || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm() != 0600 {
		file.Close()
		return nil, errors.New("guest task metadata/log must be a private regular file owned by the guest account")
	}
	return file, nil
}

func openTaskLog(root, name string, create bool) (*os.File, error) {
	flags := os.O_RDONLY
	if create {
		flags = os.O_CREATE | os.O_EXCL | os.O_RDWR
	}
	return openTaskRegular(filepath.Join(root, name), flags)
}

func taskCommandDirectory(directory *os.File) string {
	// Go's Linux fork/exec changes directory before remapping descriptors. The
	// already-open CLOEXEC directory pins that choice until exec completes.
	return "/proc/self/fd/" + strconv.Itoa(int(directory.Fd()))
}

func taskFreeSpace(state string, project *os.Root, logBytes int64) error {
	file, err := project.Open(".")
	if err != nil {
		return err
	}
	defer file.Close()
	var stateFS, projectFS unix.Statfs_t
	if err := unix.Statfs(state, &stateFS); err != nil {
		return err
	}
	if err := unix.Fstatfs(int(file.Fd()), &projectFS); err != nil {
		return err
	}
	if uint64(stateFS.Bavail)*uint64(stateFS.Bsize) < uint64(logBytes)+(16<<20) || uint64(projectFS.Bavail)*uint64(projectFS.Bsize) < 1<<20 {
		return projectProblem(507, "disk_full", "Insufficient real free space for project execution, bounded logs and the durable task journal.")
	}
	return nil
}

type linuxTaskGroups struct {
	root *os.Root
}

type linuxTaskGroup struct {
	owner *linuxTaskGroups
	name  string
	root  *os.Root
	file  *os.File
}

func delegatedTaskPath(data string) (string, error) {
	name := ""
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		if strings.HasPrefix(line, "0::") {
			if name != "" {
				return "", errors.New("ambiguous unified cgroup membership")
			}
			name = strings.TrimPrefix(line, "0::")
		}
	}
	if !strings.HasPrefix(name, "/") || path.Clean(name) != name || path.Base(name) != "supervisor" || path.Base(path.Dir(name)) != "axv-guest.service" || strings.ContainsAny(name, "\x00\n\r") {
		return "", errors.New("project tasks require axv-guest.service with Delegate=cpu memory pids and DelegateSubgroup=supervisor")
	}
	return strings.TrimPrefix(path.Dir(name), "/"), nil
}

func systemTaskGroups() (taskGroups, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return nil, err
	}
	name, err := delegatedTaskPath(string(data))
	if err != nil {
		return nil, err
	}
	mount, err := os.OpenRoot("/sys/fs/cgroup")
	if err != nil {
		return nil, err
	}
	defer mount.Close()
	root, err := mount.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	manager := &linuxTaskGroups{root: root}
	if err := manager.prepare(); err != nil {
		root.Close()
		return nil, err
	}
	// Actual atomic cgroup placement is required, including kernel clone3 support.
	// A kernel or policy that rejects it cannot advertise project-tasks support.
	group, err := manager.Create(taskGroupName("probe-"+rand.Text()), protocol.TaskLimits{CPUs: 1, MemoryMiB: 64, MaxProcesses: 8})
	if err != nil {
		root.Close()
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/true")
	err = group.Configure(cmd)
	if err == nil {
		err = cmd.Run()
	}
	err = errors.Join(err, stopTaskGroup(group), group.Close())
	if err != nil {
		root.Close()
		return nil, fmt.Errorf("atomic task cgroup placement is unavailable: %w", err)
	}
	return manager, nil
}

func readTaskControl(root *os.Root, name string) (string, error) {
	data, err := root.ReadFile(name)
	return strings.TrimSpace(string(data)), err
}

func writeTaskControl(root *os.Root, name, value string) error {
	file, err := root.OpenFile(name, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, err = file.WriteString(value)
	return errors.Join(err, file.Close())
}

func (m *linuxTaskGroups) prepare() error {
	dir, err := m.root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	var filesystem unix.Statfs_t
	if err := unix.Fstatfs(int(dir.Fd()), &filesystem); err != nil || filesystem.Type != unix.CGROUP2_SUPER_MAGIC {
		return errors.New("task delegation is not on a cgroup v2 filesystem")
	}
	info, err := dir.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("the guest service does not own its delegated cgroup")
	}
	procs, err := readTaskControl(m.root, "cgroup.procs")
	if err != nil || procs != "" {
		return errors.New("the delegated task parent must have no resident processes")
	}
	controllers, err := readTaskControl(m.root, "cgroup.controllers")
	if err != nil {
		return err
	}
	for _, required := range []string{"cpu", "memory", "pids"} {
		if !strings.Contains(" "+controllers+" ", " "+required+" ") {
			return fmt.Errorf("the guest is missing delegated %s resource control", required)
		}
	}
	return writeTaskControl(m.root, "cgroup.subtree_control", "+cpu +memory +pids")
}

func (m *linuxTaskGroups) Create(name string, limits protocol.TaskLimits) (taskGroup, error) {
	if !strings.HasPrefix(name, "attempt-") || len(name) != 72 || strings.Trim(strings.TrimPrefix(name, "attempt-"), "0123456789abcdef") != "" {
		return nil, errors.New("invalid owned task cgroup name")
	}
	if err := m.root.Mkdir(name, 0755); err != nil {
		return nil, err
	}
	group, err := m.Existing(name)
	if err != nil {
		return nil, err
	}
	g := group.(*linuxTaskGroup)
	for _, setting := range []struct{ name, value string }{
		{"cpu.max", fmt.Sprintf("%d 100000", limits.CPUs*100000)},
		{"memory.max", strconv.FormatInt(limits.MemoryMiB<<20, 10)},
		{"memory.oom.group", "1"},
		{"pids.max", strconv.Itoa(limits.MaxProcesses)},
	} {
		if err = writeTaskControl(g.root, setting.name, setting.value); err == nil {
			var actual string
			actual, err = readTaskControl(g.root, setting.name)
			if err == nil && actual != setting.value {
				err = fmt.Errorf("task %s limit was not applied", setting.name)
			}
		}
		if err != nil {
			_ = stopTaskGroup(g)
			g.Close()
			return nil, err
		}
	}
	return g, nil
}

func (m *linuxTaskGroups) Existing(name string) (taskGroup, error) {
	root, err := m.root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	file, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, err
	}
	return &linuxTaskGroup{owner: m, name: name, root: root, file: file}, nil
}

func (m *linuxTaskGroups) Close() error { return m.root.Close() }

func (g *linuxTaskGroup) Configure(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{UseCgroupFD: true, CgroupFD: int(g.file.Fd())}
	return nil
}

func (g *linuxTaskGroup) Kill() error { return writeTaskControl(g.root, "cgroup.kill", "1") }

func taskEventCount(data, key string) (uint64, error) {
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == key {
			return strconv.ParseUint(fields[1], 10, 64)
		}
	}
	return 0, fmt.Errorf("required cgroup event %s is missing", key)
}

func (g *linuxTaskGroup) Events() (string, bool, error) {
	data, err := readTaskControl(g.root, "cgroup.events")
	if err != nil {
		return "", false, err
	}
	populated, err := taskEventCount(data, "populated")
	if err != nil {
		return "", false, err
	}
	data, err = readTaskControl(g.root, "memory.events")
	if err != nil {
		return "", false, err
	}
	oom, err := taskEventCount(data, "oom_kill")
	if err != nil {
		return "", false, err
	}
	if oom > 0 {
		return "memory_limit", populated != 0, nil
	}
	data, err = readTaskControl(g.root, "pids.events")
	if err != nil {
		return "", false, err
	}
	maximum, err := taskEventCount(data, "max")
	if err != nil {
		return "", false, err
	}
	if maximum > 0 {
		return "process_limit", populated != 0, nil
	}
	return "", populated != 0, nil
}

const maxTaskGroupDescendants = 1024
const maxTaskGroupDepth = 64

// cgroup.kill terminates the subtree but leaves nested cgroup directories behind.
// Remove only directories below this attempt's already-open handle. Kernel
// control files are never unlinked, and rmdir refuses a repopulated cgroup.
func removeTaskGroupChildren(root *os.Root, depth int, remaining *int) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	for {
		entries, readErr := directory.ReadDir(64)
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if *remaining == 0 || depth >= maxTaskGroupDepth {
				return errors.New("task cgroup cleanup exceeds its bounded descendant count or depth")
			}
			*remaining -= 1
			child, err := root.OpenRoot(entry.Name())
			if err != nil {
				return err
			}
			err = removeTaskGroupChildren(child, depth+1, remaining)
			err = errors.Join(err, child.Close())
			if err != nil {
				return err
			}
			if err := root.Remove(entry.Name()); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func (g *linuxTaskGroup) Remove() error {
	remaining := maxTaskGroupDescendants
	if err := removeTaskGroupChildren(g.root, 0, &remaining); err != nil {
		return err
	}
	return g.owner.root.Remove(g.name)
}
func (g *linuxTaskGroup) Close() error { return errors.Join(g.file.Close(), g.root.Close()) }
