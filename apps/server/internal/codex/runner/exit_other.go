//go:build !darwin && !linux

package runner

// Managed host Codex runs are unavailable on these platforms.
func RecordExit(string, int) error { return ErrUnavailable }

func ReadExit(string, string) (*Exit, error) { return nil, ErrUnavailable }
