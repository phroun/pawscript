# Visible Screen, Logical Screen, Crop, and Scrollback

This document describes the relationship between the visible screen, logical screen, crop regions, scrollback buffer, and how they interact with screen splits in PurfecTerm.

## Core Concepts

### Physical Screen (Visible Area)

The **physical screen** represents what's actually visible in the terminal widget. Its dimensions are determined by:

- Widget pixel size
- Character cell dimensions (width and height)
- Screen scaling factors (horizontal/vertical scale from 132-column mode, 40-column mode, line density)

The physical dimensions are stored as `cols` and `rows` in the Buffer.

### Logical Screen

The **logical screen** is the terminal's internal view of its size, which may differ from the physical screen. This is set via the `ESC [ 8 ; rows ; cols t` escape sequence.

- If `logicalRows > rows`: Part of the logical screen is hidden above the visible area
- If `logicalCols > cols`: Part of the logical screen extends beyond the visible width (horizontal scrolling)

The logical dimensions default to 0, meaning "use physical dimensions."

```
Physical:  24 rows visible
Logical:   80 rows total
Hidden:    56 rows (80 - 24) above visible area at scrollOffset 0
```

### EffectiveRows and EffectiveCols

These helper methods return the logical dimensions if set, otherwise the physical dimensions:

```go
func (b *Buffer) EffectiveRows() int {
    if b.logicalRows > 0 {
        return b.logicalRows
    }
    return b.rows
}
```

### Scrollback Buffer

The **scrollback** is a FIFO buffer that stores lines that have scrolled off the top of the logical screen. When content scrolls up:

1. Top line of logical screen moves to scrollback
2. All other lines shift up
3. New empty line added at bottom

Scrollback has a configurable maximum size (`maxScrollback`). When full, the oldest line is discarded.

### Scroll Offset

The `scrollOffset` determines what content is currently displayed:

- `scrollOffset = 0`: Viewing the current logical screen (newest content)
- `scrollOffset > 0`: Viewing older content, scrolled into scrollback

Maximum scroll offset is calculated as:
```
maxScrollOffset = scrollbackSize + logicalHiddenAbove + magneticThreshold
```

Where `logicalHiddenAbove = max(0, effectiveRows - rows)`.

## Vertical Positioning

### Content Layers

From bottom to top of the content stack:

1. **Scrollback** - Oldest content at highest scroll offsets
2. **Logical Screen Hidden Above** - Logical rows not in physical view
3. **Visible Logical Screen** - What you see when scrollOffset = 0
4. **Cursor Position** - Where new content appears

### Calculating Visible Row Content

When rendering row `y` (0 to rows-1) of the visible screen:

```go
// effectiveScrollOffset accounts for magnetic zone
effectiveOffset := getEffectiveScrollOffset()

// logicalHiddenAbove is how much of logical screen is hidden
logicalHiddenAbove := max(0, effectiveRows - rows)

// Which logical/scrollback row to show
sourceRow := y + effectiveOffset - logicalHiddenAbove

if sourceRow < 0 {
    // Drawing from scrollback
    scrollbackIndex := len(scrollback) + sourceRow
} else {
    // Drawing from logical screen
    screenIndex := sourceRow
}
```

## Magnetic Scroll Zone

The **magnetic zone** creates a "sticky" behavior at the boundary between the logical screen and scrollback. This prevents accidental scrolling when the user is near the boundary.

### How It Works

- Zone size: 5% of total scrollable content, clamped to 2-50 lines
- When in the magnetic zone, `getEffectiveScrollOffset()` returns the offset that shows the full logical screen
- Past the magnetic zone, the threshold is subtracted for smooth transition

### Boundary Line

A yellow dashed line indicates the boundary between scrollback and logical screen:

```go
boundaryRow := scrollOffset - logicalHiddenAbove
effectiveBoundaryRow := boundaryRow - magneticThreshold
if effectiveBoundaryRow > 0 && effectiveBoundaryRow < rows {
    // Draw yellow line at effectiveBoundaryRow
}
```

## Screen Crop

The **screen crop** limits rendering to a rectangular region, specified in sprite coordinate units.

- `widthCrop`: X coordinate beyond which nothing renders (-1 = no crop)
- `heightCrop`: Y coordinate below which nothing renders (-1 = no crop)

Sprite units default to 8 subdivisions per character cell (`spriteUnitX`, `spriteUnitY`).

## Screen Splits

**Screen splits** allow different regions of the visible screen to show different parts of the logical buffer.

### Split Definition

```go
type ScreenSplit struct {
    ScreenY         int     // Y in sprite units (relative to logical screen start)
    BufferRow       int     // 0-indexed row in logical screen to draw from
    BufferCol       int     // 0-indexed column to draw from
    TopFineScroll   int     // Fine pixel offset (0 to unitY-1)
    LeftFineScroll  int     // Fine pixel offset (0 to unitX-1)
    CharWidthScale  float64 // Width multiplier (0 = inherit)
    LineDensity     int     // Line density (0 = inherit)
}
```

### Split Coordinate System

Split `ScreenY` values are **logical scanline numbers relative to the scroll boundary**:

- The first logical scanline (0) begins after the scrollback area
- Splits cannot occur in the scrollback area above the yellow dotted line
- When scrolled into scrollback, the logical screen starts at `boundaryRow`

### Rendering Order

1. Clear background
2. Render behind sprites (negative Z-order)
3. Render main screen cells (including cursor)
4. Render front sprites (non-negative Z-order)
5. Render screen splits (overlay specific regions)
6. Draw boundary line

### Interaction with Scrolling

Splits use `GetEffectiveScrollOffset()` for positioning, so they remain stable during the magnetic zone. The split's `ScreenY` is relative to where the logical screen starts, not the physical screen top.

## Cursor Tracking and Auto-Scroll

### Keyboard Activity Timer

When keyboard input occurs, a 500ms timer starts. During this window, if the cursor is moved off-screen, the terminal auto-scrolls to make it visible.

### Draw Tracking

Rather than calculating cursor visibility mathematically (which is complex with splits), the widget tracks whether the cursor was actually drawn:

1. `cursorWasDrawn` flag reset to `false` before rendering
2. Flag set to `true` when cursor is drawn in the render loop
3. After rendering, `CheckCursorAutoScroll()` scrolls by one row if:
   - Keyboard activity is recent (within 500ms)
   - Cursor was not drawn

### Scroll Direction

When the cursor wasn't drawn and auto-scroll is active, the buffer calculates where the cursor is relative to the visible area:

- If `cursorY < visibleStart`: Cursor is above visible area, scroll up (increase offset)
- If `cursorY >= visibleEnd`: Cursor is below visible area, scroll down (decrease offset)

This position-based approach is more reliable than tracking movement direction, which can be stale when output scrolls the screen without moving the cursor coordinate.

## Scrollbar Calculations

### Vertical Scrollbar

```go
maxOffset := scrollbackSize + logicalHiddenAbove + magneticThreshold
pageSize := rows
adjustedPosition := maxOffset - scrollOffset  // Inverted: 0=bottom, max=top
```

### Horizontal Scrollbar

```go
// Consider max of:
// - effectiveCols (logical screen width)
// - maxLineLength (from content)
// - splitContentWidth (from splits)
contentWidth := max(effectiveCols, maxLineLength, splitContentWidth)
pageSize := cols
position := horizOffset
```

## Summary Diagram

```
┌─────────────────────────────────────────────┐
│           SCROLLBACK BUFFER                 │  ← Oldest content
│  (scrollOffset positions into here)         │
├─────────────────────────────────────────────┤  ← Magnetic zone boundary
│  ═══════ MAGNETIC ZONE ═══════              │
├ ─ ─ ─ ─ ─ Yellow Line ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─┤  ← Visual boundary
│           LOGICAL HIDDEN ABOVE              │  ← logicalHiddenAbove rows
├─────────────────────────────────────────────┤
│  ┌─────────────────────────────────────┐    │
│  │     VISIBLE PHYSICAL SCREEN         │    │  ← rows visible
│  │  (may contain screen splits)        │    │
│  │                                     │    │
│  │     Cursor ▌                        │    │
│  └─────────────────────────────────────┘    │
│           (extends right with crop)    →    │  ← widthCrop
├─────────────────────────────────────────────┤
│           (extends down with crop)          │  ← heightCrop
└─────────────────────────────────────────────┘
```
