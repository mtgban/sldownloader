# Unstable sort before OCR risks desyncing image-to-card mapping

**Category**: Correctness
**Files**: [main.go](../main.go) — `scrapeProduct`, the `sort.Slice(cards, ...)` call immediately before the OCR block

## Problem

`cards` is sorted with `sort.Slice` right before the OCR fallback runs:

```go
sort.Slice(cards, func(i, j int) bool {
    a, b := collectorNumberValue(cards[i].Number), collectorNumberValue(cards[j].Number)
    if a != b {
        return a < b
    }
    return cards[i].Number < cards[j].Number
})
```

`sort.Slice` is explicitly documented by the Go standard library as **not
guaranteed to be stable** — equal elements are not guaranteed to retain
their relative input order.

In the exact case where OCR is about to run because no edition match was
found (§5.3 in SPECIFICATIONS.md), every card's `Number` is still `""` at
this point — meaning `collectorNumberValue` returns `0` for every card and
the raw-string tiebreak also compares `"" == ""`. Every pairwise comparison
in the sort is therefore "equal," and Go's unstable sort implementation is
not obligated to leave the cards in their original (page-scrape) order.

The OCR loop immediately after this sort assumes positional correspondence
between `cards[i]` and the `i`-th `figure a` image element on the page (in
original DOM/gallery order) — see SPECIFICATIONS.md §5.5. If the sort ever
reorders the equal-valued elements, this assumption silently breaks: OCR'd
numbers would be assigned to the wrong cards, and the Scryfall validation
step (`"<name> cn:<num>"`) would likely just fail for the mismatched pairs
(logged as "validation failed" and skipped) rather than silently accepting
a wrong assignment — but the practical effect would still be cards that
*should* have gotten a number failing to, or getting a different, also
technically-valid-looking wrong one if the misattributed name+number
combination happens to validate against some other real printing.

## Impact

Not confirmed to have manifested in production (Go's actual sort
implementation may happen to preserve input order for this specific "all
equal" input shape in practice, given how it's implemented today — but
that is an implementation detail, not a documented guarantee, and could
change between Go versions without notice). This is a **latent
correctness risk** discovered by code review, not an observed bug report.

## Suggested approach

- Replace `sort.Slice` with `sort.SliceStable` at this call site. This is
  a one-line, behavior-preserving-for-the-common-case fix (stable sort
  costs more but the input size here — cards per Secret Lair product — is
  small enough that the performance difference is irrelevant) that removes
  the risk entirely regardless of whether it has ever actually manifested.
- Add a regression test asserting that, given an all-unnumbered input
  slice in a known order, the sort output preserves that order — this
  guards the invariant going forward even though it can't retroactively
  prove today's behavior was already safe.
