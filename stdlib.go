package pawscript

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// RegisterStandardLibrary registers standard library commands
func (ps *PawScript) RegisterStandardLibrary(scriptArgs []string) {
	// argc - returns number of arguments
	ps.RegisterCommand("argc", func(ctx *Context) Result {
		ctx.SetResult(len(scriptArgs))
		return BoolStatus(true)
	})
	
	// argv - returns array of arguments or specific argument by index
	ps.RegisterCommand("argv", func(ctx *Context) Result {
		if len(ctx.Args) == 0 {
			ctx.SetResult(scriptArgs)
		} else {
			index, ok := ctx.Args[0].(int64)
			if !ok {
				// Try to convert from float
				if f, ok := ctx.Args[0].(float64); ok {
					index = int64(f)
				} else {
					ctx.SetResult(nil)
					return BoolStatus(true)
				}
			}
			
			if index >= 0 && int(index) < len(scriptArgs) {
				ctx.SetResult(scriptArgs[index])
			} else {
				ctx.SetResult(nil)
			}
		}
		return BoolStatus(true)
	})
	
	// script_error - output error messages
	ps.RegisterCommand("script_error", func(ctx *Context) Result {
		message := "Unknown error"
		if len(ctx.Args) > 0 {
			message = fmt.Sprintf("%v", ctx.Args[0])
		}
		
		errorOutput := fmt.Sprintf("[SCRIPT ERROR] %s", message)
		
		if ctx.Position != nil {
			errorOutput += fmt.Sprintf(" at line %d, column %d", ctx.Position.Line, ctx.Position.Column)
			
			if ctx.Position.OriginalText != "" {
				errorOutput += fmt.Sprintf("\n  Source: %s", ctx.Position.OriginalText)
			}
		}
		
		fmt.Fprintln(os.Stderr, errorOutput)
		return BoolStatus(true)
	})
	
	// echo/write/print - output to stdout (no automatic newline)
	outputCommand := func(ctx *Context) Result {
		text := ""
		for _, arg := range ctx.Args {
			// No automatic spaces
			text += fmt.Sprintf("%v", arg)
		}
		fmt.Print(text) // No automatic newline - use \n explicitly if needed
		return BoolStatus(true)
	}

	outputLineCommand := func(ctx *Context) Result {
		text := ""
		for i, arg := range ctx.Args {
			if i > 0 {
				text += " "
			}
			text += fmt.Sprintf("%v", arg)
		}
		fmt.Println(text) // Automatic newline in this version!
		return BoolStatus(true)
	}
	
	ps.RegisterCommand("write", outputCommand)
	ps.RegisterCommand("echo", outputLineCommand)
	ps.RegisterCommand("print", outputLineCommand)
	
	// read - read a line from stdin
	ps.RegisterCommand("read", func(ctx *Context) Result {
		token := ctx.RequestToken(nil)
		
		go func() {
			reader := bufio.NewReader(os.Stdin)
			line, err := reader.ReadString('\n')
			
			if err == nil {
				// Remove trailing newline
				if len(line) > 0 && line[len(line)-1] == '\n' {
					line = line[:len(line)-1]
				}
				ctx.SetResult(line)
				ctx.ResumeToken(token, true)
			} else {
				ctx.SetResult("")
				ctx.ResumeToken(token, false)
			}
		}()
		
		return TokenResult(token)
	})
	
	// exec - execute external command and capture output
	ps.RegisterCommand("exec", func(ctx *Context) Result {
		if len(ctx.Args) == 0 {
			fmt.Fprintln(os.Stderr, "[EXEC ERROR] No command specified")
			return BoolStatus(false)
		}
		
		// First argument is the command
		cmdName := fmt.Sprintf("%v", ctx.Args[0])
		
		// Remaining arguments are command arguments
		var cmdArgs []string
		for i := 1; i < len(ctx.Args); i++ {
			cmdArgs = append(cmdArgs, fmt.Sprintf("%v", ctx.Args[i]))
		}
		
		// Create the command
		cmd := exec.Command(cmdName, cmdArgs...)
		
		// Set up buffers to capture output
		var stdoutBuf, stderrBuf bytes.Buffer
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf
		
		// Execute the command
		err := cmd.Run()
		
		// Get the output
		stdout := stdoutBuf.String()
		stderr := stderrBuf.String()
		
		// Print stderr if present (regardless of execution success)
		if stderr != "" {
			fmt.Fprint(os.Stderr, stderr)
		}
		
		// Check if stderr has non-whitespace content
		hasStderrContent := strings.TrimSpace(stderr) != ""
		
		// Determine success
		success := err == nil && !hasStderrContent
		
		// Set stdout as the result (even on failure, might be useful)
		ctx.SetResult(stdout)
		
		// Return success status
		return BoolStatus(success)
	})
	
	// true - sets success state
	ps.RegisterCommand("true", func(ctx *Context) Result {
		return BoolStatus(true)
	})
	
	// false - sets error state
	ps.RegisterCommand("false", func(ctx *Context) Result {
		return BoolStatus(false)
	})

	// set_result - explicitly sets the result value
	ps.RegisterCommand("set_result", func(ctx *Context) Result {
		if len(ctx.Args) > 0 {
			ctx.SetResult(ctx.Args[0])
		} else {
			ctx.SetResult(nil)
		}
		return BoolStatus(true)
	})

	// get_result - gets the current result value
	ps.RegisterCommand("get_result", func(ctx *Context) Result {
	    /*fmt.Fprintf(os.Stderr, "[DEBUG get_result] HasResult: %v, Result: %v\n", 
		ctx.HasResult(), ctx.GetResult())*/
	    return BoolStatus(true)
	})
	
	// set - sets a variable in current scope
	ps.RegisterCommand("set", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[SET ERROR] Usage: set <name> <value>")
			return BoolStatus(false)
		}
		
		varName := fmt.Sprintf("%v", ctx.Args[0])
		value := ctx.Args[1]
		
		ctx.state.SetVariable(varName, value)
		return BoolStatus(true)
	})
	
	// get - gets a variable from current scope and sets it as result
	ps.RegisterCommand("get", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			fmt.Fprintln(os.Stderr, "[GET ERROR] Usage: get <name>")
			return BoolStatus(false)
		}
		
		varName := fmt.Sprintf("%v", ctx.Args[0])
		value, exists := ctx.state.GetVariable(varName)
		
		if exists {
			ctx.SetResult(value)
			return BoolStatus(true)
		}
		
		fmt.Fprintf(os.Stderr, "[GET ERROR] Variable not found: %s\n", varName)
		return BoolStatus(false)
	})
	
	// while - loop while condition is true
	ps.RegisterCommand("while", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[WHILE ERROR] Usage: while (condition) (body)")
			return BoolStatus(false)
		}
		
		conditionBlock := fmt.Sprintf("%v", ctx.Args[0])
		bodyBlock := fmt.Sprintf("%v", ctx.Args[1])
		
		maxIterations := 10000 // Safety limit
		iterations := 0
		
		for iterations < maxIterations {
			// Execute condition block
			condResult := ctx.executor.ExecuteWithState(
				conditionBlock,
				ctx.state,
				nil,
				"",
				0, 0,
			)
			
			// Check if we got a token (async not supported in while condition)
			if _, isToken := condResult.(TokenResult); isToken {
				fmt.Fprintln(os.Stderr, "[WHILE ERROR] Async operations not supported in while condition")
				return BoolStatus(false)
			}
			
			// Check if condition is true
			shouldContinue := false
			if boolRes, ok := condResult.(BoolStatus); ok {
				shouldContinue = bool(boolRes)
			}
			
			if !shouldContinue {
				break
			}
			
			// Execute body block
			bodyResult := ctx.executor.ExecuteWithState(
				bodyBlock,
				ctx.state,
				nil,
				"",
				0, 0,
			)
			
			// If body returns a token (async), return it
			if _, isToken := bodyResult.(TokenResult); isToken {
				return bodyResult
			}
			
			// Result is whatever the body set (don't overwrite it)
			iterations++
		}
		
		if iterations >= maxIterations {
			fmt.Fprintln(os.Stderr, "[WHILE ERROR] Maximum iterations (10000) exceeded")
			return BoolStatus(false)
		}
		
		return BoolStatus(true)
	})
	
	// Arithmetic operations
	ps.RegisterCommand("add", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[ADD ERROR] Usage: add <a>, <b>")
			return BoolStatus(false)
		}
		a, aOk := toNumber(ctx.Args[0])
		b, bOk := toNumber(ctx.Args[1])
		if !aOk || !bOk {
			fmt.Fprintf(os.Stderr, "[ADD ERROR] Invalid numeric arguments: %v, %v\n", ctx.Args[0], ctx.Args[1])
			return BoolStatus(false)
		}
		ctx.SetResult(a + b)
		return BoolStatus(true)
	})
	
	ps.RegisterCommand("sub", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[SUB ERROR] Usage: sub <a>, <b>")
			return BoolStatus(false)
		}
		a, aOk := toNumber(ctx.Args[0])
		b, bOk := toNumber(ctx.Args[1])
		if !aOk || !bOk {
			fmt.Fprintf(os.Stderr, "[SUB ERROR] Invalid numeric arguments: %v, %v\n", ctx.Args[0], ctx.Args[1])
			return BoolStatus(false)
		}
		ctx.SetResult(a - b)
		return BoolStatus(true)
	})
	
	ps.RegisterCommand("mul", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[MUL ERROR] Usage: mul <a>, <b>")
			return BoolStatus(false)
		}
		a, aOk := toNumber(ctx.Args[0])
		b, bOk := toNumber(ctx.Args[1])
		if !aOk || !bOk {
			fmt.Fprintf(os.Stderr, "[MUL ERROR] Invalid numeric arguments: %v, %v\n", ctx.Args[0], ctx.Args[1])
			return BoolStatus(false)
		}
		ctx.SetResult(a * b)
		return BoolStatus(true)
	})
	
	ps.RegisterCommand("div", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[DIV ERROR] Usage: div <a>, <b>")
			return BoolStatus(false)
		}
		a, aOk := toNumber(ctx.Args[0])
		b, bOk := toNumber(ctx.Args[1])
		if !aOk || !bOk {
			fmt.Fprintf(os.Stderr, "[DIV ERROR] Invalid numeric arguments: %v, %v\n", ctx.Args[0], ctx.Args[1])
			return BoolStatus(false)
		}
		if b == 0 {
			fmt.Fprintln(os.Stderr, "[DIV ERROR] Division by zero")
			return BoolStatus(false)
		}
		ctx.SetResult(a / b)
		return BoolStatus(true)
	})
	
	// Comparison operations
	ps.RegisterCommand("eq", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[EQ ERROR] Usage: eq <a>, <b>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		result := fmt.Sprintf("%v", ctx.Args[0]) == fmt.Sprintf("%v", ctx.Args[1])
		ctx.SetResult(result)
		return BoolStatus(result)
	})
	
	ps.RegisterCommand("lt", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[LT ERROR] Usage: lt <a>, <b>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		a, aOk := toNumber(ctx.Args[0])
		b, bOk := toNumber(ctx.Args[1])
		if aOk && bOk {
			result := a < b
			ctx.SetResult(result)
			return BoolStatus(result)
		}
		// String comparison as fallback
		result := fmt.Sprintf("%v", ctx.Args[0]) < fmt.Sprintf("%v", ctx.Args[1])
		ctx.SetResult(result)
		return BoolStatus(result)
	})
	
	ps.RegisterCommand("gt", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[GT ERROR] Usage: gt <a>, <b>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		a, aOk := toNumber(ctx.Args[0])
		b, bOk := toNumber(ctx.Args[1])
		if aOk && bOk {
			result := a > b
			ctx.SetResult(result)
			return BoolStatus(result)
		}
		// String comparison as fallback
		result := fmt.Sprintf("%v", ctx.Args[0]) > fmt.Sprintf("%v", ctx.Args[1])
		ctx.SetResult(result)
		return BoolStatus(result)
	})
	
	ps.RegisterCommand("gte", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[GTE ERROR] Usage: gte <a>, <b>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		a, aOk := toNumber(ctx.Args[0])
		b, bOk := toNumber(ctx.Args[1])
		if aOk && bOk {
			result := a >= b
			ctx.SetResult(result)
			return BoolStatus(result)
		}
		// String comparison as fallback
		result := fmt.Sprintf("%v", ctx.Args[0]) >= fmt.Sprintf("%v", ctx.Args[1])
		ctx.SetResult(result)
		return BoolStatus(result)
	})
	
	ps.RegisterCommand("lte", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[LTE ERROR] Usage: lte <a>, <b>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		a, aOk := toNumber(ctx.Args[0])
		b, bOk := toNumber(ctx.Args[1])
		if aOk && bOk {
			result := a <= b
			ctx.SetResult(result)
			return BoolStatus(result)
		}
		// String comparison as fallback
		result := fmt.Sprintf("%v", ctx.Args[0]) <= fmt.Sprintf("%v", ctx.Args[1])
		ctx.SetResult(result)
		return BoolStatus(result)
	})
	
	// if - normalize truthy/falsy values to boolean
	ps.RegisterCommand("if", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			fmt.Fprintln(os.Stderr, "[IF ERROR] Usage: if <value>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}
		
		// Normalize the first argument to boolean
		result := toBool(ctx.Args[0])
		ctx.SetResult(result)
		return BoolStatus(result)
	})
}

// Helper function to convert values to numbers
func toNumber(val interface{}) (float64, bool) {
	switch v := val.(type) {
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case int:
		return float64(v), true
	case string:
		// Try to parse as float
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, true
		}
		// Try to parse as int
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return float64(i), true
		}
		return 0, false
	default:
		return 0, false
	}
}

// Helper function to convert values to boolean
func toBool(val interface{}) bool {
	switch v := val.(type) {
	case bool:
		return v
	case int64:
		return v != 0
	case float64:
		return v != 0.0
	case string:
		// Empty string, "false", "0" are false
		lowerVal := strings.ToLower(strings.TrimSpace(v))
		return lowerVal != "" && 
			   lowerVal != "false" && 
			   lowerVal != "0"
	case nil:
		return false
	default:
		// Non-nil unknown types are truthy
		return true
	}
}
