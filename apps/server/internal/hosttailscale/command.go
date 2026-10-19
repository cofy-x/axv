package hosttailscale

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/limitedbuffer"
)

const maxStatusBytes = 1 << 20

func runStatus(ctx context.Context, command string) ([]byte, error) {
	return runCommand(ctx, command, []string{"status", "--json", "--peers=false"}, os.Environ())
}

func runCommand(ctx context.Context, command string, args, environment []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = statusEnvironment(environment)
	cmd.WaitDelay = 100 * time.Millisecond
	output := &limitedbuffer.Buffer{Limit: maxStatusBytes}
	cmd.Stdout, cmd.Stderr = output, io.Discard
	if err := cmd.Run(); err != nil {
		return nil, errors.New("query the host Tailscale status")
	}
	if output.Truncated {
		return nil, errors.New("host Tailscale status exceeds its size limit")
	}
	return output.Data, nil
}

func statusEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment))
	for _, value := range environment {
		key, _, _ := strings.Cut(value, "=")
		key = strings.ToUpper(key)
		// Do not let inherited auth, socket or debug knobs redirect this host
		// inspection to AXV's embedded node or change CLI behavior.
		if strings.HasPrefix(key, "TS_") || strings.HasPrefix(key, "TAILSCALE_") {
			continue
		}
		result = append(result, value)
	}
	return result
}
