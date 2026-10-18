//go:build !darwin && !linux

package cli

import (
	"context"
	"os"
)

func setupConfirmationLine(ctx context.Context, input *os.File) (string, error) {
	return confirmationLine(ctx, input)
}
