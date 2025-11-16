# PawScript Immutable Lists

## Overview

PawScript now has a proper **immutable list type** (`PawList`) with memory-efficient operations. Lists are **functional** - all operations return new lists rather than modifying existing ones.

## Key Features

- ✅ **Immutable**: Lists never change after creation
- ✅ **Memory efficient**: Slicing shares backing arrays (zero-copy)
- ✅ **Type safe**: Distinct from strings, blocks, and ParenGroups
- ✅ **Thread safe**: Immutability guarantees concurrent safety
- ✅ **Go-native**: Leverages Go's slice efficiency

## List Type

### Internal Structure
```go
type PawList struct {
    items []interface{}  // Never mutated after creation
}
```

### Memory Characteristics

| Operation | Time | Space | Backing Array |
|-----------|------|-------|---------------|
| Create | O(n) | O(n) | New |
| Slice | **O(1)** | **O(1)** | **Shared** ✅ |
| Append | O(n) | O(n) | New |
| Prepend | O(n) | O(n) | New |
| Concat | O(n+m) | O(n+m) | New |
| Get | O(1) | O(1) | Shared |
| Len | O(1) | O(1) | - |

## Commands

### Creating Lists

#### `list` - Create a new list
```pawscript
# From individual arguments
set fruits, {list apple, banana, cherry}

# From ParenGroup
set colors, {list ${set_result (red, green, blue)}}

# Empty list
set empty, {list}

# With variables
set coords, {list {get x}, {get y}, {get z}}
```

### Querying Lists

#### `len` - Get length of list or string
```pawscript
echo {len {get fruits}}        # Output: 3
echo {len "hello"}              # Output: 5 (works on strings too!)
```

#### `get_type` / `get_inferred_type` - Type detection
```pawscript
set mylist, {list 1, 2, 3}
echo {get_type mylist}          # Output: list
echo {get_inferred_type {get mylist}}  # Output: list
```

#### `argc` - Count items (enhanced)
```pawscript
echo {argc {get fruits}}        # Works with PawList
echo {argc (a, b, c)}          # Works with ParenGroup
echo {argc}                    # Script arguments count
```

#### `argv` - Access items (enhanced, 1-indexed)
```pawscript
echo {argv {get fruits}, 1}     # First item: apple
echo {argv {get fruits}, 2}     # Second item: banana
echo {argv {get fruits}}        # All items as []interface{}
```

### Transforming Lists

#### `slice` - Extract sublist (end exclusive, zero-copy!)
```pawscript
set nums, {list 1, 2, 3, 4, 5}

# Get first 3 items [0:3]
set first, {slice {get nums}, 0, 3}     # [1, 2, 3]

# Get items from index 2 to end
set tail, {slice {get nums}, 2, -1}     # [3, 4, 5]

# Get middle items [1:4]
set middle, {slice {get nums}, 1, 4}    # [2, 3, 4]

# Slicing shares backing array - O(1) time and space!
```

**String slicing works too:**
```pawscript
set text, "hello world"
set hello, {slice {get text}, 0, 5}     # "hello"
set world, {slice {get text}, 6, -1}    # "world"
```

#### `append` - Add item to end (returns new list)
```pawscript
set orig, {list a, b, c}
set new, {append {get orig}, d}

echo {len {get orig}}           # Still 3 (immutable!)
echo {len {get new}}            # Now 4
```

#### `prepend` - Add item to start (returns new list)
```pawscript
set orig, {list a, b, c}
set new, {prepend {get orig}, z}

# new is now [z, a, b, c]
```

#### `concat` - Join two lists (returns new list)
```pawscript
set list1, {list 1, 2, 3}
set list2, {list 4, 5, 6}
set combined, {concat {get list1}, {get list2}}

# combined is [1, 2, 3, 4, 5, 6]
```

#### `compact` - Copy to new backing array
```pawscript
# Create large list
set huge, {list 1, 2, 3, ..., 1000}

# Slice keeps reference to entire backing array
set tiny, {slice {get huge}, 0, 2}

# Free the large backing array
set freed, {compact {get tiny}}

# Now freed has its own small backing array
```

## Usage Patterns

### Functional Style (Recommended)

Since lists are immutable, use functional patterns:

```pawscript
# Build up a list
set items, {list}
set items, {append {get items}, first}
set items, {append {get items}, second}
set items, {append {get items}, third}

# Process list
set numbers, {list 1, 2, 3, 4, 5}
set evens, {slice {get numbers}, 0, 3}      # Take first 3
set more, {append {get evens}, 99}          # Add 99
```

### Iteration with Macros

```pawscript
macro print_item(echo "- $1")

set items, {list apple, banana, cherry}
set count, {len {get items}}

# Manual iteration (until we add foreach)
set i, 1
while {lte {get i}, {get count}} (
    print_item {argv {get items}, {get i}};
    set i, {add {get i}, 1}
)
```

### Working with Data

```pawscript
# Collect data
set readings, {list}
set readings, {append {get readings}, {read_sensor}}
set readings, {append {get readings}, {read_sensor}}
set readings, {append {get readings}, {read_sensor}}

# Process
set count, {len {get readings}}
echo "Collected {get count} readings"

# Access
echo "First: {argv {get readings}, 1}"
echo "Last: {argv {get readings}, {get count}}"
```

### Memory-Efficient Processing

```pawscript
# Load large dataset
set data, {load_big_file}  # Returns PawList with 1M items

# Process in chunks (zero-copy slicing!)
set chunk1, {slice {get data}, 0, 1000}
set chunk2, {slice {get data}, 1000, 2000}
set chunk3, {slice {get data}, 2000, 3000}

# All chunks share the same backing array - no copying!
process_chunk {get chunk1}
process_chunk {get chunk2}
process_chunk {get chunk3}
```

## Type Preservation

Lists preserve the types of their items:

```pawscript
set mixed, {list 42, "text", true, symbol, nil}

echo {get_inferred_type {argv {get mixed}, 1}}  # int
echo {get_inferred_type {argv {get mixed}, 2}}  # string
echo {get_inferred_type {argv {get mixed}, 3}}  # bool
echo {get_inferred_type {argv {get mixed}, 4}}  # symbol
echo {get_inferred_type {argv {get mixed}, 5}}  # nil
```

## Nested Lists and Blocks

Lists can contain other lists and blocks (ParenGroups):

### Nested Lists

```pawscript
# Create nested structure
set matrix, {list {list 1, 2}, {list 3, 4}, {list 5, 6}}

# Access nested elements
set row1, {argv {get matrix}, 1}     # Get first row
echo {argv {get row1}, 1}            # Get first element of first row: 1

# Deep nesting (arbitrary depth)
set deep, {list {list {list innermost}}}
```

### Lists Containing Blocks

```pawscript
# List of code blocks
set commands, {list (echo hello), (set x, 10), (if true then do_thing)}

# Access and potentially execute
set cmd1, {argv {get commands}, 1}   # Get first block: (echo hello)
echo {get_type cmd1}                  # Type: block

# Mixed: lists, blocks, and other types
set data, {list {list a, b}, (code here), 42, "text"}
```

### Use Cases for Nested Structures

**Configuration data:**
```pawscript
set config, {list 
    {list "host", "localhost"},
    {list "port", 8080},
    {list "debug", true}
}
```

**Multi-dimensional data:**
```pawscript
# 2D array / matrix
set grid, {list 
    {list 1, 2, 3},
    {list 4, 5, 6},
    {list 7, 8, 9}
}

# Access grid[2][3] (row 2, col 3)
set row2, {argv {get grid}, 2}
echo {argv {get row2}, 3}  # Output: 6
```

**Action queues:**
```pawscript
# Queue of operations to perform
set tasks, {list}
set tasks, {append {get tasks}, (validate_input)}
set tasks, {append {get tasks}, (process_data)}
set tasks, {append {get tasks}, (save_results)}

# Later: iterate and execute each task
```

## Performance Characteristics

### Zero-Copy Slicing

The biggest performance win is **zero-copy slicing**:

```pawscript
# Create 1 million element list
set huge, {list {range 1, 1000000}}

# Extract small slice - O(1)!
set tiny, {slice {get huge}, 100, 200}

# The slice shares huge's backing array
# Only 24 bytes of overhead (slice header)
# No copying of the 100 elements!
```

### When to Use `compact`

Only use `compact` when:
1. You've sliced a large list down to a small subset
2. You're done with the large list
3. You want to free the large backing array

```pawscript
set huge, {list ...}           # 1M elements
set tiny, {slice {get huge}, 0, 10}  # 10 elements, but references huge's array
set freed, {compact {get tiny}}      # 10 elements with own small array
```

### Append/Prepend Performance

These operations copy the entire list (O(n)):

```pawscript
# Building a large list this way is O(n²)
set items, {list}
set i, 0
while {lt {get i}, 1000} (
    set items, {append {get items}, {get i}}  # O(n) each time
    set i, {add {get i}, 1}
)
```

For building large lists, consider collecting items and creating the list once.

## Integration with Existing Features

### With `argc` and `argv`

Both now work seamlessly with `PawList`:

```pawscript
set mylist, {list a, b, c}
echo {argc {get mylist}}         # 3
echo {argv {get mylist}, 2}      # b
```

### With Macros

```pawscript
macro process_list(
    set items, {list $*};         # All args become list
    set count, {len {get items}};
    echo "Processing {get count} items"
)

process_list apple, banana, cherry
# Output: Processing 3 items
```

### With Type System

```pawscript
if {eq {get_type myvar}, list} then (
    echo "It's a list with {len {get myvar}} items"
) else (
    echo "Not a list"
)
```

## Comparison with Other List-Like Types

| Type | Mutable? | Slicing | Type Name | Use Case |
|------|----------|---------|-----------|----------|
| `PawList` | ❌ | Zero-copy | `list` | Data collections |
| `ParenGroup` | N/A | Parse | `block` | Code storage |
| `[]interface{}` | ✅ | Copy | `unknown` | Internal only |
| `string` | ❌ | Zero-copy | `string` | Text |

## Future Enhancements

Potential additions:

```pawscript
# foreach loop (coming soon!)
foreach item, {get fruits} (
    echo "Fruit: {get item}"
)

# Functional operations (potential)
set doubled, {map {get nums}, double}
set evens, {filter {get nums}, is_even}
set sum, {reduce {get nums}, add, 0}

# Sorting (potential)
set sorted, {sort {get items}}
set reversed, {reverse {get items}}
```

## Migration from ParenGroup

Old pattern (ParenGroup):
```pawscript
set items, (a, b, c)
echo {argc {get items}}           # Works, but parses each time
echo {argv {get items}, 1}        # Works, but parses each time
```

New pattern (PawList):
```pawscript
set items, {list a, b, c}
echo {argc {get items}}           # Direct length, no parsing
echo {argv {get items}, 1}        # Direct access, no parsing
```

Both patterns are supported - use PawList for data, ParenGroup for code.
