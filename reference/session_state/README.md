# reference/session_state

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for fixture ownership,
dual-mode proof, and external command deadlines. This package owns the existing
settings, timeout, physical-session adapter, and direct-message batch witnesses.
Its TestMain delegates to fixture.Main; no package-local setup authority is added.
SQL prepared/DISCARD consumers and all server/protocol tests remain in
[reference/sessions](../sessions/README.md#testing).

### Direct-message selected proof

The shared, unfiltered driver-batch definitions cover effects/counts (B1), result
sets (B2), parsing/prepared boundaries (B3), error timing/rollback (B4), transaction
boundaries (B5), clocks/settings/timeouts (B6), cancellation/lifecycle (B7), the
complete supported utility inventory (B8), diagnostics/authorization (B9),
retained block controls (B10), and the public feature smoke.
Run each selector below in native mode and again with `REFERENCE=1` against the
real pinned PostgreSQL18.0 UTF8/C oracle before review:

```sh
make check RUN='^TestReferenceDriverBatch(EffectsCounts|ResultSets|ParsePrepared|ErrorTimingRollback|FeatureSmoke)$'
make check RUN='^TestReferenceDriverBatch(TransactionBoundaries|ClockSettingsTimeouts|CancellationLifecycle)$'
make check RUN='^TestReferenceDriverBatch(UtilityAtomicity|DiagnosticsAuthorization|RetainedBlockControls)$'
make check RUN='^TestReference(DeleteCoreDiagnosticBoundaries|InsertConflictPredicateSuffix|RowsMetadataAndCommandSemantics)$'
make check RUN='^TestReferenceDriverBatch(PreparedBoundaries|InitialPrepare|Refresh(Composition|Atomicity|Promotion|Authorization|Cancellation|Controls))$'
```

These are shared acceptance definitions, not comparison-only parity assertions.
Utility observations use supported metadata and actual command/authorization
consumers rather than inventing pg_roles/pg_namespace/pg_attribute/pg_constraint/
pg_type or has_* inquiry support. Enum/composite/plain-domain messages create and
consume real typed destinations; missing-type CREATE TABLE probes establish
rollback and private-peer invisibility, and committed peers use the published type.
Class probes use name/kind plus the independently captured raw public namespace.
Domain CASCADE asserts actual exposed SELECT * columns/values, restored rollback
shape and missing-column42703. Physical relnatts retains dropped attribute slots
and is not an exposed-column counter.
Identity names remain NAME; raw generated OIDs are formatted in Go, not via new
casts. The renamed-column dependent view must display its current physical name
and still return its frozen output label/value. Selected summary regressions:
`make check RUN='^TestReferencePGTableViews'`, also with `REFERENCE=1`.
Native REINDEX peers read the old committed value before COMMIT and the final
value afterward; PostgreSQL independently observes its actual utility wait.
Column rename uses PostgreSQL's actual dependent-view wait before commit/join.
Native peers retain the old nonblocking catalog summary, but the dependent-view
consumer also waits: a bounded held-conflict deadline proves exclusion before
COMMIT, and the final consumer returns the new value afterward.
Other peer reads queued behind utility locks are released by commit before
joining; cancellation uses a held conflicting lock and bounded native deadline,
with PostgreSQL's public actual-wait observation.
Notice callbacks run only after a message and cannot synchronize its active wait.

The detached-private-role prepared COMMIT/ROLLBACK subcases are part of
RetainedBlockControls. They capture the real private role OID independently before
PREPARE, assert exact current_user/session_user and ordinary authorization
diagnostics afterward, then assert separate prepared finish and SET ROLE NONE
recovery. No discovery filter remains. A narrow selector must escape the dollar
before slash through make; unescaped `$/` can silently select no test:

```sh
make check RUN='^TestReferenceDriverBatchRetainedBlockControls$$/detached_private_role_prepared$$'
make check REFERENCE=1 RUN='^TestReferenceDriverBatchRetainedBlockControls$$/detached_private_role_prepared$$'
```

PreparedBoundaries pins first/post-COMMIT/post-ROLLBACK completion rejection
before lookup for existing and nonexistent GIDs, immutable prepared entries,
independent prefix disposition and unchanged singleton completion. InitialPrepare
pins warning25P01 plus an actual prepared empty implicit segment, independent
successful/failing suffixes and the singleton idle warning/no-entry contrast.

The six Refresh definitions retain independent duplicate/NULL row bags, DATA/
NO DATA population, source writes, private CREATE, repeated replacement, pending
role/MAINTAIN and MV-owner population, promotion before BEGIN, late rollback,
held-conflict cancellation and detached output across actual native control epochs.
Controls uses the unwrapped native pool sharing the notice connector's engine,
never the wrapped recording pool. PostgreSQL independently observes real waits
and57014; native uses its held-conflict bounded caller deadline. Source and MV
logical indexes publish atomically with their respective rows. Concurrent
retained refresh is also supported; genuine explicit-block and Restore refresh
exclusions remain intact.
Supplemental TestDriverBatchMaterializedReplacementRoots and
TestDriverBatchMaterializedPublicationGuards inspect first committed versus
immediate private predecessor, generation/end/root/index/role/TxRef/epoch/cancel
fences and row births at true end. TestDriverBatchMaterializedPromotionSavepoint
pins the retained replacement across genuine promotion, failure and mark recovery.
The shared `TestReferenceMaterializedRefreshConcurrentRetained` and
`TestReferenceMaterializedRefreshConcurrentRetainedCancellation` definitions
independently cover multiple targets plus RR old-root retention, private CREATE
and NULL-image matching, late suffix rollback, BEGIN promotion/rollback,
MV-owner runtime23505 disclosure and real source-wait cancellation. Native
Dump observations use the unwrapped pool belonging to the same fixture engine.

`TestReferenceMaterializedRefreshConcurrentRetainedSourceMaterialized` adds a
retained outer refresh waiting on an ordinary source while a peer concurrently
publishes its inner MV. Two MV aliases and a changed ordinary source distinguish
one active producer snapshot from per-source recapture: the first/private/final
outer output stays old, and a fresh refresh reads both new sources. A late-error
variant rolls back the retained marker write while preserving the peer's commit.
`TestMaterializedRefreshConcurrentRetainedSourceCapture` exercises actual source
and retained-stage capture, generated source-only fences, one physical frontier,
and unchanged strict target/index/ordinary/provider predecessor observations.
The actual-function `WorkBound` regression bounds real cancellation checkpoints
and comparator work for large valid composite NULL-key groups and duplicate
preflight. `DuplicateDiagnosticOrder` keeps the earliest producer diagnostic
even when key sorting visits a later producer's duplicate group first.

### Testing retained concurrent refresh

```sh
make check RUN='^TestReferenceMaterializedRefreshConcurrentRetained|^TestReferenceDriverBatchRefresh'
make check REFERENCE=1 RUN='^TestReferenceMaterializedRefreshConcurrentRetained|^TestReferenceDriverBatchRefresh'
make check RUN='^TestMaterializedRefreshConcurrent|^TestDriverBatchMaterialized'
make check RACE=1 RUN='^TestReferenceMaterializedRefreshConcurrentRetained|^TestMaterializedRefreshConcurrent'
make check RUN='^TestMaterializedRefreshConcurrent(WorkBound|DuplicateDiagnosticOrder|RetainedSourceCapture)'
make check RUN='^TestReferenceMaterializedRefreshConcurrentRetainedSourceMaterialized'
make check REFERENCE=1 RUN='^TestReferenceMaterializedRefreshConcurrentRetainedSourceMaterialized'
```

Each selected invocation has an external900-second deadline. No public SQL
definition is duplicated in native-only tests.

The focused
TestDriverBatchPolicyIdentityPublication inspects actual private CREATE→ALTER/
rename OID retention, committed-root isolation, final installation and late
rollback/dependency closure; it does not add native pg_policy inquiry.

RoleRetirementPeers independently checks PostgreSQL's role-lock wait and captured-
OID42704 against native optimistic publication rejection after a peer schema or
policy dependency. Dispositions covers own dependency retirement, unrelated peer
changes, rollback, promotion, savepoints and prepared completion. Native schema
GRANT remains excluded; the PostgreSQL fixture alone grants/revokes public USAGE.
Pure retirement guards preserve exact catalog/role/frontier/prepared authority.

HoldTrueEnd observes a middle sequence inquiry before holdable-cursor materialization
at the actual message end, stable later FETCH values, and no draw/cursor after a
failed message. ForeignKeyComposition creates and consumes private parent/child
descriptors in one direct message; a late FK failure rolls back both descriptors.
These preserve current upstream capabilities, not adjacent feature implementation.

Run these additional bounded groups in native mode and again with `REFERENCE=1`:

```sh
make check RUN='^TestReferenceDriverBatchRoleRetirement(Peers|Dispositions)$'
make check RUN='^TestReferenceDriverBatch(HoldTrueEnd|ForeignKeyComposition)$'
make check RACE=1 RUN='^TestDriverBatchRoleRetirement(PublicationGuard|CandidateRestoration|Membership)$'
```

Wrap regex alternatives once; an internal `$|` is a Make automatic variable and
can silently erase an anchor/alternation, selecting no tests. No-match package
output is not behavioral evidence.


Package admission, message ownership, private catalog/roles/index sources and
cursor cleanup supplement those public proofs. Mechanical batch fixtures use
AcquireCommand, matching the public statement budget and SQL-activity/idle
completion boundaries, not plain native-control Acquire:

```sh
make check RUN='^TestDriverBatch'
make check RACE=1 RUN='^TestDriverBatch'
```

Each check invocation has an external 900-second deadline; targets own no deadline.
`make quickcheck` remains whole-project Docker-free verification, not a substitute
for selected real PostgreSQL evidence. Finalcheck's external deadline is 1800
seconds and likewise does not replace either selected mode.

### Pinned timeout FATAL instrumentation

Pinned timeout FATAL instrumentation keeps the real server PID, SQLSTATE and
exact primary message/analysis position separate from pq's public bad-connection
sentinel. Its Docker log client belongs to the existing pinned fixture lifecycle,
not a separate container or startup owner. Before arming an owner, capture its
official PostgreSQL `%c` incarnation from `pg_stat_activity` backend-start time
and PID. Baseline and later reads use `Since` backend-start minus one second;
exact PID/incarnation matching and baseline rejection preserve stale-evidence
protection without imposing any post-arming tail limit.
Read raw non-following responses synchronously with a 32 MiB evidence cap checked
before frame payload allocation. Cancellation closes the owned response and joins
its close callback; malformed/truncated frames, size/read/close errors fail rather
than accepting partial oracle evidence. This avoids rereading the full accumulated
suite history through per-frame pipes. All test deadlines remain unchanged.

### Settings and session timeouts

Settings timeout witnesses live in `TestReferenceSettingsPublicTimeoutStages`
and `TestReferenceSettingsPublicTimeoutRollbackCleanup`. Once a physical owner
is armed with the genuine 1ms statement timer, even `SHOW` or `RESET` can
legitimately time out under scheduling pressure. The implicit-timeout scenario
therefore disposes of its armed owner without further SQL; transaction rollback
restores timeout 0 before checking SAME-owner and held-statement recovery.
Both prepared-execute routes prepare only the current candidate at timeout0,
then arm1ms before executing that genuine Conn-held handle. The rollback route
begins its transaction after preparation and closes the handle after rollback,
including fatal-path cleanup. After an untimed atomic23502 fallback, complete
candidate assertions and statement closure, then physically discard the still-armed
implicit owner without further owner SQL. The next reached candidate acquires a
fresh timeout0 owner. Rollback restores entry timeout0 on the same owner before
statement closure and recovery assertions.
All four bounded fallback sizes and the shared30s fixture deadline remain;
unused million-row fallback handles are not eagerly parsed/bound.
`TestReferenceSettingsSQL` checks tiny-timeout encoding on the arming command
and then disposes of that isolated owner. Do not increase the timer, retry an
expired-owner reset, or suppress its diagnostic to make fixture cleanup pass.
Generate and prepare only the reached bounded workload, before arming its owner.
Each candidate owns its held statement and physical connection through assertions
and closure; an untimed fallback disposes the armed implicit owner before the next
candidate opens a fresh timeout0 owner. Retain all fallback sizes and stage/backend
assertions without eager million-row preparation or a larger fixture deadline.

The `TestReferenceTimeout` family adds transaction, physical idle, cleanup and
pool contracts without duplicating the statement/lock corpus. Its ten names
are selected together below; `settingsTimeoutWorkload` is shared with the
existing CPU scenarios, and lock workers use `fixture.LockOperation` plus the
PostgreSQL external queue barrier. No new delay SQL is needed.

```sh
timeout 900s make check REFERENCE=1 RUN='^TestReferenceTimeout(Settings|TransactionBoundaries|TransactionAssignments|TransactionAbortRestore|IdleBoundaries|Precedence|ChainPrepared|FatalCleanup|PoolNoReplay|FeatureSmoke)$'
timeout 900s make check RUN='^TestReferenceTimeout(Settings|TransactionBoundaries|TransactionAssignments|TransactionAbortRestore|IdleBoundaries|Precedence|ChainPrepared|FatalCleanup|PoolNoReplay|FeatureSmoke)$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceTimeoutFeatureSmoke$'
timeout 900s make check RUN='^TestReferenceTimeoutFeatureSmoke$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceSettingsPublicTimeout'
timeout 900s make check RUN='^TestReferenceSettingsPublicTimeout'
```

The pinned full ten-definition discovery run passed before production changes.
The scenario-local PostgreSQL-only filter and its comments were then removed;
the root observed native regression from the three missing settings. Completed
checks now run the same shared bodies in both modes, with no discovery filter
remaining. That oracle result does not claim native or final verification.
The pinned run established pq's direct/Prepare/idle FATAL API
sentinel as ErrBadConn and Conn-held Execute as ErrConnDone, not portable
SQL diagnostic fields. The pinned fixture's `log_line_prefix` includes real
backend PID, session incarnation and SQLSTATE (`timeout-oracle|%p|%c|%e|`).
PostgreSQL [documents `%c`](https://www.postgresql.org/docs/18/runtime-config-logging.html#RUNTIME-CONFIG-LOGGING-WHAT)
as hexadecimal backend-start seconds plus hexadecimal PID; PG-only
`pg_stat_activity` instrumentation captures that identity before arming.
Docker `Since` reads start one second before that backend's start, preserving
boundary records without repeatedly fetching/reparsing the suite's full log
history. No `Tail` limit truncates expected errors. Exact PID/incarnation/code
matching and the same-suffix pre-arm baseline reject stale evidence; the exact
FATAL severity/English primary/Position remain independently asserted.
Idle-expiration boundaries and the feature smoke use bounded full-idle-window
retries rather than assuming a single fixed delay proves expiration. Owner probes
restart idle timers, so retry windows back off up to one second under a five-second
deadline; continuous short-interval owner polling would prevent expiration.
Only a live cursor or the exact aborted-transaction diagnostic permits another
window. The final FATAL diagnostic, unusable physical handle and rollback checks
remain mandatory.

Strict fatal-owner probes remain strict: physical-owner admission observes due
budgets before a delayed callback can be outrun, with no published Rows or new
statement timer on an expired owner. CHAIN, reenable and completed idle-reset
boundaries do not retry live rows to conceal late admission.

`TestReferenceTimeoutPrecedence/residual-expiry-before-command` observes actual
rollback through a peer read blocked behind the owner's exclusive sentinel lock,
then requires exact25P04/no Rows and a dead owner on the later command. It replaces
the old wall-time active-race assumption that an older transaction must always
beat a shorter new statement timeout. PostgreSQL processes an already-pending
statement/lock ERROR before transaction FATAL; the private pending-order mechanics
retain that separate contract. This witness tests owner-first residual expiry,
not simultaneous-pending active-work deadline arbitration.

```sh
make check RUN='^TestReferenceTimeout(IdleBoundaries|ChainPrepared|TransactionAssignments|Precedence)$' REFERENCE=1
make check RUN='^TestReferenceTimeout(IdleBoundaries|ChainPrepared|TransactionAssignments|Precedence)$'
```

A suite-owned Docker client reads each raw response synchronously, closes it
on every exit, and validates complete eight-byte frame headers and bodies under
a 32 MiB cumulative wire cap. Truncation, excess, daemon error streams and
read/close errors fail visibly; no unjoined pipe demultiplexer owns the response.
`TestTimeoutOracleLogFrames` exercises framing, cap and close/error ownership
without SQL. This fixture-cost remedy addresses cumulative reference race-suite
work, not an established single-test hang. The remedy changes no timed witnesses,
fixture budgets or check deadlines. Recorder comparisons likewise scan
backward to the latest successful matching query under the existing lock, with
the same predicate and owned-buffer assertions. No sentinel is converted into
a manufactured diagnostic object.

```sh
timeout 900s make check RUN='^TestTimeoutOracleLogFrames$'
timeout 900s make check REFERENCE=1 RUN='^TestTimeoutOracleLogFrames$'
```

Native still asserts its full structured portable diagnostics. Capture PID/OID
and log baseline before arming or Go BeginTx, then execute Go work through that
actual Tx: querying its borrowed Conn deadlocks behind database/sql ownership.
The pinned run established real server FATAL analysis locations: blocked SELECT
direct/Prepare has `at character 15`, blocked INSERT and PG CPU semantic Prepare
have `at character 13`. Assert the full independently specified suffix, not a
stripped message. Idle, direct CPU parse and cached Execute have absent Position.
Root native verification established that the1ms CPU Prepare budget instead
cancels incomplete parsing before scope.Describe; its exact25P04 FATAL has absent
Position. This is a genuine phase distinction, not a fabricated native semantic
location or a parsing-cancellation bypass. Deterministic locked semantic Prepare
still asserts Position15 in both modes. All routes retain full severity/code/
primary/optional fields, no published result/handle/rows, termination and cleanup.
Stored physical-session fatal is not blanket-decorated.
For Go-held execution, prepare `LOCK TABLE timeout_blocked IN ACCESS SHARE MODE`
on the actual Tx: this is metadata-only and retains no relation lock. Then the
peer acquires AX and the actual held Exec queues AS. Preparing SELECT inside that
Tx would retain AS and block the peer before Execute. Conn-held SELECT and
direct/semantic-Prepare SELECT routes remain separately covered.

The retained-Rows idle witness keeps native immutable Rows through the first
exact FATAL, reads/checks/closes them, and only then issues the dead-handle probe.
That later ErrBadConn can make database/sql close sql.Conn; doing so while its
previous Rows still owns the release callback blocks close until caller deadline.
This ordering preserves the retained-snapshot assertion, not a cursor lease leak
workaround. PostgreSQL's streaming Rows are still drained before transport reuse.
Detached prepared-entry lifetime compares exact identifiers/owner/database,
time.Time.Equal instant and zone name/offset, never time.Location pointer identity.

The first pooled INSERT99 after idle expiry succeeds through safe pre-work
replacement on pq; assert affected1 and exactly7,99, then remove99 and verify
new physical identity/default0. This is distinct from accepted blocked INSERT8,
which must remain unpublished and never be replayed. Savepoint restoration of
the configured positive duration after SET0 leaves SHOW positive but does not
rearm: independently observe a live owner beyond it, then require off/on expiry.

The timeout family's ordinary delay witnesses retain250ms budgets crossing
at350ms; shorter limits125ms remain below250ms. The four CHAIN timer-incarnation
cases use6s session and1s LOCAL budgets, with monotonic timestamps bracketing
BEGIN and AND CHAIN. Their successful SHOW occurs after both obsolete timer
windows but strictly before the earliest possible chained deadline; scheduling
overruns fail with measured brackets rather than accepting premature expiry or
retrying the transaction. Final25P04 and all peer-row assertions remain exact.
The positive-positive and same-value transaction assignment cases separately
use an original6s budget and assign12s or6s at the absolute BEGIN-return+3s
target. Monotonic BEGIN and SET/SHOW brackets require assignment to finish
before the earliest original deadline. They retain the same physical owner
and require exact25P04/dead-owner proof after the latest original deadline
but before the earliest possible assignment-refreshed deadline; scheduler
overruns fail explicitly, without retrying or accepting a later expiry.
Other assignment branches and the fixture's30s context remain unchanged.

```sh
make check RUN='^TestReferenceTimeoutTransactionAssignments$' REFERENCE=1
make check RUN='^TestReferenceTimeoutTransactionAssignments$'
make check RUN='^TestReferenceTimeoutTransactionAssignments$' RACE=1
```

Run each selected command with an external900-second deadline.

The old500ms budget after300ms has200ms residual in the separate precedence
witness, beating a newly configured375ms limit. Abort recovery waits1100ms beyond suppressed1s idle
and600ms beyond restored500ms. Active-idle limits remain100ms while actual
lock work lasts250ms (2.5 limits); CPU parse witnesses retain1ms. Registry
normalization/range inputs, observer polls, asynchronous joins and all
diagnostic/state/atomicity assertions are unchanged. The separate public
executable retains its independently specified100ms/300ms budgets and650ms
idle stimuli.

`TestReferenceTimeoutChainPrepared/detached-survives-origin-idle` allows eight
500ms idle intervals with a lock-free `SELECT 1` probe after each interval.
A successful probe retries; any error must still be the exact idle-session
FATAL57P05, and failure to terminate within the bounded attempts fails.
The probe must not read the table whose ACCESS EXCLUSIVE lock remains owned by
the detached transaction. Prepared-entry identity, retained-lock conflict and
eventual committed-row assertions remain exact.

The query/prepare/ping-reset branches of `TestReferenceTimeoutIdleBoundaries`
adapt only their timing stimulus to scheduler pressure. Idle budgets start
at250ms, double incrementally through500ms and1s, and stop at2s (four attempts).
Each attempt waits half its selected budget before each of three activities.
Before issuing an activity or the final identity check, elapsed time since
the prior operation START must be below three quarters of the budget. This
conservative interval includes response/assertion work and leaves dispatch
headroom. Only a measured invalid interval permits disposal of that physical
owner and a larger-budget attempt on a fresh owner; no SQL, Prepare, Ping or
identity error is retried. A valid attempt requires all three successful
activities on the SAME owner, cumulative elapsed time exceeding its SELECTED
idle budget, then unchanged identity and eventual57P05 after budget+100ms
without intervening owner activity. Exhausted timing/context bounds fail,
never skip. The original30s scenario context remains; each candidate reserves
three half-budget waits, final expiry and3s for bounded probes before starting.

Selected proof (external900-second deadline per command):

```sh
make check RUN='^TestReferenceTimeoutIdleBoundaries$'
make check RUN='^TestReferenceTimeoutIdleBoundaries$' REFERENCE=1
make check RUN='^TestReferenceTimeoutIdleBoundaries$' RACE=1
make check RUN='^TestReferenceTimeoutIdleBoundaries$' REFERENCE=1 RACE=1
```

The transaction-assignment command witness issues SHOW after two50ms idle
intervals, then probes expiry after175ms more. Its nominal275ms probe is after
the original250ms deadline but before an incorrectly rearmed350ms deadline;
the second successful SHOW has150ms nominal headroom. This test-only window
correction preserves the nonrearming assertion without changing provider budgets.

The complete pinned run also confirmed actual Go-Tx-held ErrBadConn transport,
distinct from Conn-held ErrConnDone. Native caller context cancellation retains
its ordinary error; pq health stays separate. Native mechanics selectors are
`timeout 900s make check RUN='^TestSessionTimeout'`
and `timeout 900s make check RUN='^TestSessionFatalNoReplayMechanics$'`.
These cover callback generation/token/ref/deadline authority, provisional adoption,
assignment/restoration, configured idle suppression, active/ready boundaries,
first end reason, copied fatal diagnostics, canonical RESET ALL batch/journal/cache
publication/restoration and no-replay adapter classification.
The root runs these and both completed reference modes after edits; readiness
or a discovery pass is not production verification.

`timeout 900s make check RUN='^TestSessionTimeoutPending'`
exercises actual Session liveness, not merely relation-wait diagnostic ordering.
Canonical scopes and real queued requests cover due/delivered lock ERROR,
absolute statement expiry before context delivery, both SQL deadlines, ERROR
claim before possible grant, full/child recovery and stopped-budget restoration.
The inverse grant/non-SQL terminal/completed/old-incarnation cases preserve
genuine25P04 FATAL. Concurrent grant-versus-expiry units require one atomic
outcome: LIVE/stopped/55P03 or terminal25P04, never stopped-budget success or
recoverable ERROR on a physically ended owner. Workers/callbacks join with
bounds; no SQL text, sleeps or production scheduler hook is used.

Changed complete builtin-inventory definitions also require both modes:
`timeout 900s make check RUN='^TestReference(Settings(BuiltinRegistry|ShowAllPlaceholdersAndLifecycle)|FormatEncodingSettings|TimezoneBuiltinRegistryDescriptions)$'`
and `timeout 900s make check REFERENCE=1 RUN='^TestReference(Settings(BuiltinRegistry|ShowAllPlaceholdersAndLifecycle)|FormatEncodingSettings|TimezoneBuiltinRegistryDescriptions)$'`.
The supported native registry is29 independently named definitions, not the
previous26; the PostgreSQL projection independently checks the new three0-valued
definitions and exact release short_desc, not whole-server catalog equality.

The root additionally executes the recorded standalone public-API Go feature
program with `timeout 60s go run ./.timeout-feature-smoke`, from an ephemeral
root-owned source directory which is removed after evidence capture, not committed.
Independent assertions include ordinary55P03/57014 same-owner recovery,
transaction25P04 and idle25P03/57P05 first FATAL. Compare the entire expected
native Error (both severities, code, English message, contextual Position and
every remaining optional field), nil failed Rows/results and exact affected
counts. The old handle must reject INSERT99 without effects; peer ordered INT4
rows retain7 and exclude private8/99, and an independent blocked-table query
must be empty. Peer reads also prove actual AX release. Replacements must have
different physical identities and all five timeout defaults exactly0. Pooled
idle replacement happens before new work; accepted timed work is never replayed.
The executable must exit0 only after every assertion and print
`timeout feature smoke passed`; a selected test pass does not replace this run.

The standalone smoke expects lock ERROR55P03 Position15 and statement ERROR57014
with absent Position, then transaction FATAL25P04 Position13, idle-in-transaction
FATAL25P03 and idle-session FATAL57P05 with absent Position. Setup, lock, setting,
reset and end commands affect0; initial/private/healthy inserts affect1.
Committed7 survives every death; healthy final INSERT9 affects1 and the peer
must observe exactly ordered INT4 rows7,9/cardinality2, with the blocked table
still empty and no99/private8/blocked8 replay.
Commands are verification instructions, not claims of exercised outcomes.

### Settings smoke migration

The superseded `TestSettingsSmoke` is removed; its typed registry, numeric,
metadata, notice/reentry, parser capture, access and connection-lifecycle
obligations remain in the existing shared `TestReferenceSettings*` consumers.


### Physical-session adapter witnesses

The migrated `session_adapter*_test.go` definitions retain exact physical-owner,
metadata, cancellation, Reset/Restore, and pool-lifetime assertions.
The original `fixture.SettingsPhysical` implementation is also used by the
retained SQL prepared and DISCARD consumers. Shared batch result-set matchers
(`fixture.BatchSet`, `BatchReadSet`, `BatchSets`) and notice witnesses
(`fixture.NoticeWitnesses`, `NoticesInternalDebug`) likewise have one implementation
in the existing fixture package, preserving every original assertion.

Shared-helper changes additionally require retained notice and cursor consumers:

```sh
make check RUN='^TestReference(Notices(Commands|PreparedShow|CancellationAndSyntax)|PGCursorsSourcesAndTime)$'
make check RUN='^TestReference(Notices(Commands|PreparedShow|CancellationAndSyntax)|PGCursorsSourcesAndTime)$' REFERENCE=1
make check RUN='^TestReferenceNotices(Payload|Severity|Permission)$' REFERENCE=1
```

The last group preserves legacy PostgreSQL-only tagged definitions; native
no-match is not evidence for those definitions.

### Relocation selected proof

Run every group below as `make check RUN='SELECTOR'` and again with
`REFERENCE=1`, each under an external 900-second deadline. The unchanged
PostgreSQL-only build tags retain their existing mode availability.

```sh
make check RUN='^(TestReferenceSettingsParserMode|TestReferenceSettingsBooleanBindingDiagnostics|TestReferenceSettingsUnicodeOffAndCapture|TestReferenceSettingsSetConfigNotices|TestReferenceSettingsExactDecimalSubnormal|TestReferenceSettingsLocalDiagnostics|TestReferenceSettingsLocalAliases|TestReferenceSettingsLocalConsumers)$'
make check RUN='^(TestReferenceSettingsParserMode|TestReferenceSettingsBooleanBindingDiagnostics|TestReferenceSettingsUnicodeOffAndCapture|TestReferenceSettingsSetConfigNotices|TestReferenceSettingsExactDecimalSubnormal|TestReferenceSettingsLocalDiagnostics|TestReferenceSettingsLocalAliases|TestReferenceSettingsLocalConsumers)$' REFERENCE=1
```

```sh
make check RUN='^(TestReferenceSettingsLocalNoticeLifecycle|TestReferenceSettingsLocalResetAllBatch|TestReferenceSettingsLocalSQLControls|TestReferenceSettingsLocalPrivilegeOracle|TestReferenceSettingsLocalTransactions|TestReferenceSettingsLocalImplicit|TestReferenceSettingsLocalFunctionsAndDefaults|TestReferenceSettingsMigratedLocalSmoke)$'
make check RUN='^(TestReferenceSettingsLocalNoticeLifecycle|TestReferenceSettingsLocalResetAllBatch|TestReferenceSettingsLocalSQLControls|TestReferenceSettingsLocalPrivilegeOracle|TestReferenceSettingsLocalTransactions|TestReferenceSettingsLocalImplicit|TestReferenceSettingsLocalFunctionsAndDefaults|TestReferenceSettingsMigratedLocalSmoke)$' REFERENCE=1
```

```sh
make check RUN='^(TestReferenceSettingsPrivilegeAndTimeoutOracle|TestReferenceSettingsSetConfigReentry|TestReferenceSettingsClippingNoticeSchedule|TestReferenceSettingsPublicTypedSQL|TestReferenceSettingsErrorsAndAtomicity|TestReferenceSettingsRegistryPublication|TestReferenceSettingsShowAllPlaceholdersAndLifecycle|TestReferenceSettingsPreparedBindingAndStages)$'
make check RUN='^(TestReferenceSettingsPrivilegeAndTimeoutOracle|TestReferenceSettingsSetConfigReentry|TestReferenceSettingsClippingNoticeSchedule|TestReferenceSettingsPublicTypedSQL|TestReferenceSettingsErrorsAndAtomicity|TestReferenceSettingsRegistryPublication|TestReferenceSettingsShowAllPlaceholdersAndLifecycle|TestReferenceSettingsPreparedBindingAndStages)$' REFERENCE=1
```

```sh
make check RUN='^(TestReferenceSettingsEveryUnitBase|TestReferenceSettingsControlsDefaultOnParser|TestReferenceSettingsNumericPrefixEdges|TestReferenceSettingsRegistryValidationAndLinearization|TestReferenceSettingsShowAllCurrentOverrides|TestReferenceSettingsAuthenticatedAccessClasses|TestReferenceSettingsUtilitySnapshotOwnership|TestReferenceSettingsSQL)$'
make check RUN='^(TestReferenceSettingsEveryUnitBase|TestReferenceSettingsControlsDefaultOnParser|TestReferenceSettingsNumericPrefixEdges|TestReferenceSettingsRegistryValidationAndLinearization|TestReferenceSettingsShowAllCurrentOverrides|TestReferenceSettingsAuthenticatedAccessClasses|TestReferenceSettingsUtilitySnapshotOwnership|TestReferenceSettingsSQL)$' REFERENCE=1
```

```sh
make check RUN='^(TestReferenceSettingsBuiltinRegistry|TestReferenceSettingsPublicTimeoutStages|TestReferenceSettingsPublicTimeoutRollbackCleanup|TestReferenceSettingsBooleanByteInputAndHealthySession)$'
make check RUN='^(TestReferenceSettingsBuiltinRegistry|TestReferenceSettingsPublicTimeoutStages|TestReferenceSettingsPublicTimeoutRollbackCleanup|TestReferenceSettingsBooleanByteInputAndHealthySession)$' REFERENCE=1
```

```sh
make check RUN='^(TestReferenceTimeoutIdleBoundaries|TestReferenceTimeoutPrecedence|TestReferenceTimeoutChainPrepared|TestReferenceTimeoutFatalCleanup|TestReferenceTimeoutOracleCaptureDataAndErrors|TestReferenceTimeoutOracleCaptureCancellation|TestReferenceTimeoutPoolNoReplay|TestReferenceTimeoutFeatureSmoke)$'
make check RUN='^(TestReferenceTimeoutIdleBoundaries|TestReferenceTimeoutPrecedence|TestReferenceTimeoutChainPrepared|TestReferenceTimeoutFatalCleanup|TestReferenceTimeoutOracleCaptureDataAndErrors|TestReferenceTimeoutOracleCaptureCancellation|TestReferenceTimeoutPoolNoReplay|TestReferenceTimeoutFeatureSmoke)$' REFERENCE=1
```

```sh
make check RUN='^(TestReferenceTimeoutSettings|TestReferenceTimeoutTransactionBoundaries|TestReferenceTimeoutTransactionAssignments|TestReferenceTimeoutTransactionAbortRestore)$'
make check RUN='^(TestReferenceTimeoutSettings|TestReferenceTimeoutTransactionBoundaries|TestReferenceTimeoutTransactionAssignments|TestReferenceTimeoutTransactionAbortRestore)$' REFERENCE=1
```

```sh
make check RUN='^(TestTimeoutOracleLogFrames)$'
make check RUN='^(TestTimeoutOracleLogFrames)$' REFERENCE=1
```

```sh
make check RUN='^(TestReferenceSessionPhysicalControlsAndPoolClosure|TestReferenceSessionDecimalIntegerBoundary|TestReferenceSessionResetCancellationBetweenChecks|TestReferenceSessionResetExpiredDeadline|TestReferenceSessionAdapterCancellationCleanupAndExhaustion|TestReferenceSessionShowCommandCount)$'
make check RUN='^(TestReferenceSessionPhysicalControlsAndPoolClosure|TestReferenceSessionDecimalIntegerBoundary|TestReferenceSessionResetCancellationBetweenChecks|TestReferenceSessionResetExpiredDeadline|TestReferenceSessionAdapterCancellationCleanupAndExhaustion|TestReferenceSessionShowCommandCount)$' REFERENCE=1
```

```sh
make check RUN='^(TestReferenceDriverBatchRetainedBlockControls|TestReferenceDriverBatchParsePrepared|TestReferenceDriverBatchErrorTimingRollback|TestReferenceDriverBatchHoldTrueEnd|TestReferenceDriverBatchForeignKeyComposition|TestReferenceDriverBatchTransactionBoundaries|TestReferenceDriverBatchClockSettingsTimeouts|TestReferenceDriverBatchCancellationLifecycle)$'
make check RUN='^(TestReferenceDriverBatchRetainedBlockControls|TestReferenceDriverBatchParsePrepared|TestReferenceDriverBatchErrorTimingRollback|TestReferenceDriverBatchHoldTrueEnd|TestReferenceDriverBatchForeignKeyComposition|TestReferenceDriverBatchTransactionBoundaries|TestReferenceDriverBatchClockSettingsTimeouts|TestReferenceDriverBatchCancellationLifecycle)$' REFERENCE=1
```

```sh
make check RUN='^(TestReferenceDriverBatchPolicyProviders|TestReferenceDriverBatchPolicyPreparedParameters|TestReferenceDriverBatchPolicyExplicitBoundary|TestReferenceDriverBatchPolicyCreation|TestReferenceDriverBatchPolicyAlterDrop|TestReferenceDriverBatchPreparedBoundaries|TestReferenceDriverBatchInitialPrepare|TestReferenceDriverBatchRefreshPromotion)$'
make check RUN='^(TestReferenceDriverBatchPolicyProviders|TestReferenceDriverBatchPolicyPreparedParameters|TestReferenceDriverBatchPolicyExplicitBoundary|TestReferenceDriverBatchPolicyCreation|TestReferenceDriverBatchPolicyAlterDrop|TestReferenceDriverBatchPreparedBoundaries|TestReferenceDriverBatchInitialPrepare|TestReferenceDriverBatchRefreshPromotion)$' REFERENCE=1
```

```sh
make check RUN='^(TestReferenceDriverBatchRefreshAuthorization|TestReferenceDriverBatchRefreshComposition|TestReferenceDriverBatchRefreshAtomicity|TestReferenceDriverBatchRefreshCancellation|TestReferenceDriverBatchRefreshControls|TestReferenceDriverBatchEffectsCounts|TestReferenceDriverBatchResultSets|TestReferenceDriverBatchFeatureSmoke)$'
make check RUN='^(TestReferenceDriverBatchRefreshAuthorization|TestReferenceDriverBatchRefreshComposition|TestReferenceDriverBatchRefreshAtomicity|TestReferenceDriverBatchRefreshCancellation|TestReferenceDriverBatchRefreshControls|TestReferenceDriverBatchEffectsCounts|TestReferenceDriverBatchResultSets|TestReferenceDriverBatchFeatureSmoke)$' REFERENCE=1
```

```sh
make check RUN='^(TestReferenceDriverBatchResultSetMetadata|TestReferenceDriverBatchRoleRetirementDispositions|TestReferenceDriverBatchRoleRetirementPeers|TestReferenceDriverBatchDiagnosticsAuthorization|TestReferenceDriverBatchUtilityAtomicity)$'
make check RUN='^(TestReferenceDriverBatchResultSetMetadata|TestReferenceDriverBatchRoleRetirementDispositions|TestReferenceDriverBatchRoleRetirementPeers|TestReferenceDriverBatchDiagnosticsAuthorization|TestReferenceDriverBatchUtilityAtomicity)$' REFERENCE=1
```

```sh
make check RUN='^(TestReferenceMaterializedRefreshConcurrentRetainedCancellation|TestReferenceMaterializedRefreshConcurrentRetainedSourceMaterialized|TestReferenceMaterializedRefreshConcurrentRetained)$'
make check RUN='^(TestReferenceMaterializedRefreshConcurrentRetainedCancellation|TestReferenceMaterializedRefreshConcurrentRetainedSourceMaterialized|TestReferenceMaterializedRefreshConcurrentRetained)$' REFERENCE=1
```

