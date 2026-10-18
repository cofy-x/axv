//go:build !darwin && !linux && !windows

package terminal

import "errors"

func platformSupport() string { return "Terminal supports macOS, Linux, and Windows only." }

func platformShell() (string, error) { return "", errors.New(platformSupport()) }

func startProcess(shellConfig, int, int) (process, error) {
	return nil, errors.New(platformSupport())
}
