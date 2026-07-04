# Repository Guidelines

## Architecture: Functional Core, Imperative Shell

Keep Flowmap’s analysis deterministic and isolate effects at the edges.

- **Data:** Use plain structs for models and configuration. Avoid hidden mutation.
- **Operations (Pure):** Transform explicit inputs into outputs. Analysis, classification, graphs, and queries belong in `internal/analyzer/`.
- **Side Effects (Edges):** Keep filesystem, Git, HTTP, process, and time-dependent work at boundaries. CLI code is in `cmd/flowmap/`; HTTP and UI integration live in `internal/server/`.
- Pass `context.Context` first to cancellable or I/O-bound operations. Wrap returned errors with useful context using `%w`.

Use guard clauses for invalid states and errors. Name variables by intent; reserve single-letter names for loop indexes. Extract magic values into constants. Comments explain decisions, not mechanics. Document exported declarations and label intentionally classified functions with `Operations (Pure)` or `Side Effect (Edge)`.

Favor readable vertical spacing. Separate guard clauses, setup, transformation stages, and side effects with blank lines. Do not compress unrelated operations into one visual block or place multiple statements on one line. Keep tightly coupled assignments together; blank lines should reveal the function’s phases, not decorate every statement.

## Project Structure

`cmd/flowmap/` contains the executable. `internal/analyzer/` owns Go loading, graphs, Git deltas, and classification. `internal/server/` exposes the local API; browser assets are in `internal/server/static/`. Analyzer fixtures belong in `internal/analyzer/testdata/`. Documentation lives in `README.md`, `USER_GUIDE.md`, and `docs/`; screenshots live in `captures/`.

## Commands

| Task | Command |
| :--- | :--- |
| Run locally | `go run ./cmd/flowmap serve /path/to/module` |
| Build | `make build` |
| Format | `make fmt` |
| Lint | `make lint` |
| Test all | `make test` or `go test ./...` |
| Test package | `go test ./internal/analyzer -run TestName` |
| Compatibility | `scripts/compatibility-smoke.sh` |
| Release | `make release VERSION=0.6.0` |

Run `make fmt` on changed Go files and `make lint` before submission. Follow existing two-space indentation in frontend assets.

## Testing

Use Go’s `testing` package, `TestXxx` names, and colocated `*_test.go` files. Prefer table-driven cases for related inputs and `testdata/` modules for loader or toolchain behavior. Add regression coverage for defects and run `make lint test` before submission.

## Commits and Pull Requests

Use concise imperative subjects, typically `feat:`, `fix:`, or `docs:`; version commits use `bump to X.Y.Z`. Keep commits focused. Pull requests should explain motivation, behavior, and verification, link relevant issues, and include before/after screenshots for `internal/server/static/` changes. Never commit binaries, `dist/`, secrets, or machine-specific paths.
