package purfectermcli

import (
	"os"
	"time"
)

// InputHandler manages keyboard input from the host terminal
type InputHandler struct {
	term          *Terminal
	escapeBuffer  []byte
	escapeTimeout time.Duration
	lastEscape    time.Time
}

// Special key constants for internal handling
const (
	keyNone = iota
	keyUp
	keyDown
	keyLeft
	keyRight
	keyHome
	keyEnd
	keyPageUp
	keyPageDown
	keyInsert
	keyDelete
	keyF1
	keyF2
	keyF3
	keyF4
	keyF5
	keyF6
	keyF7
	keyF8
	keyF9
	keyF10
	keyF11
	keyF12
)

// Modifier flags
const (
	modShift = 1 << iota
	modAlt
	modCtrl
)

// NewInputHandler creates a new input handler
func NewInputHandler(term *Terminal) *InputHandler {
	return &InputHandler{
		term:          term,
		escapeBuffer:  make([]byte, 0, 32),
		escapeTimeout: 50 * time.Millisecond,
	}
}

// InputLoop reads and processes input from stdin
func (h *InputHandler) InputLoop() {
	buf := make([]byte, 256)

	for {
		select {
		case <-h.term.stopRender:
			return
		default:
		}

		// Read with a timeout to handle escape sequences
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return
		}
		if n == 0 {
			continue
		}

		h.processInput(buf[:n])
	}
}

// processInput handles raw input bytes
func (h *InputHandler) processInput(data []byte) {
	for i := 0; i < len(data); {
		b := data[i]

		// Check for escape sequence start
		if b == 0x1b { // ESC
			// Collect escape sequence
			h.escapeBuffer = append(h.escapeBuffer[:0], b)
			h.lastEscape = time.Now()
			i++

			// Try to read more of the sequence
			for i < len(data) && len(h.escapeBuffer) < 32 {
				h.escapeBuffer = append(h.escapeBuffer, data[i])
				i++

				// Check if we have a complete sequence
				key, mods, consumed, passthrough := h.parseEscapeSequence(h.escapeBuffer)
				if consumed > 0 {
					if passthrough != nil {
						// This sequence should be passed through to the PTY
						h.sendToPTY(passthrough)
					} else if key != keyNone {
						// Handle special key
						h.handleSpecialKey(key, mods)
					}
					// Reset buffer
					h.escapeBuffer = h.escapeBuffer[:0]
					break
				}
			}

			// If we still have an incomplete sequence after processing all data,
			// wait briefly for more input or treat as standalone ESC
			if len(h.escapeBuffer) > 0 {
				if len(h.escapeBuffer) == 1 {
					// Just ESC - pass it through
					h.sendToPTY([]byte{0x1b})
				} else {
					// Incomplete sequence - pass through as-is
					h.sendToPTY(h.escapeBuffer)
				}
				h.escapeBuffer = h.escapeBuffer[:0]
			}
		} else {
			// Regular character
			h.handleRegularInput(b)
			i++
		}
	}
}

// parseEscapeSequence attempts to parse an escape sequence
// Returns: key code, modifiers, bytes consumed, passthrough bytes (if should be sent to PTY as-is)
func (h *InputHandler) parseEscapeSequence(seq []byte) (key int, mods int, consumed int, passthrough []byte) {
	if len(seq) < 2 {
		return keyNone, 0, 0, nil
	}

	// CSI sequences: ESC [
	if seq[1] == '[' {
		return h.parseCSISequence(seq)
	}

	// SS3 sequences: ESC O (for some function keys)
	if seq[1] == 'O' {
		return h.parseSS3Sequence(seq)
	}

	// Alt+key: ESC followed by regular character
	if len(seq) == 2 && seq[1] >= 0x20 && seq[1] < 0x7f {
		// Pass through Alt+key to PTY
		return keyNone, modAlt, 2, seq
	}

	return keyNone, 0, 0, nil
}

// parseCSISequence parses CSI (ESC [) sequences
func (h *InputHandler) parseCSISequence(seq []byte) (key int, mods int, consumed int, passthrough []byte) {
	if len(seq) < 3 {
		return keyNone, 0, 0, nil
	}

	// Check for terminal character
	lastByte := seq[len(seq)-1]

	// Standard CSI sequences end with a letter
	if lastByte >= 'A' && lastByte <= 'Z' || lastByte == '~' {
		switch lastByte {
		case 'A': // Up
			key = keyUp
		case 'B': // Down
			key = keyDown
		case 'C': // Right
			key = keyRight
		case 'D': // Left
			key = keyLeft
		case 'H': // Home
			key = keyHome
		case 'F': // End
			key = keyEnd
		case '~':
			// Check parameter for specific keys
			if len(seq) >= 4 {
				switch seq[2] {
				case '1': // Home (some terminals)
					key = keyHome
				case '2': // Insert
					key = keyInsert
				case '3': // Delete
					key = keyDelete
				case '4': // End (some terminals)
					key = keyEnd
				case '5': // PageUp
					key = keyPageUp
				case '6': // PageDown
					key = keyPageDown
				}

				// Function keys: 11-24~ for F1-F12
				if len(seq) >= 5 && seq[2] == '1' {
					switch seq[3] {
					case '1':
						key = keyF1
					case '2':
						key = keyF2
					case '3':
						key = keyF3
					case '4':
						key = keyF4
					case '5':
						key = keyF5
					case '7':
						key = keyF6
					case '8':
						key = keyF7
					case '9':
						key = keyF8
					}
				}
				if len(seq) >= 5 && seq[2] == '2' {
					switch seq[3] {
					case '0':
						key = keyF9
					case '1':
						key = keyF10
					case '3':
						key = keyF11
					case '4':
						key = keyF12
					}
				}
			}
		}

		// Check for modifiers in extended format: ESC [ 1 ; <mod> <key>
		if len(seq) >= 6 && seq[2] == '1' && seq[3] == ';' {
			modByte := seq[4]
			if modByte >= '2' && modByte <= '8' {
				modNum := int(modByte - '1')
				if modNum&1 != 0 {
					mods |= modShift
				}
				if modNum&2 != 0 {
					mods |= modAlt
				}
				if modNum&4 != 0 {
					mods |= modCtrl
				}
			}
		}

		consumed = len(seq)

		// Determine if this should be handled locally or passed through
		if h.shouldHandleLocally(key, mods) {
			return key, mods, consumed, nil
		}
		return keyNone, 0, consumed, seq
	}

	// Check for incomplete sequence (no terminator yet)
	if lastByte >= '0' && lastByte <= '9' || lastByte == ';' {
		return keyNone, 0, 0, nil // Need more data
	}

	// Unknown sequence - pass through
	return keyNone, 0, len(seq), seq
}

// parseSS3Sequence parses SS3 (ESC O) sequences
func (h *InputHandler) parseSS3Sequence(seq []byte) (key int, mods int, consumed int, passthrough []byte) {
	if len(seq) < 3 {
		return keyNone, 0, 0, nil
	}

	switch seq[2] {
	case 'A':
		key = keyUp
	case 'B':
		key = keyDown
	case 'C':
		key = keyRight
	case 'D':
		key = keyLeft
	case 'H':
		key = keyHome
	case 'F':
		key = keyEnd
	case 'P':
		key = keyF1
	case 'Q':
		key = keyF2
	case 'R':
		key = keyF3
	case 'S':
		key = keyF4
	default:
		// Unknown - pass through
		return keyNone, 0, 3, seq[:3]
	}

	consumed = 3
	if h.shouldHandleLocally(key, mods) {
		return key, mods, consumed, nil
	}
	return keyNone, 0, consumed, seq[:3]
}

// shouldHandleLocally determines if a key should be handled by the CLI adapter
// rather than passed to the child process
func (h *InputHandler) shouldHandleLocally(key int, mods int) bool {
	// Shift+PageUp/PageDown for scrollback navigation
	if mods&modShift != 0 {
		switch key {
		case keyPageUp, keyPageDown, keyUp, keyDown, keyHome, keyEnd:
			return true
		}
	}

	// Ctrl+Shift+C/V for copy/paste could be handled here
	// For now, just scrollback navigation

	return false
}

// handleSpecialKey handles keys that are processed by the CLI adapter
func (h *InputHandler) handleSpecialKey(key int, mods int) {
	if mods&modShift != 0 {
		switch key {
		case keyPageUp:
			// Scroll up one page
			_, rows := h.term.buffer.GetSize()
			h.term.ScrollUp(rows - 1)
			h.term.renderer.RequestRender()
		case keyPageDown:
			// Scroll down one page
			_, rows := h.term.buffer.GetSize()
			h.term.ScrollDown(rows - 1)
			h.term.renderer.RequestRender()
		case keyUp:
			// Scroll up one line
			h.term.ScrollUp(1)
			h.term.renderer.RequestRender()
		case keyDown:
			// Scroll down one line
			h.term.ScrollDown(1)
			h.term.renderer.RequestRender()
		case keyHome:
			// Scroll to top
			h.term.ScrollToTop()
			h.term.renderer.RequestRender()
		case keyEnd:
			// Scroll to bottom
			h.term.ScrollToBottom()
			h.term.renderer.RequestRender()
		}
	}
}

// handleRegularInput handles regular (non-escape) input
func (h *InputHandler) handleRegularInput(b byte) {
	// Check for input callback
	h.term.mu.Lock()
	callback := h.term.inputCallback
	h.term.mu.Unlock()

	if callback != nil {
		if callback([]byte{b}) {
			return // Consumed by callback
		}
	}

	// Scroll to bottom on any input
	if h.term.GetScrollOffset() > 0 {
		h.term.ScrollToBottom()
		h.term.renderer.RequestRender()
	}

	// Send to PTY
	h.sendToPTY([]byte{b})
}

// sendToPTY sends data to the child process
func (h *InputHandler) sendToPTY(data []byte) {
	h.term.mu.Lock()
	pty := h.term.pty
	h.term.mu.Unlock()

	if pty != nil {
		pty.Write(data)
	}
}

// HandleMouseInput processes mouse events (if mouse tracking is enabled)
// This is a placeholder for future mouse support
func (h *InputHandler) HandleMouseInput(x, y int, button int, pressed bool) {
	// Mouse support would be implemented here
	// For now, this is a no-op
}
