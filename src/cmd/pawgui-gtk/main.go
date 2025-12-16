// pawgui-gtk - GTK3-based GUI for PawScript with custom terminal emulator
// Cross-platform: works on Linux, macOS, and Windows
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/gotk3/gotk3/pango"
	"github.com/phroun/pawscript"
	"github.com/phroun/pawscript/pkg/pawgui"
	"github.com/phroun/pawscript/pkg/purfecterm"
	purfectermgtk "github.com/phroun/pawscript/pkg/purfecterm-gtk"
	"github.com/sqweek/dialog"
)

var version = "dev" // set via -ldflags at build time

// Default font size constant (uses shared package value)
const defaultFontSize = pawgui.DefaultFontSize

const (
	appID   = "com.pawscript.pawgui-gtk"
	appName = "PawScript Launcher (GTK)"
)

// init sets up GTK data paths on Windows for icons, schemas, etc.
// Note: This doesn't help with DLL loading (too late), but icons load at runtime.
func init() {
	if runtime.GOOS != "windows" {
		return
	}

	exePath, err := os.Executable()
	if err != nil {
		return
	}
	exeDir := filepath.Dir(exePath)

	// Set GDK-Pixbuf loader path
	loaderCache := filepath.Join(exeDir, "lib", "gdk-pixbuf-2.0", "2.10.0", "loaders.cache")
	if _, err := os.Stat(loaderCache); err == nil {
		os.Setenv("GDK_PIXBUF_MODULE_FILE", loaderCache)
	}

	// Set data directories for icons and schemas
	shareDir := filepath.Join(exeDir, "share")
	if _, err := os.Stat(shareDir); err == nil {
		os.Setenv("XDG_DATA_DIRS", shareDir)
		os.Setenv("GTK_DATA_PREFIX", exeDir)
	}

	// Set schema directory
	schemaDir := filepath.Join(shareDir, "glib-2.0", "schemas")
	if _, err := os.Stat(schemaDir); err == nil {
		os.Setenv("GSETTINGS_SCHEMA_DIR", schemaDir)
	}
}

// Global state
var (
	currentDir  string
	mainWindow  *gtk.ApplicationWindow
	app         *gtk.Application // Store app reference for creating new windows
	fileList    *gtk.ListBox
	terminal    *purfectermgtk.Terminal
	pathButton  *gtk.MenuButton // Path selector button with dropdown menu
	pathLabel   *gtk.Label      // Label inside path button showing current path
	pathMenu    *gtk.Menu       // Dropdown menu for path selection
	runButton   *gtk.Button
	contextMenu *gtk.Menu // Right-click context menu for terminal

	// Console I/O for PawScript
	consoleOutCh   *pawscript.StoredChannel
	consoleInCh    *pawscript.StoredChannel
	stdoutWriter   *io.PipeWriter
	stdinReader    *io.PipeReader
	stdinWriter    *io.PipeWriter
	clearInputFunc func()
	flushFunc      func() // Flush pending output
	scriptRunning  bool
	scriptMu       sync.Mutex

	// REPL for interactive mode when no script is running
	consoleREPL *pawscript.REPL

	// Configuration loaded at startup
	appConfig    pawscript.PSLConfig
	configHelper *pawgui.ConfigHelper

	// Track actual applied theme (resolved from Auto if needed)
	appliedThemeIsDark bool

	// Launcher narrow strip (for multiple toolbar buttons)
	launcherNarrowStrip    *gtk.Box           // The narrow strip container
	launcherMenuButton     *gtk.Button        // Hamburger button in path selector (when strip hidden)
	launcherStripMenuBtn   *gtk.Button        // Hamburger button in narrow strip (when strip visible)
	launcherWidePanel      *gtk.Box           // The wide panel (file browser)
	launcherPaned          *gtk.Paned         // The main splitter
	launcherRegisteredBtns []*ToolbarButton   // Additional registered buttons for launcher
	launcherToolbarData    *WindowToolbarData // Toolbar data for the launcher window
	launcherMenuCtx        *MenuContext       // Menu context for launcher window (updated after creation)
	launcherMenu           *gtk.Menu          // Shared hamburger menu for launcher (used by both buttons)

	// Per-window toolbar data (keyed by PawScript instance or window)
	toolbarDataByPS     = make(map[*pawscript.PawScript]*WindowToolbarData)
	toolbarDataByWindow = make(map[*gtk.ApplicationWindow]*WindowToolbarData)
	toolbarDataMu       sync.Mutex

	// UI scale operation guard - prevents re-entrant/concurrent scale operations
	uiScaleMu        sync.Mutex
	uiScaleInProgress bool

	// File list icon tracking
	rowIconTypeMap      = make(map[*gtk.ListBoxRow]gtkIconType)
	previousSelectedRow *gtk.ListBoxRow
)

// gtkIconType represents the type of icon for a file list row
type gtkIconType int

const (
	gtkIconTypeFolder gtkIconType = iota
	gtkIconTypeFolderUp
	gtkIconTypePawFile
)

// Icon size constants (base values at 1.0 scale)
const (
	gtkFileListIconSize = 26 // File list icons
	gtkToolbarIconSize  = 24 // Toolbar/hamburger button icons
	gtkMenuIconSize     = 16 // Menu checkmark and path menu icons
)

// Scaled icon size helpers - these return values adjusted for current UI scale
func scaledFileListIconSize() int {
	return int(float64(gtkFileListIconSize) * getUIScale())
}

func scaledToolbarIconSize() int {
	return int(float64(gtkToolbarIconSize) * getUIScale())
}

func scaledMenuIconSize() int {
	return int(float64(gtkMenuIconSize) * getUIScale())
}

// safeRemoveChildren removes all children from a container safely
// This collects widgets first, removes them, clears Go references,
// and forces a GC to run finalizers in a controlled state
func safeRemoveChildren(container interface {
	GetChildren() *glib.List
	Remove(gtk.IWidget)
}) {
	children := container.GetChildren()
	if children == nil {
		return
	}

	var toRemove []*gtk.Widget
	children.Foreach(func(item interface{}) {
		if widget, ok := item.(gtk.IWidget); ok {
			toRemove = append(toRemove, widget.ToWidget())
		}
	})

	for _, widget := range toRemove {
		container.Remove(widget)
		// Destroy the widget to ensure GTK properly releases it
		// This prevents orphaned wrappers with active finalizers
		widget.Destroy()
	}

	// Clear references and force GC to run finalizers now
	toRemove = nil
	runtime.GC()
}

// WindowToolbarData holds per-window toolbar state for dummy_button command
type WindowToolbarData struct {
	strip          *gtk.Box                // The narrow strip container
	menuButton     *gtk.Button             // The hamburger menu button
	registeredBtns []*ToolbarButton        // Additional registered buttons
	terminal       *purfectermgtk.Terminal // Terminal for Feed() calls
	updateFunc     func()                  // Function to update the strip's buttons
}

// ToolbarButton represents a registered toolbar button
type ToolbarButton struct {
	Icon    string      // Icon name or path
	Tooltip string      // Tooltip text
	OnClick func()      // Click handler
	Menu    *gtk.Menu   // Optional dropdown menu (if nil, OnClick is used)
	widget  *gtk.Button // The actual button widget
}

// ToolbarStrip manages a collapsible strip of toolbar buttons
type ToolbarStrip struct {
	container      *gtk.Box         // The strip container
	buttons        []*ToolbarButton // Registered buttons (excluding mandatory first)
	menuButton     *gtk.Button      // The mandatory hamburger menu button
	menu           *gtk.Menu        // The hamburger menu
	minWidth       int              // Minimum width before collapsing
	isScriptWindow bool             // True if this is a script window (no wide panel)
}

// --- Configuration Management ---

// getConfigDir returns the path to the .paw config directory in the user's home
func getConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".paw")
}

// getConfigPath returns the path to the pawgui-gtk.psl config file
func getConfigPath() string {
	configDir := getConfigDir()
	if configDir == "" {
		return ""
	}
	return filepath.Join(configDir, "pawgui-gtk.psl")
}

// loadConfig loads the configuration from ~/.paw/pawgui-gtk.psl
// Returns an empty config if the file doesn't exist or can't be read
func loadConfig() pawscript.PSLConfig {
	configPath := getConfigPath()
	if configPath == "" {
		return pawscript.PSLConfig{}
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return pawscript.PSLConfig{}
	}

	config, err := pawscript.ParsePSL(string(data))
	if err != nil {
		return pawscript.PSLConfig{}
	}

	return config
}

// saveConfig saves the configuration to ~/.paw/pawgui-gtk.psl
// Silently fails if there are any errors (graceful degradation)
func saveConfig(config pawscript.PSLConfig) {
	configPath := getConfigPath()
	if configPath == "" {
		return
	}

	// Ensure config directory exists
	configDir := getConfigDir()
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return
	}

	data := pawscript.SerializePSLPretty(config)
	_ = os.WriteFile(configPath, []byte(data+"\n"), 0644)
}

// saveBrowseDir saves the current browse directory to config
func saveBrowseDir(dir string) {
	appConfig.Set("last_browse_dir", dir)
	saveConfig(appConfig)
}

// Configuration getter wrappers using shared configHelper
func getFontFamily() string                      { return configHelper.GetFontFamily() }
func getFontFamilyUnicode() string               { return configHelper.GetFontFamilyUnicode() }
func getFontFamilyCJK() string                   { return configHelper.GetFontFamilyCJK() }
func getFontSize() int                           { return configHelper.GetFontSize() }
func getUIScale() float64                        { return configHelper.GetUIScale() }
func getOptimizationLevel() int                  { return configHelper.GetOptimizationLevel() }
func getTerminalBackground() purfecterm.Color    { return configHelper.GetTerminalBackground() }
func getTerminalForeground() purfecterm.Color    { return configHelper.GetTerminalForeground() }
func getColorPalette() []purfecterm.Color        { return configHelper.GetColorPalette() }
func getBlinkMode() purfecterm.BlinkMode         { return configHelper.GetBlinkMode() }
func getQuitShortcut() string                    { return configHelper.GetQuitShortcut() }
func getDefaultQuitShortcut() string             { return pawgui.GetDefaultQuitShortcut() }
func getCloseShortcut() string                   { return configHelper.GetCloseShortcut() }
func getDefaultCloseShortcut() string            { return pawgui.GetDefaultCloseShortcut() }
func getPSLColors() pawscript.DisplayColorConfig { return configHelper.GetPSLColors() }
func isTermThemeDark() bool                      { return configHelper.IsTermThemeDark() }

func getColorSchemeForTheme(isDark bool) purfecterm.ColorScheme {
	// Returns a dual-palette ColorScheme (isDark is now ignored)
	return configHelper.GetDualColorScheme()
}

func getDualColorScheme() purfecterm.ColorScheme {
	return configHelper.GetDualColorScheme()
}

// getLauncherWidth returns the saved launcher panel width, defaulting to 250 * uiScale
func getLauncherWidth() int {
	saved := appConfig.GetInt("launcher_width", -1)
	if saved >= 0 {
		return saved
	}
	return int(250 * getUIScale())
}

// saveLauncherWidth saves the launcher panel width to config
func saveLauncherWidth(width int) {
	appConfig.Set("launcher_width", width)
	saveConfig(appConfig)
}

// getLauncherPosition returns the saved launcher window position (x, y)
func getLauncherPosition() (int, int) {
	if items := appConfig.GetItems("launcher_position"); len(items) >= 2 {
		x := pslToInt(items[0])
		y := pslToInt(items[1])
		return x, y
	}
	return -1, -1 // -1 means not set (let window manager decide)
}

// saveLauncherPosition saves the launcher window position to config
func saveLauncherPosition(x, y int) {
	appConfig.Set("launcher_position", pawscript.PSLList{x, y})
	saveConfig(appConfig)
}

// getLauncherSize returns the saved launcher window size (width, height)
func getLauncherSize() (int, int) {
	if items := appConfig.GetItems("launcher_size"); len(items) >= 2 {
		w := pslToInt(items[0])
		h := pslToInt(items[1])
		if w > 0 && h > 0 {
			return w, h
		}
	}
	return 1100, 700 // Default size
}

// saveLauncherSize saves the launcher window size to config
func saveLauncherSize(width, height int) {
	appConfig.Set("launcher_size", pawscript.PSLList{width, height})
	saveConfig(appConfig)
}

// pslToInt converts a PSL list item to int
func pslToInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// getHomeDir returns the user's home directory
func getHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return ""
}

// getExamplesDir returns the examples directory path if it exists
func getExamplesDir() string {
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		examples := filepath.Join(exeDir, "examples")
		if info, err := os.Stat(examples); err == nil && info.IsDir() {
			return examples
		}
	}
	return ""
}

// getRecentPaths returns the list of recent paths from config (max 10)
func getRecentPaths() []string {
	if appConfig == nil {
		return nil
	}
	if paths, ok := appConfig["launcher_recent_paths"]; ok {
		if list, ok := paths.(pawscript.PSLList); ok {
			result := make([]string, 0, len(list))
			for _, p := range list {
				if s, ok := p.(string); ok && s != "" {
					result = append(result, s)
				}
			}
			return result
		}
	}
	return nil
}

// addRecentPath adds a path to the recent paths list (keeps max 10, no duplicates)
func addRecentPath(path string) {
	if appConfig == nil || path == "" {
		return
	}
	// Don't add home or examples to recent
	if path == getHomeDir() || path == getExamplesDir() {
		return
	}

	paths := getRecentPaths()

	// Remove if already exists
	newPaths := make([]string, 0, 10)
	for _, p := range paths {
		if p != path {
			newPaths = append(newPaths, p)
		}
	}

	// Add at front
	newPaths = append([]string{path}, newPaths...)

	// Keep max 10
	if len(newPaths) > 10 {
		newPaths = newPaths[:10]
	}

	// Convert to PSLList and save
	pslList := make(pawscript.PSLList, len(newPaths))
	for i, p := range newPaths {
		pslList[i] = p
	}
	appConfig.Set("launcher_recent_paths", pslList)
	saveConfig(appConfig)
}

// clearRecentPaths removes all recent paths from config
func clearRecentPaths() {
	if appConfig == nil {
		return
	}
	delete(appConfig, "launcher_recent_paths")
	saveConfig(appConfig)
}

// --- Toolbar Strip and Hamburger Menu ---

// showAboutDialog displays the About PawScript dialog
func showAboutDialog(parent gtk.IWindow) {
	// Use mainWindow as fallback if parent is nil
	if parent == nil && mainWindow != nil {
		parent = mainWindow
	}

	dialog := gtk.MessageDialogNew(
		parent,
		gtk.DIALOG_MODAL|gtk.DIALOG_DESTROY_WITH_PARENT,
		gtk.MESSAGE_INFO,
		gtk.BUTTONS_OK,
		"",
	)
	dialog.SetTitle("About PawScript")

	// Build about text
	aboutText := fmt.Sprintf(`<b>PawScript</b>
Version: %s

Copyright © 2025 Jeffrey R. Day
License: MIT`, version)

	dialog.SetMarkup(aboutText)

	// Center on parent window manually
	if parent != nil {
		if win, ok := parent.(*gtk.Window); ok {
			px, py := win.GetPosition()
			pw, ph := win.GetSize()
			dialog.Connect("realize", func() {
				dw, dh := dialog.GetSize()
				dialog.Move(px+(pw-dw)/2, py+(ph-dh)/2)
			})
		} else if appWin, ok := parent.(*gtk.ApplicationWindow); ok {
			px, py := appWin.GetPosition()
			pw, ph := appWin.GetSize()
			dialog.Connect("realize", func() {
				dw, dh := dialog.GetSize()
				dialog.Move(px+(pw-dw)/2, py+(ph-dh)/2)
			})
		}
	}

	dialog.Run()
	dialog.Destroy()
}

// showSettingsDialog displays the Settings dialog with tabbed interface
// copyPSLConfig creates a deep copy of a PSLConfig section
func copyPSLConfig(src interface{}) pawscript.PSLConfig {
	result := pawscript.PSLConfig{}
	if src == nil {
		return result
	}
	if srcConfig, ok := src.(pawscript.PSLConfig); ok {
		for k, v := range srcConfig {
			result[k] = v
		}
	}
	return result
}

func showSettingsDialog(parent gtk.IWindow) {
	// Use mainWindow as fallback if parent is nil
	if parent == nil && mainWindow != nil {
		parent = mainWindow
	}

	// Save original values for reverting on Cancel
	origWindowTheme := appConfig.GetString("theme", "auto")
	origTermTheme := appConfig.GetString("term_theme", "auto")
	origUIScale := appConfig.GetFloat("ui_scale", 1.0)
	origFontFamily := appConfig.GetString("font_family", "")
	origFontSize := appConfig.GetInt("font_size", pawgui.DefaultFontSize)
	origFontFamilyUnicode := appConfig.GetString("font_family_unicode", "")

	// Save original palette sections for reverting on Cancel
	origTermColors := copyPSLConfig(appConfig["term_colors"])
	origTermColorsDark := copyPSLConfig(appConfig["term_colors_dark"])
	origTermColorsLight := copyPSLConfig(appConfig["term_colors_light"])

	// Save original splitter position for scaling during UI scale changes
	var origSplitterPos int
	if launcherPaned != nil {
		origSplitterPos = launcherPaned.GetPosition()
	}

	// Create dialog
	dlg, _ := gtk.DialogNew()
	dlg.SetTitle("Settings")
	dlg.SetModal(true)
	dlg.SetDefaultSize(400, 300)
	if parent != nil {
		if win, ok := parent.(*gtk.Window); ok {
			dlg.SetTransientFor(win)
		} else if appWin, ok := parent.(*gtk.ApplicationWindow); ok {
			dlg.SetTransientFor(&appWin.Window)
		}
	}

	// Get content area
	contentArea, _ := dlg.GetContentArea()
	contentArea.SetMarginStart(12)
	contentArea.SetMarginEnd(12)
	contentArea.SetMarginTop(12)
	contentArea.SetMarginBottom(12)

	// Create notebook for tabs
	notebook, _ := gtk.NotebookNew()
	contentArea.PackStart(notebook, true, true, 0)

	// --- Appearance Tab ---
	appearanceBox, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 12)
	appearanceBox.SetMarginStart(12)
	appearanceBox.SetMarginEnd(12)
	appearanceBox.SetMarginTop(12)
	appearanceBox.SetMarginBottom(12)

	// Window Theme row
	windowThemeRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 12)
	windowThemeLabel, _ := gtk.LabelNew("Window Theme:")
	windowThemeLabel.SetHAlign(gtk.ALIGN_START)
	windowThemeLabel.SetWidthChars(15)
	windowThemeRow.PackStart(windowThemeLabel, false, false, 0)

	// Determine initial selection for window theme
	var windowThemeSelected int
	switch configHelper.GetTheme() {
	case pawgui.ThemeLight:
		windowThemeSelected = 1
	case pawgui.ThemeDark:
		windowThemeSelected = 2
	default:
		windowThemeSelected = 0 // Auto
	}

	// Declare both combos so they can reference each other for icon refresh
	var windowThemeCombo, consoleThemeCombo *SettingsComboMenu

	windowThemeCombo = createSettingsComboMenu([]string{"Auto", "Light", "Dark"}, windowThemeSelected, func(idx int) {
		switch idx {
		case 1:
			appConfig.Set("theme", "light")
		case 2:
			appConfig.Set("theme", "dark")
		default:
			appConfig.Set("theme", "auto")
		}
		configHelper = pawgui.NewConfigHelper(appConfig)
		applyWindowTheme()
		// Refresh icons in both combos to match new theme
		windowThemeCombo.RefreshIcons()
		if consoleThemeCombo != nil {
			consoleThemeCombo.RefreshIcons()
		}
	})
	windowThemeRow.PackStart(windowThemeCombo.Button, true, true, 0)
	appearanceBox.PackStart(windowThemeRow, false, false, 0)

	// Window Scale row
	windowScaleRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 12)
	windowScaleLabel, _ := gtk.LabelNew("Window Scale:")
	windowScaleLabel.SetHAlign(gtk.ALIGN_START)
	windowScaleLabel.SetWidthChars(15)
	windowScaleRow.PackStart(windowScaleLabel, false, false, 0)

	// Get current scale value (may be out of normal range if user edited config)
	currentScale := configHelper.GetUIScale()
	minScale := 0.5
	maxScale := 3.0
	// Extend range if current value is outside normal bounds
	if currentScale < minScale {
		minScale = currentScale
	}
	if currentScale > maxScale {
		maxScale = currentScale
	}

	windowScaleSlider, _ := gtk.ScaleNewWithRange(gtk.ORIENTATION_HORIZONTAL, minScale, maxScale, 0.1)
	windowScaleSlider.SetValue(currentScale)
	windowScaleSlider.SetDrawValue(true)
	windowScaleSlider.SetHExpand(true)
	// Update config value continuously while dragging
	windowScaleSlider.Connect("value-changed", func() {
		newScale := windowScaleSlider.GetValue()
		appConfig.Set("ui_scale", newScale)
		configHelper = pawgui.NewConfigHelper(appConfig)
	})
	// Apply visual changes only when mouse button is released
	windowScaleSlider.Connect("button-release-event", func() {
		applyUIScale()
		// Scale splitter position relative to original position and scale
		if launcherPaned != nil && origSplitterPos > 0 && origUIScale > 0 {
			newScale := getUIScale()
			scaledPos := int(float64(origSplitterPos) * newScale / origUIScale)
			if scaledPos > 0 {
				launcherPaned.SetPosition(scaledPos)
			}
		}
	})
	windowScaleRow.PackStart(windowScaleSlider, true, true, 0)
	appearanceBox.PackStart(windowScaleRow, false, false, 0)

	// Console Theme row
	consoleThemeRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 12)
	consoleThemeLabel, _ := gtk.LabelNew("Console Theme:")
	consoleThemeLabel.SetHAlign(gtk.ALIGN_START)
	consoleThemeLabel.SetWidthChars(15)
	consoleThemeRow.PackStart(consoleThemeLabel, false, false, 0)

	// Determine initial selection for console theme
	var consoleThemeSelected int
	termTheme := appConfig.GetString("term_theme", "auto")
	switch termTheme {
	case "light":
		consoleThemeSelected = 1
	case "dark":
		consoleThemeSelected = 2
	default:
		consoleThemeSelected = 0 // Auto
	}

	consoleThemeCombo = createSettingsComboMenu([]string{"Auto", "Light", "Dark"}, consoleThemeSelected, func(idx int) {
		switch idx {
		case 1:
			appConfig.Set("term_theme", "light")
		case 2:
			appConfig.Set("term_theme", "dark")
		default:
			appConfig.Set("term_theme", "auto")
		}
		configHelper = pawgui.NewConfigHelper(appConfig)
		applyConsoleTheme()
	})
	consoleThemeRow.PackStart(consoleThemeCombo.Button, true, true, 0)
	appearanceBox.PackStart(consoleThemeRow, false, false, 0)

	// Console Font row
	consoleFontRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 12)
	consoleFontLabel, _ := gtk.LabelNew("Console Font:")
	consoleFontLabel.SetHAlign(gtk.ALIGN_START)
	consoleFontLabel.SetWidthChars(15)
	consoleFontRow.PackStart(consoleFontLabel, false, false, 0)

	// Get current font family (first entry only) and size
	currentFontFamily := configHelper.GetFontFamily()
	currentFontSize := configHelper.GetFontSize()
	// Extract just the first font from the comma-separated list
	firstFont := currentFontFamily
	if idx := strings.Index(currentFontFamily, ","); idx != -1 {
		firstFont = strings.TrimSpace(currentFontFamily[:idx])
	}
	// Create font description string for FontButton (e.g., "Consolas 12")
	initialFontDesc := fmt.Sprintf("%s %d", firstFont, currentFontSize)

	consoleFontButton, _ := gtk.FontButtonNewWithFont(initialFontDesc)
	consoleFontButton.SetUseFont(true)
	consoleFontButton.SetUseSize(true)
	consoleFontButton.Connect("font-set", func() {
		fontName := consoleFontButton.GetFont()
		// Parse font name - GTK format is "Family Name Size" or "Family Name Style Size"
		// We need to extract family and size
		parts := strings.Split(fontName, " ")
		if len(parts) >= 2 {
			// Size is typically the last part
			sizeStr := parts[len(parts)-1]
			if size, err := strconv.Atoi(sizeStr); err == nil && size > 0 {
				appConfig.Set("font_size", size)
				// Family is everything before the size
				newFamily := strings.Join(parts[:len(parts)-1], " ")
				// Preserve fallback fonts from original font_family
				origFamily := appConfig.GetString("font_family", "")
				if idx := strings.Index(origFamily, ","); idx != -1 {
					// Keep the fallback fonts
					newFamily = newFamily + origFamily[idx:]
				}
				appConfig.Set("font_family", newFamily)
				configHelper = pawgui.NewConfigHelper(appConfig)
				applyFontSettings()
			}
		}
	})
	consoleFontRow.PackStart(consoleFontButton, true, true, 0)
	appearanceBox.PackStart(consoleFontRow, false, false, 0)

	// CJK Font row
	cjkFontRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 12)
	cjkFontLabel, _ := gtk.LabelNew("CJK Font:")
	cjkFontLabel.SetHAlign(gtk.ALIGN_START)
	cjkFontLabel.SetWidthChars(15)
	cjkFontRow.PackStart(cjkFontLabel, false, false, 0)

	// Get current CJK font family (first entry only)
	currentCJKFamily := appConfig.GetString("font_family_unicode", "")
	if currentCJKFamily == "" {
		currentCJKFamily = pawgui.GetDefaultUnicodeFont()
	}
	firstCJKFont := currentCJKFamily
	if idx := strings.Index(currentCJKFamily, ","); idx != -1 {
		firstCJKFont = strings.TrimSpace(currentCJKFamily[:idx])
	}
	// Use a reasonable default size for display (actual size is ignored)
	cjkFontDesc := fmt.Sprintf("%s %d", firstCJKFont, currentFontSize)

	cjkFontButton, _ := gtk.FontButtonNewWithFont(cjkFontDesc)
	cjkFontButton.SetUseFont(true)
	cjkFontButton.SetUseSize(false) // Don't show size since we ignore it
	cjkFontButton.Connect("font-set", func() {
		fontName := cjkFontButton.GetFont()
		// Parse font name - extract just the family, ignore size
		parts := strings.Split(fontName, " ")
		if len(parts) >= 2 {
			// Size is typically the last part, family is everything before
			newFamily := strings.Join(parts[:len(parts)-1], " ")
			// Preserve fallback fonts from original font_family_unicode
			origFamily := appConfig.GetString("font_family_unicode", "")
			if idx := strings.Index(origFamily, ","); idx != -1 {
				// Keep the fallback fonts
				newFamily = newFamily + origFamily[idx:]
			}
			appConfig.Set("font_family_unicode", newFamily)
			configHelper = pawgui.NewConfigHelper(appConfig)
			applyFontSettings()
		}
	})
	cjkFontRow.PackStart(cjkFontButton, true, true, 0)
	appearanceBox.PackStart(cjkFontRow, false, false, 0)

	// Add appearance tab to notebook
	appearanceLabel, _ := gtk.LabelNew("Appearance")
	notebook.AppendPage(appearanceBox, appearanceLabel)

	// --- Palette Tab ---
	paletteBox, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 8)
	paletteBox.SetMarginStart(12)
	paletteBox.SetMarginEnd(12)
	paletteBox.SetMarginTop(12)
	paletteBox.SetMarginBottom(12)

	// Track palette rows
	var paletteRows []*PaletteColorRow

	// Helper to get a color from a config section
	getColorFromSection := func(sectionName, colorName string) string {
		if section, ok := appConfig[sectionName]; ok {
			if hex := pawgui.GetConfigSectionString(section, colorName); hex != "" {
				return hex
			}
		}
		return ""
	}

	// Helper to set a color in a config section
	setColorInSection := func(sectionName, colorName, hex string) {
		var section pawscript.PSLConfig
		if existing, ok := appConfig[sectionName]; ok {
			// Convert existing section to PSLConfig, preserving values
			switch v := existing.(type) {
			case pawscript.PSLConfig:
				section = v
			case pawscript.StoredList:
				// Copy named args from StoredList to new PSLConfig
				section = pawscript.PSLConfig{}
				if args := v.NamedArgs(); args != nil {
					for k, val := range args {
						section.Set(k, val)
					}
				}
			case map[string]interface{}:
				section = pawscript.PSLConfig{}
				for k, val := range v {
					section.Set(k, val)
				}
			default:
				section = pawscript.PSLConfig{}
			}
		} else {
			section = pawscript.PSLConfig{}
		}
		if hex == "" {
			delete(section, colorName)
		} else {
			section.Set(colorName, hex)
		}
		appConfig.Set(sectionName, section)
	}

	// Helper to apply palette changes to terminal
	applyPaletteChanges := func() {
		configHelper = pawgui.NewConfigHelper(appConfig)
		applyConsoleTheme()
	}

	// UI scale for all sizes
	uiScale := getUIScale()
	columnSpacing := int(24 * uiScale)
	rowVerticalSpacing := int(4 * uiScale)
	swatchSize := int(24 * uiScale)
	labelWidth := int(115 * uiScale)
	checkboxWidth := int(16 * uiScale)     // Checkbox spacer for bg/fg rows
	darkCheckboxWidth := int(14 * uiScale) // Slightly smaller for dark checkbox spacer alignment
	rowSpacing := int(6 * uiScale)

	// Create two-column layout
	columnsBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, columnSpacing)
	paletteBox.PackStart(columnsBox, true, true, 0)

	// Left column: Background + Dark colors (00-07)
	leftColumn, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, rowVerticalSpacing)
	columnsBox.PackStart(leftColumn, true, true, 0)

	// Right column: Foreground + Bright colors (08-15)
	rightColumn, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, rowVerticalSpacing)
	columnsBox.PackStart(rightColumn, true, true, 0)

	// --- Background row (always present, no checkbox) ---
	// Layout: Label | Space | Space | LightSwatch | Space | DarkSwatch
	bgRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, rowSpacing)

	bgLabel, _ := gtk.LabelNew("Background")
	bgLabel.SetXAlign(0)
	bgLabel.SetSizeRequest(labelWidth, -1)
	bgRow.PackStart(bgLabel, false, false, 0)

	// Spacer for basic swatch
	bgBasicSpacer, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	bgBasicSpacer.SetSizeRequest(swatchSize, swatchSize)
	bgRow.PackStart(bgBasicSpacer, false, false, 0)

	// Spacer for light checkbox
	bgLightCheckSpacer, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	bgLightCheckSpacer.SetSizeRequest(checkboxWidth, swatchSize)
	bgRow.PackStart(bgLightCheckSpacer, false, false, 0)

	// Light background swatch (onChange set later)
	bgLightHex := getColorFromSection("term_colors_light", "0_background")
	if bgLightHex == "" {
		bgLightHex = "#FFFFFF"
	}
	bgLightSwatch := createColorSwatch(bgLightHex, swatchSize, nil)
	bgRow.PackStart(bgLightSwatch.Button, false, false, 0)

	// Spacer for dark checkbox
	bgDarkCheckSpacer, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	bgDarkCheckSpacer.SetSizeRequest(darkCheckboxWidth, swatchSize)
	bgRow.PackStart(bgDarkCheckSpacer, false, false, 0)

	// Dark background swatch (onChange set later)
	bgDarkHex := getColorFromSection("term_colors_dark", "0_background")
	if bgDarkHex == "" {
		bgDarkHex = "#1E1E1E"
	}
	bgDarkSwatch := createColorSwatch(bgDarkHex, swatchSize, nil)
	bgRow.PackStart(bgDarkSwatch.Button, false, false, 0)

	leftColumn.PackStart(bgRow, false, false, 0)

	// --- Foreground row (always present, no checkbox) ---
	// Layout: Label | Space | Space | LightSwatch | Space | DarkSwatch
	fgRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, rowSpacing)

	fgLabel, _ := gtk.LabelNew("Foreground")
	fgLabel.SetXAlign(0)
	fgLabel.SetSizeRequest(labelWidth, -1)
	fgRow.PackStart(fgLabel, false, false, 0)

	// Spacer for basic swatch
	fgBasicSpacer, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	fgBasicSpacer.SetSizeRequest(swatchSize, swatchSize)
	fgRow.PackStart(fgBasicSpacer, false, false, 0)

	// Spacer for light checkbox
	fgLightCheckSpacer, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	fgLightCheckSpacer.SetSizeRequest(checkboxWidth, swatchSize)
	fgRow.PackStart(fgLightCheckSpacer, false, false, 0)

	// Light foreground swatch (onChange set later)
	fgLightHex := getColorFromSection("term_colors_light", "9_foreground")
	if fgLightHex == "" {
		fgLightHex = "#1E1E1E"
	}
	fgLightSwatch := createColorSwatch(fgLightHex, swatchSize, nil)
	fgRow.PackStart(fgLightSwatch.Button, false, false, 0)

	// Spacer for dark checkbox
	fgDarkCheckSpacer, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	fgDarkCheckSpacer.SetSizeRequest(darkCheckboxWidth, swatchSize)
	fgRow.PackStart(fgDarkCheckSpacer, false, false, 0)

	// Dark foreground swatch (onChange set later)
	fgDarkHex := getColorFromSection("term_colors_dark", "9_foreground")
	if fgDarkHex == "" {
		fgDarkHex = "#D4D4D4"
	}
	fgDarkSwatch := createColorSwatch(fgDarkHex, swatchSize, nil)
	fgRow.PackStart(fgDarkSwatch.Button, false, false, 0)

	rightColumn.PackStart(fgRow, false, false, 0)

	// Set up onChange callbacks that cross-update text colors
	bgLightSwatch.onChange = func(hex string) {
		setColorInSection("term_colors_light", "0_background", hex)
		fgLightSwatch.SetText("Lt", hex) // Update fg text color to new bg
		applyPaletteChanges()
	}
	bgDarkSwatch.onChange = func(hex string) {
		setColorInSection("term_colors_dark", "0_background", hex)
		fgDarkSwatch.SetText("Dk", hex) // Update fg text color to new bg
		applyPaletteChanges()
	}
	fgLightSwatch.onChange = func(hex string) {
		setColorInSection("term_colors_light", "9_foreground", hex)
		bgLightSwatch.SetText("Lt", hex) // Update bg text color to new fg
		applyPaletteChanges()
	}
	fgDarkSwatch.onChange = func(hex string) {
		setColorInSection("term_colors_dark", "9_foreground", hex)
		bgDarkSwatch.SetText("Dk", hex) // Update bg text color to new fg
		applyPaletteChanges()
	}

	// Add text labels to background swatches only (fg swatches stay plain)
	bgLightSwatch.SetText("Lt", fgLightHex)
	bgDarkSwatch.SetText("Dk", fgDarkHex)

	// Color names for display (VGA order)
	colorDisplayNames := []string{
		"00 Black", "01 Blue", "02 Green", "03 Cyan",
		"04 Red", "05 Purple", "06 Brown", "07 Silver",
		"08 Gray", "09 Bright Blue", "10 Bright Green", "11 Bright Cyan",
		"12 Bright Red", "13 Pink", "14 Yellow", "15 White",
	}
	colorConfigNames := purfecterm.PaletteColorNames()
	defaultPaletteHex := purfecterm.DefaultPaletteHex()

	// Create palette color rows (00-07 on left, 08-15 on right)
	// Layout: Label | Basic | ☐Light | LightSwatch | ☐Dark | DarkSwatch
	for i := 0; i < 16; i++ {
		colorRow := &PaletteColorRow{
			ColorName:  colorConfigNames[i],
			ColorIndex: i,
		}

		row, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, rowSpacing)

		// Label
		label, _ := gtk.LabelNew(colorDisplayNames[i])
		label.SetXAlign(0)
		label.SetSizeRequest(labelWidth, -1)
		row.PackStart(label, false, false, 0)

		// Basic swatch (from term_colors) - onChange set after light/dark swatches created
		basicHex := getColorFromSection("term_colors", colorConfigNames[i])
		if basicHex == "" {
			basicHex = defaultPaletteHex[i]
		}
		colorRow.BasicSwatch = createColorSwatch(basicHex, swatchSize, nil)
		row.PackStart(colorRow.BasicSwatch.Button, false, false, 0)

		// Light checkbox
		colorRow.LightCheckbox, _ = gtk.CheckButtonNew()
		lightHex := getColorFromSection("term_colors_light", colorConfigNames[i])
		colorRow.LightCheckbox.SetActive(lightHex != "")
		row.PackStart(colorRow.LightCheckbox, false, false, 0)

		// Light swatch
		if lightHex == "" {
			lightHex = basicHex
		}
		colorRow.LightSwatch = createColorSwatch(lightHex, swatchSize, func(hex string) {
			setColorInSection("term_colors_light", colorRow.ColorName, hex)
			applyPaletteChanges()
		})
		colorRow.LightSwatch.SetInheritedColor(basicHex) // Show basic color when disabled
		colorRow.LightSwatch.SetEnabled(colorRow.LightCheckbox.GetActive())
		row.PackStart(colorRow.LightSwatch.Button, false, false, 0)

		// Dark checkbox
		colorRow.DarkCheckbox, _ = gtk.CheckButtonNew()
		darkHex := getColorFromSection("term_colors_dark", colorConfigNames[i])
		colorRow.DarkCheckbox.SetActive(darkHex != "")
		row.PackStart(colorRow.DarkCheckbox, false, false, 0)

		// Dark swatch
		if darkHex == "" {
			darkHex = basicHex
		}
		colorRow.DarkSwatch = createColorSwatch(darkHex, swatchSize, func(hex string) {
			setColorInSection("term_colors_dark", colorRow.ColorName, hex)
			applyPaletteChanges()
		})
		colorRow.DarkSwatch.SetInheritedColor(basicHex) // Show basic color when disabled
		colorRow.DarkSwatch.SetEnabled(colorRow.DarkCheckbox.GetActive())
		row.PackStart(colorRow.DarkSwatch.Button, false, false, 0)

		// Wire up light checkbox
		localRow := colorRow
		colorRow.LightCheckbox.Connect("toggled", func() {
			enabled := localRow.LightCheckbox.GetActive()
			localRow.LightSwatch.SetEnabled(enabled)
			if enabled {
				setColorInSection("term_colors_light", localRow.ColorName, localRow.LightSwatch.GetColor())
			} else {
				setColorInSection("term_colors_light", localRow.ColorName, "")
			}
			applyPaletteChanges()
		})

		// Wire up dark checkbox
		colorRow.DarkCheckbox.Connect("toggled", func() {
			enabled := localRow.DarkCheckbox.GetActive()
			localRow.DarkSwatch.SetEnabled(enabled)
			if enabled {
				setColorInSection("term_colors_dark", localRow.ColorName, localRow.DarkSwatch.GetColor())
			} else {
				setColorInSection("term_colors_dark", localRow.ColorName, "")
			}
			applyPaletteChanges()
		})

		// Wire up Basic swatch onChange (updates inherited color on light/dark swatches)
		colorRow.BasicSwatch.onChange = func(hex string) {
			setColorInSection("term_colors", localRow.ColorName, hex)
			// Update inherited color on light/dark swatches
			localRow.LightSwatch.SetInheritedColor(hex)
			localRow.DarkSwatch.SetInheritedColor(hex)
			applyPaletteChanges()
		}

		// Add to appropriate column
		if i < 8 {
			leftColumn.PackStart(row, false, false, 0)
		} else {
			rightColumn.PackStart(row, false, false, 0)
		}

		paletteRows = append(paletteRows, colorRow)
	}

	// Suppress unused variable warnings
	_ = paletteRows
	_ = bgLightSwatch
	_ = bgDarkSwatch
	_ = fgLightSwatch
	_ = fgDarkSwatch

	// Add palette tab to notebook
	paletteLabel, _ := gtk.LabelNew("Palette")
	notebook.AppendPage(paletteBox, paletteLabel)

	// --- Button Box ---
	buttonBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 8)
	buttonBox.SetHAlign(gtk.ALIGN_END)
	buttonBox.SetMarginTop(12)

	cancelBtn, _ := gtk.ButtonNewWithLabel("Cancel")
	cancelBtn.Connect("clicked", func() {
		dlg.Response(gtk.RESPONSE_CANCEL)
	})
	buttonBox.PackStart(cancelBtn, false, false, 0)

	saveBtn, _ := gtk.ButtonNewWithLabel("Save")
	saveBtn.Connect("clicked", func() {
		dlg.Response(gtk.RESPONSE_OK)
	})
	buttonBox.PackStart(saveBtn, false, false, 0)

	contentArea.PackStart(buttonBox, false, false, 0)

	// Center on parent window
	if parent != nil {
		if win, ok := parent.(*gtk.Window); ok {
			px, py := win.GetPosition()
			pw, ph := win.GetSize()
			dlg.Connect("realize", func() {
				dw, dh := dlg.GetSize()
				dlg.Move(px+(pw-dw)/2, py+(ph-dh)/2)
			})
		} else if appWin, ok := parent.(*gtk.ApplicationWindow); ok {
			px, py := appWin.GetPosition()
			pw, ph := appWin.GetSize()
			dlg.Connect("realize", func() {
				dw, dh := dlg.GetSize()
				dlg.Move(px+(pw-dw)/2, py+(ph-dh)/2)
			})
		}
	}

	dlg.ShowAll()

	// Run dialog and handle response
	response := dlg.Run()
	if response == gtk.RESPONSE_OK {
		// Save config to file (settings already applied via change handlers)
		saveConfig(appConfig)
	} else {
		// Revert to original values on Cancel
		currentUIScale := getUIScale() // Get current scale before reverting
		appConfig.Set("theme", origWindowTheme)
		appConfig.Set("term_theme", origTermTheme)
		appConfig.Set("ui_scale", origUIScale)
		if origFontFamily != "" {
			appConfig.Set("font_family", origFontFamily)
		}
		appConfig.Set("font_size", origFontSize)
		if origFontFamilyUnicode != "" {
			appConfig.Set("font_family_unicode", origFontFamilyUnicode)
		}
		// Revert palette sections
		if len(origTermColors) > 0 {
			appConfig.Set("term_colors", origTermColors)
		} else {
			delete(appConfig, "term_colors")
		}
		if len(origTermColorsDark) > 0 {
			appConfig.Set("term_colors_dark", origTermColorsDark)
		} else {
			delete(appConfig, "term_colors_dark")
		}
		if len(origTermColorsLight) > 0 {
			appConfig.Set("term_colors_light", origTermColorsLight)
		} else {
			delete(appConfig, "term_colors_light")
		}
		configHelper = pawgui.NewConfigHelper(appConfig)
		applyWindowTheme()
		applyConsoleTheme()
		// Only apply UI scale if it actually changed (avoids unnecessary file list rebuild)
		if currentUIScale != origUIScale {
			applyUIScale()
		}
		applyFontSettings()
		// Revert splitter position
		if launcherPaned != nil && origSplitterPos > 0 {
			launcherPaned.SetPosition(origSplitterPos)
		}
	}
	dlg.Destroy()
}

// applyWindowTheme applies the window theme setting
func applyWindowTheme() {
	applyTheme(configHelper.GetTheme())
	updateToolbarIcons()
	// Refresh path menu to update icon colors
	updatePathMenu()
}

// applyFontSettings applies font settings to all open terminals
func applyFontSettings() {
	fontFamily := configHelper.GetFontFamily()
	fontSize := configHelper.GetFontSize()
	unicodeFont := getFontFamilyUnicode()
	cjkFont := getFontFamilyCJK()

	// Update main launcher terminal
	if terminal != nil {
		terminal.SetFont(fontFamily, fontSize)
		terminal.SetFontFallbacks(unicodeFont, cjkFont)
	}

	// Update all script window terminals
	toolbarDataMu.Lock()
	for _, data := range toolbarDataByWindow {
		if data.terminal != nil {
			data.terminal.SetFont(fontFamily, fontSize)
			data.terminal.SetFontFallbacks(unicodeFont, cjkFont)
		}
	}
	for _, data := range toolbarDataByPS {
		if data.terminal != nil {
			data.terminal.SetFont(fontFamily, fontSize)
			data.terminal.SetFontFallbacks(unicodeFont, cjkFont)
		}
	}
	toolbarDataMu.Unlock()

	// Force GC to clean up any orphaned Pango/GTK objects from font changes
	runtime.GC()
}

// applyUIScale applies UI scale to all windows (requires restart for full effect)
func applyUIScale() {
	// Guard against re-entrant/concurrent calls
	uiScaleMu.Lock()
	if uiScaleInProgress {
		uiScaleMu.Unlock()
		return
	}
	uiScaleInProgress = true
	uiScaleMu.Unlock()

	// Ensure we clear the flag when done
	defer func() {
		uiScaleMu.Lock()
		uiScaleInProgress = false
		uiScaleMu.Unlock()
	}()

	// Re-apply the CSS with new scale
	applyMainCSS()

	// Rebuild file list with new scaled icons
	refreshFileList()

	// Refresh all toolbar icons with new scale
	updateToolbarIcons()

	// Rebuild menus with new scaled icon sizes
	rebuildMenus()

	// Update scrollbars on all terminal instances
	updateAllTerminalScrollbars()

	// Final GC pass to clean up any remaining orphaned wrappers
	// This ensures finalizers run now while we're in a safe state,
	// not later during unrelated GTK operations like splitter dragging
	runtime.GC()
}

// updateAllTerminalScrollbars updates scrollbars on all terminal instances
func updateAllTerminalScrollbars() {
	// Update main launcher terminal
	if terminal != nil {
		terminal.UpdateScrollbars()
	}

	// Update all script window terminals
	toolbarDataMu.Lock()
	for _, data := range toolbarDataByWindow {
		if data.terminal != nil {
			data.terminal.UpdateScrollbars()
		}
	}
	for _, data := range toolbarDataByPS {
		if data.terminal != nil {
			data.terminal.UpdateScrollbars()
		}
	}
	toolbarDataMu.Unlock()
}

// createLauncherContextMenu creates the right-click context menu for the launcher terminal
func createLauncherContextMenu() *gtk.Menu {
	menu, _ := gtk.MenuNew()

	copyItem := createMenuItemWithGutter("Copy", func() {
		if terminal != nil {
			terminal.CopySelection()
		}
	})
	menu.Append(copyItem)

	pasteItem := createMenuItemWithGutter("Paste", func() {
		if terminal != nil {
			terminal.PasteClipboard()
		}
	})
	menu.Append(pasteItem)

	selectAllItem := createMenuItemWithGutter("Select All", func() {
		if terminal != nil {
			terminal.SelectAll()
		}
	})
	menu.Append(selectAllItem)

	menu.ShowAll()
	return menu
}

// rebuildMenus recreates all menus with current UI scale
func rebuildMenus() {
	// Destroy old menus before creating new ones to prevent GC finalizer issues
	// The old menu wrappers have finalizers that can crash if they run during
	// unrelated GTK operations. Explicitly destroying them ensures GTK releases
	// them properly before we lose the Go reference.

	// Rebuild launcher hamburger menu
	if launcherMenuCtx != nil {
		oldMenu := launcherMenu
		launcherMenu = createHamburgerMenu(launcherMenuCtx)
		if oldMenu != nil {
			oldMenu.Destroy()
		}
	}

	// Rebuild path menu
	if pathButton != nil {
		oldPathMenu := pathMenu
		pathMenu, _ = gtk.MenuNew()
		pathButton.SetPopup(pathMenu)
		updatePathMenu()
		if oldPathMenu != nil {
			oldPathMenu.Destroy()
		}
	}

	// Rebuild launcher context menu
	oldContextMenu := contextMenu
	contextMenu = createLauncherContextMenu()
	if oldContextMenu != nil {
		oldContextMenu.Destroy()
	}

	// Force GC to clean up the destroyed menu wrappers now while it's safe,
	// rather than letting finalizers run during other GTK operations
	runtime.GC()
}

// updateToolbarIcons regenerates all toolbar icons with the current theme's colors
func updateToolbarIcons() {
	// Update both launcher hamburger buttons (path selector and narrow strip)
	if launcherMenuButton != nil {
		svgData := getSVGIcon(hamburgerIconSVG)
		if img := createImageFromSVG(svgData, scaledToolbarIconSize()); img != nil {
			launcherMenuButton.SetImage(img)
		}
	}
	if launcherStripMenuBtn != nil {
		svgData := getSVGIcon(hamburgerIconSVG)
		if img := createImageFromSVG(svgData, scaledToolbarIconSize()); img != nil {
			launcherStripMenuBtn.SetImage(img)
		}
	}

	// Update all registered buttons in launcher toolbar
	for _, btn := range launcherRegisteredBtns {
		if btn.widget != nil {
			svgData := getSVGIcon(starIconSVG)
			if img := createImageFromSVG(svgData, scaledToolbarIconSize()); img != nil {
				btn.widget.SetImage(img)
			}
		}
	}

	// Update buttons in all script windows (keyed by PawScript instance)
	toolbarDataMu.Lock()
	defer toolbarDataMu.Unlock()
	for _, data := range toolbarDataByPS {
		// Update the hamburger button
		if data.menuButton != nil {
			svgData := getSVGIcon(hamburgerIconSVG)
			if img := createImageFromSVG(svgData, scaledToolbarIconSize()); img != nil {
				data.menuButton.SetImage(img)
			}
		}
		// Update registered buttons
		for _, btn := range data.registeredBtns {
			if btn.widget != nil {
				svgData := getSVGIcon(starIconSVG)
				if img := createImageFromSVG(svgData, scaledToolbarIconSize()); img != nil {
					btn.widget.SetImage(img)
				}
			}
		}
	}

	// Update buttons in all windows (keyed by window pointer)
	for _, data := range toolbarDataByWindow {
		// Update the hamburger button
		if data.menuButton != nil {
			svgData := getSVGIcon(hamburgerIconSVG)
			if img := createImageFromSVG(svgData, scaledToolbarIconSize()); img != nil {
				data.menuButton.SetImage(img)
			}
		}
		// Update registered buttons
		for _, btn := range data.registeredBtns {
			if btn.widget != nil {
				svgData := getSVGIcon(starIconSVG)
				if img := createImageFromSVG(svgData, scaledToolbarIconSize()); img != nil {
					btn.widget.SetImage(img)
				}
			}
		}
	}

	// Refresh path menu icons (Home, Examples, etc.)
	updatePathMenu()

	// Refresh file list icons
	refreshFileListIcons()
}

// findRowImage finds the image widget in a file list row's box
// Returns nil if not found
func findRowImage(row *gtk.ListBoxRow) *gtk.Image {
	if row == nil {
		return nil
	}
	child, err := row.GetChild()
	if err != nil || child == nil {
		return nil
	}
	box, ok := child.(*gtk.Box)
	if !ok {
		return nil
	}

	// Find image by iterating through children with Foreach
	var foundImage *gtk.Image
	children := box.GetChildren()
	if children != nil {
		children.Foreach(func(item interface{}) {
			if foundImage != nil {
				return // Already found
			}
			if img, ok := item.(*gtk.Image); ok {
				foundImage = img
			}
		})
	}
	return foundImage
}

// refreshFileListIcons updates all file list icons to match current theme
func refreshFileListIcons() {
	if fileList == nil {
		return
	}

	selectedRow := fileList.GetSelectedRow()

	// Iterate over our tracked rows directly
	for row, iconType := range rowIconTypeMap {
		if row == nil {
			continue
		}

		// Find the image in this row
		img := findRowImage(row)
		if img == nil {
			continue
		}

		// Determine if this row is selected
		isSelected := selectedRow != nil && row == selectedRow

		// Get the appropriate SVG data
		var svgTemplate string
		switch iconType {
		case gtkIconTypeFolderUp:
			svgTemplate = folderUpIconSVG
		case gtkIconTypeFolder:
			svgTemplate = folderIconSVG
		case gtkIconTypePawFile:
			svgTemplate = pawFileIconSVG
		default:
			continue
		}

		var svgData string
		if isSelected {
			svgData = getDarkSVGIcon(svgTemplate)
		} else {
			svgData = getSVGIcon(svgTemplate)
		}

		// Update the existing image with new pixbuf
		if pixbuf := createPixbufFromSVG(svgData, scaledFileListIconSize()); pixbuf != nil {
			img.SetFromPixbuf(pixbuf)
		}
	}
}

// applyConsoleTheme applies the console theme to all terminals.
// The ColorScheme contains both dark and light palettes, so the terminal
// can switch between them via DECSCNM (\e[?5h/\e[?5l) without needing
// to reload colors. The preferred theme (from config) determines the
// default mode used after terminal reset.
func applyConsoleTheme() {
	isDark := isTermThemeDark()
	scheme := getDualColorScheme()

	// Apply to launcher terminal
	if terminal != nil {
		// Update the preferred theme (for reset) and set current theme to match
		terminal.Buffer().UpdatePreferredDarkTheme(isDark)
		terminal.Buffer().SetDarkTheme(isDark)
		terminal.SetColorScheme(scheme)
	}

	// Apply to all script window terminals
	toolbarDataMu.Lock()
	for _, data := range toolbarDataByWindow {
		if data.terminal != nil {
			data.terminal.Buffer().UpdatePreferredDarkTheme(isDark)
			data.terminal.Buffer().SetDarkTheme(isDark)
			data.terminal.SetColorScheme(scheme)
		}
	}
	for _, data := range toolbarDataByPS {
		if data.terminal != nil {
			data.terminal.Buffer().UpdatePreferredDarkTheme(isDark)
			data.terminal.Buffer().SetDarkTheme(isDark)
			data.terminal.SetColorScheme(scheme)
		}
	}
	toolbarDataMu.Unlock()
}

// MenuContext holds callbacks and state for hamburger menu items
type MenuContext struct {
	Parent           gtk.IWindow
	IsScriptWindow   bool
	Terminal         *purfectermgtk.Terminal
	IsScriptRunning  func() bool
	StopScript       func()
	IsFileListWide   func() bool   // Launcher only: returns true if wide panel visible
	ToggleFileList   func()        // Launcher only: toggles wide/narrow mode
	CloseWindow      func()        // Closes this window
	FileListMenuItem *gtk.MenuItem // Reference to File List toggle item
}

// createHamburgerMenu creates the hamburger dropdown menu
// ctx provides callbacks for menu actions
func createHamburgerMenu(ctx *MenuContext) *gtk.Menu {
	menu, _ := gtk.MenuNew()

	// About option (both)
	aboutItem := createMenuItemWithGutter("About PawScript...", func() {
		showAboutDialog(ctx.Parent)
	})
	menu.Append(aboutItem)

	// Settings option (both)
	settingsItem := createMenuItemWithGutter("Settings...", func() {
		showSettingsDialog(ctx.Parent)
	})
	menu.Append(settingsItem)

	// Separator after About/Settings
	sepAbout, _ := gtk.SeparatorMenuItemNew()
	menu.Append(sepAbout)

	// File List toggle (launcher only) - uses custom SVG icons for checked/unchecked state
	var localFileListItem *gtk.MenuItem
	if !ctx.IsScriptWindow && ctx.ToggleFileList != nil {
		// Determine current state and choose appropriate icon
		isWide := false
		if ctx.IsFileListWide != nil {
			isWide = ctx.IsFileListWide()
		}
		var iconSVG string
		if isWide {
			iconSVG = checkedIconSVG
		} else {
			iconSVG = uncheckedIconSVG
		}
		fileListItem := createMenuItemWithIcon(iconSVG, "File List", func() {
			if ctx.ToggleFileList != nil {
				ctx.ToggleFileList()
			}
		})
		localFileListItem = fileListItem
		menu.Append(fileListItem)
	}

	// Show Launcher (console windows only)
	if ctx.IsScriptWindow {
		showLauncherItem := createMenuItemWithGutter("Show Launcher", func() {
			showOrCreateLauncher()
		})
		menu.Append(showLauncherItem)
	}

	// New Window (both - creates a blank console window)
	newWindowItem := createMenuItemWithGutter("New Window", func() {
		createBlankConsoleWindow()
	})
	menu.Append(newWindowItem)

	// Separator
	sep1, _ := gtk.SeparatorMenuItemNew()
	menu.Append(sep1)

	// Stop Script (both) - disabled when no script running
	stopScriptItem := createMenuItemWithGutter("Stop Script", func() {
		if ctx.StopScript != nil {
			ctx.StopScript()
		}
	})
	stopScriptItem.SetSensitive(false) // Initially disabled
	menu.Append(stopScriptItem)

	// Reset Terminal (both) - directly under Stop Script
	resetTerminalItem := createMenuItemWithGutter("Reset Terminal", func() {
		if ctx.Terminal != nil {
			ctx.Terminal.Reset()
		}
	})
	menu.Append(resetTerminalItem)

	// Update sensitivity when menu is shown
	menu.Connect("show", func() {
		if ctx.IsScriptRunning != nil {
			stopScriptItem.SetSensitive(ctx.IsScriptRunning())
		}
		// Update file list toggle icon based on current state
		if localFileListItem != nil && ctx.IsFileListWide != nil {
			updateFileListMenuIcon(localFileListItem, ctx.IsFileListWide())
		}
	})

	// Separator
	sep2, _ := gtk.SeparatorMenuItemNew()
	menu.Append(sep2)

	// Save Scrollback ANSI (both)
	saveScrollbackANSIItem := createMenuItemWithGutter("Save Scrollback ANSI...", func() {
		if ctx.Parent != nil && ctx.Terminal != nil {
			saveScrollbackANSIDialog(ctx.Parent, ctx.Terminal)
		}
	})
	menu.Append(saveScrollbackANSIItem)

	// Save Scrollback Text (both)
	saveScrollbackTextItem := createMenuItemWithGutter("Save Scrollback Text...", func() {
		if ctx.Parent != nil && ctx.Terminal != nil {
			saveScrollbackTextDialog(ctx.Parent, ctx.Terminal)
		}
	})
	menu.Append(saveScrollbackTextItem)

	// Restore Buffer (both)
	restoreBufferItem := createMenuItemWithGutter("Restore Buffer...", func() {
		if ctx.Parent != nil && ctx.Terminal != nil {
			restoreBufferDialog(ctx.Parent, ctx.Terminal)
		}
	})
	menu.Append(restoreBufferItem)

	// Clear Scrollback (both)
	clearScrollbackItem := createMenuItemWithGutter("Clear Scrollback", func() {
		if ctx.Terminal != nil {
			ctx.Terminal.ClearScrollback()
		}
	})
	menu.Append(clearScrollbackItem)

	// Separator
	sep3, _ := gtk.SeparatorMenuItemNew()
	menu.Append(sep3)

	// Close (both) - show shortcut if configured
	closeItem := createMenuItemWithShortcut("Close", getCloseShortcut(), func() {
		if ctx.CloseWindow != nil {
			ctx.CloseWindow()
		}
	})
	menu.Append(closeItem)

	// Quit PawScript (both) - show shortcut if configured
	quitItem := createMenuItemWithShortcut("Quit PawScript", getQuitShortcut(), func() {
		quitApplication(ctx.Parent)
	})
	menu.Append(quitItem)

	menu.ShowAll()
	return menu
}

// showOrCreateLauncher brings the launcher window to front, or creates one if hidden/closed
func showOrCreateLauncher() {
	if mainWindow != nil {
		mainWindow.Present()
	} else if app != nil {
		// Launcher was closed, create a new one
		glib.IdleAdd(func() {
			activate(app)
		})
	}
}

// quitApplication prompts for confirmation if scripts are running, then exits
func quitApplication(parent gtk.IWindow) {
	// Count windows with running scripts
	runningScripts := 0
	if app != nil {
		windows := app.GetWindows()
		for l := windows; l != nil; l = l.Next() {
			// Check each window - we track script state per window
			runningScripts++ // Simplified: count all windows for now
		}
	}

	// Check if main launcher has running script
	scriptMu.Lock()
	launcherRunning := scriptRunning
	scriptMu.Unlock()

	// Count script windows with running scripts
	toolbarDataMu.Lock()
	scriptWindowCount := len(toolbarDataByPS)
	toolbarDataMu.Unlock()

	hasRunningScripts := launcherRunning || scriptWindowCount > 0

	if hasRunningScripts {
		// Show confirmation dialog
		dialog := gtk.MessageDialogNew(
			parent,
			gtk.DIALOG_MODAL|gtk.DIALOG_DESTROY_WITH_PARENT,
			gtk.MESSAGE_QUESTION,
			gtk.BUTTONS_YES_NO,
			"This will stop all scripts. Are you sure?",
		)
		dialog.SetTitle("Quit PawScript")
		response := dialog.Run()
		dialog.Destroy()

		if response != gtk.RESPONSE_YES {
			return
		}
	}

	// Quit the application
	if app != nil {
		app.Quit()
	}
}

// saveScrollbackANSIDialog shows a file dialog to save terminal scrollback as ANSI
func saveScrollbackANSIDialog(parent gtk.IWindow, term *purfectermgtk.Terminal) {
	// Use global terminal as fallback if term is nil
	if term == nil {
		term = terminal
	}
	if term == nil {
		return
	}

	// Use sqweek/dialog for native file save dialog
	filename, err := dialog.File().
		Title("Save Scrollback ANSI").
		Filter("ANSI files", "ans").
		Filter("All files", "*").
		SetStartFile("scrollback.ans").
		Save()
	if err != nil || filename == "" {
		return
	}

	// Add header comment with version info using OSC 9999
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	header := fmt.Sprintf("\x1b]9999;PawScript %s (GTK; %s; %s) Buffer Saved %s\x07",
		version, runtime.GOOS, runtime.GOARCH, timestamp)
	content := header + term.SaveScrollbackANS()

	// Write to file
	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		dialog.Message("Failed to save file: %v", err).Title("Error").Error()
	}
}

// saveScrollbackTextDialog shows a file dialog to save terminal scrollback as plain text
func saveScrollbackTextDialog(parent gtk.IWindow, term *purfectermgtk.Terminal) {
	// Use global terminal as fallback if term is nil
	if term == nil {
		term = terminal
	}
	if term == nil {
		return
	}

	// Use sqweek/dialog for native file save dialog
	filename, err := dialog.File().
		Title("Save Scrollback Text").
		Filter("Text files", "txt").
		Filter("All files", "*").
		SetStartFile("scrollback.txt").
		Save()
	if err != nil || filename == "" {
		return
	}

	// Add header comment with version info as text comment
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	header := fmt.Sprintf("# PawScript %s (GTK; %s; %s) Buffer Saved %s\n",
		version, runtime.GOOS, runtime.GOARCH, timestamp)
	content := header + term.SaveScrollbackText()

	// Write to file
	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		dialog.Message("Failed to save file: %v", err).Title("Error").Error()
	}
}

// restoreBufferDialog shows a file dialog to load and display terminal content
func restoreBufferDialog(parent gtk.IWindow, term *purfectermgtk.Terminal) {
	// Use global terminal as fallback if term is nil
	if term == nil {
		term = terminal
	}
	if term == nil {
		return
	}

	// Use sqweek/dialog for native file open dialog
	filename, err := dialog.File().
		Title("Restore Buffer").
		Filter("ANSI files", "ans").
		Filter("Text files", "txt").
		Filter("All files", "*").
		Load()
	if err != nil || filename == "" {
		return
	}

	// Read file content
	content, err := os.ReadFile(filename)
	if err != nil {
		dialog.Message("Failed to read file: %v", err).Title("Error").Error()
		return
	}

	// Convert LF to CR+LF for proper terminal display
	// (LF alone moves down without returning to column 0)
	contentStr := strings.ReplaceAll(string(content), "\r\n", "\n") // Normalize first
	contentStr = strings.ReplaceAll(contentStr, "\n", "\r\n")       // Then convert to CR+LF

	// Feed content to terminal
	term.Feed(contentStr)
}

// createBlankConsoleWindow creates a new blank terminal window with REPL
// This creates the same environment as the Run button, but without running a script
func createBlankConsoleWindow() {
	if app == nil {
		return
	}

	// Create new window
	win, err := gtk.ApplicationWindowNew(app)
	if err != nil {
		return
	}
	win.SetTitle("PawScript - Console")
	win.SetDefaultSize(900, 600)

	// Set up quit shortcut for this window
	setupQuitShortcutForWindow(win)

	// Create terminal for this window
	winTerminal, err := purfectermgtk.New(purfectermgtk.Options{
		Cols:           100,
		Rows:           30,
		ScrollbackSize: 10000,
		FontFamily:     getFontFamily(),
		FontSize:       getFontSize(),
		Scheme:         getDualColorScheme(),
	})
	if err != nil {
		win.Destroy()
		return
	}

	// Set font fallbacks for Unicode/CJK characters
	winTerminal.SetFontFallbacks(getFontFamilyUnicode(), getFontFamilyCJK())

	// Set up terminal theme from config
	prefersDark := isTermThemeDark()
	winTerminal.Buffer().SetPreferredDarkTheme(prefersDark)
	winTerminal.Buffer().SetDarkTheme(prefersDark)

	// Set up theme change callback (for CSI ? 5 h/l escape sequences)
	winTerminal.Buffer().SetThemeChangeCallback(func(isDark bool) {
		glib.IdleAdd(func() {
			winTerminal.SetColorScheme(getColorSchemeForTheme(isDark))
		})
	})

	// Create main layout with collapsible toolbar strip
	paned, _ := gtk.PanedNew(gtk.ORIENTATION_HORIZONTAL)

	// Track script running state for this window (starts with no script)
	var winScriptRunning bool
	var winScriptMu sync.Mutex

	// Create MenuContext for this console window
	consoleMenuCtx := &MenuContext{
		Parent:         win,
		IsScriptWindow: true,
		Terminal:       winTerminal,
		IsScriptRunning: func() bool {
			winScriptMu.Lock()
			defer winScriptMu.Unlock()
			return winScriptRunning
		},
		CloseWindow: func() {
			win.Close()
		},
	}

	// Narrow strip for console window (always starts visible, collapsible)
	strip, stripMenuBtn, _ := createToolbarStripWithContext(consoleMenuCtx)
	strip.SetMarginStart(2 + narrowOnlyExtraPadding)
	strip.SetSizeRequest(scaledMinNarrowStripWidth(), -1)
	paned.Pack1(strip, false, true)

	// Terminal on the right
	termWidget := winTerminal.Widget()
	termWidget.SetVExpand(true)
	termWidget.SetHExpand(true)
	termWidget.SetMarginStart(8)
	paned.Pack2(termWidget, true, false)

	// Set initial strip width and collapse behavior
	consoleStripWidth := scaledMinNarrowStripWidth() + 4 + narrowOnlyExtraPadding
	paned.SetPosition(consoleStripWidth)

	// Track position changes for drag detection
	var consolePanedPressPos int = -1
	var consolePanedDragged bool
	var consolePanedOnHandle bool

	paned.Connect("notify::position", func() {
		defer runtime.KeepAlive(paned)
		pos := paned.GetPosition()
		if consolePanedPressPos >= 0 && consolePanedOnHandle && pos != consolePanedPressPos {
			consolePanedDragged = true
		}
		if pos == 0 {
			// Already collapsed
		} else if pos < consoleStripWidth/2 {
			paned.SetPosition(0)
		} else if pos != consoleStripWidth {
			paned.SetPosition(consoleStripWidth)
		}
	})

	paned.Connect("button-press-event", func(p *gtk.Paned, ev *gdk.Event) bool {
		btnEvent := gdk.EventButtonNewFromEvent(ev)
		var handleWindow *gdk.Window
		if btnEvent.Button() == 1 {
			consolePanedOnHandle = false
			var err error
			handleWindow, err = p.GetHandleWindow()
			if err == nil && handleWindow != nil {
				hx, hy := handleWindow.GetRootOrigin()
				hw := handleWindow.WindowGetWidth()
				hh := handleWindow.WindowGetHeight()
				clickX, clickY := int(btnEvent.XRoot()), int(btnEvent.YRoot())
				consolePanedOnHandle = clickX >= hx && clickX < hx+hw && clickY >= hy && clickY < hy+hh
			}
			if !consolePanedOnHandle {
				runtime.KeepAlive(btnEvent)
				runtime.KeepAlive(handleWindow)
				return false
			}
			consolePanedPressPos = p.GetPosition()
			consolePanedDragged = false
		}
		runtime.KeepAlive(btnEvent)
		runtime.KeepAlive(handleWindow)
		return false
	})

	paned.Connect("button-release-event", func(p *gtk.Paned, ev *gdk.Event) bool {
		btnEvent := gdk.EventButtonNewFromEvent(ev)
		defer runtime.KeepAlive(btnEvent)

		if btnEvent.Button() != 1 || !consolePanedOnHandle || consolePanedDragged || consolePanedPressPos < 0 {
			consolePanedPressPos = -1
			consolePanedOnHandle = false
			return false
		}
		if p.GetPosition() == 0 {
			p.SetPosition(consoleStripWidth)
		} else {
			p.SetPosition(0)
		}
		consolePanedPressPos = -1
		consolePanedOnHandle = false
		return true
	})

	win.Add(paned)

	// Create context menu for this console window
	winContextMenu, _ := gtk.MenuNew()

	winCopyItem := createMenuItemWithGutter("Copy", func() {
		winTerminal.CopySelection()
	})
	winContextMenu.Append(winCopyItem)

	winPasteItem := createMenuItemWithGutter("Paste", func() {
		winTerminal.PasteClipboard()
	})
	winContextMenu.Append(winPasteItem)

	winSelectAllItem := createMenuItemWithGutter("Select All", func() {
		winTerminal.SelectAll()
	})
	winContextMenu.Append(winSelectAllItem)

	winClearItem := createMenuItemWithGutter("Clear", func() {
		winTerminal.Clear()
	})
	winContextMenu.Append(winClearItem)

	winContextMenu.ShowAll()

	termWidget.Connect("button-press-event", func(widget *gtk.Box, ev *gdk.Event) bool {
		btn := gdk.EventButtonNewFromEvent(ev)
		if btn.Button() == 3 {
			winContextMenu.PopupAtPointer(ev)
			return true
		}
		return false
	})

	// Create I/O channels for this window's console
	stdoutReader, stdoutWriter := io.Pipe()
	stdinReader, stdinWriter := io.Pipe()

	// Get terminal capabilities from the widget (auto-updates on resize)
	termCaps := winTerminal.GetTerminalCapabilities()

	// Non-blocking output queue
	outputQueue := make(chan interface{}, 256)
	go func() {
		for item := range outputQueue {
			switch v := item.(type) {
			case []byte:
				stdoutWriter.Write(v)
			case chan struct{}:
				close(v)
			}
		}
	}()

	winOutCh := &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		Terminal:         termCaps,
		NativeSend: func(v interface{}) error {
			var text string
			switch d := v.(type) {
			case []byte:
				text = string(d)
			case string:
				text = d
			default:
				text = fmt.Sprintf("%v", v)
			}
			text = strings.ReplaceAll(text, "\r\n", "\n")
			text = strings.ReplaceAll(text, "\n", "\r\n")
			data := []byte(text)
			select {
			case outputQueue <- data:
			default:
			}
			return nil
		},
		NativeRecv: func() (interface{}, error) {
			return nil, fmt.Errorf("cannot receive from console_out")
		},
		NativeFlush: func() error {
			writerDone := make(chan struct{})
			select {
			case outputQueue <- writerDone:
				<-writerDone
			default:
			}
			glibDone := make(chan struct{})
			glib.IdleAdd(func() bool {
				close(glibDone)
				return false
			})
			select {
			case <-glibDone:
			case <-time.After(100 * time.Millisecond):
			}
			return nil
		},
	}

	// Non-blocking input queue
	inputQueue := make(chan byte, 256)
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := stdinReader.Read(buf)
			if err != nil || n == 0 {
				close(inputQueue)
				return
			}
			select {
			case inputQueue <- buf[0]:
			default:
				select {
				case <-inputQueue:
				default:
				}
				select {
				case inputQueue <- buf[0]:
				default:
				}
			}
		}
	}()

	winInCh := &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		Terminal:         termCaps,
		NativeRecv: func() (interface{}, error) {
			b, ok := <-inputQueue
			if !ok {
				return nil, fmt.Errorf("input closed")
			}
			return []byte{b}, nil
		},
		NativeSend: func(v interface{}) error {
			return fmt.Errorf("cannot send to console_in")
		},
	}

	// Read from stdout pipe and feed to terminal
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdoutReader.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				glib.IdleAdd(func() bool {
					winTerminal.FeedBytes(data)
					return false
				})
			}
			if err != nil {
				return
			}
		}
	}()

	// REPL for interactive mode
	var winREPL *pawscript.REPL

	// Wire keyboard input
	winTerminal.SetInputCallback(func(data []byte) {
		winScriptMu.Lock()
		isRunning := winScriptRunning
		winScriptMu.Unlock()

		if isRunning {
			stdinWriter.Write(data)
		} else if winREPL != nil && winREPL.IsRunning() {
			if winREPL.IsBusy() {
				// REPL is executing a command (e.g., read) - send to stdin pipe
				stdinWriter.Write(data)
			} else {
				// REPL is waiting for input - send to REPL for line editing
				winREPL.HandleInput(data)
			}
		}
	})

	// Handle window close - clean up resources
	win.Connect("destroy", func() {
		winContextMenu.Destroy()
		stdinWriter.Close()
		stdoutWriter.Close()
		stdinReader.Close()
		stdoutReader.Close()
		close(outputQueue)
	})

	win.ShowAll()

	// Start REPL immediately (no script to run first)
	go func() {
		winREPL = pawscript.NewREPL(pawscript.REPLConfig{
			Debug:        false,
			Unrestricted: false,
			OptLevel:     getOptimizationLevel(),
			ShowBanner:   true,
			IOConfig: &pawscript.IOChannelConfig{
				Stdout: winOutCh,
				Stdin:  winInCh,
				Stderr: winOutCh,
			},
		}, func(s string) {
			glib.IdleAdd(func() bool {
				winTerminal.Feed(s)
				return false
			})
		})
		// Set flush callback to ensure output appears before blocking execution
		winREPL.SetFlush(func() {
			for i := 0; i < 10 && gtk.EventsPending(); i++ {
				gtk.MainIterationDo(false)
			}
		})
		// Set background color for prompt color selection
		bg := getTerminalBackground()
		winREPL.SetBackgroundRGB(bg.R, bg.G, bg.B)
		winREPL.SetPSLColors(getPSLColors())
		winREPL.Start()

		// Register the dummy_button command with the window's REPL
		winToolbarData := &WindowToolbarData{
			strip:      strip,
			menuButton: stripMenuBtn,
			terminal:   winTerminal,
		}
		winToolbarData.updateFunc = func() {
			updateWindowToolbarButtons(winToolbarData.strip, winToolbarData.registeredBtns)
		}
		registerDummyButtonCommand(winREPL.GetPawScript(), winToolbarData)
	}()
}

// createHamburgerButton creates a hamburger menu button with SVG icon
// forVerticalStrip: true for vertical toolbar strip, false for horizontal rows (file selector)
// menuGetter: function that returns the menu to show (allows menu to be rebuilt dynamically)
func createHamburgerButton(menuGetter func() *gtk.Menu, forVerticalStrip bool) *gtk.Button {
	btn, _ := gtk.ButtonNew()
	btn.SetSizeRequest(32, 32)
	btn.SetTooltipText("Menu")
	applyToolbarButtonStyle(btn, forVerticalStrip)

	// Set SVG icon with appropriate color for current theme
	svgData := getSVGIcon(hamburgerIconSVG)
	if img := createImageFromSVG(svgData, scaledToolbarIconSize()); img != nil {
		btn.SetImage(img)
		btn.SetAlwaysShowImage(true)
	} else {
		// Fallback to text if SVG loading fails
		btn.SetLabel("☰")
	}

	// Pop up the menu on click - calls menuGetter each time to get current menu
	btn.Connect("clicked", func() {
		menu := menuGetter()
		if menu != nil {
			menu.PopupAtWidget(btn, gdk.GDK_GRAVITY_SOUTH_WEST, gdk.GDK_GRAVITY_NORTH_WEST, nil)
		}
	})

	return btn
}

// createToolbarStripWithContext creates a vertical strip of toolbar buttons with full context
// Returns the strip container, the hamburger button, and the MenuContext
func createToolbarStripWithContext(ctx *MenuContext) (*gtk.Box, *gtk.Button, *MenuContext) {
	return createToolbarStripWithMenu(ctx, nil)
}

// createToolbarStripWithMenu creates a vertical strip of toolbar buttons with an optional existing menu
// If existingMenu is nil, a new menu is created; otherwise the provided menu is used
// Returns the strip container, the hamburger button, and the MenuContext
func createToolbarStripWithMenu(ctx *MenuContext, existingMenu *gtk.Menu) (*gtk.Box, *gtk.Button, *MenuContext) {
	strip, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4) // 4px spacing between buttons
	strip.SetMarginStart(2)
	strip.SetMarginEnd(2)
	strip.SetMarginTop(5)
	strip.SetMarginBottom(5)

	// Use existing menu or create a new one
	menu := existingMenu
	if menu == nil {
		menu = createHamburgerMenu(ctx)
	}
	// Capture menu in closure for this window's button
	menuBtn := createHamburgerButton(func() *gtk.Menu { return menu }, true) // true = vertical strip

	strip.PackStart(menuBtn, false, false, 0)

	return strip, menuBtn, ctx
}

// createToolbarStripWithMenuGetter creates a vertical strip with a dynamic menu getter
// Used when the menu may be rebuilt (e.g., launcher menu on UI scale change)
func createToolbarStripWithMenuGetter(ctx *MenuContext, menuGetter func() *gtk.Menu) (*gtk.Box, *gtk.Button, *MenuContext) {
	strip, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4) // 4px spacing between buttons
	strip.SetMarginStart(2)
	strip.SetMarginEnd(2)
	strip.SetMarginTop(5)
	strip.SetMarginBottom(5)

	menuBtn := createHamburgerButton(menuGetter, true) // true = vertical strip

	strip.PackStart(menuBtn, false, false, 0)

	return strip, menuBtn, ctx
}

// createToolbarStrip creates a vertical strip of toolbar buttons (simplified version)
// Returns the strip container and the hamburger button
func createToolbarStrip(parent gtk.IWindow, isScriptWindow bool, term *purfectermgtk.Terminal) (*gtk.Box, *gtk.Button, *MenuContext) {
	ctx := &MenuContext{
		Parent:         parent,
		IsScriptWindow: isScriptWindow,
		Terminal:       term,
	}
	return createToolbarStripWithContext(ctx)
}

// Minimum widths for panel collapse behavior (base values at 1.0 scale)
const (
	minWidePanelWidth      = 196 // Minimum width before wide panel collapses
	minNarrowStripWidth    = 40  // Minimum width before narrow strip collapses
	narrowOnlyExtraPadding = 6   // Extra left padding when in narrow-only mode
)

// Scaled threshold helpers - these return values adjusted for current UI scale
func scaledMinWidePanelWidth() int {
	return int(float64(minWidePanelWidth) * getUIScale())
}

func scaledMinNarrowStripWidth() int {
	return int(float64(minNarrowStripWidth) * getUIScale())
}

func scaledBothThreshold() int {
	return (scaledMinWidePanelWidth() / 2) + scaledMinNarrowStripWidth()
}

func scaledNarrowSnapPoint() int {
	return scaledMinNarrowStripWidth() / 2
}

// Embedded SVG icons (fill color is replaced at runtime based on theme)
const hamburgerIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <rect style="fill:{{FILL}};stroke:none" width="11.346176" height="2.25" x="0.70038122" y="0.8404575"/>
  <rect style="fill:{{FILL}};stroke:none" width="11.346176" height="2.25" x="0.70038122" y="5.3383746"/>
  <rect style="fill:{{FILL}};stroke:none" width="11.346176" height="2.25" x="0.70038122" y="9.8362913"/>
</svg>`

const starIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <path style="fill:{{FILL}};stroke:none" d="M 6.4849512,1.5761366 8.0478061,4.7428264 11.542456,5.250629 9.0137037,7.7155534 9.6106608,11.196082 6.484951,9.5527997 3.359241,11.196082 3.9561984,7.7155534 1.4274463,5.2506288 4.9220959,4.7428264 Z" transform="matrix(1.1757817,0,0,1.1757817,-1.274887,-1.2479333)"/>
</svg>`

const trashIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <g transform="translate(0,-0.42545335)">
    <path style="fill:none;stroke:{{FILL}};stroke-width:1.25;stroke-linecap:butt;stroke-linejoin:miter" d="M 1.737022,2.4884974 3.2171891,11.510245 H 9.4828113 L 10.962978,2.4884974 Z"/>
    <path style="fill:{{FILL}};stroke:{{FILL}};stroke-linecap:round;stroke-linejoin:round" d="M 1.3199,1.9156617 H 11.38 l 0.399747,1.3487906 H 0.92025 Z"/>
    <g style="stroke-width:1.37432" transform="matrix(0.9098144,0,0,0.90927138,0.51615218,0.22722416)">
      <path style="fill:none;stroke:{{FILL}};stroke-width:1.37432;stroke-linecap:butt;stroke-linejoin:miter" d="M 9.7179479,10.776284 2.2806355,3.3389676"/>
      <path style="fill:none;stroke:{{FILL}};stroke-width:1.37432;stroke-linecap:butt;stroke-linejoin:miter" d="M 2.8490844,10.909391 10.419365,3.3389676"/>
      <rect style="fill:none;stroke:{{FILL}};stroke-width:1.37432;stroke-linecap:round;stroke-linejoin:round" width="4.892848" height="4.892848" x="7.282187" y="-1.6980692" transform="rotate(45)"/>
    </g>
  </g>
</svg>`

const folderIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <path style="fill:{{FILL}};stroke-linecap:round;stroke-linejoin:round" d="M 1.9065339,1.7962728 C 1.5842088,1.7963101 1.2979459,2.0022696 1.1954661,2.3078695 L 0.5663737,3.8958863 c -0.0256507,0.07611 -0.038911,0.1558459 -0.0392741,0.2361613 v 6.7556604 c -1.4756e-4,0.414463 0.33587832,0.750489 0.75034179,0.750342 h 8.8397706 c 0.337275,-3.16e-4 0.632863,-0.225708 0.722436,-0.550871 l 1.588017,-5.7557214 c 0.02486,-0.092219 0.03187,-0.1883361 0.02067,-0.2831869 -0.01456,0.00205 -0.02923,0.00326 -0.04392,0.00362 H 4.0664185 L 2.4861532,10.456009 C 2.4269087,10.657739 2.2159526,10.773834 2.0138306,10.715942 1.8099565,10.657629 1.6924277,10.444594 1.7518311,10.241035 L 3.4121948,4.5617955 C 3.459762,4.3986948 3.609202,4.2865095 3.7790975,4.2863601 H 11.39672 V 4.1318476 C 11.396582,3.7175863 11.06064,3.3818756 10.646379,3.3820225 H 6.4021932 L 5.0642904,2.0203486 C 4.9232898,1.87690 4.7305827,1.7960934 4.5294393,1.7962728 Z"/>
</svg>`

const homeIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <g transform="translate(-0.00109499,1.0501825)">
    <path style="fill:{{FILL}};stroke:{{FILL}};stroke-linecap:round;stroke-linejoin:miter" d="M 3.2755313,6.4035176 6.3499999,3.7576843 9.4244685,6.4035176 V 9.8547301 H 8.4943104 7.7936896 c -0.050205,-0.7055517 0.050205,-3.5196369 0,-3.1534491 L 4.9063104,6.6797301 v 3.175 H 4.2056896 3.2755313 Z"/>
    <path style="fill:none;stroke:{{FILL}};stroke-width:1.5;stroke-linecap:butt;stroke-linejoin:miter" d="M 1.3781068,5.4138305 6.34781,1.2300618 11.317513,5.5125077"/>
  </g>
</svg>`

const uncheckedIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <rect style="fill:none;stroke:{{FILL}};stroke-width:1;stroke-linecap:round" width="10.104374" height="10.104374" x="1.2978133" y="1.2978133"/>
</svg>`

const checkedIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <rect style="fill:none;stroke:{{FILL}};stroke-width:1;stroke-linecap:round" width="10.104374" height="10.104374" x="1.2978133" y="1.2978133"/>
  <path style="fill:none;stroke:{{FILL}};stroke-width:2.25;stroke-linecap:round;stroke-linejoin:round" d="M 3.3162955,7.1623081 5.7369373,9.2379784 10.237921,3.5516806"/>
</svg>`

const folderUpIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <path style="fill:{{FILL}};stroke-linecap:round;stroke-linejoin:round" d="M 1.9063436 0.91028314 C 1.5840188 0.9103204 1.2977556 1.1162802 1.1952759 1.4218798 L 0.5663737 3.0098966 C 0.54072302 3.0860065 0.52746271 3.1657426 0.52709961 3.2460579 L 0.52709961 10.001718 C 0.52695205 10.41618 0.86297835 10.752207 1.2774414 10.75206 L 10.117212 10.75206 C 10.454487 10.751744 10.750076 10.526352 10.839648 10.201189 L 12.427665 4.4454679 C 12.452525 4.353249 12.459536 4.2571317 12.448336 4.162281 C 12.433776 4.164331 12.419101 4.1655384 12.404411 4.1658984 L 4.0664185 4.1658984 L 2.4861532 9.5702197 C 2.4269087 9.7719494 2.2159524 9.8880447 2.0138306 9.8301521 C 1.8099567 9.77184 1.6924277 9.5588044 1.7518311 9.3552457 L 3.4121948 3.6760058 C 3.459762 3.5129053 3.6092022 3.4007198 3.7790975 3.4005704 L 11.39672 3.4005704 L 11.39672 3.2460579 C 11.396582 2.831797 11.060639 2.4960859 10.646379 2.4962328 L 6.4021932 2.4962328 L 5.0642904 1.1345589 C 4.9232899 0.99111043 4.7305825 0.91030369 4.5294393 0.91028314 L 1.9063436 0.91028314 z M 4.7516479 4.8821337 L 9.3906413 5.0604174 L 7.7907389 6.6603198 L 10.041764 8.9113451 L 8.7808594 10.17225 L 6.529834 7.9212247 L 4.9304484 9.5206103 L 4.7516479 4.8821337 z"/>
</svg>`

const unknownFileIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <path style="fill:#ffffff;stroke:#002b36;stroke-width:0.75;stroke-linecap:round;stroke-linejoin:round" d="M 2.6458333,11.906249 V 0.79375 h 5.0270833 l 2.38125,2.38125 v 8.73125 z"/>
  <path style="fill:#ffffff;stroke:#002b36;stroke-width:0.75;stroke-linecap:round;stroke-linejoin:round" d="m 7.6729166,0.79375 v 2.38125 h 2.38125"/>
  <text style="font-size:7.05556px;fill:#77abbe;stroke:none;font-family:sans-serif;font-weight:bold" x="3.7041667" y="9.5249996">?</text>
</svg>`

const pawFileIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <path style="fill:#ffffff;stroke:#002b36;stroke-width:0.75;stroke-linecap:round;stroke-linejoin:round" d="M 2.6910152,1.1822787 H 8.1260003 L 10.008985,3.0847273 V 11.517722 H 2.6910152 Z"/>
  <path style="fill:#ffffff;stroke:#002b36;stroke-width:0.5;stroke-linecap:butt;stroke-linejoin:miter" d="M 7.848,1.368 V 3.498 h 1.973"/>
  <rect style="fill:#268bd2;stroke:none" width="2.878" height="0.731" x="4.085" y="3.834"/>
  <rect style="fill:#268bd2;stroke:none" width="2.711" height="0.731" x="3.056" y="2.625"/>
  <rect style="fill:#268bd2;stroke:none" width="1.606" height="0.731" x="3.056" y="5.042"/>
  <path style="fill:#d33682" d="M 6.9877659,6.4940823 A 0.59432477,1.0177472 10.901417 0 0 7.363159,7.6235229 0.59432477,1.0177472 10.901417 0 0 8.1469646,6.7591416 0.59432477,1.0177472 10.901417 0 0 7.7715712,5.6297008 0.59432477,1.0177472 10.901417 0 0 6.9877659,6.4940823 Z M 8.0688455,7.6686372 A 0.58822118,0.84210657 24.692905 0 0 8.3152738,8.6391475 0.58822118,0.84210657 24.692905 0 0 9.1859233,8.0478374 0.58822118,0.84210657 24.692905 0 0 8.9394952,7.0773271 0.58822118,0.84210657 24.692905 0 0 8.0688455,7.6686372 Z M 5.3231631,7.5172962 A 0.80963169,0.55863957 74.019456 0 1 4.8344933,8.2706577 0.80963169,0.55863957 74.019456 0 1 4.1731169,7.4391573 0.80963169,0.55863957 74.019456 0 1 4.6617867,6.6857959 0.80963169,0.55863957 74.019456 0 1 5.3231631,7.5172962 Z M 6.582441,6.4764168 A 1.0177472,0.59432477 84.942216 0 1 6.0940057,7.561768 1.0177472,0.59432477 84.942216 0 1 5.4022794,6.6220762 1.0177472,0.59432477 84.942216 0 1 5.8907147,5.5367251 1.0177472,0.59432477 84.942216 0 1 6.582441,6.4764168 Z M 6.8071884,7.5727143 C 6.5623925,7.5505112 6.3191375,7.5972619 6.1140814,7.7369954 5.7508331,7.9845273 5.9422246,8.2677324 5.5915221,8.3848434 5.1536649,8.5000827 4.6876296,8.8060968 4.6364088,9.3673211 4.5797156,9.992466 5.0848467,10.492654 5.6678087,10.545828 c 0.5427322,0.06569 0.6863499,-0.436116 0.9458857,-0.395595 0.3134986,0.0427 0.274105,0.506502 0.7776369,0.552396 0.5829867,0.0529 1.1700891,-0.347918 1.2271409,-0.9730306 C 8.6693714,9.1683448 8.4256602,8.7681445 7.920995,8.6411957 7.5278107,8.4509588 7.7938464,8.1615864 7.4592698,7.859377 7.2751275,7.6930487 7.0519721,7.5950518 6.8071884,7.5727143 Z M 6.751527,8.1850563 A 0.42149629,0.32909713 5.1983035 0 0 6.3021075,8.4744839 0.42149629,0.32909713 5.1983035 0 0 6.6919181,8.8402625 0.42149629,0.32909713 5.1983035 0 0 7.1413376,8.5508348 0.42149629,0.32909713 5.1983035 0 0 6.751527,8.1850563 Z M 5.808412,9.0040512 A 0.52098234,0.46766435 5.1983035 0 0 5.2473463,9.4224819 0.52098234,0.46766435 5.1983035 0 0 5.7236902,9.9352932 0.52098234,0.46766435 5.1983035 0 0 6.2847559,9.5168626 0.52098234,0.46766435 5.1983035 0 0 5.808412,9.0040512 Z M 7.5313614,9.1608004 A 0.52098234,0.46766435 5.1983035 0 0 6.9702956,9.5792311 0.52098234,0.46766435 5.1983035 0 0 7.4466396,10.092042 0.52098234,0.46766435 5.1983035 0 0 8.0077053,9.6736118 0.52098234,0.46766435 5.1983035 0 0 7.5313614,9.1608004 Z"/>
</svg>`

// getIconFillColor returns the appropriate icon fill color based on applied theme
func getIconFillColor() string {
	if appliedThemeIsDark {
		return "#ffffff"
	}
	return "#000000"
}

// getSVGIcon returns SVG data with the fill color set appropriately for current theme
func getSVGIcon(svgTemplate string) string {
	return strings.Replace(svgTemplate, "{{FILL}}", getIconFillColor(), -1)
}

// getDarkSVGIcon returns SVG data with the dark mode fill color (white) for selected rows
func getDarkSVGIcon(svgTemplate string) string {
	return strings.Replace(svgTemplate, "{{FILL}}", "#ffffff", -1)
}

// resizeSVG modifies the width and height attributes in the root <svg> tag only
// This allows the renderer to rasterize the vector directly at the target size
func resizeSVG(svgData string, size int) string {
	sizeStr := fmt.Sprintf("%d", size)

	// Find the end of the opening <svg ...> tag
	endIdx := strings.Index(svgData, ">")
	if endIdx == -1 {
		return svgData
	}

	// Split into svg tag and rest of content
	svgTag := svgData[:endIdx+1]
	rest := svgData[endIdx+1:]

	// Replace width and height only in the opening svg tag
	svgTag = regexp.MustCompile(`width="[^"]*"`).ReplaceAllString(svgTag, `width="`+sizeStr+`"`)
	svgTag = regexp.MustCompile(`height="[^"]*"`).ReplaceAllString(svgTag, `height="`+sizeStr+`"`)

	return svgTag + rest
}

func createImageFromSVG(svgData string, size int) *gtk.Image {
	// Resize SVG to target size before loading - this lets the vector renderer
	// rasterize directly at the correct size, avoiding bitmap scaling artifacts
	resizedSVG := resizeSVG(svgData, size)

	// Create a PixbufLoader for SVG
	loader, err := gdk.PixbufLoaderNew()
	if err != nil {
		return nil
	}

	// Write SVG data to loader
	_, err = loader.Write([]byte(resizedSVG))
	if err != nil {
		loader.Close()
		return nil
	}

	err = loader.Close()
	if err != nil {
		return nil
	}

	// Get the pixbuf (already at target size)
	pixbuf, err := loader.GetPixbuf()
	if err != nil || pixbuf == nil {
		return nil
	}

	// Create image from pixbuf
	img, err := gtk.ImageNewFromPixbuf(pixbuf)
	if err != nil {
		return nil
	}

	return img
}

// createPixbufFromSVG creates a GdkPixbuf from SVG data (for updating existing images)
func createPixbufFromSVG(svgData string, size int) *gdk.Pixbuf {
	// Resize SVG to target size before loading
	resizedSVG := resizeSVG(svgData, size)

	loader, err := gdk.PixbufLoaderNew()
	if err != nil {
		return nil
	}

	_, err = loader.Write([]byte(resizedSVG))
	if err != nil {
		loader.Close()
		return nil
	}

	err = loader.Close()
	if err != nil {
		return nil
	}

	// Pixbuf is already at target size
	pixbuf, err := loader.GetPixbuf()
	if err != nil || pixbuf == nil {
		return nil
	}

	return pixbuf
}

// updateRowIcon updates the icon in a file list row
func updateRowIcon(row *gtk.ListBoxRow, useDarkIcon bool) {
	if row == nil {
		return
	}

	iconType, ok := rowIconTypeMap[row]
	if !ok {
		return
	}

	// Find the image in this row
	img := findRowImage(row)
	if img == nil {
		return
	}

	// Get the SVG template based on icon type
	var svgTemplate string
	switch iconType {
	case gtkIconTypeFolderUp:
		svgTemplate = folderUpIconSVG
	case gtkIconTypeFolder:
		svgTemplate = folderIconSVG
	case gtkIconTypePawFile:
		svgTemplate = pawFileIconSVG
	default:
		return
	}

	// Get SVG data with appropriate fill color
	var svgData string
	if useDarkIcon {
		svgData = getDarkSVGIcon(svgTemplate)
	} else {
		svgData = getSVGIcon(svgTemplate)
	}

	// Update the existing image with new pixbuf
	if pixbuf := createPixbufFromSVG(svgData, scaledFileListIconSize()); pixbuf != nil {
		img.SetFromPixbuf(pixbuf)
	}
}

// createMenuItemWithIcon creates a GTK menu item with an SVG icon and label
// Uses a GtkBox with Image and Label for proper icon display in gutter
func createMenuItemWithIcon(svgTemplate string, labelText string, callback func()) *gtk.MenuItem {
	// Create a plain menu item (no built-in label)
	item, err := gtk.MenuItemNew()
	if err != nil {
		return nil
	}

	// Add CSS class for styling
	styleCtx, _ := item.GetStyleContext()
	if styleCtx != nil {
		styleCtx.AddClass("has-icon")
	}

	// Create horizontal box to hold icon and label
	hbox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	if err != nil {
		return nil
	}
	hbox.SetVExpand(true) // Expand to fill vertical space

	// Create the icon image (16x16)
	svgData := getSVGIcon(svgTemplate)
	img := createImageFromSVG(svgData, scaledMenuIconSize())
	if img != nil {
		// Position icon as far left as possible
		img.SetMarginStart(0)
		img.SetMarginEnd(18) // 0 + 16 + 18 = 34 (gutter + edge)
		img.SetVAlign(gtk.ALIGN_CENTER)
		hbox.PackStart(img, false, false, 0)
	}

	// Create label with spacing after gutter edge
	label, err := gtk.LabelNew(labelText)
	if err == nil {
		label.SetXAlign(0) // Left align
		label.SetVAlign(gtk.ALIGN_CENTER)
		label.SetMarginStart(8) // Space after edge line
		hbox.PackStart(label, true, true, 0)
	}

	// Add the box to the menu item
	item.Add(hbox)

	if callback != nil {
		item.Connect("activate", callback)
	}

	return item
}

// formatShortcutForDisplay converts a shortcut string to display format
// On macOS, uses symbols: Cmd->⌘, Ctrl->⌃, Alt->⌥, Shift->⇧
// On other platforms, keeps text format
func formatShortcutForDisplay(shortcut string) string {
	if shortcut == "" {
		return ""
	}

	parts := strings.Split(shortcut, "+")
	result := make([]string, 0, len(parts))

	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		lower := strings.ToLower(trimmed)

		if runtime.GOOS == "darwin" {
			// Use macOS symbols
			switch lower {
			case "cmd", "meta", "super":
				result = append(result, "⌘")
			case "ctrl", "control":
				result = append(result, "⌃")
			case "alt", "opt", "option":
				result = append(result, "⌥")
			case "shift":
				result = append(result, "⇧")
			default:
				result = append(result, strings.ToUpper(trimmed))
			}
		} else {
			// Keep text format for other platforms
			switch lower {
			case "cmd", "meta", "super":
				result = append(result, "Meta")
			case "ctrl", "control":
				result = append(result, "Ctrl")
			case "alt", "opt", "option":
				result = append(result, "Alt")
			case "shift":
				result = append(result, "Shift")
			default:
				result = append(result, strings.ToUpper(trimmed))
			}
		}
	}

	// On macOS, join without separator for compact look
	if runtime.GOOS == "darwin" {
		return strings.Join(result, "")
	}
	return strings.Join(result, "+")
}

// createMenuItemWithGutter creates a GTK menu item with gutter but no icon
// Uses a GtkBox with Label for consistent gutter appearance
func createMenuItemWithGutter(labelText string, callback func()) *gtk.MenuItem {
	// Create a plain menu item (no built-in label)
	item, err := gtk.MenuItemNew()
	if err != nil {
		return nil
	}

	// Add CSS class for styling
	styleCtx, _ := item.GetStyleContext()
	if styleCtx != nil {
		styleCtx.AddClass("has-gutter")
	}

	// Create horizontal box
	hbox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	if err != nil {
		return nil
	}
	hbox.SetVExpand(true) // Expand to fill vertical space

	// Create label with left margin to account for gutter + edge + spacing
	label, err := gtk.LabelNew(labelText)
	if err == nil {
		label.SetXAlign(0) // Left align
		label.SetVAlign(gtk.ALIGN_CENTER)
		label.SetMarginStart(42) // Gutter (34) + spacing (8)
		hbox.PackStart(label, true, true, 0)
	}

	// Add the box to the menu item
	item.Add(hbox)

	if callback != nil {
		item.Connect("activate", callback)
	}

	return item
}

// createMenuItemWithShortcut creates a GTK menu item with a grayed shortcut on the right
func createMenuItemWithShortcut(labelText, shortcut string, callback func()) *gtk.MenuItem {
	// Create a plain menu item (no built-in label)
	item, err := gtk.MenuItemNew()
	if err != nil {
		return nil
	}

	// Add CSS class for styling
	styleCtx, _ := item.GetStyleContext()
	if styleCtx != nil {
		styleCtx.AddClass("has-gutter")
	}

	// Create horizontal box
	hbox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	if err != nil {
		return nil
	}
	hbox.SetVExpand(true)

	// Create main label with left margin
	label, err := gtk.LabelNew(labelText)
	if err == nil {
		label.SetXAlign(0)
		label.SetVAlign(gtk.ALIGN_CENTER)
		label.SetMarginStart(42) // Gutter (34) + spacing (8)
		hbox.PackStart(label, true, true, 0)
	}

	// Create shortcut label (grayed, right-aligned)
	if shortcut != "" {
		displayShortcut := formatShortcutForDisplay(shortcut)
		shortcutLabel, err := gtk.LabelNew(displayShortcut)
		if err == nil {
			shortcutLabel.SetXAlign(1)
			shortcutLabel.SetVAlign(gtk.ALIGN_CENTER)
			shortcutLabel.SetMarginEnd(8)
			// Apply gray color via CSS
			shortcutStyleCtx, _ := shortcutLabel.GetStyleContext()
			if shortcutStyleCtx != nil {
				cssProvider, _ := gtk.CssProviderNew()
				cssProvider.LoadFromData("label { color: alpha(currentColor, 0.55); }")
				shortcutStyleCtx.AddProvider(cssProvider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
			}
			hbox.PackEnd(shortcutLabel, false, false, 0)
		}
	}

	// Add the box to the menu item
	item.Add(hbox)

	if callback != nil {
		item.Connect("activate", callback)
	}

	return item
}

// updateFileListMenuIcon updates the icon on a File List menu item based on checked state
func updateFileListMenuIcon(item *gtk.MenuItem, isChecked bool) {
	if item == nil {
		return
	}

	// Select icon based on state
	var iconSVG string
	if isChecked {
		iconSVG = checkedIconSVG
	} else {
		iconSVG = uncheckedIconSVG
	}

	// Get the box child of the menu item
	child, err := item.GetChild()
	if err != nil || child == nil {
		return
	}

	box, ok := child.(*gtk.Box)
	if !ok {
		return
	}

	// Collect widgets to remove (don't modify during iteration)
	var toRemove []gtk.IWidget
	children := box.GetChildren()
	children.Foreach(func(item interface{}) {
		if widget, ok := item.(gtk.IWidget); ok {
			toRemove = append(toRemove, widget)
		}
	})

	// Remove and destroy old widgets to prevent GC/unref issues
	for _, widget := range toRemove {
		box.Remove(widget)
		widget.ToWidget().Destroy()
	}
	// Clear references and force GC
	toRemove = nil
	runtime.GC()

	// Recreate the icon
	svgData := getSVGIcon(iconSVG)
	newImg := createImageFromSVG(svgData, scaledMenuIconSize())
	if newImg != nil {
		newImg.SetMarginStart(0)
		newImg.SetMarginEnd(18)
		newImg.SetVAlign(gtk.ALIGN_CENTER)
		box.PackStart(newImg, false, false, 0)
	}

	// Recreate the label
	label, err := gtk.LabelNew("File List")
	if err == nil {
		label.SetXAlign(0)
		label.SetVAlign(gtk.ALIGN_CENTER)
		label.SetMarginStart(8)
		box.PackStart(label, true, true, 0)
	}

	box.ShowAll()
}

// SettingsComboMenu represents a styled combo menu for settings dialogs
type SettingsComboMenu struct {
	Button   *gtk.MenuButton
	Menu     *gtk.Menu
	Label    *gtk.Label
	items    []*gtk.MenuItem
	options  []string
	selected int
	onChange func(int)
}

// createSettingsComboMenu creates a styled combo menu with check icon for selected item
func createSettingsComboMenu(options []string, selected int, onChange func(int)) *SettingsComboMenu {
	combo := &SettingsComboMenu{
		options:  options,
		selected: selected,
		onChange: onChange,
	}

	// Create menu button
	combo.Button, _ = gtk.MenuButtonNew()
	combo.Button.SetHExpand(true)

	// Create label to show current selection
	combo.Label, _ = gtk.LabelNew(options[selected])
	combo.Label.SetXAlign(0)
	combo.Label.SetMarginStart(8)
	combo.Label.SetMarginEnd(8)
	combo.Button.Add(combo.Label)

	// Create menu
	combo.Menu, _ = gtk.MenuNew()
	combo.Button.SetPopup(combo.Menu)

	// Create menu items
	combo.items = make([]*gtk.MenuItem, len(options))
	for i, option := range options {
		idx := i // Capture for closure
		optText := option

		var item *gtk.MenuItem
		if i == selected {
			// Selected item gets check icon
			item = createMenuItemWithIcon(checkedIconSVG, optText, nil)
		} else {
			// Unselected items get gutter spacing only
			item = createMenuItemWithGutter(optText, nil)
		}

		item.Connect("activate", func() {
			combo.SetSelected(idx)
			if combo.onChange != nil {
				combo.onChange(idx)
			}
		})

		combo.items[i] = item
		combo.Menu.Append(item)
	}

	combo.Menu.ShowAll()
	return combo
}

// SetSelected updates the selected item in the combo menu
func (c *SettingsComboMenu) SetSelected(idx int) {
	if idx < 0 || idx >= len(c.options) {
		return
	}

	oldSelected := c.selected
	c.selected = idx

	// Update the button label
	c.Label.SetText(c.options[idx])

	// Update the menu items: remove old check, add new check
	if oldSelected != idx {
		// Update old selected item (remove check)
		c.updateMenuItem(oldSelected, false)
		// Update new selected item (add check)
		c.updateMenuItem(idx, true)
	}
}

// updateMenuItem recreates a menu item with or without the check icon
func (c *SettingsComboMenu) updateMenuItem(idx int, isSelected bool) {
	if idx < 0 || idx >= len(c.items) {
		return
	}

	item := c.items[idx]
	if item == nil {
		return
	}

	// Get the box child
	child, err := item.GetChild()
	if err != nil || child == nil {
		return
	}

	box, ok := child.(*gtk.Box)
	if !ok {
		return
	}

	// Remove all children from the box safely
	var toRemove []gtk.IWidget
	children := box.GetChildren()
	if children != nil {
		children.Foreach(func(item interface{}) {
			if widget, ok := item.(gtk.IWidget); ok {
				toRemove = append(toRemove, widget)
			}
		})
	}
	for _, widget := range toRemove {
		box.Remove(widget)
	}
	// Clear references and force GC
	toRemove = nil
	runtime.GC()

	optText := c.options[idx]

	if isSelected {
		// Add check icon
		svgData := getSVGIcon(checkedIconSVG)
		img := createImageFromSVG(svgData, scaledMenuIconSize())
		if img != nil {
			img.SetMarginStart(0)
			img.SetMarginEnd(18)
			img.SetVAlign(gtk.ALIGN_CENTER)
			box.PackStart(img, false, false, 0)
		}
		// Add label
		label, _ := gtk.LabelNew(optText)
		label.SetXAlign(0)
		label.SetVAlign(gtk.ALIGN_CENTER)
		label.SetMarginStart(8)
		box.PackStart(label, true, true, 0)
	} else {
		// Just label with gutter margin
		label, _ := gtk.LabelNew(optText)
		label.SetXAlign(0)
		label.SetVAlign(gtk.ALIGN_CENTER)
		label.SetMarginStart(42) // Gutter (34) + spacing (8)
		box.PackStart(label, true, true, 0)
	}

	box.ShowAll()
}

// GetSelected returns the currently selected index
func (c *SettingsComboMenu) GetSelected() int {
	return c.selected
}

// RefreshIcons updates all icons in the menu to match the current theme
func (c *SettingsComboMenu) RefreshIcons() {
	// Only need to refresh the selected item's icon since unselected items don't have icons
	if c.selected >= 0 && c.selected < len(c.items) {
		c.updateMenuItem(c.selected, true)
	}
}

// ColorSwatch represents a clickable color swatch button for palette editing
type ColorSwatch struct {
	Button            *gtk.Button
	colorHex          string
	inheritedColorHex string // Color to show when disabled (inherited from basic)
	onChange          func(string)
	isEnabled         bool
	textLabel         string
	textColorHex      string
}

// createColorSwatch creates a color swatch button that opens a color picker when clicked
func createColorSwatch(initialColor string, size int, onChange func(string)) *ColorSwatch {
	swatch := &ColorSwatch{
		colorHex:  initialColor,
		onChange:  onChange,
		isEnabled: true,
	}

	swatch.Button, _ = gtk.ButtonNew()
	swatch.Button.SetSizeRequest(size, size)

	// Apply the color as background via CSS
	swatch.applyColor()

	swatch.Button.Connect("clicked", func() {
		if !swatch.isEnabled {
			return
		}
		// Open color chooser dialog
		colorDialog, _ := gtk.ColorChooserDialogNew("Choose Color", nil)

		// Set current color
		if swatch.colorHex != "" {
			rgba := gdk.NewRGBA(0, 0, 0, 1)
			if rgba.Parse(swatch.colorHex) {
				colorDialog.SetRGBA(rgba)
			}
		}

		response := colorDialog.Run()
		if response == gtk.RESPONSE_OK {
			rgba := colorDialog.GetRGBA()
			// Convert to hex
			r := int(rgba.GetRed() * 255)
			g := int(rgba.GetGreen() * 255)
			b := int(rgba.GetBlue() * 255)
			newHex := fmt.Sprintf("#%02X%02X%02X", r, g, b)
			swatch.SetColor(newHex)
			if swatch.onChange != nil {
				swatch.onChange(newHex)
			}
		}
		colorDialog.Destroy()
	})

	return swatch
}

// SetColor updates the swatch color
func (s *ColorSwatch) SetColor(hex string) {
	s.colorHex = hex
	s.applyColor()
}

// GetColor returns the current color hex value
func (s *ColorSwatch) GetColor() string {
	return s.colorHex
}

// SetEnabled enables or disables the swatch
func (s *ColorSwatch) SetEnabled(enabled bool) {
	s.isEnabled = enabled
	s.Button.SetSensitive(enabled)
	s.applyColor()
}

// applyColor applies the current color as button background via CSS
func (s *ColorSwatch) applyColor() {
	cssProvider, err := gtk.CssProviderNew()
	if err != nil {
		return
	}

	bgColor := s.colorHex
	if bgColor == "" {
		bgColor = "#808080" // Gray for empty
	}

	// When disabled, show inherited color with hash lines
	showHashLines := false
	if !s.isEnabled {
		if s.inheritedColorHex != "" {
			bgColor = s.inheritedColorHex
		}
		showHashLines = true
	}

	// Calculate contrasting border and hash line color
	borderColor := "#000000"
	hashColor := "rgba(0, 0, 0, 0.4)"
	if bgColor != "" && len(bgColor) == 7 {
		r, _ := strconv.ParseInt(bgColor[1:3], 16, 64)
		g, _ := strconv.ParseInt(bgColor[3:5], 16, 64)
		b, _ := strconv.ParseInt(bgColor[5:7], 16, 64)
		luminance := (r*299 + g*587 + b*114) / 1000
		if luminance < 128 {
			borderColor = "#FFFFFF"
			hashColor = "rgba(255, 255, 255, 0.4)"
		}
	}

	// Include text color if set
	textColorCSS := ""
	labelColorCSS := ""
	if s.textColorHex != "" {
		textColorCSS = fmt.Sprintf("color: %s;", s.textColorHex)
		labelColorCSS = fmt.Sprintf("button label { color: %s; }", s.textColorHex)
	}

	var css string
	if showHashLines {
		// Diagonal hash lines pattern using repeating-linear-gradient
		css = fmt.Sprintf(`
			button {
				background-color: %s;
				background-image: repeating-linear-gradient(
					45deg,
					%s,
					%s 2px,
					transparent 2px,
					transparent 6px
				);
				border: 1px solid %s;
				border-radius: 3px;
				padding: 0;
				min-width: 0;
				min-height: 0;
				%s
			}
			%s
		`, bgColor, hashColor, hashColor, borderColor, textColorCSS, labelColorCSS)
	} else {
		css = fmt.Sprintf(`
			button {
				background-color: %s;
				background-image: none;
				border: 1px solid %s;
				border-radius: 3px;
				padding: 0;
				min-width: 0;
				min-height: 0;
				%s
			}
			button:hover {
				background-color: %s;
				border: 2px solid %s;
			}
			%s
		`, bgColor, borderColor, textColorCSS, bgColor, borderColor, labelColorCSS)
	}

	cssProvider.LoadFromData(css)
	styleCtx, err := s.Button.GetStyleContext()
	if err != nil {
		return
	}
	styleCtx.AddProvider(cssProvider, gtk.STYLE_PROVIDER_PRIORITY_USER)
}

// SetInheritedColor sets the color to display when the swatch is disabled (inherited)
func (s *ColorSwatch) SetInheritedColor(hex string) {
	s.inheritedColorHex = hex
	if !s.isEnabled {
		s.applyColor()
	}
}

// SetText sets a text label and its color on the swatch
func (s *ColorSwatch) SetText(label, colorHex string) {
	s.textLabel = label
	s.textColorHex = colorHex
	s.Button.SetLabel(label)

	// Apply color directly to the label child widget
	if colorHex != "" {
		child, err := s.Button.GetChild()
		if err == nil && child != nil {
			if lbl, ok := child.(*gtk.Label); ok {
				cssProvider, err := gtk.CssProviderNew()
				if err == nil {
					css := fmt.Sprintf(`label { color: %s; }`, colorHex)
					cssProvider.LoadFromData(css)
					styleCtx, err := lbl.GetStyleContext()
					if err == nil {
						styleCtx.AddProvider(cssProvider, gtk.STYLE_PROVIDER_PRIORITY_USER)
					}
				}
			}
		}
	}

	s.applyColor()
}

// PaletteColorRow holds the widgets for a single palette color entry
type PaletteColorRow struct {
	BasicSwatch    *ColorSwatch
	LightSwatch    *ColorSwatch
	LightCheckbox  *gtk.CheckButton
	DarkSwatch     *ColorSwatch
	DarkCheckbox   *gtk.CheckButton
	ColorName      string
	ColorIndex     int
}

// applyToolbarButtonStyle applies CSS to make toolbar buttons square with equal padding
// forVerticalStrip: true for buttons in the vertical toolbar strip (need more top/bottom)
//
//	false for buttons in horizontal rows like file selector (match row height)
func applyToolbarButtonStyle(btn *gtk.Button, forVerticalStrip bool) {
	cssProvider, err := gtk.CssProviderNew()
	if err != nil {
		return
	}
	var css string
	if forVerticalStrip {
		// Vertical strip: more vertical padding for taller buttons
		css = `
			button {
				padding: 6px 4px;
				min-width: 0;
				min-height: 0;
			}
		`
	} else {
		// Horizontal row: more horizontal padding to be closer to square while matching row height
		css = `
			button {
				padding: 4px 6px;
				min-width: 0;
				min-height: 0;
			}
		`
	}
	cssProvider.LoadFromData(css)
	styleCtx, err := btn.GetStyleContext()
	if err != nil {
		return
	}
	styleCtx.AddProvider(cssProvider, gtk.STYLE_PROVIDER_PRIORITY_USER)
}

// Random icons for dummy buttons
var dummyIcons = []string{"★", "♦", "♠", "♣", "♥", "●", "■", "▲", "◆", "⬟", "⬢", "✦", "✧", "⚡", "☀", "☁", "☂", "☃", "✿", "❀"}

// updateLauncherToolbarButtons updates the launcher's narrow strip with the current registered buttons
func updateLauncherToolbarButtons() {
	if launcherNarrowStrip == nil {
		return
	}

	// Check current state before updating (strip visible = had buttons before)
	hadButtons := launcherNarrowStrip.GetVisible()

	// Get the strip's layout box (first child is the layout container)
	// Actually the strip IS the box, so we work with it directly

	// Remove existing dummy buttons (but keep the hamburger menu button)
	// Collect widgets to remove (skip first child which is hamburger button)
	var toRemove []gtk.IWidget
	i := 0
	launcherNarrowStrip.GetChildren().Foreach(func(item interface{}) {
		if i > 0 { // Skip first child (hamburger button)
			if widget, ok := item.(gtk.IWidget); ok {
				toRemove = append(toRemove, widget)
			}
		}
		i++
	})
	for _, widget := range toRemove {
		launcherNarrowStrip.Remove(widget)
	}
	// Clear references and force GC to prevent finalizer crash
	toRemove = nil
	runtime.GC()

	// Add new dummy buttons
	for _, btn := range launcherRegisteredBtns {
		button, _ := gtk.ButtonNew()
		button.SetSizeRequest(32, 32)
		button.SetTooltipText(btn.Tooltip)
		applyToolbarButtonStyle(button, true) // true = vertical strip
		// Set SVG icon with appropriate color for current theme
		svgData := getSVGIcon(starIconSVG)
		if img := createImageFromSVG(svgData, scaledToolbarIconSize()); img != nil {
			button.SetImage(img)
			button.SetAlwaysShowImage(true)
		} else {
			// Fallback to text if SVG loading fails
			button.SetLabel(btn.Icon)
		}
		if btn.OnClick != nil {
			callback := btn.OnClick // Capture for closure
			button.Connect("clicked", func() {
				callback()
			})
		}
		btn.widget = button
		launcherNarrowStrip.PackStart(button, false, false, 0)
		button.Show()
	}

	// Update visibility based on button count
	hasMultipleButtons := len(launcherRegisteredBtns) > 0

	// Adjust splitter position when transitioning between modes
	// Only adjust if we're in wide mode (position > narrow-only threshold)
	if launcherPaned != nil && launcherWidePanel != nil && launcherWidePanel.GetVisible() {
		pos := launcherPaned.GetPosition()
		narrowWidth := scaledMinNarrowStripWidth()
		if hadButtons && !hasMultipleButtons {
			// Transitioning from both mode to wide-only: subtract strip width
			launcherPaned.SetPosition(pos - narrowWidth)
		} else if !hadButtons && hasMultipleButtons {
			// Transitioning from wide-only to both mode: add strip width
			launcherPaned.SetPosition(pos + narrowWidth)
		}
	}

	if hasMultipleButtons {
		// Show narrow strip, hide menu button in path row
		launcherNarrowStrip.Show()
		if launcherMenuButton != nil {
			launcherMenuButton.Hide()
		}
		if launcherStripMenuBtn != nil {
			launcherStripMenuBtn.Show()
		}
	} else {
		// Hide narrow strip, show menu button in path row
		launcherNarrowStrip.Hide()
		if launcherMenuButton != nil {
			launcherMenuButton.Show()
		}
	}
}

// updateWindowToolbarButtons updates a window's toolbar strip with its registered buttons
func updateWindowToolbarButtons(strip *gtk.Box, buttons []*ToolbarButton) {
	if strip == nil {
		return
	}

	// Remove existing dummy buttons (but keep the hamburger menu button as first child)
	var toRemove []gtk.IWidget
	i := 0
	strip.GetChildren().Foreach(func(item interface{}) {
		if i > 0 { // Skip first child (hamburger button)
			if widget, ok := item.(gtk.IWidget); ok {
				toRemove = append(toRemove, widget)
			}
		}
		i++
	})
	for _, widget := range toRemove {
		strip.Remove(widget)
	}
	// Clear references and force GC to prevent finalizer crash
	toRemove = nil
	runtime.GC()

	// Add new dummy buttons
	for _, btn := range buttons {
		button, _ := gtk.ButtonNew()
		button.SetSizeRequest(32, 32)
		button.SetTooltipText(btn.Tooltip)
		applyToolbarButtonStyle(button, true) // true = vertical strip
		// Set SVG icon with appropriate color for current theme
		svgData := getSVGIcon(starIconSVG)
		if img := createImageFromSVG(svgData, scaledToolbarIconSize()); img != nil {
			button.SetImage(img)
			button.SetAlwaysShowImage(true)
		} else {
			// Fallback to text if SVG loading fails
			button.SetLabel(btn.Icon)
		}
		if btn.OnClick != nil {
			callback := btn.OnClick // Capture for closure
			button.Connect("clicked", func() {
				callback()
			})
		}
		btn.widget = button
		strip.PackStart(button, false, false, 0)
		button.Show()
	}

	// Always show the strip when it has a hamburger button (console windows)
	strip.Show()
}

// setDummyButtonsForWindow sets the number of dummy buttons for a specific window
func setDummyButtonsForWindow(data *WindowToolbarData, count int) {
	// Clear existing dummy buttons
	data.registeredBtns = nil

	// Add new dummy buttons
	for i := 0; i < count; i++ {
		icon := dummyIcons[i%len(dummyIcons)]
		idx := i              // Capture for closure
		term := data.terminal // Capture terminal for closure
		btn := &ToolbarButton{
			Icon:    icon,
			Tooltip: fmt.Sprintf("Dummy Button %d", i+1),
			OnClick: func() {
				if term != nil {
					term.Feed(fmt.Sprintf("\r\nDummy button %d clicked!\r\n", idx+1))
				}
			},
		}
		data.registeredBtns = append(data.registeredBtns, btn)
	}

	// Update the toolbar strip on GTK main thread
	updateFunc := data.updateFunc
	glib.IdleAdd(func() bool {
		if updateFunc != nil {
			updateFunc()
		}
		return false
	})
}

// setDummyButtons sets the number of dummy buttons in the launcher toolbar strip (legacy)
func setDummyButtons(count int) {
	// Clear existing dummy buttons
	launcherRegisteredBtns = nil

	// Add new dummy buttons
	for i := 0; i < count; i++ {
		icon := dummyIcons[i%len(dummyIcons)]
		idx := i // Capture for closure
		btn := &ToolbarButton{
			Icon:    icon,
			Tooltip: fmt.Sprintf("Dummy Button %d", i+1),
			OnClick: func() {
				if terminal != nil {
					terminal.Feed(fmt.Sprintf("\r\nDummy button %d clicked!\r\n", idx+1))
				}
			},
		}
		launcherRegisteredBtns = append(launcherRegisteredBtns, btn)
	}

	// Update the toolbar strip on GTK main thread
	glib.IdleAdd(func() bool {
		updateLauncherToolbarButtons()
		return false
	})
}

// registerDummyButtonCommand registers the dummy_button command with PawScript
// using per-window toolbar data
func registerDummyButtonCommand(ps *pawscript.PawScript, data *WindowToolbarData) {
	// Store the association
	toolbarDataMu.Lock()
	toolbarDataByPS[ps] = data
	toolbarDataMu.Unlock()

	ps.RegisterCommand("dummy_button", func(ctx *pawscript.Context) pawscript.Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(pawscript.CatCommand, "dummy_button requires a count argument")
			return pawscript.BoolStatus(false)
		}

		// Get the count argument
		count := 0
		switch v := ctx.Args[0].(type) {
		case int:
			count = v
		case int64:
			count = int(v)
		case float64:
			count = int(v)
		default:
			ctx.LogError(pawscript.CatCommand, "dummy_button requires a numeric argument")
			return pawscript.BoolStatus(false)
		}

		if count < 0 {
			count = 0
		}
		if count > 20 {
			count = 20 // Cap at 20 buttons
		}

		// Use the captured window data
		setDummyButtonsForWindow(data, count)
		ctx.SetResult(count)
		return pawscript.BoolStatus(true)
	})
}

// detectSystemDarkMode checks if the system is using a dark theme
// Uses platform-specific detection methods for reliability
func detectSystemDarkMode() bool {
	// macOS: Use defaults command to read AppleInterfaceStyle
	if runtime.GOOS == "darwin" {
		cmd := exec.Command("defaults", "read", "-g", "AppleInterfaceStyle")
		output, err := cmd.Output()
		if err == nil && strings.TrimSpace(string(output)) == "Dark" {
			return true
		}
		// If command fails or returns non-"Dark", it's light mode
		return false
	}

	// Linux/Windows: Use GTK theme detection
	// Create a temporary window to get theme colors
	tempWin, err := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		return false // Default to light
	}
	defer tempWin.Destroy()

	styleCtx, err := tempWin.GetStyleContext()
	if err != nil {
		return false
	}

	// Try to lookup the theme background color
	// "theme_bg_color" is a standard named color in GTK themes
	if bgColor, ok := styleCtx.LookupColor("theme_bg_color"); ok {
		// Calculate luminosity using the standard formula
		// luminosity = 0.299*R + 0.587*G + 0.114*B
		r := bgColor.GetRed()
		g := bgColor.GetGreen()
		b := bgColor.GetBlue()
		luminosity := 0.299*r + 0.587*g + 0.114*b

		// If luminosity < 0.5 (50%), it's a dark theme
		return luminosity < 0.5
	}

	// Fallback: check the foreground color - if it's light, background is probably dark
	fgColor := styleCtx.GetColor(gtk.STATE_FLAG_NORMAL)
	r := fgColor.GetRed()
	g := fgColor.GetGreen()
	b := fgColor.GetBlue()
	fgLuminosity := 0.299*r + 0.587*g + 0.114*b

	// If foreground is bright (> 0.5), it's likely a dark theme
	return fgLuminosity > 0.5
}

// applyTheme sets the GTK theme based on the configuration.
// "auto" = detect OS preference, "dark" = force dark, "light" = force light
func applyTheme(theme pawgui.ThemeMode) {
	settings, err := gtk.SettingsGetDefault()
	if err != nil {
		return
	}

	// For Auto mode, detect the system preference using background luminosity
	if theme == pawgui.ThemeAuto {
		if detectSystemDarkMode() {
			theme = pawgui.ThemeDark
		} else {
			theme = pawgui.ThemeLight
		}
	}

	// Track the actual applied theme for icon colors
	appliedThemeIsDark = (theme == pawgui.ThemeDark)

	switch theme {
	case pawgui.ThemeDark:
		settings.SetProperty("gtk-application-prefer-dark-theme", true)
	case pawgui.ThemeLight:
		settings.SetProperty("gtk-application-prefer-dark-theme", false)
	}

	// Apply menu CSS styling
	applyMenuCSS(theme == pawgui.ThemeDark)
}

// menuCSSProvider is reused to avoid creating multiple providers
var menuCSSProvider *gtk.CssProvider

// mainCSSProvider is used for UI scaling CSS
var mainCSSProvider *gtk.CssProvider

// applyMainCSS applies UI scaling CSS to all windows
func applyMainCSS() {
	if mainWindow == nil {
		return
	}

	// GTK uses 0.8x the config scale to match visual appearance with Qt
	uiScale := getUIScale() * 0.8
	baseFontSize := int(10.0 * uiScale)
	buttonPadding := int(6.0 * uiScale)

	if mainCSSProvider == nil {
		mainCSSProvider, _ = gtk.CssProviderNew()
	}

	mainCSSProvider.LoadFromData(fmt.Sprintf(`
		* {
			font-size: %dpx;
		}
		button {
			padding: %dpx %dpx;
		}
		label {
			font-size: %dpx;
		}
	`, baseFontSize*2, buttonPadding*2, buttonPadding*4, baseFontSize*2))

	screen := mainWindow.GetScreen()
	gtk.AddProviderForScreen(screen, mainCSSProvider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
}

// applyMenuCSS applies retro Office 2003/Delphi 7 style menu styling
// Gutter width matches toolbar button size (32px)
func applyMenuCSS(isDark bool) {
	var css string
	if isDark {
		// Dark theme: gutter gradient on menu background
		css = `
			menu {
				background-image: linear-gradient(to right,
					#505050 0px, #505050 56px,
					#666666 56px, #666666 57px,
					#383838 57px, #383838 100%);
				border: 1px solid #555555;
				padding: 4px 0px 4px 0px;
				margin: 0px;
			}
			menu > * {
				margin: 0px;
				padding-left: 0px;
				padding-right: 0px;
			}
			menuitem {
				padding: 4px 12px 4px 0px;
				margin: 0px;
				background-color: transparent;
			}
			menuitem:hover {
				background-color: #3a4a3a;
				box-shadow: inset 0 0 0 1px #6a9a6a;
			}
			menu separator {
				margin-left: 57px;
				margin-right: 8px;
				margin-top: 4px;
				margin-bottom: 4px;
				background-color: #555555;
				min-height: 1px;
			}
		`
	} else {
		// Light theme: gutter gradient on menu background
		css = `
			menu {
				background-image: linear-gradient(to right,
					#e0e0e0 0px, #e0e0e0 56px,
					#c0c0c0 56px, #c0c0c0 57px,
					#ffffff 57px, #ffffff 100%);
				border: 1px solid #c0c0c0;
				padding: 4px 0px 4px 0px;
				margin: 0px;
			}
			menu > * {
				margin: 0px;
				padding-left: 0px;
				padding-right: 0px;
			}
			menuitem {
				padding: 4px 12px 4px 0px;
				margin: 0px;
				background-color: transparent;
			}
			menuitem:hover {
				background-color: #e5f3ff;
				box-shadow: inset 0 0 0 1px #6699cc;
				color: #000000;
			}
			menu separator {
				margin-left: 57px;
				margin-right: 8px;
				margin-top: 4px;
				margin-bottom: 4px;
				background-color: #c0c0c0;
				min-height: 1px;
			}
		`
	}

	// Create or reuse the CSS provider
	if menuCSSProvider == nil {
		menuCSSProvider, _ = gtk.CssProviderNew()
	}
	menuCSSProvider.LoadFromData(css)

	// Apply to default screen (works even before main window exists)
	screen, err := gdk.ScreenGetDefault()
	if err == nil && screen != nil {
		gtk.AddProviderForScreen(screen, menuCSSProvider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
	}
}

// parseShortcutGTK parses a shortcut string like "Cmd+Shift+Q" into GDK key and modifiers
// Returns ok=false for empty strings (disabled shortcut)
func parseShortcutGTK(shortcut string) (targetKey uint, targetMod gdk.ModifierType, ok bool) {
	if shortcut == "" {
		return 0, 0, false // Empty string = disabled
	}
	parts := strings.Split(shortcut, "+")
	if len(parts) == 0 {
		return 0, 0, false
	}

	// Last part is the key, everything before is modifiers
	keyPart := parts[len(parts)-1]
	modParts := parts[:len(parts)-1]

	// Parse modifiers
	for _, mod := range modParts {
		switch strings.ToLower(strings.TrimSpace(mod)) {
		case "cmd", "meta", "super":
			targetMod |= gdk.META_MASK
		case "ctrl", "control":
			targetMod |= gdk.CONTROL_MASK
		case "alt", "opt", "option":
			targetMod |= gdk.MOD1_MASK
		case "shift":
			targetMod |= gdk.SHIFT_MASK
		}
	}

	// Parse key
	keyPart = strings.TrimSpace(keyPart)
	switch strings.ToUpper(keyPart) {
	case "F1":
		targetKey = gdk.KEY_F1
	case "F2":
		targetKey = gdk.KEY_F2
	case "F3":
		targetKey = gdk.KEY_F3
	case "F4":
		targetKey = gdk.KEY_F4
	case "F5":
		targetKey = gdk.KEY_F5
	case "F6":
		targetKey = gdk.KEY_F6
	case "F7":
		targetKey = gdk.KEY_F7
	case "F8":
		targetKey = gdk.KEY_F8
	case "F9":
		targetKey = gdk.KEY_F9
	case "F10":
		targetKey = gdk.KEY_F10
	case "F11":
		targetKey = gdk.KEY_F11
	case "F12":
		targetKey = gdk.KEY_F12
	case "ESCAPE", "ESC":
		targetKey = gdk.KEY_Escape
	case "TAB":
		targetKey = gdk.KEY_Tab
	case "RETURN", "ENTER":
		targetKey = gdk.KEY_Return
	case "SPACE":
		targetKey = gdk.KEY_space
	case "BACKSPACE":
		targetKey = gdk.KEY_BackSpace
	case "DELETE", "DEL":
		targetKey = gdk.KEY_Delete
	case "INSERT", "INS":
		targetKey = gdk.KEY_Insert
	case "HOME":
		targetKey = gdk.KEY_Home
	case "END":
		targetKey = gdk.KEY_End
	case "PAGEUP", "PGUP":
		targetKey = gdk.KEY_Page_Up
	case "PAGEDOWN", "PGDN":
		targetKey = gdk.KEY_Page_Down
	default:
		// Single character key
		if len(keyPart) == 1 {
			targetKey = uint(gdk.KEY_a) + uint(strings.ToLower(keyPart)[0]-'a')
		} else {
			return 0, 0, false
		}
	}

	return targetKey, targetMod, true
}

// setupShortcutsForWindow configures keyboard shortcuts (quit and close) for a window
func setupShortcutsForWindow(win *gtk.ApplicationWindow) {
	// Parse shortcuts
	quitKey, quitMod, quitOk := parseShortcutGTK(getQuitShortcut())
	closeKey, closeMod, closeOk := parseShortcutGTK(getCloseShortcut())

	if !quitOk && !closeOk {
		return // No shortcuts configured
	}

	// Connect to key-press-event on the window
	win.Connect("key-press-event", func(w *gtk.ApplicationWindow, event *gdk.Event) bool {
		keyEvent := gdk.EventKeyNewFromEvent(event)
		defer runtime.KeepAlive(keyEvent) // Prevent GC during handler

		keyval := keyEvent.KeyVal()
		state := gdk.ModifierType(keyEvent.State())

		// Mask out non-modifier bits (like num lock, caps lock)
		state = state & (gdk.CONTROL_MASK | gdk.SHIFT_MASK | gdk.MOD1_MASK | gdk.META_MASK)

		// Helper to check if a shortcut matches
		matchesShortcut := func(targetKey uint, targetMod gdk.ModifierType) bool {
			if targetKey >= uint(gdk.KEY_a) && targetKey <= uint(gdk.KEY_z) {
				upperKey := targetKey - uint(gdk.KEY_a) + uint(gdk.KEY_A)
				return (keyval == targetKey || keyval == upperKey) && state == targetMod
			}
			return keyval == targetKey && state == targetMod
		}

		// Check quit shortcut
		if quitOk && matchesShortcut(quitKey, quitMod) {
			win.Close()
			return true
		}

		// Check close shortcut
		if closeOk && matchesShortcut(closeKey, closeMod) {
			win.Close()
			return true
		}

		return false
	})
}

// setupQuitShortcutForWindow is an alias for setupShortcutsForWindow for compatibility
func setupQuitShortcutForWindow(win *gtk.ApplicationWindow) {
	setupShortcutsForWindow(win)
}

// setupQuitShortcut configures keyboard shortcuts for the main window
func setupQuitShortcut() {
	setupShortcutsForWindow(mainWindow)
}

func showCopyright() {
	fmt.Fprintf(os.Stderr, "pawgui-gtk, the PawScript GUI interpreter version %s (with GTK)\nCopyright (c) 2025 Jeffrey R. Day\nLicense: MIT\n", version)
}

func showLicense() {
	showCopyright()
	license := `
MIT License

Copyright (c) 2025 Jeffrey R. Day

Permission is hereby granted, free of charge, to any person
obtaining a copy of this software and associated documentation
files (the "Software"), to deal in the Software without
restriction, including without limitation the rights to use,
copy, modify, merge, publish, distribute, sublicense, and/or
sell copies of the Software, and to permit persons to whom the
Software is furnished to do so, subject to the following
conditions:

The above copyright notice and this permission notice
(including the next paragraph) shall be included in all copies
or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES
OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND
NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT
HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY,
WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING
FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
OTHER DEALINGS IN THE SOFTWARE.
`
	fmt.Fprint(os.Stdout, license)
}

func showUsage() {
	showCopyright()
	usage := `
Usage: pawgui-gtk [options] [script.paw] [-- args...]
       pawgui-gtk [options] < input.paw
       echo "commands" | pawgui-gtk [options]

Execute PawScript with GUI capabilities from a file, stdin, or pipe.

Options:
  --version           Show version and exit
  --license           View license and exit
  -d, --debug         Enable debug output
  -v, --verbose       Enable verbose output (same as --debug)
  -O N                Set optimization level (0=no caching, 1=cache macro/loop bodies, default: 1)
  --unrestricted      Disable all file/exec access restrictions
  --sandbox DIR       Restrict all access to DIR only
  --read-roots DIRS   Additional directories for reading
  --write-roots DIRS  Additional directories for writing
  --exec-roots DIRS   Additional directories for exec command

GUI Options:
  --window            Create console window for stdout/stdin/stderr

Arguments:
  script.paw          Script file to execute (adds .paw extension if needed)
  --                  Separates script filename from arguments

Default Security Sandbox:
  Read:   SCRIPT_DIR, CWD, /tmp
  Write:  SCRIPT_DIR/saves, SCRIPT_DIR/output, CWD/saves, CWD/output, /tmp
  Exec:   SCRIPT_DIR/helpers, SCRIPT_DIR/bin

Environment Variables (use SCRIPT_DIR as placeholder):
  PAW_READ_ROOTS      Override default read roots
  PAW_WRITE_ROOTS     Override default write roots
  PAW_EXEC_ROOTS      Override default exec roots
`
	fmt.Fprint(os.Stderr, usage)
}

// findScriptFile looks for a script file, adding .paw extension if needed
func findScriptFile(requestedFile string) string {
	// Try exact path first
	if _, err := os.Stat(requestedFile); err == nil {
		return requestedFile
	}

	// If no extension, try adding .paw
	if !strings.Contains(filepath.Base(requestedFile), ".") {
		pawFile := requestedFile + ".paw"
		if _, err := os.Stat(pawFile); err == nil {
			return pawFile
		}
	}

	return ""
}

func main() {
	// Define command line flags
	licenseFlag := flag.Bool("license", false, "Show license")
	versionFlag := flag.Bool("version", false, "Show version")
	debugFlag := flag.Bool("debug", false, "Enable debug output")
	verboseFlag := flag.Bool("verbose", false, "Enable verbose output (alias for -debug)")
	flag.BoolVar(debugFlag, "d", false, "Enable debug output (short)")
	flag.BoolVar(verboseFlag, "v", false, "Enable verbose output (short, alias for -debug)")

	// File access control flags
	unrestrictedFlag := flag.Bool("unrestricted", false, "Disable all file/exec access restrictions")
	readRootsFlag := flag.String("read-roots", "", "Additional directories for file reading")
	writeRootsFlag := flag.String("write-roots", "", "Additional directories for file writing")
	execRootsFlag := flag.String("exec-roots", "", "Additional directories for exec command")
	sandboxFlag := flag.String("sandbox", "", "Restrict all access to this directory only")

	// Optimization level flag
	optLevelFlag := flag.Int("O", 1, "Optimization level (0=no caching, 1=cache macro/loop bodies)")

	// GUI-specific flags
	windowFlag := flag.Bool("window", false, "Create console window for stdout/stdin/stderr")

	// Custom usage function
	flag.Usage = showUsage

	// Parse flags
	flag.Parse()

	if *versionFlag {
		showCopyright()
		os.Exit(0)
	}

	if *licenseFlag {
		showLicense()
		os.Exit(0)
	}

	// Verbose is an alias for debug
	debug := *debugFlag || *verboseFlag
	_ = debug // Will be used later

	// Get remaining arguments after flags
	args := flag.Args()

	var scriptFile string
	var scriptContent string
	var scriptArgs []string

	// Check for -- separator
	separatorIndex := -1
	for i, arg := range args {
		if arg == "--" {
			separatorIndex = i
			break
		}
	}

	var fileArgs []string
	if separatorIndex != -1 {
		fileArgs = args[:separatorIndex]
		scriptArgs = args[separatorIndex+1:]
	} else {
		fileArgs = args
	}

	// Check if stdin is redirected/piped
	stdinInfo, _ := os.Stdin.Stat()
	isStdinRedirected := (stdinInfo.Mode() & os.ModeCharDevice) == 0

	if len(fileArgs) > 0 {
		// Filename provided
		requestedFile := fileArgs[0]
		foundFile := findScriptFile(requestedFile)

		if foundFile == "" {
			fmt.Fprintf(os.Stderr, "Error: Script file not found: %s\n", requestedFile)
			if !strings.Contains(requestedFile, ".") {
				fmt.Fprintf(os.Stderr, "Also tried: %s.paw\n", requestedFile)
			}
			os.Exit(1)
		}

		scriptFile = foundFile

		content, err := os.ReadFile(scriptFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading script file: %v\n", err)
			os.Exit(1)
		}
		scriptContent = string(content)

		// Remaining fileArgs become script arguments (if no separator was used)
		if separatorIndex == -1 && len(fileArgs) > 1 {
			scriptArgs = fileArgs[1:]
		}

	} else if isStdinRedirected {
		// No filename, but stdin is redirected - read from stdin
		content, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading from stdin: %v\n", err)
			os.Exit(1)
		}
		scriptContent = string(content)
	}

	// If we have script content (from file or stdin), run it
	if scriptContent != "" {
		runScriptFromCLI(scriptContent, scriptFile, scriptArgs, *windowFlag, *unrestrictedFlag,
			*sandboxFlag, *readRootsFlag, *writeRootsFlag, *execRootsFlag, *optLevelFlag)
		return
	}

	// No script provided - launch GUI launcher mode
	gtkApp, err := gtk.ApplicationNew(appID, glib.APPLICATION_FLAGS_NONE)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create application: %v\n", err)
		os.Exit(1)
	}

	gtkApp.Connect("activate", func() {
		activate(gtkApp)
	})

	os.Exit(gtkApp.Run([]string{os.Args[0]})) // Pass only program name to GTK
}

// runScriptFromCLI executes a script with the given options (from command line)
func runScriptFromCLI(scriptContent, scriptFile string, scriptArgs []string, windowFlag bool,
	unrestricted bool, sandbox, readRoots, writeRoots, execRoots string, optLevel int) {

	// Build file access configuration
	var fileAccess *pawscript.FileAccessConfig
	var scriptDir string
	if scriptFile != "" {
		absScript, err := filepath.Abs(scriptFile)
		if err == nil {
			scriptDir = filepath.Dir(absScript)
		}
	}

	if !unrestricted {
		fileAccess = &pawscript.FileAccessConfig{}
		cwd, _ := os.Getwd()
		tmpDir := os.TempDir()

		// Helper to expand SCRIPT_DIR placeholder and resolve path
		expandPath := func(path string) string {
			path = strings.TrimSpace(path)
			if path == "" {
				return ""
			}
			if strings.HasPrefix(path, "SCRIPT_DIR/") {
				if scriptDir != "" {
					path = filepath.Join(scriptDir, path[11:])
				} else {
					return ""
				}
			} else if path == "SCRIPT_DIR" {
				if scriptDir != "" {
					path = scriptDir
				} else {
					return ""
				}
			}
			absPath, err := filepath.Abs(path)
			if err != nil {
				return ""
			}
			return absPath
		}

		// Helper to parse comma-separated roots with SCRIPT_DIR expansion
		parseRoots := func(rootsStr string) []string {
			var roots []string
			for _, root := range strings.Split(rootsStr, ",") {
				if expanded := expandPath(root); expanded != "" {
					roots = append(roots, expanded)
				}
			}
			return roots
		}

		if sandbox != "" {
			absPath, err := filepath.Abs(sandbox)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error resolving sandbox path: %v\n", err)
				os.Exit(1)
			}
			fileAccess.ReadRoots = []string{absPath}
			fileAccess.WriteRoots = []string{absPath}
			fileAccess.ExecRoots = []string{absPath}
		} else {
			// Check environment variables first
			envReadRoots := os.Getenv("PAW_READ_ROOTS")
			envWriteRoots := os.Getenv("PAW_WRITE_ROOTS")
			envExecRoots := os.Getenv("PAW_EXEC_ROOTS")

			if envReadRoots != "" {
				fileAccess.ReadRoots = parseRoots(envReadRoots)
			} else {
				if scriptDir != "" {
					fileAccess.ReadRoots = append(fileAccess.ReadRoots, scriptDir)
				}
				if cwd != "" && cwd != scriptDir {
					fileAccess.ReadRoots = append(fileAccess.ReadRoots, cwd)
				}
				fileAccess.ReadRoots = append(fileAccess.ReadRoots, tmpDir)
			}
			if readRoots != "" {
				fileAccess.ReadRoots = append(fileAccess.ReadRoots, parseRoots(readRoots)...)
			}

			if envWriteRoots != "" {
				fileAccess.WriteRoots = parseRoots(envWriteRoots)
			} else {
				if scriptDir != "" {
					fileAccess.WriteRoots = append(fileAccess.WriteRoots,
						filepath.Join(scriptDir, "saves"),
						filepath.Join(scriptDir, "output"))
				}
				if cwd != "" && cwd != scriptDir {
					fileAccess.WriteRoots = append(fileAccess.WriteRoots,
						filepath.Join(cwd, "saves"),
						filepath.Join(cwd, "output"))
				}
				fileAccess.WriteRoots = append(fileAccess.WriteRoots, tmpDir)
			}
			if writeRoots != "" {
				fileAccess.WriteRoots = append(fileAccess.WriteRoots, parseRoots(writeRoots)...)
			}

			if envExecRoots != "" {
				fileAccess.ExecRoots = parseRoots(envExecRoots)
			} else {
				if scriptDir != "" {
					fileAccess.ExecRoots = append(fileAccess.ExecRoots,
						filepath.Join(scriptDir, "helpers"),
						filepath.Join(scriptDir, "bin"))
				}
			}
			if execRoots != "" {
				fileAccess.ExecRoots = append(fileAccess.ExecRoots, parseRoots(execRoots)...)
			}
		}
	}

	if !windowFlag {
		// No window mode - run like CLI
		ps := pawscript.New(&pawscript.Config{
			Debug:                false,
			AllowMacros:          true,
			EnableSyntacticSugar: true,
			ShowErrorContext:     true,
			ContextLines:         2,
			FileAccess:           fileAccess,
			OptLevel:             pawscript.OptimizationLevel(optLevel),
			ScriptDir:            scriptDir,
		})
		ps.RegisterStandardLibrary(scriptArgs)

		var result pawscript.Result
		if scriptFile != "" {
			result = ps.ExecuteFile(scriptContent, scriptFile)
		} else {
			result = ps.Execute(scriptContent)
		}
		if result == pawscript.BoolStatus(false) {
			os.Exit(1)
		}
		return
	}

	// Window mode - create GTK application with console window
	gtkApp, err := gtk.ApplicationNew(appID, glib.APPLICATION_FLAGS_NONE)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create application: %v\n", err)
		os.Exit(1)
	}

	gtkApp.Connect("activate", func() {
		// Load configuration
		appConfig = loadConfig()
		configHelper = pawgui.NewConfigHelper(appConfig)
		if configHelper.PopulateDefaults() {
			saveConfig(appConfig)
		}
		applyTheme(configHelper.GetTheme())

		// Create console window and run script
		runScriptInWindow(gtkApp, scriptContent, scriptFile, scriptArgs, fileAccess, optLevel, scriptDir)
	})

	gtkApp.Run([]string{os.Args[0]})
}

// runScriptInWindow creates a console window and runs the script
func runScriptInWindow(gtkApp *gtk.Application, scriptContent, scriptFile string, scriptArgs []string,
	fileAccess *pawscript.FileAccessConfig, optLevel int, scriptDir string) {

	// Create a console window
	win, err := gtk.ApplicationWindowNew(gtkApp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create window: %v\n", err)
		return
	}

	title := "PawScript Console"
	if scriptFile != "" {
		title = filepath.Base(scriptFile) + " - PawScript"
	}
	win.SetTitle(title)
	win.SetDefaultSize(900, 600)

	// Set up quit shortcut for this window
	setupQuitShortcutForWindow(win)

	// Create terminal
	winTerminal, err := purfectermgtk.New(purfectermgtk.Options{
		Cols:           100,
		Rows:           30,
		ScrollbackSize: 10000,
		FontFamily:     getFontFamily(),
		FontSize:       getFontSize(),
		Scheme:         getDualColorScheme(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create terminal: %v\n", err)
		win.Destroy()
		return
	}

	// Set font fallbacks
	winTerminal.SetFontFallbacks(getFontFamilyUnicode(), getFontFamilyCJK())

	// Set up terminal theme from config
	prefersDark := isTermThemeDark()
	winTerminal.Buffer().SetPreferredDarkTheme(prefersDark)
	winTerminal.Buffer().SetDarkTheme(prefersDark)

	// Set up theme change callback (for CSI ? 5 h/l escape sequences)
	winTerminal.Buffer().SetThemeChangeCallback(func(isDark bool) {
		glib.IdleAdd(func() {
			winTerminal.SetColorScheme(getColorSchemeForTheme(isDark))
		})
	})

	// Create main layout with collapsible toolbar strip
	paned, _ := gtk.PanedNew(gtk.ORIENTATION_HORIZONTAL)

	// Create MenuContext for this window
	menuCtx := &MenuContext{
		Parent:         win,
		IsScriptWindow: true,
		Terminal:       winTerminal,
		CloseWindow: func() {
			win.Close()
		},
	}

	// Narrow strip for script window (always starts visible, collapsible)
	// Console windows always show strip-only, so use extra left padding
	strip, stripMenuBtn, _ := createToolbarStripWithContext(menuCtx)
	strip.SetMarginStart(2 + narrowOnlyExtraPadding)
	strip.SetSizeRequest(scaledMinNarrowStripWidth(), -1) // Keep original width, margin adds the extra space
	paned.Pack1(strip, false, true)

	// Register the toolbar data for theme updates (even without REPL)
	toolbarDataMu.Lock()
	runScriptToolbarData := &WindowToolbarData{
		strip:      strip,
		menuButton: stripMenuBtn,
		terminal:   winTerminal,
	}
	// Use a unique key (nil is fine since there's no PawScript instance)
	// Use the window pointer as a unique identifier
	toolbarDataByWindow[win] = runScriptToolbarData
	toolbarDataMu.Unlock()

	// Terminal on the right
	termWidget := winTerminal.Widget()
	termWidget.SetVExpand(true)
	termWidget.SetHExpand(true)
	termWidget.SetMarginStart(8) // Spacing from splitter
	paned.Pack2(termWidget, true, false)

	// Set initial strip width and collapse behavior
	// Script windows only have two positions: 0 (collapsed) or visible (with extra padding)
	consoleStripWidth := scaledMinNarrowStripWidth() + narrowOnlyExtraPadding
	paned.SetPosition(consoleStripWidth)

	// Track position changes for drag detection
	var consolePanedPressPos int = -1
	var consolePanedDragged bool
	var consolePanedOnHandle bool

	paned.Connect("notify::position", func() {
		defer runtime.KeepAlive(paned)
		pos := paned.GetPosition()
		// Track dragging
		if consolePanedPressPos >= 0 && consolePanedOnHandle && pos != consolePanedPressPos {
			consolePanedDragged = true
		}
		// Snap behavior
		if pos == 0 {
			// Already collapsed, do nothing
		} else if pos < consoleStripWidth/2 {
			// Less than half - snap to collapsed
			paned.SetPosition(0)
		} else if pos != consoleStripWidth {
			// More than half but not at fixed width - snap to visible
			paned.SetPosition(consoleStripWidth)
		}
	})

	// Handle click events on the console splitter handle only
	paned.Connect("button-press-event", func(p *gtk.Paned, ev *gdk.Event) bool {
		btnEvent := gdk.EventButtonNewFromEvent(ev)
		var handleWindow *gdk.Window
		if btnEvent.Button() == 1 {
			// Check if click is on the handle using handle window geometry
			consolePanedOnHandle = false
			var err error
			handleWindow, err = p.GetHandleWindow()
			if err == nil && handleWindow != nil {
				hx, hy := handleWindow.GetRootOrigin()
				hw := handleWindow.WindowGetWidth()
				hh := handleWindow.WindowGetHeight()
				clickX, clickY := int(btnEvent.XRoot()), int(btnEvent.YRoot())
				consolePanedOnHandle = clickX >= hx && clickX < hx+hw && clickY >= hy && clickY < hy+hh
			}
			if !consolePanedOnHandle {
				runtime.KeepAlive(btnEvent)
				runtime.KeepAlive(handleWindow)
				return false
			}
			consolePanedPressPos = p.GetPosition()
			consolePanedDragged = false
		}
		runtime.KeepAlive(btnEvent)
		runtime.KeepAlive(handleWindow)
		return false
	})

	paned.Connect("button-release-event", func(p *gtk.Paned, ev *gdk.Event) bool {
		btnEvent := gdk.EventButtonNewFromEvent(ev)
		defer runtime.KeepAlive(btnEvent)

		if btnEvent.Button() != 1 || !consolePanedOnHandle || consolePanedDragged || consolePanedPressPos < 0 {
			consolePanedPressPos = -1
			consolePanedOnHandle = false
			return false
		}
		// Single click: toggle between collapsed and narrow mode
		if p.GetPosition() == 0 {
			p.SetPosition(consoleStripWidth)
		} else {
			p.SetPosition(0)
		}
		consolePanedPressPos = -1
		consolePanedOnHandle = false
		return true
	})

	win.Add(paned)
	win.ShowAll()

	// Create I/O channels for this window's console
	stdoutReader, stdoutWriter := io.Pipe()
	winStdinReader, winStdinWriter := io.Pipe()

	// Get terminal capabilities from the widget (auto-updates on resize)
	termCaps := winTerminal.GetTerminalCapabilities()

	// Non-blocking output queue
	outputQueue := make(chan interface{}, 256)
	go func() {
		for item := range outputQueue {
			switch v := item.(type) {
			case []byte:
				stdoutWriter.Write(v)
			case chan struct{}:
				close(v)
			}
		}
	}()

	winOutCh := &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		Terminal:         termCaps,
		NativeSend: func(v interface{}) error {
			var text string
			switch d := v.(type) {
			case []byte:
				text = string(d)
			case string:
				text = d
			default:
				text = fmt.Sprintf("%v", v)
			}
			text = strings.ReplaceAll(text, "\r\n", "\n")
			text = strings.ReplaceAll(text, "\n", "\r\n")
			data := []byte(text)
			select {
			case outputQueue <- data:
			default:
			}
			return nil
		},
		NativeRecv: func() (interface{}, error) {
			return nil, fmt.Errorf("cannot receive from console_out")
		},
		NativeFlush: func() error {
			writerDone := make(chan struct{})
			select {
			case outputQueue <- writerDone:
				<-writerDone
			default:
			}
			glibDone := make(chan struct{})
			glib.IdleAdd(func() bool {
				close(glibDone)
				return false
			})
			select {
			case <-glibDone:
			case <-time.After(100 * time.Millisecond):
			}
			return nil
		},
	}

	// Non-blocking input queue
	inputQueue := make(chan byte, 256)
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := winStdinReader.Read(buf)
			if err != nil || n == 0 {
				close(inputQueue)
				return
			}
			select {
			case inputQueue <- buf[0]:
			default:
				select {
				case <-inputQueue:
				default:
				}
				select {
				case inputQueue <- buf[0]:
				default:
				}
			}
		}
	}()

	winInCh := &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		Terminal:         termCaps,
		NativeRecv: func() (interface{}, error) {
			b, ok := <-inputQueue
			if !ok {
				return nil, fmt.Errorf("input closed")
			}
			return []byte{b}, nil
		},
		NativeSend: func(v interface{}) error {
			return fmt.Errorf("cannot send to console_in")
		},
	}

	// Read from stdout pipe and feed to terminal
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdoutReader.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				glib.IdleAdd(func() bool {
					winTerminal.FeedBytes(data)
					return false
				})
			}
			if err != nil {
				return
			}
		}
	}()

	// Create PawScript interpreter
	ps := pawscript.New(&pawscript.Config{
		Debug:                false,
		AllowMacros:          true,
		EnableSyntacticSugar: true,
		ShowErrorContext:     true,
		ContextLines:         2,
		FileAccess:           fileAccess,
		OptLevel:             pawscript.OptimizationLevel(optLevel),
		ScriptDir:            scriptDir,
	})

	// Register standard library with console channels
	ioConfig := &pawscript.IOChannelConfig{
		Stdout: winOutCh,
		Stdin:  winInCh,
		Stderr: winOutCh,
	}
	ps.RegisterStandardLibraryWithIO(scriptArgs, ioConfig)

	// Handle terminal input
	winTerminal.SetInputCallback(func(data []byte) {
		winStdinWriter.Write(data)
	})

	// Handle window close
	win.Connect("destroy", func() {
		// Clean up toolbar data
		toolbarDataMu.Lock()
		delete(toolbarDataByWindow, win)
		toolbarDataMu.Unlock()
		winStdinWriter.Close()
		gtkApp.Quit()
	})

	// Run script in goroutine
	go func() {
		time.Sleep(100 * time.Millisecond) // Let window initialize

		var result pawscript.Result
		if scriptFile != "" {
			result = ps.ExecuteFile(scriptContent, scriptFile)
		} else {
			result = ps.Execute(scriptContent)
		}

		if winOutCh.NativeFlush != nil {
			winOutCh.NativeFlush()
		}

		if result == pawscript.BoolStatus(false) {
			winTerminal.Feed("\r\n[Script execution failed]\r\n")
		} else {
			winTerminal.Feed("\r\n[Script completed]\r\n")
		}

		// Don't auto-close - let user see output and close manually
	}()
}

func activate(application *gtk.Application) {
	// Store app reference globally for creating new windows
	app = application

	// Load configuration and create config helper
	appConfig = loadConfig()
	configHelper = pawgui.NewConfigHelper(appConfig)

	// Populate defaults using shared package
	if configHelper.PopulateDefaults() {
		saveConfig(appConfig)
	}

	// Apply theme setting
	applyTheme(configHelper.GetTheme())

	// Create main window
	var err error
	mainWindow, err = gtk.ApplicationWindowNew(app)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create window: %v\n", err)
		return
	}
	mainWindow.SetTitle(appName)

	// Get screen dimensions for bounds checking
	display, _ := gdk.DisplayGetDefault()
	monitor, _ := display.GetPrimaryMonitor()
	geometry := monitor.GetGeometry()
	screenWidth := geometry.GetWidth()
	screenHeight := geometry.GetHeight()

	// Load saved size, validate against screen bounds
	savedWidth, savedHeight := getLauncherSize()
	if savedWidth > screenWidth {
		savedWidth = screenWidth
	}
	if savedHeight > screenHeight {
		savedHeight = screenHeight
	}
	if savedWidth < 400 {
		savedWidth = 400
	}
	if savedHeight < 300 {
		savedHeight = 300
	}
	mainWindow.SetDefaultSize(savedWidth, savedHeight)

	// Load saved position, validate to ensure window is on screen
	savedX, savedY := getLauncherPosition()
	if savedX >= 0 && savedY >= 0 {
		// Ensure at least 100px of window is visible on screen
		if savedX > screenWidth-100 {
			savedX = screenWidth - 100
		}
		if savedY > screenHeight-100 {
			savedY = screenHeight - 100
		}
		if savedX < 0 {
			savedX = 0
		}
		if savedY < 0 {
			savedY = 0
		}
		mainWindow.Move(savedX, savedY)
	}

	// Track window position and size changes
	var lastX, lastY, lastWidth, lastHeight int
	mainWindow.Connect("configure-event", func(win *gtk.ApplicationWindow, event *gdk.Event) bool {
		// Get current position and size
		x, y := win.GetPosition()
		w, h := win.GetSize()

		// Save if changed (debounce by checking for actual changes)
		if x != lastX || y != lastY {
			lastX, lastY = x, y
			saveLauncherPosition(x, y)
		}
		if w != lastWidth || h != lastHeight {
			lastWidth, lastHeight = w, h
			saveLauncherSize(w, h)
		}
		return false // Continue event propagation
	})

	// Apply CSS for UI scaling (base size 10px, scaled by ui_scale config)
	// GTK uses 0.8x the config scale to match visual appearance with Qt
	uiScale := getUIScale() * 0.8
	baseFontSize := int(10.0 * uiScale)
	buttonPadding := int(6.0 * uiScale)
	cssProvider, _ := gtk.CssProviderNew()
	cssProvider.LoadFromData(fmt.Sprintf(`
		* {
			font-size: %dpx;
		}
		button {
			padding: %dpx %dpx;
		}
		label {
			font-size: %dpx;
		}
	`, baseFontSize*2, buttonPadding*2, buttonPadding*4, baseFontSize*2))
	screen := mainWindow.GetScreen()
	gtk.AddProviderForScreen(screen, cssProvider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)

	// Set up quit shortcut via key-press-event handler
	setupQuitShortcut()

	// Create context menu for terminal (right-click)
	contextMenu = createLauncherContextMenu()

	// Create main vertical box for content (no menu bar)
	mainBox, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)

	// Note: Alt+F4 and other quit/close shortcuts are now handled by setupShortcutsForWindow
	// which is called via setupQuitShortcut() below

	// Create main horizontal paned (split view)
	launcherPaned, _ = gtk.PanedNew(gtk.ORIENTATION_HORIZONTAL)
	// Position will be set after ShowAll based on saved width and button state

	// Left panel container: holds wide panel (file browser) and narrow strip side by side
	leftContainer, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)

	// Create MenuContext for the launcher window first (before createFileBrowser which uses it)
	launcherMenuCtx = &MenuContext{
		Parent:         mainWindow,
		IsScriptWindow: false,
		Terminal:       terminal, // Main launcher terminal
		IsScriptRunning: func() bool {
			scriptMu.Lock()
			defer scriptMu.Unlock()
			return scriptRunning
		},
		IsFileListWide: func() bool {
			// Wide if position >= bothThreshold (file list panel visible)
			return launcherPaned.GetPosition() >= scaledBothThreshold()
		},
		ToggleFileList: func() {
			pos := launcherPaned.GetPosition()
			bothThreshold := scaledBothThreshold()
			narrowWidth := scaledMinNarrowStripWidth()
			narrowOnlyWidth := narrowWidth + narrowOnlyExtraPadding
			hasMultipleButtons := len(launcherRegisteredBtns) > 0
			if pos >= bothThreshold {
				// Currently wide, collapse to narrow-only strip
				// Hide wide panel and show narrow strip BEFORE setting position
				launcherWidePanel.Hide()
				launcherNarrowStrip.SetMarginStart(2 + narrowOnlyExtraPadding)
				launcherNarrowStrip.Show()
				launcherMenuButton.Hide()
				launcherStripMenuBtn.Show()
				launcherPaned.SetPosition(narrowOnlyWidth)
				saveLauncherWidth(narrowOnlyWidth)
			} else {
				// Currently narrow or collapsed, expand to wide
				savedWidth := getLauncherWidth()
				// Show wide panel BEFORE setting position
				launcherWidePanel.Show()
				launcherNarrowStrip.SetMarginStart(2) // Normal padding in wide mode
				wideThreshold := scaledMinWidePanelWidth()
				if hasMultipleButtons {
					launcherNarrowStrip.Show()
					launcherMenuButton.Hide()
					launcherStripMenuBtn.Show()
					// Enforce minimum threshold like the drag handler does
					launcherPaned.SetPosition(max(savedWidth+narrowWidth, wideThreshold+narrowWidth))
					saveLauncherWidth(savedWidth)
				} else {
					launcherNarrowStrip.Hide()
					launcherMenuButton.Show()
					// Enforce minimum threshold like the drag handler does
					launcherPaned.SetPosition(max(savedWidth, wideThreshold))
					saveLauncherWidth(savedWidth)
				}
			}
		},
		CloseWindow: func() {
			mainWindow.Close()
		},
	}

	// Create shared hamburger menu for launcher (used by both wide panel and narrow strip buttons)
	launcherMenu = createHamburgerMenu(launcherMenuCtx)

	// Wide panel: File browser (uses shared launcherMenu)
	widePanel := createFileBrowser()
	leftContainer.PackStart(widePanel, true, true, 0)

	// Narrow strip: toolbar buttons (created but hidden initially - only 1 button)
	// Uses getter to always get current launcherMenu (allows menu to be rebuilt on UI scale change)
	launcherNarrowStrip, launcherStripMenuBtn, _ = createToolbarStripWithMenuGetter(launcherMenuCtx, func() *gtk.Menu { return launcherMenu })
	launcherNarrowStrip.SetNoShowAll(true)                            // Don't show when ShowAll is called
	launcherNarrowStrip.SetSizeRequest(scaledMinNarrowStripWidth(), -1) // Fixed width
	leftContainer.PackStart(launcherNarrowStrip, false, false, 0)

	// Initially: hamburger button visible in path selector, narrow strip hidden
	// (since we only have 1 button registered by default)
	launcherMenuButton.Show()
	launcherNarrowStrip.Hide()

	// Pack1(widget, resize, shrink): resize=false (fixed), shrink=true (can collapse)
	launcherPaned.Pack1(leftContainer, false, true)

	// Right panel: Terminal (with left margin for spacing from divider)
	rightPanel := createTerminal()
	rightPanel.SetMarginStart(8) // 8 pixel spacer from divider
	launcherPaned.Pack2(rightPanel, true, false)

	// Update launcher menu context with the terminal (needed for Save Scrollback/Restore Buffer)
	if launcherMenuCtx != nil {
		launcherMenuCtx.Terminal = terminal
	}

	// Save launcher width when user adjusts the splitter
	// Implement multi-stage collapse:
	// - Wide + narrow mode: when pos >= minWidePanelWidth + minNarrowStripWidth
	// - Narrow only mode: when pos >= minNarrowStripWidth but < threshold for wide panel
	// - Collapsed: when pos < halfway point of narrow strip
	var lastPanedPos int = -1
	launcherPaned.Connect("notify::position", func() {
		pos := launcherPaned.GetPosition()

		// Skip if position hasn't actually changed (reduces GC pressure during rapid events)
		if pos == lastPanedPos {
			return
		}
		lastPanedPos = pos

		hasMultipleButtons := len(launcherRegisteredBtns) > 0

		// Calculate scaled thresholds
		narrowWidth := scaledMinNarrowStripWidth()
		wideThreshold := scaledMinWidePanelWidth()
		bothThreshold := scaledBothThreshold()
		narrowSnapPoint := scaledNarrowSnapPoint()
		narrowOnlyWidth := narrowWidth + narrowOnlyExtraPadding

		// Keep widget references alive during this handler to prevent GC issues
		defer func() {
			runtime.KeepAlive(launcherWidePanel)
			runtime.KeepAlive(launcherNarrowStrip)
			runtime.KeepAlive(launcherMenuButton)
			runtime.KeepAlive(launcherStripMenuBtn)
			runtime.KeepAlive(launcherPaned)
		}()

		if pos == 0 {
			// Fully collapsed - hide all left panels, show hamburger in path selector
			launcherWidePanel.Hide()
			launcherNarrowStrip.Hide()
			launcherMenuButton.Show()
			saveLauncherWidth(pos)
		} else if pos < narrowSnapPoint {
			// Too narrow even for strip - snap to collapsed
			launcherPaned.SetPosition(0)
		} else if pos < bothThreshold {
			// Between narrow snap point and both-panels threshold
			// Show only narrow strip at its fixed width with extra left padding
			launcherWidePanel.Hide()
			launcherNarrowStrip.SetMarginStart(2 + narrowOnlyExtraPadding) // Extra padding in narrow-only mode
			launcherNarrowStrip.Show()
			launcherMenuButton.Hide()
			launcherStripMenuBtn.Show()
			// Snap to narrow strip width plus extra padding
			if pos != narrowOnlyWidth {
				launcherPaned.SetPosition(narrowOnlyWidth)
			}
			saveLauncherWidth(narrowOnlyWidth)
		} else {
			// Wide enough for full panel
			launcherWidePanel.Show()
			if hasMultipleButtons {
				launcherPaned.SetPosition(max(pos, wideThreshold+narrowWidth))
			} else {
				launcherPaned.SetPosition(max(pos, wideThreshold))
			}
			launcherNarrowStrip.SetMarginStart(2) // Normal padding in wide mode
			if hasMultipleButtons {
				// Multiple buttons: show narrow strip, hide menu button in path row
				launcherNarrowStrip.Show()
				launcherMenuButton.Hide()
				launcherStripMenuBtn.Show()
				// Save only the wide panel width (subtract strip width)
				saveLauncherWidth(launcherPaned.GetPosition() - narrowWidth)
			} else {
				// Single button: hide narrow strip, show menu button in path row
				launcherNarrowStrip.Hide()
				launcherMenuButton.Show()
				saveLauncherWidth(launcherPaned.GetPosition())
			}
		}
	})

	// Handle click events on the splitter handle only
	// Track if the position changed during a press-release cycle (drag vs click)
	var launcherPanedPressPos int = -1
	var launcherPanedDragged bool
	var launcherPanedDoubleClick bool
	var launcherPanedOnHandle bool

	launcherPaned.Connect("button-press-event", func(paned *gtk.Paned, ev *gdk.Event) bool {
		btnEvent := gdk.EventButtonNewFromEvent(ev)
		var handleWindow *gdk.Window
		if btnEvent.Button() == 1 { // Left mouse button
			// Check if click is on the handle using handle window geometry
			launcherPanedOnHandle = false
			var err error
			handleWindow, err = paned.GetHandleWindow()
			if err == nil && handleWindow != nil {
				hx, hy := handleWindow.GetRootOrigin()
				hw := handleWindow.WindowGetWidth()
				hh := handleWindow.WindowGetHeight()
				clickX, clickY := int(btnEvent.XRoot()), int(btnEvent.YRoot())
				launcherPanedOnHandle = clickX >= hx && clickX < hx+hw && clickY >= hy && clickY < hy+hh
			}

			if !launcherPanedOnHandle {
				runtime.KeepAlive(btnEvent)
				runtime.KeepAlive(handleWindow)
				return false // Not on handle, let event propagate normally
			}

			// Check for double-click (detected on press in GTK)
			if btnEvent.Type() == gdk.EVENT_2BUTTON_PRESS {
				launcherPanedDoubleClick = true
				// Double-click: collapse completely
				paned.SetPosition(0)
				runtime.KeepAlive(btnEvent)
				runtime.KeepAlive(handleWindow)
				return true
			}
			launcherPanedPressPos = paned.GetPosition()
			launcherPanedDragged = false
			launcherPanedDoubleClick = false
		}
		runtime.KeepAlive(btnEvent)
		runtime.KeepAlive(handleWindow)
		return false // Let GTK handle the event too
	})

	// Track position changes during drag (additional handler)
	launcherPaned.Connect("notify::position", func() {
		defer runtime.KeepAlive(launcherPaned)
		if launcherPanedPressPos >= 0 && launcherPanedOnHandle {
			currentPos := launcherPaned.GetPosition()
			if currentPos != launcherPanedPressPos {
				launcherPanedDragged = true
			}
		}
	})

	launcherPaned.Connect("button-release-event", func(paned *gtk.Paned, ev *gdk.Event) bool {
		btnEvent := gdk.EventButtonNewFromEvent(ev)
		defer runtime.KeepAlive(btnEvent)

		if btnEvent.Button() != 1 { // Only handle left mouse button
			return false
		}

		// If not on handle, ignore
		if !launcherPanedOnHandle {
			return false
		}

		// If it was a double-click, already handled in press
		if launcherPanedDoubleClick {
			launcherPanedDoubleClick = false
			launcherPanedPressPos = -1
			launcherPanedOnHandle = false
			return true
		}

		// If dragged, don't treat as click
		if launcherPanedDragged {
			launcherPanedPressPos = -1
			launcherPanedOnHandle = false
			return false
		}

		// If no press was recorded, ignore
		if launcherPanedPressPos < 0 {
			launcherPanedOnHandle = false
			return false
		}

		pos := paned.GetPosition()
		hasMultipleButtons := len(launcherRegisteredBtns) > 0
		narrowWidth := scaledMinNarrowStripWidth()
		narrowOnlyWidth := narrowWidth + narrowOnlyExtraPadding

		// Single click behavior based on current state
		if pos == 0 {
			// Collapsed -> narrow mode
			paned.SetPosition(narrowOnlyWidth)
		} else if pos <= narrowOnlyWidth {
			// Narrow mode -> wide mode
			savedWidth := getLauncherWidth()
			if savedWidth <= narrowOnlyWidth {
				savedWidth = int(250 * getUIScale()) // Default wide width
			}
			if hasMultipleButtons {
				paned.SetPosition(savedWidth + narrowWidth)
			} else {
				paned.SetPosition(savedWidth)
			}
		} else {
			// Wide mode -> narrow mode
			paned.SetPosition(narrowOnlyWidth)
		}

		launcherPanedPressPos = -1
		launcherPanedOnHandle = false
		return true
	})

	mainBox.PackStart(launcherPaned, true, true, 0)
	mainWindow.Add(mainBox)

	// Load initial directory
	currentDir = getDefaultDir()
	refreshFileList()
	updatePathMenu()

	// Print welcome banner
	terminal.Feed(fmt.Sprintf("pawgui-gtk, the PawScript GUI interpreter version %s (with GTK)\r\n", version))
	terminal.Feed("Copyright (c) 2025 Jeffrey R. Day\r\n")
	terminal.Feed("License: MIT\r\n\r\n")
	terminal.Feed("Interactive mode. Type 'exit' or 'quit' to leave.\r\n")
	terminal.Feed("Select a .paw file and click Run to execute.\r\n\r\n")

	mainWindow.ShowAll()

	// Apply correct UI state and position based on saved position
	// Note: savedPos represents only the wide panel width (not including strip)
	// When in both mode with buttons, we add strip width to get actual position
	savedPos := getLauncherWidth()
	hasMultipleButtons := len(launcherRegisteredBtns) > 0
	narrowWidth := scaledMinNarrowStripWidth()
	narrowOnlyWidth := narrowWidth + narrowOnlyExtraPadding

	if savedPos == 0 {
		// Fully collapsed
		launcherPaned.SetPosition(0)
	} else if savedPos == narrowOnlyWidth {
		// Narrow-only mode: hide wide panel, show narrow strip with extra padding
		launcherPaned.SetPosition(narrowOnlyWidth)
		launcherWidePanel.Hide()
		launcherNarrowStrip.SetMarginStart(2 + narrowOnlyExtraPadding)
		launcherNarrowStrip.Show()
		launcherMenuButton.Hide()
		launcherStripMenuBtn.Show()
	} else {
		// Wide mode - add strip width if buttons exist
		actualPos := savedPos
		if hasMultipleButtons {
			actualPos = savedPos + narrowWidth
		}
		launcherPaned.SetPosition(actualPos)
		launcherWidePanel.Show()
		launcherNarrowStrip.SetMarginStart(2) // Normal padding
		if hasMultipleButtons {
			launcherNarrowStrip.Show()
			launcherMenuButton.Hide()
			launcherStripMenuBtn.Show()
		} else {
			launcherNarrowStrip.Hide()
			launcherMenuButton.Show()
		}
	}

	// Focus the Run button
	runButton.GrabFocus()
}

func getDefaultDir() string {
	// First try saved last_browse_dir from config
	if appConfig != nil {
		savedDir := appConfig.GetString("last_browse_dir", "")
		if savedDir != "" {
			if info, err := os.Stat(savedDir); err == nil && info.IsDir() {
				return savedDir
			}
		}
	}

	// Try to find examples directory relative to executable
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		examples := filepath.Join(exeDir, "examples")
		if info, err := os.Stat(examples); err == nil && info.IsDir() {
			return examples
		}
	}

	// Fall back to current directory
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

func createFileBrowser() *gtk.Box {
	box, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 5)
	box.SetMarginStart(5)
	box.SetMarginEnd(5)
	box.SetMarginTop(5)
	box.SetMarginBottom(5)

	// Store reference for collapse handling
	launcherWidePanel = box

	// Top row: path selector + hamburger menu button
	topRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)

	// Path selector button with dropdown menu - ellipsizes at start to show end of path
	pathButton, _ = gtk.MenuButtonNew()
	pathButton.SetSizeRequest(0, -1)
	pathButton.SetHExpand(true)

	// Create label with ellipsis at start (shows end of path)
	pathLabel, _ = gtk.LabelNew(currentDir)
	pathLabel.SetEllipsize(pango.ELLIPSIZE_START)
	pathLabel.SetXAlign(0)
	pathLabel.SetHExpand(true)
	pathButton.Add(pathLabel)

	// Create the dropdown menu
	pathMenu, _ = gtk.MenuNew()
	pathButton.SetPopup(pathMenu)

	topRow.PackStart(pathButton, true, true, 0)

	// Hamburger menu button (shown when narrow strip is hidden)
	// Uses getter to always get current launcherMenu (allows menu to be rebuilt on UI scale change)
	launcherMenuButton = createHamburgerButton(func() *gtk.Menu { return launcherMenu }, false) // false = horizontal row
	topRow.PackStart(launcherMenuButton, false, false, 0)

	box.PackStart(topRow, false, true, 0)

	// Scrolled window for file list
	scroll, _ := gtk.ScrolledWindowNew(nil, nil)
	scroll.SetPolicy(gtk.POLICY_AUTOMATIC, gtk.POLICY_AUTOMATIC)
	scroll.SetVExpand(true)

	// File list
	fileList, _ = gtk.ListBoxNew()
	fileList.SetSelectionMode(gtk.SELECTION_SINGLE)
	fileList.SetActivateOnSingleClick(false)
	fileList.Connect("row-activated", onFileActivated)
	fileList.Connect("row-selected", onRowSelected)
	scroll.Add(fileList)
	box.PackStart(scroll, true, true, 0)

	// Button box
	buttonBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 5)

	runButton, _ = gtk.ButtonNewWithLabel("Run")
	runButton.Connect("clicked", onRunClicked)
	runButton.SetHExpand(true)
	buttonBox.PackStart(runButton, true, true, 0)

	browseButton, _ := gtk.ButtonNewWithLabel("Browse...")
	browseButton.Connect("clicked", onBrowseClicked)
	browseButton.SetHExpand(true)
	buttonBox.PackStart(browseButton, true, true, 0)

	box.PackStart(buttonBox, false, false, 0)

	return box
}

func createTerminal() *gtk.Box {
	box, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)

	// Create terminal with gtkterm package using config settings
	var err error
	terminal, err = purfectermgtk.New(purfectermgtk.Options{
		Cols:           100,
		Rows:           30,
		ScrollbackSize: 10000,
		FontFamily:     getFontFamily(),
		FontSize:       getFontSize(),
		Scheme:         getDualColorScheme(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create terminal: %v\n", err)
		// Create a placeholder label
		errLabel, _ := gtk.LabelNew(fmt.Sprintf("Terminal creation failed: %v", err))
		box.PackStart(errLabel, true, true, 0)
		return box
	}

	// Set font fallbacks for Unicode/CJK characters
	terminal.SetFontFallbacks(getFontFamilyUnicode(), getFontFamilyCJK())

	// Set up terminal theme from config
	prefersDark := isTermThemeDark()
	terminal.Buffer().SetPreferredDarkTheme(prefersDark)
	terminal.Buffer().SetDarkTheme(prefersDark)

	// Set up theme change callback (for CSI ? 5 h/l escape sequences)
	terminal.Buffer().SetThemeChangeCallback(func(isDark bool) {
		glib.IdleAdd(func() {
			terminal.SetColorScheme(getColorSchemeForTheme(isDark))
		})
	})

	// Add terminal widget to box
	termWidget := terminal.Widget()
	termWidget.SetVExpand(true)
	termWidget.SetHExpand(true)
	box.PackStart(termWidget, true, true, 0)

	// Connect right-click for context menu
	termWidget.Connect("button-press-event", func(widget *gtk.Box, ev *gdk.Event) bool {
		btn := gdk.EventButtonNewFromEvent(ev)
		if btn.Button() == 3 { // Right mouse button
			if contextMenu != nil {
				contextMenu.PopupAtPointer(ev)
			}
			return true
		}
		return false
	})

	// Create console channels for PawScript I/O
	createConsoleChannels()

	return box
}

func updatePathMenu() {
	if pathLabel == nil || pathMenu == nil {
		return
	}

	// Update the label to show current path
	pathLabel.SetText(currentDir)

	// Clear existing menu items safely
	safeRemoveChildren(pathMenu)

	// Helper to add a menu item with callback
	addMenuItem := func(label string, callback func()) {
		item := createMenuItemWithGutter(label, callback)
		pathMenu.Append(item)
	}

	// Helper to add a menu item with icon
	addIconMenuItem := func(svgTemplate string, label string, callback func()) {
		item := createMenuItemWithIcon(svgTemplate, label, callback)
		pathMenu.Append(item)
	}

	// Helper to add a separator
	addSeparator := func() {
		sep, _ := gtk.SeparatorMenuItemNew()
		pathMenu.Append(sep)
	}

	// Add current path (just shows where we are)
	currentItem := createMenuItemWithGutter(currentDir, nil)
	currentItem.SetSensitive(false)
	pathMenu.Append(currentItem)

	addSeparator()

	// Add Home directory
	if home := getHomeDir(); home != "" {
		addIconMenuItem(homeIconSVG, "Home", func() {
			if info, err := os.Stat(home); err == nil && info.IsDir() {
				currentDir = home
				refreshFileList()
				updatePathMenu()
			}
		})
	}

	// Add Examples directory
	if examples := getExamplesDir(); examples != "" {
		addIconMenuItem(folderIconSVG, "Examples", func() {
			if info, err := os.Stat(examples); err == nil && info.IsDir() {
				currentDir = examples
				refreshFileList()
				updatePathMenu()
			}
		})
	}

	// Add recent paths
	recentPaths := getRecentPaths()
	if len(recentPaths) > 0 {
		addSeparator()
		for _, p := range recentPaths {
			path := p // Capture for closure
			addMenuItem(path, func() {
				if info, err := os.Stat(path); err == nil && info.IsDir() {
					currentDir = path
					refreshFileList()
					updatePathMenu()
				}
			})
		}
	}

	// Add Clear Recent Paths option
	if len(recentPaths) > 0 {
		addSeparator()
		addIconMenuItem(trashIconSVG, "Clear Recent Paths", func() {
			clearRecentPaths()
			updatePathMenu()
		})
	}

	pathMenu.ShowAll()
}

func refreshFileList() {
	if fileList == nil {
		return
	}

	// Clear icon type map and reset previous selected row
	rowIconTypeMap = make(map[*gtk.ListBoxRow]gtkIconType)
	previousSelectedRow = nil

	// Safely remove all existing items
	safeRemoveChildren(fileList)

	// Read directory
	entries, err := os.ReadDir(currentDir)
	if err != nil {
		terminal.Feed(fmt.Sprintf("Error reading directory: %v\r\n", err))
		return
	}

	// Add parent directory entry
	if currentDir != "/" {
		row := createFileRow("..", true, true)
		fileList.Add(row)
	}

	// Add directories first
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			row := createFileRow(entry.Name(), true, false)
			fileList.Add(row)
		}
	}

	// Add .paw files
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".paw") {
			row := createFileRow(entry.Name(), false, false)
			fileList.Add(row)
		}
	}

	fileList.ShowAll()
}

func createFileRow(name string, isDir bool, isParent bool) *gtk.ListBoxRow {
	row, _ := gtk.ListBoxRowNew()

	box, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 5)
	box.SetMarginStart(5)
	box.SetMarginEnd(5)
	box.SetMarginTop(2)
	box.SetMarginBottom(2)

	// Determine icon type and SVG template
	var svgTemplate string
	var iconType gtkIconType
	if isParent {
		svgTemplate = folderUpIconSVG
		iconType = gtkIconTypeFolderUp
	} else if isDir {
		svgTemplate = folderIconSVG
		iconType = gtkIconTypeFolder
	} else {
		svgTemplate = pawFileIconSVG
		iconType = gtkIconTypePawFile
	}

	// Store icon type for later icon updates
	rowIconTypeMap[row] = iconType

	// Get themed SVG (applies {{FILL}} replacement for theme-aware icons)
	svgData := getSVGIcon(svgTemplate)
	if icon := createImageFromSVG(svgData, scaledFileListIconSize()); icon != nil {
		box.PackStart(icon, false, false, 0)
	}

	// Name label
	label, _ := gtk.LabelNew(name)
	label.SetXAlign(0)
	label.SetHExpand(true)
	box.PackStart(label, true, true, 0)

	row.Add(box)
	row.SetName(name)

	return row
}

func onFileActivated(listbox *gtk.ListBox, row *gtk.ListBoxRow) {
	name, _ := row.GetName()
	handleFileSelection(name)
}

func onRowSelected(listbox *gtk.ListBox, row *gtk.ListBoxRow) {
	// Restore previous row's icon to normal theme
	if previousSelectedRow != nil {
		updateRowIcon(previousSelectedRow, false)
	}

	// Update current row's icon to dark mode (white fill for selected row)
	if row != nil {
		updateRowIcon(row, true)
		previousSelectedRow = row
	} else {
		previousSelectedRow = nil
	}

	if row == nil || runButton == nil {
		return
	}
	name, _ := row.GetName()
	fullPath := filepath.Join(currentDir, name)

	// Check if it's a directory (including ".." parent)
	if name == ".." {
		runButton.SetLabel("Open")
		return
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		runButton.SetLabel("Run")
		return
	}

	if info.IsDir() {
		runButton.SetLabel("Open")
	} else {
		runButton.SetLabel("Run")
	}
}

func onRunClicked() {
	row := fileList.GetSelectedRow()
	if row == nil {
		terminal.Feed("No file selected.\r\n")
		return
	}

	name, _ := row.GetName()
	handleFileSelection(name)
}

func handleFileSelection(name string) {
	fullPath := filepath.Join(currentDir, name)

	info, err := os.Stat(fullPath)
	if err != nil {
		terminal.Feed(fmt.Sprintf("Error: %v\r\n", err))
		return
	}

	if info.IsDir() {
		// Navigate to directory
		if name == ".." {
			currentDir = filepath.Dir(currentDir)
		} else {
			currentDir = fullPath
		}
		refreshFileList()
		updatePathMenu()
		// Save the new directory to config
		saveBrowseDir(currentDir)
	} else {
		// Run the script
		runScript(fullPath)
	}
}

func onBrowseClicked() {
	// Use sqweek/dialog for native file open dialog
	file, err := dialog.File().
		Title("Open PawScript File").
		Filter("PawScript files", "paw").
		Filter("All files", "*").
		SetStartDir(currentDir).
		Load()
	if err == nil && file != "" {
		// Navigate to the file's directory and run the script
		currentDir = filepath.Dir(file)
		refreshFileList()
		updatePathMenu()
		// Save the new directory to config
		saveBrowseDir(currentDir)
		runScript(file)
	}
}

func runScript(filePath string) {
	scriptMu.Lock()
	if scriptRunning {
		scriptMu.Unlock()
		// Script already running in main window - spawn a new console window
		createConsoleWindow(filePath)
		return
	}
	scriptRunning = true
	scriptMu.Unlock()

	// Stop the REPL while script runs
	if consoleREPL != nil {
		consoleREPL.Stop()
	}

	terminal.Feed(fmt.Sprintf("\r\n--- Running: %s ---\r\n\r\n", filepath.Base(filePath)))

	// Clear any buffered input from previous script runs
	if clearInputFunc != nil {
		clearInputFunc()
	}

	// Read script content
	content, err := os.ReadFile(filePath)
	if err != nil {
		terminal.Feed(fmt.Sprintf("Error reading script file: %v\r\n", err))
		scriptMu.Lock()
		scriptRunning = false
		scriptMu.Unlock()
		return
	}

	scriptDir := filepath.Dir(filePath)
	absScript, _ := filepath.Abs(filePath)
	if absScript != "" {
		scriptDir = filepath.Dir(absScript)
	}

	// Add the script's directory to recent paths for the combo box
	addRecentPath(scriptDir)

	// Create file access config
	cwd, _ := os.Getwd()
	tmpDir := os.TempDir()
	fileAccess := &pawscript.FileAccessConfig{
		ReadRoots:  []string{scriptDir, cwd, tmpDir},
		WriteRoots: []string{filepath.Join(scriptDir, "saves"), filepath.Join(scriptDir, "output"), filepath.Join(cwd, "saves"), filepath.Join(cwd, "output"), tmpDir},
		ExecRoots:  []string{filepath.Join(scriptDir, "helpers"), filepath.Join(scriptDir, "bin")},
	}

	// Create a new PawScript instance for this script
	ps := pawscript.New(&pawscript.Config{
		Debug:                false,
		AllowMacros:          true,
		EnableSyntacticSugar: true,
		ShowErrorContext:     true,
		ContextLines:         2,
		FileAccess:           fileAccess,
		ScriptDir:            scriptDir,
		OptLevel:             pawscript.OptimizationLevel(getOptimizationLevel()),
	})

	// Register standard library with the console IO
	ioConfig := &pawscript.IOChannelConfig{
		Stdout: consoleOutCh,
		Stdin:  consoleInCh,
		Stderr: consoleOutCh,
	}
	ps.RegisterStandardLibraryWithIO([]string{}, ioConfig)

	// Run script in goroutine so UI stays responsive
	go func() {
		// Create an isolated snapshot for execution
		snapshot := ps.CreateRestrictedSnapshot()

		// Run the script in the isolated environment
		result := ps.ExecuteWithEnvironment(string(content), snapshot, filePath, 0, 0)

		// Flush any pending output before printing completion message
		if flushFunc != nil {
			flushFunc()
		}

		if result == pawscript.BoolStatus(false) {
			terminal.Feed("\r\n--- Script execution failed ---\r\n")
		} else {
			terminal.Feed("\r\n--- Script completed ---\r\n")
		}

		scriptMu.Lock()
		scriptRunning = false
		scriptMu.Unlock()

		// Restart the REPL
		if consoleREPL != nil {
			// Create a new REPL instance (fresh state)
			consoleREPL = pawscript.NewREPL(pawscript.REPLConfig{
				Debug:        false,
				Unrestricted: false,
				OptLevel:     getOptimizationLevel(),
				ShowBanner:   false, // Don't show banner again
				IOConfig: &pawscript.IOChannelConfig{
					Stdout: consoleOutCh,
					Stdin:  consoleInCh,
					Stderr: consoleOutCh,
				},
			}, func(s string) {
				glib.IdleAdd(func() bool {
					terminal.Feed(s)
					return false
				})
			})
			// Set flush callback to ensure output appears before blocking execution
			consoleREPL.SetFlush(func() {
				// Process pending GTK events to ensure output is displayed
				// We're on the main thread, so glib.IdleAdd won't work (we'd be waiting for ourselves)
				// Instead, use MainIterationDo to process pending events synchronously
				for i := 0; i < 10 && gtk.EventsPending(); i++ {
					gtk.MainIterationDo(false)
				}
			})
			// Set background color for prompt color selection
			bg := getTerminalBackground()
			consoleREPL.SetBackgroundRGB(bg.R, bg.G, bg.B)
			consoleREPL.SetPSLColors(getPSLColors())
			consoleREPL.Start()

			// Re-register the dummy_button command with the new REPL instance
			// Reuse the existing launcherToolbarData with the new terminal reference
			launcherToolbarData.terminal = terminal
			registerDummyButtonCommand(consoleREPL.GetPawScript(), launcherToolbarData)
		}
	}()
}

// createConsoleWindow creates a new window with just a terminal (no launcher UI)
// for running a script when the main window already has a script running
func createConsoleWindow(filePath string) {
	// Create new window
	win, err := gtk.ApplicationWindowNew(app)
	if err != nil {
		terminal.Feed(fmt.Sprintf("Failed to create console window: %v\r\n", err))
		return
	}
	win.SetTitle(fmt.Sprintf("PawScript - %s", filepath.Base(filePath)))
	win.SetDefaultSize(900, 600)

	// Set up quit shortcut for this window
	setupQuitShortcutForWindow(win)

	// Create terminal for this window
	winTerminal, err := purfectermgtk.New(purfectermgtk.Options{
		Cols:           100,
		Rows:           30,
		ScrollbackSize: 10000,
		FontFamily:     getFontFamily(),
		FontSize:       getFontSize(),
		Scheme:         getDualColorScheme(),
	})
	if err != nil {
		terminal.Feed(fmt.Sprintf("Failed to create terminal: %v\r\n", err))
		win.Destroy()
		return
	}

	// Set font fallbacks for Unicode/CJK characters
	winTerminal.SetFontFallbacks(getFontFamilyUnicode(), getFontFamilyCJK())

	// Set up terminal theme from config
	prefersDark := isTermThemeDark()
	winTerminal.Buffer().SetPreferredDarkTheme(prefersDark)
	winTerminal.Buffer().SetDarkTheme(prefersDark)

	// Set up theme change callback (for CSI ? 5 h/l escape sequences)
	winTerminal.Buffer().SetThemeChangeCallback(func(isDark bool) {
		glib.IdleAdd(func() {
			winTerminal.SetColorScheme(getColorSchemeForTheme(isDark))
		})
	})

	// Create main layout with collapsible toolbar strip
	paned, _ := gtk.PanedNew(gtk.ORIENTATION_HORIZONTAL)

	// Track script running state for this window
	var winScriptRunning bool
	var winScriptMu sync.Mutex

	// Create MenuContext for this console window
	consoleMenuCtx := &MenuContext{
		Parent:         win,
		IsScriptWindow: true,
		Terminal:       winTerminal,
		IsScriptRunning: func() bool {
			winScriptMu.Lock()
			defer winScriptMu.Unlock()
			return winScriptRunning
		},
		CloseWindow: func() {
			win.Close()
		},
	}

	// Narrow strip for script window (always starts visible, collapsible)
	// Console windows always show strip-only, so use extra left padding
	strip, stripMenuBtn, _ := createToolbarStripWithContext(consoleMenuCtx)
	strip.SetMarginStart(2 + narrowOnlyExtraPadding)
	strip.SetSizeRequest(scaledMinNarrowStripWidth(), -1) // Keep original width, margin adds the extra space
	paned.Pack1(strip, false, true)

	// Terminal on the right
	termWidget := winTerminal.Widget()
	termWidget.SetVExpand(true)
	termWidget.SetHExpand(true)
	termWidget.SetMarginStart(8) // Spacing from splitter
	paned.Pack2(termWidget, true, false)

	// Set initial strip width and collapse behavior
	// Script windows only have two positions: 0 (collapsed) or visible (with extra padding)
	consoleStripWidth := scaledMinNarrowStripWidth() + 4 + narrowOnlyExtraPadding
	paned.SetPosition(consoleStripWidth)

	// Track position changes for drag detection
	var consolePanedPressPos int = -1
	var consolePanedDragged bool
	var consolePanedOnHandle bool

	paned.Connect("notify::position", func() {
		defer runtime.KeepAlive(paned)
		pos := paned.GetPosition()
		// Track dragging
		if consolePanedPressPos >= 0 && consolePanedOnHandle && pos != consolePanedPressPos {
			consolePanedDragged = true
		}
		// Snap behavior
		if pos == 0 {
			// Already collapsed, do nothing
		} else if pos < consoleStripWidth/2 {
			// Less than half - snap to collapsed
			paned.SetPosition(0)
		} else if pos != consoleStripWidth {
			// More than half but not at fixed width - snap to visible
			paned.SetPosition(consoleStripWidth)
		}
	})

	// Handle click events on the console splitter handle only
	paned.Connect("button-press-event", func(p *gtk.Paned, ev *gdk.Event) bool {
		btnEvent := gdk.EventButtonNewFromEvent(ev)
		var handleWindow *gdk.Window
		if btnEvent.Button() == 1 {
			// Check if click is on the handle using handle window geometry
			consolePanedOnHandle = false
			var err error
			handleWindow, err = p.GetHandleWindow()
			if err == nil && handleWindow != nil {
				hx, hy := handleWindow.GetRootOrigin()
				hw := handleWindow.WindowGetWidth()
				hh := handleWindow.WindowGetHeight()
				clickX, clickY := int(btnEvent.XRoot()), int(btnEvent.YRoot())
				consolePanedOnHandle = clickX >= hx && clickX < hx+hw && clickY >= hy && clickY < hy+hh
			}
			if !consolePanedOnHandle {
				runtime.KeepAlive(btnEvent)
				runtime.KeepAlive(handleWindow)
				return false
			}
			consolePanedPressPos = p.GetPosition()
			consolePanedDragged = false
		}
		runtime.KeepAlive(btnEvent)
		runtime.KeepAlive(handleWindow)
		return false
	})

	paned.Connect("button-release-event", func(p *gtk.Paned, ev *gdk.Event) bool {
		btnEvent := gdk.EventButtonNewFromEvent(ev)
		defer runtime.KeepAlive(btnEvent)

		if btnEvent.Button() != 1 || !consolePanedOnHandle || consolePanedDragged || consolePanedPressPos < 0 {
			consolePanedPressPos = -1
			consolePanedOnHandle = false
			return false
		}
		// Single click: toggle between collapsed and narrow mode
		if p.GetPosition() == 0 {
			p.SetPosition(consoleStripWidth)
		} else {
			p.SetPosition(0)
		}
		consolePanedPressPos = -1
		consolePanedOnHandle = false
		return true
	})

	win.Add(paned)

	// Create context menu for this console window
	winContextMenu, _ := gtk.MenuNew()

	winCopyItem := createMenuItemWithGutter("Copy", func() {
		winTerminal.CopySelection()
	})
	winContextMenu.Append(winCopyItem)

	winPasteItem := createMenuItemWithGutter("Paste", func() {
		winTerminal.PasteClipboard()
	})
	winContextMenu.Append(winPasteItem)

	winSelectAllItem := createMenuItemWithGutter("Select All", func() {
		winTerminal.SelectAll()
	})
	winContextMenu.Append(winSelectAllItem)

	winClearItem := createMenuItemWithGutter("Clear", func() {
		winTerminal.Clear()
	})
	winContextMenu.Append(winClearItem)

	winContextMenu.ShowAll()

	// Connect right-click for context menu
	termWidget.Connect("button-press-event", func(widget *gtk.Box, ev *gdk.Event) bool {
		btn := gdk.EventButtonNewFromEvent(ev)
		if btn.Button() == 3 { // Right mouse button
			winContextMenu.PopupAtPointer(ev)
			return true
		}
		return false
	})

	// Create I/O channels for this window's console
	stdoutReader, stdoutWriter := io.Pipe()
	stdinReader, stdinWriter := io.Pipe()

	// Get terminal capabilities from the widget (auto-updates on resize)
	termCaps := winTerminal.GetTerminalCapabilities()

	// Non-blocking output queue
	outputQueue := make(chan interface{}, 256)
	go func() {
		for item := range outputQueue {
			switch v := item.(type) {
			case []byte:
				stdoutWriter.Write(v)
			case chan struct{}:
				close(v)
			}
		}
	}()

	winOutCh := &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		Terminal:         termCaps,
		NativeSend: func(v interface{}) error {
			var text string
			switch d := v.(type) {
			case []byte:
				text = string(d)
			case string:
				text = d
			default:
				text = fmt.Sprintf("%v", v)
			}
			text = strings.ReplaceAll(text, "\r\n", "\n")
			text = strings.ReplaceAll(text, "\n", "\r\n")
			data := []byte(text)
			select {
			case outputQueue <- data:
			default:
			}
			return nil
		},
		NativeRecv: func() (interface{}, error) {
			return nil, fmt.Errorf("cannot receive from console_out")
		},
		NativeFlush: func() error {
			writerDone := make(chan struct{})
			select {
			case outputQueue <- writerDone:
				<-writerDone
			default:
			}
			glibDone := make(chan struct{})
			glib.IdleAdd(func() bool {
				close(glibDone)
				return false
			})
			select {
			case <-glibDone:
			case <-time.After(100 * time.Millisecond):
			}
			return nil
		},
	}

	// Non-blocking input queue
	inputQueue := make(chan byte, 256)
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := stdinReader.Read(buf)
			if err != nil || n == 0 {
				close(inputQueue)
				return
			}
			select {
			case inputQueue <- buf[0]:
			default:
				select {
				case <-inputQueue:
				default:
				}
				select {
				case inputQueue <- buf[0]:
				default:
				}
			}
		}
	}()

	winInCh := &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		Terminal:         termCaps,
		NativeRecv: func() (interface{}, error) {
			b, ok := <-inputQueue
			if !ok {
				return nil, fmt.Errorf("input closed")
			}
			return []byte{b}, nil
		},
		NativeSend: func(v interface{}) error {
			return fmt.Errorf("cannot send to console_in")
		},
	}

	// Read from stdout pipe and feed to terminal
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdoutReader.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				glib.IdleAdd(func() bool {
					winTerminal.FeedBytes(data)
					return false
				})
			}
			if err != nil {
				return
			}
		}
	}()

	// REPL for interactive mode when no script is running
	var winREPL *pawscript.REPL

	// Wire keyboard input
	winTerminal.SetInputCallback(func(data []byte) {
		winScriptMu.Lock()
		isRunning := winScriptRunning
		winScriptMu.Unlock()

		if isRunning {
			stdinWriter.Write(data)
		} else if winREPL != nil && winREPL.IsRunning() {
			if winREPL.IsBusy() {
				// REPL is executing a command (e.g., read) - send to stdin pipe
				stdinWriter.Write(data)
			} else {
				// REPL is waiting for input - send to REPL for line editing
				winREPL.HandleInput(data)
			}
		}
	})

	win.ShowAll()

	// Run the script
	winTerminal.Feed(fmt.Sprintf("--- Running: %s ---\r\n\r\n", filepath.Base(filePath)))

	content, err := os.ReadFile(filePath)
	if err != nil {
		winTerminal.Feed(fmt.Sprintf("Error reading script file: %v\r\n", err))
		return
	}

	scriptDir := filepath.Dir(filePath)
	absScript, _ := filepath.Abs(filePath)
	if absScript != "" {
		scriptDir = filepath.Dir(absScript)
	}

	// Add the script's directory to recent paths for the combo box
	addRecentPath(scriptDir)

	cwd, _ := os.Getwd()
	tmpDir := os.TempDir()
	fileAccess := &pawscript.FileAccessConfig{
		ReadRoots:  []string{scriptDir, cwd, tmpDir},
		WriteRoots: []string{filepath.Join(scriptDir, "saves"), filepath.Join(scriptDir, "output"), filepath.Join(cwd, "saves"), filepath.Join(cwd, "output"), tmpDir},
		ExecRoots:  []string{filepath.Join(scriptDir, "helpers"), filepath.Join(scriptDir, "bin")},
	}

	ps := pawscript.New(&pawscript.Config{
		Debug:                false,
		AllowMacros:          true,
		EnableSyntacticSugar: true,
		ShowErrorContext:     true,
		ContextLines:         2,
		FileAccess:           fileAccess,
		ScriptDir:            scriptDir,
		OptLevel:             pawscript.OptimizationLevel(getOptimizationLevel()),
	})

	ioConfig := &pawscript.IOChannelConfig{
		Stdout: winOutCh,
		Stdin:  winInCh,
		Stderr: winOutCh,
	}
	ps.RegisterStandardLibraryWithIO([]string{}, ioConfig)

	winScriptMu.Lock()
	winScriptRunning = true
	winScriptMu.Unlock()

	// Handle window close - clean up resources to prevent GC issues
	win.Connect("destroy", func() {
		// Destroy the context menu explicitly to prevent GC finalizer crash
		winContextMenu.Destroy()
		// Close pipes to stop goroutines
		stdinWriter.Close()
		stdoutWriter.Close()
		stdinReader.Close()
		stdoutReader.Close()
		// Close output queue to stop its goroutine
		close(outputQueue)
	})

	go func() {
		snapshot := ps.CreateRestrictedSnapshot()
		result := ps.ExecuteWithEnvironment(string(content), snapshot, filePath, 0, 0)

		if winOutCh.NativeFlush != nil {
			winOutCh.NativeFlush()
		}

		if result == pawscript.BoolStatus(false) {
			winTerminal.Feed("\r\n--- Script execution failed ---\r\n")
		} else {
			winTerminal.Feed("\r\n--- Script completed ---\r\n")
		}

		winScriptMu.Lock()
		winScriptRunning = false
		winScriptMu.Unlock()

		// Start REPL for this window
		winREPL = pawscript.NewREPL(pawscript.REPLConfig{
			Debug:        false,
			Unrestricted: false,
			OptLevel:     getOptimizationLevel(),
			ShowBanner:   false,
			IOConfig: &pawscript.IOChannelConfig{
				Stdout: winOutCh,
				Stdin:  winInCh,
				Stderr: winOutCh,
			},
		}, func(s string) {
			glib.IdleAdd(func() bool {
				winTerminal.Feed(s)
				return false
			})
		})
		// Set flush callback to ensure output appears before blocking execution
		winREPL.SetFlush(func() {
			// Process pending GTK events to ensure output is displayed
			// We're on the main thread, so glib.IdleAdd won't work (we'd be waiting for ourselves)
			// Instead, use MainIterationDo to process pending events synchronously
			for i := 0; i < 10 && gtk.EventsPending(); i++ {
				gtk.MainIterationDo(false)
			}
		})
		// Set background color for prompt color selection
		bg := getTerminalBackground()
		winREPL.SetBackgroundRGB(bg.R, bg.G, bg.B)
		winREPL.SetPSLColors(getPSLColors())
		winREPL.Start()

		// Register the dummy_button command with the window's REPL
		// Create window-specific toolbar data
		winToolbarData := &WindowToolbarData{
			strip:      strip,
			menuButton: stripMenuBtn,
			terminal:   winTerminal,
		}
		winToolbarData.updateFunc = func() {
			updateWindowToolbarButtons(winToolbarData.strip, winToolbarData.registeredBtns)
		}
		registerDummyButtonCommand(winREPL.GetPawScript(), winToolbarData)
	}()
}

// createConsoleChannels creates the I/O channels for PawScript console
func createConsoleChannels() {
	// Create pipes for stdout/stdin
	stdoutReader, stdoutWriterLocal := io.Pipe()
	stdinReaderLocal, stdinWriterLocal := io.Pipe()
	stdoutWriter = stdoutWriterLocal
	stdinReader = stdinReaderLocal
	stdinWriter = stdinWriterLocal

	// Get terminal capabilities from the widget (auto-updates on resize)
	termCaps := terminal.GetTerminalCapabilities()

	// Non-blocking output: large buffer absorbs bursts
	// Uses interface{} to allow flush sentinels (chan struct{}) alongside data ([]byte)
	outputQueue := make(chan interface{}, 256)

	// Writer goroutine: drains queue and writes to terminal pipe
	go func() {
		for item := range outputQueue {
			switch v := item.(type) {
			case []byte:
				stdoutWriter.Write(v)
			case chan struct{}:
				// Flush sentinel - signal that queue has drained up to this point
				close(v)
			}
		}
	}()

	consoleOutCh = &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		Terminal:         termCaps,
		NativeSend: func(v interface{}) error {
			var text string
			switch d := v.(type) {
			case []byte:
				text = string(d)
			case string:
				text = d
			default:
				text = fmt.Sprintf("%v", v)
			}
			// Normalize newlines: first collapse any existing \r\n to \n, then convert all \n to \r\n
			// This ensures consistent terminal behavior whether in raw mode or not
			text = strings.ReplaceAll(text, "\r\n", "\n")
			text = strings.ReplaceAll(text, "\n", "\r\n")
			data := []byte(text)
			select {
			case outputQueue <- data:
			default:
				// Queue full - drop to prevent deadlock
			}
			return nil
		},
		NativeRecv: func() (interface{}, error) {
			return nil, fmt.Errorf("cannot receive from console_out")
		},
		NativeFlush: func() error {
			// Step 1: Wait for outputQueue to drain and pipe to be read
			// Since io.Pipe blocks until read, when this completes all prior data
			// has been read from the pipe (though glib.IdleAdd may still be pending)
			writerDone := make(chan struct{})
			select {
			case outputQueue <- writerDone:
				<-writerDone // Wait for writer goroutine to process sentinel
			default:
				// Queue full - shouldn't happen with 256 buffer, but proceed anyway
			}

			// Step 2: Wait for all pending glib.IdleAdd callbacks to complete
			// This ensures all FeedBytes calls have finished before we return
			// Use a timeout to avoid deadlock if GTK main loop isn't running yet
			// (e.g., during startup before app.Run() is called)
			glibDone := make(chan struct{})
			glib.IdleAdd(func() bool {
				close(glibDone)
				return false
			})
			select {
			case <-glibDone:
				// Successfully waited for GTK main loop
			case <-time.After(100 * time.Millisecond):
				// GTK main loop not running yet, proceed anyway
			}

			return nil
		},
	}

	// Set up the global flushFunc
	flushFunc = func() {
		if consoleOutCh != nil {
			consoleOutCh.Flush()
		}
	}

	// Non-blocking input queue
	inputQueue := make(chan byte, 256)

	// Reader goroutine: drains pipe and puts bytes into queue
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := stdinReader.Read(buf)
			if err != nil || n == 0 {
				close(inputQueue)
				return
			}
			select {
			case inputQueue <- buf[0]:
			default:
				// Drop oldest if full
				select {
				case <-inputQueue:
				default:
				}
				select {
				case inputQueue <- buf[0]:
				default:
				}
			}
		}
	}()

	consoleInCh = &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		Terminal:         termCaps,
		NativeRecv: func() (interface{}, error) {
			b, ok := <-inputQueue
			if !ok {
				return nil, fmt.Errorf("input closed")
			}
			return []byte{b}, nil
		},
		NativeSend: func(v interface{}) error {
			return fmt.Errorf("cannot send to console_in")
		},
	}

	clearInputFunc = func() {
		for {
			select {
			case <-inputQueue:
			default:
				return
			}
		}
	}

	// Start goroutine to read from stdout pipe and feed to terminal
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdoutReader.Read(buf)
			if n > 0 {
				// Schedule terminal update on GTK main thread
				data := make([]byte, n)
				copy(data, buf[:n])
				glib.IdleAdd(func() bool {
					terminal.FeedBytes(data)
					return false
				})
			}
			if err != nil {
				return
			}
		}
	}()

	// Wire keyboard input from terminal to stdin pipe or REPL
	terminal.SetInputCallback(func(data []byte) {
		scriptMu.Lock()
		isRunning := scriptRunning
		scriptMu.Unlock()

		if isRunning {
			// Script is running, send to stdin pipe
			if stdinWriter != nil {
				stdinWriter.Write(data)
			}
		} else if consoleREPL != nil && consoleREPL.IsRunning() {
			// REPL is active
			if consoleREPL.IsBusy() {
				// REPL is executing a command (e.g., read) - send to stdin pipe
				if stdinWriter != nil {
					stdinWriter.Write(data)
				}
			} else {
				// REPL is waiting for input - send to REPL for line editing
				consoleREPL.HandleInput(data)
			}
		}
	})

	// Create and start the REPL for interactive mode
	// ShowBanner is false because we print our own welcome message above
	consoleREPL = pawscript.NewREPL(pawscript.REPLConfig{
		Debug:        false,
		Unrestricted: false,
		OptLevel:     getOptimizationLevel(),
		ShowBanner:   false,
		IOConfig: &pawscript.IOChannelConfig{
			Stdout: consoleOutCh,
			Stdin:  consoleInCh,
			Stderr: consoleOutCh,
		},
	}, func(s string) {
		// Output to terminal on GTK main thread
		glib.IdleAdd(func() bool {
			terminal.Feed(s)
			return false
		})
	})
	// Set flush callback to ensure output appears before blocking execution
	consoleREPL.SetFlush(func() {
		// Process pending GTK events to ensure output is displayed
		// We're on the main thread, so glib.IdleAdd won't work (we'd be waiting for ourselves)
		// Instead, use MainIterationDo to process pending events synchronously
		for i := 0; i < 10 && gtk.EventsPending(); i++ {
			gtk.MainIterationDo(false)
		}
	})
	// Set background color for prompt color selection
	bg := getTerminalBackground()
	consoleREPL.SetBackgroundRGB(bg.R, bg.G, bg.B)
	consoleREPL.SetPSLColors(getPSLColors())
	consoleREPL.Start()

	// Register the dummy_button command with the REPL's PawScript instance
	// Create launcher toolbar data that uses the global launcher strip
	launcherToolbarData = &WindowToolbarData{
		strip:    launcherNarrowStrip,
		terminal: terminal,
		updateFunc: func() {
			// Copy buttons to global for launcher-specific visibility logic
			launcherRegisteredBtns = launcherToolbarData.registeredBtns
			updateLauncherToolbarButtons()
		},
	}
	registerDummyButtonCommand(consoleREPL.GetPawScript(), launcherToolbarData)
}
