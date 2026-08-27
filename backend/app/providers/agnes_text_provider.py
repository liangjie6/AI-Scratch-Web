from collections.abc import AsyncIterator
from dataclasses import dataclass
from typing import Any, Literal
import json
import logging
import time

import httpx

from ..config import Settings


ThinkingStrength = Literal['low', 'medium', 'high']
THINKING_BUDGET_TOKENS: dict[ThinkingStrength, int] = {
    'low': 1024,
    'medium': 2048,
    'high': 4096,
}
_STOP_SEQUENCES = ['\n---', '\n现在是 **Agnes**', '\n现在是 Agnes']
logger = logging.getLogger(__name__)


class AgnesTextProviderError(RuntimeError):
    pass


@dataclass(frozen=True)
class AgnesStreamDelta:
    kind: Literal['reasoning', 'content']
    text: str


class AgnesTextProvider:
    def __init__(self, settings: Settings, http_client: Any | None = None, provider: str = 'agnes') -> None:
        self.settings = settings
        self.http_client = http_client
        self.provider = provider

    def for_tier(self, tier: str) -> 'AgnesTextProvider':
        return AgnesTextProvider(self.settings, self.http_client, 'deepseek' if tier == 'advanced' else 'agnes')

    def ensure_configured(self) -> None:
        self._api_key()

    def _api_key(self) -> str:
        return (
            self.settings.require_deepseek_api_key()
            if self.provider == 'deepseek'
            else self.settings.require_agnes_api_key()
        )

    def _base_url(self) -> str:
        return self.settings.deepseek_api_base if self.provider == 'deepseek' else self.settings.agnes_api_base

    def _model(self) -> str:
        return self.settings.deepseek_chat_model if self.provider == 'deepseek' else self.settings.agnes_chat_model

    async def stream(self, messages: list[dict[str, str]]) -> AsyncIterator[str]:
        """Stream content only for callers that do not request thinking controls."""
        async for delta in self._stream_openai(messages, enable_thinking=None):
            if delta.kind == 'content':
                yield delta.text

    async def stream_with_reasoning(
        self,
        messages: list[dict[str, str]],
        *,
        thinking_enabled: bool,
        thinking_strength: ThinkingStrength,
        json_output: bool = False,
    ) -> AsyncIterator[AgnesStreamDelta]:
        if thinking_enabled and self.provider == 'agnes':
            async for delta in self._stream_anthropic(messages, thinking_strength):
                yield delta
        else:
            async for delta in self._stream_openai(messages, enable_thinking=False, json_output=json_output):
                yield delta

    async def _stream_openai(
        self,
        messages: list[dict[str, str]],
        *,
        enable_thinking: bool | None,
        json_output: bool = False,
    ) -> AsyncIterator[AgnesStreamDelta]:
        api_key = self._api_key()
        payload: dict[str, Any] = {
            'model': self._model(),
            'messages': messages,
            'stream': True,
            'stop': _STOP_SEQUENCES,
        }
        if enable_thinking is not None and self.provider == 'agnes':
            payload['chat_template_kwargs'] = {'enable_thinking': enable_thinking}
        if self.provider == 'deepseek' and json_output:
            payload['response_format'] = {'type': 'json_object'}
        headers = {
            'Authorization': f'Bearer {api_key}',
            'Content-Type': 'application/json',
        }
        async for delta in self._request_stream(
            'chat/completions', payload, headers, self._parse_openai_lines,
        ):
            yield delta

    async def _stream_anthropic(
        self,
        messages: list[dict[str, str]],
        strength: ThinkingStrength,
    ) -> AsyncIterator[AgnesStreamDelta]:
        api_key = self.settings.require_agnes_api_key()
        system_parts = [item['content'] for item in messages if item['role'] == 'system']
        conversation = [item for item in messages if item['role'] != 'system']
        payload: dict[str, Any] = {
            'model': self.settings.agnes_chat_model,
            'messages': conversation,
            'max_tokens': 32768,
            'stream': True,
            'stop_sequences': _STOP_SEQUENCES,
            'thinking': {
                'type': 'enabled',
                'budget_tokens': THINKING_BUDGET_TOKENS[strength],
            },
        }
        if system_parts:
            payload['system'] = '\n\n'.join(system_parts)
        headers = {
            'x-api-key': api_key,
            'anthropic-version': '2023-06-01',
            'Content-Type': 'application/json',
        }
        async for delta in self._request_stream(
            'messages', payload, headers, self._parse_anthropic_lines,
        ):
            yield delta

    async def _request_stream(
        self,
        endpoint: str,
        payload: dict[str, Any],
        headers: dict[str, str],
        parser: Any,
    ) -> AsyncIterator[AgnesStreamDelta]:
        started_at = time.perf_counter()
        logger.info(
            'ai_provider_request_started provider=%s endpoint=%s payload=%s',
            self.provider, endpoint, json.dumps(payload, ensure_ascii=False, default=str),
        )
        try:
            if self.http_client is None:
                async with httpx.AsyncClient(
                    base_url=self._base_url(),
                    timeout=httpx.Timeout(360, connect=10),
                ) as client:
                    async with client.stream('POST', endpoint, json=payload, headers=headers) as response:
                        logger.info(
                            'ai_provider_response provider=%s endpoint=%s status_code=%s duration_ms=%s',
                            self.provider, endpoint, response.status_code,
                            round((time.perf_counter() - started_at) * 1000),
                        )
                        self._raise_for_status(response.status_code)
                        async for delta in parser(response.aiter_lines()):
                            logger.info('ai_provider_delta provider=%s kind=%s text=%s', self.provider, delta.kind, delta.text)
                            yield delta
            else:
                async with self.http_client.stream(
                    'POST', endpoint, json=payload, headers=headers,
                ) as response:
                    logger.info(
                        'ai_provider_response provider=%s endpoint=%s status_code=%s duration_ms=%s',
                        self.provider, endpoint, response.status_code,
                        round((time.perf_counter() - started_at) * 1000),
                    )
                    self._raise_for_status(response.status_code)
                    async for delta in parser(response.aiter_lines()):
                        logger.info('ai_provider_delta provider=%s kind=%s text=%s', self.provider, delta.kind, delta.text)
                        yield delta
        except AgnesTextProviderError as exc:
            logger.error(
                'ai_provider_failed provider=%s endpoint=%s duration_ms=%s error_type=%s error=%s',
                self.provider, endpoint, round((time.perf_counter() - started_at) * 1000),
                type(exc).__name__, str(exc), exc_info=True,
            )
            raise
        except httpx.HTTPError as exc:
            logger.error(
                'ai_provider_connection_failed provider=%s endpoint=%s duration_ms=%s error_type=%s error=%s',
                self.provider, endpoint, round((time.perf_counter() - started_at) * 1000),
                type(exc).__name__, str(exc), exc_info=True,
            )
            raise AgnesTextProviderError('Agnes connection failed') from exc

    @staticmethod
    def _raise_for_status(status_code: int) -> None:
        if status_code < 200 or status_code >= 300:
            raise AgnesTextProviderError(f'AI provider rejected request with status {status_code}')

    @staticmethod
    async def _parse_openai_lines(lines: AsyncIterator[str]) -> AsyncIterator[AgnesStreamDelta]:
        async for line in lines:
            line = line.strip()
            if not line or line.startswith(':') or line.startswith('event:'):
                continue
            raw = line[5:].strip() if line.startswith('data:') else line
            if raw == '[DONE]':
                return
            try:
                item = json.loads(raw)
            except json.JSONDecodeError as exc:
                raise AgnesTextProviderError('Agnes returned an invalid stream') from exc
            if not isinstance(item, dict) or item.get('error'):
                raise AgnesTextProviderError('Agnes stream failed')
            choices = item.get('choices')
            delta = (
                choices[0].get('delta', {})
                if isinstance(choices, list) and choices and isinstance(choices[0], dict)
                else {}
            )
            if not isinstance(delta, dict):
                continue
            reasoning = delta.get('reasoning_content') or delta.get('reasoning')
            content = delta.get('content')
            if isinstance(reasoning, str) and reasoning:
                yield AgnesStreamDelta('reasoning', reasoning)
            if isinstance(content, str) and content:
                yield AgnesStreamDelta('content', content)

    @staticmethod
    async def _parse_anthropic_lines(lines: AsyncIterator[str]) -> AsyncIterator[AgnesStreamDelta]:
        async for line in lines:
            line = line.strip()
            if not line or line.startswith(':') or line.startswith('event:'):
                continue
            raw = line[5:].strip() if line.startswith('data:') else line
            if raw == '[DONE]':
                return
            try:
                item = json.loads(raw)
            except json.JSONDecodeError as exc:
                raise AgnesTextProviderError('Agnes returned an invalid stream') from exc
            if not isinstance(item, dict) or item.get('type') == 'error' or item.get('error'):
                raise AgnesTextProviderError('Agnes stream failed')
            if item.get('type') != 'content_block_delta':
                continue
            delta = item.get('delta')
            if not isinstance(delta, dict):
                continue
            delta_type = delta.get('type')
            if delta_type == 'thinking_delta':
                text = delta.get('thinking')
                if isinstance(text, str) and text:
                    yield AgnesStreamDelta('reasoning', text)
            elif delta_type == 'text_delta':
                text = delta.get('text')
                if isinstance(text, str) and text:
                    yield AgnesStreamDelta('content', text)
