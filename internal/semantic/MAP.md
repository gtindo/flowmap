# `internal/semantic` — Semantic Model

## Responsibility

This package is the language-neutral boundary between repository analysis backends and Flowmap enrichment. It defines plain data structs and a context-first `Backend` interface; it performs no I/O and imports no language toolchain or compiler packages.

## Model

- `AnalysisRequest` carries the repository root and the existing build-tag input.
- `Snapshot` contains callable symbols, relationships, and backend diagnostics.
- `Symbol` carries a stable ID, kind, names, package/namespace, source location and text, documentation, signature, contracts, test metadata, and backend facts.
- `Relationship` carries call or dependency endpoints plus source location, provenance, precision, and dynamic-dispatch metadata.
- `Fact` records semantic evidence that Flowmap classification interprets downstream.

The model includes only capabilities required to represent current Go behavior. It has no LSP abstraction, backend discovery, plugin configuration, or multilingual claim.

## Dependency Rule

Backends may import this package. `internal/analyzer/` may consume it. This package must never import a backend, Flowmap's browser/API models, or compiler-specific types.
