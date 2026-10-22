package control

import (
	"context"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
)

// performOperation keeps one accepted copy and its reservations while a provider
// writer from the previous controller still owns that intent. Only the copy's
// confirmed lock contention permits re-entry; other errors/actions are final.
func (s *Service) performOperation(ctx context.Context, op operationRecord, timeout, retryDelay time.Duration) (compute.Observation, error) {
	for {
		work, cancel := context.WithTimeout(ctx, timeout)
		observed, err := s.perform(work, op)
		cancel()
		if op.Action != "copy-volume" || compute.ErrorCode(err) != "provider_pending" || ctx.Err() != nil {
			return observed, err
		}
		if err := s.phase(ctx, op, "waiting-for-provider"); err != nil {
			return observed, err
		}
		diagnostics.FromContext(ctx).Info("operation.provider_pending")
		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return observed, ctx.Err()
		case <-timer.C:
		}
	}
}
