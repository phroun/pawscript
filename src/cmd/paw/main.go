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
	unrestrictedFlag := flag.Bool("unrestricted", false, "Disable all file/exec access restrictions")
	readRootsFlag := flag.String("read-roots", "", "Additional directories for file reading")
	writeRootsFlag := flag.String("write-roots", "", "Additional directories for file writing")
	execRootsFlag := flag.String("exec-roots", "", "Additional directories for exec command")
	sandboxFlag := flag.String("sandbox", "", "Restrict all access to this directory only")

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

	// Build file access configuration
	// Default: sandboxed to safe paths. Use --unrestricted to disable.
	var fileAccess *pawscript.FileAccessConfig

	if !*unrestrictedFlag {
		fileAccess = &pawscript.FileAccessConfig{}

		// Determine script directory and current working directory
		var scriptDir string
		if scriptFile != "" {
			absScript, err := filepath.Abs(scriptFile)
			if err == nil {
				scriptDir = filepath.Dir(absScript)
			}
		}
		cwd, _ := os.Getwd()
		tmpDir := os.TempDir()

		// Helper to expand SCRIPT_DIR placeholder and resolve path
		expandPath := func(path string) string {
			path = strings.TrimSpace(path)
			if path == "" {
				return ""
			}
			// Replace SCRIPT_DIR placeholder with actual script directory
			if strings.HasPrefix(path, "SCRIPT_DIR/") {
				if scriptDir != "" {
					path = filepath.Join(scriptDir, path[11:])
				} else {
					return "" // No script dir available, skip this path
				}
			} else if path == "SCRIPT_DIR" {
				if scriptDir != "" {
					path = scriptDir
				} else {
					return ""
				}
			}
			absPath, err := filepath.Abs(path)
			if err != nil {
				return ""
			}
			return absPath
		}

		// Helper to parse comma-separated roots with SCRIPT_DIR expansion
		parseRoots := func(rootsStr string) []string {
			var roots []string
			for _, root := range strings.Split(rootsStr, ",") {
				if expanded := expandPath(root); expanded != "" {
					roots = append(roots, expanded)
				}
			}
			return roots
		}

		if *sandboxFlag != "" {
			// --sandbox overrides all defaults with a single directory
			absPath, err := filepath.Abs(*sandboxFlag)
			if err != nil {
				errorPrintf("Error resolving sandbox path: %v\n", err)
				os.Exit(1)
			}
			fileAccess.ReadRoots = []string{absPath}
			fileAccess.WriteRoots = []string{absPath}
			fileAccess.ExecRoots = []string{absPath}
		} else {
			// Check environment variables first (override defaults if set)
			envReadRoots := os.Getenv("PAW_READ_ROOTS")
			envWriteRoots := os.Getenv("PAW_WRITE_ROOTS")
			envExecRoots := os.Getenv("PAW_EXEC_ROOTS")

			if envReadRoots != "" {
				fileAccess.ReadRoots = parseRoots(envReadRoots)
			} else {
				// Default read roots: SCRIPT_DIR, cwd, /tmp
				if scriptDir != "" {
					fileAccess.ReadRoots = append(fileAccess.ReadRoots, scriptDir)
				}
				if cwd != "" && cwd != scriptDir {
					fileAccess.ReadRoots = append(fileAccess.ReadRoots, cwd)
				}
				fileAccess.ReadRoots = append(fileAccess.ReadRoots, tmpDir)
			}

			if envWriteRoots != "" {
				fileAccess.WriteRoots = parseRoots(envWriteRoots)
			} else {
				// Default write roots: SCRIPT_DIR/saves, SCRIPT_DIR/output, cwd/saves, cwd/output, /tmp
				if scriptDir != "" {
					fileAccess.WriteRoots = append(fileAccess.WriteRoots, filepath.Join(scriptDir, "saves"))
					fileAccess.WriteRoots = append(fileAccess.WriteRoots, filepath.Join(scriptDir, "output"))
				}
				if cwd != "" {
					fileAccess.WriteRoots = append(fileAccess.WriteRoots, filepath.Join(cwd, "saves"))
					fileAccess.WriteRoots = append(fileAccess.WriteRoots, filepath.Join(cwd, "output"))
				}
				fileAccess.WriteRoots = append(fileAccess.WriteRoots, tmpDir)
			}

			if envExecRoots != "" {
				fileAccess.ExecRoots = parseRoots(envExecRoots)
			} else {
				// Default exec roots: SCRIPT_DIR/helpers, SCRIPT_DIR/bin
				if scriptDir != "" {
					fileAccess.ExecRoots = append(fileAccess.ExecRoots, filepath.Join(scriptDir, "helpers"))
					fileAccess.ExecRoots = append(fileAccess.ExecRoots, filepath.Join(scriptDir, "bin"))
				}
			}

			// Add any additional roots from command-line flags (appended to env/defaults)
			if *readRootsFlag != "" {
				fileAccess.ReadRoots = append(fileAccess.ReadRoots, parseRoots(*readRootsFlag)...)
			}
			if *writeRootsFlag != "" {
				fileAccess.WriteRoots = append(fileAccess.WriteRoots, parseRoots(*writeRootsFlag)...)
			}
			if *execRootsFlag != "" {
				fileAccess.ExecRoots = append(fileAccess.ExecRoots, parseRoots(*execRootsFlag)...)
			}
		}
	}
	// If --unrestricted, fileAccess remains nil (no restrictions)

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
  --unrestricted      Disable all file/exec access restrictions
  --sandbox DIR       Restrict all access to DIR only
  --read-roots DIRS   Additional directories for reading
  --write-roots DIRS  Additional directories for writing
  --exec-roots DIRS   Additional directories for exec command

Arguments:
  script.paw          Script file to execute (adds .paw extension if needed)
  --                  Separates script filename from arguments

Default Security Sandbox:
  Read:   SCRIPT_DIR, CWD, /tmp
  Write:  SCRIPT_DIR/saves, SCRIPT_DIR/output, CWD/saves, CWD/output, /tmp
  Exec:   SCRIPT_DIR/helpers, SCRIPT_DIR/bin

Environment Variables (use SCRIPT_DIR as placeholder):
  PAW_READ_ROOTS      Override default read roots
  PAW_WRITE_ROOTS     Override default write roots
  PAW_EXEC_ROOTS      Override default exec roots

Examples:
  paw hello.paw                    # Execute with default sandbox
  paw --unrestricted hello.paw     # No file/exec restrictions
  paw --sandbox /myapp test.paw    # Restrict all to /myapp
  paw --exec-roots /usr/bin test.paw  # Add /usr/bin to exec roots

  # Environment variable with SCRIPT_DIR placeholder:
  export PAW_WRITE_ROOTS="SCRIPT_DIR/data,/tmp"
`
	fmt.Fprint(os.Stderr, usage)
}
