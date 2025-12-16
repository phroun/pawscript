package purfecterm

import "os/exec"

// PTY is the interface for platform-specific pseudo-terminal implementations
type PTY interface {
	// Start starts the PTY with the given command
	Start(cmd *exec.Cmd) error

	// Read reads from the PTY
	Read(p []byte) (n int, err error)

	// Write writes to the PTY
	Write(p []byte) (n int, err error)

	// Resize resizes the PTY
	Resize(cols, rows int) error

	// Close closes the PTY
	Close() error
}
