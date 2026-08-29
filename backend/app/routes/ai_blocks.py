from collections.abc import AsyncIterator, Callable
from functools import lru_cache
import asyncio
import json
import logging
from typing import Any

from fastapi import APIRouter, Depends, HTTPException
from fastapi.responses import StreamingResponse
from pydantic import ValidationError

from ..config import get_settings
from ..prompts_ai_blocks import (
    build_chat_messages,
    build_plan_messages,
    build_plan_repair_messages,
    build_program_messages,
    build_program_repair_messages,
    build_project_manifest_messages,
    build_project_manifest_repair_messages,
    build_project_program_messages,
    build_project_program_repair_messages,
    build_requirements_messages,
    build_requirements_repair_messages,
)
from ..providers.agnes_text_provider import AgnesTextProvider, AgnesTextProviderError, AgnesStreamDelta
from ..schemas.ai_blocks import (
    ChatRequest,
    GenerateRequest,
    Plan,
    ProgramIR,
    ProjectManifest,
    RequirementsRequest,
    RequirementsResponse,
    ThinkingSettings,
    extract_single_json,
    validate_program_contracts,
    validate_program_for_mode,
    normalize_project_manifest_input,
)

router = APIRouter(prefix='/api/ai-blocks', tags=['ai-blocks'])
logger = logging.getLogger(__name__)


@lru_cache(maxsize=1)
def get_ai_blocks_provider() -> AgnesTextProvider:
    return AgnesTextProvider(get_settings())


@lru_cache(maxsize=1)
def get_ai_blocks_semaphore() -> asyncio.Semaphore:
    return asyncio.Semaphore(get_settings().ai_blocks_max_concurrency)


def _sse(event: str, data: dict[str, Any]) -> str:
    return f'event: {event}\ndata: {json.dumps(data, ensure_ascii=False, separators=(",", ":"))}\n\n'


def _error_summary(error: Exception) -> str:
    if isinstance(error, ValidationError):
        return '; '.join('.'.join(map(str, item['loc'])) + ': ' + item['msg'] for item in error.errors())[:2000]
    detail = str(error).strip()
    return detail[:2000] if detail else type(error).__name__


def _provider_for_tier(provider: AgnesTextProvider, tier: str) -> AgnesTextProvider:
    return provider.for_tier(tier) if hasattr(provider, 'for_tier') else provider


def _require_provider(provider: AgnesTextProvider, tier: str) -> AgnesTextProvider:
    selected = _provider_for_tier(provider, tier)
    try:
        selected.ensure_configured()
    except Exception as exc:
        logger.warning('ai_provider_not_configured provider=%s error_type=%s', getattr(selected, 'provider', 'unknown'), type(exc).__name__)
        raise HTTPException(
            status_code=503,
            detail={'code': 'AGNES_NOT_CONFIGURED', 'message': 'AI 服务尚未配置，请联系管理员'},
        ) from exc
    return selected


def _configured_retries() -> int:
    return max(0, get_settings().ai_blocks_retry_attempts - 1)


def _phase(request_id: str, name: str, message: str) -> str:
    return _sse('phase', {'request_id': request_id, 'phase': name, 'message': message})


async def _timed_events(
    provider: AgnesTextProvider,
    messages: list[dict[str, str]],
    thinking: ThinkingSettings,
    json_output: bool = False,
) -> AsyncIterator[AgnesStreamDelta]:
    timeout = get_settings().ai_blocks_provider_timeout_seconds
    try:
        async with asyncio.timeout(timeout):
            try:
                stream = provider.stream_with_reasoning(
                    messages,
                    thinking_enabled=thinking.enabled,
                    thinking_strength=thinking.strength,
                    json_output=json_output,
                )
            except TypeError as exc:
                if 'json_output' not in str(exc):
                    raise
                stream = provider.stream_with_reasoning(
                    messages,
                    thinking_enabled=thinking.enabled,
                    thinking_strength=thinking.strength,
                )
            async for delta in stream:
                if delta.kind == 'content':
                    print(delta.text, end='', flush=True)
                yield delta
    except TimeoutError as exc:
        raise AgnesTextProviderError('AI provider timed out') from exc
    except AgnesTextProviderError:
        raise
    except Exception as exc:
        logger.warning(
            'ai_stream_failed provider=%s error_type=%s',
            getattr(provider, 'provider', 'unknown'), type(exc).__name__,
        )
        raise AgnesTextProviderError('AI provider stream failed') from exc


async def _collect_resilient(
    provider: AgnesTextProvider,
    messages: list[dict[str, str]],
    thinking: ThinkingSettings,
    retries: int = 2,
    retry_delay_seconds: float | None = None,
    json_output: bool = False,
) -> tuple[str, dict[str, int] | None, list[tuple[str, str]]]:
    failures: list[tuple[str, str]] = []
    current = thinking
    delay_base = get_settings().ai_blocks_retry_delay_seconds if retry_delay_seconds is None else retry_delay_seconds
    for attempt in range(retries + 1):
        chunks: list[str] = []
        usage: dict[str, int] | None = None
        try:
            async for delta in _timed_events(provider, messages, current, json_output):
                if delta.kind == 'content':
                    chunks.append(delta.text)
                elif delta.kind == 'usage':
                    usage = delta.usage
            return ''.join(chunks), usage, failures
        except AgnesTextProviderError as exc:
            failures.append((''.join(chunks), _error_summary(exc)))
            if attempt >= retries:
                raise
            delay = delay_base * (2 ** attempt)
            logger.warning(
                'ai_provider_retry provider=%s attempt=%s max_attempts=%s delay_seconds=%s error_type=%s',
                getattr(provider, 'provider', 'unknown'), attempt + 2, retries + 1, delay, type(exc).__name__,
            )
            if delay:
                await asyncio.sleep(delay)
            current = ThinkingSettings(enabled=False, strength='low')
    raise AgnesTextProviderError('AI provider failed')


async def _generate_json(
    provider: AgnesTextProvider,
    messages: list[dict[str, str]],
    model: Any,
    thinking: ThinkingSettings,
    repair_builder: Callable[[list[tuple[str, str]]], list[dict[str, str]]],
    retries: int = 2,
    validator: Callable[[Any], None] | None = None,
    on_repair: Callable[[int], None] | None = None,
) -> Any:
    failures: list[tuple[str, str]] = []
    current_messages = messages
    for attempt in range(retries + 1):
        raw, _usage, stream_failures = await _collect_resilient(
            provider, current_messages, thinking, retries=_configured_retries(), json_output=True,
        )
        if stream_failures:
            failures.extend(stream_failures)
        try:
            value = model.model_validate(extract_single_json(raw))
            if validator is not None:
                validator(value)
            return value
        except (ValidationError, ValueError, TypeError) as exc:
            failures.append((raw, _error_summary(exc)))
            if attempt >= retries:
                raise
            if on_repair is not None:
                on_repair(attempt + 1)
            current_messages = repair_builder(failures)
    raise AgnesTextProviderError('AI JSON generation failed')


async def _generate_json_events(
    provider: AgnesTextProvider,
    messages: list[dict[str, str]],
    model: Any,
    thinking: ThinkingSettings,
    repair_builder: Callable[[list[tuple[str, str]]], list[dict[str, str]]],
    retries: int = 2,
    validator: Callable[[Any], None] | None = None,
    normalizer: Callable[[Any], Any] | None = None,
) -> AsyncIterator[tuple[str, Any]]:
    failures: list[tuple[str, str]] = []
    current_messages = messages
    for attempt in range(retries + 1):
        raw, _usage, stream_failures = await _collect_resilient(
            provider, current_messages, thinking, retries=_configured_retries(), json_output=True,
        )
        if stream_failures:
            failures.extend(stream_failures)
        try:
            raw_value = extract_single_json(raw)
            value = model.model_validate(normalizer(raw_value) if normalizer else raw_value)
            if validator is not None:
                validator(value)
            yield 'result', value
            return
        except (ValidationError, ValueError, TypeError) as exc:
            failures.append((raw, _error_summary(exc)))
            if attempt >= retries:
                raise
            yield 'repair', attempt + 1
            current_messages = repair_builder(failures)
    raise AgnesTextProviderError('AI JSON generation failed')


@router.post('/chat')
async def chat(
    body: ChatRequest,
    provider: AgnesTextProvider = Depends(get_ai_blocks_provider),
    semaphore: asyncio.Semaphore = Depends(get_ai_blocks_semaphore),
):
    selected = _require_provider(provider, body.model_tier)

    async def generate():
        async with semaphore:
            try:
                async for delta in _timed_events(selected, build_chat_messages(body), body.thinking):
                    if delta.kind == 'reasoning':
                        yield _sse('reasoning_delta', {'request_id': body.request_id, 'text': delta.text})
                    elif delta.kind == 'content':
                        yield _sse('assistant_delta', {'request_id': body.request_id, 'text': delta.text})
                yield _sse('message_complete', {'request_id': body.request_id})
            except Exception as exc:
                logger.error(
                    'ai_chat_failed request_id=%s error_type=%s', body.request_id, type(exc).__name__,
                )
                yield _sse('message_error', {
                    'request_id': body.request_id,
                    'code': 'AI_CHAT_ERROR',
                    'message': 'AI 对话暂时不可用，请稍后重试',
                })

    return StreamingResponse(generate(), media_type='text/event-stream', headers={'Cache-Control': 'no-cache'})


@router.post('/requirements', response_model=RequirementsResponse)
async def requirements(
    body: RequirementsRequest,
    provider: AgnesTextProvider = Depends(get_ai_blocks_provider),
    semaphore: asyncio.Semaphore = Depends(get_ai_blocks_semaphore),
):
    selected = _require_provider(provider, body.model_tier)
    async with semaphore:
        failures: list[tuple[str, str]] = []
        messages = build_requirements_messages(body)
        for attempt in range(3):
            try:
                raw, _usage, stream_failures = await _collect_resilient(selected, messages, body.thinking, json_output=True)
                if stream_failures:
                    failures.extend(stream_failures)
                result = RequirementsResponse.model_validate(extract_single_json(raw))
                if body.proceed_with_defaults and not result.ready:
                    user_text = next((message.content.strip() for message in reversed(body.messages) if message.role == 'user' and message.content.strip()), body.target.name)
                    goal = next((message.content.strip() for message in body.messages if message.role == 'user' and message.content.strip()), body.target.name)
                    result = result.model_copy(update={
                        'ready': True,
                        'title': result.title or body.target.name,
                        'goal': result.goal or goal,
                        'behavior_steps': result.behavior_steps or [goal],
                        'triggers': result.triggers or ['点击绿旗'],
                        'success_criteria': result.success_criteria or [goal],
                        'missing_questions': [],
                    })
                return result
            except (ValidationError, ValueError, TypeError) as exc:
                failures.append((raw if 'raw' in locals() else '', _error_summary(exc)))
                if attempt == 2:
                    raise HTTPException(
                        status_code=502,
                        detail={'code': 'INVALID_REQUIREMENTS_RESPONSE', 'message': 'AI 返回的需求结构无法校验'},
                    ) from exc
                messages = build_requirements_repair_messages(body, failures)
        raise HTTPException(status_code=502, detail={'code': 'INVALID_REQUIREMENTS_RESPONSE', 'message': 'AI 返回的需求结构无法校验'})


@router.post('/generate')
async def generate_endpoint(
    body: GenerateRequest,
    provider: AgnesTextProvider = Depends(get_ai_blocks_provider),
    semaphore: asyncio.Semaphore = Depends(get_ai_blocks_semaphore),
):
    selected = _require_provider(provider, body.model_tier)

    async def generate():
        async with semaphore:
            ready_sent = False
            try:
                plan = body.plan
                if plan is None:
                    yield _phase(body.request_id, 'planning', '正在规划积木步骤')
                    async for event, value in _generate_json_events(
                        selected,
                        build_plan_messages(body),
                        Plan,
                        body.thinking,
                        lambda failures: build_plan_repair_messages(body, failures),
                        retries=2,
                    ):
                        if event == 'repair':
                            yield _phase(body.request_id, 'repairing', '正在校正规划结构')
                        else:
                            plan = value
                if plan is None:
                    raise AgnesTextProviderError('AI plan is missing')
                yield _sse('plan_ready', {'request_id': body.request_id, 'plan': plan.model_dump(mode='json')})

                if body.output_contract == 'program_ir':
                    yield _phase(body.request_id, 'generating', '正在生成积木程序')
                    program = None
                    async for event, value in _generate_json_events(
                        selected,
                        build_program_messages(body, plan.steps),
                        ProgramIR,
                        body.thinking,
                        lambda failures: build_program_repair_messages(body, plan.steps, failures),
                        retries=4,
                        validator=lambda item: (
                            validate_program_contracts(item),
                            validate_program_for_mode(item, body.target.mode, body.target.current_program),
                        ),
                    ):
                        if event == 'repair':
                            yield _phase(body.request_id, 'repairing', '正在校正积木程序')
                        else:
                            program = value
                    if program is None:
                        raise AgnesTextProviderError('AI program is missing')
                    yield _sse('program_ready', {'request_id': body.request_id, 'program': program.model_dump(mode='json')})
                    ready_sent = True
                elif body.output_contract == 'project_manifest':
                    yield _phase(body.request_id, 'generating', '正在生成项目清单')
                    manifest = None
                    async for event, value in _generate_json_events(
                        selected,
                        build_project_manifest_messages(body, plan.steps),
                        ProjectManifest,
                        body.thinking,
                        lambda failures: build_project_manifest_repair_messages(body, plan.steps, failures),
                        normalizer=normalize_project_manifest_input,
                        retries=2,
                    ):
                        if event == 'repair':
                            yield _phase(body.request_id, 'repairing', '正在校正项目清单')
                        else:
                            manifest = value
                    if manifest is None:
                        raise AgnesTextProviderError('AI manifest is missing')
                    yield _sse('manifest_ready', {'request_id': body.request_id, 'manifest': manifest.model_dump(mode='json', exclude_none=True)})
                    ready_sent = True
                elif body.output_contract == 'project_program':
                    if body.project_manifest is None or body.program_target is None:
                        raise ValueError('project_program requires project_manifest and program_target')
                    yield _phase(body.request_id, 'generating', '正在生成目标积木')
                    program = None
                    async for event, value in _generate_json_events(
                        selected,
                        build_project_program_messages(body, plan.steps),
                        ProgramIR,
                        body.thinking,
                        lambda failures: build_project_program_repair_messages(body, plan.steps, failures),
                        retries=4,
                        validator=lambda item: (
                            validate_program_contracts(item),
                            validate_program_for_mode(item, body.program_target.mode, body.target.current_program),
                        ),
                    ):
                        if event == 'repair':
                            yield _phase(body.request_id, 'repairing', '正在校正目标积木')
                        else:
                            program = value
                    if program is None:
                        raise AgnesTextProviderError('AI program is missing')
                    yield _sse('project_program_ready', {
                        'request_id': body.request_id,
                        'target': body.program_target.model_dump(mode='json', exclude_none=True),
                        'program': program.model_dump(mode='json'),
                    })
                    ready_sent = True
                yield _sse('message_complete', {'request_id': body.request_id})
            except Exception as exc:
                logger.error(
                    'ai_generate_failed request_id=%s contract=%s error_type=%s summary=%s',
                    body.request_id, body.output_contract, type(exc).__name__, _error_summary(exc),
                )
                if not ready_sent:
                    yield _sse('message_error', {
                        'request_id': body.request_id,
                        'code': 'AI_BLOCKS_GENERATION_ERROR',
                        'message': 'AI 积木生成未完成，请重试',
                    })

    return StreamingResponse(
        generate(),
        media_type='text/event-stream',
        headers={'Cache-Control': 'no-cache', 'X-Accel-Buffering': 'no'},
    )
