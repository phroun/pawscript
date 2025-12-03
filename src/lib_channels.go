package pawscript

import (
	"fmt"
	"strings"
)

// getChannelFromArg extracts a *StoredChannel from an argument
// Handles both raw *StoredChannel and marker strings (Symbol or string)
func getChannelFromArg(arg interface{}, executor *Executor) *StoredChannel {
	// Direct *StoredChannel
	if ch, ok := arg.(*StoredChannel); ok {
		return ch
	}

	// Try to parse as marker (could be Symbol or string)
	var markerStr string
	if sym, ok := arg.(Symbol); ok {
		markerStr = string(sym)
	} else if str, ok := arg.(string); ok {
		markerStr = str
	}

	if markerStr != "" {
		markerType, objectID := parseObjectMarker(markerStr)
		if markerType == "channel" && objectID >= 0 {
			if obj, exists := executor.getObject(objectID); exists {
				if ch, ok := obj.(*StoredChannel); ok {
					return ch
				}
			}
		}
	}

	return nil
}

// RegisterChannelsLib registers channel-related commands
// Module: channels
func (ps *PawScript) RegisterChannelsLib() {

	// channel - create a native or custom channel
	ps.RegisterCommandInModule("channels", "channel", func(ctx *Context) Result {
		bufferSize := 0
		var customSend, customRecv, customClose *StoredMacro

		// Check for buffer size as first positional argument
		if len(ctx.Args) > 0 {
			if size, ok := ctx.Args[0].(int); ok {
				bufferSize = size
			} else if sizeStr, ok := ctx.Args[0].(string); ok {
				_, _ = fmt.Sscanf(sizeStr, "%d", &bufferSize)
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

		ch := NewStoredChannel(bufferSize)
		ch.CustomSend = customSend
		ch.CustomRecv = customRecv
		ch.CustomClose = customClose

		objectID := ctx.executor.storeObject(ch, "channel")
		channelMarker := fmt.Sprintf("\x00CHANNEL:%d\x00", objectID)
		ctx.state.SetResult(Symbol(channelMarker))

		ps.logger.DebugCat(CatAsync, "Created channel (object %d) with buffer size %d", objectID, bufferSize)
		return BoolStatus(true)
	})

	ps.RegisterCommandInModule("channels", "channel_subscribe", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.ErrorCat(CatCommand, "Usage: channel_subscribe <channel>")
			return BoolStatus(false)
		}

		ch := getChannelFromArg(ctx.Args[0], ctx.executor)
		if ch == nil {
			ps.logger.ErrorCat(CatArgument, "First argument must be a channel")
			return BoolStatus(false)
		}

		subscriber, err := ChannelSubscribe(ch)
		if err != nil {
			ps.logger.ErrorCat(CatAsync, "Failed to subscribe: %v", err)
			return BoolStatus(false)
		}

		objectID := ctx.executor.storeObject(subscriber, "channel")
		subscriberMarker := fmt.Sprintf("\x00CHANNEL:%d\x00", objectID)
		ctx.state.SetResult(Symbol(subscriberMarker))

		ps.logger.DebugCat(CatAsync, "Created subscriber %d for channel (object %d)", subscriber.SubscriberID, objectID)
		return BoolStatus(true)
	})

	ps.RegisterCommandInModule("channels", "channel_send", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ps.logger.ErrorCat(CatCommand, "Usage: channel_send <channel>, <value>")
			return BoolStatus(false)
		}

		// Helper to resolve a value to a channel
		resolveToChannel := func(val interface{}) *StoredChannel {
			switch v := val.(type) {
			case *StoredChannel:
				return v
			case Symbol:
				markerType, objectID := parseObjectMarker(string(v))
				if markerType == "channel" && objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						if ch, ok := obj.(*StoredChannel); ok {
							return ch
						}
					}
				}
			case string:
				markerType, objectID := parseObjectMarker(v)
				if markerType == "channel" && objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						if ch, ok := obj.(*StoredChannel); ok {
							return ch
						}
					}
				}
			}
			return nil
		}

		var ch *StoredChannel
		if channelObj, ok := ctx.Args[0].(*StoredChannel); ok {
			ch = channelObj
		} else if sym, ok := ctx.Args[0].(Symbol); ok {
			symStr := string(sym)
			// Check for #-prefixed symbol (resolve like tilde would)
			if strings.HasPrefix(symStr, "#") {
				// First check local variables
				if localVal, exists := ctx.state.GetVariable(symStr); exists {
					ch = resolveToChannel(localVal)
				}
				// Then check ObjectsModule
				if ch == nil && ctx.state.moduleEnv != nil {
					ctx.state.moduleEnv.mu.RLock()
					if ctx.state.moduleEnv.ObjectsModule != nil {
						if obj, exists := ctx.state.moduleEnv.ObjectsModule[symStr]; exists {
							ch = resolveToChannel(obj)
						}
					}
					ctx.state.moduleEnv.mu.RUnlock()
				}
			} else {
				ch = resolveToChannel(sym)
			}
		} else if str, ok := ctx.Args[0].(string); ok {
			// Handle string type markers (from $1 substitution, etc.)
			if strings.HasPrefix(str, "#") {
				// First check local variables
				if localVal, exists := ctx.state.GetVariable(str); exists {
					ch = resolveToChannel(localVal)
				}
				// Then check ObjectsModule
				if ch == nil && ctx.state.moduleEnv != nil {
					ctx.state.moduleEnv.mu.RLock()
					if ctx.state.moduleEnv.ObjectsModule != nil {
						if obj, exists := ctx.state.moduleEnv.ObjectsModule[str]; exists {
							ch = resolveToChannel(obj)
						}
					}
					ctx.state.moduleEnv.mu.RUnlock()
				}
			} else {
				ch = resolveToChannel(str)
			}
		}

		if ch == nil {
			ps.logger.ErrorCat(CatArgument, "First argument must be a channel")
			return BoolStatus(false)
		}

		err := ChannelSend(ch, ctx.Args[1])
		if err != nil {
			ps.logger.ErrorCat(CatAsync, "Failed to send: %v", err)
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	ps.RegisterCommandInModule("channels", "channel_recv", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.ErrorCat(CatCommand, "Usage: channel_recv <channel>")
			return BoolStatus(false)
		}

		// Helper to resolve a value to a channel
		resolveToChannel := func(val interface{}) *StoredChannel {
			switch v := val.(type) {
			case *StoredChannel:
				return v
			case Symbol:
				markerType, objectID := parseObjectMarker(string(v))
				if markerType == "channel" && objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						if ch, ok := obj.(*StoredChannel); ok {
							return ch
						}
					}
				}
			case string:
				markerType, objectID := parseObjectMarker(v)
				if markerType == "channel" && objectID >= 0 {
					if obj, exists := ctx.executor.getObject(objectID); exists {
						if ch, ok := obj.(*StoredChannel); ok {
							return ch
						}
					}
				}
			}
			return nil
		}

		var ch *StoredChannel
		if channelObj, ok := ctx.Args[0].(*StoredChannel); ok {
			ch = channelObj
		} else if sym, ok := ctx.Args[0].(Symbol); ok {
			symStr := string(sym)
			// Check for #-prefixed symbol (resolve like tilde would)
			if strings.HasPrefix(symStr, "#") {
				// First check local variables
				if localVal, exists := ctx.state.GetVariable(symStr); exists {
					ch = resolveToChannel(localVal)
				}
				// Then check ObjectsModule
				if ch == nil && ctx.state.moduleEnv != nil {
					ctx.state.moduleEnv.mu.RLock()
					if ctx.state.moduleEnv.ObjectsModule != nil {
						if obj, exists := ctx.state.moduleEnv.ObjectsModule[symStr]; exists {
							ch = resolveToChannel(obj)
						}
					}
					ctx.state.moduleEnv.mu.RUnlock()
				}
			} else {
				ch = resolveToChannel(sym)
			}
		} else if str, ok := ctx.Args[0].(string); ok {
			// Handle string type markers (from $1 substitution, etc.)
			if strings.HasPrefix(str, "#") {
				// First check local variables
				if localVal, exists := ctx.state.GetVariable(str); exists {
					ch = resolveToChannel(localVal)
				}
				// Then check ObjectsModule
				if ch == nil && ctx.state.moduleEnv != nil {
					ctx.state.moduleEnv.mu.RLock()
					if ctx.state.moduleEnv.ObjectsModule != nil {
						if obj, exists := ctx.state.moduleEnv.ObjectsModule[str]; exists {
							ch = resolveToChannel(obj)
						}
					}
					ctx.state.moduleEnv.mu.RUnlock()
				}
			} else {
				ch = resolveToChannel(str)
			}
		}

		if ch == nil {
			ps.logger.ErrorCat(CatArgument, "First argument must be a channel")
			return BoolStatus(false)
		}

		senderID, value, err := ChannelRecv(ch)
		if err != nil {
			ps.logger.ErrorCat(CatAsync, "Failed to receive: %v", err)
			return BoolStatus(false)
		}

		tuple := NewStoredListWithoutRefs([]interface{}{senderID, value})
		tupleID := ctx.executor.storeObject(tuple, "list")
		tupleMarker := fmt.Sprintf("\x00LIST:%d\x00", tupleID)
		ctx.state.SetResult(Symbol(tupleMarker))

		return BoolStatus(true)
	})

	ps.RegisterCommandInModule("channels", "channel_close", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.ErrorCat(CatCommand, "Usage: channel_close <channel>")
			return BoolStatus(false)
		}

		ch := getChannelFromArg(ctx.Args[0], ctx.executor)
		if ch == nil {
			ps.logger.ErrorCat(CatArgument, "First argument must be a channel")
			return BoolStatus(false)
		}

		err := ChannelClose(ch)
		if err != nil {
			ps.logger.ErrorCat(CatAsync, "Failed to close: %v", err)
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	ps.RegisterCommandInModule("channels", "channel_disconnect", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ps.logger.ErrorCat(CatCommand, "Usage: channel_disconnect <channel>, <subscriber_id>")
			return BoolStatus(false)
		}

		ch := getChannelFromArg(ctx.Args[0], ctx.executor)
		if ch == nil {
			ps.logger.ErrorCat(CatArgument, "First argument must be a channel")
			return BoolStatus(false)
		}

		subscriberID := 0
		if id, ok := ctx.Args[1].(int); ok {
			subscriberID = id
		} else if idStr, ok := ctx.Args[1].(string); ok {
			_, _ = fmt.Sscanf(idStr, "%d", &subscriberID)
		}

		err := ChannelDisconnect(ch, subscriberID)
		if err != nil {
			ps.logger.ErrorCat(CatAsync, "Failed to disconnect: %v", err)
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	ps.RegisterCommandInModule("channels", "channel_opened", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ps.logger.ErrorCat(CatCommand, "Usage: channel_opened <channel>")
			return BoolStatus(false)
		}

		ch := getChannelFromArg(ctx.Args[0], ctx.executor)
		if ch == nil {
			ps.logger.ErrorCat(CatArgument, "First argument must be a channel")
			return BoolStatus(false)
		}

		opened := ChannelIsOpened(ch)
		ctx.state.SetResult(opened)

		return BoolStatus(true)
	})
}
