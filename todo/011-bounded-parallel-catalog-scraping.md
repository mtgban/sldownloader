# Catalog-crawl mode scrapes products one at a time

**Category**: Performance
**Files**: [main.go](../main.go) — `run()`, the inner `for _, product := range resp.Products` loop

## Problem

Catalog-crawl mode processes every non-skipped product sequentially: fetch
the Wizards product page, parse it, look it up on Scryfall, possibly OCR
several images, possibly issue several backfill validation queries — all
network I/O, all blocking, one product fully finished before the next one
starts. There is no concurrency anywhere in the crawl.

## Impact

Pure performance: a full catalog crawl (or even a large `-page` range) is
slower than it needs to be, dominated by network round-trip latency rather
than CPU work. This mostly matters for a from-scratch full-history crawl
(`-page 0`) or for recovering from a large gap in `SLD_LAST_PAGE`, not for
the normal daily incremental run (which typically only has a handful of
new products per day).

## Suggested approach

- Introduce a small bounded worker pool (e.g. `errgroup.Group` with
  `SetLimit`, or a manual semaphore channel) around the per-product
  scrape-and-dump call, capping concurrency at a conservative number
  (single digits) to stay well under Scryfall's 10 req/s ceiling even with
  multiple products in flight simultaneously issuing their own Scryfall
  queries.
- The existing shared, rate-limited Scryfall client (`getScryfallClient`,
  §9.2 in SPECIFICATIONS.md) already serializes/paces Scryfall requests
  correctly across goroutines (the underlying `ratelimit.Limiter` is safe
  for concurrent use) — the main things concurrency would newly stress are
  the Wizards/Scalefast fetches (see [002](002-shared-http-client-with-timeout-and-status-checks.md))
  and, if combined with [008](008-reuse-gosseract-client.md), the
  gosseract client (which is **not** documented as safe for concurrent
  use — a shared client and concurrency are mutually exclusive unless this
  is designed as client-per-worker).
- File writes (`dumpCards`) are independent per product (distinct
  filenames) and need no additional synchronization.
- Preserve output determinism where it's relied on (the skip-list CSV
  lines and the final resume-page line, §4.1/§4.2 in SPECIFICATIONS.md) —
  concurrent scraping must not reorder or interleave those specific stdout
  writes relative to what the CI workflow parses.
