//go:build windows

package projectfiles

import (
	"io/fs"
	"os"
)

func rootIdentity(fs.FileInfo) (string, error) {
	return "", problem("unsupported", "Stable project directory identity is unavailable on this platform.")
}

func openNonblocking(root *os.Root, name string) (*os.File, error) { return root.Open(name) }

func exportLockContended(error) bool { return false }

func linkDirectories(*os.File, string, *os.File, string) error {
	return problem("unsupported", "Anchored project export is unavailable on this platform.")
}
