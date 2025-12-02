package pawscript

import (
	"fmt"
)

// RegisterBitwiseLib registers bitwise operation commands
// Module: bitwise
func (ps *PawScript) RegisterBitwiseLib() {
	// Helper function to set a StoredBytes as result with proper reference counting
	setBytesResult := func(ctx *Context, bytes StoredBytes) {
		id := ctx.executor.storeObject(bytes, "bytes")
		marker := fmt.Sprintf("\x00BYTES:%d\x00", id)
		ctx.state.SetResultWithoutClaim(Symbol(marker))
	}

	// Helper function to set a StoredList as result with proper reference counting
	setListResult := func(ctx *Context, list StoredList) {
		id := ctx.executor.storeObject(list, "list")
		marker := fmt.Sprintf("\x00LIST:%d\x00", id)
		ctx.state.SetResultWithoutClaim(Symbol(marker))
	}

	// Helper to extract int64 or bytes from argument
	extractOperand := func(ctx *Context, arg interface{}) (isBytes bool, intVal int64, bytesVal StoredBytes, ok bool) {
		resolved := ctx.executor.resolveValue(arg)
		switch v := resolved.(type) {
		case int64:
			return false, v, StoredBytes{}, true
		case int:
			return false, int64(v), StoredBytes{}, true
		case float64:
			return false, int64(v), StoredBytes{}, true
		case StoredBytes:
			return true, 0, v, true
		default:
			// Try to parse as number
			if num, numOk := toNumber(resolved); numOk {
				return false, int64(num), StoredBytes{}, true
			}
			return false, 0, StoredBytes{}, false
		}
	}

	// Helper to apply binary operation to bytes with alignment and repeat options
	applyBytesOp := func(a, b StoredBytes, op func(byte, byte) byte, align string, repeat bool) StoredBytes {
		aData := a.Data()
		bData := b.Data()
		result := make([]byte, len(aData))
		copy(result, aData)

		if len(bData) == 0 {
			return NewStoredBytes(result)
		}

		if align == "left" {
			// Left-aligned: start from index 0
			if repeat {
				for i := 0; i < len(result); i++ {
					result[i] = op(result[i], bData[i%len(bData)])
				}
			} else {
				limit := len(bData)
				if limit > len(result) {
					limit = len(result)
				}
				for i := 0; i < limit; i++ {
					result[i] = op(result[i], bData[i])
				}
				// Non-overlapping bytes: apply op with 0
				for i := limit; i < len(result); i++ {
					result[i] = op(result[i], 0)
				}
			}
		} else {
			// Right-aligned (default): align at the end
			if repeat {
				// Repeat from the right end backwards
				for i := len(result) - 1; i >= 0; i-- {
					bIdx := (len(bData) - 1) - ((len(result) - 1 - i) % len(bData))
					result[i] = op(result[i], bData[bIdx])
				}
			} else {
				// Align at right end
				offset := len(result) - len(bData)
				if offset < 0 {
					// b is longer than a, use rightmost portion of b
					bOffset := -offset
					for i := 0; i < len(result); i++ {
						result[i] = op(result[i], bData[bOffset+i])
					}
				} else {
					// a is longer than or equal to b
					// Non-overlapping bytes on left: apply op with 0
					for i := 0; i < offset; i++ {
						result[i] = op(result[i], 0)
					}
					// Overlapping bytes
					for i := 0; i < len(bData); i++ {
						result[offset+i] = op(result[offset+i], bData[i])
					}
				}
			}
		}
		return NewStoredBytes(result)
	}

	// Helper to apply operation when first arg is int64 and second is bytes or int
	// Detection: XOR: 1^1=0, OR: 1|0=1, AND: 1&0=0
	applyIntOp := func(a int64, b interface{}, op func(byte, byte) byte, align string, repeat bool, ctx *Context) int64 {
		resolved := ctx.executor.resolveValue(b)

		// Determine which operation based on byte op behavior
		isXor := op(1, 1) == 0      // XOR: 1^1=0
		isOr := op(1, 0) == 1       // OR: 1|0=1 (and not XOR)
		// isAnd is the default

		applyOp := func(aVal, bVal int64) int64 {
			if isXor {
				return aVal ^ bVal
			}
			if isOr {
				return aVal | bVal
			}
			return aVal & bVal
		}

		switch bv := resolved.(type) {
		case int64:
			return applyOp(a, bv)
		case int:
			return applyOp(a, int64(bv))
		case float64:
			return applyOp(a, int64(bv))
		case StoredBytes:
			// Convert bytes to int64 (up to 8 bytes, big-endian)
			bData := bv.Data()
			var bInt int64
			for i := 0; i < len(bData) && i < 8; i++ {
				bInt = (bInt << 8) | int64(bData[i])
			}
			return applyOp(a, bInt)
		default:
			if num, ok := toNumber(resolved); ok {
				return applyOp(a, int64(num))
			}
			return a
		}
	}

	// Helper to convert to list if possible (handles StoredList and ParenGroup)
	toListIfPossible := func(val interface{}) (StoredList, bool) {
		switch v := val.(type) {
		case StoredList:
			return v, true
		case ParenGroup:
			items, _ := parseArguments(string(v))
			return NewStoredList(items), true
		}
		return StoredList{}, false
	}

	// Helper to process list argument (apply op to each element)
	processListArg := func(ctx *Context, list StoredList, processOne func(interface{}) (interface{}, bool)) (StoredList, bool) {
		items := list.Items()
		results := make([]interface{}, len(items))
		for i, item := range items {
			result, ok := processOne(item)
			if !ok {
				return StoredList{}, false
			}
			results[i] = result
		}
		return NewStoredList(results), true
	}

	// bitwise_and - bitwise AND operation
	ps.RegisterCommandInModule("bitwise", "bitwise_and", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: bitwise_and <value1>, <value2> [align: left|right] [repeat: true|false]")
			return BoolStatus(false)
		}

		align := "right"
		repeat := false
		if v, ok := ctx.NamedArgs["align"]; ok {
			align = fmt.Sprintf("%v", v)
		}
		if v, ok := ctx.NamedArgs["repeat"]; ok {
			repeat = isTruthy(v)
		}

		andOp := func(a, b byte) byte { return a & b }

		// Check if first arg is a list
		firstResolved := ctx.executor.resolveValue(ctx.Args[0])
		if list, ok := toListIfPossible(firstResolved); ok {
			resultList, success := processListArg(ctx, list, func(item interface{}) (interface{}, bool) {
				isBytes, intVal, bytesVal, ok := extractOperand(ctx, item)
				if !ok {
					return nil, false
				}
				if isBytes {
					isBytes2, intVal2, bytesVal2, ok2 := extractOperand(ctx, ctx.Args[1])
					if !ok2 {
						return nil, false
					}
					if isBytes2 {
						return applyBytesOp(bytesVal, bytesVal2, andOp, align, repeat), true
					}
					// Second arg is int - convert to single byte if repeat, else to bytes
					if repeat {
						singleByte := NewStoredBytes([]byte{byte(intVal2 & 0xFF)})
						return applyBytesOp(bytesVal, singleByte, andOp, align, true), true
					}
					// Convert int to bytes (big-endian, minimal length)
					var intBytes []byte
					temp := intVal2
					if temp == 0 {
						intBytes = []byte{0}
					} else {
						for temp != 0 && temp != -1 {
							intBytes = append([]byte{byte(temp & 0xFF)}, intBytes...)
							temp >>= 8
						}
					}
					return applyBytesOp(bytesVal, NewStoredBytes(intBytes), andOp, align, repeat), true
				}
				// First arg is int
				return applyIntOp(intVal, ctx.Args[1], andOp, align, repeat, ctx), true
			})
			if !success {
				ctx.LogError(CatArgument, "Failed to process list elements")
				return BoolStatus(false)
			}
			setListResult(ctx, resultList)
			return BoolStatus(true)
		}

		// Non-list first argument
		isBytes, intVal, bytesVal, ok := extractOperand(ctx, ctx.Args[0])
		if !ok {
			ctx.LogError(CatArgument, "Invalid first argument for bitwise_and")
			return BoolStatus(false)
		}

		if isBytes {
			isBytes2, intVal2, bytesVal2, ok2 := extractOperand(ctx, ctx.Args[1])
			if !ok2 {
				ctx.LogError(CatArgument, "Invalid second argument for bitwise_and")
				return BoolStatus(false)
			}
			if isBytes2 {
				setBytesResult(ctx, applyBytesOp(bytesVal, bytesVal2, andOp, align, repeat))
			} else {
				// Second arg is int
				if repeat {
					singleByte := NewStoredBytes([]byte{byte(intVal2 & 0xFF)})
					setBytesResult(ctx, applyBytesOp(bytesVal, singleByte, andOp, align, true))
				} else {
					var intBytes []byte
					temp := intVal2
					if temp == 0 {
						intBytes = []byte{0}
					} else {
						for temp != 0 && temp != -1 {
							intBytes = append([]byte{byte(temp & 0xFF)}, intBytes...)
							temp >>= 8
						}
					}
					setBytesResult(ctx, applyBytesOp(bytesVal, NewStoredBytes(intBytes), andOp, align, repeat))
				}
			}
		} else {
			// First arg is int
			result := applyIntOp(intVal, ctx.Args[1], andOp, align, repeat, ctx)
			ctx.SetResult(result)
		}
		return BoolStatus(true)
	})

	// bitwise_or - bitwise OR operation
	ps.RegisterCommandInModule("bitwise", "bitwise_or", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: bitwise_or <value1>, <value2> [align: left|right] [repeat: true|false]")
			return BoolStatus(false)
		}

		align := "right"
		repeat := false
		if v, ok := ctx.NamedArgs["align"]; ok {
			align = fmt.Sprintf("%v", v)
		}
		if v, ok := ctx.NamedArgs["repeat"]; ok {
			repeat = isTruthy(v)
		}

		orOp := func(a, b byte) byte { return a | b }

		// Check if first arg is a list
		firstResolved := ctx.executor.resolveValue(ctx.Args[0])
		if list, ok := toListIfPossible(firstResolved); ok {
			resultList, success := processListArg(ctx, list, func(item interface{}) (interface{}, bool) {
				isBytes, intVal, bytesVal, ok := extractOperand(ctx, item)
				if !ok {
					return nil, false
				}
				if isBytes {
					isBytes2, intVal2, bytesVal2, ok2 := extractOperand(ctx, ctx.Args[1])
					if !ok2 {
						return nil, false
					}
					if isBytes2 {
						return applyBytesOp(bytesVal, bytesVal2, orOp, align, repeat), true
					}
					if repeat {
						singleByte := NewStoredBytes([]byte{byte(intVal2 & 0xFF)})
						return applyBytesOp(bytesVal, singleByte, orOp, align, true), true
					}
					var intBytes []byte
					temp := intVal2
					if temp == 0 {
						intBytes = []byte{0}
					} else {
						for temp != 0 && temp != -1 {
							intBytes = append([]byte{byte(temp & 0xFF)}, intBytes...)
							temp >>= 8
						}
					}
					return applyBytesOp(bytesVal, NewStoredBytes(intBytes), orOp, align, repeat), true
				}
				return applyIntOp(intVal, ctx.Args[1], orOp, align, repeat, ctx), true
			})
			if !success {
				ctx.LogError(CatArgument, "Failed to process list elements")
				return BoolStatus(false)
			}
			setListResult(ctx, resultList)
			return BoolStatus(true)
		}

		isBytes, intVal, bytesVal, ok := extractOperand(ctx, ctx.Args[0])
		if !ok {
			ctx.LogError(CatArgument, "Invalid first argument for bitwise_or")
			return BoolStatus(false)
		}

		if isBytes {
			isBytes2, intVal2, bytesVal2, ok2 := extractOperand(ctx, ctx.Args[1])
			if !ok2 {
				ctx.LogError(CatArgument, "Invalid second argument for bitwise_or")
				return BoolStatus(false)
			}
			if isBytes2 {
				setBytesResult(ctx, applyBytesOp(bytesVal, bytesVal2, orOp, align, repeat))
			} else {
				if repeat {
					singleByte := NewStoredBytes([]byte{byte(intVal2 & 0xFF)})
					setBytesResult(ctx, applyBytesOp(bytesVal, singleByte, orOp, align, true))
				} else {
					var intBytes []byte
					temp := intVal2
					if temp == 0 {
						intBytes = []byte{0}
					} else {
						for temp != 0 && temp != -1 {
							intBytes = append([]byte{byte(temp & 0xFF)}, intBytes...)
							temp >>= 8
						}
					}
					setBytesResult(ctx, applyBytesOp(bytesVal, NewStoredBytes(intBytes), orOp, align, repeat))
				}
			}
		} else {
			result := applyIntOp(intVal, ctx.Args[1], orOp, align, repeat, ctx)
			ctx.SetResult(result)
		}
		return BoolStatus(true)
	})

	// bitwise_xor - bitwise XOR operation
	ps.RegisterCommandInModule("bitwise", "bitwise_xor", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: bitwise_xor <value1>, <value2> [align: left|right] [repeat: true|false]")
			return BoolStatus(false)
		}

		align := "right"
		repeat := false
		if v, ok := ctx.NamedArgs["align"]; ok {
			align = fmt.Sprintf("%v", v)
		}
		if v, ok := ctx.NamedArgs["repeat"]; ok {
			repeat = isTruthy(v)
		}

		xorOp := func(a, b byte) byte { return a ^ b }

		// Check if first arg is a list
		firstResolved := ctx.executor.resolveValue(ctx.Args[0])
		if list, ok := toListIfPossible(firstResolved); ok {
			resultList, success := processListArg(ctx, list, func(item interface{}) (interface{}, bool) {
				isBytes, intVal, bytesVal, ok := extractOperand(ctx, item)
				if !ok {
					return nil, false
				}
				if isBytes {
					isBytes2, intVal2, bytesVal2, ok2 := extractOperand(ctx, ctx.Args[1])
					if !ok2 {
						return nil, false
					}
					if isBytes2 {
						return applyBytesOp(bytesVal, bytesVal2, xorOp, align, repeat), true
					}
					if repeat {
						singleByte := NewStoredBytes([]byte{byte(intVal2 & 0xFF)})
						return applyBytesOp(bytesVal, singleByte, xorOp, align, true), true
					}
					var intBytes []byte
					temp := intVal2
					if temp == 0 {
						intBytes = []byte{0}
					} else {
						for temp != 0 && temp != -1 {
							intBytes = append([]byte{byte(temp & 0xFF)}, intBytes...)
							temp >>= 8
						}
					}
					return applyBytesOp(bytesVal, NewStoredBytes(intBytes), xorOp, align, repeat), true
				}
				return applyIntOp(intVal, ctx.Args[1], xorOp, align, repeat, ctx), true
			})
			if !success {
				ctx.LogError(CatArgument, "Failed to process list elements")
				return BoolStatus(false)
			}
			setListResult(ctx, resultList)
			return BoolStatus(true)
		}

		isBytes, intVal, bytesVal, ok := extractOperand(ctx, ctx.Args[0])
		if !ok {
			ctx.LogError(CatArgument, "Invalid first argument for bitwise_xor")
			return BoolStatus(false)
		}

		if isBytes {
			isBytes2, intVal2, bytesVal2, ok2 := extractOperand(ctx, ctx.Args[1])
			if !ok2 {
				ctx.LogError(CatArgument, "Invalid second argument for bitwise_xor")
				return BoolStatus(false)
			}
			if isBytes2 {
				setBytesResult(ctx, applyBytesOp(bytesVal, bytesVal2, xorOp, align, repeat))
			} else {
				if repeat {
					singleByte := NewStoredBytes([]byte{byte(intVal2 & 0xFF)})
					setBytesResult(ctx, applyBytesOp(bytesVal, singleByte, xorOp, align, true))
				} else {
					var intBytes []byte
					temp := intVal2
					if temp == 0 {
						intBytes = []byte{0}
					} else {
						for temp != 0 && temp != -1 {
							intBytes = append([]byte{byte(temp & 0xFF)}, intBytes...)
							temp >>= 8
						}
					}
					setBytesResult(ctx, applyBytesOp(bytesVal, NewStoredBytes(intBytes), xorOp, align, repeat))
				}
			}
		} else {
			result := applyIntOp(intVal, ctx.Args[1], xorOp, align, repeat, ctx)
			ctx.SetResult(result)
		}
		return BoolStatus(true)
	})

	// bitwise_not - bitwise NOT operation
	ps.RegisterCommandInModule("bitwise", "bitwise_not", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "Usage: bitwise_not <value>")
			return BoolStatus(false)
		}

		// Check if first arg is a list
		firstResolved := ctx.executor.resolveValue(ctx.Args[0])
		if list, ok := toListIfPossible(firstResolved); ok {
			resultList, success := processListArg(ctx, list, func(item interface{}) (interface{}, bool) {
				isBytes, intVal, bytesVal, ok := extractOperand(ctx, item)
				if !ok {
					return nil, false
				}
				if isBytes {
					data := bytesVal.Data()
					result := make([]byte, len(data))
					for i, b := range data {
						result[i] = ^b
					}
					return NewStoredBytes(result), true
				}
				return ^intVal, true
			})
			if !success {
				ctx.LogError(CatArgument, "Failed to process list elements")
				return BoolStatus(false)
			}
			setListResult(ctx, resultList)
			return BoolStatus(true)
		}

		isBytes, intVal, bytesVal, ok := extractOperand(ctx, ctx.Args[0])
		if !ok {
			ctx.LogError(CatArgument, "Invalid argument for bitwise_not")
			return BoolStatus(false)
		}

		if isBytes {
			data := bytesVal.Data()
			result := make([]byte, len(data))
			for i, b := range data {
				result[i] = ^b
			}
			setBytesResult(ctx, NewStoredBytes(result))
		} else {
			ctx.SetResult(^intVal)
		}
		return BoolStatus(true)
	})

	// bitwise_shl - bitwise shift left
	ps.RegisterCommandInModule("bitwise", "bitwise_shl", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: bitwise_shl <value>, <distance>")
			return BoolStatus(false)
		}

		// Get shift distance
		distResolved := ctx.executor.resolveValue(ctx.Args[1])
		distance := int64(0)
		if num, ok := toNumber(distResolved); ok {
			distance = int64(num)
		}
		if distance < 0 {
			distance = 0
		}

		// Check if first arg is a list
		firstResolved := ctx.executor.resolveValue(ctx.Args[0])
		if list, ok := toListIfPossible(firstResolved); ok {
			resultList, success := processListArg(ctx, list, func(item interface{}) (interface{}, bool) {
				isBytes, intVal, bytesVal, ok := extractOperand(ctx, item)
				if !ok {
					return nil, false
				}
				if isBytes {
					// Shift bytes as a big integer (shift left = towards MSB)
					data := bytesVal.Data()
					result := make([]byte, len(data))
					copy(result, data)

					byteShift := int(distance / 8)
					bitShift := uint(distance % 8)

					if byteShift >= len(result) {
						// Shift more than the size, result is all zeros
						for i := range result {
							result[i] = 0
						}
					} else {
						// Shift bytes first
						if byteShift > 0 {
							for i := 0; i < len(result)-byteShift; i++ {
								result[i] = result[i+byteShift]
							}
							for i := len(result) - byteShift; i < len(result); i++ {
								result[i] = 0
							}
						}
						// Then shift bits
						if bitShift > 0 {
							var carry byte
							for i := len(result) - 1; i >= 0; i-- {
								newCarry := result[i] >> (8 - bitShift)
								result[i] = (result[i] << bitShift) | carry
								carry = newCarry
							}
						}
					}
					return NewStoredBytes(result), true
				}
				return intVal << uint(distance), true
			})
			if !success {
				ctx.LogError(CatArgument, "Failed to process list elements")
				return BoolStatus(false)
			}
			setListResult(ctx, resultList)
			return BoolStatus(true)
		}

		isBytes, intVal, bytesVal, ok := extractOperand(ctx, ctx.Args[0])
		if !ok {
			ctx.LogError(CatArgument, "Invalid first argument for bitwise_shl")
			return BoolStatus(false)
		}

		if isBytes {
			data := bytesVal.Data()
			result := make([]byte, len(data))
			copy(result, data)

			byteShift := int(distance / 8)
			bitShift := uint(distance % 8)

			if byteShift >= len(result) {
				for i := range result {
					result[i] = 0
				}
			} else {
				if byteShift > 0 {
					for i := 0; i < len(result)-byteShift; i++ {
						result[i] = result[i+byteShift]
					}
					for i := len(result) - byteShift; i < len(result); i++ {
						result[i] = 0
					}
				}
				if bitShift > 0 {
					var carry byte
					for i := len(result) - 1; i >= 0; i-- {
						newCarry := result[i] >> (8 - bitShift)
						result[i] = (result[i] << bitShift) | carry
						carry = newCarry
					}
				}
			}
			setBytesResult(ctx, NewStoredBytes(result))
		} else {
			ctx.SetResult(intVal << uint(distance))
		}
		return BoolStatus(true)
	})

	// bitwise_shr - bitwise shift right
	ps.RegisterCommandInModule("bitwise", "bitwise_shr", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: bitwise_shr <value>, <distance>")
			return BoolStatus(false)
		}

		distResolved := ctx.executor.resolveValue(ctx.Args[1])
		distance := int64(0)
		if num, ok := toNumber(distResolved); ok {
			distance = int64(num)
		}
		if distance < 0 {
			distance = 0
		}

		// Check if first arg is a list
		firstResolved := ctx.executor.resolveValue(ctx.Args[0])
		if list, ok := toListIfPossible(firstResolved); ok {
			resultList, success := processListArg(ctx, list, func(item interface{}) (interface{}, bool) {
				isBytes, intVal, bytesVal, ok := extractOperand(ctx, item)
				if !ok {
					return nil, false
				}
				if isBytes {
					data := bytesVal.Data()
					result := make([]byte, len(data))
					copy(result, data)

					byteShift := int(distance / 8)
					bitShift := uint(distance % 8)

					if byteShift >= len(result) {
						for i := range result {
							result[i] = 0
						}
					} else {
						if byteShift > 0 {
							for i := len(result) - 1; i >= byteShift; i-- {
								result[i] = result[i-byteShift]
							}
							for i := 0; i < byteShift; i++ {
								result[i] = 0
							}
						}
						if bitShift > 0 {
							var carry byte
							for i := 0; i < len(result); i++ {
								newCarry := result[i] << (8 - bitShift)
								result[i] = (result[i] >> bitShift) | carry
								carry = newCarry
							}
						}
					}
					return NewStoredBytes(result), true
				}
				// Use logical shift right for int64 (cast to uint64)
				return int64(uint64(intVal) >> uint(distance)), true
			})
			if !success {
				ctx.LogError(CatArgument, "Failed to process list elements")
				return BoolStatus(false)
			}
			setListResult(ctx, resultList)
			return BoolStatus(true)
		}

		isBytes, intVal, bytesVal, ok := extractOperand(ctx, ctx.Args[0])
		if !ok {
			ctx.LogError(CatArgument, "Invalid first argument for bitwise_shr")
			return BoolStatus(false)
		}

		if isBytes {
			data := bytesVal.Data()
			result := make([]byte, len(data))
			copy(result, data)

			byteShift := int(distance / 8)
			bitShift := uint(distance % 8)

			if byteShift >= len(result) {
				for i := range result {
					result[i] = 0
				}
			} else {
				if byteShift > 0 {
					for i := len(result) - 1; i >= byteShift; i-- {
						result[i] = result[i-byteShift]
					}
					for i := 0; i < byteShift; i++ {
						result[i] = 0
					}
				}
				if bitShift > 0 {
					var carry byte
					for i := 0; i < len(result); i++ {
						newCarry := result[i] << (8 - bitShift)
						result[i] = (result[i] >> bitShift) | carry
						carry = newCarry
					}
				}
			}
			setBytesResult(ctx, NewStoredBytes(result))
		} else {
			ctx.SetResult(int64(uint64(intVal) >> uint(distance)))
		}
		return BoolStatus(true)
	})

	// bitwise_rol - bitwise rotate left
	ps.RegisterCommandInModule("bitwise", "bitwise_rol", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: bitwise_rol <value>, <distance> [bitlength: N]")
			return BoolStatus(false)
		}

		distResolved := ctx.executor.resolveValue(ctx.Args[1])
		distance := int64(0)
		if num, ok := toNumber(distResolved); ok {
			distance = int64(num)
		}

		bitlength := 8 // Default to 8-bit rotation
		if v, ok := ctx.NamedArgs["bitlength"]; ok {
			if num, numOk := toNumber(v); numOk {
				bitlength = int(num)
			}
		}
		if bitlength <= 0 {
			bitlength = 8
		}
		if bitlength > 64 {
			bitlength = 64
		}

		// Normalize distance
		distance = distance % int64(bitlength)
		if distance < 0 {
			distance += int64(bitlength)
		}

		// Check if first arg is a list
		firstResolved := ctx.executor.resolveValue(ctx.Args[0])
		if list, ok := toListIfPossible(firstResolved); ok {
			resultList, success := processListArg(ctx, list, func(item interface{}) (interface{}, bool) {
				isBytes, intVal, bytesVal, ok := extractOperand(ctx, item)
				if !ok {
					return nil, false
				}
				if isBytes {
					// For bytes: rotate rightmost byte(s) based on bitlength
					data := bytesVal.Data()
					result := make([]byte, len(data))
					copy(result, data)

					bytesNeeded := (bitlength + 7) / 8
					if bytesNeeded > len(result) {
						bytesNeeded = len(result)
					}

					// Extract the bits to rotate (rightmost bytes)
					startIdx := len(result) - bytesNeeded
					mask := uint64(1<<bitlength) - 1

					// Read value from rightmost bytes
					var val uint64
					for i := startIdx; i < len(result); i++ {
						val = (val << 8) | uint64(result[i])
					}
					val &= mask

					// Rotate left
					val = ((val << uint(distance)) | (val >> uint(int64(bitlength)-distance))) & mask

					// Write back
					for i := len(result) - 1; i >= startIdx; i-- {
						result[i] = byte(val & 0xFF)
						val >>= 8
					}

					return NewStoredBytes(result), true
				}
				// int64 rotation
				mask := uint64(1<<bitlength) - 1
				val := uint64(intVal) & mask
				val = ((val << uint(distance)) | (val >> uint(int64(bitlength)-distance))) & mask
				return int64(val), true
			})
			if !success {
				ctx.LogError(CatArgument, "Failed to process list elements")
				return BoolStatus(false)
			}
			setListResult(ctx, resultList)
			return BoolStatus(true)
		}

		isBytes, intVal, bytesVal, ok := extractOperand(ctx, ctx.Args[0])
		if !ok {
			ctx.LogError(CatArgument, "Invalid first argument for bitwise_rol")
			return BoolStatus(false)
		}

		if isBytes {
			data := bytesVal.Data()
			result := make([]byte, len(data))
			copy(result, data)

			bytesNeeded := (bitlength + 7) / 8
			if bytesNeeded > len(result) {
				bytesNeeded = len(result)
			}

			startIdx := len(result) - bytesNeeded
			mask := uint64(1<<bitlength) - 1

			var val uint64
			for i := startIdx; i < len(result); i++ {
				val = (val << 8) | uint64(result[i])
			}
			val &= mask

			val = ((val << uint(distance)) | (val >> uint(int64(bitlength)-distance))) & mask

			for i := len(result) - 1; i >= startIdx; i-- {
				result[i] = byte(val & 0xFF)
				val >>= 8
			}

			setBytesResult(ctx, NewStoredBytes(result))
		} else {
			mask := uint64(1<<bitlength) - 1
			val := uint64(intVal) & mask
			val = ((val << uint(distance)) | (val >> uint(int64(bitlength)-distance))) & mask
			ctx.SetResult(int64(val))
		}
		return BoolStatus(true)
	})

	// bitwise_ror - bitwise rotate right
	ps.RegisterCommandInModule("bitwise", "bitwise_ror", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "Usage: bitwise_ror <value>, <distance> [bitlength: N]")
			return BoolStatus(false)
		}

		distResolved := ctx.executor.resolveValue(ctx.Args[1])
		distance := int64(0)
		if num, ok := toNumber(distResolved); ok {
			distance = int64(num)
		}

		bitlength := 8
		if v, ok := ctx.NamedArgs["bitlength"]; ok {
			if num, numOk := toNumber(v); numOk {
				bitlength = int(num)
			}
		}
		if bitlength <= 0 {
			bitlength = 8
		}
		if bitlength > 64 {
			bitlength = 64
		}

		distance = distance % int64(bitlength)
		if distance < 0 {
			distance += int64(bitlength)
		}

		// Check if first arg is a list
		firstResolved := ctx.executor.resolveValue(ctx.Args[0])
		if list, ok := toListIfPossible(firstResolved); ok {
			resultList, success := processListArg(ctx, list, func(item interface{}) (interface{}, bool) {
				isBytes, intVal, bytesVal, ok := extractOperand(ctx, item)
				if !ok {
					return nil, false
				}
				if isBytes {
					data := bytesVal.Data()
					result := make([]byte, len(data))
					copy(result, data)

					bytesNeeded := (bitlength + 7) / 8
					if bytesNeeded > len(result) {
						bytesNeeded = len(result)
					}

					startIdx := len(result) - bytesNeeded
					mask := uint64(1<<bitlength) - 1

					var val uint64
					for i := startIdx; i < len(result); i++ {
						val = (val << 8) | uint64(result[i])
					}
					val &= mask

					// Rotate right
					val = ((val >> uint(distance)) | (val << uint(int64(bitlength)-distance))) & mask

					for i := len(result) - 1; i >= startIdx; i-- {
						result[i] = byte(val & 0xFF)
						val >>= 8
					}

					return NewStoredBytes(result), true
				}
				mask := uint64(1<<bitlength) - 1
				val := uint64(intVal) & mask
				val = ((val >> uint(distance)) | (val << uint(int64(bitlength)-distance))) & mask
				return int64(val), true
			})
			if !success {
				ctx.LogError(CatArgument, "Failed to process list elements")
				return BoolStatus(false)
			}
			setListResult(ctx, resultList)
			return BoolStatus(true)
		}

		isBytes, intVal, bytesVal, ok := extractOperand(ctx, ctx.Args[0])
		if !ok {
			ctx.LogError(CatArgument, "Invalid first argument for bitwise_ror")
			return BoolStatus(false)
		}

		if isBytes {
			data := bytesVal.Data()
			result := make([]byte, len(data))
			copy(result, data)

			bytesNeeded := (bitlength + 7) / 8
			if bytesNeeded > len(result) {
				bytesNeeded = len(result)
			}

			startIdx := len(result) - bytesNeeded
			mask := uint64(1<<bitlength) - 1

			var val uint64
			for i := startIdx; i < len(result); i++ {
				val = (val << 8) | uint64(result[i])
			}
			val &= mask

			val = ((val >> uint(distance)) | (val << uint(int64(bitlength)-distance))) & mask

			for i := len(result) - 1; i >= startIdx; i-- {
				result[i] = byte(val & 0xFF)
				val >>= 8
			}

			setBytesResult(ctx, NewStoredBytes(result))
		} else {
			mask := uint64(1<<bitlength) - 1
			val := uint64(intVal) & mask
			val = ((val >> uint(distance)) | (val << uint(int64(bitlength)-distance))) & mask
			ctx.SetResult(int64(val))
		}
		return BoolStatus(true)
	})
}
