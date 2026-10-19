# internal/engine

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy and
public/private test placement. Run these commands from the repository root.

Transaction identity, commit order, commit timestamps, snapshot views and SSI
belong to `visibility.Registry` (`Engine.visibility`) under Engine.mu; see
[visibility/README.md](visibility/README.md), which also lists the identity
and commit-timestamp selectors.

MultiXact records, counters and version history belong to the lock manager; see
[lock/README.md](lock/README.md). Raw candidate grants imply neither assignment
nor membership. CHECK/branch admission precedes the physical event; index and
RETURNING evaluation follow it. Shared mutations journal successor/deletion
events while private roots are locked and flush after Engine/owner unlock,
before caller-visible evaluation. No Engine.mu→manager mutex acquisition is
introduced; status reads ticket atomics.

Ordinary UPDATE accumulates distinct accepted physical targets by amortized append,
without copying the growing private prefix per row. Its frozen source and FROM
winner gate supply distinctness; existing action-prefix overlaps retain immutable
replacement merging. Append choice does not bypass ordinary target/RI validation
or UPSERT's separate affected-row cardinality check. Physical events still precede
unique/RETURNING boundaries, and Finish discards private stages on failure.

```sh
make check RUN='^TestPGLocks(ProjectionModes|TransactionLifetime|PredicateLifetime)$'
make check RUN='^TestUpdateCore(MultirowAllocation|StagedPrefixOverlap|SparseAllocation)$'
make check RUN='^Test(WalkBoundExpressionVisitsCompositeChildren|InstalledFunctionRebuildKeepsWrapper)$'
```

Use external 900-second deadlines. These actual-seam units seed wrap/horizon/
collision state, immutable bags, one-updater filtering, real grant/cancel paths,
busy prepared claims and symbolic original provider ownership; public SQL and
all-six-function smoke remain in `reference/locking_transactions`.


`TestObjectACLTypeSetterProvenance` exercises actual `Engine.SetObjectACL`
publication for domains, enums and standalone composites. Genuine replacement
clears constructor-synthetic provenance for explicit default-equivalent, empty
and owner-only ACLs. The test checks raw visibility, provider validity, retained
catalog snapshots and rejection of explicit ACL serialization before either
writer interface is called.

```sh
make check RUN='^TestObjectACLType(SetterProvenance|Provenance)$'
make check RACE=1 RUN='^TestObjectACLTypeSetterProvenance$'
```

Public grant, raw-ACL, held-execution and native-control witnesses live in
[`reference/object_grants`](../../reference/object_grants/README.md), not in this
package's private invariant tests.

`TestObjectAddressRoutineOriginalAuthority`, `SourceNullOriginalGraph`,
`ScalarNullPreflightInfo` and `SourceFalseDemand` (with the ObjectAddress prefix)
retain original inquiry descriptors, complete manifests and immutable source
schemas while execution-only NULL/empty-source pruning changes actual demand.
Independent imported NULL proofs do not erase per-argument domain barriers or
actual typed-domain input validation.

`TestNumericSeriesRoutineOriginalAuthority`, the two `TestJSONRecordRoutine`
definitions and `TestIntaggRoutineOriginalAuthority` exercise selected full
formal identities, genuine source/project admission and missing/empty/foreign/
stale manifest rejection. Shape completion, retagging, relocation and stored
recipe grafting must retain original authority, not recover it from an OID.
`TestIntaggRoutineIndependentOwners` covers actual COW ownership staging, named
owner metadata import and reassignment with independent member owners.

The six `TestJSONBRecordRoutine` definitions apply original-authority, stored-view
recipe, ended-scope, failed-constructor and real ACL/owner-COW invariants to all
five existing JSONB providers. Compatible owner declarations do not waive revision
checks: retained old snapshots remain valid, an old graph rejects a changed current
revision, and fresh analysis captures the new owner/revision before admission.

These are private provider/lifetime invariants, not duplicate public SQL tests.
Fixtures create stored recipes before retaining statement admission; entering
Engine.Execute on an operation that already owns admission would recursively
acquire its mutex. Parameterized expressions retain structural rewrite coverage
without pretending they are persistable native recipes.

```sh
make check RUN='^TestObjectAddress(RoutineOriginalAuthority|SourceNullOriginalGraph|ScalarNullPreflightInfo|SourceFalseDemand)$'
make check RACE=1 RUN='^TestObjectAddress(RoutineOriginalAuthority|SourceNullOriginalGraph|ScalarNullPreflightInfo|SourceFalseDemand)$'
make check RUN='^(TestJSONRecordRoutine(OriginalAuthority|FailedConstructor)|TestIntaggRoutine(OriginalAuthority|IndependentOwners)|TestNumericSeriesRoutineOriginalAuthority)$'
make check RACE=1 RUN='^(TestJSONRecordRoutine(OriginalAuthority|FailedConstructor)|TestIntaggRoutine(OriginalAuthority|IndependentOwners)|TestNumericSeriesRoutineOriginalAuthority)$'
make check RUN='^TestJSONBRecordRoutine(OriginalAuthority|StoredRecipe|EndedScope)$'
make check RACE=1 RUN='^TestJSONBRecordRoutine(OriginalAuthority|StoredRecipe|EndedScope)$'
make check RUN='^TestJSONBRecordRoutine(FailedConstructor|OwnershipCOW|ACLCOW)$'
make check RACE=1 RUN='^TestJSONBRecordRoutine(FailedConstructor|OwnershipCOW|ACLCOW)$'
```

Virtual-generation private invariants retain the original owning recipe,
current-invoker authority, captured-source mapping and allocation-free lookup.
Cursor invariants retain DECLARE's genuine authorization snapshot while rejecting
missing/foreign/stale original manifests at FETCH/promotion and releasing all
producer authority at teardown. Stored-recipe replacement checks actual private
catalog remapping; public SQL answers and Dump/Restore live in `reference/`.

```sh
make check RUN='^(TestAlterGeneratedExpressionCapturedVirtual|TestVirtualGenerationOriginalAdmission|TestSQLCursorRoutineAuthorityLifetime)$'
make check RUN='^(TestAlterGeneratedExpressionCapturedVirtual|TestVirtualGenerationOriginalAdmission|TestSQLCursorRoutineAuthorityLifetime)$' RACE=1
make check RUN='^TestRoutineBody(OriginalLifetimeAndForeignRetag|StoredRecipeReplacement|EveryProviderOriginalLifetime)$'
make check RUN='^TestUpdateCoreRecursivePreflight$'
```

`TestNetworkOperatorRoutineOriginalAuthority` independently checks the seven
canonical declarations, complete original manifests and the one genuine SQL
edge, including missing/foreign/stale authority rejection. The SQL-body lifetime
witness now binds 40 distinct roots in 42 instances, retaining independent
BOOL/INET cast-reference removal cases.

```sh
make check RUN='^(TestNetworkOperatorRoutineOriginalAuthority|TestRoutineBodyEveryProviderOriginalLifetime)$'
make check RUN='^(TestNetworkOperatorRoutineOriginalAuthority|TestRoutineBodyEveryProviderOriginalLifetime)$' RACE=1
```

`TestTIDRoutineAuthority` binds all six existing row-backed comparison providers,
retains their canonical original descriptor/reference/revision and rejects
missing/foreign/stale or forged-retag authority. No private SQL-answer oracle or
new physical TID mechanism is involved.

```sh
make check RUN='^TestTIDRoutineAuthority$'
make check RUN='^TestTIDRoutineAuthority$' RACE=1
```

`TestACLValueRoutineOriginalAuthority` checks the four existing bound ACL
providers' full IN signatures, original descriptors/manifests and authentic
missing/foreign/stale/retag rejection. Expansion retains physical RECORD2249,
four OUT fields and SETOF classification; genuine source/project admission
uses `(false,false)`, not the scalar-planning exemption. Bare expression binding
does not itself mark the enclosing query's target-set observation.

```sh
make check RUN='^TestACLValueRoutineOriginalAuthority$'
make check RUN='^TestACLValueRoutineOriginalAuthority$' RACE=1
```

`TestPartitionRoutineOriginalAuthority` exercises genuine bound partition-key
outer/body manifests, missing/empty/foreign/stale/retag rejection and sibling
isolation. Complete-success statement preparation preserves enclosing row/scope/
frame, actual actor/command/RI phases and graph-only post-wait readmission.
The simple-key path initializes no derived program. The retained-check fixture
uses its actual copied generation owner/attribute; missing-owner rejection stays.

```sh
make check RUN='^TestPartitionRoutineOriginalAuthority$'
make check RUN='^TestPartitionRoutineOriginalAuthority$' RACE=1
```

### Server protocol ownership

The wire adapter uses the existing session, transaction, prepared-analysis and
typed-frame authorities. `ProtocolCycle` retains implicit transaction work, not
an operation or admission lock; each message acquires its own command context.
Sync closes implicit work without closing an explicit transaction. Simple-query
plain SELECT output uses the initialized live producer before statement Finish;
store-backed output uses a finished retained scope. Both release admission and
the replacement read fence through reserved I/O and validate the exact current
owner on resume. Non-retained transaction controls borrow the existing owner
identity for pre-execution disposal without starting a transaction.

Prepared SQL and wire statements share names. Wire portals share collision and
Close visibility with SQL cursors but own separate execution position/lifetime.
Plain SELECT binds its snapshot before target demand; finite execution performs
no extra-row lookahead. Retained producers and once-executed row stores do not
replay volatile projections or writes on resume. Closing a statement does not
discard an already bound portal.

Retained typed execution state is not retained wire presentation. Each Execute
serializes through its fresh scope settings and per-row borrowed catalog readers,
recycling spelling maps so repeated OIDs follow the current effective path and
quoting policy. Live producers keep their captured catalog interpretation; stored
destinations select the current canonical catalog once on their first tuple.
Catalog-derived operator groups are reused within that destination, not rebuilt
per row. Stored typed rows and their embedded immutable output environment are
never rewritten, rebound or replayed to refresh wire presentation.

`ExecuteProtocolSimple` uses the canonical execution core, including shared
prepared-name resolution and parameter import. Direct wire SELECT plans closed
immutable expressions before executor startup, preserving imported-parameter,
row, domain and volatile demand barriers. This protocol policy uses the same
canonical preflight helpers without changing embedded ordinary arithmetic's
existing row-owned phase boundary. The prepared graph feeds the live producer:
actual schema precedes row demand, and an execution error cannot retract emitted
rows. Planning errors emit neither schema nor rows. RETURNING, modifying-CTE and
utility-row strategies retain their store-before-destination boundary; failure
while filling that store does not fabricate a RowDescription. Embedded Execute
keeps its immutable, eagerly finalized Rows contract.
SQL EXECUTE retains its outer utility strategy: simple portal creation precedes
the existing prepared-name lookup, actual arguments and inner planning. Its
resolved SELECT fills a row store before destination startup, so an inner
execution error emits no RowDescription or partial DataRow prefix. Named sibling
portals remain intact; the resolved body is not promoted to a direct live SELECT.

Published EXECUTE utilities resolve iteratively through that same authority,
preserving the outer utility/store strategy at every supported nesting level.
Each invocation imports its own nonzero formal frame once before the next
lookup; wire EXECUTE retains explicit unused hints. These values do not create a
lexical `$n` namespace inside another EXECUTE utility. Zero-formal utilities keep
their existing ignored-actual policy. Intermediate frame scratch is cleared and
reused, obsolete importer captures are released, and successive consumed actual
graphs coalesce through the existing exact reference/consumer/actor union without
dropping lifetime checks or changing readmission order. The incoming Expected
signature is restored on exit; leaf query signatures and COPY's zero/nil inner
query parameters retain their normal ownership. Every hop checks the original
operation's cancellation/session state. No synthetic cycle/depth limit or
PostgreSQL host-stack diagnostic is introduced or claimed equivalent.

Eligible EXISTS preparation follows the existing presence-only plan: unused
outer targets are not evaluated, while demanded child predicates and existing
presence/sort policy still receive canonical preflight.

Simple Query removes only the unnamed portal, at actual planned-command execution
or untransformed utility entry. Empty Query and failures in required outer
parsing/analysis/planning do not reach that event; SQL EXECUTE's inner lookup and
planning follow its utility event. Failed unnamed Parse removes its old statement
before raw parsing but preserves already bound portals. Named Parse publishes
only after required analysis and rejects duplicate names without replacing the
original. Describe looks up its statement/portal first; in an aborted transaction,
row descriptors (including zero-column results) fail before parameter/result
metadata, while retained NoData and empty descriptions remain available.

SQL cursors and wire portals use one birth frontier. Both savepoint restoration
paths retire newer same-transaction wire portals through unlocked terminal
cleanup; older producer positions are not rewound.

Executor completion and name retirement are distinct. Exhausted SELECT and
row-store portals remain fetchable with current-fetch count zero; completed
non-row DML, controls and COPY reject another Execute with 55000. Empty-query
portals retain their separate EmptyQueryResponse behavior. An execution failure
disposes executor/frame/output resources but retains a failed name and descriptor
when its birth survives savepoint restoration. That name rejects execution with
55000 and duplicate Bind with 42P03 until Close or actual lifetime retirement.
Terminal disposal uses a logical reservation even after cancellation, never
resumes an executor and never replaces the primary execution error.

Query-creation utilities cross simple portal creation after canonical analysis
and before destination skip notices/schema effects, including WITH NO DATA and
existing IF NOT EXISTS destinations. These fast paths do not demand the SELECT
producer. Pending catalog publication uses the exact active operation/scope and
canonical retained incarnation, whether retention comes from a SQL message or
a protocol cycle; no synthetic message or transaction expands embedded DDL.

COPY uses the same text/CSV/binary executor as native streams. Its real preflight
supplies response metadata; callbacks run outside engine/publication locks and
admission. The terminal input hook can veto rows before application/publication,
including CopyFail after a text end marker. File/PROGRAM endpoints remain
unsupported. Notification polling shares the committed native queue/wake owner,
holds no idle activity lease and leaves wire transaction-idle gating to the
serialized adapter. FunctionCall uses actual declared executable OIDs, current
ACL authority and the shared codecs, not lookup-only routine identities.

Binary NULL arguments resolve the declared receive provider before typed NULL
construction and domain checks, without invoking nonnull conversion or container
members. Ordinary row destinations initialize binary send providers once on
their first actual tuple, before NULL attributes are encoded; Bind, Describe and
empty ordinary results do not initialize them. Resumed execution has its own
destination startup. COPY retains its separate stream-initialization boundary.
FunctionCall's strict NULL result still bypasses result-format/provider use.
Its receiver shares typed import with Bind but retains arity-before-format-count
ordering and its own format-count and signed invalid-argument-size diagnostics.

Protocol callers pass COPY stream capability lexically through that same core.
Actual COPY, including a wire-prepared body reached by SQL EXECUTE or an extended
EXECUTE wrapper, uses the real CopyProtocol hooks; no socket callback is saved by
the portal after execution. Embedded Execute passes no capability and retains
CopyBoundary. Nonzero utility formals use the existing SQL actual importer once
before CopyStart, after its arity check; zero-formal behavior adds no new checks.
Unused utility formals are not parameters of the inner COPY query. That inner
query temporarily suspends/restores the outer NoData signature and still uses
the existing fixed-zero-parameter analyzer/executor, without a second binder.

Protocol COPY of a plain SELECT transfers the canonical initialized cursor
directly to the shared exporter, without a registered portal, rebind or replay.
Planning, metadata and current authority precede CopyStart; projection demand
starts only after the real CopyOutResponse. Successfully emitted records survive
a later query error. Intrinsically blocking operators retain their own demand
policy; modifying-CTE/store strategies and embedded COPY retain their existing
execution boundary. Per-record output readers reuse bounded scratch rather than
retaining the query's output history. Disposal runs unlocked while borrowing,
not replacing or prematurely completing, COPY's existing scope reservation.

Protocol binary COPY FROM initializes declared receive functions before
CopyInResponse, visiting all live physical attributes in order, including omitted
COPY targets but excluding dropped history. Each attribute's provider setup
precedes its existing default preflight; earlier defaults retain precedence over
later provider failures. Empty/NULL streams cannot bypass an absent receiver.
A domain has the generic domain receiver, so an empty stream can complete even
when its base receiver is absent. Actual domain input, including NULL, resolves
the base at row invocation after the response and before domain constraints,
preserving genuine COPY row/column error context. Embedded stream and ordinary
Go-context policies remain unchanged.

Binary COPY TO initializes the actual declared send providers after CopyOutResponse
but before its binary signature or any row demand, including empty/NULL results.
A missing provider therefore emits the response and its genuine diagnostic, not
binary data or successful completion. Domains delegate to their base provider;
generic array send does not eagerly initialize its members' providers.

Already-pending typed wire user cancellation is deferred only through genuine
COPY decoding/conversion. The operation selects an actual standard input context
without replacing its original/source context. Only after that cancellation is
pending does the lazy view detach its cancellation key and re-project the exact
captured absolute deadline; no budget is restarted. Required codec conversion
errors keep their genuine row/column context and take precedence. Successful
conversion observes pending cancellation before returning a row to assembly.
The hold is cleared before constraints/application and restored on every exit;
ordinary Go caller cancellation and native input policy remain unchanged.

`Session.EndRequested` lazily observes canonical ending before lease cleanup,
without acquiring a lease or changing `WaitEnd`. Session ending directly cancels
any held input view. The adapter's existing watcher requests ending on physical
shutdown/disconnect and observes this wake for later FATAL delivery, so an earlier
user-cancel cause cannot hide physical or session termination.

Session checks and operation admission observe due transaction/idle budgets
under `Session.mu` before publishing a lease or invalidating the idle slot.
One timestamp feeds the existing callback expiry decision for both slots, so
delayed callback delivery cannot admit work on an expired physical owner.
Generation, transaction authority, retired budgets and pending statement/lock
ERROR priority remain shared with callbacks; termination uses the canonical
first-reason/cancellation/cleanup path. Pre-canceled callers retain their Go
context error before admission.

`TestSessionTimeoutAdmissionMechanics` covers Check, activity and command
admission without scheduler sleeps. `TestSessionTimeoutRetiredAdmissionMechanics`
and `TestSessionTimeoutCanceledAdmissionMechanics` preserve retirement and caller
cancellation; `TestSessionTimeoutPendingOrderMechanics` covers active pending
ERROR priority independently of callback scheduling.

```sh
make check RUN='^TestSessionTimeout(Admission|RetiredAdmission|CanceledAdmission|PendingOrder)Mechanics$'
```

Private mechanisms supplement the real socket tests:

- `TestProtocolPreparedRegistry`: exact-session retirement and independently
  retained portal recipes.
- `TestProtocolValueCodecs`: borrowed field-buffer growth, NULL/empty identity,
  role-reader recapture under changing presentation and occurrence-owned deferred
  RECORD input.
- `TestProtocolCycleRetention` and `TestProtocolPortalProducer`: fresh operation
  admission, exact demand/position, savepoint birth-frontier ownership and
  idempotent producer cleanup.
- `TestProtocolSimpleDisposalFence`: unlocked pre-control disposal with exact
  owner/replacement-fence preservation and reusable output reservations.
- `TestProtocolSimplePlanningBounds`: planning-owned immutable errors, no early
  volatile target demand, actual finite row demand and parameter barriers.
- `TestProtocolFailedExecutorOwnership`: canceled-operation cleanup outside locks,
  retained failed identity, birth-frontier retirement and exactly-once disposal.
- `TestProtocolCopyCancellationContext`: immutable typed SQL cancellation enriched
  from actual cursor context, primary-error precedence and embedded Go-cancel
  identity preservation.
- `TestProtocolCopyInputContext`: real codec conversion precedence, standard
  context cancellation/value/deadline contracts, decoder-only scope, original
  source identity, ordinary caller/timeout behavior and ending before lease drain.
- `TestProtocolCopyStartCancellation`: simple/extended COPY installs its first-record
  cancellation hold before the metadata hook can send CopyInResponse. Immediate
  cancellation retains actual record context and conversion-error precedence;
  the hold and immutable source diagnostic do not escape the invocation.
- `TestProtocolEndRequestedWake`: early/late request observation, repeated
  requests and completion waiting for the actual operation lease.
- `TestProtocolCopyHooks`: unlocked metadata callbacks and terminal veto before
  row application.
- `TestProtocolCopyQuerySignatureScope`: outer utility signature restoration
  across inner-analysis/output failure and unlocked actual output.
- `TestProtocolFunctionInvocation`: aborted-transaction rejection before OID
  consumption.
- `TestProtocolNotificationPoll`: FIFO dequeue, atomic wake registration, no
  activity lease and retirement wake.
- `TestPreparedSQLActualProgramOwnership`: bounded repeated actual-call graphs,
  original consumed-call identity and foreign-authority rejection.

```sh
make check RUN='^TestProtocol(PreparedRegistry|ValueCodecs|CycleRetention|PortalProducer|SimpleDisposalFence|SimplePlanningBounds|FailedExecutorOwnership|CopyCancellationContext|CopyInputContext|EndRequestedWake|CopyHooks|CopyQuerySignatureScope|FunctionInvocation|NotificationPoll)$'
make check RACE=1 RUN='^TestProtocol(CycleRetention|PortalProducer|SimpleDisposalFence|SimplePlanningBounds|FailedExecutorOwnership|CopyCancellationContext|CopyInputContext|EndRequestedWake|CopyHooks|CopyQuerySignatureScope|NotificationPoll)$'
make check RUN='^TestPreparedSQLActual(NoSnapshotDemand|ProgramOwnership)$'
```

Public answers, metadata, ACL timing, cancellation and actual pq/psql behavior
belong to [`reference/sessions`](../../reference/sessions/README.md), using real
server subprocesses and the pinned PostgreSQL fixture. Run affected shared
definitions with `make check RUN='...'` and the same selection with `REFERENCE=1`;
the private mechanisms are not a substitute for that proof. The owning sessions
README contains the separate built-server smoke and complete selector inventory.


### Recursive SEARCH components

`make check RUN='^TestQuerySearch(Shape|Parent|Identity|Remap|Cancellation|Signature)$'`
checks exact occurrence RECORD/_RECORD Usage and NULL fields, actual joined-parent
slots, immutable ancestry, complete generated collision equality including CYCLE,
destination leaf reownership/memoization and strict stored layouts. It distinguishes
stable public result identity from exact internal/parameter shape, and polls real
construction/hash/equality loops for cancellation. Run selected race proof with
`make check RACE=1 RUN='^TestQuerySearch(Parent|Remap|Cancellation)$'`.
The affected installed units are
`make check RUN='^TestQueryCycle(Equality|Parent|Ownership|Lifecycle|Signature)$'`.
Public ordering, prepared/native persistence, atomicity and cancellation witnesses
remain the dual-mode definitions documented in `reference/query_core/README.md`.
