package pawscript

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// StoredObject represents a reference-counted stored object
type StoredObject struct {
	Value    interface{} // The actual object (StoredList, etc.)
	Type     string      // "list", "dict", etc.
	RefCount int         // Number of contexts holding references
}

// Executor handles command execution
type Executor struct {
	mu              sync.RWMutex
	commands        map[string]Handler
	activeTokens    map[string]*TokenData
	storedObjects   map[int]*StoredObject // Global reference-counted object store
	nextTokenID     int
	nextObjectID    int
	logger          *Logger
	fallbackHandler func(cmdName string, args []interface{}, state *ExecutionState, position *SourcePosition) Result
}

// NewExecutor creates a new command executor
func NewExecutor(logger *Logger) *Executor {
	return &Executor{
		commands:      make(map[string]Handler),
		activeTokens:  make(map[string]*TokenData),
		storedObjects: make(map[int]*StoredObject),
		nextTokenID:   1,
		nextObjectID:  1,
		logger:        logger,
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
	if tokenData.CommandSequence != nil {
		seq := tokenData.CommandSequence
		e.mu.Unlock() // Unlock before resuming to avoid deadlock
		success = e.resumeCommandSequence(seq, effectiveStatus, state)
		e.mu.Lock()
	}

	chainedToken := tokenData.ChainedToken
	parentToken := tokenData.ParentToken
	
	// Release all object references held by this token's state
	if tokenData.ExecutionState != nil {
		tokenData.ExecutionState.ReleaseAllReferences()
	}

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
	
	// Release all object references held by this token's state
	if tokenData.ExecutionState != nil {
		tokenData.ExecutionState.ReleaseAllReferences()
	}
	
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
	// Ensure cleanup happens when execution completes
	defer state.ReleaseAllReferences()

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
	// Ensure state has executor reference for object management
	if state != nil && state.executor == nil {
		state.executor = e
	}
	
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

// maybeStoreValue checks if a value should be stored as an object and returns the appropriate representation
// Note: Does NOT claim references - the caller must claim the returned object ID
func (e *Executor) maybeStoreValue(value interface{}, state *ExecutionState) interface{} {
	switch v := value.(type) {
	case string:
		if len(v) > StringStorageThreshold {
			id := e.storeObject(StoredString(v), "string")
			return Symbol(fmt.Sprintf("\x00STR:%d\x00", id))
		}
		return v
	case QuotedString:
		if len(v) > StringStorageThreshold {
			id := e.storeObject(StoredString(v), "string")
			return Symbol(fmt.Sprintf("\x00STR:%d\x00", id))
		}
		return v
	case ParenGroup:
		if len(v) > BlockStorageThreshold {
			id := e.storeObject(StoredBlock(v), "block")
			return Symbol(fmt.Sprintf("\x00BLOCK:%d\x00", id))
		}
		return v
	default:
		return value
	}
}

// storeObject stores an object in the global store with an initial refcount of 0
// Returns the object ID
func (e *Executor) storeObject(value interface{}, typeName string) int {
	e.mu.Lock()
	defer e.mu.Unlock()

	id := e.nextObjectID
	e.nextObjectID++

	e.storedObjects[id] = &StoredObject{
		Value:    value,
		Type:     typeName,
		RefCount: 0, // Start at 0 - creator must claim ownership
	}

	e.logger.Debug("Stored object %d (type: %s, refcount: 0)", id, typeName)
	
	return id
}

// incrementObjectRefCount increments the reference count for an object
func (e *Executor) incrementObjectRefCount(objectID int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if obj, exists := e.storedObjects[objectID]; exists {
		obj.RefCount++
		e.logger.Debug("Object %d refcount incremented to %d (type: %s)", objectID, obj.RefCount, obj.Type)
	} else {
		e.logger.Warn("Attempted to increment refcount for non-existent object %d", objectID)
	}
}

// decrementObjectRefCount decrements the reference count and frees if zero
func (e *Executor) decrementObjectRefCount(objectID int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if obj, exists := e.storedObjects[objectID]; exists {
		obj.RefCount--
		e.logger.Debug("Object %d refcount decremented to %d (type: %s)", objectID, obj.RefCount, obj.Type)
		
		if obj.RefCount <= 0 {
			// Before deleting, release nested references if it's a list
			if storedList, ok := obj.Value.(StoredList); ok {
				e.mu.Unlock() // Unlock before recursive calls
				for _, item := range storedList.Items() {
					releaseNestedReferences(item, e)
				}
				e.mu.Lock() // Re-lock for deletion
			}
			
			delete(e.storedObjects, objectID)
			e.logger.Debug("Object %d freed (refcount reached 0)", objectID)
		}
	} else {
		e.logger.Warn("Attempted to decrement refcount for non-existent object %d", objectID)
	}
}

// getObject retrieves an object from the store without affecting refcount
func (e *Executor) getObject(objectID int) (interface{}, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if obj, exists := e.storedObjects[objectID]; exists {
		return obj.Value, true
	}
	return nil, false
}

// resolveValue resolves any object marker (LIST/STRING/BLOCK) to its actual value
// If the value is not a marker, returns it unchanged
// This is the central resolution function - all resolution should go through here
func (e *Executor) resolveValue(value interface{}) interface{} {
	// Check if it's a Symbol that might be a marker
	if sym, ok := value.(Symbol); ok {
		if objType, objID := parseObjectMarker(string(sym)); objID >= 0 {
			if actualValue, exists := e.getObject(objID); exists {
				e.logger.Debug("Resolved %s marker %d to actual value", objType, objID)
				// Convert stored types back to their original forms
				switch v := actualValue.(type) {
				case StoredString:
					return string(v)
				case StoredBlock:
					return ParenGroup(v)
				default:
					return actualValue
				}
			}
		}
	}
	
	// Check if it's a string that might be a marker
	if str, ok := value.(string); ok {
		if objType, objID := parseObjectMarker(str); objID >= 0 {
			if actualValue, exists := e.getObject(objID); exists {
				e.logger.Debug("Resolved %s marker %d to actual value", objType, objID)
				// Convert stored types back to their original forms
				switch v := actualValue.(type) {
				case StoredString:
					return string(v)
				case StoredBlock:
					return ParenGroup(v)
				default:
					return actualValue
				}
			}
		}
	}
	
	// Not a marker, return as-is
	return value
}

// resolveValueDeep recursively resolves markers, including nested structures
// Use this when you need to resolve markers within lists
func (e *Executor) resolveValueDeep(value interface{}) interface{} {
	resolved := e.resolveValue(value)
	
	// If it resolved to a list, recursively resolve its items
	if list, ok := resolved.(StoredList); ok {
		items := list.Items()
		resolvedItems := make([]interface{}, len(items))
		hasChanges := false
		
		for i, item := range items {
			resolvedItem := e.resolveValueDeep(item)
			resolvedItems[i] = resolvedItem
			if resolvedItem != item {
				hasChanges = true
			}
		}
		
		if hasChanges {
			return NewStoredList(resolvedItems)
		}
	}
	
	return resolved
}

// findStoredListID finds the ID of a StoredList by searching storedObjects
// Returns -1 if not found
func (e *Executor) findStoredListID(list StoredList) int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	
	// Compare by checking if they share the same backing array
	for id, obj := range e.storedObjects {
		if objList, ok := obj.Value.(StoredList); ok {
			// Two slices share backing array if they have same length and same first element address
			if len(objList.items) == len(list.items) {
				if len(objList.items) == 0 {
					// Both empty - match any empty list for now
					return id
				}
				// Check if they point to the same backing array
				if &objList.items[0] == &list.items[0] {
					return id
				}
			}
		}
	}
	
	return -1
}
