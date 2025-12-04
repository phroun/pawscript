package purfectermgtk

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
	"fmt"
	"runtime"
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

	// Extract modifier states (cast ModifierType to uint for bitwise ops)
	hasShift := state&uint(gdk.SHIFT_MASK) != 0
	hasCtrl := state&uint(gdk.CONTROL_MASK) != 0
	hasAlt := state&uint(gdk.MOD1_MASK) != 0  // Alt key
	hasMeta := state&uint(gdk.META_MASK) != 0 // Meta/Command key
	hasSuper := state&uint(gdk.SUPER_MASK) != 0

	// Ignore modifier-only key presses (they don't produce terminal output)
	if isModifierKey(keyval) {
		return false
	}

	// Also check hardware keycode for Wine/Windows modifier keys
	hwcode := key.HardwareKeyCode()
	if isModifierKeycode(hwcode) {
		return false
	}

	// Handle clipboard operations (Ctrl+C with selection, Ctrl+V)
	// Note: Ctrl+A is NOT handled here - it passes through to the terminal
	// for programs that use it (e.g., readline beginning-of-line)
	if hasCtrl && !hasAlt && !hasMeta {
		switch keyval {
		case gdk.KEY_c, gdk.KEY_C:
			if w.buffer.HasSelection() {
				text := w.buffer.GetSelectedText()
				if w.clipboard != nil {
					w.clipboard.SetText(text)
				}
				return true
			}
			// Ctrl+C without selection falls through to send interrupt
		case gdk.KEY_v, gdk.KEY_V:
			if w.clipboard != nil {
				text, err := w.clipboard.WaitForText()
				if err == nil && onInput != nil {
					onInput([]byte(text))
				}
			}
			return true
		}
	}

	if onInput == nil {
		return false
	}

	// Calculate xterm-style modifier parameter
	// mod = 1 + (shift?1:0) + (alt?2:0) + (ctrl?4:0) + (meta?8:0)
	mod := 1
	if hasShift {
		mod += 1
	}
	if hasAlt {
		mod += 2
	}
	if hasCtrl {
		mod += 4
	}
	if hasMeta || hasSuper {
		mod += 8
	}
	hasModifiers := mod > 1

	var data []byte

	// Handle special keys with potential modifiers
	switch keyval {
	case gdk.KEY_Return, gdk.KEY_KP_Enter:
		if hasModifiers {
			data = modifiedSpecialKey(mod, 13, 0) // CSI 13 ; mod u (kitty protocol)
		} else {
			data = []byte{'\r'}
		}
	case gdk.KEY_BackSpace:
		if hasCtrl {
			data = []byte{0x08} // Ctrl+Backspace = BS
		} else if hasAlt {
			data = []byte{0x1b, 0x7f} // Alt+Backspace = ESC DEL
		} else {
			data = []byte{0x7f}
		}
	case gdk.KEY_Tab:
		if hasShift {
			data = []byte{0x1b, '[', 'Z'} // Shift+Tab = CSI Z (backtab)
		} else if hasCtrl {
			data = []byte{'\t'} // Ctrl+Tab (some apps use this)
		} else {
			data = []byte{'\t'}
		}
	case gdk.KEY_ISO_Left_Tab: // GTK sends this for Shift+Tab on many systems
		data = []byte{0x1b, '[', 'Z'} // Shift+Tab = CSI Z (backtab)
	case gdk.KEY_Escape:
		if hasAlt {
			data = []byte{0x1b, 0x1b} // Alt+Escape
		} else {
			data = []byte{0x1b}
		}

	// Arrow keys
	case gdk.KEY_Up:
		data = cursorKey('A', mod, hasModifiers)
	case gdk.KEY_Down:
		data = cursorKey('B', mod, hasModifiers)
	case gdk.KEY_Right:
		data = cursorKey('C', mod, hasModifiers)
	case gdk.KEY_Left:
		data = cursorKey('D', mod, hasModifiers)

	// Navigation keys
	case gdk.KEY_Home:
		data = cursorKey('H', mod, hasModifiers)
	case gdk.KEY_End:
		data = cursorKey('F', mod, hasModifiers)
	case gdk.KEY_Page_Up:
		data = tildeKey(5, mod, hasModifiers)
	case gdk.KEY_Page_Down:
		data = tildeKey(6, mod, hasModifiers)
	case gdk.KEY_Insert:
		data = tildeKey(2, mod, hasModifiers)
	case gdk.KEY_Delete:
		data = tildeKey(3, mod, hasModifiers)

	// Function keys F1-F4 (use SS3 format without modifiers, CSI format with)
	case gdk.KEY_F1:
		data = functionKey(1, 'P', mod, hasModifiers)
	case gdk.KEY_F2:
		data = functionKey(2, 'Q', mod, hasModifiers)
	case gdk.KEY_F3:
		data = functionKey(3, 'R', mod, hasModifiers)
	case gdk.KEY_F4:
		data = functionKey(4, 'S', mod, hasModifiers)

	// Function keys F5-F12 (use tilde format)
	case gdk.KEY_F5:
		data = tildeKey(15, mod, hasModifiers)
	case gdk.KEY_F6:
		data = tildeKey(17, mod, hasModifiers)
	case gdk.KEY_F7:
		data = tildeKey(18, mod, hasModifiers)
	case gdk.KEY_F8:
		data = tildeKey(19, mod, hasModifiers)
	case gdk.KEY_F9:
		data = tildeKey(20, mod, hasModifiers)
	case gdk.KEY_F10:
		data = tildeKey(21, mod, hasModifiers)
	case gdk.KEY_F11:
		data = tildeKey(23, mod, hasModifiers)
	case gdk.KEY_F12:
		data = tildeKey(24, mod, hasModifiers)

	// Keypad keys
	case gdk.KEY_KP_Up:
		data = cursorKey('A', mod, hasModifiers)
	case gdk.KEY_KP_Down:
		data = cursorKey('B', mod, hasModifiers)
	case gdk.KEY_KP_Right:
		data = cursorKey('C', mod, hasModifiers)
	case gdk.KEY_KP_Left:
		data = cursorKey('D', mod, hasModifiers)
	case gdk.KEY_KP_Home:
		data = cursorKey('H', mod, hasModifiers)
	case gdk.KEY_KP_End:
		data = cursorKey('F', mod, hasModifiers)
	case gdk.KEY_KP_Page_Up:
		data = tildeKey(5, mod, hasModifiers)
	case gdk.KEY_KP_Page_Down:
		data = tildeKey(6, mod, hasModifiers)
	case gdk.KEY_KP_Insert:
		data = tildeKey(2, mod, hasModifiers)
	case gdk.KEY_KP_Delete:
		data = tildeKey(3, mod, hasModifiers)

	default:
		// Regular character handling
		data = w.handleRegularKey(keyval, key, hasShift, hasCtrl, hasAlt, hasMeta, hasSuper)
	}

	// Final fallback: check hardware keycodes for special keys (Wine/Windows)
	if len(data) == 0 {
		hwcode := key.HardwareKeyCode()
		data = hardwareKeycodeToSpecialWithMod(hwcode, mod, hasModifiers)

		// If still no data, try regular character from hardware keycode
		if len(data) == 0 {
			if ch := hardwareKeycodeToChar(hwcode, hasShift); ch != 0 {
				data = w.processCharWithModifiers(ch, hasShift, hasCtrl, hasAlt, hasMeta, hasSuper)
			}
		}
	}

	if len(data) > 0 {
		onInput(data)
		return true
	}

	return false
}

// handleRegularKey processes regular character keys with modifiers
func (w *Widget) handleRegularKey(keyval uint, key *gdk.EventKey, hasShift, hasCtrl, hasAlt, hasMeta, hasSuper bool) []byte {
	var ch byte
	var isChar bool

	// On macOS, Option key composes special Unicode characters (e.g., Option+R = ®)
	// We want to treat Option as Alt/Meta modifier instead, using the base key
	if runtime.GOOS == "darwin" && hasAlt {
		hwcode := key.HardwareKeyCode()
		if baseCh := macKeycodeToChar(hwcode, hasShift); baseCh != 0 {
			// Apply Ctrl transformation if needed (convert letter to control char)
			if hasCtrl {
				if baseCh >= 'a' && baseCh <= 'z' {
					baseCh = baseCh - 'a' + 1
				} else if baseCh >= 'A' && baseCh <= 'Z' {
					baseCh = baseCh - 'A' + 1
				}
			}

			// Check if the result is a named key that should use kitty protocol
			var keycode int
			switch baseCh {
			case 0x0D: // CR = Enter (from Ctrl+M)
				keycode = 13
			case 0x09: // HT = Tab (from Ctrl+I)
				keycode = 9
			case 0x08: // BS = Backspace (from Ctrl+H)
				keycode = 127
			case 0x7F: // DEL
				keycode = 127
			case 0x1B: // ESC
				keycode = 27
			}

			if keycode != 0 {
				// Use kitty protocol: CSI keycode ; mod u
				// mod = 1 + (shift?1:0) + (alt?2:0) + (ctrl?4:0) + (meta?8:0)
				// Ctrl is consumed by letter->control_char, so not included
				mod := 1
				if hasShift {
					mod += 1
				}
				mod += 2 // Alt (Option) is always pressed in this branch
				if hasMeta || hasSuper {
					mod += 8
				}
				return []byte(fmt.Sprintf("\x1b[%d;%du", keycode, mod))
			}

			// Send ESC + base character for Alt+key
			return []byte{0x1b, baseCh}
		}
	}

	// Try to get character from keyval
	if keyval >= 0x20 && keyval < 256 {
		ch = byte(keyval)
		isChar = true
	} else if keyval >= 0x20 {
		// Unicode character - only handle if no special modifiers
		if r := gdk.KeyvalToUnicode(keyval); r != 0 && r < 128 {
			ch = byte(r)
			isChar = true
		} else if r != 0 {
			// Full unicode - send as UTF-8, with ESC prefix if Alt
			if hasAlt && !hasCtrl {
				return append([]byte{0x1b}, []byte(string(r))...)
			}
			return []byte(string(r))
		}
	}

	if !isChar {
		return nil
	}

	return w.processCharWithModifiers(ch, hasShift, hasCtrl, hasAlt, hasMeta, hasSuper)
}

// processCharWithModifiers applies modifier transformations to a character
func (w *Widget) processCharWithModifiers(ch byte, hasShift, hasCtrl, hasAlt, hasMeta, hasSuper bool) []byte {
	// Ctrl+letter produces control character (1-26)
	if hasCtrl && ch >= 'a' && ch <= 'z' {
		ch = ch - 'a' + 1
	} else if hasCtrl && ch >= 'A' && ch <= 'Z' {
		ch = ch - 'A' + 1
	} else if hasCtrl {
		// Other Ctrl combinations
		switch ch {
		case '@':
			ch = 0 // Ctrl+@ = NUL
		case '[':
			ch = 0x1b // Ctrl+[ = ESC
		case '\\':
			ch = 0x1c // Ctrl+\ = FS
		case ']':
			ch = 0x1d // Ctrl+] = GS
		case '^':
			ch = 0x1e // Ctrl+^ = RS
		case '_':
			ch = 0x1f // Ctrl+_ = US
		case '?':
			ch = 0x7f // Ctrl+? = DEL
		case ' ':
			ch = 0 // Ctrl+Space = NUL
		}
	}

	// Check if the control char is a "named key" that should use kitty protocol
	// when combined with other modifiers (Alt/Meta/Super)
	if hasAlt || hasMeta || hasSuper {
		// Map control chars to their keycode for kitty protocol
		var keycode int
		switch ch {
		case 0x0D: // CR = Enter (from Ctrl+M)
			keycode = 13
		case 0x09: // HT = Tab (from Ctrl+I)
			keycode = 9
		case 0x08: // BS = Backspace (from Ctrl+H)
			keycode = 127 // Use DEL keycode for backspace
		case 0x7F: // DEL
			keycode = 127
		case 0x1B: // ESC
			keycode = 27
		}

		if keycode != 0 {
			// Use kitty protocol: CSI keycode ; mod u
			// Compute modifier: 1 + (shift?1:0) + (alt?2:0) + (ctrl?4:0) + (meta?8:0)
			// Note: Ctrl is NOT included since it was consumed to produce the control char
			mod := 1
			if hasShift {
				mod += 1
			}
			if hasAlt {
				mod += 2
			}
			if hasMeta || hasSuper {
				mod += 8
			}
			return []byte(fmt.Sprintf("\x1b[%d;%du", keycode, mod))
		}

		// For other control chars, use ESC prefix
		return []byte{0x1b, ch}
	}

	return []byte{ch}
}

// cursorKey generates escape sequence for cursor keys (arrows, home, end)
// Without modifiers: ESC [ <key>
// With modifiers: ESC [ 1 ; <mod> <key>
func cursorKey(key byte, mod int, hasModifiers bool) []byte {
	if hasModifiers {
		return []byte(fmt.Sprintf("\x1b[1;%d%c", mod, key))
	}
	return []byte{0x1b, '[', key}
}

// tildeKey generates escape sequence for tilde-style keys (PgUp, PgDn, Insert, Delete, F5-F12)
// Without modifiers: ESC [ <num> ~
// With modifiers: ESC [ <num> ; <mod> ~
func tildeKey(num int, mod int, hasModifiers bool) []byte {
	numStr := []byte(fmt.Sprintf("%d", num))
	if hasModifiers {
		modStr := []byte(fmt.Sprintf(";%d", mod))
		result := append([]byte{0x1b, '['}, numStr...)
		result = append(result, modStr...)
		result = append(result, '~')
		return result
	}
	result := append([]byte{0x1b, '['}, numStr...)
	result = append(result, '~')
	return result
}

// functionKey generates escape sequence for F1-F4
// Without modifiers: ESC O <key> (SS3 format)
// With modifiers: ESC [ 1 ; <mod> <key> (CSI format)
func functionKey(num int, key byte, mod int, hasModifiers bool) []byte {
	if hasModifiers {
		return []byte(fmt.Sprintf("\x1b[1;%d%c", mod, key))
	}
	return []byte{0x1b, 'O', key}
}

// modifiedSpecialKey generates CSI u format for special keys with modifiers (kitty protocol style)
func modifiedSpecialKey(mod int, keycode int, suffix byte) []byte {
	if suffix != 0 {
		return []byte(fmt.Sprintf("\x1b[%d;%d%c", keycode, mod, suffix))
	}
	return []byte(fmt.Sprintf("\x1b[%d;%du", keycode, mod))
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

// hardwareKeycodeToSpecialWithMod maps Windows Virtual Key codes to special key sequences with modifier support.
// This is used as a fallback when GDK can't translate keypresses (Wine/Windows).
// On Windows/Wine, HardwareKeyCode() returns Windows VK codes, not X11 keycodes.
func hardwareKeycodeToSpecialWithMod(hwcode uint16, mod int, hasModifiers bool) []byte {
	// Windows Virtual Key code mappings
	switch hwcode {
	case 13: // VK_RETURN
		return []byte{'\r'}
	case 8: // VK_BACK
		if hasModifiers && mod >= 5 { // Ctrl
			return []byte{0x08}
		} else if hasModifiers && mod >= 3 { // Alt
			return []byte{0x1b, 0x7f}
		}
		return []byte{0x7f}
	case 9: // VK_TAB
		if hasModifiers && (mod == 2 || mod == 3) { // Shift
			return []byte{0x1b, '[', 'Z'}
		}
		return []byte{'\t'}
	case 27: // VK_ESCAPE
		return []byte{0x1b}

	// Arrow keys
	case 38: // VK_UP
		return cursorKey('A', mod, hasModifiers)
	case 40: // VK_DOWN
		return cursorKey('B', mod, hasModifiers)
	case 39: // VK_RIGHT
		return cursorKey('C', mod, hasModifiers)
	case 37: // VK_LEFT
		return cursorKey('D', mod, hasModifiers)

	// Navigation keys
	case 36: // VK_HOME
		return cursorKey('H', mod, hasModifiers)
	case 35: // VK_END
		return cursorKey('F', mod, hasModifiers)
	case 33: // VK_PRIOR (Page Up)
		return tildeKey(5, mod, hasModifiers)
	case 34: // VK_NEXT (Page Down)
		return tildeKey(6, mod, hasModifiers)
	case 45: // VK_INSERT
		return tildeKey(2, mod, hasModifiers)
	case 46: // VK_DELETE
		return tildeKey(3, mod, hasModifiers)

	// Function keys F1-F4
	case 112: // VK_F1
		return functionKey(1, 'P', mod, hasModifiers)
	case 113: // VK_F2
		return functionKey(2, 'Q', mod, hasModifiers)
	case 114: // VK_F3
		return functionKey(3, 'R', mod, hasModifiers)
	case 115: // VK_F4
		return functionKey(4, 'S', mod, hasModifiers)

	// Function keys F5-F12
	case 116: // VK_F5
		return tildeKey(15, mod, hasModifiers)
	case 117: // VK_F6
		return tildeKey(17, mod, hasModifiers)
	case 118: // VK_F7
		return tildeKey(18, mod, hasModifiers)
	case 119: // VK_F8
		return tildeKey(19, mod, hasModifiers)
	case 120: // VK_F9
		return tildeKey(20, mod, hasModifiers)
	case 121: // VK_F10
		return tildeKey(21, mod, hasModifiers)
	case 122: // VK_F11
		return tildeKey(23, mod, hasModifiers)
	case 123: // VK_F12
		return tildeKey(24, mod, hasModifiers)
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

// macKeycodeToChar converts macOS hardware keycodes to ASCII characters
// On macOS, Option key produces composed characters (like ® for Option+R)
// We use hardware keycodes to get the base character for Alt/Meta sequences
func macKeycodeToChar(hwcode uint16, shift bool) byte {
	// macOS keycode to character mapping (US keyboard layout)
	// Letters - macOS keycodes are not sequential like Windows VK codes
	letterKeys := map[uint16]byte{
		0: 'a', 1: 's', 2: 'd', 3: 'f', 4: 'h', 5: 'g', 6: 'z', 7: 'x',
		8: 'c', 9: 'v', 11: 'b', 12: 'q', 13: 'w', 14: 'e', 15: 'r',
		16: 'y', 17: 't', 31: 'o', 32: 'u', 34: 'i', 35: 'p', 37: 'l',
		38: 'j', 40: 'k', 45: 'n', 46: 'm',
	}

	if ch, ok := letterKeys[hwcode]; ok {
		if shift {
			return ch - 32 // Convert to uppercase
		}
		return ch
	}

	// Number row
	numberKeys := map[uint16]struct {
		normal byte
		shift  byte
	}{
		18: {'1', '!'}, 19: {'2', '@'}, 20: {'3', '#'}, 21: {'4', '$'},
		23: {'5', '%'}, 22: {'6', '^'}, 26: {'7', '&'}, 28: {'8', '*'},
		25: {'9', '('}, 29: {'0', ')'},
	}

	if mapping, ok := numberKeys[hwcode]; ok {
		if shift {
			return mapping.shift
		}
		return mapping.normal
	}

	// Symbol keys
	symbolKeys := map[uint16]struct {
		normal byte
		shift  byte
	}{
		24: {'=', '+'}, 27: {'-', '_'}, 30: {']', '}'}, 33: {'[', '{'},
		39: {'\'', '"'}, 41: {';', ':'}, 42: {'\\', '|'}, 43: {',', '<'},
		44: {'/', '?'}, 47: {'.', '>'}, 50: {'`', '~'},
	}

	if mapping, ok := symbolKeys[hwcode]; ok {
		if shift {
			return mapping.shift
		}
		return mapping.normal
	}

	// Space
	if hwcode == 49 {
		return ' '
	}

	return 0
}

// isModifierKey returns true if the GDK keyval is a modifier key
// Modifier keys alone don't produce terminal output
func isModifierKey(keyval uint) bool {
	switch keyval {
	case gdk.KEY_Shift_L, gdk.KEY_Shift_R,
		gdk.KEY_Control_L, gdk.KEY_Control_R,
		gdk.KEY_Alt_L, gdk.KEY_Alt_R,
		gdk.KEY_Meta_L, gdk.KEY_Meta_R,
		gdk.KEY_Super_L, gdk.KEY_Super_R,
		gdk.KEY_Hyper_L, gdk.KEY_Hyper_R,
		gdk.KEY_Caps_Lock, gdk.KEY_Num_Lock, gdk.KEY_Scroll_Lock:
		return true
	}
	return false
}

// isModifierKeycode returns true if the hardware keycode is a Windows VK modifier key
// This catches modifier keys on Wine/Windows when GDK keyval detection fails
func isModifierKeycode(hwcode uint16) bool {
	switch hwcode {
	case 16,         // VK_SHIFT
		17,          // VK_CONTROL
		18,          // VK_MENU (Alt)
		20,          // VK_CAPITAL (Caps Lock)
		91, 92,      // VK_LWIN, VK_RWIN (Windows/Command keys)
		144,         // VK_NUMLOCK
		145,         // VK_SCROLL
		160, 161,    // VK_LSHIFT, VK_RSHIFT
		162, 163,    // VK_LCONTROL, VK_RCONTROL
		164, 165:    // VK_LMENU, VK_RMENU (Left/Right Alt)
		return true
	}
	return false
}
