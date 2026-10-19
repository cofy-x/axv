//go:build !darwin && !linux

package runner

import (
	"context"
	"os"
)

// Managed host Codex runs are unavailable on these platforms.
func AcquireOwnership(context.Context, string) (*os.File, error) {
	return nil, ErrUnavailable
}
