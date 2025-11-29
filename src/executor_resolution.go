package pawscript

import (
	"fmt"
	"strings"
)

// UndefinedMarker is the special marker for undefined values
const UndefinedMarker = "\x00UNDEFINED\x00"

// resolveValue resolves any object marker (LIST/STRING/BLOCK) to its actual value
// If the value is not a marker, returns it unchanged
// This is the central resolution function - all resolution should go through here
func (e *Executor) resolveValue(value interface{}) interface{} {
	// Check if it's a Symbol that might be a marker
	if sym, ok := value.(Symbol); ok {
		str := string(sym)
		// Check for undefined marker - convert to bare undefined symbol
		if str == UndefinedMarker {
			return Symbol("undefined")
		}
		if objType, objID := parseObjectMarker(str); objID >= 0 {
			if actualValue, exists := e.getObject(objID); exists {
				e.logger.DebugCat(CatVariable,"Resolved %s marker %d to actual value", objType, objID)
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
		// Check for undefined marker - convert to bare undefined symbol
		if str == UndefinedMarker {
			return Symbol("undefined")
		}
		if objType, objID := parseObjectMarker(str); objID >= 0 {
			if actualValue, exists := e.getObject(objID); exists {
				e.logger.DebugCat(CatVariable,"Resolved %s marker %d to actual value", objType, objID)
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
// nolint:unused // Reserved for future use
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
// Also handles accessors like ~x.0 or ~x.key
// Returns the resolved value and success status
func (e *Executor) resolveTildeExpression(expr string, state *ExecutionState, substitutionCtx *SubstitutionContext, position *SourcePosition) (interface{}, bool) {
	if !strings.HasPrefix(expr, "~") {
		return nil, false
	}

	// Split off any accessors first
	base, accessors := splitAccessors(expr)
	rest := base[1:] // Remove the tilde from base

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
		innerValue, ok := e.resolveTildeExpression("~"+rest, state, substitutionCtx, position)
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
	if !exists {
		// Then, check for objects with matching name in module environment
		// If varName already starts with #, use it as-is; otherwise add # prefix
		objName := varName
		if !strings.HasPrefix(varName, "#") {
			objName = "#" + varName
		}
		if state.moduleEnv != nil {
			state.moduleEnv.mu.RLock()
			// ObjectsModule either points to Inherited or is a COW copy - just check Module
			if state.moduleEnv.ObjectsModule != nil {
				if obj, found := state.moduleEnv.ObjectsModule[objName]; found {
					value = obj
					exists = true
				}
			}
			state.moduleEnv.mu.RUnlock()
		}
	}

	if !exists {
		// Nothing found
		e.logger.CommandError(CatVariable, "", fmt.Sprintf("Variable not found: %s", varName), position)
		return nil, true
	}

	// Apply any accessors
	if accessors != "" {
		value = e.applyAccessorChain(value, accessors, position)
	}

	return value, true
}

// resolveQuestionExpression resolves a question expression like ?x or ?list.key
// Returns true if the variable/accessor chain exists, false otherwise
// Unlike resolveTildeExpression, this does not log errors for missing variables
func (e *Executor) resolveQuestionExpression(expr string, state *ExecutionState, substitutionCtx *SubstitutionContext, position *SourcePosition) bool {
	if !strings.HasPrefix(expr, "?") {
		return false
	}

	// Convert ?x to ~x for resolution, but we only care about existence
	tildeExpr := "~" + expr[1:]

	// Split off any accessors
	base, accessors := splitAccessors(tildeExpr)

	// Try to resolve the base variable (silently - don't log errors)
	resolved, exists := e.resolveTildeExpressionSilent(base, state, substitutionCtx)
	if !exists {
		// Variable doesn't exist
		return false
	}

	// Check if resolved value is undefined
	if sym, isSym := resolved.(Symbol); isSym {
		if string(sym) == UndefinedMarker || string(sym) == "undefined" {
			return false
		}
	}

	// If there are accessors, check if the chain exists
	if accessors != "" {
		return e.accessorChainExists(resolved, accessors)
	}

	return true
}

// resolveTildeExpressionSilent resolves a tilde expression without logging errors
// Also handles accessors like ~x.0 or ~x.key
// Returns the resolved value and whether it exists
func (e *Executor) resolveTildeExpressionSilent(expr string, state *ExecutionState, substitutionCtx *SubstitutionContext) (interface{}, bool) {
	if !strings.HasPrefix(expr, "~") {
		return nil, false
	}

	// Split off any accessors first
	base, accessors := splitAccessors(expr)
	rest := base[1:] // Remove the tilde from base

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
			varName = "true"
		}
	} else if strings.HasPrefix(rest, "\"") && strings.HasSuffix(rest, "\"") {
		varName = rest[1 : len(rest)-1]
	} else if strings.HasPrefix(rest, "'") && strings.HasSuffix(rest, "'") {
		varName = rest[1 : len(rest)-1]
	} else if strings.HasPrefix(rest, "~") {
		innerValue, ok := e.resolveTildeExpressionSilent("~"+rest, state, substitutionCtx)
		if !ok {
			return nil, false
		}
		varName = fmt.Sprintf("%v", innerValue)
	} else {
		varName = rest
	}

	// First, check local macro variables
	value, exists := state.GetVariable(varName)
	if !exists {
		// Then, check for objects with matching name in module environment
		objName := varName
		if !strings.HasPrefix(varName, "#") {
			objName = "#" + varName
		}
		if state.moduleEnv != nil {
			state.moduleEnv.mu.RLock()
			if state.moduleEnv.ObjectsModule != nil {
				if obj, found := state.moduleEnv.ObjectsModule[objName]; found {
					value = obj
					exists = true
				}
			}
			state.moduleEnv.mu.RUnlock()
		}
	}

	if !exists {
		// Nothing found - but don't log an error
		return nil, false
	}

	// Apply any accessors
	if accessors != "" {
		value = e.applyAccessorChain(value, accessors, nil)
	}

	return value, true
}

// resolveTildesInValue resolves any tilde or question expressions in a value
func (e *Executor) resolveTildesInValue(value interface{}, state *ExecutionState, substitutionCtx *SubstitutionContext, position *SourcePosition) interface{} {
	switch v := value.(type) {
	case Symbol:
		str := string(v)
		// Handle question expressions (existence check)
		if strings.HasPrefix(str, "?") {
			exists := e.resolveQuestionExpression(str, state, substitutionCtx, position)
			return exists
		}
		// Handle tilde expressions (value lookup)
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
		// Handle question expressions (existence check)
		if strings.HasPrefix(v, "?") {
			exists := e.resolveQuestionExpression(v, state, substitutionCtx, position)
			return exists
		}
		// Handle tilde expressions (value lookup)
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
