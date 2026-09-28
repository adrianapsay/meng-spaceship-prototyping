"""`cad build spec.json [-o out/]`: build and validate a spec without the API."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from cad.service import build_and_validate


def main() -> None:
    parser = argparse.ArgumentParser(prog="cad")
    sub = parser.add_subparsers(dest="command", required=True)
    build = sub.add_parser("build", help="build, validate and export an AssemblySpec")
    build.add_argument("spec", type=Path)
    build.add_argument("-o", "--out", type=Path, default=Path("out"))
    args = parser.parse_args()

    report = build_and_validate(json.loads(args.spec.read_text()), args.out)
    for issue in report["issues"]:
        print(f"[{issue['severity']}] {issue['code']}: {issue['message']}")
    if "metrics" in report:
        print(json.dumps(report["metrics"], indent=2))
    if "artifacts" in report:
        print(f"wrote {', '.join(str(args.out / f) for f in report['artifacts'].values())}")
    print("PASSED" if report["passed"] else "FAILED")
    sys.exit(0 if report["passed"] else 1)
