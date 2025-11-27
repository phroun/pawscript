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

	logger := NewLogger(config.Debug)
	executor := NewExecutor(logger)

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
			ps.logger.Debug("Fallback handler called for command: %s", cmdName)

			if state == nil {
				state = ps.NewExecutionStateFromRoot()
			}

			// Look up macro in module environment (COW - only check MacrosModule)
			var macro *StoredMacro
			state.moduleEnv.mu.RLock()
			if m, exists := state.moduleEnv.MacrosModule[cmdName]; exists && m != nil {
				macro = m
			}
			state.moduleEnv.mu.RUnlock()

			if macro != nil {
				ps.logger.Debug("Found macro: %s", cmdName)

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
			ps.logger.Debug("Macro not found: %s", cmdName)
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

// ExecuteFile executes a script file with proper filename tracking
func (ps *PawScript) ExecuteFile(commandString, filename string) Result {
	fmt.Fprintf(os.Stderr, "[TRACE] ExecuteFile START: filename='%s'\n", filename)
	state := ps.NewExecutionStateFromRoot()
	fmt.Fprintf(os.Stderr, "[TRACE] ExecuteFile('%s'): state.moduleEnv=%p, ModuleExports=%p\n", filename, state.moduleEnv, state.moduleEnv.ModuleExports)
	result := ps.executor.ExecuteWithState(commandString, state, nil, filename, 0, 0)

	// Debug: log what's in ModuleExports before merge
	state.moduleEnv.mu.RLock()
	fmt.Fprintf(os.Stderr, "[TRACE] ExecuteFile('%s'): AFTER execution, moduleEnv=%p, ModuleExports=%p\n", filename, state.moduleEnv, state.moduleEnv.ModuleExports)
	numExports := len(state.moduleEnv.ModuleExports)
	fmt.Fprintf(os.Stderr, "[TRACE] ExecuteFile('%s'): ModuleExports has %d modules\n", filename, numExports)
	for modName, section := range state.moduleEnv.ModuleExports {
		fmt.Fprintf(os.Stderr, "[TRACE] ExecuteFile('%s'): ModuleExports['%s'] has %d items\n", filename, modName, len(section))
		for itemName, item := range section {
			fmt.Fprintf(os.Stderr, "[TRACE] ExecuteFile('%s'):   - %s (type: %s)\n", filename, itemName, item.Type)
		}
	}
	state.moduleEnv.mu.RUnlock()

	// Merge any module exports into the root environment for persistence
	state.moduleEnv.MergeExportsInto(ps.rootModuleEnv)

	// Debug: verify merge worked
	ps.rootModuleEnv.mu.RLock()
	fmt.Fprintf(os.Stderr, "[TRACE] ExecuteFile('%s'): After merge, LibraryRestricted has %d modules\n", filename, len(ps.rootModuleEnv.LibraryRestricted))
	ps.rootModuleEnv.mu.RUnlock()

	fmt.Fprintf(os.Stderr, "[TRACE] ExecuteFile END: filename='%s'\n", filename)
	return result
}

// Execute executes a command string
func (ps *PawScript) Execute(commandString string, args ...interface{}) Result {
	state := ps.NewExecutionStateFromRoot()
	defer state.ReleaseAllReferences()
	result := ps.executor.ExecuteWithState(commandString, state, nil, "", 0, 0)

	// Merge any module exports into the root environment for persistence
	state.moduleEnv.MergeExportsInto(ps.rootModuleEnv)

	return result
}

// ImportModuleToRoot imports all items from a module directly into the root environment.
// This makes the items available to all subsequent Execute() calls without needing IMPORT.
func (ps *PawScript) ImportModuleToRoot(moduleName string) bool {
	ps.rootModuleEnv.mu.Lock()
	defer ps.rootModuleEnv.mu.Unlock()

	// Find module in LibraryRestricted
	section, exists := ps.rootModuleEnv.LibraryRestricted[moduleName]
	if !exists {
		ps.logger.Error("ImportModuleToRoot: Module not found in library: %s", moduleName)
		return false
	}

	// Import all items from the module
	for itemName, item := range section {
		switch item.Type {
		case "macro":
			if macro, ok := item.Value.(*StoredMacro); ok && macro != nil {
				ps.rootModuleEnv.MacrosModule[itemName] = macro
				ps.logger.Debug("ImportModuleToRoot: Imported macro %s from %s", itemName, moduleName)
			}
		case "command":
			if handler, ok := item.Value.(Handler); ok && handler != nil {
				ps.rootModuleEnv.CommandRegistryModule[itemName] = handler
				ps.logger.Debug("ImportModuleToRoot: Imported command %s from %s", itemName, moduleName)
			}
		case "object":
			ps.rootModuleEnv.ObjectsModule[itemName] = item.Value
			ps.logger.Debug("ImportModuleToRoot: Imported object %s from %s", itemName, moduleName)
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
	defer state.ReleaseAllReferences()
	return ps.executor.ExecuteWithState(commandString, state, nil, filename, lineOffset, columnOffset)
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
		ps.logger.Warn("Macros are disabled in configuration")
		return false
	}
	if name == "" || commandSequence == "" {
		ps.logger.Error("Macro name and commands are required")
		return false
	}

	// Create macro and store in root module environment's MacrosModule
	macro := NewStoredMacro(commandSequence, nil)
	ps.rootModuleEnv.mu.Lock()
	ps.rootModuleEnv.MacrosModule[name] = &macro
	ps.rootModuleEnv.mu.Unlock()

	ps.logger.Debug("Defined macro \"%s\" in root environment", name)
	return true
}

// ExecuteMacro executes a macro by name from the root module environment
func (ps *PawScript) ExecuteMacro(name string) Result {
	if !ps.config.AllowMacros {
		ps.logger.Warn("Macros are disabled in configuration")
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
		ps.logger.Error("Macro \"%s\" not found", name)
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
		ps.logger.Error("Macro \"%s\" not found", name)
		return false
	}
	delete(ps.rootModuleEnv.MacrosModule, name)
	ps.logger.Debug("Deleted macro \"%s\"", name)
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

	ps.logger.Debug("Cleared %d macros", count)
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
