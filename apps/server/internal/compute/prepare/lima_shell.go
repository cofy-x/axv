package prepare

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

var validMachineID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type limaRuntimeState struct {
	MachineID string `json:"machineId"`
	RuntimeID string `json:"runtimeId,omitempty"`
}

// LimaShell resolves the exact local limactl shell command for a machine managed
// by this profile's Lima host. Callers must enforce local-owner-only access so
// host-specific paths are never exposed over remote transports.
func (e *Engine) LimaShell(_ context.Context, machineID string) (LimaShellInfo, error) {
	if !validMachineID.MatchString(machineID) || machineID == "." || machineID == ".." {
		return LimaShellInfo{}, problem(KindInvalidInput, "invalid_machine_id", "Select a valid VM identifier.")
	}
	p, err := profile.Read(e.options.Home)
	if err != nil {
		return LimaShellInfo{}, problem(KindConflict, "configuration_invalid", "%s", err.Error())
	}
	if p.Compute.Mode != "managed" || p.Compute.HostConfig == "" {
		return LimaShellInfo{}, problem(KindNotFound, "lima_not_managed", "This profile does not manage a local Lima VM host.")
	}
	cfg, err := compute.LoadConfig(p.Compute.HostConfig)
	if err != nil {
		return LimaShellInfo{}, problem(KindConflict, "host_configuration_invalid", "%s", err.Error())
	}
	if cfg.Lima == nil || cfg.Lima.Binary == "" {
		return LimaShellInfo{}, problem(KindNotFound, "lima_not_configured", "This host configuration does not use Lima.")
	}
	limaRoot := filepath.Join(cfg.DataDir, "lima-runtime")
	limaHome := filepath.Join(limaRoot, "home")
	if cfg.Lima.Home != "" {
		limaHome = filepath.Clean(cfg.Lima.Home)
	}
	machineDir := filepath.Join(limaRoot, machineID)
	info, err := os.Stat(machineDir)
	if err != nil || !info.IsDir() {
		return LimaShellInfo{}, problem(KindNotFound, "machine_not_found", "No local Lima runtime state was found for this VM.")
	}
	instance := machineID
	var st limaRuntimeState
	statePath := filepath.Join(machineDir, "state.json")
	if readErr := jsonfile.Read(statePath, &st); readErr == nil {
		if st.MachineID != "" && st.MachineID != machineID {
			return LimaShellInfo{}, problem(KindConflict, "runtime_mismatch", "Lima runtime state does not match the requested VM.")
		}
		if st.RuntimeID != "" {
			if strings.ContainsAny(st.RuntimeID, "/\\\x00") || st.RuntimeID == "." || st.RuntimeID == ".." {
				return LimaShellInfo{}, problem(KindConflict, "runtime_invalid", "Lima runtime state contains an invalid instance identifier.")
			}
			instance = st.RuntimeID
		}
	} else if !os.IsNotExist(readErr) {
		return LimaShellInfo{}, problem(KindConflict, "runtime_invalid", "Could not read Lima runtime state for this VM.")
	}
	cmd := "LIMA_HOME=" + shellQuote(limaHome) + " " + shellQuote(cfg.Lima.Binary) + " shell " + shellQuote(instance)
	return LimaShellInfo{
		Binary:   cfg.Lima.Binary,
		LimaHome: limaHome,
		Instance: instance,
		Command:  cmd,
	}, nil
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	safe := true
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '/' || c == '.' || c == '_' || c == '-' || c == ':' || c == '+') {
			safe = false
			break
		}
	}
	if safe {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
