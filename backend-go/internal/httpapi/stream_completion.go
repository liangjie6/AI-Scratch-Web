package httpapi

import (
	"context"
	"github.com/ai-scratch-web/backend/internal/provider"
)

// The terminal error channel is ordered after all data. Selecting both at once
// lets a closed data channel hide a buffered error, or drops buffered content.
func consumeTextStream(ctx context.Context, deltas <-chan provider.StreamDelta, errs <-chan error, consume func(provider.StreamDelta)) error {
	for deltas != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case delta, open := <-deltas:
			if !open {
				deltas = nil
				continue
			}
			consume(delta)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if errs == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errs:
		return err
	}
}
