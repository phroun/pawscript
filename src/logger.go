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
	// Fallback to system stdout (use executor's logger if available)
	if oc.Executor != nil && oc.Executor.logger != nil {
		_, err := fmt.Fprint(oc.Executor.logger.out, message)
		return err
	}
	_, err := fmt.Fprint(os.Stdout, message)
	return err
}

// WriteToErr writes a message to the resolved #err channel or falls back to system stderr
func (oc *OutputContext) WriteToErr(message string) error {
	if ch := oc.ResolveChannel("#err"); ch != nil {
		return ChannelSend(ch, message)
	}
	// Fallback to system stderr (use executor's logger if available)
	if oc.Executor != nil && oc.Executor.logger != nil {
		_, err := fmt.Fprint(oc.Executor.logger.errOut, message)
		return err
	}
	_, err := fmt.Fprint(os.Stderr, message)
	return err
}

// WriteToDebug writes a message to the resolved #debug channel or falls back to system stdout
// #debug is separate from #out to allow independent redirection of debug/log output
func (oc *OutputContext) WriteToDebug(message string) error {
	if ch := oc.ResolveChannel("#debug"); ch != nil {
		return ChannelSend(ch, message)
	}
	// Fallback to system stdout (use executor's logger if available)
	if oc.Executor != nil && oc.Executor.logger != nil {
		_, err := fmt.Fprint(oc.Executor.logger.out, message)
		return err
	}
	_, err := fmt.Fprint(os.Stdout, message)
	return err
}

// LogLevel represents the severity of a log message (higher value = higher severity)
type LogLevel int

const (
	LevelTrace  LogLevel = iota // Detailed tracing (requires enabled + category)
	LevelInfo                   // Informational messages (requires enabled + category)
	LevelDebug                  // Development debugging (requires enabled + category)
	LevelNotice                 // Notable events (always shown)
	LevelWarn                   // Warnings (always shown)
	LevelError                  // Runtime errors (always shown)
	LevelFatal                  // Parse/unknown command errors (always shown)
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
	CatNetwork  LogCategory = "network"  // Network operations
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

// AllLogCategories returns a slice of all defined log categories (excluding CatNone)
func AllLogCategories() []LogCategory {
	return []LogCategory{
		CatParse, CatCommand, CatVariable, CatArgument, CatIO, CatNetwork,
		CatMacro, CatAsync, CatMemory, CatMath, CatList, CatString,
		CatType, CatFlow, CatSystem, CatApp, CatUser,
	}
}

// LogFilter controls which log messages pass through to an output channel
// A message passes the filter if:
//   - Level >= Force (always pass), OR
//   - Level >= Floor AND Level >= threshold
//     (where threshold is Categories[cat] if exists, else Default)
type LogFilter struct {
	Default    LogLevel                // Threshold for categories not in the map
	Categories map[LogCategory]LogLevel // Per-category thresholds
	Floor      LogLevel                // Global floor: nothing below this ever shows
	Force      LogLevel                // Global force: everything at/above this always shows
}

// NewLogFilter creates a new LogFilter with sensible defaults
func NewLogFilter(defaultLevel LogLevel) *LogFilter {
	return &LogFilter{
		Default:    defaultLevel,
		Categories: make(map[LogCategory]LogLevel),
		Floor:      LevelTrace, // Don't block anything by default
		Force:      LevelFatal, // Only Fatal forces through by default
	}
}

// Passes checks if a message with the given level and category passes this filter
func (f *LogFilter) Passes(level LogLevel, cat LogCategory) bool {
	// Force: always pass if level >= Force
	if level >= f.Force {
		return true
	}

	// Floor: never pass if level < Floor
	if level < f.Floor {
		return false
	}

	// Get threshold for this category (use Default if not specified)
	threshold := f.Default
	if catLevel, exists := f.Categories[cat]; exists {
		threshold = catLevel
	}

	return level >= threshold
}

// PassesAny checks if a message passes the filter for ANY of the given categories
// This allows a message to be tagged with multiple categories (e.g., CatCommand + CatMath)
// and pass if any of those categories' thresholds are met
func (f *LogFilter) PassesAny(level LogLevel, cats []LogCategory) bool {
	// Force: always pass if level >= Force
	if level >= f.Force {
		return true
	}

	// Floor: never pass if level < Floor
	if level < f.Floor {
		return false
	}

	// Check each category - pass if ANY category's threshold is met
	for _, cat := range cats {
		threshold := f.Default
		if catLevel, exists := f.Categories[cat]; exists {
			threshold = catLevel
		}
		if level >= threshold {
			return true
		}
	}

	// No category passed
	return false
}

// Copy creates a deep copy of the LogFilter for COW semantics
func (f *LogFilter) Copy() *LogFilter {
	if f == nil {
		return nil
	}
	newFilter := &LogFilter{
		Default:    f.Default,
		Categories: make(map[LogCategory]LogLevel, len(f.Categories)),
		Floor:      f.Floor,
		Force:      f.Force,
	}
	for k, v := range f.Categories {
		newFilter.Categories[k] = v
	}
	return newFilter
}

// LogConfig holds the two log filter systems (error_logging and debug_logging)
// ErrorLog controls what goes to #err; DebugLog controls what goes to #debug
// A message can pass both filters and appear in both outputs
type LogConfig struct {
	ErrorLog *LogFilter // Filter for #err output
	DebugLog *LogFilter // Filter for #debug output
}

// LevelNone represents "off" - threshold higher than any level, so nothing passes
const LevelNone LogLevel = LevelFatal + 1

// NewLogConfig creates a new LogConfig with default settings
// ErrorLog defaults to showing Warn and above; DebugLog defaults to showing nothing
func NewLogConfig() *LogConfig {
	errorFilter := NewLogFilter(LevelWarn) // Default: show Warn and above on #err
	debugFilter := NewLogFilter(LevelNone) // Default: show nothing on #debug

	return &LogConfig{
		ErrorLog: errorFilter,
		DebugLog: debugFilter,
	}
}

// Copy creates a deep copy of the LogConfig for COW semantics
func (c *LogConfig) Copy() *LogConfig {
	if c == nil {
		return nil
	}
	return &LogConfig{
		ErrorLog: c.ErrorLog.Copy(),
		DebugLog: c.DebugLog.Copy(),
	}
}

// LogLevelFromString converts a string name to a LogLevel
// Returns -1 if the string is not a valid level name
func LogLevelFromString(name string) LogLevel {
	switch strings.ToLower(name) {
	case "trace":
		return LevelTrace
	case "info":
		return LevelInfo
	case "debug":
		return LevelDebug
	case "notice":
		return LevelNotice
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	case "fatal":
		return LevelFatal
	case "none", "off":
		return LevelNone
	default:
		return LogLevel(-1)
	}
}

// LogLevelToString converts a LogLevel to its string name
func LogLevelToString(level LogLevel) string {
	switch level {
	case LevelTrace:
		return "trace"
	case LevelInfo:
		return "info"
	case LevelDebug:
		return "debug"
	case LevelNotice:
		return "notice"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	case LevelFatal:
		return "fatal"
	case LevelNone:
		return "none"
	default:
		return "unknown"
	}
}

// LogCategoryFromString converts a string name to a LogCategory
// Returns CatNone if the string is not a valid category name
func LogCategoryFromString(name string) (LogCategory, bool) {
	switch strings.ToLower(name) {
	case "parse":
		return CatParse, true
	case "command":
		return CatCommand, true
	case "variable":
		return CatVariable, true
	case "argument":
		return CatArgument, true
	case "io":
		return CatIO, true
	case "network", "net":
		return CatNetwork, true
	case "macro":
		return CatMacro, true
	case "async":
		return CatAsync, true
	case "memory":
		return CatMemory, true
	case "math":
		return CatMath, true
	case "list":
		return CatList, true
	case "string":
		return CatString, true
	case "type":
		return CatType, true
	case "flow":
		return CatFlow, true
	case "system":
		return CatSystem, true
	case "app":
		return CatApp, true
	case "user":
		return CatUser, true
	default:
		return CatNone, false
	}
}

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

// NewLogger creates a new logger with default stdout/stderr
func NewLogger(enabled bool) *Logger {
	return NewLoggerWithWriters(enabled, os.Stdout, os.Stderr)
}

// NewLoggerWithWriters creates a new logger with custom writers
func NewLoggerWithWriters(enabled bool, stdout, stderr io.Writer) *Logger {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	return &Logger{
		enabled:           enabled,
		enabledCategories: make(map[LogCategory]bool),
		out:               stdout,
		errOut:            stderr,
		outputContext:     nil,
		colorEnabled:      stderrSupportsColor(),
	}
}

// GetStdout returns the stdout writer
func (l *Logger) GetStdout() io.Writer {
	return l.out
}

// GetStderr returns the stderr writer
func (l *Logger) GetStderr() io.Writer {
	return l.errOut
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
	case LevelFatal, LevelError, LevelWarn, LevelNotice:
		return true // Always shown
	case LevelDebug, LevelInfo, LevelTrace:
		return l.enabled && (cat == CatNone || l.enabledCategories[cat])
	default:
		return false
	}
}

// Log is the unified logging method
func (l *Logger) Log(level LogLevel, cat LogCategory, message string, position *SourcePosition, context []string) {
	// Get LogConfig from output context's module environment (if available)
	var logConfig *LogConfig
	if l.outputContext != nil && l.outputContext.State != nil && l.outputContext.State.moduleEnv != nil {
		logConfig = l.outputContext.State.moduleEnv.GetLogConfig()
	}

	// Determine which outputs this message should go to
	sendToErr := false
	sendToOut := false

	if logConfig != nil {
		// Use LogConfig for filtering
		sendToErr = logConfig.ErrorLog.Passes(level, cat)
		sendToOut = logConfig.DebugLog.Passes(level, cat)
	} else {
		// Legacy behavior: use the old shouldLog logic
		if !l.shouldLog(level, cat) {
			return
		}
		// Default routing: low severity to stdout, high severity to stderr
		isLowSeverity := level == LevelTrace || level == LevelInfo || level == LevelDebug
		sendToErr = !isLowSeverity
		sendToOut = isLowSeverity
	}

	// If nothing passes, don't log
	if !sendToErr && !sendToOut {
		return
	}

	var prefix string
	catSuffix := ""
	if cat != CatNone {
		catSuffix = fmt.Sprintf(":%s", cat)
	}

	switch level {
	case LevelTrace:
		prefix = fmt.Sprintf("[TRACE%s]", catSuffix)
	case LevelInfo:
		prefix = fmt.Sprintf("[INFO%s]", catSuffix)
	case LevelDebug:
		prefix = fmt.Sprintf("[DEBUG%s]", catSuffix)
	case LevelNotice:
		prefix = fmt.Sprintf("[PawScript%s NOTICE]", catSuffix)
	case LevelWarn:
		prefix = fmt.Sprintf("[PawScript%s WARN]", catSuffix)
	case LevelError, LevelFatal:
		prefix = fmt.Sprintf("[PawScript%s ERROR]", catSuffix)
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

	// Send to each destination that passed its filter
	if sendToErr {
		l.writeOutputToErr(output)
	}
	if sendToOut {
		l.writeOutputToDebug(output)
	}
}

// LogMulti is like Log but accepts multiple categories
// A message passes a filter if ANY of its categories pass that filter's threshold
// The first category in the list is used for the message prefix
func (l *Logger) LogMulti(level LogLevel, cats []LogCategory, message string, position *SourcePosition, context []string) {
	// Handle empty or single category case
	if len(cats) == 0 {
		l.Log(level, CatNone, message, position, context)
		return
	}
	if len(cats) == 1 {
		l.Log(level, cats[0], message, position, context)
		return
	}

	// Get LogConfig from output context's module environment (if available)
	var logConfig *LogConfig
	if l.outputContext != nil && l.outputContext.State != nil && l.outputContext.State.moduleEnv != nil {
		logConfig = l.outputContext.State.moduleEnv.GetLogConfig()
	}

	// Determine which outputs this message should go to
	sendToErr := false
	sendToOut := false

	if logConfig != nil {
		// Use LogConfig for filtering - pass if ANY category passes
		sendToErr = logConfig.ErrorLog.PassesAny(level, cats)
		sendToOut = logConfig.DebugLog.PassesAny(level, cats)
	} else {
		// Legacy behavior: use the old shouldLog logic with first category
		if !l.shouldLog(level, cats[0]) {
			return
		}
		// Default routing: low severity to stdout, high severity to stderr
		isLowSeverity := level == LevelTrace || level == LevelInfo || level == LevelDebug
		sendToErr = !isLowSeverity
		sendToOut = isLowSeverity
	}

	// If nothing passes, don't log
	if !sendToErr && !sendToOut {
		return
	}

	// Build category suffix showing all categories (e.g., ":command+math")
	var catSuffix string
	if cats[0] != CatNone {
		catParts := make([]string, 0, len(cats))
		for _, cat := range cats {
			if cat != CatNone {
				catParts = append(catParts, string(cat))
			}
		}
		if len(catParts) > 0 {
			catSuffix = ":" + strings.Join(catParts, "+")
		}
	}

	var prefix string
	switch level {
	case LevelTrace:
		prefix = fmt.Sprintf("[TRACE%s]", catSuffix)
	case LevelInfo:
		prefix = fmt.Sprintf("[INFO%s]", catSuffix)
	case LevelDebug:
		prefix = fmt.Sprintf("[DEBUG%s]", catSuffix)
	case LevelNotice:
		prefix = fmt.Sprintf("[PawScript%s NOTICE]", catSuffix)
	case LevelWarn:
		prefix = fmt.Sprintf("[PawScript%s WARN]", catSuffix)
	case LevelError, LevelFatal:
		prefix = fmt.Sprintf("[PawScript%s ERROR]", catSuffix)
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

	// Send to each destination that passed its filter
	if sendToErr {
		l.writeOutputToErr(output)
	}
	if sendToOut {
		l.writeOutputToDebug(output)
	}
}

// writeOutputToErr writes to #err channel or stderr
func (l *Logger) writeOutputToErr(output string) {
	if l.outputContext != nil {
		if err := l.outputContext.WriteToErr(output + "\n"); err == nil {
			return // Successfully wrote to channel
		}
		// Fall through to direct writer on channel error
	}

	// Direct writer fallback (system stderr)
	if l.colorEnabled {
		_, _ = fmt.Fprintf(l.errOut, "%s%s%s\n", colorYellow, output, colorReset)
	} else {
		_, _ = fmt.Fprintln(l.errOut, output)
	}
}

// writeOutputToDebug writes to #debug channel or stdout (for debug logging output)
// Uses #debug instead of #out to allow independent redirection of debug output
func (l *Logger) writeOutputToDebug(output string) {
	if l.outputContext != nil {
		if err := l.outputContext.WriteToDebug(output + "\n"); err == nil {
			return // Successfully wrote to channel
		}
		// Fall through to direct writer on channel error
	}

	// Direct writer fallback (system stdout)
	_, _ = fmt.Fprintln(l.out, output)
}

// Convenience methods that route through Log
// Ordered by severity: Fatal, Error, Warn, Notice, Debug, Info, Trace

// Fatal logs a fatal error message (no position) - use ParseError or UnknownCommandError for positioned errors
func (l *Logger) Fatal(format string, args ...interface{}) {
	l.Log(LevelFatal, CatNone, fmt.Sprintf(format, args...), nil, nil)
}

// FatalCat logs a categorized fatal error message
func (l *Logger) FatalCat(cat LogCategory, format string, args ...interface{}) {
	l.Log(LevelFatal, cat, fmt.Sprintf(format, args...), nil, nil)
}

// Error logs an error message (no position)
func (l *Logger) Error(format string, args ...interface{}) {
	l.Log(LevelError, CatNone, fmt.Sprintf(format, args...), nil, nil)
}

// ErrorCat logs a categorized error message
func (l *Logger) ErrorCat(cat LogCategory, format string, args ...interface{}) {
	l.Log(LevelError, cat, fmt.Sprintf(format, args...), nil, nil)
}

// Warn logs a warning message (no position)
func (l *Logger) Warn(format string, args ...interface{}) {
	l.Log(LevelWarn, CatNone, fmt.Sprintf(format, args...), nil, nil)
}

// WarnCat logs a categorized warning message
func (l *Logger) WarnCat(cat LogCategory, format string, args ...interface{}) {
	l.Log(LevelWarn, cat, fmt.Sprintf(format, args...), nil, nil)
}

// Notice logs a notable event (no position) - always shown, less severe than warning
func (l *Logger) Notice(format string, args ...interface{}) {
	l.Log(LevelNotice, CatNone, fmt.Sprintf(format, args...), nil, nil)
}

// NoticeCat logs a categorized notice message
func (l *Logger) NoticeCat(cat LogCategory, format string, args ...interface{}) {
	l.Log(LevelNotice, cat, fmt.Sprintf(format, args...), nil, nil)
}

// Debug logs a debug message (no position)
func (l *Logger) Debug(format string, args ...interface{}) {
	l.Log(LevelDebug, CatNone, fmt.Sprintf(format, args...), nil, nil)
}

// DebugCat logs a categorized debug message
func (l *Logger) DebugCat(cat LogCategory, format string, args ...interface{}) {
	l.Log(LevelDebug, cat, fmt.Sprintf(format, args...), nil, nil)
}

// Info logs an informational message (no position)
func (l *Logger) Info(format string, args ...interface{}) {
	l.Log(LevelInfo, CatNone, fmt.Sprintf(format, args...), nil, nil)
}

// InfoCat logs a categorized informational message
func (l *Logger) InfoCat(cat LogCategory, format string, args ...interface{}) {
	l.Log(LevelInfo, cat, fmt.Sprintf(format, args...), nil, nil)
}

// Trace logs a detailed trace message (no position)
func (l *Logger) Trace(format string, args ...interface{}) {
	l.Log(LevelTrace, CatNone, fmt.Sprintf(format, args...), nil, nil)
}

// TraceCat logs a categorized trace message
func (l *Logger) TraceCat(cat LogCategory, format string, args ...interface{}) {
	l.Log(LevelTrace, cat, fmt.Sprintf(format, args...), nil, nil)
}

// Multi-category convenience methods - use when a message belongs to multiple categories
// Message passes if ANY category's filter threshold is met

// ErrorMulti logs an error with multiple categories
func (l *Logger) ErrorMulti(cats []LogCategory, format string, args ...interface{}) {
	l.LogMulti(LevelError, cats, fmt.Sprintf(format, args...), nil, nil)
}

// WarnMulti logs a warning with multiple categories
func (l *Logger) WarnMulti(cats []LogCategory, format string, args ...interface{}) {
	l.LogMulti(LevelWarn, cats, fmt.Sprintf(format, args...), nil, nil)
}

// NoticeMulti logs a notice with multiple categories
func (l *Logger) NoticeMulti(cats []LogCategory, format string, args ...interface{}) {
	l.LogMulti(LevelNotice, cats, fmt.Sprintf(format, args...), nil, nil)
}

// DebugMulti logs a debug message with multiple categories
func (l *Logger) DebugMulti(cats []LogCategory, format string, args ...interface{}) {
	l.LogMulti(LevelDebug, cats, fmt.Sprintf(format, args...), nil, nil)
}

// InfoMulti logs an info message with multiple categories
func (l *Logger) InfoMulti(cats []LogCategory, format string, args ...interface{}) {
	l.LogMulti(LevelInfo, cats, fmt.Sprintf(format, args...), nil, nil)
}

// TraceMulti logs a trace message with multiple categories
func (l *Logger) TraceMulti(cats []LogCategory, format string, args ...interface{}) {
	l.LogMulti(LevelTrace, cats, fmt.Sprintf(format, args...), nil, nil)
}

// ErrorWithPosition logs an error with position information
func (l *Logger) ErrorWithPosition(cat LogCategory, message string, position *SourcePosition, context []string) {
	l.Log(LevelError, cat, message, position, context)
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

// FatalWithState logs a fatal error message using the given execution state for channel resolution
func (l *Logger) FatalWithState(cat LogCategory, message string, state *ExecutionState, executor *Executor) {
	l.LogWithState(LevelFatal, cat, message, nil, nil, state, executor)
}

// ErrorWithState logs an error message using the given execution state for channel resolution
func (l *Logger) ErrorWithState(cat LogCategory, message string, state *ExecutionState, executor *Executor) {
	l.LogWithState(LevelError, cat, message, nil, nil, state, executor)
}

// WarnWithState logs a warning message using the given execution state for channel resolution
func (l *Logger) WarnWithState(cat LogCategory, message string, state *ExecutionState, executor *Executor) {
	l.LogWithState(LevelWarn, cat, message, nil, nil, state, executor)
}

// NoticeWithState logs a notice message using the given execution state for channel resolution
func (l *Logger) NoticeWithState(cat LogCategory, message string, state *ExecutionState, executor *Executor) {
	l.LogWithState(LevelNotice, cat, message, nil, nil, state, executor)
}

// DebugWithState logs a debug message using the given execution state for channel resolution
func (l *Logger) DebugWithState(cat LogCategory, message string, state *ExecutionState, executor *Executor) {
	l.LogWithState(LevelDebug, cat, message, nil, nil, state, executor)
}

// InfoWithState logs an info message using the given execution state for channel resolution
func (l *Logger) InfoWithState(cat LogCategory, message string, state *ExecutionState, executor *Executor) {
	l.LogWithState(LevelInfo, cat, message, nil, nil, state, executor)
}

// TraceWithState logs a trace message using the given execution state for channel resolution
func (l *Logger) TraceWithState(cat LogCategory, message string, state *ExecutionState, executor *Executor) {
	l.LogWithState(LevelTrace, cat, message, nil, nil, state, executor)
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
