---
name: flowmap-views
description: Answer architecture questions about a Go or JavaScript/TypeScript repository (how a feature is implemented, what an execution path looks like, what a package exposes at its public boundary, what reaches or is affected by a function) by reading the repository's architecture docs and querying a pinned Flowmap call graph, then producing a verified flow view with a diagram and summary. Use when the user asks how something works at the architectural level, wants an execution flow or architecture diagram, or asks what calls, reaches, or depends on some code. Reads source only as a fallback.
---

# Flowmap views

Answer architecture questions without making the user read code, and without asking them to trust a diagram nobody can check.

Three sources, three jobs:

- **Docs** (`MAP.md`, `OVERVIEW.md`, `ARCHITECTURE.md`, `docs/`) give intent and the runtime links static analysis cannot see.
- **The Flowmap graph** gives facts: type-resolved calls and dependencies, reachability, public boundaries, side-effect classification, and Git changes. Facts from the graph are checkable, complete over the analyzed code, and cheap.
- **Source code** is a fallback for gaps that matter to the question, or for implementation details the user explicitly asks about.

The output is a **flow view**: a small JSON selection of real graph nodes and edges, plus labelled gaps, that `fm check` verifies and renders as HTML. Never draw a diagram the graph cannot back, and never hide a gap behind a guessed edge.

## Tool

All Flowmap access goes through the helper `scripts/fm`, in this skill's directory. Run it with Python 3 from inside the target repository, or pass `--repo PATH`.

| Command | Use |
|---|---|
| `fm start` | Start or reuse this repository's pinned Flowmap server and analyze it. Run it first, every time |
| `fm docs` | List architecture docs with line counts and titles |
| `fm search <name> [--lang L]` | Find symbols by **name** (not routes or string literals) |
| `fm boundary [dir] [--lang L]` | Public functions declared under a directory: a package's interface |
| `fm graph <id> [--direction downstream\|upstream\|both] [--depth N]` | Compact neighborhood with ids, root-relative locations, classes, and edges |
| `fm symbol <id> [--source]` | Signature, location, contracts, doc intent, and side-effect evidence. `--source` is a logged code read |
| `fm views` | Directory where view JSON files belong. It is outside the repository, so the user's tree stays clean |
| `fm check <view.json>` | Verify a view and render `<view>.html` |
| `fm rescan` | Re-analyze after the code changed |
| `fm reads` | Source reads logged in this repository |
| `fm stop [--all]` / `fm status` | Stop servers, or list recorded ones |

Pass `--lang go` or `--lang javascript` when a repository has both. JavaScript and Go are separate graphs with no edges between them.

## Method

1. **Start.** Run `fm start`. If it reports a version mismatch or a missing Go toolchain, stop and tell the user. Do not substitute another Flowmap binary.
2. **Read the docs first.** Run `fm docs`. Read the root map or overview, then the docs for the areas the question touches. Note entry points, routes, handoffs, and providers the docs name.
3. **Find entry points.** Use `fm search` for names the docs or the question give you. Routes, command names, and other string literals are not searchable: get them from the docs. If the docs are silent, grep for the literal **only to locate** the registering function, then continue in the graph.
4. **Walk the graph.** Use `fm graph` downstream from each entry point, starting at depth 3 and going deeper from interesting nodes rather than raising the depth everywhere. Use `upstream` for "who calls / what reaches" questions. For boundary questions, start with `fm boundary <dir>`.
5. **Bridge gaps, or admit them.** Where the flow continues but the graph stops, consult the docs. Add an `inferred` edge with its gap class, basis, evidence, and confidence, or an `unresolved` entry if nothing tells you where it goes. Use the code fallback only under the rules below.
6. **Compose the view.** Read `reference/view-format.md` from this skill's directory, then write the JSON into the `fm views` directory.
7. **Check.** Run `fm check` and fix every problem it reports. Never relabel a failing `call` edge as `inferred` just to pass. Either it is wrong, or the graph has a gap you must classify honestly.
8. **Answer.** Use the answer format below.

## Code fallback

Read source only when one of these holds:

- a gap blocks the user's actual question and docs and graph cannot resolve it;
- the user asks about an implementation detail inside a function; or
- a doc claim and the graph contradict each other and the question depends on which is right.

Prefer `fm symbol <id> --source`, which reads one function and logs it. Read whole files only when a function-level read cannot answer. List every source file or symbol you read in `process.files_read`, and mark edges it justified with basis `code`.

If the user says to use docs and graph only, or "no code", never read source. Leave gaps `unresolved`.

## View rules

- At most **25 nodes** in **3 to 7 steps**. Nodes are Flowmap ids, or `external` nodes for systems outside the index such as browsers, databases, and collectors.
- **Prune hard.** Omit logging, telemetry, error wrapping, and generic helpers. List significant omissions in `collapsed`.
- Use `call` only for a direct graph edge, and `path` when intermediates are collapsed. Use `inferred` for everything the graph cannot prove, including all cross-language edges and every edge touching an external node.
- Record `process.doc_gaps` for anything a doc should have said, and `process.doc_conflicts` for doc claims the graph contradicts.
- Write a `summary` of at most 150 words for someone who will not read the code. Name the public boundaries crossed and where side effects happen.

## Answer format

1. **Direct answer**: two to five sentences answering the question as asked.
2. **View**: the rendered HTML path. On macOS, offer `open <path>`.
3. **Gaps and uncertainty**: unresolved gaps, low-confidence inferred edges, and doc conflicts, each in one line.
4. **Sources**: docs read, and any source read as a fallback with the reason.

Keep the answer about architecture unless the user asked for implementation detail. When the user follows up about one step, walk deeper from that node and update the same view instead of starting over.

## Limits to state plainly

- The graph is static. Framework callbacks, HTTP routes, event streams, channel handoffs, registries, and configuration-selected implementations need docs or code to bridge.
- Interface calls fan out to every implementation and are marked `dynamic`. Say which implementations are realistic only if the docs or configuration support it.
- Analysis reflects the code at the last `fm start` or `fm rescan`.
