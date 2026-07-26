# OpenBackup build. Everything a contributor needs is one `make` away, and the
# same targets run in CI so a green pipeline means a working build locally too.

SHELL := /bin/sh

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# Version metadata is injected at link time, so the binary can report exactly
# what it is without a generated source file in the tree.
LDFLAGS := -s -w \
	-X github.com/foisalislambd/openbackup/internal/version.Version=$(VERSION) \
	-X github.com/foisalislambd/openbackup/internal/version.Commit=$(COMMIT) \
	-X github.com/foisalislambd/openbackup/internal/version.Date=$(DATE)

BIN     := bin
WEB_OUT := web/out
WEB_DIST := internal/server/web/dist

# PKGS is spelled out rather than ./... because the dashboard's node_modules can
# contain Go files (some npm packages ship them), and those are not ours to
# build, vet or test.
PKGS := ./cmd/... ./internal/...

.PHONY: all
all: web build

.PHONY: help
help:
	@echo "make build        Build the server and agent for this machine"
	@echo "make web          Build the dashboard and embed it in the server"
	@echo "make desktop      Build the desktop app for this machine (needs wails)"
	@echo "make test         Run every test"
	@echo "make check        Format check, vet and test (what CI runs)"
	@echo "make run-server   Run the server against ./data"
	@echo "make dev          Run the server and the dashboard dev server together"
	@echo "make release      Cross-compile release binaries for all platforms"
	@echo "make docker       Build the container image"
	@echo "make clean        Remove build output"

# ---------------------------------------------------------------------------
# Go
# ---------------------------------------------------------------------------

.PHONY: build
build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/ ./cmd/...

.PHONY: test
test:
	go test $(PKGS)

.PHONY: test-race
test-race:
	go test -race $(PKGS)

.PHONY: vet
vet:
	go vet $(PKGS)

.PHONY: fmt
fmt:
	gofmt -w .

.PHONY: check
check:
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (echo "run 'make fmt'" && exit 1)
	go vet $(PKGS)
	go test $(PKGS)

.PHONY: tidy
tidy:
	go mod tidy

# Cross-compilation matters here: a user installs the agent on whatever they own,
# and every target must build from one machine with no cgo.
.PHONY: cross
cross:
	@for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do \
		echo "checking $$target"; \
		GOOS=$${target%/*} GOARCH=$${target#*/} CGO_ENABLED=0 go build $(PKGS) || exit 1; \
	done

# ---------------------------------------------------------------------------
# Dashboard
# ---------------------------------------------------------------------------

# The dashboard is a Next.js static export copied into the package that embeds
# it, so `go build` alone produces a server with a working UI.
.PHONY: web
web:
	cd web && npm ci --no-audit --no-fund
	cd web && npm run build
	rm -rf $(WEB_DIST)
	mkdir -p $(WEB_DIST)
	cp -r $(WEB_OUT)/. $(WEB_DIST)/
	@git checkout -- $(WEB_DIST)/.gitkeep 2>/dev/null || true
	@echo "dashboard embedded in $(WEB_DIST)"

.PHONY: web-check
web-check:
	cd web && npm run typecheck && npm run lint

# ---------------------------------------------------------------------------
# Desktop app
# ---------------------------------------------------------------------------

# The desktop app is a separate module because Wails needs native webview
# headers, and the server and agent must stay pure-Go cross-compiles. It also
# only builds for the machine it runs on: there is no cross-compiling a native
# webview. Windows installers are produced by the release workflow.
.PHONY: desktop
desktop:
	@command -v wails >/dev/null || \
		(echo "install the Wails CLI: go install github.com/wailsapp/wails/v2/cmd/wails@latest" && exit 1)
	cd desktop && wails build -trimpath -ldflags '$(LDFLAGS)'
	@echo "desktop app in desktop/build/bin"

.PHONY: desktop-dev
desktop-dev:
	cd desktop && wails dev

.PHONY: desktop-check
desktop-check:
	cd desktop && go vet ./...
	cd desktop/frontend && npm run typecheck

.PHONY: dev
dev:
	@echo "server on :8080, dashboard on :3000 (proxying /api to the server)"
	@( go run ./cmd/openbackup-server & cd web && npm run dev; kill %1 )

.PHONY: run-server
run-server:
	OPENBACKUP_DATA_DIR=./data go run ./cmd/openbackup-server

# ---------------------------------------------------------------------------
# Release
# ---------------------------------------------------------------------------

.PHONY: release
release: web
	@mkdir -p dist
	@for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do \
		os=$${target%/*}; arch=$${target#*/}; ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		echo "building openbackup-$$os-$$arch"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' \
			-o dist/openbackup-$$os-$$arch$$ext ./cmd/openbackup || exit 1; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' \
			-o dist/openbackup-server-$$os-$$arch$$ext ./cmd/openbackup-server || exit 1; \
	done
	@cd dist && sha256sum * > SHA256SUMS 2>/dev/null || shasum -a 256 * > SHA256SUMS
	@echo "release binaries in ./dist"

.PHONY: docker
docker:
	docker build -t openbackup/server:$(VERSION) -t openbackup/server:latest .

.PHONY: clean
clean:
	rm -rf $(BIN) dist web/out web/.next
	rm -rf desktop/build/bin desktop/frontend/dist
	rm -rf $(WEB_DIST)
	mkdir -p $(WEB_DIST)
	git checkout -- $(WEB_DIST)/.gitkeep 2>/dev/null || true
