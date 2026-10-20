//go:build !linux

package guest

import (
	"errors"
	"os"
)

func systemTaskGroups() (taskGroups, error) {
	return nil, errors.New("project tasks require a Linux guest with delegated cgroup v2")
}
func checkTaskState(string, string, uint32) error {
	return errors.New("Linux task state is unavailable")
}
func openTaskRegular(string, int) (*os.File, error) {
	return nil, errors.New("Linux task state is unavailable")
}
func openTaskLog(string, string, bool) (*os.File, error) {
	return nil, errors.New("Linux task logs are unavailable")
}
func taskCommandDirectory(*os.File) string { return "" }
func taskFreeSpace(string, *os.Root, int64) error {
	return errors.New("Linux task storage is unavailable")
}
