# Fyne Focus and Selection Delay Issue

## Problem

When implementing clickable list items in Fyne, using the `Tappable` interface (`Tapped` method) results in a noticeable delay (~500ms) before the tap event fires. This is because Fyne waits to determine if the user is performing a double-tap before triggering the single-tap event.

This delay affects:
- Item selection in lists
- Focus changes between widgets
- Any immediate visual feedback on click

## Solution

Use the `desktop.Mouseable` interface instead of (or in addition to) `Tappable`. The `MouseDown` method fires immediately on mouse press, without waiting for double-tap detection.

### Implementation

1. **Implement the `desktop.Mouseable` interface** on your custom widget:

```go
import "fyne.io/fyne/v2/driver/desktop"

type tappableLabel struct {
    widget.Label
    onTapped       func()
    onDoubleTapped func()
    onMouseDown    func() // Fires immediately on mouse press
}

var _ desktop.Mouseable = (*tappableLabel)(nil)

func (t *tappableLabel) MouseDown(_ *desktop.MouseEvent) {
    if t.onMouseDown != nil {
        t.onMouseDown()
    }
}

func (t *tappableLabel) MouseUp(_ *desktop.MouseEvent) {
    // Required by Mouseable interface but often not needed
}
```

2. **Move immediate actions to `onMouseDown`**:

```go
// Instead of this (delayed):
lbl.onTapped = func() {
    fileList.Select(i)
}

// Do this (immediate):
lbl.onMouseDown = func() {
    fileList.Select(i)
    runBtn.FocusGained()
}
```

3. **Call `FocusGained()` directly** for immediate visual focus feedback:

```go
// Instead of:
win.Canvas().Focus(runBtn)

// For immediate visual feedback, also call:
runBtn.FocusGained()
```

### Key Points

- `Tapped` waits for double-tap timeout (~500ms) before firing
- `MouseDown` fires immediately on mouse press
- `DoubleTapped` still works correctly alongside `MouseDown`
- For focus changes, calling `FocusGained()` directly provides instant visual feedback
- You can use both interfaces together - `MouseDown` for immediate response, `DoubleTapped` for double-click actions

### Example: List Item with Immediate Selection and Double-Click Action

```go
func(i widget.ListItemID, o fyne.CanvasObject) {
    lbl := o.(*tappableLabel)
    lbl.SetText(entries[i].Name)

    // Mouse down fires immediately - select and focus right away
    lbl.onMouseDown = func() {
        fileList.Select(i)
        runBtn.FocusGained()
    }

    // Double-tap still works for performing actions
    lbl.onDoubleTapped = func() {
        performAction()
    }
}
```

## Related Issues

This pattern was discovered while implementing the pawgui launcher file browser, where click-to-select needed to feel responsive while still supporting double-click to open/run files.
