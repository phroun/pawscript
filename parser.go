package pawscript

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

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
	commandStartPos := 0  // Track position in the string for source map lookup
	currentSeparator := "none"
	
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
			})
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
	
	return commands, nil
}

// ParseCommand parses a single command into name and arguments
func ParseCommand(commandStr string) (string, []interface{}) {
	commandStr = strings.TrimSpace(commandStr)
	if commandStr == "" {
		return "", nil
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
		return commandStr, nil
	}
	
	command := string(runes[:commandEnd])
	argsStr := strings.TrimSpace(string(runes[commandEnd:]))
	
	if argsStr == "" {
		return command, nil
	}
	
	args := parseArguments(argsStr)
	return command, args
}

// parseArguments parses argument string into slice
func parseArguments(argsStr string) []interface{} {
	var args []interface{}
	var currentArg strings.Builder
	inQuote := false
	var quoteChar rune
	parenCount := 0
	braceCount := 0
	
	runes := []rune(argsStr)
	i := 0
	
	for i < len(runes) {
		char := runes[i]
		
		if char == '\\' && i+1 < len(runes) {
			currentArg.WriteRune(char)
			currentArg.WriteRune(runes[i+1])
			i += 2
			continue
		}
		
		if !inQuote && (char == '"' || char == '\'') {
			inQuote = true
			quoteChar = char
			currentArg.WriteRune(char)
			i++
			continue
		}
		
		if inQuote && char == quoteChar {
			inQuote = false
			quoteChar = 0
			currentArg.WriteRune(char)
			i++
			continue
		}
		
		if !inQuote && char == '(' {
			parenCount++
			currentArg.WriteRune(char)
			i++
			continue
		}
		
		if !inQuote && char == ')' {
			parenCount--
			currentArg.WriteRune(char)
			i++
			continue
		}
		
		if !inQuote && char == '{' {
			braceCount++
			currentArg.WriteRune(char)
			i++
			continue
		}
		
		if !inQuote && char == '}' {
			braceCount--
			currentArg.WriteRune(char)
			i++
			continue
		}
		
		if !inQuote && parenCount == 0 && braceCount == 0 && char == ',' {
			args = append(args, parseArgumentValue(strings.TrimSpace(currentArg.String())))
			currentArg.Reset()
			
			// Skip whitespace after comma
			for i+1 < len(runes) && unicode.IsSpace(runes[i+1]) {
				i++
			}
			
			i++
			continue
		}
		
		currentArg.WriteRune(char)
		i++
	}
	
	trimmed := strings.TrimSpace(currentArg.String())
	if trimmed != "" || len(args) > 0 {
		args = append(args, parseArgumentValue(trimmed))
	}
	
	return args
}

// parseArgumentValue parses a single argument value
func parseArgumentValue(argStr string) interface{} {
	if argStr == "" {
		return nil
	}
	
	// Handle parentheses - return as ParenGroup to preserve form
	if strings.HasPrefix(argStr, "(") && strings.HasSuffix(argStr, ")") {
		content := argStr[1 : len(argStr)-1]
		return ParenGroup(content)
	}
	
	// Handle quoted strings - return as QuotedString to preserve form
	if (strings.HasPrefix(argStr, "\"") && strings.HasSuffix(argStr, "\"")) ||
		(strings.HasPrefix(argStr, "'") && strings.HasSuffix(argStr, "'")) {
		content := parseStringLiteral(argStr[1 : len(argStr)-1])
		return QuotedString(content)
	}
	
	// Handle booleans
	if argStr == "true" {
		return true
	}
	if argStr == "false" {
		return false
	}
	
	// Handle nil
	if argStr == "nil" {
		return nil
	}
	
	// Handle numbers
	if num, err := strconv.ParseInt(argStr, 10, 64); err == nil {
		return num
	}
	if num, err := strconv.ParseFloat(argStr, 64); err == nil {
		return num
	}
	
	// Bare identifier - return as Symbol to preserve its nature
	return Symbol(argStr)
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
