package pawscript

import (
	"fmt"
	"strings"
)

// Module system - modules contain named items that can be imported individually
// e.g., stdlib module contains: macro, call, macro_list, etc.
// Import with "stdlib::macro" to import the macro command

// moduleItems stores module -> item name -> handler mapping
var moduleItems = make(map[string]map[string]Handler)

// importedItems tracks which items have been imported
var importedItemsMap = make(map[string]bool)

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

// ImportItem imports a single item from a module (e.g., "stdlib::macro")
// Returns true if successful, false if item not found
func (e *Executor) ImportItem(fullPath string) bool {
	// Parse "module::item" format
	parts := strings.SplitN(fullPath, "::", 2)
	if len(parts) != 2 {
		return false
	}
	moduleName := parts[0]
	itemName := parts[1]

	e.mu.Lock()

	// Check if already imported
	if importedItemsMap[fullPath] {
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

	// Mark as imported
	importedItemsMap[fullPath] = true
	e.mu.Unlock()

	// Register the command
	e.RegisterCommand(itemName, handler)
	e.logger.Debug("Imported item: %s (registered as command '%s')", fullPath, itemName)
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
	// Usage: import "stdlib::macro"              - import single item
	//        import "stdlib::macro", "stdlib::call" - import multiple items
	ps.executor.RegisterCommand("import", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.logger.CommandError(CatImport, "IMPORT", "Usage: import <module::item>, ...", ctx.Position)
			return BoolStatus(false)
		}

		for _, arg := range ctx.Args {
			itemPath := fmt.Sprintf("%v", arg)
			if !ctx.executor.ImportItem(itemPath) {
				ctx.logger.CommandError(CatImport, "IMPORT", fmt.Sprintf("Item not found: %s", itemPath), ctx.Position)
				return BoolStatus(false)
			}
		}

		return BoolStatus(true)
	})
}
