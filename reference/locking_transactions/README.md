# reference/locking_transactions

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

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

Root first resolves any new unknown result on pinned PostgreSQL18.0, then runs
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
