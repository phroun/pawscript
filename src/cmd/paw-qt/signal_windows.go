//go:build windows

package main

import "runtime"

func init() {
	// Lock the main goroutine to the main OS thread.
	// This is required for Qt GUI applications.
	runtime.LockOSThread()
	// No SIGURG handling needed on Windows - Unix signals don't exist there
}
