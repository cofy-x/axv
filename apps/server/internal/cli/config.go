package cli

import "github.com/cofy-x/axv/apps/server/internal/config"

type serveOptions struct {
	addr    string
	dataDir string
}

func (options serveOptions) overrides() config.Overrides {
	return config.Overrides{Addr: options.addr, DataDir: options.dataDir}
}
