package codex

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
}
