package main

import (
	"fmt"
	"time"

	"github.com/phroun/pawscript"
)

func main() {
	ps := pawscript.New(&pawscript.Config{
		Debug:       false,
		AllowMacros: true,
	})

	// Register standard library (includes exec)
	ps.RegisterStandardLibrary([]string{})

	// Add a custom echo command for output
	ps.RegisterCommand("show", func(ctx *pawscript.Context) pawscript.Result {
		for i, arg := range ctx.Args {
			if i > 0 {
				fmt.Print(" ")
			}
			fmt.Print(arg)
		}
		fmt.Println()
		return pawscript.BoolStatus(true)
	})

	fmt.Println("=== PawScript exec Command - Practical Examples ===")

	// Example 1: System Information
	fmt.Println("--- Example 1: System Information ---")
	ps.Execute(`
		show "System Information:"
		show "  Hostname: {exec hostname}"
		show "  User: {exec whoami}"
		show "  Date: {exec date}"
	`)
	fmt.Println()

	// Example 2: File Operations
	fmt.Println("--- Example 2: File Operations ---")
	ps.Execute(`
		show "Checking for README file..."
		exec test, "-f", "README.md" &
		show "  ✓ README.md exists" |
		show "  ✗ README.md not found"
	`)
	fmt.Println()

	// Example 3: Git Integration
	fmt.Println("--- Example 3: Git Integration ---")
	ps.DefineMacro("git_info", `
		show "Git Repository Info:"
		exec git, "rev-parse", "--abbrev-ref", "HEAD" &
		show "  Branch: {get_result}" |
		show "  Not a git repository"
	`)
	ps.ExecuteMacro("git_info")
	fmt.Println()

	// Example 4: Parallel Command Execution
	fmt.Println("--- Example 4: Parallel Execution ---")
	fmt.Println("Running 3 date commands in parallel...")
	start := time.Now()
	ps.Execute(`show "Results: {exec date, '+%H:%M:%S'} {exec date, '+%H:%M:%S'} {exec date, '+%H:%M:%S'}"`)
	fmt.Printf("(Completed in %v - all executed simultaneously)\n", time.Since(start))
	fmt.Println()

	// Example 5: Conditional Pipeline
	fmt.Println("--- Example 5: Conditional Pipeline ---")
	ps.Execute(`
		show "Starting build pipeline..."
		
		show "Step 1: Check Go installation" &
		exec which, "go" &
		show "  ✓ Go found" &
		
		show "Step 2: Check if go.mod exists" &
		exec test, "-f", "go.mod" &
		show "  ✓ go.mod found" &
		
		show "Step 3: All checks passed!" |
		show "  ✗ Pipeline failed - fix errors above"
	`)
	fmt.Println()

	// Example 6: Data Processing
	fmt.Println("--- Example 6: Working with JSON (if jq available) ---")
	ps.Execute(`
		exec which, "jq" &
		show "Testing jq with sample JSON..." &
		exec sh, "-c", "echo '{\"name\":\"John\",\"age\":30}' | jq -r .name" &
		show "  Parsed name: {get_result}" |
		show "  jq not installed - skipping"
	`)
	fmt.Println()

	// Example 7: Environment and Path
	fmt.Println("--- Example 7: Environment Information ---")
	ps.Execute(`
		show "Current directory: {exec pwd}"
		show "Home directory: {exec sh, '-c', 'echo $HOME'}"
	`)
	fmt.Println()

	// Example 8: Building a Deploy Script
	fmt.Println("--- Example 8: Deployment Workflow ---")
	ps.DefineMacro("deploy", `
		show "=== Deployment Started ==="
		
		show "1. Checking git status..." &
		exec git, "diff-index", "--quiet", "HEAD" &
		show "   ✓ Working tree clean" &
		
		show "2. Running tests..." &
		exec go, "test", "./..." &
		show "   ✓ Tests passed" &
		
		show "3. Building application..." &
		exec go, "build", "-o", "app" &
		show "   ✓ Build successful" &
		
		show "=== Deployment Complete ===" |
		show "=== Deployment Failed ==="
	`)

	fmt.Println("Simulating deployment (will fail if not in a git repo with tests):")
	ps.ExecuteMacro("deploy")
	fmt.Println()

	// Example 9: File Monitoring
	fmt.Println("--- Example 9: File Monitoring ---")
	ps.DefineMacro("check_files", `
		show "File Status Report:"
		exec test, "-f", "go.mod" &
		show "  ✓ go.mod" |
		show "  ✗ go.mod missing"
		
		exec test, "-f", "README.md" &
		show "  ✓ README.md" |
		show "  ✗ README.md missing"
		
		exec test, "-d", ".git" &
		show "  ✓ .git directory" |
		show "  ✗ Not a git repository"
	`)
	ps.ExecuteMacro("check_files")
	fmt.Println()

	// Example 10: Interactive Script
	fmt.Println("--- Example 10: Interactive Workflow ---")
	ps.DefineMacro("project_info", `
		show "Project Information:"
		exec test, "-f", "go.mod" &
		exec sh, "-c", "head -1 go.mod | awk '{print $2}'" &
		show "  Module: {get_result}" |
		show "  Not a Go project"
		
		exec sh, "-c", "find . -name '*.go' | wc -l" &
		show "  Go files: {get_result}"
		
		exec git, "log", "-1", "--format=%s" &
		show "  Last commit: {get_result}" |
		show "  No git history"
	`)
	ps.ExecuteMacro("project_info")
	fmt.Println()

	fmt.Println("=== Examples Complete ===")
	fmt.Println("\nKey Takeaways:")
	fmt.Println("1. exec integrates PawScript with system commands")
	fmt.Println("2. Use braces for parallel execution")
	fmt.Println("3. Conditional chains create robust workflows")
	fmt.Println("4. Macros encapsulate common command patterns")
	fmt.Println("5. stderr output provides immediate feedback")
}
