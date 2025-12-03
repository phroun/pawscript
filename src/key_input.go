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

	// Echo output (where to echo typed characters)
	echoWriter io.Writer

	// Debug callback (optional)
	debugFn func(string)
}

// NewKeyInputManager creates a new key input manager
// inputReader can be any io.Reader (os.Stdin, io.PipeReader, etc.)
// echoWriter is where to echo typed characters (typically os.Stdout or a pipe)
// If inputReader is os.Stdin and is a terminal, raw mode will be enabled
func NewKeyInputManager(inputReader io.Reader, echoWriter io.Writer, debugFn func(string)) *KeyInputManager {
	linesGo := make(chan []byte, 16)

	m := &KeyInputManager{
		inputReader: inputReader,
		rawBytes:    make(chan []byte, 64),
		stopChan:    make(chan struct{}),
		keysChan:    NewStoredChannel(64),
		linesChan:   NewStoredChannel(16),
		linesGo:     linesGo,
		echoWriter:  echoWriter,
		debugFn:     debugFn,
		terminalFd:  -1,
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
		m.echoWriter = os.Stdout // Enable echo during line read
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
			// Escape sequence timeout - emit buffered escape
			if m.inEscape && len(m.escBuffer) > 0 {
				m.emitEscapeBuffer()
			}
		}
	}
}

// processByte handles a single byte of input
func (m *KeyInputManager) processByte(b byte, escTimeout *time.Timer) {
	if m.inEscape {
		m.escBuffer = append(m.escBuffer, b)

		// Check if we have a complete escape sequence
		seq := string(m.escBuffer)
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
	} else {
		// UTF-8 multi-byte character - need to collect more bytes
		// For now, emit as single character (will enhance later)
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
		// Normal mode: keys go to keysChan only
		ChannelSend(m.keysChan, key)
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
	// ESC followed by a character = Alt+char
	if len(seq) == 2 && seq[0] == 0x1b {
		char := seq[1]
		if char >= 'a' && char <= 'z' {
			return fmt.Sprintf("M-%c", char), true
		}
		if char >= 'A' && char <= 'Z' {
			return fmt.Sprintf("M-S-%c", char-'A'+'a'), true
		}
		if char >= '0' && char <= '9' {
			return fmt.Sprintf("M-%c", char), true
		}
	}
	return "", false
}
