# Kicker – build entry points (spec 04).
#   make dev        Go server with virtual modules + Vite dev server (hot reload)
#   make build      web + server for this machine          → bin/kicker
#   make build-pi   web + server for the Raspberry Pi 4     → bin/kicker-linux-arm64
#   make generate   regenerate code (JSON Schemas in catalog/) – runs before dev, build and test
#   make test       Go tests + web type check + firmware library tests (if PlatformIO is installed)
#   make e2e        browser tests (Playwright) against `kicker serve --sim`
#   make loadtest   100 000 games through the real server, checked against the spec 04 §4 budgets
#   make firmware   build all module firmware (PlatformIO)
#   make deploy PI=pi@kicker.local   copy the Pi build and restart the service

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
PKG     := github.com/Humbarrt/kicker/server/internal/version
LDFLAGS := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT)
DIST    := server/internal/webui/dist
PI      ?= pi@kicker.local
MODULES := $(wildcard firmware/modules/*)

.PHONY: help generate dev web build build-pi test e2e loadtest firmware-test lint tidy firmware deploy clean

help:
	@sed -n '2,11p' Makefile

generate:
	cd server && go generate ./...

dev: generate web
	@trap 'kill 0' INT TERM EXIT; \
	(cd server && go run ./cmd/kicker serve --sim --dev --data ../data) & \
	(cd web && pnpm install --frozen-lockfile --silent && pnpm dev) & \
	wait

web:
	cd web && pnpm install --frozen-lockfile && pnpm build
	find $(DIST) -mindepth 1 ! -name .gitkeep -delete
	cp -R web/build/. $(DIST)/

build: generate web
	cd server && go build -ldflags "$(LDFLAGS)" -o ../bin/kicker ./cmd/kicker

build-pi: generate web
	cd server && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o ../bin/kicker-linux-arm64 ./cmd/kicker

test: generate
	cd server && go test ./...
	cd web && pnpm check
	@command -v pio >/dev/null && $(MAKE) firmware-test || echo "PlatformIO not installed, firmware tests skipped"

e2e: build
	cd web && KICKER_BIN=../bin/kicker pnpm e2e

loadtest: build
	bin/kicker loadtest

firmware-test:
	pio test -d firmware/lib/KickerModule -e native

lint:
	cd server && go vet ./...
	@command -v golangci-lint >/dev/null && (cd server && golangci-lint run) || echo "golangci-lint not installed, skipped"

tidy:
	cd server && go mod tidy

firmware:
	@for m in $(MODULES); do echo "== $$m"; pio run -d $$m || exit 1; done

deploy: build-pi
	scp bin/kicker-linux-arm64 $(PI):/tmp/kicker
	ssh $(PI) 'sudo install -m 0755 /tmp/kicker /opt/kicker/kicker.new && sudo mv /opt/kicker/kicker /opt/kicker/kicker.prev 2>/dev/null; sudo mv /opt/kicker/kicker.new /opt/kicker/kicker && sudo systemctl restart kicker'

clean:
	rm -rf bin web/build
	find $(DIST) -mindepth 1 ! -name .gitkeep -delete
