package purfectermqt

import (
	"math"
	"sync"

	"github.com/mappu/miqt/qt"
	"github.com/phroun/pawscript/pkg/purfecterm"
)

// Left padding for terminal content (pixels)
const terminalLeftPadding = 8

// glyphCacheEntry stores a cached rendered glyph pixmap
type glyphCacheEntry struct {
	pixmap     *qt.QPixmap
	lastAccess uint64 // Access counter for LRU eviction
}

// glyphCache provides LRU caching for rendered glyphs
type glyphCache struct {
	entries       map[purfecterm.GlyphCacheKey]*glyphCacheEntry
	accessCounter uint64 // Global counter incremented on each access
	maxEntries    int    // Maximum cache size
}

func newGlyphCache(maxEntries int) *glyphCache {
	return &glyphCache{
		entries:    make(map[purfecterm.GlyphCacheKey]*glyphCacheEntry),
		maxEntries: maxEntries,
	}
}

// get retrieves a cached glyph pixmap, updating its access time
func (c *glyphCache) get(key purfecterm.GlyphCacheKey) *qt.QPixmap {
	if entry, ok := c.entries[key]; ok {
		c.accessCounter++
		entry.lastAccess = c.accessCounter
		return entry.pixmap
	}
	return nil
}

// put adds a glyph pixmap to the cache, evicting old entries if needed
func (c *glyphCache) put(key purfecterm.GlyphCacheKey, pixmap *qt.QPixmap) {
	// Evict old entries if at capacity
	if len(c.entries) >= c.maxEntries {
		c.evictOldest(c.maxEntries / 4) // Evict 25% of entries
	}

	c.accessCounter++
	c.entries[key] = &glyphCacheEntry{
		pixmap:     pixmap,
		lastAccess: c.accessCounter,
	}
}

// evictOldest removes the n oldest entries from the cache
func (c *glyphCache) evictOldest(n int) {
	if n <= 0 || len(c.entries) == 0 {
		return
	}

	// Find the n entries with lowest lastAccess
	type entryInfo struct {
		key        purfecterm.GlyphCacheKey
		lastAccess uint64
	}

	entries := make([]entryInfo, 0, len(c.entries))
	for k, v := range c.entries {
		entries = append(entries, entryInfo{k, v.lastAccess})
	}

	// Partial sort to find n smallest
	for i := 0; i < n && i < len(entries); i++ {
		minIdx := i
		for j := i + 1; j < len(entries); j++ {
			if entries[j].lastAccess < entries[minIdx].lastAccess {
				minIdx = j
			}
		}
		entries[i], entries[minIdx] = entries[minIdx], entries[i]
	}

	// Remove the oldest n entries
	for i := 0; i < n && i < len(entries); i++ {
		delete(c.entries, entries[i].key)
	}
}

// clear removes all entries from the cache
func (c *glyphCache) clear() {
	c.entries = make(map[purfecterm.GlyphCacheKey]*glyphCacheEntry)
}

// buildCustomGlyphKey creates a cache key for a custom glyph.
// usesDefaultFG: if true, include fg color in key (palette has DefaultFG entries)
// usesBg: if true, include bg color in key (palette has transparent or single-entry mode)
func buildCustomGlyphKey(r rune, width, height int, xFlip, yFlip bool,
	paletteHash uint64, glyphHash uint64, usesDefaultFG, usesBg bool,
	fg, bg purfecterm.Color) purfecterm.GlyphCacheKey {
	key := purfecterm.GlyphCacheKey{
		Rune:          r,
		Width:         int16(width),
		Height:        int16(height),
		IsCustomGlyph: true,
		XFlip:         xFlip,
		YFlip:         yFlip,
		PaletteHash:   paletteHash,
		GlyphHash:     glyphHash,
	}
	if usesDefaultFG {
		key.FgR = fg.R
		key.FgG = fg.G
		key.FgB = fg.B
	}
	if usesBg {
		key.BgR = bg.R
		key.BgG = bg.G
		key.BgB = bg.B
	}
	return key
}

// Widget is a Qt terminal emulator widget
type Widget struct {
	widget         *qt.QWidget    // The terminal drawing area
	scrollbar      *qt.QScrollBar // Vertical scrollbar (child of widget)
	horizScrollbar *qt.QScrollBar // Horizontal scrollbar (child of widget)

	mu sync.Mutex

	// Terminal state
	buffer *purfecterm.Buffer
	parser *purfecterm.Parser

	// Glyph cache for rendered characters
	glyphCache *glyphCache

	// Font settings
	fontFamily        string
	fontFamilyUnicode string // Fallback for Unicode characters missing from main font
	fontFamilyCJK     string // Fallback for CJK characters
	fontSize          int
	charWidth         int
	charHeight        int
	charAscent        int

	// Color scheme
	scheme purfecterm.ColorScheme

	// Selection state
	selecting       bool
	selectStartX    int
	selectStartY    int
	mouseDown       bool
	mouseDownX      int
	mouseDownY      int
	selectionMoved  bool
	autoScrollTimer *qt.QTimer // Timer for auto-scrolling
	autoScrollDelta int        // Scroll direction (-1=up, 1=down), magnitude used for speed
	lastMouseX      int        // Last known mouse X cell position

	// Update coalescing for thread-safe redraws
	updatePending bool
	updateTimer   *qt.QTimer

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

	// Scrollbar update flag
	scrollbarUpdating bool
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
		glyphCache:    newGlyphCache(4096),
	}

	// Create buffer and parser
	w.buffer = purfecterm.NewBuffer(cols, rows, scrollbackSize)
	w.parser = purfecterm.NewParser(w.buffer)

	// Create update timer for thread-safe redraws (16ms ≈ 60fps)
	// This coalesces updates from background threads onto the Qt main thread
	w.updateTimer = qt.NewQTimer2(w.widget.QObject)
	w.updateTimer.OnTimeout(func() {
		if w.updatePending {
			w.updatePending = false
			w.widget.Update()
		}
	})
	w.updateTimer.Start(16)

	// Set up dirty callback to trigger redraws
	// Note: Don't call updateScrollbar here - it causes deadlock since
	// the dirty callback is called while buffer holds its lock
	// Note: We set a flag and let the timer handle the actual Update() call
	// to ensure it happens on the Qt main thread
	w.buffer.SetDirtyCallback(func() {
		w.updatePending = true
	})

	// Enable focus and mouse tracking on the terminal widget
	w.widget.SetFocusPolicy(qt.StrongFocus)
	w.widget.SetMouseTracking(true)
	w.widget.SetAttribute(qt.WA_InputMethodEnabled)

	// Calculate font metrics
	w.updateFontMetrics()

	// Set minimum size (small fixed value to allow flexible resizing)
	w.widget.SetMinimumSize2(100, 50)

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
		w.keyPressEvent(super, event)
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
	w.contextMenu = qt.NewQMenu(w.widget)

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
		w.contextMenu.ExecWithPos(w.widget.MapToGlobal(pos))
	})

	return w
}

// initScrollbar creates the scrollbars lazily (called on first resize)
func (w *Widget) initScrollbar() {
	if w.scrollbar != nil {
		return
	}
	w.scrollbarUpdating = true

	// Vertical scrollbar
	w.scrollbar = qt.NewQScrollBar(w.widget)
	w.scrollbar.SetOrientation(qt.Vertical)
	w.scrollbar.SetMinimum(0)
	w.scrollbar.SetMaximum(0)

	// Apply macOS-style scrollbar appearance for vertical
	w.scrollbar.SetStyleSheet(`
		QScrollBar:vertical {
			background: transparent;
			width: 12px;
			margin: 2px 2px 2px 0px;
		}
		QScrollBar::handle:vertical {
			background: rgba(128, 128, 128, 0.5);
			min-height: 30px;
			border-radius: 4px;
			margin: 0px 2px 0px 2px;
		}
		QScrollBar::handle:vertical:hover {
			background: rgba(128, 128, 128, 0.7);
		}
		QScrollBar::handle:vertical:pressed {
			background: rgba(100, 100, 100, 0.8);
		}
		QScrollBar::add-line:vertical, QScrollBar::sub-line:vertical {
			height: 0px;
		}
		QScrollBar::add-page:vertical, QScrollBar::sub-page:vertical {
			background: transparent;
		}
	`)

	w.scrollbar.OnValueChanged(func(value int) {
		if !w.scrollbarUpdating {
			maxScroll := w.scrollbar.Maximum()
			w.buffer.SetScrollOffset(maxScroll - value)
			// Don't snap here - let scrollbar move smoothly
			// The visual interpretation handles the magnetic zone
		}
	})

	// Horizontal scrollbar
	w.horizScrollbar = qt.NewQScrollBar(w.widget)
	w.horizScrollbar.SetOrientation(qt.Horizontal)
	w.horizScrollbar.SetMinimum(0)
	w.horizScrollbar.SetMaximum(0)

	// Apply macOS-style scrollbar appearance for horizontal
	w.horizScrollbar.SetStyleSheet(`
		QScrollBar:horizontal {
			background: transparent;
			height: 12px;
			margin: 0px 2px 2px 2px;
		}
		QScrollBar::handle:horizontal {
			background: rgba(128, 128, 128, 0.5);
			min-width: 30px;
			border-radius: 4px;
			margin: 2px 0px 2px 0px;
		}
		QScrollBar::handle:horizontal:hover {
			background: rgba(128, 128, 128, 0.7);
		}
		QScrollBar::handle:horizontal:pressed {
			background: rgba(100, 100, 100, 0.8);
		}
		QScrollBar::add-line:horizontal, QScrollBar::sub-line:horizontal {
			width: 0px;
		}
		QScrollBar::add-page:horizontal, QScrollBar::sub-page:horizontal {
			background: transparent;
		}
	`)

	w.horizScrollbar.OnValueChanged(func(value int) {
		if !w.scrollbarUpdating {
			w.buffer.SetHorizOffset(value)
			w.widget.Update()
		}
	})

	// Initially hide horizontal scrollbar
	w.horizScrollbar.Hide()

	w.scrollbarUpdating = false
}

// QWidget returns the terminal widget
func (w *Widget) QWidget() *qt.QWidget {
	return w.widget
}

// updateScrollbar updates the scrollbar to match the buffer state
func (w *Widget) updateScrollbar() {
	if w.scrollbar == nil {
		return
	}
	w.scrollbarUpdating = true
	defer func() { w.scrollbarUpdating = false }()

	scrollbackSize := w.buffer.GetScrollbackSize()
	scrollOffset := w.buffer.GetScrollOffset()

	w.scrollbar.SetMaximum(scrollbackSize)
	w.scrollbar.SetValue(scrollbackSize - scrollOffset)

	// Set page step to terminal height in lines
	_, rows := w.buffer.GetSize()
	w.scrollbar.SetPageStep(rows)
}

// updateHorizScrollbar updates the horizontal scrollbar visibility and range
func (w *Widget) updateHorizScrollbar() {
	if w.horizScrollbar == nil {
		return
	}
	w.scrollbarUpdating = true
	defer func() { w.scrollbarUpdating = false }()

	if w.buffer.NeedsHorizScrollbar() {
		maxOffset := w.buffer.GetMaxHorizOffset()
		currentOffset := w.buffer.GetHorizOffset()

		w.horizScrollbar.SetMaximum(maxOffset)
		w.horizScrollbar.SetValue(currentOffset)

		// Set page step to visible width in columns
		cols, _ := w.buffer.GetSize()
		w.horizScrollbar.SetPageStep(cols)

		// Always update geometry when showing - widget size may have changed
		scrollbarWidth := 12
		scrollbarHeight := 12
		widgetWidth := w.widget.Width()
		widgetHeight := w.widget.Height()
		horizWidth := widgetWidth - scrollbarWidth
		w.horizScrollbar.SetGeometry(0, widgetHeight-scrollbarHeight, horizWidth, scrollbarHeight)

		w.horizScrollbar.Show()
	} else {
		// Reset offset and hide scrollbar
		w.buffer.SetHorizOffset(0)
		w.horizScrollbar.SetMaximum(0)
		w.horizScrollbar.SetValue(0)
		w.horizScrollbar.Hide()
	}
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

// SetFontFallbacks sets the fallback fonts for Unicode and CJK characters.
// These are used when the main font doesn't have a glyph for a character.
func (w *Widget) SetFontFallbacks(unicodeFont, cjkFont string) {
	// Resolve font families (Qt handles comma-separated lists itself)
	resolvedUnicode := resolveFirstAvailableFont(unicodeFont)
	resolvedCJK := resolveFirstAvailableFont(cjkFont)

	w.mu.Lock()
	w.fontFamilyUnicode = resolvedUnicode
	w.fontFamilyCJK = resolvedCJK
	w.mu.Unlock()
}

// resolveFirstAvailableFont takes a comma-separated list of font families
// and returns the first one that is available on the system.
func resolveFirstAvailableFont(fontList string) string {
	if fontList == "" {
		return ""
	}

	// Parse comma-separated list and find first available by testing if Qt can load it
	parts := splitFontList(fontList)
	for _, part := range parts {
		// Try to create a font with this family and check if it resolves
		testFont := qt.NewQFont6(part, 12)
		info := qt.NewQFontInfo(testFont)
		// If the family name matches (approximately), the font is available
		if info.Family() == part || len(parts) == 1 {
			return part
		}
	}

	// Fallback to first in list if none found
	if len(parts) > 0 {
		return parts[0]
	}

	return fontList
}

// splitFontList splits a comma-separated font list and trims whitespace
func splitFontList(fontList string) []string {
	var result []string
	var current string
	for _, c := range fontList {
		if c == ',' {
			trimmed := trimSpace(current)
			if trimmed != "" {
				result = append(result, trimmed)
			}
			current = ""
		} else {
			current += string(c)
		}
	}
	trimmed := trimSpace(current)
	if trimmed != "" {
		result = append(result, trimmed)
	}
	return result
}

// trimSpace removes leading and trailing whitespace
func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

// isCJKCharacter returns true if the rune is a CJK character
func isCJKCharacter(r rune) bool {
	// CJK Unified Ideographs
	if r >= 0x4E00 && r <= 0x9FFF {
		return true
	}
	// CJK Unified Ideographs Extension A
	if r >= 0x3400 && r <= 0x4DBF {
		return true
	}
	// CJK Unified Ideographs Extension B-F
	if r >= 0x20000 && r <= 0x2CEAF {
		return true
	}
	// Hiragana
	if r >= 0x3040 && r <= 0x309F {
		return true
	}
	// Katakana
	if r >= 0x30A0 && r <= 0x30FF {
		return true
	}
	// Hangul Syllables
	if r >= 0xAC00 && r <= 0xD7AF {
		return true
	}
	// Hangul Jamo
	if r >= 0x1100 && r <= 0x11FF {
		return true
	}
	// CJK Symbols and Punctuation
	if r >= 0x3000 && r <= 0x303F {
		return true
	}
	// Halfwidth and Fullwidth Forms
	if r >= 0xFF00 && r <= 0xFFEF {
		return true
	}
	// Bopomofo
	if r >= 0x3100 && r <= 0x312F {
		return true
	}
	return false
}

// fontHasGlyph checks if a font can render the given character
func fontHasGlyph(fontFamily string, fontSize int, r rune) bool {
	font := qt.NewQFont6(fontFamily, fontSize)
	metrics := qt.NewQFontMetrics(font)
	// Check if the font has a glyph by seeing if it has a non-zero advance
	// and the resolved font family matches what we requested
	charStr := string(r)
	advance := metrics.HorizontalAdvance(charStr)
	if advance <= 0 {
		return false
	}
	// Additional check: see if Qt substituted a different font
	info := qt.NewQFontInfo(font)
	return info.Family() == fontFamily
}

// getFontForCharacter returns the appropriate font family for a character
func (w *Widget) getFontForCharacter(r rune, mainFont string, fontSize int) string {
	// ASCII characters always use main font
	if r < 128 {
		return mainFont
	}

	// Check if main font has this character
	if fontHasGlyph(mainFont, fontSize, r) {
		return mainFont
	}

	w.mu.Lock()
	unicodeFont := w.fontFamilyUnicode
	cjkFont := w.fontFamilyCJK
	w.mu.Unlock()

	// Use CJK font for CJK characters
	if isCJKCharacter(r) && cjkFont != "" {
		return cjkFont
	}

	// Use Unicode fallback for other characters
	if unicodeFont != "" {
		return unicodeFont
	}

	// Fall back to main font
	return mainFont
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

// Flush forces an immediate repaint of the widget
// This bypasses the update timer coalescing for cases where
// immediate visual feedback is needed (e.g., before blocking operations)
func (w *Widget) Flush() {
	w.updatePending = false
	w.widget.Repaint()
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

// renderCustomGlyph renders a custom glyph for a cell at the specified position
// Returns true if a custom glyph was rendered, false if normal text rendering should be used
// createCustomGlyphPixmap renders a custom glyph to a cached QPixmap.
// The pixmap is rendered at the specified cell size with all palette colors resolved.
// scaleY is used for double-height mode (1.0 for normal, 2.0 for double-height).
func (w *Widget) createCustomGlyphPixmap(cell *purfecterm.Cell, glyph *purfecterm.CustomGlyph,
	cellW, cellH int, scaleY float64) *qt.QPixmap {

	glyphW := glyph.Width
	glyphH := glyph.Height

	// Calculate pixmap dimensions (account for scaleY for double-height)
	pixmapH := int(float64(cellH) * scaleY)
	pixmap := qt.NewQPixmap2(cellW, pixmapH)
	pixmap.FillWithFillColor(qt.NewQColor2(qt.Transparent))

	painter := qt.NewQPainter2(pixmap.QPaintDevice)

	// Calculate pixel size (scale glyph to fill cell)
	pixelW := float64(cellW) / float64(glyphW)
	pixelH := float64(pixmapH) / float64(glyphH)

	// Render each pixel
	for gy := 0; gy < glyphH; gy++ {
		for gx := 0; gx < glyphW; gx++ {
			// Get palette index for this pixel
			paletteIdx := glyph.GetPixel(gx, gy)

			// Apply XFlip/YFlip
			drawX := gx
			drawY := gy
			if cell.XFlip {
				drawX = glyphW - 1 - gx
			}
			if cell.YFlip {
				drawY = glyphH - 1 - gy
			}

			// Calculate position on pixmap
			px := float64(drawX) * pixelW
			py := float64(drawY) * pixelH

			// Check for adjacent non-transparent pixels to hide seams
			rightNeighborIdx := glyph.GetPixel(gx+1, gy)
			belowNeighborIdx := glyph.GetPixel(gx, gy+1)

			// Extend pixel to cover seams
			drawW := pixelW
			drawH := pixelH
			if rightNeighborIdx != 0 {
				drawW += 1
			}
			if belowNeighborIdx != 0 {
				drawH += 1
			}

			// Resolve color from palette
			color, _ := w.buffer.ResolveGlyphColor(cell, paletteIdx)

			// Draw pixel
			qColor := qt.NewQColor3(int(color.R), int(color.G), int(color.B))
			painter.FillRect5(int(px), int(py), int(drawW+0.5), int(drawH+0.5), qColor)
		}
	}

	painter.End()
	return pixmap
}

// renderCustomGlyph renders a custom glyph for a cell at the specified position.
// Uses the glyph cache for performance - cache hits just blit the pre-rendered pixmap.
// Returns true if a custom glyph was rendered, false if normal text rendering should be used.
func (w *Widget) renderCustomGlyph(painter *qt.QPainter, cell *purfecterm.Cell, cellX, cellY, cellW, cellH int, cellCol int, blinkPhase float64, blinkMode purfecterm.BlinkMode, lineAttr purfecterm.LineAttribute) bool {
	glyph := w.buffer.GetGlyph(cell.Char)
	if glyph == nil {
		return false
	}

	// Calculate pixel dimensions
	glyphW := glyph.Width
	glyphH := glyph.Height
	if glyphW == 0 || glyphH == 0 {
		return false
	}

	// Calculate wave offset for blink bounce mode
	yOffset := 0.0
	if cell.Blink && blinkMode == purfecterm.BlinkModeBounce {
		wavePhase := blinkPhase + float64(cellCol)*0.5
		yOffset = math.Sin(wavePhase) * 3.0
	}

	// Handle double-height lines by clipping and scaling
	renderY := float64(cellY) + yOffset
	scaleY := 1.0
	clipNeeded := false

	switch lineAttr {
	case purfecterm.LineAttrDoubleWidth:
		// Just 2x horizontal, already handled by cellW being doubled
	case purfecterm.LineAttrDoubleTop:
		// Show top half of glyph, scaled 2x vertically
		scaleY = 2.0
		clipNeeded = true
	case purfecterm.LineAttrDoubleBottom:
		// Show bottom half of glyph, scaled 2x vertically
		scaleY = 2.0
		renderY = float64(cellY) - float64(cellH) + yOffset // Shift up so bottom half is visible
		clipNeeded = true
	}

	// Get palette info for cache key
	paletteNum := cell.BGP
	if paletteNum < 0 {
		paletteNum = w.buffer.ColorToANSICode(cell.Foreground)
	}
	palette := w.buffer.GetPalette(paletteNum)

	// Determine cache key flags based on palette characteristics
	var paletteHash uint64
	usesDefaultFG := true  // Default to true for fallback mode (no palette)
	usesBg := true         // Default to true for fallback mode
	isSingleEntry := false

	if palette != nil {
		paletteHash = palette.ComputeHash()
		usesDefaultFG = palette.UsesDefaultFG
		usesBg = palette.UsesBg
		isSingleEntry = len(palette.Entries) == 1
	}

	// Single-entry palettes always use background for index 0
	if isSingleEntry {
		usesBg = true
	}

	// Build cache key
	cacheKey := buildCustomGlyphKey(
		cell.Char,
		cellW, int(float64(cellH)*scaleY),
		cell.XFlip, cell.YFlip,
		paletteHash, glyph.ComputeHash(),
		usesDefaultFG, usesBg,
		cell.Foreground, cell.Background,
	)

	// Try cache lookup
	cachedPixmap := w.glyphCache.get(cacheKey)
	if cachedPixmap == nil {
		// Cache miss - create and cache the pixmap
		cachedPixmap = w.createCustomGlyphPixmap(cell, glyph, cellW, cellH, scaleY)
		w.glyphCache.put(cacheKey, cachedPixmap)
	}

	// Apply clipping for double-height lines
	if clipNeeded {
		painter.Save()
		painter.SetClipRect2(cellX, cellY, cellW, cellH)
	}

	// Blit the cached pixmap at the target position
	painter.DrawPixmap9(cellX, int(renderY), cachedPixmap)

	// Restore clipping state if we applied it
	if clipNeeded {
		painter.Restore()
	}

	return true
}

// spriteCoordToPixels converts a sprite coordinate to pixel position without rounding error accumulation.
// coordinate: sprite coordinate in subdivision units (e.g., 26.5)
// unitsPerCell: number of subdivisions per cell (e.g., 8)
// cellSize: pixel size of one cell (e.g., charWidth or charHeight)
// Returns: wholeCells * cellSize + remainderUnits * (cellSize / unitsPerCell)
func spriteCoordToPixelsQt(coordinate float64, unitsPerCell int, cellSize int) float64 {
	// Calculate whole cells first to avoid accumulating rounding errors
	wholeCells := int(coordinate) / unitsPerCell
	remainderUnits := coordinate - float64(wholeCells*unitsPerCell)
	return float64(wholeCells*cellSize) + remainderUnits*float64(cellSize)/float64(unitsPerCell)
}

// renderSprites renders a list of sprites at their positions
func (w *Widget) renderSprites(painter *qt.QPainter, sprites []*purfecterm.Sprite, charWidth, charHeight int, scheme purfecterm.ColorScheme, scrollOffsetY, horizOffsetX int) {
	if len(sprites) == 0 {
		return
	}

	unitX, unitY := w.buffer.GetSpriteUnits()

	for _, sprite := range sprites {
		w.renderSprite(painter, sprite, unitX, unitY, charWidth, charHeight, scheme, scrollOffsetY, horizOffsetX)
	}
}

// renderSprite renders a single sprite
// unitX, unitY are subdivisions per cell (e.g., 8 means 8 subdivisions per character cell)
func (w *Widget) renderSprite(painter *qt.QPainter, sprite *purfecterm.Sprite, unitX, unitY int, charWidth, charHeight int, scheme purfecterm.ColorScheme, scrollOffsetY, horizOffsetX int) {
	if sprite == nil || len(sprite.Runes) == 0 {
		return
	}

	// Get crop rectangle if specified
	var cropRect *purfecterm.CropRectangle
	if sprite.CropRect >= 0 {
		cropRect = w.buffer.GetCropRect(sprite.CropRect)
	}

	// Calculate scroll offset in pixels
	scrollPixelY := float64(scrollOffsetY * charHeight)
	scrollPixelX := float64(horizOffsetX * charWidth)

	// Calculate base position in pixels (relative to visible area)
	// Use spriteCoordToPixelsQt to avoid accumulating rounding errors
	basePixelX := spriteCoordToPixelsQt(sprite.X, unitX, charWidth) + float64(terminalLeftPadding) - scrollPixelX
	basePixelY := spriteCoordToPixelsQt(sprite.Y, unitY, charHeight) + scrollPixelY

	// Determine the total sprite dimensions in tiles
	spriteRows := len(sprite.Runes)
	spriteCols := 0
	for _, row := range sprite.Runes {
		if len(row) > spriteCols {
			spriteCols = len(row)
		}
	}

	// Calculate tile size: XScale/YScale are in cell units (XScale=1 means one tile fills one cell)
	tileW := float64(charWidth) * sprite.XScale
	tileH := float64(charHeight) * sprite.YScale

	// Get flip flags
	xFlip := sprite.GetXFlip()
	yFlip := sprite.GetYFlip()

	// Render each tile in the sprite
	for rowIdx, row := range sprite.Runes {
		for colIdx, r := range row {
			if r == 0 || r == ' ' {
				continue
			}

			// Calculate tile position
			tileX := colIdx
			tileY := rowIdx

			// Apply sprite-level flip
			if xFlip {
				tileX = spriteCols - 1 - colIdx
			}
			if yFlip {
				tileY = spriteRows - 1 - rowIdx
			}

			// Calculate pixel position for this tile
			pixelX := basePixelX + float64(tileX)*tileW
			pixelY := basePixelY + float64(tileY)*tileH

			// Apply crop rectangle if specified (relative to logical screen)
			if cropRect != nil {
				cropMinX := spriteCoordToPixelsQt(cropRect.MinX, unitX, charWidth) + float64(terminalLeftPadding) - scrollPixelX
				cropMinY := spriteCoordToPixelsQt(cropRect.MinY, unitY, charHeight) + scrollPixelY
				cropMaxX := spriteCoordToPixelsQt(cropRect.MaxX, unitX, charWidth) + float64(terminalLeftPadding) - scrollPixelX
				cropMaxY := spriteCoordToPixelsQt(cropRect.MaxY, unitY, charHeight) + scrollPixelY

				if pixelX+tileW <= cropMinX || pixelX >= cropMaxX ||
					pixelY+tileH <= cropMinY || pixelY >= cropMaxY {
					continue
				}
			}

			// Get glyph for this character
			glyph := w.buffer.GetGlyph(r)
			if glyph == nil {
				continue
			}

			// Render the glyph at this position
			w.renderSpriteGlyph(painter, glyph, sprite, pixelX, pixelY, tileW, tileH, scheme, cropRect, unitX, unitY, charWidth, charHeight, scrollPixelX, scrollPixelY)
		}
	}
}

// renderSpriteGlyph renders a single glyph within a sprite tile
func (w *Widget) renderSpriteGlyph(painter *qt.QPainter, glyph *purfecterm.CustomGlyph, sprite *purfecterm.Sprite,
	tileX, tileY, tileW, tileH float64, scheme purfecterm.ColorScheme,
	cropRect *purfecterm.CropRectangle, unitX, unitY int, charWidth, charHeight int, scrollPixelX, scrollPixelY float64) {

	glyphW := glyph.Width
	glyphH := glyph.Height
	if glyphW == 0 || glyphH == 0 {
		return
	}

	// Calculate pixel size within the tile
	pixelW := tileW / float64(glyphW)
	pixelH := tileH / float64(glyphH)

	// Calculate crop bounds in pixels if needed (relative to logical screen)
	var cropMinX, cropMinY, cropMaxX, cropMaxY float64
	hasCrop := cropRect != nil
	if hasCrop {
		cropMinX = spriteCoordToPixelsQt(cropRect.MinX, unitX, charWidth) + float64(terminalLeftPadding) - scrollPixelX
		cropMinY = spriteCoordToPixelsQt(cropRect.MinY, unitY, charHeight) + scrollPixelY
		cropMaxX = spriteCoordToPixelsQt(cropRect.MaxX, unitX, charWidth) + float64(terminalLeftPadding) - scrollPixelX
		cropMaxY = spriteCoordToPixelsQt(cropRect.MaxY, unitY, charHeight) + scrollPixelY
	}

	// Determine default foreground color for this sprite
	defaultFg := scheme.Foreground
	defaultBg := scheme.Background

	// Render each pixel of the glyph
	for gy := 0; gy < glyphH; gy++ {
		for gx := 0; gx < glyphW; gx++ {
			paletteIdx := glyph.GetPixel(gx, gy)

			px := tileX + float64(gx)*pixelW
			py := tileY + float64(gy)*pixelH

			// Check for adjacent non-transparent pixels to hide seams
			rightNeighborIdx := glyph.GetPixel(gx+1, gy)
			belowNeighborIdx := glyph.GetPixel(gx, gy+1)

			// Apply crop if specified
			if hasCrop {
				if px+pixelW <= cropMinX || px >= cropMaxX ||
					py+pixelH <= cropMinY || py >= cropMaxY {
					continue
				}
			}

			// Resolve color using sprite's FGP
			color, visible := w.buffer.ResolveSpriteGlyphColor(sprite.FGP, paletteIdx, defaultFg, defaultBg)
			if !visible {
				continue
			}

			qColor := qt.NewQColor3(int(color.R), int(color.G), int(color.B))

			// Draw main pixel
			painter.FillRect5(int(px), int(py), int(pixelW+0.5), int(pixelH+0.5), qColor)

			// Draw seam extensions as separate strips (1 screen pixel each)
			// to prevent hairline gaps without creating corner artifacts
			if rightNeighborIdx != 0 {
				// Right extension: 1 screen pixel wide strip
				painter.FillRect5(int(px+pixelW), int(py), 1, int(pixelH+0.5), qColor)
			}
			if belowNeighborIdx != 0 {
				// Bottom extension: 1 screen pixel tall strip
				painter.FillRect5(int(px), int(py+pixelH), int(pixelW+0.5), 1, qColor)
			}
		}
	}
}

// renderScreenSplits renders screen split regions using a scanline approach.
// Iterates through each sprite-unit Y position and renders rows as boundaries are encountered.
// Split ScreenY values are LOGICAL scanline numbers relative to the scroll boundary (yellow dotted line).
// The first logical scanline (0) begins after the scrollback area.
func (w *Widget) renderScreenSplits(painter *qt.QPainter, splits []*purfecterm.ScreenSplit,
	cols, rows, charWidth, charHeight, unitX, unitY int,
	fontFamily string, fontSize int, scheme purfecterm.ColorScheme, blinkPhase float64,
	cursorVisible bool, cursorVisibleX, cursorVisibleY int, cursorShape int,
	horizScale, vertScale float64, scrollOffset, horizOffset int) int {

	// Return value: max content width found in splits (for horizontal scrollbar)
	maxSplitContentWidth := 0

	// Get screen crop (in sprite units, -1 = no crop)
	widthCrop, _ := w.buffer.GetScreenCrop()

	// Convert width crop from sprite units to columns (if set)
	cropCols := -1
	if widthCrop > 0 {
		cropCols = widthCrop / unitX
	}

	// Calculate where the logical screen starts (in visible rows)
	// This is where the yellow dotted line appears
	boundaryRow := w.buffer.GetScrollbackBoundaryVisibleRow()

	// If scrolled fully into scrollback (logical screen not visible), don't render splits
	if scrollOffset > 0 && boundaryRow < 0 {
		return 0
	}

	// Logical screen starts at boundaryRow if visible, else at row 0
	logicalScreenStartRow := 0
	if boundaryRow > 0 {
		logicalScreenStartRow = boundaryRow
	}

	// Calculate the pixel offset where logical screen starts
	logicalScreenStartPixelY := logicalScreenStartRow * charHeight

	// Screen height in sprite units (only the logical screen portion)
	logicalScreenRows := rows - logicalScreenStartRow
	screenHeightUnits := logicalScreenRows * unitY

	// Track which splits have had their backgrounds cleared
	splitBackgroundCleared := make(map[int]bool)

	// Set up font once
	font := qt.NewQFont6(fontFamily, fontSize)
	font.SetFixedPitch(true)
	painter.SetFont(font)

	// Track current split as we iterate through scanlines
	// Splits are sorted by ScreenY, so we advance through them linearly
	currentSplitIdx := -1
	var currentSplit *purfecterm.ScreenSplit
	nextSplitBoundary := 0 // Y where next split begins
	splitEndY := screenHeightUnits

	// Find first split (if any starts at Y=0)
	if len(splits) > 0 && splits[0].ScreenY == 0 {
		currentSplitIdx = 0
		currentSplit = splits[0]
		if len(splits) > 1 {
			nextSplitBoundary = splits[1].ScreenY
			splitEndY = splits[1].ScreenY
		} else {
			nextSplitBoundary = screenHeightUnits
			splitEndY = screenHeightUnits
		}
	} else if len(splits) > 0 {
		nextSplitBoundary = splits[0].ScreenY
	} else {
		nextSplitBoundary = screenHeightUnits
	}

	// Iterate through each sprite-unit Y position (scanline approach)
	for y := 0; y < screenHeightUnits; y++ {
		// Check if we've crossed into a new split
		if y >= nextSplitBoundary {
			// Advance to the split that starts here
			for i := currentSplitIdx + 1; i < len(splits); i++ {
				if splits[i].ScreenY <= y {
					currentSplitIdx = i
					currentSplit = splits[i]
				} else {
					break
				}
			}
			// Update next boundary
			if currentSplitIdx+1 < len(splits) {
				nextSplitBoundary = splits[currentSplitIdx+1].ScreenY
				splitEndY = splits[currentSplitIdx+1].ScreenY
			} else {
				nextSplitBoundary = screenHeightUnits
				splitEndY = screenHeightUnits
			}
		}

		// Skip if no split at this position or if it's the main screen (ScreenY=0, not overriding)
		if currentSplit == nil || (currentSplit.ScreenY == 0 && currentSplit.BufferRow == 0 && currentSplit.BufferCol == 0 &&
			currentSplit.TopFineScroll == 0 && currentSplit.LeftFineScroll == 0) {
			continue
		}

		// Clear background for this split if not yet done
		if !splitBackgroundCleared[currentSplitIdx] {
			splitBackgroundCleared[currentSplitIdx] = true

			// Calculate pixel coordinates (offset by logical screen start)
			startPixelY := logicalScreenStartPixelY + currentSplit.ScreenY*charHeight/unitY
			endPixelY := logicalScreenStartPixelY + splitEndY*charHeight/unitY

			painter.Save()
			painter.SetClipRect2(0, startPixelY, cols*charWidth+terminalLeftPadding, endPixelY-startPixelY)
			bgColor := qt.NewQColor3(int(scheme.Background.R), int(scheme.Background.G), int(scheme.Background.B))
			painter.FillRect5(0, startPixelY, cols*charWidth+terminalLeftPadding, endPixelY-startPixelY, bgColor)
			painter.Restore()
		}

		// Check if this Y marks a row boundary for this split
		relativeY := y - currentSplit.ScreenY + currentSplit.TopFineScroll
		if relativeY < 0 || relativeY%unitY != 0 {
			continue
		}

		// Calculate which row to render within this split
		rowInSplit := relativeY / unitY

		// Calculate fine scroll offsets in pixels
		fineOffsetY := currentSplit.TopFineScroll * charHeight / unitY
		fineOffsetX := currentSplit.LeftFineScroll * charWidth / unitX

		// Calculate pixel Y position for this row (offset by logical screen start)
		rowPixelY := logicalScreenStartPixelY + y*charHeight/unitY - fineOffsetY

		// Set up clipping for this split region (offset by logical screen start)
		// Clip horizontally at terminalLeftPadding to properly handle LeftFineScroll
		startPixelY := logicalScreenStartPixelY + currentSplit.ScreenY*charHeight/unitY
		endPixelY := logicalScreenStartPixelY + splitEndY*charHeight/unitY

		painter.Save()
		painter.SetClipRect2(terminalLeftPadding, startPixelY, cols*charWidth, endPixelY-startPixelY)

		// Get line attribute for this buffer row
		lineAttr := w.buffer.GetLineAttributeForSplit(rowInSplit, currentSplit.BufferRow)

		effectiveCols := cols
		if lineAttr != purfecterm.LineAttrNormal {
			effectiveCols = cols / 2
		}

		// Get the content length for this row (excluding content before BufferCol)
		contentLen := w.buffer.GetLineLengthForSplit(rowInSplit, currentSplit.BufferRow, currentSplit.BufferCol)

		// Determine where to stop rendering:
		// - At screen edge (effectiveCols)
		// - At end of content (contentLen)
		// - At crop boundary (cropCols) if set
		maxRenderCol := effectiveCols
		if contentLen < maxRenderCol {
			maxRenderCol = contentLen
		}
		if cropCols > 0 && cropCols < maxRenderCol {
			maxRenderCol = cropCols
		}

		// Track max content width across all split rows (for horizontal scrollbar)
		// This is the effective line length, not limited by screen width
		rowContentWidth := contentLen
		if cropCols > 0 && cropCols < rowContentWidth {
			rowContentWidth = cropCols
		}
		if rowContentWidth > maxSplitContentWidth {
			maxSplitContentWidth = rowContentWidth
		}

		// Render each cell in this row
		// All cells are shifted left by fineOffsetX; the clip rect at terminalLeftPadding
		// will clip the left portion of the first cell when LeftFineScroll > 0
		// horizOffset accounts for the global horizontal scroll position
		for screenCol := 0; screenCol < maxRenderCol; screenCol++ {
			cell := w.buffer.GetCellForSplit(screenCol+horizOffset, rowInSplit, currentSplit.BufferRow, currentSplit.BufferCol)

			// Calculate cell position (shifted left by fine scroll)
			var cellX, cellW int
			cellH := charHeight

			if lineAttr != purfecterm.LineAttrNormal {
				cellX = screenCol*charWidth*2 + terminalLeftPadding - fineOffsetX
				cellW = charWidth * 2
			} else {
				cellX = screenCol*charWidth + terminalLeftPadding - fineOffsetX
				cellW = charWidth
			}

			// Skip cells that are entirely off the right edge
			if cellX >= terminalLeftPadding+cols*charWidth {
				break
			}

			// Skip cells that are entirely off the left edge (before the clip region)
			if cellX+cellW <= terminalLeftPadding {
				continue
			}

			fg := cell.Foreground
			bg := cell.Background
			if fg.Default {
				fg = scheme.Foreground
			}
			if bg.Default {
				bg = scheme.Background
			}

			// Draw cell background if different from terminal background
			if bg != scheme.Background {
				bgQColor := qt.NewQColor3(int(bg.R), int(bg.G), int(bg.B))
				painter.FillRect5(cellX, rowPixelY, cellW, cellH, bgQColor)
			}

			// Draw character
			if cell.Char != ' ' && cell.Char != 0 {
				fgQColor := qt.NewQColor3(int(fg.R), int(fg.G), int(fg.B))
				pen := qt.NewQPen3(fgQColor)
				painter.SetPenWithPen(pen)
				painter.DrawText3(cellX, rowPixelY+charHeight*3/4, cell.String())
			}
		}

		painter.Restore()

		// Optimization: skip ahead to the next potential row boundary
		nextRowY := y + unitY - (relativeY % unitY)
		if nextRowY > y+1 && nextRowY < splitEndY {
			y = nextRowY - 1
		}
	}

	return maxSplitContentWidth
}

func (w *Widget) paintEvent(event *qt.QPaintEvent) {
	w.mu.Lock()
	scheme := w.scheme
	fontFamily := w.fontFamily
	fontSize := w.fontSize
	baseCharWidth := w.charWidth
	baseCharHeight := w.charHeight
	baseCharAscent := w.charAscent
	blinkPhase := w.blinkPhase
	w.mu.Unlock()

	cols, rows := w.buffer.GetSize()
	cursorVisible := w.buffer.IsCursorVisible()
	cursorShape, _ := w.buffer.GetCursorStyle()
	scrollOffset := w.buffer.GetEffectiveScrollOffset()
	horizOffset := w.buffer.GetHorizOffset()

	// Get cursor's visible position (accounting for scroll offset)
	cursorVisibleX, cursorVisibleY := w.buffer.GetCursorVisiblePosition()
	if cursorVisibleX < 0 || cursorVisibleY < 0 {
		cursorVisible = false
	}

	// Get screen scaling factors
	horizScale := w.buffer.GetHorizontalScale()
	vertScale := w.buffer.GetVerticalScale()

	// Apply scaling to character dimensions
	charWidth := int(float64(baseCharWidth) * horizScale)
	charHeight := int(float64(baseCharHeight) * vertScale)

	painter := qt.NewQPainter2(w.widget.QPaintDevice)
	defer painter.End()

	// Fill background
	bgColor := qt.NewQColor3(int(scheme.Background.R), int(scheme.Background.G), int(scheme.Background.B))
	painter.FillRect5(0, 0, w.widget.Width(), w.widget.Height(), bgColor)

	// Apply screen crop clipping if set (crop values are in sprite coordinate units)
	widthCrop, heightCrop := w.buffer.GetScreenCrop()
	unitX, unitY := w.buffer.GetSpriteUnits()
	hasCrop := widthCrop > 0 || heightCrop > 0
	if hasCrop {
		painter.Save()
		cropW := w.widget.Width()
		cropH := w.widget.Height()
		if widthCrop > 0 {
			cropW = widthCrop*charWidth/unitX + terminalLeftPadding
		}
		if heightCrop > 0 {
			cropH = heightCrop * charHeight / unitY
		}
		painter.SetClipRect2(0, 0, cropW, cropH)
	}

	// Get sprites for rendering (behind = negative Z, front = non-negative Z)
	behindSprites, frontSprites := w.buffer.GetSpritesForRendering()

	// Render behind sprites (visible where text has default background)
	w.renderSprites(painter, behindSprites, charWidth, charHeight, scheme, scrollOffset, horizOffset)

	// Set up font
	font := qt.NewQFont6(fontFamily, fontSize)
	font.SetFixedPitch(true)
	painter.SetFont(font)

	// Track whether cursor was drawn in this frame (for auto-scroll)
	cursorWasDrawn := false

	// Draw each cell
	for y := 0; y < rows; y++ {
		lineAttr := w.buffer.GetVisibleLineAttribute(y)

		// For rendering, we need to consider horizontal offset
		// Draw visible columns from horizOffset to horizOffset + cols
		effectiveCols := cols
		if lineAttr != purfecterm.LineAttrNormal {
			effectiveCols = cols / 2
		}

		// Calculate the range of logical columns to render
		startCol := horizOffset
		endCol := horizOffset + effectiveCols

		// Track accumulated visual width for flex-width rendering
		visibleAccumulatedWidth := 0.0

		for logicalX := startCol; logicalX < endCol; logicalX++ {
			// Screen position (0-based from visible area)
			x := logicalX - horizOffset
			// GetVisibleCell takes screen position and applies horizOffset internally
			cell := w.buffer.GetVisibleCell(x, y)

			// Calculate this cell's visual width
			cellVisualWidth := 1.0
			if cell.FlexWidth && cell.CellWidth > 0 {
				cellVisualWidth = cell.CellWidth
			}

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

			// Handle selection (use logicalX for buffer position)
			if w.buffer.IsInSelection(logicalX, y) {
				bg = scheme.Selection
			}

			// Handle cursor (compare against logical position)
			isCursor := cursorVisible && x == cursorVisibleX && y == cursorVisibleY && w.cursorBlinkOn
			if isCursor && w.hasFocus && cursorShape == 0 {
				fg, bg = bg, fg
			}

			// Calculate cell position and size based on line attributes and flex width
			var cellX, cellY, cellW, cellH int
			switch lineAttr {
			case purfecterm.LineAttrNormal:
				// Use accumulated width for X position when cells have flex width
				cellX = int(visibleAccumulatedWidth*float64(charWidth)) + terminalLeftPadding
				cellY = y * charHeight
				cellW = int(cellVisualWidth * float64(charWidth))
				cellH = charHeight
			case purfecterm.LineAttrDoubleWidth:
				// Each character takes up 2x its normal width
				cellX = int(visibleAccumulatedWidth*2.0*float64(charWidth)) + terminalLeftPadding
				cellY = y * charHeight
				cellW = int(cellVisualWidth * float64(charWidth) * 2.0)
				cellH = charHeight
			case purfecterm.LineAttrDoubleTop, purfecterm.LineAttrDoubleBottom:
				// Each character takes up 2x its normal width, text is rendered 2x height
				cellX = int(visibleAccumulatedWidth*2.0*float64(charWidth)) + terminalLeftPadding
				cellY = y * charHeight
				cellW = int(cellVisualWidth * float64(charWidth) * 2.0)
				cellH = charHeight
			}

			// Track accumulated width for next cell
			_ = x // x is still useful for wave animation phase calculation
			visibleAccumulatedWidth += cellVisualWidth

			// Draw background if different from terminal background
			if bg != scheme.Background {
				bgQColor := qt.NewQColor3(int(bg.R), int(bg.G), int(bg.B))
				painter.FillRect5(cellX, cellY, cellW, cellH, bgQColor)
			}

			// Draw character
			if cell.Char != ' ' && cell.Char != 0 && blinkVisible {
				// Check for custom glyph first
				if w.renderCustomGlyph(painter, &cell, cellX, cellY, cellW, cellH, x, blinkPhase, scheme.BlinkMode, lineAttr) {
					// Custom glyph was rendered, skip normal text rendering
					goto afterCharRenderQt
				}

				fgQColor := qt.NewQColor3(int(fg.R), int(fg.G), int(fg.B))
				painter.SetPen(fgQColor)

				// Determine which font to use for this character (with fallback for Unicode/CJK)
				charFontFamily := w.getFontForCharacter(cell.Char, fontFamily, fontSize)

				// Create the appropriate font for this character
				var drawFont *qt.QFont
				if charFontFamily != fontFamily || cell.Bold {
					// Need a different font - either fallback or bold
					drawFont = qt.NewQFont6(charFontFamily, fontSize)
					drawFont.SetFixedPitch(charFontFamily == fontFamily) // Only fix pitch for main font
					if cell.Bold {
						drawFont.SetBold(true)
					}
					painter.SetFont(drawFont)
				} else {
					drawFont = font
				}

				// Measure actual character width
				metrics := qt.NewQFontMetrics(drawFont)
				charStr := cell.String() // Includes base char + any combining marks
				actualWidth := metrics.HorizontalAdvance(charStr)

				// Calculate bobbing wave offset
				yOffset := 0.0
				if cell.Blink && scheme.BlinkMode == purfecterm.BlinkModeBounce {
					wavePhase := blinkPhase + float64(x)*0.5
					yOffset = math.Sin(wavePhase) * 3.0
				}

				switch lineAttr {
				case purfecterm.LineAttrNormal:
					// Apply global screen scaling (132-column, 40-column, line density)
					// Characters are drawn at scaled size to fit in scaled cells
					painter.Save()

					// Calculate target cell width for flex width cells
					targetCellWidth := cellVisualWidth * float64(baseCharWidth)
					actualWidthF := float64(actualWidth)
					textScaleX := horizScale
					xOffset := 0.0
					if actualWidthF > targetCellWidth {
						// Wide char: squeeze to fit cell width, then apply global scale
						textScaleX *= targetCellWidth / actualWidthF
					} else if actualWidthF < targetCellWidth {
						if cellVisualWidth > 1.0 && purfecterm.IsAmbiguousWidth(cell.Char) {
							// Ambiguous width char in wide cell
							if purfecterm.IsBlockOrLineDrawing(cell.Char) {
								// Block/line drawing: full 2.0 stretch to connect properly
								textScaleX *= targetCellWidth / actualWidthF
							} else {
								// Other ambiguous (Cyrillic, Greek, etc.): 1.5x scale, centered
								textScaleX *= 1.5
								scaledWidth := actualWidthF * 1.5
								xOffset = (targetCellWidth - scaledWidth) / 2.0 * horizScale
							}
						} else {
							// Normal cell or actual wide char: center narrow char
							xOffset = (targetCellWidth - actualWidthF) / 2.0 * horizScale
						}
					}

					painter.Translate2(float64(cellX)+xOffset, float64(cellY)+float64(baseCharAscent)*vertScale+yOffset)
					painter.Scale(textScaleX, vertScale)
					painter.DrawText3(0, 0, charStr)
					painter.Restore()
				case purfecterm.LineAttrDoubleWidth:
					// Double-width line: 2x horizontal scale on top of global scaling
					// Cell is already 2x charWidth wide, text should fill it
					painter.Save()
					// Calculate target cell width for flex width cells
					targetCellWidth := cellVisualWidth * float64(baseCharWidth)
					textScaleX := horizScale * 2.0
					xOffset := 0.0
					if float64(actualWidth) > targetCellWidth {
						// Wide char: squeeze to fit cell
						textScaleX *= targetCellWidth / float64(actualWidth)
					} else if float64(actualWidth) < targetCellWidth {
						// Center narrow char (offset in final scaled coordinates)
						xOffset = (targetCellWidth - float64(actualWidth)) * horizScale
					}
					painter.Translate2(float64(cellX)+xOffset, float64(cellY)+float64(baseCharAscent)*vertScale+yOffset)
					painter.Scale(textScaleX, vertScale)
					painter.DrawText3(0, 0, charStr)
					painter.Restore()
				case purfecterm.LineAttrDoubleTop:
					// Double-height top half: 2x both directions, show top half only
					painter.Save()
					painter.SetClipRect2(cellX, cellY, cellW, cellH)
					// Calculate target cell width for flex width cells
					targetCellWidth := cellVisualWidth * float64(baseCharWidth)
					textScaleX := horizScale * 2.0
					textScaleY := vertScale * 2.0
					xOffset := 0.0
					if float64(actualWidth) > targetCellWidth {
						textScaleX *= targetCellWidth / float64(actualWidth)
					} else if float64(actualWidth) < targetCellWidth {
						xOffset = (targetCellWidth - float64(actualWidth)) * horizScale
					}
					// Position baseline at 2x ascent (only top half visible due to clip)
					painter.Translate2(float64(cellX)+xOffset, float64(cellY)+float64(baseCharAscent)*vertScale*2.0+yOffset*2)
					painter.Scale(textScaleX, textScaleY)
					painter.DrawText3(0, 0, charStr)
					painter.Restore()
				case purfecterm.LineAttrDoubleBottom:
					// Double-height bottom half: 2x both directions, show bottom half only
					painter.Save()
					painter.SetClipRect2(cellX, cellY, cellW, cellH)
					// Calculate target cell width for flex width cells
					targetCellWidth := cellVisualWidth * float64(baseCharWidth)
					textScaleX := horizScale * 2.0
					textScaleY := vertScale * 2.0
					xOffset := 0.0
					if float64(actualWidth) > targetCellWidth {
						textScaleX *= targetCellWidth / float64(actualWidth)
					} else if float64(actualWidth) < targetCellWidth {
						xOffset = (targetCellWidth - float64(actualWidth)) * horizScale
					}
					// Position so bottom half is visible (shift up by one cell height)
					painter.Translate2(float64(cellX)+xOffset, float64(cellY)+float64(baseCharAscent)*vertScale*2.0-float64(charHeight)+yOffset*2)
					painter.Scale(textScaleX, textScaleY)
					painter.DrawText3(0, 0, charStr)
					painter.Restore()
				}

				// Restore main font if we changed it (for bold or fallback)
				if charFontFamily != fontFamily || cell.Bold {
					painter.SetFont(font)
				}
			}
		afterCharRenderQt:

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
				cursorWasDrawn = true
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

	// Render front sprites (overlay on top of text)
	w.renderSprites(painter, frontSprites, charWidth, charHeight, scheme, scrollOffset, horizOffset)

	// Render screen splits if any are defined
	// Splits use logical scanline numbers relative to the scroll boundary
	splits := w.buffer.GetScreenSplitsSorted()
	if len(splits) > 0 {
		splitContentWidth := w.renderScreenSplits(painter, splits, cols, rows, charWidth, charHeight, unitX, unitY,
			fontFamily, fontSize, scheme, blinkPhase, cursorVisible, cursorVisibleX, cursorVisibleY,
			cursorShape, horizScale, vertScale, scrollOffset, horizOffset)
		// Store split content width for horizontal scrollbar calculation
		w.buffer.SetSplitContentWidth(splitContentWidth)
	} else {
		// No splits, clear split content width
		w.buffer.SetSplitContentWidth(0)
	}

	// Draw yellow dashed line between scrollback and logical screen
	boundaryRow := w.buffer.GetScrollbackBoundaryVisibleRow()
	if boundaryRow > 0 {
		lineY := boundaryRow * charHeight
		yellowColor := qt.NewQColor3(255, 200, 0)
		pen := qt.NewQPen3(yellowColor)
		pen.SetWidth(1)
		pen.SetStyle(qt.DashLine)
		painter.SetPenWithPen(pen)
		painter.DrawLine3(qt.NewQPoint2(0, lineY), qt.NewQPoint2(w.widget.Width(), lineY))
	}

	// Restore from crop clipping if it was applied
	if hasCrop {
		painter.Restore()
	}

	// Report whether cursor was drawn for auto-scroll logic
	w.buffer.SetCursorDrawn(cursorWasDrawn)

	// Check if we need to auto-scroll to bring cursor into view
	if w.buffer.CheckCursorAutoScroll() {
		// Scroll happened, redraw will be triggered by markDirty
	}

	// Update scrollbars after rendering (safe here since we're not holding buffer lock)
	w.updateScrollbar()

	w.buffer.ClearDirty()
}

func (w *Widget) screenToCell(screenX, screenY int) (cellX, cellY int) {
	w.mu.Lock()
	baseCharWidth := w.charWidth
	baseCharHeight := w.charHeight
	w.mu.Unlock()

	// Apply screen scaling
	horizScale := w.buffer.GetHorizontalScale()
	vertScale := w.buffer.GetVerticalScale()
	charWidth := int(float64(baseCharWidth) * horizScale)
	charHeight := int(float64(baseCharHeight) * vertScale)

	cellY = screenY / charHeight
	cols, rows := w.buffer.GetSize()
	if cellY < 0 {
		cellY = 0
	}
	if cellY >= rows {
		cellY = rows - 1
	}

	// Check if this line has doubled attributes (affects column calculation)
	lineAttr := w.buffer.GetVisibleLineAttribute(cellY)
	lineScale := 1.0
	if lineAttr != purfecterm.LineAttrNormal {
		// Doubled lines: each logical cell is 2x wide visually
		lineScale = 2.0
	}

	// Calculate which cell the mouse is in, accounting for flex width
	// First, get the x position relative to content area
	relativeX := float64(screenX - terminalLeftPadding)
	if relativeX < 0 {
		cellX = 0
		return
	}

	// Get horizontal scroll offset
	horizOffset := w.buffer.GetHorizOffset()

	// Iterate through cells to find which one contains this x position
	// accumulatedPixels tracks the right edge of each cell
	accumulatedPixels := 0.0
	for col := horizOffset; col < cols+horizOffset; col++ {
		cell := w.buffer.GetVisibleCell(col, cellY)

		// Calculate this cell's visual width
		cellVisualWidth := 1.0
		if cell.FlexWidth && cell.CellWidth > 0 {
			cellVisualWidth = cell.CellWidth
		}

		// Calculate pixel width of this cell
		cellPixelWidth := cellVisualWidth * float64(charWidth) * lineScale

		// Check if the click is within this cell
		if relativeX < accumulatedPixels+cellPixelWidth {
			cellX = col
			return
		}

		accumulatedPixels += cellPixelWidth
	}

	// If we've gone past all cells, return the last cell
	cellX = cols + horizOffset - 1
	if cellX < 0 {
		cellX = 0
	}
	return
}

func (w *Widget) keyPressEvent(super func(event *qt.QKeyEvent), event *qt.QKeyEvent) {
	key := event.Key()
	modifiers := event.Modifiers()

	hasShift := modifiers&qt.ShiftModifier != 0
	hasCtrl := modifiers&qt.ControlModifier != 0
	hasAlt := modifiers&qt.AltModifier != 0
	hasMeta := modifiers&qt.MetaModifier != 0

	// Special Tab handling for focus navigation:
	// - Ctrl+Tab (with or without Shift) → let Qt handle focus navigation
	// - Shift+Tab (without Ctrl) → let Qt handle focus navigation (previous widget)
	// - Plain Tab or Tab+Alt/Meta → send to terminal
	if qt.Key(key) == qt.Key_Tab || qt.Key(key) == qt.Key_Backtab {
		if hasCtrl {
			// Ctrl+Tab or Ctrl+Shift+Tab: let Qt handle focus navigation
			super(event)
			return
		}
		if hasShift && !hasAlt && !hasMeta {
			// Shift+Tab alone: let Qt handle focus navigation (previous widget)
			super(event)
			return
		}
		// Plain Tab or Tab with Alt/Meta: send to terminal (handled below)
	}

	w.mu.Lock()
	onInput := w.onInput
	w.mu.Unlock()

	if onInput == nil {
		return
	}

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
	case qt.Key_Tab, qt.Key_Backtab:
		// Tab with Alt/Meta sends modified Tab sequence to terminal
		if hasAlt || hasMeta {
			mod := w.calcMod(hasShift, hasCtrl, hasAlt, hasMeta)
			data = []byte{0x1b, '[', '9', ';', byte('0' + mod), 'u'} // CSI 9 ; mod u (kitty protocol)
		} else {
			// Plain Tab
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
		// Notify buffer of keyboard activity for auto-scroll-to-cursor
		w.buffer.NotifyKeyboardActivity()
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
		w.stopAutoScroll()
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

	// Track last mouse X for auto-scroll selection updates
	w.lastMouseX = cellX

	// Check for auto-scroll: mouse beyond top or bottom edge
	_, rows := w.buffer.GetSize()
	charHeight := w.charHeight
	mouseY := pos.Y()
	terminalHeight := rows * charHeight

	if mouseY < 0 {
		// Above top edge - scroll up
		rowsAbove := (-mouseY / charHeight) + 1
		if rowsAbove > 5 {
			rowsAbove = 5 // Cap speed
		}
		w.startAutoScroll(-rowsAbove)
	} else if mouseY >= terminalHeight {
		// Below bottom edge - scroll down
		rowsBelow := ((mouseY - terminalHeight) / charHeight) + 1
		if rowsBelow > 5 {
			rowsBelow = 5 // Cap speed
		}
		w.startAutoScroll(rowsBelow)
	} else {
		// Mouse is within terminal area - stop auto-scroll
		w.stopAutoScroll()
	}

	w.buffer.UpdateSelection(cellX, cellY)
}

// startAutoScroll begins auto-scrolling in the given direction
// delta: negative = scroll up (toward scrollback), positive = scroll down (toward current)
func (w *Widget) startAutoScroll(delta int) {
	if delta == 0 {
		w.stopAutoScroll()
		return
	}

	w.autoScrollDelta = delta

	// If timer already running, just update the delta
	if w.autoScrollTimer != nil {
		return
	}

	// Create and start auto-scroll timer (fires every 50ms for smooth scrolling)
	w.autoScrollTimer = qt.NewQTimer2(w.widget.QObject)
	w.autoScrollTimer.OnTimeout(func() {
		if !w.selecting || w.autoScrollDelta == 0 {
			w.stopAutoScroll()
			return
		}

		// Get current scroll offset
		offset := w.buffer.GetScrollOffset()
		maxOffset := w.buffer.GetMaxScrollOffset()

		// Calculate scroll amount based on delta magnitude
		scrollAmount := w.autoScrollDelta
		if scrollAmount < 0 {
			scrollAmount = -scrollAmount
		}

		// Apply scroll
		if w.autoScrollDelta < 0 {
			// Scroll up (toward scrollback)
			offset += scrollAmount
			if offset > maxOffset {
				offset = maxOffset
			}
		} else {
			// Scroll down (toward current)
			offset -= scrollAmount
			if offset < 0 {
				offset = 0
			}
		}
		w.buffer.SetScrollOffset(offset)

		// Update selection to edge
		_, rows := w.buffer.GetSize()
		if w.autoScrollDelta < 0 {
			// Scrolling up - selection extends to top row
			w.buffer.UpdateSelection(w.lastMouseX, 0)
		} else {
			// Scrolling down - selection extends to bottom row
			w.buffer.UpdateSelection(w.lastMouseX, rows-1)
		}

		w.updateScrollbar()
		w.widget.Update()
	})
	w.autoScrollTimer.Start(50)
}

// stopAutoScroll stops the auto-scroll timer
func (w *Widget) stopAutoScroll() {
	if w.autoScrollTimer != nil {
		w.autoScrollTimer.Stop()
		w.autoScrollTimer = nil
	}
	w.autoScrollDelta = 0
}

func (w *Widget) wheelEvent(event *qt.QWheelEvent) {
	modifiers := event.Modifiers()
	hasShift := modifiers&qt.ShiftModifier != 0

	deltaY := event.AngleDelta().Y()
	deltaX := event.AngleDelta().X()

	// Shift+scroll or horizontal scroll = horizontal scrolling
	if hasShift || (deltaX != 0 && deltaY == 0) {
		delta := deltaY
		if deltaX != 0 {
			delta = deltaX
		}

		offset := w.buffer.GetHorizOffset()
		maxOffset := w.buffer.GetMaxHorizOffset()

		if delta > 0 {
			offset -= 3
			if offset < 0 {
				offset = 0
			}
		} else if delta < 0 {
			offset += 3
			if offset > maxOffset {
				offset = maxOffset
			}
		}

		w.buffer.SetHorizOffset(offset)
		w.updateHorizScrollbar()
		w.widget.Update()
		return
	}

	// Vertical scrolling
	offset := w.buffer.GetScrollOffset()
	scrollbackSize := w.buffer.GetScrollbackSize()

	if deltaY > 0 {
		// Scrolling UP into scrollback - don't normalize, let them push through
		offset += 3
		if offset > scrollbackSize {
			offset = scrollbackSize
		}
		w.buffer.SetScrollOffset(offset)
	} else if deltaY < 0 {
		// Scrolling DOWN toward logical screen
		offset -= 3
		if offset < 0 {
			offset = 0
		}
		w.buffer.SetScrollOffset(offset)
		// Only snap to 0 when scrolling DOWN into the magnetic zone
		w.buffer.NormalizeScrollOffset()
	}

	w.updateScrollbar()
	w.updateHorizScrollbar() // Visibility may change based on scroll position
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

	// Create scrollbars lazily on first resize (Qt is fully initialized by now)
	w.initScrollbar()

	scrollbarWidth := 12  // Thin macOS-style scrollbar
	scrollbarHeight := 12 // Thin macOS-style scrollbar
	widgetWidth := w.widget.Width()
	widgetHeight := w.widget.Height()

	// Check if horizontal scrollbar needs to be shown
	needsHorizScrollbar := w.buffer.NeedsHorizScrollbar()
	effectiveHeight := widgetHeight
	if needsHorizScrollbar {
		effectiveHeight = widgetHeight - scrollbarHeight
	}

	// Position vertical scrollbar on the right edge
	if w.scrollbar != nil {
		w.scrollbar.SetGeometry(widgetWidth-scrollbarWidth, 0, scrollbarWidth, effectiveHeight)
		w.scrollbar.Show()
	}

	// Position horizontal scrollbar at the bottom
	if w.horizScrollbar != nil {
		if needsHorizScrollbar {
			// Leave corner space for vertical scrollbar
			horizWidth := widgetWidth - scrollbarWidth
			w.horizScrollbar.SetGeometry(0, widgetHeight-scrollbarHeight, horizWidth, scrollbarHeight)
			w.horizScrollbar.Show()
		} else {
			w.horizScrollbar.Hide()
		}
	}

	// Apply screen scaling to character dimensions
	horizScale := w.buffer.GetHorizontalScale()
	vertScale := w.buffer.GetVerticalScale()
	scaledCharWidth := int(float64(w.charWidth) * horizScale)
	scaledCharHeight := int(float64(w.charHeight) * vertScale)
	if scaledCharWidth < 1 {
		scaledCharWidth = 1
	}
	if scaledCharHeight < 1 {
		scaledCharHeight = 1
	}

	// Account for scrollbars when calculating columns
	newCols := (widgetWidth - terminalLeftPadding - scrollbarWidth) / scaledCharWidth
	newRows := effectiveHeight / scaledCharHeight

	if newCols < 1 {
		newCols = 1
	}
	if newRows < 1 {
		newRows = 1
	}

	w.buffer.Resize(newCols, newRows)
	w.updateScrollbar()
	w.updateHorizScrollbar()
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
