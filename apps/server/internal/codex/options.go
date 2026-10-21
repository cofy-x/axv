package codex

import (
	"context"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
	"github.com/cofy-x/axv/apps/server/internal/vmwork"
)

// VMProjectResolution captures a verified compute connection independently of
// the agent's host process, provider history and working directory.
type VMProjectResolution struct {
	Project                   vmwork.Project
	Socket, ConfigFingerprint string
}

// Options contains resolved inputs supplied by the desktop composition root.
// Codex does not resolve profiles or depend on desktop listener configuration.
type Options struct {
	DataDir          string
	ProfileHome      string
	ConfigPath       string
	WorkspaceDir     string
	ProxyURL         string
	TmuxSocket       string
	RunnerExecutable string
	Logger           *diagnostics.Logger
	ResolveVMProject func(context.Context, compute.ProjectBinding) (VMProjectResolution, error)
}
