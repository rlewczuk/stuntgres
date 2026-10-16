# reference/cursors

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run commands from the repository root.
This package owns the existing SQL cursor and `pg_cursors` witnesses and delegates
setup to the shared `reference/fixture`. Wire portal/cursor collision witnesses
remain in [sessions](../sessions/README.md#phase-2-server-selected-proof).
Prepared-transaction boundaries remain in
[locking_transactions](../locking_transactions/README.md#testing); combined
selectors below preserve that adjacent coverage without relocating it.

### Holdable cursor verification

The untagged `TestReferenceSQLCursorHold` definitions share the existing session
fixtures and independent row/type/count/full-diagnostic assertions. Select bounded
groups (external900-second deadline per invocation):

```sh
make check REFERENCE=1 RUN='^TestReferenceSQLCursorHold(CommitRemaining|AutocommitAndPrepared|RollbackAndSavepoints|Materialization|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceSQLCursorHold(FailureAndPromotion|ScopeAndPresentation|Restrictions|NestedRowLocks|PrepareBoundaries)$'
make check REFERENCE=1 RUN='^TestReferenceSQLCursorHold(Cancellation|SessionAndControls)$'
make check REFERENCE=1 RUN='^TestReferencePreparedTransactionSessionBoundaries$'
make check REFERENCE=1 RUN='^TestReferenceSQLCursorHold(Recursive)?CatalogChanges$'
```

Repeat the same selectors without `REFERENCE=1` for selected native proof. Both
modes are required for changed reference definitions. No discovery filter remains.
Nested/stored-view rowmarks are admitted; top-level rowmarks retain the exact
read-only restriction. Successful portal exports survive later portal
materialization failure and final SSI publication failure while private writes
roll back. Partial-promotion assertions use independent sequence drain witnesses
and exact surviving rows, never PostgreSQL hash traversal order. Fixed-name
rotations must witness at least one actually drained candidate before failure.
The division target uses the prerequisite DeclarationTiming late outer projection
over an `ORDER BY i OFFSET 0` source, so a bounded first FETCH succeeds and the
remaining fault is genuinely demanded by COMMIT, not by a blocking target sort.
Scope assertions bracket first-demand timestamps, retain prior effective FETCH
values, preserve FETCH0 as nondemanding, and check current stored-FETCH rendering.
Catalog-change definitions independently capture each backend's own relation and
table-rowtype OIDs, then require renamed symbolic and dropped numeric output.
Scalar and recursive array/domain/composite values remain typed through commit;
unread earlier FETCH batches retain their original/renamed spelling despite peer
DDL. Removing the original query source cannot replay or rebind the stored output.
Mixed scope batches use the existing REGTYPE raw transport assertion (native
string versus PostgreSQL nonnil `[]byte`); other columns keep generic validation.
PREPARE assertions distinguish live/removed/failed/old HOLD entries; live HOLD
wins TEMP and empty-GID restrictions. The shared commit-warning case separately
asserts quiet Prepare/DECLARE, current COMMIT warning policy and no later
stored-FETCH evaluation warnings.

Private mechanism and selected race proof (external900 seconds each):

```sh
make check RUN='^TestSQLCursorHold(Grammar|Owner|Demand|Authority|Publication|Cancellation)$'
make check RACE=1 RUN='^TestSQLCursorHold(Owner|Authority|Publication|Cancellation)$'
```

The private probes use real installed producers, projection stages, SQL and Go
commit paths and canonical SSI publication. They assert original transaction
tickets/current unfinished commit scope with owner/engine/session mutexes unlocked;
zero retained producer/authority/source pins after export; unreplayed remainder
and zero-width cardinality; cancellation after actual projection admission and an
actual sequence lock wait, bounded joins, no private write publication and
retired timeout/ticket authority. Implicit HOLD installation is not completion,
while cancellation after successful commit cannot retract exported values/writes.

### Forward-only SQL cursor verification

Run the entire cursor family after changing an oracle expectation: a driver panic
can truncate later tests, so a focused passing run does not establish family
coverage. Each invocation has a separate 900-second deadline:

```sh
make check REFERENCE=1 RUN='^TestReferenceSQLCursor'
make check RUN='^TestReferenceSQLCursor'
make check REFERENCE=1 RUN='^TestReferenceSQLCursorFeatureSmoke$'
make check RUN='^TestReferenceSQLCursorFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceSQLCursorRegtypeWarnings$'
make check RUN='^TestReferenceSQLCursorRegtypeWarnings$'
make check REFERENCE=1 RUN='^TestReferenceRegtypeWarnings$'
make check RUN='^TestReferenceRegtypeWarnings$'
make check RUN='^TestSQLCursor(CompletedPublicationBoundary|CancellationClearsAuthority)$'
make check RUN='^TestSQLCursor'
make check RUN='^TestSQLCursor'
```

The permanent definitions have no discovery backend filter. Native-only controls
and explicit backend capability phases remain independently asserted. Cursor
assertions distinguish blocking volatile view sorts from demand-lazy outer
projections, logical lock-error context
from PostgreSQL's unstable physical tuple coordinates, and first-effective-FETCH
stable expression values from current lock/cancellation authority.
The role/GRANT transitions and application-name `current_setting` probe are
PostgreSQL-only capability evidence. The native scope witness retains its original
owner identity, uses an explicit literal application marker, and exercises
already-supported timestamp and regclass providers rather than enabling adjacent
role/settings features. Its regclass output changes from `cursor_rows` to
`public.cursor_rows` after changing the search path; its first-demand statement
timestamp remains equal on the second FETCH.
The empty-table fixture is created before BEGIN on both backends; cursor coverage
does not enable unrelated permanent CREATE TABLE inside explicit transactions.
Internal units cover detached ownership/pin release, exact-limit/no-lookahead
demand, zero-width/zero-position states, savepoint ordinals/stale transaction
references, nested view/later UNION authority, admitted cancellation/no partial
batch and quiet description/counter exhaustion.
Nested view/later UNION units require exact returned `(i,nextval)` values and
sequence state at DECLARE, each FETCH, EOF and CLOSE. Their consumer probes observe
the current FETCH context and unfinished operation, not producer readiness or
stored occurrence counts. The later UNION arm is first demanded by the second
FETCH; its common-type consumer expression is the evaluated target.

`TestSQLCursorCompletedPublicationBoundary` cancels synchronously after Execute
success but before Finish for MOVE, FETCH, DECLARE, CLOSE and CLOSE ALL. MOVE must
publish its affected count with a genuinely nil RowResult interface, not a typed
nil detached receiver: Query MOVE must expose empty metadata without invoking
detached-row methods. `TestReferenceSQLCursorMoveCountsAndPreparation` covers
that public direct/prepared Query boundary in both modes. Expect success
from repeated Finish, an active transaction, preserved install/removal and detached
row ownership. After rollback to a savepoint, the older cursor next returns row2
after its delivered row1, never replaying or skipping it; CLOSE is not resurrected.
`TestSQLCursorCancellationClearsAuthority` separately retains mid-demand failure,
no partial result, cleared admission and recovered55000 `portal "c" cannot be run`.
The cursor-only completion classification must leave noncursor private writes,
catalog and projected-root publication intact.

`TestReferenceSQLCursorRegtypeWarnings` uses four table TEXT `timestamp(7)` values,
not a planning-time literal cast, with alternating held/direct FETCH executions.
Prepare and DECLARE are quiet. Each demanded row returns REGTYPE
`timestamp without time zone`; current warning/error/notice/warning policies yield
one/no/one/one complete WARNING/22023 precision-reduction records, with empty
Position/Context and all other unspecified logical fields empty. Exhaustion retains
full schema and emits no diagnostic; there is no extra projected warning.
Run this definition and ordinary `TestReferenceRegtypeWarnings` in both modes
before review, preserving the existing direct-operation warning contract.

Stored view columns keep their declared native nullable metadata through cursor
view/derived/join consumers; base-table NOT NULL metadata is not substituted.
Analysis diagnostics separately assert the native unknown-function capability
error0A000 `functions are unsupported` and PostgreSQL42883 with its function Hint.
Failed native Restore retains its full42P02 diagnostic at parameter position53.

`TestReferenceSQLCursorAdvisoryScope` changes peer ownership between successive
FETCH batches, independently expecting true then false from the retained advisory
expression. DECLARE is quiet, EOF preserves BOOL metadata, and cursor/transaction
teardown leaves the peer's session hold intact. After changes to shared expression
authority, select the entire existing advisory family and prepared-transaction
boundary scenario alongside every cursor definition, in both modes before review:

```sh
make check RUN='^TestReference(SQLCursor|AdvisorySession|PreparedTransactionSessionBoundaries$)'
make check REFERENCE=1 RUN='^TestReference(SQLCursor|AdvisorySession|PreparedTransactionSessionBoundaries$)'
```

Each command has its own900-second deadline. The prepared-transaction boundary
scenario asserts the actual WITH HOLD/PREPARE restriction on both backends:
0A000 `cannot PREPARE a transaction that has created a cursor WITH HOLD`.
Its TEMP/export boundaries remain unchanged; session messages now execute and
prohibit PREPARE when transaction actions/events are retained. Hash and join logical-stage fixtures keep real
admissions alive until stage disposal, then finish the scope before
operation/session cleanup. The selected-private-roots join helper completes eager
finalization before releasing its statement and operation; detached output remains
usable after transaction rollback. These fixtures never rely on finished scope
authority.

`TestReferenceSQLCursorPreparedMetadata` checks absent and stale prepared FETCH
descriptions separately from a fresh direct FETCH's runtime schema. Its safe
INT4-to-INT4 alias rebind consumes actual held-statement rows: PostgreSQL advertises
the old alias but returns the new value, whereas native describes the live portal.
Native absent/rebound rows likewise use the live schema, not lib/pq wire quirks. The pinned
lib/pq transport retains its old binary decoder after a same-name INT4-to-TEXT
cursor rebound: iterating TEXT `new` through that decoder panics. The stale
description witness therefore inspects Query metadata without calling Next, closes
the returned Rows, and independently checks fresh direct runtime rows. It does not
recover the panic, promise decoded rebound-row parity, or change the production
driver to imitate this lib/pq limitation.



### Cursor motion selected proof

SCROLL and shared FETCH/MOVE positioning have independent, pinned PostgreSQL
18.0 expectations. The oracle-first discovery round establishes complete ordered
sequence traces for late/derived/view projection, ORDER caches, and volatile
WHERE/VALUES rewind; native results must never supply expected values.
All selected groups below require both `make check RUN='SELECTOR'` and
`make check REFERENCE=1 RUN='SELECTOR'`, with an external 900-second deadline.
The same completed definitions run unchanged in both modes; no discovery filter
remains. Native precancel continuation is an explicit native-only public-control
supplement because pq may asynchronously retire its physical socket.

| Group | Exact selector |
|---|---|
| Directions/boundaries/metadata | `^TestReferenceSQLCursor(ScrollDirections|ScrollBoundaries|ScrollMetadataAndOccurrences)$` |
| Counts/projection/smoke | `^TestReferenceSQLCursor(MoveCountsAndPreparation|MotionProjectionEffects|MoveFeatureSmoke)$` |
| Grammar/no-scroll/analysis | `^TestReferenceSQLCursor(NoScrollMotionExceptions|MotionSyntaxAndDiagnostics|ZeroAndDirections|AnalysisAndDiagnostics)$` |
| Lifetime/current-of | `^TestReferenceSQLCursor(ScrollLifecycleAndAuthority|ScrollCurrentOf)$` |
| Prepared preservation | `^TestReferenceSQLCursor(PreparedUtilities|PreparedMetadata|EmptyAndMetadata|RegtypeWarnings|FeatureSmoke)$` |
| Demand/output preservation | `^TestReferenceSQLCursor(OrderedBatches|DeclarationTiming|Snapshots|QueryConsumers|OutputCapture|Parameters)$` |
| Authority/locking preservation | `^TestReferenceSQLCursor(NestedDemand|ScopePhases|CorrelatedFailure|AdvisoryScope|LockingLatestRow|SecondFetchLockTimeout)$` |
| Lifecycle preservation | `^TestReferenceSQLCursor(Savepoints|TransactionEnds|CloseAndSession|Cancellation|NativeControls)$` |
| Positioned mutation preservation | `^TestReference(DeleteCurrentOf(PositionedRow|DuplicateIdentityAndLastFetch|EligibilityAndDiagnostics|RollbackAndRepeat|FeatureSmoke)|UpdateCurrentOf(PositionedRowAndCounts|IdentityAndPreparation|EligibilityAndDiagnostics|LifetimeAndRollback|FeatureSmoke))$` |

Selected system/frame identity smoke:
`make check RUN='^TestReferenceSQLCursorScrollSystemIdentity$'` and
`make check REFERENCE=1 RUN='^TestReferenceSQLCursorScrollSystemIdentity$'`.
The supported contract is generated-query frame identity, not a new physical
`ctid` provider. Integrated virtual `n GENERATED ALWAYS AS(i+1)` and compound
`n+1` projection have independently predetermined values `3,4,5` for `i=1,2,3`;
native cursor output never supplies those expectations. The first case composes
the existing LEFT JOIN with generated right rows present only for `i=1,2`:
compound outputs are `3,4,NULL`. NEXT, ABSOLUTE2, then PRIOR selects present
`i=1` while a stale next-demand frame would identify absent `i=3`. This is an
independent non-EOF identity assertion, not a claimed observed native mismatch.
The all-present `3,4,5` case follows. Direct/prepared traces cover reverse,
absolute/relative, exact-last and after-EOF motion with meaningful metadata.
Completed definitions run unfiltered in both modes. A native external-deadline
expiry is failure evidence, not proof of a particular value mismatch or panic.

Preserve upstream public identity coverage in both modes with
`RUN='^TestReferenceGeneratedVirtual(BoundedProjection|Frames|WindowIdentity|TableOID|Composition)$'`
and private native coverage with
`make check RUN='^TestGeneratedVirtual(ScheduledProjectionIdentity|RetainedViewProjectionIdentity|WindowInputIdentityAdapter)$'`.

Selected-ordinal projection binds its generated frame to the same physical
index as the selected row and cached order values, independently of the
sequential next-demand frontier. Preserve both virtual-read and motion-retention
flags. Private proof:
`make check RUN='^TestGeneratedVirtualSelectedOrdinalIdentity$'` and
`make check RUN='^TestSQLCursorScrollVirtualFrameIdentity$'`.
The first uses real occurrence provenance and a permutation to check present
and absent selections with a divergent frontier, including a frontier at EOF;
it also asserts no recipe demand for a missing occurrence, no input mutation
and no sequential-position change. The second exercises the actual retained SQL
cursor through reverse/absolute/relative/EOF and CLOSE without public adapter
socket teardown. Neither suppresses a panic.

Integrated HOLD shared-seam preservation requires these additional small groups
in both native and real pinned modes, with the same 900-second external deadlines:

* H1: `^TestReferenceSQLCursorHold(CommitRemaining|AutocommitAndPrepared|RollbackAndSavepoints|Materialization|FeatureSmoke)$`
* H2: `^TestReferenceSQLCursorHold(FailureAndPromotion|ScopeAndPresentation|CatalogChanges|RecursiveCatalogChanges|Restrictions|NestedRowLocks|PrepareBoundaries)$`
* H3: `^TestReferenceSQLCursorHold(Cancellation|SessionAndControls)$`
* H4: `^TestReference(SQLCursorZeroAndDirections|SQLCursorAnalysisAndDiagnostics|SQLCursorTransactionEnds|PreparedTransactionSessionBoundaries)$`
* Additional positioned preservation: `^TestReferenceDeleteCurrentOfUsingReturningAndConstraints$`

Combined SCROLL/HOLD capture and saved-position witnesses are
`^TestReferenceSQLCursorScrollHold(CommitPosition|CapturedValues)$`.
Their independent release-source expectations require an oracle-first checkpoint
before production composition, followed by the same complete definitions in both
modes without discovery gates. No prior cursor motion golden is replaced by these
interaction witnesses.
The combined witnesses assert MOVE's empty schema and affected counts separately,
complete EOF descriptors and closed MOVE34000, captured RELATIVE1 values, and
sequence stability at every stored operation including before-first/after-last.
Computed NEXTVAL metadata uses the documented unknown-nullability contract, not
the catalog-column helper assumption or a native-derived value/type golden.
Supplementary witnesses require pinned oracle proof before native/final proof.

Stored-view partial-capture witnesses use
`^TestReferenceSQLCursor(StoredViewFrontier|ScrollHoldStoredViewFrontier)$`
in both modes. Their stored definition is `SELECT i FROM cursor_rows ORDER BY i`,
with no outer sort: partial MOVE then rewind must drain exactly `[1,2,3]`.
The SCROLL HOLD variant commits after partial MOVE, preserves the saved position,
and checks all three stored occurrences, reverse/absolute/relative seeks and
MOVE counts. Its late NEXTVAL target draws 10 before commit and 11..13 during
full capture; stored motion preserves those values and sequence state.
The private `TestSQLCursorStoredViewFrontier` additionally inspects the retained
child's next-uncaptured position, captured prefix, full-drain cardinality and
producer release. A position-only rewind preserves that frontier; only actual
child invalidation clears the view's values, selection, completion and lineage.
Selected private command: `make check RUN='^TestSQLCursorStoredViewFrontier$'`.

Private parser/engine supplement: `make check RUN='^TestSQLCursor'`.
Shared public smoke: `make check RUN='^TestReferenceSQLCursorMoveFeatureSmoke$'`
and the identical command with `REFERENCE=1`. It observes SCROLL EOF/reverse,
absolute/relative traversal, quiet MOVE counts, held prepared movement, detached
rows and closed-name 34000. Whole-project Docker-free verification remains
`make quickcheck`, after edits finish; final integration verification remains the
root's `make finalcheck`, not a replacement for selected dual-mode evidence.

### Session cursor metadata selected proof

The untagged `TestReferencePGCursors` definitions use the shared public fixture
and independently specified metadata, rows, counts and full diagnostics. Select
small groups from the repository root, each with an external900-second deadline:

```sh
make check REFERENCE=1 RUN='^TestReferencePGCursors(Metadata|SourcesAndTime)$'
make check REFERENCE=1 RUN='^TestReferencePGCursors(OwnerAndMotion|Lifecycle)$'
make check REFERENCE=1 RUN='^TestReferencePGCursors(SessionAndControls|Diagnostics|FeatureSmoke)$'
```

Repeat these same selectors without `REFERENCE=1` for native proof. Every changed
definition requires both modes. Filter assertions to scenario-owned names:
PostgreSQL's observer/protocol portals are not native SQL DECLARE cursors.
Omitted SCROLL's plan-dependent PostgreSQL behavior and native forward-only
limitation are asserted separately; explicit SCROLL/NO SCROLL shares the contract.

```sh
make check RUN='^TestSQLCursor(DeclarationSource|MetadataInstall)$'
make check RUN='^TestPGCursors(Source|Catalog)$'
make check RACE=1 RUN='^TestReferencePGCursors(OwnerAndMotion|SessionAndControls|FeatureSmoke)$'
make check RUN='^TestReferencePGCursorsFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGCursorsFeatureSmoke$'
```

The smoke observes exact full declaration text/options/nonzero bracketed timestamp
only in the owning physical session. FETCH returns row1, MOVE skips row2, and
NEXT returns row3 while metadata remains unchanged. CLOSE removes the row; HOLD
creating COMMIT preserves its tuple and saved position; owner DISCARD ALL clears
metadata without clearing the independently held peer cursor.
`TestReferencePGCursorsOwnerAndMotion/role_and_definer` keeps independent same-name
HOLD cursors on two physical pins outside explicit transactions. Their complete
direct and stored-view metadata tuples/instants remain unchanged as the sessions
swap effective actor/definer roles and RESET ROLE. The ordinary view is explicitly
owned by the definer, with SELECT granted to the actor; the peer never sees the
owner-only cursor. Role/view setup precedes cursor installation, and cleanup resets
roles/closes cursors before fixture schema teardown and role removal.
The view is fully qualified. PostgreSQL alone receives explicit public-schema
USAGE grants to restore visibility removed by fixture schema recreation; native
public already grants USAGE, so this does not claim native schema-GRANT support.
Actual role assertions use the NAME-typed current_user result without a cast.
Native-only phases check detached metadata, ordinary precancellation and Dump
exclusion. After Reset/Restore, fresh metadata retains the tuple/instant while
actual FETCH remains fenced with ErrCatalogReplaced. Pool return is not physical
cleanup.
Shared catalog SQL checks pg_class view kind and six attributes using the builtin
pg_catalog namespace OID11. Native pg_namespace, pg_type and pg_class.reltype
remain unavailable; no adjacent catalog feature is required. Native row/array
ownership and reciprocal element associations are checked through internal
catalog APIs; an additional PostgreSQL-only catalog phase independently checks
the real row/array relationships.
Private units supplement public routes with controlled declaration/transaction/
first-demand clocks, producer teardown, owned descriptor validation, builtin
timestamp descriptor reownership without datum/registry mutation, and canceled
materialization with no partial publication. Parser generation and whole-project
`make quickcheck` remain root-owned after edits; selected proof is still required.

### Shared cancellation admission witness

`fixture.SQLCursorCancelDemand` owns the existing PostgreSQL admission barrier,
server cancellation, bounded worker join, partial-row exclusion and complete
57014 diagnostic assertion shared by cursor FETCH, HOLD COMMIT and SQL prepared
write cancellation. The retained prepared witness stays in `reference/sessions`.
Select all three callers in both modes after changing this helper (external
900-second deadline per invocation):

```sh
make check RUN='^TestReference(SQLCursorCancellation|SQLCursorHoldCancellation|SQLPreparedCanceledWrite)$'
make check RUN='^TestReference(SQLCursorCancellation|SQLCursorHoldCancellation|SQLPreparedCanceledWrite)$' REFERENCE=1
```
