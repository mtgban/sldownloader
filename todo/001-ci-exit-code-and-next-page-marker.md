# Catalog-crawl exit code and resume-page signal are unreliable

**Category**: Operational hardening
**Files**: [main.go](../main.go) (`run()`, catalog-crawl branch), [.github/workflows/new-sld-pr.yml](../.github/workflows/new-sld-pr.yml) ("Run sldownloader" step)

## Problem

In catalog-crawl mode, `run()` always returns exit code `0`, regardless of:
- how many individual products failed to scrape (`scrapeProduct`/`dumpCards`
  errors are logged and skipped, never surfaced in the exit code),
- whether the crawl even got past its first page (a `getProducts` error
  just `break`s the loop and falls through to the same `return 0`).

The only signal the daily workflow reads is the last line printed to
stdout, `"In the future you can start from page" (i - 2)`, parsed with
`awk '{print $NF}'`. If the very first `getProducts` call in a run fails,
`i` is never incremented before the `break`, so this line prints
`pageOpt - 2` — two pages *before* where the run started — with no
indication anything went wrong. A crawl that legitimately completed and one
that failed on its first request are indistinguishable from the outside.

See [SPECIFICATIONS.md](../SPECIFICATIONS.md) §4.2 and §7 for the full
current contract.

## Impact

A transient network failure during the scheduled run can silently regress
`SLD_LAST_PAGE` backward (or leave it stuck), and nothing in the GitHub
Actions job status reflects it — the step "succeeds" either way. This would
only surface as a human noticing the daily PR stopped appearing or started
covering pages it already covered.

## Suggested approach

- Have the tool distinguish "crawl completed" from "crawl aborted early"
  in its exit code (e.g. non-zero on any `getProducts` failure), and
  consider a distinct exit code for "completed with per-product errors."
- Replace the free-text final line with an explicit, greppable marker on
  its own line, e.g. `NEXT_PAGE=<n>` — easier and less fragile to extract
  than "last word of the last line," and immune to a future change in the
  message's wording.
- In the workflow, check the tool's actual exit status after the
  `done < <(...)` read loop (today it's discarded entirely) before trusting
  `$suggested` for the next `SLD_LAST_PAGE` value.
