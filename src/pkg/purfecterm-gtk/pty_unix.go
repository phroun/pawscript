//go:build !windows
// +build !windows

package purfectermgtk

/*
#define _XOPEN_SOURCE 600
#include <stdlib.h>
#include <string.h>
#include <fcntl.h>
#include <unistd.h>
#include <sys/ioctl.h>

// ptsname_r might not be available on all platforms, use ptsname
static int get_ptsname(int fd, char *buf, size_t buflen) {
    char *name = ptsname(fd);
    if (name == NULL) {
        return -1;
    }
    size_t len = strlen(name);
    if (len >= buflen) {
        return -1;
    }
    strcpy(buf, name);
    return 0;
}

static int grant_pt(int fd) {
    return grantpt(fd);
}

static int unlock_pt(int fd) {
    return unlockpt(fd);
}

static int set_winsize(int fd, unsigned short rows, unsigned short cols) {
    struct winsize ws;
    ws.ws_row = rows;
    ws.ws_col = cols;
    ws.ws_xpixel = 0;
    ws.ws_ypixel = 0;
    return ioctl(fd, TIOCSWINSZ, &ws);
}
*/
import "C"

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// UnixPTY implements PTY for Unix systems (Linux, macOS, BSD)
type UnixPTY struct {
	master *os.File
	slave  *os.File
}

// NewPTY creates a new PTY
func NewPTY() (PTY, error) {
	return newUnixPTY()
}

func newUnixPTY() (*UnixPTY, error) {
	// Open master PTY
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}

	fd := C.int(master.Fd())

	// Grant access to slave
	if C.grant_pt(fd) != 0 {
		master.Close()
		return nil, errors.New("grantpt failed")
	}

	// Unlock slave
	if C.unlock_pt(fd) != 0 {
		master.Close()
		return nil, errors.New("unlockpt failed")
	}

	// Get slave name
	var buf [256]C.char
	if C.get_ptsname(fd, &buf[0], 256) != 0 {
		master.Close()
		return nil, errors.New("ptsname failed")
	}
	slaveName := C.GoString(&buf[0])

	// Open slave - don't use O_NOCTTY so it can become controlling terminal
	slave, err := os.OpenFile(slaveName, os.O_RDWR, 0)
	if err != nil {
		master.Close()
		return nil, err
	}

	return &UnixPTY{
		master: master,
		slave:  slave,
	}, nil
}

// Start starts the PTY with the given command
func (p *UnixPTY) Start(cmd *exec.Cmd) error {
	// Set up command to use slave as stdin/stdout/stderr
	cmd.Stdin = p.slave
	cmd.Stdout = p.slave
	cmd.Stderr = p.slave

	// Set up session and controlling terminal
	// Ctty must be set to the fd in the child's perspective (after dup2, it's 0)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0, // stdin will be the controlling terminal
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return err
	}

	// Close slave in parent - child has its own copy
	p.slave.Close()
	p.slave = nil

	return nil
}

// Read reads from the PTY
func (p *UnixPTY) Read(b []byte) (int, error) {
	return p.master.Read(b)
}

// Write writes to the PTY
func (p *UnixPTY) Write(b []byte) (int, error) {
	return p.master.Write(b)
}

// Resize resizes the PTY
func (p *UnixPTY) Resize(cols, rows int) error {
	fd := C.int(p.master.Fd())
	if C.set_winsize(fd, C.ushort(rows), C.ushort(cols)) != 0 {
		return errors.New("TIOCSWINSZ failed")
	}
	return nil
}

// Close closes the PTY
func (p *UnixPTY) Close() error {
	if p.slave != nil {
		p.slave.Close()
	}
	return p.master.Close()
}
