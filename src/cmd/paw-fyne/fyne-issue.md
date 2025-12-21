# Slow focus response when clicking on terminal widget (~1 second delay)

## Description

When clicking on the terminal widget to focus it, there's approximately a 1 second delay before the cursor appears and keyboard input is accepted. This creates a poor user experience where the first several characters typed after clicking are lost.

## Steps to Reproduce

1. Create a terminal widget using `terminal.New()`
2. Connect it with `RunWithConnection()` or `RunLocalShell()`
3. Click elsewhere to unfocus the terminal
4. Click on the terminal to focus it
5. Immediately start typing

## Expected Behavior

The cursor should appear instantly and keyboard input should be accepted immediately.

## Actual Behavior

There's approximately a 1 second delay before the cursor appears. Characters typed during this delay are lost (they go to whatever widget previously had focus).

## Additional Observations

- Text selection (click and drag) works immediately - only focus/cursor is delayed
- Switching focus AWAY from the terminal to other widgets (like Entry) is instant
- The delay only occurs when focusing the terminal

## Workaround

We found that layering a transparent Tappable widget on top of the terminal and directly calling `FocusGained()` provides immediate focus:

```go
// clickInterceptor sits on top of terminal to handle focus immediately
type clickInterceptor struct {
    widget.BaseWidget
    terminal *terminal.Terminal
}

var _ fyne.Tappable = (*clickInterceptor)(nil)

func (c *clickInterceptor) Tapped(_ *fyne.PointEvent) {
    // Directly call FocusGained to show cursor immediately
    c.terminal.FocusGained()
    // Also request focus through Fyne's system for keyboard input
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

// Usage:
term := terminal.New()
interceptor := newClickInterceptor(term)
stacked := container.NewStack(term, interceptor)
```

This suggests the delay is somewhere in how the terminal's `Tapped()` method or Fyne's focus dispatch interacts with the widget, rather than in `FocusGained()` itself.

## Environment

- Fyne version: v2.7.1
- fyne-io/terminal version: v0.0.0-20251010081556-6f9c3819f75f (or latest)
- OS: macOS (also reproduced on Linux)
- Go version: 1.21+
