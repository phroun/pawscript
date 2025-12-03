package pawscript

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Object pools for reducing allocation overhead
var (
	executionStatePool = sync.Pool{
		New: func() interface{} {
			return &ExecutionState{}
		},
	}
	variablesMapPool = sync.Pool{
		New: func() interface{} {
			return make(map[string]interface{}, 8) // Pre-size for common case
		},
	}
	ownedObjectsMapPool = sync.Pool{
		New: func() interface{} {
			return make(map[int]int, 4)
		},
	}
	bubbleMapPool = sync.Pool{
		New: func() interface{} {
			return make(map[string][]*BubbleEntry, 2)
		},
	}
)

// BubbleEntry represents a single bubble in the bubble system
// Bubbles are out-of-band values that accumulate and "bubble up" through the call stack
type BubbleEntry struct {
	Content    interface{}   // The PawScript value
	Microtime  int64         // Microseconds since epoch when created
	Memo       string        // Optional memo string
	StackTrace []interface{} // Optional stack trace (nil if trace was false)
	Flavors    []string      // All flavors this bubble belongs to (for cross-referencing)
}

// ExecutionState manages the result state during command execution
type ExecutionState struct {
	mu                    sync.RWMutex
	currentResult         interface{}
	hasResult             bool
	lastStatus            bool // Tracks the status (success/failure) of the last command
	lastBraceFailureCount int  // Tracks how many brace expressions returned false in last command
	variables             map[string]interface{}
	ownedObjects          map[int]int          // Count of references this state owns for each object ID
	executor              *Executor            // Reference to executor for object management
	fiberID               int                  // ID of the fiber this state belongs to (0 for main)
	moduleEnv             *ModuleEnvironment   // Module environment for this state
	macroContext          *MacroContext        // Current macro context for stack traces
	bubbleMap             map[string][]*BubbleEntry // Map of flavor -> list of bubbles
	// InBraceExpression is true when executing inside a brace expression {...}
	// Commands can check this to return values instead of emitting side effects to #out
	InBraceExpression bool
}

// NewExecutionState creates a new execution state
func NewExecutionState() *ExecutionState {
	return &ExecutionState{
		hasResult:    false,
		lastStatus:   true, // Default to success
		variables:    make(map[string]interface{}),
		ownedObjects: make(map[int]int),
		executor:     nil,                             // Will be set when attached to executor
		fiberID:      0,                               // Main fiber
		moduleEnv:    NewModuleEnvironment(),          // Create new module environment
		bubbleMap:    make(map[string][]*BubbleEntry), // Fresh bubble map
	}
}

// NewExecutionStateFrom creates a child state inheriting from parent
// Used for macro calls - starts with fresh result and nil bubbleMap (lazy-created if needed)
func NewExecutionStateFrom(parent *ExecutionState) *ExecutionState {
	if parent == nil {
		return NewExecutionState()
	}

	// Get state from pool
	state := executionStatePool.Get().(*ExecutionState)

	// Get maps from pools (bubbleMap is nil - lazy-created only if AddBubble is called)
	variables := variablesMapPool.Get().(map[string]interface{})
	ownedObjects := ownedObjectsMapPool.Get().(map[int]int)

	// Initialize state
	state.currentResult = nil
	state.hasResult = false
	state.lastStatus = true
	state.lastBraceFailureCount = 0
	state.variables = variables
	state.ownedObjects = ownedObjects
	state.executor = parent.executor
	state.fiberID = parent.fiberID
	state.moduleEnv = NewChildModuleEnvironment(parent.moduleEnv)
	state.macroContext = nil
	state.bubbleMap = nil // Lazy-created on first AddBubble (rare)
	state.InBraceExpression = false

	return state
}

// NewExecutionStateFromSharedVars creates a child that shares variables but has its own result storage
// This is used for braces that need isolated result storage but shared variable scope
// Bubbles survive into and out of brace expressions (shared bubbleMap)
func NewExecutionStateFromSharedVars(parent *ExecutionState) *ExecutionState {
	if parent == nil {
		return NewExecutionState()
	}

	parent.mu.RLock()
	defer parent.mu.RUnlock()

	// Get state and ownedObjects map from pools
	state := executionStatePool.Get().(*ExecutionState)
	ownedObjects := ownedObjectsMapPool.Get().(map[int]int)

	// Initialize state - shares most things with parent
	state.currentResult = parent.currentResult
	state.hasResult = parent.hasResult
	state.lastStatus = parent.lastStatus
	state.lastBraceFailureCount = parent.lastBraceFailureCount
	state.variables = parent.variables // Shared with parent
	state.ownedObjects = ownedObjects
	state.executor = parent.executor
	state.fiberID = parent.fiberID
	state.moduleEnv = parent.moduleEnv // Shared with parent
	state.macroContext = parent.macroContext
	state.bubbleMap = parent.bubbleMap // Shared with parent
	state.InBraceExpression = true

	return state
}

// Recycle returns the state and its maps to the object pools for reuse.
// Call this when the state is no longer needed (e.g., after macro execution).
// ownsVariables indicates whether this state owns its variables map (true for macro states,
// false for brace states that share variables with parent).
// ownsBubbleMap indicates whether this state owns its bubbleMap (true for macro states,
// false for brace states that share bubbleMap with parent).
func (s *ExecutionState) Recycle(ownsVariables, ownsBubbleMap bool) {
	if s == nil {
		return
	}

	// Clear and return owned maps to pools
	if ownsVariables && s.variables != nil {
		// Clear the map
		for k := range s.variables {
			delete(s.variables, k)
		}
		variablesMapPool.Put(s.variables)
	}

	if s.ownedObjects != nil {
		// Clear the map
		for k := range s.ownedObjects {
			delete(s.ownedObjects, k)
		}
		ownedObjectsMapPool.Put(s.ownedObjects)
	}

	if ownsBubbleMap && s.bubbleMap != nil {
		// Clear the map
		for k := range s.bubbleMap {
			delete(s.bubbleMap, k)
		}
		bubbleMapPool.Put(s.bubbleMap)
	}

	// Clear references to help GC
	s.currentResult = nil
	s.variables = nil
	s.ownedObjects = nil
	s.executor = nil
	s.moduleEnv = nil
	s.macroContext = nil
	s.bubbleMap = nil

	// Return state to pool
	executionStatePool.Put(s)
}

// SetResult sets the result value
func (s *ExecutionState) SetResult(value interface{}) {
	s.mu.Lock()

	// Handle the special "undefined" bare identifier token (Symbol type only)
	if sym, ok := value.(Symbol); ok && string(sym) == "undefined" {
		// Release references in old result before clearing
		if s.hasResult {
			oldRefs := s.extractObjectReferencesLocked(s.currentResult)
			s.mu.Unlock()
			for _, id := range oldRefs {
				s.ReleaseObjectReference(id)
			}
			s.mu.Lock()
		}

		s.currentResult = nil
		s.hasResult = false
		s.mu.Unlock()
		return
	}

	// Convert raw objects to markers automatically
	// This handles cases where commands pass resolved objects through the system
	if s.executor != nil {
		switch v := value.(type) {
		case StoredList:
			// Find existing ID or store the list
			if id := s.executor.findStoredListID(v); id >= 0 {
				value = Symbol(fmt.Sprintf("\x00LIST:%d\x00", id))
			} else {
				// Not found, store it
				id := s.executor.storeObject(v, "list")
				value = Symbol(fmt.Sprintf("\x00LIST:%d\x00", id))
			}
		case *StoredChannel:
			// Find existing channel ID
			if id := s.executor.findStoredChannelID(v); id >= 0 {
				value = Symbol(fmt.Sprintf("\x00CHANNEL:%d\x00", id))
			} else {
				// Store the channel
				id := s.executor.storeObject(v, "channel")
				value = Symbol(fmt.Sprintf("\x00CHANNEL:%d\x00", id))
			}
		case *StoredFile:
			// Find existing file ID
			if id := s.executor.findStoredFileID(v); id >= 0 {
				value = Symbol(fmt.Sprintf("\x00FILE:%d\x00", id))
			} else {
				// Store the file
				id := s.executor.storeObject(v, "file")
				value = Symbol(fmt.Sprintf("\x00FILE:%d\x00", id))
			}
		case StoredBytes:
			// Find existing bytes ID
			if id := s.executor.findStoredBytesID(v); id >= 0 {
				value = Symbol(fmt.Sprintf("\x00BYTES:%d\x00", id))
			} else {
				// Store the bytes
				id := s.executor.storeObject(v, "bytes")
				value = Symbol(fmt.Sprintf("\x00BYTES:%d\x00", id))
			}
		case []interface{}:
			// Convert raw slice to StoredList
			list := NewStoredListWithoutRefs(v)
			id := s.executor.storeObject(list, "list")
			value = Symbol(fmt.Sprintf("\x00LIST:%d\x00", id))
		}
	}

	// Check if large strings/blocks should be stored
	if s.executor != nil {
		s.mu.Unlock()
		value = s.executor.maybeStoreValue(value, s)
		s.mu.Lock()
	}

	// Extract object references from old and new values
	var oldRefs, newRefs []int
	if s.hasResult {
		oldRefs = s.extractObjectReferencesLocked(s.currentResult)
	}
	newRefs = s.extractObjectReferencesLocked(value)

	// Set new value
	s.currentResult = value
	s.hasResult = true
	
	// Release lock before doing reference management
	s.mu.Unlock()
	
	// Claim new references (once per occurrence)
	for _, id := range newRefs {
		s.ClaimObjectReference(id)
	}
	
	// Release old references (once per occurrence)
	for _, id := range oldRefs {
		s.ReleaseObjectReference(id)
	}
	
	// Re-acquire lock for return
	s.mu.Lock()
	defer s.mu.Unlock()
}

// SetResultWithoutClaim sets result without claiming ownership
// Used when transferring ownership from child contexts
func (s *ExecutionState) SetResultWithoutClaim(value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Release old result's references
	if s.hasResult {
		oldRefs := s.extractObjectReferencesLocked(s.currentResult)
		s.mu.Unlock()
		for _, id := range oldRefs {
			s.ReleaseObjectReference(id)
		}
		s.mu.Lock()
	}

	// Set new value WITHOUT claiming
	s.currentResult = value
	s.hasResult = true
}

// extractObjectReferencesLocked is like ExtractObjectReferences but assumes lock is held
func (s *ExecutionState) extractObjectReferencesLocked(value interface{}) []int {
	var refs []int

	switch v := value.(type) {
	case Symbol:
		if _, id := parseObjectMarker(string(v)); id >= 0 {
			refs = append(refs, id)
		}
	case string:
		if _, id := parseObjectMarker(v); id >= 0 {
			refs = append(refs, id)
		}
	case StoredList:
		// The list itself is a stored object - find its ID
		if s.executor != nil {
			if id := s.executor.findStoredListID(v); id >= 0 {
				refs = append(refs, id)
			}
		}
	case StoredBytes:
		// The bytes object is stored - find its ID
		if s.executor != nil {
			if id := s.executor.findStoredBytesID(v); id >= 0 {
				refs = append(refs, id)
			}
		}
	}

	return refs
}

// GetResult returns the current result value
func (s *ExecutionState) GetResult() interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentResult
}

// HasResult checks if a result value exists
func (s *ExecutionState) HasResult() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hasResult
}

// ClearResult clears the result value
func (s *ExecutionState) ClearResult() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentResult = nil
	s.hasResult = false
}

// GetLastStatus returns the last command status
func (s *ExecutionState) GetLastStatus() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastStatus
}

// SetLastStatus sets the last command status
func (s *ExecutionState) SetLastStatus(status bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastStatus = status
}

// GetLastBraceFailureCount returns the count of brace expressions that returned false in the last command
func (s *ExecutionState) GetLastBraceFailureCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastBraceFailureCount
}

// SetLastBraceFailureCount sets the count of brace expressions that returned false
func (s *ExecutionState) SetLastBraceFailureCount(count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastBraceFailureCount = count
}

// CreateChild creates a child state that inherits current result
func (s *ExecutionState) CreateChild() *ExecutionState {
	return NewExecutionStateFrom(s)
}

// Snapshot returns a snapshot of the current state
func (s *ExecutionState) Snapshot() (interface{}, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentResult, s.hasResult
}

// String returns a string representation for debugging
func (s *ExecutionState) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.hasResult {
		return "ExecutionState(has result)"
	}
	return "ExecutionState(no result)"
}

// SetVariable sets a variable in the current scope
func (s *ExecutionState) SetVariable(name string, value interface{}) {
	s.mu.Lock()
	
	if s.variables == nil {
		s.variables = make(map[string]interface{})
	}
	
	// Check if large strings/blocks should be stored
	if s.executor != nil {
		value = s.executor.maybeStoreValue(value, s)
	}
	
	// Extract object references from old and new values
	var oldRefs, newRefs []int
	if oldValue, exists := s.variables[name]; exists {
		oldRefs = s.extractObjectReferencesLocked(oldValue)
	}
	newRefs = s.extractObjectReferencesLocked(value)
	
	// Set new value
	s.variables[name] = value
	
	// Release lock before doing reference management
	s.mu.Unlock()
	
	// Claim new references (once per occurrence)
	for _, id := range newRefs {
		s.ClaimObjectReference(id)
	}
	
	// Release old references (once per occurrence, not all state claims)
	for _, id := range oldRefs {
		s.ReleaseObjectReference(id)
	}
}

// GetVariable gets a variable from the current scope
func (s *ExecutionState) GetVariable(name string) (interface{}, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.variables == nil {
		return nil, false
	}

	val, exists := s.variables[name]
	return val, exists
}

// DeleteVariable removes a variable from the current scope
func (s *ExecutionState) DeleteVariable(name string) {
	s.mu.Lock()

	if s.variables == nil {
		s.mu.Unlock()
		return
	}

	// Extract object references from the value being deleted
	var oldRefs []int
	if oldValue, exists := s.variables[name]; exists {
		oldRefs = s.extractObjectReferencesLocked(oldValue)
	}

	delete(s.variables, name)

	// Release lock before doing reference management
	s.mu.Unlock()

	// Release old references
	for _, id := range oldRefs {
		s.ReleaseObjectReference(id)
	}
}

// ClaimObjectReference takes ownership of an object reference
// This increments the reference count in the global store and tracks it locally
// If this state already owns the object, this is a no-op (idempotent)
func (s *ExecutionState) ClaimObjectReference(objectID int) {
	if s.executor == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Increment local count for tracking how many times this state owns it
	s.ownedObjects[objectID]++
	
	// Always increment global refcount for each claim
	// This makes global refcount = total references across all states
	s.executor.incrementObjectRefCount(objectID)
}

// ReleaseObjectReference releases ownership of an object reference
// This decrements the reference count and may free the object
func (s *ExecutionState) ReleaseObjectReference(objectID int) {
	if s.executor == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if we own this object
	count := s.ownedObjects[objectID]
	if count == 0 {
		return // Not owned, nothing to release
	}

	// Decrement local count
	s.ownedObjects[objectID]--
	
	// Remove from map if count reaches zero
	if s.ownedObjects[objectID] == 0 {
		delete(s.ownedObjects, objectID)
	}
	
	// Always decrement global refcount for each release
	s.executor.decrementObjectRefCount(objectID)
}

// ReleaseAllReferences releases all owned object references
// Called when context ends (normally or via early return)
func (s *ExecutionState) ReleaseAllReferences() {
	if s.executor == nil {
		return
	}

	s.mu.Lock()
	ownedIDs := make(map[int]int, len(s.ownedObjects))
	for id, count := range s.ownedObjects {
		ownedIDs[id] = count
	}
	s.ownedObjects = make(map[int]int) // Clear the map
	s.mu.Unlock()

	// Release all references (each ID may have multiple counts)
	for id, count := range ownedIDs {
		for i := 0; i < count; i++ {
			s.executor.decrementObjectRefCount(id)
		}
	}
}

// ExtractObjectReferences scans a value for object markers and returns their IDs
func (s *ExecutionState) ExtractObjectReferences(value interface{}) []int {
	var refs []int
	
	switch v := value.(type) {
	case Symbol:
		if _, id := parseObjectMarker(string(v)); id >= 0 {
			refs = append(refs, id)
		}
	case string:
		if _, id := parseObjectMarker(v); id >= 0 {
			refs = append(refs, id)
		}
	case StoredList:
		// The list itself is a stored object - find its ID
		if s.executor != nil {
			if id := s.executor.findStoredListID(v); id >= 0 {
				refs = append(refs, id)
			}
		}
	}
	
	return refs
}

// parseObjectMarker extracts object type and ID from markers like \x00TYPE:123\x00
// Returns ("", -1) if not a valid object marker
// Returns (type, id) for valid markers where type is "list", "str", "block", etc. (lowercase)
func parseObjectMarker(s string) (string, int) {
	if !strings.HasPrefix(s, "\x00") || !strings.HasSuffix(s, "\x00") {
		return "", -1
	}
	
	// Extract the middle part (e.g., "LIST:123")
	middle := s[1 : len(s)-1]
	
	// Split on colon
	parts := strings.SplitN(middle, ":", 2)
	if len(parts) != 2 {
		return "", -1
	}
	
	markerType := strings.ToLower(parts[0])
	idStr := parts[1]
	
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return "", -1
	}
	
	return markerType, id
}

// AddBubble adds a new bubble entry to the bubble map
// If trace is true, includes the current stack trace
func (s *ExecutionState) AddBubble(flavor string, content interface{}, trace bool, memo string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.bubbleMap == nil {
		s.bubbleMap = make(map[string][]*BubbleEntry)
	}

	// Build stack trace if requested
	var stackTrace []interface{}
	if trace && s.macroContext != nil {
		for mc := s.macroContext; mc != nil; mc = mc.ParentMacro {
			frame := map[string]interface{}{
				"macro":    mc.MacroName,
				"file":     mc.InvocationFile,
				"line":     int64(mc.InvocationLine),
				"column":   int64(mc.InvocationColumn),
				"def_file": mc.DefinitionFile,
				"def_line": int64(mc.DefinitionLine),
			}
			stackTrace = append(stackTrace, frame)
		}
	}

	entry := &BubbleEntry{
		Content:    content,
		Microtime:  time.Now().UnixMicro(),
		Memo:       memo,
		StackTrace: stackTrace,
		Flavors:    []string{flavor}, // Single flavor for this entry
	}

	s.bubbleMap[flavor] = append(s.bubbleMap[flavor], entry)
}

// AddBubbleMultiFlavor adds the SAME bubble entry to multiple flavors
// This allows the same entry to be found under different flavor keys,
// enabling efficient "burst" operations that can remove from all related flavors
func (s *ExecutionState) AddBubbleMultiFlavor(flavors []string, content interface{}, trace bool, memo string) {
	if len(flavors) == 0 {
		return
	}

	// For single flavor, delegate to AddBubble
	if len(flavors) == 1 {
		s.AddBubble(flavors[0], content, trace, memo)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.bubbleMap == nil {
		s.bubbleMap = make(map[string][]*BubbleEntry)
	}

	// Build stack trace if requested
	var stackTrace []interface{}
	if trace && s.macroContext != nil {
		for mc := s.macroContext; mc != nil; mc = mc.ParentMacro {
			frame := map[string]interface{}{
				"macro":    mc.MacroName,
				"file":     mc.InvocationFile,
				"line":     int64(mc.InvocationLine),
				"column":   int64(mc.InvocationColumn),
				"def_file": mc.DefinitionFile,
				"def_line": int64(mc.DefinitionLine),
			}
			stackTrace = append(stackTrace, frame)
		}
	}

	// Create ONE entry and add it to ALL flavors (shared reference)
	// Copy flavors slice to avoid external modification
	flavorsCopy := make([]string, len(flavors))
	copy(flavorsCopy, flavors)

	entry := &BubbleEntry{
		Content:    content,
		Microtime:  time.Now().UnixMicro(),
		Memo:       memo,
		StackTrace: stackTrace,
		Flavors:    flavorsCopy, // All flavors this entry belongs to
	}

	for _, flavor := range flavors {
		s.bubbleMap[flavor] = append(s.bubbleMap[flavor], entry)
	}
}

// MergeBubbles merges bubbles from a child state into this state
// Called when a macro returns to concatenate child's bubbles onto parent's
func (s *ExecutionState) MergeBubbles(child *ExecutionState) {
	if child == nil {
		return
	}

	// Fast path: if child's bubbleMap is nil, nothing to merge (no lock needed)
	if child.bubbleMap == nil {
		return
	}

	child.mu.RLock()
	childBubbles := child.bubbleMap
	child.mu.RUnlock()

	if len(childBubbles) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.bubbleMap == nil {
		s.bubbleMap = make(map[string][]*BubbleEntry)
	}

	// Concatenate each flavor's entries
	for flavor, entries := range childBubbles {
		s.bubbleMap[flavor] = append(s.bubbleMap[flavor], entries...)
	}
}

// GetBubbleMap returns a copy of the bubble map for inspection
func (s *ExecutionState) GetBubbleMap() map[string][]*BubbleEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.bubbleMap == nil {
		return nil
	}

	// Return a shallow copy
	result := make(map[string][]*BubbleEntry, len(s.bubbleMap))
	for k, v := range s.bubbleMap {
		result[k] = v
	}
	return result
}

// RemoveBubble removes a bubble entry from ALL flavors it belongs to
// Uses the Flavors field on the entry to find all flavor lists to remove from
func (s *ExecutionState) RemoveBubble(entry *BubbleEntry) {
	if entry == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.bubbleMap == nil {
		return
	}

	// Remove from all flavors listed in the entry
	for _, flavor := range entry.Flavors {
		entries := s.bubbleMap[flavor]
		if entries == nil {
			continue
		}

		// Find and remove the entry (by pointer equality)
		newEntries := make([]*BubbleEntry, 0, len(entries))
		for _, e := range entries {
			if e != entry {
				newEntries = append(newEntries, e)
			}
		}

		if len(newEntries) == 0 {
			delete(s.bubbleMap, flavor)
		} else {
			s.bubbleMap[flavor] = newEntries
		}
	}
}

// GetBubblesForFlavors returns all unique bubbles from the specified flavors,
// sorted by microtime (oldest first). Duplicates are removed.
func (s *ExecutionState) GetBubblesForFlavors(flavors []string) []*BubbleEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.bubbleMap == nil || len(flavors) == 0 {
		return nil
	}

	// Use a map to track seen entries (by pointer) to avoid duplicates
	seen := make(map[*BubbleEntry]bool)
	var allEntries []*BubbleEntry

	for _, flavor := range flavors {
		entries := s.bubbleMap[flavor]
		for _, entry := range entries {
			if !seen[entry] {
				seen[entry] = true
				allEntries = append(allEntries, entry)
			}
		}
	}

	// Sort by microtime (oldest first)
	sort.Slice(allEntries, func(i, j int) bool {
		return allEntries[i].Microtime < allEntries[j].Microtime
	})

	return allEntries
}

// GetAllFlavorNames returns all flavor names that currently have bubbles
func (s *ExecutionState) GetAllFlavorNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.bubbleMap == nil {
		return nil
	}

	names := make([]string, 0, len(s.bubbleMap))
	for name := range s.bubbleMap {
		names = append(names, name)
	}
	return names
}
