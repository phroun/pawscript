// pawgui-qt - Qt-based GUI for PawScript with custom terminal emulator
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
	"unsafe"

	"github.com/mappu/miqt/qt"
	"github.com/phroun/pawscript"
	"github.com/phroun/pawscript/pkg/purfecterm"
	purfectermqt "github.com/phroun/pawscript/pkg/purfecterm-qt"
)

func init() {
	// Lock the main goroutine to the main OS thread.
	// This is required for Qt on macOS to avoid signal handling conflicts.
	runtime.LockOSThread()
}

// Default font settings
const defaultFontSize = 22

// getDefaultFont returns the best monospace font for the current platform
// Includes cross-platform fallbacks so config files can be shared between OS
func getDefaultFont() string {
	switch runtime.GOOS {
	case "darwin":
		return "Menlo, JetBrains Mono, SF Mono, Cascadia Mono, Consolas, Monaco, Courier New"
	case "windows":
		return "Cascadia Mono, Consolas, JetBrains Mono, Menlo, SF Mono, Monaco, Courier New"
	default:
		return "JetBrains Mono, DejaVu Sans Mono, Liberation Mono, Menlo, Consolas, monospace"
	}
}

const appName = "PawScript Launcher (Qt)"

// Global state
var (
	currentDir string
	mainWindow *qt.QMainWindow
	fileList   *qt.QListWidget
	terminal   *purfectermqt.Terminal
	pathLabel  *qt.QLabel
	runButton  *qt.QPushButton

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
	appConfig pawscript.PSLConfig
)

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
	_ = os.WriteFile(configPath, []byte(data), 0644)
}

func saveBrowseDir(dir string) {
	appConfig.Set("last_browse_dir", dir)
	saveConfig(appConfig)
}

func getFontFamily() string {
	if appConfig != nil {
		if font := appConfig.GetString("font_family", ""); font != "" {
			return font
		}
	}
	return getDefaultFont()
}

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

// getTerminalBackground returns the configured terminal background color
func getTerminalBackground() purfecterm.Color {
	if appConfig != nil {
		if hex := appConfig.GetString("terminal_background", ""); hex != "" {
			if c, ok := purfecterm.ParseHexColor(hex); ok {
				return c
			}
		}
	}
	return purfecterm.Color{R: 30, G: 30, B: 30} // Default dark background
}

// getTerminalForeground returns the configured terminal foreground color
func getTerminalForeground() purfecterm.Color {
	if appConfig != nil {
		if hex := appConfig.GetString("terminal_foreground", ""); hex != "" {
			if c, ok := purfecterm.ParseHexColor(hex); ok {
				return c
			}
		}
	}
	return purfecterm.Color{R: 212, G: 212, B: 212} // Default light gray
}

// getColorPalette returns the configured 16-color ANSI palette
func getColorPalette() []purfecterm.Color {
	palette := make([]purfecterm.Color, 16)
	copy(palette, purfecterm.ANSIColors)

	if appConfig == nil {
		return palette
	}

	// Check for palette_colors nested config
	if paletteConfig, ok := appConfig["palette_colors"]; ok {
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

// getBlinkMode returns the configured blink mode
func getBlinkMode() purfecterm.BlinkMode {
	if appConfig != nil {
		mode := appConfig.GetString("default_blink", "bounce")
		return purfecterm.ParseBlinkMode(mode)
	}
	return purfecterm.BlinkModeBounce
}

// getQuitShortcut returns the configured quit shortcut
func getQuitShortcut() string {
	if appConfig != nil {
		if val, exists := appConfig["quit_shortcut"]; exists {
			if val == nil {
				return "" // nil means disabled
			}
			if s, ok := val.(string); ok {
				return s
			}
		}
	}
	return getDefaultQuitShortcut()
}

// getDefaultQuitShortcut returns the platform-appropriate default quit shortcut
func getDefaultQuitShortcut() string {
	if runtime.GOOS == "darwin" {
		return "Cmd+Q"
	}
	return "Ctrl+Q"
}

func main() {
	// Load configuration
	appConfig = loadConfig()

	// Auto-populate config with defaults (makes them discoverable)
	configModified := false
	if _, exists := appConfig["font_family"]; !exists {
		appConfig.Set("font_family", getDefaultFont())
		configModified = true
	}
	if _, exists := appConfig["font_size"]; !exists {
		appConfig.Set("font_size", defaultFontSize)
		configModified = true
	}
	if _, exists := appConfig["ui_scale"]; !exists {
		appConfig.Set("ui_scale", 1.0)
		configModified = true
	}
	if _, exists := appConfig["optimization_level"]; !exists {
		appConfig.Set("optimization_level", 1)
		configModified = true
	}
	if _, exists := appConfig["quit_shortcut"]; !exists {
		appConfig.Set("quit_shortcut", getDefaultQuitShortcut())
		configModified = true
	}
	if _, exists := appConfig["terminal_background"]; !exists {
		appConfig.Set("terminal_background", "#1E1E1E")
		configModified = true
	}
	if _, exists := appConfig["terminal_foreground"]; !exists {
		appConfig.Set("terminal_foreground", "#D4D4D4")
		configModified = true
	}
	if _, exists := appConfig["palette_colors"]; !exists {
		paletteConfig := pawscript.PSLConfig{}
		names := purfecterm.PaletteColorNames()
		hexColors := purfecterm.DefaultPaletteHex()
		for i, name := range names {
			paletteConfig.Set(name, hexColors[i])
		}
		appConfig.Set("palette_colors", paletteConfig)
		configModified = true
	}
	if _, exists := appConfig["default_blink"]; !exists {
		appConfig.Set("default_blink", "bounce")
		configModified = true
	}
	if configModified {
		saveConfig(appConfig)
	}

	// Get initial directory
	currentDir = appConfig.GetString("last_browse_dir", "")
	if currentDir == "" {
		currentDir, _ = os.Getwd()
	}

	// Initialize Qt application
	qt.NewQApplication(os.Args)

	// Create main window
	mainWindow = qt.NewQMainWindow2()
	mainWindow.SetWindowTitle(appName)
	mainWindow.Resize(1100, 700)

	// Create central widget with horizontal splitter
	centralWidget := qt.NewQWidget2()
	mainLayout := qt.NewQHBoxLayout2()
	centralWidget.SetLayout(mainLayout.QLayout)

	// Create splitter
	splitter := qt.NewQSplitter3(qt.Horizontal)

	// Left panel (file browser)
	leftPanel := createFilePanel()
	splitter.AddWidget(leftPanel)

	// Right panel (terminal)
	rightPanel := createTerminalPanel()
	splitter.AddWidget(rightPanel)

	// Set splitter sizes (30% left, 70% right)
	splitter.SetSizes([]int{270, 630})

	mainLayout.AddWidget(splitter.QWidget)
	mainWindow.SetCentralWidget(centralWidget)

	// Set up console I/O
	setupConsoleIO()

	// Start REPL
	startREPL()

	// Print welcome message
	terminal.Feed("PawScript Launcher (Qt)\r\n")
	terminal.Feed("Cross-platform terminal emulator\r\n")
	terminal.Feed("Select a .paw file and click Run to execute.\r\n\r\n")

	// Load initial directory
	loadDirectory(currentDir)

	// Set up quit shortcut based on config
	setupQuitShortcut()

	// Show window
	mainWindow.Show()

	// Run application
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
		keySequence = "Meta+Q"
	case "Ctrl+Q":
		keySequence = "Ctrl+Q"
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
	panel.SetLayout(layout.QLayout)

	// Path label
	pathLabel = qt.NewQLabel3("")
	pathLabel.SetWordWrap(true)
	layout.AddWidget(pathLabel.QWidget)

	// Navigation buttons
	navLayout := qt.NewQHBoxLayout2()

	upButton := qt.NewQPushButton3("Up")
	upButton.OnClicked(func() { navigateUp() })
	navLayout.AddWidget(upButton.QWidget)

	browseButton := qt.NewQPushButton3("Browse...")
	browseButton.OnClicked(func() { browseFolder() })
	navLayout.AddWidget(browseButton.QWidget)

	layout.AddLayout(navLayout.QLayout)

	// File list
	fileList = qt.NewQListWidget2()
	fileList.OnItemDoubleClicked(func(item *qt.QListWidgetItem) {
		handleFileActivated(item)
	})
	fileList.OnCurrentItemChanged(func(current *qt.QListWidgetItem, previous *qt.QListWidgetItem) {
		onSelectionChanged(current)
	})
	layout.AddWidget2(fileList.QWidget, 1)

	// Run button
	runButton = qt.NewQPushButton3("Run")
	runButton.OnClicked(func() { runSelectedFile() })
	layout.AddWidget(runButton.QWidget)

	return panel
}

func createTerminalPanel() *qt.QWidget {
	panel := qt.NewQWidget2()
	layout := qt.NewQVBoxLayout2()
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

	layout.AddWidget2(terminal.Widget(), 1)

	return panel
}

func setupConsoleIO() {
	// Create pipes for stdin
	stdinReader, stdinWriter = io.Pipe()

	// Terminal capabilities for PawScript
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
		} else {
			// No script running, send to REPL
			if consoleREPL != nil && consoleREPL.IsRunning() {
				consoleREPL.HandleInput(data)
			}
		}
	})
}

func startREPL() {
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
		// Output to terminal
		terminal.Feed(s)
	})
	consoleREPL.Start()
}

// fileItemData stores path and isDir for list items
type fileItemData struct {
	path  string
	isDir bool
}

var fileItemDataMap = make(map[unsafe.Pointer]fileItemData)
var fileItemDataMu sync.Mutex

func loadDirectory(dir string) {
	currentDir = dir
	pathLabel.SetText(dir)

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

	// Add parent directory entry (except at root)
	if dir != "/" && filepath.Dir(dir) != dir {
		item := qt.NewQListWidgetItem7("..", fileList)
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
			item := qt.NewQListWidgetItem7("["+entry.Name()+"]", fileList)
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
			consoleREPL.Start()
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

	// Add terminal to window
	win.SetCentralWidget(winTerminal.Widget())

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
			winREPL.HandleInput(data)
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
		winREPL.Start()
	}()
}
