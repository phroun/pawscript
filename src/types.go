package pawscript

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strings"
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
	Args          []interface{}
	RawArgs       []string               // Original argument strings before resolution (for diagnostics)
	NamedArgs     map[string]interface{} // Named arguments (key: value)
	Position      *SourcePosition
	state         *ExecutionState
	executor      *Executor
	logger        *Logger
	requestToken  func(cleanup func(string)) string
	resumeToken   func(tokenID string, status bool) bool
	ParsedCommand *ParsedCommand // Source parsed command (for block caching)
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

// GetOrParseBlock returns cached parsed commands for a block argument at the given index,
// or parses the block string if not cached. Returns nil and error string if parsing fails.
func (c *Context) GetOrParseBlock(argIndex int, blockStr string) ([]*ParsedCommand, string) {
	// Try to get cached version
	if cachedCmds := c.executor.GetOrCacheBlockArg(c.ParsedCommand, argIndex, blockStr, ""); cachedCmds != nil {
		return cachedCmds, ""
	}
	// Parse fresh
	parser := NewParser(blockStr, "")
	cleanedBody := parser.RemoveComments(blockStr)
	normalizedBody := parser.NormalizeKeywords(cleanedBody)
	cmds, err := parser.ParseCommandSequence(normalizedBody)
	if err != nil {
		return nil, err.Error()
	}
	return cmds, ""
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

// GetModuleEnv returns the module environment from the current execution state
// This can be used to capture the environment for deferred callback execution
func (c *Context) GetModuleEnv() *ModuleEnvironment {
	return c.state.moduleEnv
}

// GetVariable retrieves a variable from the current execution state
func (c *Context) GetVariable(name string) (interface{}, bool) {
	return c.state.GetVariable(name)
}

// ResolveHashArg resolves a hash-prefixed symbol through the lookup chain:
// 1. Local variables
// 2. ObjectsModule (module's copy-on-write object layer)
// 3. ObjectsInherited (inherited objects like io::#out)
// Returns nil if not found
func (c *Context) ResolveHashArg(name string) interface{} {
	// 1. Check local variables
	if val, exists := c.state.GetVariable(name); exists {
		return val
	}

	// 2. Check module objects (through moduleEnv)
	if c.state.moduleEnv != nil {
		c.state.moduleEnv.mu.RLock()
		if c.state.moduleEnv.ObjectsModule != nil {
			if obj, found := c.state.moduleEnv.ObjectsModule[name]; found {
				c.state.moduleEnv.mu.RUnlock()
				return obj
			}
		}
		// 3. Check inherited objects
		if c.state.moduleEnv.ObjectsInherited != nil {
			if obj, found := c.state.moduleEnv.ObjectsInherited[name]; found {
				c.state.moduleEnv.mu.RUnlock()
				return obj
			}
		}
		c.state.moduleEnv.mu.RUnlock()
	}

	return nil
}

// SetModuleObject sets a value in the current module's ObjectsModule (copy-on-write layer).
// This allows commands to override inherited objects for the current module context.
func (c *Context) SetModuleObject(name string, value interface{}) {
	if c.state.moduleEnv == nil {
		return
	}
	c.state.moduleEnv.mu.Lock()
	defer c.state.moduleEnv.mu.Unlock()
	if c.state.moduleEnv.ObjectsModule == nil {
		c.state.moduleEnv.ObjectsModule = make(map[string]interface{})
	}
	c.state.moduleEnv.ObjectsModule[name] = value
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
	ForContinuation    *ForContinuation     // Optional - set when yielding from inside for loop
	FizzContinuation   *FizzContinuation    // Optional - set when yielding from inside fizz loop
}

func (YieldResult) isResult() {}

// SuspendResult signals that a new suspension token should be created
// with the remaining commands in the current sequence
type SuspendResult struct{}

func (SuspendResult) isResult() {}

// BreakResult signals that a loop should be exited
// Levels indicates how many loop levels to break out of (1 = innermost)
type BreakResult struct {
	Levels int
}

func (BreakResult) isResult() {}

// ContinueResult signals that a loop should skip to the next iteration
// Levels indicates how many loop levels to skip (1 = innermost)
type ContinueResult struct {
	Levels int
}

func (ContinueResult) isResult() {}

// WhileContinuation stores state for resuming a while loop after yield
type WhileContinuation struct {
	ConditionBlock      string            // The while condition (re-evaluated each iteration)
	BodyBlock           string            // The full while body
	CachedBodyCmds      []*ParsedCommand  // Cached full parsed body for reuse across iterations
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
	CachedBodyCmds      []*ParsedCommand     // Cached full parsed body for reuse across iterations
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

// ForContinuation stores state for resuming a for loop after yield
type ForContinuation struct {
	BodyBlock         string              // The for body
	CachedBodyCmds    []*ParsedCommand    // Cached full parsed body for reuse across iterations
	RemainingBodyCmds []*ParsedCommand    // Commands remaining in current iteration after yield
	BodyCmdIndex      int                 // Which command in body yielded
	IterationNumber   int                 // Current iteration number (1-based for iter:)
	IterVar           string              // Variable for iteration value
	IterNumVar        string              // Variable for iter: (iteration number)
	IndexVar          string              // Variable for index: (0-based index)
	KeyVar            string              // Variable for key (key-value iteration)
	ValueVar          string              // Variable for value (key-value iteration)
	UnpackVars        []string            // Variables for unpack mode
	// Iterator state
	IteratorToken     string              // Token marker for the iterator
	IteratorType      string              // "range", "list", "keys", "generator", "channel", "structarray"
	IsDescending      bool                // Whether iterating in descending order
	State             *ExecutionState     // Execution state at time of yield
	ParentContinuation *ForContinuation   // For nested for loops
	// Numeric range state
	RangeStart        float64             // Start value for numeric range
	RangeEnd          float64             // End value for numeric range
	RangeStep         float64             // Step value for numeric range
	RangeCurrent      float64             // Current value in numeric range
}

// FizzContinuation stores state for resuming a fizz loop after yield
type FizzContinuation struct {
	BodyBlock           string               // The fizz body
	CachedBodyCmds      []*ParsedCommand     // Cached full parsed body for reuse across iterations
	RemainingBodyCmds   []*ParsedCommand     // Commands remaining in current iteration after yield
	BodyCmdIndex        int                  // Which command in body yielded
	ContentVarName      string               // Variable name for bubble content
	MetaVarName         string               // Variable name for bubble metadata (optional)
	HasMetaVar          bool                 // Whether meta variable is being used
	Flavors             []string             // Flavors being iterated
	CurrentBubbleIndex  int                  // Current position in bubble list
	Bubbles             []*BubbleEntry       // List of bubbles being iterated
	State               *ExecutionState      // Execution state at time of yield
	ParentContinuation  *FizzContinuation    // For nested fizz loops
}

// IteratorState stores state for Go-backed iterators (each, pair, range, rng)
type IteratorState struct {
	Type       string        // "each", "pair", "range", or "rng"
	ListID     int           // Object ID of the list being iterated
	Index      int           // Current position (for "each")
	Keys       []string      // Keys to iterate (for "pair")
	KeyIndex   int           // Current key position (for "pair")
	Rng        *rand.Rand    // Random number generator (for "rng")
	// Range iterator fields
	RangeStart   float64 // Start value (for "range")
	RangeEnd     float64 // End value (for "range")
	RangeStep    float64 // Step value (for "range")
	RangeCurrent float64 // Current value (for "range")
	RangeStarted bool    // Whether iteration has started (for "range")
}

// ParsedCommand represents a parsed command with metadata
type ParsedCommand struct {
	Command         string
	Arguments       []interface{}
	NamedArgs       map[string]interface{} // Named arguments (key: value)
	Position        *SourcePosition
	OriginalLine    string
	Separator       string // "none", ";", "&", "|"
	ChainType       string // "none", "chain" (~>), "chain_append" (~~>), "assign" (=>)
	CachedBlockArgs map[int][]*ParsedCommand    // Pre-parsed block arguments (for blocks without $N substitution)
	CachedBraces    map[string][]*ParsedCommand // Pre-parsed brace expressions by content string
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
	SubstitutionCtx      *SubstitutionContext  // For generator macro argument substitution
	WhileContinuation    *WhileContinuation    // For resuming while loops after yield
	ForContinuation      *ForContinuation      // For resuming for loops after yield
	RepeatContinuation   *RepeatContinuation   // For resuming repeat loops after yield
	FizzContinuation     *FizzContinuation     // For resuming fizz loops after yield
	IteratorState        *IteratorState        // For Go-backed iterators (each, pair)
	ParentState          *ExecutionState       // For macro async: parent state for deferred result transfer
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
	// CurrentParsedCommand is the current command being executed (for block caching)
	CurrentParsedCommand *ParsedCommand
}

// FileAccessConfig controls file system access permissions
type FileAccessConfig struct {
	ReadRoots  []string // Directories allowed for read access (empty = no access)
	WriteRoots []string // Directories allowed for write access (empty = no access)
	ExecRoots  []string // Directories allowed for exec command (empty = no access)
}

// Config holds configuration for PawScript
// OptimizationLevel controls AST caching behavior
type OptimizationLevel int

const (
	OptimizeNone  OptimizationLevel = 0 // -O0: No caching, always re-parse
	OptimizeBasic OptimizationLevel = 1 // -O1: Cache macro bodies and loop bodies (default)
)

type Config struct {
	Debug                bool
	DefaultTokenTimeout  time.Duration
	EnableSyntacticSugar bool
	AllowMacros          bool
	ShowErrorContext     bool
	ContextLines         int
	OptLevel             OptimizationLevel // AST caching level (default: OptimizeBasic)
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
		OptLevel:             OptimizeBasic, // Enable AST caching by default
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
	CachedCommands   []*ParsedCommand   // Lazily populated parsed form (nil until first use)
	DefinitionFile   string
	DefinitionLine   int
	DefinitionColumn int
	Timestamp        time.Time
	ModuleEnv        *ModuleEnvironment // Captured module environment
	IsForward        bool               // True if this is an unresolved forward declaration
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

// ReadBytes reads up to n bytes from the file
// If n <= 0, reads all remaining bytes
func (f *StoredFile) ReadBytes(n int) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.IsClosed || f.File == nil {
		return nil, fmt.Errorf("file is closed")
	}
	if n <= 0 {
		// Read all remaining bytes
		return io.ReadAll(f.File)
	}
	buf := make([]byte, n)
	bytesRead, err := io.ReadFull(f.File, buf)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		// Return what was read
		return buf[:bytesRead], nil
	}
	if err != nil {
		return buf[:bytesRead], err
	}
	return buf, nil
}

// WriteBytes writes raw bytes to the file
func (f *StoredFile) WriteBytes(data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.IsClosed || f.File == nil {
		return fmt.Errorf("file is closed")
	}
	_, err := f.File.Write(data)
	return err
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
//
// Type tracking fields are maintained for both positional items (arr) and named args (map):
// - Type: "empty" (nothing added), "nil", a specific type name, or "mixed"
// - Solid: true if no nil/undefined values have been added
// - Serializable: true if all values are serializable types
type StoredList struct {
	items      []interface{}
	namedArgs  map[string]interface{} // Named arguments (key: value)

	// Type tracking for positional items
	arrType         string // "empty", "nil", "undefined", specific type, or "mixed"
	arrSolid        bool   // true if no nil/undefined values
	arrSerializable bool   // true if all values are serializable

	// Type tracking for named arguments (map values)
	mapType         string // "empty", "nil", "undefined", specific type, or "mixed"
	mapSolid        bool   // true if no nil/undefined values
	mapSerializable bool   // true if all values are serializable
}

// ListTypeInfo holds type tracking information for a collection of values
type ListTypeInfo struct {
	Type         string // "empty", "nil", "undefined", specific type, or "mixed"
	Solid        bool   // true if no nil/undefined values
	Serializable bool   // true if all values are serializable
}

// NewEmptyTypeInfo returns initial type info for an empty collection
func NewEmptyTypeInfo() ListTypeInfo {
	return ListTypeInfo{
		Type:         "empty",
		Solid:        true,
		Serializable: true,
	}
}

// classifyValue returns (typeName, isSerializable, isNilOrUndefined) for a value
// If the value is a marker, it resolves it first (requires executor)
// Note: Token type is treated as serializable for now (may revisit later)
func classifyValue(value interface{}, executor *Executor) (typeName string, isSerializable bool, isNilOrUndefined bool) {
	// Handle nil
	if value == nil {
		return "nil", true, true
	}

	// Check for markers and resolve them
	var markerStr string
	switch v := value.(type) {
	case Symbol:
		markerStr = string(v)
		// Check for undefined symbol
		if markerStr == "undefined" || markerStr == UndefinedMarker {
			return "undefined", true, true
		}
	case string:
		markerStr = v
		// Check for undefined marker
		if markerStr == UndefinedMarker {
			return "undefined", true, true
		}
	}

	if markerStr != "" {
		markerType, objectID := parseObjectMarker(markerStr)
		if objectID >= 0 && executor != nil {
			// Resolve the marker to get the actual object
			if obj, exists := executor.getObject(objectID); exists {
				// Classify based on the resolved object type
				switch markerType {
				case "list":
					if list, ok := obj.(StoredList); ok {
						// List is serializable only if its contents are serializable
						return "list", list.arrSerializable && list.mapSerializable, false
					}
					return "list", true, false
				case "bytes":
					return "bytes", true, false
				case "block":
					return "block", true, false
				case "channel":
					return "channel", false, false
				case "fiber":
					return "fiber", false, false
				case "command":
					return "command", false, false
				case "macro":
					return "macro", false, false
				case "struct":
					// StoredStruct can be a single struct or struct array
					if ss, ok := obj.(*StoredStruct); ok && ss.IsArray() {
						return "structarray", false, false
					}
					return "struct", false, false
				case "token":
					// Token type: treating as serializable for now (may revisit)
					return "token", true, false
				case "str":
					// StoredString - definitely serializable
					return "string", true, false
				default:
					// Unknown marker type, assume not serializable
					return markerType, false, false
				}
			}
		}
	}

	// Classify by Go type
	switch v := value.(type) {
	case bool:
		return "bool", true, false
	case int64:
		return "int", true, false
	case float64:
		return "float", true, false
	case string:
		return "string", true, false
	case QuotedString:
		return "string", true, false
	case StoredString:
		return "string", true, false
	case Symbol:
		return "symbol", true, false
	case StoredList:
		return "list", v.arrSerializable && v.mapSerializable, false
	case StoredBytes:
		return "bytes", true, false
	case StoredBlock:
		return "block", true, false
	case ParenGroup:
		return "block", true, false
	case *StoredChannel:
		return "channel", false, false
	case *FiberHandle:
		return "fiber", false, false
	case *StoredStruct:
		if v.IsArray() {
			return "structarray", false, false
		}
		return "struct", false, false
	default:
		// Unknown type - be conservative
		return "unknown", false, false
	}
}

// mergeTypeInfo updates type info when adding a new value
// This implements the rules for combining type tracking
func mergeTypeInfo(info ListTypeInfo, typeName string, isSerializable bool, isNilOrUndefined bool) ListTypeInfo {
	result := info

	// Handle nil/undefined: they set solid to false
	if isNilOrUndefined {
		result.Solid = false
	}

	// Handle serializable flag
	if !isSerializable {
		result.Serializable = false
	}

	// Handle type tracking
	switch info.Type {
	case "empty":
		// First item determines the type
		result.Type = typeName
	case "nil", "undefined":
		// If only nil/undefined so far, a new type takes over (unless also nil/undefined)
		if !isNilOrUndefined {
			result.Type = typeName
		} else if info.Type != typeName {
			// nil and undefined are different, so mixed
			result.Type = "mixed"
		}
	case "mixed":
		// Already mixed, stays mixed
	default:
		// We have a specific type
		if isNilOrUndefined {
			// nil/undefined doesn't change the type, just solid
		} else if typeName != info.Type {
			// Different type, becomes mixed
			result.Type = "mixed"
		}
	}

	return result
}

// computeTypeInfoForSlice computes type info for a slice of values
func computeTypeInfoForSlice(items []interface{}, executor *Executor) ListTypeInfo {
	info := NewEmptyTypeInfo()
	for _, item := range items {
		typeName, isSerializable, isNilOrUndefined := classifyValue(item, executor)
		info = mergeTypeInfo(info, typeName, isSerializable, isNilOrUndefined)
	}
	return info
}

// computeTypeInfoForMap computes type info for a map's values
func computeTypeInfoForMap(m map[string]interface{}, executor *Executor) ListTypeInfo {
	info := NewEmptyTypeInfo()
	for _, value := range m {
		typeName, isSerializable, isNilOrUndefined := classifyValue(value, executor)
		info = mergeTypeInfo(info, typeName, isSerializable, isNilOrUndefined)
	}
	return info
}

// mergeTypeInfos combines two TypeInfo structs (used for Concat)
func mergeTypeInfos(a, b ListTypeInfo) ListTypeInfo {
	result := a

	// Merge solid flags
	if !b.Solid {
		result.Solid = false
	}

	// Merge serializable flags
	if !b.Serializable {
		result.Serializable = false
	}

	// Merge types
	if a.Type == "empty" {
		result.Type = b.Type
	} else if b.Type == "empty" {
		// Keep a.Type
	} else if a.Type == "mixed" || b.Type == "mixed" {
		result.Type = "mixed"
	} else if a.Type != b.Type {
		// Special case: nil/undefined with specific type
		if (a.Type == "nil" || a.Type == "undefined") && b.Type != "nil" && b.Type != "undefined" {
			result.Type = b.Type
		} else if (b.Type == "nil" || b.Type == "undefined") && a.Type != "nil" && a.Type != "undefined" {
			result.Type = a.Type
		} else {
			result.Type = "mixed"
		}
	}

	return result
}

// NewStoredList creates a new StoredList from a slice of items
// Type info is computed without executor (markers won't be fully resolved)
func NewStoredList(items []interface{}) StoredList {
	arrInfo := computeTypeInfoForSlice(items, nil)
	mapInfo := NewEmptyTypeInfo()
	return StoredList{
		items:           items,
		namedArgs:       nil,
		arrType:         arrInfo.Type,
		arrSolid:        arrInfo.Solid,
		arrSerializable: arrInfo.Serializable,
		mapType:         mapInfo.Type,
		mapSolid:        mapInfo.Solid,
		mapSerializable: mapInfo.Serializable,
	}
}

// NewStoredListWithNamed creates a new StoredList with both positional items and named arguments
// Type info is computed without executor (markers won't be fully resolved)
func NewStoredListWithNamed(items []interface{}, namedArgs map[string]interface{}) StoredList {
	arrInfo := computeTypeInfoForSlice(items, nil)
	mapInfo := computeTypeInfoForMap(namedArgs, nil)
	return StoredList{
		items:           items,
		namedArgs:       namedArgs,
		arrType:         arrInfo.Type,
		arrSolid:        arrInfo.Solid,
		arrSerializable: arrInfo.Serializable,
		mapType:         mapInfo.Type,
		mapSolid:        mapInfo.Solid,
		mapSerializable: mapInfo.Serializable,
	}
}

// NewStoredListWithRefs creates a new StoredList and claims references to any nested objects
// Type info is computed with the executor for full marker resolution
func NewStoredListWithRefs(items []interface{}, namedArgs map[string]interface{}, executor *Executor) StoredList {
	// Compute type info with executor for full resolution
	arrInfo := computeTypeInfoForSlice(items, executor)
	mapInfo := computeTypeInfoForMap(namedArgs, executor)

	list := StoredList{
		items:           items,
		namedArgs:       namedArgs,
		arrType:         arrInfo.Type,
		arrSolid:        arrInfo.Solid,
		arrSerializable: arrInfo.Serializable,
		mapType:         mapInfo.Type,
		mapSolid:        mapInfo.Solid,
		mapSerializable: mapInfo.Serializable,
	}

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
// Preserves named arguments and type info from the original list
// (Type info is conservative - slice might be more specific but we preserve parent's info)
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
	return StoredList{
		items:           pl.items[start:end],
		namedArgs:       pl.namedArgs,
		arrType:         pl.arrType,
		arrSolid:        pl.arrSolid,
		arrSerializable: pl.arrSerializable,
		mapType:         pl.mapType,
		mapSolid:        pl.mapSolid,
		mapSerializable: pl.mapSerializable,
	}
}

// Append returns a new StoredList with the item appended (O(n) copy-on-write)
// Preserves named arguments from the original list
// Type info is updated incrementally based on the new item
func (pl StoredList) Append(item interface{}) StoredList {
	newItems := make([]interface{}, len(pl.items)+1)
	copy(newItems, pl.items)
	newItems[len(pl.items)] = item

	// Update type info for positional items
	currentInfo := ListTypeInfo{
		Type:         pl.arrType,
		Solid:        pl.arrSolid,
		Serializable: pl.arrSerializable,
	}
	typeName, isSerializable, isNilOrUndefined := classifyValue(item, nil)
	newInfo := mergeTypeInfo(currentInfo, typeName, isSerializable, isNilOrUndefined)

	return StoredList{
		items:           newItems,
		namedArgs:       pl.namedArgs,
		arrType:         newInfo.Type,
		arrSolid:        newInfo.Solid,
		arrSerializable: newInfo.Serializable,
		mapType:         pl.mapType,
		mapSolid:        pl.mapSolid,
		mapSerializable: pl.mapSerializable,
	}
}

// Prepend returns a new StoredList with the item prepended (O(n) copy-on-write)
// Preserves named arguments from the original list
// Type info is updated incrementally based on the new item
func (pl StoredList) Prepend(item interface{}) StoredList {
	newItems := make([]interface{}, len(pl.items)+1)
	newItems[0] = item
	copy(newItems[1:], pl.items)

	// Update type info for positional items
	currentInfo := ListTypeInfo{
		Type:         pl.arrType,
		Solid:        pl.arrSolid,
		Serializable: pl.arrSerializable,
	}
	typeName, isSerializable, isNilOrUndefined := classifyValue(item, nil)
	newInfo := mergeTypeInfo(currentInfo, typeName, isSerializable, isNilOrUndefined)

	return StoredList{
		items:           newItems,
		namedArgs:       pl.namedArgs,
		arrType:         newInfo.Type,
		arrSolid:        newInfo.Solid,
		arrSerializable: newInfo.Serializable,
		mapType:         pl.mapType,
		mapSolid:        pl.mapSolid,
		mapSerializable: pl.mapSerializable,
	}
}

// Concat returns a new StoredList with items from both lists (O(n+m) copy)
// Named arguments are merged, with keys from 'other' replacing keys from 'pl' when both contain the same key
// Type info is merged from both lists
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

	// Merge type info for positional items
	plArrInfo := ListTypeInfo{
		Type:         pl.arrType,
		Solid:        pl.arrSolid,
		Serializable: pl.arrSerializable,
	}
	otherArrInfo := ListTypeInfo{
		Type:         other.arrType,
		Solid:        other.arrSolid,
		Serializable: other.arrSerializable,
	}
	newArrInfo := mergeTypeInfos(plArrInfo, otherArrInfo)

	// Merge type info for map (named args)
	plMapInfo := ListTypeInfo{
		Type:         pl.mapType,
		Solid:        pl.mapSolid,
		Serializable: pl.mapSerializable,
	}
	otherMapInfo := ListTypeInfo{
		Type:         other.mapType,
		Solid:        other.mapSolid,
		Serializable: other.mapSerializable,
	}
	newMapInfo := mergeTypeInfos(plMapInfo, otherMapInfo)

	return StoredList{
		items:           newItems,
		namedArgs:       newNamedArgs,
		arrType:         newArrInfo.Type,
		arrSolid:        newArrInfo.Solid,
		arrSerializable: newArrInfo.Serializable,
		mapType:         newMapInfo.Type,
		mapSolid:        newMapInfo.Solid,
		mapSerializable: newMapInfo.Serializable,
	}
}

// Compact returns a new StoredList with a new backing array
// Use this to free memory if you've sliced a large list
// Preserves named arguments and type info from the original list
func (pl StoredList) Compact() StoredList {
	newItems := make([]interface{}, len(pl.items))
	copy(newItems, pl.items)
	return StoredList{
		items:           newItems,
		namedArgs:       pl.namedArgs,
		arrType:         pl.arrType,
		arrSolid:        pl.arrSolid,
		arrSerializable: pl.arrSerializable,
		mapType:         pl.mapType,
		mapSolid:        pl.mapSolid,
		mapSerializable: pl.mapSerializable,
	}
}

// String returns a string representation for debugging
// Named arguments appear before positional items
func (pl StoredList) String() string {
	if len(pl.namedArgs) == 0 {
		return "(list)"
	}
	return "(list with named args)"
}

// ArrType returns the type of positional items: "empty", "nil", "undefined", a specific type, or "mixed"
func (pl StoredList) ArrType() string {
	if pl.arrType == "" {
		return "empty"
	}
	return pl.arrType
}

// MapType returns the type of named arg values: "empty", "nil", "undefined", a specific type, or "mixed"
func (pl StoredList) MapType() string {
	if pl.mapType == "" {
		return "empty"
	}
	return pl.mapType
}

// ArrSolid returns true if no nil/undefined values are in the positional items
func (pl StoredList) ArrSolid() bool {
	// Empty lists with uninitialized fields should be solid
	if pl.arrType == "" {
		return true
	}
	return pl.arrSolid
}

// MapSolid returns true if no nil/undefined values are in the named args
func (pl StoredList) MapSolid() bool {
	// Empty maps with uninitialized fields should be solid
	if pl.mapType == "" {
		return true
	}
	return pl.mapSolid
}

// ArrSerializable returns true if all positional items are serializable types
func (pl StoredList) ArrSerializable() bool {
	// Empty lists with uninitialized fields should be serializable
	if pl.arrType == "" {
		return true
	}
	return pl.arrSerializable
}

// MapSerializable returns true if all named arg values are serializable types
func (pl StoredList) MapSerializable() bool {
	// Empty maps with uninitialized fields should be serializable
	if pl.mapType == "" {
		return true
	}
	return pl.mapSerializable
}

// StoredBytes represents an immutable byte array
// All operations return new StoredBytes instances (copy-on-write)
// Slicing shares the backing array for memory efficiency
// When elements are extracted, they are converted to int64
type StoredBytes struct {
	data []byte
}

// NewStoredBytes creates a new StoredBytes from a byte slice
func NewStoredBytes(data []byte) StoredBytes {
	return StoredBytes{data: data}
}

// NewStoredBytesFromInts creates StoredBytes from int64 values
// Values are masked to byte range (0-255)
func NewStoredBytesFromInts(values []int64) StoredBytes {
	data := make([]byte, len(values))
	for i, v := range values {
		data[i] = byte(v & 0xFF)
	}
	return StoredBytes{data: data}
}

// Data returns the underlying byte slice (direct reference)
func (sb StoredBytes) Data() []byte {
	return sb.data
}

// Len returns the number of bytes
func (sb StoredBytes) Len() int {
	return len(sb.data)
}

// Get returns the byte at the given index as int64
// Returns 0 if index is out of bounds
func (sb StoredBytes) Get(index int) int64 {
	if index < 0 || index >= len(sb.data) {
		return 0
	}
	return int64(sb.data[index])
}

// Slice returns a new StoredBytes with bytes from start to end (end exclusive)
// Shares the backing array for memory efficiency (O(1) time, O(1) space)
func (sb StoredBytes) Slice(start, end int) StoredBytes {
	if start < 0 {
		start = 0
	}
	if end > len(sb.data) {
		end = len(sb.data)
	}
	if start > end {
		start = end
	}
	return StoredBytes{data: sb.data[start:end]}
}

// Append returns a new StoredBytes with the byte appended (O(n) copy-on-write)
// Value is masked to byte range
func (sb StoredBytes) Append(value int64) StoredBytes {
	newData := make([]byte, len(sb.data)+1)
	copy(newData, sb.data)
	newData[len(sb.data)] = byte(value & 0xFF)
	return StoredBytes{data: newData}
}

// AppendBytes returns a new StoredBytes with other bytes appended
func (sb StoredBytes) AppendBytes(other StoredBytes) StoredBytes {
	newData := make([]byte, len(sb.data)+len(other.data))
	copy(newData, sb.data)
	copy(newData[len(sb.data):], other.data)
	return StoredBytes{data: newData}
}

// Prepend returns a new StoredBytes with the byte prepended (O(n) copy-on-write)
// Value is masked to byte range
func (sb StoredBytes) Prepend(value int64) StoredBytes {
	newData := make([]byte, len(sb.data)+1)
	newData[0] = byte(value & 0xFF)
	copy(newData[1:], sb.data)
	return StoredBytes{data: newData}
}

// Concat returns a new StoredBytes with bytes from both (O(n+m) copy)
func (sb StoredBytes) Concat(other StoredBytes) StoredBytes {
	newData := make([]byte, len(sb.data)+len(other.data))
	copy(newData, sb.data)
	copy(newData[len(sb.data):], other.data)
	return StoredBytes{data: newData}
}

// Compact returns a new StoredBytes with a new backing array
// Use this to free memory if you've sliced a large byte array
func (sb StoredBytes) Compact() StoredBytes {
	newData := make([]byte, len(sb.data))
	copy(newData, sb.data)
	return StoredBytes{data: newData}
}

// String returns a hex string representation with spaces every 4 bytes
// Format: <08AEC7FF 0810CD00 24EE>
func (sb StoredBytes) String() string {
	if len(sb.data) == 0 {
		return "<>"
	}

	var result strings.Builder
	result.WriteByte('<')

	for i, b := range sb.data {
		if i > 0 && i%4 == 0 {
			result.WriteByte(' ')
		}
		result.WriteString(fmt.Sprintf("%02X", b))
	}

	result.WriteByte('>')
	return result.String()
}

// ToInt64 converts the bytes to an int64 (big-endian)
// Used when bytes are coerced to a number
func (sb StoredBytes) ToInt64() int64 {
	var result int64
	for _, b := range sb.data {
		result = (result << 8) | int64(b)
	}
	return result
}

// FromString creates StoredBytes from a string (low-ASCII only)
// Returns error if any character is >= 128
func StoredBytesFromString(s string) (StoredBytes, error) {
	data := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] >= 128 {
			return StoredBytes{}, fmt.Errorf("non-ASCII character at position %d", i)
		}
		data[i] = s[i]
	}
	return StoredBytes{data: data}, nil
}

// ToASCIIString converts bytes to string (for display/debugging)
func (sb StoredBytes) ToASCIIString() string {
	return string(sb.data)
}

// ========================================
// Struct Definitions are now StoredLists
// ========================================
// A struct definition is a StoredList with the following format:
//   __size: int (total size in bytes)
//   __named: StoredList (metadata from descriptor)
//   fieldName: StoredList [offset, length, mode] or [offset, length, "struct", nestedDefID, count]
//
// This allows advanced users to customize union types and overlapping fields.

// ========================================
// StoredStruct - Instance of a struct
// ========================================

// StoredStruct represents an instance of a struct or struct array
// The definition is now a StoredList, referenced by its object ID
type StoredStruct struct {
	defID      int    // Object ID of the definition StoredList
	data       []byte // Backing byte array
	offset     int    // Offset into the backing array (for slices)
	recordSize int    // Size of each record
	length     int    // Number of records (-1 for single struct, >= 0 for array)
}

// NewStoredStruct creates a new single struct instance
func NewStoredStruct(defID int, size int) StoredStruct {
	data := make([]byte, size)
	return StoredStruct{
		defID:      defID,
		data:       data,
		offset:     0,
		recordSize: size,
		length:     -1, // Single struct
	}
}

// NewStoredStructArray creates a new struct array with n elements
func NewStoredStructArray(defID int, size int, n int) StoredStruct {
	data := make([]byte, size*n)
	return StoredStruct{
		defID:      defID,
		data:       data,
		offset:     0,
		recordSize: size,
		length:     n,
	}
}

// NewStoredStructFromData creates a struct from existing data (for nested structs)
func NewStoredStructFromData(defID int, data []byte, recordSize int, length int) StoredStruct {
	return StoredStruct{
		defID:      defID,
		data:       data,
		offset:     0,
		recordSize: recordSize,
		length:     length,
	}
}

// DefID returns the object ID of the struct definition list
func (ss StoredStruct) DefID() int {
	return ss.defID
}

// IsArray returns true if this is a struct array
func (ss StoredStruct) IsArray() bool {
	return ss.length >= 0
}

// Len returns the number of elements (-1 for single struct)
func (ss StoredStruct) Len() int {
	return ss.length
}

// RecordSize returns the size of each record
func (ss StoredStruct) RecordSize() int {
	return ss.recordSize
}

// Data returns the underlying byte slice for this struct/array
func (ss StoredStruct) Data() []byte {
	if ss.length < 0 {
		// Single struct
		return ss.data[ss.offset : ss.offset+ss.recordSize]
	}
	// Array
	return ss.data[ss.offset : ss.offset+ss.recordSize*ss.length]
}

// Get returns the struct at index i (for arrays)
// Returns a single struct that shares the backing array
func (ss StoredStruct) Get(index int) StoredStruct {
	if ss.length < 0 {
		// Single struct - can't index
		return ss
	}
	if index < 0 || index >= ss.length {
		return StoredStruct{} // Invalid index
	}
	return StoredStruct{
		defID:      ss.defID,
		data:       ss.data,
		offset:     ss.offset + index*ss.recordSize,
		recordSize: ss.recordSize,
		length:     -1, // Returns a single struct
	}
}

// Slice returns a new struct array with elements from start to end (end exclusive)
// Shares the backing array for memory efficiency
func (ss StoredStruct) Slice(start, end int) StoredStruct {
	if ss.length < 0 {
		// Single struct - can't slice
		return ss
	}
	if start < 0 {
		start = 0
	}
	if end > ss.length {
		end = ss.length
	}
	if start > end {
		start = end
	}
	return StoredStruct{
		defID:      ss.defID,
		data:       ss.data,
		offset:     ss.offset + start*ss.recordSize,
		recordSize: ss.recordSize,
		length:     end - start,
	}
}

// Compact returns a new struct array with a fresh backing array
func (ss StoredStruct) Compact() StoredStruct {
	oldData := ss.Data()
	newData := make([]byte, len(oldData))
	copy(newData, oldData)
	return StoredStruct{
		defID:      ss.defID,
		data:       newData,
		offset:     0,
		recordSize: ss.recordSize,
		length:     ss.length,
	}
}

// GetBytesAt returns raw bytes at the given offset and length
func (ss StoredStruct) GetBytesAt(fieldOffset, fieldLength int) ([]byte, bool) {
	start := ss.offset + fieldOffset
	end := start + fieldLength
	if end > len(ss.data) {
		return nil, false
	}
	return ss.data[start:end], true
}

// SetBytesAt sets raw bytes at the given offset
func (ss StoredStruct) SetBytesAt(fieldOffset int, value []byte, maxLen int) bool {
	start := ss.offset + fieldOffset
	copyLen := len(value)
	if copyLen > maxLen {
		copyLen = maxLen
	}
	copy(ss.data[start:start+copyLen], value[:copyLen])
	return true
}

// ZeroPadAt zero-pads from the given position to the end of the field
func (ss StoredStruct) ZeroPadAt(fieldOffset, startPos, fieldLength int) {
	start := ss.offset + fieldOffset
	for i := startPos; i < fieldLength; i++ {
		ss.data[start+i] = 0
	}
}

// String returns a human-readable representation
func (ss StoredStruct) String() string {
	if ss.length < 0 {
		return fmt.Sprintf("<Struct defID=%d size=%d>", ss.defID, ss.recordSize)
	}
	return fmt.Sprintf("<StructArray[%d] defID=%d>", ss.length, ss.defID)
}
