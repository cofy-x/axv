//go:build linux

package firecracker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
)

func TestOfflineVolumeCopyPreservesDataAndRecoversWithoutReplacingCompletedCopy(t *testing.T) {
	d := &Driver{root: t.TempDir(), owner: "copy-host"}
	source := compute.Volume{ID: "volume-source", Provider: "firecracker", SizeGiB: 1}
	target := compute.Volume{ID: "volume-backup", Provider: "firecracker", SizeGiB: 1}
	dir := filepath.Join(d.root, "volumes", source.ID)
	if err := owneddir.Ensure(dir, d.owner+":"+source.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "created"), []byte(source.ID), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "disk.ext4"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(1 << 30); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("retained Git and untracked work"), 4096); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.CopyVolume(context.Background(), source, target); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(d.root, "volumes", target.ID, "disk.ext4")
	info, err := os.Stat(file)
	if err != nil || info.Size() != 1<<30 {
		t.Fatalf("copy size: %v %v", info, err)
	}
	original, _ := os.Stat(filepath.Join(dir, "disk.ext4"))
	if os.SameFile(original, info) {
		t.Fatal("copy aliases writable source storage")
	}
	copy, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, len("retained Git and untracked work"))
	if _, err := copy.ReadAt(data, 4096); err != nil || string(data) != "retained Git and untracked work" {
		t.Fatalf("copy lost data: %q %v", data, err)
	}
	copy.Close()
	if err := d.CopyVolume(context.Background(), source, target); err != nil {
		t.Fatal(err)
	}
	again, _ := os.Stat(file)
	if !os.SameFile(info, again) {
		t.Fatal("recovery replaced a completed copy")
	}
	// Same-size changes invalidate replay and immutable backup restoration.
	changed, err := os.OpenFile(file, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := changed.WriteAt([]byte("corruption"), 4096); err != nil {
		t.Fatal(err)
	}
	changed.Close()
	if err := d.CopyVolume(context.Background(), source, target); err == nil {
		t.Fatal("same-size corruption was accepted during completion replay")
	}
	target.Backup = &compute.ProjectVolumeBackup{SourceVolumeID: source.ID}
	restored := compute.Volume{ID: "volume-restored", Provider: "firecracker", SizeGiB: 1, Backup: target.Backup, RestoredFrom: target.ID}
	if err := d.CopyVolume(context.Background(), target, restored); err == nil {
		t.Fatal("corrupted immutable backup was restored")
	}
	if _, err := os.Stat(filepath.Join(d.root, "volumes", restored.ID)); !os.IsNotExist(err) {
		t.Fatal("restoring corrupt backup created target storage")
	}
	if current, _ := os.Stat(file); !os.SameFile(info, current) {
		t.Fatal("corrupt copy was silently replaced")
	}
	// Once restored as working storage, legitimate edits are a fresh backup
	// source; its historical restore digest must not reject that new snapshot.
	working := target
	working.RestoredFrom = "an-earlier-backup"
	newBackup := compute.Volume{ID: "volume-new-backup", Provider: "firecracker", SizeGiB: 1}
	if err := d.CopyVolume(context.Background(), working, newBackup); err != nil {
		t.Fatalf("legitimate restored working changes could not be backed up: %v", err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := d.CopyVolume(context.Background(), source, target); err == nil {
		t.Fatal("missing completed copy was silently recreated")
	}
}

func TestOfflineVolumeCopyReclaimsKilledCaptureWithoutAccumulatingScratch(t *testing.T) {
	const helperEnv = "AXV_TEST_KILLED_FIRECRACKER_COPY"
	root := os.Getenv(helperEnv)
	if root != "" {
		d := &Driver{root: root, owner: "killed-copy-host"}
		if err := d.CopyVolume(context.Background(), compute.Volume{ID: "source", Provider: "firecracker", SizeGiB: 1}, compute.Volume{ID: "target", Provider: "firecracker", SizeGiB: 1}); err != nil {
			t.Fatal(err)
		}
		return
	}
	root = t.TempDir()
	d := &Driver{root: root, owner: "killed-copy-host"}
	source := compute.Volume{ID: "source", Provider: "firecracker", SizeGiB: 1}
	target := compute.Volume{ID: "target", Provider: "firecracker", SizeGiB: 1}
	dir := filepath.Join(root, "volumes", source.ID)
	if err := owneddir.Ensure(dir, d.owner+":"+source.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "created"), []byte(source.ID), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "disk.ext4"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(1 << 30); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("retained source"), 0); err != nil {
		t.Fatal(err)
	}
	f.Close()
	targetDir := filepath.Join(root, "volumes", target.ID)
	scratch := filepath.Join(targetDir, "copy-disk.ext4")
	for attempt := 0; attempt < 2; attempt++ {
		command := exec.Command(os.Args[0], "-test.run=^TestOfflineVolumeCopyReclaimsKilledCaptureWithoutAccumulatingScratch$")
		command.Env = append(os.Environ(), helperEnv+"="+root)
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		// Each real capture must replace the previous scratch inode before it
		// writes. Kill that process so its normal deferred cleanup cannot run.
		deadline := time.Now().Add(10 * time.Second)
		for {
			info, statErr := os.Lstat(scratch)
			if statErr == nil && info.ModTime().After(time.Now().Add(-2*time.Second)) {
				if err := command.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				break
			}
			if time.Now().After(deadline) {
				_ = command.Process.Kill()
				_ = command.Wait()
				t.Fatal("copy helper did not create its reserved staging file")
			}
			time.Sleep(time.Millisecond)
		}
		if err := command.Wait(); err == nil {
			t.Fatal("capture helper was not killed")
		}
		if _, err := os.Stat(scratch); err != nil {
			t.Fatalf("kill did not retain recoverable scratch: %v", err)
		}
		// A stale timestamp distinguishes the next attempt from this inode.
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(scratch, old, old); err != nil {
			t.Fatal(err)
		}
	}
	unknown := filepath.Join(targetDir, ".copy-unrecognized")
	if err := os.WriteFile(unknown, []byte("preserve unknown storage"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.CopyVolume(context.Background(), source, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(scratch); !os.IsNotExist(err) {
		t.Fatal("successful retry left its staging file", err)
	}
	if data, err := os.ReadFile(unknown); err != nil || string(data) != "preserve unknown storage" {
		t.Fatal("recovery changed unrelated files", err)
	}
}

func TestOfflineVolumeCopyRefusesLegacyCompletionRecord(t *testing.T) {
	d := &Driver{root: t.TempDir(), owner: "copy-host"}
	source := compute.Volume{ID: "legacy-source", Provider: "firecracker"}
	target := compute.Volume{ID: "legacy-target", Provider: "firecracker"}
	for _, v := range []compute.Volume{source, target} {
		dir := filepath.Join(d.root, "volumes", v.ID)
		if err := owneddir.Ensure(dir, d.owner+":"+v.ID); err != nil {
			t.Fatal(err)
		}
		for name, data := range map[string]string{"created": v.ID, "disk.ext4": ""} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	marker := filepath.Join(d.root, "volumes", target.ID, "copy-complete")
	if err := os.WriteFile(marker, []byte(target.ID), 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.CopyVolume(context.Background(), source, target); err == nil {
		t.Fatal("legacy completion was adopted")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != target.ID {
		t.Fatalf("legacy completion was reset: %q %v", data, err)
	}
}
