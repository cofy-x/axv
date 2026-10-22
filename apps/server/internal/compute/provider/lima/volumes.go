package lima

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/internal/providerutil"
	"github.com/cofy-x/axv/apps/server/internal/diskspace"
	"github.com/cofy-x/axv/apps/server/internal/filedigest"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
)

const projectScript = `set -eu
project="$1"
allow_format="$2"
volume_id="$3"
marker="/var/lib/axv/volumes/$volume_id"
udevadm settle
if [ "$(blkid -s TYPE -o value /dev/vdb1 2>/dev/null || true)" != ext4 ]; then
  if [ "$allow_format" != true ] || [ -e "$marker" ]; then
    echo 'Existing project filesystem is missing; refusing to format' >&2; exit 1
  fi
  signatures=$(wipefs --no-act --noheadings --output TYPE /dev/vdb)
  if [ -n "$signatures" ]; then
    echo 'Project disk contains unrecognized or incomplete data; refusing to format' >&2; exit 1
  fi
  printf 'type=linux\n' | sfdisk --label gpt /dev/vdb
  udevadm settle
  mkfs.ext4 -q /dev/vdb1
fi
if command -v growpart >/dev/null; then
  growth=$(growpart /dev/vdb 1 2>&1) || {
    case "$growth" in *NOCHANGE*) ;; *) printf '%s\n' "$growth" >&2; exit 1;; esac
  }
fi
resize2fs /dev/vdb1
mkdir -p "$project"
if mountpoint -q "$project"; then
  source=$(findmnt -n -o SOURCE --target "$project")
  if [ "$(readlink -f "$source")" != /dev/vdb1 ]; then
    echo 'Project mount points to another filesystem' >&2; exit 1
  fi
else
  mount -t ext4 -o nosuid,nodev /dev/vdb1 "$project"
fi
if id axv >/dev/null 2>&1; then chown axv:axv "$project"; fi
mkdir -p /var/lib/axv/volumes
printf 'initialized\n' > "$marker"
`

func (d *Driver) provision(v compute.Volume, project string) []any {
	// The reviewed image supplies dependencies; Lima must not resolve packages
	// from mutable repositories during instance startup.
	scripts := []any{map[string]any{"mode": "dependency", "skipDefaultDependencyResolution": true, "script": "#!/bin/sh\nset -eu\nexit 0\n"}}
	if v.ID != "" {
		allow := "false"
		if _, err := os.Stat(filepath.Join(d.root, "volumes", v.ID, "initialized")); os.IsNotExist(err) {
			allow = "true"
		}
		boot := fmt.Sprintf("#!/bin/sh\nset -- %q %q %q\n", project, allow, v.ID) + projectScript
		scripts = append(scripts, map[string]any{"mode": "boot", "script": boot})
	}
	return scripts
}

type diskInfo struct {
	Name     string `json:"name"`
	Dir      string `json:"dir"`
	Format   string `json:"format"`
	Instance string `json:"instance"`
	Size     int64  `json:"size"`
}

func (d *Driver) inspectDisk(ctx context.Context, id string) (result diskInfo, found bool, err error) {
	b, err := d.command(ctx, nil, "disk", "list", "--json")
	if err != nil {
		return result, false, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	for {
		var item diskInfo
		if err = decoder.Decode(&item); err == io.EOF {
			return result, found, nil
		} else if err != nil {
			return result, false, err
		}
		if item.Name == "" {
			return result, false, fmt.Errorf("disk inspection omitted its identity")
		}
		if item.Name == id {
			if found {
				return result, false, fmt.Errorf("disk inspection returned duplicate identities")
			}
			result, found = item, true
		}
	}
}

func (d *Driver) volumeFile(ctx context.Context, v compute.Volume) (*os.File, error) {
	if err := owneddir.Check(filepath.Join(d.root, "volumes", v.ID), d.owner+":"+v.ID); err != nil {
		return nil, err
	}
	info, found, err := d.inspectDisk(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("the project disk is missing; no replacement was created")
	}
	if info.Format != "raw" && info.Format != "qcow2" {
		return nil, fmt.Errorf("the managed project disk has an unsupported format")
	}
	if info.Size <= 0 || (v.SizeGiB > 0 && info.Size != v.SizeGiB<<30) {
		return nil, fmt.Errorf("the project disk does not match its reserved size")
	}
	home, err := filepath.EvalSymlinks(d.home)
	if err != nil {
		return nil, err
	}
	if filepath.Clean(info.Dir) != filepath.Join(home, "_disks", v.ID) {
		return nil, fmt.Errorf("disk inspection returned a path outside its owned namespace")
	}
	root, err := os.OpenRoot(info.Dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open("datadisk")
	if err != nil {
		return nil, err
	}
	stat, err := file.Stat()
	if err == nil && !stat.Mode().IsRegular() {
		err = fmt.Errorf("the project disk must be a regular file")
	}
	if err == nil && info.Format == "raw" && stat.Size() != info.Size {
		err = fmt.Errorf("the existing project disk is truncated; no replacement was created")
	}
	if err == nil && info.Format == "qcow2" {
		// Lima can report qcow2 before the first VZ boot converts it to raw.
		// Accept only self-contained storage with the expected virtual size.
		var size int64
		size, err = compute.InspectImage(file)
		if err == nil && size != (info.Size+(1<<30)-1)>>30 {
			err = fmt.Errorf("the project disk has an unexpected virtual size")
		}
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func (d *Driver) EnsureVolume(ctx context.Context, v compute.Volume) error {
	return d.ensureDisk(ctx, v)
}

func (d *Driver) ResizeVolume(ctx context.Context, v compute.Volume, size int64) error {
	return d.resizeVolume(ctx, v, size, nil)
}

func (d *Driver) resizeVolume(ctx context.Context, v compute.Volume, size int64, files []*os.File) error {
	if size < v.SizeGiB {
		return fmt.Errorf("volume shrinking is not supported")
	}
	if err := owneddir.Check(filepath.Join(d.root, "volumes", v.ID), d.owner+":"+v.ID); err != nil {
		return err
	}
	if v.MachineID != "" {
		o, err := d.Inspect(ctx, compute.Machine{ID: v.MachineID})
		if err != nil {
			return err
		}
		if o.Status != "stopped" {
			return fmt.Errorf("the attached VM is not confirmed stopped")
		}
	}
	if err := diskspace.Require(d.root, (size-v.SizeGiB)<<30); err != nil {
		return err
	}
	if _, err := d.commandWithFiles(ctx, nil, files, "disk", "resize", v.ID, "--size", fmt.Sprintf("%dGiB", size)); err != nil {
		return err
	}
	info, found, err := d.inspectDisk(ctx, v.ID)
	if err != nil {
		return err
	}
	if !found || info.Size != size<<30 {
		return fmt.Errorf("the resized disk could not be verified")
	}
	return nil
}

func (d *Driver) AttachVolume(ctx context.Context, m compute.Machine, v compute.Volume) error {
	o, err := d.Inspect(ctx, m)
	if err != nil {
		return err
	}
	if o.Status != "stopped" {
		return fmt.Errorf("the VM must be confirmed stopped before changing its project disk")
	}
	st, err := d.load(m)
	if err != nil {
		return err
	}
	disks := []any{}
	if v.ID != "" {
		file, openErr := d.volumeFile(ctx, v)
		if openErr != nil {
			return openErr
		}
		file.Close()
		if err = ctx.Err(); err != nil {
			return err
		}
		disks = append(disks, map[string]any{"name": v.ID, "format": false, "fsType": "ext4"})
	}
	st.Guest.ProjectRoot = "/projects"
	diskJSON, _ := json.Marshal(disks)
	provisionJSON, _ := json.Marshal(d.provision(v, st.Guest.ProjectRoot))
	expression := ".additionalDisks = " + string(diskJSON) + " | .provision = " + string(provisionJSON)
	name, err := d.runtimeName(m)
	if err != nil {
		return err
	}
	if _, err = d.command(ctx, nil, "edit", "--set="+expression, name); err != nil {
		return err
	}
	return jsonfile.Write(filepath.Join(d.directory(m), "state.json"), st, 0600)
}

func (d *Driver) CopyVolume(ctx context.Context, source, target compute.Volume) error {
	if source.ID == "" || target.ID == "" || source.ID == target.ID || source.Provider != "lima" || target.Provider != source.Provider || target.SizeGiB < source.SizeGiB {
		return fmt.Errorf("an offline Lima copy requires independent volume identities, the same provider and a target at least as large as its source")
	}
	if source.MachineID != "" {
		o, err := d.Inspect(ctx, compute.Machine{ID: source.MachineID})
		if err != nil {
			return err
		}
		if o.Status != "stopped" {
			return fmt.Errorf("the source VM is not confirmed stopped")
		}
	}
	dir := filepath.Join(d.root, "volumes", target.ID)
	if err := owneddir.Ensure(dir, d.owner+":"+target.ID); err != nil {
		return err
	}
	if err := providerutil.PinJSON(filepath.Join(dir, "copy.json"), []any{source.ID, target.SizeGiB}); err != nil {
		return err
	}
	lock, err := lockVolumeCopy(ctx, dir)
	if err != nil {
		return err
	}
	defer lock.Close()
	file, err := d.volumeFile(ctx, source)
	if err != nil {
		return err
	}
	defer file.Close()
	if source.IsProjectBackup() {
		if err := providerutil.VerifyCopyCompletion(ctx, filepath.Join(d.root, "volumes", source.ID, "copy-complete"), source.Backup.SourceVolumeID, source.ID, source.SizeGiB, file); err != nil {
			return err
		}
	}
	if _, exists, err := providerutil.ReadCopyCompletion(filepath.Join(dir, "copy-complete"), source.ID, target.ID, target.SizeGiB); err != nil {
		return err
	} else if exists {
		info, found, err := d.inspectDisk(ctx, target.ID)
		if err != nil {
			return err
		}
		if !found || info.Size != target.SizeGiB<<30 {
			return fmt.Errorf("the completed volume copy is missing or changed; no replacement was created")
		}
		targetFile, err := d.volumeFile(ctx, target)
		if err != nil {
			return err
		}
		defer targetFile.Close()
		return providerutil.VerifyCopyCompletion(ctx, filepath.Join(dir, "copy-complete"), source.ID, target.ID, target.SizeGiB, targetFile)
	}
	copyPath := filepath.Join(dir, "source.raw")
	// A crash may retain either the capture or its partial staging file. Both
	// belong to this pinned intent; release them before reserving disk space.
	for _, name := range []string{copyPath, copyPath + ".copy"} {
		if err := providerutil.RemoveCopyScratch(name); err != nil {
			return err
		}
	}
	info, exists, err := d.inspectDisk(ctx, target.ID)
	if err != nil {
		return err
	}
	if exists {
		if info.Instance != "" {
			return fmt.Errorf("an incomplete volume copy is attached; refusing replacement")
		}
		if _, err = d.commandWithFiles(ctx, nil, []*os.File{lock}, "disk", "delete", target.ID); err != nil {
			return err
		}
		if _, stillExists, err := d.inspectDisk(ctx, target.ID); err != nil || stillExists {
			return fmt.Errorf("the incomplete copy could not be removed safely: %v", err)
		}
	}
	if err = diskspace.Require(d.root, (target.SizeGiB+source.SizeGiB)<<30); err != nil {
		return err
	}
	if err = copyArtifact(ctx, file, copyPath); err != nil {
		return err
	}
	defer os.Remove(copyPath)
	if _, err = d.commandWithFiles(ctx, nil, []*os.File{lock}, "disk", "import", target.ID, copyPath); err != nil {
		return err
	}
	imported, err := d.volumeFile(ctx, compute.Volume{ID: target.ID})
	if err != nil {
		return err
	}
	defer imported.Close()
	want, err := filedigest.Sum(ctx, file)
	if err != nil {
		return err
	}
	got, err := filedigest.Sum(ctx, imported)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("the imported volume copy failed its checksum verification")
	}
	if target.SizeGiB > source.SizeGiB {
		if err = d.resizeVolume(ctx, compute.Volume{ID: target.ID, SizeGiB: source.SizeGiB}, target.SizeGiB, []*os.File{lock}); err != nil {
			return err
		}
	}
	if _, err = os.Stat(filepath.Join(d.root, "volumes", source.ID, "initialized")); err == nil {
		if err = atomicfile.Write(filepath.Join(dir, "initialized"), []byte(target.ID), 0600); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = atomicfile.Write(filepath.Join(dir, "created"), []byte(target.ID), 0600); err != nil {
		return err
	}
	got, err = filedigest.Sum(ctx, imported)
	if err != nil {
		return err
	}
	return providerutil.WriteCopyCompletion(filepath.Join(dir, "copy-complete"), source.ID, target.ID, target.SizeGiB, got)
}
