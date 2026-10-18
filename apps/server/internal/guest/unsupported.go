//go:build !linux

package guest

import (
	"fmt"
	"io"
	"net"
)

func Run([]string, io.Writer) error { return fmt.Errorf("the AXV guest service requires Linux") }

func RunOptions(Options, io.Writer) error { return fmt.Errorf("the AXV guest service requires Linux") }

func listenVSOCK() (net.Listener, error) {
	return nil, fmt.Errorf("the AXV guest service requires Linux")
}
