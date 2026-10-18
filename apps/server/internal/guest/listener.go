package guest

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func listenGuest(address string) (net.Listener, error) {
	var listener net.Listener
	var err error
	if address == "vsock" {
		listener, err = listenVSOCK()
	} else if strings.HasPrefix(address, "unix:") {
		path := strings.TrimPrefix(address, "unix:")
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("Unix listener path must be absolute")
		}
		if info, statErr := os.Lstat(path); statErr == nil {
			if info.Mode()&os.ModeSocket == 0 {
				return nil, fmt.Errorf("guest listener path is not a socket")
			}
			conn, dialErr := net.DialTimeout("unix", path, time.Second)
			if dialErr == nil {
				conn.Close()
				return nil, fmt.Errorf("guest socket is already in use")
			}
			if err = os.Remove(path); err != nil {
				return nil, err
			}
		} else if !os.IsNotExist(statErr) {
			return nil, statErr
		}
		listener, err = net.Listen("unix", path)
		if err == nil {
			err = os.Chmod(path, 0600)
		}
	} else {
		return nil, fmt.Errorf("guest listeners are limited to vsock and Unix sockets")
	}
	if err != nil {
		if listener != nil {
			listener.Close()
		}
		return nil, err
	}
	return listener, nil
}
