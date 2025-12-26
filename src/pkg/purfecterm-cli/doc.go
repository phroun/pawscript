// Package purfectermcli provides a CLI-based terminal emulator adapter for PurfecTerm.
//
// This package implements a "terminal within a terminal" - it runs a terminal emulator
// inside an actual CLI terminal, handling all VT100/ANSI escape sequence interpretation
// through its own interpreter and rendering the result in a window within the actual
// CLI screen.
//
// # Features
//
//   - Full VT100/ANSI escape sequence interpretation via PurfecTerm's parser
//   - Scrollback buffer with Shift+PageUp/PageDown navigation
//   - Multiple border styles (single, double, heavy, rounded)
//   - Optional status bar showing cursor position and scroll status
//   - Window resizing that tracks the host terminal (SIGWINCH)
//   - Differential rendering for efficiency (only updates changed cells)
//   - True color (24-bit) and 256-color support
//   - Full attribute support: bold, italic, underline, strikethrough, blink, reverse
//
// # Basic Usage
//
//	opts := purfectermcli.Options{
//	    AutoSize:      true,                           // Fill available space
//	    BorderStyle:   purfectermcli.BorderRounded,    // Rounded border
//	    Title:         "My Terminal",
//	    ShowStatusBar: true,
//	}
//
//	term, err := purfectermcli.New(opts)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Start the terminal (enters raw mode)
//	if err := term.Start(); err != nil {
//	    log.Fatal(err)
//	}
//	defer term.Stop()
//
//	// Run a shell
//	if err := term.RunShell(); err != nil {
//	    log.Fatal(err)
//	}
//
//	// Wait for shell to exit
//	term.Wait()
//
// # Scrollback Navigation
//
// While running, the following keys navigate the scrollback buffer:
//
//   - Shift+PageUp: Scroll up one page
//   - Shift+PageDown: Scroll down one page
//   - Shift+Up: Scroll up one line
//   - Shift+Down: Scroll down one line
//   - Shift+Home: Jump to top of scrollback
//   - Shift+End: Jump to bottom (current output)
//
// Any regular input automatically scrolls to the bottom.
//
// # Architecture
//
// The package consists of three main components:
//
//   - Terminal: Main struct that manages the PTY, buffer, and coordinates rendering/input
//   - Renderer: Handles rendering the terminal buffer to the actual terminal using ANSI codes
//   - InputHandler: Reads raw input, parses escape sequences, handles scrollback navigation
//
// The Terminal wraps the core purfecterm.Buffer and purfecterm.Parser, reusing all the
// VT100/ANSI parsing logic from the main PurfecTerm package. This ensures consistent
// terminal emulation behavior across Qt, GTK, and CLI adapters.
package purfectermcli
