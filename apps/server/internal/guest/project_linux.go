//go:build linux

package guest

import (
	"fmt"
	"syscall"
)

func projectVolumeReady(projectRoot string) error {
	var root, project syscall.Stat_t
	if err := syscall.Stat("/", &root); err != nil {
		return err
	}
	if err := syscall.Stat(projectRoot, &project); err != nil {
		return fmt.Errorf("project volume is not mounted")
	}
	if root.Dev == project.Dev {
		return fmt.Errorf("project directory is on the root disk; expected a separate mounted volume")
	}
	if err := syscall.Access(projectRoot, 2); err != nil {
		return fmt.Errorf("project volume is not writable by the guest user")
	}
	return nil
}
