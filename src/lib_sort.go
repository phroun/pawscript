package pawscript

import (
	"fmt"
)

// sortItemsDefault sorts items using the default PawScript ordering:
// nil < false < true < numbers (low to high) < symbols (alpha) < strings (alpha) < other (original order)
// executor is optional - if provided, resolves object markers before categorizing
func sortItemsDefaultWithExecutor(items []interface{}, executor *Executor) {
	// Assign sort keys to preserve stable sort for "other" items
	type sortItem struct {
		value    interface{}
		origIdx  int
		category int // 0=nil, 1=false, 2=true, 3=number, 4=symbol, 5=string, 6=other
		numVal   float64
		strVal   string
	}

	sortItems := make([]sortItem, len(items))
	for i, item := range items {
		si := sortItem{value: item, origIdx: i, category: 6}

		// Resolve markers if executor available
		resolved := item
		if executor != nil {
			resolved = executor.resolveValue(item)
		}

		switch v := resolved.(type) {
		case nil:
			si.category = 0
		case bool:
			if v {
				si.category = 2
			} else {
				si.category = 1
			}
		case int:
			si.category = 3
			si.numVal = float64(v)
		case int64:
			si.category = 3
			si.numVal = float64(v)
		case float64:
			si.category = 3
			si.numVal = v
		case Symbol:
			// Check if it's an object marker for a string
			if markerType, _ := parseObjectMarker(string(v)); markerType == "string" {
				// Already resolved above, so this is a non-string symbol
				si.category = 4
				si.strVal = string(v)
			} else {
				si.category = 4
				si.strVal = string(v)
			}
		case QuotedString:
			si.category = 5
			si.strVal = string(v)
		case string:
			si.category = 5
			si.strVal = v
		}
		sortItems[i] = si
	}

	// Sort using insertion sort (stable)
	for i := 1; i < len(sortItems); i++ {
		key := sortItems[i]
		j := i - 1
		for j >= 0 && compareSortItems(sortItems[j], key) > 0 {
			sortItems[j+1] = sortItems[j]
			j--
		}
		sortItems[j+1] = key
	}

	// Copy back
	for i, si := range sortItems {
		items[i] = si.value
	}
}

// compareSortItems compares two sortItems, returns <0 if a<b, 0 if a==b, >0 if a>b
func compareSortItems(a, b struct {
	value    interface{}
	origIdx  int
	category int
	numVal   float64
	strVal   string
}) int {
	// Compare by category first
	if a.category != b.category {
		return a.category - b.category
	}

	// Within same category
	switch a.category {
	case 0, 1, 2: // nil, false, true - all equal within category
		return 0
	case 3: // numbers
		if a.numVal < b.numVal {
			return -1
		} else if a.numVal > b.numVal {
			return 1
		}
		return 0
	case 4, 5: // symbols, strings
		if a.strVal < b.strVal {
			return -1
		} else if a.strVal > b.strVal {
			return 1
		}
		return 0
	case 6: // other - preserve original order
		return a.origIdx - b.origIdx
	}
	return 0
}

// callComparator calls a comparator (macro/command) with two items and returns whether first < second
func callComparator(ps *PawScript, ctx *Context, comparator interface{}, a, b interface{}) (bool, error) {
	callArgs := []interface{}{a, b}
	childState := ctx.state.CreateChild()

	var result Result

	// Handle different comparator types (like call does)
	switch comp := comparator.(type) {
	case StoredCommand:
		cmdCtx := &Context{
			Args:      callArgs,
			NamedArgs: make(map[string]interface{}),
			Position:  ctx.Position,
			state:     childState,
			executor:  ctx.executor,
			logger:    ctx.logger,
		}
		result = comp.Handler(cmdCtx)

	case StoredMacro:
		result = ps.executor.ExecuteStoredMacro(&comp, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
			filename := ""
			lineOffset := 0
			columnOffset := 0
			if substCtx != nil {
				filename = substCtx.Filename
				lineOffset = substCtx.CurrentLineOffset
				columnOffset = substCtx.CurrentColumnOffset
			}
			return ps.executor.ExecuteWithState(commands, macroExecState, substCtx, filename, lineOffset, columnOffset)
		}, callArgs, make(map[string]interface{}), childState, ctx.Position, ctx.state)

	case Symbol:
		markerType, objectID := parseObjectMarker(string(comp))
		if markerType == "command" && objectID >= 0 {
			obj, exists := ctx.executor.getObject(objectID)
			if !exists {
				return false, fmt.Errorf("command object %d not found", objectID)
			}
			cmd, ok := obj.(StoredCommand)
			if !ok {
				return false, fmt.Errorf("object %d is not a command", objectID)
			}
			cmdCtx := &Context{
				Args:      callArgs,
				NamedArgs: make(map[string]interface{}),
				Position:  ctx.Position,
				state:     childState,
				executor:  ctx.executor,
				logger:    ctx.logger,
			}
			result = cmd.Handler(cmdCtx)
		} else if markerType == "macro" && objectID >= 0 {
			obj, exists := ctx.executor.getObject(objectID)
			if !exists {
				return false, fmt.Errorf("macro object %d not found", objectID)
			}
			macro, ok := obj.(StoredMacro)
			if !ok {
				return false, fmt.Errorf("object %d is not a macro", objectID)
			}
			result = ps.executor.ExecuteStoredMacro(&macro, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
				filename := ""
				lineOffset := 0
				columnOffset := 0
				if substCtx != nil {
					filename = substCtx.Filename
					lineOffset = substCtx.CurrentLineOffset
					columnOffset = substCtx.CurrentColumnOffset
				}
				return ps.executor.ExecuteWithState(commands, macroExecState, substCtx, filename, lineOffset, columnOffset)
			}, callArgs, make(map[string]interface{}), childState, ctx.Position, ctx.state)
		} else {
			// Treat as macro name - look up in module environment (COW - only check MacrosModule)
			name := string(comp)
			var macro *StoredMacro
			ctx.state.moduleEnv.mu.RLock()
			if m, exists := ctx.state.moduleEnv.MacrosModule[name]; exists && m != nil {
				macro = m
			}
			ctx.state.moduleEnv.mu.RUnlock()

			if macro == nil {
				return false, fmt.Errorf("macro \"%s\" not found", name)
			}

			result = ps.executor.ExecuteStoredMacro(macro, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
				filename := ""
				lineOffset := 0
				columnOffset := 0
				if substCtx != nil {
					filename = substCtx.Filename
					lineOffset = substCtx.CurrentLineOffset
					columnOffset = substCtx.CurrentColumnOffset
				}
				return ps.executor.ExecuteWithState(commands, macroExecState, substCtx, filename, lineOffset, columnOffset)
			}, callArgs, make(map[string]interface{}), childState, ctx.Position, ctx.state)
		}

	case ParenGroup:
		// Immediate macro (anonymous block)
		commands := string(comp)
		macroEnv := NewMacroModuleEnvironment(ctx.state.moduleEnv)
		macro := NewStoredMacroWithEnv(commands, ctx.Position, macroEnv)
		result = ps.executor.ExecuteStoredMacro(&macro, func(cmds string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
			filename := ""
			lineOffset := 0
			columnOffset := 0
			if substCtx != nil {
				filename = substCtx.Filename
				lineOffset = substCtx.CurrentLineOffset
				columnOffset = substCtx.CurrentColumnOffset
			}
			return ps.executor.ExecuteWithState(cmds, macroExecState, substCtx, filename, lineOffset, columnOffset)
		}, callArgs, make(map[string]interface{}), childState, ctx.Position, ctx.state)

	case string:
		// First check if it's a marker (from $1 substitution, etc.)
		markerType, objectID := parseObjectMarker(comp)
		if markerType == "command" && objectID >= 0 {
			obj, exists := ctx.executor.getObject(objectID)
			if !exists {
				return false, fmt.Errorf("command object %d not found", objectID)
			}
			cmd, ok := obj.(StoredCommand)
			if !ok {
				return false, fmt.Errorf("object %d is not a command", objectID)
			}
			cmdCtx := &Context{
				Args:      callArgs,
				NamedArgs: make(map[string]interface{}),
				Position:  ctx.Position,
				state:     childState,
				executor:  ctx.executor,
				logger:    ctx.logger,
			}
			result = cmd.Handler(cmdCtx)
		} else if markerType == "macro" && objectID >= 0 {
			obj, exists := ctx.executor.getObject(objectID)
			if !exists {
				return false, fmt.Errorf("macro object %d not found", objectID)
			}
			macro, ok := obj.(StoredMacro)
			if !ok {
				return false, fmt.Errorf("object %d is not a macro", objectID)
			}
			result = ps.executor.ExecuteStoredMacro(&macro, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
				filename := ""
				lineOffset := 0
				columnOffset := 0
				if substCtx != nil {
					filename = substCtx.Filename
					lineOffset = substCtx.CurrentLineOffset
					columnOffset = substCtx.CurrentColumnOffset
				}
				return ps.executor.ExecuteWithState(commands, macroExecState, substCtx, filename, lineOffset, columnOffset)
			}, callArgs, make(map[string]interface{}), childState, ctx.Position, ctx.state)
		} else {
			// Treat as macro name - look up in module environment (COW - only check MacrosModule)
			var macro *StoredMacro
			ctx.state.moduleEnv.mu.RLock()
			if m, exists := ctx.state.moduleEnv.MacrosModule[comp]; exists && m != nil {
				macro = m
			}
			ctx.state.moduleEnv.mu.RUnlock()

			if macro == nil {
				return false, fmt.Errorf("macro \"%s\" not found", comp)
			}

			result = ps.executor.ExecuteStoredMacro(macro, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
				filename := ""
				lineOffset := 0
				columnOffset := 0
				if substCtx != nil {
					filename = substCtx.Filename
					lineOffset = substCtx.CurrentLineOffset
					columnOffset = substCtx.CurrentColumnOffset
				}
				return ps.executor.ExecuteWithState(commands, macroExecState, substCtx, filename, lineOffset, columnOffset)
			}, callArgs, make(map[string]interface{}), childState, ctx.Position, ctx.state)
		}

	default:
		return false, fmt.Errorf("invalid comparator type: %T", comparator)
	}

	// Handle async result
	if token, isToken := result.(TokenResult); isToken {
		tokenID := string(token)
		waitChan := make(chan ResumeData, 1)
		ctx.executor.attachWaitChan(tokenID, waitChan)
		resumeData := <-waitChan
		return resumeData.Status, nil
	}

	// Use BoolStatus directly
	if boolRes, ok := result.(BoolStatus); ok {
		return bool(boolRes), nil
	}

	return false, nil
}
