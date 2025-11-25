package pawscript

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// RegisterSystemLib registers OS, IO, and system commands
// Modules: os, io, sys
func (ps *PawScript) RegisterSystemLib(scriptArgs []string) {
	// Helper function to set a StoredList as result with proper reference counting
	setListResult := func(ctx *Context, list StoredList) {
		id := ctx.executor.storeObject(list, "list")
		marker := fmt.Sprintf("\x00LIST:%d\x00", id)
		ctx.state.SetResultWithoutClaim(Symbol(marker))
	}

	// Helper to resolve a value to a StoredList (handles markers, direct objects, ParenGroups)
	// Returns the list and a boolean indicating if found
	valueToList := func(ctx *Context, val interface{}) (StoredList, bool) {
		switch v := val.(type) {
		case StoredList:
			return v, true
		case ParenGroup:
			items, _ := parseArguments(string(v))
			return NewStoredList(items), true
		case StoredBlock:
			items, _ := parseArguments(string(v))
			return NewStoredList(items), true
		case Symbol:
			markerType, objectID := parseObjectMarker(string(v))
			if markerType == "list" && objectID >= 0 {
				if obj, exists := ctx.executor.getObject(objectID); exists {
					if list, ok := obj.(StoredList); ok {
						return list, true
					}
				}
			}
		case string:
			markerType, objectID := parseObjectMarker(v)
			if markerType == "list" && objectID >= 0 {
				if obj, exists := ctx.executor.getObject(objectID); exists {
					if list, ok := obj.(StoredList); ok {
						return list, true
					}
				}
			}
		}
		return StoredList{}, false
	}

	// Helper to get a list from #-prefixed symbol (local vars -> ObjectsModule)
	resolveHashList := func(ctx *Context, name string) (StoredList, bool) {
		// First check local variables
		if localVal, exists := ctx.state.GetVariable(name); exists {
			if list, found := valueToList(ctx, localVal); found {
				return list, true
			}
		}
		// Then check ObjectsModule
		if ctx.state.moduleEnv != nil {
			ctx.state.moduleEnv.mu.RLock()
			defer ctx.state.moduleEnv.mu.RUnlock()
			if ctx.state.moduleEnv.ObjectsModule != nil {
				if obj, exists := ctx.state.moduleEnv.ObjectsModule[name]; exists {
					if list, found := valueToList(ctx, obj); found {
						return list, true
					}
				}
			}
		}
		return StoredList{}, false
	}

	// Helper to resolve a value to a channel (handles markers and direct objects)
	valueToChannel := func(ctx *Context, val interface{}) *StoredChannel {
		switch v := val.(type) {
		case *StoredChannel:
			return v
		case Symbol:
			markerType, objectID := parseObjectMarker(string(v))
			if markerType == "channel" && objectID >= 0 {
				if obj, exists := ctx.executor.getObject(objectID); exists {
					if ch, ok := obj.(*StoredChannel); ok {
						return ch
					}
				}
			}
		case string:
			markerType, objectID := parseObjectMarker(v)
			if markerType == "channel" && objectID >= 0 {
				if obj, exists := ctx.executor.getObject(objectID); exists {
					if ch, ok := obj.(*StoredChannel); ok {
						return ch
					}
				}
			}
		}
		return nil
	}

	// Helper to resolve a channel name (like "#out" or "#err") to a channel
	// Resolution order: local variables -> ObjectsModule -> ObjectsInherited
	resolveChannel := func(ctx *Context, channelName string) *StoredChannel {
		// First, check local macro variables
		if value, exists := ctx.state.GetVariable(channelName); exists {
			if ch := valueToChannel(ctx, value); ch != nil {
				return ch
			}
		}

		// Then, check ObjectsModule and ObjectsInherited
		if ctx.state.moduleEnv != nil {
			ctx.state.moduleEnv.mu.RLock()
			defer ctx.state.moduleEnv.mu.RUnlock()

			// Check ObjectsModule (copy-on-write layer)
			if ctx.state.moduleEnv.ObjectsModule != nil {
				if obj, exists := ctx.state.moduleEnv.ObjectsModule[channelName]; exists {
					if ch := valueToChannel(ctx, obj); ch != nil {
						return ch
					}
				}
			}

			// Check ObjectsInherited (root layer where io::#out etc. live)
			if ctx.state.moduleEnv.ObjectsInherited != nil {
				if obj, exists := ctx.state.moduleEnv.ObjectsInherited[channelName]; exists {
					if ch := valueToChannel(ctx, obj); ch != nil {
						return ch
					}
				}
			}
		}

		return nil
	}

	// Helper to get a channel from first argument or default
	getOutputChannel := func(ctx *Context, defaultName string) (*StoredChannel, []interface{}, bool) {
		args := ctx.Args

		// Check if first arg is already a channel (from tilde resolution)
		if len(args) > 0 {
			if ch, ok := args[0].(*StoredChannel); ok {
				return ch, args[1:], true
			}
			// Or if first arg is a symbol starting with #
			if sym, ok := args[0].(Symbol); ok {
				symStr := string(sym)
				if strings.HasPrefix(symStr, "#") {
					if ch := resolveChannel(ctx, symStr); ch != nil {
						return ch, args[1:], true
					}
				}
			}
		}

		// Use default channel (also resolved through local vars first)
		if ch := resolveChannel(ctx, defaultName); ch != nil {
			return ch, args, true
		}

		return nil, args, false
	}

	getInputChannel := func(ctx *Context, defaultName string) (*StoredChannel, bool) {
		// Check if first arg is already a channel (from tilde resolution)
		if len(ctx.Args) > 0 {
			if ch, ok := ctx.Args[0].(*StoredChannel); ok {
				return ch, true
			}
			// Or if first arg is a symbol starting with #
			if sym, ok := ctx.Args[0].(Symbol); ok {
				symStr := string(sym)
				if strings.HasPrefix(symStr, "#") {
					if ch := resolveChannel(ctx, symStr); ch != nil {
						return ch, true
					}
				}
			}
		}

		// Use default channel (also resolved through local vars first)
		if ch := resolveChannel(ctx, defaultName); ch != nil {
			return ch, true
		}

		return nil, false
	}

	// ==================== os:: module ====================

	// argc - returns number of arguments
	ps.RegisterCommand("argc", func(ctx *Context) Result {
		if len(ctx.Args) == 0 {
			// No arguments - use default #args
			sourceList, found := resolveHashList(ctx, "#args")
			if !found {
				ctx.LogError(CatVariable, "#args not found - no script arguments available")
				ctx.SetResult(0)
				return BoolStatus(false)
			}
			ctx.SetResult(sourceList.Len())
			return BoolStatus(true)
		}

		// Argument provided
		listArg := ctx.Args[0]

		// Check for #-prefixed symbol (auto-resolve)
		if sym, ok := listArg.(Symbol); ok {
			symStr := string(sym)
			if strings.HasPrefix(symStr, "#") {
				if sourceList, found := resolveHashList(ctx, symStr); found {
					ctx.SetResult(sourceList.Len())
					return BoolStatus(true)
				}
			}
		}

		// If it's a StoredList, return its length
		if storedList, ok := listArg.(StoredList); ok {
			ctx.SetResult(storedList.Len())
			return BoolStatus(true)
		}

		// Try to resolve as list marker
		if list, found := valueToList(ctx, listArg); found {
			ctx.SetResult(list.Len())
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
	ps.RegisterCommand("argv", func(ctx *Context) Result {
		var sourceList []interface{}
		var storedListSource StoredList
		var hasStoredList bool
		var isListProvided bool

		// Helper to get default #args list
		getDefaultArgs := func() (StoredList, bool) {
			list, found := resolveHashList(ctx, "#args")
			if !found {
				ctx.LogError(CatVariable, "#args not found - no script arguments available")
				return StoredList{}, false
			}
			return list, true
		}

		if len(ctx.Args) == 0 {
			// No arguments - return all items from #args
			list, ok := getDefaultArgs()
			if !ok {
				ctx.SetResult(nil)
				return BoolStatus(false)
			}
			setListResult(ctx, list)
			return BoolStatus(true)
		}

		// Check if first argument is a list source
		firstArg := ctx.Args[0]

		// Check for #-prefixed symbol (auto-resolve)
		if sym, ok := firstArg.(Symbol); ok {
			symStr := string(sym)
			if strings.HasPrefix(symStr, "#") {
				if list, found := resolveHashList(ctx, symStr); found {
					storedListSource = list
					sourceList = list.Items()
					hasStoredList = true
					isListProvided = true
				}
			}
		}

		if !isListProvided {
			if storedList, ok := firstArg.(StoredList); ok {
				sourceList = storedList.Items()
				storedListSource = storedList
				hasStoredList = true
				isListProvided = true
			} else if list, found := valueToList(ctx, firstArg); found {
				sourceList = list.Items()
				storedListSource = list
				hasStoredList = true
				isListProvided = true
			} else if parenGroup, ok := firstArg.(ParenGroup); ok {
				sourceList, _ = parseArguments(string(parenGroup))
				isListProvided = true
			} else if str, ok := firstArg.(string); ok {
				if len(ctx.Args) > 1 || strings.Contains(str, ",") {
					sourceList, _ = parseArguments(str)
					isListProvided = true
				}
			}
		}

		if isListProvided {
			if len(ctx.Args) == 1 {
				if hasStoredList {
					setListResult(ctx, storedListSource)
				} else {
					ctx.SetResult(sourceList)
				}
				return BoolStatus(true)
			}

			// Index provided as second argument
			index, ok := ctx.Args[1].(int64)
			if !ok {
				if f, ok := ctx.Args[1].(float64); ok {
					index = int64(f)
				} else {
					num, ok := toNumber(ctx.Args[1])
					if !ok {
						ctx.LogError(CatCommand, "Index to argv must be a number")
						ctx.SetResult(nil)
						return BoolStatus(false)
					}
					index = int64(num)
				}
			}

			// 1-indexed
			index--
			if index >= 0 && int(index) < len(sourceList) {
				ctx.SetResult(sourceList[index])
			} else {
				ctx.SetResult(nil)
			}
			return BoolStatus(true)
		}

		// First arg is not a list - treat as index into default #args
		index, ok := firstArg.(int64)
		if !ok {
			if f, ok := firstArg.(float64); ok {
				index = int64(f)
			} else {
				ctx.SetResult(firstArg)
				return BoolStatus(true)
			}
		}

		list, ok := getDefaultArgs()
		if !ok {
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		index--
		items := list.Items()
		if index >= 0 && int(index) < len(items) {
			ctx.SetResult(items[index])
		} else {
			ctx.SetResult(nil)
		}
		return BoolStatus(true)
	})

	// exec - execute external command and capture output
	ps.RegisterCommand("exec", func(ctx *Context) Result {
		if len(ctx.Args) == 0 {
			ctx.LogError(CatIO, "No command specified for exec.")
			return BoolStatus(false)
		}

		cmdName := fmt.Sprintf("%v", ctx.Args[0])

		var cmdArgs []string
		for i := 1; i < len(ctx.Args); i++ {
			cmdArgs = append(cmdArgs, fmt.Sprintf("%v", ctx.Args[i]))
		}

		cmd := exec.Command(cmdName, cmdArgs...)

		var stdoutBuf, stderrBuf bytes.Buffer
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf

		err := cmd.Run()

		stdout := stdoutBuf.String()
		stderr := stderrBuf.String()

		if stderr != "" {
			// Route stderr through channels
			outCtx := NewOutputContext(ctx.state, ctx.executor)
			_ = outCtx.WriteToErr(stderr)
		}

		hasStderrContent := strings.TrimSpace(stderr) != ""
		success := err == nil && !hasStderrContent

		if ctx.executor != nil {
			result := ctx.executor.maybeStoreValue(stdout, ctx.state)
			ctx.state.SetResultWithoutClaim(result)
		} else {
			ctx.state.SetResultWithoutClaim(stdout)
		}

		return BoolStatus(success)
	})

	// include - include another source file
	ps.RegisterCommand("include", func(ctx *Context) Result {
		if len(ctx.Args) == 0 {
			ctx.LogError(CatIO, "Usage: include \"filename\" or include (imports...), \"filename\"")
			return BoolStatus(false)
		}

		var filename string
		var importSpec []interface{}
		var importNamedSpec map[string]interface{}
		isAdvancedForm := false

		firstArg := ctx.Args[0]
		if ctx.executor != nil {
			firstArg = ctx.executor.resolveValue(firstArg)
		}

		switch v := firstArg.(type) {
		case ParenGroup:
			isAdvancedForm = true
			importSpec, importNamedSpec = parseArguments(string(v))
			if len(ctx.Args) < 2 {
				ctx.LogError(CatIO, "include: filename required after import specification")
				return BoolStatus(false)
			}
			filename = fmt.Sprintf("%v", ctx.Args[1])
		case StoredList:
			isAdvancedForm = true
			importSpec = v.Items()
			importNamedSpec = make(map[string]interface{})
			if len(ctx.Args) < 2 {
				ctx.LogError(CatIO, "include: filename required after import specification")
				return BoolStatus(false)
			}
			filename = fmt.Sprintf("%v", ctx.Args[1])
		default:
			filename = fmt.Sprintf("%v", ctx.Args[0])
		}

		// Remove quotes if present
		if strings.HasPrefix(filename, "\"") && strings.HasSuffix(filename, "\"") {
			filename = filename[1 : len(filename)-1]
		} else if strings.HasPrefix(filename, "'") && strings.HasSuffix(filename, "'") {
			filename = filename[1 : len(filename)-1]
		}

		content, err := os.ReadFile(filename)
		if err != nil {
			ctx.LogError(CatIO, fmt.Sprintf("include: failed to read file %s: %v", filename, err))
			return BoolStatus(false)
		}

		if isAdvancedForm {
			restrictedEnv := NewMacroModuleEnvironment(ctx.state.moduleEnv)

			execState := NewExecutionState()
			execState.moduleEnv = restrictedEnv
			execState.executor = ctx.executor
			defer execState.ReleaseAllReferences()

			result := ctx.executor.ExecuteWithState(string(content), execState, nil, filename, 0, 0)

			if boolStatus, ok := result.(BoolStatus); ok && !bool(boolStatus) {
				return BoolStatus(false)
			}

			ctx.state.moduleEnv.mu.Lock()
			defer ctx.state.moduleEnv.mu.Unlock()

			ctx.state.moduleEnv.CopyLibraryRestricted()

			for _, arg := range importSpec {
				moduleName := fmt.Sprintf("%v", arg)
				if section, exists := restrictedEnv.ModuleExports[moduleName]; exists {
					if ctx.state.moduleEnv.LibraryRestricted[moduleName] == nil {
						ctx.state.moduleEnv.LibraryRestricted[moduleName] = make(ModuleSection)
					}
					if ctx.state.moduleEnv.LibraryInherited[moduleName] == nil {
						ctx.state.moduleEnv.LibraryInherited[moduleName] = make(ModuleSection)
					}
					for itemName, item := range section {
						ctx.state.moduleEnv.LibraryRestricted[moduleName][itemName] = item
						ctx.state.moduleEnv.LibraryInherited[moduleName][itemName] = item
					}
				}
			}

			for targetName, sourceArg := range importNamedSpec {
				sourceName := fmt.Sprintf("%v", sourceArg)
				if section, exists := restrictedEnv.ModuleExports[sourceName]; exists {
					if ctx.state.moduleEnv.LibraryRestricted[targetName] == nil {
						ctx.state.moduleEnv.LibraryRestricted[targetName] = make(ModuleSection)
					}
					if ctx.state.moduleEnv.LibraryInherited[targetName] == nil {
						ctx.state.moduleEnv.LibraryInherited[targetName] = make(ModuleSection)
					}
					for itemName, item := range section {
						ctx.state.moduleEnv.LibraryRestricted[targetName][itemName] = item
						ctx.state.moduleEnv.LibraryInherited[targetName][itemName] = item
					}
				}
			}

			return BoolStatus(true)
		} else {
			result := ctx.executor.ExecuteWithState(string(content), ctx.state, nil, filename, 0, 0)

			if boolStatus, ok := result.(BoolStatus); ok && !bool(boolStatus) {
				return BoolStatus(false)
			}

			return BoolStatus(true)
		}
	})

	// ==================== io:: module ====================

	// write - output without automatic newline
	outputCommand := func(ctx *Context) Result {
		ch, args, found := getOutputChannel(ctx, "#out")
		if !found {
			// Fallback: use OutputContext for consistent channel resolution with system fallback
			text := ""
			for _, arg := range ctx.Args {
				text += formatArgForDisplay(arg, ctx.executor)
			}
			outCtx := NewOutputContext(ctx.state, ctx.executor)
			_ = outCtx.WriteToOut(text)
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
	outputLineCommand := func(ctx *Context) Result {
		ch, args, found := getOutputChannel(ctx, "#out")
		if !found {
			// Fallback: use OutputContext for consistent channel resolution with system fallback
			text := ""
			for i, arg := range ctx.Args {
				if i > 0 {
					text += " "
				}
				text += formatArgForDisplay(arg, ctx.executor)
			}
			outCtx := NewOutputContext(ctx.state, ctx.executor)
			_ = outCtx.WriteToOut(text + "\n")
			return BoolStatus(true)
		}

		text := ""
		for i, arg := range args {
			if i > 0 {
				text += " "
			}
			text += formatArgForDisplay(arg, ctx.executor)
		}

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
	ps.RegisterCommand("read", func(ctx *Context) Result {
		ch, found := getInputChannel(ctx, "#in")
		if !found {
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

		_, value, err := ChannelRecv(ch)
		if err != nil {
			ctx.LogError(CatIO, fmt.Sprintf("Failed to read: %v", err))
			return BoolStatus(false)
		}
		ctx.SetResult(value)
		return BoolStatus(true)
	})

	// rune - convert integer to Unicode character
	ps.RegisterCommand("rune", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatIO, "rune requires an integer argument")
			ctx.SetResult("")
			return BoolStatus(false)
		}

		var codepoint int64
		switch v := ctx.Args[0].(type) {
		case int64:
			codepoint = v
		case float64:
			codepoint = int64(v)
		case int:
			codepoint = int64(v)
		default:
			ctx.LogError(CatIO, fmt.Sprintf("rune requires an integer, got %T", ctx.Args[0]))
			ctx.SetResult("")
			return BoolStatus(false)
		}

		// Check for valid Unicode range
		if codepoint < 0 || codepoint > 0x10FFFF {
			ctx.LogError(CatIO, fmt.Sprintf("invalid Unicode codepoint: %d", codepoint))
			ctx.SetResult("")
			return BoolStatus(false)
		}

		ctx.SetResult(string(rune(codepoint)))
		return BoolStatus(true)
	})

	// ord - convert first character of string to Unicode codepoint
	ps.RegisterCommand("ord", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.SetResult(int64(0))
			return BoolStatus(false)
		}

		var str string
		switch v := ctx.Args[0].(type) {
		case string:
			str = v
		case QuotedString:
			str = string(v)
		case Symbol:
			str = string(v)
		default:
			ctx.SetResult(int64(0))
			return BoolStatus(false)
		}

		if str == "" {
			ctx.SetResult(int64(0))
			return BoolStatus(false)
		}

		// Get first rune from string
		runes := []rune(str)
		ctx.SetResult(int64(runes[0]))
		return BoolStatus(true)
	})

	// ==================== sys:: module ====================

	// msleep - sleep for specified milliseconds (async)
	ps.RegisterCommand("msleep", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: msleep <milliseconds>")
			return BoolStatus(false)
		}

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

	// log_print - output log messages from scripts
	ps.RegisterCommand("log_print", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatIO, "Usage: log_print <level>, <message>, [category]")
			return BoolStatus(false)
		}

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

		message := fmt.Sprintf("%v", ctx.Args[1])

		category := CatUser
		if len(ctx.Args) > 2 {
			category = LogCategory(fmt.Sprintf("%v", ctx.Args[2]))
		}

		ctx.logger.Log(level, category, message, ctx.Position, nil)

		return BoolStatus(level != LevelError)
	})

	// microtime - return microseconds since epoch or since interpreter started
	ps.RegisterCommand("microtime", func(ctx *Context) Result {
		// Try to get system time in microseconds
		now := time.Now()
		microtime := now.UnixMicro()
		ctx.SetResult(microtime)
		return BoolStatus(true)
	})

	// datetime - format and convert date/time values
	// datetime                        -> UTC now as "YYYY-MM-DDTHH:NN:SSZ"
	// datetime "America/Los_Angeles"  -> Local time as "YYYY-MM-DDTHH:NN:SS-07:00"
	// datetime "UTC", stamp           -> Convert stamp to UTC
	// datetime "UTC", stamp, "America/Los_Angeles" -> Interpret stamp as LA time, output UTC
	ps.RegisterCommand("datetime", func(ctx *Context) Result {
		now := time.Now()

		// Helper to format time with optional seconds
		formatTime := func(t time.Time, tz *time.Location, includeSeconds bool) string {
			t = t.In(tz)
			if tz == time.UTC {
				if includeSeconds {
					return t.Format("2006-01-02T15:04:05Z")
				}
				return t.Format("2006-01-02T15:04Z")
			}
			if includeSeconds {
				return t.Format("2006-01-02T15:04:05-07:00")
			}
			return t.Format("2006-01-02T15:04-07:00")
		}

		// Helper to parse time string, returns (time, hasSeconds, hasOffset, offsetStr, error)
		parseTimeStr := func(s string) (time.Time, bool, bool, string, error) {
			// Try formats with and without seconds, with various offset styles
			formats := []struct {
				format     string
				hasSeconds bool
				hasOffset  bool
			}{
				{"2006-01-02T15:04:05Z", true, true},
				{"2006-01-02T15:04:05-07:00", true, true},
				{"2006-01-02T15:04:05+07:00", true, true},
				{"2006-01-02T15:04Z", false, true},
				{"2006-01-02T15:04-07:00", false, true},
				{"2006-01-02T15:04+07:00", false, true},
				{"2006-01-02T15:04:05", true, false},
				{"2006-01-02T15:04", false, false},
			}

			for _, f := range formats {
				if t, err := time.Parse(f.format, s); err == nil {
					// Extract offset string if present
					offsetStr := ""
					if f.hasOffset {
						if strings.HasSuffix(s, "Z") {
							offsetStr = "Z"
						} else if idx := strings.LastIndexAny(s, "+-"); idx > 10 {
							offsetStr = s[idx:]
						}
					}
					return t, f.hasSeconds, f.hasOffset, offsetStr, nil
				}
			}
			return time.Time{}, false, false, "", fmt.Errorf("unable to parse time: %s", s)
		}

		// No arguments - return current UTC time
		if len(ctx.Args) == 0 {
			ctx.SetResult(formatTime(now, time.UTC, true))
			return BoolStatus(true)
		}

		// Get target timezone from first argument
		var targetTZ *time.Location
		var tzArg string

		switch v := ctx.Args[0].(type) {
		case string:
			tzArg = v
		case QuotedString:
			tzArg = string(v)
		case Symbol:
			tzArg = string(v)
		default:
			ctx.LogError(CatIO, fmt.Sprintf("datetime: timezone must be a string, got %T", ctx.Args[0]))
			ctx.SetResult(formatTime(now, time.UTC, true))
			return BoolStatus(false)
		}

		if tzArg == "UTC" {
			targetTZ = time.UTC
		} else {
			var err error
			targetTZ, err = time.LoadLocation(tzArg)
			if err != nil {
				ctx.LogError(CatIO, fmt.Sprintf("datetime: invalid timezone %q: %v", tzArg, err))
				ctx.SetResult(formatTime(now, time.UTC, true))
				return BoolStatus(false)
			}
		}

		// One argument - return current time in target timezone
		if len(ctx.Args) == 1 {
			ctx.SetResult(formatTime(now, targetTZ, true))
			return BoolStatus(true)
		}

		// Two or three arguments - convert a timestamp
		var stampStr string
		switch v := ctx.Args[1].(type) {
		case string:
			stampStr = v
		case QuotedString:
			stampStr = string(v)
		case Symbol:
			stampStr = string(v)
		default:
			ctx.LogError(CatIO, fmt.Sprintf("datetime: timestamp must be a string, got %T", ctx.Args[1]))
			ctx.SetResult(formatTime(now, targetTZ, true))
			return BoolStatus(false)
		}

		parsedTime, hasSeconds, hasOffset, offsetStr, err := parseTimeStr(stampStr)
		if err != nil {
			ctx.LogError(CatIO, fmt.Sprintf("datetime: %v", err))
			ctx.SetResult(formatTime(now, targetTZ, hasSeconds))
			return BoolStatus(false)
		}

		// Three arguments - source timezone specified
		if len(ctx.Args) >= 3 {
			var srcTZArg string
			switch v := ctx.Args[2].(type) {
			case string:
				srcTZArg = v
			case QuotedString:
				srcTZArg = string(v)
			case Symbol:
				srcTZArg = string(v)
			default:
				ctx.LogError(CatIO, fmt.Sprintf("datetime: source timezone must be a string, got %T", ctx.Args[2]))
				ctx.SetResult(formatTime(parsedTime.In(targetTZ), targetTZ, hasSeconds))
				return BoolStatus(false)
			}

			var srcTZ *time.Location
			if srcTZArg == "UTC" {
				srcTZ = time.UTC
			} else {
				srcTZ, err = time.LoadLocation(srcTZArg)
				if err != nil {
					ctx.LogError(CatIO, fmt.Sprintf("datetime: invalid source timezone %q: %v", srcTZArg, err))
					ctx.SetResult(formatTime(parsedTime.In(targetTZ), targetTZ, hasSeconds))
					return BoolStatus(false)
				}
			}

			// Check for conflicting offset specification
			conflictError := false
			if hasOffset {
				// Verify the offset matches the source timezone
				srcOffset := ""
				if srcTZ == time.UTC {
					srcOffset = "Z"
				} else {
					// Get offset for this time in source timezone
					testTime := time.Date(parsedTime.Year(), parsedTime.Month(), parsedTime.Day(),
						parsedTime.Hour(), parsedTime.Minute(), parsedTime.Second(), 0, srcTZ)
					_, offset := testTime.Zone()
					hours := offset / 3600
					mins := (offset % 3600) / 60
					if mins < 0 {
						mins = -mins
					}
					if hours >= 0 {
						srcOffset = fmt.Sprintf("+%02d:%02d", hours, mins)
					} else {
						srcOffset = fmt.Sprintf("%03d:%02d", hours, mins)
					}
				}

				// Check if offsets conflict
				if offsetStr != srcOffset && !(offsetStr == "Z" && srcTZ == time.UTC) {
					ctx.LogError(CatIO, fmt.Sprintf("datetime: offset %s in timestamp conflicts with timezone %s", offsetStr, srcTZArg))
					conflictError = true
				}
			}

			// Re-interpret the time in the source timezone (ignore the offset from parsing)
			reinterpretedTime := time.Date(parsedTime.Year(), parsedTime.Month(), parsedTime.Day(),
				parsedTime.Hour(), parsedTime.Minute(), parsedTime.Second(), 0, srcTZ)
			ctx.SetResult(formatTime(reinterpretedTime, targetTZ, hasSeconds))

			if conflictError {
				return BoolStatus(false)
			}
			return BoolStatus(true)
		}

		// Two arguments - timestamp already has offset info (or is UTC)
		ctx.SetResult(formatTime(parsedTime, targetTZ, hasSeconds))
		return BoolStatus(true)
	})
}
