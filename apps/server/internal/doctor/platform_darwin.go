//go:build darwin

package doctor

import (
	"context"
	"fmt"
	"path/filepath"
)

func checkPlatform(_ context.Context, r *Report, home, _ string) {
	// macOS Unix domain sockets have a strict 104-byte path limit; warn early
	// if AXV_HOME leaves too little room for Lima instance sockets.
	limaRoot := filepath.Join(home, "lima")
	if len(limaRoot)+48 > 104 {
		r.add(Check{
			ID:       "darwin_socket_path",
			Category: "vm",
			Label:    "macOS Unix socket path length",
			Status:   StatusWarn,
			Detail:   fmt.Sprintf("Profile Lima path %s (%d bytes) may exceed macOS's 104-byte Unix socket limit.", limaRoot, len(limaRoot)),
			Fix:      "Use a shorter AXV_HOME (e.g. /tmp/axv or ~/.axv) or pass --lima-home /tmp/axv-lima to `axv setup`.",
		})
	} else {
		r.add(Check{
			ID:       "darwin_socket_path",
			Category: "vm",
			Label:    "macOS Unix socket path length",
			Status:   StatusOK,
			Detail:   fmt.Sprintf("Lima state root %s fits within macOS socket limits.", limaRoot),
		})
	}
}
