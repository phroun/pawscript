# Result Flow in PawScript

PawScript has a unique result-passing system where formal results flow between commands, through brace expressions, and out of macros.

## The Result System

**`ExecutionState.currentResult`** - A "formal result" that persists across commands, separate from the boolean status.

### Core Commands

- **`set_result value`** - Explicitly stores a value as the formal result
- **`get_result`** - Returns the current formal result (used in `{get_result}` brace expressions)
- **`ret [value]`** - Early return from a block/macro. With no arg, it uses the current result; with an arg, it uses that as the result

### Status vs Result

Status (bool) and result (any value) are orthogonal - a command can fail (`false`) but still have a meaningful result.

## Result Flow Direction

### Out of Macros

When a macro ends, its final result automatically flows back to the caller's state:

```go
// Transfer result to parent state
if macroState.HasResult() {
    state.SetResult(macroState.GetResult())
}
```

You don't need an explicit `return` for the last value - results flow implicitly.

### Into Brace Expressions

Brace expressions inherit the outer context's current result:

```go
currentResult: parent.currentResult,  // Inherit parent's result for get_result
hasResult:     parent.hasResult,      // Inherit parent's result state
```

This means `{get_result}` inside a brace expression can access what was set before:

```
set_result 42
echo "The value is {get_result}"   # prints "The value is 42"
```

### Into Macros

Macros do **not** inherit the caller's result - they start with a fresh slate:

```go
currentResult: nil,    // Fresh result storage for this child
hasResult:     false,  // Child starts with no result
```

## Summary: The Asymmetry

| Context | Result Flows In? | Result Flows Out? |
|---------|------------------|-------------------|
| Brace expressions | Yes | Yes |
| Macro calls | No | Yes |

- **Brace expressions** are like inline evaluation - result flows in and out, same "stream"
- **Macros** are like function calls - start fresh, but return their result to caller

To pass a result *into* a macro, pass it as an argument explicitly. Inside a brace expression, use `{get_result}` to access the outer context's result.
