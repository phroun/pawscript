package main

import (
	"flag"
	"fmt"
	"github.com/phroun/pawscript"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var version = "dev" // set via -ldflags at build time

// ANSI color codes for terminal output
const (
	colorYellow = "\x1b[93m" // Bright yellow foreground
	colorReset  = "\x1b[0m"  // Reset to default
)

// stderrSupportsColor checks if stderr is a terminal that supports color output
// Returns true if we should use ANSI color codes
func stderrSupportsColor() bool {
	// Check if stderr is a terminal (not redirected/piped)
	stderrInfo, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	// ModeCharDevice indicates a terminal
	if (stderrInfo.Mode() & os.ModeCharDevice) == 0 {
		return false
	}

	// Respect NO_COLOR environment variable (https://no-color.org/)
	if _, exists := os.LookupEnv("NO_COLOR"); exists {
		return false
	}

	// Check TERM isn't "dumb" (which doesn't support colors)
	if term := os.Getenv("TERM"); term == "dumb" {
		return false
	}

	return true
}

// errorPrintf prints an error message to stderr, using color if supported
func errorPrintf(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	if stderrSupportsColor() {
		fmt.Fprintf(os.Stderr, "%s%s%s", colorYellow, message, colorReset)
	} else {
		fmt.Fprint(os.Stderr, message)
	}
}

func main() {

	// Define command line flags
	licenseFlag := flag.Bool("license", false, "Show license")
	debugFlag := flag.Bool("debug", false, "Enable debug output")
	verboseFlag := flag.Bool("verbose", false, "Enable verbose output (alias for -debug)")
	flag.BoolVar(debugFlag, "d", false, "Enable debug output (short)")
	flag.BoolVar(verboseFlag, "v", false, "Enable verbose output (short, alias for -debug)")

	// File access control flags
	readRootsFlag := flag.String("read-roots", "", "Comma-separated directories allowed for file reading")
	writeRootsFlag := flag.String("write-roots", "", "Comma-separated directories allowed for file writing")
	sandboxFlag := flag.String("sandbox", "", "Restrict both read and write to this directory")

	// Custom usage function
	flag.Usage = showUsage

	// Parse flags
	flag.Parse()
	
	if (*licenseFlag) {
		showLicense()
		os.Exit(0)
	}

	// Verbose is an alias for debug
	debug := *debugFlag || *verboseFlag

	// Get remaining arguments after flags
	args := flag.Args()

	var scriptFile string
	var scriptContent string
	var scriptArgs []string

	// Check for -- separator
	separatorIndex := -1
	for i, arg := range args {
		if arg == "--" {
			separatorIndex = i
			break
		}
	}

	var fileArgs []string
	if separatorIndex != -1 {
		fileArgs = args[:separatorIndex]
		scriptArgs = args[separatorIndex+1:]
	} else {
		fileArgs = args
	}

	// Check if stdin is redirected/piped
	stdinInfo, _ := os.Stdin.Stat()
	isStdinRedirected := (stdinInfo.Mode() & os.ModeCharDevice) == 0

	if len(fileArgs) > 0 {
		// Filename provided
		requestedFile := fileArgs[0]
		foundFile := findScriptFile(requestedFile)

		if foundFile == "" {
			errorPrintf("Error: Script file not found: %s\n", requestedFile)
			if !strings.Contains(requestedFile, ".") {
				errorPrintf("Also tried: %s.paw\n", requestedFile)
			}
			os.Exit(1)
		}

		scriptFile = foundFile

		content, err := os.ReadFile(scriptFile)
		if err != nil {
			errorPrintf("Error reading script file: %v\n", err)
			os.Exit(1)
		}
		scriptContent = string(content)

		// Remaining fileArgs become script arguments (if no separator was used)
		if separatorIndex == -1 && len(fileArgs) > 1 {
			scriptArgs = fileArgs[1:]
		}

	} else if isStdinRedirected {
		// No filename, but stdin is redirected - read from stdin
		content, err := io.ReadAll(os.Stdin)
		if err != nil {
			errorPrintf("Error reading from stdin: %v\n", err)
			os.Exit(1)
		}
		scriptContent = string(content)

	} else {
		// No filename and stdin is not redirected - show usage
		showCopyright()
		showUsage()
		os.Exit(1)
	}

	// Build file access configuration from flags
	var fileAccess *pawscript.FileAccessConfig
	if *sandboxFlag != "" || *readRootsFlag != "" || *writeRootsFlag != "" {
		fileAccess = &pawscript.FileAccessConfig{}

		// Sandbox overrides individual roots
		if *sandboxFlag != "" {
			absPath, err := filepath.Abs(*sandboxFlag)
			if err != nil {
				errorPrintf("Error resolving sandbox path: %v\n", err)
				os.Exit(1)
			}
			fileAccess.ReadRoots = []string{absPath}
			fileAccess.WriteRoots = []string{absPath}
		} else {
			// Parse individual roots
			if *readRootsFlag != "" {
				roots := strings.Split(*readRootsFlag, ",")
				for _, root := range roots {
					root = strings.TrimSpace(root)
					if root != "" {
						absPath, err := filepath.Abs(root)
						if err != nil {
							errorPrintf("Error resolving read root path %s: %v\n", root, err)
							os.Exit(1)
						}
						fileAccess.ReadRoots = append(fileAccess.ReadRoots, absPath)
					}
				}
			}
			if *writeRootsFlag != "" {
				roots := strings.Split(*writeRootsFlag, ",")
				for _, root := range roots {
					root = strings.TrimSpace(root)
					if root != "" {
						absPath, err := filepath.Abs(root)
						if err != nil {
							errorPrintf("Error resolving write root path %s: %v\n", root, err)
							os.Exit(1)
						}
						fileAccess.WriteRoots = append(fileAccess.WriteRoots, absPath)
					}
				}
			}
		}
	}

	// Create PawScript interpreter
	ps := pawscript.New(&pawscript.Config{
		Debug:                debug, // Use the flag value
		AllowMacros:          true,
		EnableSyntacticSugar: true,
		ShowErrorContext:     true,
		ContextLines:         2,
		FileAccess:           fileAccess,
	})

	// Register standard library commands
	ps.RegisterStandardLibrary(scriptArgs)

	// Execute the script
	var result pawscript.Result
	if scriptFile != "" {
		result = ps.ExecuteFile(scriptContent, scriptFile)
	} else {
		result = ps.Execute(scriptContent)
	}

	// Exit with appropriate code
	if boolStatus, ok := result.(pawscript.BoolStatus); ok {
		if bool(boolStatus) {
			os.Exit(0)
		} else {
			os.Exit(1)
		}
	}

	// If result is a token, async operations are pending
	// Wait for them to complete
	if _, ok := result.(pawscript.TokenResult); ok {
		// Wait for the token to complete with a timeout
		// We'll check periodically if there are still active tokens
		timeout := time.After(5 * time.Minute)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-timeout:
				errorPrintf("Timeout waiting for async operations to complete\n")
				os.Exit(1)
			case <-ticker.C:
				// Check if there are still active tokens
				status := ps.GetTokenStatus()
				activeCount, _ := status["activeCount"].(int)
				if activeCount == 0 {
					// All tokens completed
					os.Exit(0)
				}
			}
		}
	}

	// Unknown result type, exit successfully
	os.Exit(0)
}

func findScriptFile(filename string) string {
	// First try the exact filename
	if _, err := os.Stat(filename); err == nil {
		return filename
	}

	// If no extension, try adding .paw
	if filepath.Ext(filename) == "" {
		pawFile := filename + ".paw"
		if _, err := os.Stat(pawFile); err == nil {
			return pawFile
		}
	}

	return ""
}

func showCopyright() {
	fmt.Fprintf(os.Stderr, "paw, the pawscript interpreter version %s\nCopyright (c) 2025 Jeffrey R. Day\nLicense: MIT\n\n", version)
}

func showLicense() {
	fmt.Fprintf(os.Stdout, "paw, the pawscript interpreter version %s", version)
	license := `

MIT License

Copyright (c) 2025 Jeffrey R. Day

Permission is hereby granted, free of charge, to any person
obtaining a copy of this software and associated documentation
files (the "Software"), to deal in the Software without
restriction, including without limitation the rights to use,
copy, modify, merge, publish, distribute, sublicense, and/or
sell copies of the Software, and to permit persons to whom the
Software is furnished to do so, subject to the following
conditions:

The above copyright notice and this permission notice
(including the next paragraph) shall be included in all copies
or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES
OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND
NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT
HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY,
WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING
FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
OTHER DEALINGS IN THE SOFTWARE.
`
	fmt.Fprint(os.Stdout, license)
}

func showUsage() {
	usage := `Usage: paw [options] [script.paw] [-- args...]
       paw [options] < input.paw
       echo "commands" | paw [options]

Execute PawScript commands from a file, stdin, or pipe.

Options:
  --license           View license and exit
  -d, -debug          Enable debug output
  -v, -verbose        Enable verbose output (same as -debug)
  --sandbox DIR       Restrict file read/write to DIR only
  --read-roots DIRS   Comma-separated directories allowed for reading
  --write-roots DIRS  Comma-separated directories allowed for writing

Arguments:
  script.paw          Script file to execute (adds .paw extension if needed)
  --                  Separates script filename from arguments (for stdin input)

Examples:
  paw hello.paw               # Execute hello.paw
  paw hello                   # Execute hello.paw (adds .paw extension)
  paw -d hello.paw            # Execute with debug output
  paw --verbose hello.paw     # Execute with verbose output
  paw script.paw -- a b       # Execute script.paw with args "a" and "b"
  echo "echo Hello" | paw     # Execute commands from pipe
  paw -d -- a ab < my.paw     # Execute from stdin with arguments and debug
  paw --sandbox /tmp test.paw # Restrict file access to /tmp
  paw --read-roots /data,/config --write-roots /tmp test.paw
`
	fmt.Fprint(os.Stderr, usage)
}
