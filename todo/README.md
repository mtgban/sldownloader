# Improvement backlog

One file per known, not-yet-implemented improvement to `sldownloader`. This
is a notepad, not a roadmap or a promise — items sit here until someone
picks one up. See [AGENTS.md](../AGENTS.md) §7–8 for the workflow: branch
off `master`, one PR per item, delete the file (and its entry below) in the
same PR that lands the fix.

Every item was found by reading the current implementation closely — cross
references from [SPECIFICATIONS.md](../SPECIFICATIONS.md) point back to
specific items where the behavior they describe is the direct cause.

## Testing & CI

| # | Title | Why it matters |
|---|---|---|
| [004](004-golden-file-regression-tests-for-scraping.md) | No offline regression tests for the scraping/matching pipeline | Every fix today needs a live network reproduction; nothing guards against silent markup-change regressions |
| [015](015-golangci-lint-ci.md) | No linter beyond `go vet` | Easy class of bugs (unchecked errors, shadowing, etc.) goes undetected |

## Operational hardening

| # | Title | Why it matters |
|---|---|---|
| [001](001-ci-exit-code-and-next-page-marker.md) | Catalog-crawl exit code and resume-page signal are unreliable | A failed daily run can look identical to a successful one to the CI workflow |
| [002](002-shared-http-client-with-timeout-and-status-checks.md) | No shared HTTP client, timeout, or status-code checks for Wizards/Scalefast fetches | A hung server can stall the daily job indefinitely; a 404 is misreported as "no cards found" |
| [003](003-scryfall-search-pagination.md) | Scryfall search results are not paginated | Silent truncation risk if ever queried against a large result set |
| [006](006-structured-logging.md) | Logging is unlevelled `log.Println`/`Printf` calls throughout | No way to quiet routine output or get machine-parseable diagnostics |
| [007](007-context-cancellation-on-signal.md) | `context.Background()` is never cancelled | A `-page` crawl can't be interrupted cleanly mid-request |
| [009](009-dedupe-scryfall-lookups-per-product.md) | Repeated identical Scryfall queries aren't cached within a run | Wastes rate-limit headroom during long catalog crawls |
| [010](010-user-agent-on-wizards-scrape.md) | No identifying User-Agent on the Wizards/Scalefast HTTP clients | Harder to debug a block; inconsistent with the Scryfall client's own practice |

## Performance

| # | Title | Why it matters |
|---|---|---|
| [008](008-reuse-gosseract-client.md) | A new Tesseract client is constructed per OCR'd image | Unnecessary per-image setup cost on multi-card products |
| [011](011-bounded-parallel-catalog-scraping.md) | Catalog-crawl mode scrapes products one at a time | A full catalog crawl is single-threaded network I/O bound |

## Maintainability

| # | Title | Why it matters |
|---|---|---|
| [012](012-centralize-css-selectors.md) | CSS selector strings are scattered and unlabeled | A site markup change fails silently and is hard to triage |
| [013](013-version-flag.md) | No `-version` flag or build-time version stamping | Hard to tell which build produced a given decklist file or CI run |
| [014](014-makefile-for-cgo-flags.md) | No wrapper for the macOS CGO flags | New contributors hit the same confusing build failure documented in AGENTS.md §2 |
