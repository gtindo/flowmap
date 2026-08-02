# TDD 0001: Flowmap Engine Protocol

- Status: Experimental
- Protocol version: `0`
- Transport: JSON-RPC 2.0 over standard input and standard output
- Reference client: Flowmap's embedded web workbench

## Summary

This document defines an experimental, UI-independent protocol between the
Flowmap analysis engine and visualization clients. The protocol exposes
language views, immutable analysis snapshots, symbols, graph neighborhoods,
Git changes, diagnostics, and optional generated summaries without exposing
Flowmap's internal Go interfaces or prescribing how a client renders them.

Version `0` is intentionally unstable. A client and engine must negotiate the
exact same version during initialization. Backward-compatibility guarantees
begin with a future version `1`; no compatibility between different version
`0` revisions is implied.

## Goals

- Separate the analysis engine from HTTP, browser, IDE, and desktop UI
  concerns.
- Support multiple workspaces and multiple language views in one engine
  process.
- Preserve Flowmap's complete-before-publish and immutable-index behavior.
- Give every query an explicit language view and snapshot so a response never
  combines analysis generations.
- Provide a small transport that editor extensions and local shells can launch
  without requiring a network listener.
- Allow optional features, such as generated summaries, to be discovered by
  capability negotiation.

## Non-goals

Version `0` does not define:

- dynamically discovered or third-party language backend plugins;
- analysis of unsaved editor buffers or in-memory document overlays;
- graph layout, colors, panels, commands, key bindings, or other client-side
  rendering and interaction concepts;
- remote, socket, WebSocket, HTTP, or multiplexed transports;
- collaborative editing, authentication, or authorization;
- snapshot persistence across engine processes;
- compatibility between unequal experimental protocol versions.

## Terminology

**Engine**
: The Flowmap process that loads source, runs a built-in language backend,
  enriches and classifies semantic facts, captures Git state, caches optional
  summaries, and publishes immutable snapshots.

**Client**
: A web adapter, editor extension, IDE integration, desktop shell, or other
  consumer that presents engine data and owns client-specific behavior.

**Workspace**
: One opened repository root, identified on the wire by an opaque
  `workspaceId` and a `file:` URI.

**Language view**
: The independently analyzed representation of one language in a workspace.
  A workspace can have multiple views, and each view has its own analysis and
  snapshot sequence.

**Analysis**
: One attempt to build a replacement snapshot for a language view.

**Snapshot**
: A complete, immutable, queryable generation of one language view. Its
  `snapshotId` is opaque and its numeric `revision` increases within that view.

**Publication**
: The atomic act of making a complete snapshot current for its language view.

## Architecture boundaries

The engine protocol sits outside the current language backend boundary:

```text
client presentation and IDE behavior
              |
       Flowmap protocol
              |
engine session, workspace registry, analysis scheduling, snapshot cache
              |
internal/analyzer: enrichment, classification, Git attribution, queries
              |
internal/semantic.Backend: internal Go interface and semantic facts
              |
internal/backends/go and internal/backends/javascript
```

The engine owns source parsing and loading, semantic extraction, Flowmap
classification, Git analysis, optional summary caching, analysis scheduling,
and immutable graph snapshots. Built-in language backends remain internal Go
implementations of `internal/semantic.Backend`; the protocol is not a backend
plugin API.

Clients own presentation, graph layout, navigation, editor integration,
commands, preferences, and all IDE-specific behavior. Protocol models describe
semantic data only and must not acquire browser, VS Code, or desktop rendering
concepts.

This boundary follows the current package maps:
[`internal/semantic/MAP.md`](../../internal/semantic/MAP.md) defines
language-neutral backend facts,
[`internal/analyzer/MAP.md`](../../internal/analyzer/MAP.md) builds immutable
indexes and serves deterministic queries, and
[`internal/server/MAP.md`](../../internal/server/MAP.md) owns the reference HTTP
and browser edge. The proposed protocol does not change those current runtime
responsibilities by itself.

## Transport and framing

The engine reads JSON-RPC 2.0 messages from standard input and writes JSON-RPC
2.0 messages to standard output. Both streams use UTF-8. Each message is framed
with an ASCII `Content-Length` header containing the exact number of bytes in
the UTF-8 JSON payload, followed by `\r\n\r\n` and the payload:

```text
Content-Length: 33\r\n
\r\n
{"jsonrpc":"2.0","method":"exit"}
```

Header names are case-insensitive. Version `0` requires exactly one
`Content-Length` header and accepts no transfer encoding; other headers are
ignored. A missing, duplicate, malformed, or out-of-range length terminates the
session after a diagnostic is written to standard error because the stream
cannot be resynchronized safely. After reading a valid frame, invalid UTF-8 or
JSON receives a `Parse error` response with a `null` identifier and does not by
itself terminate the session.

Standard output is exclusively protocol output. Human-readable logs,
telemetry diagnostics, progress intended for operators, and crash details go
to standard error. Clients must not attempt to parse standard error as protocol
data.

JSON-RPC request and response identifiers may be strings or integers. Clients
must not reuse an identifier while its request is outstanding. Notifications
have no `id`. Batch requests and batch responses are not supported in version
`0`; a top-level JSON array receives `Invalid Request` (`-32600`).

All protocol object fields use `camelCase`. Receivers must ignore unknown
object fields. Enum values, method names, capability names, and required fields
are not extensible unless this document says otherwise.

## Session lifecycle and version negotiation

The first request must be `initialize`. It is valid exactly once. Before a
successful initialization, the engine rejects every request other than
`initialize` with `InvalidSessionState`; `exit` is always accepted because it
is a notification.

`initialize` parameters are:

| Field | Type | Required | Meaning |
|---|---|---:|---|
| `protocolVersion` | string | yes | Exact requested version; version `0` requires `"0"`. |
| `clientInfo` | `PeerInfo` | yes | Client name and optional version. |
| `requiredCapabilities` | string array | no | Capabilities without which the client cannot operate. |

The result contains the exact `protocolVersion`, `engineInfo`, and
`capabilities`. The engine returns `ProtocolVersionMismatch` when it does not
support the requested exact version. It returns `CapabilityNotSupported` with
the unsupported names in `error.data.capabilities` when any required
capability is absent. A failed initialization leaves the session
uninitialized.

Version `0` defines these engine capabilities:

| Capability | Type | Meaning |
|---|---|---|
| `languages` | string array | Built-in language view names accepted by `workspace/open`. |
| `symbolSummary` | boolean | Whether `symbol/summary` is available. |

Capabilities not listed here are unknown and cannot be required by a version
`0` client.

`shutdown` is a request with no parameters. It stops accepting new workspace,
analysis, and query requests; requests cancellation of active analyses; waits
for in-flight request handlers to finish; closes workspaces; and returns
`null`. The client then sends the `exit` notification. An engine exits with
status zero when `exit` follows a completed `shutdown`, and with a non-zero
status when `exit` arrives first. End-of-file on standard input is equivalent
to `exit` without `shutdown`.

## Workspaces, views, and analysis

### Opening and closing workspaces

`workspace/open` registers one repository root. Its parameters are:

| Field | Type | Required | Meaning |
|---|---|---:|---|
| `rootUri` | string | yes | Absolute `file:` URI for the repository root. |
| `name` | string | no | Client-facing label; it has no identity semantics. |
| `views` | `ViewSpec` array | yes | One or more distinct language views. |

`ViewSpec` contains a required `language` and an optional `buildTags` string
array. `buildTags` is valid only for the `go` view. The built-in JavaScript
backend is selected with language `javascript` and covers JavaScript,
TypeScript, JSX, and TSX source. Duplicate language views, unsupported
languages, non-`file:` roots, relative roots, and language-inapplicable options
are `Invalid params` errors.

Opening a workspace does not start analysis. The result is a `Workspace` whose
views begin in `unscanned` state. Opening the same URI more than once is allowed
and produces independent workspace and view identifiers.

`workspace/close` takes a `workspaceId`, requests cancellation of its active
analyses, invalidates its views and snapshots, and returns `null`. It is an
error to query or start analysis in a closed workspace.

### Starting and cancelling analysis

`analysis/start` takes:

| Field | Type | Required | Meaning |
|---|---|---:|---|
| `viewId` | string | yes | Language view to analyze. |
| `incrementalHint` | `IncrementalHint` | no | Correctness-neutral optimization hint. |

`IncrementalHint` may contain a `baseSnapshotId` and `changedUris`. The engine
may ignore either field and perform a full analysis. A hint must never change
the symbols, edges, classifications, changes, or diagnostics that a full
analysis of the same on-disk state would publish. Unsaved contents are never
included in a hint. Each changed URI must be an absolute `file:` URI beneath
the workspace root or the request is `Invalid params`. An unavailable or
wrong-view base snapshot makes the hint unusable and causes a full analysis;
it does not make the analysis request fail.

The request returns `{ "analysisId": string }` after the work is accepted, not
after it completes. Only one analysis may run for a view. Starting another
returns `AnalysisAlreadyRunning`; analyses for different views may run in
parallel.

`analysis/cancel` takes an `analysisId` and returns
`{ "cancelRequested": boolean }`. Cancellation is best effort. `true` means a
cancellation signal was delivered, not that publication is impossible;
publication may already have committed. `false` means the known analysis had
already reached a terminal state. An unknown analysis identifier returns
`AnalysisNotFound`.

Analysis emits these notifications:

| Notification | Parameters | Meaning |
|---|---|---|
| `analysis/progress` | `AnalysisProgress` | A non-authoritative phase and optional completion estimate. |
| `analysis/published` | `AnalysisPublished` | A complete replacement snapshot was atomically installed. |
| `analysis/failed` | `AnalysisFailure` | The attempt ended without publication, including cancellation. |

Progress carries `analysisId`, `viewId`, `phase`, an optional human-readable
`message`, and optional integer `completed` and `total` work units. Clients
must not derive correctness or lifecycle state from progress messages.

Publication carries `analysisId`, `viewId`, and the new `Snapshot` descriptor.
Failure carries `analysisId`, `viewId`, and a `Failure` with a stable `kind` of
`cancelled`, `loadFailed`, or `internal`, plus a human-readable `message`.
Failures do not invalidate the view's previously published snapshot.

### Publication and retention guarantees

- A language view has at most one analysis in progress.
- The current snapshot remains queryable while its replacement is being built.
- A replacement index is complete before it is published, and publication
  changes the current snapshot atomically.
- A failed or cancelled analysis does not publish a partial snapshot.
- Snapshot data never changes after publication.
- `revision` starts at 1 and increases by one for each successful publication
  in a view. It is informational; `snapshotId` remains the query identity.
- After a newer snapshot is published, the engine may evict any superseded
  snapshot. Queries for an evicted snapshot return `SnapshotUnavailable`.
- Closing a workspace evicts all of its snapshots.
- Snapshot identifiers and revisions are scoped to the engine process and are
  not valid after restart.

## Methods and notifications

| Name | Kind | Parameters | Result |
|---|---|---|---|
| `initialize` | request | initialization object | negotiated version and capabilities |
| `shutdown` | request | none | `null` |
| `exit` | notification | none | none |
| `workspace/open` | request | root and view specs | `Workspace` |
| `workspace/close` | request | `workspaceId` | `null` |
| `analysis/start` | request | view and optional hint | `analysisId` |
| `analysis/cancel` | request | `analysisId` | cancellation acknowledgement |
| `analysis/progress` | notification | `AnalysisProgress` | none |
| `analysis/published` | notification | `AnalysisPublished` | none |
| `analysis/failed` | notification | `AnalysisFailure` | none |
| `symbol/search` | request | `SnapshotQuery`, text, filters, page | symbol summaries page |
| `symbol/get` | request | `SnapshotQuery`, `symbolId` | full `Symbol` |
| `graph/neighborhood` | request | `SnapshotQuery`, root, traversal options | `Graph` |
| `changes/list` | request | `SnapshotQuery`, page | Git state and changes page |
| `diagnostics/list` | request | `SnapshotQuery`, page | load state and diagnostics page |
| `symbol/summary` | request | `SnapshotQuery`, `symbolId` | generated or cached summary |

### Query parameters

Every query includes both `viewId` and `snapshotId` through `SnapshotQuery`.
The engine verifies that the snapshot belongs to the view. A mismatched pair is
`SnapshotUnavailable`; the engine never silently substitutes the current
snapshot.

`symbol/search` additionally accepts `query` (default empty), `includeTests`
(default `false`), and optional `page`. It performs case-insensitive matching
over qualified name and namespace. Anonymous symbols are excluded.

`symbol/get` returns one full symbol. An unknown symbol in an available
snapshot returns `SymbolNotFound`.

`graph/neighborhood` accepts `rootSymbolId`, `direction` (`upstream`,
`downstream`, or `both`), `depth` from 0 through 8, and `includeTests` (default
`false`). Invalid directions and depths are `Invalid params` rather than being
silently coerced.

`changes/list` returns the Git state captured with the snapshot and its changed
symbols in engine-defined review order. `diagnostics/list` returns the load
state captured with the snapshot and its diagnostics.

`symbol/summary` is available only when `capabilities.symbolSummary` is true.
It accepts a symbol from a specific snapshot and returns `summary`, `source`
(`generated`), and `cached`. Authored symbol intent remains part of `Symbol`
and does not require this capability. Calling the method without the capability
returns `CapabilityNotSupported`.

Query results use these exact outer shapes:

| Method | Result object |
|---|---|
| `symbol/search` | `items: SymbolSummary[]`; optional `nextCursor` |
| `symbol/get` | `symbol: Symbol` |
| `graph/neighborhood` | the `Graph` object |
| `changes/list` | `gitState: GitState`, `items: GitChange[]`; optional `nextCursor` |
| `diagnostics/list` | `loadReport: LoadReport`, `items: Diagnostic[]`; optional `nextCursor` |
| `symbol/summary` | `summary`, `source`, `cached` |

### Pagination

Pageable requests use:

```json
{
  "limit": 50,
  "cursor": "opaque-cursor"
}
```

`limit` defaults to 50 and must be between 1 and 200. A page result contains
`items` and omits `nextCursor` when no more results exist. Cursors are opaque,
bound to the method, view, snapshot, query, and filters, and valid only for the
engine process lifetime. An invalid or mismatched cursor is `Invalid params`.

## Shared wire models

The tables below define semantic fields. Unless marked optional, a field is
required. Empty collections are encoded as `[]`, not `null`. Optional fields
are omitted rather than set to `null`.

### Identity and workspace models

`workspaceId`, `viewId`, `snapshotId`, `analysisId`, and `symbolId` are opaque,
non-empty strings. Clients must compare and return them verbatim and must not
parse, construct, or persist meaning from their contents.

| Model | Fields |
|---|---|
| `PeerInfo` | `name`; optional `version` |
| `Workspace` | `workspaceId`, `rootUri`, optional `name`, `views` |
| `LanguageView` | `viewId`, `workspaceId`, `language`, `loadState` |
| `Snapshot` | `snapshotId`, `viewId`, `revision`, `symbolCount`, `edgeCount` |
| `LoadState` | `state`; optional `analysisId`, `currentSnapshotId` |

`LoadState.state` is `unscanned`, `analyzing`, `ready`, or `failed`.
`currentSnapshotId` identifies the most recently published and still current
snapshot, including while another analysis is running or after one fails.

### Locations and symbols

`SourceLocation` has `uri`, `startLine`, and `endLine`. URIs are absolute
`file:` URIs. Lines are one-based and inclusive, matching the source spans
available from current Flowmap analysis; zero is invalid. No machine-specific
path field appears on the wire.

| Model | Fields |
|---|---|
| `SymbolSummary` | `symbolId`, `name`, `qualifiedName`, `namespace`, `language`, `signature`, `classification`, `test` |
| `Symbol` | all summary identity fields plus `kind`, `location`, `parameters`, `results`, `contracts`, `intent`, `intentSource`, `source`, `anonymous`, and `classificationDetail` |
| `Classification` | `kind`, `provenance`, `evidence` |
| `Contract` | `name`, `kind`, `fields`, `methods` |
| `ContractField` | `name`, `type` |

`Symbol.kind` is `function`, `method`, or `closure`.
`Classification.kind` is `pure`, `effect`, or `unknown`. The compact
`classification` field in `SymbolSummary` contains that same enum value;
`classificationDetail` contains the full `Classification` in `Symbol`.
`parameters`, `results`, `evidence`, `fields`, and `methods` are arrays of
strings or their named model. `intent`, `intentSource`, and `source` may be
omitted when unavailable. `test` and `anonymous` are booleans.

### Graph models

| Model | Fields |
|---|---|
| `Graph` | `rootSymbolId`, `nodes`, `edges` |
| `Edge` | `fromSymbolId`, `toSymbolId`, `kind`, `dynamic`; optional `callSite` |

`Graph.nodes` contains full `Symbol` objects. `Edge.kind` is `call` or
`dependency`. `callSite` is a `SourceLocation`. Every edge endpoint and the
graph root must be present in `nodes`.

### Git change models

| Model | Fields |
|---|---|
| `GitState` | `available`, `detached`; optional `branch`, `revision` |
| `GitChange` | `symbolId`, `qualifiedName`, `namespace`, `location`, `test`, `kind`, `diff`, `leafDescendantCount` |

Git data describes the repository state captured during analysis, not live
state at query time. When `available` is false, `branch` and `revision` are
omitted and the changes list is empty. Change `kind` values are engine-defined
strings. Version `0` emits `new` and `updated`; this field is explicitly
extensible, and clients must display unknown values without assigning behavior
to them.

### Diagnostic models

| Model | Fields |
|---|---|
| `LoadReport` | `language`, `totalUnits`, `failedUnits`, `buildTags`, `diagnosticCount` |
| `Diagnostic` | `kind`, `message`, `affectedUnits`; optional `location` |

Diagnostics describe source loading and analysis degradation captured with the
snapshot. `affectedUnits` contains backend-neutral display names for affected
packages or files and must not be interpreted as paths. `location` uses the
same one-based inclusive source model when a precise source span is available.
Diagnostics are data, not JSON-RPC errors: a usable partial analysis may
publish a snapshot with diagnostics.

## Concurrency, ordering, and cancellation

The engine may execute requests concurrently and send responses in any order.
Clients must correlate responses by JSON-RPC `id`, not send order. Notifications
may be interleaved with responses.

Operations against a published snapshot behave as reads of immutable data and
may run concurrently with each other and with analysis. Publication is atomic
with respect to all queries. A query either addresses the requested complete
snapshot or fails with `SnapshotUnavailable`; it never observes an index while
it is being assembled.

Analysis cancellation and shutdown cancellation are best effort. If
cancellation wins before publication, the engine emits `analysis/failed` with
kind `cancelled`. If publication commits first, the engine emits
`analysis/published`, and a later cancellation acknowledgement does not revoke
that snapshot.

## Errors

Errors use the JSON-RPC 2.0 error object. `error.message` is human-readable and
must not be parsed. `error.data`, when present, is an object with relevant
opaque identifiers or validation details.

| Code | Name | Use |
|---:|---|---|
| `-32700` | Parse error | Valid framing contained invalid JSON. |
| `-32600` | Invalid Request | The JSON value is not a supported JSON-RPC request, including a batch. |
| `-32601` | Method not found | The method name is not defined by negotiated version `0`. |
| `-32602` | Invalid params | Required, typed, enum, URI, range, option, pagination, or cursor validation failed. |
| `-32603` | Internal error | An unexpected request-handling failure occurred. |
| `-32001` | ProtocolVersionMismatch | Requested protocol version is not exactly supported. |
| `-32002` | CapabilityNotSupported | A required or invoked optional capability is unavailable. |
| `-32003` | InvalidSessionState | A lifecycle method was called in the wrong session state. |
| `-32004` | WorkspaceNotFound | The workspace identifier is unknown or closed. |
| `-32005` | ViewNotFound | The view identifier is unknown or its workspace is closed. |
| `-32006` | SnapshotUnavailable | The snapshot is unknown, evicted, closed, or belongs to another view. |
| `-32007` | AnalysisAlreadyRunning | The view already has an active analysis. |
| `-32008` | AnalysisNotFound | The analysis identifier was never issued by this session. |
| `-32009` | SymbolNotFound | The symbol is absent from the requested available snapshot. |

Unsupported methods always use `-32601`. A known capability-gated method uses
`-32002` when its capability was not advertised. Unknown fields alone never
produce an error.

## Representative exchanges

The examples omit `Content-Length` headers for readability. Each displayed
object is one independently framed message.

### Initialization

Client request:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "0",
    "clientInfo": {
      "name": "flowmap-web-adapter",
      "version": "0.7.0"
    },
    "requiredCapabilities": []
  }
}
```

Engine response:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": "0",
    "engineInfo": {
      "name": "flowmap",
      "version": "0.7.0"
    },
    "capabilities": {
      "languages": ["go", "javascript"],
      "symbolSummary": true
    }
  }
}
```

### Opening a workspace

Client request:

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "workspace/open",
  "params": {
    "rootUri": "file:///Users/alex/src/acme",
    "name": "acme",
    "views": [
      {
        "language": "go",
        "buildTags": ["integration"]
      },
      {
        "language": "javascript"
      }
    ]
  }
}
```

Engine response:

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "workspaceId": "workspace-7f3a",
    "rootUri": "file:///Users/alex/src/acme",
    "name": "acme",
    "views": [
      {
        "viewId": "view-go-a19c",
        "workspaceId": "workspace-7f3a",
        "language": "go",
        "loadState": {
          "state": "unscanned"
        }
      },
      {
        "viewId": "view-javascript-834e",
        "workspaceId": "workspace-7f3a",
        "language": "javascript",
        "loadState": {
          "state": "unscanned"
        }
      }
    ]
  }
}
```

### Starting and publishing analysis

Client request:

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "analysis/start",
  "params": {
    "viewId": "view-go-a19c"
  }
}
```

Engine response:

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "result": {
    "analysisId": "analysis-42"
  }
}
```

Engine progress notification:

```json
{
  "jsonrpc": "2.0",
  "method": "analysis/progress",
  "params": {
    "analysisId": "analysis-42",
    "viewId": "view-go-a19c",
    "phase": "classifying",
    "message": "Classifying call graph"
  }
}
```

Engine publication notification:

```json
{
  "jsonrpc": "2.0",
  "method": "analysis/published",
  "params": {
    "analysisId": "analysis-42",
    "viewId": "view-go-a19c",
    "snapshot": {
      "snapshotId": "snapshot-go-1-91de",
      "viewId": "view-go-a19c",
      "revision": 1,
      "symbolCount": 418,
      "edgeCount": 967
    }
  }
}
```

### Searching symbols

Client request:

```json
{
  "jsonrpc": "2.0",
  "id": 8,
  "method": "symbol/search",
  "params": {
    "viewId": "view-go-a19c",
    "snapshotId": "snapshot-go-1-91de",
    "query": "analyze",
    "includeTests": false,
    "page": {
      "limit": 20
    }
  }
}
```

Engine response:

```json
{
  "jsonrpc": "2.0",
  "id": 8,
  "result": {
    "items": [
      {
        "symbolId": "github.com/acme/project/analyzer.Analyze",
        "name": "Analyze",
        "qualifiedName": "analyzer.Analyze",
        "namespace": "github.com/acme/project/analyzer",
        "language": "go",
        "signature": "func Analyze(context.Context, Config) (*Index, error)",
        "classification": "effect",
        "test": false
      }
    ]
  }
}
```

### Fetching a graph neighborhood

Client request:

```json
{
  "jsonrpc": "2.0",
  "id": 9,
  "method": "graph/neighborhood",
  "params": {
    "viewId": "view-go-a19c",
    "snapshotId": "snapshot-go-1-91de",
    "rootSymbolId": "github.com/acme/project/analyzer.Analyze",
    "direction": "downstream",
    "depth": 1,
    "includeTests": false
  }
}
```

Engine response:

```json
{
  "jsonrpc": "2.0",
  "id": 9,
  "result": {
    "rootSymbolId": "github.com/acme/project/analyzer.Analyze",
    "nodes": [
      {
        "symbolId": "github.com/acme/project/analyzer.Analyze",
        "name": "Analyze",
        "qualifiedName": "analyzer.Analyze",
        "namespace": "github.com/acme/project/analyzer",
        "language": "go",
        "signature": "func Analyze(context.Context, Config) (*Index, error)",
        "kind": "function",
        "location": {
          "uri": "file:///Users/alex/src/acme/internal/analyzer/analyzer.go",
          "startLine": 25,
          "endLine": 52
        },
        "parameters": ["context.Context", "Config"],
        "results": ["*Index", "error"],
        "contracts": [],
        "source": "func Analyze(ctx context.Context, config Config) (*Index, error) { ... }",
        "test": false,
        "anonymous": false,
        "classification": "effect",
        "classificationDetail": {
          "kind": "effect",
          "provenance": "inferred",
          "evidence": ["loads repository source"]
        }
      },
      {
        "symbolId": "github.com/acme/project/analyzer.classify",
        "name": "classify",
        "qualifiedName": "analyzer.classify",
        "namespace": "github.com/acme/project/analyzer",
        "language": "go",
        "signature": "func classify([]Function) []Function",
        "kind": "function",
        "location": {
          "uri": "file:///Users/alex/src/acme/internal/analyzer/classify.go",
          "startLine": 18,
          "endLine": 70
        },
        "parameters": ["[]Function"],
        "results": ["[]Function"],
        "contracts": [],
        "test": false,
        "anonymous": false,
        "classification": "pure",
        "classificationDetail": {
          "kind": "pure",
          "provenance": "authored",
          "evidence": []
        }
      }
    ],
    "edges": [
      {
        "fromSymbolId": "github.com/acme/project/analyzer.Analyze",
        "toSymbolId": "github.com/acme/project/analyzer.classify",
        "kind": "call",
        "dynamic": false,
        "callSite": {
          "uri": "file:///Users/alex/src/acme/internal/analyzer/analyzer.go",
          "startLine": 43,
          "endLine": 43
        }
      }
    ]
  }
}
```

Responses to requests `8` and `9` may arrive in either order if both are
outstanding.

## Client and server mappings

### Reference HTTP server and web workbench

The existing `internal/server/` package is the reference integration. Its
project registry maps to protocol workspaces, each per-language atomic index
maps to a language view's current snapshot, and its rescan mutex maps to the
one-analysis-per-view rule. Existing search, graph, function, Git-status, load
diagnostic, and summary handlers can act as an HTTP adapter over engine
operations. The embedded web workbench remains a presentation client of that
adapter and is the reference for useful behavior, not for protocol wire shape.

The current HTTP API may retain its snake_case JSON for compatibility. The
adapter is responsible for translating it to the protocol's camelCase models
and URI-based locations.

### VS Code and other IDE clients

A VS Code extension can launch one engine subprocess, open one workspace per
repository folder, create the language views it supports, and render query
results using native editor navigation and webviews. Other IDE integrations can
use the same lifecycle and stdio transport while choosing their own native or
embedded presentation. No client sends unsaved buffer contents in version `0`;
analysis observes the filesystem.

### Future desktop shell

A desktop application can supervise the same engine subprocess and build its
own windows, graph renderer, menus, and persistence around protocol identities.
The desktop shell does not receive privileged semantic APIs and must not depend
on internal Go types.

## Compatibility and evolution

Version `0` uses exact negotiation specifically so the protocol can change
while implementations and client needs are explored. Unknown fields are
ignored to permit additive experimentation within a coordinated version, but
that rule does not make unequal versions compatible. Method names, required
fields, enum semantics, framing, and error codes may change in a later
experimental revision only when both client and engine adopt the same exact
version identifier.

An incompatible experimental revision must use a different exact version
identifier; the identifier for this document remains `"0"`. A future version
`1` must define its own compatibility policy, deprecation rules, and any
additional transports. Version `1` stability must not be inferred from this
document.
