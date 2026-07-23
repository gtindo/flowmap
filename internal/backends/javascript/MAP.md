# `internal/backends/javascript` — JavaScript Semantic Backend

## Responsibility

This package reads local JavaScript-family source files and produces a language-neutral semantic snapshot without Node.js or CGO. It covers JavaScript, TypeScript, JSX, and TSX files, extracts functions plus class constructors and methods as callable nodes, and resolves only conservative local calls and relative-module imports.

## Boundaries

- It excludes dependency, generated, declaration, VCS, and common build-output paths.
- Relative ESM namespaces and CommonJS aliases resolve only identified local exports. Local static methods plus `this` and analyzable `super` calls are direct calls. Receivers from local construction or explicit local class syntax become dynamic dependency edges; unknown, structural, generic, union, package, computed, and reflection-like receivers do not gain speculative graph edges.
- Class ownership is represented in stable callable names (`Service.save`), not as a class-diagram graph. Inheritance is retained only for locally analyzable `super` lookup; this backend does not expand polymorphic overrides.
- Package imports, dynamic dispatch, path aliases, bundler configuration, and cross-language calls remain external and conservative for classification.
- Source parsing is best-effort: healthy files continue to contribute symbols when a neighboring file cannot be read or parsed.
