package pawscript

import (
	"fmt"
	"math"
	"strings"
)

// getNumericArgs extracts numeric arguments from either multiple args or a single list.
// If there's exactly 1 argument that resolves to a StoredList, returns the list items.
// Otherwise returns ctx.Args directly. This allows math operations like:
//   add 1, 2, 3      (multiple args)
//   add ~mylist      (single list containing [1, 2, 3])
// Returns the items to iterate over and whether a list was unwrapped.
func getNumericArgs(ctx *Context) ([]interface{}, bool) {
	if len(ctx.Args) == 1 {
		resolved := ctx.executor.resolveValue(ctx.Args[0])
		if list, ok := resolved.(StoredList); ok {
			return list.Items(), true
		}
	}
	return ctx.Args, false
}

// RegisterBasicMathLib registers basic math and comparison commands
// Modules: basicmath, cmp
func (ps *PawScript) RegisterBasicMathLib() {

	// ==================== basicmath:: module ====================

	// add - sums any number of arguments, or all elements of a single list
	ps.RegisterCommandInModule("basicmath", "add", func(ctx *Context) Result {
		args, fromList := getNumericArgs(ctx)
		if len(args) < 2 {
			if fromList {
				ctx.LogError(CatCommand, "add: list must contain at least 2 elements")
			} else {
				ctx.LogError(CatCommand, "Usage: add <a>, <b>, ... or add <list>")
			}
			return BoolStatus(false)
		}
		sum := float64(0)
		for i, arg := range args {
			resolved := ctx.executor.resolveValue(arg)
			n, ok := toNumber(resolved)
			if !ok {
				ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument at position %d: %v", i+1, arg))
				return BoolStatus(false)
			}
			sum += n
		}
		ctx.SetResult(sum)
		return BoolStatus(true)
	})

	// sub - subtracts all remaining arguments from the first, or from first element of a list
	ps.RegisterCommandInModule("basicmath", "sub", func(ctx *Context) Result {
		args, fromList := getNumericArgs(ctx)
		if len(args) < 2 {
			if fromList {
				ctx.LogError(CatCommand, "sub: list must contain at least 2 elements")
			} else {
				ctx.LogError(CatCommand, "Usage: sub <start>, <a>, ... or sub <list>")
			}
			return BoolStatus(false)
		}
		resolved0 := ctx.executor.resolveValue(args[0])
		result, ok := toNumber(resolved0)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument at position 1: %v", args[0]))
			return BoolStatus(false)
		}
		for i := 1; i < len(args); i++ {
			resolved := ctx.executor.resolveValue(args[i])
			n, ok := toNumber(resolved)
			if !ok {
				ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument at position %d: %v", i+1, args[i]))
				return BoolStatus(false)
			}
			result -= n
		}
		ctx.SetResult(result)
		return BoolStatus(true)
	})

	// mul - multiplies any number of arguments, or all elements of a single list
	ps.RegisterCommandInModule("basicmath", "mul", func(ctx *Context) Result {
		args, fromList := getNumericArgs(ctx)
		if len(args) < 2 {
			if fromList {
				ctx.LogError(CatCommand, "mul: list must contain at least 2 elements")
			} else {
				ctx.LogError(CatCommand, "Usage: mul <a>, <b>, ... or mul <list>")
			}
			return BoolStatus(false)
		}
		product := float64(1)
		for i, arg := range args {
			resolved := ctx.executor.resolveValue(arg)
			n, ok := toNumber(resolved)
			if !ok {
				ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument at position %d: %v", i+1, arg))
				return BoolStatus(false)
			}
			product *= n
		}
		ctx.SetResult(product)
		return BoolStatus(true)
	})

	// idiv - floored integer division
	// With >2 args: first / product(rest)
	// Named args: remainder:true or modulo:true returns [result, remainder/modulo]
	ps.RegisterCommandInModule("basicmath", "idiv", func(ctx *Context) Result {
		args, fromList := getNumericArgs(ctx)
		if len(args) < 2 {
			if fromList {
				ctx.LogError(CatCommand, "idiv: list must contain at least 2 elements")
			} else {
				ctx.LogError(CatCommand, "Usage: idiv <dividend>, <divisor>, ... or idiv <list>")
			}
			return BoolStatus(false)
		}
		return performDivision(ctx, args, true, false, false)
	})

	// fdiv - floating point division
	// With >2 args: first / product(rest)
	// Named args: remainder:true or modulo:true returns [result, remainder/modulo]
	ps.RegisterCommandInModule("basicmath", "fdiv", func(ctx *Context) Result {
		args, fromList := getNumericArgs(ctx)
		if len(args) < 2 {
			if fromList {
				ctx.LogError(CatCommand, "fdiv: list must contain at least 2 elements")
			} else {
				ctx.LogError(CatCommand, "Usage: fdiv <dividend>, <divisor>, ... or fdiv <list>")
			}
			return BoolStatus(false)
		}
		return performDivision(ctx, args, false, false, false)
	})

	// iremainder - integer division remainder only (sign from dividend)
	ps.RegisterCommandInModule("basicmath", "iremainder", func(ctx *Context) Result {
		args, fromList := getNumericArgs(ctx)
		if len(args) < 2 {
			if fromList {
				ctx.LogError(CatCommand, "iremainder: list must contain at least 2 elements")
			} else {
				ctx.LogError(CatCommand, "Usage: iremainder <dividend>, <divisor>, ... or iremainder <list>")
			}
			return BoolStatus(false)
		}
		return performDivision(ctx, args, true, true, false)
	})

	// imodulo - integer division modulo only (sign from divisor)
	ps.RegisterCommandInModule("basicmath", "imodulo", func(ctx *Context) Result {
		args, fromList := getNumericArgs(ctx)
		if len(args) < 2 {
			if fromList {
				ctx.LogError(CatCommand, "imodulo: list must contain at least 2 elements")
			} else {
				ctx.LogError(CatCommand, "Usage: imodulo <dividend>, <divisor>, ... or imodulo <list>")
			}
			return BoolStatus(false)
		}
		return performDivision(ctx, args, true, false, true)
	})

	// fremainder - floating point division remainder only (sign from dividend)
	ps.RegisterCommandInModule("basicmath", "fremainder", func(ctx *Context) Result {
		args, fromList := getNumericArgs(ctx)
		if len(args) < 2 {
			if fromList {
				ctx.LogError(CatCommand, "fremainder: list must contain at least 2 elements")
			} else {
				ctx.LogError(CatCommand, "Usage: fremainder <dividend>, <divisor>, ... or fremainder <list>")
			}
			return BoolStatus(false)
		}
		return performDivision(ctx, args, false, true, false)
	})

	// fmodulo - floating point division modulo only (sign from divisor)
	ps.RegisterCommandInModule("basicmath", "fmodulo", func(ctx *Context) Result {
		args, fromList := getNumericArgs(ctx)
		if len(args) < 2 {
			if fromList {
				ctx.LogError(CatCommand, "fmodulo: list must contain at least 2 elements")
			} else {
				ctx.LogError(CatCommand, "Usage: fmodulo <dividend>, <divisor>, ... or fmodulo <list>")
			}
			return BoolStatus(false)
		}
		return performDivision(ctx, args, false, false, true)
	})

	// floor - rounds down to nearest integer
	ps.RegisterCommandInModule("basicmath", "floor", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: floor <value>")
			return BoolStatus(false)
		}
		resolved := ctx.executor.resolveValue(ctx.Args[0])
		n, ok := toNumber(resolved)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument: %v", ctx.Args[0]))
			return BoolStatus(false)
		}
		ctx.SetResult(math.Floor(n))
		return BoolStatus(true)
	})

	// ceil - rounds up to nearest integer
	ps.RegisterCommandInModule("basicmath", "ceil", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: ceil <value>")
			return BoolStatus(false)
		}
		resolved := ctx.executor.resolveValue(ctx.Args[0])
		n, ok := toNumber(resolved)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument: %v", ctx.Args[0]))
			return BoolStatus(false)
		}
		ctx.SetResult(math.Ceil(n))
		return BoolStatus(true)
	})

	// trunc - truncates towards zero
	ps.RegisterCommandInModule("basicmath", "trunc", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: trunc <value>")
			return BoolStatus(false)
		}
		resolved := ctx.executor.resolveValue(ctx.Args[0])
		n, ok := toNumber(resolved)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument: %v", ctx.Args[0]))
			return BoolStatus(false)
		}
		ctx.SetResult(math.Trunc(n))
		return BoolStatus(true)
	})

	// round - rounds to nearest integer (half away from zero)
	ps.RegisterCommandInModule("basicmath", "round", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: round <value>")
			return BoolStatus(false)
		}
		resolved := ctx.executor.resolveValue(ctx.Args[0])
		n, ok := toNumber(resolved)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument: %v", ctx.Args[0]))
			return BoolStatus(false)
		}
		ctx.SetResult(math.Round(n))
		return BoolStatus(true)
	})

	// abs - absolute value
	ps.RegisterCommandInModule("basicmath", "abs", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: abs <value>")
			return BoolStatus(false)
		}
		resolved := ctx.executor.resolveValue(ctx.Args[0])
		n, ok := toNumber(resolved)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument: %v", ctx.Args[0]))
			return BoolStatus(false)
		}
		ctx.SetResult(math.Abs(n))
		return BoolStatus(true)
	})

	// min - returns minimum of any number of arguments, or minimum element of a list
	ps.RegisterCommandInModule("basicmath", "min", func(ctx *Context) Result {
		args, fromList := getNumericArgs(ctx)
		if len(args) < 1 {
			if fromList {
				ctx.LogError(CatCommand, "min: list must contain at least 1 element")
			} else {
				ctx.LogError(CatCommand, "Usage: min <a>, <b>, ... or min <list>")
			}
			return BoolStatus(false)
		}
		resolved0 := ctx.executor.resolveValue(args[0])
		minVal, ok := toNumber(resolved0)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument at position 1: %v", args[0]))
			return BoolStatus(false)
		}
		for i := 1; i < len(args); i++ {
			resolved := ctx.executor.resolveValue(args[i])
			n, ok := toNumber(resolved)
			if !ok {
				ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument at position %d: %v", i+1, args[i]))
				return BoolStatus(false)
			}
			if n < minVal {
				minVal = n
			}
		}
		ctx.SetResult(minVal)
		return BoolStatus(true)
	})

	// max - returns maximum of any number of arguments, or maximum element of a list
	ps.RegisterCommandInModule("basicmath", "max", func(ctx *Context) Result {
		args, fromList := getNumericArgs(ctx)
		if len(args) < 1 {
			if fromList {
				ctx.LogError(CatCommand, "max: list must contain at least 1 element")
			} else {
				ctx.LogError(CatCommand, "Usage: max <a>, <b>, ... or max <list>")
			}
			return BoolStatus(false)
		}
		resolved0 := ctx.executor.resolveValue(args[0])
		maxVal, ok := toNumber(resolved0)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument at position 1: %v", args[0]))
			return BoolStatus(false)
		}
		for i := 1; i < len(args); i++ {
			resolved := ctx.executor.resolveValue(args[i])
			n, ok := toNumber(resolved)
			if !ok {
				ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument at position %d: %v", i+1, args[i]))
				return BoolStatus(false)
			}
			if n > maxVal {
				maxVal = n
			}
		}
		ctx.SetResult(maxVal)
		return BoolStatus(true)
	})

	// ==================== cmp:: module ====================

	// Helper to get comparison items - either from args directly or from single list arg
	getComparisonItems := func(ctx *Context) []interface{} {
		if len(ctx.Args) == 1 {
			// Single argument - if it's a list, use its items
			if list, ok := ctx.Args[0].(StoredList); ok {
				return list.Items()
			}
			// Single non-list argument - return as-is
			return ctx.Args
		}
		return ctx.Args
	}

	// Helper to check if a value is a list type (for ordering comparison errors)
	isListType := func(v interface{}) bool {
		switch v := v.(type) {
		case StoredList:
			return true
		case Symbol:
			// Check if it's a list marker
			if markerType, _ := parseObjectMarker(string(v)); markerType == "list" {
				return true
			}
		}
		return false
	}

	// compareOrdering compares two values for ordering (lt, gt, lte, gte)
	// Returns: -1 if a < b, 0 if a == b, 1 if a > b, and ok=true
	// Returns ok=false if comparison not possible (e.g., list types)
	// For strings, uses Go's lexicographic comparison which has early exit built-in
	// Explicit string types (QuotedString, StoredString) are always compared alphabetically
	compareOrdering := func(a, b interface{}, ctx *Context) (int, bool) {
		resolvedA := ctx.executor.resolveValue(a)
		resolvedB := ctx.executor.resolveValue(b)

		// Check for list types - can't compare ordering
		if isListType(resolvedA) || isListType(resolvedB) {
			ctx.LogError(CatMath, "Cannot compare ordering of list types")
			return 0, false
		}

		// Check if either value is an explicit string type (QuotedString or StoredString)
		// These should always compare alphabetically, not numerically
		var strA, strB string
		var aIsExplicitStr, bIsExplicitStr bool

		switch va := resolvedA.(type) {
		case StoredString:
			strA, aIsExplicitStr = string(va), true
		case QuotedString:
			strA, aIsExplicitStr = string(va), true
		}

		switch vb := resolvedB.(type) {
		case StoredString:
			strB, bIsExplicitStr = string(vb), true
		case QuotedString:
			strB, bIsExplicitStr = string(vb), true
		}

		// If both are explicit strings, compare alphabetically
		if aIsExplicitStr && bIsExplicitStr {
			if strA < strB {
				return -1, true
			} else if strA > strB {
				return 1, true
			}
			return 0, true
		}

		// If neither is an explicit string, try numeric comparison
		if !aIsExplicitStr && !bIsExplicitStr {
			numA, aIsNum := toNumber(resolvedA)
			numB, bIsNum := toNumber(resolvedB)

			if aIsNum && bIsNum {
				// Both are numbers - numeric comparison
				if numA < numB {
					return -1, true
				} else if numA > numB {
					return 1, true
				}
				return 0, true
			}
		}

		// String comparison for mixed cases or non-numeric values
		// Go's string comparison is lexicographic with early exit
		var aIsStr, bIsStr bool

		// Extract string value without unnecessary copies
		if !aIsExplicitStr {
			switch va := resolvedA.(type) {
			case string:
				strA, aIsStr = va, true
			case Symbol:
				if markerType, _ := parseObjectMarker(string(va)); markerType == "" {
					strA, aIsStr = string(va), true
				}
			}
		} else {
			aIsStr = true
		}

		if !bIsExplicitStr {
			switch vb := resolvedB.(type) {
			case string:
				strB, bIsStr = vb, true
			case Symbol:
				if markerType, _ := parseObjectMarker(string(vb)); markerType == "" {
					strB, bIsStr = string(vb), true
				}
			}
		} else {
			bIsStr = true
		}

		if aIsStr && bIsStr {
			// Both are strings - alphabetical comparison
			if strA < strB {
				return -1, true
			} else if strA > strB {
				return 1, true
			}
			return 0, true
		}

		// Fallback: convert to string representation and compare
		// This handles mixed types or unknown types
		if !aIsStr {
			strA = fmt.Sprintf("%v", resolvedA)
		}
		if !bIsStr {
			strB = fmt.Sprintf("%v", resolvedB)
		}
		if strA < strB {
			return -1, true
		} else if strA > strB {
			return 1, true
		}
		return 0, true
	}

	// eq - all arguments are equal (uses deep equality)
	// With 2+ args: eq a, b, c -> all equal
	// With single list: eq ~mylist -> all items in list are equal
	ps.RegisterCommandInModule("cmp", "eq", func(ctx *Context) Result {
		items := getComparisonItems(ctx)
		if len(items) < 2 {
			ctx.LogError(CatCommand, "Usage: eq <a>, <b> [, ...] or eq <list>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		// Compare all items with deep equality
		first := items[0]
		for i := 1; i < len(items); i++ {
			if !deepEqual(first, items[i], ctx.executor) {
				ctx.SetResult(false)
				return BoolStatus(false)
			}
		}
		ctx.SetResult(true)
		return BoolStatus(true)
	})

	// neq - at least one argument is not equal to another (uses deep equality)
	// With 2+ args: neq a, b, c -> at least one differs
	// With single list: neq ~mylist -> at least one item differs from another
	ps.RegisterCommandInModule("cmp", "neq", func(ctx *Context) Result {
		items := getComparisonItems(ctx)
		if len(items) < 2 {
			ctx.LogError(CatCommand, "Usage: neq <a>, <b> [, ...] or neq <list>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		// Check if at least one pair is not equal
		first := items[0]
		for i := 1; i < len(items); i++ {
			if !deepEqual(first, items[i], ctx.executor) {
				ctx.SetResult(true)
				return BoolStatus(true)
			}
		}
		ctx.SetResult(false)
		return BoolStatus(false)
	})

	// eqs - shallow equality: all arguments are equal (shallow comparison)
	// With 2+ args: eqs a, b, c -> a == b == c (shallow)
	// With single list: eqs ~mylist -> all items equal (shallow)
	// Shallow means: list members compared by identity, not recursively
	ps.RegisterCommandInModule("cmp", "eqs", func(ctx *Context) Result {
		items := getComparisonItems(ctx)
		if len(items) < 2 {
			ctx.LogError(CatCommand, "Usage: eqs <a>, <b> [, ...] or eqs <list>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		// Compare all items with shallow equality
		first := items[0]
		for i := 1; i < len(items); i++ {
			if !shallowEqual(first, items[i], ctx.executor) {
				ctx.SetResult(false)
				return BoolStatus(false)
			}
		}
		ctx.SetResult(true)
		return BoolStatus(true)
	})

	// neqs - shallow inequality: at least one argument differs (shallow comparison)
	// With 2+ args: neqs a, b, c -> at least one differs (shallow)
	// With single list: neqs ~mylist -> at least one item differs (shallow)
	// Shallow means: list members compared by identity, not recursively
	ps.RegisterCommandInModule("cmp", "neqs", func(ctx *Context) Result {
		items := getComparisonItems(ctx)
		if len(items) < 2 {
			ctx.LogError(CatCommand, "Usage: neqs <a>, <b> [, ...] or neqs <list>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		// Check if at least one pair is not equal (shallow)
		first := items[0]
		for i := 1; i < len(items); i++ {
			if !shallowEqual(first, items[i], ctx.executor) {
				ctx.SetResult(true)
				return BoolStatus(true)
			}
		}
		ctx.SetResult(false)
		return BoolStatus(false)
	})

	// lt - all arguments are in strictly ascending order
	// With 2+ args: lt a, b, c -> a < b < c
	// With single list: lt ~mylist -> all items in ascending order
	// Works with numbers (numeric) and strings (alphabetical)
	ps.RegisterCommandInModule("cmp", "lt", func(ctx *Context) Result {
		items := getComparisonItems(ctx)
		if len(items) < 2 {
			ctx.LogError(CatCommand, "Usage: lt <a>, <b> [, ...] or lt <list>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		for i := 0; i < len(items)-1; i++ {
			cmp, ok := compareOrdering(items[i], items[i+1], ctx)
			if !ok {
				ctx.SetResult(false)
				return BoolStatus(false)
			}
			if cmp >= 0 { // Not strictly less than
				ctx.SetResult(false)
				return BoolStatus(false)
			}
		}
		ctx.SetResult(true)
		return BoolStatus(true)
	})

	// gt - all arguments are in strictly descending order
	// With 2+ args: gt a, b, c -> a > b > c
	// With single list: gt ~mylist -> all items in descending order
	// Works with numbers (numeric) and strings (alphabetical)
	ps.RegisterCommandInModule("cmp", "gt", func(ctx *Context) Result {
		items := getComparisonItems(ctx)
		if len(items) < 2 {
			ctx.LogError(CatCommand, "Usage: gt <a>, <b> [, ...] or gt <list>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		for i := 0; i < len(items)-1; i++ {
			cmp, ok := compareOrdering(items[i], items[i+1], ctx)
			if !ok {
				ctx.SetResult(false)
				return BoolStatus(false)
			}
			if cmp <= 0 { // Not strictly greater than
				ctx.SetResult(false)
				return BoolStatus(false)
			}
		}
		ctx.SetResult(true)
		return BoolStatus(true)
	})

	// gte - all arguments are in descending or equal order
	// With 2+ args: gte a, b, c -> a >= b >= c
	// With single list: gte ~mylist -> all items in descending or equal order
	// Works with numbers (numeric) and strings (alphabetical)
	ps.RegisterCommandInModule("cmp", "gte", func(ctx *Context) Result {
		items := getComparisonItems(ctx)
		if len(items) < 2 {
			ctx.LogError(CatCommand, "Usage: gte <a>, <b> [, ...] or gte <list>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		for i := 0; i < len(items)-1; i++ {
			cmp, ok := compareOrdering(items[i], items[i+1], ctx)
			if !ok {
				ctx.SetResult(false)
				return BoolStatus(false)
			}
			if cmp < 0 { // Less than (not >=)
				ctx.SetResult(false)
				return BoolStatus(false)
			}
		}
		ctx.SetResult(true)
		return BoolStatus(true)
	})

	// lte - all arguments are in ascending or equal order
	// With 2+ args: lte a, b, c -> a <= b <= c
	// With single list: lte ~mylist -> all items in ascending or equal order
	// Works with numbers (numeric) and strings (alphabetical)
	ps.RegisterCommandInModule("cmp", "lte", func(ctx *Context) Result {
		items := getComparisonItems(ctx)
		if len(items) < 2 {
			ctx.LogError(CatCommand, "Usage: lte <a>, <b> [, ...] or lte <list>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		for i := 0; i < len(items)-1; i++ {
			cmp, ok := compareOrdering(items[i], items[i+1], ctx)
			if !ok {
				ctx.SetResult(false)
				return BoolStatus(false)
			}
			if cmp > 0 { // Greater than (not <=)
				ctx.SetResult(false)
				return BoolStatus(false)
			}
		}
		ctx.SetResult(true)
		return BoolStatus(true)
	})

	// if - normalize truthy/falsy values to boolean
	ps.RegisterCommandInModule("core", "if", func(ctx *Context) Result {
		if len(ctx.Args) < 1 || len(ctx.Args) > 1 {
			ctx.LogError(CatCommand, "Usage: if <value>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		// Warn if the argument is a literal ParenGroup - likely a mistake
		// User probably meant {command} (execute) not (command) (block literal)
		// Only warn if the raw argument started with '(' (literal), not if it came from a variable
		if _, ok := ctx.Args[0].(ParenGroup); ok {
			if len(ctx.RawArgs) > 0 && strings.HasPrefix(ctx.RawArgs[0], "(") {
				ctx.LogWarning(CatCommand, "if received a block literal (parentheses) which is always truthy; did you mean to use braces {command} instead?")
			}
		}

		// Normalize the first argument to boolean
		result := toBool(ctx.Args[0])
		ctx.SetResult(result)
		return BoolStatus(result)
	})
}

// performDivision is a helper that handles all division variants
// args: the numeric arguments (either from ctx.Args or expanded from a list)
// isInteger: true for floored integer division, false for floating point
// remainderOnly: if true, only return the remainder (sign from dividend)
// moduloOnly: if true, only return the modulo (sign from divisor)
func performDivision(ctx *Context, args []interface{}, isInteger bool, remainderOnly bool, moduloOnly bool) Result {
	// Get dividend (first argument)
	resolved0 := ctx.executor.resolveValue(args[0])
	dividend, ok := toNumber(resolved0)
	if !ok {
		ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument at position 1: %v", args[0]))
		return BoolStatus(false)
	}

	// Calculate divisor as product of remaining arguments
	divisor := float64(1)
	for i := 1; i < len(args); i++ {
		resolved := ctx.executor.resolveValue(args[i])
		n, ok := toNumber(resolved)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument at position %d: %v", i+1, args[i]))
			return BoolStatus(false)
		}
		divisor *= n
	}

	if divisor == 0 {
		ctx.LogError(CatMath, "Division by zero")
		return BoolStatus(false)
	}

	var quotient, remainder, modulo float64

	if isInteger {
		// Floored integer division
		quotient = math.Floor(dividend / divisor)
		// Remainder: sign follows dividend (truncated division remainder)
		remainder = dividend - math.Trunc(dividend/divisor)*divisor
		// Modulo: sign follows divisor (floored division remainder)
		modulo = dividend - quotient*divisor
	} else {
		// Floating point division
		quotient = dividend / divisor
		// For floating point, remainder uses math.Remainder (sign from dividend)
		remainder = math.Remainder(dividend, divisor)
		// Modulo: sign follows divisor
		modulo = math.Mod(dividend, divisor)
		if modulo != 0 && (divisor < 0) != (modulo < 0) {
			modulo += divisor
		}
	}

	// If only returning remainder or modulo
	if remainderOnly {
		ctx.SetResult(remainder)
		return BoolStatus(true)
	}
	if moduloOnly {
		ctx.SetResult(modulo)
		return BoolStatus(true)
	}

	// Check for remainder: or modulo: named arguments
	wantRemainder := false
	wantModulo := false
	if val, exists := ctx.NamedArgs["remainder"]; exists {
		wantRemainder = toBool(val)
	}
	if val, exists := ctx.NamedArgs["modulo"]; exists {
		wantModulo = toBool(val)
	}

	if wantRemainder {
		list := NewStoredList([]interface{}{quotient, remainder})
		id := ctx.executor.storeObject(list, "list")
		marker := fmt.Sprintf("\x00LIST:%d\x00", id)
		ctx.state.SetResultWithoutClaim(Symbol(marker))
		return BoolStatus(true)
	}
	if wantModulo {
		list := NewStoredList([]interface{}{quotient, modulo})
		id := ctx.executor.storeObject(list, "list")
		marker := fmt.Sprintf("\x00LIST:%d\x00", id)
		ctx.state.SetResultWithoutClaim(Symbol(marker))
		return BoolStatus(true)
	}

	ctx.SetResult(quotient)
	return BoolStatus(true)
}
