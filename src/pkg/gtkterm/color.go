// Package gtkterm provides a GTK3-based terminal emulator widget for Go.
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
package gtkterm

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

// ColorScheme defines the colors used by the terminal
type ColorScheme struct {
	Foreground Color
	Background Color
	Cursor     Color
	Selection  Color
	Palette    []Color // 16 ANSI colors
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
