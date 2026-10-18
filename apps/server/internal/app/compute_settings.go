package app

import (
	"fmt"
	"os"

	"github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

// ResolveCompute reads VM service settings independently of desktop storage,
// listeners and agent configuration. It never initializes the profile or services.
func ResolveCompute(home string) (Settings, error) {
	p, err := profile.ReadCompute(home)
	if err != nil {
		return Settings{}, err
	}
	s := resolveCompute(Settings{Inputs: launch.Inputs{Home: home, ComputeOverride: os.Getenv("AXV_COMPUTE_SOCKET")}, ComputeSettings: ComputeSettings{Compute: p.Compute, ComputeError: p.ComputeError}})
	if s.ComputeError != "" {
		return s, fmt.Errorf("%s", s.ComputeError)
	}
	return s, nil
}
