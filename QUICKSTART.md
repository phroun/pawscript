# PawScript Go - Quick Start Guide

Get started with PawScript in Go in 5 minutes!

## Installation

```bash
# Clone the repository
git clone https://github.com/yourusername/pawscript
cd pawscript

# Build the CLI tool
make build

# Or install to GOPATH
make install
```

## Your First PawScript

Create a file `hello.paw`:

```pawscript
echo "Hello, PawScript!"
echo "This is my first script"
```

Run it:

```bash
./paw hello.paw
```

## Using PawScript as a Library

Create `main.go`:

```go
package main

import (
    "fmt"
    "github.com/yourusername/pawscript"
)

func main() {
    ps := pawscript.New(nil)
    
    ps.RegisterCommand("hello", func(ctx *pawscript.Context) pawscript.Result {
        fmt.Println("Hello from Go!")
        return pawscript.BoolResult(true)
    })
    
    ps.Execute("hello")
}
```

Run it:

```bash
go run main.go
```

## Basic Commands

### Command Sequences

```pawscript
# Execute in order (semicolon)
echo "First"; echo "Second"; echo "Third"
```

### Conditional Execution

```pawscript
# AND - second runs only if first succeeds (ampersand)
save_file & echo "Saved successfully"

# OR - second runs only if first fails (pipe)
save_file | echo "Save failed!"
```

### Macros

```pawscript
# Define a macro
macro greet(echo 'Hello $1!')

# Use it
greet 'World'
```

### Brace Expressions

```pawscript
# Execute command and substitute result
echo "The answer is: {calculate 6, 7}"
```

## Command Handler Pattern

```go
ps.RegisterCommand("mycommand", func(ctx *pawscript.Context) pawscript.Result {
    // Get arguments
    arg1 := ctx.Args[0]
    
    // Set result for next command
    ctx.SetResult("some value")
    
    // Return success
    return pawscript.BoolResult(true)
})
```

## Async Operations Pattern

```go
ps.RegisterCommand("async_save", func(ctx *pawscript.Context) pawscript.Result {
    // Request async token
    token := ctx.RequestToken(nil)
    
    // Start async work
    go func() {
        // Do work...
        time.Sleep(1 * time.Second)
        
        // Resume when done
        ctx.ResumeToken(token, true)
    }()
    
    return pawscript.TokenResult(token)
})
```

## Running Tests

```bash
# Run all tests
make test

# Run with coverage
make test-coverage
```

## Next Steps

1. **Read the full README**: See `README.md` for complete documentation
2. **Run the example**: `go run example.go` to see various features
3. **Explore test files**: `pawscript_test.go` shows usage patterns
4. **Create your commands**: Start building your application!

## Common Patterns

### Configuration

```go
ps := pawscript.New(&pawscript.Config{
    Debug:                true,
    AllowMacros:          true,
    EnableSyntacticSugar: true,
    DefaultTokenTimeout:  5 * time.Minute,
})
```

### Error Handling

```go
ps.RegisterCommand("safe_operation", func(ctx *pawscript.Context) pawscript.Result {
    if len(ctx.Args) == 0 {
        // Return error
        return pawscript.BoolResult(false)
    }
    
    // Do work...
    
    return pawscript.BoolResult(true)
})
```

### Working with Results

```go
// Set a result
ctx.SetResult(42)

// Get a result
if ctx.HasResult() {
    value := ctx.GetResult()
}

// Clear result
ctx.ClearResult()
```

## Help

- **GitHub Issues**: Report bugs or request features
- **Documentation**: See `README.md` for full API reference
- **Examples**: Check `example.go` for comprehensive examples

## License

MIT License - see LICENSE file for details
