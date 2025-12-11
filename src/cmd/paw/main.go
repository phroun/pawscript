package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/phroun/pawscript"
	"golang.org/x/term"
)

var version = "dev" // set via -ldflags at build time

// ANSI color codes for terminal output
const (
	colorYellow    = "\x1b[93m" // Bright yellow foreground
	colorDarkBrown = "\x1b[33m" // Dark yellow/brown for light backgrounds
	colorReset     = "\x1b[0m"  // Reset to default
)

// CLIConfig holds configuration loaded from ~/.paw/paw-cli.psl
type CLIConfig struct {
	TermBackground string // "light", "dark", or "auto" (auto defaults to dark)
}

// Default CLI config
var cliConfig = CLIConfig{
	TermBackground: "auto",
}

// getConfigDir returns the path to ~/.paw directory
func getConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".paw")
}

// getConfigFilePath returns the path to ~/.paw/paw-cli.psl
func getConfigFilePath() string {
	dir := getConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "paw-cli.psl")
}

// loadCLIConfig loads configuration from ~/.paw/paw-cli.psl
// Creates the config file with defaults if it doesn't exist
func loadCLIConfig() {
	configPath := getConfigFilePath()
	if configPath == "" {
		return
	}

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Create config directory and file with defaults
		createDefaultConfig(configPath)
		return
	}

	// Read and parse the config file
	content, err := os.ReadFile(configPath)
	if err != nil {
		return // Graceful failure - use defaults
	}

	// Simple parsing for key: value format
	// Looking for: term_background: "auto" or term_background: auto
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		// Look for term_background setting
		if strings.HasPrefix(line, "term_background:") {
			value := strings.TrimPrefix(line, "term_background:")
			value = strings.TrimSpace(value)
			// Remove quotes if present
			value = strings.Trim(value, "\"'")
			value = strings.ToLower(value)
			if value == "light" || value == "dark" || value == "auto" {
				cliConfig.TermBackground = value
			}
		}
	}
}

// createDefaultConfig creates the default config file
func createDefaultConfig(configPath string) {
	configDir := filepath.Dir(configPath)

	// Try to create the directory
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return // Graceful failure
	}

	// Default config content - using proper PSL comment syntax
	// Line comments start with "# " (hash followed by space)
	defaultConfig := `# PawScript CLI Configuration
# This file is automatically created on first run

# Terminal background color for REPL prompt colors
# Options: "auto", "dark", "light"
#   auto  - assumes dark background (for now)
#   dark  - uses bright yellow prompt
#   light - uses dark brown prompt
term_background: "auto"
`

	// Try to write the file
	_ = os.WriteFile(configPath, []byte(defaultConfig), 0644) // Ignore error - graceful failure
}

// getPromptColor returns the appropriate prompt color based on config
func getPromptColor() string {
	switch cliConfig.TermBackground {
	case "light":
		return colorDarkBrown
	case "dark":
		return colorYellow
	default: // "auto" defaults to dark
		return colorYellow
	}
}

// getEqualsColor returns the color for the "=" prefix in result display
func getEqualsColor() string {
	switch cliConfig.TermBackground {
	case "light":
		return colorDarkGreen
	case "dark":
		return colorBrightGreen
	default: // "auto" defaults to dark
		return colorBrightGreen
	}
}

// getResultColor returns the color for the result value text
func getResultColor() string {
	switch cliConfig.TermBackground {
	case "light":
		return colorSilver
	case "dark":
		return colorDarkGray
	default: // "auto" defaults to dark
		return colorDarkGray
	}
}

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
	// Load CLI configuration from ~/.paw/paw-cli.psl
	loadCLIConfig()

	// Ensure terminal is restored to normal state on exit
	// This is critical when using raw mode (readkey_init) to prevent
	// the terminal from being left in a broken state (no newline translation, etc.)
	defer pawscript.CleanupTerminal()

	// Handle signals to ensure cleanup on interrupt
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		pawscript.CleanupTerminal()
		os.Exit(130) // Standard exit code for SIGINT
	}()

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

	// Optimization level flag
	optLevelFlag := flag.Int("O", 1, "Optimization level (0=no caching, 1=cache macro/loop bodies)")

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
		// No filename and stdin is not redirected - run REPL
		runREPL(debug, *unrestrictedFlag, *optLevelFlag)
		os.Exit(0)
	}

	// Build file access configuration
	// Default: sandboxed to safe paths. Use --unrestricted to disable.
	var fileAccess *pawscript.FileAccessConfig

	// Determine script directory (used for sandbox paths and relative path resolution)
	var scriptDir string
	if scriptFile != "" {
		absScript, err := filepath.Abs(scriptFile)
		if err == nil {
			scriptDir = filepath.Dir(absScript)
		}
	}

	if !*unrestrictedFlag {
		fileAccess = &pawscript.FileAccessConfig{}
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
		ScriptDir:            scriptDir,
		OptLevel:             pawscript.OptimizationLevel(*optLevelFlag),
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
  -O N                Set optimization level (0=no caching, 1=cache macro/loop bodies, default: 1)
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

// REPL color codes
const (
	colorWhite       = "\x1b[97m"
	colorRed         = "\x1b[91m"
	colorGray        = "\x1b[90m"
	colorCyan        = "\x1b[96m"
	colorDarkCyan    = "\x1b[36m"
	colorBrightGreen = "\x1b[92m" // Bright green for dark backgrounds
	colorDarkGreen   = "\x1b[32m" // Dark green for light backgrounds
	colorDarkGray    = "\x1b[90m" // Dark gray for dark backgrounds
	colorSilver      = "\x1b[37m" // Silver/light gray for light backgrounds
)

// runREPL runs an interactive Read-Eval-Print Loop
func runREPL(debug, unrestricted bool, optLevel int) {
	showCopyright()
	fmt.Println("Interactive mode. Type 'exit' or 'quit' to leave.")
	fmt.Println()

	// Set up file access (unrestricted for REPL by default, or use flag)
	var fileAccess *pawscript.FileAccessConfig
	if !unrestricted {
		cwd, _ := os.Getwd()
		tmpDir := os.TempDir()
		fileAccess = &pawscript.FileAccessConfig{
			ReadRoots:  []string{cwd, tmpDir},
			WriteRoots: []string{cwd, tmpDir},
			ExecRoots:  []string{cwd},
		}
	}

	// Create PawScript interpreter
	ps := pawscript.New(&pawscript.Config{
		Debug:                debug,
		AllowMacros:          true,
		EnableSyntacticSugar: true,
		ShowErrorContext:     true,
		ContextLines:         2,
		FileAccess:           fileAccess,
		OptLevel:             pawscript.OptimizationLevel(optLevel),
	})
	ps.RegisterStandardLibrary([]string{})

	// Put terminal in raw mode for key handling
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		fmt.Fprintln(os.Stderr, "REPL requires a terminal")
		return
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set raw mode: %v\n", err)
		return
	}
	defer term.Restore(fd, oldState)

	// History
	history := make([]string, 0, 100)
	historyPos := 0

	// Main REPL loop
	for {
		// Read a complete statement (may span multiple lines)
		input, quit := readStatement(fd, history, &historyPos)
		if quit {
			fmt.Print("\r\n")
			break
		}

		// Add to history if non-empty and different from last entry
		trimmed := strings.TrimSpace(input)
		if trimmed != "" {
			if len(history) == 0 || history[len(history)-1] != trimmed {
				history = append(history, trimmed)
			}
			historyPos = len(history)
		}

		// Check for exit commands
		lower := strings.ToLower(trimmed)
		if lower == "exit" || lower == "quit" {
			break
		}

		if trimmed == "" {
			continue
		}

		// Temporarily restore terminal for script execution (so echo works)
		term.Restore(fd, oldState)

		// Execute - blocks until complete (including async operations like msleep)
		result := ps.Execute(input)

		// Get the result value and format it
		displayResult(ps, result)

		// Back to raw mode
		oldState, _ = term.MakeRaw(fd)
	}
}

// getContinuationPrompt analyzes the input and returns the appropriate continuation prompt
// showing all nesting levels that need to be closed
func getContinuationPrompt(input string) string {
	// Stack to track what's open (in order of opening)
	// We'll use strings: "(", "{", "\"", "'", "#("
	var stack []string
	prevChar := rune(0)

	for _, ch := range input {
		// Check if we're inside a string
		inString := false
		closedString := false
		for j := len(stack) - 1; j >= 0; j-- {
			if stack[j] == "\"" || stack[j] == "'" {
				inString = true
				// Check if this character closes the string
				if (stack[j] == "\"" && ch == '"' && prevChar != '\\') ||
					(stack[j] == "'" && ch == '\'' && prevChar != '\\') {
					stack = stack[:j] // Pop the string opener
					closedString = true
				}
				break
			}
		}

		// Don't process openers if we're in a string OR if we just closed one
		// (closing quote shouldn't also open a new string)
		if !inString && !closedString {
			switch ch {
			case '"':
				stack = append(stack, "\"")
			case '\'':
				stack = append(stack, "'")
			case '(':
				// Check if preceded by # for vector syntax
				if prevChar == '#' {
					stack = append(stack, "#(")
				} else {
					stack = append(stack, "(")
				}
			case ')':
				// Pop the most recent ( or #(
				for j := len(stack) - 1; j >= 0; j-- {
					if stack[j] == "(" || stack[j] == "#(" {
						stack = append(stack[:j], stack[j+1:]...)
						break
					}
				}
			case '{':
				stack = append(stack, "{")
			case '}':
				// Pop the most recent {
				for j := len(stack) - 1; j >= 0; j-- {
					if stack[j] == "{" {
						stack = append(stack[:j], stack[j+1:]...)
						break
					}
				}
			}
		}
		prevChar = ch
	}

	// Build prompt showing all nesting levels
	if len(stack) == 0 {
		return "paw*" // Shouldn't happen if we're in continuation, but fallback
	}

	// Build the nesting indicator from the stack
	var prompt strings.Builder
	for _, item := range stack {
		switch item {
		case "(":
			prompt.WriteString("(")
		case "{":
			prompt.WriteString("{")
		case "\"":
			prompt.WriteString("\"")
		case "'":
			prompt.WriteString("'")
		case "#(":
			prompt.WriteString("#(")
		}
	}
	prompt.WriteString("*")
	return prompt.String()
}

// readStatement reads a complete statement, handling multi-line input
func readStatement(fd int, history []string, historyPos *int) (string, bool) {
	var lines []string
	var currentLine []rune
	cursorPos := 0
	savedLine := ""     // Saved current line when browsing history
	inHistory := false  // Are we browsing history?

	printPrompt := func() {
		promptClr := getPromptColor()
		if len(lines) == 0 {
			fmt.Print(promptClr + "paw*" + colorReset + " ")
		} else {
			// Determine what needs to be closed based on accumulated input
			fullInput := strings.Join(lines, "\n")
			prompt := getContinuationPrompt(fullInput)
			// Show line number in dark cyan, rest of prompt in appropriate color
			lineNum := len(lines) + 1
			fmt.Printf("%s%d %s%s%s ", colorDarkCyan, lineNum, promptClr, prompt, colorReset)
		}
	}

	redrawLine := func() {
		// Clear line and redraw
		fmt.Print("\r\x1b[K") // Move to start and clear line
		printPrompt()
		fmt.Print(string(currentLine))
		// Move cursor to correct position
		if cursorPos < len(currentLine) {
			fmt.Printf("\x1b[%dD", len(currentLine)-cursorPos)
		}
	}

	printPrompt()

	buf := make([]byte, 32)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			return "", true
		}

		i := 0
		for i < n {
			b := buf[i]
			i++

			// Handle escape sequences
			if b == 0x1b && i < n && buf[i] == '[' {
				i++ // consume '['
				if i < n {
					switch buf[i] {
					case 'A': // Up arrow
						i++
						if len(history) > 0 && *historyPos > 0 {
							if !inHistory {
								savedLine = string(currentLine)
								inHistory = true
							}
							*historyPos--
							currentLine = []rune(history[*historyPos])
							cursorPos = len(currentLine)
							redrawLine()
						}
						continue
					case 'B': // Down arrow
						i++
						if inHistory {
							if *historyPos < len(history)-1 {
								*historyPos++
								currentLine = []rune(history[*historyPos])
								cursorPos = len(currentLine)
							} else {
								*historyPos = len(history)
								currentLine = []rune(savedLine)
								cursorPos = len(currentLine)
								inHistory = false
							}
							redrawLine()
						}
						continue
					case 'C': // Right arrow
						i++
						if cursorPos < len(currentLine) {
							cursorPos++
							fmt.Print("\x1b[C")
						}
						continue
					case 'D': // Left arrow
						i++
						if cursorPos > 0 {
							cursorPos--
							fmt.Print("\x1b[D")
						}
						continue
					case '3': // Possible Delete key
						i++
						if i < n && buf[i] == '~' {
							i++
							if cursorPos < len(currentLine) {
								currentLine = append(currentLine[:cursorPos], currentLine[cursorPos+1:]...)
								redrawLine()
							}
						}
						continue
					case 'H': // Home
						i++
						if cursorPos > 0 {
							fmt.Printf("\x1b[%dD", cursorPos)
							cursorPos = 0
						}
						continue
					case 'F': // End
						i++
						if cursorPos < len(currentLine) {
							fmt.Printf("\x1b[%dC", len(currentLine)-cursorPos)
							cursorPos = len(currentLine)
						}
						continue
					}
				}
				// Skip unknown escape sequence
				continue
			}

			switch b {
			case 0x03: // Ctrl+C
				fmt.Print("^C\r\n")
				return "", true

			case 0x04: // Ctrl+D
				if len(currentLine) == 0 && len(lines) == 0 {
					fmt.Print("\r\n")
					return "", true
				}

			case 0x7f, 0x08: // Backspace
				if cursorPos > 0 {
					currentLine = append(currentLine[:cursorPos-1], currentLine[cursorPos:]...)
					cursorPos--
					redrawLine()
				}

			case '\r', '\n': // Enter
				fmt.Print("\r\n")
				line := string(currentLine)
				lines = append(lines, line)
				fullInput := strings.Join(lines, "\n")

				// Check if input is complete
				if isComplete(fullInput) {
					return fullInput, false
				}

				// Continue on next line
				currentLine = nil
				cursorPos = 0
				inHistory = false
				printPrompt()

			case 0x15: // Ctrl+U - clear line
				currentLine = nil
				cursorPos = 0
				redrawLine()

			case 0x0b: // Ctrl+K - kill to end of line
				currentLine = currentLine[:cursorPos]
				redrawLine()

			case 0x01: // Ctrl+A - beginning of line
				if cursorPos > 0 {
					fmt.Printf("\x1b[%dD", cursorPos)
					cursorPos = 0
				}

			case 0x05: // Ctrl+E - end of line
				if cursorPos < len(currentLine) {
					fmt.Printf("\x1b[%dC", len(currentLine)-cursorPos)
					cursorPos = len(currentLine)
				}

			default:
				// Regular character - might be part of UTF-8 sequence
				if b >= 32 && b < 127 {
					// ASCII printable
					currentLine = append(currentLine[:cursorPos], append([]rune{rune(b)}, currentLine[cursorPos:]...)...)
					cursorPos++
					inHistory = false
					redrawLine()
				} else if b >= 0xC0 {
					// UTF-8 start byte - collect full character
					charBytes := []byte{b}
					for i < n && buf[i] >= 0x80 && buf[i] < 0xC0 {
						charBytes = append(charBytes, buf[i])
						i++
					}
					r, _ := utf8.DecodeRune(charBytes)
					if r != utf8.RuneError {
						currentLine = append(currentLine[:cursorPos], append([]rune{r}, currentLine[cursorPos:]...)...)
						cursorPos++
						inHistory = false
						redrawLine()
					}
				}
			}
		}
	}
}

// isComplete checks if the input forms a complete statement
func isComplete(input string) bool {
	// Track nesting and quotes
	parenDepth := 0
	braceDepth := 0
	inDoubleQuote := false
	inSingleQuote := false
	prevChar := rune(0)

	for _, ch := range input {
		if inDoubleQuote {
			if ch == '"' && prevChar != '\\' {
				inDoubleQuote = false
			}
		} else if inSingleQuote {
			if ch == '\'' && prevChar != '\\' {
				inSingleQuote = false
			}
		} else {
			switch ch {
			case '"':
				inDoubleQuote = true
			case '\'':
				inSingleQuote = true
			case '(':
				parenDepth++
			case ')':
				parenDepth--
			case '{':
				braceDepth++
			case '}':
				braceDepth--
			}
		}
		prevChar = ch
	}

	// Also check for #( pattern
	hashParenDepth := 0
	for i := 0; i < len(input)-1; i++ {
		if input[i] == '#' && input[i+1] == '(' {
			hashParenDepth++
		}
	}
	// This is simplified - actual tracking would need to match with closing )

	return !inDoubleQuote && !inSingleQuote && parenDepth <= 0 && braceDepth <= 0
}

// displayResult formats and displays the execution result
func displayResult(ps *pawscript.PawScript, result pawscript.Result) {
	// Get the result value from the interpreter
	resultValue := ps.GetResultValue()

	var prefix string
	var prefixColor string

	if boolStatus, ok := result.(pawscript.BoolStatus); ok {
		if bool(boolStatus) {
			prefix = "="
			prefixColor = getEqualsColor()
		} else {
			prefix = "E"
			prefixColor = colorRed
		}
	} else {
		prefix = "="
		prefixColor = getEqualsColor()
	}

	// Format the result value as JSON
	formatted := formatValueAsJSON(ps, resultValue)
	resultClr := getResultColor()

	// Print with prefix
	lines := strings.Split(formatted, "\n")
	for i, line := range lines {
		if i == 0 {
			fmt.Printf("%s%s%s %s%s%s\n", prefixColor, prefix, colorReset, resultClr, line, colorReset)
		} else {
			fmt.Printf("  %s%s%s\n", resultClr, line, colorReset)
		}
	}
}

// formatValueAsJSON converts a PawScript value to pretty-printed JSON
func formatValueAsJSON(ps *pawscript.PawScript, val interface{}) string {
	if val == nil {
		return "null"
	}

	// Convert to JSON-compatible form
	jsonVal := toJSONValue(ps, val)

	// Pretty print
	jsonBytes, err := json.MarshalIndent(jsonVal, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", val)
	}

	return string(jsonBytes)
}

// toJSONValue converts a PawScript value to a JSON-compatible Go value
func toJSONValue(ps *pawscript.PawScript, val interface{}) interface{} {
	if val == nil {
		return nil
	}

	switch v := val.(type) {
	case pawscript.Symbol:
		str := string(v)
		if str == "undefined" {
			return nil
		}
		if str == "true" {
			return true
		}
		if str == "false" {
			return false
		}
		return str
	case string:
		return v
	case pawscript.QuotedString:
		return string(v)
	case int64:
		return v
	case float64:
		return v
	case int:
		return int64(v)
	case bool:
		return v
	case pawscript.StoredString:
		return string(v)
	case pawscript.StoredBlock:
		return string(v)
	case pawscript.StoredList:
		items := v.Items()
		namedArgs := v.NamedArgs()

		// If only positional items, return array
		if namedArgs == nil || len(namedArgs) == 0 {
			arr := make([]interface{}, len(items))
			for i, item := range items {
				arr[i] = toJSONValue(ps, item)
			}
			return arr
		}

		// If has named args, return object
		obj := make(map[string]interface{})
		if len(items) > 0 {
			arr := make([]interface{}, len(items))
			for i, item := range items {
				arr[i] = toJSONValue(ps, item)
			}
			obj["_items"] = arr
		}
		for k, v := range namedArgs {
			obj[k] = toJSONValue(ps, v)
		}
		return obj
	case *pawscript.StoredChannel:
		return "<channel>"
	case *pawscript.StoredFile:
		return "<file>"
	case pawscript.StoredBytes:
		return v.String()
	case pawscript.StoredStruct:
		return v.String()
	case pawscript.ObjectRef:
		// Resolve ObjectRef to actual value and format that
		if !v.IsValid() {
			return nil
		}
		resolved := ps.ResolveValue(v)
		if resolved == v {
			// Couldn't resolve, show type indicator
			return fmt.Sprintf("<%s>", v.Type.String())
		}
		return toJSONValue(ps, resolved)
	default:
		return fmt.Sprintf("%v", v)
	}
}
