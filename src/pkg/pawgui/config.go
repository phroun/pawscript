// Package pawgui provides shared functionality for PawScript GUI implementations.
// This package contains common code used by both pawgui-gtk and pawgui-qt.
package pawgui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/phroun/pawscript"
	"github.com/phroun/pawscript/pkg/purfecterm"
)

// Default font settings
const DefaultFontSize = 18

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

// GetDefaultCloseShortcut returns the platform-appropriate default close window shortcut.
func GetDefaultCloseShortcut() string {
	if runtime.GOOS == "darwin" {
		return "Cmd+W"
	}
	// On Windows and Linux, use Ctrl+F4
	return "Ctrl+F4"
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

// GetConfigSectionString extracts a string value from a config section.
// Handles PSLConfig, StoredList, and map[string]interface{} types.
func GetConfigSectionString(config interface{}, key string) string {
	// Helper to extract string from various value types
	toString := func(v interface{}) string {
		if v == nil {
			return ""
		}
		switch s := v.(type) {
		case string:
			return s
		case pawscript.QuotedString:
			return string(s)
		case pawscript.StoredString:
			return string(s)
		case pawscript.Symbol:
			return string(s)
		default:
			// Last resort: convert to string
			if str, ok := v.(fmt.Stringer); ok {
				return str.String()
			}
			return fmt.Sprintf("%v", v)
		}
	}

	if config == nil {
		return ""
	}

	// Try StoredList first (has NamedArgs method)
	if sl, ok := config.(pawscript.StoredList); ok {
		if args := sl.NamedArgs(); args != nil {
			if v, ok := args[key]; ok {
				return toString(v)
			}
		}
		return ""
	}

	// Try PSLConfig explicitly (it's a named type for map[string]interface{})
	if pc, ok := config.(pawscript.PSLConfig); ok {
		if v, ok := pc[key]; ok {
			return toString(v)
		}
		return ""
	}

	// Try as a plain map (covers map[string]interface{})
	if m, ok := config.(map[string]interface{}); ok {
		if v, ok := m[key]; ok {
			return toString(v)
		}
		return ""
	}

	// Try PSLList (might have named items)
	if pl, ok := config.(pawscript.PSLList); ok {
		// PSLList is []interface{}, so we can't directly get named args
		// But check if it has a single map item
		for _, item := range pl {
			if m, ok := item.(map[string]interface{}); ok {
				if v, ok := m[key]; ok {
					return toString(v)
				}
			}
		}
		return ""
	}

	return ""
}

// GetTerminalBackground returns the configured terminal background color for the current theme.
func (h *ConfigHelper) GetTerminalBackground() purfecterm.Color {
	return h.GetTerminalBackgroundForTheme(h.IsTermThemeDark())
}

// GetTerminalBackgroundForTheme returns the terminal background color for the specified theme.
func (h *ConfigHelper) GetTerminalBackgroundForTheme(isDark bool) purfecterm.Color {
	if h.Config == nil {
		if isDark {
			return purfecterm.TrueColor(30, 30, 30) // Default dark background
		}
		return purfecterm.TrueColor(255, 255, 255) // Default light background
	}

	// Try theme-specific section first
	themeSection := "term_colors_dark"
	if !isDark {
		themeSection = "term_colors_light"
	}
	if themeConfig, ok := h.Config[themeSection]; ok {
		if hex := GetConfigSectionString(themeConfig, "0_background"); hex != "" {
			if c, ok := purfecterm.ParseHexColor(hex); ok {
				return c
			}
		}
	}

	// Fall back to base term_colors section
	if termConfig, ok := h.Config["term_colors"]; ok {
		if hex := GetConfigSectionString(termConfig, "0_background"); hex != "" {
			if c, ok := purfecterm.ParseHexColor(hex); ok {
				return c
			}
		}
	}

	// Default colors
	if isDark {
		return purfecterm.TrueColor(30, 30, 30)
	}
	return purfecterm.TrueColor(255, 255, 255)
}

// GetTerminalForeground returns the configured terminal foreground color for the current theme.
func (h *ConfigHelper) GetTerminalForeground() purfecterm.Color {
	return h.GetTerminalForegroundForTheme(h.IsTermThemeDark())
}

// GetTerminalForegroundForTheme returns the terminal foreground color for the specified theme.
func (h *ConfigHelper) GetTerminalForegroundForTheme(isDark bool) purfecterm.Color {
	if h.Config == nil {
		if isDark {
			return purfecterm.TrueColor(212, 212, 212) // Default light gray on dark
		}
		return purfecterm.TrueColor(30, 30, 30) // Default dark text on light
	}

	// Try theme-specific section first
	themeSection := "term_colors_dark"
	if !isDark {
		themeSection = "term_colors_light"
	}
	if themeConfig, ok := h.Config[themeSection]; ok {
		if hex := GetConfigSectionString(themeConfig, "9_foreground"); hex != "" {
			if c, ok := purfecterm.ParseHexColor(hex); ok {
				return c
			}
		}
	}

	// Fall back to base term_colors section
	if termConfig, ok := h.Config["term_colors"]; ok {
		if hex := GetConfigSectionString(termConfig, "9_foreground"); hex != "" {
			if c, ok := purfecterm.ParseHexColor(hex); ok {
				return c
			}
		}
	}

	// Default colors
	if isDark {
		return purfecterm.TrueColor(212, 212, 212)
	}
	return purfecterm.TrueColor(30, 30, 30)
}

// GetColorPalette returns the configured 16-color ANSI palette for the current theme.
func (h *ConfigHelper) GetColorPalette() []purfecterm.Color {
	return h.GetColorPaletteForTheme(h.IsTermThemeDark())
}

// GetColorPaletteForTheme returns the 16-color ANSI palette for the specified theme.
// Config uses VGA-style naming (01_dark_blue, etc.) but ANSI escape codes
// expect ANSI order (index 1 = red). We map VGA config indices to ANSI palette indices.
// Colors are merged: term_colors base + term_colors_light/dark theme overrides.
func (h *ConfigHelper) GetColorPaletteForTheme(isDark bool) []purfecterm.Color {
	palette := make([]purfecterm.Color, 16)
	copy(palette, purfecterm.ANSIColors)

	if h.Config == nil {
		return palette
	}

	names := purfecterm.PaletteColorNames()

	// First apply base term_colors
	if termConfig, ok := h.Config["term_colors"]; ok {
		for vgaIdx, name := range names {
			if hex := GetConfigSectionString(termConfig, name); hex != "" {
				if c, ok := purfecterm.ParseHexColor(hex); ok {
					ansiIdx := purfecterm.VGAToANSI[vgaIdx]
					palette[ansiIdx] = c
				}
			}
		}
	}

	// Then apply theme-specific overrides
	themeSection := "term_colors_dark"
	if !isDark {
		themeSection = "term_colors_light"
	}
	if themeConfig, ok := h.Config[themeSection]; ok {
		for vgaIdx, name := range names {
			if hex := GetConfigSectionString(themeConfig, name); hex != "" {
				if c, ok := purfecterm.ParseHexColor(hex); ok {
					ansiIdx := purfecterm.VGAToANSI[vgaIdx]
					palette[ansiIdx] = c
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

// GetCloseShortcut returns the configured close window shortcut.
// Valid values: any shortcut string like "Cmd+W", "Ctrl+F4", or "" (disabled)
func (h *ConfigHelper) GetCloseShortcut() string {
	if h.Config != nil {
		if val, exists := h.Config["close_shortcut"]; exists {
			if val == nil {
				return "" // nil means disabled
			}
			if s, ok := val.(string); ok {
				return s
			}
		}
	}
	return GetDefaultCloseShortcut()
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

// GetTermTheme returns the configured terminal theme.
// Valid values: "light", "dark" (default: "dark")
func (h *ConfigHelper) GetTermTheme() string {
	if h.Config != nil {
		theme := h.Config.GetString("term_theme", "dark")
		if theme == "light" || theme == "dark" {
			return theme
		}
	}
	return "dark"
}

// IsTermThemeDark returns true if the terminal theme is dark
func (h *ConfigHelper) IsTermThemeDark() bool {
	return h.GetTermTheme() == "dark"
}

// GetColorScheme returns a complete dual-palette ColorScheme from config.
// The returned ColorScheme contains both dark and light mode colors.
func (h *ConfigHelper) GetColorScheme() purfecterm.ColorScheme {
	return h.GetDualColorScheme()
}

// GetDualColorScheme returns a complete ColorScheme with both dark and light palettes.
// This allows the terminal to switch between modes via DECSCNM without needing
// to reload colors from config.
func (h *ConfigHelper) GetDualColorScheme() purfecterm.ColorScheme {
	return purfecterm.ColorScheme{
		// Dark mode colors
		DarkForeground: h.GetTerminalForegroundForTheme(true),
		DarkBackground: h.GetTerminalBackgroundForTheme(true),
		DarkPalette:    h.GetColorPaletteForTheme(true),

		// Light mode colors
		LightForeground: h.GetTerminalForegroundForTheme(false),
		LightBackground: h.GetTerminalBackgroundForTheme(false),
		LightPalette:    h.GetColorPaletteForTheme(false),

		// Shared settings
		Cursor:    purfecterm.TrueColor(255, 255, 255),
		Selection: purfecterm.TrueColor(68, 68, 68),
		BlinkMode: h.GetBlinkMode(),
	}
}

// GetColorSchemeForTheme returns a complete dual-palette ColorScheme.
// Deprecated: Use GetDualColorScheme() instead. The isDark parameter is ignored
// as the returned ColorScheme contains both dark and light palettes.
func (h *ConfigHelper) GetColorSchemeForTheme(isDark bool) purfecterm.ColorScheme {
	return h.GetDualColorScheme()
}

// GetPSLColors returns the PSL result display color configuration for the current theme.
func (h *ConfigHelper) GetPSLColors() pawscript.DisplayColorConfig {
	return h.GetPSLColorsForTheme(h.IsTermThemeDark())
}

// GetPSLColorsForTheme returns the PSL result display color configuration for the specified theme.
// It checks psl_colors_light/dark first, then falls back to psl_colors for each key.
func (h *ConfigHelper) GetPSLColorsForTheme(isDark bool) pawscript.DisplayColorConfig {
	cfg := pawscript.DefaultDisplayColors()

	if h.Config == nil {
		return cfg
	}

	// Helper to get named args from a config value
	getNamedArgs := func(key string) map[string]interface{} {
		val, ok := h.Config[key]
		if !ok {
			return nil
		}
		switch v := val.(type) {
		case pawscript.StoredList:
			return v.NamedArgs()
		case pawscript.PSLConfig:
			return map[string]interface{}(v)
		case map[string]interface{}:
			return v
		}
		return nil
	}

	// Get base and theme-specific configs
	baseArgs := getNamedArgs("psl_colors")
	themeSection := "psl_colors_dark"
	if !isDark {
		themeSection = "psl_colors_light"
	}
	themeArgs := getNamedArgs(themeSection)

	// Helper to extract string value with theme override
	getStr := func(key string) string {
		// Try theme-specific first
		if themeArgs != nil {
			if v, ok := themeArgs[key]; ok {
				switch s := v.(type) {
				case string:
					return s
				case pawscript.QuotedString:
					return string(s)
				case pawscript.Symbol:
					return string(s)
				}
			}
		}
		// Fall back to base
		if baseArgs != nil {
			if v, ok := baseArgs[key]; ok {
				switch s := v.(type) {
				case string:
					return s
				case pawscript.QuotedString:
					return string(s)
				case pawscript.Symbol:
					return string(s)
				}
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
	if _, exists := h.Config["close_shortcut"]; !exists {
		h.Config.Set("close_shortcut", GetDefaultCloseShortcut())
		modified = true
	}
	if _, exists := h.Config["theme"]; !exists {
		h.Config.Set("theme", "auto")
		modified = true
	}
	if _, exists := h.Config["term_theme"]; !exists {
		h.Config.Set("term_theme", "dark")
		modified = true
	}
	if _, exists := h.Config["default_blink"]; !exists {
		h.Config.Set("default_blink", "bounce")
		modified = true
	}

	// term_colors: base palette colors (can be overridden by theme-specific sections)
	if _, exists := h.Config["term_colors"]; !exists {
		termConfig := pawscript.PSLConfig{}
		names := purfecterm.PaletteColorNames()
		hexColors := purfecterm.DefaultPaletteHex()
		for i, name := range names {
			termConfig.Set(name, hexColors[i])
		}
		h.Config.Set("term_colors", termConfig)
		modified = true
	}

	// term_colors_dark: dark theme overrides (includes background/foreground)
	if _, exists := h.Config["term_colors_dark"]; !exists {
		darkConfig := pawscript.PSLConfig{}
		darkConfig.Set("0_background", "#1E1E1E")
		darkConfig.Set("9_foreground", "#D4D4D4")
		h.Config.Set("term_colors_dark", darkConfig)
		modified = true
	}

	// term_colors_light: light theme overrides (includes background/foreground)
	if _, exists := h.Config["term_colors_light"]; !exists {
		lightConfig := pawscript.PSLConfig{}
		lightConfig.Set("0_background", "#FFFFFF")
		lightConfig.Set("9_foreground", "#1E1E1E")
		h.Config.Set("term_colors_light", lightConfig)
		modified = true
	}

	// psl_colors: base PSL display colors
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

	// psl_colors_dark: dark theme PSL color overrides (empty by default)
	if _, exists := h.Config["psl_colors_dark"]; !exists {
		h.Config.Set("psl_colors_dark", pawscript.PSLConfig{})
		modified = true
	}

	// psl_colors_light: light theme PSL color overrides (empty by default)
	if _, exists := h.Config["psl_colors_light"]; !exists {
		h.Config.Set("psl_colors_light", pawscript.PSLConfig{})
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
