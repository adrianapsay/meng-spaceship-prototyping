"""HTTP client for the CAD service."""

from __future__ import annotations

from typing import Any

import httpx


class CADClient:
    def __init__(self, base_url: str, http: httpx.AsyncClient | None = None) -> None:
        self.http = http or httpx.AsyncClient(base_url=base_url.rstrip("/"), timeout=120)

    async def schema(self) -> dict[str, Any]:
        return await self._get("/schema")

    async def catalog(self) -> dict[str, Any]:
        return await self._get("/components")

    async def build(self, spec: Any, artifact_dir: str) -> dict[str, Any]:
        """Build and check a spec; STEP/GLB go to artifact_dir on the shared volume."""
        res = await self.http.post("/build", json={"spec": spec, "artifact_dir": artifact_dir})
        res.raise_for_status()
        return res.json()

    async def _get(self, path: str) -> dict[str, Any]:
        res = await self.http.get(path)
        res.raise_for_status()
        return res.json()
