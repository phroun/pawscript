package pawscript

import (
	"fmt"
	"sync"
)

// registerFiber adds a fiber to the active fibers map
func (e *Executor) registerFiber(fiber *FiberHandle) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.activeFibers[fiber.ID] = fiber
	e.logger.Debug("Registered fiber %d", fiber.ID)
}

// unregisterFiber removes a fiber from the active fibers map
func (e *Executor) unregisterFiber(fiberID int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.activeFibers, fiberID)
	e.logger.Debug("Unregistered fiber %d", fiberID)
}

// getFiber retrieves a fiber by ID
func (e *Executor) getFiber(fiberID int) *FiberHandle {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.activeFibers[fiberID]
}

// GetFiberCount returns the number of currently running fibers
func (e *Executor) GetFiberCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.activeFibers)
}

// GetSuspendedFibers returns a map of fiberID -> tokenID for all suspended fibers
func (e *Executor) GetSuspendedFibers() map[int]string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	suspended := make(map[int]string)
	for fiberID, fiber := range e.activeFibers {
		fiber.mu.RLock()
		if fiber.SuspendedOn != "" {
			suspended[fiberID] = fiber.SuspendedOn
		}
		fiber.mu.RUnlock()
	}
	return suspended
}

// SpawnFiber spawns a new fiber to execute a macro
// parentModuleEnv allows the fiber to inherit commands from the parent context
func (e *Executor) SpawnFiber(macro *StoredMacro, macroSystem *MacroSystem, args []interface{}, namedArgs map[string]interface{}, parentModuleEnv *ModuleEnvironment) *FiberHandle {
	e.mu.Lock()
	fiberID := e.nextFiberID
	e.nextFiberID++
	e.mu.Unlock()

	// Create fiber state - inherit module environment if provided
	fiberState := NewExecutionState()
	if parentModuleEnv != nil {
		// Replace the default module environment with one that inherits from parent
		fiberState.moduleEnv = NewChildModuleEnvironment(parentModuleEnv)
	}

	handle := &FiberHandle{
		ID:           fiberID,
		State:        fiberState,
		ResumeChan:   make(chan ResumeData, 1), // Buffered to avoid blocking
		CompleteChan: make(chan struct{}),
		Completed:    false,
	}

	handle.State.fiberID = fiberID
	handle.State.executor = e

	e.registerFiber(handle)

	go func() {
		defer func() {
			handle.mu.Lock()
			handle.Completed = true
			handle.mu.Unlock()
			close(handle.CompleteChan)
			e.unregisterFiber(fiberID)
			// Release all references owned by this fiber
			handle.State.ReleaseAllReferences()
		}()

		e.logger.Debug("Fiber %d starting execution", fiberID)

		// Execute macro (token system handles all async operations and sequencing)
		result := macroSystem.ExecuteStoredMacro(
			macro,
			func(commands string, macroExecState *ExecutionState, ctx *SubstitutionContext) Result {
				filename := ""
				lineOffset := 0
				columnOffset := 0
				if ctx != nil {
					filename = ctx.Filename
					lineOffset = ctx.CurrentLineOffset
					columnOffset = ctx.CurrentColumnOffset
				}
				return e.ExecuteWithState(commands, macroExecState, ctx, filename, lineOffset, columnOffset)
			},
			args,
			namedArgs,
			handle.State,
			nil, // position
			nil, // no parent state - fiber is isolated
		)

		// Check if result is a TokenResult (suspended)
		if tokenResult, ok := result.(TokenResult); ok {
			tokenID := string(tokenResult)

			handle.mu.Lock()
			handle.SuspendedOn = tokenID
			handle.mu.Unlock()

			e.logger.Debug("Fiber %d suspended on token %s, waiting for completion", fiberID, tokenID)

			// Wait for the token chain to complete
			// The token system will send resume data when all chained tokens finish
			resumeData := <-handle.ResumeChan

			e.logger.Debug("Fiber %d token %s completed with status %v", fiberID, resumeData.TokenID, resumeData.Status)

			handle.mu.Lock()
			handle.SuspendedOn = ""
			// Get the final result from the state
			if handle.State.HasResult() {
				handle.Result = handle.State.GetResult()
			} else if resumeData.Result != nil {
				handle.Result = resumeData.Result
			}
			handle.mu.Unlock()
		} else {
			// Normal completion - get the actual result value from state
			handle.mu.Lock()
			if handle.State.HasResult() {
				handle.Result = handle.State.GetResult()
			} else {
				handle.Result = nil
			}
			handle.mu.Unlock()

			e.logger.Debug("Fiber %d completed with result: %v", fiberID, handle.Result)
		}
	}()

	return handle
}

// WaitForFiber blocks until the specified fiber completes
func (e *Executor) WaitForFiber(fiber *FiberHandle) (interface{}, error) {
	if fiber == nil {
		return nil, fmt.Errorf("fiber handle is nil")
	}

	<-fiber.CompleteChan

	fiber.mu.RLock()
	defer fiber.mu.RUnlock()

	return fiber.Result, fiber.Error
}

// WaitForAllFibers blocks until all child fibers complete
func (e *Executor) WaitForAllFibers() {
	e.mu.RLock()
	fibers := make([]*FiberHandle, 0, len(e.activeFibers))
	for _, fiber := range e.activeFibers {
		// Don't wait for main fiber (ID 0)
		if fiber.ID != 0 {
			fibers = append(fibers, fiber)
		}
	}
	e.mu.RUnlock()

	var wg sync.WaitGroup
	for _, fiber := range fibers {
		wg.Add(1)
		go func(f *FiberHandle) {
			defer wg.Done()
			<-f.CompleteChan
		}(fiber)
	}

	wg.Wait()
}
