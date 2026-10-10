# gochat — p2p terminal chat
#
# Common targets:
#   make build     build both binaries into ./bin
#   make test      run the full test suite
#   make check     gofmt check + go vet + tests
#   make smoke     build, then run the end-to-end smoke test
#   make run       run the rendezvous node locally (dev)

GO      ?= go
BINDIR  ?= bin
PORT    ?= 4001
PUBLIC  ?= $(shell hostname -I 2>/dev/null | awk '{print $$1}')

GOFLAGS  ?=
LDFLAGS  ?= -s -w

CLIENT   := $(BINDIR)/gochat
RENDEZVOUS := $(BINDIR)/gochat-rendezvous

.PHONY: all build client rendezvous test check fmt fmt-check vet clean smoke run-server help

all: build

build: client rendezvous

client:
	$(GO) build $(GOFLAGS) -trimpath -ldflags "$(LDFLAGS)" -o $(CLIENT) ./cmd/gochat

rendezvous:
	$(GO) build $(GOFLAGS) -trimpath -ldflags "$(LDFLAGS)" -o $(RENDEZVOUS) ./cmd/gochat-rendezvous

test:
	$(GO) test ./... -count=1

fmt:
	gofmt -w .

fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	$(GO) vet ./...

check: fmt-check vet test

clean:
	rm -rf $(BINDIR)

smoke: build
	./scripts/smoke-test.sh

# Run a local rendezvous/relay node (dev convenience).
run-server:
	$(GO) run ./cmd/gochat-rendezvous -port $(PORT) -data ./data-rendezvous $(if $(PUBLIC),-public $(PUBLIC),)

help:
	@sed -n '2,12p' Makefile
