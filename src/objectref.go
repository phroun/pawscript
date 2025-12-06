package pawscript

import (
	"fmt"
	"strings"
)

// ObjectType identifies the type of a stored object
type ObjectType int

const (
	ObjNone ObjectType = iota // Zero value - invalid/no reference
	ObjList
	ObjString
	ObjBlock
	ObjBytes
	ObjChannel
	ObjFiber
	ObjCommand
	ObjMacro
	ObjStruct
	ObjStructArray
	ObjFile
)

// String returns the string representation of an ObjectType
func (t ObjectType) String() string {
	switch t {
	case ObjNone:
		return "none"
	case ObjList:
		return "list"
	case ObjString:
		return "string"
	case ObjBlock:
		return "block"
	case ObjBytes:
		return "bytes"
	case ObjChannel:
		return "channel"
	case ObjFiber:
		return "fiber"
	case ObjCommand:
		return "command"
	case ObjMacro:
		return "macro"
	case ObjStruct:
		return "struct"
	case ObjStructArray:
		return "structarray"
	case ObjFile:
		return "file"
	default:
		return "unknown"
	}
}

// ObjectTypeFromString converts a string to ObjectType
func ObjectTypeFromString(s string) ObjectType {
	switch strings.ToLower(s) {
	case "list":
		return ObjList
	case "string", "str":
		return ObjString
	case "block":
		return ObjBlock
	case "bytes":
		return ObjBytes
	case "channel":
		return ObjChannel
	case "fiber":
		return ObjFiber
	case "command":
		return ObjCommand
	case "macro":
		return ObjMacro
	case "struct":
		return ObjStruct
	case "structarray":
		return ObjStructArray
	case "file":
		return ObjFile
	default:
		return ObjNone
	}
}

// ObjectRef is the internal representation of a reference to a stored object.
// It is lightweight (just type + ID) and passed around instead of marker strings.
type ObjectRef struct {
	Type ObjectType
	ID   int
}

// IsValid returns true if this is a valid object reference (not zero value)
func (ref ObjectRef) IsValid() bool {
	return ref.Type != ObjNone && ref.ID > 0
}

// ToMarker converts to the legacy marker string format.
// This should ONLY be used at code substitution boundaries.
func (ref ObjectRef) ToMarker() string {
	if !ref.IsValid() {
		return ""
	}
	return fmt.Sprintf("\x00%s:%d\x00", strings.ToUpper(ref.Type.String()), ref.ID)
}

// ParseObjectRef parses a legacy marker string into an ObjectRef.
// Returns zero-value ObjectRef if parsing fails.
func ParseObjectRef(marker string) ObjectRef {
	if len(marker) < 4 || marker[0] != '\x00' || marker[len(marker)-1] != '\x00' {
		return ObjectRef{}
	}

	inner := marker[1 : len(marker)-1]
	colonIdx := strings.Index(inner, ":")
	if colonIdx == -1 {
		return ObjectRef{}
	}

	typeName := inner[:colonIdx]
	var id int
	if _, err := fmt.Sscanf(inner[colonIdx+1:], "%d", &id); err != nil {
		return ObjectRef{}
	}

	objType := ObjectTypeFromString(typeName)
	if objType == ObjNone {
		return ObjectRef{}
	}

	return ObjectRef{Type: objType, ID: id}
}
