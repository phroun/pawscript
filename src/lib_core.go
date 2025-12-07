package pawscript

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RegisterCoreLib registers core language commands
// Modules: core, macros, flow, debug
func (ps *PawScript) RegisterCoreLib() {
	// Helper function to set a StoredList as result with proper reference counting
	// Uses the new typed constructor and ObjectRef
	// Note: RegisterObject now handles nested ref claiming for lists
	setListResult := func(ctx *Context, list StoredList) {
		// Store using RegisterObject directly since list is already constructed
		// RegisterObject claims refs for all nested items automatically
		ref := ctx.executor.RegisterObject(list, ObjList)
		ctx.state.SetResultWithoutClaim(ref)
	}

	// hasBlockIndicators checks if a string contains block indicators (;, &, |, !)
	// at the top level (outside of nested braces, parens, or quotes)
	hasBlockIndicators := func(content string) bool {
		inSingleQuote := false
		inDoubleQuote := false
		braceDepth := 0
		parenDepth := 0
		runes := []rune(content)

		for i := 0; i < len(runes); i++ {
			ch := runes[i]

			// Handle escape sequences
			if ch == '\\' && i+1 < len(runes) {
				i++ // Skip the escaped character
				continue
			}

			// Track quote state
			if !inDoubleQuote && ch == '\'' {
				inSingleQuote = !inSingleQuote
				continue
			}
			if !inSingleQuote && ch == '"' {
				inDoubleQuote = !inDoubleQuote
				continue
			}

			// Skip if inside quotes
			if inSingleQuote || inDoubleQuote {
				continue
			}

			// Track nesting
			if ch == '{' {
				braceDepth++
				continue
			}
			if ch == '}' {
				braceDepth--
				continue
			}
			if ch == '(' {
				parenDepth++
				continue
			}
			if ch == ')' {
				parenDepth--
				continue
			}

			// Check for block indicators at top level
			if braceDepth == 0 && parenDepth == 0 {
				if ch == ';' || ch == '&' || ch == '|' || ch == '!' {
					return true
				}
			}
		}
		return false
	}

	// coerceToList recursively converts ParenGroups to StoredLists
	// if they don't contain block indicators (;, &, |, !).
	//
	// This allows single-item lists like (foo) to be created without commas.
	// To force a ParenGroup to remain as a block (executable code), use a
	// semicolon after the opening paren: (; echo hello)
	//
	// Examples:
	//   (foo)           -> list with one item "foo"
	//   (foo, bar)      -> list with two items
	//   (; echo hello)  -> block (the ; is a block indicator)
	//   (echo; done)    -> block (contains ; at top level)
	//
	// Nested lists are stored and returned as ObjectRef for proper reference counting.
	var coerceToList func(arg interface{}, executor *Executor) interface{}
	coerceToList = func(arg interface{}, executor *Executor) interface{} {
		switch v := arg.(type) {
		case ParenGroup:
			content := string(v)
			// If it has block indicators (;, &, |, !), keep as ParenGroup (executable code)
			if hasBlockIndicators(content) {
				return v
			}
			// No block indicators - treat as a list and parse its contents
			_, items, namedArgs := ParseCommand("dummy " + content)
			// Recursively coerce each item
			coercedItems := make([]interface{}, len(items))
			for i, item := range items {
				coercedItems[i] = coerceToList(item, executor)
			}
			// Recursively coerce named args
			coercedNamedArgs := make(map[string]interface{})
			for k, val := range namedArgs {
				coercedNamedArgs[k] = coerceToList(val, executor)
			}
			// Create the list, store it, and return ObjectRef for proper ref counting
			newList := NewStoredListWithRefs(coercedItems, coercedNamedArgs, executor)
			ref := executor.RegisterObject(newList, ObjList)
			return ref
		case StoredList:
			// Already a list, but recursively check its contents
			items := v.Items()
			namedArgs := v.NamedArgs()
			needsCoercion := false
			// Check if any items need coercion
			for _, item := range items {
				if _, ok := item.(ParenGroup); ok {
					needsCoercion = true
					break
				}
			}
			for _, val := range namedArgs {
				if _, ok := val.(ParenGroup); ok {
					needsCoercion = true
					break
				}
			}
			if !needsCoercion {
				return v
			}
			// Coerce contents
			coercedItems := make([]interface{}, len(items))
			for i, item := range items {
				coercedItems[i] = coerceToList(item, executor)
			}
			coercedNamedArgs := make(map[string]interface{})
			for k, val := range namedArgs {
				coercedNamedArgs[k] = coerceToList(val, executor)
			}
			// Create the list, store it, and return ObjectRef for proper ref counting
			newList := NewStoredListWithRefs(coercedItems, coercedNamedArgs, executor)
			ref := executor.RegisterObject(newList, ObjList)
			return ref
		default:
			return arg
		}
	}

	// ==================== core:: module ====================

	// true - sets success state
	ps.RegisterCommandInModule("core", "true", func(ctx *Context) Result {
		return BoolStatus(true)
	})

	// false - sets error state
	ps.RegisterCommandInModule("core", "false", func(ctx *Context) Result {
		return BoolStatus(false)
	})

	// set_result - explicitly sets the result value
	ps.RegisterCommandInModule("core", "set_result", func(ctx *Context) Result {
		if len(ctx.Args) > 0 {
			ctx.SetResult(ctx.Args[0])
		} else {
			ctx.SetResult(nil)
		}
		return BoolStatus(true)
	})

	// get_result - gets the current result value and keeps it as the result
	ps.RegisterCommandInModule("core", "get_result", func(ctx *Context) Result {
		if ctx.HasResult() {
			return BoolStatus(true)
		}
		return BoolStatus(false)
	})

	// get_status - gets the previous command's status as a formal bool result
	// Returns true/false as the result, leaves status unchanged (like ret)
	ps.RegisterCommandInModule("core", "get_status", func(ctx *Context) Result {
		status := ctx.state.GetLastStatus()
		ctx.SetResult(status)
		return BoolStatus(status)
	})

	// get_substatus - gets whether the previous command's brace expressions all succeeded
	// Returns true (result and status) if no brace expressions returned false status
	// Returns false (result and status) if any brace expression returned false status
	ps.RegisterCommandInModule("core", "get_substatus", func(ctx *Context) Result {
		braceFailures := ctx.state.GetLastBraceFailureCount()
		success := braceFailures == 0
		ctx.SetResult(success)
		return BoolStatus(success)
	})

	// ret - early return from block
	ps.RegisterCommandInModule("core", "ret", func(ctx *Context) Result {
		switch len(ctx.Args) {
		case 0:
			return EarlyReturn{
				Status:    BoolStatus(ctx.state.GetLastStatus()),
				Result:    ctx.GetResult(),
				HasResult: ctx.HasResult(),
			}
		case 1:
			return EarlyReturn{
				Status:    BoolStatus(ctx.state.GetLastStatus()),
				Result:    ctx.Args[0],
				HasResult: true,
			}
		default:
			ctx.LogError(CatCommand, "Usage: ret [value]")
			return BoolStatus(false)
		}
	})

	// infer - returns the type of a value
	ps.RegisterCommandInModule("types", "infer", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.SetResult("undefined")
			return BoolStatus(true)
		}
		value := ctx.Args[0]
		// Resolve ObjectRef to get the actual stored object for correct type inference
		resolved := ctx.executor.resolveValue(value)
		typeName := getTypeName(resolved)
		ctx.SetResult(typeName)
		return BoolStatus(true)
	})

	// type - returns the type of a variable without fetching its value
	ps.RegisterCommandInModule("types", "type", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: type <variable_name>")
			ctx.SetResult("undefined")
			return BoolStatus(false)
		}

		arg := ctx.Args[0]

		// type expects a variable name (string, QuotedString, or Symbol)
		// If the argument is an already-resolved value, suggest using infer instead
		var varName string
		switch v := arg.(type) {
		case string:
			varName = v
		case QuotedString:
			varName = string(v)
		case Symbol:
			varName = string(v)
		case StoredString:
			varName = string(v)
		default:
			// Not a valid variable name - probably an already-resolved value
			ctx.LogError(CatCommand, "type expects a variable name, not a resolved value. Use {infer <value>} to get the type of an immediate or resolved value.")
			ctx.SetResult("undefined")
			return BoolStatus(false)
		}

		value, exists := ctx.state.GetVariable(varName)

		if !exists {
			ctx.SetResult("undefined")
			return BoolStatus(true)
		}

		// Resolve the value to get the actual object (for correct struct def detection)
		resolved := ctx.executor.resolveValue(value)
		typeName := getTypeName(resolved)
		ctx.SetResult(typeName)
		return BoolStatus(true)
	})

	// list - creates an immutable list from arguments
	// Options:
	//   from: json - parse first positional arg as JSON string
	//   merge: true (default) - merge _children array into positional args
	//   merge: false - keep _children as separate named key
	//   merge: nil - omit _children entirely
	//   children: "key" - use custom key instead of _children
	ps.RegisterCommandInModule("types", "list", func(ctx *Context) Result {
		// Check for from: json option
		if fromArg, hasFrom := ctx.NamedArgs["from"]; hasFrom {
			fromStr := ""
			switch v := fromArg.(type) {
			case string:
				fromStr = v
			case Symbol:
				fromStr = string(v)
			case QuotedString:
				fromStr = string(v)
			}

			if fromStr == "json" {
				// Parse JSON from first positional argument
				if len(ctx.Args) < 1 {
					ctx.LogError(CatCommand, "list from: json requires a JSON string argument")
					setListResult(ctx, NewStoredListWithoutRefs(nil))
					return BoolStatus(false)
				}

				// Get the JSON string
				jsonStr := ""
				switch v := ctx.Args[0].(type) {
				case string:
					// Check if it's an object marker
					markerType, objectID := parseObjectMarker(v)
					if markerType == "string" && objectID >= 0 {
						if obj, exists := ctx.executor.getObject(objectID); exists {
							if ss, ok := obj.(StoredString); ok {
								jsonStr = string(ss)
							} else {
								ctx.LogError(CatType, "list from: json: stored object is not a string")
								setListResult(ctx, NewStoredListWithoutRefs(nil))
								return BoolStatus(false)
							}
						} else {
							ctx.LogError(CatType, "list from: json: stored string not found")
							setListResult(ctx, NewStoredListWithoutRefs(nil))
							return BoolStatus(false)
						}
					} else {
						jsonStr = v
					}
				case Symbol:
					str := string(v)
					// Check if it's an object marker
					markerType, objectID := parseObjectMarker(str)
					if markerType == "string" && objectID >= 0 {
						if obj, exists := ctx.executor.getObject(objectID); exists {
							if ss, ok := obj.(StoredString); ok {
								jsonStr = string(ss)
							} else {
								ctx.LogError(CatType, "list from: json: stored object is not a string")
								setListResult(ctx, NewStoredListWithoutRefs(nil))
								return BoolStatus(false)
							}
						} else {
							ctx.LogError(CatType, "list from: json: stored string not found")
							setListResult(ctx, NewStoredListWithoutRefs(nil))
							return BoolStatus(false)
						}
					} else {
						jsonStr = str
					}
				case QuotedString:
					jsonStr = string(v)
				case StoredString:
					jsonStr = string(v)
				case ObjectRef:
					// ObjectRef is the preferred way to reference stored objects
					if v.Type == ObjString && v.IsValid() {
						if obj, exists := ctx.executor.getObject(v.ID); exists {
							if ss, ok := obj.(StoredString); ok {
								jsonStr = string(ss)
							} else {
								ctx.LogError(CatType, "list from: json: stored object is not a string")
								setListResult(ctx, NewStoredListWithoutRefs(nil))
								return BoolStatus(false)
							}
						} else {
							ctx.LogError(CatType, "list from: json: stored string not found")
							setListResult(ctx, NewStoredListWithoutRefs(nil))
							return BoolStatus(false)
						}
					} else {
						ctx.LogError(CatType, fmt.Sprintf("list from: json requires a string argument, got ObjectRef type %v", v.Type))
						setListResult(ctx, NewStoredListWithoutRefs(nil))
						return BoolStatus(false)
					}
				default:
					ctx.LogError(CatType, fmt.Sprintf("list from: json requires a string argument, got %T: %v", ctx.Args[0], ctx.Args[0]))
					setListResult(ctx, NewStoredListWithoutRefs(nil))
					return BoolStatus(false)
				}

				// Strip ANSI escape sequences from outside quoted strings
				jsonStr = stripANSIOutsideQuotes(jsonStr)

				// Parse JSON
				var jsonVal interface{}
				if err := json.Unmarshal([]byte(jsonStr), &jsonVal); err != nil {
					ctx.LogError(CatType, fmt.Sprintf("list from: json parse error: %v", err))
					setListResult(ctx, NewStoredListWithoutRefs(nil))
					return BoolStatus(false)
				}

				// Determine children key (default "_children")
				childrenKey := "_children"
				if ck, hasChildren := ctx.NamedArgs["children"]; hasChildren {
					switch v := ck.(type) {
					case string:
						childrenKey = v
					case Symbol:
						childrenKey = string(v)
					case QuotedString:
						childrenKey = string(v)
					}
				}

				// Determine merge behavior (default true)
				// merge: true - merge children into positional args
				// merge: false - keep children as separate named key
				// merge: nil - omit children entirely
				// merge: 0 - array_1 mode: index 0 object becomes named args, rest positional
				var mergeChildren interface{} = true
				if mergeArg, hasMerge := ctx.NamedArgs["merge"]; hasMerge {
					switch v := mergeArg.(type) {
					case bool:
						mergeChildren = v
					case nil:
						mergeChildren = nil
					case int64:
						if v == 0 {
							mergeChildren = int64(0) // array_1 mode
						} else {
							mergeChildren = true
						}
					case int:
						if v == 0 {
							mergeChildren = int64(0) // array_1 mode
						} else {
							mergeChildren = true
						}
					case Symbol:
						s := string(v)
						if s == "nil" || s == "null" {
							mergeChildren = nil
						} else if s == "0" {
							mergeChildren = int64(0) // array_1 mode
						} else if s == "false" {
							mergeChildren = false
						} else {
							mergeChildren = true
						}
					case string:
						if v == "nil" || v == "null" {
							mergeChildren = nil
						} else if v == "0" {
							mergeChildren = int64(0) // array_1 mode
						} else if v == "false" {
							mergeChildren = false
						} else {
							mergeChildren = true
						}
					}
				}

				// Convert JSON to StoredList
				result := JSONToStoredList(jsonVal, childrenKey, mergeChildren, ctx.executor)

				// Ensure result is a StoredList
				if sl, ok := result.(StoredList); ok {
					setListResult(ctx, sl)
				} else {
					// Wrap non-list result in a list
					setListResult(ctx, NewStoredListWithRefs([]interface{}{result}, nil, ctx.executor))
				}
				return BoolStatus(true)
			}
		}

		// Default behavior: create list from arguments
		// Coerce ParenGroups to nested StoredLists (unless they contain block indicators)
		coercedArgs := make([]interface{}, len(ctx.Args))
		for i, arg := range ctx.Args {
			coercedArgs[i] = coerceToList(arg, ctx.executor)
		}
		coercedNamedArgs := make(map[string]interface{})
		for k, v := range ctx.NamedArgs {
			coercedNamedArgs[k] = coerceToList(v, ctx.executor)
		}
		setListResult(ctx, NewStoredListWithRefs(coercedArgs, coercedNamedArgs, ctx.executor))
		return BoolStatus(true)
	})

	// len - returns the length of a list, string, or channel
	ps.RegisterCommandInModule("types", "len", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: len <list|string|channel>")
			ctx.SetResult(0)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		// First, resolve ObjectRef to get the actual stored object
		value = ctx.executor.resolveValue(value)

		// Helper to resolve a value (handles markers to get actual objects)
		resolveValue := func(val interface{}) interface{} {
			switch v := val.(type) {
			case Symbol:
				markerType, objectID := parseObjectMarker(string(v))
				if objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						switch markerType {
						case "channel":
							if ch, ok := obj.(*StoredChannel); ok {
								return ch
							}
						case "list":
							if list, ok := obj.(StoredList); ok {
								return list
							}
						case "string":
							if str, ok := obj.(StoredString); ok {
								return str
							}
						case "bytes":
							if bytes, ok := obj.(StoredBytes); ok {
								return bytes
							}
						case "struct":
							if s, ok := obj.(StoredStruct); ok {
								return s
							}
						// Note: structdef is now a StoredList, handled by "list" case
						}
						return obj
					}
				}
			case string:
				markerType, objectID := parseObjectMarker(v)
				if objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						switch markerType {
						case "channel":
							if ch, ok := obj.(*StoredChannel); ok {
								return ch
							}
						case "list":
							if list, ok := obj.(StoredList); ok {
								return list
							}
						case "string":
							if str, ok := obj.(StoredString); ok {
								return str
							}
						case "bytes":
							if bytes, ok := obj.(StoredBytes); ok {
								return bytes
							}
						case "struct":
							if s, ok := obj.(StoredStruct); ok {
								return s
							}
						// Note: structdef is now a StoredList, handled by "list" case
						}
						return obj
					}
				}
			}
			return val
		}

		// Check for #-prefixed symbol (resolve like tilde would)
		if sym, ok := value.(Symbol); ok {
			symStr := string(sym)
			if strings.HasPrefix(symStr, "#") {
				if localVal, exists := ctx.state.GetVariable(symStr); exists {
					value = resolveValue(localVal)
				} else if ctx.state.moduleEnv != nil {
					ctx.state.moduleEnv.mu.RLock()
					if ctx.state.moduleEnv.ObjectsModule != nil {
						if obj, exists := ctx.state.moduleEnv.ObjectsModule[symStr]; exists {
							value = resolveValue(obj)
						}
					}
					ctx.state.moduleEnv.mu.RUnlock()
				}
			} else {
				// Regular Symbol might be a marker
				value = resolveValue(value)
			}
		} else if _, ok := value.(string); ok {
			// String might be a marker
			value = resolveValue(value)
		}

		switch v := value.(type) {
		case StoredList:
			// Check for keys_only or keys parameter
			if keysOnly, exists := ctx.NamedArgs["keys_only"]; exists && isTruthy(keysOnly) {
				// Count only named keys
				namedArgs := v.NamedArgs()
				if namedArgs == nil {
					ctx.SetResult(int64(0))
				} else {
					ctx.SetResult(int64(len(namedArgs)))
				}
				return BoolStatus(true)
			}
			if keys, exists := ctx.NamedArgs["keys"]; exists && isTruthy(keys) {
				// Count both positional and named together
				namedCount := 0
				namedArgs := v.NamedArgs()
				if namedArgs != nil {
					namedCount = len(namedArgs)
				}
				ctx.SetResult(int64(v.Len() + namedCount))
				return BoolStatus(true)
			}
			// Default: count positional items only
			ctx.SetResult(int64(v.Len()))
			return BoolStatus(true)
		case StoredBytes:
			ctx.SetResult(int64(v.Len()))
			return BoolStatus(true)
		case StoredStruct:
			// For single struct, return the number of fields
			// For struct array, return the number of elements
			if v.IsArray() {
				ctx.SetResult(int64(v.Len()))
			} else {
				// Count fields from the definition list
				// Fields are named args that don't start with __
				fieldCount := 0
				if defObj, ok := ctx.executor.getObject(v.DefID()); ok {
					if defList, ok := defObj.(StoredList); ok {
						if namedArgs := defList.NamedArgs(); namedArgs != nil {
							for key := range namedArgs {
								if !strings.HasPrefix(key, "__") {
									fieldCount++
								}
							}
						}
					}
				}
				ctx.SetResult(int64(fieldCount))
			}
			return BoolStatus(true)
		case *StoredChannel:
			ctx.SetResult(int64(ChannelLen(v)))
			return BoolStatus(true)
		case StoredString:
			// LEGACY: StoredString should typically be resolved to string by resolveValue()
			// If this path is hit, it means a raw StoredString bypassed resolution
			ctx.executor.logger.WarnCat(CatVariable, "LEGACY: len received raw StoredString - should use ObjectRef")
			ctx.SetResult(int64(len(string(v))))
			return BoolStatus(true)
		case string, QuotedString, Symbol:
			resolved := ctx.executor.resolveValue(v)
			str := fmt.Sprintf("%v", resolved)
			ctx.SetResult(int64(len(str)))
			return BoolStatus(true)
		case ParenGroup:
			items, _ := parseArguments(string(v))
			ctx.SetResult(int64(len(items)))
			return BoolStatus(true)
		default:
			ctx.LogError(CatType, fmt.Sprintf("Cannot get length of type %s\n", getTypeName(v)))
			ctx.SetResult(0)
			return BoolStatus(false)
		}
	})

	// arrlen - count positional items in a list (synonym for len without named args)
	ps.RegisterCommandInModule("types", "arrlen", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: arrlen <list>")
			ctx.SetResult(0)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		// First, resolve ObjectRef to get the actual stored object
		value = ctx.executor.resolveValue(value)

		// Resolve list markers
		switch v := value.(type) {
		case Symbol:
			markerType, objectID := parseObjectMarker(string(v))
			if objectID >= 0 && markerType == "list" {
				if obj, exists := ctx.executor.getObject(objectID); exists {
					if list, ok := obj.(StoredList); ok {
						ctx.SetResult(int64(list.Len()))
						return BoolStatus(true)
					}
				}
			}
		case string:
			markerType, objectID := parseObjectMarker(v)
			if objectID >= 0 && markerType == "list" {
				if obj, exists := ctx.executor.getObject(objectID); exists {
					if list, ok := obj.(StoredList); ok {
						ctx.SetResult(int64(list.Len()))
						return BoolStatus(true)
					}
				}
			}
		case StoredList:
			ctx.SetResult(int64(v.Len()))
			return BoolStatus(true)
		}

		ctx.LogError(CatType, "arrlen requires a list argument")
		ctx.SetResult(0)
		return BoolStatus(false)
	})

	// maplen - count named keys in a list (synonym for len with keys_only: true)
	ps.RegisterCommandInModule("types", "maplen", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: maplen <list>")
			ctx.SetResult(0)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		// First, resolve ObjectRef to get the actual stored object
		value = ctx.executor.resolveValue(value)

		// Resolve list markers
		switch v := value.(type) {
		case Symbol:
			markerType, objectID := parseObjectMarker(string(v))
			if objectID >= 0 && markerType == "list" {
				if obj, exists := ctx.executor.getObject(objectID); exists {
					if list, ok := obj.(StoredList); ok {
						namedArgs := list.NamedArgs()
						if namedArgs == nil {
							ctx.SetResult(int64(0))
						} else {
							ctx.SetResult(int64(len(namedArgs)))
						}
						return BoolStatus(true)
					}
				}
			}
		case string:
			markerType, objectID := parseObjectMarker(v)
			if objectID >= 0 && markerType == "list" {
				if obj, exists := ctx.executor.getObject(objectID); exists {
					if list, ok := obj.(StoredList); ok {
						namedArgs := list.NamedArgs()
						if namedArgs == nil {
							ctx.SetResult(int64(0))
						} else {
							ctx.SetResult(int64(len(namedArgs)))
						}
						return BoolStatus(true)
					}
				}
			}
		case StoredList:
			namedArgs := v.NamedArgs()
			if namedArgs == nil {
				ctx.SetResult(int64(0))
			} else {
				ctx.SetResult(int64(len(namedArgs)))
			}
			return BoolStatus(true)
		}

		ctx.LogError(CatType, "maplen requires a list argument")
		ctx.SetResult(0)
		return BoolStatus(false)
	})

	// Helper function to resolve a list argument (shared by type info commands)
	resolveListArg := func(ctx *Context, arg interface{}) (StoredList, bool) {
		value := arg

		// First, resolve ObjectRef to get the actual stored object
		value = ctx.executor.resolveValue(value)

		// Helper to resolve a value (handles markers to get actual objects)
		resolveValue := func(val interface{}) interface{} {
			switch v := val.(type) {
			case Symbol:
				markerType, objectID := parseObjectMarker(string(v))
				if objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						if markerType == "list" {
							if list, ok := obj.(StoredList); ok {
								return list
							}
						}
						return obj
					}
				}
			case string:
				markerType, objectID := parseObjectMarker(v)
				if objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						if markerType == "list" {
							if list, ok := obj.(StoredList); ok {
								return list
							}
						}
						return obj
					}
				}
			}
			return val
		}

		// Check for #-prefixed symbol (resolve like tilde would)
		if sym, ok := value.(Symbol); ok {
			symStr := string(sym)
			if strings.HasPrefix(symStr, "#") {
				if localVal, exists := ctx.state.GetVariable(symStr); exists {
					value = resolveValue(localVal)
				} else if ctx.state.moduleEnv != nil {
					ctx.state.moduleEnv.mu.RLock()
					if ctx.state.moduleEnv.ObjectsModule != nil {
						if obj, exists := ctx.state.moduleEnv.ObjectsModule[symStr]; exists {
							value = resolveValue(obj)
						}
					}
					ctx.state.moduleEnv.mu.RUnlock()
				}
			} else {
				value = resolveValue(value)
			}
		} else if _, ok := value.(string); ok {
			value = resolveValue(value)
		}

		if list, ok := value.(StoredList); ok {
			return list, true
		}
		return StoredList{}, false
	}

	// arrtype - returns the type of positional items in a list
	ps.RegisterCommandInModule("types", "arrtype", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: arrtype <list>")
			ctx.SetResult("")
			return BoolStatus(false)
		}
		list, ok := resolveListArg(ctx, ctx.Args[0])
		if !ok {
			ctx.LogError(CatType, "arrtype requires a list argument")
			ctx.SetResult("")
			return BoolStatus(false)
		}
		ctx.SetResult(list.ArrType())
		return BoolStatus(true)
	})

	// maptype - returns the type of named argument values in a list
	ps.RegisterCommandInModule("types", "maptype", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: maptype <list>")
			ctx.SetResult("")
			return BoolStatus(false)
		}
		list, ok := resolveListArg(ctx, ctx.Args[0])
		if !ok {
			ctx.LogError(CatType, "maptype requires a list argument")
			ctx.SetResult("")
			return BoolStatus(false)
		}
		ctx.SetResult(list.MapType())
		return BoolStatus(true)
	})

	// arrsolid - returns true if positional items have no nil/undefined values
	ps.RegisterCommandInModule("types", "arrsolid", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: arrsolid <list>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		list, ok := resolveListArg(ctx, ctx.Args[0])
		if !ok {
			ctx.LogError(CatType, "arrsolid requires a list argument")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		ctx.SetResult(list.ArrSolid())
		return BoolStatus(true)
	})

	// mapsolid - returns true if named argument values have no nil/undefined values
	ps.RegisterCommandInModule("types", "mapsolid", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: mapsolid <list>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		list, ok := resolveListArg(ctx, ctx.Args[0])
		if !ok {
			ctx.LogError(CatType, "mapsolid requires a list argument")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		ctx.SetResult(list.MapSolid())
		return BoolStatus(true)
	})

	// arrser - returns true if all positional items are serializable types
	ps.RegisterCommandInModule("types", "arrser", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: arrser <list>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		list, ok := resolveListArg(ctx, ctx.Args[0])
		if !ok {
			ctx.LogError(CatType, "arrser requires a list argument")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		ctx.SetResult(list.ArrSerializable())
		return BoolStatus(true)
	})

	// mapser - returns true if all named argument values are serializable types
	ps.RegisterCommandInModule("types", "mapser", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: mapser <list>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		list, ok := resolveListArg(ctx, ctx.Args[0])
		if !ok {
			ctx.LogError(CatType, "mapser requires a list argument")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		ctx.SetResult(list.MapSerializable())
		return BoolStatus(true)
	})

	// formatJSONColored formats a Go value as colored JSON
	var formatJSONColored func(val interface{}, indent int, pretty bool, cfg DisplayColorConfig) string
	formatJSONColored = func(val interface{}, indent int, pretty bool, cfg DisplayColorConfig) string {
		indentStr := ""
		innerIndent := ""
		newline := ""
		space := ""
		if pretty {
			indentStr = strings.Repeat("  ", indent)
			innerIndent = strings.Repeat("  ", indent+1)
			newline = "\n"
			space = " "
		}

		switch v := val.(type) {
		case nil:
			return cfg.Nil + "null" + cfg.Reset
		case bool:
			if v {
				return cfg.Bool + "true" + cfg.Reset
			}
			return cfg.Bool + "false" + cfg.Reset
		case int64:
			return cfg.Number + fmt.Sprintf("%d", v) + cfg.Reset
		case int:
			return cfg.Number + fmt.Sprintf("%d", v) + cfg.Reset
		case float64:
			return cfg.Number + strconv.FormatFloat(v, 'f', -1, 64) + cfg.Reset
		case string:
			// Escape the string properly for JSON
			escaped, _ := json.Marshal(v)
			return cfg.String + string(escaped) + cfg.Reset
		case []interface{}:
			if len(v) == 0 {
				return cfg.Bracket + "[]" + cfg.Reset
			}
			var sb strings.Builder
			sb.WriteString(cfg.Bracket + "[" + cfg.Reset + newline)
			for i, item := range v {
				if pretty {
					sb.WriteString(innerIndent)
				}
				sb.WriteString(formatJSONColored(item, indent+1, pretty, cfg))
				if i < len(v)-1 {
					sb.WriteString(cfg.Colon + "," + cfg.Reset)
				}
				sb.WriteString(newline)
			}
			if pretty {
				sb.WriteString(indentStr)
			}
			sb.WriteString(cfg.Bracket + "]" + cfg.Reset)
			return sb.String()
		case map[string]interface{}:
			if len(v) == 0 {
				return cfg.Bracket + "{}" + cfg.Reset
			}
			// Sort keys for consistent output
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			var sb strings.Builder
			sb.WriteString(cfg.Bracket + "{" + cfg.Reset + newline)
			for i, k := range keys {
				if pretty {
					sb.WriteString(innerIndent)
				}
				// Key
				keyJSON, _ := json.Marshal(k)
				sb.WriteString(cfg.Key + string(keyJSON) + cfg.Reset)
				sb.WriteString(cfg.Colon + ":" + cfg.Reset + space)
				// Value
				sb.WriteString(formatJSONColored(v[k], indent+1, pretty, cfg))
				if i < len(keys)-1 {
					sb.WriteString(cfg.Colon + "," + cfg.Reset)
				}
				sb.WriteString(newline)
			}
			if pretty {
				sb.WriteString(indentStr)
			}
			sb.WriteString(cfg.Bracket + "}" + cfg.Reset)
			return sb.String()
		default:
			// Fallback - use standard JSON encoding
			encoded, err := json.Marshal(v)
			if err != nil {
				return fmt.Sprintf("%v", v)
			}
			return string(encoded)
		}
	}

	// json - serialize a list to JSON string
	// Modes: explicit, merge, named, array, array_1
	ps.RegisterCommandInModule("types", "json", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: json <list>, [mode: explicit|merge|named|array|array_1], [children: name]")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		list, ok := resolveListArg(ctx, ctx.Args[0])
		if !ok {
			ctx.LogError(CatType, "json requires a list argument")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		// Check serializability first
		if !list.ArrSerializable() {
			ctx.LogError(CatType, "json: list contains unserializable positional items")
			ctx.SetResult("")
			return BoolStatus(false)
		}
		if !list.MapSerializable() {
			ctx.LogError(CatType, "json: list contains unserializable named items")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		// Get mode (default depends on list contents)
		mode := "auto"
		if modeArg, exists := ctx.NamedArgs["mode"]; exists {
			mode = fmt.Sprintf("%v", modeArg)
		}

		// Get children property name
		childrenName := "_children"
		hasChildrenParam := false
		if childrenArg, exists := ctx.NamedArgs["children"]; exists {
			childrenName = fmt.Sprintf("%v", childrenArg)
			hasChildrenParam = true
		}

		// Declare both functions first so they can reference each other
		var toJSONValue func(val interface{}) (interface{}, error)
		var listToJSON func(l StoredList, m string, cn string, hcp bool, conv func(interface{}) (interface{}, error)) (interface{}, error)

		// Helper to convert a value to JSON-compatible form
		toJSONValue = func(val interface{}) (interface{}, error) {
			if val == nil {
				return nil, nil
			}

			// Handle markers
			switch v := val.(type) {
			case ActualUndefined:
				return nil, nil
			case ObjectRef:
				// ObjectRef is the preferred way to reference stored objects
				if v.IsValid() {
					if obj, exists := ctx.executor.getObject(v.ID); exists {
						return toJSONValue(obj)
					}
				}
				return nil, nil
			case Symbol:
				str := string(v)
				if str == "undefined" {
					return nil, nil
				}
				if str == "true" {
					return true, nil
				}
				if str == "false" {
					return false, nil
				}
				// Check for object markers
				_, objectID := parseObjectMarker(str)
				if objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						return toJSONValue(obj)
					}
				}
				return str, nil
			case string:
				_, objectID := parseObjectMarker(v)
				if objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						return toJSONValue(obj)
					}
				}
				return v, nil
			case QuotedString:
				return string(v), nil
			case int64:
				return v, nil
			case float64:
				return v, nil
			case bool:
				return v, nil
			case StoredString:
				return string(v), nil
			case StoredBlock:
				return string(v), nil
			case StoredBytes:
				// Convert to array of integers
				data := v.Data()
				arr := make([]interface{}, len(data))
				for i, b := range data {
					arr[i] = int64(b)
				}
				return arr, nil
			case StoredList:
				return listToJSON(v, mode, childrenName, hasChildrenParam, toJSONValue)
			default:
				return fmt.Sprintf("%v", v), nil
			}
		}

		// Helper to convert a list to JSON based on mode
		listToJSON = func(l StoredList, m string, cn string, hcp bool, conv func(interface{}) (interface{}, error)) (interface{}, error) {
			items := l.Items()
			namedArgs := l.NamedArgs()
			hasPositional := len(items) > 0
			hasNamed := namedArgs != nil && len(namedArgs) > 0

			// Auto-detect mode if not specified
			effectiveMode := m
			if effectiveMode == "auto" {
				if hasPositional && !hasNamed {
					effectiveMode = "array"
				} else if hasNamed && !hasPositional {
					effectiveMode = "named"
				} else if hasPositional && hasNamed {
					effectiveMode = "explicit"
				} else {
					effectiveMode = "array" // Empty list -> empty array
				}
			}

			switch effectiveMode {
			case "explicit":
				// All lists become objects, positional items go into children array
				obj := make(map[string]interface{})
				if namedArgs != nil {
					for k, v := range namedArgs {
						converted, err := conv(v)
						if err != nil {
							return nil, err
						}
						obj[k] = converted
					}
				}
				if len(items) > 0 {
					arr := make([]interface{}, len(items))
					for i, item := range items {
						converted, err := conv(item)
						if err != nil {
							return nil, err
						}
						arr[i] = converted
					}
					obj[cn] = arr
				}
				return obj, nil

			case "merge":
				// Object with positional items as numeric keys
				obj := make(map[string]interface{})
				if namedArgs != nil {
					for k, v := range namedArgs {
						converted, err := conv(v)
						if err != nil {
							return nil, err
						}
						obj[k] = converted
					}
				}
				for i, item := range items {
					key := fmt.Sprintf("%d", i)
					if _, exists := obj[key]; exists {
						return nil, fmt.Errorf("json merge mode: numeric key '%s' conflicts with named key", key)
					}
					converted, err := conv(item)
					if err != nil {
						return nil, err
					}
					obj[key] = converted
				}
				return obj, nil

			case "named":
				// Named keys take priority, positional items to children property or discarded
				obj := make(map[string]interface{})
				if namedArgs != nil {
					for k, v := range namedArgs {
						converted, err := conv(v)
						if err != nil {
							return nil, err
						}
						obj[k] = converted
					}
				}
				if hcp && len(items) > 0 {
					arr := make([]interface{}, len(items))
					for i, item := range items {
						converted, err := conv(item)
						if err != nil {
							return nil, err
						}
						arr[i] = converted
					}
					obj[cn] = arr
				}
				// If no children param, positional items are discarded
				return obj, nil

			case "array":
				// Only positional items, named discarded
				arr := make([]interface{}, len(items))
				for i, item := range items {
					converted, err := conv(item)
					if err != nil {
						return nil, err
					}
					arr[i] = converted
				}
				return arr, nil

			case "array_1":
				// Named items in element 0 as object, positional items in following elements
				arr := make([]interface{}, 0, len(items)+1)
				if namedArgs != nil && len(namedArgs) > 0 {
					obj := make(map[string]interface{})
					for k, v := range namedArgs {
						converted, err := conv(v)
						if err != nil {
							return nil, err
						}
						obj[k] = converted
					}
					arr = append(arr, obj)
				}
				for _, item := range items {
					converted, err := conv(item)
					if err != nil {
						return nil, err
					}
					arr = append(arr, converted)
				}
				return arr, nil

			default:
				return nil, fmt.Errorf("json: unknown mode '%s'", effectiveMode)
			}
		}

		// Convert list to JSON structure
		jsonVal, err := listToJSON(list, mode, childrenName, hasChildrenParam, toJSONValue)
		if err != nil {
			ctx.LogError(CatType, err.Error())
			ctx.SetResult("")
			return BoolStatus(false)
		}

		// Check for pretty parameter
		pretty := false
		if prettyArg, exists := ctx.NamedArgs["pretty"]; exists {
			switch v := prettyArg.(type) {
			case bool:
				pretty = v
			case Symbol:
				pretty = string(v) == "true" || string(v) == "1"
			case string:
				pretty = v == "true" || v == "1"
			}
		}

		// Check for color parameter - can be true or a list with color overrides
		var colorCfg *DisplayColorConfig
		if colorArg, exists := ctx.NamedArgs["color"]; exists {
			// Check if it's false/0 to explicitly disable
			isDisabled := false
			switch v := colorArg.(type) {
			case bool:
				isDisabled = !v
			case Symbol:
				s := string(v)
				isDisabled = s == "false" || s == "0"
			case string:
				isDisabled = v == "false" || v == "0"
			}
			if !isDisabled {
				cfg := ParseDisplayColorConfig(colorArg, ctx.executor)
				colorCfg = &cfg
			}
		}

		// Serialize to JSON string
		var result string
		if colorCfg != nil {
			result = formatJSONColored(jsonVal, 0, pretty, *colorCfg)
		} else {
			var jsonBytes []byte
			if pretty {
				jsonBytes, err = json.MarshalIndent(jsonVal, "", "  ")
			} else {
				jsonBytes, err = json.Marshal(jsonVal)
			}
			if err != nil {
				ctx.LogError(CatType, fmt.Sprintf("json: serialization error: %v", err))
				ctx.SetResult("")
				return BoolStatus(false)
			}
			result = string(jsonBytes)
		}

		ctx.SetResult(result)
		return BoolStatus(true)
	})

	// stack_trace - returns the current macro call stack as a list
	ps.RegisterCommandInModule("core", "stack_trace", func(ctx *Context) Result {
		macroCtx := ctx.GetMacroContext()
		if macroCtx == nil {
			// At top level, return empty list
			setListResult(ctx, NewStoredListWithoutRefs(nil))
			return BoolStatus(true)
		}

		var frames []interface{}
		for mc := macroCtx; mc != nil; mc = mc.ParentMacro {
			// Create a list for each frame with named args
			frame := NewStoredListWithNamed(nil, map[string]interface{}{
				"macro":    mc.MacroName,
				"file":     mc.InvocationFile,
				"line":     int64(mc.InvocationLine),
				"column":   int64(mc.InvocationColumn),
				"def_file": mc.DefinitionFile,
				"def_line": int64(mc.DefinitionLine),
			})
			// Store frame and get ObjectRef
			frameRef := ctx.executor.RegisterObject(frame, ObjList)
			frames = append(frames, frameRef)
		}

		setListResult(ctx, NewStoredListWithRefs(frames, nil, ctx.executor))
		return BoolStatus(true)
	})

	// bubble - add a bubble to the bubble map
	// Usage: bubble flavor, content [, trace [, memo]]
	//        bubble (flavor1, flavor2, ...), content [, trace [, memo]]
	// flavor: string key for categorizing bubbles (or list/paren group of flavors)
	// content: any PawScript value
	// trace: boolean (default true) - whether to include stack trace
	// memo: optional string memo
	// When multiple flavors are provided, the SAME bubble entry is added to all flavor lists
	ps.RegisterCommandInModule("core", "bubble", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "bubble requires at least 2 arguments: flavor(s), content")
			return BoolStatus(false)
		}

		// Get flavor(s) - can be single string, list, or parenthetic group
		var flavors []string
		flavorArg := ctx.Args[0]

		// Check if it's a stored list (either as Symbol marker or resolved StoredList)
		if storedList, ok := flavorArg.(StoredList); ok {
			for _, item := range storedList.Items() {
				flavors = append(flavors, fmt.Sprintf("%v", item))
			}
		} else if sym, ok := flavorArg.(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "list" && objectID >= 0 {
				if obj, exists := ctx.executor.getObject(objectID); exists {
					if storedList, ok := obj.(StoredList); ok {
						for _, item := range storedList.Items() {
							flavors = append(flavors, fmt.Sprintf("%v", item))
						}
					}
				}
			}
		} else if str, ok := flavorArg.(string); ok {
			// Check if it's a marker string
			markerType, objectID := parseObjectMarker(str)
			if markerType == "list" && objectID >= 0 {
				if obj, exists := ctx.executor.getObject(objectID); exists {
					if storedList, ok := obj.(StoredList); ok {
						for _, item := range storedList.Items() {
							flavors = append(flavors, fmt.Sprintf("%v", item))
						}
					}
				}
			}
		}

		// Check if it's a paren group
		if pg, ok := flavorArg.(ParenGroup); ok {
			// Parse the paren group contents as comma-separated values
			items, _ := parseArguments(string(pg))
			for _, item := range items {
				flavors = append(flavors, fmt.Sprintf("%v", item))
			}
		}

		// If no flavors extracted yet, treat as single flavor
		if len(flavors) == 0 {
			flavors = []string{fmt.Sprintf("%v", flavorArg)}
		}

		// Get content
		content := ctx.Args[1]

		// Get trace (default true)
		trace := true
		if len(ctx.Args) >= 3 {
			switch v := ctx.Args[2].(type) {
			case bool:
				trace = v
			case Symbol:
				trace = string(v) != "false" && string(v) != "0"
			case string:
				trace = v != "false" && v != "0"
			default:
				trace = true
			}
		}

		// Get memo (default empty)
		memo := ""
		if len(ctx.Args) >= 4 {
			memo = fmt.Sprintf("%v", ctx.Args[3])
		}

		// Claim reference if content is a stored object
		if sym, ok := content.(Symbol); ok {
			_, objectID := parseObjectMarker(string(sym))
			if objectID >= 0 {
				ctx.state.ClaimObjectReference(objectID)
			}
		}

		// Add bubble to all flavors (same entry shared across all)
		ctx.state.AddBubbleMultiFlavor(flavors, content, trace, memo)
		return BoolStatus(true)
	})

	// bubble_dump - debug command to dump the bubble map contents
	ps.RegisterCommandInModule("debug", "bubble_dump", func(ctx *Context) Result {
		stderr := ps.logger.GetStderr()
		bubbleMap := ctx.state.GetBubbleMap()
		if len(bubbleMap) == 0 {
			fmt.Fprintln(stderr, "[bubble_dump] No bubbles")
			return BoolStatus(true)
		}

		fmt.Fprintln(stderr, "[bubble_dump] Bubble map contents:")
		for flavor, entries := range bubbleMap {
			fmt.Fprintf(stderr, "  Flavor: %s (%d entries)\n", flavor, len(entries))
			for i, entry := range entries {
				fmt.Fprintf(stderr, "    [%d] content=%v, microtime=%d, memo=%q\n",
					i, entry.Content, entry.Microtime, entry.Memo)
				if len(entry.StackTrace) > 0 {
					fmt.Fprintf(stderr, "        stack trace (%d frames):\n", len(entry.StackTrace))
					for j, frame := range entry.StackTrace {
						if frameMap, ok := frame.(map[string]interface{}); ok {
							fmt.Fprintf(stderr, "          [%d] %v at %v:%v\n",
								j, frameMap["macro"], frameMap["file"], frameMap["line"])
						}
					}
				}
			}
		}
		return BoolStatus(true)
	})

	// bubble_orphans - retrieve orphaned bubbles from abandoned fibers
	// Merges orphaned bubbles into the current context's bubbleMap and clears the orphaned map
	ps.RegisterCommandInModule("core", "bubble_orphans", func(ctx *Context) Result {
		orphaned := ctx.executor.GetOrphanedBubbles()
		if len(orphaned) == 0 {
			return BoolStatus(true)
		}

		// Merge orphaned bubbles into current context's bubbleMap
		ctx.state.mu.Lock()
		if ctx.state.bubbleMap == nil {
			ctx.state.bubbleMap = make(map[string][]*BubbleEntry)
		}
		for flavor, entries := range orphaned {
			ctx.state.bubbleMap[flavor] = append(ctx.state.bubbleMap[flavor], entries...)
		}
		ctx.state.mu.Unlock()

		// Clear the orphaned bubbles now that they've been transferred
		ctx.executor.ClearOrphanedBubbles()

		return BoolStatus(true)
	})

	// bubble_orphans_dump - debug command to dump orphaned bubbles without retrieving them
	ps.RegisterCommandInModule("debug", "bubble_orphans_dump", func(ctx *Context) Result {
		stderr := ps.logger.GetStderr()
		orphaned := ctx.executor.GetOrphanedBubbles()
		if len(orphaned) == 0 {
			fmt.Fprintln(stderr, "[bubble_orphans_dump] No orphaned bubbles")
			return BoolStatus(true)
		}

		fmt.Fprintln(stderr, "[bubble_orphans_dump] Orphaned bubble map contents:")
		for flavor, entries := range orphaned {
			fmt.Fprintf(stderr, "  Flavor: %s (%d entries)\n", flavor, len(entries))
			for i, entry := range entries {
				fmt.Fprintf(stderr, "    [%d] content=%v, microtime=%d, memo=%q\n",
					i, entry.Content, entry.Microtime, entry.Memo)
				if len(entry.StackTrace) > 0 {
					fmt.Fprintf(stderr, "        stack trace (%d frames):\n", len(entry.StackTrace))
					for j, frame := range entry.StackTrace {
						if frameMap, ok := frame.(map[string]interface{}); ok {
							fmt.Fprintf(stderr, "          [%d] %v at %v:%v\n",
								j, frameMap["macro"], frameMap["file"], frameMap["line"])
						}
					}
				}
			}
		}
		return BoolStatus(true)
	})

	// fizz - iterate over bubbles from specified flavors
	// Usage: fizz ~flavorList, contentVar, (body)
	//        fizz (flavor_a, flavor_b), contentVar, metaVar, (body)
	// Iterates over all unique bubbles from the specified flavors, sorted by microtime.
	// For each bubble, sets contentVar to the bubble's content.
	// If metaVar is provided (not a ParenGroup), creates a StoredList with bubble metadata:
	//   microtime, memo, stack_trace, flavors
	// Supports break and continue.
	// Use 'burst' inside the body to remove the current bubble from all its flavors.
	ps.RegisterCommandInModule("flow", "fizz", func(ctx *Context) Result {
		if len(ctx.Args) < 3 {
			ctx.LogError(CatCommand, "Usage: fizz <flavor(s)>, <contentVar>, [(metaVar),] (body)")
			return BoolStatus(false)
		}

		// Helper to extract code from a block argument
		extractCode := func(arg interface{}) string {
			switch v := arg.(type) {
			case ParenGroup:
				return string(v)
			case Symbol:
				markerType, objectID := parseObjectMarker(string(v))
				if markerType == "block" && objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						if storedBlock, ok := obj.(StoredBlock); ok {
							return string(storedBlock)
						}
					}
				}
				return string(v)
			default:
				return fmt.Sprintf("%v", arg)
			}
		}

		// Parse flavors from first argument
		var flavors []string
		flavorArg := ctx.Args[0]

		// Check if it's a stored list (either as Symbol marker or resolved StoredList)
		if storedList, ok := flavorArg.(StoredList); ok {
			for _, item := range storedList.Items() {
				flavors = append(flavors, fmt.Sprintf("%v", item))
			}
		} else if sym, ok := flavorArg.(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "list" && objectID >= 0 {
				if obj, exists := ctx.executor.getObject(objectID); exists {
					if storedList, ok := obj.(StoredList); ok {
						for _, item := range storedList.Items() {
							flavors = append(flavors, fmt.Sprintf("%v", item))
						}
					}
				}
			}
		} else if str, ok := flavorArg.(string); ok {
			markerType, objectID := parseObjectMarker(str)
			if markerType == "list" && objectID >= 0 {
				if obj, exists := ctx.executor.getObject(objectID); exists {
					if storedList, ok := obj.(StoredList); ok {
						for _, item := range storedList.Items() {
							flavors = append(flavors, fmt.Sprintf("%v", item))
						}
					}
				}
			}
		}

		// Check if it's a paren group for flavors
		if pg, ok := flavorArg.(ParenGroup); ok {
			items, _ := parseArguments(string(pg))
			for _, item := range items {
				flavors = append(flavors, fmt.Sprintf("%v", item))
			}
		}

		// If no flavors extracted yet, treat as single flavor
		if len(flavors) == 0 {
			flavors = []string{fmt.Sprintf("%v", flavorArg)}
		}

		// Expand wildcard patterns in flavors
		// Get all existing flavor names from the bubble map
		allFlavorNames := ctx.state.GetAllFlavorNames()
		var expandedFlavors []string
		seen := make(map[string]bool)

		for _, pattern := range flavors {
			if strings.Contains(pattern, "*") {
				// Wildcard pattern - expand against all existing flavors
				for _, name := range allFlavorNames {
					if matchWildcard(pattern, name) && !seen[name] {
						seen[name] = true
						expandedFlavors = append(expandedFlavors, name)
					}
				}
			} else {
				// Exact match
				if !seen[pattern] {
					seen[pattern] = true
					expandedFlavors = append(expandedFlavors, pattern)
				}
			}
		}
		flavors = expandedFlavors

		// Determine if we have a meta variable (3 args = no meta, 4 args = with meta)
		var contentVarName string
		var metaVarName string
		var bodyBlock string
		hasMetaVar := false

		// The body is always the last argument and must be a ParenGroup
		lastArg := ctx.Args[len(ctx.Args)-1]
		if _, ok := lastArg.(ParenGroup); !ok {
			ctx.LogError(CatCommand, "fizz: body must be a code block (parentheses)")
			return BoolStatus(false)
		}
		bodyBlock = extractCode(lastArg)

		if len(ctx.Args) == 3 {
			// fizz flavors, contentVar, (body)
			contentVarName = fmt.Sprintf("%v", ctx.Args[1])
		} else if len(ctx.Args) >= 4 {
			// Check if arg[2] is a ParenGroup (then it's the body, not a meta var)
			if _, ok := ctx.Args[2].(ParenGroup); ok {
				// fizz flavors, contentVar, (body) - arg[2] is body
				contentVarName = fmt.Sprintf("%v", ctx.Args[1])
				bodyBlock = extractCode(ctx.Args[2])
			} else {
				// fizz flavors, contentVar, metaVar, (body)
				contentVarName = fmt.Sprintf("%v", ctx.Args[1])
				metaVarName = fmt.Sprintf("%v", ctx.Args[2])
				hasMetaVar = true
			}
		}

		// Get all unique bubbles sorted by microtime
		bubbles := ctx.state.GetBubblesForFlavors(flavors)

		if len(bubbles) == 0 {
			return BoolStatus(true)
		}

		// Parse body (with caching - body is last argument)
		bodyArgIndex := len(ctx.Args) - 1
		bodyCommands, parseErr := ctx.GetOrParseBlock(bodyArgIndex, bodyBlock)
		if parseErr != "" {
			ctx.LogError(CatCommand, fmt.Sprintf("fizz: failed to parse body: %s", parseErr))
			return BoolStatus(false)
		}

		// Internal variable name to track current bubble for 'burst' command
		const currentBubbleVar = "__fizz_current_bubble__"

		// Iterate over bubbles (with index for continuation support)
		for bubbleIdx, bubble := range bubbles {
			// Set content variable
			ctx.state.SetVariable(contentVarName, bubble.Content)

			// Set meta variable if requested
			if hasMetaVar {
				// Create a StoredList with bubble metadata
				metaNamedArgs := map[string]interface{}{
					"microtime": bubble.Microtime,
					"memo":      bubble.Memo,
					"flavors":   NewStoredListWithoutRefs(stringSliceToInterface(bubble.Flavors)),
				}

				// Add stack trace if present
				if len(bubble.StackTrace) > 0 {
					// Convert stack trace to list of lists
					var traceList []interface{}
					for _, frame := range bubble.StackTrace {
						if frameMap, ok := frame.(map[string]interface{}); ok {
							frameList := NewStoredListWithNamed(nil, frameMap)
							frameRef := ctx.executor.RegisterObject(frameList, ObjList)
							traceList = append(traceList, frameRef)
						}
					}
					stackList := NewStoredListWithRefs(traceList, nil, ctx.executor)
					stackRef := ctx.executor.RegisterObject(stackList, ObjList)
					metaNamedArgs["stack_trace"] = stackRef
				}

				metaList := NewStoredListWithNamed(nil, metaNamedArgs)
				metaRef := ctx.executor.RegisterObject(metaList, ObjList)
				ctx.state.SetVariable(metaVarName, metaRef)
			}

			// Store current bubble pointer for 'burst' command
			ctx.state.mu.Lock()
			if ctx.state.variables == nil {
				ctx.state.variables = make(map[string]interface{})
			}
			ctx.state.variables[currentBubbleVar] = bubble
			ctx.state.mu.Unlock()

			// Execute body
			lastStatus := true
			for cmdIdx, cmd := range bodyCommands {
				if strings.TrimSpace(cmd.Command) == "" {
					continue
				}

				shouldExecute := true
				switch cmd.Separator {
				case "&":
					shouldExecute = lastStatus
				case "|":
					shouldExecute = !lastStatus
				}

				if !shouldExecute {
					continue
				}

				result := ctx.executor.executeParsedCommand(cmd, ctx.state, nil)

				// Check for yield - attach fizz continuation
				if yieldResult, ok := result.(YieldResult); ok {
					outerCont := &FizzContinuation{
						BodyBlock:          bodyBlock,
						CachedBodyCmds:     bodyCommands, // Cache full body for reuse
						RemainingBodyCmds:  bodyCommands[cmdIdx+1:],
						BodyCmdIndex:       cmdIdx,
						ContentVarName:     contentVarName,
						MetaVarName:        metaVarName,
						HasMetaVar:         hasMetaVar,
						Flavors:            flavors,
						CurrentBubbleIndex: bubbleIdx,
						Bubbles:            bubbles,
						State:              ctx.state,
					}
					if yieldResult.FizzContinuation == nil {
						yieldResult.FizzContinuation = outerCont
					} else {
						yieldResult.FizzContinuation.ParentContinuation = outerCont
					}
					return yieldResult
				}

				// Check for early return
				if earlyReturn, ok := result.(EarlyReturn); ok {
					// Clean up current bubble var
					ctx.state.DeleteVariable(currentBubbleVar)
					return earlyReturn
				}

				// Check for break - exit this loop
				if breakResult, ok := result.(BreakResult); ok {
					// Clean up current bubble var
					ctx.state.DeleteVariable(currentBubbleVar)
					if breakResult.Levels <= 1 {
						return BoolStatus(true)
					}
					return BreakResult{Levels: breakResult.Levels - 1}
				}

				// Check for continue - skip to next bubble
				if continueResult, ok := result.(ContinueResult); ok {
					if continueResult.Levels <= 1 {
						break // Break inner command loop, continue to next bubble
					}
					// Clean up current bubble var
					ctx.state.DeleteVariable(currentBubbleVar)
					return ContinueResult{Levels: continueResult.Levels - 1}
				}

				// Handle async
				if asyncToken, isToken := result.(TokenResult); isToken {
					tokenID := string(asyncToken)
					waitChan := make(chan ResumeData, 1)
					ctx.executor.attachWaitChan(tokenID, waitChan)
					resumeData := <-waitChan
					lastStatus = resumeData.Status
					continue
				}

				if boolRes, ok := result.(BoolStatus); ok {
					lastStatus = bool(boolRes)
				}
			}
		}

		// Clean up current bubble var
		ctx.state.DeleteVariable(currentBubbleVar)

		return BoolStatus(true)
	})

	// burst - remove the current bubble from all its flavor maps
	// Only valid inside a fizz loop
	ps.RegisterCommandInModule("flow", "burst", func(ctx *Context) Result {
		const currentBubbleVar = "__fizz_current_bubble__"

		// Get the current bubble from the internal variable
		ctx.state.mu.RLock()
		bubbleVal, exists := ctx.state.variables[currentBubbleVar]
		ctx.state.mu.RUnlock()

		if !exists {
			ctx.LogError(CatCommand, "burst: can only be used inside a fizz loop")
			return BoolStatus(false)
		}

		bubble, ok := bubbleVal.(*BubbleEntry)
		if !ok {
			ctx.LogError(CatCommand, "burst: internal error - invalid bubble reference")
			return BoolStatus(false)
		}

		// Remove the bubble from all its flavors
		ctx.state.RemoveBubble(bubble)

		return BoolStatus(true)
	})

	// ==================== macros:: module ====================

	// macro - define a macro
	ps.RegisterCommandInModule("macros", "macro", func(ctx *Context) Result {
		ps.logger.DebugCat(CatMacro,"macro command called with %d args", len(ctx.Args))

		// Capture the current module environment for lexical scoping
		macroEnv := NewMacroModuleEnvironment(ctx.state.moduleEnv)

		// Helper to extract code from a block argument (ParenGroup, StoredBlock marker, or string)
		extractCode := func(arg interface{}) string {
			switch v := arg.(type) {
			case ParenGroup:
				return string(v)
			case Symbol:
				// Check if it's a block marker
				markerType, objectID := parseObjectMarker(string(v))
				if markerType == "block" && objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						if storedBlock, ok := obj.(StoredBlock); ok {
							return string(storedBlock)
						}
					}
				}
				return string(v)
			default:
				return fmt.Sprintf("%v", arg)
			}
		}

		// Check for anonymous macro: macro (body)
		if len(ctx.Args) == 1 {
			commands := extractCode(ctx.Args[0])
			ps.logger.DebugCat(CatMacro,"Creating anonymous macro with commands: %s", commands)

			macro := NewStoredMacroWithEnv(commands, ctx.Position, macroEnv)
			macroRef := ctx.executor.RegisterObject(macro, ObjMacro)
			ctx.state.SetResult(macroRef)

			ps.logger.DebugCat(CatMacro,"Created anonymous macro (object %d)", macroRef.ID)
			return BoolStatus(true)
		}

		// Named macro: macro name, (body)
		if len(ctx.Args) < 2 {
			ps.logger.ErrorCat(CatCommand, "Usage: macro <name>, <commands> OR macro <commands>")
			return BoolStatus(false)
		}

		name := fmt.Sprintf("%v", ctx.Args[0])
		commands := extractCode(ctx.Args[1])

		ps.logger.DebugCat(CatMacro,"Defining macro '%s' with commands: %s", name, commands)

		// Store in module environment's MacrosModule (with COW)
		ctx.state.moduleEnv.mu.Lock()
		defer ctx.state.moduleEnv.mu.Unlock()

		// Check if macro already exists
		if existing, exists := ctx.state.moduleEnv.MacrosModule[name]; exists && existing != nil {
			if existing.IsForward {
				// Fill in the forward declaration by mutating the struct in place
				// This preserves the pointer so all references see the update
				existing.Commands = commands
				existing.DefinitionFile = ctx.Position.Filename
				existing.DefinitionLine = ctx.Position.Line
				existing.DefinitionColumn = ctx.Position.Column
				existing.Timestamp = time.Now()
				existing.ModuleEnv = macroEnv
				existing.IsForward = false // No longer a forward declaration

				ps.logger.DebugCat(CatMacro, "Resolved forward declaration for macro '%s'", name)
				return BoolStatus(true)
			}
			// Macro exists and is not a forward declaration - error
			ps.logger.ErrorCat(CatMacro, "Cannot define macro '%s': already exists (use macro_delete first)", name)
			return BoolStatus(false)
		}

		// Create new StoredMacro
		macro := NewStoredMacroWithEnv(commands, ctx.Position, macroEnv)

		ctx.state.moduleEnv.EnsureMacroRegistryCopied()
		ctx.state.moduleEnv.MacrosModule[name] = &macro
		ctx.state.moduleEnv.RegistryGeneration++ // Invalidate handler caches

		ps.logger.DebugCat(CatMacro,"Successfully defined named macro '%s' in MacrosModule", name)
		return BoolStatus(true)
	})

	// macro_forward - create a forward declaration for a macro
	ps.RegisterCommandInModule("macros", "macro_forward", func(ctx *Context) Result {
		if len(ctx.Args) != 1 {
			ps.logger.ErrorCat(CatCommand, "Usage: macro_forward <name>")
			return BoolStatus(false)
		}

		name := fmt.Sprintf("%v", ctx.Args[0])

		ctx.state.moduleEnv.mu.Lock()
		defer ctx.state.moduleEnv.mu.Unlock()

		// Check if macro already exists
		if existing, exists := ctx.state.moduleEnv.MacrosModule[name]; exists && existing != nil {
			ps.logger.ErrorCat(CatMacro, "Cannot create forward declaration for '%s': macro already exists (use macro_delete first)", name)
			return BoolStatus(false)
		}

		// Create forward declaration with placeholder
		forward := &StoredMacro{
			Commands:         "", // Empty - will be filled in by actual definition
			DefinitionFile:   ctx.Position.Filename,
			DefinitionLine:   ctx.Position.Line,
			DefinitionColumn: ctx.Position.Column,
			Timestamp:        time.Now(),
			ModuleEnv:        nil, // Will be set by actual definition
			IsForward:        true,
		}

		// Store in module environment's MacrosModule (with COW)
		ctx.state.moduleEnv.EnsureMacroRegistryCopied()
		ctx.state.moduleEnv.MacrosModule[name] = forward
		ctx.state.moduleEnv.RegistryGeneration++ // Invalidate handler caches

		ps.logger.DebugCat(CatMacro, "Created forward declaration for macro '%s'", name)
		return BoolStatus(true)
	})

	// call - call a macro or command
	ps.RegisterCommandInModule("macros", "call", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.ErrorCat(CatCommand, "Usage: call <macro_name_or_object>, [args...]")
			return BoolStatus(false)
		}

		callArgs := ctx.Args[1:]
		childState := ctx.state.CreateChild()
		firstArg := ctx.Args[0]

		// Check if the first argument is already a resolved StoredCommand object
		if cmd, ok := firstArg.(StoredCommand); ok {
			ps.logger.DebugCat(CatMacro,"Calling resolved StoredCommand object: %s", cmd.CommandName)

			cmdCtx := &Context{
				Args:      callArgs,
				NamedArgs: ctx.NamedArgs,
				Position:  ctx.Position,
				state:     childState,
				executor:  ctx.executor,
				logger:    ctx.logger,
			}

			result := cmd.Handler(cmdCtx)

			if childState.HasResult() {
				ctx.state.SetResult(childState.GetResult())
			}

			return result
		}

		// Check if the first argument is already a resolved StoredMacro object
		if macro, ok := firstArg.(StoredMacro); ok {
			ps.logger.DebugCat(CatMacro,"Calling resolved StoredMacro object")

			return ps.executor.ExecuteStoredMacro(&macro, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
				filename := ""
				lineOffset := 0
				columnOffset := 0
				if substCtx != nil {
					filename = substCtx.Filename
					lineOffset = substCtx.CurrentLineOffset
					columnOffset = substCtx.CurrentColumnOffset
				}
				return ps.executor.ExecuteWithState(commands, macroExecState, substCtx, filename, lineOffset, columnOffset)
			}, callArgs, ctx.NamedArgs, childState, ctx.Position, ctx.state)
		}

		// Check if the first argument is a marker (Symbol)
		if sym, ok := firstArg.(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))

			if markerType == "command" && objectID >= 0 {
				ps.logger.DebugCat(CatMacro,"Calling StoredCommand via marker (object %d)", objectID)

				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.ErrorCat(CatArgument, "Command object %d not found", objectID)
					return BoolStatus(false)
				}

				cmd, ok := obj.(StoredCommand)
				if !ok {
					ps.logger.ErrorCat(CatArgument, "Object %d is not a StoredCommand", objectID)
					return BoolStatus(false)
				}

				cmdCtx := &Context{
					Args:      callArgs,
					NamedArgs: ctx.NamedArgs,
					Position:  ctx.Position,
					state:     childState,
					executor:  ctx.executor,
					logger:    ctx.logger,
				}

				result := cmd.Handler(cmdCtx)

				if childState.HasResult() {
					ctx.state.SetResult(childState.GetResult())
				}

				return result
			}

			if markerType == "macro" && objectID >= 0 {
				ps.logger.DebugCat(CatMacro,"Calling StoredMacro via marker (object %d)", objectID)

				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.ErrorCat(CatArgument, "Macro object %d not found", objectID)
					return BoolStatus(false)
				}

				macro, ok := obj.(StoredMacro)
				if !ok {
					ps.logger.ErrorCat(CatArgument, "Object %d is not a StoredMacro", objectID)
					return BoolStatus(false)
				}

				return ps.executor.ExecuteStoredMacro(&macro, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
					filename := ""
					lineOffset := 0
					columnOffset := 0
					if substCtx != nil {
						filename = substCtx.Filename
						lineOffset = substCtx.CurrentLineOffset
						columnOffset = substCtx.CurrentColumnOffset
					}
					return ps.executor.ExecuteWithState(commands, macroExecState, substCtx, filename, lineOffset, columnOffset)
				}, callArgs, ctx.NamedArgs, childState, ctx.Position, ctx.state)
			}

		}

	// Check if the first argument is a marker (string type, from $1 substitution etc.)
	if str, ok := firstArg.(string); ok {
		markerType, objectID := parseObjectMarker(str)

		if markerType == "command" && objectID >= 0 {
			ps.logger.DebugCat(CatMacro,"Calling StoredCommand via string marker (object %d)", objectID)

			obj, exists := ctx.executor.getObject(objectID)
			if !exists {
				ps.logger.ErrorCat(CatArgument, "Command object %d not found", objectID)
				return BoolStatus(false)
			}

			cmd, ok := obj.(StoredCommand)
			if !ok {
				ps.logger.ErrorCat(CatArgument, "Object %d is not a StoredCommand", objectID)
				return BoolStatus(false)
			}

			cmdCtx := &Context{
				Args:      callArgs,
				NamedArgs: ctx.NamedArgs,
				Position:  ctx.Position,
				state:     childState,
				executor:  ctx.executor,
				logger:    ctx.logger,
			}

			result := cmd.Handler(cmdCtx)

			if childState.HasResult() {
				ctx.state.SetResult(childState.GetResult())
			}

			return result
		}

		if markerType == "macro" && objectID >= 0 {
			ps.logger.DebugCat(CatMacro,"Calling StoredMacro via string marker (object %d)", objectID)

			obj, exists := ctx.executor.getObject(objectID)
			if !exists {
				ps.logger.ErrorCat(CatArgument, "Macro object %d not found", objectID)
				return BoolStatus(false)
			}

			macro, ok := obj.(StoredMacro)
			if !ok {
				ps.logger.ErrorCat(CatArgument, "Object %d is not a StoredMacro", objectID)
				return BoolStatus(false)
			}

			return ps.executor.ExecuteStoredMacro(&macro, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
				filename := ""
				lineOffset := 0
				columnOffset := 0
				if substCtx != nil {
					filename = substCtx.Filename
					lineOffset = substCtx.CurrentLineOffset
					columnOffset = substCtx.CurrentColumnOffset
				}
				return ps.executor.ExecuteWithState(commands, macroExecState, substCtx, filename, lineOffset, columnOffset)
			}, callArgs, ctx.NamedArgs, childState, ctx.Position, ctx.state)
		}

	}

	// Otherwise, treat it as a macro name - look up in module environment
		name := fmt.Sprintf("%v", firstArg)
		ps.logger.DebugCat(CatMacro,"Calling macro by name: %s", name)

		// Look up macro in module environment (COW - only check MacrosModule)
		var macro *StoredMacro
		ctx.state.moduleEnv.mu.RLock()
		if m, exists := ctx.state.moduleEnv.MacrosModule[name]; exists && m != nil {
			macro = m
		}
		ctx.state.moduleEnv.mu.RUnlock()

		if macro == nil {
			ps.logger.ErrorCat(CatMacro, "Macro \"%s\" not found", name)
			return BoolStatus(false)
		}

		return ps.executor.ExecuteStoredMacro(macro, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
			filename := ""
			lineOffset := 0
			columnOffset := 0
			if substCtx != nil {
				filename = substCtx.Filename
				lineOffset = substCtx.CurrentLineOffset
				columnOffset = substCtx.CurrentColumnOffset
			}
			return ps.executor.ExecuteWithState(commands, macroExecState, substCtx, filename, lineOffset, columnOffset)
		}, callArgs, ctx.NamedArgs, childState, ctx.Position, ctx.state)
	})

	// macro_list - list all defined macros in current scope
	ps.RegisterCommandInModule("macros", "macro_list", func(ctx *Context) Result {
		// Collect macros from module environment (COW - only check MacrosModule)
		ctx.state.moduleEnv.mu.RLock()
		macros := make([]string, 0, len(ctx.state.moduleEnv.MacrosModule))
		for name, macro := range ctx.state.moduleEnv.MacrosModule {
			if macro != nil {
				macros = append(macros, name)
			}
		}
		ctx.state.moduleEnv.mu.RUnlock()

		sort.Strings(macros)
		ctx.SetResult(fmt.Sprintf("%v", macros))
		return BoolStatus(true)
	})

	// macro_delete - delete a macro by name from current scope
	ps.RegisterCommandInModule("macros", "macro_delete", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: macro_delete <macro_name>")
			return BoolStatus(false)
		}

		name := fmt.Sprintf("%v", ctx.Args[0])

		// Delete from MacrosModule (COW will trigger on write if needed)
		ctx.state.moduleEnv.mu.Lock()
		macro, exists := ctx.state.moduleEnv.MacrosModule[name]
		if !exists || macro == nil {
			ctx.state.moduleEnv.mu.Unlock()
			ctx.LogError(CatMacro, fmt.Sprintf("PawScript macro \"%s\" not found or could not be deleted", name))
			return BoolStatus(false)
		}
		ctx.state.moduleEnv.EnsureMacroRegistryCopied()
		delete(ctx.state.moduleEnv.MacrosModule, name)
		ctx.state.moduleEnv.mu.Unlock()

		return BoolStatus(true)
	})

	// macro_clear - clear all macros from current scope
	ps.RegisterCommandInModule("macros", "macro_clear", func(ctx *Context) Result {
		// Count and clear macros from MacrosModule (COW)
		ctx.state.moduleEnv.mu.Lock()
		count := 0

		// Count non-nil macros in MacrosModule
		for _, macro := range ctx.state.moduleEnv.MacrosModule {
			if macro != nil {
				count++
			}
		}

		// Reset MacrosModule to empty
		ctx.state.moduleEnv.MacrosModule = make(map[string]*StoredMacro)
		ctx.state.moduleEnv.macrosModuleCopied = true

		ctx.state.moduleEnv.mu.Unlock()

		ctx.SetResult(fmt.Sprintf("Cleared %d PawScript macros", count))
		return BoolStatus(true)
	})

	// command_ref - get a reference to a built-in or registered command
	ps.RegisterCommandInModule("macros", "command_ref", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.ErrorCat(CatCommand, "Usage: command_ref <command_name>")
			return BoolStatus(false)
		}

		commandName := fmt.Sprintf("%v", ctx.Args[0])
		ps.logger.DebugCat(CatMacro, "Getting command reference for: %s", commandName)

		handler, exists := ctx.state.moduleEnv.GetCommand(commandName)
		if !exists {
			ps.logger.ErrorCat(CatMacro, "Command \"%s\" not found", commandName)
			return BoolStatus(false)
		}

		cmd := NewStoredCommand(commandName, handler)
		cmdRef := ctx.executor.RegisterObject(cmd, ObjCommand)
		ctx.state.SetResult(cmdRef)

		ps.logger.DebugCat(CatMacro,"Created command reference for '%s' (object %d)", commandName, cmdRef.ID)
		return BoolStatus(true)
	})

	// ==================== flow:: module ====================

	// break - exit from a loop
	// Usage: break        - exit the innermost loop
	// Usage: break <n>    - exit n levels of nested loops
	ps.RegisterCommandInModule("flow", "break", func(ctx *Context) Result {
		levels := 1
		if len(ctx.Args) > 0 {
			if n, ok := toNumber(ctx.Args[0]); ok && n >= 1 {
				levels = int(n)
			}
		}
		return BreakResult{Levels: levels}
	})

	// continue - skip to the next iteration of a loop
	// Usage: continue     - skip to next iteration of innermost loop
	// Usage: continue <n> - skip to next iteration n levels up
	ps.RegisterCommandInModule("flow", "continue", func(ctx *Context) Result {
		levels := 1
		if len(ctx.Args) > 0 {
			if n, ok := toNumber(ctx.Args[0]); ok && n >= 1 {
				levels = int(n)
			}
		}
		return ContinueResult{Levels: levels}
	})

	// while - loop while condition is true
	// Generator-aware: catches YieldResult and attaches WhileContinuation
	ps.RegisterCommandInModule("flow", "while", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: while (condition), (body)")
			return BoolStatus(false)
		}

		// Warn if condition is not a ParenGroup, bool true, or from a variable
		// A non-block condition is likely a mistake - the condition should be
		// re-evaluated each iteration, which requires a code block
		_, condIsParenGroup := ctx.Args[0].(ParenGroup)
		isBoolTrue := false
		if b, ok := ctx.Args[0].(bool); ok && b {
			isBoolTrue = true
		}
		condFromVariable := len(ctx.RawArgs) > 0 && strings.HasPrefix(ctx.RawArgs[0], "~")

		if !condIsParenGroup && !isBoolTrue && !condFromVariable {
			ctx.LogWarning(CatCommand, "while condition is not a code block; it will not be re-evaluated each iteration. Use (condition) for dynamic conditions or 'true' for intentional infinite loops")
		}

		// Warn if body is not a ParenGroup or from a variable
		// Someone might accidentally use braces {cmd} instead of parentheses (cmd)
		_, bodyIsParenGroup := ctx.Args[1].(ParenGroup)
		bodyFromVariable := len(ctx.RawArgs) > 1 && strings.HasPrefix(ctx.RawArgs[1], "~")

		if !bodyIsParenGroup && !bodyFromVariable {
			ctx.LogWarning(CatCommand, "while body is not a code block; use (commands) for the loop body, not {commands}")
		}

		conditionBlock := fmt.Sprintf("%v", ctx.Args[0])
		bodyBlock := fmt.Sprintf("%v", ctx.Args[1])

		// Parse body (with caching if possible - body is at arg index 1)
		bodyCommands, parseErr := ctx.GetOrParseBlock(1, bodyBlock)
		if parseErr != "" {
			ctx.LogError(CatCommand, fmt.Sprintf("while: failed to parse body: %s", parseErr))
			return BoolStatus(false)
		}

		maxIterations := ctx.executor.GetMaxIterations()
		iterations := 0

		for maxIterations <= 0 || iterations < maxIterations {
			condResult := ctx.executor.ExecuteWithState(
				conditionBlock,
				ctx.state,
				nil,
				"",
				0, 0,
			)

			if earlyReturn, ok := condResult.(EarlyReturn); ok {
				return earlyReturn
			}

			// Check for break in condition - exit loop
			if breakResult, ok := condResult.(BreakResult); ok {
				if breakResult.Levels <= 1 {
					return BoolStatus(true)
				}
				return BreakResult{Levels: breakResult.Levels - 1}
			}

			// Check for continue in condition - treated as continuing to next iteration
			if continueResult, ok := condResult.(ContinueResult); ok {
				if continueResult.Levels <= 1 {
					// Continue in condition doesn't make sense - treat as no-op
					// and continue checking condition
					continue
				}
				return ContinueResult{Levels: continueResult.Levels - 1}
			}

			// Check for yield in condition (unusual but possible)
			if yieldResult, ok := condResult.(YieldResult); ok {
				outerCont := &WhileContinuation{
					ConditionBlock:    conditionBlock,
					BodyBlock:         bodyBlock,
					CachedBodyCmds:    bodyCommands, // Cache full body for reuse across iterations
					RemainingBodyCmds: bodyCommands, // Full body since we haven't started
					BodyCmdIndex:      -1,           // -1 indicates yield was in condition
					IterationCount:    iterations,
					State:             ctx.state,
				}
				if yieldResult.WhileContinuation == nil {
					yieldResult.WhileContinuation = outerCont
				} else {
					yieldResult.WhileContinuation.ParentContinuation = outerCont
				}
				return yieldResult
			}

			// Handle async in condition
			shouldContinue := false
			if condToken, isToken := condResult.(TokenResult); isToken {
				tokenID := string(condToken)
				waitChan := make(chan ResumeData, 1)
				ctx.executor.attachWaitChan(tokenID, waitChan)
				resumeData := <-waitChan
				shouldContinue = resumeData.Status
			} else if boolRes, ok := condResult.(BoolStatus); ok {
				shouldContinue = bool(boolRes)
			}

			if !shouldContinue {
				break
			}

			// Execute body commands one at a time to track position for yields
			lastStatus := true
			for cmdIdx, cmd := range bodyCommands {
				if strings.TrimSpace(cmd.Command) == "" {
					continue
				}

				// Apply flow control
				shouldExecute := true
				switch cmd.Separator {
				case "&":
					shouldExecute = lastStatus
				case "|":
					shouldExecute = !lastStatus
				}

				if !shouldExecute {
					continue
				}

				result := ctx.executor.executeParsedCommand(cmd, ctx.state, nil)

				// Check for yield - attach while continuation
				// For nested while loops, chain as parent continuation
				if yieldResult, ok := result.(YieldResult); ok {
					outerCont := &WhileContinuation{
						ConditionBlock:    conditionBlock,
						BodyBlock:         bodyBlock,
						CachedBodyCmds:    bodyCommands, // Cache full body for reuse across iterations
						RemainingBodyCmds: bodyCommands[cmdIdx+1:],
						BodyCmdIndex:      cmdIdx,
						IterationCount:    iterations,
						State:             ctx.state,
					}
					if yieldResult.WhileContinuation == nil {
						yieldResult.WhileContinuation = outerCont
					} else {
						// Chain: inner while's continuation gets outer as parent
						yieldResult.WhileContinuation.ParentContinuation = outerCont
					}
					return yieldResult
				}

				// Check for early return - propagate up to calling context
				if earlyReturn, ok := result.(EarlyReturn); ok {
					return earlyReturn
				}

				// Check for break - exit this loop
				if breakResult, ok := result.(BreakResult); ok {
					if breakResult.Levels <= 1 {
						// Break out of this loop
						return BoolStatus(true)
					}
					// Propagate break with decremented level
					return BreakResult{Levels: breakResult.Levels - 1}
				}

				// Check for continue - skip to next iteration
				if continueResult, ok := result.(ContinueResult); ok {
					if continueResult.Levels <= 1 {
						// Continue this loop - break inner command loop to go to next iteration
						break
					}
					// Propagate continue with decremented level
					return ContinueResult{Levels: continueResult.Levels - 1}
				}

				// Handle async in body
				if bodyToken, isToken := result.(TokenResult); isToken {
					tokenID := string(bodyToken)
					waitChan := make(chan ResumeData, 1)
					ctx.executor.attachWaitChan(tokenID, waitChan)
					resumeData := <-waitChan

					if !resumeData.Status {
						ctx.LogError(CatFlow, "Async operation in while loop failed")
						return BoolStatus(false)
					}
					lastStatus = resumeData.Status
					continue
				}

				if boolRes, ok := result.(BoolStatus); ok {
					lastStatus = bool(boolRes)
				}
			}

			iterations++

			// Yield to other goroutines to prevent CPU starvation in tight loops
			runtime.Gosched()
		}

		if maxIterations > 0 && iterations >= maxIterations {
			ctx.LogError(CatFlow, fmt.Sprintf("Maximum iterations (%d) exceeded", maxIterations))
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	// for - loop over a range, list, generator, or key/value pairs
	// Forms:
	//   for <start>, <end>, <var>, (body)           - numeric range (inclusive)
	//   for <start>, <end>, by: <step>, <var>, (body) - numeric range with step
	//   for ~<iterator>, <var>, (body)              - iterate a generator/list iterator
	//   for ~<list>, <var>, (body)                  - iterate list items
	//   for ~<list>, <var>, order: descending, (body) - iterate in reverse
	//   for ~<list>, <key>, <value>, (body)         - key/value pairs (named args)
	//   for ~<struct>, <key>, <value>, (body)       - struct field names and values
	//   for ~<list>, (<unpack vars>), (body)        - unpack each item
	// Named args:
	//   by: <step>        - step value for numeric ranges
	//   order: ascending|descending - iteration order for lists
	//   iter: <var>       - variable for 1-based iteration number
	//   index: <var>      - variable for 0-based index
	ps.RegisterCommandInModule("flow", "for", func(ctx *Context) Result {
		if len(ctx.Args) < 3 {
			ctx.LogError(CatCommand, "Usage: for <range|iterator>, <var>, (body)")
			return BoolStatus(false)
		}

		// Helper to extract code from a block argument
		extractCode := func(arg interface{}) string {
			switch v := arg.(type) {
			case ParenGroup:
				return string(v)
			case Symbol:
				markerType, objectID := parseObjectMarker(string(v))
				if markerType == "block" && objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						if storedBlock, ok := obj.(StoredBlock); ok {
							return string(storedBlock)
						}
					}
				}
				return string(v)
			default:
				return fmt.Sprintf("%v", arg)
			}
		}

		// Helper to check if arg is a token (generator/iterator)
		isToken := func(arg interface{}) bool {
			switch v := arg.(type) {
			case ObjectRef:
				return v.Type == ObjToken && v.IsValid()
			case TokenResult:
				return true
			case Symbol:
				// First check if it's a token marker (from brace substitution)
				str := string(v)
				if markerType, objID := parseObjectMarker(str); markerType == "token" && objID >= 0 {
					// Verify the token object exists
					if _, exists := ctx.executor.getObject(objID); exists {
						return true
					}
				}
				// Otherwise check if it's a token string ID (lookup in activeTokens)
				ctx.executor.mu.Lock()
				_, exists := ctx.executor.activeTokens[str]
				ctx.executor.mu.Unlock()
				return exists
			case string:
				// First check if it's a token marker (from brace substitution)
				if markerType, objID := parseObjectMarker(v); markerType == "token" && objID >= 0 {
					// Verify the token object exists
					if _, exists := ctx.executor.getObject(objID); exists {
						return true
					}
				}
				// Otherwise check if it's a token string ID
				ctx.executor.mu.Lock()
				_, exists := ctx.executor.activeTokens[v]
				ctx.executor.mu.Unlock()
				return exists
			}
			return false
		}

		// Helper to check if arg is a list
		isList := func(arg interface{}) (StoredList, int, bool) {
			switch v := arg.(type) {
			case ObjectRef:
				// ObjectRef is the preferred way to reference stored objects
				if v.Type == ObjList && v.IsValid() {
					if obj, exists := ctx.executor.getObject(v.ID); exists {
						if list, ok := obj.(StoredList); ok {
							return list, v.ID, true
						}
					}
				}
			case StoredList:
				ref := ctx.executor.RegisterObject(v, ObjList)
				return v, ref.ID, true
			case ParenGroup:
				// Convert ParenGroup to StoredList
				items, namedArgs := parseArguments(string(v))
				list := NewStoredListWithNamed(items, namedArgs)
				ref := ctx.executor.RegisterObject(list, ObjList)
				return list, ref.ID, true
			case Symbol:
				markerType, objectID := parseObjectMarker(string(v))
				if markerType == "list" && objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						if list, ok := obj.(StoredList); ok {
							return list, objectID, true
						}
					}
				}
			case string:
				markerType, objectID := parseObjectMarker(v)
				if markerType == "list" && objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						if list, ok := obj.(StoredList); ok {
							return list, objectID, true
						}
					}
				}
			}
			return StoredList{}, -1, false
		}

		// Helper to check if arg is a struct
		isStruct := func(arg interface{}) (StoredStruct, bool) {
			switch v := arg.(type) {
			case StoredStruct:
				return v, true
			case Symbol:
				markerType, objectID := parseObjectMarker(string(v))
				if markerType == "struct" && objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						if s, ok := obj.(StoredStruct); ok {
							return s, true
						}
					}
				}
			case string:
				markerType, objectID := parseObjectMarker(v)
				if markerType == "struct" && objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						if s, ok := obj.(StoredStruct); ok {
							return s, true
						}
					}
				}
			}
			return StoredStruct{}, false
		}

		// Helper to check if arg is a ParenGroup (for unpack pattern)
		isParenGroup := func(arg interface{}) (string, bool) {
			if pg, ok := arg.(ParenGroup); ok {
				return string(pg), true
			}
			return "", false
		}

		// Get named args for iter: and index:
		iterVar := ""
		indexVar := ""
		if v, ok := ctx.NamedArgs["iter"]; ok {
			iterVar = fmt.Sprintf("%v", v)
		}
		if v, ok := ctx.NamedArgs["index"]; ok {
			indexVar = fmt.Sprintf("%v", v)
		}

		// Determine which form we're using
		var bodyBlock string
		var iteratorToken string
		var iterVarName string
		var keyVar, valueVar string
		var unpackVars []string
		var isKeyValue bool
		var isUnpack bool
		var iteratorType string
		var isDescending bool

		firstArg := ctx.Args[0]

		// Check for numeric range: two numbers as first args
		startNum, isStart := toFloat64(firstArg)
		if isStart && len(ctx.Args) >= 4 {
			endNum, isEnd := toFloat64(ctx.Args[1])
			if isEnd {
				// Numeric range form: for <start>, <end>, <var>, (body)
				// Or with step: for <start>, <end>, by: <step>, <var>, (body)
				iterVarName = fmt.Sprintf("%v", ctx.Args[2])
				bodyBlock = extractCode(ctx.Args[3])

				// Get step from named args
				step := 1.0
				ascending := endNum >= startNum
				if stepVal, hasStep := ctx.NamedArgs["by"]; hasStep {
					step, _ = toFloat64(stepVal)
					if step == 0 {
						ctx.LogWarning(CatCommand, "for: step is zero; loop will iterate until max iterations")
					} else if (ascending && step < 0) || (!ascending && step > 0) {
						ctx.LogWarning(CatCommand, "for: step direction doesn't match range direction")
					}
				} else if !ascending {
					step = -1
				}

				// Parse body (with caching - body is at arg index 3)
				bodyCommands, parseErr := ctx.GetOrParseBlock(3, bodyBlock)
				if parseErr != "" {
					ctx.LogError(CatCommand, fmt.Sprintf("for: failed to parse body: %s", parseErr))
					return BoolStatus(false)
				}

				// Execute the loop
				maxIterations := ctx.executor.GetMaxIterations()
				iterations := 0
				iterNum := 1
				current := startNum

				for maxIterations <= 0 || iterations < maxIterations {
					// Check termination
					if ascending && step > 0 {
						if current > endNum {
							break
						}
					} else if !ascending && step < 0 {
						if current < endNum {
							break
						}
					} else if step == 0 {
						// Infinite loop potential - will hit max iterations
					}

					// Set iteration variable
					if current == float64(int64(current)) {
						ctx.state.SetVariable(iterVarName, int64(current))
					} else {
						ctx.state.SetVariable(iterVarName, current)
					}

					// Set iter: and index: variables if requested
					if iterVar != "" {
						ctx.state.SetVariable(iterVar, int64(iterNum))
					}
					if indexVar != "" {
						ctx.state.SetVariable(indexVar, int64(iterNum-1))
					}

					// Execute body
					lastStatus := true
					for cmdIdx, cmd := range bodyCommands {
						if strings.TrimSpace(cmd.Command) == "" {
							continue
						}

						shouldExecute := true
						switch cmd.Separator {
						case "&":
							shouldExecute = lastStatus
						case "|":
							shouldExecute = !lastStatus
						}

						if !shouldExecute {
							continue
						}

						result := ctx.executor.executeParsedCommand(cmd, ctx.state, nil)

						// Handle yield - this would require ForContinuation
						if yieldResult, ok := result.(YieldResult); ok {
							// Create continuation for resuming
							outerCont := &ForContinuation{
								BodyBlock:         bodyBlock,
								CachedBodyCmds:    bodyCommands, // Cache full body for reuse
								RemainingBodyCmds: bodyCommands[cmdIdx+1:],
								BodyCmdIndex:      cmdIdx,
								IterationNumber:   iterNum,
								IterVar:           iterVarName,
								IterNumVar:        iterVar,
								IndexVar:          indexVar,
								IteratorType:      "numrange",
								State:             ctx.state,
								RangeStart:        startNum,
								RangeEnd:          endNum,
								RangeStep:         step,
								RangeCurrent:      current,
							}
							if yieldResult.ForContinuation == nil {
								yieldResult.ForContinuation = outerCont
							} else {
								yieldResult.ForContinuation.ParentContinuation = outerCont
							}
							return yieldResult
						}

						if earlyReturn, ok := result.(EarlyReturn); ok {
							// Propagate early return up to calling context
							return earlyReturn
						}

						// Check for break - exit this loop
						if breakResult, ok := result.(BreakResult); ok {
							if breakResult.Levels <= 1 {
								// Break out of this loop
								return BoolStatus(true)
							}
							// Propagate break with decremented level
							return BreakResult{Levels: breakResult.Levels - 1}
						}

						// Check for continue - skip to next iteration
						if continueResult, ok := result.(ContinueResult); ok {
							if continueResult.Levels <= 1 {
								// Continue this loop - break inner command loop to go to next iteration
								break
							}
							// Propagate continue with decremented level
							return ContinueResult{Levels: continueResult.Levels - 1}
						}

						if asyncToken, isToken := result.(TokenResult); isToken {
							tokenID := string(asyncToken)
							waitChan := make(chan ResumeData, 1)
							ctx.executor.attachWaitChan(tokenID, waitChan)
							resumeData := <-waitChan
							lastStatus = resumeData.Status
							continue
						}

						if boolRes, ok := result.(BoolStatus); ok {
							lastStatus = bool(boolRes)
						}
					}

					current += step
					iterNum++
					iterations++
				}

				if maxIterations > 0 && iterations >= maxIterations {
					ctx.LogError(CatFlow, fmt.Sprintf("Maximum iterations (%d) exceeded", maxIterations))
					return BoolStatus(false)
				}

				return BoolStatus(true)
			}
		}

		// Check for iterator/generator/list/struct forms
		if isToken(firstArg) {
			// Generator/iterator form: for ~<token>, <var>, (body)
			iteratorToken = fmt.Sprintf("%v", firstArg)
			iteratorType = "generator"

			// Check if second arg is a ParenGroup (unpack) or variable name
			if unpackPattern, ok := isParenGroup(ctx.Args[1]); ok {
				// Unpack form
				isUnpack = true
				unpackArgs, _ := parseArguments(unpackPattern)
				for _, arg := range unpackArgs {
					unpackVars = append(unpackVars, fmt.Sprintf("%v", arg))
				}
				bodyBlock = extractCode(ctx.Args[2])
			} else if len(ctx.Args) >= 4 {
				// Check for key-value form: for ~<token>, <key>, <value>, (body)
				// Last arg must be the body
				lastArg := ctx.Args[len(ctx.Args)-1]
				if _, ok := lastArg.(ParenGroup); ok {
					if len(ctx.Args) == 4 {
						// key-value form
						isKeyValue = true
						keyVar = fmt.Sprintf("%v", ctx.Args[1])
						valueVar = fmt.Sprintf("%v", ctx.Args[2])
						bodyBlock = extractCode(ctx.Args[3])
					}
				}
			}

			if !isKeyValue && !isUnpack {
				// Simple iteration: for ~<token>, <var>, (body)
				iterVarName = fmt.Sprintf("%v", ctx.Args[1])
				bodyBlock = extractCode(ctx.Args[2])
			}
		} else if list, listID, ok := isList(firstArg); ok {
			// List form
			_ = list
			_ = listID

			// Check order
			if orderVal, hasOrder := ctx.NamedArgs["order"]; hasOrder {
				orderStr := fmt.Sprintf("%v", orderVal)
				isDescending = (orderStr == "descending" || orderStr == "desc")
			}

			// Check if second arg is a ParenGroup (unpack) or variable name
			if unpackPattern, ok := isParenGroup(ctx.Args[1]); ok {
				// Unpack form
				isUnpack = true
				unpackArgs, _ := parseArguments(unpackPattern)
				for _, arg := range unpackArgs {
					unpackVars = append(unpackVars, fmt.Sprintf("%v", arg))
				}
				bodyBlock = extractCode(ctx.Args[2])
				iteratorType = "list"
			} else if len(ctx.Args) >= 4 {
				// Check for key-value form
				lastArg := ctx.Args[len(ctx.Args)-1]
				if _, ok := lastArg.(ParenGroup); ok {
					if len(ctx.Args) == 4 {
						isKeyValue = true
						keyVar = fmt.Sprintf("%v", ctx.Args[1])
						valueVar = fmt.Sprintf("%v", ctx.Args[2])
						bodyBlock = extractCode(ctx.Args[3])
						iteratorType = "keys"
					}
				}
			}

			if !isKeyValue && !isUnpack {
				iterVarName = fmt.Sprintf("%v", ctx.Args[1])
				bodyBlock = extractCode(ctx.Args[2])
				iteratorType = "list"
			}

			// Create the iterator based on type
			if iteratorType == "keys" {
				// Key-value iteration over named args
				namedArgs := list.NamedArgs()
				keys := make([]string, 0, len(namedArgs))
				for k := range namedArgs {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				if isDescending {
					// Reverse keys
					for i, j := 0, len(keys)-1; i < j; i, j = i+1, j-1 {
						keys[i], keys[j] = keys[j], keys[i]
					}
				}

				// Parse body (with caching - body is at last arg index)
				bodyCommands, parseErr := ctx.GetOrParseBlock(len(ctx.Args)-1, bodyBlock)
				if parseErr != "" {
					ctx.LogError(CatCommand, fmt.Sprintf("for: failed to parse body: %s", parseErr))
					return BoolStatus(false)
				}

				// Execute key-value loop
				iterNum := 1
				for idx, key := range keys {
					ctx.state.SetVariable(keyVar, key)
					ctx.state.SetVariable(valueVar, namedArgs[key])

					if iterVar != "" {
						ctx.state.SetVariable(iterVar, int64(iterNum))
					}
					if indexVar != "" {
						ctx.state.SetVariable(indexVar, int64(idx))
					}

					lastStatus := true
					for _, cmd := range bodyCommands {
						if strings.TrimSpace(cmd.Command) == "" {
							continue
						}
						shouldExecute := true
						switch cmd.Separator {
						case "&":
							shouldExecute = lastStatus
						case "|":
							shouldExecute = !lastStatus
						}
						if !shouldExecute {
							continue
						}

						result := ctx.executor.executeParsedCommand(cmd, ctx.state, nil)

						if earlyReturn, ok := result.(EarlyReturn); ok {
							// Propagate early return up to calling context
							return earlyReturn
						}

						// Check for break - exit this loop
						if breakResult, ok := result.(BreakResult); ok {
							if breakResult.Levels <= 1 {
								return BoolStatus(true)
							}
							return BreakResult{Levels: breakResult.Levels - 1}
						}

						// Check for continue - skip to next iteration
						if continueResult, ok := result.(ContinueResult); ok {
							if continueResult.Levels <= 1 {
								break
							}
							return ContinueResult{Levels: continueResult.Levels - 1}
						}

						if asyncToken, isToken := result.(TokenResult); isToken {
							tokenID := string(asyncToken)
							waitChan := make(chan ResumeData, 1)
							ctx.executor.attachWaitChan(tokenID, waitChan)
							resumeData := <-waitChan
							lastStatus = resumeData.Status
							continue
						}

						if boolRes, ok := result.(BoolStatus); ok {
							lastStatus = bool(boolRes)
						}
					}
					iterNum++
				}
				return BoolStatus(true)
			}

			// List iteration (items)
			items := list.Items()
			if isDescending {
				// Reverse items
				reversed := make([]interface{}, len(items))
				for i, item := range items {
					reversed[len(items)-1-i] = item
				}
				items = reversed
			}

			// Parse body (with caching - body is at last arg index)
			bodyCommands, parseErr := ctx.GetOrParseBlock(len(ctx.Args)-1, bodyBlock)
			if parseErr != "" {
				ctx.LogError(CatCommand, fmt.Sprintf("for: failed to parse body: %s", parseErr))
				return BoolStatus(false)
			}

			// Execute list loop
			iterNum := 1
			for idx, item := range items {
				if isUnpack {
					// Unpack the item into multiple variables
					var itemList []interface{}
					switch v := item.(type) {
					case StoredList:
						itemList = v.Items()
					case Symbol:
						markerType, objectID := parseObjectMarker(string(v))
						if markerType == "list" && objectID >= 0 {
							if obj, exists := ctx.executor.getObject(objectID); exists {
								if sl, ok := obj.(StoredList); ok {
									itemList = sl.Items()
								}
							}
						}
					case ParenGroup:
						// Parse as list
						args, _ := parseArguments(string(v))
						itemList = args
					}

					for i, varName := range unpackVars {
						if i < len(itemList) {
							ctx.state.SetVariable(varName, itemList[i])
						} else {
							ctx.state.SetVariable(varName, nil)
						}
					}
				} else {
					ctx.state.SetVariable(iterVarName, item)
				}

				if iterVar != "" {
					ctx.state.SetVariable(iterVar, int64(iterNum))
				}
				if indexVar != "" {
					ctx.state.SetVariable(indexVar, int64(idx))
				}

				lastStatus := true
				for _, cmd := range bodyCommands {
					if strings.TrimSpace(cmd.Command) == "" {
						continue
					}
					shouldExecute := true
					switch cmd.Separator {
					case "&":
						shouldExecute = lastStatus
					case "|":
						shouldExecute = !lastStatus
					}
					if !shouldExecute {
						continue
					}

					result := ctx.executor.executeParsedCommand(cmd, ctx.state, nil)

					if earlyReturn, ok := result.(EarlyReturn); ok {
						// Propagate early return up to calling context
						return earlyReturn
					}

					// Check for break - exit this loop
					if breakResult, ok := result.(BreakResult); ok {
						if breakResult.Levels <= 1 {
							return BoolStatus(true)
						}
						return BreakResult{Levels: breakResult.Levels - 1}
					}

					// Check for continue - skip to next iteration
					if continueResult, ok := result.(ContinueResult); ok {
						if continueResult.Levels <= 1 {
							break
						}
						return ContinueResult{Levels: continueResult.Levels - 1}
					}

					if asyncToken, isToken := result.(TokenResult); isToken {
						tokenID := string(asyncToken)
						waitChan := make(chan ResumeData, 1)
						ctx.executor.attachWaitChan(tokenID, waitChan)
						resumeData := <-waitChan
						lastStatus = resumeData.Status
						continue
					}

					if boolRes, ok := result.(BoolStatus); ok {
						lastStatus = bool(boolRes)
					}
				}
				iterNum++
			}
			return BoolStatus(true)

		} else if struc, ok := isStruct(firstArg); ok {
			// Struct form - iterate over field names and values
			if len(ctx.Args) < 4 {
				ctx.LogError(CatCommand, "for: struct iteration requires key and value variables")
				return BoolStatus(false)
			}

			keyVar = fmt.Sprintf("%v", ctx.Args[1])
			valueVar = fmt.Sprintf("%v", ctx.Args[2])
			bodyBlock = extractCode(ctx.Args[3])

			// Check if it's a struct array
			if struc.IsArray() {
				// Struct array - iterate over elements (body at last arg index)
				bodyCommands, parseErr := ctx.GetOrParseBlock(len(ctx.Args)-1, bodyBlock)
				if parseErr != "" {
					ctx.LogError(CatCommand, fmt.Sprintf("for: failed to parse body: %s", parseErr))
					return BoolStatus(false)
				}

				iterNum := 1
				for idx := 0; idx < struc.Len(); idx++ {
					elem := struc.Get(idx)
					ctx.state.SetVariable(keyVar, int64(idx))

					// Store element and set as ObjectRef
					elemRef := ctx.executor.RegisterObject(elem, ObjStruct)
					ctx.state.SetVariable(valueVar, elemRef)

					if iterVar != "" {
						ctx.state.SetVariable(iterVar, int64(iterNum))
					}
					if indexVar != "" {
						ctx.state.SetVariable(indexVar, int64(idx))
					}

					lastStatus := true
					for _, cmd := range bodyCommands {
						if strings.TrimSpace(cmd.Command) == "" {
							continue
						}
						shouldExecute := true
						switch cmd.Separator {
						case "&":
							shouldExecute = lastStatus
						case "|":
							shouldExecute = !lastStatus
						}
						if !shouldExecute {
							continue
						}

						result := ctx.executor.executeParsedCommand(cmd, ctx.state, nil)

						if earlyReturn, ok := result.(EarlyReturn); ok {
							// Propagate early return up to calling context
							return earlyReturn
						}

						// Check for break - exit this loop
						if breakResult, ok := result.(BreakResult); ok {
							if breakResult.Levels <= 1 {
								return BoolStatus(true)
							}
							return BreakResult{Levels: breakResult.Levels - 1}
						}

						// Check for continue - skip to next iteration
						if continueResult, ok := result.(ContinueResult); ok {
							if continueResult.Levels <= 1 {
								break
							}
							return ContinueResult{Levels: continueResult.Levels - 1}
						}

						if asyncToken, isToken := result.(TokenResult); isToken {
							tokenID := string(asyncToken)
							waitChan := make(chan ResumeData, 1)
							ctx.executor.attachWaitChan(tokenID, waitChan)
							resumeData := <-waitChan
							lastStatus = resumeData.Status
							continue
						}

						if boolRes, ok := result.(BoolStatus); ok {
							lastStatus = bool(boolRes)
						}
					}
					iterNum++
				}
				return BoolStatus(true)
			}

			// Single struct - iterate over fields
			// Get field names from definition
			defObj, ok := ctx.executor.getObject(struc.DefID())
			if !ok {
				ctx.LogError(CatCommand, "for: could not get struct definition")
				return BoolStatus(false)
			}
			defList, ok := defObj.(StoredList)
			if !ok {
				ctx.LogError(CatCommand, "for: invalid struct definition")
				return BoolStatus(false)
			}

			namedArgs := defList.NamedArgs()
			keys := make([]string, 0)
			for k := range namedArgs {
				if !strings.HasPrefix(k, "__") {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)

			bodyCommands, parseErr := ctx.GetOrParseBlock(len(ctx.Args)-1, bodyBlock)
			if parseErr != "" {
				ctx.LogError(CatCommand, fmt.Sprintf("for: failed to parse body: %s", parseErr))
				return BoolStatus(false)
			}

			iterNum := 1
			for idx, fieldName := range keys {
				ctx.state.SetVariable(keyVar, fieldName)
				fieldValue, _ := ctx.executor.getStructFieldValue(struc, fieldName)
				ctx.state.SetVariable(valueVar, fieldValue)

				if iterVar != "" {
					ctx.state.SetVariable(iterVar, int64(iterNum))
				}
				if indexVar != "" {
					ctx.state.SetVariable(indexVar, int64(idx))
				}

				lastStatus := true
				for _, cmd := range bodyCommands {
					if strings.TrimSpace(cmd.Command) == "" {
						continue
					}
					shouldExecute := true
					switch cmd.Separator {
					case "&":
						shouldExecute = lastStatus
					case "|":
						shouldExecute = !lastStatus
					}
					if !shouldExecute {
						continue
					}

					result := ctx.executor.executeParsedCommand(cmd, ctx.state, nil)

					if earlyReturn, ok := result.(EarlyReturn); ok {
						// Propagate early return up to calling context
						return earlyReturn
					}

					// Check for break - exit this loop
					if breakResult, ok := result.(BreakResult); ok {
						if breakResult.Levels <= 1 {
							return BoolStatus(true)
						}
						return BreakResult{Levels: breakResult.Levels - 1}
					}

					// Check for continue - skip to next iteration
					if continueResult, ok := result.(ContinueResult); ok {
						if continueResult.Levels <= 1 {
							break
						}
						return ContinueResult{Levels: continueResult.Levels - 1}
					}

					if asyncToken, isToken := result.(TokenResult); isToken {
						tokenID := string(asyncToken)
						waitChan := make(chan ResumeData, 1)
						ctx.executor.attachWaitChan(tokenID, waitChan)
						resumeData := <-waitChan
						lastStatus = resumeData.Status
						continue
					}

					if boolRes, ok := result.(BoolStatus); ok {
						lastStatus = bool(boolRes)
					}
				}
				iterNum++
			}
			return BoolStatus(true)
		}

		// Generator/iterator token form
		if iteratorToken != "" {
			bodyCommands, parseErr := ctx.GetOrParseBlock(len(ctx.Args)-1, bodyBlock)
			if parseErr != "" {
				ctx.LogError(CatCommand, fmt.Sprintf("for: failed to parse body: %s", parseErr))
				return BoolStatus(false)
			}

			maxIterations := ctx.executor.GetMaxIterations()
			iterations := 0
			iterNum := 1

			for maxIterations <= 0 || iterations < maxIterations {
				// Resume the iterator to get next value
				resumeCode := fmt.Sprintf("resume %s", iteratorToken)
				resumeResult := ctx.executor.ExecuteWithState(resumeCode, ctx.state, nil, "", 0, 0)

				// Check if iterator is exhausted
				if boolRes, ok := resumeResult.(BoolStatus); ok && !bool(boolRes) {
					break
				}

				// Get the yielded value
				var value interface{}
				if ctx.state.HasResult() {
					value = ctx.state.GetResult()
				}

				// Set variables
				if isUnpack {
					var itemList []interface{}
					switch v := value.(type) {
					case StoredList:
						itemList = v.Items()
					case Symbol:
						markerType, objectID := parseObjectMarker(string(v))
						if markerType == "list" && objectID >= 0 {
							if obj, exists := ctx.executor.getObject(objectID); exists {
								if sl, ok := obj.(StoredList); ok {
									itemList = sl.Items()
								}
							}
						}
					}
					for i, varName := range unpackVars {
						if i < len(itemList) {
							ctx.state.SetVariable(varName, itemList[i])
						} else {
							ctx.state.SetVariable(varName, nil)
						}
					}
				} else if isKeyValue {
					// For key-value with generator, expect list [key, value]
					switch v := value.(type) {
					case StoredList:
						items := v.Items()
						if len(items) >= 2 {
							ctx.state.SetVariable(keyVar, items[0])
							ctx.state.SetVariable(valueVar, items[1])
						}
					case Symbol:
						markerType, objectID := parseObjectMarker(string(v))
						if markerType == "list" && objectID >= 0 {
							if obj, exists := ctx.executor.getObject(objectID); exists {
								if sl, ok := obj.(StoredList); ok {
									items := sl.Items()
									if len(items) >= 2 {
										ctx.state.SetVariable(keyVar, items[0])
										ctx.state.SetVariable(valueVar, items[1])
									}
								}
							}
						}
					}
				} else {
					ctx.state.SetVariable(iterVarName, value)
				}

				if iterVar != "" {
					ctx.state.SetVariable(iterVar, int64(iterNum))
				}
				if indexVar != "" {
					ctx.state.SetVariable(indexVar, int64(iterNum-1))
				}

				// Execute body
				lastStatus := true
				for cmdIdx, cmd := range bodyCommands {
					if strings.TrimSpace(cmd.Command) == "" {
						continue
					}
					shouldExecute := true
					switch cmd.Separator {
					case "&":
						shouldExecute = lastStatus
					case "|":
						shouldExecute = !lastStatus
					}
					if !shouldExecute {
						continue
					}

					result := ctx.executor.executeParsedCommand(cmd, ctx.state, nil)

					// Handle yield
					if yieldResult, ok := result.(YieldResult); ok {
						outerCont := &ForContinuation{
							BodyBlock:         bodyBlock,
							CachedBodyCmds:    bodyCommands, // Cache full body for reuse
							RemainingBodyCmds: bodyCommands[cmdIdx+1:],
							BodyCmdIndex:      cmdIdx,
							IterationNumber:   iterNum,
							IterVar:           iterVarName,
							IterNumVar:        iterVar,
							IndexVar:          indexVar,
							KeyVar:            keyVar,
							ValueVar:          valueVar,
							UnpackVars:        unpackVars,
							IteratorToken:     iteratorToken,
							IteratorType:      iteratorType,
							IsDescending:      isDescending,
							State:             ctx.state,
						}
						if yieldResult.ForContinuation == nil {
							yieldResult.ForContinuation = outerCont
						} else {
							yieldResult.ForContinuation.ParentContinuation = outerCont
						}
						return yieldResult
					}

					if earlyReturn, ok := result.(EarlyReturn); ok {
						// Propagate early return up to calling context
						return earlyReturn
					}

					// Check for break - exit this loop
					if breakResult, ok := result.(BreakResult); ok {
						if breakResult.Levels <= 1 {
							return BoolStatus(true)
						}
						return BreakResult{Levels: breakResult.Levels - 1}
					}

					// Check for continue - skip to next iteration
					if continueResult, ok := result.(ContinueResult); ok {
						if continueResult.Levels <= 1 {
							break
						}
						return ContinueResult{Levels: continueResult.Levels - 1}
					}

					if asyncToken, isToken := result.(TokenResult); isToken {
						tokenID := string(asyncToken)
						waitChan := make(chan ResumeData, 1)
						ctx.executor.attachWaitChan(tokenID, waitChan)
						resumeData := <-waitChan
						lastStatus = resumeData.Status
						continue
					}

					if boolRes, ok := result.(BoolStatus); ok {
						lastStatus = bool(boolRes)
					}
				}

				iterNum++
				iterations++
			}

			if maxIterations > 0 && iterations >= maxIterations {
				ctx.LogError(CatFlow, fmt.Sprintf("Maximum iterations (%d) exceeded", maxIterations))
				return BoolStatus(false)
			}

			return BoolStatus(true)
		}

		ctx.LogError(CatCommand, "for: could not determine iteration form")
		return BoolStatus(false)
	})

	// include - include another source file
	ps.RegisterCommandInModule("core", "include", func(ctx *Context) Result {
		if len(ctx.Args) == 0 {
			ctx.LogError(CatIO, "Usage: include \"filename\" or include (imports...), \"filename\"")
			return BoolStatus(false)
		}

		var filename string
		var importSpec []interface{}
		var importNamedSpec map[string]interface{}
		isAdvancedForm := false

		firstArg := ctx.Args[0]
		if ctx.executor != nil {
			firstArg = ctx.executor.resolveValue(firstArg)
		}

		switch v := firstArg.(type) {
		case ParenGroup:
			isAdvancedForm = true
			importSpec, importNamedSpec = parseArguments(string(v))
			if len(ctx.Args) < 2 {
				ctx.LogError(CatIO, "include: filename required after import specification")
				return BoolStatus(false)
			}
			filename = fmt.Sprintf("%v", ctx.Args[1])
		case StoredList:
			isAdvancedForm = true
			importSpec = v.Items()
			importNamedSpec = make(map[string]interface{})
			if len(ctx.Args) < 2 {
				ctx.LogError(CatIO, "include: filename required after import specification")
				return BoolStatus(false)
			}
			filename = fmt.Sprintf("%v", ctx.Args[1])
		default:
			filename = fmt.Sprintf("%v", ctx.Args[0])
		}

		// Remove quotes if present
		if strings.HasPrefix(filename, "\"") && strings.HasSuffix(filename, "\"") {
			filename = filename[1 : len(filename)-1]
		} else if strings.HasPrefix(filename, "'") && strings.HasSuffix(filename, "'") {
			filename = filename[1 : len(filename)-1]
		}

		content, err := os.ReadFile(filename)
		if err != nil {
			ctx.LogError(CatIO, fmt.Sprintf("include: failed to read file %s: %v", filename, err))
			return BoolStatus(false)
		}

		if isAdvancedForm {
			restrictedEnv := NewMacroModuleEnvironment(ctx.state.moduleEnv)

			execState := NewExecutionState()
			execState.moduleEnv = restrictedEnv
			execState.executor = ctx.executor

			result := ctx.executor.ExecuteWithState(string(content), execState, nil, filename, 0, 0)

			// Handle async result - if TokenResult is returned, we need to wait for completion
			// For now, release state only if not async (include with async is unusual)
			if _, isToken := result.(TokenResult); !isToken {
				defer execState.ReleaseAllReferences()
			}

			// Merge bubbles from included file's state to caller state
			ctx.state.MergeBubbles(execState)

			if boolStatus, ok := result.(BoolStatus); ok && !bool(boolStatus) {
				return BoolStatus(false)
			}

			ctx.state.moduleEnv.mu.Lock()
			defer ctx.state.moduleEnv.mu.Unlock()

			ctx.state.moduleEnv.CopyLibraryRestricted()

			for _, arg := range importSpec {
				moduleName := fmt.Sprintf("%v", arg)
				if section, exists := restrictedEnv.ModuleExports[moduleName]; exists {
					if ctx.state.moduleEnv.LibraryRestricted[moduleName] == nil {
						ctx.state.moduleEnv.LibraryRestricted[moduleName] = make(ModuleSection)
					}
					if ctx.state.moduleEnv.LibraryInherited[moduleName] == nil {
						ctx.state.moduleEnv.LibraryInherited[moduleName] = make(ModuleSection)
					}
					for itemName, item := range section {
						ctx.state.moduleEnv.LibraryRestricted[moduleName][itemName] = item
						ctx.state.moduleEnv.LibraryInherited[moduleName][itemName] = item
					}
				}
			}

			for targetName, sourceArg := range importNamedSpec {
				sourceName := fmt.Sprintf("%v", sourceArg)
				if section, exists := restrictedEnv.ModuleExports[sourceName]; exists {
					if ctx.state.moduleEnv.LibraryRestricted[targetName] == nil {
						ctx.state.moduleEnv.LibraryRestricted[targetName] = make(ModuleSection)
					}
					if ctx.state.moduleEnv.LibraryInherited[targetName] == nil {
						ctx.state.moduleEnv.LibraryInherited[targetName] = make(ModuleSection)
					}
					for itemName, item := range section {
						ctx.state.moduleEnv.LibraryRestricted[targetName][itemName] = item
						ctx.state.moduleEnv.LibraryInherited[targetName][itemName] = item
					}
				}
			}

			return BoolStatus(true)
		} else {
			result := ctx.executor.ExecuteWithState(string(content), ctx.state, nil, filename, 0, 0)

			if boolStatus, ok := result.(BoolStatus); ok && !bool(boolStatus) {
				return BoolStatus(false)
			}

			return BoolStatus(true)
		}
	})
}
