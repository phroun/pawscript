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
	ps.RegisterCommandInModule("stdlib", "slice", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("stdlib", "append", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("stdlib", "prepend", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("stdlib", "compact", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("stdlib", "concat", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("stdlib", "split", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("stdlib", "join", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("stdlib", "str_upper", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("stdlib", "str_lower", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("stdlib", "trim", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("stdlib", "trim_start", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("stdlib", "trim_end", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("stdlib", "contains", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("stdlib", "index", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("stdlib", "replace", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("stdlib", "starts_with", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("stdlib", "ends_with", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("stdlib", "str_repeat", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("stdlib", "sort", func(ctx *Context) Result {
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
		result = ps.macroSystem.ExecuteStoredMacro(&comp, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
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
			result = ps.macroSystem.ExecuteStoredMacro(&macro, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
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
			// Treat as macro name
			name := string(comp)
			result = ps.macroSystem.ExecuteMacro(name, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
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
		result = ps.macroSystem.ExecuteStoredMacro(&macro, func(cmds string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
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
		// Treat as macro name
		result = ps.macroSystem.ExecuteMacro(comp, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
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
