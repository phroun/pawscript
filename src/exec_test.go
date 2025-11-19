package pawscript

import (
	"strings"
	"testing"
)

func TestExecCommand(t *testing.T) {
	ps := New(&Config{
		Debug: false,
	})
	
	// Register standard library
	ps.RegisterStandardLibrary([]string{})
	
	t.Run("Successful command with stdout", func(t *testing.T) {
		// Execute echo command (should work on Unix-like systems)
		result := ps.Execute("exec echo, 'Hello World'")
		
		if boolStatus, ok := result.(BoolStatus); !ok || !bool(boolStatus) {
			t.Error("Expected successful execution")
		}
		
		// Create a state to check the result
		//state := NewExecutionState()
		ps.Execute("exec echo, 'Test Output'")
		
		// Now get the result with get_result
		var capturedResult string
		ps.RegisterCommand("capture", func(ctx *Context) Result {
			if ctx.HasResult() {
				capturedResult = strings.TrimSpace(ctx.GetResult().(string))
			}
			return BoolStatus(true)
		})
		
		ps.Execute("exec echo, 'Hello'; capture")
		
		if capturedResult != "Hello" {
			t.Errorf("Expected 'Hello', got '%s'", capturedResult)
		}
	})
	
	t.Run("Command with arguments", func(t *testing.T) {
		var capturedResult string
		ps.RegisterCommand("capture", func(ctx *Context) Result {
			if ctx.HasResult() {
				capturedResult = strings.TrimSpace(ctx.GetResult().(string))
			}
			return BoolStatus(true)
		})
		
		// Test command with multiple arguments
		ps.Execute("exec echo, 'arg1', 'arg2', 'arg3'; capture")
		
		// Should have output containing all arguments
		if !strings.Contains(capturedResult, "arg1") {
			t.Errorf("Expected output to contain 'arg1', got '%s'", capturedResult)
		}
	})
	
	t.Run("Non-existent command", func(t *testing.T) {
		result := ps.Execute("exec this_command_does_not_exist_12345")
		
		if boolStatus, ok := result.(BoolStatus); !ok || bool(boolStatus) {
			t.Error("Expected failure for non-existent command")
		}
	})
	
	t.Run("Command with stderr should fail", func(t *testing.T) {
		// Use a command that outputs to stderr
		// sh -c allows us to redirect output to stderr
		result := ps.Execute("exec sh, '-c', 'echo error >&2'")
		
		if boolStatus, ok := result.(BoolStatus); !ok || bool(boolStatus) {
			t.Error("Expected failure when stderr has content")
		}
	})
	
	t.Run("Exec in brace expression", func(t *testing.T) {
		var output string
		ps.RegisterCommand("show", func(ctx *Context) Result {
			if len(ctx.Args) > 0 {
				output = strings.TrimSpace(ctx.Args[0].(string))
			}
			return BoolStatus(true)
		})
		
		// Execute command in brace and use result
		ps.Execute("show {exec echo, 'from brace'}")
		
		if output != "from brace" {
			t.Errorf("Expected 'from brace', got '%s'", output)
		}
	})
	
	t.Run("No command specified", func(t *testing.T) {
		result := ps.Execute("exec")
		
		if boolStatus, ok := result.(BoolStatus); !ok || bool(boolStatus) {
			t.Error("Expected failure when no command specified")
		}
	})
}

func TestExecWithMultipleCommands(t *testing.T) {
	ps := New(&Config{
		Debug: false,
	})
	
	ps.RegisterStandardLibrary([]string{})
	
	t.Run("Chain exec commands", func(t *testing.T) {
		// Execute multiple commands in sequence
		result := ps.Execute("exec echo, 'first' & exec echo, 'second'")
		
		if boolStatus, ok := result.(BoolStatus); !ok || !bool(boolStatus) {
			t.Error("Expected successful chained execution")
		}
	})
	
	t.Run("Exec with conditional", func(t *testing.T) {
		var executed bool
		ps.RegisterCommand("mark", func(ctx *Context) Result {
			executed = true
			return BoolStatus(true)
		})
		
		// If exec succeeds, mark should execute
		ps.Execute("exec echo, 'test' & mark")
		
		if !executed {
			t.Error("Expected mark to execute after successful exec")
		}
	})
	
	t.Run("Exec with fallback", func(t *testing.T) {
		var fallbackExecuted bool
		ps.RegisterCommand("fallback", func(ctx *Context) Result {
			fallbackExecuted = true
			return BoolStatus(true)
		})
		
		// Non-existent command should trigger fallback
		ps.Execute("exec this_does_not_exist | fallback")
		
		if !fallbackExecuted {
			t.Error("Expected fallback to execute after failed exec")
		}
	})
}

func TestExecInMacros(t *testing.T) {
	ps := New(&Config{
		Debug:       false,
		AllowMacros: true,
	})
	
	ps.RegisterStandardLibrary([]string{})
	
	t.Run("Exec in macro definition", func(t *testing.T) {
		// Define macro that uses exec
		ps.DefineMacro("get_date", "exec date, '+%Y-%m-%d'")
		
		result := ps.ExecuteMacro("get_date")
		
		if boolStatus, ok := result.(BoolStatus); !ok || !bool(boolStatus) {
			t.Error("Expected successful macro execution")
		}
	})
	
	t.Run("Macro with exec and argument", func(t *testing.T) {
		ps.DefineMacro("echo_arg", "exec echo, $1")
		
		var output string
		ps.RegisterCommand("capture", func(ctx *Context) Result {
			if ctx.HasResult() {
				output = strings.TrimSpace(ctx.GetResult().(string))
			}
			return BoolStatus(true)
		})
		
		ps.Execute("echo_arg 'macro test'; capture")
		
		if output != "macro test" {
			t.Errorf("Expected 'macro test', got '%s'", output)
		}
	})
}
