package pawscript

import (
	"fmt"
	"strings"
)

// UnpackTarget represents a single unpacking target with its type and names
type UnpackTarget struct {
	Type    string // "positional", "named_same", "named_different"
	Key     string // For named extractions
	VarName string // Variable name to store into
}

// parseAssignment checks if a command string is an assignment pattern (target: value)
// Returns target, value string, and whether it's an assignment
func (e *Executor) parseAssignment(commandStr string) (string, string, bool) {
	// Look for colon that's not inside quotes, braces, parens, or object markers
	inQuote := false
	var quoteChar rune
	braceDepth := 0
	parenDepth := 0

	runes := []rune(commandStr)
	for i := 0; i < len(runes); i++ {
		char := runes[i]

		// Handle escapes
		if char == '\\' && i+1 < len(runes) {
			i++
			continue
		}

		// Skip object markers (\x00...\x00) - they may contain colons
		if char == '\x00' {
			// Find the closing \x00
			for i++; i < len(runes) && runes[i] != '\x00'; i++ {
			}
			// i now points to closing \x00 (or end of string)
			continue
		}

		// Track quotes
		if !inQuote && (char == '"' || char == '\'') {
			inQuote = true
			quoteChar = char
			continue
		}
		if inQuote && char == quoteChar {
			inQuote = false
			continue
		}
		if inQuote {
			continue
		}

		// Track nesting
		if char == '{' {
			braceDepth++
			continue
		}
		if char == '}' {
			braceDepth--
			// Check if colon follows closing brace at depth 0
			if braceDepth == 0 && parenDepth == 0 && i+1 < len(runes) && runes[i+1] == ':' {
				target := strings.TrimSpace(string(runes[:i+1]))
				valueStr := strings.TrimSpace(string(runes[i+2:]))
				return target, valueStr, true
			}
			continue
		}
		if char == '(' {
			parenDepth++
			continue
		}
		if char == ')' {
			parenDepth--
			continue
		}

		// Check for colon at top level
		if char == ':' && braceDepth == 0 && parenDepth == 0 {
			target := strings.TrimSpace(string(runes[:i]))
			valueStr := strings.TrimSpace(string(runes[i+1:]))

			// Validate target is a valid assignment target
			if target == "" {
				return "", "", false
			}

			// Target can be: identifier, ~expr, "string", 'string', {braceExpr}, (unpack)
			// It cannot contain unquoted/unbraced spaces (that would be a command with args)
			if !isValidAssignmentTarget(target) {
				return "", "", false
			}

			return target, valueStr, true
		}
	}

	return "", "", false
}

// isValidAssignmentTarget checks if a string is a valid assignment target
// Valid targets: identifier, ~expr, "string", 'string', {braceExpr}, (unpack)
// Invalid: anything with unquoted spaces (like "echo foo")
func isValidAssignmentTarget(target string) bool {
	if target == "" {
		return false
	}

	inQuote := false
	var quoteChar rune
	braceDepth := 0
	parenDepth := 0

	for _, char := range target {
		// Handle quote tracking
		if !inQuote && (char == '"' || char == '\'') {
			inQuote = true
			quoteChar = char
			continue
		}
		if inQuote && char == quoteChar {
			inQuote = false
			continue
		}
		if inQuote {
			continue
		}

		// Track nesting
		if char == '{' {
			braceDepth++
			continue
		}
		if char == '}' {
			braceDepth--
			continue
		}
		if char == '(' {
			parenDepth++
			continue
		}
		if char == ')' {
			parenDepth--
			continue
		}

		// Skip object markers
		if char == '\x00' {
			continue
		}

		// Space at top level means this is a command, not an assignment
		if char == ' ' || char == '\t' {
			if braceDepth == 0 && parenDepth == 0 {
				return false
			}
		}
	}

	return true
}

// handleAssignment handles an assignment statement (target: value)
func (e *Executor) handleAssignment(target, valueStr string, state *ExecutionState, substitutionCtx *SubstitutionContext, position *SourcePosition) Result {
	// Check for unpacking pattern: (x, y, z): values
	if strings.HasPrefix(target, "(") && strings.HasSuffix(target, ")") {
		return e.handleUnpackingAssignment(target, valueStr, state, substitutionCtx, position)
	}

	// Resolve the target to get the variable name
	var varName string

	if strings.HasPrefix(target, "~") {
		// ~expr: value - resolve tilde to get variable name (or list of names for dynamic unpacking)
		resolved, ok := e.resolveTildeExpression(target, state, substitutionCtx, position)
		if !ok {
			return BoolStatus(false)
		}

		// Resolve any object markers to actual values
		resolved = e.resolveValue(resolved)

		// Check if the resolved value is a list - this means dynamic unpacking
		if storedList, ok := resolved.(StoredList); ok {
			return e.handleDynamicUnpackingAssignment(storedList.Items(), valueStr, state, substitutionCtx, position)
		}

		varName = fmt.Sprintf("%v", resolved)
	} else if strings.HasPrefix(target, "{") && strings.HasSuffix(target, "}") {
		// {expr}: value - evaluate brace to get variable name
		braceContent := target[1 : len(target)-1]
		braceState := NewExecutionStateFromSharedVars(state)

		result := e.ExecuteWithState(braceContent, braceState, substitutionCtx,
			substitutionCtx.Filename, substitutionCtx.CurrentLineOffset, substitutionCtx.CurrentColumnOffset)

		if boolStatus, ok := result.(BoolStatus); ok && !bool(boolStatus) {
			return BoolStatus(false)
		}

		if braceState.HasResult() {
			varName = fmt.Sprintf("%v", braceState.GetResult())
		} else {
			e.logger.CommandError(CatVariable, "", "Brace expression for assignment target produced no result", position)
			return BoolStatus(false)
		}
	} else if strings.HasPrefix(target, "\"") && strings.HasSuffix(target, "\"") {
		// "varname": value - quoted variable name
		varName = target[1 : len(target)-1]
	} else if strings.HasPrefix(target, "'") && strings.HasSuffix(target, "'") {
		// 'varname': value - single-quoted variable name
		varName = target[1 : len(target)-1]
	} else {
		// bare identifier
		varName = target
	}

	// Parse and resolve the value
	var value interface{}

	if valueStr == "" {
		value = nil
	} else {
		// Parse the value string as arguments
		args, _ := parseArguments(valueStr)
		if len(args) == 0 {
			value = nil
		} else if len(args) == 1 {
			value = args[0]
		} else {
			// Multiple values - create a list? Or use first?
			// For now, use first value (matching old set behavior)
			value = args[0]
		}

		// Resolve tildes in the value
		value = e.resolveTildesInValue(value, state, substitutionCtx, position)
	}

	// Check for undefined - delete variable instead of setting
	// Don't set result - "undefined" means no value, so leave previous result intact
	if sym, ok := value.(Symbol); ok && string(sym) == "undefined" {
		state.DeleteVariable(varName)
		return BoolStatus(true)
	}

	// Assign and set the formal result to the assigned value
	state.SetVariable(varName, value)
	state.SetResult(value)
	return BoolStatus(true)
}

// parseUnpackTargets parses unpacking patterns like (x:, z, r: radius)
// Returns a slice of UnpackTarget describing each extraction
// Note: parseArguments treats "x:" as a named arg (namedArgs["x"] = nil)
// and "x: y" as (namedArgs["x"] = "y"), which tells us about the colon syntax,
// but we need to preserve order of all targets (positional and named interleaved)
func parseUnpackTargets(targetContent string) []UnpackTarget {
	var targets []UnpackTarget

	// Parse to understand what's positional vs named
	positionalArgs, _ := parseArguments(targetContent)

	// Split by commas manually to preserve order
	var orderedItems []string
	var currentItem strings.Builder
	inQuotes := false
	quoteChar := rune(0)
	parenCount := 0
	braceCount := 0

	for i, ch := range targetContent {
		if (ch == '"' || ch == '\'') && (i == 0 || targetContent[i-1] != '\\') {
			if !inQuotes {
				inQuotes = true
				quoteChar = ch
			} else if ch == quoteChar {
				inQuotes = false
			}
			currentItem.WriteRune(ch)
			continue
		}

		if !inQuotes && ch == '(' {
			parenCount++
			currentItem.WriteRune(ch)
			continue
		}

		if !inQuotes && ch == ')' {
			parenCount--
			currentItem.WriteRune(ch)
			continue
		}

		if !inQuotes && ch == '{' {
			braceCount++
			currentItem.WriteRune(ch)
			continue
		}

		if !inQuotes && ch == '}' {
			braceCount--
			currentItem.WriteRune(ch)
			continue
		}

		if !inQuotes && parenCount == 0 && braceCount == 0 && ch == ',' {
			orderedItems = append(orderedItems, currentItem.String())
			currentItem.Reset()
			continue
		}

		currentItem.WriteRune(ch)
	}
	if currentItem.Len() > 0 {
		orderedItems = append(orderedItems, currentItem.String())
	}

	// Now build targets in order by checking each item
	targets = nil // Reset to build in order
	positionalIndex := 0

	for _, item := range orderedItems {
		itemTrimmed := strings.TrimSpace(item)

		// Check if this item contains a colon (is a named arg)
		hasColon := false
		inQuotes := false
		quoteChar := rune(0)
		for i, ch := range itemTrimmed {
			if (ch == '"' || ch == '\'') && (i == 0 || itemTrimmed[i-1] != '\\') {
				if !inQuotes {
					inQuotes = true
					quoteChar = ch
				} else if ch == quoteChar {
					inQuotes = false
				}
			}
			if !inQuotes && ch == ':' {
				hasColon = true
				break
			}
		}

		if hasColon {
			// It's a named arg - find it in namedArgs map
			// Extract the key (everything before the colon)
			colonIdx := strings.Index(itemTrimmed, ":")
			key := strings.TrimSpace(itemTrimmed[:colonIdx])

			// Check if there's a value after the colon
			if colonIdx == len(itemTrimmed)-1 || strings.TrimSpace(itemTrimmed[colonIdx+1:]) == "" {
				// named_same: "x:" means extract named arg "x" into var "x"
				targets = append(targets, UnpackTarget{
					Type:    "named_same",
					Key:     key,
					VarName: key,
				})
			} else {
				// named_different: "x: y" means extract named arg "x" into var "y"
				varName := strings.TrimSpace(itemTrimmed[colonIdx+1:])
				targets = append(targets, UnpackTarget{
					Type:    "named_different",
					Key:     key,
					VarName: varName,
				})
			}
		} else {
			// It's a positional arg
			if positionalIndex < len(positionalArgs) {
				varName := strings.TrimSpace(fmt.Sprintf("%v", positionalArgs[positionalIndex]))
				targets = append(targets, UnpackTarget{
					Type:    "positional",
					VarName: varName,
				})
				positionalIndex++
			}
		}
	}

	return targets
}

// handleUnpackingAssignment handles unpacking assignment: (x, y, z): listValue
// Supports both positional and named argument extraction
// Examples:
//   (x, y, z): list        - positional extraction
//   (x:, y:): list         - named extraction (x: and y:)
//   (name:, age, id: uid): list - mixed (name:, positional age, id->uid)
func (e *Executor) handleUnpackingAssignment(target, valueStr string, state *ExecutionState, substitutionCtx *SubstitutionContext, position *SourcePosition) Result {
	// Parse unpacking targets from pattern
	targetContent := target[1 : len(target)-1]
	unpackTargets := parseUnpackTargets(targetContent)

	// Check if we have any named targets
	hasNamed := false
	for _, t := range unpackTargets {
		if t.Type == "named_same" || t.Type == "named_different" {
			hasNamed = true
			break
		}
	}

	if !hasNamed {
		// Pure positional - use old code path for compatibility
		varNames := make([]interface{}, len(unpackTargets))
		for i, t := range unpackTargets {
			varNames[i] = t.VarName
		}
		return e.handleDynamicUnpackingAssignment(varNames, valueStr, state, substitutionCtx, position)
	}

	// Has named targets - use new logic
	return e.handleUnpackingAssignmentWithNames(unpackTargets, valueStr, state, substitutionCtx, position)
}

// handleUnpackingAssignmentWithNames handles unpacking with both positional and named arguments
func (e *Executor) handleUnpackingAssignmentWithNames(unpackTargets []UnpackTarget, valueStr string, state *ExecutionState, substitutionCtx *SubstitutionContext, position *SourcePosition) Result {
	// Parse and resolve the single list value
	if valueStr == "" {
		// Empty value - set all variables to nil
		for _, target := range unpackTargets {
			state.SetVariable(target.VarName, nil)
		}
		return BoolStatus(true)
	}

	// Parse as a single argument (the list)
	args, _ := parseArguments(valueStr)
	if len(args) == 0 {
		// No value - set all variables to nil
		for _, target := range unpackTargets {
			state.SetVariable(target.VarName, nil)
		}
		return BoolStatus(true)
	}

	// Take the first (and should be only) argument
	listArg := args[0]

	// Resolve tildes
	resolved := e.resolveTildesInValue(listArg, state, substitutionCtx, position)

	// Resolve any object markers (like \x00LIST:1\x00) to actual values
	resolved = e.resolveValue(resolved)

	// Extract items from the list-like value
	var positionalItems []interface{}
	namedItems := make(map[string]interface{})

	if storedList, ok := resolved.(StoredList); ok {
		// StoredList stores positional and named separately
		positionalItems = storedList.Items()
		if storedList.NamedArgs() != nil {
			namedItems = storedList.NamedArgs()
		}
	} else if parenGroup, ok := resolved.(ParenGroup); ok {
		// Parse paren group contents - returns positional and named separately
		positionalItems, namedItems = parseArguments(string(parenGroup))
	} else {
		// Not a list - treat as single positional value
		positionalItems = []interface{}{resolved}
	}

	// Now extract values according to unpack targets
	positionalIndex := 0
	for _, target := range unpackTargets {
		switch target.Type {
		case "positional":
			// Extract next positional item
			if positionalIndex < len(positionalItems) {
				state.SetVariable(target.VarName, positionalItems[positionalIndex])
				positionalIndex++
			} else {
				// Not enough positional values
				state.SetVariable(target.VarName, nil)
			}

		case "named_same", "named_different":
			// Extract named item by key
			if value, exists := namedItems[target.Key]; exists {
				state.SetVariable(target.VarName, value)
			} else {
				// Named item not found
				state.SetVariable(target.VarName, nil)
			}
		}
	}

	// Set the formal result to the unpacked value
	state.SetResult(resolved)
	return BoolStatus(true)
}

// handleDynamicUnpackingAssignment handles unpacking where variable names are provided as a slice
func (e *Executor) handleDynamicUnpackingAssignment(varNames []interface{}, valueStr string, state *ExecutionState, substitutionCtx *SubstitutionContext, position *SourcePosition) Result {
	// Parse and resolve the single list value
	var values []interface{}
	var resolved interface{} // Track the resolved value for setting the result

	if valueStr == "" {
		values = nil
		resolved = nil
	} else {
		// Parse as a single argument (the list)
		args, _ := parseArguments(valueStr)
		if len(args) == 0 {
			values = nil
			resolved = nil
		} else {
			// Take the first (and should be only) argument
			listArg := args[0]

			// Resolve tildes
			resolved = e.resolveTildesInValue(listArg, state, substitutionCtx, position)

			// Resolve any object markers (like \x00LIST:1\x00) to actual values
			resolved = e.resolveValue(resolved)

			// Extract items from the list-like value
			if storedList, ok := resolved.(StoredList); ok {
				values = storedList.Items()
			} else if parenGroup, ok := resolved.(ParenGroup); ok {
				// Parse paren group contents as values
				values, _ = parseArguments(string(parenGroup))
			} else {
				// Not a list - single value unpacking
				values = []interface{}{resolved}
			}
		}
	}

	// Set each variable to its corresponding value
	for i, varNameInterface := range varNames {
		varName := fmt.Sprintf("%v", varNameInterface)

		if i < len(values) {
			state.SetVariable(varName, values[i])
		} else {
			// Not enough values - set to nil
			state.SetVariable(varName, nil)
		}
	}

	// Set the formal result to the unpacked value
	state.SetResult(resolved)
	return BoolStatus(true)
}
