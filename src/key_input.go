package pawscript

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

// KeyInputManager handles raw keyboard input, parsing escape sequences
// and providing both key events and line assembly
type KeyInputManager struct {
	mu sync.Mutex

	// Input source
	inputReader io.Reader     // Raw input source (any io.Reader)
	rawBytes    chan []byte   // Channel for raw byte chunks
	stopChan    chan struct{} // Signal to stop reading

	// Output channels
	keysChan  *StoredChannel // Parsed key events ("a", "M-a", "F1", etc.)
	linesChan *StoredChannel // Assembled lines for {read}
	keysGo    chan string    // Go channel for blocking key reads
	linesGo   chan []byte    // Go channel for blocking line reads

	// Terminal handling (only used if input is os.Stdin and is a terminal)
	terminalFd        int         // File descriptor if we're managing terminal mode
	originalTermState *term.State // Original state to restore
	managesTerminal   bool        // True if we put terminal in raw mode

	// State
	running        bool
	inLineReadMode bool // True when read is waiting for line input

	// Line assembly state - stores raw bytes for proper I/O semantics
	currentLine []byte
	// Track UTF-8 character boundaries for backspace (number of bytes per char)
	charByteLengths []int

	// Escape sequence buffer
	escBuffer []byte
	inEscape  bool

	// UTF-8 multi-byte character buffer
	utf8Buffer    []byte
	utf8Remaining int // bytes remaining to complete current UTF-8 char

	// Bracketed paste state
	inPaste     bool
	pasteBuffer []byte

	// Echo output (where to echo typed characters during readkey)
	echoWriter io.Writer
	// Echo output specifically for line read mode (used by read command)
	// If set, overrides echoWriter during line assembly
	lineEchoWriter io.Writer

	// Debug callback (optional)
	debugFn func(string)
}

// NewKeyInputManager creates a new key input manager
// inputReader can be any io.Reader (os.Stdin, io.PipeReader, etc.)
// echoWriter is where to echo typed characters (typically os.Stdout or a pipe)
// If inputReader is os.Stdin and is a terminal, raw mode will be enabled
func NewKeyInputManager(inputReader io.Reader, echoWriter io.Writer, debugFn func(string)) *KeyInputManager {
	keysGo := make(chan string, 64)
	linesGo := make(chan []byte, 16)

	m := &KeyInputManager{
		inputReader: inputReader,
		rawBytes:    make(chan []byte, 64),
		stopChan:    make(chan struct{}),
		keysChan:    NewStoredChannel(64),
		linesChan:   NewStoredChannel(16),
		keysGo:      keysGo,
		linesGo:     linesGo,
		echoWriter:  echoWriter,
		debugFn:     debugFn,
		terminalFd:  -1,
	}

	// Set up blocking NativeRecv on keysChan
	m.keysChan.NativeRecv = func() (interface{}, error) {
		select {
		case key, ok := <-keysGo:
			if !ok {
				return nil, fmt.Errorf("channel closed")
			}
			return key, nil
		case <-m.stopChan:
			return nil, fmt.Errorf("channel closed")
		}
	}

	// Set up NativeLen on keysChan to report Go channel length
	m.keysChan.NativeLen = func() int {
		return len(keysGo)
	}

	// Set up blocking NativeRecv on linesChan
	m.linesChan.NativeRecv = func() (interface{}, error) {
		// Enter line read mode - keys will now go to line buffer instead of just keysChan
		// Also enable echo so user can see what they're typing
		m.mu.Lock()
		m.inLineReadMode = true
		m.currentLine = nil
		m.charByteLengths = nil
		savedEchoWriter := m.echoWriter
		// Use lineEchoWriter if set (for channel-based input with separate echo channel)
		if m.lineEchoWriter != nil {
			m.echoWriter = m.lineEchoWriter
		} else if m.echoWriter == nil && m.managesTerminal {
			// Only default to os.Stdout if we're in terminal mode
			// In channel mode (pawgui), echo must be explicitly configured via echo: argument
			m.echoWriter = os.Stdout
		}
		m.mu.Unlock()

		// Wait for line (without holding lock)
		var line []byte
		var ok bool
		var err error

		select {
		case line, ok = <-linesGo:
			if !ok {
				err = fmt.Errorf("channel closed")
			}
		case <-m.stopChan:
			err = fmt.Errorf("channel closed")
		}

		// Exit line read mode and restore echo state
		m.mu.Lock()
		m.inLineReadMode = false
		m.echoWriter = savedEchoWriter
		m.mu.Unlock()

		if err != nil {
			return nil, err
		}
		return line, nil
	}

	// Check if input is os.Stdin and is a terminal
	if f, ok := inputReader.(*os.File); ok && f == os.Stdin {
		fd := int(f.Fd())
		if term.IsTerminal(fd) {
			m.terminalFd = fd
			m.managesTerminal = true
		}
	}

	return m
}

// Escape sequence bindings - maps escape sequences to key names
var escBindings = map[string]string{
	// Arrow keys
	"\x1b[A": "Up",
	"\x1b[B": "Down",
	"\x1b[C": "Right",
	"\x1b[D": "Left",

	// Arrow keys with modifiers
	"\x1b[1;2A": "S-Up",
	"\x1b[1;2B": "S-Down",
	"\x1b[1;2C": "S-Right",
	"\x1b[1;2D": "S-Left",
	"\x1b[1;3A": "M-Up",
	"\x1b[1;3B": "M-Down",
	"\x1b[1;3C": "M-Right",
	"\x1b[1;3D": "M-Left",
	"\x1b[1;5A": "C-Up",
	"\x1b[1;5B": "C-Down",
	"\x1b[1;5C": "C-Right",
	"\x1b[1;5D": "C-Left",

	// Function keys
	"\x1bOP": "F1",
	"\x1bOQ": "F2",
	"\x1bOR": "F3",
	"\x1bOS": "F4",
	"\x1b[15~": "F5",
	"\x1b[17~": "F6",
	"\x1b[18~": "F7",
	"\x1b[19~": "F8",
	"\x1b[20~": "F9",
	"\x1b[21~": "F10",
	"\x1b[23~": "F11",
	"\x1b[24~": "F12",

	// Navigation keys
	"\x1b[H": "Home",
	"\x1b[F": "End",
	"\x1b[1~": "Home",
	"\x1b[4~": "End",
	"\x1b[2~": "Insert",
	"\x1b[3~": "Delete",
	"\x1b[5~": "PageUp",
	"\x1b[6~": "PageDown",

	// Alternate arrow key sequences (some terminals)
	"\x1bOA": "Up",
	"\x1bOB": "Down",
	"\x1bOC": "Right",
	"\x1bOD": "Left",
}

// Control key names
var controlKeys = map[byte]string{
	0:   "^@", // Ctrl-Space or Ctrl-@
	1:   "^A",
	2:   "^B",
	3:   "^C",
	4:   "^D",
	5:   "^E",
	6:   "^F",
	7:   "^G",
	8:   "Backspace", // Ctrl-H
	9:   "Tab",       // Ctrl-I
	10:  "Enter",     // Ctrl-J (LF)
	11:  "^K",
	12:  "^L",
	13:  "Enter",     // Ctrl-M (CR)
	14:  "^N",
	15:  "^O",
	16:  "^P",
	17:  "^Q",
	18:  "^R",
	19:  "^S",
	20:  "^T",
	21:  "^U",
	22:  "^V",
	23:  "^W",
	24:  "^X",
	25:  "^Y",
	26:  "^Z",
	27:  "Escape", // Escape itself (handled specially)
	28:  "^\\",
	29:  "^]",
	30:  "^^",
	31:  "^_",
	127: "Backspace", // DEL
}

// Start begins reading from input and processing keys
func (m *KeyInputManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("key input manager already running")
	}

	// Put terminal in raw mode only if we're managing it
	if m.managesTerminal {
		state, err := term.MakeRaw(m.terminalFd)
		if err != nil {
			return fmt.Errorf("failed to enable raw mode: %w", err)
		}
		m.originalTermState = state
		m.debug("Terminal set to raw mode")
	}

	m.running = true

	// Start the read goroutine
	go m.readLoop()

	// Start the processing goroutine
	go m.processLoop()

	m.debug("Key input manager started")
	return nil
}

// Stop stops reading and restores terminal state
func (m *KeyInputManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return nil
	}

	// Signal stop
	close(m.stopChan)
	m.running = false

	// Restore terminal state if we changed it
	if m.managesTerminal && m.originalTermState != nil {
		if err := term.Restore(m.terminalFd, m.originalTermState); err != nil {
			return fmt.Errorf("failed to restore terminal: %w", err)
		}
		m.originalTermState = nil
		m.debug("Terminal restored to original mode")
	}

	m.debug("Key input manager stopped")
	return nil
}

// GetKeysChannel returns the channel for reading parsed key events
func (m *KeyInputManager) GetKeysChannel() *StoredChannel {
	return m.keysChan
}

// GetLinesChannel returns the channel for reading assembled lines
func (m *KeyInputManager) GetLinesChannel() *StoredChannel {
	return m.linesChan
}

// SetLineEchoWriter sets the writer to use for echo during line read mode
// This is used when input comes from a channel (not os.Stdin directly)
// but we still want echo during {read} operations
func (m *KeyInputManager) SetLineEchoWriter(w io.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lineEchoWriter = w
}

// readLoop continuously reads raw bytes from input
func (m *KeyInputManager) readLoop() {
	buf := make([]byte, 256)
	for {
		select {
		case <-m.stopChan:
			return
		default:
			n, err := m.inputReader.Read(buf)
			if err != nil {
				m.debug(fmt.Sprintf("Read error: %v", err))
				return
			}
			if n > 0 {
				// Make a copy to send
				data := make([]byte, n)
				copy(data, buf[:n])
				select {
				case m.rawBytes <- data:
				case <-m.stopChan:
					return
				}
			}
		}
	}
}

// processLoop processes raw bytes into key events
func (m *KeyInputManager) processLoop() {
	escTimeout := time.NewTimer(0)
	if !escTimeout.Stop() {
		<-escTimeout.C
	}

	for {
		select {
		case <-m.stopChan:
			return

		case data := <-m.rawBytes:
			for _, b := range data {
				m.processByte(b, escTimeout)
			}

		case <-escTimeout.C:
			// Escape sequence timeout - try Alt sequence parsing before giving up
			if m.inEscape && len(m.escBuffer) > 0 {
				seq := string(m.escBuffer)
				// Try Alt+key parsing (ESC followed by character)
				// This handles cases like ESC O which could be Alt+Shift+O
				if key, ok := m.parseAltSequence(seq); ok {
					m.emitKey(key)
					m.escBuffer = nil
					m.inEscape = false
				} else {
					m.emitEscapeBuffer()
				}
			}
		}
	}
}

// Bracketed paste sequences
const (
	bracketedPasteStart = "\x1b[200~"
	bracketedPasteEnd   = "\x1b[201~"
)

// processByte handles a single byte of input
func (m *KeyInputManager) processByte(b byte, escTimeout *time.Timer) {
	// Handle bracketed paste mode
	if m.inPaste {
		m.pasteBuffer = append(m.pasteBuffer, b)

		// Check if paste buffer ends with the end sequence
		if len(m.pasteBuffer) >= len(bracketedPasteEnd) {
			tail := string(m.pasteBuffer[len(m.pasteBuffer)-len(bracketedPasteEnd):])
			if tail == bracketedPasteEnd {
				// End of paste - extract content (without the end sequence)
				content := m.pasteBuffer[:len(m.pasteBuffer)-len(bracketedPasteEnd)]
				m.inPaste = false
				m.pasteBuffer = nil
				m.debug(fmt.Sprintf("Paste end, %d bytes", len(content)))
				m.emitPaste(content)
				return
			}
		}
		return
	}

	if m.inEscape {
		m.escBuffer = append(m.escBuffer, b)

		// Check if we have a complete escape sequence
		seq := string(m.escBuffer)

		// Check for bracketed paste start
		if seq == bracketedPasteStart {
			m.debug("Bracketed paste start detected")
			m.inEscape = false
			m.escBuffer = nil
			m.inPaste = true
			m.pasteBuffer = nil
			escTimeout.Stop()
			return
		}

		if key, ok := escBindings[seq]; ok {
			m.emitKey(key)
			m.escBuffer = nil
			m.inEscape = false
			escTimeout.Stop()
			return
		}

		// Check if this could be a prefix of a valid sequence
		if m.couldBeEscapePrefix(seq) {
			// Reset timeout - wait for more bytes
			escTimeout.Reset(50 * time.Millisecond)
			return
		}

		// Try dynamic parsing for CSI sequences with modifiers
		if key, ok := m.parseModifiedCSI(seq); ok {
			m.emitKey(key)
			m.escBuffer = nil
			m.inEscape = false
			escTimeout.Stop()
			return
		}

		// Try Alt+key parsing (ESC followed by character)
		if key, ok := m.parseAltSequence(seq); ok {
			m.emitKey(key)
			m.escBuffer = nil
			m.inEscape = false
			escTimeout.Stop()
			return
		}

		// Not a valid sequence - emit as individual keys
		m.emitEscapeBuffer()
		return
	}

	// Check for escape start
	if b == 0x1b {
		m.inEscape = true
		m.escBuffer = []byte{b}
		escTimeout.Reset(50 * time.Millisecond)
		return
	}

	// Handle control characters
	if b < 32 || b == 127 {
		if key, ok := controlKeys[b]; ok {
			m.emitKey(key)
		} else {
			m.emitKey(fmt.Sprintf("^%c", b+64))
		}
		return
	}

	// Regular printable character or start of UTF-8 sequence
	if b < 128 {
		m.emitKey(string(b))
		return
	}

	// UTF-8 multi-byte character handling
	// Check if we're continuing an existing UTF-8 sequence
	if m.utf8Remaining > 0 {
		// Continuation byte should be 10xxxxxx (0x80-0xBF)
		if b >= 0x80 && b <= 0xBF {
			m.utf8Buffer = append(m.utf8Buffer, b)
			m.utf8Remaining--
			if m.utf8Remaining == 0 {
				// Complete UTF-8 sequence - emit the character
				m.emitKey(string(m.utf8Buffer))
				m.utf8Buffer = nil
			}
		} else {
			// Invalid continuation - emit buffer as-is and reset
			for _, bb := range m.utf8Buffer {
				m.emitKey(string(rune(bb)))
			}
			m.utf8Buffer = nil
			m.utf8Remaining = 0
			// Process this byte as a new sequence
			m.processByte(b, escTimeout)
		}
		return
	}

	// Start of new UTF-8 sequence - determine length from lead byte
	if b >= 0xC0 && b <= 0xDF {
		// 2-byte sequence: 110xxxxx
		m.utf8Buffer = []byte{b}
		m.utf8Remaining = 1
	} else if b >= 0xE0 && b <= 0xEF {
		// 3-byte sequence: 1110xxxx
		m.utf8Buffer = []byte{b}
		m.utf8Remaining = 2
	} else if b >= 0xF0 && b <= 0xF7 {
		// 4-byte sequence: 11110xxx
		m.utf8Buffer = []byte{b}
		m.utf8Remaining = 3
	} else {
		// Invalid UTF-8 lead byte or bare continuation byte - emit as-is
		m.emitKey(string(rune(b)))
	}
}

// couldBeEscapePrefix checks if seq could be a prefix of a valid escape sequence
func (m *KeyInputManager) couldBeEscapePrefix(seq string) bool {
	for key := range escBindings {
		if len(seq) < len(key) && key[:len(seq)] == seq {
			return true
		}
	}
	// Also allow CSI sequences in progress: ESC [ ...
	if len(seq) >= 2 && seq[0] == 0x1b && seq[1] == '[' {
		// CSI sequence - wait for terminator
		last := seq[len(seq)-1]
		if last >= 0x40 && last <= 0x7e {
			return false // Terminated
		}
		return true // Still in progress
	}
	return false
}

// emitEscapeBuffer emits the escape buffer as individual keys
func (m *KeyInputManager) emitEscapeBuffer() {
	// First byte is ESC
	m.emitKey("Escape")
	// Remaining bytes as regular characters
	for _, b := range m.escBuffer[1:] {
		if b < 32 || b == 127 {
			if key, ok := controlKeys[b]; ok {
				m.emitKey(key)
			}
		} else {
			m.emitKey(string(b))
		}
	}
	m.escBuffer = nil
	m.inEscape = false
}

// emitKey sends a key event to either the keys channel OR line assembly (not both)
func (m *KeyInputManager) emitKey(key string) {
	m.debug(fmt.Sprintf("Key: %q", key))

	// Check if we're in line read mode
	m.mu.Lock()
	inLineMode := m.inLineReadMode
	m.mu.Unlock()

	if inLineMode {
		// In line read mode: keys go to line assembly only
		m.handleLineAssembly(key)
	} else {
		// Normal mode: keys go to keysGo channel (blocking NativeRecv reads from this)
		select {
		case m.keysGo <- key:
			// Sent successfully
		default:
			// Buffer full - drop oldest key to make room
			select {
			case <-m.keysGo:
			default:
			}
			// Try again
			select {
			case m.keysGo <- key:
			default:
				// Still can't send, just drop this key
			}
		}
	}
}

// emitPaste handles bracketed paste content
func (m *KeyInputManager) emitPaste(content []byte) {
	m.mu.Lock()
	inLineMode := m.inLineReadMode
	m.mu.Unlock()

	if inLineMode {
		// In line read mode: add pasted content directly to line buffer
		m.handlePasteLineAssembly(content)
	} else {
		// Normal mode: emit each character as individual key events
		// This preserves behavior for programs reading key-by-key
		for len(content) > 0 {
			r, size := utf8.DecodeRune(content)
			if r == utf8.RuneError && size == 1 {
				// Invalid UTF-8, skip byte
				content = content[1:]
				continue
			}
			// Handle special characters
			if r == '\r' || r == '\n' {
				m.emitKey("Enter")
			} else if r == '\t' {
				m.emitKey("Tab")
			} else if r == 0x7f {
				m.emitKey("Backspace")
			} else if r < 32 {
				// Control character
				if key, ok := controlKeys[byte(r)]; ok {
					m.emitKey(key)
				}
			} else {
				m.emitKey(string(r))
			}
			content = content[size:]
		}
	}
}

// handlePasteLineAssembly adds pasted content to the line buffer
func (m *KeyInputManager) handlePasteLineAssembly(content []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.inLineReadMode {
		return
	}

	// Process pasted content byte by byte, handling special characters
	for len(content) > 0 {
		r, size := utf8.DecodeRune(content)
		if r == utf8.RuneError && size == 1 {
			content = content[1:]
			continue
		}

		if r == '\r' || r == '\n' {
			// Newline in paste - submit the current line and continue
			// For multi-line paste, only take the first line
			lineBytes := make([]byte, len(m.currentLine))
			copy(lineBytes, m.currentLine)
			m.currentLine = nil
			m.charByteLengths = nil
			echoWriter := m.echoWriter
			m.mu.Unlock()

			// Send line
			select {
			case m.linesGo <- lineBytes:
			default:
				select {
				case <-m.linesGo:
				default:
				}
				m.linesGo <- lineBytes
			}

			// Echo newline
			if echoWriter != nil {
				echoWriter.Write([]byte("\r\n"))
			}

			m.mu.Lock()
			// Skip remaining content after newline (single-line read)
			return
		} else if r >= 32 || r == '\t' {
			// Printable character or tab - add to line
			charBytes := content[:size]
			m.currentLine = append(m.currentLine, charBytes...)
			m.charByteLengths = append(m.charByteLengths, size)
			// Echo
			m.echoLocked(string(r))
		}
		// Skip control characters (except handled above)

		content = content[size:]
	}
}

// handleLineAssembly processes a key for line assembly
// Line buffer stores raw bytes; charByteLengths tracks UTF-8 boundaries for backspace
// Only processes keys when in line read mode (read is waiting for input)
func (m *KeyInputManager) handleLineAssembly(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Only assemble lines when read is waiting - otherwise keys just go to keysChan
	if !m.inLineReadMode {
		return
	}

	switch key {
	case "Enter":
		// Emit the completed line as raw bytes
		// Make a copy to avoid sharing the slice
		lineBytes := make([]byte, len(m.currentLine))
		copy(lineBytes, m.currentLine)
		m.currentLine = nil
		m.charByteLengths = nil
		echoWriter := m.echoWriter
		m.mu.Unlock()

		// Send to Go channel (NativeRecv will read from this) - without holding lock
		select {
		case m.linesGo <- lineBytes:
		default:
			// Buffer full, drop oldest and add new
			select {
			case <-m.linesGo:
			default:
			}
			m.linesGo <- lineBytes
		}

		// Echo newline
		if echoWriter != nil {
			echoWriter.Write([]byte("\r\n"))
		}

		m.mu.Lock() // Re-acquire for deferred unlock
		return

	case "Backspace":
		if len(m.charByteLengths) > 0 {
			// Remove the last character (which may be multiple bytes)
			lastCharLen := m.charByteLengths[len(m.charByteLengths)-1]
			m.currentLine = m.currentLine[:len(m.currentLine)-lastCharLen]
			m.charByteLengths = m.charByteLengths[:len(m.charByteLengths)-1]
			// Echo backspace (one visual character)
			m.echoLocked("\b \b")
		}

	case "^U":
		// Clear line - one backspace per visual character
		for range m.charByteLengths {
			m.echoLocked("\b \b")
		}
		m.currentLine = nil
		m.charByteLengths = nil

	case "^C":
		// Interrupt - emit empty byte slice and clear
		m.echoLocked("^C\r\n")
		m.currentLine = nil
		m.charByteLengths = nil
		m.mu.Unlock()

		// Send to Go channel (NativeRecv will read from this) - without holding lock
		select {
		case m.linesGo <- []byte{}:
		default:
		}

		m.mu.Lock() // Re-acquire for deferred unlock
		return

	default:
		// Check if it's a printable character (single rune)
		if len(key) > 0 {
			r, _ := utf8.DecodeRuneInString(key)
			if r != utf8.RuneError && len(key) == utf8.RuneLen(r) && r >= 32 {
				// Append raw bytes of the character
				m.currentLine = append(m.currentLine, []byte(key)...)
				m.charByteLengths = append(m.charByteLengths, len(key))
				// Echo character
				m.echoLocked(key)
			}
		}
	}
}

// echo writes to the echo output if configured
func (m *KeyInputManager) echo(s string) {
	if m.echoWriter != nil {
		m.echoWriter.Write([]byte(s))
	}
}

// echoLocked writes to echo output - call only while holding m.mu
func (m *KeyInputManager) echoLocked(s string) {
	if m.echoWriter != nil {
		m.echoWriter.Write([]byte(s))
	}
}

func (m *KeyInputManager) debug(msg string) {
	if m.debugFn != nil {
		m.debugFn(msg)
	}
}

// Alt key handling - detect M- prefix for alt combinations
func (m *KeyInputManager) parseAltSequence(seq string) (string, bool) {
	// ESC followed by a character = Alt+char (Meta prefix)
	if len(seq) == 2 && seq[0] == 0x1b {
		char := seq[1]
		// Lowercase letters: M-a through M-z
		if char >= 'a' && char <= 'z' {
			return fmt.Sprintf("M-%c", char), true
		}
		// Uppercase letters: M-S-a through M-S-z (shift implied)
		if char >= 'A' && char <= 'Z' {
			return fmt.Sprintf("M-S-%c", char-'A'+'a'), true
		}
		// Numbers: M-0 through M-9
		if char >= '0' && char <= '9' {
			return fmt.Sprintf("M-%c", char), true
		}
		// Symbols and punctuation - use descriptive names for common ones
		switch char {
		case '[':
			return "M-[", true
		case ']':
			return "M-]", true
		case '{':
			return "M-{", true
		case '}':
			return "M-}", true
		case '(':
			return "M-(", true
		case ')':
			return "M-)", true
		case '<':
			return "M-<", true
		case '>':
			return "M->", true
		case '/':
			return "M-/", true
		case '\\':
			return "M-\\", true
		case '\'':
			return "M-'", true
		case '"':
			return "M-\"", true
		case '`':
			return "M-`", true
		case ',':
			return "M-,", true
		case '.':
			return "M-.", true
		case ';':
			return "M-;", true
		case ':':
			return "M-:", true
		case '=':
			return "M-=", true
		case '+':
			return "M-+", true
		case '-':
			return "M--", true
		case '_':
			return "M-_", true
		case '!':
			return "M-!", true
		case '@':
			return "M-@", true
		case '#':
			return "M-#", true
		case '$':
			return "M-$", true
		case '%':
			return "M-%", true
		case '^':
			return "M-^", true
		case '&':
			return "M-&", true
		case '*':
			return "M-*", true
		case '?':
			return "M-?", true
		case '|':
			return "M-|", true
		case '~':
			return "M-~", true
		case ' ':
			return "M-Space", true
		default:
			// Special control characters with named keys
			// ESC + control_char comes from Alt/Meta + the key that produces that control char
			switch char {
			case 0x09: // Tab (Ctrl+I)
				return "M-Tab", true
			case 0x0D: // Enter/CR (Ctrl+M)
				return "M-Enter", true
			case 0x7F: // DEL/Backspace
				return "M-Backspace", true
			case 0x08: // Backspace (Ctrl+H)
				return "M-Backspace", true
			case 0x1B: // ESC (another ESC)
				return "M-Escape", true
			}
			// Other control characters: M-^A through M-^Z (ESC + 0x01-0x1A)
			// These come from Alt+Ctrl+letter combinations
			if char >= 0x01 && char <= 0x1A {
				letter := 'A' + char - 1 // ^A=1 -> 'A', ^K=11 -> 'K'
				return fmt.Sprintf("M-^%c", letter), true
			}
			// Any other printable ASCII character
			if char >= 0x20 && char < 0x7f {
				return fmt.Sprintf("M-%c", char), true
			}
		}
	}
	return "", false
}

// parseModifiedCSI dynamically parses CSI sequences with modifiers
// Handles sequences not in the static escBindings map
func (m *KeyInputManager) parseModifiedCSI(seq string) (string, bool) {
	// Must start with ESC [
	if len(seq) < 3 || seq[0] != 0x1b || seq[1] != '[' {
		return "", false
	}

	body := seq[2:]
	if len(body) == 0 {
		return "", false
	}

	// Check for Shift+Tab: ESC [ Z
	if body == "Z" {
		return "S-Tab", true
	}

	// Final byte determines the key type
	finalByte := body[len(body)-1]
	if finalByte < 0x40 || finalByte > 0x7E {
		return "", false
	}

	params := body[:len(body)-1]

	// Split parameters by semicolon
	parts := splitCSIParams(params)

	switch finalByte {
	case 'A', 'B', 'C', 'D':
		// Cursor keys: ESC [ 1 ; <mod> <A-D>
		return parseModifiedCursorKey(finalByte, parts)
	case 'H', 'F':
		// Home/End: ESC [ 1 ; <mod> <H|F>
		return parseModifiedHomeEnd(finalByte, parts)
	case 'P', 'Q', 'R', 'S':
		// F1-F4: ESC [ 1 ; <mod> <P-S>
		return parseModifiedF1toF4(finalByte, parts)
	case '~':
		// Tilde sequences: ESC [ <num> ; <mod> ~
		return parseModifiedTildeKey(parts)
	case 'u':
		// Kitty protocol: ESC [ keycode ; mod u
		return parseKittyProtocol(parts)
	}

	return "", false
}

// parseKittyProtocol handles CSI keycode ; mod u format (kitty keyboard protocol)
// This format encodes special keys with full modifier information
func parseKittyProtocol(parts []string) (string, bool) {
	if len(parts) == 0 {
		return "", false
	}

	// First param is the keycode
	keycode := parseModifierParam(parts[0])

	// Map keycodes to key names
	keyNames := map[int]string{
		9:   "Tab",
		13:  "Enter",
		27:  "Escape",
		127: "Backspace",
	}

	baseName, ok := keyNames[keycode]
	if !ok {
		// Unknown keycode
		return "", false
	}

	// One param = unmodified (rare for kitty protocol)
	if len(parts) == 1 {
		return baseName, true
	}

	// Two params = modified
	if len(parts) == 2 {
		mod := parseModifierParam(parts[1])
		prefix := modifierPrefix(mod)
		return prefix + baseName, true
	}

	return "", false
}

// splitCSIParams splits parameter string by semicolons
func splitCSIParams(params string) []string {
	if params == "" {
		return nil
	}
	var parts []string
	start := 0
	for i := 0; i <= len(params); i++ {
		if i == len(params) || params[i] == ';' {
			parts = append(parts, params[start:i])
			start = i + 1
		}
	}
	return parts
}

// modifierPrefix converts xterm modifier code to key prefix
// mod = 1 + shift + 2*alt + 4*ctrl + 8*meta
// Returns prefix like "S-", "C-", "M-", "S-C-", etc.
func modifierPrefix(mod int) string {
	if mod < 2 {
		return ""
	}
	mod-- // Remove the base 1

	prefix := ""
	if mod&1 != 0 { // Shift
		prefix += "S-"
	}
	if mod&2 != 0 { // Alt
		prefix += "M-"
	}
	if mod&4 != 0 { // Ctrl
		prefix += "C-"
	}
	if mod&8 != 0 { // Meta (Super)
		prefix += "s-" // lowercase s for Super
	}
	return prefix
}

// parseModifierParam parses a modifier parameter string to int
func parseModifierParam(s string) int {
	if s == "" {
		return 1
	}
	mod := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			mod = mod*10 + int(c-'0')
		} else {
			return 1
		}
	}
	if mod < 1 {
		return 1
	}
	return mod
}

// parseModifiedCursorKey handles ESC [ 1 ; <mod> <A-D>
func parseModifiedCursorKey(finalByte byte, parts []string) (string, bool) {
	keyNames := map[byte]string{
		'A': "Up",
		'B': "Down",
		'C': "Right",
		'D': "Left",
	}

	baseName, ok := keyNames[finalByte]
	if !ok {
		return "", false
	}

	// No params = unmodified
	if len(parts) == 0 {
		return baseName, true
	}

	// Need exactly 2 params: "1" and modifier
	if len(parts) != 2 {
		return "", false
	}

	mod := parseModifierParam(parts[1])
	prefix := modifierPrefix(mod)
	return prefix + baseName, true
}

// parseModifiedHomeEnd handles ESC [ 1 ; <mod> <H|F>
func parseModifiedHomeEnd(finalByte byte, parts []string) (string, bool) {
	keyNames := map[byte]string{
		'H': "Home",
		'F': "End",
	}

	baseName, ok := keyNames[finalByte]
	if !ok {
		return "", false
	}

	// No params = unmodified
	if len(parts) == 0 {
		return baseName, true
	}

	// Need exactly 2 params for modified version
	if len(parts) != 2 {
		return "", false
	}

	mod := parseModifierParam(parts[1])
	prefix := modifierPrefix(mod)
	return prefix + baseName, true
}

// parseModifiedF1toF4 handles ESC [ 1 ; <mod> <P-S>
func parseModifiedF1toF4(finalByte byte, parts []string) (string, bool) {
	keyNames := map[byte]string{
		'P': "F1",
		'Q': "F2",
		'R': "F3",
		'S': "F4",
	}

	baseName, ok := keyNames[finalByte]
	if !ok {
		return "", false
	}

	// No params = might be unmodified (though F1-F4 usually use SS3)
	if len(parts) == 0 {
		return baseName, true
	}

	// Need exactly 2 params for modified version
	if len(parts) != 2 {
		return "", false
	}

	mod := parseModifierParam(parts[1])
	prefix := modifierPrefix(mod)
	return prefix + baseName, true
}

// parseModifiedTildeKey handles ESC [ <num> ; <mod> ~
func parseModifiedTildeKey(parts []string) (string, bool) {
	// Tilde key number to name mapping
	tildeKeys := map[int]string{
		1:  "Home",
		2:  "Insert",
		3:  "Delete",
		4:  "End",
		5:  "PageUp",
		6:  "PageDown",
		15: "F5",
		17: "F6",
		18: "F7",
		19: "F8",
		20: "F9",
		21: "F10",
		23: "F11",
		24: "F12",
	}

	if len(parts) == 0 {
		return "", false
	}

	// First param is the key number
	keyNum := parseModifierParam(parts[0])
	baseName, ok := tildeKeys[keyNum]
	if !ok {
		return "", false
	}

	// One param = unmodified
	if len(parts) == 1 {
		return baseName, true
	}

	// Two params = modified
	if len(parts) == 2 {
		mod := parseModifierParam(parts[1])
		prefix := modifierPrefix(mod)
		return prefix + baseName, true
	}

	return "", false
}
