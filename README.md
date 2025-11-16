# PawScript - Go Implementation

A clean, idiomatic Go implementation of PawScript: a command language with
token-based suspension for text editors and command-driven applications.

## Features

- **Complex Command Syntax**: Support for sequences (`;`), conditionals (`&`), and alternatives (`|`)
- **Token-Based Suspension**: Pause and resume command execution for long-running operations
- **Macro System**: Define and execute reusable command sequences with argument substitution
- **Syntactic Sugar**: Automatic transformation of convenient syntax patterns
- **Brace Expressions**: Execute commands within `{...}` and substitute results
- **Thread-Safe**: All operations are safe for concurrent use
- **Host Agnostic**: Clean interface for integration with any application
- **Command Line Tool**: Execute PawScript files directly from the command line

## Installation

```bash
go get github.com/phroun/pawscript-go
```

## Building the CLI

```bash
go build -o paw ./cmd/paw
```

## Quick Start

```go
package main

import (
    "fmt"
    "github.com/yourusername/pawscript"
)

func main() {
    // Create PawScript interpreter
    ps := pawscript.New(nil) // Use default config
    
    // Register commands
    ps.RegisterCommand("hello", func(ctx *pawscript.Context) pawscript.Result {
        fmt.Println("Hello from PawScript!")
        return pawscript.BoolResult(true)
    })
    
    ps.RegisterCommand("echo", func(ctx *pawscript.Context) pawscript.Result {
        if len(ctx.Args) > 0 {
            fmt.Printf("Echo: %v\n", ctx.Args[0])
        }
        return pawscript.BoolResult(true)
    })
    
    // Execute commands
    ps.Execute("hello")                      // Simple command
    ps.Execute("echo 'Hello World'")         // Command with arguments
    ps.Execute("hello; echo 'chained'")      // Command sequence
}
```

## Command Line Usage

```bash
# Execute a script file
paw hello.paw

# Execute with arguments
paw script.paw -- arg1 arg2 arg3

# Execute from stdin
echo "echo 'Hello World'" | paw

# Auto-adds .paw extension
paw hello  # Executes hello.paw
```

## API Reference

### PawScript

#### Creating an Instance

```go
// With default config
ps := pawscript.New(nil)

// With custom config
ps := pawscript.New(&pawscript.Config{
    Debug:                true,
    DefaultTokenTimeout:  5 * time.Minute,
    EnableSyntacticSugar: true,
    AllowMacros:          true,
    ShowErrorContext:     true,
    ContextLines:         2,
})
```

#### Registering Commands

```go
// Single command
ps.RegisterCommand("save", func(ctx *pawscript.Context) pawscript.Result {
    // Command implementation
    return pawscript.BoolResult(true)
})

// Multiple commands
ps.RegisterCommands(map[string]pawscript.Handler{
    "open": func(ctx *pawscript.Context) pawscript.Result {
        // Implementation
        return pawscript.BoolResult(true)
    },
    "close": func(ctx *pawscript.Context) pawscript.Result {
        // Implementation
        return pawscript.BoolResult(true)
    },
})
```

#### Executing Commands

```go
// Execute command string
result := ps.Execute("save_file; close_buffer")

// Check result
if boolResult, ok := result.(pawscript.BoolResult); ok {
    if bool(boolResult) {
        fmt.Println("Success")
    }
}

// Handle async token
if tokenResult, ok := result.(pawscript.TokenResult); ok {
    tokenID := string(tokenResult)
    // Token will be resumed later
}
```

### Command Context

Command handlers receive a `*Context` with these methods:

```go
func myCommand(ctx *pawscript.Context) pawscript.Result {
    // Access arguments
    arg1 := ctx.Args[0]
    
    // Set result value
    ctx.SetResult("computed value")
    
    // Get current result
    value := ctx.GetResult()
    
    // Check if result exists
    if ctx.HasResult() {
        // ...
    }
    
    // Clear result
    ctx.ClearResult()
    
    // Request async token
    token := ctx.RequestToken(func(tokenID string) {
        // Cleanup callback
    })
    
    // Start async work
    go func() {
        // Do async work...
        // Resume when done
        ctx.ResumeToken(token, true) // true = success
    }()
    
    return pawscript.TokenResult(token)
}
```

### Macros

```go
// Define macro
ps.DefineMacro("quick_save", "save_file; update_status 'Saved'")

// Execute macro
result := ps.ExecuteMacro("quick_save")

// List macros
macros := ps.ListMacros()

// Check if macro exists
if ps.HasMacro("quick_save") {
    // ...
}

// Delete macro
ps.DeleteMacro("quick_save")

// Clear all macros
count := ps.ClearMacros()
```

### Macros with Arguments

```go
// Define macro with arguments
ps.DefineMacro("greet", "echo 'Hello $1!'")

// Execute with arguments (via command)
ps.Execute("greet 'World'")

// Or use call command
ps.Execute("call greet, 'World'")
```

## Syntax

### Basic Commands

```pawscript
save_file
open_file '/path/to/file'
move_cursor 10, 5
```

### Command Sequences

```pawscript
# Sequence: Execute all commands
save_file; close_buffer; open_file "new.txt"

# Conditional: Stop on failure
save_file & close_buffer & exit

# Alternative: Stop on success
auto_save | prompt_save | cancel
```

### Brace Expressions

```pawscript
# Execute command and substitute result
echo {calculate 10, 5}  # Outputs result of calculate

# Nested braces
echo "Result: {echo {get_value}}"
```

### Macros

```pawscript
# Define macro
macro quick_save(save_file; update_status 'Saved')

# Execute macro
quick_save

# Macro with arguments
macro greet(echo 'Hello $1!')
greet 'World'  # Outputs: Hello World!
```

### Comments

```pawscript
# Line comment

#( Block comment
   spanning multiple
   lines )#

#{ Alternative block
   comment style }#
```

## Token-Based Suspension

For async operations:

```go
ps.RegisterCommand("async_operation", func(ctx *pawscript.Context) pawscript.Result {
    // Request a token to pause execution
    token := ctx.RequestToken(func(tokenID string) {
        fmt.Printf("Operation interrupted: %s\n", tokenID)
    })
    
    // Start async work
    go func() {
        time.Sleep(2 * time.Second)
        fmt.Println("Async operation completed")
        ctx.ResumeToken(token, true) // Resume with success
    }()
    
    return pawscript.TokenResult(token)
})

// This will pause at async_operation and resume when it completes
ps.Execute("async_operation; echo 'This runs after async completes'")
```

## Result Management

```go
ps.RegisterCommand("calculate", func(ctx *pawscript.Context) pawscript.Result {
    a := ctx.Args[0].(int64)
    b := ctx.Args[1].(int64)
    ctx.SetResult(a + b)  // Set formal result
    return pawscript.BoolResult(true)
})

// Result flows through sequences
ps.Execute("calculate 5, 3; print_result")  // Prints 8
```

## Testing

```go
package myapp_test

import (
    "testing"
    "github.com/yourusername/pawscript"
)

func TestMyCommand(t *testing.T) {
    ps := pawscript.New(nil)
    
    called := false
    ps.RegisterCommand("test_cmd", func(ctx *pawscript.Context) pawscript.Result {
        called = true
        return pawscript.BoolResult(true)
    })
    
    result := ps.Execute("test_cmd")
    
    if !called {
        t.Error("Command was not called")
    }
    
    if boolResult, ok := result.(pawscript.BoolResult); !ok || !bool(boolResult) {
        t.Error("Expected true result")
    }
}
```

## License

MIT

## Contributing

Contributions are welcome! Please ensure:
1. Code follows Go conventions
2. All tests pass
3. New features include tests
4. Documentation is updated

## Architecture

The implementation consists of:

- **types.go**: Core type definitions
- **state.go**: Execution state management
- **logger.go**: Logging with position tracking
- **parser.go**: Command parsing with source mapping
- **macro.go**: Macro system
- **executor.go** / **executor2.go**: Command execution engine
- **pawscript.go**: Main API
- **stdlib.go**: Standard library commands
- **cmd/paw/main.go**: CLI tool

## Differences from TypeScript Implementation

The Go implementation maintains the same functionality as the TypeScript version while being:

- **More concurrent**: Uses goroutines for async operations
- **Type-safe**: Strong typing throughout
- **Memory-safe**: No manual memory management concerns
- **Simpler**: Cleaner separation of concerns
- **Performant**: Native binary compilation

## Examples

See the `examples/` directory for sample scripts and usage patterns.
