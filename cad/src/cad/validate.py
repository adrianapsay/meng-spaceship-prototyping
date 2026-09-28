"""Engineering checks on a built assembly.

Every issue carries concrete numbers (overlap volume, gap, exceedance) so the
agent can correct its spec rather than guess.
"""

from __future__ import annotations

from dataclasses import asdict, dataclass, field
from itertools import combinations
from typing import Any, Literal

from build123d import BoundBox

from cad.build import BUS_ID, Part
from cad.components import SolarPanel
from cad.spec import PROTRUSION_MM, AssemblySpec

OVERLAP_TOL_MM3 = 1.0
CONTACT_TOL_MM = 0.5


@dataclass
class Issue:
    code: str
    message: str
    parts: list[str] = field(default_factory=list)
    severity: Literal["error", "warning"] = "error"


def _r(v: float) -> float:
    return round(float(v), 2)


def _bbox(b: BoundBox) -> dict[str, list[float]]:
    return {"min": [_r(v) for v in b.min], "max": [_r(v) for v in b.max]}


def _corners(b: BoundBox) -> tuple[tuple[float, ...], tuple[float, ...]]:
    return tuple(b.min), tuple(b.max)


def _boxes_overlap(a: BoundBox, b: BoundBox) -> bool:
    (amin, amax), (bmin, bmax) = _corners(a), _corners(b)
    return all(amin[i] < bmax[i] and bmin[i] < amax[i] for i in range(3))


def check_interference(parts: list[Part]) -> list[Issue]:
    issues = []
    boxes = {p.id: p.shape.bounding_box() for p in parts}
    for a, b in combinations(parts, 2):
        if not _boxes_overlap(boxes[a.id], boxes[b.id]):
            continue
        overlap = a.shape.intersect(b.shape)
        volume = sum(s.volume for s in overlap) if overlap else 0.0
        if volume > OVERLAP_TOL_MM3:
            region = _bbox(overlap[0].bounding_box()) if len(overlap) == 1 else None
            where = f" in region {region}" if region else ""
            issues.append(
                Issue(
                    "interference",
                    f"{a.id} and {b.id} overlap by {_r(volume)} mm^3{where}.",
                    [a.id, b.id],
                )
            )
    return issues


def check_envelope(spec: AssemblySpec, parts: list[Part]) -> list[Issue]:
    bus = spec.bus
    lo = (-bus.x_mm / 2 - PROTRUSION_MM, -bus.y_mm / 2 - PROTRUSION_MM, 0.0)
    hi = (bus.x_mm / 2 + PROTRUSION_MM, bus.y_mm / 2 + PROTRUSION_MM, bus.z_mm)
    issues = []
    for p in parts:
        if p.deployable or p.id == BUS_ID:
            continue
        bmin, bmax = _corners(p.shape.bounding_box())
        excess = []
        for i, axis in enumerate("XYZ"):
            if bmin[i] < lo[i] - 1e-6:
                excess.append(f"-{axis} by {_r(lo[i] - bmin[i])} mm")
            if bmax[i] > hi[i] + 1e-6:
                excess.append(f"+{axis} by {_r(bmax[i] - hi[i])} mm")
        if excess:
            issues.append(
                Issue(
                    "envelope",
                    f"{p.id} exceeds the stowed {bus.size_u}U envelope on "
                    f"{', '.join(excess)} (allowed x {_r(lo[0])}..{_r(hi[0])}, "
                    f"y {_r(lo[1])}..{_r(hi[1])}, z 0..{_r(hi[2])}). "
                    "Only deployed parts may extend beyond it.",
                    [p.id],
                )
            )
    return issues


def check_attachment(parts: list[Part]) -> list[Issue]:
    """Every part must touch the bus, directly or through a chain of parts."""
    attached = [parts[0]]
    pending = parts[1:]
    progress = True
    while pending and progress:
        progress = False
        for p in list(pending):
            if any(p.shape.distance_to(q.shape) <= CONTACT_TOL_MM for q in attached):
                attached.append(p)
                pending.remove(p)
                progress = True
    issues = []
    for p in pending:
        gap, nearest = min((p.shape.distance_to(q.shape), q.id) for q in attached)
        issues.append(
            Issue(
                "floating",
                f"{p.id} is not mounted: nearest attached part is {nearest}, "
                f"{_r(gap)} mm away. Move it into contact.",
                [p.id],
            )
        )
    return issues


def mass_properties(parts: list[Part]) -> tuple[float, tuple[float, float, float]]:
    total = sum(p.mass_kg for p in parts)
    centers = [tuple(p.shape.center()) for p in parts]
    cg = tuple(
        sum(p.mass_kg * c[i] for p, c in zip(parts, centers, strict=True)) / total for i in range(3)
    )
    return total, cg


def check_mass(spec: AssemblySpec, total: float, cg: tuple[float, ...]) -> list[Issue]:
    bus = spec.bus
    issues = []
    if total > bus.mass_limit_kg:
        issues.append(
            Issue(
                "mass",
                f"Total mass {_r(total)} kg exceeds the {bus.size_u}U limit of "
                f"{bus.mass_limit_kg} kg by {_r(total - bus.mass_limit_kg)} kg.",
            )
        )
    offsets = [cg[i] - bus.center[i] for i in range(3)]
    over = [
        f"{axis} offset {_r(offsets[i])} mm (limit ±{bus.cg_limit_mm[i]})"
        for i, axis in enumerate("xyz")
        if abs(offsets[i]) > bus.cg_limit_mm[i]
    ]
    if over:
        issues.append(
            Issue(
                "center_of_mass",
                f"Center of mass is off the bus geometric center: {'; '.join(over)}. "
                "Rebalance heavy components.",
            )
        )
    return issues


def check_mission(spec: AssemblySpec) -> list[Issue]:
    """Every spacecraft needs power generation, storage, a computer and a radio."""
    types = {c.type for c in spec.components}
    required = {
        "solar_panel": "no solar panels: the spacecraft cannot generate power",
        "battery": "no battery: no power during eclipse",
        "electronics_stack": "no electronics stack: no flight computer or power system",
    }
    issues = [Issue("missing_subsystem", msg) for t, msg in required.items() if t not in types]
    if not types & {"patch_antenna", "whip_antenna"}:
        issues.append(Issue("missing_subsystem", "no antenna: no communications"))
    return issues


def validate(spec: AssemblySpec, parts: list[Part]) -> dict[str, Any]:
    total, cg = mass_properties(parts)
    issues = (
        check_interference(parts)
        + check_envelope(spec, parts)
        + check_attachment(parts)
        + check_mass(spec, total, cg)
        + check_mission(spec)
    )
    panels = [c for c in spec.components if isinstance(c, SolarPanel)]
    everything = parts[0].shape.bounding_box()
    for p in parts[1:]:
        everything = everything.add(p.shape.bounding_box())
    return {
        "passed": not any(i.severity == "error" for i in issues),
        "issues": [asdict(i) for i in issues],
        "metrics": {
            "part_count": len(parts),
            "total_mass_kg": _r(total),
            "mass_limit_kg": spec.bus.mass_limit_kg,
            "cg_mm": [_r(v) for v in cg],
            "bbox_mm": _bbox(everything),
            "solar_area_cm2": _r(sum(p.area_m2() for p in panels) * 1e4),
            "peak_power_w": _r(sum(p.peak_power_w() for p in panels)),
        },
        "bus": spec.bus.describe(),
        "parts": [
            {
                "id": p.id,
                "type": p.type,
                "mass_kg": round(p.mass_kg, 3),
                "deployable": p.deployable,
                "bbox_mm": _bbox(p.shape.bounding_box()),
            }
            for p in parts
        ],
    }
