# Mixed-finish products lose per-card Foil/Etched on Scryfall replacement

**Category**: Correctness
**Files**: [main.go](../main.go) — `scrapeProduct`, the `len(results) != len(cards)` branch

## Problem

When the number of cards scraped from the product page doesn't match the
number of cards Scryfall's edition search returns, the code trusts
Scryfall and **replaces the entire scraped card list** with the search
results (see SPECIFICATIONS.md §5.3 step 3):

```go
if len(results) != len(cards) {
    log.Println("... but the contents differ, we trust Scryfall...")
    for i := range results {
        results[i].Foil = cards[0].Foil
        results[i].Etched = cards[0].Etched
        results[i].Count = 1
    }
    cards = results
}
```

Every replacement card's `Foil`/`Etched` flags are copied from
**`cards[0]`** — the *first* originally-scraped card, whichever that
happened to be — and applied uniformly to **every** card in the
replacement list, regardless of what that individual card's own scraped
finish actually was.

## Impact

For any product where this branch fires (count mismatch between scrape and
Scryfall) **and** the product has genuinely mixed finishes within the same
page (e.g. some cards nonfoil, others foil, within one product listing —
plausible for a "Different"-style random-print product or a page listing
both a base and a bonus card with different finishes), every card in the
replaced list silently gets whichever finish `cards[0]` happened to have,
which is wrong for every card that didn't actually share that finish. This
is a real correctness bug, not just a theoretical one, though its
prevalence depends on how often this branch actually fires in practice
(unmeasured — see [suggested approach] below for how to find out).

## Suggested approach

- First, add logging (or a metric) specifically for how often this branch
  fires and what the scraped-vs-Scryfall count mismatch looks like when it
  does, to establish how common mixed-finish mismatches actually are
  before investing in a fix.
- The correct fix depends on what's actually causing the count mismatch in
  practice: if it's usually "Scryfall's count is authoritative and the
  scrape genuinely missed/duplicated a bullet point," per-card finish
  should ideally come from matching each Scryfall result back to *some*
  scraped line (even an imperfect one) to inherit its Foil/Etched, rather
  than blindly using `cards[0]`. If it's usually a difference in
  finish-variant counting (e.g. the page groups foil+nonfoil into fewer
  bullets than Scryfall lists separate printings for), the fix looks more
  like explicitly querying Scryfall's finish/foil metadata for each
  returned card instead of inferring it from the scrape at all.
- At minimum, replace the blanket `cards[0]` copy with a per-card
  best-effort match against the original `cards` list by normalized name
  (reusing `normalizeCardName`, already used elsewhere) before falling
  back to the `cards[0]` default only when no match is found at all.
