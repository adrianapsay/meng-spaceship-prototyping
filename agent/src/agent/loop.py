"""The design loop: the model proposes an AssemblySpec, the CAD service builds
and checks it, and the model revises until it passes."""

from __future__ import annotations

from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Any, Protocol

from agent.llm import OnRetry, tool_calls
from agent.prompt import TOOL_BUILD, TOOL_FINALIZE, system_prompt, tool_result, tools

MAX_NUDGES = 2

Emit = Callable[[str, dict[str, Any]], Awaitable[None]]


class LLM(Protocol):
    async def chat(
        self, messages: list[dict], tools: list[dict], on_retry: OnRetry | None = None
    ) -> dict: ...


class CAD(Protocol):
    async def schema(self) -> dict[str, Any]: ...
    async def catalog(self) -> dict[str, Any]: ...
    async def build(self, spec: Any, artifact_dir: str) -> dict[str, Any]: ...


@dataclass
class Job:
    design_id: str
    prompt: str
    max_iterations: int


@dataclass
class Result:
    passed: bool
    final_iteration: int
    summary: str | None = None
    error: str | None = None


async def run(job: Job, llm: LLM, cad: CAD, emit: Emit) -> Result:
    """Run one design to completion, reporting progress through emit."""

    async def on_retry(attempt: int, wait: float, reason: str) -> None:
        await emit("agent.retrying", {"attempt": attempt, "wait_seconds": wait, "reason": reason})

    builds = nudges = last_passing = 0
    last_passed = False

    def stop(reason: str) -> Result:
        # A passing design is kept rather than discarded because of a later
        # failure (e.g. a provider outage).
        if not last_passing:
            return Result(passed=False, final_iteration=builds, error=reason)
        return Result(
            passed=True,
            final_iteration=last_passing,
            summary=f"Stopped early ({brief(reason)}). "
            f"Kept iteration {last_passing}, the latest passing design.",
        )

    tool_defs = tools(await cad.schema())
    messages: list[dict] = [
        {"role": "system", "content": system_prompt(await cad.catalog(), job.max_iterations)},
        {"role": "user", "content": job.prompt},
    ]

    for _ in range(3 * job.max_iterations + MAX_NUDGES + 2):
        try:
            reply = await llm.chat(messages, tool_defs, on_retry)
        except Exception as e:  # provider errors end the run, keeping any passing design
            return stop(f"llm: {e}")
        messages.append(reply)
        if reply.get("content"):
            await emit("agent.message", {"text": reply["content"]})

        calls = tool_calls(reply)
        if not calls:
            if last_passed:
                return Result(True, last_passing, summary=reply.get("content") or None)
            nudges += 1
            if nudges > MAX_NUDGES:
                return stop("model stopped calling tools")
            messages.append(
                {
                    "role": "user",
                    "content": f"Continue: call {TOOL_BUILD} with a complete AssemblySpec.",
                }
            )
            continue

        final_summary: str | None = None
        for call in calls:
            if call.name == TOOL_BUILD:
                if builds >= job.max_iterations:
                    result = "Build budget exhausted."
                else:
                    builds += 1
                    await emit("iteration.started", {"n": builds})
                    try:
                        report = await cad.build(call.arguments, f"{job.design_id}/iter_{builds}")
                    except Exception as e:
                        return stop(f"cad: {e}")
                    await emit(
                        "iteration.completed",
                        {"n": builds, "spec": call.arguments, "report": report},
                    )
                    last_passed = bool(report.get("passed"))
                    if last_passed:
                        last_passing = builds
                    result = tool_result(report, job.max_iterations - builds)
            elif call.name == TOOL_FINALIZE:
                if not last_passed:
                    result = "Cannot finalize: the latest build has errors. Fix them and rebuild."
                else:
                    args = call.arguments if isinstance(call.arguments, dict) else {}
                    final_summary = str(args.get("summary", ""))
                    result = "Design finalized."
            else:
                result = f"Unknown tool {call.name!r}. Available: {TOOL_BUILD}, {TOOL_FINALIZE}."
            messages.append({"role": "tool", "tool_call_id": call.id, "content": result})

        if final_summary is not None:
            return Result(True, last_passing, summary=final_summary or None)
        if builds >= job.max_iterations:
            return stop(f"no passing design after {builds} builds")

    return stop("turn limit reached")


def brief(msg: str) -> str:
    """Shorten an error to its first sentence for user-facing summaries."""
    for sep in (".", "\n"):
        i = msg.find(sep)
        if i > 0:
            msg = msg[:i]
    return msg if len(msg) <= 120 else msg[:120] + "…"
