# cronova build targets. Requires Go 1.26+ (see go.mod).
# No CGO: the SQLite driver is pure Go, so binaries are fully static.

PKG        := ./cmd/cronova
EXEC_PKG   := ./cmd/cronova-executor
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS    := -s -w -X main.version=$(VERSION)

.PHONY: build
build: ## build ./cronova for the host OS/arch
	go build -trimpath -ldflags "$(LDFLAGS)" -o cronova $(PKG)

.PHONY: release
release: ## static linux/amd64 binary for server deploy -> dist/cronova
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		go build -trimpath -ldflags "$(LDFLAGS)" -o dist/cronova $(PKG)

.PHONY: release-executor
release-executor: ## static linux/amd64 standalone executor -> dist/cronova-executor
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		go build -trimpath -ldflags "$(LDFLAGS)" -o dist/cronova-executor $(EXEC_PKG)

.PHONY: test
test: ## run the full test suite with the race detector
	go test -race ./...

.PHONY: package
package: ## build release tarballs (linux+darwin, amd64+arm64) + checksums -> dist/
	./scripts/package.sh linux  amd64
	./scripts/package.sh linux  arm64
	./scripts/package.sh darwin amd64
	./scripts/package.sh darwin arm64
	cd dist && { command -v sha256sum >/dev/null 2>&1 && sha256sum cronova_*.tar.gz || shasum -a 256 cronova_*.tar.gz; } > SHA256SUMS
	@echo "==> dist/SHA256SUMS"

.PHONY: install
install: ## build for host + install as a native service (systemd/launchd; needs root)
	$(MAKE) build
	@case "$$(uname -s)" in \
	  Darwin) sudo ./deploy/install-macos.sh ./cronova ;; \
	  Linux)  sudo ./deploy/install.sh ./cronova ;; \
	  *) echo "unsupported OS: $$(uname -s)" >&2; exit 1 ;; \
	esac

.PHONY: clean
clean:
	rm -rf dist cronova cronova-executor

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
