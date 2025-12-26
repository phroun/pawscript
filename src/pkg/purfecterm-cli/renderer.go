package cli

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/phroun/purfecterm"
)

// Renderer handles rendering the terminal buffer to the actual CLI terminal
type Renderer struct {
	term *Terminal
	mu   sync.Mutex

	// Render state
	renderNeeded bool
	lastCells    [][]renderedCell // Previous frame for differential rendering
	renderTicker *time.Ticker

	// Output buffer for batching writes
	output strings.Builder

	// Border characters
	borderChars borderCharSet
}

// renderedCell stores the last rendered state of a cell for diff comparison
type renderedCell struct {
	char          rune
	combining     string
	fg            purfecterm.Color
	bg            purfecterm.Color
	bold          bool
	italic        bool
	underline     bool
	reverse       bool
	blink         bool
	strikethrough bool
}

// borderCharSet contains the characters for drawing borders
type borderCharSet struct {
	topLeft     rune
	topRight    rune
	bottomLeft  rune
	bottomRight rune
	horizontal  rune
	vertical    rune
	titleLeft   rune
	titleRight  rune
}

var borderStyles = map[BorderStyle]borderCharSet{
	BorderSingle: {
		topLeft: '\u250c', topRight: '\u2510', bottomLeft: '\u2514', bottomRight: '\u2518',
		horizontal: '\u2500', vertical: '\u2502', titleLeft: '\u2524', titleRight: '\u251c',
	},
	BorderDouble: {
		topLeft: '\u2554', topRight: '\u2557', bottomLeft: '\u255a', bottomRight: '\u255d',
		horizontal: '\u2550', vertical: '\u2551', titleLeft: '\u2561', titleRight: '\u255e',
	},
	BorderHeavy: {
		topLeft: '\u250f', topRight: '\u2513', bottomLeft: '\u2517', bottomRight: '\u251b',
		horizontal: '\u2501', vertical: '\u2503', titleLeft: '\u252b', titleRight: '\u2523',
	},
	BorderRounded: {
		topLeft: '\u256d', topRight: '\u256e', bottomLeft: '\u2570', bottomRight: '\u256f',
		horizontal: '\u2500', vertical: '\u2502', titleLeft: '\u2524', titleRight: '\u251c',
	},
}

// NewRenderer creates a new renderer for the terminal
func NewRenderer(term *Terminal) *Renderer {
	r := &Renderer{
		term:         term,
		renderNeeded: true,
	}

	if term.options.BorderStyle != BorderNone {
		r.borderChars = borderStyles[term.options.BorderStyle]
	}

	return r
}

// RequestRender marks that a render is needed
func (r *Renderer) RequestRender() {
	r.mu.Lock()
	r.renderNeeded = true
	r.mu.Unlock()
}

// RenderLoop runs the main render loop
func (r *Renderer) RenderLoop() {
	// Render at ~60fps max, but only when needed
	r.renderTicker = time.NewTicker(16 * time.Millisecond)
	defer r.renderTicker.Stop()

	for {
		select {
		case <-r.renderTicker.C:
			r.mu.Lock()
			needsRender := r.renderNeeded
			r.renderNeeded = false
			r.mu.Unlock()

			if needsRender {
				r.Render()
			}
		case <-r.term.stopRender:
			return
		}
	}
}

// Render performs a full or differential render of the terminal
func (r *Renderer) Render() {
	r.term.mu.Lock()
	opts := r.term.options
	buffer := r.term.buffer
	r.term.mu.Unlock()

	cols, rows := buffer.GetSize()
	cursorX, cursorY := buffer.GetCursor()
	cursorVisible := buffer.IsCursorVisible()
	isDark := buffer.IsDarkTheme()
	scrollOffset := buffer.GetScrollOffset()

	// Calculate window position
	startX := opts.OffsetX
	startY := opts.OffsetY

	// Account for border
	contentStartX := startX
	contentStartY := startY
	if opts.BorderStyle != BorderNone {
		contentStartX++
		contentStartY++
	}

	// Reset output buffer
	r.output.Reset()

	// Hide cursor during rendering to prevent flicker
	r.output.WriteString("\033[?25l")

	// Draw border if configured
	if opts.BorderStyle != BorderNone {
		r.renderBorder(startX, startY, cols, rows, opts.Title, scrollOffset)
	}

	// Get previous frame for differential rendering
	prevCells := r.lastCells
	needsFullRender := prevCells == nil || len(prevCells) != rows

	// Initialize new cell buffer
	newCells := make([][]renderedCell, rows)
	for y := 0; y < rows; y++ {
		newCells[y] = make([]renderedCell, cols)
	}

	// Current attributes for SGR optimization
	var currentFg, currentBg purfecterm.Color
	currentBold := false
	currentItalic := false
	currentUnderline := false
	currentReverse := false
	currentBlink := false
	currentStrikethrough := false
	firstAttr := true

	// Render each cell
	for y := 0; y < rows; y++ {
		rowChanged := needsFullRender
		if !needsFullRender && len(prevCells[y]) != cols {
			rowChanged = true
		}

		for x := 0; x < cols; x++ {
			cell := buffer.GetVisibleCell(x, y)

			// Resolve colors based on theme
			fg := opts.Scheme.ResolveColor(cell.Foreground, true, isDark)
			bg := opts.Scheme.ResolveColor(cell.Background, false, isDark)

			// Handle reverse video
			if cell.Reverse {
				fg, bg = bg, fg
			}

			// Store for next frame comparison
			newCells[y][x] = renderedCell{
				char:          cell.Char,
				combining:     cell.Combining,
				fg:            fg,
				bg:            bg,
				bold:          cell.Bold,
				italic:        cell.Italic,
				underline:     cell.Underline,
				reverse:       cell.Reverse,
				blink:         cell.Blink,
				strikethrough: cell.Strikethrough,
			}

			// Check if cell changed
			if !rowChanged && !needsFullRender {
				prev := prevCells[y][x]
				if prev.char == cell.Char &&
					prev.combining == cell.Combining &&
					prev.fg == fg &&
					prev.bg == bg &&
					prev.bold == cell.Bold &&
					prev.italic == cell.Italic &&
					prev.underline == cell.Underline &&
					prev.blink == cell.Blink &&
					prev.strikethrough == cell.Strikethrough {
					continue
				}
			}

			// Move cursor to position
			r.output.WriteString(fmt.Sprintf("\033[%d;%dH", contentStartY+y+1, contentStartX+x+1))

			// Build SGR sequence for attributes
			var sgr []string

			// Check if we need to reset
			needsReset := false
			if !firstAttr {
				if (currentBold && !cell.Bold) ||
					(currentItalic && !cell.Italic) ||
					(currentUnderline && !cell.Underline) ||
					(currentReverse && !cell.Reverse) ||
					(currentBlink && !cell.Blink) ||
					(currentStrikethrough && !cell.Strikethrough) {
					needsReset = true
				}
			}

			if needsReset || firstAttr {
				sgr = append(sgr, "0") // Reset
				currentBold = false
				currentItalic = false
				currentUnderline = false
				currentReverse = false
				currentBlink = false
				currentStrikethrough = false
				currentFg = purfecterm.Color{}
				currentBg = purfecterm.Color{}
			}
			firstAttr = false

			// Add attributes
			if cell.Bold && !currentBold {
				sgr = append(sgr, "1")
				currentBold = true
			}
			if cell.Italic && !currentItalic {
				sgr = append(sgr, "3")
				currentItalic = true
			}
			if cell.Underline && !currentUnderline {
				sgr = append(sgr, "4")
				currentUnderline = true
			}
			if cell.Blink && !currentBlink {
				sgr = append(sgr, "5")
				currentBlink = true
			}
			if cell.Strikethrough && !currentStrikethrough {
				sgr = append(sgr, "9")
				currentStrikethrough = true
			}

			// Add colors
			if fg != currentFg {
				sgr = append(sgr, fg.ToSGRCode(true))
				currentFg = fg
			}
			if bg != currentBg {
				sgr = append(sgr, bg.ToSGRCode(false))
				currentBg = bg
			}

			// Write SGR sequence if needed
			if len(sgr) > 0 {
				r.output.WriteString("\033[")
				r.output.WriteString(strings.Join(sgr, ";"))
				r.output.WriteString("m")
			}

			// Write character
			if cell.Char == 0 || cell.Char == ' ' {
				r.output.WriteRune(' ')
			} else {
				r.output.WriteRune(cell.Char)
				if cell.Combining != "" {
					r.output.WriteString(cell.Combining)
				}
			}
		}
	}

	// Render status bar if configured
	if opts.ShowStatusBar {
		r.renderStatusBar(startX, contentStartY+rows, cols, scrollOffset)
	}

	// Reset attributes
	r.output.WriteString("\033[0m")

	// Position and show cursor if visible and not scrolled
	if cursorVisible && scrollOffset == 0 {
		r.output.WriteString(fmt.Sprintf("\033[%d;%dH", contentStartY+cursorY+1, contentStartX+cursorX+1))
		r.output.WriteString("\033[?25h")
	}

	// Flush output
	os.Stdout.WriteString(r.output.String())

	// Store current frame
	r.lastCells = newCells
}

// renderBorder draws the terminal window border
func (r *Renderer) renderBorder(x, y, innerCols, innerRows int, title string, scrollOffset int) {
	bc := r.borderChars
	totalWidth := innerCols + 2

	// Top border
	r.output.WriteString(fmt.Sprintf("\033[%d;%dH", y+1, x+1))
	r.output.WriteString("\033[0m") // Reset attributes

	r.output.WriteRune(bc.topLeft)

	// Title in top border
	if title != "" && len(title) < innerCols-4 {
		padding := (innerCols - len(title) - 2) / 2
		for i := 0; i < padding; i++ {
			r.output.WriteRune(bc.horizontal)
		}
		r.output.WriteRune(bc.titleRight)
		r.output.WriteString(" ")
		r.output.WriteString(title)
		r.output.WriteString(" ")
		r.output.WriteRune(bc.titleLeft)
		remaining := innerCols - padding - len(title) - 4
		for i := 0; i < remaining; i++ {
			r.output.WriteRune(bc.horizontal)
		}
	} else {
		for i := 0; i < innerCols; i++ {
			r.output.WriteRune(bc.horizontal)
		}
	}
	r.output.WriteRune(bc.topRight)

	// Side borders
	for row := 0; row < innerRows; row++ {
		// Left border
		r.output.WriteString(fmt.Sprintf("\033[%d;%dH", y+row+2, x+1))
		r.output.WriteRune(bc.vertical)

		// Right border with optional scrollbar
		r.output.WriteString(fmt.Sprintf("\033[%d;%dH", y+row+2, x+totalWidth))
		if scrollOffset > 0 {
			// Show scrollbar indicator
			maxScroll := r.term.buffer.GetMaxScrollOffset()
			if maxScroll > 0 {
				scrollPos := float64(maxScroll-scrollOffset) / float64(maxScroll)
				thumbPos := int(scrollPos * float64(innerRows-1))
				if row == thumbPos {
					r.output.WriteString("\033[7m") // Reverse video
					r.output.WriteRune(bc.vertical)
					r.output.WriteString("\033[27m") // Normal video
				} else {
					r.output.WriteRune(bc.vertical)
				}
			} else {
				r.output.WriteRune(bc.vertical)
			}
		} else {
			r.output.WriteRune(bc.vertical)
		}
	}

	// Bottom border
	r.output.WriteString(fmt.Sprintf("\033[%d;%dH", y+innerRows+2, x+1))
	r.output.WriteRune(bc.bottomLeft)
	for i := 0; i < innerCols; i++ {
		r.output.WriteRune(bc.horizontal)
	}
	r.output.WriteRune(bc.bottomRight)
}

// renderStatusBar draws the status bar at the bottom
func (r *Renderer) renderStatusBar(x, y, width int, scrollOffset int) {
	r.output.WriteString(fmt.Sprintf("\033[%d;%dH", y+1, x+1))

	// Status bar style: reversed colors
	r.output.WriteString("\033[7m")

	cols, rows := r.term.buffer.GetSize()
	cursorX, cursorY := r.term.buffer.GetCursor()

	// Build status text
	var status string
	if scrollOffset > 0 {
		maxScroll := r.term.buffer.GetMaxScrollOffset()
		percent := 100 - (scrollOffset * 100 / maxScroll)
		status = fmt.Sprintf(" [%d%%] Lines: %d | Cursor: %d,%d | Size: %dx%d ",
			percent, r.term.buffer.GetScrollbackSize(), cursorX+1, cursorY+1, cols, rows)
	} else {
		status = fmt.Sprintf(" Lines: %d | Cursor: %d,%d | Size: %dx%d ",
			r.term.buffer.GetScrollbackSize(), cursorX+1, cursorY+1, cols, rows)
	}

	// Pad to full width
	if len(status) < width {
		status = status + strings.Repeat(" ", width-len(status))
	} else if len(status) > width {
		status = status[:width]
	}

	r.output.WriteString(status)
	r.output.WriteString("\033[27m") // End reverse video
}

// ForceFullRedraw clears the cached state and forces a complete redraw
func (r *Renderer) ForceFullRedraw() {
	r.mu.Lock()
	r.lastCells = nil
	r.renderNeeded = true
	r.mu.Unlock()
}
