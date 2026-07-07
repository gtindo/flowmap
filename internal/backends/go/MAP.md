# `internal/backends/go` — Go Semantic Backend

## Responsibility

This package is Flowmap's sole current language backend under the `internal/backends/` hierarchy. It contains all `go/packages`, AST, type-checking, SSA, CHA, and VTA analysis used to produce a language-neutral `semantic.Snapshot`.

## Files

| File | Responsibility |
|---|---|
| `backend.go` | Backend orchestration, package/SSA loading, local symbol collection, stable IDs, source text, and call/dependency relationships |
| `facts.go` | Syntax/type-backed effect and external-call facts for downstream Flowmap classification |
| `contracts.go` | Go signature rendering and conversion of named struct/interface boundaries into semantic contracts |
| `diagnostics.go` | Deduplicated package-load diagnostics and reproduction text used for fatal partial-load failures |
| `toolchain.go` | Compatibility guard between Flowmap's build-time Go version and the target's active toolchain |
| `*_test.go` | Stable-ID, exact-relationship, diagnostics, interface, and toolchain characterization coverage |

## Pipeline

```text
semantic.AnalysisRequest
  -> active Go toolchain compatibility check
  -> packages.Load("./...", tests enabled)
  -> retain healthy package variants and report failed variants
  -> build SSA program
  -> collect named functions, methods, and closures
  -> extract signatures, contracts, documentation, source, and semantic facts
  -> build static SSA calls, VTA interface and function-value call candidates, and passed or returned syntax dependencies
  -> semantic.Snapshot
```

Production functions duplicated in test-augmented package variants are discarded. Established stable IDs, source locations, relationship ordering, and diagnostics are preserved. Flowmap purity/edge classification does not belong in this package.

## Boundary

Compiler objects remain private to this package and never appear in `internal/semantic`. The backend does not import server, HTTP, browser, or public JSON representations. Adding a future backend would reuse the semantic contract and analyzer enrichment, but no additional backend or discovery mechanism is implemented today.
