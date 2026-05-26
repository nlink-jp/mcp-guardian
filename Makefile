BINARY  := mcp-guardian
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GOFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"
PREFIX  ?= /usr/local
DESTDIR ?=

# Developer ID Application identity matched against the keychain. Defaults
# to the generic prefix so any single Developer ID Application cert in the
# user's keychain is picked up automatically. Override when more than one
# is present:
#   make build CODESIGN_IDENTITY="Developer ID Application: ... (TEAMID)"
# Builds without a matching identity (CI, other contributors) fall back to
# the Go-linker ad-hoc signature with a warning — see scripts/codesign-darwin.sh.
CODESIGN_IDENTITY ?= Developer ID Application

# Notarization keychain profile name. Store credentials once per machine
# via `xcrun notarytool store-credentials nlink-jp-notary --key <p8>
# --key-id <id> --issuer <uuid>`. Builds without the profile skip
# notarization with a warning — see scripts/notarize-darwin.sh.
NOTARY_PROFILE ?= nlink-jp-notary

.PHONY: build build-all package install uninstall test lint check clean help \
       docs-mirror-check otel-up otel-down integration-test

## build: Build the binary to dist/
build:
	@mkdir -p dist
	go build $(GOFLAGS) -o dist/$(BINARY) .
	@scripts/codesign-darwin.sh dist/$(BINARY) "$(CODESIGN_IDENTITY)"

## build-all: Cross-compile for all platforms
build-all:
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build $(GOFLAGS) -o dist/$(BINARY)-linux-amd64   .
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build $(GOFLAGS) -o dist/$(BINARY)-linux-arm64   .
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build $(GOFLAGS) -o dist/$(BINARY)-darwin-amd64  .
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build $(GOFLAGS) -o dist/$(BINARY)-darwin-arm64  .
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -o dist/$(BINARY)-windows-amd64.exe .
	@scripts/codesign-darwin.sh dist/$(BINARY)-darwin-amd64 "$(CODESIGN_IDENTITY)"
	@scripts/codesign-darwin.sh dist/$(BINARY)-darwin-arm64 "$(CODESIGN_IDENTITY)"

## package: Build, create zip archives, and notarize darwin builds
package: build-all
	@cd dist && for f in $(BINARY)-*; do \
		case "$$f" in *.zip) continue ;; esac; \
		name=$${f%%.exe}; \
		ext=""; case "$$f" in *.exe) ext=".exe" ;; esac; \
		cp ../README.md .; \
		stage="$$(dirname "$$f")/_pkg"; rm -rf "$$stage"; mkdir -p "$$stage"; \
		cp "$$f" "$$stage/$(BINARY)$$ext"; \
		zip -j "$${name}-$(VERSION).zip" "$$stage/$(BINARY)$$ext" README.md; \
		rm -rf "$$stage"; \
		rm -f README.md; \
	done
	@scripts/notarize-darwin.sh dist/$(BINARY)-darwin-amd64-$(VERSION).zip "$(NOTARY_PROFILE)"
	@scripts/notarize-darwin.sh dist/$(BINARY)-darwin-arm64-$(VERSION).zip "$(NOTARY_PROFILE)"

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

## docs-mirror-check: Verify docs/en and docs/ja are full structural mirrors
docs-mirror-check:
	@bash scripts/docs-mirror-check.sh

## check: lint + test + docs-mirror-check
check: lint test docs-mirror-check

## otel-up: Start OTel Collector container for integration testing
otel-up:
	@scripts/otel-up.sh

## otel-down: Stop and remove OTel Collector container
otel-down:
	@scripts/otel-down.sh

## integration-test: Run integration tests (starts OTel Collector if needed)
integration-test:
	@if [ -z "$$OTEL_ENDPOINT" ]; then \
		echo "[make] Starting OTel Collector..."; \
		eval "$$(scripts/otel-up.sh)" && \
		go test -tags integration -v -count=1 ./internal/otlp/...; \
	else \
		go test -tags integration -v -count=1 ./internal/otlp/...; \
	fi

## clean: Remove build artifacts
clean:
	rm -rf dist/

## help: Show this help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'
