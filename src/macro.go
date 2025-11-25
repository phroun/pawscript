package pawscript

import (
	"fmt"
	"sync"
	"sort"
)

// MacroSystem manages macro definitions and execution
type MacroSystem struct {
	mu       sync.RWMutex
	macros   map[string]int // Maps macro name to StoredMacro object ID
	executor *Executor      // Reference to executor for object storage
	logger   *Logger
}

// NewMacroSystem creates a new macro system
func NewMacroSystem(logger *Logger) *MacroSystem {
	return &MacroSystem{
		macros: make(map[string]int),
		logger: logger,
	}
}

// SetExecutor sets the executor reference for object storage
func (ms *MacroSystem) SetExecutor(executor *Executor) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.executor = executor
}

// DefineMacro defines a new macro and returns its object ID
// If moduleEnv is provided, the macro captures that environment for lexical scoping
func (ms *MacroSystem) DefineMacro(name, commands string, position *SourcePosition, moduleEnv *ModuleEnvironment) (int, bool) {
	if name == "" || commands == "" {
		ms.logger.Error("Macro name and commands are required")
		return -1, false
	}

	if ms.executor == nil {
		ms.logger.Error("MacroSystem executor not set")
		return -1, false
	}

	// Create the StoredMacro object with captured module environment
	var macro StoredMacro
	if moduleEnv != nil {
		macro = NewStoredMacroWithEnv(commands, position, moduleEnv)
	} else {
		macro = NewStoredMacro(commands, position)
	}

	// Store it in the executor's object store
	objectID := ms.executor.storeObject(macro, "macro")

	ms.mu.Lock()

	// If a macro with this name already exists, release its reference
	if oldID, exists := ms.macros[name]; exists {
		ms.logger.Debug("Replacing existing macro \"%s\" (object %d)", name, oldID)
		ms.mu.Unlock()
		ms.executor.decrementObjectRefCount(oldID)
		ms.mu.Lock()
	}

	// Store the new macro's object ID and claim a reference
	ms.macros[name] = objectID
	ms.mu.Unlock()

	// Increment reference count since the registry now holds a reference
	ms.executor.incrementObjectRefCount(objectID)

	ms.logger.Debug("Defined macro \"%s\" at %s:%d (object %d)", name, macro.DefinitionFile, macro.DefinitionLine, objectID)
	return objectID, true
}

// ExecuteMacro executes a macro by name with the given arguments
func (ms *MacroSystem) ExecuteMacro(
	name string,
	executeCallback func(commands string, state *ExecutionState, ctx *SubstitutionContext) Result,
	args []interface{},
	namedArgs map[string]interface{},
	state *ExecutionState,
	invocationPosition *SourcePosition,
	parentState *ExecutionState,
) Result {
	if name == "" {
		ms.logger.Error("Macro name is required")
		return BoolStatus(false)
	}

	ms.mu.RLock()
	objectID, exists := ms.macros[name]
	ms.mu.RUnlock()

	if !exists {
		ms.logger.Error("Macro \"%s\" not found", name)
		return BoolStatus(false)
	}

	if ms.executor == nil {
		ms.logger.Error("MacroSystem executor not set")
		return BoolStatus(false)
	}

	// Retrieve the stored macro
	obj, exists := ms.executor.getObject(objectID)
	if !exists {
		ms.logger.Error("Macro object %d not found in storage", objectID)
		return BoolStatus(false)
	}

	macro, ok := obj.(StoredMacro)
	if !ok {
		ms.logger.Error("Object %d is not a StoredMacro", objectID)
		return BoolStatus(false)
	}

	return ms.executeStoredMacro(name, &macro, executeCallback, args, namedArgs, state, invocationPosition, parentState)
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

// ListMacros returns a list of all macro names
func (ms *MacroSystem) ListMacros() []string {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	names := make([]string, 0, len(ms.macros))
	for name := range ms.macros {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GetMacro returns the commands for a macro
func (ms *MacroSystem) GetMacro(name string) *string {
	ms.mu.RLock()
	objectID, exists := ms.macros[name]
	ms.mu.RUnlock()

	if !exists || ms.executor == nil {
		return nil
	}

	obj, exists := ms.executor.getObject(objectID)
	if !exists {
		return nil
	}

	if macro, ok := obj.(StoredMacro); ok {
		return &macro.Commands
	}
	return nil
}

// GetMacroDefinition returns the full macro definition as a MacroDefinition for compatibility
func (ms *MacroSystem) GetMacroDefinition(name string) *MacroDefinition {
	ms.mu.RLock()
	objectID, exists := ms.macros[name]
	ms.mu.RUnlock()

	if !exists || ms.executor == nil {
		return nil
	}

	obj, exists := ms.executor.getObject(objectID)
	if !exists {
		return nil
	}

	if macro, ok := obj.(StoredMacro); ok {
		// Return a MacroDefinition for backward compatibility
		return &MacroDefinition{
			Name:             name,
			Commands:         macro.Commands,
			DefinitionFile:   macro.DefinitionFile,
			DefinitionLine:   macro.DefinitionLine,
			DefinitionColumn: macro.DefinitionColumn,
			Timestamp:        macro.Timestamp,
		}
	}
	return nil
}

// GetStoredMacro returns the StoredMacro object for a macro by name
func (ms *MacroSystem) GetStoredMacro(name string) (*StoredMacro, bool) {
	ms.mu.RLock()
	objectID, exists := ms.macros[name]
	ms.mu.RUnlock()

	if !exists || ms.executor == nil {
		return nil, false
	}

	obj, exists := ms.executor.getObject(objectID)
	if !exists {
		return nil, false
	}

	if macro, ok := obj.(StoredMacro); ok {
		return &macro, true
	}
	return nil, false
}

// DeleteMacro deletes a macro
func (ms *MacroSystem) DeleteMacro(name string) bool {
	ms.mu.Lock()
	objectID, exists := ms.macros[name]
	if !exists {
		ms.mu.Unlock()
		ms.logger.Error("Macro \"%s\" not found", name)
		return false
	}

	delete(ms.macros, name)
	ms.mu.Unlock()

	// Release the reference held by the registry
	if ms.executor != nil {
		ms.executor.decrementObjectRefCount(objectID)
	}

	ms.logger.Debug("Deleted macro \"%s\" (released object %d)", name, objectID)
	return true
}

// ClearMacros clears all macros
func (ms *MacroSystem) ClearMacros() int {
	ms.mu.Lock()

	// Collect all object IDs before clearing
	objectIDs := make([]int, 0, len(ms.macros))
	for _, objectID := range ms.macros {
		objectIDs = append(objectIDs, objectID)
	}

	count := len(ms.macros)
	ms.macros = make(map[string]int)
	ms.mu.Unlock()

	// Release all references
	if ms.executor != nil {
		for _, objectID := range objectIDs {
			ms.executor.decrementObjectRefCount(objectID)
		}
	}

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
