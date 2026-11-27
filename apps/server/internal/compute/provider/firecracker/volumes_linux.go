//go:build linux

package firecracker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/internal/providerutil"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
)

func disk(ctx context.Context, path string, size int64, label, source string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	temporary := path + ".partial"
	f, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = f.Truncate(size)
	f.Close()
	if err != nil {
		return err
	}
	args := []string{"-F", "-q", "-L", label}
	if source != "" {
		args = append(args, "-d", source)
	}
	args = append(args, temporary)
	if _, err = providerutil.Command(ctx, "mkfs.ext4", args...); err != nil {
		return err
	}
	return atomicfile.Commit(temporary, path)
}

func (d *Driver) ensureVolume(ctx context.Context, v compute.Volume) error {
	dir := filepath.Join(d.root, "volumes", v.ID)
	if v.Revision > 1 {
		if exists, err := checkManagedDirectory(dir, d.owner+":"+v.ID); err != nil {
			return err
		} else if !exists {
			return fmt.Errorf("existing project volume %s storage is missing; refusing to recreate it", v.ID)
		}
	}
	if err := owneddir.Ensure(dir, d.owner+":"+v.ID); err != nil {
		return err
	}
	if data, err := os.ReadFile(filepath.Join(dir, "created")); err == nil {
		if string(data) != v.ID {
			return fmt.Errorf("project volume creation identity mismatch")
		}
		info, err := os.Lstat(filepath.Join(dir, "disk.ext4"))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("project volume disk is not a regular file")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if v.Revision > 1 {
		return fmt.Errorf("existing project volume %s creation metadata is missing; refusing to recreate it", v.ID)
	}
	if err := disk(ctx, filepath.Join(dir, "disk.ext4"), v.SizeGiB<<30, "axv-project", ""); err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(dir, "created"), []byte(v.ID), 0600)
}

func (d *Driver) checkProjectVolume(m compute.Machine, volumeID string) error {
	for _, path := range []string{filepath.Join(d.directory(m), "project.ext4"), filepath.Join(d.jail(m), "project.ext4")} {
		project, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue // Initial preparation may not have linked a project disk yet.
		}
		if err != nil {
			return err
		}
		if !project.Mode().IsRegular() || volumeID == "" {
			return fmt.Errorf("machine project disk has an invalid volume identity")
		}
		volume, err := d.projectVolumeDisk(volumeID)
		if err != nil {
			return err
		}
		if !os.SameFile(project, volume) {
			return fmt.Errorf("machine project disk does not match its owned project volume")
		}
	}
	return nil
}

func (d *Driver) checkRetainedProjectDeletion(m compute.Machine) error {
	if err := d.checkProjectVolume(m, m.Spec.VolumeID); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(d.directory(m), "prepared")); err == nil {
		// Preparation proves that a project disk existed, even if both machine
		// links have since disappeared. Its retained volume must still be valid.
		_, err = d.projectVolumeDisk(m.Spec.VolumeID)
		return err
	} else if !os.IsNotExist(err) {
		return err
	}
	// No project link and no completed preparation is an initial failure.
	// Deleting this runtime leaves any volume staging files untouched.
	return nil
}

func (d *Driver) projectVolumeDisk(volumeID string) (os.FileInfo, error) {
	if volumeID == "" {
		return nil, fmt.Errorf("machine has no project volume identity")
	}
	dir := filepath.Join(d.root, "volumes", volumeID)
	if exists, err := checkManagedDirectory(dir, d.owner+":"+volumeID); err != nil {
		return nil, err
	} else if !exists {
		return nil, fmt.Errorf("project volume %s storage is missing; preserve any linked project disk until its volume is recovered", volumeID)
	}
	data, err := os.ReadFile(filepath.Join(dir, "created"))
	if err != nil {
		return nil, fmt.Errorf("verify project volume creation identity: %w", err)
	}
	if string(data) != volumeID {
		return nil, fmt.Errorf("project volume creation identity mismatch")
	}
	volume, err := os.Lstat(filepath.Join(dir, "disk.ext4"))
	if err != nil {
		return nil, fmt.Errorf("project volume %s disk is missing; preserve any linked project disk until its volume is recovered: %w", volumeID, err)
	}
	if !volume.Mode().IsRegular() {
		return nil, fmt.Errorf("project volume disk is not a regular file")
	}
	return volume, nil
}

func (d *Driver) DeleteVolume(_ context.Context, v compute.Volume) error {
	dir := filepath.Join(d.root, "volumes", v.ID)
	if exists, err := checkManagedDirectory(dir, d.owner+":"+v.ID); err != nil {
		return err
	} else if !exists {
		return nil
	}
	return os.RemoveAll(dir)
}
