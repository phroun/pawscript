package pawscript

import (
	"fmt"
)

// RegisterMathLib registers math and comparison commands
// Modules: math, cmp
func (ps *PawScript) RegisterMathLib() {

	// ==================== math:: module ====================

	ps.RegisterCommand("add", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: add <a>, <b>")
			return BoolStatus(false)
		}
		// Resolve markers before converting to numbers
		resolved0 := ctx.executor.resolveValue(ctx.Args[0])
		resolved1 := ctx.executor.resolveValue(ctx.Args[1])
		a, aOk := toNumber(resolved0)
		b, bOk := toNumber(resolved1)
		if !aOk || !bOk {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric arguments: %v, %v\n", ctx.Args[0], ctx.Args[1]))
			return BoolStatus(false)
		}
		ctx.SetResult(a + b)
		return BoolStatus(true)
	})

	ps.RegisterCommand("sub", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: sub <a>, <b>")
			return BoolStatus(false)
		}
		// Resolve markers before converting to numbers
		resolved0 := ctx.executor.resolveValue(ctx.Args[0])
		resolved1 := ctx.executor.resolveValue(ctx.Args[1])
		a, aOk := toNumber(resolved0)
		b, bOk := toNumber(resolved1)
		if !aOk || !bOk {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric arguments: %v, %v\n", ctx.Args[0], ctx.Args[1]))
			return BoolStatus(false)
		}
		ctx.SetResult(a - b)
		return BoolStatus(true)
	})

	ps.RegisterCommand("mul", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: mul <a>, <b>")
			return BoolStatus(false)
		}
		// Resolve markers before converting to numbers
		resolved0 := ctx.executor.resolveValue(ctx.Args[0])
		resolved1 := ctx.executor.resolveValue(ctx.Args[1])
		a, aOk := toNumber(resolved0)
		b, bOk := toNumber(resolved1)
		if !aOk || !bOk {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric arguments: %v, %v\n", ctx.Args[0], ctx.Args[1]))
			return BoolStatus(false)
		}
		ctx.SetResult(a * b)
		return BoolStatus(true)
	})

	ps.RegisterCommand("div", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: div <a>, <b>")
			return BoolStatus(false)
		}
		// Resolve markers before converting to numbers
		resolved0 := ctx.executor.resolveValue(ctx.Args[0])
		resolved1 := ctx.executor.resolveValue(ctx.Args[1])
		a, aOk := toNumber(resolved0)
		b, bOk := toNumber(resolved1)
		if !aOk || !bOk {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric arguments: %v, %v\n", ctx.Args[0], ctx.Args[1]))
			return BoolStatus(false)
		}
		if b == 0 {
			ctx.LogError(CatMath, "Division by zero")
			return BoolStatus(false)
		}
		ctx.SetResult(a / b)
		return BoolStatus(true)
	})

	// ==================== cmp:: module ====================

	ps.RegisterCommand("eq", func(ctx *Context) Result {
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

	ps.RegisterCommand("lt", func(ctx *Context) Result {
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

	ps.RegisterCommand("gt", func(ctx *Context) Result {
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

	ps.RegisterCommand("gte", func(ctx *Context) Result {
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

	ps.RegisterCommand("lte", func(ctx *Context) Result {
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
	ps.RegisterCommand("if", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: if <value>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		// Normalize the first argument to boolean
		result := toBool(ctx.Args[0])
		ctx.SetResult(result)
		return BoolStatus(result)
	})
}
