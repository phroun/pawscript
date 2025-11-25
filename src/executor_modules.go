package pawscript

import (
	"fmt"
	"strings"
)

// executeSuperCommand checks if cmdName is a super command and executes it
// Returns (result, handled) where handled indicates if it was a super command
func (e *Executor) executeSuperCommand(
	cmdName string,
	args []interface{},
	namedArgs map[string]interface{},
	state *ExecutionState,
	position *SourcePosition,
) (Result, bool) {
	switch cmdName {
	case "MODULE":
		return e.handleMODULE(args, state, position), true
	case "LIBRARY":
		return e.handleLIBRARY(args, state, position), true
	case "IMPORT":
		return e.handleIMPORT(args, state, position), true
	case "REMOVE":
		return e.handleREMOVE(args, state, position), true
	case "EXPORT":
		return e.handleEXPORT(args, namedArgs, state, position), true
	default:
		return nil, false
	}
}

// handleMODULE sets the default module name for exports
// Usage: MODULE module_name
func (e *Executor) handleMODULE(args []interface{}, state *ExecutionState, position *SourcePosition) Result {
	if len(args) != 1 {
		e.logger.CommandError(CatSystem, "MODULE", "Expected 1 argument (module name)", position)
		return BoolStatus(false)
	}

	moduleName := fmt.Sprintf("%v", args[0])
	if moduleName == "" {
		e.logger.CommandError(CatSystem, "MODULE", "Module name cannot be empty", position)
		return BoolStatus(false)
	}

	state.moduleEnv.mu.Lock()
	state.moduleEnv.DefaultName = moduleName
	state.moduleEnv.mu.Unlock()

	e.logger.Debug("MODULE: Set default module name to \"%s\"", moduleName)
	return BoolStatus(true)
}

// handleLIBRARY manipulates LibraryRestricted
// Usage: LIBRARY "pattern"
// Patterns: "restrict *", "allow *", "restrict module", "allow module",
//           "allow module::item1,item2", "allow dest=source"
func (e *Executor) handleLIBRARY(args []interface{}, state *ExecutionState, position *SourcePosition) Result {
	if len(args) != 1 {
		e.logger.CommandError(CatSystem, "LIBRARY", "Expected 1 argument (pattern)", position)
		return BoolStatus(false)
	}

	// Require a quoted string argument for safety
	var pattern string
	switch v := args[0].(type) {
	case string:
		pattern = v
	case QuotedString:
		pattern = string(v)
	default:
		e.logger.CommandError(CatSystem, "LIBRARY", "Argument must be a quoted string (e.g., LIBRARY \"restrict *\")", position)
		return BoolStatus(false)
	}
	parts := strings.Fields(pattern)

	if len(parts) < 2 {
		e.logger.CommandError(CatSystem, "LIBRARY", fmt.Sprintf("Invalid pattern: %s", pattern), position)
		return BoolStatus(false)
	}

	action := parts[0]
	target := parts[1]

	state.moduleEnv.mu.Lock()
	defer state.moduleEnv.mu.Unlock()

	switch action {
	case "restrict":
		if target == "*" {
			// Empty LibraryRestricted
			state.moduleEnv.CopyLibraryRestricted()
			state.moduleEnv.LibraryRestricted = make(Library)
			e.logger.Debug("LIBRARY: Restricted all modules")
		} else {
			// Remove specific module
			state.moduleEnv.CopyLibraryRestricted()
			delete(state.moduleEnv.LibraryRestricted, target)
			e.logger.Debug("LIBRARY: Restricted module \"%s\"", target)
		}

	case "allow":
		if target == "*" {
			// Copy all from Inherited
			state.moduleEnv.CopyLibraryRestricted()
			for modName, section := range state.moduleEnv.LibraryInherited {
				newSection := make(ModuleSection)
				for itemName, item := range section {
					newSection[itemName] = item
				}
				state.moduleEnv.LibraryRestricted[modName] = newSection
			}
			e.logger.Debug("LIBRARY: Allowed all modules")
		} else if strings.Contains(target, "=") {
			// Rename: "dest=source"
			renameParts := strings.SplitN(target, "=", 2)
			if len(renameParts) != 2 {
				e.logger.CommandError(CatSystem, "LIBRARY", fmt.Sprintf("Invalid rename pattern: %s", target), position)
				return BoolStatus(false)
			}
			destName := renameParts[0]
			sourceName := renameParts[1]

			// Find source in Inherited
			if section, exists := state.moduleEnv.LibraryInherited[sourceName]; exists {
				state.moduleEnv.CopyLibraryRestricted()
				newSection := make(ModuleSection)
				for itemName, item := range section {
					newSection[itemName] = item
				}
				state.moduleEnv.LibraryRestricted[destName] = newSection
				e.logger.Debug("LIBRARY: Renamed module \"%s\" to \"%s\"", sourceName, destName)
			} else {
				e.logger.CommandError(CatSystem, "LIBRARY", fmt.Sprintf("Source module not found: %s", sourceName), position)
				return BoolStatus(false)
			}
		} else if strings.Contains(target, "::") {
			// Specific items: "module::item1,item2,#obj"
			moduleParts := strings.SplitN(target, "::", 2)
			if len(moduleParts) != 2 {
				e.logger.CommandError(CatSystem, "LIBRARY", fmt.Sprintf("Invalid module::items pattern: %s", target), position)
				return BoolStatus(false)
			}
			moduleName := moduleParts[0]
			itemsStr := moduleParts[1]
			items := strings.Split(itemsStr, ",")

			// Find source module
			sourceSection, exists := state.moduleEnv.LibraryInherited[moduleName]
			if !exists {
				e.logger.CommandError(CatSystem, "LIBRARY", fmt.Sprintf("Module not found: %s", moduleName), position)
				return BoolStatus(false)
			}

			state.moduleEnv.CopyLibraryRestricted()

			// Ensure module exists in LibraryRestricted
			if state.moduleEnv.LibraryRestricted[moduleName] == nil {
				state.moduleEnv.LibraryRestricted[moduleName] = make(ModuleSection)
			}

			// Add specific items
			for _, itemName := range items {
				itemName = strings.TrimSpace(itemName)
				if item, exists := sourceSection[itemName]; exists {
					state.moduleEnv.LibraryRestricted[moduleName][itemName] = item
					e.logger.Debug("LIBRARY: Allowed %s::%s", moduleName, itemName)
				} else {
					e.logger.CommandError(CatSystem, "LIBRARY", fmt.Sprintf("Item not found: %s::%s", moduleName, itemName), position)
					return BoolStatus(false)
				}
			}
		} else {
			// Add entire module
			if section, exists := state.moduleEnv.LibraryInherited[target]; exists {
				state.moduleEnv.CopyLibraryRestricted()
				newSection := make(ModuleSection)
				for itemName, item := range section {
					newSection[itemName] = item
				}
				state.moduleEnv.LibraryRestricted[target] = newSection
				e.logger.Debug("LIBRARY: Allowed module \"%s\"", target)
			} else {
				e.logger.CommandError(CatSystem, "LIBRARY", fmt.Sprintf("Module not found: %s", target), position)
				return BoolStatus(false)
			}
		}

	default:
		e.logger.CommandError(CatSystem, "LIBRARY", fmt.Sprintf("Unknown action: %s (expected 'restrict' or 'allow')", action), position)
		return BoolStatus(false)
	}

	return BoolStatus(true)
}

// handleIMPORT imports items from LibraryRestricted into module registries
// Usage: IMPORT "module" or IMPORT "module::item1,item2" or IMPORT "module::orig=alias"
func (e *Executor) handleIMPORT(args []interface{}, state *ExecutionState, position *SourcePosition) Result {
	if len(args) != 1 {
		e.logger.CommandError(CatSystem, "IMPORT", "Expected 1 argument (module spec)", position)
		return BoolStatus(false)
	}

	spec := fmt.Sprintf("%v", args[0])

	state.moduleEnv.mu.Lock()
	defer state.moduleEnv.mu.Unlock()

	var moduleName string
	var itemsToImport []string
	var importAll bool

	if strings.Contains(spec, "::") {
		// Specific items: "module::item1,item2" or "module::orig=alias"
		parts := strings.SplitN(spec, "::", 2)
		moduleName = parts[0]
		itemsStr := parts[1]
		itemsToImport = strings.Split(itemsStr, ",")
		importAll = false
	} else {
		// Import all items from module
		moduleName = spec
		importAll = true
	}

	// Find module in LibraryRestricted
	section, exists := state.moduleEnv.LibraryRestricted[moduleName]
	if !exists {
		e.logger.CommandError(CatSystem, "IMPORT", fmt.Sprintf("Module not found in library: %s", moduleName), position)
		return BoolStatus(false)
	}

	if importAll {
		// Import all items - collect errors for collisions
		var collisions []string
		for itemName, item := range section {
			if errMsg := e.importItem(state, moduleName, itemName, itemName, item, false); errMsg != "" {
				collisions = append(collisions, errMsg)
			}
		}
		if len(collisions) > 0 {
			e.logger.CommandError(CatSystem, "IMPORT", fmt.Sprintf("Name collisions: %s", strings.Join(collisions, "; ")), position)
			return BoolStatus(false)
		}
		e.logger.Debug("IMPORT: Imported all items from module \"%s\"", moduleName)
	} else {
		// Import specific items
		for _, itemSpec := range itemsToImport {
			itemSpec = strings.TrimSpace(itemSpec)

			var originalName, localName string
			var hasRename bool
			if strings.Contains(itemSpec, "=") {
				// Rename syntax: "newname=original" (local=original)
				renameParts := strings.SplitN(itemSpec, "=", 2)
				localName = renameParts[0]    // New local name
				originalName = renameParts[1] // Original name from library
				hasRename = true
			} else {
				// No rename
				originalName = itemSpec
				localName = itemSpec
				hasRename = false
			}

			item, exists := section[originalName]
			if !exists {
				e.logger.CommandError(CatSystem, "IMPORT", fmt.Sprintf("Item not found: %s::%s", moduleName, originalName), position)
				return BoolStatus(false)
			}

			if errMsg := e.importItem(state, moduleName, originalName, localName, item, hasRename); errMsg != "" {
				e.logger.CommandError(CatSystem, "IMPORT", errMsg, position)
				return BoolStatus(false)
			}
			e.logger.Debug("IMPORT: Imported %s::%s as \"%s\"", moduleName, originalName, localName)
		}
	}

	return BoolStatus(true)
}

// importItem imports a single item into the appropriate registry.
// If hasRename is false and the item already exists, returns an error string.
// NOTE: Caller must hold state.moduleEnv.mu lock
func (e *Executor) importItem(state *ExecutionState, moduleName, originalName, localName string, item *ModuleItem, hasRename bool) string {
	switch item.Type {
	case "command":
		// Check for collision if no explicit rename
		if !hasRename {
			if handler, exists := state.moduleEnv.CommandRegistryModule[localName]; exists && handler != nil {
				return fmt.Sprintf("command '%s' already exists; to import, use rename syntax: <newname>=%s", localName, originalName)
			}
		}
		state.moduleEnv.EnsureCommandRegistryCopied()
		state.moduleEnv.CommandRegistryModule[localName] = item.Value.(Handler)
		state.moduleEnv.ImportedFrom[localName] = &ImportMetadata{
			ModuleName:   moduleName,
			OriginalName: originalName,
		}

	case "macro":
		// Check for collision if no explicit rename
		if !hasRename {
			if macro, exists := state.moduleEnv.MacrosModule[localName]; exists && macro != nil {
				return fmt.Sprintf("macro '%s' already exists; to import, use rename syntax: <newname>=%s", localName, originalName)
			}
		}
		state.moduleEnv.EnsureMacroRegistryCopied()
		state.moduleEnv.MacrosModule[localName] = item.Value.(*StoredMacro)
		state.moduleEnv.ImportedFrom[localName] = &ImportMetadata{
			ModuleName:   moduleName,
			OriginalName: originalName,
		}

	case "object":
		// Check for collision if no explicit rename
		if !hasRename {
			if _, exists := state.moduleEnv.ObjectsModule[localName]; exists {
				return fmt.Sprintf("object '%s' already exists; to import, use rename syntax: <newname>=%s", localName, originalName)
			}
		}
		state.moduleEnv.EnsureObjectRegistryCopied()
		state.moduleEnv.ObjectsModule[localName] = item.Value
		state.moduleEnv.ImportedFrom[localName] = &ImportMetadata{
			ModuleName:   moduleName,
			OriginalName: originalName,
		}
	}
	return "" // success
}

// handleREMOVE removes items from module registries
// Usage: REMOVE ALL - resets all registries to clean slate
// Usage: REMOVE modulename - removes all items from that module
// Usage: REMOVE "module::item1,item2" - removes specific items by scoped name
func (e *Executor) handleREMOVE(args []interface{}, state *ExecutionState, position *SourcePosition) Result {
	if len(args) == 0 {
		e.logger.CommandError(CatSystem, "REMOVE", "Expected at least 1 argument (ALL, module name, or module::items)", position)
		return BoolStatus(false)
	}

	state.moduleEnv.mu.Lock()
	defer state.moduleEnv.mu.Unlock()

	// Check for REMOVE ALL (symbol, not quoted string)
	if len(args) == 1 {
		if sym, ok := args[0].(Symbol); ok && string(sym) == "ALL" {
			// Reset all module registries to clean slate
			state.moduleEnv.MacrosModule = make(map[string]*StoredMacro)
			state.moduleEnv.macrosModuleCopied = true
			state.moduleEnv.CommandRegistryModule = make(map[string]Handler)
			state.moduleEnv.commandsModuleCopied = true
			state.moduleEnv.ObjectsModule = make(map[string]interface{})
			state.moduleEnv.objectsModuleCopied = true
			// Clear ImportedFrom as well
			state.moduleEnv.ImportedFrom = make(map[string]*ImportMetadata)
			e.logger.Debug("REMOVE ALL: Reset all module registries to clean slate")
			return BoolStatus(true)
		}
	}

	for _, arg := range args {
		spec := fmt.Sprintf("%v", arg)

		if strings.Contains(spec, "::") {
			// Scoped removal: "module::item1,item2"
			parts := strings.SplitN(spec, "::", 2)
			moduleName := parts[0]
			itemsStr := parts[1]

			// Verify module exists in LibraryRestricted
			section, exists := state.moduleEnv.LibraryRestricted[moduleName]
			if !exists {
				e.logger.CommandError(CatSystem, "REMOVE", fmt.Sprintf("Module not found: %s", moduleName), position)
				return BoolStatus(false)
			}

			// Parse comma-separated items
			items := strings.Split(itemsStr, ",")
			for _, itemSpec := range items {
				itemName := strings.TrimSpace(itemSpec)
				if itemName == "" {
					continue
				}

				// Verify item exists in the module
				item, exists := section[itemName]
				if !exists {
					e.logger.CommandError(CatSystem, "REMOVE", fmt.Sprintf("Item not found: %s::%s", moduleName, itemName), position)
					return BoolStatus(false)
				}

				e.removeItem(state, itemName, item.Type)
				e.logger.Debug("REMOVE: Removed %s::%s", moduleName, itemName)
			}
		} else {
			// Module removal: remove all items from the module
			moduleName := spec

			// Find module in LibraryRestricted
			section, exists := state.moduleEnv.LibraryRestricted[moduleName]
			if !exists {
				e.logger.CommandError(CatSystem, "REMOVE", fmt.Sprintf("Module not found: %s", moduleName), position)
				return BoolStatus(false)
			}

			// Remove all items from this module
			for itemName, item := range section {
				e.removeItem(state, itemName, item.Type)
			}
			e.logger.Debug("REMOVE: Removed all items from module \"%s\"", moduleName)
		}
	}

	return BoolStatus(true)
}

// removeItem removes a single item from the appropriate registry
// NOTE: Caller must hold state.moduleEnv.mu lock
func (e *Executor) removeItem(state *ExecutionState, itemName, itemType string) {
	switch itemType {
	case "command":
		if _, exists := state.moduleEnv.CommandRegistryModule[itemName]; exists {
			state.moduleEnv.EnsureCommandRegistryCopied()
			state.moduleEnv.CommandRegistryModule[itemName] = nil // nil marks as REMOVEd
		}
	case "macro":
		if _, exists := state.moduleEnv.MacrosModule[itemName]; exists {
			state.moduleEnv.EnsureMacroRegistryCopied()
			state.moduleEnv.MacrosModule[itemName] = nil // nil marks as REMOVEd
		}
	case "object":
		if _, exists := state.moduleEnv.ObjectsModule[itemName]; exists {
			state.moduleEnv.EnsureObjectRegistryCopied()
			delete(state.moduleEnv.ObjectsModule, itemName)
		}
	}
	// Remove from ImportedFrom tracking
	delete(state.moduleEnv.ImportedFrom, itemName)
}

// handleEXPORT exports items to ModuleExports
// Usage: EXPORT item1 item2 #obj1 item3...
func (e *Executor) handleEXPORT(args []interface{}, namedArgs map[string]interface{}, state *ExecutionState, position *SourcePosition) Result {
	if len(args) == 0 {
		e.logger.CommandError(CatSystem, "EXPORT", "Expected at least 1 argument (item names)", position)
		return BoolStatus(false)
	}

	state.moduleEnv.mu.Lock()
	defer state.moduleEnv.mu.Unlock()

	// Check if MODULE has been called
	if state.moduleEnv.DefaultName == "" {
		e.logger.CommandError(CatSystem, "EXPORT", "MODULE must be called before EXPORT", position)
		return BoolStatus(false)
	}

	moduleName := state.moduleEnv.DefaultName

	// Ensure module section exists in ModuleExports
	if state.moduleEnv.ModuleExports[moduleName] == nil {
		state.moduleEnv.ModuleExports[moduleName] = make(ModuleSection)
	}

	section := state.moduleEnv.ModuleExports[moduleName]

	for _, arg := range args {
		itemName := fmt.Sprintf("%v", arg)

		// Check if it's an object export (#-prefixed)
		if strings.HasPrefix(itemName, "#") {
			// Export from ObjectsModule
			objName := itemName // Keep the # prefix
			var objValue interface{}
			found := false

			if state.moduleEnv.ObjectsModule != nil {
				if val, exists := state.moduleEnv.ObjectsModule[objName]; exists {
					objValue = val
					found = true
				}
			}
			if !found && state.moduleEnv.ObjectsInherited != nil {
				if val, exists := state.moduleEnv.ObjectsInherited[objName]; exists {
					objValue = val
					found = true
				}
			}

			if !found {
				e.logger.CommandError(CatSystem, "EXPORT", fmt.Sprintf("Object not found: %s", objName), position)
				return BoolStatus(false)
			}

			section[objName] = &ModuleItem{
				Type:  "object",
				Value: objValue,
			}
			e.logger.Debug("EXPORT: Exported object \"%s\" from module \"%s\"", objName, moduleName)
			continue
		}

		// Check for macro first
		if state.moduleEnv.MacrosModule != nil {
			if macro, exists := state.moduleEnv.MacrosModule[itemName]; exists {
				section[itemName] = &ModuleItem{
					Type:  "macro",
					Value: macro,
				}
				e.logger.Debug("EXPORT: Exported macro \"%s\" from module \"%s\"", itemName, moduleName)
				continue
			}
		}
		if macro, exists := state.moduleEnv.MacrosInherited[itemName]; exists {
			section[itemName] = &ModuleItem{
				Type:  "macro",
				Value: macro,
			}
			e.logger.Debug("EXPORT: Exported macro \"%s\" from module \"%s\"", itemName, moduleName)
			continue
		}

		// Check for command
		if state.moduleEnv.CommandRegistryModule != nil {
			if handler, exists := state.moduleEnv.CommandRegistryModule[itemName]; exists {
				section[itemName] = &ModuleItem{
					Type:  "command",
					Value: handler,
				}
				e.logger.Debug("EXPORT: Exported command \"%s\" from module \"%s\"", itemName, moduleName)
				continue
			}
		}
		if handler, exists := state.moduleEnv.CommandRegistryInherited[itemName]; exists {
			section[itemName] = &ModuleItem{
				Type:  "command",
				Value: handler,
			}
			e.logger.Debug("EXPORT: Exported command \"%s\" from module \"%s\"", itemName, moduleName)
			continue
		}

		// Not found
		e.logger.CommandError(CatSystem, "EXPORT", fmt.Sprintf("Item not found: %s", itemName), position)
		return BoolStatus(false)
	}

	return BoolStatus(true)
}
