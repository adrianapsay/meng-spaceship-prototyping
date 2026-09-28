"""Turn an AssemblySpec into located, labeled solids."""

from __future__ import annotations

from dataclasses import dataclass

from build123d import Align, Box, Color, Pos, Shape

from cad.spec import WALL_MM, AssemblySpec, Bus

BUS_ID = "bus"
BUS_COLOR = "#adb5bd"


@dataclass
class Part:
    id: str
    type: str
    shape: Shape
    mass_kg: float
    deployable: bool
    color: str


def bus_shape(bus: Bus) -> Shape:
    bottom = (Align.CENTER, Align.CENTER, Align.MIN)
    outer = Box(bus.x_mm, bus.y_mm, bus.z_mm, align=bottom)
    inner = Pos(0, 0, WALL_MM) * Box(
        bus.x_mm - 2 * WALL_MM, bus.y_mm - 2 * WALL_MM, bus.z_mm - 2 * WALL_MM, align=bottom
    )
    return outer - inner


def build_parts(spec: AssemblySpec) -> list[Part]:
    bus = spec.bus
    parts = [Part(BUS_ID, "bus", bus_shape(bus), bus.mass_kg, False, BUS_COLOR)]
    for c in spec.components:
        parts.append(Part(c.id, c.type, c.shape(), c.mass_kg(), c.is_deployable(), c.color))
    for p in parts:
        p.shape.label = p.id
        p.shape.color = Color(p.color)
    return parts
