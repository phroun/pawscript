package pawscript

import (
	"fmt"
	"time"
)

// PawScript is the main PawScript interpreter
type PawScript struct {
	config      *Config
	logger      *Logger
	executor    *Executor
	macroSystem *MacroSystem
}

// New creates a new PawScript interpreter
func New(config *Config) *PawScript {
	if config == nil {
		config = DefaultConfig()
	}

	logger := NewLogger(config.Debug)
	executor := NewExecutor(logger)
	macroSystem := NewMacroSystem(logger)

	// Set executor reference on macro system for object storage
	macroSystem.SetExecutor(executor)

	ps := &PawScript{
		config:      config,
		logger:      logger,
		executor:    executor,
		macroSystem: macroSystem,
	}

	// Register built-in super commands (always available)
	ps.registerSuperCommands()

	// Register stdlib module items (available via import)
	ps.registerStdlibModuleItems()

	// Set up macro fallback handler if macros are allowed
	if config.AllowMacros {
		executor.SetFallbackHandler(func(cmdName string, args []interface{}, namedArgs map[string]interface{}, state *ExecutionState, position *SourcePosition) Result {
			ps.logger.Debug("Fallback handler called for command: %s", cmdName)
			if macroSystem.HasMacro(cmdName) {
				ps.logger.Debug("Found macro: %s", cmdName)
				if state == nil {
					state = NewExecutionState()
				}

				// Ensure state has executor reference before creating child
				if state.executor == nil {
					state.executor = executor
				}

				// Create a child state so the macro has its own fresh variable scope
				macroState := state.CreateChild()

				result := macroSystem.ExecuteMacro(cmdName, func(commands string, macroExecState *ExecutionState, ctx *SubstitutionContext) Result {
					// Use filename and offsets from substitution context for proper error reporting
					filename := ""
					lineOffset := 0
					columnOffset := 0
					if ctx != nil {
						filename = ctx.Filename
						lineOffset = ctx.CurrentLineOffset
						columnOffset = ctx.CurrentColumnOffset
					}
					return executor.ExecuteWithState(commands, macroExecState, ctx, filename, lineOffset, columnOffset)
				}, args, namedArgs, macroState, position, state) // Pass parent state

				return result
			}
			ps.logger.Debug("Macro not found: %s", cmdName)
			return nil
		})
	}

	return ps
}

// registerStdlibModuleItems registers stdlib module items (available via import)
func (ps *PawScript) registerStdlibModuleItems() {
	// Define macro command
	ps.executor.RegisterModuleItem("stdlib", "macro", func(ctx *Context) Result {
		ps.logger.Debug("macro command called with %d args", len(ctx.Args))

		// Check for anonymous macro: macro (body)
		// If only 1 arg, create anonymous macro
		if len(ctx.Args) == 1 {
			// Anonymous macro: macro (body)
			commands := fmt.Sprintf("%v", ctx.Args[0])
			ps.logger.Debug("Creating anonymous macro with commands: %s", commands)

			// Create StoredMacro object
			macro := NewStoredMacro(commands, ctx.Position)

			// Store it in the executor's object store
			objectID := ctx.executor.storeObject(macro, "macro")

			// Create a marker for the macro
			macroMarker := fmt.Sprintf("\x00MACRO:%d\x00", objectID)

			// Set the marker as the result and claim a reference
			ctx.state.SetResult(Symbol(macroMarker))

			ps.logger.Debug("Created anonymous macro (object %d)", objectID)
			return BoolStatus(true)
		}

		// Named macro: macro name, (body)
		if len(ctx.Args) < 2 {
			ps.logger.Error("Usage: macro <name>, <commands> OR macro <commands>")
			return BoolStatus(false)
		}

		name := fmt.Sprintf("%v", ctx.Args[0])
		commands := fmt.Sprintf("%v", ctx.Args[1])

		ps.logger.Debug("Defining macro '%s' with commands: %s", name, commands)

		_, result := ps.macroSystem.DefineMacro(name, commands, ctx.Position)
		if !result {
			ps.logger.Error("Failed to define macro \"%s\"", name)
			return BoolStatus(false)
		}

		// For named macros, don't set a result - they're registered by name
		// (only anonymous macros return the macro object as a result)
		ps.logger.Debug("Successfully defined named macro '%s'", name)

		return BoolStatus(true)
	})

	// Call macro or command
	ps.executor.RegisterModuleItem("stdlib", "call", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: call <macro_name_or_object>, [args...]")
			return BoolStatus(false)
		}

		callArgs := ctx.Args[1:]

		// Create a child state so the called macro/command has its own scope
		// but can access parent variables via get_parent/set_parent
		childState := ctx.state.CreateChild()

		firstArg := ctx.Args[0]

		// Check if the first argument is already a resolved StoredCommand object
		if cmd, ok := firstArg.(StoredCommand); ok {
			ps.logger.Debug("Calling resolved StoredCommand object: %s", cmd.CommandName)

			// Create a new context for the command with the child state
			cmdCtx := &Context{
				Args:      callArgs,
				NamedArgs: ctx.NamedArgs,
				Position:  ctx.Position,
				state:     childState,
				executor:  ctx.executor,
				logger:    ctx.logger,
			}

			// Execute the command handler
			result := cmd.Handler(cmdCtx)

			// Transfer result to parent state if one was set
			if childState.HasResult() {
				ctx.state.SetResult(childState.GetResult())
			}

			return result
		}

		// Check if the first argument is already a resolved StoredMacro object
		if macro, ok := firstArg.(StoredMacro); ok {
			ps.logger.Debug("Calling resolved StoredMacro object")

			// Execute the stored macro directly
			return ps.macroSystem.ExecuteStoredMacro(&macro, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
				filename := ""
				lineOffset := 0
				columnOffset := 0
				if substCtx != nil {
					filename = substCtx.Filename
					lineOffset = substCtx.CurrentLineOffset
					columnOffset = substCtx.CurrentColumnOffset
				}
				return ps.executor.ExecuteWithState(commands, macroExecState, substCtx, filename, lineOffset, columnOffset)
			}, callArgs, ctx.NamedArgs, childState, ctx.Position, ctx.state)
		}

		// Check if the first argument is a marker (Symbol)
		if sym, ok := firstArg.(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))

			// Handle StoredCommand marker
			if markerType == "command" && objectID >= 0 {
				ps.logger.Debug("Calling StoredCommand via marker (object %d)", objectID)

				// Retrieve the command object
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.Error("Command object %d not found", objectID)
					return BoolStatus(false)
				}

				cmd, ok := obj.(StoredCommand)
				if !ok {
					ps.logger.Error("Object %d is not a StoredCommand", objectID)
					return BoolStatus(false)
				}

				// Create a new context for the command with the child state
				cmdCtx := &Context{
					Args:      callArgs,
					NamedArgs: ctx.NamedArgs,
					Position:  ctx.Position,
					state:     childState,
					executor:  ctx.executor,
					logger:    ctx.logger,
				}

				// Execute the command handler
				result := cmd.Handler(cmdCtx)

				// Transfer result to parent state if one was set
				if childState.HasResult() {
					ctx.state.SetResult(childState.GetResult())
				}

				return result
			}

			// Handle StoredMacro marker
			if markerType == "macro" && objectID >= 0 {
				// It's a StoredMacro marker - retrieve and execute it
				ps.logger.Debug("Calling StoredMacro via marker (object %d)", objectID)

				// Retrieve the macro object
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.Error("Macro object %d not found", objectID)
					return BoolStatus(false)
				}

				macro, ok := obj.(StoredMacro)
				if !ok {
					ps.logger.Error("Object %d is not a StoredMacro", objectID)
					return BoolStatus(false)
				}

				// Execute the stored macro directly
				return ps.macroSystem.ExecuteStoredMacro(&macro, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
					filename := ""
					lineOffset := 0
					columnOffset := 0
					if substCtx != nil {
						filename = substCtx.Filename
						lineOffset = substCtx.CurrentLineOffset
						columnOffset = substCtx.CurrentColumnOffset
					}
					return ps.executor.ExecuteWithState(commands, macroExecState, substCtx, filename, lineOffset, columnOffset)
				}, callArgs, ctx.NamedArgs, childState, ctx.Position, ctx.state)
			}
		}

		// Otherwise, treat it as a macro name
		name := fmt.Sprintf("%v", firstArg)
		ps.logger.Debug("Calling macro by name: %s", name)

		return ps.macroSystem.ExecuteMacro(name, func(commands string, macroExecState *ExecutionState, substCtx *SubstitutionContext) Result {
			// Use filename and offsets from substitution context for proper error reporting
			filename := ""
			lineOffset := 0
			columnOffset := 0
			if substCtx != nil {
				filename = substCtx.Filename
				lineOffset = substCtx.CurrentLineOffset
				columnOffset = substCtx.CurrentColumnOffset
			}
			return ps.executor.ExecuteWithState(commands, macroExecState, substCtx, filename, lineOffset, columnOffset)
		}, callArgs, ctx.NamedArgs, childState, ctx.Position, ctx.state) // Pass parent state
	})

	// List macros command
	ps.executor.RegisterModuleItem("stdlib", "macro_list", func(ctx *Context) Result {
		macros := ps.macroSystem.ListMacros()
		ctx.SetResult(fmt.Sprintf("%v", macros))
		return BoolStatus(true)
	})

	// Delete macro command
	ps.executor.RegisterModuleItem("stdlib", "macro_delete", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: macro_delete <macro_name>")
			return BoolStatus(false)
		}

		name := fmt.Sprintf("%v", ctx.Args[0])
		result := ps.macroSystem.DeleteMacro(name)

		if !result {
			ctx.LogError(CatMacro, fmt.Sprintf("PawScript macro \"%s\" not found or could not be deleted", name))
		}

		return BoolStatus(result)
	})

	// Command reference command - get a reference to a built-in or registered command
	ps.executor.RegisterModuleItem("stdlib", "command_ref", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: command_ref <command_name>")
			return BoolStatus(false)
		}

		commandName := fmt.Sprintf("%v", ctx.Args[0])
		ps.logger.Debug("Getting command reference for: %s", commandName)

		// Get the command handler from the executor
		handler, exists := ctx.executor.GetCommand(commandName)
		if !exists {
			ps.logger.Error("Command \"%s\" not found", commandName)
			return BoolStatus(false)
		}

		// Create StoredCommand object
		cmd := NewStoredCommand(commandName, handler)

		// Store it in the executor's object store
		objectID := ctx.executor.storeObject(cmd, "command")

		// Create a marker for the command
		commandMarker := fmt.Sprintf("\x00COMMAND:%d\x00", objectID)

		// Set the marker as the result
		ctx.state.SetResult(Symbol(commandMarker))

		ps.logger.Debug("Created command reference for '%s' (object %d)", commandName, objectID)
		return BoolStatus(true)
	})

	// Channel command - create a native or custom channel
	ps.executor.RegisterModuleItem("stdlib", "channel", func(ctx *Context) Result {
		// Parse arguments for buffer size and custom handlers
		bufferSize := 0
		var customSend, customRecv, customClose *StoredMacro

		// Check for buffer size as first positional argument
		if len(ctx.Args) > 0 {
			// Try to parse as int
			if size, ok := ctx.Args[0].(int); ok {
				bufferSize = size
			} else if sizeStr, ok := ctx.Args[0].(string); ok {
				fmt.Sscanf(sizeStr, "%d", &bufferSize)
			}
		}

		// Check for custom send/recv/close handlers in named args
		if sendVal, ok := ctx.NamedArgs["send"]; ok {
			if macro, ok := sendVal.(StoredMacro); ok {
				customSend = &macro
			}
		}
		if recvVal, ok := ctx.NamedArgs["recv"]; ok {
			if macro, ok := recvVal.(StoredMacro); ok {
				customRecv = &macro
			}
		}
		if closeVal, ok := ctx.NamedArgs["close"]; ok {
			if macro, ok := closeVal.(StoredMacro); ok {
				customClose = &macro
			}
		}

		// Create channel
		ch := NewStoredChannel(bufferSize)
		ch.CustomSend = customSend
		ch.CustomRecv = customRecv
		ch.CustomClose = customClose

		// Store in object store
		objectID := ctx.executor.storeObject(ch, "channel")
		channelMarker := fmt.Sprintf("\x00CHANNEL:%d\x00", objectID)
		ctx.state.SetResult(Symbol(channelMarker))

		ps.logger.Debug("Created channel (object %d) with buffer size %d", objectID, bufferSize)
		return BoolStatus(true)
	})

	ps.executor.RegisterModuleItem("stdlib", "channel_subscribe", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: channel_subscribe <channel>")
			return BoolStatus(false)
		}

		// Get channel from first argument
		var ch *StoredChannel
		if channelObj, ok := ctx.Args[0].(*StoredChannel); ok {
			ch = channelObj
		} else if sym, ok := ctx.Args[0].(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "channel" && objectID >= 0 {
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.Error("Channel object %d not found", objectID)
					return BoolStatus(false)
				}
				if channelObj, ok := obj.(*StoredChannel); ok {
					ch = channelObj
				}
			}
		}

		if ch == nil {
			ps.logger.Error("First argument must be a channel")
			return BoolStatus(false)
		}

		// Subscribe to channel
		subscriber, err := ChannelSubscribe(ch)
		if err != nil {
			ps.logger.Error("Failed to subscribe: %v", err)
			return BoolStatus(false)
		}

		// Store subscriber in object store
		objectID := ctx.executor.storeObject(subscriber, "channel")
		subscriberMarker := fmt.Sprintf("\x00CHANNEL:%d\x00", objectID)
		ctx.state.SetResult(Symbol(subscriberMarker))

		ps.logger.Debug("Created subscriber %d for channel (object %d)", subscriber.SubscriberID, objectID)
		return BoolStatus(true)
	})

	ps.executor.RegisterModuleItem("stdlib", "channel_send", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ps.logger.Error("Usage: channel_send <channel>, <value>")
			return BoolStatus(false)
		}

		// Get channel from first argument
		var ch *StoredChannel
		if channelObj, ok := ctx.Args[0].(*StoredChannel); ok {
			ch = channelObj
		} else if sym, ok := ctx.Args[0].(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "channel" && objectID >= 0 {
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.Error("Channel object %d not found", objectID)
					return BoolStatus(false)
				}
				if channelObj, ok := obj.(*StoredChannel); ok {
					ch = channelObj
				}
			}
		}

		if ch == nil {
			ps.logger.Error("First argument must be a channel")
			return BoolStatus(false)
		}

		// Send value to channel
		err := ChannelSend(ch, ctx.Args[1])
		if err != nil {
			ps.logger.Error("Failed to send: %v", err)
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	ps.executor.RegisterModuleItem("stdlib", "channel_recv", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: channel_recv <channel>")
			return BoolStatus(false)
		}

		// Get channel from first argument
		var ch *StoredChannel
		if channelObj, ok := ctx.Args[0].(*StoredChannel); ok {
			ch = channelObj
		} else if sym, ok := ctx.Args[0].(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "channel" && objectID >= 0 {
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.Error("Channel object %d not found", objectID)
					return BoolStatus(false)
				}
				if channelObj, ok := obj.(*StoredChannel); ok {
					ch = channelObj
				}
			}
		}

		if ch == nil {
			ps.logger.Error("First argument must be a channel")
			return BoolStatus(false)
		}

		// Receive from channel
		senderID, value, err := ChannelRecv(ch)
		if err != nil {
			ps.logger.Error("Failed to receive: %v", err)
			return BoolStatus(false)
		}

		// Return tuple (sender_id, value) as a StoredList
		tuple := NewStoredList([]interface{}{senderID, value})
		tupleID := ctx.executor.storeObject(tuple, "list")
		tupleMarker := fmt.Sprintf("\x00LIST:%d\x00", tupleID)
		ctx.state.SetResult(Symbol(tupleMarker))

		return BoolStatus(true)
	})

	ps.executor.RegisterModuleItem("stdlib", "channel_close", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: channel_close <channel>")
			return BoolStatus(false)
		}

		// Get channel from first argument
		var ch *StoredChannel
		if channelObj, ok := ctx.Args[0].(*StoredChannel); ok {
			ch = channelObj
		} else if sym, ok := ctx.Args[0].(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "channel" && objectID >= 0 {
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.Error("Channel object %d not found", objectID)
					return BoolStatus(false)
				}
				if channelObj, ok := obj.(*StoredChannel); ok {
					ch = channelObj
				}
			}
		}

		if ch == nil {
			ps.logger.Error("First argument must be a channel")
			return BoolStatus(false)
		}

		// Close channel
		err := ChannelClose(ch)
		if err != nil {
			ps.logger.Error("Failed to close: %v", err)
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	ps.executor.RegisterModuleItem("stdlib", "channel_disconnect", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ps.logger.Error("Usage: channel_disconnect <channel>, <subscriber_id>")
			return BoolStatus(false)
		}

		// Get channel from first argument
		var ch *StoredChannel
		if channelObj, ok := ctx.Args[0].(*StoredChannel); ok {
			ch = channelObj
		} else if sym, ok := ctx.Args[0].(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "channel" && objectID >= 0 {
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.Error("Channel object %d not found", objectID)
					return BoolStatus(false)
				}
				if channelObj, ok := obj.(*StoredChannel); ok {
					ch = channelObj
				}
			}
		}

		if ch == nil {
			ps.logger.Error("First argument must be a channel")
			return BoolStatus(false)
		}

		// Parse subscriber ID
		subscriberID := 0
		if id, ok := ctx.Args[1].(int); ok {
			subscriberID = id
		} else if idStr, ok := ctx.Args[1].(string); ok {
			fmt.Sscanf(idStr, "%d", &subscriberID)
		}

		// Disconnect subscriber
		err := ChannelDisconnect(ch, subscriberID)
		if err != nil {
			ps.logger.Error("Failed to disconnect: %v", err)
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	ps.executor.RegisterModuleItem("stdlib", "channel_opened", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: channel_opened <channel>")
			return BoolStatus(false)
		}

		// Get channel from first argument
		var ch *StoredChannel
		if channelObj, ok := ctx.Args[0].(*StoredChannel); ok {
			ch = channelObj
		} else if sym, ok := ctx.Args[0].(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "channel" && objectID >= 0 {
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.Error("Channel object %d not found", objectID)
					return BoolStatus(false)
				}
				if channelObj, ok := obj.(*StoredChannel); ok {
					ch = channelObj
				}
			}
		}

		if ch == nil {
			ps.logger.Error("First argument must be a channel")
			return BoolStatus(false)
		}

		// Check if opened
		opened := ChannelIsOpened(ch)
		ctx.state.SetResult(opened)

		return BoolStatus(true)
	})

	ps.executor.RegisterModuleItem("stdlib", "system_channel", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: system_channel <name>")
			ps.logger.Error("  Available: stdin, stdout, stderr, stdio")
			return BoolStatus(false)
		}

		channelType := fmt.Sprintf("%v", ctx.Args[0])

		// Create channel with custom handlers based on type
		ch := NewStoredChannel(0) // Unbuffered for I/O

		switch channelType {
		case "stdin":
			// Read-only channel from stdin
			ch.CustomRecv = &StoredMacro{
				Commands: "# Read from stdin",
			}
			// Send should error
			ch.CustomSend = &StoredMacro{
				Commands: "# Cannot send to stdin",
			}

		case "stdout":
			// Write-only channel to stdout
			ch.CustomSend = &StoredMacro{
				Commands: "# Write to stdout",
			}
			// Recv should error
			ch.CustomRecv = &StoredMacro{
				Commands: "# Cannot read from stdout",
			}

		case "stderr":
			// Write-only channel to stderr
			ch.CustomSend = &StoredMacro{
				Commands: "# Write to stderr",
			}
			// Recv should error
			ch.CustomRecv = &StoredMacro{
				Commands: "# Cannot read from stderr",
			}

		case "stdio":
			// Bidirectional: read from stdin, write to stdout
			ch.CustomRecv = &StoredMacro{
				Commands: "# Read from stdin",
			}
			ch.CustomSend = &StoredMacro{
				Commands: "# Write to stdout",
			}

		default:
			ps.logger.Error("Unknown system channel type: %s", channelType)
			ps.logger.Error("  Available: stdin, stdout, stderr, stdio")
			return BoolStatus(false)
		}

		// Store in object store
		objectID := ctx.executor.storeObject(ch, "channel")
		channelMarker := fmt.Sprintf("\x00CHANNEL:%d\x00", objectID)
		ctx.state.SetResult(Symbol(channelMarker))

		ps.logger.Debug("Created system channel '%s' (object %d)", channelType, objectID)
		return BoolStatus(true)
	})


	// Fiber commands
	ps.executor.RegisterModuleItem("stdlib", "fiber_spawn", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.Error("Usage: fiber_spawn <macro>, [args...]")
			return BoolStatus(false)
		}

		firstArg := ctx.Args[0]
		fiberArgs := ctx.Args[1:]
		namedArgs := ctx.NamedArgs

		// Get the macro to execute
		var macro *StoredMacro

		// Check if first argument is a resolved StoredMacro object
		if m, ok := firstArg.(StoredMacro); ok {
			macro = &m
		} else if sym, ok := firstArg.(Symbol); ok {
			markerType, objectID := parseObjectMarker(string(sym))
			if markerType == "macro" && objectID >= 0 {
				obj, exists := ctx.executor.getObject(objectID)
				if !exists {
					ps.logger.Error("Macro object %d not found", objectID)
					return BoolStatus(false)
				}
				if m, ok := obj.(StoredMacro); ok {
					macro = &m
				}
			}
		}

		if macro == nil {
			ps.logger.Error("First argument must be a macro")
			return BoolStatus(false)
		}

		// Spawn the fiber
		handle := ctx.executor.SpawnFiber(macro, ps.macroSystem, fiberArgs, namedArgs)

		// Store the fiber handle as an object
		objectID := ctx.executor.storeObject(handle, "fiber")
		fiberMarker := fmt.Sprintf("\x00FIBER:%d\x00", objectID)
		ctx.state.SetResult(Symbol(fiberMarker))

		ps.logger.Debug("Spawned fiber %d (object %d)", handle.ID, objectID)
		return BoolStatus(true)
	})

	ps.executor.RegisterModuleItem("stdlib", "fiber_wait", func(ctx *Context) Result {
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

	ps.executor.RegisterModuleItem("stdlib", "fiber_count", func(ctx *Context) Result {
		count := ctx.executor.GetFiberCount()
		ctx.state.SetResult(count)
		return BoolStatus(true)
	})

	ps.executor.RegisterModuleItem("stdlib", "fiber_id", func(ctx *Context) Result {
		fiberID := ctx.state.fiberID
		ctx.state.SetResult(fiberID)
		return BoolStatus(true)
	})

	ps.executor.RegisterModuleItem("stdlib", "fiber_wait_all", func(ctx *Context) Result {
		// Wait for all child fibers to complete
		ctx.executor.WaitForAllFibers()
		return BoolStatus(true)
	})

	// Clear all macros command
	ps.executor.RegisterModuleItem("stdlib", "macro_clear", func(ctx *Context) Result {
		count := ps.macroSystem.ClearMacros()
		ctx.SetResult(fmt.Sprintf("Cleared %d PawScript macros", count))
		return BoolStatus(true)
	})
}

// Configure updates the configuration
func (ps *PawScript) Configure(config *Config) {
	// Update config
	ps.config = config
	ps.logger.SetEnabled(config.Debug)
	// Note: Macro commands are now available via import "stdlib::macro" etc.
	// The AllowMacros config only affects the fallback handler for macro execution
}

// RegisterCommand registers a command handler
func (ps *PawScript) RegisterCommand(name string, handler Handler) {
	ps.executor.RegisterCommand(name, handler)
}

// RegisterCommands registers multiple command handlers
func (ps *PawScript) RegisterCommands(commands map[string]Handler) {
	for name, handler := range commands {
		ps.executor.RegisterCommand(name, handler)
	}
}

// ExecuteFile executes a script file with proper filename tracking
func (ps *PawScript) ExecuteFile(commandString, filename string) Result {
	state := NewExecutionState()
	return ps.executor.ExecuteWithState(commandString, state, nil, filename, 0, 0)
}

// Execute executes a command string
func (ps *PawScript) Execute(commandString string, args ...interface{}) Result {
	return ps.executor.Execute(commandString, args...)
}

// RequestToken requests an async completion token
func (ps *PawScript) RequestToken(cleanupCallback func(string), parentToken string, timeout time.Duration) string {
	if timeout == 0 {
		timeout = ps.config.DefaultTokenTimeout
	}
	return ps.executor.RequestCompletionToken(cleanupCallback, parentToken, timeout, nil, nil)
}

// ResumeToken resumes execution with a token
func (ps *PawScript) ResumeToken(tokenID string, status bool) bool {
	return ps.executor.PopAndResumeCommandSequence(tokenID, status)
}

// GetTokenStatus returns information about active tokens
func (ps *PawScript) GetTokenStatus() map[string]interface{} {
	return ps.executor.GetTokenStatus()
}

// ForceCleanupToken forces cleanup of a token
func (ps *PawScript) ForceCleanupToken(tokenID string) {
	ps.executor.ForceCleanupToken(tokenID)
}

// DefineMacro defines a new macro
func (ps *PawScript) DefineMacro(name, commandSequence string) bool {
	if !ps.config.AllowMacros {
		ps.logger.Warn("Macros are disabled in configuration")
		return false
	}
	_, ok := ps.macroSystem.DefineMacro(name, commandSequence, nil)
	return ok
}

// ExecuteMacro executes a macro
func (ps *PawScript) ExecuteMacro(name string) Result {
	if !ps.config.AllowMacros {
		ps.logger.Warn("Macros are disabled in configuration")
		return BoolStatus(false)
	}

	state := NewExecutionState()

	return ps.macroSystem.ExecuteMacro(name, func(commands string, macroState *ExecutionState, ctx *SubstitutionContext) Result {
		// Use filename from substitution context for proper error reporting
		filename := ""
		if ctx != nil {
			filename = ctx.Filename
		}
		return ps.executor.ExecuteWithState(commands, macroState, ctx, filename, 0, 0)
	}, []interface{}{}, nil, state, nil, nil) // No parent for top-level call
}

// ListMacros returns a list of all macro names
func (ps *PawScript) ListMacros() []string {
	return ps.macroSystem.ListMacros()
}

// GetMacro returns the commands for a macro
func (ps *PawScript) GetMacro(name string) *string {
	return ps.macroSystem.GetMacro(name)
}

// DeleteMacro deletes a macro
func (ps *PawScript) DeleteMacro(name string) bool {
	return ps.macroSystem.DeleteMacro(name)
}

// ClearMacros clears all macros
func (ps *PawScript) ClearMacros() int {
	return ps.macroSystem.ClearMacros()
}

// HasMacro checks if a macro exists
func (ps *PawScript) HasMacro(name string) bool {
	return ps.macroSystem.HasMacro(name)
}

// SetFallbackHandler sets a fallback handler for unknown commands
func (ps *PawScript) SetFallbackHandler(handler func(string, []interface{}) Result) {
	ps.executor.SetFallbackHandler(func(cmdName string, args []interface{}, namedArgs map[string]interface{}, state *ExecutionState, position *SourcePosition) Result {
		return handler(cmdName, args)
	})
}

// GetConfig returns the current configuration
func (ps *PawScript) GetConfig() *Config {
	configCopy := *ps.config
	return &configCopy
}

// SetErrorContextEnabled enables or disables error context reporting
func (ps *PawScript) SetErrorContextEnabled(enabled bool) {
	ps.config.ShowErrorContext = enabled
}

// SetContextLines sets the number of context lines for error reporting
func (ps *PawScript) SetContextLines(lines int) {
	if lines < 0 {
		lines = 0
	}
	if lines > 10 {
		lines = 10
	}
	ps.config.ContextLines = lines
}
