package purfecterm

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// scrollMagneticThresholdPercent is the percentage of total scrollable content
// that forms the magnetic zone at the boundary between logical screen and scrollback.
const scrollMagneticThresholdPercent = 5

// keyboardAutoScrollDuration is how long after keyboard activity the terminal
// will auto-scroll to keep the cursor visible on cursor movements.
const keyboardAutoScrollDuration = 500 * time.Millisecond

// manualScrollCooldown is how long after manual scrolling before auto-scroll
// can resume (if no keyboard activity or scroll-causing event occurs).
const manualScrollCooldown = 5 * time.Second

// HorizMemo stores horizontal scroll memo data for a single scanline.
// This is populated during paint to track cursor position relative to rendered content.
type HorizMemo struct {
	Valid           bool // True if this scanline was processed during paint
	LogicalRow      int  // Which buffer row is being rendered at this scanline (for debugging)
	LeftmostCell    int  // Leftmost rendered cell column number
	RightmostCell   int  // Rightmost rendered cell column number
	DistanceToLeft  int  // Distance to scroll left to reach cursor (-1 if N/A)
	DistanceToRight int  // Distance to scroll right to reach cursor (-1 if N/A)
	CursorLocated   bool // True if cursor was found within rendered area
}

// scrollMagneticThresholdMin is the minimum magnetic threshold in lines.
const scrollMagneticThresholdMin = 2

// scrollMagneticThresholdMax is the maximum magnetic threshold in lines.
const scrollMagneticThresholdMax = 50

// getMagneticThreshold calculates the dynamic magnetic threshold based on
// total scrollable content (scrollback size + logical rows hidden above).
// Returns 5% of total scrollable content, clamped between min and max values.
func (b *Buffer) getMagneticThreshold() int {
	scrollbackSize := len(b.scrollback)
	effectiveRows := b.EffectiveRows()

	// Calculate how much of the logical screen is hidden above
	logicalHiddenAbove := 0
	if effectiveRows > b.rows {
		logicalHiddenAbove = effectiveRows - b.rows
	}

	// Total scrollable area above visible
	totalScrollableAbove := scrollbackSize + logicalHiddenAbove

	// Calculate 5% of total scrollable content
	threshold := totalScrollableAbove * scrollMagneticThresholdPercent / 100

	// Clamp to reasonable bounds
	if threshold < scrollMagneticThresholdMin {
		threshold = scrollMagneticThresholdMin
	}
	if threshold > scrollMagneticThresholdMax {
		threshold = scrollMagneticThresholdMax
	}

	return threshold
}

// getEffectiveScrollOffset returns the scroll offset adjusted for the magnetic zone.
// In the magnetic zone, rendering should behave as if viewing the full logical screen.
// Past the magnetic zone, the threshold is subtracted so content transitions smoothly.
// This must be called with the lock held.
func (b *Buffer) getEffectiveScrollOffset() int {
	effectiveRows := b.EffectiveRows()

	// Calculate how much of the logical screen is hidden above
	logicalHiddenAbove := 0
	if effectiveRows > b.rows {
		logicalHiddenAbove = effectiveRows - b.rows
	}

	// Calculate boundary row (where yellow line would appear without magnetic zone)
	boundaryRow := b.scrollOffset - logicalHiddenAbove

	// If not scrolled into scrollback at all, no adjustment needed
	if boundaryRow <= 0 {
		return b.scrollOffset
	}

	magneticThreshold := b.getMagneticThreshold()

	if boundaryRow <= magneticThreshold {
		// In magnetic zone - render as if viewing full logical screen
		return logicalHiddenAbove
	}

	// Past magnetic zone - subtract threshold so content transitions smoothly
	return b.scrollOffset - magneticThreshold
}

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

	// Auto-scroll to cursor on keyboard activity
	lastKeyboardActivity time.Time // When keyboard activity last occurred
	cursorDrawnLastFrame bool      // Set by widget after drawing cursor
	lastCursorMoveDir    int       // -1=up, 0=none, 1=down (for vertical auto-scroll)
	lastManualVertScroll time.Time // When user last manually scrolled vertically

	// Horizontal auto-scroll tracking
	lastHorizCursorMoveDir  int       // -1=left, 0=unknown, 1=right (for horiz auto-scroll)
	lastManualHorizScroll   time.Time // When user last manually scrolled horizontally
	lastScrollCausingEvent  time.Time // When a scroll-causing event last occurred (line to scrollback)
	horizMemos              []HorizMemo // Per-scanline horizontal scroll memos (populated during paint)
	isAbsoluteHorizPosition bool      // True if last horiz move was absolute (CSI H/f/G)

	// Auto-scroll mode control (DEC Private Mode)
	autoScrollDisabled bool // When true, cursor-following auto-scroll is disabled

	// DECAWM - Auto-wrap mode (DEC Private Mode 7)
	autoWrapMode bool // When true (default), cursor wraps to next line at end of row

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
		autoWrapMode:        true, // DECAWM default enabled
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

	// Calculate logicalHiddenAbove BEFORE resize to track scrollback visibility state
	oldEffectiveRows := b.EffectiveRows()
	oldLogicalHiddenAbove := 0
	if oldEffectiveRows > b.rows {
		oldLogicalHiddenAbove = oldEffectiveRows - b.rows
	}

	// Check if user was viewing scrollback before resize
	// (scrollOffset > logicalHiddenAbove means boundary line would be visible or past it)
	wasViewingScrollback := b.scrollOffset > oldLogicalHiddenAbove

	// When window gets wider, prefer to unscroll horizontally first
	// This reveals hidden columns on the left before showing blank columns on the right
	if cols > b.cols && b.horizOffset > 0 {
		colsAdded := cols - b.cols
		if colsAdded >= b.horizOffset {
			// All hidden columns can be revealed
			b.horizOffset = 0
		} else {
			// Reduce scroll offset by the number of new columns
			b.horizOffset -= colsAdded
		}
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
		b.trackCursorYMove(effectiveRows - 1)
		b.cursorY = effectiveRows - 1
	}

	// Calculate new logicalHiddenAbove after resize
	newLogicalHiddenAbove := 0
	if effectiveRows > rows {
		newLogicalHiddenAbove = effectiveRows - rows
	}

	// Preserve scrollback visibility state:
	// If user was NOT viewing scrollback before, don't let resize reveal scrollback.
	// Instead, allow blank lines at the bottom and favor showing more of the logical screen.
	if !wasViewingScrollback && b.scrollOffset > newLogicalHiddenAbove {
		// Cap scrollOffset so scrollback doesn't become visible
		b.scrollOffset = newLogicalHiddenAbove
	}

	// Also clamp to maximum scroll offset (scrollback + hidden + magnetic threshold)
	maxOffset := b.getMaxScrollOffsetInternal()
	if b.scrollOffset > maxOffset {
		b.scrollOffset = maxOffset
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
			newY := b.cursorY - linesToPush
			if newY < 0 {
				newY = 0
			}
			b.trackCursorYMove(newY)
			b.cursorY = newY
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

	trimmed := false
	if len(b.scrollback) >= b.maxScrollback {
		b.scrollback = b.scrollback[1:]
		b.scrollbackInfo = b.scrollbackInfo[1:]
		trimmed = true
	}
	b.scrollback = append(b.scrollback, line)
	b.scrollbackInfo = append(b.scrollbackInfo, info)

	// If scrollback was trimmed from front and we're scrolled into scrollback,
	// adjust offset to keep viewing the same content
	if trimmed && b.scrollOffset > 0 {
		b.scrollOffset--
	}
	// Note: if user was at scrollOffset 0, they stay at 0 (viewing newest content)
	// If at some other scrollback position, they stay there but see newer lines
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
		b.trackCursorYMove(newEffectiveRows - 1)
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

// trackCursorYMove tracks cursor movement direction for auto-scroll.
// Call this before modifying cursorY with the new Y value.
func (b *Buffer) trackCursorYMove(newY int) {
	if newY > b.cursorY {
		b.lastCursorMoveDir = 1 // Moving down
	} else if newY < b.cursorY {
		b.lastCursorMoveDir = -1 // Moving up
	}
	// If equal, keep previous direction
}

// setHorizMoveDir sets the horizontal cursor movement direction for horizontal auto-scroll.
// dir: -1=left, 0=unknown/absolute, 1=right
// isAbsolute: true if this was an absolute positioning command (CSI H/f/G)
func (b *Buffer) setHorizMoveDir(dir int, isAbsolute bool) {
	b.lastHorizCursorMoveDir = dir
	b.isAbsoluteHorizPosition = isAbsolute
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

	b.trackCursorYMove(y)
	b.setHorizMoveDir(0, true) // Absolute positioning - direction unknown
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

// NotifyKeyboardActivity signals that keyboard input occurred.
// This starts/restarts the auto-scroll timer.
func (b *Buffer) NotifyKeyboardActivity() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastKeyboardActivity = time.Now()
}

// NotifyManualVertScroll signals that the user manually scrolled vertically
// (via mouse wheel, scrollbar, etc). This cancels vertical auto-scroll
// to avoid fighting with user intent.
func (b *Buffer) NotifyManualVertScroll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastManualVertScroll = time.Now()
}

// isVertAutoScrollActive returns true if vertical auto-scroll should be active.
// It checks keyboard activity and whether manual scroll should take precedence.
// Must be called with lock held.
func (b *Buffer) isVertAutoScrollActive() bool {
	// Must have recent keyboard activity
	if b.lastKeyboardActivity.IsZero() {
		return false
	}
	if time.Since(b.lastKeyboardActivity) >= keyboardAutoScrollDuration {
		return false
	}

	// If user manually scrolled more recently than keyboard activity, defer to user
	if !b.lastManualVertScroll.IsZero() && b.lastManualVertScroll.After(b.lastKeyboardActivity) {
		return false
	}

	return true
}

// extendAutoScrollTimer extends the keyboard activity timer when auto-scroll
// is actively working to bring the cursor into view. This allows longer output
// to keep scrolling without the timer expiring mid-scroll.
// Must be called with lock held.
func (b *Buffer) extendAutoScrollTimer() {
	b.lastKeyboardActivity = time.Now()
}

// isAutoScrollActive returns true if keyboard activity occurred recently
// enough that cursor movements should auto-scroll the view.
// Must be called with lock held.
// DEPRECATED: Use isVertAutoScrollActive instead for more accurate behavior.
func (b *Buffer) isAutoScrollActive() bool {
	if b.lastKeyboardActivity.IsZero() {
		return false
	}
	return time.Since(b.lastKeyboardActivity) < keyboardAutoScrollDuration
}

// SetCursorDrawn is called by the widget after rendering to indicate whether
// the cursor was actually drawn on screen.
func (b *Buffer) SetCursorDrawn(drawn bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cursorDrawnLastFrame = drawn
}

// CheckCursorAutoScroll checks if the cursor was not drawn last frame and
// auto-scroll is active, then scrolls toward the cursor position to bring
// the cursor back into view. When the cursor is multiple lines away, it
// scrolls multiple lines at once for faster catch-up.
// Returns true if a scroll occurred.
func (b *Buffer) CheckCursorAutoScroll() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Check if auto-scroll is disabled by DEC Private Mode
	if b.autoScrollDisabled {
		return false
	}

	// Only auto-scroll if keyboard activity is recent and user hasn't manually scrolled
	if !b.isVertAutoScrollActive() {
		return false
	}

	effectiveRows := b.EffectiveRows()
	logicalHiddenAbove := 0
	if effectiveRows > b.rows {
		logicalHiddenAbove = effectiveRows - b.rows
	}

	// FIRST: If we're viewing scrollback (scrollOffset > logicalHiddenAbove),
	// instantly snap to the logical screen boundary. The scrollback should be
	// forced off screen before any gradual auto-scrolling happens.
	if b.scrollOffset > logicalHiddenAbove {
		b.scrollOffset = logicalHiddenAbove
		b.extendAutoScrollTimer() // Extend timer since we're actively scrolling
		b.markDirty()
		return true
	}

	// If cursor was drawn, no scroll needed
	if b.cursorDrawnLastFrame {
		return false
	}

	// Calculate cursor's visible Y position to determine how far off-screen it is
	effectiveScrollOffset := b.getEffectiveScrollOffset()
	visibleY := b.cursorY - logicalHiddenAbove + effectiveScrollOffset

	// Determine how many rows to scroll
	var scrollAmount int
	if b.lastCursorMoveDir > 0 {
		// Cursor moved down - need to scroll down (decrease scroll offset)
		// visibleY >= b.rows means cursor is below visible area
		if visibleY >= b.rows {
			scrollAmount = visibleY - b.rows + 1 // How far below the visible area
		} else if b.scrollOffset > 0 {
			scrollAmount = 1 // Just scroll one row if we can
		}

		if scrollAmount > 0 && b.scrollOffset > 0 {
			// Don't scroll more than we have offset
			if scrollAmount > b.scrollOffset {
				scrollAmount = b.scrollOffset
			}
			b.scrollOffset -= scrollAmount
			b.extendAutoScrollTimer() // Extend timer since we're actively scrolling
			b.markDirty()
			return true
		}
	} else if b.lastCursorMoveDir < 0 {
		// Cursor moved up - need to scroll up (increase scroll offset)
		// visibleY < 0 means cursor is above visible area
		if visibleY < 0 {
			scrollAmount = -visibleY // How far above (positive value)
		} else if b.scrollOffset < logicalHiddenAbove {
			scrollAmount = 1 // Just scroll one row if we can
		}

		if scrollAmount > 0 && b.scrollOffset < logicalHiddenAbove {
			// Don't scroll beyond logicalHiddenAbove
			maxScroll := logicalHiddenAbove - b.scrollOffset
			if scrollAmount > maxScroll {
				scrollAmount = maxScroll
			}
			b.scrollOffset += scrollAmount
			b.extendAutoScrollTimer() // Extend timer since we're actively scrolling
			b.markDirty()
			return true
		}
	}
	// If lastCursorMoveDir == 0, no direction known, don't scroll

	return false
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
	b.trackCursorYMove(b.savedCursorY)
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

	// Handle line wrap (DECAWM mode 7)
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
		if b.autoWrapMode {
			// Auto-wrap enabled: move to next line
			b.setHorizMoveDir(-1, false) // Word-wrap moves cursor left to start of line
			b.cursorX = 0
			b.trackCursorYMove(b.cursorY + 1)
			b.cursorY++
			if b.cursorY >= effectiveRows {
				b.scrollUpInternal()
				b.cursorY = effectiveRows - 1
			}
		} else {
			// Auto-wrap disabled (DECAWM off): stay at last column, overwrite character
			b.cursorX = effectiveCols - 1
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
	// Only set direction to right if we didn't wrap (wrap already set it to left)
	if !shouldWrap {
		b.setHorizMoveDir(1, false) // Character output moves cursor right
	}
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
	b.trackCursorYMove(b.cursorY + 1)
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
	b.setHorizMoveDir(-1, false) // Moving left
	b.cursorX = 0
	b.markDirty()
}

// LineFeed moves cursor down one line
func (b *Buffer) LineFeed() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.trackCursorYMove(b.cursorY + 1)
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
	b.setHorizMoveDir(1, false) // Moving right
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
	b.setHorizMoveDir(-1, false) // Moving left
	if b.cursorX > 0 {
		b.cursorX--
	}
	b.markDirty()
}

func (b *Buffer) scrollUpInternal() {
	if len(b.screen) == 0 {
		return
	}

	// Push top line to scrollback - this is a scroll-causing event
	b.pushLineToScrollback(b.screen[0], b.lineInfos[0])
	b.lastScrollCausingEvent = time.Now()

	// Shift screen up
	copy(b.screen, b.screen[1:])
	copy(b.lineInfos, b.lineInfos[1:])

	// Add new empty line at bottom with current attributes
	lastIdx := len(b.screen) - 1
	b.screen[lastIdx] = b.makeEmptyLine()
	b.lineInfos[lastIdx] = b.makeDefaultLineInfo()

	// Content scrolled up = new content at bottom = cursor moving toward newer content
	// Set direction directly since most cursor movements bypass setCursorInternal
	b.lastCursorMoveDir = 1 // Down

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

// ClearScreen clears the entire screen and resets view to show top
func (b *Buffer) ClearScreen() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.updateScreenInfo() // Update screen default attributes
	b.initScreen()

	// Reset cursor to top-left
	b.trackCursorYMove(0)
	b.cursorX = 0
	b.cursorY = 0

	// Reset scroll to show logical row 0 at the top of the visible area
	// When logicalRows > rows, we need scrollOffset = logicalHiddenAbove to see row 0
	effectiveRows := b.EffectiveRows()
	if effectiveRows > b.rows {
		b.scrollOffset = effectiveRows - b.rows
	} else {
		b.scrollOffset = 0
	}

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

	// Use helper function for magnetic zone handling
	effectiveScrollOffset := b.getEffectiveScrollOffset()

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
	baseMax := scrollbackSize + logicalHiddenAbove

	// Add magnetic threshold to create extra scroll positions for the magnetic zone.
	// This ensures all scrollback content remains accessible - the magnetic zone
	// adds positions rather than consuming them.
	if scrollbackSize > 0 {
		baseMax += b.getMagneticThreshold()
	}

	return baseMax
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

// GetEffectiveScrollOffset returns the scroll offset adjusted for the magnetic zone.
// Use this for rendering sprites, splits, and other positioned elements that should
// remain stable during the magnetic zone.
func (b *Buffer) GetEffectiveScrollOffset() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.getEffectiveScrollOffset()
}

// NormalizeScrollOffset snaps the scroll offset back if it's in the magnetic zone.
// The magnetic zone is when the boundary would appear at rows 1-threshold (5% of scrollable content).
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
	magneticThreshold := b.getMagneticThreshold()
	if boundaryRow > 0 && boundaryRow <= magneticThreshold {
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

	// Boundary at or below row 0 means we're viewing logical screen only
	if boundaryRow <= 0 {
		return -1
	}

	// Magnetic zone: suppress boundary when it would appear in the first few rows
	// This creates the "sticky" feel at the transition from logical screen to scrollback
	magneticThreshold := b.getMagneticThreshold()
	if boundaryRow <= magneticThreshold {
		return -1
	}

	// Past magnetic zone - subtract threshold so boundary position matches content
	effectiveBoundaryRow := boundaryRow - magneticThreshold

	// Check effective boundary is in visible range (1 to rows-1)
	// Row 0 would mean boundary at very top (no scrollback visible)
	// Row >= rows would mean boundary below visible area
	if effectiveBoundaryRow <= 0 || effectiveBoundaryRow >= b.rows {
		return -1
	}

	return effectiveBoundaryRow
}

// GetCursorVisiblePosition returns the visible (x, y) position of the cursor
// accounting for scroll offset and magnetic zone. Returns (-1, -1) if the cursor
// is not currently visible.
func (b *Buffer) GetCursorVisiblePosition() (x, y int) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	effectiveRows := b.EffectiveRows()

	// Calculate how much of the logical screen is hidden above
	logicalHiddenAbove := 0
	if effectiveRows > b.rows {
		logicalHiddenAbove = effectiveRows - b.rows
	}

	// Use effective scroll offset to account for magnetic zone
	effectiveScrollOffset := b.getEffectiveScrollOffset()

	// The cursor is at logical position (cursorX, cursorY)
	// Its visible position is: cursorY - logicalHiddenAbove + effectiveScrollOffset
	visibleY := b.cursorY - logicalHiddenAbove + effectiveScrollOffset

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

// GetCursorVisibleY returns just the cursor's visible Y position (row) on the
// physical screen, even if the cursor is horizontally off-screen. Returns -1
// if the cursor's line is not within the visible vertical area.
// This is useful for cursor tracking: we want to know if the cursor's LINE
// is being rendered, regardless of whether the cursor itself is visible.
func (b *Buffer) GetCursorVisibleY() int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	effectiveRows := b.EffectiveRows()

	// Calculate how much of the logical screen is hidden above
	logicalHiddenAbove := 0
	if effectiveRows > b.rows {
		logicalHiddenAbove = effectiveRows - b.rows
	}

	// Use effective scroll offset to account for magnetic zone
	effectiveScrollOffset := b.getEffectiveScrollOffset()

	// The cursor is at logical position (cursorX, cursorY)
	// Its visible position is: cursorY - logicalHiddenAbove + effectiveScrollOffset
	visibleY := b.cursorY - logicalHiddenAbove + effectiveScrollOffset

	// Check if cursor is within visible vertical area
	if visibleY < 0 || visibleY >= b.rows {
		return -1
	}

	return visibleY
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

// NotifyManualHorizScroll should be called when the user manually scrolls horizontally.
// This temporarily suppresses horizontal auto-scrolling.
func (b *Buffer) NotifyManualHorizScroll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastManualHorizScroll = time.Now()
}

// SetAutoScrollDisabled enables or disables cursor-following auto-scroll.
// When disabled, tracking still occurs but no automatic scrolling happens.
// This is controlled by a DEC Private Mode sequence.
func (b *Buffer) SetAutoScrollDisabled(disabled bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.autoScrollDisabled = disabled
}

// IsAutoScrollDisabled returns true if auto-scroll is disabled.
func (b *Buffer) IsAutoScrollDisabled() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.autoScrollDisabled
}

// SetAutoWrapMode enables or disables auto-wrap at end of line (DECAWM, mode 7).
// When disabled, the cursor stays at the last column and characters overwrite that position.
func (b *Buffer) SetAutoWrapMode(enabled bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.autoWrapMode = enabled
}

// IsAutoWrapModeEnabled returns true if auto-wrap is enabled (DECAWM).
func (b *Buffer) IsAutoWrapModeEnabled() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.autoWrapMode
}

// ClearHorizMemos clears all horizontal scroll memos before a new paint frame.
// Call this at the start of each paint.
func (b *Buffer) ClearHorizMemos() {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Ensure slice is sized to match screen rows
	if len(b.horizMemos) != b.rows {
		b.horizMemos = make([]HorizMemo, b.rows)
	} else {
		// Clear existing entries
		for i := range b.horizMemos {
			b.horizMemos[i] = HorizMemo{}
		}
	}
}

// SetHorizMemo sets the horizontal scroll memo for a specific scanline.
// Call this during paint for each row where the cursor's logical line is being rendered.
func (b *Buffer) SetHorizMemo(scanline int, memo HorizMemo) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if scanline >= 0 && scanline < len(b.horizMemos) {
		b.horizMemos[scanline] = memo
	}
}

// GetHorizMemos returns a copy of all horizontal memos (for debugging/inspection).
func (b *Buffer) GetHorizMemos() []HorizMemo {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]HorizMemo, len(b.horizMemos))
	copy(result, b.horizMemos)
	return result
}

// isHorizAutoScrollActive returns true if horizontal auto-scroll should be active.
// It checks keyboard activity, manual scroll cooldown, and scrollback viewing state.
func (b *Buffer) isHorizAutoScrollActive() bool {
	// Must have recent keyboard activity
	if b.lastKeyboardActivity.IsZero() {
		return false
	}
	if time.Since(b.lastKeyboardActivity) >= keyboardAutoScrollDuration {
		return false
	}

	// Don't auto-scroll if viewing scrollback
	if b.IsViewingScrollbackInternal() {
		return false
	}

	// Check manual scroll cooldown
	if !b.lastManualHorizScroll.IsZero() {
		timeSinceManualScroll := time.Since(b.lastManualHorizScroll)

		// If keyboard activity occurred after manual scroll, allow auto-scroll
		if b.lastKeyboardActivity.After(b.lastManualHorizScroll) {
			return true
		}

		// If within cooldown period, check if a scroll-causing event occurred after manual scroll
		if timeSinceManualScroll < manualScrollCooldown {
			return false
		}

		// Past cooldown, but need a scroll-causing event to have occurred after manual scroll
		if b.lastScrollCausingEvent.IsZero() || b.lastScrollCausingEvent.Before(b.lastManualHorizScroll) {
			return false
		}
	}

	return true
}

// IsViewingScrollbackInternal returns true if currently viewing scrollback buffer (internal, no lock).
func (b *Buffer) IsViewingScrollbackInternal() bool {
	effectiveRows := b.EffectiveRows()
	logicalHiddenAbove := 0
	if effectiveRows > b.rows {
		logicalHiddenAbove = effectiveRows - b.rows
	}
	// If scroll offset is beyond logicalHiddenAbove, we're viewing actual scrollback
	return b.scrollOffset > logicalHiddenAbove
}

// CheckCursorAutoScrollHoriz checks horizontal memos and auto-scrolls if needed.
// Returns true if a scroll occurred.
// Call this after paint, after memos have been populated.
func (b *Buffer) CheckCursorAutoScrollHoriz() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Check if auto-scroll is disabled by DEC Private Mode
	if b.autoScrollDisabled {
		return false
	}

	// Check if horizontal auto-scroll is active
	if !b.isHorizAutoScrollActive() {
		return false
	}

	// FIRST: If we're viewing scrollback, snap to logical screen boundary first.
	// The scrollback should be forced off screen before any horizontal auto-scrolling.
	// (Vertical auto-scroll handles this too, but in case horizontal is called first
	// or vertical didn't trigger, we ensure scrollback is off-screen here too.)
	effectiveRows := b.EffectiveRows()
	logicalHiddenAbove := 0
	if effectiveRows > b.rows {
		logicalHiddenAbove = effectiveRows - b.rows
	}
	if b.scrollOffset > logicalHiddenAbove {
		b.scrollOffset = logicalHiddenAbove
		b.markDirty()
		return true
	}

	// Check if cursor line was rendered (from cursorDrawnLastFrame set by vertical tracking)
	// If cursor line isn't visible vertically, don't do horizontal auto-scroll
	if !b.cursorDrawnLastFrame {
		return false
	}

	// Analyze memos to find the nearest distance to cursor
	minDistLeft := -1
	minDistRight := -1
	cursorFound := false

	// For absolute positioning check
	cursorX := b.cursorX

	for _, memo := range b.horizMemos {
		if !memo.Valid {
			continue
		}

		if memo.CursorLocated {
			cursorFound = true
			break
		}

		if memo.DistanceToLeft > 0 {
			if minDistLeft < 0 || memo.DistanceToLeft < minDistLeft {
				minDistLeft = memo.DistanceToLeft
			}
		}

		if memo.DistanceToRight > 0 {
			if minDistRight < 0 || memo.DistanceToRight < minDistRight {
				minDistRight = memo.DistanceToRight
			}
		}
	}

	// If cursor was found in rendered area, no scroll needed
	if cursorFound {
		return false
	}

	// If no distances found, no scroll possible
	if minDistLeft < 0 && minDistRight < 0 {
		return false
	}

	// Determine scroll direction and amount
	var scrollAmount int
	var scrollLeft bool

	// Check for absolute positioning - look for cursor just outside rendered bounds
	if b.isAbsoluteHorizPosition {
		for _, memo := range b.horizMemos {
			if !memo.Valid {
				continue
			}
			// Cursor just one position to the left of rendered area
			if cursorX == memo.LeftmostCell-1 && memo.DistanceToLeft == 1 {
				scrollAmount = 1
				scrollLeft = true
				break
			}
			// Cursor just one position to the right of rendered area
			if cursorX == memo.RightmostCell+1 && memo.DistanceToRight == 1 {
				scrollAmount = 1
				scrollLeft = false
				break
			}
		}
	}

	// If absolute positioning didn't set a scroll, use direction-based or nearest algorithm
	if scrollAmount == 0 {
		if b.lastHorizCursorMoveDir < 0 && minDistLeft > 0 {
			// Known direction: left
			scrollAmount = minDistLeft
			scrollLeft = true
		} else if b.lastHorizCursorMoveDir > 0 && minDistRight > 0 {
			// Known direction: right
			scrollAmount = minDistRight
			scrollLeft = false
		} else {
			// Direction unknown - use nearest, favor left on tie
			if minDistLeft > 0 && minDistRight > 0 {
				if minDistLeft <= minDistRight {
					scrollAmount = minDistLeft
					scrollLeft = true
				} else {
					scrollAmount = minDistRight
					scrollLeft = false
				}
			} else if minDistLeft > 0 {
				scrollAmount = minDistLeft
				scrollLeft = true
			} else if minDistRight > 0 {
				scrollAmount = minDistRight
				scrollLeft = false
			}
		}
	}

	// Apply the scroll
	if scrollAmount > 0 {
		if scrollLeft {
			// Always scroll one extra position left to keep the cursor away from
			// the edge. This provides a visual buffer so the character isn't at
			// the very edge of the visible area.
			scrollAmount++
			b.horizOffset -= scrollAmount
			if b.horizOffset < 0 {
				b.horizOffset = 0
			}
		} else {
			b.horizOffset += scrollAmount
			// Cap at max offset
			maxOffset := b.getMaxHorizOffsetInternal()
			if b.horizOffset > maxOffset {
				b.horizOffset = maxOffset
			}
		}
		b.markDirty()
		return true
	}

	return false
}

// getMaxHorizOffsetInternal calculates max horizontal offset (internal, no lock).
func (b *Buffer) getMaxHorizOffsetInternal() int {
	// Similar to GetMaxHorizOffset but without locking
	cols := b.cols
	splitWidth := b.splitContentWidth
	currentOffset := b.horizOffset

	// Calculate longest line - simplified version for internal use
	longest := 0
	for _, line := range b.screen {
		if len(line) > longest {
			longest = len(line)
		}
	}
	if splitWidth > longest {
		longest = splitWidth
	}

	contentBasedMax := 0
	if longest > cols {
		contentBasedMax = longest - cols
	}

	if currentOffset > contentBasedMax {
		return currentOffset
	}
	return contentBasedMax
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
	newY := b.cursorY - n
	if newY < 0 {
		newY = 0
	}
	b.trackCursorYMove(newY)
	b.cursorY = newY
	b.markDirty()
}

// MoveCursorDown moves cursor down n rows
func (b *Buffer) MoveCursorDown(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	newY := b.cursorY + n
	effectiveRows := b.EffectiveRows()
	if newY >= effectiveRows {
		newY = effectiveRows - 1
	}
	b.trackCursorYMove(newY)
	b.cursorY = newY
	b.markDirty()
}

// MoveCursorForward moves cursor right n columns (CSI C)
func (b *Buffer) MoveCursorForward(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.setHorizMoveDir(1, false) // Moving right
	b.cursorX += n
	effectiveCols := b.EffectiveCols()
	if b.cursorX >= effectiveCols {
		b.cursorX = effectiveCols - 1
	}
	b.markDirty()
}

// MoveCursorBackward moves cursor left n columns (CSI D)
func (b *Buffer) MoveCursorBackward(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.setHorizMoveDir(-1, false) // Moving left
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

	// Use effective scroll offset to account for magnetic zone
	effectiveScrollOffset := b.getEffectiveScrollOffset()

	// Convert screen Y to buffer-absolute Y
	return totalScrollableAbove - effectiveScrollOffset + screenY
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

	// Use effective scroll offset to account for magnetic zone
	effectiveScrollOffset := b.getEffectiveScrollOffset()

	// Convert buffer-absolute Y to screen Y
	screenY := bufferY - totalScrollableAbove + effectiveScrollOffset

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

	// Use helper function for magnetic zone handling (same as GetVisibleCell)
	effectiveScrollOffset := b.getEffectiveScrollOffset()

	totalScrollableAbove := scrollbackSize + logicalHiddenAbove

	if effectiveScrollOffset == 0 {
		// Not scrolled - show bottom of logical screen
		logicalY := logicalHiddenAbove + y
		if logicalY >= 0 && logicalY < len(b.lineInfos) {
			return b.lineInfos[logicalY]
		}
		return LineInfo{Attribute: LineAttrNormal, DefaultCell: b.screenInfo.DefaultCell}
	}

	// Scrolled - map to scrollback or logical screen
	absoluteY := totalScrollableAbove - effectiveScrollOffset + y

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
		return TrueColor(
			uint8(float64(cell.Foreground.R)*0.6),
			uint8(float64(cell.Foreground.G)*0.6),
			uint8(float64(cell.Foreground.B)*0.6),
		), true
	default:
		// Bright variant - lighten the foreground
		return TrueColor(
			uint8(min(255, int(cell.Foreground.R)+64)),
			uint8(min(255, int(cell.Foreground.G)+64)),
			uint8(min(255, int(cell.Foreground.B)+64)),
		), true
	}
}

// resolveEntry converts a palette entry to a color
func (b *Buffer) resolveEntry(entry *PaletteEntry, cell *Cell) (Color, bool) {
	switch entry.Type {
	case PaletteEntryTransparent:
		return cell.Background, true
	case PaletteEntryDefaultFG:
		if entry.Dim {
			return TrueColor(
				uint8(float64(cell.Foreground.R)*0.6),
				uint8(float64(cell.Foreground.G)*0.6),
				uint8(float64(cell.Foreground.B)*0.6),
			), true
		}
		return cell.Foreground, true
	default:
		color := entry.Color
		if entry.Dim {
			color = TrueColor(
				uint8(float64(color.R)*0.6),
				uint8(float64(color.G)*0.6),
				uint8(float64(color.B)*0.6),
			)
		}
		return color, true
	}
}

// ColorToANSICode returns the ANSI color code for this color.
// For Standard colors (0-15), returns 30-37 or 90-97.
// For Palette colors (0-255), returns the palette index mapped to SGR codes.
// For TrueColor or Default, returns 37 (white) as fallback.
func (b *Buffer) ColorToANSICode(c Color) int {
	switch c.Type {
	case ColorTypeStandard:
		idx := int(c.Index)
		if idx < 8 {
			return 30 + idx
		}
		return 90 + (idx - 8)
	case ColorTypePalette:
		// For palette colors 0-15, treat like standard colors
		idx := int(c.Index)
		if idx < 8 {
			return 30 + idx
		} else if idx < 16 {
			return 90 + (idx - 8)
		}
		// For extended palette colors, return the index directly
		// (caller should use 38;5;N format)
		return idx
	case ColorTypeDefault:
		return 39 // Default foreground code
	case ColorTypeTrueColor:
		// Try to find matching ANSI color by RGB
		for i, ansi := range ANSIColorsRGB {
			if c.R == ansi.R && c.G == ansi.G && c.B == ansi.B {
				if i < 8 {
					return 30 + i
				}
				return 90 + (i - 8)
			}
		}
		return 37 // Fallback to white
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
		return TrueColor(
			uint8(float64(fg.R)*0.6),
			uint8(float64(fg.G)*0.6),
			uint8(float64(fg.B)*0.6),
		), true
	default:
		// Bright variant
		return TrueColor(
			uint8(min(255, int(fg.R)+64)),
			uint8(min(255, int(fg.G)+64)),
			uint8(min(255, int(fg.B)+64)),
		), true
	}
}

// resolveSpriteEntry converts a palette entry to a color for sprites
func (b *Buffer) resolveSpriteEntry(entry *PaletteEntry, fg, bg Color) (Color, bool) {
	switch entry.Type {
	case PaletteEntryTransparent:
		return Color{}, false // Transparent for sprites
	case PaletteEntryDefaultFG:
		if entry.Dim {
			return TrueColor(
				uint8(float64(fg.R)*0.6),
				uint8(float64(fg.G)*0.6),
				uint8(float64(fg.B)*0.6),
			), true
		}
		return fg, true
	default:
		color := entry.Color
		if entry.Dim {
			color = TrueColor(
				uint8(float64(color.R)*0.6),
				uint8(float64(color.G)*0.6),
				uint8(float64(color.B)*0.6),
			)
		}
		return color, true
	}
}

// --- Scrollback Management Methods ---

// ClearScrollback clears the scrollback buffer
func (b *Buffer) ClearScrollback() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.scrollback = nil
	b.scrollbackInfo = nil
	b.scrollOffset = 0
	b.markDirty()
}

// Reset resets the terminal to initial state
// Moves current screen content to scrollback, then resets all modes and cursor
func (b *Buffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Push current screen content to scrollback first
	for i := 0; i < len(b.screen); i++ {
		if len(b.screen[i]) > 0 {
			b.pushLineToScrollback(b.screen[i], b.lineInfos[i])
		}
	}

	// Reset screen
	b.initScreen()

	// Reset cursor
	b.cursorX = 0
	b.cursorY = 0
	b.cursorVisible = true
	b.cursorShape = 0
	b.cursorBlink = 0
	b.savedCursorX = 0
	b.savedCursorY = 0

	// Reset attributes
	b.currentFg = DefaultForeground
	b.currentBg = DefaultBackground
	b.currentBold = false
	b.currentItalic = false
	b.currentUnderline = false
	b.currentReverse = false
	b.currentBlink = false
	b.currentFlexWidth = false

	// Reset modes
	b.bracketedPasteMode = false
	b.flexWidthMode = false
	b.visualWidthWrap = false
	b.ambiguousWidthMode = AmbiguousWidthAuto
	b.autoWrapMode = true
	b.autoScrollDisabled = false
	b.scrollbackDisabled = false
	b.columnMode132 = false
	b.columnMode40 = false
	b.lineDensity = 25

	// Reset custom graphics state
	b.currentBGP = -1
	b.currentXFlip = false
	b.currentYFlip = false

	// Reset scroll offset
	b.scrollOffset = 0
	b.horizOffset = 0

	b.markDirty()
	b.notifyScaleChange()
}

// SaveScrollbackText returns the scrollback and screen content as plain text
func (b *Buffer) SaveScrollbackText() string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var result strings.Builder

	// Output scrollback lines
	for _, line := range b.scrollback {
		for _, cell := range line {
			if cell.Char != 0 {
				result.WriteRune(cell.Char)
			}
		}
		result.WriteString("\n")
	}

	// Output screen lines
	for _, line := range b.screen {
		for _, cell := range line {
			if cell.Char != 0 {
				result.WriteRune(cell.Char)
			}
		}
		result.WriteString("\n")
	}

	return result.String()
}

// SaveScrollbackANS returns the scrollback and screen with full ANSI/PawScript codes preserved.
// The output format:
// 1. TOP: Custom palette definitions (OSC 7000), custom glyph definitions (OSC 7001)
// 2. BODY: Content lines with DEC line attributes, SGR codes, BGP/flip attributes
// 3. END: Sprite units, screen splits, screen crop, crop rectangles, sprites, cursor position
func (b *Buffer) SaveScrollbackANS() string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var result strings.Builder

	// ========== SECTION 1: Definitions at TOP ==========

	// Output palette definitions (OSC 7000)
	// Get sorted palette keys for deterministic output
	paletteKeys := make([]int, 0, len(b.palettes))
	for k := range b.palettes {
		paletteKeys = append(paletteKeys, k)
	}
	sort.Ints(paletteKeys)

	for _, n := range paletteKeys {
		palette := b.palettes[n]
		if palette == nil || len(palette.Entries) == 0 {
			continue
		}
		// Init palette: ESC ] 7000 ; i ; N ; LEN BEL
		result.WriteString(fmt.Sprintf("\x1b]7000;i;%d;%d\x07", n, len(palette.Entries)))
		// Set each entry
		for idx, entry := range palette.Entries {
			switch entry.Type {
			case PaletteEntryColor:
				// True color: ESC ] 7000 ; s ; N ; IDX ; r ; R ; G ; B BEL
				// or with dim: ESC ] 7000 ; s ; N ; IDX ; r ; 2 ; R ; G ; B BEL
				if entry.Dim {
					result.WriteString(fmt.Sprintf("\x1b]7000;s;%d;%d;r;2;%d;%d;%d\x07",
						n, idx, entry.Color.R, entry.Color.G, entry.Color.B))
				} else {
					result.WriteString(fmt.Sprintf("\x1b]7000;s;%d;%d;r;%d;%d;%d\x07",
						n, idx, entry.Color.R, entry.Color.G, entry.Color.B))
				}
			case PaletteEntryTransparent:
				// Transparent (use background): SGR code 8
				result.WriteString(fmt.Sprintf("\x1b]7000;s;%d;%d;8\x07", n, idx))
			case PaletteEntryDefaultFG:
				// Use foreground: SGR code 9
				result.WriteString(fmt.Sprintf("\x1b]7000;s;%d;%d;9\x07", n, idx))
			}
		}
	}

	// Output glyph definitions (OSC 7001)
	// Get sorted glyph runes for deterministic output
	glyphRunes := make([]rune, 0, len(b.customGlyphs))
	for r := range b.customGlyphs {
		glyphRunes = append(glyphRunes, r)
	}
	sort.Slice(glyphRunes, func(i, j int) bool { return glyphRunes[i] < glyphRunes[j] })

	for _, r := range glyphRunes {
		glyph := b.customGlyphs[r]
		if glyph == nil || glyph.Width == 0 || len(glyph.Pixels) == 0 {
			continue
		}
		// Set glyph: ESC ] 7001 ; s ; RUNE ; WIDTH ; P1 ; P2 ; ... BEL
		result.WriteString(fmt.Sprintf("\x1b]7001;s;%d;%d", int(r), glyph.Width))
		for _, px := range glyph.Pixels {
			result.WriteString(fmt.Sprintf(";%d", px))
		}
		result.WriteString("\x07")
	}

	// ========== SECTION 1b: Terminal Mode Settings ==========

	// Always enable flex width mode for ANS files so CJK and other wide
	// characters render at their proper visual width when restored
	result.WriteString("\x1b[?2027h")

	// Output ambiguous width mode setting
	switch b.ambiguousWidthMode {
	case AmbiguousWidthNarrow:
		result.WriteString("\x1b[?2029h") // Enable narrow mode
	case AmbiguousWidthWide:
		result.WriteString("\x1b[?2030h") // Enable wide mode
	}

	// Output visual width wrap mode if enabled (DEC private mode 2028)
	if b.visualWidthWrap {
		result.WriteString("\x1b[?2028h")
	}

	// ========== SECTION 2: Content Lines ==========

	// Track current attributes to minimize escape sequences
	var lastFg, lastBg Color
	var lastBold, lastItalic, lastUnderline, lastReverse, lastBlink bool
	var lastBGP int = -1
	var lastXFlip, lastYFlip bool
	var lastLineAttr LineAttribute = LineAttrNormal

	// Helper to write ANSI color code using proper SGR codes
	writeColor := func(c Color, isFg bool) {
		result.WriteString("\x1b[" + c.ToSGRCode(isFg) + "m")
	}

	// Count total lines for cursor positioning later
	totalLines := len(b.scrollback) + len(b.screen)
	currentLineNum := 0

	outputLine := func(line []Cell, lineInfo LineInfo) {
		hasNonDefaultBg := false
		currentLineNum++

		// Output DEC line attribute if changed (at start of line)
		if lineInfo.Attribute != lastLineAttr {
			switch lineInfo.Attribute {
			case LineAttrDoubleWidth:
				result.WriteString("\x1b#6") // DECDWL
			case LineAttrDoubleTop:
				result.WriteString("\x1b#3") // DECDHL top
			case LineAttrDoubleBottom:
				result.WriteString("\x1b#4") // DECDHL bottom
			case LineAttrNormal:
				result.WriteString("\x1b#5") // DECSWL (single width)
			}
			lastLineAttr = lineInfo.Attribute
		}

		for _, cell := range line {
			// Check if standard attributes changed (need reset)
			needsReset := false
			if cell.Bold != lastBold || cell.Italic != lastItalic ||
				cell.Underline != lastUnderline || cell.Reverse != lastReverse ||
				cell.Blink != lastBlink {
				needsReset = true
			}

			if needsReset {
				result.WriteString("\x1b[0m") // Reset all
				lastFg = Color{}
				lastBg = Color{}
				lastBold = false
				lastItalic = false
				lastUnderline = false
				lastReverse = false
				lastBlink = false
				// Reset doesn't affect BGP/flip, but we track them separately
			}

			// Set standard attributes
			if cell.Bold && !lastBold {
				result.WriteString("\x1b[1m")
				lastBold = true
			}
			if cell.Italic && !lastItalic {
				result.WriteString("\x1b[3m")
				lastItalic = true
			}
			if cell.Underline && !lastUnderline {
				result.WriteString("\x1b[4m")
				lastUnderline = true
			}
			if cell.Reverse && !lastReverse {
				result.WriteString("\x1b[7m")
				lastReverse = true
			}
			if cell.Blink && !lastBlink {
				result.WriteString("\x1b[5m")
				lastBlink = true
			}

			// Set colors
			if cell.Foreground != lastFg {
				writeColor(cell.Foreground, true)
				lastFg = cell.Foreground
			}
			if cell.Background != lastBg {
				writeColor(cell.Background, false)
				lastBg = cell.Background
				if !cell.Background.IsDefault() {
					hasNonDefaultBg = true
				}
			}

			// Set BGP if changed
			if cell.BGP != lastBGP {
				if cell.BGP < 0 {
					result.WriteString("\x1b[169m") // Reset BGP
				} else {
					result.WriteString(fmt.Sprintf("\x1b[168;5;%dm", cell.BGP))
				}
				lastBGP = cell.BGP
			}

			// Set XFlip if changed
			if cell.XFlip != lastXFlip {
				if cell.XFlip {
					result.WriteString("\x1b[151m") // XFlip on
				} else {
					result.WriteString("\x1b[150m") // XFlip off
				}
				lastXFlip = cell.XFlip
			}

			// Set YFlip if changed
			if cell.YFlip != lastYFlip {
				if cell.YFlip {
					result.WriteString("\x1b[153m") // YFlip on
				} else {
					result.WriteString("\x1b[152m") // YFlip off
				}
				lastYFlip = cell.YFlip
			}

			// Output character and combining marks
			if cell.Char != 0 {
				result.WriteRune(cell.Char)
				if len(cell.Combining) > 0 {
					result.WriteString(cell.Combining)
				}
			}
		}

		// If line had background color, clear to end of line
		if hasNonDefaultBg {
			result.WriteString("\x1b[K") // Clear to end of line (preserves bg)
		}
		result.WriteString("\x1b[0m\n") // Reset and newline
		lastFg = Color{}
		lastBg = Color{}
		lastBold = false
		lastItalic = false
		lastUnderline = false
		lastReverse = false
		lastBlink = false

		// If background was dirty, clear the next line to prevent bleeding
		if hasNonDefaultBg {
			result.WriteString("\x1b[2K") // Clear entire line
		}
	}

	// Output scrollback lines
	for i, line := range b.scrollback {
		var lineInfo LineInfo
		if i < len(b.scrollbackInfo) {
			lineInfo = b.scrollbackInfo[i]
		}
		outputLine(line, lineInfo)
	}

	// Output screen lines
	for i, line := range b.screen {
		var lineInfo LineInfo
		if i < len(b.lineInfos) {
			lineInfo = b.lineInfos[i]
		}
		outputLine(line, lineInfo)
	}

	// ========== SECTION 3: State at END ==========

	// Output sprite units if not default (8x8)
	if b.spriteUnitX != 8 || b.spriteUnitY != 8 {
		result.WriteString(fmt.Sprintf("\x1b]7002;u;%d;%d\x07", b.spriteUnitX, b.spriteUnitY))
	}

	// Output screen splits (OSC 7003 ss)
	splitIDs := make([]int, 0, len(b.screenSplits))
	for id := range b.screenSplits {
		splitIDs = append(splitIDs, id)
	}
	sort.Ints(splitIDs)

	for _, id := range splitIDs {
		split := b.screenSplits[id]
		if split == nil {
			continue
		}
		// Format: ss;ID;SCREENY;BUFROW;BUFCOL;TOPFINE;LEFTFINE;CWS;LD
		// BUFROW/BUFCOL are 1-indexed in the escape sequence (0 means inherit)
		bufRow := split.BufferRow
		bufCol := split.BufferCol
		if bufRow > 0 {
			bufRow++ // Convert 0-indexed to 1-indexed
		}
		if bufCol > 0 {
			bufCol++
		}
		result.WriteString(fmt.Sprintf("\x1b]7003;ss;%d;%d;%d;%d;%d;%d;%g;%d\x07",
			id, split.ScreenY, bufRow, bufCol,
			split.TopFineScroll, split.LeftFineScroll,
			split.CharWidthScale, split.LineDensity))
	}

	// Output screen crop if set (OSC 7003 c)
	if b.widthCrop >= 0 || b.heightCrop >= 0 {
		if b.widthCrop >= 0 && b.heightCrop >= 0 {
			result.WriteString(fmt.Sprintf("\x1b]7003;c;%d;%d\x07", b.widthCrop, b.heightCrop))
		} else if b.widthCrop >= 0 {
			result.WriteString(fmt.Sprintf("\x1b]7003;c;%d\x07", b.widthCrop))
		} else {
			result.WriteString(fmt.Sprintf("\x1b]7003;c;;%d\x07", b.heightCrop))
		}
	}

	// Output crop rectangles (OSC 7002 cs)
	cropIDs := make([]int, 0, len(b.cropRects))
	for id := range b.cropRects {
		cropIDs = append(cropIDs, id)
	}
	sort.Ints(cropIDs)

	for _, id := range cropIDs {
		crop := b.cropRects[id]
		if crop == nil {
			continue
		}
		// Format: cs;ID;MINX;MINY;MAXX;MAXY
		result.WriteString(fmt.Sprintf("\x1b]7002;cs;%d;%g;%g;%g;%g\x07",
			id, crop.MinX, crop.MinY, crop.MaxX, crop.MaxY))
	}

	// Output sprites (OSC 7002 s)
	spriteIDs := make([]int, 0, len(b.sprites))
	for id := range b.sprites {
		spriteIDs = append(spriteIDs, id)
	}
	sort.Ints(spriteIDs)

	for _, id := range spriteIDs {
		sprite := b.sprites[id]
		if sprite == nil {
			continue
		}
		// Format: s;ID;X;Y;Z;FGP;FLIP;XS;YS;CROP;R1;R2;...
		// Collect all runes from the 2D array
		var runes []rune
		for rowIdx, row := range sprite.Runes {
			if rowIdx > 0 {
				runes = append(runes, 10) // Newline separator
			}
			runes = append(runes, row...)
		}
		result.WriteString(fmt.Sprintf("\x1b]7002;s;%d;%g;%g;%d;%d;%d;%g;%g;%d",
			id, sprite.X, sprite.Y, sprite.ZIndex, sprite.FGP, sprite.FlipCode,
			sprite.XScale, sprite.YScale, sprite.CropRect))
		for _, r := range runes {
			result.WriteString(fmt.Sprintf(";%d", int(r)))
		}
		result.WriteString("\x07")
	}

	// Output cursor position restoration (only if cursor is not at end of content)
	// The cursor is considered "at end" if it's on the last line at or past the content
	// In that case, we don't need CSI A or G codes
	if totalLines > 0 {
		// Calculate how far back the cursor needs to go
		linesFromEnd := totalLines - (len(b.scrollback) + b.cursorY + 1)

		// Find the last non-empty character position on the last line
		lastLineLen := 0
		if len(b.screen) > 0 {
			lastLine := b.screen[len(b.screen)-1]
			for i := len(lastLine) - 1; i >= 0; i-- {
				if lastLine[i].Char != 0 && lastLine[i].Char != ' ' {
					lastLineLen = i + 1
					break
				}
			}
		}

		// Only output cursor positioning if cursor is NOT at the natural end position
		// Natural end = last line, at or after last content character
		cursorAtEnd := (linesFromEnd == 0) && (b.cursorX >= lastLineLen)

		if !cursorAtEnd {
			// Need to reposition cursor
			if linesFromEnd > 0 {
				// Move up N lines
				result.WriteString(fmt.Sprintf("\x1b[%dA", linesFromEnd))
			}
			if b.cursorX > 0 {
				// Move to column (1-indexed)
				result.WriteString(fmt.Sprintf("\x1b[%dG", b.cursorX+1))
			}
		}
	}

	return result.String()
}
