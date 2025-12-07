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

	"github.com/phroun/pawscript"
	purfectermqt "github.com/phroun/pawscript/pkg/purfecterm-qt"
	"github.com/therecipe/qt/core"
	"github.com/therecipe/qt/gui"
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

	// Initialize Qt
	qApp = widgets.NewQApplication(len(os.Args), os.Args)
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
	// Create channels for PawScript I/O
	consoleOutCh = pawscript.NewStoredChannel()
	consoleInCh = pawscript.NewStoredChannel()

	// Create pipes for stdin
	stdinReader, stdinWriter = io.Pipe()

	// Set terminal input callback
	terminal.SetInputCallback(func(data []byte) {
		scriptMu.Lock()
		running := scriptRunning
		scriptMu.Unlock()

		if running {
			// Send to script via stdin
			stdinWriter.Write(data)
		} else if consoleREPL != nil {
			// Send to REPL
			stdinWriter.Write(data)
		}
	})

	// Start output reader goroutine
	go func() {
		for {
			result, ok := consoleOutCh.Receive()
			if !ok {
				break
			}
			if str, isStr := result.(string); isStr {
				core.QCoreApplication_PostEvent(mainWindow, core.NewQEvent(core.QEvent__User), 0)
				terminal.Feed(str)
			}
		}
	}()
}

func startREPL() {
	// Create REPL with console channels
	consoleREPL = pawscript.NewREPL(&pawscript.EvalConfig{
		StdoutCh: consoleOutCh,
		StdinCh:  consoleInCh,
	})

	// Start REPL reader goroutine
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := stdinReader.Read(buf)
			if err != nil {
				break
			}
			if n > 0 {
				scriptMu.Lock()
				running := scriptRunning
				scriptMu.Unlock()

				if !running && consoleREPL != nil {
					consoleREPL.Input(string(buf[:n]))
				}
			}
		}
	}()

	// Print initial prompt
	consoleOutCh.Send("PawScript REPL - Type 'help' for commands\n")
	consoleOutCh.Send(consoleREPL.Prompt())
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
		consoleOutCh.Send("\nA script is already running.\n")
		return
	}
	scriptRunning = true
	scriptMu.Unlock()

	// Disable run button
	runButton.SetEnabled(false)

	// Clear terminal and show message
	terminal.Clear()
	consoleOutCh.Send(fmt.Sprintf("Running: %s\n\n", filepath.Base(filePath)))

	go func() {
		// Read script
		data, err := os.ReadFile(filePath)
		if err != nil {
			consoleOutCh.Send(fmt.Sprintf("Error reading file: %v\n", err))
			finishScript()
			return
		}

		// Parse script
		program, parseErr := pawscript.ParseScript(string(data), filePath)
		if parseErr != nil {
			consoleOutCh.Send(fmt.Sprintf("Parse error: %v\n", parseErr))
			finishScript()
			return
		}

		// Create evaluator
		eval := pawscript.NewEvaluator(&pawscript.EvalConfig{
			StdoutCh:   consoleOutCh,
			StdinCh:    consoleInCh,
			ScriptPath: filePath,
		})

		// Run script
		startTime := time.Now()
		_, evalErr := eval.Evaluate(program)
		duration := time.Since(startTime)

		if evalErr != nil {
			consoleOutCh.Send(fmt.Sprintf("\nScript error: %v\n", evalErr))
		}

		consoleOutCh.Send(fmt.Sprintf("\n--- Script completed in %v ---\n", duration.Round(time.Millisecond)))
		finishScript()
	}()
}

func finishScript() {
	scriptMu.Lock()
	scriptRunning = false
	scriptMu.Unlock()

	// Re-enable run button on main thread
	core.QCoreApplication_PostEvent(mainWindow, core.NewQEvent(core.QEvent__User), 0)

	// Show REPL prompt
	if consoleREPL != nil {
		consoleOutCh.Send(consoleREPL.Prompt())
	}
}

// Custom event handler to update UI from background threads
func init() {
	// Note: Qt event handling would need proper implementation
	// This is a simplified version - full implementation would use
	// custom QEvent subclasses and proper event filtering
}
