#!/bin/bash
# build.sh - Build PawScript WASM

set -e

echo "=== PawScript WASM Build ==="
echo ""

# Check prerequisites
echo "Checking prerequisites..."
if ! command -v go &> /dev/null; then
    echo "✗ Go not found. Please install Go first."
    exit 1
fi

if ! command -v npm &> /dev/null; then
    echo "✗ npm not found. Please install Node.js first."
    exit 1
fi
echo "✓ Prerequisites OK"
echo ""

# Ensure wasm_exec.js exists
if [ ! -f "wasm_exec.js" ]; then
    echo "Getting wasm_exec.js from Go installation..."
    cp "$(go env GOROOT)/misc/wasm/wasm_exec.js" . || {
        echo "✗ Could not copy wasm_exec.js"
        echo "Try: curl -o wasm_exec.js https://raw.githubusercontent.com/golang/go/master/misc/wasm/wasm_exec.js"
        exit 1
    }
    echo "✓ wasm_exec.js copied"
else
    echo "✓ wasm_exec.js exists"
fi
echo ""

# Find main.go
MAIN_GO=""
if [ -f "src/wasm/main.go" ]; then
    MAIN_GO="src/wasm/main.go"
elif [ -f "src/main.go" ]; then
    MAIN_GO="src/main.go"
elif [ -f "main.go" ]; then
    MAIN_GO="main.go"
else
    echo "✗ Could not find main.go"
    echo "Looked in: main.go, src/main.go, src/wasm/main.go"
    exit 1
fi
echo "✓ Found Go source: $MAIN_GO"

# Build WASM
echo ""
echo "Step 1: Building WASM..."
GOOS=js GOARCH=wasm go build -o pawscript.wasm "$MAIN_GO"
echo "✓ WASM built: pawscript.wasm ($(ls -lh pawscript.wasm | awk '{print $5}'))"
echo ""

# Install dependencies if needed
if [ ! -d "node_modules" ]; then
    echo "Installing npm dependencies..."
    npm install
    echo ""
fi

# Build TypeScript
echo "Step 2: Building TypeScript..."
npm run build:ts
echo "✓ TypeScript compiled"
echo ""

# Copy assets
echo "Step 3: Copying assets..."
npm run copy:assets
echo "✓ Assets copied to dist/"
echo ""

# Verify build
echo "=== Build Verification ==="
if [ -f "dist/index.js" ]; then
    echo "✓ dist/index.js"
else
    echo "✗ dist/index.js missing"
fi

if [ -f "dist/main.js" ]; then
    echo "✓ dist/main.js"
else
    echo "? dist/main.js missing (optional)"
fi

if [ -f "dist/pawscript.wasm" ]; then
    echo "✓ dist/pawscript.wasm"
else
    echo "✗ dist/pawscript.wasm missing"
fi

if [ -f "dist/wasm_exec.js" ]; then
    echo "✓ dist/wasm_exec.js"
else
    echo "✗ dist/wasm_exec.js missing"
fi
echo ""

echo "=== Build Complete ==="
echo ""
echo "Next steps:"
echo "  - For Node.js: npm run test:node"
echo "  - For browser: open dist/index.html (serve via http server)"
echo "  - Clean build: npm run clean && ./build.sh"
echo ""
