package pawscript

import (
	"fmt"
)

// ChannelSubscribe creates a new subscriber endpoint for a channel
func ChannelSubscribe(ch *StoredChannel) (*StoredChannel, error) {
	if ch == nil {
		return nil, fmt.Errorf("channel is nil")
	}

	// Only main channels can have subscribers
	if ch.IsSubscriber {
		return nil, fmt.Errorf("cannot subscribe to a subscriber endpoint")
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()

	if ch.IsClosed {
		return nil, fmt.Errorf("channel is closed")
	}

	// Create new subscriber with unique ID
	subscriberID := ch.NextSubscriberID
	ch.NextSubscriberID++

	subscriber := NewChannelSubscriber(ch, subscriberID)
	ch.Subscribers[subscriberID] = subscriber

	return subscriber, nil
}

// ChannelSend sends a message to a channel
// If sender is the main channel (ID 0), broadcasts to all subscribers
// If sender is a subscriber, sends only to main channel
func ChannelSend(ch *StoredChannel, value interface{}) error {
	if ch == nil {
		return fmt.Errorf("channel is nil")
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()

	if ch.IsClosed {
		return fmt.Errorf("channel is closed")
	}

	// Check for native send handler first
	if ch.NativeSend != nil {
		return ch.NativeSend(value)
	}

	// Get the main channel
	mainCh := ch
	senderID := 0
	if ch.IsSubscriber {
		mainCh = ch.ParentChannel
		senderID = ch.SubscriberID
	}

	// Check buffer capacity
	if mainCh.BufferSize > 0 && len(mainCh.Messages) >= mainCh.BufferSize {
		return fmt.Errorf("channel buffer full")
	}

	// Create message with consumed tracking
	consumedBy := make(map[int]bool)

	// Mark sender as already consumed (sender doesn't receive own messages)
	consumedBy[senderID] = true

	// Add all current subscribers/channel to the consumedBy map
	// Main channel receives messages from subscribers
	if senderID != 0 {
		// Message from subscriber -> mark all other subscribers + channel as needing to consume
		consumedBy[0] = false // Main channel needs to consume
		for id := range mainCh.Subscribers {
			if id != senderID {
				consumedBy[id] = false
			}
		}
	} else {
		// Message from main channel -> broadcast to all subscribers
		if len(mainCh.Subscribers) > 0 {
			for id := range mainCh.Subscribers {
				consumedBy[id] = false
			}
		} else {
			// No subscribers - main channel can read its own messages
			consumedBy[0] = false
		}
	}

	msg := ChannelMessage{
		SenderID:   senderID,
		Value:      value,
		ConsumedBy: consumedBy,
	}

	// Call custom send handler if present
	// nolint:staticcheck // TODO: Execute custom send macro when implemented
	if mainCh.CustomSend != nil {
		_ = mainCh.CustomSend // Placeholder for future implementation
	}

	mainCh.Messages = append(mainCh.Messages, msg)

	return nil
}

// ChannelRecv receives a message from a channel
// Returns (senderID, value, error)
// Advances read pointer and cleans up fully-consumed messages
func ChannelRecv(ch *StoredChannel) (int, interface{}, error) {
	if ch == nil {
		return 0, nil, fmt.Errorf("channel is nil")
	}

	ch.mu.Lock()

	if ch.IsClosed {
		ch.mu.Unlock()
		return 0, nil, fmt.Errorf("channel is closed")
	}

	// Check for native receive handler first
	// Release lock before calling NativeRecv since it may block
	if ch.NativeRecv != nil {
		nativeRecv := ch.NativeRecv
		ch.mu.Unlock()
		value, err := nativeRecv()
		return 0, value, err
	}

	// For non-native path, use defer unlock
	defer ch.mu.Unlock()

	// Get the main channel and receiver ID
	mainCh := ch
	receiverID := 0
	if ch.IsSubscriber {
		mainCh = ch.ParentChannel
		receiverID = ch.SubscriberID
	}

	// Find first unconsumed message for this receiver
	for i := 0; i < len(mainCh.Messages); i++ {
		msg := &mainCh.Messages[i]

		// Check if this receiver has already consumed this message
		if consumed, exists := msg.ConsumedBy[receiverID]; exists && consumed {
			continue
		}

		// Mark as consumed by this receiver
		msg.ConsumedBy[receiverID] = true

		// Check if all recipients have consumed this message
		allConsumed := true
		for _, consumed := range msg.ConsumedBy {
			if !consumed {
				allConsumed = false
				break
			}
		}

		// If all consumed, remove messages from front of buffer
		if allConsumed {
			// Clean up all fully-consumed messages from the front
			cleanupCount := 0
			for j := 0; j < len(mainCh.Messages); j++ {
				allConsumedJ := true
				for _, consumed := range mainCh.Messages[j].ConsumedBy {
					if !consumed {
						allConsumedJ = false
						break
					}
				}
				if allConsumedJ {
					cleanupCount++
				} else {
					break
				}
			}
			if cleanupCount > 0 {
				mainCh.Messages = mainCh.Messages[cleanupCount:]
			}
		}

		return msg.SenderID, msg.Value, nil
	}

	// No messages available
	return 0, nil, fmt.Errorf("no messages available")
}

// ChannelClose closes a channel or subscriber
func ChannelClose(ch *StoredChannel) error {
	if ch == nil {
		return fmt.Errorf("channel is nil")
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()

	if ch.IsClosed {
		return fmt.Errorf("channel already closed")
	}

	// Call native close handler if present
	if ch.NativeClose != nil {
		err := ch.NativeClose()
		ch.IsClosed = true
		return err
	}

	if ch.IsSubscriber {
		// Disconnect subscriber from parent
		if ch.ParentChannel != nil {
			delete(ch.ParentChannel.Subscribers, ch.SubscriberID)
		}
	} else {
		// Close main channel - disconnect all subscribers
		for _, sub := range ch.Subscribers {
			sub.IsClosed = true
		}
		ch.Subscribers = make(map[int]*StoredChannel)

		// Call custom close handler if present
		// nolint:staticcheck // TODO: Execute custom close macro when implemented
		if ch.CustomClose != nil {
			_ = ch.CustomClose // Placeholder for future implementation
		}
	}

	ch.IsClosed = true
	return nil
}

// ChannelDisconnect disconnects a specific subscriber from a channel
func ChannelDisconnect(ch *StoredChannel, subscriberID int) error {
	if ch == nil {
		return fmt.Errorf("channel is nil")
	}

	if ch.IsSubscriber {
		return fmt.Errorf("cannot disconnect from a subscriber endpoint")
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()

	if ch.IsClosed {
		return fmt.Errorf("channel is closed")
	}

	sub, exists := ch.Subscribers[subscriberID]
	if !exists {
		return fmt.Errorf("subscriber %d not found", subscriberID)
	}

	// Mark subscriber as closed
	sub.IsClosed = true
	delete(ch.Subscribers, subscriberID)

	return nil
}

// ChannelIsOpened checks if a channel or subscriber is still open
func ChannelIsOpened(ch *StoredChannel) bool {
	if ch == nil {
		return false
	}

	ch.mu.RLock()
	defer ch.mu.RUnlock()

	return !ch.IsClosed
}

// ChannelLen returns the number of unread messages for a channel/subscriber
func ChannelLen(ch *StoredChannel) int {
	if ch == nil {
		return 0
	}

	ch.mu.RLock()
	defer ch.mu.RUnlock()

	// Check for native length handler first (for Go channel backing)
	if ch.NativeLen != nil {
		return ch.NativeLen()
	}

	// Get the main channel and receiver ID
	mainCh := ch
	receiverID := 0
	if ch.IsSubscriber {
		mainCh = ch.ParentChannel
		receiverID = ch.SubscriberID
	}

	// Count unconsumed messages
	count := 0
	for i := 0; i < len(mainCh.Messages); i++ {
		msg := &mainCh.Messages[i]
		if consumed, exists := msg.ConsumedBy[receiverID]; !exists || !consumed {
			count++
		}
	}

	return count
}
