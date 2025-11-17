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
