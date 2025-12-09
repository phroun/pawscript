package purfecterm

// Cell represents a single character cell in the terminal
type Cell struct {
	Char       rune    // Base character
	Combining  string  // Combining marks (vowel points, diacritics, etc.)
	Foreground Color
	Background Color
	Bold       bool
	Italic     bool
	Underline  bool
	Reverse    bool
	Blink      bool    // When true, character animates (bobbing wave instead of traditional blink)
	FlexWidth  bool    // When true, cell uses East Asian Width for variable width rendering
	CellWidth  float64 // Visual width in cell units (0.5, 1.0, 1.5, 2.0) - only used when FlexWidth is true
	BGP        int     // Base Glyph Palette index (-1 = use foreground color code as palette)
	XFlip      bool    // Horizontal flip for custom glyphs
	YFlip      bool    // Vertical flip for custom glyphs
}

// String returns the full character including any combining marks
func (c *Cell) String() string {
	if c.Combining == "" {
		return string(c.Char)
	}
	return string(c.Char) + c.Combining
}

// IsCombiningMark returns true if the rune is a Unicode combining character.
// This includes:
// - Combining Diacritical Marks (0x0300-0x036F)
// - Hebrew vowel points and marks (0x0591-0x05C7)
// - Arabic marks (0x0610-0x065F, 0x0670, 0x06D6-0x06ED)
// - Other combining marks (Mn, Mc, Me categories)
func IsCombiningMark(r rune) bool {
	// Common combining diacritical marks
	if r >= 0x0300 && r <= 0x036F {
		return true
	}
	// Combining Diacritical Marks Extended
	if r >= 0x1AB0 && r <= 0x1AFF {
		return true
	}
	// Combining Diacritical Marks Supplement
	if r >= 0x1DC0 && r <= 0x1DFF {
		return true
	}
	// Combining Diacritical Marks for Symbols
	if r >= 0x20D0 && r <= 0x20FF {
		return true
	}
	// Combining Half Marks
	if r >= 0xFE20 && r <= 0xFE2F {
		return true
	}
	// Hebrew points and marks (cantillation, vowels, etc.)
	if r >= 0x0591 && r <= 0x05BD {
		return true
	}
	if r == 0x05BF || r == 0x05C1 || r == 0x05C2 || r == 0x05C4 || r == 0x05C5 || r == 0x05C7 {
		return true
	}
	// Arabic marks
	if r >= 0x0610 && r <= 0x061A {
		return true
	}
	if r >= 0x064B && r <= 0x065F {
		return true
	}
	if r == 0x0670 {
		return true
	}
	if r >= 0x06D6 && r <= 0x06DC {
		return true
	}
	if r >= 0x06DF && r <= 0x06E4 {
		return true
	}
	if r >= 0x06E7 && r <= 0x06E8 {
		return true
	}
	if r >= 0x06EA && r <= 0x06ED {
		return true
	}
	// Thai marks
	if r >= 0x0E31 && r <= 0x0E3A {
		return true
	}
	if r >= 0x0E47 && r <= 0x0E4E {
		return true
	}
	// Devanagari, Bengali, and other Indic vowel signs (combining)
	if r >= 0x0901 && r <= 0x0903 { // Devanagari
		return true
	}
	if r >= 0x093A && r <= 0x094F {
		return true
	}
	if r >= 0x0951 && r <= 0x0957 {
		return true
	}
	if r >= 0x0962 && r <= 0x0963 {
		return true
	}
	// Korean Hangul Jungseong and Jongseong (combining vowels/finals for Jamo)
	if r >= 0x1160 && r <= 0x11FF {
		return true
	}
	// Variation selectors
	if r >= 0xFE00 && r <= 0xFE0F {
		return true
	}
	// Zero-width joiner and non-joiner (used in complex scripts)
	if r == 0x200C || r == 0x200D {
		return true
	}
	return false
}

// EastAsianWidth represents the Unicode East Asian Width property
type EastAsianWidth int

const (
	EAWidthNeutral   EastAsianWidth = iota // N - Neutral (most Western characters)
	EAWidthAmbiguous                       // A - Ambiguous (characters that can be narrow or wide)
	EAWidthHalfwidth                       // H - Halfwidth (halfwidth CJK punctuation, Katakana)
	EAWidthFullwidth                       // F - Fullwidth (fullwidth ASCII, punctuation)
	EAWidthNarrow                          // Na - Narrow (narrow but not neutral)
	EAWidthWide                            // W - Wide (CJK ideographs, etc.)
)

// AmbiguousWidthMode controls how ambiguous East Asian Width characters are rendered
type AmbiguousWidthMode int

const (
	AmbiguousWidthAuto   AmbiguousWidthMode = iota // Match width of previous character
	AmbiguousWidthNarrow                           // Force 1.0 width
	AmbiguousWidthWide                             // Force 2.0 width
)

// GetEastAsianWidth returns the East Asian Width property for a rune.
// Returns the width in cell units:
// - Halfwidth (H): 1.0 (half compared to normal CJK = same as Latin)
// - Narrow (Na) / Neutral (N): 1.0
// - Ambiguous (A): returns -1.0 to indicate "use context" (caller handles)
// - Fullwidth (F) / Wide (W): 2.0
func GetEastAsianWidth(r rune) float64 {
	cat := getEastAsianWidthCategory(r)
	switch cat {
	case EAWidthAmbiguous:
		return -1.0 // Special value: ambiguous, needs context
	case EAWidthFullwidth, EAWidthWide:
		return 2.0
	default: // Neutral, Narrow, Halfwidth - all normal width
		return 1.0
	}
}

// GetEastAsianWidthCategory returns the category for a rune (exported for debugging)
func GetEastAsianWidthCategory(r rune) EastAsianWidth {
	return getEastAsianWidthCategory(r)
}

// IsAmbiguousWidth returns true if the character has ambiguous East Asian Width
func IsAmbiguousWidth(r rune) bool {
	return getEastAsianWidthCategory(r) == EAWidthAmbiguous
}

// getEastAsianWidthCategory returns the East Asian Width category for a rune.
// Based on Unicode 15.0 East_Asian_Width property.
func getEastAsianWidthCategory(r rune) EastAsianWidth {
	// Halfwidth forms (H)
	// Halfwidth CJK punctuation
	if r >= 0xFF61 && r <= 0xFF64 {
		return EAWidthHalfwidth
	}
	// Halfwidth Katakana
	if r >= 0xFF65 && r <= 0xFF9F {
		return EAWidthHalfwidth
	}
	// Halfwidth Hangul
	if r >= 0xFFA0 && r <= 0xFFDC {
		return EAWidthHalfwidth
	}
	// Halfwidth symbols
	if r >= 0xFFE8 && r <= 0xFFEE {
		return EAWidthHalfwidth
	}

	// Fullwidth forms (F)
	// Fullwidth ASCII variants
	if r >= 0xFF01 && r <= 0xFF60 {
		return EAWidthFullwidth
	}
	// Fullwidth currency symbols
	if r >= 0xFFE0 && r <= 0xFFE6 {
		return EAWidthFullwidth
	}

	// Wide characters (W)
	// CJK Radicals Supplement
	if r >= 0x2E80 && r <= 0x2EFF {
		return EAWidthWide
	}
	// Kangxi Radicals
	if r >= 0x2F00 && r <= 0x2FDF {
		return EAWidthWide
	}
	// CJK Symbols and Punctuation
	if r >= 0x3000 && r <= 0x303F {
		return EAWidthWide
	}
	// Hiragana
	if r >= 0x3040 && r <= 0x309F {
		return EAWidthWide
	}
	// Katakana
	if r >= 0x30A0 && r <= 0x30FF {
		return EAWidthWide
	}
	// Bopomofo
	if r >= 0x3100 && r <= 0x312F {
		return EAWidthWide
	}
	// Hangul Compatibility Jamo
	if r >= 0x3130 && r <= 0x318F {
		return EAWidthWide
	}
	// Kanbun
	if r >= 0x3190 && r <= 0x319F {
		return EAWidthWide
	}
	// Bopomofo Extended
	if r >= 0x31A0 && r <= 0x31BF {
		return EAWidthWide
	}
	// CJK Strokes
	if r >= 0x31C0 && r <= 0x31EF {
		return EAWidthWide
	}
	// Katakana Phonetic Extensions
	if r >= 0x31F0 && r <= 0x31FF {
		return EAWidthWide
	}
	// Enclosed CJK Letters and Months
	if r >= 0x3200 && r <= 0x32FF {
		return EAWidthWide
	}
	// CJK Compatibility
	if r >= 0x3300 && r <= 0x33FF {
		return EAWidthWide
	}
	// CJK Unified Ideographs Extension A
	if r >= 0x3400 && r <= 0x4DBF {
		return EAWidthWide
	}
	// CJK Unified Ideographs
	if r >= 0x4E00 && r <= 0x9FFF {
		return EAWidthWide
	}
	// Yi Syllables
	if r >= 0xA000 && r <= 0xA48F {
		return EAWidthWide
	}
	// Yi Radicals
	if r >= 0xA490 && r <= 0xA4CF {
		return EAWidthWide
	}
	// Hangul Syllables
	if r >= 0xAC00 && r <= 0xD7AF {
		return EAWidthWide
	}
	// CJK Compatibility Ideographs
	if r >= 0xF900 && r <= 0xFAFF {
		return EAWidthWide
	}
	// Vertical Forms
	if r >= 0xFE10 && r <= 0xFE1F {
		return EAWidthWide
	}
	// CJK Compatibility Forms
	if r >= 0xFE30 && r <= 0xFE4F {
		return EAWidthWide
	}
	// Small Form Variants
	if r >= 0xFE50 && r <= 0xFE6F {
		return EAWidthWide
	}
	// CJK Unified Ideographs Extension B
	if r >= 0x20000 && r <= 0x2A6DF {
		return EAWidthWide
	}
	// CJK Unified Ideographs Extension C
	if r >= 0x2A700 && r <= 0x2B73F {
		return EAWidthWide
	}
	// CJK Unified Ideographs Extension D
	if r >= 0x2B740 && r <= 0x2B81F {
		return EAWidthWide
	}
	// CJK Unified Ideographs Extension E
	if r >= 0x2B820 && r <= 0x2CEAF {
		return EAWidthWide
	}
	// CJK Unified Ideographs Extension F
	if r >= 0x2CEB0 && r <= 0x2EBEF {
		return EAWidthWide
	}
	// CJK Compatibility Ideographs Supplement
	if r >= 0x2F800 && r <= 0x2FA1F {
		return EAWidthWide
	}
	// CJK Unified Ideographs Extension G
	if r >= 0x30000 && r <= 0x3134F {
		return EAWidthWide
	}
	// Emoji (many are wide)
	if r >= 0x1F300 && r <= 0x1F9FF {
		return EAWidthWide
	}
	// More emoji
	if r >= 0x1FA00 && r <= 0x1FAFF {
		return EAWidthWide
	}

	// Ambiguous characters (A)
	// These are characters that could be narrow or wide depending on context
	// Greek
	if r >= 0x0370 && r <= 0x03FF {
		return EAWidthAmbiguous
	}
	// Cyrillic
	if r >= 0x0400 && r <= 0x04FF {
		return EAWidthAmbiguous
	}
	// Latin Extended Additional
	if r >= 0x1E00 && r <= 0x1EFF {
		return EAWidthAmbiguous
	}
	// General Punctuation (some)
	if r >= 0x2010 && r <= 0x2027 {
		return EAWidthAmbiguous
	}
	// Currency Symbols
	if r >= 0x20A0 && r <= 0x20CF {
		return EAWidthAmbiguous
	}
	// Letterlike Symbols
	if r >= 0x2100 && r <= 0x214F {
		return EAWidthAmbiguous
	}
	// Number Forms
	if r >= 0x2150 && r <= 0x218F {
		return EAWidthAmbiguous
	}
	// Arrows
	if r >= 0x2190 && r <= 0x21FF {
		return EAWidthAmbiguous
	}
	// Mathematical Operators
	if r >= 0x2200 && r <= 0x22FF {
		return EAWidthAmbiguous
	}
	// Miscellaneous Technical
	if r >= 0x2300 && r <= 0x23FF {
		return EAWidthAmbiguous
	}
	// Box Drawing
	if r >= 0x2500 && r <= 0x257F {
		return EAWidthAmbiguous
	}
	// Block Elements
	if r >= 0x2580 && r <= 0x259F {
		return EAWidthAmbiguous
	}
	// Geometric Shapes
	if r >= 0x25A0 && r <= 0x25FF {
		return EAWidthAmbiguous
	}
	// Miscellaneous Symbols
	if r >= 0x2600 && r <= 0x26FF {
		return EAWidthAmbiguous
	}
	// Dingbats
	if r >= 0x2700 && r <= 0x27BF {
		return EAWidthAmbiguous
	}

	// Narrow (Na)
	// Basic Latin (ASCII)
	if r >= 0x0020 && r <= 0x007E {
		return EAWidthNarrow
	}
	// Latin-1 Supplement
	if r >= 0x00A0 && r <= 0x00FF {
		return EAWidthNarrow
	}

	// Default to Neutral
	return EAWidthNeutral
}

// EmptyCell returns an empty cell with default attributes
func EmptyCell() Cell {
	return Cell{
		Char:       ' ',
		Foreground: DefaultForeground,
		Background: DefaultBackground,
	}
}

// EmptyCellWithColors returns an empty cell with specified colors
func EmptyCellWithColors(fg, bg Color) Cell {
	return Cell{
		Char:       ' ',
		Foreground: fg,
		Background: bg,
	}
}

// EmptyCellWithAttrs returns an empty cell with full attribute specification
func EmptyCellWithAttrs(fg, bg Color, bold, italic, underline, reverse, blink bool) Cell {
	return Cell{
		Char:       ' ',
		Foreground: fg,
		Background: bg,
		Bold:       bold,
		Italic:     italic,
		Underline:  underline,
		Reverse:    reverse,
		Blink:      blink,
	}
}

// PaletteEntryType defines the type of a palette entry
type PaletteEntryType int

const (
	PaletteEntryColor      PaletteEntryType = iota // Normal color entry
	PaletteEntryTransparent                        // Use cell's background color (SGR code 8)
	PaletteEntryDefaultFG                          // Use cell's foreground color (SGR code 9)
)

// PaletteEntry represents a single entry in a custom palette
type PaletteEntry struct {
	Type  PaletteEntryType // Type of entry
	Color Color            // Color value (only used when Type == PaletteEntryColor)
	Dim   bool             // Whether this is a dim variant
}

// Palette represents a custom color palette for glyph rendering
type Palette struct {
	Entries []PaletteEntry
}

// CustomGlyph represents a custom pixel-art glyph that replaces a Unicode character
type CustomGlyph struct {
	Width  int   // Width in pixels
	Height int   // Height in pixels (derived from len(Pixels)/Width)
	Pixels []int // Palette indices, row by row, left to right, top to bottom
}

// NewPalette creates a new palette with the specified number of entries
func NewPalette(size int) *Palette {
	return &Palette{
		Entries: make([]PaletteEntry, size),
	}
}

// NewCustomGlyph creates a new custom glyph from pixel data
// Width is the pixel width, pixels are palette indices
// Height is automatically calculated from len(pixels)/width
func NewCustomGlyph(width int, pixels []int) *CustomGlyph {
	height := 0
	if width > 0 && len(pixels) > 0 {
		height = len(pixels) / width
		// Handle any remainder
		if len(pixels)%width != 0 {
			height++
		}
	}
	return &CustomGlyph{
		Width:  width,
		Height: height,
		Pixels: pixels,
	}
}

// GetPixel returns the palette index at the given x,y position
// Returns 0 if out of bounds
func (g *CustomGlyph) GetPixel(x, y int) int {
	if x < 0 || x >= g.Width || y < 0 || y >= g.Height {
		return 0
	}
	idx := y*g.Width + x
	if idx >= len(g.Pixels) {
		return 0
	}
	return g.Pixels[idx]
}

// Sprite represents an overlay sprite that can be positioned anywhere on screen
type Sprite struct {
	ID       int       // Unique identifier
	X, Y     float64   // Position in coordinate units
	ZIndex   int       // Z-order (negative = behind text layer)
	FGP      int       // Foreground Glyph Palette (-1 = use default based on rune)
	FlipCode int       // 0=none, 1=XFlip, 2=YFlip, 3=both
	XScale   float64   // Horizontal scale multiplier
	YScale   float64   // Vertical scale multiplier
	CropRect int       // Crop rectangle ID (-1 = no cropping)
	Runes    [][]rune  // 2D array of characters (rows of runes, for multi-tile sprites)
}

// NewSprite creates a new sprite with default values
func NewSprite(id int) *Sprite {
	return &Sprite{
		ID:       id,
		X:        0,
		Y:        0,
		ZIndex:   0,
		FGP:      -1,
		FlipCode: 0,
		XScale:   1.0,
		YScale:   1.0,
		CropRect: -1,
		Runes:    nil,
	}
}

// SetRunes parses rune data, splitting on newline (rune 10) for multi-row sprites
func (s *Sprite) SetRunes(runes []rune) {
	s.Runes = make([][]rune, 0)
	currentRow := make([]rune, 0)
	for _, r := range runes {
		if r == '\n' || r == 10 {
			if len(currentRow) > 0 {
				s.Runes = append(s.Runes, currentRow)
				currentRow = make([]rune, 0)
			}
		} else {
			currentRow = append(currentRow, r)
		}
	}
	if len(currentRow) > 0 {
		s.Runes = append(s.Runes, currentRow)
	}
}

// GetXFlip returns true if sprite should be horizontally flipped
func (s *Sprite) GetXFlip() bool {
	return s.FlipCode == 1 || s.FlipCode == 3
}

// GetYFlip returns true if sprite should be vertically flipped
func (s *Sprite) GetYFlip() bool {
	return s.FlipCode == 2 || s.FlipCode == 3
}

// CropRectangle defines a rectangular clipping area for sprites
type CropRectangle struct {
	ID               int
	MinX, MinY       float64
	MaxX, MaxY       float64
}

// NewCropRectangle creates a new crop rectangle
func NewCropRectangle(id int, minX, minY, maxX, maxY float64) *CropRectangle {
	return &CropRectangle{
		ID:   id,
		MinX: minX,
		MinY: minY,
		MaxX: maxX,
		MaxY: maxY,
	}
}

// Contains returns true if the point is within the crop rectangle
func (cr *CropRectangle) Contains(x, y float64) bool {
	return x >= cr.MinX && x <= cr.MaxX && y >= cr.MinY && y <= cr.MaxY
}

// LineAttribute defines the display mode for a line (VT100 DECDHL/DECDWL)
type LineAttribute int

const (
	LineAttrNormal       LineAttribute = iota // Normal single-width, single-height
	LineAttrDoubleWidth                       // DECDWL: Double-width line (ESC#6)
	LineAttrDoubleTop                         // DECDHL: Double-height top half (ESC#3)
	LineAttrDoubleBottom                      // DECDHL: Double-height bottom half (ESC#4)
)

// LineInfo contains per-line metadata including display attributes and default cell
// for rendering characters beyond the stored line length
type LineInfo struct {
	Attribute   LineAttribute // DECDWL/DECDHL display mode
	DefaultCell Cell          // Used for rendering beyond stored line length
}

// DefaultLineInfo returns a LineInfo with normal attributes and default colors
func DefaultLineInfo() LineInfo {
	return LineInfo{
		Attribute:   LineAttrNormal,
		DefaultCell: EmptyCell(),
	}
}

// LineInfoWithCell returns a LineInfo with normal attributes and the given default cell
func LineInfoWithCell(cell Cell) LineInfo {
	return LineInfo{
		Attribute:   LineAttrNormal,
		DefaultCell: cell,
	}
}

// ScreenInfo contains buffer-wide metadata for rendering logical lines
// that have no stored data yet
type ScreenInfo struct {
	DefaultCell Cell // Used for rendering logical lines beyond stored lines
}

// DefaultScreenInfo returns a ScreenInfo with default colors
func DefaultScreenInfo() ScreenInfo {
	return ScreenInfo{
		DefaultCell: EmptyCell(),
	}
}

// ScreenInfoWithCell returns a ScreenInfo with the given default cell
func ScreenInfoWithCell(cell Cell) ScreenInfo {
	return ScreenInfo{
		DefaultCell: cell,
	}
}
