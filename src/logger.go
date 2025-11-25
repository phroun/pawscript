package pawscript

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// LogLevel represents the severity of a log message
type LogLevel int

const (
	LevelDebug LogLevel = iota // Development info (requires enabled + category)
	LevelWarn                  // Warnings (requires enabled or category)
	LevelError                 // Runtime errors (always shown)
	LevelFatal                 // Parse/unknown command errors (always shown)
)

// LogCategory represents the subsystem generating the message
type LogCategory string

const (
	CatNone     LogCategory = ""         // Uncategorized
	CatParse    LogCategory = "parse"    // Parser errors
	CatCommand  LogCategory = "command"  // Command execution
	CatVariable LogCategory = "variable" // Variable operations (get/set)
	CatArgument LogCategory = "argument" // Argument validation
	CatIO       LogCategory = "io"       // I/O operations (read/exec)
	CatMacro    LogCategory = "macro"    // Macro operations
	CatAsync    LogCategory = "async"    // Async/token operations
	CatMemory   LogCategory = "memory"   // Memory/refcounting
	CatMath     LogCategory = "math"     // Arithmetic operations
	CatList     LogCategory = "list"     // List operations
	CatString   LogCategory = "string"   // String operations
	CatType     LogCategory = "type"     // Type operations
	CatFlow     LogCategory = "flow"     // Flow control (while, if, ret)
	CatSystem   LogCategory = "system"   // System/module operations
	CatApp      LogCategory = "app"      // Application specific
	CatUser     LogCategory = "user"     // User generated/custom
)

// Logger handles logging for PawScript
type Logger struct {
	enabled           bool
	enabledCategories map[LogCategory]bool
	out               io.Writer
	errOut            io.Writer
}

// NewLogger creates a new logger
func NewLogger(enabled bool) *Logger {
	return &Logger{
		enabled:           enabled,
		enabledCategories: make(map[LogCategory]bool),
		out:               os.Stdout,
		errOut:            os.Stderr,
	}
}

// SetEnabled enables or disables debug logging
func (l *Logger) SetEnabled(enabled bool) {
	l.enabled = enabled
}

// EnableCategory enables debug logging for a specific category
func (l *Logger) EnableCategory(cat LogCategory) {
	l.enabledCategories[cat] = true
}

// DisableCategory disables debug logging for a specific category
func (l *Logger) DisableCategory(cat LogCategory) {
	delete(l.enabledCategories, cat)
}

// EnableAllCategories enables all categories for debug logging
func (l *Logger) EnableAllCategories() {
	for _, cat := range []LogCategory{
		CatParse, CatCommand, CatVariable, CatArgument, CatIO,
		CatMacro, CatAsync, CatMemory, CatMath, CatList, CatString, CatType, CatFlow, CatSystem, CatApp, CatUser,
	} {
		l.enabledCategories[cat] = true
	}
}

// IsCategoryEnabled checks if a category is enabled
func (l *Logger) IsCategoryEnabled(cat LogCategory) bool {
	return l.enabledCategories[cat]
}

// shouldLog determines if a message should be logged based on level and category
func (l *Logger) shouldLog(level LogLevel, cat LogCategory) bool {
	switch level {
	case LevelFatal, LevelError:
		return true // Always shown
	case LevelWarn:
		return l.enabled || l.enabledCategories[cat]
	case LevelDebug:
		return l.enabled && (cat == CatNone || l.enabledCategories[cat])
	default:
		return false
	}
}

// Log is the unified logging method
func (l *Logger) Log(level LogLevel, cat LogCategory, message string, position *SourcePosition, context []string) {
	if !l.shouldLog(level, cat) {
		return
	}

	var prefix string
	switch level {
	case LevelDebug:
		prefix = "[DEBUG]"
	case LevelWarn:
		prefix = "[PawScript WARN]"
	case LevelError, LevelFatal:
		prefix = "[PawScript ERROR]"
	}

	// Add category to debug messages
	if level == LevelDebug && cat != CatNone {
		prefix = fmt.Sprintf("[DEBUG:%s]", cat)
	}

	output := fmt.Sprintf("%s %s", prefix, message)

	// Add position information if available
	if position != nil {
		filename := position.Filename
		if filename == "" {
			filename = "<unknown>"
		}
		output += fmt.Sprintf("\n  at line %d, column %d in %s", position.Line, position.Column, filename)

		// Add macro context if present
		if position.MacroContext != nil {
			output += l.formatMacroContext(position.MacroContext)
		}

		// Add source context lines
		if len(context) > 0 {
			output += l.formatSourceContext(position, context)
		}
	}

	// Route to appropriate output
	if level == LevelDebug {
		_, _ = fmt.Fprintln(l.out, output)
	} else {
		_, _ = fmt.Fprintln(l.errOut, output)
	}
}

// Convenience methods that route through Log

// Debug logs a debug message (no position)
func (l *Logger) Debug(format string, args ...interface{}) {
	l.Log(LevelDebug, CatNone, fmt.Sprintf(format, args...), nil, nil)
}

// DebugCat logs a categorized debug message
func (l *Logger) DebugCat(cat LogCategory, format string, args ...interface{}) {
	l.Log(LevelDebug, cat, fmt.Sprintf(format, args...), nil, nil)
}

// Warn logs a warning message (no position)
func (l *Logger) Warn(format string, args ...interface{}) {
	l.Log(LevelWarn, CatNone, fmt.Sprintf(format, args...), nil, nil)
}

// Error logs an error message (no position)
func (l *Logger) Error(format string, args ...interface{}) {
	l.Log(LevelError, CatNone, fmt.Sprintf(format, args...), nil, nil)
}

// ErrorWithPosition logs an error with position information
func (l *Logger) ErrorWithPosition(message string, position *SourcePosition, context []string) {
	l.Log(LevelError, CatNone, message, position, context)
}

// ParseError logs a parse error (always visible)
func (l *Logger) ParseError(message string, position *SourcePosition, context []string) {
	l.Log(LevelFatal, CatParse, fmt.Sprintf("Parse error: %s", message), position, context)
}

// UnknownCommandError logs an unknown command error (always visible)
func (l *Logger) UnknownCommandError(commandName string, position *SourcePosition, context []string) {
	l.Log(LevelFatal, CatCommand, fmt.Sprintf("Unknown command: %s", commandName), position, context)
}

// CommandError logs a command execution error with category
func (l *Logger) CommandError(cat LogCategory, cmdName, message string, position *SourcePosition) {
	fullMessage := message
	if cmdName != "" {
		fullMessage = fmt.Sprintf("%s: %s", strings.ToUpper(cmdName), message)
	}
	l.Log(LevelError, cat, fullMessage, position, nil)
}

// formatMacroContext formats the macro call chain
func (l *Logger) formatMacroContext(macroContext *MacroContext) string {
	chain := l.getMacroChain(macroContext)

	var message strings.Builder
	message.WriteString("\n\nMacro call chain:")

	for i, context := range chain {
		indent := strings.Repeat("  ", i+1)
		message.WriteString(fmt.Sprintf("\n%s→ macro \"%s\"", indent, context.MacroName))
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
