// pawgui-qt - Qt-based GUI for PawScript with custom terminal emulator
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
	"unsafe"

	"github.com/mappu/miqt/qt"
	"github.com/phroun/pawscript"
	"github.com/phroun/pawscript/pkg/pawgui"
	"github.com/phroun/pawscript/pkg/purfecterm"
	purfectermqt "github.com/phroun/pawscript/pkg/purfecterm-qt"
)

var version = "dev" // set via -ldflags at build time

// Default font size constant (uses shared package value)
const defaultFontSize = pawgui.DefaultFontSize

const appName = "PawScript Launcher (Qt)"

// Global state
var (
	currentDir string
	qtApp      *qt.QApplication
	mainWindow *qt.QMainWindow
	fileList   *qt.QListWidget
	terminal   *purfectermqt.Terminal
	pathButton *qt.QPushButton // Path selector button with dropdown menu
	pathMenu   *qt.QMenu       // Dropdown menu for path selection
	runButton    *qt.QPushButton
	browseButton *qt.QPushButton

	// Console I/O for PawScript
	consoleOutCh   *pawscript.StoredChannel
	consoleInCh    *pawscript.StoredChannel
	stdinReader    *io.PipeReader
	stdinWriter    *io.PipeWriter
	clearInputFunc func()
	flushFunc      func()
	scriptRunning  bool
	scriptMu       sync.Mutex

	// REPL for interactive mode
	consoleREPL *pawscript.REPL

	// Configuration
	appConfig    pawscript.PSLConfig
	configHelper *pawgui.ConfigHelper

	// Launcher narrow strip (for multiple toolbar buttons)
	launcherNarrowStrip   *qt.QWidget   // The narrow strip container
	launcherMenuButton    *qt.QPushButton // Hamburger button in path selector (when strip hidden)
	launcherStripMenuBtn  *qt.QPushButton // Hamburger button in narrow strip (when strip visible)
	launcherWidePanel     *qt.QWidget   // The wide panel (file browser)
	launcherRegisteredBtns []*QtToolbarButton // Additional registered buttons for launcher
)

// QtToolbarButton represents a registered toolbar button for Qt
type QtToolbarButton struct {
	Icon     string           // Icon name or path
	Tooltip  string           // Tooltip text
	OnClick  func()           // Click handler
	Menu     *qt.QMenu        // Optional dropdown menu (if nil, OnClick is used)
	widget   *qt.QPushButton  // The actual button widget
}

// Minimum widths for panel collapse behavior
const (
	minWidePanelWidth   = 196 // Minimum width before wide panel collapses
	minNarrowStripWidth = 40  // Minimum width before narrow strip collapses
)

// Random icons for dummy buttons
var dummyIcons = []string{"★", "♦", "♠", "♣", "♥", "●", "■", "▲", "◆", "⬟", "⬢", "✦", "✧", "⚡", "☀", "☁", "☂", "☃", "✿", "❀"}

// --- Configuration Management ---

func getConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".paw")
}

func getConfigPath() string {
	configDir := getConfigDir()
	if configDir == "" {
		return ""
	}
	return filepath.Join(configDir, "pawgui-qt.psl")
}

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

func saveConfig(config pawscript.PSLConfig) {
	configPath := getConfigPath()
	if configPath == "" {
		return
	}

	configDir := getConfigDir()
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return
	}

	data := pawscript.SerializePSLPretty(config)
	_ = os.WriteFile(configPath, []byte(data+"\n"), 0644)
}

func saveBrowseDir(dir string) {
	appConfig.Set("last_browse_dir", dir)
	saveConfig(appConfig)
}

// Configuration getter wrappers using shared configHelper
func getFontFamily() string                   { return configHelper.GetFontFamily() }
func getFontFamilyUnicode() string            { return configHelper.GetFontFamilyUnicode() }
func getFontFamilyCJK() string                { return configHelper.GetFontFamilyCJK() }
func getFontSize() int                        { return configHelper.GetFontSize() }
func getUIScale() float64                     { return configHelper.GetUIScale() }
func getOptimizationLevel() int               { return configHelper.GetOptimizationLevel() }
func getTerminalBackground() purfecterm.Color { return configHelper.GetTerminalBackground() }
func getTerminalForeground() purfecterm.Color { return configHelper.GetTerminalForeground() }
func getColorPalette() []purfecterm.Color     { return configHelper.GetColorPalette() }
func getBlinkMode() purfecterm.BlinkMode      { return configHelper.GetBlinkMode() }
func getQuitShortcut() string                 { return configHelper.GetQuitShortcut() }
func getDefaultQuitShortcut() string          { return pawgui.GetDefaultQuitShortcut() }
func getPSLColors() pawscript.DisplayColorConfig { return configHelper.GetPSLColors() }

func showCopyright() {
	fmt.Fprintf(os.Stderr, "pawgui-qt, the PawScript GUI interpreter version %s (with Qt)\nCopyright (c) 2025 Jeffrey R. Day\nLicense: MIT\n", version)
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
Usage: pawgui-qt [options] [script.paw] [-- args...]
       pawgui-qt [options] < input.paw
       echo "commands" | pawgui-qt [options]

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

// getLauncherWidth returns the saved launcher panel width, defaulting to 280
func getLauncherWidth() int {
	return appConfig.GetInt("launcher_width", 280)
}

// saveLauncherWidth saves the launcher panel width to config
func saveLauncherWidth(width int) {
	appConfig.Set("launcher_width", width)
	saveConfig(appConfig)
}

// getHomeDir returns the user's home directory path
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
func showAboutDialog(parent *qt.QWidget) {
	aboutText := fmt.Sprintf(`<h2>PawScript</h2>
<p>Version: %s</p>
<p><i>A scripting language for creative coding</i></p>
<p>Copyright © 2025 Jeffrey R. Day<br>
License: MIT</p>
<p><small>Permission is hereby granted, free of charge, to any person
obtaining a copy of this software and associated documentation
files (the "Software"), to deal in the Software without restriction,
including without limitation the rights to use, copy, modify, merge,
publish, distribute, sublicense, and/or sell copies of the Software.</small></p>`, version)

	qt.QMessageBox_About(parent, "About PawScript", aboutText)
}

// createHamburgerMenu creates the hamburger dropdown menu
// isScriptWindow: true for script windows (slightly different options)
func createHamburgerMenu(parent *qt.QWidget, isScriptWindow bool) *qt.QMenu {
	menu := qt.NewQMenu2()

	// About option
	aboutAction := menu.AddAction("About PawScript...")
	aboutAction.OnTriggered(func() {
		showAboutDialog(parent)
	})

	return menu
}

// createHamburgerButton creates a hamburger menu button (☰ icon)
func createHamburgerButton(menu *qt.QMenu) *qt.QPushButton {
	btn := qt.NewQPushButton3("☰")
	btn.SetToolTip("Menu")
	// Style to match other buttons with square form and proper padding
	btn.SetStyleSheet("padding: 5px 10px;")
	btn.SetSizePolicy(*qt.NewQSizePolicy2(qt.QSizePolicy__Fixed, qt.QSizePolicy__Fixed))
	// Don't use SetMenu() as it adds a dropdown arrow - manually pop up the menu on click
	btn.OnClicked(func() {
		// Show menu at the button's position
		menu.Popup(btn.MapToGlobal(btn.Rect().BottomLeft()))
	})
	return btn
}

// createToolbarStrip creates a vertical strip of toolbar buttons
// Returns the strip container, the hamburger button, and the menu
func createToolbarStrip(parent *qt.QWidget, isScriptWindow bool) (*qt.QWidget, *qt.QPushButton, *qt.QMenu) {
	strip := qt.NewQWidget2()
	layout := qt.NewQVBoxLayout2()
	layout.SetContentsMargins(2, 5, 2, 5)
	layout.SetSpacing(2)

	// Create hamburger menu and button
	menu := createHamburgerMenu(parent, isScriptWindow)
	menuBtn := createHamburgerButton(menu)

	layout.AddWidget(menuBtn.QWidget)
	layout.AddStretch() // Push buttons to top
	strip.SetLayout(layout.QLayout)

	return strip, menuBtn, menu
}

// updateLauncherToolbarButtons updates the launcher's narrow strip with the current registered buttons
func updateLauncherToolbarButtons() {
	if launcherNarrowStrip == nil {
		return
	}

	// Get the strip's layout
	layout := launcherNarrowStrip.Layout()
	if layout == nil {
		return
	}
	vbox := qt.UnsafeNewQVBoxLayout(layout.UnsafePointer())

	// Remove existing dummy buttons (but keep the hamburger menu button and stretch at the end)
	// We skip index 0 (hamburger) and the stretch item at the end
	for vbox.Count() > 2 {
		item := vbox.TakeAt(1)
		if item != nil && item.Widget() != nil {
			item.Widget().DeleteLater()
		}
	}

	// Add new dummy buttons (insert after hamburger button, before stretch)
	for _, btn := range launcherRegisteredBtns {
		button := qt.NewQPushButton3(btn.Icon)
		button.SetFixedSize2(32, 32)
		button.SetToolTip(btn.Tooltip)
		if btn.OnClick != nil {
			callback := btn.OnClick // Capture for closure
			button.OnClicked(func() {
				callback()
			})
		}
		btn.widget = button
		vbox.InsertWidget(vbox.Count()-1, button.QWidget) // Insert before stretch
	}

	// Update visibility based on button count
	hasMultipleButtons := len(launcherRegisteredBtns) > 0
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

// setDummyButtons sets the number of dummy buttons in the toolbar strip
func setDummyButtons(count int) {
	// Clear existing dummy buttons
	launcherRegisteredBtns = nil

	// Add new dummy buttons
	for i := 0; i < count; i++ {
		icon := dummyIcons[i%len(dummyIcons)]
		idx := i // Capture for closure
		btn := &QtToolbarButton{
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

	// Update the toolbar strip on Qt main thread using a single-shot timer
	if mainWindow != nil {
		timer := qt.NewQTimer2(mainWindow.QObject)
		timer.SetSingleShot(true)
		timer.OnTimeout(func() {
			updateLauncherToolbarButtons()
		})
		timer.Start(0)
	}
}

// registerDummyButtonCommand registers the dummy_button command with PawScript
func registerDummyButtonCommand(ps *pawscript.PawScript) {
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

		setDummyButtons(count)
		ctx.SetResult(count)
		return pawscript.BoolStatus(true)
	})
}

// applyTheme sets the Qt application palette based on the configuration.
// "auto" = let Qt/OS decide, "dark" = force dark palette, "light" = force light palette
func applyTheme(theme pawgui.ThemeMode) {
	if qtApp == nil {
		return
	}

	switch theme {
	case pawgui.ThemeDark:
		// Create a dark palette using stylesheet for better cross-platform support
		qtApp.SetStyleSheet(`
			QWidget {
				background-color: #353535;
				color: #ffffff;
			}
			QMainWindow, QDialog {
				background-color: #353535;
			}
			QPushButton {
				background-color: #454545;
				border: 1px solid #555555;
				padding: 5px 15px;
				border-radius: 3px;
			}
			QPushButton:hover {
				background-color: #505050;
			}
			QPushButton:pressed {
				background-color: #404040;
			}
			QListWidget {
				background-color: #252525;
				border: 1px solid #454545;
			}
			QListWidget::item:selected {
				background-color: #2a82da;
			}
			QLabel {
				background-color: transparent;
			}
			QSplitter::handle {
				background-color: #454545;
			}
			QScrollBar:vertical, QAbstractScrollArea QScrollBar:vertical, QListWidget QScrollBar:vertical {
				background: transparent;
				width: 12px;
				margin: 2px 2px 2px 0px;
			}
			QScrollBar::handle:vertical, QAbstractScrollArea QScrollBar::handle:vertical, QListWidget QScrollBar::handle:vertical {
				background: rgba(255, 255, 255, 0.3);
				min-height: 30px;
				border-radius: 4px;
				margin: 0px 2px 0px 2px;
			}
			QScrollBar::handle:vertical:hover {
				background: rgba(255, 255, 255, 0.5);
			}
			QScrollBar::handle:vertical:pressed {
				background: rgba(255, 255, 255, 0.6);
			}
			QScrollBar::add-line:vertical, QScrollBar::sub-line:vertical {
				height: 0px;
			}
			QScrollBar::add-page:vertical, QScrollBar::sub-page:vertical {
				background: transparent;
			}
			QScrollBar:horizontal, QAbstractScrollArea QScrollBar:horizontal, QListWidget QScrollBar:horizontal {
				background: transparent;
				height: 12px;
				margin: 0px 2px 2px 2px;
			}
			QScrollBar::handle:horizontal, QAbstractScrollArea QScrollBar::handle:horizontal, QListWidget QScrollBar::handle:horizontal {
				background: rgba(255, 255, 255, 0.3);
				min-width: 30px;
				border-radius: 4px;
				margin: 2px 0px 2px 0px;
			}
			QScrollBar::handle:horizontal:hover {
				background: rgba(255, 255, 255, 0.5);
			}
			QScrollBar::handle:horizontal:pressed {
				background: rgba(255, 255, 255, 0.6);
			}
			QScrollBar::add-line:horizontal, QScrollBar::sub-line:horizontal {
				width: 0px;
			}
			QScrollBar::add-page:horizontal, QScrollBar::sub-page:horizontal {
				background: transparent;
			}
		`)

	case pawgui.ThemeLight:
		// Create a light palette using stylesheet
		qtApp.SetStyleSheet(`
			QWidget {
				background-color: #f0f0f0;
				color: #000000;
			}
			QMainWindow, QDialog {
				background-color: #f0f0f0;
			}
			QPushButton {
				background-color: #e0e0e0;
				border: 1px solid #c0c0c0;
				padding: 5px 15px;
				border-radius: 3px;
			}
			QPushButton:hover {
				background-color: #d0d0d0;
			}
			QPushButton:pressed {
				background-color: #c0c0c0;
			}
			QListWidget {
				background-color: #ffffff;
				border: 1px solid #c0c0c0;
			}
			QListWidget::item:selected {
				background-color: #0078d7;
				color: #ffffff;
			}
			QLabel {
				background-color: transparent;
			}
			QSplitter::handle {
				background-color: #c0c0c0;
			}
			QScrollBar:vertical, QAbstractScrollArea QScrollBar:vertical, QListWidget QScrollBar:vertical {
				background: transparent;
				width: 12px;
				margin: 2px 2px 2px 0px;
			}
			QScrollBar::handle:vertical, QAbstractScrollArea QScrollBar::handle:vertical, QListWidget QScrollBar::handle:vertical {
				background: rgba(0, 0, 0, 0.3);
				min-height: 30px;
				border-radius: 4px;
				margin: 0px 2px 0px 2px;
			}
			QScrollBar::handle:vertical:hover {
				background: rgba(0, 0, 0, 0.5);
			}
			QScrollBar::handle:vertical:pressed {
				background: rgba(0, 0, 0, 0.6);
			}
			QScrollBar::add-line:vertical, QScrollBar::sub-line:vertical {
				height: 0px;
			}
			QScrollBar::add-page:vertical, QScrollBar::sub-page:vertical {
				background: transparent;
			}
			QScrollBar:horizontal, QAbstractScrollArea QScrollBar:horizontal, QListWidget QScrollBar:horizontal {
				background: transparent;
				height: 12px;
				margin: 0px 2px 2px 2px;
			}
			QScrollBar::handle:horizontal, QAbstractScrollArea QScrollBar::handle:horizontal, QListWidget QScrollBar::handle:horizontal {
				background: rgba(0, 0, 0, 0.3);
				min-width: 30px;
				border-radius: 4px;
				margin: 2px 0px 2px 0px;
			}
			QScrollBar::handle:horizontal:hover {
				background: rgba(0, 0, 0, 0.5);
			}
			QScrollBar::handle:horizontal:pressed {
				background: rgba(0, 0, 0, 0.6);
			}
			QScrollBar::add-line:horizontal, QScrollBar::sub-line:horizontal {
				width: 0px;
			}
			QScrollBar::add-page:horizontal, QScrollBar::sub-page:horizontal {
				background: transparent;
			}
		`)

	case pawgui.ThemeAuto:
		// Let Qt use the system default - no explicit setting needed
		// Qt will follow the OS dark/light mode preference on supported platforms
	}
}

// applyUIScale applies UI scaling via stylesheet (does not affect terminal)
// Qt uses 1.75x the config scale to match visual appearance with GTK
func applyUIScale(scale float64) {
	if qtApp == nil {
		return
	}

	// Qt needs 1.75x scale factor to match GTK visual appearance
	effectiveScale := scale * 1.75

	baseFontSize := int(12.0 * effectiveScale)
	buttonPadding := int(5.0 * effectiveScale)
	buttonPaddingH := int(15.0 * effectiveScale)

	// Get existing stylesheet and append scaling rules
	existing := qtApp.StyleSheet()
	scaled := fmt.Sprintf(`
		QWidget {
			font-size: %dpx;
		}
		QPushButton {
			padding: %dpx %dpx;
			font-size: %dpx;
		}
		QLabel {
			font-size: %dpx;
		}
		QListWidget {
			font-size: %dpx;
		}
	`, baseFontSize, buttonPadding, buttonPaddingH, baseFontSize, baseFontSize, baseFontSize)

	qtApp.SetStyleSheet(existing + scaled)
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
	launchGUIMode()
}

// launchGUIMode starts the Qt application in launcher mode (file browser + terminal)
func launchGUIMode() {
	// Load configuration
	appConfig = loadConfig()
	configHelper = pawgui.NewConfigHelper(appConfig)

	// Auto-populate config with defaults (makes them discoverable)
	if configHelper.PopulateDefaults() {
		saveConfig(appConfig)
	}

	// Get initial directory
	currentDir = appConfig.GetString("last_browse_dir", "")
	if currentDir == "" {
		currentDir, _ = os.Getwd()
	}

	// Initialize Qt application
	qtApp = qt.NewQApplication(os.Args)

	// Apply theme setting
	applyTheme(configHelper.GetTheme())

	// Apply UI scaling via stylesheet (affects everything except terminal)
	applyUIScale(getUIScale())

	// Create main window
	mainWindow = qt.NewQMainWindow2()
	mainWindow.SetWindowTitle(appName)
	mainWindow.Resize(1100, 700)

	// Create central widget with horizontal splitter
	centralWidget := qt.NewQWidget2()
	mainLayout := qt.NewQHBoxLayout2()
	mainLayout.SetContentsMargins(0, 0, 0, 0)
	mainLayout.SetSpacing(0)
	centralWidget.SetLayout(mainLayout.QLayout)

	// Create splitter
	splitter := qt.NewQSplitter3(qt.Horizontal)

	// Left container: holds wide panel (file browser) and narrow strip side by side
	leftContainer := qt.NewQWidget2()
	leftLayout := qt.NewQHBoxLayout2()
	leftLayout.SetContentsMargins(0, 0, 0, 0)
	leftLayout.SetSpacing(0)
	leftContainer.SetLayout(leftLayout.QLayout)

	// Wide panel (file browser)
	widePanel := createFilePanel()
	leftLayout.AddWidget2(widePanel, 1)

	// Narrow strip: toolbar buttons (created but hidden initially - only 1 button)
	launcherNarrowStrip, launcherStripMenuBtn, _ = createToolbarStrip(leftContainer, false)
	launcherNarrowStrip.SetFixedWidth(minNarrowStripWidth) // Fixed width
	launcherNarrowStrip.Hide() // Hidden initially since we only have 1 button
	leftLayout.AddWidget(launcherNarrowStrip)

	// Initially: hamburger button visible in path selector, narrow strip hidden
	launcherMenuButton.Show()

	splitter.AddWidget(leftContainer)

	// Right panel (terminal)
	rightPanel := createTerminalPanel()
	splitter.AddWidget(rightPanel)

	// Set initial splitter sizes using saved launcher width (default 280)
	launcherWidth := getLauncherWidth()
	splitter.SetSizes([]int{launcherWidth, 900 - launcherWidth})

	// Configure stretch factors so left panel stays fixed and right panel is flexible
	// This matches the GTK behavior where additional space goes to the console
	splitter.SetStretchFactor(0, 0) // Left panel: fixed size (doesn't stretch)
	splitter.SetStretchFactor(1, 1) // Right panel: flexible (absorbs size changes)

	// Save launcher width when user adjusts the splitter
	// Implement multi-stage collapse:
	// - Wide + narrow mode: when pos >= minWidePanelWidth + minNarrowStripWidth (with multiple buttons)
	// - Narrow only mode: when pos >= minNarrowStripWidth but < threshold for wide panel
	// - Collapsed: when pos < minNarrowStripWidth
	splitter.OnSplitterMoved(func(pos int, index int) {
		if index != 1 {
			return
		}
		hasMultipleButtons := len(launcherRegisteredBtns) > 0

		// Calculate threshold for showing both panels (wide panel needs its min width plus narrow strip width)
		bothThreshold := minWidePanelWidth + minNarrowStripWidth

		if pos < minNarrowStripWidth {
			// Too narrow even for strip - collapse fully
			splitter.SetSizes([]int{0, splitter.Width()})
		} else if hasMultipleButtons && pos < bothThreshold {
			// Multiple buttons mode: between narrow strip width and both-panels threshold
			// Show only narrow strip at its fixed width
			launcherWidePanel.Hide()
			launcherNarrowStrip.Show()
			launcherMenuButton.Hide()
			launcherStripMenuBtn.Show()
			// Snap to just the narrow strip width
			if pos != minNarrowStripWidth {
				splitter.SetSizes([]int{minNarrowStripWidth, splitter.Width() - minNarrowStripWidth})
			}
			saveLauncherWidth(minNarrowStripWidth)
		} else if !hasMultipleButtons && pos < minWidePanelWidth {
			// Single button mode, below wide threshold - collapse
			splitter.SetSizes([]int{0, splitter.Width()})
		} else {
			// Wide enough for full panel
			launcherWidePanel.Show()
			if hasMultipleButtons {
				launcherNarrowStrip.Show()
				launcherMenuButton.Hide()
				launcherStripMenuBtn.Show()
			} else {
				launcherNarrowStrip.Hide()
				launcherMenuButton.Show()
			}
			saveLauncherWidth(pos)
		}
	})

	mainLayout.AddWidget(splitter.QWidget)
	mainWindow.SetCentralWidget(centralWidget)

	// Set up console I/O
	setupConsoleIO()

	// Print welcome banner before REPL starts (so prompt appears after)
	terminal.Feed(fmt.Sprintf("pawgui-qt, the PawScript GUI interpreter version %s (with Qt)\r\n", version))
	terminal.Feed("Copyright (c) 2025 Jeffrey R. Day\r\n")
	terminal.Feed("License: MIT\r\n\r\n")
	terminal.Feed("Interactive mode. Type 'exit' or 'quit' to leave.\r\n")
	terminal.Feed("Select a .paw file and click Run to execute.\r\n\r\n")

	// Start REPL (prompt will appear after welcome message)
	startREPL()

	// Load initial directory
	loadDirectory(currentDir)

	// Start UI update timer (250ms) for path button elision and future UI updates
	uiUpdateTimer := qt.NewQTimer2(mainWindow.QObject)
	uiUpdateTimer.OnTimeout(func() {
		updatePathButtonText()
	})
	uiUpdateTimer.Start(250)

	// Set up quit shortcut based on config
	setupQuitShortcut()

	// Set up tab order: pathButton -> fileList -> runButton -> browseButton -> terminal
	qt.QWidget_SetTabOrder(pathButton.QWidget, fileList.QWidget)
	qt.QWidget_SetTabOrder(fileList.QWidget, runButton.QWidget)
	qt.QWidget_SetTabOrder(runButton.QWidget, browseButton.QWidget)
	qt.QWidget_SetTabOrder(browseButton.QWidget, terminal.Widget())

	// Show window
	mainWindow.Show()

	// Focus the Run button by default
	runButton.SetFocus()

	// Run application
	qt.QApplication_Exec()
}

// runScriptFromCLI executes a script provided via command line
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

	// Window mode - create Qt application with console window
	runScriptInWindow(scriptContent, scriptFile, scriptArgs, fileAccess, optLevel, scriptDir)
}

// runScriptInWindow creates a Qt console window and runs the script
func runScriptInWindow(scriptContent, scriptFile string, scriptArgs []string,
	fileAccess *pawscript.FileAccessConfig, optLevel int, scriptDir string) {

	// Load configuration
	appConfig = loadConfig()
	configHelper = pawgui.NewConfigHelper(appConfig)
	if configHelper.PopulateDefaults() {
		saveConfig(appConfig)
	}

	// Initialize Qt application
	qtApp = qt.NewQApplication(os.Args)
	applyTheme(configHelper.GetTheme())

	// Create console window
	win := qt.NewQMainWindow2()
	title := "PawScript Console"
	if scriptFile != "" {
		title = filepath.Base(scriptFile) + " - PawScript"
	}
	win.SetWindowTitle(title)
	win.Resize(900, 600)

	// Create terminal
	winTerminal, err := purfectermqt.New(purfectermqt.Options{
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
		os.Exit(1)
	}

	// Set font fallbacks
	winTerminal.SetFontFallbacks(getFontFamilyUnicode(), getFontFamilyCJK())

	// Create splitter for toolbar strip + terminal
	winSplitter := qt.NewQSplitter3(qt.Horizontal)

	// Create toolbar strip for this window (script windows only have narrow strip, no wide panel)
	winNarrowStrip, winStripMenuBtn, _ := createToolbarStrip(win.QWidget, true)
	winNarrowStrip.SetFixedWidth(minNarrowStripWidth)
	// Start visible with hamburger menu
	winNarrowStrip.Show()
	winStripMenuBtn.Show()

	winSplitter.AddWidget(winNarrowStrip)
	winSplitter.AddWidget(winTerminal.Widget())

	// Set stretch factors so strip is fixed and terminal is flexible
	winSplitter.SetStretchFactor(0, 0)
	winSplitter.SetStretchFactor(1, 1)

	// Set initial sizes
	winSplitter.SetSizes([]int{minNarrowStripWidth, 900 - minNarrowStripWidth})

	// Script windows only have two positions: 0 (collapsed) or minNarrowStripWidth (visible)
	winSplitter.OnSplitterMoved(func(pos int, index int) {
		if index != 1 {
			return
		}
		if pos == 0 {
			// Already collapsed, do nothing
		} else if pos < minNarrowStripWidth/2 {
			// Less than half - snap to collapsed
			winSplitter.SetSizes([]int{0, winSplitter.Width()})
		} else if pos != minNarrowStripWidth {
			// More than half but not at fixed width - snap to visible
			winSplitter.SetSizes([]int{minNarrowStripWidth, winSplitter.Width() - minNarrowStripWidth})
		}
	})

	win.SetCentralWidget(winSplitter.QWidget)

	// Create I/O channels for this window
	winStdinReader, winStdinWriter := io.Pipe()

	width, height := 100, 30
	winTermCaps := &pawscript.TerminalCapabilities{
		TermType:      "gui-console",
		IsTerminal:    true,
		SupportsANSI:  true,
		SupportsColor: true,
		ColorDepth:    256,
		Width:         width,
		Height:        height,
		SupportsInput: true,
		EchoEnabled:   false,
		LineMode:      false,
		Metadata:      make(map[string]interface{}),
	}

	// Non-blocking output queue
	winOutputQueue := make(chan interface{}, 256)
	go func() {
		for item := range winOutputQueue {
			switch v := item.(type) {
			case []byte:
				winTerminal.Feed(string(v))
			case string:
				winTerminal.Feed(v)
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
		Terminal:         winTermCaps,
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
			select {
			case winOutputQueue <- []byte(text):
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
			case winOutputQueue <- writerDone:
				<-writerDone
			default:
			}
			return nil
		},
	}

	// Non-blocking input queue
	winInputQueue := make(chan byte, 256)
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := winStdinReader.Read(buf)
			if err != nil || n == 0 {
				close(winInputQueue)
				return
			}
			select {
			case winInputQueue <- buf[0]:
			default:
				select {
				case <-winInputQueue:
				default:
				}
				select {
				case winInputQueue <- buf[0]:
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
		Terminal:         winTermCaps,
		NativeRecv: func() (interface{}, error) {
			b, ok := <-winInputQueue
			if !ok {
				return nil, fmt.Errorf("input closed")
			}
			return []byte{b}, nil
		},
		NativeSend: func(v interface{}) error {
			return fmt.Errorf("cannot send to console_in")
		},
	}

	// Wire keyboard input
	winTerminal.SetInputCallback(func(data []byte) {
		winStdinWriter.Write(data)
	})

	win.Show()

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

	ioConfig := &pawscript.IOChannelConfig{
		Stdout: winOutCh,
		Stdin:  winInCh,
		Stderr: winOutCh,
	}
	ps.RegisterStandardLibraryWithIO(scriptArgs, ioConfig)

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
	}()

	qt.QApplication_Exec()
}

// setupQuitShortcut configures the keyboard shortcut to quit the application
func setupQuitShortcut() {
	quitShortcut := getQuitShortcut()
	if quitShortcut == "" {
		return // Disabled
	}

	var keySequence string
	switch quitShortcut {
	case "Cmd+Q":
		// On macOS, Qt swaps Ctrl/Meta, so "Ctrl+Q" responds to physical Cmd key
		// On other platforms, use Meta+Q
		if runtime.GOOS == "darwin" {
			keySequence = "Ctrl+Q" // Physical Cmd+Q on macOS
		} else {
			keySequence = "Meta+Q"
		}
	case "Ctrl+Q":
		// Note: Ctrl+Q should generally not be used (conflicts with terminal)
		// On macOS, Qt's "Meta+Q" responds to physical Ctrl key
		if runtime.GOOS == "darwin" {
			keySequence = "Meta+Q" // Physical Ctrl+Q on macOS
		} else {
			keySequence = "Ctrl+Q"
		}
	case "Alt+F4":
		keySequence = "Alt+F4"
	default:
		return
	}

	shortcut := qt.NewQShortcut2(qt.NewQKeySequence2(keySequence), mainWindow.QWidget)
	shortcut.OnActivated(func() {
		mainWindow.Close()
	})
}

func createFilePanel() *qt.QWidget {
	panel := qt.NewQWidget2()
	layout := qt.NewQVBoxLayout2()
	layout.SetContentsMargins(4, 4, 4, 4)
	layout.SetSpacing(4)
	panel.SetLayout(layout.QLayout)

	// Store reference for collapse handling
	launcherWidePanel = panel

	// Top row: path selector + hamburger menu button
	topRow := qt.NewQWidget2()
	topRowLayout := qt.NewQHBoxLayout2()
	topRowLayout.SetContentsMargins(0, 0, 0, 0)
	topRowLayout.SetSpacing(4)
	topRow.SetLayout(topRowLayout.QLayout)

	// Path selector button with dropdown menu - styled like other buttons
	pathButton = qt.NewQPushButton3("")
	pathButton.SetSizePolicy(*qt.NewQSizePolicy2(qt.QSizePolicy__Ignored, qt.QSizePolicy__Fixed))
	pathButton.SetStyleSheet("text-align: left; padding-left: 6px;")

	// Create the dropdown menu
	pathMenu = qt.NewQMenu2()
	pathButton.SetMenu(pathMenu)

	topRowLayout.AddWidget2(pathButton.QWidget, 1)

	// Hamburger menu button (shown when narrow strip is hidden)
	launcherMenu := createHamburgerMenu(panel, false)
	launcherMenuButton = createHamburgerButton(launcherMenu)
	topRowLayout.AddWidget(launcherMenuButton.QWidget)

	layout.AddWidget(topRow)

	// File list
	fileList = qt.NewQListWidget2()
	fileList.OnItemDoubleClicked(func(item *qt.QListWidgetItem) {
		handleFileActivated(item)
	})
	fileList.OnCurrentItemChanged(func(current *qt.QListWidgetItem, previous *qt.QListWidgetItem) {
		onSelectionChanged(current)
	})
	layout.AddWidget2(fileList.QWidget, 1)

	// Run and Browse buttons
	buttonLayout := qt.NewQHBoxLayout2()

	runButton = qt.NewQPushButton3("Run")
	runButton.OnClicked(func() { runSelectedFile() })
	buttonLayout.AddWidget(runButton.QWidget)

	browseButton = qt.NewQPushButton3("Browse...")
	browseButton.OnClicked(func() { browseFolder() })
	buttonLayout.AddWidget(browseButton.QWidget)

	layout.AddLayout(buttonLayout.QLayout)

	return panel
}

func createTerminalPanel() *qt.QWidget {
	panel := qt.NewQWidget2()
	layout := qt.NewQVBoxLayout2()
	layout.SetContentsMargins(0, 0, 0, 0)
	layout.SetSpacing(0)
	panel.SetLayout(layout.QLayout)

	// Create terminal with color scheme from config
	var err error
	terminal, err = purfectermqt.New(purfectermqt.Options{
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
		os.Exit(1)
	}

	// Set font fallbacks for Unicode/CJK characters
	terminal.SetFontFallbacks(getFontFamilyUnicode(), getFontFamilyCJK())

	layout.AddWidget2(terminal.Widget(), 1)

	return panel
}

func setupConsoleIO() {
	// Create pipes for stdin
	stdinReader, stdinWriter = io.Pipe()

	// Get terminal capabilities from the widget (auto-updates on resize)
	termCaps := terminal.GetTerminalCapabilities()

	// Output queue for non-blocking writes to terminal
	outputQueue := make(chan interface{}, 256)

	// Start output writer goroutine
	go func() {
		for v := range outputQueue {
			switch d := v.(type) {
			case []byte:
				terminal.Feed(string(d))
			case string:
				terminal.Feed(d)
			case chan struct{}:
				// Sentinel for flush synchronization
				close(d)
			}
		}
	}()

	// Create console output channel
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
			// Normalize newlines for terminal
			text = strings.ReplaceAll(text, "\r\n", "\n")
			text = strings.ReplaceAll(text, "\n", "\r\n")
			select {
			case outputQueue <- []byte(text):
			default:
				// Queue full - drop to prevent deadlock
			}
			return nil
		},
		NativeRecv: func() (interface{}, error) {
			return nil, fmt.Errorf("cannot receive from console_out")
		},
		NativeFlush: func() error {
			// Wait for outputQueue to drain
			writerDone := make(chan struct{})
			select {
			case outputQueue <- writerDone:
				<-writerDone
			default:
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

	// Create console input channel
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
}

func startREPL() {
	// Create and start the REPL for interactive mode
	// ShowBanner is false because we print our own welcome message before starting
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
		// Output to terminal
		terminal.Feed(s)
	})
	// Set flush callback to ensure output appears before blocking execution
	consoleREPL.SetFlush(func() {
		// Force immediate repaint to display output before blocking operations
		terminal.Flush()
	})
	// Set background color for prompt color selection
	bg := getTerminalBackground()
	consoleREPL.SetBackgroundRGB(bg.R, bg.G, bg.B)
	consoleREPL.SetPSLColors(getPSLColors())
	consoleREPL.Start()

	// Register the dummy_button command with the REPL's PawScript instance
	registerDummyButtonCommand(consoleREPL.GetPawScript())
}

// fileItemData stores path and isDir for list items
type fileItemData struct {
	path  string
	isDir bool
}

var fileItemDataMap = make(map[unsafe.Pointer]fileItemData)
var fileItemDataMu sync.Mutex

// updatePathButtonText updates the button text with elision based on current width
func updatePathButtonText() {
	if pathButton == nil {
		return
	}
	// Compute elided text to fit in button width (elide at start to show end of path)
	buttonWidth := pathButton.Width() - 40 // Leave room for dropdown arrow and padding
	if buttonWidth < 50 {
		buttonWidth = 50
	}
	fm := qt.NewQFontMetrics(pathButton.Font())
	elidedText := fm.ElidedText(currentDir, qt.ElideLeft, buttonWidth)
	pathButton.SetText(elidedText)
}

// updatePathMenu populates the path menu with Home, Examples, recent paths, and Clear option
func updatePathMenu() {
	if pathButton == nil || pathMenu == nil {
		return
	}

	// Update button text
	updatePathButtonText()

	// Clear existing menu items
	pathMenu.Clear()

	// Add current path as disabled info item
	currentAction := pathMenu.AddAction(currentDir)
	currentAction.SetEnabled(false)

	pathMenu.AddSeparator()

	// Add Home directory
	if home := getHomeDir(); home != "" {
		homeAction := pathMenu.AddAction("🏠 Home")
		homeAction.OnTriggered(func() {
			if info, err := os.Stat(home); err == nil && info.IsDir() {
				loadDirectory(home)
			}
		})
	}

	// Add Examples directory
	if examples := getExamplesDir(); examples != "" {
		examplesAction := pathMenu.AddAction("📁 Examples")
		examplesAction.OnTriggered(func() {
			if info, err := os.Stat(examples); err == nil && info.IsDir() {
				loadDirectory(examples)
			}
		})
	}

	// Add recent paths
	recentPaths := getRecentPaths()
	if len(recentPaths) > 0 {
		pathMenu.AddSeparator()
		for _, p := range recentPaths {
			path := p // Capture for closure
			action := pathMenu.AddAction(path)
			action.OnTriggered(func() {
				if info, err := os.Stat(path); err == nil && info.IsDir() {
					loadDirectory(path)
				}
			})
		}
	}

	// Add Clear Recent Paths option
	if len(recentPaths) > 0 {
		pathMenu.AddSeparator()
		clearAction := pathMenu.AddAction("🗑 Clear Recent Paths")
		clearAction.OnTriggered(func() {
			clearRecentPaths()
			updatePathMenu()
		})
	}
}

func loadDirectory(dir string) {
	currentDir = dir
	updatePathMenu()

	fileList.Clear()

	// Clear old item data
	fileItemDataMu.Lock()
	fileItemDataMap = make(map[unsafe.Pointer]fileItemData)
	fileItemDataMu.Unlock()

	entries, err := os.ReadDir(dir)
	if err != nil {
		terminal.Feed(fmt.Sprintf("Error reading directory: %v\r\n", err))
		return
	}

	// Get standard icons from Qt style (may be nil before event loop)
	var folderIcon, fileIcon, upIcon *qt.QIcon
	if style := qt.QApplication_Style(); style != nil {
		folderIcon = style.StandardIcon(qt.QStyle__SP_DirIcon, nil, nil)
		fileIcon = style.StandardIcon(qt.QStyle__SP_FileIcon, nil, nil)
		upIcon = style.StandardIcon(qt.QStyle__SP_ArrowUp, nil, nil)
	}

	// Add parent directory entry (except at root)
	if dir != "/" && filepath.Dir(dir) != dir {
		item := qt.NewQListWidgetItem7("..", fileList)
		if upIcon != nil {
			item.SetIcon(upIcon)
		}
		fileItemDataMu.Lock()
		fileItemDataMap[item.UnsafePointer()] = fileItemData{
			path:  filepath.Dir(dir),
			isDir: true,
		}
		fileItemDataMu.Unlock()
	}

	// Add directories first
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			item := qt.NewQListWidgetItem7(entry.Name(), fileList)
			if folderIcon != nil {
				item.SetIcon(folderIcon)
			}
			// Store data using pointer map
			fileItemDataMu.Lock()
			fileItemDataMap[item.UnsafePointer()] = fileItemData{
				path:  filepath.Join(dir, entry.Name()),
				isDir: true,
			}
			fileItemDataMu.Unlock()
		}
	}

	// Add .paw files (case-insensitive)
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".paw") {
			item := qt.NewQListWidgetItem7(entry.Name(), fileList)
			if fileIcon != nil {
				item.SetIcon(fileIcon)
			}
			// Store data using pointer map
			fileItemDataMu.Lock()
			fileItemDataMap[item.UnsafePointer()] = fileItemData{
				path:  filepath.Join(dir, entry.Name()),
				isDir: false,
			}
			fileItemDataMu.Unlock()
		}
	}

	saveBrowseDir(dir)
}

func handleFileActivated(item *qt.QListWidgetItem) {
	fileItemDataMu.Lock()
	data, ok := fileItemDataMap[item.UnsafePointer()]
	fileItemDataMu.Unlock()

	if !ok {
		return
	}

	if data.isDir {
		loadDirectory(data.path)
	} else {
		runScript(data.path)
	}
}

func navigateUp() {
	parent := filepath.Dir(currentDir)
	if parent != currentDir {
		loadDirectory(parent)
	}
}

func onSelectionChanged(item *qt.QListWidgetItem) {
	if item == nil || runButton == nil {
		return
	}

	fileItemDataMu.Lock()
	data, ok := fileItemDataMap[item.UnsafePointer()]
	fileItemDataMu.Unlock()

	if !ok {
		runButton.SetText("Run")
		return
	}

	if data.isDir {
		runButton.SetText("Open")
	} else {
		runButton.SetText("Run")
	}
}

func browseFolder() {
	// Open file dialog filtered to .paw files
	file := qt.QFileDialog_GetOpenFileName4(
		mainWindow.QWidget,
		"Open PawScript File",
		currentDir,
		"PawScript files (*.paw);;All files (*)",
	)
	if file != "" {
		// Navigate to the file's directory and run the script
		currentDir = filepath.Dir(file)
		loadDirectory(currentDir)
		runScript(file)
	}
}

func runSelectedFile() {
	items := fileList.SelectedItems()
	if len(items) == 0 {
		terminal.Feed("No file selected.\r\n")
		return
	}

	item := items[0]
	fileItemDataMu.Lock()
	data, ok := fileItemDataMap[item.UnsafePointer()]
	fileItemDataMu.Unlock()

	if !ok {
		return
	}

	if data.isDir {
		loadDirectory(data.path)
	} else {
		runScript(data.path)
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
				terminal.Feed(s)
			})
			// Set flush callback to ensure output appears before blocking execution
			consoleREPL.SetFlush(func() {
				// Force immediate repaint to display output before blocking operations
				terminal.Flush()
			})
			// Set background color for prompt color selection
			bg := getTerminalBackground()
			consoleREPL.SetBackgroundRGB(bg.R, bg.G, bg.B)
			consoleREPL.SetPSLColors(getPSLColors())
			consoleREPL.Start()

			// Re-register the dummy_button command with the new REPL instance
			registerDummyButtonCommand(consoleREPL.GetPawScript())
		}
	}()
}

// createConsoleWindow creates a new window with just a terminal (no launcher UI)
// for running a script when the main window already has a script running
func createConsoleWindow(filePath string) {
	// Create new window
	win := qt.NewQMainWindow2()
	win.SetWindowTitle(fmt.Sprintf("PawScript - %s", filepath.Base(filePath)))
	win.SetMinimumSize2(900, 600)

	// Create terminal for this window with color scheme from config
	winTerminal, err := purfectermqt.New(purfectermqt.Options{
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
		terminal.Feed(fmt.Sprintf("\r\nFailed to create console window: %v\r\n", err))
		win.Close()
		return
	}

	// Create splitter for toolbar strip + terminal
	winSplitter := qt.NewQSplitter3(qt.Horizontal)

	// Create toolbar strip for this window (script windows only have narrow strip, no wide panel)
	winNarrowStrip, winStripMenuBtn, _ := createToolbarStrip(win.QWidget, true)
	winNarrowStrip.SetFixedWidth(minNarrowStripWidth)
	// Start visible if there are registered buttons, hidden otherwise
	hasMultipleButtons := len(launcherRegisteredBtns) > 0
	if hasMultipleButtons {
		winNarrowStrip.Show()
		winStripMenuBtn.Show()
	} else {
		winNarrowStrip.Hide()
	}

	winSplitter.AddWidget(winNarrowStrip)
	winSplitter.AddWidget(winTerminal.Widget())

	// Set stretch factors so strip is fixed and terminal is flexible
	winSplitter.SetStretchFactor(0, 0)
	winSplitter.SetStretchFactor(1, 1)

	// Set initial sizes
	if hasMultipleButtons {
		winSplitter.SetSizes([]int{minNarrowStripWidth, 900 - minNarrowStripWidth})
	} else {
		winSplitter.SetSizes([]int{0, 900})
	}

	// Script windows only have two positions: 0 (collapsed) or minNarrowStripWidth (visible)
	winSplitter.OnSplitterMoved(func(pos int, index int) {
		if index != 1 {
			return
		}
		if pos == 0 {
			// Already collapsed, do nothing
		} else if pos < minNarrowStripWidth/2 {
			// Less than half - snap to collapsed
			winSplitter.SetSizes([]int{0, winSplitter.Width()})
		} else if pos != minNarrowStripWidth {
			// More than half but not at fixed width - snap to visible
			winSplitter.SetSizes([]int{minNarrowStripWidth, winSplitter.Width() - minNarrowStripWidth})
		}
	})

	win.SetCentralWidget(winSplitter.QWidget)

	// Create I/O channels for this window's console
	winStdinReader, winStdinWriter := io.Pipe()

	// Terminal capabilities for this window
	winWidth, winHeight := 100, 30
	winTermCaps := &pawscript.TerminalCapabilities{
		TermType:      "gui-console",
		IsTerminal:    true,
		SupportsANSI:  true,
		SupportsColor: true,
		ColorDepth:    256,
		Width:         winWidth,
		Height:        winHeight,
		SupportsInput: true,
		EchoEnabled:   false,
		LineMode:      false,
		Metadata:      make(map[string]interface{}),
	}

	// Non-blocking output queue
	winOutputQueue := make(chan interface{}, 256)
	go func() {
		for item := range winOutputQueue {
			switch v := item.(type) {
			case []byte:
				winTerminal.Feed(string(v))
			case string:
				winTerminal.Feed(v)
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
		Terminal:         winTermCaps,
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
			select {
			case winOutputQueue <- []byte(text):
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
			case winOutputQueue <- writerDone:
				<-writerDone
			default:
			}
			return nil
		},
	}

	// Non-blocking input queue
	winInputQueue := make(chan byte, 256)
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := winStdinReader.Read(buf)
			if err != nil || n == 0 {
				close(winInputQueue)
				return
			}
			select {
			case winInputQueue <- buf[0]:
			default:
				select {
				case <-winInputQueue:
				default:
				}
				select {
				case winInputQueue <- buf[0]:
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
		Terminal:         winTermCaps,
		NativeRecv: func() (interface{}, error) {
			b, ok := <-winInputQueue
			if !ok {
				return nil, fmt.Errorf("input closed")
			}
			return []byte{b}, nil
		},
		NativeSend: func(v interface{}) error {
			return fmt.Errorf("cannot send to console_in")
		},
	}

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
			winStdinWriter.Write(data)
		} else if winREPL != nil && winREPL.IsRunning() {
			if winREPL.IsBusy() {
				// REPL is executing a command (e.g., read) - send to stdin pipe
				winStdinWriter.Write(data)
			} else {
				// REPL is waiting for input - send to REPL for line editing
				winREPL.HandleInput(data)
			}
		}
	})

	win.Show()

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
			winTerminal.Feed(s)
		})
		// Set flush callback to ensure output appears before blocking execution
		winREPL.SetFlush(func() {
			// Force immediate repaint to display output before blocking operations
			winTerminal.Flush()
		})
		// Set background color for prompt color selection
		bg := getTerminalBackground()
		winREPL.SetBackgroundRGB(bg.R, bg.G, bg.B)
		winREPL.SetPSLColors(getPSLColors())
		winREPL.Start()

		// Register the dummy_button command with the window's REPL
		registerDummyButtonCommand(winREPL.GetPawScript())
	}()
}
