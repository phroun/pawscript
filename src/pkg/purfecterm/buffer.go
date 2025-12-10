package purfecterm

import (
	"sync"
)

// ScrollMagneticThreshold creates a "magnetic" zone at the boundary between
// the logical screen and scrollback. The user must scroll past this many lines
// before the yellow dashed boundary line appears, making it feel sticky.
const ScrollMagneticThreshold = 5

// Buffer manages the terminal screen and scrollback buffer
type Buffer struct {
	mu sync.RWMutex

	// Physical dimensions (visible area from widget)
	cols int
	rows int

	// Logical dimensions (terminal's idea of its size, may differ from physical)
	// 0 means "use physical dimension"
	logicalCols int
	logicalRows int

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
	currentFlexWidth bool // Current attribute for East Asian Width mode

	// Flexible cell width mode (East Asian Width)
	flexWidthMode      bool               // When true, new chars get FlexWidth=true and calculated CellWidth
	visualWidthWrap    bool               // When true, wrap based on accumulated visual width, not cell count
	ambiguousWidthMode AmbiguousWidthMode // How to handle ambiguous width chars: Auto/Narrow/Wide

	// Screen storage - lines can have variable width
	screen    [][]Cell
	lineInfos []LineInfo

	// Buffer-wide default for logical lines with no stored data
	screenInfo ScreenInfo

	// Scrollback storage
	scrollback         [][]Cell
	scrollbackInfo     []LineInfo
	maxScrollback      int
	scrollOffset       int  // Vertical scroll offset
	scrollbackDisabled bool // When true, scrollback accumulation is disabled (for games)

	// Horizontal scrolling
	horizOffset int // Horizontal scroll offset (in columns)

	selectionActive      bool
	selStartX, selStartY int
	selEndX, selEndY     int

	savedCursorX int
	savedCursorY int

	dirty         bool
	onDirty       func()
	onScaleChange func() // Called when screen scaling modes change

	// Screen scaling modes
	columnMode132 bool // 132-column mode: horizontal scale 0.6060 (ESC [ 3 h/l)
	columnMode40  bool // 40-column mode: horizontal scale 2.0 (custom)
	lineDensity   int  // Line density: 25 (default), 30, 43, 50, 60

	// Custom glyph system - tile-based graphics
	currentBGP   int  // Current Base Glyph Palette (-1 = use foreground color code)
	currentXFlip bool // Current horizontal flip attribute
	currentYFlip bool // Current vertical flip attribute

	// Global palette and glyph storage (shared across all cells)
	palettes     map[int]*Palette      // Palette number -> Palette
	customGlyphs map[rune]*CustomGlyph // Rune -> CustomGlyph

	// Note: Glyph cache invalidation uses content hashing (Palette.ComputeHash, CustomGlyph.ComputeHash)
	// instead of version tracking, so alternating between glyph frames will be cache hits

	// Sprite overlay system
	sprites      map[int]*Sprite        // Sprite ID -> Sprite
	cropRects    map[int]*CropRectangle // Crop rectangle ID -> CropRectangle
	spriteUnitX  int                    // Subdivisions per cell horizontally (default 8)
	spriteUnitY  int                    // Subdivisions per cell vertically (default 8)

	// Screen crop (in sprite coordinate units, -1 = no crop)
	widthCrop  int // X coordinate beyond which nothing renders
	heightCrop int // Y coordinate below which nothing renders

	// Screen splits for multi-region rendering
	screenSplits map[int]*ScreenSplit // Split ID -> ScreenSplit

	// Max content width from splits (for horizontal scrollbar, independent from scrollback)
	splitContentWidth int
}

// ScreenSplit defines a split region that can show a different part of the buffer.
// ScreenY is a LOGICAL scanline number relative to the scroll boundary (yellow dotted line).
// The first logical scanline (0) begins after the scrollback area - no splits can occur
// in the scrollback area above the yellow dotted line.
type ScreenSplit struct {
	ScreenY         int     // Y in sprite units relative to logical screen start (NOT absolute screen)
	BufferRow       int     // 0-indexed row in logical screen to start drawing from
	BufferCol       int     // 0-indexed column in logical screen to start drawing from
	TopFineScroll   int     // 0 to (subdivisions-1), higher = more of top row clipped
	LeftFineScroll  int     // 0 to (subdivisions-1), higher = more of left column clipped
	CharWidthScale  float64 // Character width multiplier (0 = inherit from main screen)
	LineDensity     int     // Line density override (0 = inherit from main screen)
}

// NewBuffer creates a new terminal buffer
func NewBuffer(cols, rows, maxScrollback int) *Buffer {
	b := &Buffer{
		cols:                cols,
		rows:                rows,
		logicalCols:         0, // 0 means use physical
		logicalRows:         0, // 0 means use physical
		cursorVisible:       true,
		currentFg:           DefaultForeground,
		currentBg:           DefaultBackground,
		maxScrollback:       maxScrollback,
		screenInfo:          DefaultScreenInfo(),
		dirty:               true,
		lineDensity:         25,            // Default line density
		currentBGP:          -1,            // -1 = use foreground color code as palette
		palettes:     make(map[int]*Palette),
		customGlyphs: make(map[rune]*CustomGlyph),
		sprites:             make(map[int]*Sprite),
		cropRects:           make(map[int]*CropRectangle),
		spriteUnitX:         8,  // Default: 8 subdivisions per cell
		spriteUnitY:         8,  // Default: 8 subdivisions per cell
		widthCrop:           -1, // -1 = no crop
		heightCrop:          -1, // -1 = no crop
		screenSplits:        make(map[int]*ScreenSplit),
	}
	b.initScreen()
	return b
}

// EffectiveCols returns the logical column count (physical if logical is 0)
func (b *Buffer) EffectiveCols() int {
	if b.logicalCols > 0 {
		return b.logicalCols
	}
	return b.cols
}

// EffectiveRows returns the logical row count (physical if logical is 0)
func (b *Buffer) EffectiveRows() int {
	if b.logicalRows > 0 {
		return b.logicalRows
	}
	return b.rows
}

// SetDirtyCallback sets a callback to be invoked when the buffer changes
func (b *Buffer) SetDirtyCallback(fn func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onDirty = fn
}

// SetScaleChangeCallback sets a callback to be invoked when screen scaling modes change
// This allows the widget to recalculate terminal dimensions when scale changes
func (b *Buffer) SetScaleChangeCallback(fn func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onScaleChange = fn
}

func (b *Buffer) markDirty() {
	b.dirty = true
	if b.onDirty != nil {
		b.onDirty()
	}
}

func (b *Buffer) notifyScaleChange() {
	if b.onScaleChange != nil {
		b.onScaleChange()
	}
}

func (b *Buffer) initScreen() {
	effectiveRows := b.EffectiveRows()
	b.screen = make([][]Cell, effectiveRows)
	b.lineInfos = make([]LineInfo, effectiveRows)
	for i := range b.screen {
		b.screen[i] = b.makeEmptyLine()
		b.lineInfos[i] = b.makeDefaultLineInfo()
	}
}

// makeEmptyLine creates an empty line (zero length - will grow as chars are written)
func (b *Buffer) makeEmptyLine() []Cell {
	// Start with zero length - lines grow dynamically as characters are written
	return make([]Cell, 0)
}

// makeDefaultLineInfo creates a LineInfo with current attributes
func (b *Buffer) makeDefaultLineInfo() LineInfo {
	return LineInfo{
		Attribute:   LineAttrNormal,
		DefaultCell: b.currentDefaultCell(),
	}
}

// currentDefaultCell creates an empty cell with current attribute settings
func (b *Buffer) currentDefaultCell() Cell {
	fg := b.currentFg
	bg := b.currentBg
	if b.currentReverse {
		fg, bg = bg, fg
	}
	return EmptyCellWithAttrs(fg, bg, b.currentBold, b.currentItalic, b.currentUnderline, b.currentReverse, b.currentBlink)
}

// updateScreenInfo updates the screen info with current attributes
// Called on clear screen, clear to end of screen, and formfeed
func (b *Buffer) updateScreenInfo() {
	b.screenInfo = ScreenInfo{
		DefaultCell: b.currentDefaultCell(),
	}
}

// Resize resizes the physical terminal dimensions
// This updates the visible area but does NOT truncate line content
func (b *Buffer) Resize(cols, rows int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if cols == b.cols && rows == b.rows {
		return
	}

	b.cols = cols
	b.rows = rows

	// If logical dimensions are 0 (using physical), we may need to adjust screen size
	if b.logicalRows == 0 {
		b.adjustScreenToRows(rows)
	}

	// Clamp cursor to logical dimensions (not physical)
	effectiveCols := b.EffectiveCols()
	effectiveRows := b.EffectiveRows()
	if b.cursorX >= effectiveCols {
		b.cursorX = effectiveCols - 1
	}
	if b.cursorY >= effectiveRows {
		b.cursorY = effectiveRows - 1
	}

	b.markDirty()
}

// adjustScreenToRows adjusts the screen slice to have the target number of rows
// without truncating line content (lines remain variable width)
// Only moves lines to scrollback if actual content exceeds the new height
func (b *Buffer) adjustScreenToRows(targetRows int) {
	currentRows := len(b.screen)

	if targetRows == currentRows {
		return
	}

	if targetRows > currentRows {
		// Add new empty lines
		for i := currentRows; i < targetRows; i++ {
			b.screen = append(b.screen, b.makeEmptyLine())
			b.lineInfos = append(b.lineInfos, b.makeDefaultLineInfo())
		}
	} else {
		// Shrink: only move lines to scrollback if content doesn't fit
		// Find the last row with actual content
		lastContentRow := -1
		for i := currentRows - 1; i >= 0; i-- {
			if len(b.screen[i]) > 0 {
				lastContentRow = i
				break
			}
		}

		// Calculate how many lines need to go to scrollback
		// Only push if content extends beyond target height
		linesToPush := 0
		if lastContentRow >= targetRows {
			linesToPush = lastContentRow - targetRows + 1
		}

		// Push content lines to scrollback
		for i := 0; i < linesToPush; i++ {
			b.pushLineToScrollback(b.screen[0], b.lineInfos[0])
			b.screen = b.screen[1:]
			b.lineInfos = b.lineInfos[1:]
		}

		// Adjust cursor position to stay with content
		if linesToPush > 0 {
			b.cursorY -= linesToPush
			if b.cursorY < 0 {
				b.cursorY = 0
			}
		}

		// Now trim or add to reach target rows
		currentRows = len(b.screen)
		if currentRows > targetRows {
			// Trim empty lines from bottom
			b.screen = b.screen[:targetRows]
			b.lineInfos = b.lineInfos[:targetRows]
		} else if currentRows < targetRows {
			// Add empty lines to reach target
			for i := currentRows; i < targetRows; i++ {
				b.screen = append(b.screen, b.makeEmptyLine())
				b.lineInfos = append(b.lineInfos, b.makeDefaultLineInfo())
			}
		}
	}
}

// pushLineToScrollback adds a line to the scrollback buffer
func (b *Buffer) pushLineToScrollback(line []Cell, info LineInfo) {
	// Skip if scrollback is disabled (lines are discarded instead)
	if b.scrollbackDisabled {
		return
	}
	if len(b.scrollback) >= b.maxScrollback {
		b.scrollback = b.scrollback[1:]
		b.scrollbackInfo = b.scrollbackInfo[1:]
	}
	b.scrollback = append(b.scrollback, line)
	b.scrollbackInfo = append(b.scrollbackInfo, info)
}

// SetLogicalSize sets the logical terminal dimensions
// A value of 0 means "use physical dimension"
// This implements the ESC [ 8 ; rows ; cols t escape sequence
func (b *Buffer) SetLogicalSize(logicalRows, logicalCols int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	oldEffectiveRows := b.EffectiveRows()

	// Update logical dimensions (0 means use physical)
	b.logicalCols = logicalCols
	b.logicalRows = logicalRows

	newEffectiveRows := b.EffectiveRows()

	if newEffectiveRows == oldEffectiveRows {
		b.markDirty()
		return
	}

	if newEffectiveRows > oldEffectiveRows {
		// Growing - add empty lines at bottom if needed
		// The logical top stays the same, we gain scrollable area
		for len(b.screen) < newEffectiveRows {
			b.screen = append(b.screen, b.makeEmptyLine())
			b.lineInfos = append(b.lineInfos, LineInfo{
				Attribute:   LineAttrNormal,
				DefaultCell: b.screenInfo.DefaultCell,
			})
		}
	} else {
		// Shrinking - need to move excess lines to scrollback
		// Find the last line with actual content
		b.shrinkLogicalScreen(newEffectiveRows)
	}

	// Clamp cursor to new dimensions
	effectiveCols := b.EffectiveCols()
	if b.cursorX >= effectiveCols {
		b.cursorX = effectiveCols - 1
	}
	if b.cursorY >= newEffectiveRows {
		b.cursorY = newEffectiveRows - 1
	}

	b.markDirty()
}

// shrinkLogicalScreen shrinks the screen to targetRows
// Lines above the new top are transferred to scrollback
func (b *Buffer) shrinkLogicalScreen(targetRows int) {
	if targetRows <= 0 || len(b.screen) <= targetRows {
		return
	}

	// Find the last line that has actual content (non-empty)
	lastContentLine := -1
	for i := len(b.screen) - 1; i >= 0; i-- {
		if len(b.screen[i]) > 0 {
			lastContentLine = i
			break
		}
	}

	if lastContentLine < 0 {
		// No content at all - just resize
		b.screen = b.screen[:targetRows]
		b.lineInfos = b.lineInfos[:targetRows]
		return
	}

	// Count from lastContentLine up to get targetRows lines
	// but never go beyond the current top (index 0)
	newTopLine := lastContentLine - targetRows + 1
	if newTopLine < 0 {
		newTopLine = 0
	}

	// Transfer lines above newTopLine to scrollback
	for i := 0; i < newTopLine; i++ {
		b.pushLineToScrollback(b.screen[i], b.lineInfos[i])
	}

	// Keep lines from newTopLine to end, but only up to targetRows
	if newTopLine > 0 {
		b.screen = b.screen[newTopLine:]
		b.lineInfos = b.lineInfos[newTopLine:]
	}

	// Trim to targetRows (this handles the case where we have more lines than target)
	if len(b.screen) > targetRows {
		b.screen = b.screen[:targetRows]
		b.lineInfos = b.lineInfos[:targetRows]
	}

	// If we still have fewer lines than target, add empty ones
	for len(b.screen) < targetRows {
		b.screen = append(b.screen, b.makeEmptyLine())
		b.lineInfos = append(b.lineInfos, LineInfo{
			Attribute:   LineAttrNormal,
			DefaultCell: b.screenInfo.DefaultCell,
		})
	}
}

// GetLogicalSize returns the logical terminal dimensions
// Returns 0 for dimensions that are set to "use physical"
func (b *Buffer) GetLogicalSize() (logicalRows, logicalCols int) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.logicalRows, b.logicalCols
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
	// Use effective (logical) dimensions for cursor bounds
	effectiveCols := b.EffectiveCols()
	effectiveRows := b.EffectiveRows()
	if x < 0 {
		x = 0
	}
	if x >= effectiveCols {
		x = effectiveCols - 1
	}
	if y < 0 {
		y = 0
	}
	if y >= effectiveRows {
		y = effectiveRows - 1
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

// SetFlexWidthMode enables or disables flexible East Asian Width mode
// When enabled, new characters get FlexWidth=true and their CellWidth calculated
// based on Unicode East_Asian_Width property (0.5/1.0/1.5/2.0 cell units)
func (b *Buffer) SetFlexWidthMode(enabled bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.flexWidthMode = enabled
	b.currentFlexWidth = enabled
}

// IsFlexWidthModeEnabled returns whether flexible East Asian Width mode is enabled
func (b *Buffer) IsFlexWidthModeEnabled() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.flexWidthMode
}

// SetVisualWidthWrap enables or disables visual width-based line wrapping
// When enabled, lines wrap based on accumulated visual width (sum of CellWidth)
// When disabled, lines wrap based on cell count
func (b *Buffer) SetVisualWidthWrap(enabled bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.visualWidthWrap = enabled
}

// IsVisualWidthWrapEnabled returns whether visual width-based wrapping is enabled
func (b *Buffer) IsVisualWidthWrapEnabled() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.visualWidthWrap
}

// SetAmbiguousWidthMode sets the handling for ambiguous East Asian Width characters
// Auto: match width of previous character (default)
// Narrow: always 1.0 width
// Wide: always 2.0 width
func (b *Buffer) SetAmbiguousWidthMode(mode AmbiguousWidthMode) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ambiguousWidthMode = mode
}

// GetAmbiguousWidthMode returns the current ambiguous width mode
func (b *Buffer) GetAmbiguousWidthMode() AmbiguousWidthMode {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.ambiguousWidthMode
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

// getPreviousCellWidth returns the width of the previous cell for ambiguous auto-matching.
// If there's no previous cell or it doesn't have FlexWidth set, returns 1.0.
func (b *Buffer) getPreviousCellWidth() float64 {
	// Find the previous cell
	prevX := b.cursorX - 1
	prevY := b.cursorY

	// If we're at the start of a line, try the end of the previous line
	if prevX < 0 {
		if prevY > 0 {
			prevY--
			if prevY < len(b.screen) && len(b.screen[prevY]) > 0 {
				prevX = len(b.screen[prevY]) - 1
			} else {
				return 1.0 // No previous cell, default to 1.0
			}
		} else {
			return 1.0 // At start of buffer, default to 1.0
		}
	}

	// Get the previous cell
	if prevY < len(b.screen) && prevX < len(b.screen[prevY]) {
		prevCell := b.screen[prevY][prevX]
		if prevCell.FlexWidth && prevCell.CellWidth > 0 {
			return prevCell.CellWidth
		}
	}

	return 1.0 // Default to 1.0 if no valid previous cell
}

// getLineVisualWidth calculates the accumulated visual width of a line up to (but not including) col.
// Returns the sum of CellWidth values for cells 0 to col-1.
func (b *Buffer) getLineVisualWidth(row, col int) float64 {
	if row < 0 || row >= len(b.screen) {
		return 0
	}
	line := b.screen[row]
	width := 0.0
	for i := 0; i < col && i < len(line); i++ {
		if line[i].CellWidth > 0 {
			width += line[i].CellWidth
		} else {
			width += 1.0 // Default for cells without width set
		}
	}
	return width
}

// GetLineVisualWidth returns the visual width of a line up to (but not including) col.
// This is the public thread-safe version.
func (b *Buffer) GetLineVisualWidth(row, col int) float64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.getLineVisualWidth(row, col)
}

// GetTotalLineVisualWidth returns the total visual width of a line.
func (b *Buffer) GetTotalLineVisualWidth(row int) float64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if row < 0 || row >= len(b.screen) {
		return 0
	}
	return b.getLineVisualWidth(row, len(b.screen[row]))
}

func (b *Buffer) writeCharInternal(ch rune) {
	// Handle combining characters (Hebrew vowel points, diacritics, etc.)
	// These should be appended to the previous cell, not placed in a new cell
	if IsCombiningMark(ch) {
		b.appendCombiningMark(ch)
		return
	}

	effectiveCols := b.EffectiveCols()
	effectiveRows := b.EffectiveRows()

	// Check if this character has a custom glyph defined
	hasCustomGlyph := b.customGlyphs[ch] != nil

	// Calculate the width this character will take
	var charWidth float64
	if b.currentFlexWidth {
		if hasCustomGlyph {
			// Custom glyphs: check ambiguous width mode first for explicit overrides
			switch b.ambiguousWidthMode {
			case AmbiguousWidthNarrow:
				charWidth = 1.0
			case AmbiguousWidthWide:
				charWidth = 2.0
			default: // AmbiguousWidthAuto
				// Auto mode: use the underlying character's width category
				charWidth = GetEastAsianWidth(ch)
				// If the underlying character is ambiguous, match previous cell
				if charWidth < 0 {
					charWidth = b.getPreviousCellWidth()
				}
			}
		} else {
			charWidth = GetEastAsianWidth(ch)
			// Handle ambiguous width characters (-1.0 means ambiguous)
			if charWidth < 0 {
				switch b.ambiguousWidthMode {
				case AmbiguousWidthNarrow:
					charWidth = 1.0
				case AmbiguousWidthWide:
					charWidth = 2.0
				default: // AmbiguousWidthAuto
					// Match width of previous character
					charWidth = b.getPreviousCellWidth()
				}
			}
		}
	} else {
		charWidth = 1.0
	}

	// Handle line wrap
	// If visual width wrap is enabled, wrap based on accumulated visual width
	// Otherwise, wrap based on cell count (traditional behavior)
	shouldWrap := false
	if b.visualWidthWrap && b.currentFlexWidth {
		// Visual width wrap: wrap when adding this char would exceed column limit
		currentVisualWidth := b.getLineVisualWidth(b.cursorY, b.cursorX)
		shouldWrap = (currentVisualWidth + charWidth) > float64(effectiveCols)
	} else {
		// Traditional cell-count wrap
		shouldWrap = b.cursorX >= effectiveCols
	}

	if shouldWrap {
		b.cursorX = 0
		b.cursorY++
		if b.cursorY >= effectiveRows {
			b.scrollUpInternal()
			b.cursorY = effectiveRows - 1
		}
	}

	// Ensure screen has enough rows
	for b.cursorY >= len(b.screen) {
		b.screen = append(b.screen, b.makeEmptyLine())
		b.lineInfos = append(b.lineInfos, b.makeDefaultLineInfo())
	}

	// Ensure line is long enough for the cursor position
	b.ensureLineLength(b.cursorY, b.cursorX+1)

	fg := b.currentFg
	bg := b.currentBg
	if b.currentReverse {
		fg, bg = bg, fg
	}

	cell := Cell{
		Char:       ch,
		Foreground: fg,
		Background: bg,
		Bold:       b.currentBold,
		Italic:     b.currentItalic,
		Underline:  b.currentUnderline,
		Blink:      b.currentBlink,
		FlexWidth:  b.currentFlexWidth,
		BGP:        b.currentBGP,
		XFlip:      b.currentXFlip,
		YFlip:      b.currentYFlip,
	}

	// Use the calculated charWidth (already accounts for custom glyphs and ambiguous width mode)
	cell.CellWidth = charWidth

	b.screen[b.cursorY][b.cursorX] = cell
	b.cursorX++
	b.markDirty()
}

// appendCombiningMark appends a combining character to the previous cell.
// If there's no previous cell to attach to, the character is ignored.
func (b *Buffer) appendCombiningMark(ch rune) {
	// Find the previous cell to attach the combining mark to
	prevX := b.cursorX - 1
	prevY := b.cursorY

	// If we're at the start of a line, try the end of the previous line
	if prevX < 0 {
		if prevY > 0 {
			prevY--
			if prevY < len(b.screen) && len(b.screen[prevY]) > 0 {
				prevX = len(b.screen[prevY]) - 1
			} else {
				// No previous cell to attach to
				return
			}
		} else {
			// No previous cell to attach to (very start of buffer)
			return
		}
	}

	// Ensure the previous row exists and has the cell
	if prevY >= len(b.screen) || prevX >= len(b.screen[prevY]) {
		return
	}

	// Append the combining mark to the previous cell
	b.screen[prevY][prevX].Combining += string(ch)
	b.markDirty()
}

// ensureLineLength ensures a line has at least the specified length,
// filling gaps with the line's default cell
func (b *Buffer) ensureLineLength(row, length int) {
	if row >= len(b.screen) {
		return
	}
	line := b.screen[row]
	if len(line) >= length {
		return
	}
	// Get fill cell from line info or use empty cell
	var fillCell Cell
	if row < len(b.lineInfos) {
		fillCell = b.lineInfos[row].DefaultCell
		fillCell.Char = ' '
	} else {
		fillCell = EmptyCell()
	}
	// Extend line
	for len(line) < length {
		line = append(line, fillCell)
	}
	b.screen[row] = line
}

// Newline moves cursor to the beginning of the next line
func (b *Buffer) Newline() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cursorX = 0
	b.cursorY++
	effectiveRows := b.EffectiveRows()
	if b.cursorY >= effectiveRows {
		b.scrollUpInternal()
		b.cursorY = effectiveRows - 1
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
	effectiveRows := b.EffectiveRows()
	if b.cursorY >= effectiveRows {
		b.scrollUpInternal()
		b.cursorY = effectiveRows - 1
	}
	b.markDirty()
}

// Tab moves cursor to the next tab stop
func (b *Buffer) Tab() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cursorX = ((b.cursorX / 8) + 1) * 8
	effectiveCols := b.EffectiveCols()
	if b.cursorX >= effectiveCols {
		b.cursorX = effectiveCols - 1
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
	if len(b.screen) == 0 {
		return
	}

	// Push top line to scrollback
	b.pushLineToScrollback(b.screen[0], b.lineInfos[0])

	// Shift screen up
	copy(b.screen, b.screen[1:])
	copy(b.lineInfos, b.lineInfos[1:])

	// Add new empty line at bottom with current attributes
	lastIdx := len(b.screen) - 1
	b.screen[lastIdx] = b.makeEmptyLine()
	b.lineInfos[lastIdx] = b.makeDefaultLineInfo()
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
	screenLen := len(b.screen)
	for i := 0; i < n && screenLen > 0; i++ {
		copy(b.screen[1:], b.screen[:screenLen-1])
		copy(b.lineInfos[1:], b.lineInfos[:screenLen-1])
		b.screen[0] = b.makeEmptyLine()
		b.lineInfos[0] = b.makeDefaultLineInfo()
	}
	b.markDirty()
}

// ClearScreen clears the entire screen
func (b *Buffer) ClearScreen() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.updateScreenInfo() // Update screen default attributes
	b.initScreen()
	b.markDirty()
}

// ClearToEndOfLine clears from cursor to end of line
// This updates the line's default cell and truncates the line at cursor position
func (b *Buffer) ClearToEndOfLine() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.cursorY >= len(b.screen) {
		return
	}

	// Update line info with current attributes (for rendering beyond stored content)
	if b.cursorY < len(b.lineInfos) {
		b.lineInfos[b.cursorY].DefaultCell = b.currentDefaultCell()
	}

	// Truncate line at cursor position (variable width lines)
	if b.cursorX < len(b.screen[b.cursorY]) {
		b.screen[b.cursorY] = b.screen[b.cursorY][:b.cursorX]
	}

	b.markDirty()
}

// ClearToStartOfLine clears from start of line to cursor
// Note: Does NOT update LineInfo (LineInfo is for right side of line)
// Note: Does NOT extend the line - only clears existing cells
func (b *Buffer) ClearToStartOfLine() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.cursorY >= len(b.screen) {
		return
	}

	line := b.screen[b.cursorY]
	lineLen := len(line)

	// Only clear cells that actually exist in the line
	// No need to extend the line - cells beyond the line are conceptually blank
	clearCell := b.currentDefaultCell()
	endX := b.cursorX
	if endX >= lineLen {
		endX = lineLen - 1
	}
	for x := 0; x <= endX; x++ {
		line[x] = clearCell
	}
	b.markDirty()
}

// ClearLine clears the entire current line
// This updates the line's default cell
func (b *Buffer) ClearLine() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.cursorY >= len(b.screen) {
		return
	}

	// Update line info with current attributes
	if b.cursorY < len(b.lineInfos) {
		b.lineInfos[b.cursorY].DefaultCell = b.currentDefaultCell()
	}

	// Clear the line (make it empty - variable width)
	b.screen[b.cursorY] = b.makeEmptyLine()
	b.markDirty()
}

// ClearToEndOfScreen clears from cursor to end of screen
// This updates the ScreenInfo default cell
func (b *Buffer) ClearToEndOfScreen() {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Update screen info with current attributes
	b.updateScreenInfo()

	// Clear current line from cursor to end
	if b.cursorY < len(b.screen) {
		if b.cursorY < len(b.lineInfos) {
			b.lineInfos[b.cursorY].DefaultCell = b.currentDefaultCell()
		}
		if b.cursorX < len(b.screen[b.cursorY]) {
			b.screen[b.cursorY] = b.screen[b.cursorY][:b.cursorX]
		}
	}

	// Clear all lines below cursor
	for y := b.cursorY + 1; y < len(b.screen); y++ {
		b.screen[y] = b.makeEmptyLine()
		if y < len(b.lineInfos) {
			b.lineInfos[y] = b.makeDefaultLineInfo()
		}
	}
	b.markDirty()
}

// ClearToStartOfScreen clears from start of screen to cursor
// Note: Does NOT update ScreenInfo (ScreenInfo is for lines below stored content)
// Note: Does NOT extend lines - only clears existing cells
func (b *Buffer) ClearToStartOfScreen() {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Clear all lines above cursor
	for y := 0; y < b.cursorY && y < len(b.screen); y++ {
		b.screen[y] = b.makeEmptyLine()
		if y < len(b.lineInfos) {
			b.lineInfos[y] = b.makeDefaultLineInfo()
		}
	}

	// Clear current line from start to cursor (only existing cells)
	if b.cursorY < len(b.screen) {
		line := b.screen[b.cursorY]
		lineLen := len(line)
		clearCell := b.currentDefaultCell()
		endX := b.cursorX
		if endX >= lineLen {
			endX = lineLen - 1
		}
		for x := 0; x <= endX; x++ {
			line[x] = clearCell
		}
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
// For positions beyond stored line length, returns the line's default cell
func (b *Buffer) GetCell(x, y int) Cell {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.getCellInternal(x, y)
}

func (b *Buffer) getCellInternal(x, y int) Cell {
	// Check if y is beyond stored lines
	if y < 0 || y >= len(b.screen) {
		// Return screen default for lines beyond stored content
		return b.screenInfo.DefaultCell
	}

	line := b.screen[y]
	// Check if x is beyond this line's stored content
	if x < 0 || x >= len(line) {
		// Return line's default cell
		if y < len(b.lineInfos) {
			cell := b.lineInfos[y].DefaultCell
			cell.Char = ' '
			return cell
		}
		return EmptyCell()
	}

	return line[x]
}

// GetVisibleCell returns the cell accounting for scroll offset (both vertical and horizontal)
func (b *Buffer) GetVisibleCell(x, y int) Cell {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.getVisibleCellInternal(x, y)
}

func (b *Buffer) getVisibleCellInternal(x, y int) Cell {
	// Apply horizontal scroll offset
	actualX := x + b.horizOffset

	if y < 0 || y >= b.rows {
		return b.screenInfo.DefaultCell
	}

	effectiveRows := b.EffectiveRows()
	scrollbackSize := len(b.scrollback)

	// Calculate how much of the logical screen is hidden above
	// (if logical > physical, some logical rows are above the visible area)
	logicalHiddenAbove := 0
	if effectiveRows > b.rows {
		logicalHiddenAbove = effectiveRows - b.rows
	}

	// Calculate effective scroll offset - clamp to logicalHiddenAbove when in magnetic zone
	// This ensures the magnetic zone shows logical screen content, not scrollback
	effectiveScrollOffset := b.scrollOffset
	boundaryRow := b.scrollOffset - logicalHiddenAbove
	if boundaryRow > 0 && boundaryRow <= ScrollMagneticThreshold {
		// In magnetic zone - render as if viewing full logical screen
		effectiveScrollOffset = logicalHiddenAbove
	}

	// Total scrollable area above visible: scrollback + hidden logical rows
	totalScrollableAbove := scrollbackSize + logicalHiddenAbove

	if effectiveScrollOffset == 0 {
		// Not scrolled - show bottom of logical screen
		// Map visible y to logical y (bottom-aligned)
		logicalY := logicalHiddenAbove + y
		return b.getLogicalCell(actualX, logicalY)
	}

	// Scrolled up - need to map visible y to either scrollback or logical screen
	// scrollOffset goes from 0 (not scrolled) to totalScrollableAbove (scrolled to top)
	absoluteY := totalScrollableAbove - effectiveScrollOffset + y

	if absoluteY < scrollbackSize {
		// In scrollback
		return b.getScrollbackCell(actualX, absoluteY)
	}

	// In logical screen
	logicalY := absoluteY - scrollbackSize
	return b.getLogicalCell(actualX, logicalY)
}

// getScrollbackCell returns a cell from the scrollback buffer
func (b *Buffer) getScrollbackCell(x, scrollbackY int) Cell {
	if scrollbackY < 0 || scrollbackY >= len(b.scrollback) {
		return b.screenInfo.DefaultCell
	}

	line := b.scrollback[scrollbackY]
	if x < 0 || x >= len(line) {
		// Beyond line content - use line's default
		if scrollbackY < len(b.scrollbackInfo) {
			cell := b.scrollbackInfo[scrollbackY].DefaultCell
			cell.Char = ' '
			return cell
		}
		return EmptyCell()
	}
	return line[x]
}

// getLogicalCell returns a cell from the logical screen
func (b *Buffer) getLogicalCell(x, logicalY int) Cell {
	if logicalY < 0 {
		return b.screenInfo.DefaultCell
	}

	if logicalY >= len(b.screen) {
		// Beyond stored lines - use screen default
		return b.screenInfo.DefaultCell
	}

	line := b.screen[logicalY]
	if x < 0 || x >= len(line) {
		// Beyond line content - use line's default
		if logicalY < len(b.lineInfos) {
			cell := b.lineInfos[logicalY].DefaultCell
			cell.Char = ' '
			return cell
		}
		return EmptyCell()
	}
	return line[x]
}

// GetScrollbackSize returns the number of lines in scrollback
func (b *Buffer) GetScrollbackSize() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.scrollback)
}

// GetMaxScrollOffset returns the maximum vertical scroll offset
// This accounts for scrollback AND any logical rows hidden above the visible area
func (b *Buffer) GetMaxScrollOffset() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.getMaxScrollOffsetInternal()
}

func (b *Buffer) getMaxScrollOffsetInternal() int {
	effectiveRows := b.EffectiveRows()

	// If logical screen is larger than physical, some rows are hidden
	logicalHiddenAbove := 0
	if effectiveRows > b.rows {
		logicalHiddenAbove = effectiveRows - b.rows
	}

	// When scrollback is disabled, only allow scrolling within logical screen
	if b.scrollbackDisabled {
		return logicalHiddenAbove
	}

	scrollbackSize := len(b.scrollback)
	return scrollbackSize + logicalHiddenAbove
}

// SetScrollOffset sets how many lines we're scrolled back
func (b *Buffer) SetScrollOffset(offset int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	maxOffset := b.getMaxScrollOffsetInternal()
	if offset < 0 {
		offset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
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

// NormalizeScrollOffset snaps the scroll offset back if it's in the magnetic zone.
// The magnetic zone is when the boundary would appear at rows 1-ScrollMagneticThreshold.
// This should be called when scrolling down to create a "sticky" effect at the
// boundary between logical screen and scrollback.
// Returns true if the offset was changed.
func (b *Buffer) NormalizeScrollOffset() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	effectiveRows := b.EffectiveRows()

	// Calculate how much of the logical screen is hidden above
	logicalHiddenAbove := 0
	if effectiveRows > b.rows {
		logicalHiddenAbove = effectiveRows - b.rows
	}

	// Calculate where the boundary would appear
	boundaryRow := b.scrollOffset - logicalHiddenAbove

	// Check if we're in the magnetic zone (boundary would be at rows 1-threshold)
	if boundaryRow > 0 && boundaryRow <= ScrollMagneticThreshold {
		// Snap back to where boundary is at row 0 (just off the top of visible area)
		b.scrollOffset = logicalHiddenAbove
		b.markDirty()
		return true
	}
	return false
}

// GetScrollbackBoundaryVisibleRow returns the visible row (0-indexed from top of display)
// where the boundary between scrollback and logical screen is located.
// Returns -1 if the boundary is not currently visible (either fully in scrollback or fully in logical screen).
// The magnetic threshold suppresses the boundary for the first few rows after it would appear,
// creating a "sticky" feel when transitioning from logical screen to scrollback.
func (b *Buffer) GetScrollbackBoundaryVisibleRow() int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	scrollbackSize := len(b.scrollback)

	// If no scrollback, no boundary to show
	if scrollbackSize == 0 {
		return -1
	}

	effectiveRows := b.EffectiveRows()

	// Calculate how much of the logical screen is hidden above
	logicalHiddenAbove := 0
	if effectiveRows > b.rows {
		logicalHiddenAbove = effectiveRows - b.rows
	}

	// Normal boundary calculation
	boundaryRow := b.scrollOffset - logicalHiddenAbove

	// Boundary must be within visible area (0 to rows-1)
	if boundaryRow <= 0 || boundaryRow >= b.rows {
		return -1
	}

	// Magnetic zone: suppress boundary when it would appear in the first few rows
	// This creates the "sticky" feel at the transition from logical screen to scrollback
	if boundaryRow <= ScrollMagneticThreshold {
		return -1
	}

	return boundaryRow
}

// GetCursorVisiblePosition returns the visible (x, y) position of the cursor
// accounting for scroll offset. Returns (-1, -1) if the cursor is not currently visible.
func (b *Buffer) GetCursorVisiblePosition() (x, y int) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	effectiveRows := b.EffectiveRows()

	// Calculate how much of the logical screen is hidden above
	logicalHiddenAbove := 0
	if effectiveRows > b.rows {
		logicalHiddenAbove = effectiveRows - b.rows
	}

	// The cursor is at logical position (cursorX, cursorY)
	// Its visible position is: cursorY - logicalHiddenAbove + scrollOffset
	visibleY := b.cursorY - logicalHiddenAbove + b.scrollOffset

	// Check if cursor is within visible vertical area
	if visibleY < 0 || visibleY >= b.rows {
		return -1, -1
	}

	// X position needs to account for horizontal scroll
	visibleX := b.cursorX - b.horizOffset

	// Check if cursor is within visible horizontal area
	if visibleX < 0 || visibleX >= b.cols {
		return -1, -1
	}

	return visibleX, visibleY
}

// SetHorizOffset sets the horizontal scroll offset
func (b *Buffer) SetHorizOffset(offset int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if offset < 0 {
		offset = 0
	}
	b.horizOffset = offset
	b.markDirty()
}

// GetHorizOffset returns current horizontal scroll offset
func (b *Buffer) GetHorizOffset() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.horizOffset
}

// SetScrollbackDisabled enables or disables scrollback accumulation.
// When disabled, lines scrolling off the top are discarded instead of saved.
// Existing scrollback is preserved but inaccessible until re-enabled.
func (b *Buffer) SetScrollbackDisabled(disabled bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.scrollbackDisabled = disabled
	// Reset scroll offset when disabling to prevent viewing hidden scrollback
	if disabled && b.scrollOffset > 0 {
		b.scrollOffset = 0
	}
	b.markDirty()
}

// IsScrollbackDisabled returns true if scrollback accumulation is disabled
func (b *Buffer) IsScrollbackDisabled() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.scrollbackDisabled
}

// GetLongestLineOnScreen returns the length of the longest line currently on the logical screen
func (b *Buffer) GetLongestLineOnScreen() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	longest := 0
	for _, line := range b.screen {
		if len(line) > longest {
			longest = len(line)
		}
	}
	return longest
}

// GetLongestLineInScrollback returns the length of the longest line in scrollback
func (b *Buffer) GetLongestLineInScrollback() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	longest := 0
	for _, line := range b.scrollback {
		if len(line) > longest {
			longest = len(line)
		}
	}
	return longest
}

// GetLongestLineVisible returns the longest line width currently visible.
// Only includes scrollback width when the yellow dashed boundary line is visible.
func (b *Buffer) GetLongestLineVisible() int {
	// Check if scrollback boundary is visible (don't hold lock during this call)
	boundaryVisible := b.GetScrollbackBoundaryVisibleRow() > 0

	b.mu.RLock()
	defer b.mu.RUnlock()

	longest := 0

	// Only include scrollback width if the boundary is visible
	// (meaning we can actually see scrollback content)
	if boundaryVisible {
		for _, line := range b.scrollback {
			if len(line) > longest {
				longest = len(line)
			}
		}
	}

	// Always include screen content width
	for _, line := range b.screen {
		if len(line) > longest {
			longest = len(line)
		}
	}
	return longest
}

// NeedsHorizScrollbar returns true if there's content beyond the visible width
func (b *Buffer) NeedsHorizScrollbar() bool {
	b.mu.RLock()
	cols := b.cols
	splitWidth := b.splitContentWidth
	currentOffset := b.horizOffset
	b.mu.RUnlock()

	// If already scrolled right, show scrollbar so user can scroll back
	if currentOffset > 0 {
		return true
	}

	// GetLongestLineVisible handles the scrollOffset logic internally:
	// - If scrollOffset == 0: returns logical screen content width only
	// - If scrollOffset > 0: returns max of scrollback and screen content width
	longest := b.GetLongestLineVisible()

	// Also consider split content width (for split regions)
	if splitWidth > longest {
		longest = splitWidth
	}
	return longest > cols
}

// GetMaxHorizOffset returns the maximum horizontal scroll offset
func (b *Buffer) GetMaxHorizOffset() int {
	b.mu.RLock()
	cols := b.cols
	splitWidth := b.splitContentWidth
	currentOffset := b.horizOffset
	b.mu.RUnlock()

	// GetLongestLineVisible handles the scrollOffset logic internally:
	// - If scrollOffset == 0: returns logical screen content width only
	// - If scrollOffset > 0: returns max of scrollback and screen content width
	longest := b.GetLongestLineVisible()

	// Also consider split content width (for split regions)
	if splitWidth > longest {
		longest = splitWidth
	}

	contentBasedMax := 0
	if longest > cols {
		contentBasedMax = longest - cols
	}

	// Preserve current scroll position as valid - don't snap left when
	// scrolling vertically from wide scrollback to narrower logical screen.
	// Once user scrolls left past contentBasedMax, they can't scroll right again.
	if currentOffset > contentBasedMax {
		return currentOffset
	}
	return contentBasedMax
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
	effectiveRows := b.EffectiveRows()
	if b.cursorY >= effectiveRows {
		b.cursorY = effectiveRows - 1
	}
	b.markDirty()
}

// MoveCursorForward moves cursor right n columns
func (b *Buffer) MoveCursorForward(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cursorX += n
	effectiveCols := b.EffectiveCols()
	if b.cursorX >= effectiveCols {
		b.cursorX = effectiveCols - 1
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
	screenLen := len(b.screen)
	for i := 0; i < n && screenLen > 0; i++ {
		if b.cursorY < screenLen-1 {
			copy(b.screen[b.cursorY+1:], b.screen[b.cursorY:screenLen-1])
			copy(b.lineInfos[b.cursorY+1:], b.lineInfos[b.cursorY:screenLen-1])
		}
		b.screen[b.cursorY] = b.makeEmptyLine()
		b.lineInfos[b.cursorY] = b.makeDefaultLineInfo()
	}
	b.markDirty()
}

// DeleteLines deletes n lines at cursor
func (b *Buffer) DeleteLines(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	screenLen := len(b.screen)
	for i := 0; i < n && screenLen > 0; i++ {
		if b.cursorY < screenLen-1 {
			copy(b.screen[b.cursorY:], b.screen[b.cursorY+1:])
			copy(b.lineInfos[b.cursorY:], b.lineInfos[b.cursorY+1:])
		}
		b.screen[screenLen-1] = b.makeEmptyLine()
		b.lineInfos[screenLen-1] = b.makeDefaultLineInfo()
	}
	b.markDirty()
}

// DeleteChars deletes n characters at cursor
func (b *Buffer) DeleteChars(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.cursorY >= len(b.screen) {
		return
	}
	line := b.screen[b.cursorY]
	lineLen := len(line)

	if b.cursorX >= lineLen {
		return // Nothing to delete
	}

	// Shift characters left
	if b.cursorX+n < lineLen {
		copy(line[b.cursorX:], line[b.cursorX+n:])
		b.screen[b.cursorY] = line[:lineLen-n]
	} else {
		// Delete extends past end of line - just truncate
		b.screen[b.cursorY] = line[:b.cursorX]
	}
	b.markDirty()
}

// InsertChars inserts n blank characters at cursor
func (b *Buffer) InsertChars(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.cursorY >= len(b.screen) {
		return
	}

	// Ensure line is long enough
	b.ensureLineLength(b.cursorY, b.cursorX)

	line := b.screen[b.cursorY]
	lineLen := len(line)

	// Create space for new characters
	newCells := make([]Cell, n)
	fillCell := b.currentDefaultCell()
	for i := range newCells {
		newCells[i] = fillCell
	}

	// Insert at cursor position
	if b.cursorX >= lineLen {
		line = append(line, newCells...)
	} else {
		// Make room and insert
		line = append(line[:b.cursorX], append(newCells, line[b.cursorX:]...)...)
	}
	b.screen[b.cursorY] = line
	b.markDirty()
}

// EraseChars erases n characters at cursor (replaces with blanks)
// Does not extend line beyond current length - only erases existing cells
func (b *Buffer) EraseChars(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.cursorY >= len(b.screen) {
		return
	}

	line := b.screen[b.cursorY]
	lineLen := len(line)

	// Only erase existing cells, don't extend line
	if b.cursorX >= lineLen {
		return // Nothing to erase
	}

	endPos := b.cursorX + n
	if endPos > lineLen {
		endPos = lineLen
	}

	fillCell := b.currentDefaultCell()
	for i := b.cursorX; i < endPos; i++ {
		line[i] = fillCell
	}
	b.markDirty()
}

// screenToBufferY converts a screen Y coordinate to a buffer-absolute Y coordinate
// Buffer-absolute coordinates: Y=0 is the oldest scrollback line, increasing toward current
func (b *Buffer) screenToBufferY(screenY int) int {
	scrollbackSize := len(b.scrollback)
	effectiveRows := b.EffectiveRows()

	// Calculate how much of the logical screen is hidden above
	logicalHiddenAbove := 0
	if effectiveRows > b.rows {
		logicalHiddenAbove = effectiveRows - b.rows
	}

	// Total scrollable area above visible
	totalScrollableAbove := scrollbackSize + logicalHiddenAbove

	// Convert screen Y to buffer-absolute Y
	return totalScrollableAbove - b.scrollOffset + screenY
}

// bufferToScreenY converts a buffer-absolute Y coordinate to a screen Y coordinate
// Returns -1 if the buffer Y is not currently visible on screen
func (b *Buffer) bufferToScreenY(bufferY int) int {
	scrollbackSize := len(b.scrollback)
	effectiveRows := b.EffectiveRows()

	// Calculate how much of the logical screen is hidden above
	logicalHiddenAbove := 0
	if effectiveRows > b.rows {
		logicalHiddenAbove = effectiveRows - b.rows
	}

	// Total scrollable area above visible
	totalScrollableAbove := scrollbackSize + logicalHiddenAbove

	// Convert buffer-absolute Y to screen Y
	screenY := bufferY - totalScrollableAbove + b.scrollOffset

	// Check if visible
	if screenY < 0 || screenY >= b.rows {
		return -1
	}
	return screenY
}

// StartSelection begins a text selection (coordinates are screen-relative)
func (b *Buffer) StartSelection(x, y int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.selectionActive = true
	// Convert to buffer-absolute coordinates for stable selection
	bufferY := b.screenToBufferY(y)
	b.selStartX = x
	b.selStartY = bufferY
	b.selEndX = x
	b.selEndY = bufferY
	b.markDirty()
}

// UpdateSelection updates the end point of the selection (coordinates are screen-relative)
func (b *Buffer) UpdateSelection(x, y int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.selectionActive {
		return
	}
	// Convert to buffer-absolute coordinates
	bufferY := b.screenToBufferY(y)
	b.selEndX = x
	b.selEndY = bufferY
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

// GetSelection returns the normalized selection bounds in buffer-absolute coordinates
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

// IsCellInSelection checks if a cell at screen coordinates is within the selection
func (b *Buffer) IsCellInSelection(screenX, screenY int) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.selectionActive {
		return false
	}

	// Convert screen Y to buffer-absolute Y
	bufferY := b.screenToBufferY(screenY)

	// Get normalized selection bounds
	sx, sy := b.selStartX, b.selStartY
	ex, ey := b.selEndX, b.selEndY
	if sy > ey || (sy == ey && sx > ex) {
		sx, sy, ex, ey = ex, ey, sx, sy
	}

	// Check if the cell is within the selection
	if bufferY < sy || bufferY > ey {
		return false
	}
	if bufferY == sy && screenX < sx {
		return false
	}
	if bufferY == ey && screenX > ex {
		return false
	}
	return true
}

// getCellByAbsoluteY gets a cell using buffer-absolute Y coordinate
func (b *Buffer) getCellByAbsoluteY(x, bufferY int) Cell {
	scrollbackSize := len(b.scrollback)

	if bufferY < 0 {
		return b.screenInfo.DefaultCell
	}

	if bufferY < scrollbackSize {
		// In scrollback
		return b.getScrollbackCell(x, bufferY)
	}

	// In logical screen
	logicalY := bufferY - scrollbackSize
	return b.getLogicalCell(x, logicalY)
}

// GetSelectedText returns the text in the current selection
func (b *Buffer) GetSelectedText() string {
	sx, sy, ex, ey, active := b.GetSelection()
	if !active {
		return ""
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	// Calculate total buffer height for bounds checking
	scrollbackSize := len(b.scrollback)
	effectiveRows := b.EffectiveRows()
	totalBufferHeight := scrollbackSize + effectiveRows

	var lines []string
	for bufferY := sy; bufferY <= ey && bufferY < totalBufferHeight; bufferY++ {
		startX := 0
		endX := b.cols
		if bufferY == sy {
			startX = sx
		}
		if bufferY == ey {
			endX = ex + 1
		}
		var lineRunes []rune
		for x := startX; x < endX && x < b.cols; x++ {
			cell := b.getCellByAbsoluteY(x, bufferY)
			lineRunes = append(lineRunes, cell.Char)
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

// IsInSelection returns true if the given screen position is within the selection
// Deprecated: Use IsCellInSelection for clearer semantics
func (b *Buffer) IsInSelection(x, y int) bool {
	return b.IsCellInSelection(x, y)
}

// SelectAll selects all text in the terminal (including scrollback)
func (b *Buffer) SelectAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.selectionActive = true
	b.selStartX = 0
	b.selStartY = 0 // Buffer-absolute 0 = oldest scrollback line
	b.selEndX = b.cols - 1
	// End at the last line of the logical screen
	scrollbackSize := len(b.scrollback)
	effectiveRows := b.EffectiveRows()
	b.selEndY = scrollbackSize + effectiveRows - 1
	b.markDirty()
}

// SetLineAttribute sets the display attribute for the current line
func (b *Buffer) SetLineAttribute(attr LineAttribute) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cursorY >= 0 && b.cursorY < len(b.lineInfos) {
		b.lineInfos[b.cursorY].Attribute = attr
		b.markDirty()
	}
}

// GetLineAttribute returns the display attribute for the specified line
func (b *Buffer) GetLineAttribute(y int) LineAttribute {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if y >= 0 && y < len(b.lineInfos) {
		return b.lineInfos[y].Attribute
	}
	return LineAttrNormal
}

// GetLineInfo returns the full LineInfo for the specified line
func (b *Buffer) GetLineInfo(y int) LineInfo {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if y >= 0 && y < len(b.lineInfos) {
		return b.lineInfos[y]
	}
	return DefaultLineInfo()
}

// GetVisibleLineAttribute returns the line attribute accounting for scroll offset
func (b *Buffer) GetVisibleLineAttribute(y int) LineAttribute {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.getVisibleLineInfoInternal(y).Attribute
}

// GetVisibleLineInfo returns the full LineInfo accounting for scroll offset
func (b *Buffer) GetVisibleLineInfo(y int) LineInfo {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.getVisibleLineInfoInternal(y)
}

func (b *Buffer) getVisibleLineInfoInternal(y int) LineInfo {
	if y < 0 || y >= b.rows {
		return LineInfo{Attribute: LineAttrNormal, DefaultCell: b.screenInfo.DefaultCell}
	}

	effectiveRows := b.EffectiveRows()
	scrollbackSize := len(b.scrollback)

	// Calculate how much of the logical screen is hidden above
	logicalHiddenAbove := 0
	if effectiveRows > b.rows {
		logicalHiddenAbove = effectiveRows - b.rows
	}

	totalScrollableAbove := scrollbackSize + logicalHiddenAbove

	if b.scrollOffset == 0 {
		// Not scrolled - show bottom of logical screen
		logicalY := logicalHiddenAbove + y
		if logicalY >= 0 && logicalY < len(b.lineInfos) {
			return b.lineInfos[logicalY]
		}
		return LineInfo{Attribute: LineAttrNormal, DefaultCell: b.screenInfo.DefaultCell}
	}

	// Scrolled - map to scrollback or logical screen
	absoluteY := totalScrollableAbove - b.scrollOffset + y

	if absoluteY < scrollbackSize {
		// In scrollback
		if absoluteY >= 0 && absoluteY < len(b.scrollbackInfo) {
			return b.scrollbackInfo[absoluteY]
		}
		return LineInfo{Attribute: LineAttrNormal, DefaultCell: b.screenInfo.DefaultCell}
	}

	// In logical screen
	logicalY := absoluteY - scrollbackSize
	if logicalY >= 0 && logicalY < len(b.lineInfos) {
		return b.lineInfos[logicalY]
	}
	return LineInfo{Attribute: LineAttrNormal, DefaultCell: b.screenInfo.DefaultCell}
}

// --- Screen Scaling Mode Methods ---

// Set132ColumnMode enables or disables 132-column mode (horizontal scale 0.6060)
// This corresponds to DECCOLM (ESC [ ? 3 h / ESC [ ? 3 l)
func (b *Buffer) Set132ColumnMode(enabled bool) {
	b.mu.Lock()
	b.columnMode132 = enabled
	b.markDirty()
	b.mu.Unlock()
	b.notifyScaleChange()
}

// Get132ColumnMode returns whether 132-column mode is enabled
func (b *Buffer) Get132ColumnMode() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.columnMode132
}

// Set40ColumnMode enables or disables 40-column mode (horizontal scale 2.0)
// This is a custom extension
func (b *Buffer) Set40ColumnMode(enabled bool) {
	b.mu.Lock()
	b.columnMode40 = enabled
	b.markDirty()
	b.mu.Unlock()
	b.notifyScaleChange()
}

// Get40ColumnMode returns whether 40-column mode is enabled
func (b *Buffer) Get40ColumnMode() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.columnMode40
}

// SetLineDensity sets the line density (vertical scaling)
// Valid values: 25 (default), 30, 43, 50, 60
// Higher density = more lines in same space = smaller vertical scale
func (b *Buffer) SetLineDensity(density int) {
	b.mu.Lock()
	// Validate density
	switch density {
	case 25, 30, 43, 50, 60:
		b.lineDensity = density
	default:
		b.lineDensity = 25 // Default to 25 if invalid
	}
	b.markDirty()
	b.mu.Unlock()
	b.notifyScaleChange()
}

// GetLineDensity returns the current line density
func (b *Buffer) GetLineDensity() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.lineDensity == 0 {
		return 25 // Default
	}
	return b.lineDensity
}

// GetHorizontalScale returns the combined horizontal scaling factor
// 132-column mode: 0.6060, 40-column mode: 2.0
// If both enabled: 0.6060 * 2.0 = 1.212
func (b *Buffer) GetHorizontalScale() float64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	scale := 1.0
	if b.columnMode132 {
		scale *= 0.6060
	}
	if b.columnMode40 {
		scale *= 2.0
	}
	return scale
}

// GetVerticalScale returns the vertical scaling factor based on line density
// density 25 (default) = scale 1.0
// density 30 = scale 25/30 = 0.8333
// density 43 = scale 25/43 = 0.5814
// density 50 = scale 25/50 = 0.5
// density 60 = scale 25/60 = 0.4167
func (b *Buffer) GetVerticalScale() float64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	density := b.lineDensity
	if density == 0 || density == 25 {
		return 1.0
	}
	return 25.0 / float64(density)
}

// --- Custom Glyph System Methods ---

// SetBGP sets the Base Glyph Palette for subsequent characters
// -1 means use the foreground color code as the palette number
func (b *Buffer) SetBGP(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentBGP = n
}

// ResetBGP resets the Base Glyph Palette to default (-1)
func (b *Buffer) ResetBGP() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentBGP = -1
}

// GetBGP returns the current Base Glyph Palette setting
func (b *Buffer) GetBGP() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.currentBGP
}

// SetXFlip sets the horizontal flip attribute for subsequent characters
func (b *Buffer) SetXFlip(on bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentXFlip = on
}

// GetXFlip returns the current horizontal flip setting
func (b *Buffer) GetXFlip() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.currentXFlip
}

// SetYFlip sets the vertical flip attribute for subsequent characters
func (b *Buffer) SetYFlip(on bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentYFlip = on
}

// GetYFlip returns the current vertical flip setting
func (b *Buffer) GetYFlip() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.currentYFlip
}

// --- Palette Management ---

// DeleteAllPalettes removes all custom palettes
func (b *Buffer) DeleteAllPalettes() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.palettes = make(map[int]*Palette)
	b.markDirty()
}

// DeletePalette removes a specific palette
func (b *Buffer) DeletePalette(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.palettes, n)
	b.markDirty()
}

// InitPalette creates or reinitializes a palette with the specified number of entries
func (b *Buffer) InitPalette(n int, length int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.palettes[n] = NewPalette(length)
	b.markDirty()
}

// SetPaletteEntry sets a single entry in a palette
// colorCode uses SGR-style: 30-37/40-47 (normal), 90-97/100-107 (bright), 8 (transparent), 9 (default fg)
// If dim is true, the color is a dim variant
func (b *Buffer) SetPaletteEntry(paletteNum int, idx int, colorCode int, dim bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	palette, ok := b.palettes[paletteNum]
	if !ok {
		return // Palette doesn't exist
	}
	if idx < 0 || idx >= len(palette.Entries) {
		return // Index out of bounds
	}

	entry := &palette.Entries[idx]
	entry.Dim = dim

	switch colorCode {
	case 8:
		entry.Type = PaletteEntryTransparent
		palette.UsesBg = true // Track for cache key optimization
	case 9:
		entry.Type = PaletteEntryDefaultFG
		palette.UsesDefaultFG = true // Track for cache key optimization
	default:
		entry.Type = PaletteEntryColor
		// Map SGR color codes to actual colors
		// 30-37, 40-47 -> ANSI 0-7
		// 90-97, 100-107 -> ANSI 8-15 (bright)
		var colorIdx int
		if colorCode >= 30 && colorCode <= 37 {
			colorIdx = colorCode - 30
		} else if colorCode >= 40 && colorCode <= 47 {
			colorIdx = colorCode - 40
		} else if colorCode >= 90 && colorCode <= 97 {
			colorIdx = colorCode - 90 + 8
		} else if colorCode >= 100 && colorCode <= 107 {
			colorIdx = colorCode - 100 + 8
		} else {
			// Unknown color code, default to white
			colorIdx = 7
		}
		if colorIdx >= 0 && colorIdx < len(ANSIColors) {
			entry.Color = ANSIColors[colorIdx]
		}
	}

	b.markDirty()
}

// SetPaletteEntryColor sets a palette entry directly from a Color value
// Use this for 256-color and true color palette entries
func (b *Buffer) SetPaletteEntryColor(paletteNum int, idx int, color Color, dim bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	palette, ok := b.palettes[paletteNum]
	if !ok {
		return // Palette doesn't exist
	}
	if idx < 0 || idx >= len(palette.Entries) {
		return // Index out of bounds
	}

	entry := &palette.Entries[idx]
	entry.Type = PaletteEntryColor
	entry.Color = color
	entry.Dim = dim
	b.markDirty()
}

// GetPalette returns a palette by number, or nil if it doesn't exist
func (b *Buffer) GetPalette(n int) *Palette {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.palettes[n]
}

// --- Custom Glyph Management ---

// DeleteAllGlyphs removes all custom glyph definitions
func (b *Buffer) DeleteAllGlyphs() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.customGlyphs = make(map[rune]*CustomGlyph)
	b.markDirty()
}

// DeleteGlyph removes a specific custom glyph definition
func (b *Buffer) DeleteGlyph(r rune) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.customGlyphs, r)
	b.markDirty()
}

// SetGlyph defines a custom glyph for a rune
// width is the pixel width, pixels are palette indices (row by row, left to right)
// height is automatically calculated from len(pixels)/width
func (b *Buffer) SetGlyph(r rune, width int, pixels []int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.customGlyphs[r] = NewCustomGlyph(width, pixels)
	b.markDirty()
}

// GetGlyph returns the custom glyph for a rune, or nil if none defined
func (b *Buffer) GetGlyph(r rune) *CustomGlyph {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.customGlyphs[r]
}

// HasCustomGlyph returns true if a custom glyph is defined for the rune
func (b *Buffer) HasCustomGlyph(r rune) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.customGlyphs[r]
	return ok
}

// ResolveGlyphColor resolves a palette index to an actual color for rendering
// cell is the cell being rendered, paletteIdx is the pixel's palette index
// Returns the color to use and whether the pixel should be rendered (false = transparent)
func (b *Buffer) ResolveGlyphColor(cell *Cell, paletteIdx int) (Color, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// Determine which palette to use
	paletteNum := cell.BGP
	if paletteNum < 0 {
		// Use foreground color code as palette number
		// We need to map the cell's foreground color back to a color code
		// This is a simplification - we use the ANSI color index if possible
		paletteNum = b.ColorToANSICode(cell.Foreground)
	}

	palette := b.palettes[paletteNum]

	// Case 1: Palette doesn't exist - use fallback rendering
	if palette == nil {
		return b.fallbackGlyphColor(cell, paletteIdx)
	}

	// Case 2: Single entry palette - 0=background, 1+=entry
	if len(palette.Entries) == 1 {
		if paletteIdx == 0 {
			return cell.Background, true // Use cell background
		}
		entry := &palette.Entries[0]
		return b.resolveEntry(entry, cell)
	}

	// Case 3: Multi-entry palette - use as-is, clamp out of range
	if paletteIdx < 0 {
		paletteIdx = 0
	}
	if paletteIdx >= len(palette.Entries) {
		paletteIdx = len(palette.Entries) - 1
	}
	entry := &palette.Entries[paletteIdx]
	return b.resolveEntry(entry, cell)
}

// fallbackGlyphColor provides colors when no palette exists
// 0 = background, 1 = foreground, 2 = dim foreground, 3+ = bright foreground
func (b *Buffer) fallbackGlyphColor(cell *Cell, paletteIdx int) (Color, bool) {
	switch paletteIdx {
	case 0:
		return cell.Background, true
	case 1:
		return cell.Foreground, true
	case 2:
		// Dim variant - darken the foreground
		return Color{
			R: uint8(float64(cell.Foreground.R) * 0.6),
			G: uint8(float64(cell.Foreground.G) * 0.6),
			B: uint8(float64(cell.Foreground.B) * 0.6),
		}, true
	default:
		// Bright variant - lighten the foreground
		return Color{
			R: uint8(min(255, int(cell.Foreground.R)+64)),
			G: uint8(min(255, int(cell.Foreground.G)+64)),
			B: uint8(min(255, int(cell.Foreground.B)+64)),
		}, true
	}
}

// resolveEntry converts a palette entry to a color
func (b *Buffer) resolveEntry(entry *PaletteEntry, cell *Cell) (Color, bool) {
	switch entry.Type {
	case PaletteEntryTransparent:
		return cell.Background, true
	case PaletteEntryDefaultFG:
		if entry.Dim {
			return Color{
				R: uint8(float64(cell.Foreground.R) * 0.6),
				G: uint8(float64(cell.Foreground.G) * 0.6),
				B: uint8(float64(cell.Foreground.B) * 0.6),
			}, true
		}
		return cell.Foreground, true
	default:
		color := entry.Color
		if entry.Dim {
			color = Color{
				R: uint8(float64(color.R) * 0.6),
				G: uint8(float64(color.G) * 0.6),
				B: uint8(float64(color.B) * 0.6),
			}
		}
		return color, true
	}
}

// ColorToANSICode attempts to map a color back to an ANSI color code.
// Returns 37 (white) as default if no match.
func (b *Buffer) ColorToANSICode(c Color) int {
	// Check against ANSI colors
	for i, ansi := range ANSIColors {
		if c.R == ansi.R && c.G == ansi.G && c.B == ansi.B {
			if i < 8 {
				return 30 + i
			}
			return 90 + (i - 8)
		}
	}
	return 37 // Default to white
}

// --- Sprite Overlay System Methods ---

// SetSpriteUnits sets how many subdivisions per cell for sprite coordinates
func (b *Buffer) SetSpriteUnits(unitX, unitY int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if unitX > 0 {
		b.spriteUnitX = unitX
	}
	if unitY > 0 {
		b.spriteUnitY = unitY
	}
	b.markDirty()
}

// GetSpriteUnits returns the subdivisions per cell for sprite coordinates
func (b *Buffer) GetSpriteUnits() (unitX, unitY int) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.spriteUnitX, b.spriteUnitY
}

// DeleteAllSprites removes all sprites
func (b *Buffer) DeleteAllSprites() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sprites = make(map[int]*Sprite)
	b.markDirty()
}

// DeleteSprite removes a specific sprite
func (b *Buffer) DeleteSprite(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.sprites, id)
	b.markDirty()
}

// SetSprite creates or updates a sprite
func (b *Buffer) SetSprite(id int, x, y float64, zIndex, fgp, flipCode int, xScale, yScale float64, cropRect int, runes []rune) {
	b.mu.Lock()
	defer b.mu.Unlock()

	sprite := NewSprite(id)
	sprite.X = x
	sprite.Y = y
	sprite.ZIndex = zIndex
	sprite.FGP = fgp
	sprite.FlipCode = flipCode
	sprite.XScale = xScale
	sprite.YScale = yScale
	sprite.CropRect = cropRect
	sprite.SetRunes(runes)

	b.sprites[id] = sprite
	b.markDirty()
}

// GetSprite returns a sprite by ID, or nil if not found
func (b *Buffer) GetSprite(id int) *Sprite {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.sprites[id]
}

// MoveSprite updates only the position of an existing sprite
// Returns false if sprite doesn't exist
func (b *Buffer) MoveSprite(id int, x, y float64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	sprite := b.sprites[id]
	if sprite == nil {
		return false
	}
	sprite.X = x
	sprite.Y = y
	b.markDirty()
	return true
}

// UpdateSpriteRunes updates only the runes of an existing sprite
// Returns false if sprite doesn't exist
func (b *Buffer) UpdateSpriteRunes(id int, runes []rune) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	sprite := b.sprites[id]
	if sprite == nil {
		return false
	}
	sprite.SetRunes(runes)
	b.markDirty()
	return true
}

// MoveSpriteAndRunes updates position and runes of an existing sprite
// Returns false if sprite doesn't exist
func (b *Buffer) MoveSpriteAndRunes(id int, x, y float64, runes []rune) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	sprite := b.sprites[id]
	if sprite == nil {
		return false
	}
	sprite.X = x
	sprite.Y = y
	sprite.SetRunes(runes)
	b.markDirty()
	return true
}

// GetSpritesForRendering returns sprites sorted by Z-index and ID for rendering
// Returns two slices: behind (negative Z) and front (non-negative Z)
func (b *Buffer) GetSpritesForRendering() (behind, front []*Sprite) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	behind = make([]*Sprite, 0)
	front = make([]*Sprite, 0)

	for _, sprite := range b.sprites {
		if sprite.ZIndex < 0 {
			behind = append(behind, sprite)
		} else {
			front = append(front, sprite)
		}
	}

	// Sort by Z-index, then by ID
	sortSprites := func(sprites []*Sprite) {
		for i := 0; i < len(sprites); i++ {
			for j := i + 1; j < len(sprites); j++ {
				if sprites[i].ZIndex > sprites[j].ZIndex ||
					(sprites[i].ZIndex == sprites[j].ZIndex && sprites[i].ID > sprites[j].ID) {
					sprites[i], sprites[j] = sprites[j], sprites[i]
				}
			}
		}
	}

	sortSprites(behind)
	sortSprites(front)

	return behind, front
}

// --- Crop Rectangle Methods ---

// DeleteAllCropRects removes all crop rectangles
func (b *Buffer) DeleteAllCropRects() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cropRects = make(map[int]*CropRectangle)
	b.markDirty()
}

// DeleteCropRect removes a specific crop rectangle
func (b *Buffer) DeleteCropRect(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.cropRects, id)
	b.markDirty()
}

// SetCropRect creates or updates a crop rectangle
func (b *Buffer) SetCropRect(id int, minX, minY, maxX, maxY float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cropRects[id] = NewCropRectangle(id, minX, minY, maxX, maxY)
	b.markDirty()
}

// GetCropRect returns a crop rectangle by ID, or nil if not found
func (b *Buffer) GetCropRect(id int) *CropRectangle {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.cropRects[id]
}

// --- Screen Crop Methods ---

// SetScreenCrop sets the width and height crop in sprite coordinate units.
// -1 means no crop for that dimension.
func (b *Buffer) SetScreenCrop(widthCrop, heightCrop int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.widthCrop = widthCrop
	b.heightCrop = heightCrop
	b.markDirty()
}

// GetScreenCrop returns the current width and height crop values.
// -1 means no crop for that dimension.
func (b *Buffer) GetScreenCrop() (widthCrop, heightCrop int) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.widthCrop, b.heightCrop
}

// ClearScreenCrop removes both width and height crops.
func (b *Buffer) ClearScreenCrop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.widthCrop = -1
	b.heightCrop = -1
	b.markDirty()
}

// --- Screen Split Methods ---

// DeleteAllScreenSplits removes all screen splits.
func (b *Buffer) DeleteAllScreenSplits() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.screenSplits = make(map[int]*ScreenSplit)
	b.markDirty()
}

// DeleteScreenSplit removes a specific screen split by ID.
func (b *Buffer) DeleteScreenSplit(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.screenSplits, id)
	b.markDirty()
}

// SetScreenSplit creates or updates a screen split.
// screenY: Y coordinate in sprite units where this split begins on screen
// bufferRow, bufferCol: 0-indexed logical screen coordinates to draw from
// topFineScroll, leftFineScroll: 0 to (subdivisions-1), higher = more clipped
// charWidthScale: character width multiplier (0 = inherit)
// lineDensity: line density override (0 = inherit)
func (b *Buffer) SetScreenSplit(id int, screenY, bufferRow, bufferCol, topFineScroll, leftFineScroll int, charWidthScale float64, lineDensity int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Clamp fine scroll values
	if topFineScroll < 0 {
		topFineScroll = 0
	}
	if topFineScroll >= b.spriteUnitY {
		topFineScroll = b.spriteUnitY - 1
	}
	if leftFineScroll < 0 {
		leftFineScroll = 0
	}
	if leftFineScroll >= b.spriteUnitX {
		leftFineScroll = b.spriteUnitX - 1
	}

	b.screenSplits[id] = &ScreenSplit{
		ScreenY:        screenY,
		BufferRow:      bufferRow,
		BufferCol:      bufferCol,
		TopFineScroll:  topFineScroll,
		LeftFineScroll: leftFineScroll,
		CharWidthScale: charWidthScale,
		LineDensity:    lineDensity,
	}
	b.markDirty()
}

// GetScreenSplit returns a screen split by ID, or nil if not found.
func (b *Buffer) GetScreenSplit(id int) *ScreenSplit {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.screenSplits[id]
}

// GetScreenSplitsSorted returns all screen splits sorted by ScreenY coordinate.
func (b *Buffer) GetScreenSplitsSorted() []*ScreenSplit {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if len(b.screenSplits) == 0 {
		return nil
	}

	// Collect all splits
	splits := make([]*ScreenSplit, 0, len(b.screenSplits))
	for _, split := range b.screenSplits {
		splits = append(splits, split)
	}

	// Sort by ScreenY
	for i := 0; i < len(splits)-1; i++ {
		for j := i + 1; j < len(splits); j++ {
			if splits[j].ScreenY < splits[i].ScreenY {
				splits[i], splits[j] = splits[j], splits[i]
			}
		}
	}

	return splits
}

// GetCellForSplit returns a cell for split rendering.
// screenX/screenY: position within the split region (0 = first cell of split)
// bufferRow/bufferCol: buffer offset for this split (0-indexed)
// The cell is fetched from the logical screen at position (screenX + bufferCol, screenY + bufferRow)
// accounting for the current scroll offset.
func (b *Buffer) GetCellForSplit(screenX, screenY, bufferRow, bufferCol int) Cell {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// Calculate actual buffer position
	actualX := screenX + bufferCol
	actualY := screenY + bufferRow

	if actualY < 0 || actualY >= b.rows {
		return b.screenInfo.DefaultCell
	}

	effectiveRows := b.EffectiveRows()
	scrollbackSize := len(b.scrollback)

	// Calculate how much of the logical screen is hidden above
	logicalHiddenAbove := 0
	if effectiveRows > b.rows {
		logicalHiddenAbove = effectiveRows - b.rows
	}

	// Total scrollable area above visible
	totalScrollableAbove := scrollbackSize + logicalHiddenAbove

	if b.scrollOffset == 0 {
		// Not scrolled - show bottom of logical screen
		logicalY := logicalHiddenAbove + actualY
		return b.getLogicalCell(actualX, logicalY)
	}

	// Scrolled up
	absoluteY := totalScrollableAbove - b.scrollOffset + actualY

	if absoluteY < scrollbackSize {
		return b.getScrollbackCell(actualX, absoluteY)
	}

	logicalY := absoluteY - scrollbackSize
	return b.getLogicalCell(actualX, logicalY)
}

// GetLineAttributeForSplit returns the line attribute for split rendering.
func (b *Buffer) GetLineAttributeForSplit(screenY, bufferRow int) LineAttribute {
	b.mu.RLock()
	defer b.mu.RUnlock()

	actualY := screenY + bufferRow

	if actualY < 0 || actualY >= b.rows {
		return LineAttrNormal
	}

	effectiveRows := b.EffectiveRows()
	scrollbackSize := len(b.scrollback)

	logicalHiddenAbove := 0
	if effectiveRows > b.rows {
		logicalHiddenAbove = effectiveRows - b.rows
	}

	totalScrollableAbove := scrollbackSize + logicalHiddenAbove

	if b.scrollOffset == 0 {
		logicalY := logicalHiddenAbove + actualY
		if logicalY >= 0 && logicalY < len(b.lineInfos) {
			return b.lineInfos[logicalY].Attribute
		}
		return LineAttrNormal
	}

	absoluteY := totalScrollableAbove - b.scrollOffset + actualY

	if absoluteY < scrollbackSize {
		// Scrollback lines don't have special attributes
		return LineAttrNormal
	}

	logicalY := absoluteY - scrollbackSize
	if logicalY >= 0 && logicalY < len(b.lineInfos) {
		return b.lineInfos[logicalY].Attribute
	}
	return LineAttrNormal
}

// GetLineLengthForSplit returns the effective content length for a split row.
// This is the line length minus the BufferCol offset (content before BufferCol is excluded).
// Used to know when to stop rendering (no more content on line).
func (b *Buffer) GetLineLengthForSplit(screenY, bufferRow, bufferCol int) int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	actualY := screenY + bufferRow

	if actualY < 0 || actualY >= b.rows {
		return 0
	}

	effectiveRows := b.EffectiveRows()
	scrollbackSize := len(b.scrollback)

	logicalHiddenAbove := 0
	if effectiveRows > b.rows {
		logicalHiddenAbove = effectiveRows - b.rows
	}

	totalScrollableAbove := scrollbackSize + logicalHiddenAbove

	var lineLen int
	if b.scrollOffset == 0 {
		logicalY := logicalHiddenAbove + actualY
		if logicalY >= 0 && logicalY < len(b.screen) {
			lineLen = len(b.screen[logicalY])
		}
	} else {
		absoluteY := totalScrollableAbove - b.scrollOffset + actualY
		if absoluteY < scrollbackSize {
			if absoluteY >= 0 && absoluteY < len(b.scrollback) {
				lineLen = len(b.scrollback[absoluteY])
			}
		} else {
			logicalY := absoluteY - scrollbackSize
			if logicalY >= 0 && logicalY < len(b.screen) {
				lineLen = len(b.screen[logicalY])
			}
		}
	}

	// Subtract the BufferCol offset - content before that is excluded from this split
	effectiveLen := lineLen - bufferCol
	if effectiveLen < 0 {
		return 0
	}
	return effectiveLen
}

// SetSplitContentWidth sets the max content width found across all split regions.
// This is called by the renderer after processing splits and is used for horizontal
// scrollbar calculation independent from scrollback content.
func (b *Buffer) SetSplitContentWidth(width int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.splitContentWidth = width
}

// GetSplitContentWidth returns the max content width found across all split regions.
// Returns 0 if no splits are active or have content.
func (b *Buffer) GetSplitContentWidth() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.splitContentWidth
}

// ResolveSpriteGlyphColor resolves a palette index to a color for sprite rendering
// Similar to ResolveGlyphColor but uses sprite's FGP and handles transparency differently
// Returns the color and whether the pixel should be rendered (false = transparent)
func (b *Buffer) ResolveSpriteGlyphColor(fgp int, paletteIdx int, defaultFg, defaultBg Color) (Color, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// Determine which palette to use
	paletteNum := fgp
	if paletteNum < 0 {
		// Use a default palette number (37 = white foreground)
		paletteNum = 37
	}

	palette := b.palettes[paletteNum]

	// Case 1: Palette doesn't exist - use fallback rendering
	if palette == nil {
		return b.fallbackSpriteColor(paletteIdx, defaultFg, defaultBg)
	}

	// Case 2: Single entry palette - 0=transparent, 1+=entry
	if len(palette.Entries) == 1 {
		if paletteIdx == 0 {
			return Color{}, false // Transparent
		}
		entry := &palette.Entries[0]
		return b.resolveSpriteEntry(entry, defaultFg, defaultBg)
	}

	// Case 3: Multi-entry palette - use as-is, clamp out of range
	if paletteIdx < 0 {
		paletteIdx = 0
	}
	if paletteIdx >= len(palette.Entries) {
		paletteIdx = len(palette.Entries) - 1
	}
	entry := &palette.Entries[paletteIdx]
	return b.resolveSpriteEntry(entry, defaultFg, defaultBg)
}

// fallbackSpriteColor provides colors for sprites when no palette exists
// 0 = transparent, 1 = foreground, 2 = dim foreground, 3+ = bright foreground
func (b *Buffer) fallbackSpriteColor(paletteIdx int, fg, bg Color) (Color, bool) {
	switch paletteIdx {
	case 0:
		return Color{}, false // Transparent
	case 1:
		return fg, true
	case 2:
		// Dim variant
		return Color{
			R: uint8(float64(fg.R) * 0.6),
			G: uint8(float64(fg.G) * 0.6),
			B: uint8(float64(fg.B) * 0.6),
		}, true
	default:
		// Bright variant
		return Color{
			R: uint8(min(255, int(fg.R)+64)),
			G: uint8(min(255, int(fg.G)+64)),
			B: uint8(min(255, int(fg.B)+64)),
		}, true
	}
}

// resolveSpriteEntry converts a palette entry to a color for sprites
func (b *Buffer) resolveSpriteEntry(entry *PaletteEntry, fg, bg Color) (Color, bool) {
	switch entry.Type {
	case PaletteEntryTransparent:
		return Color{}, false // Transparent for sprites
	case PaletteEntryDefaultFG:
		if entry.Dim {
			return Color{
				R: uint8(float64(fg.R) * 0.6),
				G: uint8(float64(fg.G) * 0.6),
				B: uint8(float64(fg.B) * 0.6),
			}, true
		}
		return fg, true
	default:
		color := entry.Color
		if entry.Dim {
			color = Color{
				R: uint8(float64(color.R) * 0.6),
				G: uint8(float64(color.G) * 0.6),
				B: uint8(float64(color.B) * 0.6),
			}
		}
		return color, true
	}
}
