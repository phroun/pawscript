package pawscript

import (
	"fmt"
	"math"
)

// RegisterMathLib registers math and comparison commands
// Modules: math, cmp
func (ps *PawScript) RegisterMathLib() {

	// ==================== math:: module ====================

	// add - sums any number of arguments
	ps.RegisterCommandInModule("math", "add", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: add <a>, <b>, ...")
			return BoolStatus(false)
		}
		sum := float64(0)
		for i, arg := range ctx.Args {
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

	// sub - subtracts all remaining arguments from the first
	ps.RegisterCommandInModule("math", "sub", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: sub <start>, <a>, ...")
			return BoolStatus(false)
		}
		resolved0 := ctx.executor.resolveValue(ctx.Args[0])
		result, ok := toNumber(resolved0)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument at position 1: %v", ctx.Args[0]))
			return BoolStatus(false)
		}
		for i := 1; i < len(ctx.Args); i++ {
			resolved := ctx.executor.resolveValue(ctx.Args[i])
			n, ok := toNumber(resolved)
			if !ok {
				ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument at position %d: %v", i+1, ctx.Args[i]))
				return BoolStatus(false)
			}
			result -= n
		}
		ctx.SetResult(result)
		return BoolStatus(true)
	})

	// mul - multiplies any number of arguments
	ps.RegisterCommandInModule("math", "mul", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: mul <a>, <b>, ...")
			return BoolStatus(false)
		}
		product := float64(1)
		for i, arg := range ctx.Args {
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
	ps.RegisterCommandInModule("math", "idiv", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: idiv <dividend>, <divisor>, ... [remainder: true] [modulo: true]")
			return BoolStatus(false)
		}
		return performDivision(ctx, true, false, false)
	})

	// fdiv - floating point division
	// With >2 args: first / product(rest)
	// Named args: remainder:true or modulo:true returns [result, remainder/modulo]
	ps.RegisterCommandInModule("math", "fdiv", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: fdiv <dividend>, <divisor>, ... [remainder: true] [modulo: true]")
			return BoolStatus(false)
		}
		return performDivision(ctx, false, false, false)
	})

	// iremainder - integer division remainder only (sign from dividend)
	ps.RegisterCommandInModule("math", "iremainder", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: iremainder <dividend>, <divisor>, ...")
			return BoolStatus(false)
		}
		return performDivision(ctx, true, true, false)
	})

	// imodulo - integer division modulo only (sign from divisor)
	ps.RegisterCommandInModule("math", "imodulo", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: imodulo <dividend>, <divisor>, ...")
			return BoolStatus(false)
		}
		return performDivision(ctx, true, false, true)
	})

	// fremainder - floating point division remainder only (sign from dividend)
	ps.RegisterCommandInModule("math", "fremainder", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: fremainder <dividend>, <divisor>, ...")
			return BoolStatus(false)
		}
		return performDivision(ctx, false, true, false)
	})

	// fmodulo - floating point division modulo only (sign from divisor)
	ps.RegisterCommandInModule("math", "fmodulo", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: fmodulo <dividend>, <divisor>, ...")
			return BoolStatus(false)
		}
		return performDivision(ctx, false, false, true)
	})

	// floor - rounds down to nearest integer
	ps.RegisterCommandInModule("math", "floor", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("math", "ceil", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("math", "trunc", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("math", "round", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("math", "abs", func(ctx *Context) Result {
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

	// min - returns minimum of any number of arguments
	ps.RegisterCommandInModule("math", "min", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: min <a>, <b>, ...")
			return BoolStatus(false)
		}
		resolved0 := ctx.executor.resolveValue(ctx.Args[0])
		minVal, ok := toNumber(resolved0)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument at position 1: %v", ctx.Args[0]))
			return BoolStatus(false)
		}
		for i := 1; i < len(ctx.Args); i++ {
			resolved := ctx.executor.resolveValue(ctx.Args[i])
			n, ok := toNumber(resolved)
			if !ok {
				ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument at position %d: %v", i+1, ctx.Args[i]))
				return BoolStatus(false)
			}
			if n < minVal {
				minVal = n
			}
		}
		ctx.SetResult(minVal)
		return BoolStatus(true)
	})

	// max - returns maximum of any number of arguments
	ps.RegisterCommandInModule("math", "max", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: max <a>, <b>, ...")
			return BoolStatus(false)
		}
		resolved0 := ctx.executor.resolveValue(ctx.Args[0])
		maxVal, ok := toNumber(resolved0)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument at position 1: %v", ctx.Args[0]))
			return BoolStatus(false)
		}
		for i := 1; i < len(ctx.Args); i++ {
			resolved := ctx.executor.resolveValue(ctx.Args[i])
			n, ok := toNumber(resolved)
			if !ok {
				ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument at position %d: %v", i+1, ctx.Args[i]))
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

	ps.RegisterCommandInModule("cmp", "eq", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: eq <a>, <b>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		// Resolve markers before comparing
		resolved0 := ctx.executor.resolveValue(ctx.Args[0])
		resolved1 := ctx.executor.resolveValue(ctx.Args[1])
		result := fmt.Sprintf("%v", resolved0) == fmt.Sprintf("%v", resolved1)
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	ps.RegisterCommandInModule("cmp", "lt", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: lt <a>, <b>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		// Resolve markers first
		resolved0 := ctx.executor.resolveValue(ctx.Args[0])
		resolved1 := ctx.executor.resolveValue(ctx.Args[1])
		a, aOk := toNumber(resolved0)
		b, bOk := toNumber(resolved1)
		if aOk && bOk {
			result := a < b
			ctx.SetResult(result)
			return BoolStatus(result)
		}
		// String comparison as fallback
		result := fmt.Sprintf("%v", resolved0) < fmt.Sprintf("%v", resolved1)
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	ps.RegisterCommandInModule("cmp", "gt", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: gt <a>, <b>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		// Resolve markers first
		resolved0 := ctx.executor.resolveValue(ctx.Args[0])
		resolved1 := ctx.executor.resolveValue(ctx.Args[1])
		a, aOk := toNumber(resolved0)
		b, bOk := toNumber(resolved1)
		if aOk && bOk {
			result := a > b
			ctx.SetResult(result)
			return BoolStatus(result)
		}
		// String comparison as fallback
		result := fmt.Sprintf("%v", resolved0) > fmt.Sprintf("%v", resolved1)
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	ps.RegisterCommandInModule("cmp", "gte", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: gte <a>, <b>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		// Resolve markers first
		resolved0 := ctx.executor.resolveValue(ctx.Args[0])
		resolved1 := ctx.executor.resolveValue(ctx.Args[1])
		a, aOk := toNumber(resolved0)
		b, bOk := toNumber(resolved1)
		if aOk && bOk {
			result := a >= b
			ctx.SetResult(result)
			return BoolStatus(result)
		}
		// String comparison as fallback
		result := fmt.Sprintf("%v", resolved0) >= fmt.Sprintf("%v", resolved1)
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	ps.RegisterCommandInModule("cmp", "lte", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: lte <a>, <b>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		// Resolve markers first
		resolved0 := ctx.executor.resolveValue(ctx.Args[0])
		resolved1 := ctx.executor.resolveValue(ctx.Args[1])
		a, aOk := toNumber(resolved0)
		b, bOk := toNumber(resolved1)
		if aOk && bOk {
			result := a <= b
			ctx.SetResult(result)
			return BoolStatus(result)
		}
		// String comparison as fallback
		result := fmt.Sprintf("%v", resolved0) <= fmt.Sprintf("%v", resolved1)
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	// if - normalize truthy/falsy values to boolean
	ps.RegisterCommandInModule("flow", "if", func(ctx *Context) Result {
		if len(ctx.Args) < 1 || len(ctx.Args) > 1 {
			ctx.LogError(CatCommand, "Usage: if <value>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		// Warn if the argument is a ParenGroup - likely a mistake
		// User probably meant {command} (execute) not (command) (block literal)
		if _, ok := ctx.Args[0].(ParenGroup); ok {
			ctx.LogWarning(CatCommand, "if received a block literal (parentheses) which is always truthy; did you mean to use braces {command} instead?")
		}

		// Normalize the first argument to boolean
		result := toBool(ctx.Args[0])
		ctx.SetResult(result)
		return BoolStatus(result)
	})
}

// performDivision is a helper that handles all division variants
// isInteger: true for floored integer division, false for floating point
// remainderOnly: if true, only return the remainder (sign from dividend)
// moduloOnly: if true, only return the modulo (sign from divisor)
func performDivision(ctx *Context, isInteger bool, remainderOnly bool, moduloOnly bool) Result {
	// Get dividend (first argument)
	resolved0 := ctx.executor.resolveValue(ctx.Args[0])
	dividend, ok := toNumber(resolved0)
	if !ok {
		ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument at position 1: %v", ctx.Args[0]))
		return BoolStatus(false)
	}

	// Calculate divisor as product of remaining arguments
	divisor := float64(1)
	for i := 1; i < len(ctx.Args); i++ {
		resolved := ctx.executor.resolveValue(ctx.Args[i])
		n, ok := toNumber(resolved)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument at position %d: %v", i+1, ctx.Args[i]))
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
