# `internal/analyzer` — Package Map

## Responsibility

This package turns a Go module into an immutable, evidence-rich function index. It owns package loading, SSA-based graph construction, contract extraction, effect classification, Git change attribution, load diagnostics, and read-only graph queries.

## Files

| File | Responsibility |
|---|---|
| `model.go` | Public analysis data: functions, contracts, classifications, edges, graphs, Git snapshots, and `Index` |
| `analyzer.go` | Analysis pipeline, Go package loading, SSA function collection, stable IDs, call/dependency edge assembly, source and intent extraction |
| `contracts.go` | Rendering signature types and extracting named struct/interface contracts |
| `classify.go` | Authored labels, direct side-effect evidence, known-pure packages, and conservative purity propagation |
| `query.go` | Deterministic symbol search, function lookup, and bounded upstream/downstream graph traversal |
| `git.go` | Non-fatal Git snapshot capture and attribution of `HEAD` diffs or untracked files to current functions |
| `load_diagnostics.go` | Deduplicated package-load failures, deterministic warning text, and reproduction commands |
| `toolchain.go` | Compatibility guard between Flowmap's build-time Go version and the target's active toolchain |
| `*_test.go` | Unit and regression coverage for each analysis stage |
| `testdata/` | Small fixture modules for healthy, partially broken, fully broken, vendored, and compatibility scenarios |

## Analysis Pipeline

`Analyze` is the package entry point:

```text
Config
  -> resolve root and check active toolchain
  -> packages.Load("./...", tests enabled)
  -> collect load diagnostics and retain healthy package variants
  -> build SSA program
  -> collect local named and anonymous functions
  -> build CHA/VTA call graph and local dependency edges
  -> classify functions and propagate provable purity
  -> build immutable lookup/adjacency maps
  -> capture non-fatal Git snapshot
  -> Index
```

Production functions duplicated in test-augmented package variants are discarded. Tests remain indexed but are marked so callers can hide them. Anonymous functions are retained for traversal but excluded from search and Git change lists.

## Classification Model

Authored function documentation containing `Operations (Pure)` or `Side Effect (Edge)` takes precedence. Otherwise the classifier records syntax-visible evidence such as mutation, goroutines, channel sends, and calls into known effectful packages.

Purity is inferred only when a function has no direct effect evidence, no effect-unknown external call, and all analyzed local callees are pure. Anything that cannot meet that proof remains `unknown` with an explanation.

## Graph and Query Model

- Stable function IDs key `Index.Functions` and adjacency maps.
- `call` edges represent local calls, including dynamic dispatch candidates discovered by VTA.
- `dependency` edges represent statically identifiable local function values passed as dependencies.
- `Search` excludes anonymous functions and optionally tests.
- `Focus` returns a deterministic bounded neighborhood with depth clamped to eight.
- The completed `Index` is treated as immutable and is safe for concurrent readers.

## Side-Effect Boundaries

Most transformations in this package are pure over explicit compiler data. The intentional edges are:

- `packages.Load` and active toolchain subprocess execution
- reading source files for function text and spans
- Git subprocesses and repository reads

Git enrichment is deliberately non-fatal. Package-loading errors are fatal only when they prevent any useful analysis; otherwise they are returned in `LoadReport`.

## Change Guide

- Add public browser-facing fields in `model.go`, populate them in the analysis pipeline, and cover their JSON behavior through analyzer/server tests.
- Keep graph assembly deterministic by sorting externally visible collections or deduplicating with stable keys.
- Add loader/toolchain regressions with a focused module under `testdata/` when syntax or environment matters.
- When an analysis stage, model, edge kind, fixture role, or file responsibility changes, update this map and the root `MAP.md` when the top-level flow also changes.
