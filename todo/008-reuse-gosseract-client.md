# A new Tesseract client is constructed per OCR'd image

**Category**: Performance
**Files**: [main.go](../main.go) — `getNumberFromLink`

## Problem

`getNumberFromLink` is called once per gallery image needing OCR (inside
the `doc.Find("figure a").EachWithBreak` loop in `scrapeProduct`). Each
call does `client := gosseract.NewClient(); defer client.Close()` —
constructing and tearing down a full Tesseract client (which involves
initializing the underlying C++ engine and loading language data) for
every single image, even when a product page has many cards needing OCR
in the same run.

## Impact

Pure performance cost, no correctness issue: for a product with, say, five
cards needing OCR, this pays Tesseract's engine-initialization overhead
five times instead of once. Given catalog-crawl mode processes many
products per run, and OCR is the fallback path (triggered whenever no
Scryfall edition match is found, or when the `-ocr` flag forces it), this
adds up across a full daily crawl.

## Suggested approach

- Hoist client construction up to `scrapeProduct` (or even `run()`, shared
  across the whole crawl) and pass a single `*gosseract.Client` down to
  `getNumberFromLink`, calling `SetImageFromBytes` fresh for each image but
  reusing the client itself. `gosseract.Client` is designed to be reused
  across multiple recognition calls (that's the documented usage pattern
  in its own README) — this isn't fighting the library, just not yet using
  it as intended.
- Be careful about `SetWhitelist` needing to be re-applied (or set once
  and left alone, since it never changes here) and about goroutine-safety
  if this is ever combined with
  [011](011-bounded-parallel-catalog-scraping.md) — `gosseract.Client` is
  not documented as safe for concurrent use from multiple goroutines, so a
  shared client would need its own client-per-worker story in that case.
