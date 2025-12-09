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
| 7700 | Disable Scrollback | When enabled, scrollback buffer accumulation is disabled (for games/performance)

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

## Disable Scrollback Mode (7700)

When mode 7700 is enabled (`ESC [ ? 7700 h`), the scrollback buffer is disabled:

- **Lines scrolling off the top are discarded** instead of being saved to scrollback
- **Existing scrollback is preserved** but inaccessible (hidden, not erased)
- **Scrollbar only shows logical screen** content (cannot scroll into scrollback history)
- **Scroll offset is reset to 0** when enabling this mode

This is primarily useful for games and performance-critical applications that:
- Don't need history of screen output
- Want to avoid memory allocation overhead from scrollback
- Don't want users accidentally scrolling up during gameplay

### Example Usage

```bash
# Disable scrollback (for game mode)
printf '\e[?7700h'

# Run game - all scroll operations will discard lines instead of saving
./my_game

# Re-enable scrollback when returning to normal terminal use
printf '\e[?7700l'
```

### Behavior Details

- When disabled, the yellow dashed line (scrollback boundary) still appears if the logical screen is larger than the physical screen
- The scrollbar will still function for navigating within the logical screen (if logical rows > physical rows)
- Re-enabling scrollback does NOT restore discarded lines; they are permanently lost
- Existing scrollback history becomes accessible again when re-enabled

## Cell Width Persistence

Important: The resolved width of each character is stored in the cell at write time. Changing the ambiguous width mode after text is written does NOT affect previously rendered characters. This ensures display consistency.

## Mouse Selection with Variable Width

When flexible width mode is enabled:
- Mouse selection accurately maps pixel positions to cells
- Individual cells can be selected regardless of their visual width
- Selection boundaries respect variable cell widths

## Compatibility Notes

- These private modes (2027-2030, 7700) are PurfecTerm-specific extensions
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

## Sprite Overlay System

PurfecTerm supports a sprite overlay system for positioning graphics anywhere on screen, independent of the text grid. Sprites are rendered using the same custom glyph system but can be freely positioned, scaled, and layered.

### OSC 7002 - Sprite Management

Operating System Command sequences for managing sprites:

| Command | Format | Description |
|---------|--------|-------------|
| Delete All | `ESC ] 7002 ; da BEL` | Remove all sprites |
| Delete One | `ESC ] 7002 ; d;ID BEL` | Delete sprite with ID |
| Set (runes) | `ESC ] 7002 ; s;ID;X;Y;Z;FGP;FLIP;XS;YS;CROP;R1;R2;... BEL` | Define sprite using rune codes |
| Set (text) | `ESC ] 7002 ; t;ID;X;Y;Z;FGP;FLIP;XS;YS;CROP;text BEL` | Define sprite using text string |
| Move | `ESC ] 7002 ; m;ID;X;Y BEL` | Move sprite position only |
| Move+Runes | `ESC ] 7002 ; mr;ID;X;Y;R1;R2;... BEL` | Move and update runes |
| Move+Text | `ESC ] 7002 ; mrt;ID;X;Y;text BEL` | Move and update text |
| Set Units | `ESC ] 7002 ; u;UX;UY BEL` | Set coordinate unit size |
| Delete All Crops | `ESC ] 7002 ; cda BEL` | Remove all crop rectangles |
| Delete Crop | `ESC ] 7002 ; cd;ID BEL` | Delete crop rectangle |
| Set Crop | `ESC ] 7002 ; cs;ID;MINX;MINY;MAXX;MAXY BEL` | Define crop rectangle |

**Sprite parameters:**
- `ID` - Unique numeric identifier for the sprite
- `X, Y` - Position in coordinate units (default: 8x8 pixels per unit)
- `Z` - Z-index for layering (negative = behind text, non-negative = in front)
- `FGP` - Foreground Glyph Palette (-1 = use default)
- `FLIP` - Flip mode: 0=none, 1=XFlip, 2=YFlip, 3=both
- `XS, YS` - Scale factors (1.0 = normal, 2.0 = double size)
- `CROP` - Crop rectangle ID (-1 = no cropping)
- `R1;R2;...` - Rune codes (10 = newline for multi-row sprites)
- `text` - String of characters (newlines create rows)

### Z-Index Layering

Sprites are sorted by Z-index, then by ID:
- **Negative Z-index**: Rendered behind the text layer (visible where cell backgrounds are default)
- **Non-negative Z-index**: Rendered on top of the text layer
- Within the same Z-index, lower IDs render first (underneath higher IDs)

### Coordinate Units

By default, sprite coordinates use 8x8 pixel units. This can be changed with the `u` command:

```bash
# Set coordinate units to 16x16 (larger tiles)
printf '\e]7002;u;16;16\a'

# Position sprite at (2,3) = pixel (32,48)
printf '\e]7002;s;1;2;3;0;-1;0;1;1;-1;65\a'
```

### Multi-Tile Sprites

Sprites can contain multiple tiles arranged in a grid:
- Tiles are placed side-by-side horizontally
- Newline (rune code 10) starts a new row
- Space characters are skipped (transparent tiles)

```bash
# Create a 2x2 sprite using characters A,B,C,D
# Layout:  AB
#          CD
printf '\e]7002;s;1;0;0;1;-1;0;1;1;-1;65;66;10;67;68\a'

# Or using text format:
printf '\e]7002;t;1;0;0;1;-1;0;1;1;-1;AB
CD\a'
```

### Sprite Flipping

The FLIP parameter affects the entire composed sprite:
- When XFlip is set, the sprite is mirrored horizontally
- When YFlip is set, the sprite is mirrored vertically
- For multi-tile sprites, both tile positions and individual glyph pixels are flipped

### Crop Rectangles

Crop rectangles clip sprites to a rectangular region, useful for scroll windows or masked effects:

```bash
# Define crop rectangle 0 covering units (2,2) to (10,8)
printf '\e]7002;cs;0;2;2;10;8\a'

# Create sprite that uses this crop rectangle
printf '\e]7002;s;1;0;0;1;-1;0;1;1;0;65;66\a'
#                                 ^-- CROP=0

# Delete crop rectangle
printf '\e]7002;cd;0\a'
```

### Optimized Updates

For animation, use move commands instead of recreating sprites:

```bash
# Create sprite once
printf '\e]7002;s;1;0;0;1;-1;0;1;1;-1;65\a'

# Animate by moving (much more efficient)
for i in {1..100}; do
  printf '\e]7002;m;1;%d;%d\a' $i $i
  sleep 0.016  # ~60fps
done
```

#### Example: Animated Character

```bash
# Define walking animation frames using characters 'A' (65) and 'B' (66)
# (Assume glyphs are already defined via OSC 7001)

# Create sprite at position (5, 10)
printf '\e]7002;s;100;5;10;1;31;0;1;1;-1;65\a'

# Animation loop - move and update frame
for x in {5..50}; do
  frame=$((65 + (x % 2)))  # Alternate between A and B
  printf '\e]7002;mr;100;%d;10;%d\a' $x $frame
  sleep 0.05
done
```

#### Example: Background Layer

```bash
# Create a background sprite at Z=-1 (behind text)
# This 2x2 tile background uses characters that have glyphs defined
printf '\e]7002;s;1;0;0;-1;32;0;1;1;-1;97;98;10;99;100\a'

# Text will appear on top with default backgrounds transparent
printf '\e[0m'  # Reset to default colors
echo "Hello, World!"
```

## Screen Crops and Splits

PurfecTerm supports screen cropping and splitting for advanced viewport control, useful for games with status bars, multi-panel interfaces, or parallax scrolling effects.

### OSC 7003 - Screen Crop and Split Management

Operating System Command sequences for managing screen crops and splits:

| Command | Format | Description |
|---------|--------|-------------|
| Clear Crop | `ESC ] 7003 ; c BEL` | Remove both crops |
| Set Width Crop | `ESC ] 7003 ; c;WIDTH BEL` | Set width crop only |
| Set Height Crop | `ESC ] 7003 ; c;;HEIGHT BEL` | Set height crop only |
| Set Both Crops | `ESC ] 7003 ; c;WIDTH;HEIGHT BEL` | Set both crop dimensions |
| Delete All Splits | `ESC ] 7003 ; sda BEL` | Remove all screen splits |
| Delete Split | `ESC ] 7003 ; sd;ID BEL` | Delete split with ID |
| Set Split | `ESC ] 7003 ; ss;ID;SCREENY;BUFROW;BUFCOL;TOPFINE;LEFTFINE;CWS;LD BEL` | Define screen split |

### Screen Crop

Screen crop limits the rendering area, specified in sprite coordinate units (default 8 units per cell):

```bash
# Set crop to show only first 80x25 cells (640x200 sprite units)
printf '\e]7003;c;640;200\a'

# Set width crop only (height unlimited)
printf '\e]7003;c;640\a'

# Set height crop only (width unlimited)
printf '\e]7003;c;;200\a'

# Clear crop (full screen)
printf '\e]7003;c\a'
```

**Crop parameters:**
- `WIDTH` - Maximum X coordinate in sprite units (-1 or omit = no crop)
- `HEIGHT` - Maximum Y coordinate in sprite units (-1 or omit = no crop)

Content and sprites beyond the crop boundary are clipped.

### Screen Splits

Screen splits allow different regions of the screen to show different parts of the buffer, each with optional fine-pixel scrolling and rendering overrides.

**Split parameters:**
- `ID` - Unique numeric identifier for the split
- `SCREENY` - Y coordinate in sprite units relative to the logical screen start (see note below)
- `BUFROW` - 1-indexed row in logical screen to start drawing from (0 = inherit)
- `BUFCOL` - 1-indexed column to start drawing from (0 = inherit)
- `TOPFINE` - Top fine scroll: 0 = no clipping, 1-7 = clip top of first row (with 8 subdivisions)
- `LEFTFINE` - Left fine scroll: 0 = no clipping, 1-7 = clip left of first column
- `CWS` - Character width scale multiplier (0 = inherit from main screen)
- `LD` - Line density override (0 = inherit from main screen)

**Important:** `SCREENY` is a **logical** scanline number relative to the scroll boundary (the yellow dotted line that separates scrollback from the active screen). The first logical scanline (0) begins immediately after the scrollback area. Splits cannot appear in the scrollback region above the yellow dotted line.

The main screen (starting at ScreenY=0) is implicit. Splits with ScreenY > 0 overlay portions of the main screen.

#### Example: Status Bar

```bash
# Create a status bar showing row 25 of the buffer at the top of the screen
# Split at ScreenY=0 (top), showing buffer row 25, column 1
printf '\e]7003;ss;1;0;25;1;0;0;0;0\a'

# Main content would start below (handled by another split or main screen)
```

#### Example: Parallax Scrolling

```bash
# Background layer (top portion) - scrolls slower
# Split 1: ScreenY=0, shows buffer row 1, fine scroll for parallax effect
printf '\e]7003;ss;1;0;1;1;2;0;0;0\a'

# Foreground layer - scrolls at normal speed
# Split 2: ScreenY=64 (row 8), shows buffer row 8
printf '\e]7003;ss;2;64;8;1;0;0;0;0\a'
```

### Fine Scrolling

Fine scroll values (TOPFINE, LEFTFINE) enable sub-cell scrolling, useful for smooth animation:
- Value 0: No clipping (full cell visible)
- Value 1-7: Clip 1/8 to 7/8 of the first row/column
- With 8 sprite subdivisions per cell: fine scroll of 4 = half cell clipped

```bash
# Smooth vertical scroll: clip top 3/8 of first row
printf '\e]7003;ss;1;0;5;1;3;0;0;0\a'
```

### Split Behavior Notes

- All splits share the same scrollbar/scroll position
- Each split can have its own column offset (BUFCOL)
- The main screen is implicit at ScreenY=0, BufferRow=0, BufferCol=0
- Splits are rendered in order by ScreenY coordinate
- Split regions are clipped to prevent overflow into other regions

## See Also

- [escape-sequences.md](escape-sequences.md) - Standard escape sequences supported by PurfecTerm
