package pawscript

import (
	"fmt"
)

// maybeStoreValue checks if a value should be stored as an object and returns the appropriate representation
// Note: Does NOT claim references - the caller must claim the returned object ID
func (e *Executor) maybeStoreValue(value interface{}, state *ExecutionState) interface{} {
	switch v := value.(type) {
	case string:
		if len(v) > StringStorageThreshold {
			id := e.storeObject(StoredString(v), "string")
			return Symbol(fmt.Sprintf("\x00STR:%d\x00", id))
		}
		return v
	case QuotedString:
		if len(v) > StringStorageThreshold {
			id := e.storeObject(StoredString(v), "string")
			return Symbol(fmt.Sprintf("\x00STR:%d\x00", id))
		}
		return v
	case ParenGroup:
		if len(v) > BlockStorageThreshold {
			id := e.storeObject(StoredBlock(v), "block")
			return Symbol(fmt.Sprintf("\x00BLOCK:%d\x00", id))
		}
		return v
	case StoredList:
		// StoredList objects come from processArguments resolving markers
		// Convert back to marker to maintain pass-by-reference semantics
		if id := e.findStoredListID(v); id >= 0 {
			// The list already exists in storage
			// Don't claim here - we're just converting from StoredList to Symbol
			// The state will claim it through normal SetVariable/SetResult flow
			return Symbol(fmt.Sprintf("\x00LIST:%d\x00", id))
		}
		// List not found in store - this shouldn't happen normally
		// Store it as a new object
		id := e.storeObject(v, "list")
		return Symbol(fmt.Sprintf("\x00LIST:%d\x00", id))
	case StoredBytes:
		// StoredBytes objects - convert to marker
		if id := e.findStoredBytesID(v); id >= 0 {
			return Symbol(fmt.Sprintf("\x00BYTES:%d\x00", id))
		}
		// Not found, store as new object
		id := e.storeObject(v, "bytes")
		return Symbol(fmt.Sprintf("\x00BYTES:%d\x00", id))
	default:
		return value
	}
}

// storeObject stores an object in the global store with an initial refcount of 0
// Returns the object ID
func (e *Executor) storeObject(value interface{}, typeName string) int {
	e.mu.Lock()
	defer e.mu.Unlock()

	id := e.nextObjectID
	e.nextObjectID++

	e.storedObjects[id] = &StoredObject{
		Value:    value,
		Type:     typeName,
		RefCount: 0, // Start at 0 - creator must claim ownership
	}

	e.logger.DebugCat(CatMemory,"Stored object %d (type: %s, refcount: 0)", id, typeName)

	return id
}

// incrementObjectRefCount increments the reference count for an object
func (e *Executor) incrementObjectRefCount(objectID int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if obj, exists := e.storedObjects[objectID]; exists {
		obj.RefCount++
		e.logger.DebugCat(CatMemory,"Object %d refcount incremented to %d (type: %s)", objectID, obj.RefCount, obj.Type)
	} else {
		e.logger.WarnCat(CatMemory,"Attempted to increment refcount for non-existent object %d", objectID)
	}
}

// decrementObjectRefCount decrements the reference count and frees if zero
func (e *Executor) decrementObjectRefCount(objectID int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if obj, exists := e.storedObjects[objectID]; exists {
		obj.RefCount--
		e.logger.DebugCat(CatMemory,"Object %d refcount decremented to %d (type: %s)", objectID, obj.RefCount, obj.Type)

		if obj.RefCount <= 0 {
			// Before deleting, release nested references if it's a list
			if storedList, ok := obj.Value.(StoredList); ok {
				e.mu.Unlock() // Unlock before recursive calls
				// Release references from positional items
				for _, item := range storedList.Items() {
					releaseNestedReferences(item, e)
				}
				// Release references from named arguments (both keys and values)
				for key, val := range storedList.NamedArgs() {
					releaseNestedReferences(key, e)
					releaseNestedReferences(val, e)
				}
				e.mu.Lock() // Re-lock for deletion
			}

			// Transfer FinalBubbleMap and BubbleUpMap to orphanedBubbles if it's a fiber handle
			// This preserves bubbles from abandoned fibers for later retrieval
			if fiberHandle, ok := obj.Value.(*FiberHandle); ok {
				fiberHandle.mu.Lock()
				hasBubbles := len(fiberHandle.FinalBubbleMap) > 0 || len(fiberHandle.BubbleUpMap) > 0
				if hasBubbles {
					// Merge both maps into a combined map for orphaning
					combined := make(map[string][]*BubbleEntry)
					for flavor, entries := range fiberHandle.FinalBubbleMap {
						combined[flavor] = append(combined[flavor], entries...)
					}
					for flavor, entries := range fiberHandle.BubbleUpMap {
						combined[flavor] = append(combined[flavor], entries...)
					}
					// Clear the maps so we don't double-process
					fiberHandle.FinalBubbleMap = nil
					fiberHandle.BubbleUpMap = nil
					fiberHandle.mu.Unlock()
					// Add to orphaned bubbles (transfers ownership of references)
					e.mu.Unlock()
					e.AddOrphanedBubbles(combined)
					e.mu.Lock()
				} else {
					fiberHandle.mu.Unlock()
				}
			}

			delete(e.storedObjects, objectID)
			e.logger.DebugCat(CatMemory,"Object %d freed (refcount reached 0)", objectID)
		}
	} else {
		e.logger.WarnCat(CatMemory,"Attempted to decrement refcount for non-existent object %d", objectID)
	}
}

// getObject retrieves an object from the store without affecting refcount
func (e *Executor) getObject(objectID int) (interface{}, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if obj, exists := e.storedObjects[objectID]; exists {
		return obj.Value, true
	}
	return nil, false
}

// findStoredListID finds the ID of a StoredList by searching storedObjects
// Returns -1 if not found
func (e *Executor) findStoredListID(list StoredList) int {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Get all IDs in sorted order for deterministic iteration
	ids := make([]int, 0, len(e.storedObjects))
	for id := range e.storedObjects {
		ids = append(ids, id)
	}
	// Sort IDs to ensure deterministic iteration
	for i := 0; i < len(ids)-1; i++ {
		for j := i + 1; j < len(ids); j++ {
			if ids[i] > ids[j] {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}

	// Compare by checking if they share the same backing array
	for _, id := range ids {
		obj := e.storedObjects[id]
		if objList, ok := obj.Value.(StoredList); ok {
			// Two slices share backing array if they have same length and same first element address
			if len(objList.items) == len(list.items) {
				if len(objList.items) == 0 {
					// Both empty - match any empty list for now
					return id
				}
				// Check if they point to the same backing array
				if &objList.items[0] == &list.items[0] {
					return id
				}
			}
		}
	}

	return -1
}

// findStoredBytesID finds the ID of a StoredBytes by searching storedObjects
// Returns -1 if not found
func (e *Executor) findStoredBytesID(sb StoredBytes) int {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Compare by checking if they share the same backing array
	for id, obj := range e.storedObjects {
		if objBytes, ok := obj.Value.(StoredBytes); ok {
			if len(objBytes.data) == len(sb.data) {
				if len(objBytes.data) == 0 {
					// Both empty - match any empty bytes for now
					return id
				}
				// Check if they point to the same backing array
				if &objBytes.data[0] == &sb.data[0] {
					return id
				}
			}
		}
	}

	return -1
}

// findStoredChannelID finds the ID of a StoredChannel by searching storedObjects
// Returns -1 if not found
func (e *Executor) findStoredChannelID(ch *StoredChannel) int {
	if ch == nil {
		return -1
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	for id, obj := range e.storedObjects {
		if objCh, ok := obj.Value.(*StoredChannel); ok {
			// Compare by pointer identity
			if objCh == ch {
				return id
			}
		}
	}

	return -1
}

// findStoredFiberID finds the ID of a FiberHandle by searching storedObjects
// Returns -1 if not found
func (e *Executor) findStoredFiberID(fh *FiberHandle) int {
	if fh == nil {
		return -1
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	for id, obj := range e.storedObjects {
		if objFh, ok := obj.Value.(*FiberHandle); ok {
			// Compare by pointer identity
			if objFh == fh {
				return id
			}
		}
	}

	return -1
}

// findStoredFileID finds the ID of a StoredFile by searching storedObjects
// Returns -1 if not found
func (e *Executor) findStoredFileID(f *StoredFile) int {
	if f == nil {
		return -1
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	for id, obj := range e.storedObjects {
		if objF, ok := obj.Value.(*StoredFile); ok {
			// Compare by pointer identity
			if objF == f {
				return id
			}
		}
	}

	return -1
}
