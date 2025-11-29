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

### 0.2.8 -- November 28, 2025
- Polymorphic commands: `append`, `prepend`, `contains`, `index` now work on
  both strings and lists
- `trim`, `trim_left`, `trim_right` accept optional character sets and work
  on lists with deep comparison
- `replace` command is polymorphic with `count:` parameter for controlling
  replacements
- `starts_with`, `ends_with`, `split`, `join` are now polymorphic for lists
- Deep equality comparison for list operations (`contains`, `index`, `eq`)
- Comparison operators (`gt`, `lt`, `eq`, `gte`, `lte`) support multiple
  arguments and proper string comparison
- `repeat` command (renamed from `str_repeat`) is polymorphic: repeats strings,
  lists, or repeatedly executes a block accumulating results
- Type conversion commands: `string`, `number`, `block` for explicit type
  coercion
- Renamed `fiber_spawn` to `fiber` for brevity
- Renamed `get_type` to `type` and `get_inferred_type` to `infer`
- Renamed `str_upper`/`str_lower` to `upper`/`lower`
- Removed `get_val` command, replaced by accessor notation `~list.key`
- `len` now returns int64 for consistency
- Direct block execution in command position: `{~block}` or
  `{~block} args` syntax
- `?` operator for existence checking (undefined/defined testing)
- `stack_trace` command for runtime call stack introspection
- Tilde in command position (`~x`) is now implicit `set_result ~x`
- Bubble system for out-of-band value accumulation (`bubble_up`, `bubble_pop`,
  `bubble_dump`)
- `fiber_bubble` command for early bubble transfer from running fibers
- Orphaned bubble system: abandoned fiber bubbles automatically transfer to
  parent
- Auto-dump remaining bubbles when script execution ends
- Host application I/O stream configuration via `config.Stdin`,
  `config.Stdout`, `config.Stderr`
- Moved `if` command to core module
- `string` command returns block content when given a block
- Added documentation for COW (copy-on-write) inheritance system
- Added documentation for result passing system (result-flow.md)
- Added Fibonacci benchmark examples
- Auxiliary math library (math::) with trig functions (sin, cos, tan,
  atan2, deg, rad, log, log10, ln) and constants (#tau, #e, #phi, #root2,
  #root3, #root5, #ln2) - requires `IMPORT math`
- Renamed basic math module from math:: to basicmath:: (auto-imported)
- Named argument tilde resolution: both keys (`~dynKey:`) and values
  (`: ~dynVal`) now resolve
- Accessor syntax support in named argument keys (`~obj.field:`, `~list 0:`)
- Scope operator `::` for explicit module access
  - `module::item` searches LibraryRestricted (requires IMPORT first)
  - `::module::item` searches LibraryInherited (from all loaded modules)
- Random number generation using token pattern:
  - `rng` command creates RNG token, optional `seed:` parameter for
    reproducibility
  - `resume ~token` returns full Int63 range, `resume ~token, max`
    returns 0 to max-1
  - `resume ~token, min, max` returns random value in range min to max
    (inclusive)
  - `random` convenience command uses default `#random` or accepts custom
    generator
  - `#random` default RNG in io:: module, can be overridden locally for
    reproducible tests
  - Uses Go's `math/rand` with `NewSource` for seeded generators

### 0.2.7 -- November 27, 2025
- Move Makefile into a more standard location
- Added --license switch to show the MIT License.
- Fixed bug where object markers would resolve too early when passing
  parameters to macros.
- Made "channel" and "fiber" fully fledged types with type command support.
- Removed outdated MacroSystem now that the Environments hold macros.
- PopulateIOModule now creates StoredChannels for #stdin, #stdout, and #stderr.
- Better error checking when host environment uses SetResult.
- Added host helpers: ExecuteInRoot and ImportModuleToRoot.
- Added a proof of concept project, pawgui, to integrate Fyne with PawScript.
- Add immediate focus handling for sizedWidget terminal wrapper
- Fixed ExecuteFile to wait for async completion before merging exports.
- Fixed waitChan propagation for chained async tokens.
- Fixed fiber spawn macro lookup.
- Fixed tilde injection vulnerability in macro argument substitution.
- Fixed tilde injection in variable interpolation (external input.)
- Fixed remaining tilde injection vulnerabilities in brace expressions.
- Updated pawgui demos to use PawScript color and clear commands.
- Added channel-aware terminal capabilities for multiple consoles.
- Fixed color/clear commands to send ANSI to correct channel.
- Added InBraceExpression flag for brace expression output purity.
- Updated pawgui demos to use inline brace color expressions.
- Added generator/coroutine support with yield inside while loops.
- Added each and pair iterators for efficient list iteration.
- Added comprehensive generator test suite with expected output.
- Fixed terminal color handling when output is redirected.
- Fixed async generator interleaving and iterator termination bugs.
- Generator completion now returns status false.
- Propagated brace expression status through assignment.
- Fixed nested while loops in generators.
- Added get_status command to core module.
- Added tests for empty generator and mid-generator error handling
- Added if command validation and warnings for improper usages.
- Added get_substatus command to check brace expression failures.
- Added proper undefined result handling for unknown commands.
- Add while command argument validation warnings.
- Support for single list arguments passed to math operations
- Added log categories to all error/warning/debug messages
- Expanded log levels to seven severity levels.
- Added comprehensive logging configuration system with error_logging
  and debug_logging commands
- Add Network category, IO Category and multi-category logging support.
- Extended log_print to support multiple categories for a single entry.
- Add #debug channel for separate debug output redirection.
- Added list accessor syntax (~list.key for named args, ~list N for index)
- Added multidimensional and mixed accessor tests
- Support for index-then-dot accessor syntax (~students 1.firstName)
- Stricter number parsing: dots only in floats when digit before and after.

### 0.2.6 -- November 25, 2025
- `env_dump` returns all environment variables as a list with named args
- `rune` converts a character to its Unicode code point (inverse of `chr`)
- `ord` alias for `rune` for familiarity with other languages
- `microtime` returns current Unix timestamp in microseconds
- `datetime` returns formatted date/time string, accepts optional format string
- `clear` clears terminal screen with optional mode: eol, bol, line, eos, bos, screen
- `cursor` controls terminal cursor position, visibility, shape, and echo (duplex)
  - Supports sticky parameters: xbase, ybase, rows, cols, indent, head
  - Options: x, y, visible, shape, blink, color, free, duplex, reset
  - Returns current cursor state as a list with named args
- `color` sets terminal foreground/background colors using CGA palette (0-15)
  - Accepts color numbers or names: black, blue, green, cyan, red, purple, brown,
    silver, light blue, bright green, aqua, bright red, pink, yellow, white
  - Attributes: bold, blink, underline, invert (persist across calls)
  - Option reset: true emits ANSI reset and clears all tracked state
  - Returns current color state with terminal capability info (term, ansi, color)
- Macro storage moved from global to module environment with copy-on-write
- `LIBRARY "forget *"` removes all items from Library
- `LIBRARY "forget modname"` removes an entire module from Library
- `LIBRARY "forget modname::item1,item2"` removes specific Library items
- `EXPORT "modspec::*"` re-exports all items from restricted Library
- `EXPORT "modspec::new=item1,item2"` re-exports specific items with optional rename
- Tilde interpolation (`~#stdout`, `~#stdin`) now resolves IMPORTed items
- Math module extended with variadic and new operations:
  - `add`, `mul` now accept any number of arguments
  - `sub` accepts any number of arguments (first minus all others)
  - `div` renamed to `idiv` for floored integer division
  - `fdiv` added for floating point division
  - Division commands support multiple divisors (uses product of all)
  - `remainder:` and `modulo:` named args return `[quotient, remainder/modulo]`
  - `iremainder`, `imodulo`, `fremainder`, `fmodulo` return only remainder/modulo
  - `floor`, `ceil`, `trunc`, `round`, `abs` single-argument functions
  - `min`, `max` accept any number of arguments

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
- `sort` command for lists with optional custom comparator and `desc:` parameter
- `while` loop condition now supports async operations (e.g., `msleep`)

### 0.2.4 -- November 23, 2025
- Commands/macros now support named arguments, separated by colon.
- Lists can now function as a key:value store (map/dictionary/hash-table)
- `keys` returns a list of keys from a list
- Accessor notation `~list.key` extracts a named value from a list

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
