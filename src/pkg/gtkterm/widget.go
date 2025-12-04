package gtkterm

/*
#cgo pkg-config: gtk+-3.0
#include <gtk/gtk.h>
#include <gdk/gdk.h>

// Helper to set source color
static void set_source_rgb_helper(cairo_t *cr, double r, double g, double b) {
    cairo_set_source_rgb(cr, r, g, b);
}

// Helper to draw rectangle
static void draw_rect(cairo_t *cr, double x, double y, double w, double h) {
    cairo_rectangle(cr, x, y, w, h);
    cairo_fill(cr);
}

// Helper to show text
static void draw_text(cairo_t *cr, double x, double y, const char *text) {
    cairo_move_to(cr, x, y);
    cairo_show_text(cr, text);
}
*/
import "C"

import (
	"sync"
	"unsafe"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

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
	selecting    bool
	selectStartX int
	selectStartY int

	// Cursor blink
	cursorBlinkOn bool

	// Callback when data should be written to PTY
	onInput func([]byte)

	// Clipboard
	clipboard *gtk.Clipboard
}

// NewWidget creates a new terminal widget with the specified dimensions
func NewWidget(cols, rows, scrollbackSize int) (*Widget, error) {
	w := &Widget{
		fontFamily:    "Monospace",
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

	// Set initial size based on character dimensions
	w.updateFontMetrics()
	w.drawingArea.SetSizeRequest(cols*w.charWidth, rows*w.charHeight)

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
	// Use cairo to measure font
	// For now, use approximate metrics based on font size
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

func (w *Widget) onDraw(da *gtk.DrawingArea, cr *C.cairo_t) bool {
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

	// Draw background
	C.set_source_rgb_helper(cr,
		C.double(scheme.Background.R)/255.0,
		C.double(scheme.Background.G)/255.0,
		C.double(scheme.Background.B)/255.0)
	C.draw_rect(cr, 0, 0, C.double(cols*charWidth), C.double(rows*charHeight))

	// Set up font
	fontFamilyC := C.CString(fontFamily)
	defer C.free(unsafe.Pointer(fontFamilyC))
	C.cairo_select_font_face(cr, fontFamilyC, C.CAIRO_FONT_SLANT_NORMAL, C.CAIRO_FONT_WEIGHT_NORMAL)
	C.cairo_set_font_size(cr, C.double(fontSize))

	// Draw each cell
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			cell := w.buffer.GetCell(x, y)

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

			// Handle cursor
			isCursor := cursorVisible && x == cursorX && y == cursorY && w.cursorBlinkOn
			if isCursor {
				// Swap colors for block cursor
				fg, bg = bg, fg
			}

			// Draw cell background if different from terminal background
			if bg != scheme.Background {
				C.set_source_rgb_helper(cr,
					C.double(bg.R)/255.0,
					C.double(bg.G)/255.0,
					C.double(bg.B)/255.0)
				C.draw_rect(cr,
					C.double(x*charWidth),
					C.double(y*charHeight),
					C.double(charWidth),
					C.double(charHeight))
			}

			// Draw character
			if cell.Char != ' ' && cell.Char != 0 {
				C.set_source_rgb_helper(cr,
					C.double(fg.R)/255.0,
					C.double(fg.G)/255.0,
					C.double(fg.B)/255.0)

				// Set font weight
				if cell.Bold {
					C.cairo_select_font_face(cr, fontFamilyC, C.CAIRO_FONT_SLANT_NORMAL, C.CAIRO_FONT_WEIGHT_BOLD)
				}

				charStr := string(cell.Char)
				charC := C.CString(charStr)
				C.draw_text(cr,
					C.double(x*charWidth),
					C.double(y*charHeight+charAscent),
					charC)
				C.free(unsafe.Pointer(charC))

				// Reset font weight
				if cell.Bold {
					C.cairo_select_font_face(cr, fontFamilyC, C.CAIRO_FONT_SLANT_NORMAL, C.CAIRO_FONT_WEIGHT_NORMAL)
				}
			}

			// Draw underline if needed
			if cell.Underline {
				C.set_source_rgb_helper(cr,
					C.double(fg.R)/255.0,
					C.double(fg.G)/255.0,
					C.double(fg.B)/255.0)
				C.draw_rect(cr,
					C.double(x*charWidth),
					C.double((y+1)*charHeight-1),
					C.double(charWidth),
					1)
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

	cellX = int(screenX) / charWidth
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
		w.buffer.ClearSelection()
		w.buffer.StartSelection(cellX, cellY)
		w.selecting = true
		w.selectStartX = cellX
		w.selectStartY = cellY
		da.GrabFocus()
	}
	return true
}

func (w *Widget) onButtonRelease(da *gtk.DrawingArea, ev *gdk.Event) bool {
	btn := gdk.EventButtonNewFromEvent(ev)
	button := btn.Button()

	if button == 1 && w.selecting {
		w.selecting = false
		w.buffer.EndSelection()
	}
	return true
}

func (w *Widget) onMotionNotify(da *gtk.DrawingArea, ev *gdk.Event) bool {
	if !w.selecting {
		return false
	}

	motion := gdk.EventMotionNewFromEvent(ev)
	x, y := motion.MotionX(), motion.MotionY()
	cellX, cellY := w.screenToCell(x, y)
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
		// Regular character
		if keyval < 256 {
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
	}

	if len(data) > 0 {
		onInput(data)
		return true
	}

	return false
}

func (w *Widget) onConfigure(da *gtk.DrawingArea, ev *gdk.Event) bool {
	w.updateFontMetrics()

	// Recalculate terminal size based on widget size
	alloc := da.GetAllocation()
	newCols := alloc.GetWidth() / w.charWidth
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
