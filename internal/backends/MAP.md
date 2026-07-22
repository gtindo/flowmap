# `internal/backends` — Built-in Backend Implementations

## Responsibility

This directory groups Flowmap's built-in language backend implementations. Each backend consumes `internal/semantic.AnalysisRequest` and returns a complete language-neutral `internal/semantic.Snapshot`.

## Backends

- [`go/`](go/MAP.md) owns Go toolchain and compiler analysis.
- [`javascript/`](javascript/MAP.md) owns standalone JavaScript, TypeScript, JSX, and TSX syntax analysis without Node.js.

This hierarchy is an explicit package boundary only. It does not provide plugin discovery. Language views are selected by analyzer configuration and remain separate indexes.
