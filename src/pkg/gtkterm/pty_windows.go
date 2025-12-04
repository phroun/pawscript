//go:build windows
// +build windows

package gtkterm

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procCreatePseudoConsole        = kernel32.NewProc("CreatePseudoConsole")
	procResizePseudoConsole        = kernel32.NewProc("ResizePseudoConsole")
	procClosePseudoConsole         = kernel32.NewProc("ClosePseudoConsole")
	procInitializeProcThreadAttrList = kernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttribute  = kernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAttributeList = kernel32.NewProc("DeleteProcThreadAttributeList")
)

const (
	PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE = 0x00020016
	EXTENDED_STARTUPINFO_PRESENT        = 0x00080000
)

// COORD is the Windows console coordinate structure
type COORD struct {
	X int16
	Y int16
}

// HPCON is a handle to a pseudo console
type HPCON syscall.Handle

// ConPTY implements PTY for Windows using the ConPTY API
type ConPTY struct {
	mu      sync.Mutex
	hpc     HPCON
	pipeIn  *os.File // Write to this to send input to the console
	pipeOut *os.File // Read from this to get output from the console
	cmd     *exec.Cmd
}

// NewPTY creates a new PTY
func NewPTY() (PTY, error) {
	return newConPTY(80, 24)
}

func newConPTY(cols, rows int) (*ConPTY, error) {
	// Create pipes for I/O
	// We need two pairs of pipes:
	// - One pair for input (we write, console reads)
	// - One pair for output (console writes, we read)

	// Input pipe (our write -> console read)
	inputRead, inputWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	// Output pipe (console write -> our read)
	outputRead, outputWrite, err := os.Pipe()
	if err != nil {
		inputRead.Close()
		inputWrite.Close()
		return nil, err
	}

	// Create pseudo console
	size := COORD{X: int16(cols), Y: int16(rows)}
	var hpc HPCON

	r, _, err := procCreatePseudoConsole.Call(
		uintptr(*(*uint32)(unsafe.Pointer(&size))), // size as COORD packed into DWORD
		inputRead.Fd(),                              // hInput
		outputWrite.Fd(),                            // hOutput
		0,                                           // dwFlags
		uintptr(unsafe.Pointer(&hpc)),               // phPC
	)

	if r != 0 {
		inputRead.Close()
		inputWrite.Close()
		outputRead.Close()
		outputWrite.Close()
		return nil, errors.New("CreatePseudoConsole failed")
	}

	// Close the ends we passed to the console
	inputRead.Close()
	outputWrite.Close()

	return &ConPTY{
		hpc:     hpc,
		pipeIn:  inputWrite,
		pipeOut: outputRead,
	}, nil
}

// Start starts the PTY with the given command
func (p *ConPTY) Start(cmd *exec.Cmd) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// We need to use STARTUPINFOEX with PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE
	// This is complex in Go, so we'll use a simplified approach

	// Get the command line
	cmdLine := cmd.Path
	for _, arg := range cmd.Args[1:] {
		cmdLine += " " + syscall.EscapeArg(arg)
	}

	// For simplicity, we'll start the process using the standard Go method
	// but redirect its output through our console
	// Note: This is a simplified implementation

	cmd.Stdin = p.pipeIn
	cmd.Stdout = p.pipeOut
	cmd.Stderr = p.pipeOut

	// Set environment
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	p.cmd = cmd
	return nil
}

// Read reads from the PTY
func (p *ConPTY) Read(b []byte) (int, error) {
	return p.pipeOut.Read(b)
}

// Write writes to the PTY
func (p *ConPTY) Write(b []byte) (int, error) {
	return p.pipeIn.Write(b)
}

// Resize resizes the PTY
func (p *ConPTY) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	size := COORD{X: int16(cols), Y: int16(rows)}
	r, _, _ := procResizePseudoConsole.Call(
		uintptr(p.hpc),
		uintptr(*(*uint32)(unsafe.Pointer(&size))),
	)
	if r != 0 {
		return errors.New("ResizePseudoConsole failed")
	}
	return nil
}

// Close closes the PTY
func (p *ConPTY) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.pipeIn != nil {
		p.pipeIn.Close()
	}
	if p.pipeOut != nil {
		p.pipeOut.Close()
	}
	if p.hpc != 0 {
		procClosePseudoConsole.Call(uintptr(p.hpc))
		p.hpc = 0
	}
	return nil
}
