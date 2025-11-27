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
// nolint:unused // Reserved for future use
func (e *Executor) formatArgumentForParenGroup(arg interface{}) string {
	switch v := arg.(type) {
	case ParenGroup:
		// Parenthetic group: wrap in parens, escape internal quotes/backslashes
		escaped := e.escapeQuotesAndBackslashes(string(v))
		return "(" + escaped + ")"
	case QuotedString:
		// Quoted string: wrap with regular quotes (not escaped - not in string context)
		escaped := e.escapeQuotesAndBackslashes(string(v))
		return "\"" + escaped + "\""
	case Symbol:
		// Symbol: use as bare identifier
		return string(v)
	case string:
		// Bare string: use bare if safe identifier, otherwise wrap with quotes
		if e.isSafeIdentifier(v) {
			return v
		}
		escaped := e.escapeQuotesAndBackslashes(v)
		return "\"" + escaped + "\""
	case int64, float64, bool:
		// Numbers and booleans as-is
		return fmt.Sprintf("%v", v)
	default:
		// Unknown type: convert to string and wrap with quotes
		str := fmt.Sprintf("%v", v)
		escaped := e.escapeQuotesAndBackslashes(str)
		return "\"" + escaped + "\""
	}
}

// formatArgumentForList formats an argument for $* substitution
// Preserves original forms but escapes quotes for string contexts
// This is used when creating comma-separated lists where structure matters
func (e *Executor) formatArgumentForList(arg interface{}) string {
	switch v := arg.(type) {
	case ParenGroup:
		// Parenthetic group: wrap in parens, escape internal quotes/backslashes
		escaped := e.escapeQuotesAndBackslashes(string(v))
		return "(" + escaped + ")"
	case QuotedString:
		// Quoted string: escape and wrap with ESCAPED quotes
		// This allows it to appear inside outer quotes without breaking them
		escaped := e.escapeQuotesAndBackslashes(string(v))
		return "\\\"" + escaped + "\\\""
	case Symbol:
		// Symbol: use as bare identifier
		return string(v)
	case string:
		// Bare string: use bare if safe identifier, otherwise wrap with escaped quotes
		if e.isSafeIdentifier(v) {
			return v
		}
		escaped := e.escapeQuotesAndBackslashes(v)
		return "\\\"" + escaped + "\\\""
	case int64, float64, bool:
		// Numbers and booleans as-is
		return fmt.Sprintf("%v", v)
	default:
		// Unknown type: convert to string and wrap with escaped quotes
		str := fmt.Sprintf("%v", v)
		escaped := e.escapeQuotesAndBackslashes(str)
		return "\\\"" + escaped + "\\\""
	}
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
func (e *Executor) formatArgumentForSubstitution(arg interface{}) string {
	switch v := arg.(type) {
	case ParenGroup:
		// Unwrap parentheses - just the content
		return string(v)
	case QuotedString:
		// Unwrap quotes - just the content (already unescaped)
		return string(v)
	case Symbol:
		// Symbol as-is (bare identifier)
		return string(v)
	case string:
		// Bare string as-is
		return v
	case int64, float64, bool:
		// Numbers and booleans as-is
		return fmt.Sprintf("%v", v)
	case *StoredChannel:
		// Channel object - find or create a marker
		if id := e.findStoredChannelID(v); id >= 0 {
			return fmt.Sprintf("\x00CHANNEL:%d\x00", id)
		}
		// Not in storage yet (e.g., system IO channel) - store it now
		id := e.storeObject(v, "channel")
		return fmt.Sprintf("\x00CHANNEL:%d\x00", id)
	case *FiberHandle:
		// Fiber handle - find or create a marker
		if id := e.findStoredFiberID(v); id >= 0 {
			return fmt.Sprintf("\x00FIBER:%d\x00", id)
		}
		// Not in storage yet - store it now
		id := e.storeObject(v, "fiber")
		return fmt.Sprintf("\x00FIBER:%d\x00", id)
	case StoredMacro:
		// Macro - store and create marker
		id := e.storeObject(v, "macro")
		return fmt.Sprintf("\x00MACRO:%d\x00", id)
	case *StoredMacro:
		// Macro pointer - store and create marker
		id := e.storeObject(*v, "macro")
		return fmt.Sprintf("\x00MACRO:%d\x00", id)
	case *StoredCommand:
		// Command - store and create marker
		id := e.storeObject(v, "command")
		return fmt.Sprintf("\x00COMMAND:%d\x00", id)
	default:
		// Unknown type: convert to string
		return fmt.Sprintf("%v", v)
	}
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
func (e *Executor) formatListItems(list StoredList) string {
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
	}

	return strings.Join(parts, ", ")
}
