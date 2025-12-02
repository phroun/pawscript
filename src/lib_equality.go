package pawscript

import (
	"fmt"
	"strings"
)

// deepEqual performs a deep comparison of two values with efficiency shortcuts.
// If both values are the same object marker (same ID), they're equal.
// Otherwise, resolve and compare recursively, bailing on first difference.
func deepEqual(a, b interface{}, executor *Executor) bool {
	// Quick shortcut: if both are symbols and identical (same object marker), they're equal
	if symA, okA := a.(Symbol); okA {
		if symB, okB := b.(Symbol); okB {
			if string(symA) == string(symB) {
				return true
			}
		}
	}

	// Resolve both values
	resolvedA := a
	resolvedB := b
	if executor != nil {
		resolvedA = executor.resolveValue(a)
		resolvedB = executor.resolveValue(b)
	}

	// Handle nil
	if resolvedA == nil && resolvedB == nil {
		return true
	}
	if resolvedA == nil || resolvedB == nil {
		return false
	}

	// Compare by type
	switch va := resolvedA.(type) {
	case bool:
		if vb, ok := resolvedB.(bool); ok {
			return va == vb
		}
		return false
	case int64:
		switch vb := resolvedB.(type) {
		case int64:
			return va == vb
		case int:
			return va == int64(vb)
		case float64:
			return float64(va) == vb
		}
		return false
	case int:
		switch vb := resolvedB.(type) {
		case int:
			return va == vb
		case int64:
			return int64(va) == vb
		case float64:
			return float64(va) == vb
		}
		return false
	case float64:
		switch vb := resolvedB.(type) {
		case float64:
			return va == vb
		case int64:
			return va == float64(vb)
		case int:
			return va == float64(vb)
		}
		return false
	case string:
		if vb, ok := resolvedB.(string); ok {
			return va == vb
		}
		// Compare with Symbol/QuotedString
		return fmt.Sprintf("%v", va) == fmt.Sprintf("%v", resolvedB)
	case Symbol:
		// Check for undefined
		if string(va) == "undefined" {
			if vb, ok := resolvedB.(Symbol); ok && string(vb) == "undefined" {
				return true
			}
			return false
		}
		return fmt.Sprintf("%v", va) == fmt.Sprintf("%v", resolvedB)
	case QuotedString:
		return fmt.Sprintf("%v", va) == fmt.Sprintf("%v", resolvedB)
	case StoredList:
		// Compare lists element by element (both positional and named)
		var listB StoredList
		switch vb := resolvedB.(type) {
		case StoredList:
			listB = vb
		case ParenGroup:
			// Parse ParenGroup to list
			items, _ := parseArguments(string(vb))
			listB = NewStoredList(items)
		default:
			return false
		}
		// Compare positional items
		itemsA := va.Items()
		itemsB := listB.Items()
		if len(itemsA) != len(itemsB) {
			return false
		}
		for i := range itemsA {
			if !deepEqual(itemsA[i], itemsB[i], executor) {
				return false
			}
		}
		// Compare named arguments
		namedA := va.NamedArgs()
		namedB := listB.NamedArgs()
		// Check for same number of named args
		lenA, lenB := 0, 0
		if namedA != nil {
			lenA = len(namedA)
		}
		if namedB != nil {
			lenB = len(namedB)
		}
		if lenA != lenB {
			return false
		}
		// Compare each named arg (must exist in both with equal values)
		if namedA != nil {
			for key, valA := range namedA {
				valB, exists := namedB[key]
				if !exists {
					return false
				}
				if !deepEqual(valA, valB, executor) {
					return false
				}
			}
		}
		return true
	case ParenGroup:
		// Parse and compare as list
		itemsA, _ := parseArguments(string(va))
		var itemsB []interface{}
		switch vb := resolvedB.(type) {
		case StoredList:
			itemsB = vb.Items()
		case ParenGroup:
			itemsB, _ = parseArguments(string(vb))
		default:
			return false
		}
		if len(itemsA) != len(itemsB) {
			return false
		}
		for i := range itemsA {
			if !deepEqual(itemsA[i], itemsB[i], executor) {
				return false
			}
		}
		return true
	}

	// Fallback: string comparison
	return fmt.Sprintf("%v", resolvedA) == fmt.Sprintf("%v", resolvedB)
}

// shallowEqualValue compares two values for shallow equality.
// For object-like types (StoredList, etc.), compares by identity (same underlying data).
// For scalars, compares by value.
func shallowEqualValue(a, b interface{}) bool {
	// Handle nil
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	// For StoredList, compare by slice identity (same underlying array)
	if listA, okA := a.(StoredList); okA {
		if listB, okB := b.(StoredList); okB {
			// Compare if both lists share the same underlying items slice
			// (they came from the same object, not just equal content)
			itemsA := listA.Items()
			itemsB := listB.Items()
			namedA := listA.NamedArgs()
			namedB := listB.NamedArgs()
			// Check if slices point to same underlying array (or both nil/empty)
			sameItems := (len(itemsA) == 0 && len(itemsB) == 0) ||
				(len(itemsA) > 0 && len(itemsB) > 0 && &itemsA[0] == &itemsB[0])
			sameNamed := (len(namedA) == 0 && len(namedB) == 0) ||
				(namedA != nil && namedB != nil && fmt.Sprintf("%p", namedA) == fmt.Sprintf("%p", namedB))
			return sameItems && sameNamed
		}
		return false
	}

	// For Symbol (possibly containing object marker), compare string value
	if symA, okA := a.(Symbol); okA {
		if symB, okB := b.(Symbol); okB {
			return string(symA) == string(symB)
		}
		return false
	}

	// For other types, compare by string representation
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// shallowEqual performs a shallow comparison of two values.
// For lists: compares positional items and named args by identity/value,
// but does NOT recurse into nested structures (nested lists compared by marker).
// For scalar values: compares directly like deepEqual.
func shallowEqual(a, b interface{}, executor *Executor) bool {
	// Quick shortcut: if both are symbols and identical (same object marker), they're equal
	if symA, okA := a.(Symbol); okA {
		if symB, okB := b.(Symbol); okB {
			if string(symA) == string(symB) {
				return true
			}
		}
	}

	// Resolve both values
	resolvedA := a
	resolvedB := b
	if executor != nil {
		resolvedA = executor.resolveValue(a)
		resolvedB = executor.resolveValue(b)
	}

	// Handle nil
	if resolvedA == nil && resolvedB == nil {
		return true
	}
	if resolvedA == nil || resolvedB == nil {
		return false
	}

	// Compare by type
	switch va := resolvedA.(type) {
	case bool:
		if vb, ok := resolvedB.(bool); ok {
			return va == vb
		}
		return false
	case int64:
		switch vb := resolvedB.(type) {
		case int64:
			return va == vb
		case int:
			return va == int64(vb)
		case float64:
			return float64(va) == vb
		}
		return false
	case int:
		switch vb := resolvedB.(type) {
		case int:
			return va == vb
		case int64:
			return int64(va) == vb
		case float64:
			return float64(va) == vb
		}
		return false
	case float64:
		switch vb := resolvedB.(type) {
		case float64:
			return va == vb
		case int64:
			return va == float64(vb)
		case int:
			return va == float64(vb)
		}
		return false
	case string:
		if vb, ok := resolvedB.(string); ok {
			return va == vb
		}
		return fmt.Sprintf("%v", va) == fmt.Sprintf("%v", resolvedB)
	case Symbol:
		if string(va) == "undefined" {
			if vb, ok := resolvedB.(Symbol); ok && string(vb) == "undefined" {
				return true
			}
			return false
		}
		return fmt.Sprintf("%v", va) == fmt.Sprintf("%v", resolvedB)
	case QuotedString:
		return fmt.Sprintf("%v", va) == fmt.Sprintf("%v", resolvedB)
	case StoredList:
		// Shallow comparison: compare members by identity (same object marker), not content
		var listB StoredList
		switch vb := resolvedB.(type) {
		case StoredList:
			listB = vb
		default:
			return false
		}
		// Compare positional items by identity (object markers must match, not just content)
		itemsA := va.Items()
		itemsB := listB.Items()
		if len(itemsA) != len(itemsB) {
			return false
		}
		for i := range itemsA {
			if !shallowEqualValue(itemsA[i], itemsB[i]) {
				return false
			}
		}
		// Compare named arguments by identity
		namedA := va.NamedArgs()
		namedB := listB.NamedArgs()
		lenA, lenB := 0, 0
		if namedA != nil {
			lenA = len(namedA)
		}
		if namedB != nil {
			lenB = len(namedB)
		}
		if lenA != lenB {
			return false
		}
		if namedA != nil {
			for key, valA := range namedA {
				valB, exists := namedB[key]
				if !exists {
					return false
				}
				if !shallowEqualValue(valA, valB) {
					return false
				}
			}
		}
		return true
	}

	// Fallback: string comparison
	return fmt.Sprintf("%v", resolvedA) == fmt.Sprintf("%v", resolvedB)
}

// findAllStringPositions finds all non-overlapping positions of substr in str
func findAllStringPositions(str, substr string) []int {
	var positions []int
	if substr == "" {
		return positions
	}
	start := 0
	for {
		idx := strings.Index(str[start:], substr)
		if idx == -1 {
			break
		}
		positions = append(positions, start+idx)
		start += idx + len(substr)
	}
	return positions
}
