package pawscript

import (
	"fmt"
	"strconv"
	"strings"
)

// deepEqual performs a deep comparison of two values with efficiency shortcuts.
// If both values are the same object marker (same ID), they're equal.
// Otherwise, resolve and compare recursively, bailing on first difference.
func deepEqual(a, b interface{}, executor *Executor) bool {
	// Quick shortcut: if both are symbols and identical (same object marker), they're equal
	if symA, okA := a.(Symbol); okA {
		if symB, okB := b.(Symbol); okB {
			if string(symA) == string(symB) {
				return true
			}
		}
	}

	// Resolve both values
	resolvedA := a
	resolvedB := b
	if executor != nil {
		resolvedA = executor.resolveValue(a)
		resolvedB = executor.resolveValue(b)
	}

	// Handle nil
	if resolvedA == nil && resolvedB == nil {
		return true
	}
	if resolvedA == nil || resolvedB == nil {
		return false
	}

	// Compare by type
	switch va := resolvedA.(type) {
	case bool:
		if vb, ok := resolvedB.(bool); ok {
			return va == vb
		}
		return false
	case int64:
		switch vb := resolvedB.(type) {
		case int64:
			return va == vb
		case int:
			return va == int64(vb)
		case float64:
			return float64(va) == vb
		}
		return false
	case int:
		switch vb := resolvedB.(type) {
		case int:
			return va == vb
		case int64:
			return int64(va) == vb
		case float64:
			return float64(va) == vb
		}
		return false
	case float64:
		switch vb := resolvedB.(type) {
		case float64:
			return va == vb
		case int64:
			return va == float64(vb)
		case int:
			return va == float64(vb)
		}
		return false
	case string:
		if vb, ok := resolvedB.(string); ok {
			return va == vb
		}
		// Compare with Symbol/QuotedString
		return fmt.Sprintf("%v", va) == fmt.Sprintf("%v", resolvedB)
	case Symbol:
		// Check for undefined
		if string(va) == "undefined" {
			if vb, ok := resolvedB.(Symbol); ok && string(vb) == "undefined" {
				return true
			}
			return false
		}
		return fmt.Sprintf("%v", va) == fmt.Sprintf("%v", resolvedB)
	case QuotedString:
		return fmt.Sprintf("%v", va) == fmt.Sprintf("%v", resolvedB)
	case StoredList:
		// Compare lists element by element
		var listB StoredList
		switch vb := resolvedB.(type) {
		case StoredList:
			listB = vb
		case ParenGroup:
			// Parse ParenGroup to list
			items, _ := parseArguments(string(vb))
			listB = NewStoredList(items)
		default:
			return false
		}
		itemsA := va.Items()
		itemsB := listB.Items()
		if len(itemsA) != len(itemsB) {
			return false
		}
		// Bail on first difference
		for i := range itemsA {
			if !deepEqual(itemsA[i], itemsB[i], executor) {
				return false
			}
		}
		return true
	case ParenGroup:
		// Parse and compare as list
		itemsA, _ := parseArguments(string(va))
		var itemsB []interface{}
		switch vb := resolvedB.(type) {
		case StoredList:
			itemsB = vb.Items()
		case ParenGroup:
			itemsB, _ = parseArguments(string(vb))
		default:
			return false
		}
		if len(itemsA) != len(itemsB) {
			return false
		}
		for i := range itemsA {
			if !deepEqual(itemsA[i], itemsB[i], executor) {
				return false
			}
		}
		return true
	}

	// Fallback: string comparison
	return fmt.Sprintf("%v", resolvedA) == fmt.Sprintf("%v", resolvedB)
}

// findAllStringPositions finds all non-overlapping positions of substr in str
func findAllStringPositions(str, substr string) []int {
	var positions []int
	if substr == "" {
		return positions
	}
	start := 0
	for {
		idx := strings.Index(str[start:], substr)
		if idx == -1 {
			break
		}
		positions = append(positions, start+idx)
		start += idx + len(substr)
	}
	return positions
}

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
	ps.RegisterCommandInModule("strlist", "slice", func(ctx *Context) Result {
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

	// compact - returns a new list with a fresh backing array
	// Usage: compact ~mylist
	// Use this to free memory after slicing a large list
	ps.RegisterCommandInModule("strlist", "compact", func(ctx *Context) Result {
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

					// Check for early return
					if earlyReturn, ok := result.(EarlyReturn); ok {
						if earlyReturn.HasResult {
							ctx.SetResult(earlyReturn.Result)
						}
						return earlyReturn.Status
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
}

// sortItemsDefault sorts items using the default PawScript ordering:
// nil < false < true < numbers (low to high) < symbols (alpha) < strings (alpha) < other (original order)
// executor is optional - if provided, resolves object markers before categorizing
func sortItemsDefaultWithExecutor(items []interface{}, executor *Executor) {
	// Assign sort keys to preserve stable sort for "other" items
	type sortItem struct {
		value    interface{}
		origIdx  int
		category int // 0=nil, 1=false, 2=true, 3=number, 4=symbol, 5=string, 6=other
		numVal   float64
		strVal   string
	}

	sortItems := make([]sortItem, len(items))
	for i, item := range items {
		si := sortItem{value: item, origIdx: i, category: 6}

		// Resolve markers if executor available
		resolved := item
		if executor != nil {
			resolved = executor.resolveValue(item)
		}

		switch v := resolved.(type) {
		case nil:
			si.category = 0
		case bool:
			if v {
				si.category = 2
			} else {
				si.category = 1
			}
		case int:
			si.category = 3
			si.numVal = float64(v)
		case int64:
			si.category = 3
			si.numVal = float64(v)
		case float64:
			si.category = 3
			si.numVal = v
		case Symbol:
			// Check if it's an object marker for a string
			if markerType, _ := parseObjectMarker(string(v)); markerType == "string" {
				// Already resolved above, so this is a non-string symbol
				si.category = 4
				si.strVal = string(v)
			} else {
				si.category = 4
				si.strVal = string(v)
			}
		case QuotedString:
			si.category = 5
			si.strVal = string(v)
		case StoredString:
			si.category = 5
			si.strVal = string(v)
		case string:
			si.category = 5
			si.strVal = v
		}
		sortItems[i] = si
	}

	// Sort using insertion sort (stable)
	for i := 1; i < len(sortItems); i++ {
		key := sortItems[i]
		j := i - 1
		for j >= 0 && compareSortItems(sortItems[j], key) > 0 {
			sortItems[j+1] = sortItems[j]
			j--
		}
		sortItems[j+1] = key
	}

	// Copy back
	for i, si := range sortItems {
		items[i] = si.value
	}
}

// compareSortItems compares two sortItems, returns <0 if a<b, 0 if a==b, >0 if a>b
func compareSortItems(a, b struct {
	value    interface{}
	origIdx  int
	category int
	numVal   float64
	strVal   string
}) int {
	// Compare by category first
	if a.category != b.category {
		return a.category - b.category
	}

	// Within same category
	switch a.category {
	case 0, 1, 2: // nil, false, true - all equal within category
		return 0
	case 3: // numbers
		if a.numVal < b.numVal {
			return -1
		} else if a.numVal > b.numVal {
			return 1
		}
		return 0
	case 4, 5: // symbols, strings
		if a.strVal < b.strVal {
			return -1
		} else if a.strVal > b.strVal {
			return 1
		}
		return 0
	case 6: // other - preserve original order
		return a.origIdx - b.origIdx
	}
	return 0
}

// callComparator calls a comparator (macro/command) with two items and returns whether first < second
func callComparator(ps *PawScript, ctx *Context, comparator interface{}, a, b interface{}) (bool, error) {
	callArgs := []interface{}{a, b}
	childState := ctx.state.CreateChild()

	var result Result

	// Handle different comparator types (like call does)
	switch comp := comparator.(type) {
	case StoredCommand:
		cmdCtx := &Context{
			Args:      callArgs,
			NamedArgs: make(map[string]interface{}),
			Position:  ctx.Position,
			state:     childState,
			executor:  ctx.executor,
			logger:    ctx.logger,
		}
		result = comp.Handler(cmdCtx)

	case StoredMacro:
		result = ps.executor.ExecuteStoredMacro(&comp, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
			filename := ""
			lineOffset := 0
			columnOffset := 0
			if substCtx != nil {
				filename = substCtx.Filename
				lineOffset = substCtx.CurrentLineOffset
				columnOffset = substCtx.CurrentColumnOffset
			}
			return ps.executor.ExecuteWithState(commands, macroExecState, substCtx, filename, lineOffset, columnOffset)
		}, callArgs, make(map[string]interface{}), childState, ctx.Position, ctx.state)

	case Symbol:
		markerType, objectID := parseObjectMarker(string(comp))
		if markerType == "command" && objectID >= 0 {
			obj, exists := ctx.executor.getObject(objectID)
			if !exists {
				return false, fmt.Errorf("command object %d not found", objectID)
			}
			cmd, ok := obj.(StoredCommand)
			if !ok {
				return false, fmt.Errorf("object %d is not a command", objectID)
			}
			cmdCtx := &Context{
				Args:      callArgs,
				NamedArgs: make(map[string]interface{}),
				Position:  ctx.Position,
				state:     childState,
				executor:  ctx.executor,
				logger:    ctx.logger,
			}
			result = cmd.Handler(cmdCtx)
		} else if markerType == "macro" && objectID >= 0 {
			obj, exists := ctx.executor.getObject(objectID)
			if !exists {
				return false, fmt.Errorf("macro object %d not found", objectID)
			}
			macro, ok := obj.(StoredMacro)
			if !ok {
				return false, fmt.Errorf("object %d is not a macro", objectID)
			}
			result = ps.executor.ExecuteStoredMacro(&macro, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
				filename := ""
				lineOffset := 0
				columnOffset := 0
				if substCtx != nil {
					filename = substCtx.Filename
					lineOffset = substCtx.CurrentLineOffset
					columnOffset = substCtx.CurrentColumnOffset
				}
				return ps.executor.ExecuteWithState(commands, macroExecState, substCtx, filename, lineOffset, columnOffset)
			}, callArgs, make(map[string]interface{}), childState, ctx.Position, ctx.state)
		} else {
			// Treat as macro name - look up in module environment (COW - only check MacrosModule)
			name := string(comp)
			var macro *StoredMacro
			ctx.state.moduleEnv.mu.RLock()
			if m, exists := ctx.state.moduleEnv.MacrosModule[name]; exists && m != nil {
				macro = m
			}
			ctx.state.moduleEnv.mu.RUnlock()

			if macro == nil {
				return false, fmt.Errorf("macro \"%s\" not found", name)
			}

			result = ps.executor.ExecuteStoredMacro(macro, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
				filename := ""
				lineOffset := 0
				columnOffset := 0
				if substCtx != nil {
					filename = substCtx.Filename
					lineOffset = substCtx.CurrentLineOffset
					columnOffset = substCtx.CurrentColumnOffset
				}
				return ps.executor.ExecuteWithState(commands, macroExecState, substCtx, filename, lineOffset, columnOffset)
			}, callArgs, make(map[string]interface{}), childState, ctx.Position, ctx.state)
		}

	case ParenGroup:
		// Immediate macro (anonymous block)
		commands := string(comp)
		macroEnv := NewMacroModuleEnvironment(ctx.state.moduleEnv)
		macro := NewStoredMacroWithEnv(commands, ctx.Position, macroEnv)
		result = ps.executor.ExecuteStoredMacro(&macro, func(cmds string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
			filename := ""
			lineOffset := 0
			columnOffset := 0
			if substCtx != nil {
				filename = substCtx.Filename
				lineOffset = substCtx.CurrentLineOffset
				columnOffset = substCtx.CurrentColumnOffset
			}
			return ps.executor.ExecuteWithState(cmds, macroExecState, substCtx, filename, lineOffset, columnOffset)
		}, callArgs, make(map[string]interface{}), childState, ctx.Position, ctx.state)

	case string:
		// First check if it's a marker (from $1 substitution, etc.)
		markerType, objectID := parseObjectMarker(comp)
		if markerType == "command" && objectID >= 0 {
			obj, exists := ctx.executor.getObject(objectID)
			if !exists {
				return false, fmt.Errorf("command object %d not found", objectID)
			}
			cmd, ok := obj.(StoredCommand)
			if !ok {
				return false, fmt.Errorf("object %d is not a command", objectID)
			}
			cmdCtx := &Context{
				Args:      callArgs,
				NamedArgs: make(map[string]interface{}),
				Position:  ctx.Position,
				state:     childState,
				executor:  ctx.executor,
				logger:    ctx.logger,
			}
			result = cmd.Handler(cmdCtx)
		} else if markerType == "macro" && objectID >= 0 {
			obj, exists := ctx.executor.getObject(objectID)
			if !exists {
				return false, fmt.Errorf("macro object %d not found", objectID)
			}
			macro, ok := obj.(StoredMacro)
			if !ok {
				return false, fmt.Errorf("object %d is not a macro", objectID)
			}
			result = ps.executor.ExecuteStoredMacro(&macro, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
				filename := ""
				lineOffset := 0
				columnOffset := 0
				if substCtx != nil {
					filename = substCtx.Filename
					lineOffset = substCtx.CurrentLineOffset
					columnOffset = substCtx.CurrentColumnOffset
				}
				return ps.executor.ExecuteWithState(commands, macroExecState, substCtx, filename, lineOffset, columnOffset)
			}, callArgs, make(map[string]interface{}), childState, ctx.Position, ctx.state)
		} else {
			// Treat as macro name - look up in module environment (COW - only check MacrosModule)
			var macro *StoredMacro
			ctx.state.moduleEnv.mu.RLock()
			if m, exists := ctx.state.moduleEnv.MacrosModule[comp]; exists && m != nil {
				macro = m
			}
			ctx.state.moduleEnv.mu.RUnlock()

			if macro == nil {
				return false, fmt.Errorf("macro \"%s\" not found", comp)
			}

			result = ps.executor.ExecuteStoredMacro(macro, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
				filename := ""
				lineOffset := 0
				columnOffset := 0
				if substCtx != nil {
					filename = substCtx.Filename
					lineOffset = substCtx.CurrentLineOffset
					columnOffset = substCtx.CurrentColumnOffset
				}
				return ps.executor.ExecuteWithState(commands, macroExecState, substCtx, filename, lineOffset, columnOffset)
			}, callArgs, make(map[string]interface{}), childState, ctx.Position, ctx.state)
		}

	default:
		return false, fmt.Errorf("invalid comparator type: %T", comparator)
	}

	// Handle async result
	if token, isToken := result.(TokenResult); isToken {
		tokenID := string(token)
		waitChan := make(chan ResumeData, 1)
		ctx.executor.attachWaitChan(tokenID, waitChan)
		resumeData := <-waitChan
		return resumeData.Status, nil
	}

	// Use BoolStatus directly
	if boolRes, ok := result.(BoolStatus); ok {
		return bool(boolRes), nil
	}

	return false, nil
}
