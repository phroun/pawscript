package pawscript

import (
	"fmt"
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

	// slice - returns a slice of a list or string (end exclusive)
	// Usage: slice ~mylist, 0, 3    - items 0, 1, 2
	//        slice ~mylist, 1, -1   - from index 1 to end
	//        slice "hello", 0, 3          - "hel"
	ps.RegisterCommand("slice", func(ctx *Context) Result {
		if len(ctx.Args) < 3 {
			ctx.LogError(CatCommand, "Usage: slice <list|string>, <start>, <end>")
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

	// append - returns a new list with item appended
	// Usage: append ~mylist, newitem
	ps.RegisterCommand("append", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: append <list>, <item>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		value := ctx.Args[0]
		item := ctx.Args[1]

		switch v := value.(type) {
		case StoredList:
			setListResult(ctx, v.Append(item))
			return BoolStatus(true)
		default:
			ctx.LogError(CatType, fmt.Sprintf("Cannot append to type %s\n", getTypeName(v)))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
	})

	// prepend - returns a new list with item prepended
	// Usage: prepend ~mylist, newitem
	ps.RegisterCommand("prepend", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: prepend <list>, <item>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		value := ctx.Args[0]
		item := ctx.Args[1]

		switch v := value.(type) {
		case StoredList:
			setListResult(ctx, v.Prepend(item))
			return BoolStatus(true)
		default:
			ctx.LogError(CatType, fmt.Sprintf("Cannot prepend to type %s\n", getTypeName(v)))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
	})

	// compact - returns a new list with a fresh backing array
	// Usage: compact ~mylist
	// Use this to free memory after slicing a large list
	ps.RegisterCommand("compact", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: compact <list>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		switch v := value.(type) {
		case StoredList:
			setListResult(ctx, v.Compact())
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
	ps.RegisterCommand("concat", func(ctx *Context) Result {
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

	// split - split string into list by delimiter
	// Usage: split "a,b,c", ","  -> list of ["a", "b", "c"]
	// Inverse of join
	ps.RegisterCommand("split", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: split <string>, <delimiter>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

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

	// join - join list into string with delimiter
	// Usage: join ~mylist, ","  -> "a,b,c"
	// Inverse of split
	ps.RegisterCommand("join", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: join <list>, <delimiter>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		delimiter := resolveToString(ctx.Args[1], ctx.executor)

		// Handle StoredList
		if storedList, ok := ctx.Args[0].(StoredList); ok {
			items := storedList.Items()
			strItems := make([]string, len(items))
			for i, item := range items {
				// Resolve each item in case it's a marker
				resolved := ctx.executor.resolveValue(item)
				strItems[i] = fmt.Sprintf("%v", resolved)
			}
			result := strings.Join(strItems, delimiter)
			if ctx.executor != nil {
				result := ctx.executor.maybeStoreValue(result, ctx.state)
				ctx.state.SetResultWithoutClaim(result)
			} else {
				ctx.state.SetResultWithoutClaim(result)
			}
			return BoolStatus(true)
		}

		ctx.LogError(CatType, fmt.Sprintf("First argument must be a list, got %s\n", getTypeName(ctx.Args[0])))
		ctx.SetResult("")
		return BoolStatus(false)
	})

	// str_upper - convert string to uppercase
	// Usage: str_upper "hello"  -> "HELLO"
	ps.RegisterCommand("str_upper", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: str_upper <string>")
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

	// str_lower - convert string to lowercase
	// Usage: str_lower "HELLO"  -> "hello"
	ps.RegisterCommand("str_lower", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: str_lower <string>")
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

	// trim - trim whitespace from both ends
	// Usage: trim "  hello  "  -> "hello"
	ps.RegisterCommand("trim", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: trim <string>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		result := strings.TrimSpace(str)
		if ctx.executor != nil {
			result := ctx.executor.maybeStoreValue(result, ctx.state)
			ctx.state.SetResultWithoutClaim(result)
		} else {
			ctx.state.SetResultWithoutClaim(result)
		}
		return BoolStatus(true)
	})

	// trim_start - trim whitespace from start
	// Usage: trim_start "  hello  "  -> "hello  "
	ps.RegisterCommand("trim_start", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: trim_start <string>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		result := strings.TrimLeft(str, " \t\n\r")
		if ctx.executor != nil {
			result := ctx.executor.maybeStoreValue(result, ctx.state)
			ctx.state.SetResultWithoutClaim(result)
		} else {
			ctx.state.SetResultWithoutClaim(result)
		}
		return BoolStatus(true)
	})

	// trim_end - trim whitespace from end
	// Usage: trim_end "  hello  "  -> "  hello"
	ps.RegisterCommand("trim_end", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: trim_end <string>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		result := strings.TrimRight(str, " \t\n\r")
		if ctx.executor != nil {
			result := ctx.executor.maybeStoreValue(result, ctx.state)
			ctx.state.SetResultWithoutClaim(result)
		} else {
			ctx.state.SetResultWithoutClaim(result)
		}
		return BoolStatus(true)
	})

	// contains - check if string contains substring
	// Usage: contains "hello world", "world"  -> true
	ps.RegisterCommand("contains", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: contains <string>, <substring>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		substr := resolveToString(ctx.Args[1], ctx.executor)

		result := strings.Contains(str, substr)
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	// index - find first index of substring (-1 if not found)
	// Usage: index "hello world", "world"  -> 6
	// Returns -1 if not found (like many languages)
	// Always succeeds and sets result (use result to check if found)
	ps.RegisterCommand("index", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: index <string>, <substring>")
			ctx.SetResult(int64(-1))
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		substr := resolveToString(ctx.Args[1], ctx.executor)

		index := strings.Index(str, substr)
		ctx.SetResult(int64(index))
		// Always return success - caller checks result value
		return BoolStatus(true)
	})

	// replace - replace all occurrences of substring
	// Usage: replace "hello world", "world", "gopher"  -> "hello gopher"
	// Replaces ALL occurrences (like strings.ReplaceAll)
	ps.RegisterCommand("replace", func(ctx *Context) Result {
		if len(ctx.Args) < 3 {
			ctx.LogError(CatCommand, "Usage: replace <string>, <old>, <new>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		old := resolveToString(ctx.Args[1], ctx.executor)
		new := resolveToString(ctx.Args[2], ctx.executor)

		result := strings.ReplaceAll(str, old, new)
		if ctx.executor != nil {
			result := ctx.executor.maybeStoreValue(result, ctx.state)
			ctx.state.SetResultWithoutClaim(result)
		} else {
			ctx.state.SetResultWithoutClaim(result)
		}
		return BoolStatus(true)
	})

	// starts_with - check if string starts with prefix
	// Usage: starts_with "hello world", "hello"  -> true
	ps.RegisterCommand("starts_with", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: starts_with <string>, <prefix>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		prefix := resolveToString(ctx.Args[1], ctx.executor)

		result := strings.HasPrefix(str, prefix)
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	// ends_with - check if string ends with suffix
	// Usage: ends_with "hello world", "world"  -> true
	ps.RegisterCommand("ends_with", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: ends_with <string>, <suffix>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		suffix := resolveToString(ctx.Args[1], ctx.executor)

		result := strings.HasSuffix(str, suffix)
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	// str_repeat - repeat string n times
	// Usage: str_repeat "ab", 3  -> "ababab"
	ps.RegisterCommand("str_repeat", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: str_repeat <string>, <count>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		count, ok := toNumber(ctx.Args[1])
		if !ok {
			ctx.LogError(CatArgument, "Count must be a number")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		result := strings.Repeat(str, int(count))
		if ctx.executor != nil {
			result := ctx.executor.maybeStoreValue(result, ctx.state)
			ctx.state.SetResultWithoutClaim(result)
		} else {
			ctx.state.SetResultWithoutClaim(result)
		}
		return BoolStatus(true)
	})
}
