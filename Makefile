GO ?= go
VERSION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)

.PHONY: build server client client-linux ui fmt vet test test-p2p clean

ui:
	cd frontend && npm install && npm run build

build: server client

server:
	$(GO) build -o server ./cmd/server

# The client runs on Linux contestant machines; stamp the version for the
# admin UI's "client version" column and self-update checks.
client:
	$(GO) build -ldflags "-X main.clientVersion=$(VERSION)" -o client ./cmd/client

client-linux:
	GOOS=linux GOARCH=amd64 $(GO) build -ldflags "-X main.clientVersion=$(VERSION)" -o client-linux ./cmd/client

fmt:
	gofmt -w .

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

test-p2p:
	cd pkg/go-silver-core && $(GO) test -race ./pkg/... ./internal/...

clean:
	rm -f server client client-linux
