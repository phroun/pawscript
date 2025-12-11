package pawscript

import (
	"fmt"
	"testing"
	"time"
)

func TestBasicExecution(t *testing.T) {
	ps := New(nil)

	called := false
	ps.RegisterCommand("test", func(ctx *Context) Result {
		called = true
		return BoolStatus(true)
	})

	result := ps.Execute("test")

	if !called {
		t.Error("Command was not called")
	}

	if boolState, ok := result.(BoolStatus); !ok || !bool(boolState) {
		t.Error("Expected true result")
	}
}

func TestCommandWithArguments(t *testing.T) {
	ps := New(nil)

	var receivedArgs []interface{}
	ps.RegisterCommand("test_args", func(ctx *Context) Result {
		receivedArgs = ctx.Args
		return BoolStatus(true)
	})

	ps.Execute("test_args 'hello', 42, true")

	if len(receivedArgs) != 3 {
		t.Errorf("Expected 3 arguments, got %d", len(receivedArgs))
	}

	if fmt.Sprintf("%v", receivedArgs[0]) != "hello" {
		t.Errorf("Expected 'hello', got %v", receivedArgs[0])
	}

	if receivedArgs[1] != int64(42) {
		t.Errorf("Expected 42, got %v", receivedArgs[1])
	}

	if receivedArgs[2] != true {
		t.Errorf("Expected true, got %v", receivedArgs[2])
	}
}

func TestCommandSequence(t *testing.T) {
	ps := New(nil)

	callCount := 0
	ps.RegisterCommand("test", func(ctx *Context) Result {
		callCount++
		return BoolStatus(true)
	})

	result := ps.Execute("test; test; test")

	if callCount != 3 {
		t.Errorf("Expected 3 calls, got %d", callCount)
	}

	if boolState, ok := result.(BoolStatus); !ok || !bool(boolState) {
		t.Error("Expected true result")
	}
}

func TestConditionalExecution(t *testing.T) {
	ps := New(nil)

	successCount := 0
	failCount := 0

	ps.RegisterCommand("success", func(ctx *Context) Result {
		successCount++
		return BoolStatus(true)
	})

	ps.RegisterCommand("fail", func(ctx *Context) Result {
		failCount++
		return BoolStatus(false)
	})

	// Test AND operator - second should execute
	ps.Execute("success & success")
	if successCount != 2 {
		t.Errorf("Expected 2 success calls, got %d", successCount)
	}

	// Test AND operator - second should NOT execute
	successCount = 0
	ps.Execute("fail & success")
	if failCount != 1 {
		t.Errorf("Expected 1 fail call, got %d", failCount)
	}
	if successCount != 0 {
		t.Errorf("Expected 0 success calls after fail, got %d", successCount)
	}

	// Test OR operator - second should NOT execute
	successCount = 0
	failCount = 0
	ps.Execute("success | fail")
	if successCount != 1 {
		t.Errorf("Expected 1 success call, got %d", successCount)
	}
	if failCount != 0 {
		t.Errorf("Expected 0 fail calls after success, got %d", failCount)
	}

	// Test OR operator - second should execute
	successCount = 0
	failCount = 0
	ps.Execute("fail | success")
	if failCount != 1 {
		t.Errorf("Expected 1 fail call, got %d", failCount)
	}
	if successCount != 1 {
		t.Errorf("Expected 1 success call after fail, got %d", successCount)
	}
}

func TestResultManagement(t *testing.T) {
	ps := New(nil)

	ps.RegisterCommand("set_value", func(ctx *Context) Result {
		ctx.SetResult(ctx.Args[0])
		return BoolStatus(true)
	})

	var capturedResult interface{}
	ps.RegisterCommand("get_value", func(ctx *Context) Result {
		if ctx.HasResult() {
			capturedResult = ctx.GetResult()
		}
		return BoolStatus(true)
	})

	ps.Execute("set_value 'test'; get_value")

	if fmt.Sprintf("%v", capturedResult) != "test" {
		t.Errorf("Expected 'test', got %v", capturedResult)
	}
}

func TestMacros(t *testing.T) {
	ps := New(nil)

	callCount := 0
	ps.RegisterCommand("test", func(ctx *Context) Result {
		callCount++
		return BoolStatus(true)
	})

	// Define macro
	success := ps.DefineMacro("test_macro", "test; test")
	if !success {
		t.Error("Failed to define macro")
	}

	// Execute macro
	result := ps.ExecuteMacro("test_macro")

	if callCount != 2 {
		t.Errorf("Expected 2 calls, got %d", callCount)
	}

	if boolState, ok := result.(BoolStatus); !ok || !bool(boolState) {
		t.Error("Expected true result")
	}
}

func TestMacroWithArguments(t *testing.T) {
	ps := New(nil)

	var receivedArg string
	ps.RegisterCommand("echo", func(ctx *Context) Result {
		if len(ctx.Args) > 0 {
			receivedArg = fmt.Sprintf("%v", ctx.Args[0])
		}
		return BoolStatus(true)
	})

	// Define macro with argument substitution
	ps.DefineMacro("greet", "echo 'Hello $1!'")

	// Execute via command
	ps.Execute("greet 'World'")

	if receivedArg != "Hello World!" {
		t.Errorf("Expected 'Hello World!', got '%s'", receivedArg)
	}
}

func TestAsyncOperations(t *testing.T) {
	t.Run("Execute blocks until completion", func(t *testing.T) {
		ps := New(nil)

		completed := false
		ps.RegisterCommand("async_test", func(ctx *Context) Result {
			token := ctx.RequestToken(nil)

			go func() {
				time.Sleep(10 * time.Millisecond)
				completed = true
				ctx.ResumeToken(token, true)
			}()

			return TokenResult(token)
		})

		// Execute should block until async operation completes
		result := ps.Execute("async_test")

		// After Execute returns, operation should be completed
		if !completed {
			t.Error("Execute did not wait for async operation to complete")
		}

		// Result should be BoolStatus (converted from token completion)
		if _, ok := result.(BoolStatus); !ok {
			t.Errorf("Expected BoolStatus after blocking Execute, got %T", result)
		}
	})

	t.Run("ExecuteAsync returns token immediately", func(t *testing.T) {
		ps := New(nil)

		completed := false
		ps.RegisterCommand("async_test", func(ctx *Context) Result {
			token := ctx.RequestToken(nil)

			go func() {
				time.Sleep(10 * time.Millisecond)
				completed = true
				ctx.ResumeToken(token, true)
			}()

			return TokenResult(token)
		})

		result := ps.ExecuteAsync("async_test")

		// Should return a token immediately
		if _, ok := result.(TokenResult); !ok {
			t.Errorf("Expected TokenResult from ExecuteAsync, got %T", result)
		}

		// Operation should not be completed yet
		if completed {
			t.Error("ExecuteAsync should return before async operation completes")
		}

		// Wait for async operation
		time.Sleep(50 * time.Millisecond)

		if !completed {
			t.Error("Async operation did not complete")
		}
	})
}

func TestBraceExpressions(t *testing.T) {
	ps := New(nil)

	ps.RegisterCommand("get_value", func(ctx *Context) Result {
		ctx.SetResult("computed")
		return BoolStatus(true)
	})

	var receivedArg string
	ps.RegisterCommand("echo", func(ctx *Context) Result {
		if len(ctx.Args) > 0 {
			receivedArg = fmt.Sprintf("%v", ctx.Args[0])
		}
		return BoolStatus(true)
	})

	ps.Execute("echo 'result: {get_value}'")

	if receivedArg != "result: computed" {
		t.Errorf("Expected 'result: computed', got '%s'", receivedArg)
	}
}

func TestMacroList(t *testing.T) {
	ps := New(nil)

	ps.DefineMacro("macro1", "test")
	ps.DefineMacro("macro2", "test")

	macros := ps.ListMacros()

	if len(macros) != 2 {
		t.Errorf("Expected 2 macros, got %d", len(macros))
	}

	// Check both macros are in the list
	hasMacro1 := false
	hasMacro2 := false
	for _, name := range macros {
		if name == "macro1" {
			hasMacro1 = true
		}
		if name == "macro2" {
			hasMacro2 = true
		}
	}

	if !hasMacro1 || !hasMacro2 {
		t.Error("Not all macros found in list")
	}
}

func TestMacroDelete(t *testing.T) {
	ps := New(nil)

	ps.DefineMacro("temp_macro", "test")

	if !ps.HasMacro("temp_macro") {
		t.Error("Macro was not created")
	}

	success := ps.DeleteMacro("temp_macro")
	if !success {
		t.Error("Failed to delete macro")
	}

	if ps.HasMacro("temp_macro") {
		t.Error("Macro still exists after deletion")
	}
}

func TestMacroClear(t *testing.T) {
	ps := New(nil)

	ps.DefineMacro("macro1", "test")
	ps.DefineMacro("macro2", "test")

	count := ps.ClearMacros()

	if count != 2 {
		t.Errorf("Expected to clear 2 macros, got %d", count)
	}

	macros := ps.ListMacros()
	if len(macros) != 0 {
		t.Errorf("Expected 0 macros after clear, got %d", len(macros))
	}
}

func TestUnknownCommand(t *testing.T) {
	ps := New(&Config{
		Debug: false, // Suppress error output during test
	})

	result := ps.Execute("unknown_command")

	if boolState, ok := result.(BoolStatus); !ok || bool(boolState) {
		t.Error("Expected false result for unknown command")
	}
}

func TestComments(t *testing.T) {
	ps := New(nil)

	callCount := 0
	ps.RegisterCommand("test", func(ctx *Context) Result {
		callCount++
		return BoolStatus(true)
	})

	// Line comment should be ignored
	ps.Execute("test # this is a comment")
	if callCount != 1 {
		t.Error("Line comment affected execution")
	}

	// Block comment should be ignored
	callCount = 0
	ps.Execute("test #( block comment )# test")
	if callCount != 1 {
		t.Errorf("Block comment affected execution %d", callCount)
	}
}
