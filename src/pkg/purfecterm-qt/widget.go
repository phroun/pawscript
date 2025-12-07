package purfectermqt

import (
	"math"
	"sync"

	"github.com/mappu/miqt/qt"
	"github.com/phroun/pawscript/pkg/purfecterm"
)

// Left padding for terminal content (pixels)
const terminalLeftPadding = 8

// Widget is a Qt terminal emulator widget
type Widget struct {
	widget *qt.QWidget

	mu sync.Mutex

	// Terminal state
	buffer *purfecterm.Buffer
	parser *purfecterm.Parser

	// Font settings
	fontFamily string
	fontSize   int
	charWidth  int
	charHeight int
	charAscent int

	// Color scheme
	scheme purfecterm.ColorScheme

	// Selection state
	selecting      bool
	selectStartX   int
	selectStartY   int
	mouseDown      bool
	mouseDownX     int
	mouseDownY     int
	selectionMoved bool

	// Cursor blink
	cursorBlinkOn  bool
	blinkTimer     *qt.QTimer
	blinkTickCount int

	// Text blink animation (bobbing wave)
	blinkPhase float64

	// Focus state
	hasFocus bool

	// Callback when data should be written to PTY
	onInput func([]byte)

	// Context menu
	contextMenu *qt.QMenu
}

// NewWidget creates a new terminal widget with the specified dimensions
func NewWidget(cols, rows, scrollbackSize int) *Widget {
	w := &Widget{
		widget:        qt.NewQWidget2(),
		fontFamily:    "Monospace",
		fontSize:      14,
		charWidth:     10,
		charHeight:    20,
		charAscent:    16,
		scheme:        purfecterm.DefaultColorScheme(),
		cursorBlinkOn: true,
	}

	// Create buffer and parser
	w.buffer = purfecterm.NewBuffer(cols, rows, scrollbackSize)
	w.parser = purfecterm.NewParser(w.buffer)

	// Set up dirty callback to trigger redraws
	w.buffer.SetDirtyCallback(func() {
		w.widget.Update()
	})

	// Enable focus and mouse tracking
	w.widget.SetFocusPolicy(qt.StrongFocus)
	w.widget.SetMouseTracking(true)
	w.widget.SetAttribute(qt.WA_InputMethodEnabled)

	// Calculate font metrics
	w.updateFontMetrics()

	// Set initial size
	w.widget.SetMinimumSize2(cols*w.charWidth+terminalLeftPadding, rows*w.charHeight)

	// Create blink timer (50ms for smooth animation)
	w.blinkTimer = qt.NewQTimer2(w.widget.QObject)
	w.blinkTimer.OnTimeout(func() {
		w.onBlinkTimer()
	})
	w.blinkTimer.Start(50)

	// Connect events using miqt's OnXxxEvent pattern
	w.widget.OnPaintEvent(func(super func(event *qt.QPaintEvent), event *qt.QPaintEvent) {
		w.paintEvent(event)
	})
	w.widget.OnKeyPressEvent(func(super func(event *qt.QKeyEvent), event *qt.QKeyEvent) {
		w.keyPressEvent(event)
	})
	w.widget.OnMousePressEvent(func(super func(event *qt.QMouseEvent), event *qt.QMouseEvent) {
		w.mousePressEvent(event)
	})
	w.widget.OnMouseReleaseEvent(func(super func(event *qt.QMouseEvent), event *qt.QMouseEvent) {
		w.mouseReleaseEvent(event)
	})
	w.widget.OnMouseMoveEvent(func(super func(event *qt.QMouseEvent), event *qt.QMouseEvent) {
		w.mouseMoveEvent(event)
	})
	w.widget.OnWheelEvent(func(super func(event *qt.QWheelEvent), event *qt.QWheelEvent) {
		w.wheelEvent(event)
	})
	w.widget.OnFocusInEvent(func(super func(event *qt.QFocusEvent), event *qt.QFocusEvent) {
		w.focusInEvent(event)
	})
	w.widget.OnFocusOutEvent(func(super func(event *qt.QFocusEvent), event *qt.QFocusEvent) {
		w.focusOutEvent(event)
	})
	w.widget.OnResizeEvent(func(super func(event *qt.QResizeEvent), event *qt.QResizeEvent) {
		w.resizeEvent(event)
	})

	// Create context menu for right-click
	w.contextMenu = qt.NewQMenu2(w.widget)

	copyAction := w.contextMenu.AddAction("Copy")
	copyAction.OnTriggered(func() {
		w.CopySelection()
	})

	pasteAction := w.contextMenu.AddAction("Paste")
	pasteAction.OnTriggered(func() {
		w.PasteClipboard()
	})

	w.contextMenu.AddSeparator()

	selectAllAction := w.contextMenu.AddAction("Select All")
	selectAllAction.OnTriggered(func() {
		w.SelectAll()
	})

	clearAction := w.contextMenu.AddAction("Clear")
	clearAction.OnTriggered(func() {
		w.Clear()
	})

	// Enable context menu policy for right-click
	w.widget.SetContextMenuPolicy(qt.CustomContextMenu)
	w.widget.OnCustomContextMenuRequested(func(pos *qt.QPoint) {
		w.contextMenu.Exec2(w.widget.MapToGlobal(pos))
	})

	return w
}

// QWidget returns the underlying Qt widget
func (w *Widget) QWidget() *qt.QWidget {
	return w.widget
}

func (w *Widget) onBlinkTimer() {
	// Update text blink animation phase
	w.blinkPhase += 0.21
	if w.blinkPhase > 6.283185 {
		w.blinkPhase -= 6.283185
	}

	// Handle cursor blink timing
	w.blinkTickCount++
	_, cursorBlink := w.buffer.GetCursorStyle()
	if cursorBlink > 0 && w.hasFocus {
		ticksNeeded := 10
		if cursorBlink >= 2 {
			ticksNeeded = 5
		}
		if w.blinkTickCount >= ticksNeeded {
			w.blinkTickCount = 0
			w.cursorBlinkOn = !w.cursorBlinkOn
		}
	} else {
		if !w.cursorBlinkOn {
			w.cursorBlinkOn = true
		}
	}

	w.widget.Update()
}

// SetFont sets the terminal font
func (w *Widget) SetFont(family string, size int) {
	w.mu.Lock()
	w.fontFamily = family
	w.fontSize = size
	w.mu.Unlock()
	w.updateFontMetrics()
	w.widget.Update()
}

// SetColorScheme sets the color scheme
func (w *Widget) SetColorScheme(scheme purfecterm.ColorScheme) {
	w.mu.Lock()
	w.scheme = scheme
	w.mu.Unlock()
	w.widget.Update()
}

// SetInputCallback sets the callback for handling input
func (w *Widget) SetInputCallback(fn func([]byte)) {
	w.mu.Lock()
	w.onInput = fn
	w.mu.Unlock()
}

// Feed writes data to the terminal
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
func (w *Widget) Buffer() *purfecterm.Buffer {
	return w.buffer
}

func (w *Widget) updateFontMetrics() {
	font := qt.NewQFont6(w.fontFamily, w.fontSize)
	font.SetFixedPitch(true)
	metrics := qt.NewQFontMetrics(font)
	w.charWidth = metrics.AverageCharWidth()
	w.charHeight = metrics.Height()
	w.charAscent = metrics.Ascent()
	if w.charWidth < 1 {
		w.charWidth = w.fontSize * 6 / 10
	}
	if w.charHeight < 1 {
		w.charHeight = w.fontSize * 12 / 10
	}
}

func (w *Widget) paintEvent(event *qt.QPaintEvent) {
	w.mu.Lock()
	scheme := w.scheme
	fontFamily := w.fontFamily
	fontSize := w.fontSize
	charWidth := w.charWidth
	charHeight := w.charHeight
	charAscent := w.charAscent
	blinkPhase := w.blinkPhase
	w.mu.Unlock()

	cols, rows := w.buffer.GetSize()
	cursorX, cursorY := w.buffer.GetCursor()
	cursorVisible := w.buffer.IsCursorVisible()
	cursorShape, _ := w.buffer.GetCursorStyle()
	scrollOffset := w.buffer.GetScrollOffset()

	if scrollOffset > 0 {
		cursorVisible = false
	}

	painter := qt.NewQPainter2(w.widget.QPaintDevice)
	defer painter.End()

	// Fill background
	bgColor := qt.NewQColor3(int(scheme.Background.R), int(scheme.Background.G), int(scheme.Background.B))
	painter.FillRect5(0, 0, w.widget.Width(), w.widget.Height(), bgColor)

	// Set up font
	font := qt.NewQFont6(fontFamily, fontSize)
	font.SetFixedPitch(true)
	painter.SetFont(font)

	// Draw each cell
	for y := 0; y < rows; y++ {
		lineAttr := w.buffer.GetVisibleLineAttribute(y)

		effectiveCols := cols
		if lineAttr != purfecterm.LineAttrNormal {
			effectiveCols = cols / 2
		}

		for x := 0; x < effectiveCols; x++ {
			cell := w.buffer.GetVisibleCell(x, y)

			fg := cell.Foreground
			bg := cell.Background
			if fg.Default {
				fg = scheme.Foreground
			}
			if bg.Default {
				bg = scheme.Background
			}

			// Handle blink
			blinkVisible := true
			if cell.Blink {
				switch scheme.BlinkMode {
				case purfecterm.BlinkModeBright:
					for i := 0; i < 8; i++ {
						if len(scheme.Palette) > i+8 &&
							bg.R == scheme.Palette[i].R &&
							bg.G == scheme.Palette[i].G &&
							bg.B == scheme.Palette[i].B {
							bg = scheme.Palette[i+8]
							break
						}
					}
				case purfecterm.BlinkModeBlink:
					blinkVisible = blinkPhase < 3.14159
				}
			}

			// Handle selection
			if w.buffer.IsInSelection(x, y) {
				bg = scheme.Selection
			}

			// Handle cursor
			isCursor := cursorVisible && x == cursorX && y == cursorY && w.cursorBlinkOn
			if isCursor && w.hasFocus && cursorShape == 0 {
				fg, bg = bg, fg
			}

			// Calculate cell position
			var cellX, cellY, cellW, cellH int
			switch lineAttr {
			case purfecterm.LineAttrNormal:
				cellX = x*charWidth + terminalLeftPadding
				cellY = y * charHeight
				cellW = charWidth
				cellH = charHeight
			case purfecterm.LineAttrDoubleWidth:
				cellX = x*2*charWidth + terminalLeftPadding
				cellY = y * charHeight
				cellW = charWidth * 2
				cellH = charHeight
			case purfecterm.LineAttrDoubleTop, purfecterm.LineAttrDoubleBottom:
				cellX = x*2*charWidth + terminalLeftPadding
				cellY = y * charHeight
				cellW = charWidth * 2
				cellH = charHeight
			}

			// Draw background if different from terminal background
			if bg != scheme.Background {
				bgQColor := qt.NewQColor3(int(bg.R), int(bg.G), int(bg.B))
				painter.FillRect5(cellX, cellY, cellW, cellH, bgQColor)
			}

			// Draw character
			if cell.Char != ' ' && cell.Char != 0 && blinkVisible {
				fgQColor := qt.NewQColor3(int(fg.R), int(fg.G), int(fg.B))
				painter.SetPen(fgQColor)

				if cell.Bold {
					boldFont := qt.NewQFont6(fontFamily, fontSize)
					boldFont.SetFixedPitch(true)
					boldFont.SetBold(true)
					painter.SetFont(boldFont)
				}

				// Calculate bobbing wave offset
				yOffset := 0.0
				if cell.Blink && scheme.BlinkMode == purfecterm.BlinkModeBounce {
					wavePhase := blinkPhase + float64(x)*0.5
					yOffset = math.Sin(wavePhase) * 3.0
				}

				switch lineAttr {
				case purfecterm.LineAttrNormal:
					painter.DrawText3(cellX, cellY+charAscent+int(yOffset), string(cell.Char))
				case purfecterm.LineAttrDoubleWidth:
					painter.Save()
					painter.Translate2(float64(cellX), float64(cellY+charAscent)+yOffset)
					painter.Scale(2.0, 1.0)
					painter.DrawText3(0, 0, string(cell.Char))
					painter.Restore()
				case purfecterm.LineAttrDoubleTop:
					painter.Save()
					painter.SetClipRect2(cellX, cellY, cellW, cellH)
					painter.Translate2(float64(cellX), float64(cellY+charAscent*2)+yOffset*2)
					painter.Scale(2.0, 2.0)
					painter.DrawText3(0, 0, string(cell.Char))
					painter.Restore()
				case purfecterm.LineAttrDoubleBottom:
					painter.Save()
					painter.SetClipRect2(cellX, cellY, cellW, cellH)
					painter.Translate2(float64(cellX), float64(cellY+charAscent*2-charHeight)+yOffset*2)
					painter.Scale(2.0, 2.0)
					painter.DrawText3(0, 0, string(cell.Char))
					painter.Restore()
				}

				if cell.Bold {
					painter.SetFont(font)
				}
			}

			// Draw underline
			if cell.Underline {
				fgQColor := qt.NewQColor3(int(fg.R), int(fg.G), int(fg.B))
				underlineH := 1
				if lineAttr == purfecterm.LineAttrDoubleTop || lineAttr == purfecterm.LineAttrDoubleBottom {
					underlineH = 2
				}
				painter.FillRect5(cellX, cellY+cellH-1, cellW, underlineH, fgQColor)
			}

			// Draw cursor
			if isCursor {
				cursorQColor := qt.NewQColor3(int(scheme.Cursor.R), int(scheme.Cursor.G), int(scheme.Cursor.B))
				switch cursorShape {
				case 0: // Block
					if !w.hasFocus {
						pen := qt.NewQPen3(cursorQColor)
						pen.SetWidth(1)
						painter.SetPenWithPen(pen)
						painter.DrawRect2(cellX, cellY, cellW-1, cellH-1)
					}
				case 1: // Underline
					thickness := cellH / 4
					if !w.hasFocus {
						thickness = cellH / 6
					}
					painter.FillRect5(cellX, cellY+cellH-thickness, cellW, thickness, cursorQColor)
				case 2: // Bar
					thickness := 2
					if !w.hasFocus {
						thickness = 1
					}
					painter.FillRect5(cellX, cellY, thickness, cellH, cursorQColor)
				}
			}
		}
	}

	w.buffer.ClearDirty()
}

func (w *Widget) screenToCell(screenX, screenY int) (cellX, cellY int) {
	w.mu.Lock()
	charWidth := w.charWidth
	charHeight := w.charHeight
	w.mu.Unlock()

	cellY = screenY / charHeight
	cols, rows := w.buffer.GetSize()
	if cellY < 0 {
		cellY = 0
	}
	if cellY >= rows {
		cellY = rows - 1
	}

	lineAttr := w.buffer.GetVisibleLineAttribute(cellY)
	effectiveCharWidth := charWidth
	effectiveCols := cols
	if lineAttr != purfecterm.LineAttrNormal {
		effectiveCharWidth = charWidth * 2
		effectiveCols = cols / 2
	}

	cellX = (screenX - terminalLeftPadding) / effectiveCharWidth
	if cellX < 0 {
		cellX = 0
	}
	if cellX >= effectiveCols {
		cellX = effectiveCols - 1
	}
	return
}

func (w *Widget) keyPressEvent(event *qt.QKeyEvent) {
	w.mu.Lock()
	onInput := w.onInput
	w.mu.Unlock()

	if onInput == nil {
		return
	}

	key := event.Key()
	modifiers := event.Modifiers()

	hasShift := modifiers&qt.ShiftModifier != 0
	hasCtrl := modifiers&qt.ControlModifier != 0
	hasAlt := modifiers&qt.AltModifier != 0
	hasMeta := modifiers&qt.MetaModifier != 0

	var data []byte

	switch qt.Key(key) {
	case qt.Key_Return, qt.Key_Enter:
		data = []byte{'\r'}
	case qt.Key_Backspace:
		if hasCtrl {
			data = []byte{0x08}
		} else if hasAlt {
			data = []byte{0x1b, 0x7f}
		} else {
			data = []byte{0x7f}
		}
	case qt.Key_Tab:
		if hasShift {
			data = []byte{0x1b, '[', 'Z'}
		} else {
			data = []byte{'\t'}
		}
	case qt.Key_Escape:
		data = []byte{0x1b}
	case qt.Key_Up:
		data = w.cursorKey('A', hasShift, hasCtrl, hasAlt, hasMeta)
	case qt.Key_Down:
		data = w.cursorKey('B', hasShift, hasCtrl, hasAlt, hasMeta)
	case qt.Key_Right:
		data = w.cursorKey('C', hasShift, hasCtrl, hasAlt, hasMeta)
	case qt.Key_Left:
		data = w.cursorKey('D', hasShift, hasCtrl, hasAlt, hasMeta)
	case qt.Key_Home:
		data = w.cursorKey('H', hasShift, hasCtrl, hasAlt, hasMeta)
	case qt.Key_End:
		data = w.cursorKey('F', hasShift, hasCtrl, hasAlt, hasMeta)
	case qt.Key_PageUp:
		data = w.tildeKey(5, hasShift, hasCtrl, hasAlt, hasMeta)
	case qt.Key_PageDown:
		data = w.tildeKey(6, hasShift, hasCtrl, hasAlt, hasMeta)
	case qt.Key_Insert:
		data = w.tildeKey(2, hasShift, hasCtrl, hasAlt, hasMeta)
	case qt.Key_Delete:
		data = w.tildeKey(3, hasShift, hasCtrl, hasAlt, hasMeta)
	case qt.Key_F1:
		data = w.functionKey('P', hasShift, hasCtrl, hasAlt, hasMeta)
	case qt.Key_F2:
		data = w.functionKey('Q', hasShift, hasCtrl, hasAlt, hasMeta)
	case qt.Key_F3:
		data = w.functionKey('R', hasShift, hasCtrl, hasAlt, hasMeta)
	case qt.Key_F4:
		data = w.functionKey('S', hasShift, hasCtrl, hasAlt, hasMeta)
	case qt.Key_F5:
		data = w.tildeKey(15, hasShift, hasCtrl, hasAlt, hasMeta)
	case qt.Key_F6:
		data = w.tildeKey(17, hasShift, hasCtrl, hasAlt, hasMeta)
	case qt.Key_F7:
		data = w.tildeKey(18, hasShift, hasCtrl, hasAlt, hasMeta)
	case qt.Key_F8:
		data = w.tildeKey(19, hasShift, hasCtrl, hasAlt, hasMeta)
	case qt.Key_F9:
		data = w.tildeKey(20, hasShift, hasCtrl, hasAlt, hasMeta)
	case qt.Key_F10:
		data = w.tildeKey(21, hasShift, hasCtrl, hasAlt, hasMeta)
	case qt.Key_F11:
		data = w.tildeKey(23, hasShift, hasCtrl, hasAlt, hasMeta)
	case qt.Key_F12:
		data = w.tildeKey(24, hasShift, hasCtrl, hasAlt, hasMeta)
	default:
		// Regular character
		text := event.Text()
		if text != "" {
			ch := text[0]
			if hasCtrl && ch >= 'a' && ch <= 'z' {
				ch = ch - 'a' + 1
			} else if hasCtrl && ch >= 'A' && ch <= 'Z' {
				ch = ch - 'A' + 1
			}
			if hasAlt {
				data = []byte{0x1b, ch}
			} else {
				data = []byte(text)
			}
		}
	}

	if len(data) > 0 {
		onInput(data)
	}
}

func (w *Widget) cursorKey(key byte, hasShift, hasCtrl, hasAlt, hasMeta bool) []byte {
	mod := w.calcMod(hasShift, hasCtrl, hasAlt, hasMeta)
	if mod > 1 {
		return []byte{0x1b, '[', '1', ';', byte('0' + mod), key}
	}
	return []byte{0x1b, '[', key}
}

func (w *Widget) tildeKey(num int, hasShift, hasCtrl, hasAlt, hasMeta bool) []byte {
	mod := w.calcMod(hasShift, hasCtrl, hasAlt, hasMeta)
	if mod > 1 {
		return []byte{0x1b, '[', byte('0' + num/10), byte('0' + num%10), ';', byte('0' + mod), '~'}
	}
	return []byte{0x1b, '[', byte('0' + num/10), byte('0' + num%10), '~'}
}

func (w *Widget) functionKey(key byte, hasShift, hasCtrl, hasAlt, hasMeta bool) []byte {
	mod := w.calcMod(hasShift, hasCtrl, hasAlt, hasMeta)
	if mod > 1 {
		return []byte{0x1b, '[', '1', ';', byte('0' + mod), key}
	}
	return []byte{0x1b, 'O', key}
}

func (w *Widget) calcMod(hasShift, hasCtrl, hasAlt, hasMeta bool) int {
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
	if hasMeta {
		mod += 8
	}
	return mod
}

func (w *Widget) mousePressEvent(event *qt.QMouseEvent) {
	if event.Button() == qt.LeftButton {
		pos := event.Pos()
		cellX, cellY := w.screenToCell(pos.X(), pos.Y())
		w.mouseDown = true
		w.mouseDownX = cellX
		w.mouseDownY = cellY
		w.selectionMoved = false
		w.buffer.ClearSelection()
		w.widget.SetFocus()
	}
}

func (w *Widget) mouseReleaseEvent(event *qt.QMouseEvent) {
	if event.Button() == qt.LeftButton {
		w.mouseDown = false
		if w.selecting {
			w.selecting = false
			w.buffer.EndSelection()
		}
	}
}

func (w *Widget) mouseMoveEvent(event *qt.QMouseEvent) {
	if !w.mouseDown {
		return
	}

	pos := event.Pos()
	cellX, cellY := w.screenToCell(pos.X(), pos.Y())

	if !w.selectionMoved {
		if cellX != w.mouseDownX || cellY != w.mouseDownY {
			w.selectionMoved = true
			w.selecting = true
			w.selectStartX = w.mouseDownX
			w.selectStartY = w.mouseDownY
			w.buffer.StartSelection(w.mouseDownX, w.mouseDownY)
		} else {
			return
		}
	}

	w.buffer.UpdateSelection(cellX, cellY)
}

func (w *Widget) wheelEvent(event *qt.QWheelEvent) {
	delta := event.AngleDelta().Y()
	offset := w.buffer.GetScrollOffset()
	scrollbackSize := w.buffer.GetScrollbackSize()

	if delta > 0 {
		offset += 3
		if offset > scrollbackSize {
			offset = scrollbackSize
		}
	} else if delta < 0 {
		offset -= 3
		if offset < 0 {
			offset = 0
		}
	}

	w.buffer.SetScrollOffset(offset)
}

func (w *Widget) focusInEvent(event *qt.QFocusEvent) {
	w.hasFocus = true
	w.cursorBlinkOn = true
	w.widget.Update()
}

func (w *Widget) focusOutEvent(event *qt.QFocusEvent) {
	w.hasFocus = false
	w.widget.Update()
}

func (w *Widget) resizeEvent(event *qt.QResizeEvent) {
	w.updateFontMetrics()

	newCols := (w.widget.Width() - terminalLeftPadding) / w.charWidth
	newRows := w.widget.Height() / w.charHeight

	if newCols < 1 {
		newCols = 1
	}
	if newRows < 1 {
		newRows = 1
	}

	w.buffer.Resize(newCols, newRows)
}

// Resize resizes the terminal to the specified dimensions
func (w *Widget) Resize(cols, rows int) {
	w.buffer.Resize(cols, rows)
}

// GetSize returns the current terminal size
func (w *Widget) GetSize() (cols, rows int) {
	return w.buffer.GetSize()
}

// GetSelectedText returns the currently selected text
func (w *Widget) GetSelectedText() string {
	return w.buffer.GetSelectedText()
}

// CopySelection copies selected text to clipboard
func (w *Widget) CopySelection() {
	if w.buffer.HasSelection() {
		text := w.buffer.GetSelectedText()
		clipboard := qt.QGuiApplication_Clipboard()
		clipboard.SetText(text)
	}
}

// PasteClipboard pastes text from clipboard
func (w *Widget) PasteClipboard() {
	w.mu.Lock()
	onInput := w.onInput
	w.mu.Unlock()

	if onInput == nil {
		return
	}

	clipboard := qt.QGuiApplication_Clipboard()
	text := clipboard.Text()
	if text != "" {
		useBracketedPaste := w.buffer.IsBracketedPasteModeEnabled()

		if !useBracketedPaste {
			for _, c := range text {
				if c == '\n' || c == '\r' || c == '\x1b' || c < 32 {
					useBracketedPaste = true
					break
				}
			}
		}

		if useBracketedPaste {
			onInput([]byte("\x1b[200~"))
			onInput([]byte(text))
			onInput([]byte("\x1b[201~"))
		} else {
			onInput([]byte(text))
		}
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
