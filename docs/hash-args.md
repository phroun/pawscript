# Hash-Prefixed Arguments

Some PawScript commands accept **bare hash-prefixed symbols** (like `#out`, `#random`, `#mychannel`) as arguments. These symbols receive special resolution handling by the command itself, distinct from tilde-resolved variables.

## Bare Symbols vs Tilde Resolution

There are two ways to pass a hash-prefixed value to a command:

```pawscript
# Bare symbol - command resolves it
echo #mychan, "Hello"

# Tilde-resolved - resolved before command sees it
echo ~#mychan, "Hello"
```

With **tilde resolution** (`~#mychan`), the variable is resolved by the executor *before* the command receives it. The command sees the actual channel object.

With a **bare symbol** (`#mychan`), the Symbol value `#mychan` is passed directly to the command, which then performs its own resolution.

## Resolution Order

Commands that support bare hash-prefixed symbols use this resolution order:

1. **Local variables** - Check `ctx.state.GetVariable(name)`
2. **ObjectsModule** - Check the module's copy-on-write object layer
3. **ObjectsInherited** - Check inherited objects (e.g., `io::#out`, `io::#random`)

This allows local variable overrides to take precedence over inherited defaults.

## Example: echo Command

The `echo` command accepts an optional channel as its first argument:

```pawscript
# Use default #out (inherited from io module)
echo "Hello, world!"

# Explicit bare symbol - resolves #out through the chain
echo #out, "Hello, world!"

# Tilde-resolved - same effect, different mechanism
echo ~#out, "Hello, world!"

# Override locally
#out: ~#stderr
echo "This goes to stderr"

# Use a named channel
echo #mychan, "Message to custom channel"
```

When `echo` sees a bare symbol starting with `#`, it resolves it through the lookup chain. This means local overrides work automatically:

```pawscript
# Redirect all echo output to stderr for this scope
#out: ~#stderr
echo "Goes to stderr"
echo #out, "Also stderr"
```

## Example: random Command

The `random` command accepts an optional RNG generator as its first argument:

```pawscript
# Use default #random (inherited from io module)
x: {random 100}           # 0-99 using default generator

# Override locally for reproducible tests
#random: {rng seed: 42}
y: {random 100}           # Deterministic!

# Create a custom generator and pass via tilde
myRng: {rng seed: 123}
a: {random ~myRng, 100}   # Tilde resolves the variable

# To use bare symbol syntax, variable must have # in name
#myRng: {rng seed: 456}
b: {random #myRng, 100}   # Bare symbol, command resolves it
```

The `random` command checks if its first argument is:
1. A token marker (already resolved RNG)
2. A bare `#`-prefixed symbol (resolve through the chain)
3. A number (treat as range parameter, use default `#random`)

## When to Use Each Form

| Form | Use When |
|------|----------|
| `echo "text"` | Using the default channel |
| `echo #chan, "text"` | Channel name is known, want local override support |
| `echo ~#chan, "text"` | Explicit resolution, bypasses command's lookup |
| `echo ~myChan, "text"` | Variable doesn't start with `#` |

## Implementing Hash-Arg Resolution

Commands that want to support this pattern should:

1. Check if the first argument is a `Symbol` starting with `#`
2. Resolve through: local vars → ObjectsModule → ObjectsInherited
3. Fall back to a default (e.g., `#out` for echo, `#random` for random)

Example resolution helper pattern:

```go
// Check if first arg is a #-prefixed symbol
if sym, ok := args[0].(Symbol); ok {
    symStr := string(sym)
    if strings.HasPrefix(symStr, "#") {
        // Resolve through the chain
        if resolved := resolveFromChain(ctx, symStr); resolved != nil {
            return resolved, args[1:], true
        }
    }
}
// Fall back to default
return resolveFromChain(ctx, defaultName), args, true
```

## Commands Supporting Hash-Args

| Command | Default | Purpose |
|---------|---------|---------|
| `echo` | `#out` | Output channel |
| `print` | `#out` | Output channel |
| `write` | `#out` | Output channel (no newline) |
| `read` | `#in` | Input channel |
| `random` | `#random` | RNG generator |
