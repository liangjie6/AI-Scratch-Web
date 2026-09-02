from __future__ import annotations

from collections.abc import AsyncIterator, Callable
from dataclasses import dataclass, field
import asyncio
import json
import logging
from typing import Any, Literal

from openai.lib._pydantic import to_strict_json_schema
from pydantic import TypeAdapter, ValidationError

from ..prompt_builder import PromptBuilder, ProjectContext, TargetContext
from ..resource_contract import bounded_actual
from ..providers.agnes_text_provider import AgnesTextProvider
from ..schemas.ai_blocks import (
    Plan,
    ProgramIR,
    ProgramMetadata,
    ProgramState,
    ProgramContractError,
    ProjectManifest,
    RequirementsResponse,
    ResourceContractError,
    ResourceOp,
    Script,
    UnifiedGenerationRequest,
    normalize_project_manifest_payload,
    to_json_pointer,
    validate_program_contracts,
    validate_program_for_mode,
    validate_project_program_shared_state,
)
from .error_messages import repair_hint
from .streaming_parser import StreamFrame, StreamProtocolError, UnifiedFrameParser, parse_json


logger = logging.getLogger(__name__)


def _to_chatgpt_strict_json_schema(model: Any) -> dict[str, Any]:
    """Adapt Pydantic's strict schema to the ChatGPT Responses dialect."""
    schema = to_strict_json_schema(model)

    def normalize(value: Any) -> None:
        if isinstance(value, dict):
            if 'oneOf' in value:
                alternatives = value.pop('oneOf')
                existing = value.get('anyOf')
                value['anyOf'] = [*(existing if isinstance(existing, list) else []), *alternatives]
            value.pop('discriminator', None)
            for child in tuple(value.values()):
                normalize(child)
        elif isinstance(value, list):
            for child in value:
                normalize(child)

    normalize(schema)
    return schema


@dataclass(frozen=True)
class GenerationEvent:
    type: str
    request_id: str
    phase: str | None = None
    data: dict[str, Any] = field(default_factory=dict)

    def payload(self) -> dict[str, Any]:
        payload: dict[str, Any] = {'request_id': self.request_id}
        if self.phase is not None:
            payload['phase'] = self.phase
        if self.data:
            payload['data'] = self.data
        return payload


@dataclass
class _ProgramBuffer:
    target_index: int
    attrs: dict[str, str]
    metadata: ProgramMetadata | None = None
    shared_state: ProgramState | None = None
    scripts: list[Script] = field(default_factory=list)
    total: int | None = None


@dataclass
class _GenerationState:
    requirements: RequirementsResponse | None = None
    plan: Plan | None = None
    manifest: ProjectManifest | None = None
    programs: dict[int, ProgramIR] = field(default_factory=dict)
    active_program: _ProgramBuffer | None = None
    phase_started: set[str] = field(default_factory=set)
    phase_completed: set[str] = field(default_factory=set)
    expected_program_indices: set[int] | None = None


@dataclass
class _SegmentAttempt:
    value: Any = None
    events: list[GenerationEvent] = field(default_factory=list)
    failure: GenerationEvent | None = None
    payload: Any = None


class _SegmentValidationFailure(Exception):
    def __init__(self, event: GenerationEvent) -> None:
        self.event = event
        super().__init__(event.data.get('message', 'segment validation failed'))


class GenerationOrchestrator:
    """Run segmented project generation and the single-target tagged flow."""

    def __init__(self, prompt_builder: PromptBuilder | None = None, max_repair_rounds: int = 2, max_concurrency: int = 4) -> None:
        self.prompt_builder = prompt_builder or PromptBuilder()
        self.max_repair_rounds = max(0, max_repair_rounds)
        self.max_concurrency = max(1, max_concurrency)

    def _repair_budget(self, scope: str) -> int:
        # Keep broad project stages cheap while allowing local repairs two attempts.
        scope_limit = 1 if scope in {'requirements', 'plan', 'manifest'} else 2
        return min(self.max_repair_rounds, scope_limit)

    async def generate_unified(
        self,
        request: UnifiedGenerationRequest,
        provider: AgnesTextProvider,
    ) -> AsyncIterator[GenerationEvent]:
        if request.scope == 'project':
            async for event in self._generate_segmented_project(request, provider):
                yield event
            return
        async for event in self._generate_tagged(request, provider):
            yield event

    async def _generate_tagged(
        self,
        request: UnifiedGenerationRequest,
        provider: AgnesTextProvider,
    ) -> AsyncIterator[GenerationEvent]:
        project = self._project_context(request)
        target = self._target_context(request)
        messages, cache = self.prompt_builder.build_unified_prompt(project, target, request)
        yield GenerationEvent('cache_hit', request.request_id, data=cache.as_dict())
        state = _GenerationState()
        parser = UnifiedFrameParser()
        content_chunk_count = 0
        raw_output = ''
        repair_failure: GenerationEvent | None = None
        repair_raw = ''
        resume_kind = 'requirements'
        rounds = 1 + self.max_repair_rounds

        for attempt in range(rounds):
            if attempt > 0:
                assert repair_failure is not None
                yield GenerationEvent('repair', request.request_id, data={
                    'attempt': attempt,
                    'max_rounds': self.max_repair_rounds,
                    'phase': repair_failure.phase,
                    'path': repair_failure.data.get('path'),
                    'message': repair_failure.data.get('message'),
                    'detail': repair_failure.data.get('detail'),
                    'resume': resume_kind,
                })
                state = self._resume_state(state, resume_kind)
                parser = UnifiedFrameParser()
                raw_output = ''
                attempt_messages = self._repair_messages(messages, repair_raw, repair_failure, resume_kind)
            else:
                attempt_messages = messages
            failed = False
            try:
                stream = provider.stream_with_reasoning(
                    attempt_messages,
                    thinking_enabled=request.thinking.enabled,
                    thinking_strength=request.thinking.strength,
                    json_output=False,
                )
                async for delta in stream:
                    if delta.kind == 'reasoning' and delta.text:
                        yield GenerationEvent('reasoning_delta', request.request_id, data={'text': delta.text})
                        continue
                    if delta.kind != 'content' or not delta.text:
                        continue
                    content_chunk_count += 1
                    raw_output += delta.text
                    logger.debug('raw_delta: %r', delta.text)
                    try:
                        frames = parser.feed(delta.text)
                    except StreamProtocolError as exc:
                        yield self._failure(request.request_id, 'OUTPUT_PROTOCOL_ERROR')
                        return
                    for frame in frames:
                        events, failure = self._consume_frame(request, state, frame, allow_full_restart=attempt > 0)
                        for event in events:
                            yield event
                        if failure is not None:
                            repair_failure = failure
                            repair_raw = raw_output
                            resume_kind = self._resume_tag(frame.kind)
                            failed = True
                            break
                    if failed:
                        break
                    if state.phase_started:
                        yield GenerationEvent('content_delta', request.request_id, data={'text': delta.text})
                if not failed:
                    try:
                        trailing = parser.finish()
                    except StreamProtocolError as exc:
                        yield self._failure(request.request_id, 'OUTPUT_INCOMPLETE')
                        return
                    for frame in trailing:
                        events, failure = self._consume_frame(request, state, frame, allow_full_restart=attempt > 0)
                        for event in events:
                            yield event
                        if failure is not None:
                            repair_failure = failure
                            repair_raw = raw_output
                            resume_kind = self._resume_tag(frame.kind)
                            failed = True
                            break
            except Exception:
                raise
            if failed:
                logger.warning(
                    '统一生成 校验失败 触发修复: attempt=%d resume=%s phase=%s detail=%s',
                    attempt, resume_kind, repair_failure.phase, repair_failure.data.get('detail'),
                )
                continue
            break

        if not failed:
            failure = self._finish(request, state)
            if failure is not None:
                yield failure
                if failure.type == 'validation_error':
                    yield GenerationEvent('error', request.request_id, data={'code': 'OUTPUT_INCOMPLETE', 'message': 'AI 输出未完成'})
                return
            if 'program' in state.phase_started and 'program' not in state.phase_completed:
                yield GenerationEvent('phase_complete', request.request_id, phase='program', data={
                    'program_count': len(state.programs),
                    'target_indices': sorted(state.programs),
                })
            logger.info(
                '统一生成 完成: scope=%s 程序数=%d 内容块=%d',
                request.scope, len(state.programs), content_chunk_count,
            )
            yield GenerationEvent('complete', request.request_id, data={
                'scope': request.scope,
                'program_count': len(state.programs),
            })
            return

        assert repair_failure is not None
        yield repair_failure
        yield GenerationEvent('error', request.request_id, data={'code': 'OUTPUT_VALIDATION_ERROR', 'message': 'AI 输出未通过协议校验'})

    async def generate_project_program(
        self,
        request: UnifiedGenerationRequest,
        provider: AgnesTextProvider,
    ) -> AsyncIterator[GenerationEvent]:
        """Generate exactly one validated ProgramIR for an existing manifest target."""
        project = self._project_context(request)
        manifest = getattr(request, 'manifest', None)
        requirements = getattr(request, 'requirements', None)
        plan = getattr(request, 'plan', None)
        target_index = getattr(request, 'target_index', None)
        program_target = getattr(request, 'program_target', None)
        if manifest is None or requirements is None or plan is None or not isinstance(target_index, int) or program_target is None:
            yield GenerationEvent('error', request.request_id, data={'code': 'INVALID_PROJECT_PROGRAM_REQUEST', 'message': '项目程序请求缺少清单上下文'})
            return
        target = self._target_context_for_manifest_target(request, manifest, target_index, program_target.mode)
        target_count = len(manifest.program_targets)
        target_data = {
            'target_index': target_index,
            'target_name': target.name,
            'target_count': target_count,
        }
        yield GenerationEvent('phase_start', request.request_id, phase='program', data=target_data)
        state = _GenerationState(
            requirements=requirements,
            plan=plan,
            manifest=manifest,
            phase_started={'requirements', 'plan'},
            phase_completed={'requirements', 'plan'},
            expected_program_indices={target_index},
        )
        program_context = {
            'requirements': requirements.model_dump(mode='json'),
            'plan': plan.model_dump(mode='json'),
            'manifest': manifest.model_dump(mode='json'),
            'target': program_target.model_dump(mode='json'),
        }

        def decode_program(frames: list[StreamFrame]) -> tuple[ProgramIR, list[GenerationEvent]]:
            if len(frames) == 1 and frames[0].kind == 'program':
                output = ProgramIR.model_validate(parse_json(frames[0].content))
                target_kind, mode, current_program = self._target_contract(request, state, target_index)
                validate_program_contracts(output, target_kind)
                validate_program_for_mode(output, mode, current_program)
                validate_project_program_shared_state(output, manifest.shared_state, target_kind)
                state.programs[target_index] = output
                dumped = output.model_dump(mode='json')
                child_events = [
                    GenerationEvent('metadata', request.request_id, phase='program', data={
                        **target_data,
                        'schema_version': dumped['schema_version'],
                        'summary': dumped['summary'],
                        'remove_script_ids': dumped['remove_script_ids'],
                    }),
                    GenerationEvent('shared_state', request.request_id, phase='program', data={
                        **target_data,
                        'variables': dumped['variables'],
                        'lists': dumped['lists'],
                        'broadcasts': dumped['broadcasts'],
                    }),
                ]
                child_events.extend(GenerationEvent('script_ready', request.request_id, phase='program', data={
                    **target_data,
                    'index': index,
                    'total': len(output.scripts),
                    'script': script,
                }) for index, script in enumerate(dumped['scripts']))
                child_events.append(GenerationEvent('phase_complete', request.request_id, phase='program', data={
                    **target_data,
                    'script_count': len(output.scripts),
                    'summary': output.summary,
                }))
                return output, child_events

            local = _GenerationState(
                requirements=requirements,
                plan=plan,
                manifest=manifest,
                phase_started={'requirements', 'plan'},
                phase_completed={'requirements', 'plan'},
                expected_program_indices={target_index},
            )
            child_events: list[GenerationEvent] = []
            for frame in self._program_segment_frames(frames, target_index):
                frame_events, frame_failure = self._consume_frame(request, local, frame)
                if frame_failure is not None:
                    raise _SegmentValidationFailure(frame_failure)
                child_events.extend(frame_events)
            if target_index not in local.programs:
                raise StreamProtocolError('program segment did not produce a complete target')
            return local.programs[target_index], child_events

        value, events, failure, _ = await self._run_segment_with_repair(
            request,
            provider,
            'program',
            lambda context: self.prompt_builder.build_segment_prompt(project, target, request, 'program', context, target_index=target_index),
            decode_program,
            program_context,
            max_rounds=self._repair_budget('program'),
            target_index=target_index,
        )
        for event in events:
            if event.type == 'phase_start':
                continue
            yield GenerationEvent(
                event.type,
                event.request_id,
                event.phase,
                data={**target_data, **event.data},
            )
        if failure is not None:
            yield GenerationEvent(
                failure.type,
                failure.request_id,
                failure.phase,
                data={**target_data, **failure.data},
            )
            yield GenerationEvent('error', request.request_id, data={
                'code': 'OUTPUT_VALIDATION_ERROR',
                'message': 'AI 输出未通过协议校验',
                **target_data,
            })
            return
        if not isinstance(value, ProgramIR):
            yield GenerationEvent('error', request.request_id, data={
                'code': 'PROGRAM_MISSING',
                'message': 'AI 未完成当前目标程序输出',
                **target_data,
            })
            return
        yield GenerationEvent('complete', request.request_id, data={
            **target_data,
            'scope': 'project',
            'output_contract': 'project_program',
            'program_count': 1,
            'program': value.model_dump(mode='json'),
        })

    async def _generate_segmented_project(
        self,
        request: UnifiedGenerationRequest,
        provider: AgnesTextProvider,
    ) -> AsyncIterator[GenerationEvent]:
        project = self._project_context(request)
        target = self._target_context(request)
        state = _GenerationState()

        requirements_context = {'scope': request.scope, 'contract_version': 2}
        requirements, events, failure, _ = await self._run_segment_with_repair(
            request,
            provider,
            'requirements',
            lambda context: self.prompt_builder.build_segment_prompt(project, target, request, 'requirements', context),
            lambda frames: RequirementsResponse.model_validate(parse_json(self._single_segment_frame(frames, 'requirements').content)),
            requirements_context,
            max_rounds=self._repair_budget('requirements'),
        )
        for event in events:
            yield event
        if failure is not None or not isinstance(requirements, RequirementsResponse):
            if failure is not None:
                yield failure
            yield self._terminal_validation_error(request, failure)
            return
        state.requirements = requirements
        yield GenerationEvent('phase_start', request.request_id, phase='requirements')
        yield GenerationEvent('phase_complete', request.request_id, phase='requirements', data=requirements.model_dump(mode='json'))
        if not request.proceed_with_defaults and not requirements.ready:
            yield GenerationEvent('error', request.request_id, data={'code': 'REQUIREMENTS_INCOMPLETE', 'message': '需求信息还不完整'})
            return

        plan_context = {'requirements': requirements.model_dump(mode='json')}
        plan, events, failure, _ = await self._run_segment_with_repair(
            request,
            provider,
            'plan',
            lambda context: self.prompt_builder.build_segment_prompt(project, target, request, 'plan', context),
            lambda frames: Plan.model_validate(parse_json(self._single_segment_frame(frames, 'plan').content)),
            plan_context,
            max_rounds=self._repair_budget('plan'),
        )
        for event in events:
            yield event
        if failure is not None or not isinstance(plan, Plan):
            if failure is not None:
                yield failure
            yield self._terminal_validation_error(request, failure)
            return
        state.plan = plan
        yield GenerationEvent('phase_start', request.request_id, phase='plan')

        manifest_context = {'requirements': requirements.model_dump(mode='json'), 'plan': plan.model_dump(mode='json')}
        manifest_budget = self._repair_budget('manifest')

        def decode_manifest(frames: list[StreamFrame]) -> tuple[ProjectManifest, list[dict[str, Any]]]:
            raw_manifest = parse_json(self._single_segment_frame(frames, 'manifest').content)
            normalized_manifest, normalizations = normalize_project_manifest_payload(raw_manifest)
            manifest = ProjectManifest.model_validate(normalized_manifest)
            return manifest, [item.as_dict() for item in normalizations]

        manifest_value, events, failure, failed_manifest_payload = await self._run_segment_with_repair(
            request,
            provider,
            'manifest',
            lambda context: self.prompt_builder.build_segment_prompt(project, target, request, 'manifest', context),
            decode_manifest,
            manifest_context,
            max_rounds=0,
        )
        for event in events:
            yield event

        if failure is not None:
            repair_scope = failure.data.get('repair_scope')
            operation_index = self._resource_op_index(failure.data.get('path')) if repair_scope == 'resource_op' else None
            if operation_index is not None and isinstance(failed_manifest_payload, dict):
                operation_failure = failure
                repaired = False
                resource_budget = self._repair_budget('resource_op')
                operation_context = {
                    'resource_op_index': operation_index,
                    'resource_op': self._payload_at(failed_manifest_payload, operation_index),
                    'failure': operation_failure.data,
                }
                for attempt in range(1, resource_budget + 1):
                    yield self._repair_event(request, operation_failure, 'resource_op', attempt, resource_budget, resource_op_index=operation_index)
                    repaired_op, op_events, op_failure, failed_op_payload = await self._run_segment_with_repair(
                        request,
                        provider,
                        'resource_op',
                        lambda context, index=operation_index: self.prompt_builder.build_segment_prompt(project, target, request, 'resource_op', context, resource_op_index=index),
                        lambda frames: TypeAdapter(ResourceOp).validate_python(parse_json(self._single_segment_frame(frames, 'resource_op').content)),
                        operation_context,
                        max_rounds=0,
                        resource_op_index=operation_index,
                    )
                    if op_failure is not None or not hasattr(repaired_op, 'model_dump'):
                        operation_failure = op_failure or failure
                        operation_context = {**operation_context, 'failure': operation_failure.data}
                        if isinstance(failed_op_payload, dict):
                            operation_context['resource_op'] = failed_op_payload
                        continue
                    for event in op_events:
                        yield event
                    try:
                        merged_payload = dict(failed_manifest_payload)
                        operations = list(merged_payload.get('resource_ops', []))
                        operations[operation_index] = repaired_op.model_dump(mode='json')
                        merged_payload['resource_ops'] = operations
                        normalized_manifest, normalizations = normalize_project_manifest_payload(merged_payload)
                        manifest_value = (ProjectManifest.model_validate(normalized_manifest), [item.as_dict() for item in normalizations])
                        repaired = True
                        break
                    except (ValidationError, ValueError, ResourceContractError) as exc:
                        operation_failure = self._validation_event(request, 'plan', exc, StreamFrame('manifest'))
                        operation_context = {**operation_context, 'failure': operation_failure.data}
                if not repaired:
                    yield operation_failure
                    yield GenerationEvent('error', request.request_id, data={'code': 'OUTPUT_VALIDATION_ERROR', 'message': 'AI 输出未通过协议校验'})
                    return
            else:
                if manifest_budget <= 0:
                    yield failure
                    yield GenerationEvent('error', request.request_id, data={'code': 'OUTPUT_VALIDATION_ERROR', 'message': 'AI 输出未通过协议校验'})
                    return
                yield self._repair_event(request, failure, 'manifest', 1, manifest_budget)
                retry_context = {**manifest_context, 'failure': failure.data, 'failed_object': failed_manifest_payload}
                manifest_value, retry_events, failure, _ = await self._run_segment_with_repair(
                    request,
                    provider,
                    'manifest',
                    lambda context: self.prompt_builder.build_segment_prompt(project, target, request, 'manifest', context),
                    decode_manifest,
                    retry_context,
                    max_rounds=0,
                )
                for event in retry_events:
                    yield event
                if failure is not None:
                    yield failure
                    yield GenerationEvent('error', request.request_id, data={'code': 'OUTPUT_VALIDATION_ERROR', 'message': 'AI 输出未通过协议校验'})
                    return
        if not isinstance(manifest_value, tuple):
            yield GenerationEvent('error', request.request_id, data={'code': 'MANIFEST_MISSING', 'message': 'AI 未完成项目清单输出'})
            return
        state.manifest, manifest_normalizations = manifest_value
        yield GenerationEvent('phase_complete', request.request_id, phase='plan', data={
            'steps': state.plan.steps,
            'manifest': state.manifest.model_dump(mode='json'),
            'normalizations': manifest_normalizations,
        })
        if getattr(request, 'output_contract', None) == 'project_manifest':
            yield GenerationEvent('complete', request.request_id, data={
                'scope': 'project',
                'output_contract': 'project_manifest',
                'program_count': 0,
            })
            return

        target_count = len(state.manifest.program_targets)
        if target_count == 0:
            yield GenerationEvent('complete', request.request_id, data={'scope': request.scope, 'program_count': 0})
            return

        state.phase_started.add('program')
        yield GenerationEvent('phase_start', request.request_id, phase='program', data={'target_index': 0})
        semaphore = asyncio.Semaphore(self.max_concurrency)

        async def run_target(
            index: int,
            emit_event: Callable[[GenerationEvent], None] | None = None,
        ) -> tuple[int, Any, list[GenerationEvent], GenerationEvent | None]:
            async with semaphore:
                target_context = self._target_context_for_manifest_target(request, state.manifest, index)

                def emit_target_event(event: GenerationEvent) -> None:
                    if emit_event is None or event.type == 'phase_start':
                        return
                    emit_event(GenerationEvent(
                        event.type,
                        event.request_id,
                        event.phase,
                        data={
                            'target_index': index,
                            'target_name': target_context.name,
                            'target_count': target_count,
                            **event.data,
                        },
                    ))

                program_context = {
                    'requirements': requirements.model_dump(mode='json'),
                    'plan': plan.model_dump(mode='json'),
                    'manifest': state.manifest.model_dump(mode='json'),
                    'target': state.manifest.program_targets[index].model_dump(mode='json'),
                }

                def decode_program(frames: list[StreamFrame]) -> tuple[ProgramIR, list[GenerationEvent]]:
                    if len(frames) == 1 and frames[0].kind == 'program':
                        output = ProgramIR.model_validate(parse_json(frames[0].content))
                        target_kind, mode, current_program = self._target_contract(request, state, index)
                        validate_program_contracts(output, target_kind)
                        validate_program_for_mode(output, mode, current_program)
                        validate_project_program_shared_state(output, state.manifest.shared_state, target_kind)
                        dumped = output.model_dump(mode='json')
                        total = len(output.scripts)
                        child_events = [GenerationEvent('metadata', request.request_id, phase='program', data={
                            'target_index': index,
                            'target_name': target_context.name,
                            'target_count': target_count,
                            'schema_version': dumped['schema_version'],
                            'summary': dumped['summary'],
                            'remove_script_ids': dumped['remove_script_ids'],
                        }), GenerationEvent('shared_state', request.request_id, phase='program', data={
                            'target_index': index,
                            'target_name': target_context.name,
                            'target_count': target_count,
                            'variables': dumped['variables'],
                            'lists': dumped['lists'],
                            'broadcasts': dumped['broadcasts'],
                        })]
                        child_events.extend(GenerationEvent('script_ready', request.request_id, phase='program', data={
                            'target_index': index,
                            'target_name': target_context.name,
                            'target_count': target_count,
                            'index': script_index,
                            'total': total,
                            'script': dumped['scripts'][script_index],
                        }) for script_index, _ in enumerate(output.scripts))
                        child_events.append(GenerationEvent('phase_complete', request.request_id, phase='program', data={
                            'target_index': index,
                            'target_name': target_context.name,
                            'target_count': target_count,
                            'script_count': total,
                            'summary': output.summary,
                        }))
                        return output, child_events
                    local = _GenerationState(
                        requirements=requirements,
                        plan=plan,
                        manifest=state.manifest,
                        phase_started={'requirements', 'plan'},
                        phase_completed={'requirements', 'plan'},
                        expected_program_indices={index},
                    )
                    child_events: list[GenerationEvent] = []
                    for frame in self._program_segment_frames(frames, index):
                        frame_events, frame_failure = self._consume_frame(request, local, frame)
                        if frame_failure is not None:
                            raise _SegmentValidationFailure(frame_failure)
                        child_events.extend(frame_events)
                    if index not in local.programs:
                        raise StreamProtocolError('program segment did not produce a complete target')
                    return local.programs[index], child_events

                value, segment_events, segment_failure, _ = await self._run_segment_with_repair(
                    request,
                    provider,
                    'program',
                    lambda context: self.prompt_builder.build_segment_prompt(project, target_context, request, 'program', context, target_index=index),
                    decode_program,
                    program_context,
                    max_rounds=self._repair_budget('program'),
                    target_index=index,
                    emit_event=emit_target_event if emit_event is not None else None,
                )
                return index, value, segment_events, segment_failure

        # Keep target order stable so each validated program can be consumed immediately.
        # A later target must not overtake an earlier one or hide a repair failure.
        for index in range(target_count):
            if index > 0:
                yield GenerationEvent('phase_start', request.request_id, phase='program', data={'target_index': index})
            event_queue: asyncio.Queue[GenerationEvent | None] = asyncio.Queue()
            target_result: list[tuple[int, Any, list[GenerationEvent], GenerationEvent | None]] = []

            async def execute_target() -> None:
                try:
                    target_result.append(await run_target(index, event_queue.put_nowait))
                finally:
                    event_queue.put_nowait(None)

            target_task = asyncio.create_task(execute_target())
            try:
                while True:
                    event = await event_queue.get()
                    if event is None:
                        break
                    yield event
                await target_task
            finally:
                if not target_task.done():
                    target_task.cancel()
                    try:
                        await target_task
                    except asyncio.CancelledError:
                        pass

            target_index, value, _segment_events, segment_failure = target_result[0]
            if segment_failure is not None:
                # The live sink already emitted the terminal validation event.
                yield GenerationEvent('error', request.request_id, data={'code': 'OUTPUT_VALIDATION_ERROR', 'message': 'AI 输出未通过协议校验'})
                return
            state.programs[target_index] = value

        yield GenerationEvent('complete', request.request_id, data={'scope': request.scope, 'program_count': len(state.programs)})

    @staticmethod
    def _record_event(
        events: list[GenerationEvent],
        event: GenerationEvent,
        emit_event: Callable[[GenerationEvent], None] | None = None,
    ) -> None:
        events.append(event)
        if emit_event is not None:
            emit_event(event)

    async def _run_segment_with_repair(
        self,
        request: UnifiedGenerationRequest,
        provider: AgnesTextProvider,
        segment: str,
        build_messages: Callable[[dict[str, Any]], tuple[list[dict[str, str]], Any]],
        decode: Callable[[list[StreamFrame]], Any],
        base_context: dict[str, Any],
        *,
        max_rounds: int,
        target_index: int | None = None,
        resource_op_index: int | None = None,
        emit_event: Callable[[GenerationEvent], None] | None = None,
    ) -> tuple[Any, list[GenerationEvent], GenerationEvent | None, Any]:
        events: list[GenerationEvent] = []
        failure: GenerationEvent | None = None
        failed_payload: Any = None
        for attempt in range(max_rounds + 1):
            context = dict(base_context)
            context.setdefault('transport', 'json_object' if self._structured_output_supported(provider, segment) else 'tagged')
            if failure is not None:
                context['failure'] = failure.data
                context['failed_object'] = failed_payload
            messages, cache = build_messages(context)
            cache_event = GenerationEvent(
                'cache_hit',
                request.request_id,
                phase=self._segment_phase(segment),
                data={'segment': segment, **cache.as_dict()},
            )
            self._record_event(events, cache_event, emit_event)
            result = await self._run_segment_once(
                request,
                provider,
                segment,
                messages,
                decode,
                target_index=target_index,
                resource_op_index=resource_op_index,
                emit_event=emit_event,
            )
            if result.failure is None:
                events.extend(result.events)
                return result.value, events, None, result.payload
            if attempt == max_rounds:
                events.extend(result.events)
            else:
                events.extend(event for event in result.events if event.type not in {'validation_error', 'content_delta'})
            failure = result.failure
            failed_payload = result.payload
            if emit_event is not None:
                emit_event(failure)
            if attempt < max_rounds:
                repair_event = self._repair_event(request, failure, segment, attempt + 1, max_rounds, target_index=target_index, resource_op_index=resource_op_index)
                self._record_event(events, repair_event, emit_event)
        return None, events, failure, failed_payload

    async def _run_segment_once(
        self,
        request: UnifiedGenerationRequest,
        provider: AgnesTextProvider,
        segment: str,
        messages: list[dict[str, str]],
        decode: Callable[[list[StreamFrame]], Any],
        *,
        target_index: int | None = None,
        resource_op_index: int | None = None,
        emit_event: Callable[[GenerationEvent], None] | None = None,
    ) -> _SegmentAttempt:
        result = _SegmentAttempt()
        raw_output = ''
        parser = UnifiedFrameParser()
        frames: list[StreamFrame] = []
        structured_output = self._structured_output_supported(provider, segment)
        json_schema = self._structured_output_schema(provider, segment) if structured_output else None
        try:
            stream_kwargs: dict[str, Any] = {
                'thinking_enabled': request.thinking.enabled,
                'thinking_strength': request.thinking.strength,
                'json_output': structured_output,
            }
            if json_schema is not None:
                stream_kwargs['json_schema'] = json_schema
            stream = provider.stream_with_reasoning(messages, **stream_kwargs)
            async for delta in stream:
                if delta.kind == 'reasoning' and delta.text:
                    self._record_event(
                        result.events,
                        GenerationEvent('reasoning_delta', request.request_id, data={'text': delta.text, **({'target_index': target_index} if target_index is not None else {})}),
                        emit_event,
                    )
                elif delta.kind == 'content' and delta.text:
                    raw_output += delta.text
                    self._record_event(
                        result.events,
                        GenerationEvent(
                            'content_delta',
                            request.request_id,
                            phase=self._segment_phase(segment),
                            data={'text': delta.text, **({'target_index': target_index} if target_index is not None else {})},
                        ),
                        emit_event,
                    )
                    if not structured_output:
                        frames.extend(parser.feed(delta.text))
            if structured_output:
                parsed = json.loads(raw_output.strip())
                if not isinstance(parsed, dict):
                    raise StreamProtocolError('structured segment must contain a JSON object')
                frames = [StreamFrame(segment, raw_output)]
            else:
                frames.extend(parser.finish())
        except (ValidationError, ValueError, StreamProtocolError) as exc:
            result.failure = self._validation_event(request, self._segment_phase(segment), exc, StreamFrame(segment))
            return result
        try:
            decoded = decode(frames)
            if isinstance(decoded, tuple) and len(decoded) == 2 and isinstance(decoded[0], ProgramIR) and isinstance(decoded[1], list):
                result.value = decoded[0]
                for event in decoded[1]:
                    self._record_event(result.events, event, emit_event)
            else:
                result.value = decoded
            result.payload = self._segment_payload(frames, segment, target_index)
        except _SegmentValidationFailure as exc:
            result.failure = exc.event
            result.payload = self._segment_payload(frames, segment, target_index)
        except (ValidationError, ValueError, StreamProtocolError) as exc:
            result.failure = self._validation_event(request, self._segment_phase(segment), exc, self._error_frame(frames, segment, target_index))
            result.payload = self._segment_payload(frames, segment, target_index)
        return result

    @staticmethod
    def _structured_output_supported(provider: AgnesTextProvider, segment: str) -> bool:
        capability = getattr(provider, 'supports_structured_output', None)
        if not callable(capability):
            return False
        try:
            return bool(capability(segment))
        except TypeError:
            return bool(capability())

    @staticmethod
    def _structured_output_schema(provider: AgnesTextProvider, segment: str) -> dict[str, Any] | None:
        capability = getattr(provider, 'supports_json_schema', None)
        if not callable(capability):
            return None
        try:
            if not capability(segment):
                return None
        except TypeError:
            if not capability():
                return None
        # Apex validates the ChatGPT 5.6 strict Responses schema dialect.
        schemas: dict[str, dict[str, Any]] = {
            'requirements': _to_chatgpt_strict_json_schema(RequirementsResponse),
            'plan': _to_chatgpt_strict_json_schema(Plan),
            'manifest': _to_chatgpt_strict_json_schema(ProjectManifest),
            'resource_op': _to_chatgpt_strict_json_schema(TypeAdapter(ResourceOp)),
            'program': _to_chatgpt_strict_json_schema(ProgramIR),
        }
        schema = schemas.get(segment)
        # A root anyOf is outside the Responses structured-output subset.
        return schema if schema is not None and schema.get('type') == 'object' else None

    @staticmethod
    def _single_segment_frame(frames: list[StreamFrame], kind: str) -> StreamFrame:
        matches = [frame for frame in frames if frame.kind == kind]
        if len(matches) != 1:
            raise StreamProtocolError(f'{kind} segment must contain exactly one frame')
        return matches[0]

    @staticmethod
    def _program_segment_frames(frames: list[StreamFrame], target_index: int) -> list[StreamFrame]:
        selected: list[StreamFrame] = []
        active = False
        for frame in frames:
            if frame.kind == 'program_start':
                if active:
                    raise StreamProtocolError('program segment contains nested programs')
                active = frame.attrs.get('target_index') == str(target_index)
                if active:
                    selected.append(frame)
                continue
            if active:
                selected.append(frame)
                if frame.kind == 'program_end':
                    return selected
        raise StreamProtocolError(f'program segment for target_index={target_index} is incomplete')

    @staticmethod
    def _segment_payload(frames: list[StreamFrame], segment: str, target_index: int | None) -> Any:
        try:
            if segment == 'program' and len(frames) == 1 and frames[0].kind == 'program':
                program = parse_json(frames[0].content)
                if not isinstance(program, dict):
                    return None
                return {
                    'target_index': target_index,
                    'metadata': {
                        'schema_version': program.get('schema_version'),
                        'summary': program.get('summary'),
                        'remove_script_ids': program.get('remove_script_ids', []),
                    },
                    'shared_state': {
                        'variables': program.get('variables', []),
                        'lists': program.get('lists', []),
                        'broadcasts': program.get('broadcasts', []),
                    },
                    'scripts': program.get('scripts', []),
                }
            if segment == 'program':
                selected = GenerationOrchestrator._program_segment_frames(frames, target_index or 0)
                metadata = next((item for item in selected if item.kind == 'metadata'), None)
                shared_state = next((item for item in selected if item.kind == 'shared_state'), None)
                scripts = [item for item in selected if item.kind == 'script']
                if metadata is None and shared_state is None and not scripts:
                    return None
                return {
                    'target_index': target_index,
                    'metadata': parse_json(metadata.content) if metadata is not None else None,
                    'shared_state': parse_json(shared_state.content) if shared_state is not None else None,
                    'scripts': [parse_json(item.content) for item in scripts],
                }
            frame = next((item for item in frames if item.kind == segment), None)
            return parse_json(frame.content) if frame is not None else None
        except (StreamProtocolError, ValueError):
            return None

    @staticmethod
    def _error_frame(frames: list[StreamFrame], segment: str, target_index: int | None) -> StreamFrame:
        if segment == 'program':
            try:
                return next(item for item in GenerationOrchestrator._program_segment_frames(frames, target_index or 0) if item.kind == 'script')
            except (StopIteration, StreamProtocolError):
                return StreamFrame('program_start', attrs={'target_index': str(target_index or 0)})
        return next((item for item in frames if item.kind == segment), StreamFrame(segment))

    @staticmethod
    def _resource_op_index(path: Any) -> int | None:
        if not isinstance(path, str):
            return None
        parts = path.strip('/').split('/')
        if len(parts) < 2 or parts[0] != 'resource_ops' or not parts[1].isdigit():
            return None
        return int(parts[1])

    @staticmethod
    def _payload_at(manifest: Any, index: int) -> Any:
        if not isinstance(manifest, dict) or not isinstance(manifest.get('resource_ops'), list):
            return None
        operations = manifest['resource_ops']
        return operations[index] if index < len(operations) else None

    def _target_context_for_manifest_target(
        self,
        request: UnifiedGenerationRequest,
        manifest: ProjectManifest,
        index: int,
        mode: Literal['modify', 'append', 'rewrite'] | None = None,
    ) -> TargetContext:
        target_ref = manifest.program_targets[index].target
        if target_ref.kind == 'current_target':
            target = self._target_context(request)
            return target if mode is None else target.model_copy(update={'mode': mode})
        target_mode = mode or 'append'
        if target_ref.kind == 'created_sprite':
            operation = next((item for item in manifest.resource_ops if item.op_id == target_ref.op_id), None)
            name = operation.name if operation is not None and hasattr(operation, 'name') else target_ref.op_id
            return TargetContext(name=name, mode=target_mode, sprite_names=tuple(item.name for item in request.project_context.targets if not item.is_stage))
        if request.project_context is None:
            raise StreamProtocolError('project context is required for a manifest target')
        if target_ref.kind == 'stage':
            matches = [item for item in request.project_context.targets if item.is_stage]
        else:
            matches = [item for item in request.project_context.targets if not item.is_stage and item.name.casefold() == target_ref.name.casefold()]
        if len(matches) != 1:
            raise StreamProtocolError('manifest target is not represented exactly once in project context')
        item = matches[0]
        return TargetContext(
            target_id=item.target_id,
            revision=item.revision,
            name=item.name,
            is_stage=item.is_stage,
            has_scripts=item.has_scripts,
            mode=target_mode,
            costumes=tuple(item.costumes),
            sounds=tuple(item.sounds),
            sprite_names=tuple(item.sprite_names),
            current_program=item.current_program,
        )

    @staticmethod
    def _repair_event(
        request: UnifiedGenerationRequest,
        failure: GenerationEvent,
        segment: str,
        attempt: int,
        max_rounds: int,
        *,
        target_index: int | None = None,
        resource_op_index: int | None = None,
    ) -> GenerationEvent:
        data = {
            'attempt': attempt,
            'max_rounds': max_rounds,
            'phase': failure.phase,
            'path': failure.data.get('path'),
            'code': failure.data.get('code'),
            'operation': failure.data.get('operation'),
            'actual': failure.data.get('actual'),
            'expected': failure.data.get('expected'),
            'repair_scope': failure.data.get('repair_scope', segment),
            'repair_id': failure.data.get('repair_id'),
            'message': failure.data.get('message'),
            'detail': failure.data.get('detail'),
            'resume': segment,
        }
        if target_index is not None:
            data['target_index'] = target_index
        if resource_op_index is not None:
            data['resource_op_index'] = resource_op_index
        return GenerationEvent('repair', request.request_id, phase=failure.phase, data=data)

    @staticmethod
    def _segment_phase(segment: str) -> str:
        return 'plan' if segment in {'manifest', 'resource_op'} else 'program' if segment == 'program' else segment

    def _validation_event(self, request: UnifiedGenerationRequest, phase: str, error: Exception, frame: StreamFrame) -> GenerationEvent:
        issue = self._structured_issue(error, frame)
        logger.warning(
            'ai_segment_validation request_id=%s phase=%s code=%s path=%s operation=%s',
            request.request_id, phase, issue.get('code'), issue.get('path'), issue.get('operation'),
        )
        return GenerationEvent('validation_error', request.request_id, phase=phase, data={
            **issue,
            'message': issue['expected'],
            'detail': self._error_detail(error),
            'recoverable': False,
        })

    @staticmethod
    def _terminal_validation_error(request: UnifiedGenerationRequest, failure: GenerationEvent | None) -> GenerationEvent:
        if failure is not None:
            return GenerationEvent('error', request.request_id, data={'code': 'OUTPUT_VALIDATION_ERROR', 'message': 'AI 输出未通过协议校验'})
        return GenerationEvent('error', request.request_id, data={'code': 'OUTPUT_INCOMPLETE', 'message': 'AI 未完成分段输出'})

    @staticmethod
    def _project_context(request: UnifiedGenerationRequest) -> ProjectContext | None:
        if request.project_context is None:
            return None
        return ProjectContext(
            project_id=request.project_context.project_id,
            version=request.project_context.version,
            targets=tuple(item.model_dump(mode='json') for item in request.project_context.targets),
            shared_state=request.project_context.shared_state,
        )

    @staticmethod
    def _target_context(request: UnifiedGenerationRequest) -> TargetContext:
        target_id = 'current-target'
        revision = 0
        if request.project_context is not None:
            matches = [
                item for item in request.project_context.targets
                if item.is_stage == request.target.is_stage and item.name == request.target.name
            ]
            if len(matches) != 1:
                raise StreamProtocolError('project context must contain exactly one current target')
            target_id = matches[0].target_id
            revision = matches[0].revision
        return TargetContext(
            target_id=target_id,
            revision=revision,
            name=request.target.name,
            is_stage=request.target.is_stage,
            has_scripts=request.target.has_scripts,
            mode=request.target.mode,
            costumes=tuple(request.target.costumes),
            sounds=tuple(request.target.sounds),
            sprite_names=tuple(request.target.sprite_names),
            current_program=request.target.current_program,
        )

    def _consume_frame(
        self,
        request: UnifiedGenerationRequest,
        state: _GenerationState,
        frame: StreamFrame,
        *,
        allow_full_restart: bool = False,
    ) -> tuple[list[GenerationEvent], GenerationEvent | None]:
        try:
            if frame.kind == 'requirements':
                if state.phase_started:
                    if not allow_full_restart:
                        raise StreamProtocolError('requirements must be the first generation frame')
                    # 修复轮中模型选择从头重出：接受并重置为全新生成状态
                    self._reset_state(state)
                self._expect_new(state, 'requirements')
                self._start_phase(state, 'requirements')
                state.requirements = RequirementsResponse.model_validate(parse_json(frame.content))
                self._complete_phase(state, 'requirements')
                logger.info(
                    '统一生成 需求完成: title=%s ready=%s 行为步骤=%d 触发=%d',
                    state.requirements.title, state.requirements.ready,
                    len(state.requirements.behavior_steps), len(state.requirements.triggers),
                )
                return [GenerationEvent('phase_start', request.request_id, phase='requirements'), GenerationEvent('phase_complete', request.request_id, phase='requirements', data=state.requirements.model_dump(mode='json'))], None
            if frame.kind == 'plan':
                if state.requirements is None:
                    raise StreamProtocolError('plan appeared before requirements')
                self._expect_new(state, 'plan')
                self._start_phase(state, 'plan')
                state.plan = Plan.model_validate(parse_json(frame.content))
                if request.scope == 'single_sprite':
                    self._complete_phase(state, 'plan')
                    logger.info('统一生成 计划完成: 步骤=%d', len(state.plan.steps))
                    return [GenerationEvent('phase_start', request.request_id, phase='plan'), GenerationEvent('phase_complete', request.request_id, phase='plan', data=state.plan.model_dump(mode='json'))], None
                return [GenerationEvent('phase_start', request.request_id, phase='plan')], None
            if frame.kind == 'manifest':
                if request.scope != 'project' or state.plan is None:
                    raise StreamProtocolError('manifest is only valid after a project plan')
                if state.phase_started.intersection({'program'}):
                    raise StreamProtocolError('manifest must precede program frames')
                if state.manifest is not None:
                    raise StreamProtocolError('duplicate project manifest')
                raw_manifest = parse_json(frame.content)
                normalized_manifest, normalizations = normalize_project_manifest_payload(raw_manifest)
                state.manifest = ProjectManifest.model_validate(normalized_manifest)
                self._complete_phase(state, 'plan')
                logger.info(
                    '统一生成 计划完成: 步骤=%d 清单程序目标=%d 资源操作=%d',
                    len(state.plan.steps), len(state.manifest.program_targets), len(state.manifest.resource_ops),
                )
                return [GenerationEvent('phase_complete', request.request_id, phase='plan', data={
                    'steps': state.plan.steps,
                    'manifest': state.manifest.model_dump(mode='json'),
                    'normalizations': [item.as_dict() for item in normalizations],
                })], None
            if frame.kind == 'program_start':
                return self._program_start(request, state, frame), None
            if frame.kind in {'metadata', 'shared_state', 'script'}:
                return self._program_child(request, state, frame), None
            if frame.kind == 'program_end':
                return self._program_end(request, state), None
            raise StreamProtocolError('unknown generation frame')
        except (ValidationError, ValueError, StreamProtocolError) as exc:
            phase = self._phase_for(frame)
            issue = self._structured_issue(exc, frame)
            detail = self._error_detail(exc)
            logger.warning(
                '统一生成 校验失败: phase=%s path=%s code=%s detail=%s',
                phase, issue['path'], issue['code'], detail,
            )
            return [], GenerationEvent('validation_error', request.request_id, phase=phase, data={
                **issue,
                'message': issue['expected'],
                'detail': detail,
                'recoverable': False,
            })

    def _program_start(self, request: UnifiedGenerationRequest, state: _GenerationState, frame: StreamFrame) -> list[GenerationEvent]:
        if state.requirements is None or state.plan is None:
            raise StreamProtocolError('program appeared before plan')
        if request.scope == 'project' and state.manifest is None:
            raise StreamProtocolError('project program appeared before manifest')
        if state.active_program is not None:
            raise StreamProtocolError('nested program frame')
        if set(frame.attrs) != {'target_index'}:
            raise StreamProtocolError('program requires only target_index')
        raw_index = frame.attrs['target_index']
        try:
            target_index = int(raw_index)
        except ValueError as exc:
            raise StreamProtocolError('target_index must be an integer') from exc
        if target_index < 0:
            raise StreamProtocolError('target_index must be non-negative')
        if request.scope == 'single_sprite' and target_index != 0:
            raise StreamProtocolError('single target program must use target_index 0')
        if request.scope == 'project' and state.manifest is not None and target_index >= len(state.manifest.program_targets):
            raise StreamProtocolError('program target_index is outside the manifest')
        if state.expected_program_indices is not None:
            if target_index not in state.expected_program_indices or target_index in state.programs:
                raise StreamProtocolError('program target_index is not assigned to this segment')
        elif target_index in state.programs or target_index != len(state.programs):
            raise StreamProtocolError('program target_index must be unique and ordered')
        if 'program' not in state.phase_started:
            self._start_phase(state, 'program')
        state.active_program = _ProgramBuffer(target_index=target_index, attrs=frame.attrs)
        return [GenerationEvent('phase_start', request.request_id, phase='program', data={'target_index': target_index})] if len(state.programs) == 0 else []

    def _program_child(self, request: UnifiedGenerationRequest, state: _GenerationState, frame: StreamFrame) -> list[GenerationEvent]:
        program = state.active_program
        if program is None:
            raise StreamProtocolError('program child is outside a program frame')
        if frame.kind == 'metadata':
            if program.metadata is not None or program.scripts:
                raise StreamProtocolError('metadata must precede scripts and appear once')
            program.metadata = ProgramMetadata.model_validate(parse_json(frame.content))
            return [GenerationEvent('metadata', request.request_id, phase='program', data={
                'target_index': program.target_index,
                **program.metadata.model_dump(mode='json'),
            })]
        if frame.kind == 'shared_state':
            if program.metadata is None:
                raise StreamProtocolError('metadata must precede shared_state')
            if program.shared_state is not None or program.scripts:
                raise StreamProtocolError('shared_state must precede scripts and appear once')
            program.shared_state = ProgramState.model_validate(parse_json(frame.content))
            return [GenerationEvent('shared_state', request.request_id, phase='program', data={
                'target_index': program.target_index,
                **program.shared_state.model_dump(mode='json'),
            })]
        if program.metadata is None or program.shared_state is None:
            raise StreamProtocolError('metadata and shared_state are required before scripts')
        if set(frame.attrs) != {'target_index', 'index', 'total'}:
            raise StreamProtocolError('script requires target_index, index, and total')
        try:
            target_index = int(frame.attrs['target_index'])
            index = int(frame.attrs['index'])
        except ValueError as exc:
            raise StreamProtocolError('script target_index and index must be integers') from exc
        if target_index != program.target_index:
            raise StreamProtocolError('script target_index does not match program target_index')
        if index != len(program.scripts):
            raise StreamProtocolError('scripts must arrive in index order')
        try:
            total = int(frame.attrs['total'])
        except ValueError as exc:
            raise StreamProtocolError('script total must be an integer') from exc
        if total < 1 or index >= total:
            raise StreamProtocolError('script total is invalid')
        if program.total is not None and program.total != total:
            raise StreamProtocolError('script total must remain constant within a program')
        program.total = total
        script = Script.model_validate(parse_json(frame.content))
        if any(item.id == script.id for item in program.scripts):
            raise StreamProtocolError('script ids must be unique within a program')
        self._validate_partial_program(request, state, program, script)
        program.scripts.append(script)
        program.total = total if total is not None else program.total
        logger.debug('统一生成 程序[%d] 脚本 %d/%d 就绪: %s', program.target_index, index, total, script.purpose)
        return [GenerationEvent('script_ready', request.request_id, phase='program', data={
            'target_index': program.target_index,
            'index': index,
            'total': total,
            'script': script.model_dump(mode='json'),
        })]

    def _program_end(self, request: UnifiedGenerationRequest, state: _GenerationState) -> list[GenerationEvent]:
        program = state.active_program
        if program is None:
            raise StreamProtocolError('program end without program start')
        if program.metadata is None or program.shared_state is None or not program.scripts:
            raise StreamProtocolError('program requires metadata, shared_state, and at least one script')
        if program.total is not None and program.total != len(program.scripts):
            raise StreamProtocolError('script total does not match the program')
        output = ProgramIR(
            schema_version=program.metadata.schema_version,
            summary=program.metadata.summary,
            remove_script_ids=program.metadata.remove_script_ids,
            variables=program.shared_state.variables,
            lists=program.shared_state.lists,
            broadcasts=program.shared_state.broadcasts,
            scripts=program.scripts,
        )
        target_kind, mode, current_program = self._target_contract(request, state, program.target_index)
        validate_program_contracts(output, target_kind)
        validate_program_for_mode(output, mode, current_program)
        if request.scope == 'project' and state.manifest is not None:
            validate_project_program_shared_state(output, state.manifest.shared_state, target_kind)
        state.programs[program.target_index] = output
        state.active_program = None
        state.phase_completed.add('program')
        logger.info(
            '统一生成 程序[%d] 完成: 脚本=%d summary=%s',
            program.target_index, len(program.scripts), output.summary,
        )
        return [GenerationEvent('phase_complete', request.request_id, phase='program', data={
            'target_index': program.target_index,
            'script_count': len(program.scripts),
            'summary': output.summary,
        })]

    def _validate_partial_program(self, request: UnifiedGenerationRequest, state: _GenerationState, program: _ProgramBuffer, script: Script) -> None:
        assert program.metadata is not None
        assert program.shared_state is not None
        partial = ProgramIR(
            schema_version=program.metadata.schema_version,
            summary=program.metadata.summary,
            remove_script_ids=program.metadata.remove_script_ids,
            variables=program.shared_state.variables,
            lists=program.shared_state.lists,
            broadcasts=program.shared_state.broadcasts,
            scripts=[script],
        )
        target_kind, mode, current_program = self._target_contract(request, state, program.target_index)
        validate_program_contracts(partial, target_kind)
        validate_program_for_mode(partial, mode, current_program)

    def _target_contract(self, request: UnifiedGenerationRequest, state: _GenerationState, index: int) -> tuple[str, str, str]:
        if request.scope == 'single_sprite':
            return ('stage' if request.target.is_stage else 'sprite', request.target.mode, request.target.current_program)
        assert state.manifest is not None
        program_target = state.manifest.program_targets[index] if index < len(state.manifest.program_targets) else None
        if program_target is None:
            raise StreamProtocolError('program target_index is not present in the manifest')
        target_ref = program_target.target
        if target_ref.kind == 'current_target':
            return ('stage' if request.target.is_stage else 'sprite', program_target.mode, request.target.current_program)
        if target_ref.kind == 'created_sprite':
            return ('sprite', program_target.mode, '')
        assert request.project_context is not None
        if target_ref.kind == 'stage':
            matches = [item for item in request.project_context.targets if item.is_stage]
            target_kind = 'stage'
        else:
            wanted = target_ref.name.casefold()
            matches = [item for item in request.project_context.targets if not item.is_stage and item.name.casefold() == wanted]
            target_kind = 'sprite'
        if len(matches) != 1:
            raise StreamProtocolError('manifest target is not represented exactly once in project context')
        return target_kind, program_target.mode, matches[0].current_program

    def _finish(self, request: UnifiedGenerationRequest, state: _GenerationState) -> GenerationEvent | None:
        if state.active_program is not None:
            return GenerationEvent('validation_error', request.request_id, phase='program', data={'path': 'program', 'message': 'program frame is incomplete', 'recoverable': False})
        if state.requirements is None or state.plan is None:
            return GenerationEvent('error', request.request_id, data={'code': 'OUTPUT_INCOMPLETE', 'message': 'AI 未完成需求和计划输出'})
        if not request.proceed_with_defaults and not state.requirements.ready:
            return GenerationEvent('error', request.request_id, data={'code': 'REQUIREMENTS_INCOMPLETE', 'message': '需求信息还不完整'})
        if request.scope == 'project':
            if state.manifest is None:
                return GenerationEvent('error', request.request_id, data={'code': 'MANIFEST_MISSING', 'message': 'AI 未完成项目清单输出'})
            if len(state.programs) != len(state.manifest.program_targets):
                return GenerationEvent('error', request.request_id, data={'code': 'PROGRAMS_INCOMPLETE', 'message': 'AI 未完成项目目标程序输出'})
        elif set(state.programs) != {0}:
            return GenerationEvent('error', request.request_id, data={'code': 'PROGRAM_MISSING', 'message': 'AI 未完成当前目标程序输出'})
        return None

    @staticmethod
    def _reset_state(state: _GenerationState) -> None:
        state.requirements = None
        state.plan = None
        state.manifest = None
        state.programs.clear()
        state.active_program = None
        state.phase_started.clear()
        state.phase_completed.clear()

    @staticmethod
    def _expect_new(state: _GenerationState, name: str) -> None:
        if name in state.phase_started:
            raise StreamProtocolError(f'duplicate {name} frame')

    @staticmethod
    def _start_phase(state: _GenerationState, name: str) -> None:
        state.phase_started.add(name)

    @staticmethod
    def _complete_phase(state: _GenerationState, name: str) -> None:
        state.phase_completed.add(name)

    @staticmethod
    def _phase_for(frame: StreamFrame) -> str:
        if frame.kind == 'manifest':
            return 'plan'
        return 'program' if frame.kind in {'program_start', 'program_end', 'metadata', 'shared_state', 'script'} else frame.kind

    @staticmethod
    def _structured_issue(error: Exception, frame: StreamFrame) -> dict[str, Any]:
        if isinstance(error, ResourceContractError):
            return error.issue.as_dict()
        scope = 'manifest' if frame.kind == 'manifest' else 'resource_op' if frame.kind == 'resource_op' else 'program' if frame.kind in {'program_start', 'program_end', 'metadata', 'shared_state', 'script'} else frame.kind if frame.kind in {'requirements', 'plan'} else 'program'
        if isinstance(error, ProgramContractError) and error.issues:
            path, message = error.issues[0]
            return {
                'code': 'PROGRAM_CONTRACT_INVALID',
                'path': to_json_pointer(path),
                'operation': 'program',
                'actual': None,
                'expected': message,
                'repair_scope': 'program',
                'repair_id': None,
            }
        if isinstance(error, ValidationError):
            errors = error.errors()
            if errors:
                item = errors[0]
                location = tuple(item.get('loc', ()))
                unsupported_version = location == ('schema_version',) and item.get('type') != 'missing' and item.get('input') is not None and item.get('input') != 2
                return {
                    'code': 'UNSUPPORTED_SCHEMA_VERSION' if unsupported_version else 'SCHEMA_VALIDATION_ERROR',
                    'path': to_json_pointer(location),
                    'operation': frame.kind,
                    'actual': bounded_actual(item.get('input')),
                    'expected': 'schema_version must equal 2' if unsupported_version else str(item.get('msg') or 'structured output is invalid'),
                    'repair_scope': scope,
                    'repair_id': None,
                }
        if isinstance(error, StreamProtocolError):
            return {
                'code': 'STREAM_PROTOCOL_ERROR',
                'path': '/output',
                'operation': frame.kind,
                'actual': bounded_actual(frame.content),
                'expected': str(error),
                'repair_scope': scope,
                'repair_id': None,
            }
        return {
            'code': 'OUTPUT_VALIDATION_ERROR',
            'path': '/output',
            'operation': frame.kind,
            'actual': None,
            'expected': 'structured output validation failed',
            'repair_scope': scope,
            'repair_id': None,
        }

    @staticmethod
    def _error_path(error: Exception) -> str:
        if isinstance(error, ResourceContractError):
            return error.issue.path
        if isinstance(error, ProgramContractError) and error.issues:
            return to_json_pointer(error.issues[0][0])
        if isinstance(error, ValidationError):
            errors = error.errors()
            if errors:
                return to_json_pointer(tuple(errors[0].get('loc', ()))) or '/output'
        return '/output'

    @staticmethod
    def _safe_message(error: Exception) -> str:
        if isinstance(error, ResourceContractError):
            return error.issue.expected
        if isinstance(error, StreamProtocolError):
            return str(error)
        if isinstance(error, ValidationError):
            return '结构化输出不符合协议'
        return '结构化输出校验失败'

    @staticmethod
    def _error_detail(error: Exception) -> str:
        if isinstance(error, ResourceContractError):
            return json.dumps(error.issue.as_dict(), ensure_ascii=False, separators=(',', ':'))
        if isinstance(error, ProgramContractError):
            return '; '.join(f'{to_json_pointer(path)}: {message}' for path, message in error.issues[:5])
        if isinstance(error, ValidationError):
            parts = []
            for item in error.errors()[:5]:
                loc = to_json_pointer(tuple(item.get('loc', ()))) or '/output'
                parts.append(f'{loc}: {item.get("type", "")}: {item.get("msg", "")}')
            return '; '.join(parts)
        return str(error)

    @staticmethod
    def _resume_tag(frame_kind: str) -> str:
        """Map a failing frame kind to the tag the repair attempt starts from."""
        if frame_kind in {'requirements', 'plan', 'manifest'}:
            return frame_kind
        return 'program'

    @staticmethod
    def _resume_state(state: _GenerationState, resume_kind: str) -> _GenerationState:
        """Rebuild generation state for a repair attempt, keeping phases that already passed."""
        if resume_kind == 'requirements':
            return _GenerationState()
        if resume_kind == 'plan':
            return _GenerationState(requirements=state.requirements)
        keep = {'requirements', 'plan'}
        return _GenerationState(
            requirements=state.requirements,
            plan=state.plan,
            manifest=state.manifest if resume_kind == 'program' else None,
            phase_started=keep,
            phase_completed=keep,
        )

    @staticmethod
    def _repair_messages(
        messages: list[dict[str, str]],
        raw_output: str,
        failure: GenerationEvent,
        resume_kind: str,
    ) -> list[dict[str, str]]:
        instructions = {
            'requirements': '重新输出完整的 <requirements>、<plan> 及后续标签流',
            'plan': '从 <plan> 标签开始重新输出（保持已通过的 requirements 不变）',
            'manifest': '从 <manifest> 标签开始重新输出（保持已通过的 requirements 和 plan 不变）',
            'program': '从第一个 <program> 标签开始重新输出全部程序（保持已通过的 requirements、plan 和 manifest 不变）',
        }
        detail = failure.data.get('detail') or failure.data.get('message') or '未知校验错误'
        hint = repair_hint(failure.phase, failure.data.get('path'), str(detail))
        content = (
            f'上一次输出的以下内容未通过服务端校验：{detail}。'
            f'修复重点：{hint}'
            f'请立即修正并{instructions[resume_kind]}。'
            '只输出标签和严格 JSON，禁止 markdown、解释、代码围栏或任何标签外文本；'
            '先对照 FINAL OUTPUT CONTRACT 逐条检查报错所指字段，再输出修正后的内容。'
        )
        return [*messages, {'role': 'assistant', 'content': raw_output[:12000]}, {'role': 'user', 'content': content}]

    @staticmethod
    def _failure(request_id: str, code: str) -> GenerationEvent:
        return GenerationEvent('error', request_id, data={'code': code, 'message': 'AI 输出未通过协议校验'})
