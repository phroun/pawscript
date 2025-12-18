// Package pawscript provides a scripting language interpreter that can be
// embedded in Go applications.
//
// This package re-exports the public API from the implementation in src/.
// For full documentation, see the implementation package.
//
// Basic usage:
//
//	ps := pawscript.New(&pawscript.Config{
//		Debug:       false,
//		AllowMacros: true,
//	})
//	ps.RegisterStandardLibrary(nil)
//	ps.Execute("echo 'Hello, World!'")
package pawscript

import (
	"time"

	impl "github.com/phroun/pawscript/src"
)

// =============================================================================
// CORE TYPES
// =============================================================================

// PawScript is the main interpreter instance.
type PawScript = impl.PawScript

// Config holds configuration options for the interpreter.
type Config = impl.Config

// Context is passed to command handlers during execution.
type Context = impl.Context

// Result is the interface returned by command handlers.
type Result = impl.Result

// Handler is the function signature for command handlers.
type Handler = impl.Handler

// =============================================================================
// RESULT TYPES
// =============================================================================

// BoolStatus is a boolean result from command execution.
type BoolStatus = impl.BoolStatus

// TokenResult represents an async token for suspended operations.
type TokenResult = impl.TokenResult

// EarlyReturn signals early termination from a block.
type EarlyReturn = impl.EarlyReturn

// YieldResult signals yielding from a generator.
type YieldResult = impl.YieldResult

// BreakResult signals break from a loop.
type BreakResult = impl.BreakResult

// ContinueResult signals continue in a loop.
type ContinueResult = impl.ContinueResult

// SuspendResult signals suspension of execution.
type SuspendResult = impl.SuspendResult

// =============================================================================
// EXECUTION STATE
// =============================================================================

// ExecutionState manages result state and variables during execution.
type ExecutionState = impl.ExecutionState

// SourcePosition tracks location in source code for error reporting.
type SourcePosition = impl.SourcePosition

// MacroContext tracks macro invocation chain for debugging.
type MacroContext = impl.MacroContext

// =============================================================================
// CONFIGURATION TYPES
// =============================================================================

// OptimizationLevel controls optimization passes.
type OptimizationLevel = impl.OptimizationLevel

// Optimization level constants.
const (
	OptimizeNone  = impl.OptimizeNone
	OptimizeBasic = impl.OptimizeBasic
)

// FileAccessConfig controls file system access permissions.
type FileAccessConfig = impl.FileAccessConfig

// DisplayColorConfig holds display color settings.
type DisplayColorConfig = impl.DisplayColorConfig

// =============================================================================
// DATA TYPES
// =============================================================================

// StoredList is an immutable list with optional named arguments.
type StoredList = impl.StoredList

// StoredBytes is an immutable byte array.
type StoredBytes = impl.StoredBytes

// StoredStruct is an instance of a defined struct type.
type StoredStruct = impl.StoredStruct

// StoredChannel is a bidirectional communication channel.
type StoredChannel = impl.StoredChannel

// StoredFile is an open file handle.
type StoredFile = impl.StoredFile

// StoredMacro is a macro stored as a reference-counted object.
type StoredMacro = impl.StoredMacro

// StoredCommand wraps a command handler for storage.
type StoredCommand = impl.StoredCommand

// ListTypeInfo tracks type information for lists.
type ListTypeInfo = impl.ListTypeInfo

// =============================================================================
// STRING AND VALUE TYPES
// =============================================================================

// QuotedString represents a quoted string value.
type QuotedString = impl.QuotedString

// Symbol represents a bare identifier.
type Symbol = impl.Symbol

// ParenGroup represents a parenthesized value.
type ParenGroup = impl.ParenGroup

// StoredString represents a large string stored by reference.
type StoredString = impl.StoredString

// StoredBlock represents a large code block stored by reference.
type StoredBlock = impl.StoredBlock

// ActualUndefined represents the undefined value type.
// Use ActualUndefined{} to create an instance.
type ActualUndefined = impl.ActualUndefined

// Storage thresholds.
const (
	StringStorageThreshold = impl.StringStorageThreshold
	BlockStorageThreshold  = impl.BlockStorageThreshold
)

// =============================================================================
// MODULE SYSTEM
// =============================================================================

// ModuleEnvironment manages module state.
type ModuleEnvironment = impl.ModuleEnvironment

// ModuleItem represents an exported item (command, macro, or object).
type ModuleItem = impl.ModuleItem

// ModuleSection is a map of items in a module section.
type ModuleSection = impl.ModuleSection

// Library is a collection of module sections.
type Library = impl.Library

// ItemMetadata provides comprehensive metadata tracking.
type ItemMetadata = impl.ItemMetadata

// MacroDefinition holds macro definition data.
type MacroDefinition = impl.MacroDefinition

// =============================================================================
// LOGGING
// =============================================================================

// LogLevel represents log severity.
type LogLevel = impl.LogLevel

// Log level constants.
const (
	LevelTrace  = impl.LevelTrace
	LevelInfo   = impl.LevelInfo
	LevelDebug  = impl.LevelDebug
	LevelNotice = impl.LevelNotice
	LevelWarn   = impl.LevelWarn
	LevelError  = impl.LevelError
	LevelFatal  = impl.LevelFatal
)

// LogCategory identifies the logging subsystem.
type LogCategory = impl.LogCategory

// Log category constants.
const (
	CatNone     = impl.CatNone
	CatParse    = impl.CatParse
	CatCommand  = impl.CatCommand
	CatVariable = impl.CatVariable
	CatArgument = impl.CatArgument
	CatIO       = impl.CatIO
	CatNetwork  = impl.CatNetwork
	CatMacro    = impl.CatMacro
	CatAsync    = impl.CatAsync
	CatMemory   = impl.CatMemory
	CatMath     = impl.CatMath
	CatList     = impl.CatList
	CatString   = impl.CatString
	CatType     = impl.CatType
	CatFlow     = impl.CatFlow
	CatSystem   = impl.CatSystem
	CatApp      = impl.CatApp
	CatUser     = impl.CatUser
)

// OutputContext provides logger context.
type OutputContext = impl.OutputContext

// =============================================================================
// PSL (PAWSCRIPT SERIALIZED LIST)
// =============================================================================

// PSLMap is a map for PSL serialization.
type PSLMap = impl.PSLMap

// PSLConfig is an alias for PSLMap.
type PSLConfig = impl.PSLConfig

// PSLList is a list for PSL serialization.
type PSLList = impl.PSLList

// =============================================================================
// ASYNC AND FIBER TYPES
// =============================================================================

// FiberHandle is a lightweight thread handle.
type FiberHandle = impl.FiberHandle

// ResumeData holds resume information.
type ResumeData = impl.ResumeData

// TokenData holds information about an active async token.
type TokenData = impl.TokenData

// ChannelMessage is a message in a channel buffer.
type ChannelMessage = impl.ChannelMessage

// TerminalCapabilities describes terminal features.
type TerminalCapabilities = impl.TerminalCapabilities

// =============================================================================
// ERROR TYPES
// =============================================================================

// PawScriptError is an error with position information.
type PawScriptError = impl.PawScriptError

// =============================================================================
// PARSING TYPES
// =============================================================================

// ParsedCommand is a parsed command with metadata.
type ParsedCommand = impl.ParsedCommand

// CommandSequence represents suspended command execution.
type CommandSequence = impl.CommandSequence

// =============================================================================
// BUBBLE SYSTEM
// =============================================================================

// BubbleEntry is a single bubble for out-of-band values.
type BubbleEntry = impl.BubbleEntry

// =============================================================================
// CONSTRUCTOR FUNCTIONS
// =============================================================================

// New creates a new PawScript interpreter with the given configuration.
func New(config *Config) *PawScript {
	return impl.New(config)
}

// DefaultConfig returns a new Config with default values.
func DefaultConfig() *Config {
	return impl.DefaultConfig()
}

// DefaultDisplayColors returns the default display color configuration.
func DefaultDisplayColors() DisplayColorConfig {
	return impl.DefaultDisplayColors()
}

// =============================================================================
// EXECUTION STATE CONSTRUCTORS
// =============================================================================

// NewExecutionState creates a new execution state.
func NewExecutionState() *ExecutionState {
	return impl.NewExecutionState()
}

// NewExecutionStateFrom creates a child execution state.
func NewExecutionStateFrom(parent *ExecutionState) *ExecutionState {
	return impl.NewExecutionStateFrom(parent)
}

// NewExecutionStateFromSharedVars creates a state with shared variables.
func NewExecutionStateFromSharedVars(parent *ExecutionState) *ExecutionState {
	return impl.NewExecutionStateFromSharedVars(parent)
}

// =============================================================================
// DATA TYPE CONSTRUCTORS
// =============================================================================

// NewStoredListWithoutRefs creates a list without reference tracking.
func NewStoredListWithoutRefs(items []interface{}) StoredList {
	return impl.NewStoredListWithoutRefs(items)
}

// NewStoredListWithNamed creates a list with named arguments.
func NewStoredListWithNamed(items []interface{}, namedArgs map[string]interface{}) StoredList {
	return impl.NewStoredListWithNamed(items, namedArgs)
}

// NewStoredBytes creates a new byte array.
func NewStoredBytes(data []byte) StoredBytes {
	return impl.NewStoredBytes(data)
}

// NewStoredBytesFromInts creates a byte array from int64 values.
func NewStoredBytesFromInts(values []int64) StoredBytes {
	return impl.NewStoredBytesFromInts(values)
}

// StoredBytesFromString parses a string into a StoredBytes.
func StoredBytesFromString(s string) (StoredBytes, error) {
	return impl.StoredBytesFromString(s)
}

// NewStoredChannel creates a new channel with the given buffer size.
func NewStoredChannel(bufferSize int) *StoredChannel {
	return impl.NewStoredChannel(bufferSize)
}

// NewStoredCommand creates a new stored command.
func NewStoredCommand(name string, handler Handler) StoredCommand {
	return impl.NewStoredCommand(name, handler)
}

// NewStoredMacro creates a new stored macro.
func NewStoredMacro(commands string, position *SourcePosition) StoredMacro {
	return impl.NewStoredMacro(commands, position)
}

// NewStoredMacroWithEnv creates a macro with module environment.
func NewStoredMacroWithEnv(commands string, position *SourcePosition, moduleEnv *ModuleEnvironment) StoredMacro {
	return impl.NewStoredMacroWithEnv(commands, position, moduleEnv)
}

// NewStoredStruct creates a new struct instance.
func NewStoredStruct(defID int, size int) StoredStruct {
	return impl.NewStoredStruct(defID, size)
}

// NewStoredStructArray creates a struct array.
func NewStoredStructArray(defID int, size int, n int) StoredStruct {
	return impl.NewStoredStructArray(defID, size, n)
}

// NewEmptyTypeInfo creates empty type info.
func NewEmptyTypeInfo() ListTypeInfo {
	return impl.NewEmptyTypeInfo()
}

// =============================================================================
// MODULE CONSTRUCTORS
// =============================================================================

// NewModuleEnvironment creates a new module environment.
func NewModuleEnvironment() *ModuleEnvironment {
	return impl.NewModuleEnvironment()
}

// NewChildModuleEnvironment creates a child module environment.
func NewChildModuleEnvironment(parent *ModuleEnvironment) *ModuleEnvironment {
	return impl.NewChildModuleEnvironment(parent)
}

// NewMacroModuleEnvironment creates a module environment for macros.
func NewMacroModuleEnvironment(parent *ModuleEnvironment) *ModuleEnvironment {
	return impl.NewMacroModuleEnvironment(parent)
}

// =============================================================================
// LOGGING FUNCTIONS
// =============================================================================

// AllLogCategories returns all available log categories.
func AllLogCategories() []LogCategory {
	return impl.AllLogCategories()
}

// NewOutputContext creates a new output context.
func NewOutputContext(state *ExecutionState, executor interface{}) *OutputContext {
	// Note: executor parameter is *Executor internally but we accept interface{}
	// to avoid exposing internal types
	return impl.NewOutputContext(state, nil)
}

// =============================================================================
// PSL FUNCTIONS
// =============================================================================

// SerializePSL serializes a map to PSL format.
func SerializePSL(config PSLMap) string {
	return impl.SerializePSL(config)
}

// SerializePSLPretty serializes a map to pretty PSL format.
func SerializePSLPretty(config PSLMap) string {
	return impl.SerializePSLPretty(config)
}

// SerializePSLList serializes a list to PSL format.
func SerializePSLList(list PSLList) string {
	return impl.SerializePSLList(list)
}

// ParsePSL parses a PSL string into a map.
func ParsePSL(input string) (PSLMap, error) {
	return impl.ParsePSL(input)
}

// ParsePSLList parses a PSL string into a list.
func ParsePSLList(input string) (PSLList, error) {
	return impl.ParsePSLList(input)
}

// =============================================================================
// REPL AND TERMINAL
// =============================================================================

// REPL is the Read-Eval-Print Loop for interactive sessions.
type REPL = impl.REPL

// ObjectRef is a reference to a stored object.
type ObjectRef = impl.ObjectRef

// NewREPLWithInterpreter creates a REPL with an existing interpreter.
func NewREPLWithInterpreter(ps *PawScript, output func(string)) *REPL {
	return impl.NewREPLWithInterpreter(ps, output)
}

// CleanupTerminal restores terminal to normal state.
func CleanupTerminal() {
	impl.CleanupTerminal()
}

// =============================================================================
// CHANNEL FUNCTIONS
// =============================================================================

// ChannelRecv receives a message from a channel.
func ChannelRecv(ch *StoredChannel) (int, interface{}, error) {
	return impl.ChannelRecv(ch)
}

// =============================================================================
// FORMATTING FUNCTIONS
// =============================================================================

// FormatValueColored formats a value with ANSI colors.
func FormatValueColored(value interface{}, pretty bool, cfg DisplayColorConfig, ps *PawScript) string {
	return impl.FormatValueColored(value, pretty, cfg, ps)
}

// =============================================================================
// ASYNC TOKEN FUNCTIONS
// =============================================================================

// RequestToken is a convenience function for creating async tokens.
// For most use cases, use Context.RequestToken() instead.
func RequestToken(ps *PawScript, cleanup func(string), parentToken string, timeout time.Duration) string {
	return ps.RequestToken(cleanup, parentToken, timeout)
}
