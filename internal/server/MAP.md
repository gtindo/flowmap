# `internal/server` — Package Map

## Responsibility

This package is the HTTP adapter over the Flowmap engine protocol and the reference protocol client. It opens one engine workspace per configured project, reads view state from the engine with `workspace/get` (caching none of it), translates browser requests into snapshot-addressed protocol queries, and translates protocol models back into the browser's snake_case, path-based JSON. It also owns API routing, embedded static assets, OpenTelemetry HTTP handler wrapping, request logs, and graceful network lifecycle. It holds no analysis indexes.

## Files

| File | Responsibility |
|---|---|
| `server.go` | `App`, workspace opening, routes, OpenTelemetry HTTP wrapping, request logging, project-scoped handlers, `Scan` (start analysis and await its notification), `workspace/get`-backed view state and status mapping, paging helpers, and protocol-error to HTTP-status mapping |
| `convert.go` | Pure protocol → browser model translation (URIs to paths, camelCase to the `analyzer` JSON models, load reports for CLI warnings) |
| `files.go` | File explorer edge and models: `.gitignore`-aware project file listing (Git, or a filtered directory walk outside Git) and pure grouping of symbol summaries under root-relative files |
| `server_test.go` | API, static asset, rescan, concurrency, summary, and error-mapping coverage against a real in-process engine session |
| `files_test.go` | File listing, grouping, and `/api/files` coverage |
| `static/index.html` | Workbench document structure and controls |
| `static/app.js` | API client, graph state, rendering, interaction, rescan, changes, file explorer drawer, and browser persistence |
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
GET  /api/files
GET  /api/graph
GET  /api/functions/{id}
GET  /api/git-status
POST /api/functions/{id}/summary
POST /api/rescan
GET  /*                              embedded static workbench
```

Handlers resolve a project and language from `project=<name>&language=<language>` to an engine view, read its current snapshot from `workspace/get`, then issue `symbol/search`, `graph/neighborhood`, `symbol/get`, `changes/list`, or `symbol/summary`. `/api/files` pages every `symbol/search` result (tests included) and groups the summaries' locations under files listed from the project root; the file listing is a browser-only HTTP edge, not protocol data, because editor clients use their native file trees. A single-language project resolves omitted names for backwards compatibility. The adapter keeps the HTTP API's historical coercion of graph depth and direction because the protocol rejects invalid values. JSON errors use a small `{ "error": ... }` envelope. The returned handler is wrapped with OpenTelemetry HTTP instrumentation and telemetry-enabled structured request logging.

## Rescan Flow

```text
POST /api/projects/{name}/scan or POST /api/rescan?project={name}
  -> analysis/start for that view (AnalysisAlreadyRunning -> 409)
  -> await analysis/published or analysis/failed (request cancellation -> analysis/cancel)
  -> page through diagnostics/list and changes/list for the published snapshot
  -> return function count, load report, and Git snapshot
```

The engine permits one analysis per view. Registry projects start unscanned, and a failed scan is isolated to that view while its previous snapshot stays queryable. Notifications can arrive before `Scan` begins waiting, so terminal outcomes are buffered by analysis id. Notifications only wake scan waiters; `/api/projects` status comes from `workspace/get`.

## Summary Flow

Summaries are available only when the engine advertises the `symbolSummary` capability; otherwise the endpoint returns `501`. Provider failures surface as engine internal errors and map to `502`. The summarizer and cache live in `internal/engine/`.

## Browser Workbench

The static application searches functions, fetches focused graph neighborhoods, displays contracts/source/Git deltas, and preserves local layout preferences. Its per-project/language public-boundary toggle filters rendered upstream/downstream nodes and edges without changing the fetched graph or search results, while retaining the focused function. A hideable left file explorer drawer (`Ctrl/Cmd+B`) loads `/api/files` once per published snapshot, builds a compacted folder tree in the browser, renders children only for expanded nodes, filters test functions by the Tests toggle, marks Git changes, focuses the graph on a selected function, and reveals the current graph root. It consumes only the local API and is embedded into the Go binary with `embed.FS`.

Changes under `static/` require the existing two-space indentation and before/after screenshots in pull requests when presentation changes.

## Change Guide

- Keep HTTP effects here; put session behavior in `internal/engine/` and graph or classification logic in `internal/analyzer/`.
- New data must come through the protocol: add it to the wire models and engine first, then translate it in `convert.go`.
- Add endpoints in `Handler`, keep response models explicit, and extend `server_test.go`.
- Preserve the OpenTelemetry wrapper and request log middleware around the mux when changing routing.
- Always query by explicit snapshot id; never assume the engine's current snapshot.
- When routes, browser assets, rescan behavior, summary contracts, or file responsibilities change, update this map and the root `MAP.md` if the system-level flow changed.
