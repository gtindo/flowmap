# Flowmap User Guide

Flowmap is a local, read-only workbench for understanding Go code through focused function-call graphs. It combines possible call relationships with signatures, data contracts, source-authored intent, source excerpts, test visibility, and evidence-based functional-core/imperative-shell classification.

Flowmap performs static analysis. An edge means a function may call another function; it does not prove that the call occurs for a particular runtime input.

The current online version of this guide is published at <https://gtindo.github.io/flowmap/>.

## Download Flowmap

[Download the latest Flowmap release](https://github.com/gtindo/flowmap/releases/latest) or browse the [complete GitHub Releases history](https://github.com/gtindo/flowmap/releases) for older versions, release notes, platform archives, and checksum manifests.

## Requirements

- macOS on Apple Silicon or Intel, or Linux on AMD64.
- Go 1.24, 1.25, or 1.26 available through `go` on `PATH`. Flowmap uses this active toolchain to load and type-check the target module. Published Flowmap binaries are built with Go 1.26 and can read export data produced by these supported versions.
- Access to the target module dependencies. Already-cached dependencies work offline; otherwise the Go toolchain may need network access.
- A modern web browser.

Flowmap reads the target working tree and stores only optional graph layouts and generated summaries outside it. It does not edit the analyzed project.

## Install a release

Choose the archive matching the machine:

| Machine | Archive |
| --- | --- |
| Linux AMD64 | `flowmap_0.1.0_linux_amd64.tar.gz` |
| Apple Silicon | `flowmap_0.1.0_darwin_arm64.tar.gz` |
| Intel Mac | `flowmap_0.1.0_darwin_amd64.tar.gz` |

Verify the archive from the release directory:

```sh
shasum -a 256 -c SHA256SUMS
```

Extract it and optionally place the binary on `PATH`:

```sh
tar -xzf flowmap_0.1.0_darwin_arm64.tar.gz
cd flowmap_0.1.0_darwin_arm64
./flowmap version
```

The release is not Apple-notarized. If macOS reports that it cannot verify the binary, inspect the downloaded artifact and, if trusted, remove its quarantine attribute:

```sh
xattr -d com.apple.quarantine ./flowmap
```

## Start Flowmap

Pass the directory containing the target module’s `go.mod`:

```sh
./flowmap serve /path/to/go/project
```

Flowmap indexes the module, prints the number of discovered functions, and listens on `http://127.0.0.1:7878`. Open that address in a browser.

Useful options:

```sh
./flowmap serve /path/to/project --addr 127.0.0.1:9000
./flowmap serve /path/to/project --tags integration,linux
```

The server binds to localhost by default and is not exposed to other machines. Stop it with `Ctrl-C`.

## Start a graph

1. Type part of a package, receiver, or function name into the search field.
2. Select one result. That function becomes the graph root.
3. Choose **Downstream** to see callees, **Upstream** to see callers, or **Both directions**.
4. Enable **Tests** to include test functions in search and graph results.

A new graph loads only one hop. This keeps large codebases readable.

## Navigate and expand

- Select a node to open its detail panel.
- Choose **Focus graph here** to make that function the new root. Double-clicking a node does the same thing.
- Select the node’s **+** control to expand only that function by one hop.
- After expansion, the control becomes **−**. Selecting it removes that expansion and any descendant expansions that are no longer reachable. Nodes still supplied by another expanded path remain visible.

This node-level expansion is particularly useful for large parser, router, or orchestration functions where a global depth increase would reveal too much at once.

## Graph views and layouts

**Extended** nodes show the qualified function name, package, signature, and intent. **Simplified** nodes show only the function name and are useful for larger structural maps.

Nodes are draggable in both views. Layouts are saved in browser storage and kept separately for each view, root function, direction, and test setting. **Reset layout** clears the saved positions for the current graph.

## Zoom and pan

- Graphs open at 100% scale. Use the horizontal and vertical scrollbars, a trackpad, or Shift+wheel to navigate an oversized graph.
- Use **+** and **−** in the header to zoom the viewport.
- Use **Fit** to show the complete current graph.
- Enable **Hand** to drag the same scrollable canvas viewport. The canvas includes one viewport of extra space beyond every edge, allowing nodes to be panned clear of the detail panel.
- Disable **Hand** to resume dragging individual nodes.

The small **+** or **−** attached to a function node controls graph expansion; the header buttons control zoom.

## Read a node

The detail panel contains:

- Function signature and source location.
- Named structs and interfaces crossing the function boundary.
- The first paragraph of the source doc comment as authored intent.
- Functional classification and its evidence.
- The exact function source excerpt.

Classification is one of:

- **pure**: explicitly documented as pure or conservatively inferred to have no visible effects and only pure local callees.
- **edge**: explicitly documented as an imperative edge or inferred from visible I/O, state mutation, time/random access, goroutines, or channels.
- **unknown**: purity could not be established safely.

Authored classifications take precedence. Inferred classifications always show their evidence and should be treated as navigation help rather than formal proof.

Dashed edges represent possible interface or dynamic-dispatch targets. Solid edges represent statically resolved calls.

## Optional generated intent

AI summaries are disabled unless a command adapter is supplied. The adapter receives one JSON object on stdin and must write a JSON object containing `summary` on stdout.

Start Flowmap with an adapter:

```sh
./flowmap serve /path/to/project --summarizer-command /path/to/summary-adapter
```

Flowmap sends source only after **Generate fallback intent** is selected. Generated text is visibly marked and cached under the operating-system user cache using the adapter identity and exact function source. Changing the source invalidates the cached result.

Review an adapter before using it: it determines whether source stays local or is transmitted to a hosted model.

## Troubleshooting

### No packages or functions are found

- Confirm the supplied path contains `go.mod`.
- Run the reproduction command printed by Flowmap and resolve its reported errors.
- Confirm the required Go version is installed.

### The active Go toolchain is newer than Flowmap

The `go` executable on `PATH` determines the compiled export-data version that Flowmap must read. This is distinct from the `go` directive in the target module's `go.mod`: for example, Go 1.26 can produce Go 1.26 export data while loading a module whose directive says `go 1.24`.

This release supports active Go toolchains from Go 1.24 through Go 1.26. If `go env GOVERSION` reports Go 1.27 or newer, install a Flowmap release built with that Go version. Go 1.23 and older are not supported.

Flowmap tolerates individual broken packages when healthy neighboring packages can still be loaded. It prints a warning with deduplicated `go list`, syntax, and type diagnostics for omitted package variants. If every package is broken, indexing stops and prints the same diagnostic report. Flowmap shows up to ten unique errors and reports how many additional errors were omitted from the display.

### Dependencies cannot be loaded

Run the project’s normal dependency command, commonly `go mod download`, then start Flowmap again. Private dependencies require the same Git credentials and `GOPRIVATE` configuration used by the Go toolchain.

### A call edge is absent or ambiguous

Static call graphs are conservative approximations. Reflection, generated code, build tags, and interface dispatch can reduce precision. Supply the appropriate `--tags` values and inspect dashed dynamic edges.

### A layout looks stale

Use **Reset layout**. Layouts are keyed by graph context, but major source changes can still make an old arrangement less useful.

### The port is already in use

Choose another loopback address:

```sh
./flowmap serve /path/to/project --addr 127.0.0.1:9000
```

## Build from source

Building Flowmap from source requires Go 1.25 or newer. The lower source-build requirement is intentionally separate from the Go 1.26 toolchain used for published release binaries.

```sh
make test
make build
```

Create all supported release archives and checksums:

```sh
make release VERSION=0.1.0
```

Artifacts are written beneath `dist/v0.1.0/`.

## Publish a release

Repository maintainers can build, tag, and push a release with:

```sh
scripts/release.sh 0.2.0
```

The script accepts versions with or without the `v` prefix. Before creating a tag, it requires:

- A clean working tree with no untracked files.
- The checked-out branch to be `main`.
- Local `main` to exactly match `origin/main` after fetching.
- The release tag to be absent locally and remotely.
- The complete `make release` build and test suite to pass.

It then creates an annotated `v0.2.0` tag and pushes only that tag. The tag activates the GitHub release workflow, which rebuilds and publishes the archives and checksum manifest. If the push fails, the local tag is retained for inspection and is never silently deleted or replaced.
