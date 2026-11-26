package doctor

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

// macOSSocketPathFits mirrors compute/prepare.shortLimaHome so doctor and setup
// apply the exact same 104-byte sockaddr_un threshold.
func macOSSocketPathFits(limaRoot string) bool {
	sample := filepath.Join(limaRoot, "vm-"+strings.Repeat("0", 32), "ssh.sock."+strings.Repeat("0", 16))
	return len(sample) < 104
}

func checkDarwinLimaPath(r *Report, home string) {
	limaRoot := filepath.Join(home, "lima")
	configured := false

	if p, err := profile.Read(home); err == nil {
		switch p.Compute.Mode {
		case "external":
			r.add(Check{
				ID:       "darwin_socket_path",
				Category: "vm",
				Label:    "macOS Unix socket path length",
				Status:   StatusOK,
				Detail:   fmt.Sprintf("External VM socket configured (%s); local Lima state directory is unused.", p.Compute.Socket),
			})
			return
		case "managed":
			if p.Compute.HostConfig != "" {
				if cfg, cfgErr := compute.LoadConfig(p.Compute.HostConfig); cfgErr == nil && cfg.Lima != nil && cfg.Lima.Home != "" {
					limaRoot = cfg.Lima.Home
					configured = true
				}
			}
		}
	}

	if macOSSocketPathFits(limaRoot) {
		detail := fmt.Sprintf("Lima state directory %s (%d bytes) fits within macOS's 104-byte Unix socket limit.", limaRoot, len(limaRoot))
		if configured {
			detail = fmt.Sprintf("Configured Lima state directory %s (%d bytes) fits within macOS's 104-byte Unix socket limit.", limaRoot, len(limaRoot))
		}
		r.add(Check{
			ID:       "darwin_socket_path",
			Category: "vm",
			Label:    "macOS Unix socket path length",
			Status:   StatusOK,
			Detail:   detail,
		})
		return
	}

	detail := fmt.Sprintf("Default Lima path %s (%d bytes) may exceed macOS's 104-byte Unix socket limit.", limaRoot, len(limaRoot))
	fix := "Use a shorter AXV_HOME (e.g. ~/.axv) or pass --lima-home <short-path> to `axv setup`."
	if configured {
		detail = fmt.Sprintf("Configured Lima state directory %s (%d bytes) may exceed macOS's 104-byte Unix socket limit.", limaRoot, len(limaRoot))
		fix = "Reconfigure with a shorter --lima-home path."
	}
	r.add(Check{
		ID:       "darwin_socket_path",
		Category: "vm",
		Label:    "macOS Unix socket path length",
		Status:   StatusWarn,
		Detail:   detail,
		Fix:      fix,
	})
}
