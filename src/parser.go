package pawscript

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// ScopeMarker is the special marker for scope operator ::
// Used to distinguish module::item from named argument syntax
const ScopeMarker = "\x00SCOPE\x00"

// SourceMap maps transformed positions to original positions
type SourceMap struct {
	Filename              string
	OriginalLines         []string
	TransformedToOriginal map[int]*SourcePosition
}

// NewSourceMap creates a new source map
func NewSourceMap(source, filename string) *SourceMap {
	return &SourceMap{
		Filename:              filename,
		OriginalLines:         strings.Split(source, "\n"),
		TransformedToOriginal: make(map[int]*SourcePosition),
	}
}

// AddMapping adds a position mapping
func (sm *SourceMap) AddMapping(transformedPos int, originalPos *SourcePosition) {
	if originalPos.Filename == "" && sm.Filename != "" {
		originalPos.Filename = sm.Filename
	}
	sm.TransformedToOriginal[transformedPos] = originalPos
}

// GetOriginalPosition gets the original position for a transformed position
func (sm *SourceMap) GetOriginalPosition(transformedPos int) *SourcePosition {
	return sm.TransformedToOriginal[transformedPos]
}

// Parser handles parsing with position tracking
type Parser struct {
	sourceMap *SourceMap
}

// NewParser creates a new parser
func NewParser(source, filename string) *Parser {
	return &Parser{
		sourceMap: NewSourceMap(source, filename),
	}
}

// RemoveComments removes comments while preserving position mapping
func (p *Parser) RemoveComments(source string) string {
	var result strings.Builder
	originalLine := 1
	originalColumn := 1
	resultPosition := 0

	runes := []rune(source)
	i := 0
	length := len(runes)

	for i < length {
		char := runes[i]

		startPos := &SourcePosition{
			Line:     originalLine,
			Column:   originalColumn,
			Length:   1,
			Filename: p.sourceMap.Filename,
		}

		// Handle newlines
		if char == '\n' {
			result.WriteRune(char)
			p.sourceMap.AddMapping(resultPosition, startPos)
			resultPosition++
			originalLine++
			originalColumn = 1
			i++
			continue
		}

		// Handle escape sequences
		if char == '\\' && i+1 < length {
			escapeSeq := string(runes[i : i+2])
			result.WriteString(escapeSeq)

			// Create mapping for the backslash
			pos1 := &SourcePosition{
				Line:         originalLine,
				Column:       originalColumn,
				Length:       1,
				OriginalText: string(char),
				Filename:     p.sourceMap.Filename,
			}
			p.sourceMap.AddMapping(resultPosition, pos1)
			resultPosition++

			// Create mapping for the escaped character
			pos2 := &SourcePosition{
				Line:         originalLine,
				Column:       originalColumn + 1,
				Length:       1,
				OriginalText: string(runes[i+1]),
				Filename:     p.sourceMap.Filename,
			}
			p.sourceMap.AddMapping(resultPosition, pos2)
			resultPosition++

			originalColumn += 2
			i += 2
			continue
		}

		// Handle quoted strings - skip comment processing inside quotes
		if char == '"' || char == '\'' {
			quoteChar := char
			result.WriteRune(char)
			p.sourceMap.AddMapping(resultPosition, startPos)
			resultPosition++
			originalColumn++
			i++

			// Find end of quoted string
			for i < length {
				quoteCharRune := runes[i]
				quotePos := &SourcePosition{
					Line:     originalLine,
					Column:   originalColumn,
					Length:   1,
					Filename: p.sourceMap.Filename,
				}

				if quoteCharRune == '\n' {
					originalLine++
					originalColumn = 1
				} else {
					originalColumn++
				}

				result.WriteRune(quoteCharRune)
				p.sourceMap.AddMapping(resultPosition, quotePos)
				resultPosition++

				if quoteCharRune == '\\' && i+1 < length {
					nextChar := runes[i+1]
					result.WriteRune(nextChar)
					nextPos := &SourcePosition{
						Line:     originalLine,
						Column:   originalColumn,
						Length:   1,
						Filename: p.sourceMap.Filename,
					}
					p.sourceMap.AddMapping(resultPosition, nextPos)
					resultPosition++

					if nextChar == '\n' {
						originalLine++
						originalColumn = 1
					} else {
						originalColumn++
					}
					i += 2
				} else if quoteCharRune == quoteChar {
					i++
					break
				} else {
					i++
				}
			}
			continue
		}

		// Handle comments starting with #
		if char == '#' {
			// Check for block comments #( ... )# or #{ ... }#
			if i+1 < length {
				nextChar := runes[i+1]

				if nextChar == '(' || nextChar == '{' {
					// Found block comment start
					openBrace := nextChar
					var closeBrace rune
					if openBrace == '(' {
						closeBrace = ')'
					} else {
						closeBrace = '}'
					}

					depth := 1
					j := i + 2
					tempLine := originalLine
					tempColumn := originalColumn + 2

					// Find matching closing }# or )#
					for j < length && depth > 0 {
						c := runes[j]

						if c == '\n' {
							tempLine++
							tempColumn = 1
							j++
							continue
						}

						if c == '\\' && j+1 < length {
							j += 2
							tempColumn += 2
							continue
						}

						if c == '"' {
							j++
							tempColumn++
							for j < length && runes[j] != '"' {
								if runes[j] == '\\' && j+1 < length {
									j += 2
									tempColumn += 2
								} else {
									if runes[j] == '\n' {
										tempLine++
										tempColumn = 1
									} else {
										tempColumn++
									}
									j++
								}
							}
							if j < length && runes[j] == '"' {
								j++
								tempColumn++
							}
							continue
						}

						if c == '#' && j+1 < length && runes[j+1] == openBrace {
							depth++
							j += 2
							tempColumn += 2
							continue
						}

						if c == closeBrace && j+1 < length && runes[j+1] == '#' {
							depth--
							if depth == 0 {
								i = j + 2
								originalLine = tempLine
								originalColumn = tempColumn + 2
								break
							}
							j += 2
							tempColumn += 2
							continue
						}

						j++
						tempColumn++
					}

					if depth == 0 {
						continue
					}
					// Unclosed block comment - treat # as regular character
				}
			}

			// Check for line comments
			isAtStart := originalColumn == 1
			isPrecededByWhitespace := i > 0 && unicode.IsSpace(runes[i-1])
			isValidCommentStart := isAtStart || isPrecededByWhitespace

			if isValidCommentStart {
				// Line comment if followed by whitespace, end of line, or ! (for shebangs)
				isFollowedByWhitespaceEndOrBang := i+1 >= length || unicode.IsSpace(runes[i+1]) || runes[i+1] == '!'

				if isFollowedByWhitespaceEndOrBang {
					// Line comment - skip to end of line
					for i < length && runes[i] != '\n' {
						i++
						originalColumn++
					}
					continue
				}
			}
		}

		// Regular character
		result.WriteRune(char)
		p.sourceMap.AddMapping(resultPosition, startPos)
		resultPosition++
		originalColumn++
		i++
	}

	return result.String()
}

// ParseCommandSequence parses a command string into individual commands
func (p *Parser) ParseCommandSequence(commandStr string) ([]*ParsedCommand, error) {
	var commands []*ParsedCommand
	var currentCommand strings.Builder
	nestingDepth := 0
	inQuote := false
	var quoteChar rune

	line := 1
	column := 1
	commandStartLine := 1
	commandStartColumn := 1
	commandStartPos := 0 // Track position in the string for source map lookup
	currentSeparator := "none"
	nextChainType := "none" // Track chain type for NEXT command

	runes := []rune(commandStr)
	i := 0

	addCommand := func(cmd string, separator string, endLine, endCol int, startPos int) {
		trimmed := strings.TrimSpace(cmd)
		if trimmed != "" {
			// Get position from source map using the start position
			pos := p.sourceMap.GetOriginalPosition(startPos)

			if pos == nil {
				pos = &SourcePosition{
					Line:     commandStartLine,
					Column:   commandStartColumn,
					Length:   len(trimmed),
					Filename: p.sourceMap.Filename,
				}
			} else {
				// Use the source map position but update length
				pos.Length = len(trimmed)
			}

			commands = append(commands, &ParsedCommand{
				Command:      trimmed,
				Arguments:    []interface{}{},
				Position:     pos,
				OriginalLine: "",
				Separator:    separator,
				ChainType:    nextChainType, // Use tracked chain type
			})

			// Reset chain type after using it
			nextChainType = "none"
		}
		currentCommand.Reset()
		commandStartLine = endLine
		commandStartColumn = endCol
		// Don't update commandStartPos here - it will be set when first char is added
	}

	for i < len(runes) {
		char := runes[i]

		// Track start position when we add first non-whitespace character
		if strings.TrimSpace(currentCommand.String()) == "" && !unicode.IsSpace(char) && char != '\n' {
			commandStartPos = i
		}

		// Handle escape sequences
		if char == '\\' && i+1 < len(runes) {
			currentCommand.WriteRune(char)
			currentCommand.WriteRune(runes[i+1])
			i += 2
			column += 2
			continue
		}

		// Handle quotes
		if !inQuote && (char == '"' || char == '\'') {
			inQuote = true
			quoteChar = char
			currentCommand.WriteRune(char)
			i++
			column++
			continue
		}

		if inQuote && char == quoteChar {
			inQuote = false
			quoteChar = 0
			currentCommand.WriteRune(char)
			i++
			column++
			continue
		}

		if inQuote {
			currentCommand.WriteRune(char)
			if char == '\n' {
				line++
				column = 1
			} else {
				column++
			}
			i++
			continue
		}

		// Track nesting depth
		if char == '(' || char == '{' {
			nestingDepth++
			currentCommand.WriteRune(char)
			i++
			column++
			continue
		}

		if char == ')' || char == '}' {
			nestingDepth--
			currentCommand.WriteRune(char)
			i++
			column++
			continue
		}

		if nestingDepth > 0 {
			currentCommand.WriteRune(char)
			if char == '\n' {
				line++
				column = 1
			} else {
				column++
			}
			i++
			continue
		}

		// Handle separators at top level
		// Check for three-character operator first: ~~>
		if char == '~' && i+2 < len(runes) && runes[i+1] == '~' && runes[i+2] == '>' {
			addCommand(currentCommand.String(), currentSeparator, line, column+3, commandStartPos)
			currentSeparator = ";"
			// Mark that NEXT command needs chain_append injection
			nextChainType = "chain_append"
			i += 3
			column += 3
			continue
		}

		// Check for two-character operators: ~> and =>
		if char == '~' && i+1 < len(runes) && runes[i+1] == '>' {
			addCommand(currentCommand.String(), currentSeparator, line, column+2, commandStartPos)
			currentSeparator = ";"
			// Mark that NEXT command needs chain injection
			nextChainType = "chain"
			i += 2
			column += 2
			continue
		}

		if char == '=' && i+1 < len(runes) && runes[i+1] == '>' {
			addCommand(currentCommand.String(), currentSeparator, line, column+2, commandStartPos)
			currentSeparator = ";"
			// Mark that NEXT command needs assign injection
			nextChainType = "assign"
			i += 2
			column += 2
			continue
		}

		if char == ';' {
			addCommand(currentCommand.String(), currentSeparator, line, column+1, commandStartPos)
			currentSeparator = ";"
			i++
			column++
			continue
		}

		if char == '&' {
			addCommand(currentCommand.String(), currentSeparator, line, column+1, commandStartPos)
			currentSeparator = "&"
			i++
			column++
			continue
		}

		if char == '|' {
			addCommand(currentCommand.String(), currentSeparator, line, column+1, commandStartPos)
			currentSeparator = "|"
			i++
			column++
			continue
		}

		// Handle newlines
		if char == '\n' {
			if strings.TrimSpace(currentCommand.String()) != "" {
				addCommand(currentCommand.String(), currentSeparator, line+1, 1, commandStartPos)
				currentSeparator = ";"
			}
			line++
			column = 1
			i++
			continue
		}

		// Regular character
		currentCommand.WriteRune(char)
		i++
		column++
	}

	// Check for unclosed quotes
	if inQuote {
		pos := &SourcePosition{
			Line:     line,
			Column:   column,
			Length:   1,
			Filename: p.sourceMap.Filename,
		}
		return nil, &PawScriptError{
			Message:  fmt.Sprintf("Unclosed quote: missing closing %c", quoteChar),
			Position: pos,
			Context:  p.sourceMap.OriginalLines,
		}
	}

	// Handle final command
	if strings.TrimSpace(currentCommand.String()) != "" {
		addCommand(currentCommand.String(), currentSeparator, line, column, commandStartPos)
	}

	// Post-process commands to apply chain operators
	return p.applyChainOperators(commands)
}

// ParseCommand parses a single command into name and arguments
func ParseCommand(commandStr string) (string, []interface{}, map[string]interface{}) {
	commandStr = strings.TrimSpace(commandStr)
	if commandStr == "" {
		return "", nil, nil
	}

	// Find command end
	commandEnd := -1
	inQuote := false
	var quoteChar rune

	runes := []rune(commandStr)
	for i, char := range runes {
		if char == '\\' && i+1 < len(runes) {
			continue
		}

		if !inQuote && (char == '"' || char == '\'') {
			inQuote = true
			quoteChar = char
			continue
		}

		if inQuote && char == quoteChar {
			inQuote = false
			quoteChar = 0
			continue
		}

		if !inQuote && (char == ' ' || char == '\t') {
			commandEnd = i
			break
		}
	}

	if commandEnd == -1 {
		return commandStr, nil, nil
	}

	command := string(runes[:commandEnd])
	argsStr := strings.TrimSpace(string(runes[commandEnd:]))

	if argsStr == "" {
		return command, nil, nil
	}

	args, namedArgs := parseArguments(argsStr)
	return command, args, namedArgs
}

// argUnitType represents the type of a parsed argument unit
type argUnitType int

const (
	unitNone argUnitType = iota
	unitString
	unitNumber
	unitSymbol
	unitNil
	unitBool
	unitParen
	unitBrace
	unitComplex // object markers, etc.
)

// parseArguments parses argument string into slice of positional args and named args
// Implements concatenation rules for adjacent units without commas
func parseArguments(argsStr string) ([]interface{}, map[string]interface{}) {
	var args []interface{}
	namedArgs := make(map[string]interface{})

	runes := []rune(argsStr)
	i := 0

	// State for combination rules
	var currentValue interface{}
	var currentType argUnitType
	var potentialString bool
	var originalItem interface{}
	var lastWasNumber bool
	var sugar bool
	var pendingPositional strings.Builder // For tracking invalid positional after paren without comma
	var hasPendingPositional bool
	var accessorPending bool // True after seeing a dot following a list marker or tilde

	// Helper to finalize current argument
	finalizeArg := func() {
		if currentType == unitNone {
			return
		}
		if potentialString {
			// potentialString was never confirmed - error, revert to original
			// For now, just use originalItem
			args = append(args, originalItem)
		} else {
			// Always append - even if currentValue is nil (the PawScript nil literal)
			// We know we have a valid value because currentType != unitNone
			args = append(args, currentValue)
		}
		currentValue = nil
		currentType = unitNone
		potentialString = false
		originalItem = nil
		lastWasNumber = false
	}

	// Helper to get string representation for concatenation
	valueToString := func(v interface{}) string {
		switch val := v.(type) {
		case QuotedString:
			return string(val)
		case string:
			return val
		case Symbol:
			return "<" + string(val) + ">"
		case int64:
			return strconv.FormatInt(val, 10)
		case float64:
			return strconv.FormatFloat(val, 'f', -1, 64)
		case bool:
			if val {
				return "<true>"
			}
			return "<false>"
		case nil:
			return "<nil>"
		default:
			return fmt.Sprintf("%v", val)
		}
	}

	// Helper to get raw string for accessor concatenation (no angle brackets)
	rawString := func(v interface{}) string {
		switch val := v.(type) {
		case QuotedString:
			return string(val)
		case string:
			return val
		case Symbol:
			return string(val)
		case int64:
			return strconv.FormatInt(val, 10)
		case float64:
			return strconv.FormatFloat(val, 'f', -1, 64)
		default:
			return fmt.Sprintf("%v", val)
		}
	}

	// Helper to check if a value is a tilde or question expression (accessor-capable)
	isTildeExpr := func(v interface{}) bool {
		if sym, ok := v.(Symbol); ok {
			s := string(sym)
			return strings.HasPrefix(s, "~") || strings.HasPrefix(s, "?")
		}
		return false
	}

	// Helper to check if a symbol is a dot
	isDot := func(v interface{}) bool {
		if sym, ok := v.(Symbol); ok {
			return string(sym) == "."
		}
		return false
	}

	// Helper to combine a new unit with current state
	combineUnit := func(newValue interface{}, newType argUnitType) bool {
		// Check for pending positional (after paren without comma)
		if hasPendingPositional {
			if newType != unitNone {
				// More positional content - accumulate for later error
				pendingPositional.WriteString(valueToString(newValue))
				return true
			}
			return true
		}

		// If sugar is set, only named args allowed - positional is error
		if sugar && newType != unitNone {
			// This will be caught when we try to finalize
			// For now, just discard
			return true
		}

		if currentType == unitNone {
			// First unit
			currentValue = newValue
			currentType = newType
			return true
		}

		// Handle accessor pending state - we've seen a dot and are expecting a key
		if accessorPending {
			// The new value is the key for the accessor
			// Concatenate: currentValue + "." + key
			s := rawString(currentValue) + "." + rawString(newValue)
			currentValue = Symbol(s)
			// Keep currentType as-is (unitComplex or unitSymbol)
			accessorPending = false
			return true
		}

		// Apply combination rules based on current type
		switch currentType {
		case unitString:
			// String + something
			switch newType {
			case unitString:
				// Concatenate strings
				s1 := valueToString(currentValue)
				s2 := valueToString(newValue)
				currentValue = QuotedString(s1 + s2)
				lastWasNumber = false
			case unitNumber:
				// Concatenate number, track lastWasNumber
				s := valueToString(currentValue)
				if lastWasNumber {
					s += "; "
				}
				s += valueToString(newValue)
				currentValue = QuotedString(s)
				lastWasNumber = true
			case unitSymbol, unitNil, unitBool:
				// Concatenate - use rawString for tilde expressions to preserve for resolution
				s := valueToString(currentValue)
				if isTildeExpr(newValue) {
					s += rawString(newValue) // Keep tilde expressions resolvable
				} else {
					s += valueToString(newValue) // Wrap others in <>
				}
				currentValue = QuotedString(s)
				lastWasNumber = false
			case unitParen:
				// Imply comma, start new arg, set sugar
				finalizeArg()
				currentValue = newValue
				currentType = newType
				sugar = true
			case unitComplex:
				// Error, discard
				lastWasNumber = false
				return false
			}

		case unitNumber, unitSymbol, unitNil, unitBool:
			// Special handling for tilde expressions with accessors
			if currentType == unitSymbol && isTildeExpr(currentValue) {
				if isDot(newValue) {
					// Tilde + dot - next unit will be the key
					accessorPending = true
					return true
				}
				if newType == unitNumber {
					// Tilde + integer index accessor
					if num, ok := newValue.(int64); ok {
						s := rawString(currentValue) + " " + strconv.FormatInt(num, 10)
						currentValue = Symbol(s)
						return true
					}
				}
				// Fall through to normal symbol handling for other cases
			}

			// Number/Symbol/nil/bool + something
			switch newType {
			case unitString:
				// Convert to string with angle brackets for first item
				s := valueToString(currentValue) + valueToString(newValue)
				// Remove the angle brackets from the string part
				if qs, ok := newValue.(QuotedString); ok {
					s = valueToString(currentValue) + string(qs)
				}
				currentValue = QuotedString(s)
				currentType = unitString
				potentialString = false // Confirmed as string
				lastWasNumber = false
			case unitNumber, unitSymbol, unitNil, unitBool:
				// Enter or continue potentialString mode
				if !potentialString {
					potentialString = true
					originalItem = currentValue
				}
				// Build string
				s := valueToString(currentValue)
				if lastWasNumber && newType == unitNumber {
					s += "; "
				}
				s += valueToString(newValue)
				currentValue = QuotedString(s)
				currentType = unitString // Treat as string for further combinations
				lastWasNumber = (newType == unitNumber)
			case unitParen:
				if currentType == unitSymbol {
					// Symbol + paren: symbol becomes string, imply comma, paren is block
					strVal := QuotedString(string(currentValue.(Symbol)))
					finalizeArg()
					args = append(args, strVal)
					currentValue = newValue
					currentType = newType
					sugar = true
				} else {
					// Other types + paren: error if potentialString, else just error
					if potentialString {
						// Revert to original
						args = append(args, originalItem)
						currentValue = nil
						currentType = unitNone
						potentialString = false
						originalItem = nil
					}
					return false
				}
			case unitComplex:
				// Error
				if potentialString {
					args = append(args, originalItem)
					currentValue = nil
					currentType = unitNone
					potentialString = false
					originalItem = nil
				}
				return false
			}

		case unitParen:
			// Paren + something (without comma) - only named args allowed
			if newType != unitNone {
				// Start tracking pending positional for error
				hasPendingPositional = true
				pendingPositional.WriteString(valueToString(newValue))
			}

		case unitComplex:
			// Complex objects (list markers) can combine with accessors
			if isDot(newValue) {
				// Dot accessor - next unit will be the key
				accessorPending = true
				return true
			}
			if newType == unitNumber {
				// Integer index accessor - concatenate with space
				if num, ok := newValue.(int64); ok {
					s := rawString(currentValue) + " " + strconv.FormatInt(num, 10)
					currentValue = Symbol(s)
					return true
				}
			}
			// Other types can't combine with list markers
			return false
		}

		return true
	}

	// Skip leading whitespace
	for i < len(runes) && unicode.IsSpace(runes[i]) {
		i++
	}

	for i < len(runes) {
		char := runes[i]

		// Skip whitespace (but not as part of parsing a unit)
		if unicode.IsSpace(char) {
			i++
			continue
		}

		// Check for comma - explicit argument separator
		if char == ',' {
			// Check for pending positional error
			if hasPendingPositional {
				// Error: positional after paren without comma
				// Discard pending, continue
				hasPendingPositional = false
				pendingPositional.Reset()
			}
			// If no argument accumulated, insert undefined placeholder
			if currentType == unitNone {
				args = append(args, Symbol("undefined"))
			} else {
				finalizeArg()
			}
			sugar = false // Comma resets sugar for subsequent args... actually no, sugar persists
			// Actually re-reading: after sugar=true, only named args allowed
			// But comma should still finalize current arg
			i++
			// Skip whitespace after comma
			for i < len(runes) && unicode.IsSpace(runes[i]) {
				i++
			}
			continue
		}

		// Check for colon - key:value separator
		if char == ':' {
			// Check for pending positional error
			if hasPendingPositional {
				hasPendingPositional = false
				pendingPositional.Reset()
			}
			// Current value becomes key
			if potentialString {
				// Error state - can't use as key
				currentValue = originalItem
				potentialString = false
			}
			key := currentValue
			currentValue = nil
			currentType = unitNone

			i++
			// Skip whitespace after colon
			for i < len(runes) && unicode.IsSpace(runes[i]) {
				i++
			}

			// Parse value using same rules
			var valueBuilder []interface{}
			var valType argUnitType
			var valPotentialString bool
			var valOriginalItem interface{}
			var valLastWasNumber bool
			var valCurrent interface{}

			// Parse value until comma or end
			for i < len(runes) {
				vc := runes[i]
				if unicode.IsSpace(vc) {
					i++
					continue
				}
				if vc == ',' {
					break
				}

				// Parse next unit for value
				unit, utype, newI := parseNextUnit(runes, i)
				if newI == i {
					// No progress, skip char
					i++
					continue
				}
				i = newI

				// Apply combination rules for value
				if valType == unitNone {
					valCurrent = unit
					valType = utype
				} else {
					// Combine following similar rules
					valCurrent, valType, valPotentialString, valOriginalItem, valLastWasNumber = combineValueUnit(
						valCurrent, valType, valPotentialString, valOriginalItem, valLastWasNumber,
						unit, utype)
				}
			}

			// Finalize value
			finalValue := valCurrent
			if valPotentialString {
				finalValue = valOriginalItem
			}
			_ = valLastWasNumber // silence unused warning

			// Convert key to string
			keyStr := ""
			switch k := key.(type) {
			case string:
				keyStr = k
			case Symbol:
				keyStr = string(k)
			case QuotedString:
				keyStr = string(k)
			case ParenGroup:
				keyStr = string(k)
			case int64:
				keyStr = strconv.FormatInt(k, 10)
			case float64:
				keyStr = strconv.FormatFloat(k, 'f', -1, 64)
			default:
				if k != nil {
					keyStr = fmt.Sprint(k)
				}
			}

			if keyStr != "" {
				namedArgs[keyStr] = finalValue
			}
			_ = valueBuilder // silence unused

			// Skip comma if present
			if i < len(runes) && runes[i] == ',' {
				i++
				for i < len(runes) && unicode.IsSpace(runes[i]) {
					i++
				}
			}
			continue
		}

		// Parse next unit
		unit, utype, newI := parseNextUnit(runes, i)
		if newI == i {
			// No progress, skip character
			i++
			continue
		}
		i = newI

		// Combine with current state
		combineUnit(unit, utype)
	}

	// Check for pending positional error at end
	if hasPendingPositional {
		// Error: positional after paren without comma - discard
		hasPendingPositional = false
		pendingPositional.Reset()
	}

	// Finalize last argument
	finalizeArg()

	if len(namedArgs) == 0 {
		return args, nil
	}
	return args, namedArgs
}

// parseNextUnit parses the next complete unit from the rune slice starting at position i
// Returns the parsed value, its type, and the new position
func parseNextUnit(runes []rune, i int) (interface{}, argUnitType, int) {
	if i >= len(runes) {
		return nil, unitNone, i
	}

	char := runes[i]

	// Skip whitespace
	for i < len(runes) && unicode.IsSpace(runes[i]) {
		i++
		if i >= len(runes) {
			return nil, unitNone, i
		}
		char = runes[i]
	}

	// Object markers (\x00...\x00)
	if char == '\x00' {
		start := i
		i++
		for i < len(runes) && runes[i] != '\x00' {
			i++
		}
		if i < len(runes) {
			i++ // Include closing \x00
		}
		// Return as Symbol to preserve marker semantics
		return Symbol(string(runes[start:i])), unitComplex, i
	}

	// Quoted string
	if char == '"' || char == '\'' {
		quoteChar := char
		start := i
		i++
		for i < len(runes) {
			if runes[i] == '\\' && i+1 < len(runes) {
				i += 2
				continue
			}
			if runes[i] == quoteChar {
				i++
				break
			}
			i++
		}
		raw := string(runes[start:i])
		if len(raw) >= 2 {
			content := parseStringLiteral(raw[1 : len(raw)-1])
			return QuotedString(content), unitString, i
		}
		return QuotedString(""), unitString, i
	}

	// Paren group
	if char == '(' {
		start := i
		depth := 1
		i++
		inQuote := false
		var qChar rune
		for i < len(runes) && depth > 0 {
			c := runes[i]
			if c == '\\' && i+1 < len(runes) {
				i += 2
				continue
			}
			if !inQuote && (c == '"' || c == '\'') {
				inQuote = true
				qChar = c
				i++
				continue
			}
			if inQuote && c == qChar {
				inQuote = false
				i++
				continue
			}
			if !inQuote {
				if c == '(' {
					depth++
				} else if c == ')' {
					depth--
				}
			}
			i++
		}
		raw := string(runes[start:i])
		if len(raw) >= 2 {
			return ParenGroup(raw[1 : len(raw)-1]), unitParen, i
		}
		return ParenGroup(""), unitParen, i
	}

	// Brace expression (already resolved, but handle syntax)
	if char == '{' {
		start := i
		depth := 1
		i++
		inQuote := false
		var qChar rune
		for i < len(runes) && depth > 0 {
			c := runes[i]
			if c == '\\' && i+1 < len(runes) {
				i += 2
				continue
			}
			if !inQuote && (c == '"' || c == '\'') {
				inQuote = true
				qChar = c
				i++
				continue
			}
			if inQuote && c == qChar {
				inQuote = false
				i++
				continue
			}
			if !inQuote {
				if c == '{' {
					depth++
				} else if c == '}' {
					depth--
				}
			}
			i++
		}
		raw := string(runes[start:i])
		// Brace expressions are treated as strings (they're already resolved)
		return QuotedString(raw), unitString, i
	}

	// Single dot as its own symbol (for list accessor syntax)
	// This allows list.key to parse as: list, ., key
	if char == '.' {
		return Symbol("."), unitSymbol, i + 1
	}

	// Bare word (symbol, number, nil, true, false)
	// Handle escape sequences - backslash protects the next character
	start := i
	isAccessorExpr := char == '~' || char == '?' // Tilde and question expressions support accessor syntax
	for i < len(runes) {
		c := runes[i]
		// Handle escape sequences - skip backslash and the escaped character
		if c == '\\' && i+1 < len(runes) {
			i += 2
			continue
		}
		if unicode.IsSpace(c) || c == ',' || c == ':' || c == '(' || c == ')' || c == '{' || c == '}' || c == '"' || c == '\'' {
			break
		}
		// Tilde and question expressions stop at dot to allow accessor syntax
		if isAccessorExpr && c == '.' {
			break
		}
		// Dot is only part of a number if preceded by digit AND followed by digit
		if c == '.' {
			prevIsDigit := i > start && runes[i-1] >= '0' && runes[i-1] <= '9'
			nextIsDigit := i+1 < len(runes) && runes[i+1] >= '0' && runes[i+1] <= '9'
			if !(prevIsDigit && nextIsDigit) {
				// Not a valid float decimal point, stop here
				break
			}
		}
		i++
	}

	if i == start {
		return nil, unitNone, i
	}

	word := string(runes[start:i])

	// Process escape sequences in the word to get the actual value
	word = processEscapesInBareWord(word)

	// Check for special values
	if word == "nil" {
		return nil, unitNil, i
	}
	if word == "true" {
		return true, unitBool, i
	}
	if word == "false" {
		return false, unitBool, i
	}

	// Try parsing as number
	if num, err := strconv.ParseInt(word, 10, 64); err == nil {
		return num, unitNumber, i
	}
	if num, err := strconv.ParseFloat(word, 64); err == nil {
		return num, unitNumber, i
	}

	// It's a symbol
	return Symbol(word), unitSymbol, i
}

// processEscapesInBareWord converts escape sequences in bare words
// e.g., \"text\" becomes "text"
func processEscapesInBareWord(word string) string {
	var result strings.Builder
	runes := []rune(word)
	i := 0
	for i < len(runes) {
		if runes[i] == '\\' && i+1 < len(runes) {
			// Include the escaped character literally
			result.WriteRune(runes[i+1])
			i += 2
		} else {
			result.WriteRune(runes[i])
			i++
		}
	}
	return result.String()
}

// combineValueUnit applies combination rules for parsing a value (after colon)
// Returns updated state
func combineValueUnit(
	current interface{}, curType argUnitType,
	potentialString bool, originalItem interface{}, lastWasNumber bool,
	newValue interface{}, newType argUnitType,
) (interface{}, argUnitType, bool, interface{}, bool) {

	// Helper to get string representation
	valueToStr := func(v interface{}) string {
		switch val := v.(type) {
		case QuotedString:
			return string(val)
		case string:
			return val
		case Symbol:
			return "<" + string(val) + ">"
		case int64:
			return strconv.FormatInt(val, 10)
		case float64:
			return strconv.FormatFloat(val, 'f', -1, 64)
		case bool:
			if val {
				return "<true>"
			}
			return "<false>"
		case nil:
			return "<nil>"
		default:
			return fmt.Sprintf("%v", val)
		}
	}

	switch curType {
	case unitString:
		switch newType {
		case unitString:
			s := valueToStr(current) + valueToStr(newValue)
			if qs, ok := newValue.(QuotedString); ok {
				s = valueToStr(current)
				// For string, don't add angle brackets
				if cs, ok := current.(QuotedString); ok {
					s = string(cs)
				}
				s += string(qs)
			}
			return QuotedString(s), unitString, false, nil, false
		case unitNumber:
			s := valueToStr(current)
			if cs, ok := current.(QuotedString); ok {
				s = string(cs)
			}
			if lastWasNumber {
				s += "; "
			}
			s += valueToStr(newValue)
			return QuotedString(s), unitString, false, nil, true
		case unitSymbol, unitNil, unitBool:
			s := valueToStr(current)
			if cs, ok := current.(QuotedString); ok {
				s = string(cs)
			}
			s += valueToStr(newValue)
			return QuotedString(s), unitString, false, nil, false
		case unitParen:
			// For values, paren just becomes part of string? Or error?
			// Treating as separate would be weird in value context
			// Let's treat paren in value as error/ignored
			return current, curType, potentialString, originalItem, lastWasNumber
		}

	case unitNumber, unitSymbol, unitNil, unitBool:
		switch newType {
		case unitString:
			s := valueToStr(current)
			if qs, ok := newValue.(QuotedString); ok {
				s += string(qs)
			} else {
				s += valueToStr(newValue)
			}
			return QuotedString(s), unitString, false, nil, false
		case unitNumber, unitSymbol, unitNil, unitBool:
			if !potentialString {
				potentialString = true
				originalItem = current
			}
			s := valueToStr(current)
			if lastWasNumber && newType == unitNumber {
				s += "; "
			}
			s += valueToStr(newValue)
			return QuotedString(s), unitString, potentialString, originalItem, (newType == unitNumber)
		case unitParen:
			// Error in value context
			if potentialString {
				return originalItem, curType, false, nil, false
			}
			return current, curType, potentialString, originalItem, lastWasNumber
		}
	}

	return current, curType, potentialString, originalItem, lastWasNumber
}

// parseStringLiteral handles escape sequences in strings
func parseStringLiteral(str string) string {
	var result strings.Builder
	runes := []rune(str)
	i := 0

	for i < len(runes) {
		if runes[i] == '\\' && i+1 < len(runes) {
			// Handle escape sequence
			nextChar := runes[i+1]
			switch nextChar {
			case '0':
				result.WriteRune('\x00') // null byte
				i += 2
			case 'a':
				result.WriteRune('\x07') // bell
				i += 2
			case 'b':
				result.WriteRune('\x08') // backspace
				i += 2
			case 'e':
				result.WriteRune('\x1B') // escape
				i += 2
			case 'f':
				result.WriteRune('\x0C') // form feed
				i += 2
			case 'n':
				result.WriteRune('\n') // newline
				i += 2
			case 'r':
				result.WriteRune('\r') // carriage return
				i += 2
			case 't':
				result.WriteRune('\t') // tab
				i += 2
			case '\\':
				result.WriteRune('\\') // backslash
				i += 2
			case '\'':
				result.WriteRune('\'') // single quote
				i += 2
			case '"':
				result.WriteRune('"') // double quote
				i += 2
			case '\n':
				// Line continuation: backslash followed by newline produces empty string
				i += 2
			case '~':
				// Escaped tilde: preserve as \~ for later processing by applySubstitution
				result.WriteRune('\\')
				result.WriteRune('~')
				i += 2
			case 'x':
				// Hex escape: \xHH
				if i+3 < len(runes) {
					hexStr := string(runes[i+2 : i+4])
					if val, err := strconv.ParseUint(hexStr, 16, 8); err == nil {
						result.WriteRune(rune(val))
						i += 4
					} else {
						// Invalid hex sequence, treat as literal \x
						result.WriteRune(nextChar)
						i += 2
					}
				} else {
					// Not enough characters, treat as literal \x
					result.WriteRune(nextChar)
					i += 2
				}
			case 'u':
				// Unicode escape: \uXXXX (4 hex digits)
				if i+5 < len(runes) {
					hexStr := string(runes[i+2 : i+6])
					if val, err := strconv.ParseUint(hexStr, 16, 32); err == nil {
						result.WriteRune(rune(val))
						i += 6
					} else {
						// Invalid unicode sequence, treat as literal \u
						result.WriteRune(nextChar)
						i += 2
					}
				} else {
					// Not enough characters, treat as literal \u
					result.WriteRune(nextChar)
					i += 2
				}
			case 'U':
				// Unicode escape: \UXXXXXXXX (8 hex digits)
				if i+9 < len(runes) {
					hexStr := string(runes[i+2 : i+10])
					if val, err := strconv.ParseUint(hexStr, 16, 32); err == nil && val <= 0x10FFFF {
						result.WriteRune(rune(val))
						i += 10
					} else {
						// Invalid unicode sequence, treat as literal \U
						result.WriteRune(nextChar)
						i += 2
					}
				} else {
					// Not enough characters, treat as literal \U
					result.WriteRune(nextChar)
					i += 2
				}
			default:
				// For any other character, just remove the backslash
				result.WriteRune(nextChar)
				i += 2
			}
		} else {
			result.WriteRune(runes[i])
			i++
		}
	}

	return result.String()
}

// GetSourceMap returns the source map
func (p *Parser) GetSourceMap() *SourceMap {
	return p.sourceMap
}

// NormalizeKeywords replaces 'then' with '&', 'else' with '|', and 'not' with '!' when they are standalone words
// but skips content inside parentheses (which may be stored as data rather than executed)
func (p *Parser) NormalizeKeywords(source string) string {
	var result strings.Builder
	inQuote := false
	var quoteChar rune
	parenDepth := 0

	runes := []rune(source)
	i := 0
	resultPosition := 0

	// Create new source map mappings for the normalized string
	newMappings := make(map[int]*SourcePosition)

	for i < len(runes) {
		char := runes[i]

		// Handle escape sequences
		if char == '\\' && i+1 < len(runes) {
			result.WriteRune(char)
			result.WriteRune(runes[i+1])
			// Map both characters to their original positions
			if origPos := p.sourceMap.GetOriginalPosition(i); origPos != nil {
				newMappings[resultPosition] = origPos
				newMappings[resultPosition+1] = p.sourceMap.GetOriginalPosition(i + 1)
			}
			resultPosition += 2
			i += 2
			continue
		}

		// Track quotes
		if !inQuote && (char == '"' || char == '\'') {
			inQuote = true
			quoteChar = char
			result.WriteRune(char)
			if origPos := p.sourceMap.GetOriginalPosition(i); origPos != nil {
				newMappings[resultPosition] = origPos
			}
			resultPosition++
			i++
			continue
		}

		if inQuote {
			result.WriteRune(char)
			if origPos := p.sourceMap.GetOriginalPosition(i); origPos != nil {
				newMappings[resultPosition] = origPos
			}
			resultPosition++
			if char == quoteChar {
				inQuote = false
			}
			i++
			continue
		}

		// Track parentheses depth
		if char == '(' {
			parenDepth++
			result.WriteRune(char)
			if origPos := p.sourceMap.GetOriginalPosition(i); origPos != nil {
				newMappings[resultPosition] = origPos
			}
			resultPosition++
			i++
			continue
		}

		if char == ')' {
			parenDepth--
			result.WriteRune(char)
			if origPos := p.sourceMap.GetOriginalPosition(i); origPos != nil {
				newMappings[resultPosition] = origPos
			}
			resultPosition++
			i++
			continue
		}

		// Only normalize keywords at top level (outside parentheses)
		if parenDepth > 0 {
			result.WriteRune(char)
			if origPos := p.sourceMap.GetOriginalPosition(i); origPos != nil {
				newMappings[resultPosition] = origPos
			}
			resultPosition++
			i++
			continue
		}

		// Check for scope operator '::' (2 characters) - convert to marker
		if char == ':' && i+1 < len(runes) && runes[i+1] == ':' {
			// Write scope marker
			result.WriteString(ScopeMarker)
			// Map to original position
			if origPos := p.sourceMap.GetOriginalPosition(i); origPos != nil {
				newMappings[resultPosition] = origPos
			}
			resultPosition += len(ScopeMarker)
			i += 2 // Skip both colons
			continue
		}

		// Check for 'not' keyword (3 characters)
		if i+3 <= len(runes) && string(runes[i:i+3]) == "not" {
			// Check word boundaries
			beforeOk := i == 0 || !unicode.IsLetter(runes[i-1]) && !unicode.IsDigit(runes[i-1]) && runes[i-1] != '_'
			afterOk := i+3 >= len(runes) || !unicode.IsLetter(runes[i+3]) && !unicode.IsDigit(runes[i+3]) && runes[i+3] != '_'

			if beforeOk && afterOk {
				result.WriteRune('!')
				// Map the '!' to the original position of 'n' in 'not'
				if origPos := p.sourceMap.GetOriginalPosition(i); origPos != nil {
					newMappings[resultPosition] = origPos
				}
				resultPosition++
				i += 3
				continue
			}
		}

		// Check for 'then' keyword (4 characters)
		if i+4 <= len(runes) && string(runes[i:i+4]) == "then" {
			// Check word boundaries
			beforeOk := i == 0 || !unicode.IsLetter(runes[i-1]) && !unicode.IsDigit(runes[i-1]) && runes[i-1] != '_'
			afterOk := i+4 >= len(runes) || !unicode.IsLetter(runes[i+4]) && !unicode.IsDigit(runes[i+4]) && runes[i+4] != '_'

			if beforeOk && afterOk {
				result.WriteRune('&')
				// Map the '&' to the original position of 't' in 'then'
				if origPos := p.sourceMap.GetOriginalPosition(i); origPos != nil {
					newMappings[resultPosition] = origPos
				}
				resultPosition++
				i += 4
				continue
			}
		}

		// Check for 'else' keyword (4 characters)
		if i+4 <= len(runes) && string(runes[i:i+4]) == "else" {
			// Check word boundaries
			beforeOk := i == 0 || !unicode.IsLetter(runes[i-1]) && !unicode.IsDigit(runes[i-1]) && runes[i-1] != '_'
			afterOk := i+4 >= len(runes) || !unicode.IsLetter(runes[i+4]) && !unicode.IsDigit(runes[i+4]) && runes[i+4] != '_'

			if beforeOk && afterOk {
				result.WriteRune('|')
				// Map the '|' to the original position of 'e' in 'else'
				if origPos := p.sourceMap.GetOriginalPosition(i); origPos != nil {
					newMappings[resultPosition] = origPos
				}
				resultPosition++
				i += 4
				continue
			}
		}

		// Default: copy character as-is
		result.WriteRune(char)
		if origPos := p.sourceMap.GetOriginalPosition(i); origPos != nil {
			newMappings[resultPosition] = origPos
		}
		resultPosition++
		i++
	}

	// Replace the source map with the new mappings
	p.sourceMap.TransformedToOriginal = newMappings

	return result.String()
}

// applyChainOperators applies chain operator transformations to parsed commands
// Processes ~> (chain) and => (assign) operators by injecting {get_result}
func (p *Parser) applyChainOperators(commands []*ParsedCommand) ([]*ParsedCommand, error) {
	for i := 0; i < len(commands); i++ {
		cmd := commands[i]

		switch cmd.ChainType {
		case "chain":
			// ~> operator: prepend {get_result} to current command's arguments
			// Transform: "cmd~>next args" => "next {get_result}, args"
			// Insert {get_result} as first argument after command name
			parts := strings.SplitN(cmd.Command, " ", 2)
			if len(parts) == 1 {
				// No existing arguments
				cmd.Command = parts[0] + " {get_result}"
			} else {
				// Has existing arguments - insert {get_result} before them with comma
				cmd.Command = parts[0] + " {get_result}, " + parts[1]
			}

		case "chain_append":
			// ~~> operator: append {get_result} as last argument
			// Transform: "cmd~~>next args" => "next args, {get_result}"
			// Append {get_result} as last argument
			parts := strings.SplitN(cmd.Command, " ", 2)
			if len(parts) == 1 {
				// No existing arguments
				cmd.Command = parts[0] + " {get_result}"
			} else {
				// Has existing arguments - append {get_result} after them with comma
				cmd.Command = parts[0] + " " + parts[1] + ", {get_result}"
			}

		case "assign":
			// => operator: turn command into assignment
			// Transform: "cmd=>varname" => "varname: {get_result}"
			cmdName := strings.TrimSpace(cmd.Command)
			if cmdName == "" {
				return nil, &PawScriptError{
					Message:  "Fat arrow operator (=>) requires a variable name after it",
					Position: cmd.Position,
					Context:  p.sourceMap.OriginalLines,
				}
			}

			// Check if it looks like a valid identifier
			if strings.ContainsAny(cmdName, " \t\n(){}[]") {
				return nil, &PawScriptError{
					Message:  fmt.Sprintf("Invalid variable name after => operator: '%s'", cmdName),
					Position: cmd.Position,
					Context:  p.sourceMap.OriginalLines,
				}
			}

			// Transform to assignment
			cmd.Command = fmt.Sprintf("%s: {get_result}", cmdName)
		}
	}

	return commands, nil
}
