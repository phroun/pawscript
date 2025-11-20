# PawScript

PawScript: a command language with token-based suspension suitable for
general purpose scripting, but built with application integration in mind.

## Features

- **Cross-Platform**: Built natively for arm64 or x86_64 on any of macOS, Linux, and MS-Windows, and also for WebAssembly (Node or Browser)
- **Complex Command Syntax**: Support for sequences (`;`), conditionals (`&`), and alternatives (`|`)
- **Token-Based Suspension**: Pause and resume command execution for long-running operations
- **Macro System**: Define and execute reusable command sequences with argument substitution
- **Syntactic Sugar**: Automatic transformation of convenient syntax patterns
- **Brace Expressions**: Execute commands within `{...}` and substitute results
- **Thread-Safe**: All operations are safe for concurrent use
- **Host Agnostic**: Clean interface for integration with any application
- **Command Line Tool**: Execute PawScript files directly from the command line

## Installation

Detailed instructions coming soon.

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

## Examples

See the `examples/` directory for sample scripts and usage patterns.

## Changelog

### 0.2.2 -- November 20, 2025
- Revised memory handling for list, block, and string types.

### 0.2.1 -- November 18, 2025
- Macro local scope was broken, now fixed
- Using list/group args for set now unpacks into variable names in first arg.

### 0.2.0 -- November 13-16, 2025
- PawScript is entirely re-written in Go for improved performance.
- Added set_result to set the formal result of a brace evaluation or macro
- Added get_result to receive the current formal result
- Differentiated write (non-terminated) vs echo/print (newline-terminated)
- Automatic escaping during string interpolation
- Automatic escaping during macro argument substitution
- Context sensitive escaping
  - Inside of a quoted string literal, will not break the string
  - Outside, restores and wraps the type:
    - bare: symbol, int, float, bool
    - "quoted": string
    - (parenthetic): block or argument list literal
- Execution of child processes is possible now, will need to be configurable later
- Asychronous brace evaluation
- Local variables at macro scope with get and set commands
- Comparison operators: gt, lt, eq, gte, lte
- Parenthetical blocks can take the place of any command
- Status inversion prefix ! before commands or blocks
- "if" translates a value into a status based on truthiness
- "then" means "&" and "else" means "|"
- Formalized regression testing for many features to ensure stability
- Primitive Type System
- Arithmetic commands add, sub, mul, div
- Added while loop with two block arguments
- Can now use ${} to unescape a block
- Can use $@ to get a block of current macro arguments
- Type Introspection
- Introduced list type, supported by argc and argv as first argument
- To help with shell scripting, a comment can now begin with #!
- ret command for early exit from code blocks
  - takes optional status and optional formal result
- A full slate of escape sequences for control characters, hex, Unicode.
- Fixed long-standing issue with inaccurate line & column reporting for errors.
- String library functions
- Unified for both list and string:  slice, len, concat

### 0.1.3 -- July 25-31, 2025
- Implemented braces for command evaluation (function-like behavior)
- Implemented substitution for macro arguments $* $# $1 $2
- Added result management system with formal results, in addition to the success/fail states
- Added command-line tool (`paw`) for executing PawScript files
- Added standard library commands (argc, argv, echo, read, true, false)
- Fixed syntactic sugar parsing for multi-line content
- Fixed token suspension and resumption for async operations
- Improved macro execution with proper state management
- Enhanced test coverage and documentation

### 0.1.2 -- July 24, 2025
- Minor fixes

### 0.1.1 -- July 24, 2025
- Initial release
- Basic command execution with sequences, conditionals, and alternatives
- Token-based suspension system ("paws" feature)
- Macro system with define/execute/list capabilities
- Syntactic sugar for convenient command syntax
- Full TypeScript support with comprehensive type definitions
- Host-agnostic design for easy integration
- Comprehensive test suite and documentation
