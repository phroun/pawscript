package purfecterm

// Cell represents a single character cell in the terminal
type Cell struct {
	Char       rune
	Foreground Color
	Background Color
	Bold       bool
	Italic     bool
	Underline  bool
	Reverse    bool
	Blink      bool // When true, character animates (bobbing wave instead of traditional blink)
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
