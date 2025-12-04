package gtkterm

/*
#cgo pkg-config: gtk+-3.0
#include <gtk/gtk.h>
#include <gdk/gdk.h>

// Helper to get event coordinates
static void get_event_coords(GdkEvent *ev, double *x, double *y) {
    gdk_event_get_coords(ev, x, y);
}
*/
import "C"

import (
	"sync"
	"unsafe"

	"github.com/gotk3/gotk3/cairo"
	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

// Left padding for terminal content (pixels)
const terminalLeftPadding = 8

// Widget is a GTK terminal emulator widget
type Widget struct {
	mu sync.Mutex

	// GTK widgets
	drawingArea *gtk.DrawingArea
	scrollbar   *gtk.Scrollbar
	box         *gtk.Box

	// Terminal state
	buffer *Buffer
	parser *Parser

	// Font settings
	fontFamily string
	fontSize   int
	charWidth  int
	charHeight int
	charAscent int

	// Color scheme
	scheme ColorScheme

	// Selection state
	selecting      bool
	selectStartX   int
	selectStartY   int
	mouseDown      bool
	mouseDownX     int
	mouseDownY     int
	selectionMoved bool // True if mouse moved since button press

	// Cursor blink
	cursorBlinkOn  bool
	blinkTimerID   glib.SourceHandle
	blinkTickCount int // Counter for variable blink rates

	// Focus state
	hasFocus bool

	// Callback when data should be written to PTY
	onInput func([]byte)

	// Clipboard
	clipboard *gtk.Clipboard
}

// NewWidget creates a new terminal widget with the specified dimensions
func NewWidget(cols, rows, scrollbackSize int) (*Widget, error) {
	w := &Widget{
		fontFamily:    "Menlo",
		fontSize:      14,
		charWidth:     10, // Will be calculated properly
		charHeight:    20,
		charAscent:    16,
		scheme:        DefaultColorScheme(),
		cursorBlinkOn: true,
	}

	// Create buffer and parser
	w.buffer = NewBuffer(cols, rows, scrollbackSize)
	w.parser = NewParser(w.buffer)

	// Set up dirty callback to trigger redraws
	w.buffer.SetDirtyCallback(func() {
		glib.IdleAdd(func() {
			if w.drawingArea != nil {
				w.drawingArea.QueueDraw()
			}
		})
	})

	// Create GTK widgets
	var err error

	// Main container
	w.box, err = gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	if err != nil {
		return nil, err
	}

	// Drawing area for terminal content
	w.drawingArea, err = gtk.DrawingAreaNew()
	if err != nil {
		return nil, err
	}

	// Enable events
	w.drawingArea.AddEvents(int(gdk.BUTTON_PRESS_MASK | gdk.BUTTON_RELEASE_MASK |
		gdk.POINTER_MOTION_MASK | gdk.SCROLL_MASK | gdk.KEY_PRESS_MASK))
	w.drawingArea.SetCanFocus(true)

	// Connect signals
	w.drawingArea.Connect("draw", w.onDraw)
	w.drawingArea.Connect("button-press-event", w.onButtonPress)
	w.drawingArea.Connect("button-release-event", w.onButtonRelease)
	w.drawingArea.Connect("motion-notify-event", w.onMotionNotify)
	w.drawingArea.Connect("scroll-event", w.onScroll)
	w.drawingArea.Connect("key-press-event", w.onKeyPress)
	w.drawingArea.Connect("configure-event", w.onConfigure)
	w.drawingArea.Connect("focus-in-event", w.onFocusIn)
	w.drawingArea.Connect("focus-out-event", w.onFocusOut)

	// Create scrollbar
	adjustment, _ := gtk.AdjustmentNew(0, 0, 100, 1, 10, 10)
	w.scrollbar, err = gtk.ScrollbarNew(gtk.ORIENTATION_VERTICAL, adjustment)
	if err != nil {
		return nil, err
	}
	w.scrollbar.Connect("value-changed", w.onScrollbarChanged)

	// Pack widgets
	w.box.PackStart(w.drawingArea, true, true, 0)
	w.box.PackStart(w.scrollbar, false, false, 0)

	// Get clipboard
	w.clipboard, _ = gtk.ClipboardGet(gdk.SELECTION_CLIPBOARD)

	// Set initial size based on character dimensions (plus left padding)
	w.updateFontMetrics()
	w.drawingArea.SetSizeRequest(cols*w.charWidth+terminalLeftPadding, rows*w.charHeight)

	// Start cursor blink timer (265ms base interval)
	// cursorBlink: 0=no blink, 1=slow blink (~530ms), 2=fast blink (~265ms)
	w.blinkTimerID = glib.TimeoutAdd(265, func() bool {
		_, cursorBlink := w.buffer.GetCursorStyle()
		if cursorBlink > 0 && w.hasFocus {
			w.blinkTickCount++
			// Fast blink (2) toggles every tick, slow blink (1) every 2 ticks
			ticksNeeded := 2
			if cursorBlink >= 2 {
				ticksNeeded = 1
			}
			if w.blinkTickCount >= ticksNeeded {
				w.blinkTickCount = 0
				w.cursorBlinkOn = !w.cursorBlinkOn
				w.drawingArea.QueueDraw()
			}
		} else {
			// Keep cursor visible when not blinking or unfocused
			w.blinkTickCount = 0
			if !w.cursorBlinkOn {
				w.cursorBlinkOn = true
				w.drawingArea.QueueDraw()
			}
		}
		return true // Keep timer running
	})

	return w, nil
}

// Box returns the container widget
func (w *Widget) Box() *gtk.Box {
	return w.box
}

// DrawingArea returns the drawing area widget
func (w *Widget) DrawingArea() *gtk.DrawingArea {
	return w.drawingArea
}

// SetFont sets the terminal font
func (w *Widget) SetFont(family string, size int) {
	w.mu.Lock()
	w.fontFamily = family
	w.fontSize = size
	w.mu.Unlock()
	w.updateFontMetrics()
	w.drawingArea.QueueDraw()
}

// SetColorScheme sets the color scheme
func (w *Widget) SetColorScheme(scheme ColorScheme) {
	w.mu.Lock()
	w.scheme = scheme
	w.mu.Unlock()
	w.drawingArea.QueueDraw()
}

// SetInputCallback sets the callback for handling input
func (w *Widget) SetInputCallback(fn func([]byte)) {
	w.mu.Lock()
	w.onInput = fn
	w.mu.Unlock()
}

// Feed writes data to the terminal (for local echo or PTY output)
func (w *Widget) Feed(data []byte) {
	w.parser.Parse(data)
}

// FeedString writes a string to the terminal
func (w *Widget) FeedString(data string) {
	w.parser.ParseString(data)
}

// Clear clears the terminal screen
func (w *Widget) Clear() {
	w.buffer.ClearScreen()
	w.buffer.SetCursor(0, 0)
}

// Buffer returns the underlying buffer
func (w *Widget) Buffer() *Buffer {
	return w.buffer
}

func (w *Widget) updateFontMetrics() {
	// Use approximate metrics based on font size
	w.charWidth = w.fontSize * 6 / 10  // Approximate for monospace
	w.charHeight = w.fontSize * 12 / 10
	w.charAscent = w.fontSize
	if w.charWidth < 1 {
		w.charWidth = 10
	}
	if w.charHeight < 1 {
		w.charHeight = 20
	}
}

func (w *Widget) onDraw(da *gtk.DrawingArea, cr *cairo.Context) bool {
	w.mu.Lock()
	scheme := w.scheme
	fontFamily := w.fontFamily
	fontSize := w.fontSize
	charWidth := w.charWidth
	charHeight := w.charHeight
	charAscent := w.charAscent
	w.mu.Unlock()

	cols, rows := w.buffer.GetSize()
	cursorX, cursorY := w.buffer.GetCursor()
	cursorVisible := w.buffer.IsCursorVisible()
	cursorShape, _ := w.buffer.GetCursorStyle() // 0=block, 1=underline, 2=bar
	scrollOffset := w.buffer.GetScrollOffset()

	// Hide cursor when scrolled back
	if scrollOffset > 0 {
		cursorVisible = false
	}

	// Draw background (including left padding area)
	cr.SetSourceRGB(
		float64(scheme.Background.R)/255.0,
		float64(scheme.Background.G)/255.0,
		float64(scheme.Background.B)/255.0)
	cr.Rectangle(0, 0, float64(cols*charWidth+terminalLeftPadding), float64(rows*charHeight))
	cr.Fill()

	// Set up font
	cr.SelectFontFace(fontFamily, cairo.FONT_SLANT_NORMAL, cairo.FONT_WEIGHT_NORMAL)
	cr.SetFontSize(float64(fontSize))

	// Draw each cell (use GetVisibleCell to account for scroll offset)
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			cell := w.buffer.GetVisibleCell(x, y)

			// Determine colors
			fg := cell.Foreground
			bg := cell.Background
			if fg.Default {
				fg = scheme.Foreground
			}
			if bg.Default {
				bg = scheme.Background
			}

			// Handle selection highlighting
			if w.buffer.IsInSelection(x, y) {
				bg = scheme.Selection
			}

			// Handle cursor - only swap colors for solid block cursor when focused
			isCursor := cursorVisible && x == cursorX && y == cursorY && w.cursorBlinkOn
			if isCursor && w.hasFocus && cursorShape == 0 {
				// Swap colors for solid block cursor when focused
				fg, bg = bg, fg
			}

			// Draw cell background if different from terminal background
			if bg != scheme.Background {
				cr.SetSourceRGB(
					float64(bg.R)/255.0,
					float64(bg.G)/255.0,
					float64(bg.B)/255.0)
				cr.Rectangle(
					float64(x*charWidth+terminalLeftPadding),
					float64(y*charHeight),
					float64(charWidth),
					float64(charHeight))
				cr.Fill()
			}

			// Draw character
			if cell.Char != ' ' && cell.Char != 0 {
				cr.SetSourceRGB(
					float64(fg.R)/255.0,
					float64(fg.G)/255.0,
					float64(fg.B)/255.0)

				// Set font weight
				if cell.Bold {
					cr.SelectFontFace(fontFamily, cairo.FONT_SLANT_NORMAL, cairo.FONT_WEIGHT_BOLD)
				}

				cr.MoveTo(float64(x*charWidth+terminalLeftPadding), float64(y*charHeight+charAscent))
				cr.ShowText(string(cell.Char))

				// Reset font weight
				if cell.Bold {
					cr.SelectFontFace(fontFamily, cairo.FONT_SLANT_NORMAL, cairo.FONT_WEIGHT_NORMAL)
				}
			}

			// Draw underline if needed
			if cell.Underline {
				cr.SetSourceRGB(
					float64(fg.R)/255.0,
					float64(fg.G)/255.0,
					float64(fg.B)/255.0)
				cr.Rectangle(
					float64(x*charWidth+terminalLeftPadding),
					float64((y+1)*charHeight-1),
					float64(charWidth),
					1)
				cr.Fill()
			}

			// Draw cursor based on shape (0=block, 1=underline, 2=bar)
			if isCursor {
				cr.SetSourceRGB(
					float64(scheme.Cursor.R)/255.0,
					float64(scheme.Cursor.G)/255.0,
					float64(scheme.Cursor.B)/255.0)

				switch cursorShape {
				case 0: // Block cursor
					if !w.hasFocus {
						// Outline block when unfocused
						cr.SetLineWidth(1.0)
						cr.Rectangle(
							float64(x*charWidth+terminalLeftPadding)+0.5,
							float64(y*charHeight)+0.5,
							float64(charWidth)-1,
							float64(charHeight)-1)
						cr.Stroke()
					}
					// Focused block is handled by fg/bg swap above

				case 1: // Underline cursor (1/4 block height)
					thickness := float64(charHeight) / 4.0
					if !w.hasFocus {
						thickness = float64(charHeight) / 6.0 // Thinner when unfocused
					}
					cr.Rectangle(
						float64(x*charWidth+terminalLeftPadding),
						float64((y+1)*charHeight)-thickness,
						float64(charWidth),
						thickness)
					cr.Fill()

				case 2: // Bar (vertical line) cursor
					thickness := 2.0
					if !w.hasFocus {
						thickness = 1.0
					}
					cr.Rectangle(
						float64(x*charWidth+terminalLeftPadding),
						float64(y*charHeight),
						thickness,
						float64(charHeight))
					cr.Fill()
				}
			}
		}
	}

	w.buffer.ClearDirty()
	return true
}

func (w *Widget) screenToCell(screenX, screenY float64) (cellX, cellY int) {
	w.mu.Lock()
	charWidth := w.charWidth
	charHeight := w.charHeight
	w.mu.Unlock()

	// Account for left padding when converting screen coords to cell
	cellX = (int(screenX) - terminalLeftPadding) / charWidth
	cellY = int(screenY) / charHeight

	cols, rows := w.buffer.GetSize()
	if cellX < 0 {
		cellX = 0
	}
	if cellX >= cols {
		cellX = cols - 1
	}
	if cellY < 0 {
		cellY = 0
	}
	if cellY >= rows {
		cellY = rows - 1
	}
	return
}

func (w *Widget) onButtonPress(da *gtk.DrawingArea, ev *gdk.Event) bool {
	btn := gdk.EventButtonNewFromEvent(ev)
	x, y := btn.X(), btn.Y()
	button := btn.Button()

	if button == 1 { // Left button
		cellX, cellY := w.screenToCell(x, y)
		// Record press position but don't start selection yet
		w.mouseDown = true
		w.mouseDownX = cellX
		w.mouseDownY = cellY
		w.selectionMoved = false
		w.buffer.ClearSelection()
		da.GrabFocus()
	}
	return true
}

func (w *Widget) onButtonRelease(da *gtk.DrawingArea, ev *gdk.Event) bool {
	btn := gdk.EventButtonNewFromEvent(ev)
	button := btn.Button()

	if button == 1 {
		w.mouseDown = false
		if w.selecting {
			w.selecting = false
			w.buffer.EndSelection()
		}
	}
	return true
}

func (w *Widget) onMotionNotify(da *gtk.DrawingArea, ev *gdk.Event) bool {
	if !w.mouseDown {
		return false
	}

	// Use C helper to get coordinates from the event
	var x, y C.double
	C.get_event_coords((*C.GdkEvent)(unsafe.Pointer(ev.Native())), &x, &y)
	cellX, cellY := w.screenToCell(float64(x), float64(y))

	// Only start selection once mouse has moved to a different cell
	if !w.selectionMoved {
		if cellX != w.mouseDownX || cellY != w.mouseDownY {
			// Start selection from original mouse-down position
			w.selectionMoved = true
			w.selecting = true
			w.selectStartX = w.mouseDownX
			w.selectStartY = w.mouseDownY
			w.buffer.StartSelection(w.mouseDownX, w.mouseDownY)
		} else {
			return true // Mouse still in same cell, don't select yet
		}
	}

	w.buffer.UpdateSelection(cellX, cellY)
	return true
}

func (w *Widget) onScroll(da *gtk.DrawingArea, ev *gdk.Event) bool {
	scroll := gdk.EventScrollNewFromEvent(ev)
	dir := scroll.Direction()

	offset := w.buffer.GetScrollOffset()
	scrollbackSize := w.buffer.GetScrollbackSize()

	switch dir {
	case gdk.SCROLL_UP:
		offset += 3
		if offset > scrollbackSize {
			offset = scrollbackSize
		}
	case gdk.SCROLL_DOWN:
		offset -= 3
		if offset < 0 {
			offset = 0
		}
	}

	w.buffer.SetScrollOffset(offset)
	w.updateScrollbar()
	return true
}

func (w *Widget) onKeyPress(da *gtk.DrawingArea, ev *gdk.Event) bool {
	key := gdk.EventKeyNewFromEvent(ev)
	keyval := key.KeyVal()
	state := key.State()

	w.mu.Lock()
	onInput := w.onInput
	w.mu.Unlock()

	// Handle Ctrl+C, Ctrl+V
	if state&gdk.CONTROL_MASK != 0 {
		switch keyval {
		case gdk.KEY_c, gdk.KEY_C:
			if w.buffer.HasSelection() {
				text := w.buffer.GetSelectedText()
				if w.clipboard != nil {
					w.clipboard.SetText(text)
				}
				return true
			}
			// Ctrl+C without selection - send interrupt
			if onInput != nil {
				onInput([]byte{0x03})
			}
			return true
		case gdk.KEY_v, gdk.KEY_V:
			if w.clipboard != nil {
				text, err := w.clipboard.WaitForText()
				if err == nil && onInput != nil {
					onInput([]byte(text))
				}
			}
			return true
		case gdk.KEY_a, gdk.KEY_A:
			w.buffer.SelectAll()
			return true
		}
	}

	if onInput == nil {
		return false
	}

	// Handle special keys
	var data []byte
	switch keyval {
	case gdk.KEY_Return, gdk.KEY_KP_Enter:
		data = []byte{'\r'}
	case gdk.KEY_BackSpace:
		data = []byte{0x7f}
	case gdk.KEY_Tab:
		data = []byte{'\t'}
	case gdk.KEY_Escape:
		data = []byte{0x1b}
	case gdk.KEY_Up:
		data = []byte{0x1b, '[', 'A'}
	case gdk.KEY_Down:
		data = []byte{0x1b, '[', 'B'}
	case gdk.KEY_Right:
		data = []byte{0x1b, '[', 'C'}
	case gdk.KEY_Left:
		data = []byte{0x1b, '[', 'D'}
	case gdk.KEY_Home:
		data = []byte{0x1b, '[', 'H'}
	case gdk.KEY_End:
		data = []byte{0x1b, '[', 'F'}
	case gdk.KEY_Page_Up:
		data = []byte{0x1b, '[', '5', '~'}
	case gdk.KEY_Page_Down:
		data = []byte{0x1b, '[', '6', '~'}
	case gdk.KEY_Insert:
		data = []byte{0x1b, '[', '2', '~'}
	case gdk.KEY_Delete:
		data = []byte{0x1b, '[', '3', '~'}
	case gdk.KEY_F1:
		data = []byte{0x1b, 'O', 'P'}
	case gdk.KEY_F2:
		data = []byte{0x1b, 'O', 'Q'}
	case gdk.KEY_F3:
		data = []byte{0x1b, 'O', 'R'}
	case gdk.KEY_F4:
		data = []byte{0x1b, 'O', 'S'}
	default:
		// Regular character - try keyval first
		if keyval >= 0x20 && keyval < 256 {
			ch := byte(keyval)
			if state&gdk.CONTROL_MASK != 0 && ch >= 'a' && ch <= 'z' {
				ch = ch - 'a' + 1 // Ctrl+A = 1, Ctrl+B = 2, etc.
			}
			data = []byte{ch}
		} else if keyval >= 0x20 {
			// Unicode character
			if r := gdk.KeyvalToUnicode(keyval); r != 0 {
				data = []byte(string(r))
			}
		}

		// Fallback: use hardware keycode when GDK translation fails (Wine/Windows)
		if len(data) == 0 {
			hwcode := key.HardwareKeyCode()
			if ch := hardwareKeycodeToChar(hwcode, state&uint(gdk.SHIFT_MASK) != 0); ch != 0 {
				if state&uint(gdk.CONTROL_MASK) != 0 && ch >= 'a' && ch <= 'z' {
					ch = ch - 'a' + 1
				}
				data = []byte{ch}
			}
		}
	}

	// Final fallback: check hardware keycodes for special keys (Wine/Windows)
	if len(data) == 0 {
		hwcode := key.HardwareKeyCode()
		data = hardwareKeycodeToSpecial(hwcode)
	}

	if len(data) > 0 {
		onInput(data)
		return true
	}

	return false
}

func (w *Widget) onConfigure(da *gtk.DrawingArea, ev *gdk.Event) bool {
	w.updateFontMetrics()

	// Recalculate terminal size based on widget size (minus left padding)
	alloc := da.GetAllocation()
	newCols := (alloc.GetWidth() - terminalLeftPadding) / w.charWidth
	newRows := alloc.GetHeight() / w.charHeight

	if newCols < 1 {
		newCols = 1
	}
	if newRows < 1 {
		newRows = 1
	}

	w.buffer.Resize(newCols, newRows)
	return false
}

func (w *Widget) onFocusIn(da *gtk.DrawingArea, ev *gdk.Event) bool {
	w.hasFocus = true
	w.cursorBlinkOn = true // Reset blink so cursor is immediately visible
	w.drawingArea.QueueDraw()
	return false
}

func (w *Widget) onFocusOut(da *gtk.DrawingArea, ev *gdk.Event) bool {
	w.hasFocus = false
	w.drawingArea.QueueDraw()
	return false
}

func (w *Widget) onScrollbarChanged(sb *gtk.Scrollbar) {
	adj := sb.GetAdjustment()
	val := int(adj.GetValue())
	scrollbackSize := w.buffer.GetScrollbackSize()
	// Invert - scrollbar at top means scrolled back
	w.buffer.SetScrollOffset(scrollbackSize - val)
}

func (w *Widget) updateScrollbar() {
	scrollbackSize := w.buffer.GetScrollbackSize()
	offset := w.buffer.GetScrollOffset()
	_, rows := w.buffer.GetSize()

	adj := w.scrollbar.GetAdjustment()
	adj.SetLower(0)
	adj.SetUpper(float64(scrollbackSize + rows))
	adj.SetPageSize(float64(rows))
	adj.SetValue(float64(scrollbackSize - offset))
}

// Resize resizes the terminal to the specified dimensions
func (w *Widget) Resize(cols, rows int) {
	w.buffer.Resize(cols, rows)
	w.updateScrollbar()
}

// GetSize returns the current terminal size in characters
func (w *Widget) GetSize() (cols, rows int) {
	return w.buffer.GetSize()
}

// GetSelectedText returns currently selected text
func (w *Widget) GetSelectedText() string {
	return w.buffer.GetSelectedText()
}

// CopySelection copies selected text to clipboard
func (w *Widget) CopySelection() {
	if w.clipboard != nil && w.buffer.HasSelection() {
		text := w.buffer.GetSelectedText()
		w.clipboard.SetText(text)
	}
}

// SelectAll selects all text in the terminal
func (w *Widget) SelectAll() {
	w.buffer.SelectAll()
}

// SetCursorVisible shows or hides the cursor
func (w *Widget) SetCursorVisible(visible bool) {
	w.buffer.SetCursorVisible(visible)
}

// hardwareKeycodeToSpecial maps Windows Virtual Key codes to special key sequences.
// This is used as a fallback when GDK can't translate keypresses (Wine/Windows).
// On Windows/Wine, HardwareKeyCode() returns Windows VK codes, not X11 keycodes.
func hardwareKeycodeToSpecial(hwcode uint16) []byte {
	// Windows Virtual Key code mappings
	switch hwcode {
	case 13: // VK_RETURN
		return []byte{'\r'}
	case 8: // VK_BACK
		return []byte{0x7f}
	case 9: // VK_TAB
		return []byte{'\t'}
	case 27: // VK_ESCAPE
		return []byte{0x1b}
	case 38: // VK_UP
		return []byte{0x1b, '[', 'A'}
	case 40: // VK_DOWN
		return []byte{0x1b, '[', 'B'}
	case 39: // VK_RIGHT
		return []byte{0x1b, '[', 'C'}
	case 37: // VK_LEFT
		return []byte{0x1b, '[', 'D'}
	case 36: // VK_HOME
		return []byte{0x1b, '[', 'H'}
	case 35: // VK_END
		return []byte{0x1b, '[', 'F'}
	case 33: // VK_PRIOR (Page Up)
		return []byte{0x1b, '[', '5', '~'}
	case 34: // VK_NEXT (Page Down)
		return []byte{0x1b, '[', '6', '~'}
	case 45: // VK_INSERT
		return []byte{0x1b, '[', '2', '~'}
	case 46: // VK_DELETE
		return []byte{0x1b, '[', '3', '~'}
	case 112: // VK_F1
		return []byte{0x1b, 'O', 'P'}
	case 113: // VK_F2
		return []byte{0x1b, 'O', 'Q'}
	case 114: // VK_F3
		return []byte{0x1b, 'O', 'R'}
	case 115: // VK_F4
		return []byte{0x1b, 'O', 'S'}
	case 116: // VK_F5
		return []byte{0x1b, '[', '1', '5', '~'}
	case 117: // VK_F6
		return []byte{0x1b, '[', '1', '7', '~'}
	case 118: // VK_F7
		return []byte{0x1b, '[', '1', '8', '~'}
	case 119: // VK_F8
		return []byte{0x1b, '[', '1', '9', '~'}
	case 120: // VK_F9
		return []byte{0x1b, '[', '2', '0', '~'}
	case 121: // VK_F10
		return []byte{0x1b, '[', '2', '1', '~'}
	case 122: // VK_F11
		return []byte{0x1b, '[', '2', '3', '~'}
	case 123: // VK_F12
		return []byte{0x1b, '[', '2', '4', '~'}
	}
	return nil
}

// hardwareKeycodeToChar maps Windows Virtual Key codes to ASCII characters.
// This is used as a fallback when GDK can't translate keypresses (Wine/Windows).
// Windows VK codes for letters are 65-90 (A-Z), numbers are 48-57 (0-9).
func hardwareKeycodeToChar(hwcode uint16, shift bool) byte {
	// Letters A-Z: VK codes 65-90
	if hwcode >= 65 && hwcode <= 90 {
		if shift {
			return byte(hwcode) // 'A'-'Z'
		}
		return byte(hwcode + 32) // 'a'-'z'
	}

	// Numbers 0-9: VK codes 48-57
	if hwcode >= 48 && hwcode <= 57 {
		if shift {
			// Shifted number row symbols
			symbols := []byte{')', '!', '@', '#', '$', '%', '^', '&', '*', '('}
			return symbols[hwcode-48]
		}
		return byte(hwcode) // '0'-'9'
	}

	// Space
	if hwcode == 32 { // VK_SPACE
		return ' '
	}

	// OEM keys (symbols) - US keyboard layout
	type keyMapping struct {
		normal byte
		shift  byte
	}
	oemKeys := map[uint16]keyMapping{
		186: {';', ':'}, // VK_OEM_1
		187: {'=', '+'}, // VK_OEM_PLUS
		188: {',', '<'}, // VK_OEM_COMMA
		189: {'-', '_'}, // VK_OEM_MINUS
		190: {'.', '>'}, // VK_OEM_PERIOD
		191: {'/', '?'}, // VK_OEM_2
		192: {'`', '~'}, // VK_OEM_3
		219: {'[', '{'}, // VK_OEM_4
		220: {'\\', '|'}, // VK_OEM_5
		221: {']', '}'}, // VK_OEM_6
		222: {'\'', '"'}, // VK_OEM_7
	}

	if mapping, ok := oemKeys[hwcode]; ok {
		if shift {
			return mapping.shift
		}
		return mapping.normal
	}

	return 0
}
