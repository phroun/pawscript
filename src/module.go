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

// ItemMetadata tracks comprehensive metadata about an item in the registry
type ItemMetadata struct {
	// Original module name as registered/loaded from disk or stdlib
	OriginalModuleName string

	// For macros/objects: source location where defined/exported
	// For commands: empty
	SourceFile   string
	SourceLine   int
	SourceColumn int

	// For commands: "standard" (stdlib) or "host" (registered by host app)
	// For macros/objects: empty
	RegistrationSource string

	// The module name it was IMPORTed from (may differ if renamed via MODULE)
	ImportedFromModule string

	// The original item name in the library (before any rename via IMPORT)
	OriginalName string

	// The item type: "command", "macro", "object"
	ItemType string
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

	// Item metadata tracking (keyed by local name)
	ItemMetadataInherited map[string]*ItemMetadata // Reference from parent
	ItemMetadataModule    map[string]*ItemMetadata // Copy-on-write

	// Tracking flags for copy-on-write
	libraryInheritedCopied  bool
	libraryRestrictedCopied bool
	commandsModuleCopied    bool
	macrosModuleCopied      bool
	objectsModuleCopied     bool
	metadataModuleCopied    bool
}

// NewModuleEnvironment creates a new module environment
// All registry pairs (Inherited/Module) start pointing to the same map instance.
// They only diverge via copy-on-write when modifications are made.
func NewModuleEnvironment() *ModuleEnvironment {
	libInherited := make(Library)
	cmdRegistry := make(map[string]Handler)
	macroRegistry := make(map[string]*StoredMacro)
	objRegistry := make(map[string]interface{})
	metadataRegistry := make(map[string]*ItemMetadata)

	return &ModuleEnvironment{
		DefaultName:              "",
		LibraryInherited:         libInherited,
		LibraryRestricted:        libInherited, // Same instance, COW on modification
		CommandRegistryInherited: cmdRegistry,
		CommandRegistryModule:    cmdRegistry, // Same instance, COW on modification
		MacrosInherited:          macroRegistry,
		MacrosModule:             macroRegistry, // Same instance, COW on modification
		ObjectsInherited:         objRegistry,
		ObjectsModule:            objRegistry, // Same instance, COW on modification
		ModuleExports:            make(Library),
		ItemMetadataInherited:    metadataRegistry,
		ItemMetadataModule:       metadataRegistry, // Same instance, COW on modification
		libraryRestrictedCopied:  false,
		commandsModuleCopied:     false,
		macrosModuleCopied:       false,
		objectsModuleCopied:      false,
		metadataModuleCopied:     false,
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
	effectiveMetadata := getEffectiveMetadataRegistry(parent)

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

		// Metadata: both point to effective metadata registry
		ItemMetadataInherited: effectiveMetadata,
		ItemMetadataModule:    effectiveMetadata,

		ModuleExports:           make(Library), // Start blank - caller merges after execution
		libraryRestrictedCopied: false,
		commandsModuleCopied:    false,
		macrosModuleCopied:      false,
		objectsModuleCopied:     false,
		metadataModuleCopied:    false,
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
	effectiveCommands := getEffectiveCommandRegistry(parent)
	effectiveMacros := getEffectiveMacroRegistry(parent)
	effectiveObjects := getEffectiveObjectRegistry(parent)
	effectiveMetadata := getEffectiveMetadataRegistry(parent)

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

		// Metadata: both point to effective metadata registry
		ItemMetadataInherited: effectiveMetadata,
		ItemMetadataModule:    effectiveMetadata,

		// ModuleExports starts blank - caller merges into their LibraryInherited after execution
		ModuleExports: make(Library),

		// COW flags reset - first modification triggers copy
		libraryRestrictedCopied: false,
		commandsModuleCopied:    false,
		macrosModuleCopied:      false,
		objectsModuleCopied:     false,
		metadataModuleCopied:    false,
	}
}

// Helper functions to get effective registries.
// Since Inherited and Module start as the same map instance and diverge via COW,
// the Module registry always reflects the current effective state.

func getEffectiveCommandRegistry(env *ModuleEnvironment) map[string]Handler {
	return env.CommandRegistryModule
}

func getEffectiveMacroRegistry(env *ModuleEnvironment) map[string]*StoredMacro {
	return env.MacrosModule
}

func getEffectiveObjectRegistry(env *ModuleEnvironment) map[string]interface{} {
	return env.ObjectsModule
}

func getEffectiveMetadataRegistry(env *ModuleEnvironment) map[string]*ItemMetadata {
	return env.ItemMetadataModule
}

// CopyLibraryInherited performs copy-on-write for LibraryInherited
// This is used by LIBRARY "forget" to modify Inherited without affecting Restricted
func (env *ModuleEnvironment) CopyLibraryInherited() {
	if env.libraryInheritedCopied {
		return
	}

	// Deep copy the library structure
	newLib := make(Library)
	for moduleName, section := range env.LibraryInherited {
		newSection := make(ModuleSection)
		for itemName, item := range section {
			newSection[itemName] = item // Items themselves are references
		}
		newLib[moduleName] = newSection
	}

	env.LibraryInherited = newLib
	env.libraryInheritedCopied = true
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

// EnsureCommandRegistryCopied performs copy-on-write for CommandRegistryModule.
// Creates an isolated copy so modifications don't affect the original shared map.
func (env *ModuleEnvironment) EnsureCommandRegistryCopied() {
	if env.commandsModuleCopied {
		return
	}
	// Copy current state (Module == Inherited before COW) to a new map
	newModule := make(map[string]Handler, len(env.CommandRegistryModule))
	for k, v := range env.CommandRegistryModule {
		newModule[k] = v
	}
	env.CommandRegistryModule = newModule
	env.commandsModuleCopied = true
}

// EnsureMacroRegistryCopied performs copy-on-write for MacrosModule.
// Creates an isolated copy so modifications don't affect the original shared map.
func (env *ModuleEnvironment) EnsureMacroRegistryCopied() {
	if env.macrosModuleCopied {
		return
	}
	// Copy current state to a new map
	newModule := make(map[string]*StoredMacro, len(env.MacrosModule))
	for k, v := range env.MacrosModule {
		newModule[k] = v
	}
	env.MacrosModule = newModule
	env.macrosModuleCopied = true
}

// EnsureObjectRegistryCopied performs copy-on-write for ObjectsModule.
// Creates an isolated copy so modifications don't affect the original shared map.
func (env *ModuleEnvironment) EnsureObjectRegistryCopied() {
	if env.objectsModuleCopied {
		return
	}
	// Copy current state to a new map
	newModule := make(map[string]interface{}, len(env.ObjectsModule))
	for k, v := range env.ObjectsModule {
		newModule[k] = v
	}
	env.ObjectsModule = newModule
	env.objectsModuleCopied = true
}

// EnsureMetadataRegistryCopied performs copy-on-write for ItemMetadataModule.
// Creates an isolated copy so modifications don't affect the original shared map.
func (env *ModuleEnvironment) EnsureMetadataRegistryCopied() {
	if env.metadataModuleCopied {
		return
	}
	// Copy current state to a new map
	newModule := make(map[string]*ItemMetadata, len(env.ItemMetadataModule))
	for k, v := range env.ItemMetadataModule {
		newModule[k] = v
	}
	env.ItemMetadataModule = newModule
	env.metadataModuleCopied = true
}

// CopyCommandRegistry is an alias for EnsureCommandRegistryCopied for backward compatibility.
func (env *ModuleEnvironment) CopyCommandRegistry() {
	env.EnsureCommandRegistryCopied()
}

// CopyMacroRegistry is an alias for EnsureMacroRegistryCopied for backward compatibility.
func (env *ModuleEnvironment) CopyMacroRegistry() {
	env.EnsureMacroRegistryCopied()
}

// CopyObjectRegistry is an alias for EnsureObjectRegistryCopied for backward compatibility.
func (env *ModuleEnvironment) CopyObjectRegistry() {
	env.EnsureObjectRegistryCopied()
}

// GetCommand looks up a command from the module's command registry.
// CommandRegistryModule and CommandRegistryInherited start as the same map instance
// and only diverge via COW. A nil handler value means the command was REMOVEd.
func (env *ModuleEnvironment) GetCommand(name string) (Handler, bool) {
	env.mu.RLock()
	defer env.mu.RUnlock()

	// Check Module registry (which starts as same instance as Inherited, diverges via COW)
	handler, exists := env.CommandRegistryModule[name]
	if !exists {
		return nil, false
	}
	// A nil handler means the command was explicitly REMOVEd
	if handler == nil {
		return nil, false
	}
	return handler, true
}

// GetMacro looks up a macro from the module's macro registry.
// MacrosModule and MacrosInherited start as the same map instance and diverge via COW.
// A nil macro value means the macro was explicitly REMOVEd.
func (env *ModuleEnvironment) GetMacro(name string) (*StoredMacro, bool) {
	env.mu.RLock()
	defer env.mu.RUnlock()

	macro, exists := env.MacrosModule[name]
	if !exists {
		return nil, false
	}
	// A nil macro means it was explicitly REMOVEd
	if macro == nil {
		return nil, false
	}
	return macro, true
}

// GetObject looks up a #-prefixed object from the module's object registry.
// ObjectsModule and ObjectsInherited start as the same map instance and diverge via COW.
// A nil object value means the object was explicitly REMOVEd.
func (env *ModuleEnvironment) GetObject(name string) (interface{}, bool) {
	env.mu.RLock()
	defer env.mu.RUnlock()

	obj, exists := env.ObjectsModule[name]
	if !exists {
		return nil, false
	}
	// Note: For objects, we can't distinguish between "removed" (nil) and "stored nil value"
	// Since PawScript doesn't have a nil object type, this is acceptable
	return obj, true
}

// RegisterCommandToModule registers a command handler to the module environment
func (env *ModuleEnvironment) RegisterCommandToModule(name string, handler Handler) {
	env.mu.Lock()
	defer env.mu.Unlock()

	env.CopyCommandRegistry()
	env.CommandRegistryModule[name] = handler
}

// PopulateDefaultImports copies all commands and objects from LibraryInherited
// into CommandRegistryInherited and ObjectsInherited, making them directly callable.
// Also populates ItemMetadataInherited with metadata for each item.
// This should be called after all commands are registered via RegisterCommandInModule.
func (env *ModuleEnvironment) PopulateDefaultImports() {
	env.mu.Lock()
	defer env.mu.Unlock()

	// Iterate through all modules in LibraryInherited
	for moduleName, section := range env.LibraryInherited {
		for itemName, item := range section {
			// Create metadata for this item
			metadata := &ItemMetadata{
				OriginalModuleName: moduleName,
				ImportedFromModule: moduleName,
				OriginalName:       itemName,
				ItemType:           item.Type,
				RegistrationSource: "standard", // Default for stdlib
			}

			switch item.Type {
			case "command":
				if handler, ok := item.Value.(Handler); ok {
					env.CommandRegistryInherited[itemName] = handler
					env.ItemMetadataInherited[itemName] = metadata
				}
			case "object":
				env.ObjectsInherited[itemName] = item.Value
				metadata.RegistrationSource = "" // Objects don't have registration source
				env.ItemMetadataInherited[itemName] = metadata
			// Note: macros are not auto-imported; they must be defined at runtime
			}
		}
	}

	// LibraryRestricted already points to LibraryInherited (set in NewModuleEnvironment)
	// No need to copy - they share the same reference until LIBRARY command uses COW
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
