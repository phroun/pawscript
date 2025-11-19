package pawscript

import (
	"fmt"
	"sync"
	"time"
)

// MacroSystem manages macro definitions and execution
type MacroSystem struct {
	mu     sync.RWMutex
	macros map[string]*MacroDefinition
	logger *Logger
}

// NewMacroSystem creates a new macro system
func NewMacroSystem(logger *Logger) *MacroSystem {
	return &MacroSystem{
		macros: make(map[string]*MacroDefinition),
		logger: logger,
	}
}

// DefineMacro defines a new macro
func (ms *MacroSystem) DefineMacro(name, commands string, position *SourcePosition) bool {
	if name == "" || commands == "" {
		ms.logger.Error("Macro name and commands are required")
		return false
	}

	filename := "<unknown>"
	line := 1
	column := 1

	if position != nil {
		if position.Filename != "" {
			filename = position.Filename
		}
		line = position.Line
		column = position.Column
	}

	macro := &MacroDefinition{
		Name:             name,
		Commands:         commands,
		DefinitionFile:   filename,
		DefinitionLine:   line,
		DefinitionColumn: column,
		Timestamp:        time.Now(),
	}

	ms.mu.Lock()
	ms.macros[name] = macro
	ms.mu.Unlock()

	ms.logger.Debug("Defined macro \"%s\" at %s:%d", name, macro.DefinitionFile, macro.DefinitionLine)
	return true
}

// ExecuteMacro executes a macro with the given arguments
func (ms *MacroSystem) ExecuteMacro(
	name string,
	executeCallback func(commands string, state *ExecutionState, ctx *SubstitutionContext) Result,
	args []interface{},
	state *ExecutionState,
	invocationPosition *SourcePosition,
) Result {
	if name == "" {
		ms.logger.Error("Macro name is required")
		return BoolStatus(false)
	}

	ms.mu.RLock()
	macroDef, exists := ms.macros[name]
	ms.mu.RUnlock()

	if !exists {
		ms.logger.Error("Macro \"%s\" not found", name)
		return BoolStatus(false)
	}

	// Create macro context for error tracking
	macroContext := &MacroContext{
		MacroName:        name,
		DefinitionFile:   macroDef.DefinitionFile,
		DefinitionLine:   macroDef.DefinitionLine,
		DefinitionColumn: macroDef.DefinitionColumn,
	}

	if invocationPosition != nil {
		macroContext.InvocationFile = invocationPosition.Filename
		macroContext.InvocationLine = invocationPosition.Line
		macroContext.InvocationColumn = invocationPosition.Column
		macroContext.ParentMacro = invocationPosition.MacroContext
	}

	debugInfo := fmt.Sprintf("Executing macro \"%s\" defined at %s:%d",
		name, macroDef.DefinitionFile, macroDef.DefinitionLine)
	if invocationPosition != nil {
		debugInfo += fmt.Sprintf(", called from %s:%d",
			invocationPosition.Filename, invocationPosition.Line)
	}
	ms.logger.Debug(debugInfo)

	// Create execution state if not provided
	if state == nil {
		state = NewExecutionState()
	}

	// Create substitution context for macro arguments
	substitutionContext := &SubstitutionContext{
		Args:           args,
		ExecutionState: state,
		MacroContext:   macroContext,
	}

	// Execute the macro commands
	result := executeCallback(macroDef.Commands, state, substitutionContext)

	ms.logger.Debug("Macro \"%s\" execution completed with result: %v", name, result)
	return result
}

// ListMacros returns a list of all macro names
func (ms *MacroSystem) ListMacros() []string {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	names := make([]string, 0, len(ms.macros))
	for name := range ms.macros {
		names = append(names, name)
	}
	return names
}

// GetMacro returns the commands for a macro
func (ms *MacroSystem) GetMacro(name string) *string {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	if macro, exists := ms.macros[name]; exists {
		return &macro.Commands
	}
	return nil
}

// GetMacroDefinition returns the full macro definition
func (ms *MacroSystem) GetMacroDefinition(name string) *MacroDefinition {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	if macro, exists := ms.macros[name]; exists {
		// Return a copy to prevent external modification
		macroCopy := *macro
		return &macroCopy
	}
	return nil
}

// DeleteMacro deletes a macro
func (ms *MacroSystem) DeleteMacro(name string) bool {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	if _, exists := ms.macros[name]; !exists {
		ms.logger.Error("Macro \"%s\" not found", name)
		return false
	}

	delete(ms.macros, name)
	ms.logger.Debug("Deleted macro \"%s\"", name)
	return true
}

// ClearMacros clears all macros
func (ms *MacroSystem) ClearMacros() int {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	count := len(ms.macros)
	ms.macros = make(map[string]*MacroDefinition)
	ms.logger.Debug("Cleared %d macros", count)
	return count
}

// HasMacro checks if a macro exists
func (ms *MacroSystem) HasMacro(name string) bool {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	_, exists := ms.macros[name]
	return exists
}
