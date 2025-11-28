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

	e.logger.DebugCat(CatSystem,"MODULE: Set default module name to \"%s\"", moduleName)
	return BoolStatus(true)
}

// handleLIBRARY manipulates LibraryRestricted and LibraryInherited
// Usage: LIBRARY "pattern"
// Patterns for LibraryRestricted:
//   "restrict *", "allow *", "restrict module", "allow module",
//   "allow module::item1,item2", "allow dest=source"
// Patterns for LibraryInherited (using COW so LibraryRestricted is unaffected):
//   "forget *", "forget module", "forget module::item1,item2"
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
			e.logger.DebugCat(CatSystem,"LIBRARY: Restricted all modules")
		} else {
			// Remove specific module
			state.moduleEnv.CopyLibraryRestricted()
			delete(state.moduleEnv.LibraryRestricted, target)
			e.logger.DebugCat(CatSystem,"LIBRARY: Restricted module \"%s\"", target)
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
			e.logger.DebugCat(CatSystem,"LIBRARY: Allowed all modules")
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
				e.logger.DebugCat(CatSystem,"LIBRARY: Renamed module \"%s\" to \"%s\"", sourceName, destName)
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
					e.logger.DebugCat(CatSystem,"LIBRARY: Allowed %s::%s", moduleName, itemName)
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
				e.logger.DebugCat(CatSystem,"LIBRARY: Allowed module \"%s\"", target)
			} else {
				e.logger.CommandError(CatSystem, "LIBRARY", fmt.Sprintf("Module not found: %s", target), position)
				return BoolStatus(false)
			}
		}

	case "forget":
		// Forget removes items from LibraryInherited (using COW so LibraryRestricted is unaffected)
		if target == "*" {
			// Remove everything from LibraryInherited
			state.moduleEnv.CopyLibraryInherited()
			state.moduleEnv.LibraryInherited = make(Library)
			e.logger.DebugCat(CatSystem,"LIBRARY: Forgot all modules from LibraryInherited")
		} else if strings.Contains(target, "::") {
			// Remove specific items: "module::item1,item2"
			moduleParts := strings.SplitN(target, "::", 2)
			if len(moduleParts) != 2 {
				e.logger.CommandError(CatSystem, "LIBRARY", fmt.Sprintf("Invalid module::items pattern: %s", target), position)
				return BoolStatus(false)
			}
			moduleName := moduleParts[0]
			itemsStr := moduleParts[1]
			items := strings.Split(itemsStr, ",")

			// Check if module exists in LibraryInherited
			_, exists := state.moduleEnv.LibraryInherited[moduleName]
			if !exists {
				e.logger.CommandError(CatSystem, "LIBRARY", fmt.Sprintf("Module not found in LibraryInherited: %s", moduleName), position)
				return BoolStatus(false)
			}

			state.moduleEnv.CopyLibraryInherited()
			// Get the section after COW (it's now a copy)
			section := state.moduleEnv.LibraryInherited[moduleName]

			// Remove specific items
			for _, itemSpec := range items {
				itemName := strings.TrimSpace(itemSpec)
				if itemName == "" {
					continue
				}
				if _, exists := section[itemName]; !exists {
					e.logger.CommandError(CatSystem, "LIBRARY", fmt.Sprintf("Item not found: %s::%s", moduleName, itemName), position)
					return BoolStatus(false)
				}
				delete(section, itemName)
				e.logger.DebugCat(CatSystem,"LIBRARY: Forgot %s::%s from LibraryInherited", moduleName, itemName)
			}

			// If module is now empty, remove it entirely
			if len(section) == 0 {
				delete(state.moduleEnv.LibraryInherited, moduleName)
			}
		} else {
			// Remove entire module
			if _, exists := state.moduleEnv.LibraryInherited[target]; !exists {
				e.logger.CommandError(CatSystem, "LIBRARY", fmt.Sprintf("Module not found in LibraryInherited: %s", target), position)
				return BoolStatus(false)
			}
			state.moduleEnv.CopyLibraryInherited()
			delete(state.moduleEnv.LibraryInherited, target)
			e.logger.DebugCat(CatSystem,"LIBRARY: Forgot module \"%s\" from LibraryInherited", target)
		}

	default:
		e.logger.CommandError(CatSystem, "LIBRARY", fmt.Sprintf("Unknown action: %s (expected 'restrict', 'allow', or 'forget')", action), position)
		return BoolStatus(false)
	}

	return BoolStatus(true)
}

// handleIMPORT imports items from LibraryRestricted into module registries
// Usage: IMPORT "module" or IMPORT "module::item1,item2" or IMPORT "module::orig=alias"
// Multiple arguments can be provided: IMPORT "module1" "module2::item" "module3"
func (e *Executor) handleIMPORT(args []interface{}, state *ExecutionState, position *SourcePosition) Result {
	if len(args) == 0 {
		e.logger.CommandError(CatSystem, "IMPORT", "Expected at least 1 argument (module spec)", position)
		return BoolStatus(false)
	}

	state.moduleEnv.mu.Lock()
	defer state.moduleEnv.mu.Unlock()

	// Process each argument as a separate import spec
	for _, arg := range args {
		spec := fmt.Sprintf("%v", arg)

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
				// moduleName is used for both ImportedFromModule and OriginalModuleName
				// (they may differ if MODULE command renamed the module)
				if errMsg := e.importItem(state, moduleName, moduleName, itemName, itemName, item, false); errMsg != "" {
					collisions = append(collisions, errMsg)
				}
			}
			if len(collisions) > 0 {
				e.logger.CommandError(CatSystem, "IMPORT", fmt.Sprintf("Name collisions: %s", strings.Join(collisions, "; ")), position)
				return BoolStatus(false)
			}
			e.logger.DebugCat(CatSystem,"IMPORT: Imported all items from module \"%s\"", moduleName)
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

				if errMsg := e.importItem(state, moduleName, moduleName, originalName, localName, item, hasRename); errMsg != "" {
					e.logger.CommandError(CatSystem, "IMPORT", errMsg, position)
					return BoolStatus(false)
				}
				e.logger.DebugCat(CatSystem,"IMPORT: Imported %s::%s as \"%s\"", moduleName, originalName, localName)
			}
		}
	}

	return BoolStatus(true)
}

// importItem imports a single item into the appropriate registry.
// If hasRename is false and the item already exists, returns an error string.
// originalModuleName is the module name as it appears in LibraryInherited (for metadata)
// NOTE: Caller must hold state.moduleEnv.mu lock
func (e *Executor) importItem(state *ExecutionState, moduleName, originalModuleName, originalName, localName string, item *ModuleItem, hasRename bool) string {
	// Create metadata for this item
	metadata := &ItemMetadata{
		OriginalModuleName: originalModuleName,
		ImportedFromModule: moduleName,
		OriginalName:       originalName,
		ItemType:           item.Type,
		RegistrationSource: "standard", // Will be overwritten for macros/objects with source info
	}

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

	case "macro":
		// Check for collision if no explicit rename
		if !hasRename {
			if macro, exists := state.moduleEnv.MacrosModule[localName]; exists && macro != nil {
				return fmt.Sprintf("macro '%s' already exists; to import, use rename syntax: <newname>=%s", localName, originalName)
			}
		}
		state.moduleEnv.EnsureMacroRegistryCopied()
		state.moduleEnv.MacrosModule[localName] = item.Value.(*StoredMacro)
		// Get source location from stored macro if available
		if storedMacro, ok := item.Value.(*StoredMacro); ok && storedMacro != nil {
			metadata.SourceFile = storedMacro.DefinitionFile
			metadata.SourceLine = storedMacro.DefinitionLine
			metadata.SourceColumn = storedMacro.DefinitionColumn
			metadata.RegistrationSource = ""
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
		metadata.RegistrationSource = ""
	}

	// Store metadata
	state.moduleEnv.EnsureMetadataRegistryCopied()
	state.moduleEnv.ItemMetadataModule[localName] = metadata

	return "" // success
}

// handleREMOVE removes items from module registries
// Usage: REMOVE ALL - resets all registries to clean slate
// Usage: REMOVE modulename - removes all items from that module
// Usage: REMOVE "module::item1,item2" - removes specific items by scoped name (original names)
// Usage: REMOVE MY "localname1,localname2" - removes items by their local (possibly renamed) names
// Multiple arguments can be provided, each processed independently:
// Usage: REMOVE "module1" MY "item1,item2" "module2::item3"
func (e *Executor) handleREMOVE(args []interface{}, state *ExecutionState, position *SourcePosition) Result {
	if len(args) == 0 {
		e.logger.CommandError(CatSystem, "REMOVE", "Expected at least 1 argument (ALL, MY, module name, or module::items)", position)
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
			state.moduleEnv.ItemMetadataModule = make(map[string]*ItemMetadata)
			state.moduleEnv.metadataModuleCopied = true
			e.logger.DebugCat(CatSystem,"REMOVE ALL: Reset all module registries to clean slate")
			return BoolStatus(true)
		}
	}

	// Process each argument independently
	for _, arg := range args {
		var argStr string
		switch v := arg.(type) {
		case QuotedString:
			argStr = string(v)
		case string:
			argStr = v
		case Symbol:
			argStr = string(v)
		default:
			argStr = fmt.Sprintf("%v", arg)
		}

		// Check for MY-prefixed argument: "<MY>localname1,localname2"
		// The parser concatenates MY + "string" into "<MY>string"
		if strings.HasPrefix(argStr, "<MY>") {
			// Remove by local names (no module:: prefix allowed)
			namesStr := strings.TrimPrefix(argStr, "<MY>")
			names := strings.Split(namesStr, ",")
			for _, name := range names {
				localName := strings.TrimSpace(name)
				if localName == "" {
					continue
				}
				// Cannot use module:: prefix with MY
				if strings.Contains(localName, "::") {
					e.logger.CommandError(CatSystem, "REMOVE", "REMOVE MY does not accept module:: prefix; use local names only", position)
					return BoolStatus(false)
				}
				// Find item type from metadata or by checking registries
				itemType := e.findItemType(state, localName)
				if itemType == "" {
					e.logger.CommandError(CatSystem, "REMOVE", fmt.Sprintf("Item not found: %s", localName), position)
					return BoolStatus(false)
				}
				e.removeItem(state, localName, itemType)
				e.logger.DebugCat(CatSystem,"REMOVE MY: Removed \"%s\"", localName)
			}
			continue // Move to next argument
		}

		// Check for scoped removal: "module::item1,item2"
		if strings.Contains(argStr, "::") {
			parts := strings.SplitN(argStr, "::", 2)
			moduleName := parts[0]
			itemsStr := parts[1]

			// Verify module exists in LibraryRestricted
			section, exists := state.moduleEnv.LibraryRestricted[moduleName]
			if !exists {
				e.logger.CommandError(CatSystem, "REMOVE", fmt.Sprintf("Module not found: %s", moduleName), position)
				return BoolStatus(false)
			}

			// Parse comma-separated items (original library names)
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
				e.logger.DebugCat(CatSystem,"REMOVE: Removed %s::%s", moduleName, itemName)
			}
			continue // Move to next argument
		}

		// Module removal: remove all items from the module
		moduleName := argStr

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
		e.logger.DebugCat(CatSystem,"REMOVE: Removed all items from module \"%s\"", moduleName)
	}

	return BoolStatus(true)
}

// findItemType determines the type of an item by checking registries
// Returns "command", "macro", "object", or "" if not found
func (e *Executor) findItemType(state *ExecutionState, localName string) string {
	// Check metadata first
	if meta, exists := state.moduleEnv.ItemMetadataModule[localName]; exists && meta != nil {
		return meta.ItemType
	}
	// Fall back to checking registries
	if handler, exists := state.moduleEnv.CommandRegistryModule[localName]; exists && handler != nil {
		return "command"
	}
	if macro, exists := state.moduleEnv.MacrosModule[localName]; exists && macro != nil {
		return "macro"
	}
	if _, exists := state.moduleEnv.ObjectsModule[localName]; exists {
		return "object"
	}
	return ""
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
	// Remove metadata tracking
	if _, exists := state.moduleEnv.ItemMetadataModule[itemName]; exists {
		state.moduleEnv.EnsureMetadataRegistryCopied()
		delete(state.moduleEnv.ItemMetadataModule, itemName)
	}
}

// handleEXPORT exports items to ModuleExports
// Usage: EXPORT item1 item2 #obj1 item3...
// Exports can be: macros (from module registries), commands, objects (#prefix), or variables
// Additional quoted forms for re-exporting from LibraryRestricted:
//   EXPORT "modspec::*"                    - re-exports all items from modspec
//   EXPORT "modspec::item1,item2"          - re-exports specific items
//   EXPORT "modspec::new=orig,#obj,item"   - re-exports with optional rename (new=original)
func (e *Executor) handleEXPORT(args []interface{}, namedArgs map[string]interface{}, state *ExecutionState, position *SourcePosition) Result {
	e.logger.DebugCat(CatSystem,"EXPORT called with %d args", len(args))
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
	e.logger.DebugCat(CatSystem,"EXPORT: module name is '%s'", moduleName)

	// Ensure module section exists in ModuleExports
	if state.moduleEnv.ModuleExports[moduleName] == nil {
		state.moduleEnv.ModuleExports[moduleName] = make(ModuleSection)
	}

	section := state.moduleEnv.ModuleExports[moduleName]
	e.logger.DebugCat(CatSystem,"EXPORT: ModuleExports[%s] section created/exists", moduleName)

	for _, arg := range args {
		// Check for quoted re-export form: "module::*" or "module::new=item1,item2"
		var isQuotedArg bool
		var quotedSpec string
		switch v := arg.(type) {
		case QuotedString:
			quotedSpec = string(v)
			isQuotedArg = true
		case string:
			// Check if it looks like a module spec (contains ::)
			if strings.Contains(v, "::") {
				quotedSpec = v
				isQuotedArg = true
			}
		}

		if isQuotedArg && strings.Contains(quotedSpec, "::") {
			// Re-export form from LibraryRestricted
			parts := strings.SplitN(quotedSpec, "::", 2)
			if len(parts) != 2 {
				e.logger.CommandError(CatSystem, "EXPORT", fmt.Sprintf("Invalid re-export pattern: %s", quotedSpec), position)
				return BoolStatus(false)
			}
			sourceModule := parts[0]
			itemsSpec := parts[1]

			// Find source module in LibraryRestricted
			sourceSection, exists := state.moduleEnv.LibraryRestricted[sourceModule]
			if !exists {
				e.logger.CommandError(CatSystem, "EXPORT", fmt.Sprintf("Module not found in LibraryRestricted: %s", sourceModule), position)
				return BoolStatus(false)
			}

			if itemsSpec == "*" {
				// Re-export all items from the module
				for itemName, item := range sourceSection {
					section[itemName] = item
					e.logger.DebugCat(CatSystem,"EXPORT: Re-exported %s::%s to module \"%s\"", sourceModule, itemName, moduleName)
				}
			} else {
				// Re-export specific items, with optional rename (new=original)
				items := strings.Split(itemsSpec, ",")
				for _, itemSpec := range items {
					itemSpec = strings.TrimSpace(itemSpec)
					if itemSpec == "" {
						continue
					}

					var exportName, sourceName string
					if strings.Contains(itemSpec, "=") {
						// Rename syntax: "newname=originalname"
						renameParts := strings.SplitN(itemSpec, "=", 2)
						exportName = renameParts[0]
						sourceName = renameParts[1]
					} else {
						exportName = itemSpec
						sourceName = itemSpec
					}

					item, exists := sourceSection[sourceName]
					if !exists {
						e.logger.CommandError(CatSystem, "EXPORT", fmt.Sprintf("Item not found: %s::%s", sourceModule, sourceName), position)
						return BoolStatus(false)
					}

					section[exportName] = item
					if exportName != sourceName {
						e.logger.DebugCat(CatSystem,"EXPORT: Re-exported %s::%s as \"%s\" to module \"%s\"", sourceModule, sourceName, exportName, moduleName)
					} else {
						e.logger.DebugCat(CatSystem,"EXPORT: Re-exported %s::%s to module \"%s\"", sourceModule, sourceName, moduleName)
					}
				}
			}
			continue
		}

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
			e.logger.DebugCat(CatSystem,"EXPORT: Exported object \"%s\" from module \"%s\"", objName, moduleName)
			continue
		}

		// Check for macro in module registries
		if state.moduleEnv.MacrosModule != nil {
			if macro, exists := state.moduleEnv.MacrosModule[itemName]; exists && macro != nil {
				section[itemName] = &ModuleItem{
					Type:  "macro",
					Value: macro,
				}
				e.logger.DebugCat(CatSystem,"EXPORT: Exported macro \"%s\" from module \"%s\"", itemName, moduleName)
				continue
			}
		}
		if macro, exists := state.moduleEnv.MacrosInherited[itemName]; exists && macro != nil {
			section[itemName] = &ModuleItem{
				Type:  "macro",
				Value: macro,
			}
			e.logger.DebugCat(CatSystem,"EXPORT: Exported macro \"%s\" from module \"%s\"", itemName, moduleName)
			continue
		}

		// Check for command
		if state.moduleEnv.CommandRegistryModule != nil {
			if handler, exists := state.moduleEnv.CommandRegistryModule[itemName]; exists && handler != nil {
				section[itemName] = &ModuleItem{
					Type:  "command",
					Value: handler,
				}
				e.logger.DebugCat(CatSystem,"EXPORT: Exported command \"%s\" from module \"%s\"", itemName, moduleName)
				continue
			}
		}
		if handler, exists := state.moduleEnv.CommandRegistryInherited[itemName]; exists && handler != nil {
			section[itemName] = &ModuleItem{
				Type:  "command",
				Value: handler,
			}
			e.logger.DebugCat(CatSystem,"EXPORT: Exported command \"%s\" from module \"%s\"", itemName, moduleName)
			continue
		}

		// Check for variable (export variable value as an object)
		if val, exists := state.GetVariable(itemName); exists {
			section[itemName] = &ModuleItem{
				Type:  "object",
				Value: val,
			}
			e.logger.DebugCat(CatSystem,"EXPORT: Exported variable \"%s\" as object from module \"%s\"", itemName, moduleName)
			continue
		}

		// Not found
		e.logger.CommandError(CatSystem, "EXPORT", fmt.Sprintf("Item not found: %s", itemName), position)
		return BoolStatus(false)
	}

	return BoolStatus(true)
}
