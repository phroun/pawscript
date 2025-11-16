.PHONY: all build test clean run-example

all: build-wasm build-macos-arm64 build-macos-x64 build-ms-arm64 build-ms-x64 build-linux-arm64 build-linux-x64

build:
	@echo "Building paw CLI..."
	@go build -o ../paw ./cmd/paw
	@echo "Build complete: ./paw"

build-macos-arm64:
	@echo "Building paw macos arm64..."
	GOOS=darwin GOARCH=arm64 go build -o ../paw-macos-arm64 ./cmd/paw

build-macos-x64:
	@echo "Building paw macos x86_64..."
	GOOS=darwin GOARCH=amd64 go build -o ../paw-macos-x64 ./cmd/paw

build-ms-arm64:
	@echo "Building paw windows arm64..."
	GOOS=windows GOARCH=arm64 go build -o ../paw-arm64.exe ./cmd/paw

build-ms-x64:
	@echo "Building paw windows x86_64..."
	GOOS=windows GOARCH=amd64 go build -o ../paw-x64.exe ./cmd/paw

build-linux-arm64:
	@echo "Building paw linux arm64..."
	GOOS=linux GOARCH=arm64 go build -o ../paw-linux-arm64 ./cmd/paw

build-linux-x64:
	@echo "Building paw linux arm64..."
	GOOS=linux GOARCH=amd64 go build -o ../paw-linux-x64 ./cmd/paw

build-wasm:
	@echo "Building paw WASM..."
	GOOS=js GOARCH=wasm go build -o ../js/pawscript.wasm ./wasm

test:
	@echo "Running tests..."
	@go test -v .

test-coverage:
	@echo "Running tests with coverage..."
	@go test -v -coverprofile=coverage.out .
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

run-example:
	@echo "Running hello.paw example..."
	@../paw hello.paw -- arg1 arg2 arg3

clean:
	@echo "Cleaning..."
	@rm -f ../paw coverage.out coverage.html
	@echo "Clean complete"

install:
	@echo "Installing paw..."
	@go install ./cmd/paw
	@echo "Install complete"

fmt:
	@echo "Formatting code..."
	@go fmt ./...
	@echo "Format complete"

lint:
	@echo "Running linter..."
	@golangci-lint run
	@echo "Lint complete"

help:
	@echo "PawScript Makefile"
	@echo ""
	@echo "Targets:"
	@echo "  all          - Build and test (default)"
	@echo "  build        - Build the paw CLI"
	@echo "  test         - Run tests"
	@echo "  test-coverage - Run tests with coverage report"
	@echo "  run-example  - Run hello.paw example"
	@echo "  clean        - Remove build artifacts"
	@echo "  install      - Install paw to GOPATH"
	@echo "  fmt          - Format code"
	@echo "  lint         - Run linter"
	@echo "  help         - Show this help"
