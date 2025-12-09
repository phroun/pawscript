## Changelog

### 0.2.9 -- November 29 - December 7, 2025
- `break` and `continue` commands for loop control, with async resumption fixes
- Fixed command position execution for macros and parenthetic blocks
- Fixed `ret` not propagating result from macro calls
- `bubble_logging` system with multi-flavor bubble support for structured
  logging during execution
- `fizz` and `burst` commands for bubble iteration and consumption
- Type tracking added to StoredList for optimization hints (`arrsolid`,
  `mapsolid`, `arrlen`, `maplen`)
- Dollar-brace `${}` operator now properly escapes quotes when inside quoted
  strings
- File handles auto-close moved from variable deletion to garbage collector
  for more reliable cleanup
- Quote-aware macro argument substitution: `$1`, `$2`, etc. now correctly
  handle context when appearing inside or outside quoted strings
  - Inside quotes: content inserted without extra quotes, internal
    quotes/backslashes escaped, tilde injection prevented
  - Outside quotes: strings with spaces properly quoted
- Brace and parenthesis tracking in `$N` substitution: respects same
  structural boundaries as the parser
- Fixed premature garbage collection of local variables during async
  operations
- Fixed double-release of state references in chained async tokens
- Fixed GUI button callbacks to execute in correct module environment
- `macro_forward` command for forward declarations, enabling mutual recursion
- `LIBRARY` command enhancements:
  - `LIBRARY "restrict module::item1,item2"` restricts specific items
  - `LIBRARY "allow ::module::items"` supports scoped syntax
- `json` command for list serialization with options:
  - `pretty: true` for indented multi-line output
  - `color: true` for ANSI-colored output
- `string` command extended with `pretty:` and `color:` parameters
- `list` command extended with `from: json` for JSON deserialization
  - `merge: 0` option for array_1 format (no key merging)
- `arrlen` and `maplen` commands for quick list length by type
- `len` command enhanced with `keys: true` parameter
- `eqs` and `neqs` commands for shallow (reference) equality comparison
- `slice` command extended with `only: arr` or `only: map` to extract list
  portions by entry type
- `lib_dump` and `env_dump` headers now show command counts
- Pawgui launcher improvements:
  - File browser with double-click, scroll, and case-insensitive sorting
  - Filter box with keyboard navigation (arrows, Enter, Shift+Tab)
  - Home button and visual directory/file separation
  - Theme toggle (light/dark) with sun/moon indicators
  - Auto-select all text when `gui_entry` gains focus
  - Forward mouse events to terminal to clear selection on click
  - 4px spacer added to `gui_split` right panels
- Script isolation in launcher: scripts run in isolated snapshots using
  `CreateRestrictedSnapshot` and `ExecuteWithEnvironment`
- Explicit window targeting in gui_ commands: all gui_ commands now support
  both explicit window handle as first argument (`gui_label #mywin, "text"`)
  and automatic inherited `#window` from context
- Native OS dialog improvements: restored `.paw` file type filter, fixed nil
  pointer crash, suppressed macOS deprecation warnings
- Split lib_types.go into logical smaller files for maintainability
- Variable index accessor support for tilde expressions (`~list ~idx`)
- Route errors through `#err` channel for better error handling
- Fixed unpacking assignment with `=>` operator
- Fixed fail status in brace expressions to not cancel the outer command:
  - Brace expression failure can be found with {get_substatus}
- Fixed `msleep` corrupting execution state after async resume
- Performance optimizations:
  - AST caching for macro and loop body execution
  - Object pooling for `ExecutionState` to reduce GC pressure
  - Substitution templating for improved macro performance
  - Lazy-create `ModuleExports` and `bubbleMap` to reduce allocations
  - Handler caching with generation-based invalidation
- Raw keyboard input support:
  - `readkey_init` enables raw mode with optional echo channel
  - `readkey` returns single keypress as bytes
  - `readkey_end` restores normal terminal mode
  - Bracketed paste mode support for multi-line input
- Pawgui console improvements:
  - Non-blocking input queue for GUI console
  - Byte-driven channels for raw input support
  - Flow control to prevent output deadlock
  - Line assembly for read command in raw byte mode
- Fixed cursor ANSI output to route through channel properly
- Reference counting refactor:
  - `ObjectRef` type for unified object tracking
  - Token lifecycle management via garbage collector
  - Fixed refcount issues with empty lists and derived/sliced lists
- Pawgui-gtk: new GTK-based GUI alternative:
  - Custom purfecterm-gtk terminal emulator
  - Cross-platform font fallbacks (JetBrains Mono, Consolas, DejaVu, etc.)
  - VT100 double-size text rendering (DECDHL/DECDWL)
  - Bobbing wave animation for blink text attribute
  - Context menu with paste support
  - VGA/ANSI color palette mapping
  - macOS-style scrollbar styling using terminal background color
- Interactive REPL mode when `paw` is run without arguments
  - Persistent `ExecutionState` for REPL and host application use
  - Execute macros/blocks from tilde expressions in statement position
- REPL prompt improvements:
  - `paw*` prompt for normal input (replaces `"":`)
  - Nesting-aware continuation prompts: `(*`, `{*`, `"*`, `'*`, `#(*`
  - Full nesting stack shown (e.g., `({*` for braces inside parens)
  - Line numbers in dark cyan on continuation lines (starting from 2)
  - Background-aware prompt colors: yellow (dark bg) or dark brown (light bg)
  - Result `=` prefix: bright green (dark bg) or dark green (light bg)
  - Result values: dark gray (dark bg) or silver (light bg)
- CLI config file `~/.paw/paw-cli.psl`:
  - `term_background` setting: "auto", "dark", or "light"
  - Auto-created on first run with graceful failure if not writable
- PSL parsing improvements:
  - `ParsePSL` and `ParsePSLList` now strip comments before parsing
  - Supports `# ` line comments and `#( )#` block comments in config files
- Ported pawgui-gtk to Qt, with intention to support both options.
- Added dark & light mode option to config file for both GUI versions.
- Build system improvements:
  - GitHub workflow builds both GTK and Qt versions for Windows
  - Makefile targets for macOS .app bundles: `package-gtk-macos`, `package-qt-macos`

### 0.2.8 -- November 28-29, 2025 - Thanksgiving Alpha
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
- Renamed basic math module from math:: to basicmath:: (auto-imported)
- Auxiliary math library (math::) with trig functions (sin, cos, tan,
  atan2, deg, rad, log, log10, ln, pow) and constants (#tau, #e, #phi,
  #root2, #root3, #root5, #ln2) - requires `IMPORT math`
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
- File system module (files::) with secure sandbox:
  - `open`, `close`, `read`, `write`, `seek`, `flush`, `eof` for file handle operations
  - `read_file`, `write_file`, `append_file` for one-shot file access
  - `exists`, `is_file`, `is_dir`, `file_size`, `file_info` for file metadata
  - `mkdir`, `remove`, `rename`, `copy`, `list_dir` for file system manipulation
  - `temp_file`, `temp_dir` for temporary file creation
  - Sandbox restricts file access to script directory + explicit allow paths
  - CLI flags: `--allow-read`, `--allow-write`, `--allow-exec`, `--sandbox-dir`
  - `script_dir` added to `#args` for reliable path resolution
  - Exec restricted from running scripts in writable directories
- StoredBytes type for binary data handling:
  - `bytes` command creates byte arrays from integers, hex literals, or strings
  - Byte accessor syntax: `~myBytes 0` returns byte at index as int64
  - `slice`, `append`, `prepend`, `concat`, `compact` work on bytes
  - `len` returns byte count, hex display format `<DEADBEEF>`
- StoredStruct type for fixed-layout binary records:
  - `struct_def` creates struct definition from field descriptors
  - Field format: `("name", size, "mode")` for binary field specification
  - `struct` creates instances from definitions, optional source data and count
  - Struct arrays with `slice`, `compact`, index access (`~array 0`)
  - Field access via dot notation: `~myStruct.fieldName`
  - Struct definitions are StoredLists, enabling advanced customization
  - Extended field modes for binary data:
    - `bytes`, `string`, `struct` - basic modes
    - `int`/`int_be`, `int_le` - signed integers, big/little-endian
    - `uint`/`uint_be`, `uint_le` - unsigned integers, big/little-endian
    - `float`/`float_be`, `float_le` - IEEE 754 floats (4 or 8 bytes)
    - `bit0`-`bit7` - individual bits, size=0 to share byte with other bits
  - Bit modes enable packing 8 booleans in one byte (OR to set, AND to read)
  - Added extended struct field modes: endianness, unsigned, float, and bitfields
    - Endianness modes: int_be/int_le, uint_be/uint_le for big/little-endian
    - Unsigned integers: uint/uint_be/uint_le (no sign extension on read)
    - Float modes: float_be/float_le for IEEE 754 (4 or 8 byte) conversion
    - Bit modes: bit0-bit7 for individual bits, size=0 to share byte
    - Bit packing: up to 8 booleans in one byte (OR to set, AND to read)
    - Use toNumber for int field assignment (handles hex literals)
- New bitwise:: module with operations for int64 and bytes types:
  - bitwise_and, bitwise_or, bitwise_xor: binary ops with align/repeat options
  - bitwise_not: unary complement operation
  - bitwise_shl, bitwise_shr: shift left/right by N bits
  - bitwise_rol, bitwise_ror: rotate with configurable bitlength (default 8)
  - Output type matches first argument (int64 or bytes, preserving length)
  - align: left|right option for bytes of different lengths
  - repeat: true option for cyclic application (cipher-like XOR)
  - List support: operations applied element-wise when first arg is list
  - Handles both StoredList and ParenGroup inputs
- Implement `for` command with multiple forms:
  - Numeric ranges: `for 1, 10, i, (body)` with optional `by:` step
  - List iteration: `for ~list, item, (body)` with `order: descending`
  - Key-value pairs: `for ~list, key, value, (body)` over named args
  - Struct fields: `for ~struct, fieldname, value, (body)`
  - Generator/iterator: `for ~generator, x, (body)`
  - Unpack mode: `for ~list, (a, b, c), (body)`
- Add `iter:` and `index:` named args for iteration tracking
- Implement `range` command for explicit range generators
- Add ForContinuation type for resuming for loops after yield
- Full support for yield inside for loops in generators
- Support for async operations (msleep) inside for loops
- Add toFloat64 helper for numeric range parsing
- Added full CLI support to pawgui, run with --help
- Removed assumption of a single window in pawgui.
  - Creating a window with console: true will populate it with a terminal
    and grab #out/#in/#err to allow regular CLI-style PawScript interactions.
  - When all windows are closed, and all fibers have ended, the application
    will close itself.
- Added a primitive launcher to pawgui when invoked with no arguments.
  - CLI scripts will run directly inside the launcher's console window.
  - File -> New will create additional launcher instances.
- Explicit focus and autofocus of pawgui controls.
- Better default sizing and centering of pawgui console or launcher windows.
- Added automatic build process for pawgui for all platforms in Makefile
- Fixed a macro argument substitution bug for short string types.
- Native OS dialogs for File -> Open using sqweek.

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
- Fixed terminal color handling when output is redirected.
- Fixed async generator interleaving and iterator termination bugs.
- Generator completion now returns status false.
- Propagated brace expression status through assignment.
- Fixed nested while loops in generators.
- Added get_status command to core module.
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
