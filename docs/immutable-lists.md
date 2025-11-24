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
fruits: {list apple, banana, cherry}

# From ParenGroup
colors: {list ${set_result (red, green, blue)}}

# Empty list
empty: {list}

# With variables
coords: {list ~x, ~y, ~z}
```

### Querying Lists

#### `len` - Get length of list or string
```pawscript
echo {len ~fruits}        # Output: 3
echo {len "hello"}              # Output: 5 (works on strings too!)
```

#### `get_type` / `get_inferred_type` - Type detection
```pawscript
mylist: {list 1, 2, 3}
echo {get_type mylist}          # Output: list
echo {get_inferred_type ~mylist}  # Output: list
```

#### `argc` - Count items (enhanced)
```pawscript
echo {argc ~fruits}        # Works with PawList
echo {argc (a, b, c)}          # Works with ParenGroup
echo {argc}                    # Script arguments count
```

#### `argv` - Access items (enhanced, 1-indexed)
```pawscript
echo {argv ~fruits, 1}     # First item: apple
echo {argv ~fruits, 2}     # Second item: banana
echo {argv ~fruits}        # All items as []interface{}
```

### Transforming Lists

#### `slice` - Extract sublist (end exclusive, zero-copy!)
```pawscript
nums: {list 1, 2, 3, 4, 5}

# Get first 3 items [0:3]
first: {slice ~nums, 0, 3}     # [1, 2, 3]

# Get items from index 2 to end
tail: {slice ~nums, 2, -1}     # [3, 4, 5]

# Get middle items [1:4]
middle: {slice ~nums, 1, 4}    # [2, 3, 4]

# Slicing shares backing array - O(1) time and space!
```

**String slicing works too:**
```pawscript
text: "hello world"
hello: {slice ~text, 0, 5}     # "hello"
world: {slice ~text, 6, -1}    # "world"
```

#### `append` - Add item to end (returns new list)
```pawscript
orig: {list a, b, c}
new: {append ~orig, d}

echo {len ~orig}           # Still 3 (immutable!)
echo {len ~new}            # Now 4
```

#### `prepend` - Add item to start (returns new list)
```pawscript
orig: {list a, b, c}
new: {prepend ~orig, z}

# new is now [z, a, b, c]
```

#### `concat` - Join two lists (returns new list)
```pawscript
list1: {list 1, 2, 3}
list2: {list 4, 5, 6}
combined: {concat ~list1, ~list2}

# combined is [1, 2, 3, 4, 5, 6]
```

#### `compact` - Copy to new backing array
```pawscript
# Create large list
huge: {list 1, 2, 3, ..., 1000}

# Slice keeps reference to entire backing array
tiny: {slice ~huge, 0, 2}

# Free the large backing array
freed: {compact ~tiny}

# Now freed has its own small backing array
```

## Usage Patterns

### Functional Style (Recommended)

Since lists are immutable, use functional patterns:

```pawscript
# Build up a list
items: {list}
items: {append ~items, first}
items: {append ~items, second}
items: {append ~items, third}

# Process list
numbers: {list 1, 2, 3, 4, 5}
evens: {slice ~numbers, 0, 3}      # Take first 3
more: {append ~evens, 99}          # Add 99
```

### Iteration with Macros

```pawscript
macro print_item(echo "- $1")

items: {list apple, banana, cherry}
count: {len ~items}

# Manual iteration (until we add foreach)
i: 1
while {lte ~i, ~count} (
    print_item {argv ~items, ~i};
    i: {add ~i, 1}
)
```

### Working with Data

```pawscript
# Collect data
readings: {list}
readings: {append ~readings, {read_sensor}}
readings: {append ~readings, {read_sensor}}
readings: {append ~readings, {read_sensor}}

# Process
count: {len ~readings}
echo "Collected ~count readings"

# Access
echo "First: {argv ~readings, 1}"
echo "Last: {argv ~readings, ~count}"
```

### Memory-Efficient Processing

```pawscript
# Load large dataset
data: {load_big_file}  # Returns PawList with 1M items

# Process in chunks (zero-copy slicing!)
chunk1: {slice ~data, 0, 1000}
chunk2: {slice ~data, 1000, 2000}
chunk3: {slice ~data, 2000, 3000}

# All chunks share the same backing array - no copying!
process_chunk ~chunk1
process_chunk ~chunk2
process_chunk ~chunk3
```

## Type Preservation

Lists preserve the types of their items:

```pawscript
mixed: {list 42, "text", true, symbol, nil}

echo {get_inferred_type {argv ~mixed, 1}}  # int
echo {get_inferred_type {argv ~mixed, 2}}  # string
echo {get_inferred_type {argv ~mixed, 3}}  # bool
echo {get_inferred_type {argv ~mixed, 4}}  # symbol
echo {get_inferred_type {argv ~mixed, 5}}  # nil
```

## Nested Lists and Blocks

Lists can contain other lists and blocks (ParenGroups):

### Nested Lists

```pawscript
# Create nested structure
matrix: {list {list 1, 2}, {list 3, 4}, {list 5, 6}}

# Access nested elements
row1: {argv ~matrix, 1}     # Get first row
echo {argv ~row1, 1}            # Get first element of first row: 1

# Deep nesting (arbitrary depth)
deep: {list {list {list innermost}}}
```

### Lists Containing Blocks

```pawscript
# List of code blocks
commands: {list (echo hello), (x: 10), (if true then do_thing)}

# Access and potentially execute
cmd1: {argv ~commands, 1}   # Get first block: (echo hello)
echo {get_type cmd1}                  # Type: block

# Mixed: lists, blocks, and other types
data: {list {list a, b}, (code here), 42, "text"}
```

### Use Cases for Nested Structures

**Configuration data:**
```pawscript
config: {list 
    {list "host", "localhost"},
    {list "port", 8080},
    {list "debug", true}
}
```

**Multi-dimensional data:**
```pawscript
# 2D array / matrix
grid: {list 
    {list 1, 2, 3},
    {list 4, 5, 6},
    {list 7, 8, 9}
}

# Access grid[2][3] (row 2, col 3)
row2: {argv ~grid, 2}
echo {argv ~row2, 3}  # Output: 6
```

**Action queues:**
```pawscript
# Queue of operations to perform
tasks: {list}
tasks: {append ~tasks, (validate_input)}
tasks: {append ~tasks, (process_data)}
tasks: {append ~tasks, (save_results)}

# Later: iterate and execute each task
```

## Performance Characteristics

### Zero-Copy Slicing

The biggest performance win is **zero-copy slicing**:

```pawscript
# Create 1 million element list
huge: {list {range 1, 1000000}}

# Extract small slice - O(1)!
tiny: {slice ~huge, 100, 200}

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
huge: {list ...}           # 1M elements
tiny: {slice ~huge, 0, 10}  # 10 elements, but references huge's array
freed: {compact ~tiny}      # 10 elements with own small array
```

### Append/Prepend Performance

These operations copy the entire list (O(n)):

```pawscript
# Building a large list this way is O(n²)
items: {list}
i: 0
while {lt ~i, 1000} (
    items: {append ~items, ~i}  # O(n) each time
    i: {add ~i, 1}
)
```

For building large lists, consider collecting items and creating the list once.

## Integration with Existing Features

### With `argc` and `argv`

Both now work seamlessly with `PawList`:

```pawscript
mylist: {list a, b, c}
echo {argc ~mylist}         # 3
echo {argv ~mylist, 2}      # b
```

### With Macros

```pawscript
macro process_list(
    items: {list $*};         # All args become list
    count: {len ~items};
    echo "Processing ~count items"
)

process_list apple, banana, cherry
# Output: Processing 3 items
```

### With Type System

```pawscript
if {eq {get_type myvar}, list} then (
    echo "It's a list with {len ~myvar} items"
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
foreach item, ~fruits (
    echo "Fruit: ~item"
)

# Functional operations (potential)
doubled: {map ~nums, double}
evens: {filter ~nums, is_even}
sum: {reduce ~nums, add, 0}

# Sorting (potential)
sorted: {sort ~items}
reversed: {reverse ~items}
```

## Migration from ParenGroup

Old pattern (ParenGroup):
```pawscript
items: (a, b, c)
echo {argc ~items}           # Works, but parses each time
echo {argv ~items, 1}        # Works, but parses each time
```

New pattern (PawList):
```pawscript
items: {list a, b, c}
echo {argc ~items}           # Direct length, no parsing
echo {argv ~items, 1}        # Direct access, no parsing
```

Both patterns are supported - use PawList for data, ParenGroup for code.
