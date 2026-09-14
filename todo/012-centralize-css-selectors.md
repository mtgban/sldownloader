# CSS selector strings are scattered and unlabeled

**Category**: Maintainability
**Files**: [main.go](../main.go) (`h1[class="product-title"]`, `div[class="force-overflow"] ul li`, `div[id="collapse2"] div[class="force-overflow"] p[class="product-information"]`, `h2[class="pdp_title"]`, `figure a`), [scryfall.go](../scryfall.go) (`titleClass = ".card-grid-header-content"`)

## Problem

Every `doc.Find(...)` call in this codebase uses a selector string written
inline at its call site, each tied to the current markup of
`secretlair.wizards.com` or `scryfall.com/sets/sld`. Only one of these
(`scryfall.go`'s `titleClass`) is pulled out into a named constant; the
rest are anonymous string literals with no comment explaining what part of
the page they're meant to target or why that particular selector was
chosen over an alternative.

## Impact

If either site changes its markup (a redesign, a class rename, a
restructured DOM), the failure mode today is silent and generic: `goquery`
simply finds nothing, and downstream this surfaces as the unhelpful
`"no cards found"` error (or, for the Scryfall headers scrape, as
`loadScryfallHeaders` quietly returning an empty slice with no error at
all — `doc.Find(titleClass).Each` just never runs its callback). Someone
debugging this has to first suspect the parsing logic, rule it out, and
only then think to check the selectors — there's nothing pointing them
there directly.

## Suggested approach

- Pull every selector into a named, commented constant (mirroring the
  existing `titleClass` pattern), grouped by which site they target, e.g.:
  ```go
  const (
      // Wizards product page
      productTitleSelector = `h1[class="product-title"]`
      cardListSelector     = `div[class="force-overflow"] ul li`
      // ...
  )
  ```
- Consider adding a lightweight sanity check after each `doc.Find` that
  returns zero matches unexpectedly — e.g. if the product-title selector
  finds nothing at all, that's a stronger signal of a markup change than
  "zero cards found," and could log a more specific, actionable message.
- This is a low-risk, mechanical refactor with no behavior change — a good
  first item for someone new to the codebase.
