// Package providerutil supplies bounded commands and resource configuration pins
// for compute providers and environment preparation.
package providerutil

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
	"github.com/cofy-x/axv/apps/server/internal/limitedbuffer"
)

func Command(ctx context.Context, name string, args ...string) ([]byte, error) {
	logger := diagnostics.FromContext(ctx).With("command", filepath.Base(name))
	logger.Info("provider.command_started")
	cmd := exec.CommandContext(ctx, name, args...)
	out := &limitedbuffer.Buffer{Limit: 64 << 10}
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	if err != nil {
		logger.Error("provider.command_failed", err, "diagnostic", strings.TrimSpace(string(out.Data)))
		return out.Data, fmt.Errorf("%s: %w: %s", filepath.Base(name), err, strings.TrimSpace(string(out.Data)))
	}
	logger.Info("provider.command_completed")
	return out.Data, nil
}
