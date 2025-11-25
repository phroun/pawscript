# PawScript Language Guide

A concise reference to PawScript syntax and core concepts.

---

## Comments

```paw
# Line comment (must follow whitespace or start of line)
#!/usr/bin/env paw   # Shebang supported

#( Block comment
   spans multiple lines
)#
```

---

## Data Types

| Type | Examples |
|------|----------|
| Symbol | `foo`, `my_var`, `x` |
| Integer | `42`, `-7`, `0` |
| Float | `3.14`, `-0.5` |
| String | `"hello"`, `'world'` |
| Boolean | `true`, `false` |
| Nil | `nil` |
| List | `{list 1, 2, 3}` |
| Block | `(echo hello)` |

---

## Strings and Escapes

Both `"double"` and `'single'` quotes support escapes:

| Escape | Meaning |
|--------|---------|
| `\n` | Newline |
| `\t` | Tab |
| `\r` | Carriage return |
| `\\` | Backslash |
| `\"` `\'` | Quote |
| `\xHH` | Hex byte |
| `\uXXXX` | Unicode (4 hex) |
| `\UXXXXXXXX` | Unicode (8 hex) |

---

## Variables

### Assignment with `:`

```paw
x: 42              # Assign 42 to x
name: "Alice"      # Assign string
x: undefined       # Delete variable x
```

### Reading with `~`

```paw
echo ~x            # Print value of x
echo ~name         # Print "Alice"
y: ~x              # Copy x to y
```

### Dynamic References

```paw
varname: "x"
echo ~~varname     # Resolve varname, then resolve that (prints value of x)
echo ~{get_result} # Use result of expression as variable name
```

---

## Parentheses vs Braces

### Parentheses `(...)` - Store Code as Text

Content is stored literally for later execution. Braces inside are NOT evaluated.

```paw
code: (echo "hello"; echo "world")   # Store commands
call ~code                            # Execute later
```

### Braces `{...}` - Evaluate Immediately

Content is executed and the result substituted in place.

```paw
x: 5
echo "x is {~x}"       # Prints: x is 5
echo {add 2, 3}        # Prints: 5
y: {mul ~x, 2}         # y becomes 10
```

---

## Command Separators

| Operator | Keyword | Meaning |
|----------|---------|---------|
| `;` | | Sequential execution |
| `&` | `then` | Execute next only if previous succeeded |
| `\|` | `else` | Execute next only if previous failed |

```paw
echo one; echo two              # Both run
eq 1, 1 then echo "equal"       # Conditional
eq 1, 2 else echo "not equal"   # Runs on failure
```

---

## Status Inversion

Prefix `!` or `not` inverts success/failure:

```paw
!false              # Returns true
not eq 1, 2         # Returns true (1 != 2)
```

---

## Chain Operators

### `~>` - Chain (result as first argument)

```paw
add 2, 3 ~> mul 10    # mul 5, 10 = 50
```

### `~~>` - Chain Append (result as last argument)

```paw
add 2, 3 ~~> sub 10   # sub 10, 5 = 5
```

### `=>` - Capture Result

```paw
add 2, 3 => sum       # sum = 5
mul 4, 5 => product   # product = 20
```

---

## Lists

### Creating Lists

```paw
nums: {list 1, 2, 3}
mixed: {list "a", 42, true}
with_named: {list 1, 2, key: "value"}
```

### List Operations

```paw
len ~nums              # 3
slice ~nums, 1, 2      # Items at indices 1-2
append ~nums, 4        # New list with 4 added
keys ~with_named       # Get named argument keys
get_val ~with_named, key  # Get "value"
```

### Unpacking

```paw
(a, b, c): ~nums       # a=1, b=2, c=3
(first, _, last): ~nums  # Skip middle with _
```

---

## Hash `#` Prefix - Object References

The `#` prefix references module-level objects:

```paw
#out     # Standard output channel
#err     # Standard error channel
#args    # Script arguments list
```

---

## Control Flow

### Conditional Execution

```paw
eq ~x, 5 then echo "x is 5"
eq ~x, 5 then echo "yes" else echo "no"

# Nested with blocks
eq ~x, 5 then (
    echo "x is five"
    echo "definitely"
) else (
    echo "x is not five"
)
```

### While Loops

```paw
i: 0
while (lt ~i, 5) (
    echo ~i
    i: {add ~i, 1}
)
```

### Early Return

```paw
macro check_positive (
    lt $1, 0 then ret false
    ret true
)
```

---

## Macros

### Defining Macros

```paw
macro greet (
    echo "Hello, $1!"
)
greet "World"    # Prints: Hello, World!
```

### Macro Arguments

| Variable | Meaning |
|----------|---------|
| `$1`, `$2`, ... | Individual arguments (1-indexed) |
| `$*` | All arguments as comma-separated text |
| `$@` | All arguments as a list object |
| `$#` | Argument count |

```paw
macro show_all (
    echo "Count: $#"
    echo "Args: $*"
)
show_all a, b, c    # Count: 3, Args: a, b, c
```

### Anonymous Macros

```paw
fn: {macro (echo "I'm anonymous: $1")}
call ~fn, "test"
```

---

## Channels

Channels enable communication between code paths:

```paw
ch: {channel 10}              # Buffered channel (size 10)
channel_send ~ch, "message"
msg: {channel_recv ~ch}
channel_close ~ch
```

---

## Fibers (Concurrency)

```paw
# Spawn concurrent fiber
f: {fiber_spawn (
    msleep 100
    echo "from fiber"
)}

echo "from main"
fiber_wait ~f           # Wait for single fiber
fiber_wait_all          # Wait for all fibers
```

---

## Excerpts from the Standard Library

### Core (`stdlib`)

| Command | Description |
|---------|-------------|
| `true` | Returns boolean true |
| `false` | Returns boolean false |
| `ret [value]` | Early return from block with optional value |
| `set_result value` | Set the result register |
| `get_result` | Get the result register value |
| `if value` | Convert value to boolean (truthiness test) |
| `eq a, b` | Returns true if a equals b |
| `macro name, (body)` | Define a named macro |

### Lists and Data (`stdlib`)

| Command | Description |
|---------|-------------|
| `list items...` | Create immutable list |
| `len list` | Get length of list or string |
| `sort list, [comparator], [desc: true]` | Sort list items |
| `keys list` | Get named argument keys from list |
| `get_val list, key` | Get named argument value by key |
| `add a, b` | Add numbers |

### Strings (`stdlib`)

| Command | Description |
|---------|-------------|
| `str_upper str` | Convert to uppercase |
| `str_lower str` | Convert to lowercase |

### I/O (`io`)

| Command | Description |
|---------|-------------|
| `print args...` | Print with spaces and newline |
| `write args...` | Print without newline |
| `read [channel]` | Read line from input |
| `#out` | Standard output channel |
| `#err` | Standard error channel |

### Channels (`stdlib`)

| Command | Description |
|---------|-------------|
| `channel [size]` | Create channel (default unbuffered) |

### Concurrency (`stdlib`)

| Command | Description |
|---------|-------------|
| `fiber_spawn (body)` | Spawn concurrent fiber |
| `fiber_wait fiber` | Wait for fiber to complete |
| `fiber_wait_all` | Wait for all fibers |
| `msleep ms` | Sleep for milliseconds |

### Control Flow (`stdlib`)

| Command | Description |
|---------|-------------|
| `while (condition) (body)` | Loop while condition is true |

---

## Creating Modules with Super Commands

Super commands (UPPERCASE) manage the module system. They enable you to create reusable libraries with controlled exports and restricted access.

### Setting Up a Module

```paw
# mymodule.paw
MODULE "mymath"

macro double (
    mul $1, 2
)

macro triple (
    mul $1, 3
)

# Export only what users should access
EXPORT double, triple
```

### Including with Restrictions

The caller controls what gets imported:

```paw
# main.paw

# Import only specific items from the module
include (mymath), "mymodule.paw"

# Now double and triple are available as mymath::double, mymath::triple
mymath::double 5    # Returns 10
```

### Super Command Reference

| Command | Description |
|---------|-------------|
| `MODULE "name"` | Set default module name for exports |
| `EXPORT items...` | Export macros, commands, or `#objects` to module |
| `IMPORT "module"` | Import all items from a module |
| `IMPORT "module::item1,item2"` | Import specific items |
| `IMPORT "module::orig=alias"` | Import with renaming |
| `REMOVE items...` | Remove imported items |
| `REMOVE ALL` | Reset to clean slate |
| `LIBRARY restrict *` | Restrict all library access |
| `LIBRARY restrict module` | Restrict specific module |
| `LIBRARY allow module` | Allow access to module |
| `LIBRARY allow module::items` | Allow specific items only |

### Complete Module Example

**mathlib.paw:**
```paw
MODULE "math"

macro square (
    mul $1, $1
)

macro cube (
    mul $1, {mul $1, $1}
)

macro is_even (
    n: $1
    remainder: {sub ~n, {mul {div ~n, 2}, 2}}
    eq ~remainder, 0
)

EXPORT square, cube, is_even
```

**Using with restrictions:**
```paw
# Restrict everything first
LIBRARY restrict *

# Only allow our math module
LIBRARY allow math

# Include the module - it runs in restricted environment
include (math), "mathlib.paw"

# Use exported functions
math::square 4      # 16
math::cube 3        # 27
math::is_even 7     # false
```

### Renaming on Import

```paw
include (m=math), "mathlib.paw"

# Now accessible as m::square instead of math::square
m::square 5    # 25
```

---

## Quick Reference

```paw
# Variables
x: 42                    # Assign
echo ~x                  # Read
x: undefined             # Delete

# Flow
cmd1; cmd2               # Sequential
cmd1 then cmd2           # If cmd1 succeeds
cmd1 else cmd2           # If cmd1 fails
!cmd                     # Invert status

# Chaining
cmd1 ~> cmd2             # Result as first arg
cmd1 ~~> cmd2            # Result as last arg
cmd1 => var              # Capture result

# Blocks
(code)                   # Store for later
{code}                   # Execute now

# Macros
macro name (body)        # Define
$1 $2 $* $@ $#          # Arguments

# Lists
{list a, b, c}          # Create
len ~list               # Length
(x, y, z): ~list        # Unpack
```
