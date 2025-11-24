package pawscript

import (
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
	activeFibers    map[int]*FiberHandle  // Currently running fibers
	nextTokenID     int
	nextObjectID    int
	nextFiberID     int
	logger          *Logger
	fallbackHandler func(cmdName string, args []interface{}, namedArgs map[string]interface{}, state *ExecutionState, position *SourcePosition) Result
}

// NewExecutor creates a new command executor
func NewExecutor(logger *Logger) *Executor {
	return &Executor{
		commands:      make(map[string]Handler),
		activeTokens:  make(map[string]*TokenData),
		storedObjects: make(map[int]*StoredObject),
		activeFibers:  make(map[int]*FiberHandle),
		nextTokenID:   1,
		nextObjectID:  1,
		nextFiberID:   1, // 0 is reserved for main fiber
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

// GetCommand retrieves a command handler by name
func (e *Executor) GetCommand(name string) (Handler, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	handler, exists := e.commands[name]
	return handler, exists
}

// SetFallbackHandler sets a fallback handler for unknown commands
func (e *Executor) SetFallbackHandler(handler func(string, []interface{}, map[string]interface{}, *ExecutionState, *SourcePosition) Result) {
	e.mu.Lock()
	e.fallbackHandler = handler
	e.mu.Unlock()
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
			ctx := e.createContext(args, nil, state, nil)
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

// createContext creates a command context
func (e *Executor) createContext(args []interface{}, namedArgs map[string]interface{}, state *ExecutionState, position *SourcePosition) *Context {
	return &Context{
		Args:      args,
		NamedArgs: namedArgs,
		Position:  position,
		state:     state,
		executor:  e,
		logger:    e.logger,
		requestToken: func(cleanup func(string)) string {
			return e.RequestCompletionToken(cleanup, "", 5*time.Minute, state, position)
		},
		resumeToken: func(tokenID string, status bool) bool {
			return e.PopAndResumeCommandSequence(tokenID, status)
		},
	}
}
