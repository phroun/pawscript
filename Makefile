.PHONY: all build test clean run-example

all: build test

build:
	@echo "Building paw CLI..."
	@go build -o paw ./cmd/paw
	@echo "Build complete: ./paw"

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
	@./paw hello.paw -- arg1 arg2 arg3

clean:
	@echo "Cleaning..."
	@rm -f paw coverage.out coverage.html
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
