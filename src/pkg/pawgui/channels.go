package pawgui

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/phroun/pawscript/src"
)

// TerminalAdapter provides an abstraction for different terminal implementations.
type TerminalAdapter interface {
	// Feed sends text to the terminal for display.
	Feed(text string)
}

// GUISync provides GUI thread synchronization.
// For GTK, this would use glib.IdleAdd; for Qt, direct calls work.
type GUISync interface {
	// RunOnGUIThread runs a function on the GUI thread and optionally waits.
	// Returns a channel that will be closed when the function completes.
	RunOnGUIThread(fn func()) <-chan struct{}
}

// DirectSync provides direct synchronization (for Qt).
type DirectSync struct{}

func (DirectSync) RunOnGUIThread(fn func()) <-chan struct{} {
	done := make(chan struct{})
	fn()
	close(done)
	return done
}

// ConsoleChannels holds the I/O channels for a console.
type ConsoleChannels struct {
	OutCh    *pawscript.StoredChannel
	InCh     *pawscript.StoredChannel
	TermCaps *pawscript.TerminalCapabilities

	// Internal state
	stdinReader  *io.PipeReader
	stdinWriter  *io.PipeWriter
	outputQueue  chan interface{}
	inputQueue   chan byte
	clearInput   func()
	flush        func()
	inputRunning bool
	mu           sync.Mutex
}

// ConsoleOptions configures console channel creation.
type ConsoleOptions struct {
	Width       int
	Height      int
	Terminal    TerminalAdapter
	GUISync     GUISync // Optional - if nil, uses DirectSync
	FlushTimeout time.Duration
}

// NewConsoleChannels creates I/O channels for a console.
func NewConsoleChannels(opts ConsoleOptions) *ConsoleChannels {
	if opts.Width <= 0 {
		opts.Width = 100
	}
	if opts.Height <= 0 {
		opts.Height = 30
	}
	if opts.GUISync == nil {
		opts.GUISync = DirectSync{}
	}
	if opts.FlushTimeout <= 0 {
		opts.FlushTimeout = 100 * time.Millisecond
	}

	// Terminal capabilities for PawScript
	termCaps := &pawscript.TerminalCapabilities{
		TermType:      "gui-console",
		IsTerminal:    true,
		SupportsANSI:  true,
		SupportsColor: true,
		ColorDepth:    256,
		Width:         opts.Width,
		Height:        opts.Height,
		SupportsInput: true,
		EchoEnabled:   false,
		LineMode:      false,
		Metadata:      make(map[string]interface{}),
	}

	// Create pipes for stdin
	stdinReader, stdinWriter := io.Pipe()

	// Output queue for non-blocking writes to terminal
	outputQueue := make(chan interface{}, 256)

	// Input queue
	inputQueue := make(chan byte, 256)

	cc := &ConsoleChannels{
		TermCaps:    termCaps,
		stdinReader: stdinReader,
		stdinWriter: stdinWriter,
		outputQueue: outputQueue,
		inputQueue:  inputQueue,
	}

	// Start output writer goroutine
	go func() {
		for v := range outputQueue {
			switch d := v.(type) {
			case []byte:
				opts.Terminal.Feed(string(d))
			case string:
				opts.Terminal.Feed(d)
			case chan struct{}:
				// Flush sentinel - signal completion
				close(d)
			}
		}
	}()

	// Start input reader goroutine
	go func() {
		cc.mu.Lock()
		cc.inputRunning = true
		cc.mu.Unlock()

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

	// Create flush function
	cc.flush = func() {
		// Step 1: Wait for outputQueue to drain
		writerDone := make(chan struct{})
		select {
		case outputQueue <- writerDone:
			<-writerDone
		default:
		}

		// Step 2: Wait for GUI synchronization
		done := opts.GUISync.RunOnGUIThread(func() {})
		select {
		case <-done:
		case <-time.After(opts.FlushTimeout):
		}
	}

	// Create clear input function
	cc.clearInput = func() {
		for {
			select {
			case <-inputQueue:
			default:
				return
			}
		}
	}

	// Create output channel
	cc.OutCh = &pawscript.StoredChannel{
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
			// Normalize newlines: \r\n -> \n -> \r\n
			text = strings.ReplaceAll(text, "\r\n", "\n")
			text = strings.ReplaceAll(text, "\n", "\r\n")
			data := []byte(text)
			select {
			case outputQueue <- data:
			default:
				// Queue full - silently drop to prevent deadlock
			}
			return nil
		},
		NativeFlush: func() error {
			cc.flush()
			return nil
		},
	}

	// Create input channel
	cc.InCh = &pawscript.StoredChannel{
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

	return cc
}

// GetIOConfig returns an IOChannelConfig for use with PawScript.
func (cc *ConsoleChannels) GetIOConfig() *pawscript.IOChannelConfig {
	return &pawscript.IOChannelConfig{
		Stdout: cc.OutCh,
		Stdin:  cc.InCh,
		Stderr: cc.OutCh,
	}
}

// WriteInput writes data to the stdin pipe (for script input).
func (cc *ConsoleChannels) WriteInput(data []byte) {
	if cc.stdinWriter != nil {
		cc.stdinWriter.Write(data)
	}
}

// ClearInput clears any pending input.
func (cc *ConsoleChannels) ClearInput() {
	if cc.clearInput != nil {
		cc.clearInput()
	}
}

// Flush waits for all pending output to be displayed.
func (cc *ConsoleChannels) Flush() {
	if cc.flush != nil {
		cc.flush()
	}
}

// Close closes the console channels.
func (cc *ConsoleChannels) Close() {
	if cc.stdinWriter != nil {
		cc.stdinWriter.Close()
	}
	if cc.stdinReader != nil {
		cc.stdinReader.Close()
	}
	close(cc.outputQueue)
}
