BINARY  := mcp-guardian
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GOFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"
PREFIX  ?= /usr/local
DESTDIR ?=

.PHONY: build install uninstall test lint check clean help

## build: Build the binary to dist/
build:
	@mkdir -p dist
	go build $(GOFLAGS) -o dist/$(BINARY) .

## install: Install to $(DESTDIR)$(PREFIX)/bin
install: build
	install -d $(DESTDIR)$(PREFIX)/bin
	install -m 755 dist/$(BINARY) $(DESTDIR)$(PREFIX)/bin/$(BINARY)
	@printf 'Installed %s to %s%s/bin/%s\n' "$(BINARY)" "$(DESTDIR)" "$(PREFIX)" "$(BINARY)"

## uninstall: Remove installed binary
uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/$(BINARY)
	@printf 'Removed %s%s/bin/%s\n' "$(DESTDIR)" "$(PREFIX)" "$(BINARY)"

## test: Run all tests
test:
	go test ./...

## lint: Run go vet
lint:
	go vet ./...

## check: lint + test
check: lint test

## clean: Remove build artifacts
clean:
	rm -rf dist/

## help: Show this help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'
