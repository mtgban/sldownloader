# No linter beyond `go vet`

**Category**: Testing & CI
**Files**: repo root (no `.golangci.yml` exists yet); [.github/workflows/new-sld-pr.yml](../.github/workflows/new-sld-pr.yml)

## Problem

The only static analysis run on this codebase, even locally, is
`go vet ./...`. `go vet` catches a narrow, deliberately conservative set of
issues (it already caught one real bug in this repo's history — a
`%+q` format-verb mismatch against a struct slice). It does not catch
broader classes of issues a linter suite would: unchecked error returns
(this codebase has had exactly this class of bug before — the OCR client's
`SetWhitelist`/`SetImageFromBytes` errors were silently ignored until a
prior fix added the checks), unused parameters, variable shadowing (the
local `cleanTitle` variable shadowing the package-level `cleanTitle`
function in `scrapeProduct` — see SPECIFICATIONS.md §5.3 — is exactly the
kind of thing `shadow`/`govet`'s shadow check or a linter's `predeclared`
check would flag), and simplification opportunities.

## Impact

A recurring class of bug (unchecked errors) has already shipped and been
fixed once in this repo's history; nothing currently prevents it from
recurring in new code.

## Suggested approach

- Add [golangci-lint](https://golangci-lint.run/) with a reasonably
  conservative default config (`errcheck`, `govet`, `staticcheck`,
  `unused`, `gosimple` are a sensible starting set for a project this
  size) as a `.golangci.yml` at the repo root.
- Add a step running it to the Test workflow
  ([test.yml](../.github/workflows/test.yml)). Pin a golangci-lint
  release built with Go 1.25 or later; an older build fails to type-check
  this module.
- Run it locally first and fix (or explicitly `//nolint` with a reason)
  whatever it flags in the existing code before turning it on as a
  CI gate, so the first PR that adds the linter doesn't also need to fix
  an unrelated backlog of findings.
