//go:build !windows

package main

import (
	"os"
	"os/signal"
	"runtime"
	"syscall"
)

func init() {
	// Lock the main goroutine to the main OS thread.
	// This is required for Qt on macOS to avoid signal handling conflicts.
	runtime.LockOSThread()

	// On macOS, Qt (or its underlying frameworks) installs signal handlers
	// without the SA_ONSTACK flag, which conflicts with Go's signal handling.
	// We take control of SIGURG before Qt initializes to prevent the crash.
	// SIGURG is used for out-of-band data on sockets.
	if runtime.GOOS == "darwin" {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGURG)
		go func() {
			for range sigCh {
				// Ignore SIGURG - it's not needed for our application
			}
		}()
	}
}
