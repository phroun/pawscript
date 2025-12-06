package pawscript

import (
	"bufio"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

// normalizeNewlines converts bare \n to \r\n for consistent terminal behavior.
// This ensures output works correctly whether the terminal is in raw mode or not.
// Existing \r\n sequences are preserved (not doubled).
func normalizeNewlines(s string) string {
	// First normalize any existing \r\n to \n, then convert all \n to \r\n
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", "\r\n")
	return s
}

// Global tracking for terminal state cleanup
var (
	globalStdinStateMu sync.Mutex
	globalStdinState   *stdinChannelState
)

// CleanupTerminal restores the terminal to its original state.
// This should be called when the program exits to ensure the terminal
// is left in a usable state (proper newline handling, echo enabled, etc.)
func CleanupTerminal() {
	globalStdinStateMu.Lock()
	state := globalStdinState
	globalStdinStateMu.Unlock()

	if state == nil {
		return
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	if state.originalState != nil && state.inRawMode {
		_ = term.Restore(state.fd, state.originalState)
		state.originalState = nil
		state.inRawMode = false
		state.rawModeRefCount = 0
	}
}

// IOChannelConfig allows host applications to provide custom IO channel handlers
// Any nil channel will use the default OS-backed implementation
type IOChannelConfig struct {
	// Standard channels - if nil, defaults will be created using DefaultStdin/Stdout/Stderr
	Stdin  *StoredChannel // Read-only channel for input
	Stdout *StoredChannel // Write-only channel for standard output
	Stderr *StoredChannel // Write-only channel for error output
	Stdio  *StoredChannel // Bidirectional channel (read from stdin, write to stdout)

	// Default streams to use when creating default channels (if nil, uses os.Stdin/Stdout/Stderr)
	DefaultStdin  io.Reader
	DefaultStdout io.Writer
	DefaultStderr io.Writer

	// Additional custom channels - will be registered with their map keys as names
	// Example: {"#mylog": logChannel} would create io::#mylog
	CustomChannels map[string]*StoredChannel
}

// stdinChannelState manages the state of a terminal-backed stdin channel
// Supports mode switching between line mode and raw mode via NativeSend
type stdinChannelState struct {
	mu sync.Mutex

	// Input source
	file       *os.File      // The underlying file (os.Stdin)
	lineReader *bufio.Reader // Line-mode reader

	// Terminal state
	fd              int         // File descriptor
	isTerminal      bool        // True if fd is a terminal
	originalState   *term.State // Original terminal state (for restore)
	inRawMode       bool        // Current mode
	rawModeRefCount int         // Reference count for nested raw mode requests
}

// setRawMode puts the terminal in raw mode (reference counted)
func (s *stdinChannelState) setRawMode() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isTerminal {
		return fmt.Errorf("not a terminal")
	}

	s.rawModeRefCount++
	if s.inRawMode {
		return nil // Already in raw mode
	}

	state, err := term.MakeRaw(s.fd)
	if err != nil {
		s.rawModeRefCount--
		return err
	}
	s.originalState = state
	s.inRawMode = true
	return nil
}

// setLineMode restores line mode (reference counted)
func (s *stdinChannelState) setLineMode() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isTerminal || !s.inRawMode {
		return nil
	}

	s.rawModeRefCount--
	if s.rawModeRefCount > 0 {
		return nil // Still have active raw mode users
	}

	if s.originalState != nil {
		if err := term.Restore(s.fd, s.originalState); err != nil {
			return err
		}
		s.originalState = nil
	}
	s.inRawMode = false
	return nil
}

// readByte reads a single byte (for raw mode)
func (s *stdinChannelState) readByte() (byte, error) {
	buf := make([]byte, 1)
	_, err := s.file.Read(buf)
	if err != nil {
		return 0, err
	}
	return buf[0], nil
}

// readLine reads a line (for line mode)
func (s *stdinChannelState) readLine() ([]byte, error) {
	line, err := s.lineReader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	// Trim newline
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return []byte(line), nil
}

// PopulateIOModule creates native IO channels and registers them in the io module
// Creates: io::#stdin/#in, io::#stdout/#out, io::#stderr/#err, io::#stdio/#io
// If config is provided, uses custom channels; otherwise creates default OS-backed channels
// If executor is provided, channels are stored in storedObjects for proper ID tracking
func (env *ModuleEnvironment) PopulateIOModule(config *IOChannelConfig, executor *Executor) {
	env.mu.Lock()
	defer env.mu.Unlock()

	// Create io module section if it doesn't exist
	if env.LibraryInherited["io"] == nil {
		env.LibraryInherited["io"] = make(ModuleSection)
	}
	ioModule := env.LibraryInherited["io"]

	var stdinCh, stdoutCh, stderrCh, stdioCh *StoredChannel

	// Determine which streams to use for defaults
	defaultStdin := io.Reader(os.Stdin)
	defaultStdout := io.Writer(os.Stdout)
	defaultStderr := io.Writer(os.Stderr)
	if config != nil {
		if config.DefaultStdin != nil {
			defaultStdin = config.DefaultStdin
		}
		if config.DefaultStdout != nil {
			defaultStdout = config.DefaultStdout
		}
		if config.DefaultStderr != nil {
			defaultStderr = config.DefaultStderr
		}
	}

	// Use provided channels or create defaults
	if config != nil && config.Stdin != nil {
		stdinCh = config.Stdin
	} else {
		// Create default stdin channel with mode switching support
		// Check if defaultStdin is os.Stdin and is a terminal
		var stdinState *stdinChannelState
		if f, ok := defaultStdin.(*os.File); ok {
			fd := int(f.Fd())
			stdinState = &stdinChannelState{
				file:       f,
				lineReader: bufio.NewReader(f),
				fd:         fd,
				isTerminal: term.IsTerminal(fd),
			}
		} else {
			// Non-file reader - wrap in state with no terminal support
			stdinState = &stdinChannelState{
				lineReader: bufio.NewReader(defaultStdin),
				isTerminal: false,
			}
		}

		// Register for global cleanup on program exit
		if stdinState.isTerminal {
			globalStdinStateMu.Lock()
			globalStdinState = stdinState
			globalStdinStateMu.Unlock()
		}

		stdinCh = &StoredChannel{
			BufferSize:       0,
			Messages:         make([]ChannelMessage, 0),
			Subscribers:      make(map[int]*StoredChannel),
			NextSubscriberID: 1,
			IsClosed:         false,
			Timestamp:        time.Now(),
			NativeRecv: func() (interface{}, error) {
				stdinState.mu.Lock()
				inRaw := stdinState.inRawMode
				stdinState.mu.Unlock()

				if inRaw {
					// Raw mode: return single bytes
					b, err := stdinState.readByte()
					if err != nil {
						return nil, err
					}
					return []byte{b}, nil
				}
				// Line mode: return full lines
				return stdinState.readLine()
			},
			NativeSend: func(v interface{}) error {
				// Handle mode instructions sent to the input channel
				switch cmd := v.(type) {
				case string:
					switch cmd {
					case "raw", "raw_mode":
						return stdinState.setRawMode()
					case "line", "line_mode":
						return stdinState.setLineMode()
					default:
						return fmt.Errorf("unknown stdin mode: %s", cmd)
					}
				default:
					return fmt.Errorf("stdin accepts mode commands: 'raw' or 'line'")
				}
			},
		}

		// Set terminal capabilities
		if stdinState.isTerminal {
			stdinCh.Terminal = &TerminalCapabilities{
				IsTerminal:    true,
				SupportsInput: true,
				LineMode:      true, // Starts in line mode
				EchoEnabled:   true,
			}
		}
	}

	if config != nil && config.Stdout != nil {
		stdoutCh = config.Stdout
	} else {
		// Create default stdout channel - write-only
		// Normalizes newlines (\n -> \r\n) for consistent behavior in raw/cooked terminal modes
		stdout := defaultStdout // capture for closure
		stdoutCh = &StoredChannel{
			BufferSize:       0,
			Messages:         make([]ChannelMessage, 0),
			Subscribers:      make(map[int]*StoredChannel),
			NextSubscriberID: 1,
			IsClosed:         false,
			Timestamp:        time.Now(),
			NativeSend: func(v interface{}) error {
				// Convert to string and normalize newlines for consistent terminal behavior
				var text string
				switch val := v.(type) {
				case []byte:
					text = string(val)
				case string:
					text = val
				default:
					text = fmt.Sprintf("%v", v)
				}
				text = normalizeNewlines(text)
				_, err := io.WriteString(stdout, text)
				return err
			},
			NativeRecv: func() (interface{}, error) {
				return nil, fmt.Errorf("cannot receive from stdout")
			},
		}
	}

	if config != nil && config.Stderr != nil {
		stderrCh = config.Stderr
	} else {
		// Create default stderr channel - write-only
		// Normalizes newlines (\n -> \r\n) for consistent behavior in raw/cooked terminal modes
		stderr := defaultStderr // capture for closure
		stderrCh = &StoredChannel{
			BufferSize:       0,
			Messages:         make([]ChannelMessage, 0),
			Subscribers:      make(map[int]*StoredChannel),
			NextSubscriberID: 1,
			IsClosed:         false,
			Timestamp:        time.Now(),
			NativeSend: func(v interface{}) error {
				// Convert to string and normalize newlines for consistent terminal behavior
				var text string
				switch val := v.(type) {
				case []byte:
					text = string(val)
				case string:
					text = val
				default:
					text = fmt.Sprintf("%v", v)
				}
				text = normalizeNewlines(text)
				_, err := io.WriteString(stderr, text)
				return err
			},
			NativeRecv: func() (interface{}, error) {
				return nil, fmt.Errorf("cannot receive from stderr")
			},
		}
	}

	if config != nil && config.Stdio != nil {
		stdioCh = config.Stdio
	} else {
		// Create default stdio channel - bidirectional (read from stdin, write to stdout)
		// Normalizes newlines (\n -> \r\n) for consistent behavior in raw/cooked terminal modes
		stdioReader := bufio.NewReader(defaultStdin)
		stdout := defaultStdout // capture for closure
		stdioCh = &StoredChannel{
			BufferSize:       0,
			Messages:         make([]ChannelMessage, 0),
			Subscribers:      make(map[int]*StoredChannel),
			NextSubscriberID: 1,
			IsClosed:         false,
			Timestamp:        time.Now(),
			NativeSend: func(v interface{}) error {
				// Convert to string and normalize newlines for consistent terminal behavior
				var text string
				switch val := v.(type) {
				case []byte:
					text = string(val)
				case string:
					text = val
				default:
					text = fmt.Sprintf("%v", v)
				}
				text = normalizeNewlines(text)
				_, err := io.WriteString(stdout, text)
				return err
			},
			NativeRecv: func() (interface{}, error) {
				line, err := stdioReader.ReadString('\n')
				if err != nil {
					return nil, err
				}
				// Trim the newline and return as raw bytes
				if len(line) > 0 && line[len(line)-1] == '\n' {
					line = line[:len(line)-1]
				}
				if len(line) > 0 && line[len(line)-1] == '\r' {
					line = line[:len(line)-1]
				}
				return []byte(line), nil
			},
		}
	}

	// Set system terminal capabilities on default channels
	// All four channels share the same system terminal, so they share the same capabilities pointer
	// This means a resize event only needs to update one TerminalCapabilities instance
	sysCaps := GetSystemTerminalCapabilities()
	if stdinCh.Terminal == nil {
		stdinCh.Terminal = sysCaps
	}
	if stdoutCh.Terminal == nil {
		stdoutCh.Terminal = sysCaps
	}
	if stderrCh.Terminal == nil {
		stderrCh.Terminal = sysCaps
	}
	if stdioCh.Terminal == nil {
		stdioCh.Terminal = sysCaps
	}

	// Register channels with both full and short names
	// Full names
	ioModule["#stdin"] = &ModuleItem{Type: "object", Value: stdinCh}
	ioModule["#stdout"] = &ModuleItem{Type: "object", Value: stdoutCh}
	ioModule["#stderr"] = &ModuleItem{Type: "object", Value: stderrCh}
	ioModule["#stdio"] = &ModuleItem{Type: "object", Value: stdioCh}

	// Short names (aliases to the same channel objects)
	ioModule["#in"] = &ModuleItem{Type: "object", Value: stdinCh}
	ioModule["#out"] = &ModuleItem{Type: "object", Value: stdoutCh}
	ioModule["#err"] = &ModuleItem{Type: "object", Value: stderrCh}
	ioModule["#io"] = &ModuleItem{Type: "object", Value: stdioCh}

	// Debug output channel (alias to stdout, allows separate redirection of debug output)
	ioModule["#debug"] = &ModuleItem{Type: "object", Value: stdoutCh}

	// Create default random number generator
	// Uses current time as seed for non-reproducible random numbers
	if executor != nil {
		rngSource := rand.New(rand.NewSource(time.Now().UnixNano()))
		// Use very long timeout (effectively permanent)
		tokenID := executor.RequestCompletionToken(nil, "", 100*365*24*time.Hour, nil, nil)
		executor.mu.Lock()
		if tokenData, exists := executor.activeTokens[tokenID]; exists {
			tokenData.IteratorState = &IteratorState{
				Type: "rng",
				Rng:  rngSource,
			}
		}
		executor.mu.Unlock()
		tokenMarker := fmt.Sprintf("\x00TOKEN:%s\x00", tokenID)
		ioModule["#random"] = &ModuleItem{Type: "object", Value: Symbol(tokenMarker)}
	}

	// Register any custom channels from config
	if config != nil && config.CustomChannels != nil {
		for name, ch := range config.CustomChannels {
			if ch != nil {
				ioModule[name] = &ModuleItem{Type: "object", Value: ch}
			}
		}
	}

	// Also update LibraryRestricted to include the new io objects
	// (commands were already added via RegisterCommandInModule)
	if env.LibraryRestricted["io"] == nil {
		env.LibraryRestricted["io"] = make(ModuleSection)
	}
	// Add only the channel objects (not commands - they're already there)
	env.LibraryRestricted["io"]["#stdin"] = ioModule["#stdin"]
	env.LibraryRestricted["io"]["#stdout"] = ioModule["#stdout"]
	env.LibraryRestricted["io"]["#stderr"] = ioModule["#stderr"]
	env.LibraryRestricted["io"]["#stdio"] = ioModule["#stdio"]
	env.LibraryRestricted["io"]["#in"] = ioModule["#in"]
	env.LibraryRestricted["io"]["#out"] = ioModule["#out"]
	env.LibraryRestricted["io"]["#err"] = ioModule["#err"]
	env.LibraryRestricted["io"]["#io"] = ioModule["#io"]
	env.LibraryRestricted["io"]["#debug"] = ioModule["#debug"]
	if ioModule["#random"] != nil {
		env.LibraryRestricted["io"]["#random"] = ioModule["#random"]
	}

	// Add custom channels to LibraryRestricted as well
	if config != nil && config.CustomChannels != nil {
		for name, ch := range config.CustomChannels {
			if ch != nil {
				env.LibraryRestricted["io"][name] = &ModuleItem{Type: "object", Value: ch}
			}
		}
	}

	// Store channels in executor's storedObjects for proper ID tracking
	// This allows channels to be passed to macros and stored in lists
	if executor != nil {
		// Store each unique channel (stdin, stdout, stderr, stdio)
		// Note: we only store unique channels to avoid duplicate IDs
		storedChannels := make(map[*StoredChannel]bool)
		for _, ch := range []*StoredChannel{stdinCh, stdoutCh, stderrCh, stdioCh} {
			if ch != nil && !storedChannels[ch] {
				executor.RegisterObject(ch, ObjChannel)
				storedChannels[ch] = true
			}
		}
		// Store custom channels as well
		if config != nil && config.CustomChannels != nil {
			for _, ch := range config.CustomChannels {
				if ch != nil && !storedChannels[ch] {
					executor.RegisterObject(ch, ObjChannel)
					storedChannels[ch] = true
				}
			}
		}
	}

	// Also add io channel objects to ObjectsInherited so they're accessible
	// via tilde (~#stdout, ~#out, etc.) without explicit IMPORT
	if env.ObjectsInherited == nil {
		env.ObjectsInherited = make(map[string]interface{})
	}
	env.ObjectsInherited["#stdin"] = stdinCh
	env.ObjectsInherited["#stdout"] = stdoutCh
	env.ObjectsInherited["#stderr"] = stderrCh
	env.ObjectsInherited["#stdio"] = stdioCh
	env.ObjectsInherited["#in"] = stdinCh
	env.ObjectsInherited["#out"] = stdoutCh
	env.ObjectsInherited["#err"] = stderrCh
	env.ObjectsInherited["#io"] = stdioCh
	env.ObjectsInherited["#debug"] = stdoutCh

	// Add #random token to ObjectsInherited if it exists
	if ioModule["#random"] != nil {
		if item, ok := ioModule["#random"].Value.(Symbol); ok {
			env.ObjectsInherited["#random"] = item
		}
	}

	// Add custom channels to ObjectsInherited as well
	if config != nil && config.CustomChannels != nil {
		for name, ch := range config.CustomChannels {
			if ch != nil {
				env.ObjectsInherited[name] = ch
			}
		}
	}
}
