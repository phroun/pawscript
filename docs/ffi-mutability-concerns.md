# FFI and Struct Immutability Concerns

## Overview

PawScript structs are immutable - once created, their fields cannot be modified. You must create a new struct with the desired changes. While this design promotes safety and predictability, it creates friction with certain FFI patterns where C code expects mutable memory.

This document outlines the challenges and proposed solutions.

## Challenge 1: Multiple Sequential Modifications

### The C Pattern

```c
SDL_Rect rect;
rect.x = 10;
rect.y = 20;
rect.w = 100;
rect.h = 100;

// Later in the code...
rect.x = 50;  // Just update x
```

### PawScript Equivalent (Verbose)

```pawscript
rect: {struct ~SDL_Rect, x: 10, y: 20, w: 100, h: 100}

# Later - must rebuild entire struct
rect: {struct ~SDL_Rect, x: 50, y: ~rect.y, w: ~rect.w, h: ~rect.h}
```

### Severity: Low

This is verbose but works correctly. The overhead of creating new structs is negligible for most use cases.

### Mitigation: `struct_with` Helper

A copy-with-changes helper reduces boilerplate:

```pawscript
rect: {struct ~SDL_Rect, x: 10, y: 20, w: 100, h: 100}

# Later - concise update
rect: {struct_with ~rect, x: 50}  # Copy all fields, override x

# Multiple field updates
rect: {struct_with ~rect, x: 100, y: 200}
```

## Challenge 2: Out Parameters

### The C Pattern

```c
SDL_Event event;
while (SDL_PollEvent(&event)) {  // Function fills the struct
    printf("Event type: %d\n", event.type);
}
```

### Severity: Solved

The `out:` parameter pattern handles this by passing a struct definition and receiving a newly-minted struct:

```pawscript
(has_event, event): {ffi_call ~sdl, "SDL_PollEvent",
    out: ~SDL_Event,      # Pass definition, not instance
    returns: "int32"
}
# event is a NEW struct created from the filled buffer
```

## Challenge 3: Native Code Holding Pointers

### The Problem

Some APIs store pointers to script-provided data for later use:

```c
// C library stores this pointer internally
void RegisterConfig(Config* cfg) {
    global_config = cfg;  // Stores pointer for later
}

void DoWork() {
    use(global_config->value);  // Reads from stored pointer
}
```

### PawScript Failure Mode

```pawscript
# Create struct
config: {struct ~Config, value: 100}

# Pass to native function that STORES the pointer
ffi_call ~lib, "RegisterConfig", args: (~config)

# "Modify" by creating new struct
config: {struct ~Config, value: 200}

# PROBLEM: Native code still points to OLD struct!
# DoWork() will see value=100, not value=200
```

The native library has a stale pointer. The "new" struct exists at a different memory address.

### Severity: High

This can cause subtle bugs where native code operates on outdated data, or crashes if the old struct's memory is reclaimed.

### Solution: FFI Buffers (see below)

## Challenge 4: Persistent Native Handles

### The C Pattern

Some APIs expect you to maintain a struct that native code reads and writes over time:

```c
MyState state;
LibraryInit(&state);          // Fills initial state

while (running) {
    LibraryUpdate(&state);    // Reads AND writes state
    printf("Count: %d\n", state.count);
}
```

### PawScript Approach

```pawscript
(_, state): {ffi_call ~lib, "LibraryInit", out: ~MyState, returns: "void"}

while ~running, (
    # Each call returns a NEW struct
    (_, state): {ffi_call ~lib, "LibraryUpdate", out: ~MyState, returns: "void"}
    echo "Count:", ~state.count
)
```

### Severity: Medium

This works IF `LibraryUpdate` doesn't need to read prior state from the same pointer it writes to. If it does, see Challenge 5.

## Challenge 5: Bidirectional Struct Access

### The Problem

Some functions both READ from and WRITE to the same struct:

```c
void ProcessData(DataStruct* s) {
    // Reads input from struct
    int input = s->input;

    // Does expensive work
    int result = expensive_calculation(input);

    // Writes output back to same struct
    s->output = result;
}
```

### Why `out:` Fails

The current `out:` pattern allocates a fresh buffer, so the native function can't read existing data:

```pawscript
data: {struct ~DataStruct, input: 42, output: 0}

# PROBLEM: out: creates empty buffer - native function can't read input!
(_, result): {ffi_call ~lib, "ProcessData",
    out: ~DataStruct,
    returns: "void"
}
```

### Severity: Medium-High

This pattern is common in APIs that transform data in-place.

### Solution: `inout:` Parameter

A new parameter type that copies data TO the buffer, calls the function, then creates a struct FROM the buffer:

```pawscript
data: {struct ~DataStruct, input: 42, output: 0}

# inout: copies struct data to buffer, then extracts result
(_, result): {ffi_call ~lib, "ProcessData",
    inout: ~data,         # Existing struct, not definition
    returns: "void"
}
# result.input = 42 (preserved), result.output = <calculated>
```

Semantics:
1. Allocate native buffer sized for struct
2. Copy existing struct's bytes into buffer
3. Call native function with pointer to buffer
4. Create new immutable struct from buffer contents
5. Return the new struct

## Challenge 6: Callback Context Mutation

### The Problem

Callbacks often need to accumulate state across invocations:

```c
typedef void (*Callback)(void* context, int value);

void ForEachItem(Callback cb, void* context) {
    for (int i = 0; i < 10; i++) {
        cb(context, items[i]);
    }
}

// Usage - context struct accumulates results
void my_callback(void* ctx, int value) {
    MyContext* c = (MyContext*)ctx;
    c->sum += value;
    c->count++;
}
```

### PawScript Challenge

With immutable structs, each callback invocation would need to somehow return an updated struct, but callbacks don't work that way - they're called by native code that doesn't know about PawScript's immutability model.

### Severity: High

This fundamentally conflicts with the callback-with-context pattern used throughout C libraries.

### Solution: FFI Buffers (see below)

## Proposed Solution: FFI Buffers

For cases requiring true mutability, introduce a separate `ffi_buffer` type that represents mutable native memory:

### Creating and Using FFI Buffers

```pawscript
# Allocate mutable native memory (NOT a regular struct)
buf: {ffi_buffer ~SDL_Rect}

# Write fields to buffer
ffi_write ~buf, x: 10, y: 20, w: 100, h: 100

# Pass to native code - safe for it to store the pointer
ffi_call ~lib, "RegisterRect", args: (~buf)

# Update in place - native code sees changes immediately
ffi_write ~buf, x: 50

# Read current values from buffer
current_x: {ffi_read ~buf, "x"}
current_y: {ffi_read ~buf, "y"}

# Snapshot to immutable struct (for PawScript-side logic)
rect: {struct_from ~buf}

# Must free explicitly when done
ffi_free ~buf
```

### FFI Buffer Lifecycle

```
┌─────────────────────────────────────────────────────────┐
│                    FFI Buffer Lifecycle                  │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  1. Allocate         buf: {ffi_buffer ~MyStruct}        │
│       │                                                 │
│       ▼                                                 │
│  2. Initialize       ffi_write ~buf, field: value       │
│       │                                                 │
│       ▼                                                 │
│  3. Use with FFI     ffi_call ~lib, "Func", args:(~buf) │
│       │              (native code can hold pointer)     │
│       │                                                 │
│       ├──► Update    ffi_write ~buf, field: new_value   │
│       │              (native code sees changes)         │
│       │                                                 │
│       ├──► Read      val: {ffi_read ~buf, "field"}      │
│       │                                                 │
│       ├──► Snapshot  s: {struct_from ~buf}              │
│       │              (immutable copy for PawScript)     │
│       │                                                 │
│       ▼                                                 │
│  4. Free             ffi_free ~buf                      │
│                      (MUST be called to avoid leak)     │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

### FFI Buffers for Callbacks

```pawscript
# Create mutable context for callback
ctx: {ffi_buffer ~AccumulatorContext}
ffi_write ~ctx, sum: 0, count: 0

# Define callback that updates the buffer
accumulator: {ffi_callback ~lib, "ItemCallback",
    args: (pointer: "context", int32: "value"),
    handler: (context, value, (
        current_sum: {ffi_read ~context, "sum"}
        current_count: {ffi_read ~context, "count"}
        ffi_write ~context,
            sum: {add ~current_sum, ~value},
            count: {add ~current_count, 1}
    ))
}

# Use callback with native code
ffi_call ~lib, "ForEachItem", args: (~accumulator, ~ctx)

# Read accumulated results
total: {ffi_read ~ctx, "sum"}
count: {ffi_read ~ctx, "count"}
echo "Sum:", ~total, "Count:", ~count

# Cleanup
ffi_free ~ctx
```

### Buffer vs Struct Comparison

| Feature | Immutable Struct | FFI Buffer |
|---------|------------------|------------|
| Creation | `{struct ~Def, ...}` | `{ffi_buffer ~Def}` |
| Modify fields | Create new struct | `ffi_write` in place |
| Read fields | `~s.field` | `{ffi_read ~buf, "field"}` |
| Memory location | May move (GC) | Fixed address |
| Native pointer safety | Unsafe to hold | Safe to hold |
| Lifecycle | Automatic | Manual (`ffi_free`) |
| Thread safety | Immutable = safe | Requires care |
| Use in PawScript logic | Natural | Awkward (use snapshot) |

## Command Summary

### New Commands for Mutability Support

| Command | Description |
|---------|-------------|
| `struct_with ~s, field: val` | Create new struct copying `s` with field overrides |
| `ffi_buffer ~Definition` | Allocate mutable native memory for struct type |
| `ffi_write ~buf, field: val` | Write field(s) to buffer in place |
| `ffi_read ~buf, "field"` | Read current field value from buffer |
| `struct_from ~buf` | Create immutable struct snapshot from buffer |
| `ffi_free ~buf` | Deallocate buffer (required to avoid memory leak) |

### Enhanced `ffi_call` Parameters

| Parameter | Description |
|-----------|-------------|
| `out: ~Definition` | Allocate fresh buffer, return new struct (existing) |
| `inout: ~struct` | Copy struct to buffer, call, return new struct (new) |

## Design Philosophy

The separation between immutable structs and mutable FFI buffers is intentional:

1. **Immutable structs** - Default, safe, used for most PawScript code
2. **FFI buffers** - Opt-in, explicit, used only when native code requires mutability

This makes the "danger zone" visible in code. When you see `ffi_buffer`, you know:
- Memory must be manually freed
- Multiple references can see changes
- Thread safety is your responsibility
- The address is stable for native pointers

## Recommendations for Library Bindings

When creating binding libraries, prefer patterns that work with immutable structs:

### Prefer: Functions that return new data

```pawscript
# Good - returns new struct, works naturally with immutability
new_rect: {sdl_rect_move ~rect, dx: 10, dy: 5}
```

### Avoid: Functions that mutate in place (unless necessary)

```pawscript
# Use ffi_buffer only when the native API requires it
buf: {ffi_buffer ~ComplexState}
{native_stateful_operation ~buf}
ffi_free ~buf
```

### Document when buffers are required

```pawscript
# sdl2_bindings.paw

# This function stores the config pointer - MUST use ffi_buffer
# See: ffi-mutability-concerns.md
ffi_command sdl_set_audio_callback, (
    ...
    note: "Requires ffi_buffer for config parameter"
)
```

## Summary

| Scenario | Severity | Solution |
|----------|----------|----------|
| Building struct incrementally | Low | `struct_with` helper |
| Out parameters (function fills struct) | Solved | `out:` parameter |
| Bidirectional read/write | Medium | `inout:` parameter |
| Native code holds pointer | High | `ffi_buffer` |
| Callback context mutation | High | `ffi_buffer` |
| Large mutable data (pixels, audio) | High | `ffi_buffer` |

PawScript's struct immutability works well for most FFI patterns. The `ffi_buffer` escape hatch handles edge cases where true mutability is required, while keeping the dangerous mutable state explicit and contained.
