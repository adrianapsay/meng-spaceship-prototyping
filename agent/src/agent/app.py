"""Internal HTTP API for the agent. Called by the Go API only.

POST /runs streams newline-delimited JSON events while the agent works. The
last event is always "run.finished". If the caller disconnects, the run is
cancelled.
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
from collections.abc import AsyncIterator
from typing import Any

from fastapi import FastAPI, HTTPException
from fastapi.responses import StreamingResponse
from pydantic import BaseModel, Field

from agent import loop
from agent.cad import CADClient
from agent.llm import ChatClient
from agent.providers import Registry, UnknownProviderError

log = logging.getLogger("agent")

app = FastAPI(title="Text-to-Spaceship agent service", version="0.1.0")
registry = Registry.from_env()
cad = CADClient(os.environ.get("CAD_URL", "http://localhost:8000"))


class RunRequest(BaseModel):
    design_id: str = Field(pattern=r"^[0-9a-f-]{36}$")
    prompt: str = Field(min_length=1, max_length=2000)
    provider: str | None = None
    model: str | None = None
    max_iterations: int = Field(6, ge=1, le=12)


@app.get("/healthz")
def healthz() -> dict[str, str]:
    return {"status": "ok"}


@app.get("/providers")
def providers() -> dict[str, Any]:
    return {
        "default": registry.default or "",
        "providers": [{"name": p.name, "default_model": p.default_model} for p in registry.list()],
    }


@app.post("/runs")
async def runs(req: RunRequest) -> StreamingResponse:
    try:
        provider, model = registry.resolve(req.provider, req.model)
    except UnknownProviderError as e:
        raise HTTPException(400, str(e)) from e
    llm = ChatClient(provider.base_url, provider.api_key, model)
    job = loop.Job(req.design_id, req.prompt, req.max_iterations)
    return StreamingResponse(_stream(job, llm), media_type="application/x-ndjson")


async def _stream(job: loop.Job, llm: ChatClient) -> AsyncIterator[str]:
    """Run the agent in a task and forward its events as NDJSON lines."""
    queue: asyncio.Queue[str | None] = asyncio.Queue()

    async def emit(event: str, data: dict[str, Any]) -> None:
        await queue.put(json.dumps({"event": event, "data": data}) + "\n")

    async def work() -> None:
        try:
            result = await loop.run(job, llm, cad, emit)
        except Exception as e:  # e.g. the CAD service is down before the first build
            log.exception("run failed", extra={"design_id": job.design_id})
            result = loop.Result(passed=False, final_iteration=0, error=f"agent: {e}")
        await emit("run.finished", result.__dict__)
        await queue.put(None)

    task = asyncio.create_task(work())
    try:
        while (line := await queue.get()) is not None:
            yield line
    finally:
        task.cancel()  # the caller disconnected, or the run is done
        await llm.http.aclose()
