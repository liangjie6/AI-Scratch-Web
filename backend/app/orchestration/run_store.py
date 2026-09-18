from __future__ import annotations

from dataclasses import dataclass
from hashlib import sha256
import json
from pathlib import Path
import re
import sqlite3
import threading
import time
from typing import Any
from uuid import uuid4

from .run_state_machine import RUN_PROTOCOL_VERSION, RunStatus, RunTransitionError, assert_transition, is_terminal


RUN_STORE_SCHEMA_VERSION = 5


class RunNotFoundError(LookupError):
    pass


class IdempotencyConflictError(ValueError):
    pass


class ToolResultConflictError(ValueError):
    pass


@dataclass(frozen=True)
class RunRecord:
    run_id: str
    owner_scope_hash: str
    status: RunStatus
    state_version: int
    protocol_version: str
    project_epoch: int
    artifact_summary: dict[str, Any]
    created_at: int
    updated_at: int
    expires_at: int
    terminal_at: int | None

    def public_payload(self) -> dict[str, Any]:
        # Contracts are retained privately for the live run manager, but must
        # never be returned by status/SSE-adjacent APIs. They can contain the
        # student's task wording, resource names, and project semantics.
        summary: dict[str, Any] = {}
        for key in ('scope', 'output_contract'):
            value = self.artifact_summary.get(key)
            if isinstance(value, str):
                summary[key] = value
        manifest_summary = self.artifact_summary.get('manifest_summary')
        if isinstance(manifest_summary, dict):
            counts = {
                key: manifest_summary.get(key)
                for key in ('program_target_count', 'resource_op_count')
                if isinstance(manifest_summary.get(key), int)
            }
            if counts:
                summary['manifest_summary'] = counts
        asset_need_ids = self.artifact_summary.get('asset_need_ids')
        if isinstance(asset_need_ids, list):
            summary['asset_need_count'] = len(asset_need_ids)
        return {
            'protocol_version': self.protocol_version,
            'run_id': self.run_id,
            'status': self.status.value,
            'state_version': self.state_version,
            'project_epoch': self.project_epoch,
            'artifact_summary': summary,
            'created_at': self.created_at,
            'updated_at': self.updated_at,
            'expires_at': self.expires_at,
            'terminal': is_terminal(self.status),
        }


@dataclass(frozen=True)
class RunEvent:
    run_id: str
    event_id: str
    sequence: int
    phase: str
    type: str
    attempt: int
    payload: dict[str, Any]
    created_at: int

    def envelope(self) -> dict[str, Any]:
        return {
            'protocol_version': RUN_PROTOCOL_VERSION,
            'run_id': self.run_id,
            'event_id': self.event_id,
            'sequence': self.sequence,
            'phase': self.phase,
            'type': self.type,
            'attempt': self.attempt,
            'payload': self.payload,
        }


def canonical_json(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':'))


def request_hash(value: Any) -> str:
    return sha256(canonical_json(value).encode('utf-8')).hexdigest()


class RunStore:
    """SQLite persistence for sanitized run state and replayable protocol events."""

    def __init__(self, database_path: Path, *, event_retention_hours: int, run_retention_days: int) -> None:
        self.database_path = database_path
        self.event_retention_ms = event_retention_hours * 60 * 60 * 1000
        self.run_retention_ms = run_retention_days * 24 * 60 * 60 * 1000
        self._connection: sqlite3.Connection | None = None
        self._lock = threading.RLock()

    def initialize(self) -> None:
        with self._lock:
            if self._connection is not None:
                return
            self.database_path.parent.mkdir(parents=True, exist_ok=True)
            connection = sqlite3.connect(self.database_path, check_same_thread=False, isolation_level=None)
            connection.row_factory = sqlite3.Row
            connection.execute('PRAGMA journal_mode=WAL')
            connection.execute('PRAGMA busy_timeout=5000')
            connection.execute('PRAGMA foreign_keys=ON')
            connection.execute('''
                CREATE TABLE IF NOT EXISTS ai_run_schema (
                    version INTEGER PRIMARY KEY
                )
            ''')
            connection.execute('''
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
                )
            ''')
            connection.execute('''
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
                )
            ''')
            connection.execute('''
                CREATE TABLE IF NOT EXISTS ai_run_idempotency (
                    run_id TEXT NOT NULL REFERENCES ai_runs(run_id) ON DELETE CASCADE,
                    operation TEXT NOT NULL,
                    idempotency_key TEXT NOT NULL,
                    request_hash TEXT NOT NULL,
                    response_json TEXT NOT NULL,
                    created_at INTEGER NOT NULL,
                    PRIMARY KEY (run_id, operation, idempotency_key)
                )
            ''')
            connection.execute('''
                CREATE TABLE IF NOT EXISTS ai_run_tool_results (
                    run_id TEXT NOT NULL REFERENCES ai_runs(run_id) ON DELETE CASCADE,
                    tool_call_id TEXT NOT NULL,
                    request_hash TEXT NOT NULL,
                    response_json TEXT NOT NULL,
                    created_at INTEGER NOT NULL,
                    PRIMARY KEY (run_id, tool_call_id)
                )
            ''')
            connection.execute('CREATE INDEX IF NOT EXISTS ai_runs_expiry_idx ON ai_runs(expires_at)')
            connection.execute('CREATE INDEX IF NOT EXISTS ai_run_events_created_idx ON ai_run_events(created_at)')
            existing_version = connection.execute('SELECT MAX(version) AS version FROM ai_run_schema').fetchone()['version']
            if existing_version is not None and int(existing_version) > RUN_STORE_SCHEMA_VERSION:
                connection.close()
                raise RuntimeError('ai run store schema is newer than this application')
            if existing_version is None or int(existing_version) < 3:
                connection.execute('''
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
                    )
                ''')
                connection.execute('CREATE INDEX IF NOT EXISTS ai_run_commit_metric_receipts_created_idx ON ai_run_commit_metric_receipts(created_at)')
                if existing_version is not None and int(existing_version) >= 2:
                    connection.execute('''
                        INSERT OR IGNORE INTO ai_run_commit_metric_receipts(
                            run_id, attempt, idempotency_key, request_hash, commit_duration_ms,
                            verify_duration_ms, outcome, error_code, created_at
                        )
                        SELECT run_id, 1, idempotency_key, request_hash, commit_duration_ms,
                            verify_duration_ms, outcome, error_code, created_at
                        FROM ai_run_commit_metrics
                    ''')
                connection.execute('INSERT OR IGNORE INTO ai_run_schema(version) VALUES (3)')
            if existing_version is None or int(existing_version) < 4:
                connection.execute('''
                    CREATE TABLE IF NOT EXISTS ai_run_resource_review_metric_receipts (
                        run_id TEXT NOT NULL REFERENCES ai_runs(run_id) ON DELETE CASCADE,
                        attempt INTEGER NOT NULL,
                        idempotency_key TEXT NOT NULL,
                        request_hash TEXT NOT NULL,
                        buckets_json TEXT NOT NULL,
                        created_at INTEGER NOT NULL,
                        PRIMARY KEY (run_id, attempt),
                        UNIQUE (run_id, idempotency_key)
                    )
                ''')
                connection.execute('CREATE INDEX IF NOT EXISTS ai_run_resource_review_metric_receipts_created_idx ON ai_run_resource_review_metric_receipts(created_at)')
                connection.execute('INSERT OR IGNORE INTO ai_run_schema(version) VALUES (4)')
            if existing_version is None or int(existing_version) < 5:
                for column in (
                    'behavior_test_count INTEGER NOT NULL DEFAULT 0',
                    'behavior_passed_count INTEGER NOT NULL DEFAULT 0',
                    'behavior_must_count INTEGER NOT NULL DEFAULT 0',
                    'behavior_must_passed_count INTEGER NOT NULL DEFAULT 0',
                ):
                    connection.execute(f'ALTER TABLE ai_run_commit_metric_receipts ADD COLUMN {column}')
                connection.execute('INSERT OR IGNORE INTO ai_run_schema(version) VALUES (5)')
            connection.execute('INSERT OR IGNORE INTO ai_run_schema(version) VALUES (?)', (RUN_STORE_SCHEMA_VERSION,))
            self._connection = connection

    def close(self) -> None:
        with self._lock:
            if self._connection is not None:
                self._connection.close()
                self._connection = None

    def create_run(self, *, owner_scope_hash: str, project_epoch: int, artifact_summary: dict[str, Any]) -> RunRecord:
        now = self._now()
        run_id = f'run_{uuid4().hex}'
        with self._transaction() as connection:
            connection.execute(
                '''INSERT INTO ai_runs(
                    run_id, owner_scope_hash, status, state_version, protocol_version, project_epoch,
                    artifact_summary_json, created_at, updated_at, expires_at, terminal_at
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)''',
                (
                    run_id,
                    owner_scope_hash,
                    RunStatus.SNAPSHOTTING.value,
                    1,
                    RUN_PROTOCOL_VERSION,
                    project_epoch,
                    canonical_json(artifact_summary),
                    now,
                    now,
                    now + self.run_retention_ms,
                ),
            )
            self._insert_event(
                connection,
                run_id=run_id,
                phase='snapshotting',
                event_type='phase_started',
                payload={'status': RunStatus.SNAPSHOTTING.value},
            )
        return self.get_run(run_id, owner_scope_hash)

    def get_run(self, run_id: str, owner_scope_hash: str) -> RunRecord:
        with self._lock:
            row = self._conn().execute(
                'SELECT * FROM ai_runs WHERE run_id = ? AND owner_scope_hash = ?',
                (run_id, owner_scope_hash),
            ).fetchone()
        if row is None:
            raise RunNotFoundError(run_id)
        return self._record_from_row(row)

    def transition(
        self,
        *,
        run_id: str,
        owner_scope_hash: str,
        target: RunStatus,
        phase: str,
        event_type: str,
        payload: dict[str, Any],
        attempt: int = 1,
    ) -> RunRecord:
        now = self._now()
        with self._transaction() as connection:
            row = self._get_row(connection, run_id, owner_scope_hash)
            current = RunStatus(row['status'])
            assert_transition(current, target)
            if current != target:
                terminal_at = now if is_terminal(target) else None
                updated = connection.execute(
                    '''UPDATE ai_runs
                       SET status = ?, state_version = state_version + 1, updated_at = ?, terminal_at = ?
                       WHERE run_id = ? AND owner_scope_hash = ? AND state_version = ?''',
                    (target.value, now, terminal_at, run_id, owner_scope_hash, row['state_version']),
                )
                if updated.rowcount != 1:
                    raise RunTransitionError('concurrent run state update')
            self._insert_event(connection, run_id=run_id, phase=phase, event_type=event_type, payload=payload, attempt=attempt)
        return self.get_run(run_id, owner_scope_hash)

    def append_event(
        self,
        *,
        run_id: str,
        owner_scope_hash: str,
        phase: str,
        event_type: str,
        payload: dict[str, Any],
        attempt: int = 1,
    ) -> RunEvent:
        with self._transaction() as connection:
            row = self._get_row(connection, run_id, owner_scope_hash)
            if is_terminal(RunStatus(row['status'])):
                raise RunTransitionError('cannot append an event after a terminal run status')
            return self._insert_event(connection, run_id=run_id, phase=phase, event_type=event_type, payload=payload, attempt=attempt)

    def merge_artifact_summary(self, *, run_id: str, owner_scope_hash: str, values: dict[str, Any]) -> RunRecord:
        now = self._now()
        with self._transaction() as connection:
            row = self._get_row(connection, run_id, owner_scope_hash)
            if is_terminal(RunStatus(row['status'])):
                raise RunTransitionError('cannot modify artifacts after a terminal run status')
            summary = json.loads(row['artifact_summary_json'])
            summary.update(values)
            connection.execute(
                '''UPDATE ai_runs SET artifact_summary_json = ?, updated_at = ?
                   WHERE run_id = ? AND owner_scope_hash = ?''',
                (canonical_json(summary), now, run_id, owner_scope_hash),
            )
        return self.get_run(run_id, owner_scope_hash)

    def accepted_tool_call_ids(self, *, run_id: str, owner_scope_hash: str) -> set[str]:
        with self._lock:
            self._get_row(self._conn(), run_id, owner_scope_hash)
            rows = self._conn().execute(
                'SELECT tool_call_id FROM ai_run_tool_results WHERE run_id = ?',
                (run_id,),
            ).fetchall()
        return {str(row['tool_call_id']) for row in rows}

    def events_after(self, *, run_id: str, owner_scope_hash: str, sequence: int = 0) -> list[RunEvent]:
        with self._lock:
            self._get_row(self._conn(), run_id, owner_scope_hash)
            rows = self._conn().execute(
                '''SELECT event_id, sequence, phase, type, attempt, payload_json, created_at
                   FROM ai_run_events WHERE run_id = ? AND sequence > ? ORDER BY sequence ASC''',
                (run_id, sequence),
            ).fetchall()
        return [self._event_from_row(run_id, row) for row in rows]

    def sequence_for_event(self, *, run_id: str, owner_scope_hash: str, event_id: str) -> int | None:
        with self._lock:
            self._get_row(self._conn(), run_id, owner_scope_hash)
            row = self._conn().execute(
                'SELECT sequence FROM ai_run_events WHERE run_id = ? AND event_id = ?',
                (run_id, event_id),
            ).fetchone()
        return int(row['sequence']) if row is not None else None

    def accept_interaction(
        self,
        *,
        run_id: str,
        owner_scope_hash: str,
        operation: str,
        idempotency_key: str,
        expected_project_epoch: int,
        required_status: RunStatus,
        phase: str,
        event_type: str,
        payload: dict[str, Any],
        tool_call_id: str | None = None,
    ) -> tuple[dict[str, Any], bool]:
        """Persist an input receipt and protocol event in the same transaction."""
        payload_hash = request_hash(payload)
        with self._transaction() as connection:
            row = self._get_row(connection, run_id, owner_scope_hash)
            if int(row['project_epoch']) != expected_project_epoch:
                raise RunTransitionError('expected_project_epoch does not match the run')
            existing_key = connection.execute(
                '''SELECT request_hash, response_json FROM ai_run_idempotency
                   WHERE run_id = ? AND operation = ? AND idempotency_key = ?''',
                (run_id, operation, idempotency_key),
            ).fetchone()
            if existing_key is not None:
                if existing_key['request_hash'] != payload_hash:
                    raise IdempotencyConflictError('the idempotency key was already used with a different payload')
                return json.loads(existing_key['response_json']), True
            if RunStatus(row['status']) != required_status:
                raise RunTransitionError(f'{operation} requires run status {required_status.value}')
            if tool_call_id is not None:
                existing_tool = connection.execute(
                    '''SELECT request_hash, response_json FROM ai_run_tool_results
                       WHERE run_id = ? AND tool_call_id = ?''',
                    (run_id, tool_call_id),
                ).fetchone()
                if existing_tool is not None:
                    if existing_tool['request_hash'] != payload_hash:
                        raise ToolResultConflictError('the tool call already has a different final result')
                    return json.loads(existing_tool['response_json']), True
            event = self._insert_event(
                connection,
                run_id=run_id,
                phase=phase,
                event_type=event_type,
                payload=payload,
            )
            response = {'accepted': True, 'event_id': event.event_id, 'sequence': event.sequence}
            now = self._now()
            serialized = canonical_json(response)
            connection.execute(
                '''INSERT INTO ai_run_idempotency(run_id, operation, idempotency_key, request_hash, response_json, created_at)
                   VALUES (?, ?, ?, ?, ?, ?)''',
                (run_id, operation, idempotency_key, payload_hash, serialized, now),
            )
            if tool_call_id is not None:
                connection.execute(
                    '''INSERT INTO ai_run_tool_results(run_id, tool_call_id, request_hash, response_json, created_at)
                       VALUES (?, ?, ?, ?, ?)''',
                    (run_id, tool_call_id, payload_hash, serialized, now),
                )
        return response, False

    def cancel_idempotent(
        self,
        *,
        run_id: str,
        owner_scope_hash: str,
        expected_project_epoch: int,
        idempotency_key: str,
    ) -> tuple[RunRecord, bool]:
        payload = {'expected_project_epoch': expected_project_epoch}
        payload_hash = request_hash(payload)
        with self._transaction() as connection:
            row = self._get_row(connection, run_id, owner_scope_hash)
            if int(row['project_epoch']) != expected_project_epoch:
                raise RunTransitionError('expected_project_epoch does not match the run')
            existing = connection.execute(
                '''SELECT request_hash, response_json FROM ai_run_idempotency
                   WHERE run_id = ? AND operation = 'cancel' AND idempotency_key = ?''',
                (run_id, idempotency_key),
            ).fetchone()
            if existing is not None:
                if existing['request_hash'] != payload_hash:
                    raise IdempotencyConflictError('the idempotency key was already used with a different payload')
                return self._record_from_row(row), True
            current = RunStatus(row['status'])
            now = self._now()
            if not is_terminal(current):
                updated = connection.execute(
                    '''UPDATE ai_runs SET status = ?, state_version = state_version + 1, updated_at = ?, terminal_at = ?
                       WHERE run_id = ? AND owner_scope_hash = ? AND state_version = ?''',
                    (RunStatus.CANCELLED.value, now, now, run_id, owner_scope_hash, row['state_version']),
                )
                if updated.rowcount != 1:
                    raise RunTransitionError('concurrent run state update')
                row = self._get_row(connection, run_id, owner_scope_hash)
                self._insert_event(
                    connection,
                    run_id=run_id,
                    phase='cancelled',
                    event_type='generation_error',
                    payload={'code': 'RUN_CANCELLED', 'message': '用户已取消本次生成'},
                )
            response = self._record_from_row(row).public_payload()
            connection.execute(
                '''INSERT INTO ai_run_idempotency(run_id, operation, idempotency_key, request_hash, response_json, created_at)
                   VALUES (?, 'cancel', ?, ?, ?, ?)''',
                (run_id, idempotency_key, payload_hash, canonical_json(response), now),
            )
        return self.get_run(run_id, owner_scope_hash), False

    def purge_expired(self) -> int:
        now = self._now()
        terminal_values = tuple(status.value for status in (RunStatus.READY_TO_COMMIT, RunStatus.CANCELLED, RunStatus.FAILED))
        with self._transaction() as connection:
            connection.execute(
                f'''DELETE FROM ai_run_events
                    WHERE created_at < ? AND run_id IN (
                        SELECT run_id FROM ai_runs WHERE status IN ({','.join('?' for _ in terminal_values)})
                    )''',
                (now - self.event_retention_ms, *terminal_values),
            )
            result = connection.execute(
                f'''DELETE FROM ai_runs WHERE expires_at < ? AND status IN ({','.join('?' for _ in terminal_values)})''',
                (now, *terminal_values),
            )
        return result.rowcount

    def fail_incomplete_runs(self) -> int:
        """A provider request is intentionally not resumed after a process restart."""
        now = self._now()
        with self._transaction() as connection:
            rows = connection.execute(
                'SELECT run_id, owner_scope_hash, state_version FROM ai_runs WHERE status NOT IN (?, ?, ?)',
                tuple(status.value for status in (RunStatus.READY_TO_COMMIT, RunStatus.CANCELLED, RunStatus.FAILED)),
            ).fetchall()
            for row in rows:
                connection.execute(
                    '''UPDATE ai_runs SET status = ?, state_version = state_version + 1, updated_at = ?, terminal_at = ?
                       WHERE run_id = ? AND owner_scope_hash = ? AND state_version = ?''',
                    (RunStatus.FAILED.value, now, now, row['run_id'], row['owner_scope_hash'], row['state_version']),
                )
                self._insert_event(
                    connection,
                    run_id=row['run_id'],
                    phase='failed',
                    event_type='generation_error',
                    payload={'code': 'RUN_INTERRUPTED', 'message': '服务重启后不能恢复进行中的模型请求'},
                )
        return len(rows)

    def record_client_commit_metric(
        self,
        *,
        run_id: str,
        owner_scope_hash: str,
        expected_project_epoch: int,
        idempotency_key: str,
        commit_duration_ms: int,
        verify_duration_ms: int,
        behavior_test_count: int,
        behavior_passed_count: int,
        behavior_must_count: int,
        behavior_must_passed_count: int,
        outcome: str,
        error_code: str | None,
    ) -> tuple[dict[str, Any], bool]:
        """Persist one payload-free browser Commit/Verify outcome for a durable run."""
        payload = {
            'expected_project_epoch': expected_project_epoch,
            'commit_duration_ms': commit_duration_ms,
            'verify_duration_ms': verify_duration_ms,
            'behavior_test_count': behavior_test_count,
            'behavior_passed_count': behavior_passed_count,
            'behavior_must_count': behavior_must_count,
            'behavior_must_passed_count': behavior_must_passed_count,
            'outcome': outcome,
            'error_code': error_code,
        }
        payload_hash = request_hash(payload)
        with self._transaction() as connection:
            row = self._get_row(connection, run_id, owner_scope_hash)
            if int(row['project_epoch']) != expected_project_epoch:
                raise RunTransitionError('expected_project_epoch does not match the run')
            existing = connection.execute(
                '''SELECT request_hash FROM ai_run_commit_metric_receipts
                   WHERE run_id = ? AND idempotency_key = ?''',
                (run_id, idempotency_key),
            ).fetchone()
            if existing is not None:
                if existing['request_hash'] != payload_hash:
                    raise IdempotencyConflictError('the idempotency key was already used with a different client commit metric')
                return {'accepted': True, 'duplicate': True}, True
            attempt = int(connection.execute(
                'SELECT COALESCE(MAX(attempt), 0) + 1 AS attempt FROM ai_run_commit_metric_receipts WHERE run_id = ?',
                (run_id,),
            ).fetchone()['attempt'])
            connection.execute(
                '''INSERT INTO ai_run_commit_metric_receipts(
                    run_id, attempt, idempotency_key, request_hash, commit_duration_ms,
                    verify_duration_ms, behavior_test_count, behavior_passed_count,
                    behavior_must_count, behavior_must_passed_count, outcome, error_code, created_at
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)''',
                (run_id, attempt, idempotency_key, payload_hash, commit_duration_ms, verify_duration_ms, behavior_test_count, behavior_passed_count, behavior_must_count, behavior_must_passed_count, outcome, error_code, self._now()),
            )
        return {'accepted': True, 'duplicate': False}, False

    def record_resource_review_metric(
        self,
        *,
        run_id: str,
        owner_scope_hash: str,
        expected_project_epoch: int,
        idempotency_key: str,
        buckets: list[dict[str, Any]],
    ) -> tuple[dict[str, Any], bool]:
        """Persist an aggregate-only resource review outcome for a durable run."""
        payload = {'expected_project_epoch': expected_project_epoch, 'buckets': buckets}
        payload_hash = request_hash(payload)
        with self._transaction() as connection:
            row = self._get_row(connection, run_id, owner_scope_hash)
            if int(row['project_epoch']) != expected_project_epoch:
                raise RunTransitionError('expected_project_epoch does not match the run')
            existing = connection.execute(
                '''SELECT request_hash FROM ai_run_resource_review_metric_receipts
                   WHERE run_id = ? AND idempotency_key = ?''',
                (run_id, idempotency_key),
            ).fetchone()
            if existing is not None:
                if existing['request_hash'] != payload_hash:
                    raise IdempotencyConflictError('the idempotency key was already used with a different resource review metric')
                return {'accepted': True, 'duplicate': True}, True
            attempt = int(connection.execute(
                'SELECT COALESCE(MAX(attempt), 0) + 1 AS attempt FROM ai_run_resource_review_metric_receipts WHERE run_id = ?',
                (run_id,),
            ).fetchone()['attempt'])
            connection.execute(
                '''INSERT INTO ai_run_resource_review_metric_receipts(
                    run_id, attempt, idempotency_key, request_hash, buckets_json, created_at
                ) VALUES (?, ?, ?, ?, ?, ?)''',
                (run_id, attempt, idempotency_key, payload_hash, canonical_json(buckets), self._now()),
            )
        return {'accepted': True, 'duplicate': False}, False

    def metrics_for_owner(
        self,
        *,
        owner_scope_hash: str,
        window_hours: int,
        stuck_after_ms: int | None = None,
    ) -> dict[str, Any]:
        """Return bounded, payload-free telemetry for one opaque owner scope."""
        now = self._now()
        window_start = now - window_hours * 60 * 60 * 1000
        with self._lock:
            connection = self._conn()
            runs = connection.execute(
                '''SELECT status, created_at, updated_at, terminal_at
                   FROM ai_runs
                   WHERE owner_scope_hash = ? AND created_at >= ?''',
                (owner_scope_hash, window_start),
            ).fetchall()
            event_rows = connection.execute(
                '''SELECT event.run_id, event.phase, event.type, event.attempt, event.payload_json, event.created_at
                   FROM ai_run_events AS event
                   INNER JOIN ai_runs AS run ON run.run_id = event.run_id
                   WHERE run.owner_scope_hash = ? AND run.created_at >= ? AND event.created_at >= ?
                   ORDER BY event.run_id ASC, event.sequence ASC''',
                (owner_scope_hash, window_start, window_start),
            ).fetchall()
            client_metric_rows = connection.execute(
                '''SELECT metric.run_id, metric.commit_duration_ms, metric.verify_duration_ms,
                          metric.behavior_test_count, metric.behavior_passed_count,
                          metric.behavior_must_count, metric.behavior_must_passed_count,
                          metric.outcome, metric.error_code
                   FROM ai_run_commit_metric_receipts AS metric
                   INNER JOIN ai_runs AS run ON run.run_id = metric.run_id
                   WHERE run.owner_scope_hash = ? AND metric.created_at >= ?''',
                (owner_scope_hash, window_start),
            ).fetchall()
            resource_review_rows = connection.execute(
                '''SELECT metric.run_id, metric.buckets_json
                   FROM ai_run_resource_review_metric_receipts AS metric
                   INNER JOIN ai_runs AS run ON run.run_id = metric.run_id
                   WHERE run.owner_scope_hash = ? AND metric.created_at >= ?''',
                (owner_scope_hash, window_start),
            ).fetchall()
        statuses: dict[str, int] = {}
        durations: list[int] = []
        stuck_run_count = 0
        for row in runs:
            status = str(row['status'])
            statuses[status] = statuses.get(status, 0) + 1
            if row['terminal_at'] is not None:
                durations.append(max(0, int(row['terminal_at']) - int(row['created_at'])))
            elif stuck_after_ms is not None and status != RunStatus.AWAITING_TOOL_RESULT.value and now - int(row['updated_at']) > stuck_after_ms:
                stuck_run_count += 1
        durations.sort()

        def percentile(values: list[int], fraction: float) -> int | None:
            if not values:
                return None
            return values[min(len(values) - 1, int((len(values) - 1) * fraction))]

        event_type_counts: dict[str, int] = {}
        error_code_counts: dict[str, int] = {}
        validation_failed_runs: set[str] = set()
        validation_completed_runs: set[str] = set()
        repair_attempts: dict[str, int] = {}
        active_phases: dict[str, tuple[str, int]] = {}
        phase_durations: dict[str, list[int]] = {}

        def close_active_phase(run_id: str, ended_at: int) -> None:
            active = active_phases.pop(run_id, None)
            if active is None:
                return
            phase, started_at = active
            phase_durations.setdefault(phase, []).append(max(0, ended_at - started_at))

        for row in event_rows:
            run_id = str(row['run_id'])
            phase = str(row['phase'])
            event_type = str(row['type'])
            created_at = int(row['created_at'])
            event_type_counts[event_type] = event_type_counts.get(event_type, 0) + 1
            if event_type == 'phase_started':
                active = active_phases.get(run_id)
                if active is None:
                    active_phases[run_id] = (phase, created_at)
                elif active[0] != phase:
                    close_active_phase(run_id, created_at)
                    active_phases[run_id] = (phase, created_at)
            elif event_type == 'asset_resolution_required':
                close_active_phase(run_id, created_at)
                active_phases[run_id] = (phase, created_at)
            elif event_type == 'phase_completed':
                active = active_phases.get(run_id)
                if active is not None and active[0] == phase:
                    close_active_phase(run_id, created_at)
            elif event_type == 'repair_started':
                repair_attempts[run_id] = max(repair_attempts.get(run_id, 0), int(row['attempt']))
            elif event_type == 'validation_failed':
                validation_failed_runs.add(run_id)
            elif event_type in {'generation_complete', 'generation_error'}:
                close_active_phase(run_id, created_at)
                if event_type == 'generation_complete':
                    validation_completed_runs.add(run_id)
            if event_type == 'generation_error':
                try:
                    code = json.loads(row['payload_json']).get('code')
                except (TypeError, ValueError, json.JSONDecodeError):
                    code = None
                if isinstance(code, str) and re.fullmatch(r'[A-Z0-9_]{1,80}', code):
                    error_code_counts[code] = error_code_counts.get(code, 0) + 1

        phase_duration_ms = {}
        for phase, values in phase_durations.items():
            values.sort()
            phase_duration_ms[phase] = {
                'count': len(values),
                'p50': percentile(values, 0.5),
                'p95': percentile(values, 0.95),
            }
        repair_counts = sorted(repair_attempts.values())
        # A cancelled or provider-failed run which never reached ProgramIR
        # validation is not evidence of a first-pass schema result.
        validation_eligible_runs = validation_completed_runs | validation_failed_runs
        first_pass_runs = validation_completed_runs - validation_failed_runs
        commit_durations = sorted(max(0, int(row['commit_duration_ms'])) for row in client_metric_rows)
        verify_durations = sorted(max(0, int(row['verify_duration_ms'])) for row in client_metric_rows)
        commit_verify_durations = sorted(
            max(0, int(row['commit_duration_ms'])) + max(0, int(row['verify_duration_ms']))
            for row in client_metric_rows
        )
        behavior_test_count = sum(max(0, int(row['behavior_test_count'])) for row in client_metric_rows)
        behavior_passed_count = sum(max(0, int(row['behavior_passed_count'])) for row in client_metric_rows)
        behavior_must_count = sum(max(0, int(row['behavior_must_count'])) for row in client_metric_rows)
        behavior_must_passed_count = sum(max(0, int(row['behavior_must_passed_count'])) for row in client_metric_rows)
        commit_outcome_counts: dict[str, int] = {}
        commit_error_code_counts: dict[str, int] = {}
        for row in client_metric_rows:
            outcome = str(row['outcome'])
            commit_outcome_counts[outcome] = commit_outcome_counts.get(outcome, 0) + 1
            error_code = row['error_code']
            if isinstance(error_code, str) and re.fullmatch(r'[A-Z0-9_]{1,80}', error_code):
                commit_error_code_counts[error_code] = commit_error_code_counts.get(error_code, 0) + 1
        revision_conflict_count = commit_error_code_counts.get('COMMIT_CONFLICT', 0)
        revision_conflict_rate = revision_conflict_count / len(client_metric_rows) if client_metric_rows else None
        resource_review_by_type: dict[str, dict[str, int]] = {}
        for row in resource_review_rows:
            try:
                buckets = json.loads(row['buckets_json'])
            except (TypeError, ValueError, json.JSONDecodeError):
                continue
            if not isinstance(buckets, list):
                continue
            for bucket in buckets:
                if not isinstance(bucket, dict):
                    continue
                resource_type = bucket.get('resource_type')
                if resource_type not in {'sprite', 'costume', 'backdrop', 'sound'}:
                    continue
                aggregate = resource_review_by_type.setdefault(resource_type, {
                    'auto_selected_count': 0,
                    'auto_reselected_count': 0,
                    'manual_selected_count': 0,
                    'review_required_count': 0,
                    'generation_fallback_count': 0,
                })
                for field in aggregate:
                    value = bucket.get(field)
                    if isinstance(value, int) and 0 <= value <= 100:
                        aggregate[field] += value
        alerts: list[dict[str, Any]] = []
        failed_recovery_count = commit_outcome_counts.get('FAILED_NEEDS_RECOVERY', 0)
        if failed_recovery_count:
            alerts.append({
                'code': 'ATOMIC_RECOVERY_FAILURE',
                'severity': 'critical',
                'action': 'DISABLE_AI_COMMIT',
                'count': failed_recovery_count,
            })
        total_p95 = percentile(commit_verify_durations, 0.95)
        if len(client_metric_rows) >= 50 and total_p95 is not None and total_p95 > 5_000:
            alerts.append({
                'code': 'COMMIT_VERIFY_P95_EXCEEDED',
                'severity': 'warning',
                'action': 'STOP_ROLLOUT',
                'attempt_count': len(client_metric_rows),
                'p95_duration_ms': total_p95,
            })
        sb3_resource_missing_count = commit_error_code_counts.get('SB3_RESOURCE_MISSING', 0)
        sb3_resource_missing_rate = sb3_resource_missing_count / len(client_metric_rows) if client_metric_rows else None
        if len(client_metric_rows) >= 1_000 and sb3_resource_missing_rate is not None and sb3_resource_missing_rate > 0.001:
            alerts.append({
                'code': 'SB3_RESOURCE_MISSING_RATE_EXCEEDED',
                'severity': 'warning',
                'action': 'STOP_ROLLOUT',
                'attempt_count': len(client_metric_rows),
                'missing_resource_count': sb3_resource_missing_count,
            })
        stuck_rate = stuck_run_count / len(runs) if runs else None
        if stuck_rate is not None and stuck_rate > 0.005:
            alerts.append({
                'code': 'STUCK_RUN_RATE_EXCEEDED',
                'severity': 'warning',
                'action': 'STOP_ROLLOUT',
                'run_count': len(runs),
                'stuck_run_count': stuck_run_count,
            })
        first_pass_rate = len(first_pass_runs) / len(validation_eligible_runs) if validation_eligible_runs else None
        if len(validation_eligible_runs) >= 100 and first_pass_rate is not None and first_pass_rate < 0.9:
            alerts.append({
                'code': 'VALIDATION_FIRST_PASS_BELOW_TARGET',
                'severity': 'warning',
                'action': 'STOP_ROLLOUT',
                'eligible_run_count': len(validation_eligible_runs),
                'first_pass_rate': first_pass_rate,
            })
        behavior_must_pass_rate = behavior_must_passed_count / behavior_must_count if behavior_must_count else None
        if behavior_must_count >= 100 and behavior_must_pass_rate is not None and behavior_must_pass_rate < 0.9:
            alerts.append({
                'code': 'BEHAVIOR_MUST_PASS_BELOW_TARGET',
                'severity': 'warning',
                'action': 'STOP_ROLLOUT',
                'must_count': behavior_must_count,
                'must_pass_rate': behavior_must_pass_rate,
            })

        return {
            'window_start': window_start,
            'window_end': now,
            'run_count': len(runs),
            'terminal_run_count': len(durations),
            'status_counts': statuses,
            'event_type_counts': event_type_counts,
            'error_code_counts': error_code_counts,
            'terminal_duration_ms': {'p50': percentile(durations, 0.5), 'p95': percentile(durations, 0.95)},
            'failed_run_count': statuses.get(RunStatus.FAILED.value, 0),
            'cancelled_run_count': statuses.get(RunStatus.CANCELLED.value, 0),
            'stuck_run_count': stuck_run_count,
            'stuck_after_ms': stuck_after_ms,
            'alerts': alerts,
            'phase_duration_ms': phase_duration_ms,
            'repair': {
                'run_count': len(repair_counts),
                'total_attempts': sum(repair_counts),
                'p95_attempts': percentile(repair_counts, 0.95),
            },
            'validation': {
                'eligible_run_count': len(validation_eligible_runs),
                'failed_run_count': len(validation_failed_runs),
                'first_pass_run_count': len(first_pass_runs),
                'first_pass_rate': first_pass_rate,
            },
            'commit_verify': {
                'attempt_count': len(client_metric_rows),
                'run_count': len({str(row['run_id']) for row in client_metric_rows}),
                'outcome_counts': commit_outcome_counts,
                'rollback_kind_counts': commit_outcome_counts,
                'error_code_counts': commit_error_code_counts,
                'revision_conflict': {'count': revision_conflict_count, 'rate': revision_conflict_rate},
                'commit_duration_ms': {'p50': percentile(commit_durations, 0.5), 'p95': percentile(commit_durations, 0.95)},
                'verify_duration_ms': {'p50': percentile(verify_durations, 0.5), 'p95': percentile(verify_durations, 0.95)},
                'total_duration_ms': {'p50': percentile(commit_verify_durations, 0.5), 'p95': percentile(commit_verify_durations, 0.95)},
            },
            'behavior': {
                'test_count': behavior_test_count,
                'passed_count': behavior_passed_count,
                'pass_rate': behavior_passed_count / behavior_test_count if behavior_test_count else None,
                'must_count': behavior_must_count,
                'must_passed_count': behavior_must_passed_count,
                'must_pass_rate': behavior_must_pass_rate,
            },
            'resource_review': {
                'attempt_count': len(resource_review_rows),
                'run_count': len({str(row['run_id']) for row in resource_review_rows}),
                'by_resource_type': resource_review_by_type,
            },
        }

    def _insert_event(
        self,
        connection: sqlite3.Connection,
        *,
        run_id: str,
        phase: str,
        event_type: str,
        payload: dict[str, Any],
        attempt: int = 1,
    ) -> RunEvent:
        sequence = int(connection.execute(
            'SELECT COALESCE(MAX(sequence), 0) + 1 AS next_sequence FROM ai_run_events WHERE run_id = ?',
            (run_id,),
        ).fetchone()['next_sequence'])
        event = RunEvent(
            run_id=run_id,
            event_id=f'evt_{uuid4().hex}',
            sequence=sequence,
            phase=phase,
            type=event_type,
            attempt=attempt,
            payload=payload,
            created_at=self._now(),
        )
        connection.execute(
            '''INSERT INTO ai_run_events(run_id, sequence, event_id, phase, type, attempt, payload_json, created_at)
               VALUES (?, ?, ?, ?, ?, ?, ?, ?)''',
            (event.run_id, event.sequence, event.event_id, event.phase, event.type, event.attempt, canonical_json(event.payload), event.created_at),
        )
        return event

    def _get_row(self, connection: sqlite3.Connection, run_id: str, owner_scope_hash: str) -> sqlite3.Row:
        row = connection.execute(
            'SELECT * FROM ai_runs WHERE run_id = ? AND owner_scope_hash = ?',
            (run_id, owner_scope_hash),
        ).fetchone()
        if row is None:
            raise RunNotFoundError(run_id)
        return row

    @staticmethod
    def _record_from_row(row: sqlite3.Row) -> RunRecord:
        return RunRecord(
            run_id=row['run_id'],
            owner_scope_hash=row['owner_scope_hash'],
            status=RunStatus(row['status']),
            state_version=int(row['state_version']),
            protocol_version=row['protocol_version'],
            project_epoch=int(row['project_epoch']),
            artifact_summary=json.loads(row['artifact_summary_json']),
            created_at=int(row['created_at']),
            updated_at=int(row['updated_at']),
            expires_at=int(row['expires_at']),
            terminal_at=int(row['terminal_at']) if row['terminal_at'] is not None else None,
        )

    @staticmethod
    def _event_from_row(run_id: str, row: sqlite3.Row) -> RunEvent:
        return RunEvent(
            run_id=run_id,
            event_id=row['event_id'],
            sequence=int(row['sequence']),
            phase=row['phase'],
            type=row['type'],
            attempt=int(row['attempt']),
            payload=json.loads(row['payload_json']),
            created_at=int(row['created_at']),
        )

    def _conn(self) -> sqlite3.Connection:
        if self._connection is None:
            raise RuntimeError('run store has not been initialized')
        return self._connection

    def _transaction(self):
        class Transaction:
            def __init__(self, store: RunStore) -> None:
                self.store = store

            def __enter__(self) -> sqlite3.Connection:
                self.store._lock.acquire()
                connection = self.store._conn()
                connection.execute('BEGIN IMMEDIATE')
                return connection

            def __exit__(self, exc_type: Any, exc: Any, traceback: Any) -> None:
                connection = self.store._conn()
                connection.execute('ROLLBACK' if exc_type else 'COMMIT')
                self.store._lock.release()

        return Transaction(self)

    @staticmethod
    def _now() -> int:
        return int(time.time() * 1000)
