# `internal/backends` — Built-in Backend Implementations

## Responsibility

This directory groups Flowmap's built-in language backend implementations. Each backend consumes `internal/semantic.AnalysisRequest` and returns a complete language-neutral `internal/semantic.Snapshot`.

## Backends

- [`go/`](go/MAP.md) is the sole implemented backend and owns all current Go toolchain and compiler analysis.

This hierarchy is an explicit package boundary only. It does not provide plugin discovery, backend configuration, another language, or multilingual product support.
