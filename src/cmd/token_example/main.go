package main

import (
	"fmt"
	"time"
        pawscript "github.com/phroun/pawscript/src"
)

func main() {
	ps := pawscript.New(&pawscript.Config{
                Debug:                false, // Use the flag value
                AllowMacros:          true,
                EnableSyntacticSugar: true,
                ShowErrorContext:     true,
                ContextLines:         2,
        })
        var scriptArgs []string
        ps.RegisterStandardLibrary(scriptArgs)
        
	// Register async command
	ps.RegisterCommand("wait_async", func(ctx *pawscript.Context) pawscript.Result {
		token := ctx.RequestToken(nil)
		fmt.Println("→ wait_async: Suspending script, starting async work...")

		// Async work in goroutine
		go func() {
			time.Sleep(5 * time.Second)
			fmt.Println("\n→ wait_async: Async work DONE! Resuming script...")

			// Create inner lists first
			row1 := pawscript.NewStoredListWithoutRefs([]interface{}{1, 2, 3})
			row2 := pawscript.NewStoredListWithoutRefs([]interface{}{4, 5, 6})
			row3 := pawscript.NewStoredListWithoutRefs([]interface{}{7, 8, 9})
			
			// Create outer list containing the inner lists
			matrix := pawscript.NewStoredListWithoutRefs([]interface{}{row1, row2, row3, row1, "I waited 5 seconds!"})

			ctx.SetResult(matrix)
			ctx.ResumeToken(token, true)
		}()

		return pawscript.TokenResult(token) // Suspend NOW
	})

	// Script to run
	script := `
echo "1. Script started"
echo "2. About to call async command"
wait_async
echo "3. Resumed!"
echo "4. Got result: {get_result}"
mem_stats
echo "5. Script complete"
`

	fmt.Println("=== Minimal Async Demo ===")

	// Execute - returns IMMEDIATELY if async
	result := ps.Execute(script)

	if _, isToken := result.(pawscript.TokenResult); isToken {
		fmt.Println("→ main: Script suspended, control returned to main()")
		fmt.Println("→ main: Entering counter loop...")

		// Main loop - continues while script is suspended
		for i := 1; i <= 7; i++ {
			fmt.Printf("→ main: Counter = %d\n", i)
			time.Sleep(1 * time.Second)
		}

		fmt.Println("\n→ main: Loop finished")
		
		// Wait a bit for script to complete after resumption
		time.Sleep(100 * time.Millisecond)
	} else {
		fmt.Println("Script completed synchronously")
	}

	fmt.Println("\n=== Demo Complete ===")
}
