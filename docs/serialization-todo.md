# Future Serialization Commands

Notes for implementing additional serialization formats based on the existing `{json}` implementation.

## Current Implementation Reference

The `{json}` command in `lib_core.go` provides a template for serialization:

1. **Input**: Takes a `StoredList` argument
2. **Serializability Check**: Uses `list.ArrSerializable()` and `list.MapSerializable()`
3. **Mode System**: Supports modes (explicit, merge, named, array, array_1) for handling mixed positional/named args
4. **Children Key**: Custom property name for positional items when serializing to object format
5. **Value Conversion**: `toJSONValue()` recursive function handles type conversion
6. **Output Options**: `pretty: true` for formatted output, `color: true` for ANSI-colored output

## Planned Commands

### {yaml} Command

**Format**: YAML 1.2

**Implementation Notes**:
- Use `gopkg.in/yaml.v3` for serialization
- Maps naturally to PawScript lists (named args = map keys, positional args = sequence items)
- Consider `mode:` parameter similar to json for controlling output structure
- YAML supports anchors/aliases - could map to PawScript object references
- Multi-document support: Consider `documents: true` to output `---` separators
- Style hints: `flow: true` for compact `{key: value}` style vs block style

**Key Differences from JSON**:
- Comments could be preserved (yaml.v3 supports CommentKind)
- Multi-line strings have various styles (literal `|`, folded `>`)
- Consider `string_style:` parameter (literal, folded, quoted, plain)

**Deserialization** (`from: yaml`):
- Parse YAML, convert to Go map/slice, use same `JSONToStoredList()` approach
- Handle anchors/aliases by expanding them

### {xml} Command

**Format**: XML 1.0

**Implementation Notes**:
- Use `encoding/xml` standard library
- Requires element naming strategy since XML needs tag names
- Consider `root:` parameter for root element name (default "root")
- Named args become child elements or attributes based on mode
- Positional items need element names: Consider `item_name:` parameter (default "item")

**Mode Options**:
- `mode: elements` - All values as child elements
- `mode: attributes` - Simple values as attributes, complex as elements
- `mode: mixed` - Named args as attributes, positional as child elements

**Special Considerations**:
- Attribute values must be strings - numbers/bools need conversion
- CDATA sections for string values with special characters: `cdata: true`
- Namespace support: `xmlns:` parameter
- XML declaration: `declaration: true` for `<?xml version="1.0"?>`
- Encoding: `encoding:` parameter (default UTF-8)

**Deserialization** (`from: xml`):
- Child elements become named args
- Repeated elements with same name become positional items in a nested list
- Attributes accessible via `@attr` naming convention or separate `attributes:` list

### {toml} Command

**Format**: TOML v1.0

**Implementation Notes**:
- Use `github.com/BurntSushi/toml` (already in go.mod as indirect dependency)
- Tables map to nested lists with named args
- Arrays of tables for repeated structures
- Inline tables for compact output

**Key Considerations**:
- TOML requires all keys be strings
- DateTime type: Consider mapping to PawScript timestamp or string
- Inline tables: `inline: true` parameter
- Multi-line strings: `"""` triple quotes for long text

**Limitations**:
- TOML doesn't support mixed arrays (all elements same type)
- Root-level arrays not allowed (must use array of tables)
- May need to reject lists with positional args at root level

**Deserialization** (`from: toml`):
- Tables become lists with named args
- Arrays become lists with positional args
- Nested tables preserve hierarchy

### {inifile} Command

**Format**: INI/CFG files

**Implementation Notes**:
- Custom implementation or use `gopkg.in/ini.v1`
- Sections map to named args containing nested lists
- Key-value pairs within sections
- No standard for complex types - strings only

**Parameters**:
- `section:` for specifying section name when serializing single list
- `delimiter:` for key-value separator (default "=", some use ":")
- `comments:` include comment prefix (default ";", some use "#")
- `multiline:` support for backslash line continuation

**Structure Mapping**:
```
; From list: {list general: {list name: "App", version: "1.0"}, debug: {list level: "info"}}
[general]
name=App
version=1.0

[debug]
level=info
```

**Limitations**:
- Flat structure only (one level of nesting via sections)
- All values are strings
- No arrays (some implementations use comma-separated values)
- May need `array_sep:` parameter for splitting/joining arrays

**Deserialization** (`from: ini`):
- Each section becomes a named arg with nested list
- Global keys (before first section) in special "_global" or root level

## Common Implementation Patterns

### Type Handling

All formats need to handle these PawScript types:
- `nil` → format-specific null (null, ~, empty, etc.)
- `bool` → format-specific boolean
- `int64` → number/integer
- `float64` → number/float (watch for precision)
- `string` → format-specific string encoding
- `StoredString` → same as string (resolve marker first)
- `StoredList` → recursive serialization
- `StoredBytes` → typically base64 encoded or byte array
- `StoredBlock` → treat as string

### Error Handling

Follow json command pattern:
```go
if !list.ArrSerializable() {
    ctx.LogError(CatType, "format: list contains unserializable positional items")
    ctx.SetResult("")
    return BoolStatus(false)
}
```

### Pretty Printing

All formats should support:
- `pretty: true` for human-readable indented output
- Consistent indentation (2 spaces recommended)

### Colored Output

Consider extending color support to all formats:
- Reuse `DisplayColorConfig` from stdlib.go
- Format-specific syntax highlighting
- Strip colors when round-tripping (`stripANSIOutsideQuotes`)

## Testing Checklist

For each new format, create tests covering:
1. Simple objects (named args only)
2. Simple arrays (positional args only)
3. Mixed content with mode handling
4. Nested structures
5. All data types
6. Special characters and escaping
7. Unicode content
8. Empty structures
9. Round-trip verification
10. Long strings (exceeding StringStorageThreshold)
11. Colored output and parsing
