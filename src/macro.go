package pawscript

import (
	"fmt"
	"sync"
)

// MacroSystem manages macro execution
// Note: Macro storage/lookup is now handled by ModuleEnvironment.MacrosModule
// and MacrosInherited. This system only handles execution of StoredMacro objects.
type MacroSystem struct {
	mu       sync.RWMutex
	executor *Executor // Reference to executor for object storage
	logger   *Logger
}

// NewMacroSystem creates a new macro system
func NewMacroSystem(logger *Logger) *MacroSystem {
	return &MacroSystem{
		logger: logger,
	}
}

// SetExecutor sets the executor reference for object storage
func (ms *MacroSystem) SetExecutor(executor *Executor) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.executor = executor
}

// ExecuteStoredMacro executes a StoredMacro object directly
func (ms *MacroSystem) ExecuteStoredMacro(
	macro *StoredMacro,
	executeCallback func(commands string, state *ExecutionState, ctx *SubstitutionContext) Result,
	args []interface{},
	namedArgs map[string]interface{},
	state *ExecutionState,
	invocationPosition *SourcePosition,
	parentState *ExecutionState,
) Result {
	return ms.executeStoredMacro("", macro, executeCallback, args, namedArgs, state, invocationPosition, parentState)
}

// executeStoredMacro is the internal implementation for executing a StoredMacro
func (ms *MacroSystem) executeStoredMacro(
	name string,
	macro *StoredMacro,
	executeCallback func(commands string, state *ExecutionState, ctx *SubstitutionContext) Result,
	args []interface{},
	namedArgs map[string]interface{},
	state *ExecutionState,
	invocationPosition *SourcePosition,
	parentState *ExecutionState,
) Result {
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
		macroContext.ParentMacro = invocationPosition.MacroContext
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
	ms.logger.Debug("%s", debugInfo)

	// Create execution state if not provided
	if state == nil {
		state = NewExecutionState()
	}

	// Ensure state has executor reference
	if state.executor == nil && ms.executor != nil {
		state.executor = ms.executor
	}

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
			ms.logger.Debug("Merged %d exports from macro to parent's exports module", len(exportsSection))
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

		ms.logger.Debug("Transferred macro result to parent state: %v", state.GetResult())
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
		ms.logger.Debug("Macro \"%s\" execution completed with result: %v", name, result)
	} else {
		ms.logger.Debug("Anonymous macro execution completed with result: %v", result)
	}
	return result
}
