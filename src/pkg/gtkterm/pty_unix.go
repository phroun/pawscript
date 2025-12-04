//go:build !windows
// +build !windows

package gtkterm

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"
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

	// Get slave name and unlock
	slaveName, err := ptsname(master)
	if err != nil {
		master.Close()
		return nil, err
	}

	if err := unlockpt(master); err != nil {
		master.Close()
		return nil, err
	}

	// Open slave
	slave, err := os.OpenFile(slaveName, os.O_RDWR|syscall.O_NOCTTY, 0)
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
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
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
	ws := &winsize{
		Row: uint16(rows),
		Col: uint16(cols),
	}
	return windowSizeIoctl(p.master.Fd(), ws)
}

// Close closes the PTY
func (p *UnixPTY) Close() error {
	if p.slave != nil {
		p.slave.Close()
	}
	return p.master.Close()
}

// winsize is the struct for TIOCSWINSZ/TIOCGWINSZ
type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

// ptsname returns the name of the slave PTY
func ptsname(master *os.File) (string, error) {
	var n uint32
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n)))
	if errno != 0 {
		return "", errno
	}
	return "/dev/pts/" + itoa(int(n)), nil
}

// unlockpt unlocks the slave PTY
func unlockpt(master *os.File) error {
	var unlock int32 = 0
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock)))
	if errno != 0 {
		return errno
	}
	return nil
}

// windowSizeIoctl sets the window size
func windowSizeIoctl(fd uintptr, ws *winsize) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(ws)))
	if errno != 0 {
		return errno
	}
	return nil
}

// Simple itoa without importing strconv
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
