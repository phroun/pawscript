package pawscript

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"unicode/utf8"
)

// REPL color codes
const (
	replColorYellow    = "\x1b[93m"
	replColorDarkBrown = "\x1b[33m" // Dark yellow/brown for light backgrounds
	replColorWhite     = "\x1b[97m"
	replColorRed       = "\x1b[91m"
	replColorDarkCyan  = "\x1b[36m"
	replColorReset     = "\x1b[0m"
)

// REPLConfig configures the REPL behavior
type REPLConfig struct {
	Debug        bool
	Unrestricted bool
	OptLevel     int
	ShowBanner   bool              // Whether to show the startup banner
	IOConfig     *IOChannelConfig  // Optional IO channels (for GUI terminals)
}

// REPL provides an interactive Read-Eval-Print Loop for PawScript
type REPL struct {
	mu              sync.Mutex
	ps              *PawScript
	config          REPLConfig
	output          func(string)           // Output function (writes to terminal)
	history         []string               // Command history
	historyPos      int                    // Current position in history
	currentLine     []rune                 // Current input line
	cursorPos       int                    // Cursor position in currentLine
	lines           []string               // Lines for multi-line input
	savedLine       string                 // Saved line when browsing history
	inHistory       bool                   // Are we browsing history?
	running         bool                   // Is REPL active?
	inputChan       chan string            // Channel for complete input
	quitChan        chan struct{}          // Signal to quit
	lightBackground bool                   // True if background is bright (>50%)
}

// NewREPL creates a new REPL instance
func NewREPL(config REPLConfig, output func(string)) *REPL {
	// Set up file access
	var fileAccess *FileAccessConfig
	if !config.Unrestricted {
		cwd, _ := GetWorkingDirectory()
		tmpDir := GetTempDirectory()
		fileAccess = &FileAccessConfig{
			ReadRoots:  []string{cwd, tmpDir},
			WriteRoots: []string{cwd, tmpDir},
			ExecRoots:  []string{cwd},
		}
	}

	// Create PawScript interpreter
	ps := New(&Config{
		Debug:                config.Debug,
		AllowMacros:          true,
		EnableSyntacticSugar: true,
		ShowErrorContext:     true,
		ContextLines:         2,
		FileAccess:           fileAccess,
		OptLevel:             OptimizationLevel(config.OptLevel),
	})

	// Register standard library with IO channels if provided
	if config.IOConfig != nil {
		ps.RegisterStandardLibraryWithIO([]string{}, config.IOConfig)
	} else {
		ps.RegisterStandardLibrary([]string{})
	}

	return &REPL{
		ps:        ps,
		config:    config,
		output:    output,
		history:   make([]string, 0, 100),
		inputChan: make(chan string, 1),
		quitChan:  make(chan struct{}),
	}
}

// NewREPLWithInterpreter creates a REPL with an existing PawScript interpreter
func NewREPLWithInterpreter(ps *PawScript, output func(string)) *REPL {
	return &REPL{
		ps:        ps,
		output:    output,
		history:   make([]string, 0, 100),
		inputChan: make(chan string, 1),
		quitChan:  make(chan struct{}),
	}
}

// Start begins the REPL session
func (r *REPL) Start() {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()

	if r.config.ShowBanner {
		r.output("PawScript Interactive Mode. Type 'exit' or 'quit' to leave.\r\n\r\n")
	}

	r.printPrompt()
}

// Stop ends the REPL session
func (r *REPL) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		r.running = false
		close(r.quitChan)
	}
}

// IsRunning returns whether the REPL is active
func (r *REPL) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// SetBackgroundRGB sets the background color to determine prompt colors
// Uses brightness calculation: if brightness > 0.5, uses dark brown instead of yellow
func (r *REPL) SetBackgroundRGB(red, green, blue uint8) {
	// Calculate perceived brightness using standard luminance formula
	brightness := (0.299*float64(red) + 0.587*float64(green) + 0.114*float64(blue)) / 255.0
	r.mu.Lock()
	r.lightBackground = brightness > 0.5
	r.mu.Unlock()
}

// promptColor returns the appropriate prompt color based on background brightness
func (r *REPL) promptColor() string {
	r.mu.Lock()
	light := r.lightBackground
	r.mu.Unlock()
	if light {
		return replColorDarkBrown
	}
	return replColorYellow
}

// HandleInput processes input bytes from the terminal
// Returns true if the REPL should exit
func (r *REPL) HandleInput(data []byte) bool {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return false
	}
	r.mu.Unlock()

	i := 0
	for i < len(data) {
		b := data[i]
		i++

		// Handle escape sequences
		if b == 0x1b && i < len(data) && data[i] == '[' {
			i++ // consume '['
			if i < len(data) {
				switch data[i] {
				case 'A': // Up arrow
					i++
					r.handleUpArrow()
					continue
				case 'B': // Down arrow
					i++
					r.handleDownArrow()
					continue
				case 'C': // Right arrow
					i++
					r.handleRightArrow()
					continue
				case 'D': // Left arrow
					i++
					r.handleLeftArrow()
					continue
				case '3': // Possible Delete key
					i++
					if i < len(data) && data[i] == '~' {
						i++
						r.handleDelete()
					}
					continue
				case 'H': // Home
					i++
					r.handleHome()
					continue
				case 'F': // End
					i++
					r.handleEnd()
					continue
				case '1': // Could be Home (ESC[1~)
					i++
					if i < len(data) && data[i] == '~' {
						i++
						r.handleHome()
					}
					continue
				case '4': // Could be End (ESC[4~)
					i++
					if i < len(data) && data[i] == '~' {
						i++
						r.handleEnd()
					}
					continue
				}
			}
			continue
		}

		switch b {
		case 0x03: // Ctrl+C
			r.output("^C\r\n")
			r.Stop()
			return true

		case 0x04: // Ctrl+D
			if len(r.currentLine) == 0 && len(r.lines) == 0 {
				r.output("\r\n")
				r.Stop()
				return true
			}

		case 0x7f, 0x08: // Backspace
			r.handleBackspace()

		case '\r', '\n': // Enter
			r.handleEnter()

		case 0x15: // Ctrl+U - clear line
			r.currentLine = nil
			r.cursorPos = 0
			r.redrawLine()

		case 0x0b: // Ctrl+K - kill to end of line
			r.currentLine = r.currentLine[:r.cursorPos]
			r.redrawLine()

		case 0x01: // Ctrl+A - beginning of line
			r.handleHome()

		case 0x05: // Ctrl+E - end of line
			r.handleEnd()

		default:
			// Regular character - might be part of UTF-8 sequence
			if b >= 32 && b < 127 {
				// ASCII printable
				r.insertChar(rune(b))
			} else if b >= 0xC0 {
				// UTF-8 start byte - collect full character
				charBytes := []byte{b}
				for i < len(data) && data[i] >= 0x80 && data[i] < 0xC0 {
					charBytes = append(charBytes, data[i])
					i++
				}
				ru, _ := utf8.DecodeRune(charBytes)
				if ru != utf8.RuneError {
					r.insertChar(ru)
				}
			}
		}
	}

	return false
}

func (r *REPL) printPrompt() {
	promptClr := r.promptColor()
	if len(r.lines) == 0 {
		r.output(promptClr + "paw*" + replColorReset + " ")
	} else {
		// Determine what needs to be closed based on accumulated input
		fullInput := strings.Join(r.lines, "\n")
		prompt := r.getContinuationPrompt(fullInput)
		// Show line number in dark cyan, rest of prompt in appropriate color
		lineNum := len(r.lines) + 1
		r.output(fmt.Sprintf("%s%d %s%s%s ", replColorDarkCyan, lineNum, promptClr, prompt, replColorReset))
	}
}

// getContinuationPrompt analyzes the input and returns the appropriate continuation prompt
// showing all nesting levels that need to be closed
func (r *REPL) getContinuationPrompt(input string) string {
	// Stack to track what's open (in order of opening)
	// We'll use strings: "(", "{", "\"", "'", "#("
	var stack []string
	prevChar := rune(0)

	for _, ch := range input {
		// Check if we're inside a string
		inString := false
		closedString := false
		for j := len(stack) - 1; j >= 0; j-- {
			if stack[j] == "\"" || stack[j] == "'" {
				inString = true
				// Check if this character closes the string
				if (stack[j] == "\"" && ch == '"' && prevChar != '\\') ||
					(stack[j] == "'" && ch == '\'' && prevChar != '\\') {
					stack = stack[:j] // Pop the string opener
					closedString = true
				}
				break
			}
		}

		// Don't process openers if we're in a string OR if we just closed one
		// (closing quote shouldn't also open a new string)
		if !inString && !closedString {
			switch ch {
			case '"':
				stack = append(stack, "\"")
			case '\'':
				stack = append(stack, "'")
			case '(':
				// Check if preceded by # for vector syntax
				if prevChar == '#' {
					stack = append(stack, "#(")
				} else {
					stack = append(stack, "(")
				}
			case ')':
				// Pop the most recent ( or #(
				for j := len(stack) - 1; j >= 0; j-- {
					if stack[j] == "(" || stack[j] == "#(" {
						stack = append(stack[:j], stack[j+1:]...)
						break
					}
				}
			case '{':
				stack = append(stack, "{")
			case '}':
				// Pop the most recent {
				for j := len(stack) - 1; j >= 0; j-- {
					if stack[j] == "{" {
						stack = append(stack[:j], stack[j+1:]...)
						break
					}
				}
			}
		}
		prevChar = ch
	}

	// Build prompt showing all nesting levels
	if len(stack) == 0 {
		return "paw*" // Shouldn't happen if we're in continuation, but fallback
	}

	// Build the nesting indicator from the stack
	var prompt strings.Builder
	for _, item := range stack {
		switch item {
		case "(":
			prompt.WriteString("(")
		case "{":
			prompt.WriteString("{")
		case "\"":
			prompt.WriteString("\"")
		case "'":
			prompt.WriteString("'")
		case "#(":
			prompt.WriteString("#(")
		}
	}
	prompt.WriteString("*")
	return prompt.String()
}

func (r *REPL) redrawLine() {
	// Clear line and redraw
	r.output("\r\x1b[K") // Move to start and clear line
	r.printPrompt()
	r.output(string(r.currentLine))
	// Move cursor to correct position
	if r.cursorPos < len(r.currentLine) {
		r.output(fmt.Sprintf("\x1b[%dD", len(r.currentLine)-r.cursorPos))
	}
}

func (r *REPL) handleUpArrow() {
	if len(r.history) > 0 && r.historyPos > 0 {
		if !r.inHistory {
			r.savedLine = string(r.currentLine)
			r.inHistory = true
		}
		r.historyPos--
		r.currentLine = []rune(r.history[r.historyPos])
		r.cursorPos = len(r.currentLine)
		r.redrawLine()
	}
}

func (r *REPL) handleDownArrow() {
	if r.inHistory {
		if r.historyPos < len(r.history)-1 {
			r.historyPos++
			r.currentLine = []rune(r.history[r.historyPos])
			r.cursorPos = len(r.currentLine)
		} else {
			r.historyPos = len(r.history)
			r.currentLine = []rune(r.savedLine)
			r.cursorPos = len(r.currentLine)
			r.inHistory = false
		}
		r.redrawLine()
	}
}

func (r *REPL) handleLeftArrow() {
	if r.cursorPos > 0 {
		r.cursorPos--
		r.output("\x1b[D")
	}
}

func (r *REPL) handleRightArrow() {
	if r.cursorPos < len(r.currentLine) {
		r.cursorPos++
		r.output("\x1b[C")
	}
}

func (r *REPL) handleHome() {
	if r.cursorPos > 0 {
		r.output(fmt.Sprintf("\x1b[%dD", r.cursorPos))
		r.cursorPos = 0
	}
}

func (r *REPL) handleEnd() {
	if r.cursorPos < len(r.currentLine) {
		r.output(fmt.Sprintf("\x1b[%dC", len(r.currentLine)-r.cursorPos))
		r.cursorPos = len(r.currentLine)
	}
}

func (r *REPL) handleBackspace() {
	if r.cursorPos > 0 {
		r.currentLine = append(r.currentLine[:r.cursorPos-1], r.currentLine[r.cursorPos:]...)
		r.cursorPos--
		r.redrawLine()
	}
}

func (r *REPL) handleDelete() {
	if r.cursorPos < len(r.currentLine) {
		r.currentLine = append(r.currentLine[:r.cursorPos], r.currentLine[r.cursorPos+1:]...)
		r.redrawLine()
	}
}

func (r *REPL) insertChar(ch rune) {
	r.currentLine = append(r.currentLine[:r.cursorPos], append([]rune{ch}, r.currentLine[r.cursorPos:]...)...)
	r.cursorPos++
	r.inHistory = false
	r.redrawLine()
}

func (r *REPL) handleEnter() {
	r.output("\r\n")
	line := string(r.currentLine)
	r.lines = append(r.lines, line)
	fullInput := strings.Join(r.lines, "\n")

	// Check if input is complete
	if r.isComplete(fullInput) {
		r.processInput(fullInput)
		r.lines = nil
		r.currentLine = nil
		r.cursorPos = 0
		r.inHistory = false

		// Check if still running before printing next prompt
		r.mu.Lock()
		running := r.running
		r.mu.Unlock()
		if running {
			r.printPrompt()
		}
	} else {
		// Continue on next line
		r.currentLine = nil
		r.cursorPos = 0
		r.inHistory = false
		r.printPrompt()
	}
}

func (r *REPL) processInput(input string) {
	trimmed := strings.TrimSpace(input)

	// Add to history if non-empty and different from last entry
	if trimmed != "" {
		if len(r.history) == 0 || r.history[len(r.history)-1] != trimmed {
			r.history = append(r.history, trimmed)
		}
		r.historyPos = len(r.history)
	}

	// Check for exit commands
	lower := strings.ToLower(trimmed)
	if lower == "exit" || lower == "quit" {
		r.Stop()
		return
	}

	if trimmed == "" {
		return
	}

	// Execute
	result := r.ps.Execute(input)

	// Flush any pending output before displaying result
	r.ps.FlushIO()

	// Display result
	r.displayResult(result)
}

func (r *REPL) isComplete(input string) bool {
	// Track nesting and quotes
	parenDepth := 0
	braceDepth := 0
	inDoubleQuote := false
	inSingleQuote := false
	prevChar := rune(0)

	for _, ch := range input {
		if inDoubleQuote {
			if ch == '"' && prevChar != '\\' {
				inDoubleQuote = false
			}
		} else if inSingleQuote {
			if ch == '\'' && prevChar != '\\' {
				inSingleQuote = false
			}
		} else {
			switch ch {
			case '"':
				inDoubleQuote = true
			case '\'':
				inSingleQuote = true
			case '(':
				parenDepth++
			case ')':
				parenDepth--
			case '{':
				braceDepth++
			case '}':
				braceDepth--
			}
		}
		prevChar = ch
	}

	return !inDoubleQuote && !inSingleQuote && parenDepth <= 0 && braceDepth <= 0
}

func (r *REPL) displayResult(result Result) {
	// Get the result value from the interpreter
	resultValue := r.ps.GetResultValue()

	var prefix string
	var prefixColor string

	if boolStatus, ok := result.(BoolStatus); ok {
		if bool(boolStatus) {
			prefix = "="
			prefixColor = replColorWhite
		} else {
			prefix = "E"
			prefixColor = replColorRed
		}
	} else {
		prefix = "="
		prefixColor = replColorWhite
	}

	// Format the result value as JSON
	formatted := r.formatValueAsJSON(resultValue)

	// Print with prefix
	lines := strings.Split(formatted, "\n")
	for i, line := range lines {
		if i == 0 {
			r.output(fmt.Sprintf("%s%s%s %s\r\n", prefixColor, prefix, replColorReset, line))
		} else {
			r.output(fmt.Sprintf("  %s\r\n", line))
		}
	}
}

func (r *REPL) formatValueAsJSON(val interface{}) string {
	if val == nil {
		return "null"
	}

	// Convert to JSON-compatible form
	jsonVal := r.toJSONValue(val)

	// Pretty print
	jsonBytes, err := json.MarshalIndent(jsonVal, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", val)
	}

	return string(jsonBytes)
}

func (r *REPL) toJSONValue(val interface{}) interface{} {
	if val == nil {
		return nil
	}

	switch v := val.(type) {
	case Symbol:
		str := string(v)
		if str == "undefined" {
			return nil
		}
		if str == "true" {
			return true
		}
		if str == "false" {
			return false
		}
		return str
	case string:
		return v
	case QuotedString:
		return string(v)
	case int64:
		return v
	case float64:
		return v
	case int:
		return int64(v)
	case bool:
		return v
	case StoredString:
		return string(v)
	case StoredBlock:
		return string(v)
	case StoredList:
		items := v.Items()
		namedArgs := v.NamedArgs()

		// If only positional items, return array
		if namedArgs == nil || len(namedArgs) == 0 {
			arr := make([]interface{}, len(items))
			for i, item := range items {
				arr[i] = r.toJSONValue(item)
			}
			return arr
		}

		// If has named args, return object
		obj := make(map[string]interface{})
		if len(items) > 0 {
			arr := make([]interface{}, len(items))
			for i, item := range items {
				arr[i] = r.toJSONValue(item)
			}
			obj["_items"] = arr
		}
		for k, v := range namedArgs {
			obj[k] = r.toJSONValue(v)
		}
		return obj
	case *StoredChannel:
		return "<channel>"
	case *StoredFile:
		return "<file>"
	case StoredBytes:
		return v.String()
	case StoredStruct:
		return v.String()
	case ObjectRef:
		// Resolve ObjectRef to actual value and format that
		if !v.IsValid() {
			return nil
		}
		resolved := r.ps.ResolveValue(v)
		if resolved == v {
			// Couldn't resolve, show type indicator
			return fmt.Sprintf("<%s>", v.Type.String())
		}
		return r.toJSONValue(resolved)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// GetWorkingDirectory returns the current working directory
func GetWorkingDirectory() (string, error) {
	return os.Getwd()
}

// GetTempDirectory returns the system temp directory
func GetTempDirectory() string {
	return os.TempDir()
}
