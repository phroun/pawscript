# Reference Counting System Refactor Plan

## Overview

This document outlines the plan to refactor PawScript's reference counting system for stored objects. The goal is to create a clean, centralized, and reliable ownership model.

## Current Problems

1. **Marker strings as internal references**: `\x00LIST:5\x00` format was meant for code substitution, not internal object passing
2. **findStoredXxxID functions**: Unreliable identity lookup (fails for empty lists, lists with only namedArgs)
3. **Scattered ownership code**: Claim/release calls spread throughout codebase
4. **Inconsistent handling**: Different object types handled differently

## New Architecture

### 1. ObjectRef Type

```go
type ObjectType int

const (
    ObjList ObjectType = iota
    ObjString
    ObjBlock
    ObjBytes
    ObjChannel
    ObjFiber
    ObjCommand
    ObjMacro
    ObjStruct
    ObjStructArray
    ObjFile
)

// ObjectRef is the internal representation of a reference to a stored object
type ObjectRef struct {
    Type ObjectType
    ID   int
}

// ToMarker converts to string form ONLY for code substitution
func (ref ObjectRef) ToMarker() string {
    return fmt.Sprintf("\x00%s:%d\x00", ref.Type.String(), ref.ID)
}
```

### 2. Object Registry

```go
type StoredObject struct {
    Value    interface{}  // The actual object
    Type     ObjectType
    RefCount int
    Deleted  bool         // Marked for reuse when true
}

type Executor struct {
    // ...
    objectRegistry map[int]*StoredObject
    freeIDs        []int   // Recycled IDs from deleted objects
    nextID         int
    // ...
}
```

**ID Reuse**: When an object is GC'd (refcount→0), its ID is added to `freeIDs`. New objects first check `freeIDs` before incrementing `nextID`.

### 3. Central Ownership Functions

ALL refcount changes go through these functions:

```go
// RefClaim increments refcount for an object
// Safe to call with zero-value ObjectRef (no-op)
func (e *Executor) RefClaim(ref ObjectRef)

// RefRelease decrements refcount
// When refcount reaches 0: releases nested refs, then marks object for reuse
func (e *Executor) RefRelease(ref ObjectRef)

// RegisterObject stores a new object and returns its ObjectRef
// The object is stored with refcount=0 (caller must claim)
func (e *Executor) RegisterObject(value interface{}, objType ObjectType) ObjectRef
```

### 4. Object Constructors

Each stored object type has a constructor that:
1. Registers the object (gets ID)
2. Claims references to any ObjectRefs in its contents
3. Returns the new ObjectRef

```go
// Example for lists
func (e *Executor) NewList(items []interface{}, namedArgs map[string]interface{}) ObjectRef {
    list := StoredList{items: items, namedArgs: namedArgs}
    ref := e.RegisterObject(list, ObjList)

    // Claim refs for any ObjectRefs in contents
    for _, item := range items {
        if objRef, ok := item.(ObjectRef); ok {
            e.RefClaim(objRef)
        }
    }
    for _, val := range namedArgs {
        if objRef, ok := val.(ObjectRef); ok {
            e.RefClaim(objRef)
        }
    }
    // Keys could also be ObjectRefs in future

    return ref
}
```

### 5. Release Cascading

When `RefRelease` causes refcount to reach 0:

```go
func (e *Executor) RefRelease(ref ObjectRef) {
    obj := e.objectRegistry[ref.ID]
    obj.RefCount--

    if obj.RefCount <= 0 {
        // Release nested references based on object type
        switch ref.Type {
        case ObjList:
            list := obj.Value.(StoredList)
            for _, item := range list.items {
                if objRef, ok := item.(ObjectRef); ok {
                    e.RefRelease(objRef)
                }
            }
            for _, val := range list.namedArgs {
                if objRef, ok := val.(ObjectRef); ok {
                    e.RefRelease(objRef)
                }
            }
        case ObjChannel:
            // Release any buffered items
            ch := obj.Value.(*StoredChannel)
            // ... release buffered ObjectRefs
        // ... other types
        }

        // Mark for ID reuse
        obj.Deleted = true
        obj.Value = nil
        e.freeIDs = append(e.freeIDs, ref.ID)
    }
}
```

### 6. Ownership Points

#### 6.1 ExecutionState Variables

```go
func (s *ExecutionState) SetVar(name string, value interface{}) {
    oldValue := s.variables[name]
    s.variables[name] = value

    // Claim new FIRST
    if objRef, ok := value.(ObjectRef); ok {
        s.executor.RefClaim(objRef)
    }

    // Then release old
    if objRef, ok := oldValue.(ObjectRef); ok {
        s.executor.RefRelease(objRef)
    }
}
```

#### 6.2 ExecutionState Result

```go
func (s *ExecutionState) SetResult(value interface{}) {
    oldResult := s.currentResult
    s.currentResult = value
    s.hasResult = true

    // Claim new FIRST
    if objRef, ok := value.(ObjectRef); ok {
        s.executor.RefClaim(objRef)
    }

    // Then release old
    if objRef, ok := oldResult.(ObjectRef); ok {
        s.executor.RefRelease(objRef)
    }
}
```

#### 6.3 Macro Arguments

When a macro is invoked, its arguments are claimed by the macro's ExecutionState via SetVar for each parameter.

#### 6.4 Bubbles

```go
type BubbleEntry struct {
    Content interface{}  // May contain ObjectRef
    // ...
}

// Bubble claims its content when created
// Bubble releases its content when consumed/discarded
```

#### 6.5 Channels

Channels must claim references for buffered items:

```go
func (ch *StoredChannel) Send(value interface{}) {
    // If buffered and not immediately received
    if objRef, ok := value.(ObjectRef); ok {
        ch.executor.RefClaim(objRef)  // Channel owns it while buffered
    }
    ch.buffer <- value
}

func (ch *StoredChannel) Receive() interface{} {
    value := <-ch.buffer
    // Transfer ownership to receiver - don't release here
    // Receiver will claim via SetVar/SetResult
    return value
}
```

**Note**: Need careful handling to avoid double-claim. May need "transfer" semantics.

#### 6.6 Nested in Lists

Handled by constructor (claims on creation) and RefRelease cascade (releases on GC).

### 7. Tokens and Suspension

Tokens remain separate from the ObjectRef system (visible to userland).

A suspended state (represented by token) acts as **bookkeeper** for existing claims:
- Does NOT make new claims on suspended material
- Holds/preserves the claims that were already in the frozen execution state
- When resumed, the execution state continues with its original claims
- If token is abandoned, the suspended state's claims are released

```go
type SuspendedState struct {
    Token        string
    State        *ExecutionState  // Holds all the claims
    // ...
}

// When token is abandoned/cancelled:
func (ss *SuspendedState) Abandon() {
    ss.State.ReleaseAll()  // Release all claims held by suspended state
}
```

### 8. What Gets Deleted

These functions/patterns will be removed:

```
// Delete these functions entirely:
findStoredListID
findStoredBytesID
findStoredChannelID
findStoredFileID
findStoredStringID
findStoredBlockID
findStoredStructID

// Delete/rename to break all call sites:
incrementObjectRefCount    → (delete, use RefClaim)
decrementObjectRefCount    → (delete, use RefRelease)
ClaimObjectReference       → (delete, use RefClaim)
ReleaseObjectReference     → (delete, use RefRelease)
claimNestedReferences      → (delete, constructors handle this)
releaseNestedReferences    → (delete, RefRelease cascade handles this)
extractObjectReferencesLocked → (delete, values are ObjectRef directly)

// Delete marker parsing in most places:
parseObjectMarker          → (keep only for code substitution boundary)
```

### 9. Migration Steps

#### Step 1: Add New Types (non-breaking)
- Add `ObjectRef` type
- Add `ObjectType` enum
- Add new central functions with new names
- Add to Executor: `freeIDs` slice

#### Step 2: Update Constructors
- Modify `NewStoredList`, etc. to return `ObjectRef`
- Have constructors claim nested refs

#### Step 3: Update Ownership Points
- Update `SetVar`, `SetResult`, etc. to handle `ObjectRef`
- Update channel send/receive
- Update bubble handling

#### Step 4: Break Old Functions
- Rename/delete old functions (compiler finds all call sites)
- Fix each call site to use new patterns

#### Step 5: Remove Marker String Usage
- Audit all `parseObjectMarker` calls
- Remove internal marker usage
- Keep marker conversion only at code substitution boundary (if needed)

#### Step 6: Testing
- Run existing test suite
- Add specific refcount tests
- Test edge cases: scope transitions, channel buffering, bubbles, suspension

### 10. Open Questions

1. **Code substitution**: Do we actually need markers anywhere? Or can ObjectRef flow through the entire system?

2. **Channel ownership transfer**: When an item is received from a channel, who claims it? Need to avoid:
   - Double claim (channel claimed it, receiver claims it)
   - Premature release (channel releases before receiver claims)

3. **Result transfer between scopes**: The result holds the claim during macro return. Verify this pattern is watertight.

4. **Thread safety**: Multiple fibers accessing same object. Current mutex approach should work but verify.

### 11. File-by-File Impact

High-impact files to modify:
- `executor_core.go` - Executor struct, central functions
- `executor_objects.go` - Object registry, delete findStoredXxxID functions
- `state.go` - SetVar, SetResult, ownership in ExecutionState
- `types.go` - ObjectRef definition, constructor updates
- `lib_core.go` - List creation, setListResult helper
- `lib_types.go` - Type operations
- `lib_system.go` - Channel operations
- `fiber.go` - Fiber argument handling
- `executor_commands.go` - Command execution, scope handling

Medium-impact:
- `lib_files.go`
- `lib_bitwise.go`
- `stdlib.go` - Formatting/display
- `executor_substitution.go` - Variable substitution

Lower-impact:
- Most other lib_*.go files (consumers of the system)
