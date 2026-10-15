package runmanager

import (
	"context"

	"github.com/ai-scratch-web/backend/internal/runstore"
)

// SSEEvents streams persisted run events.
//
// Python: sse_events(manager, *, run_id, owner_scope_hash, sequence). It
// replays every event after the cursor, stops once the run is terminal, and
// otherwise waits for the next manager signal and emits a nil event so the
// HTTP layer can write a keepalive comment.
func SSEEvents(ctx context.Context, manager *AiRunManager, runID string, ownerScopeHash string, sequence int64) (<-chan *runstore.RunEvent, <-chan error) {
	events := make(chan *runstore.RunEvent)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		cursor := sequence
		for {
			batch, err := manager.store.EventsAfter(runstore.EventsAfterParams{
				RunID:          runID,
				OwnerScopeHash: ownerScopeHash,
				Sequence:       cursor,
			})
			if err != nil {
				errs <- err
				return
			}
			for index := range batch {
				cursor = batch[index].Sequence
				event := batch[index]
				select {
				case events <- &event:
				case <-ctx.Done():
					return
				}
			}
			record, err := manager.store.GetRun(runID, ownerScopeHash)
			if err != nil {
				errs <- err
				return
			}
			if runstore.IsTerminal(record.Status) {
				return
			}
			manager.waitForEvents(ctx, runID, secondsToDuration(sseWaitTimeoutSeconds))
			if ctx.Err() != nil {
				return
			}
			select {
			case events <- nil:
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, errs
}
