package pawscript

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/phroun/pawscript/src/pkg/keyboard"
	"golang.org/x/term"
)

// KeyInputManager handles raw keyboard input for PawScript.
// It wraps the keyboard.Handler package and provides StoredChannel integration.
type KeyInputManager struct {
	mu sync.Mutex

	// Underlying keyboard handler
	handler *keyboard.Handler

	// Stop channel
	stopChan chan struct{}

	// Output channels (PawScript StoredChannel wrappers)
	keysChan  *StoredChannel // Parsed key events ("a", "M-a", "F1", etc.)
	linesChan *StoredChannel // Assembled lines for {read}

	// Terminal state tracking
	managesTerminal bool // True if handler manages terminal raw mode
	terminalBacked  bool // True if input comes from a terminal-backed channel

	// Line echo handling
	lineEchoWriter io.Writer // Echo writer specifically for line read mode

	// Debug callback
	debugFn func(string)
}

// NewKeyInputManager creates a new key input manager.
// inputReader can be any io.Reader (os.Stdin, io.PipeReader, etc.)
// echoWriter is where to echo typed characters (typically os.Stdout or a pipe)
// If inputReader is os.Stdin and is a terminal, raw mode will be enabled.
func NewKeyInputManager(inputReader io.Reader, echoWriter io.Writer, debugFn func(string)) *KeyInputManager {
	// Check if we should manage terminal
	managesTerminal := false
	if f, ok := inputReader.(*os.File); ok && f == os.Stdin {
		fd := int(f.Fd())
		if term.IsTerminal(fd) {
			managesTerminal = true
		}
	}

	// Create the underlying keyboard handler
	handler := keyboard.New(keyboard.Options{
		InputReader:    inputReader,
		EchoWriter:     echoWriter,
		KeyBufferSize:  64,
		LineBufferSize: 16,
		DebugFn:        debugFn,
	})

	m := &KeyInputManager{
		handler:         handler,
		stopChan:        make(chan struct{}),
		keysChan:        NewStoredChannel(64),
		linesChan:       NewStoredChannel(16),
		managesTerminal: managesTerminal,
		debugFn:         debugFn,
	}

	// Set up blocking NativeRecv on keysChan
	m.keysChan.NativeRecv = func() (interface{}, error) {
		select {
		case key, ok := <-handler.Keys:
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
		return len(handler.Keys)
	}

	// Set up blocking NativeRecv on linesChan
	m.linesChan.NativeRecv = func() (interface{}, error) {
		m.mu.Lock()

		// Configure echo for line read mode
		savedEchoWriter := m.handler.IsLineMode() // Just to read current state
		_ = savedEchoWriter                       // Not actually needed since we'll restore after

		// Use lineEchoWriter if set (for channel-based input with separate echo channel)
		if m.lineEchoWriter != nil {
			handler.SetEchoWriter(m.lineEchoWriter)
		} else if handler.ManagesTerminal() {
			// Only default to os.Stdout if we're in terminal mode
			handler.SetEchoWriter(os.Stdout)
		}

		// Enter line read mode - keys will now go to line buffer
		handler.SetLineMode(true)
		m.mu.Unlock()

		// Wait for line (without holding lock)
		var line []byte
		var ok bool
		var err error

		select {
		case line, ok = <-handler.Lines:
			if !ok {
				err = fmt.Errorf("channel closed")
			}
		case <-m.stopChan:
			err = fmt.Errorf("channel closed")
		}

		// Exit line read mode
		m.mu.Lock()
		handler.SetLineMode(false)
		m.mu.Unlock()

		if err != nil {
			return nil, err
		}
		return line, nil
	}

	return m
}

// Start begins reading from input and processing keys.
func (m *KeyInputManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.handler.Start()
}

// Stop stops reading and restores terminal state.
func (m *KeyInputManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Signal stop to unblock NativeRecv
	select {
	case <-m.stopChan:
		// Already closed
	default:
		close(m.stopChan)
	}

	return m.handler.Stop()
}

// GetKeysChannel returns the channel for reading parsed key events.
func (m *KeyInputManager) GetKeysChannel() *StoredChannel {
	return m.keysChan
}

// GetLinesChannel returns the channel for reading assembled lines.
func (m *KeyInputManager) GetLinesChannel() *StoredChannel {
	return m.linesChan
}

// SetLineEchoWriter sets the writer to use for echo during line read mode.
// This is used when input comes from a channel (not os.Stdin directly)
// but we still want echo during {read} operations.
func (m *KeyInputManager) SetLineEchoWriter(w io.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lineEchoWriter = w
}

// IsManagingStdin returns true if this manager is managing the terminal stdin.
// This is used by REPLs to determine if they should delegate input handling.
// Returns true if either:
// - Manager directly manages terminal (input is os.Stdin)
// - Manager reads from a terminal-backed channel (e.g., #in wrapping stdin)
func (m *KeyInputManager) IsManagingStdin() bool {
	return m.managesTerminal || m.terminalBacked
}

// SetTerminalBacked marks this manager as reading from a terminal-backed channel.
// This is used when input comes through a channel that wraps stdin, so the manager
// knows to signal REPLs to delegate input handling even though it's not directly
// reading from os.Stdin.
func (m *KeyInputManager) SetTerminalBacked(backed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.terminalBacked = backed
}

// IsRunning returns true if the manager is currently running.
func (m *KeyInputManager) IsRunning() bool {
	return m.handler.IsRunning()
}

func (m *KeyInputManager) debug(msg string) {
	if m.debugFn != nil {
		m.debugFn(msg)
	}
}
