from collections.abc import AsyncIterator
from dataclasses import dataclass
from typing import Any, Literal
import json
import logging
import time

import httpx

from ..ai_blocks_contract import (
    DEEPSEEK_REASONING_EFFORT,
    THINKING_BUDGET_TOKENS,
    ThinkingStrength,
    provider_for_tier,
)
from ..config import Settings
_STOP_SEQUENCES = ['\n---', '\n现在是 **Agnes**', '\n现在是 Agnes']
logger = logging.getLogger(__name__)

class AgnesTextProviderError(RuntimeError):
    pass

@dataclass(frozen=True)
class AgnesStreamDelta:
    kind: Literal['reasoning', 'content', 'usage']
    text: str = ''
    usage: dict[str, int] | None = None

class AgnesTextProvider:
    def __init__(self, settings: Settings, http_client: Any | None = None, provider: str = 'agnes') -> None:
        self.settings, self.http_client, self.provider = settings, http_client, provider

    def for_tier(self, tier: str) -> 'AgnesTextProvider':
        return AgnesTextProvider(self.settings, self.http_client, provider_for_tier(tier))

    def _log_request_metadata(self, messages: list[dict[str, str]]) -> None:
        """Keep observability without persisting student prompts or project content."""
        logger.info(
            'ai_provider_request provider=%s model=%s base_url=%s message_count=%d content_chars=%d',
            self.provider,
            self._model(),
            self._base_url(),
            len(messages),
            sum(len(str(message.get('content', ''))) for message in messages if isinstance(message, dict)),
        )

    def ensure_configured(self) -> None:
        self._api_key()

    def supports_structured_output(self, segment: str | None = None) -> bool:
        """Return true only for segments supported by the provider's JSON transport."""
        return self.provider in {'deepseek', 'gpt'} and segment in {'requirements', 'task_spec', 'blueprint', 'plan', 'manifest', 'resource_op', 'program'}

    def supports_json_schema(self, segment: str | None = None) -> bool:
        """Return true only for ChatGPT 5.6-safe strict JSON Schema segments."""
        # Manifest/resource_op use unions and program uses dynamic Scratch maps;
        # send those as JSON objects and keep validation on the backend.
        return self.provider == 'gpt' and segment in {'requirements', 'task_spec', 'blueprint', 'plan'}

    def _api_key(self) -> str:
        if self.provider == 'deepseek': return self.settings.require_deepseek_api_key()
        if self.provider == 'gpt': return self.settings.require_gpt_api_key()
        return self.settings.require_agnes_api_key()

    def _base_url(self) -> str:
        return {'deepseek': self.settings.deepseek_api_base, 'gpt': self.settings.gpt_api_base}.get(self.provider, self.settings.agnes_api_base)

    def _model(self) -> str:
        return {'deepseek': self.settings.deepseek_chat_model, 'gpt': self.settings.gpt_chat_model}.get(self.provider, self.settings.agnes_chat_model)

    async def stream(self, messages: list[dict[str, str]]) -> AsyncIterator[str]:
        async for delta in self._stream_responses(messages, thinking_enabled=False, thinking_strength='low', json_output=False) if self.provider == 'gpt' else self._stream_openai(messages, enable_thinking=None, thinking_strength='low'):
            if delta.kind == 'content': yield delta.text

    async def stream_with_reasoning(
        self,
        messages: list[dict[str, str]],
        *,
        thinking_enabled: bool,
        thinking_strength: ThinkingStrength,
        json_output: bool = False,
        json_schema: dict[str, Any] | None = None,
    ) -> AsyncIterator[AgnesStreamDelta]:
        self._log_request_metadata([{**message, '_provider': self.provider} for message in messages])
        endpoint = 'messages' if thinking_enabled and self.provider == 'agnes' else 'responses' if self.provider == 'gpt' else 'chat/completions'
        started = time.monotonic()
        delta_count = 0
        logger.info(
            'ai_provider_stream_start provider=%s model=%s endpoint=%s json_output=%s '
            'json_schema=%s thinking_enabled=%s thinking_strength=%s',
            self.provider,
            self._model(),
            endpoint,
            json_output,
            json_schema is not None,
            thinking_enabled,
            thinking_strength,
        )
        try:
            if thinking_enabled and self.provider == 'agnes':
                source = self._stream_anthropic(messages, thinking_strength)
            elif self.provider == 'gpt':
                source = self._stream_responses(
                    messages,
                    thinking_enabled=thinking_enabled,
                    thinking_strength=thinking_strength,
                    json_output=json_output,
                    json_schema=json_schema,
                )
            else:
                source = self._stream_openai(
                    messages,
                    enable_thinking=thinking_enabled,
                    thinking_strength=thinking_strength,
                    json_output=json_output,
                    json_schema=json_schema,
                )
            async for delta in source:
                delta_count += 1
                yield delta
        finally:
            logger.info(
                'ai_provider_stream_end provider=%s model=%s endpoint=%s delta_count=%d duration_ms=%d',
                self.provider,
                self._model(),
                endpoint,
                delta_count,
                max(0, round((time.monotonic() - started) * 1000)),
            )

    async def _stream_openai(
        self,
        messages: list[dict[str, str]],
        *,
        enable_thinking: bool | None,
        thinking_strength: ThinkingStrength = 'medium',
        json_output: bool = False,
        json_schema: dict[str, Any] | None = None,
    ) -> AsyncIterator[AgnesStreamDelta]:
        payload: dict[str, Any] = {
            'model': self._model(),
            'messages': messages,
            'stream': True,
            'stream_options': {'include_usage': True},
            'stop': _STOP_SEQUENCES,
        }
        if enable_thinking is not None and self.provider == 'agnes': payload['chat_template_kwargs'] = {'enable_thinking': enable_thinking}
        if self.provider == 'deepseek':
            if enable_thinking:
                payload['reasoning_effort'] = DEEPSEEK_REASONING_EFFORT[thinking_strength]
            if json_output:
                payload['response_format'] = (
                    {'type': 'json_schema', 'json_schema': {'name': 'ai_blocks_segment', 'strict': True, 'schema': json_schema}}
                    if json_schema is not None
                    else {'type': 'json_object'}
                )
        headers = {'Authorization': f'Bearer {self._api_key()}', 'Content-Type': 'application/json'}
        async for delta in self._request_stream('chat/completions', payload, headers, self._parse_openai_lines): yield delta

    async def _stream_responses(
        self,
        messages: list[dict[str, str]],
        *,
        thinking_enabled: bool,
        thinking_strength: ThinkingStrength,
        json_output: bool,
        json_schema: dict[str, Any] | None = None,
    ) -> AsyncIterator[AgnesStreamDelta]:
        payload: dict[str, Any] = {'model': self._model(), 'input': messages, 'stream': True}
        if thinking_enabled: payload['reasoning'] = {'effort': thinking_strength, 'summary': 'auto'}
        if json_output:
            payload['text'] = {'format': (
                {'type': 'json_schema', 'name': 'ai_blocks_segment', 'strict': True, 'schema': json_schema}
                if json_schema is not None
                else {'type': 'json_object'}
            )}
        headers = {'Authorization': f'Bearer {self._api_key()}', 'Content-Type': 'application/json'}
        async for delta in self._request_stream('responses', payload, headers, self._parse_responses_lines): yield delta

    async def _stream_anthropic(self, messages: list[dict[str, str]], strength: ThinkingStrength) -> AsyncIterator[AgnesStreamDelta]:
        system = [m['content'] for m in messages if m['role'] == 'system']
        payload: dict[str, Any] = {'model': self.settings.agnes_chat_model, 'messages': [m for m in messages if m['role'] != 'system'], 'max_tokens': 32768, 'stream': True, 'stop_sequences': _STOP_SEQUENCES, 'thinking': {'type': 'enabled', 'budget_tokens': THINKING_BUDGET_TOKENS[strength]}}
        if system: payload['system'] = '\n\n'.join(system)
        headers = {'x-api-key': self.settings.require_agnes_api_key(), 'anthropic-version': '2023-06-01', 'Content-Type': 'application/json'}
        async for delta in self._request_stream('messages', payload, headers, self._parse_anthropic_lines): yield delta

    async def _request_stream(self, endpoint: str, payload: dict[str, Any], headers: dict[str, str], parser: Any) -> AsyncIterator[AgnesStreamDelta]:
        try:
            if self.http_client is None:
                async with httpx.AsyncClient(base_url=self._base_url(), timeout=httpx.Timeout(self.settings.ai_blocks_provider_timeout_seconds, connect=10)) as client:
                    async with client.stream('POST', endpoint, json=payload, headers=headers) as response:
                        async for delta in self._iter_response(response, parser):
                            yield delta
            else:
                async with self.http_client.stream('POST', endpoint, json=payload, headers=headers) as response:
                    async for delta in self._iter_response(response, parser):
                        yield delta
        except AgnesTextProviderError: raise
        except httpx.HTTPError as exc:
            logger.error('ai_provider_connection_failed provider=%s endpoint=%s error_type=%s', self.provider, endpoint, type(exc).__name__)
            raise AgnesTextProviderError('AI provider connection failed') from exc

    async def _iter_response(self, response: Any, parser: Any) -> AsyncIterator[AgnesStreamDelta]:
        self._raise_for_status(response.status_code)
        async for delta in parser(response.aiter_lines()):
            yield delta

    @staticmethod
    def _raise_for_status(status_code: int) -> None:
        if status_code < 200 or status_code >= 300: raise AgnesTextProviderError(f'AI provider rejected request with status {status_code}')

    @staticmethod
    async def _parse_openai_lines(lines: AsyncIterator[str]) -> AsyncIterator[AgnesStreamDelta]:
        async for line in lines:
            line=line.strip()
            if not line or line.startswith(':') or line.startswith('event:'): continue
            raw=line[5:].strip() if line.startswith('data:') else line
            if raw=='[DONE]': return
            try: item=json.loads(raw)
            except json.JSONDecodeError as exc: raise AgnesTextProviderError('AI provider returned invalid stream') from exc
            if not isinstance(item,dict) or item.get('error'): raise AgnesTextProviderError('AI provider stream failed')
            usage = item.get('usage')
            if isinstance(usage, dict):
                yield AgnesStreamDelta('usage', usage=AgnesTextProvider._normalize_usage(usage))
            choices=item.get('choices'); delta=choices[0].get('delta',{}) if isinstance(choices,list) and choices and isinstance(choices[0],dict) else {}
            if not isinstance(delta,dict): continue
            reasoning=delta.get('reasoning_content') or delta.get('reasoning'); content=delta.get('content')
            if isinstance(reasoning,str) and reasoning: yield AgnesStreamDelta('reasoning', reasoning)
            if isinstance(content,str) and content: yield AgnesStreamDelta('content', content)

    @staticmethod
    async def _parse_responses_lines(lines: AsyncIterator[str]) -> AsyncIterator[AgnesStreamDelta]:
        async for line in lines:
            line=line.strip()
            if not line or line.startswith(':') or line.startswith('event:'): continue
            if not line.startswith('data:'): continue
            raw=line[5:].strip()
            if raw=='[DONE]': return
            try: item=json.loads(raw)
            except json.JSONDecodeError as exc: raise AgnesTextProviderError('GPT Responses stream is invalid') from exc
            if not isinstance(item,dict): continue
            event_type=item.get('type','')
            if event_type in {'response.failed','response.incomplete'} or item.get('error'): raise AgnesTextProviderError('GPT Responses stream failed')
            if event_type in {'response.output_text.delta'}:
                text=item.get('delta')
                if isinstance(text,str) and text: yield AgnesStreamDelta('content', text)
            elif event_type in {'response.reasoning_summary_text.delta','response.reasoning_text.delta'}:
                text=item.get('delta')
                if isinstance(text,str) and text: yield AgnesStreamDelta('reasoning', text)
            elif event_type=='response.completed':
                response=item.get('response', {}); usage=response.get('usage') if isinstance(response,dict) else None
                usage=usage or item.get('usage')
                if isinstance(usage,dict):
                    yield AgnesStreamDelta('usage', usage=AgnesTextProvider._normalize_usage(usage))
                # Responses SSE streams may keep the HTTP connection alive
                # after the terminal event. Stop at the protocol terminator
                # instead of waiting for EOF (which can consume the full
                # provider timeout on later generation segments).
                return

    @staticmethod
    async def _parse_anthropic_lines(lines: AsyncIterator[str]) -> AsyncIterator[AgnesStreamDelta]:
        async for line in lines:
            line=line.strip()
            if not line or line.startswith(':') or line.startswith('event:'): continue
            raw=line[5:].strip() if line.startswith('data:') else line
            if raw=='[DONE]': return
            try: item=json.loads(raw)
            except json.JSONDecodeError as exc: raise AgnesTextProviderError('AI provider returned invalid stream') from exc
            if not isinstance(item,dict) or item.get('type')=='error' or item.get('error'): raise AgnesTextProviderError('AI provider stream failed')
            if item.get('type') in {'message_start', 'message_delta'}:
                message = item.get('message')
                usage = message.get('usage') if isinstance(message, dict) else item.get('usage')
                if isinstance(usage, dict):
                    yield AgnesStreamDelta('usage', usage=AgnesTextProvider._normalize_usage(usage))
                continue
            if item.get('type')!='content_block_delta': continue
            delta=item.get('delta')
            if not isinstance(delta,dict): continue
            if delta.get('type')=='thinking_delta' and isinstance(delta.get('thinking'),str): yield AgnesStreamDelta('reasoning', delta['thinking'])
            elif delta.get('type')=='text_delta' and isinstance(delta.get('text'),str): yield AgnesStreamDelta('content', delta['text'])

    @staticmethod
    def _normalize_usage(usage: dict[str, Any]) -> dict[str, int]:
        input_tokens = int(usage.get('input_tokens', usage.get('prompt_tokens', 0)) or 0)
        output_tokens = int(usage.get('output_tokens', usage.get('completion_tokens', 0)) or 0)
        total_tokens = int(usage.get('total_tokens', input_tokens + output_tokens) or 0)
        normalized = {
            'input_tokens': input_tokens,
            'output_tokens': output_tokens,
            'total_tokens': total_tokens,
        }
        details = usage.get('input_tokens_details') or usage.get('prompt_tokens_details')
        cached_tokens = details.get('cached_tokens') if isinstance(details, dict) else None
        if cached_tokens is None:
            cached_tokens = usage.get('cached_input_tokens', usage.get('cache_read_input_tokens'))
        if cached_tokens is not None:
            normalized['cached_input_tokens'] = int(cached_tokens or 0)
        return normalized
