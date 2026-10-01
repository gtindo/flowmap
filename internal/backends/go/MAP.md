# `internal/backends/go` — Go Semantic Backend

## Responsibility

This package is Flowmap's Go language backend under the `internal/backends/` hierarchy. It contains all `go/packages`, AST, type-checking, SSA, CHA, and VTA analysis used to produce a language-neutral `semantic.Snapshot`.

## Files

| File | Responsibility |
|---|---|
| `backend.go` | Backend orchestration, package/SSA loading, local symbol collection, stable IDs, source text, and call/dependency relationships |
| `facts.go` | Syntax/type-backed effect and external-call facts for downstream Flowmap classification |
| `contracts.go` | Go signature rendering and conversion of named struct/interface boundaries into semantic contracts |
| `diagnostics.go` | Deduplicated package-load diagnostics and reproduction text used for fatal partial-load failures |
| `toolchain.go` | Active toolchain inspection (`GOVERSION`, `GOROOT`, `GOMODCACHE`) and the compatibility guard between Flowmap's build-time Go version and the target's toolchain |
| `*_test.go` | Stable-ID, exact-relationship, unresolved dynamic call, dependency-source, diagnostics, interface, and toolchain characterization coverage |
| `testdata/` | Small modules for dynamic dispatch and dynamic calls without analyzed callees |

## Pipeline

```text
semantic.AnalysisRequest
  -> active Go toolchain inspection and compatibility check
  -> packages.Load("./...", tests enabled), stripping function bodies from GOROOT and GOMODCACHE sources
  -> forgive soft dependency type errors caused only by stripped bodies
  -> retain healthy package variants and report failed variants
  -> build SSA for analyzed packages; dependencies contribute types only
  -> collect named functions, methods, and closures
  -> extract signatures, contracts, documentation, source, exported-callable visibility, and semantic facts
  -> build static SSA calls, VTA interface and function-value call candidates, and passed or returned syntax dependencies
  -> mark non-builtin dynamic calls without call-graph candidates as effect-unknown external calls
  -> semantic.Snapshot
```

Dependency bodies are deliberately not analyzed: on Observy this cut a warm analysis from about 3s and 2.5GB RSS to under 1s and 0.8GB with unchanged symbols and classifications. VTA therefore cannot follow values through dependency code, such as a local `http.ResponseWriter` wrapper passed through `net/http` back into local handlers, so those possible edges are absent; dynamic calls left without candidates become effect-unknown so purity is never inferred from missing evidence.

Production functions duplicated in test-augmented package variants are discarded. Established stable IDs, source locations, relationship ordering, and diagnostics are preserved. Flowmap purity/edge classification does not belong in this package.

## Boundary

Compiler objects remain private to this package and never appear in `internal/semantic`. The backend does not import server, HTTP, browser, or public JSON representations. Adding a future backend would reuse the semantic contract and analyzer enrichment, but no additional backend or discovery mechanism is implemented today.
