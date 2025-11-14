package pawscript

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// executeCommandSequence executes a sequence of commands
func (e *Executor) executeCommandSequence(commands []*ParsedCommand, state *ExecutionState, substitutionCtx *SubstitutionContext) Result {
	lastResult := true // Default to true for leading operators
	
	for i, cmd := range commands {
		if strings.TrimSpace(cmd.Command) == "" {
			continue
		}
		
		// Apply flow control based on separator
		shouldExecute := true
		
		switch cmd.Separator {
		case "&": // AND: execute only if last command succeeded
			shouldExecute = lastResult
		case "|": // OR: execute only if last command failed
			shouldExecute = !lastResult
		}
		
		if !shouldExecute {
			e.logger.Debug("Skipping command \"%s\" due to flow control (separator: %s, lastResult: %v)",
				cmd.Command, cmd.Separator, lastResult)
			continue
		}
		
		result := e.executeParsedCommand(cmd, state, substitutionCtx)
		
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
					return BoolResult(false)
				}
				
				e.chainTokens(string(tokenResult), sequenceToken)
				return TokenResult(sequenceToken)
			}
			return result
		}
		
		lastResult = bool(result.(BoolResult))
	}
	
	return BoolResult(lastResult)
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
	
	// Apply syntactic sugar
	commandStr = e.applySyntacticSugar(commandStr)
	
	e.logger.Debug("executeSingleCommand called with: \"%s\"", commandStr)
	
	// CRITICAL: Always evaluate brace expressions, even when not in a macro context
	// Create a minimal substitution context if one doesn't exist
	if substitutionCtx == nil {
	    substitutionCtx = &SubstitutionContext{
		Args:                []interface{}{},
		ExecutionState:      state,
		ParentContext:       nil,
		MacroContext:        nil,
		CurrentLineOffset:   0,
		CurrentColumnOffset: 0,
	    }
	} else {
	    // CRITICAL FIX: Update the execution state to the current one!
	    // This ensures brace expressions see the latest results
	    substitutionCtx.ExecutionState = state
	}

	// Apply substitution (which includes brace expressions)
	commandStr = e.applySubstitution(commandStr, substitutionCtx)
	e.logger.Debug("After substitution: \"%s\"", commandStr)
	
	// Parse command
	cmdName, args := ParseCommand(commandStr)
	
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
			return fallbackResult
		}
	}
	
	if !exists {
		e.logger.UnknownCommandError(cmdName, position, nil)
		return BoolResult(false)
	}
	
	// Execute command
	e.logger.Debug("Executing %s with args: %v", cmdName, args)
	ctx := e.createContext(args, state, position)
	return handler(ctx)
}

// applySyntacticSugar applies syntactic sugar transformations
func (e *Executor) applySyntacticSugar(commandStr string) string {
	spaceIndex := strings.Index(commandStr, " ")
	if spaceIndex == -1 {
		return commandStr
	}
	
	commandPart := commandStr[:spaceIndex]
	argsPart := commandStr[spaceIndex+1:]
	
	// Match identifier(content) pattern
	// (?s) makes . match newlines for multiline content
	// .+? is non-greedy to stop at first closing paren
	re := regexp.MustCompile(`(?s)^([a-zA-Z_][a-zA-Z0-9_]*)\s*\((.+?)\)(.*)$`)
	matches := re.FindStringSubmatch(argsPart)
	
	if len(matches) > 0 {
		identifier := matches[1]
		content := matches[2]
		return fmt.Sprintf("%s '%s', (%s)", commandPart, identifier, content)
	}
	
	return commandStr
}

// applySubstitution applies macro argument substitution
func (e *Executor) applySubstitution(str string, ctx *SubstitutionContext) string {
	// fmt.Fprintf(os.Stderr, "[DEBUG applySubstitution] Input: %q\n", str)
	// Note: ctx should never be nil - caller should create a minimal context if needed
	if ctx == nil {
		return str
	}
	
	result := str
	
	// Apply brace expression substitution first
	result = e.substituteBraceExpressions(result, ctx)
	
	// Apply $* (all args)
	if len(ctx.Args) > 0 {
		allArgs := make([]string, len(ctx.Args))
		for i, arg := range ctx.Args {
			allArgs[i] = e.formatArgumentForSubstitution(arg)
		}
		result = strings.ReplaceAll(result, "$*", strings.Join(allArgs, ", "))
	} else {
		result = strings.ReplaceAll(result, "$*", "")
	}
	
	// Apply $# (arg count)
	result = strings.ReplaceAll(result, "$#", fmt.Sprintf("%d", len(ctx.Args)))
	
	// Apply $1, $2, etc (indexed args)
	re := regexp.MustCompile(`\$(\d+)`)
	result = re.ReplaceAllStringFunc(result, func(match string) string {
		indexStr := match[1:] // Remove $
		index, err := strconv.Atoi(indexStr)
		if err != nil {
			return match
		}
		
		index-- // Convert to 0-based
		if index >= 0 && index < len(ctx.Args) {
			return e.formatArgumentForSubstitution(ctx.Args[index])
		}
		return match
	})
	
	return result
}

// substituteBraceExpressions substitutes brace expressions {command}
func (e *Executor) substituteBraceExpressions(str string, ctx *SubstitutionContext) string {
	result := str
	modified := true
	
	for modified {
		modified = false
		
		braceStart := -1
		braceEnd := -1
		braceDepth := 0
		parenDepth := 0
		
		line := 1
		column := 1
		braceStartLine := 1
		braceStartColumn := 1
		
		runes := []rune(result)
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
					braceStart = i
					braceStartLine = line
					braceStartColumn = column
				}
				braceDepth++
			} else if parenDepth == 0 && char == '}' {
				braceDepth--
				if braceDepth == 0 && braceStart != -1 {
					braceEnd = i
					break
				}
			}
		}
		
		if braceStart != -1 && braceEnd != -1 {
			beforeBrace := string(runes[:braceStart])
			braceContent := string(runes[braceStart+1 : braceEnd])
			afterBrace := string(runes[braceEnd+1:])
			
			// Debug output
			e.logger.Debug("Brace found: line=%d, column=%d", braceStartLine, braceStartColumn)
			e.logger.Debug("Brace content: \"{%s}\"", braceContent)
			
			// Create child state
			childState := ctx.ExecutionState.CreateChild()
			
			// Calculate accumulated offsets
			currentLineOffset := 0
			currentColumnOffset := 0
			if ctx != nil {
				currentLineOffset = ctx.CurrentLineOffset
				currentColumnOffset = ctx.CurrentColumnOffset
			}
			
			e.logger.Debug("Current offsets: line=%d, column=%d", currentLineOffset, currentColumnOffset)
			e.logger.Debug("Brace position: line=%d, column=%d", braceStartLine, braceStartColumn)
			
			newLineOffset := currentLineOffset + (braceStartLine - 1)
			var newColumnOffset int
			if braceStartLine == 1 {
				newColumnOffset = currentColumnOffset + braceStartColumn - 1
			} else {
				newColumnOffset = braceStartColumn - 1
			}
			
			e.logger.Debug("New accumulated offsets: line=%d, column=%d", newLineOffset, newColumnOffset)
			
			// Create child substitution context
			childSubstitutionCtx := &SubstitutionContext{
				Args:                ctx.Args,
				ExecutionState:      childState,
				ParentContext:       ctx,
				MacroContext:        ctx.MacroContext,
				CurrentLineOffset:   newLineOffset,
				CurrentColumnOffset: newColumnOffset,
			}
			
			e.logger.Debug("executeWithState: offsets line=%d, column=%d", newLineOffset, newColumnOffset)
			e.logger.Debug("Command: \"%s\"", braceContent)
			e.logger.Debug("Filename: <none>")
			
			// Execute brace content
			executeResult := e.ExecuteWithState(
				braceContent,
				childState,
				childSubstitutionCtx,
				"",
				newLineOffset,
				newColumnOffset,
			)
			
			// Get execution value
			executionValue := ""
			if childState.HasResult() {
				executionValue = fmt.Sprintf("%v", childState.GetResult())
			} else if boolResult, ok := executeResult.(BoolResult); ok {
				executionValue = fmt.Sprintf("%v", bool(boolResult))
			} else if tokenResult, ok := executeResult.(TokenResult); ok {
				if !strings.HasPrefix(string(tokenResult), "token_") {
					executionValue = string(tokenResult)
				}
			}
			
			// Assemble result
			assembled := beforeBrace + executionValue + afterBrace
			result = e.reEvaluateToken(assembled, ctx)
			modified = true
		}
	}
	
	return result
}

// reEvaluateToken re-evaluates a token after brace substitution
func (e *Executor) reEvaluateToken(token string, ctx *SubstitutionContext) string {
	result := token
	
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
	
	return result
}

// formatArgumentForSubstitution formats an argument for substitution
func (e *Executor) formatArgumentForSubstitution(arg interface{}) string {
	str := fmt.Sprintf("%v", arg)
	
	// Quote if contains special characters
	if strings.ContainsAny(str, " ;&#|,") {
		str = strings.ReplaceAll(str, "'", "\\'")
		return fmt.Sprintf("'%s'", str)
	}
	
	return str
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
		resumeToken: func(tokenID string, result bool) bool {
			return e.PopAndResumeCommandSequence(tokenID, result)
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
