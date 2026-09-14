# No wrapper for the macOS CGO flags

**Category**: Maintainability
**Files**: repo root (no `Makefile`/`justfile` exists yet); [README.md](../README.md), [AGENTS.md](../AGENTS.md) §2 document the flags as a manual step

## Problem

Building, vetting, or testing this repo on macOS requires exporting
`CGO_CPPFLAGS`/`CGO_LDFLAGS` pointing at the Homebrew Tesseract/Leptonica
prefix before every `go build`/`go vet`/`go test` invocation (documented in
[AGENTS.md](../AGENTS.md) §2 and [README.md](../README.md)'s install
section). There is nothing in the repo that wraps this — every contributor
either remembers the exact incantation, re-derives it from the docs each
time, or hits the confusing
`tessbridge.cpp:5:10: fatal error: 'leptonica/allheaders.h' file not found`
error and has to go figure out why.

## Impact

Pure friction, but recurring: this exact confusion has already come up
once in this repo's own history (it's why the CGO-flags documentation
exists at all in AGENTS.md/README today) and will keep happening for every
new contributor or fresh machine until something mechanical fixes it.

## Suggested approach

- Add a `Makefile` (or `justfile`, matching whatever the maintainer
  prefers) with `build`, `vet`, `test`, and maybe `run` targets that
  detect macOS (`uname`) and set the CGO flags via `brew --prefix`
  automatically, falling back to plain `go build`/etc. on Linux where the
  headers are already on the default path.
- Keep it simple — this doesn't need to become a full build system, just
  remove the "remember the CGO flags" step from the local dev loop.
