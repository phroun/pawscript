package purfectermqt

import (
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/mappu/miqt/qt"
	"github.com/phroun/pawscript/pkg/purfecterm"
)

// Options configures terminal creation
type Options struct {
	Cols           int                    // Terminal width in columns (default: 80)
	Rows           int                    // Terminal height in rows (default: 24)
	ScrollbackSize int                    // Number of scrollback lines (default: 10000)
	FontFamily     string                 // Font family (default: "Monospace")
	FontSize       int                    // Font size in points (default: 14)
	Scheme         purfecterm.ColorScheme // Color scheme (default: DefaultColorScheme())
	Shell          string                 // Shell to run (default: $SHELL or /bin/sh)
	WorkingDir     string                 // Initial working directory (default: current dir)
}

// Terminal is a complete terminal emulator widget
type Terminal struct {
	mu sync.Mutex

	widget  *Widget
	pty     purfecterm.PTY
	cmd     *exec.Cmd
	options Options

	// I/O
	running bool
	done    chan struct{}
}

// New creates a new terminal emulator
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
	if opts.FontFamily == "" {
		opts.FontFamily = "Monospace"
	}
	if opts.FontSize <= 0 {
		opts.FontSize = 14
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
	if opts.Scheme.Foreground == (purfecterm.Color{}) {
		opts.Scheme = purfecterm.DefaultColorScheme()
	}

	// Create widget
	widget := NewWidget(opts.Cols, opts.Rows, opts.ScrollbackSize)
	widget.SetFont(opts.FontFamily, opts.FontSize)
	widget.SetColorScheme(opts.Scheme)

	t := &Terminal{
		widget:  widget,
		options: opts,
		done:    make(chan struct{}),
	}

	// Set input callback
	widget.SetInputCallback(func(data []byte) {
		t.mu.Lock()
		pty := t.pty
		t.mu.Unlock()
		if pty != nil {
			pty.Write(data)
		}
	})

	return t, nil
}

// Widget returns the Qt widget
func (t *Terminal) Widget() *qt.QWidget {
	return t.widget.QWidget()
}

// Feed writes data directly to the terminal display
func (t *Terminal) Feed(data string) {
	t.widget.FeedString(data)
}

// FeedBytes writes binary data to the terminal display
func (t *Terminal) FeedBytes(data []byte) {
	t.widget.Feed(data)
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
		return nil
	}
	t.done = make(chan struct{})
	t.mu.Unlock()

	// Create PTY
	pty, err := purfecterm.NewPTY()
	if err != nil {
		return err
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
		return err
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
		cmd.Wait()
		t.mu.Lock()
		t.running = false
		t.mu.Unlock()
		close(t.done)
	}()

	return nil
}

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
			t.widget.Feed(buf[:n])
		}
		if err != nil {
			if err != io.EOF {
				// Log error?
			}
			return
		}
	}
}

// Write writes to the terminal's PTY
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

// Clear clears the terminal screen
func (t *Terminal) Clear() {
	t.widget.Clear()
}

// Resize resizes the terminal
func (t *Terminal) Resize(cols, rows int) {
	t.widget.Resize(cols, rows)
	t.mu.Lock()
	pty := t.pty
	t.mu.Unlock()
	if pty != nil {
		pty.Resize(cols, rows)
	}
}

// GetSize returns the terminal size
func (t *Terminal) GetSize() (cols, rows int) {
	return t.widget.GetSize()
}

// Close closes the terminal
func (t *Terminal) Close() error {
	t.mu.Lock()
	pty := t.pty
	cmd := t.cmd
	t.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
	}
	if pty != nil {
		pty.Close()
	}
	return nil
}

// Wait waits for the terminal process to exit
func (t *Terminal) Wait() {
	<-t.done
}

// IsRunning returns true if a command is running
func (t *Terminal) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}

// GetSelectedText returns currently selected text
func (t *Terminal) GetSelectedText() string {
	return t.widget.GetSelectedText()
}

// CopySelection copies selected text to clipboard
func (t *Terminal) CopySelection() {
	t.widget.CopySelection()
}

// PasteClipboard pastes text from clipboard into terminal
func (t *Terminal) PasteClipboard() {
	t.widget.PasteClipboard()
}

// SelectAll selects all text
func (t *Terminal) SelectAll() {
	t.widget.SelectAll()
}

// SetCursorVisible shows or hides the cursor
func (t *Terminal) SetCursorVisible(visible bool) {
	t.widget.SetCursorVisible(visible)
}

// SetWorkingDirectory sets the working directory for future commands
func (t *Terminal) SetWorkingDirectory(dir string) {
	t.mu.Lock()
	t.options.WorkingDir = dir
	t.mu.Unlock()
}

// SetInputCallback sets a callback for handling keyboard input
func (t *Terminal) SetInputCallback(fn func([]byte)) {
	t.widget.SetInputCallback(fn)
}

// --- Screen Scaling Mode Methods ---

// Set132ColumnMode enables or disables 132-column mode (horizontal scale 0.6060)
func (t *Terminal) Set132ColumnMode(enabled bool) {
	t.widget.buffer.Set132ColumnMode(enabled)
}

// Get132ColumnMode returns whether 132-column mode is enabled
func (t *Terminal) Get132ColumnMode() bool {
	return t.widget.buffer.Get132ColumnMode()
}

// Set40ColumnMode enables or disables 40-column mode (horizontal scale 2.0)
func (t *Terminal) Set40ColumnMode(enabled bool) {
	t.widget.buffer.Set40ColumnMode(enabled)
}

// Get40ColumnMode returns whether 40-column mode is enabled
func (t *Terminal) Get40ColumnMode() bool {
	return t.widget.buffer.Get40ColumnMode()
}

// SetLineDensity sets the line density (25, 30, 43, 50, or 60)
func (t *Terminal) SetLineDensity(density int) {
	t.widget.buffer.SetLineDensity(density)
}

// GetLineDensity returns the current line density
func (t *Terminal) GetLineDensity() int {
	return t.widget.buffer.GetLineDensity()
}

// GetHorizontalScale returns the combined horizontal scaling factor
func (t *Terminal) GetHorizontalScale() float64 {
	return t.widget.buffer.GetHorizontalScale()
}

// GetVerticalScale returns the vertical scaling factor
func (t *Terminal) GetVerticalScale() float64 {
	return t.widget.buffer.GetVerticalScale()
}
