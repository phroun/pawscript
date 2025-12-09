package purfecterm

// Cell represents a single character cell in the terminal
type Cell struct {
	Char       rune   // Base character
	Combining  string // Combining marks (vowel points, diacritics, etc.)
	Foreground Color
	Background Color
	Bold       bool
	Italic     bool
	Underline  bool
	Reverse    bool
	Blink      bool // When true, character animates (bobbing wave instead of traditional blink)
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
