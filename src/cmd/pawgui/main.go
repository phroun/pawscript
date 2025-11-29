// pawgui - PawScript with Fyne GUI support
// A drop-in replacement for paw with additional GUI capabilities
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/fyne-io/terminal"
	pawscript "github.com/phroun/pawscript"
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
}

// GuiState holds the current GUI state accessible to PawScript
type GuiState struct {
	mu      sync.RWMutex
	app     fyne.App
	ps      *pawscript.PawScript
	windows map[int]*WindowState // Windows by object ID
	nextID  int                  // Next window ID

	// Legacy fields for backwards compatibility (default window)
	mainWindow   fyne.Window
	widgets      map[string]fyne.CanvasObject
	containers   map[string]*fyne.Container
	content      *fyne.Container  // Main content (used when no split)
	leftContent  *fyne.Container  // Left panel content
	rightContent *fyne.Container  // Right panel content
	splitView    *container.Split // HSplit container (created on demand)
	usingSplit   bool             // Whether we're using split layout
	terminal     *terminal.Terminal
}

var guiState *GuiState

func main() {
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
		// No filename and stdin is not redirected - show usage
		showCopyright()
		showUsage()
		os.Exit(1)
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

	// Create the Fyne application
	fyneApp := app.New()
	mainWindow := fyneApp.NewWindow("PawScript GUI")
	mainWindow.Resize(fyne.NewSize(400, 300))

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
	ps.RegisterStandardLibrary(scriptArgs)

	// Initialize GUI state with left/right panel support
	guiState = &GuiState{
		app:          fyneApp,
		mainWindow:   mainWindow,
		widgets:      make(map[string]fyne.CanvasObject),
		containers:   make(map[string]*fyne.Container),
		ps:           ps,
		content:      container.NewVBox(),
		leftContent:  container.NewVBox(),
		rightContent: container.NewVBox(),
		usingSplit:   false,
		windows:      make(map[int]*WindowState),
		nextID:       1,
	}

	// Register GUI commands
	registerGuiCommands(ps)

	// Set initial content
	mainWindow.SetContent(guiState.content)

	// Run the script in a goroutine so the GUI can start
	go func() {
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
	}()

	// Run the Fyne event loop (blocking)
	mainWindow.ShowAndRun()
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
  gui_title [#win,] <text>      Set window title
  gui_resize [#win,] <w>, <h>   Resize window
  gui_close #win                Close a window
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

Examples:
  pawgui examples/gui-demo.paw       # Run the GUI demo
  pawgui examples/gui-console.paw    # Run the console demo
  pawgui --unrestricted script.paw   # No file/exec restrictions
  pawgui --scale 2.0 script.paw      # Use 2x GUI scale
`
	fmt.Fprint(os.Stderr, usage)
}

// addToPanel adds a widget to the appropriate panel based on named args
func addToPanel(ctx *pawscript.Context, widget fyne.CanvasObject) {
	panel := "default"
	if p, ok := ctx.NamedArgs["panel"]; ok {
		panel = fmt.Sprintf("%v", p)
	}

	fyne.Do(func() {
		guiState.mu.Lock()
		defer guiState.mu.Unlock()

		switch panel {
		case "left":
			guiState.leftContent.Add(widget)
			guiState.leftContent.Refresh()
			ensureSplitLayout()
		case "right":
			guiState.rightContent.Add(widget)
			guiState.rightContent.Refresh()
			ensureSplitLayout()
		default:
			guiState.content.Add(widget)
			guiState.content.Refresh()
		}
	})
}

// ensureSplitLayout switches to split layout if not already using it
// Must be called with guiState.mu held
func ensureSplitLayout() {
	if guiState.usingSplit {
		return
	}

	leftScroll := container.NewVScroll(guiState.leftContent)
	rightScroll := container.NewVScroll(guiState.rightContent)
	guiState.splitView = container.NewHSplit(leftScroll, rightScroll)
	guiState.splitView.SetOffset(0.4)

	guiState.mainWindow.SetContent(guiState.splitView)
	guiState.usingSplit = true
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

// getWindowOrDefault returns the resolved window state or creates one for the main window
func getWindowOrDefault(ws *WindowState) *WindowState {
	if ws != nil {
		return ws
	}
	// Return a wrapper around the main window state
	return &WindowState{
		window:       guiState.mainWindow,
		content:      guiState.content,
		leftContent:  guiState.leftContent,
		rightContent: guiState.rightContent,
		splitView:    guiState.splitView,
		usingSplit:   guiState.usingSplit,
		widgets:      guiState.widgets,
		containers:   guiState.containers,
		terminal:     guiState.terminal,
	}
}

// registerGuiCommands registers all GUI-related commands with PawScript
func registerGuiCommands(ps *pawscript.PawScript) {
	// gui_window - Create a new window and return a handle
	// Usage: #mywin: {gui_window "Title"} or #mywin: {gui_window "Title", 800, 600}
	ps.RegisterCommand("gui_window", func(ctx *pawscript.Context) pawscript.Result {
		title := "PawScript Window"
		width := float32(400)
		height := float32(300)

		if len(ctx.Args) >= 1 {
			title = fmt.Sprintf("%v", ctx.Args[0])
		}
		if len(ctx.Args) >= 3 {
			if w, ok := toFloat(ctx.Args[1]); ok {
				width = float32(w)
			}
			if h, ok := toFloat(ctx.Args[2]); ok {
				height = float32(h)
			}
		}

		// Create window state
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

			newWindow.SetContent(ws.content)
			newWindow.Show()

			guiState.mu.Lock()
			guiState.windows[id] = ws
			guiState.mu.Unlock()

			close(done)
		})

		<-done

		// Return window marker
		marker := fmt.Sprintf("\x00WINDOW:%d\x00", id)
		ctx.SetResult(pawscript.Symbol(marker))
		return pawscript.BoolStatus(true)
	})

	// gui_split - Enable split layout with left/right panels
	ps.RegisterCommand("gui_split", func(ctx *pawscript.Context) pawscript.Result {
		offset := 0.5
		if len(ctx.Args) >= 1 {
			if o, ok := toFloat(ctx.Args[0]); ok {
				offset = o
			}
		}

		fyne.Do(func() {
			guiState.mu.Lock()
			defer guiState.mu.Unlock()

			if !guiState.usingSplit {
				leftScroll := container.NewVScroll(guiState.leftContent)
				rightScroll := container.NewVScroll(guiState.rightContent)
				guiState.splitView = container.NewHSplit(leftScroll, rightScroll)
				guiState.mainWindow.SetContent(guiState.splitView)
				guiState.usingSplit = true
			}
			guiState.splitView.SetOffset(offset)
		})
		return pawscript.BoolStatus(true)
	})

	// gui_title - Set window title
	// Usage: gui_title <title> or gui_title #window, <title>
	ps.RegisterCommand("gui_title", func(ctx *pawscript.Context) pawscript.Result {
		ws, args := resolveWindow(ctx)
		if len(args) < 1 {
			ctx.LogError(pawscript.CatCommand, "Usage: gui_title [#window,] <window_title>")
			return pawscript.BoolStatus(false)
		}
		title := fmt.Sprintf("%v", args[0])
		fyne.Do(func() {
			if ws != nil {
				ws.window.SetTitle(title)
			} else {
				guiState.mainWindow.SetTitle(title)
			}
		})
		return pawscript.BoolStatus(true)
	})

	// gui_resize - Resize window
	// Usage: gui_resize <w>, <h> or gui_resize #window, <w>, <h>
	ps.RegisterCommand("gui_resize", func(ctx *pawscript.Context) pawscript.Result {
		ws, args := resolveWindow(ctx)
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
			if ws != nil {
				ws.window.Resize(fyne.NewSize(float32(width), float32(height)))
			} else {
				guiState.mainWindow.Resize(fyne.NewSize(float32(width), float32(height)))
			}
		})
		return pawscript.BoolStatus(true)
	})

	// gui_label - Create a label widget
	ps.RegisterCommand("gui_label", func(ctx *pawscript.Context) pawscript.Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(pawscript.CatCommand, "Usage: gui_label <text> [id: <name>] [panel: left|right]")
			return pawscript.BoolStatus(false)
		}
		text := fmt.Sprintf("%v", ctx.Args[0])
		lbl := widget.NewLabel(text)

		id := ""
		if idVal, ok := ctx.NamedArgs["id"]; ok {
			id = fmt.Sprintf("%v", idVal)
			guiState.mu.Lock()
			guiState.widgets[id] = lbl
			guiState.mu.Unlock()
		}

		addToPanel(ctx, lbl)

		if id != "" {
			ctx.SetResult(id)
		}
		return pawscript.BoolStatus(true)
	})

	// gui_button - Create a button widget with callback
	ps.RegisterCommand("gui_button", func(ctx *pawscript.Context) pawscript.Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(pawscript.CatCommand, "Usage: gui_button <text> [id: <name>] [onclick: <macro_name>]")
			return pawscript.BoolStatus(false)
		}
		text := fmt.Sprintf("%v", ctx.Args[0])

		var onclickMacro string
		if onclick, ok := ctx.NamedArgs["onclick"]; ok {
			onclickMacro = fmt.Sprintf("%v", onclick)
		}

		btn := widget.NewButton(text, func() {
			if onclickMacro != "" {
				go func() {
					result := guiState.ps.Execute(onclickMacro)
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
			guiState.widgets[id] = btn
			guiState.mu.Unlock()
		}

		addToPanel(ctx, btn)

		if id != "" {
			ctx.SetResult(id)
		}
		return pawscript.BoolStatus(true)
	})

	// gui_entry - Create a text entry widget
	ps.RegisterCommand("gui_entry", func(ctx *pawscript.Context) pawscript.Result {
		placeholder := ""
		if len(ctx.Args) >= 1 {
			placeholder = fmt.Sprintf("%v", ctx.Args[0])
		}

		entry := widget.NewEntry()
		entry.SetPlaceHolder(placeholder)

		id := ""
		if idVal, ok := ctx.NamedArgs["id"]; ok {
			id = fmt.Sprintf("%v", idVal)
			guiState.mu.Lock()
			guiState.widgets[id] = entry
			guiState.mu.Unlock()
		}

		addToPanel(ctx, entry)

		if id != "" {
			ctx.SetResult(id)
		}
		return pawscript.BoolStatus(true)
	})

	// gui_get - Get value from a widget
	ps.RegisterCommand("gui_get", func(ctx *pawscript.Context) pawscript.Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(pawscript.CatCommand, "Usage: gui_get <widget_id>")
			return pawscript.BoolStatus(false)
		}
		id := fmt.Sprintf("%v", ctx.Args[0])

		guiState.mu.RLock()
		w, exists := guiState.widgets[id]
		guiState.mu.RUnlock()

		if !exists {
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
	ps.RegisterCommand("gui_set", func(ctx *pawscript.Context) pawscript.Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(pawscript.CatCommand, "Usage: gui_set <widget_id>, <value>")
			return pawscript.BoolStatus(false)
		}
		id := fmt.Sprintf("%v", ctx.Args[0])
		value := fmt.Sprintf("%v", ctx.Args[1])

		guiState.mu.RLock()
		w, exists := guiState.widgets[id]
		guiState.mu.RUnlock()

		if !exists {
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
	ps.RegisterCommand("gui_clear", func(ctx *pawscript.Context) pawscript.Result {
		fyne.Do(func() {
			guiState.mu.Lock()
			guiState.content.RemoveAll()
			guiState.widgets = make(map[string]fyne.CanvasObject)
			guiState.mu.Unlock()
			guiState.content.Refresh()
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

	// gui_msgbox - Show a message dialog
	ps.RegisterCommand("gui_msgbox", func(ctx *pawscript.Context) pawscript.Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(pawscript.CatCommand, "Usage: gui_msgbox <message> [title: <title>]")
			return pawscript.BoolStatus(false)
		}
		message := fmt.Sprintf("%v", ctx.Args[0])
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
				guiState.mainWindow.Canvas(),
			)
			popup.Show()
		})
		return pawscript.BoolStatus(true)
	})

	// gui_console - Create a terminal console widget
	ps.RegisterCommand("gui_console", func(ctx *pawscript.Context) pawscript.Result {
		width := float32(600)
		height := float32(400)

		if len(ctx.Args) >= 2 {
			if w, ok := toFloat(ctx.Args[0]); ok {
				width = float32(w)
			}
			if h, ok := toFloat(ctx.Args[1]); ok {
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
		guiState.terminal = term

		clickInterceptor := newClickInterceptor(term)
		termWithInterceptor := container.NewStack(term, clickInterceptor)
		sizedTerm := newSizedWidget(termWithInterceptor, fyne.NewSize(width, height))

		id := ""
		if idVal, ok := ctx.NamedArgs["id"]; ok {
			id = fmt.Sprintf("%v", idVal)
			guiState.mu.Lock()
			guiState.widgets[id] = sizedTerm
			guiState.mu.Unlock()
		}

		addToPanel(ctx, sizedTerm)

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
	c.terminal.FocusGained()
	fyne.CurrentApp().Driver().CanvasForObject(c.terminal).Focus(c.terminal)
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
