package lima

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/filelock"
)

// lockVolumeCopy remains open in each mutating limactl process as well as the
// controller. If the controller is killed, retry must wait until that command
// finishes before it can reclaim this intent's staging or imported disk.
func lockVolumeCopy(ctx context.Context, dir string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		lock, err := tryVolumeCopyLock(dir)
		if err == nil || !copyLockContended(err) {
			return lock, err
		}
		select {
		case <-ctx.Done():
			// Report pending only after confirming an earlier writer still owns
			// this intent. Other I/O errors retain their ordinary failure path.
			lock, err := tryVolumeCopyLock(dir)
			if err == nil {
				lock.Close()
			} else if copyLockContended(err) && errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, compute.Problem("provider_pending", "The Lima copy intent is still locked by a previous provider command; keep this operation and wait for that writer to finish.")
			} else if !copyLockContended(err) {
				return nil, err
			}
			return nil, ctx.Err()
		case <-tick.C:
		}
	}
}

func copyLockContended(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

func tryVolumeCopyLock(dir string) (*os.File, error) {
	name := filepath.Join(dir, "copy.lock")
	if info, err := os.Lstat(name); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("the Lima copy lock must be a regular owned file")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	lock, err := filelock.TryLock(name)
	if err != nil {
		return nil, fmt.Errorf("the Lima copy intent is still locked; retry after its previous provider command finishes: %w", err)
	}
	return lock, nil
}
