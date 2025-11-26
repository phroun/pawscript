// pawgui - PawScript with Fyne GUI support
// A proof of concept for running PawScript with GUI capabilities
package main

import (
	"fmt"
	"os"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
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
	content    *fyne.Container
}

var guiState *GuiState

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: pawgui <script.paw>")
		fmt.Println("       pawgui -demo")
		os.Exit(1)
	}

	// Create the Fyne application
	fyneApp := app.New()
	mainWindow := fyneApp.NewWindow("PawScript GUI")
	mainWindow.Resize(fyne.NewSize(400, 300))

	// Create PawScript instance
	ps := pawscript.New(nil)
	ps.RegisterStandardLibrary(nil)

	// Initialize GUI state
	guiState = &GuiState{
		app:        fyneApp,
		mainWindow: mainWindow,
		widgets:    make(map[string]fyne.CanvasObject),
		containers: make(map[string]*fyne.Container),
		ps:         ps,
		content:    container.NewVBox(),
	}

	// Register GUI commands
	registerGuiCommands(ps)

	// Set initial content
	mainWindow.SetContent(guiState.content)

	// Handle -demo flag for built-in demo
	scriptPath := os.Args[1]
	var script string

	if scriptPath == "-demo" {
		script = demoScript
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
	go func() {
		result := ps.Execute(script)
		if result == pawscript.BoolStatus(false) {
			fmt.Fprintf(os.Stderr, "Script execution failed\n")
		}
	}()

	// Run the Fyne event loop (blocking)
	mainWindow.ShowAndRun()
}

// registerGuiCommands registers all GUI-related commands with PawScript
func registerGuiCommands(ps *pawscript.PawScript) {
	// gui_title - Set window title
	ps.RegisterCommand("gui_title", func(ctx *pawscript.Context) pawscript.Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(pawscript.CatCommand, "Usage: gui_title <window_title>")
			return pawscript.BoolStatus(false)
		}
		title := fmt.Sprintf("%v", ctx.Args[0])
		guiState.mainWindow.SetTitle(title)
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
		guiState.mainWindow.Resize(fyne.NewSize(float32(width), float32(height)))
		return pawscript.BoolStatus(true)
	})

	// gui_label - Create a label widget
	ps.RegisterCommand("gui_label", func(ctx *pawscript.Context) pawscript.Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(pawscript.CatCommand, "Usage: gui_label <text> [id: <name>]")
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

		// Add to content
		guiState.mu.Lock()
		guiState.content.Add(lbl)
		guiState.mu.Unlock()
		guiState.content.Refresh()

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
					result := guiState.ps.Execute(onclickMacro)
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

		// Add to content
		guiState.mu.Lock()
		guiState.content.Add(btn)
		guiState.mu.Unlock()
		guiState.content.Refresh()

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

		// Add to content
		guiState.mu.Lock()
		guiState.content.Add(entry)
		guiState.mu.Unlock()
		guiState.content.Refresh()

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

		// Set value based on widget type
		switch widget := w.(type) {
		case *widget.Entry:
			widget.SetText(value)
		case *widget.Label:
			widget.SetText(value)
		}
		return pawscript.BoolStatus(true)
	})

	// gui_clear - Clear all widgets from content
	ps.RegisterCommand("gui_clear", func(ctx *pawscript.Context) pawscript.Result {
		guiState.mu.Lock()
		guiState.content.RemoveAll()
		guiState.widgets = make(map[string]fyne.CanvasObject)
		guiState.mu.Unlock()
		guiState.content.Refresh()
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

		// Show info dialog
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
		return pawscript.BoolStatus(true)
	})
}

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
# PawScript GUI Demo

gui_title "PawScript GUI Demo"
gui_resize 400, 350

gui_label "Welcome to PawScript GUI!", id: "welcome"
gui_label "Enter your name:", id: "prompt"
gui_entry "Type here...", id: "nameEntry"

# Greeting handler - reads from entry widget, writes to label
macro greet_user (
    name: {gui_get nameEntry}
    gui_set welcome, "Hello, ~name;!"
)

gui_button "Greet Me", id: "greetBtn", onclick: "greet_user"

# Counter demo - state stored in hidden widget
gui_entry "", id: "counterState"
gui_set counterState, "0"
gui_label "Counter: 0", id: "counterLabel"

# Increment reads current value from hidden entry, increments, updates both
macro increment_counter (
    current: {gui_get counterState}
    newval: {add ~current, 1}
    gui_set counterState, "~newval"
    gui_set counterLabel, "Counter: ~newval"
)

gui_button "Increment", onclick: "increment_counter"

gui_label ""
gui_label "This is a proof of concept for PawScript + Fyne"
`
