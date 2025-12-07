package purfectermqt

import (
	"math"
	"sync"

	"github.com/phroun/pawscript/pkg/purfecterm"
	"github.com/therecipe/qt/core"
	"github.com/therecipe/qt/gui"
	"github.com/therecipe/qt/widgets"
)

// Left padding for terminal content (pixels)
const terminalLeftPadding = 8

// Widget is a Qt terminal emulator widget
type Widget struct {
	widgets.QWidget

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
	blinkTimer     *core.QTimer
	blinkTickCount int

	// Text blink animation (bobbing wave)
	blinkPhase float64

	// Focus state
	hasFocus bool

	// Callback when data should be written to PTY
	onInput func([]byte)

	// Custom signals
	_ func() `signal:"terminalUpdated"`
}

// NewWidget creates a new terminal widget with the specified dimensions
func NewWidget(cols, rows, scrollbackSize int) *Widget {
	w := NewWidget2(nil, 0)
	w.init(cols, rows, scrollbackSize)
	return w
}

func (w *Widget) init(cols, rows, scrollbackSize int) {
	w.fontFamily = "Menlo"
	w.fontSize = 14
	w.charWidth = 10
	w.charHeight = 20
	w.charAscent = 16
	w.scheme = purfecterm.DefaultColorScheme()
	w.cursorBlinkOn = true

	// Create buffer and parser
	w.buffer = purfecterm.NewBuffer(cols, rows, scrollbackSize)
	w.parser = purfecterm.NewParser(w.buffer)

	// Set up dirty callback to trigger redraws
	w.buffer.SetDirtyCallback(func() {
		// Queue a repaint on the main thread
		core.QCoreApplication_PostEvent(w, core.NewQEvent(core.QEvent__UpdateRequest), 0)
	})

	// Enable focus and mouse tracking
	w.SetFocusPolicy(core.Qt__StrongFocus)
	w.SetMouseTracking(true)
	w.SetAttribute(core.Qt__WA_InputMethodEnabled, true)

	// Calculate font metrics
	w.updateFontMetrics()

	// Set initial size
	w.SetMinimumSize2(cols*w.charWidth+terminalLeftPadding, rows*w.charHeight)

	// Create blink timer (50ms for smooth animation)
	w.blinkTimer = core.NewQTimer(w)
	w.blinkTimer.ConnectTimeout(w.onBlinkTimer)
	w.blinkTimer.Start(50)

	// Connect paint event
	w.ConnectPaintEvent(w.paintEvent)
	w.ConnectKeyPressEvent(w.keyPressEvent)
	w.ConnectMousePressEvent(w.mousePressEvent)
	w.ConnectMouseReleaseEvent(w.mouseReleaseEvent)
	w.ConnectMouseMoveEvent(w.mouseMoveEvent)
	w.ConnectWheelEvent(w.wheelEvent)
	w.ConnectFocusInEvent(w.focusInEvent)
	w.ConnectFocusOutEvent(w.focusOutEvent)
	w.ConnectResizeEvent(w.resizeEvent)
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

	w.Update()
}

// SetFont sets the terminal font
func (w *Widget) SetFont(family string, size int) {
	w.mu.Lock()
	w.fontFamily = family
	w.fontSize = size
	w.mu.Unlock()
	w.updateFontMetrics()
	w.Update()
}

// SetColorScheme sets the color scheme
func (w *Widget) SetColorScheme(scheme purfecterm.ColorScheme) {
	w.mu.Lock()
	w.scheme = scheme
	w.mu.Unlock()
	w.Update()
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
	font := gui.NewQFont2(w.fontFamily, w.fontSize, -1, false)
	font.SetFixedPitch(true)
	metrics := gui.NewQFontMetrics(font)
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

func (w *Widget) paintEvent(event *gui.QPaintEvent) {
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

	painter := gui.NewQPainter2(w)
	defer painter.DestroyQPainter()

	// Fill background
	bgColor := gui.NewQColor3(int(scheme.Background.R), int(scheme.Background.G), int(scheme.Background.B), 255)
	painter.FillRect5(0, 0, w.Width(), w.Height(), bgColor)

	// Set up font
	font := gui.NewQFont2(fontFamily, fontSize, -1, false)
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
				bgQColor := gui.NewQColor3(int(bg.R), int(bg.G), int(bg.B), 255)
				painter.FillRect5(cellX, cellY, cellW, cellH, bgQColor)
			}

			// Draw character
			if cell.Char != ' ' && cell.Char != 0 && blinkVisible {
				fgQColor := gui.NewQColor3(int(fg.R), int(fg.G), int(fg.B), 255)
				painter.SetPen2(fgQColor)

				if cell.Bold {
					boldFont := gui.NewQFont2(fontFamily, fontSize, int(gui.QFont__Bold), false)
					boldFont.SetFixedPitch(true)
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
					painter.SetClipRect4(cellX, cellY, cellW, cellH)
					painter.Translate2(float64(cellX), float64(cellY+charAscent*2)+yOffset*2)
					painter.Scale(2.0, 2.0)
					painter.DrawText3(0, 0, string(cell.Char))
					painter.Restore()
				case purfecterm.LineAttrDoubleBottom:
					painter.Save()
					painter.SetClipRect4(cellX, cellY, cellW, cellH)
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
				fgQColor := gui.NewQColor3(int(fg.R), int(fg.G), int(fg.B), 255)
				underlineH := 1
				if lineAttr == purfecterm.LineAttrDoubleTop || lineAttr == purfecterm.LineAttrDoubleBottom {
					underlineH = 2
				}
				painter.FillRect5(cellX, cellY+cellH-1, cellW, underlineH, fgQColor)
			}

			// Draw cursor
			if isCursor {
				cursorQColor := gui.NewQColor3(int(scheme.Cursor.R), int(scheme.Cursor.G), int(scheme.Cursor.B), 255)
				switch cursorShape {
				case 0: // Block
					if !w.hasFocus {
						pen := gui.NewQPen3(cursorQColor)
						pen.SetWidth(1)
						painter.SetPen(pen)
						painter.DrawRect4(cellX, cellY, cellW-1, cellH-1)
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

func (w *Widget) keyPressEvent(event *gui.QKeyEvent) {
	w.mu.Lock()
	onInput := w.onInput
	w.mu.Unlock()

	if onInput == nil {
		return
	}

	key := event.Key()
	modifiers := event.Modifiers()

	hasShift := modifiers&core.Qt__ShiftModifier != 0
	hasCtrl := modifiers&core.Qt__ControlModifier != 0
	hasAlt := modifiers&core.Qt__AltModifier != 0
	hasMeta := modifiers&core.Qt__MetaModifier != 0

	var data []byte

	switch core.Qt__Key(key) {
	case core.Qt__Key_Return, core.Qt__Key_Enter:
		data = []byte{'\r'}
	case core.Qt__Key_Backspace:
		if hasCtrl {
			data = []byte{0x08}
		} else if hasAlt {
			data = []byte{0x1b, 0x7f}
		} else {
			data = []byte{0x7f}
		}
	case core.Qt__Key_Tab:
		if hasShift {
			data = []byte{0x1b, '[', 'Z'}
		} else {
			data = []byte{'\t'}
		}
	case core.Qt__Key_Escape:
		data = []byte{0x1b}
	case core.Qt__Key_Up:
		data = w.cursorKey('A', hasShift, hasCtrl, hasAlt, hasMeta)
	case core.Qt__Key_Down:
		data = w.cursorKey('B', hasShift, hasCtrl, hasAlt, hasMeta)
	case core.Qt__Key_Right:
		data = w.cursorKey('C', hasShift, hasCtrl, hasAlt, hasMeta)
	case core.Qt__Key_Left:
		data = w.cursorKey('D', hasShift, hasCtrl, hasAlt, hasMeta)
	case core.Qt__Key_Home:
		data = w.cursorKey('H', hasShift, hasCtrl, hasAlt, hasMeta)
	case core.Qt__Key_End:
		data = w.cursorKey('F', hasShift, hasCtrl, hasAlt, hasMeta)
	case core.Qt__Key_PageUp:
		data = w.tildeKey(5, hasShift, hasCtrl, hasAlt, hasMeta)
	case core.Qt__Key_PageDown:
		data = w.tildeKey(6, hasShift, hasCtrl, hasAlt, hasMeta)
	case core.Qt__Key_Insert:
		data = w.tildeKey(2, hasShift, hasCtrl, hasAlt, hasMeta)
	case core.Qt__Key_Delete:
		data = w.tildeKey(3, hasShift, hasCtrl, hasAlt, hasMeta)
	case core.Qt__Key_F1:
		data = w.functionKey('P', hasShift, hasCtrl, hasAlt, hasMeta)
	case core.Qt__Key_F2:
		data = w.functionKey('Q', hasShift, hasCtrl, hasAlt, hasMeta)
	case core.Qt__Key_F3:
		data = w.functionKey('R', hasShift, hasCtrl, hasAlt, hasMeta)
	case core.Qt__Key_F4:
		data = w.functionKey('S', hasShift, hasCtrl, hasAlt, hasMeta)
	case core.Qt__Key_F5:
		data = w.tildeKey(15, hasShift, hasCtrl, hasAlt, hasMeta)
	case core.Qt__Key_F6:
		data = w.tildeKey(17, hasShift, hasCtrl, hasAlt, hasMeta)
	case core.Qt__Key_F7:
		data = w.tildeKey(18, hasShift, hasCtrl, hasAlt, hasMeta)
	case core.Qt__Key_F8:
		data = w.tildeKey(19, hasShift, hasCtrl, hasAlt, hasMeta)
	case core.Qt__Key_F9:
		data = w.tildeKey(20, hasShift, hasCtrl, hasAlt, hasMeta)
	case core.Qt__Key_F10:
		data = w.tildeKey(21, hasShift, hasCtrl, hasAlt, hasMeta)
	case core.Qt__Key_F11:
		data = w.tildeKey(23, hasShift, hasCtrl, hasAlt, hasMeta)
	case core.Qt__Key_F12:
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

func (w *Widget) mousePressEvent(event *gui.QMouseEvent) {
	if event.Button() == core.Qt__LeftButton {
		cellX, cellY := w.screenToCell(int(event.X()), int(event.Y()))
		w.mouseDown = true
		w.mouseDownX = cellX
		w.mouseDownY = cellY
		w.selectionMoved = false
		w.buffer.ClearSelection()
		w.SetFocus2()
	}
}

func (w *Widget) mouseReleaseEvent(event *gui.QMouseEvent) {
	if event.Button() == core.Qt__LeftButton {
		w.mouseDown = false
		if w.selecting {
			w.selecting = false
			w.buffer.EndSelection()
		}
	}
}

func (w *Widget) mouseMoveEvent(event *gui.QMouseEvent) {
	if !w.mouseDown {
		return
	}

	cellX, cellY := w.screenToCell(int(event.X()), int(event.Y()))

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

func (w *Widget) wheelEvent(event *gui.QWheelEvent) {
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

func (w *Widget) focusInEvent(event *gui.QFocusEvent) {
	w.hasFocus = true
	w.cursorBlinkOn = true
	w.Update()
}

func (w *Widget) focusOutEvent(event *gui.QFocusEvent) {
	w.hasFocus = false
	w.Update()
}

func (w *Widget) resizeEvent(event *gui.QResizeEvent) {
	w.updateFontMetrics()

	newCols := (w.Width() - terminalLeftPadding) / w.charWidth
	newRows := w.Height() / w.charHeight

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
		clipboard := gui.QGuiApplication_Clipboard()
		clipboard.SetText(text, gui.QClipboard__Clipboard)
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

	clipboard := gui.QGuiApplication_Clipboard()
	text := clipboard.Text(gui.QClipboard__Clipboard)
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
