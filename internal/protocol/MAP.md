# `internal/protocol` — Package Map

## Responsibility

This package is the wire contract of the experimental Flowmap engine protocol defined in [`docs/tdd/0001-flowmap-engine-protocol.md`](../../docs/tdd/0001-flowmap-engine-protocol.md). It owns camelCase wire models, method and error-code constants, `Content-Length` framing, `file:` URI conversion, and a Go JSON-RPC client. It imports no Flowmap analysis packages, so any presentation adapter can depend on it without depending on the engine.

## Files

| File | Responsibility |
|---|---|
| `models.go` | Protocol version, method/notification/capability names, enums, limits, and every request, result, notification, and shared wire model |
| `jsonrpc.go` | JSON-RPC 2.0 envelope (`Message`), error object and codes, and string-or-integer identifier validation |
| `framing.go` | `FrameReader` (strict header parsing; `ErrFraming` for unrecoverable streams) and concurrency-safe `FrameWriter` |
| `uri.go` | Absolute path ↔ `file:` URI conversion; no machine-specific paths cross the wire |
| `client.go` | `Client`: request/response correlation by id, ordered notification handlers, typed helpers per method, and shutdown/exit |
| `protocol_test.go` | Framing, header rules, URI round trips, and identifier validation |

## Boundaries and Invariants

- Wire models contain semantic data only; no layout, colors, or client interaction concepts.
- Empty collections must encode as `[]`; producers (the engine) are responsible for non-nil slices.
- `Client` notification handlers run on the read loop in arrival order and must not block on further client calls.
- The client never interprets `error.message`; callers branch on `*Error` codes.

## Change Guide

- Change the wire shape here and in the TDD together; version `0` additions must stay additive unless the version identifier changes.
- Add a typed client helper for every new request method.
- When files or responsibilities change, update this map and the root `MAP.md`.
