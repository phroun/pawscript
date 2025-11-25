package pawscript

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/term"
)

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
	Free bool // true = can move into margin/head areas

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
		CurrentFG: -1, // -1 means default
		CurrentBG: -1, // -1 means default
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
func ANSISetCursorShape(shape string, blink string) string {
	var code int
	switch strings.ToLower(shape) {
	case "block":
		if blink == "false" {
			code = 2
		} else {
			code = 1
		}
	case "underline":
		if blink == "false" {
			code = 4
		} else {
			code = 3
		}
	case "bar":
		if blink == "false" {
			code = 6
		} else {
			code = 5
		}
	case "half":
		// Half block - not standard, fall back to block
		if blink == "false" {
			code = 2
		} else {
			code = 1
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
