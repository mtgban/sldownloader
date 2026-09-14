# `context.Background()` is never cancelled

**Category**: Operational hardening
**Files**: [main.go](../main.go) — `run()`

## Problem

Since the context-threading fix (`context.Background()` created once in
`run()` and passed down to `scrapeProduct` and the Scryfall calls), a
context exists end-to-end through the request chain — but it is never
wired to anything that can cancel it. A `Ctrl-C` (SIGINT) or a GitHub
Actions job cancellation during a long `-page` crawl currently relies
entirely on the OS killing the process; in-flight HTTP requests and OCR
calls have no chance to notice and stop early or clean up.

## Impact

Minor in practice (the process termination path still works, just without
graceful in-flight cancellation), but it means the context threading work
is only half-realized — the infrastructure is there but does nothing yet.
A long catalog crawl interrupted mid-request has no faster path to exit
than the OS-level kill.

## Suggested approach

- Replace `context.Background()` in `run()` with
  `signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)`
  (standard library, Go 1.16+), and defer its `stop()` call.
- No other code changes should be needed — every downstream call already
  accepts and threads a `context.Context`.
