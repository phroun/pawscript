package pawscript

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Executor handles command execution
type Executor struct {
	mu              sync.RWMutex
	commands        map[string]Handler
	activeTokens    map[string]*TokenData
	tempValues      map[int]interface{} // Storage for complex values during brace substitution
	nextTokenID     int
	nextTempID      int
	logger          *Logger
	fallbackHandler func(cmdName string, args []interface{}, state *ExecutionState, position *SourcePosition) Result
}

// NewExecutor creates a new command executor
func NewExecutor(logger *Logger) *Executor {
	return &Executor{
		commands:     make(map[string]Handler),
		activeTokens: make(map[string]*TokenData),
		tempValues:   make(map[int]interface{}),
		nextTokenID:  1,
		nextTempID:   1,
		logger:       logger,
	}
}

// RegisterCommand registers a command handler
func (e *Executor) RegisterCommand(name string, handler Handler) {
	e.mu.Lock()
	e.commands[name] = handler
	e.mu.Unlock()
	e.logger.Debug("Registered command: %s", name)
}

// UnregisterCommand unregisters a command
func (e *Executor) UnregisterCommand(name string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.commands[name]; exists {
		delete(e.commands, name)
		e.logger.Debug("Unregistered command: %s", name)
		return true
	}

	e.logger.Warn("Attempted to unregister unknown command: %s", name)
	return false
}

// SetFallbackHandler sets a fallback handler for unknown commands
func (e *Executor) SetFallbackHandler(handler func(string, []interface{}, *ExecutionState, *SourcePosition) Result) {
	e.mu.Lock()
	e.fallbackHandler = handler
	e.mu.Unlock()
}

// RequestCompletionToken requests a new completion token for async operations
func (e *Executor) RequestCompletionToken(
	cleanupCallback func(string),
	parentTokenID string,
	timeout time.Duration,
	state *ExecutionState,
	position *SourcePosition,
) string {
	e.mu.Lock()
	defer e.mu.Unlock()

	tokenID := fmt.Sprintf("token_%d", e.nextTokenID)
	e.nextTokenID++

	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	// Set up timeout handler
	go func() {
		<-ctx.Done()
		if ctx.Err() == context.DeadlineExceeded {
			e.logger.Warn("Token %s timed out, forcing cleanup", tokenID)
			e.ForceCleanupToken(tokenID)
		}
	}()

	suspendedResult, hasSuspendedResult := state.Snapshot()

	tokenData := &TokenData{
		CommandSequence:    nil,
		ParentToken:        parentTokenID,
		Children:           make(map[string]bool),
		CleanupCallback:    cleanupCallback,
		CancelFunc:         cancel,
		ChainedToken:       "",
		Timestamp:          time.Now(),
		ExecutionState:     state,
		SuspendedResult:    suspendedResult,
		HasSuspendedResult: hasSuspendedResult,
		Position:           position,
	}

	e.activeTokens[tokenID] = tokenData

	if parentTokenID != "" {
		if parent, exists := e.activeTokens[parentTokenID]; exists {
			parent.Children[tokenID] = true
		}
	}

	e.logger.Debug("Created completion token: %s, parent: %s, hasResult: %v",
		tokenID, parentTokenID, hasSuspendedResult)

	return tokenID
}

// RequestBraceCoordinatorToken creates a token for coordinating parallel brace evaluation
func (e *Executor) RequestBraceCoordinatorToken(
	evaluations []*BraceEvaluation,
	originalString string,
	substitutionCtx *SubstitutionContext,
	resumeCallback func(finalString string, success bool) Result,
	state *ExecutionState,
	position *SourcePosition,
) string {
	e.mu.Lock()
	defer e.mu.Unlock()

	tokenID := fmt.Sprintf("token_%d", e.nextTokenID)
	e.nextTokenID++

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)

	// Set up timeout handler
	go func() {
		<-ctx.Done()
		if ctx.Err() == context.DeadlineExceeded {
			e.logger.Warn("Brace coordinator token %s timed out, forcing cleanup", tokenID)
			e.ForceCleanupToken(tokenID)
		}
	}()

	coordinator := &BraceCoordinator{
		Evaluations:     evaluations,
		CompletedCount:  0,
		TotalCount:      len(evaluations),
		HasFailure:      false,
		OriginalString:  originalString,
		SubstitutionCtx: substitutionCtx,
		ResumeCallback:  resumeCallback,
	}

	tokenData := &TokenData{
		CommandSequence:  nil,
		ParentToken:      "",
		Children:         make(map[string]bool),
		CleanupCallback:  nil,
		CancelFunc:       cancel,
		ChainedToken:     "",
		Timestamp:        time.Now(),
		ExecutionState:   state,
		Position:         position,
		BraceCoordinator: coordinator,
	}

	e.activeTokens[tokenID] = tokenData

	// Register all async brace tokens as children
	for _, eval := range evaluations {
		if eval.IsAsync && eval.TokenID != "" {
			if childData, exists := e.activeTokens[eval.TokenID]; exists {
				childData.ParentToken = tokenID
				tokenData.Children[eval.TokenID] = true
			}
		}
	}

	e.logger.Debug("Created brace coordinator token: %s with %d evaluations (%d async)",
		tokenID, len(evaluations), len(tokenData.Children))

	return tokenID
}

// ResumeBraceEvaluation is called when a child brace evaluation completes
func (e *Executor) ResumeBraceEvaluation(coordinatorToken, childToken string, result interface{}, success bool) {
	e.mu.Lock()

	coordData, exists := e.activeTokens[coordinatorToken]
	if !exists {
		e.mu.Unlock()
		e.logger.Warn("Coordinator token %s not found for child %s", coordinatorToken, childToken)
		return
	}

	if coordData.BraceCoordinator == nil {
		e.mu.Unlock()
		e.logger.Error("Token %s is not a brace coordinator", coordinatorToken)
		return
	}

	coord := coordData.BraceCoordinator

	// Find the evaluation for this child token
	var targetEval *BraceEvaluation
	for _, eval := range coord.Evaluations {
		if eval.TokenID == childToken {
			targetEval = eval
			break
		}
	}

	if targetEval == nil {
		e.mu.Unlock()
		e.logger.Warn("Child token %s not found in coordinator %s", childToken, coordinatorToken)
		return
	}

	// Mark this evaluation as completed
	targetEval.Completed = true
	targetEval.Result = result
	coord.CompletedCount++

	if !success {
		targetEval.Failed = true
		if !coord.HasFailure {
			coord.HasFailure = true
			coord.FirstFailureError = fmt.Sprintf("Brace evaluation failed: %s", childToken)
			e.logger.Debug("Brace evaluation failed in coordinator %s: child %s", coordinatorToken, childToken)
		}
	} else {
		e.logger.Debug("Brace evaluation completed in coordinator %s: child %s (%d/%d)",
			coordinatorToken, childToken, coord.CompletedCount, coord.TotalCount)
	}

	// Check if all evaluations are complete
	allDone := coord.CompletedCount >= coord.TotalCount
	hasFailure := coord.HasFailure

	e.mu.Unlock()

	if allDone {
		e.logger.Debug("All brace evaluations complete for coordinator %s (failure: %v)",
			coordinatorToken, hasFailure)
		e.finalizeBraceCoordinator(coordinatorToken)
	}
}

// finalizeBraceCoordinator finalizes a brace coordinator and resumes execution
func (e *Executor) finalizeBraceCoordinator(coordinatorToken string) {
	e.mu.Lock()

	coordData, exists := e.activeTokens[coordinatorToken]
	if !exists {
		e.mu.Unlock()
		return
	}

	if coordData.BraceCoordinator == nil {
		e.mu.Unlock()
		return
	}

	coord := coordData.BraceCoordinator
	hasFailure := coord.HasFailure
	chainedToken := coordData.ChainedToken

	// Clean up all children (this will call their cleanup callbacks)
	e.cleanupTokenChildrenLocked(coordinatorToken)

	// Cancel timeout
	if coordData.CancelFunc != nil {
		coordData.CancelFunc()
	}

	// Remove coordinator token from active tokens
	delete(e.activeTokens, coordinatorToken)

	e.mu.Unlock()

	// Now perform the final substitution and resume callback
	var callbackResult Result
	if hasFailure {
		e.logger.Debug("Brace coordinator %s failed, calling resume with failure", coordinatorToken)
		callbackResult = coord.ResumeCallback("", false)
	} else {
		// Substitute all results into the original string
		finalString := e.substituteAllBraces(coord.OriginalString, coord.Evaluations)
		e.logger.Debug("Brace coordinator %s succeeded, substituted string: %s", coordinatorToken, finalString)
		callbackResult = coord.ResumeCallback(finalString, true)
	}

	// Handle the callback result
	if boolStatus, ok := callbackResult.(BoolStatus); ok {
		// Command completed synchronously
		success := bool(boolStatus)
		e.logger.Debug("Brace coordinator callback returned bool: %v", success)

		// If there's a chained token, resume it with this result
		if chainedToken != "" {
			e.logger.Debug("Resuming chained token %s with result %v", chainedToken, success)
			e.PopAndResumeCommandSequence(chainedToken, success)
		}
	} else if tokenResult, ok := callbackResult.(TokenResult); ok {
		// Command returned another token (nested async)
		newToken := string(tokenResult)
		e.logger.Debug("Brace coordinator callback returned new token: %s", newToken)

		// If there's a chained token, chain the new token to it
		if chainedToken != "" {
			e.logger.Debug("Chaining new token %s to %s", newToken, chainedToken)
			e.chainTokens(newToken, chainedToken)
		}
	}
}

// PushCommandSequence pushes a command sequence onto a token
func (e *Executor) PushCommandSequence(
	tokenID string,
	seqType string,
	remainingCommands []*ParsedCommand,
	currentIndex int,
	originalCommand string,
	state *ExecutionState,
	position *SourcePosition,
) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	tokenData, exists := e.activeTokens[tokenID]
	if !exists {
		return fmt.Errorf("invalid completion token: %s", tokenID)
	}

	result, hasResult := state.Snapshot()

	// Make a copy of remaining commands
	commandsCopy := make([]*ParsedCommand, len(remainingCommands))
	copy(commandsCopy, remainingCommands)

	tokenData.CommandSequence = &CommandSequence{
		Type:               seqType,
		RemainingCommands:  commandsCopy,
		CurrentIndex:       currentIndex,
		TotalCommands:      len(remainingCommands) + currentIndex,
		OriginalCommand:    originalCommand,
		Timestamp:          time.Now(),
		InheritedResult:    result,
		HasInheritedResult: hasResult,
		Position:           position,
	}

	e.logger.Debug("Pushed command sequence onto token %s. Type: %s, Remaining: %d, hasResult: %v",
		tokenID, seqType, len(remainingCommands), hasResult)

	return nil
}

// PopAndResumeCommandSequence pops and resumes a command sequence
func (e *Executor) PopAndResumeCommandSequence(tokenID string, status bool) bool {
	e.mu.Lock()

	tokenData, exists := e.activeTokens[tokenID]
	if !exists {
		e.mu.Unlock()
		e.logger.Warn("Attempted to resume with invalid token: %s", tokenID)
		return false
	}

	// Apply inversion if this token has the InvertStatus flag
	effectiveStatus := status
	if tokenData.InvertStatus {
		effectiveStatus = !status
		e.logger.Debug("Inverting async result for token %s: %v -> %v", tokenID, status, effectiveStatus)
	}

	// Check if this token's parent is a brace coordinator
	if tokenData.ParentToken != "" {
		if parentData, parentExists := e.activeTokens[tokenData.ParentToken]; parentExists {
			if parentData.BraceCoordinator != nil {
				// This is a child of a brace coordinator
				coordinatorToken := tokenData.ParentToken
				var resultValue interface{}
				if tokenData.ExecutionState != nil && tokenData.ExecutionState.HasResult() {
					resultValue = tokenData.ExecutionState.GetResult()
				}
				e.mu.Unlock()

				e.logger.Debug("Token %s is child of brace coordinator %s, forwarding result", tokenID, coordinatorToken)
				e.ResumeBraceEvaluation(coordinatorToken, tokenID, resultValue, effectiveStatus)
				return effectiveStatus
			}
		}
	}

	e.logger.Debug("Popping command sequence from token %s. Result: %v", tokenID, effectiveStatus)

	// Cleanup children
	e.cleanupTokenChildrenLocked(tokenID)

	// Cancel timeout
	if tokenData.CancelFunc != nil {
		tokenData.CancelFunc()
	}

	state := tokenData.ExecutionState
	if state == nil {
		state = NewExecutionState()
	}

	success := effectiveStatus
	if tokenData.CommandSequence != nil {
		seq := tokenData.CommandSequence
		e.mu.Unlock() // Unlock before resuming to avoid deadlock
		success = e.resumeCommandSequence(seq, effectiveStatus, state)
		e.mu.Lock()
	}

	chainedToken := tokenData.ChainedToken
	parentToken := tokenData.ParentToken

	delete(e.activeTokens, tokenID)

	if parentToken != "" {
		if parent, exists := e.activeTokens[parentToken]; exists {
			delete(parent.Children, tokenID)
		}
	}

	e.mu.Unlock()

	if chainedToken != "" {
		e.logger.Debug("Triggering chained token %s with result %v", chainedToken, success)
		return e.PopAndResumeCommandSequence(chainedToken, success)
	}

	return success
}

// cleanupTokenChildrenLocked cleans up child tokens (must be called with lock held)
func (e *Executor) cleanupTokenChildrenLocked(tokenID string) {
	tokenData, exists := e.activeTokens[tokenID]
	if !exists {
		return
	}

	for childTokenID := range tokenData.Children {
		e.forceCleanupTokenLocked(childTokenID)
	}
}

// ForceCleanupToken forces cleanup of a token
func (e *Executor) ForceCleanupToken(tokenID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.forceCleanupTokenLocked(tokenID)
}

// forceCleanupTokenLocked forces cleanup (must be called with lock held)
func (e *Executor) forceCleanupTokenLocked(tokenID string) {
	tokenData, exists := e.activeTokens[tokenID]
	if !exists {
		return
	}

	e.logger.Debug("Force cleaning up token: %s", tokenID)

	if tokenData.CleanupCallback != nil {
		tokenData.CleanupCallback(tokenID)
	}

	if tokenData.CancelFunc != nil {
		tokenData.CancelFunc()
	}

	e.cleanupTokenChildrenLocked(tokenID)
	delete(e.activeTokens, tokenID)
}

// resumeCommandSequence resumes execution of a command sequence
func (e *Executor) resumeCommandSequence(seq *CommandSequence, status bool, state *ExecutionState) bool {
	switch seq.Type {
	case "sequence":
		return e.resumeSequence(seq, status, state)
	case "conditional":
		return e.resumeConditional(seq, status, state)
	case "or":
		return e.resumeOr(seq, status, state)
	default:
		e.logger.Error("Unknown command sequence type: %s", seq.Type)
		return false
	}
}

// resumeSequence resumes a sequential command sequence
func (e *Executor) resumeSequence(seq *CommandSequence, status bool, state *ExecutionState) bool {
	success := status

	for _, parsedCmd := range seq.RemainingCommands {
		if strings.TrimSpace(parsedCmd.Command) == "" {
			continue
		}

		cmdResult := e.executeParsedCommand(parsedCmd, state, nil)

		if tokenResult, ok := cmdResult.(TokenResult); ok {
			e.logger.Warn("Command returned token during resume: %s", string(tokenResult))
			return false
		}

		success = bool(cmdResult.(BoolStatus))
	}

	return success
}

// resumeConditional resumes a conditional sequence
func (e *Executor) resumeConditional(seq *CommandSequence, status bool, state *ExecutionState) bool {
	if !status {
		return false
	}

	success := status

	for _, parsedCmd := range seq.RemainingCommands {
		if strings.TrimSpace(parsedCmd.Command) == "" {
			continue
		}

		cmdResult := e.executeParsedCommand(parsedCmd, state, nil)

		if tokenResult, ok := cmdResult.(TokenResult); ok {
			e.logger.Warn("Command returned token during resume: %s", string(tokenResult))
			return false
		}

		success = bool(cmdResult.(BoolStatus))
		if !success {
			break
		}
	}

	return success
}

// resumeOr resumes an OR sequence
func (e *Executor) resumeOr(seq *CommandSequence, status bool, state *ExecutionState) bool {
	if status {
		return true
	}

	success := false

	for _, parsedCmd := range seq.RemainingCommands {
		if strings.TrimSpace(parsedCmd.Command) == "" {
			continue
		}

		cmdResult := e.executeParsedCommand(parsedCmd, state, nil)

		if tokenResult, ok := cmdResult.(TokenResult); ok {
			e.logger.Warn("Command returned token during resume: %s", string(tokenResult))
			return false
		}

		success = bool(cmdResult.(BoolStatus))
		if success {
			break
		}
	}

	return success
}

// Execute executes a command string
func (e *Executor) Execute(commandStr string, args ...interface{}) Result {
	e.logger.Debug("Execute called with command: %s", commandStr)

	state := NewExecutionState()

	// If args provided, execute as direct command call
	if len(args) > 0 {
		e.mu.RLock()
		handler, exists := e.commands[commandStr]
		e.mu.RUnlock()

		if exists {
			ctx := e.createContext(args, state, nil)
			return handler(ctx)
		}

		e.logger.UnknownCommandError(commandStr, nil, nil)
		return BoolStatus(false)
	}

	return e.ExecuteWithState(commandStr, state, nil, "", 0, 0)
}

// ExecuteWithState executes with explicit state and substitution context
func (e *Executor) ExecuteWithState(
	commandStr string,
	state *ExecutionState,
	substitutionCtx *SubstitutionContext,
	filename string,
	lineOffset, columnOffset int,
) Result {
	parser := NewParser(commandStr, filename)
	cleanedCommand := parser.RemoveComments(commandStr)

	// Normalize keywords: 'then' -> '&', 'else' -> '|'
	normalizedCommand := parser.NormalizeKeywords(cleanedCommand)

	commands, err := parser.ParseCommandSequence(normalizedCommand)
	if err != nil {
		// Extract position and context from PawScriptError if available
		if pawErr, ok := err.(*PawScriptError); ok {
			// Apply offsets to error position
			if pawErr.Position != nil && (lineOffset > 0 || columnOffset > 0) {
				adjustedPosition := *pawErr.Position
				adjustedPosition.Line += lineOffset
				if adjustedPosition.Line == lineOffset+1 {
					adjustedPosition.Column += columnOffset
				}
				e.logger.ParseError(pawErr.Message, &adjustedPosition, pawErr.Context)
			} else {
				e.logger.ParseError(pawErr.Message, pawErr.Position, pawErr.Context)
			}
		} else {
			e.logger.ParseError(err.Error(), nil, nil)
		}
		return BoolStatus(false)
	}

	if len(commands) == 0 {
		return BoolStatus(true)
	}

	// Apply position offsets to all commands
	if lineOffset > 0 || columnOffset > 0 {
		for _, cmd := range commands {
			if cmd.Position != nil {
				cmd.Position.Line += lineOffset
				// Only apply column offset to first line
				if cmd.Position.Line == lineOffset+1 {
					cmd.Position.Column += columnOffset
				}
			}
		}
	}

	if len(commands) == 1 {
		return e.executeParsedCommand(commands[0], state, substitutionCtx)
	}

	return e.executeCommandSequence(commands, state, substitutionCtx)
}

// Continue in part 2...
