package pawscript

import (
	"fmt"
	"strings"
)

// Module system - modules contain named items that can be imported individually
// e.g., stdlib module contains: macro, call, macro_list, etc.
// Import with "stdlib::macro" to import a specific command
// Import with "stdlib" to import all commands from the module

// moduleItems stores module -> item name -> handler mapping
var moduleItems = make(map[string]map[string]Handler)

// ImportedItemInfo tracks metadata about an imported item
type ImportedItemInfo struct {
	Module       string // e.g., "stdlib"
	OriginalName string // e.g., "macro"
	Alias        string // e.g., "my_macro" (empty if no alias, same as registered name)
}

// importedItemsMap tracks which items have been imported (by full path)
var importedItemsMap = make(map[string]*ImportedItemInfo)

// RegisterModuleItem registers a single item within a module
// e.g., RegisterModuleItem("stdlib", "macro", handler)
func (e *Executor) RegisterModuleItem(moduleName, itemName string, handler Handler) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if moduleItems[moduleName] == nil {
		moduleItems[moduleName] = make(map[string]Handler)
	}
	moduleItems[moduleName][itemName] = handler
	e.logger.Debug("Registered module item: %s::%s", moduleName, itemName)
}

// ImportModule imports ALL items from a module (e.g., "stdlib")
// Returns true if successful, false if module not found
func (e *Executor) ImportModule(moduleName string) bool {
	e.mu.RLock()
	items, moduleExists := moduleItems[moduleName]
	if !moduleExists {
		e.mu.RUnlock()
		return false
	}
	// Copy item names to avoid holding lock during registration
	itemNames := make([]string, 0, len(items))
	for name := range items {
		itemNames = append(itemNames, name)
	}
	e.mu.RUnlock()

	// Import each item
	for _, itemName := range itemNames {
		fullPath := moduleName + "::" + itemName
		e.ImportItemWithAlias(fullPath, "")
	}

	e.logger.Debug("Imported all items from module: %s", moduleName)
	return true
}

// ImportItem imports a single item from a module (e.g., "stdlib::macro")
// Returns true if successful, false if item not found
func (e *Executor) ImportItem(fullPath string) bool {
	return e.ImportItemWithAlias(fullPath, "")
}

// ImportItemWithAlias imports a single item with an optional alias
// e.g., ImportItemWithAlias("stdlib::macro", "my_macro") registers as "my_macro"
// If alias is empty, uses the original item name
func (e *Executor) ImportItemWithAlias(fullPath, alias string) bool {
	// Parse "module::item" format
	parts := strings.SplitN(fullPath, "::", 2)
	if len(parts) != 2 {
		return false
	}
	moduleName := parts[0]
	itemName := parts[1]

	// Determine the registered name
	registeredName := itemName
	if alias != "" {
		registeredName = alias
	}

	e.mu.Lock()

	// Check if already imported (by full path)
	if importedItemsMap[fullPath] != nil {
		e.mu.Unlock()
		e.logger.Debug("Item already imported: %s", fullPath)
		return true
	}

	// Check if module and item exist
	items, moduleExists := moduleItems[moduleName]
	if !moduleExists {
		e.mu.Unlock()
		return false
	}

	handler, itemExists := items[itemName]
	if !itemExists {
		e.mu.Unlock()
		return false
	}

	// Mark as imported with metadata
	importedItemsMap[fullPath] = &ImportedItemInfo{
		Module:       moduleName,
		OriginalName: itemName,
		Alias:        registeredName,
	}
	e.mu.Unlock()

	// Register the command with the appropriate name
	e.RegisterCommand(registeredName, handler)
	e.logger.Debug("Imported item: %s (registered as command '%s')", fullPath, registeredName)
	return true
}

// HasModuleItem checks if a module item is registered
func (e *Executor) HasModuleItem(fullPath string) bool {
	parts := strings.SplitN(fullPath, "::", 2)
	if len(parts) != 2 {
		return false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if items, exists := moduleItems[parts[0]]; exists {
		_, itemExists := items[parts[1]]
		return itemExists
	}
	return false
}

// IsItemImported checks if a module item has been imported
func (e *Executor) IsItemImported(fullPath string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return importedItemsMap[fullPath] != nil
}

// GetImportInfo returns metadata about an imported item, or nil if not imported
func (e *Executor) GetImportInfo(fullPath string) *ImportedItemInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return importedItemsMap[fullPath]
}

// ListModules returns a list of all available module names
func (e *Executor) ListModules() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	names := make([]string, 0, len(moduleItems))
	for name := range moduleItems {
		names = append(names, name)
	}
	return names
}

// ListModuleItems returns a list of all items in a module
func (e *Executor) ListModuleItems(moduleName string) []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if items, exists := moduleItems[moduleName]; exists {
		names := make([]string, 0, len(items))
		for name := range items {
			names = append(names, name)
		}
		return names
	}
	return nil
}

// registerSuperCommands registers built-in super commands (always available, ALL CAPS)
func (ps *PawScript) registerSuperCommands() {
	// IMPORT - import items from modules (super command)
	// Usage: IMPORT stdlib                       - import all items from module
	//        IMPORT "stdlib::macro"              - import single item
	//        IMPORT "stdlib::macro", "stdlib::call" - import multiple items
	//        IMPORT my_macro: "stdlib::macro"    - import with alias (named arg)
	ps.executor.RegisterCommand("IMPORT", func(ctx *Context) Result {
		// Handle named arguments as aliases: alias_name: "module::item"
		for alias, value := range ctx.NamedArgs {
			itemPath := fmt.Sprintf("%v", value)
			if !strings.Contains(itemPath, "::") {
				ctx.logger.CommandError(CatImport, "IMPORT", fmt.Sprintf("Aliased import must use module::item format: %s", itemPath), ctx.Position)
				return BoolStatus(false)
			}
			if !ctx.executor.ImportItemWithAlias(itemPath, alias) {
				ctx.logger.CommandError(CatImport, "IMPORT", fmt.Sprintf("Item not found: %s", itemPath), ctx.Position)
				return BoolStatus(false)
			}
		}

		// Handle positional arguments
		for _, arg := range ctx.Args {
			itemPath := fmt.Sprintf("%v", arg)

			// Check if it's a module name (no ::) or specific item (has ::)
			if !strings.Contains(itemPath, "::") {
				// Module name - import all items from the module
				if !ctx.executor.ImportModule(itemPath) {
					ctx.logger.CommandError(CatImport, "IMPORT", fmt.Sprintf("Module not found: %s", itemPath), ctx.Position)
					return BoolStatus(false)
				}
			} else {
				// Specific item - import just that item
				if !ctx.executor.ImportItem(itemPath) {
					ctx.logger.CommandError(CatImport, "IMPORT", fmt.Sprintf("Item not found: %s", itemPath), ctx.Position)
					return BoolStatus(false)
				}
			}
		}

		if len(ctx.Args) == 0 && len(ctx.NamedArgs) == 0 {
			ctx.logger.CommandError(CatImport, "IMPORT", "Usage: IMPORT <module> or IMPORT <module::item>, ...", ctx.Position)
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})
}
