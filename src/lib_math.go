package pawscript

import (
	"fmt"
	"math"
)

// Mathematical constants - using Go's float64 precision
const (
	// Tau is the circle constant (2*pi) - the superior circle constant
	Tau = 6.283185307179586476925286766559005768394338798750211641949889184615632812572417997256069650684234
	// E is Euler's number
	E = math.E // 2.718281828459045235360287471352662497757247093699959574966...
	// Root2 is the square root of 2
	Root2 = math.Sqrt2 // 1.41421356237309504880168872420969807856967187537694...
	// Root3 is the square root of 3
	Root3 = 1.7320508075688772935274463415058723669428052538103806280558
	// Root5 is the square root of 5
	Root5 = 2.2360679774997896964091736687747632440588203494105043021130
	// Phi is the golden ratio (1 + sqrt(5)) / 2
	Phi = math.Phi // 1.61803398874989484820458683436563811772030917980576...
	// Ln2 is the natural logarithm of 2
	Ln2 = math.Ln2 // 0.693147180559945309417232121458176568075500134360255254120...
)

// RegisterMathLib registers the auxiliary math library with trigonometric functions
// and mathematical constants. This library is NOT auto-imported - users must
// explicitly use IMPORT math to access these functions.
// Module: math
func (ps *PawScript) RegisterMathLib() {

	// ==================== math:: module ====================

	// sin - sine of angle in radians
	ps.RegisterCommandInModule("math", "sin", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: sin <radians>")
			return BoolStatus(false)
		}
		resolved := ctx.executor.resolveValue(ctx.Args[0])
		n, ok := toNumber(resolved)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument: %v", ctx.Args[0]))
			return BoolStatus(false)
		}
		ctx.SetResult(math.Sin(n))
		return BoolStatus(true)
	})

	// cos - cosine of angle in radians
	ps.RegisterCommandInModule("math", "cos", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: cos <radians>")
			return BoolStatus(false)
		}
		resolved := ctx.executor.resolveValue(ctx.Args[0])
		n, ok := toNumber(resolved)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument: %v", ctx.Args[0]))
			return BoolStatus(false)
		}
		ctx.SetResult(math.Cos(n))
		return BoolStatus(true)
	})

	// tan - tangent of angle in radians
	ps.RegisterCommandInModule("math", "tan", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: tan <radians>")
			return BoolStatus(false)
		}
		resolved := ctx.executor.resolveValue(ctx.Args[0])
		n, ok := toNumber(resolved)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument: %v", ctx.Args[0]))
			return BoolStatus(false)
		}
		ctx.SetResult(math.Tan(n))
		return BoolStatus(true)
	})

	// atan2 - arc tangent of y/x, using signs to determine quadrant
	ps.RegisterCommandInModule("math", "atan2", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: atan2 <y>, <x>")
			return BoolStatus(false)
		}
		resolvedY := ctx.executor.resolveValue(ctx.Args[0])
		y, ok := toNumber(resolvedY)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument for y: %v", ctx.Args[0]))
			return BoolStatus(false)
		}
		resolvedX := ctx.executor.resolveValue(ctx.Args[1])
		x, ok := toNumber(resolvedX)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument for x: %v", ctx.Args[1]))
			return BoolStatus(false)
		}
		ctx.SetResult(math.Atan2(y, x))
		return BoolStatus(true)
	})

	// deg - convert radians to degrees
	ps.RegisterCommandInModule("math", "deg", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: deg <radians>")
			return BoolStatus(false)
		}
		resolved := ctx.executor.resolveValue(ctx.Args[0])
		n, ok := toNumber(resolved)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument: %v", ctx.Args[0]))
			return BoolStatus(false)
		}
		// degrees = radians * (180 / pi) = radians * (360 / tau)
		ctx.SetResult(n * 360.0 / Tau)
		return BoolStatus(true)
	})

	// rad - convert degrees to radians
	ps.RegisterCommandInModule("math", "rad", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: rad <degrees>")
			return BoolStatus(false)
		}
		resolved := ctx.executor.resolveValue(ctx.Args[0])
		n, ok := toNumber(resolved)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument: %v", ctx.Args[0]))
			return BoolStatus(false)
		}
		// radians = degrees * (pi / 180) = degrees * (tau / 360)
		ctx.SetResult(n * Tau / 360.0)
		return BoolStatus(true)
	})

	// log - logarithm with optional base (default base 10)
	ps.RegisterCommandInModule("math", "log", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: log <value> [, base:<base>]")
			return BoolStatus(false)
		}
		resolved := ctx.executor.resolveValue(ctx.Args[0])
		n, ok := toNumber(resolved)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument: %v", ctx.Args[0]))
			return BoolStatus(false)
		}
		if n <= 0 {
			ctx.LogError(CatMath, "Logarithm requires positive argument")
			return BoolStatus(false)
		}

		// Check for base: named argument, default to 10
		base := 10.0
		if baseVal, exists := ctx.NamedArgs["base"]; exists {
			b, ok := toNumber(baseVal)
			if !ok || b <= 0 || b == 1 {
				ctx.LogError(CatArgument, "base must be a positive number not equal to 1")
				return BoolStatus(false)
			}
			base = b
		}

		// log_b(x) = ln(x) / ln(b)
		ctx.SetResult(math.Log(n) / math.Log(base))
		return BoolStatus(true)
	})

	// log10 - base-10 logarithm
	ps.RegisterCommandInModule("math", "log10", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: log10 <value>")
			return BoolStatus(false)
		}
		resolved := ctx.executor.resolveValue(ctx.Args[0])
		n, ok := toNumber(resolved)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument: %v", ctx.Args[0]))
			return BoolStatus(false)
		}
		if n <= 0 {
			ctx.LogError(CatMath, "Logarithm requires positive argument")
			return BoolStatus(false)
		}
		ctx.SetResult(math.Log10(n))
		return BoolStatus(true)
	})

	// ln - natural logarithm (base e)
	ps.RegisterCommandInModule("math", "ln", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: ln <value>")
			return BoolStatus(false)
		}
		resolved := ctx.executor.resolveValue(ctx.Args[0])
		n, ok := toNumber(resolved)
		if !ok {
			ctx.LogError(CatArgument, fmt.Sprintf("Invalid numeric argument: %v", ctx.Args[0]))
			return BoolStatus(false)
		}
		if n <= 0 {
			ctx.LogError(CatMath, "Natural logarithm requires positive argument")
			return BoolStatus(false)
		}
		ctx.SetResult(math.Log(n))
		return BoolStatus(true)
	})

	// Register mathematical constants as objects
	ps.RegisterObjectInModule("math", "#tau", Tau)
	ps.RegisterObjectInModule("math", "#e", E)
	ps.RegisterObjectInModule("math", "#root2", Root2)
	ps.RegisterObjectInModule("math", "#root3", Root3)
	ps.RegisterObjectInModule("math", "#root5", Root5)
	ps.RegisterObjectInModule("math", "#phi", Phi)
	ps.RegisterObjectInModule("math", "#ln2", Ln2)
}
