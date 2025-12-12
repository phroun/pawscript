package pawscript

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/term"
)

// REPL color codes
const (
	replColorYellow      = "\x1b[93m"
	replColorDarkBrown   = "\x1b[33m" // Dark yellow/brown for light backgrounds
	replColorWhite       = "\x1b[97m"
	replColorRed         = "\x1b[91m"
	replColorDarkCyan    = "\x1b[36m"
	replColorBrightGreen = "\x1b[92m" // Bright green for dark backgrounds
	replColorDarkGreen   = "\x1b[32m" // Dark green for light backgrounds
	replColorDarkGray    = "\x1b[90m" // Dark gray for dark backgrounds
	replColorSilver      = "\x1b[37m" // Silver/light gray for light backgrounds
	replColorReset       = "\x1b[0m"
	// Elide indicator: bright white on red background
	replColorElide       = "\x1b[97;41m"
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
	flush           func()                 // Flush function (ensures output is displayed before blocking)
	history         []string               // Command history
	historyPos      int                    // Current position in history
	currentLine     []rune                 // Current input line
	cursorPos       int                    // Cursor position in currentLine
	lines           []string               // Lines for multi-line input
	savedLine       string                 // Saved line when browsing history
	inHistory       bool                   // Are we browsing history?
	running         bool                   // Is REPL active?
	busy            bool                   // Is a command currently executing?
	inputChan       chan string            // Channel for complete input
	quitChan        chan struct{}          // Signal to quit
	lightBackground bool                   // True if background is bright (>50%)
	pslColors       DisplayColorConfig     // PSL result display colors
	pslColorsSet    bool                   // True if custom PSL colors have been set
	// Horizontal scroll state for long input lines
	scrollOffset    int                    // First visible character index in currentLine
	terminalWidth   int                    // Terminal width (0 = use default 80)
	// Readline-only mode support
	readlineOnly    bool                   // When true, processInput returns input instead of executing
	readlineChan    chan string            // Channel for returning completed input in readline-only mode
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

	// Load command history from file
	history := loadReplHistory()
	if history == nil {
		history = make([]string, 0, 100)
	}

	return &REPL{
		ps:         ps,
		config:     config,
		output:     output,
		history:    history,
		historyPos: len(history),
		inputChan:  make(chan string, 1),
		quitChan:   make(chan struct{}),
	}
}

// NewREPLWithInterpreter creates a REPL with an existing PawScript interpreter
func NewREPLWithInterpreter(ps *PawScript, output func(string)) *REPL {
	// Load command history from file
	history := loadReplHistory()
	if history == nil {
		history = make([]string, 0, 100)
	}

	return &REPL{
		ps:         ps,
		output:     output,
		history:    history,
		historyPos: len(history),
		inputChan:  make(chan string, 1),
		quitChan:   make(chan struct{}),
	}
}

// SetFlush sets the flush callback that is called after outputting a newline
// but before executing a command. This is important for GUI terminals that use
// asynchronous output (like glib.IdleAdd) - the flush callback should process
// pending events to ensure the newline is displayed before blocking execution.
func (r *REPL) SetFlush(flush func()) {
	r.flush = flush
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
		// Save command history to file
		saveReplHistory(r.history)
	}
}

// IsRunning returns whether the REPL is active
func (r *REPL) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// IsBusy returns whether the REPL is currently executing a command
// When busy, terminal input should go to stdin channels instead of the REPL
func (r *REPL) IsBusy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.busy
}

// StartReadline begins a readline-only session where input is collected
// but not executed. Use ReadLine() to wait for complete input.
// The output function is used for prompts and editing feedback.
func (r *REPL) StartReadline() {
	r.mu.Lock()
	r.running = true
	r.readlineOnly = true
	r.readlineChan = make(chan string, 1)
	r.mu.Unlock()
	r.printPrompt()
}

// ReadLine waits for and returns complete input from the readline.
// Returns the input string and true, or empty string and false if quit/interrupted.
// Must be called after StartReadline().
func (r *REPL) ReadLine() (string, bool) {
	select {
	case input := <-r.readlineChan:
		return input, true
	case <-r.quitChan:
		return "", false
	}
}

// ResetLine clears the current input and shows a fresh prompt.
// Useful for the CLI to reset state between commands.
func (r *REPL) ResetLine() {
	r.mu.Lock()
	r.currentLine = nil
	r.cursorPos = 0
	r.scrollOffset = 0
	r.lines = nil
	r.inHistory = false
	r.mu.Unlock()
	r.printPrompt()
}

// GetPawScript returns the underlying PawScript interpreter
func (r *REPL) GetPawScript() *PawScript {
	return r.ps
}

// GetHistory returns the command history
func (r *REPL) GetHistory() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]string, len(r.history))
	copy(result, r.history)
	return result
}

// SaveHistory saves the command history to file
func (r *REPL) SaveHistory() {
	r.mu.Lock()
	history := r.history
	r.mu.Unlock()
	saveReplHistory(history)
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

// SetPSLColors sets the colors used for PSL result display
func (r *REPL) SetPSLColors(colors DisplayColorConfig) {
	r.mu.Lock()
	r.pslColors = colors
	r.pslColorsSet = true
	r.mu.Unlock()
}

// SetTerminalWidth sets the terminal width for horizontal scrolling calculations
// For GUI terminals, this should be called when the logical width changes
func (r *REPL) SetTerminalWidth(width int) {
	r.mu.Lock()
	r.terminalWidth = width
	r.mu.Unlock()
}

// getTerminalWidth returns the terminal width for input display
// Uses configured width, falls back to IOConfig terminal width, then system terminal, then 80
func (r *REPL) getTerminalWidth() int {
	r.mu.Lock()
	width := r.terminalWidth
	r.mu.Unlock()

	if width > 0 {
		return width
	}

	// Try IOConfig terminal width
	if r.config.IOConfig != nil && r.config.IOConfig.Stdin != nil {
		if r.config.IOConfig.Stdin.Terminal != nil {
			if w := r.config.IOConfig.Stdin.Terminal.Width; w > 0 {
				return w
			}
		}
	}

	// Fall back to system terminal size (for CLI)
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}

	return 80 // Default fallback
}

// getPSLColors returns the configured PSL colors or defaults
func (r *REPL) getPSLColors() DisplayColorConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pslColorsSet {
		return r.pslColors
	}
	return DefaultDisplayColors()
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

// equalsColor returns the color for the "=" prefix in result display
func (r *REPL) equalsColor() string {
	r.mu.Lock()
	light := r.lightBackground
	r.mu.Unlock()
	if light {
		return replColorDarkGreen
	}
	return replColorBrightGreen
}

// resultColor returns the color for the result value text
func (r *REPL) resultColor() string {
	r.mu.Lock()
	light := r.lightBackground
	r.mu.Unlock()
	if light {
		return replColorSilver
	}
	return replColorDarkGray
}

// HandleInput processes input bytes from the terminal
// Returns true if the REPL should exit
func (r *REPL) HandleInput(data []byte) bool {
	r.mu.Lock()
	if !r.running || r.busy {
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
			escStart := i - 1 // Position of ESC
			i++               // consume '['
			handled := false
			if i < len(data) {
				switch data[i] {
				case 'A': // Up arrow
					i++
					r.handleUpArrow()
					handled = true
				case 'B': // Down arrow
					i++
					r.handleDownArrow()
					handled = true
				case 'C': // Right arrow
					i++
					r.handleRightArrow()
					handled = true
				case 'D': // Left arrow
					i++
					r.handleLeftArrow()
					handled = true
				case '3': // Possible Delete key (ESC[3~)
					if i+1 < len(data) && data[i+1] == '~' {
						i += 2
						r.handleDelete()
						handled = true
					}
				case 'H': // Home
					i++
					r.handleHome()
					handled = true
				case 'F': // End
					i++
					r.handleEnd()
					handled = true
				case '1': // Could be Home (ESC[1~)
					if i+1 < len(data) && data[i+1] == '~' {
						i += 2
						r.handleHome()
						handled = true
					}
				case '4': // Could be End (ESC[4~)
					if i+1 < len(data) && data[i+1] == '~' {
						i += 2
						r.handleEnd()
						handled = true
					}
				}
			}
			if handled {
				continue
			}
			// Unknown escape sequence - capture and display it
			// Find the end of the sequence (letter or ~)
			escEnd := i
			for escEnd < len(data) && data[escEnd] >= 0x20 && data[escEnd] < 0x40 {
				escEnd++
			}
			if escEnd < len(data) {
				escEnd++ // Include the terminating character
			}
			// Build display string with \e instead of actual ESC
			escSeq := "\\e" + string(data[escStart+1:escEnd])
			r.output(fmt.Sprintf("\r\nEsc Sequence: %s\r\n", escSeq))
			r.redrawLine()
			i = escEnd
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
			r.scrollOffset = 0
			r.redrawLine()

		case 0x0b: // Ctrl+K - kill to end of line
			r.currentLine = r.currentLine[:r.cursorPos]
			// scrollOffset stays the same since we're keeping content before cursor
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

// HandleKeyEvent processes a named key event (from KeyInputManager)
// This allows the REPL to be used with KeyInputManager's keys channel
// Returns true if the REPL should exit
func (r *REPL) HandleKeyEvent(key string) bool {
	r.mu.Lock()
	if !r.running || r.busy {
		r.mu.Unlock()
		return false
	}
	r.mu.Unlock()

	// Handle named key events
	switch key {
	case "^C":
		r.output("^C\r\n")
		r.Stop()
		return true

	case "^D":
		if len(r.currentLine) == 0 && len(r.lines) == 0 {
			r.output("\r\n")
			r.Stop()
			return true
		}

	case "Enter":
		r.handleEnter()

	case "Backspace":
		r.handleBackspace()

	case "Delete":
		r.handleDelete()

	case "Up":
		r.handleUpArrow()

	case "Down":
		r.handleDownArrow()

	case "Left":
		r.handleLeftArrow()

	case "Right":
		r.handleRightArrow()

	case "Home", "^A":
		r.handleHome()

	case "End", "^E":
		r.handleEnd()

	case "^U":
		r.currentLine = nil
		r.cursorPos = 0
		r.scrollOffset = 0
		r.redrawLine()

	case "^K":
		r.currentLine = r.currentLine[:r.cursorPos]
		r.redrawLine()

	case "Tab":
		r.insertChar('\t')

	default:
		// Check for single characters (printable)
		if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
			r.insertChar(rune(key[0]))
		} else if len(key) > 0 {
			// Multi-byte UTF-8 character
			runes := []rune(key)
			if len(runes) == 1 {
				r.insertChar(runes[0])
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

// getPromptWidth returns the display width of the current prompt
func (r *REPL) getPromptWidth() int {
	if len(r.lines) == 0 {
		return 5 // "paw* " = 5 characters
	}
	// Continuation prompts: calculate from getContinuationPrompt
	fullInput := strings.Join(r.lines, "\n")
	prompt := r.getContinuationPrompt(fullInput)
	return len(prompt) + 1 // +1 for the trailing space
}

// getInputAreaWidth returns the width available for input display (terminal width minus prompt)
func (r *REPL) getInputAreaWidth() int {
	termWidth := r.getTerminalWidth()
	promptWidth := r.getPromptWidth()
	available := termWidth - promptWidth
	if available < 10 {
		available = 10 // Minimum reasonable width
	}
	return available
}

// calculatePeekAhead returns the peek-ahead size based on input area width
// Tries for 8 characters, but reduces if space is tight
func (r *REPL) calculatePeekAhead(inputAreaWidth int) int {
	// If input area < 20, reduce peek so non-peek zone >= peek zone
	// With 2 peek zones (left and right), we want: inputAreaWidth - 2*peek >= peek
	// So: inputAreaWidth >= 3*peek, thus peek <= inputAreaWidth/3
	if inputAreaWidth < 20 {
		peek := inputAreaWidth / 3
		if peek < 1 {
			peek = 1
		}
		return peek
	}
	return 8 // Default peek-ahead
}

// adjustScrollOffset ensures the cursor is visible within the input area
// with appropriate peek-ahead context. Returns true if offset changed.
func (r *REPL) adjustScrollOffset() bool {
	oldOffset := r.scrollOffset
	inputWidth := r.getInputAreaWidth()
	peek := r.calculatePeekAhead(inputWidth)
	lineLen := len(r.currentLine)

	// If line fits entirely, no scrolling needed
	if lineLen <= inputWidth {
		r.scrollOffset = 0
		return r.scrollOffset != oldOffset
	}

	// Calculate visible range with current scroll offset
	// If there's content to the left, we show '<' which takes 1 char
	leftIndicator := 0
	if r.scrollOffset > 0 {
		leftIndicator = 1
	}

	// Available space for actual content (may need space for indicators)
	contentSpace := inputWidth - leftIndicator

	// Cursor position relative to scroll offset
	cursorInView := r.cursorPos - r.scrollOffset

	// If cursor is too far left (within left peek zone), scroll left
	if cursorInView < peek && r.scrollOffset > 0 {
		// Scroll left to put cursor at peek position from left edge
		newOffset := r.cursorPos - peek
		if newOffset < 0 {
			newOffset = 0
		}
		r.scrollOffset = newOffset
	}

	// Recalculate after potential left scroll
	if r.scrollOffset > 0 {
		leftIndicator = 1
	} else {
		leftIndicator = 0
	}
	contentSpace = inputWidth - leftIndicator

	// Check if there will be right indicator and make room for it
	rightEnd := r.scrollOffset + contentSpace
	if rightEnd < lineLen {
		contentSpace-- // Make room for '>'
	}

	// If cursor is too far right (within right peek zone), scroll right
	cursorInView = r.cursorPos - r.scrollOffset - leftIndicator
	rightBoundary := contentSpace - peek
	if rightBoundary < 1 {
		rightBoundary = 1
	}

	if cursorInView > rightBoundary {
		// Scroll right to put cursor at rightBoundary position
		newOffset := r.cursorPos - rightBoundary - leftIndicator
		if newOffset < 0 {
			newOffset = 0
		}
		// Don't scroll past the end
		maxOffset := lineLen - inputWidth + 2 // +2 for potential indicators
		if newOffset > maxOffset {
			newOffset = maxOffset
		}
		r.scrollOffset = newOffset
	}

	return r.scrollOffset != oldOffset
}

// formatDisplayLine returns the visible portion of the input line with elide indicators
// Also returns the cursor position within the displayed string
func (r *REPL) formatDisplayLine() (display string, cursorDisplayPos int) {
	inputWidth := r.getInputAreaWidth()
	lineLen := len(r.currentLine)

	// If line fits entirely, return it as-is with control char replacement
	if lineLen <= inputWidth {
		r.scrollOffset = 0
		return r.formatControlChars(r.currentLine), r.cursorPos
	}

	// Calculate what portion to show
	leftIndicator := r.scrollOffset > 0
	startIdx := r.scrollOffset

	// Available space for content
	contentSpace := inputWidth
	if leftIndicator {
		contentSpace--
	}

	// Check if we need right indicator
	endIdx := startIdx + contentSpace
	if endIdx > lineLen {
		endIdx = lineLen
	}
	rightIndicator := endIdx < lineLen

	// Adjust content space if right indicator needed
	if rightIndicator {
		contentSpace--
		endIdx = startIdx + contentSpace
		if endIdx > lineLen {
			endIdx = lineLen
		}
	}

	// Build the display string
	var buf strings.Builder
	if leftIndicator {
		buf.WriteString(replColorElide + "<" + replColorReset)
	}

	// Format the visible portion with control char replacement
	visiblePortion := r.currentLine[startIdx:endIdx]
	buf.WriteString(r.formatControlChars(visiblePortion))

	if rightIndicator {
		buf.WriteString(replColorElide + ">" + replColorReset)
	}

	// Calculate cursor position in display
	cursorDisplayPos = r.cursorPos - startIdx
	if leftIndicator {
		cursorDisplayPos++ // Account for '<' indicator
	}

	return buf.String(), cursorDisplayPos
}

// formatControlChars replaces control characters (CR, LF) with visible ^M, ^J in elide color
func (r *REPL) formatControlChars(runes []rune) string {
	var buf strings.Builder
	for _, ch := range runes {
		switch ch {
		case '\r':
			buf.WriteString(replColorElide + "^M" + replColorReset)
		case '\n':
			buf.WriteString(replColorElide + "^J" + replColorReset)
		default:
			buf.WriteRune(ch)
		}
	}
	return buf.String()
}

// countDisplayWidth returns the display width of runes, accounting for ^M/^J replacements
func (r *REPL) countDisplayWidth(runes []rune) int {
	width := 0
	for _, ch := range runes {
		if ch == '\r' || ch == '\n' {
			width += 2 // ^M or ^J
		} else {
			width++
		}
	}
	return width
}

func (r *REPL) redrawLine() {
	// Adjust scroll offset to ensure cursor is visible
	r.adjustScrollOffset()

	// Clear line and redraw
	r.output("\r\x1b[K") // Move to start and clear line
	r.printPrompt()

	// Get the formatted display line with potential scroll indicators
	displayLine, cursorDisplayPos := r.formatDisplayLine()
	r.output(displayLine)

	// Calculate the actual display width (accounting for ^M/^J which are 2 chars each)
	// and the ANSI escape codes which don't take display space
	// The cursor position is in terms of logical characters, not bytes
	displayWidth := r.calculateDisplayWidth(displayLine)

	// Move cursor to correct position within the displayed content
	if cursorDisplayPos < displayWidth {
		r.output(fmt.Sprintf("\x1b[%dD", displayWidth-cursorDisplayPos))
	}
}

// calculateDisplayWidth returns the visual width of a string, ignoring ANSI escape codes
func (r *REPL) calculateDisplayWidth(s string) int {
	width := 0
	inEscape := false
	for _, ch := range s {
		if ch == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') {
				inEscape = false
			}
			continue
		}
		width++
	}
	return width
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
		r.scrollOffset = 0 // Reset scroll for new content
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
		r.scrollOffset = 0 // Reset scroll for new content
		r.redrawLine()
	}
}

func (r *REPL) handleLeftArrow() {
	if r.cursorPos > 0 {
		r.cursorPos--
		// If line is longer than visible area, always redraw to handle scroll
		if len(r.currentLine) > r.getInputAreaWidth() {
			r.redrawLine()
		} else {
			r.output("\x1b[D")
		}
	}
}

func (r *REPL) handleRightArrow() {
	if r.cursorPos < len(r.currentLine) {
		r.cursorPos++
		// If line is longer than visible area, always redraw to handle scroll
		if len(r.currentLine) > r.getInputAreaWidth() {
			r.redrawLine()
		} else {
			r.output("\x1b[C")
		}
	}
}

func (r *REPL) handleHome() {
	if r.cursorPos > 0 {
		oldPos := r.cursorPos
		r.cursorPos = 0
		// If line is longer than visible area, redraw to handle scroll
		if len(r.currentLine) > r.getInputAreaWidth() {
			r.redrawLine()
		} else {
			r.output(fmt.Sprintf("\x1b[%dD", oldPos))
		}
	}
}

func (r *REPL) handleEnd() {
	if r.cursorPos < len(r.currentLine) {
		oldPos := r.cursorPos
		r.cursorPos = len(r.currentLine)
		// If line is longer than visible area, redraw to handle scroll
		if len(r.currentLine) > r.getInputAreaWidth() {
			r.redrawLine()
		} else {
			r.output(fmt.Sprintf("\x1b[%dC", len(r.currentLine)-oldPos))
		}
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
	// If input was scrolled/elided, re-echo the full line before newline
	inputWidth := r.getInputAreaWidth()
	wasScrolled := r.scrollOffset > 0 || len(r.currentLine) > inputWidth

	if wasScrolled && len(r.currentLine) > 0 {
		// Move cursor back to start of input area (after prompt)
		// Clear from cursor to end of line, then print full input
		r.output("\r")          // Go to start of line
		r.printPrompt()         // Re-print prompt
		r.output("\x1b[K")      // Clear to end of line (CSI K)
		r.output(replColorReset) // Reset to default color
		// Print full input (this may wrap naturally)
		r.output(string(r.currentLine))
	}

	r.output("\r\n")

	// Reset scroll state for next input
	r.scrollOffset = 0

	// Flush output before potentially blocking execution
	// This ensures the newline appears before async operations like msleep
	if r.flush != nil {
		r.flush()
	}

	line := string(r.currentLine)
	r.lines = append(r.lines, line)
	fullInput := strings.Join(r.lines, "\n")

	// Check if input is complete
	if r.isComplete(fullInput) {
		// Clear input state
		r.lines = nil
		r.currentLine = nil
		r.cursorPos = 0
		r.inHistory = false

		// Process input - may run in background for GUI responsiveness
		r.processInput(fullInput)
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

	// In readline-only mode, just send input to channel and return
	r.mu.Lock()
	readlineOnly := r.readlineOnly
	r.mu.Unlock()

	if readlineOnly {
		// Send to channel (non-blocking with select to avoid deadlock if nobody is reading)
		select {
		case r.readlineChan <- input:
		default:
		}
		return
	}

	// Check for exit commands
	lower := strings.ToLower(trimmed)
	if lower == "exit" || lower == "quit" {
		r.Stop()
		return
	}

	if trimmed == "" {
		r.showPromptIfRunning()
		return
	}

	// Set busy flag to ignore input during execution
	r.mu.Lock()
	r.busy = true
	r.mu.Unlock()

	// Run execution in a goroutine so GUI remains responsive
	go func() {
		// Execute - blocks until complete (including async operations like msleep)
		result := r.ps.Execute(input)

		// Flush any pending output before displaying result
		r.ps.FlushIO()

		// Display result
		r.displayResult(result)

		// Clear busy flag and show prompt
		r.mu.Lock()
		r.busy = false
		running := r.running
		r.mu.Unlock()

		if running {
			r.printPrompt()
		}
	}()
}

func (r *REPL) showPromptIfRunning() {
	r.mu.Lock()
	running := r.running
	r.mu.Unlock()
	if running {
		r.printPrompt()
	}
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
			prefixColor = r.equalsColor()
		} else {
			prefix = "E"
			prefixColor = replColorRed
		}
	} else {
		prefix = "="
		prefixColor = r.equalsColor()
	}

	// Format the result value as PSL with colors from config
	formatted := FormatValueColored(resultValue, true, r.getPSLColors(), r.ps)

	// Print with prefix
	lines := strings.Split(formatted, "\n")
	for i, line := range lines {
		if i == 0 {
			r.output(fmt.Sprintf("%s%s%s %s%s\r\n", prefixColor, prefix, replColorReset, line, replColorReset))
		} else {
			r.output(fmt.Sprintf("  %s%s\r\n", line, replColorReset))
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
		// Check if this is an object marker that needs resolution
		resolved := r.ps.ResolveValue(v)
		if resolved != v {
			// It was a marker, recurse on the resolved value
			return r.toJSONValue(resolved)
		}
		return str
	case string:
		// Check if this is an object marker that needs resolution
		resolved := r.ps.ResolveValue(Symbol(v))
		if sym, ok := resolved.(Symbol); !ok || string(sym) != v {
			// It was a marker or resolved to something else
			return r.toJSONValue(resolved)
		}
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

// History file constants
const (
	replMaxHistoryLines = 1000 // Maximum number of history entries to keep
)

// getReplHistoryFilePath returns the path to ~/.paw/repl-history.psl
func getReplHistoryFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".paw", "repl-history.psl")
}

// loadReplHistory loads command history from the PSL history file
func loadReplHistory() []string {
	historyPath := getReplHistoryFilePath()
	if historyPath == "" {
		return nil
	}

	content, err := os.ReadFile(historyPath)
	if err != nil {
		return nil // File doesn't exist or can't be read
	}

	// Parse as PSL list
	pslList, err := ParsePSLList(string(content))
	if err != nil {
		return nil // Invalid format
	}

	// Convert to string slice
	history := make([]string, 0, len(pslList))
	for _, item := range pslList {
		if s, ok := item.(string); ok {
			history = append(history, s)
		}
	}
	return history
}

// saveReplHistory saves command history to the PSL history file
func saveReplHistory(history []string) {
	historyPath := getReplHistoryFilePath()
	if historyPath == "" {
		return
	}

	// Ensure config directory exists
	configDir := filepath.Dir(historyPath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return // Graceful failure
	}

	// Limit history size
	if len(history) > replMaxHistoryLines {
		history = history[len(history)-replMaxHistoryLines:]
	}

	// Convert to PSL list and serialize
	pslList := make(PSLList, len(history))
	for i, cmd := range history {
		pslList[i] = cmd
	}
	content := SerializePSLList(pslList)

	_ = os.WriteFile(historyPath, []byte(content+"\n"), 0644)
}
