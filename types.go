package pawscript

import (
	"context"
	"time"
)

// SourcePosition tracks the position of code in source files
type SourcePosition struct {
	Line         int
	Column       int
	Length       int
	OriginalText string
	Filename     string
	MacroContext *MacroContext
}

// MacroContext tracks macro invocation chain for error reporting
type MacroContext struct {
	MacroName        string
	DefinitionFile   string
	DefinitionLine   int
	DefinitionColumn int
	InvocationFile   string
	InvocationLine   int
	InvocationColumn int
	ParentMacro      *MacroContext
}

// Context is passed to command handlers
type Context struct {
	Args         []interface{}
	Position     *SourcePosition
	state        *ExecutionState
	executor     *Executor
	requestToken func(cleanup func(string)) string
	resumeToken  func(tokenID string, result bool) bool
}

// SetResult sets the formal result value
func (c *Context) SetResult(value interface{}) {
	c.state.SetResult(value)
}

// GetResult gets the current result value
func (c *Context) GetResult() interface{} {
	return c.state.GetResult()
}

// HasResult checks if a result value exists
func (c *Context) HasResult() bool {
	return c.state.HasResult()
}

// ClearResult clears the result value
func (c *Context) ClearResult() {
	c.state.ClearResult()
}

// RequestToken requests an async completion token
func (c *Context) RequestToken(cleanup func(string)) string {
	return c.requestToken(cleanup)
}

// ResumeToken resumes execution with a token
func (c *Context) ResumeToken(tokenID string, result bool) bool {
	return c.resumeToken(tokenID, result)
}

// Handler is a function that handles a command
type Handler func(*Context) Result

// Result represents the result of command execution
type Result interface {
	isResult()
}

// BoolResult represents a boolean success/failure
type BoolResult bool

func (BoolResult) isResult() {}

// TokenResult represents an async token
type TokenResult string

func (TokenResult) isResult() {}

// ParsedCommand represents a parsed command with metadata
type ParsedCommand struct {
	Command      string
	Arguments    []interface{}
	Position     *SourcePosition
	OriginalLine string
	Separator    string // "none", ";", "&", "|"
}

// CommandSequence represents suspended command execution
type CommandSequence struct {
	Type              string // "sequence", "conditional", "or"
	RemainingCommands []*ParsedCommand
	CurrentIndex      int
	TotalCommands     int
	OriginalCommand   string
	Timestamp         time.Time
	InheritedResult   interface{}
	HasInheritedResult bool
	Position          *SourcePosition
}

// TokenData stores information about an active token
type TokenData struct {
	CommandSequence    *CommandSequence
	ParentToken        string
	Children           map[string]bool
	CleanupCallback    func(string)
	CancelFunc         context.CancelFunc
	ChainedToken       string
	Timestamp          time.Time
	ExecutionState     *ExecutionState
	SuspendedResult    interface{}
	HasSuspendedResult bool
	Position           *SourcePosition
}

// MacroDefinition stores a macro definition
type MacroDefinition struct {
	Name             string
	Commands         string
	DefinitionFile   string
	DefinitionLine   int
	DefinitionColumn int
	Timestamp        time.Time
}

// SubstitutionContext provides context for macro argument substitution
type SubstitutionContext struct {
	Args                 []interface{}
	ExecutionState       *ExecutionState
	ParentContext        *SubstitutionContext
	MacroContext         *MacroContext
	CurrentLineOffset    int
	CurrentColumnOffset  int
}

// Config holds configuration for PawScript
type Config struct {
	Debug                bool
	DefaultTokenTimeout  time.Duration
	EnableSyntacticSugar bool
	AllowMacros          bool
	ShowErrorContext     bool
	ContextLines         int
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
	return &Config{
		Debug:                false,
		DefaultTokenTimeout:  5 * time.Minute,
		EnableSyntacticSugar: true,
		AllowMacros:          true,
		ShowErrorContext:     true,
		ContextLines:         2,
	}
}

// PawScriptError represents an error with position information
type PawScriptError struct {
	Message  string
	Position *SourcePosition
	Context  []string
}

func (e *PawScriptError) Error() string {
	return e.Message
}
