package runstore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

const testOwner = "owner-scope-hash"

func newTestStore(t *testing.T, cfg Config) *RunStore {
	t.Helper()
	if cfg.DatabasePath == "" {
		cfg.DatabasePath = filepath.Join(t.TempDir(), "ai-runs.sqlite3")
	}
	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func int64Ptr(value int64) *int64 { return &value }

func stringPtr(value string) *string { return &value }

func mustCanonical(t *testing.T, value any) string {
	t.Helper()
	serialized, err := CanonicalJSON(value)
	if err != nil {
		t.Fatalf("CanonicalJSON(%#v): %v", value, err)
	}
	return serialized
}

func assertJSONEqual(t *testing.T, want, got any) {
	t.Helper()
	wantJSON := mustCanonical(t, want)
	gotJSON := mustCanonical(t, got)
	if wantJSON != gotJSON {
		t.Fatalf("JSON mismatch\n want: %s\n  got: %s", wantJSON, gotJSON)
	}
}

func execTestSQL(t *testing.T, store *RunStore, query string, args ...any) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, err := store.db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func queryTestInt(t *testing.T, store *RunStore, query string, args ...any) int64 {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	var value int64
	if err := store.db.QueryRowContext(context.Background(), query, args...).Scan(&value); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return value
}

func queryTestString(t *testing.T, store *RunStore, query string, args ...any) string {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	var value string
	if err := store.db.QueryRowContext(context.Background(), query, args...).Scan(&value); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return value
}

func mustCreateRun(t *testing.T, store *RunStore, owner string, epoch int64, summary map[string]any) RunRecord {
	t.Helper()
	record, err := store.CreateRun(CreateRunParams{
		OwnerScopeHash:  owner,
		ProjectEpoch:    epoch,
		ArtifactSummary: summary,
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	return record
}

func mustTransition(t *testing.T, store *RunStore, record RunRecord, target RunStatus, phase, eventType string, payload map[string]any) RunRecord {
	t.Helper()
	updated, err := store.Transition(TransitionParams{
		RunID:          record.RunID,
		OwnerScopeHash: record.OwnerScopeHash,
		Target:         target,
		Phase:          phase,
		EventType:      eventType,
		Payload:        payload,
	})
	if err != nil {
		t.Fatalf("Transition(%s): %v", target, err)
	}
	return updated
}

func mustGetRun(t *testing.T, store *RunStore, runID, owner string) RunRecord {
	t.Helper()
	record, err := store.GetRun(runID, owner)
	if err != nil {
		t.Fatalf("GetRun(%s): %v", runID, err)
	}
	return record
}

func mustEventsAfter(t *testing.T, store *RunStore, runID, owner string, sequence int64) []RunEvent {
	t.Helper()
	events, err := store.EventsAfter(EventsAfterParams{RunID: runID, OwnerScopeHash: owner, Sequence: sequence})
	if err != nil {
		t.Fatalf("EventsAfter: %v", err)
	}
	return events
}

func writeLegacyDatabase(t *testing.T, path string, statements []string) {
	t.Helper()
	db, err := sql.Open(sqliteDriverName, path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatalf("legacy statement %q: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}
}

func TestInitializeCreatesSchema(t *testing.T) {
	store := newTestStore(t, Config{EventRetentionHours: 1, RunRetentionDays: 7})
	tables := map[string]bool{}
	indices := map[string]bool{}
	store.mu.Lock()
	rows, err := store.db.Query("SELECT type, name FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'")
	if err != nil {
		store.mu.Unlock()
		t.Fatalf("sqlite_master: %v", err)
	}
	for rows.Next() {
		var objectType, name string
		if err := rows.Scan(&objectType, &name); err != nil {
			rows.Close()
			store.mu.Unlock()
			t.Fatalf("scan sqlite_master: %v", err)
		}
		if objectType == "table" {
			tables[name] = true
		}
		if objectType == "index" {
			indices[name] = true
		}
	}
	rows.Close()
	store.mu.Unlock()
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite_master: %v", err)
	}
	for _, name := range []string{
		"ai_run_schema",
		"ai_runs",
		"ai_run_events",
		"ai_run_idempotency",
		"ai_run_tool_results",
		"ai_run_commit_metric_receipts",
		"ai_run_resource_review_metric_receipts",
	} {
		if !tables[name] {
			t.Fatalf("missing table %s (have %v)", name, tables)
		}
	}
	for _, name := range []string{
		"ai_runs_expiry_idx",
		"ai_run_events_created_idx",
		"ai_run_commit_metric_receipts_created_idx",
		"ai_run_resource_review_metric_receipts_created_idx",
	} {
		if !indices[name] {
			t.Fatalf("missing index %s (have %v)", name, indices)
		}
	}
	if got := queryTestInt(t, store, "SELECT MAX(version) FROM ai_run_schema"); got != RunStoreSchemaVersion {
		t.Fatalf("schema version = %d, want %d", got, RunStoreSchemaVersion)
	}
	if got := queryTestInt(t, store, "SELECT COUNT(*) FROM ai_run_schema WHERE version = ?", RunStoreSchemaVersion); got != 1 {
		t.Fatalf("schema version rows = %d, want 1", got)
	}
	if got := queryTestString(t, store, "PRAGMA journal_mode"); got != "wal" {
		t.Fatalf("journal_mode = %q, want wal", got)
	}
	if got := queryTestInt(t, store, "PRAGMA foreign_keys"); got != 1 {
		t.Fatalf("foreign_keys = %d, want 1", got)
	}
	if got := queryTestInt(t, store, "PRAGMA busy_timeout"); got != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", got)
	}
}

func TestInitializeIsIdempotent(t *testing.T) {
	store := newTestStore(t, Config{})
	run := mustCreateRun(t, store, testOwner, 0, map[string]any{"scope": "project"})
	if err := store.Initialize(); err != nil {
		t.Fatalf("second Initialize: %v", err)
	}
	if got := mustGetRun(t, store, run.RunID, testOwner); got.RunID != run.RunID {
		t.Fatalf("run disappeared after re-initialize")
	}
}

func TestStoreOperationsRequireInitialization(t *testing.T) {
	store, err := NewStore(Config{DatabasePath: filepath.Join(t.TempDir(), "never-opened.sqlite3")})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := store.GetRun("run_missing", testOwner); !errors.Is(err, ErrStoreNotInitialized) {
		t.Fatalf("GetRun before Initialize = %v, want ErrStoreNotInitialized", err)
	}
	if _, err := store.CreateRun(CreateRunParams{OwnerScopeHash: testOwner}); !errors.Is(err, ErrStoreNotInitialized) {
		t.Fatalf("CreateRun before Initialize = %v, want ErrStoreNotInitialized", err)
	}
	if _, err := store.PurgeExpired(); !errors.Is(err, ErrStoreNotInitialized) {
		t.Fatalf("PurgeExpired before Initialize = %v, want ErrStoreNotInitialized", err)
	}
	if _, err := store.MetricsForOwner(MetricsForOwnerParams{OwnerScopeHash: testOwner, WindowHours: 1}); !errors.Is(err, ErrStoreNotInitialized) {
		t.Fatalf("MetricsForOwner before Initialize = %v, want ErrStoreNotInitialized", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close on uninitialized store: %v", err)
	}
}

func TestNewStoreRequiresDatabasePath(t *testing.T) {
	if _, err := NewStore(Config{}); err == nil {
		t.Fatal("NewStore accepted an empty DatabasePath")
	}
	if _, err := NewStore(Config{DatabasePath: "   "}); err == nil {
		t.Fatal("NewStore accepted a blank DatabasePath")
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	store := newTestStore(t, Config{})
	run := mustCreateRun(t, store, testOwner, 0, nil)
	if err := store.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := store.GetRun(run.RunID, testOwner); !errors.Is(err, ErrStoreNotInitialized) {
		t.Fatalf("GetRun after Close = %v, want ErrStoreNotInitialized", err)
	}
}

func TestSchemaVersionTooNew(t *testing.T) {
	path := filepath.Join(t.TempDir(), "newer.sqlite3")
	writeLegacyDatabase(t, path, []string{
		"CREATE TABLE ai_run_schema (version INTEGER PRIMARY KEY)",
		"INSERT INTO ai_run_schema(version) VALUES (6)",
	})
	store, err := NewStore(Config{DatabasePath: path})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Initialize(); !errors.Is(err, ErrSchemaVersionTooNew) {
		t.Fatalf("Initialize on newer schema = %v, want ErrSchemaVersionTooNew", err)
	}
	if _, err := store.GetRun("run_x", testOwner); !errors.Is(err, ErrStoreNotInitialized) {
		t.Fatalf("GetRun after failed Initialize = %v, want ErrStoreNotInitialized", err)
	}
}

func TestSchemaMigratesFromVersion2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.sqlite3")
	writeLegacyDatabase(t, path, []string{
		"CREATE TABLE ai_run_schema (version INTEGER PRIMARY KEY)",
		"INSERT INTO ai_run_schema(version) VALUES (2)",
		`CREATE TABLE ai_runs (
			run_id TEXT PRIMARY KEY,
			owner_scope_hash TEXT NOT NULL,
			status TEXT NOT NULL,
			state_version INTEGER NOT NULL,
			protocol_version TEXT NOT NULL,
			project_epoch INTEGER NOT NULL,
			artifact_summary_json TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			terminal_at INTEGER
		)`,
		"INSERT INTO ai_runs VALUES ('run_legacy', 'owner', 'FAILED', 2, 'ai-scratch-run/1', 0, '{}', 100, 200, 300, 200)",
		`CREATE TABLE ai_run_commit_metrics (
			run_id TEXT NOT NULL,
			attempt INTEGER NOT NULL,
			idempotency_key TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			commit_duration_ms INTEGER NOT NULL,
			verify_duration_ms INTEGER NOT NULL,
			outcome TEXT NOT NULL,
			error_code TEXT,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (run_id, attempt),
			UNIQUE (run_id, idempotency_key)
		)`,
		"INSERT INTO ai_run_commit_metrics VALUES ('run_legacy', 1, 'legacy-key', 'legacy-hash', 12, 34, 'COMPLETED', NULL, 999)",
	})
	store, err := NewStore(Config{DatabasePath: path, EventRetentionHours: 1, RunRetentionDays: 7})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()
	if err := store.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if got := queryTestInt(t, store, "SELECT MAX(version) FROM ai_run_schema"); got != RunStoreSchemaVersion {
		t.Fatalf("schema version = %d, want %d", got, RunStoreSchemaVersion)
	}
	var (
		attempt, commitMS, verifyMS, createdAt             int64
		testCount, passedCount, mustCount, mustPassedCount int64
		key, hash, outcome                                 string
		errorCode                                          sql.NullString
	)
	err = store.db.QueryRowContext(context.Background(), `SELECT attempt, idempotency_key, request_hash, commit_duration_ms,
			verify_duration_ms, outcome, error_code, created_at,
			behavior_test_count, behavior_passed_count, behavior_must_count, behavior_must_passed_count
		FROM ai_run_commit_metric_receipts WHERE run_id = 'run_legacy'`).Scan(
		&attempt, &key, &hash, &commitMS, &verifyMS, &outcome, &errorCode, &createdAt,
		&testCount, &passedCount, &mustCount, &mustPassedCount,
	)
	if err != nil {
		t.Fatalf("select migrated receipt: %v", err)
	}
	if attempt != 1 || key != "legacy-key" || hash != "legacy-hash" || commitMS != 12 || verifyMS != 34 || outcome != "COMPLETED" || createdAt != 999 {
		t.Fatalf("migrated receipt mismatch: %d %s %s %d %d %s %d", attempt, key, hash, commitMS, verifyMS, outcome, createdAt)
	}
	if errorCode.Valid {
		t.Fatalf("migrated error_code = %q, want NULL", errorCode.String)
	}
	if testCount != 0 || passedCount != 0 || mustCount != 0 || mustPassedCount != 0 {
		t.Fatalf("migrated behavior counts = %d %d %d %d, want zeros", testCount, passedCount, mustCount, mustPassedCount)
	}
	if got := queryTestInt(t, store, "SELECT COUNT(*) FROM ai_run_resource_review_metric_receipts"); got != 0 {
		t.Fatalf("resource review receipts = %d, want 0", got)
	}
}

func TestSchemaMigratesFromVersion4(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v4.sqlite3")
	writeLegacyDatabase(t, path, []string{
		"CREATE TABLE ai_run_schema (version INTEGER PRIMARY KEY)",
		"INSERT INTO ai_run_schema(version) VALUES (4)",
		`CREATE TABLE ai_run_commit_metric_receipts (
			run_id TEXT NOT NULL,
			attempt INTEGER NOT NULL,
			idempotency_key TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			commit_duration_ms INTEGER NOT NULL,
			verify_duration_ms INTEGER NOT NULL,
			outcome TEXT NOT NULL,
			error_code TEXT,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (run_id, attempt),
			UNIQUE (run_id, idempotency_key)
		)`,
		"INSERT INTO ai_run_commit_metric_receipts VALUES ('run_old', 1, 'k', 'h', 1, 2, 'COMPLETED', NULL, 5)",
	})
	store, err := NewStore(Config{DatabasePath: path})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()
	if err := store.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	columns := map[string]bool{}
	rows, err := store.db.Query("PRAGMA table_info(ai_run_commit_metric_receipts)")
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	for rows.Next() {
		var (
			cid       int64
			name      string
			columnTyp string
			notNull   int64
			defaultV  sql.NullString
			pk        int64
		)
		if err := rows.Scan(&cid, &name, &columnTyp, &notNull, &defaultV, &pk); err != nil {
			rows.Close()
			t.Fatalf("scan table_info: %v", err)
		}
		columns[name] = true
	}
	rows.Close()
	for _, name := range []string{
		"behavior_test_count",
		"behavior_passed_count",
		"behavior_must_count",
		"behavior_must_passed_count",
	} {
		if !columns[name] {
			t.Fatalf("missing migrated column %s (have %v)", name, columns)
		}
	}
	if got := queryTestInt(t, store, "SELECT behavior_must_passed_count FROM ai_run_commit_metric_receipts WHERE run_id = 'run_old'"); got != 0 {
		t.Fatalf("default behavior count = %d, want 0", got)
	}
	if got := queryTestInt(t, store, "SELECT MAX(version) FROM ai_run_schema"); got != RunStoreSchemaVersion {
		t.Fatalf("schema version = %d, want %d", got, RunStoreSchemaVersion)
	}
}

func TestCreateRun(t *testing.T) {
	store := newTestStore(t, Config{EventRetentionHours: 48, RunRetentionDays: 7})
	summary := map[string]any{
		"scope":     "project",
		"contracts": map[string]any{"task": "make a sprite"},
		"note":      "中文",
	}
	record := mustCreateRun(t, store, testOwner, 3, summary)
	if !strings.HasPrefix(record.RunID, "run_") || len(record.RunID) != 36 {
		t.Fatalf("run id = %q, want run_ + 32 hex characters", record.RunID)
	}
	if strings.Trim(record.RunID[4:], "0123456789abcdef") != "" {
		t.Fatalf("run id = %q, want lowercase hex suffix", record.RunID)
	}
	if record.OwnerScopeHash != testOwner {
		t.Fatalf("owner = %q", record.OwnerScopeHash)
	}
	if record.Status != RunStatusSnapshotting {
		t.Fatalf("status = %q", record.Status)
	}
	if record.StateVersion != 1 {
		t.Fatalf("state_version = %d, want 1", record.StateVersion)
	}
	if record.ProtocolVersion != RunProtocolVersion {
		t.Fatalf("protocol_version = %q", record.ProtocolVersion)
	}
	if record.ProjectEpoch != 3 {
		t.Fatalf("project_epoch = %d, want 3", record.ProjectEpoch)
	}
	assertJSONEqual(t, summary, record.ArtifactSummary)
	if record.CreatedAt != record.UpdatedAt {
		t.Fatalf("created_at %d != updated_at %d", record.CreatedAt, record.UpdatedAt)
	}
	if record.ExpiresAt != record.CreatedAt+7*24*60*60*1000 {
		t.Fatalf("expires_at = %d, want created_at + 7 days", record.ExpiresAt)
	}
	if record.TerminalAt != nil {
		t.Fatalf("terminal_at = %v, want nil", *record.TerminalAt)
	}
	stored := mustGetRun(t, store, record.RunID, testOwner)
	assertJSONEqual(t, summary, stored.ArtifactSummary)
	rawSummary := queryTestString(t, store, "SELECT artifact_summary_json FROM ai_runs WHERE run_id = ?", record.RunID)
	wantRaw := `{"contracts":{"task":"make a sprite"},"note":"中文","scope":"project"}`
	if rawSummary != wantRaw {
		t.Fatalf("stored artifact_summary_json = %s, want %s", rawSummary, wantRaw)
	}
	events := mustEventsAfter(t, store, record.RunID, testOwner, 0)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	event := events[0]
	if event.Sequence != 1 || event.Phase != "snapshotting" || event.Type != "phase_started" || event.Attempt != 1 {
		t.Fatalf("first event = %+v", event)
	}
	if !strings.HasPrefix(event.EventID, "evt_") || len(event.EventID) != 36 {
		t.Fatalf("event id = %q", event.EventID)
	}
	assertJSONEqual(t, map[string]any{"status": "SNAPSHOTTING"}, event.Payload)
	accepted, err := store.AcceptedToolCallIds(AcceptedToolCallIdsParams{RunID: record.RunID, OwnerScopeHash: testOwner})
	if err != nil {
		t.Fatalf("AcceptedToolCallIds: %v", err)
	}
	if accepted == nil || len(accepted) != 0 {
		t.Fatalf("accepted tool call ids = %v, want empty set", accepted)
	}
}

func TestGetRunNotFound(t *testing.T) {
	store := newTestStore(t, Config{})
	record := mustCreateRun(t, store, testOwner, 0, nil)
	_, err := store.GetRun(record.RunID, "another-owner")
	if !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("GetRun with wrong owner = %v, want ErrRunNotFound", err)
	}
	var notFound *RunNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("GetRun error is not *RunNotFoundError: %v", err)
	}
	if notFound.RunID != record.RunID || err.Error() != record.RunID {
		t.Fatalf("RunNotFoundError = %+v (message %q)", notFound, err.Error())
	}
	if _, err := store.EventsAfter(EventsAfterParams{RunID: "run_missing", OwnerScopeHash: testOwner}); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("EventsAfter on missing run = %v", err)
	}
	if _, err := store.SequenceForEvent(SequenceForEventParams{RunID: "run_missing", OwnerScopeHash: testOwner, EventID: "evt_x"}); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("SequenceForEvent on missing run = %v", err)
	}
	if _, err := store.AcceptedToolCallIds(AcceptedToolCallIdsParams{RunID: "run_missing", OwnerScopeHash: testOwner}); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("AcceptedToolCallIds on missing run = %v", err)
	}
}

func TestPublicPayload(t *testing.T) {
	terminalAt := int64(99)
	record := RunRecord{
		RunID:           "run_abc",
		OwnerScopeHash:  testOwner,
		Status:          RunStatusFailed,
		StateVersion:    4,
		ProtocolVersion: RunProtocolVersion,
		ProjectEpoch:    2,
		ArtifactSummary: map[string]any{
			"scope":           "project",
			"output_contract": "contract text",
			"contracts":       map[string]any{"task": "private wording"},
			"manifest_summary": map[string]any{
				"program_target_count": 3,
				"resource_op_count":    2,
				"summary":              "not an int",
				"extra":                "ignored",
			},
			"asset_need_ids": []any{"a", "b"},
		},
		CreatedAt:  10,
		UpdatedAt:  20,
		ExpiresAt:  30,
		TerminalAt: &terminalAt,
	}
	payload := record.PublicPayload()
	want := map[string]any{
		"protocol_version": "ai-scratch-run/1",
		"run_id":           "run_abc",
		"status":           "FAILED",
		"state_version":    int64(4),
		"project_epoch":    int64(2),
		"artifact_summary": map[string]any{
			"scope":           "project",
			"output_contract": "contract text",
			"manifest_summary": map[string]any{
				"program_target_count": int64(3),
				"resource_op_count":    int64(2),
			},
			"asset_need_count": 2,
		},
		"created_at": int64(10),
		"updated_at": int64(20),
		"expires_at": int64(30),
		"terminal":   true,
	}
	assertJSONEqual(t, want, payload)
	if _, leaked := payload["artifact_summary"].(map[string]any)["contracts"]; leaked {
		t.Fatal("public payload leaked contracts")
	}
}

func TestPublicPayloadOmitsEmptySummaryParts(t *testing.T) {
	record := RunRecord{
		Status:          RunStatusSnapshotting,
		ProtocolVersion: RunProtocolVersion,
		ArtifactSummary: map[string]any{
			"scope":            3,
			"manifest_summary": map[string]any{"program_target_count": "three"},
			"asset_need_ids":   "not-a-list",
		},
	}
	payload := record.PublicPayload()
	want := map[string]any{
		"protocol_version": "ai-scratch-run/1",
		"run_id":           "",
		"status":           "SNAPSHOTTING",
		"state_version":    int64(0),
		"project_epoch":    int64(0),
		"artifact_summary": map[string]any{},
		"created_at":       int64(0),
		"updated_at":       int64(0),
		"expires_at":       int64(0),
		"terminal":         false,
	}
	assertJSONEqual(t, want, payload)
}

func TestRunEventEnvelope(t *testing.T) {
	event := RunEvent{
		RunID:     "run_abc",
		EventID:   "evt_1",
		Sequence:  7,
		Phase:     "generating_programs",
		Type:      "phase_started",
		Attempt:   2,
		Payload:   map[string]any{"status": "GENERATING_PROGRAMS"},
		CreatedAt: 1234,
	}
	want := map[string]any{
		"protocol_version": "ai-scratch-run/1",
		"run_id":           "run_abc",
		"event_id":         "evt_1",
		"sequence":         int64(7),
		"phase":            "generating_programs",
		"type":             "phase_started",
		"attempt":          int64(2),
		"payload":          map[string]any{"status": "GENERATING_PROGRAMS"},
	}
	assertJSONEqual(t, want, event.Envelope())
}

func TestCanonicalJSONMatchesPython(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{
			name:  "sorted keys, unicode, html characters",
			value: map[string]any{"b": 1, "a": "中文", "c": []any{}, "d": map[string]any{}, "x": "<a>&b"},
			want:  `{"a":"中文","b":1,"c":[],"d":{},"x":"<a>&b"}`,
		},
		{
			name:  "nulls and booleans",
			value: map[string]any{"z": nil, "a": true, "n": 1.5},
			want:  `{"a":true,"n":1.5,"z":null}`,
		},
		{
			name:  "nested lists",
			value: map[string]any{"buckets": []map[string]any{{"resource_type": "sprite", "auto_selected_count": 3}}},
			want:  `{"buckets":[{"auto_selected_count":3,"resource_type":"sprite"}]}`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := CanonicalJSON(testCase.value)
			if err != nil {
				t.Fatalf("CanonicalJSON: %v", err)
			}
			if got != testCase.want {
				t.Fatalf("CanonicalJSON = %s, want %s", got, testCase.want)
			}
		})
	}
}

func TestRequestHashMatchesPython(t *testing.T) {
	// Reference digests produced by CPython:
	//   hashlib.sha256(json.dumps(value, ensure_ascii=False, sort_keys=True,
	//       separators=(',',':')).encode('utf-8')).hexdigest()
	cases := []struct {
		value any
		want  string
	}{
		{
			value: map[string]any{"code": "RUN_CANCELLED", "message": "用户已取消本次生成"},
			want:  "7be60b8bfd7253e506412804083e96533eb36d37ab291dd8043d4f51fe114148",
		},
		{
			value: map[string]any{"b": 1, "a": "中文", "c": []any{}, "d": map[string]any{}, "x": "<a>&b"},
			want:  "49710f27ac48143113913e706f2857899eeb032a0ffd1c43ae1175b71c9aa0e3",
		},
	}
	for _, testCase := range cases {
		got, err := RequestHash(testCase.value)
		if err != nil {
			t.Fatalf("RequestHash: %v", err)
		}
		if got != testCase.want {
			t.Fatalf("RequestHash(%v) = %s, want %s", testCase.value, got, testCase.want)
		}
	}
}

func TestTransitionLifecycle(t *testing.T) {
	store := newTestStore(t, Config{})
	record := mustCreateRun(t, store, testOwner, 1, map[string]any{"scope": "project"})
	steps := []struct {
		target RunStatus
		phase  string
	}{
		{RunStatusSpecifyingTask, "specifying_task"},
		{RunStatusBlueprinting, "blueprinting"},
		{RunStatusPlanningManifest, "planning_manifest"},
		{RunStatusAwaitingToolResult, "awaiting_tool_result"},
		{RunStatusGeneratingPrograms, "generating_programs"},
		{RunStatusReadyToCommit, "ready_to_commit"},
	}
	version := int64(1)
	for index, step := range steps {
		record = mustTransition(t, store, record, step.target, step.phase, "phase_started", map[string]any{"status": string(step.target)})
		version++
		if record.Status != step.target {
			t.Fatalf("step %d status = %s, want %s", index, record.Status, step.target)
		}
		if record.StateVersion != version {
			t.Fatalf("step %d state_version = %d, want %d", index, record.StateVersion, version)
		}
		last := index == len(steps)-1
		if last && record.TerminalAt == nil {
			t.Fatalf("terminal run has no terminal_at")
		}
		if !last && record.TerminalAt != nil {
			t.Fatalf("non-terminal run has terminal_at %d", *record.TerminalAt)
		}
		if last && *record.TerminalAt != record.UpdatedAt {
			t.Fatalf("terminal_at %d != updated_at %d", *record.TerminalAt, record.UpdatedAt)
		}
	}
	events := mustEventsAfter(t, store, record.RunID, testOwner, 0)
	if len(events) != len(steps)+1 {
		t.Fatalf("events = %d, want %d", len(events), len(steps)+1)
	}
	for index, event := range events {
		if event.Sequence != int64(index+1) {
			t.Fatalf("event %d sequence = %d", index, event.Sequence)
		}
	}
	if events[0].Phase != "snapshotting" || events[0].Type != "phase_started" {
		t.Fatalf("first event = %+v", events[0])
	}
	lastEvent := events[len(events)-1]
	if lastEvent.Phase != "ready_to_commit" || lastEvent.Type != "phase_started" {
		t.Fatalf("last event = %+v", lastEvent)
	}
}

func TestTransitionInvalidTarget(t *testing.T) {
	store := newTestStore(t, Config{})
	record := mustCreateRun(t, store, testOwner, 0, nil)
	_, err := store.Transition(TransitionParams{
		RunID:          record.RunID,
		OwnerScopeHash: testOwner,
		Target:         RunStatusReadyToCommit,
		Phase:          "ready_to_commit",
		EventType:      "phase_started",
		Payload:        map[string]any{},
	})
	if err == nil || !errors.Is(err, ErrRunTransition) {
		t.Fatalf("invalid transition = %v, want ErrRunTransition", err)
	}
	if err.Error() != "run status SNAPSHOTTING cannot transition to READY_TO_COMMIT" {
		t.Fatalf("invalid transition message = %q", err.Error())
	}
	if got := len(mustEventsAfter(t, store, record.RunID, testOwner, 0)); got != 1 {
		t.Fatalf("events after rejected transition = %d, want 1", got)
	}
	record = mustTransition(t, store, record, RunStatusFailed, "failed", "generation_error", map[string]any{"code": "MODEL_ERROR"})
	_, err = store.Transition(TransitionParams{
		RunID:          record.RunID,
		OwnerScopeHash: testOwner,
		Target:         RunStatusCancelled,
		Phase:          "cancelled",
		EventType:      "generation_error",
		Payload:        map[string]any{"code": "RUN_CANCELLED"},
	})
	if err == nil || err.Error() != "terminal run status FAILED cannot transition" {
		t.Fatalf("terminal transition = %v", err)
	}
	if got := len(mustEventsAfter(t, store, record.RunID, testOwner, 0)); got != 2 {
		t.Fatalf("events after terminal transition = %d, want 2", got)
	}
}

func TestTransitionToSameStatusAppendsEventWithoutVersionBump(t *testing.T) {
	store := newTestStore(t, Config{})
	record := mustCreateRun(t, store, testOwner, 0, nil)
	same := mustTransition(t, store, record, RunStatusSnapshotting, "snapshotting", "heartbeat", map[string]any{"note": "still going"})
	if same.StateVersion != 1 {
		t.Fatalf("state_version = %d, want 1", same.StateVersion)
	}
	if same.Status != RunStatusSnapshotting {
		t.Fatalf("status = %s", same.Status)
	}
	if same.UpdatedAt != record.UpdatedAt {
		t.Fatalf("updated_at changed for a same-status transition: %d -> %d", record.UpdatedAt, same.UpdatedAt)
	}
	if same.TerminalAt != nil {
		t.Fatalf("terminal_at = %d, want nil", *same.TerminalAt)
	}
	events := mustEventsAfter(t, store, record.RunID, testOwner, 0)
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[1].Type != "heartbeat" || events[1].Sequence != 2 {
		t.Fatalf("second event = %+v", events[1])
	}
}

func TestAppendEvent(t *testing.T) {
	store := newTestStore(t, Config{})
	record := mustCreateRun(t, store, testOwner, 0, nil)
	defaultAttempt, err := store.AppendEvent(AppendEventParams{
		RunID:          record.RunID,
		OwnerScopeHash: testOwner,
		Phase:          "snapshotting",
		EventType:      "snapshot_progress",
		Payload:        map[string]any{"step": 1},
	})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if defaultAttempt.Attempt != 1 {
		t.Fatalf("default attempt = %d, want 1", defaultAttempt.Attempt)
	}
	explicit, err := store.AppendEvent(AppendEventParams{
		RunID:          record.RunID,
		OwnerScopeHash: testOwner,
		Phase:          "snapshotting",
		EventType:      "snapshot_progress",
		Payload:        map[string]any{"step": 2},
		Attempt:        4,
	})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if explicit.Attempt != 4 || explicit.Sequence != 3 {
		t.Fatalf("explicit event = %+v", explicit)
	}
	record = mustTransition(t, store, record, RunStatusFailed, "failed", "generation_error", map[string]any{"code": "X"})
	_, err = store.AppendEvent(AppendEventParams{
		RunID:          record.RunID,
		OwnerScopeHash: testOwner,
		Phase:          "failed",
		EventType:      "late",
		Payload:        map[string]any{},
	})
	if err == nil || !errors.Is(err, ErrRunTransition) || err.Error() != "cannot append an event after a terminal run status" {
		t.Fatalf("AppendEvent after terminal = %v", err)
	}
}

func TestMergeArtifactSummary(t *testing.T) {
	store := newTestStore(t, Config{})
	record := mustCreateRun(t, store, testOwner, 0, map[string]any{"scope": "project", "keep": true})
	merged, err := store.MergeArtifactSummary(MergeArtifactSummaryParams{
		RunID:          record.RunID,
		OwnerScopeHash: testOwner,
		Values: map[string]any{
			"contracts": map[string]any{"task": "private"},
			"scope":     "project-override",
		},
	})
	if err != nil {
		t.Fatalf("MergeArtifactSummary: %v", err)
	}
	want := map[string]any{
		"scope":     "project-override",
		"keep":      true,
		"contracts": map[string]any{"task": "private"},
	}
	assertJSONEqual(t, want, merged.ArtifactSummary)
	if merged.StateVersion != record.StateVersion {
		t.Fatalf("state_version changed: %d -> %d", record.StateVersion, merged.StateVersion)
	}
	if stored := mustGetRun(t, store, record.RunID, testOwner); mustCanonical(t, want) != mustCanonical(t, stored.ArtifactSummary) {
		t.Fatalf("merged summary did not persist")
	}
	record = mustTransition(t, store, record, RunStatusFailed, "failed", "generation_error", map[string]any{"code": "X"})
	_, err = store.MergeArtifactSummary(MergeArtifactSummaryParams{
		RunID:          record.RunID,
		OwnerScopeHash: testOwner,
		Values:         map[string]any{"scope": "nope"},
	})
	if err == nil || !errors.Is(err, ErrRunTransition) || err.Error() != "cannot modify artifacts after a terminal run status" {
		t.Fatalf("MergeArtifactSummary after terminal = %v", err)
	}
}

func acceptParams(runID string) AcceptInteractionParams {
	return AcceptInteractionParams{
		RunID:                runID,
		OwnerScopeHash:       testOwner,
		Operation:            "asset_decision",
		IdempotencyKey:       "key-0123456789abcdef",
		ExpectedProjectEpoch: 0,
		RequiredStatus:       RunStatusSnapshotting,
		Phase:                "awaiting_tool_result",
		EventType:            "asset_resolution_required",
		Payload:              map[string]any{"need_id": "need-1"},
	}
}

func TestAcceptInteractionIdempotentReplay(t *testing.T) {
	store := newTestStore(t, Config{})
	record := mustCreateRun(t, store, testOwner, 0, nil)
	params := acceptParams(record.RunID)
	response, replayed, err := store.AcceptInteraction(params)
	if err != nil {
		t.Fatalf("AcceptInteraction: %v", err)
	}
	if replayed {
		t.Fatal("first call reported a replay")
	}
	eventID, _ := response["event_id"].(string)
	sequence, _ := response["sequence"].(int64)
	if response["accepted"] != true || eventID == "" || sequence != 2 {
		t.Fatalf("response = %v", response)
	}
	events := mustEventsAfter(t, store, record.RunID, testOwner, 0)
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	acceptedEvent := events[1]
	if acceptedEvent.EventID != eventID || acceptedEvent.Sequence != sequence {
		t.Fatalf("event does not match response: %+v vs %v", acceptedEvent, response)
	}
	if acceptedEvent.Phase != "awaiting_tool_result" || acceptedEvent.Type != "asset_resolution_required" {
		t.Fatalf("accepted event = %+v", acceptedEvent)
	}
	assertJSONEqual(t, map[string]any{"need_id": "need-1"}, acceptedEvent.Payload)

	replay, replayedAgain, err := store.AcceptInteraction(params)
	if err != nil {
		t.Fatalf("replay AcceptInteraction: %v", err)
	}
	if !replayedAgain {
		t.Fatal("replay was not reported")
	}
	assertJSONEqual(t, response, replay)
	if got := len(mustEventsAfter(t, store, record.RunID, testOwner, 0)); got != 2 {
		t.Fatalf("events after replay = %d, want 2", got)
	}
	if got := queryTestInt(t, store, "SELECT COUNT(*) FROM ai_run_idempotency WHERE run_id = ?", record.RunID); got != 1 {
		t.Fatalf("idempotency rows = %d, want 1", got)
	}
}

func TestAcceptInteractionIdempotencyConflict(t *testing.T) {
	store := newTestStore(t, Config{})
	record := mustCreateRun(t, store, testOwner, 0, nil)
	params := acceptParams(record.RunID)
	if _, _, err := store.AcceptInteraction(params); err != nil {
		t.Fatalf("AcceptInteraction: %v", err)
	}
	params.Payload = map[string]any{"need_id": "need-2"}
	_, _, err := store.AcceptInteraction(params)
	if err == nil || !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting payload = %v, want ErrIdempotencyConflict", err)
	}
	if err.Error() != "the idempotency key was already used with a different payload" {
		t.Fatalf("conflict message = %q", err.Error())
	}
	var conflict *IdempotencyConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("conflict error type = %T", err)
	}
	if got := len(mustEventsAfter(t, store, record.RunID, testOwner, 0)); got != 2 {
		t.Fatalf("events after conflict = %d, want 2", got)
	}
}

func TestAcceptInteractionRequiresStatus(t *testing.T) {
	store := newTestStore(t, Config{})
	record := mustCreateRun(t, store, testOwner, 0, nil)
	record = mustTransition(t, store, record, RunStatusSpecifyingTask, "specifying_task", "phase_started", map[string]any{})
	_, _, err := store.AcceptInteraction(acceptParams(record.RunID))
	if err == nil || !errors.Is(err, ErrRunTransition) {
		t.Fatalf("wrong status = %v, want ErrRunTransition", err)
	}
	if err.Error() != "asset_decision requires run status SNAPSHOTTING" {
		t.Fatalf("wrong status message = %q", err.Error())
	}
}

func TestAcceptInteractionProjectEpochMismatch(t *testing.T) {
	store := newTestStore(t, Config{})
	record := mustCreateRun(t, store, testOwner, 5, nil)
	params := acceptParams(record.RunID)
	params.ExpectedProjectEpoch = 6
	_, _, err := store.AcceptInteraction(params)
	if err == nil || !errors.Is(err, ErrRunTransition) {
		t.Fatalf("epoch mismatch = %v, want ErrRunTransition", err)
	}
	if err.Error() != "expected_project_epoch does not match the run" {
		t.Fatalf("epoch mismatch message = %q", err.Error())
	}
}

func TestAcceptInteractionToolResults(t *testing.T) {
	store := newTestStore(t, Config{})
	record := mustCreateRun(t, store, testOwner, 0, nil)
	toolCallID := "tool-call-1"
	base := AcceptInteractionParams{
		RunID:                record.RunID,
		OwnerScopeHash:       testOwner,
		Operation:            "tool_result",
		ExpectedProjectEpoch: 0,
		RequiredStatus:       RunStatusSnapshotting,
		Phase:                "awaiting_tool_result",
		EventType:            "tool_result_received",
		Payload:              map[string]any{"op_id": "op-1", "selected_source_kind": "skipped"},
		ToolCallID:           &toolCallID,
	}
	first := base
	first.IdempotencyKey = "key-aaaaaaaaaaaaaaaa"
	response, replayed, err := store.AcceptInteraction(first)
	if err != nil {
		t.Fatalf("first tool result: %v", err)
	}
	if replayed {
		t.Fatal("first tool result reported a replay")
	}

	// A different idempotency key with the identical payload replays the stored
	// tool result without recording another idempotency receipt.
	second := base
	second.IdempotencyKey = "key-bbbbbbbbbbbbbbbb"
	toolReplay, replayed, err := store.AcceptInteraction(second)
	if err != nil {
		t.Fatalf("tool result replay: %v", err)
	}
	if !replayed {
		t.Fatal("identical tool result was not replayed")
	}
	assertJSONEqual(t, response, toolReplay)
	if got := queryTestInt(t, store, "SELECT COUNT(*) FROM ai_run_idempotency WHERE run_id = ? AND idempotency_key = ?", record.RunID, "key-bbbbbbbbbbbbbbbb"); got != 0 {
		t.Fatalf("replayed tool result inserted %d idempotency rows, want 0", got)
	}

	// The same tool call with a different payload is a hard conflict.
	third := base
	third.IdempotencyKey = "key-cccccccccccccccc"
	third.Payload = map[string]any{"op_id": "op-2", "selected_source_kind": "skipped"}
	_, _, err = store.AcceptInteraction(third)
	if err == nil || !errors.Is(err, ErrToolResultConflict) {
		t.Fatalf("tool result conflict = %v, want ErrToolResultConflict", err)
	}
	if err.Error() != "the tool call already has a different final result" {
		t.Fatalf("tool conflict message = %q", err.Error())
	}
	var conflict *ToolResultConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("tool conflict type = %T", err)
	}

	// A different tool call with the same payload is accepted normally.
	otherTool := "tool-call-2"
	fourth := base
	fourth.IdempotencyKey = "key-dddddddddddddddd"
	fourth.ToolCallID = &otherTool
	fourth.Payload = map[string]any{"op_id": "op-2", "selected_source_kind": "skipped"}
	if _, replayed, err = store.AcceptInteraction(fourth); err != nil || replayed {
		t.Fatalf("second tool result = %v replayed=%v", err, replayed)
	}

	accepted, err := store.AcceptedToolCallIds(AcceptedToolCallIdsParams{RunID: record.RunID, OwnerScopeHash: testOwner})
	if err != nil {
		t.Fatalf("AcceptedToolCallIds: %v", err)
	}
	if len(accepted) != 2 {
		t.Fatalf("accepted = %v, want two tool call ids", accepted)
	}
	for _, want := range []string{"tool-call-1", "tool-call-2"} {
		if _, ok := accepted[want]; !ok {
			t.Fatalf("accepted = %v, missing %s", accepted, want)
		}
	}
	if got := len(mustEventsAfter(t, store, record.RunID, testOwner, 0)); got != 3 {
		t.Fatalf("events = %d, want 3", got)
	}
}

func TestEventsAfterCursorAndSequenceForEvent(t *testing.T) {
	store := newTestStore(t, Config{})
	record := mustCreateRun(t, store, testOwner, 0, nil)
	second, err := store.AppendEvent(AppendEventParams{RunID: record.RunID, OwnerScopeHash: testOwner, Phase: "snapshotting", EventType: "second", Payload: map[string]any{"n": 2}})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	third, err := store.AppendEvent(AppendEventParams{RunID: record.RunID, OwnerScopeHash: testOwner, Phase: "snapshotting", EventType: "third", Payload: map[string]any{"n": 3}})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if second.Sequence != 2 || third.Sequence != 3 {
		t.Fatalf("sequences = %d, %d", second.Sequence, third.Sequence)
	}
	events := mustEventsAfter(t, store, record.RunID, testOwner, 0)
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	for index, event := range events {
		if event.Sequence != int64(index+1) {
			t.Fatalf("event %d sequence = %d", index, event.Sequence)
		}
		if event.CreatedAt == 0 {
			t.Fatalf("event %d has no created_at", index)
		}
	}
	tail := mustEventsAfter(t, store, record.RunID, testOwner, 2)
	if len(tail) != 1 || tail[0].EventID != third.EventID {
		t.Fatalf("events after 2 = %+v", tail)
	}
	empty := mustEventsAfter(t, store, record.RunID, testOwner, 99)
	if empty == nil || len(empty) != 0 {
		t.Fatalf("events after 99 = %v, want empty non-nil slice", empty)
	}
	sequence, err := store.SequenceForEvent(SequenceForEventParams{RunID: record.RunID, OwnerScopeHash: testOwner, EventID: second.EventID})
	if err != nil {
		t.Fatalf("SequenceForEvent: %v", err)
	}
	if sequence == nil || *sequence != 2 {
		t.Fatalf("sequence = %v, want 2", sequence)
	}
	missing, err := store.SequenceForEvent(SequenceForEventParams{RunID: record.RunID, OwnerScopeHash: testOwner, EventID: "evt_missing"})
	if err != nil {
		t.Fatalf("SequenceForEvent missing: %v", err)
	}
	if missing != nil {
		t.Fatalf("sequence for unknown event = %v, want nil", *missing)
	}
}

func TestCancelIdempotent(t *testing.T) {
	store := newTestStore(t, Config{})
	record := mustCreateRun(t, store, testOwner, 0, map[string]any{"scope": "project"})
	cancelled, replayed, err := store.CancelIdempotent(CancelIdempotentParams{
		RunID:                record.RunID,
		OwnerScopeHash:       testOwner,
		ExpectedProjectEpoch: 0,
		IdempotencyKey:       "cancel-key-0123456789",
	})
	if err != nil {
		t.Fatalf("CancelIdempotent: %v", err)
	}
	if replayed {
		t.Fatal("first cancel reported a replay")
	}
	if cancelled.Status != RunStatusCancelled {
		t.Fatalf("status = %s, want CANCELLED", cancelled.Status)
	}
	if cancelled.StateVersion != 2 {
		t.Fatalf("state_version = %d, want 2", cancelled.StateVersion)
	}
	if cancelled.TerminalAt == nil || *cancelled.TerminalAt != cancelled.UpdatedAt {
		t.Fatalf("terminal_at = %v, updated_at = %d", cancelled.TerminalAt, cancelled.UpdatedAt)
	}
	events := mustEventsAfter(t, store, record.RunID, testOwner, 0)
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	last := events[1]
	if last.Phase != "cancelled" || last.Type != "generation_error" {
		t.Fatalf("cancel event = %+v", last)
	}
	assertJSONEqual(t, map[string]any{"code": "RUN_CANCELLED", "message": "用户已取消本次生成"}, last.Payload)

	replay, replayed, err := store.CancelIdempotent(CancelIdempotentParams{
		RunID:                record.RunID,
		OwnerScopeHash:       testOwner,
		ExpectedProjectEpoch: 0,
		IdempotencyKey:       "cancel-key-0123456789",
	})
	if err != nil {
		t.Fatalf("cancel replay: %v", err)
	}
	if !replayed {
		t.Fatal("cancel replay was not reported")
	}
	if replay.RunID != cancelled.RunID || replay.Status != cancelled.Status || replay.StateVersion != cancelled.StateVersion {
		t.Fatalf("replayed record = %+v, want %+v", replay, cancelled)
	}
	if got := len(mustEventsAfter(t, store, record.RunID, testOwner, 0)); got != 2 {
		t.Fatalf("events after cancel replay = %d, want 2", got)
	}
}

func TestCancelIdempotentOnTerminalRun(t *testing.T) {
	store := newTestStore(t, Config{})
	record := mustCreateRun(t, store, testOwner, 0, nil)
	record = mustTransition(t, store, record, RunStatusFailed, "failed", "generation_error", map[string]any{"code": "MODEL_ERROR"})
	cancelled, replayed, err := store.CancelIdempotent(CancelIdempotentParams{
		RunID:                record.RunID,
		OwnerScopeHash:       testOwner,
		ExpectedProjectEpoch: 0,
		IdempotencyKey:       "cancel-key-terminal",
	})
	if err != nil {
		t.Fatalf("CancelIdempotent: %v", err)
	}
	if replayed {
		t.Fatal("cancel of a terminal run reported a replay")
	}
	if cancelled.Status != RunStatusFailed || cancelled.StateVersion != record.StateVersion {
		t.Fatalf("terminal cancel changed the run: %+v", cancelled)
	}
	if got := len(mustEventsAfter(t, store, record.RunID, testOwner, 0)); got != 2 {
		t.Fatalf("events = %d, want 2", got)
	}
	if got := queryTestInt(t, store, "SELECT COUNT(*) FROM ai_run_idempotency WHERE run_id = ? AND operation = 'cancel'", record.RunID); got != 1 {
		t.Fatalf("cancel idempotency rows = %d, want 1", got)
	}
}

func TestCancelIdempotentProjectEpochMismatch(t *testing.T) {
	store := newTestStore(t, Config{})
	record := mustCreateRun(t, store, testOwner, 1, nil)
	_, _, err := store.CancelIdempotent(CancelIdempotentParams{
		RunID:                record.RunID,
		OwnerScopeHash:       testOwner,
		ExpectedProjectEpoch: 2,
		IdempotencyKey:       "cancel-key-mismatch",
	})
	if err == nil || !errors.Is(err, ErrRunTransition) || err.Error() != "expected_project_epoch does not match the run" {
		t.Fatalf("cancel epoch mismatch = %v", err)
	}
	if run := mustGetRun(t, store, record.RunID, testOwner); run.Status != RunStatusSnapshotting {
		t.Fatalf("run status changed to %s", run.Status)
	}
}

func TestPurgeExpiredDeletesExpiredTerminalRuns(t *testing.T) {
	store := newTestStore(t, Config{EventRetentionHours: 0, RunRetentionDays: 0})
	terminal := mustCreateRun(t, store, testOwner, 0, nil)
	terminal = mustTransition(t, store, terminal, RunStatusFailed, "failed", "generation_error", map[string]any{"code": "X"})
	active := mustCreateRun(t, store, testOwner, 0, nil)
	execTestSQL(t, store, "UPDATE ai_runs SET expires_at = 0 WHERE run_id IN (?, ?)", terminal.RunID, active.RunID)
	execTestSQL(t, store, "UPDATE ai_run_events SET created_at = 0 WHERE run_id IN (?, ?)", terminal.RunID, active.RunID)
	deleted, err := store.PurgeExpired()
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if _, err := store.GetRun(terminal.RunID, testOwner); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("terminal run survived purge: %v", err)
	}
	if _, err := store.GetRun(active.RunID, testOwner); err != nil {
		t.Fatalf("active run was purged: %v", err)
	}
	if got := queryTestInt(t, store, "SELECT COUNT(*) FROM ai_run_events WHERE run_id = ?", terminal.RunID); got != 0 {
		t.Fatalf("terminal events left behind = %d", got)
	}
}

func TestPurgeExpiredDeletesOldEventsButKeepsRuns(t *testing.T) {
	store := newTestStore(t, Config{EventRetentionHours: 0, RunRetentionDays: 30})
	record := mustCreateRun(t, store, testOwner, 0, nil)
	record = mustTransition(t, store, record, RunStatusFailed, "failed", "generation_error", map[string]any{"code": "X"})
	execTestSQL(t, store, "UPDATE ai_run_events SET created_at = 0 WHERE run_id = ?", record.RunID)
	deleted, err := store.PurgeExpired()
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0", deleted)
	}
	if _, err := store.GetRun(record.RunID, testOwner); err != nil {
		t.Fatalf("unexpired run was purged: %v", err)
	}
	if got := queryTestInt(t, store, "SELECT COUNT(*) FROM ai_run_events WHERE run_id = ?", record.RunID); got != 0 {
		t.Fatalf("old events left behind = %d", got)
	}
}

func TestFailIncompleteRuns(t *testing.T) {
	store := newTestStore(t, Config{})
	failed := mustCreateRun(t, store, testOwner, 0, nil)
	failed = mustTransition(t, store, failed, RunStatusFailed, "failed", "generation_error", map[string]any{"code": "X"})
	cancelled := mustCreateRun(t, store, testOwner, 0, nil)
	if _, _, err := store.CancelIdempotent(CancelIdempotentParams{
		RunID:                cancelled.RunID,
		OwnerScopeHash:       testOwner,
		ExpectedProjectEpoch: 0,
		IdempotencyKey:       "cancel-key-interrupted",
	}); err != nil {
		t.Fatalf("CancelIdempotent: %v", err)
	}
	interrupted := mustCreateRun(t, store, testOwner, 0, nil)
	otherOwner := mustCreateRun(t, store, "another-owner", 0, nil)
	count, err := store.FailIncompleteRuns()
	if err != nil {
		t.Fatalf("FailIncompleteRuns: %v", err)
	}
	if count != 2 {
		t.Fatalf("failed runs = %d, want 2", count)
	}
	for _, want := range []RunRecord{interrupted, otherOwner} {
		run := mustGetRun(t, store, want.RunID, want.OwnerScopeHash)
		if run.Status != RunStatusFailed {
			t.Fatalf("run %s status = %s, want FAILED", run.RunID, run.Status)
		}
		if run.StateVersion != 2 {
			t.Fatalf("run %s state_version = %d, want 2", run.RunID, run.StateVersion)
		}
		if run.TerminalAt == nil || *run.TerminalAt != run.UpdatedAt {
			t.Fatalf("run %s terminal_at = %v", run.RunID, run.TerminalAt)
		}
		events := mustEventsAfter(t, store, run.RunID, want.OwnerScopeHash, 0)
		if len(events) != 2 {
			t.Fatalf("run %s events = %d, want 2", run.RunID, len(events))
		}
		last := events[1]
		if last.Phase != "failed" || last.Type != "generation_error" {
			t.Fatalf("run %s interruption event = %+v", run.RunID, last)
		}
		assertJSONEqual(t, map[string]any{"code": "RUN_INTERRUPTED", "message": "服务重启后不能恢复进行中的模型请求"}, last.Payload)
	}
	if run := mustGetRun(t, store, failed.RunID, testOwner); run.StateVersion != 2 {
		t.Fatalf("already failed run was touched: %+v", run)
	}
	if run := mustGetRun(t, store, cancelled.RunID, testOwner); run.Status != RunStatusCancelled || run.StateVersion != 2 {
		t.Fatalf("cancelled run was touched: %+v", run)
	}
	again, err := store.FailIncompleteRuns()
	if err != nil {
		t.Fatalf("second FailIncompleteRuns: %v", err)
	}
	if again != 0 {
		t.Fatalf("second FailIncompleteRuns = %d, want 0", again)
	}
}

func TestRecordClientCommitMetric(t *testing.T) {
	store := newTestStore(t, Config{})
	record := mustCreateRun(t, store, testOwner, 0, nil)
	params := RecordClientCommitMetricParams{
		RunID:                   record.RunID,
		OwnerScopeHash:          testOwner,
		ExpectedProjectEpoch:    0,
		IdempotencyKey:          "commit-key-1",
		CommitDurationMs:        1000,
		VerifyDurationMs:        500,
		BehaviorTestCount:       10,
		BehaviorPassedCount:     9,
		BehaviorMustCount:       4,
		BehaviorMustPassedCount: 4,
		Outcome:                 "COMPLETED",
		ErrorCode:               nil,
	}
	response, replayed, err := store.RecordClientCommitMetric(params)
	if err != nil {
		t.Fatalf("RecordClientCommitMetric: %v", err)
	}
	if replayed {
		t.Fatal("first commit metric reported a replay")
	}
	assertJSONEqual(t, map[string]any{"accepted": true, "duplicate": false}, response)
	if got := queryTestInt(t, store, "SELECT attempt FROM ai_run_commit_metric_receipts WHERE run_id = ?", record.RunID); got != 1 {
		t.Fatalf("attempt = %d, want 1", got)
	}
	params.IdempotencyKey = "commit-key-2"
	params.Outcome = "FAILED_NEEDS_RECOVERY"
	params.ErrorCode = stringPtr("COMMIT_CONFLICT")
	if _, replayed, err = store.RecordClientCommitMetric(params); err != nil || replayed {
		t.Fatalf("second commit metric = %v replayed=%v", err, replayed)
	}
	replayResponse, replayed, err := store.RecordClientCommitMetric(params)
	if err != nil {
		t.Fatalf("replay commit metric: %v", err)
	}
	if !replayed {
		t.Fatal("commit metric replay was not reported")
	}
	assertJSONEqual(t, map[string]any{"accepted": true, "duplicate": true}, replayResponse)

	params.IdempotencyKey = "commit-key-2"
	params.ErrorCode = stringPtr("COMMIT_FAILED")
	_, _, err = store.RecordClientCommitMetric(params)
	if err == nil || !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("commit metric conflict = %v", err)
	}
	if err.Error() != "the idempotency key was already used with a different client commit metric" {
		t.Fatalf("commit metric conflict message = %q", err.Error())
	}
	params.IdempotencyKey = "commit-key-3"
	params.ExpectedProjectEpoch = 4
	_, _, err = store.RecordClientCommitMetric(params)
	if err == nil || !errors.Is(err, ErrRunTransition) || err.Error() != "expected_project_epoch does not match the run" {
		t.Fatalf("commit metric epoch mismatch = %v", err)
	}
	if got := queryTestInt(t, store, "SELECT COUNT(*) FROM ai_run_commit_metric_receipts WHERE run_id = ?", record.RunID); got != 2 {
		t.Fatalf("commit metric rows = %d, want 2", got)
	}
}

func TestRecordResourceReviewMetric(t *testing.T) {
	store := newTestStore(t, Config{})
	record := mustCreateRun(t, store, testOwner, 0, nil)
	buckets := []map[string]any{
		{
			"resource_type":             "sprite",
			"auto_selected_count":       3,
			"auto_reselected_count":     1,
			"manual_selected_count":     0,
			"review_required_count":     2,
			"generation_fallback_count": 1,
		},
	}
	params := RecordResourceReviewMetricParams{
		RunID:                record.RunID,
		OwnerScopeHash:       testOwner,
		ExpectedProjectEpoch: 0,
		IdempotencyKey:       "review-key-1",
		Buckets:              buckets,
	}
	response, replayed, err := store.RecordResourceReviewMetric(params)
	if err != nil {
		t.Fatalf("RecordResourceReviewMetric: %v", err)
	}
	if replayed {
		t.Fatal("first resource review reported a replay")
	}
	assertJSONEqual(t, map[string]any{"accepted": true, "duplicate": false}, response)
	if _, replayed, err = store.RecordResourceReviewMetric(params); err != nil || !replayed {
		t.Fatalf("resource review replay = %v replayed=%v", err, replayed)
	}
	params.Buckets = []map[string]any{{"resource_type": "sound", "auto_selected_count": 1}}
	_, _, err = store.RecordResourceReviewMetric(params)
	if err == nil || !errors.Is(err, ErrIdempotencyConflict) || err.Error() != "the idempotency key was already used with a different resource review metric" {
		t.Fatalf("resource review conflict = %v", err)
	}
	storedBuckets := queryTestString(t, store, "SELECT buckets_json FROM ai_run_resource_review_metric_receipts WHERE run_id = ?", record.RunID)
	wantBuckets := `[{"auto_reselected_count":1,"auto_selected_count":3,"generation_fallback_count":1,"manual_selected_count":0,"resource_type":"sprite","review_required_count":2}]`
	if storedBuckets != wantBuckets {
		t.Fatalf("stored buckets = %s, want %s", storedBuckets, wantBuckets)
	}
	if _, _, err := store.RecordResourceReviewMetric(RecordResourceReviewMetricParams{
		RunID:                record.RunID,
		OwnerScopeHash:       testOwner,
		ExpectedProjectEpoch: 9,
		IdempotencyKey:       "review-key-3",
		Buckets:              buckets,
	}); err == nil || err.Error() != "expected_project_epoch does not match the run" {
		t.Fatalf("resource review epoch mismatch = %v", err)
	}
}
