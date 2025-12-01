package pawscript

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
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
			e.logger.DebugCat(CatCommand,"Skipping command \"%s\" due to flow control (separator: %s, lastStatus: %v)",
				cmd.Command, cmd.Separator, lastStatus)
			continue
		}

		result := e.executeParsedCommand(cmd, state, substitutionCtx)

		// Check for early return
		if earlyReturn, ok := result.(EarlyReturn); ok {
			e.logger.DebugCat(CatCommand,"Command returned early return, terminating sequence")
			// Set the result if provided
			if earlyReturn.HasResult {
				state.SetResult(earlyReturn.Result)
			}
			// Return the EarlyReturn itself so callers can propagate it
			return earlyReturn
		}

		// Check for yield (from generator) - bubble up as EarlyReturn
		if yieldResult, ok := result.(YieldResult); ok {
			e.logger.DebugCat(CatCommand,"Command returned yield, bubbling up with value: %v", yieldResult.Value)
			// Return the yield result so the resume handler can catch it
			return yieldResult
		}

		// Check for suspend - create token with remaining commands and return it
		if _, ok := result.(SuspendResult); ok {
			e.logger.DebugCat(CatCommand,"Command returned suspend, creating token for remaining commands")

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
			e.logger.DebugCat(CatCommand,"Command returned token %s, setting up sequence continuation", string(tokenResult))

			remainingCommands := commands[i+1:]
			if len(remainingCommands) > 0 {
				sequenceToken := e.RequestCompletionToken(
					func(tokenID string) {
						e.logger.DebugCat(CatCommand,"Cleaning up suspended sequence for token %s", tokenID)
					},
					"",
					5*time.Minute,
					state,
					cmd.Position,
				)

				err := e.PushCommandSequence(sequenceToken, "sequence", remainingCommands, i+1, "sequence", state, cmd.Position)
				if err != nil {
					e.logger.ErrorCat(CatCommand,"Failed to push command sequence: %v", err)
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
		e.logger.DebugCat(CatCommand,"Detected ! operator, will invert success status")
	}

	// Check for parenthesis block - execute in same scope
	// Supports: (code) or (code) arg1, arg2
	// BUT first check if this is an unpacking assignment like (a, b, c): values
	if strings.HasPrefix(commandStr, "(") {
		// Find matching closing paren
		closeIdx := e.findMatchingParen(commandStr, 0)
		if closeIdx > 0 {
			// Check if this is actually an unpacking assignment pattern
			if _, _, isAssign := e.parseAssignment(commandStr); !isAssign {
				blockContent := commandStr[1:closeIdx]
				argsStr := strings.TrimSpace(commandStr[closeIdx+1:])

				e.logger.DebugCat(CatCommand, "Executing parenthesis block in same scope: (%s) with args: %s", blockContent, argsStr)

				// Create substitution context for block arguments
				blockSubstCtx := substitutionCtx
				if argsStr != "" {
					// Parse the arguments
					_, args, _ := ParseCommand("dummy " + argsStr)
					args = e.processArguments(args, state, substitutionCtx, position)

					// Create args list for $@ and store it
					argsList := NewStoredList(args)
					argsListID := e.storeObject(argsList, "list")
					argsMarker := fmt.Sprintf("\x00LIST:%d\x00", argsListID)

					// Create a minimal MacroContext to enable $1, $2 substitution
					blockMacroCtx := &MacroContext{
						MacroName:      "(block)",
						InvocationFile: position.Filename,
						InvocationLine: position.Line,
					}

					// Create new substitution context with args
					blockSubstCtx = &SubstitutionContext{
						Args:                args,
						ExecutionState:      state,
						ParentContext:       substitutionCtx,
						MacroContext:        blockMacroCtx,
						CurrentLineOffset:   0,
						CurrentColumnOffset: 0,
						Filename:            position.Filename,
					}

					// Set $@ in state for dollar substitution
					state.SetVariable("$@", Symbol(argsMarker))
				}

				// Execute block content in the SAME state (no child scope)
				result := e.ExecuteWithState(
					blockContent,
					state, // Same state, not a child
					blockSubstCtx,
					position.Filename,
					0, 0,
				)

				// Apply inversion if needed
				if shouldInvert {
					return e.invertStatus(result, state, position)
				}
				return result
			}
		}
		// Fall through to handle as assignment later
	}

	// Apply syntactic sugar
	commandStr = e.applySyntacticSugar(commandStr)

	e.logger.DebugCat(CatCommand,"executeSingleCommand called with: \"%s\"", commandStr)

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
		e.logger.DebugCat(CatCommand,"Brace evaluation failed, returning false")
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

		e.logger.DebugCat(CatCommand,"Async brace evaluation detected, coordinator token: %s", coordinatorToken)

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
					e.logger.DebugCat(CatCommand,"Brace evaluation failed, command cannot execute")
					for i, eval := range evaluations {
						if eval.Failed && eval.Position != nil {
							e.logger.DebugCat(CatCommand,"Failed brace %d was at line %d, column %d",
								i, eval.Position.Line, eval.Position.Column)
						}
					}
					result := BoolStatus(false)
					if capturedShouldInvert {
						return BoolStatus(!bool(result))
					}
					return result
				}

				e.logger.DebugCat(CatCommand,"Brace coordinator resumed with substituted string: %s", finalString)

				// Check for assignment pattern (target: value)
				if target, valueStr, isAssign := e.parseAssignment(finalString); isAssign {
					e.logger.DebugCat(CatCommand,"Detected assignment in async resume: target=%s, value=%s", target, valueStr)
					result := e.handleAssignment(target, valueStr, capturedState, capturedSubstitutionCtx, capturedPosition)
					if capturedShouldInvert {
						return e.invertStatus(result, capturedState, capturedPosition)
					}
					return result
				}

				// Check for question expression (existence check as command)
				if strings.HasPrefix(finalString, "?") {
					e.logger.DebugCat(CatCommand, "Detected question expression in async resume: %s", finalString)
					exists := e.resolveQuestionExpression(finalString, capturedState, capturedSubstitutionCtx, capturedPosition)
					capturedState.SetResult(exists)
					if capturedShouldInvert {
						return BoolStatus(!exists)
					}
					return BoolStatus(exists)
				}

				// Check for tilde expression (pure value expression as command)
				// Implicit set_result
				if strings.HasPrefix(finalString, "~") {
					e.logger.DebugCat(CatCommand,"Detected tilde expression in async resume: %s", finalString)
					_, args, _ := ParseCommand("set_result " + finalString)
					args = e.processArguments(args, capturedState, capturedSubstitutionCtx, capturedPosition)
					if len(args) > 0 {
						capturedState.SetResult(args[0])
					}
					if capturedShouldInvert {
						return BoolStatus(false)
					}
					return BoolStatus(true)
				}

				// Check for block marker in command position
				if strings.HasPrefix(finalString, "\x00BLOCK:") {
					endIdx := strings.Index(finalString[1:], "\x00")
					if endIdx >= 0 {
						blockMarker := finalString[:endIdx+2]
						argsStr := strings.TrimSpace(finalString[endIdx+2:])
						if strings.HasPrefix(argsStr, ",") {
							argsStr = strings.TrimSpace(argsStr[1:])
						}
						_, objectID := parseObjectMarker(blockMarker)
						if objectID >= 0 {
							if obj, exists := e.getObject(objectID); exists {
								if storedBlock, ok := obj.(StoredBlock); ok {
									blockSubstCtx := capturedSubstitutionCtx
									if argsStr != "" {
										_, args, _ := ParseCommand("dummy " + argsStr)
										args = e.processArguments(args, capturedState, capturedSubstitutionCtx, capturedPosition)
										argsList := NewStoredList(args)
										argsListID := e.storeObject(argsList, "list")
										argsMarker := fmt.Sprintf("\x00LIST:%d\x00", argsListID)
										blockMacroCtx := &MacroContext{
											MacroName:      "(block)",
											InvocationFile: capturedPosition.Filename,
											InvocationLine: capturedPosition.Line,
										}
										blockSubstCtx = &SubstitutionContext{
											Args:                args,
											ExecutionState:      capturedState,
											ParentContext:       capturedSubstitutionCtx,
											MacroContext:        blockMacroCtx,
											CurrentLineOffset:   0,
											CurrentColumnOffset: 0,
											Filename:            capturedPosition.Filename,
										}
										capturedState.SetVariable("$@", Symbol(argsMarker))
									}
									result := e.ExecuteWithState(
										string(storedBlock),
										capturedState,
										blockSubstCtx,
										capturedPosition.Filename,
										0, 0,
									)
									if capturedShouldInvert {
										return e.invertStatus(result, capturedState, capturedPosition)
									}
									return result
								}
							}
						}
					}
				}

				// Check for macro marker in command position
				if strings.HasPrefix(finalString, "\x00MACRO:") {
					endIdx := strings.Index(finalString[1:], "\x00")
					if endIdx >= 0 {
						macroMarker := finalString[:endIdx+2]
						argsStr := strings.TrimSpace(finalString[endIdx+2:])
						if strings.HasPrefix(argsStr, ",") {
							argsStr = strings.TrimSpace(argsStr[1:])
						}
						_, objectID := parseObjectMarker(macroMarker)
						if objectID >= 0 {
							if obj, exists := e.getObject(objectID); exists {
								if storedMacro, ok := obj.(StoredMacro); ok {
									e.logger.DebugCat(CatCommand, "Executing macro from marker (async resume) with args: %s", argsStr)
									var macroArgs []interface{}
									var namedArgs map[string]interface{}
									if argsStr != "" {
										_, macroArgs, namedArgs = ParseCommand("dummy " + argsStr)
										macroArgs = e.processArguments(macroArgs, capturedState, capturedSubstitutionCtx, capturedPosition)
										namedArgs = e.processNamedArguments(namedArgs, capturedState, capturedSubstitutionCtx, capturedPosition)
									}
									result := e.executeMacro(&storedMacro, macroArgs, namedArgs, capturedState, capturedPosition)
									if capturedShouldInvert {
										return e.invertStatus(result, capturedState, capturedPosition)
									}
									return result
								}
							}
						}
					}
				}

				// Check for parenthetic block in command position
				if strings.HasPrefix(finalString, "(") {
					closeIdx := e.findMatchingParen(finalString, 0)
					if closeIdx > 0 {
						if _, _, isAssign := e.parseAssignment(finalString); !isAssign {
							blockContent := finalString[1:closeIdx]
							argsStr := strings.TrimSpace(finalString[closeIdx+1:])
							e.logger.DebugCat(CatCommand, "Executing parenthetic block (async resume): (%s) with args: %s", blockContent, argsStr)
							blockSubstCtx := capturedSubstitutionCtx
							if argsStr != "" {
								_, args, _ := ParseCommand("dummy " + argsStr)
								args = e.processArguments(args, capturedState, capturedSubstitutionCtx, capturedPosition)
								argsList := NewStoredList(args)
								argsListID := e.storeObject(argsList, "list")
								argsMarker := fmt.Sprintf("\x00LIST:%d\x00", argsListID)
								blockMacroCtx := &MacroContext{
									MacroName:      "(block)",
									InvocationFile: capturedPosition.Filename,
									InvocationLine: capturedPosition.Line,
								}
								blockSubstCtx = &SubstitutionContext{
									Args:                args,
									ExecutionState:      capturedState,
									ParentContext:       capturedSubstitutionCtx,
									MacroContext:        blockMacroCtx,
									CurrentLineOffset:   0,
									CurrentColumnOffset: 0,
									Filename:            capturedPosition.Filename,
								}
								capturedState.SetVariable("$@", Symbol(argsMarker))
							}
							result := e.ExecuteWithState(
								blockContent,
								capturedState,
								blockSubstCtx,
								capturedPosition.Filename,
								0, 0,
							)
							if capturedShouldInvert {
								return e.invertStatus(result, capturedState, capturedPosition)
							}
							return result
						}
					}
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

				e.logger.DebugCat(CatCommand,"Parsed as - Command: \"%s\", Args: %v", cmdName, args)

				// Check for super commands first
				if result, handled := e.executeSuperCommand(cmdName, args, namedArgs, capturedState, capturedPosition); handled {
					if capturedShouldInvert {
						return e.invertStatus(result, capturedState, capturedPosition)
					}
					return result
				}

				// Check for macros in module environment
				if macro, exists := capturedState.moduleEnv.GetMacro(cmdName); exists {
					e.logger.DebugCat(CatCommand,"Found macro \"%s\" in module environment", cmdName)
					result := e.executeMacro(macro, args, namedArgs, capturedState, capturedPosition)
					if capturedShouldInvert {
						return e.invertStatus(result, capturedState, capturedPosition)
					}
					return result
				}

				// Check for commands in module environment
				if handler, exists := capturedState.moduleEnv.GetCommand(cmdName); exists {
					e.logger.DebugCat(CatCommand,"Found command \"%s\" in module environment", cmdName)
					ctx := e.createContext(args, rawArgs, namedArgs, capturedState, capturedPosition)
					result := handler(ctx)
					if capturedShouldInvert {
						return e.invertStatus(result, capturedState, capturedPosition)
					}
					return result
				}

				// Try fallback handler if command not found
				if e.fallbackHandler != nil {
					e.logger.DebugCat(CatCommand,"Command \"%s\" not found, trying fallback handler", cmdName)
					fallbackResult := e.fallbackHandler(cmdName, args, namedArgs, capturedState, capturedPosition)
					if fallbackResult != nil {
						e.logger.DebugCat(CatCommand,"Fallback handler returned: %v", fallbackResult)
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
			e.logger.ErrorCat(CatCommand,"Coordinator token %s not found or invalid", coordinatorToken)
			result := BoolStatus(false)
			if shouldInvert {
				return BoolStatus(!bool(result))
			}
			return result
		}

		// Return the coordinator token to suspend this command
		return TokenResult(coordinatorToken)
	}

	e.logger.DebugCat(CatCommand,"After substitution: \"%s\"", commandStr)

	// Check for assignment pattern (target: value)
	if target, valueStr, isAssign := e.parseAssignment(commandStr); isAssign {
		e.logger.DebugCat(CatCommand,"Detected assignment: target=%s, value=%s", target, valueStr)
		result := e.handleAssignment(target, valueStr, state, substitutionCtx, position)
		if shouldInvert {
			return e.invertStatus(result, state, position)
		}
		return result
	}

	// Check for question expression (existence check as command)
	// Returns true/false based on whether variable/accessor chain exists
	if strings.HasPrefix(commandStr, "?") {
		e.logger.DebugCat(CatCommand, "Detected question expression: %s", commandStr)
		exists := e.resolveQuestionExpression(commandStr, state, substitutionCtx, position)
		state.SetResult(exists)
		if shouldInvert {
			return BoolStatus(!exists)
		}
		return BoolStatus(exists)
	}

	// Check for tilde expression (pure value expression as command)
	// This is an implicit "set_result" - the tilde expression is parsed as an argument
	// with full accessor and unit combination support.
	// For block execution with args, use: {~block}, args
	if strings.HasPrefix(commandStr, "~") {
		e.logger.DebugCat(CatCommand, "Detected tilde expression (implicit set_result): %s", commandStr)
		// Treat as "set_result <expr>" - parse and process as argument
		_, args, _ := ParseCommand("set_result " + commandStr)
		args = e.processArguments(args, state, substitutionCtx, position)
		if len(args) > 0 {
			state.SetResult(args[0])
		}
		if shouldInvert {
			return BoolStatus(false)
		}
		return BoolStatus(true)
	}

	// Check for block marker in command position (e.g., from {~block}, args)
	// This allows block execution via: {~block}, arg1, arg2
	if strings.HasPrefix(commandStr, "\x00BLOCK:") {
		// Find the end of the block marker
		endIdx := strings.Index(commandStr[1:], "\x00")
		if endIdx >= 0 {
			blockMarker := commandStr[:endIdx+2]
			argsStr := strings.TrimSpace(commandStr[endIdx+2:])
			// Remove leading comma if present
			if strings.HasPrefix(argsStr, ",") {
				argsStr = strings.TrimSpace(argsStr[1:])
			}

			_, objectID := parseObjectMarker(blockMarker)
			if objectID >= 0 {
				if obj, exists := e.getObject(objectID); exists {
					if storedBlock, ok := obj.(StoredBlock); ok {
						e.logger.DebugCat(CatCommand, "Executing block from marker with args: %s", argsStr)

						// Create substitution context for block arguments
						blockSubstCtx := substitutionCtx
						if argsStr != "" {
							// Parse the arguments
							_, args, _ := ParseCommand("dummy " + argsStr)
							args = e.processArguments(args, state, substitutionCtx, position)

							// Create args list for $@ and store it
							argsList := NewStoredList(args)
							argsListID := e.storeObject(argsList, "list")
							argsMarker := fmt.Sprintf("\x00LIST:%d\x00", argsListID)

							// Create a minimal MacroContext to enable $1, $2 substitution
							blockMacroCtx := &MacroContext{
								MacroName:      "(block)",
								InvocationFile: position.Filename,
								InvocationLine: position.Line,
							}

							// Create new substitution context with args
							blockSubstCtx = &SubstitutionContext{
								Args:                args,
								ExecutionState:      state,
								ParentContext:       substitutionCtx,
								MacroContext:        blockMacroCtx,
								CurrentLineOffset:   0,
								CurrentColumnOffset: 0,
								Filename:            position.Filename,
							}

							// Set $@ in state for dollar substitution
							state.SetVariable("$@", Symbol(argsMarker))
						}

						// Execute block content in current scope
						result := e.ExecuteWithState(
							string(storedBlock),
							state,
							blockSubstCtx,
							position.Filename,
							0, 0,
						)
						if shouldInvert {
							return e.invertStatus(result, state, position)
						}
						return result
					}
				}
			}
		}
	}

	// Check for macro marker in command position (e.g., from {macro (...)}, args or {~m}, args)
	// This allows macro execution via: {~macro}, arg1, arg2
	if strings.HasPrefix(commandStr, "\x00MACRO:") {
		// Find the end of the macro marker
		endIdx := strings.Index(commandStr[1:], "\x00")
		if endIdx >= 0 {
			macroMarker := commandStr[:endIdx+2]
			argsStr := strings.TrimSpace(commandStr[endIdx+2:])
			// Remove leading comma if present
			if strings.HasPrefix(argsStr, ",") {
				argsStr = strings.TrimSpace(argsStr[1:])
			}

			_, objectID := parseObjectMarker(macroMarker)
			if objectID >= 0 {
				if obj, exists := e.getObject(objectID); exists {
					if storedMacro, ok := obj.(StoredMacro); ok {
						e.logger.DebugCat(CatCommand, "Executing macro from marker with args: %s", argsStr)

						// Parse arguments
						var macroArgs []interface{}
						var namedArgs map[string]interface{}
						if argsStr != "" {
							_, macroArgs, namedArgs = ParseCommand("dummy " + argsStr)
							macroArgs = e.processArguments(macroArgs, state, substitutionCtx, position)
							namedArgs = e.processNamedArguments(namedArgs, state, substitutionCtx, position)
						}

						// Execute the macro
						result := e.executeMacro(&storedMacro, macroArgs, namedArgs, state, position)
						if shouldInvert {
							return e.invertStatus(result, state, position)
						}
						return result
					}
				}
			}
		}
	}

	// Check for parenthetic block in command position after substitution
	// This handles cases where brace substitution produced a parenthetic block
	// e.g., {~y} where y contains (echo "hello")
	if strings.HasPrefix(commandStr, "(") {
		closeIdx := e.findMatchingParen(commandStr, 0)
		if closeIdx > 0 {
			// Check if this is an unpacking assignment pattern
			if _, _, isAssign := e.parseAssignment(commandStr); !isAssign {
				blockContent := commandStr[1:closeIdx]
				argsStr := strings.TrimSpace(commandStr[closeIdx+1:])

				e.logger.DebugCat(CatCommand, "Executing parenthetic block after substitution: (%s) with args: %s", blockContent, argsStr)

				// Create substitution context for block arguments
				blockSubstCtx := substitutionCtx
				if argsStr != "" {
					// Parse the arguments
					_, args, _ := ParseCommand("dummy " + argsStr)
					args = e.processArguments(args, state, substitutionCtx, position)

					// Create args list for $@ and store it
					argsList := NewStoredList(args)
					argsListID := e.storeObject(argsList, "list")
					argsMarker := fmt.Sprintf("\x00LIST:%d\x00", argsListID)

					// Create a minimal MacroContext to enable $1, $2 substitution
					blockMacroCtx := &MacroContext{
						MacroName:      "(block)",
						InvocationFile: position.Filename,
						InvocationLine: position.Line,
					}

					// Create new substitution context with args
					blockSubstCtx = &SubstitutionContext{
						Args:                args,
						ExecutionState:      state,
						ParentContext:       substitutionCtx,
						MacroContext:        blockMacroCtx,
						CurrentLineOffset:   0,
						CurrentColumnOffset: 0,
						Filename:            position.Filename,
					}

					// Set $@ in state for dollar substitution
					state.SetVariable("$@", Symbol(argsMarker))
				}

				// Execute block content in the SAME state (no child scope)
				result := e.ExecuteWithState(
					blockContent,
					state, // Same state, not a child
					blockSubstCtx,
					position.Filename,
					0, 0,
				)

				// Apply inversion if needed
				if shouldInvert {
					return e.invertStatus(result, state, position)
				}
				return result
			}
		}
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

	// Process named argument values the same way
	namedArgs = e.processNamedArguments(namedArgs, state, substitutionCtx, position)

	e.logger.DebugCat(CatCommand,"Parsed as - Command: \"%s\", Args: %v", cmdName, args)

	// Check for super commands first (MODULE, LIBRARY, IMPORT, REMOVE, EXPORT)
	if result, handled := e.executeSuperCommand(cmdName, args, namedArgs, state, position); handled {
		if shouldInvert {
			return e.invertStatus(result, state, position)
		}
		return result
	}

	// Check for macros in module environment
	if macro, exists := state.moduleEnv.GetMacro(cmdName); exists {
		e.logger.DebugCat(CatCommand,"Found macro \"%s\" in module environment", cmdName)
		result := e.executeMacro(macro, args, namedArgs, state, position)
		if shouldInvert {
			return e.invertStatus(result, state, position)
		}
		return result
	}

	// Check for commands in module environment
	if handler, exists := state.moduleEnv.GetCommand(cmdName); exists {
		e.logger.DebugCat(CatCommand,"Found command \"%s\" in module environment", cmdName)
		ctx := e.createContext(args, rawArgs, namedArgs, state, position)
		result := handler(ctx)
		if shouldInvert {
			return e.invertStatus(result, state, position)
		}
		return result
	}

	// Try fallback handler if command not found
	if e.fallbackHandler != nil {
		e.logger.DebugCat(CatCommand,"Command \"%s\" not found, trying fallback handler", cmdName)
		fallbackResult := e.fallbackHandler(cmdName, args, namedArgs, state, position)
		if fallbackResult != nil {
			e.logger.DebugCat(CatCommand,"Fallback handler returned: %v", fallbackResult)
			if shouldInvert {
				return e.invertStatus(fallbackResult, state, position)
			}
			return fallbackResult
		}
	}

	// Command not found - set result to undefined marker and return false status
	// Note: Using UndefinedMarker not Symbol("undefined") because the bare
	// symbol has special handling in SetResult that clears the result
	e.logger.UnknownCommandError(cmdName, position, nil)
	state.SetResult(Symbol(UndefinedMarker))
	if shouldInvert {
		return BoolStatus(true)
	}
	return BoolStatus(false)
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

// splitAccessors splits a string into base and accessor parts
// For "~list.key.sub" returns ("~list", ".key.sub")
// For "\x00LIST:5\x00.key" returns ("\x00LIST:5\x00", ".key")
// For "\x00LIST:5\x00 0 1" returns ("\x00LIST:5\x00", " 0 1")
func splitAccessors(s string) (base string, accessors string) {
	// Check for list marker pattern
	if strings.HasPrefix(s, "\x00") {
		// Find the closing \x00
		endIdx := strings.Index(s[1:], "\x00")
		if endIdx >= 0 {
			base = s[:endIdx+2] // Include both \x00 markers
			accessors = s[endIdx+2:]
			return
		}
	}

	// For tilde expressions, find the first accessor (. or space followed by digit)
	for i := 1; i < len(s); i++ {
		if s[i] == '.' {
			return s[:i], s[i:]
		}
		if s[i] == ' ' {
			// Check if followed by a digit
			j := i + 1
			for j < len(s) && s[j] == ' ' {
				j++
			}
			if j < len(s) && s[j] >= '0' && s[j] <= '9' {
				return s[:i], s[i:]
			}
		}
	}
	return s, ""
}

// findMatchingParen finds the index of the closing paren that matches the opening paren at startIdx
// Returns -1 if no matching paren found
func (e *Executor) findMatchingParen(s string, startIdx int) int {
	if startIdx >= len(s) || s[startIdx] != '(' {
		return -1
	}

	depth := 0
	inQuote := false
	var quoteChar byte

	for i := startIdx; i < len(s); i++ {
		char := s[i]

		// Handle escape sequences
		if char == '\\' && i+1 < len(s) {
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

		// Track paren nesting
		if char == '(' {
			depth++
		} else if char == ')' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// applyAccessorChain applies a chain of accessors to a value
// Accessors: ".key" for named args, " N" for index access
func (e *Executor) applyAccessorChain(value interface{}, accessors string, position *SourcePosition) interface{} {
	if accessors == "" {
		return value
	}

	current := value
	i := 0

	for i < len(accessors) {
		// Skip whitespace
		for i < len(accessors) && accessors[i] == ' ' {
			i++
		}
		if i >= len(accessors) {
			break
		}

		// Resolve current to get the actual list, bytes, or struct
		resolved := e.resolveValue(current)
		list, isList := resolved.(StoredList)
		bytes, isBytes := resolved.(StoredBytes)
		structVal, isStruct := resolved.(StoredStruct)

		if accessors[i] == '.' {
			// Dot accessor for named argument or struct field
			i++ // skip the dot
			if i >= len(accessors) {
				e.logger.ErrorCat(CatList, "Expected key name after dot")
				return Symbol(UndefinedMarker)
			}

			// Collect the key name
			keyStart := i
			for i < len(accessors) && accessors[i] != '.' && accessors[i] != ' ' {
				i++
			}
			key := accessors[keyStart:i]

			if isStruct {
				// Struct field access using definition list
				val, exists := e.getStructFieldValue(structVal, key)
				if !exists {
					e.logger.DebugCat(CatList, "Field '%s' not found in struct", key)
					return Symbol(UndefinedMarker)
				}
				current = val
			} else if isList {
				namedArgs := list.NamedArgs()
				if namedArgs == nil {
					e.logger.DebugCat(CatList, "List has no named arguments, cannot access .%s", key)
					return Symbol(UndefinedMarker)
				}
				val, exists := namedArgs[key]
				if !exists {
					e.logger.DebugCat(CatList, "Named argument '%s' not found in list", key)
					return Symbol(UndefinedMarker)
				}
				current = val
			} else {
				e.logger.ErrorCat(CatList, "Cannot use dot accessor on non-list/non-struct value")
				return Symbol(UndefinedMarker)
			}

		} else if accessors[i] >= '0' && accessors[i] <= '9' {
			// Integer index accessor
			numStart := i
			for i < len(accessors) && accessors[i] >= '0' && accessors[i] <= '9' {
				i++
			}
			// Check for decimal point (error case)
			if i < len(accessors) && accessors[i] == '.' {
				j := i + 1
				if j < len(accessors) && accessors[j] >= '0' && accessors[j] <= '9' {
					e.logger.ErrorCat(CatList, "Non-integer index not allowed")
					return Symbol(UndefinedMarker)
				}
			}
			numStr := accessors[numStart:i]
			idx, err := strconv.Atoi(numStr)
			if err != nil {
				e.logger.ErrorCat(CatList, "Invalid index: %s", numStr)
				return Symbol(UndefinedMarker)
			}

			if isList {
				if idx < 0 || idx >= list.Len() {
					e.logger.DebugCat(CatList, "Index %d out of bounds (list has %d items)", idx, list.Len())
					return Symbol(UndefinedMarker)
				}
				current = list.Get(idx)
			} else if isBytes {
				if idx < 0 || idx >= bytes.Len() {
					e.logger.DebugCat(CatList, "Index %d out of bounds (bytes has %d items)", idx, bytes.Len())
					return Symbol(UndefinedMarker)
				}
				// Return byte as int64
				current = bytes.Get(idx)
			} else if isStruct {
				// Struct array index access
				if !structVal.IsArray() {
					e.logger.ErrorCat(CatList, "Cannot use index accessor on single struct (use dot accessor for fields)")
					return Symbol(UndefinedMarker)
				}
				if idx < 0 || idx >= structVal.Len() {
					e.logger.DebugCat(CatList, "Index %d out of bounds (struct array has %d items)", idx, structVal.Len())
					return Symbol(UndefinedMarker)
				}
				// Return a single struct from the array
				current = structVal.Get(idx)
			} else {
				e.logger.ErrorCat(CatList, "Cannot use index accessor on non-list/non-bytes/non-struct value")
				return Symbol(UndefinedMarker)
			}

		} else {
			// Unknown accessor character, stop
			break
		}
	}

	return current
}

// accessorChainExists checks if a chain of accessors resolves successfully
// Returns true if entire chain exists, false if any part is missing
// This is the existence-check variant of applyAccessorChain (used by ? operator)
func (e *Executor) accessorChainExists(value interface{}, accessors string) bool {
	if accessors == "" {
		return true
	}

	current := value
	i := 0

	for i < len(accessors) {
		// Skip whitespace
		for i < len(accessors) && accessors[i] == ' ' {
			i++
		}
		if i >= len(accessors) {
			break
		}

		// Resolve current to get the actual list, bytes, or struct
		resolved := e.resolveValue(current)
		list, isList := resolved.(StoredList)
		bytes, isBytes := resolved.(StoredBytes)
		structVal, isStruct := resolved.(StoredStruct)

		if accessors[i] == '.' {
			// Dot accessor for named argument or struct field
			i++ // skip the dot
			if i >= len(accessors) {
				return false
			}

			// Collect the key name
			keyStart := i
			for i < len(accessors) && accessors[i] != '.' && accessors[i] != ' ' {
				i++
			}
			key := accessors[keyStart:i]

			if isStruct {
				val, exists := e.getStructFieldValue(structVal, key)
				if !exists {
					return false
				}
				current = val
			} else if isList {
				namedArgs := list.NamedArgs()
				if namedArgs == nil {
					return false
				}
				val, exists := namedArgs[key]
				if !exists {
					return false
				}
				current = val
			} else {
				return false
			}

		} else if accessors[i] >= '0' && accessors[i] <= '9' {
			// Integer index accessor
			numStart := i
			for i < len(accessors) && accessors[i] >= '0' && accessors[i] <= '9' {
				i++
			}
			// Check for decimal point (error case)
			if i < len(accessors) && accessors[i] == '.' {
				j := i + 1
				if j < len(accessors) && accessors[j] >= '0' && accessors[j] <= '9' {
					return false
				}
			}
			numStr := accessors[numStart:i]
			idx, err := strconv.Atoi(numStr)
			if err != nil {
				return false
			}

			if isList {
				if idx < 0 || idx >= list.Len() {
					return false
				}
				current = list.Get(idx)
			} else if isBytes {
				if idx < 0 || idx >= bytes.Len() {
					return false
				}
				current = bytes.Get(idx)
			} else if isStruct {
				if !structVal.IsArray() || idx < 0 || idx >= structVal.Len() {
					return false
				}
				current = structVal.Get(idx)
			} else {
				return false
			}

		} else {
			// Unknown accessor character, stop
			break
		}
	}

	// Check if final value is undefined
	if sym, ok := current.(Symbol); ok {
		if string(sym) == UndefinedMarker || string(sym) == "undefined" {
			return false
		}
	}

	return true
}

// getStructFieldValue retrieves a field value from a struct using the definition list
// This replaces the old GetFieldValue method that required *StructDef
func (e *Executor) getStructFieldValue(ss StoredStruct, fieldName string) (interface{}, bool) {
	// Look up the definition list by defID
	defObj, ok := e.getObject(ss.DefID())
	if !ok {
		return nil, false
	}
	defList, ok := defObj.(StoredList)
	if !ok {
		return nil, false
	}

	// Look up field info from definition list
	defNamedArgs := defList.NamedArgs()
	if defNamedArgs == nil {
		return nil, false
	}
	fieldInfoVal, hasField := defNamedArgs[fieldName]
	if !hasField {
		return nil, false
	}

	// Resolve the field info list
	fieldInfo := e.resolveValue(fieldInfoVal)
	fieldInfoList, ok := fieldInfo.(StoredList)
	if !ok {
		return nil, false
	}

	// Field info format: [offset, length, mode, optionalNestedDefID, optionalCount]
	if fieldInfoList.Len() < 3 {
		return nil, false
	}

	offsetNum, _ := toNumber(fieldInfoList.Get(0))
	lengthNum, _ := toNumber(fieldInfoList.Get(1))
	modeVal := fieldInfoList.Get(2)

	fieldOffset := int(offsetNum)
	fieldLength := int(lengthNum)
	var fieldMode string
	switch m := modeVal.(type) {
	case string:
		fieldMode = m
	case QuotedString:
		fieldMode = string(m)
	case Symbol:
		fieldMode = string(m)
	default:
		fieldMode = "bytes" // default to bytes
	}

	// Get the raw bytes for this field
	bytes, ok := ss.GetBytesAt(fieldOffset, fieldLength)
	if !ok {
		return nil, false
	}

	switch fieldMode {
	case "bytes":
		// Return as StoredBytes
		result := make([]byte, len(bytes))
		copy(result, bytes)
		return NewStoredBytes(result), true

	case "string":
		// Return as string, trimmed of trailing nulls
		end := len(bytes)
		for end > 0 && bytes[end-1] == 0 {
			end--
		}
		return string(bytes[:end]), true

	case "int", "int_be":
		// Return as signed int64 (big-endian)
		var result int64
		for _, b := range bytes {
			result = (result << 8) | int64(b)
		}
		// Sign extend if MSB is set
		if len(bytes) > 0 && len(bytes) < 8 && bytes[0]&0x80 != 0 {
			signBits := int64(-1) << (uint(len(bytes)) * 8)
			result |= signBits
		}
		return result, true

	case "int_le":
		// Return as signed int64 (little-endian)
		var result int64
		for i := len(bytes) - 1; i >= 0; i-- {
			result = (result << 8) | int64(bytes[i])
		}
		// Sign extend if MSB is set
		if len(bytes) > 0 && len(bytes) < 8 && bytes[len(bytes)-1]&0x80 != 0 {
			signBits := int64(-1) << (uint(len(bytes)) * 8)
			result |= signBits
		}
		return result, true

	case "uint", "uint_be":
		// Return as unsigned int64 (big-endian)
		var result int64
		for _, b := range bytes {
			result = (result << 8) | int64(b)
		}
		return result, true

	case "uint_le":
		// Return as unsigned int64 (little-endian)
		var result int64
		for i := len(bytes) - 1; i >= 0; i-- {
			result = (result << 8) | int64(bytes[i])
		}
		return result, true

	case "float", "float_be":
		// Return as float64 (big-endian IEEE 754)
		if len(bytes) == 4 {
			bits := uint32(bytes[0])<<24 | uint32(bytes[1])<<16 | uint32(bytes[2])<<8 | uint32(bytes[3])
			return float64(math.Float32frombits(bits)), true
		} else if len(bytes) == 8 {
			bits := uint64(bytes[0])<<56 | uint64(bytes[1])<<48 | uint64(bytes[2])<<40 | uint64(bytes[3])<<32 |
				uint64(bytes[4])<<24 | uint64(bytes[5])<<16 | uint64(bytes[6])<<8 | uint64(bytes[7])
			return math.Float64frombits(bits), true
		}
		// Unsupported float size, return as bytes
		result := make([]byte, len(bytes))
		copy(result, bytes)
		return NewStoredBytes(result), true

	case "float_le":
		// Return as float64 (little-endian IEEE 754)
		if len(bytes) == 4 {
			bits := uint32(bytes[3])<<24 | uint32(bytes[2])<<16 | uint32(bytes[1])<<8 | uint32(bytes[0])
			return float64(math.Float32frombits(bits)), true
		} else if len(bytes) == 8 {
			bits := uint64(bytes[7])<<56 | uint64(bytes[6])<<48 | uint64(bytes[5])<<40 | uint64(bytes[4])<<32 |
				uint64(bytes[3])<<24 | uint64(bytes[2])<<16 | uint64(bytes[1])<<8 | uint64(bytes[0])
			return math.Float64frombits(bits), true
		}
		// Unsupported float size, return as bytes
		result := make([]byte, len(bytes))
		copy(result, bytes)
		return NewStoredBytes(result), true

	case "bit0", "bit1", "bit2", "bit3", "bit4", "bit5", "bit6", "bit7":
		// Return boolean based on specific bit (use bitwise AND for retrieval)
		if len(bytes) < 1 {
			return false, true
		}
		bitNum := int(fieldMode[3] - '0') // Extract bit number from mode name
		mask := byte(1 << bitNum)
		return (bytes[0] & mask) != 0, true

	case "struct":
		// Return as nested StoredStruct
		// Get optional nested def ID and count
		var nestedDefID int
		var nestedCount int = -1 // -1 for single struct
		if fieldInfoList.Len() >= 4 {
			nestedIDNum, _ := toNumber(fieldInfoList.Get(3))
			nestedDefID = int(nestedIDNum)
		}
		if fieldInfoList.Len() >= 5 {
			countNum, _ := toNumber(fieldInfoList.Get(4))
			nestedCount = int(countNum)
		}

		if nestedDefID == 0 {
			return nil, false
		}

		nestedData := make([]byte, fieldLength)
		copy(nestedData, bytes)

		// Get nested struct size from its definition
		nestedDefObj, ok := e.getObject(nestedDefID)
		if !ok {
			return nil, false
		}
		nestedDefList, ok := nestedDefObj.(StoredList)
		if !ok {
			return nil, false
		}
		nestedDefNamedArgs := nestedDefList.NamedArgs()
		var nestedSizeVal interface{}
		if nestedDefNamedArgs != nil {
			nestedSizeVal = nestedDefNamedArgs["__size"]
		}
		nestedSizeNum, _ := toNumber(nestedSizeVal)
		nestedSize := int(nestedSizeNum)

		if nestedCount > 0 {
			// Nested struct array
			return NewStoredStructFromData(nestedDefID, nestedData, nestedSize, nestedCount), true
		}
		// Single nested struct
		return NewStoredStructFromData(nestedDefID, nestedData, nestedSize, -1), true

	default:
		// Unknown mode, return as bytes
		result := make([]byte, len(bytes))
		copy(result, bytes)
		return NewStoredBytes(result), true
	}
}

// processArguments processes arguments array to resolve object markers and tilde expressions
// Resolves LIST, STR, and BLOCK markers to their actual values
// Also resolves ~expr tilde expressions to their variable values
// Handles accessor patterns like .key and integer indices
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
			e.logger.DebugCat(CatCommand,"processArguments[%d]: Symbol arg, len=%d, first chars=%q", i, len(markerStr), markerStr[:min(len(markerStr), 20)])
		} else if str, ok := arg.(string); ok {
			markerStr = str
			isMarker = true
			e.logger.DebugCat(CatCommand,"processArguments[%d]: string arg, len=%d, first chars=%q", i, len(markerStr), markerStr[:min(len(markerStr), 20)])
		}

		if isMarker {
			// Check for question expression first (?varname) - existence check
			if strings.HasPrefix(markerStr, "?") {
				// Same resolution as ~, but return bool for existence
				innerExpr := "~" + markerStr[1:] // Convert ?x to ~x for resolution
				base, accessors := splitAccessors(innerExpr)

				resolved, ok := e.resolveTildeExpression(base, state, substitutionCtx, position)
				if !ok {
					// Variable doesn't exist
					e.logger.DebugCat(CatCommand, "processArguments[%d]: Question expression - variable not found for %q", i, base)
					result[i] = false
					continue
				}

				// Check if accessor chain exists
				if accessors != "" {
					exists := e.accessorChainExists(resolved, accessors)
					e.logger.DebugCat(CatCommand, "processArguments[%d]: Question expression %q accessor chain exists: %v", i, markerStr, exists)
					result[i] = exists
				} else {
					// Variable exists, no accessors to check
					// But check if the value itself is undefined
					if sym, ok := resolved.(Symbol); ok {
						if string(sym) == UndefinedMarker || string(sym) == "undefined" {
							result[i] = false
							continue
						}
					}
					result[i] = true
				}
				continue
			}

			// Check for tilde expression (~varname)
			if strings.HasPrefix(markerStr, "~") {
				// Split off any accessors first
				base, accessors := splitAccessors(markerStr)

				resolved, ok := e.resolveTildeExpression(base, state, substitutionCtx, position)
				if !ok {
					// Tilde resolution failed, error already logged - keep original
					e.logger.DebugCat(CatCommand,"processArguments[%d]: Tilde resolution failed for %q", i, base)
					result[i] = arg
					continue
				}
				e.logger.DebugCat(CatCommand,"processArguments[%d]: Resolved tilde expression %q to %v", i, base, resolved)

				// Apply any accessors
				if accessors != "" {
					resolved = e.applyAccessorChain(resolved, accessors, position)
					e.logger.DebugCat(CatCommand,"processArguments[%d]: After accessors %q: %v", i, accessors, resolved)
				}

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

			// Check for object marker (possibly with accessors)
			base, accessors := splitAccessors(markerStr)
			if objType, objID := parseObjectMarker(base); objID >= 0 {
				e.logger.DebugCat(CatCommand,"processArguments[%d]: Detected %s marker with ID %d", i, objType, objID)
				// Retrieve the actual value (doesn't affect refcount)
				if value, exists := e.getObject(objID); exists {
					switch objType {
					case "list":
						// Return as StoredList - this passes the object by reference
						// Don't claim here - the receiving context (SetVariable, etc.) will claim
						finalValue := value
						// Apply any accessors
						if accessors != "" {
							finalValue = e.applyAccessorChain(value, accessors, position)
							e.logger.DebugCat(CatCommand,"processArguments[%d]: After accessors %q: %v", i, accessors, finalValue)
						}
						result[i] = finalValue
						e.logger.DebugCat(CatCommand,"processArguments[%d]: Resolved list marker to StoredList", i)
					case "str":
						// Keep as marker (pass-by-reference) - don't copy the string
						// The marker will be resolved when needed (display, string ops)
						// Keep the original arg (Symbol or string containing marker)
						result[i] = arg
						e.logger.DebugCat(CatCommand,"processArguments[%d]: Preserved string marker (pass-by-reference)", i)
					case "block":
						// Keep as marker (pass-by-reference) - don't copy the block
						// The marker will be resolved when needed (execution)
						result[i] = arg
						e.logger.DebugCat(CatCommand,"processArguments[%d]: Preserved block marker (pass-by-reference)", i)
					case "channel":
						// Keep as marker (pass-by-reference) - channel identity must be preserved
						result[i] = arg
						e.logger.DebugCat(CatCommand,"processArguments[%d]: Preserved channel marker (pass-by-reference)", i)
					case "fiber":
						// Keep as marker (pass-by-reference) - fiber identity must be preserved
						result[i] = arg
						e.logger.DebugCat(CatCommand,"processArguments[%d]: Preserved fiber marker (pass-by-reference)", i)
					case "file":
						// Return as *StoredFile - this passes the file handle by reference
						result[i] = value
						e.logger.DebugCat(CatCommand,"processArguments[%d]: Resolved file marker to *StoredFile", i)
					case "bytes":
						// Return as StoredBytes - this passes the bytes by reference
						finalValue := value
						// Apply any accessors
						if accessors != "" {
							finalValue = e.applyAccessorChain(value, accessors, position)
							e.logger.DebugCat(CatCommand,"processArguments[%d]: After accessors %q: %v", i, accessors, finalValue)
						}
						result[i] = finalValue
						e.logger.DebugCat(CatCommand,"processArguments[%d]: Resolved bytes marker to StoredBytes", i)
					case "struct":
						// Return as StoredStruct - this passes the struct by reference
						finalValue := value
						// Apply any accessors (index and field)
						if accessors != "" {
							finalValue = e.applyAccessorChain(value, accessors, position)
							e.logger.DebugCat(CatCommand,"processArguments[%d]: After accessors %q: %v", i, accessors, finalValue)
						}
						result[i] = finalValue
						e.logger.DebugCat(CatCommand,"processArguments[%d]: Resolved struct marker to StoredStruct", i)
					// Note: struct definitions are now just lists (handled by "list" case)
					default:
						// For unknown types, keep the marker to preserve reference semantics
						result[i] = arg
						e.logger.DebugCat(CatCommand,"processArguments[%d]: Preserved %s marker (pass-by-reference)", i, objType)
					}
					continue
				} else {
					e.logger.DebugCat(CatCommand,"processArguments[%d]: Object %d not found in store!", i, objID)
				}
			} else {
				e.logger.DebugCat(CatCommand,"processArguments[%d]: Not a valid object marker", i)
			}
		}

		// Not a marker or tilde, keep the original argument
		result[i] = arg
	}

	return result
}

// processNamedArguments processes named argument keys and values to resolve tilde expressions and object markers
// This ensures named args like "~dynKey: ~dynValue" or "prefix~var: value" get resolved the same way positional args do
func (e *Executor) processNamedArguments(namedArgs map[string]interface{}, state *ExecutionState, substitutionCtx *SubstitutionContext, position *SourcePosition) map[string]interface{} {
	if len(namedArgs) == 0 {
		return namedArgs
	}

	result := make(map[string]interface{}, len(namedArgs))
	for key, value := range namedArgs {
		// Process the key through full resolution (handles embedded tildes, accessors, etc.)
		processedKey := e.processArguments([]interface{}{Symbol(key)}, state, substitutionCtx, position)
		finalKey := key
		if len(processedKey) > 0 {
			// Convert resolved key back to string
			finalKey = fmt.Sprintf("%v", processedKey[0])
		}

		// Process the value as a single-element slice and extract the result
		processed := e.processArguments([]interface{}{value}, state, substitutionCtx, position)
		if len(processed) > 0 {
			result[finalKey] = processed[0]
		} else {
			result[finalKey] = value
		}
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
		e.logger.DebugCat(CatCommand,"Inverted synchronous result: %v -> %v", bool(boolStatus), inverted)
		return BoolStatus(inverted)
	} else if earlyReturn, ok := result.(EarlyReturn); ok {
		// Invert the status of an early return
		inverted := !bool(earlyReturn.Status)
		e.logger.DebugCat(CatCommand,"Inverted early return status: %v -> %v", bool(earlyReturn.Status), inverted)
		return EarlyReturn{
			Status:    BoolStatus(inverted),
			Result:    earlyReturn.Result,
			HasResult: earlyReturn.HasResult,
		}
	} else if tokenResult, ok := result.(TokenResult); ok {
		// For async result, create wrapper token with inversion flag
		e.logger.DebugCat(CatCommand,"Creating inverter wrapper for async token: %s", string(tokenResult))

		inverterToken := e.RequestCompletionToken(nil, "", 5*time.Minute, state, position)

		// Mark this token for result inversion
		e.mu.Lock()
		if tokenData, exists := e.activeTokens[inverterToken]; exists {
			tokenData.InvertStatus = true
		}
		e.mu.Unlock()

		// Chain the inverter to the original token
		e.chainTokens(string(tokenResult), inverterToken)

		e.logger.DebugCat(CatCommand,"Created inverter token: %s -> %s", string(tokenResult), inverterToken)
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
	}
	// Parent macro context comes from the current execution state, not the position
	macroContext.ParentMacro = state.macroContext

	e.logger.DebugCat(CatCommand,"Executing macro defined at %s:%d, called from %s:%d",
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

	// Set macro context for stack traces
	macroState.macroContext = macroContext

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
		e.logger.DebugCat(CatCommand,"Merged %d exports from macro to parent's exports module", len(exportsSection))
	}
	macroState.moduleEnv.mu.RUnlock()

	// Transfer result to parent state
	if macroState.HasResult() {
		state.SetResult(macroState.GetResult())
		e.logger.DebugCat(CatCommand,"Transferred macro result to parent state: %v", macroState.GetResult())
	}

	// Merge bubbles from macro state to parent state
	state.MergeBubbles(macroState)

	// Clean up macro state
	macroState.ReleaseAllReferences()

	e.logger.DebugCat(CatCommand,"Macro execution completed with result: %v", result)
	return result
}
