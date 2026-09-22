MODULE  := github.com/RidgetopAi/backstory
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BIN     := bin/backstory
GO      ?= go

.PHONY: build fmt-check vet lint test integration check clean

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X $(MODULE)/internal/version.Version=$(VERSION)" -o $(BIN) ./cmd/backstory

fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt: files need formatting:"; echo "$$out"; exit 1; fi

vet:
	$(GO) vet ./...

lint:
	golangci-lint run ./...

test:
	$(GO) test -race -count=1 ./...

integration:
	$(GO) test -race -count=1 -tags integration ./...

check: fmt-check vet lint test

clean:
	rm -rf bin
