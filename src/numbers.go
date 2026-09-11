package pawscript

// Numbers that keep the precision they were given.
//
// PawScript holds an integer as an int64 from the moment it is parsed, and a
// PSL document carries one in and out unharmed. Everything that COMPARED or
// COMPUTED with one went through float64, which is exact only to 2^53 -- so
// two ids differing in their last digit compared equal, and a nanosecond
// timestamp (~1.7e18) lost its last two digits on the way into a sum.
//
// A number here is an integer or a float, never both. An operation stays in
// int64 while it can: every operand an integer, and a result that fits.
// Anything else -- a float operand, a division that does not divide, a sum too
// large to hold -- widens to float64, and says so by what it returns.

import (
	"math"
	"strconv"
)

// number is one value, held as whichever of the two it is.
type number struct {
	i     int64
	f     float64
	isInt bool
}

func intNumber(i int64) number     { return number{i: i, isInt: true} }
func floatNumber(f float64) number { return number{f: f} }

// float is this number as a float64, whichever way it is held. Above 2^53 an
// integer loses digits on the way, which is why nothing that can stay exact
// goes through here.
func (n number) float() float64 {
	if n.isInt {
		return float64(n.i)
	}
	return n.f
}

// value is this number the way PawScript holds one: an int64 or a float64.
func (n number) value() interface{} {
	if n.isInt {
		return n.i
	}
	return n.f
}

// toExactNumber reads a value as a number, keeping it an integer when it is
// one. It accepts exactly what toNumber accepts -- anything it cannot answer
// for itself, it asks toNumber -- so the only difference between them is how
// much of the answer survives.
func toExactNumber(val interface{}) (number, bool) {
	switch v := val.(type) {
	case int64:
		return intNumber(v), true
	case int:
		return intNumber(int64(v)), true
	case float64:
		return floatNumber(v), true
	case StoredBytes:
		return intNumber(v.ToInt64()), true
	case Symbol:
		return textToNumber(string(v))
	case QuotedString:
		return textToNumber(string(v))
	case string:
		return textToNumber(v)
	}
	f, ok := toNumber(val)
	return floatNumber(f), ok
}

// textToNumber reads a number written as text. A hex literal and a run of
// digits are integers; anything else a float parses is a float.
func textToNumber(s string) (number, bool) {
	if i, ok := parseHexToInt64(s); ok {
		return intNumber(i), true
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return intNumber(i), true
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return floatNumber(f), true
	}
	return number{}, false
}

// compareNumbers orders two numbers: -1, 0 or 1. Two integers compare as
// integers however large they are, and an integer against a float compares
// without either being converted to the other -- casting the integer would
// lose digits, and casting the float would lose the fraction that decides it.
func compareNumbers(a, b number) int {
	switch {
	case a.isInt && b.isInt:
		switch {
		case a.i < b.i:
			return -1
		case a.i > b.i:
			return 1
		}
		return 0
	case a.isInt:
		return -compareFloatToInt(b.f, a.i)
	case b.isInt:
		return compareFloatToInt(a.f, b.i)
	}
	switch {
	case a.f < b.f:
		return -1
	case a.f > b.f:
		return 1
	}
	return 0
}

// compareFloatToInt orders a float against an integer exactly: -1 if the float
// is the smaller. A float too large to be an int64 is decided by its sign
// alone; otherwise the whole part decides, and the fraction breaks the tie.
// NaN compares equal to everything, which is what the float comparisons it
// replaces did.
func compareFloatToInt(f float64, i int64) int {
	if math.IsNaN(f) {
		return 0
	}
	const (
		tooLarge = 9223372036854775808.0  // 2^63, one past the largest int64
		tooSmall = -9223372036854775808.0 // -2^63, the smallest int64 exactly
	)
	if f >= tooLarge {
		return 1
	}
	if f < tooSmall {
		return -1
	}
	whole := int64(math.Trunc(f))
	switch {
	case whole < i:
		return -1
	case whole > i:
		return 1
	}
	// Same whole part: the fraction decides, and its sign says which way.
	switch frac := f - math.Trunc(f); {
	case frac > 0:
		return 1
	case frac < 0:
		return -1
	}
	return 0
}

// addNumbers, subNumbers and mulNumbers stay in int64 while both operands are
// integers and the answer fits in one. A result that does not fit widens to
// float64 rather than wrapping around, which is the arithmetic a script
// already expected -- it just used to happen one step earlier.
func addNumbers(a, b number) number {
	if a.isInt && b.isInt {
		if sum := a.i + b.i; (sum > a.i) == (b.i > 0) {
			return intNumber(sum)
		}
	}
	return floatNumber(a.float() + b.float())
}

func subNumbers(a, b number) number {
	if a.isInt && b.isInt {
		if diff := a.i - b.i; (diff < a.i) == (b.i > 0) {
			return intNumber(diff)
		}
	}
	return floatNumber(a.float() - b.float())
}

func mulNumbers(a, b number) number {
	if a.isInt && b.isInt {
		if a.i == 0 || b.i == 0 {
			return intNumber(0)
		}
		product := a.i * b.i
		if product/b.i == a.i && !(a.i == math.MinInt64 && b.i == -1) &&
			!(b.i == math.MinInt64 && a.i == -1) {
			return intNumber(product)
		}
	}
	return floatNumber(a.float() * b.float())
}

// absNumber is the distance from zero. The smallest int64 has no positive
// counterpart, so it is the one integer that widens.
func absNumber(a number) number {
	if a.isInt {
		if a.i == math.MinInt64 {
			return floatNumber(-float64(a.i))
		}
		if a.i < 0 {
			return intNumber(-a.i)
		}
		return a
	}
	return floatNumber(math.Abs(a.f))
}

// dividedNumbers is floored division, the remainder that takes the dividend's
// sign, and the modulo that takes the divisor's -- the same three the float
// path computes, in integers when both sides are integers.
//
// ok is false when the division cannot be done in integers at all, and the
// caller falls back to the float path.
func dividedNumbers(a, b number) (quotient, remainder, modulo number, ok bool) {
	if !a.isInt || !b.isInt || b.i == 0 {
		return number{}, number{}, number{}, false
	}
	if a.i == math.MinInt64 && b.i == -1 {
		return number{}, number{}, number{}, false // the one quotient that will not fit
	}
	q := a.i / b.i
	r := a.i % b.i // Go's remainder already takes the dividend's sign
	if r != 0 && (r < 0) != (b.i < 0) {
		q-- // floored, not truncated
	}
	m := a.i - q*b.i
	return intNumber(q), intNumber(r), intNumber(m), true
}
