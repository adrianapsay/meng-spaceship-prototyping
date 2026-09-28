"""Internal HTTP API for the CAD engine. Called by the Go orchestrator only."""

from __future__ import annotations

import os
import threading
from pathlib import Path, PurePosixPath
from typing import Any

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

from cad.service import build_and_validate, catalog
from cad.spec import llm_schema

ARTIFACTS_ROOT = Path(os.environ.get("ARTIFACTS_ROOT", "artifacts")).resolve()

# OpenCascade is not reliably thread-safe; serialize builds within a process
# and scale out with more processes if needed.
_build_lock = threading.Lock()

app = FastAPI(title="Text-to-Spaceship CAD service", version="0.1.0")


class BuildRequest(BaseModel):
    spec: Any = Field(description="AssemblySpec JSON; validated by the engine.")
    artifact_dir: str | None = Field(
        default=None,
        description="Relative directory under ARTIFACTS_ROOT to write STEP/GLB into; "
        "omit to validate without exporting.",
    )


def _resolve_artifact_dir(rel: str) -> Path:
    path = PurePosixPath(rel)
    if path.is_absolute() or ".." in path.parts:
        raise HTTPException(400, "artifact_dir must be a relative path without '..'")
    return ARTIFACTS_ROOT / path


@app.get("/healthz")
def healthz() -> dict[str, str]:
    return {"status": "ok"}


@app.get("/schema")
def schema() -> dict[str, Any]:
    return llm_schema()


@app.get("/components")
def components() -> dict[str, Any]:
    return catalog()


@app.post("/build")
def build(req: BuildRequest) -> dict[str, Any]:
    out_dir = _resolve_artifact_dir(req.artifact_dir) if req.artifact_dir else None
    with _build_lock:
        report = build_and_validate(req.spec, out_dir)
    if "artifacts" in report:
        report["artifacts"] = {
            kind: f"{req.artifact_dir}/{name}" for kind, name in report["artifacts"].items()
        }
    return report
