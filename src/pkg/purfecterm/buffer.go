package purfecterm

import (
	"sync"
)

// LineAttribute defines the display mode for a line (VT100 DECDHL/DECDWL)
type LineAttribute int

const (
	LineAttrNormal       LineAttribute = iota // Normal single-width, single-height
	LineAttrDoubleWidth                       // DECDWL: Double-width line (ESC#6)
	LineAttrDoubleTop                         // DECDHL: Double-height top half (ESC#3)
	LineAttrDoubleBottom                      // DECDHL: Double-height bottom half (ESC#4)
)

// Buffer manages the terminal screen and scrollback buffer
type Buffer struct {
	mu sync.RWMutex

	cols int
	rows int

	cursorX       int
	cursorY       int
	cursorVisible bool
	cursorShape   int // 0=block, 1=underline, 2=bar
	cursorBlink   int // 0=no blink, 1=slow blink, 2=fast blink

	bracketedPasteMode bool

	currentFg        Color
	currentBg        Color
	currentBold      bool
	currentItalic    bool
	currentUnderline bool
	currentReverse   bool
	currentBlink     bool

	screen    [][]Cell
	lineAttrs []LineAttribute

	scrollback     [][]Cell
	scrollbackAttr []LineAttribute
	maxScrollback  int
	scrollOffset   int

	selectionActive      bool
	selStartX, selStartY int
	selEndX, selEndY     int

	savedCursorX int
	savedCursorY int

	dirty   bool
	onDirty func()
}

// NewBuffer creates a new terminal buffer
func NewBuffer(cols, rows, maxScrollback int) *Buffer {
	b := &Buffer{
		cols:          cols,
		rows:          rows,
		cursorVisible: true,
		currentFg:     DefaultForeground,
		currentBg:     DefaultBackground,
		maxScrollback: maxScrollback,
		dirty:         true,
	}
	b.initScreen()
	return b
}

// SetDirtyCallback sets a callback to be invoked when the buffer changes
func (b *Buffer) SetDirtyCallback(fn func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onDirty = fn
}

func (b *Buffer) markDirty() {
	b.dirty = true
	if b.onDirty != nil {
		b.onDirty()
	}
}

func (b *Buffer) initScreen() {
	b.screen = make([][]Cell, b.rows)
	b.lineAttrs = make([]LineAttribute, b.rows)
	for i := range b.screen {
		b.screen[i] = b.makeEmptyLine()
		b.lineAttrs[i] = LineAttrNormal
	}
}

func (b *Buffer) makeEmptyLine() []Cell {
	line := make([]Cell, b.cols)
	for i := range line {
		line[i] = EmptyCell()
	}
	return line
}

// Resize resizes the terminal buffer
func (b *Buffer) Resize(cols, rows int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if cols == b.cols && rows == b.rows {
		return
	}

	oldScreen := b.screen
	oldLineAttrs := b.lineAttrs
	oldRows := b.rows
	oldCols := b.cols

	b.cols = cols
	b.rows = rows
	b.initScreen()

	copyRows := oldRows
	if rows < copyRows {
		copyRows = rows
	}
	copyCols := oldCols
	if cols < copyCols {
		copyCols = cols
	}

	for y := 0; y < copyRows && y < len(oldScreen); y++ {
		for x := 0; x < copyCols && x < len(oldScreen[y]); x++ {
			b.screen[y][x] = oldScreen[y][x]
		}
		if y < len(oldLineAttrs) {
			b.lineAttrs[y] = oldLineAttrs[y]
		}
	}

	if b.cursorX >= cols {
		b.cursorX = cols - 1
	}
	if b.cursorY >= rows {
		b.cursorY = rows - 1
	}

	b.markDirty()
}

// GetSize returns the current terminal dimensions
func (b *Buffer) GetSize() (cols, rows int) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.cols, b.rows
}

// GetCursor returns the current cursor position
func (b *Buffer) GetCursor() (x, y int) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.cursorX, b.cursorY
}

// SetCursor sets the cursor position (clamped to valid range)
func (b *Buffer) SetCursor(x, y int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.setCursorInternal(x, y)
}

func (b *Buffer) setCursorInternal(x, y int) {
	if x < 0 {
		x = 0
	}
	if x >= b.cols {
		x = b.cols - 1
	}
	if y < 0 {
		y = 0
	}
	if y >= b.rows {
		y = b.rows - 1
	}
	b.cursorX = x
	b.cursorY = y
	b.markDirty()
}

// SetCursorVisible sets cursor visibility
func (b *Buffer) SetCursorVisible(visible bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cursorVisible = visible
	b.markDirty()
}

// IsCursorVisible returns cursor visibility
func (b *Buffer) IsCursorVisible() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.cursorVisible
}

// SetCursorStyle sets the cursor shape and blink mode
func (b *Buffer) SetCursorStyle(shape, blink int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cursorShape = shape
	b.cursorBlink = blink
	b.markDirty()
}

// GetCursorStyle returns the cursor shape and blink mode
func (b *Buffer) GetCursorStyle() (shape, blink int) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.cursorShape, b.cursorBlink
}

// SetBracketedPasteMode enables or disables bracketed paste mode
func (b *Buffer) SetBracketedPasteMode(enabled bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.bracketedPasteMode = enabled
}

// IsBracketedPasteModeEnabled returns whether bracketed paste mode is enabled
func (b *Buffer) IsBracketedPasteModeEnabled() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.bracketedPasteMode
}

// SaveCursor saves the current cursor position
func (b *Buffer) SaveCursor() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.savedCursorX = b.cursorX
	b.savedCursorY = b.cursorY
}

// RestoreCursor restores the saved cursor position
func (b *Buffer) RestoreCursor() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cursorX = b.savedCursorX
	b.cursorY = b.savedCursorY
	b.markDirty()
}

// WriteChar writes a character at the current cursor position
func (b *Buffer) WriteChar(ch rune) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.writeCharInternal(ch)
}

func (b *Buffer) writeCharInternal(ch rune) {
	if b.cursorX >= b.cols {
		b.cursorX = 0
		b.cursorY++
		if b.cursorY >= b.rows {
			b.scrollUpInternal()
			b.cursorY = b.rows - 1
		}
	}

	fg := b.currentFg
	bg := b.currentBg
	if b.currentReverse {
		fg, bg = bg, fg
	}

	b.screen[b.cursorY][b.cursorX] = Cell{
		Char:       ch,
		Foreground: fg,
		Background: bg,
		Bold:       b.currentBold,
		Italic:     b.currentItalic,
		Underline:  b.currentUnderline,
		Blink:      b.currentBlink,
	}
	b.cursorX++
	b.markDirty()
}

// Newline moves cursor to the beginning of the next line
func (b *Buffer) Newline() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cursorX = 0
	b.cursorY++
	if b.cursorY >= b.rows {
		b.scrollUpInternal()
		b.cursorY = b.rows - 1
	}
	b.markDirty()
}

// CarriageReturn moves cursor to the beginning of the current line
func (b *Buffer) CarriageReturn() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cursorX = 0
	b.markDirty()
}

// LineFeed moves cursor down one line
func (b *Buffer) LineFeed() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cursorY++
	if b.cursorY >= b.rows {
		b.scrollUpInternal()
		b.cursorY = b.rows - 1
	}
	b.markDirty()
}

// Tab moves cursor to the next tab stop
func (b *Buffer) Tab() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cursorX = ((b.cursorX / 8) + 1) * 8
	if b.cursorX >= b.cols {
		b.cursorX = b.cols - 1
	}
	b.markDirty()
}

// Backspace moves cursor left one position
func (b *Buffer) Backspace() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cursorX > 0 {
		b.cursorX--
	}
	b.markDirty()
}

func (b *Buffer) scrollUpInternal() {
	if len(b.scrollback) >= b.maxScrollback {
		b.scrollback = b.scrollback[1:]
		b.scrollbackAttr = b.scrollbackAttr[1:]
	}
	b.scrollback = append(b.scrollback, b.screen[0])
	b.scrollbackAttr = append(b.scrollbackAttr, b.lineAttrs[0])

	copy(b.screen, b.screen[1:])
	copy(b.lineAttrs, b.lineAttrs[1:])
	b.screen[b.rows-1] = b.makeEmptyLine()
	b.lineAttrs[b.rows-1] = LineAttrNormal
	b.markDirty()
}

// ScrollUp scrolls up by n lines
func (b *Buffer) ScrollUp(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := 0; i < n; i++ {
		b.scrollUpInternal()
	}
}

// ScrollDown scrolls down by n lines
func (b *Buffer) ScrollDown(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := 0; i < n; i++ {
		copy(b.screen[1:], b.screen[:b.rows-1])
		copy(b.lineAttrs[1:], b.lineAttrs[:b.rows-1])
		b.screen[0] = b.makeEmptyLine()
		b.lineAttrs[0] = LineAttrNormal
	}
	b.markDirty()
}

// ClearScreen clears the entire screen
func (b *Buffer) ClearScreen() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.initScreen()
	b.markDirty()
}

// ClearToEndOfLine clears from cursor to end of line
func (b *Buffer) ClearToEndOfLine() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for x := b.cursorX; x < b.cols; x++ {
		b.screen[b.cursorY][x] = EmptyCellWithColors(b.currentFg, b.currentBg)
	}
	b.markDirty()
}

// ClearToStartOfLine clears from start of line to cursor
func (b *Buffer) ClearToStartOfLine() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for x := 0; x <= b.cursorX && x < b.cols; x++ {
		b.screen[b.cursorY][x] = EmptyCellWithColors(b.currentFg, b.currentBg)
	}
	b.markDirty()
}

// ClearLine clears the entire current line
func (b *Buffer) ClearLine() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.screen[b.cursorY] = b.makeEmptyLine()
	b.markDirty()
}

// ClearToEndOfScreen clears from cursor to end of screen
func (b *Buffer) ClearToEndOfScreen() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for x := b.cursorX; x < b.cols; x++ {
		b.screen[b.cursorY][x] = EmptyCellWithColors(b.currentFg, b.currentBg)
	}
	for y := b.cursorY + 1; y < b.rows; y++ {
		b.screen[y] = b.makeEmptyLine()
	}
	b.markDirty()
}

// ClearToStartOfScreen clears from start of screen to cursor
func (b *Buffer) ClearToStartOfScreen() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for y := 0; y < b.cursorY; y++ {
		b.screen[y] = b.makeEmptyLine()
	}
	for x := 0; x <= b.cursorX && x < b.cols; x++ {
		b.screen[b.cursorY][x] = EmptyCellWithColors(b.currentFg, b.currentBg)
	}
	b.markDirty()
}

// SetAttributes sets current text rendering attributes
func (b *Buffer) SetAttributes(fg, bg Color, bold, italic, underline, reverse bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentFg = fg
	b.currentBg = bg
	b.currentBold = bold
	b.currentItalic = italic
	b.currentUnderline = underline
	b.currentReverse = reverse
}

// ResetAttributes resets text attributes to defaults
func (b *Buffer) ResetAttributes() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentFg = DefaultForeground
	b.currentBg = DefaultBackground
	b.currentBold = false
	b.currentItalic = false
	b.currentUnderline = false
	b.currentReverse = false
	b.currentBlink = false
}

// SetForeground sets the current foreground color
func (b *Buffer) SetForeground(c Color) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentFg = c
}

// SetBackground sets the current background color
func (b *Buffer) SetBackground(c Color) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentBg = c
}

// SetBold sets bold attribute
func (b *Buffer) SetBold(bold bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentBold = bold
}

// SetItalic sets italic attribute
func (b *Buffer) SetItalic(italic bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentItalic = italic
}

// SetUnderline sets underline attribute
func (b *Buffer) SetUnderline(underline bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentUnderline = underline
}

// SetReverse sets reverse video attribute
func (b *Buffer) SetReverse(reverse bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentReverse = reverse
}

// SetBlink sets blink attribute
func (b *Buffer) SetBlink(blink bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentBlink = blink
}

// GetCell returns the cell at the given screen position
func (b *Buffer) GetCell(x, y int) Cell {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if y < 0 || y >= b.rows || x < 0 || x >= b.cols {
		return EmptyCell()
	}
	return b.screen[y][x]
}

// GetVisibleCell returns the cell accounting for scroll offset
func (b *Buffer) GetVisibleCell(x, y int) Cell {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if x < 0 || x >= b.cols || y < 0 || y >= b.rows {
		return EmptyCell()
	}

	if b.scrollOffset == 0 {
		return b.screen[y][x]
	}

	scrollbackSize := len(b.scrollback)
	if y < b.scrollOffset {
		scrollbackIdx := scrollbackSize - b.scrollOffset + y
		if scrollbackIdx < 0 || scrollbackIdx >= scrollbackSize {
			return EmptyCell()
		}
		line := b.scrollback[scrollbackIdx]
		if x < len(line) {
			return line[x]
		}
		return EmptyCell()
	}
	screenY := y - b.scrollOffset
	if screenY >= 0 && screenY < b.rows {
		return b.screen[screenY][x]
	}
	return EmptyCell()
}

// GetScrollbackSize returns the number of lines in scrollback
func (b *Buffer) GetScrollbackSize() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.scrollback)
}

// SetScrollOffset sets how many lines we're scrolled back
func (b *Buffer) SetScrollOffset(offset int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if offset < 0 {
		offset = 0
	}
	if offset > len(b.scrollback) {
		offset = len(b.scrollback)
	}
	b.scrollOffset = offset
	b.markDirty()
}

// GetScrollOffset returns current scroll offset
func (b *Buffer) GetScrollOffset() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.scrollOffset
}

// IsDirty returns true if the buffer has changed since last render
func (b *Buffer) IsDirty() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.dirty
}

// ClearDirty clears the dirty flag
func (b *Buffer) ClearDirty() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dirty = false
}

// MoveCursorUp moves cursor up n rows
func (b *Buffer) MoveCursorUp(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cursorY -= n
	if b.cursorY < 0 {
		b.cursorY = 0
	}
	b.markDirty()
}

// MoveCursorDown moves cursor down n rows
func (b *Buffer) MoveCursorDown(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cursorY += n
	if b.cursorY >= b.rows {
		b.cursorY = b.rows - 1
	}
	b.markDirty()
}

// MoveCursorForward moves cursor right n columns
func (b *Buffer) MoveCursorForward(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cursorX += n
	if b.cursorX >= b.cols {
		b.cursorX = b.cols - 1
	}
	b.markDirty()
}

// MoveCursorBackward moves cursor left n columns
func (b *Buffer) MoveCursorBackward(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cursorX -= n
	if b.cursorX < 0 {
		b.cursorX = 0
	}
	b.markDirty()
}

// InsertLines inserts n blank lines at cursor
func (b *Buffer) InsertLines(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := 0; i < n; i++ {
		if b.cursorY < b.rows-1 {
			copy(b.screen[b.cursorY+1:], b.screen[b.cursorY:b.rows-1])
			copy(b.lineAttrs[b.cursorY+1:], b.lineAttrs[b.cursorY:b.rows-1])
		}
		b.screen[b.cursorY] = b.makeEmptyLine()
		b.lineAttrs[b.cursorY] = LineAttrNormal
	}
	b.markDirty()
}

// DeleteLines deletes n lines at cursor
func (b *Buffer) DeleteLines(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := 0; i < n; i++ {
		if b.cursorY < b.rows-1 {
			copy(b.screen[b.cursorY:], b.screen[b.cursorY+1:])
			copy(b.lineAttrs[b.cursorY:], b.lineAttrs[b.cursorY+1:])
		}
		b.screen[b.rows-1] = b.makeEmptyLine()
		b.lineAttrs[b.rows-1] = LineAttrNormal
	}
	b.markDirty()
}

// DeleteChars deletes n characters at cursor
func (b *Buffer) DeleteChars(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	line := b.screen[b.cursorY]
	if b.cursorX+n < b.cols {
		copy(line[b.cursorX:], line[b.cursorX+n:])
	}
	for i := b.cols - n; i < b.cols; i++ {
		if i >= 0 {
			line[i] = EmptyCellWithColors(b.currentFg, b.currentBg)
		}
	}
	b.markDirty()
}

// InsertChars inserts n blank characters at cursor
func (b *Buffer) InsertChars(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	line := b.screen[b.cursorY]
	if b.cursorX+n < b.cols {
		copy(line[b.cursorX+n:], line[b.cursorX:b.cols-n])
	}
	for i := 0; i < n && b.cursorX+i < b.cols; i++ {
		line[b.cursorX+i] = EmptyCellWithColors(b.currentFg, b.currentBg)
	}
	b.markDirty()
}

// EraseChars erases n characters at cursor
func (b *Buffer) EraseChars(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := 0; i < n && b.cursorX+i < b.cols; i++ {
		b.screen[b.cursorY][b.cursorX+i] = EmptyCellWithColors(b.currentFg, b.currentBg)
	}
	b.markDirty()
}

// StartSelection begins a text selection
func (b *Buffer) StartSelection(x, y int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.selectionActive = true
	b.selStartX = x
	b.selStartY = y
	b.selEndX = x
	b.selEndY = y
	b.markDirty()
}

// UpdateSelection updates the end point of the selection
func (b *Buffer) UpdateSelection(x, y int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.selectionActive {
		return
	}
	b.selEndX = x
	b.selEndY = y
	b.markDirty()
}

// EndSelection finalizes the selection
func (b *Buffer) EndSelection() {
	// Selection remains active until cleared
}

// ClearSelection clears any active selection
func (b *Buffer) ClearSelection() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.selectionActive = false
	b.markDirty()
}

// HasSelection returns true if there's an active selection
func (b *Buffer) HasSelection() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.selectionActive
}

// GetSelection returns the normalized selection bounds
func (b *Buffer) GetSelection() (startX, startY, endX, endY int, active bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.selectionActive {
		return 0, 0, 0, 0, false
	}
	sx, sy := b.selStartX, b.selStartY
	ex, ey := b.selEndX, b.selEndY
	if sy > ey || (sy == ey && sx > ex) {
		sx, sy, ex, ey = ex, ey, sx, sy
	}
	return sx, sy, ex, ey, true
}

// GetSelectedText returns the text in the current selection
func (b *Buffer) GetSelectedText() string {
	sx, sy, ex, ey, active := b.GetSelection()
	if !active {
		return ""
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	var lines []string
	for y := sy; y <= ey && y < b.rows; y++ {
		startX := 0
		endX := b.cols
		if y == sy {
			startX = sx
		}
		if y == ey {
			endX = ex + 1
		}
		var lineRunes []rune
		for x := startX; x < endX && x < b.cols; x++ {
			lineRunes = append(lineRunes, b.screen[y][x].Char)
		}
		line := string(lineRunes)
		for len(line) > 0 && (line[len(line)-1] == ' ' || line[len(line)-1] == 0) {
			line = line[:len(line)-1]
		}
		lines = append(lines, line)
	}

	result := ""
	for i, line := range lines {
		result += line
		if i < len(lines)-1 {
			result += "\n"
		}
	}
	return result
}

// IsInSelection returns true if the given position is within the selection
func (b *Buffer) IsInSelection(x, y int) bool {
	sx, sy, ex, ey, active := b.GetSelection()
	if !active {
		return false
	}
	if y < sy || y > ey {
		return false
	}
	if y == sy && y == ey {
		return x >= sx && x <= ex
	}
	if y == sy {
		return x >= sx
	}
	if y == ey {
		return x <= ex
	}
	return true
}

// SelectAll selects all text in the terminal
func (b *Buffer) SelectAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.selectionActive = true
	b.selStartX = 0
	b.selStartY = 0
	b.selEndX = b.cols - 1
	b.selEndY = b.rows - 1
	b.markDirty()
}

// SetLineAttribute sets the display attribute for the current line
func (b *Buffer) SetLineAttribute(attr LineAttribute) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cursorY >= 0 && b.cursorY < len(b.lineAttrs) {
		b.lineAttrs[b.cursorY] = attr
		b.markDirty()
	}
}

// GetLineAttribute returns the display attribute for the specified line
func (b *Buffer) GetLineAttribute(y int) LineAttribute {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if y >= 0 && y < len(b.lineAttrs) {
		return b.lineAttrs[y]
	}
	return LineAttrNormal
}

// GetVisibleLineAttribute returns the line attribute accounting for scroll offset
func (b *Buffer) GetVisibleLineAttribute(y int) LineAttribute {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if y < 0 || y >= b.rows {
		return LineAttrNormal
	}

	if b.scrollOffset == 0 {
		if y < len(b.lineAttrs) {
			return b.lineAttrs[y]
		}
		return LineAttrNormal
	}

	scrollbackSize := len(b.scrollback)
	if y < b.scrollOffset {
		scrollbackIdx := scrollbackSize - b.scrollOffset + y
		if scrollbackIdx >= 0 && scrollbackIdx < len(b.scrollbackAttr) {
			return b.scrollbackAttr[scrollbackIdx]
		}
		return LineAttrNormal
	}

	screenY := y - b.scrollOffset
	if screenY >= 0 && screenY < len(b.lineAttrs) {
		return b.lineAttrs[screenY]
	}
	return LineAttrNormal
}
