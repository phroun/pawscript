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

		// Resolve the value to get the actual object (for correct struct def detection)
		resolved := ctx.executor.resolveValue(value)
		typeName := getTypeName(resolved)
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
						case "bytes":
							if bytes, ok := obj.(StoredBytes); ok {
								return bytes
							}
						case "struct":
							if s, ok := obj.(StoredStruct); ok {
								return s
							}
						// Note: structdef is now a StoredList, handled by "list" case
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
						case "bytes":
							if bytes, ok := obj.(StoredBytes); ok {
								return bytes
							}
						case "struct":
							if s, ok := obj.(StoredStruct); ok {
								return s
							}
						// Note: structdef is now a StoredList, handled by "list" case
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
		case StoredBytes:
			ctx.SetResult(int64(v.Len()))
			return BoolStatus(true)
		case StoredStruct:
			// For single struct, return the number of fields
			// For struct array, return the number of elements
			if v.IsArray() {
				ctx.SetResult(int64(v.Len()))
			} else {
				// Count fields from the definition list
				// Fields are named args that don't start with __
				fieldCount := 0
				if defObj, ok := ctx.executor.getObject(v.DefID()); ok {
					if defList, ok := defObj.(StoredList); ok {
						if namedArgs := defList.NamedArgs(); namedArgs != nil {
							for key := range namedArgs {
								if !strings.HasPrefix(key, "__") {
									fieldCount++
								}
							}
						}
					}
				}
				ctx.SetResult(int64(fieldCount))
			}
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
	//        bubble (flavor1, flavor2, ...), content [, trace [, memo]]
	// flavor: string key for categorizing bubbles (or list/paren group of flavors)
	// content: any PawScript value
	// trace: boolean (default true) - whether to include stack trace
	// memo: optional string memo
	// When multiple flavors are provided, the SAME bubble entry is added to all flavor lists
	ps.RegisterCommandInModule("core", "bubble", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "bubble requires at least 2 arguments: flavor(s), content")
			return BoolStatus(false)
		}

		// Get flavor(s) - can be single string, list, or parenthetic group
		var flavors []string
		flavorArg := ctx.Args[0]

		// Check if it's a stored list (either as Symbol marker or resolved StoredList)
		if storedList, ok := flavorArg.(StoredList); ok {
			for _, item := range storedList.Items() {
				flavors = append(flavors, fmt.Sprintf("%v", item))
			}
		} else if sym, ok := flavorArg.(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "list" && objectID >= 0 {
				if obj, exists := ctx.executor.getObject(objectID); exists {
					if storedList, ok := obj.(StoredList); ok {
						for _, item := range storedList.Items() {
							flavors = append(flavors, fmt.Sprintf("%v", item))
						}
					}
				}
			}
		} else if str, ok := flavorArg.(string); ok {
			// Check if it's a marker string
			markerType, objectID := parseObjectMarker(str)
			if markerType == "list" && objectID >= 0 {
				if obj, exists := ctx.executor.getObject(objectID); exists {
					if storedList, ok := obj.(StoredList); ok {
						for _, item := range storedList.Items() {
							flavors = append(flavors, fmt.Sprintf("%v", item))
						}
					}
				}
			}
		}

		// Check if it's a paren group
		if pg, ok := flavorArg.(ParenGroup); ok {
			// Parse the paren group contents as comma-separated values
			items, _ := parseArguments(string(pg))
			for _, item := range items {
				flavors = append(flavors, fmt.Sprintf("%v", item))
			}
		}

		// If no flavors extracted yet, treat as single flavor
		if len(flavors) == 0 {
			flavors = []string{fmt.Sprintf("%v", flavorArg)}
		}

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

		// Add bubble to all flavors (same entry shared across all)
		ctx.state.AddBubbleMultiFlavor(flavors, content, trace, memo)
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

	// break - exit from a loop
	// Usage: break        - exit the innermost loop
	// Usage: break <n>    - exit n levels of nested loops
	ps.RegisterCommandInModule("flow", "break", func(ctx *Context) Result {
		levels := 1
		if len(ctx.Args) > 0 {
			if n, ok := toNumber(ctx.Args[0]); ok && n >= 1 {
				levels = int(n)
			}
		}
		return BreakResult{Levels: levels}
	})

	// continue - skip to the next iteration of a loop
	// Usage: continue     - skip to next iteration of innermost loop
	// Usage: continue <n> - skip to next iteration n levels up
	ps.RegisterCommandInModule("flow", "continue", func(ctx *Context) Result {
		levels := 1
		if len(ctx.Args) > 0 {
			if n, ok := toNumber(ctx.Args[0]); ok && n >= 1 {
				levels = int(n)
			}
		}
		return ContinueResult{Levels: levels}
	})

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
				return earlyReturn
			}

			// Check for break in condition - exit loop
			if breakResult, ok := condResult.(BreakResult); ok {
				if breakResult.Levels <= 1 {
					return BoolStatus(true)
				}
				return BreakResult{Levels: breakResult.Levels - 1}
			}

			// Check for continue in condition - treated as continuing to next iteration
			if continueResult, ok := condResult.(ContinueResult); ok {
				if continueResult.Levels <= 1 {
					// Continue in condition doesn't make sense - treat as no-op
					// and continue checking condition
					continue
				}
				return ContinueResult{Levels: continueResult.Levels - 1}
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

				// Check for early return - propagate up to calling context
				if earlyReturn, ok := result.(EarlyReturn); ok {
					return earlyReturn
				}

				// Check for break - exit this loop
				if breakResult, ok := result.(BreakResult); ok {
					if breakResult.Levels <= 1 {
						// Break out of this loop
						return BoolStatus(true)
					}
					// Propagate break with decremented level
					return BreakResult{Levels: breakResult.Levels - 1}
				}

				// Check for continue - skip to next iteration
				if continueResult, ok := result.(ContinueResult); ok {
					if continueResult.Levels <= 1 {
						// Continue this loop - break inner command loop to go to next iteration
						break
					}
					// Propagate continue with decremented level
					return ContinueResult{Levels: continueResult.Levels - 1}
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

	// for - loop over a range, list, generator, or key/value pairs
	// Forms:
	//   for <start>, <end>, <var>, (body)           - numeric range (inclusive)
	//   for <start>, <end>, by: <step>, <var>, (body) - numeric range with step
	//   for ~<iterator>, <var>, (body)              - iterate a generator/list iterator
	//   for ~<list>, <var>, (body)                  - iterate list items
	//   for ~<list>, <var>, order: descending, (body) - iterate in reverse
	//   for ~<list>, <key>, <value>, (body)         - key/value pairs (named args)
	//   for ~<struct>, <key>, <value>, (body)       - struct field names and values
	//   for ~<list>, (<unpack vars>), (body)        - unpack each item
	// Named args:
	//   by: <step>        - step value for numeric ranges
	//   order: ascending|descending - iteration order for lists
	//   iter: <var>       - variable for 1-based iteration number
	//   index: <var>      - variable for 0-based index
	ps.RegisterCommandInModule("flow", "for", func(ctx *Context) Result {
		if len(ctx.Args) < 3 {
			ctx.LogError(CatCommand, "Usage: for <range|iterator>, <var>, (body)")
			return BoolStatus(false)
		}

		// Helper to extract code from a block argument
		extractCode := func(arg interface{}) string {
			switch v := arg.(type) {
			case ParenGroup:
				return string(v)
			case Symbol:
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

		// Helper to check if arg is a token (generator/iterator)
		isToken := func(arg interface{}) bool {
			switch v := arg.(type) {
			case Symbol:
				return strings.HasPrefix(string(v), "\x00TOKEN:")
			case string:
				return strings.HasPrefix(v, "\x00TOKEN:")
			}
			return false
		}

		// Helper to check if arg is a list
		isList := func(arg interface{}) (StoredList, int, bool) {
			switch v := arg.(type) {
			case StoredList:
				id := ctx.executor.storeObject(v, "list")
				return v, id, true
			case ParenGroup:
				// Convert ParenGroup to StoredList
				items, namedArgs := parseArguments(string(v))
				list := NewStoredListWithNamed(items, namedArgs)
				id := ctx.executor.storeObject(list, "list")
				return list, id, true
			case Symbol:
				markerType, objectID := parseObjectMarker(string(v))
				if markerType == "list" && objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						if list, ok := obj.(StoredList); ok {
							return list, objectID, true
						}
					}
				}
			case string:
				markerType, objectID := parseObjectMarker(v)
				if markerType == "list" && objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						if list, ok := obj.(StoredList); ok {
							return list, objectID, true
						}
					}
				}
			}
			return StoredList{}, -1, false
		}

		// Helper to check if arg is a struct
		isStruct := func(arg interface{}) (StoredStruct, bool) {
			switch v := arg.(type) {
			case StoredStruct:
				return v, true
			case Symbol:
				markerType, objectID := parseObjectMarker(string(v))
				if markerType == "struct" && objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						if s, ok := obj.(StoredStruct); ok {
							return s, true
						}
					}
				}
			case string:
				markerType, objectID := parseObjectMarker(v)
				if markerType == "struct" && objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						if s, ok := obj.(StoredStruct); ok {
							return s, true
						}
					}
				}
			}
			return StoredStruct{}, false
		}

		// Helper to check if arg is a ParenGroup (for unpack pattern)
		isParenGroup := func(arg interface{}) (string, bool) {
			if pg, ok := arg.(ParenGroup); ok {
				return string(pg), true
			}
			return "", false
		}

		// Get named args for iter: and index:
		iterVar := ""
		indexVar := ""
		if v, ok := ctx.NamedArgs["iter"]; ok {
			iterVar = fmt.Sprintf("%v", v)
		}
		if v, ok := ctx.NamedArgs["index"]; ok {
			indexVar = fmt.Sprintf("%v", v)
		}

		// Determine which form we're using
		var bodyBlock string
		var iteratorToken string
		var iterVarName string
		var keyVar, valueVar string
		var unpackVars []string
		var isKeyValue bool
		var isUnpack bool
		var iteratorType string
		var isDescending bool

		firstArg := ctx.Args[0]

		// Check for numeric range: two numbers as first args
		startNum, isStart := toFloat64(firstArg)
		if isStart && len(ctx.Args) >= 4 {
			endNum, isEnd := toFloat64(ctx.Args[1])
			if isEnd {
				// Numeric range form: for <start>, <end>, <var>, (body)
				// Or with step: for <start>, <end>, by: <step>, <var>, (body)
				iterVarName = fmt.Sprintf("%v", ctx.Args[2])
				bodyBlock = extractCode(ctx.Args[3])

				// Get step from named args
				step := 1.0
				ascending := endNum >= startNum
				if stepVal, hasStep := ctx.NamedArgs["by"]; hasStep {
					step, _ = toFloat64(stepVal)
					if step == 0 {
						ctx.LogWarning(CatCommand, "for: step is zero; loop will iterate until max iterations")
					} else if (ascending && step < 0) || (!ascending && step > 0) {
						ctx.LogWarning(CatCommand, "for: step direction doesn't match range direction")
					}
				} else if !ascending {
					step = -1
				}

				// Parse body
				parser := NewParser(bodyBlock, "")
				cleanedBody := parser.RemoveComments(bodyBlock)
				normalizedBody := parser.NormalizeKeywords(cleanedBody)
				bodyCommands, err := parser.ParseCommandSequence(normalizedBody)
				if err != nil {
					ctx.LogError(CatCommand, fmt.Sprintf("for: failed to parse body: %v", err))
					return BoolStatus(false)
				}

				// Execute the loop
				maxIterations := 100000
				iterations := 0
				iterNum := 1
				current := startNum

				for iterations < maxIterations {
					// Check termination
					if ascending && step > 0 {
						if current > endNum {
							break
						}
					} else if !ascending && step < 0 {
						if current < endNum {
							break
						}
					} else if step == 0 {
						// Infinite loop potential - will hit max iterations
					}

					// Set iteration variable
					if current == float64(int64(current)) {
						ctx.state.SetVariable(iterVarName, int64(current))
					} else {
						ctx.state.SetVariable(iterVarName, current)
					}

					// Set iter: and index: variables if requested
					if iterVar != "" {
						ctx.state.SetVariable(iterVar, int64(iterNum))
					}
					if indexVar != "" {
						ctx.state.SetVariable(indexVar, int64(iterNum-1))
					}

					// Execute body
					lastStatus := true
					for cmdIdx, cmd := range bodyCommands {
						if strings.TrimSpace(cmd.Command) == "" {
							continue
						}

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

						// Handle yield - this would require ForContinuation
						if yieldResult, ok := result.(YieldResult); ok {
							// Create continuation for resuming
							outerCont := &ForContinuation{
								BodyBlock:         bodyBlock,
								RemainingBodyCmds: bodyCommands[cmdIdx+1:],
								BodyCmdIndex:      cmdIdx,
								IterationNumber:   iterNum,
								IterVar:           iterVarName,
								IterNumVar:        iterVar,
								IndexVar:          indexVar,
								IteratorType:      "numrange",
								State:             ctx.state,
								RangeStart:        startNum,
								RangeEnd:          endNum,
								RangeStep:         step,
								RangeCurrent:      current,
							}
							if yieldResult.ForContinuation == nil {
								yieldResult.ForContinuation = outerCont
							} else {
								yieldResult.ForContinuation.ParentContinuation = outerCont
							}
							return yieldResult
						}

						if earlyReturn, ok := result.(EarlyReturn); ok {
							// Propagate early return up to calling context
							return earlyReturn
						}

						// Check for break - exit this loop
						if breakResult, ok := result.(BreakResult); ok {
							if breakResult.Levels <= 1 {
								// Break out of this loop
								return BoolStatus(true)
							}
							// Propagate break with decremented level
							return BreakResult{Levels: breakResult.Levels - 1}
						}

						// Check for continue - skip to next iteration
						if continueResult, ok := result.(ContinueResult); ok {
							if continueResult.Levels <= 1 {
								// Continue this loop - break inner command loop to go to next iteration
								break
							}
							// Propagate continue with decremented level
							return ContinueResult{Levels: continueResult.Levels - 1}
						}

						if asyncToken, isToken := result.(TokenResult); isToken {
							tokenID := string(asyncToken)
							waitChan := make(chan ResumeData, 1)
							ctx.executor.attachWaitChan(tokenID, waitChan)
							resumeData := <-waitChan
							lastStatus = resumeData.Status
							continue
						}

						if boolRes, ok := result.(BoolStatus); ok {
							lastStatus = bool(boolRes)
						}
					}

					current += step
					iterNum++
					iterations++
				}

				if iterations >= maxIterations {
					ctx.LogError(CatFlow, "Maximum iterations (100000) exceeded")
					return BoolStatus(false)
				}

				return BoolStatus(true)
			}
		}

		// Check for iterator/generator/list/struct forms
		if isToken(firstArg) {
			// Generator/iterator form: for ~<token>, <var>, (body)
			iteratorToken = fmt.Sprintf("%v", firstArg)
			iteratorType = "generator"

			// Check if second arg is a ParenGroup (unpack) or variable name
			if unpackPattern, ok := isParenGroup(ctx.Args[1]); ok {
				// Unpack form
				isUnpack = true
				unpackArgs, _ := parseArguments(unpackPattern)
				for _, arg := range unpackArgs {
					unpackVars = append(unpackVars, fmt.Sprintf("%v", arg))
				}
				bodyBlock = extractCode(ctx.Args[2])
			} else if len(ctx.Args) >= 4 {
				// Check for key-value form: for ~<token>, <key>, <value>, (body)
				// Last arg must be the body
				lastArg := ctx.Args[len(ctx.Args)-1]
				if _, ok := lastArg.(ParenGroup); ok {
					if len(ctx.Args) == 4 {
						// key-value form
						isKeyValue = true
						keyVar = fmt.Sprintf("%v", ctx.Args[1])
						valueVar = fmt.Sprintf("%v", ctx.Args[2])
						bodyBlock = extractCode(ctx.Args[3])
					}
				}
			}

			if !isKeyValue && !isUnpack {
				// Simple iteration: for ~<token>, <var>, (body)
				iterVarName = fmt.Sprintf("%v", ctx.Args[1])
				bodyBlock = extractCode(ctx.Args[2])
			}
		} else if list, listID, ok := isList(firstArg); ok {
			// List form
			_ = list
			_ = listID

			// Check order
			if orderVal, hasOrder := ctx.NamedArgs["order"]; hasOrder {
				orderStr := fmt.Sprintf("%v", orderVal)
				isDescending = (orderStr == "descending" || orderStr == "desc")
			}

			// Check if second arg is a ParenGroup (unpack) or variable name
			if unpackPattern, ok := isParenGroup(ctx.Args[1]); ok {
				// Unpack form
				isUnpack = true
				unpackArgs, _ := parseArguments(unpackPattern)
				for _, arg := range unpackArgs {
					unpackVars = append(unpackVars, fmt.Sprintf("%v", arg))
				}
				bodyBlock = extractCode(ctx.Args[2])
				iteratorType = "list"
			} else if len(ctx.Args) >= 4 {
				// Check for key-value form
				lastArg := ctx.Args[len(ctx.Args)-1]
				if _, ok := lastArg.(ParenGroup); ok {
					if len(ctx.Args) == 4 {
						isKeyValue = true
						keyVar = fmt.Sprintf("%v", ctx.Args[1])
						valueVar = fmt.Sprintf("%v", ctx.Args[2])
						bodyBlock = extractCode(ctx.Args[3])
						iteratorType = "keys"
					}
				}
			}

			if !isKeyValue && !isUnpack {
				iterVarName = fmt.Sprintf("%v", ctx.Args[1])
				bodyBlock = extractCode(ctx.Args[2])
				iteratorType = "list"
			}

			// Create the iterator based on type
			if iteratorType == "keys" {
				// Key-value iteration over named args
				namedArgs := list.NamedArgs()
				keys := make([]string, 0, len(namedArgs))
				for k := range namedArgs {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				if isDescending {
					// Reverse keys
					for i, j := 0, len(keys)-1; i < j; i, j = i+1, j-1 {
						keys[i], keys[j] = keys[j], keys[i]
					}
				}

				// Parse body
				parser := NewParser(bodyBlock, "")
				cleanedBody := parser.RemoveComments(bodyBlock)
				normalizedBody := parser.NormalizeKeywords(cleanedBody)
				bodyCommands, err := parser.ParseCommandSequence(normalizedBody)
				if err != nil {
					ctx.LogError(CatCommand, fmt.Sprintf("for: failed to parse body: %v", err))
					return BoolStatus(false)
				}

				// Execute key-value loop
				iterNum := 1
				for idx, key := range keys {
					ctx.state.SetVariable(keyVar, key)
					ctx.state.SetVariable(valueVar, namedArgs[key])

					if iterVar != "" {
						ctx.state.SetVariable(iterVar, int64(iterNum))
					}
					if indexVar != "" {
						ctx.state.SetVariable(indexVar, int64(idx))
					}

					lastStatus := true
					for _, cmd := range bodyCommands {
						if strings.TrimSpace(cmd.Command) == "" {
							continue
						}
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

						if earlyReturn, ok := result.(EarlyReturn); ok {
							// Propagate early return up to calling context
							return earlyReturn
						}

						// Check for break - exit this loop
						if breakResult, ok := result.(BreakResult); ok {
							if breakResult.Levels <= 1 {
								return BoolStatus(true)
							}
							return BreakResult{Levels: breakResult.Levels - 1}
						}

						// Check for continue - skip to next iteration
						if continueResult, ok := result.(ContinueResult); ok {
							if continueResult.Levels <= 1 {
								break
							}
							return ContinueResult{Levels: continueResult.Levels - 1}
						}

						if asyncToken, isToken := result.(TokenResult); isToken {
							tokenID := string(asyncToken)
							waitChan := make(chan ResumeData, 1)
							ctx.executor.attachWaitChan(tokenID, waitChan)
							resumeData := <-waitChan
							lastStatus = resumeData.Status
							continue
						}

						if boolRes, ok := result.(BoolStatus); ok {
							lastStatus = bool(boolRes)
						}
					}
					iterNum++
				}
				return BoolStatus(true)
			}

			// List iteration (items)
			items := list.Items()
			if isDescending {
				// Reverse items
				reversed := make([]interface{}, len(items))
				for i, item := range items {
					reversed[len(items)-1-i] = item
				}
				items = reversed
			}

			// Parse body
			parser := NewParser(bodyBlock, "")
			cleanedBody := parser.RemoveComments(bodyBlock)
			normalizedBody := parser.NormalizeKeywords(cleanedBody)
			bodyCommands, err := parser.ParseCommandSequence(normalizedBody)
			if err != nil {
				ctx.LogError(CatCommand, fmt.Sprintf("for: failed to parse body: %v", err))
				return BoolStatus(false)
			}

			// Execute list loop
			iterNum := 1
			for idx, item := range items {
				if isUnpack {
					// Unpack the item into multiple variables
					var itemList []interface{}
					switch v := item.(type) {
					case StoredList:
						itemList = v.Items()
					case Symbol:
						markerType, objectID := parseObjectMarker(string(v))
						if markerType == "list" && objectID >= 0 {
							if obj, exists := ctx.executor.getObject(objectID); exists {
								if sl, ok := obj.(StoredList); ok {
									itemList = sl.Items()
								}
							}
						}
					case ParenGroup:
						// Parse as list
						args, _ := parseArguments(string(v))
						itemList = args
					}

					for i, varName := range unpackVars {
						if i < len(itemList) {
							ctx.state.SetVariable(varName, itemList[i])
						} else {
							ctx.state.SetVariable(varName, nil)
						}
					}
				} else {
					ctx.state.SetVariable(iterVarName, item)
				}

				if iterVar != "" {
					ctx.state.SetVariable(iterVar, int64(iterNum))
				}
				if indexVar != "" {
					ctx.state.SetVariable(indexVar, int64(idx))
				}

				lastStatus := true
				for _, cmd := range bodyCommands {
					if strings.TrimSpace(cmd.Command) == "" {
						continue
					}
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

					if earlyReturn, ok := result.(EarlyReturn); ok {
						// Propagate early return up to calling context
						return earlyReturn
					}

					// Check for break - exit this loop
					if breakResult, ok := result.(BreakResult); ok {
						if breakResult.Levels <= 1 {
							return BoolStatus(true)
						}
						return BreakResult{Levels: breakResult.Levels - 1}
					}

					// Check for continue - skip to next iteration
					if continueResult, ok := result.(ContinueResult); ok {
						if continueResult.Levels <= 1 {
							break
						}
						return ContinueResult{Levels: continueResult.Levels - 1}
					}

					if asyncToken, isToken := result.(TokenResult); isToken {
						tokenID := string(asyncToken)
						waitChan := make(chan ResumeData, 1)
						ctx.executor.attachWaitChan(tokenID, waitChan)
						resumeData := <-waitChan
						lastStatus = resumeData.Status
						continue
					}

					if boolRes, ok := result.(BoolStatus); ok {
						lastStatus = bool(boolRes)
					}
				}
				iterNum++
			}
			return BoolStatus(true)

		} else if struc, ok := isStruct(firstArg); ok {
			// Struct form - iterate over field names and values
			if len(ctx.Args) < 4 {
				ctx.LogError(CatCommand, "for: struct iteration requires key and value variables")
				return BoolStatus(false)
			}

			keyVar = fmt.Sprintf("%v", ctx.Args[1])
			valueVar = fmt.Sprintf("%v", ctx.Args[2])
			bodyBlock = extractCode(ctx.Args[3])

			// Check if it's a struct array
			if struc.IsArray() {
				// Struct array - iterate over elements
				parser := NewParser(bodyBlock, "")
				cleanedBody := parser.RemoveComments(bodyBlock)
				normalizedBody := parser.NormalizeKeywords(cleanedBody)
				bodyCommands, err := parser.ParseCommandSequence(normalizedBody)
				if err != nil {
					ctx.LogError(CatCommand, fmt.Sprintf("for: failed to parse body: %v", err))
					return BoolStatus(false)
				}

				iterNum := 1
				for idx := 0; idx < struc.Len(); idx++ {
					elem := struc.Get(idx)
					ctx.state.SetVariable(keyVar, int64(idx))

					// Store element and create marker
					elemID := ctx.executor.storeObject(elem, "struct")
					elemMarker := fmt.Sprintf("\x00STRUCT:%d\x00", elemID)
					ctx.state.SetVariable(valueVar, Symbol(elemMarker))

					if iterVar != "" {
						ctx.state.SetVariable(iterVar, int64(iterNum))
					}
					if indexVar != "" {
						ctx.state.SetVariable(indexVar, int64(idx))
					}

					lastStatus := true
					for _, cmd := range bodyCommands {
						if strings.TrimSpace(cmd.Command) == "" {
							continue
						}
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

						if earlyReturn, ok := result.(EarlyReturn); ok {
							// Propagate early return up to calling context
							return earlyReturn
						}

						// Check for break - exit this loop
						if breakResult, ok := result.(BreakResult); ok {
							if breakResult.Levels <= 1 {
								return BoolStatus(true)
							}
							return BreakResult{Levels: breakResult.Levels - 1}
						}

						// Check for continue - skip to next iteration
						if continueResult, ok := result.(ContinueResult); ok {
							if continueResult.Levels <= 1 {
								break
							}
							return ContinueResult{Levels: continueResult.Levels - 1}
						}

						if asyncToken, isToken := result.(TokenResult); isToken {
							tokenID := string(asyncToken)
							waitChan := make(chan ResumeData, 1)
							ctx.executor.attachWaitChan(tokenID, waitChan)
							resumeData := <-waitChan
							lastStatus = resumeData.Status
							continue
						}

						if boolRes, ok := result.(BoolStatus); ok {
							lastStatus = bool(boolRes)
						}
					}
					iterNum++
				}
				return BoolStatus(true)
			}

			// Single struct - iterate over fields
			// Get field names from definition
			defObj, ok := ctx.executor.getObject(struc.DefID())
			if !ok {
				ctx.LogError(CatCommand, "for: could not get struct definition")
				return BoolStatus(false)
			}
			defList, ok := defObj.(StoredList)
			if !ok {
				ctx.LogError(CatCommand, "for: invalid struct definition")
				return BoolStatus(false)
			}

			namedArgs := defList.NamedArgs()
			keys := make([]string, 0)
			for k := range namedArgs {
				if !strings.HasPrefix(k, "__") {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)

			parser := NewParser(bodyBlock, "")
			cleanedBody := parser.RemoveComments(bodyBlock)
			normalizedBody := parser.NormalizeKeywords(cleanedBody)
			bodyCommands, err := parser.ParseCommandSequence(normalizedBody)
			if err != nil {
				ctx.LogError(CatCommand, fmt.Sprintf("for: failed to parse body: %v", err))
				return BoolStatus(false)
			}

			iterNum := 1
			for idx, fieldName := range keys {
				ctx.state.SetVariable(keyVar, fieldName)
				fieldValue, _ := ctx.executor.getStructFieldValue(struc, fieldName)
				ctx.state.SetVariable(valueVar, fieldValue)

				if iterVar != "" {
					ctx.state.SetVariable(iterVar, int64(iterNum))
				}
				if indexVar != "" {
					ctx.state.SetVariable(indexVar, int64(idx))
				}

				lastStatus := true
				for _, cmd := range bodyCommands {
					if strings.TrimSpace(cmd.Command) == "" {
						continue
					}
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

					if earlyReturn, ok := result.(EarlyReturn); ok {
						// Propagate early return up to calling context
						return earlyReturn
					}

					// Check for break - exit this loop
					if breakResult, ok := result.(BreakResult); ok {
						if breakResult.Levels <= 1 {
							return BoolStatus(true)
						}
						return BreakResult{Levels: breakResult.Levels - 1}
					}

					// Check for continue - skip to next iteration
					if continueResult, ok := result.(ContinueResult); ok {
						if continueResult.Levels <= 1 {
							break
						}
						return ContinueResult{Levels: continueResult.Levels - 1}
					}

					if asyncToken, isToken := result.(TokenResult); isToken {
						tokenID := string(asyncToken)
						waitChan := make(chan ResumeData, 1)
						ctx.executor.attachWaitChan(tokenID, waitChan)
						resumeData := <-waitChan
						lastStatus = resumeData.Status
						continue
					}

					if boolRes, ok := result.(BoolStatus); ok {
						lastStatus = bool(boolRes)
					}
				}
				iterNum++
			}
			return BoolStatus(true)
		}

		// Generator/iterator token form
		if iteratorToken != "" {
			parser := NewParser(bodyBlock, "")
			cleanedBody := parser.RemoveComments(bodyBlock)
			normalizedBody := parser.NormalizeKeywords(cleanedBody)
			bodyCommands, err := parser.ParseCommandSequence(normalizedBody)
			if err != nil {
				ctx.LogError(CatCommand, fmt.Sprintf("for: failed to parse body: %v", err))
				return BoolStatus(false)
			}

			maxIterations := 100000
			iterations := 0
			iterNum := 1

			for iterations < maxIterations {
				// Resume the iterator to get next value
				resumeCode := fmt.Sprintf("resume %s", iteratorToken)
				resumeResult := ctx.executor.ExecuteWithState(resumeCode, ctx.state, nil, "", 0, 0)

				// Check if iterator is exhausted
				if boolRes, ok := resumeResult.(BoolStatus); ok && !bool(boolRes) {
					break
				}

				// Get the yielded value
				var value interface{}
				if ctx.state.HasResult() {
					value = ctx.state.GetResult()
				}

				// Set variables
				if isUnpack {
					var itemList []interface{}
					switch v := value.(type) {
					case StoredList:
						itemList = v.Items()
					case Symbol:
						markerType, objectID := parseObjectMarker(string(v))
						if markerType == "list" && objectID >= 0 {
							if obj, exists := ctx.executor.getObject(objectID); exists {
								if sl, ok := obj.(StoredList); ok {
									itemList = sl.Items()
								}
							}
						}
					}
					for i, varName := range unpackVars {
						if i < len(itemList) {
							ctx.state.SetVariable(varName, itemList[i])
						} else {
							ctx.state.SetVariable(varName, nil)
						}
					}
				} else if isKeyValue {
					// For key-value with generator, expect list [key, value]
					switch v := value.(type) {
					case StoredList:
						items := v.Items()
						if len(items) >= 2 {
							ctx.state.SetVariable(keyVar, items[0])
							ctx.state.SetVariable(valueVar, items[1])
						}
					case Symbol:
						markerType, objectID := parseObjectMarker(string(v))
						if markerType == "list" && objectID >= 0 {
							if obj, exists := ctx.executor.getObject(objectID); exists {
								if sl, ok := obj.(StoredList); ok {
									items := sl.Items()
									if len(items) >= 2 {
										ctx.state.SetVariable(keyVar, items[0])
										ctx.state.SetVariable(valueVar, items[1])
									}
								}
							}
						}
					}
				} else {
					ctx.state.SetVariable(iterVarName, value)
				}

				if iterVar != "" {
					ctx.state.SetVariable(iterVar, int64(iterNum))
				}
				if indexVar != "" {
					ctx.state.SetVariable(indexVar, int64(iterNum-1))
				}

				// Execute body
				lastStatus := true
				for cmdIdx, cmd := range bodyCommands {
					if strings.TrimSpace(cmd.Command) == "" {
						continue
					}
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

					// Handle yield
					if yieldResult, ok := result.(YieldResult); ok {
						outerCont := &ForContinuation{
							BodyBlock:         bodyBlock,
							RemainingBodyCmds: bodyCommands[cmdIdx+1:],
							BodyCmdIndex:      cmdIdx,
							IterationNumber:   iterNum,
							IterVar:           iterVarName,
							IterNumVar:        iterVar,
							IndexVar:          indexVar,
							KeyVar:            keyVar,
							ValueVar:          valueVar,
							UnpackVars:        unpackVars,
							IteratorToken:     iteratorToken,
							IteratorType:      iteratorType,
							IsDescending:      isDescending,
							State:             ctx.state,
						}
						if yieldResult.ForContinuation == nil {
							yieldResult.ForContinuation = outerCont
						} else {
							yieldResult.ForContinuation.ParentContinuation = outerCont
						}
						return yieldResult
					}

					if earlyReturn, ok := result.(EarlyReturn); ok {
						// Propagate early return up to calling context
						return earlyReturn
					}

					// Check for break - exit this loop
					if breakResult, ok := result.(BreakResult); ok {
						if breakResult.Levels <= 1 {
							return BoolStatus(true)
						}
						return BreakResult{Levels: breakResult.Levels - 1}
					}

					// Check for continue - skip to next iteration
					if continueResult, ok := result.(ContinueResult); ok {
						if continueResult.Levels <= 1 {
							break
						}
						return ContinueResult{Levels: continueResult.Levels - 1}
					}

					if asyncToken, isToken := result.(TokenResult); isToken {
						tokenID := string(asyncToken)
						waitChan := make(chan ResumeData, 1)
						ctx.executor.attachWaitChan(tokenID, waitChan)
						resumeData := <-waitChan
						lastStatus = resumeData.Status
						continue
					}

					if boolRes, ok := result.(BoolStatus); ok {
						lastStatus = bool(boolRes)
					}
				}

				iterNum++
				iterations++
			}

			if iterations >= maxIterations {
				ctx.LogError(CatFlow, "Maximum iterations (100000) exceeded")
				return BoolStatus(false)
			}

			return BoolStatus(true)
		}

		ctx.LogError(CatCommand, "for: could not determine iteration form")
		return BoolStatus(false)
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
