# reference/locking_transactions

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### pgrowlocks: independently specified oracle-first contract

The untagged `pgrowlocks_*_test.go` scenarios use this package's shared public
fixture, physical connections, real transaction identities and joined workers.
They specify PostgreSQL 18.6 expectations independently; adding a scenario is
not evidence that either backend has passed it.

| Group | Public witness |
|---|---|
| R1 InstalledAndShape | Installation/member identity, STRICT NULL, all six empty/populated descriptors, named/coerced arguments, inferred held parameters, numeric TEXT names |
| R2 ModesAndOwners | Four SELECT lock modes, UPDATE/key UPDATE/DELETE action modes, actual XIDs/PIDs, self locks, invisible INSERT, stable release/reacquire identity |
| R3 ConflictingHandoff | Actual queued conflicting SELECT/UPDATE absent from owners, COMMIT/ROLLBACK handoff, joined worker, restored data and tuple identity |
| R4 RawMultiAndPID | Compatible owners, complete aligned raw bag, independent multixact crosscheck, ended member PID0, new transaction does not revive PID, prepared owner PID0 |
| R5 VisibilityAndVersions | RC/RR version choice, committed mixed updater retained under RR, inherited singleton/Multi successor headers, own writes/deletes, top-level rollback |
| R6 NamesPermissionsAndDiagnostics | Quoted/schema/current-database/TEMP/numeric names, exact relation only, kinds/AM, table versus column SELECT, inherited stat role/NOINHERIT, namespace/EXECUTE, unfiltered RLS storage |
| R7 ExpressionRoutes | FROM/record/target/lateral/alias/ordinality, held reexecution and owned Rows, volatile child evaluated once, exact analysis errors and UTF8 character positions |
| R8 LifecycleAndControls | Transaction/savepoint installation publication, failed install atomicity, schema/path and stored view dependency, drop/reinstall identity, native Dump/Reset/Restore |
| R9 RelationWaitAndIdentity | Real relation-AS wait, NULL no wait, cancellation/request removal, exact relation only, function-added AS release versus earlier SELECT preservation, replacement fencing |
| R10 FeatureSmoke | Installation, real conflicting UPDATE handoff, direct/prepared six-column shape, strict NULL, raw multi/PID0, rollback restoration and distinct committed successor |
| R13 DynamicSnapshot | Real outer storage row, correlated empty-table AX/AS gate, separately named target UPDATE/COMMIT, original snapshot/raw members, RC/RR reexecution through direct/held/stored/nested consumers |

Assertions compare result rows and aligned `(xid,mode,pid)` member triples as
bags, preserving multiplicity. They never sort arrays independently, pin
allocator numbers, promise heap coordinates or issue `SELECT table.ctid`.
Child-XID/savepoint-acquired row-lock parity is not claimed. Actual prepared
transactions use existing public fixture setup. Native controls are explicit
native-only phases, not fabricated PostgreSQL APIs. Custom nonheap table-AM
setup is PostgreSQL-only: represented native ordinary storage is heap and has
no custom-AM constructor. Queued replacement is deliberately backend-specific:
PostgreSQL exercises actual transactional DROP/recreate and checks its new
identity; native independently asserts the existing explicit-DROP `0A000`
boundary and unchanged real table, then Reset/Restore exercises queued
catalog-replacement fencing without retargeting.

Each pgrowlocks backend scenario explicitly drops the extension before setup
and after all borrowed owner/observer handles and workers are cleaned up, using
a separate administrative connection. Resetting `public` alone can remove the
function while leaving its extension registration; nonpublic installations must
not survive into the next scenario. The denied-installer ACL setup uses a
separate schema, removed before native controls so provider/view replay does
not require unsupported explicit namespace ACL serialization. Failure cleanup
drops both schemas before removing the installer role.

The installed-function OID assertion uses the shared OID fixture: PostgreSQL
returns decimal bytes while native returns an integer, but both must identify
the exact RECORD OID2249 with OID metadata. The own-header schedule keeps its
exact PostgreSQL18.6 observation: FOR UPDATE followed by the same owner's
non-key UPDATE reports predecessor action `Update`, while the own successor
inherits `For Key Share`. Ordinary non-key-update expectations remain unchanged.

Record expansion retains scalar ownership by field attribute path and result
usage, not just the shared written `.*` position. Direct/prepared populated and
empty routes check every descriptor and actual owner field; a supplemental
engine unit distinguishes same-typed sibling fields. Ordinary SELECT under
the false RLS policy returns no rows on PostgreSQL and independently reports
the existing native policy-expression `0A000` boundary. In both modes,
pgrowlocks still reports the actual locked storage row after its own ACL check;
this does not implement ordinary RLS enforcement.

Use selected proof in both modes, with an external 900-second deadline per
command, after all discovery corrections and removal of temporary discovery
selection. These commands are requirements, not a passing-evidence claim:

```sh
make check RUN='^TestReferencePgrowlocks(InstalledAndShape|NamesPermissionsAndDiagnostics|ExpressionRoutes)$' REFERENCE=1
make check RUN='^TestReferencePgrowlocks(InstalledAndShape|NamesPermissionsAndDiagnostics|ExpressionRoutes)$'
make check RUN='^TestReferencePgrowlocks(ModesAndOwners|ConflictingHandoff|RawMultiAndPID|VisibilityAndVersions)$' REFERENCE=1
make check RUN='^TestReferencePgrowlocks(ModesAndOwners|ConflictingHandoff|RawMultiAndPID|VisibilityAndVersions)$'
make check RUN='^TestReferencePgrowlocks(LifecycleAndControls|RelationWaitAndIdentity|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferencePgrowlocks(LifecycleAndControls|RelationWaitAndIdentity|FeatureSmoke)$'
make check RUN='^TestReferencePgrowlocks(ConflictingHandoff|RawMultiAndPID|RelationWaitAndIdentity)$' RACE=1
```

Standalone feature smoke uses the permanent shared-body scenario, not Ping:

```sh
make check RUN='^TestReferencePgrowlocksFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferencePgrowlocksFeatureSmoke$'
```

Expected smoke observation is quiet exit0 only after the actual singleton A,
queued-but-not-owner B, granted B action, empty-after-rollback, stable original
TID, full-shape NULL, raw compatible Multi/ended PID0 and distinct committed
successor assertions pass. A mismatch exits nonzero. No smoke has been observed
merely by writing these definitions.

#### Dynamic-target snapshot review remedy

`TestReferencePgrowlocksDynamicSnapshot`, in
`pgrowlocks_dynamic_snapshot_test.go`, supplies the public dynamic-target witness
in addition to the internal snapshot-mechanism unit. Four consumers (direct,
held prepared, stored view, and held prepared over nested stored views/subquery)
each run at READ COMMITTED and REPEATABLE READ. The held handles are prepared
before BEGIN and reused after the concurrent commit.

A real `pgrow_names` row supplies both target and gate names. Its MATERIALIZED
producer evaluates a correlated `pgrowlocks` count on the empty `pgrow_gate`
table; another physical session holds that gate's AccessExclusiveLock. The
observer requires the reader's actual pending AccessShareLock on that exact
gate and its actual blocking PID before a different session updates `pgrow_t`
and commits. The target argument depends on the completed gate count, so the
target observation cannot precede the update. There are no sleeps, private
snapshot constructors, advisory APIs, or assumed allocator values.

Independently expected outcomes: both blocked isolation levels retain the old
TID and original raw MultiXact locker, with the surviving owner's
`For Key Share` member and committed updater's `No Key Update` member/PID0.
A fresh RC observer sees the distinct successor with its inherited singleton
key-share header. Reexecuting the same consumer advances RC to that successor,
while RR retains the predecessor/raw bag; ordinary value reads then prove RC1
versus RR0. Rolling back the surviving owner removes all reported locks.

The completed definition executes the same scenario for both available backends;
there is no discovery filter. PostgreSQL-first discovery established these
expectations before native verification exposed the stored/nested RC snapshot
regression. Keep that oracle-first order when changing the behavior.

```sh
make check RUN='^TestReferencePgrowlocksDynamicSnapshot$' REFERENCE=1
make check RUN='^TestReferencePgrowlocksDynamicSnapshot$'
make check RUN='^TestReferencePgrowlocksDynamicSnapshot$' RACE=1
```

Use an external 900-second deadline per selected command. These commands exercise
the completed definitions; observed results belong in the numbered part reports.
Also select the structural-construction regression in both modes when changing
the XID-array boundary:

```sh
make check RUN='^TestReferenceComparisonStructuralConstruction$'
make check RUN='^TestReferenceComparisonStructuralConstruction$' REFERENCE=1
```

### Transaction settings and read-only temporary MERGE

`TestReferenceTransactionSettingsDefaultsLifecycle` fixes independent expectations
for all three default/current dimensions, SESSION/LOCAL masking, duplicate marks,
repeated rollback-to, release, commit/rollback, RESET ALL and NULL function resets.
SHOW has one canonical TEXT column and count0; setting functions return TEXT/count1.
The held-prepared routes prepare consumers before BEGIN and reuse those handles
across state changes. Peer physical sessions retain their own baseline.

`TestReferenceTransactionSettingsResetRules` consolidates the original
`TestReferenceTransactionIsolationResetRules` assertions and extends them to all
three current names, direct/held-prepared RESET, SET/SET LOCAL DEFAULT and both
NULL-function local flags. Reset fails at execution with exact `0A000`, the block
then reports `25P02`, and rollback restores health. RESET ALL after a query leaves
current modes unchanged, resets defaults, and obeys outer commit/rollback.

`TestReferenceTransactionSettingsTemporaryMergeReadOnly` proves a held consumer
does not capture read-write mode: own-TEMP MERGE changes1→2 in READ ONLY, commits2
and rolls back to1. Native RowsAffected is1. The pinned PostgreSQL lib/pq adapter
does not decode MERGE command tags and reports0 (the shared `fixture.MergeRun`
contract); exact INT4 rows independently prove effects. Permanent MERGE still
reports exact25006, aborts the block and leaves the peer's row1 unchanged.
`TestReferenceTransactionSettingsSmoke` composes defaults, savepoint restoration,
fresh current modes, late25001→25P02 recovery and permanent INSERT25006.

Run both selected modes after affected changes, each with an external900-second
deadline. The selector includes the changed legacy reset entry point:

```sh
make check RUN='^TestReferenceTransaction(Settings(DefaultsLifecycle|ResetRules|TemporaryMergeReadOnly|Smoke)|IsolationResetRules)$' REFERENCE=1
make check RUN='^TestReferenceTransaction(Settings(DefaultsLifecycle|ResetRules|TemporaryMergeReadOnly|Smoke)|IsolationResetRules)$'
make check RUN='^TestReferenceMergeUpdatePreflightOrder$' REFERENCE=1
make check RUN='^TestReferenceMergeUpdatePreflightOrder$'
make check RUN='^TestReferenceSerializable(Deferrable|DeferrableTimeout|ReviewGoDefaultSafeWait)$' REFERENCE=1
make check RUN='^TestReferenceSerializable(Deferrable|DeferrableTimeout|ReviewGoDefaultSafeWait)$'
```

The SSI witnesses exercise actual safe-snapshot waits/retries and statement
timeout, including Prepare; SHOW DEFERRABLE alone is not proof of this behavior.
Whole-project quickcheck follows selected proof; it does not replace either mode.

For standalone runtime smoke, use a temporary public Go program outside the
repository, importing only the standard library and `stuntgres`. Pin two physical
connections, set RR/on/on defaults, begin and inspect all current modes, mask the
defaults locally at a savepoint and roll back to restore them. Execute an actual
preheld own-TEMP MERGE in a fresh read-only transaction; require count1 and a
committed value2. Restore RC/off/off defaults, acquire a query snapshot, then
require late isolation25001 followed by25P02 and rollback. Require a permanent
INSERT25006 followed by25P02, roll back, perform a healthy write and require peer
rows[1,3]. A failed assertion must exit nonzero. For example, after materializing
the program at the path below, run from this module root and remove it afterwards:

```sh
timeout 900s go run /tmp/pg18-admin-settings-tx-smoke/main.go
```

### SHOW transaction isolation alias

`TestReferenceTransactionIsolationShowAlias` independently asserts one
`transaction_isolation` TEXT column, one canonical row and SHOW Exec count0 for
the alias, its mixed-case/comment spelling and ordinary setting-name SHOW.
Direct and preheld consumers observe RC baseline, inherited RR, unchanged RR
after a default change to serializable, a legal current change to RU after SHOW,
fresh serializable after commit, and fresh RC after reset. A setting-function
control confirms RU; the peer remains RC. No other SHOW alias is implied.

Run both modes with an external900-second deadline:

```sh
make check RUN='^TestReferenceTransactionIsolationShowAlias$' REFERENCE=1
make check RUN='^TestReferenceTransactionIsolationShowAlias$'
```

For standalone feature smoke, use a temporary public Go consumer outside the
repository: hold the alias statement on a pinned connection before changing
defaults, check canonical TEXT metadata and exactly one row through both direct
and held queries at RC→RR→RU→serializable→RC transitions, and require Exec count0.
Change the default inside the RR block without changing its current RR, then
require SET TRANSACTION RU to succeed after SHOW. Commit, verify a fresh
serializable block, roll back, reset defaults and verify fresh RC. A second
physical session must stay RC. Materialize the consumer outside the repository,
run from the module root, and remove the temporary program afterwards:

```sh
timeout 900s go run /tmp/pg18-admin-settings-tx-alias-smoke/main.go
```

### Session-message PREPARE restrictions

`TestReferencePreparedTransactionSessionBoundaries` now executes
LISTEN/NOTIFY/UNLISTEN on both backends before asserting actual PREPARE
restrictions, rather than expecting native unsupported commands. Existing
subscriptions alone permit PREPARE; retained actions/events, duplicate LISTEN,
net-zero actions, and sticky-history UNLISTEN after UNLISTEN ALL/DISCARD ALL
forbid it. Pristine physical UNLISTEN ALL is action-free. Savepoint rollback drops
child restrictions; RELEASE retains them. Failed PREPARE creates no GID, discards
outgoing/staged subscriptions and releases committed pending peer messages at the
actual outer idle boundary. TEMP/WITH HOLD and unsupported snapshot export keep
their independent established precedence and behavior.

`TestReferenceSessionMessagesPreparedOriginReuse` covers empty and row-bearing
detached entries while the origin has a later transaction with staged NOTIFY,
LISTEN and UNLISTEN. Completion must neither publish uncommitted events nor
change the finisher's subscriptions. Both later-origin commit and rollback are
independently witnessed with positive ordered sentinels and committed row reads.

Run each command with an external900-second deadline:

```sh
make check RUN='^TestReferencePreparedTransactionSessionBoundaries$'
make check RUN='^TestReferencePreparedTransactionSessionBoundaries$' REFERENCE=1
make check RUN='^TestReferenceSessionMessagesPreparedOriginReuse$'
make check RUN='^TestReferenceSessionMessagesPreparedOriginReuse$' REFERENCE=1
make check RACE=1 RUN='^TestNotificationPreparedOriginProgress$'
```

See [local notification proof and executable smoke](../sessions/README.md#session-messages)
for the exported native receiver and physical-session lifetime contract.

### Transaction identity oracle-first contract

The `TestReferenceTransactionIdentity` definitions specify assignment,
stable top-level/savepoint identity, live and retained status, wrapping ages,
typed direct/prepared metadata and diagnostics, and actual immutable MultiXact
bags. Generated identities are relational, not constants. Counter discovery
uses `mxid_age('1'::xid)` and accounts for every new ID on serialized tiny rows;
no `xmax`, XID ordering, or live-holder approximation is used.
The six-mode schedule includes same-owner keysh/forupd/upd expansion. Separate
CHECK rejection, UPSERT WHERE false, post-action RETURNING failure, EPQ and
queued/canceled/NOWAIT/SKIP witnesses distinguish candidates, installed locks
and actual modifications. Retained committed-action bags are checked publicly;
committed-updater filtering on an old physical tuple belongs to real-seam units,
not a new SELECT that would follow the successor.
`TestReferenceTransactionIdentityMultixactSuccessor` additionally proves that a
CHECK-rejected candidate stays unassigned after rollback-to, two surviving
KEY SHARE owners supply original raw K, and a non-key updater creates only U.
Successors inherit K without allocation; repeated actions reuse the owner's U,
while a fresh transaction on the same connection locks the successor without
capturing its old updater. A surviving owner's later upgrade still expands raw K.
The empty-COPY assignment phase uses native `stuntgres.CopyFrom` on the unwrapped
pool and PostgreSQL's COPY protocol, with the same explicit-owner unassigned
expectations. Backend PID/activity observations are PostgreSQL-only; native
queue/EPQ observations use the existing parked-request barrier. Successful query
contexts remain live until returned rows are drained and closed. Assigned-owner
cancellation uses a genuinely admitted native relation wait and PostgreSQL's
server cancellation phase, then checks retained aborted status in both modes.

These shared definitions retain independently specified values/relations, exact
metadata and portable diagnostics; real PostgreSQL and native use the same body.
Run each selected command with an external 900-second deadline, real mode first
and the same completed definition in quick mode second:

```sh
make check RUN='^TestReferenceTransactionIdentity(Assignment|Ages|Metadata|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferenceTransactionIdentity(Assignment|Ages|Metadata|Diagnostics)$'
make check RUN='^TestReferenceTransactionIdentity(Status|Lifecycle)$' REFERENCE=1
make check RUN='^TestReferenceTransactionIdentity(Status|Lifecycle)$'
make check RUN='^TestReferenceTransactionIdentity(Multixact|MultixactLifecycle|MultixactSuccessor)$' REFERENCE=1
make check RUN='^TestReferenceTransactionIdentity(Multixact|MultixactLifecycle|MultixactSuccessor)$'
make check RUN='^TestReferenceTransactionIdentityFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceTransactionIdentityFeatureSmoke$'
```

The feature smoke independently checks all six functions: initially unassigned
READ ONLY, stable allocation, in-progress/committed/aborted transitions, zero
current age and special age, actual keysh/sh membership and retention, and empty
strict-NULL SRF metadata. The sessions package owns default tracking-OFF status.
The successor definition is an additional focused feature regression, not a
replacement for the all-six-function smoke above.
Selected lifecycle race proof adds `RACE=1`; whole-project Docker-free proof is
`make quickcheck`, not a replacement for selected dual-mode oracle proof.


### Queued isolation-conflict execution timeout

`TestReferenceTransactionIsolationQueuedConflictTimeout` retains a REPEATABLE READ
snapshot of10 while another transaction updates to20 and deletes the same row.
Direct and actually prepared UPDATE/DELETE attempts keep that holder live until
the existing2ms statement execution budget expires. Prepared routes complete
Prepare before enabling that budget: analysis time is not the row-wait witness.
The timer duration is unchanged; no retry or post-timeout queue polling is used.
The mutation must return no result and exact57014 statement-timeout diagnostics,
including the real PostgreSQL physical updating/deleting tuple Context. The
transaction then reports25P02; both rollbacks preserve the original value10.

Run each command under an external900-second deadline, also with `RACE=1` for
selected race proof:

```sh
make check RUN='^TestReferenceTransactionIsolationQueuedConflictTimeout$'
make check RUN='^TestReferenceTransactionIsolationQueuedConflictTimeout$' REFERENCE=1
```

These affected-surface checks do not replace whole-project quickcheck or
post-integration finalcheck.

### Commit timestamp information

This package starts its single pinned oracle with commit timestamp tracking on;
native scenario engines inherit the same constructor-only startup mode.
The selected functions return independently checked typed values:
`pg_xact_commit_timestamp(xid) -> TIMESTAMPTZ`,
`pg_xact_commit_timestamp_origin(xid) -> RECORD(timestamp TIMESTAMPTZ, roident OID)`,
and `pg_last_committed_xact() -> RECORD(xid XID, timestamp TIMESTAMPTZ, roident OID)`.

Lifecycle witnesses use each backend's server clock around actual publication,
then check exact lookup/last identities and local origin 0. They do not compare
backend allocator values or wall-clock timestamps. Explicit SQL/API and
autocommit writes, catalog publication, rollback/aborted/read-only stability,
cross-session observations, prepared public-XID agreement, pending/aborted
unavailability, empty PREPARE and reverse allocation-order completion are
covered without adjacent transaction-ID inquiry functions.
SQL-visible SSI pivot diagnostics retain that same canonical public XID after
preparation/completion; internal incarnation counters are not a second public
identity. The prepared SSI witness compares against its independently queried
`pg_prepared_xacts.transaction`, without pinning allocator numbers.

Public XIDs are assigned by accepted nonempty writes, actual physical-row grants
and catalog installation, not by potentially modifying statement classification.
`TestReferenceCommitTimeAssignmentBoundaries` checks that empty DELETE, UPDATE
and FOR UPDATE scans preserve last in autocommit and explicit transactions;
actual mutation/locking commits replace it. Rolling all inserted rows back to a
savepoint does not erase genuine assignment before plain COMMIT.

`TestReferenceCommitTimeCopyPublication` covers autocommit/explicit stream
completion, empty COPY stability and malformed-stream failure atomicity.
`TestReferenceCommitTimeConcurrentIndexPublication` observes a successful build
and a durable first phase before a duplicate-key build failure. The failed build
preserves that phase's exact timestamp/XID and visible invalid index. These
witnesses compare independently queried identities, not counts of backend
internal commits.

Metadata witnesses distinguish whole SQL NULL from present all-NULL records,
check raw RECORD representations before normalization, inspect empty cursors,
and retain field/FROM aliases, timezone formatting and held-statement reuse.
Views/defaults remain symbolic; volatile inquiries are rejected in immutable
contexts. Native-only Reset/Restore phases check history retirement, preserved
startup mode, fresh nonreused identity and failed replacement atomicity.
Replacement remaps symbolic inquiry arguments and their source attributes,
including unary XID children, rather than capturing an engine or evaluated
history. The expression unit checks both zero-input persistence and detached
child remapping; the controls scenario exercises actual Dump/Restore replay.

Run from the repository root with an external 900-second deadline per command:

```sh
make check RUN='^TestReferenceCommitTime(Startup|Metadata|Lifecycle|Prepared|Publication|Replay|Controls|FeatureSmoke|AssignmentBoundaries|CopyPublication|ConcurrentIndexPublication)$'
make check RUN='^TestReferenceCommitTime(Startup|Metadata|Lifecycle|Prepared|Publication|Replay|Controls|FeatureSmoke|AssignmentBoundaries|CopyPublication|ConcurrentIndexPublication)$' REFERENCE=1
make check RUN='^TestCommitTime(Assignment|Publication|Replacement|Expression)$'
make check RUN='^TestReferencePreparedTransactionSSIImmuneWriter$'
make check RUN='^TestReferencePreparedTransactionSSIImmuneWriter$' REFERENCE=1
make check RUN='^TestSerializableGraphFinalizedPivotIdentity$'
```

Standalone smoke commits a visible row, bounds its timestamp, checks last and
both lookups, proves rollback stability, then commits a prepared transaction
whose preobserved XID must equal last completion:

```sh
make check RUN='^TestReferenceCommitTimeFeatureSmoke$'
make check RUN='^TestReferenceCommitTimeFeatureSmoke$' REFERENCE=1
```

The review-remedy smoke selects actual assignment, COPY and durable index-phase
behavior together:

```sh
make check RUN='^TestReferenceCommitTime(AssignmentBoundaries|CopyPublication|ConcurrentIndexPublication)$'
make check RUN='^TestReferenceCommitTime(AssignmentBoundaries|CopyPublication|ConcurrentIndexPublication)$' REFERENCE=1
```

### Inherited physical mutation and ALTER admission

`TestReferenceInheritanceDMLContention` independently specifies physical child
conflicts, ONLY-parent independence, RC latest-row refresh/predicate/delete,
RR40001, exclusion of a later inserted match and cancellation with whole-statement
rollback. PostgreSQL observes the real blocker before release/cancel. Public
native outcomes do not replace private exact manifest/grant/queue/publication
units. Physical PostgreSQL tuple Context is an external-server diagnostic,
never synthesized as native tuple storage.

Root runs with an external 900-second deadline and requires both completed
modes before review:

```sh
make check REFERENCE=1 RUN='^TestReferenceInheritanceDMLContention$'
make check RUN='^TestReferenceInheritanceDMLContention$'
```

`TestReferenceInheritanceAlterMergeStopLocks` separately observes real child AX
conflicts: valid existing SET NOT NULL completes while that conflicting child
lock remains held; default ADD CHECK can acquire preparatory descendant locks
even when its compatible inherited fact later merges at the named root; ONLY
merge does not infer the same preparatory lock set. The blocking lane observes
the actual server blocker before release, not elapsed time or command entry.

```sh
make check RUN='^TestReferenceInheritanceAlterMergeStopLocks$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceAlterMergeStopLocks$'
```

### Prepared transactions

The prepared-transaction family independently asserts state/diagnostics,
ownership and five-column XID metadata, physical-origin-close survival,
SESSION/LOCAL and TEMP restrictions, held resources, coupled existing-index/
ALTER/data/sequence publication, SSI finalization and native replacement guards:

```sh
make check RUN='^TestReferencePreparedTransaction'
make check REFERENCE=1 RUN='^TestReferencePreparedTransaction'
make check RUN='^TestReferencePreparedTransactionFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePreparedTransactionFeatureSmoke$'
```

Bound each invocation to900 seconds. Smoke expects private7 invisible to both
origin/peer, one correctly typed stable prepared row, survival after actual
physical origin exit, peer commit publishing7, and rollback9 preserving7.
Register bounded GID-specific ROLLBACK PREPARED cleanup before each prepare-capable
operation, ahead of schema/pool/role cleanup; never blanket-clean unrelated GIDs.
sql.Conn.Close only returns a borrower. Dispose the isolated pool/raw driver
connection to witness physical origin end. All worker cancellation/join paths
remain bounded. The pinned fixture enables max_prepared_transactions100 at
startup; native has no matching capacity/configuration surface.

Prepared deferrable coverage distinguishes real admission from goroutine start:
PostgreSQL's genuine SafeSnapshot observer asserts admitted predecessor0 across
PREPARE/origin close and either final outcome. Native has no public readiness
catalog/notice; package-function tests admit a real candidate through the existing
tracker barrier and assert retained original roots/frontier and predecessor0.
The shared both-backend synchronous statement-timeout companion proves a prepared
writer remains unresolved, then verifies exact fresh commit1/rollback0 after
actual completion. No scheduling sleeps or0-or1 tolerance substitute for admission.

Private `TestPreparedPopulatedReplaceCatalogGuards`,
`TestPreparedPopulatedCompetingCompletionAndRetry`,
`TestPreparedCompetingGIDDisposesWholeLoser`,
`TestPreparedLateCancellationAndWaiterObservePublication` and
`TestPreparedValidationOmitsInstallationRoots` use real admitted AST statements
and populated surviving UPDATE plus insert/deletion/catalogue/index/generator/
SSI/ticket state. They cover same-owner and foreign-owner replacement refusal,
actual competing claims, outside-metadata expression barriers, cancellation and
independent final counter veto retaining the exact UPDATE/base/version, committed
heads, original ticket, other roots and all Engine counters; whole losing-GID/
settings disposal; successful retry exactly once; and publication visible to a
real resource waiter before late cancellation. The validation-only structural
regression checks genuine live UPDATE predecessors and rejects wrong base
versions and already-ended predecessors in both phases. Validation-only work
produces no replacement heads/slot arrays; materializing work produces a separate
head and the exact update-slot manifest without installing it during validation.
These package tests issue no public SQL and add no production hooks.
Every concurrent boundary scenario registers cleanup before launching workers:
arrival/outcome waits have deadlines, cleanup releases admission/expression
barriers once, cancels outstanding operation/request contexts and joins every
launched worker through bounded waits, including assertion-failure paths.
Admitted AST fixtures specify parser-established provider defaults explicitly
(for example, CREATE INDEX uses `Method: "btree"`); an empty method is not an
engine default. The existing changed-predecessor publication regression keeps
its original canonical `mergeIndexRoot` preparation boundary and all unchanged-
state, real-peer retry, final-veto and exact row/index/SSI outcome assertions.
`TestIndexClausesReplacement` and `TestIndexClausesValidationOnlyReplacement`
also preserve the canonical nonunique replacement contract for reduced valid
parent/index catalogues: ordering/provider/shape checks require no namespace
descriptor. Namespace binding remains required when constructing UNIQUE
diagnostics. Invalid key definitions still fail both validation-only and
materializing phases.

```sh
make check RUN='^TestPrepared'
```

Held-resource timeout Context includes genuine PostgreSQL physical tuple
coordinates only on PostgreSQL; native independently asserts its absent Context.

### Session advisory lock reference contract

The untagged `TestReferenceAdvisorySession` family uses the same completed public
definitions in both modes. It enumerates thirteen signatures through direct
Query/Exec and genuinely held prepared handles; validates raw VOID/BOOL/NULL and
metadata; and independently checks disjoint keys, mode/count matrices, complete
warnings, rollback/pooling/physical close, wait/cancel/timeout/deadlock, CHECK
runtime demand and native Reset/Dump/Restore staging. Stored CHECK/view SQL is not
live ownership serialization, and Restore's direct scalar allowlist stays closed.

`TestReferenceAdvisorySessionReviewCheckPreparation` distinguishes constant-NULL
suppression from runtime-column-NULL demand across writes, held prepared statements,
ALTER rewrites and native staged replay. It checks quiet complete warning capture,
peer-held blocking keys, unchanged counted ownership and execution-stage immutable
child errors before any volatile CHECK runs. `...ReviewQuotedArgumentLimit` asserts
all seven quoted nonexact spellings and qualification through direct/real Prepare,
including complete54023 diagnostics and earlier bad-child42703 source positions.
Private `TestAdvisorySessionCheckPreparationCache` and
`...CheckPrepareAllBeforeEvaluation` supplement the public outcomes with command/
descriptor cache isolation, copy-on-change syntax preservation, row-frame restoration
and complete immutable-child diagnostics. `TestCheckAbsentValidatorHasNoAllocation`
retains the no-CHECK allocation regression.

Root first resolves any new unknown result on pinned PostgreSQL18.6, then runs
both complete modes after changes. Discovery filters/logging are temporary only;
native controls are explicit native phases, not invented PostgreSQL equivalents.
Exact PostgreSQL pending evidence uses a narrow advisory `pg_locks` observer and
`pg_blocking_pids`. Native public outcomes are paired with private exact under-mutex
queue/fence tests; worker-start or elapsed sleep is never registration proof.
Every worker owns cancellation and bounded join before physical/schema cleanup.
Prior advisory holds survive deadlock/timeout/rollback; cleanup must explicitly
unlock or close them before requiring a blocked survivor to finish.

Each selected check invocation has a900second deadline:

```sh
make check REFERENCE=1 RUN='^TestReferenceAdvisorySessionCheckConsumers'
make check RUN='^TestReferenceAdvisorySession'
make check REFERENCE=1 RUN='^TestReferenceAdvisorySession'
make check RUN='^TestReferenceAdvisorySessionReview'
make check REFERENCE=1 RUN='^TestReferenceAdvisorySessionReview'
make check RUN='^TestReferenceCheck(VolatileEffects|ScalarConstants|CompleteRowAndAtomicity|CorePreparedFrames|Controls|ExplicitTransactionAndSavepoint|PreparedExecutionOutputEnvironment|AlterCandidates|AlterCandidateTransactions|ReviewReplayHistory|ReviewReplayOwnedSequence|Review2OrderLogicalRoundtrip)'
make check REFERENCE=1 RUN='^TestReferenceCheck(VolatileEffects|ScalarConstants|CompleteRowAndAtomicity|CorePreparedFrames|Controls|ExplicitTransactionAndSavepoint|PreparedExecutionOutputEnvironment|AlterCandidates|AlterCandidateTransactions|ReviewReplayHistory|ReviewReplayOwnedSequence|Review2OrderLogicalRoundtrip)'
make check RUN='^TestAdvisorySession'
make check RACE=1 RUN='^Test(AdvisorySession|RelationLock|RelationDeadlock)'
make check RUN='^TestReferenceAdvisorySessionFeatureSmoke'
make check REFERENCE=1 RUN='^TestReferenceAdvisorySessionFeatureSmoke'
```

The feature smoke requires double prepared acquisition42, rollback survival,
peer exclusion until the second matching decrement, exact unmatched warning,
shared pair coexistence and bigint independence, real timeout/wake outcomes and
empty unlock-all. Native phases additionally require retained counts and data
through actual controls. Private ownership/graph/binding/parser tests supplement,
not replace, the identical public contract. Finalcheck runs only after merge
under one1800second deadline.

### Transaction advisory lock reference contract

The four transaction advisory functions support bigint and integer-pair overloads,
shared/exclusive blocking requests and nonblocking Boolean try requests. Both
lifetimes share resource identities and conflicts, while the two overload spaces
remain disjoint. Same physical-session reentry spans lifetimes and can pass a
queued competitor, never an incompatible external grant. Transaction repeated
mode acquisition is idempotent; session acquisition remains counted.

Transaction modes release at implicit statement completion, full transaction end
(including CHAIN), physical end and rollback to their acquiring savepoint.
Earlier modes survive rollback; RELEASE retains child contributions. Rows/Stmt
close and borrower return do not end an explicit block. No manual transaction
unlock exists, and session unlock/unlock-all cannot remove those contributions.
PREPARE transfers the exact transaction ticket; origin reuse/close cannot bypass
or release detached ownership. Holding both lifetimes on the same tag, including
different modes, rejects PREPARE with exact0A000/English diagnostics and ends the
transaction without publishing a GID/private writes. Different tags/spaces do not.

`pg_locks` aggregates granted modes per physical participant/tag, not lifetime or
repeat count. Detached prepared owners remain separate and have NULL PID; pending
requests retain their actual mode, wait timestamp and blocker. Native successful
replacement retires transaction ownership and fences old waits while preserving
session holds; failed staging preserves both.

The eight untagged `TestReferenceAdvisoryXact` tests independently specify all8
signatures/direct and held Query/Exec, formal inference/strict demand/complete
diagnostics, lifetime/mode conflict matrices, actual pending wakeups, savepoints,
pooling/physical termination, cancellation/timeouts/deadlocks, prepared ownership
and lock metadata. Workers cancel and join before teardown. Discovery used the
pinned18.6 fixture first; no discovery filter remains. Internal tests exercise
stored lifetime, exact grants/journals, savepoint intersections, replacement
readmission, prepared transfer, snapshot capture and terminal races.
PostgreSQL participant identity uses `pg_backend_pid()`. Native observation and
the standalone consumer reuse the existing public TEMP-namespace identity route:
initialize TEMP outside potentially prepared transactions, then read the physical
session suffix from its `pg_temp_` namespace. This does not add native
`pg_backend_pid()` support or weaken exact waiter/blocker checks.

Run each selected command with an external900second deadline. Every changed
reference definition requires both modes:

```sh
make check RUN='^TestReferenceAdvisoryXact(Signatures|DiagnosticsStrictness)$'
make check RUN='^TestReferenceAdvisoryXact(Signatures|DiagnosticsStrictness)$' REFERENCE=1
make check RUN='^TestReferenceAdvisoryXact(CompatibilityBlocking|LifecycleSavepoints)$'
make check RUN='^TestReferenceAdvisoryXact(CompatibilityBlocking|LifecycleSavepoints)$' REFERENCE=1
make check RUN='^TestReferenceAdvisoryXact(CancellationTimeout|Deadlocks)$'
make check RUN='^TestReferenceAdvisoryXact(CancellationTimeout|Deadlocks)$' REFERENCE=1
make check RUN='^TestReferenceAdvisoryXact(PreparedTransactions|LockMetadata)$'
make check RUN='^TestReferenceAdvisoryXact(PreparedTransactions|LockMetadata)$' REFERENCE=1
make check RUN='^TestAdvisoryXact(BindingTraversal|OwnershipCompatibility|SavepointLifecycle)$'
make check RUN='^TestAdvisoryXact(PreparedOwnership|LockSnapshot|TerminalRaces)$'
make check RUN='^TestAdvisoryXact(SavepointLifecycle|PreparedOwnership|TerminalRaces)$' RACE=1
make check RUN='^TestReferenceAdvisoryXact(CancellationTimeout|Deadlocks)$' RACE=1
make check RUN='^TestReferenceAdvisorySession(Signatures|DiagnosticsConsumers)$'
make check RUN='^TestReferenceAdvisorySession(Signatures|DiagnosticsConsumers)$' REFERENCE=1
make check RUN='^TestReferencePreparedTransactionSessionBoundaries$'
make check RUN='^TestReferencePreparedTransactionSessionBoundaries$' REFERENCE=1
make check RUN='^TestAdvisorySession(LifecycleReplacement|QueueTimersDeadlock|CompatibleCohortTerminalOrder|PhysicalSoftCycle)$'
```

The standalone public consumer is supplied as session source
`local://advisory-xact-feature-smoke.go`, not a permanent duplicate SQL test.
Materialize it at `/tmp/pg18-tx-advisory-xact-smoke/main.go`, then run from the
worktree (external900second deadlines):

```sh
go run /tmp/pg18-tx-advisory-xact-smoke/main.go -case lifecycle
go run /tmp/pg18-tx-advisory-xact-smoke/main.go -case prepared-metadata
```

`lifecycle` must report held VOID/counts, overload independence, savepoint mode
release/earlier retention, unlock-all isolation, actual pending/blocker observation
and rollback wakeup, healthy try failure and native replacement lifetime effects.
`prepared-metadata` must report one overlapping exclusive row, exact mixed
PREPARE rejection without GID, retained session exclusion, NULL-PID detached
ownership, origin exclusion, peer completion release and real private-write
commit/rollback. Any failed assertion exits nonzero. Remove the temporary
materialization after recording argv/output and expected-versus-observed evidence.

### Transaction and ALTER smoke migrations

The former public SQL smoke scenarios now live in untagged reference tests.
Run both selectors before review, each under its own 900-second deadline:

```sh
make check RUN='^TestReference(TransactionControlsSmoke|TransactionSmoke|TransactionBeginContextCancellation|ControlTransactionsSmoke|AlterNullability(Smoke|ControlsSmoke)|AlterDropColumn(Smoke|ZeroColumnControlsSmoke))$'
make check REFERENCE=1 RUN='^TestReference(TransactionControlsSmoke|TransactionSmoke|TransactionBeginContextCancellation|ControlTransactionsSmoke|AlterNullability(Smoke|ControlsSmoke)|AlterDropColumn(Smoke|ZeroColumnControlsSmoke))$'
```

These definitions preserve direct and prepared SQL, held statements, independent
rows/types/counts and complete logical diagnostics. Native ScanType and known
nullability assertions supplement PostgreSQL's public wire metadata without
requiring lib/pq to report native-only descriptors. SQL transaction visibility,
abort recovery, CHAIN, notices, ALTER atomicity, dropped-column dependencies,
quoted names, owner authority, comments and zero-column multiplicity run against
both backends. Dump/Reset/Restore, retained eager native rows, replacement
incarnation errors and Restore reader cancellation remain explicit native control
phases; PostgreSQL has no equivalent engine control API. No migration enables
additional engine functionality or turns a native failure into an oracle golden.

Pre-canceled native BeginTx deterministically rejects admission with an ordinary
context error and no effects. In the pq route, pre-canceled admission and rollback
race the adapter's context watcher; their incidental transport outcomes are not
PostgreSQL SQL expectations. These scenarios clean up any returned transaction
and explicitly retire the canceled physical connection, then independently assert
unchanged rows through the unaffected observer and a fresh usable borrower.
The live canceled transaction still publishes no private rows, and a subsequent
transaction commits its independently expected row. The context-cancellation
definition additionally exercises admitted server cancellation in direct/prepared
routes. Run the changed cancellation definitions under race in both modes before
review, each invocation bounded to 900 seconds:

```sh
make check RACE=1 RUN='^TestReferenceTransaction(Smoke|BeginContextCancellation)$'
make check RACE=1 REFERENCE=1 RUN='^TestReferenceTransaction(Smoke|BeginContextCancellation)$'
```

### Installed pgrowlocks

The shared PostgreSQL18.6/native scenarios cover the one installed TEXT→SETOF
RECORD overload, all six output descriptors, strict NULL, each row-lock/action
mode, real conflicting handoff, raw MultiXact bags and ended PID0 members,
RC/RR/own-overlay exact-version visibility, inherited singleton headers,
permissions/kinds/names, expression routes and owned retained Rows, extension
lifecycle/dependency/reinstall, native controls and relation-only AS waits.
Owner XIDs/PIDs are captured from real dedicated sessions, not fixed goldens:
after assigning an XID, read its granted transactionid ExclusiveLock in
`pg_locks`. This independently witnesses backend identity without depending on
the unsupported `pg_backend_pid()` or on the pgrowlocks output being tested.
Idle waiters use a temporary identifying transaction, rolled back before the
queued command so its autocommit cancellation/replacement semantics are unchanged.
Unordered rows and aligned member triples are bags with multiplicity.

The independently observed strong-lock promotion is exact: prior FOR UPDATE
then non-key UPDATE reports Update on the predecessor, not No Key Update.
The fresh conflicting updater in handoff/smoke has no earlier stronger grant
and reports No Key Update. Native TIDs are checked logical version identities,
not PostgreSQL coordinates or a `table.ctid` implementation. PostgreSQL's child
XIDs remain an inherited native boundary; the named rollback witnesses are
top-level transactions. PostgreSQL-only nonheap AM and full pg_proc declaration
metadata use genuine oracle setup, not fabricated native descriptors/catalog
columns. Shared direct/prepared six-field metadata and U4 declaration/OUT-shape
checks remain mandatory.

Run each group in both modes, with external 900-second deadlines:

```sh
make check RUN='^TestReferencePgrowlocks(InstalledAndShape|NamesPermissionsAndDiagnostics|ExpressionRoutes)$'
make check REFERENCE=1 RUN='^TestReferencePgrowlocks(InstalledAndShape|NamesPermissionsAndDiagnostics|ExpressionRoutes)$'
make check RUN='^TestReferencePgrowlocks(ModesAndOwners|ConflictingHandoff|RawMultiAndPID|VisibilityAndVersions)$'
make check REFERENCE=1 RUN='^TestReferencePgrowlocks(ModesAndOwners|ConflictingHandoff|RawMultiAndPID|VisibilityAndVersions)$'
make check RUN='^TestReferencePgrowlocks(LifecycleAndControls|RelationWaitAndIdentity|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePgrowlocks(LifecycleAndControls|RelationWaitAndIdentity|FeatureSmoke)$'
make check RUN='^TestPgrowlocks(LogicalTID|Capture|Eligibility|ProviderIdentity|RelationIdentity|InheritedOwnerProvenance|XIDArray)$'
make check RACE=1 RUN='^(TestPgrowlocks(Capture|Eligibility|RelationIdentity|InheritedOwnerProvenance)|TestReferencePgrowlocks(ConflictingHandoff|RawMultiAndPID|RelationWaitAndIdentity))$'
```

Actual feature smoke:

```sh
make check RUN='^TestReferencePgrowlocksFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePgrowlocksFeatureSmoke$'
```

The smoke installs1.2, locks an actual row in A, observes A directly and through
a held prepared call, queues B's fresh UPDATE, proves B is not yet an owner,
rolls A back and observes actual B's No Key Update, then rolls B back and
requires empty output/original data. It checks stable reacquisition TID, strict
NULL/full metadata, raw compatible Multi members with ended PID0 and a distinct
committed successor TID. Workers cancel/join and Rows close before fixture reset.
No successful native verification is implied merely by this documented argv.

