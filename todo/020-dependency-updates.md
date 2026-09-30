# Direct dependencies are behind, and nothing proposes updates

**Category**: Dependencies
**Files**: [go.mod](../go.mod); `.github/` (no `dependabot.yml`)

## Problem

As of 2026-09-30, `go list -m -u` shows every updatable direct dependency
behind:

| Module | Pinned | Latest | Notes |
|---|---|---|---|
| `github.com/PuerkitoBio/goquery` | v1.9.2 | v1.13.0 | Four minor releases; the HTML parser every scrape depends on |
| `github.com/BlueMonday/go-scryfall` | v0.9.1 | v0.10.0 | Renames format legality fields and adds a Game Changer flag; this tool uses neither |
| `go.uber.org/ratelimit` | v0.2.0 | v0.3.1 | Replaces the 2016 pseudo-version `github.com/andres-erbsen/clock` with `github.com/benbjohnson/clock` v1.3.0 |
| `github.com/hashicorp/go-retryablehttp` | v0.7.7 | v0.7.8 | |
| `golang.org/x/net` (indirect) | v0.55.0 | v0.59.0 | GO-2026-5942 (panic parsing SVCB/HTTPS records), fixed in v0.56.0; `govulncheck` finds it unreachable from this code |

There is no `.github/dependabot.yml`, so only Dependabot security updates
arrive (e.g. the earlier `golang.org/x/net` bump); version updates for Go
modules and for the pinned GitHub Actions never do.

## Impact

Low today, but it compounds: the longer a dependency sits, the larger the
eventual jump, and a real vulnerability fix in goquery or retryablehttp
would arrive on top of several releases of unrelated changes.

## Suggested approach

- Update the direct dependencies in one PR, running the full gate plus a
  byte-for-byte comparison of a live `-page` crawl against master (these
  libraries sit directly under the scraping and matching output).
- Add `.github/dependabot.yml` with monthly, grouped version updates for
  `gomod` and `github-actions`, so future bumps arrive small and are
  tested by the Test workflow.
