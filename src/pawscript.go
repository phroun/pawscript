package pawscript

import (
	"fmt"
	"time"
)

// PawScript is the main PawScript interpreter
type PawScript struct {
	config      *Config
	logger      *Logger
	executor    *Executor
	macroSystem *MacroSystem
}

// New creates a new PawScript interpreter
func New(config *Config) *PawScript {
	if config == nil {
		config = DefaultConfig()
	}
	
	logger := NewLogger(config.Debug)
	executor := NewExecutor(logger)
	macroSystem := NewMacroSystem(logger)
	
	ps := &PawScript{
		config:      config,
		logger:      logger,
		executor:    executor,
		macroSystem: macroSystem,
	}
	
	// Set up macro fallback handler
	if config.AllowMacros {
		executor.SetFallbackHandler(func(cmdName string, args []interface{}, state *ExecutionState, position *SourcePosition) Result {
			ps.logger.Debug("Fallback handler called for command: %s", cmdName)
			if macroSystem.HasMacro(cmdName) {
				ps.logger.Debug("Found macro: %s", cmdName)
				if state == nil {
					state = NewExecutionState()
				}
				
				// Create a child state so the macro has its own fresh variable scope
				macroState := state.CreateChild()
				
				result := macroSystem.ExecuteMacro(cmdName, func(commands string, macroExecState *ExecutionState, ctx *SubstitutionContext) Result {
					return executor.ExecuteWithState(commands, macroExecState, ctx, "", 0, 0)
				}, args, macroState, position)
				
				return result
			}
			ps.logger.Debug("Macro not found: %s", cmdName)
			return nil
		})
		
		// Register built-in macro commands
		ps.registerBuiltInMacroCommands()
	}
	
	return ps
}

// registerBuiltInMacroCommands registers built-in macro commands
func (ps *PawScript) registerBuiltInMacroCommands() {
	// Define macro command
	ps.executor.RegisterCommand("macro", func(ctx *Context) Result {
		ps.logger.Debug("macro command called with %d args", len(ctx.Args))
		if len(ctx.Args) < 2 {
			ps.logger.Error("Usage: macro <name>, <commands>")
			return BoolStatus(false)
		}
		
		name := fmt.Sprintf("%v", ctx.Args[0])
		commands := fmt.Sprintf("%v", ctx.Args[1])
		
		ps.logger.Debug("Defining macro '%s' with commands: %s", name, commands)
		
		result := ps.macroSystem.DefineMacro(name, commands, ctx.Position)
		if !result {
			ps.logger.Error("Failed to define macro \"%s\"", name)
		}
		
		return BoolStatus(result)
	})
	
	// Call macro command
	ps.executor.RegisterCommand("call", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: call <macro_name>, [args...]")
			return BoolStatus(false)
		}
		
		name := fmt.Sprintf("%v", ctx.Args[0])
		macroArgs := ctx.Args[1:]
		
		// Create a child state so the called macro has its own scope
		// but can access parent variables via get_parent/set_parent
		macroState := ctx.state.CreateChild()
		
		return ps.macroSystem.ExecuteMacro(name, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
			return ps.executor.ExecuteWithState(commands, macroExecState, substCtx, "", 0, 0)
		}, macroArgs, macroState, ctx.Position)
	})
	
	// List macros command
	ps.executor.RegisterCommand("macro_list", func(ctx *Context) Result {
		macros := ps.macroSystem.ListMacros()
		ctx.SetResult(fmt.Sprintf("%v", macros))
		return BoolStatus(true)
	})
	
	// Delete macro command
	ps.executor.RegisterCommand("macro_delete", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: macro_delete <macro_name>")
			return BoolStatus(false)
		}
		
		name := fmt.Sprintf("%v", ctx.Args[0])
		result := ps.macroSystem.DeleteMacro(name)
		
		if !result {
			ps.logger.Error("PawScript macro \"%s\" not found or could not be deleted", name)
		}
		
		return BoolStatus(result)
	})
	
	// Clear all macros command
	ps.executor.RegisterCommand("macro_clear", func(ctx *Context) Result {
		count := ps.macroSystem.ClearMacros()
		ctx.SetResult(fmt.Sprintf("Cleared %d PawScript macros", count))
		return BoolStatus(true)
	})
}

// unregisterBuiltInMacroCommands unregisters built-in macro commands
func (ps *PawScript) unregisterBuiltInMacroCommands() {
	ps.executor.UnregisterCommand("macro")
	ps.executor.UnregisterCommand("call")
	ps.executor.UnregisterCommand("macro_list")
	ps.executor.UnregisterCommand("macro_delete")
	ps.executor.UnregisterCommand("macro_clear")
}

// Configure updates the configuration
func (ps *PawScript) Configure(config *Config) {
	oldAllowMacros := ps.config.AllowMacros
	
	// Update config
	ps.config = config
	ps.logger.SetEnabled(config.Debug)
	
	// Handle macro command registration/unregistration
	if oldAllowMacros != config.AllowMacros {
		if config.AllowMacros && !oldAllowMacros {
			ps.registerBuiltInMacroCommands()
		} else if !config.AllowMacros && oldAllowMacros {
			ps.unregisterBuiltInMacroCommands()
		}
	}
}

// RegisterCommand registers a command handler
func (ps *PawScript) RegisterCommand(name string, handler Handler) {
	ps.executor.RegisterCommand(name, handler)
}

// RegisterCommands registers multiple command handlers
func (ps *PawScript) RegisterCommands(commands map[string]Handler) {
	for name, handler := range commands {
		ps.executor.RegisterCommand(name, handler)
	}
}

// ExecuteFile executes a script file with proper filename tracking
func (ps *PawScript) ExecuteFile(commandString, filename string) Result {
	state := NewExecutionState()
	return ps.executor.ExecuteWithState(commandString, state, nil, filename, 0, 0)
}

// Execute executes a command string
func (ps *PawScript) Execute(commandString string, args ...interface{}) Result {
	return ps.executor.Execute(commandString, args...)
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

// DefineMacro defines a new macro
func (ps *PawScript) DefineMacro(name, commandSequence string) bool {
	if !ps.config.AllowMacros {
		ps.logger.Warn("Macros are disabled in configuration")
		return false
	}
	return ps.macroSystem.DefineMacro(name, commandSequence, nil)
}

// ExecuteMacro executes a macro
func (ps *PawScript) ExecuteMacro(name string) Result {
	if !ps.config.AllowMacros {
		ps.logger.Warn("Macros are disabled in configuration")
		return BoolStatus(false)
	}
	
	state := NewExecutionState()
	
	return ps.macroSystem.ExecuteMacro(name, func(commands string, macroState *ExecutionState, ctx *SubstitutionContext) Result {
		return ps.executor.ExecuteWithState(commands, macroState, ctx, "", 0, 0)
	}, []interface{}{}, state, nil)
}

// ListMacros returns a list of all macro names
func (ps *PawScript) ListMacros() []string {
	return ps.macroSystem.ListMacros()
}

// GetMacro returns the commands for a macro
func (ps *PawScript) GetMacro(name string) *string {
	return ps.macroSystem.GetMacro(name)
}

// DeleteMacro deletes a macro
func (ps *PawScript) DeleteMacro(name string) bool {
	return ps.macroSystem.DeleteMacro(name)
}

// ClearMacros clears all macros
func (ps *PawScript) ClearMacros() int {
	return ps.macroSystem.ClearMacros()
}

// HasMacro checks if a macro exists
func (ps *PawScript) HasMacro(name string) bool {
	return ps.macroSystem.HasMacro(name)
}

// SetFallbackHandler sets a fallback handler for unknown commands
func (ps *PawScript) SetFallbackHandler(handler func(string, []interface{}) Result) {
	ps.executor.SetFallbackHandler(func(cmdName string, args []interface{}, state *ExecutionState, position *SourcePosition) Result {
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
