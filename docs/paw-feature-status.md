# paw-qt vs paw-gtk Feature Comparison

This document tracks feature parity between the Qt and GTK versions of paw.

## Configuration Features

| Feature | GTK Status | Qt Status |
|---------|------------|-----------|
| `ui_scale` - UI scaling factor | Applied via CSS | ✅ Config supported (not yet applied) |
| `optimization_level` - script caching | Used (default 1) | ✅ Implemented |
| `terminal_background` - custom bg color | From config | ✅ Implemented |
| `terminal_foreground` - custom fg color | From config | ✅ Implemented |
| `palette_colors` - 16 ANSI colors | Configurable | ✅ Implemented |
| `default_blink` - blink mode | bounce/blink/bright | ✅ Implemented |
| `quit_shortcut` - Cmd+Q/Ctrl+Q/Alt+F4 | Configurable | ✅ Implemented |
| Auto-populate config with defaults | Writes missing keys | ✅ Implemented |

## UI Features

| Feature | GTK | Qt |
|---------|-----|-----|
| Font fallback list | Multi-font fallback | ✅ Implemented |
| ".." parent directory entry | In file list | ✅ Implemented |
| Run button label changes | "Open" for dirs, "Run" for files | ✅ Implemented |
| Icons in file list | folder/file/go-up icons | ❌ Text only (Qt icons are complex) |
| Welcome message | Startup banner | ✅ Implemented |
| Path label selectable | Can copy path | ❌ Not selectable |
| Window default size | 1100x700 | ✅ Implemented |
| Quit keyboard shortcut | Cmd+Q/Ctrl+Q | ✅ Implemented |
| Alt+F4 handler | Explicit handler | ✅ Via quit shortcut config |

## Terminal Features

| Feature | GTK | Qt |
|---------|-----|-----|
| Right-click context menu | Copy/Paste/SelectAll/Clear | ✅ Implemented |
| Scrollbar widget | Visible scrollbar | ❌ Requires widget changes |
| Terminal capabilities | Sets TermCaps on channels | ✅ Implemented |
| Case-insensitive `.paw` | `ToLower()` check | ✅ Implemented |

## File Browser

| Feature | GTK | Qt |
|---------|-----|-----|
| Browse button | Opens file picker with .paw filter | ✅ Implemented |
| Directory read errors | Shown in terminal | ✅ Implemented |
| Console window cleanup | Destroys on failure | ✅ Implemented |

## Remaining Items

The following features are not yet implemented:

1. **Scrollbar** - Requires changes to purfecterm-qt widget
2. **File list icons** - Qt icon handling is more complex
3. **Selectable path label** - Minor UI enhancement
4. **UI scale application** - Config is read but not applied to Qt widgets

## Differences That Are OK

- GTK uses `glib.IdleAdd` for thread safety; Qt handles this through its event loop
- GTK has Windows-specific `init()` for GTK data paths - not needed for Qt
