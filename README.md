# PawScript

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

PawScript: a command language with token-based suspension suitable for
general purpose scripting, but built with application integration in mind.

*If you use this, please support me on ko-fi:  [https://ko-fi.com/jeffday](https://ko-fi.com/F2F61JR2B4)*

[![ko-fi](https://ko-fi.com/img/githubbutton_sm.svg)](https://ko-fi.com/F2F61JR2B4)

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

See CHANGELOG.md
