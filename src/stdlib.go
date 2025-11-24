package pawscript

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// formatListForDisplay formats a StoredList as a ParenGroup-like representation
func formatListForDisplay(list StoredList) string {
	var parts []string
	
	// First, add named arguments (key: value pairs)
	namedArgs := list.NamedArgs()
	if len(namedArgs) > 0 {
		// Get keys in sorted order for consistent output
		keys := make([]string, 0, len(namedArgs))
		for k := range namedArgs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		
		for _, key := range keys {
			value := namedArgs[key]
			var valueStr string
			switch v := value.(type) {
			case StoredList:
				valueStr = formatListForDisplay(v)
			case ParenGroup:
				valueStr = "(" + string(v) + ")"
			case QuotedString:
				escaped := strings.ReplaceAll(string(v), "\\", "\\\\")
				escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
				valueStr = "\"" + escaped + "\""
			case Symbol:
				valueStr = string(v)
			case string:
				escaped := strings.ReplaceAll(v, "\\", "\\\\")
				escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
				valueStr = "\"" + escaped + "\""
			case int64, float64, bool:
				valueStr = fmt.Sprintf("%v", v)
			case nil:
				valueStr = "nil"
			default:
				valueStr = fmt.Sprintf("%v", v)
			}
			
			// Format as "key: value"
			parts = append(parts, key + ": " + valueStr)
		}
	}
	
	// Then, add positional items
	items := list.Items()
	for _, item := range items {
		switch v := item.(type) {
		case StoredList:
			// Recursively format nested lists
			parts = append(parts, formatListForDisplay(v))
		case ParenGroup:
			parts = append(parts, "(" + string(v) + ")")
		case QuotedString:
			// Escape internal quotes
			escaped := strings.ReplaceAll(string(v), "\\", "\\\\")
			escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
			parts = append(parts, "\"" + escaped + "\"")
		case Symbol:
			parts = append(parts, string(v))
		case string:
			// Regular strings get quoted
			escaped := strings.ReplaceAll(v, "\\", "\\\\")
			escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
			parts = append(parts, "\"" + escaped + "\"")
		case int64, float64, bool:
			parts = append(parts, fmt.Sprintf("%v", v))
		case nil:
			parts = append(parts, "nil")
		default:
			parts = append(parts, fmt.Sprintf("%v", v))
		}
	}
	
	if len(parts) == 0 {
		return "()"
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

// resolveToString resolves an argument to a string, handling markers
func resolveToString(arg interface{}, executor *Executor) string {
	if executor == nil {
		return fmt.Sprintf("%v", arg)
	}
	
	// Resolve any markers
	resolved := executor.resolveValue(arg)
	return fmt.Sprintf("%v", resolved)
}

// RegisterStandardLibrary registers standard library commands
func (ps *PawScript) RegisterStandardLibrary(scriptArgs []string) {
	// Helper function to set a StoredList as result with proper reference counting
	setListResult := func(ctx *Context, list StoredList) {
		// Store it in the executor's object store (starts with refcount=0)
		id := ctx.executor.storeObject(list, "list")
		
		// Set marker as result WITHOUT claiming
		// Consumer will claim when they use it (SetVariable, etc.)
		marker := fmt.Sprintf("\x00LIST:%d\x00", id)
		ctx.state.SetResultWithoutClaim(Symbol(marker))
	}
	
	// argc - returns number of arguments
	// Usage: argc           - returns count of script arguments
	//        argc (a, b, c) - returns count of items in list (3)
	//        argc ~list     - returns count of items in StoredList
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
			args, _ := parseArguments(string(parenGroup))
			ctx.SetResult(len(args))
			return BoolStatus(true)
		}

		// If it's a string that looks like a list, parse it
		if str, ok := listArg.(string); ok {
			args, _ := parseArguments(str)
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
	//        argv ~list, 2     - returns second item from StoredList (1-indexed)
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
			sourceList, _ = parseArguments(string(parenGroup))
			isListProvided = true
		} else if str, ok := firstArg.(string); ok {
			// Check if it looks like a comma-separated list
			// Only treat as list if it contains a comma or if we have 2 args
			if len(ctx.Args) > 1 || strings.Contains(str, ",") {
				sourceList, _ = parseArguments(str)
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
						ctx.LogError(CatCommand, "Index to argv must be a number")
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

	// log_print - output log messages from scripts
	// Usage: log_print "level", "message"              - logs message at level
	//        log_print "level", "message", "category"  - logs message at level with category
	// Levels: debug, warn, error
	// Returns: true for debug/warn, false for error
	ps.RegisterCommand("log_print", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatIO, "Usage: log_print <level>, <message>, [category]")
			return BoolStatus(false)
		}

		// First argument is level
		levelStr := strings.ToLower(fmt.Sprintf("%v", ctx.Args[0]))
		var level LogLevel
		switch levelStr {
		case "debug":
			level = LevelDebug
		case "warn", "warning":
			level = LevelWarn
		case "error":
			level = LevelError
		default:
			ctx.LogError(CatIO, fmt.Sprintf("Invalid log level: %s (use debug, warn, or error)", levelStr))
			return BoolStatus(false)
		}

		// Second argument is message
		message := fmt.Sprintf("%v", ctx.Args[1])

		// Third argument is category (optional, default "user")
		category := CatUser
		if len(ctx.Args) > 2 {
			category = LogCategory(fmt.Sprintf("%v", ctx.Args[2]))
		}

		ctx.logger.Log(level, category, message, ctx.Position, nil)

		// Return false for errors, true otherwise
		return BoolStatus(level != LevelError)
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

	// msleep - sleep for specified milliseconds (async)
	ps.RegisterCommand("msleep", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: msleep <milliseconds>")
			return BoolStatus(false)
		}

		// Parse milliseconds argument
		var ms int64
		switch v := ctx.Args[0].(type) {
		case int:
			ms = int64(v)
		case int64:
			ms = v
		case float64:
			ms = int64(v)
		case string:
			parsed, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				ps.logger.Error("msleep: invalid milliseconds value: %v", v)
				return BoolStatus(false)
			}
			ms = parsed
		default:
			ps.logger.Error("msleep: milliseconds must be a number, got %T", v)
			return BoolStatus(false)
		}

		if ms < 0 {
			ps.logger.Error("msleep: milliseconds cannot be negative")
			return BoolStatus(false)
		}

		token := ctx.RequestToken(nil)

		go func() {
			time.Sleep(time.Duration(ms) * time.Millisecond)
			ctx.ResumeToken(token, true)
		}()

		return TokenResult(token)
	})

	// exec - execute external command and capture output
	ps.RegisterCommand("exec", func(ctx *Context) Result {
		if len(ctx.Args) == 0 {
			ctx.LogError(CatIO, "No command specified for exec.")
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
		if ctx.executor != nil {
			result := ctx.executor.maybeStoreValue(stdout, ctx.state)
			ctx.state.SetResultWithoutClaim(result)
		} else {
			ctx.state.SetResultWithoutClaim(stdout)
		}

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
	// Usage: ret              - leave status and result unchanged
	//        ret <value>      - leave status unchanged, set result
	ps.RegisterCommand("ret", func(ctx *Context) Result {
		switch len(ctx.Args) {
		case 0:
			// No arguments - leave everything as-is, preserve last status
			return EarlyReturn{
				Status:    BoolStatus(ctx.state.GetLastStatus()),
				Result:    ctx.GetResult(),
				HasResult: ctx.HasResult(),
			}

		case 1:
			// One argument - leave status unchanged, set result
			return EarlyReturn{
				Status:    BoolStatus(ctx.state.GetLastStatus()),
				Result:    ctx.Args[0],
				HasResult: true,
			}

		default:
			// Too many arguments - error
			ctx.LogError(CatCommand, "Usage: ret [value]")
			return BoolStatus(false)
		}
	})


	// while - loop while condition is true
	// Fully supports async operations in loop body via synchronous blocking
	ps.RegisterCommand("while", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: while (condition), (body)")
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
				ctx.LogError(CatFlow, "Async operations not supported in while condition")
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

			// If body returns a token (async), block waiting for it to complete
			if bodyToken, isToken := bodyResult.(TokenResult); isToken {
				tokenID := string(bodyToken)

				// Create a channel to wait on
				waitChan := make(chan ResumeData, 1)

				// Attach the wait channel to the token
				ctx.executor.attachWaitChan(tokenID, waitChan)

				// Block waiting for the async operation to complete
				resumeData := <-waitChan

				// Check if the operation succeeded
				if !resumeData.Status {
					ctx.LogError(CatFlow, "Async operation in while loop failed")
					return BoolStatus(false)
				}

				// Continue to next iteration - all local state is preserved!
				iterations++
				continue
			}

			// Result is whatever the body set (don't overwrite it)
			iterations++
		}

		if iterations >= maxIterations {
			ctx.LogError(CatFlow, "Maximum iterations (10000) exceeded")
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	// Arithmetic operations
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

	// Comparison operations
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
	// More efficient than get_inferred_type ~x for large values or frequent checks
	ps.RegisterCommand("get_type", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: get_type <variable_name>")
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
	//        list ~x, ~y               - computed values work (braces evaluate first)
	//        list (a), (b), (c)        - blocks as items
	//        list {list a, b}, {list c, d} - nested lists
	//        list                      - creates empty list
	ps.RegisterCommand("list", func(ctx *Context) Result {
		// Create and store the list with proper reference counting for nested objects
		// Include both positional arguments and named arguments
		setListResult(ctx, NewStoredListWithRefs(ctx.Args, ctx.NamedArgs, ctx.executor))
		return BoolStatus(true)
	})

	// len - returns the length of a list, string, or channel
	// Usage: len ~mylist
	//        len "hello"
	//        len ~mychannel  - returns number of unread messages
	ps.RegisterCommand("len", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: len <list|string|channel>")
			ctx.SetResult(0)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		switch v := value.(type) {
		case StoredList:
			ctx.SetResult(v.Len())
			return BoolStatus(true)
		case *StoredChannel:
			// Return number of unread messages
			ctx.SetResult(ChannelLen(v))
			return BoolStatus(true)
		case string, QuotedString, Symbol:
			// Resolve in case it's a string marker
			resolved := ctx.executor.resolveValue(v)
			str := fmt.Sprintf("%v", resolved)
			ctx.SetResult(len(str))
			return BoolStatus(true)
		case ParenGroup:
			// Treat as a list by parsing
			items, _ := parseArguments(string(v))
			ctx.SetResult(len(items))
			return BoolStatus(true)
		default:
			ctx.LogError(CatType, fmt.Sprintf("Cannot get length of type %s\n", getTypeName(v)))
			ctx.SetResult(0)
			return BoolStatus(false)
		}
	})

	// slice - returns a slice of a list or string (end exclusive)
	// Usage: slice ~mylist, 0, 3    - items 0, 1, 2
	//        slice ~mylist, 1, -1   - from index 1 to end
	//        slice "hello", 0, 3          - "hel"
	ps.RegisterCommand("slice", func(ctx *Context) Result {
		if len(ctx.Args) < 3 {
			ctx.LogError(CatCommand, "Usage: slice <list|string>, <start>, <end>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		// Parse start index
		startNum, ok := toNumber(ctx.Args[1])
		if !ok {
			ctx.LogError(CatArgument, "Start index must be a number")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
		start := int(startNum)

		// Parse end index
		endNum, ok := toNumber(ctx.Args[2])
		if !ok {
			ctx.LogError(CatArgument, "End index must be a number")
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
			// Resolve in case it's a string marker
			resolved := ctx.executor.resolveValue(v)
			str := fmt.Sprintf("%v", resolved)
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
			result := str[start:end]
			if ctx.executor != nil {
				result := ctx.executor.maybeStoreValue(result, ctx.state)
				ctx.state.SetResultWithoutClaim(result)
			} else {
				ctx.state.SetResultWithoutClaim(result)
			}
			return BoolStatus(true)
		default:
			ctx.LogError(CatType, fmt.Sprintf("Cannot slice type %s\n", getTypeName(v)))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
	})

	// append - returns a new list with item appended
	// Usage: append ~mylist, newitem
	ps.RegisterCommand("append", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: append <list>, <item>")
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
			ctx.LogError(CatType, fmt.Sprintf("Cannot append to type %s\n", getTypeName(v)))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
	})

	// prepend - returns a new list with item prepended
	// Usage: prepend ~mylist, newitem
	ps.RegisterCommand("prepend", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: prepend <list>, <item>")
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
			ctx.LogError(CatType, fmt.Sprintf("Cannot prepend to type %s\n", getTypeName(v)))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
	})

	// compact - returns a new list with a fresh backing array
	// Usage: compact ~mylist
	// Use this to free memory after slicing a large list
	ps.RegisterCommand("compact", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: compact <list>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		switch v := value.(type) {
		case StoredList:
			setListResult(ctx, v.Compact())
			return BoolStatus(true)
		default:
			ctx.LogError(CatType, fmt.Sprintf("Cannot compact type %s\n", getTypeName(v)))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
	})

	// concat - polymorphic concatenation
	// Usage: concat "hello", " ", "world"            -> "hello world" (strings)
	//        concat ~list1, ~list2                   -> combined list (lists)
	//        concat ~list, "item1", "item2"          -> list with items appended
	//        concat ~list1, ~list2, "extra"          -> lists concatenated + item appended
	ps.RegisterCommand("concat", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: concat <value1>, <value2>, ...")
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
			// Resolve markers before converting to string
			resolved := ctx.executor.resolveValue(arg)
			result.WriteString(fmt.Sprintf("%v", resolved))
		}

		ctx.SetResult(result.String())
		return BoolStatus(true)
	})

	// keys - returns a list of all keys from a list's named arguments
	// Usage: keys ~mylist  -> list of keys as values
	ps.RegisterCommand("keys", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: keys <list>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		switch v := value.(type) {
		case StoredList:
			namedArgs := v.NamedArgs()
			if len(namedArgs) == 0 {
				// No named arguments, return empty list
				setListResult(ctx, NewStoredList([]interface{}{}))
				return BoolStatus(true)
			}

			// Extract keys and sort them for consistent ordering
			keys := make([]string, 0, len(namedArgs))
			for key := range namedArgs {
				keys = append(keys, key)
			}
			sort.Strings(keys)

			// Convert to []interface{} for the list
			items := make([]interface{}, len(keys))
			for i, key := range keys {
				items[i] = key
			}

			setListResult(ctx, NewStoredList(items))
			return BoolStatus(true)
		default:
			ctx.LogError(CatType, fmt.Sprintf("Cannot get keys from type %s", getTypeName(v)))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
	})

	// get_val - returns the value for a key from a list's named arguments
	// Usage: get_val ~mylist, "radius"  -> value of radius
	ps.RegisterCommand("get_val", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: get_val <list>, <key>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		listValue := ctx.Args[0]
		keyValue := ctx.Args[1]

		// Convert key to string
		var keyStr string
		switch k := keyValue.(type) {
		case string:
			keyStr = k
		case Symbol:
			keyStr = string(k)
		case QuotedString:
			keyStr = string(k)
		default:
			keyStr = fmt.Sprint(k)
		}

		switch v := listValue.(type) {
		case StoredList:
			namedArgs := v.NamedArgs()
			if namedArgs == nil {
				ctx.LogError(CatCommand, fmt.Sprintf("List has no named arguments"))
				ctx.SetResult(nil)
				return BoolStatus(false)
			}

			value, exists := namedArgs[keyStr]
			if !exists {
				ctx.LogError(CatCommand, fmt.Sprintf("Key '%s' not found in list", keyStr))
				ctx.SetResult(nil)
				return BoolStatus(false)
			}

			ctx.SetResult(value)
			return BoolStatus(true)
		default:
			ctx.LogError(CatType, fmt.Sprintf("Cannot get value from type %s", getTypeName(v)))
			ctx.SetResult(nil)
			return BoolStatus(false)
		}
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
			ctx.LogError(CatCommand, "Usage: split <string>, <delimiter>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		delimiter := resolveToString(ctx.Args[1], ctx.executor)

		parts := strings.Split(str, delimiter)
		items := make([]interface{}, len(parts))
		for i, part := range parts {
			items[i] = part
		}

		setListResult(ctx, NewStoredList(items))
		return BoolStatus(true)
	})

	// join - join list into string with delimiter
	// Usage: join ~mylist, ","  -> "a,b,c"
	// Inverse of split
	ps.RegisterCommand("join", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: join <list>, <delimiter>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		delimiter := resolveToString(ctx.Args[1], ctx.executor)

		// Handle StoredList
		if storedList, ok := ctx.Args[0].(StoredList); ok {
			items := storedList.Items()
			strItems := make([]string, len(items))
			for i, item := range items {
				// Resolve each item in case it's a marker
				resolved := ctx.executor.resolveValue(item)
				strItems[i] = fmt.Sprintf("%v", resolved)
			}
			result := strings.Join(strItems, delimiter)
			if ctx.executor != nil {
				result := ctx.executor.maybeStoreValue(result, ctx.state)
				ctx.state.SetResultWithoutClaim(result)
			} else {
				ctx.state.SetResultWithoutClaim(result)
			}
			return BoolStatus(true)
		}

		ctx.LogError(CatType, fmt.Sprintf("First argument must be a list, got %s\n", getTypeName(ctx.Args[0])))
		ctx.SetResult("")
		return BoolStatus(false)
	})

	// str_upper - convert string to uppercase
	// Usage: str_upper "hello"  -> "HELLO"
	ps.RegisterCommand("str_upper", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: str_upper <string>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		// Use SetResultWithoutClaim - consumer will claim
		result := strings.ToUpper(str)
		if ctx.executor != nil {
			result := ctx.executor.maybeStoreValue(result, ctx.state)
			ctx.state.SetResultWithoutClaim(result)
		} else {
			ctx.state.SetResultWithoutClaim(result)
		}
		return BoolStatus(true)
	})

	// str_lower - convert string to lowercase
	// Usage: str_lower "HELLO"  -> "hello"
	ps.RegisterCommand("str_lower", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: str_lower <string>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		// Use SetResultWithoutClaim - consumer will claim
		result := strings.ToLower(str)
		if ctx.executor != nil {
			result := ctx.executor.maybeStoreValue(result, ctx.state)
			ctx.state.SetResultWithoutClaim(result)
		} else {
			ctx.state.SetResultWithoutClaim(result)
		}
		return BoolStatus(true)
	})

	// trim - trim whitespace from both ends
	// Usage: trim "  hello  "  -> "hello"
	ps.RegisterCommand("trim", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: trim <string>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		result := strings.TrimSpace(str)
		if ctx.executor != nil {
			result := ctx.executor.maybeStoreValue(result, ctx.state)
			ctx.state.SetResultWithoutClaim(result)
		} else {
			ctx.state.SetResultWithoutClaim(result)
		}
		return BoolStatus(true)
	})

	// trim_start - trim whitespace from start
	// Usage: trim_start "  hello  "  -> "hello  "
	ps.RegisterCommand("trim_start", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: trim_start <string>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		result := strings.TrimLeft(str, " \t\n\r")
		if ctx.executor != nil {
			result := ctx.executor.maybeStoreValue(result, ctx.state)
			ctx.state.SetResultWithoutClaim(result)
		} else {
			ctx.state.SetResultWithoutClaim(result)
		}
		return BoolStatus(true)
	})

	// trim_end - trim whitespace from end
	// Usage: trim_end "  hello  "  -> "  hello"
	ps.RegisterCommand("trim_end", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: trim_end <string>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		result := strings.TrimRight(str, " \t\n\r")
		if ctx.executor != nil {
			result := ctx.executor.maybeStoreValue(result, ctx.state)
			ctx.state.SetResultWithoutClaim(result)
		} else {
			ctx.state.SetResultWithoutClaim(result)
		}
		return BoolStatus(true)
	})

	// contains - check if string contains substring
	// Usage: contains "hello world", "world"  -> true
	ps.RegisterCommand("contains", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: contains <string>, <substring>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		substr := resolveToString(ctx.Args[1], ctx.executor)

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
			ctx.LogError(CatCommand, "Usage: index <string>, <substring>")
			ctx.SetResult(int64(-1))
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		substr := resolveToString(ctx.Args[1], ctx.executor)

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
			ctx.LogError(CatCommand, "Usage: replace <string>, <old>, <new>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		old := resolveToString(ctx.Args[1], ctx.executor)
		new := resolveToString(ctx.Args[2], ctx.executor)

		result := strings.ReplaceAll(str, old, new)
		if ctx.executor != nil {
			result := ctx.executor.maybeStoreValue(result, ctx.state)
			ctx.state.SetResultWithoutClaim(result)
		} else {
			ctx.state.SetResultWithoutClaim(result)
		}
		return BoolStatus(true)
	})

	// starts_with - check if string starts with prefix
	// Usage: starts_with "hello world", "hello"  -> true
	ps.RegisterCommand("starts_with", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: starts_with <string>, <prefix>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		prefix := resolveToString(ctx.Args[1], ctx.executor)

		result := strings.HasPrefix(str, prefix)
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	// ends_with - check if string ends with suffix
	// Usage: ends_with "hello world", "world"  -> true
	ps.RegisterCommand("ends_with", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: ends_with <string>, <suffix>")
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		suffix := resolveToString(ctx.Args[1], ctx.executor)

		result := strings.HasSuffix(str, suffix)
		ctx.SetResult(result)
		return BoolStatus(result)
	})

	// str_repeat - repeat string n times
	// Usage: str_repeat "ab", 3  -> "ababab"
	ps.RegisterCommand("str_repeat", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: str_repeat <string>, <count>")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		str := resolveToString(ctx.Args[0], ctx.executor)
		count, ok := toNumber(ctx.Args[1])
		if !ok {
			ctx.LogError(CatArgument, "Count must be a number")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		result := strings.Repeat(str, int(count))
		if ctx.executor != nil {
			result := ctx.executor.maybeStoreValue(result, ctx.state)
			ctx.state.SetResultWithoutClaim(result)
		} else {
			ctx.state.SetResultWithoutClaim(result)
		}
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
		
		// Sort objects by ID for consistent, readable output
		for i := 0; i < len(objects)-1; i++ {
			for j := i + 1; j < len(objects); j++ {
				if objects[i].ID > objects[j].ID {
					objects[i], objects[j] = objects[j], objects[i]
				}
			}
		}
		
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
	case StoredMacro:
		return "macro"
	case StoredCommand:
		return "command"
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
