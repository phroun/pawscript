package pawscript

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// applySubstitution applies macro argument substitution
func (e *Executor) applySubstitution(str string, ctx *SubstitutionContext) string {
	// fmt.Fprintf(os.Stderr, "[DEBUG applySubstitution] Input: %q\n", str)
	// Note: ctx should never be nil - caller should create a minimal context if needed
	if ctx == nil {
		return str
	}

	result := str

	// First, protect escaped dollar signs and tildes by replacing with placeholders
	const escapedDollarPlaceholder = "\x00SUB\x00"
	const escapedTildePlaceholder = "\x00TILDE\x00"
	result = strings.ReplaceAll(result, `\$`, escapedDollarPlaceholder)
	result = strings.ReplaceAll(result, `\~`, escapedTildePlaceholder)

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
					if objType, id := parseObjectMarker(marker); objType == "list" && id >= 0 {
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
		// Use position-aware substitution to handle quote context correctly
		result = e.substituteDollarArgs(result, ctx)
	}

	// Finally, restore escaped dollar signs
	result = strings.ReplaceAll(result, escapedDollarPlaceholder, "$")

	// Apply tilde variable interpolation (after $N substitutions, so ~var works in results)
	// Create a position for error reporting
	position := &SourcePosition{
		Line:     ctx.CurrentLineOffset + 1,
		Column:   ctx.CurrentColumnOffset + 1,
		Filename: ctx.Filename,
	}
	result = e.substituteTildeExpressions(result, ctx.ExecutionState, position)

	// Restore escaped tildes (both placeholder from original \~ and backslash escapes from $N substitution)
	result = strings.ReplaceAll(result, escapedTildePlaceholder, "~")
	result = strings.ReplaceAll(result, `\~`, "~")

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

	e.logger.DebugCat(CatCommand,"Found %d top-level braces to evaluate", len(braces))

	// Execute all braces and collect their results
	evaluations := make([]*BraceEvaluation, len(braces))
	hasAsync := false

	for i, brace := range braces {
		e.logger.DebugCat(CatCommand,"Evaluating brace %d: line=%d, column=%d", i, brace.StartLine, brace.StartColumn)
		e.logger.DebugCat(CatCommand,"Brace content: \"{%s}\"", brace.Content)

		// Create a child state with shared variables but isolated result storage
		// This prevents async braces from racing on result storage while still sharing variables

		if ctx == nil {
			// Handle the nil case - either return an error or use a default
			return str // nil, fmt.Errorf("context cannot be nil")
		}
		braceState := NewExecutionStateFromSharedVars(ctx.ExecutionState)
		// Mark this state as being inside a brace expression
		// Commands can check this to return values instead of emitting side effects to #out
		braceState.InBraceExpression = true

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

		e.logger.DebugCat(CatCommand,"Brace offsets: line=%d, column=%d", newLineOffset, newColumnOffset)

		// Create substitution context using the child state
		braceSubstitutionCtx := &SubstitutionContext{
			Args:              ctx.Args,
			ExecutionState:    braceState,
			MacroContext:      ctx.MacroContext,
			CurrentLineOffset: newLineOffset,
			CurrentColumnOffset: newColumnOffset,
			Filename:          ctx.Filename,
			CapturedModuleEnv: ctx.CapturedModuleEnv, // Inherit for handler caching
		}

		// Execute the brace content with the child state (isolated result storage, shared variables)
		var executeResult Result

		// Check for cached parsed commands for this brace content
		var cachedCmds []*ParsedCommand
		if ctx.CurrentParsedCommand != nil && ctx.CurrentParsedCommand.CachedBraces != nil {
			cachedCmds = ctx.CurrentParsedCommand.CachedBraces[brace.Content]
		}

		if cachedCmds != nil {
			// Use cached parsed commands
			e.logger.DebugCat(CatCommand, "Using cached brace: {%s}", brace.Content)
			executeResult = e.ExecuteParsedCommands(
				cachedCmds,
				braceState,
				braceSubstitutionCtx,
				newLineOffset,
				newColumnOffset,
			)
		} else {
			// Parse and execute fresh
			executeResult = e.ExecuteWithState(
				brace.Content,
				braceState,
				braceSubstitutionCtx,
				ctx.Filename,
				newLineOffset,
				newColumnOffset,
			)
		}

		// Track that a brace was evaluated (for get_substatus)
		ctx.BracesEvaluated++

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
			e.logger.DebugCat(CatCommand,"Brace %d returned async token: %s", i, evaluations[i].TokenID)
		} else if earlyReturn, ok := executeResult.(EarlyReturn); ok {
			// Handle early return (ret command) - treat as successful completion
			// Use the formal result from the EarlyReturn
			evaluations[i].Completed = true

			// Use the result from EarlyReturn if present
			if earlyReturn.HasResult {
				evaluations[i].Result = earlyReturn.Result
				e.logger.DebugCat(CatCommand,"Early return has result: %v", earlyReturn.Result)
			} else if hasCapturedResult {
				evaluations[i].Result = capturedResult
			}

			// Only mark as failed if ret has no result AND status is false
			// When ret provides a value (like `ret ""`), the brace succeeds with that value
			// regardless of the previous command's status
			hasReturnValue := earlyReturn.HasResult || hasCapturedResult
			if !hasReturnValue && !bool(earlyReturn.Status) {
				evaluations[i].Failed = true
				e.logger.DebugCat(CatCommand,"Brace %d completed with early return (failure, no result)", i)
			} else {
				e.logger.DebugCat(CatCommand,"Brace %d completed with early return (success)", i)
			}

			// Handle ownership for result references
			if earlyReturn.HasResult {
				resultRefs := braceState.ExtractObjectReferences(earlyReturn.Result)
				for _, refID := range resultRefs {
					braceState.mu.Lock()
					childCount := braceState.ownedObjects[refID]
					braceState.mu.Unlock()

					if childCount > 0 {
						// Check if parent already owns this object
						ctx.ExecutionState.mu.Lock()
						parentOwns := ctx.ExecutionState.ownedObjects[refID] > 0
						ctx.ExecutionState.mu.Unlock()

						if !parentOwns {
							// Parent doesn't own it yet - claim ownership
							ctx.ExecutionState.ClaimObjectReference(refID)
							e.logger.DebugCat(CatCommand,"Parent claimed ownership of object %d from brace result", refID)
						}
					}
				}
			}
		} else {
			// Synchronous completion
			evaluations[i].Completed = true

			// Track brace failures in the substitution context
			// This allows assignment to propagate the status from brace expressions
			if boolStatus, ok := executeResult.(BoolStatus); ok && !bool(boolStatus) {
				ctx.BraceFailureCount++
				e.logger.DebugCat(CatCommand,"Brace %d returned false status, failure count now: %d", i, ctx.BraceFailureCount)
			}

			// Determine the result value first
			// Commands that return a BoolStatus without calling SetResult should use the
			// boolean value itself as the result (e.g., {false} returns "false", {true} returns "true")
			if hasCapturedResult {
				evaluations[i].Result = capturedResult
				e.logger.DebugCat(CatCommand,"Brace %d has captured result: %s", i, evaluations[i].Result)
			} else if boolStatus, ok := executeResult.(BoolStatus); ok {
				evaluations[i].Result = fmt.Sprintf("%v", bool(boolStatus))
				e.logger.DebugCat(CatCommand,"Brace %d using boolean status as result: %s", i, evaluations[i].Result)
			}

			// Only mark as truly failed if we got an error result, not just a false status
			// Commands like {false}, {eq 1, 2}, {token_valid ~exhausted} return false status
			// with a valid boolean result - they should NOT abort the parent command
			hasResult := hasCapturedResult || executeResult != nil
			if !hasResult {
				evaluations[i].Failed = true
				e.logger.DebugCat(CatCommand,"Brace %d completed with no result (failure)", i)
			} else {
				e.logger.DebugCat(CatCommand,"Brace %d completed synchronously with success", i)
			}
			// end part that used to be in an else

			// Handle ownership for ALL references owned by the brace state
			// Since the brace shares variables with the parent, any objects stored in
			// those variables need to be owned by the parent before we release the brace's claims.
			// This includes:
			// 1. Objects returned as results (e.g., from gui_console)
			// 2. Objects stored in variables during the brace (e.g., #out from destructuring)
			braceState.mu.Lock()
			ownedByBrace := make(map[int]int)
			for refID, count := range braceState.ownedObjects {
				ownedByBrace[refID] = count
			}
			braceState.mu.Unlock()

			for refID, childCount := range ownedByBrace {
				if childCount > 0 {
					// Check if parent already owns this object
					ctx.ExecutionState.mu.Lock()
					parentOwns := ctx.ExecutionState.ownedObjects[refID] > 0
					ctx.ExecutionState.mu.Unlock()

					if !parentOwns {
						// Parent doesn't own it yet - transfer ownership
						// Claim for parent (increments global refcount)
						ctx.ExecutionState.ClaimObjectReference(refID)
					}
					// If parent already owns it, the child's claim will be released below
				}
			}

			// Clean up brace state references
			braceState.ReleaseAllReferences()
			braceState.Recycle(false, false) // Doesn't own variables or bubbleMap (shared with parent)
		}
	}

	// If any evaluation is async, we need to coordinate
	if hasAsync {
		e.logger.DebugCat(CatCommand,"At least one brace is async, creating coordinator token")

		// We need to return a special marker that tells the caller we're suspending
		// The caller (executeSingleCommand) will need to handle this
		coordinatorToken := e.RequestBraceCoordinatorToken(
			evaluations,
			str,
			ctx,
			func(finalString string, success bool) Result {
				// This callback will be invoked when all braces complete
				// For now, we need to signal back through the token system
				e.logger.DebugCat(CatCommand,"Brace coordinator completed: success=%v, string=%s", success, finalString)
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

			e.logger.WarnCat(CatCommand,errorMsg, eval.Position, sourceContext)
			e.logger.DebugCat(CatCommand,"Synchronous brace evaluation %d failed, aborting command", i)
			// Return special marker to indicate brace failure
			return "\x00PAWS_FAILED\x00"
		}
	}

	// Substitute all results immediately
	result := e.substituteAllBraces(str, evaluations, ctx.ExecutionState)
	e.logger.DebugCat(CatCommand,"All braces synchronous, substituted result: %s", result)

	return result
}

// substituteTildeExpressions substitutes ~varname and ?varname patterns in strings
// ~varname substitutes with the variable value
// ?varname substitutes with "true" if variable exists, "false" otherwise
func (e *Executor) substituteTildeExpressions(str string, state *ExecutionState, position *SourcePosition) string {
	// Placeholder for escaped tildes - must match the one used in applySubstitution
	const escapedTildePlaceholder = "\x00TILDE\x00"

	if state == nil {
		return str
	}

	tildes := e.findAllTildeLocations(str)

	if len(tildes) == 0 {
		return str
	}

	// Build result by iterating through tilde locations and substituting
	runes := []rune(str)
	result := make([]rune, 0, len(runes))

	lastEnd := 0
	for _, tilde := range tildes {
		// Append everything before this tilde/question
		result = append(result, runes[lastEnd:tilde.StartPos]...)

		// Look up the variable - first in local variables, then in ObjectsModule
		value, exists := state.GetVariable(tilde.VarName)
		if !exists && state.moduleEnv != nil {
			// Fallback: check ObjectsModule only (not ObjectsInherited)
			state.moduleEnv.mu.RLock()
			if obj, found := state.moduleEnv.ObjectsModule[tilde.VarName]; found {
				value = obj
				exists = true
			}
			state.moduleEnv.mu.RUnlock()
		}

		if tilde.IsQuestion {
			// ? expression - substitute "true" or "false" based on existence
			// Also check if value is undefined
			if exists {
				if sym, ok := value.(Symbol); ok {
					if string(sym) == UndefinedMarker || string(sym) == "undefined" {
						exists = false
					}
				}
			}
			if exists {
				result = append(result, []rune("true")...)
			} else {
				result = append(result, []rune("false")...)
			}
		} else {
			// ~ expression - substitute with value
			if exists {
				// Resolve any object markers to get display value
				resolved := e.resolveValue(value)
				var valueStr string
				// Handle StoredList and StoredBytes specially to format contents
				if list, ok := resolved.(StoredList); ok {
					valueStr = formatListForDisplay(list)
				} else if bytes, ok := resolved.(StoredBytes); ok {
					valueStr = bytes.String()
				} else {
					valueStr = fmt.Sprintf("%v", resolved)
				}
				// Since tildes are only found inside double-quoted strings,
				// we need to escape backslashes and quotes in the substituted value
				// to prevent breaking the quote structure.
				// IMPORTANT: These must come BEFORE tilde escaping, otherwise the
				// placeholder's \x00 bytes would get double-escaped to \\x00
				valueStr = strings.ReplaceAll(valueStr, `\`, `\\`)
				valueStr = strings.ReplaceAll(valueStr, `"`, `\"`)
				// Escape tildes in the resolved value to prevent tilde injection
				// This ensures user input containing tildes doesn't get interpreted
				// as variable references when the result string is re-parsed
				valueStr = strings.ReplaceAll(valueStr, "~", escapedTildePlaceholder)
				result = append(result, []rune(valueStr)...)
			} else {
				// Variable not found - log error and leave empty
				e.logger.CommandError(CatVariable, "", fmt.Sprintf("Variable not found: %s", tilde.VarName), position)
			}
		}

		lastEnd = tilde.EndPos + 1
	}

	// Append any remaining content after the last tilde
	if lastEnd < len(runes) {
		result = append(result, runes[lastEnd:]...)
	}

	return string(result)
}

// findAllTopLevelBraces finds all brace expressions at the current nesting level
// Also detects ${...} for unescape mode
func (e *Executor) findAllTopLevelBraces(str string, ctx *SubstitutionContext) []*BraceLocation {
	var braces []*BraceLocation

	braceDepth := 0
	parenDepth := 0
	braceStart := -1
	braceIsUnescape := false
	inQuote := false
	var quoteChar rune

	line := 1
	column := 1
	braceStartLine := 1
	braceStartColumn := 1

	runes := []rune(str)
	for i := 0; i < len(runes); i++ {
		char := runes[i]

		if char == '\n' {
			line++
			column = 1
		} else {
			column++
		}

		// Handle escape sequences - skip the escaped character
		if char == '\\' && i+1 < len(runes) {
			i++ // Skip the escaped character
			if runes[i] == '\n' {
				line++
				column = 1
			} else {
				column++
			}
			continue
		}

		// Track quote state (only when not inside brace expressions)
		if braceDepth == 0 {
			if !inQuote && (char == '"' || char == '\'') {
				inQuote = true
				quoteChar = char
				continue
			}
			if inQuote && char == quoteChar {
				inQuote = false
				quoteChar = 0
				continue
			}
		}

		// Only track parentheses when NOT inside quotes
		if !inQuote {
			if char == '(' {
				parenDepth++
				continue
			} else if char == ')' {
				parenDepth--
				continue
			}
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

// findAllTildeLocations finds all ~varname patterns inside quoted strings for interpolation
// Variable names end at: space, special characters, or explicit semicolon (which is consumed)
// Escaped tildes (\~) are skipped
// Only processes tildes inside double-quoted strings (not bare tildes or single-quoted strings)
// RESPECTS PARENTHESIS PROTECTION: Does not substitute tildes inside parenthetical groups
// Note: This runs AFTER brace substitution, so we don't need to track braces
func (e *Executor) findAllTildeLocations(str string) []*TildeLocation {
	var tildes []*TildeLocation

	runes := []rune(str)
	i := 0
	inDoubleQuote := false
	parenDepth := 0

	for i < len(runes) {
		char := runes[i]

		// Handle escape sequences - skip escaped characters
		if char == '\\' && i+1 < len(runes) {
			i += 2 // Skip both backslash and next character
			continue
		}

		// Track parenthesis depth (outside quotes)
		if !inDoubleQuote {
			if char == '(' {
				parenDepth++
				i++
				continue
			} else if char == ')' {
				parenDepth--
				i++
				continue
			}
		}

		// Track double quote state (only outside parens)
		if char == '"' && parenDepth == 0 {
			inDoubleQuote = !inDoubleQuote
			i++
			continue
		}

		// Skip single-quoted strings entirely (no interpolation in single quotes)
		if char == '\'' && !inDoubleQuote && parenDepth == 0 {
			i++
			for i < len(runes) {
				if runes[i] == '\\' && i+1 < len(runes) {
					i += 2 // Skip escape sequence
				} else if runes[i] == '\'' {
					i++ // Skip closing quote
					break
				} else {
					i++
				}
			}
			continue
		}

		// Process tildes (~) and question marks (?) inside double-quoted strings AND outside parentheses
		// ~ substitutes the value, ? substitutes "true" or "false" based on existence
		if (char == '~' || char == '?') && inDoubleQuote && parenDepth == 0 && i+1 < len(runes) {
			isQuestion := char == '?'
			tildeStart := i
			i++ // Move past the ~ or ?

			// Collect variable name characters (letters, digits, underscore, or # prefix)
			// The # prefix is allowed for ObjectsModule items like #stdin, #stdout
			varStart := i
			isFirst := true
			for i < len(runes) {
				c := runes[i]
				if unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || (isFirst && c == '#') {
					i++
					isFirst = false
				} else {
					break
				}
			}

			// Must have at least one character for a valid variable name
			if i > varStart {
				varName := string(runes[varStart:i])
				endPos := i - 1 // Last char of varname
				hasSemicolon := false

				// Check for explicit semicolon terminator
				if i < len(runes) && runes[i] == ';' {
					hasSemicolon = true
					endPos = i // Include semicolon in the range to replace
					i++        // Consume the semicolon
				}

				tildes = append(tildes, &TildeLocation{
					StartPos:     tildeStart,
					EndPos:       endPos,
					VarName:      varName,
					HasSemicolon: hasSemicolon,
					IsQuestion:   isQuestion,
				})
			}
			continue
		}

		i++
	}

	return tildes
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
		// IMPORTANT: Prioritize eval.Result over state.GetResult() because:
		// 1. For EarlyReturn, the result is explicitly stored in eval.Result
		// 2. The braceState might inherit parent's result (from NewExecutionStateFromSharedVars)
		//    which would be the wrong value
		var rawValue interface{}
		if eval.Result != nil {
			rawValue = eval.Result
		} else if eval.State != nil && eval.State.HasResult() {
			rawValue = eval.State.GetResult()
		}

		// Format the result based on type
		var resultValue string
		if eval.Location.IsUnescape {
			// ${...} - unwrap outer parens (splat), but still escape quotes when inside quotes
			insideQuotes := e.isInsideQuotes(originalString, eval.Location.StartPos)

			// For StoredList, unwrap outer parens (just like ParenGroup)
			if list, ok := rawValue.(StoredList); ok {
				resultValue = e.formatListItems(list)
			} else if sym, ok := rawValue.(Symbol); ok {
				// Check if it's an object marker
				if objType, objID := parseObjectMarker(string(sym)); objID >= 0 {
					// Resolve and display the object
					if actualValue, exists := e.getObject(objID); exists {
						switch objType {
						case "list":
							if list, ok := actualValue.(StoredList); ok {
								resultValue = e.formatListItems(list)
							} else {
								resultValue = fmt.Sprintf("<%s %d>", objType, objID)
							}
						case "block":
							if storedBlock, ok := actualValue.(StoredBlock); ok {
								resultValue = string(storedBlock)
							} else {
								resultValue = fmt.Sprintf("<%s %d>", objType, objID)
							}
						case "str":
							if storedStr, ok := actualValue.(StoredString); ok {
								resultValue = string(storedStr)
							} else {
								resultValue = fmt.Sprintf("<%s %d>", objType, objID)
							}
						default:
							resultValue = fmt.Sprintf("<%s %d>", objType, objID)
						}
					} else {
						resultValue = fmt.Sprintf("<invalid-%s-ref:%d>", objType, objID)
					}
				} else {
					resultValue = string(sym)
				}
			} else {
				resultValue = fmt.Sprintf("%v", rawValue)
			}

			// If inside quotes, escape quotes and backslashes in the result
			if insideQuotes {
				resultValue = e.escapeQuotesAndBackslashes(resultValue)
			}
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

// formatBraceResult formats a brace evaluation result for substitution
// Takes the original string and brace position to detect quote context
// formatBraceResult formats a brace evaluation result for substitution
// Takes the original string and brace position to detect quote context
//
// CRITICAL: Object markers (like \x00LIST:7\x00) are handled based on context:
// - Inside quoted strings: Resolve and format for display (string interpolation)
//   Example: echo "Result: {get_result}" -> "Result: (a, b, c)"
// - Outside quotes: Preserve marker unchanged (pass by reference)
//   Example: set x, {get_result} -> x = \x00LIST:7\x00
//
// This ensures nested structures maintain shared storage via reference passing
// while still supporting human-readable display in string contexts.
func (e *Executor) formatBraceResult(value interface{}, originalString string, bracePos int, state *ExecutionState) string {
	// Placeholder for escaped tildes - must match the one used in applySubstitution
	const escapedTildePlaceholder = "\x00TILDE\x00"

	// Handle nil specially - output as bare word "nil"
	if value == nil {
		fmt.Printf("DEBUG formatBraceResult: value is nil\n")
		return "nil"
	}

	// Check if we're inside a quoted string context
	insideQuotes := e.isInsideQuotes(originalString, bracePos)
	fmt.Printf("DEBUG formatBraceResult: value type=%T, value=%v, insideQuotes=%v, originalString=%q\n", value, value, insideQuotes, originalString)

	// If it's a Symbol that might be a marker, return it unchanged to preserve the reference
	if sym, ok := value.(Symbol); ok {
		// Handle undefined marker specially
		if string(sym) == UndefinedMarker {
			if insideQuotes {
				return "<undefined>"
			}
			return "undefined"
		}
		if objType, objID := parseObjectMarker(string(sym)); objID >= 0 {
			// It's an object marker - pass it through unchanged
			// Don't resolve and re-store, that would create duplicate storage entries!
			if insideQuotes {
				// Inside quotes, need to display the object
				if actualValue, exists := e.getObject(objID); exists {
					// Format based on object type
					switch objType {
					case "list":
						if list, ok := actualValue.(StoredList); ok {
							return formatListForDisplay(list)
						}
					case "str":
						// Resolve and display string content
						if storedStr, ok := actualValue.(StoredString); ok {
							return e.escapeQuotesAndBackslashes(string(storedStr))
						}
					case "block":
						// Resolve and display block content
						if storedBlock, ok := actualValue.(StoredBlock); ok {
							return e.escapeQuotesAndBackslashes(string(storedBlock))
						}
					}
					// Fallback for other types
					return fmt.Sprintf("%v", actualValue)
				}
				// Marker not found, display as broken reference
				return fmt.Sprintf("<invalid-%s-ref:%d>", objType, objID)
			}
			// Outside quotes: return the marker as-is to preserve the reference
			return string(sym)
		} else {
			// Not a marker, just a regular symbol
			return string(sym)
		}
	}

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
			// Also escape tildes to prevent tilde injection
			result := e.escapeQuotesAndBackslashes(string(v))
			result = strings.ReplaceAll(result, "~", escapedTildePlaceholder)
			return result
		}
		// Outside quotes: preserve as parentheses
		// Also escape tildes to prevent tilde injection
		content := strings.ReplaceAll(string(v), "~", escapedTildePlaceholder)
		return "(" + content + ")"
	case QuotedString:
		if insideQuotes {
			// Inside quotes: unwrap and escape only quotes/backslashes
			// Also escape tildes to prevent tilde injection
			result := e.escapeQuotesAndBackslashes(string(v))
			result = strings.ReplaceAll(result, "~", escapedTildePlaceholder)
			return result
		}
		// Outside quotes: preserve as quoted string, escaping internal quotes
		// Also escape tildes to prevent tilde injection
		escaped := e.escapeQuotesAndBackslashes(string(v))
		escaped = strings.ReplaceAll(escaped, "~", escapedTildePlaceholder)
		return "\"" + escaped + "\""
	case Symbol:
		// If it's a Symbol that might be a marker, resolve it first for proper formatting
		if sym, ok := value.(Symbol); ok {
			if _, objID := parseObjectMarker(string(sym)); objID >= 0 {
				if insideQuotes {
					// Inside quotes: resolve and format for display
					if _, exists := e.getObject(objID); exists {
						// Object exists - return the symbol as string for now
						// TODO: Consider formatting the resolved value
						return string(sym)
					} else {
						return string(sym)
					}
				} else {
					// Outside quotes: keep as marker (already properly formatted)
					// Don't resolve and re-store - that would create a duplicate!
					return string(sym)
				}
			} else {
				// Not a marker, just a regular symbol
				return string(sym)
			}
		}
		return string(v)
	case string:
		if insideQuotes {
			// Inside quotes: escape only quotes/backslashes
			// Also escape tildes to prevent tilde injection
			result := e.escapeQuotesAndBackslashes(v)
			result = strings.ReplaceAll(result, "~", escapedTildePlaceholder)
			return result
		}
		// Outside quotes: wrap in quotes to preserve the string value
		// (don't escape spaces/special chars - that's for bare words only)
		// Also escape tildes to prevent tilde injection
		escaped := e.escapeQuotesAndBackslashes(v)
		escaped = strings.ReplaceAll(escaped, "~", escapedTildePlaceholder)
		return "\"" + escaped + "\""
	case StoredList:
		if insideQuotes {
			// Inside quotes: format as readable list display
			return formatListForDisplay(v)
		}
		// Outside quotes: use a special marker that preserves the object
		// Format: \x00LIST:index\x00 where index is stored in the execution state
		id := e.storeObject(value, "list")
		// The creating context claims the first reference
		if state != nil {
			state.ClaimObjectReference(id)
		}
		return fmt.Sprintf("\x00LIST:%d\x00", id)
	case StoredBytes:
		if insideQuotes {
			// Inside quotes: format as hex display
			return v.String()
		}
		// Outside quotes: use a special marker that preserves the object
		// Format: \x00BYTES:index\x00 where index is stored in the execution state
		id := e.storeObject(value, "bytes")
		// The creating context claims the first reference
		if state != nil {
			state.ClaimObjectReference(id)
		}
		return fmt.Sprintf("\x00BYTES:%d\x00", id)
	case *StoredFile:
		if insideQuotes {
			// Inside quotes: show file path
			return v.Path
		}
		// Outside quotes: use a special marker that preserves the object
		// Format: \x00FILE:index\x00 where index is stored in the execution state
		id := e.storeObject(value, "file")
		// The creating context claims the first reference
		if state != nil {
			state.ClaimObjectReference(id)
		}
		return fmt.Sprintf("\x00FILE:%d\x00", id)
	case StoredStruct:
		if insideQuotes {
			// Inside quotes: format as display string
			return v.String()
		}
		// Outside quotes: use a special marker that preserves the object
		// Format: \x00STRUCT:index\x00 where index is stored in the execution state
		id := e.storeObject(value, "struct")
		// The creating context claims the first reference
		if state != nil {
			state.ClaimObjectReference(id)
		}
		return fmt.Sprintf("\x00STRUCT:%d\x00", id)
	// Note: StructDef is now a StoredList, handled above
	case int64, float64:
		// Numbers as-is
		return fmt.Sprintf("%v", v)
	default:
		// Unknown type - convert to string and wrap in quotes outside quote context
		str := fmt.Sprintf("%v", v)
		if insideQuotes {
			result := e.escapeQuotesAndBackslashes(str)
			result = strings.ReplaceAll(result, "~", escapedTildePlaceholder)
			return result
		}
		// Wrap in quotes to preserve value
		// Also escape tildes to prevent tilde injection
		escaped := e.escapeQuotesAndBackslashes(str)
		escaped = strings.ReplaceAll(escaped, "~", escapedTildePlaceholder)
		return "\"" + escaped + "\""
	}
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

// substituteDollarArgs substitutes $1, $2, etc. with quote-awareness
// When $N is inside quotes, just insert the content (no extra quotes)
// When $N is outside quotes, preserve quotes around strings with spaces
// Properly tracks braces, parentheses, and escape sequences like the parser
func (e *Executor) substituteDollarArgs(str string, ctx *SubstitutionContext) string {
	runes := []rune(str)
	var result strings.Builder
	result.Grow(len(str))

	inQuote := false
	var quoteChar rune
	braceDepth := 0
	parenDepth := 0
	i := 0

	for i < len(runes) {
		char := runes[i]

		// Handle escape sequences - skip the escaped character
		if char == '\\' && i+1 < len(runes) {
			result.WriteRune(char)
			result.WriteRune(runes[i+1])
			i += 2
			continue
		}

		// Track quote state - quotes inside parens still count as "in quote" for $N context
		// But quotes inside braces don't affect our tracking (braces are already resolved)
		if !inQuote && (char == '"' || char == '\'') {
			inQuote = true
			quoteChar = char
			result.WriteRune(char)
			i++
			continue
		}
		if inQuote && char == quoteChar {
			inQuote = false
			quoteChar = 0
			result.WriteRune(char)
			i++
			continue
		}

		// Track parentheses (only when not in quotes)
		if !inQuote {
			if char == '(' {
				parenDepth++
				result.WriteRune(char)
				i++
				continue
			} else if char == ')' {
				parenDepth--
				result.WriteRune(char)
				i++
				continue
			}
		}

		// Track braces (only when not in quotes and not inside parens)
		if !inQuote && parenDepth == 0 {
			if char == '{' {
				braceDepth++
				result.WriteRune(char)
				i++
				continue
			} else if char == '}' {
				braceDepth--
				result.WriteRune(char)
				i++
				continue
			}
		}

		// Check for $N pattern
		if char == '$' && i+1 < len(runes) && runes[i+1] >= '0' && runes[i+1] <= '9' {
			// Parse the number
			numStart := i + 1
			numEnd := numStart
			for numEnd < len(runes) && runes[numEnd] >= '0' && runes[numEnd] <= '9' {
				numEnd++
			}

			indexStr := string(runes[numStart:numEnd])
			index, err := strconv.Atoi(indexStr)
			if err != nil {
				// Not a valid number, output as-is
				result.WriteRune(char)
				i++
				continue
			}

			// Get the argument value
			var argValue interface{}
			found := false

			// Try to get from $@ list first
			if ctx.ExecutionState != nil {
				if argsVar, exists := ctx.ExecutionState.GetVariable("$@"); exists {
					if sym, ok := argsVar.(Symbol); ok {
						marker := string(sym)
						if objType, objID := parseObjectMarker(marker); objType == "list" && objID >= 0 {
							if listObj, exists := e.getObject(objID); exists {
								if storedList, ok := listObj.(StoredList); ok {
									// index is 1-based, convert to 0-based
									item := storedList.Get(index - 1)
									if item != nil {
										argValue = item
										found = true
									}
								}
							}
						}
					}
				}
			}

			// Fallback to ctx.Args
			if !found {
				idx := index - 1 // Convert to 0-based
				if idx >= 0 && idx < len(ctx.Args) {
					argValue = ctx.Args[idx]
					found = true
				}
			}

			if found {
				// Format based on quote context
				var substitution string
				if inQuote {
					// Inside quotes: just insert content, no extra quotes
					substitution = e.formatArgumentForQuotedContext(argValue)
				} else {
					// Outside quotes: may need to add quotes to preserve token boundaries
					substitution = e.formatArgumentForSubstitution(argValue)
				}
				result.WriteString(substitution)
			} else {
				// Argument not found, leave $N as-is
				result.WriteString(string(runes[i:numEnd]))
			}

			i = numEnd
			continue
		}

		// Regular character
		result.WriteRune(char)
		i++
	}

	return result.String()
}

// ParseSubstitutionTemplate parses a string into a SubstitutionTemplate for efficient runtime substitution
// This pre-computes all substitution points so runtime only needs to iterate and fill in values
func (e *Executor) ParseSubstitutionTemplate(str string, filename string) *SubstitutionTemplate {
	template := &SubstitutionTemplate{
		Segments: make([]TemplateSegment, 0, 4), // Pre-allocate for typical case
	}

	runes := []rune(str)
	if len(runes) == 0 {
		return template
	}

	var literalStart int
	inQuote := false
	var quoteChar rune
	braceDepth := 0
	parenDepth := 0
	i := 0

	// Helper to flush accumulated literal text
	flushLiteral := func(end int) {
		if end > literalStart {
			template.Segments = append(template.Segments, TemplateSegment{
				Type:    SegmentLiteral,
				Literal: string(runes[literalStart:end]),
			})
		}
	}

	for i < len(runes) {
		char := runes[i]

		// Handle escape sequences - include in literal and skip interpretation
		// The escaped sequence stays in the literal for later processing
		if char == '\\' && i+1 < len(runes) {
			nextChar := runes[i+1]
			// For \$ and \~, skip past both to avoid treating as substitution
			// They'll remain in the literal as \$ or \~ for later processing
			if nextChar == '$' || nextChar == '~' || nextChar == '\\' || nextChar == '{' {
				i += 2
				continue
			}
			// For other escapes, just move past the backslash
			i++
			continue
		}

		// Track quote state (only at top level - not inside braces/parens)
		if braceDepth == 0 && parenDepth == 0 {
			if !inQuote && (char == '"' || char == '\'') {
				inQuote = true
				quoteChar = char
				i++
				continue
			}
			if inQuote && char == quoteChar {
				inQuote = false
				quoteChar = 0
				i++
				continue
			}
		}

		// Track parentheses (only when not in quotes)
		if !inQuote && braceDepth == 0 {
			if char == '(' {
				parenDepth++
				i++
				continue
			} else if char == ')' {
				parenDepth--
				i++
				continue
			}
		}

		// Check for brace expressions at top level (outside parens)
		if parenDepth == 0 && char == '{' {
			// Check if this is ${...} unescape mode
			isUnescape := i > 0 && runes[i-1] == '$'
			braceStart := i
			if isUnescape {
				braceStart = i - 1
			}

			// Find matching closing brace
			braceDepth = 1
			contentStart := i + 1
			j := i + 1
			for j < len(runes) && braceDepth > 0 {
				if runes[j] == '\\' && j+1 < len(runes) {
					j += 2
					continue
				}
				if runes[j] == '{' {
					braceDepth++
				} else if runes[j] == '}' {
					braceDepth--
				}
				j++
			}

			if braceDepth == 0 {
				// Found complete brace expression
				braceContent := string(runes[contentStart : j-1])

				// Flush any literal before this brace
				flushLiteral(braceStart)

				// Parse the brace content into AST
				parser := NewParser(braceContent, filename)
				cleanedBody := parser.RemoveComments(braceContent)
				normalizedBody := parser.NormalizeKeywords(cleanedBody)
				cmds, err := parser.ParseCommandSequence(normalizedBody)
				if err == nil {
					// Recursively pre-cache templates in nested commands
					for _, cmd := range cmds {
						e.PreCacheCommandTemplates(cmd, filename)
					}
				}

				template.Segments = append(template.Segments, TemplateSegment{
					Type:       SegmentBrace,
					BraceAST:   cmds,
					BraceRaw:   braceContent,
					IsUnescape: isUnescape,
					InQuote:    inQuote && quoteChar == '"',
				})
				template.HasBraceSub = true

				literalStart = j
				i = j
				braceDepth = 0
				continue
			}
			braceDepth = 0 // Reset if unmatched
		}

		// Check for dollar substitutions (only when we might be in macro context)
		if char == '$' && i+1 < len(runes) {
			nextChar := runes[i+1]

			// $* - all args comma-separated
			if nextChar == '*' {
				flushLiteral(i)
				template.Segments = append(template.Segments, TemplateSegment{
					Type: SegmentDollarStar,
				})
				template.HasDollarSub = true
				literalStart = i + 2
				i += 2
				continue
			}

			// $@ - all args as list
			if nextChar == '@' {
				flushLiteral(i)
				template.Segments = append(template.Segments, TemplateSegment{
					Type: SegmentDollarAt,
				})
				template.HasDollarSub = true
				literalStart = i + 2
				i += 2
				continue
			}

			// $# - arg count
			if nextChar == '#' {
				flushLiteral(i)
				template.Segments = append(template.Segments, TemplateSegment{
					Type: SegmentDollarHash,
				})
				template.HasDollarSub = true
				literalStart = i + 2
				i += 2
				continue
			}

			// $N - numbered arg
			if nextChar >= '0' && nextChar <= '9' {
				numStart := i + 1
				numEnd := numStart
				for numEnd < len(runes) && runes[numEnd] >= '0' && runes[numEnd] <= '9' {
					numEnd++
				}
				argNum, err := strconv.Atoi(string(runes[numStart:numEnd]))
				if err == nil {
					flushLiteral(i)
					template.Segments = append(template.Segments, TemplateSegment{
						Type:    SegmentDollarArg,
						ArgNum:  argNum,
						InQuote: inQuote && quoteChar == '"',
					})
					template.HasDollarSub = true
					literalStart = numEnd
					i = numEnd
					continue
				}
			}
		}

		// Check for tilde/question substitutions inside double quotes
		if (char == '~' || char == '?') && inQuote && quoteChar == '"' && parenDepth == 0 && i+1 < len(runes) {
			isQuestion := char == '?'
			varStart := i + 1

			// Collect variable name
			j := varStart
			isFirst := true
			for j < len(runes) {
				c := runes[j]
				if isFirst && c == '#' {
					j++
					isFirst = false
					continue
				}
				if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
					j++
					isFirst = false
				} else {
					break
				}
			}

			if j > varStart {
				varName := string(runes[varStart:j])
				endPos := j

				// Check for semicolon terminator
				if j < len(runes) && runes[j] == ';' {
					endPos = j + 1
				}

				flushLiteral(i)
				template.Segments = append(template.Segments, TemplateSegment{
					Type:       SegmentTildeVar,
					VarName:    varName,
					IsQuestion: isQuestion,
				})
				template.HasTildeSub = true
				literalStart = endPos
				i = endPos
				continue
			}
		}

		i++
	}

	// Flush remaining literal
	flushLiteral(len(runes))

	return template
}

// PreCacheCommandTemplates pre-parses substitution templates for a command's arguments
func (e *Executor) PreCacheCommandTemplates(cmd *ParsedCommand, filename string) {
	if cmd == nil {
		return
	}

	// Check if command name needs templating
	if containsSubstitution(cmd.Command) {
		cmd.CommandTemplate = e.ParseSubstitutionTemplate(cmd.Command, filename)
	}

	// Pre-parse templates for string arguments
	if len(cmd.Arguments) > 0 {
		cmd.ArgTemplates = make([]*SubstitutionTemplate, len(cmd.Arguments))
		for i, arg := range cmd.Arguments {
			switch v := arg.(type) {
			case string:
				if containsSubstitution(v) {
					cmd.ArgTemplates[i] = e.ParseSubstitutionTemplate(v, filename)
				}
			case QuotedString:
				if containsSubstitution(string(v)) {
					cmd.ArgTemplates[i] = e.ParseSubstitutionTemplate(string(v), filename)
				}
			case ParenGroup:
				if containsSubstitution(string(v)) {
					cmd.ArgTemplates[i] = e.ParseSubstitutionTemplate(string(v), filename)
				}
			}
		}
	}

	// Pre-parse templates for named arguments
	for _, namedArg := range cmd.NamedArgs {
		switch v := namedArg.(type) {
		case string:
			if containsSubstitution(v) {
				// Named args don't have a separate template storage yet
				// Could add later if needed
			}
		case QuotedString:
			if containsSubstitution(string(v)) {
				// Named args don't have a separate template storage yet
			}
		}
	}
}

// containsSubstitution checks if a string might contain substitution markers
// This is a quick check to avoid parsing strings that don't need it
// Returns false if the string contains escape sequences that would complicate templating
func containsSubstitution(s string) bool {
	hasSubstitution := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		// If there are escape sequences, don't use templating - too complex
		// Escape sequences need runtime processing
		if c == '\\' && i+1 < len(s) {
			next := s[i+1]
			if next == '$' || next == '~' || next == '\\' || next == '{' {
				return false // Fall back to original substitution
			}
		}
		if c == '$' || c == '~' || c == '?' || c == '{' {
			hasSubstitution = true
		}
	}
	return hasSubstitution
}

// ApplyTemplate executes a pre-parsed substitution template with the given context
// Returns the substituted string and a flag indicating if async processing is needed
func (e *Executor) ApplyTemplate(template *SubstitutionTemplate, ctx *SubstitutionContext) (string, bool) {
	if template == nil || len(template.Segments) == 0 {
		return "", false
	}

	// Fast path: single literal segment
	if len(template.Segments) == 1 && template.Segments[0].Type == SegmentLiteral {
		return template.Segments[0].Literal, false
	}

	var result strings.Builder
	result.Grow(128) // Pre-allocate for typical result

	for _, seg := range template.Segments {
		switch seg.Type {
		case SegmentLiteral:
			result.WriteString(seg.Literal)

		case SegmentTildeVar:
			value := e.lookupTildeVar(seg.VarName, seg.IsQuestion, ctx)
			result.WriteString(value)

		case SegmentDollarArg:
			if ctx.MacroContext != nil {
				value := e.lookupDollarArgWithContext(seg.ArgNum, seg.InQuote, ctx)
				result.WriteString(value)
			} else {
				// Not in macro context, output as-is
				result.WriteString(fmt.Sprintf("$%d", seg.ArgNum))
			}

		case SegmentDollarStar:
			if ctx.MacroContext != nil {
				value := e.formatAllArgsComma(ctx)
				result.WriteString(value)
			} else {
				result.WriteString("$*")
			}

		case SegmentDollarAt:
			if ctx.MacroContext != nil {
				value := e.formatArgsAsList(ctx)
				result.WriteString(value)
			} else {
				result.WriteString("$@")
			}

		case SegmentDollarHash:
			if ctx.MacroContext != nil {
				value := e.formatArgCount(ctx)
				result.WriteString(value)
			} else {
				result.WriteString("$#")
			}

		case SegmentBrace:
			// Execute the pre-parsed brace AST
			braceResult, isAsync := e.executeBraceFromTemplate(seg, ctx)
			if isAsync {
				// Return partial result with async marker
				// The caller will handle coordination
				result.WriteString(braceResult)
				return result.String(), true
			}
			result.WriteString(braceResult)
		}
	}

	return result.String(), false
}

// lookupTildeVar looks up a tilde variable and formats the result
func (e *Executor) lookupTildeVar(varName string, isQuestion bool, ctx *SubstitutionContext) string {
	if ctx.ExecutionState == nil {
		if isQuestion {
			return "false"
		}
		return ""
	}

	// Look up variable
	value, exists := ctx.ExecutionState.GetVariable(varName)
	if !exists && ctx.ExecutionState.moduleEnv != nil {
		ctx.ExecutionState.moduleEnv.mu.RLock()
		if obj, found := ctx.ExecutionState.moduleEnv.ObjectsModule[varName]; found {
			value = obj
			exists = true
		}
		ctx.ExecutionState.moduleEnv.mu.RUnlock()
	}

	if isQuestion {
		// ? expression - return "true" or "false"
		if exists {
			if sym, ok := value.(Symbol); ok {
				if string(sym) == UndefinedMarker || string(sym) == "undefined" {
					return "false"
				}
			}
			return "true"
		}
		return "false"
	}

	// ~ expression - return value
	if !exists {
		return ""
	}

	// Resolve and format value
	resolved := e.resolveValue(value)
	var valueStr string
	if list, ok := resolved.(StoredList); ok {
		valueStr = formatListForDisplay(list)
	} else if bytes, ok := resolved.(StoredBytes); ok {
		valueStr = bytes.String()
	} else {
		valueStr = fmt.Sprintf("%v", resolved)
	}

	// Escape for safety
	valueStr = strings.ReplaceAll(valueStr, `\`, `\\`)
	valueStr = strings.ReplaceAll(valueStr, `"`, `\"`)
	valueStr = strings.ReplaceAll(valueStr, "~", "\x00TILDE\x00")

	return valueStr
}

// lookupDollarArgWithContext looks up a numbered argument and formats it based on quote context
func (e *Executor) lookupDollarArgWithContext(argNum int, inQuote bool, ctx *SubstitutionContext) string {
	var argValue interface{}
	found := false

	// Try to get from $@ list first
	if ctx.ExecutionState != nil {
		if argsVar, exists := ctx.ExecutionState.GetVariable("$@"); exists {
			if sym, ok := argsVar.(Symbol); ok {
				marker := string(sym)
				if objType, objID := parseObjectMarker(marker); objType == "list" && objID >= 0 {
					if listObj, exists := e.getObject(objID); exists {
						if storedList, ok := listObj.(StoredList); ok {
							item := storedList.Get(argNum - 1)
							if item != nil {
								argValue = item
								found = true
							}
						}
					}
				}
			}
		}
	}

	// Fallback to ctx.Args
	if !found {
		idx := argNum - 1
		if idx >= 0 && idx < len(ctx.Args) {
			argValue = ctx.Args[idx]
			found = true
		}
	}

	if !found {
		return fmt.Sprintf("$%d", argNum)
	}

	// Format based on quote context
	if inQuote {
		return e.formatArgumentForQuotedContext(argValue)
	}
	return e.formatArgumentForSubstitution(argValue)
}

// lookupDollarArg looks up a numbered argument and formats it (outside quote context)
func (e *Executor) lookupDollarArg(argNum int, ctx *SubstitutionContext) string {
	var argValue interface{}
	found := false

	// Try to get from $@ list first
	if ctx.ExecutionState != nil {
		if argsVar, exists := ctx.ExecutionState.GetVariable("$@"); exists {
			if sym, ok := argsVar.(Symbol); ok {
				marker := string(sym)
				if objType, objID := parseObjectMarker(marker); objType == "list" && objID >= 0 {
					if listObj, exists := e.getObject(objID); exists {
						if storedList, ok := listObj.(StoredList); ok {
							item := storedList.Get(argNum - 1)
							if item != nil {
								argValue = item
								found = true
							}
						}
					}
				}
			}
		}
	}

	// Fallback to ctx.Args
	if !found {
		idx := argNum - 1
		if idx >= 0 && idx < len(ctx.Args) {
			argValue = ctx.Args[idx]
			found = true
		}
	}

	if !found {
		return fmt.Sprintf("$%d", argNum)
	}

	// Format the argument
	return e.formatArgumentForSubstitution(argValue)
}

// formatAllArgsComma formats all args as comma-separated list
func (e *Executor) formatAllArgsComma(ctx *SubstitutionContext) string {
	if len(ctx.Args) == 0 {
		return ""
	}
	allArgs := make([]string, len(ctx.Args))
	for i, arg := range ctx.Args {
		allArgs[i] = e.formatArgumentForList(arg)
	}
	return strings.Join(allArgs, ", ")
}

// formatArgsAsList formats args as a list marker
func (e *Executor) formatArgsAsList(ctx *SubstitutionContext) string {
	if ctx.ExecutionState != nil {
		if argsVar, exists := ctx.ExecutionState.GetVariable("$@"); exists {
			return fmt.Sprintf("%v", argsVar)
		}
	}
	return "()"
}

// formatArgCount formats the argument count
func (e *Executor) formatArgCount(ctx *SubstitutionContext) string {
	if ctx.ExecutionState != nil {
		if argsVar, exists := ctx.ExecutionState.GetVariable("$@"); exists {
			if sym, ok := argsVar.(Symbol); ok {
				marker := string(sym)
				if objType, id := parseObjectMarker(marker); objType == "list" && id >= 0 {
					if listObj, exists := e.getObject(id); exists {
						if storedList, ok := listObj.(StoredList); ok {
							return fmt.Sprintf("%d", storedList.Len())
						}
					}
				}
			}
		}
	}
	return fmt.Sprintf("%d", len(ctx.Args))
}

// executeBraceFromTemplate executes a brace segment from a template
func (e *Executor) executeBraceFromTemplate(seg TemplateSegment, ctx *SubstitutionContext) (string, bool) {
	if seg.BraceAST == nil {
		return "", false
	}

	// Create child state for brace execution
	braceState := NewExecutionStateFromSharedVars(ctx.ExecutionState)
	braceState.InBraceExpression = true

	// Create substitution context for the brace
	braceSubCtx := &SubstitutionContext{
		Args:              ctx.Args,
		ExecutionState:    braceState,
		MacroContext:      ctx.MacroContext,
		CurrentLineOffset: ctx.CurrentLineOffset,
		CurrentColumnOffset: ctx.CurrentColumnOffset,
		Filename:          ctx.Filename,
		CapturedModuleEnv: ctx.CapturedModuleEnv, // Inherit for handler caching
	}

	// Execute the pre-parsed commands
	executeResult := e.ExecuteParsedCommands(
		seg.BraceAST,
		braceState,
		braceSubCtx,
		ctx.CurrentLineOffset,
		ctx.CurrentColumnOffset,
	)

	// Track brace evaluation
	ctx.BracesEvaluated++

	// Capture result
	var capturedResult interface{}
	var hasCapturedResult bool
	if braceState.HasResult() {
		capturedResult = braceState.GetResult()
		hasCapturedResult = true
	}

	// Check for async
	if tokenResult, ok := executeResult.(TokenResult); ok {
		// Return async marker - caller will coordinate
		return fmt.Sprintf("\x00PAWS:%s\x00", string(tokenResult)), true
	}

	// Handle early return
	if earlyReturn, ok := executeResult.(EarlyReturn); ok {
		if !bool(earlyReturn.Status) {
			ctx.BraceFailureCount++
		}
		if earlyReturn.HasResult {
			capturedResult = earlyReturn.Result
			hasCapturedResult = true
		}
	}

	// Handle synchronous completion
	if boolStatus, ok := executeResult.(BoolStatus); ok && !bool(boolStatus) {
		ctx.BraceFailureCount++
	}

	// Format result
	var resultValue string
	if hasCapturedResult {
		resultValue = e.formatBraceResultFromTemplate(capturedResult, seg.IsUnescape, seg.InQuote, ctx)
	} else if boolStatus, ok := executeResult.(BoolStatus); ok {
		resultValue = fmt.Sprintf("%v", bool(boolStatus))
	}

	// Transfer object ownership from brace state to parent
	braceState.mu.Lock()
	ownedByBrace := make(map[int]int)
	for refID, count := range braceState.ownedObjects {
		ownedByBrace[refID] = count
	}
	braceState.mu.Unlock()

	for refID, childCount := range ownedByBrace {
		if childCount > 0 {
			ctx.ExecutionState.mu.Lock()
			parentOwns := ctx.ExecutionState.ownedObjects[refID] > 0
			ctx.ExecutionState.mu.Unlock()
			if !parentOwns {
				ctx.ExecutionState.ClaimObjectReference(refID)
			}
		}
	}

	// Clean up brace state
	braceState.ReleaseAllReferences()
	braceState.Recycle(false, false)

	return resultValue, false
}

// formatBraceResultFromTemplate formats a brace result for template substitution
func (e *Executor) formatBraceResultFromTemplate(value interface{}, isUnescape bool, inQuote bool, ctx *SubstitutionContext) string {
	if value == nil {
		return "nil"
	}

	// Handle symbol/marker
	if sym, ok := value.(Symbol); ok {
		if string(sym) == UndefinedMarker {
			if inQuote {
				return "<undefined>"
			}
			return "undefined"
		}
		if objType, objID := parseObjectMarker(string(sym)); objID >= 0 {
			if isUnescape {
				// ${...} - resolve and splat
				if actualValue, exists := e.getObject(objID); exists {
					switch objType {
					case "list":
						if list, ok := actualValue.(StoredList); ok {
							return e.formatListItems(list)
						}
					case "str":
						if storedStr, ok := actualValue.(StoredString); ok {
							return string(storedStr)
						}
					case "block":
						if storedBlock, ok := actualValue.(StoredBlock); ok {
							return string(storedBlock)
						}
					}
				}
			}
			// Inside quotes, display object contents
			if inQuote {
				if actualValue, exists := e.getObject(objID); exists {
					switch objType {
					case "list":
						if list, ok := actualValue.(StoredList); ok {
							return formatListForDisplay(list)
						}
					case "str":
						if storedStr, ok := actualValue.(StoredString); ok {
							return string(storedStr)
						}
					case "block":
						if storedBlock, ok := actualValue.(StoredBlock); ok {
							return string(storedBlock)
						}
					}
				}
			}
			// Return marker as-is for reference passing
			return string(sym)
		}
		return string(sym)
	}

	// Format based on type
	switch v := value.(type) {
	case bool:
		if v {
			return "true"
		}
		return "false"
	case StoredList:
		if isUnescape {
			return e.formatListItems(v)
		}
		// Store and return marker
		id := e.storeObject(value, "list")
		if ctx.ExecutionState != nil {
			ctx.ExecutionState.ClaimObjectReference(id)
		}
		return fmt.Sprintf("\x00LIST:%d\x00", id)
	case int64, float64:
		return fmt.Sprintf("%v", v)
	case string:
		return v
	case QuotedString:
		return string(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}
