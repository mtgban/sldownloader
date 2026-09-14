# No identifying User-Agent on the Wizards/Scalefast HTTP clients

**Category**: Operational hardening
**Files**: [main.go](../main.go) — `scrapeProduct`, `getImageBytes`, `getProducts`; [scryfall.go](../scryfall.go) — `loadScryfallHeaders`

## Problem

`getScryfallClient` (scryfall.go) explicitly sets `WithUserAgent("sldownloader/1.0")`
on the Scryfall REST API client — a deliberate practice, since Scryfall's
own API guidelines ask for identifiable clients. None of the other four
HTTP call sites in this codebase (the Wizards product-page fetch, the
image-gallery downloads, the Scalefast catalog API, and the
`scryfall.com/sets/sld` HTML scrape) set any custom User-Agent at all —
they all send whatever default their respective HTTP client library uses.

## Impact

Minor and mostly about consistency and debuggability rather than a known
problem today: if any of these four endpoints ever start rate-limiting or
blocking by User-Agent (a common anti-scraping measure), there is no way
to identify or adjust this tool's traffic without already knowing to look
for the underlying library's default string. It's also simply inconsistent
with the practice already established for the Scryfall client.

## Suggested approach

- Set a consistent, identifying User-Agent (e.g. `sldownloader/1.0
  (+https://github.com/mtgban/sldownloader)`) on all HTTP requests this
  tool makes, not just the Scryfall API ones. For `retryablehttp.Client`,
  this means setting a default header via the underlying `HTTPClient`'s
  transport or via a request-level header set before each `Do` call; for
  `go-cleanhttp`'s client (used in `loadScryfallHeaders`), via the request
  object directly (this already uses `http.NewRequestWithContext`, so
  adding `req.Header.Set("User-Agent", ...)` there is a one-line change).
- Natural to combine with [002](002-shared-http-client-with-timeout-and-status-checks.md)
  if that item is tackled first, since both want a shared, pre-configured
  client for the same call sites.
