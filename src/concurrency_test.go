package pawscript

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestFiberModuleEnvSharedMap reproduces the fiber module-environment race: a
// fiber child created via NewChildModuleEnvironment aliases the parent's
// MacrosModule map. Once the parent has COW-copied (owns a private map), further
// macro definitions write that map in place while the fiber reads it under a
// different mutex -> concurrent map read/write. Run with -race.
func TestFiberModuleEnvSharedMap(t *testing.T) {
	parent := NewModuleEnvironment()
	// Trigger the parent's macro COW so it owns a private map (flag = true).
	parent.mu.Lock()
	parent.EnsureMacroRegistryCopied()
	parent.MacrosModule["seed"] = &StoredMacro{}
	parent.mu.Unlock()

	// Spawn a fiber child; it aliases the parent's (now private) macro map.
	child := NewChildModuleEnvironment(parent)
	// SpawnFiber applies this to decouple the fiber's maps from the parent's.
	child.IsolateRegistriesForFiber()

	var wg sync.WaitGroup
	wg.Add(2)
	// Fiber goroutine: repeatedly resolves a macro (reads child.MacrosModule).
	go func() {
		defer wg.Done()
		for i := 0; i < 5000; i++ {
			_, _ = child.GetMacro("seed")
		}
	}()
	// Parent goroutine: keeps defining macros the same way the runtime does.
	go func() {
		defer wg.Done()
		for i := 0; i < 5000; i++ {
			parent.mu.Lock()
			parent.EnsureMacroRegistryCopied() // no-op: flag already true
			parent.MacrosModule[fmt.Sprintf("m%d", i)] = &StoredMacro{}
			parent.mu.Unlock()
		}
	}()
	wg.Wait()
}

// TestFiberConcurrencyEndToEnd drives the real interpreter — fibers running
// async (msleep) while loops concurrently, plus macro definitions and more
// fiber spawns from the main goroutine. Under `go test -race` this instruments
// the actual fiber / token / module-environment machinery and is the CI guard
// for the concurrency fixes. It intentionally asserts no panic/race, not output.
func TestFiberConcurrencyEndToEnd(t *testing.T) {
	script := `
print "start"
# Fibers doing async work in a loop (churns the token system across goroutines).
fiber {macro (
  i: 0
  while (lt ~i, 5), (
    msleep 5
    i: {add ~i, 1}
  )
)}
fiber {macro (
  i: 0
  while (lt ~i, 5), (
    msleep 4
    i: {add ~i, 1}
  )
)}
# Meanwhile the main goroutine keeps defining macros (exercises module-env
# isolation) and spawning more fibers while the above are still running.
macro helperA( ret 1 )
fiber {macro ( msleep 6 )}
macro helperB( ret 2 )
fiber {macro ( msleep 3 )}
macro helperC( ret 3 )
fiber_wait_all
print "done"
`
	done := make(chan struct{})
	go func() {
		defer close(done)
		ps := newTestPS()
		ps.Execute(script)
	}()
	<-done // completes without deadlock/panic; -race flags any data race
}

// TestAttachWaitChanNoLostWakeup verifies attachWaitChan never leaves a caller
// blocked forever when the token has already completed/cleaned up before the
// attach (the lost-wakeup hang). The caller pattern is: attach, then <-waitChan.
func TestAttachWaitChanNoLostWakeup(t *testing.T) {
	ps := newTestPS()
	e := ps.executor

	// "already completed and removed" case: a token id that isn't active.
	waitChan := make(chan ResumeData, 1)
	e.attachWaitChan("fiber-0-token-does-not-exist", waitChan)
	select {
	case <-waitChan:
		// delivered — caller would proceed, not hang
	case <-time.After(2 * time.Second):
		t.Fatal("attachWaitChan on a completed/unknown token hung the caller (lost wakeup)")
	}
}

// TestChannelObjectSurvivesSenderDrop is an end-to-end smoke test through the
// real channel_send/channel_recv commands: it confirms an object round-trips
// intact through a channel and that the recv handler's transfer-ref release does
// not over-release or corrupt the value. (The precise use-after-free guard, with
// explicit refcount tracing, is TestChannelMessageRefLifecycle.)
func TestChannelObjectSurvivesSenderDrop(t *testing.T) {
	ps := newTestPS()
	ps.Execute(`ch: {channel 10}`)
	ps.Execute(`payload: {list "alpha", "beta"}`)
	ps.Execute(`channel_send ~ch, ~payload`)
	ps.Execute(`payload: 0`) // drop the sender's reference
	ps.Execute(`got: {channel_recv ~ch}`)
	ps.Execute(`result: {argv ~got, 2}`) // tuple = [senderID, value]; value at index 2

	v, ok := ps.GetRootState().GetVariable("result")
	if !ok {
		t.Fatal("result not set — received value was lost")
	}
	// The received value must still be the intact ["alpha","beta"] list even
	// though the sender dropped its reference before we received it. Resolve the
	// ObjectRef and inspect the underlying list.
	var objID int = -1
	switch rv := v.(type) {
	case ObjectRef:
		objID = rv.ID
	case Symbol:
		_, objID = parseObjectMarker(string(rv))
	case string:
		_, objID = parseObjectMarker(rv)
	}
	if objID < 0 {
		t.Fatalf("received value %v (%T) is not an object reference", v, v)
	}
	obj, exists := ps.executor.getObject(objID)
	if !exists {
		t.Fatal("received object was freed — send did not keep it alive")
	}
	list, ok := obj.(StoredList)
	if !ok {
		t.Fatalf("received object is %T, not a StoredList — object corrupted in channel", obj)
	}
	items := list.Items()
	if len(items) != 2 ||
		strings.TrimSpace(fmt.Sprintf("%v", items[0])) != "alpha" ||
		strings.TrimSpace(fmt.Sprintf("%v", items[1])) != "beta" {
		t.Errorf("received list = %v, want [alpha beta] (object corrupted/freed in channel)", items)
	}
}

// runScriptWithDeadlineOr fails the test if the script does not finish in time,
// which is how we detect a deadlock (the race detector cannot see deadlocks).
func runScriptWithDeadline(t *testing.T, name, script string, d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ps := newTestPS()
		ps.Execute(script)
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s: did not complete within %v — likely deadlock", name, d)
	}
}

// TestFiberAbandonNoDeadlock stresses ABBA #1: fibers are spawned and their
// handles immediately dropped (never fiber_wait'd), so the handle object can be
// freed (decrementObjectRefCount -> e.mu -> handle.mu) while the fiber's own
// completion defer runs (handle.mu -> e.mu). Many iterations to hit the window.
func TestFiberAbandonNoDeadlock(t *testing.T) {
	script := `
worker: {macro (
  bubble ok, "done"
  msleep 1
)}
i: 0
while (lt ~i, 300), (
  fiber ~worker
  i: {add ~i, 1}
)
fiber_wait_all
`
	runScriptWithDeadline(t, "fiber-abandon", script, 30*time.Second)
}

// TestSharedRandomRNGConcurrent drives multiple fibers pulling from the shared
// #random generator concurrently. #random is a single inherited token holding
// one *rand.Rand (not safe for concurrent use), so without a lock this races on
// the generator's internal state. Run with -race.
func TestSharedRandomRNGConcurrent(t *testing.T) {
	script := `
worker: {macro (
  i: 0
  while (lt ~i, 40), (
    r: {resume ~#random, 100}
    i: {add ~i, 1}
  )
)}
fiber ~worker
fiber ~worker
fiber ~worker
fiber ~worker
fiber_wait_all
`
	done := make(chan struct{})
	go func() {
		defer close(done)
		ps := newTestPS()
		ps.Execute(script)
	}()
	<-done
}

// objAlive reports whether an object id is still present (not freed).
func objAlive(e *Executor, id int) bool {
	_, ok := e.getObject(id)
	return ok
}

// objRefCount returns the refcount of a stored object (0 if freed/absent).
func objRefCount(e *Executor, id int) int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if o, ok := e.storedObjects[id]; ok {
		return o.RefCount
	}
	return -1
}

// TestChannelMessageRefLifecycle verifies the channel-message ref-counting fix:
// a sent object survives while buffered even after the sender releases it (the
// use-after-free), survives the recv handoff, and is ultimately freed with no
// leak once the receiver's copy is released.
func TestChannelMessageRefLifecycle(t *testing.T) {
	ps := newTestPS()
	e := ps.executor

	ch := NewStoredChannel(10)
	e.RegisterObject(ch, ObjChannel) // sets ch.executor

	// A payload object with one "sender holds it" reference.
	payload := NewStoredListWithoutRefs([]interface{}{"a", "b", "c"})
	pRef := e.RegisterObject(payload, ObjList)
	e.incrementObjectRefCount(pRef.ID) // sender's hold -> refcount 1

	// Send, then the sender's scope ends (drops its hold).
	if err := ChannelSend(ch, pRef); err != nil {
		t.Fatalf("send: %v", err)
	}
	e.decrementObjectRefCount(pRef.ID) // -> refcount 1 (channel's send-claim)

	// THE FIX: still alive despite the sender releasing it.
	if !objAlive(e, pRef.ID) {
		t.Fatal("payload freed while buffered — send did not claim a reference (use-after-free)")
	}

	// Receive it. The value must survive the handoff (transfer ref).
	_, got, err := ChannelRecv(ch)
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	gotRef, ok := got.(ObjectRef)
	if !ok || gotRef.ID != pRef.ID {
		t.Fatalf("recv returned %v, want payload ref %d", got, pRef.ID)
	}
	if !objAlive(e, pRef.ID) {
		t.Fatal("payload freed during recv handoff")
	}

	// Simulate the channel_recv handler taking ownership: tuple claims the value,
	// then the transfer ref is released.
	tuple := NewStoredListWithoutRefs([]interface{}{0, got})
	tRef := e.RegisterObject(tuple, ObjList) // claims payload
	releaseNestedReferences(got, e)          // release transfer ref
	if !objAlive(e, pRef.ID) {
		t.Fatal("payload freed after recv+claim; should be owned by the receiver's tuple")
	}
	if rc := objRefCount(e, pRef.ID); rc != 1 {
		t.Fatalf("payload refcount after handoff = %d, want 1 (owned solely by tuple)", rc)
	}

	// Receiver is done: freeing the tuple must cascade-free the payload (no leak).
	e.decrementObjectRefCount(tRef.ID)
	if objAlive(e, pRef.ID) {
		t.Fatal("payload leaked after its only owner (the tuple) was freed")
	}
}

// TestChannelCloseReleasesBufferedRefs verifies closing a channel with unread
// messages releases their send-claims (no leak), and freeing an abandoned
// (never-closed) channel does the same.
func TestChannelCloseReleasesBufferedRefs(t *testing.T) {
	ps := newTestPS()
	e := ps.executor

	// --- close path ---
	ch := NewStoredChannel(10)
	e.RegisterObject(ch, ObjChannel)
	p1 := e.RegisterObject(NewStoredListWithoutRefs([]interface{}{"x"}), ObjList)
	e.incrementObjectRefCount(p1.ID) // sender hold -> 1
	if err := ChannelSend(ch, p1); err != nil {
		t.Fatalf("send: %v", err)
	}
	e.decrementObjectRefCount(p1.ID) // sender drops -> 1 (send-claim)
	if !objAlive(e, p1.ID) {
		t.Fatal("payload freed while buffered")
	}
	if err := ChannelClose(ch); err != nil {
		t.Fatalf("close: %v", err)
	}
	if objAlive(e, p1.ID) {
		t.Fatal("closing the channel leaked the buffered message's reference")
	}

	// --- abandoned (freed without close) path ---
	ch2 := NewStoredChannel(10)
	ch2Ref := e.RegisterObject(ch2, ObjChannel)
	e.incrementObjectRefCount(ch2Ref.ID) // hold the channel -> 1
	p2 := e.RegisterObject(NewStoredListWithoutRefs([]interface{}{"y"}), ObjList)
	e.incrementObjectRefCount(p2.ID)
	if err := ChannelSend(ch2, p2); err != nil {
		t.Fatalf("send: %v", err)
	}
	e.decrementObjectRefCount(p2.ID) // -> 1 (send-claim)
	// Free the channel object without closing it.
	e.decrementObjectRefCount(ch2Ref.ID) // -> 0, channel freed
	if objAlive(e, ch2Ref.ID) {
		t.Fatal("channel not freed")
	}
	if objAlive(e, p2.ID) {
		t.Fatal("freeing an abandoned channel leaked the buffered message's reference")
	}
}

// TestChannelConcurrentEndpoints stresses a single logical channel from many
// goroutines through distinct subscriber endpoints. Because each endpoint holds
// only its own mutex while mutating the shared parent channel's Messages /
// Subscribers, the race detector flags this without the fix. Run with -race.
func TestChannelConcurrentEndpoints(t *testing.T) {
	main := NewStoredChannel(0)

	// Pre-create several subscribers.
	const nSubs = 6
	subs := make([]*StoredChannel, 0, nSubs)
	for i := 0; i < nSubs; i++ {
		s, err := ChannelSubscribe(main)
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		subs = append(subs, s)
	}

	var wg sync.WaitGroup
	const iters = 200

	// Each subscriber concurrently sends and receives on the shared channel.
	for _, s := range subs {
		wg.Add(1)
		go func(ep *StoredChannel) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				_ = ChannelSend(ep, i)
				_, _, _ = ChannelRecv(ep)
				_ = ChannelLen(ep)
			}
		}(s)
	}

	// The main channel also sends/broadcasts concurrently.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = ChannelSend(main, i)
			_, _, _ = ChannelRecv(main)
		}
	}()

	// Concurrent subscribe/disconnect churns the Subscribers map.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iters/4; i++ {
			s, err := ChannelSubscribe(main)
			if err == nil {
				_ = ChannelDisconnect(main, s.SubscriberID)
			}
		}
	}()

	wg.Wait()
}
