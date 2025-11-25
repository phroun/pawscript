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

	// ==================== strlist:: module ====================
	// Commands that work on both strings and lists

	// slice - returns a slice of a list or string (end exclusive)
	ps.RegisterCommand("slice", func(ctx *Context) Result {
		if len(ctx.Args) < 3 {
			ctx.LogError(CatCommand, "Usage: slice <list|string>, <start>, <end>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		startNum, ok := toNumber(ctx.Args[1])
		if !ok {
			ctx.LogError(CatArgument, "Start index must be a number")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
		start := int(startNum)

		endNum, ok := toNumber(ctx.Args[2])
		if !ok {
			ctx.LogError(CatArgument, "End index must be a number")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
		end := int(endNum)

		switch v := value.(type) {
		case StoredList:
			if end < 0 {
				end = v.Len()
			}
			setListResult(ctx, v.Slice(start, end))
			return BoolStatus(true)
		case string, QuotedString, Symbol:
			resolved := ctx.executor.resolveValue(v)
			str := fmt.Sprintf("%v", resolved)
			if end < 0 {
				end = len(str)
			}
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
				id := ctx.executor.storeObject(StoredString(result), "string")
				marker := fmt.Sprintf("\x00STRING:%d\x00", id)
				ctx.state.SetResult(Symbol(marker))
			} else {
				ctx.SetResult(result)
			}
			return BoolStatus(true)
		default:
			ctx.LogError(CatType, fmt.Sprintf("Cannot slice type %s", getTypeName(v)))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
	})

	// append - append item to list or string
	ps.RegisterCommand("append", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: append <list|string>, <item>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		target := ctx.Args[0]
		item := ctx.Args[1]

		if list, ok := target.(StoredList); ok {
			newList := list.Append(item)
			setListResult(ctx, newList)
			return BoolStatus(true)
		}

		// String append
		resolved := ctx.executor.resolveValue(target)
		str := fmt.Sprintf("%v", resolved)
		itemStr := fmt.Sprintf("%v", item)
		result := str + itemStr
		ctx.SetResult(result)
		return BoolStatus(true)
	})

	// prepend - prepend item to list or string
	ps.RegisterCommand("prepend", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: prepend <list|string>, <item>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		target := ctx.Args[0]
		item := ctx.Args[1]

		if list, ok := target.(StoredList); ok {
			newList := list.Prepend(item)
			setListResult(ctx, newList)
			return BoolStatus(true)
		}

		// String prepend
		resolved := ctx.executor.resolveValue(target)
		str := fmt.Sprintf("%v", resolved)
		itemStr := fmt.Sprintf("%v", item)
		result := itemStr + str
		ctx.SetResult(result)
		return BoolStatus(true)
	})

	// compact - remove nil/empty values from list
	ps.RegisterCommand("compact", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: compact <list>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		target := ctx.Args[0]

		if list, ok := target.(StoredList); ok {
			var nonEmpty []interface{}
			for _, item := range list.Items() {
				if item != nil && fmt.Sprintf("%v", item) != "" {
					nonEmpty = append(nonEmpty, item)
				}
			}
			setListResult(ctx, NewStoredList(nonEmpty))
			return BoolStatus(true)
		}

		ctx.LogError(CatType, "compact requires a list argument")
		ctx.SetResult(nil)
		return BoolStatus(false)
	})

	// concat - concatenate lists or strings
	ps.RegisterCommand("concat", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: concat <a>, <b>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		first := ctx.Args[0]
		second := ctx.Args[1]

		// If first is a list, try to concat as lists
		if list1, ok := first.(StoredList); ok {
			if list2, ok := second.(StoredList); ok {
				combined := append(list1.Items(), list2.Items()...)
				setListResult(ctx, NewStoredList(combined))
				return BoolStatus(true)
			}
			// Append single item to list
			newList := list1.Append(second)
			setListResult(ctx, newList)
			return BoolStatus(true)
		}

		// String concatenation
		resolved1 := ctx.executor.resolveValue(first)
		resolved2 := ctx.executor.resolveValue(second)
		str1 := fmt.Sprintf("%v", resolved1)
		str2 := fmt.Sprintf("%v", resolved2)
		ctx.SetResult(str1 + str2)
		return BoolStatus(true)
	})

	// split - split string into list
	ps.RegisterCommand("split", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: split <string>, <delimiter>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		str := fmt.Sprintf("%v", ctx.Args[0])
		delim := fmt.Sprintf("%v", ctx.Args[1])

		parts := strings.Split(str, delim)
		items := make([]interface{}, len(parts))
		for i, p := range parts {
			items[i] = p
		}

		setListResult(ctx, NewStoredList(items))
		return BoolStatus(true)
	})

	// join - join list into string
	ps.RegisterCommand("join", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: join <list>, <delimiter>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		listArg := ctx.Args[0]
		delim := fmt.Sprintf("%v", ctx.Args[1])

		var items []interface{}

		if list, ok := listArg.(StoredList); ok {
			items = list.Items()
		} else if parenGroup, ok := listArg.(ParenGroup); ok {
			items, _ = parseArguments(string(parenGroup))
		} else {
			ctx.LogError(CatType, "join requires a list as first argument")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		parts := make([]string, len(items))
		for i, item := range items {
			parts[i] = fmt.Sprintf("%v", item)
		}

		ctx.SetResult(strings.Join(parts, delim))
		return BoolStatus(true)
	})

	// trim - remove whitespace from both ends
	ps.RegisterCommand("trim", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: trim <string>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		resolved := ctx.executor.resolveValue(ctx.Args[0])
		str := fmt.Sprintf("%v", resolved)
		ctx.SetResult(strings.TrimSpace(str))
		return BoolStatus(true)
	})

	// trim_start - remove whitespace from start
	ps.RegisterCommand("trim_start", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: trim_start <string>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		resolved := ctx.executor.resolveValue(ctx.Args[0])
		str := fmt.Sprintf("%v", resolved)
		ctx.SetResult(strings.TrimLeft(str, " \t\n\r"))
		return BoolStatus(true)
	})

	// trim_end - remove whitespace from end
	ps.RegisterCommand("trim_end", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: trim_end <string>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		resolved := ctx.executor.resolveValue(ctx.Args[0])
		str := fmt.Sprintf("%v", resolved)
		ctx.SetResult(strings.TrimRight(str, " \t\n\r"))
		return BoolStatus(true)
	})

	// contains - check if string contains substring
	ps.RegisterCommand("contains", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: contains <string>, <substring>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		str := fmt.Sprintf("%v", ctx.Args[0])
		substr := fmt.Sprintf("%v", ctx.Args[1])
		result := strings.Contains(str, substr)
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	// index - find index of substring
	ps.RegisterCommand("index", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: index <string>, <substring>")
			ctx.SetResult(-1)
			return BoolStatus(false)
		}

		str := fmt.Sprintf("%v", ctx.Args[0])
		substr := fmt.Sprintf("%v", ctx.Args[1])
		ctx.SetResult(int64(strings.Index(str, substr)))
		return BoolStatus(true)
	})

	// replace - replace substring
	ps.RegisterCommand("replace", func(ctx *Context) Result {
		if len(ctx.Args) < 3 {
			ctx.LogError(CatCommand, "Usage: replace <string>, <old>, <new>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := fmt.Sprintf("%v", ctx.Args[0])
		old := fmt.Sprintf("%v", ctx.Args[1])
		new := fmt.Sprintf("%v", ctx.Args[2])
		ctx.SetResult(strings.ReplaceAll(str, old, new))
		return BoolStatus(true)
	})

	// starts_with - check if string starts with prefix
	ps.RegisterCommand("starts_with", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: starts_with <string>, <prefix>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		str := fmt.Sprintf("%v", ctx.Args[0])
		prefix := fmt.Sprintf("%v", ctx.Args[1])
		result := strings.HasPrefix(str, prefix)
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	// ends_with - check if string ends with suffix
	ps.RegisterCommand("ends_with", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: ends_with <string>, <suffix>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		str := fmt.Sprintf("%v", ctx.Args[0])
		suffix := fmt.Sprintf("%v", ctx.Args[1])
		result := strings.HasSuffix(str, suffix)
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	// ==================== str:: module ====================

	// str_repeat - repeat string n times
	ps.RegisterCommand("str_repeat", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: str_repeat <string>, <count>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := fmt.Sprintf("%v", ctx.Args[0])
		count, ok := toNumber(ctx.Args[1])
		if !ok || count < 0 {
			ctx.LogError(CatArgument, "count must be a non-negative number")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		ctx.SetResult(strings.Repeat(str, int(count)))
		return BoolStatus(true)
	})

	// str_upper - convert string to uppercase
	ps.RegisterCommand("str_upper", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: str_upper <string>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		resolved := ctx.executor.resolveValue(ctx.Args[0])
		str := fmt.Sprintf("%v", resolved)
		ctx.SetResult(strings.ToUpper(str))
		return BoolStatus(true)
	})

	// str_lower - convert string to lowercase
	ps.RegisterCommand("str_lower", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: str_lower <string>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		resolved := ctx.executor.resolveValue(ctx.Args[0])
		str := fmt.Sprintf("%v", resolved)
		ctx.SetResult(strings.ToLower(str))
		return BoolStatus(true)
	})
}
