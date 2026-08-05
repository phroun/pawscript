package pawscript

import (
	"sync/atomic"
	"testing"
	"time"
)

// A command sequence joined by flow-control operators (`&`, `|`) must honor
// those operators even when an earlier command SUSPENDS on a completion token
// and the sequence resumes asynchronously. The synchronous path already gated
// on the separator; the resume path (resumeSequence) did not, so a suspended
// `a & b` ran b regardless of a's resolved status — the bug behind mew's
// `buffer_save_all & exit` exiting (and losing work) after a cancel.
//
// Each case registers an async command that resolves its token to a chosen
// status after a short delay, then a marker whose execution we watch. Execute
// blocks until the whole (resumed) chain finishes.
func TestResumeHonorsFlowControlOperators(t *testing.T) {
	cases := []struct {
		name       string
		script     string
		resolveTo  bool // status the suspended command resolves to
		wantMarker bool // should the trailing marker have run?
	}{
		{"and_after_failure_skips", "suspend & marker", false, false},
		{"and_after_success_runs", "suspend & marker", true, true},
		{"or_after_failure_runs", "suspend | marker", false, true},
		{"or_after_success_skips", "suspend | marker", true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ps := New(nil)

			resolveTo := tc.resolveTo
			ps.RegisterCommand("suspend", func(ctx *Context) Result {
				token := ctx.RequestToken(nil)
				go func() {
					time.Sleep(10 * time.Millisecond)
					ctx.ResumeToken(token, resolveTo)
				}()
				return TokenResult(token)
			})

			var markerRan atomic.Bool
			ps.RegisterCommand("marker", func(ctx *Context) Result {
				markerRan.Store(true)
				return BoolStatus(true)
			})

			ps.Execute(tc.script) // blocks until the resumed chain completes

			if got := markerRan.Load(); got != tc.wantMarker {
				t.Fatalf("%s: marker ran = %v, want %v", tc.script, got, tc.wantMarker)
			}
		})
	}
}

// A chain that MIXES operators across a suspend boundary resumes correctly.
// This is the case the retired homogeneous "conditional"/"or" resume types
// could not represent (each assumed a chain of one operator): resumeSequence
// gates per command, exactly like the synchronous executeCommandSequence.
// Here `suspend` resolves FALSE, so `& runsOnSuccess` is skipped, but the
// unconditional `; runsAlways` still runs.
func TestResumeHandlesMixedOperatorsAfterSuspend(t *testing.T) {
	ps := New(nil)

	ps.RegisterCommand("suspend", func(ctx *Context) Result {
		token := ctx.RequestToken(nil)
		go func() {
			time.Sleep(10 * time.Millisecond)
			ctx.ResumeToken(token, false)
		}()
		return TokenResult(token)
	})
	var onSuccessRan, alwaysRan atomic.Bool
	ps.RegisterCommand("runsOnSuccess", func(ctx *Context) Result {
		onSuccessRan.Store(true)
		return BoolStatus(true)
	})
	ps.RegisterCommand("runsAlways", func(ctx *Context) Result {
		alwaysRan.Store(true)
		return BoolStatus(true)
	})

	ps.Execute("suspend & runsOnSuccess ; runsAlways")

	if onSuccessRan.Load() {
		t.Error("`& runsOnSuccess` must be skipped after suspend resolved false")
	}
	if !alwaysRan.Load() {
		t.Error("`; runsAlways` is unconditional and must run regardless")
	}
}
