package pawscript

import (
	"bufio"
	"fmt"
	"os"
	"time"
)

// PopulateIOModule creates native IO channels and registers them in the io module
// Creates: io::#stdin/#in, io::#stdout/#out, io::#stderr/#err, io::#stdio/#io
func (env *ModuleEnvironment) PopulateIOModule() {
	env.mu.Lock()
	defer env.mu.Unlock()

	// Create io module section if it doesn't exist
	if env.LibraryInherited["io"] == nil {
		env.LibraryInherited["io"] = make(ModuleSection)
	}
	ioModule := env.LibraryInherited["io"]

	// Create stdin channel - read-only
	stdinReader := bufio.NewReader(os.Stdin)
	stdinCh := &StoredChannel{
		BufferSize:       0,
		Messages:         make([]ChannelMessage, 0),
		Subscribers:      make(map[int]*StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		NativeRecv: func() (interface{}, error) {
			line, err := stdinReader.ReadString('\n')
			if err != nil {
				return nil, err
			}
			// Trim the newline
			if len(line) > 0 && line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			return line, nil
		},
		NativeSend: func(v interface{}) error {
			return fmt.Errorf("cannot send to stdin")
		},
	}

	// Create stdout channel - write-only
	// Note: NativeSend does NOT add newline - callers add it if needed
	stdoutCh := &StoredChannel{
		BufferSize:       0,
		Messages:         make([]ChannelMessage, 0),
		Subscribers:      make(map[int]*StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		NativeSend: func(v interface{}) error {
			_, err := fmt.Fprintf(os.Stdout, "%v", v)
			return err
		},
		NativeRecv: func() (interface{}, error) {
			return nil, fmt.Errorf("cannot receive from stdout")
		},
	}

	// Create stderr channel - write-only
	// Note: NativeSend does NOT add newline - callers add it if needed
	stderrCh := &StoredChannel{
		BufferSize:       0,
		Messages:         make([]ChannelMessage, 0),
		Subscribers:      make(map[int]*StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		NativeSend: func(v interface{}) error {
			_, err := fmt.Fprintf(os.Stderr, "%v", v)
			return err
		},
		NativeRecv: func() (interface{}, error) {
			return nil, fmt.Errorf("cannot receive from stderr")
		},
	}

	// Create stdio channel - bidirectional (read from stdin, write to stdout)
	// Note: NativeSend does NOT add newline - callers add it if needed
	stdioReader := bufio.NewReader(os.Stdin)
	stdioCh := &StoredChannel{
		BufferSize:       0,
		Messages:         make([]ChannelMessage, 0),
		Subscribers:      make(map[int]*StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		Timestamp:        time.Now(),
		NativeSend: func(v interface{}) error {
			_, err := fmt.Fprintf(os.Stdout, "%v", v)
			return err
		},
		NativeRecv: func() (interface{}, error) {
			line, err := stdioReader.ReadString('\n')
			if err != nil {
				return nil, err
			}
			// Trim the newline
			if len(line) > 0 && line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			return line, nil
		},
	}

	// Register channels with both full and short names
	// Full names
	ioModule["#stdin"] = &ModuleItem{Type: "object", Value: stdinCh}
	ioModule["#stdout"] = &ModuleItem{Type: "object", Value: stdoutCh}
	ioModule["#stderr"] = &ModuleItem{Type: "object", Value: stderrCh}
	ioModule["#stdio"] = &ModuleItem{Type: "object", Value: stdioCh}

	// Short names (aliases to the same channel objects)
	ioModule["#in"] = &ModuleItem{Type: "object", Value: stdinCh}
	ioModule["#out"] = &ModuleItem{Type: "object", Value: stdoutCh}
	ioModule["#err"] = &ModuleItem{Type: "object", Value: stderrCh}
	ioModule["#io"] = &ModuleItem{Type: "object", Value: stdioCh}

	// Also update LibraryRestricted to include the new io objects
	// (commands were already added by PopulateStdlibModules)
	if env.LibraryRestricted["io"] == nil {
		env.LibraryRestricted["io"] = make(ModuleSection)
	}
	// Add only the channel objects (not commands - they're already there)
	env.LibraryRestricted["io"]["#stdin"] = ioModule["#stdin"]
	env.LibraryRestricted["io"]["#stdout"] = ioModule["#stdout"]
	env.LibraryRestricted["io"]["#stderr"] = ioModule["#stderr"]
	env.LibraryRestricted["io"]["#stdio"] = ioModule["#stdio"]
	env.LibraryRestricted["io"]["#in"] = ioModule["#in"]
	env.LibraryRestricted["io"]["#out"] = ioModule["#out"]
	env.LibraryRestricted["io"]["#err"] = ioModule["#err"]
	env.LibraryRestricted["io"]["#io"] = ioModule["#io"]
}
