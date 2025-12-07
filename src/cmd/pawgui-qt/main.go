// pawgui-qt - Qt-based GUI for PawScript with custom terminal emulator
// Cross-platform: works on Linux, macOS, and Windows
package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/mappu/miqt/qt"
	"github.com/phroun/pawscript"
	"github.com/phroun/pawscript/pkg/pawgui"
	"github.com/phroun/pawscript/pkg/purfecterm"
	purfectermqt "github.com/phroun/pawscript/pkg/purfecterm-qt"
)

func init() {
	// Lock the main goroutine to the main OS thread.
	// This is required for Qt on macOS to avoid signal handling conflicts.
	runtime.LockOSThread()

	// On macOS, Qt (or its underlying frameworks) installs signal handlers
	// without the SA_ONSTACK flag, which conflicts with Go's signal handling.
	// We take control of SIGURG before Qt initializes to prevent the crash.
	// SIGURG is used for out-of-band data on sockets.
	if runtime.GOOS == "darwin" {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGURG)
		go func() {
			for range sigCh {
				// Ignore SIGURG - it's not needed for our application
			}
		}()
	}
}

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
	appConfig    pawscript.PSLConfig
	configHelper *pawgui.ConfigHelper
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

// Configuration getter wrappers using shared configHelper
func getFontFamily() string                   { return configHelper.GetFontFamily() }
func getFontSize() int                        { return configHelper.GetFontSize() }
func getUIScale() float64                     { return configHelper.GetUIScale() }
func getOptimizationLevel() int               { return configHelper.GetOptimizationLevel() }
func getTerminalBackground() purfecterm.Color { return configHelper.GetTerminalBackground() }
func getTerminalForeground() purfecterm.Color { return configHelper.GetTerminalForeground() }
func getColorPalette() []purfecterm.Color     { return configHelper.GetColorPalette() }
func getBlinkMode() purfecterm.BlinkMode      { return configHelper.GetBlinkMode() }
func getQuitShortcut() string                 { return configHelper.GetQuitShortcut() }
func getDefaultQuitShortcut() string          { return pawgui.GetDefaultQuitShortcut() }

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

	// Print welcome message before REPL starts (so prompt appears after)
	terminal.Feed("PawScript Launcher (Qt)\r\n")
	terminal.Feed("Cross-platform terminal emulator\r\n")
	terminal.Feed("Select a .paw file and click Run to execute.\r\n\r\n")

	// Start REPL (prompt will appear after welcome message)
	startREPL()

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
	layout.SetContentsMargins(4, 4, 4, 4)
	layout.SetSpacing(4)
	panel.SetLayout(layout.QLayout)

	// Path label
	pathLabel = qt.NewQLabel3("")
	pathLabel.SetWordWrap(true)
	layout.AddWidget(pathLabel.QWidget)

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

	browseButton := qt.NewQPushButton3("Browse...")
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
