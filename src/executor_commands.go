package pawscript

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// executeCommandSequence executes a sequence of commands
func (e *Executor) executeCommandSequence(commands []*ParsedCommand, state *ExecutionState, substitutionCtx *SubstitutionContext) Result {
	lastStatus := true // Default to true for leading operators

	for i, cmd := range commands {
		if strings.TrimSpace(cmd.Command) == "" {
			continue
		}

		// Apply flow control based on separator
		shouldExecute := true

		switch cmd.Separator {
		case "&": // AND: execute only if last command succeeded
			shouldExecute = lastStatus
		case "|": // OR: execute only if last command failed
			shouldExecute = !lastStatus
		}

		if !shouldExecute {
			e.logger.Debug("Skipping command \"%s\" due to flow control (separator: %s, lastStatus: %v)",
				cmd.Command, cmd.Separator, lastStatus)
			continue
		}

		result := e.executeParsedCommand(cmd, state, substitutionCtx)

		// Check for early return
		if earlyReturn, ok := result.(EarlyReturn); ok {
			e.logger.Debug("Command returned early return, terminating sequence")
			// Set the result if provided
			if earlyReturn.HasResult {
				state.SetResult(earlyReturn.Result)
			}
			// Return the EarlyReturn itself so callers can propagate it
			return earlyReturn
		}

		// Check for yield (from generator) - bubble up as EarlyReturn
		if yieldResult, ok := result.(YieldResult); ok {
			e.logger.Debug("Command returned yield, bubbling up with value: %v", yieldResult.Value)
			// Return the yield result so the resume handler can catch it
			return yieldResult
		}

		// Check for suspend - create token with remaining commands and return it
		if _, ok := result.(SuspendResult); ok {
			e.logger.Debug("Command returned suspend, creating token for remaining commands")

			remainingCommands := commands[i+1:]

			// Create a token for the suspension
			suspendToken := e.RequestCompletionToken(
				nil,
				"",
				30*time.Minute,
				state,
				cmd.Position,
			)

			// Store remaining commands in the token
			if len(remainingCommands) > 0 {
				e.mu.Lock()
				if tokenData, exists := e.activeTokens[suspendToken]; exists {
					tokenData.CommandSequence = &CommandSequence{
						Type:              "generator",
						RemainingCommands: remainingCommands,
						CurrentIndex:      i + 1,
						TotalCommands:     len(commands),
						OriginalCommand:   "suspend",
						Timestamp:         time.Now(),
						Position:          cmd.Position,
					}
					tokenData.SubstitutionCtx = substitutionCtx
				}
				e.mu.Unlock()
			}

			// Create token marker and store in state's #token
			tokenMarker := fmt.Sprintf("\x00TOKEN:%s\x00", suspendToken)
			state.SetVariable("#token", Symbol(tokenMarker))

			// Return as EarlyReturn with the token marker
			state.SetResult(Symbol(tokenMarker))
			return EarlyReturn{
				Status:    BoolStatus(true),
				Result:    Symbol(tokenMarker),
				HasResult: true,
			}
		}

		if tokenResult, ok := result.(TokenResult); ok {
			e.logger.Debug("Command returned token %s, setting up sequence continuation", string(tokenResult))

			remainingCommands := commands[i+1:]
			if len(remainingCommands) > 0 {
				sequenceToken := e.RequestCompletionToken(
					func(tokenID string) {
						e.logger.Debug("Cleaning up suspended sequence for token %s", tokenID)
					},
					"",
					5*time.Minute,
					state,
					cmd.Position,
				)

				err := e.PushCommandSequence(sequenceToken, "sequence", remainingCommands, i+1, "sequence", state, cmd.Position)
				if err != nil {
					e.logger.Error("Failed to push command sequence: %v", err)
					return BoolStatus(false)
				}

				e.chainTokens(string(tokenResult), sequenceToken)
				return TokenResult(sequenceToken)
			}
			return result
		}

		lastStatus = bool(result.(BoolStatus))
		state.SetLastStatus(lastStatus)
	}

	return BoolStatus(lastStatus)
}

// executeParsedCommand executes a single parsed command
func (e *Executor) executeParsedCommand(parsedCmd *ParsedCommand, state *ExecutionState, substitutionCtx *SubstitutionContext) Result {
	return e.executeSingleCommand(parsedCmd.Command, state, substitutionCtx, parsedCmd.Position)
}

// executeSingleCommand executes a single command with substitution
func (e *Executor) executeSingleCommand(
	commandStr string,
	state *ExecutionState,
	substitutionCtx *SubstitutionContext,
	position *SourcePosition,
) Result {
	commandStr = strings.TrimSpace(commandStr)

	// Check for ! prefix (inversion operator)
	// This inverts the success status (BoolStatus), not the result value
	shouldInvert := false
	if strings.HasPrefix(commandStr, "!") {
		shouldInvert = true
		commandStr = strings.TrimSpace(commandStr[1:]) // Strip ! and trim again
		e.logger.Debug("Detected ! operator, will invert success status")
	}

	// Check for parenthesis block - execute in same scope
	// BUT first check if this is an unpacking assignment like (a, b, c): values
	if strings.HasPrefix(commandStr, "(") && strings.HasSuffix(commandStr, ")") {
		// Check if this is actually an unpacking assignment pattern
		if _, _, isAssign := e.parseAssignment(commandStr); !isAssign {
			blockContent := commandStr[1 : len(commandStr)-1]

			e.logger.Debug("Executing parenthesis block in same scope: (%s)", blockContent)

			// Execute block content in the SAME state (no child scope)
			result := e.ExecuteWithState(
				blockContent,
				state, // Same state, not a child
				substitutionCtx,
				position.Filename,
				0, 0,
			)

			// Apply inversion if needed
			if shouldInvert {
				return e.invertStatus(result, state, position)
			}
			return result
		}
		// Fall through to handle as assignment later
	}

	// Apply syntactic sugar
	commandStr = e.applySyntacticSugar(commandStr)

	e.logger.Debug("executeSingleCommand called with: \"%s\"", commandStr)

	// CRITICAL: Always evaluate brace expressions, even when not in a macro context
	// Create a minimal substitution context if one doesn't exist
	if substitutionCtx == nil {
		filename := ""
		lineOffset := 0
		columnOffset := 0
		if position != nil {
			filename = position.Filename
			// Initialize offsets from command position so brace expressions
			// report correct file positions
			lineOffset = position.Line - 1
			columnOffset = position.Column - 1
		}
		substitutionCtx = &SubstitutionContext{
			Args:                []interface{}{},
			ExecutionState:      state,
			ParentContext:       nil,
			MacroContext:        nil,
			CurrentLineOffset:   lineOffset,
			CurrentColumnOffset: columnOffset,
			Filename:            filename,
		}
	} else {
		// CRITICAL FIX: Update the execution state to the current one!
		// This ensures brace expressions see the latest results
		substitutionCtx.ExecutionState = state
		// Also update filename if we have position info
		if position != nil && position.Filename != "" {
			substitutionCtx.Filename = position.Filename
		}
	}

	// Apply substitution (which includes brace expressions)
	commandStr = e.applySubstitution(commandStr, substitutionCtx)

	// Store the brace failure count for get_substatus, but only if:
	// 1. Braces were evaluated in this command
	// 2. We're not inside a brace expression (avoid inner commands overwriting outer count)
	if substitutionCtx.BracesEvaluated > 0 && !state.InBraceExpression {
		state.SetLastBraceFailureCount(substitutionCtx.BraceFailureCount)
	}

	// Check if brace evaluation failed
	if commandStr == "\x00PAWS_FAILED\x00" {
		// Error already logged by ExecuteWithState with correct position
		e.logger.Debug("Brace evaluation failed, returning false")
		result := BoolStatus(false)
		if shouldInvert {
			return BoolStatus(!bool(result))
		}
		return result
	}

	// Check if substitution returned an async brace marker
	if strings.HasPrefix(commandStr, "\x00PAWS:") && strings.HasSuffix(commandStr, "\x00") {
		// Extract the coordinator token ID
		markerLen := len("\x00PAWS:")
		coordinatorToken := commandStr[markerLen : len(commandStr)-1]

		e.logger.Debug("Async brace evaluation detected, coordinator token: %s", coordinatorToken)

		// We need to update the coordinator's resume callback to continue this command
		e.mu.Lock()
		if coordData, exists := e.activeTokens[coordinatorToken]; exists && coordData.BraceCoordinator != nil {
			// Store state and context for later
			capturedState := state
			capturedPosition := position
			capturedShouldInvert := shouldInvert
			capturedSubstitutionCtx := substitutionCtx

			// Get the evaluations so we can access their positions
			evaluations := coordData.BraceCoordinator.Evaluations

			// Update the resume callback to continue command execution
			coordData.BraceCoordinator.ResumeCallback = func(finalString string, success bool) Result {
				if !success {
					// Error already logged by ExecuteWithState with correct position
					// Just debug log which brace failed
					e.logger.Debug("Brace evaluation failed, command cannot execute")
					for i, eval := range evaluations {
						if eval.Failed && eval.Position != nil {
							e.logger.Debug("Failed brace %d was at line %d, column %d",
								i, eval.Position.Line, eval.Position.Column)
						}
					}
					result := BoolStatus(false)
					if capturedShouldInvert {
						return BoolStatus(!bool(result))
					}
					return result
				}

				e.logger.Debug("Brace coordinator resumed with substituted string: %s", finalString)

				// Check for assignment pattern (target: value)
				if target, valueStr, isAssign := e.parseAssignment(finalString); isAssign {
					e.logger.Debug("Detected assignment in async resume: target=%s, value=%s", target, valueStr)
					result := e.handleAssignment(target, valueStr, capturedState, capturedSubstitutionCtx, capturedPosition)
					if capturedShouldInvert {
						return e.invertStatus(result, capturedState, capturedPosition)
					}
					return result
				}

				// Check for tilde expression (pure value expression as command)
				if strings.HasPrefix(finalString, "~") {
					e.logger.Debug("Detected tilde expression in async resume: %s", finalString)
					value, ok := e.resolveTildeExpression(finalString, capturedState, capturedSubstitutionCtx, capturedPosition)
					if ok {
						capturedState.SetResult(value)
						if capturedShouldInvert {
							return BoolStatus(false)
						}
						return BoolStatus(true)
					}
					// Tilde resolution failed, error already logged
					if capturedShouldInvert {
						return BoolStatus(true)
					}
					return BoolStatus(false)
				}

				// Now parse and execute the command with the substituted string
				cmdName, args, namedArgs := ParseCommand(finalString)

				// Capture raw args before resolution (preserve parens for ParenGroups)
				rawArgs := make([]string, len(args))
				for i, arg := range args {
					if _, ok := arg.(ParenGroup); ok {
						rawArgs[i] = fmt.Sprintf("(%v)", arg)
					} else {
						rawArgs[i] = fmt.Sprintf("%v", arg)
					}
				}

				// Process arguments to resolve any LIST markers and tilde expressions
				args = e.processArguments(args, capturedState, capturedSubstitutionCtx, capturedPosition)

				e.logger.Debug("Parsed as - Command: \"%s\", Args: %v", cmdName, args)

				// Check for super commands first
				if result, handled := e.executeSuperCommand(cmdName, args, namedArgs, capturedState, capturedPosition); handled {
					if capturedShouldInvert {
						return e.invertStatus(result, capturedState, capturedPosition)
					}
					return result
				}

				// Check for macros in module environment
				if macro, exists := capturedState.moduleEnv.GetMacro(cmdName); exists {
					e.logger.Debug("Found macro \"%s\" in module environment", cmdName)
					result := e.executeMacro(macro, args, namedArgs, capturedState, capturedPosition)
					if capturedShouldInvert {
						return e.invertStatus(result, capturedState, capturedPosition)
					}
					return result
				}

				// Check for commands in module environment
				if handler, exists := capturedState.moduleEnv.GetCommand(cmdName); exists {
					e.logger.Debug("Found command \"%s\" in module environment", cmdName)
					ctx := e.createContext(args, rawArgs, namedArgs, capturedState, capturedPosition)
					result := handler(ctx)
					if capturedShouldInvert {
						return e.invertStatus(result, capturedState, capturedPosition)
					}
					return result
				}

				// Try fallback handler if command not found
				if e.fallbackHandler != nil {
					e.logger.Debug("Command \"%s\" not found, trying fallback handler", cmdName)
					fallbackResult := e.fallbackHandler(cmdName, args, namedArgs, capturedState, capturedPosition)
					if fallbackResult != nil {
						e.logger.Debug("Fallback handler returned: %v", fallbackResult)
						if capturedShouldInvert {
							return e.invertStatus(fallbackResult, capturedState, capturedPosition)
						}
						return fallbackResult
					}
				}

				// Command not found
				e.logger.UnknownCommandError(cmdName, capturedPosition, nil)
				result := BoolStatus(false)
				if capturedShouldInvert {
					return BoolStatus(!bool(result))
				}
				return result
			}
			e.mu.Unlock()
		} else {
			e.mu.Unlock()
			e.logger.Error("Coordinator token %s not found or invalid", coordinatorToken)
			result := BoolStatus(false)
			if shouldInvert {
				return BoolStatus(!bool(result))
			}
			return result
		}

		// Return the coordinator token to suspend this command
		return TokenResult(coordinatorToken)
	}

	e.logger.Debug("After substitution: \"%s\"", commandStr)

	// Check for assignment pattern (target: value)
	if target, valueStr, isAssign := e.parseAssignment(commandStr); isAssign {
		e.logger.Debug("Detected assignment: target=%s, value=%s", target, valueStr)
		result := e.handleAssignment(target, valueStr, state, substitutionCtx, position)
		if shouldInvert {
			return e.invertStatus(result, state, position)
		}
		return result
	}

	// Check for tilde expression (pure value expression as command)
	if strings.HasPrefix(commandStr, "~") {
		e.logger.Debug("Detected tilde expression: %s", commandStr)
		value, ok := e.resolveTildeExpression(commandStr, state, substitutionCtx, position)
		if ok {
			state.SetResult(value)
			if shouldInvert {
				return BoolStatus(false)
			}
			return BoolStatus(true)
		}
		// Tilde resolution failed, error already logged
		if shouldInvert {
			return BoolStatus(true)
		}
		return BoolStatus(false)
	}

	// Parse command
	cmdName, args, namedArgs := ParseCommand(commandStr)

	// Capture raw args before resolution (for diagnostic warnings like 'if' with ParenGroup)
	// For ParenGroups, preserve the outer parens so we can tell literals from variables
	rawArgs := make([]string, len(args))
	for i, arg := range args {
		if _, ok := arg.(ParenGroup); ok {
			rawArgs[i] = fmt.Sprintf("(%v)", arg)
		} else {
			rawArgs[i] = fmt.Sprintf("%v", arg)
		}
	}

	// Process arguments to resolve any LIST markers and tilde expressions
	args = e.processArguments(args, state, substitutionCtx, position)

	e.logger.Debug("Parsed as - Command: \"%s\", Args: %v", cmdName, args)

	// Check for super commands first (MODULE, LIBRARY, IMPORT, REMOVE, EXPORT)
	if result, handled := e.executeSuperCommand(cmdName, args, namedArgs, state, position); handled {
		if shouldInvert {
			return e.invertStatus(result, state, position)
		}
		return result
	}

	// Check for macros in module environment
	if macro, exists := state.moduleEnv.GetMacro(cmdName); exists {
		e.logger.Debug("Found macro \"%s\" in module environment", cmdName)
		result := e.executeMacro(macro, args, namedArgs, state, position)
		if shouldInvert {
			return e.invertStatus(result, state, position)
		}
		return result
	}

	// Check for commands in module environment
	if handler, exists := state.moduleEnv.GetCommand(cmdName); exists {
		e.logger.Debug("Found command \"%s\" in module environment", cmdName)
		ctx := e.createContext(args, rawArgs, namedArgs, state, position)
		result := handler(ctx)
		if shouldInvert {
			return e.invertStatus(result, state, position)
		}
		return result
	}

	// Try fallback handler if command not found
	if e.fallbackHandler != nil {
		e.logger.Debug("Command \"%s\" not found, trying fallback handler", cmdName)
		fallbackResult := e.fallbackHandler(cmdName, args, namedArgs, state, position)
		if fallbackResult != nil {
			e.logger.Debug("Fallback handler returned: %v", fallbackResult)
			if shouldInvert {
				return e.invertStatus(fallbackResult, state, position)
			}
			return fallbackResult
		}
	}

	// Command not found
	e.logger.UnknownCommandError(cmdName, position, nil)
	result := BoolStatus(false)
	if shouldInvert {
		return BoolStatus(!bool(result))
	}
	return result
}

// applySyntacticSugar applies syntactic sugar transformations
func (e *Executor) applySyntacticSugar(commandStr string) string {
	spaceIndex := strings.Index(commandStr, " ")
	if spaceIndex == -1 {
		return commandStr
	}

	commandPart := commandStr[:spaceIndex]
	argsPart := commandStr[spaceIndex+1:]

	// Try to match identifier(content) pattern with proper nesting
	argsPart = strings.TrimSpace(argsPart)

	// Check if it starts with identifier followed by optional whitespace and (
	identifierMatch := regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*)\s*\(`).FindStringSubmatch(argsPart)
	if len(identifierMatch) == 0 {
		return commandStr
	}

	identifier := identifierMatch[1]
	startPos := len(identifierMatch[0]) - 1 // Position of opening (

	// Manually find matching closing paren, handling nesting
	runes := []rune(argsPart)
	depth := 0
	inQuote := false
	var quoteChar rune
	endPos := -1

	for i := startPos; i < len(runes); i++ {
		char := runes[i]

		// Handle escape sequences
		if char == '\\' && i+1 < len(runes) {
			i++ // Skip next character
			continue
		}

		// Handle quotes
		if !inQuote && (char == '"' || char == '\'') {
			inQuote = true
			quoteChar = char
			continue
		}
		if inQuote && char == quoteChar {
			inQuote = false
			continue
		}
		if inQuote {
			continue
		}

		// Track parenthesis depth
		if char == '(' {
			depth++
		} else if char == ')' {
			depth--
			if depth == 0 {
				endPos = i
				break
			}
		}
	}

	if endPos == -1 {
		// No matching closing paren found
		return commandStr
	}

	// Extract content between parens
	content := string(runes[startPos+1 : endPos])

	// Get any remaining text after the closing paren
	remainder := ""
	if endPos+1 < len(runes) {
		remainder = string(runes[endPos+1:])
	}

	return fmt.Sprintf("%s '%s', (%s)%s", commandPart, identifier, content, remainder)
}

// processArguments processes arguments array to resolve object markers and tilde expressions
// Resolves LIST, STR, and BLOCK markers to their actual values
// Also resolves ~expr tilde expressions to their variable values
func (e *Executor) processArguments(args []interface{}, state *ExecutionState, substitutionCtx *SubstitutionContext, position *SourcePosition) []interface{} {
	if len(args) == 0 {
		return args
	}

	result := make([]interface{}, len(args))
	for i, arg := range args {
		var markerStr string
		var isMarker bool

		// Check if it's a Symbol or string that might be an object marker or tilde expression
		if sym, ok := arg.(Symbol); ok {
			markerStr = string(sym)
			isMarker = true
			e.logger.Debug("processArguments[%d]: Symbol arg, len=%d, first chars=%q", i, len(markerStr), markerStr[:min(len(markerStr), 20)])
		} else if str, ok := arg.(string); ok {
			markerStr = str
			isMarker = true
			e.logger.Debug("processArguments[%d]: string arg, len=%d, first chars=%q", i, len(markerStr), markerStr[:min(len(markerStr), 20)])
		}

		if isMarker {
			// Check for tilde expression first (~varname)
			if strings.HasPrefix(markerStr, "~") {
				resolved, ok := e.resolveTildeExpression(markerStr, state, substitutionCtx, position)
				if !ok {
					// Tilde resolution failed, error already logged - keep original
					e.logger.Debug("processArguments[%d]: Tilde resolution failed for %q", i, markerStr)
					result[i] = arg
					continue
				}
				e.logger.Debug("processArguments[%d]: Resolved tilde expression %q to %v", i, markerStr, resolved)
				// Update arg to the resolved value and check if it's a marker that needs further resolution
				arg = resolved
				if sym, ok := resolved.(Symbol); ok {
					markerStr = string(sym)
				} else if str, ok := resolved.(string); ok {
					markerStr = str
				} else {
					// Not a string type (e.g., already a StoredList), use directly
					result[i] = resolved
					continue
				}
				// Fall through to check for object markers in the resolved value
			}

			// Check for object marker
			if objType, objID := parseObjectMarker(markerStr); objID >= 0 {
				e.logger.Debug("processArguments[%d]: Detected %s marker with ID %d", i, objType, objID)
				// Retrieve the actual value (doesn't affect refcount)
				if value, exists := e.getObject(objID); exists {
					switch objType {
					case "list":
						// Return as StoredList - this passes the object by reference
						// Don't claim here - the receiving context (SetVariable, etc.) will claim
						result[i] = value
						e.logger.Debug("processArguments[%d]: Resolved list marker to StoredList", i)
					case "str":
						// Keep as marker (pass-by-reference) - don't copy the string
						// The marker will be resolved when needed (display, string ops)
						// Keep the original arg (Symbol or string containing marker)
						result[i] = arg
						e.logger.Debug("processArguments[%d]: Preserved string marker (pass-by-reference)", i)
					case "block":
						// Keep as marker (pass-by-reference) - don't copy the block
						// The marker will be resolved when needed (execution)
						result[i] = arg
						e.logger.Debug("processArguments[%d]: Preserved block marker (pass-by-reference)", i)
					case "channel":
						// Keep as marker (pass-by-reference) - channel identity must be preserved
						result[i] = arg
						e.logger.Debug("processArguments[%d]: Preserved channel marker (pass-by-reference)", i)
					case "fiber":
						// Keep as marker (pass-by-reference) - fiber identity must be preserved
						result[i] = arg
						e.logger.Debug("processArguments[%d]: Preserved fiber marker (pass-by-reference)", i)
					default:
						// For unknown types, keep the marker to preserve reference semantics
						result[i] = arg
						e.logger.Debug("processArguments[%d]: Preserved %s marker (pass-by-reference)", i, objType)
					}
					continue
				} else {
					e.logger.Debug("processArguments[%d]: Object %d not found in store!", i, objID)
				}
			} else {
				e.logger.Debug("processArguments[%d]: Not a valid object marker", i)
			}
		}

		// Not a marker or tilde, keep the original argument
		result[i] = arg
	}

	return result
}

// invertStatus inverts the success status of a Result
// For BoolStatus, it inverts immediately
// For TokenResult, it creates a wrapper token that will invert when the async operation completes
func (e *Executor) invertStatus(result Result, state *ExecutionState, position *SourcePosition) Result {
	if boolStatus, ok := result.(BoolStatus); ok {
		// Invert synchronous result immediately
		inverted := !bool(boolStatus)
		e.logger.Debug("Inverted synchronous result: %v -> %v", bool(boolStatus), inverted)
		return BoolStatus(inverted)
	} else if earlyReturn, ok := result.(EarlyReturn); ok {
		// Invert the status of an early return
		inverted := !bool(earlyReturn.Status)
		e.logger.Debug("Inverted early return status: %v -> %v", bool(earlyReturn.Status), inverted)
		return EarlyReturn{
			Status:    BoolStatus(inverted),
			Result:    earlyReturn.Result,
			HasResult: earlyReturn.HasResult,
		}
	} else if tokenResult, ok := result.(TokenResult); ok {
		// For async result, create wrapper token with inversion flag
		e.logger.Debug("Creating inverter wrapper for async token: %s", string(tokenResult))

		inverterToken := e.RequestCompletionToken(nil, "", 5*time.Minute, state, position)

		// Mark this token for result inversion
		e.mu.Lock()
		if tokenData, exists := e.activeTokens[inverterToken]; exists {
			tokenData.InvertStatus = true
		}
		e.mu.Unlock()

		// Chain the inverter to the original token
		e.chainTokens(string(tokenResult), inverterToken)

		e.logger.Debug("Created inverter token: %s -> %s", string(tokenResult), inverterToken)
		return TokenResult(inverterToken)
	}

	// Unknown result type, return as-is
	return result
}

// executeMacro executes a macro from the module environment
func (e *Executor) executeMacro(
	macro *StoredMacro,
	args []interface{},
	namedArgs map[string]interface{},
	state *ExecutionState,
	position *SourcePosition,
) Result {
	// Create macro context for error tracking
	macroContext := &MacroContext{
		MacroName:        "", // Name not available here
		DefinitionFile:   macro.DefinitionFile,
		DefinitionLine:   macro.DefinitionLine,
		DefinitionColumn: macro.DefinitionColumn,
	}

	if position != nil {
		macroContext.InvocationFile = position.Filename
		macroContext.InvocationLine = position.Line
		macroContext.InvocationColumn = position.Column
		macroContext.ParentMacro = position.MacroContext
	}

	e.logger.Debug("Executing macro defined at %s:%d, called from %s:%d",
		macro.DefinitionFile, macro.DefinitionLine,
		position.Filename, position.Line)

	// Create child state for macro execution
	macroState := NewExecutionStateFrom(state)

	// If macro has a captured environment, use it (create child from captured)
	// Otherwise use the current state's environment
	if macro.ModuleEnv != nil {
		macroState.moduleEnv = NewChildModuleEnvironment(macro.ModuleEnv)
	}

	// Set default module name to "exports" so any EXPORT calls in the macro
	// will export to the "exports" module, which can be merged into caller
	macroState.moduleEnv.mu.Lock()
	macroState.moduleEnv.DefaultName = "exports"
	macroState.moduleEnv.mu.Unlock()

	// Ensure macro state has executor reference
	macroState.executor = e

	// Create a LIST from the arguments (both positional and named) and store it as $@
	argsList := NewStoredListWithRefs(args, namedArgs, e)
	argsListID := e.storeObject(argsList, "list")
	argsMarker := fmt.Sprintf("\x00LIST:%d\x00", argsListID)

	// Store the list marker in the macro state's variables as $@
	macroState.SetVariable("$@", Symbol(argsMarker))

	// Create substitution context for macro arguments
	substitutionContext := &SubstitutionContext{
		Args:                args,
		ExecutionState:      macroState,
		MacroContext:        macroContext,
		CurrentLineOffset:   macro.DefinitionLine - 1,
		CurrentColumnOffset: macro.DefinitionColumn - 1,
		Filename:            macro.DefinitionFile,
	}

	// Execute the macro commands
	result := e.ExecuteWithState(macro.Commands, macroState, substitutionContext,
		macro.DefinitionFile, macro.DefinitionLine-1, macro.DefinitionColumn-1)

	// Merge macro exports into parent's LibraryInherited under "exports" module
	macroState.moduleEnv.mu.RLock()
	if exportsSection, exists := macroState.moduleEnv.ModuleExports["exports"]; exists && len(exportsSection) > 0 {
		state.moduleEnv.mu.Lock()
		// Ensure exports module exists in parent's LibraryInherited
		if state.moduleEnv.LibraryInherited == nil {
			state.moduleEnv.LibraryInherited = make(Library)
		}
		if state.moduleEnv.LibraryInherited["exports"] == nil {
			state.moduleEnv.LibraryInherited["exports"] = make(ModuleSection)
		}
		// Copy all exported items
		for name, item := range exportsSection {
			state.moduleEnv.LibraryInherited["exports"][name] = item
		}
		state.moduleEnv.mu.Unlock()
		e.logger.Debug("Merged %d exports from macro to parent's exports module", len(exportsSection))
	}
	macroState.moduleEnv.mu.RUnlock()

	// Transfer result to parent state
	if macroState.HasResult() {
		state.SetResult(macroState.GetResult())
		e.logger.Debug("Transferred macro result to parent state: %v", macroState.GetResult())
	}

	// Clean up macro state
	macroState.ReleaseAllReferences()

	e.logger.Debug("Macro execution completed with result: %v", result)
	return result
}
