// Package runstore is the Go port of the Python durable AI run store
// (backend/app/orchestration/run_state_machine.py and
// backend/app/orchestration/run_store.py).
//
// It keeps a SQLite schema version and table layout identical to the Python
// implementation, writes JSON columns with the same canonical encoding
// (ensure_ascii=False, sort_keys=True, separators=(',',':')) and hashes them
// with SHA-256, and preserves the Python transaction boundaries and idempotency
// semantics.
package runstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// RunStoreSchemaVersion is the schema version this build understands.
//
// Python: RUN_STORE_SCHEMA_VERSION. A database carrying a newer version is
// refused at initialization.
const RunStoreSchemaVersion = 5

// sqliteDriverName is the database/sql driver registered by modernc.org/sqlite.
const sqliteDriverName = "sqlite"

// Sentinel errors. The concrete conflict errors wrap these, so callers can use
// errors.Is to recover the HTTP status mapping performed by _http_error:
//
//	*RunNotFoundError         -> 404 RUN_NOT_FOUND
//	*IdempotencyConflictError -> 409 IDEMPOTENCY_CONFLICT
//	*ToolResultConflictError  -> 409 IDEMPOTENCY_CONFLICT
//	*RunTransitionError       -> 409 RUN_STATE_CONFLICT
var (
	// ErrStoreNotInitialized mirrors RuntimeError('run store has not been initialized').
	ErrStoreNotInitialized = errors.New("run store has not been initialized")
	// ErrSchemaVersionTooNew mirrors RuntimeError('ai run store schema is newer than this application').
	ErrSchemaVersionTooNew = errors.New("ai run store schema is newer than this application")
	// ErrRunNotFound is wrapped by every *RunNotFoundError.
	ErrRunNotFound = errors.New("run not found")
	// ErrIdempotencyConflict is wrapped by every *IdempotencyConflictError.
	ErrIdempotencyConflict = errors.New("idempotency conflict")
	// ErrToolResultConflict is wrapped by every *ToolResultConflictError.
	ErrToolResultConflict = errors.New("tool result conflict")
)

// RunNotFoundError is raised when a run does not exist for the supplied owner
// scope. Like Python, the message is the run id.
//
// Python: RunNotFoundError(LookupError).
type RunNotFoundError struct {
	RunID string
}

// Error implements the error interface.
func (e *RunNotFoundError) Error() string { return e.RunID }

// Unwrap exposes ErrRunNotFound for errors.Is.
func (e *RunNotFoundError) Unwrap() error { return ErrRunNotFound }

// IdempotencyConflictError is raised when an idempotency key was already used
// with a different payload.
//
// Python: IdempotencyConflictError(ValueError).
type IdempotencyConflictError struct {
	Message string
}

// Error implements the error interface.
func (e *IdempotencyConflictError) Error() string {
	if e.Message == "" {
		return "idempotency conflict"
	}
	return e.Message
}

// Unwrap exposes ErrIdempotencyConflict for errors.Is.
func (e *IdempotencyConflictError) Unwrap() error { return ErrIdempotencyConflict }

// ToolResultConflictError is raised when a tool call already has a different
// final result.
//
// Python: ToolResultConflictError(ValueError).
type ToolResultConflictError struct {
	Message string
}

// Error implements the error interface.
func (e *ToolResultConflictError) Error() string {
	if e.Message == "" {
		return "tool result conflict"
	}
	return e.Message
}

// Unwrap exposes ErrToolResultConflict for errors.Is.
func (e *ToolResultConflictError) Unwrap() error { return ErrToolResultConflict }

// Config is the subset of application settings the run store needs.
//
// Python: RunStore(database_path, event_retention_hours=..., run_retention_days=...).
type Config struct {
	DatabasePath          string
	EventRetentionHours   int
	RunRetentionDays      int
	MaxActiveRuns         int
	MaxActiveRunsPerOwner int
}

// RunRecord is one durable run row.
//
// Python: the frozen RunRecord dataclass.
type RunRecord struct {
	RunID           string
	OwnerScopeHash  string
	Status          RunStatus
	StateVersion    int64
	ProtocolVersion string
	ProjectEpoch    int64
	ArtifactSummary map[string]any
	CreatedAt       int64
	UpdatedAt       int64
	ExpiresAt       int64
	TerminalAt      *int64
}

// PublicPayload returns the sanitized status payload for status/SSE-adjacent
// APIs.
//
// Contracts are retained privately for the live run manager, but must never be
// returned by status APIs: they can contain the student's task wording,
// resource names, and project semantics.
//
// Python: RunRecord.public_payload().
func (r RunRecord) PublicPayload() map[string]any {
	summary := map[string]any{}
	for _, key := range []string{"scope", "output_contract"} {
		if value, ok := r.ArtifactSummary[key].(string); ok {
			summary[key] = value
		}
	}
	if manifestSummary, ok := jsonObject(r.ArtifactSummary["manifest_summary"]); ok {
		counts := map[string]any{}
		for _, key := range []string{"program_target_count", "resource_op_count"} {
			if value, ok := jsonInt64(manifestSummary[key]); ok {
				counts[key] = value
			}
		}
		if len(counts) > 0 {
			summary["manifest_summary"] = counts
		}
	}
	if count, ok := jsonListLength(r.ArtifactSummary["asset_need_ids"]); ok {
		summary["asset_need_count"] = count
	}
	return map[string]any{
		"protocol_version": r.ProtocolVersion,
		"run_id":           r.RunID,
		"status":           string(r.Status),
		"state_version":    r.StateVersion,
		"project_epoch":    r.ProjectEpoch,
		"artifact_summary": summary,
		"created_at":       r.CreatedAt,
		"updated_at":       r.UpdatedAt,
		"expires_at":       r.ExpiresAt,
		"terminal":         IsTerminal(r.Status),
	}
}

// RunEvent is one replayable protocol event.
//
// Python: the frozen RunEvent dataclass.
type RunEvent struct {
	RunID     string
	EventID   string
	Sequence  int64
	Phase     string
	Type      string
	Attempt   int64
	Payload   map[string]any
	CreatedAt int64
}

// Envelope returns the SSE/JSON envelope for this event.
//
// Python: RunEvent.envelope().
func (e RunEvent) Envelope() map[string]any {
	return map[string]any{
		"protocol_version": RunProtocolVersion,
		"run_id":           e.RunID,
		"event_id":         e.EventID,
		"sequence":         e.Sequence,
		"phase":            e.Phase,
		"type":             e.Type,
		"attempt":          e.Attempt,
		"payload":          e.Payload,
	}
}

// CanonicalJSON encodes value exactly like the Python canonical_json helper:
// json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',',':')).
//
// encoding/json sorts object keys (byte order, which for UTF-8 equals Python's
// code point order) and the encoder is configured with SetEscapeHTML(false) so
// that <, > and & are emitted literally like Python does. The trailing newline
// that json.Encoder appends is removed.
func CanonicalJSON(value any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buffer.String(), "\n"), nil
}

// RequestHash returns the SHA-256 hex digest of CanonicalJSON(value).
//
// Python: request_hash().
func RequestHash(value any) (string, error) {
	canonical, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	return sha256Hex(canonical), nil
}

// RunStore is the SQLite-backed durable run store.
//
// Python: RunStore. Python guards a single shared connection with an RLock; Go
// uses a database/sql pool capped at one connection plus the same exclusive
// mutex, and the driver is opened with _txlock=immediate so every transaction
// starts with BEGIN IMMEDIATE exactly like the Python _transaction helper.
type RunStore struct {
	databasePath          string
	eventRetentionMs      int64
	runRetentionMs        int64
	maxActiveRuns         int
	maxActiveRunsPerOwner int

	mu sync.Mutex
	db *sql.DB
}

// NewStore builds a run store. It does not touch the database; call Initialize
// to create the schema and open the pool.
//
// Python: RunStore.__init__.
func NewStore(cfg Config) (*RunStore, error) {
	if strings.TrimSpace(cfg.DatabasePath) == "" {
		return nil, errors.New("runstore: DatabasePath is required")
	}
	if cfg.MaxActiveRuns <= 0 {
		cfg.MaxActiveRuns = 32
	}
	if cfg.MaxActiveRunsPerOwner <= 0 {
		cfg.MaxActiveRunsPerOwner = 8
	}
	return &RunStore{
		maxActiveRuns: cfg.MaxActiveRuns, maxActiveRunsPerOwner: cfg.MaxActiveRunsPerOwner,
		databasePath:     cfg.DatabasePath,
		eventRetentionMs: int64(cfg.EventRetentionHours) * 60 * 60 * 1000,
		runRetentionMs:   int64(cfg.RunRetentionDays) * 24 * 60 * 60 * 1000,
	}, nil
}

// Initialize opens the database, applies the connection pragmas and creates or
// migrates the schema. It is idempotent, matching Python's early return when a
// connection already exists.
//
// Python: RunStore.initialize().
func (s *RunStore) Initialize() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.databasePath), 0o755); err != nil {
		return err
	}
	dsn, err := s.dsn()
	if err != nil {
		return err
	}
	db, err := sql.Open(sqliteDriverName, dsn)
	if err != nil {
		return err
	}
	// Python serializes every statement through one connection guarded by a
	// lock; one pooled connection reproduces that, and the mutex in this type
	// serializes transactions.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return err
	}
	if err := initializeSchema(db); err != nil {
		db.Close()
		return err
	}
	s.db = db
	return nil
}

// Close releases the connection pool. It is safe to call more than once.
//
// Python: RunStore.close().
func (s *RunStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// CreateRun inserts a new run in SNAPSHOTTING state and appends its first
// phase_started event in the same transaction.
//
// Python: create_run().
func (s *RunStore) CreateRun(params CreateRunParams) (RunRecord, error) {
	now := nowMillis()
	runID, err := newUUIDHex()
	if err != nil {
		return RunRecord{}, err
	}
	runID = "run_" + runID
	summaryJSON, err := CanonicalJSON(params.ArtifactSummary)
	if err != nil {
		return RunRecord{}, err
	}
	err = s.transaction(func(tx *sql.Tx) error {
		ctx := context.Background()
		var active, owned int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(owner_scope_hash = ?), 0) FROM ai_runs WHERE status NOT IN ('READY_TO_COMMIT', 'CANCELLED', 'FAILED')`, params.OwnerScopeHash).Scan(&active, &owned); err != nil {
			return err
		}
		if active >= s.maxActiveRuns || owned >= s.maxActiveRunsPerOwner {
			return ErrRunCapacity
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO ai_runs(
				run_id, owner_scope_hash, status, state_version, protocol_version, project_epoch,
				artifact_summary_json, created_at, updated_at, expires_at, terminal_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
			runID,
			params.OwnerScopeHash,
			string(RunStatusSnapshotting),
			1,
			RunProtocolVersion,
			params.ProjectEpoch,
			summaryJSON,
			now,
			now,
			now+s.runRetentionMs,
		); err != nil {
			return err
		}
		_, err := s.insertEvent(ctx, tx, insertEventArgs{
			runID:     runID,
			phase:     "snapshotting",
			eventType: "phase_started",
			payload:   map[string]any{"status": string(RunStatusSnapshotting)},
			attempt:   1,
		})
		return err
	})
	if err != nil {
		return RunRecord{}, err
	}
	return s.GetRun(runID, params.OwnerScopeHash)
}

// GetRun loads one run for an owner scope.
//
// Python: get_run().
func (s *RunStore) GetRun(runID, ownerScopeHash string) (RunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := s.handle()
	if err != nil {
		return RunRecord{}, err
	}
	return s.getRow(context.Background(), db, runID, ownerScopeHash)
}

// Transition changes a run status (when it differs) and appends the protocol
// event in the same transaction.
//
// Python: transition().
func (s *RunStore) Transition(params TransitionParams) (RunRecord, error) {
	now := nowMillis()
	attempt := params.Attempt
	if attempt == 0 {
		attempt = 1
	}
	err := s.transaction(func(tx *sql.Tx) error {
		ctx := context.Background()
		row, err := s.getRow(ctx, tx, params.RunID, params.OwnerScopeHash)
		if err != nil {
			return err
		}
		if err := AssertTransition(row.Status, params.Target); err != nil {
			return err
		}
		if row.Status != params.Target {
			var terminalAt any
			if IsTerminal(params.Target) {
				terminalAt = now
			}
			updated, err := tx.ExecContext(ctx, `
				UPDATE ai_runs
				SET status = ?, state_version = state_version + 1, updated_at = ?, terminal_at = ?
				WHERE run_id = ? AND owner_scope_hash = ? AND state_version = ?`,
				string(params.Target), now, terminalAt, params.RunID, params.OwnerScopeHash, row.StateVersion,
			)
			if err != nil {
				return err
			}
			affected, err := updated.RowsAffected()
			if err != nil {
				return err
			}
			if affected != 1 {
				return NewRunTransitionError("concurrent run state update")
			}
		}
		_, err = s.insertEvent(ctx, tx, insertEventArgs{
			runID:     params.RunID,
			phase:     params.Phase,
			eventType: params.EventType,
			payload:   params.Payload,
			attempt:   attempt,
		})
		return err
	})
	if err != nil {
		return RunRecord{}, err
	}
	return s.GetRun(params.RunID, params.OwnerScopeHash)
}

// AppendEvent appends a protocol event to a non-terminal run.
//
// Python: append_event().
func (s *RunStore) AppendEvent(params AppendEventParams) (RunEvent, error) {
	attempt := params.Attempt
	if attempt == 0 {
		attempt = 1
	}
	var event RunEvent
	err := s.transaction(func(tx *sql.Tx) error {
		ctx := context.Background()
		row, err := s.getRow(ctx, tx, params.RunID, params.OwnerScopeHash)
		if err != nil {
			return err
		}
		if IsTerminal(row.Status) {
			return NewRunTransitionError("cannot append an event after a terminal run status")
		}
		event, err = s.insertEvent(ctx, tx, insertEventArgs{
			runID:     params.RunID,
			phase:     params.Phase,
			eventType: params.EventType,
			payload:   params.Payload,
			attempt:   attempt,
		})
		return err
	})
	return event, err
}

// MergeArtifactSummary merges values into the stored artifact summary of a
// non-terminal run.
//
// Python: merge_artifact_summary().
func (s *RunStore) MergeArtifactSummary(params MergeArtifactSummaryParams) (RunRecord, error) {
	now := nowMillis()
	err := s.transaction(func(tx *sql.Tx) error {
		ctx := context.Background()
		row, err := s.getRow(ctx, tx, params.RunID, params.OwnerScopeHash)
		if err != nil {
			return err
		}
		if IsTerminal(row.Status) {
			return NewRunTransitionError("cannot modify artifacts after a terminal run status")
		}
		summary := row.ArtifactSummary
		if summary == nil {
			summary = map[string]any{}
		}
		for key, value := range params.Values {
			summary[key] = value
		}
		serialized, err := CanonicalJSON(summary)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE ai_runs SET artifact_summary_json = ?, updated_at = ?
			WHERE run_id = ? AND owner_scope_hash = ?`,
			serialized, now, params.RunID, params.OwnerScopeHash,
		)
		return err
	})
	if err != nil {
		return RunRecord{}, err
	}
	return s.GetRun(params.RunID, params.OwnerScopeHash)
}

// AcceptedToolCallIds returns the tool call ids that already have a final
// result for a run.
//
// Python: accepted_tool_call_ids(), which returns set[str]; Go uses the
// idiomatic map[string]struct{} set.
func (s *RunStore) AcceptedToolCallIds(params AcceptedToolCallIdsParams) (map[string]struct{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := s.handle()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	if _, err := s.getRow(ctx, db, params.RunID, params.OwnerScopeHash); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, "SELECT tool_call_id FROM ai_run_tool_results WHERE run_id = ?", params.RunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accepted := map[string]struct{}{}
	for rows.Next() {
		var toolCallID string
		if err := rows.Scan(&toolCallID); err != nil {
			return nil, err
		}
		accepted[toolCallID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return accepted, nil
}

// EventsAfter returns the events of a run with a sequence greater than the
// supplied cursor, ordered by sequence.
//
// Python: events_after().
func (s *RunStore) EventsAfter(params EventsAfterParams) ([]RunEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := s.handle()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	if _, err := s.getRow(ctx, db, params.RunID, params.OwnerScopeHash); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT event_id, sequence, phase, type, attempt, payload_json, created_at
		FROM ai_run_events WHERE run_id = ? AND sequence > ? ORDER BY sequence ASC`,
		params.RunID, params.Sequence,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]RunEvent, 0)
	for rows.Next() {
		event, err := scanRunEvent(params.RunID, rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

// SequenceForEvent resolves an event id to its sequence, or nil when the event
// does not exist.
//
// Python: sequence_for_event() -> int | None.
func (s *RunStore) SequenceForEvent(params SequenceForEventParams) (*int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := s.handle()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	if _, err := s.getRow(ctx, db, params.RunID, params.OwnerScopeHash); err != nil {
		return nil, err
	}
	var sequence int64
	err = db.QueryRowContext(ctx,
		"SELECT sequence FROM ai_run_events WHERE run_id = ? AND event_id = ?",
		params.RunID, params.EventID,
	).Scan(&sequence)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sequence, nil
}

// AcceptInteraction persists an input receipt and its protocol event in one
// transaction. It returns the stored response and whether it was replayed from
// an earlier identical request.
//
// Python: accept_interaction() -> (response, replayed).
func (s *RunStore) AcceptInteraction(params AcceptInteractionParams) (map[string]any, bool, error) {
	payloadHash, err := RequestHash(params.Payload)
	if err != nil {
		return nil, false, err
	}
	var response map[string]any
	replayed := false
	err = s.transaction(func(tx *sql.Tx) error {
		ctx := context.Background()
		row, err := s.getRow(ctx, tx, params.RunID, params.OwnerScopeHash)
		if err != nil {
			return err
		}
		if row.ProjectEpoch != params.ExpectedProjectEpoch {
			return NewRunTransitionError("expected_project_epoch does not match the run")
		}
		var existingHash, existingResponse string
		err = tx.QueryRowContext(ctx, `
			SELECT request_hash, response_json FROM ai_run_idempotency
			WHERE run_id = ? AND operation = ? AND idempotency_key = ?`,
			params.RunID, params.Operation, params.IdempotencyKey,
		).Scan(&existingHash, &existingResponse)
		if err == nil {
			if existingHash != payloadHash {
				return &IdempotencyConflictError{Message: "the idempotency key was already used with a different payload"}
			}
			decoded, err := decodeJSONObject(existingResponse)
			if err != nil {
				return err
			}
			response, replayed = decoded, true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if row.Status != params.RequiredStatus {
			return NewRunTransitionError("%s requires run status %s", params.Operation, params.RequiredStatus)
		}
		if params.ToolCallID != nil {
			var toolHash, toolResponse string
			err = tx.QueryRowContext(ctx, `
				SELECT request_hash, response_json FROM ai_run_tool_results
				WHERE run_id = ? AND tool_call_id = ?`,
				params.RunID, *params.ToolCallID,
			).Scan(&toolHash, &toolResponse)
			if err == nil {
				if toolHash != payloadHash {
					return &ToolResultConflictError{Message: "the tool call already has a different final result"}
				}
				decoded, err := decodeJSONObject(toolResponse)
				if err != nil {
					return err
				}
				response, replayed = decoded, true
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		event, err := s.insertEvent(ctx, tx, insertEventArgs{
			runID:     params.RunID,
			phase:     params.Phase,
			eventType: params.EventType,
			payload:   params.Payload,
			attempt:   1,
		})
		if err != nil {
			return err
		}
		response = map[string]any{"accepted": true, "event_id": event.EventID, "sequence": event.Sequence}
		serialized, err := CanonicalJSON(response)
		if err != nil {
			return err
		}
		now := nowMillis()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO ai_run_idempotency(run_id, operation, idempotency_key, request_hash, response_json, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			params.RunID, params.Operation, params.IdempotencyKey, payloadHash, serialized, now,
		); err != nil {
			return err
		}
		if params.ToolCallID != nil {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO ai_run_tool_results(run_id, tool_call_id, request_hash, response_json, created_at)
				VALUES (?, ?, ?, ?, ?)`,
				params.RunID, *params.ToolCallID, payloadHash, serialized, now,
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return response, replayed, nil
}

// CancelIdempotent cancels a run once, replaying the stored response for a
// repeated idempotency key.
//
// Python: cancel_idempotent() -> (RunRecord, replayed).
func (s *RunStore) CancelIdempotent(params CancelIdempotentParams) (RunRecord, bool, error) {
	payload := map[string]any{"expected_project_epoch": params.ExpectedProjectEpoch}
	payloadHash, err := RequestHash(payload)
	if err != nil {
		return RunRecord{}, false, err
	}
	var record RunRecord
	replayed := false
	err = s.transaction(func(tx *sql.Tx) error {
		ctx := context.Background()
		row, err := s.getRow(ctx, tx, params.RunID, params.OwnerScopeHash)
		if err != nil {
			return err
		}
		if row.ProjectEpoch != params.ExpectedProjectEpoch {
			return NewRunTransitionError("expected_project_epoch does not match the run")
		}
		var existingHash, existingResponse string
		err = tx.QueryRowContext(ctx, `
			SELECT request_hash, response_json FROM ai_run_idempotency
			WHERE run_id = ? AND operation = 'cancel' AND idempotency_key = ?`,
			params.RunID, params.IdempotencyKey,
		).Scan(&existingHash, &existingResponse)
		if err == nil {
			if existingHash != payloadHash {
				return &IdempotencyConflictError{Message: "the idempotency key was already used with a different payload"}
			}
			record, replayed = row, true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		now := nowMillis()
		if !IsTerminal(row.Status) {
			updated, err := tx.ExecContext(ctx, `
				UPDATE ai_runs SET status = ?, state_version = state_version + 1, updated_at = ?, terminal_at = ?
				WHERE run_id = ? AND owner_scope_hash = ? AND state_version = ?`,
				string(RunStatusCancelled), now, now, params.RunID, params.OwnerScopeHash, row.StateVersion,
			)
			if err != nil {
				return err
			}
			affected, err := updated.RowsAffected()
			if err != nil {
				return err
			}
			if affected != 1 {
				return NewRunTransitionError("concurrent run state update")
			}
			row, err = s.getRow(ctx, tx, params.RunID, params.OwnerScopeHash)
			if err != nil {
				return err
			}
			if _, err := s.insertEvent(ctx, tx, insertEventArgs{
				runID:     params.RunID,
				phase:     "cancelled",
				eventType: "generation_error",
				payload:   map[string]any{"code": "RUN_CANCELLED", "message": "用户已取消本次生成"},
				attempt:   1,
			}); err != nil {
				return err
			}
		}
		response, err := CanonicalJSON(row.PublicPayload())
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO ai_run_idempotency(run_id, operation, idempotency_key, request_hash, response_json, created_at)
			VALUES (?, 'cancel', ?, ?, ?, ?)`,
			params.RunID, params.IdempotencyKey, payloadHash, response, now,
		)
		return err
	})
	if err != nil {
		return RunRecord{}, false, err
	}
	if replayed {
		return record, true, nil
	}
	record, err = s.GetRun(params.RunID, params.OwnerScopeHash)
	if err != nil {
		return RunRecord{}, false, err
	}
	return record, false, nil
}

// PurgeExpired deletes old events of terminal runs and expired terminal runs.
// It returns the number of deleted runs, like the Python cursor rowcount.
//
// Python: purge_expired() -> int.
func (s *RunStore) PurgeExpired() (int, error) {
	now := nowMillis()
	terminalValues := []any{
		string(RunStatusReadyToCommit),
		string(RunStatusCancelled),
		string(RunStatusFailed),
	}
	deleted := 0
	err := s.transaction(func(tx *sql.Tx) error {
		ctx := context.Background()
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM ai_run_events
			WHERE created_at < ? AND run_id IN (
				SELECT run_id FROM ai_runs WHERE status IN (?, ?, ?)
			)`,
			append([]any{now - s.eventRetentionMs}, terminalValues...)...,
		); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
			DELETE FROM ai_runs WHERE expires_at < ? AND status IN (?, ?, ?)`,
			append([]any{now}, terminalValues...)...,
		)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		deleted = int(affected)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

// FailIncompleteRuns marks every non-terminal run as FAILED and appends the
// interruption event. A provider request is intentionally not resumed after a
// process restart.
//
// Python: fail_incomplete_runs() -> int.
func (s *RunStore) FailIncompleteRuns() (int, error) {
	now := nowMillis()
	terminalValues := []any{
		string(RunStatusReadyToCommit),
		string(RunStatusCancelled),
		string(RunStatusFailed),
	}
	type incompleteRun struct {
		runID          string
		ownerScopeHash string
		stateVersion   int64
	}
	failed := 0
	err := s.transaction(func(tx *sql.Tx) error {
		ctx := context.Background()
		rows, err := tx.QueryContext(ctx,
			"SELECT run_id, owner_scope_hash, state_version FROM ai_runs WHERE status NOT IN (?, ?, ?)",
			terminalValues...,
		)
		if err != nil {
			return err
		}
		incomplete := make([]incompleteRun, 0)
		for rows.Next() {
			var item incompleteRun
			if err := rows.Scan(&item.runID, &item.ownerScopeHash, &item.stateVersion); err != nil {
				rows.Close()
				return err
			}
			incomplete = append(incomplete, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, item := range incomplete {
			if _, err := tx.ExecContext(ctx, `
				UPDATE ai_runs SET status = ?, state_version = state_version + 1, updated_at = ?, terminal_at = ?
				WHERE run_id = ? AND owner_scope_hash = ? AND state_version = ?`,
				string(RunStatusFailed), now, now, item.runID, item.ownerScopeHash, item.stateVersion,
			); err != nil {
				return err
			}
			if _, err := s.insertEvent(ctx, tx, insertEventArgs{
				runID:     item.runID,
				phase:     "failed",
				eventType: "generation_error",
				payload:   map[string]any{"code": "RUN_INTERRUPTED", "message": "服务重启后不能恢复进行中的模型请求"},
				attempt:   1,
			}); err != nil {
				return err
			}
		}
		failed = len(incomplete)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return failed, nil
}

// RecordClientCommitMetric persists one payload-free browser Commit/Verify
// outcome for a durable run.
//
// Python: record_client_commit_metric() -> (response, replayed).
func (s *RunStore) RecordClientCommitMetric(params RecordClientCommitMetricParams) (map[string]any, bool, error) {
	payload := map[string]any{
		"expected_project_epoch":     params.ExpectedProjectEpoch,
		"commit_duration_ms":         params.CommitDurationMs,
		"verify_duration_ms":         params.VerifyDurationMs,
		"behavior_test_count":        params.BehaviorTestCount,
		"behavior_passed_count":      params.BehaviorPassedCount,
		"behavior_must_count":        params.BehaviorMustCount,
		"behavior_must_passed_count": params.BehaviorMustPassedCount,
		"outcome":                    params.Outcome,
		"error_code":                 params.ErrorCode,
	}
	payloadHash, err := RequestHash(payload)
	if err != nil {
		return nil, false, err
	}
	replayed := false
	err = s.transaction(func(tx *sql.Tx) error {
		ctx := context.Background()
		row, err := s.getRow(ctx, tx, params.RunID, params.OwnerScopeHash)
		if err != nil {
			return err
		}
		if row.ProjectEpoch != params.ExpectedProjectEpoch {
			return NewRunTransitionError("expected_project_epoch does not match the run")
		}
		var existingHash string
		err = tx.QueryRowContext(ctx, `
			SELECT request_hash FROM ai_run_commit_metric_receipts
			WHERE run_id = ? AND idempotency_key = ?`,
			params.RunID, params.IdempotencyKey,
		).Scan(&existingHash)
		if err == nil {
			if existingHash != payloadHash {
				return &IdempotencyConflictError{Message: "the idempotency key was already used with a different client commit metric"}
			}
			replayed = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var attempt int64
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(attempt), 0) + 1 AS attempt FROM ai_run_commit_metric_receipts WHERE run_id = ?`,
			params.RunID,
		).Scan(&attempt); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO ai_run_commit_metric_receipts(
				run_id, attempt, idempotency_key, request_hash, commit_duration_ms,
				verify_duration_ms, behavior_test_count, behavior_passed_count,
				behavior_must_count, behavior_must_passed_count, outcome, error_code, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			params.RunID, attempt, params.IdempotencyKey, payloadHash, params.CommitDurationMs,
			params.VerifyDurationMs, params.BehaviorTestCount, params.BehaviorPassedCount,
			params.BehaviorMustCount, params.BehaviorMustPassedCount, params.Outcome, params.ErrorCode, nowMillis(),
		)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return map[string]any{"accepted": true, "duplicate": replayed}, replayed, nil
}

// RecordResourceReviewMetric persists an aggregate-only resource review outcome
// for a durable run.
//
// Python: record_resource_review_metric() -> (response, replayed).
func (s *RunStore) RecordResourceReviewMetric(params RecordResourceReviewMetricParams) (map[string]any, bool, error) {
	buckets := params.Buckets
	if buckets == nil {
		buckets = []map[string]any{}
	}
	payload := map[string]any{"expected_project_epoch": params.ExpectedProjectEpoch, "buckets": buckets}
	payloadHash, err := RequestHash(payload)
	if err != nil {
		return nil, false, err
	}
	replayed := false
	err = s.transaction(func(tx *sql.Tx) error {
		ctx := context.Background()
		row, err := s.getRow(ctx, tx, params.RunID, params.OwnerScopeHash)
		if err != nil {
			return err
		}
		if row.ProjectEpoch != params.ExpectedProjectEpoch {
			return NewRunTransitionError("expected_project_epoch does not match the run")
		}
		var existingHash string
		err = tx.QueryRowContext(ctx, `
			SELECT request_hash FROM ai_run_resource_review_metric_receipts
			WHERE run_id = ? AND idempotency_key = ?`,
			params.RunID, params.IdempotencyKey,
		).Scan(&existingHash)
		if err == nil {
			if existingHash != payloadHash {
				return &IdempotencyConflictError{Message: "the idempotency key was already used with a different resource review metric"}
			}
			replayed = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var attempt int64
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(attempt), 0) + 1 AS attempt FROM ai_run_resource_review_metric_receipts WHERE run_id = ?`,
			params.RunID,
		).Scan(&attempt); err != nil {
			return err
		}
		serialized, err := CanonicalJSON(buckets)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO ai_run_resource_review_metric_receipts(
				run_id, attempt, idempotency_key, request_hash, buckets_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?)`,
			params.RunID, attempt, params.IdempotencyKey, payloadHash, serialized, nowMillis(),
		)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return map[string]any{"accepted": true, "duplicate": replayed}, replayed, nil
}

// sqlQuerier is satisfied by *sql.DB and *sql.Tx.
type sqlQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type insertEventArgs struct {
	runID     string
	phase     string
	eventType string
	payload   map[string]any
	attempt   int64
}

// insertEvent computes the next per-run sequence and inserts the event,
// mirroring Python's _insert_event.
func (s *RunStore) insertEvent(ctx context.Context, q sqlQuerier, args insertEventArgs) (RunEvent, error) {
	var sequence int64
	if err := q.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(sequence), 0) + 1 AS next_sequence FROM ai_run_events WHERE run_id = ?",
		args.runID,
	).Scan(&sequence); err != nil {
		return RunEvent{}, err
	}
	eventID, err := newUUIDHex()
	if err != nil {
		return RunEvent{}, err
	}
	payloadJSON, err := CanonicalJSON(args.payload)
	if err != nil {
		return RunEvent{}, err
	}
	event := RunEvent{
		RunID:     args.runID,
		EventID:   "evt_" + eventID,
		Sequence:  sequence,
		Phase:     args.phase,
		Type:      args.eventType,
		Attempt:   args.attempt,
		Payload:   args.payload,
		CreatedAt: nowMillis(),
	}
	if _, err := q.ExecContext(ctx,
		`INSERT INTO ai_run_events(run_id, sequence, event_id, phase, type, attempt, payload_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.RunID, event.Sequence, event.EventID, event.Phase, event.Type, event.Attempt, payloadJSON, event.CreatedAt,
	); err != nil {
		return RunEvent{}, err
	}
	return event, nil
}

const runSelectColumns = "run_id, owner_scope_hash, status, state_version, protocol_version, project_epoch, artifact_summary_json, created_at, updated_at, expires_at, terminal_at"

// getRow loads one run row or returns *RunNotFoundError, mirroring _get_row.
func (s *RunStore) getRow(ctx context.Context, q sqlQuerier, runID, ownerScopeHash string) (RunRecord, error) {
	row := q.QueryRowContext(ctx,
		"SELECT "+runSelectColumns+" FROM ai_runs WHERE run_id = ? AND owner_scope_hash = ?",
		runID, ownerScopeHash,
	)
	record, err := scanRunRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RunRecord{}, &RunNotFoundError{RunID: runID}
	}
	if err != nil {
		return RunRecord{}, err
	}
	return record, nil
}

// scanRunRecord converts one ai_runs row into a RunRecord.
func scanRunRecord(row *sql.Row) (RunRecord, error) {
	var (
		record      RunRecord
		status      string
		summaryJSON string
		terminalAt  sql.NullInt64
	)
	if err := row.Scan(
		&record.RunID,
		&record.OwnerScopeHash,
		&status,
		&record.StateVersion,
		&record.ProtocolVersion,
		&record.ProjectEpoch,
		&summaryJSON,
		&record.CreatedAt,
		&record.UpdatedAt,
		&record.ExpiresAt,
		&terminalAt,
	); err != nil {
		return RunRecord{}, err
	}
	parsedStatus, err := ParseRunStatus(status)
	if err != nil {
		return RunRecord{}, err
	}
	record.Status = parsedStatus
	summary, err := decodeJSONObject(summaryJSON)
	if err != nil {
		return RunRecord{}, err
	}
	record.ArtifactSummary = summary
	if terminalAt.Valid {
		value := terminalAt.Int64
		record.TerminalAt = &value
	}
	return record, nil
}

// scanRunEvent converts one ai_run_events row into a RunEvent.
func scanRunEvent(runID string, rows *sql.Rows) (RunEvent, error) {
	var (
		event       RunEvent
		payloadJSON string
	)
	event.RunID = runID
	if err := rows.Scan(&event.EventID, &event.Sequence, &event.Phase, &event.Type, &event.Attempt, &payloadJSON, &event.CreatedAt); err != nil {
		return RunEvent{}, err
	}
	payload, err := decodeJSONObject(payloadJSON)
	if err != nil {
		return RunEvent{}, err
	}
	event.Payload = payload
	return event, nil
}

// transaction runs fn inside a BEGIN IMMEDIATE transaction while holding the
// store lock, committing on success and rolling back on error, exactly like the
// Python _transaction context manager.
func (s *RunStore) transaction(fn func(tx *sql.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := s.handle()
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// handle returns the pool or the not-initialized error, mirroring _conn().
func (s *RunStore) handle() (*sql.DB, error) {
	if s.db == nil {
		return nil, ErrStoreNotInitialized
	}
	return s.db, nil
}

// dsn builds the modernc.org/sqlite DSN with the same connection pragmas the
// Python store sets explicitly, plus immediate transaction locking to match
// BEGIN IMMEDIATE.
func (s *RunStore) dsn() (string, error) {
	absolute, err := filepath.Abs(s.databasePath)
	if err != nil {
		return "", err
	}
	query := url.Values{}
	query.Set("_txlock", "immediate")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(ON)")
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute), RawQuery: query.Encode()}
	return uri.String(), nil
}

// initializeSchema creates and migrates the tables, mirroring the statement
// order (and the autocommit behaviour) of RunStore.initialize().
func initializeSchema(db *sql.DB) error {
	ctx := context.Background()
	statements := []string{
		`
		CREATE TABLE IF NOT EXISTS ai_run_schema (
			version INTEGER PRIMARY KEY
		)`,
		`
		CREATE TABLE IF NOT EXISTS ai_runs (
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
		`
		CREATE TABLE IF NOT EXISTS ai_run_events (
			run_id TEXT NOT NULL REFERENCES ai_runs(run_id) ON DELETE CASCADE,
			sequence INTEGER NOT NULL,
			event_id TEXT NOT NULL UNIQUE,
			phase TEXT NOT NULL,
			type TEXT NOT NULL,
			attempt INTEGER NOT NULL,
			payload_json TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (run_id, sequence)
		)`,
		`
		CREATE TABLE IF NOT EXISTS ai_run_idempotency (
			run_id TEXT NOT NULL REFERENCES ai_runs(run_id) ON DELETE CASCADE,
			operation TEXT NOT NULL,
			idempotency_key TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			response_json TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (run_id, operation, idempotency_key)
		)`,
		`
		CREATE TABLE IF NOT EXISTS ai_run_tool_results (
			run_id TEXT NOT NULL REFERENCES ai_runs(run_id) ON DELETE CASCADE,
			tool_call_id TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			response_json TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (run_id, tool_call_id)
		)`,
		"CREATE INDEX IF NOT EXISTS ai_runs_active_idx ON ai_runs(status, owner_scope_hash)",
		"CREATE INDEX IF NOT EXISTS ai_runs_expiry_idx ON ai_runs(expires_at)",
		"CREATE INDEX IF NOT EXISTS ai_run_events_created_idx ON ai_run_events(created_at)",
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}

	var existing sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT MAX(version) AS version FROM ai_run_schema").Scan(&existing); err != nil {
		return err
	}
	if existing.Valid && existing.Int64 > RunStoreSchemaVersion {
		return ErrSchemaVersionTooNew
	}
	existingVersion := existing

	if !existingVersion.Valid || existingVersion.Int64 < 3 {
		if _, err := db.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS ai_run_commit_metric_receipts (
				run_id TEXT NOT NULL REFERENCES ai_runs(run_id) ON DELETE CASCADE,
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
			)`); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS ai_run_commit_metric_receipts_created_idx ON ai_run_commit_metric_receipts(created_at)"); err != nil {
			return err
		}
		if existingVersion.Valid && existingVersion.Int64 >= 2 {
			if _, err := db.ExecContext(ctx, `
				INSERT OR IGNORE INTO ai_run_commit_metric_receipts(
					run_id, attempt, idempotency_key, request_hash, commit_duration_ms,
					verify_duration_ms, outcome, error_code, created_at
				)
				SELECT run_id, 1, idempotency_key, request_hash, commit_duration_ms,
					verify_duration_ms, outcome, error_code, created_at
				FROM ai_run_commit_metrics`); err != nil {
				return err
			}
		}
		if _, err := db.ExecContext(ctx, "INSERT OR IGNORE INTO ai_run_schema(version) VALUES (3)"); err != nil {
			return err
		}
	}
	if !existingVersion.Valid || existingVersion.Int64 < 4 {
		if _, err := db.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS ai_run_resource_review_metric_receipts (
				run_id TEXT NOT NULL REFERENCES ai_runs(run_id) ON DELETE CASCADE,
				attempt INTEGER NOT NULL,
				idempotency_key TEXT NOT NULL,
				request_hash TEXT NOT NULL,
				buckets_json TEXT NOT NULL,
				created_at INTEGER NOT NULL,
				PRIMARY KEY (run_id, attempt),
				UNIQUE (run_id, idempotency_key)
			)`); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS ai_run_resource_review_metric_receipts_created_idx ON ai_run_resource_review_metric_receipts(created_at)"); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, "INSERT OR IGNORE INTO ai_run_schema(version) VALUES (4)"); err != nil {
			return err
		}
	}
	if !existingVersion.Valid || existingVersion.Int64 < 5 {
		for _, column := range []string{
			"behavior_test_count INTEGER NOT NULL DEFAULT 0",
			"behavior_passed_count INTEGER NOT NULL DEFAULT 0",
			"behavior_must_count INTEGER NOT NULL DEFAULT 0",
			"behavior_must_passed_count INTEGER NOT NULL DEFAULT 0",
		} {
			if _, err := db.ExecContext(ctx, "ALTER TABLE ai_run_commit_metric_receipts ADD COLUMN "+column); err != nil {
				return err
			}
		}
		if _, err := db.ExecContext(ctx, "INSERT OR IGNORE INTO ai_run_schema(version) VALUES (5)"); err != nil {
			return err
		}
	}
	if _, err := db.ExecContext(ctx, "INSERT OR IGNORE INTO ai_run_schema(version) VALUES (?)", RunStoreSchemaVersion); err != nil {
		return err
	}
	return nil
}

// decodeJSONObject decodes a stored JSON object column.
func decodeJSONObject(serialized string) (map[string]any, error) {
	decoded := map[string]any{}
	if serialized == "" {
		return decoded, nil
	}
	if err := json.Unmarshal([]byte(serialized), &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

// jsonObject reports whether value is a JSON object, accepting any Go map with
// string keys so hand-built artifact summaries behave like Python dicts.
func jsonObject(value any) (map[string]any, bool) {
	if object, ok := value.(map[string]any); ok {
		return object, true
	}
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() || reflected.Kind() != reflect.Map || reflected.Type().Key().Kind() != reflect.String {
		return nil, false
	}
	object := make(map[string]any, reflected.Len())
	iterator := reflected.MapRange()
	for iterator.Next() {
		object[iterator.Key().String()] = iterator.Value().Interface()
	}
	return object, true
}

// jsonInt64 reports whether value is an integer, mirroring Python's
// isinstance(value, int) for values that round-trip through JSON.
func jsonInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int8:
		return int64(typed), true
	case int16:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case uint:
		return int64(typed), true
	case uint8:
		return int64(typed), true
	case uint16:
		return int64(typed), true
	case uint32:
		return int64(typed), true
	case uint64:
		if typed > math.MaxInt64 {
			return 0, false
		}
		return int64(typed), true
	case float32:
		return floatToInt64(float64(typed))
	case float64:
		return floatToInt64(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	}
	return 0, false
}

func floatToInt64(value float64) (int64, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value != math.Trunc(value) {
		return 0, false
	}
	if value > math.MaxInt64 || value < math.MinInt64 {
		return 0, false
	}
	return int64(value), true
}

// jsonListLength reports whether value is a JSON array, accepting any Go slice
// or array so hand-built artifact summaries behave like Python lists.
func jsonListLength(value any) (int, bool) {
	if value == nil {
		return 0, false
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Slice, reflect.Array:
		return reflected.Len(), true
	}
	return 0, false
}

// newUUIDHex returns the 32 lowercase hex characters of a random UUID v4,
// matching uuid4().hex.
func newUUIDHex() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return hex.EncodeToString(raw[:]), nil
}

// nowMillis mirrors RunStore._now(): int(time.time() * 1000).
func nowMillis() int64 { return time.Now().UnixMilli() }

// sha256Hex returns the lowercase hex SHA-256 digest of value.
func sha256Hex(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
