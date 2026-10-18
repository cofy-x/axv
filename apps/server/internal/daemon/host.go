package daemon

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/app"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/profile"
	"github.com/cofy-x/axv/apps/server/internal/profile/logs"
)

// EnsureHost serves both VM commands and background desktop activation. Its job
// record is published before launch, independently of desktop ownership.
func EnsureHost(ctx context.Context, s app.Settings, output io.Writer) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	supervisor, err := nativeSupervisor()
	if err != nil {
		return nil, err
	}
	return ensureHost(ctx, s, output, supervisor)
}

func ensureHost(ctx context.Context, s app.Settings, output io.Writer, supervisor supervisor) (func(), error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if s.Host == nil || s.Compute.Mode != "managed" {
		return nil, fmt.Errorf("managed host configuration is required")
	}
	lock, err := commandLock(ctx, s.Home)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if s.Compute.Socket != s.Host.Socket {
		return nil, fmt.Errorf("managed host socket does not match its configuration")
	}
	if err := s.Prepare(); err != nil {
		return nil, err
	}
	m, err := readManifest(s.Home)
	if os.IsNotExist(err) {
		m = manifest{Version: 4, Home: s.Home}
	} else if err != nil {
		return nil, err
	}
	job := m.job("host")
	if job != nil && (m.OwnedHost == nil || m.OwnedHost.Fingerprint() != s.Host.Fingerprint()) {
		return nil, fmt.Errorf("the owned VM service uses different settings; existing provider and storage ownership must be preserved")
	}
	if _, err := app.HostStatus(ctx, s); err == nil {
		return nil, nil
	}
	// A service may still be recovering or may belong to another launcher,
	// including after our recorded native job was stopped. An occupied socket
	// never authorizes starting another host or replacing its ownership.
	if conn, dialErr := net.DialTimeout("unix", s.Compute.Socket, 250*time.Millisecond); dialErr == nil {
		conn.Close()
		lock.Close()
		return nil, waitHostContext(ctx, s)
	}
	var binary string
	if job == nil {
		binary, err = pinHostExecutable(s)
		if err != nil {
			return nil, err
		}
		created, createErr := hostJob(s, binary)
		if createErr != nil {
			return nil, createErr
		}
		m.OwnedHost = s.Host
		m.Jobs = append([]Job{created}, m.Jobs...)
		if err = jsonfile.Write(manifestPath(s.Home), m, 0600); err != nil {
			return nil, err
		}
		job = &m.Jobs[0]
	}
	loaded, err := supervisor.Loaded(ctx, *job)
	if err != nil {
		return nil, err
	}
	if !loaded {
		if binary == "" {
			binary, err = pinHostExecutable(s)
			if err != nil {
				return nil, err
			}
		}
		if binary != job.Args[0] {
			// Only an unloaded job may adopt the invoking executable. Keep its
			// verified provider/storage configuration and publish the new owner
			// before launch; live hosts retain their original executable.
			replacement, err := hostJob(s, binary)
			if err != nil {
				return nil, err
			}
			*job = replacement
			if err := jsonfile.Write(manifestPath(s.Home), m, 0600); err != nil {
				return nil, err
			}
		}
		if err = supervisor.Start(ctx, *job); err != nil {
			return nil, err
		}
	}
	lock.Close()
	if err = waitHostContext(ctx, s); err != nil {
		return nil, fmt.Errorf("%w; see %s", err, job.Log)
	}
	return nil, nil
}

func pinHostExecutable(s app.Settings) (string, error) {
	executable := s.RunnerExecutable
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return "", err
		}
	}
	binary, err := profile.PinExecutable(s.Home, executable)
	if err != nil {
		return "", fmt.Errorf("pin AXV host executable: %w", err)
	}
	return binary, nil
}

func hostJob(s app.Settings, binary string) (Job, error) {
	if err := logs.PrepareLegacy(s.Home, "host"); err != nil {
		return Job{}, err
	}
	marker := filepath.Join(s.RunDir(), compute.NewID("host")+".json")
	if err := jsonfile.Write(marker, s.Host, 0600); err != nil {
		return Job{}, err
	}
	label := jobLabel(s.Home, "host", marker)
	job := Job{Role: "host", Label: label, Args: []string{binary, "host", "--config", marker}, Marker: marker, Definition: filepath.Join(s.RunDir(), label+".plist"), Log: filepath.Join(s.Home, "logs", "host.log"), Environment: environment(s.Home)}
	return job, nil
}

func commandLock(ctx context.Context, home string) (*os.File, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lock, err := profile.Lock(home)
		if err == nil {
			return lock, nil
		}
		if os.IsPermission(err) || os.IsNotExist(err) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func waitHostContext(ctx context.Context, s app.Settings) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var last error
	for ctx.Err() == nil {
		if _, last = app.HostStatus(ctx, s); last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	if ctx.Err() != nil {
		return fmt.Errorf("compute host did not become ready: %w (%v)", ctx.Err(), last)
	}
	return last
}
