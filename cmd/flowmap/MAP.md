# `cmd/flowmap` — Package Map

## Responsibility

This package is Flowmap's executable shell. It translates command-line input into analyzer and server configuration, owns process-level cancellation and error reporting, and starts the local workbench.

## Files

| File | Responsibility |
|---|---|
| `main.go` | `serve`/`version` dispatch, flag parsing, initial analysis, optional summarizer setup, interrupt handling, and HTTP startup |
| `main_test.go` | CLI parsing, build-tag normalization, warning output, and command behavior coverage |

## Startup Flow

```text
main
  -> run
  -> parse serve flags and module path
  -> signal.NotifyContext
  -> analyzer.Analyze
  -> report non-fatal package load failures
  -> optionally create CommandSummarizer and SummaryCache
  -> server.NewRescannable
  -> App.Listen
```

`version` prints the build-time version. Release builds replace the default `dev` value through linker flags in the root `Makefile`.

## Boundaries and Invariants

- The CLI is an imperative edge; analysis decisions belong in `internal/analyzer/`.
- Exactly one module path is required for `serve`, whether it appears before or after flags.
- Build tags are normalized before they enter `analyzer.Config`.
- Package load failures may produce a warning while still yielding a usable partial index.
- AI summaries remain opt-in and require `--summarizer-command`.
- The signal-derived context controls both analysis cancellation and graceful HTTP shutdown.

## Change Guide

- Add or change CLI flags in `run`, then extend `main_test.go`.
- Keep provider-specific summary behavior outside this package; the CLI should only assemble interfaces and configuration.
- When startup wiring or command behavior changes, update this map and the root `MAP.md` if the top-level flow changed.
