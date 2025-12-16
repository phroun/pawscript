package pawscript

import (
	"fmt"
	"math"
)

// fillStructFromSource fills a struct from a source list using the definition list
func fillStructFromSource(s *StoredStruct, source interface{}, defList StoredList, ctx *Context) {
	sourceList, ok := source.(StoredList)
	if !ok {
		return
	}

	// Fill from named arguments
	if sourceList.NamedArgs() != nil {
		for name, value := range sourceList.NamedArgs() {
			resolved := ctx.executor.resolveValue(value)
			setStructFieldValue(s, name, resolved, defList, ctx)
		}
	}
}

// setStructFieldValue sets a field value in a struct using the definition list
func setStructFieldValue(s *StoredStruct, fieldName string, value interface{}, defList StoredList, ctx *Context) bool {
	// Look up field info from definition list
	defNamedArgs := defList.NamedArgs()
	if defNamedArgs == nil {
		return false
	}
	fieldInfoVal, hasField := defNamedArgs[fieldName]
	if !hasField {
		return false
	}

	// Resolve the field info list
	fieldInfo := ctx.executor.resolveValue(fieldInfoVal)
	fieldInfoList, ok := fieldInfo.(StoredList)
	if !ok {
		return false
	}

	// Field info format: [offset, length, mode, ...]
	if fieldInfoList.Len() < 3 {
		return false
	}

	offsetNum, _ := toNumber(fieldInfoList.Get(0))
	lengthNum, _ := toNumber(fieldInfoList.Get(1))
	modeVal := fieldInfoList.Get(2)

	fieldOffset := int(offsetNum)
	fieldLength := int(lengthNum)
	var fieldMode string
	switch m := modeVal.(type) {
	case string:
		fieldMode = m
	case QuotedString:
		fieldMode = string(m)
	case Symbol:
		fieldMode = string(m)
	default:
		fieldMode = fmt.Sprintf("%v", m)
	}

	switch fieldMode {
	case "bytes":
		switch v := value.(type) {
		case StoredBytes:
			s.SetBytesAt(fieldOffset, v.Data(), fieldLength)
		case []byte:
			s.SetBytesAt(fieldOffset, v, fieldLength)
		default:
			return false
		}
		return true

	case "string":
		var str string
		switch v := value.(type) {
		case string:
			str = v
		case QuotedString:
			str = string(v)
		case Symbol:
			str = string(v)
		default:
			str = fmt.Sprintf("%v", value)
		}
		copyLen := len(str)
		if copyLen > fieldLength {
			copyLen = fieldLength
		}
		s.SetBytesAt(fieldOffset, []byte(str[:copyLen]), fieldLength)
		s.ZeroPadAt(fieldOffset, copyLen, fieldLength)
		return true

	case "int", "int_be", "uint", "uint_be":
		// Write as big-endian integer (signed/unsigned handled identically for storage)
		numVal, ok := toNumber(value)
		if !ok {
			return false
		}
		intVal := int64(numVal)
		bytes := make([]byte, fieldLength)
		for i := fieldLength - 1; i >= 0; i-- {
			bytes[i] = byte(intVal & 0xFF)
			intVal >>= 8
		}
		s.SetBytesAt(fieldOffset, bytes, fieldLength)
		return true

	case "int_le", "uint_le":
		// Write as little-endian integer
		numVal, ok := toNumber(value)
		if !ok {
			return false
		}
		intVal := int64(numVal)
		bytes := make([]byte, fieldLength)
		for i := 0; i < fieldLength; i++ {
			bytes[i] = byte(intVal & 0xFF)
			intVal >>= 8
		}
		s.SetBytesAt(fieldOffset, bytes, fieldLength)
		return true

	case "float", "float_be":
		// Write as big-endian IEEE 754 float
		var floatVal float64
		switch v := value.(type) {
		case float64:
			floatVal = v
		case int64:
			floatVal = float64(v)
		case int:
			floatVal = float64(v)
		default:
			return false
		}
		var bytes []byte
		if fieldLength == 4 {
			bits := math.Float32bits(float32(floatVal))
			bytes = []byte{byte(bits >> 24), byte(bits >> 16), byte(bits >> 8), byte(bits)}
		} else if fieldLength == 8 {
			bits := math.Float64bits(floatVal)
			bytes = []byte{
				byte(bits >> 56), byte(bits >> 48), byte(bits >> 40), byte(bits >> 32),
				byte(bits >> 24), byte(bits >> 16), byte(bits >> 8), byte(bits),
			}
		} else {
			return false // unsupported float size
		}
		s.SetBytesAt(fieldOffset, bytes, fieldLength)
		return true

	case "float_le":
		// Write as little-endian IEEE 754 float
		var floatVal float64
		switch v := value.(type) {
		case float64:
			floatVal = v
		case int64:
			floatVal = float64(v)
		case int:
			floatVal = float64(v)
		default:
			return false
		}
		var bytes []byte
		if fieldLength == 4 {
			bits := math.Float32bits(float32(floatVal))
			bytes = []byte{byte(bits), byte(bits >> 8), byte(bits >> 16), byte(bits >> 24)}
		} else if fieldLength == 8 {
			bits := math.Float64bits(floatVal)
			bytes = []byte{
				byte(bits), byte(bits >> 8), byte(bits >> 16), byte(bits >> 24),
				byte(bits >> 32), byte(bits >> 40), byte(bits >> 48), byte(bits >> 56),
			}
		} else {
			return false // unsupported float size
		}
		s.SetBytesAt(fieldOffset, bytes, fieldLength)
		return true

	case "bit0", "bit1", "bit2", "bit3", "bit4", "bit5", "bit6", "bit7":
		// Set or clear specific bit (read-modify-write)
		// Extract bit number from mode name
		bitNum := int(fieldMode[3] - '0')
		mask := byte(1 << bitNum)

		// Determine boolean value
		var setBit bool
		switch v := value.(type) {
		case bool:
			setBit = v
		case int64:
			setBit = v != 0
		case int:
			setBit = v != 0
		case float64:
			setBit = v != 0
		case string:
			setBit = v == "true" || v == "1"
		case Symbol:
			setBit = string(v) == "true"
		default:
			return false
		}

		// Read current byte, modify bit, write back
		currentBytes, ok := s.GetBytesAt(fieldOffset, 1)
		if !ok {
			currentBytes = []byte{0}
		}
		currentByte := currentBytes[0]
		if setBit {
			currentByte |= mask // Set bit using OR
		} else {
			currentByte &^= mask // Clear bit using AND NOT
		}
		s.SetBytesAt(fieldOffset, []byte{currentByte}, 1)
		return true

	case "struct":
		// For nested structs, copy bytes directly
		switch v := value.(type) {
		case StoredStruct:
			s.SetBytesAt(fieldOffset, v.Data(), fieldLength)
		case StoredBytes:
			s.SetBytesAt(fieldOffset, v.Data(), fieldLength)
		default:
			return false
		}
		return true

	default:
		return false
	}
}
