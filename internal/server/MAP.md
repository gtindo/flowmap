# `internal/server` — Package Map

## Responsibility

This package is the HTTP adapter over the Flowmap engine protocol and the reference protocol client. It opens one engine workspace per configured project, mirrors view state from engine notifications, translates browser requests into snapshot-addressed protocol queries, and translates protocol models back into the browser's snake_case, path-based JSON. It also owns API routing, embedded static assets, OpenTelemetry HTTP handler wrapping, request logs, and graceful network lifecycle. It holds no analysis indexes.

## Files

| File | Responsibility |
|---|---|
| `server.go` | `App`, workspace opening, routes, OpenTelemetry HTTP wrapping, request logging, project-scoped handlers, `Scan` (start analysis and await its notification), notification-driven view state, paging helpers, and protocol-error to HTTP-status mapping |
| `convert.go` | Pure protocol → browser model translation (URIs to paths, camelCase to the `analyzer` JSON models, load reports for CLI warnings) |
| `server_test.go` | API, static asset, rescan, concurrency, summary, and error-mapping coverage against a real in-process engine session |
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

Handlers resolve a project and language from `project=<name>&language=<language>` to an engine view and its current snapshot id, then issue `symbol/search`, `graph/neighborhood`, `symbol/get`, `changes/list`, or `symbol/summary`. A single-language project resolves omitted names for backwards compatibility. The adapter keeps the HTTP API's historical coercion of graph depth and direction because the protocol rejects invalid values. JSON errors use a small `{ "error": ... }` envelope. The returned handler is wrapped with OpenTelemetry HTTP instrumentation and telemetry-enabled structured request logging.

## Rescan Flow

```text
POST /api/projects/{name}/scan or POST /api/rescan?project={name}
  -> analysis/start for that view (AnalysisAlreadyRunning -> 409)
  -> await analysis/published or analysis/failed (request cancellation -> analysis/cancel)
  -> page through diagnostics/list and changes/list for the published snapshot
  -> return function count, load report, and Git snapshot
```

The engine permits one analysis per view. Registry projects start unscanned, and a failed scan is isolated to that view while its previous snapshot stays queryable. Notifications can arrive before `Scan` begins waiting, so terminal outcomes are buffered by analysis id.

## Summary Flow

Summaries are available only when the engine advertises the `symbolSummary` capability; otherwise the endpoint returns `501`. Provider failures surface as engine internal errors and map to `502`. The summarizer and cache live in `internal/engine/`.

## Browser Workbench

The static application searches functions, fetches focused graph neighborhoods, displays contracts/source/Git deltas, and preserves local layout preferences. Its per-project/language public-boundary toggle filters rendered upstream/downstream nodes and edges without changing the fetched graph or search results, while retaining the focused function. It consumes only the local API and is embedded into the Go binary with `embed.FS`.

Changes under `static/` require the existing two-space indentation and before/after screenshots in pull requests when presentation changes.

## Change Guide

- Keep HTTP effects here; put session behavior in `internal/engine/` and graph or classification logic in `internal/analyzer/`.
- New data must come through the protocol: add it to the wire models and engine first, then translate it in `convert.go`.
- Add endpoints in `Handler`, keep response models explicit, and extend `server_test.go`.
- Preserve the OpenTelemetry wrapper and request log middleware around the mux when changing routing.
- Always query by explicit snapshot id; never assume the engine's current snapshot.
- When routes, browser assets, rescan behavior, summary contracts, or file responsibilities change, update this map and the root `MAP.md` if the system-level flow changed.
