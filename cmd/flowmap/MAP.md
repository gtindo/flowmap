# `cmd/flowmap` — Package Map

## Responsibility

This package is Flowmap's executable shell. It translates command-line input into engine and server configuration, owns process-level cancellation and error reporting, starts the local workbench over an in-process engine session, and runs the stdio engine for editor integrations.

## Files

| File | Responsibility |
|---|---|
| `main.go` | `serve`/`engine`/`version` dispatch, path or JSON registry parsing, optional telemetry setup, engine options and summarizer setup, in-process engine start/close, eager or lazy scans through the server adapter, interrupt handling, and HTTP startup |
| `main_test.go` | CLI parsing, build-tag normalization, warning output, and a real Go analysis driven through `flowmap engine` over stdio pipes |

## Startup Flow

```text
main
  -> run
  -> serve:
       parse flags and module path or `--config` registry, then detect configured Go and/or JavaScript language views
       -> signal.NotifyContext
       -> telemetry.Setup when OTLP environment configuration is present
       -> engine options (optional CommandSummarizer and SummaryCache)
       -> engine.StartInProcess (initialized protocol client)
       -> server.New (one workspace per project)
       -> legacy one-project mode: App.Scan each language eagerly and report non-fatal load failures
       -> App.Listen
       -> protocol shutdown/exit with a bounded timeout
  -> engine:
       parse `--summarizer-command`
       -> telemetry.Setup writing to stderr
       -> engine.New(...).Serve(stdin, stdout); diagnostics go to stderr
```

`version` prints the build-time version. Release builds replace the default `dev` value through linker flags in the root `Makefile`.

## Boundaries and Invariants

- The CLI is an imperative edge; analysis decisions belong in `internal/analyzer/` and session behavior in `internal/engine/`.
- `flowmap engine` writes only protocol frames to stdout; it exits non-zero when input ends or `exit` arrives before a completed `shutdown`.
- `serve` accepts either one module path or a JSON registry through `--config`; the legacy path remains eager while registry projects are lazy.
- Build tags are normalized before they enter `analyzer.Config`.
- Package load failures may produce a warning while still yielding a usable partial index.
- AI summaries remain opt-in and require `--summarizer-command`.
- OpenTelemetry export remains opt-in through OTLP environment configuration.
- The signal-derived context controls both analysis cancellation and graceful HTTP shutdown.

## Change Guide

- Add or change CLI flags in `run`, then extend `main_test.go`.
- Keep provider-specific summary behavior outside this package; the CLI should only assemble interfaces and configuration.
- Keep telemetry exporter details in `internal/telemetry`; the CLI should only initialize and shut down providers.
- When startup wiring or command behavior changes, update this map and the root `MAP.md` if the top-level flow changed.
