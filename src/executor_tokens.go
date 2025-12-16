package pawscript

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// RequestCompletionToken requests a new completion token for async operations
// If timeout <= 0, no timeout is set (token relies on explicit completion)
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

	// Set up context and optional timeout
	var cancel context.CancelFunc
	if timeout > 0 {
		var ctx context.Context
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
		// Set up timeout handler
		go func() {
			<-ctx.Done()
			if ctx.Err() == context.DeadlineExceeded {
				e.logger.WarnCat(CatAsync, "Token %s timed out, forcing cleanup", tokenID)
				e.ForceCleanupToken(tokenID)
			}
		}()
	} else {
		// No timeout - token relies on explicit completion
		_, cancel = context.WithCancel(context.Background())
	}

	// Handle nil state for system-level tokens (e.g., #random in io module)
	var suspendedResult interface{}
	var hasSuspendedResult bool
	if state != nil {
		suspendedResult, hasSuspendedResult = state.Snapshot()
		// Ensure state has executor reference
		if state.executor == nil {
			state.executor = e
		}
	}

	// Register the token as a stored object first to get ObjectID
	objectID := e.registerObjectLocked(nil, ObjToken) // Temporarily nil, will update
	e.tokenStringToID[tokenID] = objectID

	// RefCount starts at 0 - external claims (SetResult, SetVariable) will increment
	// Token is kept alive by activeTokens map while running

	tokenData := &TokenData{
		StringID:           tokenID,
		ObjectID:           objectID,
		Completed:          false,
		FinalStatus:        false,
		FinalResult:        nil,
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

	// Update the stored object with the actual tokenData
	e.storedObjects[objectID].Value = tokenData

	e.activeTokens[tokenID] = tokenData

	if parentTokenID != "" {
		if parent, exists := e.activeTokens[parentTokenID]; exists {
			parent.Children[tokenID] = true
		}
	}

	e.logger.DebugCat(CatAsync, "Created completion token: %s (fiber %d, objID %d), parent: %s, hasResult: %v, timeout: %v",
		tokenID, fiberID, objectID, parentTokenID, hasSuspendedResult, timeout)

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
			e.logger.WarnCat(CatAsync, "Brace coordinator token %s timed out, forcing cleanup", tokenID)
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

	// Register the token as a stored object first to get ObjectID
	objectID := e.registerObjectLocked(nil, ObjToken) // Temporarily nil, will update
	e.tokenStringToID[tokenID] = objectID

	// RefCount starts at 0 - external claims will increment
	// Token is kept alive by activeTokens map while running

	tokenData := &TokenData{
		StringID:         tokenID,
		ObjectID:         objectID,
		Completed:        false,
		FinalStatus:      false,
		FinalResult:      nil,
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

	// Update the stored object with the actual tokenData
	e.storedObjects[objectID].Value = tokenData

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

	e.logger.DebugCat(CatAsync,"Created brace coordinator token: %s (objID %d) with %d evaluations (%d async)",
		tokenID, objectID, len(evaluations), len(tokenData.Children))

	return tokenID
}

// ResumeBraceEvaluation is called when a child brace evaluation completes
func (e *Executor) ResumeBraceEvaluation(coordinatorToken, childToken string, result interface{}, success bool) {
	e.mu.Lock()

	coordData, exists := e.activeTokens[coordinatorToken]
	if !exists {
		e.mu.Unlock()
		e.logger.WarnCat(CatAsync,"Coordinator token %s not found for child %s", coordinatorToken, childToken)
		return
	}

	if coordData.BraceCoordinator == nil {
		e.mu.Unlock()
		e.logErrorWithContext(CatAsync, fmt.Sprintf("Token %s is not a brace coordinator", coordinatorToken), coordData.ExecutionState, coordData.Position)
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
		e.logger.WarnCat(CatAsync,"Child token %s not found in coordinator %s", childToken, coordinatorToken)
		return
	}

	// Mark this evaluation as completed
	targetEval.Completed = true
	targetEval.Result = result
	coord.CompletedCount++

	// Transfer owned references from brace state to parent before releasing
	// Since the brace shares variables with the parent, any objects stored in
	// those variables need to be owned by the parent before we release the brace's claims
	if targetEval.State != nil && coord.SubstitutionCtx != nil && coord.SubstitutionCtx.ExecutionState != nil {
		parentState := coord.SubstitutionCtx.ExecutionState
		targetEval.State.mu.Lock()
		ownedByBrace := make(map[int]int)
		for refID, count := range targetEval.State.ownedObjects {
			ownedByBrace[refID] = count
		}
		targetEval.State.mu.Unlock()

		for refID, childCount := range ownedByBrace {
			if childCount > 0 {
				parentState.mu.Lock()
				parentOwns := parentState.ownedObjects[refID] > 0
				parentState.mu.Unlock()

				if !parentOwns {
					parentState.ClaimObjectReference(refID)
				}
			}
		}
		targetEval.State.ReleaseAllReferences()
	} else if targetEval.State != nil {
		targetEval.State.ReleaseAllReferences()
	}

	if !success {
		targetEval.Failed = true
		if !coord.HasFailure {
			coord.HasFailure = true
			coord.FirstFailureError = fmt.Sprintf("Brace evaluation failed: %s", childToken)
			e.logger.DebugCat(CatAsync,"Brace evaluation failed in coordinator %s: child %s", coordinatorToken, childToken)
		}
	} else {
		e.logger.DebugCat(CatAsync,"Brace evaluation completed in coordinator %s: child %s (%d/%d)",
			coordinatorToken, childToken, coord.CompletedCount, coord.TotalCount)
	}

	// Check if all evaluations are complete
	allDone := coord.CompletedCount >= coord.TotalCount
	hasFailure := coord.HasFailure

	e.mu.Unlock()

	if allDone {
		e.logger.DebugCat(CatAsync,"All brace evaluations complete for coordinator %s (failure: %v)",
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

	// Complete the coordinator token (marks as done, releases executor's ref)
	e.completeTokenLocked(coordinatorToken, !hasFailure, nil)

	e.mu.Unlock()

	// Now perform the final substitution and resume callback
	var callbackResult Result
	if hasFailure {
		e.logger.DebugCat(CatAsync,"Brace coordinator %s failed, calling resume with failure", coordinatorToken)
		callbackResult = coord.ResumeCallback("", false)
	} else {
		// Substitute all results into the original string
		finalString := e.substituteAllBraces(coord.OriginalString, coord.Evaluations, coord.SubstitutionCtx.ExecutionState)
		e.logger.DebugCat(CatAsync,"Brace coordinator %s succeeded, substituted string: %s", coordinatorToken, finalString)
		callbackResult = coord.ResumeCallback(finalString, true)
	}

	// Handle the callback result
	if boolStatus, ok := callbackResult.(BoolStatus); ok {
		// Command completed synchronously
		success := bool(boolStatus)
		e.logger.DebugCat(CatAsync,"Brace coordinator callback returned bool: %v", success)

		// If there's a chained token, resume it with this result
		if chainedToken != "" {
			e.logger.DebugCat(CatAsync,"Resuming chained token %s with result %v", chainedToken, success)
			e.PopAndResumeCommandSequence(chainedToken, success)
		}
	} else if tokenResult, ok := callbackResult.(TokenResult); ok {
		// Command returned another token (nested async)
		newToken := string(tokenResult)
		e.logger.DebugCat(CatAsync,"Brace coordinator callback returned new token: %s", newToken)

		// If there's a chained token, chain the new token to it
		if chainedToken != "" {
			e.logger.DebugCat(CatAsync,"Chaining new token %s to %s", newToken, chainedToken)
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

	e.logger.DebugCat(CatAsync,"Pushed command sequence onto token %s. Type: %s, Remaining: %d, hasResult: %v",
		tokenID, seqType, len(remainingCommands), hasResult)

	return nil
}

// PopAndResumeCommandSequence pops and resumes a command sequence
func (e *Executor) PopAndResumeCommandSequence(tokenID string, status bool) bool {
	e.mu.Lock()

	tokenData, exists := e.activeTokens[tokenID]
	if !exists {
		e.mu.Unlock()
		e.logger.WarnCat(CatAsync,"Attempted to resume with invalid token: %s", tokenID)
		return false
	}

	// Apply inversion if this token has the InvertStatus flag
	effectiveStatus := status
	if tokenData.InvertStatus {
		effectiveStatus = !status
		e.logger.DebugCat(CatAsync,"Inverting async result for token %s: %v -> %v", tokenID, status, effectiveStatus)
	}

	// Check if this token's parent is a brace coordinator
	// We need to record this now but forward the result AFTER resumeCommandSequence runs
	// so that remaining commands (like `ret "good"`) can update the result
	var isBraceCoordinatorChild bool
	var coordinatorToken string
	if tokenData.ParentToken != "" {
		if parentData, parentExists := e.activeTokens[tokenData.ParentToken]; parentExists {
			if parentData.BraceCoordinator != nil {
				isBraceCoordinatorChild = true
				coordinatorToken = tokenData.ParentToken
			}
		}
	}

	e.logger.DebugCat(CatAsync,"Popping command sequence from token %s. Result: %v", tokenID, effectiveStatus)

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

	// Now that remaining commands have been executed, forward result to brace coordinator if needed
	// This MUST happen after resumeCommandSequence so that commands like `ret "good"` have run
	if isBraceCoordinatorChild && newChainedToken == "" {
		// No more async pending - we have the final result
		var resultValue interface{}
		if state != nil && state.HasResult() {
			resultValue = state.GetResult()
		}

		// Clean up before forwarding
		e.cleanupTokenChildrenLocked(tokenID)
		e.completeTokenLocked(tokenID, success, resultValue)

		e.mu.Unlock()

		e.logger.DebugCat(CatAsync, "Token %s is child of brace coordinator %s, forwarding result: %v", tokenID, coordinatorToken, resultValue)
		e.ResumeBraceEvaluation(coordinatorToken, tokenID, resultValue, success)

		// Release state references
		if state != nil {
			state.ReleaseAllReferences()
		}

		return success
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

		// If we're a brace coordinator child, propagate that relationship to the new token
		if isBraceCoordinatorChild {
			if newTokenData, exists := e.activeTokens[newChainedToken]; exists {
				newTokenData.ParentToken = coordinatorToken
				// Also register as child of the coordinator
				if coordData, coordExists := e.activeTokens[coordinatorToken]; coordExists {
					coordData.Children[newChainedToken] = true
				}
				e.logger.DebugCat(CatAsync,"Propagated brace coordinator parent %s to new token %s", coordinatorToken, newChainedToken)
			}
		}
	}
	parentToken := tokenData.ParentToken
	fiberID := tokenData.FiberID
	waitChan := tokenData.WaitChan

	// Check if this token belongs to a fiber
	var fiberHandle *FiberHandle
	if fiberID != 0 {
		fiberHandle = e.activeFibers[fiberID]
	}

	// Get the result value before completing
	var resultValue interface{}
	if state != nil {
		resultValue = state.GetResult()
	}

	// Remove this token from parent's children list
	if parentToken != "" {
		if parent, exists := e.activeTokens[parentToken]; exists {
			delete(parent.Children, tokenID)
		}
	}

	// Complete the token (marks as done, releases executor's ref)
	e.completeTokenLocked(tokenID, success, resultValue)

	e.mu.Unlock()

	// If this token belongs to a fiber, send resume data to the fiber
	if fiberHandle != nil {
		e.logger.DebugCat(CatAsync,"Sending resume data to fiber %d for token %s", fiberID, tokenID)
		resumeData := ResumeData{
			TokenID: tokenID,
			Status:  success,
			Result:  state.GetResult(),
		}
		// Non-blocking send since fiber might not be waiting yet
		select {
		case fiberHandle.ResumeChan <- resumeData:
			e.logger.DebugCat(CatAsync,"Successfully sent resume data to fiber %d", fiberID)
		default:
			e.logger.WarnCat(CatAsync,"Fiber %d resume channel full or not ready", fiberID)
		}
	}

	if chainedToken != "" {
		// Propagate waitChan to the chained token so the final token in the chain
		// will signal completion (not this intermediate token)
		if waitChan != nil {
			e.attachWaitChan(chainedToken, waitChan)
			e.logger.DebugCat(CatAsync,"Propagated wait channel to chained token %s", chainedToken)
		}

		// If asyncPending is true, the chainedToken is waiting for an async operation
		// to complete (e.g., msleep). Don't resume it now - it will be triggered
		// automatically when the async operation's token completes.
		if asyncPending {
			e.logger.DebugCat(CatAsync,"Async operation pending, not immediately triggering chained token %s", chainedToken)
			// Release our state references since we're done with this token
			// But only if no other token is using the same state
			if tokenData.ExecutionState != nil {
				stateInUse := false
				for otherID, otherData := range e.activeTokens {
					if otherID != tokenID && otherData.ExecutionState == tokenData.ExecutionState {
						stateInUse = true
						break
					}
				}
				if !stateInUse {
					tokenData.ExecutionState.ReleaseAllReferences()
				}
			}
			return success
		}

		e.logger.DebugCat(CatAsync,"Triggering chained token %s with result %v", chainedToken, success)
		result := e.PopAndResumeCommandSequence(chainedToken, success)

		// Don't release state references here - the chained token (or its chain)
		// will handle releasing the state when the final command completes.
		// If we released here, we'd double-release since the chained token already
		// released when it found no other tokens using the state.

		return result
	}

	// No chained token - this is the final token in the chain

	// If this token has a parent state (from macro async), transfer the result now
	if tokenData.ParentState != nil && state != nil && state.HasResult() {
		tokenData.ParentState.SetResult(state.GetResult())
		e.logger.DebugCat(CatAsync,"Transferred async macro result to parent state: %v", state.GetResult())
	}

	// If this token has a wait channel (synchronous blocking), send to it now
	if waitChan != nil {
		e.logger.DebugCat(CatAsync,"Sending resume data to wait channel for token %s (final in chain)", tokenID)
		resumeData := ResumeData{
			TokenID: tokenID,
			Status:  success,
			Result:  state.GetResult(),
		}
		// Send to wait channel (blocking is expected here)
		waitChan <- resumeData
		e.logger.DebugCat(CatAsync,"Successfully sent resume data to wait channel")
		// Don't release state references here - the caller (e.g., while loop)
		// is still using this state and will continue after receiving from waitChan
		return success
	}

	// No chain and no waitChan - safe to release now
	// But only release if this state is not being used by any other active token
	if tokenData.ExecutionState != nil {
		stateInUse := false
		for otherID, otherData := range e.activeTokens {
			if otherID != tokenID && otherData.ExecutionState == tokenData.ExecutionState {
				stateInUse = true
				break
			}
		}
		if !stateInUse {
			tokenData.ExecutionState.ReleaseAllReferences()
		}
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

// completeTokenLocked marks a token as completed and releases the executor's reference.
// The token remains in storedObjects until its refcount reaches 0 (GC'd).
// Must be called with e.mu lock held.
func (e *Executor) completeTokenLocked(tokenID string, status bool, result interface{}) {
	tokenData, exists := e.activeTokens[tokenID]
	if !exists {
		return
	}

	// Mark as completed with final status/result
	tokenData.Completed = true
	tokenData.FinalStatus = status
	tokenData.FinalResult = result

	// If the result is an ObjectRef, claim a reference so it's not GC'd
	// while the token still holds it (same pattern as lists claiming nested items)
	if resultRef, ok := result.(ObjectRef); ok && resultRef.IsValid() {
		if obj, objExists := e.storedObjects[resultRef.ID]; objExists && !obj.Deleted {
			obj.RefCount++
			e.logger.DebugCat(CatMemory, "Token %s claims result ref %v, refcount now %d",
				tokenID, resultRef, obj.RefCount)
		}
	}

	// Cancel the context (stops timeout goroutine)
	if tokenData.CancelFunc != nil {
		tokenData.CancelFunc()
	}

	// Remove from activeTokens (no longer actively running)
	delete(e.activeTokens, tokenID)

	// Remove from string→ID lookup (no longer need reverse lookup for resume)
	delete(e.tokenStringToID, tokenID)

	// Check if token can be freed (no external refs)
	// Executor doesn't hold a claim - only external claims (SetResult, SetVariable) affect refcount
	objectID := tokenData.ObjectID
	if obj, objExists := e.storedObjects[objectID]; objExists && !obj.Deleted {
		e.logger.DebugCat(CatMemory, "Token %s completed, refcount is %d", tokenID, obj.RefCount)
		if obj.RefCount <= 0 {
			// No external references, can be freed
			// First release the FinalResult ref if it was an ObjectRef
			if resultRef, ok := tokenData.FinalResult.(ObjectRef); ok && resultRef.IsValid() {
				if resultObj, resultExists := e.storedObjects[resultRef.ID]; resultExists && !resultObj.Deleted {
					resultObj.RefCount--
					e.logger.DebugCat(CatMemory, "Token %s releasing result ref %v, refcount now %d",
						tokenID, resultRef, resultObj.RefCount)
				}
			}
			obj.Deleted = true
			obj.Value = nil
			e.freeIDs = append(e.freeIDs, objectID)
			e.logger.DebugCat(CatMemory, "Token object %d freed (no refs)", objectID)
		}
	}
}

// forceDeleteTokenLocked immediately removes a token from the object system.
// Used for forced cleanup (timeout, cancellation). Bypasses normal GC.
// Must be called with e.mu lock held.
func (e *Executor) forceDeleteTokenLocked(tokenID string) {
	tokenData, exists := e.activeTokens[tokenID]
	if !exists {
		return
	}

	// Cancel the context
	if tokenData.CancelFunc != nil {
		tokenData.CancelFunc()
	}

	// Remove from activeTokens
	delete(e.activeTokens, tokenID)

	// Remove from string→ID lookup
	delete(e.tokenStringToID, tokenID)

	// Force delete from object system (bypass refcount)
	objectID := tokenData.ObjectID
	if obj, objExists := e.storedObjects[objectID]; objExists {
		obj.Deleted = true
		obj.Value = nil
		e.freeIDs = append(e.freeIDs, objectID)
		e.logger.DebugCat(CatMemory, "Token object %d force deleted", objectID)
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

	e.logger.DebugCat(CatAsync, "Force cleaning up token: %s", tokenID)

	if tokenData.CleanupCallback != nil {
		tokenData.CleanupCallback(tokenID)
	}

	e.cleanupTokenChildrenLocked(tokenID)

	// Release all object references held by this token's state
	// But only if no other token is using the same state
	if tokenData.ExecutionState != nil {
		stateInUse := false
		for otherID, otherData := range e.activeTokens {
			if otherID != tokenID && otherData.ExecutionState == tokenData.ExecutionState {
				stateInUse = true
				break
			}
		}
		if !stateInUse {
			tokenData.ExecutionState.ReleaseAllReferences()
		}
	}

	e.forceDeleteTokenLocked(tokenID)
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
		e.logErrorWithContext(CatAsync, fmt.Sprintf("Unknown command sequence type: %s", seq.Type), state, nil)
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
			e.logger.DebugCat(CatAsync,"Command returned early return during resume, terminating sequence")
			if earlyReturn.HasResult {
				state.SetResult(earlyReturn.Result)
			}
			return bool(earlyReturn.Status), ""
		}

		if tokenResult, ok := cmdResult.(TokenResult); ok {
			e.logger.DebugCat(CatAsync,"Command returned token during resume: %s, chaining remaining commands", string(tokenResult))

			// Handle remaining commands after this token
			remainingCommands := seq.RemainingCommands[i+1:]
			if len(remainingCommands) > 0 {
				// Create a new sequence token for the remaining commands
				sequenceToken := e.RequestCompletionToken(
					func(tokenID string) {
						e.logger.DebugCat(CatAsync,"Cleaning up suspended sequence for token %s", tokenID)
					},
					"",
					5*time.Minute,
					state,
					parsedCmd.Position,
				)

				err := e.PushCommandSequence(sequenceToken, "sequence", remainingCommands, 0, "sequence", state, parsedCmd.Position)
				if err != nil {
					e.logErrorWithContext(CatAsync, fmt.Sprintf("Failed to push command sequence: %v", err), state, parsedCmd.Position)
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
			e.logger.DebugCat(CatAsync,"Command returned early return during resume, terminating sequence")
			if earlyReturn.HasResult {
				state.SetResult(earlyReturn.Result)
			}
			return bool(earlyReturn.Status), ""
		}

		if tokenResult, ok := cmdResult.(TokenResult); ok {
			e.logger.DebugCat(CatAsync,"Command returned token during conditional resume: %s, chaining remaining commands", string(tokenResult))

			// Handle remaining commands after this token
			remainingCommands := seq.RemainingCommands[i+1:]
			if len(remainingCommands) > 0 {
				// Create a new sequence token for the remaining commands
				sequenceToken := e.RequestCompletionToken(
					func(tokenID string) {
						e.logger.DebugCat(CatAsync,"Cleaning up suspended conditional sequence for token %s", tokenID)
					},
					"",
					5*time.Minute,
					state,
					parsedCmd.Position,
				)

				err := e.PushCommandSequence(sequenceToken, "conditional", remainingCommands, 0, "conditional", state, parsedCmd.Position)
				if err != nil {
					e.logErrorWithContext(CatAsync, fmt.Sprintf("Failed to push command sequence: %v", err), state, parsedCmd.Position)
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
			e.logger.DebugCat(CatAsync,"Command returned early return during resume, terminating sequence")
			if earlyReturn.HasResult {
				state.SetResult(earlyReturn.Result)
			}
			return bool(earlyReturn.Status), ""
		}

		if tokenResult, ok := cmdResult.(TokenResult); ok {
			e.logger.DebugCat(CatAsync,"Command returned token during OR resume: %s, chaining remaining commands", string(tokenResult))

			// Handle remaining commands after this token
			remainingCommands := seq.RemainingCommands[i+1:]
			if len(remainingCommands) > 0 {
				// Create a new sequence token for the remaining commands
				sequenceToken := e.RequestCompletionToken(
					func(tokenID string) {
						e.logger.DebugCat(CatAsync,"Cleaning up suspended OR sequence for token %s", tokenID)
					},
					"",
					5*time.Minute,
					state,
					parsedCmd.Position,
				)

				err := e.PushCommandSequence(sequenceToken, "or", remainingCommands, 0, "or", state, parsedCmd.Position)
				if err != nil {
					e.logErrorWithContext(CatAsync, fmt.Sprintf("Failed to push command sequence: %v", err), state, parsedCmd.Position)
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
		// Try to get a state from whichever token exists for error routing
		var state *ExecutionState
		if exists1 {
			state = firstTokenData.ExecutionState
		} else if exists2 {
			state = secondTokenData.ExecutionState
		}
		e.logErrorWithContext(CatAsync, fmt.Sprintf("Cannot chain tokens: %s or %s not found", firstToken, secondToken), state, nil)
		return
	}

	firstTokenData.ChainedToken = secondToken
	secondTokenData.ParentToken = firstToken

	e.logger.DebugCat(CatAsync,"Chained token %s to complete after %s", secondToken, firstToken)
}

// attachWaitChan attaches a wait channel to a token for synchronous blocking
func (e *Executor) attachWaitChan(tokenID string, waitChan chan ResumeData) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if tokenData, exists := e.activeTokens[tokenID]; exists {
		tokenData.WaitChan = waitChan
		e.logger.DebugCat(CatAsync,"Attached wait channel to token %s", tokenID)
	} else {
		e.logger.WarnCat(CatAsync,"Attempted to attach wait channel to non-existent token: %s", tokenID)
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
