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

	// if - conditional execution
	ps.RegisterCommand("if", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: if <condition>, <then_body>, [else_body]")
			return BoolStatus(false)
		}

		condition := ctx.Args[0]
		thenBody := fmt.Sprintf("%v", ctx.Args[1])

		// Evaluate condition
		condResult := evaluateCondition(condition)

		if condResult {
			// Execute then body
			thenState := NewExecutionStateFromSharedVars(ctx.state)
			result := ctx.executor.ExecuteWithState(thenBody, thenState, nil, ctx.Position.Filename, ctx.Position.Line, ctx.Position.Column)

			if thenState.HasResult() {
				ctx.state.SetResult(thenState.GetResult())
			}

			if status, ok := result.(BoolStatus); ok {
				return status
			}
			return BoolStatus(true)
		} else if len(ctx.Args) >= 3 {
			// Execute else body if provided
			elseBody := fmt.Sprintf("%v", ctx.Args[2])
			elseState := NewExecutionStateFromSharedVars(ctx.state)
			result := ctx.executor.ExecuteWithState(elseBody, elseState, nil, ctx.Position.Filename, ctx.Position.Line, ctx.Position.Column)

			if elseState.HasResult() {
				ctx.state.SetResult(elseState.GetResult())
			}

			if status, ok := result.(BoolStatus); ok {
				return status
			}
			return BoolStatus(true)
		}

		return BoolStatus(false)
	})
}

// evaluateCondition evaluates a condition value to boolean
func evaluateCondition(condition interface{}) bool {
	switch v := condition.(type) {
	case bool:
		return v
	case int64:
		return v != 0
	case float64:
		return v != 0
	case string:
		lower := v
		if lower == "true" || lower == "1" {
			return true
		}
		if lower == "false" || lower == "0" || lower == "" {
			return false
		}
		return len(v) > 0
	case Symbol:
		s := string(v)
		if s == "true" || s == "1" {
			return true
		}
		if s == "false" || s == "0" || s == "" {
			return false
		}
		return len(s) > 0
	case nil:
		return false
	default:
		return true
	}
}
