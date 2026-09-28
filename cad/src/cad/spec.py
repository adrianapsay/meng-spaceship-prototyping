"""AssemblySpec: the typed contract the agent emits and the CAD engine builds."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Annotated, Any, Literal, Union

from pydantic import BaseModel, ConfigDict, Field, model_validator

from cad.components import COMPONENT_TYPES

BusSize = Literal[1, 2, 3, 6]

WALL_MM = 2.0
STRUCTURE_KG_PER_U = 0.12
MASS_LIMIT_KG_PER_U = 2.0
# Stowed components may protrude this far beyond the X/Y faces (CubeSat Design Spec).
PROTRUSION_MM = 6.5


@dataclass(frozen=True)
class Bus:
    size_u: int
    x_mm: float
    y_mm: float
    z_mm: float
    cg_limit_mm: tuple[float, float, float]

    @property
    def cavity_min(self) -> tuple[float, float, float]:
        return (-self.x_mm / 2 + WALL_MM, -self.y_mm / 2 + WALL_MM, WALL_MM)

    @property
    def cavity_max(self) -> tuple[float, float, float]:
        return (self.x_mm / 2 - WALL_MM, self.y_mm / 2 - WALL_MM, self.z_mm - WALL_MM)

    @property
    def mass_kg(self) -> float:
        return STRUCTURE_KG_PER_U * self.size_u

    @property
    def mass_limit_kg(self) -> float:
        return MASS_LIMIT_KG_PER_U * self.size_u

    @property
    def center(self) -> tuple[float, float, float]:
        return (0.0, 0.0, self.z_mm / 2)

    def describe(self) -> dict[str, Any]:
        hx, hy = self.x_mm / 2, self.y_mm / 2
        return {
            "size_u": self.size_u,
            "outer_mm": {"x": [-hx, hx], "y": [-hy, hy], "z": [0.0, self.z_mm]},
            "cavity_mm": {
                "x": [self.cavity_min[0], self.cavity_max[0]],
                "y": [self.cavity_min[1], self.cavity_max[1]],
                "z": [self.cavity_min[2], self.cavity_max[2]],
            },
            "mass_kg": round(self.mass_kg, 3),
            "mass_limit_kg": self.mass_limit_kg,
            "cg_limit_mm": dict(zip("xyz", self.cg_limit_mm, strict=True)),
        }


BUSES: dict[int, Bus] = {
    1: Bus(1, 100.0, 100.0, 113.5, (20.0, 20.0, 20.0)),
    2: Bus(2, 100.0, 100.0, 227.0, (20.0, 20.0, 45.0)),
    3: Bus(3, 100.0, 100.0, 340.5, (20.0, 20.0, 70.0)),
    6: Bus(6, 226.3, 100.0, 366.0, (45.0, 20.0, 70.0)),
}

# Rotation that points a component's local +Z along each outward face normal.
FACE_ROTATIONS: dict[str, list[float]] = {
    "+X": [0, 90, 0],
    "-X": [0, -90, 0],
    "+Y": [-90, 0, 0],
    "-Y": [90, 0, 0],
    "+Z": [0, 0, 0],
    "-Z": [180, 0, 0],
}

AnyComponent = Annotated[Union[COMPONENT_TYPES], Field(discriminator="type")]  # noqa: UP007


class AssemblySpec(BaseModel):
    model_config = ConfigDict(extra="forbid")

    name: str = Field(max_length=80, description="Short mission/design name.")
    bus_size_u: BusSize = Field(
        description="CubeSat form factor. The bus frame origin is the center of the "
        "bus bottom (-Z) face; the bus spans z = 0 to its height."
    )
    components: list[AnyComponent] = Field(min_length=1, max_length=40)
    rationale: str = Field(
        default="", max_length=1000, description="Why this layout meets the request."
    )

    @model_validator(mode="after")
    def _unique_ids(self) -> AssemblySpec:
        seen: set[str] = set()
        for c in self.components:
            if c.id in seen or c.id == "bus":
                raise ValueError(f"duplicate or reserved component id: {c.id!r}")
            seen.add(c.id)
        return self

    @property
    def bus(self) -> Bus:
        return BUSES[self.bus_size_u]


def llm_schema() -> dict[str, Any]:
    """JSON Schema for AssemblySpec with all $refs inlined.

    Several LLM providers reject $ref/$defs and discriminator keywords in tool
    schemas, so this returns a self-contained schema.
    """
    schema = AssemblySpec.model_json_schema()
    defs = schema.pop("$defs", {})

    def inline(node: Any) -> Any:
        if isinstance(node, dict):
            if "$ref" in node:
                return inline(defs[node["$ref"].rsplit("/", 1)[-1]])
            return {k: inline(v) for k, v in node.items() if k not in ("discriminator", "title")}
        if isinstance(node, list):
            return [inline(v) for v in node]
        return node

    return inline(schema)
