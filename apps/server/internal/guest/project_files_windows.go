//go:build windows

package guest

import (
	"os"
)

func projectFileOpen(*os.Root, string) (*os.File, error) {
	return nil, projectProblem(501, "unsupported", "The AXV guest file service requires Unix file access.")
}
