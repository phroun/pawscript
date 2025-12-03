# PawScript Interpreter Optimizations

This document describes the performance optimizations implemented in the PawScript interpreter to minimize overhead during execution, particularly for macro-heavy workloads.

## Overview

The interpreter employs several optimization strategies focused on:
1. Avoiding repeated parsing and compilation work
2. Reducing memory allocations through pooling and lazy initialization
3. Caching resolved handlers to avoid map lookups
4. Minimizing GC pressure in hot paths

## Optimization Levels

PawScript supports configurable optimization levels:
- `OptimizeNone (0)`: No optimizations - parse everything fresh each time
- `OptimizeBasic (1)`: Enable caching (default)

## Implemented Optimizations

### 1. AST Caching for Macro Bodies

When a macro is first executed, its body is parsed into a sequence of `ParsedCommand` structures. These are cached on the `StoredMacro` object and reused on subsequent invocations.

**Files:** `src/executor_core.go` (GetOrCacheMacroCommands)

**Impact:** Eliminates repeated parsing of macro bodies. For a macro called N times, parsing happens once instead of N times.

### 2. Brace Expression Caching

Brace expressions like `{add 1, 2}` within macro bodies are pre-parsed into ASTs during macro body caching. The parsed commands are stored on `TemplateSegment.BraceAST`.

**Files:** `src/executor_substitution.go` (preCacheBraceExpressions, ParseSubstitutionTemplate)

**Impact:** Nested brace expressions are parsed once at macro definition time, not on every evaluation.

### 3. Substitution Templating

Argument strings containing substitution markers (`$N`, `~var`, `{...}`) are pre-parsed into `SubstitutionTemplate` structures consisting of literal segments and substitution segments. This avoids re-scanning the string on each execution.

**Files:** `src/executor_substitution.go` (ParseSubstitutionTemplate, ApplyTemplate), `src/types.go` (SubstitutionTemplate, TemplateSegment)

**Impact:** Substitution processing becomes a template application rather than full re-parsing.

### 4. Block Argument Caching for Loops

Loop commands (`while`, `for`, `repeat`, `fizz`) cache their body block parsing. If the block doesn't contain `$N` patterns (which require per-call re-parsing), the parsed commands are stored on the loop command's `CachedBlockArgs` map.

**Files:** `src/executor_core.go` (GetOrCacheBlockArg)

**Impact:** Loop bodies are parsed once per unique loop command.

### 5. Handler Caching with Generation-Based Invalidation

Resolved command handlers and macros are cached directly on `ParsedCommand` objects, avoiding map lookups on subsequent executions. The cache uses a generation counter to invalidate entries when registries are modified.

**Mechanism:**
- Each `ModuleEnvironment` has a `RegistryGeneration` counter
- When a command/macro is registered, imported, or removed, the generation increments
- Cached entries store the environment reference and generation at resolution time
- On cache lookup, if environment + generation match, the cached handler is used directly

**Files:**
- `src/types.go` (ParsedCommand.ResolvedHandler, ResolvedMacro, CachedEnv, CachedGeneration)
- `src/module.go` (ModuleEnvironment.RegistryGeneration)
- `src/executor_commands.go` (cache check in executeSingleCommand)

**Limitations:**
- Commands with dynamic names (e.g., `{~cmd arg}`) cannot be cached
- Cache is per-`ParsedCommand`, so dynamically constructed commands don't benefit

### 6. ExecutionState Pooling

`ExecutionState` objects are obtained from a sync.Pool and recycled after use, reducing allocation pressure during macro calls where many short-lived states are created.

**Files:** `src/executor_core.go` (executionStatePool)

**Impact:** Reduces GC pressure in recursive macro calls.

### 7. SubstitutionContext Pooling

Similar to ExecutionState, `SubstitutionContext` objects are pooled and reused.

**Files:** `src/executor_commands.go` (substitutionContextPool)

**Impact:** Further reduces allocation overhead in hot execution paths.

### 8. Lazy Map Initialization

Several maps are lazily initialized to avoid allocation when not needed:
- `ModuleEnvironment.ModuleExports`: Only allocated when exports are defined
- `ExecutionState.bubbleMap`: Only allocated when bubbles are used in that state

**Files:** `src/module.go`, `src/types.go`

**Impact:** Reduces memory footprint for simple scripts and macro calls.

### 9. OriginalCmd Tracking for Position-Adjusted Copies

When executing cached commands, position information may need adjustment for error reporting. Instead of mutating cached commands, shallow copies are made with adjusted positions. The `OriginalCmd` field tracks the original so handler caching persists across invocations.

**Files:** `src/executor_core.go` (ExecuteParsedCommands), `src/types.go` (ParsedCommand.OriginalCmd)

**Impact:** Enables handler caching even when position adjustments require copying.

### 10. CapturedModuleEnv for Macro Caching

Macros capture their defining environment at definition time. To enable handler caching within macros (where a new child environment is created per call), the `CapturedModuleEnv` is propagated through substitution contexts.

**Files:** `src/types.go` (SubstitutionContext.CapturedModuleEnv), `src/executor_substitution.go`

**Impact:** Handler caching works correctly for commands inside macro bodies.

## Cache Invalidation Points

Registry generation is incremented at:
- `RegisterCommand` / `RegisterCommandInModule`
- `RegisterMacro` / `ImportModuleToRoot`
- `macro` / `macro_forward` command execution
- `IMPORT` / `REMOVE` module operations

## Benchmark Results

Using the recursive Fibonacci benchmark (`examples/benchmark_fibonacci.paw`):

| Operation | Time |
|-----------|------|
| fib(15) | ~128ms |
| fib(20) | ~1,374ms |
| fib(25) | ~15,285ms |

For comparison, equivalent Ruby code runs fib(25) in ~6ms, demonstrating that interpreted dispatch overhead dominates. Future work could explore bytecode compilation.

## Future Optimization Opportunities

1. **Bytecode Compilation**: Convert AST to threaded bytecode for faster dispatch
2. **JIT Compilation**: Generate native code for hot macro bodies
3. **Constant Folding**: Evaluate constant expressions at parse time
4. **Memoization Support**: Language-level memoization for pure functions
5. **Inline Caching**: Polymorphic inline caches for method dispatch
