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
	resumeToken  func(tokenID string, status bool) bool
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
func (c *Context) ResumeToken(tokenID string, status bool) bool {
	return c.resumeToken(tokenID, status)
}

// Handler is a function that handles a command
type Handler func(*Context) Result

// Result represents the result of command execution (either a status or async token)
type Result interface {
	isResult()
}

// BoolStatus represents a boolean success/failure status
type BoolStatus bool

func (BoolStatus) isResult() {}

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

// BraceLocation tracks the position of a brace expression in the source string
type BraceLocation struct {
	StartPos         int
	EndPos           int
	Content          string
	StartLine        int
	StartColumn      int
	PositionInParent *SourcePosition
	IsUnescape       bool // true if ${...}, false if {...}
}

// BraceEvaluation tracks the evaluation state of a single brace expression
type BraceEvaluation struct {
	Location  *BraceLocation
	TokenID   string      // Token ID if async, empty if sync
	Result    interface{} // Result value when completed
	State     *ExecutionState
	IsAsync   bool
	Completed bool
	Failed    bool
	Error     string
	Position  *SourcePosition // Position of the content inside the brace (for error reporting)
}

// BraceCoordinator manages parallel brace evaluation
type BraceCoordinator struct {
	Evaluations        []*BraceEvaluation
	CompletedCount     int
	TotalCount         int
	HasFailure         bool
	FirstFailureError  string
	OriginalString     string
	SubstitutionCtx    *SubstitutionContext
	ResumeCallback     func(finalString string, success bool) Result
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
	BraceCoordinator   *BraceCoordinator // For coordinating parallel brace evaluation
	InvertStatus       bool              // If true, invert the success status when this token completes
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
	Filename             string // Filename for error reporting
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

// ParenGroup represents a value that was originally in parentheses
// This preserves the original form for $* substitution
type ParenGroup string

func (p ParenGroup) String() string { return string(p) }

// QuotedString represents a value that was originally quoted
// This preserves the original form for $* substitution
type QuotedString string

func (q QuotedString) String() string { return string(q) }

// Symbol represents a bare identifier (unquoted, non-keyword)
// This preserves the original form and allows type detection
type Symbol string

func (s Symbol) String() string { return string(s) }
