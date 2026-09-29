"""System prompt and tool definitions for the design agent."""

from __future__ import annotations

import json
from typing import Any

TOOL_BUILD = "build_and_validate"
TOOL_FINALIZE = "finalize"


def tools(spec_schema: dict[str, Any]) -> list[dict[str, Any]]:
    return [
        _tool(
            TOOL_BUILD,
            "Build the assembly in CAD and run engineering checks (interference, envelope, "
            "mounting, mass, center of mass). Returns a report with every issue and each "
            "part's bounding box. The argument is the full AssemblySpec.",
            spec_schema,
        ),
        _tool(
            TOOL_FINALIZE,
            "Finish the design. Only allowed after the latest build passed.",
            {
                "type": "object",
                "properties": {
                    "summary": {
                        "type": "string",
                        "description": "2-4 sentence summary of the final design and how it "
                        "meets the request.",
                    }
                },
                "required": ["summary"],
            },
        ),
    ]


def system_prompt(catalog: dict[str, Any], max_builds: int) -> str:
    return f"""You are a spacecraft mechanical design engineer. Turn the user's mission request into a CubeSat assembly by writing an AssemblySpec and checking it with the {TOOL_BUILD} tool.

How to work:
1. Choose a bus size and the components the mission needs: payload, power (solar panels + battery), avionics (electronics_stack), communications (antenna), and attitude control (reaction wheels, star tracker) where relevant.
2. Compute every position explicitly from the dimensions below. Internal parts sit inside the bus cavity and stack along Z: the first part rests on the cavity floor (z = 2), and each next part starts where the previous one ends. External parts sit flush on an outer face, rotated with the face_rotations table. Deployed solar wings may extend beyond the envelope but must touch the bus.
3. Call {TOOL_BUILD} with the full spec. Read every issue: they include exact overlaps, gaps and exceedances in mm. Fix all errors in one revision and rebuild.
4. When a build passes, call {TOOL_FINALIZE} with a short summary.

Treat every explicit requirement in the request (e.g. "deployable solar panels", a payload type, a power or pointing need) as mandatory. You have {max_builds} builds. Keep the center of mass near the bus center: place heavy parts symmetrically. Prefer a clean, realistic layout over cramming parts in.

Component catalog, frame conventions and bus dimensions (JSON):
{json.dumps(catalog)}"""


def tool_result(report: dict[str, Any], builds_left: int) -> str:
    if report.get("passed"):
        nxt = (
            "All checks passed. Now re-read the user's request and list each explicit "
            "requirement (payload, deployables, power, pointing, etc.). If every one is met, "
            f"call {TOOL_FINALIZE} immediately. Otherwise change the spec to meet it and "
            "rebuild. Never resubmit an identical spec."
        )
    else:
        nxt = f"Fix every error and call {TOOL_BUILD} again ({builds_left} builds left)."
    return json.dumps(report) + "\n\n" + nxt


def _tool(name: str, description: str, parameters: dict[str, Any]) -> dict[str, Any]:
    return {
        "type": "function",
        "function": {"name": name, "description": description, "parameters": parameters},
    }
