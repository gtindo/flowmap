# `internal/engine` — Package Map

## Responsibility

This package implements one Flowmap engine protocol session. It owns session lifecycle, the workspace and language-view registry, analysis scheduling and cancellation, immutable snapshot publication and retention, snapshot-addressed queries over `analyzer.Index`, and opt-in symbol summaries with their cache. It is the only owner of analysis indexes at runtime; HTTP and editor clients reach them through the protocol.

## Files

| File | Responsibility |
|---|---|
| `engine.go` | `Engine`, `Options`, session state, workspaces, views, analysis start/cancel/commit, snapshot retention, and shutdown |
| `serve.go` | `Serve` read loop, JSON-RPC envelope validation, lifecycle routing, concurrent request handlers, and response/notification writing |
| `handlers.go` | Method table and per-method parameter validation for workspace, analysis, query, and summary requests |
| `convert.go` | Pure `analyzer` → `protocol` model conversion, position parsing, stable search ordering, and self-validating pagination cursors |
| `summarizer.go` | Opt-in command summarizer and content-addressed user-cache storage |
| `inprocess.go` | `StartInProcess`: engine plus initialized client over in-memory pipes, and protocol `Close` |
| `engine_test.go` | Wire-level, lifecycle, snapshot, pagination, cancellation, and summary coverage |

## Session Flow

```text
Serve
  -> initialize (read loop, exactly once)
  -> workspace/open: validate file: root and view specs, issue opaque ids
  -> analysis/start: register active analysis, respond, then launch goroutine
       -> analysis/progress
       -> Options.Analyze (default analyzer.Analyze)
       -> commit under the session lock: cancelled | failed | publish snapshot
       -> analysis/published or analysis/failed
  -> queries resolve (viewId, snapshotId) to an immutable retained snapshot
  -> shutdown (read loop): cancel analyses, wait for handlers, close workspaces
  -> exit or end of input
```

## Boundaries and Invariants

- One active analysis per view; cancellation and publication are ordered by the session lock.
- A failed or cancelled analysis never replaces the current snapshot.
- Each view retains its current snapshot and immediate predecessor; older snapshots return `SnapshotUnavailable`.
- A per-view emit lock keeps each view's notifications in commit order, and the `analysis/start` response is written before its notifications.
- Standard output is protocol-only; operator diagnostics go to `Options.Logger`.
- The engine never substitutes the current snapshot for a requested one.

## Change Guide

- Add a method by defining its models in `internal/protocol/`, a handler in `handlers.go`, conversions in `convert.go`, and tests in `engine_test.go`.
- Keep analysis decisions in `internal/analyzer/`; this package schedules and serves them.
- When lifecycle, retention, files, or responsibilities change, update this map, the TDD, and the root `MAP.md`.
