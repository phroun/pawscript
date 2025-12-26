// Package cli provides a CLI-based terminal emulator adapter for PurfecTerm.
// It renders a terminal emulator within an actual CLI terminal, handling all VT100/ANSI
// escape sequence interpretation through its own interpreter and rendering the result
// in a window within the actual CLI screen.
package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"

	"github.com/phroun/purfecterm"
	"golang.org/x/term"
)

// BorderStyle defines the visual style for the terminal window border
type BorderStyle int

const (
	BorderNone   BorderStyle = iota // No border
	BorderSingle                    // Single-line box drawing characters
	BorderDouble                    // Double-line box drawing characters
	BorderHeavy                     // Heavy/thick box drawing characters
	BorderRounded                   // Rounded corners (single line)
)

// TerminalCapabilities describes the capabilities of the terminal
type TerminalCapabilities struct {
	TermType      string
	IsTerminal    bool
	IsRedirected  bool
	SupportsANSI  bool
	SupportsColor bool
	ColorDepth    int // 0, 8, 16, 256, or 24
	Width         int
	Height        int
	SupportsInput bool
	EchoEnabled   bool
	LineMode      bool
}

// Options configures terminal creation
type Options struct {
	Cols           int                    // Terminal width in columns (default: auto-detect or 80)
	Rows           int                    // Terminal height in rows (default: auto-detect or 24)
	ScrollbackSize int                    // Number of scrollback lines (default: 10000)
	Scheme         purfecterm.ColorScheme // Color scheme (default: DefaultColorScheme())
	Shell          string                 // Shell to run (default: $SHELL or /bin/sh)
	WorkingDir     string                 // Initial working directory (default: current dir)

	// Display options
	BorderStyle   BorderStyle // Border style around the terminal window
	Title         string      // Window title (displayed in top border if applicable)
	OffsetX       int         // X offset from top-left of actual terminal (0 = left edge)
	OffsetY       int         // Y offset from top-left of actual terminal (0 = top edge)

	// If true, the terminal window auto-sizes to fill available space
	AutoSize bool

	// If true, render a status bar at the bottom
	ShowStatusBar bool
}

// Terminal is a complete terminal emulator running within a CLI terminal
type Terminal struct {
	mu sync.Mutex

	buffer  *purfecterm.Buffer
	parser  *purfecterm.Parser
	pty     purfecterm.PTY
	cmd     *exec.Cmd
	options Options

	// Rendering state
	renderer   *Renderer
	input      *InputHandler
	lastRender [][]purfecterm.Cell // For differential rendering

	// Terminal state
	running    bool
	done       chan struct{}
	stopRender chan struct{}

	// Original terminal state for restoration
	oldState *term.State

	// Actual terminal size
	hostCols int
	hostRows int

	// Callbacks
	onExit   func(int)            // Called when child process exits with exit code
	onResize func(cols, rows int) // Called when terminal is resized

	// Input callback for intercepting input before sending to PTY
	inputCallback func([]byte) bool // Return true to consume input
}

// New creates a new CLI terminal emulator
func New(opts Options) (*Terminal, error) {
	// Apply defaults
	if opts.Cols <= 0 {
		opts.Cols = 80
	}
	if opts.Rows <= 0 {
		opts.Rows = 24
	}
	if opts.ScrollbackSize <= 0 {
		opts.ScrollbackSize = 10000
	}
	if opts.Shell == "" {
		opts.Shell = os.Getenv("SHELL")
		if opts.Shell == "" {
			opts.Shell = "/bin/sh"
		}
	}
	if opts.WorkingDir == "" {
		opts.WorkingDir, _ = os.Getwd()
	}
	if opts.Scheme.DarkForeground == (purfecterm.Color{}) {
		opts.Scheme = purfecterm.DefaultColorScheme()
	}

	// Detect host terminal size if auto-sizing
	hostCols, hostRows := getHostTerminalSize()
	if opts.AutoSize {
		// Account for border if present
		borderOffset := 0
		if opts.BorderStyle != BorderNone {
			borderOffset = 2 // Top and bottom border
		}
		statusOffset := 0
		if opts.ShowStatusBar {
			statusOffset = 1
		}
		opts.Cols = hostCols - opts.OffsetX*2
		opts.Rows = hostRows - opts.OffsetY*2 - borderOffset - statusOffset
		if opts.Cols < 20 {
			opts.Cols = 20
		}
		if opts.Rows < 5 {
			opts.Rows = 5
		}
	}

	// Create buffer and parser
	buffer := purfecterm.NewBuffer(opts.Cols, opts.Rows, opts.ScrollbackSize)
	parser := purfecterm.NewParser(buffer)

	t := &Terminal{
		buffer:     buffer,
		parser:     parser,
		options:    opts,
		done:       make(chan struct{}),
		stopRender: make(chan struct{}),
		hostCols:   hostCols,
		hostRows:   hostRows,
	}

	// Create renderer
	t.renderer = NewRenderer(t)

	// Create input handler
	t.input = NewInputHandler(t)

	// Set dirty callback for efficient rendering
	buffer.SetDirtyCallback(func() {
		t.renderer.RequestRender()
	})

	return t, nil
}

// getHostTerminalSize returns the current size of the host terminal
func getHostTerminalSize() (cols, rows int) {
	cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 80, 24
	}
	return cols, rows
}

// Start initializes the terminal, enters raw mode, and starts rendering
func (t *Terminal) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Enter raw mode
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to enter raw mode: %w", err)
	}
	t.oldState = oldState

	// Hide host cursor
	fmt.Print("\033[?25l")

	// Enable alternate screen buffer
	fmt.Print("\033[?1049h")

	// Clear screen
	fmt.Print("\033[2J\033[H")

	// Set up SIGWINCH handler for terminal resize
	go t.handleSIGWINCH()

	// Start render loop
	go t.renderer.RenderLoop()

	// Start input loop
	go t.input.InputLoop()

	return nil
}

// handleSIGWINCH listens for terminal resize signals
func (t *Terminal) handleSIGWINCH() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGWINCH)
	defer signal.Stop(sigChan)

	for {
		select {
		case <-sigChan:
			t.handleResize()
		case <-t.done:
			return
		}
	}
}

// handleResize updates terminal size when the host terminal is resized
func (t *Terminal) handleResize() {
	t.mu.Lock()
	defer t.mu.Unlock()

	newCols, newRows := getHostTerminalSize()
	if newCols == t.hostCols && newRows == t.hostRows {
		return
	}
	t.hostCols = newCols
	t.hostRows = newRows

	if t.options.AutoSize {
		// Recalculate terminal size
		borderOffset := 0
		if t.options.BorderStyle != BorderNone {
			borderOffset = 2
		}
		statusOffset := 0
		if t.options.ShowStatusBar {
			statusOffset = 1
		}
		cols := newCols - t.options.OffsetX*2
		rows := newRows - t.options.OffsetY*2 - borderOffset - statusOffset
		if cols < 20 {
			cols = 20
		}
		if rows < 5 {
			rows = 5
		}

		t.buffer.Resize(cols, rows)
		if t.pty != nil {
			t.pty.Resize(cols, rows)
		}
		t.options.Cols = cols
		t.options.Rows = rows
	}

	// Force full redraw
	t.lastRender = nil
	t.renderer.RequestRender()

	if t.onResize != nil {
		t.onResize(t.options.Cols, t.options.Rows)
	}
}

// RunShell starts the default shell in the terminal
func (t *Terminal) RunShell() error {
	return t.RunCommand(t.options.Shell)
}

// RunCommand runs a command in the terminal
func (t *Terminal) RunCommand(name string, args ...string) error {
	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		return fmt.Errorf("command already running")
	}
	t.done = make(chan struct{})
	t.mu.Unlock()

	// Create PTY
	pty, err := purfecterm.NewPTY()
	if err != nil {
		return fmt.Errorf("failed to create PTY: %w", err)
	}

	t.mu.Lock()
	t.pty = pty
	t.mu.Unlock()

	// Create command
	cmd := exec.Command(name, args...)
	cmd.Dir = t.options.WorkingDir
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	)

	// Start PTY
	if err := pty.Start(cmd); err != nil {
		pty.Close()
		return fmt.Errorf("failed to start PTY: %w", err)
	}

	t.mu.Lock()
	t.cmd = cmd
	t.running = true
	t.mu.Unlock()

	// Set initial size
	pty.Resize(t.options.Cols, t.options.Rows)

	// Start reading from PTY
	go t.readLoop()

	// Wait for command to exit
	go func() {
		exitCode := 0
		if err := cmd.Wait(); err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				exitCode = exitError.ExitCode()
			}
		}
		t.mu.Lock()
		t.running = false
		t.mu.Unlock()

		if t.onExit != nil {
			t.onExit(exitCode)
		}
		close(t.done)
	}()

	return nil
}

// readLoop reads output from the PTY and feeds it to the parser
func (t *Terminal) readLoop() {
	buf := make([]byte, 4096)
	for {
		t.mu.Lock()
		pty := t.pty
		running := t.running
		t.mu.Unlock()

		if !running || pty == nil {
			return
		}

		n, err := pty.Read(buf)
		if n > 0 {
			t.parser.Parse(buf[:n])
		}
		if err != nil {
			if err != io.EOF {
				// Could log error here
			}
			return
		}
	}
}

// Feed writes data directly to the terminal display (bypassing PTY)
func (t *Terminal) Feed(data []byte) {
	t.parser.Parse(data)
}

// FeedString writes a string to the terminal display
func (t *Terminal) FeedString(data string) {
	t.parser.ParseString(data)
}

// Write writes to the terminal's PTY (sends input to child process)
func (t *Terminal) Write(data []byte) (int, error) {
	t.mu.Lock()
	pty := t.pty
	t.mu.Unlock()
	if pty == nil {
		return 0, nil
	}
	return pty.Write(data)
}

// WriteString writes a string to the terminal's PTY
func (t *Terminal) WriteString(s string) (int, error) {
	return t.Write([]byte(s))
}

// GetSize returns the terminal size in columns and rows
func (t *Terminal) GetSize() (cols, rows int) {
	return t.buffer.GetSize()
}

// GetHostSize returns the host terminal size
func (t *Terminal) GetHostSize() (cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.hostCols, t.hostRows
}

// Resize resizes the terminal
func (t *Terminal) Resize(cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.buffer.Resize(cols, rows)
	t.options.Cols = cols
	t.options.Rows = rows

	if t.pty != nil {
		t.pty.Resize(cols, rows)
	}

	// Force full redraw
	t.lastRender = nil
	t.renderer.RequestRender()
}

// Buffer returns the underlying terminal buffer
func (t *Terminal) Buffer() *purfecterm.Buffer {
	return t.buffer
}

// ScrollUp scrolls the view up by n lines (into scrollback)
func (t *Terminal) ScrollUp(n int) {
	current := t.buffer.GetScrollOffset()
	max := t.buffer.GetMaxScrollOffset()
	newOffset := current + n
	if newOffset > max {
		newOffset = max
	}
	t.buffer.SetScrollOffset(newOffset)
}

// ScrollDown scrolls the view down by n lines (toward current output)
func (t *Terminal) ScrollDown(n int) {
	current := t.buffer.GetScrollOffset()
	newOffset := current - n
	if newOffset < 0 {
		newOffset = 0
	}
	t.buffer.SetScrollOffset(newOffset)
}

// ScrollToTop scrolls to the top of scrollback
func (t *Terminal) ScrollToTop() {
	max := t.buffer.GetMaxScrollOffset()
	t.buffer.SetScrollOffset(max)
}

// ScrollToBottom scrolls to the bottom (current output)
func (t *Terminal) ScrollToBottom() {
	t.buffer.SetScrollOffset(0)
}

// GetScrollOffset returns the current scroll offset
func (t *Terminal) GetScrollOffset() int {
	return t.buffer.GetScrollOffset()
}

// GetMaxScrollOffset returns the maximum scroll offset
func (t *Terminal) GetMaxScrollOffset() int {
	return t.buffer.GetMaxScrollOffset()
}

// Clear clears the terminal screen
func (t *Terminal) Clear() {
	t.buffer.ClearScreen()
}

// ClearScrollback clears the scrollback buffer
func (t *Terminal) ClearScrollback() {
	t.buffer.ClearScrollback()
}

// Reset resets the terminal to initial state
func (t *Terminal) Reset() {
	t.buffer.Reset()
}

// IsRunning returns true if a command is running
func (t *Terminal) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}

// Wait waits for the terminal process to exit
func (t *Terminal) Wait() {
	<-t.done
}

// SetInputCallback sets a callback for intercepting input
// Return true from the callback to consume the input
func (t *Terminal) SetInputCallback(fn func([]byte) bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inputCallback = fn
}

// SetOnExit sets a callback for when the child process exits
func (t *Terminal) SetOnExit(fn func(int)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onExit = fn
}

// SetOnResize sets a callback for terminal resize events
func (t *Terminal) SetOnResize(fn func(cols, rows int)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onResize = fn
}

// SetTitle sets the terminal window title
func (t *Terminal) SetTitle(title string) {
	t.mu.Lock()
	t.options.Title = title
	t.mu.Unlock()
	t.renderer.RequestRender()
}

// GetTerminalCapabilities returns the terminal capabilities
func (t *Terminal) GetTerminalCapabilities() *TerminalCapabilities {
	cols, rows := t.GetSize()
	return &TerminalCapabilities{
		TermType:      "xterm-256color",
		IsTerminal:    true,
		IsRedirected:  false,
		SupportsANSI:  true,
		SupportsColor: true,
		ColorDepth:    24,
		Width:         cols,
		Height:        rows,
		SupportsInput: true,
		EchoEnabled:   false,
		LineMode:      false,
	}
}

// GetSelectedText returns currently selected text
func (t *Terminal) GetSelectedText() string {
	return t.buffer.GetSelectedText()
}

// SaveScrollbackText returns the scrollback buffer as plain text
func (t *Terminal) SaveScrollbackText() string {
	return t.buffer.SaveScrollbackText()
}

// SaveScrollbackANS returns the scrollback with ANSI codes preserved
func (t *Terminal) SaveScrollbackANS() string {
	return t.buffer.SaveScrollbackANS()
}

// SetColorScheme sets the terminal color scheme
func (t *Terminal) SetColorScheme(scheme purfecterm.ColorScheme) {
	t.mu.Lock()
	t.options.Scheme = scheme
	t.mu.Unlock()
	t.renderer.RequestRender()
}

// Stop stops the terminal and restores the original terminal state
func (t *Terminal) Stop() error {
	// Signal stop
	close(t.stopRender)

	// Kill child process if running
	t.mu.Lock()
	if t.cmd != nil && t.cmd.Process != nil {
		t.cmd.Process.Kill()
	}
	if t.pty != nil {
		t.pty.Close()
	}
	oldState := t.oldState
	t.mu.Unlock()

	// Restore terminal state
	if oldState != nil {
		// Disable alternate screen buffer
		fmt.Print("\033[?1049l")

		// Show cursor
		fmt.Print("\033[?25h")

		// Reset attributes
		fmt.Print("\033[0m")

		// Restore terminal mode
		term.Restore(int(os.Stdin.Fd()), oldState)
	}

	return nil
}

// Close is an alias for Stop
func (t *Terminal) Close() error {
	return t.Stop()
}
