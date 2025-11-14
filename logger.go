package pawscript

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Logger handles logging for PawScript
type Logger struct {
	enabled bool
	out     io.Writer
	errOut  io.Writer
}

// NewLogger creates a new logger
func NewLogger(enabled bool) *Logger {
	return &Logger{
		enabled: enabled,
		out:     os.Stdout,
		errOut:  os.Stderr,
	}
}

// Debug logs a debug message
func (l *Logger) Debug(format string, args ...interface{}) {
	if l.enabled {
		fmt.Fprintf(l.out, "[DEBUG] "+format+"\n", args...)
	}
}

// Warn logs a warning message
func (l *Logger) Warn(format string, args ...interface{}) {
	if l.enabled {
		fmt.Fprintf(l.errOut, "[PawScript WARN] "+format+"\n", args...)
	}
}

// Error logs an error message
func (l *Logger) Error(format string, args ...interface{}) {
	if l.enabled {
		fmt.Fprintf(l.errOut, "[PawScript ERROR] "+format+"\n", args...)
	}
}

// ErrorWithPosition logs an error with position information
func (l *Logger) ErrorWithPosition(message string, position *SourcePosition, context []string) {
	if !l.enabled {
		return
	}
	
	errorMsg := fmt.Sprintf("[PawScript ERROR] %s", message)
	
	if position != nil {
		filename := position.Filename
		if filename == "" {
			filename = "<unknown>"
		}
		errorMsg += fmt.Sprintf("\n  at line %d, column %d in %s", position.Line, position.Column, filename)
		
		// Add macro context if present
		if position.MacroContext != nil {
			errorMsg += l.formatMacroContext(position.MacroContext)
		}
		
		// Add source context lines
		if len(context) > 0 {
			errorMsg += l.formatSourceContext(position, context)
		}
	}
	
	fmt.Fprintln(l.errOut, errorMsg)
}

// ParseError logs a parse error (always visible)
func (l *Logger) ParseError(message string, position *SourcePosition, context []string) {
	fullMessage := fmt.Sprintf("Parse error: %s", message)
	
	errorOutput := fmt.Sprintf("[PawScript ERROR] %s", fullMessage)
	
	if position != nil {
		filename := position.Filename
		if filename == "" {
			filename = "<unknown>"
		}
		errorOutput += fmt.Sprintf("\n  at line %d, column %d in %s", position.Line, position.Column, filename)
		
		if position.MacroContext != nil {
			errorOutput += l.formatMacroContext(position.MacroContext)
		}
		
		if len(context) > 0 {
			errorOutput += l.formatSourceContext(position, context)
		}
	}
	
	fmt.Fprintln(l.errOut, errorOutput)
}

// UnknownCommandError logs an unknown command error (always visible)
func (l *Logger) UnknownCommandError(commandName string, position *SourcePosition, context []string) {
	message := fmt.Sprintf("Unknown command: %s", commandName)
	
	errorOutput := fmt.Sprintf("[PawScript ERROR] %s", message)
	
	if position != nil {
		filename := position.Filename
		if filename == "" {
			filename = "<unknown>"
		}
		errorOutput += fmt.Sprintf("\n  at line %d, column %d in %s", position.Line, position.Column, filename)
		
		if position.MacroContext != nil {
			errorOutput += l.formatMacroContext(position.MacroContext)
		}
		
		if len(context) > 0 {
			errorOutput += l.formatSourceContext(position, context)
		}
	}
	
	fmt.Fprintln(l.errOut, errorOutput)
}

// CommandError logs a command execution error
func (l *Logger) CommandError(commandName, message string, position *SourcePosition, context []string) {
	fullMessage := fmt.Sprintf("Error executing command '%s': %s", commandName, message)
	l.ErrorWithPosition(fullMessage, position, context)
}

// formatMacroContext formats the macro call chain
func (l *Logger) formatMacroContext(macroContext *MacroContext) string {
	chain := l.getMacroChain(macroContext)
	
	var message strings.Builder
	message.WriteString("\n\nMacro call chain:")
	
	for i, context := range chain {
		indent := strings.Repeat("  ", i+1)
		message.WriteString(fmt.Sprintf("\n%sâ†’ macro \"%s\"", indent, context.MacroName))
		message.WriteString(fmt.Sprintf("\n%s  defined in %s:%d:%d", indent, context.DefinitionFile, context.DefinitionLine, context.DefinitionColumn))
		
		if context.InvocationFile != "" && context.InvocationLine > 0 {
			message.WriteString(fmt.Sprintf("\n%s  called from %s:%d:%d", indent, context.InvocationFile, context.InvocationLine, context.InvocationColumn))
		}
	}
	
	return message.String()
}

// getMacroChain extracts the macro call chain
func (l *Logger) getMacroChain(macroContext *MacroContext) []*MacroContext {
	var chain []*MacroContext
	current := macroContext
	
	for current != nil {
		chain = append(chain, current)
		current = current.ParentMacro
	}
	
	return chain
}

// formatSourceContext formats source context with line numbers
func (l *Logger) formatSourceContext(position *SourcePosition, context []string) string {
	var message strings.Builder
	message.WriteString("\n")
	
	contextStart := max(0, position.Line-2)
	contextEnd := min(len(context), position.Line+1)
	
	for i := contextStart; i < contextEnd; i++ {
		lineNum := i + 1
		isErrorLine := lineNum == position.Line
		
		var prefix string
		if isErrorLine {
			prefix = ">"
		} else {
			prefix = " "
		}
		
		lineNumStr := fmt.Sprintf("%3d", lineNum)
		message.WriteString(fmt.Sprintf("\n  %s %s | %s", prefix, lineNumStr, context[i]))
		
		if isErrorLine && position.Column > 0 {
			indent := "      | " + strings.Repeat(" ", position.Column-1)
			caretLen := max(1, position.Length)
			caret := strings.Repeat("^", caretLen)
			message.WriteString(fmt.Sprintf("\n  %s%s", indent, caret))
		}
	}
	
	return message.String()
}

// SetEnabled enables or disables logging
func (l *Logger) SetEnabled(enabled bool) {
	l.enabled = enabled
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
