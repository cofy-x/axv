//go:build !darwin && !linux

package vmwork

import (
	"errors"
	"os"
)

func private(os.FileInfo) bool { return false }

func openPrivate(*os.Root, string) (*os.File, error) {
	return nil, errors.New("VM project agent contexts require Linux or macOS")
}
