# Flowmap — Codebase Map

## Purpose

This file is the starting point for code exploration. It describes the repository's major responsibilities and directs readers to focused package maps; it is not a formal architecture decision record.

## Overview

Flowmap is a local code-reading workbench for Go and JavaScript/TypeScript repositories. Built-in language backends emit language-neutral semantic snapshots; Flowmap then builds a function-level graph, classifies functions by their relationship to side effects, overlays local Git changes, and serves the result through an embedded browser UI.

The design follows a functional-core, imperative-shell boundary:

- `internal/semantic/` defines the backend contract and language-neutral semantic facts.
- `internal/backends/go/` owns Go toolchain, package loading, compiler, and call-graph effects; `internal/backends/javascript/` owns standalone JS/TS syntax analysis.
- `internal/analyzer/` owns deterministic semantic enrichment, classification, graph assembly, Git attribution, and queries.
- `cmd/flowmap/` owns command-line and process lifecycle effects.
- `internal/server/` owns HTTP, subprocess summarization, cache, and browser integration effects.
- `internal/telemetry/` owns optional OpenTelemetry SDK/exporter setup at process startup.

## Project Structure

```text
flowmap/
├── cmd/flowmap/             # CLI entry point and startup orchestration
├── internal/semantic/       # Language-neutral backend interface and semantic facts
├── internal/backends/       # Built-in language backend implementations
│   └── go/                  # Go loading, compiler analysis, and semantic extraction
├── internal/analyzer/       # Flowmap enrichment, classification, queries, and Git deltas
│   └── testdata/            # Fixture modules for loader and compatibility behavior
├── internal/server/         # Local HTTP API, embedded web app, rescans, and summaries
│   └── static/              # Browser workbench and PWA assets
├── internal/telemetry/      # Optional OpenTelemetry traces, metrics, and log export setup
├── scripts/                 # Compatibility and release automation
├── docs/                    # Documentation-site configuration
├── captures/                # README screenshots
├── README.md                # Product and contributor overview
├── USER_GUIDE.md            # Installation and usage documentation
├── Makefile                 # Build, format, lint, test, and release targets
└── go.mod                   # Module and Go toolchain requirements
```

## Core Components

### `cmd/flowmap/` — Process Shell

Parses `serve` and `version`, validates flags, runs the initial analysis, configures optional command-backed summaries, and owns signal-aware server startup. See [`cmd/flowmap/MAP.md`](cmd/flowmap/MAP.md).

### `internal/analyzer/` — Analysis Engine

Transforms a complete semantic snapshot into the existing `Index`, classifies functions, captures Git changes, and exposes immutable search and graph queries. See [`internal/analyzer/MAP.md`](internal/analyzer/MAP.md).

### `internal/semantic/` — Backend Contract

Defines plain structs for callable symbols, stable identities, source locations, signatures, contracts, evidence, relationships, and diagnostics, plus the context-first `Backend` interface. It contains no Go compiler types. See [`internal/semantic/MAP.md`](internal/semantic/MAP.md).

### `internal/backends/go/` — Go Semantic Backend

Implements the Go backend with `go/packages`, AST/type information, SSA, CHA, and VTA. The JavaScript backend uses standalone syntax analysis for JS, TS, JSX, and TSX. Both preserve stable IDs and emit semantic facts without Flowmap classification. See [`internal/backends/MAP.md`](internal/backends/MAP.md).

### `internal/server/` — Local Workbench

Publishes the analysis through JSON endpoints, atomically replaces indexes during rescans, optionally invokes a user-provided summarizer command, and serves the embedded browser application. See [`internal/server/MAP.md`](internal/server/MAP.md).

### `internal/telemetry/` — Optional Telemetry Edge

Configures OpenTelemetry providers, OTLP/gRPC exporters, propagation, and the slog bridge when OTLP environment configuration is present.

## Main Data Flow

```text
CLI path or JSON project registry
  -> one or more analyzer.Config values
  -> selected built-in language backend
  -> language-neutral semantic snapshot
  -> Flowmap classification, graph/index assembly, and load report
  -> Git snapshot
  -> immutable analyzer.Index
  -> server.App project registry with per-project atomic index pointers
  -> JSON API
  -> embedded browser workbench
```

A project scan repeats analysis beside that project's active index and swaps the completed replacement atomically, so concurrent readers never observe partially rebuilt state. Configured projects are lazily scanned when selected.

## Exploration Guide

- For the backend interface or semantic vocabulary, start in `internal/semantic/`.
- For Go loading, compiler analysis, IDs, contracts, relationships, or toolchain diagnostics, start in `internal/backends/go/`.
- For Flowmap classification, graph/index assembly, queries, or change detection, start in `internal/analyzer/`.
- For flags, startup failures, cancellation, or top-level wiring, start in `cmd/flowmap/`.
- For endpoints, rescan concurrency, summary providers, caching, or UI behavior, start in `internal/server/`.
- For telemetry startup, OTLP exporter wiring, or structured log bridging, start in `internal/telemetry/`.
- For toolchain compatibility behavior, also inspect `scripts/compatibility-smoke.sh` and the analyzer fixture modules.
- For release packaging, inspect `Makefile`, `scripts/release.sh`, and `.github/workflows/`.

## Map Maintenance

Update this map and the relevant package maps whenever code changes alter responsibilities, data flow, package boundaries, entry points, or the role of an important file.
