# PawScript code review — hardening findings (2026-07-12)

Scope: core interpreter under `src/` (parser, executors, stdlib, fiber/channel
concurrency) and the Go test suite. Focus was on crashes and hardening gaps
reachable from malformed or adversarial `.paw` scripts.

Key structural fact that amplifies everything below: **there is no `recover()`
anywhere in the core execution path** (only in tests and the GUI `main`). So any
panic in a command handler or the parser propagates uncaught and takes down the
interpreter — and any embedding host process with it. Go *fatal* errors (stack
overflow, OOM) can't be recovered at all.

Severity legend: **CRIT** = short script crashes the host irrecoverably ·
**HIGH** = short script panics/corrupts state · **MED** = correctness/leak under
specific conditions · **LOW** = hardening / defense-in-depth.

**Status (2026-07-12): CRIT-1, CRIT-2, HIGH-3, HIGH-4 and the BUILD item below
are FIXED**, with regression tests in `src/hardening_test.go` and
`tests/test_double_tilde.paw` / `tests/test_struct_bounds.paw` fixtures.
**The entire concurrency cluster is now FIXED**: three data races (fiber
module-env, channel endpoints, token system), a lost-wakeup hang, a `fiber_wait`
double-release, a channel-message use-after-free, the RNG race, the
`SetResultWithoutClaim` window, a test-bug race, and — via a repo-wide
lock-hierarchy audit — all 22 ABBA/self-deadlock lock-order violations. The whole
Go suite is race-clean and 98 `.paw` regressions pass. The embedded async-brace
hang ("the msleep hang"), the logger race, and the shared-variable-state race
are all FIXED, as is the residual set of *unlocked / wrong-mutex* direct
`variables`/`bubbleMap` accesses in the command handlers (now all routed through
the owner-aware API). Remaining open: the sandbox items and the broader
test-coverage gaps.

## Confirmed by execution (reproduced locally)

### CRIT-1 — `~~x` infinite recursion → uncatchable stack overflow  ✅ FIXED
`executor_resolution.go:156` (and the twin at `:277` in
`resolveTildeExpressionSilent`). The "chained tilde" branch re-prepends a tilde:
it calls `resolveTildeExpression("~"+rest, ...)` where `rest` already starts with
`~`, so the input never shrinks and the function recurses forever.
Repro: `echo ~~x` → `fatal error: stack overflow`. Constant-size input, no
variables needed, not catchable by `recover()`.
Note: `~~x` is a *valid feature* (one extra level of dereference — the value of
the variable whose name is the value of `x`); the bug was that it was never
reachable. Fixed by passing `rest` instead of `"~"+rest` at `:156` and `:277`,
which makes chained dereference work at arbitrary depth (`~~~x`, …).

### CRIT-2 — `repeat` count is unbounded → uncatchable OOM  ✅ FIXED
`lib_types.go:1733/1863/1874`. `count` is clamped `>= 0` but never upper-bounded.
String mode `strings.Repeat(str, count)` and block/list modes
`make([]interface{}, 0, count)` allocate on a script-controlled size.
Repro: `repeat "ab", 1000000000000` → `fatal error: runtime: out of memory`
(attempts a ~2 TB allocation). Not catchable.
Fix: cap `count` (and `len(items)*count`) to a sane maximum; return an error
instead of allocating.

### HIGH-3 — `struct_def` negative/huge field size → `makeslice` panic  ✅ FIXED
`lib_types.go` struct path → `types.go` `NewStoredStructArray`/`SetBytesAt` and
`lib_struct.go:105/120`. Field size flows unchecked into `make([]byte, size)`.
Repro: a field descriptor `("f", -1, "bytes")` fed to `struct_def` then `struct`
→ `runtime error: makeslice: len out of range`. A large size overflows the
offset accumulator / OOMs. Related: `SetBytesAt` (`types.go:2008`) bounds-checks
nothing on the write path (the read path `GetBytesAt` does), so a hand-crafted
definition list can drive an out-of-range write.
Fixed with defense in depth (new `MaxStructBytes` = 256 MiB cap):
- `struct_def` rejects negative field sizes and bounds the cumulative offset
  (int64) so it can't overflow negative.
- `struct` validates `__size` and the `__size * count` product (guards
  hand-crafted definition lists that bypass `struct_def`).
- `NewStoredStruct`/`NewStoredStructArray` clamp bad sizes so the `make()` can
  never panic/OOM as a last resort.
- `SetBytesAt` (write) and `GetBytesAt` (read) now full-bounds-check
  `start >= 0 && start+len <= len(data)`; `ZeroPadAt` skips out-of-range indices.
- `lib_struct.go` rejects negative field offset/length before the `make()`.

### HIGH-4 — bare NUL byte in an argument → slice-bounds panic  ✅ FIXED
`parseObjectMarker` (`state.go:589`): `middle := s[1:len(s)-1]`. For a one-byte
string `"\x00"`, `HasPrefix` and `HasSuffix` are both true (same byte), so the
slice becomes `s[1:0]` and panics. Reached from `processArguments` →
`splitAccessors` (`executor_commands.go:1138`) with attacker-derived symbols.
Repro: `echo \x00` or `set x, \x00` → `slice bounds out of range [1:0]`.
Fix: require `len(s) >= 2` at the top of `parseObjectMarker` (guards all callers).

### BUILD — orphan package breaks `go build ./...` / `go test ./...`  ✅ FIXED
`pkg/purfecterm-cli/input.go:10` references undefined `Terminal`. This is a
leftover from commit 6fd823a ("deleting purfecterm-cli, moving to other repo") —
the directory was not actually removed. It fails to compile, which breaks
whole-module build, vet, and test for anyone not building package-by-package.
Fix: delete `pkg/purfecterm-cli/`.

## Concurrency — fibers/channels/tokens under `go test -race`

The entire concurrency subsystem had **zero** `go test` coverage, so the race
detector had never seen the interpreter's own concurrent code. It does now:
`src/concurrency_test.go` drives fibers, channels, and the token system under
`-race`, and the **whole Go suite is race-clean**. Running the existing
`tests/test_fiber_concurrency.paw` fixture under a `-race` build was the ground
truth — it surfaced real races (below) that careful reading alone had missed.

**Status: the two originally-listed races plus a token-system race the fixtures
surfaced are FIXED and verified race-clean; a test-bug race is also fixed. The
remaining reading-only findings (deadlocks, lost-wakeup, ref-counting) were NOT
reproduced by the available fixtures — see the note at the end of this section.**

- ✅ **FIXED — Fiber child module env shared live maps with parent under
  different mutexes** (`module.go` COW aliasing). Once the parent had COW-copied,
  a later `macro` definition wrote the shared map in place while the fiber read
  it → `fatal error: concurrent map read and map write`. Reproduced in
  `TestFiberModuleEnvSharedMap`. Fix: `IsolateRegistriesForFiber()` gives each
  fiber private registry copies at spawn time (`fiber.go` SpawnFiber).
- ✅ **FIXED — Channel subscriber endpoints mutated the parent channel without
  its lock** (`channel.go`). `ChannelSend/Recv/Len/Close` locked the *endpoint's*
  `mu` but mutated the *parent's* `Messages`/`Subscribers`; two subscribers held
  different locks over the same buffer. Reproduced in
  `TestChannelConcurrentEndpoints`. Fix: all operations now serialize on the
  canonical (main) channel's mutex via `canonicalChannel()`.
- ✅ **FIXED — Token system iterated `e.activeTokens` without the lock.**
  `PopAndResumeCommandSequence` releases `e.mu` before running resumed commands
  and again after completing a token, then scanned `e.activeTokens` (the
  "is this state still in use?" check at the old lines 558/610) while unlocked —
  racing with fibers writing the map under the lock. This is what
  `test_fiber_concurrency.paw` tripped. Fix: `stateInUseByOtherToken` (lock-
  acquiring) / `stateInUseByOtherTokenLocked` helpers; the post-unlock scans now
  re-acquire `e.mu`. Guarded end-to-end by `TestFiberConcurrencyEndToEnd`.
- ✅ **FIXED (test bug) — `TestAsyncOperations` had an unsynchronized `completed`
  bool** (written by the async goroutine, read by the test), which made
  `go test -race` red and would have masked any real interpreter race in CI. Now
  an `atomic.Bool`.

- ✅ **FIXED — Lost wakeup in `attachWaitChan`.** Every async caller does
  `attachWaitChan(token, waitChan)` then blocks on `<-waitChan`. If the token
  completed in the window before the attach, its completion could not signal the
  (not-yet-attached) channel, and the old code's "token not found → warn" left
  the caller blocked forever. `attachWaitChan` now delivers immediately when the
  token is already completed or gone, so the caller proceeds instead of hanging.
  One choke point protects all 25+ call sites. Guarded by
  `TestAttachWaitChanNoLostWakeup` (verified it hangs without the fix).

- ✅ **FIXED — `fiber_wait` re-merged `FinalBubbleMap` without clearing it**
  (`lib_fibers.go`). It transferred bubble ref ownership to the caller (claim for
  caller, `decrementObjectRefCount` for the fiber's copy) but left
  `FinalBubbleMap` populated, so a second `fiber_wait` on the same handle
  double-released. Now takes a write lock and clears `FinalBubbleMap` after the
  transfer (also fixes the RLock-during-mutation).

- ✅ **FIXED — Channel messages now claim references (was a use-after-free).**
  `channel_send` buffered the value without claiming a ref (contrast `SpawnFiber`,
  which claims fiber args), so sending an object and letting the sender's scope
  release it could leave the buffered message pointing at a freed/reused ID.
  Fix (mirrors the fiber-arg claim/release discipline):
  - `RegisterObject` gives each channel an `executor` handle.
  - `ChannelSend` claims the value before buffering; the claim is released when
    the message leaves the buffer (`ChannelRecv` cleanup), the channel closes
    (`ChannelClose` releases all still-buffered messages), or an abandoned
    channel is freed (new `StoredChannel` case in `decrementObjectRefCount`).
  - `ChannelRecv` claims a **transfer reference** on the returned value *before*
    the cleanup release, so the value can't be freed mid-handoff even when the
    same recv removes it from the buffer; the `channel_recv` handler releases
    that transfer ref once it has taken ownership via `RegisterObject`.
  - Lock ordering: send/recv/close take `channel.mu` then `e.mu`; the free path
    never takes a channel mutex (refcount 0 = exclusive), so there is no cycle.
  Verified by `TestChannelMessageRefLifecycle` (explicit refcount tracing:
  survives sender-drop, survives recv handoff, freed with no leak) and
  `TestChannelCloseReleasesBufferedRefs` (close + abandoned-free paths); both
  fail without the fix.

- ✅ **FIXED — ABBA deadlocks + self-deadlocks (`FiberHandle.mu` / `state.mu` /
  `Executor.mu`).** A full lock-nesting audit found 22 lock-order violations, and
  worse than ABBA: several paths held `e.mu` and called a state/token method that
  re-acquires `e.mu` (non-reentrant → guaranteed self-deadlock, e.g. a parallel
  async brace whose brace owns an object hung outright). Fixed by enforcing one
  hierarchy repo-wide — **`handle.mu` (outermost) < `state.mu` < `e.mu`
  (innermost)** — and moving every reverse acquisition out: `orphanFiberBubbles`
  drops `e.mu` before taking `handle.mu` (in `decrementObjectRefCount`/
  `RefRelease`); `GetSuspendedFibers` snapshots handles first; `fiber_bubble`
  reorders to `handle`-before-`state`; token code captures `Snapshot`/result
  before/outside `e.mu`; `ResumeBraceEvaluation` and the `forceCleanupTokenLocked`
  cleanup path defer all `ClaimObjectReference`/`ReleaseAllReferences` until after
  `e.mu` is released (new `transferBraceOwnership` + a pending-release list). A
  second independent audit of the other 15 files confirmed **zero remaining
  violations**. Guarded by `TestFiberAbandonNoDeadlock` and
  `tests/test_parallel_brace_object.paw` (the latter hangs on the pre-fix code).
- ✅ **FIXED — `SetResultWithoutClaim` double-release window.** It released the
  old refs before swapping in the new value, so a concurrent reader saw the stale
  `currentResult` and re-released it. Now the new value is set before the old
  refs are released (and the release happens outside `state.mu`).
- ✅ **FIXED — shared `#random` `*rand.Rand` race.** `#random` is one inherited
  token holding a single `*rand.Rand` (not concurrency-safe); concurrent fibers
  pulling from it raced on its internal state. Added a mutex to `IteratorState`
  guarding the three `Int63`/`Int63n` sites. Guarded by
  `TestSharedRandomRNGConcurrent`.

## Newly discovered while stress-testing (pre-existing, separate subsystems)

- ✅ **FIXED — Logger data race + cross-contamination under concurrent fibers.**
  `Logger` held a single shared `outputContext` field that `SetOutputContext`
  overwrote per execution scope; concurrent fibers both data-raced on it and
  cross-contaminated routing (one fiber's log could go to another's #out/#err).
  Now scoped **per goroutine** (a `sync.Map` keyed by goroutine id, plus a
  bound-context path for `WithContext`); the hot logging path skips the
  goroutine-id lookup entirely via an atomic active-context counter when no
  context is set. Guarded by `TestLoggerConcurrentContexts`.

- ✅ **FIXED — Shared-variable brace states shared a map but not a mutex.**
  `NewExecutionStateFromSharedVars` gave a brace state the parent's
  `variables`/`bubbleMap` maps by reference but its own mutex, so an async brace
  resuming on a completion goroutine and the owning fiber's macro cleanup
  (`executeStoredMacro`) locked *different* mutexes over the same `variables` map
  → data race. Fixed by adding a `varsOwner` field: brace states point at the
  state that owns the shared maps, and every variable/bubble method plus the
  macro-cleanup loop lock `varsMutexOwner().mu`, so all access to a shared map
  serializes on one mutex. Own fields (`currentResult`/`ownedObjects`) keep the
  state's own `mu`; no method mixes shared and own fields under one lock.
  Verified by `TestSharedVarStateConcurrent` (fails without the fix) and
  extensive end-to-end fiber+brace `-race` stress.

- ✅ **FIXED — residual unlocked/wrong-mutex `variables`/`bubbleMap` accesses.**
  Follow-up to the shared-var fix: a set of command handlers reached the
  `variables`/`bubbleMap` maps *directly* — either with no lock or with the
  state's own `mu` instead of the owner's — so a shared brace state could tear
  the map under `-race`. All are now routed through the owner-aware API:
  - `fizz`/`burst` current-bubble variable → `SetVariable`/`GetVariable`
    (`lib_core.go`); the generator continuation twin likewise (`lib_coroutines.go`).
  - 25 generator/`for`/`repeat`/`fizz` `bubbleMap` resets in `lib_coroutines.go`
    → `ResetBubbleMap()`.
  - `fiber_wait` / `fiber_wait_all` / `fiber_bubble` bubble merges
    (`lib_fibers.go`): the map append goes through `MergeRawBubbles()` /
    `TakeBubbleMap()` (owner mutex) while the object-ref transfer stays under the
    state's own `mu` (which guards `ownedObjects`).
  - `bubble_orphans` merge (`lib_core.go`) → `MergeRawBubbles()`; host cleanup
    orphan-transfer and bubble dump (`pawscript.go`) → `TakeBubbleMap()` /
    `GetBubbleMap()`.
  - `MergeBubbles` dropped its unlocked `child.bubbleMap == nil` fast-path read.
  New helpers `ResetBubbleMap`/`MergeRawBubbles`/`TakeBubbleMap` on
  `ExecutionState` all lock `varsMutexOwner().mu`. Behavior is unchanged (the
  fixes only correct *which* mutex guards each access). Guarded by
  `TestSharedBubbleMapHelpersConcurrent` (verified non-vacuous: reverting one
  helper to the state's own mutex trips `-race`), plus the full `-race` suite,
  98 `.paw` regressions, and a fiber→parent bubble-object `-race` stress.
- ✅ **FIXED — async brace substitution hang ("the msleep hang").** A command
  with an async brace (`echo {msleep 2; ret "x"}`, or an async brace inside a
  `while` loop) returns a brace-coordinator token that the caller
  (`Execute`/`WaitForToken`, or the while-loop async handler) blocks on via a
  wait channel. `finalizeBraceCoordinator` ran the command (output appeared) and
  completed the coordinator token but **never signaled that token's wait
  channel** — so the caller blocked forever even though the work was done. (The
  `attachWaitChan` lost-wakeup guard only masked it when the coordinator happened
  to finish before the attach; when the attach won the race it hung — which is
  what showed up in the wild.) Fixed: `finalizeBraceCoordinator` now captures the
  coordinator's wait channel and delivers the result to it when there is no
  downstream chained token (or propagates it to the new token if the callback
  itself went async). Guarded by `TestAsyncBraceExecuteCompletes` and
  `tests/test_async_brace_while.paw`.

## Sandbox / file access — hardening (LOW–MED)

- ✅ **FIXED — Symlink bypass of file sandbox** (`lib_files.go` `validateFileAccess`).
  Roots were enforced on the cleaned textual path only (no symlink resolution), so
  a symlink inside an allowed root pointing outside it passed the prefix check and
  `open`/`RemoveAll` followed it. Fixed by adding a `FollowSymlinks bool` to
  `FileAccessConfig` (**default false = secure**). When disabled, after the textual
  root check the validator resolves the path's real location
  (`filepath.EvalSymlinks`, walking up to the deepest existing ancestor for
  not-yet-created leaves) and requires it to stay within the *resolved* roots — so
  a symlink that escapes a root is denied, while a symlink that stays in-jail (or a
  root reached through a symlink like `/tmp`→`/private/tmp`) still works. When
  enabled, symlinks are followed wherever they point (operator opts into trusting
  root contents). The whole path-validation moved into the package-level
  `validateFileAccess(config, path, needsWrite)` so it is unit-testable; every
  `file`/`rm`/`rmdir`/`mkdir`/`list_dir`/`file_*` command already funnels through
  it, so the guard is applied at a single chokepoint. Opt-in via `--follow-symlinks`
  / `PAW_FOLLOW_SYMLINKS=1`.

  **Security invariant behind the option:** following symlinks is only safe
  because a sandboxed script *cannot create* a symlink — PawScript exposes no
  link-making command and no `os.Symlink`/`os.Link` call exists in the
  interpreter, so a script cannot plant an escaping symlink itself; the guard only
  has to defend against symlinks already present in the roots. This is locked down
  by `TestNoSymlinkCreatingCommand`, which fails if any link-creating command is
  ever registered. Guarded by `lib_files_test.go` (escape denied/allowed by mode,
  in-jail symlink allowed, create-through-escaping-parent denied, leaf-symlink
  write denied) — verified non-vacuous by disabling the guard — plus an
  end-to-end `--sandbox` escape check through the `paw` binary.

  *Known limitation:* the check is resolve-then-open, so a symlink swapped in by an
  **external** process between validation and the `os` call could still be followed
  (classic TOCTOU). That is out of the script sandbox's threat model — the script
  is the adversary and it cannot create symlinks; fully TOCTOU-safe traversal would
  need `openat2(RESOLVE_BENEATH)`/`O_NOFOLLOW` component walks.
- **`exec` empty-allowlist default is allow-all** (`lib_system.go:534`): the
  allow-list only applies `if len(ExecRoots) > 0`; empty means unrestricted
  process execution — the opposite of read/write roots, where empty means
  deny-all. Easy misconfiguration into arbitrary command execution. **Note the
  interaction with the symlink guard:** an enabled `exec` is also the one way a
  script *could* create a symlink (`ln -s`, `mklink`), which would defeat
  `FollowSymlinks=true`. Exec is a strictly larger capability than symlink
  creation (it is arbitrary code execution), so this is governed by the exec
  allow-list, not the file sandbox; fixing the empty-allowlist default to
  deny-all would also close the exec-based symlink-creation path.
- **No recursion depth limit in `json`** (`lib_core.go` ~1047): deeply nested
  input can exhaust the stack.

## Test suite

- **Coverage ~11.6% of statements.** The real regression corpus (~112 `.paw`
  fixtures under `tests/`) runs only via `tests/test_regressions.sh` against a
  prebuilt binary — invisible to `go test`, `-race`, and coverage. Highest-value
  cheap win: a table-driven Go test that loads `tests/*.paw` + `*.expected`, which
  puts the whole corpus under `-race`/coverage at once.
- **A data race lives in a test** (`pawscript_test.go:244` write vs `:259` read of
  `completed`), which makes `go test -race` red and would mask any *real* future
  interpreter race in CI. Also uses `time.Sleep(50ms)` instead of waiting on the
  returned token.
- **Tests that panic instead of failing**: `pawscript_test.go:40-53` indexes
  `receivedArgs[0..2]` after a non-fatal `t.Errorf`; unchecked
  `GetResult().(string)` assertions at `exec_test.go:33/49/179`.
- **Vacuous / weak assertions**: `exec_test.go:54-60` claims to check all args but
  only asserts `arg1`; `pawscript_test.go:389-393` block-comment test passes even
  if `)#` termination is broken.

### Top coverage gaps (ranked)
1. Fibers/coroutines/channels — no `go test` at all (never seen by `-race`).
2. Malformed input — no Go tests for unterminated strings/comments, unbalanced
   `{}`/`()`, NUL bytes, `~~x`. Fixtures exist but only run via the shell script.
3. File I/O commands (`lib_files.go`) — `rm`/recursive-remove untested.
4. Token/async lifecycle beyond the happy path — double-resume, never-resumed
   (does `Execute` hang?), cancellation/timeout.
5. Core language semantics (assignment, if/while, objects, type system,
   substitution) — 0% via `go test`.
