"""Minimal OpenAI-compatible chat client with tool calling and visible retries.

Messages are plain dicts in the Chat Completions format. The assistant message
is returned exactly as the provider sent it, so provider-specific fields (e.g.
Gemini thought signatures) survive when it is sent back on the next turn.
"""

from __future__ import annotations

import asyncio
import json
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Any

import httpx

OnRetry = Callable[[int, float, str], Awaitable[None]]


class LLMError(RuntimeError):
    pass


@dataclass
class ToolCall:
    id: str
    name: str
    arguments: Any  # parsed JSON, or the raw string if it was malformed


class ChatClient:
    def __init__(
        self,
        base_url: str,
        api_key: str,
        model: str,
        *,
        max_retries: int = 6,
        http: httpx.AsyncClient | None = None,
    ) -> None:
        self.url = base_url.rstrip("/") + "/chat/completions"
        self.api_key = api_key
        self.model = model
        self.max_retries = max_retries
        self.http = http or httpx.AsyncClient(timeout=180)

    async def chat(
        self, messages: list[dict], tools: list[dict], on_retry: OnRetry | None = None
    ) -> dict:
        """Send the conversation and return the model's next assistant message."""
        body = {"model": self.model, "messages": messages, "tools": tools}
        headers = {"Authorization": f"Bearer {self.api_key}"} if self.api_key else {}
        for attempt in range(self.max_retries + 1):
            res = await self.http.post(self.url, json=body, headers=headers)
            if res.status_code == 200:
                choices = res.json().get("choices") or []
                if not choices:
                    raise LLMError(f"response has no choices: {res.text[:300]}")
                return choices[0]["message"]

            reason = f"{res.status_code} {res.reason_phrase}"
            # A daily quota will not recover within any sensible retry window.
            daily_quota = res.status_code == 429 and "PerDay" in res.text
            retryable = (res.status_code == 429 or res.status_code >= 500) and not daily_quota
            if not retryable or attempt == self.max_retries:
                raise LLMError(f"{reason}: {error_message(res)}")
            wait = backoff(attempt, res.headers.get("retry-after"))
            if on_retry:
                await on_retry(attempt + 1, wait, reason)
            await asyncio.sleep(wait)
        raise AssertionError("unreachable")


def tool_calls(message: dict) -> list[ToolCall]:
    calls = []
    for tc in message.get("tool_calls") or []:
        raw = tc["function"].get("arguments") or "{}"
        try:
            args = json.loads(raw)
        except json.JSONDecodeError:
            args = raw  # kept as a string so the agent can report the problem
        calls.append(ToolCall(tc["id"], tc["function"]["name"], args))
    return calls


def backoff(attempt: int, retry_after: str | None) -> float:
    if retry_after and retry_after.isdigit():
        return float(retry_after)
    return min(2.0 * 2**attempt, 30.0)


def error_message(res: httpx.Response) -> str:
    """The human-readable message from an OpenAI-style error body.

    Gemini wraps the error object in a list; OpenAI does not.
    """
    try:
        body = res.json()
    except ValueError:
        return res.text[:500]
    if isinstance(body, list) and body:
        body = body[0]
    if isinstance(body, dict) and isinstance(body.get("error"), dict):
        return body["error"].get("message") or res.text[:500]
    return res.text[:500]
