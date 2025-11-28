# Copy-on-Write (COW) Inheritance System

PawScript uses a copy-on-write (COW) pattern for its module environment system. This provides efficient memory usage while maintaining proper isolation between execution contexts.

## Core Concept

### LibraryInherited

`LibraryInherited` holds all items available for the current Environment to IMPORT.

They may be removed by using the `LIBRARY "forget ..."` supercommand.

### LibraryRestricted

`LibraryRestricted` holds all items which will be available to child Environments,
such as Macros or included modules.

The items in LibraryRestricted are essentially a subset of the items in LibraryInherited, and
are managed by LIBRARY "restrict ..." and LIBRARY "allow..."

Up until the first change, however, LibraryRestricted is just a reference that points to the
very same underlying map as LibraryInherited.  This is to save time and memory so that it only
needs to be duplicated if a more restrictive Child Environment is being prepared.

###  Commands, Macros, Objects, and their MetaData

Each `ModuleEnvironment` contains paired registries for three types of imported library items
that follow the COW pattern:

| Inherited (unchanged original) | Module (reference or COW working copy) | Purpose of working Copy |
| `CommandRegistryInherited` | `CommandRegistryModule` |  |
| `MacrosInherited` | `MacrosModule` | Managed by IMPORT and REMOVE super commands. |
| `ObjectsInherited` | `ObjectsModule` | Managed by IMPORT and REMOVE super commands. |
| `ItemMetadataInherited` | `ItemMetadataModule` | Managed by IMPORT and REMOVE super commands. |

For each of these, the Module version is managed by the IMPORT and REMOVE super commands.
The items brought in from an IMPORT command are sourced from `LibraryRestricted`.

## Critical: Shared References

**Initially, both the Inherited and Module fields point to the exact same underlying map.**

```go
// In NewModuleEnvironment():
cmdRegistry := make(map[string]Handler)

return &ModuleEnvironment{
    CommandRegistryInherited: cmdRegistry,
    CommandRegistryModule:    cmdRegistry,  // Same instance!
    // ...
}
```

This means:
- Reading from either field returns the same data
- Adding to `ObjectsInherited` is immediately visible via `ObjectsModule`
- No memory is duplicated until a modification triggers COW
- You should be reading from the Module version to find out the current state from the Child's point of view

## When COW Triggers

The maps remain shared until code explicitly copies them. Each registry has an `Ensure*Copied()` method:

```go
func (env *ModuleEnvironment) EnsureCommandRegistryCopied() {
    if env.commandsModuleCopied {
        return  // Already copied, nothing to do
    }

    // Create a new map with copies of all entries
    newModule := make(map[string]Handler, len(env.CommandRegistryModule))
    for k, v := range env.CommandRegistryModule {
        newModule[k] = v
    }

    env.CommandRegistryModule = newModule
    env.commandsModuleCopied = true
}
```

After copying:
- `CommandRegistryInherited` still points to the original shared map
- `CommandRegistryModule` points to a new, independent map
- Modifications to the Module no longer affect the Inherited

## Child Environment Inheritance

When creating a child environment (e.g., for macro execution), the child inherits from the parent's "effective" registry - which is the Module layer:

```go
func NewChildModuleEnvironment(parent *ModuleEnvironment) *ModuleEnvironment {
    // Get what the parent currently sees
    effectiveCommands := getEffectiveCommandRegistry(parent)  // returns parent.CommandRegistryModule
    effectiveObjects := getEffectiveObjectRegistry(parent)    // returns parent.ObjectsModule

    return &ModuleEnvironment{
        // Child's Inherited and Module both point to parent's effective registry
        CommandRegistryInherited: effectiveCommands,
        CommandRegistryModule:    effectiveCommands,  // Same reference!

        ObjectsInherited: effectiveObjects,
        ObjectsModule:    effectiveObjects,  // Same reference!

        // COW flags reset - child hasn't made its own copy yet
        commandsModuleCopied: false,
        objectsModuleCopied:  false,
    }
}
```

## Practical Implications

### Adding Items to Root Environment

When populating the root environment (e.g., in `PopulateIOModule`):

```go
env.ObjectsInherited["#stdout"] = stdoutCh
```

This works because:
1. `ObjectsInherited` and `ObjectsModule` are the same map
2. Child environments inherit from `ObjectsModule`
3. So the item is visible everywhere without needing to add to both

### Modifying Items in Child Environment

When a child environment modifies a registry:

```go
// In child environment:
env.EnsureCommandRegistryCopied()  // Triggers COW
env.CommandRegistryModule["mycommand"] = handler
```

After this:
1. Child's `CommandRegistryModule` is now a separate map
2. Child's `CommandRegistryInherited` still references the parent's data
3. Parent's registries are unchanged

### The REMOVE Command

REMOVE uses COW to hide items without affecting the parent:

```go
env.EnsureCommandRegistryCopied()
env.CommandRegistryModule["somecommand"] = nil  // nil means "removed"
```

The item still exists in `Inherited` but lookups check `Module` first and see `nil`.

## Memory Efficiency

The COW pattern is memory-efficient because:

1. **No copying until needed**: Child environments share parent data by default
2. **Shallow copies**: When COW triggers, only the map structure is copied, not the values (handlers, macros, etc. are still shared references)
3. **One-time cost**: The `*Copied` flags ensure copying happens at most once per registry per environment

## Registry Lookup Order

When looking up an item (e.g., a command), only the Module layer needs to be checked:

```go
func (env *ModuleEnvironment) GetCommand(name string) (Handler, bool) {
    handler, exists := env.CommandRegistryModule[name]
    if !exists {
        return nil, false
    }
    // nil handler means explicitly removed
    if handler == nil {
        return nil, false
    }
    return handler, true
}
```

Since Module and Inherited start as the same map, this naturally returns inherited items until COW creates divergence.

## Summary

| Scenario | Inherited | Module | Same Map? |
|----------|-----------|--------|-----------|
| Fresh environment | `map A` | `map A` | Yes |
| After adding item to Inherited | `map A` (with item) | `map A` (with item) | Yes |
| After EnsureCopied() | `map A` | `map B` (copy) | No |
| Child of above | `map B` | `map B` | Yes |

The COW system provides the illusion of independent environments while minimizing actual memory allocation and copying until modifications require isolation.
