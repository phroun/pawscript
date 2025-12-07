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

// GetDefaultQuitShortcut returns the platform-appropriate default quit shortcut.
func GetDefaultQuitShortcut() string {
	if runtime.GOOS == "darwin" {
		return "Cmd+Q"
	}
	return "Ctrl+Q"
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
