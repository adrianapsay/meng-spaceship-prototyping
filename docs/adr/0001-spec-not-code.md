# ADR 0001: The agent writes a typed assembly spec, not CAD code

**Status:** accepted · 2026-09-27

## Context
Coding agents can produce single parts with CAD scripts (CadQuery, build123d, OpenSCAD), but multi-part assemblies fail in predictable ways: parts overlap, float in space, or violate envelope and mass constraints. Nothing in a free-form script tells the model *which* constraint it broke or by how much.

## Decision
The LLM emits an **AssemblySpec**: a JSON document that picks components from a parametric library and places each one (position and rotation in a shared bus frame). The Python CAD service owns the schema as a Pydantic model. That model is the single source of truth: the Go orchestrator fetches it (`GET /schema`) and hands it to the model as the tool's input schema.

Deterministic code then builds the geometry (build123d/OpenCascade) and runs checks. Each failure comes back with numbers the model can act on: overlap volume, gap distance, envelope exceedance per axis, mass over the limit, and center-of-mass offset.

## Consequences
- **Good:** every build is valid geometry, and the checks are exact, not judged by an LLM. Feedback is specific enough that models converge in a few iterations. Specs are small, diffable and storable, which sets up evals across models.
- **Cost:** designs are limited to what the library can express. New capability means adding a component class (about 20 lines). A later escape hatch could let the agent author a custom part in a sandbox and register it in the spec.
- **Split:** the CAD engine stays in Python, where the kernel lives. Orchestration, state and streaming live in Go. They talk over HTTP/JSON because the spec is opaque to Go. gRPC becomes worth it if the CAD API grows or needs streaming.
