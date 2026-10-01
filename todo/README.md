# Improvement backlog

One file per known, not-yet-implemented improvement to `sldownloader`. This
is a notepad, not a roadmap or a promise — items sit here until someone
picks one up. See [AGENTS.md](../AGENTS.md) §7–8 for the workflow: branch
off `master`, one PR per item, delete the file (and its entry below) in the
same PR that lands the fix.

Every item was found by reading the current implementation closely — cross
references from [SPECIFICATIONS.md](../SPECIFICATIONS.md) point back to
specific items where the behavior they describe is the direct cause.

## Performance

| # | Title | Why it matters |
|---|---|---|
| [008](008-reuse-gosseract-client.md) | A new Tesseract client is constructed per OCR'd image | Unnecessary per-image setup cost on multi-card products |

## Maintainability

| # | Title | Why it matters |
|---|---|---|
| [013](013-version-flag.md) | No `-version` flag or build-time version stamping | Hard to tell which build produced a given decklist file or CI run |
| [014](014-makefile-for-cgo-flags.md) | No wrapper for the macOS CGO flags | New contributors hit the same confusing build failure documented in AGENTS.md §2 |

## Dependencies

| # | Title | Why it matters |
|---|---|---|
| [020](020-dependency-updates.md) | Direct dependencies are behind, and nothing proposes updates | Updates arrive only as security fixes, on top of several unrelated releases |
