// src/wasm/main.go
//go:build js && wasm
// +build js,wasm

package main

import (
	"fmt"
	"syscall/js"

	pawscript "github.com/phroun/pawscript"
)

// wasmPaw wraps PawScript and manages the JS bridge
type wasmPaw struct {
	ps *pawscript.PawScript
}

// --- Type conversion helpers ---

// goToJS converts Go values to JS values recursively
func goToJS(value interface{}) js.Value {
	if value == nil {
		return js.Null()
	}

	switch v := value.(type) {
	case bool:
		return js.ValueOf(v)
	case int, int8, int16, int32, int64:
		return js.ValueOf(v)
	case uint, uint8, uint16, uint32, uint64:
		return js.ValueOf(v)
	case float32, float64:
		return js.ValueOf(v)
	case string:
		return js.ValueOf(v)
	case pawscript.Symbol:
		return js.ValueOf(string(v))
	case pawscript.QuotedString:
		return js.ValueOf(string(v))
	case pawscript.ParenGroup:
		return js.ValueOf(string(v))
	case pawscript.StoredString:
		return js.ValueOf(string(v))
	case pawscript.StoredBlock:
		return js.ValueOf(string(v))
	case pawscript.StoredList:
		items := v.Items()
		arr := make([]interface{}, len(items))
		for i, item := range items {
			arr[i] = goToJS(item)
		}
		return js.ValueOf(arr)
	case []interface{}:
		arr := make([]interface{}, len(v))
		for i, item := range v {
			arr[i] = goToJS(item)
		}
		return js.ValueOf(arr)
	case pawscript.BoolStatus:
		return js.ValueOf(bool(v))
	case pawscript.TokenResult:
		return js.ValueOf(string(v))
	default:
		// Fallback: convert to string
		return js.ValueOf(fmt.Sprintf("%v", v))
	}
}

// jsToGo converts JS values to Go interface{} values
func jsToGo(value js.Value) interface{} {
	switch value.Type() {
	case js.TypeBoolean:
		return value.Bool()
	case js.TypeNumber:
		return value.Float()
	case js.TypeString:
		return value.String()
	case js.TypeNull, js.TypeUndefined:
		return nil
	case js.TypeObject:
		// Check if it's an array
		if value.Get("length").Type() != js.TypeUndefined {
			length := value.Get("length").Int()
			arr := make([]interface{}, length)
			for i := 0; i < length; i++ {
				arr[i] = jsToGo(value.Index(i))
			}
			return arr
		}
		// Otherwise return as string representation
		return value.String()
	default:
		return value.String()
	}
}

// --- JS bridge functions ---

// wasmExecute executes a PawScript command string
// Signature: pawscript_execute(command: string) -> any
func (w *wasmPaw) wasmExecute(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return js.ValueOf(map[string]interface{}{
			"success": false,
			"error":   "No command provided",
		})
	}
	cmd := args[0].String()

	result := w.ps.Execute(cmd)

	switch v := result.(type) {
	case pawscript.BoolStatus:
		return js.ValueOf(map[string]interface{}{
			"type":    "status",
			"success": bool(v),
		})
	case pawscript.TokenResult:
		return js.ValueOf(map[string]interface{}{
			"type":  "token",
			"token": string(v),
		})
	case pawscript.EarlyReturn:
		return js.ValueOf(map[string]interface{}{
			"type":      "early_return",
			"success":   bool(v.Status),
			"hasResult": v.HasResult,
			"result":    goToJS(v.Result),
		})
	default:
		return js.ValueOf(map[string]interface{}{
			"type":    "status",
			"success": false,
			"error":   fmt.Sprintf("Unknown result type: %T", result),
		})
	}
}

// wasmRegisterCommand registers a JS function as a PawScript command
// Signature: pawscript_register_command(name: string, handler: (ctx: Context) => boolean | string)
func (w *wasmPaw) wasmRegisterCommand(this js.Value, args []js.Value) interface{} {
	if len(args) < 2 {
		return false
	}
	cmdName := args[0].String()
	jsFunc := args[1]

	w.ps.RegisterCommand(cmdName, func(ctx *pawscript.Context) pawscript.Result {
		// Build context object for JS
		jsArgs := make([]interface{}, len(ctx.Args))
		for i, arg := range ctx.Args {
			jsArgs[i] = goToJS(arg)
		}

		jsCtx := map[string]interface{}{
			"args": jsArgs,
			// setResult callback
			"setResult": js.FuncOf(func(this js.Value, args []js.Value) interface{} {
				if len(args) > 0 {
					ctx.SetResult(jsToGo(args[0]))
				}
				return nil
			}),
			// getResult callback
			"getResult": js.FuncOf(func(this js.Value, args []js.Value) interface{} {
				return goToJS(ctx.GetResult())
			}),
			// hasResult callback
			"hasResult": js.FuncOf(func(this js.Value, args []js.Value) interface{} {
				return ctx.HasResult()
			}),
			// requestToken callback - returns token ID
			"requestToken": js.FuncOf(func(this js.Value, args []js.Value) interface{} {
				// JS will handle the async work and call resumeToken when done
				token := ctx.RequestToken(nil)
				return token
			}),
		}

		// Call the JS function with the context
		jsResult := jsFunc.Invoke(js.ValueOf(jsCtx))

		// Handle return value
		switch jsResult.Type() {
		case js.TypeBoolean:
			return pawscript.BoolStatus(jsResult.Bool())
		case js.TypeString:
			// Check if it's a token
			tokenStr := jsResult.String()
			if tokenStr != "" && len(tokenStr) > 0 {
				return pawscript.TokenResult(tokenStr)
			}
			return pawscript.BoolStatus(true)
		default:
			return pawscript.BoolStatus(true)
		}
	})

	return true
}

// wasmResumeToken resumes execution after async operation
// Signature: pawscript_resume_token(tokenID: string, success: boolean, result?: any)
func (w *wasmPaw) wasmResumeToken(this js.Value, args []js.Value) interface{} {
	if len(args) < 2 {
		return false
	}
	tokenID := args[0].String()
	success := args[1].Bool()

	// Optional result value
	if len(args) > 2 && args[2].Type() != js.TypeUndefined {
		// Get the execution state from token and set result
		// Note: This is a simplified approach - ideally we'd expose SetResult on the token
		// For now, commands that need to set results should do so before requesting the token
	}

	return w.ps.ResumeToken(tokenID, success)
}

// wasmGetVariable gets a variable value from the current execution state
// Signature: pawscript_get_variable(name: string) -> any
func (w *wasmPaw) wasmGetVariable(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return js.Null()
	}
	// Note: This accesses the global execution state's variables
	// In a real implementation, you'd need to track per-execution state
	return js.ValueOf("Variable access requires execution context")
}

// wasmSetVariable sets a variable value in the current execution state
// Signature: pawscript_set_variable(name: string, value: any) -> boolean
func (w *wasmPaw) wasmSetVariable(this js.Value, args []js.Value) interface{} {
	if len(args) < 2 {
		return false
	}
	// Note: Same limitation as getVariable
	return false
}

// wasmGetResult gets the current result value
// Signature: pawscript_get_result() -> any
func (w *wasmPaw) wasmGetResult(this js.Value, args []js.Value) interface{} {
	// Note: Would need to track current execution state
	return js.Null()
}

// wasmGetTokenStatus returns information about active tokens
// Signature: pawscript_get_token_status() -> object
func (w *wasmPaw) wasmGetTokenStatus(this js.Value, args []js.Value) interface{} {
	status := w.ps.GetTokenStatus()
	return goToJS(status)
}

// --- Main entrypoint ---
func main() {
	cfg := &pawscript.Config{
		Debug:                false,
		AllowMacros:          true,
		EnableSyntacticSugar: true,
		ShowErrorContext:     true,
		ContextLines:         2,
	}

	ps := pawscript.New(cfg)
	ps.RegisterStandardLibrary([]string{})

	// Wrap PawScript for WASM bridge
	wasm := &wasmPaw{
		ps: ps,
	}

	// Expose JS functions
	js.Global().Set("pawscript_execute", js.FuncOf(wasm.wasmExecute))
	js.Global().Set("pawscript_register_command", js.FuncOf(wasm.wasmRegisterCommand))
	js.Global().Set("pawscript_resume_token", js.FuncOf(wasm.wasmResumeToken))
	js.Global().Set("pawscript_get_variable", js.FuncOf(wasm.wasmGetVariable))
	js.Global().Set("pawscript_set_variable", js.FuncOf(wasm.wasmSetVariable))
	js.Global().Set("pawscript_get_result", js.FuncOf(wasm.wasmGetResult))
	js.Global().Set("pawscript_get_token_status", js.FuncOf(wasm.wasmGetTokenStatus))

	fmt.Println("PawScript WASM ready!")

	// Keep the WASM runtime alive
	select {}
}
