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
call {macro ~code}                    # Convert to macro and execute
```

Blocks can be used directly as conditions or bodies in `while`, or as the body of a `macro` definition.

### Braces `{...}` - Evaluate Immediately

Content is executed as a command and the formal result substituted in place.

```paw
x: 5
echo "Next is {add ~x, 1}"   # Prints: Next is 6
y: {mul ~x, 2}               # y becomes 10
sum: {add 2, 3}              # sum becomes 5
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
!false              # Returns success (status inverted)
not eq 1, 2         # Returns success (1 != 2, so eq fails, inverted to success)
```

---

## Chain Operators

### `~>` - Chain (formal result as first argument)

```paw
add 2, 3 ~> mul 10    # mul 5, 10 = 50
```

### `~~>` - Chain Append (formal result as last argument)

```paw
add 2, 3 ~~> sub 10   # sub 10, 5 = 5
```

### `=>` - Capture Formal Result

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
len ~nums => size                    # size = 3
slice ~nums, 1, 2 => middle          # Items at indices 1-2
append ~nums, 4 => extended          # New list with 4 added
keys ~with_named => keylist          # Get named argument keys
get_val ~with_named, key => val      # val = "value"
sort ~nums, desc: true => descending # Sort descending
```

### Unpacking

```paw
(a, b, c): ~nums       # a=1, b=2, c=3
(first, , last): ~nums # Skip middle with extra comma
```

---

## Hash `#` Prefix - Object References

The `#` prefix usually references module-level objects. These can be temporarily overshadowed in local scope:

```paw
#out     # Standard output channel
#err     # Standard error channel
#args    # Script arguments list

# Override locally to redirect output
#out: {channel 10}   # Now print/echo go to this channel
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
    true   # Ensure else is skipped
) else (
    echo "x is not five"
)
```

**Important:** There is no `else if` construct. The `else` (or `|`) reacts to the status of the last command that executed, which may be inside the `then` block. If in doubt, end your `then` block with the `true` command to ensure the `else` is skipped.

### While Loops

```paw
i: 0
while (lt ~i, 5), (
    echo ~i
    i: {add ~i, 1}
)
```

### Early Return

```paw
macro check_positive(
    lt $1, 0 then ret false
    ret true
)
```

---

## Macros

### Defining Macros

```paw
macro greet(
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
macro show_all(
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
channel_recv ~ch => msg
channel_close ~ch
```

---

## Fibers (Concurrency)

```paw
macro worker(
    msleep 100
    echo "from fiber"
)

h: {fiber worker}
echo "from main"
fiber_wait ~h           # Wait for single fiber
fiber_wait_all          # Wait for all fibers
```

---

## Excerpts from the Standard Library

### Core (`stdlib`)

| Command | Description |
|---------|-------------|
| `true` | Sets success status |
| `false` | Sets failure status |
| `ret [value]` | Early return from block with optional value |
| `set_result value` | Set the formal result |
| `get_result` | Get the formal result value |
| `if value` | Tests truthiness: sets success if truthy, failure if falsy; also sets formal result to boolean |
| `eq a, b` | Sets success if a equals b, failure otherwise; also sets formal result to boolean |
| `macro name(body)` | Define a named macro |
| `add a, b` | Add numbers; formal result is the sum |

### Lists and Data (`stdlib`)

| Command | Description |
|---------|-------------|
| `list items...` | Create immutable list |
| `len list` | Get length of list or string |
| `sort list, [cmp], [desc: true]` | Sort list items |
| `keys list` | Get named argument keys from list |
| `get_val list, key` | Get named argument value by key |

### Strings (`stdlib`)

| Command | Description |
|---------|-------------|
| `upper str` | Convert to uppercase |
| `lower str` | Convert to lowercase |

### I/O (`io`)

| Command | Description |
|---------|-------------|
| `print args...` | Print with spaces and newline |
| `write [#chan], args...` | Print without newline; optionally specify channel like `#err` |
| `read [#chan]` | Read line from input or specified channel |

### Channels (`stdlib`)

| Command | Description |
|---------|-------------|
| `channel [size]` | Create channel (default unbuffered) |

### Concurrency (`stdlib`)

| Command | Description |
|---------|-------------|
| `fiber macro` | Spawn concurrent fiber running the macro |
| `fiber_wait handle` | Wait for fiber to complete |
| `fiber_wait_all` | Wait for all fibers |
| `msleep ms` | Sleep for milliseconds |

### Control Flow (`stdlib`)

| Command | Description |
|---------|-------------|
| `while (cond), (body)` | Loop while condition is true |

---

## Creating Modules with Super Commands

Super commands (UPPERCASE) manage the module system. They enable you to create reusable libraries with controlled exports and restricted access.

### Setting Up a Module

```paw
# mymodule.paw
MODULE "mymath"

macro double(
    mul $1, 2
)

macro triple(
    mul $1, 3
)

# Export only what users should access
EXPORT double, triple
```

### Including with Restrictions

The caller controls what gets imported. When you `include` a module, it runs in an isolated environment. You then use `IMPORT` to bring specific items into your namespace.

```paw
# main.paw

# Include the module (runs it, captures exports)
include (mymath), "mymodule.paw"

# Import what you need into your namespace
IMPORT "mymath::double"
IMPORT "mymath::triple"

# Now call directly (no namespace prefix)
double 5 => result    # result = 10
triple 3 => result    # result = 9
```

### Super Command Reference

| Command | Description |
|---------|-------------|
| `MODULE "name"` | Set default module name for exports |
| `EXPORT items...` | Export macros, commands, or `#objects` to module |
| `IMPORT "module"` | Import all items from a module |
| `IMPORT "module::item1,item2"` | Import specific items |
| `IMPORT "module::newname=origname"` | Import with renaming |
| `REMOVE items...` | Remove imported items |
| `REMOVE ALL` | Reset to clean slate |
| `LIBRARY "restrict *"` | Restrict all library access |
| `LIBRARY "restrict module"` | Restrict specific module |
| `LIBRARY "allow module"` | Allow access to module |
| `LIBRARY "allow module::items"` | Allow specific items only |
| `LIBRARY "allow newname=oldname"` | Allow with renaming |

### Complete Module Example

**mathlib.paw:**
```paw
MODULE "math"

macro square(
    mul $1, $1
)

macro cube(
    mul $1, {mul $1, $1}
)

macro is_even(
    n: $1
    remainder: {sub ~n, {mul {div ~n, 2}, 2}}
    eq ~remainder, 0
)

EXPORT square, cube, is_even
```

**Using with restrictions:**
```paw
# Restrict everything first
LIBRARY "restrict *"

# Only allow our math module
LIBRARY "allow math"

# Include the module - it runs in restricted environment
include (math), "mathlib.paw"

# Import the functions we want
IMPORT "math::square"
IMPORT "math::cube"
IMPORT "math::is_even"

# Use exported functions directly
square 4 => result      # 16
cube 3 => result        # 27
is_even 7 then echo "even" else echo "odd"  # odd
```

### Renaming on Import

```paw
include (math), "mathlib.paw"

# Import with a new name
IMPORT "math::sq=square"

# Now accessible as sq
sq 5 => result    # 25
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
cmd1 ~> cmd2             # Formal result as first arg
cmd1 ~~> cmd2            # Formal result as last arg
cmd1 => var              # Capture formal result

# Blocks
(code)                   # Store for later
{code}                   # Execute now

# Macros
macro name(body)         # Define
$1 $2 $* $@ $#           # Arguments

# Lists
{list a, b, c}           # Create
len ~list => n           # Length
(x, y, z): ~list         # Unpack
```
