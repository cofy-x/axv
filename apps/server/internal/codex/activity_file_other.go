//go:build !darwin && !linux

package codex

import (
	"os"

	"github.com/cofy-x/axv/apps/server/internal/codex/runner"
)

func openActivityReview(string) (*os.File, error) {
	return nil, runner.ErrActivityUnavailable
}
