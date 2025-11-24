package pawscript

import (
	"fmt"
	"strings"
)

// resolveValue resolves any object marker (LIST/STRING/BLOCK) to its actual value
// If the value is not a marker, returns it unchanged
// This is the central resolution function - all resolution should go through here
func (e *Executor) resolveValue(value interface{}) interface{} {
	// Check if it's a Symbol that might be a marker
	if sym, ok := value.(Symbol); ok {
		if objType, objID := parseObjectMarker(string(sym)); objID >= 0 {
			if actualValue, exists := e.getObject(objID); exists {
				e.logger.Debug("Resolved %s marker %d to actual value", objType, objID)
				// Convert stored types back to their original forms
				switch v := actualValue.(type) {
				case StoredString:
					return string(v)
				case StoredBlock:
					return ParenGroup(v)
				default:
					return actualValue
				}
			}
		}
	}

	// Check if it's a string that might be a marker
	if str, ok := value.(string); ok {
		if objType, objID := parseObjectMarker(str); objID >= 0 {
			if actualValue, exists := e.getObject(objID); exists {
				e.logger.Debug("Resolved %s marker %d to actual value", objType, objID)
				// Convert stored types back to their original forms
				switch v := actualValue.(type) {
				case StoredString:
					return string(v)
				case StoredBlock:
					return ParenGroup(v)
				default:
					return actualValue
				}
			}
		}
	}

	// Not a marker, return as-is
	return value
}

// resolveValueDeep recursively resolves markers, including nested structures
// Use this when you need to resolve markers within lists
func (e *Executor) resolveValueDeep(value interface{}) interface{} {
	resolved := e.resolveValue(value)

	// If it resolved to a list, recursively resolve its items
	if list, ok := resolved.(StoredList); ok {
		items := list.Items()
		resolvedItems := make([]interface{}, len(items))
		hasChanges := false

		for i, item := range items {
			resolvedItem := e.resolveValueDeep(item)
			resolvedItems[i] = resolvedItem
			if resolvedItem != item {
				hasChanges = true
			}
		}

		if hasChanges {
			return NewStoredList(resolvedItems)
		}
	}

	return resolved
}

// resolveTildeExpression resolves a tilde expression like ~x or ~"varname" or ~{expr}
// Returns the resolved value and success status
func (e *Executor) resolveTildeExpression(expr string, state *ExecutionState, substitutionCtx *SubstitutionContext, position *SourcePosition) (interface{}, bool) {
	if !strings.HasPrefix(expr, "~") {
		return nil, false
	}

	rest := expr[1:] // Remove the tilde

	var varName string

	if strings.HasPrefix(rest, "{") && strings.HasSuffix(rest, "}") {
		// ~{expr} - evaluate brace expression to get variable name
		braceContent := rest[1 : len(rest)-1]
		braceState := NewExecutionStateFromSharedVars(state)

		result := e.ExecuteWithState(braceContent, braceState, substitutionCtx,
			substitutionCtx.Filename, substitutionCtx.CurrentLineOffset, substitutionCtx.CurrentColumnOffset)

		if boolStatus, ok := result.(BoolStatus); ok && !bool(boolStatus) {
			return nil, false
		}

		if braceState.HasResult() {
			varName = fmt.Sprintf("%v", braceState.GetResult())
		} else {
			varName = "true" // Default result of successful command
		}
	} else if strings.HasPrefix(rest, "\"") && strings.HasSuffix(rest, "\"") {
		// ~"varname" - quoted variable name
		varName = rest[1 : len(rest)-1]
	} else if strings.HasPrefix(rest, "'") && strings.HasSuffix(rest, "'") {
		// ~'varname' - single-quoted variable name
		varName = rest[1 : len(rest)-1]
	} else if strings.HasPrefix(rest, "~") {
		// ~~x - chained tilde (resolve x, use result as varname, resolve that)
		innerValue, ok := e.resolveTildeExpression(rest, state, substitutionCtx, position)
		if !ok {
			return nil, false
		}
		varName = fmt.Sprintf("%v", innerValue)
	} else {
		// ~identifier - bare variable name
		varName = rest
	}

	// First, check local macro variables
	value, exists := state.GetVariable(varName)
	if exists {
		return value, true
	}

	// Then, check for objects with matching name in module environment
	// If varName already starts with #, use it as-is; otherwise add # prefix
	objName := varName
	if !strings.HasPrefix(varName, "#") {
		objName = "#" + varName
	}
	if state.moduleEnv != nil {
		state.moduleEnv.mu.RLock()
		// Check ObjectsModule first (local overrides)
		if state.moduleEnv.ObjectsModule != nil {
			if obj, exists := state.moduleEnv.ObjectsModule[objName]; exists {
				state.moduleEnv.mu.RUnlock()
				return obj, true
			}
		}
		// Fall back to ObjectsInherited (inherited from parent)
		if state.moduleEnv.ObjectsInherited != nil {
			if obj, exists := state.moduleEnv.ObjectsInherited[objName]; exists {
				state.moduleEnv.mu.RUnlock()
				return obj, true
			}
		}
		state.moduleEnv.mu.RUnlock()
	}

	// Nothing found
	e.logger.CommandError(CatVariable, "", fmt.Sprintf("Variable not found: %s", varName), position)
	return nil, true
}

// resolveTildesInValue resolves any tilde expressions in a value
func (e *Executor) resolveTildesInValue(value interface{}, state *ExecutionState, substitutionCtx *SubstitutionContext, position *SourcePosition) interface{} {
	switch v := value.(type) {
	case Symbol:
		str := string(v)
		if strings.HasPrefix(str, "~") {
			resolved, ok := e.resolveTildeExpression(str, state, substitutionCtx, position)
			if ok {
				return resolved
			}
			// Resolution failed, return as-is (error already logged)
			return value
		}
		return value
	case string:
		if strings.HasPrefix(v, "~") {
			resolved, ok := e.resolveTildeExpression(v, state, substitutionCtx, position)
			if ok {
				return resolved
			}
			return value
		}
		return value
	default:
		return value
	}
}
