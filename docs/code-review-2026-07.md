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
the owner-aware API). The file-sandbox **symlink escape**, the **`exec`
empty-allowlist allow-all** (Model B: exec fail-closed), the **`include`
sandbox bypass** (now gated by a dedicated `IncludeRoots`), and the **`json`
serialize recursion depth** (now guarded) are also FIXED. Remaining open: the
broader test-coverage gaps.

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
- ✅ **FIXED — `exec` empty-allowlist default was allow-all** (`lib_system.go`
  `validateExecAccess`). The allow-list previously only applied `if
  len(ExecRoots) > 0`, so an empty/nil `ExecRoots` meant *unrestricted* process
  execution — the opposite of read/write roots — and it also silently skipped the
  write-then-execute overlap guard. Fixed with **Model B (exec is fail-closed):**
  when a `FileAccess` sandbox is configured, `exec` is deny-by-default —
  empty **or** nil `ExecRoots` permits nothing; a command must resolve within an
  explicitly-listed `ExecRoot` and must not sit inside a `WriteRoot`. Fully
  unrestricted exec is only reachable when `FileAccess == nil` (the
  `--unrestricted` mode). Read/write keep their existing nil=unrestricted
  semantics; only exec is made special, because arbitrary command execution
  defeats all file sandboxing and so warrants opt-in. Path-based only (no loader/
  exec-resolver callback). Re-grant paths: list the binary's directory in
  `ExecRoots` (`--exec-roots`, `PAW_EXEC_ROOTS`, or embedder config), or
  `--unrestricted` for fully open.

  The validation moved into package-level `validateExecAccess(config, cmdName,
  resolvedCmd)` (unit-testable, reuses `pathWithinRoots`). Guarded by
  `lib_system_test.go` (deny-by-default for nil and empty ExecRoots — verified
  non-vacuous; allow-within-root; outside-root denied; write/exec overlap denied;
  not-found; PATH-lookup branch) plus an end-to-end check through the `paw`
  binary: piped stdin (nil ExecRoots) now denies exec, and `PAW_EXEC_ROOTS` /
  `--unrestricted` re-grant it. Full `-race` suite and 98 `.paw` regressions pass.

  This also closes the exec-based symlink-creation path relative to
  `FollowSymlinks=true`: with exec deny-by-default, a script can't reach `ln -s`
  unless the operator explicitly lists an exec root containing it.
- ✅ **FIXED — `include` bypassed the file sandbox** (`lib_core.go`). The
  `include` command read module files with a bare `os.ReadFile(filename)` — no
  root check at all (so it ignored `ReadRoots`) and it resolved relative paths
  against the process CWD instead of `ScriptDir`. Fixed by giving `include` its
  own **`IncludeRoots`** on `FileAccessConfig` and routing it through
  `validateIncludeAccess` (the shared resolver/`enforceRoots` core, so it gets the
  same symlink guard and `ScriptDir`-relative resolution as `file`). Loading code
  is a distinct trust axis from reading data — an app can restrict `ReadRoots` to
  a data path while loading its own modules from `IncludeRoots` — so it is a
  separate root set, **not** folded into `ReadRoots`. Semantics match read/write
  roots: nil = unrestricted (back-compat), empty = deny-all, listed = only within.
  The bundled apps default `IncludeRoots` to the script's own directory
  (`--sandbox` → the sandbox dir; `PAW_INCLUDE_ROOTS` / `--include-roots` to
  extend). Guarded by `lib_files_test.go` (root semantics; symlink escape;
  **independence from `ReadRoots`** — a module dir is includable-but-not-readable
  and a data dir is readable-but-not-includable, verified non-vacuous) plus an
  end-to-end check through the `paw` binary (own module loads, outside file
  denied, relative include now resolves against `ScriptDir`, data/module split).
  Strictly path-based — no pluggable module-loader callback (a possible future
  feature for host-served modules, deliberately out of scope here).
- ✅ **FIXED (guarded) — recursion depth in `json` serialize** (`lib_core.go`).
  The `json` command serializes via mutually-recursive `toJSONValue`/`listToJSON`
  that follow reference markers (`getObject`) with no depth cap, so a
  pathologically deep value graph could overflow the goroutine stack (an
  uncatchable Go fatal). Added a `maxJSONDepth` (10000, matching
  `encoding/json`'s own parse cap) guard: `toJSONValue` is the choke point every
  nested value flows through, so one captured counter (`depth++`/`defer depth--`)
  bounds the whole recursion with no signature or call-site churn — past the cap
  it returns an error that propagates out as a normal command failure.
  Reachability is low and now moot: the *parse* direction was already bounded by
  Go's `Unmarshal` 10000 cap, and **list cycles cannot form** — `StoredList` is a
  copy-on-write value type (value receivers, fresh backing array on `Append`, no
  in-place `.items`/`.namedArgs` writes, no element-set command), so the object
  graph is always acyclic and a depth bound needs no visited-set. Building a truly
  cap-deep graph via the interpreter is O(n²) (a linear `findStoredListID` scan
  per claim) and impractical anyway. Guarded by `TestJSONDepthGuard` (lowers the
  cap transiently and checks deep-rejected / shallow-ok).

## Test suite

- ✅ **DONE — table-driven corpus test** (`corpus_test.go` `TestCorpus`). Loads
  every `tests/*.paw` with a matching `*.expected`, runs it in-process (combined
  stdout+stderr via a synchronized writer, config mirroring the `paw` binary's
  default sandbox and `-O1`), and diffs against `*.expected`. Puts the whole
  corpus under `go test`/`-race`/coverage at once: **coverage jumped from ~18.5%
  (Go tests alone) to 51.3%**. 96 fixtures run; 2 are skipped for the
  os.Stdout/Stderr-bypass bug below, and 3 misnamed `*.paw.expected` fixtures are
  skipped exactly as `test_regressions.sh` skips them (see below).

### Discrepancies found while building the corpus test (to investigate)
- **`-O0` vs `-O1` produce different *output*, not just speed.** With
  `OptLevel: OptimizeNone` (0), `escape.paw` line 24 (`echo "...", ${set_result
  ~list}`) raises `Cannot follow symbol with symbol in a single argument`; with
  `OptimizeBasic` (1, the binary default) it runs clean. The two AST paths should
  be behavior-identical — a real correctness bug in one of them. The corpus test
  pins `-O1` to match the `.expected` baseline.
- **Output paths that bypass `Config.Stdout`/`Stderr`.** `module.go:485` and
  `:559` (`getScopedCommand`/`getScopedMacro`) write `"[ERROR] Module not found"`
  straight to `os.Stderr` (note the bare `[ERROR]` vs the logger's
  `[PawScript:...]`), and `terminal.go:911` writes the terminal-reset sequence via
  `fmt.Print` (os.Stdout). These bypass the configured writers, so an embedder who
  redirects output misses them — and they make `test_scope_operator.paw` /
  `test_terminal_cursor.paw` non-reproducible in-process (hence skipped). The
  `module.go` ones look like leftover diagnostics (the caller already reports the
  proper error via the logger).
- **3 misnamed fixtures** (`test_json.paw.expected`,
  `test_list_from_json.paw.expected`, `test_pretty.paw.expected`) use a double
  `.paw.expected` extension, so both the shell runner and the corpus test look for
  `<name>.paw.paw` and skip them — those three scripts are effectively untested.
  Renaming to `<name>.expected` would fold them in (pending a check that they
  still match).
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

## Performance

Profiled via `BenchmarkHotLoop` (`bench_test.go`) and a CPU profile over the
corpus. A representative 400-iteration loop does ~1000 allocs/iter, so allocation
pressure (GC ≈ 26% of a corpus run) dominates. **Cumulative effect of the fixes
below: the hot-loop bench went from 88 ms/op to ~39 ms/op (~2.2×) and from ~430k
to ~206k allocs/op.**

- ✅ **FIXED — `applySyntacticSugar` recompiled a regexp per command** (the single
  biggest win). It called `regexp.MustCompile(...)` on *every* invocation
  (`executor_commands.go`), recompiling the `identifier(` matcher for each command
  executed. Hoisted it to a package-level `var syntacticSugarCallRe`. **~45%
  faster (70 → 39 ms/op), ~62% less memory (22 → 8.3 MB/op), ~40% fewer allocs
  (342k → 206k)** — behavior identical (same pattern, compiled once). A grep
  confirmed this was the only `regexp.MustCompile` left in a hot path. **Caveat on
  the headline number:** the bench is a command-execution-bound tight loop, a best
  case (the regex compiled once per command). On the varied 96-script corpus the
  same fix is ~7–8% (≈1.24 → 1.14 s), since real scripts spend time on parsing,
  I/O, macros, and fibers too. The win scales with command-execution density.
- ✅ **FIXED — `SourceMap.OriginalLines` split on every parse.** `NewSourceMap`
  eagerly `strings.Split`-ted the source into lines, but only error paths use them
  (for context). Made it a lazy method (`parser.go`) that splits on first use.
  ~6k fewer allocs; matters because every re-parsed brace built a fresh source map.
  (An attempt to also make `TransformedToOriginal` a value map to kill the
  per-character `*SourcePosition` alloc was **reverted** — it cut alloc *count* but
  raised bytes and wall-time, since the ~56-byte struct in map buckets costs more
  than the pointer it replaced.)
- ✅ **FIXED — `Logger.DebugCat` formatted then discarded.** It ran
  `fmt.Sprintf(format, args...)` unconditionally before `Log` decided (usually) to
  drop the message — across ~294 hot-path call sites with debug off. Gated it: when
  no per-goroutine output context is active (`boundContext == nil &&
  activeContexts == 0`, the common case), it applies the legacy `shouldLog` check —
  the exact decision `Log` makes on its `octx == nil` path — and skips the Sprintf.
  When a context is active it falls through unchanged, so behavior is identical.
  **~18% faster and ~44k fewer allocs** on the hot-loop bench (88 → 71 ms/op).
- ✅ **FIXED — `findStoredListID` hand-rolled bubble sort** (`executor_objects.go`).
  Replaced the O(n²) sort with `sort.Ints` (deterministic ID selection is required —
  claim/release must resolve an aliased backing array to the same ID). Low
  real-world impact (not in the corpus hot path) but it removes a latent O(n²) per
  refcount; the function's remaining O(n) linear scan still makes pathological deep
  reference chains O(n²) to build — a full fix needs an ID reverse-index.
- ✅ **FIXED — `ExecuteWithState` re-parsed on every call.** Repeated
  `ExecuteWithState` of the same string (most visibly a loop *condition*, re-checked
  every iteration — `lib_core.go:2403`) re-ran `RemoveComments` +
  `NormalizeKeywords` + `ParseCommandSequence` each time. Added a bounded
  (`maxParseCacheEntries`), mutex-guarded parse cache on the `Executor`, used only
  for the offset-free path (offsets mutate command positions, so only the
  un-adjusted parse is shareable). Cached commands get their brace/template caches
  pre-filled before storage, so concurrent reuse from fibers is read-only — the same
  discipline `GetOrParseMacroCommands` uses (validated race-clean). ~13k fewer
  allocs on the hot-loop bench.
- ✅ **FIXED — `protectEscapeSequences` rebuilt every string** (`executor_substitution.go`).
  It ran on every substitution, converting to `[]rune` and rebuilding via `append`
  even when there was nothing to change. It only rewrites backslash escapes
  (`\$`/`\~`/`\?`), so a `no-backslash → return str` fast path (the common case)
  skips it entirely. It was the **top allocator** in the hot loop; ~23k fewer allocs.
- **Biggest remaining opportunity — loop *bodies* re-parse every brace every
  iteration — ATTEMPTED, reverted; needs deeper work.** A cached loop body runs
  each command via `executeParsedCommand(cmd, state, nil)` (`lib_core.go:2488`)
  with a **nil** `substitutionCtx`. The fast pre-parsed-template path
  (`executor_commands.go:324` → `ApplyTemplate`) needs
  `substitutionCtx.CurrentParsedCommand.CommandTemplate != nil`, so a nil ctx falls
  through to `applySubstitution` → `substituteBraceExpressions` (~67% of the hot
  loop), which re-scans/re-parses each `{...}`.

  **The reroute was tried:** pre-cache the block's `CommandTemplate`s (a clean
  no-op on its own — verified) **and** have `executeParsedCommand` synthesize a
  minimal ctx (with `CurrentParsedCommand` set) when the command has a template so
  the dispatch takes `ApplyTemplate`. Result against the corpus:
  - Execution and **scope are fine** — variables resolve correctly (the synthesized
    ctx shares `ExecutionState`).
  - But **error-position columns regress** for body commands (`bad-command-in-brace`,
    `bubble`: reported `column 1` instead of the true `column 16`). A body command's
    `Position` is relative to the *block* string, so the offsets carried by the
    real loop context aren't reproduced by a synthesized ctx.
  - And **`demo.paw` infinite-recurses** (goroutine stack overflow) — its nested
    `macro`-definition bodies interact badly with routing block commands through
    the template path.

  So the win is real (~67% of the hot loop) but the template/position/dispatch
  machinery is coupled in ways a synthesized context doesn't satisfy — the loop
  body genuinely needs the loop's own positional/execution context, not a fresh
  one. A correct version must thread the *actual* loop substitution context
  (offsets + parsed-command) into body execution rather than fabricating one, and
  reconcile the nested-macro-definition recursion. Left as a deliberate follow-up;
  the safe wins above stand.
