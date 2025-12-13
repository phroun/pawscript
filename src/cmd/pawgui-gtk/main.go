// pawgui-gtk - GTK3-based GUI for PawScript with custom terminal emulator
// Cross-platform: works on Linux, macOS, and Windows
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
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

	// Launcher narrow strip (for multiple toolbar buttons)
	launcherNarrowStrip    *gtk.Box           // The narrow strip container
	launcherMenuButton     *gtk.Button        // Hamburger button in path selector (when strip hidden)
	launcherStripMenuBtn   *gtk.Button        // Hamburger button in narrow strip (when strip visible)
	launcherWidePanel      *gtk.Box           // The wide panel (file browser)
	launcherPaned          *gtk.Paned         // The main splitter
	launcherRegisteredBtns []*ToolbarButton   // Additional registered buttons for launcher
	launcherToolbarData    *WindowToolbarData // Toolbar data for the launcher window

	// Per-window toolbar data (keyed by PawScript instance)
	toolbarDataByPS = make(map[*pawscript.PawScript]*WindowToolbarData)
	toolbarDataMu   sync.Mutex
)

// WindowToolbarData holds per-window toolbar state for dummy_button command
type WindowToolbarData struct {
	strip          *gtk.Box                // The narrow strip container
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
func getPSLColors() pawscript.DisplayColorConfig { return configHelper.GetPSLColors() }

// getLauncherWidth returns the saved launcher panel width, defaulting to 280
func getLauncherWidth() int {
	return appConfig.GetInt("launcher_width", 280)
}

// saveLauncherWidth saves the launcher panel width to config
func saveLauncherWidth(width int) {
	appConfig.Set("launcher_width", width)
	saveConfig(appConfig)
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

<i>A scripting language for creative coding</i>

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

// createHamburgerMenu creates the hamburger dropdown menu
// isScriptWindow: true for script windows (slightly different options)
func createHamburgerMenu(parent gtk.IWindow, isScriptWindow bool) *gtk.Menu {
	menu, _ := gtk.MenuNew()

	// About option
	aboutItem, _ := gtk.MenuItemNewWithLabel("About PawScript...")
	aboutItem.Connect("activate", func() {
		showAboutDialog(parent)
	})
	menu.Append(aboutItem)

	menu.ShowAll()
	return menu
}

// createHamburgerButton creates a hamburger menu button with SVG icon
// forVerticalStrip: true for vertical toolbar strip, false for horizontal rows (file selector)
func createHamburgerButton(menu *gtk.Menu, forVerticalStrip bool) *gtk.Button {
	btn, _ := gtk.ButtonNew()
	btn.SetSizeRequest(32, 32)
	btn.SetTooltipText("Menu")
	applyToolbarButtonStyle(btn, forVerticalStrip)

	// Set SVG icon with appropriate color for current theme
	svgData := getSVGIcon(hamburgerIconSVG)
	if img := createImageFromSVG(svgData, 24); img != nil {
		btn.SetImage(img)
		btn.SetAlwaysShowImage(true)
	} else {
		// Fallback to text if SVG loading fails
		btn.SetLabel("☰")
	}

	// Pop up the menu on click
	btn.Connect("clicked", func() {
		menu.PopupAtWidget(btn, gdk.GDK_GRAVITY_SOUTH_WEST, gdk.GDK_GRAVITY_NORTH_WEST, nil)
	})

	return btn
}

// createToolbarStrip creates a vertical strip of toolbar buttons
// Returns the strip container and the hamburger button
func createToolbarStrip(parent gtk.IWindow, isScriptWindow bool) (*gtk.Box, *gtk.Button, *gtk.Menu) {
	strip, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4) // 4px spacing between buttons
	strip.SetMarginStart(2)
	strip.SetMarginEnd(2)
	strip.SetMarginTop(5)
	strip.SetMarginBottom(5)

	// Create hamburger menu and button
	menu := createHamburgerMenu(parent, isScriptWindow)
	menuBtn := createHamburgerButton(menu, true) // true = vertical strip

	strip.PackStart(menuBtn, false, false, 0)

	return strip, menuBtn, menu
}

// Minimum widths for panel collapse behavior
const (
	minWidePanelWidth      = 196 // Minimum width before wide panel collapses
	minNarrowStripWidth    = 40  // Minimum width before narrow strip collapses
	narrowOnlyExtraPadding = 6   // Extra left padding when in narrow-only mode
)

// Embedded SVG icons (fill color is replaced at runtime based on theme)
const hamburgerIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <rect style="fill:{{FILL}};stroke:none" width="11.346176" height="2.25" x="0.70038122" y="0.8404575"/>
  <rect style="fill:{{FILL}};stroke:none" width="11.346176" height="2.25" x="0.70038122" y="5.3383746"/>
  <rect style="fill:{{FILL}};stroke:none" width="11.346176" height="2.25" x="0.70038122" y="9.8362913"/>
</svg>`

const starIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <path style="fill:{{FILL}};stroke:none" d="M 6.4849512,1.5761366 8.0478061,4.7428264 11.542456,5.250629 9.0137037,7.7155534 9.6106608,11.196082 6.484951,9.5527997 3.359241,11.196082 3.9561984,7.7155534 1.4274463,5.2506288 4.9220959,4.7428264 Z" transform="matrix(1.1757817,0,0,1.1757817,-1.274887,-1.2479333)"/>
</svg>`

// getIconFillColor returns the appropriate icon fill color based on theme
func getIconFillColor() string {
	theme := configHelper.GetTheme()
	switch theme {
	case pawgui.ThemeDark:
		return "#ffffff"
	case pawgui.ThemeLight:
		return "#000000"
	default: // ThemeAuto - query GTK to see if dark theme is active
		settings, err := gtk.SettingsGetDefault()
		if err == nil {
			darkPref, err := settings.GetProperty("gtk-application-prefer-dark-theme")
			if err == nil {
				if isDark, ok := darkPref.(bool); ok && isDark {
					return "#ffffff"
				}
			}
			// Also check the theme name for "dark" suffix as fallback
			themeName, err := settings.GetProperty("gtk-theme-name")
			if err == nil {
				if name, ok := themeName.(string); ok {
					if strings.Contains(strings.ToLower(name), "dark") {
						return "#ffffff"
					}
				}
			}
		}
		// Default to black (light theme) if we can't determine
		return "#000000"
	}
}

// getSVGIcon returns SVG data with the fill color set appropriately for current theme
func getSVGIcon(svgTemplate string) string {
	return strings.Replace(svgTemplate, "{{FILL}}", getIconFillColor(), -1)
}

// createImageFromSVG creates a GtkImage from SVG data
func createImageFromSVG(svgData string, size int) *gtk.Image {
	// Create a PixbufLoader for SVG
	loader, err := gdk.PixbufLoaderNew()
	if err != nil {
		return nil
	}

	// Write SVG data to loader
	_, err = loader.Write([]byte(svgData))
	if err != nil {
		loader.Close()
		return nil
	}

	err = loader.Close()
	if err != nil {
		return nil
	}

	// Get the pixbuf
	pixbuf, err := loader.GetPixbuf()
	if err != nil || pixbuf == nil {
		return nil
	}

	// Scale to desired size
	scaled, err := pixbuf.ScaleSimple(size, size, gdk.INTERP_BILINEAR)
	if err != nil || scaled == nil {
		return nil
	}

	// Create image from pixbuf
	img, err := gtk.ImageNewFromPixbuf(scaled)
	if err != nil {
		return nil
	}

	return img
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

	// Add new dummy buttons
	for _, btn := range launcherRegisteredBtns {
		button, _ := gtk.ButtonNew()
		button.SetSizeRequest(32, 32)
		button.SetTooltipText(btn.Tooltip)
		applyToolbarButtonStyle(button, true) // true = vertical strip
		// Set SVG icon with appropriate color for current theme
		svgData := getSVGIcon(starIconSVG)
		if img := createImageFromSVG(svgData, 24); img != nil {
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
		if hadButtons && !hasMultipleButtons {
			// Transitioning from both mode to wide-only: subtract strip width
			launcherPaned.SetPosition(pos - minNarrowStripWidth)
		} else if !hadButtons && hasMultipleButtons {
			// Transitioning from wide-only to both mode: add strip width
			launcherPaned.SetPosition(pos + minNarrowStripWidth)
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

	// Add new dummy buttons
	for _, btn := range buttons {
		button, _ := gtk.ButtonNew()
		button.SetSizeRequest(32, 32)
		button.SetTooltipText(btn.Tooltip)
		applyToolbarButtonStyle(button, true) // true = vertical strip
		// Set SVG icon with appropriate color for current theme
		svgData := getSVGIcon(starIconSVG)
		if img := createImageFromSVG(svgData, 24); img != nil {
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

// applyTheme sets the GTK theme based on the configuration.
// "auto" = let GTK/OS decide, "dark" = force dark, "light" = force light
func applyTheme(theme pawgui.ThemeMode) {
	settings, err := gtk.SettingsGetDefault()
	if err != nil {
		return
	}

	switch theme {
	case pawgui.ThemeDark:
		settings.SetProperty("gtk-application-prefer-dark-theme", true)
	case pawgui.ThemeLight:
		settings.SetProperty("gtk-application-prefer-dark-theme", false)
	case pawgui.ThemeAuto:
		// Let GTK use the system default - no explicit setting needed
		// On most systems, this will follow the OS dark/light mode preference
	}
}

// setupQuitShortcut configures the keyboard shortcut to quit the application
func setupQuitShortcut() {
	quitShortcut := getQuitShortcut()
	if quitShortcut == "" {
		return // Disabled
	}

	var targetKey uint
	var targetMod gdk.ModifierType
	switch quitShortcut {
	case "Cmd+Q":
		targetKey = gdk.KEY_q
		targetMod = gdk.META_MASK
	case "Ctrl+Q":
		targetKey = gdk.KEY_q
		targetMod = gdk.CONTROL_MASK
	case "Alt+F4":
		targetKey = gdk.KEY_F4
		targetMod = gdk.MOD1_MASK
	default:
		return
	}

	// Connect to key-press-event on the main window
	mainWindow.Connect("key-press-event", func(win *gtk.ApplicationWindow, event *gdk.Event) bool {
		keyEvent := gdk.EventKeyNewFromEvent(event)
		keyval := keyEvent.KeyVal()
		state := gdk.ModifierType(keyEvent.State())

		// Mask out non-modifier bits (like num lock, caps lock)
		state = state & (gdk.CONTROL_MASK | gdk.SHIFT_MASK | gdk.MOD1_MASK | gdk.META_MASK)

		// Check for lowercase or uppercase 'q'
		if targetKey == gdk.KEY_q {
			if (keyval == gdk.KEY_q || keyval == gdk.KEY_Q) && state == targetMod {
				mainWindow.Close()
				return true
			}
		} else if keyval == targetKey && state == targetMod {
			mainWindow.Close()
			return true
		}
		return false
	})
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

	// Create terminal
	winTerminal, err := purfectermgtk.New(purfectermgtk.Options{
		Cols:           100,
		Rows:           30,
		ScrollbackSize: 10000,
		FontFamily:     getFontFamily(),
		FontSize:       getFontSize(),
		Scheme: purfecterm.ColorScheme{
			Foreground: getTerminalForeground(),
			Background: getTerminalBackground(),
			Cursor:     purfecterm.Color{R: 255, G: 255, B: 255},
			Selection:  purfecterm.Color{R: 68, G: 68, B: 68},
			Palette:    getColorPalette(),
			BlinkMode:  getBlinkMode(),
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create terminal: %v\n", err)
		win.Destroy()
		return
	}

	// Set font fallbacks
	winTerminal.SetFontFallbacks(getFontFamilyUnicode(), getFontFamilyCJK())

	// Create main layout with collapsible toolbar strip
	paned, _ := gtk.PanedNew(gtk.ORIENTATION_HORIZONTAL)

	// Narrow strip for script window (always starts visible, collapsible)
	// Console windows always show strip-only, so use extra left padding
	strip, _, _ := createToolbarStrip(win, true)
	strip.SetMarginStart(2 + narrowOnlyExtraPadding)
	strip.SetSizeRequest(minNarrowStripWidth, -1) // Keep original width, margin adds the extra space
	paned.Pack1(strip, false, true)

	// Terminal on the right
	termWidget := winTerminal.Widget()
	termWidget.SetVExpand(true)
	termWidget.SetHExpand(true)
	termWidget.SetMarginStart(8) // Spacing from splitter
	paned.Pack2(termWidget, true, false)

	// Set initial strip width and collapse behavior
	// Script windows only have two positions: 0 (collapsed) or visible (with extra padding)
	consoleStripWidth := minNarrowStripWidth + narrowOnlyExtraPadding
	paned.SetPosition(consoleStripWidth)

	// Track position changes for drag detection
	var consolePanedPressPos int = -1
	var consolePanedDragged bool
	var consolePanedOnHandle bool
	const consoleHandleClickWidth = 10 // Pixels around handle position to detect clicks

	paned.Connect("notify::position", func() {
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
		if btnEvent.Button() == 1 {
			// Check if click is on the handle area
			clickX := int(btnEvent.X())
			handlePos := p.GetPosition()
			consolePanedOnHandle = clickX >= handlePos-consoleHandleClickWidth && clickX <= handlePos+consoleHandleClickWidth
			if !consolePanedOnHandle {
				return false
			}
			consolePanedPressPos = handlePos
			consolePanedDragged = false
		}
		return false
	})

	paned.Connect("button-release-event", func(p *gtk.Paned, ev *gdk.Event) bool {
		btnEvent := gdk.EventButtonNewFromEvent(ev)
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
	mainWindow.SetDefaultSize(1100, 700)

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
	contextMenu, _ = gtk.MenuNew()

	copyItem, _ := gtk.MenuItemNewWithLabel("Copy")
	copyItem.Connect("activate", func() {
		if terminal != nil {
			terminal.CopySelection()
		}
	})
	contextMenu.Append(copyItem)

	pasteItem, _ := gtk.MenuItemNewWithLabel("Paste")
	pasteItem.Connect("activate", func() {
		if terminal != nil {
			terminal.PasteClipboard()
		}
	})
	contextMenu.Append(pasteItem)

	selectAllItem, _ := gtk.MenuItemNewWithLabel("Select All")
	selectAllItem.Connect("activate", func() {
		if terminal != nil {
			terminal.SelectAll()
		}
	})
	contextMenu.Append(selectAllItem)

	clearItem, _ := gtk.MenuItemNewWithLabel("Clear")
	clearItem.Connect("activate", func() {
		if terminal != nil {
			terminal.Clear()
		}
	})
	contextMenu.Append(clearItem)

	contextMenu.ShowAll()

	// Create main vertical box for content (no menu bar)
	mainBox, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)

	// Handle Alt+F4 on Windows/Linux (usually handled by window manager, but add for safety)
	mainWindow.Connect("key-press-event", func(win *gtk.ApplicationWindow, ev *gdk.Event) bool {
		keyEvent := gdk.EventKeyNewFromEvent(ev)
		keyval := keyEvent.KeyVal()
		state := keyEvent.State()
		// Alt+F4 to quit
		if keyval == gdk.KEY_F4 && state&gdk.MOD1_MASK != 0 {
			mainWindow.Close()
			return true
		}
		return false
	})

	// Create main horizontal paned (split view)
	launcherPaned, _ = gtk.PanedNew(gtk.ORIENTATION_HORIZONTAL)
	// Position will be set after ShowAll based on saved width and button state

	// Left panel container: holds wide panel (file browser) and narrow strip side by side
	leftContainer, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)

	// Wide panel: File browser
	widePanel := createFileBrowser()
	leftContainer.PackStart(widePanel, true, true, 0)

	// Narrow strip: toolbar buttons (created but hidden initially - only 1 button)
	launcherNarrowStrip, launcherStripMenuBtn, _ = createToolbarStrip(mainWindow, false)
	launcherNarrowStrip.SetNoShowAll(true)                      // Don't show when ShowAll is called
	launcherNarrowStrip.SetSizeRequest(minNarrowStripWidth, -1) // Fixed width
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

	// Save launcher width when user adjusts the splitter
	// Implement multi-stage collapse:
	// - Wide + narrow mode: when pos >= minWidePanelWidth + minNarrowStripWidth
	// - Narrow only mode: when pos >= minNarrowStripWidth but < threshold for wide panel
	// - Collapsed: when pos < halfway point of narrow strip
	narrowOnlyWidth := minNarrowStripWidth + narrowOnlyExtraPadding
	launcherPaned.Connect("notify::position", func() {
		pos := launcherPaned.GetPosition()
		hasMultipleButtons := len(launcherRegisteredBtns) > 0

		// Calculate threshold for showing both panels (wide panel needs its min width plus narrow strip width)
		bothThreshold := (minWidePanelWidth / 2) + minNarrowStripWidth
		// Halfway point for collapsing narrow strip
		narrowSnapPoint := minNarrowStripWidth / 2

		if pos == 0 {
			// Fully collapsed
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
				launcherPaned.SetPosition(max(pos, minWidePanelWidth+minNarrowStripWidth))
			} else {
				launcherPaned.SetPosition(max(pos, minWidePanelWidth))
			}
			launcherNarrowStrip.SetMarginStart(2) // Normal padding in wide mode
			if hasMultipleButtons {
				// Multiple buttons: show narrow strip, hide menu button in path row
				launcherNarrowStrip.Show()
				launcherMenuButton.Hide()
				launcherStripMenuBtn.Show()
				// Save only the wide panel width (subtract strip width)
				saveLauncherWidth(launcherPaned.GetPosition() - minNarrowStripWidth)
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
	const handleClickWidth = 10 // Pixels around handle position to detect clicks

	launcherPaned.Connect("button-press-event", func(paned *gtk.Paned, ev *gdk.Event) bool {
		btnEvent := gdk.EventButtonNewFromEvent(ev)
		if btnEvent.Button() == 1 { // Left mouse button
			// Check if click is on the handle area (near the splitter position)
			clickX := int(btnEvent.X())
			handlePos := paned.GetPosition()
			launcherPanedOnHandle = clickX >= handlePos-handleClickWidth && clickX <= handlePos+handleClickWidth

			if !launcherPanedOnHandle {
				return false // Not on handle, let event propagate normally
			}

			// Check for double-click (detected on press in GTK)
			if btnEvent.Type() == gdk.EVENT_2BUTTON_PRESS {
				launcherPanedDoubleClick = true
				// Double-click: collapse completely
				paned.SetPosition(0)
				return true
			}
			launcherPanedPressPos = handlePos
			launcherPanedDragged = false
			launcherPanedDoubleClick = false
		}
		return false // Let GTK handle the event too
	})

	// Track position changes during drag (additional handler)
	launcherPaned.Connect("notify::position", func() {
		if launcherPanedPressPos >= 0 && launcherPanedOnHandle {
			currentPos := launcherPaned.GetPosition()
			if currentPos != launcherPanedPressPos {
				launcherPanedDragged = true
			}
		}
	})

	launcherPaned.Connect("button-release-event", func(paned *gtk.Paned, ev *gdk.Event) bool {
		btnEvent := gdk.EventButtonNewFromEvent(ev)
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

		// Single click behavior based on current state
		if pos == 0 {
			// Collapsed -> narrow mode
			paned.SetPosition(narrowOnlyWidth)
		} else if pos <= narrowOnlyWidth {
			// Narrow mode -> wide mode
			savedWidth := getLauncherWidth()
			if savedWidth <= narrowOnlyWidth {
				savedWidth = 280 // Default wide width
			}
			if hasMultipleButtons {
				paned.SetPosition(savedWidth + minNarrowStripWidth)
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
			actualPos = savedPos + minNarrowStripWidth
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
	// Note: menu parent will be set to mainWindow after it's created
	launcherMenu := createHamburgerMenu(nil, false)
	launcherMenuButton = createHamburgerButton(launcherMenu, false) // false = horizontal row
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
		Scheme: purfecterm.ColorScheme{
			Foreground: getTerminalForeground(),
			Background: getTerminalBackground(),
			Cursor:     purfecterm.Color{R: 255, G: 255, B: 255},
			Selection:  purfecterm.Color{R: 68, G: 68, B: 68},
			Palette:    getColorPalette(),
			BlinkMode:  getBlinkMode(),
		},
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

	// Clear existing menu items
	pathMenu.GetChildren().Foreach(func(item interface{}) {
		if widget, ok := item.(gtk.IWidget); ok {
			pathMenu.Remove(widget)
		}
	})

	// Helper to add a menu item with callback
	addMenuItem := func(label string, callback func()) {
		item, _ := gtk.MenuItemNewWithLabel(label)
		item.Connect("activate", callback)
		pathMenu.Append(item)
	}

	// Helper to add a separator
	addSeparator := func() {
		sep, _ := gtk.SeparatorMenuItemNew()
		pathMenu.Append(sep)
	}

	// Add current path (just shows where we are)
	currentItem, _ := gtk.MenuItemNewWithLabel(currentDir)
	currentItem.SetSensitive(false)
	pathMenu.Append(currentItem)

	addSeparator()

	// Add Home directory
	if home := getHomeDir(); home != "" {
		addMenuItem("🏠 Home", func() {
			if info, err := os.Stat(home); err == nil && info.IsDir() {
				currentDir = home
				refreshFileList()
				updatePathMenu()
			}
		})
	}

	// Add Examples directory
	if examples := getExamplesDir(); examples != "" {
		addMenuItem("📁 Examples", func() {
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
		addMenuItem("🗑 Clear Recent Paths", func() {
			clearRecentPaths()
			updatePathMenu()
		})
	}

	pathMenu.ShowAll()
}

func refreshFileList() {
	// Clear existing items
	children := fileList.GetChildren()
	children.Foreach(func(item interface{}) {
		if widget, ok := item.(*gtk.Widget); ok {
			fileList.Remove(widget)
		}
	})

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

	// Use GTK icons from Adwaita theme
	var iconName string
	if isParent {
		iconName = "go-up"
	} else if isDir {
		iconName = "folder"
	} else {
		iconName = "text-x-generic"
	}
	icon, err := gtk.ImageNewFromIconName(iconName, gtk.ICON_SIZE_LARGE_TOOLBAR)
	if err == nil {
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

	// Create terminal for this window
	winTerminal, err := purfectermgtk.New(purfectermgtk.Options{
		Cols:           100,
		Rows:           30,
		ScrollbackSize: 10000,
		FontFamily:     getFontFamily(),
		FontSize:       getFontSize(),
		Scheme: purfecterm.ColorScheme{
			Foreground: getTerminalForeground(),
			Background: getTerminalBackground(),
			Cursor:     purfecterm.Color{R: 255, G: 255, B: 255},
			Selection:  purfecterm.Color{R: 68, G: 68, B: 68},
			Palette:    getColorPalette(),
			BlinkMode:  getBlinkMode(),
		},
	})
	if err != nil {
		terminal.Feed(fmt.Sprintf("Failed to create terminal: %v\r\n", err))
		win.Destroy()
		return
	}

	// Set font fallbacks for Unicode/CJK characters
	winTerminal.SetFontFallbacks(getFontFamilyUnicode(), getFontFamilyCJK())

	// Create main layout with collapsible toolbar strip
	paned, _ := gtk.PanedNew(gtk.ORIENTATION_HORIZONTAL)

	// Narrow strip for script window (always starts visible, collapsible)
	// Console windows always show strip-only, so use extra left padding
	strip, _, _ := createToolbarStrip(win, true)
	strip.SetMarginStart(2 + narrowOnlyExtraPadding)
	strip.SetSizeRequest(minNarrowStripWidth, -1) // Keep original width, margin adds the extra space
	paned.Pack1(strip, false, true)

	// Terminal on the right
	termWidget := winTerminal.Widget()
	termWidget.SetVExpand(true)
	termWidget.SetHExpand(true)
	termWidget.SetMarginStart(8) // Spacing from splitter
	paned.Pack2(termWidget, true, false)

	// Set initial strip width and collapse behavior
	// Script windows only have two positions: 0 (collapsed) or visible (with extra padding)
	consoleStripWidth := minNarrowStripWidth + 4 + narrowOnlyExtraPadding
	paned.SetPosition(consoleStripWidth)

	// Track position changes for drag detection
	var consolePanedPressPos int = -1
	var consolePanedDragged bool
	var consolePanedOnHandle bool
	const consoleHandleClickWidth2 = 10 // Pixels around handle position to detect clicks

	paned.Connect("notify::position", func() {
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
		if btnEvent.Button() == 1 {
			// Check if click is on the handle area
			clickX := int(btnEvent.X())
			handlePos := p.GetPosition()
			consolePanedOnHandle = clickX >= handlePos-consoleHandleClickWidth2 && clickX <= handlePos+consoleHandleClickWidth2
			if !consolePanedOnHandle {
				return false
			}
			consolePanedPressPos = handlePos
			consolePanedDragged = false
		}
		return false
	})

	paned.Connect("button-release-event", func(p *gtk.Paned, ev *gdk.Event) bool {
		btnEvent := gdk.EventButtonNewFromEvent(ev)
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

	winCopyItem, _ := gtk.MenuItemNewWithLabel("Copy")
	winCopyItem.Connect("activate", func() {
		winTerminal.CopySelection()
	})
	winContextMenu.Append(winCopyItem)

	winPasteItem, _ := gtk.MenuItemNewWithLabel("Paste")
	winPasteItem.Connect("activate", func() {
		winTerminal.PasteClipboard()
	})
	winContextMenu.Append(winPasteItem)

	winSelectAllItem, _ := gtk.MenuItemNewWithLabel("Select All")
	winSelectAllItem.Connect("activate", func() {
		winTerminal.SelectAll()
	})
	winContextMenu.Append(winSelectAllItem)

	winClearItem, _ := gtk.MenuItemNewWithLabel("Clear")
	winClearItem.Connect("activate", func() {
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

	// Track script running state for this window
	var winScriptRunning bool
	var winScriptMu sync.Mutex
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
			strip:    strip,
			terminal: winTerminal,
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
	consoleREPL = pawscript.NewREPL(pawscript.REPLConfig{
		Debug:        false,
		Unrestricted: false,
		OptLevel:     getOptimizationLevel(),
		ShowBanner:   true,
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
