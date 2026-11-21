//go:build linux && amd64

package hostinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/client"
)

const standardSocket = "/run/axv-host/control.sock"

// DefaultImageInstallAvailable is a display hint for the desktop account,
// which can inspect the installer record's metadata but cannot read it.
func DefaultImageInstallAvailable() bool {
	i := systemInstallation()
	for _, dir := range []string{binDir, filepath.Dir(unitFile)} {
		if err := i.directories(dir, false); err != nil {
			return false
		}
	}
	path := i.path(completed)
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm() == 0600 &&
		i.trusted(path, info) == nil && i.agrees(unitFile, unit, 0644, true) == nil
}

func InstallImage(ctx context.Context, expectedHostID string, out io.Writer) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("run host install-image with sudo on the AXV host")
	}
	return systemInstallation().installImage(ctx, expectedHostID, out)
}

func (i installation) installedConfig(ctx context.Context) (compute.Config, []byte, error) {
	for _, dir := range []string{binDir, filepath.Dir(configFile), filepath.Dir(unitFile)} {
		if err := i.directories(dir, false); err != nil {
			return compute.Config{}, nil, err
		}
	}
	done, err := i.marker(completed)
	if err != nil {
		return compute.Config{}, nil, err
	}
	if !done {
		return compute.Config{}, nil, fmt.Errorf("this command requires the default service created by axv host install-service")
	}
	if err = i.agrees(unitFile, unit, 0644, true); err != nil {
		return compute.Config{}, nil, err
	}
	_, active, err := i.unitState(ctx)
	if err != nil {
		return compute.Config{}, nil, err
	}
	if !active {
		return compute.Config{}, nil, fmt.Errorf("start the installed service with sudo systemctl start %s, then run this command again", unitName)
	}
	raw, err := i.read(configFile, 0600)
	if err != nil {
		return compute.Config{}, nil, err
	}
	cfg, err := compute.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return compute.Config{}, nil, err
	}
	if cfg.Socket != standardSocket || cfg.Firecracker == nil || cfg.Firecracker.Version != firecrackerVersion {
		return compute.Config{}, nil, fmt.Errorf("the default image requires the installed Firecracker %s service on %s", firecrackerVersion, standardSocket)
	}
	return cfg, raw, nil
}

func hasImage(info compute.Info, image compute.Image) bool {
	for _, entry := range info.Images {
		if entry.ID == image.ID && entry.SHA256 == image.RootfsSHA256 && (entry.Status == "configured" || entry.Status == "ready") {
			return true
		}
	}
	return false
}

func (i installation) installImage(ctx context.Context, expectedHostID string, out io.Writer) error {
	lock, err := i.lock()
	if err != nil {
		return err
	}
	defer lock.Close()
	current, raw, err := i.installedConfig(ctx)
	if err != nil {
		return err
	}
	next := current
	index := slices.IndexFunc(current.Images, func(entry compute.Image) bool { return entry.ID == i.image.ID })
	if index == -1 {
		next.Images = append(slices.Clone(current.Images), i.image)
	} else if !reflect.DeepEqual(current.Images[index], i.image) {
		return fmt.Errorf("the default image ID is already used by a different configuration; preserving it")
	}
	previous := current
	if index != -1 {
		// A previous invocation may have saved the catalog and been interrupted
		// before restarting. Accept only the exact same config without this image.
		previous.Images = slices.Delete(slices.Clone(current.Images), index, index+1)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	info, err := i.readHost(probeCtx, current.Socket, client.Binding{HostID: expectedHostID})
	cancel()
	if err != nil {
		return fmt.Errorf("verify the installed VM host: %w", err)
	}
	if info.ConfigFingerprint != current.Fingerprint() && info.ConfigFingerprint != previous.Fingerprint() {
		return fmt.Errorf("the running VM service uses a different configuration; preserving it")
	}
	if err = firecrackerReady(info); err != nil {
		return err
	}
	if info.ConfigFingerprint == next.Fingerprint() && !hasImage(info, i.image) {
		return fmt.Errorf("the default image is already in %s but is missing from the host catalog", configFile)
	}
	if err = i.stageGuest(ctx, out); err != nil {
		return err
	}
	if info.ConfigFingerprint != next.Fingerprint() {
		// Revalidate after the long download or an administrator's edit.
		if _, _, err = i.installedConfig(ctx); err != nil {
			return err
		}
		if err = i.agrees(configFile, raw, 0600, true); err != nil {
			return err
		}
		bound := client.Binding{HostID: info.HostID, ConfigFingerprint: info.ConfigFingerprint}
		probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		_, err = i.readHost(probeCtx, current.Socket, bound)
		cancel()
		if err != nil {
			return fmt.Errorf("the VM host changed during installation: %w", err)
		}
		if err = i.recordImageReload(ImageReload{
			HostID: bound.HostID, Previous: bound.ConfigFingerprint, Fingerprint: next.Fingerprint(),
			ImageID: i.image.ID, StartedAt: time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("record the image reload: %w", err)
		}
		if index == -1 {
			data, err := json.MarshalIndent(next, "", "  ")
			if err != nil {
				return err
			}
			if err = atomicfile.Write(i.path(configFile), append(data, '\n'), 0600); err != nil {
				return err
			}
		}
		fmt.Fprintln(out, "Reloading axv-host.service with the default VM image…")
		if _, err = i.systemctl(ctx, "--system", "try-restart", unitName); err != nil {
			return fmt.Errorf("image registration was saved; inspect systemctl status %s --no-pager: %w", unitName, err)
		}
		if info, err = i.waitHost(ctx, next, bound.HostID); err != nil {
			return fmt.Errorf("verify the updated VM service: %w", err)
		}
		if !hasImage(info, i.image) {
			return fmt.Errorf("the VM service did not register the default image")
		}
	}
	fmt.Fprintln(out, "Default VM image is ready. Virtual Machines will refresh when the service reconnects.")
	return nil
}
