package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"github.com/phroun/pawscript"
)

func main() {
	// Define command line flags
	debugFlag := flag.Bool("debug", false, "Enable debug output")
	verboseFlag := flag.Bool("verbose", false, "Enable verbose output (alias for -debug)")
	flag.BoolVar(debugFlag, "d", false, "Enable debug output (short)")
	flag.BoolVar(verboseFlag, "v", false, "Enable verbose output (short, alias for -debug)")
	
	// Custom usage function
	flag.Usage = showUsage
	
	// Parse flags
	flag.Parse()
	
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
			fmt.Fprintf(os.Stderr, "Error: Script file not found: %s\n", requestedFile)
			if !strings.Contains(requestedFile, ".") {
				fmt.Fprintf(os.Stderr, "Also tried: %s.paw\n", requestedFile)
			}
			os.Exit(1)
		}
		
		scriptFile = foundFile
		
		content, err := os.ReadFile(scriptFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading script file: %v\n", err)
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
			fmt.Fprintf(os.Stderr, "Error reading from stdin: %v\n", err)
			os.Exit(1)
		}
		scriptContent = string(content)
		
	} else {
		// No filename and stdin is not redirected - show usage
		showUsage()
		os.Exit(1)
	}
	
	// Create PawScript interpreter
	ps := pawscript.New(&pawscript.Config{
		Debug:                debug,  // Use the flag value
		AllowMacros:          true,
		EnableSyntacticSugar: true,
		ShowErrorContext:     true,
		ContextLines:         2,
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
				fmt.Fprintf(os.Stderr, "Timeout waiting for async operations to complete\n")
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

func showUsage() {
	usage := `Usage: paw [options] [script.paw] [-- args...]
       paw [options] < input.paw
       echo "commands" | paw [options]

Execute PawScript commands from a file, stdin, or pipe.

Options:
  -d, -debug          Enable debug output
  -v, -verbose        Enable verbose output (same as -debug)
  
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
`
	fmt.Fprint(os.Stderr, usage)
}
