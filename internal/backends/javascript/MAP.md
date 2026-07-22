# `internal/backends/javascript` — JavaScript Semantic Backend

## Responsibility

This package reads local JavaScript-family source files and produces a language-neutral semantic snapshot without Node.js or CGO. It covers JavaScript, TypeScript, JSX, and TSX files and resolves only direct local calls and relative-module imports.

## Boundaries

- It excludes dependency, generated, declaration, VCS, and common build-output paths.
- Package imports, dynamic dispatch, path aliases, bundler configuration, and cross-language calls remain external and conservative for classification.
- Source parsing is best-effort: healthy files continue to contribute symbols when a neighboring file cannot be read or parsed.
