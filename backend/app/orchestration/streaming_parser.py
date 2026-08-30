from __future__ import annotations

from dataclasses import dataclass, field
import re
from typing import Any


class StreamProtocolError(ValueError):
    pass


@dataclass(frozen=True)
class StreamFrame:
    kind: str
    content: str = ''
    attrs: dict[str, str] = field(default_factory=dict)


_ROOT_TAGS = ('requirements', 'plan', 'manifest', 'resource_op')
_PROGRAM_CHILD_TAGS = ('metadata', 'shared_state')


class UnifiedFrameParser:
    """Parse the tagged provider stream without assuming provider chunk boundaries."""

    def __init__(self) -> None:
        self._buffer = ''
        self._inside_program = False
        self._program_attrs: dict[str, str] = {}

    def feed(self, text: str) -> list[StreamFrame]:
        if text:
            self._buffer += text
        frames: list[StreamFrame] = []
        while True:
            frame = self._drain_one()
            if frame is None:
                return frames
            frames.append(frame)

    def finish(self) -> list[StreamFrame]:
        frames = self.feed('')
        if self._inside_program:
            raise StreamProtocolError('program frame is incomplete')
        if self._buffer.strip():
            raise StreamProtocolError('stream contains an incomplete or unexpected frame')
        self._buffer = ''
        return frames

    def _drain_one(self) -> StreamFrame | None:
        self._buffer = self._buffer.lstrip()
        if not self._buffer:
            return None
        if self._inside_program:
            return self._drain_program()
        if self._buffer.startswith('</'):
            raise StreamProtocolError('unexpected closing tag')
        if self._buffer.startswith('<program'):
            if not self._is_tag_prefix(self._buffer, 'program'):
                raise StreamProtocolError('invalid program opening tag')
            end = self._buffer.find('>')
            if end < 0:
                return None
            opening = self._buffer[:end + 1]
            self._program_attrs = self._attrs(opening[len('<program'): -1])
            self._buffer = self._buffer[end + 1:]
            self._inside_program = True
            return StreamFrame('program_start', attrs=self._program_attrs)
        for tag in _ROOT_TAGS:
            opening = '<' + tag + '>'
            if self._buffer.startswith(opening):
                closing = '</' + tag + '>'
                end = self._buffer.find(closing, len(opening))
                if end < 0:
                    return None
                content = self._buffer[len(opening):end]
                self._buffer = self._buffer[end + len(closing):]
                return StreamFrame(tag, content)
        if self._buffer.startswith('<') and self._incomplete_tag_prefix(self._buffer, (*_ROOT_TAGS, 'program')):
            return None
        raise StreamProtocolError('unexpected top-level output')

    def _drain_program(self) -> StreamFrame | None:
        self._buffer = self._buffer.lstrip()
        if self._buffer.startswith('</program>'):
            self._buffer = self._buffer[len('</program>'):]
            attrs = self._program_attrs
            self._program_attrs = {}
            self._inside_program = False
            return StreamFrame('program_end', attrs=attrs)
        for tag in _PROGRAM_CHILD_TAGS:
            opening = '<' + tag + '>'
            if self._buffer.startswith(opening):
                closing = '</' + tag + '>'
                end = self._buffer.find(closing, len(opening))
                if end < 0:
                    return None
                content = self._buffer[len(opening):end]
                self._buffer = self._buffer[end + len(closing):]
                return StreamFrame(tag, content, dict(self._program_attrs))
        if self._buffer.startswith('<script'):
            if not self._is_tag_prefix(self._buffer, 'script'):
                raise StreamProtocolError('invalid script opening tag')
            opening_end = self._buffer.find('>')
            if opening_end < 0:
                return None
            opening = self._buffer[:opening_end + 1]
            attrs = self._attrs(opening[len('<script'): -1])
            if set(attrs) != {'index', 'total'}:
                raise StreamProtocolError('script opening requires only index and total')
            closing = '</script>'
            end = self._buffer.find(closing, opening_end + 1)
            if end < 0:
                return None
            content = self._buffer[opening_end + 1:end]
            self._buffer = self._buffer[end + len(closing):]
            attrs = {**self._program_attrs, **attrs}
            return StreamFrame('script', content, attrs)
        if self._buffer.startswith('<') and self._incomplete_tag_prefix(self._buffer, (*_PROGRAM_CHILD_TAGS, 'script', '/program')):
            return None
        raise StreamProtocolError('unexpected program child')

    @staticmethod
    def _is_tag_prefix(value: str, tag: str) -> bool:
        prefix = '<' + tag
        if value == prefix or value == prefix + '>':
            return True
        return value.startswith(prefix) and len(value) > len(prefix) and value[len(prefix)] in ' \t\r\n'

    @staticmethod
    def _incomplete_tag_prefix(value: str, tags: tuple[str, ...]) -> bool:
        if '>' in value or not value.startswith('<'):
            return False
        return any(tag.startswith(value[1:]) for tag in tags)

    @staticmethod
    def _attrs(value: str) -> dict[str, str]:
        attrs: dict[str, str] = {}
        remaining = value.strip()
        pattern = re.compile(r"([A-Za-z_][A-Za-z0-9_-]*)\s*=\s*(?:\"([^\"]*)\"|'([^']*)'|([^\s]+))")
        while remaining:
            match = pattern.match(remaining)
            if match is None:
                raise StreamProtocolError('invalid tag attribute syntax')
            key = match.group(1)
            if key in attrs:
                raise StreamProtocolError(f'duplicate tag attribute: {key}')
            attrs[key] = next(group for group in match.groups()[1:] if group is not None)
            remaining = remaining[match.end():].lstrip()
        return attrs


def parse_json(content: str) -> Any:
    import json

    value = content.strip()
    try:
        parsed = json.loads(value)
    except json.JSONDecodeError as exc:
        raise StreamProtocolError('frame does not contain valid JSON') from exc
    if not isinstance(parsed, dict):
        raise StreamProtocolError('frame JSON must be an object')
    return parsed
