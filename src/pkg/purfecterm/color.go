// Package purfecterm provides the core terminal emulation logic shared between
// GUI toolkit implementations (GTK, Qt, etc.).
//
// This package contains:
//   - Color types and palettes
//   - Cell representation
//   - Terminal buffer with scrollback
//   - ANSI escape sequence parser
//   - PTY interfaces
//
// GUI-specific packages (purfecterm-gtk, purfecterm-qt) provide the widget
// implementations that use this core package.
package purfecterm

// ColorType indicates how a color was specified
type ColorType uint8

const (
	ColorTypeDefault   ColorType = iota // Use terminal default fg/bg (SGR 39/49)
	ColorTypeStandard                   // Standard 16 ANSI colors (0-15)
	ColorTypePalette                    // 256-color palette (0-255)
	ColorTypeTrueColor                  // 24-bit RGB
)

// Color represents a terminal color with its original specification preserved.
// This allows proper round-tripping for ANS files and dynamic palette swapping.
type Color struct {
	Type    ColorType // How the color was specified
	Index   uint8     // For Standard (0-15) or Palette (0-255)
	R, G, B uint8     // For TrueColor, or resolved RGB for display
}

// Predefined colors
var (
	DefaultForeground = Color{Type: ColorTypeDefault, R: 212, G: 212, B: 212}
	DefaultBackground = Color{Type: ColorTypeDefault, R: 30, G: 30, B: 30}
)

// StandardColor creates a standard 16-color ANSI color (index 0-15)
func StandardColor(index int) Color {
	if index < 0 || index > 15 {
		index = 7 // Default to white
	}
	rgb := ANSIColorsRGB[index]
	return Color{Type: ColorTypeStandard, Index: uint8(index), R: rgb.R, G: rgb.G, B: rgb.B}
}

// PaletteColor creates a 256-color palette color (index 0-255)
func PaletteColor(index int) Color {
	if index < 0 || index > 255 {
		index = 7 // Default to white
	}
	rgb := Get256ColorRGB(index)
	return Color{Type: ColorTypePalette, Index: uint8(index), R: rgb.R, G: rgb.G, B: rgb.B}
}

// TrueColor creates a 24-bit true color
func TrueColor(r, g, b uint8) Color {
	return Color{Type: ColorTypeTrueColor, R: r, G: g, B: b}
}

// IsDefault returns true if this is the default fg/bg color
func (c Color) IsDefault() bool {
	return c.Type == ColorTypeDefault
}

// ToANSIIndex returns the color index for standard/palette colors, or -1 for true color
func (c Color) ToANSIIndex() int {
	switch c.Type {
	case ColorTypeStandard, ColorTypePalette:
		return int(c.Index)
	default:
		return -1
	}
}

// ToSGRCode returns the SGR color code(s) for this color (foreground if isFg=true)
func (c Color) ToSGRCode(isFg bool) string {
	switch c.Type {
	case ColorTypeDefault:
		if isFg {
			return "39"
		}
		return "49"
	case ColorTypeStandard:
		idx := int(c.Index)
		if idx < 8 {
			// Normal colors: 30-37 or 40-47
			if isFg {
				return itoa(30 + idx)
			}
			return itoa(40 + idx)
		}
		// Bright colors: 90-97 or 100-107
		if isFg {
			return itoa(90 + idx - 8)
		}
		return itoa(100 + idx - 8)
	case ColorTypePalette:
		if isFg {
			return "38;5;" + itoa(int(c.Index))
		}
		return "48;5;" + itoa(int(c.Index))
	case ColorTypeTrueColor:
		if isFg {
			return "38;2;" + itoa(int(c.R)) + ";" + itoa(int(c.G)) + ";" + itoa(int(c.B))
		}
		return "48;2;" + itoa(int(c.R)) + ";" + itoa(int(c.G)) + ";" + itoa(int(c.B))
	}
	return ""
}

// itoa is a simple int to string conversion
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	if i < 0 {
		return "-" + itoa(-i)
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// BlinkMode determines how the blink attribute is rendered
type BlinkMode int

const (
	BlinkModeBounce BlinkMode = iota // Bobbing wave animation (default)
	BlinkModeBlink                   // Traditional on/off blinking
	BlinkModeBright                  // Interpret as bright background (VGA style)
)

// RGB holds just the red, green, blue components (used internally)
type RGB struct {
	R, G, B uint8
}

// Standard ANSI 16-color palette RGB values (in ANSI order for escape code compatibility)
var ANSIColorsRGB = []RGB{
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

// ANSIColors returns standard ANSI colors as full Color structs (for backwards compatibility)
var ANSIColors = func() []Color {
	colors := make([]Color, 16)
	for i := 0; i < 16; i++ {
		colors[i] = StandardColor(i)
	}
	return colors
}()

// VGAToANSI maps VGA/CGA color index to ANSI color index
var VGAToANSI = []int{0, 4, 2, 6, 1, 5, 3, 7, 8, 12, 10, 14, 9, 13, 11, 15}

// ANSIToVGA maps ANSI color index to VGA/CGA color index
var ANSIToVGA = []int{0, 4, 2, 6, 1, 5, 3, 7, 8, 12, 10, 14, 9, 13, 11, 15}

// Get256ColorRGB returns the RGB values for a 256-color palette index
func Get256ColorRGB(idx int) RGB {
	if idx < 0 {
		idx = 0
	} else if idx > 255 {
		idx = 255
	}
	if idx < 16 {
		return ANSIColorsRGB[idx]
	} else if idx < 232 {
		idx -= 16
		b := idx % 6
		g := (idx / 6) % 6
		r := idx / 36
		return RGB{R: uint8(r * 51), G: uint8(g * 51), B: uint8(b * 51)}
	} else {
		gray := uint8((idx-232)*10 + 8)
		return RGB{R: gray, G: gray, B: gray}
	}
}

// Get256Color returns the color for a 256-color mode index (returns PaletteColor type)
func Get256Color(idx int) Color {
	return PaletteColor(idx)
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
// Returns a TrueColor type
func ParseHexColor(s string) (Color, bool) {
	if len(s) == 0 || s[0] != '#' {
		return Color{}, false
	}
	s = s[1:]
	var r, g, b uint8
	switch len(s) {
	case 3:
		r = parseHexNibble(s[0]) * 17
		g = parseHexNibble(s[1]) * 17
		b = parseHexNibble(s[2]) * 17
	case 6:
		r = parseHexNibble(s[0])<<4 | parseHexNibble(s[1])
		g = parseHexNibble(s[2])<<4 | parseHexNibble(s[3])
		b = parseHexNibble(s[4])<<4 | parseHexNibble(s[5])
	default:
		return Color{}, false
	}
	return TrueColor(r, g, b), true
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
	"00_black": 0, "01_dark_blue": 1, "02_dark_green": 2, "03_dark_cyan": 3,
	"04_dark_red": 4, "05_purple": 5, "06_brown": 6, "07_silver": 7,
	"08_dark_gray": 8, "09_bright_blue": 9, "10_bright_green": 10, "11_bright_cyan": 11,
	"12_bright_red": 12, "13_pink": 13, "14_yellow": 14, "15_white": 15,
}

// ColorScheme defines the colors used by the terminal
type ColorScheme struct {
	Foreground Color
	Background Color
	Cursor     Color
	Selection  Color
	Palette    []Color
	BlinkMode  BlinkMode
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
func DefaultPaletteHex() []string {
	result := make([]string, 16)
	for vgaIdx := 0; vgaIdx < 16; vgaIdx++ {
		result[vgaIdx] = ANSIColors[VGAToANSI[vgaIdx]].ToHex()
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
		Foreground: TrueColor(212, 212, 212),
		Background: TrueColor(30, 30, 30),
		Cursor:     TrueColor(255, 255, 255),
		Selection:  TrueColor(68, 68, 68),
		Palette:    ANSIColors,
	}
}
