package pawscript

// PSL (PawScript Serialized List) format helpers
// These functions use the existing PawScript parser and serialization,
// providing a simple interface without needing a full PawScript environment.
//
// PSL is an established serialization format and things outside this
// repository read and write it. The text is the contract: what these functions
// emit has to stay readable by those things, and what they accept has to keep
// accepting what those things write.
//
// A parse hands back everything the document holds. A PSL list carries an
// ordered sequence and a keyed map side by side, so a reader that can only say
// one of them discards the other, and discarding half a document is not a
// simplification -- it is a bug that reports success.

import (
	"fmt"
	"strconv"
	"strings"
)

// PSLMap holds a map of named items from a PSL structure
// Keys are strings, values can be strings, ints, floats, bools, nil, or nested structures
type PSLMap map[string]interface{}

// PSLConfig is an alias for PSLMap (deprecated name, use PSLMap)
type PSLConfig = PSLMap

// PSLList holds an ordered list of positional items
type PSLList []interface{}

// SerializePSL serializes a PSLMap to PSL format string
// Uses the same format as PawScript's list serialization
func SerializePSL(config PSLMap) string {
	if len(config) == 0 {
		return "()"
	}

	// Convert to named args map with PawScript values
	namedArgs := make(map[string]interface{})
	for key, value := range config {
		namedArgs[key] = convertToPawValue(value)
	}

	// Create StoredList with named args and use existing serialization
	list := NewStoredListWithNamed(nil, namedArgs)
	return formatListForDisplay(list)
}

// SerializePSLPretty serializes a PSLMap to PSL format with pretty indentation
// Each named argument on its own line for readability
func SerializePSLPretty(config PSLMap) string {
	if len(config) == 0 {
		return "()"
	}

	// Convert to named args map with PawScript values
	namedArgs := make(map[string]interface{})
	for key, value := range config {
		namedArgs[key] = convertToPawValue(value)
	}

	// Create StoredList with named args and use pretty serialization
	list := NewStoredListWithNamed(nil, namedArgs)
	return formatListForDisplayPretty(list, 0)
}

// SerializePSLList serializes a PSLList to PSL format string
func SerializePSLList(list PSLList) string {
	if len(list) == 0 {
		return "()"
	}

	// Convert to items slice with PawScript values
	items := make([]interface{}, len(list))
	for i, item := range list {
		items[i] = convertToPawValue(item)
	}

	// Create StoredList and use existing serialization
	storedList := NewStoredListWithoutRefs(items)
	return formatListForDisplay(storedList)
}

// convertToPawValue converts a Go value to a PawScript value
func convertToPawValue(value interface{}) interface{} {
	switch v := value.(type) {
	case nil:
		return nil
	case bool:
		return v
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return v
	case string:
		return QuotedString(v)
	case PSLConfig:
		namedArgs := make(map[string]interface{})
		for key, val := range v {
			namedArgs[key] = convertToPawValue(val)
		}
		return NewStoredListWithNamed(nil, namedArgs)
	case PSLList:
		items := make([]interface{}, len(v))
		for i, item := range v {
			items[i] = convertToPawValue(item)
		}
		return NewStoredListWithoutRefs(items)
	case map[string]interface{}:
		// A caller-built plain map is treated the same as a PSLMap so nested
		// structures serialize correctly instead of falling to the fmt.Sprintf
		// default (which would emit a bogus "map[...]" string).
		namedArgs := make(map[string]interface{})
		for key, val := range v {
			namedArgs[key] = convertToPawValue(val)
		}
		return NewStoredListWithNamed(nil, namedArgs)
	case []interface{}:
		// A caller-built plain slice is treated the same as a PSLList.
		items := make([]interface{}, len(v))
		for i, item := range v {
			items[i] = convertToPawValue(item)
		}
		return NewStoredListWithoutRefs(items)
	default:
		return QuotedString(fmt.Sprintf("%v", v))
	}
}

// ParsePSL parses a PSL document and keeps all of it: the ordered children,
// the keyed members, and both again at every depth.
//
//	n, err := ParsePSL(text)
//	n.Len(), n.Item(i), n.Child(i)   the ordered children
//	n.Get(key), n.Map()              the keyed members
//
// Map shares the node's storage rather than copying it, so the typed accessors
// written against PSLMap -- GetString, GetInt, GetItems and the rest -- reach a
// parsed document directly.
func ParsePSL(input string) (*PSLNode, error) {
	inner, ok, err := pslInner("PSL", input)
	if err != nil {
		return nil, err
	}
	if !ok {
		return NewPSLNode(), nil
	}
	args, namedArgs := parseArguments(inner)
	return newNodeFrom(args, namedArgs), nil
}

// convertFromPawValue converts a PawScript value back to a Go value
func convertFromPawValue(value interface{}) interface{} {
	switch v := value.(type) {
	case nil:
		return nil
	case bool:
		return v
	case int64:
		return v
	case float64:
		return v
	case int:
		return int64(v)
	case QuotedString:
		return string(v)
	case StoredString:
		return string(v)
	case Symbol:
		s := string(v)
		// Handle special symbols
		if s == "nil" {
			return nil
		}
		if s == "true" {
			return true
		}
		if s == "false" {
			return false
		}
		return s
	case string:
		return v
	case ParenGroup:
		// A nested list, which has both collections in it like any other, so it
		// comes back as a node rather than as whichever half is not empty.
		args, namedArgs := parseArguments(string(v))
		if len(args) == 0 && len(namedArgs) == 0 {
			return string(v) // not a list after all
		}
		return newNodeFrom(args, namedArgs)
	case StoredList:
		return newNodeFrom(v.Items(), v.NamedArgs())
	default:
		return fmt.Sprintf("%v", v)
	}
}

// GetString returns a string value from PSLMap, with default fallback
func (m PSLMap) GetString(key string, defaultVal string) string {
	if v, ok := m[key]; ok {
		switch val := v.(type) {
		case string:
			return val
		default:
			return fmt.Sprintf("%v", val)
		}
	}
	return defaultVal
}

// GetInt returns an int value from PSLMap, with default fallback
func (m PSLMap) GetInt(key string, defaultVal int) int {
	if v, ok := m[key]; ok {
		switch val := v.(type) {
		case int:
			return val
		case int64:
			return int(val)
		case float64:
			return int(val)
		case string:
			if i, err := strconv.Atoi(val); err == nil {
				return i
			}
		}
	}
	return defaultVal
}

// GetFloat returns a float64 value from PSLMap, with default fallback
func (m PSLMap) GetFloat(key string, defaultVal float64) float64 {
	if v, ok := m[key]; ok {
		switch val := v.(type) {
		case float64:
			return val
		case int:
			return float64(val)
		case int64:
			return float64(val)
		case string:
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				return f
			}
		}
	}
	return defaultVal
}

// GetBool returns a bool value from PSLMap, with default fallback
func (m PSLMap) GetBool(key string, defaultVal bool) bool {
	if v, ok := m[key]; ok {
		switch val := v.(type) {
		case bool:
			return val
		case string:
			return val == "true" || val == "1" || val == "yes"
		case int:
			return val != 0
		case int64:
			return val != 0
		}
	}
	return defaultVal
}

// GetItems returns the ordered children of a list value, or nil if there is no
// such member or it is not a list. A parsed list is a node; a hand-built one is
// a PSLList or a plain []interface{}; all three answer the same way.
func (m PSLMap) GetItems(key string) []interface{} {
	if v, ok := m[key]; ok {
		switch list := v.(type) {
		case *PSLNode:
			return list.Items
		case PSLList:
			return list
		case []interface{}:
			return list
		}
	}
	return nil
}

// GetMap returns the keyed members of a list value, or nil if there is no such
// member or it is not a list. A parsed list is a node; a hand-built one is a
// PSLMap or a plain map[string]interface{}; all three answer the same way.
func (m PSLMap) GetMap(key string) PSLMap {
	if v, ok := m[key]; ok {
		switch sub := v.(type) {
		case *PSLNode:
			return sub.Map()
		case PSLMap:
			return sub
		case map[string]interface{}:
			return PSLMap(sub)
		}
	}
	return nil
}

// Set sets a value in the PSLMap
func (m PSLMap) Set(key string, value interface{}) {
	m[key] = value
}

// PSLNode is a PSL list as it actually is: the ordered children in the order
// they appeared, and the keyed members beside them. It is what a parse returns,
// at the top level and at every depth, because it is the only Go shape that can
// say everything a PSL list holds.
//
//	(_hash: "deadbeef", ("first"), ("second"))
//
// PSLMap and PSLList are each one collection of that, and remain what a caller
// builds a document out of by hand: the serializer takes either.
//
// The two collections are independent. Keyed members occupy no positions, so
// Items is indexed by the items alone and adding a named member cannot shift a
// child's index. Nested lists are nodes in turn, so a document survives to any
// depth.
//
// Named is a plain map[string]interface{}, which is PSLMap's underlying type:
// PSLMap(node.Named) converts with no copy and brings the typed accessors
// (GetString, GetInt, GetItems and the rest) with it.
type PSLNode struct {
	Items []interface{}
	Named map[string]interface{}
}

// NewPSLNode returns an empty node with its map ready to write into.
func NewPSLNode() *PSLNode {
	return &PSLNode{Named: map[string]interface{}{}}
}

// Len is how many ordered children this node has.
func (n *PSLNode) Len() int {
	if n == nil {
		return 0
	}
	return len(n.Items)
}

// Item is the ordered child at index i, counting from zero, and whether there
// is one there. Indices address the items alone; a keyed member named "0" is a
// different thing in a different collection and does not shadow it.
func (n *PSLNode) Item(i int) (interface{}, bool) {
	if n == nil || i < 0 || i >= len(n.Items) {
		return nil, false
	}
	return n.Items[i], true
}

// Get is the keyed member under this key, and whether there is one.
func (n *PSLNode) Get(key string) (interface{}, bool) {
	if n == nil || n.Named == nil {
		return nil, false
	}
	v, ok := n.Named[key]
	return v, ok
}

// Child is the ordered child at index i when it is itself a list, which is
// what a document made of records asks for most.
func (n *PSLNode) Child(i int) (*PSLNode, bool) {
	v, ok := n.Item(i)
	if !ok {
		return nil, false
	}
	c, ok := v.(*PSLNode)
	return c, ok
}

// Map is this node's keyed members as a PSLMap, for the typed accessors
// already written against it. It shares the node's storage rather than copying
// it, so writing through the map writes through the node.
func (n *PSLNode) Map() PSLMap {
	if n == nil {
		return nil
	}
	return PSLMap(n.Named)
}

// SerializePSLNode writes a node back out as PSL.
//
// Ordered children keep their order. Keyed members are emitted first and in
// sorted order, which is the serializer's own long-standing behaviour: order
// among keyed members is not a thing PSL preserves, and nothing should depend
// on it.
func SerializePSLNode(n *PSLNode) string {
	if n == nil || (len(n.Items) == 0 && len(n.Named) == 0) {
		return "()"
	}
	return formatListForDisplay(nodeToStoredList(n))
}

// SerializePSLNodePretty is SerializePSLNode with each member on its own line.
func SerializePSLNodePretty(n *PSLNode) string {
	if n == nil || (len(n.Items) == 0 && len(n.Named) == 0) {
		return "()"
	}
	return formatListForDisplayPretty(nodeToStoredList(n), 0)
}

// pslInner strips the comments and the outer parentheses, and reports whether
// anything was left to parse. It is the front of ParsePSL, factored out so the
// node parser accepts exactly what the map parser accepts.
func pslInner(what, input string) (string, bool, error) {
	input = strings.TrimSpace(input)
	if input == "" || input == "()" {
		return "", false, nil
	}

	parser := NewParser(input, "")
	input = strings.TrimSpace(parser.RemoveComments(input))
	if input == "" || input == "()" {
		return "", false, nil
	}

	if !strings.HasPrefix(input, "(") || !strings.HasSuffix(input, ")") {
		return "", false, fmt.Errorf("%s must be enclosed in parentheses", what)
	}
	return input[1 : len(input)-1], true, nil
}

// newNodeFrom builds a node from one level of parsed arguments, carrying every
// nested list down as a node of its own.
func newNodeFrom(args []interface{}, namedArgs map[string]interface{}) *PSLNode {
	n := &PSLNode{Named: make(map[string]interface{}, len(namedArgs))}
	for _, arg := range args {
		n.Items = append(n.Items, convertFromPawValue(arg))
	}
	for key, value := range namedArgs {
		n.Named[key] = convertFromPawValue(value)
	}
	return n
}

// nodeToStoredList converts a node for the serializer, taking nodes itself and
// leaving every other value to the conversion PSLMap already uses.
func nodeToStoredList(n *PSLNode) StoredList {
	items := make([]interface{}, 0, len(n.Items))
	for _, item := range n.Items {
		items = append(items, nodeValueToPaw(item))
	}
	named := make(map[string]interface{}, len(n.Named))
	for key, value := range n.Named {
		named[key] = nodeValueToPaw(value)
	}
	if len(items) == 0 {
		items = nil
	}
	return NewStoredListWithNamed(items, named)
}

func nodeValueToPaw(value interface{}) interface{} {
	if n, ok := value.(*PSLNode); ok {
		return nodeToStoredList(n)
	}
	return convertToPawValue(value)
}
