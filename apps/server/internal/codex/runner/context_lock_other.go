//go:build !darwin && !linux

package runner

// Managed host Codex runs are unavailable on these platforms.
func contextLockContended(error) bool { return false }
