//go:build !linux

package hostinstall

import (
	"context"
	"fmt"
	"io"
)

func Status(_ context.Context, currentVersion string) ServiceStatus {
	return ServiceStatus{
		Supported:      false,
		State:          "unsupported",
		CurrentVersion: currentVersion,
	}
}

func StartService(context.Context, io.Writer) error {
	return fmt.Errorf("axv-host.service lifecycle commands are supported on Linux only")
}

func StopService(context.Context, io.Writer) error {
	return fmt.Errorf("axv-host.service lifecycle commands are supported on Linux only")
}

func RestartService(context.Context, io.Writer) error {
	return fmt.Errorf("axv-host.service lifecycle commands are supported on Linux only")
}

func UpgradeService(context.Context, io.Writer) error {
	return fmt.Errorf("axv-host.service upgrade is supported on Linux only")
}

func UninstallService(context.Context, UninstallOptions, io.Writer) error {
	return fmt.Errorf("axv-host.service uninstall is supported on Linux only")
}

func StreamLogs(context.Context, bool, int, io.Writer) error {
	return fmt.Errorf("axv-host.service logs are supported on Linux only")
}

func HostNetworkInfo() (string, bool) { return "", false }
