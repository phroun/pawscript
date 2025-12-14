# PurfecTerm ANSI Extensions

This document describes the terminal escape sequences and extensions supported by PurfecTerm beyond standard ANSI/VT100.

## DEC Private Modes

Use `ESC [ ? <mode> h` to enable and `ESC [ ? <mode> l` to disable.

| Mode | Name | Description |
|------|------|-------------|
| 1 | DECCKM | Application cursor keys (not yet implemented) |
| 3 | DECCOLM | 132 Column Mode (horizontal scale 0.6060) |
| 5 | DECSCNM | Screen Mode: `h`=light theme, `l`=dark theme |
| 7 | DECAWM | Auto-wrap mode: `h`=wrap to next line, `l`=stay at last column |
| 12 | Cursor Blink | `h`=fast blink, `l`=slow blink |
| 25 | DECTCEM | Cursor visibility: `h`=show, `l`=hide |
| 1049 | Alt Screen | Alternate screen buffer (not yet implemented) |
| 2004 | Bracketed Paste | Bracketed paste mode |
| 2027 | Flex Width | Flexible East Asian Width mode |
| 2028 | Visual Wrap | Visual width-based line wrapping |
| 2029 | Narrow Ambiguous | Ambiguous width characters use narrow (1.0) width |
| 2030 | Wide Ambiguous | Ambiguous width characters use wide (2.0) width |
| 7700 | Scrollback Control | `h`=disable scrollback accumulation (for games), `l`=re-enable |
| 7701 | Auto-Scroll Control | `h`=disable cursor-following auto-scroll, `l`=re-enable |
| 7702 | Smart Word Wrap | `h`=wrap at word boundaries, `l`=standard mid-word wrap |

### Smart Word Wrap (Mode 7702)

When enabled, word wrap occurs at word boundary characters instead of mid-word. Boundary characters are:
- Space (` `)
- Hyphen (`-`)
- Comma (`,`)
- Semicolon (`;`)
- Emdash (`—`, U+2014)

When a wrap would occur, all cells after the last word boundary are moved to the beginning of the next line, preserving all cell attributes (colors, styles, etc.). If no word boundary is found on the line, standard wrap behavior is used.

## OSC Sequences

Format: `ESC ] <cmd> ; <args> BEL` (or `ESC ] <cmd> ; <args> ESC \`)

| OSC | Name | Description |
|-----|------|-------------|
| 7000 | Palette | Palette management for custom glyphs |
| 7001 | Glyph | Custom glyph definition |
| 7002 | Sprite | Sprite overlay management |
| 7003 | Screen Crop | Screen crop and split regions |

### OSC 7000: Palette Management

Commands are separated by semicolons after the OSC number.

| Command | Format | Description |
|---------|--------|-------------|
| Delete All | `da` | Delete all palettes |
| Delete | `d;N` | Delete palette N |
| Initialize | `i;N;LEN` | Initialize palette N with LEN entries |
| Set Color | `s;N;IDX;COL` | Set palette N, index IDX to color COL |
| Set Dim | `s;N;IDX;2;COL` | Set palette N, index IDX to dim color COL |

Example: `ESC ] 7000 ; i;1;16 BEL` - Initialize palette 1 with 16 entries.

### OSC 7001: Glyph Management

Define custom tile-based glyphs that can be displayed using Unicode Private Use Area codepoints.

### OSC 7002: Sprite Management

Manage sprite overlays that render on top of the terminal content at sub-cell precision.

### OSC 7003: Screen Crop and Splits

Control screen cropping and define split regions for multi-region rendering.

## SGR Extensions

Standard SGR (Select Graphic Rendition) via `ESC [ <params> m`:

| Param | Description |
|-------|-------------|
| 0 | Reset all attributes |
| 1 | Bold |
| 2 | Dim (treated as not bold) |
| 3 | Italic |
| 4 | Underline |
| 5 | Blink |
| 7 | Reverse video |
| 21 | Bold off |
| 22 | Normal intensity |
| 23 | Italic off |
| 24 | Underline off |
| 25 | Blink off |
| 27 | Reverse off |
| 30-37 | Standard foreground colors |
| 38;5;N | 256-color foreground |
| 38;2;R;G;B | 24-bit RGB foreground |
| 39 | Default foreground |
| 40-47 | Standard background colors |
| 48;5;N | 256-color background |
| 48;2;R;G;B | 24-bit RGB background |
| 49 | Default background |
| 90-97 | Bright foreground colors |
| 100-107 | Bright background colors |

### PurfecTerm SGR Extensions

| Param | Description |
|-------|-------------|
| 150 | Reset horizontal flip (XFlip off) |
| 151 | Enable horizontal flip (XFlip on) |
| 152 | Reset vertical flip (YFlip off) |
| 153 | Enable vertical flip (YFlip on) |
| 168;5;N | Set Base Glyph Palette (BGP) to palette N |
| 169 | Reset BGP to default (use foreground color) |

## Window Manipulation

Format: `ESC [ <cmd> ; <args> t`

| Command | Format | Description |
|---------|--------|-------------|
| Set Size | `8;ROWS;COLS` | Set terminal size to ROWS x COLS |
| 40-Column | `9;40;0/1` | Disable/enable 40-column mode (2x horizontal scale) |

## Cursor Style

Format: `ESC [ <style> SP q` (note: SP is a literal space character)

| Style | Description |
|-------|-------------|
| 0, 1 | Blinking block |
| 2 | Steady block |
| 3 | Blinking underline |
| 4 | Steady underline |
| 5 | Blinking bar |
| 6 | Steady bar |
