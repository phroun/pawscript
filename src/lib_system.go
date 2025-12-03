package pawscript

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
			return NewStoredListWithoutRefs(items), true
		case StoredBlock:
			items, _ := parseArguments(string(v))
			return NewStoredListWithoutRefs(items), true
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
		ps.logger.DebugCat(CatIO,"valueToChannel: input type=%T, value=%v", val, val)
		switch v := val.(type) {
		case *StoredChannel:
			ps.logger.DebugCat(CatIO,"valueToChannel: direct *StoredChannel")
			return v
		case Symbol:
			markerType, objectID := parseObjectMarker(string(v))
			ps.logger.DebugCat(CatIO,"valueToChannel: Symbol, markerType=%s, objectID=%d", markerType, objectID)
			if markerType == "channel" && objectID >= 0 {
				if obj, exists := ctx.executor.getObject(objectID); exists {
					ps.logger.DebugCat(CatIO,"valueToChannel: got object from storage, type=%T", obj)
					if ch, ok := obj.(*StoredChannel); ok {
						ps.logger.DebugCat(CatIO,"valueToChannel: channel hasNativeSend=%v, isClosed=%v", ch.NativeSend != nil, ch.IsClosed)
						return ch
					}
				} else {
					ps.logger.DebugCat(CatIO,"valueToChannel: object %d not found in storage", objectID)
				}
			}
		case string:
			markerType, objectID := parseObjectMarker(v)
			ps.logger.DebugCat(CatIO,"valueToChannel: string, markerType=%s, objectID=%d", markerType, objectID)
			if markerType == "channel" && objectID >= 0 {
				if obj, exists := ctx.executor.getObject(objectID); exists {
					ps.logger.DebugCat(CatIO,"valueToChannel: got object from storage, type=%T", obj)
					if ch, ok := obj.(*StoredChannel); ok {
						ps.logger.DebugCat(CatIO,"valueToChannel: channel hasNativeSend=%v, isClosed=%v", ch.NativeSend != nil, ch.IsClosed)
						return ch
					}
				} else {
					ps.logger.DebugCat(CatIO,"valueToChannel: object %d not found in storage", objectID)
				}
			}
		default:
			ps.logger.DebugCat(CatIO,"valueToChannel: unhandled type %T", val)
		}
		return nil
	}

	// Helper to convert a value to a StoredFile
	valueToFile := func(ctx *Context, val interface{}) *StoredFile {
		switch v := val.(type) {
		case *StoredFile:
			return v
		}
		return nil
	}

	// Helper to convert channel value (which may be []byte or StoredBytes) to string
	// Used by 'read' command to convert raw bytes from I/O channels to unicode strings
	bytesToString := func(val interface{}) string {
		switch v := val.(type) {
		case []byte:
			return string(v)
		case StoredBytes:
			return string(v.Data())
		case string:
			return v
		default:
			return fmt.Sprintf("%v", v)
		}
	}

	// Helper to resolve a file name (like "#myfile") to a file
	// Resolution order: local variables -> ObjectsModule -> ObjectsInherited
	resolveFile := func(ctx *Context, fileName string) *StoredFile {
		// First, check local macro variables
		if value, exists := ctx.state.GetVariable(fileName); exists {
			if f := valueToFile(ctx, value); f != nil {
				return f
			}
		}

		// Then, check ObjectsModule and ObjectsInherited
		if ctx.state.moduleEnv != nil {
			ctx.state.moduleEnv.mu.RLock()
			defer ctx.state.moduleEnv.mu.RUnlock()

			if ctx.state.moduleEnv.ObjectsModule != nil {
				if obj, exists := ctx.state.moduleEnv.ObjectsModule[fileName]; exists {
					if f := valueToFile(ctx, obj); f != nil {
						return f
					}
				}
			}

			if ctx.state.moduleEnv.ObjectsInherited != nil {
				if obj, exists := ctx.state.moduleEnv.ObjectsInherited[fileName]; exists {
					if f := valueToFile(ctx, obj); f != nil {
						return f
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
			ps.logger.DebugCat(CatIO,"resolveChannel(%s): found in local vars, value type=%T, value=%v", channelName, value, value)
			if ch := valueToChannel(ctx, value); ch != nil {
				ps.logger.DebugCat(CatIO,"resolveChannel(%s): valueToChannel returned channel", channelName)
				return ch
			}
			ps.logger.DebugCat(CatIO,"resolveChannel(%s): valueToChannel returned nil", channelName)
		} else {
			ps.logger.DebugCat(CatIO,"resolveChannel(%s): NOT found in local vars", channelName)
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
		ps.logger.DebugCat(CatIO,"getOutputChannel: defaultName=%s, numArgs=%d", defaultName, len(args))

		// Check if first arg is already a channel (from tilde resolution)
		if len(args) > 0 {
			ps.logger.DebugCat(CatIO,"getOutputChannel: first arg type=%T, value=%v", args[0], args[0])
			if ch, ok := args[0].(*StoredChannel); ok {
				ps.logger.DebugCat(CatIO,"getOutputChannel: first arg is *StoredChannel, hasNativeSend=%v", ch.NativeSend != nil)
				return ch, args[1:], true
			}
			// Or if first arg is a symbol starting with #
			if sym, ok := args[0].(Symbol); ok {
				symStr := string(sym)
				if strings.HasPrefix(symStr, "#") {
					ps.logger.DebugCat(CatIO,"getOutputChannel: first arg is #-prefixed Symbol: %s", symStr)
					if ch := resolveChannel(ctx, symStr); ch != nil {
						ps.logger.DebugCat(CatIO,"getOutputChannel: resolved to channel, hasNativeSend=%v", ch.NativeSend != nil)
						return ch, args[1:], true
					}
					ps.logger.DebugCat(CatIO,"getOutputChannel: resolveChannel returned nil for %s", symStr)
				}
			}
		}

		// Use default channel (also resolved through local vars first)
		ps.logger.DebugCat(CatIO,"getOutputChannel: trying default channel %s", defaultName)
		if ch := resolveChannel(ctx, defaultName); ch != nil {
			ps.logger.DebugCat(CatIO,"getOutputChannel: default channel resolved, hasNativeSend=%v", ch.NativeSend != nil)
			return ch, args, true
		}

		ps.logger.DebugCat(CatIO,"getOutputChannel: NO channel found, returning false")
		return nil, args, false
	}

	getInputChannel := func(ctx *Context, defaultName string) (*StoredChannel, bool) {
		// Check if first arg is already a channel (from tilde resolution or channel marker)
		if len(ctx.Args) > 0 {
			// Try valueToChannel which handles direct channels, markers, and symbols
			if ch := valueToChannel(ctx, ctx.Args[0]); ch != nil {
				return ch, true
			}
			// Or if first arg is a symbol starting with # (like #in)
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
	ps.RegisterCommandInModule("os", "argc", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("os", "argv", func(ctx *Context) Result {
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
					// Convert raw slice to StoredList before setting as result
					setListResult(ctx, NewStoredListWithoutRefs(sourceList))
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
	ps.RegisterCommandInModule("os", "exec", func(ctx *Context) Result {
		if len(ctx.Args) == 0 {
			ctx.LogError(CatIO, "No command specified for exec.")
			return BoolStatus(false)
		}

		cmdName := fmt.Sprintf("%v", ctx.Args[0])
		resolvedCmd := cmdName // Will be updated if we resolve the path

		// Resolve relative paths with directory components relative to script directory
		if !filepath.IsAbs(cmdName) && (strings.Contains(cmdName, string(filepath.Separator)) || strings.Contains(cmdName, "/")) {
			if ps.config != nil && ps.config.ScriptDir != "" {
				resolvedCmd = filepath.Join(ps.config.ScriptDir, cmdName)
			} else {
				resolvedCmd, _ = filepath.Abs(cmdName)
			}
		}

		// Validate exec access against ExecRoots if configured
		if ps.config != nil && ps.config.FileAccess != nil {
			fileAccess := ps.config.FileAccess
			if len(fileAccess.ExecRoots) > 0 {
				// Resolve the command path for validation
				var cmdPath string
				var err error
				if filepath.IsAbs(resolvedCmd) {
					cmdPath = resolvedCmd
					// Check if the file exists
					if _, err = os.Stat(cmdPath); err != nil {
						ctx.LogError(CatIO, fmt.Sprintf("exec: command not found: %s", cmdName))
						return BoolStatus(false)
					}
				} else {
					// Try to find the command in PATH
					cmdPath, err = exec.LookPath(resolvedCmd)
					if err != nil {
						ctx.LogError(CatIO, fmt.Sprintf("exec: command not found: %s", cmdName))
						return BoolStatus(false)
					}
				}
				cmdPath, _ = filepath.Abs(cmdPath)
				cmdPath = filepath.Clean(cmdPath)

				// Check if command is within allowed exec roots
				// Use case-insensitive comparison on Windows/macOS
				allowed := false
				for _, root := range fileAccess.ExecRoots {
					// Normalize root path to handle any .. sequences
					absRoot, err := filepath.Abs(root)
					if err != nil {
						continue
					}
					absRoot = filepath.Clean(absRoot)
					if pathHasPrefix(cmdPath, absRoot+string(filepath.Separator)) || pathEquals(cmdPath, absRoot) {
						allowed = true
						break
					}
				}
				if !allowed {
					ctx.LogError(CatIO, "exec: access denied: command outside allowed roots")
					return BoolStatus(false)
				}

				// Security: exec roots must not overlap with write roots
				// This prevents write-then-execute attacks
				// Use case-insensitive comparison on Windows/macOS
				if len(fileAccess.WriteRoots) > 0 {
					for _, writeRoot := range fileAccess.WriteRoots {
						absWriteRoot, err := filepath.Abs(writeRoot)
						if err != nil {
							continue
						}
						absWriteRoot = filepath.Clean(absWriteRoot)
						if pathHasPrefix(cmdPath, absWriteRoot+string(filepath.Separator)) || pathEquals(cmdPath, absWriteRoot) {
							ctx.LogError(CatIO, "exec: access denied: cannot execute from writable directory (security restriction)")
							return BoolStatus(false)
						}
					}
				}
			}
		}

		var cmdArgs []string
		for i := 1; i < len(ctx.Args); i++ {
			cmdArgs = append(cmdArgs, fmt.Sprintf("%v", ctx.Args[i]))
		}

		cmd := exec.Command(resolvedCmd, cmdArgs...)

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

	// ==================== io:: module ====================

	// write - output without automatic newline (supports files and channels)
	outputCommand := func(ctx *Context) Result {
		// Check if first arg is a file handle
		if len(ctx.Args) > 0 {
			ps.logger.DebugCat(CatIO, "write: first arg type=%T, value=%v", ctx.Args[0], ctx.Args[0])
			// Direct file handle
			if f, ok := ctx.Args[0].(*StoredFile); ok {
				text := ""
				for _, arg := range ctx.Args[1:] {
					text += formatArgForDisplay(arg, ctx.executor)
				}
				err := f.Write(text)
				if err != nil {
					ctx.LogError(CatIO, fmt.Sprintf("write: %v", err))
					return BoolStatus(false)
				}
				return BoolStatus(true)
			}
			// Check for #-prefixed symbol that might be a file
			if sym, ok := ctx.Args[0].(Symbol); ok {
				symStr := string(sym)
				if strings.HasPrefix(symStr, "#") {
					if f := resolveFile(ctx, symStr); f != nil {
						text := ""
						for _, arg := range ctx.Args[1:] {
							text += formatArgForDisplay(arg, ctx.executor)
						}
						err := f.Write(text)
						if err != nil {
							ctx.LogError(CatIO, fmt.Sprintf("write: %v", err))
							return BoolStatus(false)
						}
						return BoolStatus(true)
					}
				}
			}
		}

		// Fall back to channel handling
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

	// echo/print - output with automatic newline and spaces between args (supports files)
	outputLineCommand := func(ctx *Context) Result {
		ps.logger.DebugCat(CatIO,"outputLineCommand (print/echo): starting")

		// Check if first arg is a file handle
		if len(ctx.Args) > 0 {
			// Direct file handle
			if f, ok := ctx.Args[0].(*StoredFile); ok {
				text := ""
				for i, arg := range ctx.Args[1:] {
					if i > 0 {
						text += " "
					}
					text += formatArgForDisplay(arg, ctx.executor)
				}
				err := f.Write(text + "\n")
				if err != nil {
					ctx.LogError(CatIO, fmt.Sprintf("echo: %v", err))
					return BoolStatus(false)
				}
				return BoolStatus(true)
			}
			// Check for #-prefixed symbol that might be a file
			if sym, ok := ctx.Args[0].(Symbol); ok {
				symStr := string(sym)
				if strings.HasPrefix(symStr, "#") {
					if f := resolveFile(ctx, symStr); f != nil {
						text := ""
						for i, arg := range ctx.Args[1:] {
							if i > 0 {
								text += " "
							}
							text += formatArgForDisplay(arg, ctx.executor)
						}
						err := f.Write(text + "\n")
						if err != nil {
							ctx.LogError(CatIO, fmt.Sprintf("echo: %v", err))
							return BoolStatus(false)
						}
						return BoolStatus(true)
					}
				}
			}
		}

		// Fall back to channel handling
		ch, args, found := getOutputChannel(ctx, "#out")
		if !found {
			// Fallback: use OutputContext for consistent channel resolution with system fallback
			ps.logger.DebugCat(CatIO,"outputLineCommand: NO channel found, using OutputContext fallback")
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

		ps.logger.DebugCat(CatIO,"outputLineCommand: channel found, hasNativeSend=%v", ch.NativeSend != nil)
		text := ""
		for i, arg := range args {
			if i > 0 {
				text += " "
			}
			text += formatArgForDisplay(arg, ctx.executor)
		}

		ps.logger.DebugCat(CatIO,"outputLineCommand: calling ChannelSend with text=%q", text)
		err := ChannelSend(ch, text+"\n")
		if err != nil {
			ps.logger.DebugCat(CatIO,"outputLineCommand: ChannelSend returned error: %v", err)
			ctx.LogError(CatIO, fmt.Sprintf("Failed to write: %v", err))
			return BoolStatus(false)
		}
		ps.logger.DebugCat(CatIO,"outputLineCommand: ChannelSend succeeded")
		return BoolStatus(true)
	}

	ps.RegisterCommandInModule("io", "write", outputCommand)
	ps.RegisterCommandInModule("io", "echo", outputLineCommand)
	ps.RegisterCommandInModule("io", "print", outputLineCommand)

	// read - read a line from stdin, channel, or file
	// For files: read <file> or read <file>, eof: true
	ps.RegisterCommandInModule("io", "read", func(ctx *Context) Result {
		// Helper to read from file with eof option
		readFromFile := func(f *StoredFile) Result {
			readToEof := false
			if eof, ok := ctx.NamedArgs["eof"]; ok {
				if b, ok := eof.(bool); ok {
					readToEof = b
				} else if s, ok := eof.(string); ok {
					readToEof = s == "true"
				}
			}
			var content string
			var err error
			if readToEof {
				content, err = f.ReadAll()
			} else {
				content, err = f.ReadLine()
			}
			if err != nil {
				if err.Error() == "EOF" || strings.Contains(err.Error(), "EOF") {
					ctx.SetResult("")
					return BoolStatus(false)
				}
				ctx.LogError(CatIO, fmt.Sprintf("read: %v", err))
				ctx.SetResult("")  // Set empty result on error to avoid stale values
				return BoolStatus(false)
			}
			ctx.SetResult(content)
			return BoolStatus(true)
		}

		// Check if first arg is a file (special case)
		if len(ctx.Args) > 0 {
			// Direct file handle
			if f, ok := ctx.Args[0].(*StoredFile); ok {
				return readFromFile(f)
			}
			// Check for #-prefixed symbol that might be a file
			if sym, ok := ctx.Args[0].(Symbol); ok {
				symStr := string(sym)
				if strings.HasPrefix(symStr, "#") {
					// Try to resolve as file first
					if f := resolveFile(ctx, symStr); f != nil {
						return readFromFile(f)
					}
				}
			}
		}

		// Try to get input channel (handles direct channels, markers, #symbols, defaults to #in)
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
			ctx.SetResult("")  // Set empty result on error to avoid stale values
			return BoolStatus(false)
		}
		// Convert raw bytes from I/O channels to unicode string
		ctx.SetResult(bytesToString(value))
		return BoolStatus(true)
	})

	// read_bytes - read binary data from a file
	// Usage: read_bytes <file> [count] or read_bytes <file>, all: true
	// Returns a StoredBytes object
	ps.RegisterCommandInModule("io", "read_bytes", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatIO, "read_bytes: file required")
			return BoolStatus(false)
		}

		// Get file handle
		var file *StoredFile
		if f, ok := ctx.Args[0].(*StoredFile); ok {
			file = f
		} else if sym, ok := ctx.Args[0].(Symbol); ok {
			symStr := string(sym)
			if strings.HasPrefix(symStr, "#") {
				file = resolveFile(ctx, symStr)
			}
		}

		if file == nil {
			ctx.LogError(CatIO, "read_bytes: not a file handle")
			return BoolStatus(false)
		}

		// Determine how many bytes to read
		count := 0 // 0 means read all

		// Check named args first
		if _, ok := ctx.NamedArgs["all"]; ok {
			count = 0 // Read all remaining bytes
		}

		// Check positional count argument
		if len(ctx.Args) > 1 {
			if n, ok := toInt64(ctx.Args[1]); ok {
				count = int(n)
			}
		}

		// Read bytes from file
		data, err := file.ReadBytes(count)
		if err != nil {
			if err.Error() == "EOF" || strings.Contains(err.Error(), "EOF") {
				// Return empty bytes on EOF
				result := NewStoredBytes(nil)
				id := ctx.executor.storeObject(result, "bytes")
				marker := fmt.Sprintf("\x00BYTES:%d\x00", id)
				ctx.state.SetResultWithoutClaim(Symbol(marker))
				return BoolStatus(false)
			}
			ctx.LogError(CatIO, fmt.Sprintf("read_bytes: %v", err))
			return BoolStatus(false)
		}

		// Create StoredBytes result
		result := NewStoredBytes(data)
		id := ctx.executor.storeObject(result, "bytes")
		marker := fmt.Sprintf("\x00BYTES:%d\x00", id)
		ctx.state.SetResultWithoutClaim(Symbol(marker))
		return BoolStatus(true)
	})

	// readkey_init - initialize key input manager for raw keyboard handling
	// Usage: readkey_init [input_channel] [echo: true|false]
	// Returns a list containing [lines_channel, keys_channel]
	// If input_channel is provided with a RawReader, uses that for input
	// Otherwise defaults to os.Stdin. If stdin is a terminal, enables raw mode.
	// By default, echo is disabled (for games/TUI). Use echo: true to enable.
	ps.RegisterCommandInModule("io", "readkey_init", func(ctx *Context) Result {
		// Get input source - default to os.Stdin
		var inputReader io.Reader = os.Stdin
		var echoWriter io.Writer = nil // Default: no echo

		// Check for channel argument with RawReader
		if len(ctx.Args) > 0 {
			if ch := valueToChannel(ctx, ctx.Args[0]); ch != nil {
				if ch.RawReader != nil {
					inputReader = ch.RawReader
				}
			}
		}

		// Check for echo: named argument
		if echoArg, hasEcho := ctx.NamedArgs["echo"]; hasEcho {
			if echoBool, ok := echoArg.(bool); ok && echoBool {
				echoWriter = os.Stdout
			} else if echoStr, ok := echoArg.(string); ok && (echoStr == "true" || echoStr == "yes") {
				echoWriter = os.Stdout
			}
		}

		// Create the key input manager
		manager := NewKeyInputManager(inputReader, echoWriter, nil)

		// Start the manager
		if err := manager.Start(); err != nil {
			ctx.LogError(CatIO, fmt.Sprintf("readkey_init: %v", err))
			return BoolStatus(false)
		}

		// Store the manager in executor for cleanup and access
		ctx.executor.mu.Lock()
		ctx.executor.keyInputManager = manager
		ctx.executor.mu.Unlock()

		// Return the two channels as a list
		linesCh := manager.GetLinesChannel()
		keysCh := manager.GetKeysChannel()

		// Store channels and create list with proper refs
		linesID := ctx.executor.storeObject(linesCh, "channel")
		keysID := ctx.executor.storeObject(keysCh, "channel")

		linesMarker := fmt.Sprintf("\x00CHANNEL:%d\x00", linesID)
		keysMarker := fmt.Sprintf("\x00CHANNEL:%d\x00", keysID)

		// Use NewStoredListWithRefs to properly claim references to the channels
		resultList := NewStoredListWithRefs([]interface{}{Symbol(linesMarker), Symbol(keysMarker)}, nil, ctx.executor)
		listID := ctx.executor.storeObject(resultList, "list")
		listMarker := fmt.Sprintf("\x00LIST:%d\x00", listID)
		ctx.state.SetResultWithoutClaim(Symbol(listMarker))

		return BoolStatus(true)
	})

	// readkey_stop - stop key input manager and restore terminal
	ps.RegisterCommandInModule("io", "readkey_stop", func(ctx *Context) Result {
		ctx.executor.mu.Lock()
		manager := ctx.executor.keyInputManager
		ctx.executor.keyInputManager = nil
		ctx.executor.mu.Unlock()

		if manager == nil {
			ctx.LogError(CatIO, "readkey_stop: no key input manager running")
			return BoolStatus(false)
		}

		if err := manager.Stop(); err != nil {
			ctx.LogError(CatIO, fmt.Sprintf("readkey_stop: %v", err))
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	// readkey - read a single key event from key input manager
	// Usage: readkey or readkey #keyin_channel
	// Returns the key as a string ("a", "M-a", "F1", "^C", etc.)
	ps.RegisterCommandInModule("io", "readkey", func(ctx *Context) Result {
		var keysCh *StoredChannel

		// Check for explicit channel argument
		if len(ctx.Args) > 0 {
			if ch, ok := ctx.Args[0].(*StoredChannel); ok {
				keysCh = ch
			} else if sym, ok := ctx.Args[0].(Symbol); ok {
				symStr := string(sym)
				if strings.HasPrefix(symStr, "#") {
					// Try to resolve as channel from state
					if obj, found := ctx.state.GetVariable(symStr); found && obj != nil {
						if ch, ok := obj.(*StoredChannel); ok {
							keysCh = ch
						}
					}
				}
			}
		}

		// If no channel specified, use the manager's keys channel
		if keysCh == nil {
			ctx.executor.mu.Lock()
			manager := ctx.executor.keyInputManager
			ctx.executor.mu.Unlock()

			if manager == nil {
				ctx.LogError(CatIO, "readkey: no key input manager running (call readkey_init first)")
				return BoolStatus(false)
			}
			keysCh = manager.GetKeysChannel()
		}

		// Receive from channel
		_, value, err := ChannelRecv(keysCh)
		if err != nil {
			ctx.LogError(CatIO, fmt.Sprintf("readkey: %v", err))
			ctx.SetResult("")  // Set empty result on error to avoid stale values
			return BoolStatus(false)
		}

		ctx.SetResult(value)
		return BoolStatus(true)
	})

	// write_bytes - write binary data to a file
	// Usage: write_bytes <file>, <bytes>
	ps.RegisterCommandInModule("io", "write_bytes", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatIO, "write_bytes: file and bytes required")
			return BoolStatus(false)
		}

		// Get file handle
		var file *StoredFile
		if f, ok := ctx.Args[0].(*StoredFile); ok {
			file = f
		} else if sym, ok := ctx.Args[0].(Symbol); ok {
			symStr := string(sym)
			if strings.HasPrefix(symStr, "#") {
				file = resolveFile(ctx, symStr)
			}
		}

		if file == nil {
			ctx.LogError(CatIO, "write_bytes: not a file handle")
			return BoolStatus(false)
		}

		// Get bytes to write
		var data []byte
		switch v := ctx.Args[1].(type) {
		case StoredBytes:
			data = v.Data()
		default:
			ctx.LogError(CatIO, fmt.Sprintf("write_bytes: expected bytes, got %s", getTypeName(v)))
			return BoolStatus(false)
		}

		// Write bytes to file
		err := file.WriteBytes(data)
		if err != nil {
			ctx.LogError(CatIO, fmt.Sprintf("write_bytes: %v", err))
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	// rune - convert integer to Unicode character
	ps.RegisterCommandInModule("io", "rune", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("io", "ord", func(ctx *Context) Result {
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

	// clear - clear terminal screen or specific regions
	// With no args: clear screen (ANSI in terminal, separator if redirected)
	// With arg: "eol", "bol", "line", "eos", "bos", "screen" for specific ANSI clear modes
	ps.RegisterCommandInModule("io", "clear", func(ctx *Context) Result {
		ts := ps.terminalState
		ts.mu.Lock()
		defer ts.mu.Unlock()

		// Get output channel - use it for both capability checking AND writing
		outCh, _, found := getOutputChannel(ctx, "#out")

		// Helper to send output to the resolved channel or system stdout
		sendOutput := func(text string) {
			if found && outCh != nil {
				_ = ChannelSend(outCh, text)
			} else {
				fmt.Print(text)
			}
		}

		// Check for mode argument
		if len(ctx.Args) > 0 {
			var mode string
			switch v := ctx.Args[0].(type) {
			case string:
				mode = v
			case Symbol:
				mode = string(v)
			case QuotedString:
				mode = string(v)
			default:
				mode = fmt.Sprintf("%v", v)
			}

			// Handle specific clear modes - emit ANSI codes if supported
			ansiCode := ANSIClearMode(mode)
			if ansiCode != "" && ChannelSupportsANSI(outCh) {
				// In brace expression: return ANSI code as string for substitution
				// Otherwise: emit to output channel
				if ctx.state.InBraceExpression {
					ctx.SetResult(QuotedString(ansiCode))
					return BoolStatus(true)
				}
				sendOutput(ansiCode)
				ts.HasCleared = false // partial clear doesn't count as full clear
				return BoolStatus(true)
			}
			// Unknown mode or no ANSI support - return empty string in brace, fall through otherwise
			if ctx.state.InBraceExpression {
				ctx.SetResult(QuotedString(""))
				return BoolStatus(true)
			}
		}

		// Default clear behavior - check channel's terminal capabilities
		if ChannelIsTerminal(outCh) && ChannelSupportsANSI(outCh) {
			// In brace expression: return ANSI code as string for substitution
			if ctx.state.InBraceExpression {
				ctx.SetResult(QuotedString(ANSIClearScreen()))
				return BoolStatus(true)
			}
			// Terminal mode - send ANSI clear and home cursor
			sendOutput(ANSIClearScreen())
			// Reset cursor position in our tracking
			ts.X = ts.XBase
			ts.Y = ts.YBase
			ts.HasCleared = true
		} else {
			// In brace expression without ANSI: return empty string (no side effects)
			if ctx.state.InBraceExpression {
				ctx.SetResult(QuotedString(""))
				return BoolStatus(true)
			}
			// Redirected mode - send separator unless we just cleared
			if !ts.HasCleared {
				sendOutput("\n=======================================\n\n")
				ts.HasCleared = true
			}
		}

		return BoolStatus(true)
	})

	// color - set foreground and/or background colors with optional attributes
	// color <fg>           - set foreground only, preserve background
	// color <fg>, <bg>     - set both foreground and background
	// Named args: bold, blink, underline, invert (boolean), reset (boolean)
	// Returns: list with (fg, bg, bold:, blink:, underline:, invert:, term:, ansi:, color:)
	ps.RegisterCommandInModule("io", "color", func(ctx *Context) Result {
		ts := ps.terminalState
		ts.mu.Lock()
		defer ts.mu.Unlock()

		// Get output channel - use it for both capability checking AND writing
		outCh, _, found := getOutputChannel(ctx, "#out")

		// Helper to send output to the resolved channel or system stdout
		sendOutput := func(text string) {
			if found && outCh != nil {
				_ = ChannelSend(outCh, text)
			} else {
				fmt.Print(text)
			}
		}

		// Check for reset option first
		if v, ok := ctx.NamedArgs["reset"]; ok && isTruthy(v) {
			// In brace expression: return ANSI code as string for substitution
			// Otherwise: emit to output channel
			if ctx.state.InBraceExpression {
				if ChannelSupportsANSI(outCh) {
					ctx.SetResult(QuotedString(ANSIReset()))
				} else {
					ctx.SetResult(QuotedString(""))
				}
				return BoolStatus(true)
			}

			// Emit reset sequence if ANSI supported
			if ChannelSupportsANSI(outCh) {
				sendOutput(ANSIReset())
			}

			// Reset all tracked state to defaults
			ts.CurrentFG = -1
			ts.CurrentBG = -1
			ts.Bold = false
			ts.AttrBlink = false
			ts.Underline = false
			ts.Invert = false
			ts.HasCleared = false

			// Build and return result list with channel's terminal info
			resultNamedArgs := map[string]interface{}{
				"bold":      false,
				"blink":     false,
				"underline": false,
				"invert":    false,
				"term":      ChannelGetTerminalType(outCh),
				"ansi":      ChannelSupportsANSI(outCh),
				"color":     ChannelSupportsColor(outCh),
			}

			result := NewStoredListWithNamed([]interface{}{
				int64(-1), // fg (default)
				int64(-1), // bg (default)
			}, resultNamedArgs)

			id := ctx.executor.storeObject(result, "list")
			marker := fmt.Sprintf("\x00LIST:%d\x00", id)
			ctx.state.SetResultWithoutClaim(Symbol(marker))
			return BoolStatus(true)
		}

		// Parse color arguments
		fg := -1 // -1 means don't change
		bg := -1

		// Helper to parse a color value
		parseColor := func(val interface{}) int {
			switch v := val.(type) {
			case int64:
				if v >= 0 && v <= 15 {
					return int(v)
				}
				return -1
			case int:
				if v >= 0 && v <= 15 {
					return v
				}
				return -1
			case float64:
				iv := int(v)
				if iv >= 0 && iv <= 15 {
					return iv
				}
				return -1
			case string:
				return ParseColorName(v)
			case Symbol:
				return ParseColorName(string(v))
			case QuotedString:
				return ParseColorName(string(v))
			default:
				return -1
			}
		}

		// Track whether we're actually setting anything (vs just querying)
		isSettingColor := false

		// Parse positional arguments
		if len(ctx.Args) >= 1 {
			fg = parseColor(ctx.Args[0])
			isSettingColor = true
		}
		if len(ctx.Args) >= 2 {
			bg = parseColor(ctx.Args[1])
		}

		// Parse named arguments for attributes
		// Start with current tracked state
		bold := ts.Bold
		blink := ts.AttrBlink
		underline := ts.Underline
		invert := ts.Invert

		if v, ok := ctx.NamedArgs["bold"]; ok {
			bold = isTruthy(v)
			isSettingColor = true
		}
		if v, ok := ctx.NamedArgs["blink"]; ok {
			blink = isTruthy(v)
			isSettingColor = true
		}
		if v, ok := ctx.NamedArgs["underline"]; ok {
			underline = isTruthy(v)
			isSettingColor = true
		}
		if v, ok := ctx.NamedArgs["invert"]; ok {
			invert = isTruthy(v)
			isSettingColor = true
		}

		// Generate and emit ANSI sequence
		// When fg or bg is -1 (unchanged), use the current tracked color
		// so it gets re-applied after any reset needed for attributes
		effectiveFG := fg
		if effectiveFG == -1 {
			effectiveFG = ts.CurrentFG
		}
		effectiveBG := bg
		if effectiveBG == -1 {
			effectiveBG = ts.CurrentBG
		}

		// Generate ANSI code if supported
		var ansiCode string
		if ChannelSupportsANSI(outCh) {
			if bg == -1 && len(ctx.Args) == 1 && !bold && !blink && !underline && !invert {
				// Only foreground specified, no attributes - just change foreground
				ansiCode = fmt.Sprintf("\x1b[%dm", CGAToANSIFG(fg))
			} else {
				// Need full color setting (handles reset for attributes)
				ansiCode = ANSIColor(effectiveFG, effectiveBG, bold, blink, underline, invert)
			}
		}

		// In brace expression with color setting: return ANSI code as string for substitution
		// When just querying (no args), fall through to return info list
		if ctx.state.InBraceExpression && isSettingColor {
			ctx.SetResult(QuotedString(ansiCode))
			return BoolStatus(true)
		}

		// Emit ANSI code to output
		if ansiCode != "" {
			sendOutput(ansiCode)
		}

		// Update tracked state
		if fg >= 0 {
			ts.CurrentFG = fg
		}
		if bg >= 0 {
			ts.CurrentBG = bg
		}
		ts.Bold = bold
		ts.AttrBlink = blink
		ts.Underline = underline
		ts.Invert = invert
		ts.HasCleared = false

		// Build and return result list with channel's terminal info
		resultNamedArgs := map[string]interface{}{
			"bold":      ts.Bold,
			"blink":     ts.AttrBlink,
			"underline": ts.Underline,
			"invert":    ts.Invert,
			"term":      ChannelGetTerminalType(outCh),
			"ansi":      ChannelSupportsANSI(outCh),
			"color":     ChannelSupportsColor(outCh),
		}

		result := NewStoredListWithNamed([]interface{}{
			int64(ts.CurrentFG),
			int64(ts.CurrentBG),
		}, resultNamedArgs)

		id := ctx.executor.storeObject(result, "list")
		marker := fmt.Sprintf("\x00LIST:%d\x00", id)
		ctx.state.SetResultWithoutClaim(Symbol(marker))

		return BoolStatus(true)
	})

	// cursor - get/set cursor position and appearance
	// Named args: xbase, ybase, rows, cols, indent, head (sticky - set once)
	//             x/col, y/row (position), h/v (relative movement)
	//             visible, shape, blink, color, free, duplex, reset
	// Returns: list with screen_rows, screen_cols, x, y, row, col, and settings
	ps.RegisterCommandInModule("io", "cursor", func(ctx *Context) Result {
		ts := ps.terminalState
		ts.mu.Lock()
		defer ts.mu.Unlock()

		// Handle reset FIRST, before any other processing
		// This resets terminal to initial state (like tput reset)
		if v, ok := ctx.NamedArgs["reset"]; ok && isTruthy(v) {
			ts.ResetTerminal()
		}

		// Re-detect screen size each time for accuracy
		ts.detectScreenSize()

		// Process duplex (echo) setting
		if duplex, ok := ctx.NamedArgs["duplex"]; ok {
			enabled := isTruthy(duplex)
			ts.mu.Unlock()
			_ = ts.SetDuplex(enabled)
			ts.mu.Lock()
		}

		// Process sticky region parameters
		if xbase, ok := ctx.NamedArgs["xbase"]; ok {
			if v, ok := toInt64(xbase); ok {
				ts.XBase = int(v)
			}
		}
		if ybase, ok := ctx.NamedArgs["ybase"]; ok {
			if v, ok := toInt64(ybase); ok {
				ts.YBase = int(v)
			}
		}
		if rows, ok := ctx.NamedArgs["rows"]; ok {
			if v, ok := toInt64(rows); ok {
				ts.Rows = int(v)
			}
		}
		if cols, ok := ctx.NamedArgs["cols"]; ok {
			if v, ok := toInt64(cols); ok {
				ts.Cols = int(v)
			}
		}
		if indent, ok := ctx.NamedArgs["indent"]; ok {
			if v, ok := toInt64(indent); ok {
				ts.Indent = int(v)
			}
		}
		if head, ok := ctx.NamedArgs["head"]; ok {
			if v, ok := toInt64(head); ok {
				ts.Head = int(v)
			}
		}

		// Process free mode
		if free, ok := ctx.NamedArgs["free"]; ok {
			ts.Free = isTruthy(free)
		}

		// Process cursor appearance
		if visible, ok := ctx.NamedArgs["visible"]; ok {
			ts.Visible = isTruthy(visible)
			if ts.Visible {
				fmt.Print(ANSIShowCursor())
			} else {
				fmt.Print(ANSIHideCursor())
			}
		}
		if shape, ok := ctx.NamedArgs["shape"]; ok {
			ts.Shape = fmt.Sprintf("%v", shape)
			fmt.Print(ANSISetCursorShape(ts.Shape, ts.Blink))
		}
		if blink, ok := ctx.NamedArgs["blink"]; ok {
			ts.Blink = fmt.Sprintf("%v", blink)
			fmt.Print(ANSISetCursorShape(ts.Shape, ts.Blink))
		}
		if color, ok := ctx.NamedArgs["color"]; ok {
			if v, ok := toInt64(color); ok {
				ts.Color = int(v)
			}
		}

		// Process position changes (x/col, y/row)
		posChanged := false
		if x, ok := ctx.NamedArgs["x"]; ok {
			if v, ok := toInt64(x); ok {
				ts.X = int(v)
				posChanged = true
			}
		}
		if col, ok := ctx.NamedArgs["col"]; ok {
			if v, ok := toInt64(col); ok {
				ts.X = int(v)
				posChanged = true
			}
		}
		if y, ok := ctx.NamedArgs["y"]; ok {
			if v, ok := toInt64(y); ok {
				ts.Y = int(v)
				posChanged = true
			}
		}
		if row, ok := ctx.NamedArgs["row"]; ok {
			if v, ok := toInt64(row); ok {
				ts.Y = int(v)
				posChanged = true
			}
		}

		// Process relative movement (h, v)
		if h, ok := ctx.NamedArgs["h"]; ok {
			if v, ok := toInt64(h); ok {
				ts.X += int(v)
				posChanged = true
			}
		}
		if v, ok := ctx.NamedArgs["v"]; ok {
			if val, ok := toInt64(v); ok {
				ts.Y += int(val)
				posChanged = true
			}
		}

		// Handle positional arguments for x, y
		if len(ctx.Args) >= 1 {
			if v, ok := toInt64(ctx.Args[0]); ok {
				ts.X = int(v)
				posChanged = true
			}
		}
		if len(ctx.Args) >= 2 {
			if v, ok := toInt64(ctx.Args[1]); ok {
				ts.Y = int(v)
				posChanged = true
			}
		}

		// Clamp position if changed
		if posChanged {
			// Unlock for ClampPosition which takes its own lock
			ts.mu.Unlock()
			ts.ClampPosition()
			ts.mu.Lock()

			// Move cursor - emit ANSI codes
			physX := ts.GetPhysicalX()
			physY := ts.GetPhysicalY()
			fmt.Print(ANSIMoveCursor(physY, physX))
		}

		// Cursor output marks position tracking as stale
		ts.HasCleared = false

		// Build result list with named args for all current state
		resultNamedArgs := map[string]interface{}{
			"screen_rows": int64(ts.ScreenRows),
			"screen_cols": int64(ts.ScreenCols),
			"x":           int64(ts.X),
			"y":           int64(ts.Y),
			"col":         int64(ts.X),
			"row":         int64(ts.Y),
			"rows":        int64(ts.Rows),
			"cols":        int64(ts.Cols),
			"head":        int64(ts.Head),
			"indent":      int64(ts.Indent),
			"visible":     ts.Visible,
			"shape":       ts.Shape,
			"blink":       ts.Blink,
			"color":       int64(ts.Color),
			"duplex":      ts.Duplex,
		}

		// Positional items: screen_rows, screen_cols, x, y, phys_x, phys_y
		result := NewStoredListWithNamed([]interface{}{
			int64(ts.ScreenRows),
			int64(ts.ScreenCols),
			int64(ts.X),
			int64(ts.Y),
			int64(ts.GetPhysicalX()),
			int64(ts.GetPhysicalY()),
		}, resultNamedArgs)

		// Store and return the list
		id := ctx.executor.storeObject(result, "list")
		marker := fmt.Sprintf("\x00LIST:%d\x00", id)
		ctx.state.SetResultWithoutClaim(Symbol(marker))

		return BoolStatus(true)
	})

	// ==================== sys:: module ====================

	// msleep - sleep for specified milliseconds (async)
	ps.RegisterCommandInModule("time", "msleep", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.ErrorCat(CatCommand, "Usage: msleep <milliseconds>")
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
				ps.logger.ErrorCat(CatArgument, "msleep: invalid milliseconds value: %v", v)
				return BoolStatus(false)
			}
			ms = parsed
		default:
			ps.logger.ErrorCat(CatArgument, "msleep: milliseconds must be a number, got %T", v)
			return BoolStatus(false)
		}

		if ms < 0 {
			ps.logger.ErrorCat(CatArgument, "msleep: milliseconds cannot be negative")
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
	// Supports multiple categories: log_print level, message, cat1, cat2, ...
	// Or a list of categories: log_print level, message, (cat1, cat2, ...)
	ps.RegisterCommandInModule("debug", "log_print", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatIO, "Usage: log_print <level>, <message>, [categories...]")
			return BoolStatus(false)
		}

		levelStr := strings.ToLower(fmt.Sprintf("%v", ctx.Args[0]))
		var level LogLevel
		switch levelStr {
		case "trace":
			level = LevelTrace
		case "info":
			level = LevelInfo
		case "debug":
			level = LevelDebug
		case "notice":
			level = LevelNotice
		case "warn", "warning":
			level = LevelWarn
		case "error":
			level = LevelError
		case "fatal":
			level = LevelFatal
		default:
			ctx.LogError(CatIO, fmt.Sprintf("Invalid log level: %s (use trace, info, debug, notice, warn, error, or fatal)", levelStr))
			return BoolStatus(false)
		}

		message := fmt.Sprintf("%v", ctx.Args[1])

		// Parse categories from remaining arguments
		var categories []LogCategory

		if len(ctx.Args) > 2 {
			// Check if third arg is a list (ParenGroup, StoredList, or list marker)
			thirdArg := ctx.Args[2]

			// Try to extract categories from a list-like argument
			var catItems []interface{}
			gotList := false

			switch v := thirdArg.(type) {
			case ParenGroup:
				catItems, _ = parseArguments(string(v))
				gotList = true
			case StoredBlock:
				catItems, _ = parseArguments(string(v))
				gotList = true
			case StoredList:
				catItems = v.Items()
				gotList = true
			case Symbol:
				// Check for list marker
				markerType, objectID := parseObjectMarker(string(v))
				if markerType == "list" && objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						if list, ok := obj.(StoredList); ok {
							catItems = list.Items()
							gotList = true
						}
					}
				}
			case string:
				// Check for list marker
				markerType, objectID := parseObjectMarker(v)
				if markerType == "list" && objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						if list, ok := obj.(StoredList); ok {
							catItems = list.Items()
							gotList = true
						}
					}
				}
			}

			if gotList {
				// Convert list items to categories
				for _, item := range catItems {
					catStr := fmt.Sprintf("%v", item)
					if cat, valid := LogCategoryFromString(catStr); valid {
						categories = append(categories, cat)
					} else {
						// Use as-is if not a recognized category name
						categories = append(categories, LogCategory(catStr))
					}
				}
			} else {
				// Multiple positional arguments as categories
				for i := 2; i < len(ctx.Args); i++ {
					catStr := fmt.Sprintf("%v", ctx.Args[i])
					if cat, valid := LogCategoryFromString(catStr); valid {
						categories = append(categories, cat)
					} else {
						// Use as-is if not a recognized category name
						categories = append(categories, LogCategory(catStr))
					}
				}
			}
		}

		// Default to CatUser if no categories specified
		if len(categories) == 0 {
			categories = []LogCategory{CatUser}
		}

		// Set output context for channel routing and LogConfig access
		ctx.logger.SetOutputContext(NewOutputContext(ctx.state, ctx.executor))
		defer ctx.logger.ClearOutputContext()

		// Use LogMulti for multiple categories, Log for single
		if len(categories) == 1 {
			ctx.logger.Log(level, categories[0], message, ctx.Position, nil)
		} else {
			ctx.logger.LogMulti(level, categories, message, ctx.Position, nil)
		}

		return BoolStatus(level != LevelError)
	})

	// microtime - return microseconds since epoch or since interpreter started
	ps.RegisterCommandInModule("time", "microtime", func(ctx *Context) Result {
		// Try to get system time in microseconds
		now := time.Now()
		microtime := now.UnixMicro()
		ctx.SetResult(microtime)
		return BoolStatus(true)
	})

	// error_logging - configure which log messages go to #err
	// Named args: default, floor, force (log levels), plus per-category levels
	// Returns: current configuration as StoredList with named args
	ps.RegisterCommandInModule("debug", "error_logging", func(ctx *Context) Result {
		return configureLogFilter(ctx, ps, "error")
	})

	// debug_logging - configure which log messages go to #out
	// Named args: default, floor, force (log levels), plus per-category levels
	// Returns: current configuration as StoredList with named args
	ps.RegisterCommandInModule("debug", "debug_logging", func(ctx *Context) Result {
		return configureLogFilter(ctx, ps, "debug")
	})

	// bubble_logging - configure which log messages get captured as bubbles
	// Named args: default, floor, force (log levels), plus per-category levels
	// Bubbles are created with flavor "level_category" (e.g., "error_argument", "warn_io")
	// Returns: current configuration as StoredList with named args
	ps.RegisterCommandInModule("debug", "bubble_logging", func(ctx *Context) Result {
		return configureLogFilter(ctx, ps, "bubble")
	})

	// datetime - format and convert date/time values
	// datetime                        -> UTC now as "YYYY-MM-DDTHH:NN:SSZ"
	// datetime "America/Los_Angeles"  -> Local time as "YYYY-MM-DDTHH:NN:SS-07:00"
	// datetime "UTC", stamp           -> Convert stamp to UTC
	// datetime "UTC", stamp, "America/Los_Angeles" -> Interpret stamp as LA time, output UTC
	ps.RegisterCommandInModule("time", "datetime", func(ctx *Context) Result {
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

	// ==================== debug:: module ====================

	// mem_stats - debug command to show stored objects
	ps.RegisterCommandInModule("debug", "mem_stats", func(ctx *Context) Result {
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

		// Sort by ID
		for i := 0; i < len(objects)-1; i++ {
			for j := i + 1; j < len(objects); j++ {
				if objects[i].ID > objects[j].ID {
					objects[i], objects[j] = objects[j], objects[i]
				}
			}
		}

		// Route output through channels
		outCtx := NewOutputContext(ctx.state, ctx.executor)
		var output strings.Builder
		output.WriteString("=== Memory Statistics ===\n")
		output.WriteString(fmt.Sprintf("Total stored objects: %d\n", len(objects)))
		output.WriteString(fmt.Sprintf("Total estimated size: %d bytes\n\n", totalSize))

		if len(objects) > 0 {
			output.WriteString("ID    Type      RefCount  Size(bytes)\n")
			output.WriteString("----  --------  --------  -----------\n")
			for _, obj := range objects {
				output.WriteString(fmt.Sprintf("%-4d  %-8s  %-8d  %d\n", obj.ID, obj.Type, obj.RefCount, obj.Size))
			}
		}
		_ = outCtx.WriteToOut(output.String())

		return BoolStatus(true)
	})

	// env_dump - debug command to show module environment details
	ps.RegisterCommandInModule("debug", "env_dump", func(ctx *Context) Result {
		outCtx := NewOutputContext(ctx.state, ctx.executor)
		var output strings.Builder

		env := ctx.state.moduleEnv
		env.mu.RLock()
		defer env.mu.RUnlock()

		output.WriteString("=== Module Environment ===\n")

		// Default module name
		if env.DefaultName != "" {
			output.WriteString(fmt.Sprintf("Default Module: %s\n", env.DefaultName))
		}

		// LibraryRestricted (available modules)
		restrictedCmdCount := 0
		for _, cmds := range env.LibraryRestricted {
			restrictedCmdCount += len(cmds)
		}
		output.WriteString(fmt.Sprintf("\n--- Library Restricted (%d in %d modules) ---\n", restrictedCmdCount, len(env.LibraryRestricted)))
		writeLibrarySectionWrapped(&output, env.LibraryRestricted)

		// Item metadata (shows import info) - grouped by source module
		// First, group items by their source module to count modules
		importedByModule := make(map[string][]string)
		for name, meta := range env.ItemMetadataModule {
			importedByModule[meta.ImportedFromModule] = append(importedByModule[meta.ImportedFromModule], name)
		}
		output.WriteString(fmt.Sprintf("\n--- Imported (%d in %d modules) ---\n", len(env.ItemMetadataModule), len(importedByModule)))
		if len(env.ItemMetadataModule) == 0 {
			output.WriteString("  (none)\n")
		} else {
			// Group items by their source module
			byModule := make(map[string][]string)
			for name, meta := range env.ItemMetadataModule {
				// Format: name(original) if renamed, else just name
				displayName := name
				if meta.OriginalName != name {
					displayName = fmt.Sprintf("%s(%s)", name, meta.OriginalName)
				}
				byModule[meta.ImportedFromModule] = append(byModule[meta.ImportedFromModule], displayName)
			}

			// Get sorted module names
			modNames := make([]string, 0, len(byModule))
			for modName := range byModule {
				modNames = append(modNames, modName)
			}
			sort.Strings(modNames)

			// Output in same format as Library Restricted
			for _, modName := range modNames {
				items := byModule[modName]
				sort.Strings(items)

				// Write "  modname:: " prefix
				prefix := fmt.Sprintf("  %s:: ", modName)
				output.WriteString(prefix)

				// Continuation indent is 4 spaces
				contIndent := "    "
				lineLen := len(prefix)

				for i, name := range items {
					if i > 0 {
						output.WriteString(", ")
						lineLen += 2
					}
					if lineLen+len(name) > 78 && i > 0 {
						output.WriteString("\n")
						output.WriteString(contIndent)
						lineLen = len(contIndent)
					}
					output.WriteString(name)
					lineLen += len(name)
				}
				output.WriteString("\n")
			}
		}

		// CommandRegistryModule - count only non-nil (non-REMOVEd) commands
		cmdNames := make([]string, 0, len(env.CommandRegistryModule))
		for name, handler := range env.CommandRegistryModule {
			if handler != nil { // Skip REMOVEd commands
				cmdNames = append(cmdNames, name)
			}
		}
		sort.Strings(cmdNames)
		output.WriteString(fmt.Sprintf("\n--- Commands (%d) ---\n", len(cmdNames)))
		writeWrappedList(&output, cmdNames, 2)

		// MacrosModule - count only non-nil (non-REMOVEd) macros
		macroNames := make([]string, 0, len(env.MacrosModule))
		for name, macro := range env.MacrosModule {
			if macro != nil { // Skip REMOVEd macros
				macroNames = append(macroNames, name)
			}
		}
		sort.Strings(macroNames)
		output.WriteString(fmt.Sprintf("\n--- Macros (%d) ---\n", len(macroNames)))
		writeWrappedList(&output, macroNames, 2)

		// ObjectsModule
		output.WriteString(fmt.Sprintf("\n--- Objects (%d) ---\n", len(env.ObjectsModule)))
		if len(env.ObjectsModule) == 0 {
			output.WriteString("  (none)\n")
		} else {
			objNames := make([]string, 0, len(env.ObjectsModule))
			for name := range env.ObjectsModule {
				objNames = append(objNames, name)
			}
			sort.Strings(objNames)
			writeWrappedList(&output, objNames, 2)
		}

		// Module exports
		output.WriteString(fmt.Sprintf("\n--- Exports (%d) ---\n", len(env.ModuleExports)))
		writeLibrarySectionWrapped(&output, env.ModuleExports)

		_ = outCtx.WriteToErr(output.String())
		return BoolStatus(true)
	})

	// lib_dump - debug command to show LibraryInherited (the full inherited library)
	ps.RegisterCommandInModule("debug", "lib_dump", func(ctx *Context) Result {
		outCtx := NewOutputContext(ctx.state, ctx.executor)
		var output strings.Builder

		env := ctx.state.moduleEnv
		env.mu.RLock()
		defer env.mu.RUnlock()

		output.WriteString("=== Library Inherited ===\n")
		inheritedCmdCount := 0
		for _, cmds := range env.LibraryInherited {
			inheritedCmdCount += len(cmds)
		}
		output.WriteString(fmt.Sprintf("\n--- Modules (%d in %d modules) ---\n", inheritedCmdCount, len(env.LibraryInherited)))
		writeLibrarySectionWrapped(&output, env.LibraryInherited)

		_ = outCtx.WriteToErr(output.String())
		return BoolStatus(true)
	})
}

// configureLogFilter implements error_logging, debug_logging, and bubble_logging commands
// filterType: "error" for #err, "debug" for #out, "bubble" for bubble capture
func configureLogFilter(ctx *Context, ps *PawScript, filterType string) Result {
	if ctx.state.moduleEnv == nil {
		ctx.LogError(CatSystem, "no module environment available")
		return BoolStatus(false)
	}

	// Get the LogConfig, triggering COW if we're making changes
	hasChanges := len(ctx.Args) > 0 || len(ctx.NamedArgs) > 0

	ctx.state.moduleEnv.mu.Lock()
	var filter *LogFilter
	if hasChanges {
		logConfig := ctx.state.moduleEnv.GetLogConfigForModification()
		switch filterType {
		case "error":
			filter = logConfig.ErrorLog
		case "debug":
			filter = logConfig.DebugLog
		case "bubble":
			filter = logConfig.BubbleLog
		}
	} else {
		logConfig := ctx.state.moduleEnv.LogConfigModule
		switch filterType {
		case "error":
			filter = logConfig.ErrorLog
		case "debug":
			filter = logConfig.DebugLog
		case "bubble":
			filter = logConfig.BubbleLog
		}
	}
	ctx.state.moduleEnv.mu.Unlock()

	// Helper to parse a log level from an argument
	parseLevel := func(val interface{}) (LogLevel, bool) {
		switch v := val.(type) {
		case string:
			level := LogLevelFromString(v)
			return level, level >= 0
		case QuotedString:
			level := LogLevelFromString(string(v))
			return level, level >= 0
		case Symbol:
			level := LogLevelFromString(string(v))
			return level, level >= 0
		case int64:
			if v >= int64(LevelTrace) && v <= int64(LevelFatal) {
				return LogLevel(v), true
			}
		case int:
			if v >= int(LevelTrace) && v <= int(LevelFatal) {
				return LogLevel(v), true
			}
		case float64:
			iv := int(v)
			if iv >= int(LevelTrace) && iv <= int(LevelFatal) {
				return LogLevel(iv), true
			}
		}
		return LevelFatal, false
	}

	// Process "default" level from positional arg or named arg
	if len(ctx.Args) >= 1 {
		if level, ok := parseLevel(ctx.Args[0]); ok {
			filter.Default = level
		}
	}
	if val, exists := ctx.NamedArgs["default"]; exists {
		if level, ok := parseLevel(val); ok {
			filter.Default = level
		}
	}

	// Process "floor" and "force" from named args
	if val, exists := ctx.NamedArgs["floor"]; exists {
		if level, ok := parseLevel(val); ok {
			filter.Floor = level
		}
	}
	if val, exists := ctx.NamedArgs["force"]; exists {
		if level, ok := parseLevel(val); ok {
			filter.Force = level
		}
	}

	// Process per-category levels from named args
	for key, val := range ctx.NamedArgs {
		// Skip reserved names
		if key == "default" || key == "floor" || key == "force" {
			continue
		}

		// Check if this is a valid category name
		if cat, valid := LogCategoryFromString(key); valid {
			if level, ok := parseLevel(val); ok {
				filter.Categories[cat] = level
			}
		}
	}

	// Build and return result list with current configuration
	resultNamedArgs := map[string]interface{}{
		"default": LogLevelToString(filter.Default),
		"floor":   LogLevelToString(filter.Floor),
		"force":   LogLevelToString(filter.Force),
	}

	// Add all category settings
	for _, cat := range AllLogCategories() {
		if level, exists := filter.Categories[cat]; exists {
			resultNamedArgs[string(cat)] = LogLevelToString(level)
		}
	}

	result := NewStoredListWithNamed([]interface{}{}, resultNamedArgs)
	id := ctx.executor.storeObject(result, "list")
	marker := fmt.Sprintf("\x00LIST:%d\x00", id)
	ctx.state.SetResultWithoutClaim(Symbol(marker))

	return BoolStatus(true)
}

// writeWrappedList writes a comma-separated list with word wrapping
func writeWrappedList(output *strings.Builder, items []string, indent int) {
	if len(items) == 0 {
		output.WriteString(strings.Repeat(" ", indent))
		output.WriteString("(none)\n")
		return
	}
	indentStr := strings.Repeat(" ", indent)
	output.WriteString(indentStr)
	lineLen := indent
	for i, name := range items {
		if i > 0 {
			output.WriteString(", ")
			lineLen += 2
		}
		if lineLen+len(name) > 78 && i > 0 {
			output.WriteString("\n")
			output.WriteString(indentStr)
			lineLen = indent
		}
		output.WriteString(name)
		lineLen += len(name)
	}
	output.WriteString("\n")
}

// writeLibrarySectionWrapped writes a Library (map of modules) with word wrapping
// Format: "  modname:: item1, item2, ..."
// Continuation lines are indented 2 spaces more than the initial "  "
func writeLibrarySectionWrapped(output *strings.Builder, lib Library) {
	if len(lib) == 0 {
		output.WriteString("  (none)\n")
		return
	}

	// Get sorted module names
	modNames := make([]string, 0, len(lib))
	for name := range lib {
		modNames = append(modNames, name)
	}
	sort.Strings(modNames)

	for _, modName := range modNames {
		section := lib[modName]
		itemNames := make([]string, 0, len(section))
		for itemName := range section {
			itemNames = append(itemNames, itemName)
		}
		sort.Strings(itemNames)

		// Write "  modname:: " prefix
		prefix := fmt.Sprintf("  %s:: ", modName)
		output.WriteString(prefix)

		// Continuation indent is 4 spaces (2 more than the leading "  ")
		contIndent := "    "
		lineLen := len(prefix)

		for i, name := range itemNames {
			if i > 0 {
				output.WriteString(", ")
				lineLen += 2
			}
			if lineLen+len(name) > 78 && i > 0 {
				output.WriteString("\n")
				output.WriteString(contIndent)
				lineLen = len(contIndent)
			}
			output.WriteString(name)
			lineLen += len(name)
		}
		output.WriteString("\n")
	}
}
