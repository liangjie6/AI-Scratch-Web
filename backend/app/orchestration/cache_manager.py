from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
import threading
import time
from typing import Any, TypeVar


T = TypeVar('T')


@dataclass(frozen=True)
class CacheEntry:
    value: Any
    expires_at: float


class PromptCache:
    """Small process-local cache for the stable prompt layers."""

    def __init__(self, clock: Callable[[], float] | None = None) -> None:
        self._clock = clock or time.monotonic
        self._entries: dict[str, CacheEntry] = {}
        self._lock = threading.RLock()

    def get(self, key: str) -> Any | None:
        with self._lock:
            entry = self._entries.get(key)
            if entry is None:
                return None
            if entry.expires_at <= self._clock():
                del self._entries[key]
                return None
            return entry.value

    def get_or_build(self, key: str, builder: Callable[[], T], ttl: float = 300) -> tuple[T, bool]:
        with self._lock:
            entry = self._entries.get(key)
            now = self._clock()
            if entry is not None and entry.expires_at > now:
                return entry.value, True
            if entry is not None:
                del self._entries[key]
            value = builder()
            self._entries[key] = CacheEntry(value=value, expires_at=now + max(0, ttl))
            return value, False

    def set(self, key: str, value: T, ttl: float = 300) -> T:
        with self._lock:
            self._entries[key] = CacheEntry(value=value, expires_at=self._clock() + max(0, ttl))
        return value

    def invalidate(self, key_prefix: str | None = None) -> None:
        with self._lock:
            if key_prefix is None:
                self._entries.clear()
            else:
                for key in tuple(self._entries):
                    if key.startswith(key_prefix):
                        del self._entries[key]

    def clear(self) -> None:
        self.invalidate()

    def __len__(self) -> int:
        with self._lock:
            return len(self._entries)


class CacheManager(PromptCache):
    pass
