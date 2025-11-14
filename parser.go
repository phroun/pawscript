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
			pos := &SourcePosition{
				Line:         originalLine,
				Column:       originalColumn,
				Length:       2,
				OriginalText: escapeSeq,
				Filename:     p.sourceMap.Filename,
			}
			p.sourceMap.AddMapping(resultPosition, pos)
			resultPosition += 2
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
				isFollowedByWhitespaceOrEnd := i+1 >= length || unicode.IsSpace(runes[i+1])
				
				if isFollowedByWhitespaceOrEnd {
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
	currentSeparator := "none"
	
	runes := []rune(commandStr)
	i := 0
	
	addCommand := func(cmd string, separator string, endLine, endCol int) {
		trimmed := strings.TrimSpace(cmd)
		if trimmed != "" {
			pos := p.sourceMap.GetOriginalPosition(0)
			if pos == nil {
				pos = &SourcePosition{
					Line:     commandStartLine,
					Column:   commandStartColumn,
					Length:   len(trimmed),
					Filename: p.sourceMap.Filename,
				}
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
	}
	
	for i < len(runes) {
		char := runes[i]
		
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
			addCommand(currentCommand.String(), currentSeparator, line, column+1)
			currentSeparator = ";"
			i++
			column++
			continue
		}
		
		if char == '&' {
			addCommand(currentCommand.String(), currentSeparator, line, column+1)
			currentSeparator = "&"
			i++
			column++
			continue
		}
		
		if char == '|' {
			addCommand(currentCommand.String(), currentSeparator, line, column+1)
			currentSeparator = "|"
			i++
			column++
			continue
		}
		
		// Handle newlines
		if char == '\n' {
			if strings.TrimSpace(currentCommand.String()) != "" {
				addCommand(currentCommand.String(), currentSeparator, line+1, 1)
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
		addCommand(currentCommand.String(), currentSeparator, line, column)
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
	
	// Handle parentheses - return content without parens
	if strings.HasPrefix(argStr, "(") && strings.HasSuffix(argStr, ")") {
		return argStr[1 : len(argStr)-1]
	}
	
	// Handle quoted strings
	if (strings.HasPrefix(argStr, "\"") && strings.HasSuffix(argStr, "\"")) ||
		(strings.HasPrefix(argStr, "'") && strings.HasSuffix(argStr, "'")) {
		return parseStringLiteral(argStr[1 : len(argStr)-1])
	}
	
	// Handle booleans
	if argStr == "true" {
		return true
	}
	if argStr == "false" {
		return false
	}
	
	// Handle numbers
	if num, err := strconv.ParseInt(argStr, 10, 64); err == nil {
		return num
	}
	if num, err := strconv.ParseFloat(argStr, 64); err == nil {
		return num
	}
	
	return argStr
}

// parseStringLiteral handles escape sequences in strings
func parseStringLiteral(str string) string {
	str = strings.ReplaceAll(str, `\'`, "'")
	str = strings.ReplaceAll(str, `\"`, "\"")
	str = strings.ReplaceAll(str, `\n`, "\n")
	str = strings.ReplaceAll(str, `\r`, "\r")
	str = strings.ReplaceAll(str, `\t`, "\t")
	str = strings.ReplaceAll(str, `\\`, "\\")
	return str
}

// GetSourceMap returns the source map
func (p *Parser) GetSourceMap() *SourceMap {
	return p.sourceMap
}
