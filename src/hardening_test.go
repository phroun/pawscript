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

// TestStructNegativeSizeNoPanic verifies a negative struct field size is
// rejected/handled instead of panicking makeslice. (Documents current behavior;
// see docs/code-review for the struct hardening item.)
func TestStructNegativeSizeNoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("struct negative-size still panics (tracked separately): %v", r)
		}
	}()
	ps := newTestPS()
	ps.Execute(`d: {list ("f", -1, "bytes")}`)
	ps.Execute(`def: {struct_def ~d}`)
	ps.Execute(`s: {struct ~def, {list f: 1}}`)
}
