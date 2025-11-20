package pawscript

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
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
			// Return the status from the early return
			return earlyReturn.Status
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
	}

	return BoolStatus(lastStatus)
}

// executeParsedCommand executes a single parsed command
func (e *Executor) executeParsedCommand(parsedCmd *ParsedCommand, state *ExecutionState, substitutionCtx *SubstitutionContext) Result {
	return e.executeSingleCommand(parsedCmd.Command, state, substitutionCtx, parsedCmd.Position)
}

// chainTokens chains two tokens together
func (e *Executor) chainTokens(firstToken, secondToken string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	firstTokenData, exists1 := e.activeTokens[firstToken]
	secondTokenData, exists2 := e.activeTokens[secondToken]

	if !exists1 || !exists2 {
		e.logger.Error("Cannot chain tokens: %s or %s not found", firstToken, secondToken)
		return
	}

	firstTokenData.ChainedToken = secondToken
	secondTokenData.ParentToken = firstToken

	e.logger.Debug("Chained token %s to complete after %s", secondToken, firstToken)
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
	if strings.HasPrefix(commandStr, "(") && strings.HasSuffix(commandStr, ")") {
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

	// Apply syntactic sugar
	commandStr = e.applySyntacticSugar(commandStr)

	e.logger.Debug("executeSingleCommand called with: \"%s\"", commandStr)

	// CRITICAL: Always evaluate brace expressions, even when not in a macro context
	// Create a minimal substitution context if one doesn't exist
	if substitutionCtx == nil {
		filename := ""
		if position != nil {
			filename = position.Filename
		}
		substitutionCtx = &SubstitutionContext{
			Args:                []interface{}{},
			ExecutionState:      state,
			ParentContext:       nil,
			MacroContext:        nil,
			CurrentLineOffset:   0,
			CurrentColumnOffset: 0,
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

				// Now parse and execute the command with the substituted string
				cmdName, args := ParseCommand(finalString)

				// Process arguments to resolve any LIST markers
				args = e.processArguments(args, capturedState)

				e.logger.Debug("Parsed as - Command: \"%s\", Args: %v", cmdName, args)

				// Try registered command
				e.mu.RLock()
				handler, exists := e.commands[cmdName]
				e.mu.RUnlock()

				// Try fallback handler if command not found
				if !exists && e.fallbackHandler != nil {
					e.logger.Debug("Command \"%s\" not found, trying fallback handler", cmdName)
					fallbackResult := e.fallbackHandler(cmdName, args, capturedState, capturedPosition)
					if fallbackResult != nil {
						e.logger.Debug("Fallback handler returned: %v", fallbackResult)
						if capturedShouldInvert {
							return e.invertStatus(fallbackResult, capturedState, capturedPosition)
						}
						return fallbackResult
					}
				}

				if !exists {
					e.logger.UnknownCommandError(cmdName, capturedPosition, nil)
					result := BoolStatus(false)
					if capturedShouldInvert {
						return BoolStatus(!bool(result))
					}
					return result
				}

				// Execute command
				e.logger.Debug("Executing %s with args: %v", cmdName, args)
				ctx := e.createContext(args, capturedState, capturedPosition)
				result := handler(ctx)

				// Apply inversion if needed
				if capturedShouldInvert {
					return e.invertStatus(result, capturedState, capturedPosition)
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

	// Parse command
	cmdName, args := ParseCommand(commandStr)

	// Process arguments to resolve any LIST markers
	args = e.processArguments(args, state)

	e.logger.Debug("Parsed as - Command: \"%s\", Args: %v", cmdName, args)

	// Try registered command
	e.mu.RLock()
	handler, exists := e.commands[cmdName]
	e.mu.RUnlock()

	// Try fallback handler if command not found
	if !exists && e.fallbackHandler != nil {
		e.logger.Debug("Command \"%s\" not found, trying fallback handler", cmdName)
		fallbackResult := e.fallbackHandler(cmdName, args, state, position)
		if fallbackResult != nil {
			e.logger.Debug("Fallback handler returned: %v", fallbackResult)
			if shouldInvert {
				return e.invertStatus(fallbackResult, state, position)
			}
			return fallbackResult
		}
	}

	if !exists {
		e.logger.UnknownCommandError(cmdName, position, nil)
		result := BoolStatus(false)
		if shouldInvert {
			return BoolStatus(!bool(result))
		}
		return result
	}

	// Execute command
	e.logger.Debug("Executing %s with args: %v", cmdName, args)
	ctx := e.createContext(args, state, position)
	result := handler(ctx)

	// Apply inversion if needed
	if shouldInvert {
		return e.invertStatus(result, state, position)
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

// applySubstitution applies macro argument substitution
func (e *Executor) applySubstitution(str string, ctx *SubstitutionContext) string {
	// fmt.Fprintf(os.Stderr, "[DEBUG applySubstitution] Input: %q\n", str)
	// Note: ctx should never be nil - caller should create a minimal context if needed
	if ctx == nil {
		return str
	}

	result := str

	// First, protect escaped dollar signs by replacing \$ with a placeholder
	// We use a placeholder that's unlikely to appear in normal text
	const escapedDollarPlaceholder = "\x00SUB\x00"
	result = strings.ReplaceAll(result, `\$`, escapedDollarPlaceholder)

	// Apply brace expression substitution first
	result = e.substituteBraceExpressions(result, ctx)

	// Check if brace substitution failed
	if result == "\x00PAWS_FAILED\x00" {
		// Error already logged by ExecuteWithState, just propagate the failure
		return result
	}

	// Check if brace substitution returned an async marker
	if strings.HasPrefix(result, "\x00PAWS:") && strings.HasSuffix(result, "\x00") {
		// Extract the token and return it as-is
		// The caller (executeSingleCommand) will handle this
		return result
	}

	// CRITICAL: Only apply $*, $#, and $N substitutions when we're in a macro execution context
	// This prevents premature substitution when defining nested macros
	if ctx.MacroContext != nil {
		// Apply $* (all args) - preserve forms for comma-separated list
		if len(ctx.Args) > 0 {
			allArgs := make([]string, len(ctx.Args))
			for i, arg := range ctx.Args {
				allArgs[i] = e.formatArgumentForList(arg)
			}
			result = strings.ReplaceAll(result, "$*", strings.Join(allArgs, ", "))
		} else {
			result = strings.ReplaceAll(result, "$*", "")
		}

		// Apply $@ (all args as LIST) - substitutes the marker from $@ variable
		// This preserves the LIST object through substitution
		if ctx.ExecutionState != nil {
			if argsVar, exists := ctx.ExecutionState.GetVariable("$@"); exists {
				// The variable contains the \x00LIST:id\x00 marker
				argsMarker := fmt.Sprintf("%v", argsVar)
				result = strings.ReplaceAll(result, "$@", argsMarker)
			} else {
				// No $@ variable - empty list
				result = strings.ReplaceAll(result, "$@", "()")
			}
		} else {
			result = strings.ReplaceAll(result, "$@", "()")
		}

		// Apply $# (arg count) - use argc on $@ list
		if ctx.ExecutionState != nil {
			if argsVar, exists := ctx.ExecutionState.GetVariable("$@"); exists {
				// Get the LIST object
				if sym, ok := argsVar.(Symbol); ok {
					marker := string(sym)
					if id := parseObjectMarker(marker); id >= 0 {
						if listObj, exists := e.getObject(id); exists {
							if storedList, ok := listObj.(StoredList); ok {
								result = strings.ReplaceAll(result, "$#", fmt.Sprintf("%d", storedList.Len()))
							}
						}
					}
				}
			}
		}
		// Fallback if we couldn't get the list
		if strings.Contains(result, "$#") {
			result = strings.ReplaceAll(result, "$#", fmt.Sprintf("%d", len(ctx.Args)))
		}

		// Apply $1, $2, etc (indexed args) - pull from $@ list using argv
		re := regexp.MustCompile(`\$(\d+)`)
		result = re.ReplaceAllStringFunc(result, func(match string) string {
			indexStr := match[1:] // Remove $
			index, err := strconv.Atoi(indexStr)
			if err != nil {
				return match
			}

			// Get from $@ list
			if ctx.ExecutionState != nil {
				if argsVar, exists := ctx.ExecutionState.GetVariable("$@"); exists {
					// Get the LIST object
					if sym, ok := argsVar.(Symbol); ok {
						marker := string(sym)
						if objID := parseObjectMarker(marker); objID >= 0 {
							if listObj, exists := e.getObject(objID); exists {
								if storedList, ok := listObj.(StoredList); ok {
									// index is 1-based, convert to 0-based
									item := storedList.Get(index - 1)
									if item != nil {
										return e.formatArgumentForSubstitution(item)
									}
								}
							}
						}
					}
				}
			}
			
			// Fallback to old behavior
			index-- // Convert to 0-based
			if index >= 0 && index < len(ctx.Args) {
				return e.formatArgumentForSubstitution(ctx.Args[index])
			}
			return match
		})
	}

	// Finally, restore escaped dollar signs
	result = strings.ReplaceAll(result, escapedDollarPlaceholder, "$")

	return result
}

// substituteBraceExpressions substitutes brace expressions {command}
// This version supports parallel async evaluation of all braces at the same nesting level
func (e *Executor) substituteBraceExpressions(str string, ctx *SubstitutionContext) string {
	// Find all top-level braces in this string
	braces := e.findAllTopLevelBraces(str, ctx)

	if len(braces) == 0 {
		return str // No braces to process
	}

	e.logger.Debug("Found %d top-level braces to evaluate", len(braces))

	// Execute all braces and collect their results
	evaluations := make([]*BraceEvaluation, len(braces))
	hasAsync := false

	for i, brace := range braces {
		e.logger.Debug("Evaluating brace %d: line=%d, column=%d", i, brace.StartLine, brace.StartColumn)
		e.logger.Debug("Brace content: \"{%s}\"", brace.Content)

		// Create a child state with shared variables but isolated result storage
		// This prevents async braces from racing on result storage while still sharing variables

		if ctx == nil {
			// Handle the nil case - either return an error or use a default
			return str // nil, fmt.Errorf("context cannot be nil")
		}
		braceState := NewExecutionStateFromSharedVars(ctx.ExecutionState)

		// Calculate accumulated offsets for this brace
		/*currentLineOffset := 0
		currentColumnOffset := 0
		if ctx != nil {
		*/
		currentLineOffset := ctx.CurrentLineOffset
		currentColumnOffset := ctx.CurrentColumnOffset
		// }

		newLineOffset := currentLineOffset + (brace.StartLine - 1)
		var newColumnOffset int
		if brace.StartLine == 1 {
			// braceStartColumn is off-by-one (column++ happens before char check)
			// So if '{' is at position 18, braceStartColumn is 19
			// Content starts at position 19, so offset should be 18 (19-1)
			newColumnOffset = currentColumnOffset + brace.StartColumn - 1
		} else {
			// Same off-by-one issue
			newColumnOffset = brace.StartColumn - 1
		}

		e.logger.Debug("Brace offsets: line=%d, column=%d", newLineOffset, newColumnOffset)

		// Create substitution context using the child state
		braceSubstitutionCtx := &SubstitutionContext{
			Args:                ctx.Args,
			ExecutionState:      braceState,
			ParentContext:       ctx,
			MacroContext:        ctx.MacroContext,
			CurrentLineOffset:   newLineOffset,
			CurrentColumnOffset: newColumnOffset,
			Filename:            ctx.Filename,
		}

		// Execute the brace content with the child state (isolated result storage, shared variables)
		executeResult := e.ExecuteWithState(
			brace.Content,
			braceState,
			braceSubstitutionCtx,
			ctx.Filename, // Pass filename for error reporting
			newLineOffset,
			newColumnOffset,
		)

		// Capture the result IMMEDIATELY after execution, before the next brace can overwrite it
		var capturedResult interface{}
		var hasCapturedResult bool
		if braceState.HasResult() {
			capturedResult = braceState.GetResult()
			hasCapturedResult = true
		}

		// Create position for error reporting (points to first character inside brace)
		braceContentPosition := &SourcePosition{
			Line:     newLineOffset + 1,
			Column:   newColumnOffset + 1, // First character of content inside brace
			Length:   len(brace.Content),
			Filename: ctx.Filename,
		}

		// Create evaluation record
		evaluations[i] = &BraceEvaluation{
			Location:  brace,
			State:     braceState,
			Completed: false,
			Failed:    false,
			Position:  braceContentPosition,
		}

		// Check if this evaluation is async
		if tokenResult, ok := executeResult.(TokenResult); ok {
			evaluations[i].IsAsync = true
			evaluations[i].TokenID = string(tokenResult)
			hasAsync = true
			e.logger.Debug("Brace %d returned async token: %s", i, evaluations[i].TokenID)
		} else {
			// Synchronous completion
			evaluations[i].Completed = true

			// Check if it was successful
			if boolStatus, ok := executeResult.(BoolStatus); ok && !bool(boolStatus) {
				evaluations[i].Failed = true
				evaluations[i].Error = "Command returned false"
				e.logger.Debug("Brace %d completed synchronously with failure", i)
			} else {
				// Use the captured result
				if hasCapturedResult {
					evaluations[i].Result = capturedResult
				} else if boolStatus, ok := executeResult.(BoolStatus); ok {
					evaluations[i].Result = fmt.Sprintf("%v", bool(boolStatus))
				}
				e.logger.Debug("Brace %d completed synchronously with result: %v", i, evaluations[i].Result)
			}
			
			// Transfer ownership of objects in result to parent before cleanup
			if hasCapturedResult {
				resultRefs := braceState.ExtractObjectReferences(capturedResult)
				for _, refID := range resultRefs {
					ctx.ExecutionState.ClaimObjectReference(refID)
				}
			}
			
			// Clean up brace state references (synchronous case)
			braceState.ReleaseAllReferences()
		}
	}

	// If any evaluation is async, we need to coordinate
	if hasAsync {
		e.logger.Debug("At least one brace is async, creating coordinator token")

		// We need to return a special marker that tells the caller we're suspending
		// The caller (executeSingleCommand) will need to handle this
		coordinatorToken := e.RequestBraceCoordinatorToken(
			evaluations,
			str,
			ctx,
			func(finalString string, success bool) Result {
				// This callback will be invoked when all braces complete
				// For now, we need to signal back through the token system
				e.logger.Debug("Brace coordinator completed: success=%v, string=%s", success, finalString)
				// Store the result in a way that can be retrieved
				return BoolStatus(success)
			},
			ctx.ExecutionState,
			nil,
		)

		// Return a special marker that includes the coordinator token
		// The executeSingleCommand will need to detect this and return the token
		return fmt.Sprintf("\x00PAWS:%s\x00", coordinatorToken)
	}

	// All synchronous - check for any failures
	for i, eval := range evaluations {
		if eval.Failed {
			// Log the error with position information
			// Note: Parse errors are already logged by ExecuteWithState, but command execution
			// failures (commands returning false) need to be logged here
			errorMsg := "Command in brace expression failed"
			if eval.Error != "" {
				errorMsg = eval.Error
			}

			// Get source context if we have the original lines
			var sourceContext []string
			if ctx != nil && ctx.Filename != "" {
				// Try to get source lines from parser's source map if available
				sourceContext = nil // We'll add this later if needed
			}

			e.logger.ErrorWithPosition(errorMsg, eval.Position, sourceContext)
			e.logger.Debug("Synchronous brace evaluation %d failed, aborting command", i)
			// Return special marker to indicate brace failure
			return "\x00PAWS_FAILED\x00"
		}
	}

	// Substitute all results immediately
	result := e.substituteAllBraces(str, evaluations, ctx.ExecutionState)
	e.logger.Debug("All braces synchronous, substituted result: %s", result)

	return result
}

/*
// reEvaluateToken re-evaluates a token after brace substitution
func (e *Executor) reEvaluateToken(token string, ctx *SubstitutionContext) string {
	result := token

	// First, protect escaped dollar signs
	const escapedDollarPlaceholder = "\x00SUB\x00"
	result = strings.ReplaceAll(result, `\$`, escapedDollarPlaceholder)

	// Re-apply $* substitution
	if len(ctx.Args) > 0 {
		allArgs := make([]string, len(ctx.Args))
		for i, arg := range ctx.Args {
			allArgs[i] = e.formatArgumentForSubstitution(arg)
		}
		result = strings.ReplaceAll(result, "$*", strings.Join(allArgs, ", "))
	} else {
		result = strings.ReplaceAll(result, "$*", "")
	}

	// Re-apply $# substitution
	result = strings.ReplaceAll(result, "$#", fmt.Sprintf("%d", len(ctx.Args)))

	// Re-apply $1, $2, etc substitution
	re := regexp.MustCompile(`\$(\d+)`)
	result = re.ReplaceAllStringFunc(result, func(match string) string {
		indexStr := match[1:]
		index, err := strconv.Atoi(indexStr)
		if err != nil {
			return match
		}

		index--
		if index >= 0 && index < len(ctx.Args) {
			return e.formatArgumentForSubstitution(ctx.Args[index])
		}
		return match
	})

	// Restore escaped dollar signs
	result = strings.ReplaceAll(result, escapedDollarPlaceholder, "$")

	return result
}
*/

/*
// escapeSpecialCharacters escapes special syntax characters for safe substitution
func (e *Executor) escapeSpecialCharacters(str string) string {
	// Characters that have special meaning in PawScript syntax
	// Must be escaped when substituting into command text
	const specialChars = ";&#(){}\"'\\$,! \t\n\r"

	var result strings.Builder
	for _, char := range str {
		if strings.ContainsRune(specialChars, char) {
			result.WriteRune('\\')
		}
		result.WriteRune(char)
	}
	return result.String()
}
*/

// formatArgumentForParenGroup formats an argument for $@ substitution
// Preserves original forms for creating ParenGroup literals
// Similar to formatArgumentForList but without escaping quotes (not in string context)
func (e *Executor) formatArgumentForParenGroup(arg interface{}) string {
	switch v := arg.(type) {
	case ParenGroup:
		// Parenthetic group: wrap in parens, escape internal quotes/backslashes
		escaped := e.escapeQuotesAndBackslashes(string(v))
		return "(" + escaped + ")"
	case QuotedString:
		// Quoted string: wrap with regular quotes (not escaped - not in string context)
		escaped := e.escapeQuotesAndBackslashes(string(v))
		return "\"" + escaped + "\""
	case Symbol:
		// Symbol: use as bare identifier
		return string(v)
	case string:
		// Bare string: use bare if safe identifier, otherwise wrap with quotes
		if e.isSafeIdentifier(v) {
			return v
		}
		escaped := e.escapeQuotesAndBackslashes(v)
		return "\"" + escaped + "\""
	case int64, float64, bool:
		// Numbers and booleans as-is
		return fmt.Sprintf("%v", v)
	default:
		// Unknown type: convert to string and wrap with quotes
		str := fmt.Sprintf("%v", v)
		escaped := e.escapeQuotesAndBackslashes(str)
		return "\"" + escaped + "\""
	}
}

// formatArgumentForList formats an argument for $* substitution
// Preserves original forms but escapes quotes for string contexts
// This is used when creating comma-separated lists where structure matters
func (e *Executor) formatArgumentForList(arg interface{}) string {
	switch v := arg.(type) {
	case ParenGroup:
		// Parenthetic group: wrap in parens, escape internal quotes/backslashes
		escaped := e.escapeQuotesAndBackslashes(string(v))
		return "(" + escaped + ")"
	case QuotedString:
		// Quoted string: escape and wrap with ESCAPED quotes
		// This allows it to appear inside outer quotes without breaking them
		escaped := e.escapeQuotesAndBackslashes(string(v))
		return "\\\"" + escaped + "\\\""
	case Symbol:
		// Symbol: use as bare identifier
		return string(v)
	case string:
		// Bare string: use bare if safe identifier, otherwise wrap with escaped quotes
		if e.isSafeIdentifier(v) {
			return v
		}
		escaped := e.escapeQuotesAndBackslashes(v)
		return "\\\"" + escaped + "\\\""
	case int64, float64, bool:
		// Numbers and booleans as-is
		return fmt.Sprintf("%v", v)
	default:
		// Unknown type: convert to string and wrap with escaped quotes
		str := fmt.Sprintf("%v", v)
		escaped := e.escapeQuotesAndBackslashes(str)
		return "\\\"" + escaped + "\\\""
	}
}

// escapeQuotesAndBackslashes escapes only quotes and backslashes
// Used for preserving content inside string contexts
func (e *Executor) escapeQuotesAndBackslashes(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}

// formatArgumentForSubstitution formats an argument for $1, $2, etc. substitution
// Unwraps quotes and parentheses to get raw content
// This is used when substituting into string contexts where we don't want nesting
func (e *Executor) formatArgumentForSubstitution(arg interface{}) string {
	switch v := arg.(type) {
	case ParenGroup:
		// Unwrap parentheses - just the content
		return string(v)
	case QuotedString:
		// Unwrap quotes - just the content (already unescaped)
		return string(v)
	case Symbol:
		// Symbol as-is (bare identifier)
		return string(v)
	case string:
		// Bare string as-is
		return v
	case int64, float64, bool:
		// Numbers and booleans as-is
		return fmt.Sprintf("%v", v)
	default:
		// Unknown type: convert to string
		return fmt.Sprintf("%v", v)
	}
}

// isSafeIdentifier checks if a string can be used as a bare identifier
func (e *Executor) isSafeIdentifier(s string) bool {
	// Empty or keywords are not safe
	if s == "" || s == "true" || s == "false" || s == "then" || s == "else" || s == "not" {
		return false
	}

	// Must be alphanumeric/underscore, can't start with digit
	runes := []rune(s)
	for i, r := range runes {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return false
			}
		} else {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
				return false
			}
		}
	}
	return true
}

// createContext creates a command context
func (e *Executor) createContext(args []interface{}, state *ExecutionState, position *SourcePosition) *Context {
	return &Context{
		Args:     args,
		Position: position,
		state:    state,
		executor: e,
		requestToken: func(cleanup func(string)) string {
			return e.RequestCompletionToken(cleanup, "", 5*time.Minute, state, position)
		},
		resumeToken: func(tokenID string, status bool) bool {
			return e.PopAndResumeCommandSequence(tokenID, status)
		},
	}
}

// GetTokenStatus returns information about active tokens
func (e *Executor) GetTokenStatus() map[string]interface{} {
	e.mu.RLock()
	defer e.mu.RUnlock()

	tokens := make([]map[string]interface{}, 0, len(e.activeTokens))

	for id, data := range e.activeTokens {
		tokens = append(tokens, map[string]interface{}{
			"id":                 id,
			"parentToken":        data.ParentToken,
			"childCount":         len(data.Children),
			"hasCommandSequence": data.CommandSequence != nil,
			"age":                time.Since(data.Timestamp).Milliseconds(),
			"hasSuspendedResult": data.HasSuspendedResult,
		})
	}

	return map[string]interface{}{
		"activeCount": len(e.activeTokens),
		"tokens":      tokens,
	}
}

// substituteAllBraces substitutes all brace evaluation results into the original string
func (e *Executor) substituteAllBraces(originalString string, evaluations []*BraceEvaluation, state *ExecutionState) string {
	// Sort evaluations by position (descending) so we substitute from end to start
	// This prevents position shifts from affecting later substitutions
	sortedEvals := make([]*BraceEvaluation, len(evaluations))
	copy(sortedEvals, evaluations)

	// Bubble sort by StartPos descending
	for i := 0; i < len(sortedEvals)-1; i++ {
		for j := 0; j < len(sortedEvals)-i-1; j++ {
			if sortedEvals[j].Location.StartPos < sortedEvals[j+1].Location.StartPos {
				sortedEvals[j], sortedEvals[j+1] = sortedEvals[j+1], sortedEvals[j]
			}
		}
	}

	result := originalString
	runes := []rune(result)

	for _, eval := range sortedEvals {
		// Get the result value
		var rawValue interface{}
		if eval.State != nil && eval.State.HasResult() {
			rawValue = eval.State.GetResult()
		} else if eval.Result != nil {
			rawValue = eval.Result
		}

		// Format the result based on type
		var resultValue string
		if eval.Location.IsUnescape {
			// ${...} - no escaping, direct insertion
			resultValue = fmt.Sprintf("%v", rawValue)
		} else {
			// {...} - preserve types properly, considering quote context
			resultValue = e.formatBraceResult(rawValue, originalString, eval.Location.StartPos, state)
		}

		// Substitute: replace from StartPos to EndPos+1 with resultValue
		before := string(runes[:eval.Location.StartPos])
		after := string(runes[eval.Location.EndPos+1:])
		result = before + resultValue + after
		runes = []rune(result)
	}

	return result
}

// isInsideQuotes checks if a position in a string is inside quoted text
// This is used to determine how to format brace substitution results
func (e *Executor) isInsideQuotes(str string, pos int) bool {
	inQuote := false
	var quoteChar rune

	runes := []rune(str)
	i := 0

	for i < len(runes) && i < pos {
		char := runes[i]

		// Handle escape sequences - skip the backslash and next char
		if char == '\\' && i+1 < len(runes) {
			i += 2
			continue
		}

		// Track quote state
		if !inQuote && (char == '"' || char == '\'') {
			inQuote = true
			quoteChar = char
		} else if inQuote && char == quoteChar {
			inQuote = false
			quoteChar = 0
		}

		i++
	}

	return inQuote
}

// formatBraceResult formats a brace evaluation result for substitution
// Takes the original string and brace position to detect quote context
func (e *Executor) formatBraceResult(value interface{}, originalString string, bracePos int, state *ExecutionState) string {
	// Handle nil specially - output as bare word "nil"
	if value == nil {
		return "nil"
	}

	// Check if we're inside a quoted string context
	insideQuotes := e.isInsideQuotes(originalString, bracePos)

	switch v := value.(type) {
	case bool:
		// Booleans as bare words - parser will recognize them
		if v {
			return "true"
		}
		return "false"
	case ParenGroup:
		if insideQuotes {
			// Inside quotes: unwrap and escape only quotes/backslashes
			return e.escapeQuotesAndBackslashes(string(v))
		}
		// Outside quotes: preserve as parentheses
		return "(" + string(v) + ")"
	case QuotedString:
		if insideQuotes {
			// Inside quotes: unwrap and escape only quotes/backslashes
			return e.escapeQuotesAndBackslashes(string(v))
		}
		// Outside quotes: preserve as quoted string, escaping internal quotes
		escaped := e.escapeQuotesAndBackslashes(string(v))
		return "\"" + escaped + "\""
	case Symbol:
		// Symbol as bare identifier
		return string(v)
	case string:
		if insideQuotes {
			// Inside quotes: escape only quotes/backslashes
			return e.escapeQuotesAndBackslashes(v)
		}
		// Outside quotes: wrap in quotes to preserve the string value
		// (don't escape spaces/special chars - that's for bare words only)
		escaped := e.escapeQuotesAndBackslashes(v)
		return "\"" + escaped + "\""
	case StoredList:
		// StoredList: use a special marker that preserves the object
		// We'll use a unique placeholder that will be detected during argument parsing
		// Format: \x00LIST:index\x00 where index is stored in the execution state
		id := e.storeObject(value, "list")
		// The creating context claims the first reference
		if state != nil {
			state.ClaimObjectReference(id)
		}
		return fmt.Sprintf("\x00LIST:%d\x00", id)
	case int64, float64:
		// Numbers as-is
		return fmt.Sprintf("%v", v)
	default:
		// Unknown type - convert to string and wrap in quotes outside quote context
		str := fmt.Sprintf("%v", v)
		if insideQuotes {
			return e.escapeQuotesAndBackslashes(str)
		}
		// Wrap in quotes to preserve value
		escaped := e.escapeQuotesAndBackslashes(str)
		return "\"" + escaped + "\""
	}
}

// findAllTopLevelBraces finds all brace expressions at the current nesting level
// Also detects ${...} for unescape mode
func (e *Executor) findAllTopLevelBraces(str string, ctx *SubstitutionContext) []*BraceLocation {
	var braces []*BraceLocation

	braceDepth := 0
	parenDepth := 0
	braceStart := -1
	braceIsUnescape := false

	line := 1
	column := 1
	braceStartLine := 1
	braceStartColumn := 1

	runes := []rune(str)
	for i, char := range runes {
		if char == '\n' {
			line++
			column = 1
		} else {
			column++
		}

		if char == '(' {
			parenDepth++
			continue
		} else if char == ')' {
			parenDepth--
			continue
		}

		if parenDepth == 0 && char == '{' {
			if braceDepth == 0 {
				// Check if preceded by $ for unescape mode
				isUnescape := false
				actualStart := i
				if i > 0 && runes[i-1] == '$' {
					isUnescape = true
					actualStart = i - 1 // Include the $
				}

				braceStart = actualStart
				braceIsUnescape = isUnescape
				braceStartLine = line
				braceStartColumn = column
			}
			braceDepth++
		} else if parenDepth == 0 && char == '}' {
			braceDepth--
			if braceDepth == 0 && braceStart != -1 {
				// Determine content start position
				contentStart := braceStart + 1
				if braceIsUnescape {
					contentStart = braceStart + 2 // Skip ${
				}

				braces = append(braces, &BraceLocation{
					StartPos:    braceStart,
					EndPos:      i,
					Content:     string(runes[contentStart:i]),
					StartLine:   braceStartLine,
					StartColumn: braceStartColumn,
					IsUnescape:  braceIsUnescape,
				})
				braceStart = -1
			}
		}
	}

	return braces
}

// processArguments processes arguments array to resolve LIST markers
// LIST markers: \x00LIST:index\x00 for StoredList objects
func (e *Executor) processArguments(args []interface{}, state *ExecutionState) []interface{} {
	if len(args) == 0 {
		return args
	}

	result := make([]interface{}, len(args))
	for i, arg := range args {
		// Check if it's a Symbol that might be a LIST marker
		if sym, ok := arg.(Symbol); ok {
			str := string(sym)

			// Check for LIST marker
			if strings.HasPrefix(str, "\x00LIST:") && strings.HasSuffix(str, "\x00") {
				// Extract the index
				indexStr := str[len("\x00LIST:") : len(str)-1]
				if index, err := strconv.Atoi(indexStr); err == nil {
					// Retrieve the actual StoredList value (doesn't affect refcount)
					if value, exists := e.getObject(index); exists {
						result[i] = value
						// Receiving context claims a reference
						if state != nil {
							state.ClaimObjectReference(index)
						}
						continue
					}
				}
			}
		}
		// Not a marker, keep the original argument
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
