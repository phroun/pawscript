// pawgui - PawScript with Fyne GUI support
// A proof of concept for running PawScript with GUI capabilities
package main

import (
	"fmt"
	"io"
	"os"
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

// GuiState holds the current GUI state accessible to PawScript
type GuiState struct {
	mu         sync.RWMutex
	app        fyne.App
	mainWindow fyne.Window
	widgets    map[string]fyne.CanvasObject
	containers map[string]*fyne.Container
	ps         *pawscript.PawScript

	// Layout containers
	content      *fyne.Container // Main content (used when no split)
	leftContent  *fyne.Container // Left panel content
	rightContent *fyne.Container // Right panel content
	splitView    *container.Split // HSplit container (created on demand)
	usingSplit   bool             // Whether we're using split layout

	// Terminal widget (if created)
	terminal *terminal.Terminal
}

var guiState *GuiState

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: pawgui <script.paw>")
		fmt.Println("       pawgui -demo")
		fmt.Println("       pawgui -console")
		fmt.Println("       pawgui -d <script.paw>  (debug mode)")
		fmt.Println("       pawgui -d -demo         (debug mode)")
		os.Exit(1)
	}

	// Check for debug flag
	debugMode := false
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "-d" {
		debugMode = true
		args = args[1:]
	}
	if len(args) < 1 {
		fmt.Println("Usage: pawgui [-d] <script.paw>")
		os.Exit(1)
	}

	// Create the Fyne application
	fyneApp := app.New()
	mainWindow := fyneApp.NewWindow("PawScript GUI")
	mainWindow.Resize(fyne.NewSize(400, 300))

	// Create PawScript instance with debug mode if requested
	config := pawscript.DefaultConfig()
	config.Debug = debugMode
	ps := pawscript.New(config)
	ps.RegisterStandardLibrary(nil)

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
	}

	// Register GUI commands
	registerGuiCommands(ps)

	// Set initial content
	mainWindow.SetContent(guiState.content)

	// Handle script path
	scriptPath := args[0]
	var script string

	if scriptPath == "-demo" {
		script = demoScript
	} else if scriptPath == "-console" {
		script = consoleDemo
	} else {
		// Read the script file
		data, err := os.ReadFile(scriptPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading script: %v\n", err)
			os.Exit(1)
		}
		script = string(data)
	}

	// Run the script in a goroutine so the GUI can start
	// Use ExecuteFile to ensure exports (including macros) persist to root
	go func() {
		result := ps.ExecuteFile(script, scriptPath)
		if result == pawscript.BoolStatus(false) {
			fmt.Fprintf(os.Stderr, "Script execution failed\n")
		}
	}()

	// Run the Fyne event loop (blocking)
	mainWindow.ShowAndRun()
}

// addToPanel adds a widget to the appropriate panel based on named args
// Returns the target container
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

	// Create the split view with scrollable panels
	leftScroll := container.NewVScroll(guiState.leftContent)
	rightScroll := container.NewVScroll(guiState.rightContent)
	guiState.splitView = container.NewHSplit(leftScroll, rightScroll)
	guiState.splitView.SetOffset(0.4) // 40% left, 60% right

	guiState.mainWindow.SetContent(guiState.splitView)
	guiState.usingSplit = true
}

// registerGuiCommands registers all GUI-related commands with PawScript
func registerGuiCommands(ps *pawscript.PawScript) {
	// gui_split - Enable split layout with left/right panels
	ps.RegisterCommand("gui_split", func(ctx *pawscript.Context) pawscript.Result {
		// Optional offset (0.0-1.0, default 0.5)
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
	ps.RegisterCommand("gui_title", func(ctx *pawscript.Context) pawscript.Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(pawscript.CatCommand, "Usage: gui_title <window_title>")
			return pawscript.BoolStatus(false)
		}
		title := fmt.Sprintf("%v", ctx.Args[0])
		fyne.Do(func() {
			guiState.mainWindow.SetTitle(title)
		})
		return pawscript.BoolStatus(true)
	})

	// gui_resize - Resize window
	ps.RegisterCommand("gui_resize", func(ctx *pawscript.Context) pawscript.Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(pawscript.CatCommand, "Usage: gui_resize <width>, <height>")
			return pawscript.BoolStatus(false)
		}
		width, wOk := toFloat(ctx.Args[0])
		height, hOk := toFloat(ctx.Args[1])
		if !wOk || !hOk {
			ctx.LogError(pawscript.CatArgument, "Invalid dimensions")
			return pawscript.BoolStatus(false)
		}
		fyne.Do(func() {
			guiState.mainWindow.Resize(fyne.NewSize(float32(width), float32(height)))
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

		// Get optional ID
		id := ""
		if idVal, ok := ctx.NamedArgs["id"]; ok {
			id = fmt.Sprintf("%v", idVal)
			guiState.mu.Lock()
			guiState.widgets[id] = lbl
			guiState.mu.Unlock()
		}

		// Add to appropriate panel
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

		// Get optional onclick handler
		var onclickMacro string
		if onclick, ok := ctx.NamedArgs["onclick"]; ok {
			onclickMacro = fmt.Sprintf("%v", onclick)
		}

		btn := widget.NewButton(text, func() {
			if onclickMacro != "" {
				// Execute the macro when button is clicked
				go func() {
					callback := onclickMacro
					result := guiState.ps.Execute(fmt.Sprintf("IMPORT exports;%s"),callback)
					if result == pawscript.BoolStatus(false) {
						fmt.Fprintf(os.Stderr, "Button callback error\n")
					}
				}()
			}
		})

		// Get optional ID
		id := ""
		if idVal, ok := ctx.NamedArgs["id"]; ok {
			id = fmt.Sprintf("%v", idVal)
			guiState.mu.Lock()
			guiState.widgets[id] = btn
			guiState.mu.Unlock()
		}

		// Add to appropriate panel
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

		// Get optional ID (required to read value later)
		id := ""
		if idVal, ok := ctx.NamedArgs["id"]; ok {
			id = fmt.Sprintf("%v", idVal)
			guiState.mu.Lock()
			guiState.widgets[id] = entry
			guiState.mu.Unlock()
		}

		// Add to appropriate panel
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

		// Get value based on widget type
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

		// Set value based on widget type (thread-safe)
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

		// Show info dialog (thread-safe)
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
	// Returns a list: [out_channel, in_channel, err_channel]
	// - out_channel: send to display text in the terminal
	// - in_channel: recv to read keyboard input
	// - err_channel: same as out_channel (for compatibility)
	// You can have multiple consoles, each with their own channels
	ps.RegisterCommand("gui_console", func(ctx *pawscript.Context) pawscript.Result {
		// Default size
		width := float32(600)
		height := float32(400)

		// Get optional dimensions
		if len(ctx.Args) >= 2 {
			if w, ok := toFloat(ctx.Args[0]); ok {
				width = float32(w)
			}
			if h, ok := toFloat(ctx.Args[1]); ok {
				height = float32(h)
			}
		}

		// Create IO pipes for the console
		stdinReader, stdinWriter := io.Pipe()
		stdoutReader, stdoutWriter := io.Pipe()

		// Create console channels backed by pipes
		consoleOutCh, consoleInCh := createConsoleChannels(stdinReader, stdoutWriter)

		// Create terminal widget
		term := terminal.New()
		guiState.terminal = term

		// Wrap terminal in a sizedWidget to enforce minimum size
		sizedTerm := newSizedWidget(term, fyne.NewSize(width, height))

		// Get optional ID
		id := ""
		if idVal, ok := ctx.NamedArgs["id"]; ok {
			id = fmt.Sprintf("%v", idVal)
			guiState.mu.Lock()
			guiState.widgets[id] = sizedTerm
			guiState.mu.Unlock()
		}

		// Add to appropriate panel
		addToPanel(ctx, sizedTerm)

		// Connect the terminal to our pipes
		// RunWithConnection expects: in = where to write keyboard input, out = what to display
		go func() {
			err := term.RunWithConnection(stdinWriter, stdoutReader)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Terminal error: %v\n", err)
			}
		}()

		// Store channels and create markers
		outID := ctx.StoreObject(consoleOutCh, "channel")
		inID := ctx.StoreObject(consoleInCh, "channel")

		// Explicitly claim ownership - these channels are long-lived GUI resources
		ctx.ClaimObjectReference(outID)
		ctx.ClaimObjectReference(inID)

		// Use outCh for err as well (same channel)
		outMarker := pawscript.Symbol(fmt.Sprintf("\x00CHANNEL:%d\x00", outID))
		inMarker := pawscript.Symbol(fmt.Sprintf("\x00CHANNEL:%d\x00", inID))
		errMarker := outMarker // err uses same channel as out

		// Create a StoredList with the channel markers
		// NewStoredListWithRefs claims references to the channels via their markers
		channelList := ctx.NewStoredListWithRefs([]interface{}{outMarker, inMarker, errMarker}, nil)
		listID := ctx.StoreObject(channelList, "list")
		listMarker := fmt.Sprintf("\x00LIST:%d\x00", listID)

		// Return the list marker so unpacking works correctly
		ctx.SetResult(pawscript.Symbol(listMarker))
		return pawscript.BoolStatus(true)
	})
}

// createConsoleChannels creates StoredChannels for the console terminal
func createConsoleChannels(stdinReader *io.PipeReader, stdoutWriter *io.PipeWriter) (*pawscript.StoredChannel, *pawscript.StoredChannel) {
	// Helper to write to terminal with proper CRLF conversion
	writeToTerminal := func(text string) {
		text = strings.ReplaceAll(text, "\r\n", "\n")
		text = strings.ReplaceAll(text, "\n", "\r\n")
		fmt.Fprint(stdoutWriter, text)
	}

	// Console output channel - write to terminal display
	consoleOutCh := &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		NativeSend: func(v interface{}) error {
			// Convert \n to \r\n for proper terminal line endings
			text := fmt.Sprintf("%v", v)
			text = strings.ReplaceAll(text, "\r\n", "\n") // Normalize first
			text = strings.ReplaceAll(text, "\n", "\r\n") // Then convert to CRLF
			_, err := fmt.Fprint(stdoutWriter, text)
			return err
		},
		NativeRecv: func() (interface{}, error) {
			return nil, fmt.Errorf("cannot receive from console_out")
		},
	}

	// Console input channel - read from terminal keyboard with readline behavior
	consoleInCh := &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		NativeRecv: func() (interface{}, error) {
			// Readline-like input handling with echo
			var line []byte
			buf := make([]byte, 1)

			for {
				n, err := stdinReader.Read(buf)
				if err != nil {
					return string(line), err
				}
				if n == 0 {
					continue
				}

				b := buf[0]
				switch {
				case b == '\r' || b == '\n':
					// Enter pressed - echo newline and return line
					writeToTerminal("\n")
					return string(line), nil

				case b == 0x7F || b == 0x08:
					// Backspace (DEL or BS)
					if len(line) > 0 {
						line = line[:len(line)-1]
						// Echo: move back, space over, move back
						fmt.Fprint(stdoutWriter, "\b \b")
					}

				case b == 0x03:
					// Ctrl+C - return empty/interrupt
					writeToTerminal("^C\n")
					return "", fmt.Errorf("interrupted")

				case b == 0x15:
					// Ctrl+U - clear line
					for range line {
						fmt.Fprint(stdoutWriter, "\b \b")
					}
					line = line[:0]

				case b >= 32 && b < 127:
					// Printable ASCII - echo and append
					line = append(line, b)
					fmt.Fprint(stdoutWriter, string(b))

				// Handle UTF-8 multi-byte sequences
				case b >= 0xC0:
					// Start of UTF-8 sequence - read remaining bytes
					var utf8Bytes []byte
					utf8Bytes = append(utf8Bytes, b)

					// Determine how many more bytes to read
					var remaining int
					if b >= 0xF0 {
						remaining = 3
					} else if b >= 0xE0 {
						remaining = 2
					} else {
						remaining = 1
					}

					for i := 0; i < remaining; i++ {
						n, err := stdinReader.Read(buf)
						if err != nil || n == 0 {
							break
						}
						utf8Bytes = append(utf8Bytes, buf[0])
					}

					line = append(line, utf8Bytes...)
					fmt.Fprint(stdoutWriter, string(utf8Bytes))
				}
			}
		},
		NativeSend: func(v interface{}) error {
			return fmt.Errorf("cannot send to console_in")
		},
	}

	return consoleOutCh, consoleInCh
}

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

// demoScript is a built-in demo that shows basic GUI capabilities
const demoScript = `
# PawScript GUI Demo with Split Layout

gui_title "PawScript GUI Demo"
gui_resize 900, 500

# Enable split layout (40% left, 60% right)
gui_split 0.4

# === LEFT PANEL: GUI Controls ===
gui_label "=== GUI Controls ===", panel: "left"
gui_label "", panel: "left"

gui_label "Enter your name:", panel: "left"
gui_entry "Type here...", id: "nameEntry", panel: "left"

# Greeting handler - reads from entry widget, writes to label
macro greet_user (
    name: {gui_get nameEntry}
    gui_set welcome, "Hello, ~name;!"
)

gui_button "Greet Me", onclick: "greet_user", panel: "left"
gui_label "Welcome!", id: "welcome", panel: "left"

gui_label "", panel: "left"
gui_label "--- Counter ---", panel: "left"

# Counter demo - state stored in hidden widget
gui_entry "", id: "counterState", panel: "left"
gui_set counterState, "0"
gui_label "Counter: 0", id: "counterLabel", panel: "left"

macro increment_counter (
    current: {gui_get counterState}
    newval: {add ~current, 1}
    gui_set counterState, "~newval"
    gui_set counterLabel, "Counter: ~newval"
)

gui_button "Increment", onclick: "increment_counter", panel: "left"

gui_label "", panel: "left"
gui_label "GUI remains responsive", panel: "left"
gui_label "while console runs!", panel: "left"

# === RIGHT PANEL: Console Terminal ===
# Create console on right panel, unpack to #out/#in/#err for print/read
(#out, #in, #err): {gui_console 400, 400, panel: "right"}

msleep 1000

# Define console interaction macro
# Receives channels as $1, $2, $3 from fiber_spawn
macro console_loop (
    # Initialize IO channels from arguments
    #out: $1
    #in: $2
    #err: $3
    print "out:", ~#out
    print #out, "To console"

    # Clear and show welcome
    write "\x1b[2J\x1b[H"
    print "\x1b[36m=== PawScript Console ===\x1b[0m"
    print ""
    print "This console runs in a \x1b[33mfiber\x1b[0m,"
    print "so it doesn't block the GUI!"
    print ""
    print "\x1b[32mColors work:\x1b[0m \x1b[31mred\x1b[0m \x1b[33myellow\x1b[0m \x1b[34mblue\x1b[0m"
    print ""

    # Interactive loop
    count: 0
    while (true), (
        count: {add ~count, 1}
        print "\x1b[35m[\x1b[0m~count\x1b[35m]\x1b[0m Enter text (or 'quit'):"
        input: {read}

        {eq ~input, "quit"} & (
            print "\x1b[31mGoodbye!\x1b[0m"
            ret
        )

        print "You said: \x1b[36m~input\x1b[0m"
        print ""
    )
)

# Export macros for callbacks
MODULE exports
EXPORT greet_user, increment_counter, console_loop

# Run console interaction in a fiber, passing the channels as arguments
msleep 300
fiber_spawn console_loop, ~#out, ~#in, ~#err

`

// consoleDemo is a demo script that shows terminal/console capabilities
const consoleDemo = `
# PawScript Console Demo
# Shows terminal features using gui_console returned channels

gui_title "PawScript Console Demo"
gui_resize 700, 500

# Create console and unpack channels directly into #out, #in, #err
# This makes echo, print, write, read automatically use them!
(#out, #in, #err): {gui_console 680, 450}

# Give the terminal a moment to initialize
msleep 200

# Now we can use standard print and echo commands!
# Clear screen and show title
send ~#out, "\x1b[2J\x1b[H"
print "\x1b[36m=== PawScript Console Demo ===\x1b[0m"
echo ""
print "This terminal supports ANSI escape codes!"
echo ""

# Show some colors using raw ANSI codes
print "\x1b[31mThis is red text\x1b[0m"
print "\x1b[32mThis is green text\x1b[0m"
print "\x1b[33mThis is yellow text\x1b[0m"
print "\x1b[34mThis is blue text\x1b[0m"
echo ""

print "Type something and press Enter:"

# Read from console using the standard read command
input: {read}
print "You typed: ~input"
`
