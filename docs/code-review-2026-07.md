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
**Three concurrency data races (fiber module-env, channel endpoints, token
system), a lost-wakeup hang, and a test-bug race are also FIXED** and the whole
Go suite is now race-clean (`src/concurrency_test.go`). The remaining
reading-only concurrency findings (deadlocks, ref-counting), the sandbox items,
and the broader test-coverage gaps remain open.

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

Not yet reproduced (reading-only findings; a `-race` sweep of the current
fixtures did not trigger them — they need targeted stress, and the deadlock /
ref-counting ones would not surface as data races at all):

- **ABBA deadlocks** between `FiberHandle.mu` ↔ `Executor.mu`
  (`fiber.go:94-141` vs `executor_objects.go:409-431`) and
  `ExecutionState.mu` ↔ `Executor.mu` (`state.go:405-497` vs
  `executor_tokens.go:498-522`). Because `Executor.mu` guards everything, either
  hangs the whole interpreter.
- **Channel messages don't claim references** (`channel.go:96` vs `SpawnFiber`'s
  explicit claim at `fiber.go:81`): a sent object can be freed and its ID reused
  before receipt → use-after-free / silent value swap.
- **Shared `#random` `*rand.Rand` used from multiple fibers without a lock**
  (`io_channels.go:456`, `lib_coroutines.go:379`): data race on RNG state.
- **`fiber_wait` re-merges `FinalBubbleMap` without clearing it**
  (`lib_fibers.go:162-185`): a second `fiber_wait ~f` double-releases bubble refs.
- **Result-set unlock/relock window double-releases refs** (`state.go:275-292`):
  two goroutines setting the result on one shared state release the same old refs.

## Sandbox / file access — hardening (LOW–MED)

- **Symlink bypass of file sandbox** (`lib_files.go:45` `validatePathAccess`):
  roots are enforced on the cleaned textual path only; no `filepath.EvalSymlinks`
  and no `O_NOFOLLOW`. A symlink inside an allowed root pointing outside it passes
  the prefix check, and `open`/`RemoveAll` follow it. Affects `file`, `rm`/`rmdir`.
- **`exec` empty-allowlist default is allow-all** (`lib_system.go:534`): the
  allow-list only applies `if len(ExecRoots) > 0`; empty means unrestricted
  process execution — the opposite of read/write roots, where empty means
  deny-all. Easy misconfiguration into arbitrary command execution.
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
