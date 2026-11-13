package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ai-scratch-web/backend/internal/config"
	"github.com/ai-scratch-web/backend/internal/runmanager"
	"github.com/ai-scratch-web/backend/internal/runstore"
	"github.com/ai-scratch-web/backend/internal/schema"
)

func (s *Server) registerAIRuns(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/ai-runs", s.requireDurableRuns(s.handleAIRunCreate))
	mux.HandleFunc("GET /api/ai-runs/metrics", s.requireDurableRuns(s.handleAIRunMetrics))
	mux.HandleFunc("GET /api/ai-runs/{run_id}", s.requireDurableRuns(s.handleAIRunGet))
	mux.HandleFunc("POST /api/ai-runs/{run_id}/client-commit-metrics", s.requireDurableRuns(s.handleAIRunClientCommitMetric))
	mux.HandleFunc("POST /api/ai-runs/{run_id}/resource-review-metrics", s.requireDurableRuns(s.handleAIRunResourceReviewMetric))
	mux.HandleFunc("GET /api/ai-runs/{run_id}/events", s.requireDurableRuns(s.handleAIRunEvents))
	mux.HandleFunc("POST /api/ai-runs/{run_id}/decisions", s.requireDurableRuns(s.handleAIRunDecision))
	mux.HandleFunc("POST /api/ai-runs/{run_id}/tool-results", s.requireDurableRuns(s.handleAIRunToolResult))
	mux.HandleFunc("POST /api/ai-runs/{run_id}/program-contexts", s.requireDurableRuns(s.handleAIRunProgramContexts))
	mux.HandleFunc("POST /api/ai-runs/{run_id}/cancel", s.requireDurableRuns(s.handleAIRunCancel))
}

func (s *Server) requireDurableRuns(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if !s.Settings.AIDurableRunsEnabled {
			WriteError(writer, http.StatusNotFound, "DURABLE_RUNS_DISABLED", "持久化 AI 任务尚未启用")
			return
		}
		if s.runManager() == nil {
			WriteError(writer, http.StatusServiceUnavailable, "RUN_STORE_UNAVAILABLE", "持久化 AI 任务存储不可用")
			return
		}
		next(writer, request)
	}
}

func (s *Server) requireRollout(writer http.ResponseWriter, ownerScopeHash string) bool {
	if !s.Settings.DurableRunsEnabledForScopeHash(ownerScopeHash) {
		WriteError(writer, http.StatusNotFound, "DURABLE_RUNS_NOT_ROLLED_OUT", "持久化 AI 任务尚未对当前会话开放")
		return false
	}
	return true
}

// scopeHash mirrors _scope_hash.
func scopeHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// ownerScope mirrors _owner_scope.
func ownerScope(writer http.ResponseWriter, request *http.Request) (string, bool) {
	raw := request.Header.Get("X-AI-Run-Scope")
	trimmed := strings.TrimSpace(raw)
	if raw == "" || trimmed == "" || len(raw) > 512 {
		WriteError(writer, http.StatusBadRequest, "RUN_SCOPE_REQUIRED", "缺少本地会话作用域")
		return "", false
	}
	return scopeHash(trimmed), true
}

// httpErrorFromRun maps store/orchestration errors onto the FastAPI status codes.
func httpErrorFromRun(writer http.ResponseWriter, err error) {
	if errors.Is(err, runstore.ErrRunCapacity) {
		writer.Header().Set("Retry-After", "30")
		WriteError(writer, http.StatusTooManyRequests, "AI_RUN_CAPACITY", "未结束任务已达上限，请完成或取消现有任务后重试")
		return
	}
	var notFound *runstore.RunNotFoundError
	if errors.As(err, &notFound) {
		WriteError(writer, http.StatusNotFound, "RUN_NOT_FOUND", "未找到该生成任务")
		return
	}
	var idempotency *runstore.IdempotencyConflictError
	if errors.As(err, &idempotency) {
		WriteError(writer, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "幂等键或工具结果与首次请求不一致")
		return
	}
	var toolConflict *runstore.ToolResultConflictError
	if errors.As(err, &toolConflict) {
		WriteError(writer, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "幂等键或工具结果与首次请求不一致")
		return
	}
	if errors.Is(err, runstore.ErrRunTransition) {
		WriteError(writer, http.StatusConflict, "RUN_STATE_CONFLICT", err.Error())
		return
	}
	s := &config.ConfigurationError{}
	_ = s
	WriteError(writer, http.StatusUnprocessableEntity, "AI_RUN_REQUEST_FAILED", "AI 任务请求失败")
}

func runIDFrom(request *http.Request) string { return request.PathValue("run_id") }

func (s *Server) decodeRunBody(writer http.ResponseWriter, request *http.Request, target any) bool {
	body, ok := s.ReadBody(writer, request)
	if !ok {
		return false
	}
	if err := schema.DecodeStrict(body, target); err != nil {
		WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
		return false
	}
	return true
}

type aiRunCreateRequest struct {
	Generation schema.UnifiedGenerationRequest `json:"generation"`
}

func (s *Server) handleAIRunCreate(writer http.ResponseWriter, request *http.Request) {
	ownerScopeHash, ok := ownerScope(writer, request)
	if !ok {
		return
	}
	if !s.requireRollout(writer, ownerScopeHash) {
		return
	}
	var body aiRunCreateRequest
	if !s.decodeRunBody(writer, request, &body) {
		return
	}
	if body.Generation.Scope != "project" || body.Generation.ProjectContext == nil {
		WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
		return
	}
	selected, ok := s.requireProvider(writer, string(body.Generation.ModelTier))
	if !ok {
		return
	}
	response, err := s.runManager().Start(request.Context(), runmanager.StartParams{
		Request:        &body.Generation,
		OwnerScopeHash: ownerScopeHash,
		Provider:       selected,
		Semaphore:      s.deps().blocksSemaphore,
		Orchestrator:   s.deps().orchestrator,
	})
	if err != nil {
		httpErrorFromRun(writer, err)
		return
	}
	WriteJSON(writer, http.StatusAccepted, response)
}

func (s *Server) handleAIRunMetrics(writer http.ResponseWriter, request *http.Request) {
	ownerScopeHash, ok := ownerScope(writer, request)
	if !ok {
		return
	}
	windowHours := 168
	if raw := request.URL.Query().Get("window_hours"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 24*30 {
			WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
			return
		}
		windowHours = parsed
	}
	stuckAfterMs := int64(s.Settings.AIBlocksProviderTimeoutSeconds * 1000)
	response, err := s.runManager().Store().MetricsForOwner(runstore.MetricsForOwnerParams{
		OwnerScopeHash: ownerScopeHash,
		WindowHours:    windowHours,
		StuckAfterMs:   &stuckAfterMs,
	})
	if err != nil {
		httpErrorFromRun(writer, err)
		return
	}
	WriteJSON(writer, http.StatusOK, response)
}

func (s *Server) handleAIRunGet(writer http.ResponseWriter, request *http.Request) {
	ownerScopeHash, ok := ownerScope(writer, request)
	if !ok {
		return
	}
	record, err := s.runManager().Store().GetRun(runIDFrom(request), ownerScopeHash)
	if err != nil {
		httpErrorFromRun(writer, err)
		return
	}
	WriteJSON(writer, http.StatusOK, record.PublicPayload())
}

type aiRunClientCommitMetricRequest struct {
	IdempotencyKey          string  `json:"idempotency_key"`
	ExpectedProjectEpoch    int64   `json:"expected_project_epoch"`
	Outcome                 string  `json:"outcome"`
	CommitDurationMs        int64   `json:"commit_duration_ms"`
	VerifyDurationMs        int64   `json:"verify_duration_ms"`
	BehaviorTestCount       int64   `json:"behavior_test_count"`
	BehaviorPassedCount     int64   `json:"behavior_passed_count"`
	BehaviorMustCount       int64   `json:"behavior_must_count"`
	BehaviorMustPassedCount int64   `json:"behavior_must_passed_count"`
	ErrorCode               *string `json:"error_code"`
}

func (s *Server) handleAIRunClientCommitMetric(writer http.ResponseWriter, request *http.Request) {
	ownerScopeHash, ok := ownerScope(writer, request)
	if !ok {
		return
	}
	var body aiRunClientCommitMetricRequest
	if !s.decodeRunBody(writer, request, &body) {
		return
	}
	response, _, err := s.runManager().Store().RecordClientCommitMetric(runstore.RecordClientCommitMetricParams{
		RunID:                   runIDFrom(request),
		OwnerScopeHash:          ownerScopeHash,
		ExpectedProjectEpoch:    body.ExpectedProjectEpoch,
		IdempotencyKey:          body.IdempotencyKey,
		CommitDurationMs:        body.CommitDurationMs,
		VerifyDurationMs:        body.VerifyDurationMs,
		BehaviorTestCount:       body.BehaviorTestCount,
		BehaviorPassedCount:     body.BehaviorPassedCount,
		BehaviorMustCount:       body.BehaviorMustCount,
		BehaviorMustPassedCount: body.BehaviorMustPassedCount,
		Outcome:                 body.Outcome,
		ErrorCode:               body.ErrorCode,
	})
	if err != nil {
		httpErrorFromRun(writer, err)
		return
	}
	WriteJSON(writer, http.StatusOK, response)
}

type aiRunResourceReviewBucket struct {
	ResourceType            string `json:"resource_type"`
	AutoSelectedCount       int64  `json:"auto_selected_count"`
	AutoReselectedCount     int64  `json:"auto_reselected_count"`
	ManualSelectedCount     int64  `json:"manual_selected_count"`
	ReviewRequiredCount     int64  `json:"review_required_count"`
	GenerationFallbackCount int64  `json:"generation_fallback_count"`
}

type aiRunResourceReviewMetricRequest struct {
	IdempotencyKey       string                      `json:"idempotency_key"`
	ExpectedProjectEpoch int64                       `json:"expected_project_epoch"`
	Buckets              []aiRunResourceReviewBucket `json:"buckets"`
}

func (s *Server) handleAIRunResourceReviewMetric(writer http.ResponseWriter, request *http.Request) {
	ownerScopeHash, ok := ownerScope(writer, request)
	if !ok {
		return
	}
	var body aiRunResourceReviewMetricRequest
	if !s.decodeRunBody(writer, request, &body) {
		return
	}
	buckets := make([]map[string]any, 0, len(body.Buckets))
	for _, bucket := range body.Buckets {
		buckets = append(buckets, map[string]any{
			"resource_type":             bucket.ResourceType,
			"auto_selected_count":       bucket.AutoSelectedCount,
			"auto_reselected_count":     bucket.AutoReselectedCount,
			"manual_selected_count":     bucket.ManualSelectedCount,
			"review_required_count":     bucket.ReviewRequiredCount,
			"generation_fallback_count": bucket.GenerationFallbackCount,
		})
	}
	response, _, err := s.runManager().Store().RecordResourceReviewMetric(runstore.RecordResourceReviewMetricParams{
		RunID:                runIDFrom(request),
		OwnerScopeHash:       ownerScopeHash,
		ExpectedProjectEpoch: body.ExpectedProjectEpoch,
		IdempotencyKey:       body.IdempotencyKey,
		Buckets:              buckets,
	})
	if err != nil {
		httpErrorFromRun(writer, err)
		return
	}
	WriteJSON(writer, http.StatusOK, response)
}

func (s *Server) handleAIRunEvents(writer http.ResponseWriter, request *http.Request) {
	ownerScopeHash, ok := ownerScope(writer, request)
	if !ok {
		return
	}
	runID := runIDFrom(request)
	manager := s.runManager()

	sequence := int64(0)
	if lastEventID := request.Header.Get("Last-Event-ID"); lastEventID != "" {
		resolved, err := manager.Store().SequenceForEvent(runstore.SequenceForEventParams{
			RunID:          runID,
			OwnerScopeHash: ownerScopeHash,
			EventID:        lastEventID,
		})
		if err != nil {
			httpErrorFromRun(writer, err)
			return
		}
		if resolved == nil {
			WriteError(writer, http.StatusGone, "EVENT_CURSOR_EXPIRED", "事件游标已过期，请先读取任务状态")
			return
		}
		sequence = *resolved
	} else if _, err := manager.Store().GetRun(runID, ownerScopeHash); err != nil {
		httpErrorFromRun(writer, err)
		return
	}

	sse, ok := NewSSEWriter(writer)
	if !ok {
		WriteError(writer, http.StatusInternalServerError, "STREAM_UNAVAILABLE", "服务端不支持流式响应")
		return
	}
	events, errs := runmanager.SSEEvents(request.Context(), manager, runID, ownerScopeHash, sequence)
	for {
		select {
		case <-request.Context().Done():
			return
		case event, open := <-events:
			if !open {
				return
			}
			if event == nil {
				sse.Comment("keepalive")
				continue
			}
			envelope, err := json.Marshal(event.Envelope())
			if err != nil {
				continue
			}
			sse.Raw("id: " + event.EventID + "\nevent: " + event.Type + "\ndata: " + string(envelope) + "\n\n")
		case err, open := <-errs:
			if !open {
				continue
			}
			if err != nil {
				httpErrorFromRun(writer, err)
			}
			return
		}
	}
}

type aiRunDecisionRequest struct {
	IdempotencyKey       string  `json:"idempotency_key"`
	ExpectedProjectEpoch int64   `json:"expected_project_epoch"`
	DecisionKind         string  `json:"decision_kind"`
	NeedID               *string `json:"need_id"`
	SelectedCandidateID  *string `json:"selected_candidate_id"`
	Approved             *bool   `json:"approved"`
}

func (s *Server) handleAIRunDecision(writer http.ResponseWriter, request *http.Request) {
	ownerScopeHash, ok := ownerScope(writer, request)
	if !ok {
		return
	}
	var body aiRunDecisionRequest
	if !s.decodeRunBody(writer, request, &body) {
		return
	}
	if len(body.IdempotencyKey) < 16 || len(body.IdempotencyKey) > 160 || body.ExpectedProjectEpoch < 0 {
		WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
		return
	}
	payload := map[string]any{
		"decision_kind":          body.DecisionKind,
		"expected_project_epoch": body.ExpectedProjectEpoch,
	}
	if body.NeedID != nil {
		payload["need_id"] = *body.NeedID
	}
	if body.SelectedCandidateID != nil {
		payload["selected_candidate_id"] = *body.SelectedCandidateID
	}
	if body.Approved != nil {
		payload["approved"] = *body.Approved
	}
	switch body.DecisionKind {
	case "asset_choice":
		if body.NeedID == nil || body.SelectedCandidateID == nil {
			WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
			return
		}
	case "generation_consent":
		if body.NeedID == nil || body.Approved == nil {
			WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
			return
		}
	case "resource_policy":
		if body.Approved == nil {
			WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
			return
		}
	default:
		WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
		return
	}
	response, err := s.runManager().SubmitDecision(request.Context(), runmanager.DecisionParams{
		RunID:                runIDFrom(request),
		OwnerScopeHash:       ownerScopeHash,
		IdempotencyKey:       body.IdempotencyKey,
		ExpectedProjectEpoch: body.ExpectedProjectEpoch,
		Payload:              payload,
	})
	if err != nil {
		httpErrorFromRun(writer, err)
		return
	}
	WriteJSON(writer, http.StatusOK, response)
}

type aiRunToolResultRequest struct {
	ToolCallID           string   `json:"tool_call_id"`
	IdempotencyKey       string   `json:"idempotency_key"`
	ExpectedProjectEpoch int64    `json:"expected_project_epoch"`
	NeedID               string   `json:"need_id"`
	OpID                 string   `json:"op_id"`
	SelectedSourceKind   string   `json:"selected_source_kind"`
	NormalizedName       *string  `json:"normalized_name"`
	NormalizedTags       []string `json:"normalized_tags"`
	Width                *int64   `json:"width"`
	Height               *int64   `json:"height"`
	DurationMs           *int64   `json:"duration_ms"`
}

func (s *Server) handleAIRunToolResult(writer http.ResponseWriter, request *http.Request) {
	ownerScopeHash, ok := ownerScope(writer, request)
	if !ok {
		return
	}
	var body aiRunToolResultRequest
	if !s.decodeRunBody(writer, request, &body) {
		return
	}
	if len(body.IdempotencyKey) < 16 || len(body.IdempotencyKey) > 160 || body.ExpectedProjectEpoch < 0 {
		WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
		return
	}
	switch body.SelectedSourceKind {
	case "skipped":
		if body.NormalizedName != nil || body.Width != nil || body.Height != nil || body.DurationMs != nil {
			WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
			return
		}
	case "scratch_library", "image_generation", "audio_generation":
		if body.NormalizedName == nil {
			WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
			return
		}
	default:
		WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
		return
	}
	if (body.Width == nil) != (body.Height == nil) {
		WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
		return
	}

	runID := runIDFrom(request)
	manager := s.runManager()
	tier := "normal"
	if record, err := manager.Store().GetRun(runID, ownerScopeHash); err == nil {
		if value, ok := record.ArtifactSummary["model_tier"].(string); ok {
			tier = value
		}
	}
	selected, ok := s.requireProvider(writer, tier)
	if !ok {
		return
	}
	payload := map[string]any{
		"tool_call_id":           body.ToolCallID,
		"expected_project_epoch": body.ExpectedProjectEpoch,
		"need_id":                body.NeedID,
		"op_id":                  body.OpID,
		"selected_source_kind":   body.SelectedSourceKind,
		"normalized_tags":        emptyStrings(body.NormalizedTags),
	}
	if body.NormalizedName != nil {
		payload["normalized_name"] = *body.NormalizedName
	}
	if body.Width != nil {
		payload["width"] = *body.Width
	}
	if body.Height != nil {
		payload["height"] = *body.Height
	}
	if body.DurationMs != nil {
		payload["duration_ms"] = *body.DurationMs
	}
	response, err := manager.SubmitToolResult(request.Context(), runmanager.ToolResultParams{
		RunID:          runID,
		OwnerScopeHash: ownerScopeHash,
		ToolCallID:     body.ToolCallID,
		IdempotencyKey: body.IdempotencyKey,
		Payload:        payload,
		Provider:       selected,
		Semaphore:      s.deps().blocksSemaphore,
		Orchestrator:   s.deps().orchestrator,
	})
	if err != nil {
		httpErrorFromRun(writer, err)
		return
	}
	WriteJSON(writer, http.StatusOK, response)
}

type aiRunProgramContextRequest struct {
	ExpectedProjectEpoch int64                `json:"expected_project_epoch"`
	TargetIndex          int                  `json:"target_index"`
	Target               schema.TargetContext `json:"target"`
}

func (s *Server) handleAIRunProgramContexts(writer http.ResponseWriter, request *http.Request) {
	ownerScopeHash, ok := ownerScope(writer, request)
	if !ok {
		return
	}
	var body aiRunProgramContextRequest
	if !s.decodeRunBody(writer, request, &body) {
		return
	}
	if body.ExpectedProjectEpoch < 0 || body.TargetIndex < 0 || body.TargetIndex > 11 {
		WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
		return
	}
	runID := runIDFrom(request)
	manager := s.runManager()
	tier := "normal"
	if record, err := manager.Store().GetRun(runID, ownerScopeHash); err == nil {
		if value, ok := record.ArtifactSummary["model_tier"].(string); ok {
			tier = value
		}
	}
	selected, ok := s.requireProvider(writer, tier)
	if !ok {
		return
	}
	response, err := manager.SubmitProgramContexts(request.Context(), runmanager.ProgramContextsParams{
		RunID:                runID,
		OwnerScopeHash:       ownerScopeHash,
		ExpectedProjectEpoch: body.ExpectedProjectEpoch,
		TargetIndex:          body.TargetIndex,
		Target:               body.Target,
		Provider:             selected,
		Semaphore:            s.deps().blocksSemaphore,
		Orchestrator:         s.deps().orchestrator,
	})
	if err != nil {
		httpErrorFromRun(writer, err)
		return
	}
	WriteJSON(writer, http.StatusOK, response)
}

type aiRunCancelRequest struct {
	IdempotencyKey       string `json:"idempotency_key"`
	ExpectedProjectEpoch int64  `json:"expected_project_epoch"`
}

func (s *Server) handleAIRunCancel(writer http.ResponseWriter, request *http.Request) {
	ownerScopeHash, ok := ownerScope(writer, request)
	if !ok {
		return
	}
	var body aiRunCancelRequest
	if !s.decodeRunBody(writer, request, &body) {
		return
	}
	if len(body.IdempotencyKey) < 16 || len(body.IdempotencyKey) > 160 || body.ExpectedProjectEpoch < 0 {
		WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
		return
	}
	response, err := s.runManager().Cancel(request.Context(), runmanager.CancelParams{
		RunID:                runIDFrom(request),
		OwnerScopeHash:       ownerScopeHash,
		ExpectedProjectEpoch: body.ExpectedProjectEpoch,
		IdempotencyKey:       body.IdempotencyKey,
	})
	if err != nil {
		httpErrorFromRun(writer, err)
		return
	}
	WriteJSON(writer, http.StatusOK, response)
}

func emptyStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

var _ = context.Background
