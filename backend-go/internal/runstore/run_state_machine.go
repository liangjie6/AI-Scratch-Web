package runstore

import (
	"errors"
	"fmt"
)

// RunProtocolVersion is the wire protocol version stamped on every run row and
// on every event envelope.
//
// Python: RUN_PROTOCOL_VERSION.
const RunProtocolVersion = "ai-scratch-run/1"

// RunStatus is the durable lifecycle state of one generation run.
//
// Python: RunStatus, a StrEnum, so the constants compare equal to the TEXT
// values persisted in ai_runs.status and ai_run_events.phase.
type RunStatus string

// The RunStatus values, in the same order as the Python enum.
const (
	RunStatusSnapshotting       RunStatus = "SNAPSHOTTING"
	RunStatusSpecifyingTask     RunStatus = "SPECIFYING_TASK"
	RunStatusBlueprinting       RunStatus = "BLUEPRINTING"
	RunStatusPlanningManifest   RunStatus = "PLANNING_MANIFEST"
	RunStatusAwaitingToolResult RunStatus = "AWAITING_TOOL_RESULT"
	RunStatusGeneratingPrograms RunStatus = "GENERATING_PROGRAMS"
	RunStatusReadyToCommit      RunStatus = "READY_TO_COMMIT"
	RunStatusCancelled          RunStatus = "CANCELLED"
	RunStatusFailed             RunStatus = "FAILED"
)

// runStatuses is the set of valid members, mirroring the Python enum.
var runStatuses = map[RunStatus]struct{}{
	RunStatusSnapshotting:       {},
	RunStatusSpecifyingTask:     {},
	RunStatusBlueprinting:       {},
	RunStatusPlanningManifest:   {},
	RunStatusAwaitingToolResult: {},
	RunStatusGeneratingPrograms: {},
	RunStatusReadyToCommit:      {},
	RunStatusCancelled:          {},
	RunStatusFailed:             {},
}

// ParseRunStatus converts a persisted TEXT status into a RunStatus.
//
// Python: RunStatus(row['status']), which raises ValueError for a value that is
// not a member of the enum.
func ParseRunStatus(value string) (RunStatus, error) {
	status := RunStatus(value)
	if _, ok := runStatuses[status]; !ok {
		return "", fmt.Errorf("runstore: %q is not a valid run status", value)
	}
	return status, nil
}

// terminalRunStatuses mirrors TERMINAL_RUN_STATUSES.
var terminalRunStatuses = map[RunStatus]struct{}{
	RunStatusReadyToCommit: {},
	RunStatusCancelled:     {},
	RunStatusFailed:        {},
}

// allowedTransitions mirrors _ALLOWED_TRANSITIONS.
var allowedTransitions = map[RunStatus]map[RunStatus]struct{}{
	RunStatusSnapshotting: {
		RunStatusSpecifyingTask: {},
		RunStatusCancelled:      {},
		RunStatusFailed:         {},
	},
	RunStatusSpecifyingTask: {
		RunStatusBlueprinting: {},
		RunStatusCancelled:    {},
		RunStatusFailed:       {},
	},
	RunStatusBlueprinting: {
		RunStatusPlanningManifest: {},
		RunStatusCancelled:        {},
		RunStatusFailed:           {},
	},
	RunStatusPlanningManifest: {
		RunStatusAwaitingToolResult: {},
		RunStatusGeneratingPrograms: {},
		RunStatusCancelled:          {},
		RunStatusFailed:             {},
	},
	RunStatusAwaitingToolResult: {
		RunStatusGeneratingPrograms: {},
		RunStatusCancelled:          {},
		RunStatusFailed:             {},
	},
	RunStatusGeneratingPrograms: {
		RunStatusReadyToCommit: {},
		RunStatusCancelled:     {},
		RunStatusFailed:        {},
	},
	RunStatusReadyToCommit: {},
	RunStatusCancelled:     {},
	RunStatusFailed:        {},
}

// ErrRunTransition is the sentinel wrapped by every *RunTransitionError. The
// HTTP layer maps it to 409 RUN_STATE_CONFLICT.
var ErrRunTransition = errors.New("run state conflict")

// RunTransitionError reports a rejected run status change or a run whose state
// forbids the requested mutation.
//
// Python: RunTransitionError(ValueError). The message is preserved verbatim so
// the HTTP layer can forward str(error) exactly as _http_error does.
type RunTransitionError struct {
	Message string
}

// Error implements the error interface. Python: str(RunTransitionError(...)).
func (e *RunTransitionError) Error() string {
	if e.Message == "" {
		return "run state conflict"
	}
	return e.Message
}

// Unwrap exposes ErrRunTransition for errors.Is.
func (e *RunTransitionError) Unwrap() error { return ErrRunTransition }

// NewRunTransitionError builds the error raised by the state machine.
func NewRunTransitionError(format string, args ...any) *RunTransitionError {
	return &RunTransitionError{Message: fmt.Sprintf(format, args...)}
}

// IsTerminal reports whether status is one of the terminal statuses.
//
// Python: is_terminal().
func IsTerminal(status RunStatus) bool {
	_, ok := terminalRunStatuses[status]
	return ok
}

// AssertTransition validates a status change, returning the same errors the
// Python assert_transition raises.
//
// Python: assert_transition(current, target) -> None (raises RunTransitionError).
func AssertTransition(current, target RunStatus) error {
	if current == target {
		return nil
	}
	if IsTerminal(current) {
		return NewRunTransitionError("terminal run status %s cannot transition", current)
	}
	if _, ok := allowedTransitions[current][target]; !ok {
		return NewRunTransitionError("run status %s cannot transition to %s", current, target)
	}
	return nil
}
