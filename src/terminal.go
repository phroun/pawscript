package pawscript

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/term"
)

// TerminalCapabilities holds terminal capabilities that can be associated with a channel
// This allows different channels (e.g., system stdout vs gui_console) to report
// their own capabilities independently
type TerminalCapabilities struct {
	mu sync.RWMutex

	// Terminal type and detection
	TermType     string // e.g., "xterm-256color", "gui-console"
	IsTerminal   bool   // true if this is an interactive terminal
	IsRedirected bool   // true if output is being redirected (piped/file)
	SupportsANSI bool   // true if ANSI escape codes are supported
	SupportsColor bool  // true if color output is supported
	ColorDepth   int    // 0=none, 8=basic, 16=extended, 256=256color, 24=truecolor

	// Screen dimensions
	Width  int // columns
	Height int // rows

	// Input capabilities
	SupportsInput bool // true if this channel can receive input
	EchoEnabled   bool // true if input should be echoed (duplex mode)
	LineMode      bool // true if input is line-buffered, false for raw/char mode

	// Custom metadata (for host-provided channels)
	Metadata map[string]interface{}
}

// NewTerminalCapabilities creates a new capabilities struct with defaults
func NewTerminalCapabilities() *TerminalCapabilities {
	return &TerminalCapabilities{
		TermType:      "unknown",
		IsTerminal:    false,
		IsRedirected:  false,
		SupportsANSI:  false,
		SupportsColor: false,
		ColorDepth:    0,
		Width:         80,
		Height:        24,
		SupportsInput: false,
		EchoEnabled:   true,
		LineMode:      true,
		Metadata:      make(map[string]interface{}),
	}
}

// DetectSystemTerminalCapabilities creates capabilities by detecting the system terminal
func DetectSystemTerminalCapabilities() *TerminalCapabilities {
	caps := NewTerminalCapabilities()

	// Check if stdout is a terminal
	caps.IsTerminal = term.IsTerminal(int(os.Stdout.Fd()))

	// Set IsRedirected when stdout is not a terminal (piped or file)
	caps.IsRedirected = !caps.IsTerminal

	// Get terminal type from environment
	caps.TermType = os.Getenv("TERM")
	if caps.TermType == "" {
		caps.TermType = "unknown"
	}

	// When redirected, use special rules:
	// - Act as 80x24 screen with color support
	// - Emit ANSI codes to output stream (for test comparison, etc.)
	if caps.IsRedirected {
		caps.Width = 80
		caps.Height = 24
		// Enable ANSI/color if TERM suggests the original terminal would support it
		caps.SupportsANSI = detectANSISupportForRedirect(caps.TermType)
		caps.SupportsColor, caps.ColorDepth = detectColorSupport(caps.TermType)
	} else {
		// Normal terminal detection
		caps.SupportsANSI = detectANSISupport(caps.TermType, caps.IsTerminal)
		caps.SupportsColor, caps.ColorDepth = detectColorSupport(caps.TermType)

		// Detect screen size
		width, height, err := term.GetSize(int(os.Stdout.Fd()))
		if err == nil && width > 0 && height > 0 {
			caps.Width = width
			caps.Height = height
		}
	}

	// System terminal supports input on stdin
	caps.SupportsInput = term.IsTerminal(int(os.Stdin.Fd()))
	caps.EchoEnabled = true
	caps.LineMode = true

	return caps
}

// detectANSISupport checks if the terminal likely supports ANSI escape codes
func detectANSISupport(termType string, isTerminal bool) bool {
	if !isTerminal {
		return false
	}
	if termType == "" || termType == "dumb" {
		return false
	}

	termLower := strings.ToLower(termType)

	// Check for known ANSI-supporting terminal types
	ansiTerms := []string{
		"xterm", "vt100", "vt102", "vt220", "vt320", "ansi", "linux",
		"screen", "tmux", "rxvt", "konsole", "gnome", "putty",
		"cygwin", "mintty", "eterm", "alacritty", "kitty", "iterm",
	}

	for _, t := range ansiTerms {
		if strings.Contains(termLower, t) {
			return true
		}
	}

	// Check COLORTERM environment variable
	if os.Getenv("COLORTERM") != "" {
		return true
	}

	// If TERM is set and not "dumb", assume ANSI support
	return true
}

// detectANSISupportForRedirect checks if ANSI is supported when output is redirected.
// When redirected, we check TERM type to determine if the original terminal would
// support ANSI, and if so, we emit codes to the redirected output.
func detectANSISupportForRedirect(termType string) bool {
	if termType == "" || termType == "dumb" {
		return false
	}

	termLower := strings.ToLower(termType)

	// Check for known ANSI-supporting terminal types
	ansiTerms := []string{
		"xterm", "vt100", "vt102", "vt220", "vt320", "ansi", "linux",
		"screen", "tmux", "rxvt", "konsole", "gnome", "putty",
		"cygwin", "mintty", "eterm", "alacritty", "kitty", "iterm",
	}

	for _, t := range ansiTerms {
		if strings.Contains(termLower, t) {
			return true
		}
	}

	// Check COLORTERM environment variable
	if os.Getenv("COLORTERM") != "" {
		return true
	}

	// If TERM is set and not "dumb", assume ANSI support
	return true
}

// detectColorSupport checks if terminal supports color and returns depth
func detectColorSupport(termType string) (supportsColor bool, depth int) {
	termLower := strings.ToLower(termType)
	colorTerm := strings.ToLower(os.Getenv("COLORTERM"))

	// Check for truecolor
	if colorTerm == "truecolor" || colorTerm == "24bit" {
		return true, 24
	}
	if strings.Contains(termLower, "truecolor") || strings.Contains(termLower, "24bit") {
		return true, 24
	}

	// Check for 256 color
	if strings.Contains(termLower, "256color") {
		return true, 256
	}

	// Check COLORTERM
	if colorTerm != "" {
		return true, 16
	}

	// Check for known color terminals
	colorTerms := []string{
		"xterm", "linux", "screen", "tmux", "rxvt", "konsole",
		"gnome", "putty", "cygwin", "mintty", "eterm", "alacritty",
		"kitty", "iterm",
	}

	for _, t := range colorTerms {
		if strings.Contains(termLower, t) {
			return true, 16
		}
	}

	if strings.Contains(termLower, "color") {
		return true, 8
	}

	return false, 0
}

// Clone creates a copy of the terminal capabilities
func (tc *TerminalCapabilities) Clone() *TerminalCapabilities {
	if tc == nil {
		return nil
	}
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	clone := &TerminalCapabilities{
		TermType:      tc.TermType,
		IsTerminal:    tc.IsTerminal,
		SupportsANSI:  tc.SupportsANSI,
		SupportsColor: tc.SupportsColor,
		ColorDepth:    tc.ColorDepth,
		Width:         tc.Width,
		Height:        tc.Height,
		SupportsInput: tc.SupportsInput,
		EchoEnabled:   tc.EchoEnabled,
		LineMode:      tc.LineMode,
		Metadata:      make(map[string]interface{}),
	}

	for k, v := range tc.Metadata {
		clone.Metadata[k] = v
	}

	return clone
}

// SetSize updates the terminal dimensions
func (tc *TerminalCapabilities) SetSize(width, height int) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.Width = width
	tc.Height = height
}

// GetSize returns the terminal dimensions
func (tc *TerminalCapabilities) GetSize() (width, height int) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.Width, tc.Height
}

// systemTerminalCaps is the singleton for system terminal capabilities
var systemTerminalCaps *TerminalCapabilities
var systemTerminalCapsOnce sync.Once

// GetSystemTerminalCapabilities returns the detected system terminal capabilities
func GetSystemTerminalCapabilities() *TerminalCapabilities {
	systemTerminalCapsOnce.Do(func() {
		systemTerminalCaps = DetectSystemTerminalCapabilities()
	})
	return systemTerminalCaps
}

// TerminalState holds cursor position and terminal configuration
type TerminalState struct {
	mu sync.RWMutex

	// Sticky cursor parameters
	XBase  int // leftmost column value (default 1)
	YBase  int // topmost row value (default 1)
	Rows   int // total rows in region (default 24)
	Cols   int // total cols in region (default 80)
	Indent int // columns reserved on left
	Head   int // rows reserved on top

	// Current cursor position (relative to xbase/ybase after indent/head)
	X int
	Y int

	// Cursor appearance
	Visible bool   // cursor visibility
	Shape   string // "underline", "block", "bar", "half"
	Blink   string // "false", "true", "fast"
	Color   int    // cursor color number

	// Behavior
	Free   bool // true = can move into margin/head areas
	Duplex bool // true = echo input to terminal (default true)

	// Screen tracking
	ScreenRows int // detected physical rows
	ScreenCols int // detected physical cols

	// Clear tracking
	HasCleared bool // true if clear was last output operation

	// Color tracking (for preserving background when only foreground changes)
	CurrentFG int // current foreground color (-1 = default)
	CurrentBG int // current background color (-1 = default)

	// Attribute tracking
	Bold      bool
	AttrBlink bool // named differently to avoid conflict with cursor Blink
	Underline bool
	Invert    bool

	// Terminal state for restoring
	originalTermState *term.State
}

// NewTerminalState creates a new terminal state with defaults
func NewTerminalState() *TerminalState {
	ts := &TerminalState{
		XBase:     1,
		YBase:     1,
		Rows:      24,
		Cols:      80,
		Indent:    0,
		Head:      0,
		X:         1,
		Y:         1,
		Visible:   true,
		Shape:     "block",
		Blink:     "true",
		Color:     -1, // -1 means default
		Free:      false,
		Duplex:    true, // echo enabled by default
		CurrentFG: -1,   // -1 means default
		CurrentBG: -1,   // -1 means default
	}

	// Try to detect actual screen size
	ts.detectScreenSize()

	return ts
}

// detectScreenSize attempts to get the terminal dimensions
func (ts *TerminalState) detectScreenSize() {
	width, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err == nil && width > 0 && height > 0 {
		ts.ScreenCols = width
		ts.ScreenRows = height
	} else {
		// Fallback defaults
		ts.ScreenCols = 80
		ts.ScreenRows = 24
	}
}

// IsTerminal checks if stdout is a terminal
func IsTerminal() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// IsRedirected checks if stdout is being redirected (piped or file)
func IsRedirected() bool {
	return !term.IsTerminal(int(os.Stdout.Fd()))
}

// GetPhysicalX returns the physical column on screen
func (ts *TerminalState) GetPhysicalX() int {
	return ts.X + ts.Indent + ts.XBase - 1
}

// GetPhysicalY returns the physical row on screen
func (ts *TerminalState) GetPhysicalY() int {
	return ts.Y + ts.Head + ts.YBase - 1
}

// ClampPosition ensures cursor is within bounds based on Free setting
func (ts *TerminalState) ClampPosition() {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.Free {
		// Only bounded by physical screen
		if ts.ScreenCols > 0 {
			physX := ts.GetPhysicalX()
			if physX < 1 {
				ts.X = 1 - ts.Indent - ts.XBase + 1
			}
			if physX > ts.ScreenCols {
				ts.X = ts.ScreenCols - ts.Indent - ts.XBase + 1
			}
		}
		if ts.ScreenRows > 0 {
			physY := ts.GetPhysicalY()
			if physY < 1 {
				ts.Y = 1 - ts.Head - ts.YBase + 1
			}
			if physY > ts.ScreenRows {
				ts.Y = ts.ScreenRows - ts.Head - ts.YBase + 1
			}
		}
	} else {
		// Bounded by region
		if ts.X < ts.XBase {
			ts.X = ts.XBase
		}
		maxX := ts.Cols
		if ts.ScreenCols > 0 {
			availCols := ts.ScreenCols - ts.Indent
			if availCols < maxX {
				maxX = availCols
			}
		}
		if ts.X > maxX {
			ts.X = maxX
		}

		if ts.Y < ts.YBase {
			ts.Y = ts.YBase
		}
		maxY := ts.Rows
		if ts.ScreenRows > 0 {
			availRows := ts.ScreenRows - ts.Head
			if availRows < maxY {
				maxY = availRows
			}
		}
		if ts.Y > maxY {
			ts.Y = maxY
		}
	}
}

// ANSI escape code helpers

// ClearScreen returns ANSI code to clear screen
func ANSIClearScreen() string {
	return "\x1b[2J\x1b[H"
}

// MoveCursor returns ANSI code to position cursor
func ANSIMoveCursor(row, col int) string {
	return fmt.Sprintf("\x1b[%d;%dH", row, col)
}

// MoveCursorRelative returns ANSI codes for relative movement
func ANSIMoveRelative(h, v int) string {
	var codes strings.Builder
	if h > 0 {
		codes.WriteString(fmt.Sprintf("\x1b[%dC", h)) // forward
	} else if h < 0 {
		codes.WriteString(fmt.Sprintf("\x1b[%dD", -h)) // back
	}
	if v > 0 {
		codes.WriteString(fmt.Sprintf("\x1b[%dB", v)) // down
	} else if v < 0 {
		codes.WriteString(fmt.Sprintf("\x1b[%dA", -v)) // up
	}
	return codes.String()
}

// ShowCursor returns ANSI code to show cursor
func ANSIShowCursor() string {
	return "\x1b[?25h"
}

// HideCursor returns ANSI code to hide cursor
func ANSIHideCursor() string {
	return "\x1b[?25l"
}

// SetCursorShape returns ANSI code for cursor shape
// Shapes: 0=default, 1=blinking block, 2=steady block, 3=blinking underline,
// 4=steady underline, 5=blinking bar, 6=steady bar
// Blink: "true"/"fast" = blinking, "false" = steady
func ANSISetCursorShape(shape string, blink string) string {
	var code int
	blinkLower := strings.ToLower(blink)
	isBlinking := blinkLower != "false"

	switch strings.ToLower(shape) {
	case "block":
		if isBlinking {
			code = 1
		} else {
			code = 2
		}
	case "underline":
		if isBlinking {
			code = 3
		} else {
			code = 4
		}
	case "bar":
		if isBlinking {
			code = 5
		} else {
			code = 6
		}
	case "half":
		// Half block - not standard, fall back to block
		if isBlinking {
			code = 1
		} else {
			code = 2
		}
	default:
		code = 0 // default
	}
	return fmt.Sprintf("\x1b[%d q", code)
}

// ParseColorName converts a color name or number to CGA color index (0-15)
// Returns -1 for invalid/default
func ParseColorName(name string) int {
	// Normalize: lowercase, trim spaces
	name = strings.ToLower(strings.TrimSpace(name))

	// Try parsing as number first
	var num int
	if _, err := fmt.Sscanf(name, "%d", &num); err == nil {
		if num >= 0 && num <= 15 {
			return num
		}
		return -1
	}

	// Color name mapping (CGA/EGA/VGA text mode colors)
	switch name {
	case "black":
		return 0
	case "blue":
		return 1
	case "green":
		return 2
	case "cyan", "teal":
		return 3
	case "red":
		return 4
	case "purple", "magenta":
		return 5
	case "brown", "orange", "amber", "dark yellow":
		return 6
	case "silver", "grey", "gray", "dark white", "default":
		return 7
	case "light black", "bright black", "dark gray", "dark grey":
		return 8
	case "bright blue", "light blue":
		return 9
	case "bright green", "light green":
		return 10
	case "aqua", "bright cyan", "light cyan":
		return 11
	case "bright red", "light red":
		return 12
	case "pink", "light purple", "bright purple", "light magenta", "bright magenta":
		return 13
	case "yellow", "bright amber", "light amber", "bright yellow", "light yellow":
		return 14
	case "white", "bright white":
		return 15
	default:
		return -1
	}
}

// CGAToANSIFG converts CGA color (0-15) to ANSI foreground code
func CGAToANSIFG(cga int) int {
	if cga < 0 || cga > 15 {
		return 39 // default
	}
	// CGA to ANSI color mapping
	// CGA: 0=black, 1=blue, 2=green, 3=cyan, 4=red, 5=magenta, 6=brown, 7=light gray
	// ANSI: 30=black, 31=red, 32=green, 33=yellow, 34=blue, 35=magenta, 36=cyan, 37=white
	cgaToANSI := []int{30, 34, 32, 36, 31, 35, 33, 37} // for colors 0-7
	if cga < 8 {
		return cgaToANSI[cga]
	}
	// Bright colors (8-15) use 90-97
	return cgaToANSI[cga-8] + 60
}

// CGAToANSIBG converts CGA color (0-15) to ANSI background code
func CGAToANSIBG(cga int) int {
	if cga < 0 || cga > 15 {
		return 49 // default
	}
	cgaToANSI := []int{40, 44, 42, 46, 41, 45, 43, 47} // for colors 0-7
	if cga < 8 {
		return cgaToANSI[cga]
	}
	// Bright background colors (8-15) use 100-107
	return cgaToANSI[cga-8] + 60
}

// ANSIColor generates ANSI escape sequence for colors and attributes
// fg, bg: CGA color numbers (0-15), -1 for default/unchanged
// bold, blink, underline, invert: attribute flags
func ANSIColor(fg, bg int, bold, blink, underline, invert bool) string {
	var codes []string

	// Start with reset if we have any attributes to ensure clean state
	// But we'll rebuild colors after reset
	needsReset := bold || blink || underline || invert

	if needsReset {
		// We need to set attributes, so we reset first and rebuild
		codes = append(codes, "0")
		if bold {
			codes = append(codes, "1")
		}
		if blink {
			codes = append(codes, "5")
		}
		if underline {
			codes = append(codes, "4")
		}
		if invert {
			codes = append(codes, "7")
		}
	}

	// Add foreground color
	if fg >= 0 && fg <= 15 {
		codes = append(codes, fmt.Sprintf("%d", CGAToANSIFG(fg)))
	} else if needsReset && fg == -1 {
		// After reset, explicitly set default if no color specified
		codes = append(codes, "39")
	}

	// Add background color
	if bg >= 0 && bg <= 15 {
		codes = append(codes, fmt.Sprintf("%d", CGAToANSIBG(bg)))
	} else if needsReset && bg == -1 {
		// After reset, explicitly set default if no color specified
		codes = append(codes, "49")
	}

	if len(codes) == 0 {
		return ""
	}

	return fmt.Sprintf("\x1b[%sm", strings.Join(codes, ";"))
}

// ANSIColorPreserving generates ANSI color sequence while preserving existing background
// This is used when only foreground is specified
func ANSIColorPreserving(fg, currentBG int, bold, blink, underline, invert bool) string {
	// If we have attributes, we need to reset and rebuild everything
	if bold || blink || underline || invert {
		return ANSIColor(fg, currentBG, bold, blink, underline, invert)
	}

	// No attributes, just set foreground
	if fg >= 0 && fg <= 15 {
		return fmt.Sprintf("\x1b[%dm", CGAToANSIFG(fg))
	}
	return ""
}

// ANSIClearMode returns ANSI clear sequence for different modes
func ANSIClearMode(mode string) string {
	switch strings.ToLower(mode) {
	case "eol": // clear to end of line
		return "\x1b[K"
	case "bol": // clear to beginning of line
		return "\x1b[1K"
	case "line": // clear entire line
		return "\x1b[2K"
	case "eos": // clear to end of screen
		return "\x1b[J"
	case "bos": // clear to beginning of screen
		return "\x1b[1J"
	case "screen": // clear entire screen
		return "\x1b[2J"
	default:
		return ""
	}
}

// ANSIReset returns the ANSI reset sequence
func ANSIReset() string {
	return "\x1b[0m"
}

// GetTerminalType returns the terminal type from TERM environment variable
func GetTerminalType() string {
	term := os.Getenv("TERM")
	if term == "" {
		return "unknown"
	}
	return term
}

// SupportsANSI returns true if the terminal likely supports ANSI escape codes
func SupportsANSI() bool {
	// If not a terminal, likely doesn't support ANSI (unless it's a pipe to something that does)
	if !IsTerminal() {
		return false
	}

	termType := os.Getenv("TERM")
	if termType == "" {
		return false
	}

	// Check for known ANSI-supporting terminal types
	termType = strings.ToLower(termType)

	// Most modern terminals support ANSI
	ansiTerms := []string{
		"xterm", "vt100", "vt102", "vt220", "vt320", "ansi", "linux",
		"screen", "tmux", "rxvt", "konsole", "gnome", "putty",
		"cygwin", "mintty", "eterm", "alacritty", "kitty", "iterm",
	}

	for _, t := range ansiTerms {
		if strings.Contains(termType, t) {
			return true
		}
	}

	// Also check COLORTERM environment variable
	colorTerm := os.Getenv("COLORTERM")
	if colorTerm != "" {
		return true
	}

	// If TERM is set and not "dumb", assume ANSI support
	return termType != "dumb"
}

// SupportsColor returns true if the terminal likely supports color
func SupportsColor() bool {
	if !SupportsANSI() {
		return false
	}

	termType := strings.ToLower(os.Getenv("TERM"))

	// Check for explicit color support indicators
	if strings.Contains(termType, "color") || strings.Contains(termType, "256color") || strings.Contains(termType, "truecolor") {
		return true
	}

	// Check COLORTERM
	colorTerm := os.Getenv("COLORTERM")
	if colorTerm != "" {
		return true
	}

	// Most xterm-like terminals support color
	colorTerms := []string{
		"xterm", "linux", "screen", "tmux", "rxvt", "konsole",
		"gnome", "putty", "cygwin", "mintty", "eterm", "alacritty",
		"kitty", "iterm",
	}

	for _, t := range colorTerms {
		if strings.Contains(termType, t) {
			return true
		}
	}

	return false
}

// ChannelSupportsANSI returns true if the channel's terminal supports ANSI escape codes
// Falls back to system terminal if channel is nil or has no terminal capabilities
func ChannelSupportsANSI(ch *StoredChannel) bool {
	if ch == nil {
		return SupportsANSI()
	}
	caps := ch.GetTerminalCapabilities()
	if caps == nil {
		return SupportsANSI()
	}
	caps.mu.RLock()
	defer caps.mu.RUnlock()
	return caps.SupportsANSI
}

// ChannelSupportsColor returns true if the channel's terminal supports color
// Falls back to system terminal if channel is nil or has no terminal capabilities
func ChannelSupportsColor(ch *StoredChannel) bool {
	if ch == nil {
		return SupportsColor()
	}
	caps := ch.GetTerminalCapabilities()
	if caps == nil {
		return SupportsColor()
	}
	caps.mu.RLock()
	defer caps.mu.RUnlock()
	return caps.SupportsColor
}

// ChannelIsTerminal returns true if the channel represents an interactive terminal
// Falls back to system terminal if channel is nil or has no terminal capabilities
func ChannelIsTerminal(ch *StoredChannel) bool {
	if ch == nil {
		return IsTerminal()
	}
	caps := ch.GetTerminalCapabilities()
	if caps == nil {
		return IsTerminal()
	}
	caps.mu.RLock()
	defer caps.mu.RUnlock()
	return caps.IsTerminal
}

// ChannelIsRedirected returns true if the channel's output is being redirected
// Falls back to system terminal if channel is nil or has no terminal capabilities
func ChannelIsRedirected(ch *StoredChannel) bool {
	if ch == nil {
		return IsRedirected()
	}
	caps := ch.GetTerminalCapabilities()
	if caps == nil {
		return IsRedirected()
	}
	caps.mu.RLock()
	defer caps.mu.RUnlock()
	return caps.IsRedirected
}

// ChannelGetTerminalType returns the terminal type for the channel
// Falls back to system terminal if channel is nil or has no terminal capabilities
func ChannelGetTerminalType(ch *StoredChannel) string {
	if ch == nil {
		return GetTerminalType()
	}
	caps := ch.GetTerminalCapabilities()
	if caps == nil {
		return GetTerminalType()
	}
	caps.mu.RLock()
	defer caps.mu.RUnlock()
	return caps.TermType
}

// ChannelGetSize returns the terminal dimensions for the channel
// Falls back to system terminal if channel is nil or has no terminal capabilities
func ChannelGetSize(ch *StoredChannel) (width, height int) {
	if ch == nil {
		return GetSystemTerminalCapabilities().GetSize()
	}
	caps := ch.GetTerminalCapabilities()
	if caps == nil {
		return GetSystemTerminalCapabilities().GetSize()
	}
	return caps.GetSize()
}

// SetDuplex enables or disables terminal echo (duplex mode)
// When duplex is true (default), typed characters are echoed to the screen
// When duplex is false, typed characters are not echoed (for password entry, etc.)
func (ts *TerminalState) SetDuplex(enabled bool) error {
	if !IsTerminal() {
		// Not a terminal, can't control echo
		ts.Duplex = enabled
		return nil
	}

	fd := int(os.Stdin.Fd())

	if enabled {
		// Restore original terminal state if we have it
		if ts.originalTermState != nil {
			err := term.Restore(fd, ts.originalTermState)
			if err != nil {
				return err
			}
			ts.originalTermState = nil
		}
		ts.Duplex = true
	} else {
		// Save original state and disable echo
		if ts.originalTermState == nil {
			state, err := term.GetState(fd)
			if err != nil {
				return err
			}
			ts.originalTermState = state
		}
		// Make raw to disable echo, but we'll use a custom approach
		// Actually, we need to manipulate termios directly for echo only
		// For simplicity, we'll use raw mode which also disables echo
		_, err := term.MakeRaw(fd)
		if err != nil {
			return err
		}
		ts.Duplex = false
	}
	return nil
}

// ResetTerminal performs a full terminal reset
// This is equivalent to 'tput reset' - resets all attributes and state
func (ts *TerminalState) ResetTerminal() {
	// Restore terminal state if modified
	if ts.originalTermState != nil {
		fd := int(os.Stdin.Fd())
		_ = term.Restore(fd, ts.originalTermState)
		ts.originalTermState = nil
	}

	// Emit reset sequences
	// \e[0m - Reset all attributes
	// \e[?25h - Show cursor
	// \e[?7h - Enable line wrap
	// \ec - Full terminal reset (RIS - Reset to Initial State)
	fmt.Print("\x1b[0m\x1b[?25h\x1b[?7h")

	// Reset all tracked state
	ts.CurrentFG = -1
	ts.CurrentBG = -1
	ts.Bold = false
	ts.AttrBlink = false
	ts.Underline = false
	ts.Invert = false
	ts.Visible = true
	ts.Shape = "block"
	ts.Blink = "true"
	ts.Duplex = true
	ts.HasCleared = false

	// Re-detect screen size
	ts.detectScreenSize()
}
