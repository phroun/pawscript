# Path Selector Widget

The Path Selector is a custom dropdown widget used in the PawScript GUI launchers (pawgui-gtk and pawgui-qt) to display the current working directory with a dropdown menu for quick navigation.

## Features

- Displays current path with **start-elision** (showing the end of the path, which is most relevant)
- Shrinks to fit available panel width
- Dropdown menu contains:
  - Home directory
  - Examples directory
  - Recent paths (last 10 successfully launched)
  - "Clear Recent Paths" option
- Proper menu dividers between sections
- Keyboard accessible (included in tab order)

## GTK Implementation

**Widgets used:** `GtkMenuButton` containing a `GtkLabel`, with a `GtkMenu` popup

**Key code patterns:**

```go
// Create the button with embedded label
pathButton, _ = gtk.MenuButtonNew()
pathButton.SetSizeRequest(0, -1)  // Allow shrinking to any width

pathLabel, _ = gtk.LabelNew(currentDir)
pathLabel.SetEllipsize(pango.ELLIPSIZE_START)  // Elide at start
pathLabel.SetXAlign(0)      // Left-align text
pathLabel.SetHExpand(true)  // Fill button width
pathButton.Add(pathLabel)

// Create and attach menu
pathMenu, _ = gtk.MenuNew()
pathButton.SetPopup(pathMenu)
```

**Menu items with proper separators:**

```go
separator := gtk.SeparatorMenuItemNew()
pathMenu.Append(separator)
```

**Global variables:**
- `pathButton *gtk.MenuButton`
- `pathLabel *gtk.Label`
- `pathMenu *gtk.Menu`

## Qt Implementation

**Widgets used:** `QPushButton` with `QMenu`, using `QFontMetrics` for manual text elision

**Key code patterns:**

```go
// Create button with size policy that allows shrinking
pathButton = qt.NewQPushButton3("")
pathButton.SetSizePolicy(*qt.NewQSizePolicy2(qt.QSizePolicy__Ignored, qt.QSizePolicy__Fixed))
pathButton.SetStyleSheet("text-align: left; padding-left: 6px;")

// Attach menu (QPushButton shows dropdown arrow automatically)
pathMenu = qt.NewQMenu2()
pathButton.SetMenu(pathMenu)
```

**Manual text elision with timer:**

```go
// 250ms timer for responsive resize updates
uiUpdateTimer := qt.NewQTimer2(mainWindow.QObject)
uiUpdateTimer.OnTimeout(func() {
    updatePathButtonText()
})
uiUpdateTimer.Start(250)

func updatePathButtonText() {
    buttonWidth := pathButton.Width() - 40  // Room for dropdown arrow
    if buttonWidth < 50 {
        buttonWidth = 50
    }
    fm := qt.NewQFontMetrics(pathButton.Font())
    elidedText := fm.ElidedText(currentDir, qt.ElideLeft, buttonWidth)
    pathButton.SetText(elidedText)
}
```

**Menu separators:**

```go
pathMenu.AddSeparator()
```

**Global variables:**
- `pathButton *qt.QPushButton`
- `pathMenu *qt.QMenu`

## Pitfalls and Solutions

### GTK

| Problem | Solution |
|---------|----------|
| `GtkComboBoxText` doesn't shrink - expands to fit longest item | Use `GtkMenuButton` with `GtkLabel` instead |
| CSS `min-width: 0` doesn't constrain combo box | Not applicable - switch to MenuButton |
| Wrapping combo in `GtkScrolledWindow` causes crashes | Don't do this - use MenuButton approach |
| Default ellipsize shows start of path | Use `pango.ELLIPSIZE_START` to show end of path |

### Qt

| Problem | Solution |
|---------|----------|
| `QComboBox` doesn't shrink well | Use `QPushButton` with `QMenu` |
| `QToolButton` has ugly beveled appearance | Use `QPushButton` which matches other buttons |
| Text is center-aligned by default | Use stylesheet: `text-align: left; padding-left: 6px;` |
| No built-in start-elision for button text | Use `QFontMetrics.ElidedText()` with `qt.ElideLeft` |
| `panel.ResizeEvent` is undefined in Go bindings | Use a `QTimer` (250ms) to periodically update text |
| `QFontMetrics.ElidedText()` call fails | Takes 3 arguments `(text, mode, width)`, not 4 |
| `qt.NewQTimer2()` fails | Requires parent `*QObject` argument |
| Text gets cut off by dropdown arrow | Subtract 40 pixels from button width for padding |
| Manual "▼" arrow in text looks wrong | Don't add it - `QPushButton.SetMenu()` adds one automatically |

## Tab Order

The Path Selector should be first in the tab order for the left panel:

- **GTK:** Handled automatically by widget order in container
- **Qt:** Explicitly set with `qt.QWidget_SetTabOrder(pathButton.QWidget, fileList.QWidget)`

## Future Considerations

- The Qt 250ms UI update timer can be reused for other infrequent UI updates
- Recent paths are stored in the config file under `recent_paths`
- Maximum of 10 recent paths are kept
- Current path is included in the recent paths section (even though redundant with button text) for easier re-selection after browsing elsewhere
