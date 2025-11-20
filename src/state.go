package pawscript

import (
	"strconv"
	"strings"
	"sync"
)

// ExecutionState manages the result state during command execution
type ExecutionState struct {
	mu            sync.RWMutex
	currentResult interface{}
	hasResult     bool
	variables     map[string]interface{}
	ownedObjects  map[int]bool // Set of object IDs this context owns references to
	executor      *Executor    // Reference to executor for object management
}

// NewExecutionState creates a new execution state
func NewExecutionState() *ExecutionState {
	return &ExecutionState{
		hasResult:    false,
		variables:    make(map[string]interface{}),
		ownedObjects: make(map[int]bool),
		executor:     nil, // Will be set when attached to executor
	}
}

// NewExecutionStateFrom creates a child state inheriting from parent
func NewExecutionStateFrom(parent *ExecutionState) *ExecutionState {
	if parent == nil {
		return NewExecutionState()
	}

	return &ExecutionState{
		currentResult: nil,                  // Fresh result storage for this child
		hasResult:     false,                // Child starts with no result
		variables:     make(map[string]interface{}), // Create fresh map
		ownedObjects:  make(map[int]bool),   // Fresh owned objects set
		executor:      parent.executor,      // Share executor reference
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
		currentResult: parent.currentResult, // Inherit parent's result for get_result
		hasResult:     parent.hasResult,     // Inherit parent's result state
		variables:     parent.variables,     // Share the variables map with parent
		ownedObjects:  make(map[int]bool),   // Fresh owned objects set (will clean up separately)
		executor:      parent.executor,      // Share executor reference
	}
}

// SetResult sets the result value
func (s *ExecutionState) SetResult(value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Handle the special "undefined" bare identifier token (Symbol type only)
	// This allows clearing the result with the bare identifier: undefined
	// But still allows storing the string "undefined" as a type name
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
		return
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
	
	// Claim new references
	for _, id := range newRefs {
		s.ClaimObjectReference(id)
	}
	
	// Release old references
	for _, id := range oldRefs {
		s.ReleaseObjectReference(id)
	}
	
	// Re-acquire lock for defer
	s.mu.Lock()
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
	
	// Claim new references
	for _, id := range newRefs {
		s.ClaimObjectReference(id)
	}
	
	// Release old references
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
	defer s.mu.Unlock()

	if s.variables != nil {
		delete(s.variables, name)
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

	// Check if we already own this object
	if s.ownedObjects[objectID] {
		// Already owned - don't increment refcount again
		return
	}

	// Track locally
	s.ownedObjects[objectID] = true

	// Increment global refcount
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

	// Remove from local tracking
	delete(s.ownedObjects, objectID)

	// Decrement global refcount (may free the object)
	s.executor.decrementObjectRefCount(objectID)
}

// ReleaseAllReferences releases all owned object references
// Called when context ends (normally or via early return)
func (s *ExecutionState) ReleaseAllReferences() {
	if s.executor == nil {
		return
	}

	s.mu.Lock()
	ownedIDs := make([]int, 0, len(s.ownedObjects))
	for id := range s.ownedObjects {
		ownedIDs = append(ownedIDs, id)
	}
	s.ownedObjects = make(map[int]bool) // Clear the map
	s.mu.Unlock()

	// Release all references
	for _, id := range ownedIDs {
		s.executor.decrementObjectRefCount(id)
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
// Returns (type, id) for valid markers where type is "list", "string", "block", etc.
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
