package pawscript

// PSL (PawScript Serialized List) format helpers
// These functions use the existing PawScript parser and serialization,
// providing a simple interface without needing a full PawScript environment.
//
// ============================================================================
// WARNING: DO NOT MODIFY THIS FILE
// ============================================================================
// The PSL format is an established serialization format. The API defined here
// is stable. Do not add new methods to PSLMap or modify the serialization or
// parsing behavior without explicit approval.
// ============================================================================

import (
	"fmt"
	"strconv"
	"strings"
)

// PSLMap holds a map of named items from a PSL structure
// Keys are strings, values can be strings, ints, floats, bools, nil, or nested structures
type PSLMap map[string]interface{}

// PSLConfig is an alias for PSLMap (deprecated name, use PSLMap)
type PSLConfig = PSLMap

// PSLList holds an ordered list of positional items
type PSLList []interface{}

// SerializePSL serializes a PSLMap to PSL format string
// Uses the same format as PawScript's list serialization
func SerializePSL(config PSLMap) string {
	if len(config) == 0 {
		return "()"
	}

	// Convert to named args map with PawScript values
	namedArgs := make(map[string]interface{})
	for key, value := range config {
		namedArgs[key] = convertToPawValue(value)
	}

	// Create StoredList with named args and use existing serialization
	list := NewStoredListWithNamed(nil, namedArgs)
	return formatListForDisplay(list)
}

// SerializePSLPretty serializes a PSLMap to PSL format with pretty indentation
// Each named argument on its own line for readability
func SerializePSLPretty(config PSLMap) string {
	if len(config) == 0 {
		return "()"
	}

	// Convert to named args map with PawScript values
	namedArgs := make(map[string]interface{})
	for key, value := range config {
		namedArgs[key] = convertToPawValue(value)
	}

	// Create StoredList with named args and use pretty serialization
	list := NewStoredListWithNamed(nil, namedArgs)
	return formatListForDisplayPretty(list, 0)
}

// SerializePSLList serializes a PSLList to PSL format string
func SerializePSLList(list PSLList) string {
	if len(list) == 0 {
		return "()"
	}

	// Convert to items slice with PawScript values
	items := make([]interface{}, len(list))
	for i, item := range list {
		items[i] = convertToPawValue(item)
	}

	// Create StoredList and use existing serialization
	storedList := NewStoredListWithoutRefs(items)
	return formatListForDisplay(storedList)
}

// convertToPawValue converts a Go value to a PawScript value
func convertToPawValue(value interface{}) interface{} {
	switch v := value.(type) {
	case nil:
		return nil
	case bool:
		return v
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return v
	case string:
		return QuotedString(v)
	case PSLConfig:
		namedArgs := make(map[string]interface{})
		for key, val := range v {
			namedArgs[key] = convertToPawValue(val)
		}
		return NewStoredListWithNamed(nil, namedArgs)
	case PSLList:
		items := make([]interface{}, len(v))
		for i, item := range v {
			items[i] = convertToPawValue(item)
		}
		return NewStoredListWithoutRefs(items)
	default:
		return QuotedString(fmt.Sprintf("%v", v))
	}
}

// ParsePSL parses a PSL format string into a PSLMap
// Uses the existing PawScript parser
func ParsePSL(input string) (PSLMap, error) {
	input = strings.TrimSpace(input)

	if input == "" || input == "()" {
		return PSLMap{}, nil
	}

	// Remove comments before parsing (# line comments and #( )# block comments)
	parser := NewParser(input, "")
	input = parser.RemoveComments(input)
	input = strings.TrimSpace(input)

	if input == "" || input == "()" {
		return PSLMap{}, nil
	}

	// Must be wrapped in parentheses
	if !strings.HasPrefix(input, "(") || !strings.HasSuffix(input, ")") {
		return nil, fmt.Errorf("PSL must be enclosed in parentheses")
	}

	// Use the existing argument parser
	inner := input[1 : len(input)-1]
	args, namedArgs := parseArguments(inner)

	// We primarily want the named args for config
	result := PSLConfig{}
	for key, value := range namedArgs {
		result[key] = convertFromPawValue(value)
	}

	// If there are positional args but no named args, it might be a list
	// In that case, return as items under numeric keys or handle appropriately
	if len(namedArgs) == 0 && len(args) > 0 {
		for i, arg := range args {
			result[strconv.Itoa(i)] = convertFromPawValue(arg)
		}
	}

	return result, nil
}

// ParsePSLList parses a PSL format string into a PSLList
func ParsePSLList(input string) (PSLList, error) {
	input = strings.TrimSpace(input)

	if input == "" || input == "()" {
		return PSLList{}, nil
	}

	// Remove comments before parsing (# line comments and #( )# block comments)
	parser := NewParser(input, "")
	input = parser.RemoveComments(input)
	input = strings.TrimSpace(input)

	if input == "" || input == "()" {
		return PSLList{}, nil
	}

	// Must be wrapped in parentheses
	if !strings.HasPrefix(input, "(") || !strings.HasSuffix(input, ")") {
		return nil, fmt.Errorf("PSL list must be enclosed in parentheses")
	}

	// Use the existing argument parser
	inner := input[1 : len(input)-1]
	args, _ := parseArguments(inner)

	result := PSLList{}
	for _, arg := range args {
		result = append(result, convertFromPawValue(arg))
	}

	return result, nil
}

// convertFromPawValue converts a PawScript value back to a Go value
func convertFromPawValue(value interface{}) interface{} {
	switch v := value.(type) {
	case nil:
		return nil
	case bool:
		return v
	case int64:
		return v
	case float64:
		return v
	case int:
		return int64(v)
	case QuotedString:
		return string(v)
	case StoredString:
		return string(v)
	case Symbol:
		s := string(v)
		// Handle special symbols
		if s == "nil" {
			return nil
		}
		if s == "true" {
			return true
		}
		if s == "false" {
			return false
		}
		return s
	case string:
		return v
	case ParenGroup:
		// Try to parse as nested structure
		inner := string(v)
		args, namedArgs := parseArguments(inner)
		if len(namedArgs) > 0 {
			result := PSLConfig{}
			for key, val := range namedArgs {
				result[key] = convertFromPawValue(val)
			}
			return result
		}
		if len(args) > 0 {
			result := PSLList{}
			for _, arg := range args {
				result = append(result, convertFromPawValue(arg))
			}
			return result
		}
		return string(v)
	case StoredList:
		// Convert StoredList back to PSL types
		namedArgs := v.NamedArgs()
		items := v.Items()
		if len(namedArgs) > 0 {
			result := PSLConfig{}
			for key, val := range namedArgs {
				result[key] = convertFromPawValue(val)
			}
			return result
		}
		result := PSLList{}
		for _, item := range items {
			result = append(result, convertFromPawValue(item))
		}
		return result
	default:
		return fmt.Sprintf("%v", v)
	}
}

// GetString returns a string value from PSLMap, with default fallback
func (m PSLMap) GetString(key string, defaultVal string) string {
	if v, ok := m[key]; ok {
		switch val := v.(type) {
		case string:
			return val
		default:
			return fmt.Sprintf("%v", val)
		}
	}
	return defaultVal
}

// GetInt returns an int value from PSLMap, with default fallback
func (m PSLMap) GetInt(key string, defaultVal int) int {
	if v, ok := m[key]; ok {
		switch val := v.(type) {
		case int:
			return val
		case int64:
			return int(val)
		case float64:
			return int(val)
		case string:
			if i, err := strconv.Atoi(val); err == nil {
				return i
			}
		}
	}
	return defaultVal
}

// GetFloat returns a float64 value from PSLMap, with default fallback
func (m PSLMap) GetFloat(key string, defaultVal float64) float64 {
	if v, ok := m[key]; ok {
		switch val := v.(type) {
		case float64:
			return val
		case int:
			return float64(val)
		case int64:
			return float64(val)
		case string:
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				return f
			}
		}
	}
	return defaultVal
}

// GetBool returns a bool value from PSLMap, with default fallback
func (m PSLMap) GetBool(key string, defaultVal bool) bool {
	if v, ok := m[key]; ok {
		switch val := v.(type) {
		case bool:
			return val
		case string:
			return val == "true" || val == "1" || val == "yes"
		case int:
			return val != 0
		case int64:
			return val != 0
		}
	}
	return defaultVal
}

// GetItems returns the positional items from a list value, or nil if not found/not a list
func (m PSLMap) GetItems(key string) []interface{} {
	if v, ok := m[key]; ok {
		if list, ok := v.([]interface{}); ok {
			return list
		}
	}
	return nil
}

// Set sets a value in the PSLMap
func (m PSLMap) Set(key string, value interface{}) {
	m[key] = value
}
