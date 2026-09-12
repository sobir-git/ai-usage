VERSION ?= 0.2.0
BINARY ?= bin/ai-usage
GO ?= go

.PHONY: build test install clean

build:
	@mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(BINARY) ./cmd/ai-usage

test:
	$(GO) test ./...

install: build
	install -Dm755 $(BINARY) $(HOME)/.local/bin/ai-usage

clean:
	rm -f $(BINARY)
