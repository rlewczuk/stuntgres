# reference/views

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### Materialized view creation and refresh

Materialized-view coverage uses the same public dual-mode scenarios:

```sh
make check RUN='^TestReferenceMaterializedView'
make check REFERENCE=1 RUN='^TestReferenceMaterializedView'
make check RUN='^TestReferenceMaterializedViewFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceMaterializedViewFeatureSmoke$'
make check RUN='^TestMaterializedView'
```

Each selected check has a 900-second deadline. Independently specified bags retain
duplicate/NULL rows after base changes, distinguish empty/populated/no-data
states, and assert complete55000/42809/42501/25006 diagnostics, real cursor
metadata, dependency DROP, source/drop lock timeouts and atomic failure.
RestrictedSearchPath uses supported CURRENT_SCHEMA: population sees pg_catalog
while binding the original public source; SHOW and subsequent CURRENT_SCHEMA
still see public. Changed goldens require pinned oracle evidence, not native output.
Native controls assert exact stale rows after two cycles/fresh Driver/second pool,
including RestoreTemporary collisions. PostgreSQL replay of the actual emitted
script asserts definitions and unpopulated55000, never stale-row parity.
Native precanceled CREATE guarantees no publication; lib/pq may complete a fast
CREATE despite an already-canceled context. Shared lock-timeout cases retain
portable atomicity assertions. Private units cover real descriptor/staging,
frozen identities, strict codec/import and opt-in lexical comment boundaries.
Review-remedy regressions use
`RUN='^TestReferenceMaterializedView(KeywordTypes|SnapshotTypmods)$'` with both
`make check` and
`make check REFERENCE=1` (900 seconds each).
They preserve unquoted `materialized` type/schema legality and reject native
lossy snapshot coercions atomically beside genuine-Dump positives. NUMERIC
assertions use exact decimal driver transport, not numeric-to-text SQL casts.
KeywordTypes independently asserts native DROP TYPE's existing0A000 utility
exclusion; fixture reset and supported DROP SCHEMA CASCADE clean up native enum
types. Declaration, unquoted type references and cast legality remain shared.
Recursive/specialized snapshot scenarios use
`RUN='^TestReferenceMaterializedView(RecursiveSnapshots|TemporalSnapshots)$'`
with both `make check` and
`make check REFERENCE=1` (900 seconds each).
Independently expected raw domain/enum/constrained-array/composite values and
metadata precede native genuine-Dump restoration, reminted identities, retained
MV cursors and atomic malformed recursive payload rejection. Temporal coverage
includes both infinities and converted TIMETZ int32-displacement endpoints; the
endpoint's TEXT representation is intentional because pq cannot construct its
raw invalid-Location time.Time. Normal SQL expectations are shared; no PostgreSQL
Restore API or native stored-row parity from PostgreSQL script replay is invented.

Nonconcurrent refresh uses the shared `TestReferenceMaterializedRefresh` family:

```sh
make check RUN='^TestReferenceMaterializedRefresh'
make check REFERENCE=1 RUN='^TestReferenceMaterializedRefresh'
make check RUN='^TestMaterializedRefresh'
make check RACE=1 RUN='^TestMaterializedRefresh|^TestReferenceMaterializedRefresh(Locks|Cancellation|QueuedReplacement|Snapshots|Controls)$'
make check RUN='^TestReferenceMaterializedRefreshFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceMaterializedRefreshFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceMaterializedRefreshQueuedReplacement/(data_data|no_data_data)/^postgres$'
```

Each check has an external900-second deadline. Independent bags retain
duplicate/NULL rows, default/DATA refresh returns affected0, and NO DATA is
unpopulated rather than populated-empty. Full producer/kind/ACL/read-only/lock
diagnostics, actual protocol Prepare/held statements, catalog identities and
typmod metadata are checked before backend comparison.
Public rowtype identity is observed through the existing REGTYPE registry;
`pg_class.reltype` and reciprocal `pg_type.typrelid` links are PG-only
complements. Native publication units independently pin the exact rowtype link.
Owner-context sequence witnesses use existing ALTER SEQUENCE OWNER transitions,
not excluded direct sequence grants. The caller's ownership cannot rescue a
denied MV owner; source/sequence owner context and caller settings/SESSION_USER
are checked independently.
Native explicit utility0A000/aborted25P02, eager retained Rows, original context
cancellation, genuine Dump/Reset/Restore and fresh-driver stale rows remain
explicit native phases. PostgreSQL independently asserts transactional
refresh/rollback/commit, active-portal55006 and actual backend-cancel57014.
Native active cancellation observes the actual public worker's target-request
wait through runtime goroutine profiles, following the existing active-provider
witness pattern. Package units additionally pin the lock-manager pending
request and queued refresh-refresh handoff. No SQL LOCK-on-MV capability,
elapsed-time admission assumption or merely-started signal is introduced.
QueuedReplacement starts an actual direct first worker behind a retained reader,
observes its target AX request, then starts an actual protocol-prepared second
worker and observes its later AX request. PG asserts the second worker's first
worker soft blocker; native profiles identify each distinct worker's real target
wait. DATA→DATA and NO DATA→DATA both join with affected0, the exact duplicate/NULL
four-row bag, populatedtrue and stable relation/REGTYPE/rewrite identities,
owner and definition. Private units additionally observe the first publication
and grant-time predecessor for both transitions, and reject owner, column,
attribute, rowtype, definition, identity and epoch changes while truly queued.
Workers always have bounded cancel/release/join cleanup. The PG-only command
above discovers this new contract without modifying the shared definitions.
Completed real mode consumes the actual native emitted dump SQL, asserts
unpopulated definitions, then explicitly refreshes replayed base rows.
The selected FeatureSmoke composes these contract scenarios. A separate
root-owned standalone public Go source in the OMP session is temporarily
materialized as `./.materialized-refresh-smoke/main.go`, run with
`go run ./.materialized-refresh-smoke` (external900-second deadline), then removed.
Neither selected tests nor oracle-only discovery replace this executable proof.

Concurrent refresh uses the shared `TestReferenceMaterializedRefreshConcurrent`
families (Lifecycle, Prepared, Metadata, Eligibility, Diagnostics, Duplicates,
Atomicity, Readers, Snapshots, Queue, Cancellation, Controls, Transactions and
FeatureSmoke), plus the affected Boundaries/FeatureSmoke callers:

```sh
make check RUN='^TestReferenceMaterializedRefreshConcurrent|^TestReferenceMaterializedRefresh(Boundaries|FeatureSmoke)|^TestReferenceMaterializedAlterIndexRefreshAtomicity'
make check REFERENCE=1 RUN='^TestReferenceMaterializedRefreshConcurrent|^TestReferenceMaterializedRefresh(Boundaries|FeatureSmoke)|^TestReferenceMaterializedAlterIndexRefreshAtomicity'
make check RUN='^TestMaterializedRefreshConcurrent'
make check RACE=1 RUN='^TestMaterializedRefreshConcurrent|^TestReferenceMaterializedRefreshConcurrent(Readers|Snapshots|Queue|Cancellation|Retained|SourceMaterialized)'
make check RUN='^TestReferenceMaterializedRefreshConcurrentFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceMaterializedRefreshConcurrentFeatureSmoke$'
```

Each invocation has an external900-second deadline. Independent assertions cover
direct/held prepared affected0, exact bags/raw cells/metadata, current eligibility
at Execute, populated/options precedence, same-lifetime catalog identities and
genuine native stale-row/index Dump/Reset/Restore/fresh-driver replay.
NULL/image cases distinguish the raw producer bag from the effective diff:
old image matches survive once and suppress all matching new occurrences;
unmatched new occurrences retain multiplicity. All eligible index keys supply
non-NULL SQL equality quals; INCLUDE payloads do not. Numeric scale/signed-zero
images remain distinct. Complete non-NULL duplicates fail21000 before runtime
key conflicts23505, including owner-disclosed Detail under MAINTAIN-only callers.
The local runtime diagnostic helper validates PostgreSQL's exact generated SPI
INSERT Context shape before normalizing only that physical, non-engine field;
InternalQuery/InternalPosition and every logical field remain exact. Queued plain
target-lock timeout independently asserts Position17 on both backends.
Reader/queue/cancellation scenarios observe actual baseAS or targetEXCLUSIVE
waits: PG uses exact OID/PID/mode/blocker; native uses bounded real worker frames.
Readers pass EXCLUSIVE and queued EXCLUSIVE, not queued AX. RR holds old roots
across two peer publications, including a snapshot established before first MV
demand. Private units pin deterministic admission/publish/capture interleavings,
immutable buffers/frontier/end roots, narrow lineage and negative authority/
ordinary-incarnation fences. Workers always cancel/release/join on failure.
SourceMaterialized independently pins an outer refresh's old active source
snapshot while another base-source lock is awaited and its inner MV concurrently
publishes new rows; the next fresh outer refresh sees those new rows. All physical
sources share one capture/frontier. Source-only retained fences accept only exact
defining authority and uninterrupted concurrent lineage; target/index/provider
original fences are never relaxed. The sessions package's Retained and
RetainedCancellation families exercise the existing eager multi-command owner.
Standalone feature source is supplied through the OMP session and temporarily
materialized at `./.materialized-refresh-concurrent-smoke/main.go`; the root runs
`go run ./.materialized-refresh-concurrent-smoke` (external900-second deadline),
records checked observations, then removes the materialized source.


### Materialized alteration and attachments — change note

The untagged shared `TestReferenceMaterializedAlter` definitions specify
independent expectations for identity/definition/population/metadata,
authorization and owner context, full diagnostic precedence, grammar and held
Prepare/message boundaries, genuine indexes and refresh atomicity, extension
edges/retirement, controls, real lock waits/cancellation and feature smoke.

```sh
make check RUN='^TestReferenceMaterializedAlter'
make check REFERENCE=1 RUN='^TestReferenceMaterializedAlter'
make check RUN='^TestMaterializedAlter'
make check RACE=1 RUN='^TestMaterializedAlter|^TestReferenceMaterializedAlter(Locks|Cancellation)$'
make check RUN='^TestReferenceMaterializedAlterFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceMaterializedAlterFeatureSmoke$'
```

Each invocation has an external 900-second deadline. Changed shared definitions
require completed native and real pinned modes before review. Shared witnesses
use stable regclass/regtype/rewrite identities, pg_depend, summaries and
pg_get_indexdef. pg_attribute labels/attnums are PG-only complements; there is
no native pg_index-provider expansion. Package units use real registered
unaccent descriptors and package stages, genuine bound indexes/roots,
publication fences, provider-root retirement in both modes, merged x/normal
dependency traversal, queued grant-time owner authority and immutable remapping.
No fake provider, manufactured positive edge or index is used.
Numeric OIDs may repeat across Reset/Restore or fresh drivers. Each cycle proves
x references its current genuine provider, not an allocator-specific inequality.
ControlRemap forces a real cross-class imported-extension/retained-TEMP namespace
collision, inspects the actual displaced provider, x edge, rows and index roots,
then retires that provider while preserving the genuine TEMP and source snapshots.

The root materializes `local://materialized-alter-smoke.go` as an ephemeral
`.materialized-alter-smoke/main.go` and runs
`timeout 900s go run ./.materialized-alter-smoke` from the worktree. Quiet exit0
requires typed duplicate/NULL stale rows, metadata changes, genuine index and
extension edges, owner/schema coherence, repeated/fresh-driver control replay,
retained Rows, detach/provider retirement and subsequent refreshed rows.
Selected contract smokes do not replace this public executable proof.

### Automatically updatable view witnesses

The `TestReferenceViewUpdatable` families exercise logical view targets through
public direct and retained prepared consumers: base defaults and counts,
visibility/filter escape, existing DML caller variants, restricted shapes and
full diagnostics, current owner/invoker authority, atomicity, transactions,
contention, replacement and native-control cycles. Their independently specified
expectations must be observed on the pinned oracle before production changes.
Select the small families separately in both modes (900 seconds per command):

```sh
make check RUN='^TestReferenceViewUpdatable(Rows|Visibility|Callers|Obligations)$' REFERENCE=1
make check RUN='^TestReferenceViewUpdatable(Restrictions|Diagnostics|Discovery)$' REFERENCE=1
make check RUN='^TestReferenceViewUpdatable(Security|InsertSecurity|WriteSecurity)$' REFERENCE=1
make check RUN='^TestReferenceViewUpdatable(Atomicity|Transactions|Contention)$' REFERENCE=1
make check RUN='^TestReferenceViewUpdatable(Lifecycle|Smoke|Replay)$' REFERENCE=1
make check RUN='^TestViewWrite(Mapping|Defaults|Demand|QueuedRecheck|Cancellation|Replacement)$' RACE=1
make check RUN='^TestReferenceViewWriteCapabilityBoundary$' REFERENCE=1
```

Repeat those exact selectors without `REFERENCE=1` for native proof. Initial
oracle discovery may temporarily filter non-PG backends under the policy below;
remove every discovery filter before native red and final dual-mode verification.
`Discovery` resolves no-op casts/COLLATE, stored row marks and unused constant
expression demand using full diagnostic mismatch evidence, not native goldens.
Native Reset/Dump/Restore phases do not invent server control APIs. PostgreSQL
tuple contexts are structurally validated, not assigned native physical values.
Supplemental native manager/stage units accompany the production round.
`TestViewWriteDefaults` also checks physical conflict-arbiter selection without
changing the logical target namespace; `TestViewWriteDemand` retains typed
parameter identity and imported values through the shared expression rewrite.
`TestViewWriteMapping` admits source-alias wrappers emitted by native Dump replay
without admitting aliases around derived sources; public Smoke and the actual-API
Go smoke retain their post-Restore future-write expectations unchanged.
The independent-review regressions pin narrow-view INSERT conversion22003,
independent stored-subquery SELECT42501 under owner/invoker grant changes, and
live subquery counts after unrelated source-layout changes and own update/delete
overlays. Run the complete definitions in both modes:

```sh
make check RUN='^TestReferenceViewUpdatableReview(Conversion|SubqueryACL|SubqueryVisibility)$'
make check RUN='^TestReferenceViewUpdatableReview(Conversion|SubqueryACL|SubqueryVisibility)$' REFERENCE=1
```

`TestViewWriteDemand` additionally captures a real physical prefix with a
committed deletion, selects its live rows, then bridges reordered frozen
attributes and closes the source before checking manager cleanup.
These mechanics supplement the public Callers/Security/Lifecycle/Smoke/Replay
regressions; they do not replace their required native and real-mode proof.
WITH, TABLESAMPLE and general function FROM/target SRFs remain outside those
writable-view witnesses. Selected `string_to_table` read views now have their
own executable Stored55000 diagnostics, not parser-error substitutes.
The separate actual-API Go smoke source is an ephemeral OMP session artifact;
root materializes, runs and removes it rather than committing a smoke executable.

### View check-option witnesses

`TestReferenceViewCheck` covers independent direct and genuinely retained prepared
NEW-row behavior, the full 27-mode nested truth table, default/generated physical
mapping, logical RETURNING metadata, atomic producer failures, correlated/live/own
predicate sources, actor disclosure/ACL freshness, transaction/savepoint/cancel
boundaries, rule/trigger oracle semantics, and native persistent mode round trips.
The tests were authored before implementation; exact diagnostics and operation
precedence are observed against the pinned PostgreSQL18.0 oracle, not native
goldens. Functional definitions are shared; PostgreSQL provider and native
control-only phases explicitly identify mechanisms unavailable in the other mode.

Run these small selectors with an external900-second deadline in **both** modes:

```sh
make check RUN='^TestReferenceViewCheck(Grammar|Declaration|Options|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferenceViewCheck(Basic|Nested|NewRows)$' REFERENCE=1
make check RUN='^TestReferenceViewCheck(Atomicity|Sources|Security|ConflictUpdate)$' REFERENCE=1
make check RUN='^TestReferenceViewCheck(Transactions|Cancellation|RuleTriggerBoundary)$' REFERENCE=1
make check RUN='^TestReferenceViewCheck(Lifecycle|Replay|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceViewCheckReview(VirtualNewRows|QuotedNames)$' REFERENCE=1
make check RUN='^TestReferenceViewReplace(CapabilityBoundaries|CapabilityBoundariesConsumerRoutes|Options)$' REFERENCE=1
```

Repeat each exact command without REFERENCE=1 for native proof. Changed existing
ViewUpdatable definitions/callers retain their separately selected dual-mode
regression families above. Discovery filters must be removed before native red
and final dual-mode proof. The additional existing identity-order conflict-update
producer received its own PG-first regression/native-red checkpoint:
`make check RUN='^TestReferenceViewCheckConflictUpdate$'`, also with REFERENCE=1.
It is common-gate coverage, not general view action mapping expansion.
The review-remedy regressions also received a PG-first/native-red checkpoint.
They independently assert virtual generated NEW predicates with retained handles
across live layout changes, independent generated subquery occurrences, and full
44000 diagnostics with literal embedded-quote/backslash view names.

Focused mechanism units (not public SQL substitutes):

```sh
make check RUN='^TestViewCheck(Options|Declaration|Stack|Mapping|Predicate|Admission|Cancellation|Finish|ControlRemap)$'
make check RUN='^TestViewCheck(Cancellation|Finish)$' RACE=1
```

The feature smoke selector is `^TestReferenceViewCheckFeatureSmoke$`, in both modes.
Native lifecycle proof performs two genuine Dump/Reset/Restore cycles and future
held writes, plus invalid suffix/reader/pre-cancel/EOF-cancel vetoes. Replay sends
actual native Dump bytes to PostgreSQL; SELECT-only ViewCopy is not persistence
proof. The separate actual-API smoke is an ephemeral OMP session source: root
materializes `.view-check-public-smoke/main.go`, runs
`timeout 900s go run ./.view-check-public-smoke/main.go`, records observations,
and removes that root-created scaffold rather than committing an executable.

### Temporary views

Temporary-view definitions share physical-owner, notice and direct/prepared
fixtures. They independently assert explicit TEMP/TEMPORARY metadata and dynamic
duplicate/NULL/zero-width rows, relation-only inference throughout admitted query
frames, namespace/shadowing and same-name session isolation, exact complete
diagnostics/notices, real `_RETURN` identity/drop cleanup, dependency closure,
physical exit, and blocked-source cancellation followed by same-handle recovery.
`TestReferenceTemporaryTableCapabilityBoundaries` now asserts shared TEMP-source
view success and its exact inference notice, retaining the SET SCHEMA boundary.
The native control phases independently replay the actual persistent dump into
a fresh engine and prove TEMP-only/unchanged pinned-catalog retention with exact
identities, held reads and intermediate builtin usages. Invalidated persistent
providers, TEMP restore scripts and invalid/canceled imports must leave both
persistent and TEMP projections unchanged. Package-function stages supplement
associated rowtype/array private publication/removal, ON COMMIT DROP closure,
canceled commit, utility-only Describe and bounded retained-provider provenance.
They do not replace public SQL evidence or add pg_type/pg_class.reltype providers.
`TestReferenceTemporaryViewInferencePrecedence` independently combines TEMP
inference with duplicate outputs, forbidden/missing destinations, replacement
targets, options and indeterminate collation. Early SELECT/excess-alias errors
must remain notice-free. Fresh options-before-duplicates and existing-replacement
duplicates-before-options are distinct contracts. Isolation and FeatureSmoke
also perform an actual prepared peer lookup requiring complete42P01/Position15
at Prepare, separately from their retained direct Execute witness.
`TestTemporaryViewRetainedSourceFrontier` exercises genuine session publication,
native replacement and recursive source-frontier capture, while refusing a bare
old-epoch table reference. `TestReferenceTemporaryViewRetainedWrites` independently
asserts existing INSERT/UPDATE/DELETE counts, reordered nested-view rows and
peer isolation in both backends, with native Reset/Restore repetition. Stored
subquery sources retain their defining-layer ACL and frozen-layout obligations.

```sh
make check REFERENCE=1 RUN='^TestReferenceTemporaryView'
make check RUN='^TestReferenceTemporaryView'
make check REFERENCE=1 RUN='^TestReferenceTemporaryTableCapabilityBoundaries$'
make check RUN='^TestReferenceTemporaryTableCapabilityBoundaries$'
make check RUN='^Test(CreateTemporaryView|TemporaryView)'
make check REFERENCE=1 RUN='^TestReferenceTemporaryViewFeatureSmoke$'
make check RUN='^TestReferenceTemporaryViewFeatureSmoke$'
make check RACE=1 RUN='^TestReferenceTemporaryView(Cancellation|Lifetime)$'
make check REFERENCE=1 RUN='^TestReferenceViewUpdatable'
make check RUN='^TestReferenceViewUpdatable'
```

Use external900-second deadlines, and selected existing VIEW/replacement/join/
control regressions for every shared seam touched. Both completed modes are
required before review; neither quickcheck nor finalcheck replaces selected
pinned proof. A standalone public Go smoke may additionally use NewDriver,
physical sql.Conn owners and native Dump/Reset/Restore; test selectors alone
are not standalone runtime smoke evidence.
