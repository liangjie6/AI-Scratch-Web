from __future__ import annotations

import asyncio
from collections.abc import AsyncIterator
from functools import lru_cache
from hashlib import sha256
import json
from typing import Annotated, Any, Literal

from fastapi import APIRouter, Depends, Header, HTTPException, Query
from fastapi.responses import StreamingResponse
from pydantic import Field, model_validator

from ..config import get_settings
from ..orchestration.orchestrator import GenerationOrchestrator
from ..orchestration.run_manager import AiRunManager, sse_events
from ..orchestration.run_state_machine import RunStatus, RunTransitionError
from ..orchestration.run_store import IdempotencyConflictError, RunNotFoundError, RunStore, ToolResultConflictError
from ..providers.agnes_text_provider import AgnesTextProvider
from ..schemas.ai_blocks import ShortString, StrictModel, TargetContext, UnifiedGenerationRequest
from .ai_blocks import get_ai_blocks_provider, get_ai_blocks_semaphore, get_ai_blocks_orchestrator


def _require_durable_runs_enabled() -> None:
    if not get_settings().ai_durable_runs_enabled:
        raise HTTPException(status_code=404, detail={'code': 'DURABLE_RUNS_DISABLED', 'message': '持久化 AI 任务尚未启用'})


def _require_durable_runs_rollout(owner_scope_hash: str) -> None:
    if not get_settings().durable_runs_enabled_for_scope_hash(owner_scope_hash):
        raise HTTPException(status_code=404, detail={'code': 'DURABLE_RUNS_NOT_ROLLED_OUT', 'message': '持久化 AI 任务尚未对当前会话开放'})


router = APIRouter(prefix='/api/ai-runs', tags=['ai-runs'], dependencies=[Depends(_require_durable_runs_enabled)])


class AiRunCreateRequest(StrictModel):
    generation: UnifiedGenerationRequest

    @model_validator(mode='after')
    def validate_project_generation(self) -> 'AiRunCreateRequest':
        if self.generation.scope != 'project' or self.generation.project_context is None:
            raise ValueError('durable AI runs require a project generation request')
        return self


class AiRunDecisionRequest(StrictModel):
    idempotency_key: str = Field(min_length=16, max_length=160)
    expected_project_epoch: int = Field(ge=0)
    decision_kind: Literal['asset_choice', 'generation_consent', 'resource_policy']
    need_id: ShortString | None = None
    selected_candidate_id: ShortString | None = None
    approved: bool | None = None

    @model_validator(mode='after')
    def validate_decision(self) -> 'AiRunDecisionRequest':
        if self.decision_kind == 'asset_choice' and (not self.need_id or not self.selected_candidate_id):
            raise ValueError('asset_choice requires need_id and selected_candidate_id')
        if self.decision_kind == 'generation_consent' and (not self.need_id or self.approved is None):
            raise ValueError('generation_consent requires need_id and approved')
        if self.decision_kind == 'resource_policy' and self.approved is None:
            raise ValueError('resource_policy requires approved')
        return self


class AiRunToolResultRequest(StrictModel):
    tool_call_id: ShortString
    idempotency_key: str = Field(min_length=16, max_length=160)
    expected_project_epoch: int = Field(ge=0)
    need_id: ShortString
    op_id: ShortString
    selected_source_kind: Literal['scratch_library', 'image_generation', 'audio_generation', 'skipped']
    normalized_name: ShortString | None = None
    normalized_tags: list[ShortString] = Field(default_factory=list, max_length=20)
    width: int | None = Field(default=None, ge=1, le=20_000)
    height: int | None = Field(default=None, ge=1, le=20_000)
    duration_ms: int | None = Field(default=None, ge=1, le=120_000)

    @model_validator(mode='after')
    def validate_summary(self) -> 'AiRunToolResultRequest':
        if self.selected_source_kind == 'skipped':
            if any(value is not None for value in (self.normalized_name, self.width, self.height, self.duration_ms)):
                raise ValueError('skipped tool result cannot include media metadata')
        elif not self.normalized_name:
            raise ValueError('resolved tool result requires normalized_name')
        if (self.width is None) != (self.height is None):
            raise ValueError('width and height must be provided together')
        return self


class AiRunProgramContextRequest(StrictModel):
    expected_project_epoch: int = Field(ge=0)
    target_index: int = Field(ge=0, le=11)
    target: TargetContext


class AiRunCancelRequest(StrictModel):
    idempotency_key: str = Field(min_length=16, max_length=160)
    expected_project_epoch: int = Field(ge=0)


class AiRunClientCommitMetricRequest(StrictModel):
    idempotency_key: str = Field(min_length=16, max_length=160)
    expected_project_epoch: int = Field(ge=0)
    outcome: Literal['COMPLETED', 'ROLLED_BACK', 'FAILED_NEEDS_RECOVERY', 'REJECTED_BEFORE_WRITE']
    commit_duration_ms: int = Field(ge=0, le=5 * 60 * 1000)
    verify_duration_ms: int = Field(ge=0, le=5 * 60 * 1000)
    behavior_test_count: int = Field(default=0, ge=0, le=40)
    behavior_passed_count: int = Field(default=0, ge=0, le=40)
    behavior_must_count: int = Field(default=0, ge=0, le=40)
    behavior_must_passed_count: int = Field(default=0, ge=0, le=40)
    error_code: Literal[
        'COMMIT_FAILED',
        'COMMIT_CONFLICT',
        'COMMIT_RECOVERY_FAILED',
        'SB3_ARCHIVE_INVALID',
        'SB3_RESOURCE_MISSING',
        'SB3_BEHAVIOR_FAILED',
        'SB3_VM_ROUND_TRIP_FAILED',
    ] | None = None

    @model_validator(mode='after')
    def validate_outcome(self) -> 'AiRunClientCommitMetricRequest':
        if self.outcome == 'COMPLETED' and self.error_code is not None:
            raise ValueError('completed commit metric cannot include an error code')
        if self.outcome != 'COMPLETED' and self.error_code is None:
            raise ValueError('failed commit metric requires a bounded error code')
        if self.behavior_passed_count > self.behavior_test_count:
            raise ValueError('behavior passed count cannot exceed test count')
        if self.behavior_must_count > self.behavior_test_count or self.behavior_must_passed_count > self.behavior_must_count or self.behavior_must_passed_count > self.behavior_passed_count:
            raise ValueError('behavior must counts are invalid')
        return self


class AiRunResourceReviewMetricBucket(StrictModel):
    resource_type: Literal['sprite', 'costume', 'backdrop', 'sound']
    auto_selected_count: int = Field(ge=0, le=100)
    auto_reselected_count: int = Field(ge=0, le=100)
    manual_selected_count: int = Field(ge=0, le=100)
    review_required_count: int = Field(ge=0, le=100)
    generation_fallback_count: int = Field(ge=0, le=100)

    @model_validator(mode='after')
    def validate_counts(self) -> 'AiRunResourceReviewMetricBucket':
        if self.generation_fallback_count > self.review_required_count:
            raise ValueError('generation fallback count cannot exceed review-required count')
        return self


class AiRunResourceReviewMetricRequest(StrictModel):
    idempotency_key: str = Field(min_length=16, max_length=160)
    expected_project_epoch: int = Field(ge=0)
    buckets: list[AiRunResourceReviewMetricBucket] = Field(min_length=1, max_length=4)

    @model_validator(mode='after')
    def validate_buckets(self) -> 'AiRunResourceReviewMetricRequest':
        resource_types = [bucket.resource_type for bucket in self.buckets]
        if len(set(resource_types)) != len(resource_types):
            raise ValueError('resource review metrics cannot repeat a resource type')
        return self


def _scope_hash(value: str) -> str:
    return sha256(value.encode('utf-8')).hexdigest()


def _owner_scope(x_ai_run_scope: Annotated[str | None, Header(alias='X-AI-Run-Scope')] = None) -> str:
    if x_ai_run_scope is None or not x_ai_run_scope.strip() or len(x_ai_run_scope) > 512:
        raise HTTPException(status_code=400, detail={'code': 'RUN_SCOPE_REQUIRED', 'message': '缺少本地会话作用域'})
    return _scope_hash(x_ai_run_scope.strip())


@lru_cache(maxsize=1)
def get_ai_run_store() -> RunStore:
    settings = get_settings()
    store = RunStore(
        settings.ai_runs_database_path,
        event_retention_hours=settings.ai_runs_event_retention_hours,
        run_retention_days=settings.ai_runs_retention_days,
    )
    store.initialize()
    return store


@lru_cache(maxsize=1)
def get_ai_run_manager() -> AiRunManager:
    return AiRunManager(get_ai_run_store(), get_settings())


def _provider_for_run(provider: AgnesTextProvider, tier: str) -> AgnesTextProvider:
    selected = provider if tier == 'normal' else provider.for_tier(tier)
    try:
        selected.ensure_configured()
    except (RuntimeError, ValueError) as exc:
        raise HTTPException(status_code=503, detail={'code': 'AI_PROVIDER_NOT_CONFIGURED', 'message': 'AI 服务暂未配置'}) from exc
    return selected


def _http_error(error: Exception) -> HTTPException:
    if isinstance(error, RunNotFoundError):
        return HTTPException(status_code=404, detail={'code': 'RUN_NOT_FOUND', 'message': '未找到该生成任务'})
    if isinstance(error, (IdempotencyConflictError, ToolResultConflictError)):
        return HTTPException(status_code=409, detail={'code': 'IDEMPOTENCY_CONFLICT', 'message': '幂等键或工具结果与首次请求不一致'})
    if isinstance(error, RunTransitionError):
        return HTTPException(status_code=409, detail={'code': 'RUN_STATE_CONFLICT', 'message': str(error)})
    raise error


@router.post('', status_code=202)
async def create_run(
    body: AiRunCreateRequest,
    owner_scope_hash: str = Depends(_owner_scope),
    provider: AgnesTextProvider = Depends(get_ai_blocks_provider),
    semaphore: asyncio.Semaphore = Depends(get_ai_blocks_semaphore),
    orchestrator: GenerationOrchestrator = Depends(get_ai_blocks_orchestrator),
    manager: AiRunManager = Depends(get_ai_run_manager),
) -> dict[str, Any]:
    _require_durable_runs_rollout(owner_scope_hash)
    selected = _provider_for_run(provider, body.generation.model_tier)
    return await manager.start(
        request=body.generation,
        owner_scope_hash=owner_scope_hash,
        provider=selected,
        semaphore=semaphore,
        orchestrator=orchestrator,
    )


@router.get('/metrics')
async def run_metrics(
    window_hours: int = Query(default=168, ge=1, le=24 * 30),
    owner_scope_hash: str = Depends(_owner_scope),
    store: RunStore = Depends(get_ai_run_store),
) -> dict[str, Any]:
    return store.metrics_for_owner(
        owner_scope_hash=owner_scope_hash,
        window_hours=window_hours,
        stuck_after_ms=int(get_settings().ai_blocks_provider_timeout_seconds * 1000),
    )


@router.get('/{run_id}')
async def get_run(
    run_id: str,
    owner_scope_hash: str = Depends(_owner_scope),
    store: RunStore = Depends(get_ai_run_store),
) -> dict[str, Any]:
    try:
        return store.get_run(run_id, owner_scope_hash).public_payload()
    except Exception as exc:
        raise _http_error(exc)


@router.post('/{run_id}/client-commit-metrics')
async def record_client_commit_metric(
    run_id: str,
    body: AiRunClientCommitMetricRequest,
    owner_scope_hash: str = Depends(_owner_scope),
    store: RunStore = Depends(get_ai_run_store),
) -> dict[str, Any]:
    try:
        response, _ = store.record_client_commit_metric(
            run_id=run_id,
            owner_scope_hash=owner_scope_hash,
            expected_project_epoch=body.expected_project_epoch,
            idempotency_key=body.idempotency_key,
            commit_duration_ms=body.commit_duration_ms,
            verify_duration_ms=body.verify_duration_ms,
            behavior_test_count=body.behavior_test_count,
            behavior_passed_count=body.behavior_passed_count,
            behavior_must_count=body.behavior_must_count,
            behavior_must_passed_count=body.behavior_must_passed_count,
            outcome=body.outcome,
            error_code=body.error_code,
        )
        return response
    except Exception as exc:
        raise _http_error(exc)


@router.post('/{run_id}/resource-review-metrics')
async def record_resource_review_metric(
    run_id: str,
    body: AiRunResourceReviewMetricRequest,
    owner_scope_hash: str = Depends(_owner_scope),
    store: RunStore = Depends(get_ai_run_store),
) -> dict[str, Any]:
    try:
        response, _ = store.record_resource_review_metric(
            run_id=run_id,
            owner_scope_hash=owner_scope_hash,
            expected_project_epoch=body.expected_project_epoch,
            idempotency_key=body.idempotency_key,
            buckets=[bucket.model_dump(mode='json') for bucket in body.buckets],
        )
        return response
    except Exception as exc:
        raise _http_error(exc)


@router.get('/{run_id}/events')
async def stream_run_events(
    run_id: str,
    last_event_id: Annotated[str | None, Header(alias='Last-Event-ID')] = None,
    owner_scope_hash: str = Depends(_owner_scope),
    manager: AiRunManager = Depends(get_ai_run_manager),
):
    try:
        sequence = 0
        if last_event_id is not None:
            sequence = manager.store.sequence_for_event(run_id=run_id, owner_scope_hash=owner_scope_hash, event_id=last_event_id)
            if sequence is None:
                raise HTTPException(status_code=410, detail={'code': 'EVENT_CURSOR_EXPIRED', 'message': '事件游标已过期，请先读取任务状态'})
        else:
            manager.store.get_run(run_id, owner_scope_hash)
    except HTTPException:
        raise
    except Exception as exc:
        raise _http_error(exc)

    async def generate() -> AsyncIterator[str]:
        async for event in sse_events(manager, run_id=run_id, owner_scope_hash=owner_scope_hash, sequence=sequence):
            if event is None:
                yield ': keepalive\n\n'
                continue
            yield f'id: {event.event_id}\nevent: {event.type}\ndata: {json.dumps(event.envelope(), ensure_ascii=False, separators=(",", ":"))}\n\n'

    return StreamingResponse(
        generate(),
        media_type='text/event-stream',
        headers={'Cache-Control': 'no-cache', 'X-Accel-Buffering': 'no'},
    )


@router.post('/{run_id}/decisions')
async def submit_decision(
    run_id: str,
    body: AiRunDecisionRequest,
    owner_scope_hash: str = Depends(_owner_scope),
    manager: AiRunManager = Depends(get_ai_run_manager),
) -> dict[str, Any]:
    payload = body.model_dump(mode='json', exclude={'idempotency_key'})
    try:
        return await manager.submit_decision(
            run_id=run_id,
            owner_scope_hash=owner_scope_hash,
            idempotency_key=body.idempotency_key,
            expected_project_epoch=body.expected_project_epoch,
            payload=payload,
        )
    except Exception as exc:
        raise _http_error(exc)


@router.post('/{run_id}/tool-results')
async def submit_tool_result(
    run_id: str,
    body: AiRunToolResultRequest,
    owner_scope_hash: str = Depends(_owner_scope),
    provider: AgnesTextProvider = Depends(get_ai_blocks_provider),
    semaphore: asyncio.Semaphore = Depends(get_ai_blocks_semaphore),
    orchestrator: GenerationOrchestrator = Depends(get_ai_blocks_orchestrator),
    manager: AiRunManager = Depends(get_ai_run_manager),
) -> dict[str, Any]:
    payload = body.model_dump(mode='json', exclude={'idempotency_key'})
    try:
        tier = manager.store.get_run(run_id, owner_scope_hash).artifact_summary.get('model_tier')
        selected = _provider_for_run(provider, tier if isinstance(tier, str) else 'normal')
        response = await manager.submit_tool_result(
            run_id=run_id,
            owner_scope_hash=owner_scope_hash,
            tool_call_id=body.tool_call_id,
            idempotency_key=body.idempotency_key,
            expected_project_epoch=body.expected_project_epoch,
            payload=payload,
            provider=selected,
            semaphore=semaphore,
            orchestrator=orchestrator,
        )
        return response
    except Exception as exc:
        raise _http_error(exc)


@router.post('/{run_id}/program-contexts')
async def submit_program_contexts(
    run_id: str,
    body: AiRunProgramContextRequest,
    owner_scope_hash: str = Depends(_owner_scope),
    provider: AgnesTextProvider = Depends(get_ai_blocks_provider),
    semaphore: asyncio.Semaphore = Depends(get_ai_blocks_semaphore),
    orchestrator: GenerationOrchestrator = Depends(get_ai_blocks_orchestrator),
    manager: AiRunManager = Depends(get_ai_run_manager),
) -> dict[str, Any]:
    try:
        tier = manager.store.get_run(run_id, owner_scope_hash).artifact_summary.get('model_tier')
        selected = _provider_for_run(provider, tier if isinstance(tier, str) else 'normal')
        return await manager.submit_program_contexts(
            run_id=run_id,
            owner_scope_hash=owner_scope_hash,
            expected_project_epoch=body.expected_project_epoch,
            target_index=body.target_index,
            target=body.target,
            provider=selected,
            semaphore=semaphore,
            orchestrator=orchestrator,
        )
    except Exception as exc:
        raise _http_error(exc)


@router.post('/{run_id}/cancel')
async def cancel_run(
    run_id: str,
    body: AiRunCancelRequest,
    owner_scope_hash: str = Depends(_owner_scope),
    manager: AiRunManager = Depends(get_ai_run_manager),
) -> dict[str, Any]:
    try:
        response, _ = await manager.cancel(
            run_id=run_id,
            owner_scope_hash=owner_scope_hash,
            expected_project_epoch=body.expected_project_epoch,
            idempotency_key=body.idempotency_key,
        )
        return response
    except Exception as exc:
        raise _http_error(exc)
