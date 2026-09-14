# CI never runs `go test`

**Category**: Testing & CI
**Files**: [.github/workflows/new-sld-pr.yml](../.github/workflows/new-sld-pr.yml)

## Problem

The one GitHub Actions workflow in this repo (`new-sld-pr.yml`) runs
`go install ./...` to build the tool, and then runs it — there is no step
that runs `go vet ./...` or `go test ./...`. Both are relied on today as a
purely local, manual gate (see [AGENTS.md](../AGENTS.md) §2 and §8), which
means it is entirely possible for a PR to merge with a failing test or a
`go vet` warning if the author simply forgot to run them (or ran them
without the required CGO flags on macOS and saw a build error unrelated to
the actual check — see [014](014-makefile-for-cgo-flags.md)).

There is also no separate CI trigger for pull requests at all — the
existing workflow only runs on a schedule and `workflow_dispatch`, both of
which run against `master`/the default branch, not a PR's branch.

## Impact

The `main_test.go` suite (and the `go vet` check that already caught at
least one real bug in this repo's history — a `%+q` format-verb mismatch)
provides no protection unless every contributor remembers to run it
locally before every PR.

## Suggested approach

- Add a `pull_request` trigger and a dedicated `test` job to this repo's
  Actions config: check out, set up Go (matching the existing
  `go-version-file: 'go.mod'` approach), install the Tesseract/Leptonica
  packages (mirroring the existing "Install Tesseract OCR" step), then run
  `go build ./...`, `go vet ./...`, and `go test ./...` in sequence,
  failing the job on any non-zero exit.
- This can live in the existing workflow file as a separate job, or in a
  new `.github/workflows/test.yml` — either is fine as long as it runs on
  every PR, not just on the scheduled sync.
