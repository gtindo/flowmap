# Flowmap

![Flowmap Logo](internal/server/static/icon-192.png)
<!-- PLACEHOLDER: Flowmap Logo Image -->

Flowmap is a local, spatial code-review workbench for auditing rapid structural changes and code flux in Go, JavaScript, and TypeScript repositories. When AI agents write hundreds of lines across multiple files in seconds, traditional file-tree IDEs can create cognitive overload. Flowmap replaces the file tree with an interactive, function-level call graph designed for rapid architectural supervision.

For installation and usage instructions, see [USER_GUIDE.md](USER_GUIDE.md) or the [Flowmap documentation site](https://gtindo.github.io/flowmap/).

![Flowmap Light](captures/light.png)

![Flowmap dark](captures/dark.png)

---

## 🎯 What Flowmap Is (and Is Not)

> **What it is**
>
> - **A spatial review lens:** See the shape of code changes, map execution paths, and verify structural topology without repeatedly switching file tabs.
> - **An architectural auditor:** Inspect signatures, parameter contracts, and return types to verify that generated code respects system boundaries.
> - **Focused and incremental:** Explore a lazy-loaded graph one hop at a time, avoiding unreadable visual "hairballs."

> **What it is not**
>
> - **An AI chatbot or tutor:** It does not explain basic code logic or provide automated walkthroughs.
> - **A runtime tracer:** It performs static analysis. A call edge means a function *may* call another; it does not trace live state or execution variables.
> - **A generic multi-language tool:** It supports Go plus a standalone JavaScript/TypeScript view; it does not emulate package-manager or bundler resolution.

---

## Run

```sh
go run ./cmd/flowmap serve /path/to/go/module
```

Open `http://127.0.0.1:7878`, search for a function, and choose upstream, downstream, or both directions. Use `--tags tag1,tag2` for build tags and `--addr 127.0.0.1:9000` to change the local address.

To serve several projects from one workbench, provide a JSON registry. Projects are scanned only when selected in the project picker:

```sh
flowmap serve --config projects.json
```

```json
{"projects":[{"name":"API","path":"/work/api","tags":["integration"],"languages":["go"]},{"name":"Web","path":"/work/web","languages":["javascript"]}]}
```

To keep Flowmap in the macOS Dock, open the running workbench in Safari and choose **File > Add to Dock**, or use **Install Flowmap** in Chrome. The installed web app uses the same host and port; it does not start the Flowmap server. See the user guide for details.

Tests are indexed but hidden until the **Tests** toggle is enabled. A mixed repository exposes a language picker so Go and JavaScript/TypeScript remain separate graphs. JavaScript support covers `.js`, `.mjs`, `.cjs`, `.jsx`, `.ts`, `.mts`, `.cts`, and `.tsx` without requiring Node.js; it follows only relative local imports. Anonymous functions appear when traversing their named parents but stay out of search and Git change lists. Non-local packages remain outside the visible graph. Dashed edges are interface or dynamic-dispatch candidates, while dotted edges show local functions passed as arguments.

## Navigate the Graph and Audit Flux

- **Focused one-hop exploration:** A new focus starts with exactly one hop. Use the `+` control on a node to expand that function by one additional hop. The control becomes `−` after expansion; collapsing it removes any now-unreachable expanded subtree while preserving nodes supplied by another path.
- **Reviewing code flux:** When the module belongs to a Git repository, the header shows the branch captured by the current scan. Select **Changes** to review functions that differ from `HEAD`, including staged, unstaged, and non-ignored untracked changes.
- **Visual status anchors:** In the graph canvas, node titles reflect Git delta status: blue indicates a new function, while yellow or amber indicates an updated function. Selecting an entry focuses its graph and opens its local diff.
- **Spatial layouts:** Choose **Extended** for contracts and intent, or **Simplified** for compact function-name nodes. Nodes are draggable in both views, and layouts are saved separately in browser storage.
- **Canvas telemetry:** Graphs open at a readable 100% scale. Use scrollbars, a trackpad, or Shift+wheel to move through an oversized graph. Use `+`, `−`, and **Fit** for zoom. **Hand** enables drag-to-scroll navigation; disable it to resume node dragging.
- **Rescan codebase:** Rebuild the analysis after an agent or human modifies the source code, without restarting the server. The current graph refreshes with its settings intact.

## Classification

Authored **Operations (Pure)** and **Side Effect (Edge)** documentation takes precedence. Otherwise, Flowmap conservatively identifies visible effects such as package-state writes, object or index mutation, goroutines, channel sends, time or random access, and known I/O packages.

A function is inferred pure only when it has no visible effects, no effect-unknown external calls, and every analyzed local callee is pure. Every result includes provenance and evidence.

## HTTP API

```text
GET  /api/projects
POST /api/projects/<name>/scan
GET  /api/search?q=<text>&tests=<bool>
GET  /api/graph?root=<id>&direction=<upstream|downstream|both>&depth=<0..8>&tests=<bool>
GET  /api/functions/<id>
GET  /api/git-status
POST /api/rescan?project=<name>
```

All project data endpoints accept `project=<name>` in multi-project mode. Legacy one-project requests continue to work without it.

## Development

Building Flowmap from source requires Go 1.25 or newer. Published binaries are built with Go 1.26 and support projects loaded by Go 1.24 through Go 1.26. See the user guide for the distinction between a module's `go` directive and its active toolchain.

```sh
go test ./...
go build ./cmd/flowmap
```

Build the three shareable release archives and their checksum manifest:

```sh
make release VERSION=0.1.0
```

Pushing a tag such as `v0.2.0` runs `.github/workflows/release.yml`, builds and verifies every supported archive, and publishes them through GitHub Releases. `.github/workflows/pages.yml` publishes the canonical `USER_GUIDE.md` to GitHub Pages whenever the guide changes on `main`.

## 🛠 Contributing and Customization

Flowmap is a personal software tool tailored to a specific development velocity, workflow, and aesthetic preference. Upstream feature requests, bug reports, and pull requests are not accepted.

In the era of agentic coding, it is often more efficient to fork and customize software. If Flowmap matches most of your workflow but you want to change the visual layout, add features, or port it to another environment, fork the repository and use AI agents to reshape it into your own workbench.

## License

Flowmap is available under the [MIT License](LICENSE).
