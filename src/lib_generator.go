package pawscript

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// RegisterGeneratorLib registers generator and coroutine commands
// Module: core
func (ps *PawScript) RegisterGeneratorLib() {
	// Helper to resolve a token from an argument (marker, Symbol, or string)
	resolveToken := func(ctx *Context, arg interface{}) (string, bool) {
		var tokenStr string
		switch v := arg.(type) {
		case Symbol:
			tokenStr = string(v)
		case string:
			tokenStr = v
		default:
			return "", false
		}

		// Check if it's a token marker (format: \x00TOKEN:tokenID\x00)
		// Token IDs are strings, not integers, so we can't use parseObjectMarker
		if strings.HasPrefix(tokenStr, "\x00TOKEN:") && strings.HasSuffix(tokenStr, "\x00") {
			return tokenStr, true
		}

		return "", false
	}

	// Helper to get token ID from a marker
	getTokenIDFromMarker := func(marker string) string {
		// Token marker format: \x00TOKEN:tokenID\x00
		if !strings.HasPrefix(marker, "\x00TOKEN:") || !strings.HasSuffix(marker, "\x00") {
			return ""
		}
		return marker[len("\x00TOKEN:") : len(marker)-1]
	}

	// generator - Create a generator from a macro without executing it
	// Returns a token that can be resumed to get values
	// generator <macro_name>, [args...]
	ps.RegisterCommandInModule("core", "generator", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: generator <macro_name>, [args...]")
			return BoolStatus(false)
		}

		// Get macro name
		var macroName string
		var macro *StoredMacro

		firstArg := ctx.Args[0]

		// Check if it's already a StoredMacro
		if m, ok := firstArg.(StoredMacro); ok {
			macro = &m
		} else if sym, ok := firstArg.(Symbol); ok {
			// Check for macro marker
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "macro" && objectID >= 0 {
				if obj, exists := ctx.executor.getObject(objectID); exists {
					if m, ok := obj.(StoredMacro); ok {
						macro = &m
					}
				}
			} else {
				macroName = string(sym)
			}
		} else if str, ok := firstArg.(string); ok {
			// Check for macro marker
			markerType, objectID := parseObjectMarker(str)
			if markerType == "macro" && objectID >= 0 {
				if obj, exists := ctx.executor.getObject(objectID); exists {
					if m, ok := obj.(StoredMacro); ok {
						macro = &m
					}
				}
			} else {
				macroName = str
			}
		} else {
			ctx.LogError(CatCommand, "generator: first argument must be a macro name or macro object")
			return BoolStatus(false)
		}

		// Look up macro by name if not already resolved
		if macro == nil && macroName != "" {
			if m, exists := ctx.state.moduleEnv.GetMacro(macroName); exists {
				macro = m
			} else {
				ctx.LogError(CatCommand, fmt.Sprintf("generator: macro '%s' not found", macroName))
				return BoolStatus(false)
			}
		}

		if macro == nil {
			ctx.LogError(CatCommand, "generator: could not resolve macro")
			return BoolStatus(false)
		}

		// Get arguments for the macro
		macroArgs := ctx.Args[1:]

		// Create execution state for the generator
		genState := NewExecutionStateFrom(ctx.state)
		genState.executor = ctx.executor

		// If macro has a captured environment, use it
		if macro.ModuleEnv != nil {
			genState.moduleEnv = NewChildModuleEnvironment(macro.ModuleEnv)
		}

		// Set up module exports
		genState.moduleEnv.mu.Lock()
		genState.moduleEnv.DefaultName = "exports"
		genState.moduleEnv.mu.Unlock()

		// Create a LIST from the arguments and store as $@
		argsList := NewStoredListWithRefs(macroArgs, ctx.NamedArgs, ctx.executor)
		argsListID := ctx.executor.storeObject(argsList, "list")
		argsMarker := fmt.Sprintf("\x00LIST:%d\x00", argsListID)
		genState.SetVariable("$@", Symbol(argsMarker))

		// Parse the macro body into commands
		parser := NewParser(macro.Commands, macro.DefinitionFile)
		cleanedCommand := parser.RemoveComments(macro.Commands)
		normalizedCommand := parser.NormalizeKeywords(cleanedCommand)

		commands, err := parser.ParseCommandSequence(normalizedCommand)
		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("generator: failed to parse macro body: %v", err))
			return BoolStatus(false)
		}

		// Apply position offsets
		lineOffset := macro.DefinitionLine - 1
		columnOffset := macro.DefinitionColumn - 1
		for _, cmd := range commands {
			if cmd.Position != nil {
				cmd.Position.Line += lineOffset
				if cmd.Position.Line == lineOffset+1 {
					cmd.Position.Column += columnOffset
				}
			}
		}

		// Create the token with the generator state and commands
		tokenID := ctx.executor.RequestCompletionToken(
			nil,
			"",
			30*time.Minute, // Generators can be long-lived
			genState,
			ctx.Position,
		)

		// Create the command sequence
		seq := &CommandSequence{
			Type:              "generator",
			RemainingCommands: commands,
			CurrentIndex:      0,
			TotalCommands:     len(commands),
			OriginalCommand:   macro.Commands,
			Timestamp:         time.Now(),
			Position:          ctx.Position,
		}

		// Store the sequence in the token
		ctx.executor.mu.Lock()
		if tokenData, exists := ctx.executor.activeTokens[tokenID]; exists {
			tokenData.CommandSequence = seq
			// Store substitution context for the macro
			// IMPORTANT: MacroContext must be non-nil for $1, $2, etc. substitution to work
			tokenData.SubstitutionCtx = &SubstitutionContext{
				Args:                macroArgs,
				ExecutionState:      genState,
				CurrentLineOffset:   lineOffset,
				CurrentColumnOffset: columnOffset,
				Filename:            macro.DefinitionFile,
				MacroContext: &MacroContext{
					MacroName:        "generator",
					DefinitionFile:   macro.DefinitionFile,
					DefinitionLine:   macro.DefinitionLine,
					DefinitionColumn: macro.DefinitionColumn,
				},
			}
		}
		ctx.executor.mu.Unlock()

		// Create the token marker and store as object
		tokenMarker := fmt.Sprintf("\x00TOKEN:%s\x00", tokenID)

		// Store #token in the generator's state so yield can find it
		genState.SetVariable("#token", Symbol(tokenMarker))

		// Return the token marker as the result
		ctx.SetResult(Symbol(tokenMarker))
		return BoolStatus(true)
	})

	// resume - Resume execution of a suspended token
	// resume <token>
	ps.RegisterCommandInModule("core", "resume", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: resume <token>")
			return BoolStatus(false)
		}

		// Resolve the token
		tokenMarker, ok := resolveToken(ctx, ctx.Args[0])
		if !ok {
			ctx.LogError(CatCommand, "resume: invalid token")
			return BoolStatus(false)
		}

		tokenID := getTokenIDFromMarker(tokenMarker)
		if tokenID == "" {
			ctx.LogError(CatCommand, "resume: could not extract token ID")
			return BoolStatus(false)
		}

		// Get the token data
		ctx.executor.mu.Lock()
		tokenData, exists := ctx.executor.activeTokens[tokenID]
		if !exists {
			ctx.executor.mu.Unlock()
			ctx.LogError(CatCommand, "resume: token not found or exhausted")
			return BoolStatus(false)
		}

		seq := tokenData.CommandSequence
		state := tokenData.ExecutionState
		substCtx := tokenData.SubstitutionCtx
		whileCont := tokenData.WhileContinuation
		iterState := tokenData.IteratorState
		ctx.executor.mu.Unlock()

		// Handle iterator tokens (each, pair) - optimized Go-backed iterators
		if iterState != nil {
			switch iterState.Type {
			case "each":
				// Get the list
				listObj, exists := ctx.executor.getObject(iterState.ListID)
				if !exists {
					ctx.LogError(CatCommand, "resume: iterator list no longer exists")
					// Clean up token
					ctx.executor.mu.Lock()
					delete(ctx.executor.activeTokens, tokenID)
					ctx.executor.mu.Unlock()
					return BoolStatus(false)
				}

				list, ok := listObj.(StoredList)
				if !ok {
					ctx.LogError(CatCommand, "resume: iterator target is not a list")
					ctx.executor.mu.Lock()
					delete(ctx.executor.activeTokens, tokenID)
					ctx.executor.mu.Unlock()
					return BoolStatus(false)
				}

				items := list.Items()
				if iterState.Index >= len(items) {
					// Iterator exhausted - release list reference and delete token
					ctx.executor.decrementObjectRefCount(iterState.ListID)
					ctx.executor.mu.Lock()
					delete(ctx.executor.activeTokens, tokenID)
					ctx.executor.mu.Unlock()

					// Return nil with BoolStatus(false) to indicate exhaustion
					// This allows while (x: {resume ~iter}) pattern to exit cleanly
					ctx.SetResult(nil)
					return BoolStatus(false)
				}

				// Get current item and advance index
				item := items[iterState.Index]
				ctx.executor.mu.Lock()
				if td, exists := ctx.executor.activeTokens[tokenID]; exists {
					td.IteratorState.Index++
				}
				ctx.executor.mu.Unlock()

				ctx.SetResult(item)
				return BoolStatus(true)

			case "pair":
				// Get the list
				listObj, exists := ctx.executor.getObject(iterState.ListID)
				if !exists {
					ctx.LogError(CatCommand, "resume: iterator list no longer exists")
					ctx.executor.mu.Lock()
					delete(ctx.executor.activeTokens, tokenID)
					ctx.executor.mu.Unlock()
					return BoolStatus(false)
				}

				list, ok := listObj.(StoredList)
				if !ok {
					ctx.LogError(CatCommand, "resume: iterator target is not a list")
					ctx.executor.mu.Lock()
					delete(ctx.executor.activeTokens, tokenID)
					ctx.executor.mu.Unlock()
					return BoolStatus(false)
				}

				if iterState.KeyIndex >= len(iterState.Keys) {
					// Iterator exhausted - release list reference and delete token
					ctx.executor.decrementObjectRefCount(iterState.ListID)
					ctx.executor.mu.Lock()
					delete(ctx.executor.activeTokens, tokenID)
					ctx.executor.mu.Unlock()

					// Return nil with BoolStatus(false) to indicate exhaustion
					// This allows while (p: {resume ~iter}) pattern to exit cleanly
					ctx.SetResult(nil)
					return BoolStatus(false)
				}

				// Get current key/value pair
				key := iterState.Keys[iterState.KeyIndex]
				value := list.NamedArgs()[key]

				// Create a list with [key, value]
				pairList := NewStoredList([]interface{}{key, value})
				pairID := ctx.executor.storeObject(pairList, "list")
				pairMarker := fmt.Sprintf("\x00LIST:%d\x00", pairID)

				// Advance index
				ctx.executor.mu.Lock()
				if td, exists := ctx.executor.activeTokens[tokenID]; exists {
					td.IteratorState.KeyIndex++
				}
				ctx.executor.mu.Unlock()

				ctx.SetResult(Symbol(pairMarker))
				return BoolStatus(true)
			}
		}

		// Handle while continuation if present
		if whileCont != nil {
			ps.logger.Debug("resume: handling while continuation, %d remaining body commands", len(whileCont.RemainingBodyCmds))

			// Clear the continuation from the token (we're handling it now)
			ctx.executor.mu.Lock()
			if td, exists := ctx.executor.activeTokens[tokenID]; exists {
				td.WhileContinuation = nil
			}
			ctx.executor.mu.Unlock()

			// Execute remaining body commands from where we left off
			lastStatus := true
			for cmdIdx, cmd := range whileCont.RemainingBodyCmds {
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

				result := ctx.executor.executeParsedCommand(cmd, state, substCtx)

				// Check for yield within remaining body
				if yieldResult, ok := result.(YieldResult); ok {
					ps.logger.Debug("resume: yield in while continuation body, value: %v", yieldResult.Value)

					// Store new continuation for next resume
					ctx.executor.mu.Lock()
					if td, exists := ctx.executor.activeTokens[tokenID]; exists {
						td.WhileContinuation = &WhileContinuation{
							ConditionBlock:    whileCont.ConditionBlock,
							BodyBlock:         whileCont.BodyBlock,
							RemainingBodyCmds: whileCont.RemainingBodyCmds[cmdIdx+1:],
							BodyCmdIndex:      whileCont.BodyCmdIndex + cmdIdx + 1,
							IterationCount:    whileCont.IterationCount,
							State:             state,
							SubstitutionCtx:   substCtx,
						}
					}
					ctx.executor.mu.Unlock()

					ctx.SetResult(yieldResult.Value)
					return BoolStatus(true)
				}

				// Check for early return
				if earlyReturn, ok := result.(EarlyReturn); ok {
					ctx.executor.mu.Lock()
					delete(ctx.executor.activeTokens, tokenID)
					ctx.executor.mu.Unlock()

					if earlyReturn.HasResult {
						ctx.SetResult(earlyReturn.Result)
					}
					return earlyReturn.Status
				}

				if boolRes, ok := result.(BoolStatus); ok {
					lastStatus = bool(boolRes)
				}
			}

			// Finished remaining body commands, now continue the while loop
			// Re-parse body for fresh iteration
			parser := NewParser(whileCont.BodyBlock, "")
			cleanedBody := parser.RemoveComments(whileCont.BodyBlock)
			normalizedBody := parser.NormalizeKeywords(cleanedBody)
			bodyCommands, err := parser.ParseCommandSequence(normalizedBody)
			if err != nil {
				ctx.LogError(CatCommand, fmt.Sprintf("resume: failed to parse while body: %v", err))
				return BoolStatus(false)
			}

			maxIterations := 10000
			iterations := whileCont.IterationCount + 1 // Start from next iteration

			for iterations < maxIterations {
				// Check condition
				condResult := ctx.executor.ExecuteWithState(
					whileCont.ConditionBlock,
					state,
					substCtx,
					"",
					0, 0,
				)

				if earlyReturn, ok := condResult.(EarlyReturn); ok {
					// Condition returned early - propagate
					if earlyReturn.HasResult {
						ctx.SetResult(earlyReturn.Result)
					}
					// Don't delete token yet - continue with remaining commands
					break
				}

				// Check for yield in condition
				if yieldResult, ok := condResult.(YieldResult); ok {
					ctx.executor.mu.Lock()
					if td, exists := ctx.executor.activeTokens[tokenID]; exists {
						td.WhileContinuation = &WhileContinuation{
							ConditionBlock:    whileCont.ConditionBlock,
							BodyBlock:         whileCont.BodyBlock,
							RemainingBodyCmds: bodyCommands,
							BodyCmdIndex:      -1,
							IterationCount:    iterations,
							State:             state,
							SubstitutionCtx:   substCtx,
						}
					}
					ctx.executor.mu.Unlock()

					ctx.SetResult(yieldResult.Value)
					return BoolStatus(true)
				}

				shouldContinue := false
				if boolRes, ok := condResult.(BoolStatus); ok {
					shouldContinue = bool(boolRes)
				}

				if !shouldContinue {
					break
				}

				// Execute body commands
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

					result := ctx.executor.executeParsedCommand(cmd, state, substCtx)

					// Check for yield
					if yieldResult, ok := result.(YieldResult); ok {
						ps.logger.Debug("resume: yield in while loop continuation, value: %v", yieldResult.Value)

						ctx.executor.mu.Lock()
						if td, exists := ctx.executor.activeTokens[tokenID]; exists {
							td.WhileContinuation = &WhileContinuation{
								ConditionBlock:    whileCont.ConditionBlock,
								BodyBlock:         whileCont.BodyBlock,
								RemainingBodyCmds: bodyCommands[cmdIdx+1:],
								BodyCmdIndex:      cmdIdx,
								IterationCount:    iterations,
								State:             state,
								SubstitutionCtx:   substCtx,
							}
						}
						ctx.executor.mu.Unlock()

						ctx.SetResult(yieldResult.Value)
						return BoolStatus(true)
					}

					// Check for early return
					if earlyReturn, ok := result.(EarlyReturn); ok {
						ctx.executor.mu.Lock()
						delete(ctx.executor.activeTokens, tokenID)
						ctx.executor.mu.Unlock()

						if earlyReturn.HasResult {
							ctx.SetResult(earlyReturn.Result)
						}
						return earlyReturn.Status
					}

					if boolRes, ok := result.(BoolStatus); ok {
						lastStatus = bool(boolRes)
					}
				}

				iterations++
			}

			// While loop finished, continue with remaining generator commands
			// Fall through to normal command execution
		}

		if seq == nil || len(seq.RemainingCommands) == 0 {
			// Generator is exhausted
			ctx.executor.mu.Lock()
			delete(ctx.executor.activeTokens, tokenID)
			ctx.executor.mu.Unlock()

			// Return the final result from the generator's state
			if state != nil && state.HasResult() {
				ctx.SetResult(state.GetResult())
			}
			return BoolStatus(true)
		}

		// Execute commands until yield, suspend, ret, or end
		ps.logger.Debug("resume: executing generator with %d remaining commands", len(seq.RemainingCommands))

		lastStatus := true
		for i, cmd := range seq.RemainingCommands {
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

			result := ctx.executor.executeParsedCommand(cmd, state, substCtx)

			// Check for yield
			if yieldResult, ok := result.(YieldResult); ok {
				ps.logger.Debug("resume: caught yield with value: %v", yieldResult.Value)

				// Update token's remaining commands to after this yield
				ctx.executor.mu.Lock()
				if tokenData, exists := ctx.executor.activeTokens[tokenID]; exists {
					// If this yield came from a while loop, store the continuation
					if yieldResult.WhileContinuation != nil {
						tokenData.WhileContinuation = yieldResult.WhileContinuation
						tokenData.WhileContinuation.SubstitutionCtx = substCtx
						// Keep the while command in remaining (don't advance past it)
						// so that when continuation finishes, we continue after the while
						tokenData.CommandSequence.RemainingCommands = seq.RemainingCommands[i+1:]
						tokenData.CommandSequence.CurrentIndex += i + 1
					} else {
						tokenData.CommandSequence.RemainingCommands = seq.RemainingCommands[i+1:]
						tokenData.CommandSequence.CurrentIndex += i + 1
					}
				}
				ctx.executor.mu.Unlock()

				// Return the yielded value
				ctx.SetResult(yieldResult.Value)
				return BoolStatus(true)
			}

			// Check for suspend (returns new token)
			if _, ok := result.(SuspendResult); ok {
				ps.logger.Debug("resume: caught suspend")

				// Create a new token for the remaining commands
				newTokenID := ctx.executor.RequestCompletionToken(
					nil,
					"",
					30*time.Minute,
					state,
					cmd.Position,
				)

				// Store remaining commands in new token
				ctx.executor.mu.Lock()
				if newTokenData, exists := ctx.executor.activeTokens[newTokenID]; exists {
					newTokenData.CommandSequence = &CommandSequence{
						Type:              "generator",
						RemainingCommands: seq.RemainingCommands[i+1:],
						CurrentIndex:      seq.CurrentIndex + i + 1,
						TotalCommands:     seq.TotalCommands,
						OriginalCommand:   seq.OriginalCommand,
						Timestamp:         time.Now(),
						Position:          cmd.Position,
					}
					newTokenData.SubstitutionCtx = substCtx
				}
				// Remove the old token
				delete(ctx.executor.activeTokens, tokenID)
				ctx.executor.mu.Unlock()

				// Return the new token
				newTokenMarker := fmt.Sprintf("\x00TOKEN:%s\x00", newTokenID)
				state.SetVariable("#token", Symbol(newTokenMarker))
				ctx.SetResult(Symbol(newTokenMarker))
				return BoolStatus(true)
			}

			// Check for early return (ret command)
			if earlyReturn, ok := result.(EarlyReturn); ok {
				ps.logger.Debug("resume: caught early return")

				// Generator is done - remove token
				ctx.executor.mu.Lock()
				delete(ctx.executor.activeTokens, tokenID)
				ctx.executor.mu.Unlock()

				// Return the result
				if earlyReturn.HasResult {
					ctx.SetResult(earlyReturn.Result)
				} else if state.HasResult() {
					ctx.SetResult(state.GetResult())
				}
				return earlyReturn.Status
			}

			// Check for async (TokenResult) - not fully supported yet
			if _, ok := result.(TokenResult); ok {
				ctx.LogError(CatCommand, "resume: async operations inside generators not yet supported")
				return BoolStatus(false)
			}

			lastStatus = bool(result.(BoolStatus))
			state.SetLastStatus(lastStatus)
		}

		// Generator finished all commands - remove token
		ctx.executor.mu.Lock()
		delete(ctx.executor.activeTokens, tokenID)
		ctx.executor.mu.Unlock()

		// Return final result
		if state.HasResult() {
			ctx.SetResult(state.GetResult())
		}
		return BoolStatus(lastStatus)
	})

	// yield - Yield a value from a generator, pausing execution
	// yield [token], <value>
	ps.RegisterCommandInModule("core", "yield", func(ctx *Context) Result {
		var value interface{}
		var tokenID string

		switch len(ctx.Args) {
		case 1:
			// yield <value> - use #token from local state
			value = ctx.Args[0]
			if tokenVar, exists := ctx.state.GetVariable("#token"); exists {
				if marker, ok := resolveToken(ctx, tokenVar); ok {
					tokenID = getTokenIDFromMarker(marker)
				}
			}
		case 2:
			// yield <token>, <value>
			if marker, ok := resolveToken(ctx, ctx.Args[0]); ok {
				tokenID = getTokenIDFromMarker(marker)
			}
			value = ctx.Args[1]
		default:
			ctx.LogError(CatCommand, "Usage: yield [token], <value>")
			return BoolStatus(false)
		}

		if tokenID == "" {
			ctx.LogError(CatCommand, "yield: no valid token found (use #token or provide explicitly)")
			return BoolStatus(false)
		}

		// Return YieldResult - the executor loop will catch this
		return YieldResult{
			Value:   value,
			TokenID: tokenID,
		}
	})

	// suspend - Suspend execution and return a token to the caller
	// suspend
	ps.RegisterCommandInModule("core", "suspend", func(ctx *Context) Result {
		// Return SuspendResult - the executor loop will catch this
		// and create a new token with the remaining commands
		return SuspendResult{}
	})

	// token_valid - Check if a token is still valid (not exhausted)
	// token_valid <token>
	// Returns BoolStatus(true/false) based on whether the token exists
	ps.RegisterCommandInModule("core", "token_valid", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		tokenMarker, ok := resolveToken(ctx, ctx.Args[0])
		if !ok {
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		tokenID := getTokenIDFromMarker(tokenMarker)
		if tokenID == "" {
			ctx.SetResult(false)
			return BoolStatus(false)
		}

		// Check if token exists
		ctx.executor.mu.RLock()
		_, exists := ctx.executor.activeTokens[tokenID]
		ctx.executor.mu.RUnlock()

		ctx.SetResult(exists)
		return BoolStatus(exists)
	})

	// Helper to resolve a value to a list and get its object ID
	resolveListWithID := func(ctx *Context, val interface{}) (StoredList, int, bool) {
		switch v := val.(type) {
		case StoredList:
			// Direct list - need to store it first
			id := ctx.executor.storeObject(v, "list")
			return v, id, true
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

	// each - Create an iterator that yields each positional item from a list
	// each <list>
	ps.RegisterCommandInModule("core", "each", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: each <list>")
			return BoolStatus(false)
		}

		// Resolve the list
		list, listID, ok := resolveListWithID(ctx, ctx.Args[0])
		if !ok {
			ctx.LogError(CatCommand, "each: argument must be a list")
			return BoolStatus(false)
		}

		// Check if list has any items
		if len(list.Items()) == 0 {
			// Empty list - return a marker for "no items"
			ctx.SetResult(nil)
			return BoolStatus(true)
		}

		// Claim reference to the list so it's not GC'd while iterating
		ctx.executor.incrementObjectRefCount(listID)

		// Create a token for the iterator
		tokenID := ctx.executor.RequestCompletionToken(
			nil,
			"",
			30*time.Minute,
			ctx.state,
			ctx.Position,
		)

		// Store iterator state in the token
		ctx.executor.mu.Lock()
		if tokenData, exists := ctx.executor.activeTokens[tokenID]; exists {
			tokenData.IteratorState = &IteratorState{
				Type:   "each",
				ListID: listID,
				Index:  0,
			}
		}
		ctx.executor.mu.Unlock()

		// Return the token marker
		tokenMarker := fmt.Sprintf("\x00TOKEN:%s\x00", tokenID)
		ctx.SetResult(Symbol(tokenMarker))
		return BoolStatus(true)
	})

	// pair - Create an iterator that yields key/value pairs from a list's named arguments
	// pair <list>
	ps.RegisterCommandInModule("core", "pair", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: pair <list>")
			return BoolStatus(false)
		}

		// Resolve the list
		list, listID, ok := resolveListWithID(ctx, ctx.Args[0])
		if !ok {
			ctx.LogError(CatCommand, "pair: argument must be a list")
			return BoolStatus(false)
		}

		namedArgs := list.NamedArgs()
		if len(namedArgs) == 0 {
			// No named args - return nil
			ctx.SetResult(nil)
			return BoolStatus(true)
		}

		// Get sorted keys for deterministic iteration
		keys := make([]string, 0, len(namedArgs))
		for k := range namedArgs {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		// Claim reference to the list so it's not GC'd while iterating
		ctx.executor.incrementObjectRefCount(listID)

		// Create a token for the iterator
		tokenID := ctx.executor.RequestCompletionToken(
			nil,
			"",
			30*time.Minute,
			ctx.state,
			ctx.Position,
		)

		// Store iterator state in the token
		ctx.executor.mu.Lock()
		if tokenData, exists := ctx.executor.activeTokens[tokenID]; exists {
			tokenData.IteratorState = &IteratorState{
				Type:     "pair",
				ListID:   listID,
				Keys:     keys,
				KeyIndex: 0,
			}
		}
		ctx.executor.mu.Unlock()

		// Return the token marker
		tokenMarker := fmt.Sprintf("\x00TOKEN:%s\x00", tokenID)
		ctx.SetResult(Symbol(tokenMarker))
		return BoolStatus(true)
	})
}
