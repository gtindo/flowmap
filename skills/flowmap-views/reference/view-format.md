# Flow view format

A flow view is a JSON file in the directory printed by `fm views`. `fm check <file>` verifies it against the running Flowmap server and renders `<file>.html` beside it.

## View format

```json
{
  "question": "How is <feature> implemented?",
  "project": "default",
  "summary": "Plain-language architectural summary. Paragraphs separated by blank lines.",
  "nodes": [
    { "id": "<flowmap symbol id>", "language": "go", "label": "Short step label", "note": "Optional" },
    { "id": "browser", "external": true, "label": "Browser" }
  ],
  "steps": [
    { "title": "Step title", "nodes": ["<id>", "<id>"] }
  ],
  "edges": [
    { "from": "<id>", "to": "<id>", "kind": "call", "label": "optional" },
    { "from": "<id>", "to": "<id>", "kind": "path", "hops": 3 },
    { "from": "<id>", "to": "<id>", "kind": "inferred", "gap": "channel-handoff", "basis": "overview", "confidence": "medium", "evidence": "<path>/OVERVIEW.md, <section>" }
  ],
  "unresolved": [
    { "from": "<id>", "gap": "registry-lookup", "note": "Handler is looked up at runtime; target not identifiable" }
  ],
  "collapsed": [{ "name": "<qualified name>", "reason": "logging helper" }],
  "process": { "api_calls": 0, "files_read": [], "friction": [], "doc_gaps": [], "doc_conflicts": [] }
}
```

| Element | Checker rule |
|---|---|
| Node | Must exist in the Flowmap index for its language unless `external` is true |
| `call` edge | A direct call or dependency edge `from → to` exists in the graph |
| `path` edge | `to` is reachable downstream from `from` within `hops` (default 4, max 8) |
| `inferred` edge | Requires `gap`, `basis` (`code`, `map`, `overview`, or `graph`), `evidence`, and `confidence` (`high`, `medium`, or `low`). Rejected when the graph already reaches `to` from `from` within 8 hops. Required for cross-language edges and edges touching external nodes |
| `unresolved` entry | Must start at a declared node and name a gap class. Renders as a `?` node |

`basis` names the most specific source that justified the edge:

| Basis | Source |
|---|---|
| `map` | Navigation docs that say where responsibilities live: `MAP.md`, `AGENTS.md`, `README.md` |
| `overview` | Design and data-flow docs: `OVERVIEW.md`, `ARCHITECTURE.md`, `docs/` pages, design records |
| `graph` | Names, signatures, contracts, or doc comments matched in the Flowmap index |
| `code` | Source you read as a fallback, with `fm symbol --source` or a direct file read |

When several sources say the same thing, cite the first in the order `map`, `overview`, `graph`, `code`. That keeps code reads visible as the last resort they should be.

`fm check` prints and renders a gap breakdown: each gap class, counted as bridged via each basis, or as unresolved.

## Gap classes

| Class | Static analysis cannot see… |
|---|---|
| `cross-language` | A JS ↔ Go call, because the language views are separate |
| `framework-callback` | A library or framework invoking registered code, such as an RPC server calling a service method |
| `http-route` | A route string linking a request to its handler |
| `event-stream` | Server-sent events, WebSocket messages, or named-event delivery |
| `channel-handoff` | Work passed over a channel or to another goroutine |
| `registry-lookup` | A function stored in a map or registry and invoked later |
| `unresolved-dispatch` | An interface or function-value call the graph missed or over-approximated |
| `config-selection` | An implementation chosen from runtime configuration |
| `external-system` | Calls into databases, collectors, browsers, and other processes |
