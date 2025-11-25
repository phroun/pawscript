package pawscript

// PopulateOSModule creates the os module with script arguments as #args
// Creates: os::#args (StoredList containing script arguments)
func (env *ModuleEnvironment) PopulateOSModule(scriptArgs []string) {
	env.mu.Lock()
	defer env.mu.Unlock()

	// Create os module section if it doesn't exist
	if env.LibraryInherited["os"] == nil {
		env.LibraryInherited["os"] = make(ModuleSection)
	}
	osModule := env.LibraryInherited["os"]

	// Convert []string to []interface{} for StoredList
	argsItems := make([]interface{}, len(scriptArgs))
	for i, arg := range scriptArgs {
		argsItems[i] = arg
	}

	// Create StoredList for script arguments
	argsList := NewStoredList(argsItems)

	// Register #args in os module
	osModule["#args"] = &ModuleItem{Type: "object", Value: argsList}

	// Also update LibraryRestricted
	if env.LibraryRestricted["os"] == nil {
		env.LibraryRestricted["os"] = make(ModuleSection)
	}
	env.LibraryRestricted["os"]["#args"] = osModule["#args"]

	// Add to ObjectsInherited so it's accessible via tilde (~#args)
	// and auto-resolved in argc/argv without explicit IMPORT
	if env.ObjectsInherited == nil {
		env.ObjectsInherited = make(map[string]interface{})
	}
	env.ObjectsInherited["#args"] = argsList
}
