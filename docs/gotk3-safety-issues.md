# gotk3 Safety Issues and Mitigation Strategies

This document describes known issues with the gotk3 Go bindings for GTK3 and the strategies we use to prevent crashes related to GObject lifecycle management.

## The Fundamental Problem

gotk3 creates Go wrapper objects around GTK's C GObjects. These wrappers use Go's `runtime.SetFinalizer` to automatically call `g_object_unref` when Go's garbage collector determines the wrapper is no longer reachable.

The problem is that Go's GC and GTK's reference counting don't always agree about object lifetimes:

1. **GTK may free objects first**: When widgets are removed from containers, GTK unrefs them. If Go still has a wrapper with a finalizer, the finalizer will later try to unref an already-freed object.

2. **GC runs unpredictably**: Go's GC can run at any time, including during CGO calls. This means finalizers might execute while GTK is in the middle of signal handling or layout operations.

3. **Signal handlers create temporary wrappers**: Functions like `gdk.EventButtonNewFromEvent()` create new Go wrappers for C objects. These wrappers have finalizers that can run before the signal handler completes.

## Crash Symptoms

These issues typically manifest as:

- `SIGTRAP: trace trap` during `g_object_unref`
- `SIGSEGV: segmentation violation` when accessing GObject properties
- Crashes in `runtime.runFinalizers()` goroutine
- Crashes during `goMarshal` (signal callback handling)
- Stack traces showing `glib.(*Object).Unref` or `glib.Take.func1`

## Mitigation Strategies

### 1. Use `runtime.KeepAlive` in Signal Handlers

When signal handlers create temporary GTK wrapper objects, add `runtime.KeepAlive()` calls to prevent premature garbage collection:

```go
// BAD - handleWindow may be GC'd during signal handling
paned.Connect("button-press-event", func(p *gtk.Paned, ev *gdk.Event) bool {
    btnEvent := gdk.EventButtonNewFromEvent(ev)
    if handleWindow, err := p.GetHandleWindow(); err == nil {
        // Use handleWindow...
    }
    return false  // btnEvent and handleWindow may be finalized here or earlier
})

// GOOD - keep objects alive until handler completes
paned.Connect("button-press-event", func(p *gtk.Paned, ev *gdk.Event) bool {
    btnEvent := gdk.EventButtonNewFromEvent(ev)
    var handleWindow *gdk.Window

    var err error
    handleWindow, err = p.GetHandleWindow()
    if err == nil && handleWindow != nil {
        // Use handleWindow...
    }

    // Prevent GC until after this point
    runtime.KeepAlive(btnEvent)
    runtime.KeepAlive(handleWindow)
    return false
})

// ALTERNATIVE - use defer for cleaner code with multiple return paths
paned.Connect("button-release-event", func(p *gtk.Paned, ev *gdk.Event) bool {
    btnEvent := gdk.EventButtonNewFromEvent(ev)
    defer runtime.KeepAlive(btnEvent)

    if btnEvent.Button() != 1 {
        return false  // KeepAlive still runs due to defer
    }
    // ... more logic with early returns
    return true
})
```

### 2. Avoid Storing Widget References When Possible

Instead of storing GTK widget pointers that might become stale, use getter functions:

```go
// BAD - menu pointer may become invalid after rebuild
hamburgerBtn := createHamburgerButton(menu)

// GOOD - get fresh menu reference when needed
hamburgerBtn := createHamburgerButton(func() *gtk.Menu {
    return createHamburgerMenu(ctx)
})
```

### 3. Don't Iterate `app.GetWindows()` Unnecessarily

The `GetWindows()` method returns a list that may contain stale window references:

```go
// BAD - can crash if windows have been destroyed
windows := app.GetWindows()
for l := windows; l != nil; l = l.Next() {
    if win, ok := l.Data().(*gtk.ApplicationWindow); ok {
        // May crash here
    }
}

// GOOD - if you don't need to do anything, don't iterate
// (If you must iterate, consider tracking windows manually in Go)
```

### 4. Be Careful with Widget Removal

When removing widgets from containers, the widgets may be freed by GTK. Don't access them afterward:

```go
// Be careful - after Remove(), the widget may be invalid
container.Remove(widget)
// Don't use 'widget' after this point
```

### 5. Avoid Concurrent GTK Operations

GTK is not thread-safe. All GTK operations should happen on the main thread. Use `glib.IdleAdd` to schedule work from other goroutines:

```go
// BAD - calling GTK from a goroutine
go func() {
    label.SetText("Updated")  // May crash
}()

// GOOD - schedule on main thread
go func() {
    glib.IdleAdd(func() {
        label.SetText("Updated")
    })
}()
```

### 6. Use Mutex Guards for Complex Operations

For operations that involve multiple GTK calls that shouldn't be interrupted:

```go
var uiScaleMu sync.Mutex

func applyUIScale() {
    uiScaleMu.Lock()
    defer uiScaleMu.Unlock()

    // Multiple GTK operations here...
}
```

## Specific Problem Areas

### Paned (Splitter) Widgets

The splitter's signal handlers are particularly prone to GC issues during rapid dragging:

**button-press-event and button-release-event handlers** create temporary `EventButton` and `Window` objects. Always use `runtime.KeepAlive` for:
- `gdk.EventButtonNewFromEvent()` results
- `paned.GetHandleWindow()` results

**notify::position handlers** fire very rapidly during dragging. Always:
- Use `defer runtime.KeepAlive(paned)` at the start
- Add `runtime.KeepAlive` for any widgets being manipulated (Show/Hide calls)
- Consider deduplication to skip processing when position hasn't changed:

```go
var lastPos int = -1
paned.Connect("notify::position", func() {
    pos := paned.GetPosition()
    if pos == lastPos {
        return  // Skip if unchanged, reduces GC pressure
    }
    lastPos = pos

    defer func() {
        runtime.KeepAlive(paned)
        runtime.KeepAlive(widgetA)
        runtime.KeepAlive(widgetB)
    }()

    // ... show/hide logic
})
```

### Menu Rebuilding

When UI scale changes, menus need to be rebuilt with new icon sizes. Store menu getter functions rather than menu pointers to ensure you always get the current menu.

### Settings Dialog

Opening and closing settings dialogs can trigger multiple UI rebuilds. Guard against re-entrant calls and ensure proper cleanup.

## Testing for These Issues

These crashes are often intermittent and depend on GC timing. To increase likelihood of catching them:

1. Run with `GOGC=1` to make GC more aggressive
2. Rapidly perform the triggering action (e.g., drag splitter quickly)
3. Open/close dialogs repeatedly
4. Resize windows while performing other operations

## References

- gotk3 issue tracker: https://github.com/gotk3/gotk3/issues
- Go finalizer documentation: https://pkg.go.dev/runtime#SetFinalizer
- GTK reference counting: https://docs.gtk.org/gobject/concepts.html#reference-counting
