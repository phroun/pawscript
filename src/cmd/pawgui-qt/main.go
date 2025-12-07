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
func getDefaultFont() string {
	switch runtime.GOOS {
	case "darwin":
		return "Menlo"
	case "windows":
		return "Cascadia Mono"
	default:
		return "DejaVu Sans Mono"
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

func main() {
	// Load configuration
	appConfig = loadConfig()

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
	mainWindow.SetMinimumSize2(900, 700)

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

	// Load initial directory
	loadDirectory(currentDir)

	// Show window
	mainWindow.Show()

	// Run application
	qt.QApplication_Exec()
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

	// Create terminal
	var err error
	terminal, err = purfectermqt.New(purfectermqt.Options{
		Cols:           80,
		Rows:           24,
		ScrollbackSize: 10000,
		FontFamily:     getFontFamily(),
		FontSize:       getFontSize(),
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
		OptLevel:     0,
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
		return
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

	// Add .paw files
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".paw") {
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

func browseFolder() {
	dir := qt.QFileDialog_GetExistingDirectory3(mainWindow.QWidget, "Select Folder", currentDir)
	if dir != "" {
		loadDirectory(dir)
	}
}

func runSelectedFile() {
	items := fileList.SelectedItems()
	if len(items) == 0 {
		return
	}

	item := items[0]
	fileItemDataMu.Lock()
	data, ok := fileItemDataMap[item.UnsafePointer()]
	fileItemDataMu.Unlock()

	if !ok {
		return
	}

	if !data.isDir {
		runScript(data.path)
	}
}

func runScript(filePath string) {
	scriptMu.Lock()
	if scriptRunning {
		scriptMu.Unlock()
		terminal.Feed("\r\nA script is already running.\r\n")
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
		OptLevel:             pawscript.OptimizationLevel(0),
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
				OptLevel:     0,
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
