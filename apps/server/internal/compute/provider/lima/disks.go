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
	"github.com/cofy-x/axv/apps/server/internal/diskspace"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
)

func (d *Driver) ensureDisk(ctx context.Context, v compute.Volume) error {
	dir := filepath.Join(d.root, "volumes", v.ID)
	if err := owneddir.Ensure(dir, d.owner+":"+v.ID); err != nil {
		return err
	}
	exists, err := d.diskExists(ctx, v.ID)
	if err != nil {
		return err
	}
	if exists {
		file, err := d.volumeFile(ctx, v)
		if err != nil {
			return err
		}
		file.Close()
		return atomicfile.Write(filepath.Join(dir, "created"), []byte(v.ID), 0600)
	}
	if _, err = os.Stat(filepath.Join(dir, "created")); err == nil {
		return fmt.Errorf("previously created Lima volume is missing; refusing to replace it")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = diskspace.Require(d.root, v.SizeGiB<<30); err != nil {
		return err
	}
	if _, err = d.command(ctx, nil, "disk", "create", v.ID, "--size", fmt.Sprintf("%dGiB", v.SizeGiB), "--format", "raw"); err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(dir, "created"), []byte(v.ID), 0600)
}

func (d *Driver) DeleteVolume(ctx context.Context, v compute.Volume) error {
	dir := filepath.Join(d.root, "volumes", v.ID)
	if err := owneddir.Check(dir, d.owner+":"+v.ID); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	lock, err := lockVolumeCopy(ctx, dir)
	if err != nil {
		return err
	}
	defer lock.Close()
	exists, err := d.diskExists(ctx, v.ID)
	if err != nil {
		return err
	}
	if exists {
		if _, err = d.commandWithFiles(ctx, nil, []*os.File{lock}, "disk", "delete", v.ID); err != nil {
			return err
		}
		if stillExists, inspectErr := d.diskExists(ctx, v.ID); inspectErr != nil {
			return inspectErr
		} else if stillExists {
			return fmt.Errorf("the project disk is still referenced; its ownership metadata was preserved")
		}
	}
	return os.RemoveAll(dir)
}

func (d *Driver) diskExists(ctx context.Context, id string) (bool, error) {
	b, err := d.command(ctx, nil, "disk", "list", "--json")
	if err != nil {
		return false, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	found := false
	for {
		var disk struct{ Name string }
		if err = decoder.Decode(&disk); err == io.EOF {
			return found, nil
		} else if err != nil {
			return false, err
		}
		if disk.Name == "" {
			return false, fmt.Errorf("Lima disk inspection omitted its identity")
		}
		if disk.Name == id {
			found = true
		}
	}
}

// AXV checks first-use formatting during boot and again before guest startup.
// Lima's automatic formatting stays disabled to protect retained filesystems.
func (d *Driver) prepareProject(ctx context.Context, m compute.Machine, v compute.Volume, project string) error {
	if v.ID == "" {
		return nil
	}
	marker := filepath.Join(d.root, "volumes", v.ID, "initialized")
	if _, err := d.shell(ctx, nil, m, "sudo", "-n", "sh", "-c", projectScript, "axv-project", project, "false", v.ID); err != nil {
		return err
	}
	return atomicfile.Write(marker, []byte(v.ID), 0600)
}
