package pawscript

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"sync"
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
	RawArgs      []string               // Original argument strings before resolution (for diagnostics)
	NamedArgs    map[string]interface{} // Named arguments (key: value)
	Position     *SourcePosition
	state        *ExecutionState
	executor     *Executor
	logger       *Logger
	requestToken func(cleanup func(string)) string
	resumeToken  func(tokenID string, status bool) bool
}

// LogError logs a command error with position, routing through execution state channels
func (c *Context) LogError(cat LogCategory, message string) {
	// Set output context for channel routing
	c.logger.SetOutputContext(NewOutputContext(c.state, c.executor))
	defer c.logger.ClearOutputContext()
	c.logger.CommandError(cat, "", message, c.Position)
}

// LogWarning logs a command warning with position, routing through execution state channels
func (c *Context) LogWarning(cat LogCategory, message string) {
	// Set output context for channel routing
	c.logger.SetOutputContext(NewOutputContext(c.state, c.executor))
	defer c.logger.ClearOutputContext()
	c.logger.CommandWarning(cat, "", message, c.Position)
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

// StoreObject stores an object and returns its ID
func (c *Context) StoreObject(value interface{}, typeName string) int {
	return c.executor.storeObject(value, typeName)
}

// ClaimObjectReference claims ownership of an object to prevent garbage collection
func (c *Context) ClaimObjectReference(objectID int) {
	c.state.ClaimObjectReference(objectID)
}

// NewStoredListWithRefs creates a StoredList and claims references to any nested object markers
func (c *Context) NewStoredListWithRefs(items []interface{}, namedArgs map[string]interface{}) StoredList {
	return NewStoredListWithRefs(items, namedArgs, c.executor)
}

// GetMacroContext returns the current macro context for stack traces
func (c *Context) GetMacroContext() *MacroContext {
	return c.state.macroContext
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

// YieldResult represents yielding a value from a generator
// The executor catches this and updates the token's remaining commands
type YieldResult struct {
	Value              interface{}
	TokenID            string               // Token to update (empty = use #token from state)
	WhileContinuation  *WhileContinuation   // Optional - set when yielding from inside while loop
	RepeatContinuation *RepeatContinuation  // Optional - set when yielding from inside repeat loop
}

func (YieldResult) isResult() {}

// SuspendResult signals that a new suspension token should be created
// with the remaining commands in the current sequence
type SuspendResult struct{}

func (SuspendResult) isResult() {}

// WhileContinuation stores state for resuming a while loop after yield
type WhileContinuation struct {
	ConditionBlock      string            // The while condition (re-evaluated each iteration)
	BodyBlock           string            // The full while body
	RemainingBodyCmds   []*ParsedCommand  // Commands remaining in current iteration after yield
	BodyCmdIndex        int               // Which command in body yielded
	IterationCount      int               // Current iteration number
	State               *ExecutionState   // Execution state at time of yield
	SubstitutionCtx     *SubstitutionContext
	ParentContinuation  *WhileContinuation // For nested while loops - outer loop's state
}

// RepeatContinuation stores state for resuming a repeat loop after yield
type RepeatContinuation struct {
	BodyBlock           string               // The repeat body
	RemainingBodyCmds   []*ParsedCommand     // Commands remaining in current iteration after yield
	BodyCmdIndex        int                  // Which command in body yielded
	CurrentIteration    int                  // Current iteration number (0-based)
	TotalIterations     int                  // Total number of iterations
	CounterVar          string               // Optional variable name for iteration counter
	Results             []interface{}        // Results collected so far
	Failures            []interface{}        // Failed iteration numbers so far
	State               *ExecutionState      // Execution state at time of yield
	ParentContinuation  *RepeatContinuation  // For nested repeat loops
}

// IteratorState stores state for Go-backed iterators (each, pair)
type IteratorState struct {
	Type       string        // "each", "pair", or "rng"
	ListID     int           // Object ID of the list being iterated
	Index      int           // Current position (for "each")
	Keys       []string      // Keys to iterate (for "pair")
	KeyIndex   int           // Current key position (for "pair")
	Rng        *rand.Rand    // Random number generator (for "rng")
}

// ParsedCommand represents a parsed command with metadata
type ParsedCommand struct {
	Command      string
	Arguments    []interface{}
	NamedArgs    map[string]interface{} // Named arguments (key: value)
	Position     *SourcePosition
	OriginalLine string
	Separator    string // "none", ";", "&", "|"
	ChainType    string // "none", "chain" (~>), "chain_append" (~~>), "assign" (=>)
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

// TildeLocation tracks the position of a tilde or question variable reference in a string
type TildeLocation struct {
	StartPos     int    // Position of the ~ or ?
	EndPos       int    // Position of last char of varname (or semicolon if present)
	VarName      string // The variable name (without ~ or ? or ;)
	HasSemicolon bool   // true if terminated by explicit semicolon
	IsQuestion   bool   // true if this is a ? (existence check) expression, false for ~ (value)
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
	BraceCoordinator   *BraceCoordinator  // For coordinating parallel brace evaluation
	InvertStatus       bool               // If true, invert the success status when this token completes
	FiberID            int                // ID of the fiber that created this token
	WaitChan           chan ResumeData    // For synchronous blocking (e.g., in while loops)
	SubstitutionCtx    *SubstitutionContext // For generator macro argument substitution
	WhileContinuation  *WhileContinuation // For resuming while loops after yield
	IteratorState      *IteratorState     // For Go-backed iterators (each, pair)
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
	// BraceFailureCount tracks how many brace expressions returned false status during substitution
	// This is separate from ExecutionState.lastStatus which tracks the main command's status
	// If > 0, assignment should propagate failure status
	BraceFailureCount int
	// BracesEvaluated tracks how many brace expressions were evaluated during substitution
	// Used to know whether to update lastBraceFailureCount (only if braces were present)
	BracesEvaluated int
}

// FileAccessConfig controls file system access permissions
type FileAccessConfig struct {
	ReadRoots  []string // Directories allowed for read access (empty = no access)
	WriteRoots []string // Directories allowed for write access (empty = no access)
	ExecRoots  []string // Directories allowed for exec command (empty = no access)
}

// Config holds configuration for PawScript
type Config struct {
	Debug                bool
	DefaultTokenTimeout  time.Duration
	EnableSyntacticSugar bool
	AllowMacros          bool
	ShowErrorContext     bool
	ContextLines         int
	Stdin                io.Reader         // Custom stdin reader (default: os.Stdin)
	Stdout               io.Writer         // Custom stdout writer (default: os.Stdout)
	Stderr               io.Writer         // Custom stderr writer (default: os.Stderr)
	FileAccess           *FileAccessConfig // File system access control (nil = unrestricted)
	ScriptDir            string            // Directory containing the script being executed
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
		Stdin:                os.Stdin,
		Stdout:               os.Stdout,
		Stderr:               os.Stderr,
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

// StoredMacro represents a macro stored as a reference-counted object
// This can be either a named macro (registered in the macro system) or anonymous
type StoredMacro struct {
	Commands         string
	DefinitionFile   string
	DefinitionLine   int
	DefinitionColumn int
	Timestamp        time.Time
	ModuleEnv        *ModuleEnvironment // Captured module environment
}

// NewStoredMacro creates a new StoredMacro
func NewStoredMacro(commands string, position *SourcePosition) StoredMacro {
	filename := "<unknown>"
	line := 1
	column := 1

	if position != nil {
		if position.Filename != "" {
			filename = position.Filename
		}
		line = position.Line
		column = position.Column
	}

	return StoredMacro{
		Commands:         commands,
		DefinitionFile:   filename,
		DefinitionLine:   line,
		DefinitionColumn: column,
		Timestamp:        time.Now(),
		ModuleEnv:        nil, // Will be set by caller
	}
}

// NewStoredMacroWithEnv creates a new StoredMacro with captured environment
func NewStoredMacroWithEnv(commands string, position *SourcePosition, moduleEnv *ModuleEnvironment) StoredMacro {
	macro := NewStoredMacro(commands, position)
	macro.ModuleEnv = moduleEnv
	return macro
}

// String returns a string representation for debugging
func (sm StoredMacro) String() string {
	return "(macro)"
}

// StoredCommand represents a built-in or registered command stored as a reference-counted object
// This allows commands to be treated as first-class values
type StoredCommand struct {
	CommandName string
	Handler     Handler
	Timestamp   time.Time
}

// NewStoredCommand creates a new StoredCommand
func NewStoredCommand(name string, handler Handler) StoredCommand {
	return StoredCommand{
		CommandName: name,
		Handler:     handler,
		Timestamp:   time.Now(),
	}
}

// String returns a string representation for debugging
func (sc StoredCommand) String() string {
	return "(command)"
}

// ChannelMessage represents a message in a channel buffer
type ChannelMessage struct {
	SenderID   int
	Value      interface{}
	ConsumedBy map[int]bool // Track which subscribers have read this message
}

// StoredChannel represents a bidirectional communication channel with pub-sub support
// Supports both native (Go-backed) and custom (macro-backed) channels
type StoredChannel struct {
	mu              sync.RWMutex
	BufferSize      int
	Messages        []ChannelMessage
	Subscribers     map[int]*StoredChannel // Map of subscriber ID to subscriber endpoint
	NextSubscriberID int
	IsClosed        bool
	IsSubscriber    bool             // True if this is a subscriber endpoint
	SubscriberID    int              // ID of this subscriber (0 for main channel)
	ParentChannel   *StoredChannel   // Reference to parent if this is a subscriber
	CustomSend      *StoredMacro     // Optional custom send handler
	CustomRecv      *StoredMacro     // Optional custom recv handler
	CustomClose     *StoredMacro     // Optional custom close handler
	Timestamp       time.Time
	// Native function handlers for Go-backed channels (stdio, etc.)
	// If set, these are called instead of the buffer-based operations
	NativeSend      func(interface{}) error         // Native send handler
	NativeRecv      func() (interface{}, error)     // Native receive handler
	NativeClose     func() error                    // Native close handler
	// Terminal capabilities associated with this channel
	// Allows channels to report their own ANSI/color/size support
	// If nil, system terminal capabilities are used as fallback
	Terminal        *TerminalCapabilities
}

// GetTerminalCapabilities returns terminal capabilities for this channel
// Falls back through: this channel -> parent channel -> system terminal
func (ch *StoredChannel) GetTerminalCapabilities() *TerminalCapabilities {
	if ch == nil {
		return GetSystemTerminalCapabilities()
	}

	ch.mu.RLock()
	defer ch.mu.RUnlock()

	// Check this channel's terminal
	if ch.Terminal != nil {
		return ch.Terminal
	}

	// For subscribers, check parent (without holding our lock)
	if ch.IsSubscriber && ch.ParentChannel != nil {
		ch.mu.RUnlock()
		caps := ch.ParentChannel.GetTerminalCapabilities()
		ch.mu.RLock()
		return caps
	}

	// Fall back to system terminal
	return GetSystemTerminalCapabilities()
}

// SetTerminalCapabilities sets terminal capabilities for this channel
// Multiple channels can share the same capabilities pointer
func (ch *StoredChannel) SetTerminalCapabilities(caps *TerminalCapabilities) {
	if ch == nil {
		return
	}
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.Terminal = caps
}

// NewStoredChannel creates a new channel with optional buffer size
func NewStoredChannel(bufferSize int) *StoredChannel {
	return &StoredChannel{
		BufferSize:      bufferSize,
		Messages:        make([]ChannelMessage, 0),
		Subscribers:     make(map[int]*StoredChannel),
		NextSubscriberID: 1,
		IsClosed:        false,
		IsSubscriber:    false,
		SubscriberID:    0,
		ParentChannel:   nil,
		Timestamp:       time.Now(),
	}
}

// NewChannelSubscriber creates a subscriber endpoint for a channel
func NewChannelSubscriber(parent *StoredChannel, id int) *StoredChannel {
	return &StoredChannel{
		BufferSize:    parent.BufferSize,
		Messages:      nil, // Subscribers share parent's message buffer
		Subscribers:   nil, // Subscribers can't have their own subscribers
		IsClosed:      false,
		IsSubscriber:  true,
		SubscriberID:  id,
		ParentChannel: parent,
		Timestamp:     time.Now(),
	}
}

// String returns a string representation for debugging
func (ch *StoredChannel) String() string {
	if ch.IsSubscriber {
		return fmt.Sprintf("(channel-sub:%d)", ch.SubscriberID)
	}
	return "(channel)"
}

// StoredFile represents an open file handle
// Files act like channels for read/write but support additional operations
type StoredFile struct {
	mu       sync.RWMutex
	File     *os.File  // The underlying OS file handle
	Path     string    // Original path used to open the file
	Mode     string    // "r", "w", "a", "rw"
	IsClosed bool
}

// NewStoredFile creates a new file handle
func NewStoredFile(file *os.File, path, mode string) *StoredFile {
	return &StoredFile{
		File:     file,
		Path:     path,
		Mode:     mode,
		IsClosed: false,
	}
}

// Close closes the file handle
func (f *StoredFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.IsClosed {
		return nil
	}
	f.IsClosed = true
	if f.File != nil {
		return f.File.Close()
	}
	return nil
}

// String returns a string representation for debugging
func (f *StoredFile) String() string {
	if f.IsClosed {
		return "(file:closed)"
	}
	return fmt.Sprintf("(file:%s)", f.Path)
}

// ReadLine reads a single line from the file
func (f *StoredFile) ReadLine() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.IsClosed || f.File == nil {
		return "", fmt.Errorf("file is closed")
	}
	// Use a simple byte-by-byte read to get a line
	var line []byte
	buf := make([]byte, 1)
	for {
		n, err := f.File.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				break
			}
			line = append(line, buf[0])
		}
		if err != nil {
			if err == io.EOF && len(line) > 0 {
				break
			}
			return string(line), err
		}
	}
	// Strip trailing \r if present (Windows line endings)
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return string(line), nil
}

// ReadAll reads the entire remaining content of the file
func (f *StoredFile) ReadAll() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.IsClosed || f.File == nil {
		return "", fmt.Errorf("file is closed")
	}
	content, err := io.ReadAll(f.File)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// Write writes a string to the file
func (f *StoredFile) Write(s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.IsClosed || f.File == nil {
		return fmt.Errorf("file is closed")
	}
	_, err := f.File.WriteString(s)
	return err
}

// Seek moves the file position
func (f *StoredFile) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.IsClosed || f.File == nil {
		return 0, fmt.Errorf("file is closed")
	}
	return f.File.Seek(offset, whence)
}

// Tell returns the current file position
func (f *StoredFile) Tell() (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.IsClosed || f.File == nil {
		return 0, fmt.Errorf("file is closed")
	}
	return f.File.Seek(0, io.SeekCurrent)
}

// Flush flushes the file buffers
func (f *StoredFile) Flush() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.IsClosed || f.File == nil {
		return fmt.Errorf("file is closed")
	}
	return f.File.Sync()
}

// Truncate truncates the file at the current position
func (f *StoredFile) Truncate() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.IsClosed || f.File == nil {
		return fmt.Errorf("file is closed")
	}
	pos, err := f.File.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	return f.File.Truncate(pos)
}

// ResumeData contains information for resuming a suspended fiber
type ResumeData struct {
	TokenID string
	Status  bool
	Result  interface{}
}

// FiberHandle represents a running fiber (lightweight thread)
type FiberHandle struct {
	mu             sync.RWMutex
	ID             int
	State          *ExecutionState
	SuspendedOn    string                    // tokenID if suspended, "" if running
	ResumeChan     chan ResumeData           // Channel for resuming suspended fiber
	Result         interface{}               // Final result when fiber completes
	Error          error                     // Error if fiber failed
	CompleteChan   chan struct{}             // Closed when fiber completes
	Completed      bool                      // True when fiber has finished
	FinalBubbleMap map[string][]*BubbleEntry // Preserved bubbleMap after fiber completion
	BubbleUpMap    map[string][]*BubbleEntry // Early bubble staging area (for fiber_bubble)
}

// StoredList represents an immutable list of values with optional named arguments
// All operations return new StoredList instances (copy-on-write)
// Slicing shares the backing array for memory efficiency
// Named arguments (key-value pairs) are stored separately from positional items
type StoredList struct {
	items      []interface{}
	namedArgs  map[string]interface{} // Named arguments (key: value)
}

// NewStoredList creates a new StoredList from a slice of items
func NewStoredList(items []interface{}) StoredList {
	return StoredList{items: items, namedArgs: nil}
}

// NewStoredListWithNamed creates a new StoredList with both positional items and named arguments
func NewStoredListWithNamed(items []interface{}, namedArgs map[string]interface{}) StoredList {
	return StoredList{items: items, namedArgs: namedArgs}
}

// NewStoredListWithRefs creates a new StoredList and claims references to any nested objects
func NewStoredListWithRefs(items []interface{}, namedArgs map[string]interface{}, executor *Executor) StoredList {
	list := StoredList{items: items, namedArgs: namedArgs}
	// Claim references to any nested objects in positional items
	if executor != nil {
		for _, item := range items {
			claimNestedReferences(item, executor)
		}
		// Claim references to any nested objects in named arguments (both keys and values)
		for key, value := range namedArgs {
			claimNestedReferences(key, executor)
			claimNestedReferences(value, executor)
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
		// Then recursively claim references in positional list items
		for _, item := range v.Items() {
			claimNestedReferences(item, executor)
		}
		// Also claim references in named arguments (both keys and values)
		for key, val := range v.NamedArgs() {
			claimNestedReferences(key, executor)
			claimNestedReferences(val, executor)
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

// NamedArgs returns the named arguments map (direct reference, not a copy)
// Returns nil if there are no named arguments
func (pl StoredList) NamedArgs() map[string]interface{} {
	return pl.namedArgs
}

// Len returns the number of positional items in the list (excludes named arguments)
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
// Preserves named arguments from the original list
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
	return StoredList{items: pl.items[start:end], namedArgs: pl.namedArgs}
}

// Append returns a new StoredList with the item appended (O(n) copy-on-write)
// Preserves named arguments from the original list
func (pl StoredList) Append(item interface{}) StoredList {
	newItems := make([]interface{}, len(pl.items)+1)
	copy(newItems, pl.items)
	newItems[len(pl.items)] = item
	return StoredList{items: newItems, namedArgs: pl.namedArgs}
}

// Prepend returns a new StoredList with the item prepended (O(n) copy-on-write)
// Preserves named arguments from the original list
func (pl StoredList) Prepend(item interface{}) StoredList {
	newItems := make([]interface{}, len(pl.items)+1)
	newItems[0] = item
	copy(newItems[1:], pl.items)
	return StoredList{items: newItems, namedArgs: pl.namedArgs}
}

// Concat returns a new StoredList with items from both lists (O(n+m) copy)
// Named arguments are merged, with keys from 'other' replacing keys from 'pl' when both contain the same key
func (pl StoredList) Concat(other StoredList) StoredList {
	newItems := make([]interface{}, len(pl.items)+len(other.items))
	copy(newItems, pl.items)
	copy(newItems[len(pl.items):], other.items)
	
	// Merge named arguments
	var newNamedArgs map[string]interface{}
	if pl.namedArgs != nil || other.namedArgs != nil {
		newNamedArgs = make(map[string]interface{})
		// Copy from pl first
		for k, v := range pl.namedArgs {
			newNamedArgs[k] = v
		}
		// Then copy from other, overwriting any duplicate keys
		for k, v := range other.namedArgs {
			newNamedArgs[k] = v
		}
	}
	
	return StoredList{items: newItems, namedArgs: newNamedArgs}
}

// Compact returns a new StoredList with a new backing array
// Use this to free memory if you've sliced a large list
// Preserves named arguments from the original list
func (pl StoredList) Compact() StoredList {
	newItems := make([]interface{}, len(pl.items))
	copy(newItems, pl.items)
	return StoredList{items: newItems, namedArgs: pl.namedArgs}
}

// String returns a string representation for debugging
// Named arguments appear before positional items
func (pl StoredList) String() string {
	if len(pl.namedArgs) == 0 {
		return "(list)"
	}
	return "(list with named args)"
}
