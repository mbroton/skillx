BINARY := skillx
DIST := dist
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/mbroton/skillx/internal/cmd.version=$(VERSION)

.PHONY: all test build clean darwin darwin-arm64 darwin-amd64

all: test build

test:
	go test ./...

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

darwin: darwin-arm64 darwin-amd64

darwin-arm64:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build \
		-trimpath \
		-ldflags "-s -w $(LDFLAGS)" \
		-o $(DIST)/$(BINARY)-darwin-arm64 .

darwin-amd64:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build \
		-trimpath \
		-ldflags "-s -w $(LDFLAGS)" \
		-o $(DIST)/$(BINARY)-darwin-amd64 .

clean:
	rm -rf $(DIST) $(BINARY)
