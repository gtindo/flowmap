# `internal/analyzer` — Package Map

## Responsibility

This package turns a language-neutral semantic snapshot into an immutable, evidence-rich Flowmap index. It owns semantic enrichment, effect classification, graph/index construction, Git change attribution, public load-report rendering, and read-only graph queries. Go loading and compiler analysis live behind `internal/semantic.Backend` in `internal/backends/go/`.

## Files

| File | Responsibility |
|---|---|
| `model.go` | Public analysis data: functions, contracts, classifications, edges, graphs, Git snapshots, and `Index` |
| `analyzer.go` | Built-in-backend entry point and pure semantic-to-`Index` translation of symbols, contracts, relationships, source, intent, and diagnostics |
| `classify.go` | Authored labels, direct side-effect evidence, known-pure packages, and conservative purity propagation |
| `query.go` | Deterministic symbol search, function lookup, and bounded upstream/downstream graph traversal |
| `git.go` | Non-fatal Git snapshot capture and attribution of `HEAD` diffs or untracked files to current callables, including owner-qualified JavaScript class methods |
| `git_hierarchy.go` | Pure changed-function reachability, recursive-component collapse, leaf counts, and review ordering |
| `load_diagnostics.go` | Translation and deterministic rendering of backend diagnostics and reproduction commands |
| `*_test.go` | Unit and regression coverage for each analysis stage |
| `testdata/` | Small fixture modules for healthy, partially broken, fully broken, vendored, and compatibility scenarios |

## Analysis Pipeline

`Analyze` remains the package entry point and selects the built-in Go or JavaScript backend from `Config.Language`. `AnalyzeWithBackend` makes the internal backend boundary explicit without adding discovery or plugins:

```text
Config
  -> semantic.AnalysisRequest
  -> semantic.Backend.Analyze
  -> complete semantic.Snapshot
  -> translate symbols, signatures, contracts, diagnostics, and relationships
  -> classify functions and propagate provable purity
  -> build immutable lookup/adjacency maps
  -> capture non-fatal Git snapshot
  -> Index
```

Backend symbol identities pass through unchanged. Tests remain indexed but are marked so callers can hide them. Closures are retained for traversal but excluded from search and Git change lists.

## Classification Model

Authored function documentation containing `Operations (Pure)` or `Side Effect (Edge)` takes precedence. Otherwise the classifier interprets backend facts such as mutation, concurrent work, channel sends, and external package calls.

Purity is inferred only when a function has no direct effect evidence, no effect-unknown external call, and all analyzed local callees are pure. Anything that cannot meet that proof remains `unknown` with an explanation.

## Graph and Query Model

- Stable function IDs key `Index.Functions` and adjacency maps.
- `call` edges represent backend-reported local calls, including dynamic interface and function-value candidates and their precision metadata before translation to the unchanged public edge model.
- `dependency` edges represent statically identifiable local function values passed or returned as dependencies, plus conservative dynamic JavaScript receiver-type relationships.
- `Search` excludes anonymous functions and optionally tests.
- `Focus` returns a deterministic bounded neighborhood with depth clamped to eight.
- Changed-function review order traverses calls and dependencies through unchanged nodes, collapses recursive components, and ranks graph levels by distinct changed leaf descendants.
- The completed `Index` is treated as immutable and is safe for concurrent readers.

## Side-Effect Boundaries

Semantic-to-index transformation and classification are pure over explicit snapshot data. The intentional analyzer edge is Git subprocess/repository access; compiler, toolchain, and source-loading effects are isolated in `internal/backends/go/`.

Git enrichment is deliberately non-fatal. Package-loading errors are fatal only when they prevent any useful analysis; otherwise they are returned in `LoadReport`.

## Change Guide

- Add backend facts in `internal/semantic/`, produce them in the backend, then interpret them here without coupling the snapshot to browser JSON.
- Add public browser-facing fields in `model.go`, populate them during semantic enrichment, and cover their JSON behavior through analyzer/server tests.
- Keep graph assembly deterministic by sorting externally visible collections or deduplicating with stable keys.
- Add loader/toolchain regressions with a focused module under `testdata/` when syntax or environment matters.
- When an analysis stage, model, edge kind, fixture role, or file responsibility changes, update this map and the root `MAP.md` when the top-level flow also changes.
