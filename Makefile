# Version
VERSION := $(shell git describe --tags --always --dirty)

# Directories
RELEASE_DIR := releases
SRC_DIR := src

# Detect native platform
NATIVE_OS := $(shell go env GOOS)
NATIVE_ARCH := $(shell go env GOARCH)

# Set binary name based on OS
ifeq ($(NATIVE_OS),windows)
    BINARY_NAME := paw.exe
else
    BINARY_NAME := paw
endif

.PHONY: build-all clean-releases build install test test-coverage run-example clean fmt lint help

# Build native version for local use
build:
	@echo "Building paw for native platform ($(NATIVE_OS)/$(NATIVE_ARCH))..."
	cd $(SRC_DIR) && go build -ldflags "-X main.version=$(VERSION)" -o ../$(BINARY_NAME) ./cmd/paw
	@echo "Created: $(BINARY_NAME)"

build-token-example:
	@echo "Building token_example for native platform ($(NATIVE_OS)/$(NATIVE_ARCH))..."
	cd $(SRC_DIR) && go build -ldflags "-X main.version=$(VERSION)" -o ../token_example ./cmd/token_example
	@echo "Created: token_example"

# Alias for build
install: build

# Build and package all platforms
build-all: build-wasm build-macos-arm64 build-macos-x64 build-ms-arm64 build-ms-x64 build-linux-arm64 build-linux-x64

# Clean release artifacts
clean-releases:
	rm -rf $(RELEASE_DIR)

define build-release
	@echo "Building and packaging paw $(3) $(4)..."
	@mkdir -p $(RELEASE_DIR)/paw-$(VERSION)-$(3)-$(4)
	@cp -r examples $(RELEASE_DIR)/paw-$(VERSION)-$(3)-$(4)/examples
	@cp README.md $(RELEASE_DIR)/paw-$(VERSION)-$(3)-$(4)/README.md
	@cp LICENSE $(RELEASE_DIR)/paw-$(VERSION)-$(3)-$(4)/LICENSE
	cd $(SRC_DIR) && GOOS=$(1) GOARCH=$(2) go build -ldflags "-X main.version=$(VERSION)" -o ../$(RELEASE_DIR)/paw-$(VERSION)-$(3)-$(4)/$(5) ./cmd/paw
	@cd $(RELEASE_DIR) && $(6) paw-$(VERSION)-$(3)-$(4)$(7) paw-$(VERSION)-$(3)-$(4)
	@rm -rf $(RELEASE_DIR)/paw-$(VERSION)-$(3)-$(4)
	@echo "Created: $(RELEASE_DIR)/paw-$(VERSION)-$(3)-$(4)$(7)"
endef

build-macos-arm64:
	$(call build-release,darwin,arm64,macos,arm64,paw,tar -czf,.tar.gz)

build-macos-x64:
	$(call build-release,darwin,amd64,macos,x64,paw,tar -czf,.tar.gz)

build-ms-arm64:
	$(call build-release,windows,arm64,windows,arm64,paw.exe,zip -r,.zip)

build-ms-x64:
	$(call build-release,windows,amd64,windows,x64,paw.exe,zip -r,.zip)

build-linux-arm64:
	$(call build-release,linux,arm64,linux,arm64,paw,tar -czf,.tar.gz)

build-linux-x64:
	$(call build-release,linux,amd64,linux,x64,paw,tar -czf,.tar.gz)

build-wasm:
	@echo "Building paw WASM..."
	cd $(SRC_DIR) && GOOS=js GOARCH=wasm go build -o ../js/pawscript.wasm ./wasm

test:
	@echo "Running tests..."
	@cd tests && ./test_regressions.sh

test-coverage:
	@echo "Running tests with coverage..."
	cd $(SRC_DIR) && go test -v -coverprofile=coverage.out .
	cd $(SRC_DIR) && go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: $(SRC_DIR)/coverage.html"

run-example:
	@echo "Running hello.paw example..."
	./paw examples/hello.paw -- arg1 arg2 arg3

clean:
	@echo "Cleaning..."
	@rm -f paw $(SRC_DIR)/coverage.out $(SRC_DIR)/coverage.html
	@echo "Clean complete"

fmt:
	@echo "Formatting code..."
	cd $(SRC_DIR) && go fmt ./...
	@echo "Format complete"

lint:
	@echo "Running linter..."
	cd $(SRC_DIR) && golangci-lint run
	@echo "Lint complete"

help:
	@echo "PawScript Makefile"
	@echo ""
	@echo "Targets:"
	@echo "  build          - Build paw for native platform"
	@echo "  build-all      - Build and package for all platforms"
	@echo "  run-example    - Run hello.paw example"
	@echo "  test           - Run regression tests"
	@echo "  test-coverage  - Run tests with coverage report"
	@echo "  clean          - Remove build artifacts"
	@echo "  clean-releases - Clean release artifacts"
	@echo "  install        - Alias for build"
	@echo "  fmt            - Format code"
	@echo "  lint           - Run linter"
	@echo "  help           - Show this help"
