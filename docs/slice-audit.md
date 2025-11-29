# Slice Command Audit

## Overview

The `slice` command is implemented in `lib_types.go:177-243`. It extracts a portion of a list or string, returning a new value containing elements from the start index up to (but not including) the end index.

## Usage

```paw
# List slicing
mylist: (1, 2, 3, 4, 5)
first_three: {slice ~mylist, 0, 3}    # (1, 2, 3)
from_index_1: {slice ~mylist, 1, -1}  # (2, 3, 4, 5) - negative end means "to end"

# String slicing
greeting: "hello world"
hello: {slice ~greeting, 0, 5}        # "hello"
```

## Implementation Details

### List Slicing

When slicing a list, the command:

1. Calls `StoredList.Slice(start, end)` which creates a new `StoredList`
2. The new list **shares the backing Go array** with the original (O(1) time and space)
3. Named arguments from the original list are preserved (immutable, safe to share)
4. The result is stored via `storeObject` with initial refcount=0
5. Result is set via `SetResultWithoutClaim`

**Backing Array Sharing** (types.go:854-865):
```go
func (pl StoredList) Slice(start, end int) StoredList {
    if start < 0 { start = 0 }
    if end > len(pl.items) { end = len(pl.items) }
    if start > end { start = end }
    return StoredList{items: pl.items[start:end], namedArgs: pl.namedArgs}
}
```

This is memory-efficient: slicing a 1000-element list to get 10 elements doesn't copy 1000 elements. Go's garbage collector correctly handles the shared backing array - it won't be freed until all slices referencing it are freed.

### String Slicing

When slicing a string, the command:

1. Resolves the string value (in case it's a stored string marker)
2. Uses Go's native string slicing `str[start:end]`
3. If the result exceeds `StringStorageThreshold`, it's stored as a `StoredString`
4. Result is set via `SetResultWithoutClaim`

## Reference Counting Flow

```
1. slice command executes
2. New object stored with refcount=0
3. SetResultWithoutClaim sets result (doesn't increment refcount)
4. If assigned to variable: SetVariable claims reference → refcount=1
5. If not assigned: object eligible for cleanup (refcount=0)
```

This is correct behavior - temporary results that aren't captured are automatically cleaned up.

### Example Flow

```paw
original: (1, 2, 3, 4, 5)           # Stored as object ID=1, refcount=1
sliced: {slice ~original, 0, 3}    # Stored as object ID=2, refcount=0 → 1
```

After assignment:
- Object ID=1 (original): refcount=1, items=[1,2,3,4,5]
- Object ID=2 (sliced): refcount=1, items=[1,2,3] (shares backing array with ID=1)

## Storage Behavior

Each `slice` call creates a **new storage entry**, even if the slice parameters are identical:

```paw
original: (1, 2, 3, 4, 5)
a: {slice ~original, 0, 3}   # Creates object ID=X
b: {slice ~original, 0, 3}   # Creates object ID=Y (different ID)
```

Both `a` and `b` have different object IDs but share the same underlying data via Go's slice mechanics. This is intentional - each variable independently tracks its reference.

## Bounds Handling

The slice command handles out-of-bounds indices gracefully:

- Negative start index → clamped to 0
- End index beyond length → clamped to length
- Start > end → returns empty result
- Negative end index (-1) → interpreted as "to end of list/string"

## Path Normalization Note

For strings, the command uses `filepath.Clean` internally when dealing with path-like strings in related file operations, which normalizes `../` sequences. This is handled separately from the slice command itself.

## Performance Characteristics

| Operation | Time | Space |
|-----------|------|-------|
| List slice | O(1) | O(1) - shares backing array |
| String slice | O(n) | O(n) - Go strings are immutable, creates copy |
| Storage overhead | O(1) | Fixed per stored object |

## Related Commands

### compact

The `compact` command (lib_types.go:319-340) is the complement to `slice`. Since sliced lists share the original backing array, `compact` creates a truly independent copy:

```paw
# Large list
big_list: {range 1, 1000000}

# Slice shares backing array - original 1M array can't be freed
small: {slice ~big_list, 0, 10}
big_list: undefined  # Original variable gone, but backing array still held

# Compact creates independent copy - now original array can be freed
small: {compact ~small}
```

**Implementation** (types.go:912-916):
```go
func (pl StoredList) Compact() StoredList {
    newItems := make([]interface{}, len(pl.items))
    copy(newItems, pl.items)
    return StoredList{items: newItems, namedArgs: pl.namedArgs}
}
```

| Command | Time | Space | Backing Array |
|---------|------|-------|---------------|
| slice | O(1) | O(1) | Shared with original |
| compact | O(n) | O(n) | New independent copy |

## Related Functions

- `StoredList.Slice()` - types.go:854-865
- `setListResult()` - lib_types.go:167-171
- `storeObject()` - executor_objects.go:47-65
- `SetResultWithoutClaim()` - state.go:196-215
- `maybeStoreValue()` - executor_objects.go:7-45
