# No `-version` flag or build-time version stamping

**Category**: Maintainability
**Files**: [main.go](../main.go) — `run()`, flag definitions

## Problem

The binary has no way to report its own version or build provenance. Given
`git rev-parse HEAD` at build time, or a Go module version when installed
via `go install .../sldownloader@<tag>`, none of that is embedded in or
retrievable from the resulting binary.

## Impact

Minor, but real when debugging: given a decklist `.txt` file (which records
its `SOURCE` URL and `DATE` but nothing about which version of the tool
produced it) or a daily workflow run that behaved unexpectedly, there's no
quick way to confirm which commit's logic actually ran without cross
referencing the workflow's own checkout SHA in its job log.

## Suggested approach

- Add a `-version` flag that prints a version string and exits.
- Populate that string via `-ldflags "-X main.version=..."` at build time
  (the workflow's "Install sldownloader" step would need this flag added
  to its `go install` invocation), falling back to `debug.ReadBuildInfo()`
  (`runtime/debug`, standard library) for the module version/VCS revision
  when built via plain `go install .../sldownloader@<version>` without
  custom ldflags — that path already carries VCS metadata automatically
  since Go 1.18 for module-aware builds.
