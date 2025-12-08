# PawScript FFI (Foreign Function Interface) Plan

## Overview

This document outlines a proposed design for FFI (Foreign Function Interface) capability in PawScript, allowing scripts to call functions in native libraries (.dll/.dylib/.so). This leverages the existing Struct system for argument/return marshaling.

## Design Goals

1. **Leverage existing Struct system** - Use struct definitions for marshaling complex types
2. **Host-controlled security** - Only whitelisted libraries can be loaded
3. **Thread affinity support** - Some libraries require main thread (GUI, graphics)
4. **Cross-platform** - Windows, macOS, Linux support
5. **No CGO requirement** - Use pure Go FFI where possible

## Script-Side API

### Loading Libraries

```pawscript
# Load a library (must be whitelisted by host)
sdl: {ffi_load "SDL2"}
gl: {ffi_load "opengl32"}

# Check if library is available
if {ffi_available "SDL2"} then (
    sdl: {ffi_load "SDL2"}
)
```

### Defining Structs for FFI

The existing struct system already supports what FFI needs:

```pawscript
# Define structs for arguments/returns
SDL_Rect: {struct_def
    x: (0, 4, "int_le"),
    y: (4, 4, "int_le"),
    w: (8, 4, "int_le"),
    h: (12, 4, "int_le")
}

SDL_Color: {struct_def
    r: (0, 1, "uint8"),
    g: (1, 1, "uint8"),
    b: (2, 1, "uint8"),
    a: (3, 1, "uint8")
}

SDL_Event: {struct_def
    type: (0, 4, "uint_le"),
    padding: (4, 52, "bytes")  # Union - interpret based on type
}
```

### Calling Functions

```pawscript
# Simple call - scalar args and return
result: {ffi_call ~sdl, "SDL_Init",
    args: (0x00000020),        # SDL_INIT_VIDEO
    returns: "int32"
}

# Call with struct argument
rect: {struct ~SDL_Rect, x: 100, y: 50, w: 640, h: 480}
ffi_call ~sdl, "SDL_RenderFillRect",
    args: (~renderer, ~rect),
    returns: "int32"

# Call returning a pointer (opaque handle)
window: {ffi_call ~sdl, "SDL_CreateWindow",
    args: ("My Game", 100, 100, 800, 600, 0),
    returns: "pointer",
    thread: "main"    # Must run on main thread
}

# Call with explicit calling convention
result: {ffi_call ~user32, "MessageBoxW",
    args: (0, "Hello", "Title", 0),
    returns: "int32",
    convention: "stdcall"
}
```

### Out Parameters (Structs filled by callee)

Since PawScript structs are immutable, out parameters work by passing the struct
**definition** and receiving a newly-minted struct back:

```pawscript
# Call that fills a struct (out parameter)
# Pass the struct DEFINITION, get back (return_value, new_struct)
(has_event, event): {ffi_call ~sdl, "SDL_PollEvent",
    out: ~SDL_Event,          # Pass the definition, not an instance
    returns: "int32"
}
# event is a NEW struct created from the buffer after the call
if ~has_event then (
    echo "Event type:", ~event.type
)

# Multiple out parameters
(result, info, extra): {ffi_call ~lib, "SomeFunction",
    args: (123),
    out: (~InfoStruct, ~ExtraStruct),   # Multiple out structs
    returns: "int32"
}
```

The FFI system:
1. Allocates temporary buffer(s) for out parameters
2. Passes pointer(s) to the native function
3. After the call, creates new immutable struct(s) from the buffer(s)
4. Returns a tuple of (return_value, out_struct1, out_struct2, ...)

### String Handling

```pawscript
# Strings are passed as null-terminated pointers
title: "My Window Title"
window: {ffi_call ~sdl, "SDL_CreateWindow",
    args: (~title, 100, 100, 800, 600, 0),
    returns: "pointer"
}

# Getting strings back
error_msg: {ffi_call ~sdl, "SDL_GetError",
    args: (),
    returns: "string"    # Reads null-terminated string from returned pointer
}
```

## Supported Types

### Primitive Types for Args/Returns

| Type | Size | Description |
|------|------|-------------|
| `int8` | 1 | Signed 8-bit integer |
| `int16` | 2 | Signed 16-bit integer |
| `int32` | 4 | Signed 32-bit integer |
| `int64` | 8 | Signed 64-bit integer |
| `uint8` | 1 | Unsigned 8-bit integer |
| `uint16` | 2 | Unsigned 16-bit integer |
| `uint32` | 4 | Unsigned 32-bit integer |
| `uint64` | 8 | Unsigned 64-bit integer |
| `float32` | 4 | IEEE 754 single precision |
| `float64` | 8 | IEEE 754 double precision |
| `pointer` | 4/8 | Opaque pointer (platform-sized) |
| `string` | 4/8 | Null-terminated string pointer |
| `void` | 0 | No return value |
| `struct` | varies | Pass struct by value (copies bytes) |

### Calling Conventions

| Convention | Description | Platform |
|------------|-------------|----------|
| `cdecl` | C default, caller cleans stack | All (default) |
| `stdcall` | Callee cleans stack | Windows API |
| `fastcall` | Register-based where possible | Windows |

### Thread Affinity Options

| Option | Description |
|--------|-------------|
| `"any"` | Run on any thread (default) |
| `"main"` | Run on main application thread |
| `"pool:X"` | Run on dedicated thread pool X |

## Union Handling

Since C unions share memory, use struct overlays to interpret the same bytes differently:

```pawscript
# SDL_Event is a union - same memory, different interpretations
SDL_Event_base: {struct_def
    type: (0, 4, "uint_le")
}

SDL_KeyboardEvent: {struct_def
    type:      (0, 4, "uint_le"),
    timestamp: (4, 4, "uint_le"),
    windowID:  (8, 4, "uint_le"),
    state:     (12, 1, "uint8"),
    repeat:    (13, 1, "uint8"),
    padding:   (14, 2, "bytes"),
    scancode:  (16, 4, "int_le"),
    keycode:   (20, 4, "int_le"),
    mod:       (24, 2, "uint_le")
}

SDL_MouseMotionEvent: {struct_def
    type:      (0, 4, "uint_le"),
    timestamp: (4, 4, "uint_le"),
    windowID:  (8, 4, "uint_le"),
    which:     (12, 4, "uint_le"),
    state:     (16, 4, "uint_le"),
    x:         (20, 4, "int_le"),
    y:         (24, 4, "int_le"),
    xrel:      (28, 4, "int_le"),
    yrel:      (32, 4, "int_le")
}

# Poll event and interpret based on type
# Use out: to get a new struct back (immutable pattern)
(has_event, event_bytes): {ffi_call ~sdl, "SDL_PollEvent",
    out: ~SDL_Event_base,     # Get raw event as base struct
    returns: "int32"
}

if ~has_event then (
    # Check type field from the returned struct
    if {eq ~event_bytes.type, 768} then (  # SDL_KEYDOWN
        # Reinterpret the same bytes as keyboard event
        key_event: {struct ~SDL_KeyboardEvent, from: ~event_bytes}
        echo "Key pressed:", ~key_event.keycode
    ) else if {eq ~event_bytes.type, 1024} then (  # SDL_MOUSEMOTION
        # Reinterpret as mouse event
        mouse_event: {struct ~SDL_MouseMotionEvent, from: ~event_bytes}
        echo "Mouse:", ~mouse_event.x, ~mouse_event.y
    )
)
```

## Callback Support (Advanced)

For libraries that require callbacks (audio, event handlers):

```pawscript
# Define a callback
my_audio_callback: {ffi_callback ~sdl, "SDL_AudioCallback",
    signature: ("pointer", "pointer", "int32"),
    handler: (userdata, stream, len, (
        # Fill audio buffer
        # stream is a pointer to bytes we can write
    ))
}

# Pass callback to library
audio_spec: {struct ~SDL_AudioSpec,
    freq: 44100,
    format: 0x8010,  # AUDIO_S16LSB
    channels: 2,
    samples: 4096,
    callback: ~my_audio_callback
}
```

## Host-Side Configuration (Go)

### FFI Configuration

```go
ps.SetFFIConfig(&pawscript.FFIConfig{
    // Whitelist allowed libraries
    AllowedLibraries: map[string]FFILibraryConfig{
        "SDL2": {
            Paths: []string{
                "SDL2.dll",           // Windows
                "libSDL2.dylib",      // macOS
                "libSDL2.so",         // Linux
                "libSDL2-2.0.so.0",   // Linux alternate
            },
            ThreadAffinity: "main",   // All SDL calls on main thread
            Convention:     "cdecl",
        },
        "SDL2_image": {
            Paths: []string{
                "SDL2_image.dll",
                "libSDL2_image.dylib",
                "libSDL2_image.so",
            },
            ThreadAffinity: "main",
            Convention:     "cdecl",
        },
        "opengl32": {
            Paths: []string{
                "opengl32.dll",
                "libGL.so",
                "libGL.so.1",
                "/System/Library/Frameworks/OpenGL.framework/OpenGL",
            },
            ThreadAffinity: "main",
            Convention:     "cdecl",
        },
        "user32": {
            Paths: []string{"user32.dll"},
            Convention:     "stdcall",
            ThreadAffinity: "main",
        },
    },

    // Global restrictions
    MaxStructSize:     64 * 1024,  // 64KB max struct
    AllowCallbacks:    true,
    MaxCallbackDepth:  8,
    DefaultConvention: "cdecl",
})
```

### Thread Affinity Implementation

```go
// Main thread dispatcher pattern (in host application's main.go)
func main() {
    runtime.LockOSThread()  // Lock main goroutine to main OS thread

    ps := pawscript.New(config)
    ps.SetFFIConfig(ffiConfig)

    // Start script execution in separate goroutine
    go func() {
        ps.ExecuteFile("game.paw")
        ps.SignalDone()
    }()

    // Main thread event loop - processes FFI calls requiring main thread
    for {
        select {
        case call := <-ps.MainThreadCalls():
            result := call.Execute()
            call.Complete(result)
        case <-ps.Done():
            return
        }
    }
}
```

### Dedicated Thread Pools

```go
// Register a dedicated thread for specific library or purpose
ps.RegisterFFIThread("audio", func(calls <-chan *FFICall) {
    runtime.LockOSThread()

    // Optional: Initialize thread-local state
    // e.g., audio context initialization

    for call := range calls {
        result := call.Execute()
        call.Complete(result)
    }
})
```

## Security Model

### Access Levels

| Level | FFI Access |
|-------|------------|
| `--sandbox` (default) | None - FFI completely disabled |
| `--allow-ffi=SDL2` | Specific library only |
| `--allow-ffi=SDL2,opengl32` | Multiple specific libraries |
| `--unrestricted` | All host-whitelisted libraries |

### Security Principles

1. **Host whitelist is absolute** - Scripts can never load libraries not in host config
2. **Path resolution is host-controlled** - Scripts use logical names, host maps to actual paths
3. **No arbitrary code execution** - Only exported functions can be called
4. **Memory isolation** - Structs are copied, not shared directly
5. **Thread control** - Host decides thread affinity requirements

## Go Implementation Notes

### Pure Go Option (Recommended)

Use `github.com/ebitengine/purego`:

```go
import "github.com/ebitengine/purego"

// Load library
lib, err := purego.Dlopen("libSDL2.so", purego.RTLD_NOW|purego.RTLD_GLOBAL)

// Get function
var SDL_Init func(flags uint32) int32
purego.RegisterLibFunc(&SDL_Init, lib, "SDL_Init")

// Call it
result := SDL_Init(0x20)
```

Benefits:
- No CGO required
- Cross-compilation works
- Used by Ebitengine (production game engine)
- Supports Windows, macOS, Linux
- Handles cdecl and stdcall

### With CGO Option

For cases where CGO is acceptable:

```go
/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
*/
import "C"

// Use dlopen/dlsym directly
```

## Complete Example: SDL2 Game Loop

```pawscript
#!/usr/bin/env pawscript
# Simple SDL2 game using FFI

# Load SDL2
sdl: {ffi_load "SDL2"}

# Constants
SDL_INIT_VIDEO: 0x00000020
SDL_QUIT: 256
SDL_KEYDOWN: 768

# Initialize SDL
result: {ffi_call ~sdl, "SDL_Init", args: (~SDL_INIT_VIDEO), returns: "int32"}
if {lt ~result, 0} then (
    echo "SDL_Init failed:", {ffi_call ~sdl, "SDL_GetError", args: (), returns: "string"}
    ret status: false
)

# Create window
window: {ffi_call ~sdl, "SDL_CreateWindow",
    args: ("PawScript Game", 100, 100, 800, 600, 0),
    returns: "pointer",
    thread: "main"
}

# Create renderer
renderer: {ffi_call ~sdl, "SDL_CreateRenderer",
    args: (~window, -1, 2),  # -1 = default driver, 2 = accelerated
    returns: "pointer",
    thread: "main"
}

# Event handling struct definition
SDL_Event: {struct_def
    type: (0, 4, "uint_le"),
    padding: (4, 52, "bytes")
}

# Rectangle for rendering
SDL_Rect: {struct_def
    x: (0, 4, "int_le"),
    y: (4, 4, "int_le"),
    w: (8, 4, "int_le"),
    h: (12, 4, "int_le")
}

# Game state
player_x: 400
player_y: 300
running: true

echo "Starting game loop..."

# Main game loop
while ~running, (
    # Poll events - out: returns a NEW struct each call (immutable pattern)
    (has_event, event): {ffi_call ~sdl, "SDL_PollEvent",
        out: ~SDL_Event,
        returns: "int32"
    }
    while ~has_event, (
        if {eq ~event.type, ~SDL_QUIT} then (
            running: false
        )
        # Poll next event
        (has_event, event): {ffi_call ~sdl, "SDL_PollEvent",
            out: ~SDL_Event,
            returns: "int32"
        }
    )

    # Clear screen (black)
    ffi_call ~sdl, "SDL_SetRenderDrawColor",
        args: (~renderer, 0, 0, 0, 255),
        returns: "int32"
    ffi_call ~sdl, "SDL_RenderClear",
        args: (~renderer),
        returns: "int32"

    # Draw player (white rectangle)
    ffi_call ~sdl, "SDL_SetRenderDrawColor",
        args: (~renderer, 255, 255, 255, 255),
        returns: "int32"

    player_rect: {struct ~SDL_Rect,
        x: ~player_x,
        y: ~player_y,
        w: 50,
        h: 50
    }
    ffi_call ~sdl, "SDL_RenderFillRect",
        args: (~renderer, ~player_rect),
        returns: "int32"

    # Present
    ffi_call ~sdl, "SDL_RenderPresent",
        args: (~renderer),
        returns: "void",
        thread: "main"

    # Cap at ~60 FPS
    msleep 16
)

# Cleanup
echo "Cleaning up..."
ffi_call ~sdl, "SDL_DestroyRenderer", args: (~renderer), returns: "void"
ffi_call ~sdl, "SDL_DestroyWindow", args: (~window), returns: "void"
ffi_call ~sdl, "SDL_Quit", args: (), returns: "void"

echo "Done!"
```

## Proposed Command Set

| Command | Description |
|---------|-------------|
| `ffi_load name` | Load a whitelisted library, returns handle |
| `ffi_available name` | Check if library is whitelisted and loadable |
| `ffi_call lib, func, ...` | Call a function |
| `ffi_callback lib, name, ...` | Create a callback for the library to call |
| `ffi_symbol lib, name` | Get raw pointer to symbol (advanced) |
| `ffi_close lib` | Explicitly unload library |

## Named Arguments for ffi_call

| Argument | Type | Description |
|----------|------|-------------|
| `args:` | tuple | Function arguments |
| `returns:` | string | Return type |
| `convention:` | string | Calling convention (cdecl, stdcall, fastcall) |
| `thread:` | string | Thread affinity (any, main, pool:X) |

## Future Considerations

### Struct Packing

May need to add alignment/packing options:

```pawscript
MyStruct: {struct_def
    a: (0, 1, "uint8"),
    b: (4, 4, "int_le"),   # Aligned to 4 bytes
    pack: 4                 # Struct packing/alignment
}
```

### Array Support

Arrays of primitives in structs:

```pawscript
Vector3Array: {struct_def
    count: (0, 4, "int_le"),
    data: (4, 12, "float_le[3]")  # Array of 3 floats
}
```

### Automatic Bindings Generation

Tool to generate PawScript struct definitions from C headers:

```bash
paw-bindgen SDL.h > sdl_bindings.paw
```

## Native-Feel Bindings

### The Problem

Raw FFI calls work but are verbose and require intimate knowledge of library details:

```pawscript
# Raw FFI - verbose and OS-specific details exposed
sdl: {ffi_load "SDL2"}
result: {ffi_call ~sdl, "SDL_Init", args: (0x20), returns: "int32"}
window: {ffi_call ~sdl, "SDL_CreateWindow",
    args: ("Title", 100, 100, 800, 600, 0),
    returns: "pointer",
    thread: "main"
}
```

### The Solution: Bindings

Bindings let FFI calls feel like native PawScript commands:

```pawscript
# With bindings - clean, native feel
result: {sdl_init 0x20}
window: {sdl_create_window "Title", 100, 100, 800, 600, 0}
```

### Host-Side Binding Registration (Go)

Hosts can register bindings that map PawScript commands to FFI calls:

```go
ps.RegisterFFIBinding(&pawscript.FFIBinding{
    Command: "sdl_init",
    Library: pawscript.LibraryPaths{
        Windows: "SDL2.dll",
        Darwin:  "libSDL2.dylib",
        Linux:   "libSDL2.so",
    },
    Symbol:     "SDL_Init",
    Args:       []string{"uint32"},
    Returns:    "int32",
    Thread:     "main",
    Convention: "cdecl",
})

ps.RegisterFFIBinding(&pawscript.FFIBinding{
    Command: "sdl_create_window",
    Library: pawscript.LibraryPaths{
        Windows: "SDL2.dll",
        Darwin:  "libSDL2.dylib",
        Linux:   "libSDL2.so",
    },
    Symbol:  "SDL_CreateWindow",
    Args:    []string{"string", "int32", "int32", "int32", "int32", "uint32"},
    Returns: "pointer",
    Thread:  "main",
})
```

### PawScript-Side Binding Definitions

Bindings can also be defined in PawScript itself using `ffi_command`:

```pawscript
# Define a cross-platform binding
ffi_command sdl_init, (
    windows: ("SDL2.dll", "SDL_Init"),
    darwin:  ("libSDL2.dylib", "SDL_Init"),
    linux:   ("libSDL2.so", "SDL_Init"),
    args:    (uint32: "flags"),
    returns: "int32",
    thread:  "main"
)

ffi_command sdl_create_window, (
    windows: ("SDL2.dll", "SDL_CreateWindow"),
    darwin:  ("libSDL2.dylib", "SDL_CreateWindow"),
    linux:   ("libSDL2.so", "SDL_CreateWindow"),
    args:    (string: "title", int32: "x", int32: "y", int32: "w", int32: "h", uint32: "flags"),
    returns: "pointer",
    thread:  "main"
)

ffi_command sdl_poll_event, (
    windows: ("SDL2.dll", "SDL_PollEvent"),
    darwin:  ("libSDL2.dylib", "SDL_PollEvent"),
    linux:   ("libSDL2.so", "SDL_PollEvent"),
    args:    (),
    out:     ~SDL_Event,
    returns: "int32"
)

# Now use them naturally
result: {sdl_init 0x20}
window: {sdl_create_window "My Game", 100, 100, 800, 600, 0}
(has_event, event): {sdl_poll_event}
```

### Binding Libraries

Collections of bindings can be packaged as `.paw` files and imported:

```pawscript
# Import binding library
import "sdl2_bindings.paw"

# All SDL2 commands now available as native commands
{sdl_init SDL_INIT_VIDEO}
window: {sdl_create_window "Game", 100, 100, 800, 600, 0}
renderer: {sdl_create_renderer ~window, -1, SDL_RENDERER_ACCELERATED}

# Event loop
while ~running, (
    (has_event, event): {sdl_poll_event}
    if ~has_event then (
        if {eq ~event.type, SDL_QUIT} then (
            running: false
        )
    )

    {sdl_render_clear ~renderer}
    {sdl_render_present ~renderer}
)

{sdl_quit}
```

### Standard Binding Libraries

The host application or PawScript distribution could include pre-built bindings for common libraries:

| Library | Binding File | Commands |
|---------|--------------|----------|
| SDL2 | `sdl2_bindings.paw` | `sdl_init`, `sdl_quit`, `sdl_create_window`, ... |
| SDL2_image | `sdl2_image_bindings.paw` | `img_load`, `img_save_png`, ... |
| SDL2_mixer | `sdl2_mixer_bindings.paw` | `mix_open_audio`, `mix_play_channel`, ... |
| SDL2_ttf | `sdl2_ttf_bindings.paw` | `ttf_open_font`, `ttf_render_text`, ... |
| OpenGL | `opengl_bindings.paw` | `gl_clear`, `gl_draw_arrays`, ... |
| GLFW | `glfw_bindings.paw` | `glfw_init`, `glfw_create_window`, ... |

### Handling Platform Differences

For APIs that differ between platforms (not just library paths), use platform-specific overrides:

```pawscript
# Windows-specific MessageBox
ffi_command message_box, (
    windows: ("user32.dll", "MessageBoxW"),
    args:    (pointer: "hwnd", string: "text", string: "caption", uint32: "type"),
    returns: "int32",
    convention: "stdcall"  # Windows calling convention
)

# macOS-specific notification (Cocoa via FFI bridge)
ffi_command show_notification, (
    darwin: ("libnotify_bridge.dylib", "show_notification"),
    args:   (string: "title", string: "message"),
    returns: "void"
)

# Cross-platform fallback pattern
if {os} eq "windows" then (
    {message_box 0, "Hello", "Title", 0}
) else if {os} eq "darwin" then (
    {show_notification "Title", "Hello"}
) else (
    echo "Hello"  # Linux fallback
)
```

### Struct Definitions in Binding Libraries

Binding libraries can include struct definitions alongside commands:

```pawscript
# sdl2_bindings.paw

# Struct definitions
SDL_Rect: {struct_def
    x: (0, 4, "int_le"),
    y: (4, 4, "int_le"),
    w: (8, 4, "int_le"),
    h: (12, 4, "int_le")
}

SDL_Event: {struct_def
    type: (0, 4, "uint_le"),
    padding: (4, 52, "bytes")
}

SDL_KeyboardEvent: {struct_def
    type:      (0, 4, "uint_le"),
    timestamp: (4, 4, "uint_le"),
    windowID:  (8, 4, "uint_le"),
    state:     (12, 1, "uint8"),
    repeat:    (13, 1, "uint8"),
    scancode:  (16, 4, "int_le"),
    keycode:   (20, 4, "int_le"),
    mod:       (24, 2, "uint_le")
}

# Constants
SDL_INIT_VIDEO:    0x00000020
SDL_INIT_AUDIO:    0x00000010
SDL_QUIT:          256
SDL_KEYDOWN:       768
SDL_KEYUP:         769
SDL_MOUSEMOTION:   1024
SDL_MOUSEBUTTONDOWN: 1025

# Command bindings
ffi_command sdl_init, (
    windows: ("SDL2.dll", "SDL_Init"),
    darwin:  ("libSDL2.dylib", "SDL_Init"),
    linux:   ("libSDL2.so", "SDL_Init"),
    args:    (uint32: "flags"),
    returns: "int32",
    thread:  "main"
)

# ... more bindings ...
```

### Security with Bindings

Bindings respect the same security model as raw FFI:

1. **Host whitelist still applies** - `ffi_command` can only create bindings for whitelisted libraries
2. **Imported binding files are validated** - Bindings can only reference allowed libraries
3. **No privilege escalation** - A binding can't grant access the host didn't provide

```go
// Host configuration determines what bindings can actually work
ps.SetFFIConfig(&pawscript.FFIConfig{
    AllowedLibraries: map[string]FFILibraryConfig{
        "SDL2": {...},  // Only SDL2 allowed
        // OpenGL NOT in list - any opengl bindings will fail to load
    },
})
```

## Summary

This FFI design:

1. **Leverages existing infrastructure** - Struct system handles all marshaling
2. **Maintains security** - Host controls all library access
3. **Supports real-world use** - Thread affinity for GUI/graphics libraries
4. **Enables game development** - SDL2, OpenGL, audio libraries all accessible
5. **Stays cross-platform** - Pure Go implementation possible with purego
6. **Provides native feel** - Binding system makes FFI calls feel like built-in commands

The existing Struct system with its endianness support, bit fields, and union-via-overlay capability provides exactly the foundation needed for robust FFI. The binding system then builds on this to provide a clean, native-feeling API that abstracts away platform differences.
