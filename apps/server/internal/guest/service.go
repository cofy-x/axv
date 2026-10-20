// Package guest owns execution inside a Linux VM. It cannot access host resources.
package guest

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/guest/internal/access"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

type service struct {
	config         protocol.Config
	bootID         string
	instanceID     string
	projectFilesMu sync.Mutex
	terminals      *terminals
	tasks          *projectTasks
	taskError      error
	access         *access.Server
	capabilities   []string
	executions     chan struct{}
	ctx            context.Context
	cancel         context.CancelFunc
	projectReady   func() error
	shutdown       func(context.Context) error
}

// newService assembles request handling independently of guest boot discovery,
// project-mount verification and listener creation.
func newService(ctx context.Context, config protocol.Config, bootID string, capabilities []string, projectReady func() error, shutdown func(context.Context) error) *service {
	ctx, cancel := context.WithCancel(ctx)
	s := &service{
		config: config, bootID: bootID, instanceID: rand.Text(), capabilities: append([]string(nil), capabilities...),
		executions: make(chan struct{}, 8), ctx: ctx, cancel: cancel, projectReady: projectReady, shutdown: shutdown,
	}
	s.access = access.New(ctx, config.MachineID, bootID)
	s.terminals = newTerminals(s)
	return s
}

func (s *service) close() {
	s.cancel()
	s.terminals.close()
	if s.tasks != nil {
		s.tasks.close()
	}
}

func (s *service) serve(listener net.Listener, output io.Writer) error {
	defer s.close()
	defer listener.Close()
	server := &http.Server{Handler: s.handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	fmt.Fprintf(output, "AXV_GUEST_READY %s\n", s.config.MachineID)
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-s.ctx.Done():
	}
	s.close()
	shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	return server.Shutdown(shutdown)
}
