package runstore

import (
	"errors"
	"slices"
	"testing"
)

func TestRunProtocolVersion(t *testing.T) {
	if RunProtocolVersion != "ai-scratch-run/1" {
		t.Fatalf("RunProtocolVersion = %q, want %q", RunProtocolVersion, "ai-scratch-run/1")
	}
}

func TestRunStatusValuesMatchPythonEnum(t *testing.T) {
	want := []struct {
		status RunStatus
		value  string
	}{
		{RunStatusSnapshotting, "SNAPSHOTTING"},
		{RunStatusSpecifyingTask, "SPECIFYING_TASK"},
		{RunStatusBlueprinting, "BLUEPRINTING"},
		{RunStatusPlanningManifest, "PLANNING_MANIFEST"},
		{RunStatusAwaitingToolResult, "AWAITING_TOOL_RESULT"},
		{RunStatusGeneratingPrograms, "GENERATING_PROGRAMS"},
		{RunStatusReadyToCommit, "READY_TO_COMMIT"},
		{RunStatusCancelled, "CANCELLED"},
		{RunStatusFailed, "FAILED"},
	}
	for _, item := range want {
		if string(item.status) != item.value {
			t.Fatalf("status %q != %q", item.status, item.value)
		}
		parsed, err := ParseRunStatus(item.value)
		if err != nil {
			t.Fatalf("ParseRunStatus(%q): %v", item.value, err)
		}
		if parsed != item.status {
			t.Fatalf("ParseRunStatus(%q) = %q", item.value, parsed)
		}
	}
	if _, err := ParseRunStatus("NOT_A_STATUS"); err == nil {
		t.Fatal("ParseRunStatus accepted an unknown status")
	}
	if _, err := ParseRunStatus("snapshotting"); err == nil {
		t.Fatal("ParseRunStatus accepted a lowercase status")
	}
}

func TestIsTerminal(t *testing.T) {
	terminal := map[RunStatus]bool{
		RunStatusReadyToCommit: true,
		RunStatusCancelled:     true,
		RunStatusFailed:        true,
	}
	all := []RunStatus{
		RunStatusSnapshotting,
		RunStatusSpecifyingTask,
		RunStatusBlueprinting,
		RunStatusPlanningManifest,
		RunStatusAwaitingToolResult,
		RunStatusGeneratingPrograms,
		RunStatusReadyToCommit,
		RunStatusCancelled,
		RunStatusFailed,
	}
	for _, status := range all {
		if got := IsTerminal(status); got != terminal[status] {
			t.Fatalf("IsTerminal(%s) = %v, want %v", status, got, terminal[status])
		}
	}
}

func TestAssertTransitionMirrorsPythonMatrix(t *testing.T) {
	allowed := map[RunStatus][]RunStatus{
		RunStatusSnapshotting:       {RunStatusSpecifyingTask, RunStatusCancelled, RunStatusFailed},
		RunStatusSpecifyingTask:     {RunStatusBlueprinting, RunStatusCancelled, RunStatusFailed},
		RunStatusBlueprinting:       {RunStatusPlanningManifest, RunStatusCancelled, RunStatusFailed},
		RunStatusPlanningManifest:   {RunStatusAwaitingToolResult, RunStatusGeneratingPrograms, RunStatusCancelled, RunStatusFailed},
		RunStatusAwaitingToolResult: {RunStatusGeneratingPrograms, RunStatusCancelled, RunStatusFailed},
		RunStatusGeneratingPrograms: {RunStatusReadyToCommit, RunStatusCancelled, RunStatusFailed},
		RunStatusReadyToCommit:      {},
		RunStatusCancelled:          {},
		RunStatusFailed:             {},
	}
	all := []RunStatus{
		RunStatusSnapshotting,
		RunStatusSpecifyingTask,
		RunStatusBlueprinting,
		RunStatusPlanningManifest,
		RunStatusAwaitingToolResult,
		RunStatusGeneratingPrograms,
		RunStatusReadyToCommit,
		RunStatusCancelled,
		RunStatusFailed,
	}
	for _, current := range all {
		for _, target := range all {
			err := AssertTransition(current, target)
			wantOK := current == target || slices.Contains(allowed[current], target)
			if wantOK {
				if err != nil {
					t.Fatalf("AssertTransition(%s, %s) = %v, want nil", current, target, err)
				}
				continue
			}
			if err == nil {
				t.Fatalf("AssertTransition(%s, %s) = nil, want error", current, target)
			}
			if !errors.Is(err, ErrRunTransition) {
				t.Fatalf("AssertTransition(%s, %s) does not wrap ErrRunTransition: %v", current, target, err)
			}
			var transitionErr *RunTransitionError
			if !errors.As(err, &transitionErr) {
				t.Fatalf("AssertTransition(%s, %s) is not *RunTransitionError: %v", current, target, err)
			}
			wantMessage := ""
			if IsTerminal(current) {
				wantMessage = "terminal run status " + string(current) + " cannot transition"
			} else {
				wantMessage = "run status " + string(current) + " cannot transition to " + string(target)
			}
			if err.Error() != wantMessage {
				t.Fatalf("AssertTransition(%s, %s) message = %q, want %q", current, target, err.Error(), wantMessage)
			}
		}
	}
}

func TestAssertTransitionToSameTerminalStatusIsAllowed(t *testing.T) {
	// Python returns early when current == target, before the terminal check.
	for _, status := range []RunStatus{RunStatusFailed, RunStatusCancelled, RunStatusReadyToCommit} {
		if err := AssertTransition(status, status); err != nil {
			t.Fatalf("AssertTransition(%s, %s) = %v, want nil", status, status, err)
		}
	}
}

func TestRunTransitionErrorMessageDefault(t *testing.T) {
	err := &RunTransitionError{}
	if err.Error() != "run state conflict" {
		t.Fatalf("empty RunTransitionError = %q", err.Error())
	}
}
