package pawscript

import (
	"fmt"
	"sync"
	"testing"
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
