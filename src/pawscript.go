package pawscript

import (
	"fmt"
	"os"
	"sort"
	"time"
)

// PawScript is the main PawScript interpreter
type PawScript struct {
	config        *Config
	logger        *Logger
	executor      *Executor
	rootModuleEnv *ModuleEnvironment // Root module environment for all execution states
	startTime     time.Time          // Time when interpreter was initialized
	terminalState *TerminalState     // Terminal/cursor state for io commands
}

// New creates a new PawScript interpreter
func New(config *Config) *PawScript {
	if config == nil {
		config = DefaultConfig()
	}

	// Ensure I/O streams are set
	if config.Stdin == nil {
		config.Stdin = os.Stdin
	}
	if config.Stdout == nil {
		config.Stdout = os.Stdout
	}
	if config.Stderr == nil {
		config.Stderr = os.Stderr
	}

	logger := NewLoggerWithWriters(config.Debug, config.Stdout, config.Stderr)
	executor := NewExecutor(logger)

	// Set optimization level from config
	executor.SetOptimizationLevel(config.OptLevel)

	// Create root module environment for all execution states
	rootModuleEnv := NewModuleEnvironment()

	ps := &PawScript{
		config:        config,
		logger:        logger,
		executor:      executor,
		rootModuleEnv: rootModuleEnv,
		startTime:     time.Now(),
		terminalState: NewTerminalState(),
	}

	// Set up macro fallback handler
	if config.AllowMacros {
		executor.SetFallbackHandler(func(cmdName string, args []interface{}, namedArgs map[string]interface{}, state *ExecutionState, position *SourcePosition) Result {
			ps.logger.DebugCat(CatCommand, "Fallback handler called for command: %s", cmdName)

			if state == nil {
				state = ps.NewExecutionStateFromRoot()
			}

			// Look up macro in module environment (COW - only check MacrosModule)
			var macro *StoredMacro
			if state.moduleEnv != nil {
				state.moduleEnv.mu.RLock()
				if m, exists := state.moduleEnv.MacrosModule[cmdName]; exists && m != nil {
					macro = m
				}
				state.moduleEnv.mu.RUnlock()
			}

			if macro != nil {
				ps.logger.DebugCat(CatMacro, "Found macro: %s", cmdName)

				// Ensure state has executor reference before creating child
				if state.executor == nil {
					state.executor = executor
				}

				// Create a child state so the macro has its own fresh variable scope
				macroState := state.CreateChild()

				result := executor.ExecuteStoredMacro(macro, func(commands string, macroExecState *ExecutionState, ctx *SubstitutionContext) Result {
					// Use filename and offsets from substitution context for proper error reporting
					filename := ""
					lineOffset := 0
					columnOffset := 0
					if ctx != nil {
						filename = ctx.Filename
						lineOffset = ctx.CurrentLineOffset
						columnOffset = ctx.CurrentColumnOffset
					}
					return executor.ExecuteWithState(commands, macroExecState, ctx, filename, lineOffset, columnOffset)
				}, args, namedArgs, macroState, position, state) // Pass parent state

				return result
			}
			ps.logger.DebugCat(CatMacro, "Macro not found: %s", cmdName)
			return nil
		})
	}

	return ps
}

// Configure updates the configuration
func (ps *PawScript) Configure(config *Config) {
	// Update config
	ps.config = config
	ps.logger.SetEnabled(config.Debug)
}

// RegisterCommand registers a command handler (legacy - adds to CommandRegistryInherited directly)
func (ps *PawScript) RegisterCommand(name string, handler Handler) {
	ps.executor.RegisterCommand(name, handler)
	// Also register to root module environment
	ps.rootModuleEnv.CommandRegistryInherited[name] = handler
	ps.rootModuleEnv.RegistryGeneration++ // Invalidate handler caches
}

// RegisterCommandInModule registers a command handler in a specific module within LibraryInherited
func (ps *PawScript) RegisterCommandInModule(moduleName, cmdName string, handler Handler) {
	ps.rootModuleEnv.mu.Lock()
	defer ps.rootModuleEnv.mu.Unlock()

	// Ensure module section exists
	if ps.rootModuleEnv.LibraryInherited[moduleName] == nil {
		ps.rootModuleEnv.LibraryInherited[moduleName] = make(ModuleSection)
	}

	// Add command to the module
	ps.rootModuleEnv.LibraryInherited[moduleName][cmdName] = &ModuleItem{
		Type:  "command",
		Value: handler,
	}
}

// RegisterObjectInModule registers an object (like #stdin) in a specific module within LibraryInherited
func (ps *PawScript) RegisterObjectInModule(moduleName, objName string, value interface{}) {
	ps.rootModuleEnv.mu.Lock()
	defer ps.rootModuleEnv.mu.Unlock()

	// Ensure module section exists
	if ps.rootModuleEnv.LibraryInherited[moduleName] == nil {
		ps.rootModuleEnv.LibraryInherited[moduleName] = make(ModuleSection)
	}

	// Add object to the module
	ps.rootModuleEnv.LibraryInherited[moduleName][objName] = &ModuleItem{
		Type:  "object",
		Value: value,
	}
}

// RegisterCommands registers multiple command handlers
func (ps *PawScript) RegisterCommands(commands map[string]Handler) {
	for name, handler := range commands {
		ps.executor.RegisterCommand(name, handler)
	}
}

// NewExecutionStateFromRoot creates an execution state that inherits from root module environment
func (ps *PawScript) NewExecutionStateFromRoot() *ExecutionState {
	state := NewExecutionState()
	// Replace the module environment with one that inherits from root
	state.moduleEnv = NewChildModuleEnvironment(ps.rootModuleEnv)
	return state
}

// dumpRemainingBubbles dumps any remaining bubbles to stderr before returning control to host.
// This includes orphaned bubbles (from abandoned fibers) and bubbles in the execution state.
func (ps *PawScript) dumpRemainingBubbles(state *ExecutionState) {
	// Collect all bubbles: orphaned + state's bubbleMap
	orphaned := ps.executor.GetOrphanedBubbles()

	state.mu.Lock()
	stateBubbles := state.bubbleMap
	state.mu.Unlock()

	hasOrphaned := len(orphaned) > 0
	hasStateBubbles := len(stateBubbles) > 0

	if !hasOrphaned && !hasStateBubbles {
		return
	}

	stderr := ps.config.Stderr

	// Helper to dump a bubble map
	dumpBubbleMap := func(label string, bubbleMap map[string][]*BubbleEntry) {
		if len(bubbleMap) == 0 {
			return
		}
		fmt.Fprintf(stderr, "[%s]\n", label)
		for flavor, entries := range bubbleMap {
			fmt.Fprintf(stderr, "  Flavor: %s (%d entries)\n", flavor, len(entries))
			for i, entry := range entries {
				fmt.Fprintf(stderr, "    [%d] content=%v, microtime=%d, memo=%q\n",
					i, entry.Content, entry.Microtime, entry.Memo)
				if len(entry.StackTrace) > 0 {
					fmt.Fprintf(stderr, "        stack trace (%d frames):\n", len(entry.StackTrace))
					for j, frame := range entry.StackTrace {
						if frameMap, ok := frame.(map[string]interface{}); ok {
							fmt.Fprintf(stderr, "          [%d] %v at %v:%v\n",
								j, frameMap["macro"], frameMap["file"], frameMap["line"])
						}
					}
				}
			}
		}
	}

	if hasOrphaned {
		dumpBubbleMap("Orphaned bubbles", orphaned)
	}
	if hasStateBubbles {
		dumpBubbleMap("Remaining bubbles", stateBubbles)
	}
}

// ExecuteFile executes a script file with proper filename tracking.
// If the script contains async operations (like msleep), this function waits
// for the entire script to complete before returning and merging exports.
func (ps *PawScript) ExecuteFile(commandString, filename string) Result {
	state := ps.NewExecutionStateFromRoot()
	result := ps.executor.ExecuteWithState(commandString, state, nil, filename, 0, 0)

	// If the result is an async token, we need to wait for the script to complete
	// before we can merge exports (MODULE/EXPORT may run after async operations)
	if tokenResult, ok := result.(TokenResult); ok {
		tokenID := string(tokenResult)
		ps.logger.DebugCat(CatAsync, "ExecuteFile('%s'): Script returned async token %s, waiting for completion", filename, tokenID)

		// Create a channel to wait for completion
		waitChan := make(chan ResumeData, 1)
		ps.executor.attachWaitChan(tokenID, waitChan)

		// Wait for the script to complete
		resumeData := <-waitChan
		ps.logger.DebugCat(CatAsync, "ExecuteFile('%s'): Script completed with status %v", filename, resumeData.Status)

		// Update result based on completion
		if resumeData.Status {
			result = BoolStatus(true)
		} else {
			result = BoolStatus(false)
		}
	}

	// Debug: log what's in ModuleExports before merge
	state.moduleEnv.mu.RLock()
	numExports := len(state.moduleEnv.ModuleExports)
	ps.logger.DebugCat(CatSystem, "ExecuteFile('%s'): ModuleExports has %d modules", filename, numExports)
	for modName, section := range state.moduleEnv.ModuleExports {
		ps.logger.DebugCat(CatSystem, "ExecuteFile('%s'): ModuleExports['%s'] has %d items", filename, modName, len(section))
	}
	state.moduleEnv.mu.RUnlock()

	// Merge any module exports into the root environment for persistence
	state.moduleEnv.MergeExportsInto(ps.rootModuleEnv)

	// Dump any remaining bubbles to stderr before returning control to host
	ps.dumpRemainingBubbles(state)

	return result
}

// Execute executes a command string
func (ps *PawScript) Execute(commandString string, args ...interface{}) Result {
	state := ps.NewExecutionStateFromRoot()
	result := ps.executor.ExecuteWithState(commandString, state, nil, "", 0, 0)

	// Merge any module exports into the root environment for persistence
	state.moduleEnv.MergeExportsInto(ps.rootModuleEnv)

	// Dump any remaining bubbles to stderr before returning control to host
	ps.dumpRemainingBubbles(state)

	// Only release state if not returning a token (async operation)
	// The token system will release the state when the async operation completes
	if _, isToken := result.(TokenResult); !isToken {
		state.ReleaseAllReferences()
	}

	return result
}

// HasLibraryModule checks if a module exists in the library.
// Use this to check before calling ImportModuleToRoot to avoid error logging.
func (ps *PawScript) HasLibraryModule(moduleName string) bool {
	ps.rootModuleEnv.mu.RLock()
	defer ps.rootModuleEnv.mu.RUnlock()
	_, exists := ps.rootModuleEnv.LibraryRestricted[moduleName]
	return exists
}

// SetInheritedObject sets or overrides an object in the ObjectsInherited map.
// This is useful for overriding default channels like #out, #in, #err at runtime.
// The moduleName parameter is currently ignored - objects are set at the root level.
func (ps *PawScript) SetInheritedObject(moduleName, objectName string, value interface{}) {
	ps.rootModuleEnv.mu.Lock()
	defer ps.rootModuleEnv.mu.Unlock()
	if ps.rootModuleEnv.ObjectsInherited == nil {
		ps.rootModuleEnv.ObjectsInherited = make(map[string]interface{})
	}
	ps.rootModuleEnv.ObjectsInherited[objectName] = value
}

// GetFiberCount returns the number of currently active fibers.
// Returns 0 if no fibers are running.
func (ps *PawScript) GetFiberCount() int {
	if ps.executor == nil {
		return 0
	}
	return ps.executor.GetFiberCount()
}

// ImportModuleToRoot imports all items from a module directly into the root environment.
// This makes the items available to all subsequent Execute() calls without needing IMPORT.
func (ps *PawScript) ImportModuleToRoot(moduleName string) bool {
	ps.rootModuleEnv.mu.Lock()
	defer ps.rootModuleEnv.mu.Unlock()

	// Find module in LibraryRestricted
	section, exists := ps.rootModuleEnv.LibraryRestricted[moduleName]
	if !exists {
		ps.logger.ErrorCat(CatSystem, "ImportModuleToRoot: Module not found in library: %s", moduleName)
		return false
	}

	// Import all items from the module
	for itemName, item := range section {
		switch item.Type {
		case "macro":
			if macro, ok := item.Value.(*StoredMacro); ok && macro != nil {
				ps.rootModuleEnv.MacrosModule[itemName] = macro
				ps.rootModuleEnv.RegistryGeneration++ // Invalidate handler caches
				ps.logger.DebugCat(CatSystem, "ImportModuleToRoot: Imported macro %s from %s", itemName, moduleName)
			}
		case "command":
			if handler, ok := item.Value.(Handler); ok && handler != nil {
				ps.rootModuleEnv.CommandRegistryModule[itemName] = handler
				ps.rootModuleEnv.RegistryGeneration++ // Invalidate handler caches
				ps.logger.DebugCat(CatSystem, "ImportModuleToRoot: Imported command %s from %s", itemName, moduleName)
			}
		case "object":
			ps.rootModuleEnv.ObjectsModule[itemName] = item.Value
			ps.logger.DebugCat(CatSystem, "ImportModuleToRoot: Imported object %s from %s", itemName, moduleName)
		}
	}

	return true
}

// ExecuteInRoot executes a command string directly in the root environment.
// Any IMPORT, macro definitions, or other changes persist directly to root.
// Use this when you need changes to be visible to all subsequent Execute() calls.
func (ps *PawScript) ExecuteInRoot(commandString string) Result {
	return ps.ExecuteWithEnvironment(commandString, ps.rootModuleEnv, "", 0, 0)
}

// CreateRestrictedSnapshot creates a restricted environment snapshot
// This captures the current state with copy-on-write isolation, similar to what
// a macro definition would capture. Use with ExecuteWithEnvironment to run
// scripts in a restricted/isolated environment.
func (ps *PawScript) CreateRestrictedSnapshot() *ModuleEnvironment {
	return NewMacroModuleEnvironment(ps.rootModuleEnv)
}

// ExecuteWithEnvironment executes a command string using a specific module environment
// This allows running scripts in a restricted/isolated environment created by
// CreateRestrictedSnapshot. Exports from this execution are NOT merged into root.
// Optional source location parameters help track the origin of the code for error messages.
func (ps *PawScript) ExecuteWithEnvironment(commandString string, env *ModuleEnvironment, filename string, lineOffset, columnOffset int) Result {
	state := NewExecutionState()
	state.moduleEnv = env
	result := ps.executor.ExecuteWithState(commandString, state, nil, filename, lineOffset, columnOffset)

	// Dump any remaining bubbles to stderr before returning control to host
	ps.dumpRemainingBubbles(state)

	// Only release state if not returning a token (async operation)
	// The token system will release the state when the async operation completes
	if _, isToken := result.(TokenResult); !isToken {
		state.ReleaseAllReferences()
	}

	return result
}

// RequestToken requests an async completion token
func (ps *PawScript) RequestToken(cleanupCallback func(string), parentToken string, timeout time.Duration) string {
	if timeout == 0 {
		timeout = ps.config.DefaultTokenTimeout
	}
	return ps.executor.RequestCompletionToken(cleanupCallback, parentToken, timeout, nil, nil)
}

// ResumeToken resumes execution with a token
func (ps *PawScript) ResumeToken(tokenID string, status bool) bool {
	return ps.executor.PopAndResumeCommandSequence(tokenID, status)
}

// GetTokenStatus returns information about active tokens
func (ps *PawScript) GetTokenStatus() map[string]interface{} {
	return ps.executor.GetTokenStatus()
}

// ForceCleanupToken forces cleanup of a token
func (ps *PawScript) ForceCleanupToken(tokenID string) {
	ps.executor.ForceCleanupToken(tokenID)
}

// DefineMacro defines a new macro in the root module environment
func (ps *PawScript) DefineMacro(name, commandSequence string) bool {
	if !ps.config.AllowMacros {
		ps.logger.WarnCat(CatMacro, "Macros are disabled in configuration")
		return false
	}
	if name == "" || commandSequence == "" {
		ps.logger.ErrorCat(CatMacro, "Macro name and commands are required")
		return false
	}

	// Create macro and store in root module environment's MacrosModule
	macro := NewStoredMacro(commandSequence, nil)
	ps.rootModuleEnv.mu.Lock()
	ps.rootModuleEnv.MacrosModule[name] = &macro
	ps.rootModuleEnv.RegistryGeneration++ // Invalidate handler caches
	ps.rootModuleEnv.mu.Unlock()

	ps.logger.DebugCat(CatMacro, "Defined macro \"%s\" in root environment", name)
	return true
}

// ExecuteMacro executes a macro by name from the root module environment
func (ps *PawScript) ExecuteMacro(name string) Result {
	if !ps.config.AllowMacros {
		ps.logger.WarnCat(CatMacro, "Macros are disabled in configuration")
		return BoolStatus(false)
	}

	state := ps.NewExecutionStateFromRoot()

	// Look up macro in root module environment (COW - only check MacrosModule)
	var macro *StoredMacro
	ps.rootModuleEnv.mu.RLock()
	if m, exists := ps.rootModuleEnv.MacrosModule[name]; exists && m != nil {
		macro = m
	}
	ps.rootModuleEnv.mu.RUnlock()

	if macro == nil {
		ps.logger.ErrorCat(CatMacro, "Macro \"%s\" not found", name)
		return BoolStatus(false)
	}

	return ps.executor.ExecuteStoredMacro(macro, func(commands string, macroState *ExecutionState, ctx *SubstitutionContext) Result {
		// Use filename from substitution context for proper error reporting
		filename := ""
		if ctx != nil {
			filename = ctx.Filename
		}
		return ps.executor.ExecuteWithState(commands, macroState, ctx, filename, 0, 0)
	}, []interface{}{}, nil, state, nil, nil) // No parent for top-level call
}

// ListMacros returns a list of all macro names from the root module environment
func (ps *PawScript) ListMacros() []string {
	ps.rootModuleEnv.mu.RLock()
	macros := make([]string, 0, len(ps.rootModuleEnv.MacrosModule))
	for name, macro := range ps.rootModuleEnv.MacrosModule {
		if macro != nil {
			macros = append(macros, name)
		}
	}
	ps.rootModuleEnv.mu.RUnlock()

	sort.Strings(macros)
	return macros
}

// GetMacro returns the commands for a macro from the root module environment
func (ps *PawScript) GetMacro(name string) *string {
	ps.rootModuleEnv.mu.RLock()
	defer ps.rootModuleEnv.mu.RUnlock()

	if macro, exists := ps.rootModuleEnv.MacrosModule[name]; exists && macro != nil {
		return &macro.Commands
	}
	return nil
}

// DeleteMacro deletes a macro from the root module environment
func (ps *PawScript) DeleteMacro(name string) bool {
	ps.rootModuleEnv.mu.Lock()
	defer ps.rootModuleEnv.mu.Unlock()

	macro, exists := ps.rootModuleEnv.MacrosModule[name]
	if !exists || macro == nil {
		ps.logger.ErrorCat(CatMacro, "Macro \"%s\" not found", name)
		return false
	}
	delete(ps.rootModuleEnv.MacrosModule, name)
	ps.logger.DebugCat(CatMacro, "Deleted macro \"%s\"", name)
	return true
}

// ClearMacros clears all macros from the root module environment
func (ps *PawScript) ClearMacros() int {
	ps.rootModuleEnv.mu.Lock()
	defer ps.rootModuleEnv.mu.Unlock()

	count := 0
	for _, macro := range ps.rootModuleEnv.MacrosModule {
		if macro != nil {
			count++
		}
	}
	ps.rootModuleEnv.MacrosModule = make(map[string]*StoredMacro)

	ps.logger.DebugCat(CatMacro, "Cleared %d macros", count)
	return count
}

// HasMacro checks if a macro exists in the root module environment
func (ps *PawScript) HasMacro(name string) bool {
	ps.rootModuleEnv.mu.RLock()
	defer ps.rootModuleEnv.mu.RUnlock()

	macro, exists := ps.rootModuleEnv.MacrosModule[name]
	return exists && macro != nil
}

// SetFallbackHandler sets a fallback handler for unknown commands
func (ps *PawScript) SetFallbackHandler(handler func(string, []interface{}) Result) {
	ps.executor.SetFallbackHandler(func(cmdName string, args []interface{}, namedArgs map[string]interface{}, state *ExecutionState, position *SourcePosition) Result {
		return handler(cmdName, args)
	})
}

// GetConfig returns the current configuration
func (ps *PawScript) GetConfig() *Config {
	configCopy := *ps.config
	return &configCopy
}

// SetErrorContextEnabled enables or disables error context reporting
func (ps *PawScript) SetErrorContextEnabled(enabled bool) {
	ps.config.ShowErrorContext = enabled
}

// SetContextLines sets the number of context lines for error reporting
func (ps *PawScript) SetContextLines(lines int) {
	if lines < 0 {
		lines = 0
	}
	if lines > 10 {
		lines = 10
	}
	ps.config.ContextLines = lines
}
