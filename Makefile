BIN     := bin/tfy
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/raulsh/tfy/internal/cli.version=$(VERSION)
TFHOME  := $(or $(TFY_HOME),$(HOME)/.tfy)

.PHONY: build ui go test lint generate dev demo install clean

## build: the UI, then the binary with the UI embedded
build: ui go

ui:
	pnpm -C ui install --frozen-lockfile
	pnpm -C ui build
	@touch internal/web/dist/.gitkeep

go:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/tfy

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
	@test -f $(TFHOME)/token || go run ./cmd/tfy init
	@echo "open http://localhost:5174/?token=$$(cat $(TFHOME)/token)"
	@trap 'kill 0' INT TERM; go run ./cmd/tfy serve --dev & pnpm -C ui dev; wait

## demo: the whole pipeline against fake claude and gh, for free (port 7430)
demo:
	./scripts/demo.sh

install: build
	install -d $(HOME)/.local/bin
	install -m 0755 $(BIN) $(HOME)/.local/bin/tfy

clean:
	rm -rf bin tmp dist
	find internal/web/dist -mindepth 1 ! -name .gitkeep -delete
