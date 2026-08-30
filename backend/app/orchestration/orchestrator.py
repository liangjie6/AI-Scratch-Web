from __future__ import annotations

from collections.abc import AsyncIterator
from dataclasses import dataclass, field
import json
import logging
from typing import Any

from pydantic import ValidationError

from ..prompt_builder import PromptBuilder, ProjectContext, TargetContext
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
    Script,
    UnifiedGenerationRequest,
    normalize_project_manifest_payload,
    to_json_pointer,
    validate_program_contracts,
    validate_program_for_mode,
)
from .streaming_parser import StreamFrame, StreamProtocolError, UnifiedFrameParser, parse_json


logger = logging.getLogger(__name__)


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


class GenerationOrchestrator:
    """Run requirements, planning, and program generation over one provider stream."""

    def __init__(self, prompt_builder: PromptBuilder | None = None, max_repair_rounds: int = 1) -> None:
        self.prompt_builder = prompt_builder or PromptBuilder()
        self.max_repair_rounds = max(0, max_repair_rounds)

    async def generate_unified(
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
        if target_index in state.programs or target_index != len(state.programs):
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
        scope = 'manifest' if frame.kind == 'manifest' else 'program' if frame.kind in {'program_start', 'program_end', 'metadata', 'shared_state', 'script'} else 'stream'
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
                return {
                    'code': 'SCHEMA_VALIDATION_ERROR',
                    'path': to_json_pointer(tuple(item.get('loc', ()))),
                    'operation': frame.kind,
                    'actual': None,
                    'expected': str(item.get('msg') or 'structured output is invalid'),
                    'repair_scope': scope,
                    'repair_id': None,
                }
        if isinstance(error, StreamProtocolError):
            return {
                'code': 'STREAM_PROTOCOL_ERROR',
                'path': '/output',
                'operation': frame.kind,
                'actual': None,
                'expected': str(error),
                'repair_scope': 'stream',
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
        content = (
            f'上一次输出的以下内容未通过服务端校验：{detail}。'
            f'请立即修正并{instructions[resume_kind]}。'
            '只输出标签和严格 JSON，禁止 markdown、解释、代码围栏或任何标签外文本；'
            '先对照 FINAL OUTPUT CONTRACT 逐条检查报错所指字段，再输出修正后的内容。'
        )
        return [*messages, {'role': 'assistant', 'content': raw_output[:12000]}, {'role': 'user', 'content': content}]

    @staticmethod
    def _failure(request_id: str, code: str) -> GenerationEvent:
        return GenerationEvent('error', request_id, data={'code': code, 'message': 'AI 输出未通过协议校验'})
