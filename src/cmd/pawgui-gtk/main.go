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
	"github.com/phroun/pawscript/pkg/gtkterm"
	"github.com/sqweek/dialog"
)

// Default font settings
// Font priority: platform-specific defaults with cross-platform fallbacks
// - macOS: Menlo (built-in)
// - Windows: Cascadia Mono (Win11), Consolas (Win7+), Courier New (fallback)
// - Linux: JetBrains Mono, DejaVu Sans Mono, monospace
const (
	defaultFontSize = 22
)

// getDefaultFont returns the best monospace font for the current platform
func getDefaultFont() string {
	switch runtime.GOOS {
	case "darwin":
		return "Menlo"
	case "windows":
		// Cascadia Mono is on Windows 11+ and Windows Terminal
		// Consolas is on Windows Vista+, Courier New is universal fallback
		return "Cascadia Mono, Consolas, Courier New"
	default:
		// Linux: try popular coding fonts first
		return "JetBrains Mono, DejaVu Sans Mono, Liberation Mono, monospace"
	}
}

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
	currentDir string
	mainWindow *gtk.ApplicationWindow
	fileList   *gtk.ListBox
	terminal   *gtkterm.Terminal
	pathLabel  *gtk.Label
	runButton  *gtk.Button

	// Console I/O for PawScript
	consoleOutCh   *pawscript.StoredChannel
	consoleInCh    *pawscript.StoredChannel
	stdoutWriter   *io.PipeWriter
	stdinReader    *io.PipeReader
	stdinWriter    *io.PipeWriter
	clearInputFunc func()
	scriptRunning  bool
	scriptMu       sync.Mutex

	// Configuration loaded at startup
	appConfig pawscript.PSLConfig
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

	data := pawscript.SerializePSL(config)
	_ = os.WriteFile(configPath, []byte(data), 0644)
}

// saveBrowseDir saves the current browse directory to config
func saveBrowseDir(dir string) {
	appConfig.Set("last_browse_dir", dir)
	saveConfig(appConfig)
}

// getFontFamily returns the configured font family or platform default
func getFontFamily() string {
	if appConfig != nil {
		if font := appConfig.GetString("font_family", ""); font != "" {
			return font
		}
	}
	return getDefaultFont()
}

// getFontSize returns the configured font size or default
func getFontSize() int {
	if appConfig != nil {
		if size := appConfig.GetInt("font_size", 0); size > 0 {
			return size
		}
	}
	return defaultFontSize
}

// getUIScale returns the configured UI scale factor (default 1.0)
func getUIScale() float64 {
	if appConfig != nil {
		if scale := appConfig.GetFloat("ui_scale", 0); scale > 0 {
			return scale
		}
	}
	return 1.0
}

// getOptimizationLevel returns the configured optimization level (default 1)
// 0 = no caching, 1 = cache macro/loop bodies
func getOptimizationLevel() int {
	if appConfig != nil {
		return appConfig.GetInt("optimization_level", 1)
	}
	return 1
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

func activate(app *gtk.Application) {
	// Load configuration
	appConfig = loadConfig()

	// Ensure config has defaults and save (creates file if missing)
	configModified := false
	if appConfig.GetString("font_family", "") == "" {
		appConfig.Set("font_family", getDefaultFont())
		configModified = true
	}
	if appConfig.GetInt("font_size", 0) == 0 {
		appConfig.Set("font_size", defaultFontSize)
		configModified = true
	}
	if appConfig.GetFloat("ui_scale", 0) == 0 {
		appConfig.Set("ui_scale", 1.0)
		configModified = true
	}
	// optimization_level: 0=no caching, 1=cache macro/loop bodies (default)
	if _, exists := appConfig["optimization_level"]; !exists {
		appConfig.Set("optimization_level", 1)
		configModified = true
	}
	if configModified {
		saveConfig(appConfig)
	}

	// Create main window
	var err error
	mainWindow, err = gtk.ApplicationWindowNew(app)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create window: %v\n", err)
		return
	}
	mainWindow.SetTitle(appName)
	mainWindow.SetDefaultSize(1100, 750)

	// Apply CSS for UI scaling (base size 10px, scaled by ui_scale config)
	uiScale := getUIScale()
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

	// Create accelerator group for keyboard shortcuts
	accelGroup, _ := gtk.AccelGroupNew()
	mainWindow.AddAccelGroup(accelGroup)

	// Create main vertical box for menu + content
	mainBox, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)

	// Create menu bar
	menuBar, _ := gtk.MenuBarNew()

	// File menu
	fileMenuItem, _ := gtk.MenuItemNewWithLabel("File")
	fileMenu, _ := gtk.MenuNew()
	fileMenuItem.SetSubmenu(fileMenu)

	// Quit menu item with keyboard shortcut
	quitItem, _ := gtk.MenuItemNewWithLabel("Quit")
	quitItem.Connect("activate", func() {
		mainWindow.Close()
	})
	// Add Cmd+Q (Mac) or Ctrl+Q (Linux/Windows) accelerator
	quitKey := uint(gdk.KEY_q)
	var quitMod gdk.ModifierType
	if runtime.GOOS == "darwin" {
		quitMod = gdk.META_MASK // Cmd key on macOS
	} else {
		quitMod = gdk.CONTROL_MASK
	}
	quitItem.AddAccelerator("activate", accelGroup, quitKey, quitMod, gtk.ACCEL_VISIBLE)
	fileMenu.Append(quitItem)

	// Edit menu
	editMenuItem, _ := gtk.MenuItemNewWithLabel("Edit")
	editMenu, _ := gtk.MenuNew()
	editMenuItem.SetSubmenu(editMenu)

	// Copy menu item with Cmd/Ctrl+C
	copyItem, _ := gtk.MenuItemNewWithLabel("Copy")
	copyItem.Connect("activate", func() {
		if terminal != nil {
			terminal.CopySelection()
		}
	})
	copyKey := uint(gdk.KEY_c)
	var copyMod gdk.ModifierType
	if runtime.GOOS == "darwin" {
		copyMod = gdk.META_MASK
	} else {
		copyMod = gdk.CONTROL_MASK
	}
	copyItem.AddAccelerator("activate", accelGroup, copyKey, copyMod, gtk.ACCEL_VISIBLE)
	editMenu.Append(copyItem)

	// Select All menu item with Cmd/Ctrl+A
	selectAllItem, _ := gtk.MenuItemNewWithLabel("Select All")
	selectAllItem.Connect("activate", func() {
		if terminal != nil {
			terminal.SelectAll()
		}
	})
	selectAllKey := uint(gdk.KEY_a)
	var selectAllMod gdk.ModifierType
	if runtime.GOOS == "darwin" {
		selectAllMod = gdk.META_MASK
	} else {
		selectAllMod = gdk.CONTROL_MASK
	}
	selectAllItem.AddAccelerator("activate", accelGroup, selectAllKey, selectAllMod, gtk.ACCEL_VISIBLE)
	editMenu.Append(selectAllItem)

	// Clear menu item
	clearItem, _ := gtk.MenuItemNewWithLabel("Clear")
	clearItem.Connect("activate", func() {
		if terminal != nil {
			terminal.Clear()
		}
	})
	editMenu.Append(clearItem)

	menuBar.Append(fileMenuItem)
	menuBar.Append(editMenuItem)
	mainBox.PackStart(menuBar, false, false, 0)

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

	// Directory label
	dirLabel, _ := gtk.LabelNew("")
	dirLabel.SetXAlign(0)
	dirLabel.SetMarkup("<b>Directory:</b>")
	box.PackStart(dirLabel, false, false, 0)

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

	// Label
	label, _ := gtk.LabelNew("")
	label.SetXAlign(0)
	label.SetMarkup("<b>Console Output:</b>")
	label.SetMarginStart(5)
	label.SetMarginTop(5)
	box.PackStart(label, false, false, 0)

	// Create terminal with gtkterm package using config settings
	var err error
	terminal, err = gtkterm.New(gtkterm.Options{
		Cols:           100,
		Rows:           30,
		ScrollbackSize: 10000,
		FontFamily:     getFontFamily(),
		FontSize:       getFontSize(),
		Scheme: gtkterm.ColorScheme{
			Foreground: gtkterm.Color{R: 212, G: 212, B: 212},
			Background: gtkterm.Color{R: 30, G: 30, B: 30},
			Cursor:     gtkterm.Color{R: 255, G: 255, B: 255},
			Selection:  gtkterm.Color{R: 68, G: 68, B: 68},
			Palette:    gtkterm.ANSIColors,
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
		terminal.Feed("A script is already running.\r\n")
		return
	}
	scriptRunning = true
	scriptMu.Unlock()

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
		if result == pawscript.BoolStatus(false) {
			terminal.Feed("\r\n--- Script execution failed ---\r\n")
		} else {
			terminal.Feed("\r\n--- Script completed ---\r\n")
		}

		scriptMu.Lock()
		scriptRunning = false
		scriptMu.Unlock()
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
	outputQueue := make(chan []byte, 256)

	// Writer goroutine: drains queue and writes to terminal pipe
	go func() {
		for data := range outputQueue {
			stdoutWriter.Write(data)
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
			var data []byte
			switch d := v.(type) {
			case []byte:
				data = d
			case string:
				text := strings.ReplaceAll(d, "\r\n", "\n")
				text = strings.ReplaceAll(text, "\n", "\r\n")
				data = []byte(text)
			default:
				text := fmt.Sprintf("%v", v)
				text = strings.ReplaceAll(text, "\r\n", "\n")
				text = strings.ReplaceAll(text, "\n", "\r\n")
				data = []byte(text)
			}
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

	// Wire keyboard input from terminal to stdin pipe
	terminal.SetInputCallback(func(data []byte) {
		if stdinWriter != nil {
			stdinWriter.Write(data)
		}
	})
}
