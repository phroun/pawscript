// pawgui - PawScript with Fyne GUI support
// A drop-in replacement for paw with additional GUI capabilities
package main

import (
	"flag"
	"fmt"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/fyne-io/terminal"
	pawscript "github.com/phroun/pawscript"
	"github.com/sqweek/dialog"
)

var version = "dev" // set via -ldflags at build time

// WindowState holds state for a single window
type WindowState struct {
	window       fyne.Window
	content      *fyne.Container  // Main content (used when no split)
	leftContent  *fyne.Container  // Left panel content
	rightContent *fyne.Container  // Right panel content
	splitView    *container.Split // HSplit container (created on demand)
	usingSplit   bool             // Whether we're using split layout
	widgets      map[string]fyne.CanvasObject
	containers   map[string]*fyne.Container
	terminal     *terminal.Terminal
	// Console IO channels (for launcher windows)
	consoleOutCh  *pawscript.StoredChannel
	consoleInCh   *pawscript.StoredChannel
	stdoutWriter  *io.PipeWriter
}

// GuiState holds the current GUI state accessible to PawScript
type GuiState struct {
	mu              sync.RWMutex
	app             fyne.App
	ps              *pawscript.PawScript
	windows         map[int]*WindowState // Windows by object ID
	nextID          int                  // Next window ID
	scriptCompleted bool                 // True when main script execution is done
}

var guiState *GuiState

func main() {
	// Recover from panics during initialization (e.g., OpenGL failures)
	defer func() {
		if r := recover(); r != nil {
			errorPrintf("Fatal error during initialization: %v\n", r)
			errorPrintf("\nThis often occurs when your system's graphics driver doesn't support OpenGL.\n")
			errorPrintf("Possible solutions:\n")
			errorPrintf("  1. Update your graphics drivers\n")
			errorPrintf("  2. Download Mesa3D software renderer from:\n")
			errorPrintf("     https://github.com/pal1000/mesa-dist-win/releases\n")
			errorPrintf("     and place opengl32.dll in the same directory as pawgui.exe\n")
			errorPrintf("  3. Try setting environment variable: GALLIUM_DRIVER=llvmpipe\n")
			os.Exit(1)
		}
	}()

	// Define command line flags (same as paw)
	licenseFlag := flag.Bool("license", false, "Show license")
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

	// GUI-specific flags
	scaleFlag := flag.Float64("scale", 1.5, "GUI scale factor (default 1.5)")
	windowFlag := flag.Bool("window", false, "Create a console window for stdout/stdin/stderr")

	// Custom usage function
	flag.Usage = showUsage

	// Parse flags
	flag.Parse()

	if *licenseFlag {
		showLicense()
		os.Exit(0)
	}

	// Verbose is an alias for debug
	debug := *debugFlag || *verboseFlag

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
			errorPrintf("Error: Script file not found: %s\n", requestedFile)
			if !strings.Contains(requestedFile, ".") {
				errorPrintf("Also tried: %s.paw\n", requestedFile)
			}
			os.Exit(1)
		}

		scriptFile = foundFile

		content, err := os.ReadFile(scriptFile)
		if err != nil {
			errorPrintf("Error reading script file: %v\n", err)
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
			errorPrintf("Error reading from stdin: %v\n", err)
			os.Exit(1)
		}
		scriptContent = string(content)

	} else {
		// No filename and stdin is not redirected - launch GUI mode
		// Set the FYNE_SCALE environment variable
		err := os.Setenv("FYNE_SCALE", strconv.FormatFloat(*scaleFlag, 'f', -1, 64))
		if err != nil {
			fmt.Println("Error setting FYNE_SCALE:", err)
		}

		if debug {
			fmt.Fprintln(os.Stderr, "Debug: Initializing Fyne application...")
		}

		// Create the Fyne application
		fyneApp := app.New()

		if debug {
			fmt.Fprintln(os.Stderr, "Debug: Fyne application created successfully")
		}

		// Initialize GUI state
		guiState = &GuiState{
			app:     fyneApp,
			windows: make(map[int]*WindowState),
			nextID:  1,
		}

		// Create and show the launcher window
		createLauncherWindow()

		// Run the Fyne event loop (blocking)
		fyneApp.Run()
		return
	}

	// Build file access configuration (same as paw)
	var fileAccess *pawscript.FileAccessConfig
	var scriptDir string
	if scriptFile != "" {
		absScript, err := filepath.Abs(scriptFile)
		if err == nil {
			scriptDir = filepath.Dir(absScript)
		}
	}

	if !*unrestrictedFlag {
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

		if *sandboxFlag != "" {
			absPath, err := filepath.Abs(*sandboxFlag)
			if err != nil {
				errorPrintf("Error resolving sandbox path: %v\n", err)
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

			if envWriteRoots != "" {
				fileAccess.WriteRoots = parseRoots(envWriteRoots)
			} else {
				if scriptDir != "" {
					fileAccess.WriteRoots = append(fileAccess.WriteRoots, filepath.Join(scriptDir, "saves"))
					fileAccess.WriteRoots = append(fileAccess.WriteRoots, filepath.Join(scriptDir, "output"))
				}
				if cwd != "" {
					fileAccess.WriteRoots = append(fileAccess.WriteRoots, filepath.Join(cwd, "saves"))
					fileAccess.WriteRoots = append(fileAccess.WriteRoots, filepath.Join(cwd, "output"))
				}
				fileAccess.WriteRoots = append(fileAccess.WriteRoots, tmpDir)
			}

			if envExecRoots != "" {
				fileAccess.ExecRoots = parseRoots(envExecRoots)
			} else {
				if scriptDir != "" {
					fileAccess.ExecRoots = append(fileAccess.ExecRoots, filepath.Join(scriptDir, "helpers"))
					fileAccess.ExecRoots = append(fileAccess.ExecRoots, filepath.Join(scriptDir, "bin"))
				}
			}

			// Add command-line flags
			if *readRootsFlag != "" {
				fileAccess.ReadRoots = append(fileAccess.ReadRoots, parseRoots(*readRootsFlag)...)
			}
			if *writeRootsFlag != "" {
				fileAccess.WriteRoots = append(fileAccess.WriteRoots, parseRoots(*writeRootsFlag)...)
			}
			if *execRootsFlag != "" {
				fileAccess.ExecRoots = append(fileAccess.ExecRoots, parseRoots(*execRootsFlag)...)
			}
		}
	}

	// Set the FYNE_SCALE environment variable
	err := os.Setenv("FYNE_SCALE", strconv.FormatFloat(*scaleFlag, 'f', -1, 64))
	if err != nil {
		fmt.Println("Error setting FYNE_SCALE:", err)
	}

	if debug {
		fmt.Fprintln(os.Stderr, "Debug: Initializing Fyne application...")
	}

	// Create the Fyne application
	fyneApp := app.New()

	if debug {
		fmt.Fprintln(os.Stderr, "Debug: Fyne application created successfully")
	}

	// Create PawScript instance with full configuration
	ps := pawscript.New(&pawscript.Config{
		Debug:                debug,
		AllowMacros:          true,
		EnableSyntacticSugar: true,
		ShowErrorContext:     true,
		ContextLines:         2,
		FileAccess:           fileAccess,
		ScriptDir:            scriptDir,
	})

	// If --window flag is set, create console channels BEFORE registering standard library
	// so they become the default #out/#in/#err channels
	var consoleStdinReader, consoleStdoutWriter *io.PipeWriter
	var consoleStdoutReader *io.PipeReader
	var consoleStdinWriter *io.PipeReader

	if *windowFlag {
		// Create pipes for console I/O
		consoleStdinWriter, consoleStdinReader = io.Pipe()
		consoleStdoutReader, consoleStdoutWriter = io.Pipe()

		// Create console channels
		width := float32(800)
		height := float32(600)
		charWidth := int(width / 9)
		charHeight := int(height / 18)
		if charWidth < 1 {
			charWidth = 80
		}
		if charHeight < 1 {
			charHeight = 24
		}

		consoleOutCh, consoleInCh, _ := createConsoleChannels(consoleStdinWriter, consoleStdoutWriter, charWidth, charHeight)

		// Register standard library with custom IO channels
		ioConfig := &pawscript.IOChannelConfig{
			Stdout: consoleOutCh,
			Stdin:  consoleInCh,
			Stderr: consoleOutCh, // stderr goes to same console
		}
		ps.RegisterStandardLibraryWithIO(scriptArgs, ioConfig)
	} else {
		// Use default IO channels
		ps.RegisterStandardLibrary(scriptArgs)
	}

	// Initialize GUI state - no default window, scripts must create their own
	guiState = &GuiState{
		app:     fyneApp,
		ps:      ps,
		windows: make(map[int]*WindowState),
		nextID:  1,
	}

	// Register GUI commands
	registerGuiCommands(ps)

	// If --window flag is set, create the console window with terminal
	if *windowFlag {
		createConsoleWindowWithPipes(scriptFile, consoleStdinReader, consoleStdoutReader)
	}

	// Run the script in a goroutine
	go func() {
		// Small delay to allow console window to initialize if created
		if *windowFlag {
			time.Sleep(200 * time.Millisecond)
		}

		var result pawscript.Result
		if scriptFile != "" {
			result = ps.ExecuteFile(scriptContent, scriptFile)
		} else {
			result = ps.Execute(scriptContent)
		}
		if result == pawscript.BoolStatus(false) {
			fmt.Fprintf(os.Stderr, "Script execution failed\n")
		}
		// Import exports module directly into root so macros are callable
		// Only attempt if the module exists to avoid error logging
		if ps.HasLibraryModule("exports") {
			if ps.ImportModuleToRoot("exports") {
				if debug {
					fmt.Fprintf(os.Stderr, "Successfully imported exports module to root\n")
				}
			}
		}

		// Mark script as completed and start monitoring for auto-quit
		guiState.mu.Lock()
		guiState.scriptCompleted = true
		guiState.mu.Unlock()

		// Start monitoring for auto-quit condition
		go monitorAutoQuit()
	}()

	// Run the Fyne event loop (blocking)
	fyneApp.Run()
}

// monitorAutoQuit periodically checks if all conditions for auto-quit are met:
// 1. Script execution completed
// 2. No active fibers
// 3. No open windows
func monitorAutoQuit() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		guiState.mu.RLock()
		scriptDone := guiState.scriptCompleted
		windowCount := len(guiState.windows)
		guiState.mu.RUnlock()

		if !scriptDone {
			continue
		}

		// Check fiber count
		fiberCount := guiState.ps.GetFiberCount()

		// If script is done, no fibers, and no windows, quit
		if fiberCount == 0 && windowCount == 0 {
			fyne.Do(func() {
				guiState.app.Quit()
			})
			return
		}
	}
}

// ANSI color codes for terminal output
const (
	colorYellow = "\x1b[93m"
	colorReset  = "\x1b[0m"
)

// stderrSupportsColor checks if stderr is a terminal that supports color output
func stderrSupportsColor() bool {
	stderrInfo, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	if (stderrInfo.Mode() & os.ModeCharDevice) == 0 {
		return false
	}
	if _, exists := os.LookupEnv("NO_COLOR"); exists {
		return false
	}
	if term := os.Getenv("TERM"); term == "dumb" {
		return false
	}
	return true
}

// errorPrintf prints an error message to stderr, using color if supported
func errorPrintf(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	if stderrSupportsColor() {
		fmt.Fprintf(os.Stderr, "%s%s%s", colorYellow, message, colorReset)
	} else {
		fmt.Fprint(os.Stderr, message)
	}
}

func findScriptFile(filename string) string {
	if _, err := os.Stat(filename); err == nil {
		return filename
	}
	if filepath.Ext(filename) == "" {
		pawFile := filename + ".paw"
		if _, err := os.Stat(pawFile); err == nil {
			return pawFile
		}
	}
	return ""
}

func showCopyright() {
	fmt.Fprintf(os.Stderr, "pawgui, the PawScript GUI interpreter version %s\nCopyright (c) 2025 Jeffrey R. Day\nLicense: MIT\n\n", version)
}

func showLicense() {
	fmt.Fprintf(os.Stdout, "pawgui, the PawScript GUI interpreter version %s", version)
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
	usage := `Usage: pawgui [options] [script.paw] [-- args...]
       pawgui [options] < input.paw
       echo "commands" | pawgui [options]

Execute PawScript with GUI capabilities from a file, stdin, or pipe.

Options:
  --license           View license and exit
  -d, -debug          Enable debug output
  -v, -verbose        Enable verbose output (same as -debug)
  --unrestricted      Disable all file/exec access restrictions
  --sandbox DIR       Restrict all access to DIR only
  --read-roots DIRS   Additional directories for reading
  --write-roots DIRS  Additional directories for writing
  --exec-roots DIRS   Additional directories for exec command

GUI Options:
  --scale FACTOR      GUI scale factor (default 1.5)
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

GUI Commands (available in scripts):
  gui_window <title> [w], [h]   Create new window, returns handle
                                Use console: true for easy print/read mode
  gui_title [#win,] <text>      Set window title
  gui_resize [#win,] <w>, <h>   Resize window
  gui_close #win                Close a window
  gui_focus <id>                Focus a widget by ID
  gui_split [offset]            Enable split layout (0.0-1.0)
  gui_label <text>              Create label widget
  gui_button <text>             Create button widget
  gui_entry [placeholder]       Create text entry widget
  gui_get <id>                  Get widget value
  gui_set <id>, <value>         Set widget value
  gui_clear                     Clear all widgets
  gui_msgbox <message>          Show message dialog
  gui_console [w], [h]          Create terminal console

Window Handle Usage:
  #window: {gui_window "My Window", 800, 600}
  gui_title #window, "New Title"
  gui_resize #window, 400, 300
  gui_close #window

Easy Console Mode (beginners):
  gui_window "My App", 800, 600, console: true
  print "Hello World!"      # Prints to the console window
  name: {read}              # Reads from user input

Examples:
  pawgui examples/gui-demo.paw       # Run the GUI demo
  pawgui examples/gui-console.paw    # Run the console demo
  pawgui --unrestricted script.paw   # No file/exec restrictions
  pawgui --scale 2.0 script.paw      # Use 2x GUI scale
`
	fmt.Fprint(os.Stderr, usage)
}

// addToPanel adds a widget to the appropriate panel of a window based on named args
func addToPanel(ctx *pawscript.Context, ws *WindowState, widget fyne.CanvasObject) {
	if ws == nil {
		ctx.LogError(pawscript.CatCommand, "No window available - create one with gui_window first")
		return
	}

	panel := "default"
	if p, ok := ctx.NamedArgs["panel"]; ok {
		panel = fmt.Sprintf("%v", p)
	}

	fyne.Do(func() {
		guiState.mu.Lock()
		defer guiState.mu.Unlock()

		switch panel {
		case "left":
			ws.leftContent.Add(widget)
			ws.leftContent.Refresh()
			ensureSplitLayout(ws)
		case "right":
			ws.rightContent.Add(widget)
			ws.rightContent.Refresh()
			ensureSplitLayout(ws)
		default:
			ws.content.Add(widget)
			ws.content.Refresh()
		}
	})
}

// ensureSplitLayout switches a window to split layout if not already using it
// Must be called with guiState.mu held
func ensureSplitLayout(ws *WindowState) {
	if ws.usingSplit {
		return
	}

	leftScroll := container.NewVScroll(ws.leftContent)
	rightScroll := container.NewVScroll(ws.rightContent)
	ws.splitView = container.NewHSplit(leftScroll, rightScroll)
	ws.splitView.SetOffset(0.4)

	ws.window.SetContent(ws.splitView)
	ws.usingSplit = true
}

// resolveWindow resolves a window from hash-args pattern
// Returns the WindowState and remaining args
// If first arg is a #-prefixed symbol, resolves through: local vars → ObjectsModule → ObjectsInherited
// Falls back to default window (ID 0 = main window) if not specified
func resolveWindow(ctx *pawscript.Context) (*WindowState, []interface{}) {
	args := ctx.Args

	// Check if first arg is a #-prefixed symbol
	if len(args) > 0 {
		if sym, ok := args[0].(pawscript.Symbol); ok {
			symStr := string(sym)
			if strings.HasPrefix(symStr, "#") {
				// Try to resolve through the chain
				resolved := ctx.ResolveHashArg(symStr)
				if resolved != nil {
					// Check if it's a window marker
					if marker, ok := resolved.(pawscript.Symbol); ok {
						markerStr := string(marker)
						if strings.HasPrefix(markerStr, "\x00WINDOW:") && strings.HasSuffix(markerStr, "\x00") {
							idStr := markerStr[len("\x00WINDOW:") : len(markerStr)-1]
							if id, err := strconv.Atoi(idStr); err == nil {
								guiState.mu.RLock()
								ws, exists := guiState.windows[id]
								guiState.mu.RUnlock()
								if exists {
									return ws, args[1:]
								}
							}
						}
					}
				}
			}
		}
	}

	// Return nil to indicate use default window (mainWindow)
	return nil, args
}

// getFirstWindow returns the first available window (for commands that don't specify one)
// Returns nil if no windows exist
func getFirstWindow() *WindowState {
	guiState.mu.RLock()
	defer guiState.mu.RUnlock()
	for _, ws := range guiState.windows {
		return ws
	}
	return nil
}

// getWindowOrFirst returns the resolved window or the first available window
func getWindowOrFirst(ws *WindowState) *WindowState {
	if ws != nil {
		return ws
	}
	return getFirstWindow()
}

// getWindowFromContext checks for #window in the context's inherited objects
// and returns the corresponding WindowState. Falls back to getFirstWindow() if not found.
func getWindowFromContext(ctx *pawscript.Context) *WindowState {
	// Try to get #window from context (inherited or module object)
	if windowObj := ctx.ResolveHashArg("#window"); windowObj != nil {
		if marker, ok := windowObj.(pawscript.Symbol); ok {
			markerStr := string(marker)
			if strings.HasPrefix(markerStr, "\x00WINDOW:") && strings.HasSuffix(markerStr, "\x00") {
				idStr := markerStr[len("\x00WINDOW:") : len(markerStr)-1]
				if id, err := strconv.Atoi(idStr); err == nil {
					guiState.mu.RLock()
					ws, exists := guiState.windows[id]
					guiState.mu.RUnlock()
					if exists {
						return ws
					}
				}
			}
		}
	}
	return getFirstWindow()
}

// resolveWindowWithContext tries to resolve a window in this order:
// 1. Explicit #window argument (first arg)
// 2. Inherited #window from context
// 3. First available window
// Returns the WindowState and remaining args (with explicit window arg consumed if present)
func resolveWindowWithContext(ctx *pawscript.Context) (*WindowState, []interface{}) {
	// First try explicit window handle as first argument
	ws, remainingArgs := resolveWindow(ctx)
	if ws != nil {
		return ws, remainingArgs
	}

	// Fall back to inherited #window or first window
	return getWindowFromContext(ctx), ctx.Args
}

// createConsoleWindowWithPipes creates a window with a terminal console connected to the provided pipes
// stdinReader: reads user input from terminal, stdinWriter writes to this
// stdoutReader: reads output to display, stdoutWriter writes to this
func createConsoleWindowWithPipes(scriptFile string, stdinReader *io.PipeWriter, stdoutReader *io.PipeReader) {
	title := "PawScript Console"
	if scriptFile != "" {
		title = filepath.Base(scriptFile) + " - PawScript Console"
	}

	width := float32(800)
	height := float32(600)

	guiState.mu.Lock()
	id := guiState.nextID
	guiState.nextID++
	guiState.mu.Unlock()

	var ws *WindowState
	done := make(chan struct{})

	fyne.Do(func() {
		newWindow := guiState.app.NewWindow(title)
		newWindow.Resize(fyne.NewSize(width, height))

		ws = &WindowState{
			window:       newWindow,
			content:      container.NewVBox(),
			leftContent:  container.NewVBox(),
			rightContent: container.NewVBox(),
			usingSplit:   false,
			widgets:      make(map[string]fyne.CanvasObject),
			containers:   make(map[string]*fyne.Container),
		}

		term := terminal.New()
		ws.terminal = term

		// Add close handler to remove window from tracking
		windowID := id // Capture for closure
		newWindow.SetCloseIntercept(func() {
			guiState.mu.Lock()
			delete(guiState.windows, windowID)
			guiState.mu.Unlock()
			newWindow.Close()
		})

		// Set the terminal as the window content (full size)
		clickInterceptor := newClickInterceptor(term)
		termWithInterceptor := container.NewStack(term, clickInterceptor)
		newWindow.SetContent(termWithInterceptor)
		newWindow.CenterOnScreen()
		newWindow.Show()

		guiState.mu.Lock()
		guiState.windows[id] = ws
		guiState.mu.Unlock()

		// Start the terminal connection
		// stdinReader receives input from user typing in terminal
		// stdoutReader provides output to display in terminal
		go func() {
			time.Sleep(time.Millisecond * 100)
			err := term.RunWithConnection(stdinReader, stdoutReader)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Terminal error: %v\n", err)
			}
		}()

		// Auto-focus the terminal
		canvas := newWindow.Canvas()
		if canvas != nil {
			canvas.Focus(term)
		}

		close(done)
	})

	<-done
}

// FileEntry represents a file or directory entry in the browser
type FileEntry struct {
	Name    string
	IsDir   bool
	IsParent bool // true for "../" entry
}

// getEntriesInDir returns a list of .paw files and directories in the given directory
// Returns "../" at the top if not at root, then directories (with "/" suffix), then .paw files
func getEntriesInDir(dir string) []FileEntry {
	var entries []FileEntry

	// Add parent directory option if not at filesystem root
	absDir, err := filepath.Abs(dir)
	if err == nil {
		parent := filepath.Dir(absDir)
		if parent != absDir {
			entries = append(entries, FileEntry{Name: "../", IsDir: true, IsParent: true})
		}
	}

	// Read directory entries
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return entries
	}

	// Separate directories and files
	var dirs []string
	var files []string

	for _, entry := range dirEntries {
		name := entry.Name()
		// Skip hidden files/directories (starting with .)
		if strings.HasPrefix(name, ".") {
			continue
		}
		if entry.IsDir() {
			dirs = append(dirs, name)
		} else if strings.HasSuffix(strings.ToLower(name), ".paw") {
			files = append(files, name)
		}
	}

	// Sort directories and files
	sort.Strings(dirs)
	sort.Strings(files)

	// Add directories with "/" suffix
	for _, d := range dirs {
		entries = append(entries, FileEntry{Name: d + "/", IsDir: true, IsParent: false})
	}

	// Add .paw files
	for _, f := range files {
		entries = append(entries, FileEntry{Name: f, IsDir: false, IsParent: false})
	}

	return entries
}

// getPawFilesInDir returns a sorted list of .paw files in the given directory (legacy compatibility)
func getPawFilesInDir(dir string) []string {
	var files []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return files
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".paw") {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	return files
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

// getConfigPath returns the path to the pawgui.psl config file
func getConfigPath() string {
	configDir := getConfigDir()
	if configDir == "" {
		return ""
	}
	return filepath.Join(configDir, "pawgui.psl")
}

// loadConfig loads the configuration from ~/.paw/pawgui.psl
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

// saveConfig saves the configuration to ~/.paw/pawgui.psl
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

// getExamplesDir returns the path to the examples directory relative to the executable
func getExamplesDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exeDir := filepath.Dir(exe)
	return filepath.Join(exeDir, "examples")
}

// getDefaultBrowseDir returns the default directory for the file browser
// Priority: saved last_browse_dir -> examples dir -> executable dir -> current dir
func getDefaultBrowseDir() string {
	// Try to load saved config
	config := loadConfig()
	savedDir := config.GetString("last_browse_dir", "")

	// If saved directory exists, use it
	if savedDir != "" {
		if info, err := os.Stat(savedDir); err == nil && info.IsDir() {
			return savedDir
		}
	}

	// Try examples directory
	examplesDir := getExamplesDir()
	if examplesDir != "" {
		if info, err := os.Stat(examplesDir); err == nil && info.IsDir() {
			return examplesDir
		}
	}

	// Try executable directory
	exe, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exe)
		if info, err := os.Stat(exeDir); err == nil && info.IsDir() {
			return exeDir
		}
	}

	// Fall back to current working directory
	cwd, err := os.Getwd()
	if err == nil {
		return cwd
	}

	return "."
}

// saveBrowseDir saves the current browse directory to config
func saveBrowseDir(dir string) {
	config := loadConfig()
	config.Set("last_browse_dir", dir)
	saveConfig(config)
}

// truncatePathFromStart truncates a path from the beginning if it exceeds maxLen
// Returns "...end/of/path" format to emphasize the final part
func truncatePathFromStart(path string, maxLen int) string {
	if len(path) <= maxLen {
		return path
	}
	if maxLen < 4 {
		return "..."
	}
	// Leave room for "..."
	remaining := maxLen - 3
	return "..." + path[len(path)-remaining:]
}

// createMainMenu creates the application main menu
func createMainMenu(win fyne.Window) *fyne.MainMenu {
	// File menu
	// Note: Menu callbacks run on the Fyne main thread, so we must use goroutines
	// for functions that call fyne.Do() and wait (to avoid deadlock)
	newItem := fyne.NewMenuItem("New Window", func() {
		go createLauncherWindow()
	})
	openItem := fyne.NewMenuItem("Open...", func() {
		showOpenFileDialog(win)
	})
	fileMenu := fyne.NewMenu("File", newItem, openItem)

	// Window menu - will be dynamically populated
	windowMenu := fyne.NewMenu("Window")

	return fyne.NewMainMenu(fileMenu, windowMenu)
}

// updateWindowMenu refreshes the Window menu with current windows
func updateWindowMenu(menu *fyne.MainMenu) {
	if menu == nil || len(menu.Items) < 2 {
		return
	}
	windowMenu := menu.Items[1]

	guiState.mu.RLock()
	defer guiState.mu.RUnlock()

	// Build list of window items
	var items []*fyne.MenuItem
	for id, ws := range guiState.windows {
		windowID := id
		windowState := ws
		title := ws.window.Title()
		if title == "" {
			title = fmt.Sprintf("Window %d", id)
		}
		item := fyne.NewMenuItem(title, func() {
			fyne.Do(func() {
				windowState.window.RequestFocus()
			})
		})
		_ = windowID // suppress unused warning
		items = append(items, item)
	}

	windowMenu.Items = items
	windowMenu.Refresh()
}

// showOpenFileDialog shows a native file dialog to select a .paw file (opens in new window)
func showOpenFileDialog(win fyne.Window) {
	// Use native OS file dialog via sqweek/dialog
	go func() {
		cwd, _ := os.Getwd()
		filePath, err := dialog.File().
			Filter("PawScript files", "paw").
			SetStartDir(cwd).
			Title("Open PawScript File").
			Load()
		if err != nil {
			// User cancelled or error occurred
			return
		}
		go runScriptFile(filePath)
	}()
}

// showOpenFileDialogForWindow shows a native file dialog and runs the script in the given window's console
func showOpenFileDialogForWindow(win fyne.Window, ws *WindowState) {
	// Use native OS file dialog via sqweek/dialog
	go func() {
		cwd, _ := os.Getwd()
		filePath, err := dialog.File().
			Filter("PawScript files", "paw").
			SetStartDir(cwd).
			Title("Open PawScript File").
			Load()
		if err != nil {
			// User cancelled or error occurred
			return
		}
		go runScriptInWindow(filePath, ws)
	}()
}

// runScriptInWindow runs a .paw script file in an existing window's console
func runScriptInWindow(filePath string, ws *WindowState) {
	if ws == nil || ws.consoleOutCh == nil || ws.consoleInCh == nil {
		fmt.Fprintf(os.Stderr, "Error: Window does not have console channels\n")
		return
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		// Write error to console
		errMsg := fmt.Sprintf("\r\nError reading script file: %v\r\n", err)
		fmt.Fprint(ws.stdoutWriter, errMsg)
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
	})

	// Register standard library with the window's console IO
	ioConfig := &pawscript.IOChannelConfig{
		Stdout: ws.consoleOutCh,
		Stdin:  ws.consoleInCh,
		Stderr: ws.consoleOutCh,
	}
	ps.RegisterStandardLibraryWithIO([]string{}, ioConfig)
	registerGuiCommands(ps)

	// Update window title
	title := filepath.Base(filePath) + " - PawScript Launcher"
	fyne.Do(func() {
		ws.window.SetTitle(title)
	})

	// Print separator and run message
	runMsg := fmt.Sprintf("\r\n--- Running: %s ---\r\n\r\n", filepath.Base(filePath))
	fmt.Fprint(ws.stdoutWriter, runMsg)

	// Create an isolated snapshot for execution so scripts don't impact each other
	snapshot := ps.CreateRestrictedSnapshot()

	// Run the script in the isolated environment
	result := ps.ExecuteWithEnvironment(string(content), snapshot, filePath, 0, 0)
	if result == pawscript.BoolStatus(false) {
		fmt.Fprint(ws.stdoutWriter, "\r\n--- Script execution failed ---\r\n")
	} else {
		fmt.Fprint(ws.stdoutWriter, "\r\n--- Script completed ---\r\n")
	}
}

// runScriptFile runs a .paw script file in a new console window
func runScriptFile(filePath string) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading script file: %v\n", err)
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
	})

	// Create console window
	guiState.mu.Lock()
	id := guiState.nextID
	guiState.nextID++
	guiState.mu.Unlock()

	title := filepath.Base(filePath) + " - PawScript"
	width := float32(653)
	height := float32(414)

	// Create pipes for console I/O
	// Note: io.Pipe() returns (reader, writer) - variable names indicate usage direction
	stdinWriter, stdinReader := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()

	charWidth := 80
	charHeight := 25

	consoleOutCh, consoleInCh, _ := createConsoleChannels(stdinWriter, stdoutWriter, charWidth, charHeight)

	// Register standard library with console IO
	ioConfig := &pawscript.IOChannelConfig{
		Stdout: consoleOutCh,
		Stdin:  consoleInCh,
		Stderr: consoleOutCh,
	}
	ps.RegisterStandardLibraryWithIO([]string{}, ioConfig)
	registerGuiCommands(ps)

	var ws *WindowState
	done := make(chan struct{})

	fyne.Do(func() {
		newWindow := guiState.app.NewWindow(title)
		newWindow.Resize(fyne.NewSize(width, height))

		ws = &WindowState{
			window:       newWindow,
			content:      container.NewVBox(),
			leftContent:  container.NewVBox(),
			rightContent: container.NewVBox(),
			usingSplit:   false,
			widgets:      make(map[string]fyne.CanvasObject),
			containers:   make(map[string]*fyne.Container),
		}

		term := terminal.New()
		ws.terminal = term

		windowID := id
		newWindow.SetCloseIntercept(func() {
			guiState.mu.Lock()
			delete(guiState.windows, windowID)
			guiState.mu.Unlock()
			newWindow.Close()
		})

		clickInterceptor := newClickInterceptor(term)
		termWithInterceptor := container.NewStack(term, clickInterceptor)
		newWindow.SetContent(termWithInterceptor)
		newWindow.CenterOnScreen()
		newWindow.Show()

		guiState.mu.Lock()
		guiState.windows[id] = ws
		guiState.mu.Unlock()

		go func() {
			time.Sleep(time.Millisecond * 100)
			err := term.RunWithConnection(stdinReader, stdoutReader)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Terminal error: %v\n", err)
			}
		}()

		canvas := newWindow.Canvas()
		if canvas != nil {
			canvas.Focus(term)
		}

		close(done)
	})

	<-done

	// Run the script in an isolated snapshot
	go func() {
		time.Sleep(200 * time.Millisecond)
		snapshot := ps.CreateRestrictedSnapshot()
		result := ps.ExecuteWithEnvironment(string(content), snapshot, filePath, 0, 0)
		if result == pawscript.BoolStatus(false) {
			fmt.Fprintf(os.Stderr, "Script execution failed\n")
		}
	}()
}

// createLauncherWindow creates the main launcher window with file browser and help console
func createLauncherWindow() {
	guiState.mu.Lock()
	id := guiState.nextID
	guiState.nextID++
	guiState.mu.Unlock()

	done := make(chan struct{})

	// Create pipes for console I/O outside fyne.Do so they're accessible
	stdinWriter, stdinReader := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()

	// Create console channels for script execution
	charWidth := 80
	charHeight := 25
	consoleOutCh, consoleInCh, _ := createConsoleChannels(stdinWriter, stdoutWriter, charWidth, charHeight)

	var ws *WindowState

	fyne.Do(func() {
		win := guiState.app.NewWindow("PawScript Launcher")
		win.Resize(fyne.NewSize(900, 500))

		// Create the main menu
		mainMenu := createMainMenu(win)
		win.SetMainMenu(mainMenu)

		// Create window state with console channels
		ws = &WindowState{
			window:       win,
			content:      container.NewVBox(),
			leftContent:  container.NewVBox(),
			rightContent: container.NewVBox(),
			usingSplit:   false,
			widgets:      make(map[string]fyne.CanvasObject),
			containers:   make(map[string]*fyne.Container),
			consoleOutCh: consoleOutCh,
			consoleInCh:  consoleInCh,
			stdoutWriter: stdoutWriter,
		}

		// --- Left Panel: File Browser ---
		currentDir := getDefaultBrowseDir()
		entries := getEntriesInDir(currentDir)

		// State variables for the file browser
		var selectedEntry *FileEntry

		// Forward declarations for mutual references
		var runBtn *widget.Button
		var performAction func()
		var fileList *widget.List

		// Create list with tappable labels for double-click support
		fileList = widget.NewList(
			func() int { return len(entries) },
			func() fyne.CanvasObject {
				// Create a tappable label that will handle double-taps
				lbl := newTappableLabel("template", nil, nil)
				return lbl
			},
			func(i widget.ListItemID, o fyne.CanvasObject) {
				lbl := o.(*tappableLabel)
				lbl.SetText(entries[i].Name)
				// Update the double-tap handler for this specific item
				lbl.onDoubleTapped = func() {
					selectedEntry = &entries[i]
					fileList.Select(i)
					performAction()
				}
			},
		)

		// Handle selection
		fileList.OnSelected = func(id widget.ListItemID) {
			if id >= 0 && int(id) < len(entries) {
				selectedEntry = &entries[id]
				if selectedEntry.IsDir {
					runBtn.SetText("Open")
				} else {
					runBtn.SetText("Run")
				}
			}
		}

		fileList.OnUnselected = func(id widget.ListItemID) {
			selectedEntry = nil
			runBtn.SetText("Run")
		}

		// Label for the file list - will be updated when navigating
		// Truncate long paths from the start to show the end (more important)
		const maxDirLabelLen = 50
		dirLabel := widget.NewLabel(truncatePathFromStart(currentDir, maxDirLabelLen))
		dirLabel.TextStyle = fyne.TextStyle{Bold: true}

		// Wrap directory label in horizontal scroll so panel can shrink
		dirLabelScroll := container.NewHScroll(dirLabel)
		dirLabelScroll.SetMinSize(fyne.NewSize(50, 0)) // Allow shrinking to small size

		// Header with visual separation from file list
		dirHeader := container.NewVBox(
			dirLabelScroll,
			widget.NewSeparator(),
			widget.NewSeparator(), // Double separator for more visibility
		)

		// Run/Open button - text changes based on selection
		runBtn = widget.NewButton("Run", nil)

		// Function to perform the action (open dir or run script)
		performAction = func() {
			if selectedEntry == nil {
				return
			}
			if selectedEntry.IsDir {
				// Navigate to directory
				var newDir string
				if selectedEntry.IsParent {
					newDir = filepath.Dir(currentDir)
				} else {
					// Remove the trailing "/" from directory name
					dirName := strings.TrimSuffix(selectedEntry.Name, "/")
					newDir = filepath.Join(currentDir, dirName)
				}
				absDir, err := filepath.Abs(newDir)
				if err != nil {
					return
				}
				// Verify the directory exists
				info, err := os.Stat(absDir)
				if err != nil || !info.IsDir() {
					return
				}
				currentDir = absDir
				entries = getEntriesInDir(currentDir)
				selectedEntry = nil
				runBtn.SetText("Run")
				dirLabel.SetText(truncatePathFromStart(currentDir, maxDirLabelLen))
				fileList.UnselectAll()
				fileList.Refresh()
				// Save the current directory to config
				go saveBrowseDir(currentDir)
			} else {
				// Run the script
				fullPath := filepath.Join(currentDir, selectedEntry.Name)
				go runScriptInWindow(fullPath, ws)
			}
		}

		// Run/Open button handler
		runBtn.OnTapped = performAction

		browseBtn := widget.NewButton("Browse...", func() {
			showOpenFileDialogForWindow(win, ws)
		})

		// Theme toggle button - load preference from config
		config := loadConfig()
		isLightMode := config.GetBool("light_mode", false)
		if isLightMode {
			guiState.app.Settings().SetTheme(&lightTheme{})
		}

		// Theme toggle button
		themeBtn := widget.NewButton("💡", func() {
			cfg := loadConfig()
			lightMode := cfg.GetBool("light_mode", false)
			lightMode = !lightMode
			cfg.Set("light_mode", lightMode)
			saveConfig(cfg)

			if lightMode {
				guiState.app.Settings().SetTheme(&lightTheme{})
			} else {
				guiState.app.Settings().SetTheme(&darkTheme{})
			}
		})
		themeBtn.Importance = widget.LowImportance

		// Use HBox with spacer to push theme button to the right
		buttonBox := container.NewHBox(
			runBtn,
			browseBtn,
			layout.NewSpacer(),
			themeBtn,
		)

		// Wrap file list in scroll container for horizontal scrolling
		fileListScroll := container.NewScroll(fileList)

		leftPanel := container.NewBorder(
			dirHeader,      // top (label + separator)
			buttonBox,      // bottom
			nil,            // left
			nil,            // right
			fileListScroll, // center (fills remaining space, with scroll)
		)

		// --- Right Panel: Console ---
		term := terminal.New()
		ws.terminal = term

		clickInterceptor := newClickInterceptor(term)
		termWithInterceptor := container.NewStack(term, clickInterceptor)

		// Start terminal
		go func() {
			time.Sleep(time.Millisecond * 100)
			err := term.RunWithConnection(stdinReader, stdoutReader)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Terminal error: %v\n", err)
			}
		}()

		// Write help text to terminal after a short delay
		go func() {
			time.Sleep(time.Millisecond * 200)
			helpText := getUsageText()
			// Convert newlines for terminal
			helpText = strings.ReplaceAll(helpText, "\n", "\r\n")
			fmt.Fprint(stdoutWriter, helpText)
		}()

		// --- Create Split Layout ---
		split := container.NewHSplit(leftPanel, termWithInterceptor)
		split.SetOffset(0.3) // 30% for file list, 70% for console

		windowID := id
		win.SetCloseIntercept(func() {
			guiState.mu.Lock()
			delete(guiState.windows, windowID)
			guiState.mu.Unlock()
			win.Close()
		})

		win.SetContent(split)
		win.CenterOnScreen()
		win.Show()

		guiState.mu.Lock()
		guiState.windows[id] = ws
		guiState.mu.Unlock()

		// Focus the terminal
		canvas := win.Canvas()
		if canvas != nil {
			canvas.Focus(term)
		}

		close(done)
	})

	<-done
}

// getUsageText returns the help/usage text for display in the launcher console
func getUsageText() string {
	return fmt.Sprintf(`pawgui, the PawScript GUI interpreter version %s
Copyright (c) 2025 Jeffrey R. Day
License: MIT

Usage: pawgui [options] [script.paw] [-- args...]
       pawgui [options] < input.paw
       echo "commands" | pawgui [options]

Execute PawScript with GUI capabilities from a file, stdin, or pipe.

Options:
  --license           View license and exit
  -d, -debug          Enable debug output
  -v, -verbose        Enable verbose output (same as -debug)
  --unrestricted      Disable all file/exec access restrictions
  --sandbox DIR       Restrict all access to DIR only
  --read-roots DIRS   Additional directories for reading
  --write-roots DIRS  Additional directories for writing
  --exec-roots DIRS   Additional directories for exec command

GUI Options:
  --scale FACTOR      GUI scale factor (default 1.5)
  --window            Create console window for stdout/stdin/stderr

Select a script from the list on the left and click "Run" to execute it,
or click "Browse..." to find a script elsewhere.

Use File > New Window to open another launcher window.
Use File > Open... to open a script file.
`, version)
}

// registerGuiCommands registers all GUI-related commands with PawScript
func registerGuiCommands(ps *pawscript.PawScript) {
	// gui_window - Create a new window and return a handle
	// Usage: #mywin: {gui_window "Title"} or #mywin: {gui_window "Title", 800, 600}
	// With console: true, creates a console window and redirects stdout/stdin/stderr
	ps.RegisterCommand("gui_window", func(ctx *pawscript.Context) pawscript.Result {
		title := "PawScript Window"
		width := float32(400)
		height := float32(300)
		sizeSpecified := false

		if len(ctx.Args) >= 1 {
			title = fmt.Sprintf("%v", ctx.Args[0])
		}
		if len(ctx.Args) >= 3 {
			if w, ok := toFloat(ctx.Args[1]); ok {
				width = float32(w)
				sizeSpecified = true
			}
			if h, ok := toFloat(ctx.Args[2]); ok {
				height = float32(h)
				sizeSpecified = true
			}
		}

		// Check for console: true named argument
		isConsole := false
		if consoleArg, ok := ctx.NamedArgs["console"]; ok {
			if b, ok := consoleArg.(bool); ok && b {
				isConsole = true
			} else if s, ok := consoleArg.(string); ok && (s == "true" || s == "yes" || s == "1") {
				isConsole = true
			}
		}

		// Default to 80x25 character size for console windows
		// Base character size: 9 pixels wide, 18 pixels tall
		// Note: Fyne's scale affects rendering density, not logical dimensions
		// Dimensions reduced to account for terminal widget overhead
		if isConsole && !sizeSpecified {
			width = 653  // 80 * 9 - 67 (adjustment for terminal overhead)
			height = 414 // 25 * 18 - 36 (adjustment for terminal overhead)
		}

		// Create window state
		guiState.mu.Lock()
		id := guiState.nextID
		guiState.nextID++
		guiState.mu.Unlock()

		var ws *WindowState
		done := make(chan struct{})

		// For console mode, we need to set up pipes and channels
		var consoleOutCh, consoleInCh *pawscript.StoredChannel

		if isConsole {
			// Create pipes for console I/O
			stdinWriter, stdinReader := io.Pipe()
			stdoutReader, stdoutWriter := io.Pipe()

			// Create console channels
			charWidth := int(width / 9)
			charHeight := int(height / 18)
			if charWidth < 1 {
				charWidth = 80
			}
			if charHeight < 1 {
				charHeight = 24
			}

			consoleOutCh, consoleInCh, _ = createConsoleChannels(stdinWriter, stdoutWriter, charWidth, charHeight)

			fyne.Do(func() {
				newWindow := guiState.app.NewWindow(title)
				newWindow.Resize(fyne.NewSize(width, height))

				ws = &WindowState{
					window:       newWindow,
					content:      container.NewVBox(),
					leftContent:  container.NewVBox(),
					rightContent: container.NewVBox(),
					usingSplit:   false,
					widgets:      make(map[string]fyne.CanvasObject),
					containers:   make(map[string]*fyne.Container),
				}

				term := terminal.New()
				ws.terminal = term

				// Add close handler to remove window from tracking
				windowID := id // Capture for closure
				newWindow.SetCloseIntercept(func() {
					guiState.mu.Lock()
					delete(guiState.windows, windowID)
					guiState.mu.Unlock()
					newWindow.Close()
				})

				// Set the terminal as the window content (full size)
				clickInterceptor := newClickInterceptor(term)
				termWithInterceptor := container.NewStack(term, clickInterceptor)
				newWindow.SetContent(termWithInterceptor)
				newWindow.CenterOnScreen()
				newWindow.Show()

				guiState.mu.Lock()
				guiState.windows[id] = ws
				guiState.mu.Unlock()

				// Start the terminal connection
				go func() {
					time.Sleep(time.Millisecond * 100)
					err := term.RunWithConnection(stdinReader, stdoutReader)
					if err != nil {
						fmt.Fprintf(os.Stderr, "Terminal error: %v\n", err)
					}
				}()

				// Auto-focus the terminal
				canvas := newWindow.Canvas()
				if canvas != nil {
					canvas.Focus(term)
				}

				close(done)
			})

			<-done

			// Redirect #out, #in, #err to the console channels
			// Set in both root environment (for future modules) and current module context
			// Use the ps instance that registered these commands, not guiState.ps (which may be nil)
			ps.SetInheritedObject("io", "#out", consoleOutCh)
			ps.SetInheritedObject("io", "#stdout", consoleOutCh)
			ps.SetInheritedObject("io", "#in", consoleInCh)
			ps.SetInheritedObject("io", "#stdin", consoleInCh)
			ps.SetInheritedObject("io", "#err", consoleOutCh)
			ps.SetInheritedObject("io", "#stderr", consoleOutCh)

			// Also set in current module's ObjectsModule for immediate effect
			ctx.SetModuleObject("#out", consoleOutCh)
			ctx.SetModuleObject("#stdout", consoleOutCh)
			ctx.SetModuleObject("#in", consoleInCh)
			ctx.SetModuleObject("#stdin", consoleInCh)
			ctx.SetModuleObject("#err", consoleOutCh)
			ctx.SetModuleObject("#stderr", consoleOutCh)
		} else {
			// Standard window (no console)
			fyne.Do(func() {
				newWindow := guiState.app.NewWindow(title)
				newWindow.Resize(fyne.NewSize(width, height))

				ws = &WindowState{
					window:       newWindow,
					content:      container.NewVBox(),
					leftContent:  container.NewVBox(),
					rightContent: container.NewVBox(),
					usingSplit:   false,
					widgets:      make(map[string]fyne.CanvasObject),
					containers:   make(map[string]*fyne.Container),
				}

				// Add close handler to remove window from tracking
				windowID := id // Capture for closure
				newWindow.SetCloseIntercept(func() {
					guiState.mu.Lock()
					delete(guiState.windows, windowID)
					guiState.mu.Unlock()
					newWindow.Close()
				})

				newWindow.SetContent(ws.content)
				newWindow.CenterOnScreen()
				newWindow.Show()

				guiState.mu.Lock()
				guiState.windows[id] = ws
				guiState.mu.Unlock()

				close(done)
			})

			<-done
		}

		// Return window marker
		marker := fmt.Sprintf("\x00WINDOW:%d\x00", id)
		windowSymbol := pawscript.Symbol(marker)

		// Set #window as an inherited object so macros can access it
		// This allows scripts to define macros that implicitly use the window
		ps.SetInheritedObject("gui", "#window", windowSymbol)
		ctx.SetModuleObject("#window", windowSymbol)

		ctx.SetResult(windowSymbol)
		return pawscript.BoolStatus(true)
	})

	// gui_split - Enable split layout with left/right panels
	// Usage: gui_split [offset] or gui_split #window, [offset]
	ps.RegisterCommand("gui_split", func(ctx *pawscript.Context) pawscript.Result {
		ws, args := resolveWindowWithContext(ctx)
		if ws == nil {
			ctx.LogError(pawscript.CatCommand, "No window available - create one with gui_window first")
			return pawscript.BoolStatus(false)
		}

		offset := 0.5
		if len(args) >= 1 {
			if o, ok := toFloat(args[0]); ok {
				offset = o
			}
		}

		fyne.Do(func() {
			guiState.mu.Lock()
			defer guiState.mu.Unlock()

			if !ws.usingSplit {
				leftScroll := container.NewVScroll(ws.leftContent)
				rightScroll := container.NewVScroll(ws.rightContent)
				ws.splitView = container.NewHSplit(leftScroll, rightScroll)
				ws.window.SetContent(ws.splitView)
				ws.usingSplit = true
			}
			ws.splitView.SetOffset(offset)
		})
		return pawscript.BoolStatus(true)
	})

	// gui_title - Set window title
	// Usage: gui_title <title> or gui_title #window, <title>
	ps.RegisterCommand("gui_title", func(ctx *pawscript.Context) pawscript.Result {
		ws, args := resolveWindowWithContext(ctx)
		if ws == nil {
			ctx.LogError(pawscript.CatCommand, "No window available - create one with gui_window first")
			return pawscript.BoolStatus(false)
		}
		if len(args) < 1 {
			ctx.LogError(pawscript.CatCommand, "Usage: gui_title [#window,] <window_title>")
			return pawscript.BoolStatus(false)
		}
		title := fmt.Sprintf("%v", args[0])
		fyne.Do(func() {
			ws.window.SetTitle(title)
		})
		return pawscript.BoolStatus(true)
	})

	// gui_resize - Resize window
	// Usage: gui_resize <w>, <h> or gui_resize #window, <w>, <h>
	ps.RegisterCommand("gui_resize", func(ctx *pawscript.Context) pawscript.Result {
		ws, args := resolveWindowWithContext(ctx)
		if ws == nil {
			ctx.LogError(pawscript.CatCommand, "No window available - create one with gui_window first")
			return pawscript.BoolStatus(false)
		}
		if len(args) < 2 {
			ctx.LogError(pawscript.CatCommand, "Usage: gui_resize [#window,] <width>, <height>")
			return pawscript.BoolStatus(false)
		}
		width, wOk := toFloat(args[0])
		height, hOk := toFloat(args[1])
		if !wOk || !hOk {
			ctx.LogError(pawscript.CatArgument, "Invalid dimensions")
			return pawscript.BoolStatus(false)
		}
		fyne.Do(func() {
			ws.window.Resize(fyne.NewSize(float32(width), float32(height)))
		})
		return pawscript.BoolStatus(true)
	})

	// gui_label - Create a label widget
	// Usage: gui_label <text> or gui_label #window, <text>
	ps.RegisterCommand("gui_label", func(ctx *pawscript.Context) pawscript.Result {
		ws, args := resolveWindowWithContext(ctx)
		if ws == nil {
			ctx.LogError(pawscript.CatCommand, "No window available - create one with gui_window first")
			return pawscript.BoolStatus(false)
		}
		if len(args) < 1 {
			ctx.LogError(pawscript.CatCommand, "Usage: gui_label [#window,] <text> [id: <name>] [panel: left|right]")
			return pawscript.BoolStatus(false)
		}
		text := fmt.Sprintf("%v", args[0])
		lbl := widget.NewLabel(text)

		id := ""
		if idVal, ok := ctx.NamedArgs["id"]; ok {
			id = fmt.Sprintf("%v", idVal)
			guiState.mu.Lock()
			ws.widgets[id] = lbl
			guiState.mu.Unlock()
		}

		addToPanel(ctx, ws, lbl)

		if id != "" {
			ctx.SetResult(id)
		}
		return pawscript.BoolStatus(true)
	})

	// gui_button - Create a button widget with callback
	// Usage: gui_button <text> or gui_button #window, <text>
	ps.RegisterCommand("gui_button", func(ctx *pawscript.Context) pawscript.Result {
		ws, args := resolveWindowWithContext(ctx)
		if ws == nil {
			ctx.LogError(pawscript.CatCommand, "No window available - create one with gui_window first")
			return pawscript.BoolStatus(false)
		}
		if len(args) < 1 {
			ctx.LogError(pawscript.CatCommand, "Usage: gui_button [#window,] <text> [id: <name>] [onclick: <macro_name>]")
			return pawscript.BoolStatus(false)
		}
		text := fmt.Sprintf("%v", args[0])

		var onclickMacro string
		if onclick, ok := ctx.NamedArgs["onclick"]; ok {
			onclickMacro = fmt.Sprintf("%v", onclick)
		}

		btn := widget.NewButton(text, func() {
			if onclickMacro != "" {
				go func() {
					// Use the ps instance that registered these commands, not guiState.ps (which may be nil)
					result := ps.Execute(onclickMacro)
					if result == pawscript.BoolStatus(false) {
						fmt.Fprintf(os.Stderr, "Button callback error: %s\n", onclickMacro)
					}
				}()
			}
		})

		id := ""
		if idVal, ok := ctx.NamedArgs["id"]; ok {
			id = fmt.Sprintf("%v", idVal)
			guiState.mu.Lock()
			ws.widgets[id] = btn
			guiState.mu.Unlock()
		}

		addToPanel(ctx, ws, btn)

		if id != "" {
			ctx.SetResult(id)
		}
		return pawscript.BoolStatus(true)
	})

	// gui_entry - Create a text entry widget
	// Usage: gui_entry [placeholder] or gui_entry #window, [placeholder]
	ps.RegisterCommand("gui_entry", func(ctx *pawscript.Context) pawscript.Result {
		ws, args := resolveWindowWithContext(ctx)
		if ws == nil {
			ctx.LogError(pawscript.CatCommand, "No window available - create one with gui_window first")
			return pawscript.BoolStatus(false)
		}
		placeholder := ""
		if len(args) >= 1 {
			placeholder = fmt.Sprintf("%v", args[0])
		}

		entry := widget.NewEntry()
		entry.SetPlaceHolder(placeholder)

		id := ""
		if idVal, ok := ctx.NamedArgs["id"]; ok {
			id = fmt.Sprintf("%v", idVal)
			guiState.mu.Lock()
			ws.widgets[id] = entry
			guiState.mu.Unlock()
		}

		addToPanel(ctx, ws, entry)

		if id != "" {
			ctx.SetResult(id)
		}
		return pawscript.BoolStatus(true)
	})

	// gui_get - Get value from a widget
	// Searches all windows for the widget ID
	ps.RegisterCommand("gui_get", func(ctx *pawscript.Context) pawscript.Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(pawscript.CatCommand, "Usage: gui_get <widget_id>")
			return pawscript.BoolStatus(false)
		}
		id := fmt.Sprintf("%v", ctx.Args[0])

		// Search all windows for the widget
		var w fyne.CanvasObject
		guiState.mu.RLock()
		for _, ws := range guiState.windows {
			if widget, exists := ws.widgets[id]; exists {
				w = widget
				break
			}
		}
		guiState.mu.RUnlock()

		if w == nil {
			ctx.LogError(pawscript.CatArgument, fmt.Sprintf("Widget not found: %s", id))
			return pawscript.BoolStatus(false)
		}

		switch widget := w.(type) {
		case *widget.Entry:
			ctx.SetResult(widget.Text)
		case *widget.Label:
			ctx.SetResult(widget.Text)
		default:
			ctx.SetResult("")
		}
		return pawscript.BoolStatus(true)
	})

	// gui_set - Set value on a widget
	// Searches all windows for the widget ID
	ps.RegisterCommand("gui_set", func(ctx *pawscript.Context) pawscript.Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(pawscript.CatCommand, "Usage: gui_set <widget_id>, <value>")
			return pawscript.BoolStatus(false)
		}
		id := fmt.Sprintf("%v", ctx.Args[0])
		value := fmt.Sprintf("%v", ctx.Args[1])

		// Search all windows for the widget
		var w fyne.CanvasObject
		guiState.mu.RLock()
		for _, ws := range guiState.windows {
			if widget, exists := ws.widgets[id]; exists {
				w = widget
				break
			}
		}
		guiState.mu.RUnlock()

		if w == nil {
			ctx.LogError(pawscript.CatArgument, fmt.Sprintf("Widget not found: %s", id))
			return pawscript.BoolStatus(false)
		}

		fyne.Do(func() {
			switch widget := w.(type) {
			case *widget.Entry:
				widget.SetText(value)
			case *widget.Label:
				widget.SetText(value)
			}
		})
		return pawscript.BoolStatus(true)
	})

	// gui_clear - Clear all widgets from content
	// Usage: gui_clear or gui_clear #window
	ps.RegisterCommand("gui_clear", func(ctx *pawscript.Context) pawscript.Result {
		ws, _ := resolveWindowWithContext(ctx)
		if ws == nil {
			ctx.LogError(pawscript.CatCommand, "No window available - create one with gui_window first")
			return pawscript.BoolStatus(false)
		}
		fyne.Do(func() {
			guiState.mu.Lock()
			ws.content.RemoveAll()
			ws.widgets = make(map[string]fyne.CanvasObject)
			guiState.mu.Unlock()
			ws.content.Refresh()
		})
		return pawscript.BoolStatus(true)
	})

	// gui_close - Close a window
	// Usage: gui_close #window
	ps.RegisterCommand("gui_close", func(ctx *pawscript.Context) pawscript.Result {
		ws, _ := resolveWindow(ctx)
		if ws == nil {
			ctx.LogError(pawscript.CatCommand, "Usage: gui_close #window")
			return pawscript.BoolStatus(false)
		}
		fyne.Do(func() {
			ws.window.Close()
		})
		return pawscript.BoolStatus(true)
	})

	// gui_focus - Focus a widget by ID
	// Searches all windows for the widget ID and focuses it
	ps.RegisterCommand("gui_focus", func(ctx *pawscript.Context) pawscript.Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(pawscript.CatCommand, "Usage: gui_focus <widget_id>")
			return pawscript.BoolStatus(false)
		}
		id := fmt.Sprintf("%v", ctx.Args[0])

		// Search all windows for the widget
		var targetWidget fyne.CanvasObject
		var targetWindow fyne.Window
		guiState.mu.RLock()
		for _, ws := range guiState.windows {
			if w, exists := ws.widgets[id]; exists {
				targetWidget = w
				targetWindow = ws.window
				break
			}
		}
		guiState.mu.RUnlock()

		if targetWidget == nil {
			ctx.LogError(pawscript.CatArgument, fmt.Sprintf("Widget not found: %s", id))
			return pawscript.BoolStatus(false)
		}

		// Check if widget is focusable
		focusable, ok := targetWidget.(fyne.Focusable)
		if !ok {
			ctx.LogError(pawscript.CatArgument, fmt.Sprintf("Widget is not focusable: %s", id))
			return pawscript.BoolStatus(false)
		}

		fyne.Do(func() {
			canvas := targetWindow.Canvas()
			if canvas != nil {
				canvas.Focus(focusable)
			}
		})
		return pawscript.BoolStatus(true)
	})

	// gui_msgbox - Show a message dialog
	// Usage: gui_msgbox <message> or gui_msgbox #window, <message>
	ps.RegisterCommand("gui_msgbox", func(ctx *pawscript.Context) pawscript.Result {
		ws, args := resolveWindowWithContext(ctx)
		if ws == nil {
			ctx.LogError(pawscript.CatCommand, "No window available - create one with gui_window first")
			return pawscript.BoolStatus(false)
		}
		if len(args) < 1 {
			ctx.LogError(pawscript.CatCommand, "Usage: gui_msgbox [#window,] <message> [title: <title>]")
			return pawscript.BoolStatus(false)
		}
		message := fmt.Sprintf("%v", args[0])
		title := "Message"
		if t, ok := ctx.NamedArgs["title"]; ok {
			title = fmt.Sprintf("%v", t)
		}

		fyne.Do(func() {
			dialog := widget.NewLabel(message)
			popup := widget.NewModalPopUp(
				container.NewVBox(
					widget.NewLabel(title),
					dialog,
					widget.NewButton("OK", func() {}),
				),
				ws.window.Canvas(),
			)
			popup.Show()
		})
		return pawscript.BoolStatus(true)
	})

	// gui_console - Create a terminal console widget
	// Usage: gui_console [w], [h] or gui_console #window, [w], [h]
	ps.RegisterCommand("gui_console", func(ctx *pawscript.Context) pawscript.Result {
		ws, args := resolveWindowWithContext(ctx)
		if ws == nil {
			ctx.LogError(pawscript.CatCommand, "No window available - create one with gui_window first")
			return pawscript.BoolStatus(false)
		}

		width := float32(600)
		height := float32(400)

		if len(args) >= 2 {
			if w, ok := toFloat(args[0]); ok {
				width = float32(w)
			}
			if h, ok := toFloat(args[1]); ok {
				height = float32(h)
			}
		}

		stdinReader, stdinWriter := io.Pipe()
		stdoutReader, stdoutWriter := io.Pipe()

		charWidth := int(width / 9)
		charHeight := int(height / 18)
		if charWidth < 1 {
			charWidth = 80
		}
		if charHeight < 1 {
			charHeight = 24
		}

		consoleOutCh, consoleInCh, termCaps := createConsoleChannels(stdinReader, stdoutWriter, charWidth, charHeight)
		_ = termCaps

		term := terminal.New()
		ws.terminal = term

		clickInterceptor := newClickInterceptor(term)
		termWithInterceptor := container.NewStack(term, clickInterceptor)
		sizedTerm := newSizedWidget(termWithInterceptor, fyne.NewSize(width, height))

		id := ""
		if idVal, ok := ctx.NamedArgs["id"]; ok {
			id = fmt.Sprintf("%v", idVal)
			guiState.mu.Lock()
			ws.widgets[id] = sizedTerm
			guiState.mu.Unlock()
		}

		addToPanel(ctx, ws, sizedTerm)

		go func() {
			time.Sleep(time.Millisecond * 100)
			err := term.RunWithConnection(stdinWriter, stdoutReader)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Terminal error: %v\n", err)
			}
		}()

		outID := ctx.StoreObject(consoleOutCh, "channel")
		inID := ctx.StoreObject(consoleInCh, "channel")

		ctx.ClaimObjectReference(outID)
		ctx.ClaimObjectReference(inID)

		outMarker := pawscript.Symbol(fmt.Sprintf("\x00CHANNEL:%d\x00", outID))
		inMarker := pawscript.Symbol(fmt.Sprintf("\x00CHANNEL:%d\x00", inID))
		errMarker := outMarker

		channelList := ctx.NewStoredListWithRefs([]interface{}{outMarker, inMarker, errMarker}, nil)
		listID := ctx.StoreObject(channelList, "list")
		listMarker := fmt.Sprintf("\x00LIST:%d\x00", listID)

		ctx.SetResult(pawscript.Symbol(listMarker))
		return pawscript.BoolStatus(true)
	})
}

// consoleInputHandler manages readline input in a background goroutine
type consoleInputHandler struct {
	stdinReader  *io.PipeReader
	stdoutWriter *io.PipeWriter
	lines        chan string
	readActive   chan struct{}
	mu           sync.Mutex
	waiting      bool
}

func newConsoleInputHandler(stdinReader *io.PipeReader, stdoutWriter *io.PipeWriter) *consoleInputHandler {
	h := &consoleInputHandler{
		stdinReader:  stdinReader,
		stdoutWriter: stdoutWriter,
		lines:        make(chan string, 10),
		readActive:   make(chan struct{}, 1),
	}
	go h.readLoop()
	return h
}

func (h *consoleInputHandler) writeToTerminal(text string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\n", "\r\n")
	fmt.Fprint(h.stdoutWriter, text)
}

func (h *consoleInputHandler) isWaiting() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.waiting
}

func (h *consoleInputHandler) setWaiting(w bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.waiting = w
}

func (h *consoleInputHandler) readLoop() {
	var line []byte
	buf := make([]byte, 1)

	for {
		n, err := h.stdinReader.Read(buf)
		if err != nil {
			close(h.lines)
			return
		}
		if n == 0 {
			continue
		}

		b := buf[0]

		if !h.isWaiting() {
			continue
		}

		switch {
		case b == '\r' || b == '\n':
			h.writeToTerminal("\n")
			select {
			case h.lines <- string(line):
			default:
				select {
				case <-h.lines:
				default:
				}
				h.lines <- string(line)
			}
			line = line[:0]

		case b == 0x7F || b == 0x08:
			if len(line) > 0 {
				line = line[:len(line)-1]
				fmt.Fprint(h.stdoutWriter, "\b \b")
			}

		case b == 0x03:
			h.writeToTerminal("^C\n")
			select {
			case h.lines <- "":
			default:
			}
			line = line[:0]

		case b == 0x15:
			for range line {
				fmt.Fprint(h.stdoutWriter, "\b \b")
			}
			line = line[:0]

		case b >= 32 && b < 127:
			line = append(line, b)
			fmt.Fprint(h.stdoutWriter, string(b))

		case b >= 0xC0:
			var utf8Bytes []byte
			utf8Bytes = append(utf8Bytes, b)

			var remaining int
			if b >= 0xF0 {
				remaining = 3
			} else if b >= 0xE0 {
				remaining = 2
			} else {
				remaining = 1
			}

			for i := 0; i < remaining; i++ {
				n, err := h.stdinReader.Read(buf)
				if err != nil || n == 0 {
					break
				}
				utf8Bytes = append(utf8Bytes, buf[0])
			}

			line = append(line, utf8Bytes...)
			fmt.Fprint(h.stdoutWriter, string(utf8Bytes))
		}
	}
}

func (h *consoleInputHandler) readLine() (string, error) {
	h.setWaiting(true)
	defer h.setWaiting(false)

	line, ok := <-h.lines
	if !ok {
		return "", io.EOF
	}
	return line, nil
}

// createConsoleChannels creates StoredChannels for the console terminal
func createConsoleChannels(stdinReader *io.PipeReader, stdoutWriter *io.PipeWriter, width, height int) (*pawscript.StoredChannel, *pawscript.StoredChannel, *pawscript.TerminalCapabilities) {
	inputHandler := newConsoleInputHandler(stdinReader, stdoutWriter)

	termCaps := &pawscript.TerminalCapabilities{
		TermType:      "gui-console",
		IsTerminal:    true,
		SupportsANSI:  true,
		SupportsColor: true,
		ColorDepth:    256,
		Width:         width,
		Height:        height,
		SupportsInput: true,
		EchoEnabled:   true,
		LineMode:      true,
		Metadata:      make(map[string]interface{}),
	}

	consoleOutCh := &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		Terminal:         termCaps,
		NativeSend: func(v interface{}) error {
			text := fmt.Sprintf("%v", v)
			text = strings.ReplaceAll(text, "\r\n", "\n")
			text = strings.ReplaceAll(text, "\n", "\r\n")
			_, err := fmt.Fprint(stdoutWriter, text)
			return err
		},
		NativeRecv: func() (interface{}, error) {
			return nil, fmt.Errorf("cannot receive from console_out")
		},
	}

	consoleInCh := &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		Terminal:         termCaps,
		NativeRecv: func() (interface{}, error) {
			return inputHandler.readLine()
		},
		NativeSend: func(v interface{}) error {
			return fmt.Errorf("cannot send to console_in")
		},
	}

	return consoleOutCh, consoleInCh, termCaps
}

// Custom themes for light/dark mode toggle
type darkTheme struct{}

func (d *darkTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	return theme.DefaultTheme().Color(name, theme.VariantDark)
}

func (d *darkTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (d *darkTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (d *darkTheme) Size(name fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(name)
}

type lightTheme struct{}

func (l *lightTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	return theme.DefaultTheme().Color(name, theme.VariantLight)
}

func (l *lightTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (l *lightTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (l *lightTheme) Size(name fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(name)
}

// tappableLabel is a label that supports tap and double-tap events
type tappableLabel struct {
	widget.Label
	onTapped       func()
	onDoubleTapped func()
}

var _ fyne.Tappable = (*tappableLabel)(nil)
var _ fyne.DoubleTappable = (*tappableLabel)(nil)

func newTappableLabel(text string, onTapped, onDoubleTapped func()) *tappableLabel {
	t := &tappableLabel{
		onTapped:       onTapped,
		onDoubleTapped: onDoubleTapped,
	}
	t.ExtendBaseWidget(t)
	t.SetText(text)
	return t
}

func (t *tappableLabel) Tapped(_ *fyne.PointEvent) {
	if t.onTapped != nil {
		t.onTapped()
	}
}

func (t *tappableLabel) DoubleTapped(_ *fyne.PointEvent) {
	if t.onDoubleTapped != nil {
		t.onDoubleTapped()
	}
}

// clickInterceptor is a transparent widget that sits on top of the terminal
type clickInterceptor struct {
	widget.BaseWidget
	terminal *terminal.Terminal
}

var _ fyne.Tappable = (*clickInterceptor)(nil)

func newClickInterceptor(term *terminal.Terminal) *clickInterceptor {
	c := &clickInterceptor{
		terminal: term,
	}
	c.ExtendBaseWidget(c)
	return c
}

func (c *clickInterceptor) Tapped(_ *fyne.PointEvent) {
	if c == nil || c.terminal == nil {
		return
	}
	c.terminal.FocusGained()
	app := fyne.CurrentApp()
	if app == nil {
		return
	}
	driver := app.Driver()
	if driver == nil {
		return
	}
	canvas := driver.CanvasForObject(c.terminal)
	if canvas == nil {
		return
	}
	canvas.Focus(c.terminal)
}

func (c *clickInterceptor) CreateRenderer() fyne.WidgetRenderer {
	return &clickInterceptorRenderer{}
}

type clickInterceptorRenderer struct{}

func (r *clickInterceptorRenderer) Layout(size fyne.Size)        {}
func (r *clickInterceptorRenderer) MinSize() fyne.Size           { return fyne.NewSize(0, 0) }
func (r *clickInterceptorRenderer) Refresh()                     {}
func (r *clickInterceptorRenderer) Objects() []fyne.CanvasObject { return nil }
func (r *clickInterceptorRenderer) Destroy()                     {}

// sizedWidget wraps a canvas object and enforces a minimum size
type sizedWidget struct {
	widget.BaseWidget
	wrapped fyne.CanvasObject
	minSize fyne.Size
}

func newSizedWidget(wrapped fyne.CanvasObject, minSize fyne.Size) *sizedWidget {
	s := &sizedWidget{
		wrapped: wrapped,
		minSize: minSize,
	}
	s.ExtendBaseWidget(s)
	return s
}

func (s *sizedWidget) CreateRenderer() fyne.WidgetRenderer {
	return &sizedWidgetRenderer{widget: s}
}

func (s *sizedWidget) MinSize() fyne.Size {
	return s.minSize
}

type sizedWidgetRenderer struct {
	widget *sizedWidget
}

func (r *sizedWidgetRenderer) Layout(size fyne.Size) {
	r.widget.wrapped.Resize(size)
	r.widget.wrapped.Move(fyne.NewPos(0, 0))
}

func (r *sizedWidgetRenderer) MinSize() fyne.Size {
	return r.widget.minSize
}

func (r *sizedWidgetRenderer) Refresh() {
	r.widget.wrapped.Refresh()
}

func (r *sizedWidgetRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.widget.wrapped}
}

func (r *sizedWidgetRenderer) Destroy() {}

// toFloat converts an interface{} to float64
func toFloat(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case string:
		var f float64
		_, err := fmt.Sscanf(val, "%f", &f)
		return f, err == nil
	}
	return 0, false
}
