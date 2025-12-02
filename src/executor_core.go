package pawscript

import (
	"fmt"
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
	mu               sync.RWMutex
	commands         map[string]Handler
	activeTokens     map[string]*TokenData
	storedObjects    map[int]*StoredObject // Global reference-counted object store
	activeFibers     map[int]*FiberHandle  // Currently running fibers
	orphanedBubbles  map[string][]*BubbleEntry // Bubbles from abandoned fibers
	nextTokenID      int
	nextObjectID     int
	nextFiberID      int
	logger           *Logger
	fallbackHandler  func(cmdName string, args []interface{}, namedArgs map[string]interface{}, state *ExecutionState, position *SourcePosition) Result
}

// NewExecutor creates a new command executor
func NewExecutor(logger *Logger) *Executor {
	return &Executor{
		commands:        make(map[string]Handler),
		activeTokens:    make(map[string]*TokenData),
		storedObjects:   make(map[int]*StoredObject),
		activeFibers:    make(map[int]*FiberHandle),
		orphanedBubbles: make(map[string][]*BubbleEntry),
		nextTokenID:     1,
		nextObjectID:    1,
		nextFiberID:     1, // 0 is reserved for main fiber
		logger:          logger,
	}
}

// AddOrphanedBubbles merges bubbles from an abandoned fiber into orphanedBubbles
// The references are transferred (not released) - caller must NOT release them after this call
func (e *Executor) AddOrphanedBubbles(bubbleMap map[string][]*BubbleEntry) {
	if len(bubbleMap) == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for flavor, entries := range bubbleMap {
		e.orphanedBubbles[flavor] = append(e.orphanedBubbles[flavor], entries...)
	}
	e.logger.DebugCat(CatMemory, "Added orphaned bubbles from abandoned fiber")
}

// GetOrphanedBubbles returns the current orphaned bubbles map
// Does NOT clear the map - caller should call ClearOrphanedBubbles after processing
func (e *Executor) GetOrphanedBubbles() map[string][]*BubbleEntry {
	e.mu.RLock()
	defer e.mu.RUnlock()
	// Return a copy to avoid race conditions
	result := make(map[string][]*BubbleEntry)
	for flavor, entries := range e.orphanedBubbles {
		result[flavor] = entries
	}
	return result
}

// ClearOrphanedBubbles clears the orphaned bubbles map
// Should be called after retrieving and processing orphaned bubbles
func (e *Executor) ClearOrphanedBubbles() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.orphanedBubbles = make(map[string][]*BubbleEntry)
}

// RegisterCommand registers a command handler
func (e *Executor) RegisterCommand(name string, handler Handler) {
	e.mu.Lock()
	e.commands[name] = handler
	e.mu.Unlock()
	e.logger.DebugCat(CatCommand, "Registered command: %s", name)
}

// UnregisterCommand unregisters a command
func (e *Executor) UnregisterCommand(name string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.commands[name]; exists {
		delete(e.commands, name)
		e.logger.DebugCat(CatCommand, "Unregistered command: %s", name)
		return true
	}

	e.logger.WarnCat(CatCommand, "Attempted to unregister unknown command: %s", name)
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
	e.logger.DebugCat(CatCommand, "Execute called with command: %s", commandStr)

	state := NewExecutionState()

	// If args provided, execute as direct command call
	if len(args) > 0 {
		e.mu.RLock()
		handler, exists := e.commands[commandStr]
		e.mu.RUnlock()

		if exists {
			ctx := e.createContext(args, nil, nil, state, nil)
			result := handler(ctx)
			// Only release state if not returning a token (async operation)
			if _, isToken := result.(TokenResult); !isToken {
				state.ReleaseAllReferences()
			}
			return result
		}

		e.logger.UnknownCommandError(commandStr, nil, nil)
		state.SetResult(Symbol(UndefinedMarker)) // Marker not bare Symbol - bare Symbol("undefined") clears the result
		state.ReleaseAllReferences()
		return BoolStatus(false)
	}

	result := e.ExecuteWithState(commandStr, state, nil, "", 0, 0)
	// Only release state if not returning a token (async operation)
	// The token system will release the state when the async operation completes
	if _, isToken := result.(TokenResult); !isToken {
		state.ReleaseAllReferences()
	}
	return result
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
func (e *Executor) createContext(args []interface{}, rawArgs []string, namedArgs map[string]interface{}, state *ExecutionState, position *SourcePosition) *Context {
	return &Context{
		Args:      args,
		RawArgs:   rawArgs,
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

// ExecuteStoredMacro executes a StoredMacro object directly
func (e *Executor) ExecuteStoredMacro(
	macro *StoredMacro,
	executeCallback func(commands string, state *ExecutionState, ctx *SubstitutionContext) Result,
	args []interface{},
	namedArgs map[string]interface{},
	state *ExecutionState,
	invocationPosition *SourcePosition,
	parentState *ExecutionState,
) Result {
	return e.executeStoredMacro("", macro, executeCallback, args, namedArgs, state, invocationPosition, parentState)
}

// executeStoredMacro is the internal implementation for executing a StoredMacro
func (e *Executor) executeStoredMacro(
	name string,
	macro *StoredMacro,
	executeCallback func(commands string, state *ExecutionState, ctx *SubstitutionContext) Result,
	args []interface{},
	namedArgs map[string]interface{},
	state *ExecutionState,
	invocationPosition *SourcePosition,
	parentState *ExecutionState,
) Result {
	// Check for unresolved forward declaration
	if macro.IsForward {
		macroDesc := name
		if macroDesc == "" {
			macroDesc = "<anonymous>"
		}
		e.logger.ErrorCat(CatMacro, "Cannot call macro '%s': forward declaration was never resolved with a macro definition", macroDesc)
		if state != nil {
			state.SetResult(Symbol("undefined"))
		}
		return BoolStatus(false)
	}

	// Create macro context for error tracking
	macroContext := &MacroContext{
		MacroName:        name, // Empty for anonymous macros
		DefinitionFile:   macro.DefinitionFile,
		DefinitionLine:   macro.DefinitionLine,
		DefinitionColumn: macro.DefinitionColumn,
	}

	if invocationPosition != nil {
		macroContext.InvocationFile = invocationPosition.Filename
		macroContext.InvocationLine = invocationPosition.Line
		macroContext.InvocationColumn = invocationPosition.Column
	}
	// Parent macro context comes from the parent state, not the position
	if parentState != nil {
		macroContext.ParentMacro = parentState.macroContext
	}

	debugInfo := ""
	if name != "" {
		debugInfo = fmt.Sprintf("Executing macro \"%s\" defined at %s:%d",
			name, macro.DefinitionFile, macro.DefinitionLine)
	} else {
		debugInfo = fmt.Sprintf("Executing anonymous macro defined at %s:%d",
			macro.DefinitionFile, macro.DefinitionLine)
	}
	if invocationPosition != nil {
		debugInfo += fmt.Sprintf(", called from %s:%d",
			invocationPosition.Filename, invocationPosition.Line)
	}
	e.logger.DebugCat(CatMacro, "%s", debugInfo)

	// Create execution state if not provided
	if state == nil {
		state = NewExecutionState()
	}

	// Ensure state has executor reference
	if state.executor == nil {
		state.executor = e
	}

	// Set macro context for stack traces
	state.macroContext = macroContext

	// Set default module name to "exports" so any EXPORT calls in the macro
	// will export to the "exports" module, which can be merged into caller
	state.moduleEnv.mu.Lock()
	state.moduleEnv.DefaultName = "exports"
	state.moduleEnv.mu.Unlock()

	// Create a LIST from the arguments (both positional and named) and store it as $@
	argsList := NewStoredListWithRefs(args, namedArgs, state.executor)
	argsListID := state.executor.storeObject(argsList, "list")
	argsMarker := fmt.Sprintf("\x00LIST:%d\x00", argsListID)

	// Store the list marker in the state's variables as $@
	// SetVariable will claim the reference
	state.SetVariable("$@", Symbol(argsMarker))

	// Create substitution context for macro arguments
	// Use macro definition location for error reporting within macro body
	substitutionContext := &SubstitutionContext{
		Args:                args,
		ExecutionState:      state,
		MacroContext:        macroContext,
		CurrentLineOffset:   macro.DefinitionLine - 1,
		CurrentColumnOffset: macro.DefinitionColumn - 1,
		Filename:            macro.DefinitionFile,
	}

	// Execute the macro commands
	result := executeCallback(macro.Commands, state, substitutionContext)

	// Merge macro exports into parent's LibraryInherited under "exports" module
	if parentState != nil {
		state.moduleEnv.mu.RLock()
		if exportsSection, exists := state.moduleEnv.ModuleExports["exports"]; exists && len(exportsSection) > 0 {
			parentState.moduleEnv.mu.Lock()
			// Ensure exports module exists in parent's LibraryInherited
			if parentState.moduleEnv.LibraryInherited == nil {
				parentState.moduleEnv.LibraryInherited = make(Library)
			}
			if parentState.moduleEnv.LibraryInherited["exports"] == nil {
				parentState.moduleEnv.LibraryInherited["exports"] = make(ModuleSection)
			}
			// Copy all exported items
			for name, item := range exportsSection {
				parentState.moduleEnv.LibraryInherited["exports"][name] = item
			}
			parentState.moduleEnv.mu.Unlock()
			e.logger.DebugCat(CatMacro, "Merged %d exports from macro to parent's exports module", len(exportsSection))
		}
		state.moduleEnv.mu.RUnlock()
	}

	// Transfer result to parent state
	if parentState != nil && state.HasResult() {
		// Ensure parent has executor reference
		if parentState.executor == nil && state.executor != nil {
			parentState.executor = state.executor
		}

		// Set result in parent (this will claim ownership)
		parentState.SetResult(state.GetResult())

		// Don't clear macro result here - ReleaseAllReferences will handle it

		e.logger.DebugCat(CatMacro, "Transferred macro result to parent state: %v", state.GetResult())
	}

	// Clear all variables (including $@) to release their references
	state.mu.Lock()
	for varName := range state.variables {
		oldValue := state.variables[varName]
		delete(state.variables, varName)

		// Extract and release references from the old variable value
		oldRefs := state.extractObjectReferencesLocked(oldValue)
		state.mu.Unlock()
		for _, id := range oldRefs {
			state.ReleaseObjectReference(id)
		}
		state.mu.Lock()
	}
	state.mu.Unlock()

	// Release all remaining owned references
	state.ReleaseAllReferences()

	if name != "" {
		e.logger.DebugCat(CatMacro, "Macro \"%s\" execution completed with result: %v", name, result)
	} else {
		e.logger.DebugCat(CatMacro, "Anonymous macro execution completed with result: %v", result)
	}
	return result
}
