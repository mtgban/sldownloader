# Logging is unlevelled `log.Println`/`Printf` calls throughout

**Category**: Operational hardening
**Files**: [main.go](../main.go), [scryfall.go](../scryfall.go) — every `log.Println`/`log.Printf` call site

## Problem

All diagnostic output goes through the standard library's bare `log`
package, with no level distinction between routine progress ("Found card:
...", "Adopting Scryfall spelling..."), recoverable-but-noteworthy events
("validation failed", "empty result set from Scryfall, ignoring"), and
genuine failures ("Unable to query scryfall"). Every call writes to
stderr, unconditionally, with no way to quiet the routine progress lines
without losing the failure ones, and no structured (key-value/JSON) output
that a log aggregator or the CI job summary could parse.

## Impact

- The daily workflow's job log is a flat, high-volume stream of per-card
  progress lines mixed with the handful of lines that actually matter
  (validation failures, unmatched editions) — finding the signal requires
  reading past a lot of routine noise.
- There is no `-quiet`/`-verbose` flag to control this, and no easy way to
  extract just-the-errors for alerting or triage without grepping for
  specific phrasings.

## Suggested approach

- Introduce a small leveled-logging wrapper (either `log/slog`, now in the
  standard library, or a minimal custom type) with at least
  info/warn/error levels, and a `-quiet`/`-v` flag to control what's
  printed.
- Keep the existing stdout contract (§4.2/§6.2 in SPECIFICATIONS.md)
  completely separate from whatever logger is introduced — the
  machine-readable stdout output (resume-page line, decklist text) must
  not be touched by this change; only the stderr diagnostic stream is in
  scope.
