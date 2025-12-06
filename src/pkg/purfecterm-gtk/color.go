// Package purfectermgtk provides a GTK3-based terminal emulator widget for Go.
//
// It features:
//   - Full ANSI escape sequence support (colors, cursor movement, clearing)
//   - 256-color and true color (24-bit) support
//   - Scrollback buffer with configurable size
//   - Text selection with clipboard support
//   - Cross-platform: uses native PTY on Unix, ConPTY on Windows
//
// Basic usage:
//
//	term, err := gtkterm.New(gtkterm.Options{
//	    Cols:           80,
//	    Rows:           24,
//	    ScrollbackSize: 10000,
//	    FontDesc:       "Monospace 12",
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	container.Add(term.Widget())
//	term.RunShell() // or term.RunCommand("program", "arg1", "arg2")
package purfectermgtk

// Color represents an RGB color
type Color struct {
	R, G, B uint8
	Default bool // Use default fg/bg color instead of RGB values
}

// Predefined colors
var (
	DefaultForeground = Color{R: 212, G: 212, B: 212, Default: true}
	DefaultBackground = Color{R: 30, G: 30, B: 30, Default: true}
)

// Standard ANSI 16-color palette
var ANSIColors = []Color{
	{R: 0, G: 0, B: 0},       // 0: Black
	{R: 205, G: 49, B: 49},   // 1: Red
	{R: 13, G: 188, B: 121},  // 2: Green
	{R: 229, G: 229, B: 16},  // 3: Yellow
	{R: 36, G: 114, B: 200},  // 4: Blue
	{R: 188, G: 63, B: 188},  // 5: Magenta
	{R: 17, G: 168, B: 205},  // 6: Cyan
	{R: 229, G: 229, B: 229}, // 7: White
	// Bright variants (8-15)
	{R: 102, G: 102, B: 102}, // 8: Bright Black (Gray)
	{R: 241, G: 76, B: 76},   // 9: Bright Red
	{R: 35, G: 209, B: 139},  // 10: Bright Green
	{R: 245, G: 245, B: 67},  // 11: Bright Yellow
	{R: 59, G: 142, B: 234},  // 12: Bright Blue
	{R: 214, G: 112, B: 214}, // 13: Bright Magenta
	{R: 41, G: 184, B: 219},  // 14: Bright Cyan
	{R: 255, G: 255, B: 255}, // 15: Bright White
}

// Get256Color returns the color for a 256-color mode index
func Get256Color(idx int) Color {
	if idx < 16 {
		// Standard ANSI colors
		return ANSIColors[idx]
	} else if idx < 232 {
		// 6x6x6 color cube (16-231)
		idx -= 16
		b := idx % 6
		g := (idx / 6) % 6
		r := idx / 36
		return Color{
			R: uint8(r * 51),
			G: uint8(g * 51),
			B: uint8(b * 51),
		}
	} else {
		// Grayscale (232-255)
		gray := uint8((idx-232)*10 + 8)
		return Color{R: gray, G: gray, B: gray}
	}
}

// ToHex returns the color as a hex string like "#RRGGBB"
func (c Color) ToHex() string {
	return "#" + hexByte(c.R) + hexByte(c.G) + hexByte(c.B)
}

func hexByte(b uint8) string {
	const hex = "0123456789ABCDEF"
	return string([]byte{hex[b>>4], hex[b&0x0F]})
}

// ParseHexColor parses a hex color string in "#RRGGBB" or "#RGB" format
// Returns the color and true on success, or a zero color and false on failure
func ParseHexColor(s string) (Color, bool) {
	if len(s) == 0 || s[0] != '#' {
		return Color{}, false
	}
	s = s[1:] // Remove the '#' prefix

	var r, g, b uint8
	switch len(s) {
	case 3: // #RGB format
		r = parseHexNibble(s[0]) * 17 // 0xF -> 0xFF, 0x8 -> 0x88, etc.
		g = parseHexNibble(s[1]) * 17
		b = parseHexNibble(s[2]) * 17
	case 6: // #RRGGBB format
		r = parseHexNibble(s[0])<<4 | parseHexNibble(s[1])
		g = parseHexNibble(s[2])<<4 | parseHexNibble(s[3])
		b = parseHexNibble(s[4])<<4 | parseHexNibble(s[5])
	default:
		return Color{}, false
	}
	return Color{R: r, G: g, B: b}, true
}

func parseHexNibble(c byte) uint8 {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	default:
		return 0
	}
}

// ColorNames maps ANSI color index names to their indices (0-15)
var ColorNames = map[string]int{
	"black":         0,
	"red":           1,
	"green":         2,
	"yellow":        3,
	"blue":          4,
	"magenta":       5,
	"cyan":          6,
	"white":         7,
	"bright_black":  8,
	"bright_red":    9,
	"bright_green":  10,
	"bright_yellow": 11,
	"bright_blue":   12,
	"bright_magenta": 13,
	"bright_cyan":   14,
	"bright_white":  15,
}

// ColorScheme defines the colors used by the terminal
type ColorScheme struct {
	Foreground Color
	Background Color
	Cursor     Color
	Selection  Color
	Palette    []Color // 16 ANSI colors
}

// DefaultPaletteHex returns the default 16-color ANSI palette as hex strings
func DefaultPaletteHex() []string {
	result := make([]string, 16)
	for i, c := range ANSIColors {
		result[i] = c.ToHex()
	}
	return result
}

// PaletteColorNames returns the names for the 16 palette colors in order
func PaletteColorNames() []string {
	return []string{
		"black", "red", "green", "yellow", "blue", "magenta", "cyan", "white",
		"bright_black", "bright_red", "bright_green", "bright_yellow",
		"bright_blue", "bright_magenta", "bright_cyan", "bright_white",
	}
}

// DefaultColorScheme returns a dark color scheme similar to VS Code
func DefaultColorScheme() ColorScheme {
	return ColorScheme{
		Foreground: Color{R: 212, G: 212, B: 212},
		Background: Color{R: 30, G: 30, B: 30},
		Cursor:     Color{R: 255, G: 255, B: 255},
		Selection:  Color{R: 68, G: 68, B: 68},
		Palette:    ANSIColors,
	}
}
