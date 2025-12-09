// pawgui-gtk - GTK3-based GUI for PawScript with custom terminal emulator
// Cross-platform: works on Linux, macOS, and Windows
package main

import (
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
	"github.com/phroun/pawscript"
	"github.com/phroun/pawscript/pkg/pawgui"
	"github.com/phroun/pawscript/pkg/purfecterm"
	purfectermgtk "github.com/phroun/pawscript/pkg/purfecterm-gtk"
	"github.com/sqweek/dialog"
)

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
	pathLabel   *gtk.Label
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
)

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
	_ = os.WriteFile(configPath, []byte(data), 0644)
}

// saveBrowseDir saves the current browse directory to config
func saveBrowseDir(dir string) {
	appConfig.Set("last_browse_dir", dir)
	saveConfig(appConfig)
}

// Configuration getter wrappers using shared configHelper
func getFontFamily() string          { return configHelper.GetFontFamily() }
func getFontSize() int               { return configHelper.GetFontSize() }
func getUIScale() float64            { return configHelper.GetUIScale() }
func getOptimizationLevel() int      { return configHelper.GetOptimizationLevel() }
func getTerminalBackground() purfecterm.Color { return configHelper.GetTerminalBackground() }
func getTerminalForeground() purfecterm.Color { return configHelper.GetTerminalForeground() }
func getColorPalette() []purfecterm.Color     { return configHelper.GetColorPalette() }
func getBlinkMode() purfecterm.BlinkMode      { return configHelper.GetBlinkMode() }
func getQuitShortcut() string        { return configHelper.GetQuitShortcut() }
func getDefaultQuitShortcut() string { return pawgui.GetDefaultQuitShortcut() }

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

func main() {
	app, err := gtk.ApplicationNew(appID, glib.APPLICATION_FLAGS_NONE)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create application: %v\n", err)
		os.Exit(1)
	}

	app.Connect("activate", func() {
		activate(app)
	})

	os.Exit(app.Run(os.Args))
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
	paned, _ := gtk.PanedNew(gtk.ORIENTATION_HORIZONTAL)
	paned.SetPosition(400)

	// Left panel: File browser
	leftPanel := createFileBrowser()
	paned.Pack1(leftPanel, false, false)

	// Right panel: Terminal (with left margin for spacing from divider)
	rightPanel := createTerminal()
	rightPanel.SetMarginStart(8) // 8 pixel spacer from divider
	paned.Pack2(rightPanel, true, false)

	mainBox.PackStart(paned, true, true, 0)
	mainWindow.Add(mainBox)

	// Load initial directory
	currentDir = getDefaultDir()
	refreshFileList()
	updatePathLabel()

	// Print welcome message
	terminal.Feed("PawScript Launcher (GTK3)\r\n")
	terminal.Feed("Cross-platform terminal emulator\r\n")
	terminal.Feed("Select a .paw file and click Run to execute.\r\n\r\n")

	mainWindow.ShowAll()

	// Deselect any text in the path label and focus the Run button
	pathLabel.SelectRegion(0, 0)
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

	// Current path label
	pathLabel, _ = gtk.LabelNew(currentDir)
	pathLabel.SetXAlign(0)
	pathLabel.SetLineWrap(true)
	pathLabel.SetSelectable(true)
	box.PackStart(pathLabel, false, false, 0)

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
	createConsoleChannels(100, 30)

	return box
}

func updatePathLabel() {
	if pathLabel != nil {
		pathLabel.SetText(currentDir)
	}
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
		updatePathLabel()
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
		updatePathLabel()
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
			// Set background color for prompt color selection
			bg := getTerminalBackground()
			consoleREPL.SetBackgroundRGB(bg.R, bg.G, bg.B)
			consoleREPL.Start()
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

	// Add terminal to window
	termWidget := winTerminal.Widget()
	termWidget.SetVExpand(true)
	termWidget.SetHExpand(true)
	win.Add(termWidget)

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

	width, height := 100, 30
	termCaps := &pawscript.TerminalCapabilities{
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
			winREPL.HandleInput(data)
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
			glib.IdleAdd(func() bool {
				winTerminal.Feed(s)
				return false
			})
		})
		// Set background color for prompt color selection
		bg := getTerminalBackground()
		winREPL.SetBackgroundRGB(bg.R, bg.G, bg.B)
		winREPL.Start()
	}()
}

// createConsoleChannels creates the I/O channels for PawScript console
func createConsoleChannels(width, height int) {
	// Create pipes for stdout/stdin
	stdoutReader, stdoutWriterLocal := io.Pipe()
	stdinReaderLocal, stdinWriterLocal := io.Pipe()
	stdoutWriter = stdoutWriterLocal
	stdinReader = stdinReaderLocal
	stdinWriter = stdinWriterLocal

	termCaps := &pawscript.TerminalCapabilities{
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
		} else {
			// No script running, send to REPL
			if consoleREPL != nil && consoleREPL.IsRunning() {
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
	// Set background color for prompt color selection
	bg := getTerminalBackground()
	consoleREPL.SetBackgroundRGB(bg.R, bg.G, bg.B)
	consoleREPL.Start()
}
