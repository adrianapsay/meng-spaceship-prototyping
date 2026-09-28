import json
from pathlib import Path

import pytest
from fastapi.testclient import TestClient

from cad import app as app_module

EXAMPLE = Path(__file__).parents[1] / "examples" / "3u_imaging.json"


@pytest.fixture
def client(tmp_path, monkeypatch):
    monkeypatch.setattr(app_module, "ARTIFACTS_ROOT", tmp_path)
    return TestClient(app_module.app)


def test_schema_and_components(client):
    assert "properties" in client.get("/schema").json()
    assert client.get("/components").json()["components"]


def test_build_writes_artifacts_under_root(client, tmp_path):
    spec = json.loads(EXAMPLE.read_text())
    res = client.post("/build", json={"spec": spec, "artifact_dir": "d1/iter_1"})
    body = res.json()
    assert res.status_code == 200 and body["passed"]
    assert body["artifacts"] == {"step": "d1/iter_1/model.step", "glb": "d1/iter_1/model.glb"}
    assert (tmp_path / "d1/iter_1/model.glb").exists()


@pytest.mark.parametrize("bad", ["/etc", "../escape", "a/../../b"])
def test_build_rejects_path_traversal(client, bad):
    spec = json.loads(EXAMPLE.read_text())
    assert client.post("/build", json={"spec": spec, "artifact_dir": bad}).status_code == 400


def test_invalid_spec_is_a_report_not_an_error(client):
    res = client.post("/build", json={"spec": {"name": "x"}})
    assert res.status_code == 200
    assert not res.json()["passed"]
