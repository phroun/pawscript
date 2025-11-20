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

// formatListForDisplay formats a StoredList as a ParenGroup-like representation
func formatListForDisplay(list StoredList) string {
	items := list.Items()
	if len(items) == 0 {
		return "()"
	}
	
	parts := make([]string, len(items))
	for i, item := range items {
		switch v := item.(type) {
		case StoredList:
			// Recursively format nested lists
			parts[i] = formatListForDisplay(v)
		case ParenGroup:
			parts[i] = "(" + string(v) + ")"
		case QuotedString:
			// Escape internal quotes
			escaped := strings.ReplaceAll(string(v), "\\", "\\\\")
			escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
			parts[i] = "\"" + escaped + "\""
		case Symbol:
			parts[i] = string(v)
		case string:
			// Regular strings get quoted
			escaped := strings.ReplaceAll(v, "\\", "\\\\")
			escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
			parts[i] = "\"" + escaped + "\""
		case int64, float64, bool:
			parts[i] = fmt.Sprintf("%v", v)
		case nil:
			parts[i] = "nil"
		default:
			parts[i] = fmt.Sprintf("%v", v)
		}
	}
	
	return "(" + strings.Join(parts, ", ") + ")"
}

// formatArgForDisplay formats any argument for display, handling StoredList specially
// Also resolves any object markers (LIST/STRING/BLOCK) before displaying
func formatArgForDisplay(arg interface{}, executor *Executor) string {
	// Resolve any markers first (LIST/STRING/BLOCK -> actual values)
	if executor != nil {
		arg = executor.resolveValue(arg)
	}
	
	// Now format the resolved value
	if list, ok := arg.(StoredList); ok {
		return formatListForDisplay(list)
	}
	return fmt.Sprintf("%v", arg)
}

// RegisterStandardLibrary registers standard library commands
func (ps *PawScript) RegisterStandardLibrary(scriptArgs []string) {
	// Helper function to set a StoredList as result with proper reference counting
	setListResult := func(ctx *Context, list StoredList) {
		// Store it in the executor's object store
		id := ctx.executor.storeObject(list, "list")
		
		// Claim ownership in this context
		ctx.state.ClaimObjectReference(id)
		
		// Set the marker as the result
		marker := fmt.Sprintf("\x00LIST:%d\x00", id)
		ctx.SetResult(Symbol(marker))
	}
	
	// argc - returns number of arguments
	// Usage: argc           - returns count of script arguments
	//        argc (a, b, c) - returns count of items in list (3)
	//        argc {get list} - returns count of items in StoredList
	ps.RegisterCommand("argc", func(ctx *Context) Result {
		if len(ctx.Args) == 0 {
			// No arguments - return script arg count
			ctx.SetResult(len(scriptArgs))
			return BoolStatus(true)
		}

		// Argument provided - parse it as a list
		listArg := ctx.Args[0]

		// If it's a StoredList, return its length
		if storedList, ok := listArg.(StoredList); ok {
			ctx.SetResult(storedList.Len())
			return BoolStatus(true)
		}

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
	//        argv {get list}, 2 - returns second item from StoredList (1-indexed)
	ps.RegisterCommand("argv", func(ctx *Context) Result {
		if len(ctx.Args) == 0 {
			// No arguments - return all script args
			ctx.SetResult(scriptArgs)
			return BoolStatus(true)
		}

		// Check if first argument is a list (StoredList, ParenGroup, or string)
		firstArg := ctx.Args[0]
		var sourceList []interface{}
		var isListProvided bool

		if storedList, ok := firstArg.(StoredList); ok {
			// StoredList - get items
			sourceList = storedList.Items()
			isListProvided = true
		} else if parenGroup, ok := firstArg.(ParenGroup); ok {
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
					// Try to convert from string/symbol using toNumber
					num, ok := toNumber(ctx.Args[1])
					if !ok {
						fmt.Fprintln(os.Stderr, "[ARGV ERROR] Index must be a number")
						ctx.SetResult(nil)
						return BoolStatus(false)
					}
					index = int64(num)
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
			text += formatArgForDisplay(arg, ctx.executor)
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
			text += formatArgForDisplay(arg, ctx.executor)
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

	// get_result - gets the current result value and keeps it as the result
	ps.RegisterCommand("get_result", func(ctx *Context) Result {
		if ctx.HasResult() {
			return BoolStatus(true)
		}
		// No result available
		return BoolStatus(false)
	})

	// ret - early return from block
	// Usage: ret                    - leave status and result unchanged
	//        ret <value>            - set status from truthiness, leave result unchanged
	//        ret <value>, <result>  - set status from truthiness, set result
	//        ret , <result>         - leave status unchanged, set result (first arg is nil/empty)
	ps.RegisterCommand("ret", func(ctx *Context) Result {
		switch len(ctx.Args) {
		case 0:
			// No arguments - leave everything as-is
			// Get current status from state (default to true if none)
			// We need to get the current status somehow...
			// For now, let's default to true
			return EarlyReturn{
				Status:    BoolStatus(true),
				Result:    ctx.GetResult(),
				HasResult: ctx.HasResult(),
			}

		case 1:
			// One argument - set status from truthiness, keep result
			status := toBool(ctx.Args[0])
			return EarlyReturn{
				Status:    BoolStatus(status),
				Result:    ctx.GetResult(),
				HasResult: ctx.HasResult(),
			}

		default:
			// Two or more arguments - check first for nil/empty
			firstArg := ctx.Args[0]
			secondArg := ctx.Args[1]

			// If first arg is nil, Symbol(""), or empty string, leave status unchanged
			if firstArg == nil || firstArg == "" {
				return EarlyReturn{
					Status:    BoolStatus(true), // Current status (defaulting to true)
					Result:    secondArg,
					HasResult: true,
				}
			}

			// Check if it's an empty symbol
			if sym, ok := firstArg.(Symbol); ok && string(sym) == "" {
				return EarlyReturn{
					Status:    BoolStatus(true), // Current status (defaulting to true)
					Result:    secondArg,
					HasResult: true,
				}
			}

			// Normal case - set status from first arg, result from second
			status := toBool(firstArg)
			return EarlyReturn{
				Status:    BoolStatus(status),
				Result:    secondArg,
				HasResult: true,
			}
		}
	})

	// set - sets a variable in current scope
	// Usage: set varname, value                    - simple assignment
	//        set (x, y, z), {list 1, 2, 3}         - unpack list into variables
	//        set (a, b), (10, 20)                  - unpack ParenGroup into variables
	//        set {get targets}, {get values}       - dynamic unpacking
	ps.RegisterCommand("set", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[SET ERROR] Usage: set <n> <value>")
			return BoolStatus(false)
		}

		firstArg := ctx.Args[0]
		secondArg := ctx.Args[1]

		// Check if first arg is a list or ParenGroup (for unpacking)
		var varNames []interface{}
		isUnpacking := false

		if storedList, ok := firstArg.(StoredList); ok {
			// First arg is a list - unpack mode
			varNames = storedList.Items()
			isUnpacking = true
		} else if parenGroup, ok := firstArg.(ParenGroup); ok {
			// First arg is a ParenGroup - parse it as comma-separated list
			varNames = parseArguments(string(parenGroup))
			isUnpacking = true
		}

		if isUnpacking {
			// Unpacking mode - extract values from second arg
			var values []interface{}

			if storedList, ok := secondArg.(StoredList); ok {
				values = storedList.Items()
			} else if parenGroup, ok := secondArg.(ParenGroup); ok {
				values = parseArguments(string(parenGroup))
			} else if str, ok := secondArg.(string); ok {
				// Try parsing as comma-separated values
				values = parseArguments(str)
			} else {
				// Single value - wrap in slice
				values = []interface{}{secondArg}
			}

			// Set each variable to its corresponding value
			for i, varNameInterface := range varNames {
				varName := fmt.Sprintf("%v", varNameInterface)

				if i < len(values) {
					ctx.state.SetVariable(varName, values[i])
				} else {
					// Not enough values - set to nil
					ctx.state.SetVariable(varName, nil)
				}
			}

			return BoolStatus(true)
		}

		// Normal mode - simple assignment
		varName := fmt.Sprintf("%v", firstArg)
		ctx.state.SetVariable(varName, secondArg)
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

			// Check if condition returned early
			if earlyReturn, ok := condResult.(EarlyReturn); ok {
				// Set the result if provided
				if earlyReturn.HasResult {
					ctx.SetResult(earlyReturn.Result)
				}
				// Return the status from the early return
				return earlyReturn.Status
			}

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

			// If body returns early, break out of loop
			if earlyReturn, ok := bodyResult.(EarlyReturn); ok {
				// Set the result if provided
				if earlyReturn.HasResult {
					ctx.SetResult(earlyReturn.Result)
				}
				// Return the status from the early return
				return earlyReturn.Status
			}

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

	// list - creates an immutable list from arguments
	// Usage: list item1, item2, item3  - creates list from individual arguments
	//        list {get x}, {get y}     - computed values work (braces evaluate first)
	//        list (a), (b), (c)        - blocks as items
	//        list {list a, b}, {list c, d} - nested lists
	//        list                      - creates empty list
	ps.RegisterCommand("list", func(ctx *Context) Result {
		// Create and store the list with proper reference counting for nested objects
		setListResult(ctx, NewStoredListWithRefs(ctx.Args, ctx.executor))
		return BoolStatus(true)
	})

	// len - returns the length of a list or string
	// Usage: len {get mylist}
	//        len "hello"
	ps.RegisterCommand("len", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			fmt.Fprintln(os.Stderr, "[LEN ERROR] Usage: len <list|string>")
			ctx.SetResult(0)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		switch v := value.(type) {
		case StoredList:
			ctx.SetResult(v.Len())
			return BoolStatus(true)
		case string, QuotedString, Symbol:
			str := fmt.Sprintf("%v", v)
			ctx.SetResult(len(str))
			return BoolStatus(true)
		case ParenGroup:
			// Treat as a list by parsing
			items := parseArguments(string(v))
			ctx.SetResult(len(items))
			return BoolStatus(true)
		default:
			fmt.Fprintf(os.Stderr, "[LEN ERROR] Cannot get length of type %s\n", getTypeName(v))
			ctx.SetResult(0)
			return BoolStatus(false)
		}
	})

	// slice - returns a slice of a list or string (end exclusive)
	// Usage: slice {get mylist}, 0, 3    - items 0, 1, 2
	//        slice {get mylist}, 1, -1   - from index 1 to end
	//        slice "hello", 0, 3          - "hel"
	ps.RegisterCommand("slice", func(ctx *Context) Result {
		if len(ctx.Args) < 3 {
			fmt.Fprintln(os.Stderr, "[SLICE ERROR] Usage: slice <list|string>, <start>, <end>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		// Parse start index
		startNum, ok := toNumber(ctx.Args[1])
		if !ok {
			fmt.Fprintln(os.Stderr, "[SLICE ERROR] Start index must be a number")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
		start := int(startNum)

		// Parse end index
		endNum, ok := toNumber(ctx.Args[2])
		if !ok {
			fmt.Fprintln(os.Stderr, "[SLICE ERROR] End index must be a number")
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
			str := fmt.Sprintf("%v", v)
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
			ctx.SetResult(str[start:end])
			return BoolStatus(true)
		default:
			fmt.Fprintf(os.Stderr, "[SLICE ERROR] Cannot slice type %s\n", getTypeName(v))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
	})

	// append - returns a new list with item appended
	// Usage: append {get mylist}, newitem
	ps.RegisterCommand("append", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[APPEND ERROR] Usage: append <list>, <item>")
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
			fmt.Fprintf(os.Stderr, "[APPEND ERROR] Cannot append to type %s\n", getTypeName(v))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
	})

	// prepend - returns a new list with item prepended
	// Usage: prepend {get mylist}, newitem
	ps.RegisterCommand("prepend", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[PREPEND ERROR] Usage: prepend <list>, <item>")
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
			fmt.Fprintf(os.Stderr, "[PREPEND ERROR] Cannot prepend to type %s\n", getTypeName(v))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
	})

	// compact - returns a new list with a fresh backing array
	// Usage: compact {get mylist}
	// Use this to free memory after slicing a large list
	ps.RegisterCommand("compact", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			fmt.Fprintln(os.Stderr, "[COMPACT ERROR] Usage: compact <list>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		switch v := value.(type) {
		case StoredList:
			setListResult(ctx, v.Compact())
			return BoolStatus(true)
		default:
			fmt.Fprintf(os.Stderr, "[COMPACT ERROR] Cannot compact type %s\n", getTypeName(v))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
	})

	// concat - polymorphic concatenation
	// Usage: concat "hello", " ", "world"            -> "hello world" (strings)
	//        concat {get list1}, {get list2}         -> combined list (lists)
	//        concat {get list}, "item1", "item2"     -> list with items appended
	//        concat {get list1}, {get list2}, "extra" -> lists concatenated + item appended
	ps.RegisterCommand("concat", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[CONCAT ERROR] Usage: concat <value1>, <value2>, ...")
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
			result.WriteString(fmt.Sprintf("%v", arg))
		}

		ctx.SetResult(result.String())
		return BoolStatus(true)
	})

	// ==========================================
	// STRING MANIPULATION FUNCTIONS
	// Following same semantics as list operations where possible
	// ==========================================

	// split - split string into list by delimiter
	// Usage: split "a,b,c", ","  -> list of ["a", "b", "c"]
	// Inverse of join
	ps.RegisterCommand("split", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[SPLIT ERROR] Usage: split <string>, <delimiter>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		str := fmt.Sprintf("%v", ctx.Args[0])
		delimiter := fmt.Sprintf("%v", ctx.Args[1])

		parts := strings.Split(str, delimiter)
		items := make([]interface{}, len(parts))
		for i, part := range parts {
			items[i] = part
		}

		setListResult(ctx, NewStoredList(items))
		return BoolStatus(true)
	})

	// join - join list into string with delimiter
	// Usage: join {get mylist}, ","  -> "a,b,c"
	// Inverse of split
	ps.RegisterCommand("join", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[JOIN ERROR] Usage: join <list>, <delimiter>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		delimiter := fmt.Sprintf("%v", ctx.Args[1])

		// Handle StoredList
		if storedList, ok := ctx.Args[0].(StoredList); ok {
			items := storedList.Items()
			strItems := make([]string, len(items))
			for i, item := range items {
				strItems[i] = fmt.Sprintf("%v", item)
			}
			ctx.SetResult(strings.Join(strItems, delimiter))
			return BoolStatus(true)
		}

		fmt.Fprintf(os.Stderr, "[STR_JOIN ERROR] First argument must be a list, got %s\n", getTypeName(ctx.Args[0]))
		ctx.SetResult("")
		return BoolStatus(false)
	})

	// str_upper - convert string to uppercase
	// Usage: str_upper "hello"  -> "HELLO"
	ps.RegisterCommand("str_upper", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			fmt.Fprintln(os.Stderr, "[STR_UPPER ERROR] Usage: str_upper <string>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := fmt.Sprintf("%v", ctx.Args[0])
		ctx.SetResult(strings.ToUpper(str))
		return BoolStatus(true)
	})

	// str_lower - convert string to lowercase
	// Usage: str_lower "HELLO"  -> "hello"
	ps.RegisterCommand("str_lower", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			fmt.Fprintln(os.Stderr, "[STR_LOWER ERROR] Usage: str_lower <string>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := fmt.Sprintf("%v", ctx.Args[0])
		ctx.SetResult(strings.ToLower(str))
		return BoolStatus(true)
	})

	// trim - trim whitespace from both ends
	// Usage: trim "  hello  "  -> "hello"
	ps.RegisterCommand("trim", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			fmt.Fprintln(os.Stderr, "[TRIM ERROR] Usage: trim <string>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := fmt.Sprintf("%v", ctx.Args[0])
		ctx.SetResult(strings.TrimSpace(str))
		return BoolStatus(true)
	})

	// trim_start - trim whitespace from start
	// Usage: trim_start "  hello  "  -> "hello  "
	ps.RegisterCommand("trim_start", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			fmt.Fprintln(os.Stderr, "[TRIM_START ERROR] Usage: trim_start <string>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := fmt.Sprintf("%v", ctx.Args[0])
		ctx.SetResult(strings.TrimLeft(str, " \t\n\r"))
		return BoolStatus(true)
	})

	// trim_end - trim whitespace from end
	// Usage: trim_end "  hello  "  -> "  hello"
	ps.RegisterCommand("trim_end", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			fmt.Fprintln(os.Stderr, "[TRIM_END ERROR] Usage: trim_end <string>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := fmt.Sprintf("%v", ctx.Args[0])
		ctx.SetResult(strings.TrimRight(str, " \t\n\r"))
		return BoolStatus(true)
	})

	// contains - check if string contains substring
	// Usage: contains "hello world", "world"  -> true
	ps.RegisterCommand("contains", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[CONTAINS ERROR] Usage: contains <string>, <substring>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		str := fmt.Sprintf("%v", ctx.Args[0])
		substr := fmt.Sprintf("%v", ctx.Args[1])

		result := strings.Contains(str, substr)
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	// index - find first index of substring (-1 if not found)
	// Usage: index "hello world", "world"  -> 6
	// Returns -1 if not found (like many languages)
	// Always succeeds and sets result (use result to check if found)
	ps.RegisterCommand("index", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[INDEX ERROR] Usage: index <string>, <substring>")
			ctx.SetResult(int64(-1))
			return BoolStatus(false)
		}

		str := fmt.Sprintf("%v", ctx.Args[0])
		substr := fmt.Sprintf("%v", ctx.Args[1])

		index := strings.Index(str, substr)
		ctx.SetResult(int64(index))
		// Always return success - caller checks result value
		return BoolStatus(true)
	})

	// replace - replace all occurrences of substring
	// Usage: replace "hello world", "world", "gopher"  -> "hello gopher"
	// Replaces ALL occurrences (like strings.ReplaceAll)
	ps.RegisterCommand("replace", func(ctx *Context) Result {
		if len(ctx.Args) < 3 {
			fmt.Fprintln(os.Stderr, "[REPLACE ERROR] Usage: replace <string>, <old>, <new>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := fmt.Sprintf("%v", ctx.Args[0])
		old := fmt.Sprintf("%v", ctx.Args[1])
		new := fmt.Sprintf("%v", ctx.Args[2])

		result := strings.ReplaceAll(str, old, new)
		ctx.SetResult(result)
		return BoolStatus(true)
	})

	// starts_with - check if string starts with prefix
	// Usage: starts_with "hello world", "hello"  -> true
	ps.RegisterCommand("starts_with", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[STARTS_WITH ERROR] Usage: starts_with <string>, <prefix>")
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
	// Usage: ends_with "hello world", "world"  -> true
	ps.RegisterCommand("ends_with", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[ENDS_WITH ERROR] Usage: ends_with <string>, <suffix>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		str := fmt.Sprintf("%v", ctx.Args[0])
		suffix := fmt.Sprintf("%v", ctx.Args[1])

		result := strings.HasSuffix(str, suffix)
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	// str_repeat - repeat string n times
	// Usage: str_repeat "ab", 3  -> "ababab"
	ps.RegisterCommand("str_repeat", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			fmt.Fprintln(os.Stderr, "[STR_REPEAT ERROR] Usage: str_repeat <string>, <count>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := fmt.Sprintf("%v", ctx.Args[0])
		count, ok := toNumber(ctx.Args[1])
		if !ok {
			fmt.Fprintln(os.Stderr, "[STR_REPEAT ERROR] Count must be a number")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		result := strings.Repeat(str, int(count))
		ctx.SetResult(result)
		return BoolStatus(true)
	})

	// mem_stats - debug command to show stored objects
	ps.RegisterCommand("mem_stats", func(ctx *Context) Result {
		// Get a snapshot of stored objects without creating new ones
		type objectInfo struct {
			ID       int
			Type     string
			RefCount int
			Size     int
		}
		
		var objects []objectInfo
		totalSize := 0
		
		ctx.executor.mu.RLock()
		for id, obj := range ctx.executor.storedObjects {
			size := estimateObjectSize(obj.Value)
			objects = append(objects, objectInfo{
				ID:       id,
				Type:     obj.Type,
				RefCount: obj.RefCount,
				Size:     size,
			})
			totalSize += size
		}
		ctx.executor.mu.RUnlock()
		
		// Output using simple fmt to avoid creating new objects
		fmt.Println("=== Memory Statistics ===")
		fmt.Printf("Total stored objects: %d\n", len(objects))
		fmt.Printf("Total estimated size: %d bytes\n\n", totalSize)
		
		if len(objects) > 0 {
			fmt.Println("ID    Type      RefCount  Size(bytes)")
			fmt.Println("----  --------  --------  -----------")
			for _, obj := range objects {
				fmt.Printf("%-4d  %-8s  %-8d  %d\n", obj.ID, obj.Type, obj.RefCount, obj.Size)
			}
		}
		
		return BoolStatus(true)
	})
}

// estimateObjectSize provides a rough estimate of object size in bytes
func estimateObjectSize(value interface{}) int {
	switch v := value.(type) {
	case StoredList:
		// Base list overhead + items
		size := 24 // slice header
		for _, item := range v.Items() {
			size += estimateItemSize(item)
		}
		return size
	case StoredString:
		return len(string(v)) + 16
	case StoredBlock:
		return len(string(v)) + 16
	default:
		return estimateItemSize(value)
	}
}

// estimateItemSize estimates size of individual items
func estimateItemSize(value interface{}) int {
	switch v := value.(type) {
	case string, QuotedString, Symbol:
		return len(fmt.Sprintf("%v", v)) + 16 // string header overhead
	case ParenGroup:
		return len(string(v)) + 16
	case int64, float64, bool:
		return 8
	case nil:
		return 0
	default:
		// Unknown type, rough estimate
		return 32
	}
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
	case StoredList:
		return "list"
	case ParenGroup:
		return "block"
	case QuotedString:
		// QuotedString is still a string type, just with different formatting
		return "string"
	case Symbol:
		// Check if it's an object marker - if so, return the stored type
		if objType, objID := parseObjectMarker(string(v)); objID >= 0 {
			// Return the marker type directly (list, string, block)
			return objType
		}
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
		// Check if it's an object marker
		if objType, objID := parseObjectMarker(v); objID >= 0 {
			return objType
		}
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
