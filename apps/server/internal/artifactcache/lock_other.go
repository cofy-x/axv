//go:build !linux && !darwin

package artifactcache

import (
	"fmt"
	"os"
)

func owned(os.FileInfo) bool      { return true }
func singleLink(os.FileInfo) bool { return true }
func tryLock(*os.File) (bool, error) {
	return false, fmt.Errorf("shared VM artifact caching requires macOS or Linux")
}
