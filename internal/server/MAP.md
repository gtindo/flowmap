# `internal/server` — Package Map

## Responsibility

This package exposes a registry of per-project, per-language analysis indexes as a local HTTP workbench. It owns API routing, embedded static assets, lazy language scans, OpenTelemetry HTTP handler wrapping, request logs, atomic rescans, graceful network lifecycle, optional command-backed summaries, and summary caching outside the analyzed repository.

## Files

| File | Responsibility |
|---|---|
| `server.go` | `App`, project registry constructors, routes, OpenTelemetry HTTP wrapping, request logging, project-scoped HTTP handlers, atomic index replacement, scan serialization, and graceful serving |
| `summarizer.go` | Summary contracts, command subprocess adapter, validation, and content-addressed user-cache storage |
| `server_test.go` | API, static asset, rescan, concurrency, and summary behavior coverage |
| `static/index.html` | Workbench document structure and controls |
| `static/app.js` | API client, graph state, rendering, interaction, rescan, changes, and browser persistence |
| `static/style.css` | Responsive visual system, graph/node layout, and light/dark presentation |
| `static/sw.js` | Service worker and offline asset behavior |
| `static/manifest.webmanifest` | Installable PWA metadata |
| `static/offline.html` | Offline fallback document |
| `static/favicon.svg`, `icon-*.png` | Browser and installed-app icons |

## API Surface

```text
GET  /api/projects
POST /api/projects/{name}/scan
POST /api/projects/{name}/languages/{language}/scan
GET  /api/search
GET  /api/graph
GET  /api/functions/{id}
GET  /api/git-status
POST /api/functions/{id}/summary
POST /api/rescan
GET  /*                              embedded static workbench
```

Handlers resolve a project and language from `project=<name>&language=<language>` and read that immutable `analyzer.Index` through an atomic pointer. A single-language project resolves omitted names for backwards compatibility. JSON errors use a small `{ "error": ... }` envelope. The returned handler is wrapped with OpenTelemetry HTTP instrumentation and telemetry-enabled structured request logging.

## Rescan Flow

```text
POST /api/projects/{name}/scan or POST /api/rescan?project={name}
  -> reject if that project scan is already active
  -> analyzer.Analyze with that project's original Config
  -> build a complete replacement Index
  -> atomic pointer swap
  -> return function count, load report, and Git snapshot
```

Each project mutex permits one scan at a time. Registry projects start unscanned, and a failed scan is isolated to that project. The atomic swap keeps searches and graph requests available against the previous complete index until its replacement is ready.

## Summary Flow

Summaries are disabled unless the CLI supplies both a `Summarizer` and `SummaryCache`. `CommandSummarizer` sends a minimal JSON request to an explicitly configured shell command and expects a non-empty JSON `summary`. Cache keys include provider identity and the exact request payload, so changed function context or commands do not reuse stale text.

The cache lives under the operating-system user cache directory, never in the analyzed repository.

## Browser Workbench

The static application searches functions, fetches focused graph neighborhoods, displays contracts/source/Git deltas, and preserves local layout preferences. It consumes only the local API and is embedded into the Go binary with `embed.FS`.

Changes under `static/` require the existing two-space indentation and before/after screenshots in pull requests when presentation changes.

## Change Guide

- Keep HTTP and subprocess effects here; put reusable graph or classification logic in `internal/analyzer/`.
- Add endpoints in `Handler`, keep response models explicit, and extend `server_test.go`.
- Preserve the OpenTelemetry wrapper and request log middleware around the mux when changing routing.
- Preserve immutable-index reads and complete-before-swap behavior when changing rescans.
- When routes, browser assets, rescan behavior, summary contracts, or file responsibilities change, update this map and the root `MAP.md` if the system-level flow changed.
