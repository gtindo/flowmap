# `internal/telemetry` — Package Map

## Responsibility

This package owns optional OpenTelemetry startup configuration at the process edge. It builds SDK providers, OTLP/gRPC exporters, resource attributes, propagation, and the slog bridge when OTLP environment configuration is present.

## Files

| File | Responsibility |
|---|---|
| `telemetry.go` | OTLP traces, metrics, and logs setup; JSON console plus OpenTelemetry slog fanout; shutdown and enabled-state tracking |

## Configuration Model

Telemetry is opt-in. `Setup` enables providers when `OTEL_EXPORTER_OTLP_ENDPOINT` is set and leaves normal Flowmap runs unchanged otherwise. The OTLP exporters read standard OpenTelemetry environment variables for protocol, endpoint, TLS certificates, and headers.

## Boundaries and Invariants

- This package is an imperative edge; analysis and query code must not depend on it.
- Shutdown flushes logs, metrics, and traces with a bounded context.
- Console output remains available while logs are also bridged into OpenTelemetry when telemetry is enabled.

## Change Guide

- Keep vendor/backend-specific exporter wiring here rather than in `cmd/flowmap/` or `internal/server/`.
- Add tests for environment-gated behavior when changing enablement rules.
- Update this map and the root `MAP.md` when telemetry responsibilities or configuration boundaries change.
