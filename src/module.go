package pawscript

import (
	"sync"
)

// ModuleItem represents an exported item from a module (command, macro, or object)
type ModuleItem struct {
	Type  string      // "command", "macro", "object"
	Value interface{} // Handler, *StoredMacro, or stored object value
}

// ModuleSection holds all items exported under a module name
type ModuleSection map[string]*ModuleItem // itemName -> ModuleItem

// Library is a two-level structure: moduleName -> itemName -> ModuleItem
type Library map[string]ModuleSection

// ImportedFrom tracks metadata about imported items
// Structure: localName -> {moduleName, originalName}
type ImportMetadata struct {
	ModuleName   string
	OriginalName string
}

// ModuleEnvironment encapsulates the module system state
type ModuleEnvironment struct {
	mu sync.RWMutex

	// Current default module name for exports
	DefaultName string

	// Library layers (two-level: module -> items)
	LibraryInherited  Library // Read-only reference from parent
	LibraryRestricted Library // Copy-on-write, starts pointing to Inherited

	// Command registry layers
	CommandRegistryInherited map[string]Handler // Reference from parent
	CommandRegistryModule    map[string]Handler // Copy-on-write

	// Macro registry layers
	MacrosInherited map[string]*StoredMacro // Reference from parent
	MacrosModule    map[string]*StoredMacro // Copy-on-write

	// Object storage layers (for #-prefixed exports)
	ObjectsInherited map[string]interface{} // Reference from parent
	ObjectsModule    map[string]interface{} // Copy-on-write

	// Module exports (accumulated during execution)
	ModuleExports Library

	// Import metadata for REMOVE and debugging
	ImportedFrom map[string]*ImportMetadata

	// Tracking flags for copy-on-write
	libraryRestrictedCopied bool
	commandsModuleCopied    bool
	macrosModuleCopied      bool
	objectsModuleCopied     bool
}

// NewModuleEnvironment creates a new module environment
func NewModuleEnvironment() *ModuleEnvironment {
	libInherited := make(Library)
	return &ModuleEnvironment{
		DefaultName:              "",
		LibraryInherited:         libInherited,
		LibraryRestricted:        libInherited, // Initially points to same instance
		CommandRegistryInherited: make(map[string]Handler),
		CommandRegistryModule:    nil, // nil means "use inherited"
		MacrosInherited:          make(map[string]*StoredMacro),
		MacrosModule:             nil, // nil means "use inherited"
		ObjectsInherited:         make(map[string]interface{}),
		ObjectsModule:            nil, // nil means "use inherited"
		ModuleExports:            make(Library),
		ImportedFrom:             make(map[string]*ImportMetadata),
		libraryRestrictedCopied:  false,
		commandsModuleCopied:     false,
		macrosModuleCopied:       false,
		objectsModuleCopied:      false,
	}
}

// NewChildModuleEnvironment creates a child environment inheriting from parent
func NewChildModuleEnvironment(parent *ModuleEnvironment) *ModuleEnvironment {
	parent.mu.RLock()
	defer parent.mu.RUnlock()

	// Get effective registries from parent
	effectiveCommands := getEffectiveCommandRegistry(parent)
	effectiveMacros := getEffectiveMacroRegistry(parent)
	effectiveObjects := getEffectiveObjectRegistry(parent)

	// Child inherits from parent's LibraryRestricted (becomes new Inherited)
	// Child starts with its Restricted pointing to same instance
	return &ModuleEnvironment{
		DefaultName:       parent.DefaultName,
		LibraryInherited:  parent.LibraryRestricted, // Inherit parent's restricted
		LibraryRestricted: parent.LibraryRestricted, // Start with same reference

		// Commands: both point to effective command registry
		// COW flag reset - any modification creates a new copy
		CommandRegistryInherited: effectiveCommands,
		CommandRegistryModule:    effectiveCommands,

		// Macros: both point to effective macro registry
		// COW flag reset - any modification creates a new copy
		MacrosInherited: effectiveMacros,
		MacrosModule:    effectiveMacros,

		// Objects: both point to effective object registry
		// COW flag reset - any modification creates a new copy
		ObjectsInherited: effectiveObjects,
		ObjectsModule:    effectiveObjects,

		ModuleExports:           make(Library), // Start blank - caller merges after execution
		ImportedFrom:            make(map[string]*ImportMetadata),
		libraryRestrictedCopied: false,
		commandsModuleCopied:    false,
		macrosModuleCopied:      false,
		objectsModuleCopied:     false,
	}
}

// NewMacroModuleEnvironment creates an environment for a macro definition
// This captures the current state with copy-on-write isolation:
// - Inherited layers point to parent's current Module layers
// - Module layers share the same reference (COW ensures isolation on modification)
// - ModuleExports starts blank; caller merges exports into their LibraryInherited after execution
func NewMacroModuleEnvironment(parent *ModuleEnvironment) *ModuleEnvironment {
	parent.mu.RLock()
	defer parent.mu.RUnlock()

	// Get effective registries from parent (what macro should see)
	// For commands, we need to merge inherited + module to get full view
	effectiveCommands := getEffectiveCommandRegistry(parent)
	effectiveMacros := getEffectiveMacroRegistry(parent)
	effectiveObjects := getEffectiveObjectRegistry(parent)

	return &ModuleEnvironment{
		DefaultName: parent.DefaultName,

		// Library: both point to parent's LibraryRestricted
		// COW flag reset - any modification creates a new copy
		LibraryInherited:  parent.LibraryRestricted,
		LibraryRestricted: parent.LibraryRestricted,

		// Commands: both point to effective command registry
		// COW flag reset - any modification creates a new copy
		CommandRegistryInherited: effectiveCommands,
		CommandRegistryModule:    effectiveCommands,

		// Macros: both point to effective macro registry
		// COW flag reset - any modification creates a new copy
		MacrosInherited: effectiveMacros,
		MacrosModule:    effectiveMacros,

		// Objects: both point to effective object registry
		// COW flag reset - any modification creates a new copy
		ObjectsInherited: effectiveObjects,
		ObjectsModule:    effectiveObjects,

		// ModuleExports starts blank - caller merges into their LibraryInherited after execution
		ModuleExports: make(Library),

		// Fresh import metadata for this macro
		ImportedFrom: make(map[string]*ImportMetadata),

		// COW flags reset - first modification triggers copy
		libraryRestrictedCopied: false,
		commandsModuleCopied:    false,
		macrosModuleCopied:      false,
		objectsModuleCopied:     false,
	}
}

// Helper functions to get effective registries
func getEffectiveCommandRegistry(env *ModuleEnvironment) map[string]Handler {
	// If no module-specific registry, just inherit
	if env.CommandRegistryModule == nil {
		return env.CommandRegistryInherited
	}

	// If module registry exists, merge inherited + module (module takes precedence)
	// This ensures children get all available commands
	if env.CommandRegistryInherited == nil {
		return env.CommandRegistryModule
	}

	// Merge: start with inherited, overlay module
	merged := make(map[string]Handler, len(env.CommandRegistryInherited)+len(env.CommandRegistryModule))
	for name, handler := range env.CommandRegistryInherited {
		merged[name] = handler
	}
	for name, handler := range env.CommandRegistryModule {
		merged[name] = handler
	}
	return merged
}

func getEffectiveMacroRegistry(env *ModuleEnvironment) map[string]*StoredMacro {
	if env.MacrosModule != nil {
		return env.MacrosModule
	}
	return env.MacrosInherited
}

func getEffectiveObjectRegistry(env *ModuleEnvironment) map[string]interface{} {
	if env.ObjectsModule != nil {
		return env.ObjectsModule
	}
	return env.ObjectsInherited
}

// CopyLibraryRestricted performs copy-on-write for LibraryRestricted
func (env *ModuleEnvironment) CopyLibraryRestricted() {
	if env.libraryRestrictedCopied {
		return
	}

	// Deep copy the library structure
	newLib := make(Library)
	for moduleName, section := range env.LibraryRestricted {
		newSection := make(ModuleSection)
		for itemName, item := range section {
			newSection[itemName] = item // Items themselves are references
		}
		newLib[moduleName] = newSection
	}

	env.LibraryRestricted = newLib
	env.libraryRestrictedCopied = true
}

// EnsureCommandRegistryCopied performs copy-on-write for CommandRegistryModule
// Copies from Inherited to create an isolated Module map
func (env *ModuleEnvironment) EnsureCommandRegistryCopied() {
	if env.commandsModuleCopied {
		return
	}
	// Copy from Inherited to new Module
	newModule := make(map[string]Handler)
	if env.CommandRegistryInherited != nil {
		for k, v := range env.CommandRegistryInherited {
			newModule[k] = v
		}
	}
	env.CommandRegistryModule = newModule
	env.commandsModuleCopied = true
}

// EnsureMacroRegistryCopied performs copy-on-write for MacrosModule
// Copies from Inherited to create an isolated Module map
func (env *ModuleEnvironment) EnsureMacroRegistryCopied() {
	if env.macrosModuleCopied {
		return
	}
	// Copy from Inherited to new Module
	newModule := make(map[string]*StoredMacro)
	if env.MacrosInherited != nil {
		for k, v := range env.MacrosInherited {
			newModule[k] = v
		}
	}
	env.MacrosModule = newModule
	env.macrosModuleCopied = true
}

// EnsureObjectRegistryCopied performs copy-on-write for ObjectsModule
// Copies from Inherited to create an isolated Module map
func (env *ModuleEnvironment) EnsureObjectRegistryCopied() {
	if env.objectsModuleCopied {
		return
	}
	// Copy from Inherited to new Module
	newModule := make(map[string]interface{})
	if env.ObjectsInherited != nil {
		for k, v := range env.ObjectsInherited {
			newModule[k] = v
		}
	}
	env.ObjectsModule = newModule
	env.objectsModuleCopied = true
}

// CopyCommandRegistry performs copy-on-write for CommandRegistryModule
func (env *ModuleEnvironment) CopyCommandRegistry() {
	if env.commandsModuleCopied {
		return
	}

	newReg := make(map[string]Handler)
	source := env.CommandRegistryInherited
	if env.CommandRegistryModule != nil {
		source = env.CommandRegistryModule
	}

	for k, v := range source {
		newReg[k] = v
	}

	env.CommandRegistryModule = newReg
	env.commandsModuleCopied = true
}

// CopyMacroRegistry performs copy-on-write for MacrosModule
func (env *ModuleEnvironment) CopyMacroRegistry() {
	if env.macrosModuleCopied {
		return
	}

	newReg := make(map[string]*StoredMacro)
	source := env.MacrosInherited
	if env.MacrosModule != nil {
		source = env.MacrosModule
	}

	for k, v := range source {
		newReg[k] = v
	}

	env.MacrosModule = newReg
	env.macrosModuleCopied = true
}

// CopyObjectRegistry performs copy-on-write for ObjectsModule
func (env *ModuleEnvironment) CopyObjectRegistry() {
	if env.objectsModuleCopied {
		return
	}

	newReg := make(map[string]interface{})
	source := env.ObjectsInherited
	if env.ObjectsModule != nil {
		source = env.ObjectsModule
	}

	for k, v := range source {
		newReg[k] = v
	}

	env.ObjectsModule = newReg
	env.objectsModuleCopied = true
}

// GetCommand looks up a command from the module's command registry
// Only checks Module (which starts pointing to Inherited and diverges on COW)
func (env *ModuleEnvironment) GetCommand(name string) (Handler, bool) {
	env.mu.RLock()
	defer env.mu.RUnlock()

	// Module either points to Inherited or is a COW copy - just check Module
	if env.CommandRegistryModule != nil {
		if handler, ok := env.CommandRegistryModule[name]; ok {
			return handler, true
		}
	}

	return nil, false
}

// GetMacro looks up a macro from the module's macro registry
// ONLY checks Module registry (explicit module isolation) - NEVER checks Inherited
func (env *ModuleEnvironment) GetMacro(name string) (*StoredMacro, bool) {
	env.mu.RLock()
	defer env.mu.RUnlock()

	// ONLY check Module, never Inherited (explicit module isolation)
	if env.MacrosModule != nil {
		if macro, ok := env.MacrosModule[name]; ok {
			return macro, true
		}
	}

	return nil, false
}

// GetObject looks up a #-prefixed object from the module's object registry
// ONLY checks Module registry (explicit module isolation) - NEVER checks Inherited
func (env *ModuleEnvironment) GetObject(name string) (interface{}, bool) {
	env.mu.RLock()
	defer env.mu.RUnlock()

	// ONLY check Module, never Inherited (explicit module isolation)
	if env.ObjectsModule != nil {
		if obj, ok := env.ObjectsModule[name]; ok {
			return obj, true
		}
	}

	return nil, false
}

// RegisterCommandToModule registers a command handler to the module environment
func (env *ModuleEnvironment) RegisterCommandToModule(name string, handler Handler) {
	env.mu.Lock()
	defer env.mu.Unlock()

	env.CopyCommandRegistry()
	env.CommandRegistryModule[name] = handler
}

// PopulateStdlibModules populates LibraryInherited with stdlib commands organized into modules
// This should be called after all commands are registered in CommandRegistryInherited
func (env *ModuleEnvironment) PopulateStdlibModules() {
	env.mu.Lock()
	defer env.mu.Unlock()

	// List of commands that go into the "core" module
	coreCommands := map[string]bool{
		"true":             true,
		"false":            true,
		"set_result":       true,
		"get_result":       true,
		"ret":              true,
		"get_inferred_type": true,
		"get_type":         true,
		"list":             true,
		"len":              true,
		"keys":             true,
		"get_val":          true,
	}

	// List of commands that go into the "macros" module
	macrosCommands := map[string]bool{
		"macro":        true,
		"call":         true,
		"macro_list":   true,
		"macro_delete": true,
		"macro_clear":  true,
		"command_ref":  true,
	}

	// List of commands that go into the "flow" module
	flowCommands := map[string]bool{
		"if":    true,
		"while": true,
	}

	// List of commands that go into the "debug" module
	debugCommands := map[string]bool{
		"mem_stats": true,
		"env_dump":  true,
		"log_print": true,
	}

	// List of commands that go into the "sys" module
	sysCommands := map[string]bool{
		"exec":      true,
		"microtime": true,
		"datetime":  true,
	}

	// List of commands that go into the "io" module
	ioCommands := map[string]bool{
		"echo":   true,
		"print":  true,
		"write":  true,
		"read":   true,
		"rune":   true,
		"ord":    true,
		"clear":  true,
		"cursor": true,
		"color":  true,
	}

	// Create module sections
	coreModule := make(ModuleSection)
	macrosModule := make(ModuleSection)
	flowModule := make(ModuleSection)
	debugModule := make(ModuleSection)
	sysModule := make(ModuleSection)
	ioModule := make(ModuleSection)
	stdlibModule := make(ModuleSection) // catch-all for remaining commands

	// Distribute commands from CommandRegistryInherited into modules
	for cmdName, handler := range env.CommandRegistryInherited {
		item := &ModuleItem{
			Type:  "command",
			Value: handler,
		}
		if coreCommands[cmdName] {
			coreModule[cmdName] = item
		} else if macrosCommands[cmdName] {
			macrosModule[cmdName] = item
		} else if flowCommands[cmdName] {
			flowModule[cmdName] = item
		} else if debugCommands[cmdName] {
			debugModule[cmdName] = item
		} else if sysCommands[cmdName] {
			sysModule[cmdName] = item
		} else if ioCommands[cmdName] {
			ioModule[cmdName] = item
		} else {
			stdlibModule[cmdName] = item
		}
	}

	// Add modules to LibraryInherited
	env.LibraryInherited["core"] = coreModule
	env.LibraryInherited["macros"] = macrosModule
	env.LibraryInherited["flow"] = flowModule
	env.LibraryInherited["debug"] = debugModule
	env.LibraryInherited["sys"] = sysModule
	env.LibraryInherited["io"] = ioModule
	env.LibraryInherited["stdlib"] = stdlibModule

	// Initially, LibraryRestricted should allow all modules
	env.LibraryRestricted = make(Library)
	for modName, section := range env.LibraryInherited {
		newSection := make(ModuleSection)
		for itemName, item := range section {
			newSection[itemName] = item
		}
		env.LibraryRestricted[modName] = newSection
	}
}

// MergeExportsInto merges this environment's ModuleExports into another environment's LibraryInherited
// This is used to persist module exports between executions
func (env *ModuleEnvironment) MergeExportsInto(target *ModuleEnvironment) {
	env.mu.RLock()
	exportsToMerge := make(Library)
	for modName, section := range env.ModuleExports {
		newSection := make(ModuleSection)
		for itemName, item := range section {
			newSection[itemName] = item
		}
		exportsToMerge[modName] = newSection
	}
	env.mu.RUnlock()

	// Merge into target's LibraryInherited
	target.mu.Lock()
	defer target.mu.Unlock()

	for modName, section := range exportsToMerge {
		if target.LibraryInherited[modName] == nil {
			// Module doesn't exist, create it
			target.LibraryInherited[modName] = section
		} else {
			// Module exists, merge items
			for itemName, item := range section {
				target.LibraryInherited[modName][itemName] = item
			}
		}
	}

	// Also update LibraryRestricted to include new exports
	for modName, section := range exportsToMerge {
		if target.LibraryRestricted[modName] == nil {
			// Module doesn't exist in restricted, create it
			newSection := make(ModuleSection)
			for itemName, item := range section {
				newSection[itemName] = item
			}
			target.LibraryRestricted[modName] = newSection
		} else {
			// Module exists, merge items
			for itemName, item := range section {
				target.LibraryRestricted[modName][itemName] = item
			}
		}
	}
}
