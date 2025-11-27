package pawscript

import (
	"fmt"
	"regexp"
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
						if objType, objID := parseObjectMarker(marker); objType == "list" && objID >= 0 {
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

	// Apply tilde variable interpolation (after $N substitutions, so ~var works in results)
	// Create a position for error reporting
	position := &SourcePosition{
		Line:     ctx.CurrentLineOffset + 1,
		Column:   ctx.CurrentColumnOffset + 1,
		Filename: ctx.Filename,
	}
	result = e.substituteTildeExpressions(result, ctx.ExecutionState, position)

	// Restore escaped tildes
	result = strings.ReplaceAll(result, escapedTildePlaceholder, "~")

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
		} else if earlyReturn, ok := executeResult.(EarlyReturn); ok {
			// Handle early return (ret command) - treat as successful completion
			// Use the formal result from the EarlyReturn
			evaluations[i].Completed = true

			// Check the status to determine success/failure
			if !bool(earlyReturn.Status) {
				evaluations[i].Failed = true
				e.logger.Debug("Brace %d completed with early return (failure)", i)
			} else {
				e.logger.Debug("Brace %d completed with early return (success)", i)
			}

			// Use the result from EarlyReturn if present
			if earlyReturn.HasResult {
				evaluations[i].Result = earlyReturn.Result
				e.logger.Debug("Early return has result: %v", earlyReturn.Result)
			} else if hasCapturedResult {
				evaluations[i].Result = capturedResult
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
							e.logger.Debug("Parent claimed ownership of object %d from brace result", refID)
						}
					}
				}
			}
		} else {
			// Synchronous completion
			evaluations[i].Completed = true

			// Check if it was successful
			if boolStatus, ok := executeResult.(BoolStatus); ok && !bool(boolStatus) {
				evaluations[i].Failed = true
				//evaluations[i].Error = "Command returned false"
				e.logger.Debug("Brace %d completed synchronously with failure", i)
			} else {
				e.logger.Debug("Brace %d completed synchronously with success", i)
			}
			// Use the captured result - this part used to be in an else
			if hasCapturedResult {
				evaluations[i].Result = capturedResult
				e.logger.Debug("Brace %d has captured result: %s", i, evaluations[i].Result)
			} else if boolStatus, ok := executeResult.(BoolStatus); ok {
				evaluations[i].Result = fmt.Sprintf("%v", bool(boolStatus))
			}
			// end part that used to be in an else

			// Handle ownership for result references
			// Two cases:
			// 1. Pass-through of shared variable: parent already owns it, child's claim is temporary
			// 2. New object creation: parent doesn't own it yet, needs to take ownership
			if hasCapturedResult {
				resultRefs := braceState.ExtractObjectReferences(capturedResult)
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
							// New object - parent needs to claim it
							// Claim for parent (increments global refcount)
							ctx.ExecutionState.ClaimObjectReference(refID)
						}
						// If parent already owns it, don't add extra ownership
						// The child's claim will be released below
					}
				}
			}

			// Clean up brace state references
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

			e.logger.Warn(errorMsg, eval.Position, sourceContext)
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

// substituteTildeExpressions substitutes ~varname patterns in strings with variable values
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
		// Append everything before this tilde
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
		if exists {
			// Resolve any object markers to get display value
			resolved := e.resolveValue(value)
			var valueStr string
			// Handle StoredList specially to format contents
			if list, ok := resolved.(StoredList); ok {
				valueStr = formatListForDisplay(list)
			} else {
				valueStr = fmt.Sprintf("%v", resolved)
			}
			// Escape tildes in the resolved value to prevent tilde injection
			// This ensures user input containing tildes doesn't get interpreted
			// as variable references when the result string is re-parsed
			valueStr = strings.ReplaceAll(valueStr, "~", escapedTildePlaceholder)
			// Since tildes are only found inside double-quoted strings,
			// we need to escape any double quotes in the substituted value
			// to prevent breaking the quote structure
			valueStr = strings.ReplaceAll(valueStr, `\`, `\\`)
			valueStr = strings.ReplaceAll(valueStr, `"`, `\"`)
			result = append(result, []rune(valueStr)...)
		} else {
			// Variable not found - log error and leave empty
			e.logger.CommandError(CatVariable, "", fmt.Sprintf("Variable not found: %s", tilde.VarName), position)
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

		// Only process tildes inside double-quoted strings AND outside parentheses
		if char == '~' && inDoubleQuote && parenDepth == 0 && i+1 < len(runes) {
			tildeStart := i
			i++ // Move past the tilde

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
			// For StoredList, unwrap outer parens (just like ParenGroup)
			if list, ok := rawValue.(StoredList); ok {
				resultValue = e.formatListItems(list)
			} else {
				resultValue = fmt.Sprintf("%v", rawValue)
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
	// Handle nil specially - output as bare word "nil"
	if value == nil {
		return "nil"
	}

	// Check if we're inside a quoted string context
	insideQuotes := e.isInsideQuotes(originalString, bracePos)

	// If it's a Symbol that might be a marker, return it unchanged to preserve the reference
	if sym, ok := value.(Symbol); ok {
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
			return e.escapeQuotesAndBackslashes(v)
		}
		// Outside quotes: wrap in quotes to preserve the string value
		// (don't escape spaces/special chars - that's for bare words only)
		escaped := e.escapeQuotesAndBackslashes(v)
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
