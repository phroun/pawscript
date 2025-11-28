package pawscript

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
)

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
		executor:     nil,                 // Will be set when attached to executor
		fiberID:      0,                   // Main fiber
		moduleEnv:    NewModuleEnvironment(), // Create new module environment
	}
}

// NewExecutionStateFrom creates a child state inheriting from parent
func NewExecutionStateFrom(parent *ExecutionState) *ExecutionState {
	if parent == nil {
		return NewExecutionState()
	}

	return &ExecutionState{
		currentResult: nil,                             // Fresh result storage for this child
		hasResult:     false,                           // Child starts with no result
		lastStatus:    true,                            // Child starts with success status
		variables:     make(map[string]interface{}),    // Create fresh map
		ownedObjects:  make(map[int]int),               // Fresh owned objects counter
		executor:      parent.executor,                 // Share executor reference
		fiberID:       parent.fiberID,                  // Inherit fiber ID
		moduleEnv:     NewChildModuleEnvironment(parent.moduleEnv), // Create child module environment
	}
}

// NewExecutionStateFromSharedVars creates a child that shares variables but has its own result storage
// This is used for braces that need isolated result storage but shared variable scope
func NewExecutionStateFromSharedVars(parent *ExecutionState) *ExecutionState {
	if parent == nil {
		return NewExecutionState()
	}

	parent.mu.RLock()
	defer parent.mu.RUnlock()

	return &ExecutionState{
		currentResult:         parent.currentResult,         // Inherit parent's result for get_result
		hasResult:             parent.hasResult,             // Inherit parent's result state
		lastStatus:            parent.lastStatus,            // Inherit parent's last status
		lastBraceFailureCount: parent.lastBraceFailureCount, // Inherit parent's brace failure count for get_substatus
		variables:             parent.variables,             // Share the variables map with parent
		ownedObjects:          make(map[int]int),            // Fresh owned objects counter (will clean up separately)
		executor:              parent.executor,              // Share executor reference
		fiberID:               parent.fiberID,               // Inherit fiber ID
		moduleEnv:             parent.moduleEnv,             // Share module environment with parent
		macroContext:          parent.macroContext,          // Inherit macro context for stack traces
	}
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
		case []interface{}:
			// Convert raw slice to StoredList
			list := NewStoredList(v)
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
