package pawscript

import (
	"strings"
	"testing"
)

// newTestPS builds an interpreter with the standard library loaded.
func newTestPS() *PawScript {
	ps := New(&Config{Debug: false})
	ps.RegisterStandardLibrary([]string{})
	return ps
}

// TestDoubleTildeIndirection verifies that ~~x performs one extra level of
// dereference (value of the variable whose name is the value of x) instead of
// recursing forever. Regression for the stack-overflow crash on ~~x.
func TestDoubleTildeIndirection(t *testing.T) {
	ps := newTestPS()
	// target = "hello"; pointer = "target"; ~~pointer should yield "hello".
	ps.Execute(`target: "hello"`)
	ps.Execute(`pointer: "target"`)
	ps.Execute(`out: {~~pointer}`)

	root := ps.GetRootState()
	v, ok := root.GetVariable("out")
	if !ok {
		t.Fatalf("variable out was not set (double-tilde likely failed)")
	}
	if got := strings.TrimSpace(resolveToString(v, ps.executor)); got != "hello" {
		t.Errorf("~~pointer = %q, want %q", got, "hello")
	}
}

// TestDoubleTildeDoesNotCrash guards specifically against the infinite recursion:
// even with an undefined inner variable, ~~x must return, not stack-overflow.
func TestDoubleTildeDoesNotCrash(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ps := newTestPS()
		ps.Execute(`echo ~~undefined_var_xyz`)
	}()
	<-done // if this ever hangs/overflows the test binary dies — that is the failure
}

// TestNulByteArgumentNoPanic verifies a lone NUL byte in an argument no longer
// panics parseObjectMarker with a slice-bounds error.
func TestNulByteArgumentNoPanic(t *testing.T) {
	cases := []string{"echo \x00", "set x, \x00", "\x00", "echo a\x00b"}
	for _, src := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("PANIC on %q: %v", src, r)
				}
			}()
			newTestPS().Execute(src)
		}()
	}
}

// TestParseObjectMarkerBareNul is a direct unit test of the guarded function.
func TestParseObjectMarkerBareNul(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("parseObjectMarker panicked: %v", r)
		}
	}()
	for _, s := range []string{"", "\x00", "\x00\x00", "x"} {
		if typ, id := parseObjectMarker(s); typ != "" || id != -1 {
			t.Errorf("parseObjectMarker(%q) = (%q,%d), want (\"\",-1)", s, typ, id)
		}
	}
}

// TestRepeatBoundsRejectHuge verifies script-controlled repeat counts can no
// longer trigger an uncatchable OOM. These must return without allocating.
func TestRepeatBoundsRejectHuge(t *testing.T) {
	cases := []string{
		`repeat "ab", 1000000000000`,          // string mode, ~2 TB
		`repeat "x", 999999999999`,            // string mode
		`repeat { echo hi }, 100000000000000`, // block mode preallocation
	}
	for _, src := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("PANIC on %q: %v", src, r)
				}
			}()
			newTestPS().Execute(src)
		}()
	}
}

// TestRepeatStillWorks confirms the cap doesn't break legitimate use.
func TestRepeatStillWorks(t *testing.T) {
	ps := newTestPS()
	ps.Execute(`out: {repeat "ab", 3}`)
	v, ok := ps.GetRootState().GetVariable("out")
	if !ok {
		t.Fatalf("out not set")
	}
	if got := resolveToString(v, ps.executor); got != "ababab" {
		t.Errorf(`repeat "ab", 3 = %q, want "ababab"`, got)
	}
}

// TestStructHardeningNoPanic verifies script-controlled struct sizes/counts and
// hand-crafted definitions can no longer panic makeslice or OOM the host.
func TestStructHardeningNoPanic(t *testing.T) {
	cases := []struct {
		name string
		src  []string
	}{
		{"negative field size (struct_def)", []string{
			`d: {list ("f", -1, "bytes")}`,
			`def: {struct_def ~d}`,
			`s: {struct ~def, {list f: 1}}`,
		}},
		{"huge field size (struct_def)", []string{
			`d: {list ("f", 2000000000, "bytes")}`,
			`def: {struct_def ~d}`,
			`s: {struct ~def, {list f: 1}}`,
		}},
		{"huge array count (struct)", []string{
			`d: {list ("f", 8, "int")}`,
			`def: {struct_def ~d}`,
			`s: {struct ~def, {list}, 1000000000}`,
		}},
		{"crafted def: tiny __size, far field offset", []string{
			// Bypasses struct_def: a raw list used directly as a definition with
			// a field whose offset/length far exceeds __size. Must not panic the
			// write path (SetBytesAt).
			`fi: {list 1000, 4, "int"}`,
			`def: {list} __size: 2 f: ~fi`,
			`s: {struct ~def, {list f: 5}}`,
		}},
		{"crafted def: negative field length", []string{
			`fi: {list 0, -4, "int"}`,
			`def: {list} __size: 8 f: ~fi`,
			`s: {struct ~def, {list f: 5}}`,
		}},
	}
	for _, tc := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("PANIC [%s]: %v", tc.name, r)
				}
			}()
			ps := newTestPS()
			for _, line := range tc.src {
				ps.Execute(line)
			}
		}()
	}
}

// TestStructBytePrimitivesBounds directly exercises the low-level byte ops that
// a hand-crafted definition can drive with out-of-range offsets/lengths. These
// are the ultimate backstop: they must return false, never panic.
func TestStructBytePrimitivesBounds(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("primitive panicked: %v", r)
		}
	}()
	ss := NewStoredStruct(0, 2) // 2-byte backing array

	// Out-of-range / negative writes must fail, not panic (previously unchecked).
	if ss.SetBytesAt(1000, []byte{1, 2, 3, 4}, 4) {
		t.Error("SetBytesAt at offset 1000 on 2-byte struct should fail")
	}
	if ss.SetBytesAt(-5, []byte{1}, 1) {
		t.Error("SetBytesAt at negative offset should fail")
	}
	// In-range write still works.
	if !ss.SetBytesAt(0, []byte{0xAB, 0xCD}, 2) {
		t.Error("in-range SetBytesAt should succeed")
	}

	// Out-of-range / negative reads return ok=false, not panic.
	if _, ok := ss.GetBytesAt(1000, 4); ok {
		t.Error("GetBytesAt out of range should return ok=false")
	}
	if _, ok := ss.GetBytesAt(-1, 2); ok {
		t.Error("GetBytesAt negative offset should return ok=false")
	}
	if _, ok := ss.GetBytesAt(0, -1); ok {
		t.Error("GetBytesAt negative length should return ok=false")
	}
	// In-range read returns the written bytes.
	if b, ok := ss.GetBytesAt(0, 2); !ok || len(b) != 2 || b[0] != 0xAB || b[1] != 0xCD {
		t.Errorf("GetBytesAt in-range = %v ok=%v, want [AB CD]", b, ok)
	}

	// ZeroPadAt with out-of-range indices must not panic.
	ss.ZeroPadAt(1000, 0, 4)
	ss.ZeroPadAt(-3, 0, 2)
}

// TestStructConstructorsNoPanic verifies the constructors never panic makeslice
// or OOM on bad sizes/counts, regardless of what upstream validation missed.
func TestStructConstructorsNoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("constructor panicked: %v", r)
		}
	}()
	_ = NewStoredStruct(0, -1)                 // negative size
	_ = NewStoredStruct(0, 1<<40)              // huge size
	_ = NewStoredStructArray(0, -1, 5)         // negative size
	_ = NewStoredStructArray(0, 8, -1)         // negative count
	_ = NewStoredStructArray(0, 1<<20, 1<<20)  // size*count huge
	_ = NewStoredStructArray(0, 1<<31, 1<<31)  // size*count overflows int32
}

// TestStructStillWorks confirms the validation doesn't break normal struct use.
func TestStructStillWorks(t *testing.T) {
	ps := newTestPS()
	ps.Execute(`d: {list ("n", 4, "int")}`)
	ps.Execute(`def: {struct_def ~d}`)
	ps.Execute(`s: {struct ~def, {list n: 4660}}`)
	ps.Execute(`out: {~s.n}`)
	v, ok := ps.GetRootState().GetVariable("out")
	if !ok {
		t.Fatalf("out not set — struct round-trip failed")
	}
	if got := strings.TrimSpace(resolveToString(v, ps.executor)); got != "4660" {
		t.Errorf("struct field round-trip = %q, want %q", got, "4660")
	}
}
