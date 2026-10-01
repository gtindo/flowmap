# `internal/backends/javascript` — JavaScript Semantic Backend

## Responsibility

This package reads local JavaScript-family source files and produces a language-neutral semantic snapshot without Node.js or CGO. It covers JavaScript, TypeScript, JSX, and TSX files, extracts functions plus class constructors and methods as callable nodes, and resolves only conservative local calls and relative-module imports.

## Boundaries

- Inside a Git work tree it lists files with `git ls-files --cached --others --exclude-standard`, so repository ignore rules apply; outside Git (or when Git lists nothing) it walks the directory. Either way it excludes dependency stores, package-manager runtimes (`.yarn`, `.pnp.cjs`), caches, generated and declaration files, VCS metadata, and common build-output directories.
- Files over 1 MiB or with minified-length average lines are skipped and reported as one `skipped` diagnostic per reason instead of being analyzed.
- Relative ESM namespaces and CommonJS aliases resolve only identified local exports. Local static methods plus `this` and analyzable `super` calls are direct calls. Receivers from local construction or explicit local class syntax become dynamic dependency edges; unknown, structural, generic, union, package, computed, and reflection-like receivers do not gain speculative graph edges.
- Class ownership is represented in stable callable names (`Service.save`), not as a class-diagram graph. Inheritance is retained only for locally analyzable `super` lookup; this backend does not expand polymorphic overrides.
- Public-boundary metadata marks ESM/CommonJS exports and local re-exports, plus public constructors and methods of exported classes; private/protected and unexported-class members remain internal.
- Package imports, dynamic dispatch, path aliases, bundler configuration, and cross-language calls remain external and conservative for classification.
- Source parsing is best-effort: healthy files continue to contribute symbols when a neighboring file cannot be read or parsed; an extraction panic becomes that file's `parse` diagnostic.

## Performance Model

Extraction is linear in source size. Each file is masked once and indexed once (line starts, semicolons, and matched brace pairs), so per-symbol lookups never rescan the file. File reading, extraction, and module-syntax scanning run on a bounded worker pool, as does per-record call resolution; order-sensitive module linking runs serially and results are folded in path order so output stays deterministic. Workers stop when the context is cancelled. There is no external parser: an earlier `go-typescript-eslint` validation pass was removed because it looped forever on JSX text and its result was unused.
