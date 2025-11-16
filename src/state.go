package pawscript

import "sync"

// ExecutionState manages the result state during command execution
type ExecutionState struct {
	mu            sync.RWMutex
	currentResult interface{}
	hasResult     bool
	variables     map[string]interface{}
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
		variables:     parent.variables, // Share the same variables map
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

// SetVariable sets a variable in the current scope
func (s *ExecutionState) SetVariable(name string, value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if s.variables == nil {
		s.variables = make(map[string]interface{})
	}
	s.variables[name] = value
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
