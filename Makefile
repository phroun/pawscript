# Version
VERSION := $(shell git describe --tags --always --dirty)

# Directories
RELEASE_DIR := releases
SRC_DIR := src

# Detect native platform
NATIVE_OS := $(shell go env GOOS)
NATIVE_ARCH := $(shell go env GOARCH)

# Fyne CLI path (in GOPATH/bin)
GOBIN := $(shell go env GOPATH)/bin
FYNE := $(GOBIN)/fyne

# Set binary name based on OS
ifeq ($(NATIVE_OS),windows)
    BINARY_NAME := paw.exe
else
    BINARY_NAME := paw
endif

.PHONY: build-all build-all-gui clean-releases build build-gui build-gui-debug build-gui-software install test test-coverage run-example clean fmt lint help \
	build-gui-macos-arm64 build-gui-macos-x64 build-gui-ms-arm64 build-gui-ms-x64 build-gui-linux-arm64 build-gui-linux-x64 ensure-fyne

# Build native version for local use
build:
	@echo "Building paw for native platform ($(NATIVE_OS)/$(NATIVE_ARCH))..."
	cd $(SRC_DIR) && go build -ldflags "-X main.version=$(VERSION)" -o ../$(BINARY_NAME) ./cmd/paw
	@echo "Created: $(BINARY_NAME)"

build-token-example:
	@echo "Building token_example for native platform ($(NATIVE_OS)/$(NATIVE_ARCH))..."
	cd $(SRC_DIR) && go build -ldflags "-X main.version=$(VERSION)" -o ../token_example ./cmd/token_example
	@echo "Created: token_example"

# Ensure fyne CLI is installed (now at fyne.io/tools)
ensure-fyne:
	@test -f $(FYNE) || (echo "Installing fyne CLI..." && go install fyne.io/tools/cmd/fyne@latest)

# Build GUI version (auto-installs fyne CLI if needed)
build-gui: ensure-fyne
	@echo "Building pawgui for native platform ($(NATIVE_OS)/$(NATIVE_ARCH))..."
ifeq ($(NATIVE_OS),windows)
	cd $(SRC_DIR)/cmd/pawgui && $(FYNE) build --tags openglangle -o ../../../pawgui.exe
	@echo "Created: pawgui.exe (using ANGLE/DirectX backend)"
else ifeq ($(NATIVE_OS),darwin)
	cd $(SRC_DIR)/cmd/pawgui && $(FYNE) build -o ../../../pawgui
	cd $(SRC_DIR)/cmd/pawgui && $(FYNE) package -name pawgui && rm -rf ../../../pawgui.app && mv pawgui.app ../../../
	@echo "Created: pawgui (binary) and pawgui.app (bundle)"
else
	cd $(SRC_DIR)/cmd/pawgui && $(FYNE) build -o ../../../pawgui
	@echo "Created: pawgui"
endif

# Build GUI with debug symbols (for crash debugging)
# On Windows: use cv2pdb to convert DWARF to PDB, or use delve/gdb debugger
build-gui-debug:
	@echo "Building pawgui with debug symbols for $(NATIVE_OS)/$(NATIVE_ARCH)..."
ifeq ($(NATIVE_OS),windows)
	cd $(SRC_DIR) && go build -tags openglangle -gcflags "all=-N -l" -o ../pawgui-debug.exe ./cmd/pawgui
	@echo "Created: pawgui-debug.exe (with DWARF symbols, using ANGLE/DirectX)"
	@echo "To generate PDB: cv2pdb pawgui-debug.exe (requires cv2pdb tool)"
else
	cd $(SRC_DIR) && go build -gcflags "all=-N -l" -o ../pawgui-debug ./cmd/pawgui
	@echo "Created: pawgui-debug (with DWARF symbols)"
endif

# Build GUI with software rendering (for systems with graphics driver issues)
build-gui-software: ensure-fyne
	@echo "Building pawgui with software rendering for $(NATIVE_OS)/$(NATIVE_ARCH)..."
ifeq ($(NATIVE_OS),windows)
	cd $(SRC_DIR)/cmd/pawgui && $(FYNE) build --tags software -o ../../../pawgui-software.exe
	@echo "Created: pawgui-software.exe (software rendering - slower but compatible)"
else
	cd $(SRC_DIR)/cmd/pawgui && $(FYNE) build --tags software -o ../../../pawgui-software
	@echo "Created: pawgui-software (software rendering - slower but compatible)"
endif

# Default install prefix
PREFIX ?= /usr/local

# Install paw (and pawgui if built) to system
install: build
ifeq ($(NATIVE_OS),windows)
	@echo "Built: $(BINARY_NAME)"
	@if [ -f pawgui.exe ]; then echo "Built: pawgui.exe (from 'make build-gui')"; fi
	@echo "Note: On Windows, manually copy to a directory in your PATH."
else ifeq ($(NATIVE_OS),darwin)
	@mkdir -p $(PREFIX)/bin
	@install -m 755 $(BINARY_NAME) $(PREFIX)/bin/paw
	@echo "Installed: $(PREFIX)/bin/paw"
	@if [ -d pawgui.app ]; then \
		rm -rf /Applications/pawgui.app && \
		cp -R pawgui.app /Applications/ && \
		echo "Installed: /Applications/pawgui.app"; \
	fi
else
	@mkdir -p $(PREFIX)/bin
	@install -m 755 $(BINARY_NAME) $(PREFIX)/bin/paw
	@echo "Installed: $(PREFIX)/bin/paw"
	@if [ -f pawgui ]; then \
		install -m 755 pawgui $(PREFIX)/bin/pawgui && \
		echo "Installed: $(PREFIX)/bin/pawgui"; \
	fi
endif

# Build and package CLI for all platforms
build-all: build-wasm build-macos-arm64 build-macos-x64 build-ms-arm64 build-ms-x64 build-linux-arm64 build-linux-x64

# Build and package GUI for all platforms (requires fyne-cross and Docker)
build-all-gui: build-gui-macos-arm64 build-gui-macos-x64 build-gui-ms-arm64 build-gui-ms-x64 build-gui-linux-arm64 build-gui-linux-x64

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

# GUI cross-compilation using fyne-cross (requires Docker)
# Install: go install github.com/fyne-io/fyne-cross@latest
build-gui-macos-arm64:
	@echo "Building pawgui for macOS arm64 using fyne-cross..."
	cd $(SRC_DIR) && fyne-cross darwin -arch arm64 -app-id com.pawscript.pawgui -output pawgui ./cmd/pawgui
	@mkdir -p $(RELEASE_DIR)
	@mv $(SRC_DIR)/fyne-cross/dist/darwin-arm64/pawgui.app $(RELEASE_DIR)/pawgui-$(VERSION)-macos-arm64.app 2>/dev/null || true
	@cd $(RELEASE_DIR) && tar -czf pawgui-$(VERSION)-macos-arm64.tar.gz pawgui-$(VERSION)-macos-arm64.app 2>/dev/null || true
	@rm -rf $(SRC_DIR)/fyne-cross
	@echo "Created: $(RELEASE_DIR)/pawgui-$(VERSION)-macos-arm64.tar.gz"

build-gui-macos-x64:
	@echo "Building pawgui for macOS x64 using fyne-cross..."
	cd $(SRC_DIR) && fyne-cross darwin -arch amd64 -app-id com.pawscript.pawgui -output pawgui ./cmd/pawgui
	@mkdir -p $(RELEASE_DIR)
	@mv $(SRC_DIR)/fyne-cross/dist/darwin-amd64/pawgui.app $(RELEASE_DIR)/pawgui-$(VERSION)-macos-x64.app 2>/dev/null || true
	@cd $(RELEASE_DIR) && tar -czf pawgui-$(VERSION)-macos-x64.tar.gz pawgui-$(VERSION)-macos-x64.app 2>/dev/null || true
	@rm -rf $(SRC_DIR)/fyne-cross
	@echo "Created: $(RELEASE_DIR)/pawgui-$(VERSION)-macos-x64.tar.gz"

build-gui-ms-arm64:
	@echo "Building pawgui for Windows arm64 using fyne-cross..."
	cd $(SRC_DIR) && fyne-cross windows -arch arm64 -app-id com.pawscript.pawgui -output pawgui ./cmd/pawgui
	@mkdir -p $(RELEASE_DIR)/pawgui-$(VERSION)-windows-arm64
	@cp -r examples $(RELEASE_DIR)/pawgui-$(VERSION)-windows-arm64/examples
	@cp README.md LICENSE $(RELEASE_DIR)/pawgui-$(VERSION)-windows-arm64/
	@unzip -o $(SRC_DIR)/fyne-cross/dist/windows-arm64/pawgui.zip -d $(RELEASE_DIR)/pawgui-$(VERSION)-windows-arm64/
	@cd $(RELEASE_DIR) && zip -r pawgui-$(VERSION)-windows-arm64.zip pawgui-$(VERSION)-windows-arm64
	@rm -rf $(RELEASE_DIR)/pawgui-$(VERSION)-windows-arm64 $(SRC_DIR)/fyne-cross
	@echo "Created: $(RELEASE_DIR)/pawgui-$(VERSION)-windows-arm64.zip"

build-gui-ms-x64:
	@echo "Building pawgui for Windows x64 using fyne-cross..."
	cd $(SRC_DIR) && fyne-cross windows --console --tags "openglangle" -arch amd64 -app-id com.pawscript.pawgui -output pawgui ./cmd/pawgui
	@mkdir -p $(RELEASE_DIR)/pawgui-$(VERSION)-windows-x64
	@cp -r examples $(RELEASE_DIR)/pawgui-$(VERSION)-windows-x64/examples
	@cp README.md LICENSE $(RELEASE_DIR)/pawgui-$(VERSION)-windows-x64/
	@unzip -o $(SRC_DIR)/fyne-cross/dist/windows-amd64/pawgui.zip -d $(RELEASE_DIR)/pawgui-$(VERSION)-windows-x64/
	@cd $(RELEASE_DIR) && zip -r pawgui-$(VERSION)-windows-x64.zip pawgui-$(VERSION)-windows-x64
	@rm -rf $(RELEASE_DIR)/pawgui-$(VERSION)-windows-x64 $(SRC_DIR)/fyne-cross
	@echo "Created: $(RELEASE_DIR)/pawgui-$(VERSION)-windows-x64.zip"

build-gui-linux-arm64:
	@echo "Building pawgui for Linux arm64 using fyne-cross..."
	cd $(SRC_DIR) && fyne-cross linux -arch arm64 -app-id com.pawscript.pawgui -output pawgui ./cmd/pawgui
	@mkdir -p $(RELEASE_DIR)/pawgui-$(VERSION)-linux-arm64
	@cp -r examples $(RELEASE_DIR)/pawgui-$(VERSION)-linux-arm64/examples
	@cp README.md LICENSE $(RELEASE_DIR)/pawgui-$(VERSION)-linux-arm64/
	@tar -xf $(SRC_DIR)/fyne-cross/dist/linux-arm64/pawgui.tar.xz -C $(RELEASE_DIR)/pawgui-$(VERSION)-linux-arm64/
	@cd $(RELEASE_DIR) && tar -czf pawgui-$(VERSION)-linux-arm64.tar.gz pawgui-$(VERSION)-linux-arm64
	@rm -rf $(RELEASE_DIR)/pawgui-$(VERSION)-linux-arm64 $(SRC_DIR)/fyne-cross
	@echo "Created: $(RELEASE_DIR)/pawgui-$(VERSION)-linux-arm64.tar.gz"

build-gui-linux-x64:
	@echo "Building pawgui for Linux x64 using fyne-cross..."
	cd $(SRC_DIR) && fyne-cross linux -arch amd64 -app-id com.pawscript.pawgui -output pawgui ./cmd/pawgui
	@mkdir -p $(RELEASE_DIR)/pawgui-$(VERSION)-linux-x64
	@cp -r examples $(RELEASE_DIR)/pawgui-$(VERSION)-linux-x64/examples
	@cp README.md LICENSE $(RELEASE_DIR)/pawgui-$(VERSION)-linux-x64/
	@tar -xf $(SRC_DIR)/fyne-cross/dist/linux-amd64/pawgui.tar.xz -C $(RELEASE_DIR)/pawgui-$(VERSION)-linux-x64/
	@cd $(RELEASE_DIR) && tar -czf pawgui-$(VERSION)-linux-x64.tar.gz pawgui-$(VERSION)-linux-x64
	@rm -rf $(RELEASE_DIR)/pawgui-$(VERSION)-linux-x64 $(SRC_DIR)/fyne-cross
	@echo "Created: $(RELEASE_DIR)/pawgui-$(VERSION)-linux-x64.tar.gz"

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
	@rm -f paw pawgui pawgui.exe pawgui-debug pawgui-debug.exe pawgui-software pawgui-software.exe $(SRC_DIR)/coverage.out $(SRC_DIR)/coverage.html
	@rm -rf pawgui.app
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
	@echo "  build-gui      - Build pawgui (Fyne GUI) for native platform"
	@echo "  build-gui-debug - Build pawgui with debug symbols (for crash debugging)"
	@echo "  build-gui-software - Build pawgui with software rendering (for driver issues)"
	@echo "  build-all      - Build and package paw CLI for all platforms"
	@echo "  build-all-gui  - Build and package pawgui for all platforms (requires Docker)"
	@echo "  run-example    - Run hello.paw example"
	@echo "  test           - Run regression tests"
	@echo "  test-coverage  - Run tests with coverage report"
	@echo "  clean          - Remove build artifacts"
	@echo "  clean-releases - Clean release artifacts"
	@echo "  install        - Build and install paw (and pawgui if built) to PREFIX"
	@echo "  fmt            - Format code"
	@echo "  lint           - Run linter"
	@echo "  help           - Show this help"
	@echo ""
	@echo "GUI cross-compilation requires fyne-cross, fyne CLI, and Docker:"
	@echo "  go install github.com/fyne-io/fyne-cross@latest"
	@echo "  go install fyne.io/tools/cmd/fyne@latest"
	@echo "See BUILDING.md for troubleshooting version compatibility issues."
