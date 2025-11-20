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

// EarlyReturn represents early termination from a block
type EarlyReturn struct {
	Status    BoolStatus
	Result    interface{}
	HasResult bool
}

func (EarlyReturn) isResult() {}

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
	Type               string // "sequence", "conditional", "or"
	RemainingCommands  []*ParsedCommand
	CurrentIndex       int
	TotalCommands      int
	OriginalCommand    string
	Timestamp          time.Time
	InheritedResult    interface{}
	HasInheritedResult bool
	Position           *SourcePosition
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
	Evaluations       []*BraceEvaluation
	CompletedCount    int
	TotalCount        int
	HasFailure        bool
	FirstFailureError string
	OriginalString    string
	SubstitutionCtx   *SubstitutionContext
	ResumeCallback    func(finalString string, success bool) Result
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
	Args                []interface{}
	ExecutionState      *ExecutionState
	ParentContext       *SubstitutionContext
	MacroContext        *MacroContext
	CurrentLineOffset   int
	CurrentColumnOffset int
	Filename            string // Filename for error reporting
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

// StoredString represents a large string stored outside the argument flow
// Strings exceeding StringStorageThreshold are stored as objects and passed by reference
type StoredString string

func (s StoredString) String() string { return string(s) }

// StoredBlock represents a large code block stored outside the argument flow
// Blocks (ParenGroup) exceeding BlockStorageThreshold are stored as objects and passed by reference
type StoredBlock string

func (s StoredBlock) String() string { return string(s) }

// Storage thresholds - values larger than these are stored as objects
const (
	StringStorageThreshold = 200 // characters - strings larger than this become StoredString
	BlockStorageThreshold  = 500 // characters - blocks larger than this become StoredBlock
)

// StoredList represents an immutable list of values
// All operations return new StoredList instances (copy-on-write)
// Slicing shares the backing array for memory efficiency
type StoredList struct {
	items []interface{}
}

// NewStoredList creates a new StoredList from a slice of items
func NewStoredList(items []interface{}) StoredList {
	return StoredList{items: items}
}

// NewStoredListWithRefs creates a new StoredList and claims references to any nested objects
func NewStoredListWithRefs(items []interface{}, executor *Executor) StoredList {
	list := StoredList{items: items}
	// Claim references to any nested objects
	if executor != nil {
		for _, item := range items {
			claimNestedReferences(item, executor)
		}
	}
	return list
}

// claimNestedReferences recursively claims references to nested objects
func claimNestedReferences(value interface{}, executor *Executor) {
	switch v := value.(type) {
	case Symbol:
		if _, id := parseObjectMarker(string(v)); id >= 0 {
			executor.incrementObjectRefCount(id)
		}
	case string:
		if _, id := parseObjectMarker(v); id >= 0 {
			executor.incrementObjectRefCount(id)
		}
	case StoredList:
		// First, claim a reference to the list itself
		if id := executor.findStoredListID(v); id >= 0 {
			executor.incrementObjectRefCount(id)
		}
		// Then recursively claim references in nested list items
		for _, item := range v.Items() {
			claimNestedReferences(item, executor)
		}
	}
}

// releaseNestedReferences recursively releases references to nested objects
func releaseNestedReferences(value interface{}, executor *Executor) {
	switch v := value.(type) {
	case Symbol:
		if _, id := parseObjectMarker(string(v)); id >= 0 {
			executor.decrementObjectRefCount(id)
		}
	case string:
		if _, id := parseObjectMarker(v); id >= 0 {
			executor.decrementObjectRefCount(id)
		}
	case StoredList:
		// First, decrement refcount for the list itself
		// This mirrors what claimNestedReferences does when claiming
		if id := executor.findStoredListID(v); id >= 0 {
			executor.decrementObjectRefCount(id)
		}
		// Note: The nested list's items will be released when that list is freed
		// (when its refcount reaches 0), so we don't recursively release here
	}
}

// Items returns a copy of the underlying items slice
func (pl StoredList) Items() []interface{} {
	return pl.items
}

// Len returns the number of items in the list
func (pl StoredList) Len() int {
	return len(pl.items)
}

// Get returns the item at the given index (0-based)
// Returns nil if index is out of bounds
func (pl StoredList) Get(index int) interface{} {
	if index < 0 || index >= len(pl.items) {
		return nil
	}
	return pl.items[index]
}

// Slice returns a new StoredList with items from start to end (end exclusive)
// Shares the backing array for memory efficiency (O(1) time, O(1) space)
func (pl StoredList) Slice(start, end int) StoredList {
	if start < 0 {
		start = 0
	}
	if end > len(pl.items) {
		end = len(pl.items)
	}
	if start > end {
		start = end
	}
	return StoredList{items: pl.items[start:end]}
}

// Append returns a new StoredList with the item appended (O(n) copy-on-write)
func (pl StoredList) Append(item interface{}) StoredList {
	newItems := make([]interface{}, len(pl.items)+1)
	copy(newItems, pl.items)
	newItems[len(pl.items)] = item
	return StoredList{items: newItems}
}

// Prepend returns a new StoredList with the item prepended (O(n) copy-on-write)
func (pl StoredList) Prepend(item interface{}) StoredList {
	newItems := make([]interface{}, len(pl.items)+1)
	newItems[0] = item
	copy(newItems[1:], pl.items)
	return StoredList{items: newItems}
}

// Concat returns a new StoredList with items from both lists (O(n+m) copy)
func (pl StoredList) Concat(other StoredList) StoredList {
	newItems := make([]interface{}, len(pl.items)+len(other.items))
	copy(newItems, pl.items)
	copy(newItems[len(pl.items):], other.items)
	return StoredList{items: newItems}
}

// Compact returns a new StoredList with a new backing array
// Use this to free memory if you've sliced a large list
func (pl StoredList) Compact() StoredList {
	newItems := make([]interface{}, len(pl.items))
	copy(newItems, pl.items)
	return StoredList{items: newItems}
}

// String returns a string representation for debugging
func (pl StoredList) String() string {
	return "(list)"
}
