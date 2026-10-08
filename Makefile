VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test lint docker clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/lantern ./cmd/lantern

test:
	go test -race ./...

lint:
	golangci-lint run ./...

docker:
	docker build --build-arg VERSION=$(VERSION) -t lantern:$(VERSION) -t lantern:latest .

clean:
	rm -rf bin
