import asyncio
import json

import httpx
import pytest
from fastapi.testclient import TestClient

from agent import app as app_module
from agent import loop
from agent.llm import ChatClient, LLMError
from agent.prompt import TOOL_BUILD, TOOL_FINALIZE
from agent.providers import Provider, Registry


def call(id_: str, name: str, args: dict) -> dict:
    return {
        "role": "assistant",
        "content": None,
        "tool_calls": [
            {
                "id": id_,
                "type": "function",
                "function": {"name": name, "arguments": json.dumps(args)},
            }
        ],
    }


class ScriptedLLM:
    """Replays fixed assistant replies and records what it was sent."""

    def __init__(self, replies: list[dict], error_at: int = 0) -> None:
        self.replies = list(replies)
        self.seen: list[list[dict]] = []
        self.error_at = error_at  # 1-based call number that raises; 0 = never

    async def chat(self, messages, tools, on_retry=None):
        self.seen.append(list(messages))
        if self.error_at == len(self.seen):
            raise LLMError("503 Service Unavailable: high demand")
        if not self.replies:
            return {"role": "assistant", "content": "done"}
        return self.replies.pop(0)


class FakeCAD:
    """Passes a spec only if it contains "ok": true."""

    def __init__(self) -> None:
        self.dirs: list[str] = []

    async def schema(self):
        return {"type": "object"}

    async def catalog(self):
        return {"components": []}

    async def build(self, spec, artifact_dir):
        self.dirs.append(artifact_dir)
        ok = isinstance(spec, dict) and spec.get("ok") is True
        issues = [] if ok else [{"code": "interference", "message": "a and b overlap by 10 mm^3"}]
        return {"passed": ok, "issues": issues}


def run(replies, max_iterations=4, error_at=0):
    llm, cad, events = ScriptedLLM(replies, error_at), FakeCAD(), []

    async def emit(event, data):
        events.append(event)

    job = loop.Job("00000000-0000-0000-0000-000000000001", "3U imager", max_iterations)
    result = asyncio.run(loop.run(job, llm, cad, emit))
    return result, llm, cad, events


def test_fixes_failing_build_then_finalizes():
    result, llm, cad, events = run(
        [
            call("1", TOOL_BUILD, {"ok": False}),
            call("2", TOOL_BUILD, {"ok": True}),
            call("3", TOOL_FINALIZE, {"summary": "a fine satellite"}),
        ]
    )
    assert result == loop.Result(True, 2, summary="a fine satellite")
    assert cad.dirs[1].endswith("/iter_2")
    assert events.count("iteration.completed") == 2
    # The failing report and remaining budget must reach the model.
    tool_msg = llm.seen[1][-1]
    assert tool_msg["role"] == "tool" and tool_msg["tool_call_id"] == "1"
    assert "overlap by 10 mm^3" in tool_msg["content"] and "3 builds left" in tool_msg["content"]


def test_finalize_rejected_before_passing_build():
    result, llm, _, _ = run(
        [
            call("1", TOOL_FINALIZE, {"summary": "premature"}),
            call("2", TOOL_BUILD, {"ok": True}),
            call("3", TOOL_FINALIZE, {"summary": "real"}),
        ]
    )
    assert result.summary == "real"
    assert "Cannot finalize" in llm.seen[1][-1]["content"]


def test_fails_when_budget_exhausted():
    result, _, cad, _ = run(
        [call("1", TOOL_BUILD, {}), call("2", TOOL_BUILD, {})], max_iterations=2
    )
    assert not result.passed and "no passing design" in result.error
    assert len(cad.dirs) == 2


def test_nudges_then_gives_up_when_model_stops_calling_tools():
    result, llm, _, _ = run([], max_iterations=3)
    assert not result.passed and len(llm.seen) == loop.MAX_NUDGES + 1


def test_provider_outage_after_pass_keeps_last_passing_iteration():
    result, _, _, _ = run(
        [
            call("1", TOOL_BUILD, {"ok": True}),
            call("2", TOOL_BUILD, {"ok": False}),
            call("3", TOOL_BUILD, {"ok": False}),
        ],
        max_iterations=5,
        error_at=4,
    )
    assert result.passed and result.final_iteration == 1
    assert "503" in result.summary


def chat_client(handler) -> ChatClient:
    return ChatClient(
        "https://llm.test/v1",
        "key",
        "m",
        http=httpx.AsyncClient(transport=httpx.MockTransport(handler)),
    )


def test_chat_retries_rate_limit_and_reports_it(monkeypatch):
    monkeypatch.setattr(asyncio, "sleep", _no_sleep)
    responses = [
        httpx.Response(429, json={"error": {"message": "slow down"}}),
        httpx.Response(
            200, json={"choices": [{"message": {"role": "assistant", "content": "hi"}}]}
        ),
    ]
    retries = []

    async def on_retry(attempt, wait, reason):
        retries.append((attempt, reason))

    reply = asyncio.run(chat_client(lambda req: responses.pop(0)).chat([], [], on_retry))
    assert reply["content"] == "hi"
    assert retries == [(1, "429 Too Many Requests")]


def test_daily_quota_fails_fast_with_readable_message():
    calls = []

    def handler(req):
        calls.append(req)
        return httpx.Response(
            429,
            json=[{"error": {"message": "Quota exceeded: limit 20", "details": "PerDay"}}],
        )

    with pytest.raises(LLMError, match="^429 Too Many Requests: Quota exceeded: limit 20$"):
        asyncio.run(chat_client(handler).chat([], []))
    assert len(calls) == 1


def test_runs_endpoint_streams_events_ending_in_run_finished(monkeypatch):
    llm = ScriptedLLM(
        [call("1", TOOL_BUILD, {"ok": True}), call("2", TOOL_FINALIZE, {"summary": "s"})]
    )
    llm.http = httpx.AsyncClient()
    monkeypatch.setattr(
        app_module, "registry", Registry([Provider("fake", "http://x", "", "m")], "fake")
    )
    monkeypatch.setattr(app_module, "cad", FakeCAD())
    monkeypatch.setattr(app_module, "ChatClient", lambda *a, **k: llm)

    client = TestClient(app_module.app)
    body = {"design_id": "00000000-0000-0000-0000-000000000001", "prompt": "1U demo"}
    with client.stream("POST", "/runs", json=body) as res:
        events = [json.loads(line) for line in res.iter_lines() if line]
    assert [e["event"] for e in events] == [
        "iteration.started",
        "iteration.completed",
        "run.finished",
    ]
    assert events[1]["data"]["spec"] == {"ok": True}
    assert events[-1]["data"] == {
        "passed": True,
        "final_iteration": 1,
        "summary": "s",
        "error": None,
    }

    res = client.post("/runs", json={**body, "provider": "nope"})
    assert res.status_code == 400


async def _no_sleep(_):
    return None
