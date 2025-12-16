//go:build !windows

package main

/*
#include <signal.h>
#include <string.h>

// Empty signal handler that does nothing
static void empty_handler(int sig) {
    // Do nothing - just prevent the signal from being handled by Qt
}

// Install a signal handler with SA_ONSTACK flag before Qt can install one without it
static void install_sigurg_handler() {
    struct sigaction sa;
    memset(&sa, 0, sizeof(sa));
    sa.sa_handler = empty_handler;
    sa.sa_flags = SA_ONSTACK;
    sigemptyset(&sa.sa_mask);
    sigaction(SIGURG, &sa, NULL);
}
*/
import "C"

import (
	"runtime"
)

func init() {
	// Lock the main goroutine to the main OS thread.
	// This is required for Qt on macOS to avoid signal handling conflicts.
	runtime.LockOSThread()

	// On macOS, Qt (or its underlying frameworks) installs signal handlers
	// without the SA_ONSTACK flag, which conflicts with Go's signal handling.
	// We install our own handler with SA_ONSTACK before Qt initializes.
	if runtime.GOOS == "darwin" {
		C.install_sigurg_handler()
	}
}
