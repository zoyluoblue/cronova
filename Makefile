# cronova build targets. Requires Go 1.26+ (see go.mod).
# No CGO: the SQLite driver is pure Go, so binaries are fully static.

PKG        := ./cmd/cronova
EXEC_PKG   := ./cmd/cronova-executor
LDFLAGS    := -s -w

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

.PHONY: install
install: release ## build + install as a systemd service (needs root)
	sudo ./deploy/install.sh dist/cronova

.PHONY: clean
clean:
	rm -rf dist cronova cronova-executor

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
