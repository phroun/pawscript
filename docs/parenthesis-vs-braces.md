# Parsing of Grouping Constructs: Parentheses vs Braces

## Overview

PawScript has two grouping constructs that look similar but behave very differently:
- **Parentheses `(...)`**: For storing/grouping code as data
- **Braces `{...}`**: For immediate evaluation and substitution

This document explains what processing occurs during **initial parsing** (before execution).

## Parentheses `(...)` - Initial Parsing

Parentheses group content as data, protecting it from interpretation.

### What Gets Processed

| Feature | Processed? | Details |
|---------|-----------|---------|
| **Comment removal** | ✅ Yes | Comments are removed |
| **Keyword normalization** | ❌ No | `then`/`else`/`not` preserved |
| **Escape sequences** | ❌ No | Kept as literal text (e.g., `\n` stays as backslash-n) |
| **Outer delimiters** | ✅ Stripped | The `(` and `)` themselves are removed |
| **Nested structure** | ✅ Tracked | Nested parens, quotes, braces tracked for matching |
| **Comma protection** | ✅ Yes | Commas inside don't split arguments |

### Processing Steps

1. **Comments are removed**:
   ```pawscript
   a: (code # comment)
   # Stores: "code "
   ```

2. **Keywords are preserved**:
   ```pawscript
   a: (if x then y else z)
   # Stores: "if x then y else z"
   ```

3. **Escape sequences are kept literal**:
   ```pawscript
   a: (hello\nworld)
   # Stores: "hello\nworld" (literal backslash-n, NOT a newline)
   ```

4. **Outer parens are stripped**:
   ```pawscript
   a: (content)
   # Stores: "content" (without the parens)
   ```

5. **Nested structure is preserved**:
   ```pawscript
   a: (outer (inner) more)
   # Stores: "outer (inner) more"
   ```

6. **Quotes inside are preserved**:
   ```pawscript
   a: (say "hello")
   # Stores: "say "hello"" (quotes kept)
   ```

7. **Braces inside are preserved** (NOT evaluated):
   ```pawscript
   a: (code {with braces})
   # Stores: "code {with braces}" (literal braces, not evaluated)
   ```

### Example: Full Pipeline

```pawscript
code: (if true then #( comment )# echo "yes\n" else echo "no")

# Step 1: Comment removal
# Result: (if true then  echo "yes\n" else echo "no")

# Step 2: Keyword normalization - SKIPPED (inside parens)
# Result: (if true then  echo "yes\n" else echo "no")

# Step 3: Parse as argument - strips outer parens
# Stored: "if true then  echo "yes\n" else echo "no"

# Note: The \n inside quotes is still literal at this point
# It's part of the stored text, not processed as newline
```

## Braces `{...}` - Initial Parsing

Braces trigger **immediate evaluation** during the substitution phase, which happens before the command is fully parsed.

### What Gets Processed

| Feature | Processed? | Details |
|---------|-----------|---------|
| **Evaluation timing** | ✅ Immediate | Content executed during substitution phase |
| **Comment removal** | ✅ Yes | Comments removed before evaluation |
| **Keyword normalization** | ✅ Yes | `then`/`else`/`not` converted when evaluated |
| **Escape sequences** | ✅ Yes | Processed based on context (quotes or not) |
| **Outer delimiters** | ✅ Replaced | The `{` and `}` replaced with result |
| **Nested structure** | ✅ Tracked | Nested braces evaluated recursively |
| **Location tracking** | ✅ Yes | Not inside parens or quotes |

### Processing Steps

1. **Braces are detected at top level**:
   ```pawscript
   echo "Result: {echo test}"
   # The {echo test} is detected as brace expression
   ```

2. **Content is executed immediately**:
   ```pawscript
   echo "Result: {set_result test}"
   # Step 1: Execute "set_result test" 
   # Step 2: Get result: "test"
   # Step 3: Substitute into string
   # Becomes: echo "Result: test"
   ```

3. **Result replaces the entire brace expression**:
   ```pawscript
   a: {set_result computed}
   # Executes: set_result computed
   # Result: "computed"
   # Substitutes: a: computed
   # Stores: "computed" (not "{set_result computed}")
   ```

4. **Nested braces are evaluated**:
   ```pawscript
   echo {set_result {set_result inner}}
   # Innermost first: {set_result inner} → "inner"
   # Becomes: echo {set_result inner}
   # Then: {set_result inner} → "inner"  
   # Becomes: echo inner
   ```

### Key Difference: Location Matters

**Braces at top level (outside parens/quotes):** Evaluated immediately

```pawscript
a: {set_result test}
# Brace at top level → Evaluated
# Executes: set_result test
# Stores: "test" (the result)
```

**Braces inside parentheses:** Kept as literal text

```pawscript
a: (code {with braces})
# Braces inside parens → NOT evaluated
# Stores: "code {with braces}" (literal text)
```

**Braces inside quotes:** Evaluated (substituted)

```pawscript
a: "result: {set_result test}"
# Braces inside quotes → Evaluated
# Executes: set_result test → "test"
# Becomes: a: "result: test"
# Stores: "result: test"
```

## Side-by-Side Comparison

### Storage Behavior

```pawscript
# Parentheses: Store code as text
a: (echo hello)
echo ~a
# Output: echo hello

# Braces: Execute and store result
b: {set_result hello}
echo ~b
# Output: hello
```

### Nested Structure

```pawscript
# Parentheses preserve nested braces
a: (outer {inner})
echo ~a
# Output: outer {inner}

# Braces at top level evaluate immediately
b: outer {set_result inner}
# Executes: set_result inner → "inner"
# Stores: "outer inner"
```

### Escape Sequences

```pawscript
# Parentheses: Keep literal
a: (hello\nworld)
echo ~a
# Output: hello\nworld (literal backslash-n)

# Braces with quotes: Process escapes in quotes
b: {set_result "hello\nworld"}
# Executes: set_result "hello\nworld"
# The string has escape processed: actual newline
# Result stored: "hello
#                 world"
```

### Comments

```pawscript
# Both remove comments during processing
a: (code #comment)
# Stores: "code "

b: {set_result test #comment}
# Executes: set_result test (comment removed)
# Stores: "test"
```

### Keywords

```pawscript
# Parentheses: Keywords preserved
a: (if x then y else z)
echo ~a
# Output: if x then y else z

# Braces: Keywords normalized during execution
b: {set_result "dummy"; echo "if x then y"}
# The string inside quotes is not normalized
# But if we had: {if true then echo "yes" else echo "no"}
# During execution, keywords would be normalized
```

## Processing Order: Complete Pipeline

### For Parentheses

```
User writes: a: (if x then y #comment)
       ↓
1. Comment removal: (if x then y )
       ↓
2. Keyword normalization: SKIPPED (inside parens)
       ↓
3. Argument parsing: Strips outer parens
       ↓
4. Stored value: "if x then y "
```

### For Braces (Top Level)

```
User writes: a: {set_result test #comment}
       ↓
1. Substitution phase detects brace expression
       ↓
2. Brace content extracted: "set_result test #comment"
       ↓
3. Content is executed via ExecuteWithState:
   - Comment removal: "set_result test "
   - Keyword normalization: "set_result test " (no keywords)
   - Execution: set_result command runs, sets result to "test"
       ↓
4. Result ("test") replaces entire {..}
       ↓
5. String becomes: a: test
       ↓
6. Stored value: "test"
```

### For Braces (Inside Parens)

```
User writes: a: (code {braces})
       ↓
1. Substitution phase detects parens
       ↓
2. Braces inside parens are NOT evaluated
       ↓
3. Argument parsing: Strips outer parens
       ↓
4. Stored value: "code {braces}"
```

## Use Cases

### Parentheses: Storing Code

**Use when:** You want to store code to execute later

```pawscript
# Store conditional logic
condition: (if ~ready then start_process else wait)

# Store loop body
loop_body: (
    process_item;
    increment_counter
)

# Store complex command
handler: (handle_error & retry | abort)
```

### Braces: Computed Values

**Use when:** You want a value computed now

```pawscript
# Computed arguments
echo "Count: ~counter"

# Computed filenames
save_file "output_~timestamp;.txt"

# Nested computations
total: {add ~x, ~y}

# Conditional values
msg: {if ~error then echo "Failed" else echo "OK"}
```

## Summary Table

| Aspect | Parentheses `(...)` | Braces `{...}` |
|--------|-------------------|----------------|
| **Purpose** | Group/store code as data | Evaluate and substitute result |
| **Timing** | Content stored for later | Content executed immediately |
| **Comment removal** | ✅ During parsing | ✅ During execution |
| **Keyword normalization** | ❌ Preserved | ✅ Applied during execution |
| **Escape sequences** | ❌ Literal text | ✅ Processed if in quotes |
| **Outer delimiters** | Stripped from result | Replaced with result |
| **Inside parens** | N/A | NOT evaluated (literal) |
| **Inside quotes** | N/A | ✅ **Evaluated** (substituted) |
| **Inside braces** | NOT evaluated (literal) | Evaluated (nested) |
| **Result** | Text without outer parens | Computed value |

## Important Notes

1. **Braces are eagerly evaluated**: They execute during substitution, before the containing command runs
2. **Parens protect braces**: Braces inside parens are kept as literal text
3. **Quotes do NOT protect braces**: Braces inside quotes ARE evaluated and substituted
4. **Escape sequences differ**: 
   - Parens: `\n` stays as literal `\n`
   - Braces: Depends on context (quotes process escapes)
5. **Keywords differ**:
   - Parens: Keywords preserved as-is
   - Braces: Keywords normalized during execution

## When Content Gets Fully Parsed

### Parentheses

Content is only fully parsed (keywords normalized, etc.) when explicitly executed:

```pawscript
code: (if true then echo "yes" else echo "no")
# Stored: "if true then echo "yes" else echo "no"" (preserved)

# If you execute it somehow (future feature):
# THEN keywords would be normalized and escape sequences processed
```

### Braces

Content is fully parsed during the brace evaluation:

```pawscript
echo {if true then echo "yes" else echo "no"}
# During brace evaluation:
#   - Keywords normalized: if true & echo "yes" | echo "no"
#   - Commands executed
#   - Result substituted
# Output: yes
```

## Edge Cases

### Empty Constructs

```pawscript
a: ()
# Stores: "" (empty string)

b: {}
# Executes: (empty command)
# Result: false (no command to execute)
```

### Whitespace

```pawscript
a: (  spaced  )
# Stores: "  spaced  " (whitespace preserved)

b: {  echo test  }
# Executes: echo test (whitespace trimmed during execution)
# Result: "test"
```

### Mixed Nesting

```pawscript
# Parens can contain braces (literal)
a: (outer {inner})
# Stores: "outer {inner}"

# Braces can contain parens (executed)
b: {set_result (grouped text)}
# Executes: set_result (grouped text)
# Result: "grouped text" (parens stripped during argument parsing)
```
