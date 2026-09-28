import copy
import json
from pathlib import Path

import pytest

from cad.components import PatchAntenna
from cad.service import build_and_validate, catalog
from cad.spec import FACE_ROTATIONS, llm_schema

EXAMPLE = Path(__file__).parents[1] / "examples" / "3u_imaging.json"


@pytest.fixture
def spec():
    return json.loads(EXAMPLE.read_text())


def component(spec, cid):
    return next(c for c in spec["components"] if c["id"] == cid)


def codes(report):
    return [i["code"] for i in report["issues"] if i["severity"] == "error"]


def test_reference_design_passes_and_exports(spec, tmp_path):
    report = build_and_validate(spec, tmp_path)
    assert report["passed"], report["issues"]
    assert (tmp_path / "model.step").stat().st_size > 0
    assert (tmp_path / "model.glb").stat().st_size > 0
    assert report["metrics"]["part_count"] == len(spec["components"]) + 1
    # Deployed wings extend the overall bounding box well past the bus.
    assert report["metrics"]["bbox_mm"]["max"][0] == pytest.approx(350)


@pytest.mark.parametrize(
    "face,axis,sign",
    [
        ("+X", 0, 1),
        ("-X", 0, -1),
        ("+Y", 1, 1),
        ("-Y", 1, -1),
        ("+Z", 2, 1),
        ("-Z", 2, -1),
    ],
)
def test_face_rotations_point_local_z_outward(face, axis, sign):
    patch = PatchAntenna(
        id="p", type="patch_antenna", position_mm=[0, 0, 0], rotation_deg=FACE_ROTATIONS[face]
    )
    bb = patch.shape().bounding_box()
    lo, hi = tuple(bb.min), tuple(bb.max)
    extent = hi[axis] if sign > 0 else -lo[axis]
    assert extent == pytest.approx(6, abs=1e-6)


def test_overlapping_parts_are_reported(spec):
    component(spec, "battery")["position_mm"] = [0, 0, 130]
    report = build_and_validate(spec)
    assert not report["passed"]
    clash = next(i for i in report["issues"] if i["code"] == "interference")
    assert set(clash["parts"]) == {"avionics", "battery"}


def test_stowed_part_outside_envelope_fails_until_deployed(spec):
    wing = component(spec, "wing_px")
    wing["deployed"] = False
    assert "envelope" in codes(build_and_validate(spec))
    wing["deployed"] = True
    assert "envelope" not in codes(build_and_validate(spec))


def test_floating_part_reports_gap(spec):
    component(spec, "star_tracker")["position_mm"] = [0, 0, 238.5]
    report = build_and_validate(spec)
    issue = next(i for i in report["issues"] if i["code"] == "floating")
    assert issue["parts"] == ["star_tracker"]
    assert "10.0 mm" in issue["message"]


def test_mass_limit(spec):
    spec["bus_size_u"] = 1
    spec["components"] = [
        {"id": f"batt_{i}", "type": "battery", "capacity_wh": 150, "position_mm": [0, 0, 0]}
        for i in range(3)
    ]
    assert "mass" in codes(build_and_validate(spec))


def test_center_of_mass_offset(spec):
    heavy = copy.deepcopy(component(spec, "battery"))
    heavy.update(id="ballast", capacity_wh=150, position_mm=[0, 50, 150], rotation_deg=[-90, 0, 0])
    spec["components"].append(heavy)
    assert "center_of_mass" in codes(build_and_validate(spec))


@pytest.mark.parametrize(
    "mutate,needle",
    [
        (
            lambda s: s["components"].append(
                {"id": "x", "type": "warp_drive", "position_mm": [0, 0, 0]}
            ),
            "warp_drive",
        ),
        (lambda s: s["components"].append(copy.deepcopy(s["components"][0])), "duplicate"),
        (lambda s: s.update(bus_size_u=4), "bus_size_u"),
    ],
)
def test_invalid_specs_return_readable_issues(spec, mutate, needle):
    mutate(spec)
    report = build_and_validate(spec)
    assert not report["passed"]
    assert codes(report) and set(codes(report)) == {"invalid_spec"}
    assert any(needle in i["message"] for i in report["issues"])


def test_llm_schema_is_self_contained():
    text = json.dumps(llm_schema())
    assert "$ref" not in text and "$defs" not in text
    assert "solar_panel" in text


def test_catalog_lists_every_component():
    types = {c["type"] for c in catalog()["components"]}
    assert {"camera_payload", "solar_panel", "reaction_wheel", "whip_antenna"} <= types


def test_missing_subsystem_is_an_error(spec):
    spec["components"] = [c for c in spec["components"] if "antenna" not in c["type"]]
    report = build_and_validate(spec)
    assert not report["passed"]
    assert "missing_subsystem" in codes(report)
