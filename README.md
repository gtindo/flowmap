# Flowmap

Flowmap is a local static code-reading workbench for Go. Start from one function and explore a focused caller/callee neighborhood enriched with typed inputs, outputs, named data contracts, authored intent, source, test reachability, and evidence-based functional-core/imperative-shell classification.

Flowmap displays possible static calls. It does not claim that an edge executes at runtime or expose runtime values.

For installation and complete usage instructions, see [USER_GUIDE.md](USER_GUIDE.md) or the [Flowmap documentation site](https://gtindo.github.io/flowmap/).

## Run

```sh
go run ./cmd/flowmap serve /path/to/go/module
```

Open `http://127.0.0.1:7878`, search for a function, and choose upstream, downstream, or both directions. Use `--tags tag1,tag2` for build tags and `--addr 127.0.0.1:9000` to change the local address.

Tests are indexed but hidden until the **Tests** toggle is enabled. Anonymous functions and non-local packages stay outside the visible graph. Dashed edges are interface/dynamic-dispatch candidates rather than definite static calls.

## Navigate the graph

- A new focus starts with one hop. Use the **+** control on any node to expand only that function by one additional hop. The control becomes **−** after expansion; collapsing it removes any now-unreachable expanded subtree while preserving nodes still supplied by another path.
- Click a node to inspect it, then choose **Focus graph here** to make it the new root. Double-clicking a node performs the same action.
- Choose **Extended** for contracts and intent or **Simplified** for compact function-name nodes. Nodes are draggable in both views and their layouts are saved separately in browser storage.
- Use **+**, **−**, and **Fit** for viewport zoom. Enable **Hand** to pan the canvas; disable it to resume node dragging.
- **Reset layout** clears saved positions for the current root, direction, test setting, and view.

## Classification

Authored `Operations (Pure)` and `Side Effect (Edge)` documentation wins. Otherwise Flowmap conservatively identifies visible effects such as package-state writes, object/index mutation, goroutines, channel sends, time/random access, and known I/O packages. A function is inferred pure only when it has no visible effects, no effect-unknown external calls, and every analyzed local callee is pure. Every result includes its provenance and evidence.

## Optional intent summaries

AI generation is disabled by default. Opt in with a command that reads one JSON `SummaryRequest` from stdin and writes `{"summary":"..."}` to stdout:

```sh
go run ./cmd/flowmap serve /path/to/module --summarizer-command /path/to/your-adapter
```

Generation happens only when you press **Generate fallback intent**. Results are marked generated and cached by provider identity plus exact function source under the operating-system user cache; the analyzed repository is never modified.

## HTTP API

- `GET /api/search?q=<text>&tests=<bool>`
- `GET /api/graph?root=<id>&direction=<upstream|downstream|both>&depth=<0..8>&tests=<bool>`
- `GET /api/functions/<id>`
- `POST /api/functions/<id>/summary`

## Develop

```sh
go test ./...
go build ./cmd/flowmap
```

Build the three shareable release archives and their checksum manifest:

```sh
make release VERSION=0.1.0
```

Pushing a tag such as `v0.2.0` runs `.github/workflows/release.yml`, builds and verifies every supported archive, and publishes them through GitHub Releases. `.github/workflows/pages.yml` publishes the canonical `USER_GUIDE.md` to GitHub Pages whenever the guide changes on `main`.

Enable **Settings → Pages → Build and deployment → GitHub Actions** once for the repository before the first Pages deployment.
