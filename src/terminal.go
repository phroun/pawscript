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
}

// NewTerminalState creates a new terminal state with defaults
func NewTerminalState() *TerminalState {
	ts := &TerminalState{
		XBase:   1,
		YBase:   1,
		Rows:    24,
		Cols:    80,
		Indent:  0,
		Head:    0,
		X:       1,
		Y:       1,
		Visible: true,
		Shape:   "block",
		Blink:   "true",
		Color:   -1, // -1 means default
		Free:    false,
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
