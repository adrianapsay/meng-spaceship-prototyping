"""Write the assembly as STEP (for CAD tools) and GLB (for the web viewer)."""

from __future__ import annotations

from pathlib import Path

from build123d import Compound, export_gltf, export_step

from cad.build import Part


def export(parts: list[Part], out_dir: Path) -> dict[str, str]:
    out_dir.mkdir(parents=True, exist_ok=True)
    assembly = Compound(children=[p.shape for p in parts], label="assembly")
    step, glb = out_dir / "model.step", out_dir / "model.glb"
    export_step(assembly, step)
    export_gltf(assembly, glb, binary=True, linear_deflection=0.1, angular_deflection=0.2)
    return {"step": step.name, "glb": glb.name}
