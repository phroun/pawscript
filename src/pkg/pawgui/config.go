// Package pawgui provides shared functionality for PawScript GUI implementations.
// This package contains common code used by both pawgui-gtk and pawgui-qt.
package pawgui

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/phroun/pawscript"
	"github.com/phroun/pawscript/pkg/purfecterm"
)

// Default font settings
const DefaultFontSize = 22

// ThemeMode represents the GUI theme setting
type ThemeMode string

const (
	ThemeAuto  ThemeMode = "auto"  // Follow OS preference
	ThemeDark  ThemeMode = "dark"  // Force dark theme
	ThemeLight ThemeMode = "light" // Force light theme
)

// GetDefaultFont returns the best monospace font for the current platform.
// Includes cross-platform fallbacks so config files can be shared between OS.
func GetDefaultFont() string {
	switch runtime.GOOS {
	case "darwin":
		return "Menlo, JetBrains Mono, SF Mono, Cascadia Mono, Consolas, Monaco, Courier New"
	case "windows":
		return "Cascadia Mono, Consolas, JetBrains Mono, Menlo, SF Mono, Monaco, Courier New"
	default:
		return "JetBrains Mono, DejaVu Sans Mono, Liberation Mono, Menlo, Consolas, monospace"
	}
}

// GetDefaultUnicodeFont returns the best Unicode fallback font for the current platform.
// Used for characters missing from the main monospace font (Hebrew, Greek, Cyrillic, etc.)
func GetDefaultUnicodeFont() string {
	switch runtime.GOOS {
	case "darwin":
		return "Arial Unicode MS, Apple Symbols, Lucida Grande, Apple Color Emoji"
	case "windows":
		return "Segoe UI Symbol, Arial Unicode MS, Lucida Sans Unicode, Segoe UI Emoji"
	default:
		return "DejaVu Sans, Noto Sans, Noto Sans Symbols, FreeSans, Symbola"
	}
}

// GetDefaultCJKFont returns the best CJK (Chinese/Japanese/Korean) font for the current platform.
// Used specifically for CJK characters which often need specialized fonts.
func GetDefaultCJKFont() string {
	switch runtime.GOOS {
	case "darwin":
		return "Hiragino Sans, PingFang SC, Heiti SC, MS Gothic, Apple SD Gothic Neo"
	case "windows":
		return "Yu Gothic, Microsoft YaHei, MS Gothic, SimSun, Malgun Gothic"
	default:
		return "Noto Sans CJK SC, Noto Sans CJK JP, WenQuanYi Micro Hei, Droid Sans Fallback, Source Han Sans"
	}
}

// GetDefaultQuitShortcut returns the platform-appropriate default quit shortcut.
// Never uses Ctrl+key as Ctrl should pass through to terminal applications.
func GetDefaultQuitShortcut() string {
	if runtime.GOOS == "darwin" {
		return "Cmd+Q"
	}
	// On Windows and Linux, use Alt+F4 (Ctrl keys should pass to terminal)
	return "Alt+F4"
}

// ConfigHelper provides common configuration access methods.
type ConfigHelper struct {
	Config pawscript.PSLConfig
}

// NewConfigHelper creates a new ConfigHelper with the given config.
func NewConfigHelper(config pawscript.PSLConfig) *ConfigHelper {
	return &ConfigHelper{Config: config}
}

// GetFontFamily returns the configured font family.
func (h *ConfigHelper) GetFontFamily() string {
	if h.Config != nil {
		if family := h.Config.GetString("font_family", ""); family != "" {
			return family
		}
	}
	return GetDefaultFont()
}

// GetFontSize returns the configured font size.
func (h *ConfigHelper) GetFontSize() int {
	if h.Config != nil {
		if size := h.Config.GetInt("font_size", 0); size > 0 {
			return size
		}
	}
	return DefaultFontSize
}

// GetFontFamilyUnicode returns the configured Unicode fallback font family.
// Used for characters missing from the main monospace font (Hebrew, Greek, Cyrillic, etc.)
func (h *ConfigHelper) GetFontFamilyUnicode() string {
	if h.Config != nil {
		if family := h.Config.GetString("font_family_unicode", ""); family != "" {
			return family
		}
	}
	return GetDefaultUnicodeFont()
}

// GetFontFamilyCJK returns the configured CJK (Chinese/Japanese/Korean) font family.
func (h *ConfigHelper) GetFontFamilyCJK() string {
	if h.Config != nil {
		if family := h.Config.GetString("font_family_cjk", ""); family != "" {
			return family
		}
	}
	return GetDefaultCJKFont()
}

// GetUIScale returns the configured UI scale factor (default 1.0).
func (h *ConfigHelper) GetUIScale() float64 {
	if h.Config != nil {
		if scale := h.Config.GetFloat("ui_scale", 0); scale > 0 {
			return scale
		}
	}
	return 1.0
}

// GetOptimizationLevel returns the configured optimization level (default 1).
// 0 = no caching, 1 = cache macro/loop bodies
func (h *ConfigHelper) GetOptimizationLevel() int {
	if h.Config != nil {
		return h.Config.GetInt("optimization_level", 1)
	}
	return 1
}

// GetTerminalBackground returns the configured terminal background color.
func (h *ConfigHelper) GetTerminalBackground() purfecterm.Color {
	if h.Config != nil {
		if hex := h.Config.GetString("terminal_background", ""); hex != "" {
			if c, ok := purfecterm.ParseHexColor(hex); ok {
				return c
			}
		}
	}
	return purfecterm.Color{R: 30, G: 30, B: 30} // Default dark background
}

// GetTerminalForeground returns the configured terminal foreground color.
func (h *ConfigHelper) GetTerminalForeground() purfecterm.Color {
	if h.Config != nil {
		if hex := h.Config.GetString("terminal_foreground", ""); hex != "" {
			if c, ok := purfecterm.ParseHexColor(hex); ok {
				return c
			}
		}
	}
	return purfecterm.Color{R: 212, G: 212, B: 212} // Default light gray
}

// GetColorPalette returns the configured 16-color ANSI palette.
// Config uses VGA-style naming (01_dark_blue, etc.) but ANSI escape codes
// expect ANSI order (index 1 = red). We map VGA config indices to ANSI palette indices.
func (h *ConfigHelper) GetColorPalette() []purfecterm.Color {
	palette := make([]purfecterm.Color, 16)
	copy(palette, purfecterm.ANSIColors)

	if h.Config == nil {
		return palette
	}

	// Check for palette_colors nested config
	if paletteConfig, ok := h.Config["palette_colors"]; ok {
		if pc, ok := paletteConfig.(pawscript.PSLConfig); ok {
			names := purfecterm.PaletteColorNames()
			for vgaIdx, name := range names {
				if hex := pc.GetString(name, ""); hex != "" {
					if c, ok := purfecterm.ParseHexColor(hex); ok {
						// Map VGA config index to ANSI palette index
						ansiIdx := purfecterm.VGAToANSI[vgaIdx]
						palette[ansiIdx] = c
					}
				}
			}
		}
	}

	return palette
}

// GetBlinkMode returns the configured blink mode.
// "bounce" = bobbing wave animation (default)
// "blink" = traditional on/off blinking
// "bright" = interpret as bright background (VGA style)
func (h *ConfigHelper) GetBlinkMode() purfecterm.BlinkMode {
	if h.Config != nil {
		mode := h.Config.GetString("default_blink", "bounce")
		return purfecterm.ParseBlinkMode(mode)
	}
	return purfecterm.BlinkModeBounce
}

// GetQuitShortcut returns the configured quit shortcut.
// Valid values: "Cmd+Q", "Ctrl+Q", "Alt+F4", or "" (disabled)
func (h *ConfigHelper) GetQuitShortcut() string {
	if h.Config != nil {
		if val, exists := h.Config["quit_shortcut"]; exists {
			if val == nil {
				return "" // nil means disabled
			}
			if s, ok := val.(string); ok {
				return s
			}
		}
	}
	return GetDefaultQuitShortcut()
}

// GetTheme returns the configured GUI theme mode.
// Valid values: "auto", "dark", "light"
func (h *ConfigHelper) GetTheme() ThemeMode {
	if h.Config != nil {
		theme := h.Config.GetString("theme", "auto")
		switch theme {
		case "dark":
			return ThemeDark
		case "light":
			return ThemeLight
		}
	}
	return ThemeAuto
}

// GetColorScheme returns a complete ColorScheme from config.
func (h *ConfigHelper) GetColorScheme() purfecterm.ColorScheme {
	return purfecterm.ColorScheme{
		Foreground: h.GetTerminalForeground(),
		Background: h.GetTerminalBackground(),
		Cursor:     purfecterm.Color{R: 255, G: 255, B: 255},
		Selection:  purfecterm.Color{R: 68, G: 68, B: 68},
		Palette:    h.GetColorPalette(),
		BlinkMode:  h.GetBlinkMode(),
	}
}

// GetPSLColors returns the PSL result display color configuration.
func (h *ConfigHelper) GetPSLColors() pawscript.DisplayColorConfig {
	cfg := pawscript.DefaultDisplayColors()

	if h.Config == nil {
		return cfg
	}

	colorsVal, ok := h.Config["psl_colors"]
	if !ok {
		return cfg
	}

	// Handle both StoredList and PSLConfig (which is a map)
	var namedArgs map[string]interface{}
	switch v := colorsVal.(type) {
	case pawscript.StoredList:
		namedArgs = v.NamedArgs()
	case pawscript.PSLConfig:
		namedArgs = map[string]interface{}(v)
	case map[string]interface{}:
		namedArgs = v
	default:
		return cfg
	}

	if namedArgs == nil {
		return cfg
	}

	// Helper to extract string value
	getStr := func(key string) string {
		if v, ok := namedArgs[key]; ok {
			switch s := v.(type) {
			case string:
				return s
			case pawscript.QuotedString:
				return string(s)
			case pawscript.Symbol:
				return string(s)
			}
		}
		return ""
	}

	if v := getStr("reset"); v != "" {
		cfg.Reset = v
	}
	if v := getStr("key"); v != "" {
		cfg.Key = v
	}
	if v := getStr("string"); v != "" {
		cfg.String = v
	}
	if v := getStr("int"); v != "" {
		cfg.Int = v
	}
	if v := getStr("float"); v != "" {
		cfg.Float = v
	}
	if v := getStr("true"); v != "" {
		cfg.True = v
	}
	if v := getStr("false"); v != "" {
		cfg.False = v
	}
	if v := getStr("nil"); v != "" {
		cfg.Nil = v
	}
	if v := getStr("bracket"); v != "" {
		cfg.Bracket = v
	}
	if v := getStr("colon"); v != "" {
		cfg.Colon = v
	}
	if v := getStr("symbol"); v != "" {
		cfg.Symbol = v
	}
	if v := getStr("object"); v != "" {
		cfg.Object = v
	}
	if v := getStr("bytes"); v != "" {
		cfg.Bytes = v
	}

	return cfg
}

// PopulateDefaults ensures all config keys have default values.
// Returns true if the config was modified.
func (h *ConfigHelper) PopulateDefaults() bool {
	if h.Config == nil {
		return false
	}

	modified := false

	if _, exists := h.Config["font_family"]; !exists {
		h.Config.Set("font_family", GetDefaultFont())
		modified = true
	}
	if _, exists := h.Config["font_size"]; !exists {
		h.Config.Set("font_size", DefaultFontSize)
		modified = true
	}
	if _, exists := h.Config["font_family_unicode"]; !exists {
		h.Config.Set("font_family_unicode", GetDefaultUnicodeFont())
		modified = true
	}
	if _, exists := h.Config["font_family_cjk"]; !exists {
		h.Config.Set("font_family_cjk", GetDefaultCJKFont())
		modified = true
	}
	if _, exists := h.Config["ui_scale"]; !exists {
		h.Config.Set("ui_scale", 1.0)
		modified = true
	}
	if _, exists := h.Config["optimization_level"]; !exists {
		h.Config.Set("optimization_level", 1)
		modified = true
	}
	if _, exists := h.Config["quit_shortcut"]; !exists {
		h.Config.Set("quit_shortcut", GetDefaultQuitShortcut())
		modified = true
	}
	if _, exists := h.Config["theme"]; !exists {
		h.Config.Set("theme", "auto")
		modified = true
	}
	if _, exists := h.Config["terminal_background"]; !exists {
		h.Config.Set("terminal_background", "#1E1E1E")
		modified = true
	}
	if _, exists := h.Config["terminal_foreground"]; !exists {
		h.Config.Set("terminal_foreground", "#D4D4D4")
		modified = true
	}
	if _, exists := h.Config["palette_colors"]; !exists {
		paletteConfig := pawscript.PSLConfig{}
		names := purfecterm.PaletteColorNames()
		hexColors := purfecterm.DefaultPaletteHex()
		for i, name := range names {
			paletteConfig.Set(name, hexColors[i])
		}
		h.Config.Set("palette_colors", paletteConfig)
		modified = true
	}
	if _, exists := h.Config["default_blink"]; !exists {
		h.Config.Set("default_blink", "bounce")
		modified = true
	}
	if _, exists := h.Config["psl_colors"]; !exists {
		pslColorsConfig := pawscript.PSLConfig{}
		defaultColors := pawscript.DefaultDisplayColors()
		pslColorsConfig.Set("reset", defaultColors.Reset)
		pslColorsConfig.Set("key", defaultColors.Key)
		pslColorsConfig.Set("string", defaultColors.String)
		pslColorsConfig.Set("int", defaultColors.Int)
		pslColorsConfig.Set("float", defaultColors.Float)
		pslColorsConfig.Set("true", defaultColors.True)
		pslColorsConfig.Set("false", defaultColors.False)
		pslColorsConfig.Set("nil", defaultColors.Nil)
		pslColorsConfig.Set("bracket", defaultColors.Bracket)
		pslColorsConfig.Set("colon", defaultColors.Colon)
		pslColorsConfig.Set("symbol", defaultColors.Symbol)
		pslColorsConfig.Set("object", defaultColors.Object)
		pslColorsConfig.Set("bytes", defaultColors.Bytes)
		h.Config.Set("psl_colors", pslColorsConfig)
		modified = true
	}

	return modified
}

// GetConfigDir returns the configuration directory path.
func GetConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".paw")
}

// GetConfigPath returns the full path to the config file.
func GetConfigPath() string {
	return filepath.Join(GetConfigDir(), "pawgui.psl")
}
