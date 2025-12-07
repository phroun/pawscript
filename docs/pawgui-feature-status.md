# pawgui-qt vs pawgui-gtk Feature Comparison

This document tracks feature parity between the Qt and GTK versions of pawgui.

## Missing Configuration Features

| Feature | GTK Status | Qt Status |
|---------|------------|-----------|
| `ui_scale` - UI scaling factor | Applied via CSS | Missing |
| `optimization_level` - script caching | Used (default 1) | Hardcoded to 0 |
| `terminal_background` - custom bg color | From config | Uses default |
| `terminal_foreground` - custom fg color | From config | Uses default |
| `palette_colors` - 16 ANSI colors | Configurable | Uses default |
| `default_blink` - blink mode | bounce/blink/bright | Uses default |
| `quit_shortcut` - Cmd+Q/Ctrl+Q/Alt+F4 | Configurable | Missing |
| Auto-populate config with defaults | Writes missing keys | Missing |

## Missing UI Features

| Feature | GTK | Qt |
|---------|-----|-----|
| Font fallback list | `"Menlo, JetBrains Mono, SF Mono, ..."` | Single font only |
| ".." parent directory entry | In file list | Uses "Up" button |
| Run button label changes | "Open" for dirs, "Run" for files | Always "Run" |
| Icons in file list | folder/file/go-up icons | Text only |
| Welcome message | Startup banner | Missing |
| Path label selectable | Can copy path | Not selectable |
| Window default size | 1100x700 | 900x700 (minimum) |
| Quit keyboard shortcut | Cmd+Q/Ctrl+Q | Missing |
| Alt+F4 handler | Explicit handler | Missing |
| File list scroll container | ScrolledWindow | May need fixing |

## Missing Terminal Features

| Feature | GTK | Qt |
|---------|-----|-----|
| Scrollbar widget | Visible scrollbar | Missing |
| Terminal capabilities | Sets TermCaps on channels | Missing |
| Case-insensitive `.paw` | `ToLower()` check | Exact match only |

## Browse Button Behavior

- **GTK**: Opens a file picker filtered to `.paw` files, then runs the selected file
- **Qt**: Opens a folder picker only

## Error Handling

- **GTK**: Prints directory read errors to terminal; destroys failed console windows
- **Qt**: Silent failures; doesn't clean up on console window errors

## Likely OK Differences

- GTK uses `glib.IdleAdd` for thread safety; Qt handles this differently through its event loop
- GTK has Windows-specific `init()` for GTK data paths (pixbuf, schemas) - may not be needed for Qt
