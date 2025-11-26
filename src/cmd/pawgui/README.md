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

## GUI Module Commands

The `gui::` module provides these commands:

### Window Control

- `gui::title <text>` - Set the window title
- `gui::resize <width>, <height>` - Resize the window
- `gui::clear` - Remove all widgets

### Widget Creation

- `gui::label <text> [id: <name>]` - Create a text label
- `gui::button <text> [id: <name>] [onclick: <macro>]` - Create a clickable button
- `gui::entry [placeholder] [id: <name>]` - Create a text input field

### Widget Interaction

- `gui::get <widget_id>` - Get the current value/text of a widget
- `gui::set <widget_id>, <value>` - Set the value/text of a widget

### Dialogs

- `gui::msgbox <message> [title: <title>]` - Show a message popup

## Example Script

```pawscript
# Set up the window
gui::title "My App"
gui::resize 400, 300

# Create widgets
gui::label "Enter your name:"
gui::entry "Type here...", id: "nameInput"

# Define button handler
define say_hello (
    name: {gui::get nameInput}
    gui::msgbox "Hello, ~name;!", title: "Greeting"
)

gui::button "Say Hello", onclick: "say_hello"
```

## Architecture

- `main.go` - Application entry point, Fyne setup, and GUI command registration
- `GuiState` - Thread-safe state holding the Fyne app, window, and widget registry
- Scripts run in a goroutine while the Fyne event loop runs on the main thread
- Button callbacks execute PawScript macros asynchronously

## Extending

To add more widgets, follow the pattern in `registerGuiCommands()`:

1. Register a command in the `gui` module
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
