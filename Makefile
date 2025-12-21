# Version
VERSION := $(shell git describe --tags --always --dirty)

# Directories
RELEASE_DIR := releases
SRC_DIR := src

# Detect native platform
NATIVE_OS := $(shell go env GOOS)
NATIVE_ARCH := $(shell go env GOARCH)

GOBIN := $(shell go env GOPATH)/bin

# Set binary name based on OS
ifeq ($(NATIVE_OS),windows)
    BINARY_NAME := paw.exe
else
    BINARY_NAME := paw
endif

.PHONY: build-all clean-releases build build-gui-gtk build-gui-qt install test test-coverage run-example clean fmt lint help \
	package-gtk package-qt package-gtk-macos package-qt-macos build-token-example

# Build native version for local use
build:
	@echo "Building paw for native platform ($(NATIVE_OS)/$(NATIVE_ARCH))..."
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY_NAME) ./src/cmd/paw
	@echo "Created: $(BINARY_NAME)"

build-token-example:
	@echo "Building token_example for native platform ($(NATIVE_OS)/$(NATIVE_ARCH))..."
	go build -ldflags "-X main.version=$(VERSION)" -o token_example ./src/cmd/token_example
	@echo "Created: token_example"

# Build GTK3-based GUI (requires GTK3 development libraries)
# Linux: apt install libgtk-3-dev
# macOS: brew install gtk+3
# Windows: Use MSYS2 with mingw-w64-x86_64-gtk3
build-gui-gtk:
	@echo "Building paw-gtk for native platform ($(NATIVE_OS)/$(NATIVE_ARCH))..."
	@go mod tidy
ifeq ($(NATIVE_OS),windows)
	go build -ldflags="-H windowsgui -s -w" -o paw-gtk.exe ./src/cmd/paw-gtk
	@echo "Created: paw-gtk.exe"
else
	go build -o paw-gtk ./src/cmd/paw-gtk
	@echo "Created: paw-gtk"
endif

# Build Qt-based GUI (requires Qt5/Qt6 development libraries)
# Uses miqt bindings: https://github.com/mappu/miqt
# Linux: apt install qtbase5-dev
# macOS: brew install qt@5
# Windows: Use MSYS2 with mingw-w64-x86_64-qt5-base
build-gui-qt:
	@echo "Building paw-qt for native platform ($(NATIVE_OS)/$(NATIVE_ARCH))..."
	@go mod tidy
ifeq ($(NATIVE_OS),windows)
	go build -ldflags="-H windowsgui -s -w" -o paw-qt.exe ./src/cmd/paw-qt
	@echo "Created: paw-qt.exe"
else
	go build -o paw-qt ./src/cmd/paw-qt
	@echo "Created: paw-qt"
endif

# Package GTK GUI as macOS .app bundle (macOS only)
package-gtk-macos:
ifeq ($(NATIVE_OS),darwin)
	@echo "Packaging paw-gtk as macOS app bundle..."
	@$(MAKE) build-gui-gtk
	@rm -rf paw-gtk.app
	@mkdir -p paw-gtk.app/Contents/MacOS
	@mkdir -p paw-gtk.app/Contents/Resources
	@cp paw-gtk paw-gtk.app/Contents/MacOS/
	@cp -r examples paw-gtk.app/Contents/Resources/examples
	@if [ -f assets/pawscript.icns ]; then \
		cp assets/pawscript.icns paw-gtk.app/Contents/Resources/pawscript.icns; \
	fi
	@echo '<?xml version="1.0" encoding="UTF-8"?>' > paw-gtk.app/Contents/Info.plist
	@echo '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' >> paw-gtk.app/Contents/Info.plist
	@echo '<plist version="1.0"><dict>' >> paw-gtk.app/Contents/Info.plist
	@echo '<key>CFBundleExecutable</key><string>paw-gtk</string>' >> paw-gtk.app/Contents/Info.plist
	@echo '<key>CFBundleIdentifier</key><string>com.pawscript.paw-gtk</string>' >> paw-gtk.app/Contents/Info.plist
	@echo '<key>CFBundleIconFile</key><string>pawscript</string>' >> paw-gtk.app/Contents/Info.plist
	@echo '<key>CFBundleName</key><string>PawScript GTK</string>' >> paw-gtk.app/Contents/Info.plist
	@echo '<key>CFBundlePackageType</key><string>APPL</string>' >> paw-gtk.app/Contents/Info.plist
	@echo '<key>CFBundleShortVersionString</key><string>$(VERSION)</string>' >> paw-gtk.app/Contents/Info.plist
	@echo '<key>CFBundleVersion</key><string>$(VERSION)</string>' >> paw-gtk.app/Contents/Info.plist
	@echo '<key>NSHighResolutionCapable</key><true/>' >> paw-gtk.app/Contents/Info.plist
	@echo '</dict></plist>' >> paw-gtk.app/Contents/Info.plist
	@echo "Created: paw-gtk.app"
else
	@echo "Error: package-gtk-macos is only available on macOS"
	@exit 1
endif

# Package Qt GUI as macOS .app bundle (macOS only)
package-qt-macos:
ifeq ($(NATIVE_OS),darwin)
	@echo "Packaging paw-qt as macOS app bundle..."
	@$(MAKE) build-gui-qt
	@rm -rf paw-qt.app
	@mkdir -p paw-qt.app/Contents/MacOS
	@mkdir -p paw-qt.app/Contents/Resources
	@cp paw-qt paw-qt.app/Contents/MacOS/
	@cp -r examples paw-qt.app/Contents/Resources/examples
	@if [ -f assets/pawscript.icns ]; then \
		cp assets/pawscript.icns paw-qt.app/Contents/Resources/pawscript.icns; \
	fi
	@echo '<?xml version="1.0" encoding="UTF-8"?>' > paw-qt.app/Contents/Info.plist
	@echo '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' >> paw-qt.app/Contents/Info.plist
	@echo '<plist version="1.0"><dict>' >> paw-qt.app/Contents/Info.plist
	@echo '<key>CFBundleExecutable</key><string>paw-qt</string>' >> paw-qt.app/Contents/Info.plist
	@echo '<key>CFBundleIdentifier</key><string>com.pawscript.paw-qt</string>' >> paw-qt.app/Contents/Info.plist
	@echo '<key>CFBundleIconFile</key><string>pawscript</string>' >> paw-qt.app/Contents/Info.plist
	@echo '<key>CFBundleName</key><string>PawScript Qt</string>' >> paw-qt.app/Contents/Info.plist
	@echo '<key>CFBundlePackageType</key><string>APPL</string>' >> paw-qt.app/Contents/Info.plist
	@echo '<key>CFBundleShortVersionString</key><string>$(VERSION)</string>' >> paw-qt.app/Contents/Info.plist
	@echo '<key>CFBundleVersion</key><string>$(VERSION)</string>' >> paw-qt.app/Contents/Info.plist
	@echo '<key>NSHighResolutionCapable</key><true/>' >> paw-qt.app/Contents/Info.plist
	@echo '</dict></plist>' >> paw-qt.app/Contents/Info.plist
	@echo "Created: paw-qt.app"
else
	@echo "Error: package-qt-macos is only available on macOS"
	@exit 1
endif

# Package native GUI (convenience target - detects platform and builds appropriate package)
package-gtk:
ifeq ($(NATIVE_OS),darwin)
	@$(MAKE) package-gtk-macos
else
	@$(MAKE) build-gui-gtk
	@echo "Note: On $(NATIVE_OS), no special packaging is needed. Binary is ready to use."
endif

package-qt:
ifeq ($(NATIVE_OS),darwin)
	@$(MAKE) package-qt-macos
else
	@$(MAKE) build-gui-qt
	@echo "Note: On $(NATIVE_OS), no special packaging is needed. Binary is ready to use."
endif

# Default install prefix
PREFIX ?= /usr/local

# Install paw to system
install: build
ifeq ($(NATIVE_OS),windows)
	@echo "Built: $(BINARY_NAME)"
	@echo "Note: On Windows, manually copy to a directory in your PATH."
else ifeq ($(NATIVE_OS),darwin)
	@mkdir -p $(PREFIX)/bin
	@install -m 755 $(BINARY_NAME) $(PREFIX)/bin/paw
	@echo "Installed: $(PREFIX)/bin/paw"
	@if [ -d paw-gtk.app ]; then \
		rm -rf /Applications/paw-gtk.app && \
		cp -R paw-gtk.app /Applications/ && \
		echo "Installed: /Applications/paw-gtk.app"; \
	fi
	@if [ -d paw-qt.app ]; then \
		rm -rf /Applications/paw-qt.app && \
		cp -R paw-qt.app /Applications/ && \
		echo "Installed: /Applications/paw-qt.app"; \
	fi
else
	@mkdir -p $(PREFIX)/bin
	@install -m 755 $(BINARY_NAME) $(PREFIX)/bin/paw
	@echo "Installed: $(PREFIX)/bin/paw"
	@if [ -f paw-gtk ]; then \
		install -m 755 paw-gtk $(PREFIX)/bin/paw-gtk && \
		echo "Installed: $(PREFIX)/bin/paw-gtk"; \
	fi
	@if [ -f paw-qt ]; then \
		install -m 755 paw-qt $(PREFIX)/bin/paw-qt && \
		echo "Installed: $(PREFIX)/bin/paw-qt"; \
	fi
endif

# Build and package CLI for all platforms
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
	GOOS=$(1) GOARCH=$(2) go build -ldflags "-X main.version=$(VERSION)" -o $(RELEASE_DIR)/paw-$(VERSION)-$(3)-$(4)/$(5) ./src/cmd/paw
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
	GOOS=js GOARCH=wasm go build -o js/pawscript.wasm ./src/wasm

test:
	@echo "Running tests..."
	@cd tests && ./test_regressions.sh

test-coverage:
	@echo "Running tests with coverage..."
	go test -v -coverprofile=coverage.out ./src
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

run-example:
	@echo "Running hello.paw example..."
	./paw examples/hello.paw -- arg1 arg2 arg3

clean:
	@echo "Cleaning..."
	@rm -f paw paw-gtk paw-gtk.exe paw-qt paw-qt.exe token_example coverage.out coverage.html
	@rm -rf paw-gtk.app paw-qt.app
	@echo "Clean complete"

fmt:
	@echo "Formatting code..."
	go fmt ./src/...
	@echo "Format complete"

lint:
	@echo "Running linter..."
	golangci-lint run ./src/...
	@echo "Lint complete"

help:
	@echo "PawScript Makefile"
	@echo ""
	@echo "Build Targets:"
	@echo "  build          - Build paw CLI for native platform"
	@echo "  build-gui-gtk  - Build paw-gtk (GTK3 GUI)"
	@echo "  build-gui-qt   - Build paw-qt (Qt5 GUI)"
	@echo "  build-all      - Build and package paw CLI for all platforms"
	@echo ""
	@echo "Package Targets (native packaging):"
	@echo "  package-gtk    - Build and package GTK GUI (macOS: .app bundle)"
	@echo "  package-qt     - Build and package Qt GUI (macOS: .app bundle)"
	@echo "  package-gtk-macos - Create paw-gtk.app bundle (macOS only)"
	@echo "  package-qt-macos  - Create paw-qt.app bundle (macOS only)"
	@echo ""
	@echo "Other Targets:"
	@echo "  install        - Build and install paw (and GUI if built) to PREFIX"
	@echo "  run-example    - Run hello.paw example"
	@echo "  test           - Run regression tests"
	@echo "  test-coverage  - Run tests with coverage report"
	@echo "  clean          - Remove build artifacts"
	@echo "  clean-releases - Clean release artifacts"
	@echo "  fmt            - Format code"
	@echo "  lint           - Run linter"
	@echo ""
	@echo "GUI Requirements:"
	@echo "  GTK: Linux: libgtk-3-dev | macOS: brew install gtk+3 | Windows: MSYS2 mingw-w64-x86_64-gtk3"
	@echo "  Qt:  Linux: qtbase5-dev  | macOS: brew install qt@5  | Windows: MSYS2 mingw-w64-x86_64-qt5-base"
	@echo ""
	@echo "Note: Windows GUI builds use GitHub Actions workflow (see .github/workflows/)"
