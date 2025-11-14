package pawscript

import "sync"

// ExecutionState manages the result state during command execution
type ExecutionState struct {
	mu            sync.RWMutex
	currentResult interface{}
	hasResult     bool
}

// NewExecutionState creates a new execution state
func NewExecutionState() *ExecutionState {
	return &ExecutionState{
		hasResult: false,
	}
}

// NewExecutionStateFrom creates a child state inheriting from parent
func NewExecutionStateFrom(parent *ExecutionState) *ExecutionState {
	if parent == nil {
		return NewExecutionState()
	}
	
	parent.mu.RLock()
	defer parent.mu.RUnlock()
	
	return &ExecutionState{
		currentResult: parent.currentResult,
		hasResult:     parent.hasResult,
	}
}

// SetResult sets the result value
func (s *ExecutionState) SetResult(value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	// Handle the special "undefined" bare identifier token
	if str, ok := value.(string); ok && str == "undefined" {
		s.currentResult = nil
		s.hasResult = false
		return
	}
	
	s.currentResult = value
	s.hasResult = true
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
