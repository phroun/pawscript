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

## Custom Glyph System

PurfecTerm supports a tile-based graphics system allowing Unicode characters to be replaced with custom pixel-art glyphs. This enables retro-style games and graphics while maintaining terminal text semantics.

### OSC 7000 - Palette Management

Operating System Command sequences for managing color palettes:

| Command | Format | Description |
|---------|--------|-------------|
| Delete All | `ESC ] 7000 ; da BEL` | Remove all custom palettes |
| Delete One | `ESC ] 7000 ; d;N BEL` | Delete palette number N |
| Initialize | `ESC ] 7000 ; i;N;LEN BEL` | Create palette N with LEN entries |
| Set Entry | `ESC ] 7000 ; s;N;IDX;COL BEL` | Set palette N, index IDX to color COL |
| Set Dim Entry | `ESC ] 7000 ; s;N;IDX;2;COL BEL` | Set palette N, index IDX to dim variant of color COL |

**Color codes (COL)** use SGR-style numbers:
- `8` - Transparent (use cell's background color)
- `9` - Default foreground (use cell's foreground color)
- `30-37`, `40-47` - Standard ANSI colors 0-7
- `90-97`, `100-107` - Bright ANSI colors 8-15

#### Example: Define a 4-color palette

```bash
# Create palette 256 with 4 entries
printf '\e]7000;i;256;4\a'

# Set entry 0 to transparent (background)
printf '\e]7000;s;256;0;8\a'

# Set entry 1 to red
printf '\e]7000;s;256;1;31\a'

# Set entry 2 to dim green
printf '\e]7000;s;256;2;2;32\a'

# Set entry 3 to bright blue
printf '\e]7000;s;256;3;94\a'
```

### OSC 7001 - Glyph Definition

Operating System Command sequences for defining custom glyphs:

| Command | Format | Description |
|---------|--------|-------------|
| Delete All | `ESC ] 7001 ; da BEL` | Remove all custom glyph definitions |
| Delete One | `ESC ] 7001 ; d;RUNE BEL` | Delete glyph for character code RUNE |
| Define | `ESC ] 7001 ; s;RUNE;W;P1;P2;... BEL` | Define glyph for RUNE with width W and pixels |

**Glyph format:**
- `RUNE` - Unicode code point (decimal)
- `W` - Width in pixels
- `P1;P2;...` - Palette indices for each pixel (row by row, left to right, top to bottom)
- Height is automatically calculated as `pixel_count / width`

#### Example: Define an 8x8 smiley face for character 'A' (65)

```bash
# Define 8x8 glyph for 'A' using palette indices 0 (bg) and 1 (fg)
printf '\e]7001;s;65;8;'
printf '0;0;1;1;1;1;0;0;'    # Row 1
printf '0;1;0;0;0;0;1;0;'    # Row 2
printf '1;0;1;0;0;1;0;1;'    # Row 3 (eyes)
printf '1;0;0;0;0;0;0;1;'    # Row 4
printf '1;0;1;0;0;1;0;1;'    # Row 5 (smile corners)
printf '1;0;0;1;1;0;0;1;'    # Row 6 (smile)
printf '0;1;0;0;0;0;1;0;'    # Row 7
printf '0;0;1;1;1;1;0;0'     # Row 8
printf '\a'
```

### SGR Codes for Glyph Attributes

| Code | Name | Description |
|------|------|-------------|
| 150 | Reset XFlip | Disable horizontal flip |
| 151 | XFlip | Enable horizontal flip (mirror) |
| 152 | Reset YFlip | Disable vertical flip |
| 153 | YFlip | Enable vertical flip (mirror) |
| 168;5;N | Set BGP | Set Base Glyph Palette to N |
| 169 | Reset BGP | Reset BGP to default (use foreground color code) |

#### Example: Use flipping for sprite mirroring

```bash
# Draw character facing right
printf '\e[151m'  # Enable XFlip
echo 'A'          # Draws mirrored glyph

# Draw character facing left (normal)
printf '\e[150m'  # Disable XFlip
echo 'A'          # Draws normal glyph
```

### Base Glyph Palette (BGP)

The BGP determines which palette is used to render custom glyphs. The palette index stored in each cell affects how glyph pixels are colored.

**Palette resolution:**
1. If BGP is explicitly set (`ESC[168;5;Nm`), use that palette
2. If BGP is default (-1), use the foreground color code as the palette number
   - e.g., `ESC[31m` (red) would look up palette 31

**Color resolution rules:**
1. **Palette doesn't exist**: 0=background, 1=foreground, 2=dim foreground, 3+=bright foreground
2. **Single-entry palette**: 0=background, 1+=palette entry
3. **Multi-entry palette**: Use entries directly, clamp out-of-range to highest entry

#### Example: Sprite with palette swap

```bash
# Define palette 31 (red scheme)
printf '\e]7000;i;31;3\a'
printf '\e]7000;s;31;0;8\a'   # transparent
printf '\e]7000;s;31;1;31\a'  # red
printf '\e]7000;s;31;2;91\a'  # bright red

# Define palette 34 (blue scheme)
printf '\e]7000;i;34;3\a'
printf '\e]7000;s;34;0;8\a'   # transparent
printf '\e]7000;s;34;1;34\a'  # blue
printf '\e]7000;s;34;2;94\a'  # bright blue

# Draw sprite in red (foreground color determines palette)
printf '\e[31m'  # Red foreground
echo 'A'         # Uses palette 31

# Same sprite in blue
printf '\e[34m'  # Blue foreground
echo 'A'         # Uses palette 34
```

### Glyph Rendering Details

- Glyphs are scaled to completely fill the cell (width and height)
- Adjacent glyphs touch with no gaps
- Double-width CJK cells: single glyph spans the full doubled cell
- XFlip/YFlip are applied during rendering, affecting the displayed pixels
- Modifying a palette updates all currently displayed glyphs using that palette

## See Also

- [escape-sequences.md](escape-sequences.md) - Standard escape sequences supported by PurfecTerm
