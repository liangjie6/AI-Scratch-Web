package runstore

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestP2CapacityIsAtomicAcrossStoresAndRecovers(t *testing.T) {
	first := newTestStore(t, Config{MaxActiveRuns: 4, MaxActiveRunsPerOwner: 2, RunRetentionDays: 7})
	second := newTestStore(t, Config{DatabasePath: first.databasePath, MaxActiveRuns: 4, MaxActiveRunsPerOwner: 2, RunRetentionDays: 7})
	var accepted, rejected atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := first
			if i%2 == 1 {
				s = second
			}
			owner := "one"
			if i%3 == 0 {
				owner = "two"
			}
			_, err := s.CreateRun(CreateRunParams{OwnerScopeHash: owner})
			if err == nil {
				accepted.Add(1)
			} else if errors.Is(err, ErrRunCapacity) {
				rejected.Add(1)
			} else {
				t.Errorf("create: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if accepted.Load() != 4 || rejected.Load() != 36 {
		t.Fatalf("accepted=%d rejected=%d", accepted.Load(), rejected.Load())
	}
	if n := queryTestInt(t, first, "SELECT count(*) FROM ai_runs"); n != 4 {
		t.Fatal(n)
	}
	execTestSQL(t, first, "UPDATE ai_runs SET status='CANCELLED'")
	if _, err := second.CreateRun(CreateRunParams{OwnerScopeHash: "one"}); err != nil {
		t.Fatal(err)
	}
}

func TestP2AbandonedReviewExpiresWithEvidenceThenRetention(t *testing.T) {
	s := newTestStore(t, Config{RunRetentionDays: 7, EventRetentionHours: 24})
	old := mustCreateRun(t, s, "one", 1, nil)
	fresh := mustCreateRun(t, s, "one", 1, nil)
	now := time.Now()
	execTestSQL(t, s, "UPDATE ai_runs SET status='AWAITING_TOOL_RESULT' WHERE run_id IN (?,?)", old.RunID, fresh.RunID)
	execTestSQL(t, s, "UPDATE ai_runs SET updated_at=? WHERE run_id=?", now.Add(-25*time.Hour).UnixMilli(), old.RunID)
	if err := s.ExpireAwaiting(now); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetRun(old.RunID, "one")
	if err != nil || r.Status != RunStatusFailed || r.TerminalAt == nil || r.ExpiresAt < now.Add(6*24*time.Hour).UnixMilli() {
		t.Fatalf("expired %+v %v", r, err)
	}
	if n := queryTestInt(t, s, "SELECT count(*) FROM ai_run_events WHERE run_id=? AND payload_json LIKE '%REVIEW_EXPIRED%'", old.RunID); n != 1 {
		t.Fatal(n)
	}
	r, err = s.GetRun(fresh.RunID, "one")
	if err != nil || r.Status != RunStatusAwaitingToolResult {
		t.Fatalf("fresh %+v %v", r, err)
	}
	if err = s.ExpireAwaiting(now); err != nil {
		t.Fatal(err)
	}
	if n := queryTestInt(t, s, "SELECT count(*) FROM ai_run_events WHERE run_id=?", old.RunID); n != 2 {
		t.Fatal(n)
	}
	execTestSQL(t, s, "UPDATE ai_runs SET expires_at=0 WHERE run_id=?", old.RunID)
	if n, err := s.PurgeExpired(); err != nil || n != 1 {
		t.Fatalf("purge=%d %v", n, err)
	}
	if n := queryTestInt(t, s, "SELECT count(*) FROM ai_run_events WHERE run_id=?", old.RunID); n != 0 {
		t.Fatal(n)
	}
}
