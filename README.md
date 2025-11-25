# PawScript

PawScript: a command language with token-based suspension suitable for
general purpose scripting, but built with application integration in mind.

## Features

- **Cross-Platform**: Native builds for arm64/x86_64 on macOS, Linux, Windows, plus WebAssembly
- **Module System**: Namespaced standard library (core::, io::, os::, math::, str::, channels::, fibers::)
- **Fibers & Channels**: Lightweight concurrent execution with channel-based communication
- **Token-Based Suspension**: Pause and resume execution for async operations
- **Macro System**: Reusable command sequences with lexical scoping and argument substitution
- **Immutable Lists**: Copy-on-write semantics with named argument support (key: value)
- **Custom IO Channels**: Host apps can provide custom stdin/stdout/stderr handlers
- **Brace Expressions**: Inline command evaluation with `{...}` substitution
- **Thread-Safe**: All operations safe for concurrent use
- **Host Integration**: Clean API for embedding in applications

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

Core components in `src/`:

- **pawscript.go**: Main API and PawScript instance
- **types.go**: Core type definitions (StoredList, StoredChannel, StoredMacro, etc.)
- **parser.go**: Command parsing with source mapping
- **executor_*.go**: Execution engine (core, commands, tokens, resolution, substitution, formatting)
- **state.go**: Execution state and variable management
- **module.go**: Module environment with copy-on-write semantics
- **macro.go**: Macro system with lexical scoping
- **channel.go**: Channel implementation for inter-fiber communication
- **fiber.go**: Fiber system for concurrent execution

Standard library in `src/lib_*.go`:

- **lib_core.go**: core::, macros::, flow::, debug:: modules
- **lib_math.go**: math::, cmp:: modules
- **lib_types.go**: strlist::, str:: modules
- **lib_system.go**: os::, io::, sys:: modules
- **lib_channels.go**: channels:: module
- **lib_fibers.go**: fibers:: module

Supporting files:

- **stdlib.go**: Library registration and helpers
- **io_channels.go**: Native IO channel setup (with custom handler support)
- **os_args.go**: Script argument handling
- **logger.go**: Logging with position tracking
- **cmd/paw/main.go**: CLI tool

## Examples

See the `examples/` directory for sample scripts and usage patterns.

## Changelog

### 0.2.5 -- November 24, 2025
- Module environment with copy-on-write semantics for macro/fiber isolation
- Native IO channels: `io::#stdin`, `#stdout`, `#stderr`, `#stdio`
  (and short aliases `#in`, `#out`, `#err`, `#io`)
- Host applications can provide custom IO channel handlers via
  `RegisterStandardLibraryWithIO()`
- `include` command for sourcing files, with optional selective imports
- `os::#args` provides script arguments as a StoredList; `argc`/`argv` use
  it by default
- `x: undefined` syntax deletes a variable from local scope
- `echo`, `print`, `write`, `read` accept `#channel` as first arg to override
  default output/input
- Reorganized standard library into modular source files

### 0.2.4 -- November 23, 2025
- Commands/macros now support named arguments, separated by colon.
- Lists can now function as a key:value store (map/dictionary/hash-table)
- `keys` returns a list of keys from a list
- `get_val` given a list and a key, extracts the corresponding value

### 0.2.3 -- November 22, 2025
- New concise syntax for get ~x and set x:

### 0.2.2 -- November 21, 2025
- Revised memory handling for list, block, and string types.
- Improved consistency of error message handling.
- Provided print_log to allow deliberate generation of debug messages,
  warnings, and errors.

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
