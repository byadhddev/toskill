VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BINARY  := toskill
OUTDIR  := bin
GOFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

# Platforms for cross-compilation
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: build install clean test release help

## build: Build the unified binary
build:
	@echo "Building $(BINARY) $(VERSION)..."
	@mkdir -p $(OUTDIR)
	go build $(GOFLAGS) -o $(OUTDIR)/$(BINARY) ./cmd/toskill/

## install: Install to $GOPATH/bin
install:
	go install $(GOFLAGS) ./cmd/toskill/

## test: Run tests
test:
	go test ./...

## clean: Remove build artifacts
clean:
	rm -rf $(OUTDIR)

## release: Cross-compile for all platforms
release: clean
	@mkdir -p $(OUTDIR)
	@for platform in $(PLATFORMS); do \
		GOOS=$${platform%/*} GOARCH=$${platform#*/} \
		go build $(GOFLAGS) -o $(OUTDIR)/$(BINARY)-$${platform%/*}-$${platform#*/}$$([ "$${platform%/*}" = "windows" ] && echo ".exe") ./cmd/toskill/ && \
		echo "  ✅ $${platform}"; \
	done
	@cd $(OUTDIR) && sha256sum $(BINARY)-* > checksums.txt
	@echo "\nRelease binaries in $(OUTDIR)/"

## help: Show this help
help:
	@echo "toskill Makefile"
	@echo ""
	@sed -n 's/^## //p' $(MAKEFILE_LIST) | column -t -s ':' | sed 's/^/  /'
