package pawscript

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// RegisterCoreLib registers core language commands
// Modules: core, macros, flow, debug
func (ps *PawScript) RegisterCoreLib() {
	// Helper function to set a StoredList as result with proper reference counting
	setListResult := func(ctx *Context, list StoredList) {
		id := ctx.executor.storeObject(list, "list")
		marker := fmt.Sprintf("\x00LIST:%d\x00", id)
		ctx.state.SetResultWithoutClaim(Symbol(marker))
	}

	// ==================== core:: module ====================

	// true - sets success state
	ps.RegisterCommandInModule("core", "true", func(ctx *Context) Result {
		return BoolStatus(true)
	})

	// false - sets error state
	ps.RegisterCommandInModule("core", "false", func(ctx *Context) Result {
		return BoolStatus(false)
	})

	// set_result - explicitly sets the result value
	ps.RegisterCommandInModule("core", "set_result", func(ctx *Context) Result {
		if len(ctx.Args) > 0 {
			ctx.SetResult(ctx.Args[0])
		} else {
			ctx.SetResult(nil)
		}
		return BoolStatus(true)
	})

	// get_result - gets the current result value and keeps it as the result
	ps.RegisterCommandInModule("core", "get_result", func(ctx *Context) Result {
		if ctx.HasResult() {
			return BoolStatus(true)
		}
		return BoolStatus(false)
	})

	// get_status - gets the previous command's status as a formal bool result
	// Returns true/false as the result, leaves status unchanged (like ret)
	ps.RegisterCommandInModule("core", "get_status", func(ctx *Context) Result {
		status := ctx.state.GetLastStatus()
		ctx.SetResult(status)
		return BoolStatus(status)
	})

	// get_substatus - gets whether the previous command's brace expressions all succeeded
	// Returns true (result and status) if no brace expressions returned false status
	// Returns false (result and status) if any brace expression returned false status
	ps.RegisterCommandInModule("core", "get_substatus", func(ctx *Context) Result {
		braceFailures := ctx.state.GetLastBraceFailureCount()
		success := braceFailures == 0
		ctx.SetResult(success)
		return BoolStatus(success)
	})

	// ret - early return from block
	ps.RegisterCommandInModule("core", "ret", func(ctx *Context) Result {
		switch len(ctx.Args) {
		case 0:
			return EarlyReturn{
				Status:    BoolStatus(ctx.state.GetLastStatus()),
				Result:    ctx.GetResult(),
				HasResult: ctx.HasResult(),
			}
		case 1:
			return EarlyReturn{
				Status:    BoolStatus(ctx.state.GetLastStatus()),
				Result:    ctx.Args[0],
				HasResult: true,
			}
		default:
			ctx.LogError(CatCommand, "Usage: ret [value]")
			return BoolStatus(false)
		}
	})

	// infer - returns the type of a value
	ps.RegisterCommandInModule("types", "infer", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.SetResult("undefined")
			return BoolStatus(true)
		}
		value := ctx.Args[0]
		typeName := getTypeName(value)
		ctx.SetResult(typeName)
		return BoolStatus(true)
	})

	// type - returns the type of a variable without fetching its value
	ps.RegisterCommandInModule("types", "type", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: type <variable_name>")
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
	ps.RegisterCommandInModule("types", "list", func(ctx *Context) Result {
		setListResult(ctx, NewStoredListWithRefs(ctx.Args, ctx.NamedArgs, ctx.executor))
		return BoolStatus(true)
	})

	// len - returns the length of a list, string, or channel
	ps.RegisterCommandInModule("types", "len", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: len <list|string|channel>")
			ctx.SetResult(0)
			return BoolStatus(false)
		}

		value := ctx.Args[0]

		// Helper to resolve a value (handles markers to get actual objects)
		resolveValue := func(val interface{}) interface{} {
			switch v := val.(type) {
			case Symbol:
				markerType, objectID := parseObjectMarker(string(v))
				if objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						switch markerType {
						case "channel":
							if ch, ok := obj.(*StoredChannel); ok {
								return ch
							}
						case "list":
							if list, ok := obj.(StoredList); ok {
								return list
							}
						case "string":
							if str, ok := obj.(StoredString); ok {
								return str
							}
						}
						return obj
					}
				}
			case string:
				markerType, objectID := parseObjectMarker(v)
				if objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						switch markerType {
						case "channel":
							if ch, ok := obj.(*StoredChannel); ok {
								return ch
							}
						case "list":
							if list, ok := obj.(StoredList); ok {
								return list
							}
						case "string":
							if str, ok := obj.(StoredString); ok {
								return str
							}
						}
						return obj
					}
				}
			}
			return val
		}

		// Check for #-prefixed symbol (resolve like tilde would)
		if sym, ok := value.(Symbol); ok {
			symStr := string(sym)
			if strings.HasPrefix(symStr, "#") {
				if localVal, exists := ctx.state.GetVariable(symStr); exists {
					value = resolveValue(localVal)
				} else if ctx.state.moduleEnv != nil {
					ctx.state.moduleEnv.mu.RLock()
					if ctx.state.moduleEnv.ObjectsModule != nil {
						if obj, exists := ctx.state.moduleEnv.ObjectsModule[symStr]; exists {
							value = resolveValue(obj)
						}
					}
					ctx.state.moduleEnv.mu.RUnlock()
				}
			} else {
				// Regular Symbol might be a marker
				value = resolveValue(value)
			}
		} else if _, ok := value.(string); ok {
			// String might be a marker
			value = resolveValue(value)
		}

		switch v := value.(type) {
		case StoredList:
			ctx.SetResult(int64(v.Len()))
			return BoolStatus(true)
		case *StoredChannel:
			ctx.SetResult(int64(ChannelLen(v)))
			return BoolStatus(true)
		case StoredString:
			ctx.SetResult(int64(len(string(v))))
			return BoolStatus(true)
		case string, QuotedString, Symbol:
			resolved := ctx.executor.resolveValue(v)
			str := fmt.Sprintf("%v", resolved)
			ctx.SetResult(int64(len(str)))
			return BoolStatus(true)
		case ParenGroup:
			items, _ := parseArguments(string(v))
			ctx.SetResult(int64(len(items)))
			return BoolStatus(true)
		default:
			ctx.LogError(CatType, fmt.Sprintf("Cannot get length of type %s\n", getTypeName(v)))
			ctx.SetResult(0)
			return BoolStatus(false)
		}
	})

	// stack_trace - returns the current macro call stack as a list
	ps.RegisterCommandInModule("core", "stack_trace", func(ctx *Context) Result {
		macroCtx := ctx.GetMacroContext()
		if macroCtx == nil {
			// At top level, return empty list
			setListResult(ctx, NewStoredList(nil))
			return BoolStatus(true)
		}

		var frames []interface{}
		for mc := macroCtx; mc != nil; mc = mc.ParentMacro {
			// Create a list for each frame with named args
			frame := NewStoredListWithNamed(nil, map[string]interface{}{
				"macro":    mc.MacroName,
				"file":     mc.InvocationFile,
				"line":     int64(mc.InvocationLine),
				"column":   int64(mc.InvocationColumn),
				"def_file": mc.DefinitionFile,
				"def_line": int64(mc.DefinitionLine),
			})
			// Store frame and create marker
			frameID := ctx.executor.storeObject(frame, "list")
			frameMarker := Symbol(fmt.Sprintf("\x00LIST:%d\x00", frameID))
			frames = append(frames, frameMarker)
		}

		setListResult(ctx, NewStoredList(frames))
		return BoolStatus(true)
	})

	// bubble - add a bubble to the bubble map
	// Usage: bubble flavor, content [, trace [, memo]]
	// flavor: string key for categorizing bubbles
	// content: any PawScript value
	// trace: boolean (default true) - whether to include stack trace
	// memo: optional string memo
	ps.RegisterCommandInModule("core", "bubble", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "bubble requires at least 2 arguments: flavor, content")
			return BoolStatus(false)
		}

		// Get flavor (convert to string)
		flavor := fmt.Sprintf("%v", ctx.Args[0])

		// Get content
		content := ctx.Args[1]

		// Get trace (default true)
		trace := true
		if len(ctx.Args) >= 3 {
			switch v := ctx.Args[2].(type) {
			case bool:
				trace = v
			case Symbol:
				trace = string(v) != "false" && string(v) != "0"
			case string:
				trace = v != "false" && v != "0"
			default:
				trace = true
			}
		}

		// Get memo (default empty)
		memo := ""
		if len(ctx.Args) >= 4 {
			memo = fmt.Sprintf("%v", ctx.Args[3])
		}

		// Claim reference if content is a stored object
		if sym, ok := content.(Symbol); ok {
			_, objectID := parseObjectMarker(string(sym))
			if objectID >= 0 {
				ctx.state.ClaimObjectReference(objectID)
			}
		}

		ctx.state.AddBubble(flavor, content, trace, memo)
		return BoolStatus(true)
	})

	// bubble_dump - debug command to dump the bubble map contents
	ps.RegisterCommandInModule("debug", "bubble_dump", func(ctx *Context) Result {
		stderr := ps.logger.GetStderr()
		bubbleMap := ctx.state.GetBubbleMap()
		if len(bubbleMap) == 0 {
			fmt.Fprintln(stderr, "[bubble_dump] No bubbles")
			return BoolStatus(true)
		}

		fmt.Fprintln(stderr, "[bubble_dump] Bubble map contents:")
		for flavor, entries := range bubbleMap {
			fmt.Fprintf(stderr, "  Flavor: %s (%d entries)\n", flavor, len(entries))
			for i, entry := range entries {
				fmt.Fprintf(stderr, "    [%d] content=%v, microtime=%d, memo=%q\n",
					i, entry.Content, entry.Microtime, entry.Memo)
				if len(entry.StackTrace) > 0 {
					fmt.Fprintf(stderr, "        stack trace (%d frames):\n", len(entry.StackTrace))
					for j, frame := range entry.StackTrace {
						if frameMap, ok := frame.(map[string]interface{}); ok {
							fmt.Fprintf(stderr, "          [%d] %v at %v:%v\n",
								j, frameMap["macro"], frameMap["file"], frameMap["line"])
						}
					}
				}
			}
		}
		return BoolStatus(true)
	})

	// bubble_orphans - retrieve orphaned bubbles from abandoned fibers
	// Merges orphaned bubbles into the current context's bubbleMap and clears the orphaned map
	ps.RegisterCommandInModule("core", "bubble_orphans", func(ctx *Context) Result {
		orphaned := ctx.executor.GetOrphanedBubbles()
		if len(orphaned) == 0 {
			return BoolStatus(true)
		}

		// Merge orphaned bubbles into current context's bubbleMap
		ctx.state.mu.Lock()
		if ctx.state.bubbleMap == nil {
			ctx.state.bubbleMap = make(map[string][]*BubbleEntry)
		}
		for flavor, entries := range orphaned {
			ctx.state.bubbleMap[flavor] = append(ctx.state.bubbleMap[flavor], entries...)
		}
		ctx.state.mu.Unlock()

		// Clear the orphaned bubbles now that they've been transferred
		ctx.executor.ClearOrphanedBubbles()

		return BoolStatus(true)
	})

	// bubble_orphans_dump - debug command to dump orphaned bubbles without retrieving them
	ps.RegisterCommandInModule("debug", "bubble_orphans_dump", func(ctx *Context) Result {
		stderr := ps.logger.GetStderr()
		orphaned := ctx.executor.GetOrphanedBubbles()
		if len(orphaned) == 0 {
			fmt.Fprintln(stderr, "[bubble_orphans_dump] No orphaned bubbles")
			return BoolStatus(true)
		}

		fmt.Fprintln(stderr, "[bubble_orphans_dump] Orphaned bubble map contents:")
		for flavor, entries := range orphaned {
			fmt.Fprintf(stderr, "  Flavor: %s (%d entries)\n", flavor, len(entries))
			for i, entry := range entries {
				fmt.Fprintf(stderr, "    [%d] content=%v, microtime=%d, memo=%q\n",
					i, entry.Content, entry.Microtime, entry.Memo)
				if len(entry.StackTrace) > 0 {
					fmt.Fprintf(stderr, "        stack trace (%d frames):\n", len(entry.StackTrace))
					for j, frame := range entry.StackTrace {
						if frameMap, ok := frame.(map[string]interface{}); ok {
							fmt.Fprintf(stderr, "          [%d] %v at %v:%v\n",
								j, frameMap["macro"], frameMap["file"], frameMap["line"])
						}
					}
				}
			}
		}
		return BoolStatus(true)
	})

	// ==================== macros:: module ====================

	// macro - define a macro
	ps.RegisterCommandInModule("macros", "macro", func(ctx *Context) Result {
		ps.logger.DebugCat(CatMacro,"macro command called with %d args", len(ctx.Args))

		// Capture the current module environment for lexical scoping
		macroEnv := NewMacroModuleEnvironment(ctx.state.moduleEnv)

		// Helper to extract code from a block argument (ParenGroup, StoredBlock marker, or string)
		extractCode := func(arg interface{}) string {
			switch v := arg.(type) {
			case ParenGroup:
				return string(v)
			case Symbol:
				// Check if it's a block marker
				markerType, objectID := parseObjectMarker(string(v))
				if markerType == "block" && objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						if storedBlock, ok := obj.(StoredBlock); ok {
							return string(storedBlock)
						}
					}
				}
				return string(v)
			default:
				return fmt.Sprintf("%v", arg)
			}
		}

		// Check for anonymous macro: macro (body)
		if len(ctx.Args) == 1 {
			commands := extractCode(ctx.Args[0])
			ps.logger.DebugCat(CatMacro,"Creating anonymous macro with commands: %s", commands)

			macro := NewStoredMacroWithEnv(commands, ctx.Position, macroEnv)
			objectID := ctx.executor.storeObject(macro, "macro")
			macroMarker := fmt.Sprintf("\x00MACRO:%d\x00", objectID)
			ctx.state.SetResult(Symbol(macroMarker))

			ps.logger.DebugCat(CatMacro,"Created anonymous macro (object %d)", objectID)
			return BoolStatus(true)
		}

		// Named macro: macro name, (body)
		if len(ctx.Args) < 2 {
			ps.logger.ErrorCat(CatCommand, "Usage: macro <name>, <commands> OR macro <commands>")
			return BoolStatus(false)
		}

		name := fmt.Sprintf("%v", ctx.Args[0])
		commands := extractCode(ctx.Args[1])

		ps.logger.DebugCat(CatMacro,"Defining macro '%s' with commands: %s", name, commands)

		// Create the StoredMacro
		macro := NewStoredMacroWithEnv(commands, ctx.Position, macroEnv)

		// Store in module environment's MacrosModule (with COW)
		ctx.state.moduleEnv.mu.Lock()
		ctx.state.moduleEnv.EnsureMacroRegistryCopied()
		ctx.state.moduleEnv.MacrosModule[name] = &macro
		ctx.state.moduleEnv.mu.Unlock()

		ps.logger.DebugCat(CatMacro,"Successfully defined named macro '%s' in MacrosModule", name)
		return BoolStatus(true)
	})

	// call - call a macro or command
	ps.RegisterCommandInModule("macros", "call", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.ErrorCat(CatCommand, "Usage: call <macro_name_or_object>, [args...]")
			return BoolStatus(false)
		}

		callArgs := ctx.Args[1:]
		childState := ctx.state.CreateChild()
		firstArg := ctx.Args[0]

		// Check if the first argument is already a resolved StoredCommand object
		if cmd, ok := firstArg.(StoredCommand); ok {
			ps.logger.DebugCat(CatMacro,"Calling resolved StoredCommand object: %s", cmd.CommandName)

			cmdCtx := &Context{
				Args:      callArgs,
				NamedArgs: ctx.NamedArgs,
				Position:  ctx.Position,
				state:     childState,
				executor:  ctx.executor,
				logger:    ctx.logger,
			}

			result := cmd.Handler(cmdCtx)

			if childState.HasResult() {
				ctx.state.SetResult(childState.GetResult())
			}

			return result
		}

		// Check if the first argument is already a resolved StoredMacro object
		if macro, ok := firstArg.(StoredMacro); ok {
			ps.logger.DebugCat(CatMacro,"Calling resolved StoredMacro object")

			return ps.executor.ExecuteStoredMacro(&macro, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
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

			if markerType == "command" && objectID >= 0 {
				ps.logger.DebugCat(CatMacro,"Calling StoredCommand via marker (object %d)", objectID)

				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.ErrorCat(CatArgument, "Command object %d not found", objectID)
					return BoolStatus(false)
				}

				cmd, ok := obj.(StoredCommand)
				if !ok {
					ps.logger.ErrorCat(CatArgument, "Object %d is not a StoredCommand", objectID)
					return BoolStatus(false)
				}

				cmdCtx := &Context{
					Args:      callArgs,
					NamedArgs: ctx.NamedArgs,
					Position:  ctx.Position,
					state:     childState,
					executor:  ctx.executor,
					logger:    ctx.logger,
				}

				result := cmd.Handler(cmdCtx)

				if childState.HasResult() {
					ctx.state.SetResult(childState.GetResult())
				}

				return result
			}

			if markerType == "macro" && objectID >= 0 {
				ps.logger.DebugCat(CatMacro,"Calling StoredMacro via marker (object %d)", objectID)

				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.ErrorCat(CatArgument, "Macro object %d not found", objectID)
					return BoolStatus(false)
				}

				macro, ok := obj.(StoredMacro)
				if !ok {
					ps.logger.ErrorCat(CatArgument, "Object %d is not a StoredMacro", objectID)
					return BoolStatus(false)
				}

				return ps.executor.ExecuteStoredMacro(&macro, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
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

	// Check if the first argument is a marker (string type, from $1 substitution etc.)
	if str, ok := firstArg.(string); ok {
		markerType, objectID := parseObjectMarker(str)

		if markerType == "command" && objectID >= 0 {
			ps.logger.DebugCat(CatMacro,"Calling StoredCommand via string marker (object %d)", objectID)

			obj, exists := ctx.executor.getObject(objectID)
			if !exists {
				ps.logger.ErrorCat(CatArgument, "Command object %d not found", objectID)
				return BoolStatus(false)
			}

			cmd, ok := obj.(StoredCommand)
			if !ok {
				ps.logger.ErrorCat(CatArgument, "Object %d is not a StoredCommand", objectID)
				return BoolStatus(false)
			}

			cmdCtx := &Context{
				Args:      callArgs,
				NamedArgs: ctx.NamedArgs,
				Position:  ctx.Position,
				state:     childState,
				executor:  ctx.executor,
				logger:    ctx.logger,
			}

			result := cmd.Handler(cmdCtx)

			if childState.HasResult() {
				ctx.state.SetResult(childState.GetResult())
			}

			return result
		}

		if markerType == "macro" && objectID >= 0 {
			ps.logger.DebugCat(CatMacro,"Calling StoredMacro via string marker (object %d)", objectID)

			obj, exists := ctx.executor.getObject(objectID)
			if !exists {
				ps.logger.ErrorCat(CatArgument, "Macro object %d not found", objectID)
				return BoolStatus(false)
			}

			macro, ok := obj.(StoredMacro)
			if !ok {
				ps.logger.ErrorCat(CatArgument, "Object %d is not a StoredMacro", objectID)
				return BoolStatus(false)
			}

			return ps.executor.ExecuteStoredMacro(&macro, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
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

	// Otherwise, treat it as a macro name - look up in module environment
		name := fmt.Sprintf("%v", firstArg)
		ps.logger.DebugCat(CatMacro,"Calling macro by name: %s", name)

		// Look up macro in module environment (COW - only check MacrosModule)
		var macro *StoredMacro
		ctx.state.moduleEnv.mu.RLock()
		if m, exists := ctx.state.moduleEnv.MacrosModule[name]; exists && m != nil {
			macro = m
		}
		ctx.state.moduleEnv.mu.RUnlock()

		if macro == nil {
			ps.logger.ErrorCat(CatMacro, "Macro \"%s\" not found", name)
			return BoolStatus(false)
		}

		return ps.executor.ExecuteStoredMacro(macro, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
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
	})

	// macro_list - list all defined macros in current scope
	ps.RegisterCommandInModule("macros", "macro_list", func(ctx *Context) Result {
		// Collect macros from module environment (COW - only check MacrosModule)
		ctx.state.moduleEnv.mu.RLock()
		macros := make([]string, 0, len(ctx.state.moduleEnv.MacrosModule))
		for name, macro := range ctx.state.moduleEnv.MacrosModule {
			if macro != nil {
				macros = append(macros, name)
			}
		}
		ctx.state.moduleEnv.mu.RUnlock()

		sort.Strings(macros)
		ctx.SetResult(fmt.Sprintf("%v", macros))
		return BoolStatus(true)
	})

	// macro_delete - delete a macro by name from current scope
	ps.RegisterCommandInModule("macros", "macro_delete", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: macro_delete <macro_name>")
			return BoolStatus(false)
		}

		name := fmt.Sprintf("%v", ctx.Args[0])

		// Delete from MacrosModule (COW will trigger on write if needed)
		ctx.state.moduleEnv.mu.Lock()
		macro, exists := ctx.state.moduleEnv.MacrosModule[name]
		if !exists || macro == nil {
			ctx.state.moduleEnv.mu.Unlock()
			ctx.LogError(CatMacro, fmt.Sprintf("PawScript macro \"%s\" not found or could not be deleted", name))
			return BoolStatus(false)
		}
		ctx.state.moduleEnv.EnsureMacroRegistryCopied()
		delete(ctx.state.moduleEnv.MacrosModule, name)
		ctx.state.moduleEnv.mu.Unlock()

		return BoolStatus(true)
	})

	// macro_clear - clear all macros from current scope
	ps.RegisterCommandInModule("macros", "macro_clear", func(ctx *Context) Result {
		// Count and clear macros from MacrosModule (COW)
		ctx.state.moduleEnv.mu.Lock()
		count := 0

		// Count non-nil macros in MacrosModule
		for _, macro := range ctx.state.moduleEnv.MacrosModule {
			if macro != nil {
				count++
			}
		}

		// Reset MacrosModule to empty
		ctx.state.moduleEnv.MacrosModule = make(map[string]*StoredMacro)
		ctx.state.moduleEnv.macrosModuleCopied = true

		ctx.state.moduleEnv.mu.Unlock()

		ctx.SetResult(fmt.Sprintf("Cleared %d PawScript macros", count))
		return BoolStatus(true)
	})

	// command_ref - get a reference to a built-in or registered command
	ps.RegisterCommandInModule("macros", "command_ref", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.ErrorCat(CatCommand, "Usage: command_ref <command_name>")
			return BoolStatus(false)
		}

		commandName := fmt.Sprintf("%v", ctx.Args[0])
		ps.logger.DebugCat(CatMacro, "Getting command reference for: %s", commandName)

		handler, exists := ctx.state.moduleEnv.GetCommand(commandName)
		if !exists {
			ps.logger.ErrorCat(CatMacro, "Command \"%s\" not found", commandName)
			return BoolStatus(false)
		}

		cmd := NewStoredCommand(commandName, handler)
		objectID := ctx.executor.storeObject(cmd, "command")
		commandMarker := fmt.Sprintf("\x00COMMAND:%d\x00", objectID)
		ctx.state.SetResult(Symbol(commandMarker))

		ps.logger.DebugCat(CatMacro,"Created command reference for '%s' (object %d)", commandName, objectID)
		return BoolStatus(true)
	})

	// ==================== flow:: module ====================

	// while - loop while condition is true
	// Generator-aware: catches YieldResult and attaches WhileContinuation
	ps.RegisterCommandInModule("flow", "while", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: while (condition), (body)")
			return BoolStatus(false)
		}

		// Warn if condition is not a ParenGroup, bool true, or from a variable
		// A non-block condition is likely a mistake - the condition should be
		// re-evaluated each iteration, which requires a code block
		_, condIsParenGroup := ctx.Args[0].(ParenGroup)
		isBoolTrue := false
		if b, ok := ctx.Args[0].(bool); ok && b {
			isBoolTrue = true
		}
		condFromVariable := len(ctx.RawArgs) > 0 && strings.HasPrefix(ctx.RawArgs[0], "~")

		if !condIsParenGroup && !isBoolTrue && !condFromVariable {
			ctx.LogWarning(CatCommand, "while condition is not a code block; it will not be re-evaluated each iteration. Use (condition) for dynamic conditions or 'true' for intentional infinite loops")
		}

		// Warn if body is not a ParenGroup or from a variable
		// Someone might accidentally use braces {cmd} instead of parentheses (cmd)
		_, bodyIsParenGroup := ctx.Args[1].(ParenGroup)
		bodyFromVariable := len(ctx.RawArgs) > 1 && strings.HasPrefix(ctx.RawArgs[1], "~")

		if !bodyIsParenGroup && !bodyFromVariable {
			ctx.LogWarning(CatCommand, "while body is not a code block; use (commands) for the loop body, not {commands}")
		}

		conditionBlock := fmt.Sprintf("%v", ctx.Args[0])
		bodyBlock := fmt.Sprintf("%v", ctx.Args[1])

		// Parse body into commands once so we can track position for yields
		parser := NewParser(bodyBlock, "")
		cleanedBody := parser.RemoveComments(bodyBlock)
		normalizedBody := parser.NormalizeKeywords(cleanedBody)
		bodyCommands, err := parser.ParseCommandSequence(normalizedBody)
		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("while: failed to parse body: %v", err))
			return BoolStatus(false)
		}

		maxIterations := 10000
		iterations := 0

		for iterations < maxIterations {
			condResult := ctx.executor.ExecuteWithState(
				conditionBlock,
				ctx.state,
				nil,
				"",
				0, 0,
			)

			if earlyReturn, ok := condResult.(EarlyReturn); ok {
				if earlyReturn.HasResult {
					ctx.SetResult(earlyReturn.Result)
				}
				return earlyReturn.Status
			}

			// Check for yield in condition (unusual but possible)
			if yieldResult, ok := condResult.(YieldResult); ok {
				outerCont := &WhileContinuation{
					ConditionBlock:    conditionBlock,
					BodyBlock:         bodyBlock,
					RemainingBodyCmds: bodyCommands, // Full body since we haven't started
					BodyCmdIndex:      -1,           // -1 indicates yield was in condition
					IterationCount:    iterations,
					State:             ctx.state,
				}
				if yieldResult.WhileContinuation == nil {
					yieldResult.WhileContinuation = outerCont
				} else {
					yieldResult.WhileContinuation.ParentContinuation = outerCont
				}
				return yieldResult
			}

			// Handle async in condition
			shouldContinue := false
			if condToken, isToken := condResult.(TokenResult); isToken {
				tokenID := string(condToken)
				waitChan := make(chan ResumeData, 1)
				ctx.executor.attachWaitChan(tokenID, waitChan)
				resumeData := <-waitChan
				shouldContinue = resumeData.Status
			} else if boolRes, ok := condResult.(BoolStatus); ok {
				shouldContinue = bool(boolRes)
			}

			if !shouldContinue {
				break
			}

			// Execute body commands one at a time to track position for yields
			lastStatus := true
			for cmdIdx, cmd := range bodyCommands {
				if strings.TrimSpace(cmd.Command) == "" {
					continue
				}

				// Apply flow control
				shouldExecute := true
				switch cmd.Separator {
				case "&":
					shouldExecute = lastStatus
				case "|":
					shouldExecute = !lastStatus
				}

				if !shouldExecute {
					continue
				}

				result := ctx.executor.executeParsedCommand(cmd, ctx.state, nil)

				// Check for yield - attach while continuation
				// For nested while loops, chain as parent continuation
				if yieldResult, ok := result.(YieldResult); ok {
					outerCont := &WhileContinuation{
						ConditionBlock:    conditionBlock,
						BodyBlock:         bodyBlock,
						RemainingBodyCmds: bodyCommands[cmdIdx+1:],
						BodyCmdIndex:      cmdIdx,
						IterationCount:    iterations,
						State:             ctx.state,
					}
					if yieldResult.WhileContinuation == nil {
						yieldResult.WhileContinuation = outerCont
					} else {
						// Chain: inner while's continuation gets outer as parent
						yieldResult.WhileContinuation.ParentContinuation = outerCont
					}
					return yieldResult
				}

				// Check for early return
				if earlyReturn, ok := result.(EarlyReturn); ok {
					if earlyReturn.HasResult {
						ctx.SetResult(earlyReturn.Result)
					}
					return earlyReturn.Status
				}

				// Handle async in body
				if bodyToken, isToken := result.(TokenResult); isToken {
					tokenID := string(bodyToken)
					waitChan := make(chan ResumeData, 1)
					ctx.executor.attachWaitChan(tokenID, waitChan)
					resumeData := <-waitChan

					if !resumeData.Status {
						ctx.LogError(CatFlow, "Async operation in while loop failed")
						return BoolStatus(false)
					}
					lastStatus = resumeData.Status
					continue
				}

				if boolRes, ok := result.(BoolStatus); ok {
					lastStatus = bool(boolRes)
				}
			}

			iterations++
		}

		if iterations >= maxIterations {
			ctx.LogError(CatFlow, "Maximum iterations (10000) exceeded")
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	// include - include another source file
	ps.RegisterCommandInModule("core", "include", func(ctx *Context) Result {
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

			// Merge bubbles from included file's state to caller state
			ctx.state.MergeBubbles(execState)

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
}
