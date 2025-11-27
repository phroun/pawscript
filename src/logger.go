package pawscript

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// OutputContext provides the necessary context for channel-based output routing
// This allows the logger to resolve #out/#err channels through the proper hierarchy
type OutputContext struct {
	State    *ExecutionState
	Executor *Executor
}

// NewOutputContext creates a new OutputContext from an ExecutionState
func NewOutputContext(state *ExecutionState, executor *Executor) *OutputContext {
	if state == nil {
		return nil
	}
	return &OutputContext{
		State:    state,
		Executor: executor,
	}
}

// ResolveChannel resolves a channel name (like "#out" or "#err") through the hierarchy:
// 1. Local macro/execution context variables
// 2. Module's ObjectsModule (copy-on-write layer)
// 3. Root ObjectsInherited (where io::#out etc. live)
// Returns nil if no channel found (caller should fall back to system console)
func (oc *OutputContext) ResolveChannel(channelName string) *StoredChannel {
	if oc == nil || oc.State == nil {
		return nil
	}

	// Helper to convert a value to a StoredChannel
	valueToChannel := func(val interface{}) *StoredChannel {
		switch v := val.(type) {
		case *StoredChannel:
			return v
		case Symbol:
			markerType, objectID := parseObjectMarker(string(v))
			if markerType == "channel" && objectID >= 0 && oc.Executor != nil {
				if obj, exists := oc.Executor.getObject(objectID); exists {
					if ch, ok := obj.(*StoredChannel); ok {
						return ch
					}
				}
			}
		case string:
			markerType, objectID := parseObjectMarker(v)
			if markerType == "channel" && objectID >= 0 && oc.Executor != nil {
				if obj, exists := oc.Executor.getObject(objectID); exists {
					if ch, ok := obj.(*StoredChannel); ok {
						return ch
					}
				}
			}
		}
		return nil
	}

	// 1. Check local variables first (macro context)
	if localVal, exists := oc.State.GetVariable(channelName); exists {
		if ch := valueToChannel(localVal); ch != nil {
			return ch
		}
	}

	// 2. Check ObjectsModule (module-level, copy-on-write layer)
	if oc.State.moduleEnv != nil {
		oc.State.moduleEnv.mu.RLock()
		defer oc.State.moduleEnv.mu.RUnlock()

		if oc.State.moduleEnv.ObjectsModule != nil {
			if obj, exists := oc.State.moduleEnv.ObjectsModule[channelName]; exists {
				if ch := valueToChannel(obj); ch != nil {
					return ch
				}
			}
		}

		// 3. Check ObjectsInherited (root level, where io::#out etc. live)
		if oc.State.moduleEnv.ObjectsInherited != nil {
			if obj, exists := oc.State.moduleEnv.ObjectsInherited[channelName]; exists {
				if ch := valueToChannel(obj); ch != nil {
					return ch
				}
			}
		}
	}

	return nil
}

// WriteToOut writes a message to the resolved #out channel or falls back to system stdout
func (oc *OutputContext) WriteToOut(message string) error {
	if ch := oc.ResolveChannel("#out"); ch != nil {
		return ChannelSend(ch, message)
	}
	// Fallback to system stdout
	_, err := fmt.Fprint(os.Stdout, message)
	return err
}

// WriteToErr writes a message to the resolved #err channel or falls back to system stderr
func (oc *OutputContext) WriteToErr(message string) error {
	if ch := oc.ResolveChannel("#err"); ch != nil {
		return ChannelSend(ch, message)
	}
	// Fallback to system stderr
	_, err := fmt.Fprint(os.Stderr, message)
	return err
}

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

// ANSI color codes for terminal output
const (
	colorYellow = "\x1b[93m" // Bright yellow foreground
	colorReset  = "\x1b[0m"  // Reset to default
)

// Logger handles logging for PawScript
type Logger struct {
	enabled           bool
	enabledCategories map[LogCategory]bool
	out               io.Writer
	errOut            io.Writer
	// outputContext holds the current execution context for channel routing
	// This is set per-execution and allows log output to go through #out/#err
	outputContext     *OutputContext
	// colorEnabled is true if terminal colors should be used for stderr output
	colorEnabled      bool
}

// stderrSupportsColor checks if stderr is a terminal that supports color output
func stderrSupportsColor() bool {
	// Check if stderr is a terminal (not redirected/piped)
	stderrInfo, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	// ModeCharDevice indicates a terminal
	if (stderrInfo.Mode() & os.ModeCharDevice) == 0 {
		return false
	}

	// Respect NO_COLOR environment variable (https://no-color.org/)
	if _, exists := os.LookupEnv("NO_COLOR"); exists {
		return false
	}

	// Check TERM isn't "dumb" (which doesn't support colors)
	if term := os.Getenv("TERM"); term == "dumb" {
		return false
	}

	return true
}

// NewLogger creates a new logger
func NewLogger(enabled bool) *Logger {
	return &Logger{
		enabled:           enabled,
		enabledCategories: make(map[LogCategory]bool),
		out:               os.Stdout,
		errOut:            os.Stderr,
		outputContext:     nil,
		colorEnabled:      stderrSupportsColor(),
	}
}

// SetOutputContext sets the output context for channel-based logging
// This should be called when entering a new execution scope
func (l *Logger) SetOutputContext(ctx *OutputContext) {
	l.outputContext = ctx
}

// ClearOutputContext clears the output context (reverts to direct io.Writer)
func (l *Logger) ClearOutputContext() {
	l.outputContext = nil
}

// WithContext returns a copy of the logger with the given output context
// This is useful for creating a logger bound to a specific execution state
func (l *Logger) WithContext(state *ExecutionState, executor *Executor) *Logger {
	return &Logger{
		enabled:           l.enabled,
		enabledCategories: l.enabledCategories,
		out:               l.out,
		errOut:            l.errOut,
		outputContext:     NewOutputContext(state, executor),
		colorEnabled:      l.colorEnabled,
	}
}

// writeOutput writes to the appropriate output (channel or direct writer)
// For debug output, uses #out channel; for errors/warnings, uses #err channel
func (l *Logger) writeOutput(isDebug bool, output string) {
	if l.outputContext != nil {
		var err error
		if isDebug {
			err = l.outputContext.WriteToOut(output + "\n")
		} else {
			err = l.outputContext.WriteToErr(output + "\n")
		}
		if err == nil {
			return // Successfully wrote to channel
		}
		// Fall through to direct writer on channel error
	}

	// Direct writer fallback (system console)
	if isDebug {
		_, _ = fmt.Fprintln(l.out, output)
	} else {
		// Apply color to error/warning output when writing to terminal
		if l.colorEnabled {
			_, _ = fmt.Fprintf(l.errOut, "%s%s%s\n", colorYellow, output, colorReset)
		} else {
			_, _ = fmt.Fprintln(l.errOut, output)
		}
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
	case LevelFatal, LevelError, LevelWarn:
		return true // Always shown
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

	// Route to appropriate output using channel-aware helper
	l.writeOutput(level == LevelDebug, output)
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

// CommandWarning logs a command warning with category
func (l *Logger) CommandWarning(cat LogCategory, cmdName, message string, position *SourcePosition) {
	fullMessage := message
	if cmdName != "" {
		fullMessage = fmt.Sprintf("%s: %s", strings.ToUpper(cmdName), message)
	}
	l.Log(LevelWarn, cat, fullMessage, position, nil)
}

// LogWithState logs a message using the given execution state for channel resolution
// This is useful when you want to log with a specific state context
func (l *Logger) LogWithState(level LogLevel, cat LogCategory, message string, position *SourcePosition, context []string, state *ExecutionState, executor *Executor) {
	if !l.shouldLog(level, cat) {
		return
	}

	// Temporarily set the output context
	oldCtx := l.outputContext
	l.outputContext = NewOutputContext(state, executor)
	defer func() { l.outputContext = oldCtx }()

	l.Log(level, cat, message, position, context)
}

// ErrorWithState logs an error message using the given execution state for channel resolution
func (l *Logger) ErrorWithState(message string, state *ExecutionState, executor *Executor) {
	l.LogWithState(LevelError, CatNone, message, nil, nil, state, executor)
}

// WarnWithState logs a warning message using the given execution state for channel resolution
func (l *Logger) WarnWithState(message string, state *ExecutionState, executor *Executor) {
	l.LogWithState(LevelWarn, CatNone, message, nil, nil, state, executor)
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
