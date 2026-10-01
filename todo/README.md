# Improvement backlog

One file per known, not-yet-implemented improvement to `sldownloader`. This
is a notepad, not a roadmap or a promise — items sit here until someone
picks one up. See [AGENTS.md](../AGENTS.md) §7–8 for the workflow: branch
off `master`, one PR per item, delete the file (and its entry below) in the
same PR that lands the fix.

Every item was found by reading the current implementation closely — cross
references from [SPECIFICATIONS.md](../SPECIFICATIONS.md) point back to
specific items where the behavior they describe is the direct cause.

## Maintainability

| # | Title | Why it matters |
|---|---|---|
| [014](014-makefile-for-cgo-flags.md) | No wrapper for the macOS CGO flags | New contributors hit the same confusing build failure documented in AGENTS.md §2 |
