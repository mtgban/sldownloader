# No per-request timeout or status-code checks on the Wizards, Scalefast and scryfall.com fetches

**Category**: Operational hardening
**Files**: [main.go](../main.go) — `newRetryClient` and its callers (`scrapeProduct`, `getImageBytes`, `getProducts`); [scryfall.go](../scryfall.go) — `loadScryfallHeaders`

## Problem

Retries are bounded: `newRetryClient` caps each backoff at `RetryWaitMax`
(30s) even when a server sends a longer `Retry-After`, and the daily job
stops after 90 minutes (`timeout-minutes` in
[new-sld-pr.yml](../.github/workflows/new-sld-pr.yml)). Three gaps remain.

**No limit on waiting for a response.** Every client sits on go-cleanhttp's
transport, which limits dialing (30s) and the TLS handshake (10s) but sets
no `ResponseHeaderTimeout`, and no `http.Client.Timeout` is set anywhere. A
server that accepts the connection and never answers holds that attempt
open indefinitely; only the 90-minute job timeout ends it, and then the
whole run is lost instead of one product.

**Non-2xx responses are parsed as pages.** `retryablehttp`'s
`DefaultRetryPolicy` retries 429 and 5xx (except 501) and turns an
exhausted retry into an error, but any other status — 403, 404, 410 — comes
back as an ordinary response. `scrapeProduct` parses the error page, finds
no card list, and reports the generic `"no cards found"`, which is
indistinguishable from a markup change (see
[012](012-centralize-css-selectors.md)). `loadScryfallHeaders` has no retry
policy and no status check at all: an error page yields zero headers and a
nil error, and every product in the run then falls through to OCR.

**A fresh client per call.** `newRetryClient` builds a new client, and
with it a new connection pool, for every product page, gallery image and
catalog page, so no connection is reused within a run.

## Impact

- One unresponsive request can cost the whole daily run.
- A removed product page and a changed page layout produce the same log
  line, and a broken scryfall.com set page silently turns every product
  into an OCR job.

## Suggested approach

- Set `http.Client.Timeout` (e.g. 60s) on the client `newRetryClient`
  returns, and on the client `loadScryfallHeaders` uses, so each attempt is
  bounded and the retry policy gets to try again.
- Check `resp.StatusCode` before parsing, returning e.g.
  `fmt.Errorf("unexpected status %d fetching %s", resp.StatusCode, link)`.
  In `loadScryfallHeaders`, also return an error when zero headers are
  found.
- Share one client from `newRetryClient` (lazily built, like
  `getScryfallClient` in [scryfall.go](../scryfall.go)), keeping the
  per-call logger choice: the product-page fetch keeps the default logger,
  whose `[DEBUG]` lines are what made the Retry-After stalls diagnosable.
  Pairs with [010](010-user-agent-on-wizards-scrape.md), which wants the
  same shared client.
