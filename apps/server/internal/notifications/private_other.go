//go:build !darwin && !linux

package notifications

import "os"

func privateDirectory(string) bool              { return false }
func validatePrivateFile(string, *os.File) bool { return false }
func openPrivate(string) (*os.File, error)      { return nil, ErrStore }
