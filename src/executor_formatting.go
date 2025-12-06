package pawscript

import (
	"fmt"
	"strings"
	"unicode"
)

// escapeSpecialCharacters escapes special syntax characters for safe substitution
// nolint:unused // Reserved for future use
func (e *Executor) escapeSpecialCharacters(str string) string {
	// Characters that have special meaning in PawScript syntax
	// Must be escaped when substituting into command text
	const specialChars = ";&#(){}\"'\\$,! \t\n\r"

	var result strings.Builder
	for _, char := range str {
		if strings.ContainsRune(specialChars, char) {
			result.WriteRune('\\')
		}
		result.WriteRune(char)
	}
	return result.String()
}

// formatArgumentForParenGroup formats an argument for $@ substitution
// Preserves original forms for creating ParenGroup literals
// Similar to formatArgumentForList but without escaping quotes (not in string context)
// IMPORTANT: Escapes tildes to prevent tilde injection (tildes in values should not
// be interpreted as variable references)
// nolint:unused // Reserved for future use
func (e *Executor) formatArgumentForParenGroup(arg interface{}) string {
	const escapedTildePlaceholder = "\x00TILDE\x00"
	var result string

	switch v := arg.(type) {
	case ParenGroup:
		// Parenthetic group: wrap in parens, escape internal quotes/backslashes
		escaped := e.escapeQuotesAndBackslashes(string(v))
		result = "(" + escaped + ")"
	case QuotedString:
		// Quoted string: wrap with regular quotes (not escaped - not in string context)
		escaped := e.escapeQuotesAndBackslashes(string(v))
		result = "\"" + escaped + "\""
	case Symbol:
		// Symbol: use as bare identifier
		result = string(v)
	case string:
		// Bare string: use bare if safe identifier, otherwise wrap with quotes
		if e.isSafeIdentifier(v) {
			result = v
		} else {
			escaped := e.escapeQuotesAndBackslashes(v)
			result = "\"" + escaped + "\""
		}
	case int64, float64, bool:
		// Numbers and booleans as-is
		result = fmt.Sprintf("%v", v)
	default:
		// Unknown type: convert to string and wrap with quotes
		str := fmt.Sprintf("%v", v)
		escaped := e.escapeQuotesAndBackslashes(str)
		result = "\"" + escaped + "\""
	}

	// Escape tildes to prevent tilde injection
	result = strings.ReplaceAll(result, "~", escapedTildePlaceholder)
	return result
}

// formatArgumentForList formats an argument for $* substitution
// Preserves original forms but escapes quotes for string contexts
// This is used when creating comma-separated lists where structure matters
// IMPORTANT: Escapes tildes to prevent tilde injection (tildes in values should not
// be interpreted as variable references)
func (e *Executor) formatArgumentForList(arg interface{}) string {
	const escapedTildePlaceholder = "\x00TILDE\x00"
	var result string

	switch v := arg.(type) {
	case ParenGroup:
		// Parenthetic group: wrap in parens, escape internal quotes/backslashes
		escaped := e.escapeQuotesAndBackslashes(string(v))
		result = "(" + escaped + ")"
	case QuotedString:
		// Quoted string: escape and wrap with ESCAPED quotes
		// This allows it to appear inside outer quotes without breaking them
		escaped := e.escapeQuotesAndBackslashes(string(v))
		result = "\\\"" + escaped + "\\\""
	case Symbol:
		// Symbol: use as bare identifier
		result = string(v)
	case string:
		// Bare string: use bare if safe identifier, otherwise wrap with escaped quotes
		if e.isSafeIdentifier(v) {
			result = v
		} else {
			escaped := e.escapeQuotesAndBackslashes(v)
			result = "\\\"" + escaped + "\\\""
		}
	case int64, float64, bool:
		// Numbers and booleans as-is
		result = fmt.Sprintf("%v", v)
	default:
		// Unknown type: convert to string and wrap with escaped quotes
		str := fmt.Sprintf("%v", v)
		escaped := e.escapeQuotesAndBackslashes(str)
		result = "\\\"" + escaped + "\\\""
	}

	// Escape tildes to prevent tilde injection
	result = strings.ReplaceAll(result, "~", escapedTildePlaceholder)
	return result
}

// escapeQuotesAndBackslashes escapes only quotes and backslashes
// Used for preserving content inside string contexts
func (e *Executor) escapeQuotesAndBackslashes(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}

// formatArgumentForSubstitution formats an argument for $1, $2, etc. substitution
// Unwraps quotes and parentheses to get raw content
// This is used when substituting into string contexts where we don't want nesting
// IMPORTANT: Escapes tildes as \~ to prevent tilde injection (tildes in values should not
// be interpreted as variable references)
func (e *Executor) formatArgumentForSubstitution(arg interface{}) string {
	var result string
	switch v := arg.(type) {
	case ParenGroup:
		// Unwrap parentheses - just the content
		result = string(v)
	case QuotedString:
		// QuotedString needs to preserve quotes if it contains spaces or special chars
		// This ensures "YOUR NAME" stays as a single argument when substituted
		content := string(v)
		if strings.ContainsAny(content, " \t\n,(){}[]") || content == "" {
			// Preserve as quoted string - escape internal quotes and backslashes
			escaped := strings.ReplaceAll(content, `\`, `\\`)
			escaped = strings.ReplaceAll(escaped, `"`, `\"`)
			return `"` + escaped + `"`
		}
		result = content
	case Symbol:
		// Symbol as-is (bare identifier)
		result = string(v)
	case string:
		// Bare string - if it contains spaces, wrap in quotes
		if strings.ContainsAny(v, " \t\n,(){}[]") || v == "" {
			escaped := strings.ReplaceAll(v, `\`, `\\`)
			escaped = strings.ReplaceAll(escaped, `"`, `\"`)
			return `"` + escaped + `"`
		}
		result = v
	case int64, float64, bool:
		// Numbers and booleans as-is
		result = fmt.Sprintf("%v", v)
	case StoredList:
		// List object - find existing ID or store it and return a marker
		if id := e.findStoredListID(v); id >= 0 {
			return fmt.Sprintf("\x00LIST:%d\x00", id)
		}
		// Not in storage yet - store it now
		ref := e.RegisterObject(v, ObjList)
		return ref.ToMarker()
	case StoredBytes:
		// Bytes object - find existing ID or store it and return a marker
		if id := e.findStoredBytesID(v); id >= 0 {
			return fmt.Sprintf("\x00BYTES:%d\x00", id)
		}
		// Not in storage yet - store it now
		ref := e.RegisterObject(v, ObjBytes)
		return ref.ToMarker()
	case *StoredChannel:
		// Channel object - find or create a marker
		if id := e.findStoredChannelID(v); id >= 0 {
			return fmt.Sprintf("\x00CHANNEL:%d\x00", id)
		}
		// Not in storage yet (e.g., system IO channel) - store it now
		ref := e.RegisterObject(v, ObjChannel)
		return ref.ToMarker()
	case *FiberHandle:
		// Fiber handle - find or create a marker
		if id := e.findStoredFiberID(v); id >= 0 {
			return fmt.Sprintf("\x00FIBER:%d\x00", id)
		}
		// Not in storage yet - store it now
		ref := e.RegisterObject(v, ObjFiber)
		return ref.ToMarker()
	case StoredMacro:
		// Macro - store and create marker
		ref := e.RegisterObject(v, ObjMacro)
		return ref.ToMarker()
	case *StoredMacro:
		// Macro pointer - store and create marker
		ref := e.RegisterObject(*v, ObjMacro)
		return ref.ToMarker()
	case *StoredCommand:
		// Command - store and create marker
		ref := e.RegisterObject(v, ObjCommand)
		return ref.ToMarker()
	case ObjectRef:
		// ObjectRef - convert to marker format for substitution
		// This allows the reference to be resolved later during parsing
		return v.ToMarker()
	default:
		// Unknown type: convert to string
		result = fmt.Sprintf("%v", v)
	}

	// Protect tildes using two mechanisms:
	// 1. Use placeholder \x00TILDE\x00 which survives substituteTildeExpressions
	//    (for when $1 is inside double quotes in the macro body)
	// 2. Wrap in single quotes so after placeholder restoration, the result
	//    parses as a QuotedString which processArguments doesn't tilde-resolve
	//    (for when $1 is a bare token in the macro body)
	if strings.Contains(result, "~") {
		const escapedTildePlaceholder = "\x00TILDE\x00"
		// First, escape tildes with placeholder
		result = strings.ReplaceAll(result, "~", escapedTildePlaceholder)
		// Then wrap in single quotes (escape any existing single quotes first)
		escaped := strings.ReplaceAll(result, "'", "\\'")
		return "'" + escaped + "'"
	}

	return result
}

// formatArgumentForQuotedContext formats an argument for substitution inside quotes
// This extracts just the content without adding extra quotes, but escapes internal quotes/backslashes
func (e *Executor) formatArgumentForQuotedContext(arg interface{}) string {
	var content string
	switch v := arg.(type) {
	case ParenGroup:
		content = string(v)
	case QuotedString:
		content = string(v)
	case Symbol:
		content = string(v)
	case string:
		content = v
	case int64, float64, bool:
		return fmt.Sprintf("%v", v)
	case StoredList:
		// List object - find existing ID or store it and return a marker
		if id := e.findStoredListID(v); id >= 0 {
			return fmt.Sprintf("\x00LIST:%d\x00", id)
		}
		ref := e.RegisterObject(v, ObjList)
		return ref.ToMarker()
	case StoredBytes:
		// Bytes object - find existing ID or store it and return a marker
		if id := e.findStoredBytesID(v); id >= 0 {
			return fmt.Sprintf("\x00BYTES:%d\x00", id)
		}
		ref := e.RegisterObject(v, ObjBytes)
		return ref.ToMarker()
	case *StoredChannel:
		if id := e.findStoredChannelID(v); id >= 0 {
			return fmt.Sprintf("\x00CHANNEL:%d\x00", id)
		}
		ref := e.RegisterObject(v, ObjChannel)
		return ref.ToMarker()
	case *FiberHandle:
		if id := e.findStoredFiberID(v); id >= 0 {
			return fmt.Sprintf("\x00FIBER:%d\x00", id)
		}
		ref := e.RegisterObject(v, ObjFiber)
		return ref.ToMarker()
	case StoredMacro:
		ref := e.RegisterObject(v, ObjMacro)
		return ref.ToMarker()
	case *StoredMacro:
		ref := e.RegisterObject(*v, ObjMacro)
		return ref.ToMarker()
	case *StoredCommand:
		ref := e.RegisterObject(v, ObjCommand)
		return ref.ToMarker()
	default:
		content = fmt.Sprintf("%v", v)
	}

	// Escape backslashes first, then quotes to prevent breaking out of quoted context
	content = strings.ReplaceAll(content, `\`, `\\`)
	content = strings.ReplaceAll(content, `"`, `\"`)

	// Escape tildes to prevent tilde injection - use backslash escape
	// This works because findAllTildeLocations skips escape sequences (\~)
	content = strings.ReplaceAll(content, "~", `\~`)

	return content
}

// isSafeIdentifier checks if a string can be used as a bare identifier
func (e *Executor) isSafeIdentifier(s string) bool {
	// Empty or keywords are not safe
	if s == "" || s == "true" || s == "false" || s == "then" || s == "else" || s == "not" {
		return false
	}

	// Must be alphanumeric/underscore, can't start with digit
	runes := []rune(s)
	for i, r := range runes {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return false
			}
		} else {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
				return false
			}
		}
	}
	return true
}

// formatListItems formats the items of a StoredList as comma-separated values
// without the outer parentheses (for use with unescape operator ${...})
// IMPORTANT: Escapes tildes to prevent tilde injection (tildes in values should not
// be interpreted as variable references)
func (e *Executor) formatListItems(list StoredList) string {
	const escapedTildePlaceholder = "\x00TILDE\x00"
	items := list.Items()
	if len(items) == 0 {
		return ""
	}

	parts := make([]string, len(items))
	for i, item := range items {
		switch v := item.(type) {
		case StoredList:
			// Nested lists keep their parentheses
			parts[i] = formatListForDisplay(v)
		case ParenGroup:
			parts[i] = "(" + string(v) + ")"
		case QuotedString:
			// Escape internal quotes
			escaped := strings.ReplaceAll(string(v), "\\", "\\\\")
			escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
			parts[i] = "\"" + escaped + "\""
		case Symbol:
			parts[i] = string(v)
		case string:
			// Regular strings get quoted
			escaped := strings.ReplaceAll(v, "\\", "\\\\")
			escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
			parts[i] = "\"" + escaped + "\""
		case int64, float64, bool:
			parts[i] = fmt.Sprintf("%v", v)
		case nil:
			parts[i] = "nil"
		default:
			parts[i] = fmt.Sprintf("%v", v)
		}
		// Escape tildes in this item to prevent tilde injection
		parts[i] = strings.ReplaceAll(parts[i], "~", escapedTildePlaceholder)
	}

	return strings.Join(parts, ", ")
}
