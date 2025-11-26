# PawGUI - PawScript with Fyne GUI

A proof of concept for running PawScript with GUI capabilities using [Fyne](https://fyne.io/).

## Prerequisites

Fyne requires native graphics libraries. On Linux:

```bash
# Debian/Ubuntu
sudo apt-get install libgl1-mesa-dev xorg-dev

# Fedora
sudo dnf install mesa-libGL-devel libXcursor-devel libXrandr-devel libXinerama-devel libXi-devel libXxf86vm-devel
```

On macOS, Xcode command line tools are required. On Windows, a C compiler (MinGW or TDM-GCC) is needed.

See https://docs.fyne.io/started/ for detailed setup instructions.

## Setup

Install the Fyne dependency:

```bash
cd src
go get fyne.io/fyne/v2@latest
go mod tidy
```

Then build:

```bash
go build -o pawgui ./cmd/pawgui/
```

## Usage

```bash
# Run with a script
./pawgui myscript.paw

# Run the built-in demo
./pawgui -demo
```

## GUI Commands

PawGUI provides these commands (prefixed with `gui_` to avoid conflicts):

### Window Control

- `gui_title <text>` - Set the window title
- `gui_resize <width>, <height>` - Resize the window
- `gui_clear` - Remove all widgets

### Widget Creation

- `gui_label <text> [id: <name>]` - Create a text label
- `gui_button <text> [id: <name>] [onclick: <macro>]` - Create a clickable button
- `gui_entry [placeholder] [id: <name>]` - Create a text input field

### Widget Interaction

- `gui_get <widget_id>` - Get the current value/text of a widget
- `gui_set <widget_id>, <value>` - Set the value/text of a widget

### Dialogs

- `gui_msgbox <message> [title: <title>]` - Show a message popup

## Example Script

```pawscript
# Set up the window
gui_title "My App"
gui_resize 400, 300

# Create widgets
gui_label "Enter your name:"
gui_entry "Type here...", id: "nameInput"

# Button handler - macros don't have outer scope access,
# so read/write state through widgets
macro say_hello (
    name: {gui_get nameInput}
    gui_msgbox "Hello, ~name;!", title: "Greeting"
)

gui_button "Say Hello", onclick: "say_hello"

# For stateful values like counters, store in a hidden entry widget
gui_entry "", id: "counterState"
gui_set counterState, "0"
gui_label "Count: 0", id: "countLabel"

macro increment (
    current: {gui_get counterState}
    newval: {add ~current, 1}
    gui_set counterState, "~newval"
    gui_set countLabel, "Count: ~newval"
)

gui_button "Increment", onclick: "increment"
```

**Note:** PawScript macros are isolated and don't have access to outer scope variables. For GUI callbacks that need persistent state, store values in entry widgets and read/write them with `gui_get`/`gui_set`.

## Architecture

- `main.go` - Application entry point, Fyne setup, and GUI command registration
- `GuiState` - Thread-safe state holding the Fyne app, window, and widget registry
- Scripts run in a goroutine while the Fyne event loop runs on the main thread
- Button callbacks execute PawScript macros asynchronously

## Extending

To add more widgets, follow the pattern in `registerGuiCommands()`:

1. Register a command with `ps.RegisterCommand("gui_xxx", ...)`
2. Create the Fyne widget
3. Optionally store it by ID in `guiState.widgets`
4. Add it to `guiState.content`
5. Call `Refresh()` to update the display

Future improvements could include:
- Layout containers (HBox, VBox, Grid)
- More widgets (checkbox, radio, select, slider)
- Event binding beyond onclick
- Multiple windows
- Menus and toolbars
- Canvas drawing
