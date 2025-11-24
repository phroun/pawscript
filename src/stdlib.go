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
	// Helper to get a channel from first argument or default
	// Returns (channel, remaining args, found)
	getOutputChannel := func(ctx *Context, defaultName string) (*StoredChannel, []interface{}, bool) {
		args := ctx.Args
		var ch *StoredChannel

		// Check if first arg is a symbol that matches an object
		if len(args) > 0 {
			if sym, ok := args[0].(Symbol); ok {
				symStr := string(sym)
				if strings.HasPrefix(symStr, "#") {
					// Look up in ObjectsModule/ObjectsInherited
					if ctx.state.moduleEnv != nil {
						ctx.state.moduleEnv.mu.RLock()
						var obj interface{}
						found := false
						if ctx.state.moduleEnv.ObjectsModule != nil {
							if o, exists := ctx.state.moduleEnv.ObjectsModule[symStr]; exists {
								obj = o
								found = true
							}
						}
						if !found && ctx.state.moduleEnv.ObjectsInherited != nil {
							if o, exists := ctx.state.moduleEnv.ObjectsInherited[symStr]; exists {
								obj = o
								found = true
							}
						}
						ctx.state.moduleEnv.mu.RUnlock()

						if found {
							if channel, ok := obj.(*StoredChannel); ok {
								return channel, args[1:], true
							}
						}
					}
				}
			}
		}

		// Use default channel from io module
		if ctx.state.moduleEnv != nil {
			ctx.state.moduleEnv.mu.RLock()
			if ioSection, exists := ctx.state.moduleEnv.LibraryRestricted["io"]; exists {
				if item, exists := ioSection[defaultName]; exists {
					if channel, ok := item.Value.(*StoredChannel); ok {
						ch = channel
					}
				}
			}
			ctx.state.moduleEnv.mu.RUnlock()
		}

		return ch, args, ch != nil
	}

	getInputChannel := func(ctx *Context, defaultName string) (*StoredChannel, bool) {
		// Check if first arg is a symbol that matches an object
		if len(ctx.Args) > 0 {
			if sym, ok := ctx.Args[0].(Symbol); ok {
				symStr := string(sym)
				if strings.HasPrefix(symStr, "#") {
					// Look up in ObjectsModule/ObjectsInherited
					if ctx.state.moduleEnv != nil {
						ctx.state.moduleEnv.mu.RLock()
						var obj interface{}
						found := false
						if ctx.state.moduleEnv.ObjectsModule != nil {
							if o, exists := ctx.state.moduleEnv.ObjectsModule[symStr]; exists {
								obj = o
								found = true
							}
						}
						if !found && ctx.state.moduleEnv.ObjectsInherited != nil {
							if o, exists := ctx.state.moduleEnv.ObjectsInherited[symStr]; exists {
								obj = o
								found = true
							}
						}
						ctx.state.moduleEnv.mu.RUnlock()

						if found {
							if channel, ok := obj.(*StoredChannel); ok {
								return channel, true
							}
						}
					}
				}
			}
		}

		// Use default channel from io module
		var ch *StoredChannel
		if ctx.state.moduleEnv != nil {
			ctx.state.moduleEnv.mu.RLock()
			if ioSection, exists := ctx.state.moduleEnv.LibraryRestricted["io"]; exists {
				if item, exists := ioSection[defaultName]; exists {
					if channel, ok := item.Value.(*StoredChannel); ok {
						ch = channel
					}
				}
			}
			ctx.state.moduleEnv.mu.RUnlock()
		}

		return ch, ch != nil
	}

	// write - output without automatic newline
	// Usage: write [#channel] args...
	outputCommand := func(ctx *Context) Result {
		ch, args, found := getOutputChannel(ctx, "#out")
		if !found {
			// Fallback to direct stdout if no channel available
			text := ""
			for _, arg := range ctx.Args {
				text += formatArgForDisplay(arg, ctx.executor)
			}
			fmt.Print(text)
			return BoolStatus(true)
		}

		text := ""
		for _, arg := range args {
			text += formatArgForDisplay(arg, ctx.executor)
		}

		err := ChannelSend(ch, text)
		if err != nil {
			ctx.LogError(CatIO, fmt.Sprintf("Failed to write: %v", err))
			return BoolStatus(false)
		}
		return BoolStatus(true)
	}

	// echo/print - output with automatic newline and spaces between args
	// Usage: echo [#channel] args...
	outputLineCommand := func(ctx *Context) Result {
		ch, args, found := getOutputChannel(ctx, "#out")
		if !found {
			// Fallback to direct stdout if no channel available
			text := ""
			for i, arg := range ctx.Args {
				if i > 0 {
					text += " "
				}
				text += formatArgForDisplay(arg, ctx.executor)
			}
			fmt.Println(text)
			return BoolStatus(true)
		}

		text := ""
		for i, arg := range args {
			if i > 0 {
				text += " "
			}
			text += formatArgForDisplay(arg, ctx.executor)
		}

		// Add newline for echo/print (channel doesn't add it automatically)
		err := ChannelSend(ch, text+"\n")
		if err != nil {
			ctx.LogError(CatIO, fmt.Sprintf("Failed to write: %v", err))
			return BoolStatus(false)
		}
		return BoolStatus(true)
	}

	ps.RegisterCommand("write", outputCommand)
	ps.RegisterCommand("echo", outputLineCommand)
	ps.RegisterCommand("print", outputLineCommand)

	// read - read a line from stdin or specified channel
	// Usage: read [#channel]
	ps.RegisterCommand("read", func(ctx *Context) Result {
		ch, found := getInputChannel(ctx, "#in")
		if !found {
			// Fallback to direct stdin read
			token := ctx.RequestToken(nil)
			go func() {
				reader := bufio.NewReader(os.Stdin)
				line, err := reader.ReadString('\n')
				if err == nil {
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
		}

		// Use channel for reading
		_, value, err := ChannelRecv(ch)
		if err != nil {
			ctx.LogError(CatIO, fmt.Sprintf("Failed to read: %v", err))
			return BoolStatus(false)
		}
		ctx.SetResult(value)
		return BoolStatus(true)
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

	// ==========================================
	// MACRO AND FIBER COMMANDS
	// These commands enable macros, channels, and fibers
	// ==========================================

	// Define macro command
	ps.RegisterCommand("macro", func(ctx *Context) Result {
		ps.logger.Debug("macro command called with %d args", len(ctx.Args))

		// Capture the current module environment for lexical scoping
		macroEnv := NewMacroModuleEnvironment(ctx.state.moduleEnv)

		// Check for anonymous macro: macro (body)
		// If only 1 arg, create anonymous macro
		if len(ctx.Args) == 1 {
			// Anonymous macro: macro (body)
			commands := fmt.Sprintf("%v", ctx.Args[0])
			ps.logger.Debug("Creating anonymous macro with commands: %s", commands)

			// Create StoredMacro object with captured environment
			macro := NewStoredMacroWithEnv(commands, ctx.Position, macroEnv)

			// Store it in the executor's object store
			objectID := ctx.executor.storeObject(macro, "macro")

			// Create a marker for the macro
			macroMarker := fmt.Sprintf("\x00MACRO:%d\x00", objectID)

			// Set the marker as the result and claim a reference
			ctx.state.SetResult(Symbol(macroMarker))

			ps.logger.Debug("Created anonymous macro (object %d)", objectID)
			return BoolStatus(true)
		}

		// Named macro: macro name, (body)
		if len(ctx.Args) < 2 {
			ps.logger.Error("Usage: macro <name>, <commands> OR macro <commands>")
			return BoolStatus(false)
		}

		name := fmt.Sprintf("%v", ctx.Args[0])
		commands := fmt.Sprintf("%v", ctx.Args[1])

		ps.logger.Debug("Defining macro '%s' with commands: %s", name, commands)

		_, result := ps.macroSystem.DefineMacro(name, commands, ctx.Position, macroEnv)
		if !result {
			ps.logger.Error("Failed to define macro \"%s\"", name)
			return BoolStatus(false)
		}

		// For named macros, don't set a result - they're registered by name
		// (only anonymous macros return the macro object as a result)
		ps.logger.Debug("Successfully defined named macro '%s'", name)

		return BoolStatus(true)
	})

	// Call macro or command
	ps.RegisterCommand("call", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: call <macro_name_or_object>, [args...]")
			return BoolStatus(false)
		}

		callArgs := ctx.Args[1:]

		// Create a child state so the called macro/command has its own scope
		// but can access parent variables via get_parent/set_parent
		childState := ctx.state.CreateChild()

		firstArg := ctx.Args[0]

		// Check if the first argument is already a resolved StoredCommand object
		if cmd, ok := firstArg.(StoredCommand); ok {
			ps.logger.Debug("Calling resolved StoredCommand object: %s", cmd.CommandName)

			// Create a new context for the command with the child state
			cmdCtx := &Context{
				Args:      callArgs,
				NamedArgs: ctx.NamedArgs,
				Position:  ctx.Position,
				state:     childState,
				executor:  ctx.executor,
				logger:    ctx.logger,
			}

			// Execute the command handler
			result := cmd.Handler(cmdCtx)

			// Transfer result to parent state if one was set
			if childState.HasResult() {
				ctx.state.SetResult(childState.GetResult())
			}

			return result
		}

		// Check if the first argument is already a resolved StoredMacro object
		if macro, ok := firstArg.(StoredMacro); ok {
			ps.logger.Debug("Calling resolved StoredMacro object")

			// Execute the stored macro directly
			return ps.macroSystem.ExecuteStoredMacro(&macro, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
				filename := ""
				lineOffset := 0
				columnOffset := 0
				if substCtx != nil {
					filename = substCtx.Filename
					lineOffset = substCtx.CurrentLineOffset
					columnOffset = substCtx.CurrentColumnOffset
				}
				return ps.executor.ExecuteWithState(commands, macroExecState, substCtx, filename, lineOffset, columnOffset)
			}, callArgs, ctx.NamedArgs, childState, ctx.Position, ctx.state)
		}

		// Check if the first argument is a marker (Symbol)
		if sym, ok := firstArg.(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))

			// Handle StoredCommand marker
			if markerType == "command" && objectID >= 0 {
				ps.logger.Debug("Calling StoredCommand via marker (object %d)", objectID)

				// Retrieve the command object
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.Error("Command object %d not found", objectID)
					return BoolStatus(false)
				}

				cmd, ok := obj.(StoredCommand)
				if !ok {
					ps.logger.Error("Object %d is not a StoredCommand", objectID)
					return BoolStatus(false)
				}

				// Create a new context for the command with the child state
				cmdCtx := &Context{
					Args:      callArgs,
					NamedArgs: ctx.NamedArgs,
					Position:  ctx.Position,
					state:     childState,
					executor:  ctx.executor,
					logger:    ctx.logger,
				}

				// Execute the command handler
				result := cmd.Handler(cmdCtx)

				// Transfer result to parent state if one was set
				if childState.HasResult() {
					ctx.state.SetResult(childState.GetResult())
				}

				return result
			}

			// Handle StoredMacro marker
			if markerType == "macro" && objectID >= 0 {
				// It's a StoredMacro marker - retrieve and execute it
				ps.logger.Debug("Calling StoredMacro via marker (object %d)", objectID)

				// Retrieve the macro object
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.Error("Macro object %d not found", objectID)
					return BoolStatus(false)
				}

				macro, ok := obj.(StoredMacro)
				if !ok {
					ps.logger.Error("Object %d is not a StoredMacro", objectID)
					return BoolStatus(false)
				}

				// Execute the stored macro directly
				return ps.macroSystem.ExecuteStoredMacro(&macro, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
					filename := ""
					lineOffset := 0
					columnOffset := 0
					if substCtx != nil {
						filename = substCtx.Filename
						lineOffset = substCtx.CurrentLineOffset
						columnOffset = substCtx.CurrentColumnOffset
					}
					return ps.executor.ExecuteWithState(commands, macroExecState, substCtx, filename, lineOffset, columnOffset)
				}, callArgs, ctx.NamedArgs, childState, ctx.Position, ctx.state)
			}
		}

		// Otherwise, treat it as a macro name
		name := fmt.Sprintf("%v", firstArg)
		ps.logger.Debug("Calling macro by name: %s", name)

		return ps.macroSystem.ExecuteMacro(name, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
			// Use filename and offsets from substitution context for proper error reporting
			filename := ""
			lineOffset := 0
			columnOffset := 0
			if substCtx != nil {
				filename = substCtx.Filename
				lineOffset = substCtx.CurrentLineOffset
				columnOffset = substCtx.CurrentColumnOffset
			}
			return ps.executor.ExecuteWithState(commands, macroExecState, substCtx, filename, lineOffset, columnOffset)
		}, callArgs, ctx.NamedArgs, childState, ctx.Position, ctx.state) // Pass parent state
	})

	// List macros command
	ps.RegisterCommand("macro_list", func(ctx *Context) Result {
		macros := ps.macroSystem.ListMacros()
		ctx.SetResult(fmt.Sprintf("%v", macros))
		return BoolStatus(true)
	})

	// Delete macro command
	ps.RegisterCommand("macro_delete", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: macro_delete <macro_name>")
			return BoolStatus(false)
		}

		name := fmt.Sprintf("%v", ctx.Args[0])
		result := ps.macroSystem.DeleteMacro(name)

		if !result {
			ctx.LogError(CatMacro, fmt.Sprintf("PawScript macro \"%s\" not found or could not be deleted", name))
		}

		return BoolStatus(result)
	})

	// Command reference command - get a reference to a built-in or registered command
	ps.RegisterCommand("command_ref", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: command_ref <command_name>")
			return BoolStatus(false)
		}

		commandName := fmt.Sprintf("%v", ctx.Args[0])
		ps.logger.Debug("Getting command reference for: %s", commandName)

		// Get the command handler from the executor
		handler, exists := ctx.executor.GetCommand(commandName)
		if !exists {
			ps.logger.Error("Command \"%s\" not found", commandName)
			return BoolStatus(false)
		}

		// Create StoredCommand object
		cmd := NewStoredCommand(commandName, handler)

		// Store it in the executor's object store
		objectID := ctx.executor.storeObject(cmd, "command")

		// Create a marker for the command
		commandMarker := fmt.Sprintf("\x00COMMAND:%d\x00", objectID)

		// Set the marker as the result
		ctx.state.SetResult(Symbol(commandMarker))

		ps.logger.Debug("Created command reference for '%s' (object %d)", commandName, objectID)
		return BoolStatus(true)
	})

	// Channel command - create a native or custom channel
	ps.RegisterCommand("channel", func(ctx *Context) Result {
		// Parse arguments for buffer size and custom handlers
		bufferSize := 0
		var customSend, customRecv, customClose *StoredMacro

		// Check for buffer size as first positional argument
		if len(ctx.Args) > 0 {
			// Try to parse as int
			if size, ok := ctx.Args[0].(int); ok {
				bufferSize = size
			} else if sizeStr, ok := ctx.Args[0].(string); ok {
				fmt.Sscanf(sizeStr, "%d", &bufferSize)
			}
		}

		// Check for custom send/recv/close handlers in named args
		if sendVal, ok := ctx.NamedArgs["send"]; ok {
			if macro, ok := sendVal.(StoredMacro); ok {
				customSend = &macro
			}
		}
		if recvVal, ok := ctx.NamedArgs["recv"]; ok {
			if macro, ok := recvVal.(StoredMacro); ok {
				customRecv = &macro
			}
		}
		if closeVal, ok := ctx.NamedArgs["close"]; ok {
			if macro, ok := closeVal.(StoredMacro); ok {
				customClose = &macro
			}
		}

		// Create channel
		ch := NewStoredChannel(bufferSize)
		ch.CustomSend = customSend
		ch.CustomRecv = customRecv
		ch.CustomClose = customClose

		// Store in object store
		objectID := ctx.executor.storeObject(ch, "channel")
		channelMarker := fmt.Sprintf("\x00CHANNEL:%d\x00", objectID)
		ctx.state.SetResult(Symbol(channelMarker))

		ps.logger.Debug("Created channel (object %d) with buffer size %d", objectID, bufferSize)
		return BoolStatus(true)
	})

	ps.RegisterCommand("channel_subscribe", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: channel_subscribe <channel>")
			return BoolStatus(false)
		}

		// Get channel from first argument
		var ch *StoredChannel
		if channelObj, ok := ctx.Args[0].(*StoredChannel); ok {
			ch = channelObj
		} else if sym, ok := ctx.Args[0].(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "channel" && objectID >= 0 {
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.Error("Channel object %d not found", objectID)
					return BoolStatus(false)
				}
				if channelObj, ok := obj.(*StoredChannel); ok {
					ch = channelObj
				}
			}
		}

		if ch == nil {
			ps.logger.Error("First argument must be a channel")
			return BoolStatus(false)
		}

		// Subscribe to channel
		subscriber, err := ChannelSubscribe(ch)
		if err != nil {
			ps.logger.Error("Failed to subscribe: %v", err)
			return BoolStatus(false)
		}

		// Store subscriber in object store
		objectID := ctx.executor.storeObject(subscriber, "channel")
		subscriberMarker := fmt.Sprintf("\x00CHANNEL:%d\x00", objectID)
		ctx.state.SetResult(Symbol(subscriberMarker))

		ps.logger.Debug("Created subscriber %d for channel (object %d)", subscriber.SubscriberID, objectID)
		return BoolStatus(true)
	})

	ps.RegisterCommand("channel_send", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ps.logger.Error("Usage: channel_send <channel>, <value>")
			return BoolStatus(false)
		}

		// Get channel from first argument
		var ch *StoredChannel
		if channelObj, ok := ctx.Args[0].(*StoredChannel); ok {
			ch = channelObj
		} else if sym, ok := ctx.Args[0].(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "channel" && objectID >= 0 {
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.Error("Channel object %d not found", objectID)
					return BoolStatus(false)
				}
				if channelObj, ok := obj.(*StoredChannel); ok {
					ch = channelObj
				}
			}
		}

		if ch == nil {
			ps.logger.Error("First argument must be a channel")
			return BoolStatus(false)
		}

		// Send value to channel
		err := ChannelSend(ch, ctx.Args[1])
		if err != nil {
			ps.logger.Error("Failed to send: %v", err)
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	ps.RegisterCommand("channel_recv", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: channel_recv <channel>")
			return BoolStatus(false)
		}

		// Get channel from first argument
		var ch *StoredChannel
		if channelObj, ok := ctx.Args[0].(*StoredChannel); ok {
			ch = channelObj
		} else if sym, ok := ctx.Args[0].(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "channel" && objectID >= 0 {
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.Error("Channel object %d not found", objectID)
					return BoolStatus(false)
				}
				if channelObj, ok := obj.(*StoredChannel); ok {
					ch = channelObj
				}
			}
		}

		if ch == nil {
			ps.logger.Error("First argument must be a channel")
			return BoolStatus(false)
		}

		// Receive from channel
		senderID, value, err := ChannelRecv(ch)
		if err != nil {
			ps.logger.Error("Failed to receive: %v", err)
			return BoolStatus(false)
		}

		// Return tuple (sender_id, value) as a StoredList
		tuple := NewStoredList([]interface{}{senderID, value})
		tupleID := ctx.executor.storeObject(tuple, "list")
		tupleMarker := fmt.Sprintf("\x00LIST:%d\x00", tupleID)
		ctx.state.SetResult(Symbol(tupleMarker))

		return BoolStatus(true)
	})

	ps.RegisterCommand("channel_close", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: channel_close <channel>")
			return BoolStatus(false)
		}

		// Get channel from first argument
		var ch *StoredChannel
		if channelObj, ok := ctx.Args[0].(*StoredChannel); ok {
			ch = channelObj
		} else if sym, ok := ctx.Args[0].(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "channel" && objectID >= 0 {
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.Error("Channel object %d not found", objectID)
					return BoolStatus(false)
				}
				if channelObj, ok := obj.(*StoredChannel); ok {
					ch = channelObj
				}
			}
		}

		if ch == nil {
			ps.logger.Error("First argument must be a channel")
			return BoolStatus(false)
		}

		// Close channel
		err := ChannelClose(ch)
		if err != nil {
			ps.logger.Error("Failed to close: %v", err)
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	ps.RegisterCommand("channel_disconnect", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ps.logger.Error("Usage: channel_disconnect <channel>, <subscriber_id>")
			return BoolStatus(false)
		}

		// Get channel from first argument
		var ch *StoredChannel
		if channelObj, ok := ctx.Args[0].(*StoredChannel); ok {
			ch = channelObj
		} else if sym, ok := ctx.Args[0].(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "channel" && objectID >= 0 {
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.Error("Channel object %d not found", objectID)
					return BoolStatus(false)
				}
				if channelObj, ok := obj.(*StoredChannel); ok {
					ch = channelObj
				}
			}
		}

		if ch == nil {
			ps.logger.Error("First argument must be a channel")
			return BoolStatus(false)
		}

		// Parse subscriber ID
		subscriberID := 0
		if id, ok := ctx.Args[1].(int); ok {
			subscriberID = id
		} else if idStr, ok := ctx.Args[1].(string); ok {
			fmt.Sscanf(idStr, "%d", &subscriberID)
		}

		// Disconnect subscriber
		err := ChannelDisconnect(ch, subscriberID)
		if err != nil {
			ps.logger.Error("Failed to disconnect: %v", err)
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	ps.RegisterCommand("channel_opened", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: channel_opened <channel>")
			return BoolStatus(false)
		}

		// Get channel from first argument
		var ch *StoredChannel
		if channelObj, ok := ctx.Args[0].(*StoredChannel); ok {
			ch = channelObj
		} else if sym, ok := ctx.Args[0].(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "channel" && objectID >= 0 {
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.Error("Channel object %d not found", objectID)
					return BoolStatus(false)
				}
				if channelObj, ok := obj.(*StoredChannel); ok {
					ch = channelObj
				}
			}
		}

		if ch == nil {
			ps.logger.Error("First argument must be a channel")
			return BoolStatus(false)
		}

		// Check if opened
		opened := ChannelIsOpened(ch)
		ctx.state.SetResult(opened)

		return BoolStatus(true)
	})

	// Fiber commands
	ps.RegisterCommand("fiber_spawn", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: fiber_spawn <macro>, [args...]")
			return BoolStatus(false)
		}

		firstArg := ctx.Args[0]
		fiberArgs := ctx.Args[1:]
		namedArgs := ctx.NamedArgs

		// Get the macro to execute
		var macro *StoredMacro

		// Check if first argument is a resolved StoredMacro object
		if m, ok := firstArg.(StoredMacro); ok {
			macro = &m
		} else if sym, ok := firstArg.(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "macro" && objectID >= 0 {
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.Error("Macro object %d not found", objectID)
					return BoolStatus(false)
				}
				if m, ok := obj.(StoredMacro); ok {
					macro = &m
				}
			}
		}

		if macro == nil {
			ps.logger.Error("First argument must be a macro")
			return BoolStatus(false)
		}

		// Spawn the fiber - use macro's lexical environment if available, otherwise caller's
		parentModuleEnv := macro.ModuleEnv
		if parentModuleEnv == nil {
			// Inline macros may not have captured env, use caller's
			parentModuleEnv = ctx.state.moduleEnv
		}
		handle := ctx.executor.SpawnFiber(macro, ps.macroSystem, fiberArgs, namedArgs, parentModuleEnv)

		// Store the fiber handle as an object
		objectID := ctx.executor.storeObject(handle, "fiber")
		fiberMarker := fmt.Sprintf("\x00FIBER:%d\x00", objectID)
		ctx.state.SetResult(Symbol(fiberMarker))

		ps.logger.Debug("Spawned fiber %d (object %d)", handle.ID, objectID)
		return BoolStatus(true)
	})

	ps.RegisterCommand("fiber_wait", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: fiber_wait <fiber_handle>")
			return BoolStatus(false)
		}

		var handle *FiberHandle
		if h, ok := ctx.Args[0].(*FiberHandle); ok {
			handle = h
		} else if sym, ok := ctx.Args[0].(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "fiber" && objectID >= 0 {
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.Error("Fiber object %d not found", objectID)
					return BoolStatus(false)
				}
				if h, ok := obj.(*FiberHandle); ok {
					handle = h
				}
			}
		}

		if handle == nil {
			ps.logger.Error("First argument must be a fiber handle")
			return BoolStatus(false)
		}

		result, err := ctx.executor.WaitForFiber(handle)
		if err != nil {
			ps.logger.Error("Failed to wait for fiber: %v", err)
			return BoolStatus(false)
		}

		if result != nil {
			ctx.state.SetResult(result)
		}

		return BoolStatus(true)
	})

	ps.RegisterCommand("fiber_count", func(ctx *Context) Result {
		count := ctx.executor.GetFiberCount()
		ctx.state.SetResult(count)
		return BoolStatus(true)
	})

	ps.RegisterCommand("fiber_id", func(ctx *Context) Result {
		fiberID := ctx.state.fiberID
		ctx.state.SetResult(fiberID)
		return BoolStatus(true)
	})

	ps.RegisterCommand("fiber_wait_all", func(ctx *Context) Result {
		// Wait for all child fibers to complete
		ctx.executor.WaitForAllFibers()
		return BoolStatus(true)
	})

	// Clear all macros command
	ps.RegisterCommand("macro_clear", func(ctx *Context) Result {
		count := ps.macroSystem.ClearMacros()
		ctx.SetResult(fmt.Sprintf("Cleared %d PawScript macros", count))
		return BoolStatus(true)
	})

	// Populate module system with stdlib commands organized into modules
	ps.rootModuleEnv.PopulateStdlibModules()

	// Populate IO module with native stdin/stdout/stderr/stdio channels
	ps.rootModuleEnv.PopulateIOModule()
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
