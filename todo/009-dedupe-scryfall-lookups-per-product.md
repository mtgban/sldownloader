# Repeated identical Scryfall queries aren't cached within a run

**Category**: Operational hardening
**Files**: [main.go](../main.go) — the OCR validation loop and the backfill loop in `scrapeProduct`, both calling `search(ctx, "<name> cn:<number>")`

## Problem

The OCR-validation loop (§5.5) and the backfill loop (§5.6) each issue one
Scryfall search per card, with no memoization. In catalog-crawl mode this
happens repeatedly across many products in one run, and it's common for
the same basic land or reprinted staple to appear across multiple Secret
Lair drops within one crawl — each occurrence pays a fresh, individually
rate-limited API round trip even though the query (and its answer) is
identical to one already made earlier in the same run.

## Impact

Not a correctness issue — every query is already validated independently
and correctly. It is a real, if modest, tax on the 8 req/s rate-limit
budget (§9.2 in SPECIFICATIONS.md): every avoidable duplicate request is
one fewer request of headroom against Scryfall's 10 req/s hard limit
during a long catalog crawl, and it slows down the crawl for no benefit.

## Suggested approach

- Add a simple in-process cache (a `map[string][]CardData]`, keyed by the
  exact query string) around the `search` call, scoped to the lifetime of
  one `run()` invocation (a package-level map cleared at the start, or a
  cache threaded through as a parameter/struct field — avoid a
  process-lifetime-unbounded global if this tool is ever adapted to run as
  a long-lived service rather than a one-shot CLI invocation).
- This is a small, self-contained change independent of the other
  Scryfall-related items ([003](003-scryfall-search-pagination.md)).
