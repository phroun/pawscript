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
	case StoredStruct:
		// StoredStruct objects - convert to marker
		if id := e.findStoredStructID(v); id >= 0 {
			return Symbol(fmt.Sprintf("\x00STRUCT:%d\x00", id))
		}
		// Not found, store as new object
		id := e.storeObject(v, "struct")
		return Symbol(fmt.Sprintf("\x00STRUCT:%d\x00", id))
	// Note: StructDef is now a StoredList, no special handling needed
	default:
		return value
	}
}

// RegisterObject stores a new object and returns its ObjectRef
// The object is stored with refcount=0 - caller must claim ownership
func (e *Executor) RegisterObject(value interface{}, objType ObjectType) ObjectRef {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Try to reuse a freed ID first
	var id int
	if len(e.freeIDs) > 0 {
		id = e.freeIDs[len(e.freeIDs)-1]
		e.freeIDs = e.freeIDs[:len(e.freeIDs)-1]
	} else {
		id = e.nextObjectID
		e.nextObjectID++
	}

	e.storedObjects[id] = &StoredObject{
		Value:    value,
		Type:     objType,
		RefCount: 0, // Start at 0 - creator must claim ownership
		Hash:     0, // Will be set by caller if deduplication is used
		Deleted:  false,
	}

	e.logger.DebugCat(CatMemory, "Stored object %d (type: %s, refcount: 0)", id, objType.String())

	return ObjectRef{Type: objType, ID: id}
}

// RefClaim increments the reference count for an object.
// Safe to call with zero-value ObjectRef (no-op).
// This is the ONLY function that should be used to claim object references.
func (e *Executor) RefClaim(ref ObjectRef) {
	if !ref.IsValid() {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if obj, exists := e.storedObjects[ref.ID]; exists {
		if obj.Deleted {
			e.logger.WarnCat(CatMemory, "Attempted to claim deleted object %d", ref.ID)
			return
		}
		obj.RefCount++
		e.logger.DebugCat(CatMemory, "Object %d refcount incremented to %d (type: %s)", ref.ID, obj.RefCount, ref.Type.String())
	} else {
		e.logger.WarnCat(CatMemory, "Attempted to claim non-existent object %d", ref.ID)
	}
}

// RefRelease decrements the reference count for an object.
// When refcount reaches 0, releases nested refs and marks for ID reuse.
// Safe to call with zero-value ObjectRef (no-op).
// This is the ONLY function that should be used to release object references.
func (e *Executor) RefRelease(ref ObjectRef) {
	if !ref.IsValid() {
		return
	}

	e.mu.Lock()

	obj, exists := e.storedObjects[ref.ID]
	if !exists {
		e.mu.Unlock()
		e.logger.WarnCat(CatMemory, "Attempted to release non-existent object %d", ref.ID)
		return
	}

	if obj.Deleted {
		e.mu.Unlock()
		e.logger.WarnCat(CatMemory, "Attempted to release already deleted object %d", ref.ID)
		return
	}

	obj.RefCount--
	e.logger.DebugCat(CatMemory, "Object %d refcount decremented to %d (type: %s)", ref.ID, obj.RefCount, ref.Type.String())

	if obj.RefCount <= 0 {
		// Mark as deleted and add ID to free list
		obj.Deleted = true
		e.freeIDs = append(e.freeIDs, ref.ID)

		// Remove from content hash if present
		if obj.Hash != 0 {
			delete(e.contentHash, obj.Hash)
		}

		// Collect nested refs to release (we'll release them after unlocking)
		var nestedRefs []ObjectRef

		switch ref.Type {
		case ObjList:
			if storedList, ok := obj.Value.(StoredList); ok {
				// Collect refs from positional items
				for _, item := range storedList.Items() {
					if itemRef, ok := item.(ObjectRef); ok {
						nestedRefs = append(nestedRefs, itemRef)
					}
				}
				// Collect refs from named arguments
				for _, val := range storedList.NamedArgs() {
					if valRef, ok := val.(ObjectRef); ok {
						nestedRefs = append(nestedRefs, valRef)
					}
				}
			}

		case ObjFiber:
			// Transfer bubbles to orphaned if present
			if fiberHandle, ok := obj.Value.(*FiberHandle); ok {
				fiberHandle.mu.Lock()
				hasBubbles := len(fiberHandle.FinalBubbleMap) > 0 || len(fiberHandle.BubbleUpMap) > 0
				if hasBubbles {
					combined := make(map[string][]*BubbleEntry)
					for flavor, entries := range fiberHandle.FinalBubbleMap {
						combined[flavor] = append(combined[flavor], entries...)
					}
					for flavor, entries := range fiberHandle.BubbleUpMap {
						combined[flavor] = append(combined[flavor], entries...)
					}
					fiberHandle.FinalBubbleMap = nil
					fiberHandle.BubbleUpMap = nil
					fiberHandle.mu.Unlock()
					e.mu.Unlock()
					e.AddOrphanedBubbles(combined)
					e.mu.Lock()
				} else {
					fiberHandle.mu.Unlock()
				}
			}

		case ObjFile:
			// Auto-close file handles
			if storedFile, ok := obj.Value.(*StoredFile); ok {
				e.logger.DebugCat(CatMemory, "Auto-closing file handle %s (refcount reached 0)", storedFile.Path)
				storedFile.Close()
			}

		case ObjBlock:
			// Clean up cached parsed form
			delete(e.blockCache, ref.ID)
			e.logger.DebugCat(CatMemory, "Removed block %d from parse cache", ref.ID)

		case ObjChannel:
			// TODO: Handle channel cleanup if needed
			_ = obj.Value // Silence unused warning
		}

		// Clear the value to help GC
		obj.Value = nil

		e.logger.DebugCat(CatMemory, "Object %d marked for reuse (type: %s)", ref.ID, ref.Type.String())

		// Release lock before releasing nested refs
		e.mu.Unlock()

		// Release nested refs outside the lock
		for _, nestedRef := range nestedRefs {
			e.RefRelease(nestedRef)
		}
		return
	}

	e.mu.Unlock()
}

// ============================================================================
// Object Constructors
// ============================================================================
// Each constructor creates an object, registers it, claims nested refs,
// and returns an ObjectRef with refcount=0. Caller must claim via SetVar/SetResult.

// NewList creates a new stored list and returns its ObjectRef.
// Claims nested refs for any ObjectRefs in items or namedArgs.
// Returns ObjectRef with refcount=0 - caller must claim ownership.
func (e *Executor) NewList(items []interface{}, namedArgs map[string]interface{}) ObjectRef {
	// Compute type info with executor for full marker resolution
	arrInfo := computeTypeInfoForSlice(items, e)
	mapInfo := computeTypeInfoForMap(namedArgs, e)

	list := StoredList{
		items:           items,
		namedArgs:       namedArgs,
		arrType:         arrInfo.Type,
		arrSolid:        arrInfo.Solid,
		arrSerializable: arrInfo.Serializable,
		mapType:         mapInfo.Type,
		mapSolid:        mapInfo.Solid,
		mapSerializable: mapInfo.Serializable,
	}

	ref := e.RegisterObject(list, ObjList)

	// Claim refs for any ObjectRefs in contents
	for _, item := range items {
		if objRef, ok := item.(ObjectRef); ok {
			e.RefClaim(objRef)
		}
	}
	for _, val := range namedArgs {
		if objRef, ok := val.(ObjectRef); ok {
			e.RefClaim(objRef)
		}
	}

	return ref
}

// NewString creates a new stored string and returns its ObjectRef.
// Returns ObjectRef with refcount=0 - caller must claim ownership.
func (e *Executor) NewString(s string) ObjectRef {
	return e.RegisterObject(StoredString(s), ObjString)
}

// NewBlock creates a new stored block and returns its ObjectRef.
// Returns ObjectRef with refcount=0 - caller must claim ownership.
func (e *Executor) NewBlock(s string) ObjectRef {
	return e.RegisterObject(StoredBlock(s), ObjBlock)
}

// NewBytes creates a new stored bytes object and returns its ObjectRef.
// Returns ObjectRef with refcount=0 - caller must claim ownership.
func (e *Executor) NewBytes(data []byte) ObjectRef {
	return e.RegisterObject(StoredBytes{data: data}, ObjBytes)
}

// NewChannel creates a new stored channel and returns its ObjectRef.
// Returns ObjectRef with refcount=0 - caller must claim ownership.
func (e *Executor) NewChannel(bufferSize int) ObjectRef {
	ch := NewStoredChannel(bufferSize)
	return e.RegisterObject(ch, ObjChannel)
}

// NewMacro creates a new stored macro and returns its ObjectRef.
// Returns ObjectRef with refcount=0 - caller must claim ownership.
func (e *Executor) NewMacro(macro StoredMacro) ObjectRef {
	return e.RegisterObject(macro, ObjMacro)
}

// NewCommand creates a new stored command and returns its ObjectRef.
// Returns ObjectRef with refcount=0 - caller must claim ownership.
func (e *Executor) NewCommand(cmd StoredCommand) ObjectRef {
	return e.RegisterObject(cmd, ObjCommand)
}

// NewFile creates a new stored file and returns its ObjectRef.
// Returns ObjectRef with refcount=0 - caller must claim ownership.
func (e *Executor) NewFile(file *StoredFile) ObjectRef {
	return e.RegisterObject(file, ObjFile)
}

// NewStruct creates a new stored struct and returns its ObjectRef.
// Returns ObjectRef with refcount=0 - caller must claim ownership.
func (e *Executor) NewStruct(ss StoredStruct) ObjectRef {
	return e.RegisterObject(ss, ObjStruct)
}

// NewStructArray creates a new stored struct array and returns its ObjectRef.
// Returns ObjectRef with refcount=0 - caller must claim ownership.
func (e *Executor) NewStructArray(ss StoredStruct) ObjectRef {
	return e.RegisterObject(ss, ObjStructArray)
}

// NewFiber creates a new stored fiber and returns its ObjectRef.
// Returns ObjectRef with refcount=0 - caller must claim ownership.
func (e *Executor) NewFiber(fh *FiberHandle) ObjectRef {
	return e.RegisterObject(fh, ObjFiber)
}

// ============================================================================
// Compatibility Shims (DEPRECATED)
// ============================================================================

// storeObject is a compatibility shim during migration.
// DEPRECATED: Use the typed constructors (NewList, NewString, etc.) instead.
func (e *Executor) storeObject(value interface{}, typeName string) int {
	objType := ObjectTypeFromString(typeName)
	ref := e.RegisterObject(value, objType)
	return ref.ID
}

// incrementObjectRefCount increments the reference count for an object
// DEPRECATED: Use RefClaim instead
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

			// Auto-close file handles when their last reference is released
			// This allows files to be passed between threads/macros without premature closing
			if storedFile, ok := obj.Value.(*StoredFile); ok {
				e.logger.DebugCat(CatMemory, "Auto-closing file handle %s (refcount reached 0)", storedFile.Path)
				storedFile.Close()
			}

			// Clean up cached parsed form for blocks
			if _, ok := obj.Value.(StoredBlock); ok {
				delete(e.blockCache, objectID)
				e.logger.DebugCat(CatMemory, "Removed block %d from parse cache", objectID)
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

	// Check if this is a truly empty list (no items AND no namedArgs)
	// If so, return the canonical empty list ID
	if len(list.items) == 0 && (list.namedArgs == nil || len(list.namedArgs) == 0) {
		return e.emptyListID
	}

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
					// Empty positional items but has namedArgs - can't match by pointer
					// since there's no items[0] to compare
					continue
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

// findStoredStructID finds the ID of a StoredStruct by searching storedObjects
// Returns -1 if not found
func (e *Executor) findStoredStructID(ss StoredStruct) int {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Compare by checking if they share the same backing array and have same offset/length
	for id, obj := range e.storedObjects {
		if objSS, ok := obj.Value.(StoredStruct); ok {
			if len(objSS.data) == len(ss.data) && objSS.offset == ss.offset && objSS.length == ss.length {
				if len(objSS.data) == 0 {
					// Both empty - match any empty struct for now
					return id
				}
				// Check if they point to the same backing array
				if &objSS.data[0] == &ss.data[0] {
					return id
				}
			}
		}
	}

	return -1
}

// Note: StructDef is now a StoredList, so findStructDefID is no longer needed
