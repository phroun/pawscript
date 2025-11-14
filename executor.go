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
	nextTokenID     int
	logger          *Logger
	fallbackHandler func(cmdName string, args []interface{}, state *ExecutionState, position *SourcePosition) Result
}

// NewExecutor creates a new command executor
func NewExecutor(logger *Logger) *Executor {
	return &Executor{
		commands:     make(map[string]Handler),
		activeTokens: make(map[string]*TokenData),
		nextTokenID:  1,
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
func (e *Executor) PopAndResumeCommandSequence(tokenID string, result bool) bool {
	e.mu.Lock()
	
	tokenData, exists := e.activeTokens[tokenID]
	if !exists {
		e.mu.Unlock()
		e.logger.Warn("Attempted to resume with invalid token: %s", tokenID)
		return false
	}
	
	e.logger.Debug("Popping command sequence from token %s. Result: %v", tokenID, result)
	
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
	
	success := result
	if tokenData.CommandSequence != nil {
		seq := tokenData.CommandSequence
		e.mu.Unlock() // Unlock before resuming to avoid deadlock
		success = e.resumeCommandSequence(seq, result, state)
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
func (e *Executor) resumeCommandSequence(seq *CommandSequence, result bool, state *ExecutionState) bool {
	switch seq.Type {
	case "sequence":
		return e.resumeSequence(seq, result, state)
	case "conditional":
		return e.resumeConditional(seq, result, state)
	case "or":
		return e.resumeOr(seq, result, state)
	default:
		e.logger.Error("Unknown command sequence type: %s", seq.Type)
		return false
	}
}

// resumeSequence resumes a sequential command sequence
func (e *Executor) resumeSequence(seq *CommandSequence, result bool, state *ExecutionState) bool {
	success := result
	
	for _, parsedCmd := range seq.RemainingCommands {
		if strings.TrimSpace(parsedCmd.Command) == "" {
			continue
		}
		
		cmdResult := e.executeParsedCommand(parsedCmd, state, nil)
		
		if tokenResult, ok := cmdResult.(TokenResult); ok {
			e.logger.Warn("Command returned token during resume: %s", string(tokenResult))
			return false
		}
		
		success = bool(cmdResult.(BoolResult))
	}
	
	return success
}

// resumeConditional resumes a conditional sequence
func (e *Executor) resumeConditional(seq *CommandSequence, result bool, state *ExecutionState) bool {
	if !result {
		return false
	}
	
	success := result
	
	for _, parsedCmd := range seq.RemainingCommands {
		if strings.TrimSpace(parsedCmd.Command) == "" {
			continue
		}
		
		cmdResult := e.executeParsedCommand(parsedCmd, state, nil)
		
		if tokenResult, ok := cmdResult.(TokenResult); ok {
			e.logger.Warn("Command returned token during resume: %s", string(tokenResult))
			return false
		}
		
		success = bool(cmdResult.(BoolResult))
		if !success {
			break
		}
	}
	
	return success
}

// resumeOr resumes an OR sequence
func (e *Executor) resumeOr(seq *CommandSequence, result bool, state *ExecutionState) bool {
	if result {
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
		
		success = bool(cmdResult.(BoolResult))
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
		return BoolResult(false)
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
	
	commands, err := parser.ParseCommandSequence(cleanedCommand)
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
		return BoolResult(false)
	}
	
	if len(commands) == 0 {
		return BoolResult(true)
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
