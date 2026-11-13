// Package runmanager ports backend/app/orchestration/run_manager.py.
//
// AiRunManager owns the live provider tasks while the RunStore remains the
// recovery source of truth. Every exported name mirrors one Python method or
// keyword argument; the comments call out the Python line being reproduced.
package runmanager

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/ai-scratch-web/backend/internal/config"
	"github.com/ai-scratch-web/backend/internal/orchestration"
	"github.com/ai-scratch-web/backend/internal/provider"
	"github.com/ai-scratch-web/backend/internal/runstore"
	"github.com/ai-scratch-web/backend/internal/schema"
)

// DefaultWaitTimeoutSeconds mirrors the Python default of
// wait_for_events(..., timeout_seconds=15).
const DefaultWaitTimeoutSeconds = 15.0

// sseWaitTimeoutSeconds is the polling interval of sse_events, which calls
// wait_for_events with its default timeout.
const sseWaitTimeoutSeconds = DefaultWaitTimeoutSeconds

// StartParams are the keyword arguments of AiRunManager.start.
type StartParams struct {
	Request        *schema.UnifiedGenerationRequest
	OwnerScopeHash string
	Provider       *provider.TextProvider
	Semaphore      chan struct{}
	Orchestrator   *orchestration.GenerationOrchestrator
}

// CancelParams are the keyword arguments of AiRunManager.cancel. Python returns
// (payload, replayed); the HTTP layer only consumes the payload, so Cancel
// returns just the payload.
type CancelParams struct {
	RunID                string
	OwnerScopeHash       string
	ExpectedProjectEpoch int64
	IdempotencyKey       string
}

// DecisionParams are the keyword arguments of AiRunManager.submit_decision.
type DecisionParams struct {
	RunID                string
	OwnerScopeHash       string
	IdempotencyKey       string
	ExpectedProjectEpoch int64
	Payload              map[string]any
}

// ToolResultParams are the keyword arguments of
// AiRunManager.submit_tool_result.
//
// ExpectedProjectEpoch is the Python argument of the same name. The current
// Go HTTP layer does not populate it and instead carries the value inside
// Payload (exactly like the Python route, which dumps the whole request body
// into the payload); when the field is left at its zero value the payload
// value is used so the two call styles agree.
type ToolResultParams struct {
	RunID                string
	OwnerScopeHash       string
	ToolCallID           string
	IdempotencyKey       string
	ExpectedProjectEpoch int64
	Payload              map[string]any
	Provider             *provider.TextProvider
	Semaphore            chan struct{}
	Orchestrator         *orchestration.GenerationOrchestrator
}

// ProgramContextsParams are the keyword arguments of
// AiRunManager.submit_program_contexts.
type ProgramContextsParams struct {
	RunID                string
	OwnerScopeHash       string
	ExpectedProjectEpoch int64
	TargetIndex          int
	Target               schema.TargetContext
	Provider             *provider.TextProvider
	Semaphore            chan struct{}
	Orchestrator         *orchestration.GenerationOrchestrator
}

// runTask is one live generation goroutine: the Go analogue of an
// asyncio.Task plus its cancel handle.
type runTask struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// AiRunManager owns live provider tasks for durable runs.
//
// Python guards every manager map with a single asyncio.Lock (self._guard);
// Go uses one sync.Mutex. Store calls stay synchronous inside the critical
// section exactly like the Python coroutine does.
type AiRunManager struct {
	store    *runstore.RunStore
	settings *config.Settings

	guard           sync.Mutex
	tasks           map[string]*runTask
	wakeups         map[string]*runWakeup
	requests        map[string]*schema.UnifiedGenerationRequest
	programContexts map[string]map[int]schema.TargetContext
	waiters         map[string]int
}

// NewAiRunManager builds a run manager.
//
// Python: AiRunManager.__init__.
func NewAiRunManager(store *runstore.RunStore, settings *config.Settings) *AiRunManager {
	return &AiRunManager{
		store:           store,
		settings:        settings,
		tasks:           map[string]*runTask{},
		wakeups:         map[string]*runWakeup{},
		requests:        map[string]*schema.UnifiedGenerationRequest{},
		programContexts: map[string]map[int]schema.TargetContext{},
		waiters:         map[string]int{},
	}
}

// Store returns the durable run store owned by this manager.
func (m *AiRunManager) Store() *runstore.RunStore { return m.store }

// Start creates a durable project run and launches its manifest pipeline.
//
// Python: AiRunManager.start. The request context is deliberately not the
// parent of the background task: Python's asyncio.create_task detaches the run
// from the HTTP request, so a client disconnect never cancels generation.
func (m *AiRunManager) Start(ctx context.Context, params StartParams) (map[string]any, error) {
	request := params.Request
	if request == nil || request.Scope != "project" || request.ProjectContext == nil {
		return nil, errors.New("durable runs currently require project scope and project_context")
	}
	record, err := m.store.CreateRun(runstore.CreateRunParams{
		OwnerScopeHash: params.OwnerScopeHash,
		ProjectEpoch:   int64(request.ProjectContext.Version),
		ArtifactSummary: map[string]any{
			"scope":           request.Scope,
			"output_contract": "project_manifest",
			"project_id":      request.ProjectContext.ProjectID,
			"model_tier":      string(request.ModelTier),
		},
	})
	if err != nil {
		return nil, err
	}
	m.signal(record.RunID)

	taskCtx, cancel := context.WithCancel(context.Background())
	task := &runTask{cancel: cancel, done: make(chan struct{})}
	m.guard.Lock()
	m.requests[record.RunID] = request
	m.tasks[record.RunID] = task
	m.guard.Unlock()

	go func() {
		defer close(task.done)
		m.executeManifest(taskCtx, task, record.RunID, params.OwnerScopeHash, request, params.Provider, params.Semaphore, params.Orchestrator)
	}()
	return record.PublicPayload(), nil
}

// Cancel cancels a run once, replaying the stored response for a repeated
// idempotency key.
//
// Python: AiRunManager.cancel.
func (m *AiRunManager) Cancel(ctx context.Context, params CancelParams) (map[string]any, error) {
	record, _, err := m.store.CancelIdempotent(runstore.CancelIdempotentParams{
		RunID:                params.RunID,
		OwnerScopeHash:       params.OwnerScopeHash,
		ExpectedProjectEpoch: params.ExpectedProjectEpoch,
		IdempotencyKey:       params.IdempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	m.guard.Lock()
	task := m.tasks[params.RunID]
	m.guard.Unlock()
	if task != nil {
		// Python checks task.done() first; context cancel functions are
		// idempotent, so cancelling a finished task is a no-op here too.
		task.cancel()
	}
	m.guard.Lock()
	delete(m.requests, params.RunID)
	delete(m.programContexts, params.RunID)
	m.guard.Unlock()
	m.signal(params.RunID)
	return record.PublicPayload(), nil
}

// WaitForEvents blocks until the run is signalled or the timeout expires.
//
// Python: AiRunManager.wait_for_events(run_id, timeout_seconds=15). A timeout
// and a wakeup both clear the event, matching Python's finally: wakeup.clear().
func (m *AiRunManager) WaitForEvents(runID string, timeoutSeconds float64) {
	m.waitForEvents(context.Background(), runID, secondsToDuration(timeoutSeconds))
}

// waitForEvents is WaitForEvents with a context so SSE streams stop promptly.
func (m *AiRunManager) waitForEvents(ctx context.Context, runID string, timeout time.Duration) bool {
	m.guard.Lock()
	wakeup := m.wakeups[runID]
	if wakeup == nil {
		wakeup = newRunWakeup()
		m.wakeups[runID] = wakeup
	}
	m.waiters[runID]++
	m.guard.Unlock()
	defer func() {
		m.guard.Lock()
		m.waiters[runID]--
		if m.waiters[runID] == 0 {
			delete(m.waiters, runID)
		}
		m.guard.Unlock()
	}()
	return wakeup.wait(ctx, timeout)
}

// wakeupFor returns the per-run asyncio.Event analogue, creating it on demand.
// Maintenance removes terminal entries only after their current waiters leave.
func (m *AiRunManager) wakeupFor(runID string) *runWakeup {
	m.guard.Lock()
	defer m.guard.Unlock()
	wakeup := m.wakeups[runID]
	if wakeup == nil {
		wakeup = newRunWakeup()
		m.wakeups[runID] = wakeup
	}
	return wakeup
}

// Shutdown cancels every live task and waits for it to finish.
//
// Python: AiRunManager.shutdown. The context is a Go-only escape hatch: if it
// is cancelled before a task settles, Shutdown stops waiting for the rest.
func (m *AiRunManager) Shutdown(ctx context.Context) {
	m.guard.Lock()
	tasks := make([]*runTask, 0, len(m.tasks))
	for _, task := range m.tasks {
		tasks = append(tasks, task)
	}
	m.tasks = map[string]*runTask{}
	m.requests = map[string]*schema.UnifiedGenerationRequest{}
	m.programContexts = map[string]map[int]schema.TargetContext{}
	m.guard.Unlock()

	for _, task := range tasks {
		task.cancel()
	}
	for _, task := range tasks {
		select {
		case <-task.done:
		case <-ctx.Done():
			return
		}
	}
}

// signal wakes every waiter of a run.
//
// Python: AiRunManager._signal.
func (m *AiRunManager) signal(runID string) {
	wakeup := m.wakeupFor(runID)
	wakeup.set()
}

// runWakeup is the Go analogue of one asyncio.Event used by _wakeups.
//
// Set is a broadcast: it closes the current channel so every waiter wakes, and
// leaves a pending flag so a waiter that arrives after the signal returns
// immediately (Python's Event stays set until a waiter clears it). Wait clears
// the event on both the signalled and the timeout path, mirroring the Python
// TimeoutError return plus the finally clause that clears the event.
type runWakeup struct {
	mu      sync.Mutex
	pending bool
	ch      chan struct{}
}

func newRunWakeup() *runWakeup { return &runWakeup{ch: make(chan struct{})} }

func (w *runWakeup) set() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending {
		return
	}
	w.pending = true
	close(w.ch)
	w.ch = make(chan struct{})
}

// wait reports whether the wakeup fired before the timeout or the context.
func (w *runWakeup) wait(ctx context.Context, timeout time.Duration) bool {
	w.mu.Lock()
	if w.pending {
		w.pending = false
		w.mu.Unlock()
		return true
	}
	ch := w.ch
	w.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	signalled := false
	select {
	case <-ch:
		signalled = true
	case <-timer.C:
	case <-ctx.Done():
	}

	w.mu.Lock()
	w.pending = false
	if w.ch == ch {
		w.ch = make(chan struct{})
	}
	w.mu.Unlock()
	return signalled
}

func secondsToDuration(seconds float64) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

// logError reports an execution failure with Python's logger event name and
// attributes. Project content is never logged.
func logError(event string, runID string, err error) {
	slog.Error(event, "run_id", runID, "error_type", pythonTypeName(err))
}
