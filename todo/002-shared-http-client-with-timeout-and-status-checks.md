# No shared HTTP client, timeout, or status-code checks for Wizards/Scalefast fetches

**Category**: Operational hardening
**Files**: [main.go](../main.go) — `scrapeProduct` (package-level `retryablehttp.Get`), `getImageBytes`, `getProducts`

## Problem

Three different code paths make HTTP requests with three different,
inconsistent client setups:

- `scrapeProduct` uses the package-level `retryablehttp.Get`, which builds
  a **new** client per call with `retryablehttp`'s **default logger left
  on** (debug-level request logging to stderr) — unlike the other two,
  which explicitly silence it (`retryClient.Logger = nil`).
- `getImageBytes` and `getProducts` each build their own fresh,
  silenced `retryablehttp.Client` per call.

None of the three sets an overall request timeout — `retryablehttp`'s
underlying `http.Client` has no `Timeout` configured anywhere in this
codebase, so a hung server can block a request indefinitely (the retry
logic only governs *retries*, not a ceiling on any single attempt or the
whole operation).

None of the three checks `resp.StatusCode`. A 404, 500, or other non-2xx
response is parsed as if it were a normal page: `goquery` will happily
parse an error page's HTML, `doc.Find(...)` will find nothing, and the
resulting error is the generic, misleading `"no cards found"` — with no
indication the actual product page never loaded.

## Impact

- A hung Wizards or Scalefast endpoint can stall the daily scheduled job
  until the GitHub Actions job-level timeout kicts in, burning runner
  minutes for no useful work.
- A genuinely broken/removed product link is misreported identically to a
  page whose markup this tool's selectors simply don't match, making the
  two failure modes indistinguishable from the logs.

## Suggested approach

- Introduce one shared, silenced `retryablehttp.Client` (module-level,
  lazily constructed, similar in spirit to `getScryfallClient` in
  [scryfall.go](../scryfall.go)) with an explicit `Timeout` on its
  underlying `http.Client`, reused by all three call sites.
- After each fetch, check `resp.StatusCode` and return a distinct,
  specific error (e.g. `fmt.Errorf("unexpected status %d fetching %s",
  resp.StatusCode, link)`) before attempting to parse the body.
