package purfecterm

import (
	"strconv"
	"strings"
)

// Parser states
type parserState int

const (
	stateGround      parserState = iota
	stateEscape                  // After ESC
	stateCSI                     // After ESC [
	stateCSIParam                // Reading CSI parameters
	stateOSC                     // After ESC ]
	stateOSCString               // Reading OSC string
	stateCharset                 // After ESC ( or ESC )
	stateDECLineAttr             // After ESC # (waiting for line attribute command)
)

// Parser parses ANSI escape sequences and updates a Buffer
type Parser struct {
	buffer *Buffer
	state  parserState

	// CSI sequence accumulator
	csiParams       []int
	csiPrivate      byte // For private sequences like ?25h
	csiIntermediate byte // For sequences with intermediate bytes like DECSCUSR (SP q)
	csiBuf          strings.Builder

	// OSC accumulator
	oscCmd int             // OSC command number (e.g., 7000 for palette, 7001 for glyph)
	oscBuf strings.Builder // OSC command arguments

	// UTF-8 multi-byte handling
	utf8Buf  []byte
	utf8Need int
}

// NewParser creates a new ANSI parser for the given buffer
func NewParser(buffer *Buffer) *Parser {
	return &Parser{
		buffer:    buffer,
		state:     stateGround,
		csiParams: make([]int, 0, 16),
	}
}

// Parse processes input data and updates the terminal buffer
func (p *Parser) Parse(data []byte) {
	for _, b := range data {
		p.processByte(b)
	}
}

// ParseString processes a string and updates the terminal buffer
func (p *Parser) ParseString(data string) {
	p.Parse([]byte(data))
}

func (p *Parser) processByte(b byte) {
	// Handle UTF-8 continuation bytes
	if p.utf8Need > 0 {
		if b&0xC0 == 0x80 {
			p.utf8Buf = append(p.utf8Buf, b)
			p.utf8Need--
			if p.utf8Need == 0 {
				// Complete UTF-8 sequence
				r := decodeUTF8(p.utf8Buf)
				if p.state == stateGround {
					p.buffer.WriteChar(r)
				}
				p.utf8Buf = p.utf8Buf[:0]
			}
			return
		}
		// Invalid UTF-8, reset
		p.utf8Buf = p.utf8Buf[:0]
		p.utf8Need = 0
	}

	// Check for UTF-8 start bytes in ground state
	if p.state == stateGround {
		if b&0xE0 == 0xC0 {
			// 2-byte sequence
			p.utf8Buf = append(p.utf8Buf[:0], b)
			p.utf8Need = 1
			return
		} else if b&0xF0 == 0xE0 {
			// 3-byte sequence
			p.utf8Buf = append(p.utf8Buf[:0], b)
			p.utf8Need = 2
			return
		} else if b&0xF8 == 0xF0 {
			// 4-byte sequence
			p.utf8Buf = append(p.utf8Buf[:0], b)
			p.utf8Need = 3
			return
		}
	}

	switch p.state {
	case stateGround:
		p.handleGround(b)
	case stateEscape:
		p.handleEscape(b)
	case stateCSI, stateCSIParam:
		p.handleCSI(b)
	case stateOSC:
		p.handleOSC(b)
	case stateOSCString:
		p.handleOSCString(b)
	case stateCharset:
		// Consume one character and return to ground
		p.state = stateGround
	case stateDECLineAttr:
		p.handleDECLineAttr(b)
	}
}

func decodeUTF8(buf []byte) rune {
	if len(buf) == 0 {
		return 0xFFFD
	}
	switch len(buf) {
	case 2:
		return rune(buf[0]&0x1F)<<6 | rune(buf[1]&0x3F)
	case 3:
		return rune(buf[0]&0x0F)<<12 | rune(buf[1]&0x3F)<<6 | rune(buf[2]&0x3F)
	case 4:
		return rune(buf[0]&0x07)<<18 | rune(buf[1]&0x3F)<<12 | rune(buf[2]&0x3F)<<6 | rune(buf[3]&0x3F)
	default:
		return 0xFFFD
	}
}

func (p *Parser) handleGround(b byte) {
	switch b {
	case 0x00: // NUL - ignore
	case 0x07: // BEL - bell (ignore for now)
	case 0x08: // BS - backspace
		p.buffer.Backspace()
	case 0x09: // HT - horizontal tab
		p.buffer.Tab()
	case 0x0A: // LF - line feed
		p.buffer.LineFeed()
	case 0x0B, 0x0C: // VT, FF - treated as line feed
		p.buffer.LineFeed()
	case 0x0D: // CR - carriage return
		p.buffer.CarriageReturn()
	case 0x1B: // ESC
		p.state = stateEscape
	default:
		if b >= 0x20 && b < 0x7F {
			// Printable ASCII
			p.buffer.WriteChar(rune(b))
		}
	}
}

func (p *Parser) handleEscape(b byte) {
	switch b {
	case '[': // CSI - Control Sequence Introducer
		p.state = stateCSI
		p.csiParams = p.csiParams[:0]
		p.csiPrivate = 0
		p.csiIntermediate = 0
		p.csiBuf.Reset()
	case ']': // OSC - Operating System Command
		p.state = stateOSC
		p.oscBuf.Reset()
	case '(', ')': // Character set designation
		p.state = stateCharset
	case '#': // DEC line attribute commands (DECDHL, DECDWL, DECSWL, DECALN)
		p.state = stateDECLineAttr
	case '7': // DECSC - Save Cursor
		p.buffer.SaveCursor()
		p.state = stateGround
	case '8': // DECRC - Restore Cursor
		p.buffer.RestoreCursor()
		p.state = stateGround
	case 'c': // RIS - Reset to Initial State
		p.buffer.ClearScreen()
		p.buffer.SetCursor(0, 0)
		p.buffer.ResetAttributes()
		p.state = stateGround
	case 'D': // IND - Index (move down one line, scroll if needed)
		_, rows := p.buffer.GetSize()
		_, y := p.buffer.GetCursor()
		if y >= rows-1 {
			p.buffer.ScrollUp(1)
		} else {
			p.buffer.MoveCursorDown(1)
		}
		p.state = stateGround
	case 'E': // NEL - Next Line
		p.buffer.CarriageReturn()
		p.buffer.LineFeed()
		p.state = stateGround
	case 'M': // RI - Reverse Index (move up one line, scroll if needed)
		_, y := p.buffer.GetCursor()
		if y == 0 {
			p.buffer.ScrollDown(1)
		} else {
			p.buffer.MoveCursorUp(1)
		}
		p.state = stateGround
	case '=': // DECKPAM - Keypad Application Mode
		p.state = stateGround
	case '>': // DECKPNM - Keypad Numeric Mode
		p.state = stateGround
	default:
		// Unknown escape sequence, return to ground state
		p.state = stateGround
	}
}

// handleDECLineAttr handles ESC # sequences for line attributes
// ESC#3 - DECDHL: Double-height line, top half
// ESC#4 - DECDHL: Double-height line, bottom half
// ESC#5 - DECSWL: Single-width line (normal)
// ESC#6 - DECDWL: Double-width line
// ESC#8 - DECALN: Screen alignment test (fill screen with 'E')
func (p *Parser) handleDECLineAttr(b byte) {
	switch b {
	case '3': // DECDHL top half
		p.buffer.SetLineAttribute(LineAttrDoubleTop)
	case '4': // DECDHL bottom half
		p.buffer.SetLineAttribute(LineAttrDoubleBottom)
	case '5': // DECSWL - single width (normal)
		p.buffer.SetLineAttribute(LineAttrNormal)
	case '6': // DECDWL - double width
		p.buffer.SetLineAttribute(LineAttrDoubleWidth)
	case '8': // DECALN - Screen alignment test (fill with 'E')
		cols, rows := p.buffer.GetSize()
		for y := 0; y < rows; y++ {
			p.buffer.SetCursor(0, y)
			p.buffer.SetLineAttribute(LineAttrNormal)
			for x := 0; x < cols; x++ {
				p.buffer.WriteChar('E')
			}
		}
		p.buffer.SetCursor(0, 0)
	}
	p.state = stateGround
}

func (p *Parser) handleCSI(b byte) {
	if p.state == stateCSI {
		// First byte after ESC [
		if b == '?' || b == '>' || b == '!' || b == '<' {
			p.csiPrivate = b
			p.state = stateCSIParam
			return
		}
		p.state = stateCSIParam
	}

	// Collect parameter bytes
	if b >= '0' && b <= '9' {
		p.csiBuf.WriteByte(b)
		return
	}

	if b == ';' {
		// Parameter separator
		p.parseCSIParam()
		p.csiBuf.Reset()
		return
	}

	if b == ':' {
		// Sub-parameter separator (used in some SGR sequences)
		p.csiBuf.WriteByte(b)
		return
	}

	// Intermediate bytes (0x20-0x2F) - used in sequences like DECSCUSR (ESC [ Ps SP q)
	if b >= 0x20 && b <= 0x2F {
		p.parseCSIParam() // Parse any parameter before the intermediate
		p.csiIntermediate = b
		return
	}

	// Final byte - execute the sequence
	p.parseCSIParam() // Parse any remaining parameter
	p.executeCSI(b)
	p.state = stateGround
}

func (p *Parser) parseCSIParam() {
	s := p.csiBuf.String()
	if s == "" {
		p.csiParams = append(p.csiParams, 0) // Default value
	} else {
		n, _ := strconv.Atoi(s)
		p.csiParams = append(p.csiParams, n)
	}
}

func (p *Parser) getParam(idx, defaultVal int) int {
	if idx < len(p.csiParams) && p.csiParams[idx] > 0 {
		return p.csiParams[idx]
	}
	return defaultVal
}

func (p *Parser) executeCSI(finalByte byte) {
	switch finalByte {
	case 'A': // CUU - Cursor Up
		p.buffer.MoveCursorUp(p.getParam(0, 1))

	case 'B': // CUD - Cursor Down
		p.buffer.MoveCursorDown(p.getParam(0, 1))

	case 'C': // CUF - Cursor Forward
		p.buffer.MoveCursorForward(p.getParam(0, 1))

	case 'D': // CUB - Cursor Backward
		p.buffer.MoveCursorBackward(p.getParam(0, 1))

	case 'E': // CNL - Cursor Next Line
		p.buffer.MoveCursorDown(p.getParam(0, 1))
		p.buffer.CarriageReturn()

	case 'F': // CPL - Cursor Previous Line
		p.buffer.MoveCursorUp(p.getParam(0, 1))
		p.buffer.CarriageReturn()

	case 'G': // CHA - Cursor Horizontal Absolute
		x := p.getParam(0, 1) - 1 // 1-indexed to 0-indexed
		_, y := p.buffer.GetCursor()
		p.buffer.SetCursor(x, y)

	case 'H', 'f': // CUP/HVP - Cursor Position
		row := p.getParam(0, 1) - 1
		col := p.getParam(1, 1) - 1
		p.buffer.SetCursor(col, row)

	case 'J': // ED - Erase in Display
		switch p.getParam(0, 0) {
		case 0:
			p.buffer.ClearToEndOfScreen()
		case 1:
			p.buffer.ClearToStartOfScreen()
		case 2, 3:
			p.buffer.ClearScreen()
			p.buffer.SetCursor(0, 0)
		}

	case 'K': // EL - Erase in Line
		switch p.getParam(0, 0) {
		case 0:
			p.buffer.ClearToEndOfLine()
		case 1:
			p.buffer.ClearToStartOfLine()
		case 2:
			p.buffer.ClearLine()
		}

	case 'L': // IL - Insert Lines
		p.buffer.InsertLines(p.getParam(0, 1))

	case 'M': // DL - Delete Lines
		p.buffer.DeleteLines(p.getParam(0, 1))

	case 'P': // DCH - Delete Characters
		p.buffer.DeleteChars(p.getParam(0, 1))

	case '@': // ICH - Insert Characters
		p.buffer.InsertChars(p.getParam(0, 1))

	case 'X': // ECH - Erase Characters
		p.buffer.EraseChars(p.getParam(0, 1))

	case 'S': // SU - Scroll Up
		p.buffer.ScrollUp(p.getParam(0, 1))

	case 'T': // SD - Scroll Down
		p.buffer.ScrollDown(p.getParam(0, 1))

	case 'd': // VPA - Vertical Position Absolute
		y := p.getParam(0, 1) - 1
		x, _ := p.buffer.GetCursor()
		p.buffer.SetCursor(x, y)

	case 'm': // SGR - Select Graphic Rendition
		p.executeSGR()

	case 'h': // SM - Set Mode
		if p.csiPrivate == '?' {
			p.executePrivateModeSet(true)
		}

	case 'l': // RM - Reset Mode
		if p.csiPrivate == '?' {
			p.executePrivateModeSet(false)
		}

	case 's': // SCP - Save Cursor Position
		p.buffer.SaveCursor()

	case 'u': // RCP - Restore Cursor Position
		p.buffer.RestoreCursor()

	case 'n': // DSR - Device Status Report
		// Would need to send response - ignore for now

	case 'r': // DECSTBM - Set Top and Bottom Margins
		// Scroll region - not yet implemented

	case 'c': // DA - Device Attributes
		// Would need to send response - ignore

	case 't': // Window manipulation
		p.executeWindowManipulation()

	case 'q': // DECSCUSR - Set Cursor Style (with space intermediate)
		if p.csiIntermediate == ' ' {
			p.executeDECSCUSR()
		}
	}
}

// executeWindowManipulation handles ESC [ Ps ; Ps ; Ps t - Window manipulation
// We specifically handle ESC [ 8 ; rows ; cols t to set logical screen size
// Custom extensions:
//   ESC [ 9 ; 40 ; 0 t - Disable 40-column mode
//   ESC [ 9 ; 40 ; 1 t - Enable 40-column mode
//   ESC [ 9 ; 25 t - Set line density to 25 (also: 30, 43, 50, 60)
func (p *Parser) executeWindowManipulation() {
	if len(p.csiParams) == 0 {
		return
	}

	cmd := p.csiParams[0]
	switch cmd {
	case 8: // ESC [ 8 ; rows ; cols t - Set terminal size
		// Get parameters (0 or omitted means "use physical/current")
		rows := 0
		cols := 0
		if len(p.csiParams) > 1 {
			rows = p.csiParams[1]
		}
		if len(p.csiParams) > 2 {
			cols = p.csiParams[2]
		}
		p.buffer.SetLogicalSize(rows, cols)

	case 9: // Custom PurfecTerm extensions
		if len(p.csiParams) < 2 {
			return
		}
		subCmd := p.csiParams[1]
		switch subCmd {
		case 40: // 40-column mode toggle
			// ESC [ 9 ; 40 ; 0 t = disable, ESC [ 9 ; 40 ; 1 t = enable
			enabled := false
			if len(p.csiParams) > 2 && p.csiParams[2] != 0 {
				enabled = true
			}
			p.buffer.Set40ColumnMode(enabled)
		case 25, 30, 43, 50, 60: // Line density
			// ESC [ 9 ; density t
			p.buffer.SetLineDensity(subCmd)
		}

	// Other window manipulation commands could be added here
	// case 1: De-iconify window
	// case 2: Iconify window
	// case 3: Move window
	// case 4: Resize window in pixels
	// etc.
	}
}

// executeDECSCUSR handles ESC [ Ps SP q - Set Cursor Style
func (p *Parser) executeDECSCUSR() {
	style := p.getParam(0, 1)
	// Ps = 0, 1: Blinking block
	// Ps = 2: Steady block
	// Ps = 3: Blinking underline
	// Ps = 4: Steady underline
	// Ps = 5: Blinking bar
	// Ps = 6: Steady bar
	var shape, blink int
	switch style {
	case 0, 1: // Blinking block (default)
		shape, blink = 0, 1
	case 2: // Steady block
		shape, blink = 0, 0
	case 3: // Blinking underline
		shape, blink = 1, 1
	case 4: // Steady underline
		shape, blink = 1, 0
	case 5: // Blinking bar
		shape, blink = 2, 1
	case 6: // Steady bar
		shape, blink = 2, 0
	default:
		shape, blink = 0, 1 // Default to blinking block
	}
	p.buffer.SetCursorStyle(shape, blink)
}

func (p *Parser) executeSGR() {
	if len(p.csiParams) == 0 {
		p.buffer.ResetAttributes()
		return
	}

	i := 0
	for i < len(p.csiParams) {
		param := p.csiParams[i]
		switch param {
		case 0: // Reset
			p.buffer.ResetAttributes()
		case 1: // Bold
			p.buffer.SetBold(true)
		case 2: // Dim (treat as not bold)
			p.buffer.SetBold(false)
		case 3: // Italic
			p.buffer.SetItalic(true)
		case 4: // Underline
			p.buffer.SetUnderline(true)
		case 5, 6: // Blink (slow=5, rapid=6) - rendered as bobbing wave animation
			p.buffer.SetBlink(true)
		case 7: // Reverse video
			p.buffer.SetReverse(true)
		case 9: // Strikethrough
			p.buffer.SetStrikethrough(true)
		case 21: // Bold off (double underline in some terminals)
			p.buffer.SetBold(false)
		case 22: // Normal intensity
			p.buffer.SetBold(false)
		case 23: // Italic off
			p.buffer.SetItalic(false)
		case 24: // Underline off
			p.buffer.SetUnderline(false)
		case 25: // Blink off
			p.buffer.SetBlink(false)
		case 27: // Reverse off
			p.buffer.SetReverse(false)
		case 29: // Strikethrough off
			p.buffer.SetStrikethrough(false)

		// Foreground colors (30-37)
		case 30, 31, 32, 33, 34, 35, 36, 37:
			p.buffer.SetForeground(StandardColor(param - 30))

		// Bright foreground colors (90-97)
		case 90, 91, 92, 93, 94, 95, 96, 97:
			p.buffer.SetForeground(StandardColor(param - 90 + 8))

		// Background colors (40-47)
		case 40, 41, 42, 43, 44, 45, 46, 47:
			p.buffer.SetBackground(StandardColor(param - 40))

		// Bright background colors (100-107)
		case 100, 101, 102, 103, 104, 105, 106, 107:
			p.buffer.SetBackground(StandardColor(param - 100 + 8))

		case 38: // Extended foreground color
			if i+2 < len(p.csiParams) && p.csiParams[i+1] == 5 {
				// 256-color mode: ESC[38;5;Nm
				p.buffer.SetForeground(PaletteColor(p.csiParams[i+2]))
				i += 2
			} else if i+4 < len(p.csiParams) && p.csiParams[i+1] == 2 {
				// True color mode: ESC[38;2;R;G;Bm
				p.buffer.SetForeground(TrueColor(
					uint8(p.csiParams[i+2]),
					uint8(p.csiParams[i+3]),
					uint8(p.csiParams[i+4]),
				))
				i += 4
			}

		case 39: // Default foreground
			p.buffer.SetForeground(DefaultForeground)

		case 48: // Extended background color
			if i+2 < len(p.csiParams) && p.csiParams[i+1] == 5 {
				// 256-color mode: ESC[48;5;Nm
				p.buffer.SetBackground(PaletteColor(p.csiParams[i+2]))
				i += 2
			} else if i+4 < len(p.csiParams) && p.csiParams[i+1] == 2 {
				// True color mode: ESC[48;2;R;G;Bm
				p.buffer.SetBackground(TrueColor(
					uint8(p.csiParams[i+2]),
					uint8(p.csiParams[i+3]),
					uint8(p.csiParams[i+4]),
				))
				i += 4
			}

		case 49: // Default background
			p.buffer.SetBackground(DefaultBackground)

		// Custom glyph system - flip attributes
		case 150: // Reset XFlip
			p.buffer.SetXFlip(false)
		case 151: // XFlip on
			p.buffer.SetXFlip(true)
		case 152: // Reset YFlip
			p.buffer.SetYFlip(false)
		case 153: // YFlip on
			p.buffer.SetYFlip(true)

		// Base Glyph Palette (BGP)
		case 158: // Set BGP: ESC[158;Nm
			if i+1 < len(p.csiParams) {
				p.buffer.SetBGP(p.csiParams[i+1])
				i++
			}
		case 159: // Reset BGP to default
			p.buffer.ResetBGP()
		}
		i++
	}
}

func (p *Parser) executePrivateModeSet(set bool) {
	for _, param := range p.csiParams {
		switch param {
		case 3: // DECCOLM - 132 Column Mode (horizontal scale 0.6060)
			p.buffer.Set132ColumnMode(set)
		case 5: // DECSCNM - Screen Mode (reverse video)
			// h = reverse video (light mode), l = normal video (dark mode)
			p.buffer.SetDarkTheme(!set)
		case 25: // DECTCEM - Cursor visibility
			p.buffer.SetCursorVisible(set)
		case 1049: // Alternate screen buffer
			// Not yet implemented
		case 2004: // Bracketed paste mode
			p.buffer.SetBracketedPasteMode(set)
		case 2027: // Flexible East Asian Width mode
			p.buffer.SetFlexWidthMode(set)
		case 2028: // Visual width-based line wrapping
			p.buffer.SetVisualWidthWrap(set)
		case 2029: // Ambiguous width: narrow (1.0)
			if set {
				p.buffer.SetAmbiguousWidthMode(AmbiguousWidthNarrow)
			} else {
				// Turning off narrow - check if wide is set, otherwise auto
				if p.buffer.GetAmbiguousWidthMode() == AmbiguousWidthNarrow {
					p.buffer.SetAmbiguousWidthMode(AmbiguousWidthAuto)
				}
			}
		case 2030: // Ambiguous width: wide (2.0)
			if set {
				p.buffer.SetAmbiguousWidthMode(AmbiguousWidthWide)
			} else {
				// Turning off wide - check if narrow is set, otherwise auto
				if p.buffer.GetAmbiguousWidthMode() == AmbiguousWidthWide {
					p.buffer.SetAmbiguousWidthMode(AmbiguousWidthAuto)
				}
			}
		case 1: // DECCKM - Application cursor keys
			// Not yet implemented
		case 7: // DECAWM - Auto-wrap mode
			// h = enable auto-wrap (cursor wraps to next line), l = disable (stay at last column)
			p.buffer.SetAutoWrapMode(set)
		case 12: // Cursor blink rate: h=fast, l=slow
			shape, _ := p.buffer.GetCursorStyle()
			if set {
				p.buffer.SetCursorStyle(shape, 2) // Fast blink
			} else {
				p.buffer.SetCursorStyle(shape, 1) // Slow blink
			}
		case 7700: // PurfecTerm: Disable scrollback buffer (for games)
			// h = disable scrollback accumulation, l = re-enable
			p.buffer.SetScrollbackDisabled(set)
		case 7701: // PurfecTerm: Disable cursor-following auto-scroll
			// h = disable auto-scroll, l = re-enable
			// When disabled, tracking still occurs but no automatic scrolling happens
			p.buffer.SetAutoScrollDisabled(set)
		case 7702: // PurfecTerm: Smart word wrap
			// h = enable smart word wrap (wrap at word boundaries), l = disable
			p.buffer.SetSmartWordWrap(set)
		}
	}
}

func (p *Parser) handleOSC(b byte) {
	if b >= '0' && b <= '9' {
		p.oscBuf.WriteByte(b)
		return
	}
	if b == ';' {
		// Parse and save OSC command number
		cmdStr := p.oscBuf.String()
		p.oscCmd, _ = strconv.Atoi(cmdStr)
		p.oscBuf.Reset()
		p.state = stateOSCString
		return
	}
	// Invalid OSC, return to ground
	p.state = stateGround
}

func (p *Parser) handleOSCString(b byte) {
	if b == 0x07 { // BEL terminates OSC
		p.executeOSC()
		p.state = stateGround
		return
	}
	if b == 0x1B { // ESC might start ST (ESC \)
		p.executeOSC()
		p.state = stateGround
		return
	}
	p.oscBuf.WriteByte(b)
}

// executeOSC processes a complete OSC command
func (p *Parser) executeOSC() {
	args := p.oscBuf.String()

	switch p.oscCmd {
	case 7000: // Palette management
		p.executeOSCPalette(args)
	case 7001: // Glyph management
		p.executeOSCGlyph(args)
	case 7002: // Sprite management
		p.executeOSCSprite(args)
	case 7003: // Screen crop and splits
		p.executeOSCScreenCrop(args)
	// Other OSC commands (title, etc.) could be added here
	}
}

// executeOSCPalette handles OSC 7000 palette commands
// Format: ESC ] 7000 ; cmd BEL
// Commands:
//   da           - delete all palettes
//   d;N          - delete palette N
//   i;N;LEN      - init palette N with LEN entries
//   s;N;IDX;COL  - set palette N index IDX to color COL
//   s;N;IDX;2;COL - set palette N index IDX to dim color COL
func (p *Parser) executeOSCPalette(args string) {
	parts := strings.Split(args, ";")
	if len(parts) == 0 {
		return
	}

	cmd := parts[0]
	switch cmd {
	case "da": // Delete all palettes
		p.buffer.DeleteAllPalettes()

	case "d": // Delete single palette
		if len(parts) >= 2 {
			n, _ := strconv.Atoi(parts[1])
			p.buffer.DeletePalette(n)
		}

	case "i": // Init palette
		if len(parts) >= 3 {
			n, _ := strconv.Atoi(parts[1])
			length, _ := strconv.Atoi(parts[2])
			p.buffer.InitPalette(n, length)
		}

	case "s": // Set palette entry
		// Formats:
		//   s;N;IDX;COL           - SGR-style color (30-37, 90-97)
		//   s;N;IDX;2;COL         - SGR-style color, dim
		//   s;N;IDX;5;N256        - 256-color palette index
		//   s;N;IDX;5;2;N256      - 256-color, dim
		//   s;N;IDX;r;R;G;B       - True color RGB
		//   s;N;IDX;r;2;R;G;B     - True color RGB, dim
		if len(parts) >= 4 {
			n, _ := strconv.Atoi(parts[1])
			idx, _ := strconv.Atoi(parts[2])
			mode := parts[3]

			switch mode {
			case "5": // 256-color mode
				dim := false
				colorIdx := 4
				if len(parts) > 4 && parts[4] == "2" {
					dim = true
					colorIdx = 5
				}
				if colorIdx < len(parts) {
					colorNum, _ := strconv.Atoi(parts[colorIdx])
					color := Get256Color(colorNum)
					p.buffer.SetPaletteEntryColor(n, idx, color, dim)
				}

			case "r": // True color RGB mode
				dim := false
				rgbStart := 4
				if len(parts) > 4 && parts[4] == "2" {
					dim = true
					rgbStart = 5
				}
				if rgbStart+2 < len(parts) {
					r, _ := strconv.Atoi(parts[rgbStart])
					g, _ := strconv.Atoi(parts[rgbStart+1])
					b, _ := strconv.Atoi(parts[rgbStart+2])
					color := TrueColor(uint8(r), uint8(g), uint8(b))
					p.buffer.SetPaletteEntryColor(n, idx, color, dim)
				}

			case "2": // Dim modifier for SGR-style (legacy format)
				if len(parts) >= 5 {
					colorCode, _ := strconv.Atoi(parts[4])
					p.buffer.SetPaletteEntry(n, idx, colorCode, true)
				}

			default: // SGR-style color code
				colorCode, _ := strconv.Atoi(mode)
				p.buffer.SetPaletteEntry(n, idx, colorCode, false)
			}
		}
	}
}

// executeOSCGlyph handles OSC 7001 glyph commands
// Format: ESC ] 7001 ; cmd BEL
// Commands:
//   da                    - delete all glyphs
//   d;RUNE                - delete glyph for rune
//   s;RUNE;W;P1;P2;...    - set glyph for rune (W=width, P=pixels)
func (p *Parser) executeOSCGlyph(args string) {
	parts := strings.Split(args, ";")
	if len(parts) == 0 {
		return
	}

	cmd := parts[0]
	switch cmd {
	case "da": // Delete all glyphs
		p.buffer.DeleteAllGlyphs()

	case "d": // Delete single glyph
		if len(parts) >= 2 {
			runeCode, _ := strconv.Atoi(parts[1])
			p.buffer.DeleteGlyph(rune(runeCode))
		}

	case "s": // Set glyph
		// Format: s;RUNE;WIDTH;P1;P2;P3;...
		if len(parts) >= 4 {
			runeCode, _ := strconv.Atoi(parts[1])
			width, _ := strconv.Atoi(parts[2])
			pixels := make([]int, 0, len(parts)-3)
			for i := 3; i < len(parts); i++ {
				px, _ := strconv.Atoi(parts[i])
				pixels = append(pixels, px)
			}
			if width > 0 && len(pixels) > 0 {
				p.buffer.SetGlyph(rune(runeCode), width, pixels)
			}
		}
	}
}

// executeOSCSprite handles OSC 7002 sprite commands
// Format: ESC ] 7002 ; cmd BEL
// Commands:
//
//	da                                         - delete all sprites
//	d;ID                                       - delete sprite by ID
//	s;ID;X;Y;Z;FGP;FLIP;XS;YS;CROP;R1;R2;...   - set sprite (rune codes, 10=newline)
//	t;ID;X;Y;Z;FGP;FLIP;XS;YS;CROP;text        - set sprite (text string)
//	m;ID;X;Y                                   - move sprite (position only)
//	mr;ID;X;Y;R1;R2;...                        - move and update runes (rune codes)
//	mrt;ID;X;Y;text                            - move and update runes (text)
//	u;UX;UY                                    - set coordinate units
//	cda                                        - delete all crop rectangles
//	cd;ID                                      - delete crop rectangle
//	cs;ID;MINX;MINY;MAXX;MAXY                  - set crop rectangle
func (p *Parser) executeOSCSprite(args string) {
	parts := strings.Split(args, ";")
	if len(parts) == 0 {
		return
	}

	cmd := parts[0]
	switch cmd {
	case "da": // Delete all sprites
		p.buffer.DeleteAllSprites()

	case "d": // Delete single sprite
		if len(parts) >= 2 {
			id, _ := strconv.Atoi(parts[1])
			p.buffer.DeleteSprite(id)
		}

	case "s": // Set sprite with rune codes
		// Format: s;ID;X;Y;Z;FGP;FLIP;XS;YS;CROP;R1;R2;...
		if len(parts) >= 11 {
			id, _ := strconv.Atoi(parts[1])
			x, _ := strconv.ParseFloat(parts[2], 64)
			y, _ := strconv.ParseFloat(parts[3], 64)
			z, _ := strconv.Atoi(parts[4])
			fgp, _ := strconv.Atoi(parts[5])
			flipCode, _ := strconv.Atoi(parts[6])
			xScale, _ := strconv.ParseFloat(parts[7], 64)
			yScale, _ := strconv.ParseFloat(parts[8], 64)
			cropRect, _ := strconv.Atoi(parts[9])

			// Collect runes from remaining parts
			runes := make([]rune, 0, len(parts)-10)
			for i := 10; i < len(parts); i++ {
				code, _ := strconv.Atoi(parts[i])
				runes = append(runes, rune(code))
			}

			// Default scales if zero
			if xScale == 0 {
				xScale = 1.0
			}
			if yScale == 0 {
				yScale = 1.0
			}

			p.buffer.SetSprite(id, x, y, z, fgp, flipCode, xScale, yScale, cropRect, runes)
		}

	case "t": // Set sprite with text string
		// Format: t;ID;X;Y;Z;FGP;FLIP;XS;YS;CROP;text
		if len(parts) >= 11 {
			id, _ := strconv.Atoi(parts[1])
			x, _ := strconv.ParseFloat(parts[2], 64)
			y, _ := strconv.ParseFloat(parts[3], 64)
			z, _ := strconv.Atoi(parts[4])
			fgp, _ := strconv.Atoi(parts[5])
			flipCode, _ := strconv.Atoi(parts[6])
			xScale, _ := strconv.ParseFloat(parts[7], 64)
			yScale, _ := strconv.ParseFloat(parts[8], 64)
			cropRect, _ := strconv.Atoi(parts[9])

			// Text is everything after the 9th semicolon (may contain semicolons)
			text := strings.Join(parts[10:], ";")
			runes := []rune(text)

			// Default scales if zero
			if xScale == 0 {
				xScale = 1.0
			}
			if yScale == 0 {
				yScale = 1.0
			}

			p.buffer.SetSprite(id, x, y, z, fgp, flipCode, xScale, yScale, cropRect, runes)
		}

	case "m": // Move sprite (position only)
		// Format: m;ID;X;Y
		if len(parts) >= 4 {
			id, _ := strconv.Atoi(parts[1])
			x, _ := strconv.ParseFloat(parts[2], 64)
			y, _ := strconv.ParseFloat(parts[3], 64)
			p.buffer.MoveSprite(id, x, y)
		}

	case "mr": // Move and update runes (rune codes)
		// Format: mr;ID;X;Y;R1;R2;...
		if len(parts) >= 5 {
			id, _ := strconv.Atoi(parts[1])
			x, _ := strconv.ParseFloat(parts[2], 64)
			y, _ := strconv.ParseFloat(parts[3], 64)

			// Collect runes from remaining parts
			runes := make([]rune, 0, len(parts)-4)
			for i := 4; i < len(parts); i++ {
				code, _ := strconv.Atoi(parts[i])
				runes = append(runes, rune(code))
			}

			p.buffer.MoveSpriteAndRunes(id, x, y, runes)
		}

	case "mrt": // Move and update runes (text)
		// Format: mrt;ID;X;Y;text
		if len(parts) >= 5 {
			id, _ := strconv.Atoi(parts[1])
			x, _ := strconv.ParseFloat(parts[2], 64)
			y, _ := strconv.ParseFloat(parts[3], 64)

			// Text is everything after the 3rd semicolon (may contain semicolons)
			text := strings.Join(parts[4:], ";")
			runes := []rune(text)

			p.buffer.MoveSpriteAndRunes(id, x, y, runes)
		}

	case "u": // Set coordinate units (subdivisions per cell)
		// Format: u;UX;UY
		if len(parts) >= 3 {
			ux, _ := strconv.Atoi(parts[1])
			uy, _ := strconv.Atoi(parts[2])
			p.buffer.SetSpriteUnits(ux, uy)
		}

	case "cda": // Delete all crop rectangles
		p.buffer.DeleteAllCropRects()

	case "cd": // Delete crop rectangle
		if len(parts) >= 2 {
			id, _ := strconv.Atoi(parts[1])
			p.buffer.DeleteCropRect(id)
		}

	case "cs": // Set crop rectangle
		// Format: cs;ID;MINX;MINY;MAXX;MAXY
		if len(parts) >= 6 {
			id, _ := strconv.Atoi(parts[1])
			minX, _ := strconv.ParseFloat(parts[2], 64)
			minY, _ := strconv.ParseFloat(parts[3], 64)
			maxX, _ := strconv.ParseFloat(parts[4], 64)
			maxY, _ := strconv.ParseFloat(parts[5], 64)
			p.buffer.SetCropRect(id, minX, minY, maxX, maxY)
		}
	}
}

// executeOSCScreenCrop handles OSC 7003 screen crop and split commands
// Format: ESC ] 7003 ; cmd BEL
// Commands:
//
//	c                                                       - clear both crops
//	c;WIDTH                                                 - set width crop only (in sprite units, -1 = no crop)
//	c;;HEIGHT                                               - set height crop only
//	c;WIDTH;HEIGHT                                          - set both crops
//	sda                                                     - delete all screen splits
//	sd;ID                                                   - delete screen split by ID
//	ss;ID;SCREENY;BUFROW;BUFCOL;TOPFINE;LEFTFINE;CWS;LD     - set screen split
//	    ID: split identifier
//	    SCREENY: Y coordinate in sprite units where split begins on screen
//	    BUFROW, BUFCOL: 1-indexed logical screen coordinates (0 = inherit/default)
//	    TOPFINE, LEFTFINE: fine scroll (0 to subdivisions-1, higher = more clipped)
//	    CWS: character width scale (0 = inherit)
//	    LD: line density override (0 = inherit)
func (p *Parser) executeOSCScreenCrop(args string) {
	parts := strings.Split(args, ";")
	if len(parts) == 0 {
		return
	}

	cmd := parts[0]
	switch cmd {
	case "c": // Screen crop: c (clear), c;W, c;;H, or c;W;H
		widthCrop := -1
		heightCrop := -1
		if len(parts) >= 2 && parts[1] != "" {
			widthCrop, _ = strconv.Atoi(parts[1])
		}
		if len(parts) >= 3 && parts[2] != "" {
			heightCrop, _ = strconv.Atoi(parts[2])
		}
		p.buffer.SetScreenCrop(widthCrop, heightCrop)

	case "sda": // Delete all screen splits
		p.buffer.DeleteAllScreenSplits()

	case "sd": // Delete screen split
		// Format: sd;ID
		if len(parts) >= 2 {
			id, _ := strconv.Atoi(parts[1])
			p.buffer.DeleteScreenSplit(id)
		}

	case "ss": // Set screen split
		// Format: ss;ID;SCREENY;BUFROW;BUFCOL;TOPFINE;LEFTFINE;CWS;LD
		if len(parts) >= 9 {
			id, _ := strconv.Atoi(parts[1])
			screenY, _ := strconv.Atoi(parts[2])
			bufRow, _ := strconv.Atoi(parts[3])
			bufCol, _ := strconv.Atoi(parts[4])
			topFine, _ := strconv.Atoi(parts[5])
			leftFine, _ := strconv.Atoi(parts[6])
			charWidthScale, _ := strconv.ParseFloat(parts[7], 64)
			lineDensity, _ := strconv.Atoi(parts[8])

			// Convert 1-indexed buffer coordinates to 0-indexed
			// (0 in escape means inherit/default, maps to 0 internally)
			if bufRow > 0 {
				bufRow--
			}
			if bufCol > 0 {
				bufCol--
			}

			p.buffer.SetScreenSplit(id, screenY, bufRow, bufCol, topFine, leftFine, charWidthScale, lineDensity)
		}
	}
}
