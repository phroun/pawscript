// Example program demonstrating the CLI terminal adapter
//
// This runs a shell inside a terminal emulator window within your actual terminal.
// The terminal window has a border and status bar, and supports scrollback navigation.
//
// Controls:
//   - Shift+PageUp/PageDown: Scroll through scrollback buffer
//   - Shift+Up/Down: Scroll one line at a time
//   - Shift+Home/End: Jump to top/bottom of scrollback
//   - All other input is passed to the shell
//
// Usage:
//   go run main.go                    # Run default shell
//   go run main.go -- vim file.txt    # Run vim
//   go run main.go -- htop            # Run htop

package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/phroun/purfecterm/cli"
)

func main() {
	// Determine command to run
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	var args []string
	if len(os.Args) > 1 {
		// Check for -- separator
		for i, arg := range os.Args[1:] {
			if arg == "--" {
				if i+2 < len(os.Args) {
					shell = os.Args[i+2]
					args = os.Args[i+3:]
				}
				break
			}
		}
	}

	// Create terminal with options
	opts := cli.Options{
		AutoSize:       true,              // Fill available space
		BorderStyle:    cli.BorderRounded, // Nice rounded corners
		Title:          "PurfecTerm CLI",
		ShowStatusBar:  true,
		ScrollbackSize: 10000,
	}

	term, err := cli.New(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create terminal: %v\n", err)
		os.Exit(1)
	}

	// Handle cleanup on signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		term.Stop()
		os.Exit(0)
	}()

	// Set exit callback
	exitCode := 0
	term.SetOnExit(func(code int) {
		exitCode = code
	})

	// Start the terminal (enters raw mode, etc.)
	if err := term.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start terminal: %v\n", err)
		os.Exit(1)
	}

	// Run the shell/command
	if err := term.RunCommand(shell, args...); err != nil {
		term.Stop()
		fmt.Fprintf(os.Stderr, "Failed to run command: %v\n", err)
		os.Exit(1)
	}

	// Wait for the shell to exit
	term.Wait()

	// Clean up
	term.Stop()

	os.Exit(exitCode)
}
