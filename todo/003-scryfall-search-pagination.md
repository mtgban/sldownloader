# Scryfall search results are not paginated

**Category**: Operational hardening
**Files**: [scryfall.go](../scryfall.go) — `search(ctx, query)`

## Problem

`search` calls `client.SearchCards` once and returns `result.Cards`
directly. `go-scryfall`'s `CardListResponse` includes a `HasMore` flag and
a `NextPage` URL for results spanning more than one API page (175 cards),
but neither is consulted — only the first page is ever read.

## Impact

For a single Secret Lair edition query (the only thing this tool currently
searches for), editions are small enough that this has not been observed
to matter in practice. But it is a silent truncation risk: if `search` is
ever reused against a larger query (a wider set-level search, a name-only
lookup with many printings, etc.), results past the first page would be
dropped with no error and no log message — the caller has no way to know
truncation happened.

## Suggested approach

- Loop while `result.HasMore` is true, issuing follow-up requests via
  `NextPage` (check what `go-scryfall`'s API surface offers for this —
  some client libraries expose a dedicated pager helper), accumulating
  `Cards` across pages before returning.
- At minimum, log a warning if `HasMore` is ever true for a Secret Lair
  edition query, since that would indicate either a bug or a materially
  different product shape than anything seen so far.
