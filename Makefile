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
	cd $(SRC_DIR) && go build -ldflags "-X main.version=$(VERSION)" -o ../$(BINARY_NAME) ./cmd/paw
	@echo "Created: $(BINARY_NAME)"

build-token-example:
	@echo "Building token_example for native platform ($(NATIVE_OS)/$(NATIVE_ARCH))..."
	cd $(SRC_DIR) && go build -ldflags "-X main.version=$(VERSION)" -o ../token_example ./cmd/token_example
	@echo "Created: token_example"

# Build GTK3-based GUI (requires GTK3 development libraries)
# Linux: apt install libgtk-3-dev
# macOS: brew install gtk+3
# Windows: Use MSYS2 with mingw-w64-x86_64-gtk3
build-gui-gtk:
	@echo "Building pawgui-gtk for native platform ($(NATIVE_OS)/$(NATIVE_ARCH))..."
	@cd $(SRC_DIR) && go mod tidy
ifeq ($(NATIVE_OS),windows)
	cd $(SRC_DIR) && go build -ldflags="-H windowsgui -s -w" -o ../pawgui-gtk.exe ./cmd/pawgui-gtk
	@echo "Created: pawgui-gtk.exe"
else
	cd $(SRC_DIR) && go build -o ../pawgui-gtk ./cmd/pawgui-gtk
	@echo "Created: pawgui-gtk"
endif

# Build Qt-based GUI (requires Qt5/Qt6 development libraries)
# Uses miqt bindings: https://github.com/mappu/miqt
# Linux: apt install qtbase5-dev
# macOS: brew install qt@5
# Windows: Use MSYS2 with mingw-w64-x86_64-qt5-base
build-gui-qt:
	@echo "Building pawgui-qt for native platform ($(NATIVE_OS)/$(NATIVE_ARCH))..."
	@cd $(SRC_DIR) && go mod tidy
ifeq ($(NATIVE_OS),windows)
	cd $(SRC_DIR) && go build -ldflags="-H windowsgui -s -w" -o ../pawgui-qt.exe ./cmd/pawgui-qt
	@echo "Created: pawgui-qt.exe"
else
	cd $(SRC_DIR) && go build -o ../pawgui-qt ./cmd/pawgui-qt
	@echo "Created: pawgui-qt"
endif

# Package GTK GUI as macOS .app bundle (macOS only)
package-gtk-macos:
ifeq ($(NATIVE_OS),darwin)
	@echo "Packaging pawgui-gtk as macOS app bundle..."
	@$(MAKE) build-gui-gtk
	@rm -rf pawgui-gtk.app
	@mkdir -p pawgui-gtk.app/Contents/MacOS
	@mkdir -p pawgui-gtk.app/Contents/Resources
	@cp pawgui-gtk pawgui-gtk.app/Contents/MacOS/
	@cp -r examples pawgui-gtk.app/Contents/Resources/examples
	@if [ -f assets/pawscript.icns ]; then \
		cp assets/pawscript.icns pawgui-gtk.app/Contents/Resources/pawscript.icns; \
	fi
	@echo '<?xml version="1.0" encoding="UTF-8"?>' > pawgui-gtk.app/Contents/Info.plist
	@echo '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' >> pawgui-gtk.app/Contents/Info.plist
	@echo '<plist version="1.0"><dict>' >> pawgui-gtk.app/Contents/Info.plist
	@echo '<key>CFBundleExecutable</key><string>pawgui-gtk</string>' >> pawgui-gtk.app/Contents/Info.plist
	@echo '<key>CFBundleIdentifier</key><string>com.pawscript.pawgui-gtk</string>' >> pawgui-gtk.app/Contents/Info.plist
	@echo '<key>CFBundleIconFile</key><string>pawscript</string>' >> pawgui-gtk.app/Contents/Info.plist
	@echo '<key>CFBundleName</key><string>PawScript GTK</string>' >> pawgui-gtk.app/Contents/Info.plist
	@echo '<key>CFBundlePackageType</key><string>APPL</string>' >> pawgui-gtk.app/Contents/Info.plist
	@echo '<key>CFBundleShortVersionString</key><string>$(VERSION)</string>' >> pawgui-gtk.app/Contents/Info.plist
	@echo '<key>CFBundleVersion</key><string>$(VERSION)</string>' >> pawgui-gtk.app/Contents/Info.plist
	@echo '<key>NSHighResolutionCapable</key><true/>' >> pawgui-gtk.app/Contents/Info.plist
	@echo '</dict></plist>' >> pawgui-gtk.app/Contents/Info.plist
	@echo "Created: pawgui-gtk.app"
else
	@echo "Error: package-gtk-macos is only available on macOS"
	@exit 1
endif

# Package Qt GUI as macOS .app bundle (macOS only)
package-qt-macos:
ifeq ($(NATIVE_OS),darwin)
	@echo "Packaging pawgui-qt as macOS app bundle..."
	@$(MAKE) build-gui-qt
	@rm -rf pawgui-qt.app
	@mkdir -p pawgui-qt.app/Contents/MacOS
	@mkdir -p pawgui-qt.app/Contents/Resources
	@cp pawgui-qt pawgui-qt.app/Contents/MacOS/
	@cp -r examples pawgui-qt.app/Contents/Resources/examples
	@if [ -f assets/pawscript.icns ]; then \
		cp assets/pawscript.icns pawgui-qt.app/Contents/Resources/pawscript.icns; \
	fi
	@echo '<?xml version="1.0" encoding="UTF-8"?>' > pawgui-qt.app/Contents/Info.plist
	@echo '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' >> pawgui-qt.app/Contents/Info.plist
	@echo '<plist version="1.0"><dict>' >> pawgui-qt.app/Contents/Info.plist
	@echo '<key>CFBundleExecutable</key><string>pawgui-qt</string>' >> pawgui-qt.app/Contents/Info.plist
	@echo '<key>CFBundleIdentifier</key><string>com.pawscript.pawgui-qt</string>' >> pawgui-qt.app/Contents/Info.plist
	@echo '<key>CFBundleIconFile</key><string>pawscript</string>' >> pawgui-qt.app/Contents/Info.plist
	@echo '<key>CFBundleName</key><string>PawScript Qt</string>' >> pawgui-qt.app/Contents/Info.plist
	@echo '<key>CFBundlePackageType</key><string>APPL</string>' >> pawgui-qt.app/Contents/Info.plist
	@echo '<key>CFBundleShortVersionString</key><string>$(VERSION)</string>' >> pawgui-qt.app/Contents/Info.plist
	@echo '<key>CFBundleVersion</key><string>$(VERSION)</string>' >> pawgui-qt.app/Contents/Info.plist
	@echo '<key>NSHighResolutionCapable</key><true/>' >> pawgui-qt.app/Contents/Info.plist
	@echo '</dict></plist>' >> pawgui-qt.app/Contents/Info.plist
	@echo "Created: pawgui-qt.app"
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
	@if [ -d pawgui-gtk.app ]; then \
		rm -rf /Applications/pawgui-gtk.app && \
		cp -R pawgui-gtk.app /Applications/ && \
		echo "Installed: /Applications/pawgui-gtk.app"; \
	fi
	@if [ -d pawgui-qt.app ]; then \
		rm -rf /Applications/pawgui-qt.app && \
		cp -R pawgui-qt.app /Applications/ && \
		echo "Installed: /Applications/pawgui-qt.app"; \
	fi
else
	@mkdir -p $(PREFIX)/bin
	@install -m 755 $(BINARY_NAME) $(PREFIX)/bin/paw
	@echo "Installed: $(PREFIX)/bin/paw"
	@if [ -f pawgui-gtk ]; then \
		install -m 755 pawgui-gtk $(PREFIX)/bin/pawgui-gtk && \
		echo "Installed: $(PREFIX)/bin/pawgui-gtk"; \
	fi
	@if [ -f pawgui-qt ]; then \
		install -m 755 pawgui-qt $(PREFIX)/bin/pawgui-qt && \
		echo "Installed: $(PREFIX)/bin/pawgui-qt"; \
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
	@rm -f paw pawgui-gtk pawgui-gtk.exe pawgui-qt pawgui-qt.exe token_example $(SRC_DIR)/coverage.out $(SRC_DIR)/coverage.html
	@rm -rf pawgui-gtk.app pawgui-qt.app
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
	@echo "Build Targets:"
	@echo "  build          - Build paw CLI for native platform"
	@echo "  build-gui-gtk  - Build pawgui-gtk (GTK3 GUI)"
	@echo "  build-gui-qt   - Build pawgui-qt (Qt5 GUI)"
	@echo "  build-all      - Build and package paw CLI for all platforms"
	@echo ""
	@echo "Package Targets (native packaging):"
	@echo "  package-gtk    - Build and package GTK GUI (macOS: .app bundle)"
	@echo "  package-qt     - Build and package Qt GUI (macOS: .app bundle)"
	@echo "  package-gtk-macos - Create pawgui-gtk.app bundle (macOS only)"
	@echo "  package-qt-macos  - Create pawgui-qt.app bundle (macOS only)"
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
