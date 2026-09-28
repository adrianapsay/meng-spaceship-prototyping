"""Entry points shared by the HTTP API and the CLI."""

from __future__ import annotations

from pathlib import Path
from typing import Any

from pydantic import ValidationError

from cad.build import build_parts
from cad.components import COMPONENT_TYPES
from cad.export import export
from cad.spec import BUSES, FACE_ROTATIONS, AssemblySpec
from cad.validate import validate


def build_and_validate(raw_spec: Any, out_dir: Path | None = None) -> dict[str, Any]:
    """Validate, build, check, and optionally export a spec.

    Schema errors come back as a failed report rather than an exception so the
    agent can read them and fix its spec.
    """
    try:
        spec = AssemblySpec.model_validate(raw_spec)
    except ValidationError as e:
        return {
            "passed": False,
            "issues": [
                {
                    "code": "invalid_spec",
                    "message": f"{'.'.join(str(p) for p in err['loc'])}: {err['msg']}",
                    "parts": [],
                    "severity": "error",
                }
                for err in e.errors()
            ],
        }

    parts = build_parts(spec)
    report = validate(spec, parts)
    if out_dir is not None:
        report["artifacts"] = export(parts, out_dir)
    return report


def catalog() -> dict[str, Any]:
    components = []
    for cls in COMPONENT_TYPES:
        schema = cls.model_json_schema()
        params = {
            name: {k: v for k, v in prop.items() if k != "title"}
            for name, prop in schema["properties"].items()
            if name not in ("id", "type", "position_mm", "rotation_deg")
        }
        type_name = schema["properties"]["type"]["const"]
        components.append(
            {
                "type": type_name,
                "summary": cls.summary,
                "deployable": cls.deployable,
                "params": params,
            }
        )
    return {
        "conventions": {
            "units": "millimetres, degrees, kilograms",
            "bus_frame": "Origin at the center of the bus bottom (-Z) face. The bus is a "
            "2 mm-wall shell; internal parts must sit inside the cavity, external parts "
            "flush against an outer face.",
            "component_frame": "Each component's origin is the center of its mounting "
            "face and its body extends along local +Z.",
            "face_rotations": FACE_ROTATIONS,
            "mounting": "Every part must touch the bus or another mounted part "
            "(<= 0.5 mm gap). Parts may touch but not overlap.",
            "envelope": "Non-deployed parts must stay within the bus outline plus 6.5 mm "
            "on X/Y and within z = 0..height.",
        },
        "buses": [b.describe() for b in BUSES.values()],
        "components": components,
    }
