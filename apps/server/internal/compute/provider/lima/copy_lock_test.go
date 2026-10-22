package lima

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/internal/providerutil"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
)

func TestLimaCopyLockSurvivesControllerKillUntilProviderFinishes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Lima copy inheritance requires POSIX descriptors")
	}
	const helperEnv = "AXV_TEST_LIMA_COPY_LOCK_ROOT"
	if root := os.Getenv(helperEnv); root != "" {
		d := &Driver{root: root, home: root, config: compute.LimaConfig{Binary: filepath.Join(root, "limactl")}}
		lock, err := lockVolumeCopy(context.Background(), filepath.Join(root, "volumes", "target"))
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()
		ready, release := os.NewFile(3, "ready"), os.NewFile(4, "release")
		defer ready.Close()
		defer release.Close()
		if _, err := d.commandWithFiles(context.Background(), nil, []*os.File{lock, ready, release}, "disk", "import", "target", "source.raw"); err != nil {
			t.Fatal(err)
		}
		return
	}
	root := t.TempDir()
	target := compute.Volume{ID: "target", Provider: "lima", SizeGiB: 1}
	source := compute.Volume{ID: "source", Provider: "lima", SizeGiB: 1}
	dir := filepath.Join(root, "volumes", target.ID)
	if err := owneddir.Ensure(dir, "lock-test:"+target.ID); err != nil {
		t.Fatal(err)
	}
	if err := providerutil.PinJSON(filepath.Join(dir, "copy.json"), []any{source.ID, target.SizeGiB}); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(dir, "source.raw")
	if err := os.WriteFile(scratch, []byte("capture still being imported"), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "limactl")
	// The real pinned Lima import is synchronous. This process models its
	// lifetime while keeping the inherited lock and open capture alive.
	script := "#!/bin/sh\nset -eu\nexec 6<\"$LIMA_HOME/volumes/target/source.raw\"\nprintf 'ready\\n' >&4\nread -r permit <&5 || :\nprintf 'finished' > \"$LIMA_HOME/finished\"\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readyRead.Close()
	defer readyWrite.Close()
	releaseRead, releaseWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer releaseRead.Close()
	defer releaseWrite.Close()
	command := exec.Command(os.Args[0], "-test.run=^TestLimaCopyLockSurvivesControllerKillUntilProviderFinishes$")
	command.Env = append(os.Environ(), helperEnv+"="+root)
	command.ExtraFiles = []*os.File{readyWrite, releaseRead}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	readyWrite.Close()
	releaseRead.Close()
	if err := readyRead.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	ready := make([]byte, len("ready\n"))
	if _, err := io.ReadFull(readyRead, ready); err != nil || string(ready) != "ready\n" {
		t.Fatalf("provider did not acquire inherited descriptors: %q %v", ready, err)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("controller was not killed")
	}
	waited = true
	d := &Driver{root: root, home: root, owner: "lock-test", config: compute.LimaConfig{Binary: binary}}
	for _, attempt := range []func(context.Context) error{
		func(ctx context.Context) error { return d.CopyVolume(ctx, source, target) },
		func(ctx context.Context) error { return d.DeleteVolume(ctx, target) },
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		err := attempt(ctx)
		cancel()
		if compute.ErrorCode(err) != "provider_pending" || !strings.Contains(err.Error(), "still locked") {
			t.Fatalf("a surviving import did not prevent storage mutation: %v", err)
		}
	}
	if data, err := os.ReadFile(scratch); err != nil || string(data) != "capture still being imported" {
		t.Fatal("retry removed a surviving import's capture", err)
	}
	releaseWrite.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		lock, err := tryVolumeCopyLock(dir)
		if err == nil {
			lock.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completed provider did not release its inherited lock", err)
		}
		time.Sleep(time.Millisecond)
	}
	if data, err := os.ReadFile(filepath.Join(root, "finished")); err != nil || string(data) != "finished" {
		t.Fatal("lock released before provider completed its storage work", err)
	}
}

func TestLimaLockedMutationFinishesBeforeCancellationReturns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Lima copy inheritance requires POSIX descriptors")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "limactl")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nset -eu\nprintf 'ready\\n' >&4\nread -r permit <&5 || :\nprintf 'finished'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := lockVolumeCopy(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readyRead.Close()
	defer readyWrite.Close()
	releaseRead, releaseWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer releaseRead.Close()
	defer releaseWrite.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	d := &Driver{root: root, home: root, config: compute.LimaConfig{Binary: binary}}
	go func() {
		_, err := d.commandWithFiles(ctx, nil, []*os.File{lock, readyWrite, releaseRead}, "disk", "import")
		result <- err
	}()
	if err := readyRead.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(readyRead, make([]byte, len("ready\n"))); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-result:
		t.Fatalf("cancellation abandoned a running storage mutation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	releaseWrite.Close()
	select {
	case <-result:
	case <-time.After(10 * time.Second):
		t.Fatal("completed storage mutation did not return")
	}
}
