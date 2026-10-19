//go:build !linux

package guest

import (
	"io/fs"
	"os"
)

func projectFileRootIdentity(*os.Root, fs.FileInfo) (string, error) {
	return "", projectProblem(501, "unsupported", "The AXV guest file service requires Linux filesystem file handles.")
}
