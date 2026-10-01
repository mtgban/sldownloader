# No offline regression tests for the scraping/matching pipeline

**Category**: Testing & CI
**Files**: [main_test.go](../main_test.go), [main.go](../main.go) (`scrapeProduct`), [scryfall.go](../scryfall.go) (`search`, `searchURI`, `loadScryfallHeaders`)

## Problem

`main_test.go` covers the pure string-processing functions well
(`cleanLine`, `cleanTitle`, `collectorNumberValue`, `normalizeCardName`,
`canonicalName`, `matchCardNumbers`, `extractNumber`), but nothing that
touches the network — `scrapeProduct`, `search`, `searchURI`,
`loadScryfallHeaders`, `getProducts` — has any test coverage at all, and
`getNumberFromLink` is tested only on a blank image. The established practice for validating a fix in this
area (documented in [AGENTS.md](../AGENTS.md) §6) is a **live** run against
the actual product URL that exposed the bug, plus a manual regression check
against a couple of neighboring drops.

## Impact

- Every fix to the scraping/matching pipeline requires a live network
  reproduction to verify, which is slow, non-reproducible in CI, and
  invisible to anyone reviewing a PR without also running the tool
  themselves.
- Nothing guards against the two most likely real-world regressions for
  this tool: a Wizards or Scryfall markup change silently breaking a
  selector (see [012](012-centralize-css-selectors.md)), or a future edit
  to the matching/backfill logic changing behavior on an already-handled
  product shape without anyone noticing until the next daily run.

## Suggested approach

- Capture a handful of representative product pages (a straightforward
  single-finish drop, a foil/nonfoil pair, a "Different" full-art-lands
  style product, a product needing the fallback `<p>`-tag card-list path,
  one exercising `foldMode`) as static HTML fixtures checked into the repo
  (e.g. `testdata/`), along with the Scryfall API responses they'd need
  (either as fixtures too, or by pointing `search`/`loadScryfallHeaders` at
  an injectable base URL for the test).
- Use `net/http/httptest.Server` to serve the fixtures, and refactor
  `scrapeProduct`'s and `loadScryfallHeaders`'s hardcoded URLs into
  parameters or package-level vars that a test can override.
- Assert against the exact expected `CardSet` (or the exact rendered
  `.txt` output via `dumpCards`) for each fixture, so a future change that
  alters output for any of these known-good cases fails loudly in `go
  test` instead of silently in production.
- The Test workflow ([test.yml](../.github/workflows/test.yml)) runs
  `go test` on every PR, so fixtures added here are enforced as soon as
  they land.
