package pawscript

import (
	"fmt"
	"sync"
)

// RegisterFibersLib registers fiber-related commands
// Module: fibers
func (ps *PawScript) RegisterFibersLib() {

	// fiber - spawn a new fiber to execute a macro
	ps.RegisterCommandInModule("fibers", "fiber", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.ErrorCat(CatCommand, "Usage: fiber <macro>, [args...]")
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
					ps.logger.ErrorCat(CatArgument, "Macro object %d not found", objectID)
					return BoolStatus(false)
				}
				if m, ok := obj.(StoredMacro); ok {
					macro = &m
				}
			} else {
				// Look up macro in module environment (same as call command)
				ctx.state.moduleEnv.mu.RLock()
				if m, exists := ctx.state.moduleEnv.MacrosModule[symStr]; exists && m != nil {
					macro = m
				}
				ctx.state.moduleEnv.mu.RUnlock()
			}
		} else if str, ok := firstArg.(string); ok {
			// First check if it's an object marker (from $1 substitution, etc.)
			markerType, objectID := parseObjectMarker(str)
			if markerType == "macro" && objectID >= 0 {
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.ErrorCat(CatArgument, "Macro object %d not found", objectID)
					return BoolStatus(false)
				}
				if m, ok := obj.(StoredMacro); ok {
					macro = &m
				}
			} else {
				// Look up macro in module environment (string form)
				ctx.state.moduleEnv.mu.RLock()
				if m, exists := ctx.state.moduleEnv.MacrosModule[str]; exists && m != nil {
					macro = m
				}
				ctx.state.moduleEnv.mu.RUnlock()
			}
		}

		if macro == nil {
			ps.logger.ErrorCat(CatArgument, "First argument must be a macro or macro name")
			return BoolStatus(false)
		}

		// Spawn the fiber - use macro's lexical environment if available, otherwise caller's
		parentModuleEnv := macro.ModuleEnv
		if parentModuleEnv == nil {
			parentModuleEnv = ctx.state.moduleEnv
		}
		handle := ctx.executor.SpawnFiber(macro, fiberArgs, namedArgs, parentModuleEnv)

		objectID := ctx.executor.storeObject(handle, "fiber")
		fiberMarker := fmt.Sprintf("\x00FIBER:%d\x00", objectID)
		ctx.state.SetResult(Symbol(fiberMarker))

		ps.logger.DebugCat(CatAsync, "Spawned fiber %d (object %d)", handle.ID, objectID)
		return BoolStatus(true)
	})

	// fiber_wait - wait for a fiber to complete and get its result
	ps.RegisterCommandInModule("fibers", "fiber_wait", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.ErrorCat(CatCommand, "Usage: fiber_wait <fiber_handle>")
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
					ps.logger.ErrorCat(CatArgument, "Fiber object %d not found", objectID)
					return BoolStatus(false)
				}
				if h, ok := obj.(*FiberHandle); ok {
					handle = h
				}
			}
		} else if str, ok := ctx.Args[0].(string); ok {
			// Handle string type markers (from $1 substitution, etc.)
			markerType, objectID := parseObjectMarker(str)
			if markerType == "fiber" && objectID >= 0 {
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.ErrorCat(CatArgument, "Fiber object %d not found", objectID)
					return BoolStatus(false)
				}
				if h, ok := obj.(*FiberHandle); ok {
					handle = h
				}
			}
		}

		if handle == nil {
			ps.logger.ErrorCat(CatArgument, "First argument must be a fiber handle")
			return BoolStatus(false)
		}

		result, err := ctx.executor.WaitForFiber(handle)
		if err != nil {
			ps.logger.ErrorCat(CatAsync, "Failed to wait for fiber: %v", err)
			return BoolStatus(false)
		}

		// Merge bubbles from fiber to caller's state
		handle.mu.RLock()
		if len(handle.FinalBubbleMap) > 0 {
			ctx.state.mu.Lock()
			if ctx.state.bubbleMap == nil {
				ctx.state.bubbleMap = make(map[string][]*BubbleEntry)
			}
			for flavor, entries := range handle.FinalBubbleMap {
				ctx.state.bubbleMap[flavor] = append(ctx.state.bubbleMap[flavor], entries...)
				// Transfer ownership: caller claims refs, release the extra refs we held
				for _, entry := range entries {
					if sym, ok := entry.Content.(Symbol); ok {
						_, objectID := parseObjectMarker(string(sym))
						if objectID >= 0 {
							// Caller claims the reference
							ctx.state.ownedObjects[objectID]++
							// Release the extra ref we added in fiber completion
							ctx.executor.decrementObjectRefCount(objectID)
						}
					}
				}
			}
			ctx.state.mu.Unlock()
		}
		handle.mu.RUnlock()

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
		// Collect all active fibers (except main fiber)
		ctx.executor.mu.RLock()
		fibers := make([]*FiberHandle, 0, len(ctx.executor.activeFibers))
		for _, fiber := range ctx.executor.activeFibers {
			if fiber.ID != 0 {
				fibers = append(fibers, fiber)
			}
		}
		ctx.executor.mu.RUnlock()

		// Wait for all fibers to complete
		var wg sync.WaitGroup
		for _, fiber := range fibers {
			wg.Add(1)
			go func(f *FiberHandle) {
				defer wg.Done()
				<-f.CompleteChan
			}(fiber)
		}
		wg.Wait()

		// Merge bubbles from all fibers to caller's state
		for _, fiber := range fibers {
			fiber.mu.RLock()
			if len(fiber.FinalBubbleMap) > 0 {
				ctx.state.mu.Lock()
				if ctx.state.bubbleMap == nil {
					ctx.state.bubbleMap = make(map[string][]*BubbleEntry)
				}
				for flavor, entries := range fiber.FinalBubbleMap {
					ctx.state.bubbleMap[flavor] = append(ctx.state.bubbleMap[flavor], entries...)
					// Transfer ownership: caller claims refs, release the extra refs we held
					for _, entry := range entries {
						if sym, ok := entry.Content.(Symbol); ok {
							_, objectID := parseObjectMarker(string(sym))
							if objectID >= 0 {
								// Caller claims the reference
								ctx.state.ownedObjects[objectID]++
								// Release the extra ref we added in fiber completion
								ctx.executor.decrementObjectRefCount(objectID)
							}
						}
					}
				}
				ctx.state.mu.Unlock()
			}
			fiber.mu.RUnlock()
		}

		return BoolStatus(true)
	})

	// fiber_bubble - early bubble transfer between fiber and parent
	// fiber_bubble up - from within fiber, moves bubbleMap to bubbleUpMap (available to parent)
	// fiber_bubble <handle> - from parent, retrieves bubbleUpMap from fiber into caller's bubbleMap
	ps.RegisterCommandInModule("fibers", "fiber_bubble", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.ErrorCat(CatCommand, "Usage: fiber_bubble up | fiber_bubble <fiber_handle>")
			return BoolStatus(false)
		}

		// Check for "up" mode (called from within a fiber)
		if sym, ok := ctx.Args[0].(Symbol); ok && string(sym) == "up" {
			// Get current fiber ID
			fiberID := ctx.state.fiberID
			if fiberID == 0 {
				ps.logger.ErrorCat(CatCommand, "fiber_bubble up: not running in a fiber")
				return BoolStatus(false)
			}

			// Find our fiber handle
			ctx.executor.mu.RLock()
			handle, exists := ctx.executor.activeFibers[fiberID]
			ctx.executor.mu.RUnlock()

			if !exists {
				ps.logger.ErrorCat(CatCommand, "fiber_bubble up: fiber handle not found")
				return BoolStatus(false)
			}

			// Move bubbleMap entries to bubbleUpMap
			ctx.state.mu.Lock()
			if len(ctx.state.bubbleMap) > 0 {
				handle.mu.Lock()
				if handle.BubbleUpMap == nil {
					handle.BubbleUpMap = make(map[string][]*BubbleEntry)
				}
				for flavor, entries := range ctx.state.bubbleMap {
					// Claim refs for each entry's content (will be released when retrieved)
					for _, entry := range entries {
						if sym, ok := entry.Content.(Symbol); ok {
							_, objectID := parseObjectMarker(string(sym))
							if objectID >= 0 {
								ctx.executor.incrementObjectRefCount(objectID)
							}
						}
					}
					handle.BubbleUpMap[flavor] = append(handle.BubbleUpMap[flavor], entries...)
				}
				handle.mu.Unlock()
				// Clear the fiber's bubbleMap
				ctx.state.bubbleMap = make(map[string][]*BubbleEntry)
			}
			ctx.state.mu.Unlock()

			return BoolStatus(true)
		}

		// Handle mode: retrieve bubbleUpMap from a fiber handle
		var handle *FiberHandle
		if h, ok := ctx.Args[0].(*FiberHandle); ok {
			handle = h
		} else if sym, ok := ctx.Args[0].(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "fiber" && objectID >= 0 {
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.ErrorCat(CatArgument, "Fiber object %d not found", objectID)
					return BoolStatus(false)
				}
				if h, ok := obj.(*FiberHandle); ok {
					handle = h
				}
			}
		} else if str, ok := ctx.Args[0].(string); ok {
			markerType, objectID := parseObjectMarker(str)
			if markerType == "fiber" && objectID >= 0 {
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.ErrorCat(CatArgument, "Fiber object %d not found", objectID)
					return BoolStatus(false)
				}
				if h, ok := obj.(*FiberHandle); ok {
					handle = h
				}
			}
		}

		if handle == nil {
			ps.logger.ErrorCat(CatArgument, "fiber_bubble: argument must be 'up' or a fiber handle")
			return BoolStatus(false)
		}

		// Retrieve bubbleUpMap from fiber and merge into caller's bubbleMap
		handle.mu.Lock()
		if len(handle.BubbleUpMap) > 0 {
			ctx.state.mu.Lock()
			if ctx.state.bubbleMap == nil {
				ctx.state.bubbleMap = make(map[string][]*BubbleEntry)
			}
			for flavor, entries := range handle.BubbleUpMap {
				ctx.state.bubbleMap[flavor] = append(ctx.state.bubbleMap[flavor], entries...)
				// Transfer ownership: caller claims refs, release the staging refs
				for _, entry := range entries {
					if sym, ok := entry.Content.(Symbol); ok {
						_, objectID := parseObjectMarker(string(sym))
						if objectID >= 0 {
							// Caller claims the reference
							ctx.state.ownedObjects[objectID]++
							// Release the staging ref we added in "up" mode
							ctx.executor.decrementObjectRefCount(objectID)
						}
					}
				}
			}
			ctx.state.mu.Unlock()
			// Clear the fiber's bubbleUpMap after retrieval
			handle.BubbleUpMap = make(map[string][]*BubbleEntry)
		}
		handle.mu.Unlock()

		return BoolStatus(true)
	})
}
