# Regex Commands

PawScript provides three regex commands in the `strlist::` module for pattern matching
and text manipulation. These commands use Go's `regexp` package which implements RE2
syntax (similar to Perl/PCRE but without backtracking).

## Input Types

All regex commands accept multiple input types:

| Type | Description | Example |
|------|-------------|---------|
| **string** | Quoted strings | `"hello world"` |
| **bytes** | StoredBytes (binary data) | `{bytes "hello"}` |
| **block** | ParenGroup (code blocks) | `(some code)` |
| **symbol** | Bare identifiers | `my_variable` |

When a command returns a modified string (like `regex_replace`), it preserves the
original input type where applicable.

## Pattern Types

Regex patterns can be specified as:

- **Literal blocks** (recommended): `(pattern)` - avoids escaping issues
- **Quoted strings**: `"pattern"` - may need escaping for special chars
- **Symbols**: `pattern` - for simple patterns without special chars

**Tip**: Use literal blocks `()` for patterns to avoid double-escaping issues.

---

## match

Tests if a regex pattern matches anywhere in the input.

### Syntax
```pawscript
match <input>, <pattern> [, case_insensitive: true]
```

### Arguments

| Argument | Type | Description |
|----------|------|-------------|
| `input` | string/bytes/block/symbol | Text to search |
| `pattern` | string/block/symbol | Regex pattern |

### Named Arguments

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `case_insensitive` | bool | `false` | Ignore case when matching |

### Returns

- `true` if pattern matches anywhere in input
- `false` if no match found

### Examples

```pawscript
# Basic match
{match "hello world", (world)}           # true
{match "hello world", (xyz)}             # false

# Case-insensitive
{match "Hello World", (hello)}                          # false
{match "Hello World", (hello), case_insensitive: true}  # true

# Match with regex features
{match "abc123def", ([0-9]+)}            # true (has digits)
{match "foo@bar.com", (\w+@\w+\.\w+)}    # true (email pattern)

# Using literal block for complex patterns
{match "2024-11-29", ([0-9]{4}-[0-9]{2}-[0-9]{2})}  # true
```

---

## regex_find

Finds matches and capture groups in the input.

### Syntax
```pawscript
regex_find <input>, <pattern> [, all: true] [, case_insensitive: true]
```

### Arguments

| Argument | Type | Description |
|----------|------|-------------|
| `input` | string/bytes/block/symbol | Text to search |
| `pattern` | string/block/symbol | Regex pattern (may include capture groups) |

### Named Arguments

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `all` | bool | `false` | Find all matches instead of just first |
| `case_insensitive` | bool | `false` | Ignore case when matching |

### Returns

**Single match (default):**
- Returns a list: `[fullMatch, group1, group2, ...]`
- Returns `nil` if no match found

**All matches (`all: true`):**
- Returns a list of lists: `[[fullMatch, g1, g2], [fullMatch, g1, g2], ...]`
- Returns `nil` if no matches found

### Examples

```pawscript
# Find first number
found: {regex_find "abc123def456", ([0-9]+)}
echo {~found 0}                          # "123"

# Find all numbers
allFound: {regex_find "abc123def456", ([0-9]+), all: true}
echo {len ~allFound}                     # 2
echo {~allFound 0 0}                     # "123"
echo {~allFound 1 0}                     # "456"

# Capture groups
dateMatch: {regex_find "Date: 2024-11-29", (([0-9]{4})-([0-9]{2})-([0-9]{2}))}
echo {~dateMatch 0}                      # "2024-11-29" (full match)
echo {~dateMatch 1}                      # "2024" (year)
echo {~dateMatch 2}                      # "11" (month)
echo {~dateMatch 3}                      # "29" (day)

# Named capture groups (Go RE2 syntax)
match: {regex_find "John Smith", ((?P<first>\w+) (?P<last>\w+))}
echo {~match 0}                          # "John Smith"
echo {~match 1}                          # "John"
echo {~match 2}                          # "Smith"

# No match returns nil
noMatch: {regex_find "abc", ([0-9]+)}
echo {type noMatch}                      # nil
```

---

## regex_replace

Replaces regex matches with a replacement string.

### Syntax
```pawscript
regex_replace <input>, <pattern>, <replacement> [, count: N] [, case_insensitive: true]
```

### Arguments

| Argument | Type | Description |
|----------|------|-------------|
| `input` | string/bytes/block/symbol | Text to modify |
| `pattern` | string/block/symbol | Regex pattern |
| `replacement` | string/block/symbol | Replacement text |

### Named Arguments

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `count` | int | `0` | Number of replacements (see below) |
| `case_insensitive` | bool | `false` | Ignore case when matching |

**Count values:**
- `0` or omitted: Replace all matches
- Positive N: Replace first N matches
- Negative N: Replace last N matches

### Replacement Syntax

The replacement string supports backreferences:

| Syntax | Description |
|--------|-------------|
| `$0` | Entire match |
| `$1`, `$2`, ... | Capture group by number |
| `${name}` | Named capture group |
| `$$` | Literal `$` character |

### Returns

Modified string/bytes/block (same type as input).

### Examples

```pawscript
# Basic replacement
{regex_replace "hello world", (world), "universe"}   # "hello universe"

# Replace pattern matches
{regex_replace "cat bat rat", ([cbr]at), "pet"}      # "pet pet pet"

# Replace first N matches
text: "aaa bbb aaa ccc aaa"
{regex_replace ~text, (aaa), "XXX", count: 2}        # "XXX bbb XXX ccc aaa"

# Replace last N matches
{regex_replace ~text, (aaa), "XXX", count: -1}       # "aaa bbb aaa ccc XXX"

# Backreferences - swap first and last name
{regex_replace "John Smith", ((\w+) (\w+)), "$2, $1"}  # "Smith, John"

# Named groups
{regex_replace "2024-11-29", ((?P<y>\d+)-(?P<m>\d+)-(?P<d>\d+)), "${d}/${m}/${y}"}
# Returns: "29/11/2024"

# Case-insensitive replacement
{regex_replace "Hello HELLO hello", (hello), "hi", case_insensitive: true}
# Returns: "hi hi hi"

# Preserve input type
myBytes: {bytes "abc123def"}
result: {regex_replace ~myBytes, ([0-9]+), "NUM"}
echo {type result}                       # bytes

myBlock: (text with 42 numbers)
result: {regex_replace ~myBlock, ([0-9]+), "N"}
echo {type result}                       # block
```

---

## Pattern Syntax Reference

PawScript uses Go's RE2 regex syntax. Common patterns:

| Pattern | Description |
|---------|-------------|
| `.` | Any character except newline |
| `\d` | Digit `[0-9]` |
| `\D` | Non-digit |
| `\w` | Word character `[A-Za-z0-9_]` |
| `\W` | Non-word character |
| `\s` | Whitespace |
| `\S` | Non-whitespace |
| `^` | Start of string |
| `$` | End of string |
| `*` | Zero or more |
| `+` | One or more |
| `?` | Zero or one |
| `{n}` | Exactly n times |
| `{n,m}` | Between n and m times |
| `[abc]` | Character class |
| `[^abc]` | Negated character class |
| `(...)` | Capture group |
| `(?:...)` | Non-capturing group |
| `(?P<name>...)` | Named capture group |
| `(?i)` | Case-insensitive flag |
| `\|` | Alternation (or) |

**Note**: RE2 does not support backreferences in patterns (only in replacements),
lookahead, or lookbehind assertions.

---

## Error Handling

Invalid regex patterns will log an error and return:
- `match`: returns `false`
- `regex_find`: returns `nil`
- `regex_replace`: returns `nil`

```pawscript
# Invalid pattern (unbalanced parens)
result: {match "test", ([)}
# Logs error: Invalid regex pattern: ...
# Returns: false
```
