package pawscript

import (
	"context"
	"fmt"
	"strings"
	"time"
)

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

	// Get fiber ID from state (0 for main fiber)
	fiberID := 0
	if state != nil {
		fiberID = state.fiberID
	}

	// Include fiber ID in token format for easier debugging
	tokenID := fmt.Sprintf("fiber-%d-token-%d", fiberID, e.nextTokenID)
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

	// Ensure state has executor reference
	if state.executor == nil {
		state.executor = e
	}

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
		FiberID:            fiberID,
	}

	e.activeTokens[tokenID] = tokenData

	if parentTokenID != "" {
		if parent, exists := e.activeTokens[parentTokenID]; exists {
			parent.Children[tokenID] = true
		}
	}

	e.logger.Debug("Created completion token: %s (fiber %d), parent: %s, hasResult: %v",
		tokenID, fiberID, parentTokenID, hasSuspendedResult)

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

	// Ensure state has executor reference
	if state != nil && state.executor == nil {
		state.executor = e
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

	// Clean up the brace's state references
	if targetEval.State != nil {
		targetEval.State.ReleaseAllReferences()
	}

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
		finalString := e.substituteAllBraces(coord.OriginalString, coord.Evaluations, coord.SubstitutionCtx.ExecutionState)
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
	var newChainedToken string
	if tokenData.CommandSequence != nil {
		seq := tokenData.CommandSequence
		e.mu.Unlock() // Unlock before resuming to avoid deadlock
		success, newChainedToken = e.resumeCommandSequence(seq, effectiveStatus, state)
		e.mu.Lock()
	}

	// If resume created a new chained token, it means an async operation was encountered.
	// That new token is chained FROM the async operation's token, and will be triggered
	// when the async completes. We should NOT immediately resume it - just propagate
	// the waitChan so the final result signals correctly.
	//
	// If no new chain was created, use the existing ChainedToken (set by chainTokens earlier).
	chainedToken := tokenData.ChainedToken
	asyncPending := newChainedToken != ""
	if asyncPending {
		// newChainedToken will be triggered by its parent async token when it completes.
		// Just propagate the waitChan to it.
		chainedToken = newChainedToken
	}
	parentToken := tokenData.ParentToken
	fiberID := tokenData.FiberID
	waitChan := tokenData.WaitChan

	// Check if this token belongs to a fiber
	var fiberHandle *FiberHandle
	if fiberID != 0 {
		fiberHandle = e.activeFibers[fiberID]
	}

	delete(e.activeTokens, tokenID)

	if parentToken != "" {
		if parent, exists := e.activeTokens[parentToken]; exists {
			delete(parent.Children, tokenID)
		}
	}

	e.mu.Unlock()

	// If this token belongs to a fiber, send resume data to the fiber
	if fiberHandle != nil {
		e.logger.Debug("Sending resume data to fiber %d for token %s", fiberID, tokenID)
		resumeData := ResumeData{
			TokenID: tokenID,
			Status:  success,
			Result:  state.GetResult(),
		}
		// Non-blocking send since fiber might not be waiting yet
		select {
		case fiberHandle.ResumeChan <- resumeData:
			e.logger.Debug("Successfully sent resume data to fiber %d", fiberID)
		default:
			e.logger.Warn("Fiber %d resume channel full or not ready", fiberID)
		}
	}

	if chainedToken != "" {
		// Propagate waitChan to the chained token so the final token in the chain
		// will signal completion (not this intermediate token)
		if waitChan != nil {
			e.attachWaitChan(chainedToken, waitChan)
			e.logger.Debug("Propagated wait channel to chained token %s", chainedToken)
		}

		// If asyncPending is true, the chainedToken is waiting for an async operation
		// to complete (e.g., msleep). Don't resume it now - it will be triggered
		// automatically when the async operation's token completes.
		if asyncPending {
			e.logger.Debug("Async operation pending, not immediately triggering chained token %s", chainedToken)
			// Release our state references since we're done with this token
			if tokenData.ExecutionState != nil {
				tokenData.ExecutionState.ReleaseAllReferences()
			}
			return success
		}

		e.logger.Debug("Triggering chained token %s with result %v", chainedToken, success)
		result := e.PopAndResumeCommandSequence(chainedToken, success)

		// Release all object references held by this token's state
		if tokenData.ExecutionState != nil {
			tokenData.ExecutionState.ReleaseAllReferences()
		}

		return result
	}

	// No chained token - this is the final token in the chain
	// If this token has a wait channel (synchronous blocking), send to it now
	if waitChan != nil {
		e.logger.Debug("Sending resume data to wait channel for token %s (final in chain)", tokenID)
		resumeData := ResumeData{
			TokenID: tokenID,
			Status:  success,
			Result:  state.GetResult(),
		}
		// Send to wait channel (blocking is expected here)
		waitChan <- resumeData
		e.logger.Debug("Successfully sent resume data to wait channel")
	}

	// No chain - safe to release now
	if tokenData.ExecutionState != nil {
		tokenData.ExecutionState.ReleaseAllReferences()
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

	// Release all object references held by this token's state
	if tokenData.ExecutionState != nil {
		tokenData.ExecutionState.ReleaseAllReferences()
	}

	delete(e.activeTokens, tokenID)
}

// resumeCommandSequence resumes execution of a command sequence
// Returns (success, newChainedToken) where newChainedToken is non-empty if a new token chain was created
func (e *Executor) resumeCommandSequence(seq *CommandSequence, status bool, state *ExecutionState) (bool, string) {
	switch seq.Type {
	case "sequence":
		return e.resumeSequence(seq, status, state)
	case "conditional":
		return e.resumeConditional(seq, status, state)
	case "or":
		return e.resumeOr(seq, status, state)
	default:
		e.logger.Error("Unknown command sequence type: %s", seq.Type)
		return false, ""
	}
}

// resumeSequence resumes a sequential command sequence
// Returns (success, newChainedToken) where newChainedToken is non-empty if a new token chain was created
func (e *Executor) resumeSequence(seq *CommandSequence, status bool, state *ExecutionState) (bool, string) {
	success := status

	for i, parsedCmd := range seq.RemainingCommands {
		if strings.TrimSpace(parsedCmd.Command) == "" {
			continue
		}

		cmdResult := e.executeParsedCommand(parsedCmd, state, nil)

		// Check for early return
		if earlyReturn, ok := cmdResult.(EarlyReturn); ok {
			e.logger.Debug("Command returned early return during resume, terminating sequence")
			if earlyReturn.HasResult {
				state.SetResult(earlyReturn.Result)
			}
			return bool(earlyReturn.Status), ""
		}

		if tokenResult, ok := cmdResult.(TokenResult); ok {
			e.logger.Debug("Command returned token during resume: %s, chaining remaining commands", string(tokenResult))

			// Handle remaining commands after this token
			remainingCommands := seq.RemainingCommands[i+1:]
			if len(remainingCommands) > 0 {
				// Create a new sequence token for the remaining commands
				sequenceToken := e.RequestCompletionToken(
					func(tokenID string) {
						e.logger.Debug("Cleaning up suspended sequence for token %s", tokenID)
					},
					"",
					5*time.Minute,
					state,
					parsedCmd.Position,
				)

				err := e.PushCommandSequence(sequenceToken, "sequence", remainingCommands, 0, "sequence", state, parsedCmd.Position)
				if err != nil {
					e.logger.Error("Failed to push command sequence: %v", err)
					return false, ""
				}

				// Chain the current token to the sequence token
				e.chainTokens(string(tokenResult), sequenceToken)

				// Return the sequence token as the new chain
				return true, sequenceToken
			}

			// No more commands, return the token itself as the new chain
			return true, string(tokenResult)
		}

		success = bool(cmdResult.(BoolStatus))
		state.SetLastStatus(success)
	}

	return success, ""
}

// resumeConditional resumes a conditional sequence
// Returns (success, newChainedToken) where newChainedToken is non-empty if a new token chain was created
func (e *Executor) resumeConditional(seq *CommandSequence, status bool, state *ExecutionState) (bool, string) {
	if !status {
		return false, ""
	}

	success := status

	for i, parsedCmd := range seq.RemainingCommands {
		if strings.TrimSpace(parsedCmd.Command) == "" {
			continue
		}

		cmdResult := e.executeParsedCommand(parsedCmd, state, nil)

		// Check for early return
		if earlyReturn, ok := cmdResult.(EarlyReturn); ok {
			e.logger.Debug("Command returned early return during resume, terminating sequence")
			if earlyReturn.HasResult {
				state.SetResult(earlyReturn.Result)
			}
			return bool(earlyReturn.Status), ""
		}

		if tokenResult, ok := cmdResult.(TokenResult); ok {
			e.logger.Debug("Command returned token during conditional resume: %s, chaining remaining commands", string(tokenResult))

			// Handle remaining commands after this token
			remainingCommands := seq.RemainingCommands[i+1:]
			if len(remainingCommands) > 0 {
				// Create a new sequence token for the remaining commands
				sequenceToken := e.RequestCompletionToken(
					func(tokenID string) {
						e.logger.Debug("Cleaning up suspended conditional sequence for token %s", tokenID)
					},
					"",
					5*time.Minute,
					state,
					parsedCmd.Position,
				)

				err := e.PushCommandSequence(sequenceToken, "conditional", remainingCommands, 0, "conditional", state, parsedCmd.Position)
				if err != nil {
					e.logger.Error("Failed to push command sequence: %v", err)
					return false, ""
				}

				// Chain the current token to the sequence token
				e.chainTokens(string(tokenResult), sequenceToken)

				// Return the sequence token as the new chain
				return true, sequenceToken
			}

			// No more commands, return the token itself as the new chain
			return true, string(tokenResult)
		}

		success = bool(cmdResult.(BoolStatus))
		state.SetLastStatus(success)
		if !success {
			break
		}
	}

	return success, ""
}

// resumeOr resumes an OR sequence
// Returns (success, newChainedToken) where newChainedToken is non-empty if a new token chain was created
func (e *Executor) resumeOr(seq *CommandSequence, status bool, state *ExecutionState) (bool, string) {
	if status {
		return true, ""
	}

	success := false

	for i, parsedCmd := range seq.RemainingCommands {
		if strings.TrimSpace(parsedCmd.Command) == "" {
			continue
		}

		cmdResult := e.executeParsedCommand(parsedCmd, state, nil)

		// Check for early return
		if earlyReturn, ok := cmdResult.(EarlyReturn); ok {
			e.logger.Debug("Command returned early return during resume, terminating sequence")
			if earlyReturn.HasResult {
				state.SetResult(earlyReturn.Result)
			}
			return bool(earlyReturn.Status), ""
		}

		if tokenResult, ok := cmdResult.(TokenResult); ok {
			e.logger.Debug("Command returned token during OR resume: %s, chaining remaining commands", string(tokenResult))

			// Handle remaining commands after this token
			remainingCommands := seq.RemainingCommands[i+1:]
			if len(remainingCommands) > 0 {
				// Create a new sequence token for the remaining commands
				sequenceToken := e.RequestCompletionToken(
					func(tokenID string) {
						e.logger.Debug("Cleaning up suspended OR sequence for token %s", tokenID)
					},
					"",
					5*time.Minute,
					state,
					parsedCmd.Position,
				)

				err := e.PushCommandSequence(sequenceToken, "or", remainingCommands, 0, "or", state, parsedCmd.Position)
				if err != nil {
					e.logger.Error("Failed to push command sequence: %v", err)
					return false, ""
				}

				// Chain the current token to the sequence token
				e.chainTokens(string(tokenResult), sequenceToken)

				// Return the sequence token as the new chain
				return true, sequenceToken
			}

			// No more commands, return the token itself as the new chain
			return true, string(tokenResult)
		}

		success = bool(cmdResult.(BoolStatus))
		state.SetLastStatus(success)
		if success {
			break
		}
	}

	return success, ""
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

// attachWaitChan attaches a wait channel to a token for synchronous blocking
func (e *Executor) attachWaitChan(tokenID string, waitChan chan ResumeData) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if tokenData, exists := e.activeTokens[tokenID]; exists {
		tokenData.WaitChan = waitChan
		e.logger.Debug("Attached wait channel to token %s", tokenID)
	} else {
		e.logger.Warn("Attempted to attach wait channel to non-existent token: %s", tokenID)
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
