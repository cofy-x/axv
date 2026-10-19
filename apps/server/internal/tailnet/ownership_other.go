//go:build !darwin && !linux

package tailnet

import "os"

func ownedFile(os.FileInfo) bool  { return false }
func singleLink(os.FileInfo) bool { return false }
