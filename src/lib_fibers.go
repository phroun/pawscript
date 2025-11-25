package pawscript

import (
	"fmt"
)

// RegisterFibersLib registers fiber-related commands
// Module: fibers
func (ps *PawScript) RegisterFibersLib() {

	// fiber_spawn - spawn a new fiber to execute a macro
	ps.RegisterCommandInModule("fibers", "fiber_spawn", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: fiber_spawn <macro>, [args...]")
			return BoolStatus(false)
		}

		firstArg := ctx.Args[0]
		fiberArgs := ctx.Args[1:]
		namedArgs := ctx.NamedArgs

		var macro *StoredMacro

		// Check if first argument is a resolved StoredMacro object
		if m, ok := firstArg.(StoredMacro); ok {
			macro = &m
		} else if sym, ok := firstArg.(Symbol); ok {
			symStr := string(sym)
			// First check if it's an object marker
			markerType, objectID := parseObjectMarker(symStr)
			if markerType == "macro" && objectID >= 0 {
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.Error("Macro object %d not found", objectID)
					return BoolStatus(false)
				}
				if m, ok := obj.(StoredMacro); ok {
					macro = &m
				}
			} else {
				// Try to look up as a named macro
				if m, found := ps.macroSystem.GetStoredMacro(symStr); found {
					macro = m
				}
			}
		} else if str, ok := firstArg.(string); ok {
			// Try to look up as a named macro (string form)
			if m, found := ps.macroSystem.GetStoredMacro(str); found {
				macro = m
			}
		}

		if macro == nil {
			ps.logger.Error("First argument must be a macro or macro name")
			return BoolStatus(false)
		}

		// Spawn the fiber - use macro's lexical environment if available, otherwise caller's
		parentModuleEnv := macro.ModuleEnv
		if parentModuleEnv == nil {
			parentModuleEnv = ctx.state.moduleEnv
		}
		handle := ctx.executor.SpawnFiber(macro, ps.macroSystem, fiberArgs, namedArgs, parentModuleEnv)

		objectID := ctx.executor.storeObject(handle, "fiber")
		fiberMarker := fmt.Sprintf("\x00FIBER:%d\x00", objectID)
		ctx.state.SetResult(Symbol(fiberMarker))

		ps.logger.Debug("Spawned fiber %d (object %d)", handle.ID, objectID)
		return BoolStatus(true)
	})

	// fiber_wait - wait for a fiber to complete and get its result
	ps.RegisterCommandInModule("fibers", "fiber_wait", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: fiber_wait <fiber_handle>")
			return BoolStatus(false)
		}

		var handle *FiberHandle
		if h, ok := ctx.Args[0].(*FiberHandle); ok {
			handle = h
		} else if sym, ok := ctx.Args[0].(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "fiber" && objectID >= 0 {
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.Error("Fiber object %d not found", objectID)
					return BoolStatus(false)
				}
				if h, ok := obj.(*FiberHandle); ok {
					handle = h
				}
			}
		}

		if handle == nil {
			ps.logger.Error("First argument must be a fiber handle")
			return BoolStatus(false)
		}

		result, err := ctx.executor.WaitForFiber(handle)
		if err != nil {
			ps.logger.Error("Failed to wait for fiber: %v", err)
			return BoolStatus(false)
		}

		if result != nil {
			ctx.state.SetResult(result)
		}

		return BoolStatus(true)
	})

	// fiber_count - get the current number of active fibers
	ps.RegisterCommandInModule("fibers", "fiber_count", func(ctx *Context) Result {
		count := ctx.executor.GetFiberCount()
		ctx.state.SetResult(count)
		return BoolStatus(true)
	})

	// fiber_id - get the current fiber's ID
	ps.RegisterCommandInModule("fibers", "fiber_id", func(ctx *Context) Result {
		fiberID := ctx.state.fiberID
		ctx.state.SetResult(fiberID)
		return BoolStatus(true)
	})

	// fiber_wait_all - wait for all child fibers to complete
	ps.RegisterCommandInModule("fibers", "fiber_wait_all", func(ctx *Context) Result {
		ctx.executor.WaitForAllFibers()
		return BoolStatus(true)
	})
}
