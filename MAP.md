# Flowmap — Codebase Map

## Purpose

This file is the starting point for code exploration. It describes the repository's major responsibilities and directs readers to focused package maps; it is not a formal architecture decision record.

## Overview

Flowmap is a local code-reading workbench for Go and JavaScript/TypeScript repositories. Built-in language backends emit language-neutral semantic snapshots; Flowmap then builds a function-level graph, classifies functions by their relationship to side effects, overlays local Git changes, and publishes immutable snapshots through the experimental engine protocol (JSON-RPC over stdio, see `docs/tdd/0001-flowmap-engine-protocol.md`). The embedded browser UI is served by an HTTP adapter that is itself a protocol client; editor integrations launch `flowmap engine`.

The design follows a functional-core, imperative-shell boundary:

 - `internal/semantic/` defines the backend contract and language-neutral semantic facts, including callable visibility.
- `internal/backends/go/` owns Go toolchain, package loading, compiler, and call-graph effects; `internal/backends/javascript/` owns standalone JS/TS syntax analysis, including class-owned callable extraction and conservative member-call resolution.
- `internal/analyzer/` owns deterministic semantic enrichment, classification, graph assembly, Git attribution, and queries.
- `internal/protocol/` owns the engine protocol wire models, framing, and Go client.
- `internal/engine/` owns protocol sessions, analysis scheduling, snapshot publication, queries, and subprocess summarization with its cache.
- `cmd/flowmap/` owns command-line and process lifecycle effects.
- `internal/server/` owns the HTTP adapter over the engine protocol and browser integration effects.
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
├── internal/protocol/       # Engine protocol wire models, framing, and Go client
├── internal/engine/         # Engine protocol sessions, snapshots, queries, and summaries
├── internal/server/         # HTTP adapter over the engine protocol and embedded web app
│   └── static/              # Browser workbench and PWA assets
├── internal/telemetry/      # Optional OpenTelemetry traces, metrics, and log export setup
├── skills/                  # Agent skills shipped from this repository
│   └── flowmap-views/       # Claude Code skill: docs + pinned Flowmap graph -> verified flow views
├── scripts/                 # Compatibility, release, and skill installation automation
├── docs/                    # Documentation-site configuration and technical design docs
├── captures/                # README screenshots
├── README.md                # Product and contributor overview
├── USER_GUIDE.md            # Installation and usage documentation
├── Makefile                 # Build, format, lint, test, and release targets
└── go.mod                   # Module and Go toolchain requirements
```

## Core Components

### `cmd/flowmap/` — Process Shell

Parses `serve`, `engine`, and `version`, validates flags, starts an in-process engine session for `serve` or a stdio engine session for `engine`, configures optional command-backed summaries, and owns signal-aware startup and shutdown. See [`cmd/flowmap/MAP.md`](cmd/flowmap/MAP.md).

### `internal/analyzer/` — Analysis Engine

Transforms a complete semantic snapshot into the existing `Index`, classifies functions, captures Git changes, and exposes immutable search and graph queries. See [`internal/analyzer/MAP.md`](internal/analyzer/MAP.md).

### `internal/semantic/` — Backend Contract

Defines plain structs for callable symbols, stable identities, source locations, signatures, contracts, evidence, relationships, and diagnostics, plus the context-first `Backend` interface. It contains no Go compiler types. See [`internal/semantic/MAP.md`](internal/semantic/MAP.md).

### `internal/backends/go/` — Go Semantic Backend

Implements the Go backend with `go/packages`, AST/type information, SSA, CHA, and VTA. The JavaScript backend uses standalone syntax analysis for JS, TS, JSX, and TSX. Both preserve stable IDs and emit semantic facts without Flowmap classification. See [`internal/backends/MAP.md`](internal/backends/MAP.md).

### `internal/protocol/` — Engine Protocol Contract

Defines camelCase wire models, JSON-RPC envelopes and error codes, `Content-Length` framing, `file:` URI conversion, and a Go client with typed method helpers. See [`internal/protocol/MAP.md`](internal/protocol/MAP.md).

### `internal/engine/` — Engine Session

Owns protocol lifecycle, workspaces and language views, one-analysis-per-view scheduling, atomic snapshot publication and retention, snapshot-addressed queries, and opt-in summaries. See [`internal/engine/MAP.md`](internal/engine/MAP.md).

### `internal/server/` — Local Workbench Adapter

Translates the browser's JSON endpoints into engine protocol requests, waits for analysis notifications during scans, and serves the embedded browser application. See [`internal/server/MAP.md`](internal/server/MAP.md).

### `skills/flowmap-views/` — Claude Code Skill

A global agent skill that answers architecture questions by reading a repository's docs and querying a pinned Flowmap release over the local HTTP API. `SKILL.md` holds the method and the code-fallback policy. `scripts/fm` is the only interface the skill uses: it manages one server per repository under `~/.cache/flowmap-skill/` and prints compact, root-relative query results. `scripts/check_view.py` verifies flow views against the graph and renders them, and `reference/view-format.md` defines the view JSON and gap classes. `FLOWMAP_VERSION` pins the release that `scripts/install-skill.sh` downloads, verifies, and installs with the skill. The skill depends on the browser HTTP API, so API changes must update `scripts/fm` and the pinned version together.

### `internal/telemetry/` — Optional Telemetry Edge

Configures OpenTelemetry providers, OTLP/gRPC exporters, propagation, and the slog bridge when OTLP environment configuration is present.

## Main Data Flow

```text
CLI path or JSON project registry
  -> one or more analyzer.Config values
  -> server.App opens one engine workspace per project (one view per language)
  -> analysis/start over the in-process protocol connection
  -> engine runs the selected built-in language backend
  -> language-neutral semantic snapshot
  -> Flowmap classification, graph/index assembly, and load report
  -> Git snapshot
  -> immutable analyzer.Index published as a protocol snapshot
  -> analysis/published notification
  -> snapshot-addressed protocol queries
  -> server adapter's snake_case JSON API
  -> embedded browser workbench

Editor clients: `flowmap engine` -> the same engine session over stdin/stdout.
```

A scan builds a replacement snapshot beside the view's current one and publishes it atomically, so concurrent readers never observe partially rebuilt state. Configured projects are lazily scanned when selected.

## Exploration Guide

- For the backend interface or semantic vocabulary, start in `internal/semantic/`.
- For Go loading, compiler analysis, IDs, contracts, relationships, or toolchain diagnostics, start in `internal/backends/go/`.
- For Flowmap classification, graph/index assembly, queries, or change detection, start in `internal/analyzer/`.
- For the engine protocol wire shape or the Go client, start in `internal/protocol/` and the TDD in `docs/tdd/`.
- For protocol lifecycle, analysis scheduling, snapshot retention, queries, or summary providers and caching, start in `internal/engine/`.
- For flags, startup failures, cancellation, or top-level wiring, start in `cmd/flowmap/`.
- For HTTP endpoints, protocol-to-browser model translation, or UI behavior, start in `internal/server/`.
- For telemetry startup, OTLP exporter wiring, or structured log bridging, start in `internal/telemetry/`.
- For toolchain compatibility behavior, also inspect `scripts/compatibility-smoke.sh` and the analyzer fixture modules.
- For release packaging, inspect `Makefile`, `scripts/release.sh`, and `.github/workflows/`.
- For the Claude Code skill, start in `skills/flowmap-views/SKILL.md`, then `scripts/fm` and `scripts/install-skill.sh`.

## Map Maintenance

Update this map and the relevant package maps whenever code changes alter responsibilities, data flow, package boundaries, entry points, or the role of an important file.
