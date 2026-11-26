package cli

import (
	"context"
	"fmt"
	"io"
	"runtime"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/hostinstall"
	"github.com/cofy-x/axv/apps/server/internal/daemon"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

// hostStatusReport is the cross-platform JSON payload for `axv host status --json`.
type hostStatusReport struct {
	Supported        bool                 `json:"supported"`
	Platform         string               `json:"platform"`
	Mode             string               `json:"mode,omitempty"`
	Installed        bool                 `json:"installed"`
	Enabled          bool                 `json:"enabled"`
	Active           bool                 `json:"active"`
	State            string               `json:"state"`
	Socket           string               `json:"socket,omitempty"`
	SocketReachable  bool                 `json:"socketReachable"`
	HostID           string               `json:"hostId,omitempty"`
	Registered       *bool                `json:"registered,omitempty"`
	InstalledBinary  string               `json:"installedBinary,omitempty"`
	InstalledVersion string               `json:"installedVersion,omitempty"`
	CurrentVersion   string               `json:"currentVersion,omitempty"`
	VersionDrift     bool                 `json:"versionDrift,omitempty"`
	Providers        []compute.Capability `json:"providers,omitempty"`
	Error            string               `json:"error,omitempty"`
}

func hostStatusFromLinux(st hostinstall.ServiceStatus) hostStatusReport {
	return hostStatusReport{
		Supported:        st.Supported,
		Platform:         "linux",
		Mode:             "system",
		Installed:        st.Installed,
		Enabled:          st.Enabled,
		Active:           st.Active,
		State:            st.State,
		Socket:           st.Socket,
		SocketReachable:  st.SocketReachable,
		InstalledBinary:  st.InstalledBinary,
		InstalledVersion: st.InstalledVersion,
		CurrentVersion:   st.CurrentVersion,
		VersionDrift:     st.VersionDrift,
	}
}

func hostStatusFromDaemon(platform, version string, report daemon.StatusReport) hostStatusReport {
	vm := report.VMHost
	if platform != "darwin" {
		return hostStatusReport{
			Supported:      false,
			Platform:       platform,
			State:          "unsupported",
			CurrentVersion: version,
		}
	}
	res := hostStatusReport{
		Supported:       true,
		Platform:        platform,
		Mode:            vm.Mode,
		Socket:          vm.Socket,
		SocketReachable: vm.Ready,
		Active:          vm.Ready,
		HostID:          vm.HostID,
		Registered:      vm.Registered,
		CurrentVersion:  version,
		Providers:       vm.Providers,
		Error:           vm.Error,
	}
	switch {
	case vm.Mode == "" || vm.Mode == "disabled":
		res.Mode = "disabled"
		res.Installed = false
		res.State = "not_configured"
	case vm.Ready:
		res.Installed = true
		res.State = "active"
	default:
		res.Installed = true
		res.State = "inactive"
	}
	return res
}

func runHostStatus(ctx context.Context, version string, asJSON bool, out io.Writer) error {
	if runtime.GOOS == "linux" {
		st := hostinstall.Status(ctx, version)
		if asJSON {
			return jsonResult(out, hostStatusFromLinux(st))
		}
		fmt.Fprintf(out, "Service: %s (%s, boot autostart: %t)\n", hostinstall.UnitName, st.State, st.Enabled)
		fmt.Fprintf(out, "Socket: %s (reachable: %t)\n", st.Socket, st.SocketReachable)
		if st.InstalledVersion != "" {
			fmt.Fprintf(out, "Installed version: %s (current CLI: %s)\n", st.InstalledVersion, st.CurrentVersion)
		}
		if st.VersionDrift {
			fmt.Fprintln(out, "Version drift detected; run `sudo axv host upgrade` or `sudo axv daemon upgrade`.")
		}
		return nil
	}
	if runtime.GOOS == "darwin" {
		home, err := profile.Home()
		if err != nil {
			return err
		}
		report, err := daemon.CollectStatus(ctx, home, version)
		if err != nil {
			return err
		}
		st := hostStatusFromDaemon("darwin", version, report)
		if asJSON {
			return jsonResult(out, st)
		}
		return writeDarwinHostStatus(out, st)
	}
	st := hostStatusReport{Supported: false, Platform: runtime.GOOS, State: "unsupported", CurrentVersion: version}
	if asJSON {
		return jsonResult(out, st)
	}
	fmt.Fprintln(out, "AXV VM host lifecycle commands support Linux and macOS.")
	return nil
}

func writeDarwinHostStatus(out io.Writer, st hostStatusReport) error {
	if st.State == "not_configured" {
		fmt.Fprintln(out, "VM host: not configured (run `axv setup` to prepare macOS Lima hosting)")
		return nil
	}
	fmt.Fprintf(out, "VM host: %s (mode: %s)\n", st.State, st.Mode)
	if st.Socket != "" {
		fmt.Fprintf(out, "Socket: %s (reachable: %t)\n", st.Socket, st.SocketReachable)
	}
	if st.HostID != "" {
		fmt.Fprintf(out, "Host ID: %s\n", st.HostID)
	}
	if st.Registered != nil {
		fmt.Fprintf(out, "launchd job registered: %t\n", *st.Registered)
	}
	if st.Error != "" {
		fmt.Fprintf(out, "Error: %s\n", st.Error)
	}
	for _, p := range st.Providers {
		if p.Available {
			fmt.Fprintf(out, "  %s: available\n", p.Provider)
		} else {
			fmt.Fprintf(out, "  %s: unavailable (%s)\n", p.Provider, p.Reason)
		}
	}
	return nil
}
