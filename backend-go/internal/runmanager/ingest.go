package runmanager

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/ai-scratch-web/backend/internal/orchestration"
	"github.com/ai-scratch-web/backend/internal/provider"
	"github.com/ai-scratch-web/backend/internal/runstore"
)

// ingestGenerationEvent persists one orchestrator event and returns the
// (possibly rebuilt) job id map.
//
// Python: AiRunManager._ingest_generation_event.
func (m *AiRunManager) ingestGenerationEvent(
	ctx context.Context,
	runID string,
	ownerScopeHash string,
	event orchestration.GenerationEvent,
	jobIDs map[int]string,
	prov *provider.TextProvider,
	semaphore chan struct{},
	orch *orchestration.GenerationOrchestrator,
	terminalOnComplete bool,
) (map[int]string, error) {
	data := copyMap(event.Data)
	if event.Type == "content_delta" || event.Type == "reasoning_delta" {
		// Provider text is deliberately excluded from durable storage and the
		// public event stream.
		return jobIDs, nil
	}
	if event.Type == "phase_start" {
		return m.ingestPhaseStart(runID, ownerScopeHash, event.Phase, data, jobIDs)
	}
	if event.Type == "phase_complete" {
		updated, err := m.ingestPhaseComplete(runID, ownerScopeHash, event.Phase, data, jobIDs)
		if err != nil {
			return jobIDs, err
		}
		m.signal(runID)
		return updated, nil
	}

	switch event.Type {
	case "script_ready":
		index, isIndex := pythonInt(data["target_index"])
		jobID, hasJob := "", false
		if isIndex {
			jobID, hasJob = jobIDs[index]
		}
		if isIndex && hasJob {
			if _, err := m.appendEvent(runID, ownerScopeHash, "generating_programs", "program_script_ready", mergeJobID(jobID, data), 1); err != nil {
				return jobIDs, err
			}
		} else if err := m.failIfActive(runID, ownerScopeHash, "RUN_PROTOCOL_ERROR", "脚本事件缺少已声明的 job_id"); err != nil {
			return jobIDs, err
		}
	case "repair":
		attempt, err := attemptFromData(data)
		if err != nil {
			return jobIDs, err
		}
		if _, err := m.appendEvent(runID, ownerScopeHash, defaultPhase(event.Phase, "generating_programs"), "repair_started", data, attempt); err != nil {
			return jobIDs, err
		}
	case "validation_error":
		if _, err := m.appendEvent(runID, ownerScopeHash, defaultPhase(event.Phase, "generating_programs"), "validation_failed", data, 1); err != nil {
			return jobIDs, err
		}
	case "error":
		code := pyStr(valueOrDefault(data, "code", "AI_GENERATION_ERROR"))
		message := pyStr(valueOrDefault(data, "message", "AI 输出未完成，请重新生成"))
		if err := m.failIfActive(runID, ownerScopeHash, code, message); err != nil {
			return jobIDs, err
		}
		// A program target validation error is terminal for the run. Raise into
		// the gather supervising the dependency layer so peer target requests
		// are cancelled instead of continuing after failure.
		if !terminalOnComplete {
			return jobIDs, runstore.NewRunTransitionError("项目目标程序生成失败")
		}
	case "complete":
		updated, err := m.ingestComplete(runID, ownerScopeHash, data, jobIDs, terminalOnComplete)
		if err != nil {
			return jobIDs, err
		}
		if !terminalOnComplete {
			return updated, nil
		}
		jobIDs = updated
	case "metadata", "shared_state", "cache_hit", "usage":
		if _, err := m.appendEvent(runID, ownerScopeHash, defaultPhase(event.Phase, "generating_programs"), event.Type, data, 1); err != nil {
			return jobIDs, err
		}
	}
	m.signal(runID)
	return jobIDs, nil
}

// ingestPhaseStart ports the phase_start branch.
func (m *AiRunManager) ingestPhaseStart(runID string, ownerScopeHash string, phase string, data map[string]any, jobIDs map[int]string) (map[int]string, error) {
	switch phase {
	case "blueprint":
		return jobIDs, m.transition(runID, ownerScopeHash, runstore.RunStatusBlueprinting, "blueprinting", "phase_started", map[string]any{})
	case "plan":
		return jobIDs, m.transition(runID, ownerScopeHash, runstore.RunStatusPlanningManifest, "planning_manifest", "phase_started", map[string]any{})
	case "program":
		if err := m.transition(runID, ownerScopeHash, runstore.RunStatusGeneratingPrograms, "generating_programs", "phase_started", map[string]any{}); err != nil {
			return jobIDs, err
		}
		index, isIndex := pythonInt(data["target_index"])
		if isIndex {
			if jobID, hasJob := jobIDs[index]; hasJob {
				// Preserve the same target coordinates used by the live stream;
				// the job id is an internal durable correlation key.
				if _, err := m.appendEvent(runID, ownerScopeHash, "generating_programs", "program_target_started", mergeJobID(jobID, data), 1); err != nil {
					return jobIDs, err
				}
			}
		}
		return jobIDs, nil
	default:
		return jobIDs, nil
	}
}

// ingestPhaseComplete ports the phase_complete branch.
func (m *AiRunManager) ingestPhaseComplete(runID string, ownerScopeHash string, phase string, data map[string]any, jobIDs map[int]string) (map[int]string, error) {
	switch phase {
	case "requirements":
		if _, err := m.store.MergeArtifactSummary(runstore.MergeArtifactSummaryParams{
			RunID:          runID,
			OwnerScopeHash: ownerScopeHash,
			Values:         map[string]any{"contracts": map[string]any{"requirements": data}},
		}); err != nil {
			return jobIDs, err
		}
		if _, err := m.appendEvent(runID, ownerScopeHash, "specifying_task", "requirements_ready", data, 1); err != nil {
			return jobIDs, err
		}
	case "task_spec":
		if err := m.mergeContract(runID, ownerScopeHash, "task_spec", data["task_spec"]); err != nil {
			return jobIDs, err
		}
		if _, err := m.appendEvent(runID, ownerScopeHash, "specifying_task", "task_spec_ready", data, 1); err != nil {
			return jobIDs, err
		}
	case "blueprint":
		blueprint := data["blueprint"]
		if err := m.mergeContract(runID, ownerScopeHash, "blueprint", blueprint); err != nil {
			return jobIDs, err
		}
		if blueprintObject, ok := jsonObjectValue(blueprint); ok {
			assetNeedIDs := []any{}
			if needs, ok := jsonArray(blueprintObject["asset_needs"]); ok {
				for _, item := range needs {
					itemObject, ok := jsonObjectValue(item)
					if !ok {
						continue
					}
					if needID, ok := itemObject["need_id"].(string); ok {
						assetNeedIDs = append(assetNeedIDs, needID)
					}
				}
			}
			if _, err := m.store.MergeArtifactSummary(runstore.MergeArtifactSummaryParams{
				RunID:          runID,
				OwnerScopeHash: ownerScopeHash,
				Values:         map[string]any{"blueprint_asset_need_ids": assetNeedIDs},
			}); err != nil {
				return jobIDs, err
			}
		}
		if _, err := m.appendEvent(runID, ownerScopeHash, "blueprinting", "blueprint_ready", data, 1); err != nil {
			return jobIDs, err
		}
	case "plan":
		manifest := data["manifest"]
		if manifestObject, ok := jsonObjectValue(manifest); ok {
			if targets, ok := jsonArray(manifestObject["program_targets"]); ok {
				rebuilt := map[int]string{}
				for index, target := range targets {
					targetObject, ok := jsonObjectValue(target)
					if !ok {
						continue
					}
					if jobID, ok := targetObject["job_id"].(string); ok {
						rebuilt[index] = jobID
					}
				}
				jobIDs = rebuilt
			}
			resourceOps, err := resourceOpsOf(manifestObject)
			if err != nil {
				return jobIDs, err
			}
			needOps := map[string][]string{}
			for _, operation := range resourceOps {
				operationObject, ok := jsonObjectValue(operation)
				if !ok {
					continue
				}
				sourceNeedID, hasSource := operationObject["source_need_id"].(string)
				opID, hasOp := operationObject["op_id"].(string)
				if hasSource && hasOp {
					needOps[sourceNeedID] = append(needOps[sourceNeedID], opID)
				}
			}
			if err := m.mergeContract(runID, ownerScopeHash, "plan", map[string]any{"steps": data["steps"]}); err != nil {
				return jobIDs, err
			}
			if err := m.mergeContract(runID, ownerScopeHash, "manifest", manifest); err != nil {
				return jobIDs, err
			}
			assetNeedIDs := make([]string, 0, len(needOps))
			for needID := range needOps {
				assetNeedIDs = append(assetNeedIDs, needID)
			}
			sort.Strings(assetNeedIDs)
			if _, err := m.store.MergeArtifactSummary(runstore.MergeArtifactSummaryParams{
				RunID:          runID,
				OwnerScopeHash: ownerScopeHash,
				Values: map[string]any{
					"asset_need_ids": assetNeedIDs,
					"asset_need_ops": needOps,
					"manifest_summary": map[string]any{
						"program_target_count": len(jobIDs),
						"resource_op_count":    len(resourceOps),
					},
				},
			}); err != nil {
				return jobIDs, err
			}
		}
		if _, err := m.appendEvent(runID, ownerScopeHash, "planning_manifest", "manifest_ready", data, 1); err != nil {
			return jobIDs, err
		}
	case "program":
		index, isIndex := pythonInt(data["target_index"])
		jobID, hasJob := "", false
		if isIndex {
			jobID, hasJob = jobIDs[index]
		}
		if isIndex && hasJob {
			if _, err := m.appendEvent(runID, ownerScopeHash, "generating_programs", "program_target_ready", mergeJobID(jobID, data), 1); err != nil {
				return jobIDs, err
			}
		} else if err := m.failIfActive(runID, ownerScopeHash, "RUN_PROTOCOL_ERROR", "程序事件缺少已声明的 job_id"); err != nil {
			return jobIDs, err
		}
	default:
		if _, err := m.appendEvent(runID, ownerScopeHash, defaultPhase(phase, "specifying_task"), "phase_completed", data, 1); err != nil {
			return jobIDs, err
		}
	}
	return jobIDs, nil
}

// ingestComplete ports the complete branch. It returns the possibly updated
// job id map and whether the caller should return early without signalling
// again (the zero-target manifest path signals itself, exactly like Python).
func (m *AiRunManager) ingestComplete(runID string, ownerScopeHash string, data map[string]any, jobIDs map[int]string, terminalOnComplete bool) (map[int]string, error) {
	if !terminalOnComplete {
		m.signal(runID)
		return jobIDs, nil
	}
	outputContract, _ := data["output_contract"].(string)
	if outputContract == "project_manifest" {
		record, err := m.store.GetRun(runID, ownerScopeHash)
		if err != nil {
			return jobIDs, err
		}
		summary := record.ArtifactSummary
		targetCount, _ := nestedInt(summary, "manifest_summary", "program_target_count", -1)
		if targetCount == 0 {
			if err := m.transition(runID, ownerScopeHash, runstore.RunStatusReadyToCommit, "ready_to_commit", "generation_complete", data); err != nil {
				return jobIDs, err
			}
			m.signal(runID)
			return jobIDs, nil
		}
		toolCalls := []any{}
		if needIDs, ok := jsonArray(summary["asset_need_ids"]); ok {
			for _, item := range needIDs {
				if needID, ok := item.(string); ok {
					toolCalls = append(toolCalls, map[string]any{
						"tool_call_id": "resolve_" + needID,
						"need_id":      needID,
					})
				}
			}
		}
		return jobIDs, m.transition(runID, ownerScopeHash, runstore.RunStatusAwaitingToolResult, "awaiting_tool_result", "asset_resolution_required", map[string]any{
			"program_context_required": true,
			"tool_calls":               toolCalls,
		})
	}
	return jobIDs, m.transition(runID, ownerScopeHash, runstore.RunStatusReadyToCommit, "ready_to_commit", "generation_complete", data)
}

// resourceOpsOf ports manifest.get('resource_ops', []): a missing key is an
// empty list, while an explicit null or a non-list raises the TypeError that
// Python reports when iterating or measuring it.
func resourceOpsOf(manifestObject map[string]any) ([]any, error) {
	value, exists := manifestObject["resource_ops"]
	if !exists {
		return nil, nil
	}
	if value == nil {
		return nil, fmt.Errorf("resource_ops is not iterable")
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("resource_ops is not iterable")
	}
	return items, nil
}

// valueOrDefault ports dict.get(key, default).
func valueOrDefault(data map[string]any, key string, fallback any) any {
	if value, ok := data[key]; ok {
		return value
	}
	return fallback
}

// defaultPhase ports (event.phase or 'fallback').
func defaultPhase(phase string, fallback string) string {
	if phase == "" {
		return fallback
	}
	return phase
}

// mergeJobID ports {'job_id': job_ids[index], **data}.
func mergeJobID(jobID string, data map[string]any) map[string]any {
	payload := map[string]any{"job_id": jobID}
	for key, value := range data {
		payload[key] = value
	}
	return payload
}

// attemptFromData ports int(data.get('attempt', 1)).
func attemptFromData(data map[string]any) (int64, error) {
	value, exists := data["attempt"]
	if !exists {
		return 1, nil
	}
	if parsed, ok := jsonInt(value); ok {
		return parsed, nil
	}
	if text, ok := value.(string); ok {
		if parsed, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64); err == nil {
			return parsed, nil
		}
	}
	return 0, fmt.Errorf("attempt is not an integer")
}
