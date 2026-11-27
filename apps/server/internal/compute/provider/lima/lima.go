// Package lima adapts a pinned limactl executable. It never edits Lima's state files.
package lima

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/internal/providerutil"
	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
	"github.com/cofy-x/axv/apps/server/internal/diskspace"
	"github.com/cofy-x/axv/apps/server/internal/filedigest"
	guestproto "github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/limitedbuffer"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
)

type Driver struct {
	config            compute.LimaConfig
	root, home, owner string
	verifier          filedigest.Verifier
}

type state struct {
	MachineID string            `json:"machineId"`
	RuntimeID string            `json:"runtimeId,omitempty"`
	Guest     guestproto.Config `json:"guest"`
}

func New(root, owner string, config compute.LimaConfig) (*Driver, error) {
	root = filepath.Join(root, "lima-runtime")
	home := filepath.Join(root, "home")
	if config.Home != "" {
		if !filepath.IsAbs(config.Home) {
			return nil, fmt.Errorf("Lima home must be absolute")
		}
		home = filepath.Clean(config.Home)
		if home == filepath.Clean(root) {
			return nil, fmt.Errorf("Lima home must be separate from its provider metadata directory")
		}
	}
	// Lima reserves a suffix for OpenSSH's temporary control socket. Validate
	// before creating storage so an unusable data directory fails at startup.
	socket := filepath.Join(home, "vm-"+strings.Repeat("0", 32), "ssh.sock."+strings.Repeat("0", 16))
	if runtime.GOOS == "darwin" && len(socket) >= 104 {
		return nil, fmt.Errorf("Lima data directory is too long for macOS Unix sockets; configure a shorter lima.home such as ~/.axv/lima")
	}
	if err := owneddir.Ensure(root, owner); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(config.Binary) {
		return nil, fmt.Errorf("Lima binary path must be absolute")
	}
	if err := providerutil.PinJSON(filepath.Join(root, "provider.json"), config); err != nil {
		return nil, err
	}
	d := &Driver{config: config, root: root, home: home, owner: owner}
	if config.Home != "" {
		if err := owneddir.Ensure(d.home, owner); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(d.home, 0700); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *Driver) command(ctx context.Context, input io.Reader, args ...string) ([]byte, error) {
	return d.commandWithFiles(ctx, input, nil, args...)
}

func (d *Driver) commandWithFiles(ctx context.Context, input io.Reader, files []*os.File, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	logger := diagnostics.FromContext(ctx).With("provider", "lima", "command", args[0])
	started := time.Now()
	logger.Info("provider.command_started")
	cmd := exec.CommandContext(ctx, d.config.Binary, append([]string{"--tty=false"}, args...)...)
	if len(files) != 0 {
		// A disk mutation must retain its inherited lock until limactl and its
		// synchronous helpers finish. Killing only the CLI on cancellation can
		// orphan a helper after releasing the lock that protects its storage.
		cmd.Cancel = nil
	}
	cmd.ExtraFiles = files
	cmd.Dir = d.root
	cmd.Env = d.environment()
	cmd.Stdin = input
	out, diagnostic := &limitedbuffer.Buffer{Limit: 4 << 20}, &limitedbuffer.Buffer{Limit: 32 << 10}
	cmd.Stdout, cmd.Stderr = out, diagnostic
	if err := cmd.Run(); err != nil {
		if args[0] == "shell" {
			logger.Error("provider.command_failed", err, "durationMs", time.Since(started).Milliseconds())
		} else {
			logger.Error("provider.command_failed", err, "durationMs", time.Since(started).Milliseconds(), "diagnostic", strings.TrimSpace(string(diagnostic.Data)))
		}
		return nil, fmt.Errorf("limactl %s: %w: %s", args[0], err, strings.TrimSpace(string(diagnostic.Data)))
	}
	logger.Info("provider.command_completed", "durationMs", time.Since(started).Milliseconds())
	if len(args) > 1 && args[0] == "disk" && args[1] == "list" && len(diagnostic.Data) != 0 {
		return nil, fmt.Errorf("Lima disk inspection: %s", diagnostic.Data)
	}
	if out.Truncated {
		return nil, fmt.Errorf("Lima returned more output than allowed")
	}
	return out.Data, nil
}

func (d *Driver) environment() []string {
	var env []string
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "LIMA_") && !strings.HasPrefix(value, "SSH=") {
			env = append(env, value)
		}
	}
	return append(env, "LIMA_HOME="+d.home)
}

func (d *Driver) Probe(ctx context.Context) compute.Capability {
	c := compute.Capability{Provider: "lima", PersistentVolumes: true, GuestExecution: true, Version: d.config.Version, Configuration: true, ForceStop: true, Volumes: true, VolumeCopy: true, Images: true, SnapshotReason: "Lima/VZ does not implement native snapshots. Use an AXV cold checkpoint or backup when available."}
	if runtime.GOOS != "darwin" {
		c.Reason = "This adapter currently supports macOS with VZ."
		return c
	}
	if d.config.Version == "" {
		c.Reason = "Pin the limactl version in host configuration."
		return c
	}
	if d.config.Version != compute.SupportedLimaVersion {
		c.Reason = "This AXV release supports Lima " + compute.SupportedLimaVersion + ". Update the provider pin through an explicit maintenance operation."
		return c
	}
	b, err := d.command(ctx, nil, "--version")
	if err != nil {
		c.Reason = err.Error()
		return c
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 || strings.TrimPrefix(fields[len(fields)-1], "v") != d.config.Version {
		c.Reason = "limactl version does not match the configured pin."
		return c
	}
	c.Available = true
	return c
}

func (d *Driver) directory(m compute.Machine) string { return filepath.Join(d.root, m.ID) }

func (d *Driver) runtimeName(m compute.Machine) (string, error) {
	st, err := d.load(m)
	if os.IsNotExist(err) {
		return m.ID, nil
	}
	if err != nil {
		return "", err
	}
	if st.RuntimeID == "" {
		return m.ID, nil
	}
	if strings.ContainsAny(st.RuntimeID, "/\\\x00") || st.RuntimeID == "." || st.RuntimeID == ".." {
		return "", fmt.Errorf("invalid VM runtime identity")
	}
	return st.RuntimeID, nil
}

func (d *Driver) load(m compute.Machine) (state, error) {
	var st state
	if err := owneddir.Check(d.directory(m), d.owner+":"+m.ID); err != nil {
		return st, err
	}
	err := jsonfile.Read(filepath.Join(d.directory(m), "state.json"), &st)
	if err == nil && st.MachineID != m.ID {
		err = fmt.Errorf("Lima runtime identity mismatch")
	}
	return st, err
}

func (d *Driver) Ensure(ctx context.Context, m compute.Machine, image compute.Image, v compute.Volume) error {
	if c := d.Probe(ctx); !c.Available {
		return fmt.Errorf("%s", c.Reason)
	}
	if err := d.verifier.Verify(image.Rootfs, image.RootfsSHA256); err != nil {
		return err
	}
	if err := owneddir.Ensure(d.directory(m), d.owner+":"+m.ID); err != nil {
		return err
	}
	if err := providerutil.PinJSON(filepath.Join(d.directory(m), "image.json"), image); err != nil {
		return err
	}
	st, err := d.load(m)
	if os.IsNotExist(err) {
		st = state{MachineID: m.ID, RuntimeID: m.ID, Guest: guestproto.Config{MachineID: m.ID, Token: compute.NewID("token"), ProjectRoot: "/projects"}}
		if err = jsonfile.Write(filepath.Join(d.directory(m), "state.json"), st, 0600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err = d.ensureDisk(ctx, v); err != nil {
		return err
	}
	o, err := d.Inspect(ctx, m)
	if err != nil {
		return err
	}
	if o.Status != "absent" {
		return atomicfile.Write(filepath.Join(d.directory(m), "created"), []byte(m.ID), 0600)
	}
	if _, err = os.Stat(filepath.Join(d.directory(m), "created")); err == nil {
		return fmt.Errorf("previously created Lima machine is missing; refusing to replace its root disk")
	} else if !os.IsNotExist(err) {
		return err
	}
	arch := "aarch64"
	if image.Arch == "amd64" {
		arch = "x86_64"
	}
	// JSON is valid YAML and avoids interpolation of names or arbitrary user text.
	configuration := map[string]any{
		"vmType": "vz", "arch": arch, "upgradePackages": false, "cpus": m.Spec.Resources.CPUs,
		"memory": fmt.Sprintf("%dMiB", m.Spec.Resources.MemoryMiB), "disk": fmt.Sprintf("%dGiB", m.Spec.Resources.DiskGiB),
		"images": []any{map[string]any{"location": image.Rootfs, "arch": arch, "digest": "sha256:" + image.RootfsSHA256}},
		"mounts": []any{}, "containerd": map[string]bool{"system": false, "user": false},
		"ssh":             map[string]bool{"loadDotSSHPubKeys": false, "forwardAgent": false},
		"user":            map[string]any{"name": "axv", "uid": 1000, "home": "/home/axv", "shell": "/bin/bash"},
		"additionalDisks": []any{map[string]any{"name": v.ID, "format": false, "fsType": "ext4"}},
		"portForwards":    []any{map[string]any{"guestIP": "0.0.0.0", "guestIPMustBeZero": false, "guestPortRange": []int{1, 65535}, "proto": "any", "ignore": true}},
		"provision":       d.provision(v, st.Guest.ProjectRoot),
	}
	path := filepath.Join(d.directory(m), "instance.yaml")
	if err = diskspace.Require(d.root, m.Spec.Resources.DiskGiB<<30); err != nil {
		return err
	}
	if err = jsonfile.Write(path, configuration, 0600); err != nil {
		return err
	}
	instance, err := d.runtimeName(m)
	if err != nil {
		return err
	}
	if _, err = d.command(ctx, nil, "create", "--name="+instance, path); err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(d.directory(m), "created"), []byte(m.ID), 0600)
}

func (d *Driver) Start(ctx context.Context, m compute.Machine, image compute.Image, v compute.Volume) error {
	st, err := d.load(m)
	if err != nil {
		return err
	}
	instance, err := d.runtimeName(m)
	if err != nil {
		return err
	}
	if _, err = d.command(ctx, nil, "start", instance); err != nil {
		return err
	}
	return d.prepareProject(ctx, m, v, st.Guest.ProjectRoot)
}

func (d *Driver) Inspect(ctx context.Context, m compute.Machine) (compute.Observation, error) {
	name, err := d.runtimeName(m)
	if err != nil {
		return compute.Observation{}, err
	}
	// A named list exits unsuccessfully when the instance does not exist. List
	// the private home instead so a missing instance remains distinct from a
	// failed inspection. Lima emits one JSON object per instance.
	b, err := d.command(ctx, nil, "list", "--format=json")
	if err != nil {
		return compute.Observation{}, err
	}
	type listedInstance struct {
		Name, Status string
		Errors       []string
	}
	var instance *listedInstance
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	for {
		var item listedInstance
		if err = decoder.Decode(&item); err == io.EOF {
			break
		} else if err != nil {
			return compute.Observation{}, err
		}
		if item.Name == "" {
			return compute.Observation{}, fmt.Errorf("Lima instance inspection omitted its identity")
		}
		if item.Name == name {
			if instance != nil {
				return compute.Observation{}, fmt.Errorf("Lima returned duplicate instance identities")
			}
			instance = &item
		}
	}
	if instance == nil {
		return compute.Observation{Status: "absent"}, nil
	}
	if len(instance.Errors) != 0 {
		return compute.Observation{}, fmt.Errorf("Lima instance inspection error: %s", strings.Join(instance.Errors, "; "))
	}
	if err = owneddir.Check(d.directory(m), d.owner+":"+m.ID); err != nil {
		return compute.Observation{}, err
	}
	switch strings.ToLower(instance.Status) {
	case "running":
		return compute.Observation{Status: "running"}, nil
	case "stopped":
		return compute.Observation{Status: "stopped"}, nil
	default:
		return compute.Observation{Status: "unknown", Reason: "Lima reports " + instance.Status}, nil
	}
}

func (d *Driver) Stop(ctx context.Context, m compute.Machine) error {
	o, err := d.Inspect(ctx, m)
	if err != nil {
		return err
	}
	if o.Status == "absent" || o.Status == "stopped" {
		return nil
	}
	name, err := d.runtimeName(m)
	if err != nil {
		return err
	}
	_, err = d.command(ctx, nil, "stop", name)
	return err
}

func (d *Driver) ForceStop(ctx context.Context, m compute.Machine) error {
	o, err := d.Inspect(ctx, m)
	if err != nil {
		return err
	}
	if o.Status == "absent" || o.Status == "stopped" {
		return nil
	}
	name, err := d.runtimeName(m)
	if err != nil {
		return err
	}
	_, err = d.command(ctx, nil, "stop", "--force", name)
	return err
}

func (d *Driver) Configure(ctx context.Context, m compute.Machine, target compute.Spec, _ compute.Volume) error {
	o, err := d.Inspect(ctx, m)
	if err != nil {
		return err
	}
	if o.Status != "stopped" {
		return fmt.Errorf("the VM must be stopped before editing its resources")
	}
	if target.Resources.DiskGiB < m.Spec.Resources.DiskGiB {
		return fmt.Errorf("root disk shrinking is not supported")
	}
	expression := fmt.Sprintf(".cpus = %d | .memory = %q | .disk = %q", target.Resources.CPUs, fmt.Sprintf("%dMiB", target.Resources.MemoryMiB), fmt.Sprintf("%dGiB", target.Resources.DiskGiB))
	name, err := d.runtimeName(m)
	if err != nil {
		return err
	}
	_, err = d.command(ctx, nil, "edit", "--set="+expression, name)
	return err
}

func (d *Driver) Delete(ctx context.Context, m compute.Machine) error {
	o, err := d.Inspect(ctx, m)
	if err != nil {
		return err
	}
	if o.Status != "absent" {
		if o.Status != "stopped" {
			return fmt.Errorf("stop the machine before deleting it")
		}
		name, nameErr := d.runtimeName(m)
		if nameErr != nil {
			return nameErr
		}
		if _, err = d.command(ctx, nil, "delete", name); err != nil {
			return err
		}
	}
	if err = owneddir.Check(d.directory(m), d.owner+":"+m.ID); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return os.RemoveAll(d.directory(m))
}
