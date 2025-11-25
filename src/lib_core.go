package pawscript

import (
	"fmt"
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
		return BoolStatus(false)
	})

	// ret - early return from block
	ps.RegisterCommand("ret", func(ctx *Context) Result {
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

	// get_inferred_type - returns the type of a value
	ps.RegisterCommand("get_inferred_type", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.SetResult("undefined")
			return BoolStatus(true)
		}
		value := ctx.Args[0]
		typeName := getTypeName(value)
		ctx.SetResult(typeName)
		return BoolStatus(true)
	})

	// get_type - returns the type of a variable without fetching its value
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
	ps.RegisterCommand("list", func(ctx *Context) Result {
		setListResult(ctx, NewStoredListWithRefs(ctx.Args, ctx.NamedArgs, ctx.executor))
		return BoolStatus(true)
	})

	// len - returns the length of a list, string, or channel
	ps.RegisterCommand("len", func(ctx *Context) Result {
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
			}
		}

		switch v := value.(type) {
		case StoredList:
			ctx.SetResult(v.Len())
			return BoolStatus(true)
		case *StoredChannel:
			ctx.SetResult(ChannelLen(v))
			return BoolStatus(true)
		case string, QuotedString, Symbol:
			resolved := ctx.executor.resolveValue(v)
			str := fmt.Sprintf("%v", resolved)
			ctx.SetResult(len(str))
			return BoolStatus(true)
		case ParenGroup:
			items, _ := parseArguments(string(v))
			ctx.SetResult(len(items))
			return BoolStatus(true)
		default:
			ctx.LogError(CatType, fmt.Sprintf("Cannot get length of type %s\n", getTypeName(v)))
			ctx.SetResult(0)
			return BoolStatus(false)
		}
	})

	// keys - returns a list of all keys from a list's named arguments
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
				setListResult(ctx, NewStoredList([]interface{}{}))
				return BoolStatus(true)
			}

			keys := make([]string, 0, len(namedArgs))
			for key := range namedArgs {
				keys = append(keys, key)
			}
			sort.Strings(keys)

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
	ps.RegisterCommand("get_val", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: get_val <list>, <key>")
			ctx.SetResult(nil)
			return BoolStatus(false)
		}

		listValue := ctx.Args[0]
		keyValue := ctx.Args[1]

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
				ctx.LogError(CatCommand, "List has no named arguments")
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

	// ==================== macros:: module ====================

	// macro - define a macro
	ps.RegisterCommand("macro", func(ctx *Context) Result {
		ps.logger.Debug("macro command called with %d args", len(ctx.Args))

		// Capture the current module environment for lexical scoping
		macroEnv := NewMacroModuleEnvironment(ctx.state.moduleEnv)

		// Check for anonymous macro: macro (body)
		if len(ctx.Args) == 1 {
			commands := fmt.Sprintf("%v", ctx.Args[0])
			ps.logger.Debug("Creating anonymous macro with commands: %s", commands)

			macro := NewStoredMacroWithEnv(commands, ctx.Position, macroEnv)
			objectID := ctx.executor.storeObject(macro, "macro")
			macroMarker := fmt.Sprintf("\x00MACRO:%d\x00", objectID)
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

		ps.logger.Debug("Successfully defined named macro '%s'", name)
		return BoolStatus(true)
	})

	// call - call a macro or command
	ps.RegisterCommand("call", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: call <macro_name_or_object>, [args...]")
			return BoolStatus(false)
		}

		callArgs := ctx.Args[1:]
		childState := ctx.state.CreateChild()
		firstArg := ctx.Args[0]

		// Check if the first argument is already a resolved StoredCommand object
		if cmd, ok := firstArg.(StoredCommand); ok {
			ps.logger.Debug("Calling resolved StoredCommand object: %s", cmd.CommandName)

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
			ps.logger.Debug("Calling resolved StoredMacro object")

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

			if markerType == "command" && objectID >= 0 {
				ps.logger.Debug("Calling StoredCommand via marker (object %d)", objectID)

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
				ps.logger.Debug("Calling StoredMacro via marker (object %d)", objectID)

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

	// macro_list - list all defined macros
	ps.RegisterCommand("macro_list", func(ctx *Context) Result {
		macros := ps.macroSystem.ListMacros()
		ctx.SetResult(fmt.Sprintf("%v", macros))
		return BoolStatus(true)
	})

	// macro_delete - delete a macro by name
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

	// macro_clear - clear all macros
	ps.RegisterCommand("macro_clear", func(ctx *Context) Result {
		count := ps.macroSystem.ClearMacros()
		ctx.SetResult(fmt.Sprintf("Cleared %d PawScript macros", count))
		return BoolStatus(true)
	})

	// command_ref - get a reference to a built-in or registered command
	ps.RegisterCommand("command_ref", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: command_ref <command_name>")
			return BoolStatus(false)
		}

		commandName := fmt.Sprintf("%v", ctx.Args[0])
		ps.logger.Debug("Getting command reference for: %s", commandName)

		handler, exists := ctx.executor.GetCommand(commandName)
		if !exists {
			ps.logger.Error("Command \"%s\" not found", commandName)
			return BoolStatus(false)
		}

		cmd := NewStoredCommand(commandName, handler)
		objectID := ctx.executor.storeObject(cmd, "command")
		commandMarker := fmt.Sprintf("\x00COMMAND:%d\x00", objectID)
		ctx.state.SetResult(Symbol(commandMarker))

		ps.logger.Debug("Created command reference for '%s' (object %d)", commandName, objectID)
		return BoolStatus(true)
	})

	// ==================== flow:: module ====================

	// while - loop while condition is true
	ps.RegisterCommand("while", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: while (condition), (body)")
			return BoolStatus(false)
		}

		conditionBlock := fmt.Sprintf("%v", ctx.Args[0])
		bodyBlock := fmt.Sprintf("%v", ctx.Args[1])

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

			bodyResult := ctx.executor.ExecuteWithState(
				bodyBlock,
				ctx.state,
				nil,
				"",
				0, 0,
			)

			if earlyReturn, ok := bodyResult.(EarlyReturn); ok {
				if earlyReturn.HasResult {
					ctx.SetResult(earlyReturn.Result)
				}
				return earlyReturn.Status
			}

			if bodyToken, isToken := bodyResult.(TokenResult); isToken {
				tokenID := string(bodyToken)
				waitChan := make(chan ResumeData, 1)
				ctx.executor.attachWaitChan(tokenID, waitChan)
				resumeData := <-waitChan

				if !resumeData.Status {
					ctx.LogError(CatFlow, "Async operation in while loop failed")
					return BoolStatus(false)
				}

				iterations++
				continue
			}

			iterations++
		}

		if iterations >= maxIterations {
			ctx.LogError(CatFlow, "Maximum iterations (10000) exceeded")
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	// ==================== debug:: module ====================

	// mem_stats - debug command to show stored objects
	ps.RegisterCommand("mem_stats", func(ctx *Context) Result {
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
}
