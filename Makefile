# The go commands from AGENTS.md §2, with the cgo flags gosseract needs on
# macOS, where Homebrew installs the Tesseract and Leptonica headers outside
# the default search path. Homebrew tools are found by full path when
# /opt/homebrew/bin is not on PATH.

GO ?= $(shell command -v go 2>/dev/null || echo /opt/homebrew/bin/go)
GOFMT ?= $(dir $(GO))gofmt
REVIVE_VERSION := v1.13.0
STATICCHECK_VERSION := 2026.2.1

ifeq ($(shell uname -s),Darwin)
BREW ?= $(shell command -v brew 2>/dev/null || echo /opt/homebrew/bin/brew)
BREW_PREFIX := $(shell $(BREW) --prefix)
export CGO_CPPFLAGS := -I$(BREW_PREFIX)/include
export CGO_LDFLAGS := -L$(BREW_PREFIX)/lib
endif

.PHONY: build vet test lint check

build:
	$(GO) build -o sldownloader .

vet:
	$(GO) vet ./...

test:
	$(GO) test -race ./...

lint:
	test -z "$$($(GOFMT) -s -l .)" || { echo "files are not gofmt-ed"; exit 1; }
	$(GO) run github.com/mgechev/revive@$(REVIVE_VERSION) -set_exit_status -config .revive.toml ./...
	$(GO) run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...

# The gate every change passes before a PR (AGENTS.md §8)
check: build vet test lint
