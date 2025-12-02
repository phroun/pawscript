package pawscript

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// RegisterTypesLib registers string and list manipulation commands
// Modules: strlist, str
func (ps *PawScript) RegisterTypesLib() {
	// Helper function to set a StoredList as result with proper reference counting
	setListResult := func(ctx *Context, list StoredList) {
		id := ctx.executor.storeObject(list, "list")
		marker := fmt.Sprintf("\x00LIST:%d\x00", id)
		ctx.state.SetResultWithoutClaim(Symbol(marker))
	}

	// Helper function to set a StoredBytes as result with proper reference counting
	setBytesResult := func(ctx *Context, bytes StoredBytes) {
		id := ctx.executor.storeObject(bytes, "bytes")
		marker := fmt.Sprintf("\x00BYTES:%d\x00", id)
		ctx.state.SetResultWithoutClaim(Symbol(marker))
	}

	// Helper function to set a StoredStruct as result with proper reference counting
	setStructResult := func(ctx *Context, s StoredStruct) {
		id := ctx.executor.storeObject(s, "struct")
		marker := fmt.Sprintf("\x00STRUCT:%d\x00", id)
		ctx.state.SetResultWithoutClaim(Symbol(marker))
	}

	// Note: struct definitions are now just StoredLists, so use setListResult for them

	// bytes - creates a byte array from numbers, strings, or hex literals
	// Usage: bytes 72, 101, 108, 108, 111           - from numbers
	//        bytes "Hello"                           - from ASCII string
	//        bytes 0xDEADBEEF                        - from hex literal
	//        bytes 72, "ello", 0x21                  - mixed
	//        bytes 256, single: true                 - masks to byte (256 -> 0)
	ps.RegisterCommandInModule("strlist", "bytes", func(ctx *Context) Result {
		if len(ctx.Args) == 0 {
			// Empty bytes
			setBytesResult(ctx, NewStoredBytes(nil))
			return BoolStatus(true)
		}

		// Check for single: true named argument (mask each arg to single byte)
		singleMode := false
		if ctx.NamedArgs != nil {
			if val, ok := ctx.NamedArgs["single"]; ok {
				singleMode = isTruthy(val)
			}
		}

		var result []byte

		for _, arg := range ctx.Args {
			resolved := ctx.executor.resolveValue(arg)

			switch v := resolved.(type) {
			case int64:
				if singleMode {
					result = append(result, byte(v&0xFF))
				} else {
					// Check range
					if v < -128 || v > 255 {
						ctx.LogError(CatArgument, fmt.Sprintf("Value %d out of byte range (-128 to 255)", v))
						return BoolStatus(false)
					}
					result = append(result, byte(v&0xFF))
				}
			case float64:
				intVal := int64(v)
				if singleMode {
					result = append(result, byte(intVal&0xFF))
				} else {
					if intVal < -128 || intVal > 255 {
						ctx.LogError(CatArgument, fmt.Sprintf("Value %v out of byte range (-128 to 255)", v))
						return BoolStatus(false)
					}
					result = append(result, byte(intVal&0xFF))
				}
			case string:
				// Check if it's a hex literal
				if isHexLiteral(v) {
					if hexBytes, ok := parseHexToBytes(v); ok {
						result = append(result, hexBytes.Data()...)
					} else {
						ctx.LogError(CatArgument, fmt.Sprintf("Invalid hex literal: %s", v))
						return BoolStatus(false)
					}
				} else {
					// ASCII string
					for i := 0; i < len(v); i++ {
						if v[i] >= 128 {
							ctx.LogError(CatArgument, fmt.Sprintf("Non-ASCII character at position %d in string", i))
							return BoolStatus(false)
						}
						result = append(result, v[i])
					}
				}
			case QuotedString:
				str := string(v)
				// Check if it's a hex literal
				if isHexLiteral(str) {
					if hexBytes, ok := parseHexToBytes(str); ok {
						result = append(result, hexBytes.Data()...)
					} else {
						ctx.LogError(CatArgument, fmt.Sprintf("Invalid hex literal: %s", str))
						return BoolStatus(false)
					}
				} else {
					// ASCII string
					for i := 0; i < len(str); i++ {
						if str[i] >= 128 {
							ctx.LogError(CatArgument, fmt.Sprintf("Non-ASCII character at position %d in string", i))
							return BoolStatus(false)
						}
						result = append(result, str[i])
					}
				}
			case Symbol:
				str := string(v)
				// Check if it's a hex literal
				if isHexLiteral(str) {
					if hexBytes, ok := parseHexToBytes(str); ok {
						result = append(result, hexBytes.Data()...)
					} else {
						ctx.LogError(CatArgument, fmt.Sprintf("Invalid hex literal: %s", str))
						return BoolStatus(false)
					}
				} else {
					// Try to parse as number
					if num, ok := toNumber(v); ok {
						intVal := int64(num)
						if singleMode {
							result = append(result, byte(intVal&0xFF))
						} else {
							if intVal < -128 || intVal > 255 {
								ctx.LogError(CatArgument, fmt.Sprintf("Value %v out of byte range (-128 to 255)", num))
								return BoolStatus(false)
							}
							result = append(result, byte(intVal&0xFF))
						}
					} else {
						ctx.LogError(CatArgument, fmt.Sprintf("Cannot convert %v to bytes", v))
						return BoolStatus(false)
					}
				}
			case StoredBytes:
				// Append existing bytes
				result = append(result, v.Data()...)
			default:
				ctx.LogError(CatArgument, fmt.Sprintf("Cannot convert %T to bytes", v))
				return BoolStatus(false)
			}
		}

		setBytesResult(ctx, NewStoredBytes(result))
		return BoolStatus(true)
	})

	// slice - returns a slice of a list or string (end exclusive)
	// Usage: slice ~mylist, 0, 3    - items 0, 1, 2
	//        slice ~mylist, 1, -1   - from index 1 to end
	//        slice "hello", 0, 3          - "hel"
	ps.RegisterCommandInModule("strlist", "slice", func(ctx *Context) Result {
		// Check for only: parameter (extract arr or map portion of a list)
		if onlyArg, hasOnly := ctx.NamedArgs["only"]; hasOnly {
			if len(ctx.Args) < 1 {
				ctx.LogError(CatCommand, "Usage: slice <list>, only: arr|map")
				ctx.SetResult(nil)
				return BoolStatus(false)
			}

			// Get the only value as string
			onlyStr := ""
			switch v := onlyArg.(type) {
			case string:
				onlyStr = v
			case Symbol:
				onlyStr = string(v)
			case QuotedString:
				onlyStr = string(v)
			}

			// Resolve the list argument
			value := ctx.Args[0]
			var list StoredList
			switch v := value.(type) {
			case StoredList:
				list = v
			case Symbol:
				// Try to resolve marker
				if resolved := ctx.executor.resolveValue(v); resolved != nil {
					if l, ok := resolved.(StoredList); ok {
						list = l
					} else {
						ctx.LogError(CatType, "slice only: requires a list argument")
						ctx.SetResult(nil)
						return BoolStatus(false)
					}
				}
			default:
				ctx.LogError(CatType, "slice only: requires a list argument")
				ctx.SetResult(nil)
				return BoolStatus(false)
			}

			// Handle the only: value
			switch onlyStr {
			case "arr", "array", "positional", "items":
				// Extract only positional items
				items := list.Items()
				setListResult(ctx, NewStoredListWithRefs(items, nil, ctx.executor))
				return BoolStatus(true)
			case "map", "named":
				// Extract only named args
				namedArgs := list.NamedArgs()
				newNamed := make(map[string]interface{})
				if namedArgs != nil {
					for k, v := range namedArgs {
						newNamed[k] = v
					}
				}
				setListResult(ctx, NewStoredListWithRefs(nil, newNamed, ctx.executor))
				return BoolStatus(true)
			default:
				ctx.LogError(CatArgument, fmt.Sprintf("slice only: invalid value '%s', expected arr or map", onlyStr))
				ctx.SetResult(nil)
				return BoolStatus(false)
			}
		}

		// Standard slice behavior: slice <list|string>, <start>, <end>
		if len(ctx.Args) < 3 {
			ctx.LogError(CatCommand, "Usage: slice <list|string>, <start>, <end> or slice <list>, only: arr|map")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		// Parse start index
		startNum, ok := toNumber(ctx.Args[1])
		if !ok {
			ctx.LogError(CatArgument, "Start index must be a number")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
		start := int(startNum)

		// Parse end index
		endNum, ok := toNumber(ctx.Args[2])
		if !ok {
			ctx.LogError(CatArgument, "End index must be a number")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
		end := int(endNum)

		switch v := value.(type) {
		case StoredList:
			// Handle negative indices
			if end < 0 {
				end = v.Len()
			}
			setListResult(ctx, v.Slice(start, end))
			return BoolStatus(true)
		case StoredBytes:
			// Handle negative indices
			if end < 0 {
				end = v.Len()
			}
			setBytesResult(ctx, v.Slice(start, end))
			return BoolStatus(true)
		case StoredStruct:
			// Struct arrays can be sliced
			if !v.IsArray() {
				ctx.LogError(CatType, "Cannot slice a single struct (only struct arrays)")
				ctx.SetResult(nil)
				return BoolStatus(false)
			}
			// Handle negative indices
			if end < 0 {
				end = v.Len()
			}
			setStructResult(ctx, v.Slice(start, end))
			return BoolStatus(true)
		case string, QuotedString, Symbol:
			// Resolve in case it's a string marker
			resolved := ctx.executor.resolveValue(v)
			str := fmt.Sprintf("%v", resolved)
			// Handle negative indices
			if end < 0 {
				end = len(str)
			}
			// Bounds checking
			if start < 0 {
				start = 0
			}
			if end > len(str) {
				end = len(str)
			}
			if start > end {
				start = end
			}
			result := str[start:end]
			if ctx.executor != nil {
				result := ctx.executor.maybeStoreValue(result, ctx.state)
				ctx.state.SetResultWithoutClaim(result)
			} else {
				ctx.state.SetResultWithoutClaim(result)
			}
			return BoolStatus(true)
		default:
			ctx.LogError(CatType, fmt.Sprintf("Cannot slice type %s\n", getTypeName(v)))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
	})

	// append - returns a new list with item appended, or string with suffix appended
	// Usage: append ~mylist, newitem
	//        append "hello", " world"  -> "hello world"
	ps.RegisterCommandInModule("strlist", "append", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: append <list|string>, <item|suffix>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		value := ctx.Args[0]
		item := ctx.Args[1]

		switch v := value.(type) {
		case StoredList:
			setListResult(ctx, v.Append(item))
			return BoolStatus(true)
		case StoredBytes:
			// Bytes mode: append byte(s)
			resolvedItem := ctx.executor.resolveValue(item)
			switch bi := resolvedItem.(type) {
			case int64:
				setBytesResult(ctx, v.Append(bi))
			case float64:
				setBytesResult(ctx, v.Append(int64(bi)))
			case StoredBytes:
				setBytesResult(ctx, v.AppendBytes(bi))
			case string:
				if isHexLiteral(bi) {
					if hexBytes, ok := parseHexToBytes(bi); ok {
						setBytesResult(ctx, v.AppendBytes(hexBytes))
					} else {
						ctx.LogError(CatArgument, fmt.Sprintf("Invalid hex literal: %s", bi))
						return BoolStatus(false)
					}
				} else {
					ctx.LogError(CatArgument, "Cannot append string to bytes (use bytes command)")
					return BoolStatus(false)
				}
			case Symbol:
				str := string(bi)
				if isHexLiteral(str) {
					if hexBytes, ok := parseHexToBytes(str); ok {
						setBytesResult(ctx, v.AppendBytes(hexBytes))
					} else {
						ctx.LogError(CatArgument, fmt.Sprintf("Invalid hex literal: %s", str))
						return BoolStatus(false)
					}
				} else if num, ok := toNumber(bi); ok {
					setBytesResult(ctx, v.Append(int64(num)))
				} else {
					ctx.LogError(CatArgument, fmt.Sprintf("Cannot append %v to bytes", bi))
					return BoolStatus(false)
				}
			default:
				ctx.LogError(CatArgument, fmt.Sprintf("Cannot append %T to bytes", bi))
				return BoolStatus(false)
			}
			return BoolStatus(true)
		case string, QuotedString, Symbol, StoredString:
			// String mode: concatenate suffix
			resolved := ctx.executor.resolveValue(v)
			str := fmt.Sprintf("%v", resolved)
			suffix := resolveToString(item, ctx.executor)
			result := str + suffix
			if ctx.executor != nil {
				result := ctx.executor.maybeStoreValue(result, ctx.state)
				ctx.state.SetResultWithoutClaim(result)
			} else {
				ctx.state.SetResultWithoutClaim(result)
			}
			return BoolStatus(true)
		default:
			ctx.LogError(CatType, fmt.Sprintf("Cannot append to type %s\n", getTypeName(v)))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
	})

	// prepend - returns a new list with item prepended, or string with prefix prepended
	// Usage: prepend ~mylist, newitem
	//        prepend "world", "hello "  -> "hello world"
	ps.RegisterCommandInModule("strlist", "prepend", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: prepend <list|string>, <item|prefix>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		value := ctx.Args[0]
		item := ctx.Args[1]

		switch v := value.(type) {
		case StoredList:
			setListResult(ctx, v.Prepend(item))
			return BoolStatus(true)
		case StoredBytes:
			// Bytes mode: prepend byte(s)
			resolvedItem := ctx.executor.resolveValue(item)
			switch bi := resolvedItem.(type) {
			case int64:
				setBytesResult(ctx, v.Prepend(bi))
			case float64:
				setBytesResult(ctx, v.Prepend(int64(bi)))
			case StoredBytes:
				// Prepend: other bytes come first
				setBytesResult(ctx, bi.AppendBytes(v))
			case string:
				if isHexLiteral(bi) {
					if hexBytes, ok := parseHexToBytes(bi); ok {
						setBytesResult(ctx, hexBytes.AppendBytes(v))
					} else {
						ctx.LogError(CatArgument, fmt.Sprintf("Invalid hex literal: %s", bi))
						return BoolStatus(false)
					}
				} else {
					ctx.LogError(CatArgument, "Cannot prepend string to bytes (use bytes command)")
					return BoolStatus(false)
				}
			case Symbol:
				str := string(bi)
				if isHexLiteral(str) {
					if hexBytes, ok := parseHexToBytes(str); ok {
						setBytesResult(ctx, hexBytes.AppendBytes(v))
					} else {
						ctx.LogError(CatArgument, fmt.Sprintf("Invalid hex literal: %s", str))
						return BoolStatus(false)
					}
				} else if num, ok := toNumber(bi); ok {
					setBytesResult(ctx, v.Prepend(int64(num)))
				} else {
					ctx.LogError(CatArgument, fmt.Sprintf("Cannot prepend %v to bytes", bi))
					return BoolStatus(false)
				}
			default:
				ctx.LogError(CatArgument, fmt.Sprintf("Cannot prepend %T to bytes", bi))
				return BoolStatus(false)
			}
			return BoolStatus(true)
		case string, QuotedString, Symbol, StoredString:
			// String mode: prepend prefix
			resolved := ctx.executor.resolveValue(v)
			str := fmt.Sprintf("%v", resolved)
			prefix := resolveToString(item, ctx.executor)
			result := prefix + str
			if ctx.executor != nil {
				result := ctx.executor.maybeStoreValue(result, ctx.state)
				ctx.state.SetResultWithoutClaim(result)
			} else {
				ctx.state.SetResultWithoutClaim(result)
			}
			return BoolStatus(true)
		default:
			ctx.LogError(CatType, fmt.Sprintf("Cannot prepend to type %s\n", getTypeName(v)))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
	})

	// compact - returns a new list/bytes with a fresh backing array
	// Usage: compact ~mylist
	// Use this to free memory after slicing a large list or bytes
	ps.RegisterCommandInModule("strlist", "compact", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: compact <list|bytes>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		switch v := value.(type) {
		case StoredList:
			setListResult(ctx, v.Compact())
			return BoolStatus(true)
		case StoredBytes:
			setBytesResult(ctx, v.Compact())
			return BoolStatus(true)
		case StoredStruct:
			setStructResult(ctx, v.Compact())
			return BoolStatus(true)
		default:
			ctx.LogError(CatType, fmt.Sprintf("Cannot compact type %s\n", getTypeName(v)))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
	})

	// concat - polymorphic concatenation
	// Usage: concat "hello", " ", "world"            -> "hello world" (strings)
	//        concat ~list1, ~list2                   -> combined list (lists)
	//        concat ~list, "item1", "item2"          -> list with items appended
	//        concat ~list1, ~list2, "extra"          -> lists concatenated + item appended
	//        concat ~bytes1, ~bytes2                 -> combined bytes
	//        concat ~bytes, 0x0A, 255                -> bytes with values appended
	ps.RegisterCommandInModule("strlist", "concat", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: concat <value1>, <value2>, ...")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		// Check if first argument is a StoredList
		if list, ok := ctx.Args[0].(StoredList); ok {
			// List mode: concatenate lists and append other items
			result := list

			for i := 1; i < len(ctx.Args); i++ {
				arg := ctx.Args[i]

				if otherList, ok := arg.(StoredList); ok {
					// Concatenate lists
					result = result.Concat(otherList)
				} else {
					// Append as single item
					result = result.Append(arg)
				}
			}

			setListResult(ctx, result)
			return BoolStatus(true)
		}

		// Check if first argument is StoredBytes
		if bytes, ok := ctx.Args[0].(StoredBytes); ok {
			// Bytes mode: concatenate bytes and append other values
			result := bytes

			for i := 1; i < len(ctx.Args); i++ {
				arg := ctx.Args[i]

				if otherBytes, ok := arg.(StoredBytes); ok {
					// Concatenate bytes
					result = result.Concat(otherBytes)
				} else {
					// Try to append as single byte value
					switch v := arg.(type) {
					case int64:
						result = result.Append(v)
					case float64:
						result = result.Append(int64(v))
					case Symbol:
						// Check for hex literal
						if hexBytes, ok := parseHexToBytes(string(v)); ok {
							result = result.Concat(hexBytes)
						} else {
							ctx.LogError(CatType, fmt.Sprintf("Cannot append %s to bytes", getTypeName(arg)))
							ctx.SetResult(nil)
							return BoolStatus(false)
						}
					default:
						ctx.LogError(CatType, fmt.Sprintf("Cannot append %s to bytes", getTypeName(arg)))
						ctx.SetResult(nil)
						return BoolStatus(false)
					}
				}
			}

			setBytesResult(ctx, result)
			return BoolStatus(true)
		}

		// String mode: concatenate all arguments as strings
		var result strings.Builder
		for _, arg := range ctx.Args {
			// Resolve markers before converting to string
			resolved := ctx.executor.resolveValue(arg)
			result.WriteString(fmt.Sprintf("%v", resolved))
		}

		ctx.SetResult(result.String())
		return BoolStatus(true)
	})

	// split - split string or list by delimiter
	// String Usage: split "a,b,c", ","  -> list of ["a", "b", "c"]
	// List Usage:
	//   split ~mylist, "x"       -> split list on occurrences of "x"
	//   split ~mylist, ~delim    -> split list on sequence matching all items in delim
	// Inverse of join
	ps.RegisterCommandInModule("strlist", "split", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: split <string|list>, <delimiter>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		// Check if first argument is a list
		if list, ok := ctx.Args[0].(StoredList); ok {
			items := list.Items()
			delimiter := ctx.Args[1]

			// Check if delimiter is a list (sequence match) or single value
			if delimList, ok := delimiter.(StoredList); ok {
				// Sequence match: split on occurrences of the full sequence
				delimItems := delimList.Items()
				delimLen := len(delimItems)

				if delimLen == 0 {
					// Empty delimiter - return original list
					setListResult(ctx, list)
					return BoolStatus(true)
				}

				var result [][]interface{}
				var current []interface{}

				i := 0
				for i < len(items) {
					// Check if sequence matches at current position
					if i+delimLen <= len(items) {
						match := true
						for j := 0; j < delimLen; j++ {
							if !deepEqual(items[i+j], delimItems[j], ctx.executor) {
								match = false
								break
							}
						}
						if match {
							// Found delimiter sequence - save current segment
							result = append(result, current)
							current = nil
							i += delimLen
							continue
						}
					}
					current = append(current, items[i])
					i++
				}
				// Add final segment
				result = append(result, current)

				// Convert to list of lists
				resultItems := make([]interface{}, len(result))
				for i, segment := range result {
					segList := NewStoredList(segment)
					id := ctx.executor.storeObject(segList, "list")
					resultItems[i] = Symbol(fmt.Sprintf("\x00LIST:%d\x00", id))
				}
				setListResult(ctx, NewStoredListWithRefs(resultItems, nil, ctx.executor))
				return BoolStatus(true)
			}

			// Single value match: split on occurrences of this value
			var result [][]interface{}
			var current []interface{}

			for _, item := range items {
				if deepEqual(item, delimiter, ctx.executor) {
					// Found delimiter - save current segment
					result = append(result, current)
					current = nil
				} else {
					current = append(current, item)
				}
			}
			// Add final segment
			result = append(result, current)

			// Convert to list of lists
			resultItems := make([]interface{}, len(result))
			for i, segment := range result {
				segList := NewStoredList(segment)
				id := ctx.executor.storeObject(segList, "list")
				resultItems[i] = Symbol(fmt.Sprintf("\x00LIST:%d\x00", id))
			}
			setListResult(ctx, NewStoredListWithRefs(resultItems, nil, ctx.executor))
			return BoolStatus(true)
		}

		// String mode: original behavior
		str := resolveToString(ctx.Args[0], ctx.executor)
		delimiter := resolveToString(ctx.Args[1], ctx.executor)

		parts := strings.Split(str, delimiter)
		items := make([]interface{}, len(parts))
		for i, part := range parts {
			items[i] = part
		}

		setListResult(ctx, NewStoredList(items))
		return BoolStatus(true)
	})

	// join - join list with delimiter (string or list)
	// String delimiter: join ~mylist, ","  -> "a,b,c"
	// List delimiter: join ~mylist, ~delim -> inserts all delim items between each original item
	// Inverse of split
	ps.RegisterCommandInModule("strlist", "join", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: join <list>, <delimiter>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		// Handle StoredList as first argument
		if storedList, ok := ctx.Args[0].(StoredList); ok {
			items := storedList.Items()
			delimiter := ctx.Args[1]

			// Check if delimiter is a list
			if delimList, ok := delimiter.(StoredList); ok {
				// List delimiter: insert all delimiter items between each original item
				delimItems := delimList.Items()

				if len(items) == 0 {
					setListResult(ctx, NewStoredList(nil))
					return BoolStatus(true)
				}

				var resultItems []interface{}
				for i, item := range items {
					resultItems = append(resultItems, item)
					if i < len(items)-1 {
						// Insert delimiter items between original items
						resultItems = append(resultItems, delimItems...)
					}
				}
				setListResult(ctx, NewStoredListWithRefs(resultItems, nil, ctx.executor))
				return BoolStatus(true)
			}

			// String delimiter: join into string (original behavior)
			delimStr := resolveToString(delimiter, ctx.executor)
			strItems := make([]string, len(items))
			for i, item := range items {
				// Resolve each item in case it's a marker
				resolved := ctx.executor.resolveValue(item)
				strItems[i] = fmt.Sprintf("%v", resolved)
			}
			result := strings.Join(strItems, delimStr)
			if ctx.executor != nil {
				result := ctx.executor.maybeStoreValue(result, ctx.state)
				ctx.state.SetResultWithoutClaim(result)
			} else {
				ctx.state.SetResultWithoutClaim(result)
			}
			return BoolStatus(true)
		}

		ctx.LogError(CatType, fmt.Sprintf("First argument must be a list, got %s\n", getTypeName(ctx.Args[0])))
		ctx.SetResult(nil)
		return BoolStatus(false)
	})

	// upper - convert string to uppercase
	// Usage: upper "hello"  -> "HELLO"
	ps.RegisterCommandInModule("strlist", "upper", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: upper <string>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		// Use SetResultWithoutClaim - consumer will claim
		result := strings.ToUpper(str)
		if ctx.executor != nil {
			result := ctx.executor.maybeStoreValue(result, ctx.state)
			ctx.state.SetResultWithoutClaim(result)
		} else {
			ctx.state.SetResultWithoutClaim(result)
		}
		return BoolStatus(true)
	})

	// lower - convert string to lowercase
	// Usage: lower "HELLO"  -> "hello"
	ps.RegisterCommandInModule("strlist", "lower", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: lower <string>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		// Use SetResultWithoutClaim - consumer will claim
		result := strings.ToLower(str)
		if ctx.executor != nil {
			result := ctx.executor.maybeStoreValue(result, ctx.state)
			ctx.state.SetResultWithoutClaim(result)
		} else {
			ctx.state.SetResultWithoutClaim(result)
		}
		return BoolStatus(true)
	})

	// trim - trim values from both ends (polymorphic: strings or lists)
	// String Usage:
	//   trim "  hello  "              -> "hello" (default whitespace)
	//   trim "xxhelloxx", "x"         -> "hello" (override: trim only "x")
	//   trim "xxhello  ",, "x"        -> "hello" (extend: whitespace + "x")
	//   trim "xxhello##",, "x", "#"   -> "hello" (extend: whitespace + "x" + "#")
	// List Usage:
	//   trim ~mylist                  -> removes nil/undefined from both ends
	//   trim ~mylist, "x"             -> removes "x" from both ends (override)
	//   trim ~mylist,, "x"            -> removes nil/undefined and "x" from both ends (extend)
	//   trim ~mylist, (1, 2)          -> removes lists matching (1, 2) by value
	ps.RegisterCommandInModule("strlist", "trim", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: trim <string|list>, [values], [extend_values...]")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		// Helper to check if arg is undefined/skipped
		isUndefined := func(arg interface{}) bool {
			if arg == nil {
				return true
			}
			if sym, ok := arg.(Symbol); ok && string(sym) == "undefined" {
				return true
			}
			return false
		}

		// Check if first argument is a list
		if list, ok := ctx.Args[0].(StoredList); ok {
			// List mode: trim matching values from both ends
			items := list.Items()

			// Build list of values to trim
			var trimValues []interface{}

			if len(ctx.Args) >= 2 && !isUndefined(ctx.Args[1]) {
				// Override mode: use only the specified values
				trimValues = append(trimValues, ctx.Args[1])
				// Also add any additional args (arg 3+) in override mode
				for i := 2; i < len(ctx.Args); i++ {
					if !isUndefined(ctx.Args[i]) {
						trimValues = append(trimValues, ctx.Args[i])
					}
				}
			} else {
				// Default: trim nil/undefined
				trimValues = []interface{}{nil, Symbol("undefined")}
				// Extend mode: add args from position 3 onward
				if len(ctx.Args) >= 3 {
					for i := 2; i < len(ctx.Args); i++ {
						if !isUndefined(ctx.Args[i]) {
							trimValues = append(trimValues, ctx.Args[i])
						}
					}
				}
			}

			// Helper to check if item should be trimmed
			shouldTrim := func(item interface{}) bool {
				for _, trimVal := range trimValues {
					if deepEqual(item, trimVal, ctx.executor) {
						return true
					}
				}
				return false
			}

			// Find start index (skip items that should be trimmed)
			start := 0
			for start < len(items) && shouldTrim(items[start]) {
				start++
			}

			// Find end index (skip items that should be trimmed from end)
			end := len(items)
			for end > start && shouldTrim(items[end-1]) {
				end--
			}

			// Create result list
			resultItems := items[start:end]
			setListResult(ctx, NewStoredList(resultItems))
			return BoolStatus(true)
		}

		// String mode: original behavior
		str := resolveToString(ctx.Args[0], ctx.executor)
		cutset := " \t\n\r" // default whitespace

		// Check for override (arg 2) or extend (arg 3+)
		if len(ctx.Args) >= 2 && !isUndefined(ctx.Args[1]) {
			// Override mode: use only the specified chars
			cutset = resolveToString(ctx.Args[1], ctx.executor)
		} else if len(ctx.Args) >= 3 {
			// Extend mode: add all non-undefined args from position 3 onward
			for i := 2; i < len(ctx.Args); i++ {
				if !isUndefined(ctx.Args[i]) {
					cutset += resolveToString(ctx.Args[i], ctx.executor)
				}
			}
		}

		result := strings.Trim(str, cutset)
		if ctx.executor != nil {
			result := ctx.executor.maybeStoreValue(result, ctx.state)
			ctx.state.SetResultWithoutClaim(result)
		} else {
			ctx.state.SetResultWithoutClaim(result)
		}
		return BoolStatus(true)
	})

	// trim_start - trim values from start (polymorphic: strings or lists)
	// String Usage:
	//   trim_start "  hello  "              -> "hello  " (default whitespace)
	//   trim_start "xxhello", "x"           -> "hello" (override: trim only "x")
	//   trim_start "xxhello",, "x"          -> "hello" (extend: whitespace + "x")
	//   trim_start "##xxhello",, "x", "#"   -> "hello" (extend: whitespace + "x" + "#")
	// List Usage:
	//   trim_start ~mylist                  -> removes nil/undefined from start
	//   trim_start ~mylist, "x"             -> removes "x" from start (override)
	//   trim_start ~mylist,, "x"            -> removes nil/undefined and "x" from start (extend)
	ps.RegisterCommandInModule("strlist", "trim_start", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: trim_start <string|list>, [values], [extend_values...]")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		// Helper to check if arg is undefined/skipped
		isUndefined := func(arg interface{}) bool {
			if arg == nil {
				return true
			}
			if sym, ok := arg.(Symbol); ok && string(sym) == "undefined" {
				return true
			}
			return false
		}

		// Check if first argument is a list
		if list, ok := ctx.Args[0].(StoredList); ok {
			// List mode: trim matching values from start
			items := list.Items()

			// Build list of values to trim
			var trimValues []interface{}

			if len(ctx.Args) >= 2 && !isUndefined(ctx.Args[1]) {
				// Override mode: use only the specified values
				trimValues = append(trimValues, ctx.Args[1])
				for i := 2; i < len(ctx.Args); i++ {
					if !isUndefined(ctx.Args[i]) {
						trimValues = append(trimValues, ctx.Args[i])
					}
				}
			} else {
				// Default: trim nil/undefined
				trimValues = []interface{}{nil, Symbol("undefined")}
				// Extend mode: add args from position 3 onward
				if len(ctx.Args) >= 3 {
					for i := 2; i < len(ctx.Args); i++ {
						if !isUndefined(ctx.Args[i]) {
							trimValues = append(trimValues, ctx.Args[i])
						}
					}
				}
			}

			// Helper to check if item should be trimmed
			shouldTrim := func(item interface{}) bool {
				for _, trimVal := range trimValues {
					if deepEqual(item, trimVal, ctx.executor) {
						return true
					}
				}
				return false
			}

			// Find start index (skip items that should be trimmed)
			start := 0
			for start < len(items) && shouldTrim(items[start]) {
				start++
			}

			// Create result list (keep from start to end)
			resultItems := items[start:]
			setListResult(ctx, NewStoredList(resultItems))
			return BoolStatus(true)
		}

		// String mode: original behavior
		str := resolveToString(ctx.Args[0], ctx.executor)
		cutset := " \t\n\r" // default whitespace

		// Check for override (arg 2) or extend (arg 3+)
		if len(ctx.Args) >= 2 && !isUndefined(ctx.Args[1]) {
			// Override mode: use only the specified chars
			cutset = resolveToString(ctx.Args[1], ctx.executor)
		} else if len(ctx.Args) >= 3 {
			// Extend mode: add all non-undefined args from position 3 onward
			for i := 2; i < len(ctx.Args); i++ {
				if !isUndefined(ctx.Args[i]) {
					cutset += resolveToString(ctx.Args[i], ctx.executor)
				}
			}
		}

		result := strings.TrimLeft(str, cutset)
		if ctx.executor != nil {
			result := ctx.executor.maybeStoreValue(result, ctx.state)
			ctx.state.SetResultWithoutClaim(result)
		} else {
			ctx.state.SetResultWithoutClaim(result)
		}
		return BoolStatus(true)
	})

	// trim_end - trim values from end (polymorphic: strings or lists)
	// String Usage:
	//   trim_end "  hello  "              -> "  hello" (default whitespace)
	//   trim_end "helloxx", "x"           -> "hello" (override: trim only "x")
	//   trim_end "helloxx  ",, "x"        -> "hello" (extend: whitespace + "x")
	//   trim_end "helloxx##",, "x", "#"   -> "hello" (extend: whitespace + "x" + "#")
	// List Usage:
	//   trim_end ~mylist                  -> removes nil/undefined from end
	//   trim_end ~mylist, "x"             -> removes "x" from end (override)
	//   trim_end ~mylist,, "x"            -> removes nil/undefined and "x" from end (extend)
	ps.RegisterCommandInModule("strlist", "trim_end", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: trim_end <string|list>, [values], [extend_values...]")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		// Helper to check if arg is undefined/skipped
		isUndefined := func(arg interface{}) bool {
			if arg == nil {
				return true
			}
			if sym, ok := arg.(Symbol); ok && string(sym) == "undefined" {
				return true
			}
			return false
		}

		// Check if first argument is a list
		if list, ok := ctx.Args[0].(StoredList); ok {
			// List mode: trim matching values from end
			items := list.Items()

			// Build list of values to trim
			var trimValues []interface{}

			if len(ctx.Args) >= 2 && !isUndefined(ctx.Args[1]) {
				// Override mode: use only the specified values
				trimValues = append(trimValues, ctx.Args[1])
				for i := 2; i < len(ctx.Args); i++ {
					if !isUndefined(ctx.Args[i]) {
						trimValues = append(trimValues, ctx.Args[i])
					}
				}
			} else {
				// Default: trim nil/undefined
				trimValues = []interface{}{nil, Symbol("undefined")}
				// Extend mode: add args from position 3 onward
				if len(ctx.Args) >= 3 {
					for i := 2; i < len(ctx.Args); i++ {
						if !isUndefined(ctx.Args[i]) {
							trimValues = append(trimValues, ctx.Args[i])
						}
					}
				}
			}

			// Helper to check if item should be trimmed
			shouldTrim := func(item interface{}) bool {
				for _, trimVal := range trimValues {
					if deepEqual(item, trimVal, ctx.executor) {
						return true
					}
				}
				return false
			}

			// Find end index (skip items that should be trimmed from end)
			end := len(items)
			for end > 0 && shouldTrim(items[end-1]) {
				end--
			}

			// Create result list (keep from 0 to end)
			resultItems := items[:end]
			setListResult(ctx, NewStoredList(resultItems))
			return BoolStatus(true)
		}

		// String mode: original behavior
		str := resolveToString(ctx.Args[0], ctx.executor)
		cutset := " \t\n\r" // default whitespace

		// Check for override (arg 2) or extend (arg 3+)
		if len(ctx.Args) >= 2 && !isUndefined(ctx.Args[1]) {
			// Override mode: use only the specified chars
			cutset = resolveToString(ctx.Args[1], ctx.executor)
		} else if len(ctx.Args) >= 3 {
			// Extend mode: add all non-undefined args from position 3 onward
			for i := 2; i < len(ctx.Args); i++ {
				if !isUndefined(ctx.Args[i]) {
					cutset += resolveToString(ctx.Args[i], ctx.executor)
				}
			}
		}

		result := strings.TrimRight(str, cutset)
		if ctx.executor != nil {
			result := ctx.executor.maybeStoreValue(result, ctx.state)
			ctx.state.SetResultWithoutClaim(result)
		} else {
			ctx.state.SetResultWithoutClaim(result)
		}
		return BoolStatus(true)
	})

	// contains - check if string contains substring, or if list contains item
	// Usage: contains "hello world", "world"  -> true
	//        contains ~mylist, "item"         -> true if item in list
	//        contains ~mylist, ~sublist       -> true if sublist in list (deep comparison)
	ps.RegisterCommandInModule("strlist", "contains", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: contains <string|list>, <substring|item>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		value := ctx.Args[0]
		search := ctx.Args[1]

		// Check if first argument is a list
		if list, ok := value.(StoredList); ok {
			// List mode: check if item exists using deep comparison
			for _, item := range list.Items() {
				if deepEqual(item, search, ctx.executor) {
					ctx.SetResult(true)
					return BoolStatus(true)
				}
			}
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		// String mode: check if substring exists
		str := resolveToString(value, ctx.executor)
		substr := resolveToString(search, ctx.executor)

		result := strings.Contains(str, substr)
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	// index - find first index of substring or item (-1 if not found)
	// Usage: index "hello world", "world"  -> 6
	//        index ~mylist, "item"          -> position of item in list
	//        index ~mylist, ~sublist        -> position of sublist in list (deep comparison)
	// Returns -1 if not found (like many languages)
	// Always succeeds and sets result (use result to check if found)
	ps.RegisterCommandInModule("strlist", "index", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: index <string|list>, <substring|item>")
			ctx.SetResult(int64(-1))
			return BoolStatus(false)
		}

		value := ctx.Args[0]
		search := ctx.Args[1]

		// Check if first argument is a list
		if list, ok := value.(StoredList); ok {
			// List mode: find index of item using deep comparison
			for i, item := range list.Items() {
				if deepEqual(item, search, ctx.executor) {
					ctx.SetResult(int64(i))
					return BoolStatus(true)
				}
			}
			ctx.SetResult(int64(-1))
			return BoolStatus(true)
		}

		// String mode: find index of substring
		str := resolveToString(value, ctx.executor)
		substr := resolveToString(search, ctx.executor)

		index := strings.Index(str, substr)
		ctx.SetResult(int64(index))
		// Always return success - caller checks result value
		return BoolStatus(true)
	})

	// replace - replace occurrences in string or list
	// String Usage: replace "hello world world", "world", "gopher"  -> "hello gopher gopher" (all)
	//               replace "hello world world", "world", "gopher", 1  -> "hello gopher world" (first 1)
	//               replace "hello world world", "world", "gopher", -1 -> "hello world gopher" (last 1)
	// List Usage:   replace ~list, old, new       -> replace all occurrences (single value or sequence)
	//               replace ~list, old, new, N    -> replace first N (positive) or last N (negative)
	// Count: omitted or 0 = all, positive N = first N, negative N = last N
	ps.RegisterCommandInModule("strlist", "replace", func(ctx *Context) Result {
		if len(ctx.Args) < 3 {
			ctx.LogError(CatCommand, "Usage: replace <string|list>, <old>, <new> [, count]")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		// Parse optional count argument (4th arg)
		count := int64(0) // 0 means replace all
		if len(ctx.Args) >= 4 {
			countArg := ctx.Args[3]
			switch v := countArg.(type) {
			case int64:
				count = v
			case float64:
				count = int64(v)
			default:
				countStr := resolveToString(countArg, ctx.executor)
				if parsed, err := strconv.ParseInt(countStr, 10, 64); err == nil {
					count = parsed
				}
			}
		}

		// Check if first argument is a list
		if list, ok := ctx.Args[0].(StoredList); ok {
			items := list.Items()
			oldVal := ctx.Args[1]
			newVal := ctx.Args[2]

			// Check if old value is a list (sequence match mode)
			// List old = sequence to find, new = sequence to splice in
			if oldList, ok := oldVal.(StoredList); ok {
				oldItems := oldList.Items()
				oldLen := len(oldItems)

				if oldLen == 0 {
					// Empty old sequence - return original list
					setListResult(ctx, list)
					return BoolStatus(true)
				}

				// Find all sequence match positions
				var matchPositions []int
				for i := 0; i+oldLen <= len(items); i++ {
					match := true
					for j := 0; j < oldLen; j++ {
						if !deepEqual(items[i+j], oldItems[j], ctx.executor) {
							match = false
							break
						}
					}
					if match {
						matchPositions = append(matchPositions, i)
						i += oldLen - 1 // Skip to end of this match (loop will add 1)
					}
				}

				// Determine which matches to replace based on count
				replaceSet := make(map[int]bool)
				if count == 0 || len(matchPositions) == 0 {
					// Replace all
					for _, pos := range matchPositions {
						replaceSet[pos] = true
					}
				} else if count > 0 {
					// Replace first N
					n := int(count)
					if n > len(matchPositions) {
						n = len(matchPositions)
					}
					for i := 0; i < n; i++ {
						replaceSet[matchPositions[i]] = true
					}
				} else {
					// Replace last N (count is negative)
					n := int(-count)
					if n > len(matchPositions) {
						n = len(matchPositions)
					}
					for i := len(matchPositions) - n; i < len(matchPositions); i++ {
						replaceSet[matchPositions[i]] = true
					}
				}

				// Build result by iterating through items
				// For sequence mode: new is spliced in as items
				var result []interface{}
				i := 0
				for i < len(items) {
					if replaceSet[i] {
						// Replace this sequence with newVal items
						if newList, ok := newVal.(StoredList); ok {
							result = append(result, newList.Items()...)
						} else {
							result = append(result, newVal)
						}
						i += oldLen
					} else {
						result = append(result, items[i])
						i++
					}
				}

				setListResult(ctx, NewStoredListWithRefs(result, nil, ctx.executor))
				return BoolStatus(true)
			}

			// Single value match mode
			// Single old = find that item, new replaces exactly as one element
			// Find all match positions first
			var matchPositions []int
			for i, item := range items {
				if deepEqual(item, oldVal, ctx.executor) {
					matchPositions = append(matchPositions, i)
				}
			}

			// Determine which matches to replace based on count
			replaceSet := make(map[int]bool)
			if count == 0 || len(matchPositions) == 0 {
				// Replace all
				for _, pos := range matchPositions {
					replaceSet[pos] = true
				}
			} else if count > 0 {
				// Replace first N
				n := int(count)
				if n > len(matchPositions) {
					n = len(matchPositions)
				}
				for i := 0; i < n; i++ {
					replaceSet[matchPositions[i]] = true
				}
			} else {
				// Replace last N (count is negative)
				n := int(-count)
				if n > len(matchPositions) {
					n = len(matchPositions)
				}
				for i := len(matchPositions) - n; i < len(matchPositions); i++ {
					replaceSet[matchPositions[i]] = true
				}
			}

			// Build result
			// For single-value mode: new replaces exactly as one element (even if it's a list)
			var result []interface{}
			for i, item := range items {
				if replaceSet[i] {
					// Replace this item with newVal exactly (as single element)
					result = append(result, newVal)
				} else {
					result = append(result, item)
				}
			}

			setListResult(ctx, NewStoredListWithRefs(result, nil, ctx.executor))
			return BoolStatus(true)
		}

		// String mode
		str := resolveToString(ctx.Args[0], ctx.executor)
		old := resolveToString(ctx.Args[1], ctx.executor)
		newStr := resolveToString(ctx.Args[2], ctx.executor)

		var result string
		if count == 0 {
			// Replace all
			result = strings.ReplaceAll(str, old, newStr)
		} else if count > 0 {
			// Replace first N occurrences
			result = strings.Replace(str, old, newStr, int(count))
		} else {
			// Replace last N occurrences
			// Need to find positions and replace from the end
			n := int(-count)
			positions := findAllStringPositions(str, old)
			if len(positions) == 0 {
				result = str
			} else {
				// Determine which positions to replace (last N)
				startIdx := len(positions) - n
				if startIdx < 0 {
					startIdx = 0
				}
				replacePositions := positions[startIdx:]

				// Build result by replacing only those positions
				var sb strings.Builder
				lastEnd := 0
				posSet := make(map[int]bool)
				for _, pos := range replacePositions {
					posSet[pos] = true
				}
				for _, pos := range positions {
					sb.WriteString(str[lastEnd:pos])
					if posSet[pos] {
						sb.WriteString(newStr)
					} else {
						sb.WriteString(old)
					}
					lastEnd = pos + len(old)
				}
				sb.WriteString(str[lastEnd:])
				result = sb.String()
			}
		}

		if ctx.executor != nil {
			storedResult := ctx.executor.maybeStoreValue(result, ctx.state)
			ctx.state.SetResultWithoutClaim(storedResult)
		} else {
			ctx.state.SetResultWithoutClaim(result)
		}
		return BoolStatus(true)
	})

	// starts_with - check if string starts with prefix, or list starts with value(s)
	// String Usage: starts_with "hello world", "hello"  -> true
	// List Usage:
	//   starts_with ~mylist, "x"       -> true if list starts with "x"
	//   starts_with ~mylist, ~prefix   -> true if list starts with all items in prefix (in sequence)
	ps.RegisterCommandInModule("strlist", "starts_with", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: starts_with <string|list>, <prefix|value>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		if len(ctx.Args) > 2 {
			ctx.LogError(CatCommand, "starts_with takes exactly 2 arguments")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		// Check if first argument is a list
		if list, ok := ctx.Args[0].(StoredList); ok {
			items := list.Items()
			prefix := ctx.Args[1]

			// Check if prefix is a list (sequence match) or single value
			if prefixList, ok := prefix.(StoredList); ok {
				// Sequence match: check if list starts with all items in prefixList
				prefixItems := prefixList.Items()
				if len(prefixItems) > len(items) {
					ctx.SetResult(false)
					return BoolStatus(false)
				}
				for i, prefixItem := range prefixItems {
					if !deepEqual(items[i], prefixItem, ctx.executor) {
						ctx.SetResult(false)
						return BoolStatus(false)
					}
				}
				ctx.SetResult(true)
				return BoolStatus(true)
			}

			// Single value match: check if list starts with this value
			if len(items) == 0 {
				ctx.SetResult(false)
				return BoolStatus(false)
			}
			result := deepEqual(items[0], prefix, ctx.executor)
			ctx.SetResult(result)
			return BoolStatus(result)
		}

		// String mode: original behavior
		str := resolveToString(ctx.Args[0], ctx.executor)
		prefix := resolveToString(ctx.Args[1], ctx.executor)

		result := strings.HasPrefix(str, prefix)
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	// ends_with - check if string ends with suffix, or list ends with value(s)
	// String Usage: ends_with "hello world", "world"  -> true
	// List Usage:
	//   ends_with ~mylist, "x"       -> true if list ends with "x"
	//   ends_with ~mylist, ~suffix   -> true if list ends with all items in suffix (in sequence)
	ps.RegisterCommandInModule("strlist", "ends_with", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: ends_with <string|list>, <suffix|value>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		if len(ctx.Args) > 2 {
			ctx.LogError(CatCommand, "ends_with takes exactly 2 arguments")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		// Check if first argument is a list
		if list, ok := ctx.Args[0].(StoredList); ok {
			items := list.Items()
			suffix := ctx.Args[1]

			// Check if suffix is a list (sequence match) or single value
			if suffixList, ok := suffix.(StoredList); ok {
				// Sequence match: check if list ends with all items in suffixList
				suffixItems := suffixList.Items()
				if len(suffixItems) > len(items) {
					ctx.SetResult(false)
					return BoolStatus(false)
				}
				startIdx := len(items) - len(suffixItems)
				for i, suffixItem := range suffixItems {
					if !deepEqual(items[startIdx+i], suffixItem, ctx.executor) {
						ctx.SetResult(false)
						return BoolStatus(false)
					}
				}
				ctx.SetResult(true)
				return BoolStatus(true)
			}

			// Single value match: check if list ends with this value
			if len(items) == 0 {
				ctx.SetResult(false)
				return BoolStatus(false)
			}
			result := deepEqual(items[len(items)-1], suffix, ctx.executor)
			ctx.SetResult(result)
			return BoolStatus(result)
		}

		// String mode: original behavior
		str := resolveToString(ctx.Args[0], ctx.executor)
		suffix := resolveToString(ctx.Args[1], ctx.executor)

		result := strings.HasSuffix(str, suffix)
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	// repeat - polymorphic repetition command
	// String mode: repeat "abc", 3 -> "abcabcabc"
	// List mode: repeat ~mylist, 3 -> items repeated 3 times
	// Block mode: repeat (code), 5, [counter_var] -> runs block 5 times, collects results
	//   - Optional 3rd arg is variable name to store iteration counter (0-based)
	//   - Returns list of results from each iteration (nil for undefined results)
	//   - If any iteration fails, adds failures: (list of failed iteration numbers)
	ps.RegisterCommandInModule("strlist", "repeat", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: repeat <string|list|block>, <count>, [counter_var]")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		countNum, ok := toNumber(ctx.Args[1])
		if !ok {
			ctx.LogError(CatArgument, "Count must be a number")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
		count := int(countNum)
		if count < 0 {
			count = 0
		}

		value := ctx.Args[0]

		// Check for block mode first (ParenGroup or StoredBlock marker)
		var bodyBlock string
		isBlock := false

		if block, ok := value.(ParenGroup); ok {
			bodyBlock = string(block)
			isBlock = true
		} else if sym, ok := value.(Symbol); ok {
			// Check if it's a block marker
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "block" && objectID >= 0 {
				if obj, exists := ctx.executor.getObject(objectID); exists {
					if storedBlock, ok := obj.(StoredBlock); ok {
						bodyBlock = string(storedBlock)
						isBlock = true
					}
				}
			}
		}

		if isBlock {
			// Block mode - execute block count times, collect results

			// Get optional counter variable name
			var counterVar string
			if len(ctx.Args) >= 3 {
				counterVar = fmt.Sprintf("%v", ctx.Args[2])
			}

			// Parse body into commands once
			parser := NewParser(bodyBlock, "")
			cleanedBody := parser.RemoveComments(bodyBlock)
			normalizedBody := parser.NormalizeKeywords(cleanedBody)
			bodyCommands, err := parser.ParseCommandSequence(normalizedBody)
			if err != nil {
				ctx.LogError(CatCommand, fmt.Sprintf("repeat: failed to parse body: %v", err))
				ctx.SetResult(nil)
				return BoolStatus(false)
			}

			results := make([]interface{}, 0, count)
			var failures []interface{}

			for iteration := 0; iteration < count; iteration++ {
				// Set counter variable if specified
				if counterVar != "" {
					ctx.state.SetVariable(counterVar, int64(iteration))
				}

				// Clear result for this iteration
				ctx.state.ClearResult()

				// Execute body commands
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

					// Check for yield - attach repeat continuation
					if yieldResult, ok := result.(YieldResult); ok {
						outerCont := &RepeatContinuation{
							BodyBlock:         bodyBlock,
							CachedBodyCmds:    bodyCommands, // Cache full body for reuse
							RemainingBodyCmds: bodyCommands[cmdIdx+1:],
							BodyCmdIndex:      cmdIdx,
							CurrentIteration:  iteration,
							TotalIterations:   count,
							CounterVar:        counterVar,
							Results:           results,
							Failures:          failures,
							State:             ctx.state,
						}
						if yieldResult.RepeatContinuation == nil {
							yieldResult.RepeatContinuation = outerCont
						} else {
							yieldResult.RepeatContinuation.ParentContinuation = outerCont
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
							// Return current results collected so far
							var namedArgs map[string]interface{}
							if len(failures) > 0 {
								namedArgs = map[string]interface{}{
									"failures": NewStoredList(failures),
								}
							}
							resultList := NewStoredListWithNamed(results, namedArgs)
							setListResult(ctx, resultList)
							return BoolStatus(true)
						}
						return BreakResult{Levels: breakResult.Levels - 1}
					}

					// Check for continue - skip to next iteration
					if continueResult, ok := result.(ContinueResult); ok {
						if continueResult.Levels <= 1 {
							// Break out of inner command loop to go to next iteration
							break
						}
						return ContinueResult{Levels: continueResult.Levels - 1}
					}

					// Handle async in body
					if bodyToken, isToken := result.(TokenResult); isToken {
						tokenID := string(bodyToken)
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

				// Collect result for this iteration
				iterResult := ctx.state.GetResult()
				results = append(results, iterResult)

				// Track failure if final status of iteration is false
				if !lastStatus {
					failures = append(failures, int64(iteration))
				}
			}

			// Build result list with optional failures named arg
			var namedArgs map[string]interface{}
			if len(failures) > 0 {
				namedArgs = map[string]interface{}{
					"failures": NewStoredList(failures),
				}
			}

			resultList := NewStoredListWithNamed(results, namedArgs)
			setListResult(ctx, resultList)
			return BoolStatus(true)
		}

		// Check for list mode
		if list, isList := value.(StoredList); isList {
			// List mode - repeat list items count times
			items := list.Items()
			newItems := make([]interface{}, 0, len(items)*count)
			for i := 0; i < count; i++ {
				newItems = append(newItems, items...)
			}
			resultList := NewStoredListWithNamed(newItems, list.NamedArgs())
			setListResult(ctx, resultList)
			return BoolStatus(true)
		}

		// String mode (default) - repeat string count times
		str := resolveToString(value, ctx.executor)
		result := strings.Repeat(str, count)
		if ctx.executor != nil {
			stored := ctx.executor.maybeStoreValue(result, ctx.state)
			ctx.state.SetResultWithoutClaim(stored)
		} else {
			ctx.state.SetResultWithoutClaim(result)
		}
		return BoolStatus(true)
	})

	// sort - sort a list with optional custom comparator
	// Usage: sort ~mylist                           -> sorted with default ordering
	//        sort ~mylist, (lt $1, $2)              -> sorted with custom comparator
	//        sort ~mylist, myComparator             -> sorted using named macro
	//        sort ~mylist, desc: true               -> sorted descending
	//        sort ~mylist, ~comparatorRef, desc: true
	//
	// Default sort order (ascending):
	//   nil < false < true < numbers (low to high) < symbols (alpha) < strings (alpha) < other (original order)
	//
	// Custom comparator receives two items as $1 and $2, returns true if $1 should come before $2
	ps.RegisterCommandInModule("strlist", "sort", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: sort <list>, [comparator], [desc: true|false]")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		// Parse desc: named argument (default false)
		descending := false
		if descVal, hasDesc := ctx.NamedArgs["desc"]; hasDesc {
			descending = toBool(descVal)
		}

		// Get the list to sort
		value := ctx.Args[0]
		var items []interface{}
		var namedArgs map[string]interface{}

		// Handle different input types (like len does)
		switch v := value.(type) {
		case StoredList:
			items = make([]interface{}, len(v.Items()))
			copy(items, v.Items())
			namedArgs = v.NamedArgs()
		case ParenGroup:
			parsed, parsedNamed := parseArguments(string(v))
			items = parsed
			namedArgs = parsedNamed
		default:
			// Try to resolve as marker
			if sym, ok := value.(Symbol); ok {
				markerType, objectID := parseObjectMarker(string(sym))
				if markerType == "list" && objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						if list, ok := obj.(StoredList); ok {
							items = make([]interface{}, len(list.Items()))
							copy(items, list.Items())
							namedArgs = list.NamedArgs()
						}
					}
				}
			}
			if items == nil {
				ctx.LogError(CatType, fmt.Sprintf("Cannot sort type %s", getTypeName(value)))
				ctx.SetResult(nil)
				return BoolStatus(false)
			}
		}

		// Check for custom comparator (second positional argument)
		var hasCustomComparator bool
		var comparator interface{}
		if len(ctx.Args) >= 2 {
			hasCustomComparator = true
			comparator = ctx.Args[1]
		}

		if hasCustomComparator {
			// Sort with custom comparator
			// We need to call the comparator for each comparison
			// Use a simple bubble sort to handle potential async comparators

			n := len(items)
			for i := 0; i < n-1; i++ {
				for j := 0; j < n-i-1; j++ {
					// Call comparator with items[j] and items[j+1]
					// If comparator returns false, swap them
					shouldSwap, err := callComparator(ps, ctx, comparator, items[j], items[j+1])
					if err != nil {
						ctx.LogError(CatCommand, fmt.Sprintf("Comparator error: %v", err))
						ctx.SetResult(nil)
						return BoolStatus(false)
					}
					// comparator returns true if first < second (should come before)
					// so we swap if comparator returns false (first >= second)
					if !shouldSwap {
						items[j], items[j+1] = items[j+1], items[j]
					}
				}
			}
		} else {
			// Default sort using Go's native sorting
			sortItemsDefaultWithExecutor(items, ctx.executor)
		}

		// Reverse if descending
		if descending {
			for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
				items[i], items[j] = items[j], items[i]
			}
		}

		// Create result list with sorted items and preserved named args
		resultList := NewStoredListWithNamed(items, namedArgs)
		setListResult(ctx, resultList)
		return BoolStatus(true)
	})

	// Helper function to extract string content from various types for regex operations
	extractStringContent := func(value interface{}, executor *Executor) (string, string) {
		// Returns (content, sourceType) where sourceType is "string", "bytes", "block", "symbol"
		if executor != nil {
			value = executor.resolveValue(value)
		}
		switch v := value.(type) {
		case string:
			return v, "string"
		case QuotedString:
			return string(v), "string"
		case StoredString:
			return string(v), "string"
		case StoredBytes:
			return string(v.Data()), "bytes"
		case []byte:
			return string(v), "bytes"
		case ParenGroup:
			return string(v), "block"
		case Symbol:
			return string(v), "symbol"
		default:
			return fmt.Sprintf("%v", v), "string"
		}
	}

	// Helper to return result in compatible type
	returnCompatibleType := func(ctx *Context, result string, sourceType string) {
		switch sourceType {
		case "bytes":
			ctx.SetResult(NewStoredBytes([]byte(result)))
		case "block":
			ctx.SetResult(ParenGroup(result))
		case "symbol":
			ctx.SetResult(Symbol(result))
		default:
			ctx.SetResult(result)
		}
	}

	// Helper to extract regex pattern from various input types
	extractPattern := func(value interface{}, executor *Executor) string {
		if executor != nil {
			value = executor.resolveValue(value)
		}
		switch v := value.(type) {
		case string:
			return v
		case QuotedString:
			return string(v)
		case StoredString:
			return string(v)
		case ParenGroup:
			return string(v)
		case Symbol:
			return string(v)
		default:
			return fmt.Sprintf("%v", v)
		}
	}

	// match - test if a regex pattern matches the input
	// Usage: match <input>, <pattern> [, case_insensitive: true]
	// Returns true/false for match success
	// Input can be string, bytes, block (ParenGroup), or symbol
	// Pattern can be literal string, block, or symbol
	ps.RegisterCommandInModule("strlist", "match", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: match <input>, <pattern> [, case_insensitive: true]")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		input, _ := extractStringContent(ctx.Args[0], ctx.executor)
		pattern := extractPattern(ctx.Args[1], ctx.executor)

		// Handle case_insensitive option
		if ci, hasCi := ctx.NamedArgs["case_insensitive"]; hasCi && toBool(ci) {
			pattern = "(?i)" + pattern
		}

		re, err := regexp.Compile(pattern)
		if err != nil {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid regex pattern: %v", err))
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		matched := re.MatchString(input)
		ctx.SetResult(matched)
		return BoolStatus(matched)
	})

	// regex_find - find all matches and capture groups
	// Usage: regex_find <input>, <pattern> [, all: true] [, case_insensitive: true]
	// Returns: single match returns list of [fullMatch, group1, group2, ...]
	//          all: true returns list of lists [[fullMatch, g1, g2], [fullMatch, g1, g2], ...]
	// Returns nil if no match found
	ps.RegisterCommandInModule("strlist", "regex_find", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: regex_find <input>, <pattern> [, all: true] [, case_insensitive: true]")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		input, _ := extractStringContent(ctx.Args[0], ctx.executor)
		pattern := extractPattern(ctx.Args[1], ctx.executor)

		// Handle options
		findAll := false
		if allVal, hasAll := ctx.NamedArgs["all"]; hasAll {
			findAll = toBool(allVal)
		}
		if ci, hasCi := ctx.NamedArgs["case_insensitive"]; hasCi && toBool(ci) {
			pattern = "(?i)" + pattern
		}

		re, err := regexp.Compile(pattern)
		if err != nil {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid regex pattern: %v", err))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		if findAll {
			// Find all matches with capture groups
			allMatches := re.FindAllStringSubmatch(input, -1)
			if allMatches == nil || len(allMatches) == 0 {
				ctx.SetResult(nil)
				return BoolStatus(true)
			}

			// Convert to list of lists
			var resultItems []interface{}
			for _, match := range allMatches {
				var matchItems []interface{}
				for _, group := range match {
					matchItems = append(matchItems, group)
				}
				matchList := NewStoredList(matchItems)
				matchID := ctx.executor.storeObject(matchList, "list")
				resultItems = append(resultItems, Symbol(fmt.Sprintf("\x00LIST:%d\x00", matchID)))
			}
			// Use NewStoredListWithRefs to properly claim references to nested lists
			resultList := NewStoredListWithRefs(resultItems, nil, ctx.executor)
			setListResult(ctx, resultList)
			return BoolStatus(true)
		} else {
			// Find first match with capture groups
			match := re.FindStringSubmatch(input)
			if match == nil {
				ctx.SetResult(nil)
				return BoolStatus(true)
			}

			// Convert to list
			var matchItems []interface{}
			for _, group := range match {
				matchItems = append(matchItems, group)
			}
			resultList := NewStoredList(matchItems)
			setListResult(ctx, resultList)
			return BoolStatus(true)
		}
	})

	// regex_replace - replace matches with replacement string
	// Usage: regex_replace <input>, <pattern>, <replacement> [, count: N] [, case_insensitive: true]
	// Replacement can use $1, $2, etc. for capture groups, or ${name} for named groups
	// count: 0 or omitted = replace all, positive = replace first N, negative = replace last N
	// Returns modified string/bytes/block (same type as input)
	ps.RegisterCommandInModule("strlist", "regex_replace", func(ctx *Context) Result {
		if len(ctx.Args) < 3 {
			ctx.LogError(CatCommand, "Usage: regex_replace <input>, <pattern>, <replacement> [, count: N] [, case_insensitive: true]")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		input, sourceType := extractStringContent(ctx.Args[0], ctx.executor)
		pattern := extractPattern(ctx.Args[1], ctx.executor)
		replacement := extractPattern(ctx.Args[2], ctx.executor)

		// Handle options
		count := int64(0) // 0 = replace all
		if countVal, hasCount := ctx.NamedArgs["count"]; hasCount {
			if num, ok := toNumber(countVal); ok {
				count = int64(num)
			}
		}
		if ci, hasCi := ctx.NamedArgs["case_insensitive"]; hasCi && toBool(ci) {
			pattern = "(?i)" + pattern
		}

		re, err := regexp.Compile(pattern)
		if err != nil {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid regex pattern: %v", err))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		var result string
		if count == 0 {
			// Replace all
			result = re.ReplaceAllString(input, replacement)
		} else if count > 0 {
			// Replace first N matches
			replaced := 0
			result = re.ReplaceAllStringFunc(input, func(match string) string {
				if int64(replaced) < count {
					replaced++
					// Expand replacement with capture groups
					return re.ReplaceAllString(match, replacement)
				}
				return match
			})
		} else {
			// Replace last N matches (count is negative)
			n := int(-count)
			matches := re.FindAllStringIndex(input, -1)
			if matches == nil || len(matches) == 0 {
				result = input
			} else {
				// Determine which matches to replace (last N)
				replaceSet := make(map[int]bool)
				start := len(matches) - n
				if start < 0 {
					start = 0
				}
				for i := start; i < len(matches); i++ {
					replaceSet[matches[i][0]] = true
				}

				// Replace only the selected matches
				var builder strings.Builder
				lastEnd := 0
				for _, match := range matches {
					matchStart, matchEnd := match[0], match[1]
					builder.WriteString(input[lastEnd:matchStart])
					if replaceSet[matchStart] {
						// This match should be replaced
						matchedStr := input[matchStart:matchEnd]
						builder.WriteString(re.ReplaceAllString(matchedStr, replacement))
					} else {
						builder.WriteString(input[matchStart:matchEnd])
					}
					lastEnd = matchEnd
				}
				builder.WriteString(input[lastEnd:])
				result = builder.String()
			}
		}

		returnCompatibleType(ctx, result, sourceType)
		return BoolStatus(true)
	})

	// string - convert any value to its string representation
	// Usage: string 123      -> "123"
	//        string 3.14     -> "3.14"
	//        string true     -> "true"
	//        string nil      -> "nil"
	//        string ~mylist  -> "(1, 2, 3)"
	//        string ~myblock -> block content as string
	ps.RegisterCommandInModule("types", "string", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: string <value>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		// Resolve any markers
		resolved := value
		if ctx.executor != nil {
			resolved = ctx.executor.resolveValue(value)
		}

		var result string

		switch v := resolved.(type) {
		case nil:
			result = "nil"
		case bool:
			if v {
				result = "true"
			} else {
				result = "false"
			}
		case int64:
			result = strconv.FormatInt(v, 10)
		case int:
			result = strconv.Itoa(v)
		case float64:
			// Format float without unnecessary trailing zeros
			result = strconv.FormatFloat(v, 'f', -1, 64)
		case Symbol:
			s := string(v)
			// Check for special symbols
			if s == "nil" || s == "undefined" || s == "true" || s == "false" {
				result = s
			} else {
				result = s
			}
		case QuotedString:
			result = string(v)
		case StoredString:
			result = string(v)
		case string:
			result = v
		case StoredList:
			// Check for pretty parameter
			pretty := false
			if prettyArg, exists := ctx.NamedArgs["pretty"]; exists {
				switch pv := prettyArg.(type) {
				case bool:
					pretty = pv
				case Symbol:
					pretty = string(pv) == "true" || string(pv) == "1"
				case string:
					pretty = pv == "true" || pv == "1"
				}
			}
			// Check for color parameter - can be true or a list with color overrides
			var colorCfg *DisplayColorConfig
			if colorArg, exists := ctx.NamedArgs["color"]; exists {
				// Check if it's false/0 to explicitly disable
				isDisabled := false
				switch cv := colorArg.(type) {
				case bool:
					isDisabled = !cv
				case Symbol:
					s := string(cv)
					isDisabled = s == "false" || s == "0"
				case string:
					isDisabled = cv == "false" || cv == "0"
				}
				if !isDisabled {
					cfg := ParseDisplayColorConfig(colorArg, ctx.executor)
					colorCfg = &cfg
				}
			}
			// Use formatListForDisplay for lists
			if colorCfg != nil {
				result = formatListForDisplayColored(v, 0, pretty, *colorCfg)
			} else if pretty {
				result = formatListForDisplayPretty(v, 0)
			} else {
				result = formatListForDisplay(v)
			}
		case ParenGroup:
			// Block/code - return the block content
			result = string(v)
		case StoredBlock:
			// Stored block - return the block content
			result = string(v)
		case StoredMacro, *StoredMacro:
			result = "<macro>"
		case *StoredCommand:
			if v.CommandName != "" {
				result = fmt.Sprintf("<command %s>", v.CommandName)
			} else {
				result = "<command>"
			}
		case *StoredChannel:
			if ctx.executor != nil {
				if id := ctx.executor.findStoredChannelID(v); id >= 0 {
					result = fmt.Sprintf("<channel %d>", id)
				} else {
					result = "<channel>"
				}
			} else {
				result = "<channel>"
			}
		case *FiberHandle:
			if ctx.executor != nil {
				if id := ctx.executor.findStoredFiberID(v); id >= 0 {
					result = fmt.Sprintf("<fiber %d>", id)
				} else {
					result = "<fiber>"
				}
			} else {
				result = "<fiber>"
			}
		default:
			// Fallback to fmt.Sprintf
			result = fmt.Sprintf("%v", v)
		}

		// Store the result string if needed
		if ctx.executor != nil {
			stored := ctx.executor.maybeStoreValue(result, ctx.state)
			ctx.state.SetResultWithoutClaim(stored)
		} else {
			ctx.state.SetResultWithoutClaim(result)
		}
		return BoolStatus(true)
	})

	// float - convert value to float, with optional default on failure
	// Usage: float "3.14"           -> 3.14
	//        float "abc"            -> nil
	//        float "abc", 0         -> 0
	//        float 42               -> 42.0
	//        float "123"            -> 123.0
	ps.RegisterCommandInModule("types", "float", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: float <value>, [default]")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		// Resolve any markers
		resolved := value
		if ctx.executor != nil {
			resolved = ctx.executor.resolveValue(value)
		}

		// Try to convert to float
		if num, ok := toNumber(resolved); ok {
			ctx.SetResult(num)
			return BoolStatus(true)
		}

		// Conversion failed - return default or nil
		if len(ctx.Args) >= 2 {
			ctx.SetResult(ctx.Args[1])
		} else {
			ctx.SetResult(nil)
		}
		return BoolStatus(true)
	})

	// number - convert value to number (int64 if exact, float64 otherwise)
	// Usage: number "42"             -> 42 (int64)
	//        number "3.14"           -> 3.14 (float64)
	//        number "abc"            -> nil + failure
	//        number "abc", 0         -> 0 (default on failure)
	//        number 42.0             -> 42 (int64, exact)
	//        number 42.5             -> 42.5 (float64, not exact)
	ps.RegisterCommandInModule("types", "number", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: number <value>, [default]")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		// Resolve any markers
		resolved := value
		if ctx.executor != nil {
			resolved = ctx.executor.resolveValue(value)
		}

		// Try to convert to number
		if num, ok := toNumber(resolved); ok {
			// Check if it can be represented as an integer
			intVal := int64(num)
			if float64(intVal) == num {
				ctx.SetResult(intVal)
			} else {
				ctx.SetResult(num)
			}
			return BoolStatus(true)
		}

		// Conversion failed - return default or nil with failure
		if len(ctx.Args) >= 2 {
			ctx.SetResult(ctx.Args[1])
			return BoolStatus(true) // Default provided, success
		}
		ctx.LogError(CatType, "Cannot convert to number")
		ctx.SetResult(nil)
		return BoolStatus(false)
	})

	// bool - check truthiness and return true/false symbol
	// Usage: bool 1         -> true
	//        bool 0         -> false
	//        bool ""        -> false
	//        bool "hello"   -> true
	//        bool nil       -> false
	//        bool ~list     -> true (non-nil)
	ps.RegisterCommandInModule("types", "bool", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: bool <value>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		// Resolve any markers
		resolved := value
		if ctx.executor != nil {
			resolved = ctx.executor.resolveValue(value)
		}

		// Check truthiness
		if isTruthy(resolved) {
			ctx.SetResult(true)
		} else {
			ctx.SetResult(false)
		}
		return BoolStatus(true)
	})

	// symbol - convert any value to a symbol
	// Like string but returns Symbol type; fails if result contains null bytes
	// Usage: symbol 123           -> 123 (as symbol)
	//        symbol "hello"       -> hello (as symbol)
	//        symbol ~list, default -> default if contains nulls
	ps.RegisterCommandInModule("types", "symbol", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: symbol <value>, [default]")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		// Resolve any markers
		resolved := value
		if ctx.executor != nil {
			resolved = ctx.executor.resolveValue(value)
		}

		var result string

		switch v := resolved.(type) {
		case nil:
			result = "nil"
		case bool:
			if v {
				result = "true"
			} else {
				result = "false"
			}
		case int64:
			result = strconv.FormatInt(v, 10)
		case int:
			result = strconv.Itoa(v)
		case float64:
			// Format float without unnecessary trailing zeros
			result = strconv.FormatFloat(v, 'f', -1, 64)
		case Symbol:
			result = string(v)
		case QuotedString:
			result = string(v)
		case StoredString:
			result = string(v)
		case string:
			result = v
		case StoredList:
			// Use formatListForDisplay for lists
			result = formatListForDisplay(v)
		case ParenGroup:
			// Block/code - return the block content
			result = string(v)
		case StoredBlock:
			// Stored block - return the block content
			result = string(v)
		case StoredMacro, *StoredMacro:
			result = "<macro>"
		case *StoredCommand:
			if v.CommandName != "" {
				result = fmt.Sprintf("<command %s>", v.CommandName)
			} else {
				result = "<command>"
			}
		case *StoredChannel:
			if ctx.executor != nil {
				if id := ctx.executor.findStoredChannelID(v); id >= 0 {
					result = fmt.Sprintf("<channel %d>", id)
				} else {
					result = "<channel>"
				}
			} else {
				result = "<channel>"
			}
		case *FiberHandle:
			if ctx.executor != nil {
				if id := ctx.executor.findStoredFiberID(v); id >= 0 {
					result = fmt.Sprintf("<fiber %d>", id)
				} else {
					result = "<fiber>"
				}
			} else {
				result = "<fiber>"
			}
		default:
			// Fallback to fmt.Sprintf
			result = fmt.Sprintf("%v", v)
		}

		// Check for null bytes - symbols cannot contain them
		if strings.Contains(result, "\x00") {
			ctx.LogError(CatType, "Cannot create symbol containing null bytes")
			if len(ctx.Args) >= 2 {
				ctx.SetResult(ctx.Args[1])
			} else {
				ctx.SetResult(nil)
			}
			return BoolStatus(false)
		}

		ctx.SetResult(Symbol(result))
		return BoolStatus(true)
	})

	// block - convert string or list to a StoredBlock (executable code block)
	// Usage: block "echo hello"           -> block that echoes hello
	//        block ~mylist                 -> joins list items with newlines into block
	//        block ~myStringVar            -> creates block from string variable
	// This is the inverse of getting a block's code content
	ps.RegisterCommandInModule("types", "block", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: block <string|list>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		// Resolve any markers
		resolved := value
		if ctx.executor != nil {
			resolved = ctx.executor.resolveValue(value)
		}

		var code string

		switch v := resolved.(type) {
		case string:
			code = v
		case QuotedString:
			code = string(v)
		case StoredString:
			code = string(v)
		case Symbol:
			code = string(v)
		case StoredList:
			// Join list items with newlines to form code
			items := v.Items()
			lines := make([]string, len(items))
			for i, item := range items {
				itemResolved := item
				if ctx.executor != nil {
					itemResolved = ctx.executor.resolveValue(item)
				}
				lines[i] = fmt.Sprintf("%v", itemResolved)
			}
			code = strings.Join(lines, "\n")
		case ParenGroup:
			// Already a block-like thing, just use its content
			code = string(v)
		case StoredBlock:
			// Already a block, return as-is
			code = string(v)
		default:
			ctx.LogError(CatType, fmt.Sprintf("Cannot convert %s to block", getTypeName(resolved)))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		// Store as StoredBlock and return marker
		block := StoredBlock(code)
		id := ctx.executor.storeObject(block, "block")
		marker := fmt.Sprintf("\x00BLOCK:%d\x00", id)
		ctx.state.SetResultWithoutClaim(Symbol(marker))
		return BoolStatus(true)
	})

	// keys - returns a list of all keys from a list's named arguments
	ps.RegisterCommandInModule("strlist", "keys", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: keys <list>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		switch v := value.(type) {
		case StoredList:
			namedArgs := v.NamedArgs()
			if len(namedArgs) == 0 {
				setListResult(ctx, NewStoredList([]interface{}{}))
				return BoolStatus(true)
			}

			keys := make([]string, 0, len(namedArgs))
			for key := range namedArgs {
				keys = append(keys, key)
			}
			sort.Strings(keys)

			items := make([]interface{}, len(keys))
			for i, key := range keys {
				items[i] = key
			}

			setListResult(ctx, NewStoredList(items))
			return BoolStatus(true)
		default:
			ctx.LogError(CatType, fmt.Sprintf("Cannot get keys from type %s", getTypeName(v)))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
	})

	// struct_def - creates a struct definition from a descriptor list
	// Usage: struct_def ~descriptor
	// Descriptor format: {list ("name", size, "mode"), ("name2", size2, "mode2"), ..., metaKey: "metaValue"}
	// Returns a StoredList that can be used with the struct command:
	//   __size: total size in bytes
	//   __named: StoredList of metadata
	//   fieldName: StoredList [offset, length, mode] or [offset, length, "struct", nestedDefID, count]
	ps.RegisterCommandInModule("strlist", "struct_def", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: struct_def <descriptor_list>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		// Get the descriptor list
		desc, ok := ctx.Args[0].(StoredList)
		if !ok {
			ctx.LogError(CatType, "struct_def requires a list descriptor")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		// Build the result list with named args for fields
		resultNamedArgs := make(map[string]interface{})
		var currentOffset int

		// Process positional entries (field definitions)
		for i := 0; i < desc.Len(); i++ {
			item := desc.Get(i)
			// Resolve if it's a marker
			item = ctx.executor.resolveValue(item)

			// Each field should be a tuple/list: (name, size, mode, [structref, [count]])
			// Handle both StoredList and ParenGroup (tuples)
			var fieldItems []interface{}
			switch ft := item.(type) {
			case StoredList:
				for j := 0; j < ft.Len(); j++ {
					fieldItems = append(fieldItems, ft.Get(j))
				}
			case ParenGroup:
				// Parse the paren group to extract arguments
				args, _ := parseArguments(string(ft))
				fieldItems = args
			default:
				ctx.LogError(CatArgument, fmt.Sprintf("Field %d must be a tuple (name, size, mode), got %T", i, item))
				ctx.SetResult(nil)
				return BoolStatus(false)
			}

			if len(fieldItems) < 3 {
				ctx.LogError(CatArgument, fmt.Sprintf("Field %d tuple must have at least 3 elements (name, size, mode)", i))
				ctx.SetResult(nil)
				return BoolStatus(false)
			}

			// Get field name
			nameVal := ctx.executor.resolveValue(fieldItems[0])
			var fieldName string
			switch n := nameVal.(type) {
			case string:
				fieldName = n
			case QuotedString:
				fieldName = string(n)
			case Symbol:
				fieldName = string(n)
			default:
				ctx.LogError(CatArgument, fmt.Sprintf("Field %d name must be a string, got %T", i, nameVal))
				ctx.SetResult(nil)
				return BoolStatus(false)
			}

			// Get field size
			sizeVal := ctx.executor.resolveValue(fieldItems[1])
			sizeNum, ok := toNumber(sizeVal)
			if !ok {
				ctx.LogError(CatArgument, fmt.Sprintf("Field %d size must be a number, got %v", i, sizeVal))
				ctx.SetResult(nil)
				return BoolStatus(false)
			}
			fieldSize := int(sizeNum)

			// Get field mode
			modeVal := ctx.executor.resolveValue(fieldItems[2])
			var fieldMode string
			switch m := modeVal.(type) {
			case string:
				fieldMode = m
			case QuotedString:
				fieldMode = string(m)
			case Symbol:
				fieldMode = string(m)
			default:
				ctx.LogError(CatArgument, fmt.Sprintf("Field %d mode must be a string, got %T", i, modeVal))
				ctx.SetResult(nil)
				return BoolStatus(false)
			}

			// Validate mode - supports extended modes for binary data handling
			// Base modes: bytes, string, struct
			// Integer modes: int/int_be (signed big-endian), int_le (signed little-endian)
			//                uint/uint_be (unsigned big-endian), uint_le (unsigned little-endian)
			// Float modes: float/float_be (big-endian IEEE 754), float_le (little-endian)
			// Bit modes: bit0-bit7 for individual bits within a byte (use size=0 to share byte)
			isBitMode := false
			switch fieldMode {
			case "bytes", "string", "struct",
				"int", "int_be", "int_le",
				"uint", "uint_be", "uint_le",
				"float", "float_be", "float_le":
				// Valid modes
			case "bit0", "bit1", "bit2", "bit3", "bit4", "bit5", "bit6", "bit7":
				// Bit modes - size=0 means share byte with other fields
				isBitMode = true
			default:
				ctx.LogError(CatArgument, fmt.Sprintf("Field %d has invalid mode '%s' (valid: bytes, string, int, int_be, int_le, uint, uint_be, uint_le, float, float_be, float_le, bit0-bit7, struct)", i, fieldMode))
				ctx.SetResult(nil)
				return BoolStatus(false)
			}

			// Build field info list: [offset, length, mode] or [offset, length, "struct", nestedDefID, count]
			var fieldInfoItems []interface{}
			fieldInfoItems = append(fieldInfoItems, int64(currentOffset))
			// For bit modes, always store length=1 (1 byte) even if size was 0
			if isBitMode {
				fieldInfoItems = append(fieldInfoItems, int64(1))
			} else {
				fieldInfoItems = append(fieldInfoItems, int64(fieldSize))
			}
			fieldInfoItems = append(fieldInfoItems, fieldMode)

			// Get optional struct ref and count for nested structs
			if fieldMode == "struct" && len(fieldItems) >= 4 {
				refVal := ctx.executor.resolveValue(fieldItems[3])
				// The nested struct def should be a list marker - extract its ID
				if sym, ok := refVal.(Symbol); ok {
					markerType, id := parseObjectMarker(string(sym))
					if markerType == "list" {
						fieldInfoItems = append(fieldInfoItems, int64(id))
					}
				} else if nestedList, ok := refVal.(StoredList); ok {
					// Store it and get the ID
					nestedID := ctx.executor.storeObject(nestedList, "list")
					fieldInfoItems = append(fieldInfoItems, int64(nestedID))
				}
				if len(fieldItems) >= 5 {
					countVal := ctx.executor.resolveValue(fieldItems[4])
					if countNum, ok := toNumber(countVal); ok {
						fieldInfoItems = append(fieldInfoItems, int64(countNum))
					}
				}
			}

			// Create field info as a StoredList and store it
			fieldInfoList := NewStoredList(fieldInfoItems)
			fieldInfoID := ctx.executor.storeObject(fieldInfoList, "list")
			fieldInfoMarker := fmt.Sprintf("\x00LIST:%d\x00", fieldInfoID)

			// Add to result named args
			resultNamedArgs[fieldName] = Symbol(fieldInfoMarker)

			currentOffset += fieldSize
		}

		// Add __size to named args
		resultNamedArgs["__size"] = int64(currentOffset)

		// Create __named list from descriptor's named args (metadata)
		if desc.NamedArgs() != nil && len(desc.NamedArgs()) > 0 {
			namedMeta := make(map[string]interface{})
			for k, v := range desc.NamedArgs() {
				namedMeta[k] = v
			}
			namedList := NewStoredListWithNamed(nil, namedMeta)
			namedID := ctx.executor.storeObject(namedList, "list")
			namedMarker := fmt.Sprintf("\x00LIST:%d\x00", namedID)
			resultNamedArgs["__named"] = Symbol(namedMarker)
		}

		// Create the result list (no positional items, only named args)
		resultList := NewStoredListWithNamed(nil, resultNamedArgs)
		setListResult(ctx, resultList)
		return BoolStatus(true)
	})

	// struct - creates a struct instance from a definition
	// Usage: struct ~def                        - create empty single struct
	//        struct ~def, ~sourceList           - create single struct, fill from source
	//        struct ~def, ~sourceList, 10       - create array of 10 structs, fill from source
	// The definition is now a StoredList with __size and field info
	ps.RegisterCommandInModule("strlist", "struct", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: struct <def> [, <source_list> [, <count>]]")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		// Get the struct definition - handle #-prefixed symbols
		defArg := ctx.Args[0]

		// Check for #-prefixed symbol (resolve like tilde would)
		if sym, ok := defArg.(Symbol); ok {
			symStr := string(sym)
			if strings.HasPrefix(symStr, "#") {
				// First check local variables
				if localVal, exists := ctx.state.GetVariable(symStr); exists {
					defArg = localVal
				} else if ctx.state.moduleEnv != nil {
					// Then check ObjectsModule
					ctx.state.moduleEnv.mu.RLock()
					if ctx.state.moduleEnv.ObjectsModule != nil {
						if obj, exists := ctx.state.moduleEnv.ObjectsModule[symStr]; exists {
							defArg = obj
						}
					}
					ctx.state.moduleEnv.mu.RUnlock()
				}
			}
		}

		// Resolve the definition - it should be a StoredList
		defVal := ctx.executor.resolveValue(defArg)

		// Get the defID from the marker if it's a symbol
		var defID int
		if sym, ok := defArg.(Symbol); ok {
			markerType, id := parseObjectMarker(string(sym))
			if markerType == "list" {
				defID = id
			}
		}

		defList, ok := defVal.(StoredList)
		if !ok {
			ctx.LogError(CatType, fmt.Sprintf("struct requires a struct definition list as first argument, got %T", defVal))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		// Get __size from the definition
		defNamedArgs := defList.NamedArgs()
		if defNamedArgs == nil {
			ctx.LogError(CatType, "struct definition missing named args")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
		sizeVal, hasSize := defNamedArgs["__size"]
		if !hasSize {
			ctx.LogError(CatType, "struct definition missing __size")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
		sizeNum, ok := toNumber(ctx.executor.resolveValue(sizeVal))
		if !ok {
			ctx.LogError(CatType, "struct definition __size must be a number")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
		structSize := int(sizeNum)

		// If we didn't get defID from marker, store the def list now
		if defID == 0 {
			defID = ctx.executor.storeObject(defList, "list")
		}

		// Check for count (array vs single)
		var count int = -1 // -1 means single struct
		if len(ctx.Args) >= 3 {
			countVal := ctx.executor.resolveValue(ctx.Args[2])
			countNum, ok := toNumber(countVal)
			if !ok {
				ctx.LogError(CatArgument, "Third argument (count) must be a number")
				ctx.SetResult(nil)
				return BoolStatus(false)
			}
			count = int(countNum)
			if count < 0 {
				ctx.LogError(CatArgument, "Count must be non-negative")
				ctx.SetResult(nil)
				return BoolStatus(false)
			}
		}

		// Create the struct
		var result StoredStruct
		if count >= 0 {
			result = NewStoredStructArray(defID, structSize, count)
		} else {
			result = NewStoredStruct(defID, structSize)
		}

		// Fill from source list if provided
		if len(ctx.Args) >= 2 {
			sourceVal := ctx.executor.resolveValue(ctx.Args[1])
			if sourceList, ok := sourceVal.(StoredList); ok {
				if count >= 0 {
					// Array mode: each positional item fills one record
					for i := 0; i < sourceList.Len() && i < count; i++ {
						itemVal := ctx.executor.resolveValue(sourceList.Get(i))
						record := result.Get(i)
						fillStructFromSource(&record, itemVal, defList, ctx)
					}
					if sourceList.Len() > count {
						ctx.LogWarning(CatArgument, fmt.Sprintf("Source list has %d items but struct array only has %d slots", sourceList.Len(), count))
					}
				} else {
					// Single struct mode: fill from the source list directly
					fillStructFromSource(&result, sourceVal, defList, ctx)
				}
			}
		}

		setStructResult(ctx, result)
		return BoolStatus(true)
	})
}
