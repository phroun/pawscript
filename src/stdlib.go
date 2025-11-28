package pawscript

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// formatListForDisplay formats a StoredList as a ParenGroup-like representation
func formatListForDisplay(list StoredList) string {
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
				valueStr = formatListForDisplay(v)
			case ParenGroup:
				valueStr = "(" + string(v) + ")"
			case QuotedString:
				escaped := strings.ReplaceAll(string(v), "\\", "\\\\")
				escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
				valueStr = "\"" + escaped + "\""
			case Symbol:
				valueStr = string(v)
			case string:
				escaped := strings.ReplaceAll(v, "\\", "\\\\")
				escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
				valueStr = "\"" + escaped + "\""
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
		case StoredList:
			// Recursively format nested lists
			parts = append(parts, formatListForDisplay(v))
		case ParenGroup:
			parts = append(parts, "("+string(v)+")")
		case QuotedString:
			// Escape internal quotes
			escaped := strings.ReplaceAll(string(v), "\\", "\\\\")
			escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
			parts = append(parts, "\""+escaped+"\"")
		case Symbol:
			parts = append(parts, string(v))
		case string:
			// Regular strings get quoted
			escaped := strings.ReplaceAll(v, "\\", "\\\\")
			escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
			parts = append(parts, "\""+escaped+"\"")
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
		return formatListForDisplay(v)
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
// Uses default OS-backed IO channels (stdin, stdout, stderr)
func (ps *PawScript) RegisterStandardLibrary(scriptArgs []string) {
	ps.RegisterStandardLibraryWithIO(scriptArgs, nil)
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
	ps.RegisterMathLib()             // math::, cmp::
	ps.RegisterTypesLib()            // strlist::, str::
	ps.RegisterSystemLib(scriptArgs) // os::, io::, sys::
	ps.RegisterChannelsLib()         // channels::
	ps.RegisterFibersLib()           // fibers::
	ps.RegisterGeneratorLib()        // coroutines::

	// Copy commands from LibraryInherited to CommandRegistryInherited for direct access
	ps.rootModuleEnv.PopulateDefaultImports()

	// Populate IO module with native stdin/stdout/stderr/stdio channels
	// Uses custom channels from ioConfig if provided
	// Pass executor so channels get stored with proper IDs
	ps.rootModuleEnv.PopulateIOModule(ioConfig, ps.executor)

	// Populate OS module with script arguments as #args
	ps.rootModuleEnv.PopulateOSModule(scriptArgs)
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

// toNumber converts values to numbers
func toNumber(val interface{}) (float64, bool) {
	switch v := val.(type) {
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case int:
		return float64(v), true
	case Symbol:
		// Try to parse symbol as number
		str := string(v)
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
	case StoredList:
		return "list"
	case StoredString:
		return "string"
	case StoredBlock:
		return "block"
	case ParenGroup:
		return "block"
	case QuotedString:
		// QuotedString is still a string type, just with different formatting
		return "string"
	case Symbol:
		str := string(v)
		// Check for undefined marker
		if str == UndefinedMarker {
			return "undefined"
		}
		// Check for token marker (special case - token IDs are strings, not ints)
		if strings.HasPrefix(str, "\x00TOKEN:") && strings.HasSuffix(str, "\x00") {
			return "token"
		}
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
		// Check for token marker (special case - token IDs are strings, not ints)
		if strings.HasPrefix(v, "\x00TOKEN:") && strings.HasSuffix(v, "\x00") {
			return "token"
		}
		// Check if it's an object marker
		if objType, objID := parseObjectMarker(v); objID >= 0 {
			return objType
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
