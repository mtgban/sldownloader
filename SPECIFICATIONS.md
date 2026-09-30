# SPECIFICATIONS

This document is a precise, implementation-level specification of what
`sldownloader` does: its interface, its external data sources, its
processing pipeline step by step, and its output format. It describes
behavior as implemented, including edge cases and quirks that are easy to
miss on a casual read of the source. It is not a tutorial — for that, see
[README.md](README.md). For contribution/workflow guidance, see
[AGENTS.md](AGENTS.md).

Line/function references point at [main.go](main.go) and
[scryfall.go](scryfall.go) as of the commit that introduces this document;
they will drift as the code evolves; treat them as pointers, not guarantees.

---

## 1. Purpose

Given a Secret Lair Drop product page (or a range of them, discovered via a
catalog listing), produce a plain-text decklist file per product, listing
each card's name, quantity, and Scryfall collector number in the `SLD` set,
formatted for direct use in
[taw/magic-preconstructed-decks](https://github.com/taw/magic-preconstructed-decks)
under `data/sld/sld/`.

---

## 2. Command-line interface

```
sldownloader [-page N] [-ocr] [URL ...]
```

| Flag/arg | Type | Default | Meaning |
|---|---|---|---|
| `-page` | int | `-1` | Starting page for catalog-crawl mode. `0` starts from the very beginning of the catalog. A negative value (the default) means "not set." |
| `-ocr` | bool | `false` | Force an OCR pass even when the Scryfall edition match succeeds. OCR still only fills in cards that don't already have a number from the edition match (see §5.3) — it never overrides an already-matched number. |
| positional args | `[]string` | none | One or more explicit `secretlair.wizards.com` product page URLs. When present, these are processed **instead of** catalog-crawl mode, regardless of `-page`. |

### 2.1 Mode selection

- **Explicit-URL mode**: triggered by any positional argument(s). `-page` is
  ignored entirely in this mode. Every URL argument is processed (not just
  the first); the per-card decklist for each is printed to **stdout** (no
  file is written — see §6.2). The process exit code is `1` if *any* URL
  failed to scrape or dump, `0` otherwise; all URLs are attempted regardless
  of earlier failures.
- **Catalog-crawl mode**: triggered when there are no positional arguments.
  Requires `-page >= 0`; if `-page` is left at its default (`-1`), the tool
  logs `"Missing starting -page argument"` and exits `1`. See §4 for the
  crawl algorithm.

---

## 3. External data sources

| Source | Protocol | Used for | Client |
|---|---|---|---|
| Scalefast Store Search API | JSON, paginated, no auth (`scalefastURL` in main.go) | Enumerating Secret Lair catalog products in catalog-crawl mode | `retryablehttp` (fresh client per call, no shared rate limiting) |
| `secretlair.wizards.com/.../product/<id>` | HTML | The actual card list, title, and image gallery for one product | `retryablehttp` (package-level `retryablehttp.Get`, default logger — i.e. **not** silenced, unlike the other two `retryablehttp` clients in the codebase) |
| `scryfall.com/sets/sld` | HTML (scraped, **not** the JSON API) | The list of `(edition title, prebuilt search URI)` pairs used for the primary name-matching pass | `go-cleanhttp` default client, once per process run |
| `api.scryfall.com` | REST/JSON, via [go-scryfall](https://github.com/BlueMonday/go-scryfall) | Card search/validation: resolving an edition's card list, and validating OCR/backfill guesses | Single shared, lazily-constructed, 8 req/s rate-limited client (`getScryfallClient`) — see §5.5 |

None of these integrations use authentication. None but the Scryfall REST
API client has explicit rate limiting applied by this tool.

---

## 4. Catalog-crawl algorithm (`run()`, no positional args)

```
i := pageOpt
loop:
  resp, err := getProducts(i * 50)      # Scalefast API, 50 items/page
  if err: log, BREAK (do not increment i first)
  i++
  if resp has zero products: BREAK
  for each product in resp.Products:
    if any of product.Descriptions[*].Title matches the skip list (§4.1):
      print `"<title>",<releaseDate>` to stdout, skip this product
      continue
    link := "https://secretlair.wizards.com/us/product/" + product.ProductID
    scrape & dump link (§5, §6) — on error, log and continue to next product
                                    (does NOT abort the crawl or affect exit code)
print "In the future you can start from page" (i - 2)  # see §4.2
return 0   # ALWAYS 0 for this mode, see §7
```

`product.ReleaseDate` (an RFC3339 timestamp in the Scalefast response) is
formatted as `YYYY-MM-DD` and passed through as the `// DATE:` line in the
output file (§6.1).

### 4.1 Skip list

A product is skipped (never scraped) if **any** of its `Descriptions`
entries — across all languages present in the API response, not just
English — has a `Title` containing any of the following substrings
(case-sensitive, exact strings as they appear in `main.go`):

```
Bundle, BUNDLE, Festival in a Box, Transformers TCG, DRAGON'S ENDGAME,
[both "Secret Lair" AND "Deck" in the same title], They're Just Like Us but,
Heads I Win, Tails, Deluxe Collection, Heroes of the Borderlands,
Welcome to the Hellfire Club, D&D Sapphire Anniversary, Fan Merch,
30th Anniversary Edition, Japanese, " JP", " SP", Countdown Kit
```

This list encodes accumulated exclusions for non-card-decklist product
types (bundles, foreign-language SKUs, non-SLD tie-ins) that would either
fail to parse meaningfully or don't belong in the target decklist format.
`out.csv` in the repo root is a **static, untracked, gitignored leftover**
capture of exactly this skip-line stream from one past full-catalog run —
it is not read or regenerated by the tool; it exists only as a historical
reference of what got excluded and why.

### 4.2 The resume-page contract with CI

The final line printed to stdout in catalog-crawl mode,
`"In the future you can start from page" (i - 2)`, is the **only**
machine-readable signal this tool emits about crawl progress. The daily
workflow ([.github/workflows/new-sld-pr.yml](.github/workflows/new-sld-pr.yml))
captures the tool's stdout via process substitution, keeps the last line
printed, and extracts the last whitespace-separated field with
`awk '{print $NF}'` to get the page number for the *next* run's
`SLD_LAST_PAGE` repository variable.

The `i - 2` (not `i - 1`) offset is deliberate: it resumes from the **last
page that actually returned products**, not the first empty page past it,
so that a product added to that page after this run (Scalefast sorts by
`release_date`, and late-arriving items can land on an already-seen page)
is picked up by the next run instead of being permanently missed. This
means every run intentionally **re-scrapes** its last populated page; the
workflow only opens a PR for genuinely new files (git untracked-file
detection), so re-scraping an already-committed product is a no-op for the
output, just wasted work.

**Caveat**: if the very first `getProducts` call in a run fails (network
error, bad response), the loop breaks before `i` is ever incremented, so
the printed resume page becomes `pageOpt - 2` — two pages *before* where
the run started, not a safe no-op. There is no distinction in the printed
output between "crawl completed normally" and "crawl aborted early due to
an error"; both cases print the same message shape. See
[todo/001-ci-exit-code-and-next-page-marker.md](todo/001-ci-exit-code-and-next-page-marker.md).

**Also note**: `run()` always returns exit code `0` for catalog-crawl mode,
regardless of how many individual products failed to scrape or how early
the crawl aborted (see §7) — the process exit code carries no information
about crawl health; only the printed page number and the stderr log do.

---

## 5. Product-page scraping and matching (`scrapeProduct`)

Applies identically whether reached via catalog-crawl mode or explicit-URL
mode.

### 5.1 Fetch and title extraction

1. `GET` the product URL via `retryablehttp.Get` (package-level client,
   default retry policy, default — i.e. non-silenced — logger).
2. Parse the HTML with `goquery`.
3. Extract the title from `h1[class="product-title"]`.
4. Run it through `cleanTitle` (§8) to get `(cardSet.Filename, cardSet.Title)`.

### 5.2 Card-line extraction

1. Primary path: every `li` inside `div[class="force-overflow"] ul` is
   treated as one card line and passed to `processLine` (§9), which in turn
   calls `cleanLine` (§8) on the raw text.
2. Fallback path (only if the primary path produced zero cards): the HTML
   of `div[id="collapse2"] div[class="force-overflow"] p[class="product-information"]`
   is split on literal `<br/>` tags, and each resulting fragment is passed
   through `processLine` the same way.
3. If both paths produce zero cards, `scrapeProduct` returns the error
   `"no cards found"` and the product is skipped entirely (no file written).

### 5.3 Edition matching against Scryfall

A **local, shadowing** variable named `cleanTitle` (not to be confused with
the package-level `cleanTitle` function of the same name — this is a
naming choice worth being careful with when editing this function) is
derived from `cardSet.Title` by stripping the literal suffixes
`" Foil Edition"`, `" Raised"`, and `" Galaxy"` — this stripped form is
used *only* for matching against Scryfall edition titles, never for output.

For each `(Title, URI)` header scraped from `scryfall.com/sets/sld` (§10),
in scrape/DOM order:

1. **Match test**: lowercase both the local `cleanTitle` and `header.Title`;
   match if `fuzzy.Match(a, b)` (subsequence fuzzy match, from
   `lithammer/fuzzysearch`) **or** either string contains the other as a
   substring. The **first** header satisfying this wins — there is no
   scoring or "best match" selection among multiple headers that could
   match.
2. On a match, re-run the exact query embedded in that header's URI
   (`?q=...`) against the Scryfall REST API via `searchURI` → `search`
   (§10.2). If this errs, or returns zero results, log and move on to the
   **next** header candidate (this header is not treated as authoritative,
   the loop continues) — matching does *not* stop at the first *title*
   match, only at the first title match that also yields usable Scryfall
   results.
3. **If the result count doesn't equal the scraped card count**
   (`len(results) != len(cards)`): the scraped `cards` list is **discarded
   and replaced wholesale** by `results`, via `inheritFinish`. Every
   replacement card is given `Count = 1`. Its `Foil`/`Etched` are inherited
   from the *scraped* card with the same name under `normalizeCardName`
   (§8.5.1), if one exists — this preserves per-card finish for a
   mixed-finish product page. Only when no scraped card matches a given
   replacement card at all does it fall back to copying `Foil`/`Etched`
   from `cards[0]` (the first originally-scraped card), as a best-effort
   default for a card this pass otherwise has no finish information for.
4. **Otherwise** (`len(results) == len(cards)`): `matchCardNumbers` (§8.5)
   aligns the two lists by name — exact match first, then a
   punctuation/case-insensitive fallback that also adopts Scryfall's
   spelling of the name (see §8.5 for the two-round algorithm).
5. Once a header produces usable results (step 2 succeeded), the loop
   **stops** (`foundMatch = true; break`) — no further headers are tried
   even if a later one might have matched more cards.

If **no** header ever produces usable results, `foundMatch` stays `false`
and OCR is unconditionally forced on for this product
(`doOCR = true`), regardless of the `-ocr` flag's value.

### 5.4 Sort

Immediately after the matching phase (**before** OCR and backfill run),
`cards` is sorted by `sortCardsByNumber`, using `sort.SliceStable` on:
1. `collectorNumberValue(number)` ascending (parses the leading run of
   ASCII digits in the number string as an integer; a card with no number
   yet, or a non-numeric-leading number, sorts as `0` — i.e. first);
2. tiebreak: lexical string comparison of the raw number field.

The sort is deliberately **stable**, not merely `sort.Slice`: in the common
case where OCR is about to run (§5.3 found no match), every card's `Number`
is still `""` at this point, so every pairwise comparison in the sort is
"equal" under this ordering. The OCR loop immediately after (§5.5) assumes
`cards[i]` lines up positionally with the `i`-th image in the product's
gallery (in original page/DOM order); stability is what guarantees that a
run of "equal" (all-unnumbered) cards keeps that original scrape order
through the sort, rather than an unstable sort being free to reorder them
and silently desync the OCR loop's image-to-card mapping.

### 5.5 OCR fallback

Runs when `doOCR` is true (either the `-ocr` flag, or forced because no
edition match was found, §5.3).

1. **Fold-mode detection**: read `h2[class="pdp_title"]`'s text
   (`galleryTitle`). If it contains `" ("`, parse the last
   whitespace-separated token, strip a leading `(` and trailing `)`, parse
   it as an integer (`expectedNumber`). If `expectedNumber / 2 == len(cards)`,
   set `foldMode = true` — this handles product galleries that show two
   images per card (front and back) rather than one, so the physical image
   index needs to be halved to reach the corresponding logical card index.
2. Iterate every `figure a` element on the page (`EachWithBreak`, index
   `i`, stops entirely — not just skips — if `i >= len(cards)` after
   fold-mode halving, logging `"Found more images than loaded cards..."`):
   - If `cards[i]` already has a `Number`, skip (return `true`, continue).
   - If the image has no `href`, skip.
   - Resolve a relative `href` (starting with `/`) against
     `https://secretlair.wizards.com`.
   - Download the image and run Tesseract OCR on it via
     `getNumberFromLink` → `extractNumber` (§11) to get a candidate number
     string. On any error (download or gosseract failure), log and skip
     this image (leaving the card unnumbered for now).
   - **Validate** the candidate: issue a live Scryfall search for
     `"<card name> cn:<candidate>"`. If it errs or returns zero results,
     log `"validation failed"` and **do not** assign the number — this
     card stays unnumbered and is eligible for the backfill pass (§5.6).
   - If valid: adopt the canonical Scryfall spelling of the name
     (`canonicalName`, §8.4) and assign the number.

### 5.6 Backfill

Runs **unconditionally** at the end of `scrapeProduct` (regardless of
whether OCR ran), if any card is still missing a `Number` after §5.3–§5.5:

1. Among cards that **do** have a number, find the one whose number
   **string is longest** (by character count, not by numeric value — see
   the note below) and record its numeric value (`cn`, computed by
   trimming leading zeros and parsing as an int) and its position `pos` in
   the (already-sorted, §5.4) `cards` slice.
2. If no card has any number at all, log `"...worth a shot"` and stop —
   there is nothing to anchor a guess to.
3. If the anchor's numeric value `cn <= 0` (e.g. it failed to parse),
   stop silently.
4. Otherwise, for every still-unnumbered card at index `j`: guess
   `num = cn + (j - pos)` — i.e., assume collector numbers are laid out as
   a simple contiguous integer sequence matching the (sorted) card order,
   anchored at the known card. Validate the guess with a live Scryfall
   search (`"<name> cn:<guess>"`) exactly as in §5.5; on success, adopt the
   canonical name and the guessed number; on failure, log and leave that
   card permanently unnumbered for this run.

**Note on "longest, not largest"**: the anchor-selection heuristic compares
string *length*, not the parsed numeric value. Given the domain constraint
that SLD collector numbers are always 3+ digits (§11.1), this rarely
diverges from "numerically largest" in practice, but the two are not the
same rule — a hypothetical shorter number that happens to be numerically
larger than a padded/longer one would lose to the longer string here.

---

## 6. Output

### 6.1 File format (`dumpCards`)

```
// NAME: <cardSet.Title>
// SOURCE: <link>
// DATE: <releaseDate>          (this line omitted entirely if releaseDate == "")
<count> [SLD<:number>] <name>[ [foil]][ [etched]][ [token]]
...one line per card, in cardSet.Cards order (i.e. post-sort, §5.4)...
```

- `<:number>` is literally the string `:` + the collector number if
  `card.Number != ""`, or nothing at all (producing the literal text
  `[SLD]` with no colon) if the number was never resolved.
- The three optional suffix tags are emitted **in this fixed order**
  (`[foil]`, then `[etched]`, then `[token]`), each independently, so a
  card can carry more than one (e.g. `... [foil] [etched]`).
- Trailing newline after every card line; no blank line at end of file
  beyond that.

### 6.2 Destination

- **Explicit-URL mode** (`filename == ""` is passed to `dumpCards`): the
  full decklist text above is written to **stdout**. No file is created.
- **Catalog-crawl mode**: `dumpCards` is called with `cardSet.Filename`
  (derived in §8.4/cleanTitle) as the filename; the tool creates
  `<filename>.txt` via `os.Create` **in the current working directory** —
  there is no `-o`/`--output-dir` flag. The daily workflow relies on this
  by setting `working-directory:` to the target path inside the cloned
  upstream fork before invoking the tool (see AGENTS.md §2 / the workflow
  file). After a successful file write, `log.Printf("Created '%s' (%s)", ...)`
  is emitted to **stderr** (not part of the stdout contract described in
  §4.2).

---

## 7. Exit codes

| Path | Exit code |
|---|---|
| Scryfall header load fails (`loadScryfallHeaders` errors) | `1` |
| Explicit-URL mode, **all** URLs scraped/dumped successfully | `0` |
| Explicit-URL mode, **any** URL failed (all URLs are still attempted) | `1` |
| Catalog-crawl mode, `-page` not given (stays at default `-1`) | `1` |
| Catalog-crawl mode, ran to completion (any number of individual product failures) | `0` |
| Catalog-crawl mode, aborted early by a `getProducts` fetch error | `0` — **same as successful completion** |

The last row is the significant asymmetry to be aware of: catalog-crawl
mode's exit code carries **no** information about whether the crawl
completed normally, partially failed, or aborted immediately. Only the
resume-page number printed to stdout (§4.2, itself ambiguous per its
caveat) and the stderr log distinguish these cases. See
[todo/001-ci-exit-code-and-next-page-marker.md](todo/001-ci-exit-code-and-next-page-marker.md).

---

## 8. Name and title cleaning

### 8.1 `normalizeSpaces(s string) string`

Maps every rune for which `unicode.IsSpace` is true to a plain ASCII space
(`' '`). Applied as the **first** transformation in both `cleanLine` and
`cleanTitle`, before any `strings.Replace`/`strings.NewReplacer` call. This
ordering matters: `strings.NewReplacer` performs a single left-to-right
pass and never re-scans text it has already emitted, so a whitespace
variant that survived past the replacer (e.g. a non-breaking space inside
a phrase like `"Secret Lair x "`) can permanently prevent a
literal-string match (`"Secret Lair x "`) from firing, even if a
replacement pair for that exact whitespace character is *also* present in
the replacer — because the two pairs are needed to fire in a single pass
in the right order, on text the replacer hasn't already rewritten out from
under itself. Normalizing to plain spaces up front sidesteps the ordering
problem entirely.

### 8.2 `cleanLine(cardLine string) (name string, count int, tags detectedTags, err error)`

Applied to one scraped bullet/line of card-list text. `detectedTags` is a
small struct (`Foil`, `Etched`, `Token bool`) recording which finish/type
tags were actually consumed as real variant markers during the steps
below — as opposed to a tag substring that turned out to be part of the
card's own name and was therefore left alone. `processLine` (§8.6) uses
this report directly, rather than re-deriving Foil/Etched/Token from the
raw line independently. Steps, in order:

1. `normalizeSpaces`.
2. Replace curly quotes: `’`→`'`, `”`→`"`, `“`→`"`.
3. `TrimSpace`.
4. `strings.SplitN(cardLine, "x ", 2)` — must yield exactly 2 fields
   (a count prefix and the rest of the line) or the function returns the
   error `"unexpected line format"`. Splitting on the **first**
   occurrence of `"x "` (not naive `Split`) means a card name that itself
   contains `"x "` (e.g. "Nyx Lotus") does not break this step.
5. Parse the first field as an integer count (`strconv.Atoi`); a
   non-numeric prefix returns the error `"invalid number in line"`.
6. `TrimSpace` the remainder — this is the working `cardLine` for the rest
   of the function.
7. If the remainder contains `(`, truncate everything from `(` onward
   (drops parenthetical annotations like `(Retro Frame)`).
8. **Foil-prefix stripping**: if the remainder contains `"Foil"`, is not
   exactly `"Foil"`, does not contain `"Foil to Conspiracy"`, and does not
   end with `"Foil Edition"` or `"Foil Etched"` — split on the first
   `"Foil"` and keep everything **after** it. This strips leading variant
   descriptors that precede the word "Foil" (e.g. `"Galaxy Foil Sol Ring"`
   → `"Sol Ring"`), while the exclusions protect the literal card named
   "Foil" and the card "Lavinia, Foil to Conspiracy" (whose name contains
   "Foil" mid-string, not as a prefix marker) from being truncated. When
   this branch fires, `tags.Foil` is set — this is a genuine foil-variant
   marker being consumed, not just name text.
9. **Phyrexian-tag removal**: strip the literal substring `"Phyrexian"`
   unless the remainder also contains one of `"Tower"`, `"Crusader"`,
   `"Metamorph"`, `"Reclamation"`, `"Arena"`, or `"Unlife"` — these six
   guard real card names that are themselves Phyrexian-named (e.g.
   "Phyrexian Tower", "Phyrexian Metamorph", "Phyrexian Arena").
10. **Tag-word stripping**: for each tag in `nameTags` (§8.3), unless it is
    explicitly protected for this line (`keepEtched`/`keepFoil`, computed
    just before this step by checking whether the remainder contains one
    of a fixed list of real card names built from "Etched" or "Foil" — see
    §8.3), remove every word-boundary match (original or lowercased form)
    via the precompiled `nameTagRegexps`. Whenever a tag's regex actually
    matches and strips something (compared before/after), the
    corresponding `detectedTags` field is set: `"Foil"` → `tags.Foil`,
    `"Etched"` → `tags.Etched`, `"Foil-etched"` → both, `"Token"`/`"Tokens"`
    → `tags.Token`. A tag skipped by `keepEtched`/`keepFoil` for this line
    never reaches the strip, so it never sets its flag — this is what
    keeps e.g. "Lavinia, Foil to Conspiracy" (no real foil-variant marker
    in that line) from being reported as `tags.Foil`, while "Etched
    Champion" appearing after an actual `"Foil "` prefix (step 8) still
    correctly reports `tags.Foil` (from step 8) without also reporting
    `tags.Etched` (protected here, since "Etched Champion" is the card's
    own name).
11. **Flavor-name removal**: if the remainder contains `" as "`, keep only
    the text before it (strips "X as Y" flavor-name framing).
12. **"Bob Ross Drop" special case**: if the remainder contains
    `" with art"`, keep only the text before it.
13. **DFC (double-faced card) standardization**: if the remainder contains
    `"//"` but not `" // "`, insert surrounding spaces; same for a lone
    `" / "` (single slash) that isn't already part of `" // "`.
14. **Single-face reduction**: split on `" // "` and keep only the first
    face's name (this tool tracks front faces only).
15. Rename `"Sticker Sheets"` → `"Sticker sheet"` (matches upstream's
    naming for that product type).
16. Fixed typo corrections (exact substring replacements, unconditional):
    `Xenegos`→`Xenagos`, `Death Render`→`Deathrender`,
    `All is Dust`→`All Is Dust`, `Mistep`→`Misstep`,
    `Triumph of Hordes`→`Triumph of the Hordes`.
17. `TrimSpace` and return `(name, count, tags, nil)`.

### 8.3 `nameTags` and word-boundary matching

```go
var nameTags = []string{
    "Full-Text", "Full-Art", "Full-art", "Alt-Art",
    "Reversible", "Old Frame", "Retro Frame",
    "Poster", "Stained Glass",
    "Foil-etched", "Etched", "Foil", "Tokens", "Token",
    "Different", "Hand-Drawn", "Borderless",
    "Showcase", "Left-Handed", "Edition", "cards", "Japanese",
    "Regular Human Guy", "Ichor-E", "DFC", "Italian-language", "*",
    "REVERSIBLE",
}
```

Each tag is compiled once (`nameTagRegexps`, package-level `init`-time
closure) into a regex that:
- matches the tag verbatim **and** its lowercased form, when those differ
  (a tag that is already all-lowercase, e.g. `"cards"`, matches only that
  exact casing — no separate all-caps variant is synthesized for it);
- is anchored with `\b` on whichever end(s) touch a "word character"
  (letters, digits, `_`) — so `"Edition"` cannot match inside
  "Expedition", but a symbol-only tag like `"*"` (no word-character edges)
  is matched anywhere it appears, unchanged from naive substring removal.

Before this loop runs, two guards are computed for the current line:
- `keepEtched`: true if the line contains one of "Etched Champion",
  "Etched Cornfield", "Etched Familiar", "Etched Host", "Etched
  Monstrosity", "Etched Oracle", or "Etched Slith" (the seven real Magic
  cards whose own name starts with "Etched"). If true, the `"Etched"` tag
  is skipped for this line.
- `keepFoil`: true if the remainder is exactly `"Foil"` (the card literally
  named "Foil") or contains `"Foil to Conspiracy"` (Lavinia's card). If
  true, the `"Foil"` tag is skipped for this line.

These lists are known-exhaustive **as of the time they were written**, not
guaranteed exhaustive against future Magic card names. Adding a new tag to
`nameTags` should be checked against a live Scryfall name search for
collisions before shipping (see AGENTS.md §4.3/§6).

### 8.4 `cleanTitle(title string) (filename string, name string)`

Applied to the product page's `<h1>` title. Produces two related but
distinct strings: `filename` (used as the on-disk basename, colons
replaced with hyphens) and `name` (used for display and as the `// NAME:`
line in the output file, colons preserved). Steps, in order:

1. `normalizeSpaces`.
2. **Pipe handling**: if the title contains `|`, replace the first
   occurrence of `" |"` with `":"`. If the *original* (pre-replacement)
   title additionally contains `"Extra Life"`, instead take only the
   segment before the first `" | "` from the original title, and if the
   original ended with `"Foil Edition"`, re-append `" Foil Edition"` to
   that segment. (This handles titles like
   `"Secret Lair x Extra Life | Avatar Foil Edition"`, keeping "Avatar" out
   of the fundraiser-campaign framing while preserving the finish suffix.)
3. Strip pricing: split on `" $"`, keep the first segment.
4. Apply the package-level `replacer` (a `strings.NewReplacer` over the
   pairs in `replacerStrings`, §8.4.1) in a single pass.
5. Unless the title (post-replacer) contains `"High"`, strip the first
   occurrence of the literal prefix `"Secret Lair "` (note the trailing
   space, and the absence of "x " here — the `"Secret Lair x "` case was
   already handled by the replacer in step 4; this step catches the
   non-"x" phrasing, e.g. `"Secret Lair Bloomburrow"`). The `"High"`
   exception preserves the literal product line "Secret Lair High" as a
   name.
6. Replace `"S.P.E.C.I.A.L."` with `"SPECIAL"` (a Fallout tie-in title has
   too many periods to search for comfortably).
7. If the title now ends with exactly `"Foil"`, append `" Edition"`.
8. `name := TrimSpace(title)` — this is the second return value, and
   becomes `cardSet.Title`.
9. `filename := TrimSpace(strings.ReplaceAll(title, ":", "-"))` — this is
   the first return value.

#### 8.4.1 `replacerStrings` (applied as one `strings.NewReplacer` pass)

```
’ → '            ‘ → '            ® → (removed)      ™ → (removed)
﻿ → (removed)                ​ → (removed)
< → (removed)     > → (removed)   / → (removed)       \ → (removed)
* → (removed)     " - " → " "
" Is in " → " is in "             " is In " → " is in "
Regular → (removed)               "DD " → (removed)
"Secret Lair x " → (removed)
"(English)" → (removed)           English → (removed)  " EN" → (removed)
"   " → " "       "  " → " "     (collapse runs of spaces, applied last)
```

### 8.5 `matchCardNumbers(cards, results []CardData)` and canonical names

Two-round, in-place alignment used when the scraped card count exactly
matches the Scryfall edition's card count (§5.3 step 4):

- **Round 1 — exact match**: for each scraped card (in order), find the
  first result with a non-empty `Number` whose `Name` is byte-for-byte
  identical; assign its number and clear the result's `Number` so it can't
  be claimed twice.
- **Round 2 — normalized match**: for each scraped card still without a
  number, find the first *remaining* result whose name matches after
  `normalizeCardName` (§8.5.1) on both sides; assign its number **and**
  overwrite the scraped card's `Name` with the result's (Scryfall's)
  spelling, logging the substitution. This is what recovers from upstream
  typos or spurious punctuation in the scraped title (e.g. the product
  page listing `"Dosan, the Falling Leaf"` for the card actually named
  "Dosan the Falling Leaf") without needing a hardcoded fix per misspelling.

Running exact matches first (rather than only the normalized pass) exists
specifically so that two similarly-named cards in the same product can't
steal each other's slot via the looser comparison.

`canonicalName(results, name)` performs the equivalent single-name lookup
used by the OCR (§5.5) and backfill (§5.6) validation paths: given a
Scryfall search result set (typically one card, from a `name cn:number`
query) and a scraped name, if the result's name differs from the scraped
one but is equal under `normalizeCardName`, adopt the result's spelling.

#### 8.5.1 `normalizeCardName(name string) string`

Maps every rune to its lowercase form if it is a letter or digit
(`unicode.IsLetter`/`unicode.IsDigit`), and drops every other rune
entirely (punctuation, spaces, symbols). Two names compare equal under
this function if and only if they agree on their letters and digits,
ignoring case, spacing, and punctuation entirely.

### 8.6 `processLine(cards []CardData, line string) ([]CardData, error)`

Wraps `cleanLine` with duplicate-merging and the "Different" expansion.
On a `cleanLine` error, the input `cards` slice is returned unchanged
alongside the error (safe to call in a loop over many lines, one bad line
does not lose prior progress).

- `card.Foil`, `card.Etched`, `card.Token` are set directly from the
  `detectedTags` `cleanLine` returns (§8.2) — not from an independent
  check of the raw line. This is what keeps a card whose real name happens
  to contain "Foil" or "Etched" (e.g. "Lavinia, Foil to Conspiracy", the
  seven "Etched ___" cards) from being misclassified as having that
  finish, since `cleanLine`'s own `keepFoil`/`keepEtched` guards (already
  needed for the name-cleaning itself) are the single source of truth for
  whether an occurrence of one of these words was a real variant marker.
- If the raw line contains `"Different"`, the count is expanded: `num`
  identical entries are appended, each with `Count = 1` (used for lines
  like "3x Different Full-Art Lands", representing N distinct/random
  prints tracked as N separate output lines rather than one line with
  Count=3). This branch **always appends** — it does not consult or merge
  with any existing entry, even across repeated calls with the same name.
- Otherwise, the existing `cards` slice is scanned for an entry with the
  same `Name`, `Foil`, and `Etched` (not `Token`) as the new card; if
  found, its `Count` is incremented by `num` instead of a new entry being
  appended.

---

## 9. Scryfall integration detail

### 9.1 `loadScryfallHeaders(ctx) ([]scryfallHeader, error)`

`GET https://scryfall.com/sets/sld`, parsed with `goquery`. For every
element matching `.card-grid-header-content`: take its text, split on `•`
and keep the first segment (drops the trailing card-count annotation
Scryfall renders next to each edition name), trim whitespace → `Title`;
take the `href` of the first `a` descendant → `URI` (a prebuilt Scryfall
search URL with a `q=` query parameter already encoded for that edition).
Order of the returned slice matches DOM order on the page (Scryfall lists
Secret Lair editions in some site-defined order — not alphabetical, not
guaranteed stable across Scryfall site changes).

### 9.2 `getScryfallClient()` and rate limiting

A single `*scryfall.Client` is constructed exactly once per process
(`sync.Once`), configured with:
- `WithUserAgent("sldownloader/1.0")`
- `WithLimiter(ratelimit.New(8))` — 8 requests/second, leaving margin under
  Scryfall's documented "less than 10 requests per second" requirement.

**This sharing is load-bearing.** `go-scryfall`'s rate limiter is a field
on the `*scryfall.Client` struct; a limiter only paces requests made
through the *same* client instance. Constructing a new client per call
(as an earlier version of this code did) defeats the limiter entirely —
each fresh limiter starts unthrottled, so a tight loop of searches (the
OCR-validation and backfill-validation loops, §5.5/§5.6, each issue one
search per card) can burst well past 10 req/s and trigger Scryfall's
`rate_limited` error, which per Scryfall's own error text risks an IP-level
network block if ignored.

### 9.3 `search(ctx, query) ([]CardData, error)`

Calls `Client.SearchCards` with `Unique: UniqueModePrints`,
`Order: OrderSet`, `Dir: DirAsc` (ascending by collector number),
`IncludeExtras: true`. For each returned card:
- **Excluded**: cards whose `PromoTypes` includes `"sldbonus"` (Secret
  Lair bonus cards — tracked by a separate mechanism upstream, not this
  tool's concern).
- **Excluded**: cards whose `CollectorNumber` ends in `★` (the star
  suffix Scryfall uses for older, now-duplicated foil-only printings).
- **Name**: only the first face is kept (`strings.Split(card.Name, " // ")[0]`),
  matching the single-face convention used throughout (§8.2 step 14).
- **Number**: `card.CollectorNumber`, with a literal `"a"` suffix appended
  if the card has more than one `CardFace` (Scryfall's own convention for
  distinguishing DFC/split-card printings that share a collector number
  base).
- `Token` is set from whether `TypeLine` contains `"Token"` (this field is
  populated but not currently read by anything downstream — flagged for
  awareness, not necessarily a defect).

**Pagination is not followed** — only the first page of results (up to
Scryfall's page size) is used. For a single Secret Lair edition query this
has not been observed to matter in practice (editions are small), but it
is a real, silent truncation risk if ever queried against something
larger. See
[todo/003-scryfall-search-pagination.md](todo/003-scryfall-search-pagination.md).

### 9.4 `searchURI(ctx, uri) ([]CardData, error)`

Parses the `uri` (one of the prebuilt search URIs from §9.1), extracts its
`q` query parameter, and calls `search` with that exact query string —
i.e., this re-executes precisely the search Scryfall's own site would run
for that edition's "view all prints" link, guaranteeing the same
inclusion/exclusion semantics.

---

## 10. OCR (`getNumberFromLink`, `extractNumber`)

### 10.1 `getNumberFromLink(link string) (string, error)`

1. Construct a `gosseract.Client` (one per call — **not** reused across
   calls, unlike the Scryfall client; see
   [todo/008-reuse-gosseract-client.md](todo/008-reuse-gosseract-client.md)
   for the performance implication).
2. `SetWhitelist("0123456789 ™ ©")` — constrains Tesseract's recognition
   alphabet to digits, space, and the two terminator glyphs described
   below. Errors from this call are surfaced (not ignored).
3. Download the image bytes (`getImageBytes`, via a fresh silenced
   `retryablehttp` client, no shared rate limit or timeout beyond the
   library default).
4. `SetImageFromBytes(data)` — errors surfaced.
5. `client.Text()` — run OCR, get the recognized text.
6. Split on whitespace into `fields`, then call `extractNumber` twice in
   sequence (§10.2): once with `minLen = 3`, and — only if that returns an
   empty string — again with `minLen = 2`.

### 10.2 `extractNumber(fields []string, minLen int) string`

Scans `fields` in order. For each field:
- If it is exactly `"™"` or `"©"`, **return `""` immediately** — these are
  treated as hard terminators, on the assumption that OCR noise past the
  card's copyright/trademark line is not the collector number. This means
  a genuine number appearing *after* a stray `™`/`©` misread earlier in
  the OCR'd text is unreachable and the whole scan yields nothing, on
  **both** the `minLen=3` and `minLen=2` passes independently (each call
  re-scans `fields` from the start).
- Otherwise, if `len(field) > minLen` and the field parses cleanly as an
  integer (`strconv.Atoi`), return it — the **first** qualifying field
  wins.
- If nothing qualifies, return `""`.

### 10.3 The 3-digit floor is a deliberate domain constraint

The two-pass call in `getNumberFromLink` (`minLen=3` first, i.e. requiring
**4+** characters to match on the first pass; falling back to `minLen=2`,
i.e. **3+** characters, only if the first pass finds nothing anywhere in
the field list) means the OCR path will never return a 1- or 2-digit
collector number. **This is intentional and must not be "fixed" to accept
shorter numbers**: collector numbers in the `SLD` set are always 3 or more
digits. This has been raised in review and explicitly rejected by the
maintainer for exactly this reason — see AGENTS.md §4.2. Widening this
floor would not be a bug fix; it would break a correct assumption about
this specific set's numbering.

---

## 11. Dependencies

| Module | Purpose |
|---|---|
| `github.com/BlueMonday/go-scryfall` | Scryfall REST API client (card search) |
| `github.com/PuerkitoBio/goquery` | HTML parsing/querying (jQuery-style selectors) for both Wizards and Scryfall HTML scraping |
| `github.com/hashicorp/go-cleanhttp` | Plain, non-pooled-transport-sharing HTTP client, used once for the Scryfall headers scrape |
| `github.com/hashicorp/go-retryablehttp` | HTTP client with built-in retry/backoff, used for the Wizards product pages, the Scalefast catalog API, and OCR image downloads |
| `github.com/lithammer/fuzzysearch` | Subsequence fuzzy string matching, used to match a cleaned product title against Scryfall edition titles |
| `github.com/otiai10/gosseract/v2` | cgo bindings to Tesseract OCR |
| `go.uber.org/ratelimit` | Leaky-bucket rate limiter, used to cap the shared Scryfall client at 8 req/s |

Transitive: `andres-erbsen/clock`, `andybalholm/cascadia` (goquery's CSS
selector engine), `google/go-querystring`, `golang.org/x/net`,
`golang.org/x/text`.

---

## 12. Known limitations summary

A consolidated pointer list; each item is detailed either inline above or
in its own `todo/` file:

- Catalog-crawl mode's process exit code never reflects failure (§7) —
  [todo/001](todo/001-ci-exit-code-and-next-page-marker.md)
- No shared HTTP client, timeout, or status-code checking for the
  Wizards/Scalefast fetches (§3) —
  [todo/002](todo/002-shared-http-client-with-timeout-and-status-checks.md)
- Scryfall search results are not paginated (§9.3) —
  [todo/003](todo/003-scryfall-search-pagination.md)
- No offline/fixture test coverage for any network-touching function
  (§table in AGENTS.md §3) —
  [todo/004](todo/004-golden-file-regression-tests-for-scraping.md)

See [todo/README.md](todo/README.md) for the full backlog, including items
that are process/tooling improvements rather than direct spec-level
correctness issues.
