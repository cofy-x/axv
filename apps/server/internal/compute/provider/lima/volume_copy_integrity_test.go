package lima

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/internal/providerutil"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
)

func TestLimaRestoreVerifiesImmutableBackupDigestBeforeCopying(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake limactl requires POSIX shell")
	}
	root := t.TempDir()
	home, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	source := compute.Volume{ID: "backup-volume", Provider: "lima", SizeGiB: 1, Backup: &compute.ProjectVolumeBackup{SourceVolumeID: "original-volume"}}
	target := compute.Volume{ID: "restored-volume", Provider: "lima", SizeGiB: 1, Backup: source.Backup, RestoredFrom: source.ID}
	disk := filepath.Join(home, "_disks", source.ID)
	if err := os.MkdirAll(disk, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(disk, "datadisk"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(1 << 30); err != nil {
		t.Fatal(err)
	}
	f.Close()
	dir := filepath.Join(root, "volumes", source.ID)
	if err := owneddir.Ensure(dir, "test-host:"+source.ID); err != nil {
		t.Fatal(err)
	}
	if err := providerutil.WriteCopyCompletion(filepath.Join(dir, "copy-complete"), source.Backup.SourceVolumeID, source.ID, 1, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(diskInfo{Name: source.ID, Dir: disk, Format: "raw", Size: 1 << 30})
	binary := filepath.Join(root, "limactl")
	script := "#!/bin/sh\n[ \"$*\" = '--tty=false disk list --json' ] || exit 2\nprintf '%s' '" + strings.ReplaceAll(string(data), "'", "'\\''") + "'\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	d := &Driver{config: compute.LimaConfig{Binary: binary}, root: root, home: root, owner: "test-host"}
	if err := d.CopyVolume(context.Background(), source, target); err == nil || !strings.Contains(err.Error(), "recorded checksum") {
		t.Fatalf("changed backup did not fail checksum validation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "volumes", target.ID, "source.raw")); !os.IsNotExist(err) {
		t.Fatal("changed backup began copying")
	}
}

func TestLimaCopyRejectsIdentityAliasBeforeProviderMutation(t *testing.T) {
	d := &Driver{root: t.TempDir()}
	source := compute.Volume{ID: "original-volume", Provider: "lima", SizeGiB: 1}
	for _, target := range []compute.Volume{source, {ID: "target", Provider: "other", SizeGiB: 1}, {ID: "target", Provider: "lima", SizeGiB: 0}} {
		if err := d.CopyVolume(context.Background(), source, target); err == nil || !strings.Contains(err.Error(), "independent volume identities") {
			t.Fatalf("invalid copy reached provider storage: %+v %v", target, err)
		}
	}
}
