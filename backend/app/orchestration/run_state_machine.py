from __future__ import annotations

from enum import StrEnum


RUN_PROTOCOL_VERSION = 'ai-scratch-run/1'


class RunStatus(StrEnum):
    SNAPSHOTTING = 'SNAPSHOTTING'
    SPECIFYING_TASK = 'SPECIFYING_TASK'
    BLUEPRINTING = 'BLUEPRINTING'
    PLANNING_MANIFEST = 'PLANNING_MANIFEST'
    AWAITING_TOOL_RESULT = 'AWAITING_TOOL_RESULT'
    GENERATING_PROGRAMS = 'GENERATING_PROGRAMS'
    READY_TO_COMMIT = 'READY_TO_COMMIT'
    CANCELLED = 'CANCELLED'
    FAILED = 'FAILED'


TERMINAL_RUN_STATUSES = frozenset({
    RunStatus.READY_TO_COMMIT,
    RunStatus.CANCELLED,
    RunStatus.FAILED,
})


_ALLOWED_TRANSITIONS: dict[RunStatus, frozenset[RunStatus]] = {
    RunStatus.SNAPSHOTTING: frozenset({RunStatus.SPECIFYING_TASK, RunStatus.CANCELLED, RunStatus.FAILED}),
    RunStatus.SPECIFYING_TASK: frozenset({RunStatus.BLUEPRINTING, RunStatus.CANCELLED, RunStatus.FAILED}),
    RunStatus.BLUEPRINTING: frozenset({RunStatus.PLANNING_MANIFEST, RunStatus.CANCELLED, RunStatus.FAILED}),
    RunStatus.PLANNING_MANIFEST: frozenset({
        RunStatus.AWAITING_TOOL_RESULT,
        RunStatus.GENERATING_PROGRAMS,
        RunStatus.CANCELLED,
        RunStatus.FAILED,
    }),
    RunStatus.AWAITING_TOOL_RESULT: frozenset({RunStatus.GENERATING_PROGRAMS, RunStatus.CANCELLED, RunStatus.FAILED}),
    RunStatus.GENERATING_PROGRAMS: frozenset({RunStatus.READY_TO_COMMIT, RunStatus.CANCELLED, RunStatus.FAILED}),
    RunStatus.READY_TO_COMMIT: frozenset(),
    RunStatus.CANCELLED: frozenset(),
    RunStatus.FAILED: frozenset(),
}


class RunTransitionError(ValueError):
    pass


def is_terminal(status: RunStatus) -> bool:
    return status in TERMINAL_RUN_STATUSES


def assert_transition(current: RunStatus, target: RunStatus) -> None:
    if current == target:
        return
    if is_terminal(current):
        raise RunTransitionError(f'terminal run status {current} cannot transition')
    if target not in _ALLOWED_TRANSITIONS[current]:
        raise RunTransitionError(f'run status {current} cannot transition to {target}')
