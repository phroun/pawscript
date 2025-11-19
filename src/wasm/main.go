// src/wasm/main.go
//go:build js && wasm
// +build js,wasm

package main

import (
	"fmt"
	"syscall/js"
//	"time"

	pawscript "github.com/phroun/pawscript"
)

// wasmPaw wraps PawScript and async token management
type wasmPaw struct {
	ps             *pawscript.PawScript
	tokenResumeMap map[string]func(bool) bool
}

// --- JS bridge functions ---

// wasmExecute is called from JS: pawscript_execute(command: string)
func (w *wasmPaw) wasmExecute(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return false
	}
	cmd := args[0].String()

	result := w.ps.Execute(cmd)

	// Convert Go Result to JS-friendly value
	switch v := result.(type) {
	case pawscript.BoolStatus:
		return bool(v)
	case pawscript.TokenResult:
		return string(v)
	default:
		return false
	}
}

// wasmRegisterJSCommand registers a JS function as a PawScript command
func (w *wasmPaw) wasmRegisterJSCommand(this js.Value, args []js.Value) interface{} {
	if len(args) < 2 {
		return false
	}
	cmdName := args[0].String()
	jsFunc := args[1]

	w.ps.RegisterCommand(cmdName, func(ctx *pawscript.Context) pawscript.Result {
		// Convert arguments to []interface{}
		jsArgs := make([]interface{}, len(ctx.Args))
		copy(jsArgs, ctx.Args)

		// Wrap cleanup for async token support
		token := ctx.RequestToken(func(tokenID string) {
			res := jsFunc.Invoke(tokenID, jsArgs)
			status := false
			if res.Type() == js.TypeBoolean {
				status = res.Bool()
			}
			ctx.ResumeToken(tokenID, status)
		})

		// If RequestToken returned a token, return it
		if token != "" {
			return pawscript.TokenResult(token)
		}
		return pawscript.BoolStatus(true)
	})

	return true
}

// wasmResumeToken is called from JS: pawscript_resume_token(tokenID: string, status: boolean)
func (w *wasmPaw) wasmResumeToken(this js.Value, args []js.Value) interface{} {
	if len(args) < 2 {
		return false
	}
	tokenID := args[0].String()
	status := args[1].Bool()

	if resumeFunc, ok := w.tokenResumeMap[tokenID]; ok {
		delete(w.tokenResumeMap, tokenID)
		return resumeFunc(status)
	}
	return false
}

// --- Main entrypoint ---
func main() {
	cfg := &pawscript.Config{
		Debug:       false,
		AllowMacros: true,
                EnableSyntacticSugar: true,
                ShowErrorContext:     true,
                ContextLines:         2,
	}

	ps := pawscript.New(cfg)
        ps.RegisterStandardLibrary(nil)

	// Wrap PawScript for WASM bridge
	wasm := &wasmPaw{
		ps:             ps,
		tokenResumeMap: make(map[string]func(bool) bool),
	}

	// Expose JS functions
	js.Global().Set("pawscript_execute", js.FuncOf(wasm.wasmExecute))
	js.Global().Set("pawscript_register_js_command", js.FuncOf(wasm.wasmRegisterJSCommand))
	js.Global().Set("pawscript_resume_token", js.FuncOf(wasm.wasmResumeToken))

	fmt.Println("PawScript WASM ready!")

	// Keep the WASM runtime alive
	select {}
}
