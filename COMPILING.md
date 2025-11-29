# Compiling PawScript

This document covers building PawScript from source.

## Prerequisites

- Go 1.21 or later
- For GUI builds: platform-specific dependencies (see below)

## Building the CLI (paw)

The command-line interpreter has no special dependencies:

```bash
make build
```

This creates the `paw` binary for your native platform.

## Building the GUI (pawgui)

The GUI version uses [Fyne](https://fyne.io/) and requires CGO with platform-specific graphics libraries.

### Linux

Install the required dependencies:

```bash
# Debian/Ubuntu
sudo apt-get install libgl1-mesa-dev xorg-dev

# Fedora
sudo dnf install mesa-libGL-devel libXcursor-devel libXrandr-devel libXinerama-devel libXi-devel libXxf86vm-devel
```

Then build:

```bash
make build-gui
```

### macOS

Xcode command line tools are required:

```bash
xcode-select --install
```

Then build:

```bash
make build-gui
```

### Windows

A C compiler is required. Install one of:
- [TDM-GCC](https://jmeubank.github.io/tdm-gcc/)
- [MSYS2](https://www.msys2.org/) with mingw-w64-gcc

Then build:

```bash
make build-gui
```

## Cross-Compilation

### CLI Cross-Compilation

The CLI can be cross-compiled for all platforms without special tools:

```bash
make build-macos-arm64
make build-macos-x64
make build-ms-arm64
make build-ms-x64
make build-linux-arm64
make build-linux-x64
```

### GUI Cross-Compilation

GUI cross-compilation requires [fyne-cross](https://github.com/fyne-io/fyne-cross), which uses Docker to provide the necessary toolchains.

1. Install Docker and ensure it's running

2. Install fyne-cross:
   ```bash
   go install github.com/fyne-io/fyne-cross@latest
   ```

3. Build for specific platforms:
   ```bash
   make build-gui-macos-arm64
   make build-gui-macos-x64
   make build-gui-ms-arm64
   make build-gui-ms-x64
   make build-gui-linux-arm64
   make build-gui-linux-x64
   ```

## Build All Releases

To build CLI and GUI for all platforms:

```bash
make build-all
```

This creates release archives in the `releases/` directory.

## Installation

To install locally:

```bash
make install              # installs to /usr/local/bin
make install PREFIX=/opt  # custom prefix
```

If `pawgui` has been built, it will also be installed.

## Other Targets

```bash
make test           # Run regression tests
make test-coverage  # Run tests with coverage report
make clean          # Remove build artifacts
make clean-releases # Remove release archives
make fmt            # Format Go code
make lint           # Run linter (requires golangci-lint)
make help           # Show all targets
```
