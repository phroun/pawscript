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

	// get_inferred_type - returns the type of a value
	ps.RegisterCommandInModule("core", "get_inferred_type", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("core", "get_type", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("core", "list", func(ctx *Context) Result {
		setListResult(ctx, NewStoredListWithRefs(ctx.Args, ctx.NamedArgs, ctx.executor))
		return BoolStatus(true)
	})

	// len - returns the length of a list, string, or channel
	ps.RegisterCommandInModule("core", "len", func(ctx *Context) Result {
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
			ctx.SetResult(v.Len())
			return BoolStatus(true)
		case *StoredChannel:
			ctx.SetResult(ChannelLen(v))
			return BoolStatus(true)
		case StoredString:
			ctx.SetResult(len(string(v)))
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
	ps.RegisterCommandInModule("core", "keys", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("core", "get_val", func(ctx *Context) Result {
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
	ps.RegisterCommandInModule("macros", "macro", func(ctx *Context) Result {
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

		// Create the StoredMacro
		macro := NewStoredMacroWithEnv(commands, ctx.Position, macroEnv)

		// Store in module environment's MacrosModule (with COW)
		ctx.state.moduleEnv.mu.Lock()
		ctx.state.moduleEnv.EnsureMacroRegistryCopied()
		ctx.state.moduleEnv.MacrosModule[name] = &macro
		ctx.state.moduleEnv.mu.Unlock()

		ps.logger.Debug("Successfully defined named macro '%s' in MacrosModule", name)
		return BoolStatus(true)
	})

	// call - call a macro or command
	ps.RegisterCommandInModule("macros", "call", func(ctx *Context) Result {
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
			ps.logger.Debug("Calling StoredCommand via string marker (object %d)", objectID)

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
			ps.logger.Debug("Calling StoredMacro via string marker (object %d)", objectID)

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
		ps.logger.Debug("Calling macro by name: %s", name)

		// Look up macro in module environment (COW - only check MacrosModule)
		var macro *StoredMacro
		ctx.state.moduleEnv.mu.RLock()
		if m, exists := ctx.state.moduleEnv.MacrosModule[name]; exists && m != nil {
			macro = m
		}
		ctx.state.moduleEnv.mu.RUnlock()

		if macro == nil {
			ps.logger.Error("Macro \"%s\" not found", name)
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
			ps.logger.Error("Usage: command_ref <command_name>")
			return BoolStatus(false)
		}

		commandName := fmt.Sprintf("%v", ctx.Args[0])
		ps.logger.Debug("Getting command reference for: %s", commandName)

		handler, exists := ctx.state.moduleEnv.GetCommand(commandName)
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
	// Generator-aware: catches YieldResult and attaches WhileContinuation
	ps.RegisterCommandInModule("flow", "while", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: while (condition), (body)")
			return BoolStatus(false)
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
		output.WriteString(fmt.Sprintf("\n--- Library Restricted (%d) ---\n", len(env.LibraryRestricted)))
		writeLibrarySectionWrapped(&output, env.LibraryRestricted)

		// Item metadata (shows import info) - grouped by source module
		output.WriteString(fmt.Sprintf("\n--- Imported (%d) ---\n", len(env.ItemMetadataModule)))
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
		output.WriteString(fmt.Sprintf("\n--- Modules (%d) ---\n", len(env.LibraryInherited)))
		writeLibrarySectionWrapped(&output, env.LibraryInherited)

		_ = outCtx.WriteToErr(output.String())
		return BoolStatus(true)
	})
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
