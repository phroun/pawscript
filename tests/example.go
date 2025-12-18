package main

// This is an example of using PawScript as a library in a Go application

import (
	"fmt"
	"time"

	"github.com/phroun/pawscript"
)

func main() {
	// Create PawScript interpreter with custom config
	ps := pawscript.New(&pawscript.Config{
		Debug:                true,  // Enable debug logging
		AllowMacros:          true,
		EnableSyntacticSugar: true,
		DefaultTokenTimeout:  5 * time.Minute,
	})
	
	// Register custom commands
	ps.RegisterCommands(map[string]pawscript.Handler{
		"greet": func(ctx *pawscript.Context) pawscript.Result {
			name := "World"
			if len(ctx.Args) > 0 {
				name = fmt.Sprintf("%v", ctx.Args[0])
			}
			fmt.Printf("Hello, %s!\n", name)
			return pawscript.BoolStatus(true)
		},
		
		"calculate": func(ctx *pawscript.Context) pawscript.Result {
			if len(ctx.Args) < 2 {
				return pawscript.BoolStatus(false)
			}
			
			a := ctx.Args[0].(int64)
			b := ctx.Args[1].(int64)
			result := a + b
			
			ctx.SetResult(result)
			fmt.Printf("%d + %d = %d\n", a, b, result)
			return pawscript.BoolStatus(true)
		},
		
		"async_operation": func(ctx *pawscript.Context) pawscript.Result {
			fmt.Println("Starting async operation...")
			
			// Request a token for async operation
			token := ctx.RequestToken(func(tokenID string) {
				fmt.Printf("Operation interrupted: %s\n", tokenID)
			})
			
			// Start async work
			go func() {
				time.Sleep(2 * time.Second)
				fmt.Println("Async operation completed!")
				ctx.SetResult("async_result")
				ctx.ResumeToken(token, true)
			}()
			
			return pawscript.TokenResult(token)
		},
		
		"print_result": func(ctx *pawscript.Context) pawscript.Result {
			if ctx.HasResult() {
				fmt.Printf("Result: %v\n", ctx.GetResult())
			} else {
				fmt.Println("No result available")
			}
			return pawscript.BoolStatus(true)
		},
	})
	
	fmt.Println("=== PawScript Example ===\n")
	
	// Example 1: Simple command
	fmt.Println("Example 1: Simple command")
	ps.Execute("greet")
	ps.Execute("greet 'Alice'")
	fmt.Println()
	
	// Example 2: Command sequence
	fmt.Println("Example 2: Command sequence")
	ps.Execute("greet 'Bob'; greet 'Charlie'")
	fmt.Println()
	
	// Example 3: Conditional execution
	fmt.Println("Example 3: Conditional execution")
	ps.Execute("greet 'Dave' & greet 'Eve'")
	fmt.Println()
	
	// Example 4: Result management
	fmt.Println("Example 4: Result management")
	ps.Execute("calculate 10, 5; print_result")
	fmt.Println()
	
	// Example 5: Brace expressions
	fmt.Println("Example 5: Brace expressions")
	ps.Execute("greet 'Result is: {calculate 7, 3}'")
	fmt.Println()
	
	// Example 6: Define and use macros
	fmt.Println("Example 6: Macros")
	ps.DefineMacro("welcome", "greet 'Welcome to PawScript!'")
	ps.ExecuteMacro("welcome")
	fmt.Println()
	
	// Example 7: Macros with arguments
	fmt.Println("Example 7: Macros with arguments")
	ps.DefineMacro("greet_twice", "greet $1; greet $1")
	ps.Execute("greet_twice 'Frank'")
	fmt.Println()
	
	// Example 8: Async operations
	fmt.Println("Example 8: Async operations")
	result := ps.Execute("async_operation; print_result")
	
	// Wait for async operation to complete
	if _, ok := result.(pawscript.TokenResult); ok {
		fmt.Println("Waiting for async operation...")
		time.Sleep(3 * time.Second)
	}
	fmt.Println()
	
	// Example 9: Complex flow control
	fmt.Println("Example 9: Complex flow control")
	ps.RegisterCommand("random_fail", func(ctx *pawscript.Context) pawscript.Result {
		// Simulate random failure
		success := time.Now().Unix()%2 == 0
		if success {
			fmt.Println("Operation succeeded")
		} else {
			fmt.Println("Operation failed")
		}
		return pawscript.BoolStatus(success)
	})
	
	ps.Execute("random_fail & greet 'Success!' | greet 'Fallback!'")
	fmt.Println()
	
	fmt.Println("=== Examples Complete ===")
}
