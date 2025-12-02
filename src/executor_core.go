package pawscript

import (
	"fmt"
	"regexp"
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
	blockCache       map[int][]*ParsedCommand  // Cached parsed forms for StoredBlock objects (by ID)
	nextTokenID      int
	nextObjectID     int
	nextFiberID      int
	logger           *Logger
	optLevel         OptimizationLevel // AST caching level
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
		blockCache:      make(map[int][]*ParsedCommand),
		nextTokenID:     1,
		nextObjectID:    1,
		nextFiberID:     1, // 0 is reserved for main fiber
		logger:          logger,
		optLevel:        OptimizeBasic, // Default to caching enabled
	}
}

// SetOptimizationLevel sets the AST caching level
func (e *Executor) SetOptimizationLevel(level OptimizationLevel) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.optLevel = level
}

// GetOptimizationLevel returns the current AST caching level
func (e *Executor) GetOptimizationLevel() OptimizationLevel {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.optLevel
}

// GetOrParseMacroCommands returns cached parsed commands for a macro, or parses and caches them
// If caching is disabled (OptimizeNone), it parses fresh each time
// The returned commands can be executed with ExecuteParsedCommands
func (e *Executor) GetOrParseMacroCommands(macro *StoredMacro, filename string) ([]*ParsedCommand, error) {
	// Check if we already have cached commands
	if macro.CachedCommands != nil && e.optLevel >= OptimizeBasic {
		e.logger.DebugCat(CatCommand, "Using cached parsed commands for macro")
		return macro.CachedCommands, nil
	}

	// Parse the commands
	parser := NewParser(macro.Commands, filename)
	cleanedCommand := parser.RemoveComments(macro.Commands)
	normalizedCommand := parser.NormalizeKeywords(cleanedCommand)

	commands, err := parser.ParseCommandSequence(normalizedCommand)
	if err != nil {
		return nil, err
	}

	// Cache the parsed commands if optimization is enabled
	if e.optLevel >= OptimizeBasic && len(commands) > 0 {
		macro.CachedCommands = commands
		e.logger.DebugCat(CatCommand, "Cached %d parsed commands for macro", len(commands))

		// Pre-cache brace expressions and substitution templates in each command
		for _, cmd := range commands {
			e.preCacheBraceExpressions(cmd, cmd.Command, filename)
			e.PreCacheCommandTemplates(cmd, filename)
		}
	}

	return commands, nil
}

// dollarSubstitutionPattern matches $N (like $1, $2) and $* patterns
var dollarSubstitutionPattern = regexp.MustCompile(`\$(\d+|\*)`)

// GetOrCacheBlockArg returns cached parsed commands for a block argument, or parses and caches them.
// This is called by loop commands (while, for, repeat, fizz) to cache their body blocks.
// If the block contains $N patterns, it cannot be cached and nil is returned (caller should parse fresh).
// If cmd is nil (not from cached macro), nil is returned and caller should parse fresh.
func (e *Executor) GetOrCacheBlockArg(cmd *ParsedCommand, argIndex int, blockStr string, filename string) []*ParsedCommand {
	if e.optLevel < OptimizeBasic || cmd == nil {
		return nil // Caller should parse fresh
	}

	// Check if already cached
	if cmd.CachedBlockArgs != nil {
		if cached, exists := cmd.CachedBlockArgs[argIndex]; exists {
			e.logger.DebugCat(CatCommand, "Using cached block at arg %d (%d commands)", argIndex, len(cached))
			return cached
		}
	}

	// Skip if the block contains $N substitution patterns (needs per-invocation parsing)
	if dollarSubstitutionPattern.MatchString(blockStr) {
		e.logger.DebugCat(CatCommand, "Block at arg %d contains $ pattern, cannot cache", argIndex)
		return nil
	}

	// Parse the block
	parser := NewParser(blockStr, filename)
	cleanedBlock := parser.RemoveComments(blockStr)
	normalizedBlock := parser.NormalizeKeywords(cleanedBlock)

	parsedBlock, err := parser.ParseCommandSequence(normalizedBlock)
	if err != nil {
		e.logger.DebugCat(CatCommand, "Failed to parse block at arg %d: %v", argIndex, err)
		return nil
	}

	if len(parsedBlock) == 0 {
		return nil
	}

	// Store the cached parsed block
	if cmd.CachedBlockArgs == nil {
		cmd.CachedBlockArgs = make(map[int][]*ParsedCommand)
	}
	cmd.CachedBlockArgs[argIndex] = parsedBlock
	e.logger.DebugCat(CatCommand, "Cached block at arg %d (%d commands)", argIndex, len(parsedBlock))

	return parsedBlock
}

// preCacheBraceExpressions finds and pre-parses brace expressions in a command string.
// It stores the parsed commands in cmd.CachedBraces keyed by brace content.
// This is called during macro body caching to avoid re-parsing braces at runtime.
func (e *Executor) preCacheBraceExpressions(cmd *ParsedCommand, commandStr string, filename string) {
	if e.optLevel < OptimizeBasic || cmd == nil {
		return
	}

	// Skip if the command string contains $N patterns (needs per-invocation parsing)
	if dollarSubstitutionPattern.MatchString(commandStr) {
		return
	}

	// Find all brace expressions in the command string
	braces := e.findAllTopLevelBraces(commandStr, nil)
	if len(braces) == 0 {
		return
	}

	for _, brace := range braces {
		// Skip ${...} unescape braces - they're not executed as code
		if brace.IsUnescape {
			continue
		}

		content := brace.Content

		// Skip if content contains $N patterns
		if dollarSubstitutionPattern.MatchString(content) {
			continue
		}

		// Parse the brace content
		parser := NewParser(content, filename)
		cleanedContent := parser.RemoveComments(content)
		normalizedContent := parser.NormalizeKeywords(cleanedContent)

		parsedCmds, err := parser.ParseCommandSequence(normalizedContent)
		if err != nil {
			e.logger.DebugCat(CatCommand, "Failed to pre-parse brace content: %v", err)
			continue
		}

		if len(parsedCmds) == 0 {
			continue
		}

		// Store in cache
		if cmd.CachedBraces == nil {
			cmd.CachedBraces = make(map[string][]*ParsedCommand)
		}
		cmd.CachedBraces[content] = parsedCmds
		e.logger.DebugCat(CatCommand, "Pre-cached brace expression: {%s} (%d commands)", content, len(parsedCmds))

		// Recursively pre-cache braces in the parsed commands
		for _, parsedCmd := range parsedCmds {
			e.preCacheBraceExpressions(parsedCmd, parsedCmd.Command, filename)
		}
	}
}

// ExecuteParsedCommands executes pre-parsed commands with the given state and context
// This is the cached-command equivalent of ExecuteWithState
func (e *Executor) ExecuteParsedCommands(
	commands []*ParsedCommand,
	state *ExecutionState,
	substitutionCtx *SubstitutionContext,
	lineOffset, columnOffset int,
) Result {
	// Ensure state has executor reference for object management
	if state != nil && state.executor == nil {
		state.executor = e
	}

	if len(commands) == 0 {
		return BoolStatus(true)
	}

	// Apply position offsets to all commands (make copies to avoid mutating cached commands)
	if lineOffset > 0 || columnOffset > 0 {
		adjustedCommands := make([]*ParsedCommand, len(commands))
		for i, cmd := range commands {
			// Create a shallow copy with adjusted position
			adjustedCmd := *cmd
			if cmd.Position != nil {
				adjustedPos := *cmd.Position
				adjustedPos.Line += lineOffset
				if adjustedPos.Line == lineOffset+1 {
					adjustedPos.Column += columnOffset
				}
				adjustedCmd.Position = &adjustedPos
			}
			adjustedCommands[i] = &adjustedCmd
		}
		commands = adjustedCommands
	}

	if len(commands) == 1 {
		return e.executeParsedCommand(commands[0], state, substitutionCtx)
	}

	return e.executeCommandSequence(commands, state, substitutionCtx)
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
			ctx := e.createContext(args, nil, nil, state, nil, nil)
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
func (e *Executor) createContext(args []interface{}, rawArgs []string, namedArgs map[string]interface{}, state *ExecutionState, position *SourcePosition, substitutionCtx *SubstitutionContext) *Context {
	var parsedCmd *ParsedCommand
	if substitutionCtx != nil {
		parsedCmd = substitutionCtx.CurrentParsedCommand
	}
	return &Context{
		Args:          args,
		RawArgs:       rawArgs,
		NamedArgs:     namedArgs,
		Position:      position,
		state:         state,
		executor:      e,
		logger:        e.logger,
		ParsedCommand: parsedCmd,
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
