# `scrapeProduct` does everything in one 190-line function

**Category**: Maintainability
**Files**: [main.go](../main.go) — `scrapeProduct`

## Problem

`scrapeProduct` fetches the product page, extracts the title and card list
(with the `<p>` fallback), matches the title against the Scryfall edition
headers, searches Scryfall, aligns or replaces the card list, sorts it,
runs the OCR pass over the gallery, and backfills the remaining numbers —
in one function that takes a URL, calls Wizards and Scryfall, and runs
Tesseract.

None of it can be tested without the network, including the parts that
are pure logic once their inputs exist: extracting cards from a parsed
page, choosing an edition header, the fold-mode image indexing, and the
backfill arithmetic. It also declares a local `cleanTitle` that shadows
the package-level `cleanTitle` function for the rest of its body.

## Impact

- Every change to this function needs a live run to verify (AGENTS.md §6),
  and the logic most prone to regressions has no unit tests.
- [004](004-golden-file-regression-tests-for-scraping.md) is blocked on
  this: a test can't reach any one step without running all of them.

## Suggested approach

Split along the existing phases, keeping `scrapeProduct` as a thin
sequence of calls:

- `parseProductPage(doc *goquery.Document) (title string, cards []CardData)`
  — pure; testable with a short inline HTML string.
- `matchEdition(ctx, headers, title, cards, search)` — the header loop,
  taking the search function as a parameter so a test can supply canned
  results.
- `ocrNumbers(ctx, doc, cards, ...)` and `backfillNumbers(ctx, cards, search)`
  — the OCR pass and the contiguous-number guess, the latter testable with
  a fake search.

Rename the shadowing local (e.g. `matchTitle`). No behavior change: verify
with a byte-for-byte diff of a live `-page` crawl's output files before and
after.
