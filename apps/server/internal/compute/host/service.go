// Package host composes the single-host compute controller and drivers.
package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/control"
	computehttp "github.com/cofy-x/axv/apps/server/internal/compute/httpapi"
	"github.com/cofy-x/axv/apps/server/internal/compute/provider/firecracker"
	"github.com/cofy-x/axv/apps/server/internal/compute/provider/lima"
	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
	"github.com/cofy-x/axv/apps/server/internal/filelock"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
)

// Run starts the host using a configuration chosen by the caller.
func Run(path string, output io.Writer) (result error) {
	c, err := compute.LoadConfig(path)
	if err != nil {
		return err
	}

	logger := diagnostics.Stderr("host", os.Stderr)
	logger.Info("service.start")
	defer func() {
		if result != nil {
			logger.Error("service.failed", result)
		}
		logger.Info("service.stop")
	}()
	if err = os.MkdirAll(c.DataDir, 0700); err != nil {
		return err
	}
	lock, err := filelock.TryLock(filepath.Join(c.DataDir, "host.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	restoreLog := diagnostics.RedirectStandard(logger)
	defer restoreLog()
	store, err := control.OpenStore(c.DataDir)
	if err != nil {
		return err
	}
	defer store.Close()
	if err = store.SeedImages(context.Background(), c.Images); err != nil {
		return err
	}
	drivers := map[string]compute.Driver{}
	if c.Firecracker != nil {
		d, err := firecracker.New(c.DataDir, store.HostID, *c.Firecracker)
		if err != nil {
			return err
		}
		drivers["firecracker"] = d
	}
	if c.Lima != nil {
		d, err := lima.New(c.DataDir, store.HostID, *c.Lima)
		if err != nil {
			return err
		}
		drivers["lima"] = d
	}
	// A dedicated socket directory is also owned, so a stale socket from another
	// service is never unlinked merely because dialing it failed.
	if err = owneddir.Ensure(filepath.Dir(c.Socket), store.HostID); err != nil {
		return err
	}
	socketLock, err := filelock.TryLock(c.Socket + ".lock")
	if err != nil {
		return err
	}
	defer socketLock.Close()
	if info, err := os.Lstat(c.Socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("host socket path is occupied by a non-socket file")
		}
		conn, dialErr := net.DialTimeout("unix", c.Socket, time.Second)
		if dialErr == nil {
			conn.Close()
			return fmt.Errorf("host socket is already active")
		}
		if err = os.Remove(c.Socket); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	listener, err := net.Listen("unix", c.Socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	mode := os.FileMode(0600)
	if c.SocketGroup != nil {
		if err = os.Chown(filepath.Dir(c.Socket), os.Geteuid(), *c.SocketGroup); err != nil {
			return err
		}
		if err = os.Chmod(filepath.Dir(c.Socket), 0710); err != nil {
			return err
		}
		if err = os.Chown(c.Socket, os.Geteuid(), *c.SocketGroup); err != nil {
			return err
		}
		mode = 0660
	}
	if err = os.Chmod(c.Socket, mode); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	connections := &connectionSet{connections: map[*connection]bool{}}
	service := control.NewService(control.Options{Logger: logger, Config: c, Store: store, Drivers: drivers,
		OnReady: func() {
			logger.Info("service.ready", "hostId", store.HostID)
			fmt.Fprintf(output, "AXV_HOST_READY %s %s\n", store.HostID, c.Socket)
		},
	})
	server := &http.Server{Handler: computehttp.Handler(service), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}

	controllerDone := make(chan error, 1)
	go func() { controllerDone <- service.Run(ctx) }()
	httpDone := make(chan error, 1)
	go func() { httpDone <- server.Serve(&trackedListener{Listener: listener, owner: connections}) }()
	select {
	case err = <-controllerDone:
		cancel()
		controllerDone = nil
	case err = <-httpDone:
		cancel()
	case <-ctx.Done():
	}
	cancel()
	connections.close()
	shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	shutdownErr := server.Shutdown(shutdown)
	if controllerDone != nil {
		if controllerErr := <-controllerDone; err == nil {
			err = controllerErr
		}
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return shutdownErr
}
