# PurfecTerm Private ANSI Escape Codes

This document describes the private-use ANSI escape sequences implemented by PurfecTerm that extend beyond standard terminal emulator functionality.

## Private Mode Sequences

Private mode sequences use the format `ESC [ ? <code> h` (enable) and `ESC [ ? <code> l` (disable).

### Standard Private Modes Supported

| Code | Name | Description |
|------|------|-------------|
| 3 | DECCOLM | 132-column mode (horizontal scale 0.6060) |
| 12 | Cursor Blink | `h` = fast blink, `l` = slow blink |
| 25 | DECTCEM | Cursor visibility |
| 2004 | Bracketed Paste | Wrap pasted text with `ESC [200~` and `ESC [201~` |

### PurfecTerm Extended Private Modes

| Code | Name | Description |
|------|------|-------------|
| 2027 | Flexible East Asian Width Mode | When enabled, new characters get variable widths based on Unicode East_Asian_Width property |
| 2028 | Visual Width Wrap | When enabled, line wrapping is based on accumulated visual width rather than cell count |
| 2029 | Ambiguous Width: Narrow | When enabled, ambiguous East Asian Width characters are rendered as 1.0 width |
| 2030 | Ambiguous Width: Wide | When enabled, ambiguous East Asian Width characters are rendered as 2.0 width |

## Flexible East Asian Width Mode (2027)

When mode 2027 is enabled (`ESC [ ? 2027 h`):

- Each cell stores its visual width (0.5 to 2.0 cell units)
- Character widths are determined by the Unicode East_Asian_Width property:
  - **Fullwidth (F)** / **Wide (W)**: 2.0 cell units (CJK ideographs, Hiragana, Katakana, etc.)
  - **Halfwidth (H)**: 1.0 cell units (half compared to normal CJK = same as Latin)
  - **Narrow (Na)** / **Neutral (N)**: 1.0 cell units
  - **Ambiguous (A)**: Context-dependent (see below)

When disabled (`ESC [ ? 2027 l`), all characters use 1.0 width (traditional terminal behavior).

### Example Usage

```
# Enable flexible width mode
printf '\e[?2027h'

# Print CJK text (each character = 2.0 width)
echo '日本語'

# Print Latin text (each character = 1.0 width)
echo 'Hello'

# Disable flexible width mode
printf '\e[?2027l'
```

## Visual Width Wrap Mode (2028)

When mode 2028 is enabled along with mode 2027:

- Line wrapping occurs when the accumulated visual width exceeds the column limit
- Example: 40 wide (2.0) characters will wrap at column 80
- Example: 160 narrow (0.5) characters could fit in 80 columns before wrapping

When disabled, line wrapping is based on cell count (traditional behavior).

### Example Usage

```
# Enable both flexible width and visual width wrap
printf '\e[?2027h\e[?2028h'

# Now lines wrap based on visual width
# 40 CJK characters will fill an 80-column line
```

## Ambiguous Width Modes (2029, 2030)

Ambiguous East Asian Width characters (Greek, Cyrillic, mathematical symbols, box drawing, etc.) can be rendered with different widths depending on context.

### Auto Mode (Default)

When neither 2029 nor 2030 is enabled, ambiguous characters automatically match the width of the previous character:
- After Latin text: 1.0 width
- After CJK text: 2.0 width
- At start of line/buffer: 1.0 width (default)

### Narrow Mode (2029)

When mode 2029 is enabled (`ESC [ ? 2029 h`):
- All ambiguous characters are rendered as 1.0 width
- Suitable for Western/Latin-focused documents

### Wide Mode (2030)

When mode 2030 is enabled (`ESC [ ? 2030 h`):
- All ambiguous characters are rendered as 2.0 width
- Suitable for CJK-focused documents

### Returning to Auto Mode

Disable whichever mode is currently active:
- `ESC [ ? 2029 l` if Narrow was enabled
- `ESC [ ? 2030 l` if Wide was enabled

### Example Usage

```
# Use narrow ambiguous width (Western style)
printf '\e[?2029h'
echo 'α β γ δ'  # Greek letters as 1.0 width

# Use wide ambiguous width (CJK style)
printf '\e[?2029l\e[?2030h'
echo 'α β γ δ'  # Greek letters as 2.0 width

# Return to auto mode
printf '\e[?2030l'
echo 'α β γ δ'  # Greek letters match previous character width
```

## Cell Width Persistence

Important: The resolved width of each character is stored in the cell at write time. Changing the ambiguous width mode after text is written does NOT affect previously rendered characters. This ensures display consistency.

## Mouse Selection with Variable Width

When flexible width mode is enabled:
- Mouse selection accurately maps pixel positions to cells
- Individual cells can be selected regardless of their visual width
- Selection boundaries respect variable cell widths

## Compatibility Notes

- These private modes (2027-2030) are PurfecTerm-specific extensions
- Other terminal emulators will ignore these sequences
- Standard private mode numbers (1-2004) follow their conventional meanings
- The escape sequences use the DEC private mode format for compatibility

## See Also

- [escape-sequences.md](escape-sequences.md) - Standard escape sequences supported by PurfecTerm
