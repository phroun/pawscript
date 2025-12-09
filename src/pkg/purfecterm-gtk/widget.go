package purfectermgtk

/*
#cgo pkg-config: gtk+-3.0 pangocairo
#include <stdlib.h>
#include <gtk/gtk.h>
#include <gdk/gdk.h>
#include <pango/pangocairo.h>

// Helper to get event coordinates
static void get_event_coords(GdkEvent *ev, double *x, double *y) {
    gdk_event_get_coords(ev, x, y);
}

// Check if a font family is available via Pango
static int font_family_exists(const char *family_name) {
    PangoFontMap *font_map = pango_cairo_font_map_get_default();
    if (!font_map) return 0;

    PangoFontFamily **families;
    int n_families;
    pango_font_map_list_families(font_map, &families, &n_families);

    int found = 0;
    for (int i = 0; i < n_families; i++) {
        const char *name = pango_font_family_get_name(families[i]);
        if (g_ascii_strcasecmp(name, family_name) == 0) {
            found = 1;
            break;
        }
    }
    g_free(families);
    return found;
}

// Check if a font has a glyph for a specific Unicode code point
// Returns 1 if the font has the glyph, 0 otherwise
static int font_has_glyph(const char *family_name, int font_size, gunichar codepoint) {
    PangoFontMap *font_map = pango_cairo_font_map_get_default();
    if (!font_map) return 0;

    PangoContext *context = pango_font_map_create_context(font_map);
    if (!context) return 0;

    PangoFontDescription *desc = pango_font_description_new();
    pango_font_description_set_family(desc, family_name);
    pango_font_description_set_size(desc, font_size * PANGO_SCALE);

    PangoFont *font = pango_font_map_load_font(font_map, context, desc);
    pango_font_description_free(desc);

    if (!font) {
        g_object_unref(context);
        return 0;
    }

    PangoCoverage *coverage = pango_font_get_coverage(font, pango_language_get_default());
    int has_glyph = (pango_coverage_get(coverage, codepoint) == PANGO_COVERAGE_EXACT);

    pango_coverage_unref(coverage);
    g_object_unref(font);
    g_object_unref(context);

    return has_glyph;
}

// Render text using Pango for proper Unicode combining character support
// This handles complex text shaping that Cairo's ShowText cannot do
static void pango_render_text(cairo_t *cr, const char *text, const char *font_family,
                              int font_size, int bold, double r, double g, double b) {
    PangoLayout *layout = pango_cairo_create_layout(cr);

    // Create font description
    PangoFontDescription *desc = pango_font_description_new();
    pango_font_description_set_family(desc, font_family);
    pango_font_description_set_size(desc, font_size * PANGO_SCALE);
    if (bold) {
        pango_font_description_set_weight(desc, PANGO_WEIGHT_BOLD);
    }

    pango_layout_set_font_description(layout, desc);
    pango_layout_set_text(layout, text, -1);

    // Set color and render
    cairo_set_source_rgb(cr, r, g, b);
    pango_cairo_show_layout(cr, layout);

    pango_font_description_free(desc);
    g_object_unref(layout);
}

// Get the pixel width of text rendered with Pango
static int pango_text_width(cairo_t *cr, const char *text, const char *font_family,
                            int font_size, int bold) {
    PangoLayout *layout = pango_cairo_create_layout(cr);

    PangoFontDescription *desc = pango_font_description_new();
    pango_font_description_set_family(desc, font_family);
    pango_font_description_set_size(desc, font_size * PANGO_SCALE);
    if (bold) {
        pango_font_description_set_weight(desc, PANGO_WEIGHT_BOLD);
    }

    pango_layout_set_font_description(layout, desc);
    pango_layout_set_text(layout, text, -1);

    int width, height;
    pango_layout_get_pixel_size(layout, &width, &height);

    pango_font_description_free(desc);
    g_object_unref(layout);

    return width;
}

// Get font metrics for proper baseline positioning (creates its own temp surface)
// Returns: ascent in out_ascent, descent in out_descent, height in out_height
static void pango_get_font_metrics_standalone(const char *font_family, int font_size,
                                              int *out_ascent, int *out_descent, int *out_height) {
    // Create a temporary image surface just to get a cairo context for Pango
    cairo_surface_t *surface = cairo_image_surface_create(CAIRO_FORMAT_ARGB32, 1, 1);
    cairo_t *cr = cairo_create(surface);

    PangoLayout *layout = pango_cairo_create_layout(cr);

    PangoFontDescription *desc = pango_font_description_new();
    pango_font_description_set_family(desc, font_family);
    pango_font_description_set_size(desc, font_size * PANGO_SCALE);

    pango_layout_set_font_description(layout, desc);
    pango_layout_set_text(layout, "M", -1); // Use M for metrics

    PangoContext *context = pango_layout_get_context(layout);
    PangoFontMetrics *metrics = pango_context_get_metrics(context, desc, NULL);

    *out_ascent = pango_font_metrics_get_ascent(metrics) / PANGO_SCALE;
    *out_descent = pango_font_metrics_get_descent(metrics) / PANGO_SCALE;
    *out_height = (*out_ascent) + (*out_descent);

    pango_font_metrics_unref(metrics);
    pango_font_description_free(desc);
    g_object_unref(layout);

    // Clean up temporary surface
    cairo_destroy(cr);
    cairo_surface_destroy(surface);
}

// Get text width standalone (creates its own temp surface)
static int pango_text_width_standalone(const char *text, const char *font_family,
                                       int font_size, int bold) {
    cairo_surface_t *surface = cairo_image_surface_create(CAIRO_FORMAT_ARGB32, 1, 1);
    cairo_t *cr = cairo_create(surface);

    PangoLayout *layout = pango_cairo_create_layout(cr);

    PangoFontDescription *desc = pango_font_description_new();
    pango_font_description_set_family(desc, font_family);
    pango_font_description_set_size(desc, font_size * PANGO_SCALE);
    if (bold) {
        pango_font_description_set_weight(desc, PANGO_WEIGHT_BOLD);
    }

    pango_layout_set_font_description(layout, desc);
    pango_layout_set_text(layout, text, -1);

    int width, height;
    pango_layout_get_pixel_size(layout, &width, &height);

    pango_font_description_free(desc);
    g_object_unref(layout);

    cairo_destroy(cr);
    cairo_surface_destroy(surface);

    return width;
}
*/
import "C"

import (
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/gotk3/gotk3/cairo"
	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/phroun/pawscript/pkg/purfecterm"
)

// Left padding for terminal content (pixels)
const terminalLeftPadding = 8

// Widget is a GTK terminal emulator widget
type Widget struct {
	mu sync.Mutex

	// GTK widgets
	drawingArea      *gtk.DrawingArea
	scrollbar        *gtk.Scrollbar   // Vertical scrollbar
	horizScrollbar   *gtk.Scrollbar   // Horizontal scrollbar
	box              *gtk.Box         // Outer vertical box
	innerBox         *gtk.Box         // Inner horizontal box (drawingArea + vscrollbar)

	// Terminal state
	buffer *purfecterm.Buffer
	parser *purfecterm.Parser

	// Font settings
	fontFamily        string
	fontFamilyUnicode string // Fallback for Unicode characters missing from main font
	fontFamilyCJK     string // Fallback for CJK characters
	fontSize          int
	charWidth         int
	charHeight        int
	charAscent        int

	// Color scheme
	scheme purfecterm.ColorScheme

	// Selection state
	selecting      bool
	selectStartX   int
	selectStartY   int
	mouseDown      bool
	mouseDownX     int
	mouseDownY     int
	selectionMoved bool // True if mouse moved since button press

	// Cursor blink
	cursorBlinkOn  bool
	blinkTimerID   glib.SourceHandle
	blinkTickCount int // Counter for variable blink rates

	// Text blink animation (bobbing wave)
	blinkPhase float64 // Animation phase in radians (0 to 2*PI)

	// Focus state
	hasFocus bool

	// Callback when data should be written to PTY
	onInput func([]byte)

	// Clipboard
	clipboard *gtk.Clipboard
}

// NewWidget creates a new terminal widget with the specified dimensions
func NewWidget(cols, rows, scrollbackSize int) (*Widget, error) {
	w := &Widget{
		fontFamily:    "Menlo",
		fontSize:      14,
		charWidth:     10, // Will be calculated properly
		charHeight:    20,
		charAscent:    16,
		scheme:        purfecterm.DefaultColorScheme(),
		cursorBlinkOn: true,
	}

	// Create buffer and parser
	w.buffer = purfecterm.NewBuffer(cols, rows, scrollbackSize)
	w.parser = purfecterm.NewParser(w.buffer)

	// Set up dirty callback to trigger redraws
	w.buffer.SetDirtyCallback(func() {
		glib.IdleAdd(func() {
			if w.drawingArea != nil {
				w.drawingArea.QueueDraw()
			}
		})
	})

	// Create GTK widgets
	var err error

	// Outer container (vertical: content area + horizontal scrollbar)
	w.box, err = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	if err != nil {
		return nil, err
	}

	// Inner container (horizontal: drawing area + vertical scrollbar)
	w.innerBox, err = gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	if err != nil {
		return nil, err
	}

	// Drawing area for terminal content
	w.drawingArea, err = gtk.DrawingAreaNew()
	if err != nil {
		return nil, err
	}

	// Enable events
	w.drawingArea.AddEvents(int(gdk.BUTTON_PRESS_MASK | gdk.BUTTON_RELEASE_MASK |
		gdk.POINTER_MOTION_MASK | gdk.SCROLL_MASK | gdk.KEY_PRESS_MASK))
	w.drawingArea.SetCanFocus(true)

	// Connect signals
	w.drawingArea.Connect("draw", w.onDraw)
	w.drawingArea.Connect("button-press-event", w.onButtonPress)
	w.drawingArea.Connect("button-release-event", w.onButtonRelease)
	w.drawingArea.Connect("motion-notify-event", w.onMotionNotify)
	w.drawingArea.Connect("scroll-event", w.onScroll)
	w.drawingArea.Connect("key-press-event", w.onKeyPress)
	w.drawingArea.Connect("configure-event", w.onConfigure)
	w.drawingArea.Connect("focus-in-event", w.onFocusIn)
	w.drawingArea.Connect("focus-out-event", w.onFocusOut)

	// Create vertical scrollbar
	adjustment, _ := gtk.AdjustmentNew(0, 0, 100, 1, 10, 10)
	w.scrollbar, err = gtk.ScrollbarNew(gtk.ORIENTATION_VERTICAL, adjustment)
	if err != nil {
		return nil, err
	}
	w.scrollbar.Connect("value-changed", w.onScrollbarChanged)

	// Create horizontal scrollbar
	hAdjustment, _ := gtk.AdjustmentNew(0, 0, 100, 1, 10, 10)
	w.horizScrollbar, err = gtk.ScrollbarNew(gtk.ORIENTATION_HORIZONTAL, hAdjustment)
	if err != nil {
		return nil, err
	}
	w.horizScrollbar.Connect("value-changed", w.onHorizScrollbarChanged)
	w.horizScrollbar.SetNoShowAll(true) // Don't show when parent shows all

	// Apply macOS-style scrollbar CSS using a unique style class
	w.scrollbar.SetName("purfecterm-scrollbar")
	w.horizScrollbar.SetName("purfecterm-hscrollbar")
	w.applyScrollbarCSS()

	// Pack widgets: inner box holds drawing area and vertical scrollbar
	w.innerBox.PackStart(w.drawingArea, true, true, 0)
	w.innerBox.PackStart(w.scrollbar, false, false, 0)

	// Outer box holds inner box and horizontal scrollbar
	w.box.PackStart(w.innerBox, true, true, 0)
	w.box.PackStart(w.horizScrollbar, false, false, 0)

	// Get clipboard
	w.clipboard, _ = gtk.ClipboardGet(gdk.SELECTION_CLIPBOARD)

	// Set minimum size (small fixed value to allow flexible resizing)
	w.updateFontMetrics()
	w.drawingArea.SetSizeRequest(100, 50)

	// Start animation timer (50ms interval for smooth bobbing wave animation)
	// Also handles cursor blink timing
	w.blinkTimerID = glib.TimeoutAdd(50, func() bool {
		// Update text blink animation phase (complete wave cycle in ~1.5 seconds)
		w.blinkPhase += 0.21         // ~1.5 second cycle
		if w.blinkPhase > 6.283185 { // 2*PI
			w.blinkPhase -= 6.283185
		}

		// Handle cursor blink timing (roughly every 250ms = 5 ticks)
		w.blinkTickCount++
		_, cursorBlink := w.buffer.GetCursorStyle()
		if cursorBlink > 0 && w.hasFocus {
			// Fast blink (2) toggles every 5 ticks (~250ms), slow blink (1) every 10 ticks (~500ms)
			ticksNeeded := 10
			if cursorBlink >= 2 {
				ticksNeeded = 5
			}
			if w.blinkTickCount >= ticksNeeded {
				w.blinkTickCount = 0
				w.cursorBlinkOn = !w.cursorBlinkOn
			}
		} else {
			// Keep cursor visible when not blinking or unfocused
			if !w.cursorBlinkOn {
				w.cursorBlinkOn = true
			}
		}

		w.drawingArea.QueueDraw()
		return true // Keep timer running
	})

	return w, nil
}

// Box returns the container widget
func (w *Widget) Box() *gtk.Box {
	return w.box
}

// DrawingArea returns the drawing area widget
func (w *Widget) DrawingArea() *gtk.DrawingArea {
	return w.drawingArea
}

// SetFont sets the terminal font
// family can be a comma-separated list of fonts; the first available one is used
func (w *Widget) SetFont(family string, size int) {
	// Resolve the first available font from the fallback list
	resolvedFont := resolveFirstAvailableFont(family)

	w.mu.Lock()
	w.fontFamily = resolvedFont
	w.fontSize = size
	w.mu.Unlock()
	w.updateFontMetrics()
	w.drawingArea.QueueDraw()
}

// SetFontFallbacks sets the Unicode and CJK fallback fonts
// unicodeFont is used for characters missing from the main font (Hebrew, Greek, Cyrillic, etc.)
// cjkFont is used specifically for CJK (Chinese/Japanese/Korean) characters
func (w *Widget) SetFontFallbacks(unicodeFont, cjkFont string) {
	resolvedUnicode := resolveFirstAvailableFont(unicodeFont)
	resolvedCJK := resolveFirstAvailableFont(cjkFont)

	w.mu.Lock()
	w.fontFamilyUnicode = resolvedUnicode
	w.fontFamilyCJK = resolvedCJK
	w.mu.Unlock()
}

// isCJKCharacter returns true if the rune is a CJK character
// This includes CJK Unified Ideographs, Hiragana, Katakana, Hangul, and related ranges
func isCJKCharacter(r rune) bool {
	// CJK Unified Ideographs
	if r >= 0x4E00 && r <= 0x9FFF {
		return true
	}
	// CJK Unified Ideographs Extension A
	if r >= 0x3400 && r <= 0x4DBF {
		return true
	}
	// CJK Unified Ideographs Extension B-F
	if r >= 0x20000 && r <= 0x2CEAF {
		return true
	}
	// CJK Compatibility Ideographs
	if r >= 0xF900 && r <= 0xFAFF {
		return true
	}
	// Hiragana
	if r >= 0x3040 && r <= 0x309F {
		return true
	}
	// Katakana
	if r >= 0x30A0 && r <= 0x30FF {
		return true
	}
	// Hangul Syllables
	if r >= 0xAC00 && r <= 0xD7AF {
		return true
	}
	// Hangul Jamo
	if r >= 0x1100 && r <= 0x11FF {
		return true
	}
	// Bopomofo
	if r >= 0x3100 && r <= 0x312F {
		return true
	}
	return false
}

// getFontForCharacter returns the appropriate font family for a character
// It checks if the main font has the glyph, and falls back to Unicode or CJK fonts if needed
func (w *Widget) getFontForCharacter(r rune, mainFont string, fontSize int) string {
	// ASCII characters always use the main font
	if r < 128 {
		return mainFont
	}

	// Check if main font has this glyph
	cFont := C.CString(mainFont)
	hasGlyph := C.font_has_glyph(cFont, C.int(fontSize), C.gunichar(r))
	C.free(unsafe.Pointer(cFont))

	if hasGlyph != 0 {
		return mainFont
	}

	// Main font doesn't have the glyph - use fallback
	w.mu.Lock()
	unicodeFont := w.fontFamilyUnicode
	cjkFont := w.fontFamilyCJK
	w.mu.Unlock()

	// Use CJK font for CJK characters
	if isCJKCharacter(r) && cjkFont != "" {
		return cjkFont
	}

	// Use Unicode font for other characters
	if unicodeFont != "" {
		return unicodeFont
	}

	// Final fallback to main font
	return mainFont
}

// resolveFirstAvailableFont parses a comma-separated font list and returns the first available font.
// Uses Pango/Cairo font map to check font availability. Falls back to "Monospace" if none found.
func resolveFirstAvailableFont(familyList string) string {
	// Parse the comma-separated list and find the first available font
	parts := strings.Split(familyList, ",")
	for _, part := range parts {
		fontName := strings.TrimSpace(part)
		if fontName == "" {
			continue
		}
		// Check if this font is available via Pango
		cName := C.CString(fontName)
		exists := C.font_family_exists(cName)
		C.free(unsafe.Pointer(cName))
		if exists != 0 {
			return fontName
		}
	}

	// None found, return "Monospace" as ultimate fallback
	return "Monospace"
}

// pangoRenderText renders text using Pango for proper combining character support.
// This replaces Cairo's ShowText which doesn't handle complex text shaping.
func pangoRenderText(cr *cairo.Context, text, fontFamily string, fontSize int, bold bool, r, g, b float64) {
	cText := C.CString(text)
	cFont := C.CString(fontFamily)
	defer C.free(unsafe.Pointer(cText))
	defer C.free(unsafe.Pointer(cFont))

	boldInt := 0
	if bold {
		boldInt = 1
	}

	// Get native cairo context pointer
	crNative := (*C.cairo_t)(unsafe.Pointer(cr.Native()))
	C.pango_render_text(crNative, cText, cFont, C.int(fontSize), C.int(boldInt), C.double(r), C.double(g), C.double(b))
}

// pangoTextWidth returns the pixel width of text rendered with Pango.
func pangoTextWidth(cr *cairo.Context, text, fontFamily string, fontSize int, bold bool) int {
	cText := C.CString(text)
	cFont := C.CString(fontFamily)
	defer C.free(unsafe.Pointer(cText))
	defer C.free(unsafe.Pointer(cFont))

	boldInt := 0
	if bold {
		boldInt = 1
	}

	crNative := (*C.cairo_t)(unsafe.Pointer(cr.Native()))
	return int(C.pango_text_width(crNative, cText, cFont, C.int(fontSize), C.int(boldInt)))
}

// pangoFontMetrics returns the ascent, descent, and total height for a font.
// This standalone version creates its own temporary cairo surface.
func pangoFontMetrics(fontFamily string, fontSize int) (ascent, descent, height int) {
	cFont := C.CString(fontFamily)
	defer C.free(unsafe.Pointer(cFont))

	var cAscent, cDescent, cHeight C.int
	C.pango_get_font_metrics_standalone(cFont, C.int(fontSize), &cAscent, &cDescent, &cHeight)

	return int(cAscent), int(cDescent), int(cHeight)
}

// pangoTextWidthStandalone returns the pixel width of text using a temporary surface.
// Use this when no cairo context is available.
func pangoTextWidthStandalone(text, fontFamily string, fontSize int, bold bool) int {
	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))

	cFont := C.CString(fontFamily)
	defer C.free(unsafe.Pointer(cFont))

	boldInt := 0
	if bold {
		boldInt = 1
	}

	return int(C.pango_text_width_standalone(cText, cFont, C.int(fontSize), C.int(boldInt)))
}

// SetColorScheme sets the color scheme
func (w *Widget) SetColorScheme(scheme purfecterm.ColorScheme) {
	w.mu.Lock()
	w.scheme = scheme
	w.mu.Unlock()
	w.applyScrollbarCSS() // Update scrollbar background to match
	w.drawingArea.QueueDraw()
}

// applyScrollbarCSS applies macOS-style CSS to the scrollbar with the current scheme's background
func (w *Widget) applyScrollbarCSS() {
	w.mu.Lock()
	bg := w.scheme.Background
	w.mu.Unlock()

	cssProvider, err := gtk.CssProviderNew()
	if err != nil {
		return
	}

	css := fmt.Sprintf(`
		#purfecterm-scrollbar, #purfecterm-hscrollbar {
			background-color: rgb(%d, %d, %d);
		}
		#purfecterm-scrollbar slider, #purfecterm-hscrollbar slider {
			min-width: 8px;
			min-height: 30px;
			border-radius: 4px;
			background-color: rgba(128, 128, 128, 0.5);
		}
		#purfecterm-scrollbar slider:hover, #purfecterm-hscrollbar slider:hover {
			background-color: rgba(128, 128, 128, 0.7);
		}
		#purfecterm-scrollbar slider:active, #purfecterm-hscrollbar slider:active {
			background-color: rgba(100, 100, 100, 0.8);
		}
		#purfecterm-scrollbar button, #purfecterm-hscrollbar button {
			min-width: 0;
			min-height: 0;
			padding: 0;
		}
		#purfecterm-hscrollbar slider {
			min-width: 30px;
			min-height: 8px;
		}
	`, bg.R, bg.G, bg.B)

	cssProvider.LoadFromData(css)
	screen, err := gdk.ScreenGetDefault()
	if err == nil {
		gtk.AddProviderForScreen(screen, cssProvider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
	}
}

// SetInputCallback sets the callback for handling input
func (w *Widget) SetInputCallback(fn func([]byte)) {
	w.mu.Lock()
	w.onInput = fn
	w.mu.Unlock()
}

// Feed writes data to the terminal (for local echo or PTY output)
func (w *Widget) Feed(data []byte) {
	w.parser.Parse(data)
}

// FeedString writes a string to the terminal
func (w *Widget) FeedString(data string) {
	w.parser.ParseString(data)
}

// Clear clears the terminal screen
func (w *Widget) Clear() {
	w.buffer.ClearScreen()
	w.buffer.SetCursor(0, 0)
}

// Buffer returns the underlying buffer
func (w *Widget) Buffer() *purfecterm.Buffer {
	return w.buffer
}

func (w *Widget) updateFontMetrics() {
	// Get actual font metrics from Pango (uses standalone C functions)
	ascent, descent, height := pangoFontMetrics(w.fontFamily, w.fontSize)

	// Get character width by measuring a typical character
	charWidth := pangoTextWidthStandalone("M", w.fontFamily, w.fontSize, false)

	w.charWidth = charWidth
	w.charHeight = height
	w.charAscent = ascent

	// Ensure minimum values
	if w.charWidth < 1 {
		w.charWidth = w.fontSize * 6 / 10
		if w.charWidth < 1 {
			w.charWidth = 10
		}
	}
	if w.charHeight < 1 {
		w.charHeight = w.fontSize * 12 / 10
		if w.charHeight < 1 {
			w.charHeight = 20
		}
	}

	_ = descent // descent is included in height
}

func (w *Widget) onDraw(da *gtk.DrawingArea, cr *cairo.Context) bool {
	w.mu.Lock()
	scheme := w.scheme
	fontFamily := w.fontFamily
	fontSize := w.fontSize
	baseCharWidth := w.charWidth
	baseCharHeight := w.charHeight
	blinkPhase := w.blinkPhase
	w.mu.Unlock()

	cols, rows := w.buffer.GetSize()
	cursorX, cursorY := w.buffer.GetCursor()
	cursorVisible := w.buffer.IsCursorVisible()
	cursorShape, _ := w.buffer.GetCursorStyle() // 0=block, 1=underline, 2=bar
	scrollOffset := w.buffer.GetScrollOffset()

	// Get screen scaling factors
	horizScale := w.buffer.GetHorizontalScale()
	vertScale := w.buffer.GetVerticalScale()

	// Apply scaling to character dimensions
	charWidth := int(float64(baseCharWidth) * horizScale)
	charHeight := int(float64(baseCharHeight) * vertScale)

	// Hide cursor when scrolled back
	if scrollOffset > 0 {
		cursorVisible = false
	}

	// Draw background - fill entire widget area (not just cell area)
	// This ensures any extra space at edges is filled with terminal background
	alloc := da.GetAllocation()
	cr.SetSourceRGB(
		float64(scheme.Background.R)/255.0,
		float64(scheme.Background.G)/255.0,
		float64(scheme.Background.B)/255.0)
	cr.Rectangle(0, 0, float64(alloc.GetWidth()), float64(alloc.GetHeight()))
	cr.Fill()

	// Set up font
	cr.SelectFontFace(fontFamily, cairo.FONT_SLANT_NORMAL, cairo.FONT_WEIGHT_NORMAL)
	cr.SetFontSize(float64(fontSize))

	// Get horizontal scroll offset
	horizOffset := w.buffer.GetHorizOffset()

	// Draw each cell (use GetVisibleCell to account for scroll offset)
	for y := 0; y < rows; y++ {
		lineAttr := w.buffer.GetVisibleLineAttribute(y)

		// Calculate effective columns for this line (half for double-width/height)
		effectiveCols := cols
		if lineAttr != purfecterm.LineAttrNormal {
			effectiveCols = cols / 2
		}

		// Calculate the range of logical columns to render
		startCol := horizOffset
		endCol := horizOffset + effectiveCols

		// Track accumulated visual width for flex-width rendering
		// This is the accumulated width in base cell units (before line attribute scaling)
		visibleAccumulatedWidth := 0.0

		for logicalX := startCol; logicalX < endCol; logicalX++ {
			// Screen position (0-based from visible area)
			x := logicalX - horizOffset
			cell := w.buffer.GetVisibleCell(logicalX, y)

			// Calculate this cell's visual width
			cellVisualWidth := 1.0
			if cell.FlexWidth && cell.CellWidth > 0 {
				cellVisualWidth = cell.CellWidth
			}

			// Determine colors
			fg := cell.Foreground
			bg := cell.Background
			if fg.Default {
				fg = scheme.Foreground
			}
			if bg.Default {
				bg = scheme.Background
			}

			// Handle blink attribute based on mode
			blinkVisible := true // For traditional blink mode
			if cell.Blink {
				switch scheme.BlinkMode {
				case purfecterm.BlinkModeBright:
					// Interpret blink as bright background (VGA style)
					// Find if bg matches a dark color (0-7) and use bright version (8-15)
					for i := 0; i < 8; i++ {
						if len(scheme.Palette) > i+8 &&
							bg.R == scheme.Palette[i].R &&
							bg.G == scheme.Palette[i].G &&
							bg.B == scheme.Palette[i].B {
							bg = scheme.Palette[i+8]
							break
						}
					}
				case purfecterm.BlinkModeBlink:
					// Traditional on/off blink - visible when phase is in first half
					blinkVisible = blinkPhase < 3.14159
					// BlinkModeBounce is handled later in character drawing
				}
			}

			// Handle selection highlighting (use logicalX for buffer position)
			if w.buffer.IsInSelection(logicalX, y) {
				bg = scheme.Selection
			}

			// Handle cursor - only swap colors for solid block cursor when focused
			isCursor := cursorVisible && logicalX == cursorX && y == cursorY && w.cursorBlinkOn
			if isCursor && w.hasFocus && cursorShape == 0 {
				// Swap colors for solid block cursor when focused
				fg, bg = bg, fg
			}

			// Calculate cell position and size based on line attributes and flex width
			var cellX, cellY, cellW, cellH float64
			switch lineAttr {
			case purfecterm.LineAttrNormal:
				// Use accumulated width for X position when cells have flex width
				cellX = visibleAccumulatedWidth*float64(charWidth) + float64(terminalLeftPadding)
				cellY = float64(y * charHeight)
				cellW = cellVisualWidth * float64(charWidth)
				cellH = float64(charHeight)
			case purfecterm.LineAttrDoubleWidth:
				// Each character takes up 2x its normal width
				cellX = visibleAccumulatedWidth*2.0*float64(charWidth) + float64(terminalLeftPadding)
				cellY = float64(y * charHeight)
				cellW = cellVisualWidth * float64(charWidth) * 2.0
				cellH = float64(charHeight)
			case purfecterm.LineAttrDoubleTop, purfecterm.LineAttrDoubleBottom:
				// Each character takes up 2x its normal width, text is rendered 2x height
				cellX = visibleAccumulatedWidth*2.0*float64(charWidth) + float64(terminalLeftPadding)
				cellY = float64(y * charHeight)
				cellW = cellVisualWidth * float64(charWidth) * 2.0
				cellH = float64(charHeight)
			}

			// Track accumulated width for next cell (after position calculation)
			_ = x // x is still useful for wave animation phase calculation
			visibleAccumulatedWidth += cellVisualWidth

			// Draw cell background if different from terminal background
			if bg != scheme.Background {
				cr.SetSourceRGB(
					float64(bg.R)/255.0,
					float64(bg.G)/255.0,
					float64(bg.B)/255.0)
				cr.Rectangle(cellX, cellY, cellW, cellH)
				cr.Fill()
			}

			// Draw character (skip if traditional blink mode and currently invisible)
			if cell.Char != ' ' && cell.Char != 0 && blinkVisible {
				// Determine which font to use for this character (with fallback for Unicode/CJK)
				charFont := w.getFontForCharacter(cell.Char, fontFamily, fontSize)

				// Get character string including any combining marks
				charStr := cell.String()

				// Measure actual character width using Pango (handles combining chars properly)
				actualWidth := float64(pangoTextWidth(cr, charStr, charFont, fontSize, cell.Bold))

				// Get foreground color as floats
				fgR := float64(fg.R) / 255.0
				fgG := float64(fg.G) / 255.0
				fgB := float64(fg.B) / 255.0

				// Calculate vertical offset for bobbing wave animation on blink text
				// Each character is offset by a phase shift based on its x position,
				// creating a "wave" effect where characters bob up and down in sequence
				yOffset := 0.0
				if cell.Blink && scheme.BlinkMode == purfecterm.BlinkModeBounce {
					// Wave parameters: each character is phase-shifted by 0.5 radians from its neighbor
					// Amplitude is about 3 pixels up and down
					wavePhase := blinkPhase + float64(x)*0.5
					yOffset = math.Sin(wavePhase) * 3.0
				}

				switch lineAttr {
				case purfecterm.LineAttrNormal:
					// Apply global screen scaling (132-column, 40-column, line density)
					// Characters are drawn at scaled size to fit in scaled cells
					cr.Save()

					// Calculate horizontal scale factor for flex width cells
					// The cell's target width is cellW (which accounts for CellWidth)
					targetCellWidth := cellW / horizScale // Unscaled target width
					textScaleX := horizScale
					xOff := 0.0
					if actualWidth > targetCellWidth {
						// Wide char: squeeze to fit cell width, then apply global scale
						textScaleX *= targetCellWidth / actualWidth
					} else if actualWidth < targetCellWidth {
						// Narrow char: center within the cell (offset is in scaled coordinates)
						xOff = (targetCellWidth - actualWidth) / 2.0 * horizScale
					}

					textBaseX := cellX + xOff
					textBaseY := float64(y*charHeight) + yOffset
					cr.Translate(textBaseX, textBaseY)
					cr.Scale(textScaleX, vertScale)
					// Use Pango for proper combining character rendering
					pangoRenderText(cr, charStr, charFont, fontSize, cell.Bold, fgR, fgG, fgB)
					cr.Restore()

				case purfecterm.LineAttrDoubleWidth:
					// Double-width line: 2x horizontal scale on top of global scaling
					cr.Save()
					// The cell's target width is cellW (which includes 2x for double-width)
					targetCellWidth := cellW / (horizScale * 2.0) // Unscaled target width
					textScaleX := horizScale * 2.0
					xOff := 0.0
					if actualWidth > targetCellWidth {
						// Wide char: squeeze to fit cell
						textScaleX *= targetCellWidth / actualWidth
					} else if actualWidth < targetCellWidth {
						// Center narrow char (offset in final scaled coordinates)
						xOff = (targetCellWidth - actualWidth) * horizScale
					}
					textX := cellX + xOff
					textY := cellY + yOffset
					cr.Translate(textX, textY)
					cr.Scale(textScaleX, vertScale)
					pangoRenderText(cr, charStr, charFont, fontSize, cell.Bold, fgR, fgG, fgB)
					cr.Restore()

				case purfecterm.LineAttrDoubleTop:
					// Double-height top half: 2x both directions, show top half only
					cr.Save()
					// Clip to just this cell's area
					cr.Rectangle(cellX, cellY, cellW, cellH)
					cr.Clip()
					// The cell's target width is cellW (which includes 2x for double-width)
					targetCellWidth := cellW / (horizScale * 2.0) // Unscaled target width
					textScaleX := horizScale * 2.0
					textScaleY := vertScale * 2.0
					xOff := 0.0
					if actualWidth > targetCellWidth {
						textScaleX *= targetCellWidth / actualWidth
					} else if actualWidth < targetCellWidth {
						xOff = (targetCellWidth - actualWidth) * horizScale
					}
					// Position baseline at 2x ascent (only top half visible due to clip)
					textX := cellX + xOff
					textY := cellY + yOffset*2
					cr.Translate(textX, textY)
					cr.Scale(textScaleX, textScaleY)
					pangoRenderText(cr, charStr, charFont, fontSize, cell.Bold, fgR, fgG, fgB)
					cr.Restore()

				case purfecterm.LineAttrDoubleBottom:
					// Double-height bottom half: 2x both directions, show bottom half only
					cr.Save()
					// Clip to just this cell's area
					cr.Rectangle(cellX, cellY, cellW, cellH)
					cr.Clip()
					// The cell's target width is cellW (which includes 2x for double-width)
					targetCellWidth := cellW / (horizScale * 2.0) // Unscaled target width
					textScaleX := horizScale * 2.0
					textScaleY := vertScale * 2.0
					xOff := 0.0
					if actualWidth > targetCellWidth {
						textScaleX *= targetCellWidth / actualWidth
					} else if actualWidth < targetCellWidth {
						xOff = (targetCellWidth - actualWidth) * horizScale
					}
					// Position so bottom half is visible (shift up by one cell height)
					textX := cellX + xOff
					textY := cellY - float64(charHeight) + yOffset*2
					cr.Translate(textX, textY)
					cr.Scale(textScaleX, textScaleY)
					pangoRenderText(cr, charStr, charFont, fontSize, cell.Bold, fgR, fgG, fgB)
					cr.Restore()
				}
			}

			// Draw underline if needed
			if cell.Underline {
				cr.SetSourceRGB(
					float64(fg.R)/255.0,
					float64(fg.G)/255.0,
					float64(fg.B)/255.0)
				underlineY := cellY + cellH - 1
				underlineH := 1.0
				if lineAttr == purfecterm.LineAttrDoubleTop || lineAttr == purfecterm.LineAttrDoubleBottom {
					underlineH = 2.0
				}
				cr.Rectangle(cellX, underlineY, cellW, underlineH)
				cr.Fill()
			}

			// Draw cursor based on shape (0=block, 1=underline, 2=bar)
			if isCursor {
				cr.SetSourceRGB(
					float64(scheme.Cursor.R)/255.0,
					float64(scheme.Cursor.G)/255.0,
					float64(scheme.Cursor.B)/255.0)

				switch cursorShape {
				case 0: // Block cursor
					if !w.hasFocus {
						// Outline block when unfocused
						cr.SetLineWidth(1.0)
						cr.Rectangle(cellX+0.5, cellY+0.5, cellW-1, cellH-1)
						cr.Stroke()
					}
					// Focused block is handled by fg/bg swap above

				case 1: // Underline cursor (1/4 block height)
					thickness := cellH / 4.0
					if !w.hasFocus {
						thickness = cellH / 6.0 // Thinner when unfocused
					}
					cr.Rectangle(cellX, cellY+cellH-thickness, cellW, thickness)
					cr.Fill()

				case 2: // Bar (vertical line) cursor
					thickness := 2.0
					if !w.hasFocus {
						thickness = 1.0
					}
					cr.Rectangle(cellX, cellY, thickness, cellH)
					cr.Fill()
				}
			}
		}
	}

	// Draw yellow dashed line between scrollback and logical screen
	if scrollOffset > 0 && scrollOffset < rows {
		lineY := float64(scrollOffset * charHeight)
		cr.SetSourceRGB(1.0, 0.78, 0.0) // Yellow (255, 200, 0)
		cr.SetLineWidth(1.0)
		cr.SetDash([]float64{4, 4}, 0)
		cr.MoveTo(0, lineY)
		cr.LineTo(float64(alloc.GetWidth()), lineY)
		cr.Stroke()
		cr.SetDash([]float64{}, 0) // Reset dash pattern
	}

	w.buffer.ClearDirty()
	return true
}

func (w *Widget) screenToCell(screenX, screenY float64) (cellX, cellY int) {
	w.mu.Lock()
	baseCharWidth := w.charWidth
	baseCharHeight := w.charHeight
	w.mu.Unlock()

	// Apply screen scaling
	horizScale := w.buffer.GetHorizontalScale()
	vertScale := w.buffer.GetVerticalScale()
	charWidth := int(float64(baseCharWidth) * horizScale)
	charHeight := int(float64(baseCharHeight) * vertScale)

	// Calculate row first (needed to check line attributes)
	cellY = int(screenY) / charHeight

	cols, rows := w.buffer.GetSize()
	if cellY < 0 {
		cellY = 0
	}
	if cellY >= rows {
		cellY = rows - 1
	}

	// Check if this line has doubled attributes (affects column calculation)
	lineAttr := w.buffer.GetVisibleLineAttribute(cellY)
	lineScale := 1.0
	if lineAttr != purfecterm.LineAttrNormal {
		// Doubled lines: each logical cell is 2x wide visually
		lineScale = 2.0
	}

	// Calculate which cell the mouse is in, accounting for flex width
	// First, get the x position relative to content area
	relativeX := screenX - float64(terminalLeftPadding)
	if relativeX < 0 {
		cellX = 0
		return
	}

	// Get horizontal scroll offset
	horizOffset := w.buffer.GetHorizOffset()

	// Iterate through cells to find which one contains this x position
	// accumulatedPixels tracks the right edge of each cell
	accumulatedPixels := 0.0
	for col := horizOffset; col < cols+horizOffset; col++ {
		cell := w.buffer.GetVisibleCell(col, cellY)

		// Calculate this cell's visual width
		cellVisualWidth := 1.0
		if cell.FlexWidth && cell.CellWidth > 0 {
			cellVisualWidth = cell.CellWidth
		}

		// Calculate pixel width of this cell
		cellPixelWidth := cellVisualWidth * float64(charWidth) * lineScale

		// Check if the click is within this cell
		if relativeX < accumulatedPixels+cellPixelWidth {
			cellX = col
			return
		}

		accumulatedPixels += cellPixelWidth
	}

	// If we've gone past all cells, return the last cell
	cellX = cols + horizOffset - 1
	if cellX < 0 {
		cellX = 0
	}
	return
}

func (w *Widget) onButtonPress(da *gtk.DrawingArea, ev *gdk.Event) bool {
	btn := gdk.EventButtonNewFromEvent(ev)
	x, y := btn.X(), btn.Y()
	button := btn.Button()

	if button == 1 { // Left button
		cellX, cellY := w.screenToCell(x, y)
		// Record press position but don't start selection yet
		w.mouseDown = true
		w.mouseDownX = cellX
		w.mouseDownY = cellY
		w.selectionMoved = false
		w.buffer.ClearSelection()
		da.GrabFocus()
		return true
	}
	// Let other buttons (like right-click) propagate for context menus
	return false
}

func (w *Widget) onButtonRelease(da *gtk.DrawingArea, ev *gdk.Event) bool {
	btn := gdk.EventButtonNewFromEvent(ev)
	button := btn.Button()

	if button == 1 {
		w.mouseDown = false
		if w.selecting {
			w.selecting = false
			w.buffer.EndSelection()
		}
	}
	return true
}

func (w *Widget) onMotionNotify(da *gtk.DrawingArea, ev *gdk.Event) bool {
	if !w.mouseDown {
		return false
	}

	// Use C helper to get coordinates from the event
	var x, y C.double
	C.get_event_coords((*C.GdkEvent)(unsafe.Pointer(ev.Native())), &x, &y)
	cellX, cellY := w.screenToCell(float64(x), float64(y))

	// Only start selection once mouse has moved to a different cell
	if !w.selectionMoved {
		if cellX != w.mouseDownX || cellY != w.mouseDownY {
			// Start selection from original mouse-down position
			w.selectionMoved = true
			w.selecting = true
			w.selectStartX = w.mouseDownX
			w.selectStartY = w.mouseDownY
			w.buffer.StartSelection(w.mouseDownX, w.mouseDownY)
		} else {
			return true // Mouse still in same cell, don't select yet
		}
	}

	w.buffer.UpdateSelection(cellX, cellY)
	return true
}

func (w *Widget) onScroll(da *gtk.DrawingArea, ev *gdk.Event) bool {
	scroll := gdk.EventScrollNewFromEvent(ev)
	dir := scroll.Direction()
	state := scroll.State()

	// Check for Shift modifier for horizontal scrolling
	hasShift := state&gdk.SHIFT_MASK != 0

	maxOffset := w.buffer.GetMaxScrollOffset()

	switch dir {
	case gdk.SCROLL_UP:
		if hasShift {
			// Horizontal scroll left
			horizOffset := w.buffer.GetHorizOffset()
			horizOffset -= 3
			if horizOffset < 0 {
				horizOffset = 0
			}
			w.buffer.SetHorizOffset(horizOffset)
		} else {
			// Vertical scroll up
			offset := w.buffer.GetScrollOffset()
			offset += 3
			if offset > maxOffset {
				offset = maxOffset
			}
			w.buffer.SetScrollOffset(offset)
		}
	case gdk.SCROLL_DOWN:
		if hasShift {
			// Horizontal scroll right
			horizOffset := w.buffer.GetHorizOffset()
			maxHoriz := w.buffer.GetMaxHorizOffset()
			horizOffset += 3
			if horizOffset > maxHoriz {
				horizOffset = maxHoriz
			}
			w.buffer.SetHorizOffset(horizOffset)
		} else {
			// Vertical scroll down
			offset := w.buffer.GetScrollOffset()
			offset -= 3
			if offset < 0 {
				offset = 0
			}
			w.buffer.SetScrollOffset(offset)
		}
	case gdk.SCROLL_LEFT:
		// Horizontal scroll left
		horizOffset := w.buffer.GetHorizOffset()
		horizOffset -= 3
		if horizOffset < 0 {
			horizOffset = 0
		}
		w.buffer.SetHorizOffset(horizOffset)
	case gdk.SCROLL_RIGHT:
		// Horizontal scroll right
		horizOffset := w.buffer.GetHorizOffset()
		maxHoriz := w.buffer.GetMaxHorizOffset()
		horizOffset += 3
		if horizOffset > maxHoriz {
			horizOffset = maxHoriz
		}
		w.buffer.SetHorizOffset(horizOffset)
	}

	w.updateScrollbar()
	return true
}

func (w *Widget) onKeyPress(da *gtk.DrawingArea, ev *gdk.Event) bool {
	key := gdk.EventKeyNewFromEvent(ev)
	keyval := key.KeyVal()
	state := key.State()

	w.mu.Lock()
	onInput := w.onInput
	w.mu.Unlock()

	// Extract modifier states (cast ModifierType to uint for bitwise ops)
	hasShift := state&uint(gdk.SHIFT_MASK) != 0
	hasCtrl := state&uint(gdk.CONTROL_MASK) != 0
	hasAlt := state&uint(gdk.MOD1_MASK) != 0  // Alt key
	hasMeta := state&uint(gdk.META_MASK) != 0 // Meta/Command key
	hasSuper := state&uint(gdk.SUPER_MASK) != 0

	// Ignore modifier-only key presses (they don't produce terminal output)
	if isModifierKey(keyval) {
		return false
	}

	// Also check hardware keycode for Wine/Windows modifier keys
	// Only on Windows - macOS keycodes are different (e.g., 16='y', 17='t' on macOS)
	hwcode := key.HardwareKeyCode()
	if runtime.GOOS == "windows" && isModifierKeycode(hwcode) {
		return false
	}

	// Handle clipboard copy (Ctrl+C with selection only)
	// Note: Ctrl+V paste is NOT handled here - use PasteClipboard() via context menu
	// Note: Ctrl+A is NOT handled here - it passes through to the terminal
	// for programs that use it (e.g., readline beginning-of-line)
	if hasCtrl && !hasAlt && !hasMeta {
		switch keyval {
		case gdk.KEY_c, gdk.KEY_C:
			if w.buffer.HasSelection() {
				text := w.buffer.GetSelectedText()
				if w.clipboard != nil {
					w.clipboard.SetText(text)
				}
				return true
			}
			// Ctrl+C without selection falls through to send interrupt
		}
	}

	if onInput == nil {
		return false
	}

	// Calculate xterm-style modifier parameter
	// mod = 1 + (shift?1:0) + (alt?2:0) + (ctrl?4:0) + (meta?8:0)
	mod := 1
	if hasShift {
		mod += 1
	}
	if hasAlt {
		mod += 2
	}
	if hasCtrl {
		mod += 4
	}
	if hasMeta || hasSuper {
		mod += 8
	}
	hasModifiers := mod > 1

	var data []byte

	// Handle special keys with potential modifiers
	switch keyval {
	case gdk.KEY_Return, gdk.KEY_KP_Enter:
		if hasModifiers {
			data = modifiedSpecialKey(mod, 13, 0) // CSI 13 ; mod u (kitty protocol)
		} else {
			data = []byte{'\r'}
		}
	case gdk.KEY_BackSpace:
		if hasCtrl {
			data = []byte{0x08} // Ctrl+Backspace = BS
		} else if hasAlt {
			data = []byte{0x1b, 0x7f} // Alt+Backspace = ESC DEL
		} else {
			data = []byte{0x7f}
		}
	case gdk.KEY_Tab:
		if hasShift {
			data = []byte{0x1b, '[', 'Z'} // Shift+Tab = CSI Z (backtab)
		} else if hasCtrl {
			data = []byte{'\t'} // Ctrl+Tab (some apps use this)
		} else {
			data = []byte{'\t'}
		}
	case gdk.KEY_ISO_Left_Tab: // GTK sends this for Shift+Tab on many systems
		data = []byte{0x1b, '[', 'Z'} // Shift+Tab = CSI Z (backtab)
	case gdk.KEY_Escape:
		if hasAlt {
			data = []byte{0x1b, 0x1b} // Alt+Escape
		} else {
			data = []byte{0x1b}
		}

	// Arrow keys
	case gdk.KEY_Up:
		data = cursorKey('A', mod, hasModifiers)
	case gdk.KEY_Down:
		data = cursorKey('B', mod, hasModifiers)
	case gdk.KEY_Right:
		data = cursorKey('C', mod, hasModifiers)
	case gdk.KEY_Left:
		data = cursorKey('D', mod, hasModifiers)

	// Navigation keys
	case gdk.KEY_Home:
		data = cursorKey('H', mod, hasModifiers)
	case gdk.KEY_End:
		data = cursorKey('F', mod, hasModifiers)
	case gdk.KEY_Page_Up:
		data = tildeKey(5, mod, hasModifiers)
	case gdk.KEY_Page_Down:
		data = tildeKey(6, mod, hasModifiers)
	case gdk.KEY_Insert:
		data = tildeKey(2, mod, hasModifiers)
	case gdk.KEY_Delete:
		data = tildeKey(3, mod, hasModifiers)

	// Function keys F1-F4 (use SS3 format without modifiers, CSI format with)
	case gdk.KEY_F1:
		data = functionKey(1, 'P', mod, hasModifiers)
	case gdk.KEY_F2:
		data = functionKey(2, 'Q', mod, hasModifiers)
	case gdk.KEY_F3:
		data = functionKey(3, 'R', mod, hasModifiers)
	case gdk.KEY_F4:
		data = functionKey(4, 'S', mod, hasModifiers)

	// Function keys F5-F12 (use tilde format)
	case gdk.KEY_F5:
		data = tildeKey(15, mod, hasModifiers)
	case gdk.KEY_F6:
		data = tildeKey(17, mod, hasModifiers)
	case gdk.KEY_F7:
		data = tildeKey(18, mod, hasModifiers)
	case gdk.KEY_F8:
		data = tildeKey(19, mod, hasModifiers)
	case gdk.KEY_F9:
		data = tildeKey(20, mod, hasModifiers)
	case gdk.KEY_F10:
		data = tildeKey(21, mod, hasModifiers)
	case gdk.KEY_F11:
		data = tildeKey(23, mod, hasModifiers)
	case gdk.KEY_F12:
		data = tildeKey(24, mod, hasModifiers)

	// Keypad keys
	case gdk.KEY_KP_Up:
		data = cursorKey('A', mod, hasModifiers)
	case gdk.KEY_KP_Down:
		data = cursorKey('B', mod, hasModifiers)
	case gdk.KEY_KP_Right:
		data = cursorKey('C', mod, hasModifiers)
	case gdk.KEY_KP_Left:
		data = cursorKey('D', mod, hasModifiers)
	case gdk.KEY_KP_Home:
		data = cursorKey('H', mod, hasModifiers)
	case gdk.KEY_KP_End:
		data = cursorKey('F', mod, hasModifiers)
	case gdk.KEY_KP_Page_Up:
		data = tildeKey(5, mod, hasModifiers)
	case gdk.KEY_KP_Page_Down:
		data = tildeKey(6, mod, hasModifiers)
	case gdk.KEY_KP_Insert:
		data = tildeKey(2, mod, hasModifiers)
	case gdk.KEY_KP_Delete:
		data = tildeKey(3, mod, hasModifiers)

	default:
		// Regular character handling
		data = w.handleRegularKey(keyval, key, hasShift, hasCtrl, hasAlt, hasMeta, hasSuper)
	}

	// Final fallback: check hardware keycodes for special keys (Wine/Windows)
	if len(data) == 0 {
		hwcode := key.HardwareKeyCode()
		data = hardwareKeycodeToSpecialWithMod(hwcode, mod, hasModifiers)

		// If still no data, try regular character from hardware keycode
		if len(data) == 0 {
			if ch := hardwareKeycodeToChar(hwcode, hasShift); ch != 0 {
				data = w.processCharWithModifiers(ch, hasShift, hasCtrl, hasAlt, hasMeta, hasSuper)
			}
		}
	}

	if len(data) > 0 {
		onInput(data)
		return true
	}

	return false
}

// handleRegularKey processes regular character keys with modifiers
func (w *Widget) handleRegularKey(keyval uint, key *gdk.EventKey, hasShift, hasCtrl, hasAlt, hasMeta, hasSuper bool) []byte {
	var ch byte
	var isChar bool

	// On macOS, Option key composes special Unicode characters (e.g., Option+R = ®)
	// We want to treat Option as Alt/Meta modifier instead, using the base key
	if runtime.GOOS == "darwin" && hasAlt {
		hwcode := key.HardwareKeyCode()
		if baseCh := macKeycodeToChar(hwcode, hasShift); baseCh != 0 {
			// Apply Ctrl transformation if needed (convert letter to control char)
			if hasCtrl {
				if baseCh >= 'a' && baseCh <= 'z' {
					baseCh = baseCh - 'a' + 1
				} else if baseCh >= 'A' && baseCh <= 'Z' {
					baseCh = baseCh - 'A' + 1
				}
			}

			// Check if the result is a named key that should use kitty protocol
			var keycode int
			switch baseCh {
			case 0x0D: // CR = Enter (from Ctrl+M)
				keycode = 13
			case 0x09: // HT = Tab (from Ctrl+I)
				keycode = 9
			case 0x08: // BS = Backspace (from Ctrl+H)
				keycode = 127
			case 0x7F: // DEL
				keycode = 127
			case 0x1B: // ESC
				keycode = 27
			}

			if keycode != 0 {
				// Use kitty protocol: CSI keycode ; mod u
				// mod = 1 + (shift?1:0) + (alt?2:0) + (ctrl?4:0) + (meta?8:0)
				// Ctrl is consumed by letter->control_char, so not included
				mod := 1
				if hasShift {
					mod += 1
				}
				mod += 2 // Alt (Option) is always pressed in this branch
				if hasMeta || hasSuper {
					mod += 8
				}
				return []byte(fmt.Sprintf("\x1b[%d;%du", keycode, mod))
			}

			// Send ESC + base character for Alt+key
			return []byte{0x1b, baseCh}
		}
	}

	// Try to get character from keyval
	if keyval >= 0x20 && keyval < 256 {
		ch = byte(keyval)
		isChar = true
	} else if keyval >= 0x20 {
		// Unicode character - only handle if no special modifiers
		if r := gdk.KeyvalToUnicode(keyval); r != 0 && r < 128 {
			ch = byte(r)
			isChar = true
		} else if r != 0 {
			// Full unicode - send as UTF-8, with ESC prefix if Alt
			if hasAlt && !hasCtrl {
				return append([]byte{0x1b}, []byte(string(r))...)
			}
			return []byte(string(r))
		}
	}

	if !isChar {
		return nil
	}

	return w.processCharWithModifiers(ch, hasShift, hasCtrl, hasAlt, hasMeta, hasSuper)
}

// processCharWithModifiers applies modifier transformations to a character
func (w *Widget) processCharWithModifiers(ch byte, hasShift, hasCtrl, hasAlt, hasMeta, hasSuper bool) []byte {
	// Ctrl+letter produces control character (1-26)
	if hasCtrl && ch >= 'a' && ch <= 'z' {
		ch = ch - 'a' + 1
	} else if hasCtrl && ch >= 'A' && ch <= 'Z' {
		ch = ch - 'A' + 1
	} else if hasCtrl {
		// Other Ctrl combinations
		switch ch {
		case '@':
			ch = 0 // Ctrl+@ = NUL
		case '[':
			ch = 0x1b // Ctrl+[ = ESC
		case '\\':
			ch = 0x1c // Ctrl+\ = FS
		case ']':
			ch = 0x1d // Ctrl+] = GS
		case '^':
			ch = 0x1e // Ctrl+^ = RS
		case '_':
			ch = 0x1f // Ctrl+_ = US
		case '?':
			ch = 0x7f // Ctrl+? = DEL
		case ' ':
			ch = 0 // Ctrl+Space = NUL
		}
	}

	// Check if the control char is a "named key" that should use kitty protocol
	// when combined with other modifiers (Alt/Meta/Super)
	if hasAlt || hasMeta || hasSuper {
		// Map control chars to their keycode for kitty protocol
		var keycode int
		switch ch {
		case 0x0D: // CR = Enter (from Ctrl+M)
			keycode = 13
		case 0x09: // HT = Tab (from Ctrl+I)
			keycode = 9
		case 0x08: // BS = Backspace (from Ctrl+H)
			keycode = 127 // Use DEL keycode for backspace
		case 0x7F: // DEL
			keycode = 127
		case 0x1B: // ESC
			keycode = 27
		}

		if keycode != 0 {
			// Use kitty protocol: CSI keycode ; mod u
			// Compute modifier: 1 + (shift?1:0) + (alt?2:0) + (ctrl?4:0) + (meta?8:0)
			// Note: Ctrl is NOT included since it was consumed to produce the control char
			mod := 1
			if hasShift {
				mod += 1
			}
			if hasAlt {
				mod += 2
			}
			if hasMeta || hasSuper {
				mod += 8
			}
			return []byte(fmt.Sprintf("\x1b[%d;%du", keycode, mod))
		}

		// For other control chars, use ESC prefix
		return []byte{0x1b, ch}
	}

	return []byte{ch}
}

// cursorKey generates escape sequence for cursor keys (arrows, home, end)
// Without modifiers: ESC [ <key>
// With modifiers: ESC [ 1 ; <mod> <key>
func cursorKey(key byte, mod int, hasModifiers bool) []byte {
	if hasModifiers {
		return []byte(fmt.Sprintf("\x1b[1;%d%c", mod, key))
	}
	return []byte{0x1b, '[', key}
}

// tildeKey generates escape sequence for tilde-style keys (PgUp, PgDn, Insert, Delete, F5-F12)
// Without modifiers: ESC [ <num> ~
// With modifiers: ESC [ <num> ; <mod> ~
func tildeKey(num int, mod int, hasModifiers bool) []byte {
	numStr := []byte(fmt.Sprintf("%d", num))
	if hasModifiers {
		modStr := []byte(fmt.Sprintf(";%d", mod))
		result := append([]byte{0x1b, '['}, numStr...)
		result = append(result, modStr...)
		result = append(result, '~')
		return result
	}
	result := append([]byte{0x1b, '['}, numStr...)
	result = append(result, '~')
	return result
}

// functionKey generates escape sequence for F1-F4
// Without modifiers: ESC O <key> (SS3 format)
// With modifiers: ESC [ 1 ; <mod> <key> (CSI format)
func functionKey(num int, key byte, mod int, hasModifiers bool) []byte {
	if hasModifiers {
		return []byte(fmt.Sprintf("\x1b[1;%d%c", mod, key))
	}
	return []byte{0x1b, 'O', key}
}

// modifiedSpecialKey generates CSI u format for special keys with modifiers (kitty protocol style)
func modifiedSpecialKey(mod int, keycode int, suffix byte) []byte {
	if suffix != 0 {
		return []byte(fmt.Sprintf("\x1b[%d;%d%c", keycode, mod, suffix))
	}
	return []byte(fmt.Sprintf("\x1b[%d;%du", keycode, mod))
}

func (w *Widget) onConfigure(da *gtk.DrawingArea, ev *gdk.Event) bool {
	w.updateFontMetrics()

	// Apply screen scaling to character dimensions
	horizScale := w.buffer.GetHorizontalScale()
	vertScale := w.buffer.GetVerticalScale()
	scaledCharWidth := int(float64(w.charWidth) * horizScale)
	scaledCharHeight := int(float64(w.charHeight) * vertScale)
	if scaledCharWidth < 1 {
		scaledCharWidth = 1
	}
	if scaledCharHeight < 1 {
		scaledCharHeight = 1
	}

	// Recalculate terminal size based on widget size (minus left padding)
	alloc := da.GetAllocation()
	newCols := (alloc.GetWidth() - terminalLeftPadding) / scaledCharWidth
	newRows := alloc.GetHeight() / scaledCharHeight

	if newCols < 1 {
		newCols = 1
	}
	if newRows < 1 {
		newRows = 1
	}

	w.buffer.Resize(newCols, newRows)
	return false
}

func (w *Widget) onFocusIn(da *gtk.DrawingArea, ev *gdk.Event) bool {
	w.hasFocus = true
	w.cursorBlinkOn = true // Reset blink so cursor is immediately visible
	w.drawingArea.QueueDraw()
	return false
}

func (w *Widget) onFocusOut(da *gtk.DrawingArea, ev *gdk.Event) bool {
	w.hasFocus = false
	w.drawingArea.QueueDraw()
	return false
}

func (w *Widget) onScrollbarChanged(sb *gtk.Scrollbar) {
	adj := sb.GetAdjustment()
	val := int(adj.GetValue())
	maxOffset := w.buffer.GetMaxScrollOffset()
	// Invert - scrollbar at top means scrolled back
	w.buffer.SetScrollOffset(maxOffset - val)
	w.updateHorizScrollbar() // Horizontal scrollbar depends on scroll position
}

func (w *Widget) onHorizScrollbarChanged(sb *gtk.Scrollbar) {
	adj := sb.GetAdjustment()
	val := int(adj.GetValue())
	w.buffer.SetHorizOffset(val)
}

func (w *Widget) updateScrollbar() {
	maxOffset := w.buffer.GetMaxScrollOffset()
	offset := w.buffer.GetScrollOffset()
	_, rows := w.buffer.GetSize()

	adj := w.scrollbar.GetAdjustment()
	adj.SetLower(0)
	adj.SetUpper(float64(maxOffset + rows))
	adj.SetPageSize(float64(rows))
	adj.SetValue(float64(maxOffset - offset))

	// Also update horizontal scrollbar
	w.updateHorizScrollbar()
}

func (w *Widget) updateHorizScrollbar() {
	cols, _ := w.buffer.GetSize()
	longestLine := w.buffer.GetLongestLineVisible()
	horizOffset := w.buffer.GetHorizOffset()

	// Show horizontal scrollbar only if content is wider than visible area
	if longestLine > cols {
		w.horizScrollbar.Show()

		adj := w.horizScrollbar.GetAdjustment()
		adj.SetLower(0)
		adj.SetUpper(float64(longestLine))
		adj.SetPageSize(float64(cols))
		adj.SetValue(float64(horizOffset))
	} else {
		w.horizScrollbar.Hide()
		// Reset horizontal offset if no longer needed
		if horizOffset > 0 {
			w.buffer.SetHorizOffset(0)
		}
	}
}

// Resize resizes the terminal to the specified dimensions
func (w *Widget) Resize(cols, rows int) {
	w.buffer.Resize(cols, rows)
	w.updateScrollbar()
}

// GetSize returns the current terminal size in characters
func (w *Widget) GetSize() (cols, rows int) {
	return w.buffer.GetSize()
}

// GetSelectedText returns currently selected text
func (w *Widget) GetSelectedText() string {
	return w.buffer.GetSelectedText()
}

// CopySelection copies selected text to clipboard
func (w *Widget) CopySelection() {
	if w.clipboard != nil && w.buffer.HasSelection() {
		text := w.buffer.GetSelectedText()
		w.clipboard.SetText(text)
	}
}

// PasteClipboard pastes text from clipboard into terminal
// Uses bracketed paste mode if enabled by the application or if the
// pasted text contains special characters (newlines, control chars, etc.)
func (w *Widget) PasteClipboard() {
	if w.clipboard != nil && w.onInput != nil {
		text, err := w.clipboard.WaitForText()
		if err == nil && len(text) > 0 {
			// Determine if we should use bracketed paste
			useBracketedPaste := w.buffer.IsBracketedPasteModeEnabled()

			// Also use bracketed paste if text contains special characters
			// even if the application hasn't requested it
			if !useBracketedPaste {
				for _, c := range text {
					// Check for newlines, control chars, or escape
					if c == '\n' || c == '\r' || c == '\x1b' || c < 32 {
						useBracketedPaste = true
						break
					}
				}
			}

			if useBracketedPaste {
				// Send bracketed paste start sequence
				w.onInput([]byte("\x1b[200~"))
				w.onInput([]byte(text))
				// Send bracketed paste end sequence
				w.onInput([]byte("\x1b[201~"))
			} else {
				w.onInput([]byte(text))
			}
		}
	}
}

// SelectAll selects all text in the terminal
func (w *Widget) SelectAll() {
	w.buffer.SelectAll()
}

// SetCursorVisible shows or hides the cursor
func (w *Widget) SetCursorVisible(visible bool) {
	w.buffer.SetCursorVisible(visible)
}

// hardwareKeycodeToSpecialWithMod maps Windows Virtual Key codes to special key sequences with modifier support.
// This is used as a fallback when GDK can't translate keypresses (Wine/Windows).
// On Windows/Wine, HardwareKeyCode() returns Windows VK codes, not X11 keycodes.
func hardwareKeycodeToSpecialWithMod(hwcode uint16, mod int, hasModifiers bool) []byte {
	// Windows Virtual Key code mappings
	switch hwcode {
	case 13: // VK_RETURN
		return []byte{'\r'}
	case 8: // VK_BACK
		if hasModifiers && mod >= 5 { // Ctrl
			return []byte{0x08}
		} else if hasModifiers && mod >= 3 { // Alt
			return []byte{0x1b, 0x7f}
		}
		return []byte{0x7f}
	case 9: // VK_TAB
		if hasModifiers && (mod == 2 || mod == 3) { // Shift
			return []byte{0x1b, '[', 'Z'}
		}
		return []byte{'\t'}
	case 27: // VK_ESCAPE
		return []byte{0x1b}

	// Arrow keys
	case 38: // VK_UP
		return cursorKey('A', mod, hasModifiers)
	case 40: // VK_DOWN
		return cursorKey('B', mod, hasModifiers)
	case 39: // VK_RIGHT
		return cursorKey('C', mod, hasModifiers)
	case 37: // VK_LEFT
		return cursorKey('D', mod, hasModifiers)

	// Navigation keys
	case 36: // VK_HOME
		return cursorKey('H', mod, hasModifiers)
	case 35: // VK_END
		return cursorKey('F', mod, hasModifiers)
	case 33: // VK_PRIOR (Page Up)
		return tildeKey(5, mod, hasModifiers)
	case 34: // VK_NEXT (Page Down)
		return tildeKey(6, mod, hasModifiers)
	case 45: // VK_INSERT
		return tildeKey(2, mod, hasModifiers)
	case 46: // VK_DELETE
		return tildeKey(3, mod, hasModifiers)

	// Function keys F1-F4
	case 112: // VK_F1
		return functionKey(1, 'P', mod, hasModifiers)
	case 113: // VK_F2
		return functionKey(2, 'Q', mod, hasModifiers)
	case 114: // VK_F3
		return functionKey(3, 'R', mod, hasModifiers)
	case 115: // VK_F4
		return functionKey(4, 'S', mod, hasModifiers)

	// Function keys F5-F12
	case 116: // VK_F5
		return tildeKey(15, mod, hasModifiers)
	case 117: // VK_F6
		return tildeKey(17, mod, hasModifiers)
	case 118: // VK_F7
		return tildeKey(18, mod, hasModifiers)
	case 119: // VK_F8
		return tildeKey(19, mod, hasModifiers)
	case 120: // VK_F9
		return tildeKey(20, mod, hasModifiers)
	case 121: // VK_F10
		return tildeKey(21, mod, hasModifiers)
	case 122: // VK_F11
		return tildeKey(23, mod, hasModifiers)
	case 123: // VK_F12
		return tildeKey(24, mod, hasModifiers)
	}
	return nil
}

// hardwareKeycodeToChar maps Windows Virtual Key codes to ASCII characters.
// This is used as a fallback when GDK can't translate keypresses (Wine/Windows).
// Windows VK codes for letters are 65-90 (A-Z), numbers are 48-57 (0-9).
func hardwareKeycodeToChar(hwcode uint16, shift bool) byte {
	// Letters A-Z: VK codes 65-90
	if hwcode >= 65 && hwcode <= 90 {
		if shift {
			return byte(hwcode) // 'A'-'Z'
		}
		return byte(hwcode + 32) // 'a'-'z'
	}

	// Numbers 0-9: VK codes 48-57
	if hwcode >= 48 && hwcode <= 57 {
		if shift {
			// Shifted number row symbols
			symbols := []byte{')', '!', '@', '#', '$', '%', '^', '&', '*', '('}
			return symbols[hwcode-48]
		}
		return byte(hwcode) // '0'-'9'
	}

	// Space
	if hwcode == 32 { // VK_SPACE
		return ' '
	}

	// OEM keys (symbols) - US keyboard layout
	type keyMapping struct {
		normal byte
		shift  byte
	}
	oemKeys := map[uint16]keyMapping{
		186: {';', ':'},  // VK_OEM_1
		187: {'=', '+'},  // VK_OEM_PLUS
		188: {',', '<'},  // VK_OEM_COMMA
		189: {'-', '_'},  // VK_OEM_MINUS
		190: {'.', '>'},  // VK_OEM_PERIOD
		191: {'/', '?'},  // VK_OEM_2
		192: {'`', '~'},  // VK_OEM_3
		219: {'[', '{'},  // VK_OEM_4
		220: {'\\', '|'}, // VK_OEM_5
		221: {']', '}'},  // VK_OEM_6
		222: {'\'', '"'}, // VK_OEM_7
	}

	if mapping, ok := oemKeys[hwcode]; ok {
		if shift {
			return mapping.shift
		}
		return mapping.normal
	}

	return 0
}

// macKeycodeToChar converts macOS hardware keycodes to ASCII characters
// On macOS, Option key produces composed characters (like ® for Option+R)
// We use hardware keycodes to get the base character for Alt/Meta sequences
func macKeycodeToChar(hwcode uint16, shift bool) byte {
	// macOS keycode to character mapping (US keyboard layout)
	// Letters - macOS keycodes are not sequential like Windows VK codes
	letterKeys := map[uint16]byte{
		0: 'a', 1: 's', 2: 'd', 3: 'f', 4: 'h', 5: 'g', 6: 'z', 7: 'x',
		8: 'c', 9: 'v', 11: 'b', 12: 'q', 13: 'w', 14: 'e', 15: 'r',
		16: 'y', 17: 't', 31: 'o', 32: 'u', 34: 'i', 35: 'p', 37: 'l',
		38: 'j', 40: 'k', 45: 'n', 46: 'm',
	}

	if ch, ok := letterKeys[hwcode]; ok {
		if shift {
			return ch - 32 // Convert to uppercase
		}
		return ch
	}

	// Number row
	numberKeys := map[uint16]struct {
		normal byte
		shift  byte
	}{
		18: {'1', '!'}, 19: {'2', '@'}, 20: {'3', '#'}, 21: {'4', '$'},
		23: {'5', '%'}, 22: {'6', '^'}, 26: {'7', '&'}, 28: {'8', '*'},
		25: {'9', '('}, 29: {'0', ')'},
	}

	if mapping, ok := numberKeys[hwcode]; ok {
		if shift {
			return mapping.shift
		}
		return mapping.normal
	}

	// Symbol keys
	symbolKeys := map[uint16]struct {
		normal byte
		shift  byte
	}{
		24: {'=', '+'}, 27: {'-', '_'}, 30: {']', '}'}, 33: {'[', '{'},
		39: {'\'', '"'}, 41: {';', ':'}, 42: {'\\', '|'}, 43: {',', '<'},
		44: {'/', '?'}, 47: {'.', '>'}, 50: {'`', '~'},
	}

	if mapping, ok := symbolKeys[hwcode]; ok {
		if shift {
			return mapping.shift
		}
		return mapping.normal
	}

	// Space
	if hwcode == 49 {
		return ' '
	}

	return 0
}

// isModifierKey returns true if the GDK keyval is a modifier key
// Modifier keys alone don't produce terminal output
func isModifierKey(keyval uint) bool {
	switch keyval {
	case gdk.KEY_Shift_L, gdk.KEY_Shift_R,
		gdk.KEY_Control_L, gdk.KEY_Control_R,
		gdk.KEY_Alt_L, gdk.KEY_Alt_R,
		gdk.KEY_Meta_L, gdk.KEY_Meta_R,
		gdk.KEY_Super_L, gdk.KEY_Super_R,
		gdk.KEY_Hyper_L, gdk.KEY_Hyper_R,
		gdk.KEY_Caps_Lock, gdk.KEY_Num_Lock, gdk.KEY_Scroll_Lock:
		return true
	}
	return false
}

// isModifierKeycode returns true if the hardware keycode is a Windows VK modifier key
// This catches modifier keys on Wine/Windows when GDK keyval detection fails
func isModifierKeycode(hwcode uint16) bool {
	switch hwcode {
	case 16, // VK_SHIFT
		17,     // VK_CONTROL
		18,     // VK_MENU (Alt)
		20,     // VK_CAPITAL (Caps Lock)
		91, 92, // VK_LWIN, VK_RWIN (Windows/Command keys)
		144,      // VK_NUMLOCK
		145,      // VK_SCROLL
		160, 161, // VK_LSHIFT, VK_RSHIFT
		162, 163, // VK_LCONTROL, VK_RCONTROL
		164, 165: // VK_LMENU, VK_RMENU (Left/Right Alt)
		return true
	}
	return false
}
