# AGENTS.md

Guidance for AI coding agents (and human contributors) working in this repository.
This file is about **how to work here**: build/test mechanics, code conventions,
domain gotchas, and workflow rules. For **what the tool does**, see
[SPECIFICATIONS.md](SPECIFICATIONS.md). For user-facing install/usage docs, see
[README.md](README.md).

---

## 1. What this repository is

`sldownloader` is a single-binary Go CLI (package `main`, two files: `main.go`
and `scryfall.go`) that scrapes Secret Lair Drop product pages from
`secretlair.wizards.com`, extracts card names and collector numbers using a
combination of HTML scraping, Scryfall lookups, OCR, and hand-tuned string
heuristics, and writes out `.txt` decklist files formatted for
[taw/magic-preconstructed-decks](https://github.com/taw/magic-preconstructed-decks).

A GitHub Actions workflow ([.github/workflows/new-sld-pr.yml](.github/workflows/new-sld-pr.yml))
runs this tool daily, diffs the output against a fork of that upstream repo, and
opens a PR with any newly discovered drops.

There is no `AGENTS.md`-style convention beyond this file — this is a small,
single-maintainer repo. Read it in full before making non-trivial changes.

---

## 2. Build, test, and run

### The one non-obvious thing: CGO and Tesseract

OCR support comes from [gosseract](https://github.com/otiai10/gosseract), which
is a cgo wrapper around Tesseract/Leptonica. **`go build`, `go vet`, and `go test`
all require the Tesseract and Leptonica C headers to be installed**, or the
build fails with something like:

```
tessbridge.cpp:5:10: fatal error: 'leptonica/allheaders.h' file not found
```

Install the libraries first:

```bash
# Debian/Ubuntu (this is what CI does)
sudo apt-get install -y tesseract-ocr libleptonica-dev libtesseract-dev

# macOS
brew install tesseract leptonica
```

On Debian/Ubuntu the headers land on the default include path and nothing
further is needed. **On macOS (Homebrew), the headers live under the Homebrew
prefix and are not on the default cgo search path**, so every build/vet/test
invocation needs:

```bash
export CGO_CPPFLAGS="-I$(brew --prefix)/include"
export CGO_LDFLAGS="-L$(brew --prefix)/lib"
```

Export these once per shell session (or prefix every command) before running
any of the commands below. Forgetting this is the single most common source
of a confusing local build failure in this repo — if `go build` fails with a
missing-header error, this is almost certainly why.

### Commands

```bash
go build ./...   # build the binary
go vet ./...      # static checks — must be clean, CI runs it too
go test ./...     # run main_test.go

# lint with .golangci.yml, at the version CI pins in test.yml
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...
```

Run golangci-lint through `go run` at the pinned version rather than a
locally installed binary: a build made with a Go release older than the
one in `go.mod` fails to type-check this module.

There is currently no `Makefile`/`justfile` wrapping the CGO flags (see
[todo/014-makefile-for-cgo-flags.md](todo/014-makefile-for-cgo-flags.md)).

### Running the tool locally

```bash
# Single product page (fastest way to iterate on a parsing bug)
./sldownloader https://secretlair.wizards.com/us/product/<id>

# Catalog crawl, starting from page 0 (the very beginning)
./sldownloader -page 0

# Force OCR even when the Scryfall edition match succeeds
./sldownloader -ocr https://secretlair.wizards.com/us/product/<id>
```

Every run does a live Scryfall lookup (`https://scryfall.com/sets/sld`) to
build the header index and live HTTP fetches of the product pages; only
`go test` runs offline (§6).
Expect a single product run to take several seconds due to network I/O and
the Scryfall rate limiter (2 searches/s, see §4.4).

---

## 3. Code map

| File | Contents |
|---|---|
| [main.go](main.go) | Everything except the Scryfall client: CLI entrypoint (`run`/`main`), Scalefast catalog API client, product-page scraping (`scrapeProduct`, run as `fetchProductPage`, `parseCardList`, `matchEdition`, `ocrNumbers` and `backfillNumbers`), all the name/title cleaning heuristics (`cleanLine`, `cleanTitle`, `nameTags`), OCR (`getNumberFromLink`, `extractNumber`), collector-number backfill, and file output (`dumpCards`). |
| [scryfall.go](scryfall.go) | The Scryfall integration: scraping `scryfall.com/sets/sld` for per-edition search headers (`loadScryfallHeaders`), the card-name catalog that protects real names from `cleanLine` (`loadCardNames`), the rate-limited shared client (`getScryfallClient`), and card search (`search`, `searchWithClient`, `searchURI`), which tells "no such card" apart from a Scryfall failure (`isScryfallError`). |
| [main_test.go](main_test.go) | Table-driven tests for the pure string-processing functions (`cleanLine`, `cleanTitle`, `collectorNumberValue`, `normalizeCardName`, `canonicalName`, `matchCardNumbers`, `extractNumber`) and for `processLine`'s duplicate-merging behavior. The scraping pipeline is tested offline too: `parseCardList`, `galleryFoldMode` and `scrapeProduct` on short inline HTML snippets (`scrapeProduct` through a local `httptest` server), `matchEdition` and `backfillNumbers` against canned search results (`fakeSearch`). `searchWithClient`'s handling of Scryfall errors is tested against an `httptest` server replaying Scryfall's error bodies, and `getNumberFromLink` only on a blank image. Product pages are never saved into the repo: tests use hand-written snippets of just the markup the scraper reads. |
| [.github/workflows/new-sld-pr.yml](.github/workflows/new-sld-pr.yml) | The daily automation: build the tool, run it against a page range remembered in a GitHub Actions repo variable (`SLD_LAST_PAGE`), diff the output against a fork of `taw/magic-preconstructed-decks`, push a branch, open a PR upstream. Runs no tests itself. |
| [.github/workflows/test.yml](.github/workflows/test.yml) | `go build`, `go vet` and `go test -race` on every pull request and every push to `master`. Kept separate from the daily sync workflow so that pull request code never runs in a job holding its tokens. |
| [README.md](README.md) | User-facing install/usage instructions. |
| [SPECIFICATIONS.md](SPECIFICATIONS.md) | Full behavioral/data-format specification of the tool. |
| [todo/](todo/) | Improvement backlog, one file per item — see §7. |

There is no `internal/`, no subpackages, no `cmd/` layout. Everything is
`package main` in the repo root by design; this is intentionally a small tool,
not a library.

---

## 4. Domain knowledge you need before touching the code

This tool exists because Secret Lair product pages are messy, semi-structured
HTML written for humans, not machines, and matching what they say to actual
Magic cards requires several layers of fallback. Read this section before
"fixing" anything that looks like an odd special case — it is very likely
there for a reason a past run surfaced. Check `git log -p -- main.go` for the
commit that introduced a given special-case string if its purpose isn't
obvious; the history is full of one-line fixes for one specific drop.

### 4.1 The matching pipeline (high level — full detail in SPECIFICATIONS.md)

1. Scrape the product page's bullet list into
   raw `CardData` entries via `cleanLine` (name + count only, no number yet).
2. Fuzzy-match the product title against the list of Secret Lair edition
   titles scraped from `scryfall.com/sets/sld`, then pull that edition's
   card list from the Scryfall API and align by name (`matchCardNumbers`) to
   assign collector numbers.
3. If no edition matched, or specific cards are still missing a number,
   fall back to OCR against the product gallery images
   (`getNumberFromLink` → `extractNumber`), each result **validated** against
   a live Scryfall search (`name cn:<number>`) before being trusted.
4. If cards are *still* missing a number after OCR, backfill by assuming
   contiguous collector numbers relative to the highest-confidence number
   found, again validating each guess against Scryfall.
5. Sort by numeric collector number, write out a `.txt` file.

### 4.2 Collector numbers are always 3+ digits for this domain

`extractNumber` in main.go only accepts OCR'd digit strings longer than 2
characters. **This is intentional, not a bug**: Secret Lair collector numbers
in the `SLD` set are always 3 or more digits. Do not "fix" this to accept
1–2 digit numbers — a prior review suggested it and the maintainer explicitly
rejected the change for this reason. If you're ever asked to make this tool
work against a *different* Secret Lair-adjacent set with short collector
numbers, treat that as a genuinely new requirement to discuss, not a bug fix.

### 4.3 Name-cleaning is a minefield of one-off fixes

`nameTags` (main.go) and `replacerStrings` (main.go) are both lists of
string substitutions accumulated over many real Secret Lair drops, each
entry earned by a wrongly-parsed card name in a past run. Examples of traps
already fixed and **must stay fixed**:

- Tag words are stripped on **word boundaries only** (`nameTagRegexps`), not
  via naive `strings.Replace`, because naive replacement corrupted real card
  names that happen to contain a tag substring (e.g. stripping "Edition"
  naively would corrupt "Expedition Map"; stripping "Etched" naively would
  corrupt "Etched Champion").
- No cut in `cleanLine` may remove text inside a real card name. Each one
  (parentheses, the Foil prefix, Phyrexian, every tag, `" as "`,
  `" with art"`, `//`) goes through `cardNames.matches`, which skips any
  match inside a name from Scryfall's card-name catalog, loaded once per
  run. That is what keeps "Phyrexian Altar", "Isshin, Two Heavens as One"
  and "Etched Champion" intact without a per-card exception list. A new
  cut must go through `cardNames.matches` as well (and be added to the
  `cuts` in `newCardNames`), with a `TestCleanLine` case for a real name it
  could break; a new `nameTags` entry is covered automatically.
- All unicode whitespace is normalized to a plain space in one early pass
  (`normalizeSpaces`) *before* any string replacer runs. `strings.NewReplacer`
  is single-pass and never rescans its own output, so a replacer-based
  "fix" for one whitespace variant (e.g. only U+202F) will not catch others
  (e.g. U+00A0) even if you add both as pairs — this exact bug shipped once
  and is why `normalizeSpaces` exists as its own function.
- Card name spelling mismatches from the upstream page (typos, spurious
  punctuation) are **not** patched with a hardcoded string replacement for
  each one. Instead, `canonicalName`/`matchCardNumbers` adopt whatever
  Scryfall calls the card, as long as the scraped name matches after
  stripping case and punctuation (`normalizeCardName`). Prefer extending
  this general mechanism over adding another one-off `strings.Replace` for
  a new misspelling, unless the mismatch is not punctuation/case (e.g. a
  genuine word substitution like "Xenegos" → "Xenagos"). `matchCardNumbers`
  also pairs a letter-level typo by elimination when it is the only card
  left unmatched in an edition of the same size (SPECIFICATIONS.md §8.5),
  so check whether that already covers the product before adding a
  hardcoded fix; it does not cover the OCR or backfill paths, or a product
  with two such typos.

When you add a new special case, add a matching table-driven test entry in
`main_test.go` in the same commit — see the existing structure of
`TestCleanLine` and `TestCleanTitle`.

### 4.4 Scryfall rate limiting

Scryfall's [rate limits](https://scryfall.com/docs/api/rate-limits) allow
`/cards/search` **2 requests/second** (10/s for most other endpoints), and
every `search`/`searchURI` call is a `/cards/search`. Exceeding that gets a
`rate_limited` answer and a lockout of 30–60 seconds, and, per Scryfall's
own warning text, risks a network block. `search` answers a query it has
already sent in this run from memory (`searchCache`), since a Foil Edition
and its nonfoil twin send the same ones. `getScryfallClient` (scryfall.go)
enforces **2 req/s** via a single shared, lazily-initialized
`*scryfall.Client` (`sync.Once`). This sharing is
load-bearing: the underlying `go-scryfall` rate limiter lives on the client
instance, so constructing a fresh client per call (as the code used to do)
defeats it entirely — no request pacing happens without a shared client. If
you ever see per-call `scryfall.NewClient()` reappear in a diff, that is
almost certainly reintroducing this exact bug.

The limiter also has **no burst slack** (`newScryfallLimiter`).
`ratelimit.New` defaults to a slack of 10, which banks the time spent on
OCR and Wizards page fetches and then lets up to 10 searches through back
to back; `TestScryfallLimiterNoBurst` fails if that comes back. The budget
is per IP, so crawls running at once from one machine share it (§6).

A Scryfall error must never fall through to the code that handles an
empty result: an empty validation result leaves the card unnumbered, so a
throttled run would write `[SLD]` lines with no collector numbers and
exit 0. `search` returns no cards and no error only for `not_found`; it
waits out a `rate_limited` answer once and retries, and returns every
other failure (SPECIFICATIONS.md §9.3). Each fails the product, except
`bad_request`: a malformed query is deterministic, so it fails the
product only if the card it was resolving gets no number some other way
(SPECIFICATIONS.md §5.7). A new `search` caller should return its error,
or keep a `bad_request` in `rejected`, rather than log and carry on.

Everything outside the Scryfall API — Wizards product pages and gallery
images, the Scalefast catalog, the scryfall.com set page — is fetched with
`httpGet`, on one shared client with a 60s per-attempt timeout, an
identifying User-Agent, and an error for any status outside 2xx. None of
those fetches is rate limited by this tool. A new fetch must go through
`httpGet` too. Its client comes from `newRetryClient`, which caps the
backoff at `RetryWaitMax`: Wizards answers some pages with a 503 and
`Retry-After: 3600`, and the library's default backoff waits that out in
full before each retry, turning one bad page into a four-hour stall.

### 4.5 CSS selectors are tied to the sites' current markup

Every selector the scraper uses is a named, commented constant: the
Wizards product page's in main.go (`productTitleSelector`,
`cardListSelector`, `galleryTitleSelector`, `galleryImageSelector`) and the scryfall.com set page's in scryfall.go
(`editionHeaderSelector`). A redesign of either site breaks these first.
It shows up as `FAILED ... no product title found` or `no cards found`
across many products in the daily run's PR body or job summary, or as the
run failing with `no Secret Lair editions found`. Suspect a markup change
before suspecting the parsing logic, and fix the constant.

---

## 5. Data flow and external dependencies (agent-relevant subset)

Full detail is in SPECIFICATIONS.md. The parts most relevant to *changing
code safely*:

- **Scalefast catalog API** (`getProducts` in main.go) — paginated JSON,
  50 items/page, used only by the `-page` crawl mode, not the explicit-URL
  mode. No auth, no documented rate limit; the code applies none beyond
  `httpGet`'s retries.
- **`secretlair.wizards.com` product pages** — scraped as HTML via `goquery`.
  No API, no stability guarantee, no rate limiting applied by this tool.
- **`scryfall.com/sets/sld`** — scraped as HTML (not the API) once per run,
  to get the list of `(edition title, search URI)` pairs used for the
  primary matching pass. This is a *second*, independent scrape target from
  the product pages, fetched through the same `httpGet`.
- **Scryfall REST API** (`api.scryfall.com`, via `go-scryfall`) — used for
  the actual card search/validation calls. Rate-limited as described in §4.4.
  Every `search()` call filters out `sldbonus` promo cards and `★`-suffixed
  duplicate foil-only collector numbers — these exclusions are deliberate,
  not incidental (bonus cards are tracked by a separate mechanism upstream).

There is no persistence or database, and the only cache is Scryfall search
results kept in memory for one run (`searchCache`). Every run is stateless except
for the `-page` counter, which in CI is round-tripped through a GitHub
Actions repo variable (`SLD_LAST_PAGE`) — see workflow file §3 above and
SPECIFICATIONS.md §4.2 for the exact contract of what the tool prints
(`FAILED` lines, `NEXT_PAGE=`) and its exit code. Changing that output
means changing the workflow's parsing in the same PR.

---

## 6. Verifying a fix live before opening a PR

Unit tests cover each step of the scraping pipeline offline (§3), but they
run on hand-written snippets, not the real pages, so the established
practice for validating a parsing fix is still:

1. Reproduce with a **live** run against the actual product URL that
   exposed the bug: `./sldownloader <url>` (see §2).
2. If the fix concerns a name/spelling match, cross-check the expected
   canonical name directly against the Scryfall API, e.g.:
   ```bash
   curl -s -A "sldownloader-dev/1.0" --get "https://api.scryfall.com/cards/search" \
     --data-urlencode "q=<card name> cn:<number>"
   ```
   (Scryfall requires a `User-Agent` header on direct API calls — a bare
   `curl` without one gets a 400.)
3. Run the same fixed binary against a couple of **neighboring** drops
   (same catalog page, similar naming) as a cheap regression check — this
   has repeatedly caught the fix being too narrow or too broad.
4. Add or update a test case in `main_test.go` that encodes the fixed
   behavior, so the next person doesn't need to repeat the live
   reproduction: a `TestCleanLine` row for a name, or a short inline HTML
   snippet (`TestParseCardList`) or canned search results
   (`TestMatchEdition`, `TestBackfillNumbers`) for the steps after it.
   Write the snippet by hand with only the markup involved; do not save
   whole product pages into the repo.

Before a `-page` crawl, check `pgrep -fl 'sld.* -page'` for one already
running from this machine, and never run two back to back: they share
Scryfall's per-IP search budget (§4.4), and the second one gets locked out
with `rate_limited` answers.

If the bug already shipped into an open PR against
`taw/magic-preconstructed-decks` (i.e. bad `.txt` files are already sitting
in an upstream PR), fixing the tool does **not** retroactively fix that PR —
the daily workflow only picks up *new* files. Fixing an already-open PR's
existing files is a separate, manual step (edit the file(s) in the PR
branch directly, in the `kodawah/magic-preconstructed-decks` fork).

---

## 7. The `todo/` folder

[todo/](todo/) contains one Markdown file per known improvement opportunity
that is not yet implemented — problem, impact, and a suggested approach for
each. It is not a promise of intent or a roadmap; it is a durable notepad so
findings from review don't get lost between sessions. See
[todo/README.md](todo/README.md) for the index and picking guidance.

When you pick up a `todo/NNN-*.md` item and implement it, delete the file in
the same PR that lands the fix (don't leave a stale entry pointing at
already-done work), and update `todo/README.md`'s index accordingly.

---

## 8. Workflow rules for this repo

- **Branch off `master`, always** — this repo does not stack PRs. If a piece
  of work depends on code in another still-open PR, either wait for it to
  merge first, or accept the dependency will show up in your diff until it
  does (GitHub collapses this automatically once the base PR merges — do
  not branch off another feature branch to avoid it).
- **One PR per concern.** Recent history in this repo consistently splits
  unrelated fixes (rate limiting, a workflow tweak, parser bugs, docs) into
  separate PRs rather than one large one, even when they were found in the
  same review pass. Follow that pattern.
- **Never push directly to `master`.** Always land work as a feature branch
  + PR (`gh pr create`), even for small fixes. Pushing straight to `master`
  needs an explicit, separately-given instruction — a repo's history of
  commits landing on `master` is not evidence that direct pushes are fine;
  it may just mean PRs get merged promptly.
- **Gate every change locally before opening a PR**: `go build ./...`,
  `go vet ./...`, `go test ./...` and golangci-lint (§2) all clean
  (remember the CGO flags from §2 on macOS), plus, for anything touching
  the scraping/matching logic, a live run per §6. The Test workflow
  re-runs the first four on the PR; it cannot do the live run for you.
- **Update [SPECIFICATIONS.md](SPECIFICATIONS.md) in the same PR** as
  any change to behavior it describes: grep it for every function you
  touched. People and agents read it instead of the code, so a stale
  section misleads them exactly where they look first.
- **Never widen the OCR digit-length filter** (§4.2) — this has been asked
  for before and explicitly rejected; treat it as settled unless the
  underlying domain assumption (SLD numbers are 3+ digits) is challenged
  directly by the person requesting the change.
- Commit messages and PR bodies in this repo explain **why**, briefly, not
  just what — most existing commits are one paragraph: what broke (ideally
  with the concrete input that broke it), why, what changed. Match that
  style rather than a bare summary of the diff.
