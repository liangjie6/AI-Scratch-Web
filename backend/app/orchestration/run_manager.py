from __future__ import annotations

import asyncio
from collections.abc import AsyncIterator
import logging
from typing import Any

from ..config import Settings
from ..providers.agnes_text_provider import AgnesTextProvider, AgnesTextProviderError
from ..schemas.ai_blocks import (
    Plan,
    ProjectBlueprint,
    ProjectManifest,
    ProjectManifestGenerationRequest,
    ProjectProgramGenerationRequest,
    RequirementsResponse,
    ScratchTaskSpec,
    TargetContext,
    UnifiedGenerationRequest,
    validate_request_extension_permissions,
)
from .orchestrator import GenerationEvent, GenerationOrchestrator
from .run_state_machine import RunStatus, RunTransitionError, is_terminal
from .run_store import RunEvent, RunNotFoundError, RunStore


logger = logging.getLogger(__name__)


def _program_generation_layers(blueprint: ProjectBlueprint, manifest: ProjectManifest) -> list[list[int]]:
    """Build stable, cross-job dependency layers from the verified Blueprint."""
    behavior_to_target: dict[str, int] = {}
    for index, target in enumerate(manifest.program_targets):
        for behavior_id in target.behavior_ids:
            if behavior_id in behavior_to_target:
                raise RunTransitionError(f'behavior is assigned to multiple program targets: {behavior_id}')
            behavior_to_target[behavior_id] = index

    dependencies = [set() for _ in manifest.program_targets]
    for behavior in blueprint.behavior_graph:
        target_index = behavior_to_target.get(behavior.behavior_id)
        if target_index is None:
            raise RunTransitionError(f'Blueprint behavior has no program target: {behavior.behavior_id}')
        for dependency_id in behavior.depends_on:
            dependency_target = behavior_to_target.get(dependency_id)
            if dependency_target is None:
                raise RunTransitionError(f'Blueprint dependency has no program target: {dependency_id}')
            if dependency_target != target_index:
                dependencies[target_index].add(dependency_target)

    remaining = [set(item) for item in dependencies]
    completed: set[int] = set()
    layers: list[list[int]] = []
    while len(completed) < len(manifest.program_targets):
        layer = [index for index, item in enumerate(remaining) if not item and index not in completed]
        if not layer:
            logger.warning('Blueprint program dependencies contain a cycle; falling back to self-healing layers')
            min_deps = float('inf')
            fallback_candidate = None
            for idx in range(len(manifest.program_targets)):
                if idx not in completed and len(remaining[idx]) < min_deps:
                    min_deps = len(remaining[idx])
                    fallback_candidate = idx
            if fallback_candidate is not None:
                layer = [fallback_candidate]
            else:
                layer = [idx for idx in range(len(manifest.program_targets)) if idx not in completed]
        layers.append(layer)
        completed.update(layer)
        for item in remaining:
            item.difference_update(layer)
    return layers


class AiRunManager:
    """Owns live provider tasks while the RunStore remains the recovery source of truth."""

    def __init__(self, store: RunStore, settings: Settings) -> None:
        self.store = store
        self.settings = settings
        self._tasks: dict[str, asyncio.Task[None]] = {}
        self._wakeups: dict[str, asyncio.Event] = {}
        self._requests: dict[str, UnifiedGenerationRequest] = {}
        # Full target programs are transient provider input. They are never
        # copied into RunStore events, summaries, or idempotency records.
        self._program_contexts: dict[str, dict[int, TargetContext]] = {}
        self._guard = asyncio.Lock()

    async def start(
        self,
        *,
        request: UnifiedGenerationRequest,
        owner_scope_hash: str,
        provider: AgnesTextProvider,
        semaphore: asyncio.Semaphore,
        orchestrator: GenerationOrchestrator,
    ) -> dict[str, Any]:
        if request.scope != 'project' or request.project_context is None:
            raise ValueError('durable runs currently require project scope and project_context')
        record = self.store.create_run(
            owner_scope_hash=owner_scope_hash,
            project_epoch=request.project_context.version,
            artifact_summary={
                'scope': request.scope,
                'output_contract': 'project_manifest',
                'project_id': request.project_context.project_id,
                'model_tier': request.model_tier,
            },
        )
        await self._signal(record.run_id)
        async with self._guard:
            self._requests[record.run_id] = request
            task = asyncio.create_task(
                self._execute_manifest(record.run_id, owner_scope_hash, request, provider, semaphore, orchestrator),
                name=f'ai-run:{record.run_id}',
            )
            self._tasks[record.run_id] = task
        return record.public_payload()

    async def cancel(
        self,
        *,
        run_id: str,
        owner_scope_hash: str,
        expected_project_epoch: int,
        idempotency_key: str,
    ) -> tuple[dict[str, Any], bool]:
        record, replayed = self.store.cancel_idempotent(
            run_id=run_id,
            owner_scope_hash=owner_scope_hash,
            expected_project_epoch=expected_project_epoch,
            idempotency_key=idempotency_key,
        )
        async with self._guard:
            task = self._tasks.get(run_id)
        if task is not None and not task.done():
            task.cancel()
        async with self._guard:
            self._requests.pop(run_id, None)
            self._program_contexts.pop(run_id, None)
        await self._signal(run_id)
        return record.public_payload(), replayed

    async def wait_for_events(self, run_id: str, timeout_seconds: float = 15) -> None:
        async with self._guard:
            wakeup = self._wakeups.setdefault(run_id, asyncio.Event())
        try:
            await asyncio.wait_for(wakeup.wait(), timeout=timeout_seconds)
        except TimeoutError:
            return
        finally:
            wakeup.clear()

    async def shutdown(self) -> None:
        async with self._guard:
            tasks = tuple(self._tasks.values())
            self._tasks.clear()
            self._requests.clear()
            self._program_contexts.clear()
        for task in tasks:
            task.cancel()
        if tasks:
            await asyncio.gather(*tasks, return_exceptions=True)

    async def submit_tool_result(
        self,
        *,
        run_id: str,
        owner_scope_hash: str,
        tool_call_id: str,
        idempotency_key: str,
        expected_project_epoch: int,
        payload: dict[str, Any],
        provider: AgnesTextProvider,
        semaphore: asyncio.Semaphore,
        orchestrator: GenerationOrchestrator,
    ) -> dict[str, Any]:
        async with self._guard:
            record = self.store.get_run(run_id, owner_scope_hash)
            summary = record.artifact_summary
            need_id = payload.get('need_id')
            expected_calls = {f'resolve_{item}' for item in summary.get('asset_need_ids', [])}
            expected_ops = summary.get('asset_need_ops', {})
            if not isinstance(need_id, str) or tool_call_id != f'resolve_{need_id}' or tool_call_id not in expected_calls:
                raise RunTransitionError('tool_call_id must match a declared Blueprint asset need')
            allowed_ops = expected_ops.get(need_id, []) if isinstance(expected_ops, dict) else []
            if payload.get('op_id') not in allowed_ops:
                raise RunTransitionError('tool result op_id is not declared for this asset need')
            response, replayed = self.store.accept_interaction(
                run_id=run_id,
                owner_scope_hash=owner_scope_hash,
                operation='tool-results',
                idempotency_key=idempotency_key,
                expected_project_epoch=expected_project_epoch,
                required_status=RunStatus.AWAITING_TOOL_RESULT,
                phase='awaiting_tool_result',
                event_type='tool_result_received',
                payload=payload,
                tool_call_id=tool_call_id,
            )
            if replayed:
                return response
            self._start_programs_if_ready(run_id, owner_scope_hash, provider, semaphore, orchestrator)
        await self._signal(run_id)
        return response

    async def submit_program_contexts(
        self,
        *,
        run_id: str,
        owner_scope_hash: str,
        expected_project_epoch: int,
        target_index: int,
        target: TargetContext,
        provider: AgnesTextProvider,
        semaphore: asyncio.Semaphore,
        orchestrator: GenerationOrchestrator,
    ) -> dict[str, Any]:
        async with self._guard:
            record = self.store.get_run(run_id, owner_scope_hash)
            if record.project_epoch != expected_project_epoch:
                raise RunTransitionError('program contexts use a stale project epoch')
            existing = self._program_contexts.get(run_id, {})
            prior = existing.get(target_index)
            if record.status is not RunStatus.AWAITING_TOOL_RESULT:
                if prior == target:
                    return record.public_payload()
                raise RunTransitionError('program contexts require run status awaiting_tool_result')
            target_count = record.artifact_summary.get('manifest_summary', {}).get('program_target_count', -1)
            if not isinstance(target_count, int) or target_index not in range(target_count):
                raise RunTransitionError('program context target_index is outside manifest targets')
            contexts = self._program_contexts.setdefault(run_id, {})
            prior = contexts.get(target_index)
            if prior is not None and prior != target:
                raise RunTransitionError('program contexts were already submitted with different content')
            contexts[target_index] = target
            self._start_programs_if_ready(run_id, owner_scope_hash, provider, semaphore, orchestrator)
            return self.store.get_run(run_id, owner_scope_hash).public_payload()

    def _start_programs_if_ready(
        self,
        run_id: str,
        owner_scope_hash: str,
        provider: AgnesTextProvider,
        semaphore: asyncio.Semaphore,
        orchestrator: GenerationOrchestrator,
    ) -> None:
        record = self.store.get_run(run_id, owner_scope_hash)
        if record.status is not RunStatus.AWAITING_TOOL_RESULT:
            return
        summary = record.artifact_summary
        needs = summary.get('asset_need_ids', [])
        expected_calls = {f'resolve_{item}' for item in needs if isinstance(item, str)}
        accepted = self.store.accepted_tool_call_ids(run_id=run_id, owner_scope_hash=owner_scope_hash)
        target_count = summary.get('manifest_summary', {}).get('program_target_count', -1)
        contexts = self._program_contexts.get(run_id, {})
        if not isinstance(target_count, int) or not expected_calls.issubset(accepted) or set(contexts) != set(range(target_count)):
            return
        request = self._requests.get(run_id)
        if request is None:
            raise RunTransitionError('the server no longer has the active request for this run')
        self.store.transition(
            run_id=run_id,
            owner_scope_hash=owner_scope_hash,
            target=RunStatus.GENERATING_PROGRAMS,
            phase='generating_programs',
            event_type='phase_started',
            payload={},
        )
        self._tasks[run_id] = asyncio.create_task(
            self._execute_programs(run_id, owner_scope_hash, request, provider, semaphore, orchestrator),
            name=f'ai-run-programs:{run_id}',
        )

    async def submit_decision(
        self,
        *,
        run_id: str,
        owner_scope_hash: str,
        idempotency_key: str,
        expected_project_epoch: int,
        payload: dict[str, Any],
    ) -> dict[str, Any]:
        async with self._guard:
            summary = self.store.get_run(run_id, owner_scope_hash).artifact_summary
            need_id = payload.get('need_id')
            declared_needs = set(summary.get('blueprint_asset_need_ids', []))
            if need_id is not None and need_id not in declared_needs:
                raise RunTransitionError('decision need_id is not declared by the Blueprint')
            response, _ = self.store.accept_interaction(
                run_id=run_id,
                owner_scope_hash=owner_scope_hash,
                operation='decisions',
                idempotency_key=idempotency_key,
                expected_project_epoch=expected_project_epoch,
                required_status=RunStatus.AWAITING_TOOL_RESULT,
                phase='awaiting_tool_result',
                event_type='decision_received',
                payload=payload,
            )
        await self._signal(run_id)
        return response

    async def _execute_manifest(
        self,
        run_id: str,
        owner_scope_hash: str,
        request: UnifiedGenerationRequest,
        provider: AgnesTextProvider,
        semaphore: asyncio.Semaphore,
        orchestrator: GenerationOrchestrator,
    ) -> None:
        job_ids: dict[int, str] = {}
        try:
            manifest_request = ProjectManifestGenerationRequest.model_validate({
                **request.model_dump(mode='json'),
                'output_contract': 'project_manifest',
            })
            self._transition(run_id, owner_scope_hash, RunStatus.SPECIFYING_TASK, 'specifying_task', 'phase_started', {})
            async with semaphore:
                async with asyncio.timeout(self.settings.ai_blocks_provider_timeout_seconds):
                    async for event in orchestrator.generate_unified(manifest_request, provider):
                        job_ids = await self._ingest_generation_event(
                            run_id, owner_scope_hash, event, job_ids, provider, semaphore, orchestrator, terminal_on_complete=True,
                        )
        except asyncio.CancelledError:
            # cancel() has already written the terminal event. A shutdown must not write a second terminal state.
            raise
        except asyncio.TimeoutError:
            self._fail_if_active(run_id, owner_scope_hash, 'AI_PROVIDER_TIMEOUT', 'AI 生成超时，请稍后重试')
        except AgnesTextProviderError:
            self._fail_if_active(run_id, owner_scope_hash, 'AI_PROVIDER_ERROR', 'AI 生成暂时不可用，请稍后重试')
        except Exception as exc:
            logger.error('ai_run_failed run_id=%s error_type=%s', run_id, type(exc).__name__)
            self._fail_if_active(run_id, owner_scope_hash, 'AI_GENERATION_ERROR', 'AI 输出未完成，请重新生成')
        finally:
            async with self._guard:
                if self._tasks.get(run_id) is asyncio.current_task():
                    self._tasks.pop(run_id, None)
                record = self.store.get_run(run_id, owner_scope_hash)
                if is_terminal(record.status):
                    self._requests.pop(run_id, None)
            await self._signal(run_id)

    async def _execute_programs(
        self,
        run_id: str,
        owner_scope_hash: str,
        request: UnifiedGenerationRequest,
        provider: AgnesTextProvider,
        semaphore: asyncio.Semaphore,
        orchestrator: GenerationOrchestrator,
    ) -> None:
        try:
            summary = self.store.get_run(run_id, owner_scope_hash).artifact_summary
            artifacts = summary.get('contracts')
            if not isinstance(artifacts, dict):
                raise RunTransitionError('run is missing the validated generation contracts')
            requirements = RequirementsResponse.model_validate(artifacts['requirements'])
            task_spec = ScratchTaskSpec.model_validate(artifacts['task_spec'])
            blueprint = ProjectBlueprint.model_validate(artifacts['blueprint'])
            plan = Plan.model_validate(artifacts['plan'])
            manifest = ProjectManifest.model_validate(artifacts['manifest'])
            validate_request_extension_permissions(request, manifest)
            async def generate_program(index: int) -> None:
                program_target = manifest.program_targets[index]
                target_context = self._program_contexts.get(run_id, {}).get(index)
                if target_context is None:
                    raise RunTransitionError('run is missing transient program context')
                program_request = ProjectProgramGenerationRequest.model_validate({
                    **request.model_dump(mode='json'),
                    'output_contract': 'project_program',
                    'requirements': requirements.model_dump(mode='json'),
                    'task_spec': task_spec.model_dump(mode='json'),
                    'blueprint': blueprint.model_dump(mode='json'),
                    'plan': plan.model_dump(mode='json'),
                    'manifest': manifest.model_dump(mode='json'),
                    'program_target': program_target.model_dump(mode='json'),
                    'target_index': index,
                    'target': target_context.model_dump(mode='json'),
                })
                async with semaphore:
                    async with asyncio.timeout(self.settings.ai_blocks_provider_timeout_seconds):
                        async for event in orchestrator.generate_project_program(program_request, provider):
                            await self._ingest_generation_event(
                                run_id,
                                owner_scope_hash,
                                event,
                                {index: program_target.job_id},
                                provider,
                                semaphore,
                                orchestrator,
                                terminal_on_complete=False,
                            )

            for layer in _program_generation_layers(blueprint, manifest):
                # Keep provider concurrency bounded while preserving dependency order.
                for offset in range(0, len(layer), 2):
                    tasks = [asyncio.create_task(generate_program(index)) for index in layer[offset:offset + 2]]
                    try:
                        await asyncio.gather(*tasks)
                    except BaseException:
                        for task in tasks:
                            if not task.done():
                                task.cancel()
                        await asyncio.gather(*tasks, return_exceptions=True)
                        raise
            self._transition(
                run_id,
                owner_scope_hash,
                RunStatus.READY_TO_COMMIT,
                'ready_to_commit',
                'generation_complete',
                {'scope': 'project', 'program_count': len(manifest.program_targets)},
            )
        except asyncio.CancelledError:
            raise
        except asyncio.TimeoutError:
            self._fail_if_active(run_id, owner_scope_hash, 'AI_PROVIDER_TIMEOUT', 'AI 生成超时，请稍后重试')
        except AgnesTextProviderError:
            self._fail_if_active(run_id, owner_scope_hash, 'AI_PROVIDER_ERROR', 'AI 生成暂时不可用，请稍后重试')
        except Exception as exc:
            logger.error('ai_run_programs_failed run_id=%s error_type=%s', run_id, type(exc).__name__)
            self._fail_if_active(run_id, owner_scope_hash, 'AI_GENERATION_ERROR', 'AI 输出未完成，请重新生成')
        finally:
            async with self._guard:
                if self._tasks.get(run_id) is asyncio.current_task():
                    self._tasks.pop(run_id, None)
                self._requests.pop(run_id, None)
                self._program_contexts.pop(run_id, None)
            await self._signal(run_id)

    async def _ingest_generation_event(
        self,
        run_id: str,
        owner_scope_hash: str,
        event: GenerationEvent,
        job_ids: dict[int, str],
        provider: AgnesTextProvider,
        semaphore: asyncio.Semaphore,
        orchestrator: GenerationOrchestrator,
        *,
        terminal_on_complete: bool,
    ) -> dict[int, str]:
        data = dict(event.data)
        if event.type in {'content_delta', 'reasoning_delta'}:
            # Provider text is deliberately excluded from durable storage and the public event stream.
            return job_ids
        if event.type == 'phase_start':
            if event.phase == 'blueprint':
                self._transition(run_id, owner_scope_hash, RunStatus.BLUEPRINTING, 'blueprinting', 'phase_started', {})
                return job_ids
            if event.phase == 'plan':
                self._transition(run_id, owner_scope_hash, RunStatus.PLANNING_MANIFEST, 'planning_manifest', 'phase_started', {})
                return job_ids
            if event.phase == 'program':
                self._transition(run_id, owner_scope_hash, RunStatus.GENERATING_PROGRAMS, 'generating_programs', 'phase_started', {})
                index = data.get('target_index')
                if isinstance(index, int) and index in job_ids:
                    # Preserve the same target coordinates used by the live
                    # stream; the job id is an internal durable correlation key.
                    self._append(run_id, owner_scope_hash, 'generating_programs', 'program_target_started', {'job_id': job_ids[index], **data})
                return job_ids
            return job_ids
        if event.type == 'phase_complete':
            if event.phase == 'requirements':
                self.store.merge_artifact_summary(
                    run_id=run_id,
                    owner_scope_hash=owner_scope_hash,
                    values={'contracts': {'requirements': data}},
                )
                self._append(run_id, owner_scope_hash, 'specifying_task', 'requirements_ready', data)
            elif event.phase == 'task_spec':
                self._merge_contract(run_id, owner_scope_hash, 'task_spec', data.get('task_spec'))
                self._append(run_id, owner_scope_hash, 'specifying_task', 'task_spec_ready', data)
            elif event.phase == 'blueprint':
                blueprint = data.get('blueprint')
                self._merge_contract(run_id, owner_scope_hash, 'blueprint', blueprint)
                if isinstance(blueprint, dict):
                    asset_need_ids = [item.get('need_id') for item in blueprint.get('asset_needs', []) if isinstance(item, dict) and isinstance(item.get('need_id'), str)]
                    self.store.merge_artifact_summary(
                        run_id=run_id,
                        owner_scope_hash=owner_scope_hash,
                        values={'blueprint_asset_need_ids': asset_need_ids},
                    )
                self._append(run_id, owner_scope_hash, 'blueprinting', 'blueprint_ready', data)
            elif event.phase == 'plan':
                manifest = data.get('manifest')
                if isinstance(manifest, dict):
                    targets = manifest.get('program_targets')
                    if isinstance(targets, list):
                        job_ids = {
                            index: target['job_id']
                            for index, target in enumerate(targets)
                            if isinstance(target, dict) and isinstance(target.get('job_id'), str)
                        }
                    need_ops: dict[str, list[str]] = {}
                    for operation in manifest.get('resource_ops', []):
                        if isinstance(operation, dict) and isinstance(operation.get('source_need_id'), str) and isinstance(operation.get('op_id'), str):
                            need_ops.setdefault(operation['source_need_id'], []).append(operation['op_id'])
                    self._merge_contract(run_id, owner_scope_hash, 'plan', {'steps': data.get('steps')})
                    self._merge_contract(run_id, owner_scope_hash, 'manifest', manifest)
                    self.store.merge_artifact_summary(
                        run_id=run_id,
                        owner_scope_hash=owner_scope_hash,
                        values={
                            'asset_need_ids': sorted(need_ops),
                            'asset_need_ops': need_ops,
                            'manifest_summary': {'program_target_count': len(job_ids), 'resource_op_count': len(manifest.get('resource_ops', []))},
                        },
                    )
                self._append(run_id, owner_scope_hash, 'planning_manifest', 'manifest_ready', data)
            elif event.phase == 'program':
                index = data.get('target_index')
                if isinstance(index, int) and index in job_ids:
                    self._append(run_id, owner_scope_hash, 'generating_programs', 'program_target_ready', {'job_id': job_ids[index], **data})
                else:
                    self._fail_if_active(run_id, owner_scope_hash, 'RUN_PROTOCOL_ERROR', '程序事件缺少已声明的 job_id')
            else:
                self._append(run_id, owner_scope_hash, event.phase or 'specifying_task', 'phase_completed', data)
            await self._signal(run_id)
            return job_ids
        if event.type == 'script_ready':
            index = data.get('target_index')
            if isinstance(index, int) and index in job_ids:
                self._append(run_id, owner_scope_hash, 'generating_programs', 'program_script_ready', {'job_id': job_ids[index], **data})
            else:
                self._fail_if_active(run_id, owner_scope_hash, 'RUN_PROTOCOL_ERROR', '脚本事件缺少已声明的 job_id')
        elif event.type == 'repair':
            self._append(run_id, owner_scope_hash, event.phase or 'generating_programs', 'repair_started', data, attempt=int(data.get('attempt', 1)))
        elif event.type == 'validation_error':
            self._append(run_id, owner_scope_hash, event.phase or 'generating_programs', 'validation_failed', data)
        elif event.type == 'error':
            self._fail_if_active(
                run_id,
                owner_scope_hash,
                str(data.get('code', 'AI_GENERATION_ERROR')),
                str(data.get('message', 'AI 输出未完成，请重新生成')),
            )
            # A program target validation error is terminal for the run. Raise
            # into the gather supervising the dependency layer so peer target
            # requests are cancelled instead of continuing after failure.
            if not terminal_on_complete:
                raise RunTransitionError('项目目标程序生成失败')
        elif event.type == 'complete':
            if not terminal_on_complete:
                await self._signal(run_id)
                return job_ids
            if data.get('output_contract') == 'project_manifest':
                summary = self.store.get_run(run_id, owner_scope_hash).artifact_summary
                need_ids = summary.get('asset_need_ids', [])
                target_count = summary.get('manifest_summary', {}).get('program_target_count', -1)
                if target_count == 0:
                    self._transition(
                        run_id,
                        owner_scope_hash,
                        RunStatus.READY_TO_COMMIT,
                        'ready_to_commit',
                        'generation_complete',
                        data,
                    )
                    await self._signal(run_id)
                    return job_ids
                self._transition(
                    run_id,
                    owner_scope_hash,
                    RunStatus.AWAITING_TOOL_RESULT,
                    'awaiting_tool_result',
                    'asset_resolution_required',
                    {'program_context_required': True, 'tool_calls': [
                        {'tool_call_id': f'resolve_{need_id}', 'need_id': need_id}
                        for need_id in need_ids if isinstance(need_id, str)
                    ]},
                )
            else:
                self._transition(
                    run_id,
                    owner_scope_hash,
                    RunStatus.READY_TO_COMMIT,
                    'ready_to_commit',
                    'generation_complete',
                    data,
                )
        elif event.type in {'metadata', 'shared_state', 'cache_hit', 'usage'}:
            self._append(run_id, owner_scope_hash, event.phase or 'generating_programs', event.type, data)
        await self._signal(run_id)
        return job_ids

    def _transition(
        self,
        run_id: str,
        owner_scope_hash: str,
        target: RunStatus,
        phase: str,
        event_type: str,
        payload: dict[str, Any],
    ) -> None:
        try:
            self.store.transition(
                run_id=run_id,
                owner_scope_hash=owner_scope_hash,
                target=target,
                phase=phase,
                event_type=event_type,
                payload=payload,
            )
        except RunTransitionError:
            record = self.store.get_run(run_id, owner_scope_hash)
            if not is_terminal(record.status):
                raise

    def _append(
        self,
        run_id: str,
        owner_scope_hash: str,
        phase: str,
        event_type: str,
        payload: dict[str, Any],
        *,
        attempt: int = 1,
    ) -> RunEvent:
        return self.store.append_event(
            run_id=run_id,
            owner_scope_hash=owner_scope_hash,
            phase=phase,
            event_type=event_type,
            payload=payload,
            attempt=attempt,
        )

    def _fail_if_active(self, run_id: str, owner_scope_hash: str, code: str, message: str) -> None:
        try:
            self._transition(
                run_id,
                owner_scope_hash,
                RunStatus.FAILED,
                'failed',
                'generation_error',
                {'code': code, 'message': message},
            )
        except RunNotFoundError:
            logger.warning('ai_run_missing_while_failing run_id=%s', run_id)

    def _merge_contract(self, run_id: str, owner_scope_hash: str, name: str, value: Any) -> None:
        if value is None:
            raise RunTransitionError(f'{name} contract is missing from a completed generation phase')
        summary = self.store.get_run(run_id, owner_scope_hash).artifact_summary
        existing = summary.get('contracts')
        contracts = dict(existing) if isinstance(existing, dict) else {}
        contracts[name] = value
        self.store.merge_artifact_summary(run_id=run_id, owner_scope_hash=owner_scope_hash, values={'contracts': contracts})

    async def _signal(self, run_id: str) -> None:
        async with self._guard:
            self._wakeups.setdefault(run_id, asyncio.Event()).set()


async def sse_events(manager: AiRunManager, *, run_id: str, owner_scope_hash: str, sequence: int) -> AsyncIterator[RunEvent | None]:
    """Yield persisted events first; None is a heartbeat for long idle model calls."""
    cursor = sequence
    while True:
        events = manager.store.events_after(run_id=run_id, owner_scope_hash=owner_scope_hash, sequence=cursor)
        for event in events:
            cursor = event.sequence
            yield event
        record = manager.store.get_run(run_id, owner_scope_hash)
        if is_terminal(record.status):
            return
        await manager.wait_for_events(run_id)
        yield None
