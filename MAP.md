# Flowmap — Codebase Map

## Purpose

This file is the starting point for code exploration. It describes the repository's major responsibilities and directs readers to focused package maps; it is not a formal architecture decision record.

## Overview

Flowmap is a local code-reading workbench for Go repositories. It loads a target module with the Go toolchain, builds a typed function-level call graph, classifies functions by their relationship to side effects, overlays local Git changes, and serves the result through an embedded browser UI.

The design follows a functional-core, imperative-shell boundary:

- `internal/analyzer/` owns deterministic models, classification, graph assembly, and queries, while keeping toolchain, filesystem, and Git access explicit.
- `cmd/flowmap/` owns command-line and process lifecycle effects.
- `internal/server/` owns HTTP, subprocess summarization, cache, and browser integration effects.

## Project Structure

```text
flowmap/
├── cmd/flowmap/             # CLI entry point and startup orchestration
├── internal/analyzer/       # Go loading, call graph, classification, queries, and Git deltas
│   └── testdata/            # Fixture modules for loader and compatibility behavior
├── internal/server/         # Local HTTP API, embedded web app, rescans, and summaries
│   └── static/              # Browser workbench and PWA assets
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

Loads the target module with `go/packages`, constructs SSA and local call relationships, extracts contracts and documentation, classifies functions, captures Git changes, and exposes immutable search and graph queries. See [`internal/analyzer/MAP.md`](internal/analyzer/MAP.md).

### `internal/server/` — Local Workbench

Publishes the analysis through JSON endpoints, atomically replaces indexes during rescans, optionally invokes a user-provided summarizer command, and serves the embedded browser application. See [`internal/server/MAP.md`](internal/server/MAP.md).

## Main Data Flow

```text
CLI arguments
  -> analyzer.Config
  -> Go package loading and toolchain check
  -> SSA functions and call relationships
  -> classification, contracts, load report, and Git snapshot
  -> immutable analyzer.Index
  -> server.App atomic index pointer
  -> JSON API
  -> embedded browser workbench
```

A rescan repeats the analysis beside the active index and swaps the completed replacement atomically, so concurrent readers never observe partially rebuilt state.

## Exploration Guide

- For loading, graph accuracy, classifications, contracts, diagnostics, or change detection, start in `internal/analyzer/`.
- For flags, startup failures, cancellation, or top-level wiring, start in `cmd/flowmap/`.
- For endpoints, rescan concurrency, summary providers, caching, or UI behavior, start in `internal/server/`.
- For toolchain compatibility behavior, also inspect `scripts/compatibility-smoke.sh` and the analyzer fixture modules.
- For release packaging, inspect `Makefile`, `scripts/release.sh`, and `.github/workflows/`.

## Map Maintenance

Update this map and the relevant package maps whenever code changes alter responsibilities, data flow, package boundaries, entry points, or the role of an important file.
