package guest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"github.com/cofy-x/axv/apps/server/internal/limitedbuffer"
)

func (s *service) execute(w http.ResponseWriter, r *http.Request) {
	var req protocol.ExecRequest
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, "invalid_request", err)
		return
	}
	if len(req.Argv) == 0 || len(req.Argv) > 128 || req.TimeoutSeconds < 0 || req.TimeoutSeconds > 300 {
		fail(w, 400, "invalid_request", fmt.Errorf("supply argv and a timeout of at most 300 seconds"))
		return
	}
	dir, err := s.directory(req.Directory)
	if err != nil {
		fail(w, 400, "invalid_directory", err)
		return
	}
	select {
	case s.executions <- struct{}{}:
		defer func() { <-s.executions }()
	default:
		fail(w, 503, "execution_limit", fmt.Errorf("guest execution capacity is full"))
		return
	}
	if req.TimeoutSeconds == 0 {
		req.TimeoutSeconds = 60
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(req.TimeoutSeconds)*time.Second)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	cmd := exec.CommandContext(ctx, req.Argv[0], req.Argv[1:]...)
	cmd.Dir = dir
	configureExecution(cmd)
	cmd.WaitDelay = 2 * time.Second
	out, diagnostic := &limitedbuffer.Buffer{Limit: 64 << 10}, &limitedbuffer.Buffer{Limit: 64 << 10}
	cmd.Stdout, cmd.Stderr = out, diagnostic
	err = cmd.Run()
	if ctx.Err() != nil {
		fail(w, 408, "execution_timeout", ctx.Err())
		return
	}
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			fail(w, 502, "execution_failed", err)
			return
		}
		code = exit.ExitCode()
	}
	respond(w, 200, protocol.ExecResult{ExitCode: code, Stdout: string(out.Data), Stderr: string(diagnostic.Data), Truncated: out.Truncated || diagnostic.Truncated})
}
