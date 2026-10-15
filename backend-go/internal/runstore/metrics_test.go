package runstore

import (
	"testing"
)

func stripWindowBounds(t *testing.T, metrics map[string]any, windowHours int) map[string]any {
	t.Helper()
	windowStart, ok := metrics["window_start"].(int64)
	if !ok {
		t.Fatalf("window_start = %#v", metrics["window_start"])
	}
	windowEnd, ok := metrics["window_end"].(int64)
	if !ok {
		t.Fatalf("window_end = %#v", metrics["window_end"])
	}
	if windowEnd-windowStart != int64(windowHours)*60*60*1000 {
		t.Fatalf("window span = %d, want %d hours", windowEnd-windowStart, windowHours)
	}
	delete(metrics, "window_start")
	delete(metrics, "window_end")
	return metrics
}

func TestMetricsForOwnerEmptyWindow(t *testing.T) {
	store := newTestStore(t, Config{})
	metrics, err := store.MetricsForOwner(MetricsForOwnerParams{OwnerScopeHash: "owner-with-no-runs", WindowHours: 24})
	if err != nil {
		t.Fatalf("MetricsForOwner: %v", err)
	}
	want := map[string]any{
		"run_count":            0,
		"terminal_run_count":   0,
		"status_counts":        map[string]int{},
		"event_type_counts":    map[string]int{},
		"error_code_counts":    map[string]int{},
		"terminal_duration_ms": map[string]any{"p50": nil, "p95": nil},
		"failed_run_count":     0,
		"cancelled_run_count":  0,
		"stuck_run_count":      0,
		"stuck_after_ms":       nil,
		"alerts":               []map[string]any{},
		"phase_duration_ms":    map[string]map[string]any{},
		"repair": map[string]any{
			"run_count":      0,
			"total_attempts": int64(0),
			"p95_attempts":   nil,
		},
		"validation": map[string]any{
			"eligible_run_count":   0,
			"failed_run_count":     0,
			"first_pass_run_count": 0,
			"first_pass_rate":      nil,
		},
		"commit_verify": map[string]any{
			"attempt_count":        0,
			"run_count":            0,
			"outcome_counts":       map[string]int{},
			"rollback_kind_counts": map[string]int{},
			"error_code_counts":    map[string]int{},
			"revision_conflict":    map[string]any{"count": 0, "rate": nil},
			"commit_duration_ms":   map[string]any{"p50": nil, "p95": nil},
			"verify_duration_ms":   map[string]any{"p50": nil, "p95": nil},
			"total_duration_ms":    map[string]any{"p50": nil, "p95": nil},
		},
		"behavior": map[string]any{
			"test_count":        int64(0),
			"passed_count":      int64(0),
			"pass_rate":         nil,
			"must_count":        int64(0),
			"must_passed_count": int64(0),
			"must_pass_rate":    nil,
		},
		"resource_review": map[string]any{
			"attempt_count":    0,
			"run_count":        0,
			"by_resource_type": map[string]map[string]int64{},
		},
	}
	assertJSONEqual(t, want, stripWindowBounds(t, metrics, 24))
}

func TestMetricsForOwnerAggregates(t *testing.T) {
	store := newTestStore(t, Config{EventRetentionHours: 24, RunRetentionDays: 30})
	const owner = "owner-metrics"
	base := nowMillis() - 600_000

	// Run 1: a failed run with a repair, a validation failure and two protocol
	// errors. Its events are backdated so every duration is deterministic.
	run1 := mustCreateRun(t, store, owner, 1, map[string]any{"scope": "project"})
	appendEvent := func(phase, eventType string, payload map[string]any, attempt int64) {
		t.Helper()
		params := AppendEventParams{
			RunID:          run1.RunID,
			OwnerScopeHash: owner,
			Phase:          phase,
			EventType:      eventType,
			Payload:        payload,
			Attempt:        attempt,
		}
		if _, err := store.AppendEvent(params); err != nil {
			t.Fatalf("AppendEvent(%s): %v", eventType, err)
		}
	}
	appendEvent("specifying_task", "phase_started", map[string]any{"status": "SPECIFYING_TASK"}, 0)
	appendEvent("specifying_task", "repair_started", map[string]any{"attempt": 3}, 3)
	appendEvent("specifying_task", "phase_completed", map[string]any{"status": "SPECIFYING_TASK"}, 0)
	appendEvent("generating_programs", "phase_started", map[string]any{"status": "GENERATING_PROGRAMS"}, 0)
	appendEvent("generating_programs", "validation_failed", map[string]any{"errors": 2}, 0)
	appendEvent("generating_programs", "generation_error", map[string]any{"code": "MODEL_TIMEOUT"}, 0)
	run1 = mustTransition(t, store, run1, RunStatusFailed, "failed", "generation_error", map[string]any{"code": "MODEL_TIMEOUT"})
	eventTimes := []int64{base, base + 200, base + 210, base + 500, base + 600, base + 610, base + 900, base + 1000}
	for index, createdAt := range eventTimes {
		execTestSQL(t, store, "UPDATE ai_run_events SET created_at = ? WHERE run_id = ? AND sequence = ?", createdAt, run1.RunID, index+1)
	}
	execTestSQL(t, store, "UPDATE ai_runs SET created_at = ?, updated_at = ?, terminal_at = ? WHERE run_id = ?", base, base+1000, base+1000, run1.RunID)

	// Run 2: still awaiting a tool result, so it is neither terminal nor stuck.
	run2 := mustCreateRun(t, store, owner, 1, map[string]any{"scope": "project"})
	run2 = mustTransition(t, store, run2, RunStatusSpecifyingTask, "specifying_task", "phase_started", map[string]any{})
	run2 = mustTransition(t, store, run2, RunStatusBlueprinting, "blueprinting", "phase_started", map[string]any{})
	run2 = mustTransition(t, store, run2, RunStatusPlanningManifest, "planning_manifest", "phase_started", map[string]any{})
	run2 = mustTransition(t, store, run2, RunStatusAwaitingToolResult, "awaiting_tool_result", "phase_started", map[string]any{})
	for index, createdAt := range []int64{base + 5000, base + 5100, base + 5200, base + 5300, base + 5400} {
		execTestSQL(t, store, "UPDATE ai_run_events SET created_at = ? WHERE run_id = ? AND sequence = ?", createdAt, run2.RunID, index+1)
	}
	execTestSQL(t, store, "UPDATE ai_runs SET created_at = ?, updated_at = ? WHERE run_id = ?", base+5000, base+5400, run2.RunID)

	// Runs belonging to another owner or created outside the window are excluded.
	otherOwner := mustCreateRun(t, store, "owner-other", 1, nil)
	_ = otherOwner
	outside := mustCreateRun(t, store, owner, 1, nil)
	execTestSQL(t, store, "UPDATE ai_runs SET created_at = ?, updated_at = ? WHERE run_id = ?", nowMillis()-2*60*60*1000, nowMillis(), outside.RunID)

	// Client commit metrics: one clean outcome, one recovery failure and one
	// rollback.
	recordCommit := func(key string, outcome string, errorCode *string, commitMs, verifyMs, tests, passed, must, mustPassed int64) {
		t.Helper()
		if _, _, err := store.RecordClientCommitMetric(RecordClientCommitMetricParams{
			RunID:                   run1.RunID,
			OwnerScopeHash:          owner,
			ExpectedProjectEpoch:    1,
			IdempotencyKey:          key,
			CommitDurationMs:        commitMs,
			VerifyDurationMs:        verifyMs,
			BehaviorTestCount:       tests,
			BehaviorPassedCount:     passed,
			BehaviorMustCount:       must,
			BehaviorMustPassedCount: mustPassed,
			Outcome:                 outcome,
			ErrorCode:               errorCode,
		}); err != nil {
			t.Fatalf("RecordClientCommitMetric(%s): %v", key, err)
		}
	}
	recordCommit("commit-key-1", "COMPLETED", nil, 1000, 500, 10, 9, 4, 4)
	recordCommit("commit-key-2", "FAILED_NEEDS_RECOVERY", stringPtr("COMMIT_CONFLICT"), 2000, 1000, 5, 3, 2, 1)
	recordCommit("commit-key-3", "ROLLED_BACK", stringPtr("COMMIT_FAILED"), 500, 250, 0, 0, 0, 0)

	// Resource review: one receipt through the API, two malformed receipts
	// written directly so the defensive branches are exercised.
	if _, _, err := store.RecordResourceReviewMetric(RecordResourceReviewMetricParams{
		RunID:                run1.RunID,
		OwnerScopeHash:       owner,
		ExpectedProjectEpoch: 1,
		IdempotencyKey:       "review-key-1",
		Buckets: []map[string]any{
			{
				"resource_type":             "sprite",
				"auto_selected_count":       3,
				"auto_reselected_count":     1,
				"manual_selected_count":     0,
				"review_required_count":     2,
				"generation_fallback_count": 1,
			},
			{
				"resource_type":         "sound",
				"auto_selected_count":   5,
				"auto_reselected_count": 0,
				"manual_selected_count": 0,
				"review_required_count": 0,
			},
			{"resource_type": "music", "auto_selected_count": 9},
			{"resource_type": "sprite", "auto_selected_count": 250},
		},
	}); err != nil {
		t.Fatalf("RecordResourceReviewMetric: %v", err)
	}
	execTestSQL(t, store, "INSERT INTO ai_run_resource_review_metric_receipts(run_id, attempt, idempotency_key, request_hash, buckets_json, created_at) VALUES (?, 2, ?, ?, ?, ?)",
		run1.RunID, "review-key-2", "hash-2", `{"not":"a list"}`, nowMillis())
	execTestSQL(t, store, "INSERT INTO ai_run_resource_review_metric_receipts(run_id, attempt, idempotency_key, request_hash, buckets_json, created_at) VALUES (?, 3, ?, ?, ?, ?)",
		run1.RunID, "review-key-3", "hash-3", "not json", nowMillis())

	metrics, err := store.MetricsForOwner(MetricsForOwnerParams{OwnerScopeHash: owner, WindowHours: 1})
	if err != nil {
		t.Fatalf("MetricsForOwner: %v", err)
	}
	want := map[string]any{
		"run_count":          2,
		"terminal_run_count": 1,
		"status_counts": map[string]int{
			"FAILED":               1,
			"AWAITING_TOOL_RESULT": 1,
		},
		"event_type_counts": map[string]int{
			"phase_started":     8,
			"repair_started":    1,
			"phase_completed":   1,
			"validation_failed": 1,
			"generation_error":  2,
		},
		"error_code_counts":    map[string]int{"MODEL_TIMEOUT": 2},
		"terminal_duration_ms": map[string]any{"p50": int64(1000), "p95": int64(1000)},
		"failed_run_count":     1,
		"cancelled_run_count":  0,
		"stuck_run_count":      0,
		"stuck_after_ms":       nil,
		"alerts": []map[string]any{
			{
				"code":     "ATOMIC_RECOVERY_FAILURE",
				"severity": "critical",
				"action":   "DISABLE_AI_COMMIT",
				"count":    1,
			},
		},
		"phase_duration_ms": map[string]map[string]any{
			"snapshotting":        {"count": 2, "p50": int64(100), "p95": int64(100)},
			"specifying_task":     {"count": 2, "p50": int64(100), "p95": int64(100)},
			"blueprinting":        {"count": 1, "p50": int64(100), "p95": int64(100)},
			"planning_manifest":   {"count": 1, "p50": int64(100), "p95": int64(100)},
			"generating_programs": {"count": 1, "p50": int64(300), "p95": int64(300)},
		},
		"repair": map[string]any{
			"run_count":      1,
			"total_attempts": int64(3),
			"p95_attempts":   int64(3),
		},
		"validation": map[string]any{
			"eligible_run_count":   1,
			"failed_run_count":     1,
			"first_pass_run_count": 0,
			"first_pass_rate":      0.0,
		},
		"commit_verify": map[string]any{
			"attempt_count": 3,
			"run_count":     1,
			"outcome_counts": map[string]int{
				"COMPLETED":             1,
				"FAILED_NEEDS_RECOVERY": 1,
				"ROLLED_BACK":           1,
			},
			"rollback_kind_counts": map[string]int{
				"COMPLETED":             1,
				"FAILED_NEEDS_RECOVERY": 1,
				"ROLLED_BACK":           1,
			},
			"error_code_counts": map[string]int{
				"COMMIT_CONFLICT": 1,
				"COMMIT_FAILED":   1,
			},
			"revision_conflict":  map[string]any{"count": 1, "rate": float64(1) / float64(3)},
			"commit_duration_ms": map[string]any{"p50": int64(1000), "p95": int64(1000)},
			"verify_duration_ms": map[string]any{"p50": int64(500), "p95": int64(500)},
			"total_duration_ms":  map[string]any{"p50": int64(1500), "p95": int64(1500)},
		},
		"behavior": map[string]any{
			"test_count":        int64(15),
			"passed_count":      int64(12),
			"pass_rate":         0.8,
			"must_count":        int64(6),
			"must_passed_count": int64(5),
			"must_pass_rate":    float64(5) / float64(6),
		},
		"resource_review": map[string]any{
			"attempt_count": 3,
			"run_count":     1,
			"by_resource_type": map[string]map[string]int64{
				"sprite": {
					"auto_selected_count":       3,
					"auto_reselected_count":     1,
					"manual_selected_count":     0,
					"review_required_count":     2,
					"generation_fallback_count": 1,
				},
				"sound": {
					"auto_selected_count":       5,
					"auto_reselected_count":     0,
					"manual_selected_count":     0,
					"review_required_count":     0,
					"generation_fallback_count": 0,
				},
			},
		},
	}
	assertJSONEqual(t, want, stripWindowBounds(t, metrics, 1))
}

func TestMetricsStuckRunsAndWindowFilter(t *testing.T) {
	store := newTestStore(t, Config{})
	const owner = "owner-stuck"
	stuck := mustCreateRun(t, store, owner, 0, nil)
	execTestSQL(t, store, "UPDATE ai_runs SET updated_at = ? WHERE run_id = ?", nowMillis()-120_000, stuck.RunID)
	waiting := mustCreateRun(t, store, owner, 0, nil)
	waiting = mustTransition(t, store, waiting, RunStatusSpecifyingTask, "specifying_task", "phase_started", map[string]any{})
	waiting = mustTransition(t, store, waiting, RunStatusBlueprinting, "blueprinting", "phase_started", map[string]any{})
	waiting = mustTransition(t, store, waiting, RunStatusPlanningManifest, "planning_manifest", "phase_started", map[string]any{})
	waiting = mustTransition(t, store, waiting, RunStatusAwaitingToolResult, "awaiting_tool_result", "phase_started", map[string]any{})
	execTestSQL(t, store, "UPDATE ai_runs SET updated_at = ? WHERE run_id = ?", nowMillis()-120_000, waiting.RunID)
	outside := mustCreateRun(t, store, owner, 0, nil)
	execTestSQL(t, store, "UPDATE ai_runs SET created_at = ?, updated_at = ? WHERE run_id = ?", nowMillis()-2*60*60*1000, nowMillis()-2*60*60*1000, outside.RunID)

	metrics, err := store.MetricsForOwner(MetricsForOwnerParams{
		OwnerScopeHash: owner,
		WindowHours:    1,
		StuckAfterMs:   int64Ptr(60_000),
	})
	if err != nil {
		t.Fatalf("MetricsForOwner: %v", err)
	}
	metrics = stripWindowBounds(t, metrics, 1)
	if metrics["run_count"] != 2 {
		t.Fatalf("run_count = %v, want 2", metrics["run_count"])
	}
	if metrics["stuck_run_count"] != 1 {
		t.Fatalf("stuck_run_count = %v, want 1", metrics["stuck_run_count"])
	}
	alerts, ok := metrics["alerts"].([]map[string]any)
	if !ok || len(alerts) != 1 {
		t.Fatalf("alerts = %#v, want exactly the stuck-run alert", metrics["alerts"])
	}
	assertJSONEqual(t, map[string]any{
		"code":            "STUCK_RUN_RATE_EXCEEDED",
		"severity":        "warning",
		"action":          "STOP_ROLLOUT",
		"run_count":       2,
		"stuck_run_count": 1,
	}, alerts[0])
	if metrics["failed_run_count"] != 0 || metrics["cancelled_run_count"] != 0 {
		t.Fatalf("terminal counts = %v / %v", metrics["failed_run_count"], metrics["cancelled_run_count"])
	}
	if metrics["terminal_run_count"] != 0 {
		t.Fatalf("terminal_run_count = %v, want 0", metrics["terminal_run_count"])
	}
	assertJSONEqual(t, int64(60_000), metrics["stuck_after_ms"])
}

func TestMetricsCommitVerifyP95Alert(t *testing.T) {
	store := newTestStore(t, Config{})
	const owner = "owner-p95"
	record := mustCreateRun(t, store, owner, 0, nil)
	for index := 0; index < 50; index++ {
		if _, _, err := store.RecordClientCommitMetric(RecordClientCommitMetricParams{
			RunID:                record.RunID,
			OwnerScopeHash:       owner,
			ExpectedProjectEpoch: 0,
			IdempotencyKey:       "commit-key-" + string(rune('a'+index%26)) + string(rune('a'+index/26)),
			CommitDurationMs:     5000,
			VerifyDurationMs:     1000,
			Outcome:              "COMPLETED",
		}); err != nil {
			t.Fatalf("RecordClientCommitMetric(%d): %v", index, err)
		}
	}
	metrics, err := store.MetricsForOwner(MetricsForOwnerParams{OwnerScopeHash: owner, WindowHours: 1})
	if err != nil {
		t.Fatalf("MetricsForOwner: %v", err)
	}
	metrics = stripWindowBounds(t, metrics, 1)
	alerts, ok := metrics["alerts"].([]map[string]any)
	if !ok {
		t.Fatalf("alerts = %#v", metrics["alerts"])
	}
	assertJSONEqual(t, []map[string]any{
		{
			"code":            "COMMIT_VERIFY_P95_EXCEEDED",
			"severity":        "warning",
			"action":          "STOP_ROLLOUT",
			"attempt_count":   50,
			"p95_duration_ms": int64(6000),
		},
	}, alerts)
	commitVerify, ok := metrics["commit_verify"].(map[string]any)
	if !ok {
		t.Fatalf("commit_verify = %#v", metrics["commit_verify"])
	}
	if commitVerify["attempt_count"] != 50 {
		t.Fatalf("attempt_count = %v", commitVerify["attempt_count"])
	}
}
