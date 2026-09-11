package pawscript

// Integers that keep every digit they were given.
//
// A PSL document already carried a full int64 in and out unharmed; it was
// comparing and computing with one that went through float64. These tests work
// above 2^53, where a float64 stops being able to tell two integers apart, so
// each of them fails if any step of the path widens.

import (
	"bytes"
	"math"
	"strings"
	"testing"
)

// runScript executes a script and returns everything it printed.
func runScript(t *testing.T, script string) string {
	t.Helper()
	var buf bytes.Buffer
	ps := New(&Config{Stdout: &buf, Stderr: &buf})
	ps.RegisterStandardLibrary(nil)
	ps.Execute(script)
	return strings.TrimSpace(buf.String())
}

// Two integers a single step apart, far above where a float64 can tell them
// apart, stay apart through arithmetic.
func TestArithmeticKeepsEveryDigitOfAnInteger(t *testing.T) {
	for _, c := range []struct{ script, want string }{
		{`echo {add 9007199254740992, 1}`, "9007199254740993"},
		{`echo {sub 9007199254740994, 1}`, "9007199254740993"},
		{`echo {mul 4503599627370496, 2}`, "9007199254740992"},
		{`echo {add 1700000000123456789, 0}`, "1700000000123456789"},
		{`echo {min 9007199254740993, 9007199254740992}`, "9007199254740992"},
		{`echo {max 9007199254740993, 9007199254740992}`, "9007199254740993"},
		{`echo {abs -9007199254740993}`, "9007199254740993"},
	} {
		if got := runScript(t, c.script); got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.script, got, c.want)
		}
	}
}

// A sum too large to hold widens to a float rather than wrapping round to a
// negative number, which is the arithmetic a script already expected.
func TestAResultTooLargeToHoldWidensRatherThanWraps(t *testing.T) {
	got := runScript(t, `echo {add 9223372036854775807, 1}`)
	if strings.HasPrefix(got, "-") {
		t.Errorf("an overflowing sum wrapped to %s", got)
	}
	if !strings.Contains(got, "9.22") {
		t.Errorf("an overflowing sum came back as %s, want a float near 9.22e18", got)
	}
}

// Ordering tells two adjacent large integers apart, whichever way round.
func TestOrderingTellsLargeIntegersApart(t *testing.T) {
	for _, c := range []struct{ script, want string }{
		{`echo {lt 9007199254740992, 9007199254740993}`, "true"},
		{`echo {gt 9007199254740992, 9007199254740993}`, "false"},
		{`echo {lt 9007199254740993, 9007199254740992}`, "false"},
		{`echo {gte 9007199254740993, 9007199254740993}`, "true"},
	} {
		if got := runScript(t, c.script); got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.script, got, c.want)
		}
	}
}

// Sorting a list orders large integers by what they are, not by what a float64
// can hold of them.
func TestSortingOrdersLargeIntegers(t *testing.T) {
	// The two differ by one, and both land on the same float64 -- so through a
	// float they compare EQUAL, the stable sort leaves them as they came, and
	// the larger stays in front.
	got := runScript(t, `x: {list 9007199254740993, 9007199254740992}
echo {sort ~x}`)
	small := strings.Index(got, "9007199254740992")
	large := strings.Index(got, "9007199254740993")
	if small < 0 || large < 0 {
		t.Fatalf("sorted list came back as %q", got)
	}
	if small > large {
		t.Errorf("the smaller integer did not sort first: %s", got)
	}
}

// An integer compared against a float is compared exactly: neither is
// converted to the other, because converting the integer loses digits and
// converting the float loses the fraction that decides it.
func TestAnIntegerAgainstAFloatComparesExactly(t *testing.T) {
	big := int64(9007199254740993) // one past what a float64 can represent
	cases := []struct {
		f    float64
		i    int64
		want int
	}{
		{9007199254740992.0, big, -1}, // the float is the integer below it
		{9007199254740992.0, big - 1, 0},
		{2.5, 2, 1},
		{2.5, 3, -1},
		{-2.5, -2, -1},
		{-2.5, -3, 1},
		{0.0, 0, 0},
		{-0.5, 0, -1},
		{1e300, math.MaxInt64, 1},
		{-1e300, math.MinInt64, -1},
	}
	for _, c := range cases {
		if got := compareFloatToInt(c.f, c.i); got != c.want {
			t.Errorf("compareFloatToInt(%v, %d) = %d, want %d", c.f, c.i, got, c.want)
		}
	}
}

// Rounding an integer gives back the integer: it is already rounded, and
// sending it through a float64 to prove that is where the digits go.
func TestRoundingAnIntegerLeavesItAlone(t *testing.T) {
	for _, verb := range []string{"floor", "ceil", "trunc", "round"} {
		script := "echo {" + verb + " 9007199254740993}"
		if got := runScript(t, script); got != "9007199254740993" {
			t.Errorf("%s gave %s", script, got)
		}
	}
}

// Integer division stays in integers, and keeps the floored quotient, the
// dividend-signed remainder and the divisor-signed modulo it always had.
func TestIntegerDivisionStaysExact(t *testing.T) {
	for _, c := range []struct{ script, want string }{
		{`echo {idiv 9007199254740993, 1}`, "9007199254740993"},
		{`echo {idiv 7, 2}`, "3"},
		{`echo {idiv -7, 2}`, "-4"}, // floored, not truncated
		{`echo {idiv 7, -2}`, "-4"},
		{`echo {iremainder -7, 2}`, "-1"}, // sign from the dividend
		{`echo {imodulo -7, 2}`, "1"},     // sign from the divisor
		{`echo {iremainder 7, -2}`, "1"},
		{`echo {imodulo 7, -2}`, "-1"},
	} {
		if got := runScript(t, c.script); got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.script, got, c.want)
		}
	}
}

// The one integer with no positive counterpart widens rather than staying
// negative, which is what abs of it used to do in float anyway.
func TestTheSmallestIntegerHasNoAbsoluteValue(t *testing.T) {
	if got := absNumber(intNumber(math.MinInt64)); got.isInt {
		t.Errorf("abs of the smallest integer stayed an integer: %d", got.i)
	}
}

// Whole numbers written as text are read as integers, so a key that arrived as
// a string still compares exactly.
func TestTextThatIsAWholeNumberReadsAsAnInteger(t *testing.T) {
	n, ok := toExactNumber(QuotedString("9007199254740993"))
	if !ok || !n.isInt || n.i != 9007199254740993 {
		t.Errorf("a whole number in text read as %#v", n)
	}
	f, ok := toExactNumber(QuotedString("1.5"))
	if !ok || f.isInt || f.f != 1.5 {
		t.Errorf("a fractional number in text read as %#v", f)
	}
}
