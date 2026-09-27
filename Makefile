BIN     := bin/thefactory
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/raulsh/thefactory/internal/cli.version=$(VERSION)
TFHOME  := $(or $(THEFACTORY_HOME),$(HOME)/.thefactory)

.PHONY: build ui go test lint generate dev demo install clean

## build: the UI, then the binary with the UI embedded
build: ui go

ui:
	pnpm -C ui install --frozen-lockfile
	pnpm -C ui build
	@touch internal/web/dist/.gitkeep

go:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/thefactory

## test: Go tests (race detector), UI type check and lint
test:
	go test -race ./...
	pnpm -C ui exec tsc --noEmit
	pnpm -C ui exec biome check .

lint:
	gofmt -l . | (! grep .)
	go vet ./...
	pnpm -C ui exec biome check .

## generate: regenerate sqlc query code after editing internal/store/queries
generate:
	sqlc generate

## dev: the Go server (:7420, --dev) and the Vite dev server (:5174) together
dev:
	@test -f $(TFHOME)/token || go run ./cmd/thefactory init
	@echo "open http://localhost:5174/?token=$$(cat $(TFHOME)/token)"
	@trap 'kill 0' INT TERM; go run ./cmd/thefactory serve --dev & pnpm -C ui dev; wait

## demo: the whole pipeline against fake claude and gh, for free (port 7430)
demo:
	./scripts/demo.sh

install: build
	install -d $(HOME)/.local/bin
	install -m 0755 $(BIN) $(HOME)/.local/bin/thefactory

clean:
	rm -rf bin tmp
	find internal/web/dist -mindepth 1 ! -name .gitkeep -delete
