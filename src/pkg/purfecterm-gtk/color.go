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

// BlinkMode determines how the blink attribute is rendered
type BlinkMode int

const (
	BlinkModeBounce BlinkMode = iota // Bobbing wave animation (default)
	BlinkModeBlink                   // Traditional on/off blinking
	BlinkModeBright                  // Interpret as bright background (VGA style)
)

// Standard ANSI 16-color palette (in ANSI order for escape code compatibility)
// ANSI order: black, red, green, yellow, blue, magenta, cyan, white
// VGA order:  black, blue, green, cyan, red, magenta, brown, silver
var ANSIColors = []Color{
	{R: 0, G: 0, B: 0},       // ANSI 0: Black
	{R: 170, G: 0, B: 0},     // ANSI 1: Red
	{R: 0, G: 170, B: 0},     // ANSI 2: Green
	{R: 170, G: 85, B: 0},    // ANSI 3: Yellow/Brown
	{R: 0, G: 0, B: 170},     // ANSI 4: Blue
	{R: 170, G: 0, B: 170},   // ANSI 5: Magenta/Purple
	{R: 0, G: 170, B: 170},   // ANSI 6: Cyan
	{R: 170, G: 170, B: 170}, // ANSI 7: White/Silver
	// Bright variants (8-15)
	{R: 85, G: 85, B: 85},    // ANSI 8: Bright Black (Dark Gray)
	{R: 255, G: 85, B: 85},   // ANSI 9: Bright Red
	{R: 85, G: 255, B: 85},   // ANSI 10: Bright Green
	{R: 255, G: 255, B: 85},  // ANSI 11: Bright Yellow
	{R: 85, G: 85, B: 255},   // ANSI 12: Bright Blue
	{R: 255, G: 85, B: 255},  // ANSI 13: Bright Magenta/Pink
	{R: 85, G: 255, B: 255},  // ANSI 14: Bright Cyan
	{R: 255, G: 255, B: 255}, // ANSI 15: White
}

// VGAToANSI maps VGA/CGA color index to ANSI color index
// VGA: 0=black,1=blue,2=green,3=cyan,4=red,5=magenta,6=brown,7=silver,...
// ANSI: 0=black,1=red,2=green,3=yellow,4=blue,5=magenta,6=cyan,7=white,...
var VGAToANSI = []int{0, 4, 2, 6, 1, 5, 3, 7, 8, 12, 10, 14, 9, 13, 11, 15}

// ANSIToVGA maps ANSI color index to VGA/CGA color index
var ANSIToVGA = []int{0, 4, 2, 6, 1, 5, 3, 7, 8, 12, 10, 14, 9, 13, 11, 15}

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
	"00_black":        0,
	"01_dark_blue":    1,
	"02_dark_green":   2,
	"03_dark_cyan":    3,
	"04_dark_red":     4,
	"05_purple":       5,
	"06_brown":        6,
	"07_silver":       7,
	"08_dark_gray":    8,
	"09_bright_blue":  9,
	"10_bright_green": 10,
	"11_bright_cyan":  11,
	"12_bright_red":   12,
	"13_pink":         13,
	"14_yellow":       14,
	"15_white":        15,
}

// ColorScheme defines the colors used by the terminal
type ColorScheme struct {
	Foreground Color
	Background Color
	Cursor     Color
	Selection  Color
	Palette    []Color   // 16 ANSI colors
	BlinkMode  BlinkMode // How to render blink attribute
}

// ParseBlinkMode parses a blink mode string
func ParseBlinkMode(s string) BlinkMode {
	switch s {
	case "blink":
		return BlinkModeBlink
	case "bright":
		return BlinkModeBright
	default:
		return BlinkModeBounce
	}
}

// DefaultPaletteHex returns the default 16-color palette as hex strings in VGA order
// (for config file compatibility with VGA-style color names)
func DefaultPaletteHex() []string {
	result := make([]string, 16)
	for vgaIdx := 0; vgaIdx < 16; vgaIdx++ {
		ansiIdx := VGAToANSI[vgaIdx]
		result[vgaIdx] = ANSIColors[ansiIdx].ToHex()
	}
	return result
}

// PaletteColorNames returns the names for the 16 palette colors in order
func PaletteColorNames() []string {
	return []string{
		"00_black", "01_dark_blue", "02_dark_green", "03_dark_cyan",
		"04_dark_red", "05_purple", "06_brown", "07_silver",
		"08_dark_gray", "09_bright_blue", "10_bright_green", "11_bright_cyan",
		"12_bright_red", "13_pink", "14_yellow", "15_white",
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
