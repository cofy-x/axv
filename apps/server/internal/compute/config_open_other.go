//go:build !unix

package compute

import "os"

func openHostConfig(root *os.Root, name string) (*os.File, error) {
	return root.Open(name)
}
