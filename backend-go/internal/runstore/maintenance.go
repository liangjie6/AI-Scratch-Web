package runstore

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrRunCapacity = errors.New("active run capacity reached")

// ExpireAwaiting preserves an explicit failed event before the normal retention
// policy removes an abandoned review. The status/age test is in the same write
// transaction as the transition, so an accepted user result cannot be expired.
func (s *RunStore) ExpireAwaiting(now time.Time) error {
	return s.transaction(func(tx *sql.Tx) error {
		ctx := context.Background()
		cutoff := now.Add(-24 * time.Hour).UnixMilli()
		rows, err := tx.QueryContext(ctx, `SELECT run_id FROM ai_runs WHERE status = 'AWAITING_TOOL_RESULT' AND updated_at < ? ORDER BY updated_at LIMIT 64`, cutoff)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range ids {
			_, err = tx.ExecContext(ctx, `UPDATE ai_runs SET status='FAILED', state_version=state_version+1, updated_at=?, terminal_at=?, expires_at=? WHERE run_id=?`, now.UnixMilli(), now.UnixMilli(), now.UnixMilli()+s.runRetentionMs, id)
			if err != nil {
				return err
			}
			if _, err = s.insertEvent(ctx, tx, insertEventArgs{runID: id, phase: "failed", eventType: "failed", payload: map[string]any{"code": "REVIEW_EXPIRED", "message": "任务等待确认超过24小时，请重新生成。"}, attempt: 1}); err != nil {
				return err
			}
		}
		return nil
	})
}

// RetainedRunIDs supplies one bounded-lifetime map snapshot for the manager;
// it omits terminal rows, whose evidence remains in SQLite until retention.
func (s *RunStore) ActiveRunIDs() (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := s.handle()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT run_id FROM ai_runs WHERE status NOT IN ('READY_TO_COMMIT','CANCELLED','FAILED')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	return ids, rows.Err()
}
