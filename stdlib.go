package pawscript

import (
	"bufio"
	"fmt"
	"os"
)

// RegisterStandardLibrary registers standard library commands
func (ps *PawScript) RegisterStandardLibrary(scriptArgs []string) {
	// argc - returns number of arguments
	ps.RegisterCommand("argc", func(ctx *Context) Result {
		ctx.SetResult(len(scriptArgs))
		return BoolResult(true)
	})
	
	// argv - returns array of arguments or specific argument by index
	ps.RegisterCommand("argv", func(ctx *Context) Result {
		if len(ctx.Args) == 0 {
			ctx.SetResult(scriptArgs)
		} else {
			index, ok := ctx.Args[0].(int64)
			if !ok {
				// Try to convert from float
				if f, ok := ctx.Args[0].(float64); ok {
					index = int64(f)
				} else {
					ctx.SetResult(nil)
					return BoolResult(true)
				}
			}
			
			if index >= 0 && int(index) < len(scriptArgs) {
				ctx.SetResult(scriptArgs[index])
			} else {
				ctx.SetResult(nil)
			}
		}
		return BoolResult(true)
	})
	
	// script_error - output error messages
	ps.RegisterCommand("script_error", func(ctx *Context) Result {
		message := "Unknown error"
		if len(ctx.Args) > 0 {
			message = fmt.Sprintf("%v", ctx.Args[0])
		}
		
		errorOutput := fmt.Sprintf("[SCRIPT ERROR] %s", message)
		
		if ctx.Position != nil {
			errorOutput += fmt.Sprintf(" at line %d, column %d", ctx.Position.Line, ctx.Position.Column)
			
			if ctx.Position.OriginalText != "" {
				errorOutput += fmt.Sprintf("\n  Source: %s", ctx.Position.OriginalText)
			}
		}
		
		fmt.Fprintln(os.Stderr, errorOutput)
		return BoolResult(true)
	})
	
	// echo/write/print - output to stdout
	outputCommand := func(ctx *Context) Result {
		text := ""
		for i, arg := range ctx.Args {
			if i > 0 {
				text += " "
			}
			text += fmt.Sprintf("%v", arg)
		}
		fmt.Println(text)
		return BoolResult(true)
	}
	
	ps.RegisterCommand("echo", outputCommand)
	ps.RegisterCommand("write", outputCommand)
	ps.RegisterCommand("print", outputCommand)
	
	// read - read a line from stdin
	ps.RegisterCommand("read", func(ctx *Context) Result {
		token := ctx.RequestToken(nil)
		
		go func() {
			reader := bufio.NewReader(os.Stdin)
			line, err := reader.ReadString('\n')
			
			if err == nil {
				// Remove trailing newline
				if len(line) > 0 && line[len(line)-1] == '\n' {
					line = line[:len(line)-1]
				}
				ctx.SetResult(line)
				ctx.ResumeToken(token, true)
			} else {
				ctx.SetResult("")
				ctx.ResumeToken(token, false)
			}
		}()
		
		return TokenResult(token)
	})
	
	// true - sets success state
	ps.RegisterCommand("true", func(ctx *Context) Result {
		return BoolResult(true)
	})
	
	// false - sets error state
	ps.RegisterCommand("false", func(ctx *Context) Result {
		return BoolResult(false)
	})
	
	// get_result - gets the current result value
	ps.RegisterCommand("get_result", func(ctx *Context) Result {
		if ctx.HasResult() {
			// Set the result as this command's output so brace expressions can capture it
			result := ctx.GetResult()
			ctx.SetResult(result)
			return BoolResult(true)
		} else {
			ctx.SetResult("")
			return BoolResult(true)
		}
	})
}
