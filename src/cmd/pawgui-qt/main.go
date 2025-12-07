// pawgui-qt - Qt-based GUI for PawScript with custom terminal emulator
// Cross-platform: works on Linux, macOS, and Windows
package main

import (
	// IMPORTANT: qtinit must be imported first to set Qt environment variables
	// before any therecipe/qt packages initialize
	_ "github.com/phroun/pawscript/pkg/qtinit"

	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/phroun/pawscript"
	purfectermqt "github.com/phroun/pawscript/pkg/purfecterm-qt"
	"github.com/therecipe/qt/core"
	"github.com/therecipe/qt/widgets"
)

// Default font settings
const defaultFontSize = 22

// getDefaultFont returns the best monospace font for the current platform
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
	mainWindow *widgets.QMainWindow
	qApp       *widgets.QApplication
	fileList   *widgets.QListWidget
	terminal   *purfectermqt.Terminal
	pathLabel  *widgets.QLabel
	runButton  *widgets.QPushButton

	// Console I/O for PawScript
	consoleOutCh   *pawscript.StoredChannel
	consoleInCh    *pawscript.StoredChannel
	stdoutWriter   *io.PipeWriter
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

	// Initialize Qt with explicit args to avoid potential nil issues
	args := os.Args
	if len(args) == 0 {
		args = []string{"pawgui-qt"}
	}
	qApp = widgets.NewQApplication(len(args), args)
	qApp.SetApplicationName(appName)

	// Create main window
	mainWindow = widgets.NewQMainWindow(nil, 0)
	mainWindow.SetWindowTitle(appName)
	mainWindow.SetMinimumSize2(900, 700)

	// Create central widget with horizontal splitter
	centralWidget := widgets.NewQWidget(nil, 0)
	mainLayout := widgets.NewQHBoxLayout()
	centralWidget.SetLayout(mainLayout)

	// Create splitter
	splitter := widgets.NewQSplitter2(core.Qt__Horizontal, nil)

	// Left panel (file browser)
	leftPanel := createFilePanel()
	splitter.AddWidget(leftPanel)

	// Right panel (terminal)
	rightPanel := createTerminalPanel()
	splitter.AddWidget(rightPanel)

	// Set splitter sizes (30% left, 70% right)
	splitter.SetSizes([]int{270, 630})

	mainLayout.AddWidget(splitter, 0, 0)
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
	qApp.Exec()
}

func createFilePanel() *widgets.QWidget {
	panel := widgets.NewQWidget(nil, 0)
	layout := widgets.NewQVBoxLayout()
	panel.SetLayout(layout)

	// Path label
	pathLabel = widgets.NewQLabel2("", nil, 0)
	pathLabel.SetWordWrap(true)
	layout.AddWidget(pathLabel, 0, 0)

	// Navigation buttons
	navLayout := widgets.NewQHBoxLayout()

	upButton := widgets.NewQPushButton2("Up", nil)
	upButton.ConnectClicked(func(bool) { navigateUp() })
	navLayout.AddWidget(upButton, 0, 0)

	browseButton := widgets.NewQPushButton2("Browse...", nil)
	browseButton.ConnectClicked(func(bool) { browseFolder() })
	navLayout.AddWidget(browseButton, 0, 0)

	layout.AddLayout(navLayout, 0)

	// File list
	fileList = widgets.NewQListWidget(nil)
	fileList.ConnectItemDoubleClicked(func(item *widgets.QListWidgetItem) {
		handleFileActivated(item)
	})
	layout.AddWidget(fileList, 1, 0)

	// Run button
	runButton = widgets.NewQPushButton2("Run", nil)
	runButton.ConnectClicked(func(bool) { runSelectedFile() })
	layout.AddWidget(runButton, 0, 0)

	return panel
}

func createTerminalPanel() *widgets.QWidget {
	panel := widgets.NewQWidget(nil, 0)
	layout := widgets.NewQVBoxLayout()
	panel.SetLayout(layout)

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

	layout.AddWidget(terminal.Widget(), 1, 0)

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

func loadDirectory(dir string) {
	currentDir = dir
	pathLabel.SetText(dir)

	fileList.Clear()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	// Add directories first
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			item := widgets.NewQListWidgetItem2("["+entry.Name()+"]", fileList, 0)
			item.SetData(int(core.Qt__UserRole), core.NewQVariant1(filepath.Join(dir, entry.Name())))
			item.SetData(int(core.Qt__UserRole)+1, core.NewQVariant1(true)) // isDir
		}
	}

	// Add .paw files
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".paw") {
			item := widgets.NewQListWidgetItem2(entry.Name(), fileList, 0)
			item.SetData(int(core.Qt__UserRole), core.NewQVariant1(filepath.Join(dir, entry.Name())))
			item.SetData(int(core.Qt__UserRole)+1, core.NewQVariant1(false)) // isDir
		}
	}

	saveBrowseDir(dir)
}

func handleFileActivated(item *widgets.QListWidgetItem) {
	path := item.Data(int(core.Qt__UserRole)).ToString()
	isDir := item.Data(int(core.Qt__UserRole) + 1).ToBool()

	if isDir {
		loadDirectory(path)
	} else {
		runScript(path)
	}
}

func navigateUp() {
	parent := filepath.Dir(currentDir)
	if parent != currentDir {
		loadDirectory(parent)
	}
}

func browseFolder() {
	dialog := widgets.NewQFileDialog2(mainWindow, "Select Folder", currentDir, "")
	dialog.SetFileMode(widgets.QFileDialog__Directory)
	dialog.SetOption(widgets.QFileDialog__ShowDirsOnly, true)

	if dialog.Exec() == int(widgets.QDialog__Accepted) {
		dirs := dialog.SelectedFiles()
		if len(dirs) > 0 {
			loadDirectory(dirs[0])
		}
	}
}

func runSelectedFile() {
	items := fileList.SelectedItems()
	if len(items) == 0 {
		return
	}

	item := items[0]
	path := item.Data(int(core.Qt__UserRole)).ToString()
	isDir := item.Data(int(core.Qt__UserRole) + 1).ToBool()

	if !isDir {
		runScript(path)
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

// Custom event handler to update UI from background threads
func init() {
	// Note: Qt event handling would need proper implementation
	// This is a simplified version - full implementation would use
	// custom QEvent subclasses and proper event filtering
}
