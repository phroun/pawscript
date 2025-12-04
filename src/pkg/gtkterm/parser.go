package gtkterm

import (
	"strconv"
	"strings"
)

// Parser states
type parserState int

const (
	stateGround parserState = iota
	stateEscape             // After ESC
	stateCSI                // After ESC [
	stateCSIParam           // Reading CSI parameters
	stateOSC                // After ESC ]
	stateOSCString          // Reading OSC string
	stateCharset            // After ESC ( or ESC )
)

// Parser parses ANSI escape sequences and updates a Buffer
type Parser struct {
	buffer *Buffer
	state  parserState

	// CSI sequence accumulator
	csiParams        []int
	csiPrivate       byte // For private sequences like ?25h
	csiIntermediate  byte // For sequences with intermediate bytes like DECSCUSR (SP q)
	csiBuf           strings.Builder

	// OSC accumulator
	oscBuf strings.Builder

	// UTF-8 multi-byte handling
	utf8Buf   []byte
	utf8Need  int
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

	case 't': // Window manipulation - ignore

	case 'q': // DECSCUSR - Set Cursor Style (with space intermediate)
		if p.csiIntermediate == ' ' {
			p.executeDECSCUSR()
		}
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
		case 7: // Reverse video
			p.buffer.SetReverse(true)
		case 21: // Bold off (double underline in some terminals)
			p.buffer.SetBold(false)
		case 22: // Normal intensity
			p.buffer.SetBold(false)
		case 23: // Italic off
			p.buffer.SetItalic(false)
		case 24: // Underline off
			p.buffer.SetUnderline(false)
		case 27: // Reverse off
			p.buffer.SetReverse(false)

		// Foreground colors (30-37)
		case 30, 31, 32, 33, 34, 35, 36, 37:
			p.buffer.SetForeground(ANSIColors[param-30])

		// Bright foreground colors (90-97)
		case 90, 91, 92, 93, 94, 95, 96, 97:
			p.buffer.SetForeground(ANSIColors[param-90+8])

		// Background colors (40-47)
		case 40, 41, 42, 43, 44, 45, 46, 47:
			p.buffer.SetBackground(ANSIColors[param-40])

		// Bright background colors (100-107)
		case 100, 101, 102, 103, 104, 105, 106, 107:
			p.buffer.SetBackground(ANSIColors[param-100+8])

		case 38: // Extended foreground color
			if i+2 < len(p.csiParams) && p.csiParams[i+1] == 5 {
				// 256-color mode: ESC[38;5;Nm
				p.buffer.SetForeground(Get256Color(p.csiParams[i+2]))
				i += 2
			} else if i+4 < len(p.csiParams) && p.csiParams[i+1] == 2 {
				// True color mode: ESC[38;2;R;G;Bm
				p.buffer.SetForeground(Color{
					R: uint8(p.csiParams[i+2]),
					G: uint8(p.csiParams[i+3]),
					B: uint8(p.csiParams[i+4]),
				})
				i += 4
			}

		case 39: // Default foreground
			p.buffer.SetForeground(DefaultForeground)

		case 48: // Extended background color
			if i+2 < len(p.csiParams) && p.csiParams[i+1] == 5 {
				// 256-color mode: ESC[48;5;Nm
				p.buffer.SetBackground(Get256Color(p.csiParams[i+2]))
				i += 2
			} else if i+4 < len(p.csiParams) && p.csiParams[i+1] == 2 {
				// True color mode: ESC[48;2;R;G;Bm
				p.buffer.SetBackground(Color{
					R: uint8(p.csiParams[i+2]),
					G: uint8(p.csiParams[i+3]),
					B: uint8(p.csiParams[i+4]),
				})
				i += 4
			}

		case 49: // Default background
			p.buffer.SetBackground(DefaultBackground)
		}
		i++
	}
}

func (p *Parser) executePrivateModeSet(set bool) {
	for _, param := range p.csiParams {
		switch param {
		case 25: // DECTCEM - Cursor visibility
			p.buffer.SetCursorVisible(set)
		case 1049: // Alternate screen buffer
			// Not yet implemented
		case 2004: // Bracketed paste mode
			// Not yet implemented
		case 1: // DECCKM - Application cursor keys
			// Not yet implemented
		case 7: // DECAWM - Auto-wrap mode
			// Not yet implemented
		case 12: // Cursor blink
			// Not yet implemented
		}
	}
}

func (p *Parser) handleOSC(b byte) {
	if b >= '0' && b <= '9' {
		p.oscBuf.WriteByte(b)
		return
	}
	if b == ';' {
		p.state = stateOSCString
		p.oscBuf.Reset()
		return
	}
	// Invalid OSC, return to ground
	p.state = stateGround
}

func (p *Parser) handleOSCString(b byte) {
	if b == 0x07 { // BEL terminates OSC
		// OSC command complete - could handle window title here
		p.state = stateGround
		return
	}
	if b == 0x1B { // ESC might start ST (ESC \)
		// For simplicity, treat ESC as terminator
		p.state = stateGround
		return
	}
	p.oscBuf.WriteByte(b)
}
