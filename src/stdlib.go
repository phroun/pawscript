package pawscript

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// DisplayColorConfig holds ANSI color codes for colored output
type DisplayColorConfig struct {
	Reset   string
	Key     string
	String  string
	Int     string
	Float   string
	True    string
	False   string
	Nil     string
	Bracket string
	Colon   string
	Symbol  string
	Object  string
	Bytes   string
}

// DefaultDisplayColors returns the default color configuration
func DefaultDisplayColors() DisplayColorConfig {
	return DisplayColorConfig{
		Reset:   "\033[0m",
		Key:     "\033[36m", // Cyan for keys
		String:  "\033[32m", // Green for strings
		Int:     "\033[33m", // Yellow for integers
		Float:   "\033[93m", // Bright yellow for floats
		True:    "\033[92m", // Bright green for true
		False:   "\033[91m", // Bright red for false
		Nil:     "\033[31m", // Red for nil/null
		Bracket: "\033[37m", // White for brackets
		Colon:   "\033[90m", // Gray for colons/commas
		Symbol:  "\033[97m", // Bright white for symbols
		Object:  "\033[34m", // Blue for object references
		Bytes:   "\033[96m", // Bright cyan for bytes
	}
}

// ParseDisplayColorConfig extracts color configuration from a list, using defaults for unspecified colors
func ParseDisplayColorConfig(colorArg interface{}, executor *Executor) DisplayColorConfig {
	cfg := DefaultDisplayColors()

	// If it's just a boolean true, use all defaults
	if b, ok := colorArg.(bool); ok && b {
		return cfg
	}
	if sym, ok := colorArg.(Symbol); ok && (string(sym) == "true" || string(sym) == "1") {
		return cfg
	}
	if s, ok := colorArg.(string); ok && (s == "true" || s == "1") {
		return cfg
	}

	// Try to resolve as a list for custom colors
	var colorList StoredList
	hasColorList := false
	switch v := colorArg.(type) {
	case StoredList:
		colorList = v
		hasColorList = true
	case Symbol:
		if executor != nil {
			if resolved := executor.resolveValue(v); resolved != nil {
				if sl, ok := resolved.(StoredList); ok {
					colorList = sl
					hasColorList = true
				}
			}
		}
	}

	if hasColorList {
		namedArgs := colorList.NamedArgs()
		if namedArgs != nil {
			if v, ok := namedArgs["reset"]; ok {
				cfg.Reset = fmt.Sprintf("%v", v)
			}
			if v, ok := namedArgs["key"]; ok {
				cfg.Key = fmt.Sprintf("%v", v)
			}
			if v, ok := namedArgs["string"]; ok {
				cfg.String = fmt.Sprintf("%v", v)
			}
			if v, ok := namedArgs["int"]; ok {
				cfg.Int = fmt.Sprintf("%v", v)
			}
			if v, ok := namedArgs["float"]; ok {
				cfg.Float = fmt.Sprintf("%v", v)
			}
			if v, ok := namedArgs["true"]; ok {
				cfg.True = fmt.Sprintf("%v", v)
			}
			if v, ok := namedArgs["false"]; ok {
				cfg.False = fmt.Sprintf("%v", v)
			}
			if v, ok := namedArgs["nil"]; ok {
				cfg.Nil = fmt.Sprintf("%v", v)
			}
			if v, ok := namedArgs["bracket"]; ok {
				cfg.Bracket = fmt.Sprintf("%v", v)
			}
			if v, ok := namedArgs["colon"]; ok {
				cfg.Colon = fmt.Sprintf("%v", v)
			}
			if v, ok := namedArgs["symbol"]; ok {
				cfg.Symbol = fmt.Sprintf("%v", v)
			}
			if v, ok := namedArgs["object"]; ok {
				cfg.Object = fmt.Sprintf("%v", v)
			}
			if v, ok := namedArgs["bytes"]; ok {
				cfg.Bytes = fmt.Sprintf("%v", v)
			}
		}
	}
	return cfg
}

// stripANSIOutsideQuotes removes ANSI escape sequences (ESC...m) from a string,
// but only those that appear outside of JSON quoted string values.
// This handles colored JSON output where escape codes appear around keys/values.
func stripANSIOutsideQuotes(s string) string {
	var result strings.Builder
	inQuote := false
	i := 0
	for i < len(s) {
		ch := s[i]

		// Track quoted strings
		if ch == '"' && (i == 0 || s[i-1] != '\\') {
			inQuote = !inQuote
			result.WriteByte(ch)
			i++
			continue
		}

		// If inside a quoted string, keep everything
		if inQuote {
			result.WriteByte(ch)
			i++
			continue
		}

		// Check for ANSI escape sequence: ESC (0x1b or \033) followed by chars ending in 'm'
		if ch == 0x1b && i+1 < len(s) {
			// Skip until we find 'm' (the terminator for color/style codes)
			j := i + 1
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) && s[j] == 'm' {
				// Skip the entire escape sequence including 'm'
				i = j + 1
				continue
			}
		}

		result.WriteByte(ch)
		i++
	}
	return result.String()
}

// JSONToStoredList converts a parsed JSON value to a StoredList structure.
// - Arrays become lists with positional items only
// - Objects become lists with named args only
// - Special handling for _children (or custom childrenKey):
//   - mergeChildren == nil: omit children entirely
//   - mergeChildren == false: keep as separate named key with positional-only list
//   - mergeChildren == true (default): merge children into positional args of parent
//   - mergeChildren == int64(0): array_1 mode - index 0 object becomes named args, rest become positional
func JSONToStoredList(value interface{}, childrenKey string, mergeChildren interface{}, executor *Executor) interface{} {
	switch v := value.(type) {
	case nil:
		return nil
	case bool, float64, string:
		return v
	case []interface{}:
		// Check for merge: 0 (array_1 mode) on top-level arrays
		if mergeInt, ok := mergeChildren.(int64); ok && mergeInt == 0 && len(v) > 0 {
			// If index 0 is an object, its keys become named args, rest become positional
			if firstObj, ok := v[0].(map[string]interface{}); ok {
				namedArgs := make(map[string]interface{})
				var positionalItems []interface{}
				// Index 0 object keys become named args
				for objKey, objVal := range firstObj {
					namedArgs[objKey] = JSONToStoredList(objVal, childrenKey, mergeChildren, executor)
				}
				// Items after index 0 become positional
				for i := 1; i < len(v); i++ {
					positionalItems = append(positionalItems, JSONToStoredList(v[i], childrenKey, mergeChildren, executor))
				}
				return NewStoredListWithRefs(positionalItems, namedArgs, executor)
			}
		}
		// Default: Array -> list with positional items only
		items := make([]interface{}, len(v))
		for i, item := range v {
			items[i] = JSONToStoredList(item, childrenKey, mergeChildren, executor)
		}
		return NewStoredListWithRefs(items, nil, executor)
	case map[string]interface{}:
		// Object -> list with named args
		namedArgs := make(map[string]interface{})
		var positionalItems []interface{}

		for k, val := range v {
			if k == childrenKey {
				// Handle children based on merge setting
				if mergeChildren == nil {
					// mergeChildren is nil -> omit children entirely
					continue
				}
				if mergeBool, ok := mergeChildren.(bool); ok && !mergeBool {
					// mergeChildren is false -> keep as separate key with positional-only list
					if childArr, ok := val.([]interface{}); ok {
						childItems := make([]interface{}, len(childArr))
						for i, item := range childArr {
							childItems[i] = JSONToStoredList(item, childrenKey, mergeChildren, executor)
						}
						namedArgs[k] = NewStoredListWithRefs(childItems, nil, executor)
					} else {
						namedArgs[k] = JSONToStoredList(val, childrenKey, mergeChildren, executor)
					}
					continue
				}
				// Check for merge: 0 (array_1 mode)
				if mergeInt, ok := mergeChildren.(int64); ok && mergeInt == 0 {
					if childArr, ok := val.([]interface{}); ok && len(childArr) > 0 {
						// Index 0: if it's an object, merge its keys into parent's named args
						if firstObj, ok := childArr[0].(map[string]interface{}); ok {
							for objKey, objVal := range firstObj {
								namedArgs[objKey] = JSONToStoredList(objVal, childrenKey, mergeChildren, executor)
							}
						} else {
							// Index 0 is not an object, treat as positional
							positionalItems = append(positionalItems, JSONToStoredList(childArr[0], childrenKey, mergeChildren, executor))
						}
						// Items after index 0 become positional
						for i := 1; i < len(childArr); i++ {
							positionalItems = append(positionalItems, JSONToStoredList(childArr[i], childrenKey, mergeChildren, executor))
						}
					}
					continue
				}
				// Default (merge: true): merge children into positional args
				if childArr, ok := val.([]interface{}); ok {
					for _, item := range childArr {
						positionalItems = append(positionalItems, JSONToStoredList(item, childrenKey, mergeChildren, executor))
					}
				}
				continue
			}
			// Regular key
			namedArgs[k] = JSONToStoredList(val, childrenKey, mergeChildren, executor)
		}

		return NewStoredListWithRefs(positionalItems, namedArgs, executor)
	default:
		// Numbers from JSON are float64, convert to int64 if whole number
		if f, ok := v.(float64); ok {
			if f == float64(int64(f)) {
				return int64(f)
			}
			return f
		}
		return v
	}
}

// formatBlockForPSL formats a block's content for PSL serialization.
// Ensures the content starts with a semicolon (block indicator) so that
// when parsed back with {list}, it will remain a block and not be coerced to a list.
func formatBlockForPSL(content string) string {
	trimmed := strings.TrimLeft(content, " \t\n\r")
	if len(trimmed) > 0 && trimmed[0] == ';' {
		// Already has a leading semicolon
		return "(" + content + ")"
	}
	// Add a leading semicolon to preserve block semantics
	return "(; " + content + ")"
}

// formatListForDisplay formats a StoredList as a ParenGroup-like representation
// The optional executor parameter allows resolving ObjectRefs to their actual values.
func formatListForDisplay(list StoredList, exec ...*Executor) string {
	var e *Executor
	if len(exec) > 0 {
		e = exec[0]
	}

	var parts []string

	// First, add named arguments (key: value pairs)
	namedArgs := list.NamedArgs()
	if len(namedArgs) > 0 {
		// Get keys in sorted order for consistent output
		keys := make([]string, 0, len(namedArgs))
		for k := range namedArgs {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, key := range keys {
			value := namedArgs[key]
			var valueStr string
			switch v := value.(type) {
			case ObjectRef:
				// Handle ObjectRef - resolve if executor available
				if e != nil && v.Type == ObjList {
					if resolved, exists := e.getObject(v.ID); exists {
						if resolvedList, ok := resolved.(StoredList); ok {
							valueStr = formatListForDisplay(resolvedList, e)
						} else {
							valueStr = fmt.Sprintf("<%s %d>", v.Type.String(), v.ID)
						}
					} else {
						valueStr = fmt.Sprintf("<%s %d>", v.Type.String(), v.ID)
					}
				} else {
					valueStr = fmt.Sprintf("<%s %d>", v.Type.String(), v.ID)
				}
			case StoredList:
				valueStr = formatListForDisplay(v, e)
			case ParenGroup:
				valueStr = formatBlockForPSL(string(v))
			case StoredBlock:
				valueStr = formatBlockForPSL(string(v))
			case QuotedString:
				escaped := strings.ReplaceAll(string(v), "\\", "\\\\")
				escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
				valueStr = "\"" + escaped + "\""
			case Symbol:
				// Check if this is an object marker that should be formatted specially
				if objType, objID := parseObjectMarker(string(v)); objID >= 0 {
					valueStr = fmt.Sprintf("<%s %d>", objType, objID)
				} else {
					valueStr = string(v)
				}
			case string:
				// Check if this is an object marker that should be formatted specially
				if objType, objID := parseObjectMarker(v); objID >= 0 {
					valueStr = fmt.Sprintf("<%s %d>", objType, objID)
				} else {
					escaped := strings.ReplaceAll(v, "\\", "\\\\")
					escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
					valueStr = "\"" + escaped + "\""
				}
			case int64, float64, bool:
				valueStr = fmt.Sprintf("%v", v)
			case nil:
				valueStr = "nil"
			default:
				valueStr = fmt.Sprintf("%v", v)
			}

			// Format as "key: value"
			parts = append(parts, key+": "+valueStr)
		}
	}

	// Then, add positional items
	items := list.Items()
	for _, item := range items {
		switch v := item.(type) {
		case ObjectRef:
			// Handle ObjectRef - resolve if executor available
			if e != nil && v.Type == ObjList {
				if resolved, exists := e.getObject(v.ID); exists {
					if resolvedList, ok := resolved.(StoredList); ok {
						parts = append(parts, formatListForDisplay(resolvedList, e))
					} else {
						parts = append(parts, fmt.Sprintf("<%s %d>", v.Type.String(), v.ID))
					}
				} else {
					parts = append(parts, fmt.Sprintf("<%s %d>", v.Type.String(), v.ID))
				}
			} else {
				parts = append(parts, fmt.Sprintf("<%s %d>", v.Type.String(), v.ID))
			}
		case StoredList:
			// Recursively format nested lists
			parts = append(parts, formatListForDisplay(v, e))
		case ParenGroup:
			parts = append(parts, formatBlockForPSL(string(v)))
		case StoredBlock:
			// Display block contents in parentheses
			parts = append(parts, formatBlockForPSL(string(v)))
		case QuotedString:
			// Escape internal quotes
			escaped := strings.ReplaceAll(string(v), "\\", "\\\\")
			escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
			parts = append(parts, "\""+escaped+"\"")
		case Symbol:
			// Check if this is an object marker that should be formatted specially
			if objType, objID := parseObjectMarker(string(v)); objID >= 0 {
				parts = append(parts, fmt.Sprintf("<%s %d>", objType, objID))
			} else {
				parts = append(parts, string(v))
			}
		case string:
			// Check if this is an object marker that should be formatted specially
			if objType, objID := parseObjectMarker(v); objID >= 0 {
				parts = append(parts, fmt.Sprintf("<%s %d>", objType, objID))
			} else {
				// Regular strings get quoted
				escaped := strings.ReplaceAll(v, "\\", "\\\\")
				escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
				parts = append(parts, "\""+escaped+"\"")
			}
		case int64, float64, bool:
			parts = append(parts, fmt.Sprintf("%v", v))
		case nil:
			parts = append(parts, "nil")
		default:
			parts = append(parts, fmt.Sprintf("%v", v))
		}
	}

	if len(parts) == 0 {
		return "()"
	}

	return "(" + strings.Join(parts, ", ") + ")"
}

// formatListForDisplayPretty formats a StoredList with indentation for readability
func formatListForDisplayPretty(list StoredList, indent int) string {
	indentStr := strings.Repeat("  ", indent)
	innerIndent := strings.Repeat("  ", indent+1)
	var parts []string

	// First, add named arguments (key: value pairs)
	namedArgs := list.NamedArgs()
	if len(namedArgs) > 0 {
		// Get keys in sorted order for consistent output
		keys := make([]string, 0, len(namedArgs))
		for k := range namedArgs {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, key := range keys {
			value := namedArgs[key]
			var valueStr string
			switch v := value.(type) {
			case StoredList:
				valueStr = formatListForDisplayPretty(v, indent+1)
			case ParenGroup:
				valueStr = formatBlockForPSL(string(v))
			case StoredBlock:
				valueStr = formatBlockForPSL(string(v))
			case QuotedString:
				escaped := strings.ReplaceAll(string(v), "\\", "\\\\")
				escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
				valueStr = "\"" + escaped + "\""
			case Symbol:
				if objType, objID := parseObjectMarker(string(v)); objID >= 0 {
					valueStr = fmt.Sprintf("<%s %d>", objType, objID)
				} else {
					valueStr = string(v)
				}
			case string:
				if objType, objID := parseObjectMarker(v); objID >= 0 {
					valueStr = fmt.Sprintf("<%s %d>", objType, objID)
				} else {
					escaped := strings.ReplaceAll(v, "\\", "\\\\")
					escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
					valueStr = "\"" + escaped + "\""
				}
			case int64, float64, bool:
				valueStr = fmt.Sprintf("%v", v)
			case nil:
				valueStr = "nil"
			default:
				valueStr = fmt.Sprintf("%v", v)
			}
			parts = append(parts, key+": "+valueStr)
		}
	}

	// Then, add positional items
	items := list.Items()
	for _, item := range items {
		switch v := item.(type) {
		case StoredList:
			parts = append(parts, formatListForDisplayPretty(v, indent+1))
		case ParenGroup:
			parts = append(parts, formatBlockForPSL(string(v)))
		case StoredBlock:
			parts = append(parts, formatBlockForPSL(string(v)))
		case QuotedString:
			escaped := strings.ReplaceAll(string(v), "\\", "\\\\")
			escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
			parts = append(parts, "\""+escaped+"\"")
		case Symbol:
			if objType, objID := parseObjectMarker(string(v)); objID >= 0 {
				parts = append(parts, fmt.Sprintf("<%s %d>", objType, objID))
			} else {
				parts = append(parts, string(v))
			}
		case string:
			if objType, objID := parseObjectMarker(v); objID >= 0 {
				parts = append(parts, fmt.Sprintf("<%s %d>", objType, objID))
			} else {
				escaped := strings.ReplaceAll(v, "\\", "\\\\")
				escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
				parts = append(parts, "\""+escaped+"\"")
			}
		case int64, float64, bool:
			parts = append(parts, fmt.Sprintf("%v", v))
		case nil:
			parts = append(parts, "nil")
		default:
			parts = append(parts, fmt.Sprintf("%v", v))
		}
	}

	if len(parts) == 0 {
		return "()"
	}

	// For simple lists (no nested structures), use single-line format
	hasNested := false
	for _, item := range items {
		if _, ok := item.(StoredList); ok {
			hasNested = true
			break
		}
	}
	for _, value := range namedArgs {
		if _, ok := value.(StoredList); ok {
			hasNested = true
			break
		}
	}

	if !hasNested && len(parts) <= 3 {
		// Short simple lists stay on one line
		return "(" + strings.Join(parts, ", ") + ")"
	}

	// Multi-line format for complex lists
	var sb strings.Builder
	sb.WriteString("(\n")
	for i, part := range parts {
		sb.WriteString(innerIndent)
		sb.WriteString(part)
		if i < len(parts)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString(indentStr)
	sb.WriteString(")")
	return sb.String()
}

// formatListForDisplayColored formats a StoredList with ANSI colors for type distinction
func formatListForDisplayColored(list StoredList, indent int, pretty bool, cfg DisplayColorConfig, ps *PawScript) string {
	indentStr := ""
	innerIndent := ""
	if pretty {
		indentStr = strings.Repeat("  ", indent)
		innerIndent = strings.Repeat("  ", indent+1)
	}

	var parts []string

	// Helper to colorize a value based on its type
	var colorizeValue func(value interface{}) string
	colorizeValue = func(value interface{}) string {
		switch v := value.(type) {
		case StoredList:
			return formatListForDisplayColored(v, indent+1, pretty, cfg, ps)
		case ParenGroup:
			content := string(v)
			trimmed := strings.TrimLeft(content, " \t\n\r")
			if len(trimmed) == 0 || trimmed[0] != ';' {
				content = "; " + content
			}
			return cfg.Bracket + "(" + cfg.Reset + content + cfg.Bracket + ")" + cfg.Reset
		case StoredBlock:
			content := string(v)
			trimmed := strings.TrimLeft(content, " \t\n\r")
			if len(trimmed) == 0 || trimmed[0] != ';' {
				content = "; " + content
			}
			return cfg.Bracket + "(" + cfg.Reset + content + cfg.Bracket + ")" + cfg.Reset
		case QuotedString:
			escaped := strings.ReplaceAll(string(v), "\\", "\\\\")
			escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
			return cfg.String + "\"" + escaped + "\"" + cfg.Reset
		case Symbol:
			s := string(v)
			if objType, objID := parseObjectMarker(s); objID >= 0 {
				return cfg.Object + fmt.Sprintf("<%s %d>", objType, objID) + cfg.Reset
			}
			if s == "true" {
				return cfg.True + s + cfg.Reset
			}
			if s == "false" {
				return cfg.False + s + cfg.Reset
			}
			if s == "nil" || s == "null" {
				return cfg.Nil + s + cfg.Reset
			}
			return cfg.Symbol + s + cfg.Reset
		case string:
			if objType, objID := parseObjectMarker(v); objID >= 0 {
				return cfg.Object + fmt.Sprintf("<%s %d>", objType, objID) + cfg.Reset
			}
			escaped := strings.ReplaceAll(v, "\\", "\\\\")
			escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
			return cfg.String + "\"" + escaped + "\"" + cfg.Reset
		case int64:
			return cfg.Int + fmt.Sprintf("%d", v) + cfg.Reset
		case float64:
			return cfg.Float + strconv.FormatFloat(v, 'f', -1, 64) + cfg.Reset
		case bool:
			if v {
				return cfg.True + "true" + cfg.Reset
			}
			return cfg.False + "false" + cfg.Reset
		case nil:
			return cfg.Nil + "nil" + cfg.Reset
		case *StoredChannel:
			if ps != nil && ps.executor != nil {
				if id := ps.executor.findStoredChannelID(v); id >= 0 {
					return cfg.Object + fmt.Sprintf("<channel %d>", id) + cfg.Reset
				}
			}
			return cfg.Object + "<channel>" + cfg.Reset
		case *StoredFile:
			if ps != nil && ps.executor != nil {
				if id := ps.executor.findStoredFileID(v); id >= 0 {
					return cfg.Object + fmt.Sprintf("<file %d>", id) + cfg.Reset
				}
			}
			return cfg.Object + "<file>" + cfg.Reset
		case StoredMacro:
			return cfg.Object + "<macro>" + cfg.Reset
		case *StoredMacro:
			return cfg.Object + "<macro>" + cfg.Reset
		case *StoredCommand:
			if v.CommandName != "" {
				return cfg.Object + fmt.Sprintf("<command %s>", v.CommandName) + cfg.Reset
			}
			return cfg.Object + "<command>" + cfg.Reset
		case *FiberHandle:
			if ps != nil && ps.executor != nil {
				if id := ps.executor.findStoredFiberID(v); id >= 0 {
					return cfg.Object + fmt.Sprintf("<fiber %d>", id) + cfg.Reset
				}
			}
			return cfg.Object + "<fiber>" + cfg.Reset
		case StoredBytes:
			return cfg.Bytes + v.String() + cfg.Reset
		case StoredStruct:
			return cfg.Object + v.String() + cfg.Reset
		case ObjectRef:
			// Handle ObjectRef - format with object color, resolve lists recursively
			if ps != nil && ps.executor != nil && v.Type == ObjList {
				if resolved, exists := ps.executor.getObject(v.ID); exists {
					if resolvedList, ok := resolved.(StoredList); ok {
						return formatListForDisplayColored(resolvedList, indent+1, pretty, cfg, ps)
					}
				}
			}
			return cfg.Object + fmt.Sprintf("<%s %d>", v.Type.String(), v.ID) + cfg.Reset
		default:
			return fmt.Sprintf("%v", v)
		}
	}

	// First, add named arguments (key: value pairs)
	namedArgs := list.NamedArgs()
	if len(namedArgs) > 0 {
		keys := make([]string, 0, len(namedArgs))
		for k := range namedArgs {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, key := range keys {
			value := namedArgs[key]
			valueStr := colorizeValue(value)
			parts = append(parts, cfg.Key+key+cfg.Reset+cfg.Colon+": "+cfg.Reset+valueStr)
		}
	}

	// Then, add positional items
	items := list.Items()
	for _, item := range items {
		parts = append(parts, colorizeValue(item))
	}

	if len(parts) == 0 {
		return cfg.Bracket + "()" + cfg.Reset
	}

	if !pretty {
		return cfg.Bracket + "(" + cfg.Reset + strings.Join(parts, cfg.Colon+", "+cfg.Reset) + cfg.Bracket + ")" + cfg.Reset
	}

	// For simple lists (no nested structures), use single-line format
	hasNested := false
	for _, item := range items {
		if _, ok := item.(StoredList); ok {
			hasNested = true
			break
		}
	}
	for _, value := range namedArgs {
		if _, ok := value.(StoredList); ok {
			hasNested = true
			break
		}
	}

	if !hasNested && len(parts) <= 3 {
		return cfg.Bracket + "(" + cfg.Reset + strings.Join(parts, cfg.Colon+", "+cfg.Reset) + cfg.Bracket + ")" + cfg.Reset
	}

	// Multi-line format for complex lists
	var sb strings.Builder
	sb.WriteString(cfg.Bracket + "(\n" + cfg.Reset)
	for i, part := range parts {
		sb.WriteString(innerIndent)
		sb.WriteString(part)
		if i < len(parts)-1 {
			sb.WriteString(cfg.Colon + "," + cfg.Reset)
		}
		sb.WriteString("\n")
	}
	sb.WriteString(indentStr)
	sb.WriteString(cfg.Bracket + ")" + cfg.Reset)
	return sb.String()
}

// FormatValueColored formats any PawScript value with ANSI colors in PSL format
// This is the exported version that can be used by CLI tools
func FormatValueColored(value interface{}, pretty bool, cfg DisplayColorConfig, ps *PawScript) string {
	// Resolve any markers first
	if ps != nil {
		value = ps.ResolveValue(value)
	}

	return formatValueColoredInternal(value, 0, pretty, cfg, ps)
}

// formatValueColoredInternal is the internal recursive implementation
func formatValueColoredInternal(value interface{}, indent int, pretty bool, cfg DisplayColorConfig, ps *PawScript) string {
	switch v := value.(type) {
	case StoredList:
		return formatListForDisplayColored(v, indent, pretty, cfg, ps)
	case ParenGroup:
		content := string(v)
		trimmed := strings.TrimLeft(content, " \t\n\r")
		if len(trimmed) == 0 || trimmed[0] != ';' {
			content = "; " + content
		}
		return cfg.Bracket + "(" + cfg.Reset + content + cfg.Bracket + ")" + cfg.Reset
	case StoredBlock:
		content := string(v)
		trimmed := strings.TrimLeft(content, " \t\n\r")
		if len(trimmed) == 0 || trimmed[0] != ';' {
			content = "; " + content
		}
		return cfg.Bracket + "(" + cfg.Reset + content + cfg.Bracket + ")" + cfg.Reset
	case QuotedString:
		escaped := strings.ReplaceAll(string(v), "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
		return cfg.String + "\"" + escaped + "\"" + cfg.Reset
	case Symbol:
		s := string(v)
		// Try to resolve if it's a marker
		if ps != nil {
			resolved := ps.ResolveValue(v)
			if resolved != v {
				return formatValueColoredInternal(resolved, indent, pretty, cfg, ps)
			}
		}
		if objType, objID := parseObjectMarker(s); objID >= 0 {
			return cfg.Object + fmt.Sprintf("<%s %d>", objType, objID) + cfg.Reset
		}
		if s == "true" {
			return cfg.True + s + cfg.Reset
		}
		if s == "false" {
			return cfg.False + s + cfg.Reset
		}
		if s == "nil" || s == "null" || s == "undefined" {
			return cfg.Nil + s + cfg.Reset
		}
		return cfg.Symbol + s + cfg.Reset
	case string:
		// Try to resolve if it's a marker
		if ps != nil {
			resolved := ps.ResolveValue(Symbol(v))
			if sym, ok := resolved.(Symbol); !ok || string(sym) != v {
				return formatValueColoredInternal(resolved, indent, pretty, cfg, ps)
			}
		}
		if objType, objID := parseObjectMarker(v); objID >= 0 {
			return cfg.Object + fmt.Sprintf("<%s %d>", objType, objID) + cfg.Reset
		}
		escaped := strings.ReplaceAll(v, "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
		return cfg.String + "\"" + escaped + "\"" + cfg.Reset
	case StoredString:
		escaped := strings.ReplaceAll(string(v), "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
		return cfg.String + "\"" + escaped + "\"" + cfg.Reset
	case int64:
		return cfg.Int + fmt.Sprintf("%d", v) + cfg.Reset
	case float64:
		return cfg.Float + strconv.FormatFloat(v, 'f', -1, 64) + cfg.Reset
	case int:
		return cfg.Int + fmt.Sprintf("%d", v) + cfg.Reset
	case bool:
		if v {
			return cfg.True + "true" + cfg.Reset
		}
		return cfg.False + "false" + cfg.Reset
	case nil:
		return cfg.Nil + "nil" + cfg.Reset
	case *StoredChannel:
		if ps != nil && ps.executor != nil {
			if id := ps.executor.findStoredChannelID(v); id >= 0 {
				return cfg.Object + fmt.Sprintf("<channel %d>", id) + cfg.Reset
			}
		}
		return cfg.Object + "<channel>" + cfg.Reset
	case *StoredFile:
		if ps != nil && ps.executor != nil {
			if id := ps.executor.findStoredFileID(v); id >= 0 {
				return cfg.Object + fmt.Sprintf("<file %d>", id) + cfg.Reset
			}
		}
		return cfg.Object + "<file>" + cfg.Reset
	case StoredMacro:
		return cfg.Object + "<macro>" + cfg.Reset
	case *StoredMacro:
		return cfg.Object + "<macro>" + cfg.Reset
	case *StoredCommand:
		if v.CommandName != "" {
			return cfg.Object + fmt.Sprintf("<command %s>", v.CommandName) + cfg.Reset
		}
		return cfg.Object + "<command>" + cfg.Reset
	case *FiberHandle:
		if ps != nil && ps.executor != nil {
			if id := ps.executor.findStoredFiberID(v); id >= 0 {
				return cfg.Object + fmt.Sprintf("<fiber %d>", id) + cfg.Reset
			}
		}
		return cfg.Object + "<fiber>" + cfg.Reset
	case StoredBytes:
		return cfg.Bytes + v.String() + cfg.Reset
	case StoredStruct:
		return cfg.Object + v.String() + cfg.Reset
	case ObjectRef:
		// Handle ObjectRef - format with object color, resolve lists recursively
		if ps != nil && ps.executor != nil && v.Type == ObjList {
			if resolved, exists := ps.executor.getObject(v.ID); exists {
				if resolvedList, ok := resolved.(StoredList); ok {
					return formatListForDisplayColored(resolvedList, indent, pretty, cfg, ps)
				}
			}
		}
		return cfg.Object + fmt.Sprintf("<%s %d>", v.Type.String(), v.ID) + cfg.Reset
	default:
		return fmt.Sprintf("%v", v)
	}
}

// formatArgForDisplay formats any argument for display, handling StoredList specially
// Also resolves any object markers (LIST/STRING/BLOCK) before displaying
// For object types that shouldn't leak internal representation, displays as <type N>
func formatArgForDisplay(arg interface{}, executor *Executor) string {
	// Resolve any markers first (LIST/STRING/BLOCK -> actual values)
	if executor != nil {
		arg = executor.resolveValue(arg)
	}

	// Now format the resolved value
	switch v := arg.(type) {
	case StoredList:
		return formatListForDisplay(v, executor)
	case *StoredChannel:
		// Display as <channel N> to avoid leaking internal representation
		if executor != nil {
			if id := executor.findStoredChannelID(v); id >= 0 {
				return fmt.Sprintf("<channel %d>", id)
			}
		}
		return "<channel>"
	case StoredMacro:
		return "<macro>"
	case *StoredMacro:
		return "<macro>"
	case *StoredCommand:
		if v.CommandName != "" {
			return fmt.Sprintf("<command %s>", v.CommandName)
		}
		return "<command>"
	case *FiberHandle:
		// Try to find the fiber ID
		if executor != nil {
			if id := executor.findStoredFiberID(v); id >= 0 {
				return fmt.Sprintf("<fiber %d>", id)
			}
		}
		return "<fiber>"
	case *StoredFile:
		// Display as <file N> to avoid leaking internal representation
		if executor != nil {
			if id := executor.findStoredFileID(v); id >= 0 {
				return fmt.Sprintf("<file %d>", id)
			}
		}
		return "<file>"
	case StoredBytes:
		// Display as hex string with angle brackets: <08AEC7FF 0810CD00 24EE>
		return v.String()
	default:
		// Check if the original value was an unresolved marker (string or Symbol form)
		// This handles cases where the object was garbage collected or not found
		if sym, ok := arg.(Symbol); ok {
			if objType, objID := parseObjectMarker(string(sym)); objID >= 0 {
				return fmt.Sprintf("<%s %d>", objType, objID)
			}
		}
		if str, ok := arg.(string); ok {
			if objType, objID := parseObjectMarker(str); objID >= 0 {
				return fmt.Sprintf("<%s %d>", objType, objID)
			}
		}
		return fmt.Sprintf("%v", arg)
	}
}

// resolveToString resolves an argument to a string, handling markers
func resolveToString(arg interface{}, executor *Executor) string {
	if executor == nil {
		return fmt.Sprintf("%v", arg)
	}

	// Resolve any markers
	resolved := executor.resolveValue(arg)
	return fmt.Sprintf("%v", resolved)
}

// RegisterStandardLibrary registers all standard library commands
// This is the main entry point for setting up the PawScript standard library
// Uses custom IO channels from ps.config if set, otherwise defaults to OS-backed channels
func (ps *PawScript) RegisterStandardLibrary(scriptArgs []string) {
	// Create IOChannelConfig from ps.config's Stdin/Stdout/Stderr if any are set
	var ioConfig *IOChannelConfig
	if ps.config != nil && (ps.config.Stdin != nil || ps.config.Stdout != nil || ps.config.Stderr != nil) {
		ioConfig = &IOChannelConfig{
			DefaultStdin:  ps.config.Stdin,
			DefaultStdout: ps.config.Stdout,
			DefaultStderr: ps.config.Stderr,
		}
	}
	ps.RegisterStandardLibraryWithIO(scriptArgs, ioConfig)
}

// RegisterStandardLibraryWithIO registers all standard library commands with custom IO channels
// If ioConfig is nil, uses default OS-backed channels
// If specific channels in ioConfig are nil, defaults are used for those channels
//
// Example usage with custom channels:
//
//	config := &pawscript.IOChannelConfig{
//		Stdout: myCustomStdout,  // Custom stdout handler
//		// Stdin, Stderr, Stdio will use defaults
//		CustomChannels: map[string]*pawscript.StoredChannel{
//			"#mylog": myLogChannel,
//		},
//	}
//	ps.RegisterStandardLibraryWithIO(args, config)
func (ps *PawScript) RegisterStandardLibraryWithIO(scriptArgs []string, ioConfig *IOChannelConfig) {
	// Register all library modules
	ps.RegisterCoreLib()             // core::, macros::, flow::, debug::
	ps.RegisterBasicMathLib()        // basicmath::, cmp::
	ps.RegisterTypesLib()            // strlist::, str::
	ps.RegisterSystemLib(scriptArgs) // os::, io::, sys::
	ps.RegisterChannelsLib()         // channels::
	ps.RegisterFibersLib()           // fibers::
	ps.RegisterGeneratorLib()        // coroutines::

	// Copy commands from LibraryInherited to CommandRegistryInherited for direct access
	ps.rootModuleEnv.PopulateDefaultImports()

	// Register auxiliary libraries AFTER PopulateDefaultImports
	// These are available via IMPORT but not auto-imported
	ps.RegisterMathLib()    // math:: (trig functions, constants)
	ps.RegisterFilesLib()   // files:: (file system operations)
	ps.RegisterBitwiseLib() // bitwise:: (bitwise operations)

	// Populate IO module with native stdin/stdout/stderr/stdio channels
	// Uses custom channels from ioConfig if provided
	// Pass executor so channels get stored with proper IDs
	ps.rootModuleEnv.PopulateIOModule(ioConfig, ps.executor)

	// Populate OS module with script arguments as #args
	scriptDir := ""
	if ps.config != nil {
		scriptDir = ps.config.ScriptDir
	}
	ps.rootModuleEnv.PopulateOSModule(scriptArgs, scriptDir)
}

// estimateObjectSize provides a rough estimate of object size in bytes
func estimateObjectSize(value interface{}) int {
	switch v := value.(type) {
	case StoredList:
		// Base list overhead + items
		size := 24 // slice header
		for _, item := range v.Items() {
			size += estimateItemSize(item)
		}
		return size
	case StoredString:
		return len(string(v)) + 16
	case StoredBlock:
		return len(string(v)) + 16
	default:
		return estimateItemSize(value)
	}
}

// estimateItemSize estimates size of individual items
func estimateItemSize(value interface{}) int {
	switch v := value.(type) {
	case string, QuotedString, Symbol:
		return len(fmt.Sprintf("%v", v)) + 16 // string header overhead
	case ParenGroup:
		return len(string(v)) + 16
	case int64, float64, bool:
		return 8
	case nil:
		return 0
	default:
		// Unknown type, rough estimate
		return 32
	}
}

// parseHexToInt64 parses a hex string like "0xF0A1B2" to int64
// Handles odd digit count by assuming leading zero
func parseHexToInt64(s string) (int64, bool) {
	if !strings.HasPrefix(strings.ToLower(s), "0x") {
		return 0, false
	}
	hexPart := s[2:]
	if len(hexPart) == 0 {
		return 0, false
	}
	// Odd number of digits - assume leading zero
	if len(hexPart)%2 == 1 {
		hexPart = "0" + hexPart
	}
	val, err := strconv.ParseInt(hexPart, 16, 64)
	if err != nil {
		return 0, false
	}
	return val, true
}

// parseHexToBytes parses a hex string like "0xF0A1B2" to StoredBytes
// Handles odd digit count by assuming leading zero
func parseHexToBytes(s string) (StoredBytes, bool) {
	if !strings.HasPrefix(strings.ToLower(s), "0x") {
		return StoredBytes{}, false
	}
	hexPart := strings.ToUpper(s[2:])
	if len(hexPart) == 0 {
		return StoredBytes{}, false
	}
	// Odd number of digits - assume leading zero
	if len(hexPart)%2 == 1 {
		hexPart = "0" + hexPart
	}
	// Parse each pair of hex digits
	data := make([]byte, len(hexPart)/2)
	for i := 0; i < len(hexPart); i += 2 {
		var b byte
		for j := 0; j < 2; j++ {
			c := hexPart[i+j]
			b <<= 4
			if c >= '0' && c <= '9' {
				b |= c - '0'
			} else if c >= 'A' && c <= 'F' {
				b |= c - 'A' + 10
			} else {
				return StoredBytes{}, false
			}
		}
		data[i/2] = b
	}
	return NewStoredBytes(data), true
}

// isHexLiteral checks if a string is a hex literal (starts with 0x)
func isHexLiteral(s string) bool {
	return strings.HasPrefix(strings.ToLower(s), "0x")
}

// toNumber converts values to numbers
func toNumber(val interface{}) (float64, bool) {
	switch v := val.(type) {
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case int:
		return float64(v), true
	case StoredBytes:
		// Coerce bytes to int64 (big-endian)
		return float64(v.ToInt64()), true
	case Symbol:
		// Try to parse symbol as number
		str := string(v)
		// Check for hex literal first
		if i, ok := parseHexToInt64(str); ok {
			return float64(i), true
		}
		// Try to parse as float
		if f, err := strconv.ParseFloat(str, 64); err == nil {
			return f, true
		}
		// Try to parse as int
		if i, err := strconv.ParseInt(str, 10, 64); err == nil {
			return float64(i), true
		}
		return 0, false
	case QuotedString:
		// QuotedString behaves like string for parsing
		str := string(v)
		// Check for hex literal first
		if i, ok := parseHexToInt64(str); ok {
			return float64(i), true
		}
		if f, err := strconv.ParseFloat(str, 64); err == nil {
			return f, true
		}
		if i, err := strconv.ParseInt(str, 10, 64); err == nil {
			return float64(i), true
		}
		return 0, false
	case ParenGroup:
		// ParenGroup (code block) is not a number
		return 0, false
	case string:
		// Check for hex literal first
		if i, ok := parseHexToInt64(v); ok {
			return float64(i), true
		}
		// Try to parse as float
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, true
		}
		// Try to parse as int
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return float64(i), true
		}
		return 0, false
	default:
		return 0, false
	}
}

// toInt64 converts values to int64
func toInt64(val interface{}) (int64, bool) {
	switch v := val.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		return int64(v), true
	case Symbol:
		str := string(v)
		if i, err := strconv.ParseInt(str, 10, 64); err == nil {
			return i, true
		}
		if f, err := strconv.ParseFloat(str, 64); err == nil {
			return int64(f), true
		}
		return 0, false
	case QuotedString:
		str := string(v)
		if i, err := strconv.ParseInt(str, 10, 64); err == nil {
			return i, true
		}
		if f, err := strconv.ParseFloat(str, 64); err == nil {
			return int64(f), true
		}
		return 0, false
	case string:
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i, true
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return int64(f), true
		}
		return 0, false
	default:
		return 0, false
	}
}

// toFloat64 converts values to float64
func toFloat64(val interface{}) (float64, bool) {
	switch v := val.(type) {
	case float64:
		return v, true
	case int64:
		return float64(v), true
	case int:
		return float64(v), true
	case Symbol:
		str := string(v)
		if f, err := strconv.ParseFloat(str, 64); err == nil {
			return f, true
		}
		if i, err := strconv.ParseInt(str, 10, 64); err == nil {
			return float64(i), true
		}
		return 0, false
	case QuotedString:
		str := string(v)
		if f, err := strconv.ParseFloat(str, 64); err == nil {
			return f, true
		}
		return 0, false
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, true
		}
		return 0, false
	default:
		return 0, false
	}
}

// isTruthy checks if a value is truthy (non-zero, non-empty, non-false)
func isTruthy(val interface{}) bool {
	switch v := val.(type) {
	case bool:
		return v
	case int64:
		return v != 0
	case int:
		return v != 0
	case float64:
		return v != 0
	case string:
		return v != "" && v != "0" && v != "false"
	case Symbol:
		s := string(v)
		return s != "" && s != "0" && s != "false"
	case QuotedString:
		s := string(v)
		return s != "" && s != "0" && s != "false"
	case nil:
		return false
	default:
		return true // non-nil values are truthy by default
	}
}

// toBool converts values to boolean
func toBool(val interface{}) bool {
	switch v := val.(type) {
	case bool:
		return v
	case int64:
		return v != 0
	case float64:
		return v != 0.0
	case Symbol:
		// Symbols are like non-empty strings - truthy
		// Symbol("false") shouldn't exist (parser converts to bool)
		// So any symbol is truthy if non-empty
		return string(v) != ""
	case QuotedString:
		// QuotedString behaves like string for truthiness
		// Empty string, "false", "0" are false
		lowerVal := strings.ToLower(strings.TrimSpace(string(v)))
		return lowerVal != "" &&
			lowerVal != "false" &&
			lowerVal != "0"
	case ParenGroup:
		// ParenGroup (code block) is truthy if non-empty
		return string(v) != ""
	case string:
		// Empty string, "false", "0" are false
		lowerVal := strings.ToLower(strings.TrimSpace(v))
		return lowerVal != "" &&
			lowerVal != "false" &&
			lowerVal != "0"
	case nil:
		return false
	default:
		// Non-nil unknown types are truthy
		return true
	}
}

// getTypeName returns the type name of a value
func getTypeName(val interface{}) string {
	if val == nil {
		return "nil"
	}

	switch v := val.(type) {
	case ActualUndefined:
		return "undefined"
	case StoredMacro:
		return "macro"
	case *StoredMacro:
		return "macro"
	case StoredCommand:
		return "command"
	case *StoredCommand:
		return "command"
	case *StoredChannel:
		return "channel"
	case *FiberHandle:
		return "fiber"
	case *StoredFile:
		return "file"
	case StoredList:
		// Struct definitions are just lists with __size
		return "list"
	case StoredBytes:
		return "bytes"
	case StoredStruct:
		if v.IsArray() {
			return "structarray"
		}
		return "struct"
	case StoredString:
		return "string"
	case StoredBlock:
		return "block"
	case ParenGroup:
		return "block"
	case QuotedString:
		// QuotedString is still a string type, just with different formatting
		return "string"
	case ObjectRef:
		// Object references - return type based on ObjectType
		switch v.Type {
		case ObjToken:
			return "token"
		case ObjList:
			return "list"
		case ObjBytes:
			return "bytes"
		case ObjStruct:
			return "struct"
		case ObjString:
			return "string"
		case ObjBlock:
			return "block"
		case ObjChannel:
			return "channel"
		case ObjFile:
			return "file"
		default:
			return "object"
		}
	case Symbol:
		str := string(v)
		// Check if it's an object marker - if so, return the stored type
		if objType, objID := parseObjectMarker(str); objID >= 0 {
			// Return the marker type directly (list, string, block)
			return objType
		}
		// Bare identifier (unquoted, non-keyword)
		return "symbol"
	case bool:
		return "bool"
	case int64:
		return "int"
	case int:
		return "int"
	case float64:
		return "float"
	case float32:
		return "float"
	case string:
		// Check if it's an object marker
		if objType, objID := parseObjectMarker(v); objID >= 0 {
			return fmt.Sprintf("EVIL_OBJECT<%s>", objType)
		}
		return "string"
	case TokenResult:
		return "token"
	case BoolStatus:
		// This would be unusual as an argument, but handle it
		return "bool"
	default:
		// Unknown type - return the Go type name as a fallback
		return fmt.Sprintf("unknown(%T)", v)
	}
}

// stringSliceToInterface converts a []string to []interface{}
func stringSliceToInterface(strs []string) []interface{} {
	result := make([]interface{}, len(strs))
	for i, s := range strs {
		result[i] = s
	}
	return result
}

// matchWildcard checks if a string matches a wildcard pattern (using * for any sequence)
// Supports patterns like "error_*", "*_async", "*", "prefix*suffix"
func matchWildcard(pattern, str string) bool {
	if pattern == "*" {
		return true
	}

	// Handle simple cases without wildcards
	if !strings.Contains(pattern, "*") {
		return pattern == str
	}

	// Split pattern by * and check parts match in order
	parts := strings.Split(pattern, "*")

	// If pattern starts with *, str can start with anything
	// If pattern doesn't start with *, str must start with first part
	pos := 0
	for i, part := range parts {
		if part == "" {
			continue
		}

		idx := strings.Index(str[pos:], part)
		if idx < 0 {
			return false
		}

		// If this is the first part and pattern doesn't start with *, must match at start
		if i == 0 && !strings.HasPrefix(pattern, "*") && idx != 0 {
			return false
		}

		pos += idx + len(part)
	}

	// If pattern doesn't end with *, str must end with last part
	if !strings.HasSuffix(pattern, "*") {
		lastPart := parts[len(parts)-1]
		if lastPart != "" && !strings.HasSuffix(str, lastPart) {
			return false
		}
	}

	return true
}
