// pawgui-qt - Qt-based GUI for PawScript with custom terminal emulator
// Cross-platform: works on Linux, macOS, and Windows
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/mappu/miqt/qt"
	"github.com/phroun/pawscript"
	"github.com/phroun/pawscript/pkg/pawgui"
	"github.com/phroun/pawscript/pkg/purfecterm"
	purfectermqt "github.com/phroun/pawscript/pkg/purfecterm-qt"
)

var version = "dev" // set via -ldflags at build time

// Default font size constant (uses shared package value)
const defaultFontSize = pawgui.DefaultFontSize

const appName = "PawScript Launcher (Qt)"

// Global state
var (
	currentDir   string
	qtApp        *qt.QApplication
	mainWindow   *qt.QMainWindow
	fileList     *qt.QListWidget
	terminal     *purfectermqt.Terminal
	pathButton   *qt.QPushButton // Path selector button with dropdown menu
	pathMenu     *qt.QMenu       // Dropdown menu for path selection
	runButton    *qt.QPushButton
	browseButton *qt.QPushButton

	// Console I/O for PawScript
	consoleOutCh   *pawscript.StoredChannel
	consoleInCh    *pawscript.StoredChannel
	stdinReader    *io.PipeReader
	stdinWriter    *io.PipeWriter
	clearInputFunc func()
	flushFunc      func()
	scriptRunning  bool
	scriptMu       sync.Mutex

	// REPL for interactive mode
	consoleREPL *pawscript.REPL

	// Configuration
	appConfig    pawscript.PSLConfig
	configHelper *pawgui.ConfigHelper

	// Track actual applied theme (resolved from Auto if needed)
	appliedThemeIsDark bool

	// Launcher narrow strip (for multiple toolbar buttons)
	launcherNarrowStrip    *qt.QWidget        // The narrow strip container
	launcherMenuButton     *IconButton        // Hamburger button in path selector (when strip hidden)
	launcherStripMenuBtn   *IconButton        // Hamburger button in narrow strip (when strip visible)
	launcherWidePanel      *qt.QWidget        // The wide panel (file browser)
	launcherSplitter       *qt.QSplitter      // The main launcher splitter
	launcherRegisteredBtns []*QtToolbarButton // Additional registered buttons for launcher
	launcherMenu           *qt.QMenu          // Shared hamburger menu for launcher (used by both buttons)
	pendingToolbarUpdate   bool               // Flag to signal main thread to update toolbar
	splitterAdjusting      bool               // Flag to prevent recursive splitter callbacks
)

// QtToolbarButton represents a registered toolbar button for Qt
type QtToolbarButton struct {
	Icon    string      // Icon name or path
	Tooltip string      // Tooltip text
	OnClick func()      // Click handler
	Menu    *qt.QMenu   // Optional dropdown menu (if nil, OnClick is used)
	widget  *IconButton // The actual button widget
}

// QtWindowToolbarData holds per-window toolbar state for dummy_button command
type QtWindowToolbarData struct {
	strip          *qt.QWidget            // The narrow strip container
	menuButton     *IconButton            // The hamburger menu button
	registeredBtns []*QtToolbarButton     // Additional registered buttons
	terminal       *purfectermqt.Terminal // Terminal for Feed() calls
	updateFunc     func()                 // Function to update the strip's buttons
}

// Per-window toolbar data (keyed by PawScript instance or window)
var (
	qtToolbarDataByPS     = make(map[*pawscript.PawScript]*QtWindowToolbarData)
	qtToolbarDataByWindow = make(map[*qt.QMainWindow]*QtWindowToolbarData)
	qtToolbarDataMu       sync.Mutex
	launcherToolbarData   *QtWindowToolbarData   // Toolbar data for the launcher window
	pendingWindowUpdates  []*QtWindowToolbarData // Windows that need toolbar updates
	pendingWindowUpdateMu sync.Mutex
)

// Minimum widths for panel collapse behavior
const (
	minWidePanelWidth   = 196 // Minimum width before wide panel collapses
	minNarrowStripWidth = 48  // Minimum width to fit 40px toolbar buttons
)

// Embedded SVG icons (fill color is replaced at runtime based on theme)
const hamburgerIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <rect style="fill:{{FILL}};stroke:none" width="11.346176" height="2.25" x="0.70038122" y="0.8404575"/>
  <rect style="fill:{{FILL}};stroke:none" width="11.346176" height="2.25" x="0.70038122" y="5.3383746"/>
  <rect style="fill:{{FILL}};stroke:none" width="11.346176" height="2.25" x="0.70038122" y="9.8362913"/>
</svg>`

const starIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <path style="fill:{{FILL}};stroke:none" d="M 6.4849512,1.5761366 8.0478061,4.7428264 11.542456,5.250629 9.0137037,7.7155534 9.6106608,11.196082 6.484951,9.5527997 3.359241,11.196082 3.9561984,7.7155534 1.4274463,5.2506288 4.9220959,4.7428264 Z" transform="matrix(1.1757817,0,0,1.1757817,-1.274887,-1.2479333)"/>
</svg>`

const trashIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <g transform="translate(0,-0.42545335)">
    <path style="fill:none;stroke:{{FILL}};stroke-width:1.25;stroke-linecap:butt;stroke-linejoin:miter" d="M 1.737022,2.4884974 3.2171891,11.510245 H 9.4828113 L 10.962978,2.4884974 Z"/>
    <path style="fill:{{FILL}};stroke:{{FILL}};stroke-linecap:round;stroke-linejoin:round" d="M 1.3199,1.9156617 H 11.38 l 0.399747,1.3487906 H 0.92025 Z"/>
    <g style="stroke-width:1.37432" transform="matrix(0.9098144,0,0,0.90927138,0.51615218,0.22722416)">
      <path style="fill:none;stroke:{{FILL}};stroke-width:1.37432;stroke-linecap:butt;stroke-linejoin:miter" d="M 9.7179479,10.776284 2.2806355,3.3389676"/>
      <path style="fill:none;stroke:{{FILL}};stroke-width:1.37432;stroke-linecap:butt;stroke-linejoin:miter" d="M 2.8490844,10.909391 10.419365,3.3389676"/>
      <rect style="fill:none;stroke:{{FILL}};stroke-width:1.37432;stroke-linecap:round;stroke-linejoin:round" width="4.892848" height="4.892848" x="7.282187" y="-1.6980692" transform="rotate(45)"/>
    </g>
  </g>
</svg>`

const folderIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <path style="fill:{{FILL}};stroke-linecap:round;stroke-linejoin:round" d="M 1.9065339,1.7962728 C 1.5842088,1.7963101 1.2979459,2.0022696 1.1954661,2.3078695 L 0.5663737,3.8958863 c -0.0256507,0.07611 -0.038911,0.1558459 -0.0392741,0.2361613 v 6.7556604 c -1.4756e-4,0.414463 0.33587832,0.750489 0.75034179,0.750342 h 8.8397706 c 0.337275,-3.16e-4 0.632863,-0.225708 0.722436,-0.550871 l 1.588017,-5.7557214 c 0.02486,-0.092219 0.03187,-0.1883361 0.02067,-0.2831869 -0.01456,0.00205 -0.02923,0.00326 -0.04392,0.00362 H 4.0664185 L 2.4861532,10.456009 C 2.4269087,10.657739 2.2159526,10.773834 2.0138306,10.715942 1.8099565,10.657629 1.6924277,10.444594 1.7518311,10.241035 L 3.4121948,4.5617955 C 3.459762,4.3986948 3.609202,4.2865095 3.7790975,4.2863601 H 11.39672 V 4.1318476 C 11.396582,3.7175863 11.06064,3.3818756 10.646379,3.3820225 H 6.4021932 L 5.0642904,2.0203486 C 4.9232898,1.87690 4.7305827,1.7960934 4.5294393,1.7962728 Z"/>
</svg>`

const homeIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <g transform="translate(-0.00109499,1.0501825)">
    <path style="fill:{{FILL}};stroke:{{FILL}};stroke-linecap:round;stroke-linejoin:miter" d="M 3.2755313,6.4035176 6.3499999,3.7576843 9.4244685,6.4035176 V 9.8547301 H 8.4943104 7.7936896 c -0.050205,-0.7055517 0.050205,-3.5196369 0,-3.1534491 L 4.9063104,6.6797301 v 3.175 H 4.2056896 3.2755313 Z"/>
    <path style="fill:none;stroke:{{FILL}};stroke-width:1.5;stroke-linecap:butt;stroke-linejoin:miter" d="M 1.3781068,5.4138305 6.34781,1.2300618 11.317513,5.5125077"/>
  </g>
</svg>`

const uncheckedIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <rect style="fill:none;stroke:{{FILL}};stroke-width:1;stroke-linecap:round" width="10.104374" height="10.104374" x="1.2978133" y="1.2978133"/>
</svg>`

const checkedIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <rect style="fill:none;stroke:{{FILL}};stroke-width:1;stroke-linecap:round" width="10.104374" height="10.104374" x="1.2978133" y="1.2978133"/>
  <path style="fill:none;stroke:{{FILL}};stroke-width:2.25;stroke-linecap:round;stroke-linejoin:round" d="M 3.3162955,7.1623081 5.7369373,9.2379784 10.237921,3.5516806"/>
</svg>`

const folderUpIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <path style="fill:{{FILL}};stroke-linecap:round;stroke-linejoin:round" d="M 1.9063436 0.91028314 C 1.5840188 0.9103204 1.2977556 1.1162802 1.1952759 1.4218798 L 0.5663737 3.0098966 C 0.54072302 3.0860065 0.52746271 3.1657426 0.52709961 3.2460579 L 0.52709961 10.001718 C 0.52695205 10.41618 0.86297835 10.752207 1.2774414 10.75206 L 10.117212 10.75206 C 10.454487 10.751744 10.750076 10.526352 10.839648 10.201189 L 12.427665 4.4454679 C 12.452525 4.353249 12.459536 4.2571317 12.448336 4.162281 C 12.433776 4.164331 12.419101 4.1655384 12.404411 4.1658984 L 4.0664185 4.1658984 L 2.4861532 9.5702197 C 2.4269087 9.7719494 2.2159524 9.8880447 2.0138306 9.8301521 C 1.8099567 9.77184 1.6924277 9.5588044 1.7518311 9.3552457 L 3.4121948 3.6760058 C 3.459762 3.5129053 3.6092022 3.4007198 3.7790975 3.4005704 L 11.39672 3.4005704 L 11.39672 3.2460579 C 11.396582 2.831797 11.060639 2.4960859 10.646379 2.4962328 L 6.4021932 2.4962328 L 5.0642904 1.1345589 C 4.9232899 0.99111043 4.7305825 0.91030369 4.5294393 0.91028314 L 1.9063436 0.91028314 z M 4.7516479 4.8821337 L 9.3906413 5.0604174 L 7.7907389 6.6603198 L 10.041764 8.9113451 L 8.7808594 10.17225 L 6.529834 7.9212247 L 4.9304484 9.5206103 L 4.7516479 4.8821337 z"/>
</svg>`

const unknownFileIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <path style="fill:#ffffff;stroke:#002b36;stroke-width:0.75;stroke-linecap:round;stroke-linejoin:round" d="M 2.6458333,11.906249 V 0.79375 h 5.0270833 l 2.38125,2.38125 v 8.73125 z"/>
  <path style="fill:#ffffff;stroke:#002b36;stroke-width:0.75;stroke-linecap:round;stroke-linejoin:round" d="m 7.6729166,0.79375 v 2.38125 h 2.38125"/>
  <text style="font-size:7.05556px;fill:#77abbe;stroke:none;font-family:sans-serif;font-weight:bold" x="3.7041667" y="9.5249996">?</text>
</svg>`

const pawFileIconSVG = `<svg width="48" height="48" viewBox="0 0 12.7 12.7" xmlns="http://www.w3.org/2000/svg">
  <path style="fill:#ffffff;stroke:#002b36;stroke-width:0.75;stroke-linecap:round;stroke-linejoin:round" d="M 2.6910152,1.1822787 H 8.1260003 L 10.008985,3.0847273 V 11.517722 H 2.6910152 Z"/>
  <path style="fill:#ffffff;stroke:#002b36;stroke-width:0.5;stroke-linecap:butt;stroke-linejoin:miter" d="M 7.848,1.368 V 3.498 h 1.973"/>
  <rect style="fill:#268bd2;stroke:none" width="2.878" height="0.731" x="4.085" y="3.834"/>
  <rect style="fill:#268bd2;stroke:none" width="2.711" height="0.731" x="3.056" y="2.625"/>
  <rect style="fill:#268bd2;stroke:none" width="1.606" height="0.731" x="3.056" y="5.042"/>
  <path style="fill:#d33682" d="M 6.9877659,6.4940823 A 0.59432477,1.0177472 10.901417 0 0 7.363159,7.6235229 0.59432477,1.0177472 10.901417 0 0 8.1469646,6.7591416 0.59432477,1.0177472 10.901417 0 0 7.7715712,5.6297008 0.59432477,1.0177472 10.901417 0 0 6.9877659,6.4940823 Z M 8.0688455,7.6686372 A 0.58822118,0.84210657 24.692905 0 0 8.3152738,8.6391475 0.58822118,0.84210657 24.692905 0 0 9.1859233,8.0478374 0.58822118,0.84210657 24.692905 0 0 8.9394952,7.0773271 0.58822118,0.84210657 24.692905 0 0 8.0688455,7.6686372 Z M 5.3231631,7.5172962 A 0.80963169,0.55863957 74.019456 0 1 4.8344933,8.2706577 0.80963169,0.55863957 74.019456 0 1 4.1731169,7.4391573 0.80963169,0.55863957 74.019456 0 1 4.6617867,6.6857959 0.80963169,0.55863957 74.019456 0 1 5.3231631,7.5172962 Z M 6.582441,6.4764168 A 1.0177472,0.59432477 84.942216 0 1 6.0940057,7.561768 1.0177472,0.59432477 84.942216 0 1 5.4022794,6.6220762 1.0177472,0.59432477 84.942216 0 1 5.8907147,5.5367251 1.0177472,0.59432477 84.942216 0 1 6.582441,6.4764168 Z M 6.8071884,7.5727143 C 6.5623925,7.5505112 6.3191375,7.5972619 6.1140814,7.7369954 5.7508331,7.9845273 5.9422246,8.2677324 5.5915221,8.3848434 5.1536649,8.5000827 4.6876296,8.8060968 4.6364088,9.3673211 4.5797156,9.992466 5.0848467,10.492654 5.6678087,10.545828 c 0.5427322,0.06569 0.6863499,-0.436116 0.9458857,-0.395595 0.3134986,0.0427 0.274105,0.506502 0.7776369,0.552396 0.5829867,0.0529 1.1700891,-0.347918 1.2271409,-0.9730306 C 8.6693714,9.1683448 8.4256602,8.7681445 7.920995,8.6411957 7.5278107,8.4509588 7.7938464,8.1615864 7.4592698,7.859377 7.2751275,7.6930487 7.0519721,7.5950518 6.8071884,7.5727143 Z M 6.751527,8.1850563 A 0.42149629,0.32909713 5.1983035 0 0 6.3021075,8.4744839 0.42149629,0.32909713 5.1983035 0 0 6.6919181,8.8402625 0.42149629,0.32909713 5.1983035 0 0 7.1413376,8.5508348 0.42149629,0.32909713 5.1983035 0 0 6.751527,8.1850563 Z M 5.808412,9.0040512 A 0.52098234,0.46766435 5.1983035 0 0 5.2473463,9.4224819 0.52098234,0.46766435 5.1983035 0 0 5.7236902,9.9352932 0.52098234,0.46766435 5.1983035 0 0 6.2847559,9.5168626 0.52098234,0.46766435 5.1983035 0 0 5.808412,9.0040512 Z M 7.5313614,9.1608004 A 0.52098234,0.46766435 5.1983035 0 0 6.9702956,9.5792311 0.52098234,0.46766435 5.1983035 0 0 7.4466396,10.092042 0.52098234,0.46766435 5.1983035 0 0 8.0077053,9.6736118 0.52098234,0.46766435 5.1983035 0 0 7.5313614,9.1608004 Z"/>
</svg>`

// getIconFillColor returns the appropriate icon fill color based on applied theme
func getIconFillColor() string {
	if appliedThemeIsDark {
		return "#ffffff"
	}
	return "#000000"
}

// getSVGIcon returns SVG data with the fill color set appropriately for current theme
func getSVGIcon(svgTemplate string) string {
	return strings.Replace(svgTemplate, "{{FILL}}", getIconFillColor(), -1)
}

// getDarkSVGIcon returns SVG data with the dark mode fill color (white) for selected rows
func getDarkSVGIcon(svgTemplate string) string {
	return strings.Replace(svgTemplate, "{{FILL}}", "#ffffff", -1)
}

// createDarkIconFromSVG creates a QIcon with dark mode fill color at the specified size
func createDarkIconFromSVG(svgTemplate string, size int) *qt.QIcon {
	svgData := getDarkSVGIcon(svgTemplate)
	pixmap := createPixmapFromSVG(svgData, size)
	if pixmap != nil {
		icon := qt.NewQIcon()
		// Add pixmap for all modes to prevent Qt from auto-generating modified versions
		icon.AddPixmap2(pixmap, qt.QIcon__Normal)
		icon.AddPixmap2(pixmap, qt.QIcon__Selected)
		icon.AddPixmap2(pixmap, qt.QIcon__Active)
		return icon
	}
	return nil
}

// resizeSVG modifies the width and height attributes in the root <svg> tag only
// This allows Qt to render the vector directly at the target size
func resizeSVG(svgData string, size int) string {
	sizeStr := fmt.Sprintf("%d", size)

	// Find the end of the opening <svg ...> tag
	endIdx := strings.Index(svgData, ">")
	if endIdx == -1 {
		return svgData
	}

	// Split into svg tag and rest of content
	svgTag := svgData[:endIdx+1]
	rest := svgData[endIdx+1:]

	// Replace width and height only in the opening svg tag
	svgTag = regexp.MustCompile(`width="[^"]*"`).ReplaceAllString(svgTag, `width="`+sizeStr+`"`)
	svgTag = regexp.MustCompile(`height="[^"]*"`).ReplaceAllString(svgTag, `height="`+sizeStr+`"`)

	return svgTag + rest
}

// createPixmapFromSVG creates a QPixmap from SVG data at the specified size
func createPixmapFromSVG(svgData string, size int) *qt.QPixmap {
	// Resize SVG to target size before loading - this lets the vector renderer
	// rasterize directly at the correct size, avoiding bitmap scaling artifacts
	resizedSVG := resizeSVG(svgData, size)
	pixmap := qt.NewQPixmap()
	data := []byte(resizedSVG)
	if pixmap.LoadFromData(unsafe.SliceData(data), uint(len(data))) {
		return pixmap
	}
	return nil
}

// createIconFromSVG creates a QIcon from SVG template at the specified size
func createIconFromSVG(svgTemplate string, size int) *qt.QIcon {
	svgData := getSVGIcon(svgTemplate)
	pixmap := createPixmapFromSVG(svgData, size)
	if pixmap != nil {
		icon := qt.NewQIcon()
		// Add pixmap for all modes to prevent Qt from auto-generating modified versions
		icon.AddPixmap2(pixmap, qt.QIcon__Normal)
		icon.AddPixmap2(pixmap, qt.QIcon__Selected)
		icon.AddPixmap2(pixmap, qt.QIcon__Active)
		return icon
	}
	return nil
}

// IconButton is a custom widget that draws an icon centered with proper padding
type IconButton struct {
	*qt.QWidget
	pixmap    *qt.QPixmap
	onClick   func()
	tooltip   string
	isHovered bool
	isPressed bool
}

// NewIconButton creates a new icon button with the given size and icon
func NewIconButton(buttonSize, iconSize int, svgData string) *IconButton {
	widget := qt.NewQWidget2()

	btn := &IconButton{
		QWidget: widget,
		pixmap:  createPixmapFromSVG(svgData, iconSize),
	}

	// Set fixed size
	widget.SetMinimumSize2(buttonSize, buttonSize)
	widget.SetMaximumSize2(buttonSize, buttonSize)

	// Enable mouse tracking for hover effects
	widget.SetMouseTracking(true)

	// Override paint event
	widget.OnPaintEvent(func(super func(event *qt.QPaintEvent), event *qt.QPaintEvent) {
		btn.paintEvent(event)
	})

	// Override mouse events
	widget.OnMousePressEvent(func(super func(event *qt.QMouseEvent), event *qt.QMouseEvent) {
		btn.isPressed = true
		widget.Update()
	})

	widget.OnMouseReleaseEvent(func(super func(event *qt.QMouseEvent), event *qt.QMouseEvent) {
		if btn.isPressed && btn.onClick != nil {
			btn.onClick()
		}
		btn.isPressed = false
		widget.Update()
	})

	widget.OnEnterEvent(func(super func(event *qt.QEvent), event *qt.QEvent) {
		btn.isHovered = true
		widget.Update()
	})

	widget.OnLeaveEvent(func(super func(event *qt.QEvent), event *qt.QEvent) {
		btn.isHovered = false
		btn.isPressed = false
		widget.Update()
	})

	// Clear hover state when widget is hidden (e.g., by a popup menu)
	widget.OnHideEvent(func(super func(event *qt.QHideEvent), event *qt.QHideEvent) {
		btn.isHovered = false
		btn.isPressed = false
		super(event)
	})

	// Clear hover state on any event if mouse is no longer over the button
	widget.OnEvent(func(super func(event *qt.QEvent) bool, event *qt.QEvent) bool {
		if btn.isHovered && !widget.UnderMouse() {
			btn.isHovered = false
			btn.isPressed = false
			widget.Update()
		}
		return super(event)
	})

	return btn
}

func (btn *IconButton) paintEvent(event *qt.QPaintEvent) {
	painter := qt.NewQPainter2(btn.QWidget.QPaintDevice)
	defer painter.End()

	// Verify hover state matches reality (in case leave event was missed)
	actuallyHovered := btn.QWidget.UnderMouse()
	if btn.isHovered && !actuallyHovered {
		btn.isHovered = false
		btn.isPressed = false
	}

	// Get widget dimensions
	w := btn.Width()
	h := btn.Height()

	// Draw button background based on state
	if btn.isPressed && actuallyHovered {
		bgColor := qt.NewQColor3(128, 128, 128)
		bgColor.SetAlpha(80)
		painter.FillRect5(0, 0, w, h, bgColor)
	} else if btn.isHovered && actuallyHovered {
		bgColor := qt.NewQColor3(128, 128, 128)
		bgColor.SetAlpha(40)
		painter.FillRect5(0, 0, w, h, bgColor)
	}

	// Draw the icon centered
	if btn.pixmap != nil && !btn.pixmap.IsNull() {
		iconW := btn.pixmap.Width()
		iconH := btn.pixmap.Height()
		x := (w - iconW) / 2
		y := (h - iconH) / 2
		painter.DrawPixmap9(x, y, btn.pixmap)
	}
}

func (btn *IconButton) SetOnClick(callback func()) {
	btn.onClick = callback
}

func (btn *IconButton) SetToolTip(tip string) {
	btn.tooltip = tip
	btn.QWidget.SetToolTip(tip)
}

func (btn *IconButton) UpdateIcon(svgData string, iconSize int) {
	btn.pixmap = createPixmapFromSVG(svgData, iconSize)
	btn.QWidget.Update()
}

// Random icons for dummy buttons
var dummyIcons = []string{"★", "♦", "♠", "♣", "♥", "●", "■", "▲", "◆", "⬟", "⬢", "✦", "✧", "⚡", "☀", "☁", "☂", "☃", "✿", "❀"}

// --- Configuration Management ---

func getConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".paw")
}

func getConfigPath() string {
	configDir := getConfigDir()
	if configDir == "" {
		return ""
	}
	return filepath.Join(configDir, "pawgui-qt.psl")
}

func loadConfig() pawscript.PSLConfig {
	configPath := getConfigPath()
	if configPath == "" {
		return pawscript.PSLConfig{}
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return pawscript.PSLConfig{}
	}

	config, err := pawscript.ParsePSL(string(data))
	if err != nil {
		return pawscript.PSLConfig{}
	}

	return config
}

func saveConfig(config pawscript.PSLConfig) {
	configPath := getConfigPath()
	if configPath == "" {
		return
	}

	configDir := getConfigDir()
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return
	}

	data := pawscript.SerializePSLPretty(config)
	_ = os.WriteFile(configPath, []byte(data+"\n"), 0644)
}

func saveBrowseDir(dir string) {
	appConfig.Set("last_browse_dir", dir)
	saveConfig(appConfig)
}

// Configuration getter wrappers using shared configHelper
func getFontFamily() string                      { return configHelper.GetFontFamily() }
func getFontFamilyUnicode() string               { return configHelper.GetFontFamilyUnicode() }
func getFontFamilyCJK() string                   { return configHelper.GetFontFamilyCJK() }
func getFontSize() int                           { return configHelper.GetFontSize() }
func getUIScale() float64                        { return configHelper.GetUIScale() }
func getOptimizationLevel() int                  { return configHelper.GetOptimizationLevel() }
func getTerminalBackground() purfecterm.Color    { return configHelper.GetTerminalBackground() }
func getTerminalForeground() purfecterm.Color    { return configHelper.GetTerminalForeground() }
func getColorPalette() []purfecterm.Color        { return configHelper.GetColorPalette() }
func getBlinkMode() purfecterm.BlinkMode         { return configHelper.GetBlinkMode() }
func getQuitShortcut() string                    { return configHelper.GetQuitShortcut() }
func getDefaultQuitShortcut() string             { return pawgui.GetDefaultQuitShortcut() }
func getPSLColors() pawscript.DisplayColorConfig { return configHelper.GetPSLColors() }
func isTermThemeDark() bool                      { return configHelper.IsTermThemeDark() }

func getColorSchemeForTheme(isDark bool) purfecterm.ColorScheme {
	return configHelper.GetColorSchemeForTheme(isDark)
}

func showCopyright() {
	fmt.Fprintf(os.Stderr, "pawgui-qt, the PawScript GUI interpreter version %s (with Qt)\nCopyright (c) 2025 Jeffrey R. Day\nLicense: MIT\n", version)
}

func showLicense() {
	showCopyright()
	license := `
MIT License

Copyright (c) 2025 Jeffrey R. Day

Permission is hereby granted, free of charge, to any person
obtaining a copy of this software and associated documentation
files (the "Software"), to deal in the Software without
restriction, including without limitation the rights to use,
copy, modify, merge, publish, distribute, sublicense, and/or
sell copies of the Software, and to permit persons to whom the
Software is furnished to do so, subject to the following
conditions:

The above copyright notice and this permission notice
(including the next paragraph) shall be included in all copies
or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES
OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND
NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT
HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY,
WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING
FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
OTHER DEALINGS IN THE SOFTWARE.
`
	fmt.Fprint(os.Stdout, license)
}

func showUsage() {
	showCopyright()
	usage := `
Usage: pawgui-qt [options] [script.paw] [-- args...]
       pawgui-qt [options] < input.paw
       echo "commands" | pawgui-qt [options]

Execute PawScript with GUI capabilities from a file, stdin, or pipe.

Options:
  --version           Show version and exit
  --license           View license and exit
  -d, --debug         Enable debug output
  -v, --verbose       Enable verbose output (same as --debug)
  -O N                Set optimization level (0=no caching, 1=cache macro/loop bodies, default: 1)
  --unrestricted      Disable all file/exec access restrictions
  --sandbox DIR       Restrict all access to DIR only
  --read-roots DIRS   Additional directories for reading
  --write-roots DIRS  Additional directories for writing
  --exec-roots DIRS   Additional directories for exec command

GUI Options:
  --window            Create console window for stdout/stdin/stderr

Arguments:
  script.paw          Script file to execute (adds .paw extension if needed)
  --                  Separates script filename from arguments

Default Security Sandbox:
  Read:   SCRIPT_DIR, CWD, /tmp
  Write:  SCRIPT_DIR/saves, SCRIPT_DIR/output, CWD/saves, CWD/output, /tmp
  Exec:   SCRIPT_DIR/helpers, SCRIPT_DIR/bin

Environment Variables (use SCRIPT_DIR as placeholder):
  PAW_READ_ROOTS      Override default read roots
  PAW_WRITE_ROOTS     Override default write roots
  PAW_EXEC_ROOTS      Override default exec roots
`
	fmt.Fprint(os.Stderr, usage)
}

// findScriptFile looks for a script file, adding .paw extension if needed
func findScriptFile(requestedFile string) string {
	// Try exact path first
	if _, err := os.Stat(requestedFile); err == nil {
		return requestedFile
	}

	// If no extension, try adding .paw
	if !strings.Contains(filepath.Base(requestedFile), ".") {
		pawFile := requestedFile + ".paw"
		if _, err := os.Stat(pawFile); err == nil {
			return pawFile
		}
	}

	return ""
}

// getLauncherWidth returns the saved launcher panel width, defaulting to 280
func getLauncherWidth() int {
	return appConfig.GetInt("launcher_width", 280)
}

// saveLauncherWidth saves the launcher panel width to config
func saveLauncherWidth(width int) {
	appConfig.Set("launcher_width", width)
	saveConfig(appConfig)
}

// getLauncherPosition returns the saved launcher window position (x, y)
func getLauncherPosition() (int, int) {
	if items := appConfig.GetItems("launcher_position"); len(items) >= 2 {
		x := pslToInt(items[0])
		y := pslToInt(items[1])
		return x, y
	}
	return -1, -1 // -1 means not set (let window manager decide)
}

// saveLauncherPosition saves the launcher window position to config
func saveLauncherPosition(x, y int) {
	appConfig.Set("launcher_position", []interface{}{x, y})
	saveConfig(appConfig)
}

// getLauncherSize returns the saved launcher window size (width, height)
func getLauncherSize() (int, int) {
	if items := appConfig.GetItems("launcher_size"); len(items) >= 2 {
		w := pslToInt(items[0])
		h := pslToInt(items[1])
		if w > 0 && h > 0 {
			return w, h
		}
	}
	return 1100, 700 // Default size
}

// saveLauncherSize saves the launcher window size to config
func saveLauncherSize(width, height int) {
	appConfig.Set("launcher_size", []interface{}{width, height})
	saveConfig(appConfig)
}

// pslToInt converts a PSL list item to int
func pslToInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// getHomeDir returns the user's home directory path
func getHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return ""
}

// getExamplesDir returns the examples directory path if it exists
func getExamplesDir() string {
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		examples := filepath.Join(exeDir, "examples")
		if info, err := os.Stat(examples); err == nil && info.IsDir() {
			return examples
		}
	}
	return ""
}

// getRecentPaths returns the list of recent paths from config (max 10)
func getRecentPaths() []string {
	if appConfig == nil {
		return nil
	}
	if paths, ok := appConfig["launcher_recent_paths"]; ok {
		if list, ok := paths.(pawscript.PSLList); ok {
			result := make([]string, 0, len(list))
			for _, p := range list {
				if s, ok := p.(string); ok && s != "" {
					result = append(result, s)
				}
			}
			return result
		}
	}
	return nil
}

// addRecentPath adds a path to the recent paths list (keeps max 10, no duplicates)
func addRecentPath(path string) {
	if appConfig == nil || path == "" {
		return
	}
	// Don't add home or examples to recent
	if path == getHomeDir() || path == getExamplesDir() {
		return
	}

	paths := getRecentPaths()

	// Remove if already exists
	newPaths := make([]string, 0, 10)
	for _, p := range paths {
		if p != path {
			newPaths = append(newPaths, p)
		}
	}

	// Add at front
	newPaths = append([]string{path}, newPaths...)

	// Keep max 10
	if len(newPaths) > 10 {
		newPaths = newPaths[:10]
	}

	// Convert to PSLList and save
	pslList := make(pawscript.PSLList, len(newPaths))
	for i, p := range newPaths {
		pslList[i] = p
	}
	appConfig.Set("launcher_recent_paths", pslList)
	saveConfig(appConfig)
}

// clearRecentPaths removes all recent paths from config
func clearRecentPaths() {
	if appConfig == nil {
		return
	}
	delete(appConfig, "launcher_recent_paths")
	saveConfig(appConfig)
}

// --- Toolbar Strip and Hamburger Menu ---

// showAboutDialog displays the About PawScript dialog
func showAboutDialog(parent *qt.QWidget) {
	aboutText := fmt.Sprintf(`<h2>PawScript</h2>
<p>Version: %s</p>
<p><i>A scripting language for creative coding</i></p>
<p>Copyright © 2025 Jeffrey R. Day<br>
License: MIT</p>`, version)

	qt.QMessageBox_About(parent, "About PawScript", aboutText)
}

// QtSettingsComboMenu represents a styled combo menu for settings dialogs using QPushButton + QMenu
type QtSettingsComboMenu struct {
	Button   *qt.QPushButton
	Menu     *qt.QMenu
	actions  []*qt.QAction
	options  []string
	selected int
	onChange func(int)
}

// createQtSettingsComboMenu creates a styled combo menu with check icon for selected item
func createQtSettingsComboMenu(options []string, selected int, onChange func(int)) *QtSettingsComboMenu {
	combo := &QtSettingsComboMenu{
		options:  options,
		selected: selected,
		onChange: onChange,
	}

	// Create button that shows current selection
	combo.Button = qt.NewQPushButton3(options[selected])
	combo.Button.SetMinimumWidth(150)

	// Create menu for dropdown
	combo.Menu = qt.NewQMenu2()

	// Create actions with icon for selected item
	combo.actions = make([]*qt.QAction, len(options))
	for i, option := range options {
		idx := i // Capture for closure
		action := combo.Menu.AddAction(option)
		// Set check icon only on the selected item
		if i == selected {
			if icon := createIconFromSVG(checkedIconSVG, 16); icon != nil {
				action.SetIcon(icon)
			}
		}
		action.OnTriggered(func() {
			combo.SetSelected(idx)
			if combo.onChange != nil {
				combo.onChange(idx)
			}
		})
		combo.actions[i] = action
	}

	// Show menu when button clicked
	combo.Button.OnClicked(func() {
		combo.Menu.Popup(combo.Button.MapToGlobal(combo.Button.Rect().BottomLeft()))
	})

	return combo
}

// SetSelected updates the selected item in the combo menu
func (c *QtSettingsComboMenu) SetSelected(idx int) {
	if idx < 0 || idx >= len(c.options) {
		return
	}

	// Remove icon from old selection
	if c.selected >= 0 && c.selected < len(c.actions) {
		c.actions[c.selected].SetIcon(qt.NewQIcon())
	}

	c.selected = idx

	// Update button text
	c.Button.SetText(c.options[idx])

	// Set check icon on new selection
	if icon := createIconFromSVG(checkedIconSVG, 16); icon != nil {
		c.actions[idx].SetIcon(icon)
	}
}

// GetSelected returns the currently selected index
func (c *QtSettingsComboMenu) GetSelected() int {
	return c.selected
}

// RefreshIcons updates the selected item's icon to match the current theme
func (c *QtSettingsComboMenu) RefreshIcons() {
	if c.selected >= 0 && c.selected < len(c.actions) {
		if icon := createIconFromSVG(checkedIconSVG, 16); icon != nil {
			c.actions[c.selected].SetIcon(icon)
		}
	}
}

// showSettingsDialog displays the Settings dialog with tabbed interface
func showSettingsDialog(parent *qt.QWidget) {
	// Save original values for reverting on Cancel
	origWindowTheme := appConfig.GetString("theme", "auto")
	origTermTheme := appConfig.GetString("term_theme", "auto")
	origUIScale := appConfig.GetFloat("ui_scale", 1.0)
	origFontFamily := appConfig.GetString("font_family", "")
	origFontSize := appConfig.GetInt("font_size", pawgui.DefaultFontSize)
	origFontFamilyUnicode := appConfig.GetString("font_family_unicode", "")

	// Create dialog
	dialog := qt.NewQDialog2()
	dialog.SetWindowTitle("Settings")
	dialog.SetMinimumSize2(400, 300)
	dialog.SetModal(true)

	// Main layout
	mainLayout := qt.NewQVBoxLayout2()
	mainLayout.SetContentsMargins(12, 12, 12, 12)
	mainLayout.SetSpacing(12)
	dialog.SetLayout(mainLayout.QLayout)

	// Create tab widget
	tabWidget := qt.NewQTabWidget2()
	mainLayout.AddWidget(tabWidget.QWidget)

	// --- Appearance Tab ---
	appearanceWidget := qt.NewQWidget2()
	appearanceLayout := qt.NewQFormLayout2()
	appearanceLayout.SetContentsMargins(12, 12, 12, 12)
	appearanceLayout.SetSpacing(12)
	appearanceWidget.SetLayout(appearanceLayout.QLayout)

	// Window Theme combo - determine initial selection
	var windowThemeSelected int
	switch configHelper.GetTheme() {
	case pawgui.ThemeLight:
		windowThemeSelected = 1
	case pawgui.ThemeDark:
		windowThemeSelected = 2
	default:
		windowThemeSelected = 0 // Auto
	}

	// Declare both combos so they can reference each other for icon refresh
	var windowThemeCombo, consoleThemeCombo *QtSettingsComboMenu

	windowThemeCombo = createQtSettingsComboMenu([]string{"Auto", "Light", "Dark"}, windowThemeSelected, func(idx int) {
		switch idx {
		case 1:
			appConfig.Set("theme", "light")
		case 2:
			appConfig.Set("theme", "dark")
		default:
			appConfig.Set("theme", "auto")
		}
		configHelper = pawgui.NewConfigHelper(appConfig)
		applyTheme(configHelper.GetTheme())
		// Refresh icons in both combos to match new theme
		windowThemeCombo.RefreshIcons()
		if consoleThemeCombo != nil {
			consoleThemeCombo.RefreshIcons()
		}
	})
	appearanceLayout.AddRow3("Window Theme:", windowThemeCombo.Button.QWidget)

	// Window Scale - use QDoubleSpinBox for precise control
	currentScale := configHelper.GetUIScale()
	minScale := 0.5
	maxScale := 3.0
	// Extend range if current value is outside normal bounds
	if currentScale < minScale {
		minScale = currentScale
	}
	if currentScale > maxScale {
		maxScale = currentScale
	}

	// Create horizontal layout for slider and value label
	scaleLayout := qt.NewQHBoxLayout2()
	scaleLayout.SetContentsMargins(0, 0, 0, 0)

	// QSlider uses integers, so scale by 10 (0.5 -> 5, 3.0 -> 30)
	windowScaleSlider := qt.NewQSlider2()
	windowScaleSlider.SetOrientation(qt.Horizontal)
	windowScaleSlider.SetRange(int(minScale*10), int(maxScale*10))
	windowScaleSlider.SetSingleStep(1)
	windowScaleSlider.SetValue(int(currentScale * 10))
	windowScaleSlider.SetTickPosition(qt.QSlider__TicksBelow)
	windowScaleSlider.SetTickInterval(5) // Tick every 0.5

	// Label to show current value
	scaleValueLabel := qt.NewQLabel3(fmt.Sprintf("%.1f", currentScale))
	scaleValueLabel.SetMinimumWidth(30)

	// Update config and label while dragging
	windowScaleSlider.OnValueChanged(func(value int) {
		scaledValue := float64(value) / 10.0
		appConfig.Set("ui_scale", scaledValue)
		configHelper = pawgui.NewConfigHelper(appConfig)
		scaleValueLabel.SetText(fmt.Sprintf("%.1f", scaledValue))
	})
	// Apply visual changes only when slider is released
	windowScaleSlider.OnSliderReleased(func() {
		applyUIScaleFromConfig()
	})

	scaleLayout.AddWidget(windowScaleSlider.QWidget)
	scaleLayout.AddWidget(scaleValueLabel.QWidget)

	scaleWidget := qt.NewQWidget2()
	scaleWidget.SetLayout(scaleLayout.QLayout)
	appearanceLayout.AddRow3("Window Scale:", scaleWidget)

	// Console Theme combo - determine initial selection
	var consoleThemeSelected int
	termTheme := appConfig.GetString("term_theme", "auto")
	switch termTheme {
	case "light":
		consoleThemeSelected = 1
	case "dark":
		consoleThemeSelected = 2
	default:
		consoleThemeSelected = 0 // Auto
	}

	consoleThemeCombo = createQtSettingsComboMenu([]string{"Auto", "Light", "Dark"}, consoleThemeSelected, func(idx int) {
		switch idx {
		case 1:
			appConfig.Set("term_theme", "light")
		case 2:
			appConfig.Set("term_theme", "dark")
		default:
			appConfig.Set("term_theme", "auto")
		}
		configHelper = pawgui.NewConfigHelper(appConfig)
		applyConsoleTheme()
	})
	appearanceLayout.AddRow3("Console Theme:", consoleThemeCombo.Button.QWidget)

	// Console Font - button that opens font dialog
	currentFontFamily := configHelper.GetFontFamily()
	currentFontSize := configHelper.GetFontSize()
	// Extract just the first font from the comma-separated list
	firstFont := currentFontFamily
	if idx := strings.Index(currentFontFamily, ","); idx != -1 {
		firstFont = strings.TrimSpace(currentFontFamily[:idx])
	}

	consoleFontButton := qt.NewQPushButton3(fmt.Sprintf("%s, %dpt", firstFont, currentFontSize))
	consoleFontButton.OnClicked(func() {
		// Create initial font from current settings
		initialFont := qt.NewQFont2(firstFont)
		initialFont.SetPointSize(currentFontSize)

		ok := false
		selectedFont := qt.QFontDialog_GetFont2(&ok, initialFont)
		if ok && selectedFont != nil {
			newFamily := selectedFont.Family()
			newSize := selectedFont.PointSize()

			// Update font_size
			appConfig.Set("font_size", newSize)

			// Preserve fallback fonts from original font_family
			origFamily := appConfig.GetString("font_family", "")
			if idx := strings.Index(origFamily, ","); idx != -1 {
				newFamily = newFamily + origFamily[idx:]
			}
			appConfig.Set("font_family", newFamily)
			configHelper = pawgui.NewConfigHelper(appConfig)
			applyFontSettings()

			// Update button text
			consoleFontButton.SetText(fmt.Sprintf("%s, %dpt", selectedFont.Family(), newSize))
		}
	})
	appearanceLayout.AddRow3("Console Font:", consoleFontButton.QWidget)

	// CJK Font - button that opens font dialog (size ignored)
	currentCJKFamily := appConfig.GetString("font_family_unicode", "")
	if currentCJKFamily == "" {
		currentCJKFamily = pawgui.GetDefaultUnicodeFont()
	}
	firstCJKFont := currentCJKFamily
	if idx := strings.Index(currentCJKFamily, ","); idx != -1 {
		firstCJKFont = strings.TrimSpace(currentCJKFamily[:idx])
	}

	cjkFontButton := qt.NewQPushButton3(firstCJKFont)
	cjkFontButton.OnClicked(func() {
		// Create initial font from current CJK font setting
		initialFont := qt.NewQFont2(firstCJKFont)
		initialFont.SetPointSize(currentFontSize) // Use console font size for display

		ok := false
		selectedFont := qt.QFontDialog_GetFont2(&ok, initialFont)
		if ok && selectedFont != nil {
			newFamily := selectedFont.Family()
			// Size is ignored for CJK font

			// Preserve fallback fonts from original font_family_unicode
			origFamily := appConfig.GetString("font_family_unicode", "")
			if idx := strings.Index(origFamily, ","); idx != -1 {
				newFamily = newFamily + origFamily[idx:]
			}
			appConfig.Set("font_family_unicode", newFamily)
			configHelper = pawgui.NewConfigHelper(appConfig)
			applyFontSettings()

			// Update button text (show family only, no size)
			cjkFontButton.SetText(selectedFont.Family())
		}
	})
	appearanceLayout.AddRow3("CJK Font:", cjkFontButton.QWidget)

	tabWidget.AddTab(appearanceWidget, "Appearance")

	// --- Button Box ---
	buttonLayout := qt.NewQHBoxLayout2()
	buttonLayout.AddStretch()

	cancelBtn := qt.NewQPushButton3("Cancel")
	cancelBtn.OnClicked(func() {
		dialog.Reject()
	})
	buttonLayout.AddWidget(cancelBtn.QWidget)

	saveBtn := qt.NewQPushButton3("Save")
	saveBtn.SetDefault(true)
	saveBtn.OnClicked(func() {
		dialog.Accept()
	})
	buttonLayout.AddWidget(saveBtn.QWidget)

	mainLayout.AddLayout(buttonLayout.QLayout)

	// Show dialog and handle response
	if dialog.Exec() == 1 { // QDialog::Accepted = 1
		// Save config to file (settings already applied via change handlers)
		saveConfig(appConfig)
	} else {
		// Revert to original values on Cancel
		appConfig.Set("theme", origWindowTheme)
		appConfig.Set("term_theme", origTermTheme)
		appConfig.Set("ui_scale", origUIScale)
		if origFontFamily != "" {
			appConfig.Set("font_family", origFontFamily)
		}
		appConfig.Set("font_size", origFontSize)
		if origFontFamilyUnicode != "" {
			appConfig.Set("font_family_unicode", origFontFamilyUnicode)
		}
		configHelper = pawgui.NewConfigHelper(appConfig)
		applyTheme(configHelper.GetTheme())
		applyConsoleTheme()
		applyUIScaleFromConfig()
		applyFontSettings()
	}

	dialog.DeleteLater()
}

// applyConsoleTheme applies the console theme to all terminals
func applyConsoleTheme() {
	isDark := isTermThemeDark()
	scheme := getColorSchemeForTheme(isDark)

	// Apply to launcher terminal
	if terminal != nil {
		terminal.Buffer().SetPreferredDarkTheme(isDark)
		terminal.Buffer().SetDarkTheme(isDark)
		terminal.SetColorScheme(scheme)
	}
}

// applyFontSettings applies font settings to all open terminals
func applyFontSettings() {
	fontFamily := configHelper.GetFontFamily()
	fontSize := configHelper.GetFontSize()
	unicodeFont := getFontFamilyUnicode()
	cjkFont := getFontFamilyCJK()

	// Update main launcher terminal
	if terminal != nil {
		terminal.SetFont(fontFamily, fontSize)
		terminal.SetFontFallbacks(unicodeFont, cjkFont)
	}

	// Update all script window terminals
	qtToolbarDataMu.Lock()
	for _, data := range qtToolbarDataByWindow {
		if data.terminal != nil {
			data.terminal.SetFont(fontFamily, fontSize)
			data.terminal.SetFontFallbacks(unicodeFont, cjkFont)
		}
	}
	for _, data := range qtToolbarDataByPS {
		if data.terminal != nil {
			data.terminal.SetFont(fontFamily, fontSize)
			data.terminal.SetFontFallbacks(unicodeFont, cjkFont)
		}
	}
	qtToolbarDataMu.Unlock()
}

// applyUIScaleFromConfig applies the current UI scale from config
func applyUIScaleFromConfig() {
	applyUIScale(getUIScale())
}

// createHamburgerMenu creates the hamburger dropdown menu
// isScriptWindow: true for script windows (slightly different options)
// term: terminal widget for this window (nil to use global terminal)
// isScriptRunningFunc: returns true if a script is running in this window
// closeWindowFunc: closes this window
func createHamburgerMenu(parent *qt.QWidget, isScriptWindow bool, term *purfectermqt.Terminal, isScriptRunningFunc func() bool, closeWindowFunc func()) *qt.QMenu {
	menu := qt.NewQMenu2()

	// Helper to get the terminal (uses provided term or falls back to global)
	getTerminal := func() *purfectermqt.Terminal {
		if term != nil {
			return term
		}
		return terminal
	}

	// About option (both)
	aboutAction := menu.AddAction("About PawScript...")
	aboutAction.OnTriggered(func() {
		showAboutDialog(parent)
	})

	// Settings option (both)
	settingsAction := menu.AddAction("Settings...")
	settingsAction.OnTriggered(func() {
		showSettingsDialog(parent)
	})

	// Separator after About/Settings
	menu.AddSeparator()

	// File List toggle with custom icon (launcher only)
	var fileListAction *qt.QAction
	if !isScriptWindow {
		fileListAction = menu.AddAction("File List")
		// Set initial icon based on current state
		if isWideMode() {
			if icon := createIconFromSVG(checkedIconSVG, 16); icon != nil {
				fileListAction.SetIcon(icon)
			}
		} else {
			if icon := createIconFromSVG(uncheckedIconSVG, 16); icon != nil {
				fileListAction.SetIcon(icon)
			}
		}
		fileListAction.OnTriggered(func() {
			toggleFileList()
		})
	}

	// Show Launcher (console windows only)
	if isScriptWindow {
		showLauncherAction := menu.AddAction("Show Launcher")
		showLauncherAction.OnTriggered(func() {
			showOrCreateLauncher()
		})
	}

	// New Window (both - creates a blank console window)
	newWindowAction := menu.AddAction("New Window")
	newWindowAction.OnTriggered(func() {
		createBlankConsoleWindow()
	})

	menu.AddSeparator()

	// Stop Script (both) - disabled when no script running
	stopScriptAction := menu.AddAction("Stop Script")
	stopScriptAction.SetEnabled(false) // Initially disabled

	// Reset Terminal (both) - directly under Stop Script
	resetTerminalAction := menu.AddAction("Reset Terminal")
	resetTerminalAction.OnTriggered(func() {
		if t := getTerminal(); t != nil {
			t.Reset()
		}
	})

	// Update dynamic states when menu is about to show
	menu.OnAboutToShow(func() {
		// Update File List icon to match current state
		if fileListAction != nil {
			if isWideMode() {
				if icon := createIconFromSVG(checkedIconSVG, 16); icon != nil {
					fileListAction.SetIcon(icon)
				}
			} else {
				if icon := createIconFromSVG(uncheckedIconSVG, 16); icon != nil {
					fileListAction.SetIcon(icon)
				}
			}
		}
		// Update Stop Script enabled state
		if isScriptRunningFunc != nil {
			stopScriptAction.SetEnabled(isScriptRunningFunc())
		}
	})

	menu.AddSeparator()

	// Save Scrollback ANSI (both)
	saveScrollbackANSIAction := menu.AddAction("Save Scrollback ANSI...")
	saveScrollbackANSIAction.OnTriggered(func() {
		saveScrollbackANSIDialog(parent, getTerminal())
	})

	// Save Scrollback Text (both)
	saveScrollbackTextAction := menu.AddAction("Save Scrollback Text...")
	saveScrollbackTextAction.OnTriggered(func() {
		saveScrollbackTextDialog(parent, getTerminal())
	})

	// Restore Buffer (both)
	restoreBufferAction := menu.AddAction("Restore Buffer...")
	restoreBufferAction.OnTriggered(func() {
		restoreBufferDialog(parent, getTerminal())
	})

	// Clear Scrollback (both)
	clearScrollbackAction := menu.AddAction("Clear Scrollback")
	clearScrollbackAction.OnTriggered(func() {
		if t := getTerminal(); t != nil {
			t.ClearScrollback()
		}
	})

	menu.AddSeparator()

	// Close (both)
	closeAction := menu.AddAction("Close")
	closeAction.OnTriggered(func() {
		if closeWindowFunc != nil {
			closeWindowFunc()
		} else if mainWindow != nil {
			mainWindow.Close()
		}
	})

	// Quit PawScript (both)
	quitAction := menu.AddAction("Quit PawScript")
	quitAction.OnTriggered(func() {
		quitApplication(parent)
	})

	return menu
}

// isWideMode returns true if the file list panel is visible
func isWideMode() bool {
	if launcherSplitter == nil {
		return true
	}
	sizes := launcherSplitter.Sizes()
	if len(sizes) >= 2 {
		// Wide mode when position >= bothThreshold (file list panel visible)
		bothThreshold := (minWidePanelWidth / 2) + minNarrowStripWidth
		return sizes[0] >= bothThreshold
	}
	return true
}

// toggleFileList toggles between wide and narrow-only file list modes
func toggleFileList() {
	if launcherSplitter == nil {
		return
	}
	sizes := launcherSplitter.Sizes()
	if len(sizes) < 2 {
		return
	}
	totalWidth := sizes[0] + sizes[1]
	hasMultipleButtons := len(launcherRegisteredBtns) > 0

	// Use same threshold as isWideMode() for consistency
	bothThreshold := (minWidePanelWidth / 2) + minNarrowStripWidth

	if sizes[0] >= bothThreshold {
		// Currently wide - collapse to narrow-only strip
		// Must hide wide panel BEFORE setting sizes, otherwise it fights for space
		launcherWidePanel.Hide()
		launcherNarrowStrip.Show()
		launcherMenuButton.Hide()
		launcherStripMenuBtn.Show()
		launcherSplitter.SetSizes([]int{minNarrowStripWidth, totalWidth - minNarrowStripWidth})
		saveLauncherWidth(minNarrowStripWidth)
	} else {
		// Currently narrow or collapsed - expand to wide
		savedWidth := 300 // Default
		if appConfig != nil {
			savedWidth = appConfig.GetInt("launcher_width", 300)
		}
		// Show wide panel before resizing
		launcherWidePanel.Show()
		if hasMultipleButtons {
			launcherNarrowStrip.Show()
			launcherMenuButton.Hide()
			launcherStripMenuBtn.Show()
			launcherSplitter.SetSizes([]int{savedWidth + minNarrowStripWidth, totalWidth - savedWidth - minNarrowStripWidth})
			saveLauncherWidth(savedWidth)
		} else {
			launcherNarrowStrip.Hide()
			launcherMenuButton.Show()
			launcherSplitter.SetSizes([]int{savedWidth, totalWidth - savedWidth})
			saveLauncherWidth(savedWidth)
		}
	}
}

// showOrCreateLauncher brings the launcher window to front, or creates one if needed
func showOrCreateLauncher() {
	if mainWindow != nil {
		mainWindow.Show()
		mainWindow.Raise()
		mainWindow.ActivateWindow()
	}
}

// quitApplication prompts for confirmation if scripts are running, then exits
func quitApplication(parent *qt.QWidget) {
	// Check if any scripts are running
	scriptMu.Lock()
	isRunning := scriptRunning
	scriptMu.Unlock()

	if isRunning {
		// Show confirmation dialog
		result := qt.QMessageBox_Question6(
			parent,
			"Quit PawScript",
			"This will stop all scripts. Are you sure?",
			qt.QMessageBox__Yes|qt.QMessageBox__No,
			qt.QMessageBox__No,
		)
		if result != qt.QMessageBox__Yes {
			return
		}
	}

	// Quit the application
	qt.QCoreApplication_Quit()
}

// saveScrollbackANSIDialog shows a file dialog to save terminal scrollback as ANSI
func saveScrollbackANSIDialog(parent *qt.QWidget, term *purfectermqt.Terminal) {
	if term == nil {
		return
	}

	file := qt.QFileDialog_GetSaveFileName4(
		parent,
		"Save Scrollback ANSI",
		"scrollback.ans",
		"ANSI Files (*.ans);;All Files (*)",
	)

	if file == "" {
		return
	}

	// Add header comment with version info using OSC 9999
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	header := fmt.Sprintf("\x1b]9999;PawScript %s (Qt; %s; %s) Buffer Saved %s\x07",
		version, runtime.GOOS, runtime.GOARCH, timestamp)
	content := header + term.SaveScrollbackANS()

	// Write to file
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		qt.QMessageBox_Critical5(
			parent,
			"Error",
			fmt.Sprintf("Failed to save file: %v", err),
			qt.QMessageBox__Ok,
		)
	}
}

// saveScrollbackTextDialog shows a file dialog to save terminal scrollback as plain text
func saveScrollbackTextDialog(parent *qt.QWidget, term *purfectermqt.Terminal) {
	if term == nil {
		return
	}

	file := qt.QFileDialog_GetSaveFileName4(
		parent,
		"Save Scrollback Text",
		"scrollback.txt",
		"Text Files (*.txt);;All Files (*)",
	)

	if file == "" {
		return
	}

	// Add header comment with version info as text comment
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	header := fmt.Sprintf("# PawScript %s (Qt; %s; %s) Buffer Saved %s\n",
		version, runtime.GOOS, runtime.GOARCH, timestamp)
	content := header + term.SaveScrollbackText()

	// Write to file
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		qt.QMessageBox_Critical5(
			parent,
			"Error",
			fmt.Sprintf("Failed to save file: %v", err),
			qt.QMessageBox__Ok,
		)
	}
}

// restoreBufferDialog shows a file dialog to load and display terminal content
func restoreBufferDialog(parent *qt.QWidget, term *purfectermqt.Terminal) {
	if term == nil {
		return
	}

	file := qt.QFileDialog_GetOpenFileName4(
		parent,
		"Restore Buffer",
		"",
		"ANSI Files (*.ans);;Text Files (*.txt);;All Files (*)",
	)

	if file == "" {
		return
	}

	// Read file content
	content, err := os.ReadFile(file)
	if err != nil {
		qt.QMessageBox_Critical5(
			parent,
			"Error",
			fmt.Sprintf("Failed to read file: %v", err),
			qt.QMessageBox__Ok,
		)
		return
	}

	// Convert LF to CR+LF for proper terminal display
	// (LF alone moves down without returning to column 0)
	contentStr := strings.ReplaceAll(string(content), "\r\n", "\n") // Normalize first
	contentStr = strings.ReplaceAll(contentStr, "\n", "\r\n")       // Then convert to CR+LF

	// Feed content to terminal
	term.Feed(contentStr)
}

// createBlankConsoleWindow creates a new blank terminal window with REPL
func createBlankConsoleWindow() {
	// Create new window
	win := qt.NewQMainWindow2()
	win.SetWindowTitle("PawScript - Console")
	win.SetMinimumSize2(900, 600)

	// Create terminal for this window with color scheme from config
	winTerminal, err := purfectermqt.New(purfectermqt.Options{
		Cols:           100,
		Rows:           30,
		ScrollbackSize: 10000,
		FontFamily:     getFontFamily(),
		FontSize:       getFontSize(),
		Scheme: purfecterm.ColorScheme{
			Foreground: getTerminalForeground(),
			Background: getTerminalBackground(),
			Cursor:     purfecterm.TrueColor(255, 255, 255),
			Selection:  purfecterm.TrueColor(68, 68, 68),
			Palette:    getColorPalette(),
			BlinkMode:  getBlinkMode(),
		},
	})
	if err != nil {
		win.Close()
		return
	}

	// Set font fallbacks for Unicode/CJK characters
	winTerminal.SetFontFallbacks(getFontFamilyUnicode(), getFontFamilyCJK())

	// Set up terminal theme from config
	prefersDark := isTermThemeDark()
	winTerminal.Buffer().SetPreferredDarkTheme(prefersDark)
	winTerminal.Buffer().SetDarkTheme(prefersDark)

	// Set up theme change callback (for CSI ? 5 h/l escape sequences)
	winTerminal.Buffer().SetThemeChangeCallback(func(isDark bool) {
		winTerminal.SetColorScheme(getColorSchemeForTheme(isDark))
	})

	// Track script running state for this window (starts with no script)
	var winScriptRunning bool
	var winScriptMu sync.Mutex

	// Create splitter for toolbar strip + terminal
	winSplitter := qt.NewQSplitter3(qt.Horizontal)

	// Create toolbar strip for this window
	winNarrowStrip, winStripMenuBtn, _ := createToolbarStripForWindow(win.QWidget, true, winTerminal, func() bool {
		winScriptMu.Lock()
		defer winScriptMu.Unlock()
		return winScriptRunning
	}, func() {
		win.Close()
	})
	winNarrowStrip.SetFixedWidth(minNarrowStripWidth)
	winNarrowStrip.Show()
	winStripMenuBtn.Show()

	// Register the toolbar data for theme updates (even without REPL initially)
	qtToolbarDataMu.Lock()
	blankConsoleToolbarData := &QtWindowToolbarData{
		strip:      winNarrowStrip,
		menuButton: winStripMenuBtn,
		terminal:   winTerminal,
	}
	qtToolbarDataByWindow[win] = blankConsoleToolbarData
	qtToolbarDataMu.Unlock()

	winSplitter.AddWidget(winNarrowStrip)
	winSplitter.AddWidget(winTerminal.Widget())

	winSplitter.SetStretchFactor(0, 0)
	winSplitter.SetStretchFactor(1, 1)
	winSplitter.SetSizes([]int{minNarrowStripWidth, 900 - minNarrowStripWidth})

	winSplitter.OnSplitterMoved(func(pos int, index int) {
		if index != 1 {
			return
		}
		if pos == 0 {
			// Already collapsed
		} else if pos < minNarrowStripWidth/2 {
			winSplitter.SetSizes([]int{0, winSplitter.Width()})
		} else if pos != minNarrowStripWidth {
			winSplitter.SetSizes([]int{minNarrowStripWidth, winSplitter.Width() - minNarrowStripWidth})
		}
	})

	win.SetCentralWidget(winSplitter.QWidget)

	// Create I/O channels for this window's console
	winStdinReader, winStdinWriter := io.Pipe()

	// Terminal capabilities for this window
	winWidth, winHeight := 100, 30
	winTermCaps := &pawscript.TerminalCapabilities{
		TermType:      "gui-console",
		IsTerminal:    true,
		SupportsANSI:  true,
		SupportsColor: true,
		ColorDepth:    256,
		Width:         winWidth,
		Height:        winHeight,
		SupportsInput: true,
		EchoEnabled:   false,
		LineMode:      false,
		Metadata:      make(map[string]interface{}),
	}

	// Non-blocking output queue
	winOutputQueue := make(chan interface{}, 256)
	go func() {
		for item := range winOutputQueue {
			switch v := item.(type) {
			case []byte:
				winTerminal.Feed(string(v))
			case string:
				winTerminal.Feed(v)
			case chan struct{}:
				close(v)
			}
		}
	}()

	winOutCh := &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		Terminal:         winTermCaps,
		NativeSend: func(v interface{}) error {
			var text string
			switch d := v.(type) {
			case []byte:
				text = string(d)
			case string:
				text = d
			default:
				text = fmt.Sprintf("%v", v)
			}
			text = strings.ReplaceAll(text, "\r\n", "\n")
			text = strings.ReplaceAll(text, "\n", "\r\n")
			select {
			case winOutputQueue <- []byte(text):
			default:
			}
			return nil
		},
		NativeRecv: func() (interface{}, error) {
			return nil, fmt.Errorf("cannot receive from console_out")
		},
		NativeFlush: func() error {
			writerDone := make(chan struct{})
			select {
			case winOutputQueue <- writerDone:
				<-writerDone
			default:
			}
			return nil
		},
	}

	// Non-blocking input queue
	winInputQueue := make(chan byte, 256)
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := winStdinReader.Read(buf)
			if err != nil || n == 0 {
				close(winInputQueue)
				return
			}
			select {
			case winInputQueue <- buf[0]:
			default:
				select {
				case <-winInputQueue:
				default:
				}
				select {
				case winInputQueue <- buf[0]:
				default:
				}
			}
		}
	}()

	winInCh := &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		Terminal:         winTermCaps,
		NativeRecv: func() (interface{}, error) {
			b, ok := <-winInputQueue
			if !ok {
				return nil, fmt.Errorf("input closed")
			}
			return []byte{b}, nil
		},
		NativeSend: func(v interface{}) error {
			return fmt.Errorf("cannot send to console_in")
		},
	}

	var winREPL *pawscript.REPL

	// Wire keyboard input
	winTerminal.SetInputCallback(func(data []byte) {
		winScriptMu.Lock()
		isRunning := winScriptRunning
		winScriptMu.Unlock()

		if isRunning {
			winStdinWriter.Write(data)
		} else if winREPL != nil && winREPL.IsRunning() {
			if winREPL.IsBusy() {
				winStdinWriter.Write(data)
			} else {
				winREPL.HandleInput(data)
			}
		}
	})

	// Clean up on window close
	win.OnDestroyed(func() {
		// Clean up toolbar data
		qtToolbarDataMu.Lock()
		delete(qtToolbarDataByWindow, win)
		qtToolbarDataMu.Unlock()
		winStdinWriter.Close()
		winStdinReader.Close()
		close(winOutputQueue)
	})

	win.Show()

	// Start REPL immediately (no script to run first)
	go func() {
		winREPL = pawscript.NewREPL(pawscript.REPLConfig{
			Debug:        false,
			Unrestricted: false,
			OptLevel:     getOptimizationLevel(),
			ShowBanner:   true,
			IOConfig: &pawscript.IOChannelConfig{
				Stdout: winOutCh,
				Stdin:  winInCh,
				Stderr: winOutCh,
			},
		}, func(s string) {
			winTerminal.Feed(s)
		})
		winREPL.SetFlush(func() {
			// Qt doesn't need explicit event processing like GTK
		})
		bg := getTerminalBackground()
		winREPL.SetBackgroundRGB(bg.R, bg.G, bg.B)
		winREPL.SetPSLColors(getPSLColors())
		winREPL.Start()
	}()
}

// createToolbarStripForWindow creates a vertical strip of toolbar buttons for a specific window
func createToolbarStripForWindow(parent *qt.QWidget, isScriptWindow bool, term *purfectermqt.Terminal, isScriptRunningFunc func() bool, closeWindowFunc func()) (*qt.QWidget, *IconButton, *qt.QMenu) {
	menu := createHamburgerMenu(parent, isScriptWindow, term, isScriptRunningFunc, closeWindowFunc)
	return createToolbarStripWithMenu(menu)
}

// createToolbarStripWithMenu creates a vertical strip of toolbar buttons using an existing menu
func createToolbarStripWithMenu(menu *qt.QMenu) (*qt.QWidget, *IconButton, *qt.QMenu) {
	strip := qt.NewQWidget2()
	layout := qt.NewQVBoxLayout2()
	layout.SetContentsMargins(4, 9, 4, 5)
	layout.SetSpacing(8)

	menuBtn := createHamburgerButton(menu)

	layout.AddWidget(menuBtn.QWidget)
	layout.AddStretch()
	strip.SetLayout(layout.QLayout)

	return strip, menuBtn, menu
}

// Toolbar button size constant for consistent square buttons
const toolbarButtonSize = 40
const toolbarIconSize = 24 // Icon is smaller than button, creating visible padding

// File list icon size (1.35x taller items than default)
const fileListIconSize = 32

// createHamburgerButton creates a hamburger menu button with custom icon widget
func createHamburgerButton(menu *qt.QMenu) *IconButton {
	svgData := getSVGIcon(hamburgerIconSVG)
	btn := NewIconButton(toolbarButtonSize, toolbarIconSize, svgData)
	btn.SetToolTip("Menu")

	// Show menu at the button's position when clicked
	btn.SetOnClick(func() {
		menu.Popup(btn.MapToGlobal(btn.Rect().BottomLeft()))
	})
	return btn
}

// createToolbarStrip creates a vertical strip of toolbar buttons
// Returns the strip container, the hamburger button, and the menu
func createToolbarStrip(parent *qt.QWidget, isScriptWindow bool) (*qt.QWidget, *IconButton, *qt.QMenu) {
	// Use global terminal for the main launcher
	isScriptRunningFunc := func() bool {
		scriptMu.Lock()
		defer scriptMu.Unlock()
		return scriptRunning
	}
	closeWindowFunc := func() {
		if mainWindow != nil {
			mainWindow.Close()
		}
	}
	return createToolbarStripForWindow(parent, isScriptWindow, nil, isScriptRunningFunc, closeWindowFunc)
}

// updateLauncherToolbarButtons updates the launcher's narrow strip with the current registered buttons
func updateLauncherToolbarButtons() {
	if launcherNarrowStrip == nil {
		return
	}

	// Check current state before updating (strip visible = had buttons before)
	hadButtons := launcherNarrowStrip.IsVisible()

	// Get the strip's layout
	layout := launcherNarrowStrip.Layout()
	if layout == nil {
		return
	}
	vbox := qt.UnsafeNewQVBoxLayout(layout.UnsafePointer())

	// Remove existing dummy buttons (but keep the hamburger menu button and stretch at the end)
	// We skip index 0 (hamburger) and the stretch item at the end
	for vbox.Count() > 2 {
		item := vbox.TakeAt(1)
		if item != nil && item.Widget() != nil {
			item.Widget().DeleteLater()
		}
	}

	// Add new dummy buttons (insert after hamburger button, before stretch)
	for _, btn := range launcherRegisteredBtns {
		svgData := getSVGIcon(starIconSVG)
		button := NewIconButton(toolbarButtonSize, toolbarIconSize, svgData)
		button.SetToolTip(btn.Tooltip)
		if btn.OnClick != nil {
			callback := btn.OnClick // Capture for closure
			button.SetOnClick(func() {
				callback()
			})
		}
		btn.widget = button
		vbox.InsertWidget(vbox.Count()-1, button.QWidget) // Insert before stretch
	}

	// Update visibility based on button count
	hasMultipleButtons := len(launcherRegisteredBtns) > 0

	// Adjust splitter position when transitioning between modes
	if launcherSplitter != nil {
		sizes := launcherSplitter.Sizes()
		if len(sizes) >= 2 {
			pos := sizes[0]
			totalWidth := sizes[0] + sizes[1]
			// Use same threshold as isWideMode() for consistency
			bothThreshold := (minWidePanelWidth / 2) + minNarrowStripWidth

			if pos >= bothThreshold {
				// Wide mode (both panels visible)
				if hadButtons && !hasMultipleButtons {
					// Transitioning from both mode to wide-only: subtract strip width
					newPos := pos - minNarrowStripWidth
					splitterAdjusting = true
					launcherSplitter.SetSizes([]int{newPos, totalWidth - newPos})
					splitterAdjusting = false
				} else if !hadButtons && hasMultipleButtons {
					// Transitioning from wide-only to both mode: add strip width
					newPos := pos + minNarrowStripWidth
					splitterAdjusting = true
					launcherSplitter.SetSizes([]int{newPos, totalWidth - newPos})
					splitterAdjusting = false
				}
			} else if pos > 0 && hadButtons && !hasMultipleButtons {
				// Narrow-only mode: collapse to 0 when removing buttons
				// (wide panel is hidden, and strip is being hidden too)
				splitterAdjusting = true
				launcherSplitter.SetSizes([]int{0, totalWidth})
				splitterAdjusting = false
			}
		}
	}

	if hasMultipleButtons {
		// Show narrow strip, hide menu button in path row
		launcherNarrowStrip.Show()
		if launcherMenuButton != nil {
			launcherMenuButton.Hide()
		}
		if launcherStripMenuBtn != nil {
			launcherStripMenuBtn.Show()
		}
	} else {
		// Hide narrow strip, show menu button in path row
		launcherNarrowStrip.Hide()
		if launcherMenuButton != nil {
			launcherMenuButton.Show()
		}
	}
}

// updateWindowToolbarButtons updates a window's toolbar strip with its registered buttons
func updateWindowToolbarButtons(strip *qt.QWidget, buttons []*QtToolbarButton) {
	if strip == nil {
		return
	}

	// Get the strip's layout
	layout := strip.Layout()
	if layout == nil {
		return
	}
	vbox := qt.UnsafeNewQVBoxLayout(layout.UnsafePointer())

	// Remove existing dummy buttons (but keep the hamburger menu button and stretch at the end)
	// We skip index 0 (hamburger) and the stretch item at the end
	for vbox.Count() > 2 {
		item := vbox.TakeAt(1)
		if item != nil && item.Widget() != nil {
			item.Widget().DeleteLater()
		}
	}

	// Add new dummy buttons (insert after hamburger button, before stretch)
	for _, btn := range buttons {
		svgData := getSVGIcon(starIconSVG)
		button := NewIconButton(toolbarButtonSize, toolbarIconSize, svgData)
		button.SetToolTip(btn.Tooltip)
		if btn.OnClick != nil {
			callback := btn.OnClick // Capture for closure
			button.SetOnClick(func() {
				callback()
			})
		}
		btn.widget = button
		vbox.InsertWidget(vbox.Count()-1, button.QWidget) // Insert before stretch
	}

	// Always show the strip when it has a hamburger button (console windows)
	strip.Show()
}

// setDummyButtonsForWindow sets the number of dummy buttons for a specific window
func setDummyButtonsForWindow(data *QtWindowToolbarData, count int) {
	// Clear existing dummy buttons
	data.registeredBtns = nil

	// Add new dummy buttons
	for i := 0; i < count; i++ {
		icon := dummyIcons[i%len(dummyIcons)]
		idx := i              // Capture for closure
		term := data.terminal // Capture terminal for closure
		btn := &QtToolbarButton{
			Icon:    icon,
			Tooltip: fmt.Sprintf("Dummy Button %d", i+1),
			OnClick: func() {
				if term != nil {
					term.Feed(fmt.Sprintf("\r\nDummy button %d clicked!\r\n", idx+1))
				}
			},
		}
		data.registeredBtns = append(data.registeredBtns, btn)
	}

	// Queue this window for update on the main thread
	if data.updateFunc != nil {
		pendingWindowUpdateMu.Lock()
		pendingWindowUpdates = append(pendingWindowUpdates, data)
		pendingWindowUpdateMu.Unlock()
	}
}

// setDummyButtons sets the number of dummy buttons in the launcher toolbar strip (legacy)
func setDummyButtons(count int) {
	// Clear existing dummy buttons
	launcherRegisteredBtns = nil

	// Add new dummy buttons
	for i := 0; i < count; i++ {
		icon := dummyIcons[i%len(dummyIcons)]
		idx := i // Capture for closure
		btn := &QtToolbarButton{
			Icon:    icon,
			Tooltip: fmt.Sprintf("Dummy Button %d", i+1),
			OnClick: func() {
				if terminal != nil {
					terminal.Feed(fmt.Sprintf("\r\nDummy button %d clicked!\r\n", idx+1))
				}
			},
		}
		launcherRegisteredBtns = append(launcherRegisteredBtns, btn)
	}

	// Signal the main thread to update the toolbar strip
	// The uiUpdateTimer will check this flag and call updateLauncherToolbarButtons()
	pendingToolbarUpdate = true
}

// registerDummyButtonCommand registers the dummy_button command with PawScript
// using per-window toolbar data
func registerDummyButtonCommand(ps *pawscript.PawScript, data *QtWindowToolbarData) {
	// Store the association
	qtToolbarDataMu.Lock()
	qtToolbarDataByPS[ps] = data
	qtToolbarDataMu.Unlock()

	ps.RegisterCommand("dummy_button", func(ctx *pawscript.Context) pawscript.Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(pawscript.CatCommand, "dummy_button requires a count argument")
			return pawscript.BoolStatus(false)
		}

		// Get the count argument
		count := 0
		switch v := ctx.Args[0].(type) {
		case int:
			count = v
		case int64:
			count = int(v)
		case float64:
			count = int(v)
		default:
			ctx.LogError(pawscript.CatCommand, "dummy_button requires a numeric argument")
			return pawscript.BoolStatus(false)
		}

		if count < 0 {
			count = 0
		}
		if count > 20 {
			count = 20 // Cap at 20 buttons
		}

		// Use the captured window data
		setDummyButtonsForWindow(data, count)
		ctx.SetResult(count)
		return pawscript.BoolStatus(true)
	})
}

// isSystemDarkMode detects if the OS is currently using dark mode
func isSystemDarkMode() bool {
	// On macOS, check AppleInterfaceStyle preference
	if runtime.GOOS == "darwin" {
		// Try to read macOS dark mode setting
		cmd := exec.Command("defaults", "read", "-g", "AppleInterfaceStyle")
		output, err := cmd.Output()
		if err == nil && strings.TrimSpace(string(output)) == "Dark" {
			return true
		}
		// If the key doesn't exist, system is in light mode
		return false
	}

	// For other platforms, check Qt palette
	// Process events first to ensure palette is fully initialized
	qt.QCoreApplication_ProcessEvents()

	palette := qt.QGuiApplication_Palette()
	windowColor := palette.ColorWithCr(qt.QPalette__Window)
	// Calculate luminance using standard formula
	luminance := 0.299*float64(windowColor.Red()) + 0.587*float64(windowColor.Green()) + 0.114*float64(windowColor.Blue())
	return luminance < 128
}

// applyTheme sets the Qt application palette based on the configuration.
// "auto" = detect OS preference, "dark" = force dark palette, "light" = force light palette
func applyTheme(theme pawgui.ThemeMode) {
	if qtApp == nil {
		return
	}

	// For Auto mode, detect OS preference and apply appropriate theme
	if theme == pawgui.ThemeAuto {
		if isSystemDarkMode() {
			theme = pawgui.ThemeDark
		} else {
			theme = pawgui.ThemeLight
		}
	}

	// Track the actual applied theme for icon colors
	appliedThemeIsDark = (theme == pawgui.ThemeDark)

	switch theme {
	case pawgui.ThemeDark:
		// Create a dark palette using stylesheet for better cross-platform support
		qtApp.SetStyleSheet(`
			QWidget {
				background-color: #353535;
				color: #ffffff;
			}
			QMainWindow, QDialog {
				background-color: #353535;
			}
			QPushButton {
				background-color: #454545;
				border: 1px solid #555555;
				padding: 5px 15px;
				border-radius: 3px;
			}
			QPushButton:hover {
				background-color: #505050;
			}
			QPushButton:pressed {
				background-color: #404040;
			}
			QListWidget {
				background-color: #252525;
				border: 1px solid #454545;
			}
			QListWidget::item:selected {
				background-color: #2a82da;
			}
			QLabel {
				background-color: transparent;
			}
			QSplitter::handle {
				background-color: #454545;
			}
			QScrollBar:vertical, QAbstractScrollArea QScrollBar:vertical, QListWidget QScrollBar:vertical {
				background: transparent;
				width: 12px;
				margin: 2px 2px 2px 0px;
			}
			QScrollBar::handle:vertical, QAbstractScrollArea QScrollBar::handle:vertical, QListWidget QScrollBar::handle:vertical {
				background: rgba(255, 255, 255, 0.3);
				min-height: 30px;
				border-radius: 4px;
				margin: 0px 2px 0px 2px;
			}
			QScrollBar::handle:vertical:hover {
				background: rgba(255, 255, 255, 0.5);
			}
			QScrollBar::handle:vertical:pressed {
				background: rgba(255, 255, 255, 0.6);
			}
			QScrollBar::add-line:vertical, QScrollBar::sub-line:vertical {
				height: 0px;
			}
			QScrollBar::add-page:vertical, QScrollBar::sub-page:vertical {
				background: transparent;
			}
			QScrollBar:horizontal, QAbstractScrollArea QScrollBar:horizontal, QListWidget QScrollBar:horizontal {
				background: transparent;
				height: 12px;
				margin: 0px 2px 2px 2px;
			}
			QScrollBar::handle:horizontal, QAbstractScrollArea QScrollBar::handle:horizontal, QListWidget QScrollBar::handle:horizontal {
				background: rgba(255, 255, 255, 0.3);
				min-width: 30px;
				border-radius: 4px;
				margin: 2px 0px 2px 0px;
			}
			QScrollBar::handle:horizontal:hover {
				background: rgba(255, 255, 255, 0.5);
			}
			QScrollBar::handle:horizontal:pressed {
				background: rgba(255, 255, 255, 0.6);
			}
			QScrollBar::add-line:horizontal, QScrollBar::sub-line:horizontal {
				width: 0px;
			}
			QScrollBar::add-page:horizontal, QScrollBar::sub-page:horizontal {
				background: transparent;
			}
			QMenu {
				background-color: #505050;
				border: 1px solid #555555;
				padding: 4px 0px;
			}
			QMenu::item {
				background-color: #383838;
				border-left: 1px solid #666666;
				margin-left: 40px;
				padding: 6px 20px 6px 8px;
			}
			QMenu::item:selected {
				background-color: #4a4a4a;
				border: 1px solid #888888;
				margin-left: 0px;
				padding-left: 48px;
			}
			QMenu::item:disabled {
				color: #888888;
			}
			QMenu::icon {
				subcontrol-origin: margin;
				subcontrol-position: left center;
				left: 12px;
			}
			QMenu::indicator {
				width: 16px;
				height: 16px;
				subcontrol-origin: margin;
				subcontrol-position: left center;
				left: 12px;
			}
			QMenu::indicator:checked {
				background-color: transparent;
				border-left: 3px solid #ffffff;
				border-bottom: 3px solid #ffffff;
				width: 5px;
				height: 10px;
				subcontrol-origin: margin;
				subcontrol-position: left center;
				left: 14px;
			}
			QMenu::indicator:checked:selected {
				background-color: transparent;
				border-left: 3px solid #ffffff;
				border-bottom: 3px solid #ffffff;
				width: 5px;
				height: 10px;
				subcontrol-origin: margin;
				subcontrol-position: left center;
				left: 14px;
			}
			QMenu::separator {
				height: 1px;
				background: #555555;
				margin: 2px 8px 2px 48px;
			}
		`)

	case pawgui.ThemeLight:
		// Create a light palette using stylesheet
		qtApp.SetStyleSheet(`
			QWidget {
				background-color: #f0f0f0;
				color: #000000;
			}
			QMainWindow, QDialog {
				background-color: #f0f0f0;
			}
			QPushButton {
				background-color: #e0e0e0;
				border: 1px solid #c0c0c0;
				padding: 5px 15px;
				border-radius: 3px;
			}
			QPushButton:hover {
				background-color: #d0d0d0;
			}
			QPushButton:pressed {
				background-color: #c0c0c0;
			}
			QListWidget {
				background-color: #ffffff;
				border: 1px solid #c0c0c0;
			}
			QListWidget::item:selected {
				background-color: #0078d7;
				color: #ffffff;
			}
			QLabel {
				background-color: transparent;
			}
			QSplitter::handle {
				background-color: #c0c0c0;
			}
			QScrollBar:vertical, QAbstractScrollArea QScrollBar:vertical, QListWidget QScrollBar:vertical {
				background: transparent;
				width: 12px;
				margin: 2px 2px 2px 0px;
			}
			QScrollBar::handle:vertical, QAbstractScrollArea QScrollBar::handle:vertical, QListWidget QScrollBar::handle:vertical {
				background: rgba(0, 0, 0, 0.3);
				min-height: 30px;
				border-radius: 4px;
				margin: 0px 2px 0px 2px;
			}
			QScrollBar::handle:vertical:hover {
				background: rgba(0, 0, 0, 0.5);
			}
			QScrollBar::handle:vertical:pressed {
				background: rgba(0, 0, 0, 0.6);
			}
			QScrollBar::add-line:vertical, QScrollBar::sub-line:vertical {
				height: 0px;
			}
			QScrollBar::add-page:vertical, QScrollBar::sub-page:vertical {
				background: transparent;
			}
			QScrollBar:horizontal, QAbstractScrollArea QScrollBar:horizontal, QListWidget QScrollBar:horizontal {
				background: transparent;
				height: 12px;
				margin: 0px 2px 2px 2px;
			}
			QScrollBar::handle:horizontal, QAbstractScrollArea QScrollBar::handle:horizontal, QListWidget QScrollBar::handle:horizontal {
				background: rgba(0, 0, 0, 0.3);
				min-width: 30px;
				border-radius: 4px;
				margin: 2px 0px 2px 0px;
			}
			QScrollBar::handle:horizontal:hover {
				background: rgba(0, 0, 0, 0.5);
			}
			QScrollBar::handle:horizontal:pressed {
				background: rgba(0, 0, 0, 0.6);
			}
			QScrollBar::add-line:horizontal, QScrollBar::sub-line:horizontal {
				width: 0px;
			}
			QScrollBar::add-page:horizontal, QScrollBar::sub-page:horizontal {
				background: transparent;
			}
			QMenu {
				background-color: #e0e0e0;
				border: 1px solid #c0c0c0;
				padding: 4px 0px;
			}
			QMenu::item {
				background-color: #ffffff;
				border-left: 1px solid #c0c0c0;
				margin-left: 40px;
				padding: 6px 20px 6px 8px;
			}
			QMenu::item:selected {
				background-color: #e5f3ff;
				border: 1px solid #6699cc;
				margin-left: 0px;
				padding-left: 48px;
			}
			QMenu::item:disabled {
				color: #888888;
			}
			QMenu::icon {
				subcontrol-origin: margin;
				subcontrol-position: left center;
				left: 12px;
			}
			QMenu::indicator {
				width: 16px;
				height: 16px;
				subcontrol-origin: margin;
				subcontrol-position: left center;
				left: 12px;
			}
			QMenu::indicator:checked {
				background-color: transparent;
				border-left: 3px solid #000000;
				border-bottom: 3px solid #000000;
				width: 5px;
				height: 10px;
				subcontrol-origin: margin;
				subcontrol-position: left center;
				left: 14px;
			}
			QMenu::indicator:checked:selected {
				background-color: transparent;
				border-left: 3px solid #000000;
				border-bottom: 3px solid #000000;
				width: 5px;
				height: 10px;
				subcontrol-origin: margin;
				subcontrol-position: left center;
				left: 14px;
			}
			QMenu::separator {
				height: 1px;
				background: #c0c0c0;
				margin: 2px 8px 2px 48px;
			}
		`)
	}

	// Re-apply UI scaling after theme change (theme replaces stylesheet)
	applyUIScale(getUIScale())

	// Update toolbar icons to match new theme colors
	updateToolbarIcons()
}

// updateToolbarIcons regenerates all toolbar icons with the current theme's colors
func updateToolbarIcons() {
	// Update both launcher hamburger buttons (path selector and narrow strip)
	if launcherMenuButton != nil {
		launcherMenuButton.UpdateIcon(getSVGIcon(hamburgerIconSVG), toolbarIconSize)
	}
	if launcherStripMenuBtn != nil {
		launcherStripMenuBtn.UpdateIcon(getSVGIcon(hamburgerIconSVG), toolbarIconSize)
	}

	// Update all registered buttons in launcher toolbar
	for _, btn := range launcherRegisteredBtns {
		if btn.widget != nil {
			btn.widget.UpdateIcon(getSVGIcon(starIconSVG), toolbarIconSize)
		}
	}

	// Update buttons in all script windows (keyed by PawScript instance)
	qtToolbarDataMu.Lock()
	for _, data := range qtToolbarDataByPS {
		// Update the hamburger button
		if data.menuButton != nil {
			data.menuButton.UpdateIcon(getSVGIcon(hamburgerIconSVG), toolbarIconSize)
		}
		// Update registered buttons
		for _, btn := range data.registeredBtns {
			if btn.widget != nil {
				btn.widget.UpdateIcon(getSVGIcon(starIconSVG), toolbarIconSize)
			}
		}
	}

	// Update buttons in all windows (keyed by window pointer)
	for _, data := range qtToolbarDataByWindow {
		// Update the hamburger button
		if data.menuButton != nil {
			data.menuButton.UpdateIcon(getSVGIcon(hamburgerIconSVG), toolbarIconSize)
		}
		// Update registered buttons
		for _, btn := range data.registeredBtns {
			if btn.widget != nil {
				btn.widget.UpdateIcon(getSVGIcon(starIconSVG), toolbarIconSize)
			}
		}
	}
	qtToolbarDataMu.Unlock()

	// Refresh path menu icons (Home, Examples, etc.)
	updatePathMenu()

	// Refresh file list icons
	refreshFileListIcons()
}

// refreshFileListIcons updates all file list icons to match current theme
func refreshFileListIcons() {
	if fileList == nil {
		return
	}

	currentItem := fileList.CurrentItem()

	for i := 0; i < fileList.Count(); i++ {
		item := fileList.Item(i)
		if item == nil {
			continue
		}

		fileItemDataMu.Lock()
		data, ok := fileItemDataMap[item.UnsafePointer()]
		fileItemDataMu.Unlock()

		if !ok {
			continue
		}

		// Use dark icon if this item is selected, normal theme icon otherwise
		isSelected := currentItem != nil && item.UnsafePointer() == currentItem.UnsafePointer()

		var icon *qt.QIcon
		switch data.iconType {
		case iconTypeFolderUp:
			if isSelected {
				icon = createDarkIconFromSVG(folderUpIconSVG, fileListIconSize)
			} else {
				icon = createIconFromSVG(folderUpIconSVG, fileListIconSize)
			}
		case iconTypeFolder:
			if isSelected {
				icon = createDarkIconFromSVG(folderIconSVG, fileListIconSize)
			} else {
				icon = createIconFromSVG(folderIconSVG, fileListIconSize)
			}
		case iconTypePawFile:
			// pawFile icon doesn't change with theme, but we still update it
			icon = createIconFromSVG(pawFileIconSVG, fileListIconSize)
		}

		if icon != nil {
			item.SetIcon(icon)
		}
	}
}

// applyUIScale applies UI scaling via stylesheet (does not affect terminal)
// Qt uses 1.75x the config scale to match visual appearance with GTK
func applyUIScale(scale float64) {
	if qtApp == nil {
		return
	}

	// Qt needs 1.75x scale factor to match GTK visual appearance
	effectiveScale := scale * 1.75

	baseFontSize := int(10.0 * effectiveScale)
	buttonPadding := int(5.0 * effectiveScale)
	buttonPaddingH := int(15.0 * effectiveScale)

	// Get existing stylesheet and append scaling rules
	existing := qtApp.StyleSheet()
	scaled := fmt.Sprintf(`
		QWidget {
			font-size: %dpx;
		}
		QPushButton {
			padding: %dpx %dpx;
			font-size: %dpx;
		}
		QLabel {
			font-size: %dpx;
		}
		QListWidget {
			font-size: %dpx;
		}
	`, baseFontSize, buttonPadding, buttonPaddingH, baseFontSize, baseFontSize, baseFontSize)

	qtApp.SetStyleSheet(existing + scaled)
}

func main() {
	// Define command line flags
	licenseFlag := flag.Bool("license", false, "Show license")
	versionFlag := flag.Bool("version", false, "Show version")
	debugFlag := flag.Bool("debug", false, "Enable debug output")
	verboseFlag := flag.Bool("verbose", false, "Enable verbose output (alias for -debug)")
	flag.BoolVar(debugFlag, "d", false, "Enable debug output (short)")
	flag.BoolVar(verboseFlag, "v", false, "Enable verbose output (short, alias for -debug)")

	// File access control flags
	unrestrictedFlag := flag.Bool("unrestricted", false, "Disable all file/exec access restrictions")
	readRootsFlag := flag.String("read-roots", "", "Additional directories for file reading")
	writeRootsFlag := flag.String("write-roots", "", "Additional directories for file writing")
	execRootsFlag := flag.String("exec-roots", "", "Additional directories for exec command")
	sandboxFlag := flag.String("sandbox", "", "Restrict all access to this directory only")

	// Optimization level flag
	optLevelFlag := flag.Int("O", 1, "Optimization level (0=no caching, 1=cache macro/loop bodies)")

	// GUI-specific flags
	windowFlag := flag.Bool("window", false, "Create console window for stdout/stdin/stderr")

	// Custom usage function
	flag.Usage = showUsage

	// Parse flags
	flag.Parse()

	if *versionFlag {
		showCopyright()
		os.Exit(0)
	}

	if *licenseFlag {
		showLicense()
		os.Exit(0)
	}

	// Verbose is an alias for debug
	debug := *debugFlag || *verboseFlag
	_ = debug // Will be used later

	// Get remaining arguments after flags
	args := flag.Args()

	var scriptFile string
	var scriptContent string
	var scriptArgs []string

	// Check for -- separator
	separatorIndex := -1
	for i, arg := range args {
		if arg == "--" {
			separatorIndex = i
			break
		}
	}

	var fileArgs []string
	if separatorIndex != -1 {
		fileArgs = args[:separatorIndex]
		scriptArgs = args[separatorIndex+1:]
	} else {
		fileArgs = args
	}

	// Check if stdin is redirected/piped
	stdinInfo, _ := os.Stdin.Stat()
	isStdinRedirected := (stdinInfo.Mode() & os.ModeCharDevice) == 0

	if len(fileArgs) > 0 {
		// Filename provided
		requestedFile := fileArgs[0]
		foundFile := findScriptFile(requestedFile)

		if foundFile == "" {
			fmt.Fprintf(os.Stderr, "Error: Script file not found: %s\n", requestedFile)
			if !strings.Contains(requestedFile, ".") {
				fmt.Fprintf(os.Stderr, "Also tried: %s.paw\n", requestedFile)
			}
			os.Exit(1)
		}

		scriptFile = foundFile

		content, err := os.ReadFile(scriptFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading script file: %v\n", err)
			os.Exit(1)
		}
		scriptContent = string(content)

		// Remaining fileArgs become script arguments (if no separator was used)
		if separatorIndex == -1 && len(fileArgs) > 1 {
			scriptArgs = fileArgs[1:]
		}

	} else if isStdinRedirected {
		// No filename, but stdin is redirected - read from stdin
		content, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading from stdin: %v\n", err)
			os.Exit(1)
		}
		scriptContent = string(content)
	}

	// If we have script content (from file or stdin), run it
	if scriptContent != "" {
		runScriptFromCLI(scriptContent, scriptFile, scriptArgs, *windowFlag, *unrestrictedFlag,
			*sandboxFlag, *readRootsFlag, *writeRootsFlag, *execRootsFlag, *optLevelFlag)
		return
	}

	// No script provided - launch GUI launcher mode
	launchGUIMode()
}

// launchGUIMode starts the Qt application in launcher mode (file browser + terminal)
func launchGUIMode() {
	// Load configuration
	appConfig = loadConfig()
	configHelper = pawgui.NewConfigHelper(appConfig)

	// Auto-populate config with defaults (makes them discoverable)
	if configHelper.PopulateDefaults() {
		saveConfig(appConfig)
	}

	// Get initial directory
	currentDir = appConfig.GetString("last_browse_dir", "")
	if currentDir == "" {
		currentDir, _ = os.Getwd()
	}

	// Initialize Qt application
	qtApp = qt.NewQApplication(os.Args)

	// Apply theme setting
	applyTheme(configHelper.GetTheme())

	// Apply UI scaling via stylesheet (affects everything except terminal)
	applyUIScale(getUIScale())

	// Create main window
	mainWindow = qt.NewQMainWindow2()
	mainWindow.SetWindowTitle(appName)

	// Get screen dimensions for bounds checking
	screen := qt.QGuiApplication_PrimaryScreen()
	screenGeom := screen.AvailableGeometry()
	screenWidth := screenGeom.Width()
	screenHeight := screenGeom.Height()

	// Load saved size, validate against screen bounds
	savedWidth, savedHeight := getLauncherSize()
	if savedWidth > screenWidth {
		savedWidth = screenWidth
	}
	if savedHeight > screenHeight {
		savedHeight = screenHeight
	}
	if savedWidth < 400 {
		savedWidth = 400
	}
	if savedHeight < 300 {
		savedHeight = 300
	}
	mainWindow.Resize(savedWidth, savedHeight)

	// Load saved position, validate to ensure window is on screen
	savedX, savedY := getLauncherPosition()
	if savedX >= 0 && savedY >= 0 {
		// Ensure at least 100px of window is visible on screen
		if savedX > screenWidth-100 {
			savedX = screenWidth - 100
		}
		if savedY > screenHeight-100 {
			savedY = screenHeight - 100
		}
		if savedX < 0 {
			savedX = 0
		}
		if savedY < 0 {
			savedY = 0
		}
		mainWindow.Move(savedX, savedY)
	}

	// Track window geometry changes using event filter
	mainWindow.InstallEventFilter(mainWindow.QObject)
	var lastX, lastY, lastWidth, lastHeight int
	mainWindow.OnEventFilter(func(super func(watched *qt.QObject, event *qt.QEvent) bool, watched *qt.QObject, event *qt.QEvent) bool {
		if event.Type() == qt.QEvent__Move {
			pos := mainWindow.Pos()
			x, y := pos.X(), pos.Y()
			if x != lastX || y != lastY {
				lastX, lastY = x, y
				saveLauncherPosition(x, y)
			}
		} else if event.Type() == qt.QEvent__Resize {
			size := mainWindow.Size()
			w, h := size.Width(), size.Height()
			if w != lastWidth || h != lastHeight {
				lastWidth, lastHeight = w, h
				saveLauncherSize(w, h)
			}
		}
		return super(watched, event) // Let the event propagate normally
	})

	// Create central widget with horizontal splitter
	centralWidget := qt.NewQWidget2()
	mainLayout := qt.NewQHBoxLayout2()
	mainLayout.SetContentsMargins(0, 0, 0, 0)
	mainLayout.SetSpacing(0)
	centralWidget.SetLayout(mainLayout.QLayout)

	// Create splitter
	launcherSplitter = qt.NewQSplitter3(qt.Horizontal)

	// Left container: holds wide panel (file browser) and narrow strip side by side
	leftContainer := qt.NewQWidget2()
	leftLayout := qt.NewQHBoxLayout2()
	leftLayout.SetContentsMargins(0, 0, 0, 0)
	leftLayout.SetSpacing(0)
	leftContainer.SetLayout(leftLayout.QLayout)

	// Create shared hamburger menu for launcher (used by both wide panel and narrow strip buttons)
	launcherMenu = createHamburgerMenu(leftContainer, false, nil, func() bool {
		scriptMu.Lock()
		defer scriptMu.Unlock()
		return scriptRunning
	}, func() {
		if mainWindow != nil {
			mainWindow.Close()
		}
	})

	// Wide panel (file browser) - uses shared launcherMenu
	widePanel := createFilePanel()
	leftLayout.AddWidget2(widePanel, 1)

	// Narrow strip: toolbar buttons (created but hidden initially - only 1 button)
	// Uses the same shared launcherMenu as the wide panel button
	launcherNarrowStrip, launcherStripMenuBtn, _ = createToolbarStripWithMenu(launcherMenu)
	launcherNarrowStrip.SetFixedWidth(minNarrowStripWidth) // Fixed width
	launcherNarrowStrip.Hide()                             // Hidden initially since we only have 1 button
	leftLayout.AddWidget(launcherNarrowStrip)

	// Initially: hamburger button visible in path selector, narrow strip hidden
	launcherMenuButton.Show()

	launcherSplitter.AddWidget(leftContainer)

	// Right panel (terminal)
	rightPanel := createTerminalPanel()
	launcherSplitter.AddWidget(rightPanel)

	// Set initial splitter sizes using saved launcher width
	// Note: panelWidth represents only the wide panel width (not including strip)
	// When buttons exist, we add strip width to get actual splitter position
	panelWidth := getLauncherWidth()
	hasMultipleButtons := len(launcherRegisteredBtns) > 0
	initialWidth := panelWidth
	if hasMultipleButtons && panelWidth > minNarrowStripWidth {
		// Wide mode with buttons: add strip width
		initialWidth = panelWidth + minNarrowStripWidth
	}
	launcherSplitter.SetSizes([]int{initialWidth, 900 - initialWidth})

	// Configure stretch factors so left panel stays fixed and right panel is flexible
	// This matches the GTK behavior where additional space goes to the console
	launcherSplitter.SetStretchFactor(0, 0) // Left panel: fixed size (doesn't stretch)
	launcherSplitter.SetStretchFactor(1, 1) // Right panel: flexible (absorbs size changes)

	// Save launcher width when user adjusts the splitter
	// Implement multi-stage collapse:
	// - Wide + narrow mode: when pos >= minWidePanelWidth + minNarrowStripWidth
	// - Narrow only mode: when pos >= minNarrowStripWidth but < threshold for wide panel
	// - Collapsed: when pos < halfway point of narrow strip
	launcherSplitter.OnSplitterMoved(func(pos int, index int) {
		if index != 1 {
			return
		}
		// Prevent recursive callbacks when we call SetSizes
		if splitterAdjusting {
			return
		}

		hasMultipleButtons := len(launcherRegisteredBtns) > 0

		// Calculate threshold for showing both panels (use halfway point for easier expansion)
		bothThreshold := (minWidePanelWidth / 2) + minNarrowStripWidth

		// Use halfway points for snapping decisions
		narrowSnapPoint := minNarrowStripWidth / 2 // 20 - below this, collapse fully

		if pos < narrowSnapPoint {
			// Too narrow even for strip - collapse fully
			splitterAdjusting = true
			launcherSplitter.SetSizes([]int{0, launcherSplitter.Width()})
			splitterAdjusting = false
			// Hide everything
			launcherWidePanel.Hide()
			launcherNarrowStrip.Hide()
			launcherMenuButton.Show()
			saveLauncherWidth(0)
		} else if pos < bothThreshold {
			// Between narrow snap point and both-panels threshold
			// Show only narrow strip at its fixed width
			launcherWidePanel.Hide()
			launcherNarrowStrip.Show()
			launcherMenuButton.Hide()
			launcherStripMenuBtn.Show()
			// Snap to just the narrow strip width
			if pos != minNarrowStripWidth {
				splitterAdjusting = true
				launcherSplitter.SetSizes([]int{minNarrowStripWidth, launcherSplitter.Width() - minNarrowStripWidth})
				splitterAdjusting = false
			}
			saveLauncherWidth(minNarrowStripWidth)
		} else {
			// Wide enough for full panel
			launcherWidePanel.Show()
			if hasMultipleButtons {
				launcherNarrowStrip.Show()
				launcherMenuButton.Hide()
				launcherStripMenuBtn.Show()
				// Save only the wide panel width (subtract strip width)
				saveLauncherWidth(pos - minNarrowStripWidth)
			} else {
				launcherNarrowStrip.Hide()
				launcherMenuButton.Show()
				saveLauncherWidth(pos)
			}
		}
	})

	// Note: Click handling on splitter handles not supported in miqt
	// (can only override virtual methods on directly constructed objects)

	mainLayout.AddWidget(launcherSplitter.QWidget)
	mainWindow.SetCentralWidget(centralWidget)

	// Set up console I/O
	setupConsoleIO()

	// Print welcome banner before REPL starts (so prompt appears after)
	terminal.Feed(fmt.Sprintf("pawgui-qt, the PawScript GUI interpreter version %s (with Qt)\r\n", version))
	terminal.Feed("Copyright (c) 2025 Jeffrey R. Day\r\n")
	terminal.Feed("License: MIT\r\n\r\n")
	terminal.Feed("Interactive mode. Type 'exit' or 'quit' to leave.\r\n")
	terminal.Feed("Select a .paw file and click Run to execute.\r\n\r\n")

	// Start REPL (prompt will appear after welcome message)
	startREPL()

	// Load initial directory
	loadDirectory(currentDir)

	// Start UI update timer (250ms) for path button elision and future UI updates
	uiUpdateTimer := qt.NewQTimer2(mainWindow.QObject)
	uiUpdateTimer.OnTimeout(func() {
		updatePathButtonText()
		// Check for pending launcher toolbar updates
		if pendingToolbarUpdate {
			pendingToolbarUpdate = false
			updateLauncherToolbarButtons()
		}
		// Process pending window toolbar updates
		pendingWindowUpdateMu.Lock()
		updates := pendingWindowUpdates
		pendingWindowUpdates = nil
		pendingWindowUpdateMu.Unlock()
		for _, data := range updates {
			if data.updateFunc != nil {
				data.updateFunc()
			}
		}
	})
	uiUpdateTimer.Start(250)

	// Set up quit shortcut based on config
	setupQuitShortcut()

	// Set up tab order: pathButton -> fileList -> runButton -> browseButton -> terminal
	qt.QWidget_SetTabOrder(pathButton.QWidget, fileList.QWidget)
	qt.QWidget_SetTabOrder(fileList.QWidget, runButton.QWidget)
	qt.QWidget_SetTabOrder(runButton.QWidget, browseButton.QWidget)
	qt.QWidget_SetTabOrder(browseButton.QWidget, terminal.Widget())

	// Show window
	mainWindow.Show()

	// Focus the Run button by default
	runButton.SetFocus()

	// Run application
	qt.QApplication_Exec()
}

// runScriptFromCLI executes a script provided via command line
func runScriptFromCLI(scriptContent, scriptFile string, scriptArgs []string, windowFlag bool,
	unrestricted bool, sandbox, readRoots, writeRoots, execRoots string, optLevel int) {

	// Build file access configuration
	var fileAccess *pawscript.FileAccessConfig
	var scriptDir string
	if scriptFile != "" {
		absScript, err := filepath.Abs(scriptFile)
		if err == nil {
			scriptDir = filepath.Dir(absScript)
		}
	}

	if !unrestricted {
		fileAccess = &pawscript.FileAccessConfig{}
		cwd, _ := os.Getwd()
		tmpDir := os.TempDir()

		// Helper to expand SCRIPT_DIR placeholder and resolve path
		expandPath := func(path string) string {
			path = strings.TrimSpace(path)
			if path == "" {
				return ""
			}
			if strings.HasPrefix(path, "SCRIPT_DIR/") {
				if scriptDir != "" {
					path = filepath.Join(scriptDir, path[11:])
				} else {
					return ""
				}
			} else if path == "SCRIPT_DIR" {
				if scriptDir != "" {
					path = scriptDir
				} else {
					return ""
				}
			}
			absPath, err := filepath.Abs(path)
			if err != nil {
				return ""
			}
			return absPath
		}

		// Helper to parse comma-separated roots with SCRIPT_DIR expansion
		parseRoots := func(rootsStr string) []string {
			var roots []string
			for _, root := range strings.Split(rootsStr, ",") {
				if expanded := expandPath(root); expanded != "" {
					roots = append(roots, expanded)
				}
			}
			return roots
		}

		if sandbox != "" {
			absPath, err := filepath.Abs(sandbox)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error resolving sandbox path: %v\n", err)
				os.Exit(1)
			}
			fileAccess.ReadRoots = []string{absPath}
			fileAccess.WriteRoots = []string{absPath}
			fileAccess.ExecRoots = []string{absPath}
		} else {
			// Check environment variables first
			envReadRoots := os.Getenv("PAW_READ_ROOTS")
			envWriteRoots := os.Getenv("PAW_WRITE_ROOTS")
			envExecRoots := os.Getenv("PAW_EXEC_ROOTS")

			if envReadRoots != "" {
				fileAccess.ReadRoots = parseRoots(envReadRoots)
			} else {
				if scriptDir != "" {
					fileAccess.ReadRoots = append(fileAccess.ReadRoots, scriptDir)
				}
				if cwd != "" && cwd != scriptDir {
					fileAccess.ReadRoots = append(fileAccess.ReadRoots, cwd)
				}
				fileAccess.ReadRoots = append(fileAccess.ReadRoots, tmpDir)
			}
			if readRoots != "" {
				fileAccess.ReadRoots = append(fileAccess.ReadRoots, parseRoots(readRoots)...)
			}

			if envWriteRoots != "" {
				fileAccess.WriteRoots = parseRoots(envWriteRoots)
			} else {
				if scriptDir != "" {
					fileAccess.WriteRoots = append(fileAccess.WriteRoots,
						filepath.Join(scriptDir, "saves"),
						filepath.Join(scriptDir, "output"))
				}
				if cwd != "" && cwd != scriptDir {
					fileAccess.WriteRoots = append(fileAccess.WriteRoots,
						filepath.Join(cwd, "saves"),
						filepath.Join(cwd, "output"))
				}
				fileAccess.WriteRoots = append(fileAccess.WriteRoots, tmpDir)
			}
			if writeRoots != "" {
				fileAccess.WriteRoots = append(fileAccess.WriteRoots, parseRoots(writeRoots)...)
			}

			if envExecRoots != "" {
				fileAccess.ExecRoots = parseRoots(envExecRoots)
			} else {
				if scriptDir != "" {
					fileAccess.ExecRoots = append(fileAccess.ExecRoots,
						filepath.Join(scriptDir, "helpers"),
						filepath.Join(scriptDir, "bin"))
				}
			}
			if execRoots != "" {
				fileAccess.ExecRoots = append(fileAccess.ExecRoots, parseRoots(execRoots)...)
			}
		}
	}

	if !windowFlag {
		// No window mode - run like CLI
		ps := pawscript.New(&pawscript.Config{
			Debug:                false,
			AllowMacros:          true,
			EnableSyntacticSugar: true,
			ShowErrorContext:     true,
			ContextLines:         2,
			FileAccess:           fileAccess,
			OptLevel:             pawscript.OptimizationLevel(optLevel),
			ScriptDir:            scriptDir,
		})
		ps.RegisterStandardLibrary(scriptArgs)

		var result pawscript.Result
		if scriptFile != "" {
			result = ps.ExecuteFile(scriptContent, scriptFile)
		} else {
			result = ps.Execute(scriptContent)
		}
		if result == pawscript.BoolStatus(false) {
			os.Exit(1)
		}
		return
	}

	// Window mode - create Qt application with console window
	runScriptInWindow(scriptContent, scriptFile, scriptArgs, fileAccess, optLevel, scriptDir)
}

// runScriptInWindow creates a Qt console window and runs the script
func runScriptInWindow(scriptContent, scriptFile string, scriptArgs []string,
	fileAccess *pawscript.FileAccessConfig, optLevel int, scriptDir string) {

	// Load configuration
	appConfig = loadConfig()
	configHelper = pawgui.NewConfigHelper(appConfig)
	if configHelper.PopulateDefaults() {
		saveConfig(appConfig)
	}

	// Initialize Qt application
	qtApp = qt.NewQApplication(os.Args)
	applyTheme(configHelper.GetTheme())

	// Create console window
	win := qt.NewQMainWindow2()
	title := "PawScript Console"
	if scriptFile != "" {
		title = filepath.Base(scriptFile) + " - PawScript"
	}
	win.SetWindowTitle(title)
	win.Resize(900, 600)

	// Create terminal
	winTerminal, err := purfectermqt.New(purfectermqt.Options{
		Cols:           100,
		Rows:           30,
		ScrollbackSize: 10000,
		FontFamily:     getFontFamily(),
		FontSize:       getFontSize(),
		Scheme: purfecterm.ColorScheme{
			Foreground: getTerminalForeground(),
			Background: getTerminalBackground(),
			Cursor:     purfecterm.TrueColor(255, 255, 255),
			Selection:  purfecterm.TrueColor(68, 68, 68),
			Palette:    getColorPalette(),
			BlinkMode:  getBlinkMode(),
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create terminal: %v\n", err)
		os.Exit(1)
	}

	// Set font fallbacks
	winTerminal.SetFontFallbacks(getFontFamilyUnicode(), getFontFamilyCJK())

	// Set up terminal theme from config
	prefersDark := isTermThemeDark()
	winTerminal.Buffer().SetPreferredDarkTheme(prefersDark)
	winTerminal.Buffer().SetDarkTheme(prefersDark)

	// Set up theme change callback (for CSI ? 5 h/l escape sequences)
	winTerminal.Buffer().SetThemeChangeCallback(func(isDark bool) {
		winTerminal.SetColorScheme(getColorSchemeForTheme(isDark))
	})

	// In standalone script mode, script is always running
	winScriptRunning := true

	// Create splitter for toolbar strip + terminal
	winSplitter := qt.NewQSplitter3(qt.Horizontal)

	// Create toolbar strip for this window (script windows only have narrow strip, no wide panel)
	winNarrowStrip, winStripMenuBtn, _ := createToolbarStripForWindow(win.QWidget, true, winTerminal, func() bool {
		return winScriptRunning
	}, func() {
		win.Close()
	})
	winNarrowStrip.SetFixedWidth(minNarrowStripWidth)
	// Start visible with hamburger menu
	winNarrowStrip.Show()
	winStripMenuBtn.Show()

	// Register the toolbar data for theme updates (even without REPL)
	qtToolbarDataMu.Lock()
	runScriptToolbarData := &QtWindowToolbarData{
		strip:      winNarrowStrip,
		menuButton: winStripMenuBtn,
		terminal:   winTerminal,
	}
	qtToolbarDataByWindow[win] = runScriptToolbarData
	qtToolbarDataMu.Unlock()

	winSplitter.AddWidget(winNarrowStrip)
	winSplitter.AddWidget(winTerminal.Widget())

	// Set stretch factors so strip is fixed and terminal is flexible
	winSplitter.SetStretchFactor(0, 0)
	winSplitter.SetStretchFactor(1, 1)

	// Set initial sizes
	winSplitter.SetSizes([]int{minNarrowStripWidth, 900 - minNarrowStripWidth})

	// Script windows only have two positions: 0 (collapsed) or minNarrowStripWidth (visible)
	winSplitter.OnSplitterMoved(func(pos int, index int) {
		if index != 1 {
			return
		}
		if pos == 0 {
			// Already collapsed, do nothing
		} else if pos < minNarrowStripWidth/2 {
			// Less than half - snap to collapsed
			winSplitter.SetSizes([]int{0, winSplitter.Width()})
		} else if pos != minNarrowStripWidth {
			// More than half but not at fixed width - snap to visible
			winSplitter.SetSizes([]int{minNarrowStripWidth, winSplitter.Width() - minNarrowStripWidth})
		}
	})

	win.SetCentralWidget(winSplitter.QWidget)

	// Create I/O channels for this window
	winStdinReader, winStdinWriter := io.Pipe()

	width, height := 100, 30
	winTermCaps := &pawscript.TerminalCapabilities{
		TermType:      "gui-console",
		IsTerminal:    true,
		SupportsANSI:  true,
		SupportsColor: true,
		ColorDepth:    256,
		Width:         width,
		Height:        height,
		SupportsInput: true,
		EchoEnabled:   false,
		LineMode:      false,
		Metadata:      make(map[string]interface{}),
	}

	// Non-blocking output queue
	winOutputQueue := make(chan interface{}, 256)
	go func() {
		for item := range winOutputQueue {
			switch v := item.(type) {
			case []byte:
				winTerminal.Feed(string(v))
			case string:
				winTerminal.Feed(v)
			case chan struct{}:
				close(v)
			}
		}
	}()

	winOutCh := &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		Terminal:         winTermCaps,
		NativeSend: func(v interface{}) error {
			var text string
			switch d := v.(type) {
			case []byte:
				text = string(d)
			case string:
				text = d
			default:
				text = fmt.Sprintf("%v", v)
			}
			text = strings.ReplaceAll(text, "\r\n", "\n")
			text = strings.ReplaceAll(text, "\n", "\r\n")
			select {
			case winOutputQueue <- []byte(text):
			default:
			}
			return nil
		},
		NativeRecv: func() (interface{}, error) {
			return nil, fmt.Errorf("cannot receive from console_out")
		},
		NativeFlush: func() error {
			writerDone := make(chan struct{})
			select {
			case winOutputQueue <- writerDone:
				<-writerDone
			default:
			}
			return nil
		},
	}

	// Non-blocking input queue
	winInputQueue := make(chan byte, 256)
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := winStdinReader.Read(buf)
			if err != nil || n == 0 {
				close(winInputQueue)
				return
			}
			select {
			case winInputQueue <- buf[0]:
			default:
				select {
				case <-winInputQueue:
				default:
				}
				select {
				case winInputQueue <- buf[0]:
				default:
				}
			}
		}
	}()

	winInCh := &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		Terminal:         winTermCaps,
		NativeRecv: func() (interface{}, error) {
			b, ok := <-winInputQueue
			if !ok {
				return nil, fmt.Errorf("input closed")
			}
			return []byte{b}, nil
		},
		NativeSend: func(v interface{}) error {
			return fmt.Errorf("cannot send to console_in")
		},
	}

	// Wire keyboard input
	winTerminal.SetInputCallback(func(data []byte) {
		winStdinWriter.Write(data)
	})

	// Clean up on window close
	win.OnDestroyed(func() {
		// Clean up toolbar data
		qtToolbarDataMu.Lock()
		delete(qtToolbarDataByWindow, win)
		qtToolbarDataMu.Unlock()
		winStdinWriter.Close()
	})

	win.Show()

	// Create PawScript interpreter
	ps := pawscript.New(&pawscript.Config{
		Debug:                false,
		AllowMacros:          true,
		EnableSyntacticSugar: true,
		ShowErrorContext:     true,
		ContextLines:         2,
		FileAccess:           fileAccess,
		OptLevel:             pawscript.OptimizationLevel(optLevel),
		ScriptDir:            scriptDir,
	})

	ioConfig := &pawscript.IOChannelConfig{
		Stdout: winOutCh,
		Stdin:  winInCh,
		Stderr: winOutCh,
	}
	ps.RegisterStandardLibraryWithIO(scriptArgs, ioConfig)

	// Run script in goroutine
	go func() {
		time.Sleep(100 * time.Millisecond) // Let window initialize

		var result pawscript.Result
		if scriptFile != "" {
			result = ps.ExecuteFile(scriptContent, scriptFile)
		} else {
			result = ps.Execute(scriptContent)
		}

		if winOutCh.NativeFlush != nil {
			winOutCh.NativeFlush()
		}

		if result == pawscript.BoolStatus(false) {
			winTerminal.Feed("\r\n[Script execution failed]\r\n")
		} else {
			winTerminal.Feed("\r\n[Script completed]\r\n")
		}
	}()

	qt.QApplication_Exec()
}

// setupQuitShortcut configures the keyboard shortcut to quit the application
func setupQuitShortcut() {
	quitShortcut := getQuitShortcut()
	if quitShortcut == "" {
		return // Disabled
	}

	var keySequence string
	switch quitShortcut {
	case "Cmd+Q":
		// On macOS, Qt swaps Ctrl/Meta, so "Ctrl+Q" responds to physical Cmd key
		// On other platforms, use Meta+Q
		if runtime.GOOS == "darwin" {
			keySequence = "Ctrl+Q" // Physical Cmd+Q on macOS
		} else {
			keySequence = "Meta+Q"
		}
	case "Ctrl+Q":
		// Note: Ctrl+Q should generally not be used (conflicts with terminal)
		// On macOS, Qt's "Meta+Q" responds to physical Ctrl key
		if runtime.GOOS == "darwin" {
			keySequence = "Meta+Q" // Physical Ctrl+Q on macOS
		} else {
			keySequence = "Ctrl+Q"
		}
	case "Alt+F4":
		keySequence = "Alt+F4"
	default:
		return
	}

	shortcut := qt.NewQShortcut2(qt.NewQKeySequence2(keySequence), mainWindow.QWidget)
	shortcut.OnActivated(func() {
		mainWindow.Close()
	})
}

func createFilePanel() *qt.QWidget {
	panel := qt.NewQWidget2()
	layout := qt.NewQVBoxLayout2()
	layout.SetContentsMargins(4, 4, 4, 4)
	layout.SetSpacing(4)
	panel.SetLayout(layout.QLayout)

	// Store reference for collapse handling
	launcherWidePanel = panel

	// Top row: path selector + hamburger menu button
	topRow := qt.NewQWidget2()
	topRowLayout := qt.NewQHBoxLayout2()
	topRowLayout.SetContentsMargins(0, 0, 0, 0)
	topRowLayout.SetSpacing(4)
	topRow.SetLayout(topRowLayout.QLayout)

	// Path selector button with dropdown menu - styled like other buttons
	pathButton = qt.NewQPushButton3("")
	pathButton.SetSizePolicy(*qt.NewQSizePolicy2(qt.QSizePolicy__Ignored, qt.QSizePolicy__Fixed))
	pathButton.SetStyleSheet("text-align: left; padding-left: 6px;")

	// Create the dropdown menu
	pathMenu = qt.NewQMenu2()
	pathButton.SetMenu(pathMenu)

	topRowLayout.AddWidget2(pathButton.QWidget, 1)

	// Hamburger menu button (shown when narrow strip is hidden)
	// Uses the shared launcherMenu which is created before createFilePanel is called
	launcherMenuButton = createHamburgerButton(launcherMenu)
	topRowLayout.AddWidget(launcherMenuButton.QWidget)

	layout.AddWidget(topRow)

	// File list
	fileList = qt.NewQListWidget2()
	fileList.SetIconSize(qt.NewQSize2(fileListIconSize, fileListIconSize))
	fileList.OnItemDoubleClicked(func(item *qt.QListWidgetItem) {
		handleFileActivated(item)
	})
	fileList.OnCurrentItemChanged(func(current *qt.QListWidgetItem, previous *qt.QListWidgetItem) {
		onSelectionChanged(current, previous)
	})
	layout.AddWidget2(fileList.QWidget, 1)

	// Run and Browse buttons
	buttonLayout := qt.NewQHBoxLayout2()

	runButton = qt.NewQPushButton3("Run")
	runButton.OnClicked(func() { runSelectedFile() })
	buttonLayout.AddWidget(runButton.QWidget)

	browseButton = qt.NewQPushButton3("Browse...")
	browseButton.OnClicked(func() { browseFolder() })
	buttonLayout.AddWidget(browseButton.QWidget)

	layout.AddLayout(buttonLayout.QLayout)

	return panel
}

func createTerminalPanel() *qt.QWidget {
	panel := qt.NewQWidget2()
	layout := qt.NewQVBoxLayout2()
	layout.SetContentsMargins(0, 0, 0, 0)
	layout.SetSpacing(0)
	panel.SetLayout(layout.QLayout)

	// Create terminal with color scheme from config
	var err error
	terminal, err = purfectermqt.New(purfectermqt.Options{
		Cols:           100,
		Rows:           30,
		ScrollbackSize: 10000,
		FontFamily:     getFontFamily(),
		FontSize:       getFontSize(),
		Scheme: purfecterm.ColorScheme{
			Foreground: getTerminalForeground(),
			Background: getTerminalBackground(),
			Cursor:     purfecterm.TrueColor(255, 255, 255),
			Selection:  purfecterm.TrueColor(68, 68, 68),
			Palette:    getColorPalette(),
			BlinkMode:  getBlinkMode(),
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create terminal: %v\n", err)
		os.Exit(1)
	}

	// Set font fallbacks for Unicode/CJK characters
	terminal.SetFontFallbacks(getFontFamilyUnicode(), getFontFamilyCJK())

	// Set up terminal theme from config
	prefersDark := isTermThemeDark()
	terminal.Buffer().SetPreferredDarkTheme(prefersDark)
	terminal.Buffer().SetDarkTheme(prefersDark)

	// Set up theme change callback (for CSI ? 5 h/l escape sequences)
	terminal.Buffer().SetThemeChangeCallback(func(isDark bool) {
		terminal.SetColorScheme(getColorSchemeForTheme(isDark))
	})

	layout.AddWidget2(terminal.Widget(), 1)

	return panel
}

func setupConsoleIO() {
	// Create pipes for stdin
	stdinReader, stdinWriter = io.Pipe()

	// Get terminal capabilities from the widget (auto-updates on resize)
	termCaps := terminal.GetTerminalCapabilities()

	// Output queue for non-blocking writes to terminal
	outputQueue := make(chan interface{}, 256)

	// Start output writer goroutine
	go func() {
		for v := range outputQueue {
			switch d := v.(type) {
			case []byte:
				terminal.Feed(string(d))
			case string:
				terminal.Feed(d)
			case chan struct{}:
				// Sentinel for flush synchronization
				close(d)
			}
		}
	}()

	// Create console output channel
	consoleOutCh = &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		Terminal:         termCaps,
		NativeSend: func(v interface{}) error {
			var text string
			switch d := v.(type) {
			case []byte:
				text = string(d)
			case string:
				text = d
			default:
				text = fmt.Sprintf("%v", v)
			}
			// Normalize newlines for terminal
			text = strings.ReplaceAll(text, "\r\n", "\n")
			text = strings.ReplaceAll(text, "\n", "\r\n")
			select {
			case outputQueue <- []byte(text):
			default:
				// Queue full - drop to prevent deadlock
			}
			return nil
		},
		NativeRecv: func() (interface{}, error) {
			return nil, fmt.Errorf("cannot receive from console_out")
		},
		NativeFlush: func() error {
			// Wait for outputQueue to drain
			writerDone := make(chan struct{})
			select {
			case outputQueue <- writerDone:
				<-writerDone
			default:
			}
			return nil
		},
	}

	// Set up the global flushFunc
	flushFunc = func() {
		if consoleOutCh != nil {
			consoleOutCh.Flush()
		}
	}

	// Non-blocking input queue
	inputQueue := make(chan byte, 256)

	// Reader goroutine: drains pipe and puts bytes into queue
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := stdinReader.Read(buf)
			if err != nil || n == 0 {
				close(inputQueue)
				return
			}
			select {
			case inputQueue <- buf[0]:
			default:
				// Drop oldest if full
				select {
				case <-inputQueue:
				default:
				}
				select {
				case inputQueue <- buf[0]:
				default:
				}
			}
		}
	}()

	// Create console input channel
	consoleInCh = &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		Terminal:         termCaps,
		NativeRecv: func() (interface{}, error) {
			b, ok := <-inputQueue
			if !ok {
				return nil, fmt.Errorf("input closed")
			}
			return []byte{b}, nil
		},
		NativeSend: func(v interface{}) error {
			return fmt.Errorf("cannot send to console_in")
		},
	}

	clearInputFunc = func() {
		for {
			select {
			case <-inputQueue:
			default:
				return
			}
		}
	}

	// Wire keyboard input from terminal to stdin pipe or REPL
	terminal.SetInputCallback(func(data []byte) {
		scriptMu.Lock()
		isRunning := scriptRunning
		scriptMu.Unlock()

		if isRunning {
			// Script is running, send to stdin pipe
			if stdinWriter != nil {
				stdinWriter.Write(data)
			}
		} else if consoleREPL != nil && consoleREPL.IsRunning() {
			// REPL is active
			if consoleREPL.IsBusy() {
				// REPL is executing a command (e.g., read) - send to stdin pipe
				if stdinWriter != nil {
					stdinWriter.Write(data)
				}
			} else {
				// REPL is waiting for input - send to REPL for line editing
				consoleREPL.HandleInput(data)
			}
		}
	})
}

func startREPL() {
	// Create and start the REPL for interactive mode
	// ShowBanner is false because we print our own welcome message before starting
	consoleREPL = pawscript.NewREPL(pawscript.REPLConfig{
		Debug:        false,
		Unrestricted: false,
		OptLevel:     getOptimizationLevel(),
		ShowBanner:   false,
		IOConfig: &pawscript.IOChannelConfig{
			Stdout: consoleOutCh,
			Stdin:  consoleInCh,
			Stderr: consoleOutCh,
		},
	}, func(s string) {
		// Output to terminal
		terminal.Feed(s)
	})
	// Set flush callback to ensure output appears before blocking execution
	consoleREPL.SetFlush(func() {
		// Force immediate repaint to display output before blocking operations
		terminal.Flush()
	})
	// Set background color for prompt color selection
	bg := getTerminalBackground()
	consoleREPL.SetBackgroundRGB(bg.R, bg.G, bg.B)
	consoleREPL.SetPSLColors(getPSLColors())
	consoleREPL.Start()

	// Register the dummy_button command with the REPL's PawScript instance
	// Create launcher toolbar data that uses the global launcher strip
	launcherToolbarData = &QtWindowToolbarData{
		strip:    launcherNarrowStrip,
		terminal: terminal,
		updateFunc: func() {
			// Copy buttons to global for launcher-specific visibility logic
			launcherRegisteredBtns = launcherToolbarData.registeredBtns
			updateLauncherToolbarButtons()
		},
	}
	registerDummyButtonCommand(consoleREPL.GetPawScript(), launcherToolbarData)
}

// iconType represents the type of icon for a file list item
type iconType int

const (
	iconTypeFolder iconType = iota
	iconTypeFolderUp
	iconTypePawFile
)

// fileItemData stores path and isDir for list items
type fileItemData struct {
	path     string
	isDir    bool
	iconType iconType
}

var fileItemDataMap = make(map[unsafe.Pointer]fileItemData)
var fileItemDataMu sync.Mutex
var previousSelectedItem *qt.QListWidgetItem

// updatePathButtonText updates the button text with elision based on current width
func updatePathButtonText() {
	if pathButton == nil {
		return
	}
	// Compute elided text to fit in button width (elide at start to show end of path)
	buttonWidth := pathButton.Width() - 40 // Leave room for dropdown arrow and padding
	if buttonWidth < 50 {
		buttonWidth = 50
	}
	fm := qt.NewQFontMetrics(pathButton.Font())
	elidedText := fm.ElidedText(currentDir, qt.ElideLeft, buttonWidth)
	pathButton.SetText(elidedText)
}

// updatePathMenu populates the path menu with Home, Examples, recent paths, and Clear option
func updatePathMenu() {
	if pathButton == nil || pathMenu == nil {
		return
	}

	// Update button text
	updatePathButtonText()

	// Clear existing menu items
	pathMenu.Clear()

	// Add current path as disabled info item
	currentAction := pathMenu.AddAction(currentDir)
	currentAction.SetEnabled(false)

	pathMenu.AddSeparator()

	// Add Home directory
	if home := getHomeDir(); home != "" {
		homeAction := pathMenu.AddAction("Home")
		if icon := createIconFromSVG(homeIconSVG, 16); icon != nil {
			homeAction.SetIcon(icon)
		}
		homeAction.OnTriggered(func() {
			if info, err := os.Stat(home); err == nil && info.IsDir() {
				loadDirectory(home)
			}
		})
	}

	// Add Examples directory
	if examples := getExamplesDir(); examples != "" {
		examplesAction := pathMenu.AddAction("Examples")
		if icon := createIconFromSVG(folderIconSVG, 16); icon != nil {
			examplesAction.SetIcon(icon)
		}
		examplesAction.OnTriggered(func() {
			if info, err := os.Stat(examples); err == nil && info.IsDir() {
				loadDirectory(examples)
			}
		})
	}

	// Add recent paths
	recentPaths := getRecentPaths()
	if len(recentPaths) > 0 {
		pathMenu.AddSeparator()
		for _, p := range recentPaths {
			path := p // Capture for closure
			action := pathMenu.AddAction(path)
			action.OnTriggered(func() {
				if info, err := os.Stat(path); err == nil && info.IsDir() {
					loadDirectory(path)
				}
			})
		}
	}

	// Add Clear Recent Paths option
	if len(recentPaths) > 0 {
		pathMenu.AddSeparator()
		clearAction := pathMenu.AddAction("Clear Recent Paths")
		if icon := createIconFromSVG(trashIconSVG, 16); icon != nil {
			clearAction.SetIcon(icon)
		}
		clearAction.OnTriggered(func() {
			clearRecentPaths()
			updatePathMenu()
		})
	}
}

func loadDirectory(dir string) {
	currentDir = dir
	updatePathMenu()

	fileList.Clear()

	// Clear old item data
	fileItemDataMu.Lock()
	fileItemDataMap = make(map[unsafe.Pointer]fileItemData)
	fileItemDataMu.Unlock()

	entries, err := os.ReadDir(dir)
	if err != nil {
		terminal.Feed(fmt.Sprintf("Error reading directory: %v\r\n", err))
		return
	}

	// Create custom SVG icons for file list
	upIcon := createIconFromSVG(folderUpIconSVG, fileListIconSize)
	folderIcon := createIconFromSVG(folderIconSVG, fileListIconSize)
	fileIcon := createIconFromSVG(pawFileIconSVG, fileListIconSize)

	// Reset previous selected item when directory changes
	previousSelectedItem = nil

	// Add parent directory entry (except at root)
	if dir != "/" && filepath.Dir(dir) != dir {
		item := qt.NewQListWidgetItem7("..", fileList)
		if upIcon != nil {
			item.SetIcon(upIcon)
		}
		fileItemDataMu.Lock()
		fileItemDataMap[item.UnsafePointer()] = fileItemData{
			path:     filepath.Dir(dir),
			isDir:    true,
			iconType: iconTypeFolderUp,
		}
		fileItemDataMu.Unlock()
	}

	// Add directories first
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			item := qt.NewQListWidgetItem7(entry.Name(), fileList)
			if folderIcon != nil {
				item.SetIcon(folderIcon)
			}
			// Store data using pointer map
			fileItemDataMu.Lock()
			fileItemDataMap[item.UnsafePointer()] = fileItemData{
				path:     filepath.Join(dir, entry.Name()),
				isDir:    true,
				iconType: iconTypeFolder,
			}
			fileItemDataMu.Unlock()
		}
	}

	// Add .paw files (case-insensitive)
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".paw") {
			item := qt.NewQListWidgetItem7(entry.Name(), fileList)
			if fileIcon != nil {
				item.SetIcon(fileIcon)
			}
			// Store data using pointer map
			fileItemDataMu.Lock()
			fileItemDataMap[item.UnsafePointer()] = fileItemData{
				path:     filepath.Join(dir, entry.Name()),
				isDir:    false,
				iconType: iconTypePawFile,
			}
			fileItemDataMu.Unlock()
		}
	}

	saveBrowseDir(dir)
}

func handleFileActivated(item *qt.QListWidgetItem) {
	fileItemDataMu.Lock()
	data, ok := fileItemDataMap[item.UnsafePointer()]
	fileItemDataMu.Unlock()

	if !ok {
		return
	}

	if data.isDir {
		loadDirectory(data.path)
	} else {
		runScript(data.path)
	}
}

func navigateUp() {
	parent := filepath.Dir(currentDir)
	if parent != currentDir {
		loadDirectory(parent)
	}
}

func onSelectionChanged(current *qt.QListWidgetItem, previous *qt.QListWidgetItem) {
	// Restore previous item's icon to normal theme
	if previous != nil {
		fileItemDataMu.Lock()
		prevData, prevOk := fileItemDataMap[previous.UnsafePointer()]
		fileItemDataMu.Unlock()
		if prevOk {
			var icon *qt.QIcon
			switch prevData.iconType {
			case iconTypeFolderUp:
				icon = createIconFromSVG(folderUpIconSVG, fileListIconSize)
			case iconTypeFolder:
				icon = createIconFromSVG(folderIconSVG, fileListIconSize)
			case iconTypePawFile:
				icon = createIconFromSVG(pawFileIconSVG, fileListIconSize)
			}
			if icon != nil {
				previous.SetIcon(icon)
			}
		}
	}

	if current == nil || runButton == nil {
		return
	}

	fileItemDataMu.Lock()
	data, ok := fileItemDataMap[current.UnsafePointer()]
	fileItemDataMu.Unlock()

	if !ok {
		runButton.SetText("Run")
		return
	}

	// Set current item's icon to dark mode (white fill for selected row)
	var darkIcon *qt.QIcon
	switch data.iconType {
	case iconTypeFolderUp:
		darkIcon = createDarkIconFromSVG(folderUpIconSVG, fileListIconSize)
	case iconTypeFolder:
		darkIcon = createDarkIconFromSVG(folderIconSVG, fileListIconSize)
	case iconTypePawFile:
		darkIcon = createDarkIconFromSVG(pawFileIconSVG, fileListIconSize)
	}
	if darkIcon != nil {
		current.SetIcon(darkIcon)
	}

	if data.isDir {
		runButton.SetText("Open")
	} else {
		runButton.SetText("Run")
	}
}

func browseFolder() {
	// Open file dialog filtered to .paw files
	file := qt.QFileDialog_GetOpenFileName4(
		mainWindow.QWidget,
		"Open PawScript File",
		currentDir,
		"PawScript files (*.paw);;All files (*)",
	)
	if file != "" {
		// Navigate to the file's directory and run the script
		currentDir = filepath.Dir(file)
		loadDirectory(currentDir)
		runScript(file)
	}
}

func runSelectedFile() {
	items := fileList.SelectedItems()
	if len(items) == 0 {
		terminal.Feed("No file selected.\r\n")
		return
	}

	item := items[0]
	fileItemDataMu.Lock()
	data, ok := fileItemDataMap[item.UnsafePointer()]
	fileItemDataMu.Unlock()

	if !ok {
		return
	}

	if data.isDir {
		loadDirectory(data.path)
	} else {
		runScript(data.path)
	}
}

func runScript(filePath string) {
	scriptMu.Lock()
	if scriptRunning {
		scriptMu.Unlock()
		// Script already running in main window - spawn a new console window
		createConsoleWindow(filePath)
		return
	}
	scriptRunning = true
	scriptMu.Unlock()

	// Stop the REPL while script runs
	if consoleREPL != nil {
		consoleREPL.Stop()
	}

	terminal.Feed(fmt.Sprintf("\r\n--- Running: %s ---\r\n\r\n", filepath.Base(filePath)))

	// Clear any buffered input from previous script runs
	if clearInputFunc != nil {
		clearInputFunc()
	}

	// Read script content
	content, err := os.ReadFile(filePath)
	if err != nil {
		terminal.Feed(fmt.Sprintf("Error reading script file: %v\r\n", err))
		scriptMu.Lock()
		scriptRunning = false
		scriptMu.Unlock()
		return
	}

	scriptDir := filepath.Dir(filePath)
	absScript, _ := filepath.Abs(filePath)
	if absScript != "" {
		scriptDir = filepath.Dir(absScript)
	}

	// Add the script's directory to recent paths for the combo box
	addRecentPath(scriptDir)

	// Create file access config
	cwd, _ := os.Getwd()
	tmpDir := os.TempDir()
	fileAccess := &pawscript.FileAccessConfig{
		ReadRoots:  []string{scriptDir, cwd, tmpDir},
		WriteRoots: []string{filepath.Join(scriptDir, "saves"), filepath.Join(scriptDir, "output"), filepath.Join(cwd, "saves"), filepath.Join(cwd, "output"), tmpDir},
		ExecRoots:  []string{filepath.Join(scriptDir, "helpers"), filepath.Join(scriptDir, "bin")},
	}

	// Create a new PawScript instance for this script
	ps := pawscript.New(&pawscript.Config{
		Debug:                false,
		AllowMacros:          true,
		EnableSyntacticSugar: true,
		ShowErrorContext:     true,
		ContextLines:         2,
		FileAccess:           fileAccess,
		ScriptDir:            scriptDir,
		OptLevel:             pawscript.OptimizationLevel(getOptimizationLevel()),
	})

	// Register standard library with the console IO
	ioConfig := &pawscript.IOChannelConfig{
		Stdout: consoleOutCh,
		Stdin:  consoleInCh,
		Stderr: consoleOutCh,
	}
	ps.RegisterStandardLibraryWithIO([]string{}, ioConfig)

	// Run script in goroutine so UI stays responsive
	go func() {
		// Create an isolated snapshot for execution
		snapshot := ps.CreateRestrictedSnapshot()

		// Run the script in the isolated environment
		result := ps.ExecuteWithEnvironment(string(content), snapshot, filePath, 0, 0)

		// Flush any pending output before printing completion message
		if flushFunc != nil {
			flushFunc()
		}

		if result == pawscript.BoolStatus(false) {
			terminal.Feed("\r\n--- Script execution failed ---\r\n")
		} else {
			terminal.Feed("\r\n--- Script completed ---\r\n")
		}

		scriptMu.Lock()
		scriptRunning = false
		scriptMu.Unlock()

		// Restart the REPL
		if consoleREPL != nil {
			// Create a new REPL instance (fresh state)
			consoleREPL = pawscript.NewREPL(pawscript.REPLConfig{
				Debug:        false,
				Unrestricted: false,
				OptLevel:     getOptimizationLevel(),
				ShowBanner:   false, // Don't show banner again
				IOConfig: &pawscript.IOChannelConfig{
					Stdout: consoleOutCh,
					Stdin:  consoleInCh,
					Stderr: consoleOutCh,
				},
			}, func(s string) {
				terminal.Feed(s)
			})
			// Set flush callback to ensure output appears before blocking execution
			consoleREPL.SetFlush(func() {
				// Force immediate repaint to display output before blocking operations
				terminal.Flush()
			})
			// Set background color for prompt color selection
			bg := getTerminalBackground()
			consoleREPL.SetBackgroundRGB(bg.R, bg.G, bg.B)
			consoleREPL.SetPSLColors(getPSLColors())
			consoleREPL.Start()

			// Re-register the dummy_button command with the new REPL instance
			// Reuse the existing launcherToolbarData with the new terminal reference
			launcherToolbarData.terminal = terminal
			registerDummyButtonCommand(consoleREPL.GetPawScript(), launcherToolbarData)
		}
	}()
}

// createConsoleWindow creates a new window with just a terminal (no launcher UI)
// for running a script when the main window already has a script running
func createConsoleWindow(filePath string) {
	// Create new window
	win := qt.NewQMainWindow2()
	win.SetWindowTitle(fmt.Sprintf("PawScript - %s", filepath.Base(filePath)))
	win.SetMinimumSize2(900, 600)

	// Create terminal for this window with color scheme from config
	winTerminal, err := purfectermqt.New(purfectermqt.Options{
		Cols:           100,
		Rows:           30,
		ScrollbackSize: 10000,
		FontFamily:     getFontFamily(),
		FontSize:       getFontSize(),
		Scheme: purfecterm.ColorScheme{
			Foreground: getTerminalForeground(),
			Background: getTerminalBackground(),
			Cursor:     purfecterm.TrueColor(255, 255, 255),
			Selection:  purfecterm.TrueColor(68, 68, 68),
			Palette:    getColorPalette(),
			BlinkMode:  getBlinkMode(),
		},
	})
	if err != nil {
		terminal.Feed(fmt.Sprintf("\r\nFailed to create console window: %v\r\n", err))
		win.Close()
		return
	}

	// Set font fallbacks for Unicode/CJK characters
	winTerminal.SetFontFallbacks(getFontFamilyUnicode(), getFontFamilyCJK())

	// Set up terminal theme from config
	prefersDark := isTermThemeDark()
	winTerminal.Buffer().SetPreferredDarkTheme(prefersDark)
	winTerminal.Buffer().SetDarkTheme(prefersDark)

	// Set up theme change callback (for CSI ? 5 h/l escape sequences)
	winTerminal.Buffer().SetThemeChangeCallback(func(isDark bool) {
		winTerminal.SetColorScheme(getColorSchemeForTheme(isDark))
	})

	// Track script running state for this window
	var winScriptRunning bool
	var winScriptMu sync.Mutex

	// Create splitter for toolbar strip + terminal
	winSplitter := qt.NewQSplitter3(qt.Horizontal)

	// Create toolbar strip for this window (script windows only have narrow strip, no wide panel)
	winNarrowStrip, winStripMenuBtn, _ := createToolbarStripForWindow(win.QWidget, true, winTerminal, func() bool {
		winScriptMu.Lock()
		defer winScriptMu.Unlock()
		return winScriptRunning
	}, func() {
		win.Close()
	})
	winNarrowStrip.SetFixedWidth(minNarrowStripWidth)
	// Always show the strip (has hamburger menu)
	winNarrowStrip.Show()
	winStripMenuBtn.Show()

	winSplitter.AddWidget(winNarrowStrip)
	winSplitter.AddWidget(winTerminal.Widget())

	// Set stretch factors so strip is fixed and terminal is flexible
	winSplitter.SetStretchFactor(0, 0)
	winSplitter.SetStretchFactor(1, 1)

	// Set initial sizes - always show narrow strip
	winSplitter.SetSizes([]int{minNarrowStripWidth, 900 - minNarrowStripWidth})

	// Script windows only have two positions: 0 (collapsed) or minNarrowStripWidth (visible)
	winSplitter.OnSplitterMoved(func(pos int, index int) {
		if index != 1 {
			return
		}
		if pos == 0 {
			// Already collapsed, do nothing
		} else if pos < minNarrowStripWidth/2 {
			// Less than half - snap to collapsed
			winSplitter.SetSizes([]int{0, winSplitter.Width()})
		} else if pos != minNarrowStripWidth {
			// More than half but not at fixed width - snap to visible
			winSplitter.SetSizes([]int{minNarrowStripWidth, winSplitter.Width() - minNarrowStripWidth})
		}
	})

	win.SetCentralWidget(winSplitter.QWidget)

	// Create I/O channels for this window's console
	winStdinReader, winStdinWriter := io.Pipe()

	// Terminal capabilities for this window
	winWidth, winHeight := 100, 30
	winTermCaps := &pawscript.TerminalCapabilities{
		TermType:      "gui-console",
		IsTerminal:    true,
		SupportsANSI:  true,
		SupportsColor: true,
		ColorDepth:    256,
		Width:         winWidth,
		Height:        winHeight,
		SupportsInput: true,
		EchoEnabled:   false,
		LineMode:      false,
		Metadata:      make(map[string]interface{}),
	}

	// Non-blocking output queue
	winOutputQueue := make(chan interface{}, 256)
	go func() {
		for item := range winOutputQueue {
			switch v := item.(type) {
			case []byte:
				winTerminal.Feed(string(v))
			case string:
				winTerminal.Feed(v)
			case chan struct{}:
				close(v)
			}
		}
	}()

	winOutCh := &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		Terminal:         winTermCaps,
		NativeSend: func(v interface{}) error {
			var text string
			switch d := v.(type) {
			case []byte:
				text = string(d)
			case string:
				text = d
			default:
				text = fmt.Sprintf("%v", v)
			}
			text = strings.ReplaceAll(text, "\r\n", "\n")
			text = strings.ReplaceAll(text, "\n", "\r\n")
			select {
			case winOutputQueue <- []byte(text):
			default:
			}
			return nil
		},
		NativeRecv: func() (interface{}, error) {
			return nil, fmt.Errorf("cannot receive from console_out")
		},
		NativeFlush: func() error {
			writerDone := make(chan struct{})
			select {
			case winOutputQueue <- writerDone:
				<-writerDone
			default:
			}
			return nil
		},
	}

	// Non-blocking input queue
	winInputQueue := make(chan byte, 256)
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := winStdinReader.Read(buf)
			if err != nil || n == 0 {
				close(winInputQueue)
				return
			}
			select {
			case winInputQueue <- buf[0]:
			default:
				select {
				case <-winInputQueue:
				default:
				}
				select {
				case winInputQueue <- buf[0]:
				default:
				}
			}
		}
	}()

	winInCh := &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		Terminal:         winTermCaps,
		NativeRecv: func() (interface{}, error) {
			b, ok := <-winInputQueue
			if !ok {
				return nil, fmt.Errorf("input closed")
			}
			return []byte{b}, nil
		},
		NativeSend: func(v interface{}) error {
			return fmt.Errorf("cannot send to console_in")
		},
	}

	var winREPL *pawscript.REPL

	// Wire keyboard input
	winTerminal.SetInputCallback(func(data []byte) {
		winScriptMu.Lock()
		isRunning := winScriptRunning
		winScriptMu.Unlock()

		if isRunning {
			winStdinWriter.Write(data)
		} else if winREPL != nil && winREPL.IsRunning() {
			if winREPL.IsBusy() {
				// REPL is executing a command (e.g., read) - send to stdin pipe
				winStdinWriter.Write(data)
			} else {
				// REPL is waiting for input - send to REPL for line editing
				winREPL.HandleInput(data)
			}
		}
	})

	win.Show()

	// Run the script
	winTerminal.Feed(fmt.Sprintf("--- Running: %s ---\r\n\r\n", filepath.Base(filePath)))

	content, err := os.ReadFile(filePath)
	if err != nil {
		winTerminal.Feed(fmt.Sprintf("Error reading script file: %v\r\n", err))
		return
	}

	scriptDir := filepath.Dir(filePath)
	absScript, _ := filepath.Abs(filePath)
	if absScript != "" {
		scriptDir = filepath.Dir(absScript)
	}

	// Add the script's directory to recent paths for the combo box
	addRecentPath(scriptDir)

	cwd, _ := os.Getwd()
	tmpDir := os.TempDir()
	fileAccess := &pawscript.FileAccessConfig{
		ReadRoots:  []string{scriptDir, cwd, tmpDir},
		WriteRoots: []string{filepath.Join(scriptDir, "saves"), filepath.Join(scriptDir, "output"), filepath.Join(cwd, "saves"), filepath.Join(cwd, "output"), tmpDir},
		ExecRoots:  []string{filepath.Join(scriptDir, "helpers"), filepath.Join(scriptDir, "bin")},
	}

	ps := pawscript.New(&pawscript.Config{
		Debug:                false,
		AllowMacros:          true,
		EnableSyntacticSugar: true,
		ShowErrorContext:     true,
		ContextLines:         2,
		FileAccess:           fileAccess,
		ScriptDir:            scriptDir,
		OptLevel:             pawscript.OptimizationLevel(getOptimizationLevel()),
	})

	ioConfig := &pawscript.IOChannelConfig{
		Stdout: winOutCh,
		Stdin:  winInCh,
		Stderr: winOutCh,
	}
	ps.RegisterStandardLibraryWithIO([]string{}, ioConfig)

	winScriptMu.Lock()
	winScriptRunning = true
	winScriptMu.Unlock()

	go func() {
		snapshot := ps.CreateRestrictedSnapshot()
		result := ps.ExecuteWithEnvironment(string(content), snapshot, filePath, 0, 0)

		if winOutCh.NativeFlush != nil {
			winOutCh.NativeFlush()
		}

		if result == pawscript.BoolStatus(false) {
			winTerminal.Feed("\r\n--- Script execution failed ---\r\n")
		} else {
			winTerminal.Feed("\r\n--- Script completed ---\r\n")
		}

		winScriptMu.Lock()
		winScriptRunning = false
		winScriptMu.Unlock()

		// Start REPL for this window
		winREPL = pawscript.NewREPL(pawscript.REPLConfig{
			Debug:        false,
			Unrestricted: false,
			OptLevel:     getOptimizationLevel(),
			ShowBanner:   false,
			IOConfig: &pawscript.IOChannelConfig{
				Stdout: winOutCh,
				Stdin:  winInCh,
				Stderr: winOutCh,
			},
		}, func(s string) {
			winTerminal.Feed(s)
		})
		// Set flush callback to ensure output appears before blocking execution
		winREPL.SetFlush(func() {
			// Force immediate repaint to display output before blocking operations
			winTerminal.Flush()
		})
		// Set background color for prompt color selection
		bg := getTerminalBackground()
		winREPL.SetBackgroundRGB(bg.R, bg.G, bg.B)
		winREPL.SetPSLColors(getPSLColors())
		winREPL.Start()

		// Register the dummy_button command with the window's REPL
		// Create window-specific toolbar data
		winToolbarData := &QtWindowToolbarData{
			strip:      winNarrowStrip,
			menuButton: winStripMenuBtn,
			terminal:   winTerminal,
		}
		winToolbarData.updateFunc = func() {
			updateWindowToolbarButtons(winToolbarData.strip, winToolbarData.registeredBtns)
		}
		registerDummyButtonCommand(winREPL.GetPawScript(), winToolbarData)
	}()
}
