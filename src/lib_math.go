package pawscript

import (
	"fmt"
)

// RegisterMathLib registers math and comparison commands
// Modules: math, cmp
func (ps *PawScript) RegisterMathLib() {

	// ==================== math:: module ====================

	// add - add numbers
	ps.RegisterCommand("add", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: add <a>, <b>")
			ctx.SetResult(0)
			return BoolStatus(false)
		}
		a, ok1 := toNumber(ctx.Args[0])
		b, ok2 := toNumber(ctx.Args[1])
		if !ok1 || !ok2 {
			ctx.LogError(CatType, "add requires numeric arguments")
			ctx.SetResult(0)
			return BoolStatus(false)
		}
		ctx.SetResult(a + b)
		return BoolStatus(true)
	})

	// sub - subtract numbers
	ps.RegisterCommand("sub", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: sub <a>, <b>")
			ctx.SetResult(0)
			return BoolStatus(false)
		}
		a, ok1 := toNumber(ctx.Args[0])
		b, ok2 := toNumber(ctx.Args[1])
		if !ok1 || !ok2 {
			ctx.LogError(CatType, "sub requires numeric arguments")
			ctx.SetResult(0)
			return BoolStatus(false)
		}
		ctx.SetResult(a - b)
		return BoolStatus(true)
	})

	// mul - multiply numbers
	ps.RegisterCommand("mul", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: mul <a>, <b>")
			ctx.SetResult(0)
			return BoolStatus(false)
		}
		a, ok1 := toNumber(ctx.Args[0])
		b, ok2 := toNumber(ctx.Args[1])
		if !ok1 || !ok2 {
			ctx.LogError(CatType, "mul requires numeric arguments")
			ctx.SetResult(0)
			return BoolStatus(false)
		}
		ctx.SetResult(a * b)
		return BoolStatus(true)
	})

	// div - divide numbers
	ps.RegisterCommand("div", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: div <a>, <b>")
			ctx.SetResult(0)
			return BoolStatus(false)
		}
		a, ok1 := toNumber(ctx.Args[0])
		b, ok2 := toNumber(ctx.Args[1])
		if !ok1 || !ok2 {
			ctx.LogError(CatType, "div requires numeric arguments")
			ctx.SetResult(0)
			return BoolStatus(false)
		}
		if b == 0 {
			ctx.LogError(CatMath, "division by zero")
			ctx.SetResult(0)
			return BoolStatus(false)
		}
		ctx.SetResult(a / b)
		return BoolStatus(true)
	})

	// ==================== cmp:: module ====================

	// eq - check equality
	ps.RegisterCommand("eq", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: eq <a>, <b>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		a := fmt.Sprintf("%v", ctx.Args[0])
		b := fmt.Sprintf("%v", ctx.Args[1])
		result := a == b
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	// lt - less than comparison
	ps.RegisterCommand("lt", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: lt <a>, <b>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		a, ok1 := toNumber(ctx.Args[0])
		b, ok2 := toNumber(ctx.Args[1])
		if !ok1 || !ok2 {
			ctx.LogError(CatType, "lt requires numeric arguments")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		result := a < b
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	// gt - greater than comparison
	ps.RegisterCommand("gt", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: gt <a>, <b>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		a, ok1 := toNumber(ctx.Args[0])
		b, ok2 := toNumber(ctx.Args[1])
		if !ok1 || !ok2 {
			ctx.LogError(CatType, "gt requires numeric arguments")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		result := a > b
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	// gte - greater than or equal comparison
	ps.RegisterCommand("gte", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: gte <a>, <b>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		a, ok1 := toNumber(ctx.Args[0])
		b, ok2 := toNumber(ctx.Args[1])
		if !ok1 || !ok2 {
			ctx.LogError(CatType, "gte requires numeric arguments")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		result := a >= b
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	// lte - less than or equal comparison (not in spec but keeping for completeness)
	ps.RegisterCommand("lte", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: lte <a>, <b>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		a, ok1 := toNumber(ctx.Args[0])
		b, ok2 := toNumber(ctx.Args[1])
		if !ok1 || !ok2 {
			ctx.LogError(CatType, "lte requires numeric arguments")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		result := a <= b
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
