# Comprehensive Escape Sequence Support

## Overview

PawScript supports a comprehensive set of escape sequences in string literals, matching the capabilities of modern programming languages.
This guide applies to both parsing (reading escape sequences) and escaping (writing escape sequences).

## Supported Escape Sequences

### Control Characters (Short Forms)

These use the shortest possible representation for common control characters:

| Escape | Hex  | Dec | Name | Description |
|--------|------|-----|------|-------------|
| `\0`   | 0x00 | 0   | NUL  | Null byte |
| `\a`   | 0x07 | 7   | BEL  | Bell/alert |
| `\b`   | 0x08 | 8   | BS   | Backspace |
| `\e`   | 0x1B | 27  | ESC  | Escape |
| `\f`   | 0x0C | 12  | FF   | Form feed |
| `\n`   | 0x0A | 10  | LF   | Newline (line feed) |
| `\r`   | 0x0D | 13  | CR   | Carriage return |
| `\t`   | 0x09 | 9   | HT   | Horizontal tab |

### Special Characters

| Escape | Result | Description |
|--------|--------|-------------|
| `\\`   | `\`    | Backslash |
| `\"`   | `"`    | Double quote |
| `\'`   | `'`    | Single quote |

### Line Continuation

| Escape | Result | Description |
|--------|--------|-------------|
| `\` followed by newline | Empty string | Line continuation (removes backslash and newline) |

### Hex Escape

| Format | Example | Result | Description |
|--------|---------|--------|-------------|
| `\xHH` | `\x41` | `A` | Hex byte (exactly 2 hex digits) |
| `\xHH` | `\x0a` | newline | Same as `\n` |
| `\xHH` | `\x00` | null | Same as `\0` |

### Unicode Escapes

| Format | Example | Result | Description |
|--------|---------|--------|-------------|
| `\uXXXX` | `\u20ac` | € | Unicode codepoint (4 hex digits) |
| `\uXXXX` | `\u263a` | ☺ | Smiley face |
| `\UXXXXXXXX` | `\U0001f600` | 😀 | Unicode codepoint (8 hex digits) |
| `\UXXXXXXXX` | `\U0001f680` | 🚀 | Rocket emoji |

### Generic Escape

For any other character after backslash, the backslash is removed:

| Escape | Result | Description |
|--------|--------|-------------|
| `\!`   | `!`    | Removes backslash |
| `\@`   | `@`    | Removes backslash |
| Any `\x` | `x` | Removes backslash, keeps character |

## Examples

### Basic Control Characters

```pawscript
echo "Line 1\nLine 2"
# Output: Line 1
#         Line 2

echo "Col1\tCol2\tCol3"
# Output: Col1    Col2    Col3

echo "Path: C:\\Users\\Name"
# Output: Path: C:\Users\Name
```

### Special Effects

```pawscript
echo "Alert!\a"
# Produces a beep on terminals that support it

echo "Overwrite\rXX"
# Output: XXerwrite (carriage return moves cursor to start)

echo "\e[31mRed text\e[0m"
# Output: Red text (on ANSI-compatible terminals)
```

### Null Bytes

```pawscript
echo "Before\0After"
# Output: Before[null]After (contains actual null byte)

set data, "Binary\0Data"
# Stores string with embedded null byte
```

### Line Continuation

```pawscript
echo "This is a \
very long line \
split across \
multiple lines"
# Output: This is a very long line split across multiple lines
```

### Hex Escapes

```pawscript
echo "ASCII: \x41\x42\x43"
# Output: ASCII: ABC

echo "Hex newline: \x0a"
# Output: Hex newline: 
#         (newline character)

echo "Null: [\x00]"
# Output: Null: [[null]]
```

### Unicode Characters

```pawscript
echo "Symbols: \u2665 \u263a \u00a9"
# Output: Symbols: ♥ ☺ ©

echo "Euro: \u20ac 100"
# Output: Euro: € 100

echo "Emojis: \U0001f600 \U0001f680 \U0001f497"
# Output: Emojis: 😀 🚀 💗
```

### Mixed Escapes

```pawscript
echo "Mix: \n\t\x41\u0042\U00000043"
# Output: Mix: 
#             ABC
```

## Escaping During Substitution

When values are substituted into commands (via `$1`, `$*`, or `{...}`), special characters are automatically escaped using the shortest possible form.

### Escape Priority

The escaper uses these rules to produce the shortest output:

1. **Short-form control characters** (preferred):
   - `\0` instead of `\x00`
   - `\n` instead of `\x0a`
   - `\t` instead of `\x09`
   - etc.

2. **PawScript syntax characters** (always escaped):
   - `;` → `\;`
   - `&` → `\&`
   - `|` → `\|`
   - `(` → `\(`
   - `)` → `\)`
   - `{` → `\{`
   - `}` → `\}`
   - `"` → `\"`
   - `'` → `\'`
   - `\` → `\\`
   - `$` → `\$`
   - `,` → `\,`
   - `!` → `\!`
   - ` ` (space) → `\ `

3. **Other non-printable ASCII** (0x00-0x1F, 0x7F):
   - Escaped as `\xHH`

4. **High Unicode** (> 0xFFFF):
   - Escaped as `\UXXXXXXXX`

5. **Regular characters**:
   - Left as-is (including normal Unicode 0x80-0xFFFF)

### Examples of Automatic Escaping

```pawscript
# Newlines are escaped as \n (short form)
macro show(echo "Value: $1")
show "Line1
Line2"
# Expands to: echo "Value: Line1\nLine2"
# Output: Value: Line1\nLine2

# Tabs are escaped as \t (short form)
set value, "Col1	Col2"
echo "Data: {get value}"
# Output: Data: Col1\tCol2

# Null bytes are escaped as \0 (short form)
set data, "Before\0After"
echo "Stored: {get value}"
# Output: Stored: Before\0After

# Syntax characters are escaped
macro display(echo $1)
display "a, b; c"
# Expands to: echo a\,\ b\;\ c
# Output: a, b; c
```

## Implementation Details

### Parsing: `parseStringLiteral()` in `parser.go`

Processes escape sequences when parsing quoted strings:

```go
switch nextChar {
case '0': result.WriteRune('\x00')  // \0
case 'n': result.WriteRune('\n')    // \n
case 't': result.WriteRune('\t')    // \t
// ... etc
case 'x':  // \xHH - parse 2 hex digits
case 'u':  // \uXXXX - parse 4 hex digits  
case 'U':  // \UXXXXXXXX - parse 8 hex digits
}
```

### Escaping: `escapeSpecialCharacters()` in `executor2.go`

Produces escaped strings during substitution, preferring short forms:

```go
switch r {
case '\x00': result.WriteString(`\0`)  // Prefer \0 over \x00
case '\n':   result.WriteString(`\n`)  // Prefer \n over \x0a
case '\t':   result.WriteString(`\t`)  // Prefer \t over \x09
// ... etc
}
```

## Error Handling

### Invalid Hex/Unicode Sequences

If hex or unicode escapes don't have enough digits, they're treated as literal text:

```pawscript
echo "\x1"
# Not enough hex digits, outputs: x1 (backslash removed, 'x' treated as generic escape)

echo "\u12"
# Not enough unicode digits, outputs: u12

echo "\U123"
# Not enough Unicode digits, outputs: U123
```

### Invalid Unicode Codepoints

For `\UXXXXXXXX`, values > 0x10FFFF are invalid and treated as literal:

```pawscript
echo "\U99999999"
# Invalid codepoint, outputs: U99999999
```

## Use Cases

### Binary Data

```pawscript
set packet, "\x00\x01\x02\x03"
# Store binary data with null bytes and control chars
```

### Terminal Control

```pawscript
echo "\e[2J\e[H"
# Clear screen and move cursor home (on ANSI terminals)
```

### Multi-line Strings

```pawscript
set config, "line1\nline2\nline3"
# Store multi-line content in single string
```

### Long Lines in Scripts

```pawscript
echo "This is a very \
long message that \
spans multiple lines \
for readability"
# Line continuation makes source readable
```

### Unicode Content

```pawscript
echo "Price: \u20ac100"
# Unicode symbols

echo "Emoji: \U0001f44d"
# Emoji characters
```

## Testing

Test file provided: `test_comprehensive_escapes.paw`

Tests cover:
1. All short-form control characters
2. Quote and backslash escaping
3. Line continuation
4. Hex escapes (all ASCII range)
5. Unicode escapes (4-digit)
6. Unicode escapes (8-digit)
7. Mixed escape combinations
8. Escaping during macro substitution
9. Control characters in variables
10. PawScript special characters
11. Non-printable characters
12. Null byte preservation
13. Invalid escape sequences
14. Complex combinations

## Compatibility Notes

- **Backward compatible**: Old escape sequences (`\n`, `\t`, `\\`, etc.) work exactly as before
- **Extension**: New escapes (`\0`, `\a`, `\e`, `\xHH`, `\uXXXX`, `\UXXXXXXXX`) are additions
- **Consistent**: Both parsing and escaping use the same rules
- **Shortest form**: Escaper always prefers `\0` over `\x00`, `\n` over `\x0a`, etc.

## Performance

- Parsing is character-by-character with minimal backtracking
- Escaping is single-pass through the string
- Both operations are O(n) where n is string length
- No regex or complex matching required
