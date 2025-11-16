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
	// Usage: argc           - returns count of script arguments
	//        argc (a, b, c) - returns count of items in list (3)
	ps.RegisterCommand("argc", func(ctx *Context) Result {
		if len(ctx.Args) == 0 {
			// No arguments - return script arg count
			ctx.SetResult(len(scriptArgs))
			return BoolStatus(true)
		}
		
		// Argument provided - parse it as a list
		listArg := ctx.Args[0]
		
		// If it's a ParenGroup, parse the contents
		if parenGroup, ok := listArg.(ParenGroup); ok {
			args := parseArguments(string(parenGroup))
			ctx.SetResult(len(args))
			return BoolStatus(true)
		}
		
		// If it's a string that looks like a list, parse it
		if str, ok := listArg.(string); ok {
			args := parseArguments(str)
			ctx.SetResult(len(args))
			return BoolStatus(true)
		}
		
		// Single item
		ctx.SetResult(1)
		return BoolStatus(true)
	})
	
	// argv - returns array of arguments or specific argument by index
	// Usage: argv              - returns all script arguments
	//        argv 1            - returns first script argument (1-indexed)
	//        argv (a, b, c)    - returns all items in list
	//        argv (a, b, c), 2 - returns second item from list (1-indexed)
	ps.RegisterCommand("argv", func(ctx *Context) Result {
		if len(ctx.Args) == 0 {
			// No arguments - return all script args
			ctx.SetResult(scriptArgs)
			return BoolStatus(true)
		}
		
		// Check if first argument is a list (ParenGroup or string)
		firstArg := ctx.Args[0]
		var sourceList []interface{}
		var isListProvided bool
		
		if parenGroup, ok := firstArg.(ParenGroup); ok {
			// Parse the parenthetic group as a list
			sourceList = parseArguments(string(parenGroup))
			isListProvided = true
		} else if str, ok := firstArg.(string); ok {
			// Check if it looks like a comma-separated list
			// Only treat as list if it contains a comma or if we have 2 args
			if len(ctx.Args) > 1 || strings.Contains(str, ",") {
				sourceList = parseArguments(str)
				isListProvided = true
			}
		}
		
		if isListProvided {
			// First arg is a list
			if len(ctx.Args) == 1 {
				// No index - return the whole list
				ctx.SetResult(sourceList)
				return BoolStatus(true)
			}
			
			// Index provided as second argument
			index, ok := ctx.Args[1].(int64)
			if !ok {
				// Try to convert from float
				if f, ok := ctx.Args[1].(float64); ok {
					index = int64(f)
				} else {
					fmt.Fprintln(os.Stderr, "[ARGV ERROR] Index must be a number")
					ctx.SetResult(nil)
					return BoolStatus(false)
				}
			}
			
			// 1-indexed (like $1, $2, etc.)
			index-- // Convert to 0-based
			if index >= 0 && int(index) < len(sourceList) {
				ctx.SetResult(sourceList[index])
			} else {
				ctx.SetResult(nil)
			}
			return BoolStatus(true)
		}
		
		// First arg is not a list - treat as index into script args
		index, ok := firstArg.(int64)
		if !ok {
			// Try to convert from float
			if f, ok := firstArg.(float64); ok {
				index = int64(f)
			} else {
				// Not a number, not a list - just return it
				ctx.SetResult(firstArg)
				return BoolStatus(true)
			}
		}
		
		// Index into script args (1-indexed)
		index-- // Convert to 0-based
		if index >= 0 && int(index) < len(scriptArgs) {
			ctx.SetResult(scriptArgs[index])
		} else {
			ctx.SetResult(nil)
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
			fmt.Fprintln(os.Stderr, "[WHILE ERROR] Usage: while (condition), (body)")
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
	
	// get_inferred_type - returns the type of a value
	ps.RegisterCommand("get_inferred_type", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			// No argument provided - return "undefined"
			ctx.SetResult("undefined")
			return BoolStatus(true)
		}
		
		value := ctx.Args[0]
		typeName := getTypeName(value)
		ctx.SetResult(typeName)
		return BoolStatus(true)
	})
	
	// get_type - returns the type of a variable without fetching its value
	// More efficient than get_inferred_type {get x} for large values or frequent checks
	ps.RegisterCommand("get_type", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			fmt.Fprintln(os.Stderr, "[GET_TYPE ERROR] Usage: get_type <variable_name>")
			ctx.SetResult("undefined")
			return BoolStatus(false)
		}
		
		varName := fmt.Sprintf("%v", ctx.Args[0])
		value, exists := ctx.state.GetVariable(varName)
		
		if !exists {
			ctx.SetResult("undefined")
			return BoolStatus(true)
		}
		
		typeName := getTypeName(value)
		ctx.SetResult(typeName)
		return BoolStatus(true)
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
	case Symbol:
		// Try to parse symbol as number
		str := string(v)
		// Try to parse as float
		if f, err := strconv.ParseFloat(str, 64); err == nil {
			return f, true
		}
		// Try to parse as int
		if i, err := strconv.ParseInt(str, 10, 64); err == nil {
			return float64(i), true
		}
		return 0, false
	case QuotedString:
		// QuotedString behaves like string for parsing
		str := string(v)
		if f, err := strconv.ParseFloat(str, 64); err == nil {
			return f, true
		}
		if i, err := strconv.ParseInt(str, 10, 64); err == nil {
			return float64(i), true
		}
		return 0, false
	case ParenGroup:
		// ParenGroup (code block) is not a number
		return 0, false
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
	case Symbol:
		// Symbols are like non-empty strings - truthy
		// Symbol("false") shouldn't exist (parser converts to bool)
		// So any symbol is truthy if non-empty
		return string(v) != ""
	case QuotedString:
		// QuotedString behaves like string for truthiness
		// Empty string, "false", "0" are false
		lowerVal := strings.ToLower(strings.TrimSpace(string(v)))
		return lowerVal != "" && 
			   lowerVal != "false" && 
			   lowerVal != "0"
	case ParenGroup:
		// ParenGroup (code block) is truthy if non-empty
		return string(v) != ""
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

// Helper function to get the type name of a value
func getTypeName(val interface{}) string {
	if val == nil {
		return "nil"
	}
	
	switch v := val.(type) {
	case ParenGroup:
		return "block"
	case QuotedString:
		// QuotedString is still a string type, just with different formatting
		return "string"
	case Symbol:
		// Bare identifier (unquoted, non-keyword)
		return "symbol"
	case bool:
		return "bool"
	case int64:
		return "int"
	case int:
		return "int"
	case float64:
		return "float"
	case float32:
		return "float"
	case string:
		return "string"
	case TokenResult:
		return "token"
	case BoolStatus:
		// This would be unusual as an argument, but handle it
		return "bool"
	default:
		// Unknown type - return the Go type name as a fallback
		return fmt.Sprintf("unknown(%T)", v)
	}
}
