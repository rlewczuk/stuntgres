# internal/engine

## Settings ownership

Setting definitions, the builtin table, parsing, validation and the immutable
registry live in [`settings`](settings/README.md). `Engine.settings` holds one
`settings.Registry` by value, constructed before provider registration and
publication with the visibility tracking flag; parameter providers borrow a
pointer to it, and descriptor validation and grant-name canonicalization use
the same registry. ACL catalogs remain sparse. Search-path resolution stays in
`schema_path.go`; runtime notice levels, threshold captures and emission stay
in `notices.go`. Physical-session values, journals, timeout publication and
their existing locks remain engine-owned. The engine, the root driver and
`cmd/stuntgres` name `settings.` types directly; no aliases remain.

## System catalog producers

The rows-only producers, their `Input`/`Runtime` contract, the OID registry,
the descriptor spec data and their pure helpers live in
[`pgcatalog`](pgcatalog/README.md). The engine keeps the orchestration around
them:

- `catalog_source_capture.go`: `openCatalogSourceLocked` looks up
  `pgcatalog.ProducerFor` and chooses per-OID immutable catalog/role roots,
  presentation and unlock timing; it is not a second producer dispatcher.
  `captureCatalogSourceRows` alone wraps producer rows as
  `querySource{ref, anonymous}`; private units use that same wrapper. Only
  `pg_locks` and lazy `pg_sequences` have explicit non-producer handlers.
- `catalog_source_runtime.go`: `engineCatalogRuntime` implements
  `pgcatalog.Runtime` per call and on demand: construction retains handles and
  cheap facts without enumerating unrelated runtime state. Its value-taking
  operations expose neither Engine, StatementScope, AuthorizationView nor
  bindings. Cursors are observed without fetching; settings capture one
  coherent physical-session `row_security` tuple after unlocking; prepared
  rows use the actual registry, assigned XID and currently published owner
  name. Expression/query walkers, deparsers, the foreign-key binder, type and
  ACL validation, output formatters and error-bearing privilege operations
  stay adapter-owned. Physical TEMP ownership, sequence journal overlays,
  cancellation boundaries and foreign disclosure booleans keep their
  semantics.
- `pg_locks_capture.go` keeps the unlock/capture/relock epoch fence and never
  takes the lock manager mutex under `Engine.mu`; `pgcatalog.LockRows` builds
  the rows. Lazy sequence completion alone demands sequence locks and
  generator state.
- `catalog_sources.go`: `initializeCatalogSources`, `catalogSourceDescriptor`
  and `registerCatalogSource` resolve owned types from `pgcatalog.Specs` and
  register fresh descriptors, preserving registration order, OIDs, columns,
  typmods, dimensions, nullability, collation, origins, flags and actual
  row/array links.
- `catalog_pinning.go`: `pgDependPinned`, reached by producers through
  `Runtime.Pinned`.

Provenance is same-pointer authority, not equal names/OIDs/epochs:
`catalogStore.owner()` supplies the stable `*catalog.Owner`, whose Domain,
Principals and ACLAuthority are the engine's exact paired authorities.
Publication/index history lives in `catalog_relation_history.go`, sequence
predicate admission in `query_pg_sequences_admission.go`, and shared
expression/plan reference walking in `expression_references.go`; those are not
producer ownership. Engine code names the moved types directly
(`pgcatalog.Input`, `pgcatalog.Runtime`, `pgcatalog.DependEdge`, ...).

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy and
public/private test placement. Run these commands from the repository root.

### Immutable settings registry

Focused ownership/default and publication checks:

```sh
make check RUN='^Test(Setting(CapturedDefaults|ProviderDefaultsAndDescriptorOwnership|ExactDecimalDefaultCapture|UtilitySnapshotOwnership)|SettingsTimezoneAbbreviationsCapturedProvider)$'
make check RUN='^TestSettingsLocal(NormalizerPublication|ExactTypedUndo)$' RACE=1
make check RUN='^TestSettingsConcurrentFreeze$' RACE=1
make check RUN='^TestReferenceSettings(BuiltinRegistry|PublicTypedSQL|ErrorsAndAtomicity|RegistryPublication|ShowAllPlaceholdersAndLifecycle|PreparedBindingAndStages|ControlsDefaultOnParser)$'
make check RUN='^TestReferenceSettings(BuiltinRegistry|PublicTypedSQL|ErrorsAndAtomicity|RegistryPublication|ShowAllPlaceholdersAndLifecycle|PreparedBindingAndStages|ControlsDefaultOnParser)$' REFERENCE=1
```

The normalizer publication witness installs a validated custom definition before
construction. Its staged callback still performs actual cancellation, native
replacement and acquisition of all three publication locks; no registry mutation
hook is exposed. Existing public reference scenarios are unchanged. Pure
definition tests live in the [`settings` README](settings/README.md#testing);
split evidence lives in
[the split prompt](../../docs/ENGINE-SPLIT-settings.md#status).

### Catalog inquiry and session identity

`functions` owns the ten immutable inquiry/identity nodes, their binders,
registry definitions and node-owned protocols, including all seven visibility
kinds and twelve deparse signatures. Keyword identity keeps its distinct parser
route. `catalog_inquiry_runtime.go` and `session_inquiry_runtime.go` hold the
`statementHost` bodies behind the narrow functions capabilities that
`exec.Context` forwards, not forwarding engine nodes.
The leaf imports no engine/catalog/storage/lock/visibility state packages.

Engine retains SQL-callable precedence, analyzer/coercion authority, statement
catalog/role/comment/visibility snapshots, locks and authorization recapture,
schemaInquiryPathLocked and pending-TEMP staging/publication, schema execution
caches and FK/rule/view expression authority. It also owns address lookup,
catalog index interpretation, post-NULL successful-only lazy routine admission,
deparse/tree engines and serial ownership lookup. pg_extension_update_paths
is not part of the move; sequence calls moved separately (below).

Generic visitors consume node-owned capture, source, demand, cost, grouping,
volatility and rendering hooks; installed/catalog wrappers still win. Stored
literal pairing, logical identities/remapping, prepared invalidation, snapshot
manifests and native catalog-replacement fences stay here. Pure node units moved
to functions; real binder/captured-authority/stored/serial integrations did not.

```sh
make check RUN='^TestCatalogRelations(Binding|Consumers|Capture)$'
make check RUN='^TestNamespaceIdentity(TypedBinding|StagedAuthorityAndFinishVeto|NoPendingFastPath)$'
make check RUN='^TestCatalogInquiryRetainedVolatility$'
make check RUN='^TestVisibilityInquiryStoredNullManifest$'
make check RUN='^TestObjectAddress(RoutineOriginalAuthority|SourceNullOriginalGraph|ScalarNullPreflightInfo|SourceFalseDemand|StoredRecipes)$'
make check RUN='^TestSerialSequence(Ownership|NameInput|Expression)$'
make check RUN='^TestReferenceObjectAddress(Signatures|RoundTrips|Nulls|Metadata|StrictEvaluation|StoredBoundaries)$'
make check RUN='^TestReferenceObjectAddress(Signatures|RoundTrips|Nulls|Metadata|StrictEvaluation|StoredBoundaries)$' REFERENCE=1
make check RUN='^TestReferenceObjectAddress(StoredDemand|StrictConstantNull|StrictConstantNullFrom|TypeArgumentOrder|DiagnosticParameterMatrix)$'
make check RUN='^TestReferenceObjectAddress(StoredDemand|StrictConstantNull|StrictConstantNullFrom|TypeArgumentOrder|DiagnosticParameterMatrix)$' REFERENCE=1
make check RUN='^Test(CatalogRelations(Capture|Cursor)|ReferenceCatalogRelations(Held|Lifecycle))$' RACE=1
make check RUN='^TestReferenceNamespaceIdentity(FeatureSmoke|PendingTemp|Ownership|Retirement)$' RACE=1
```

Use external 900-second deadlines. Complete public selectors remain in the owning
reference READMEs; each affected selection needs native and real pinned
PostgreSQL18.6 UTF8/C proof. After the concurrent split rebase and visibility
manifest remedy, production build, all 70 bounded public native/real commands,
focused lifecycle races, dependencies/import-layer checks and actual public
direct/SAME-held role/path/native replay smoke passed (940 dump bytes).
Full quickcheck passed in 128s and unchanged GOMAXPROCS=8 racecheck in 490s.
An earlier default-CPU race run hit two 30-second fixture deadlines under
contention; it remains failed evidence, not a skipped or weakened test.
Latest default-CPU startup samples were 0.844–1.314ms, with one of five above
the <1ms target and 5152 steady allocations (first sample 5161). The original
5152-allocation baseline's five 1.008–1.704ms samples all missed that target;
shared-machine timings do not establish a speedup.
The wrapped-NULL stored-manifest unit failed before the narrow replay remedy
and passed afterward with identical argv. It preserves unconditional demand for
a retained visibility node, bare-NULL folding, binary-conversion NULL suppression
and sibling demand in both orders, including persisted-manifest validation.
Review, integration and post-integration finalcheck are separate lifecycle gates,
recorded in the catalog part's reports; no neighboring waiver applies.

### F3 provider and diagnostic ownership

Engine callers and declaration inventories use `functions.RoutineProvider` and
its family constants directly; private authority/call-shape fixtures use the
same owners. Cast-diagnostic fixtures consume `functions.SignatureHint`.
These are name/import adaptations, not unit moves or public behavior changes.
The real analyzer's missing-signature note and composite fallback remain here.
See [functions Testing](functions/README.md#testing) for selected private units,
native/real public proof, actual public smoke and detailed startup results.
Latest-base root tests-12 establishes 1291 exact-owner uses, 26 removed
declarations, 28 Go files, 1,065,774 conserved non-target bindings and eight
retained diagnostic-method callers; actual public smoke also passed.
Tests-10/tests-11 describe the preceding base: 1293 uses/30 Go files, build,
all selected native/pinned-PostgreSQL proof, quickcheck (147.74s), full racecheck
(595.62s), dependencies and import graph passed. Latest complete-candidate
gates refresh before review. Three of five preceding-base startup samples
exceeded1ms; the advanced integration
base and shared-host contention prevent a clean causal comparison with the
historical baseline. No runtime optimization or test weakening was added.
Only Item6 is marked verified; independent review/integration remain separate.
Post-integration finalcheck was explicitly waived by the user, not run or passed.

### Advisory and blocking inquiry authority

`functions.Advisory` and `functions.BlockingInquiry` own SQL binding, immutable
node operations and argument demand. Engine `advisory_runtime.go` and
`blocking_runtime.go` implement their narrow value capabilities with compile-time
checks. Resource construction, current physical-session/transaction selection,
admission/wait/readmission, notices, canonical manager reads and SSI safe waits
remain here. Ordinary blocking reads never nest Engine.mu and manager mutexes;
safe-snapshot capture remains under Engine.mu. No prepared node caches an owner.

Run `make check RUN='^Test(AdvisoryBlockingDomainInput|AdvisorySession(BindingTraversal|CheckPreparationCache|CheckPrepareAllBeforeEvaluation)|AdvisoryXactBindingTraversal|BlockingPids(BindingAndIdentity|TraversalReplay))$'`
for actual binder/context/traversal/replay units. Node-only units and the
public selected proof/smoke instructions are in the functions README. Existing
manager ownership, savepoint/prepared transfer, replacement and SSI tests remain
at their canonical seams; moving SQL nodes does not move their authority.

### Sequence call authority

`functions.SequenceCall` and `functions.BindSequenceCall` own the
`nextval`/`currval`/`setval`/`lastval` binding, immutable node operations and
argument demand; `parser.CallExpr` dispatch calls the binder directly. The
engine keeps the authority behind `SequenceRuntime` (`sequence_runtime.go`): the
evaluation prelude and live statement scope, OID-to-reference construction and
`lastval`'s session provider, the catalog epoch fence (including TEMP epoch
revalidation) before and after the RowExclusive `acquireSequence`, ACL and
read-only checks, `sequenceGeneratorLocked` and the physical-session `currval`/`lastval`
memories ([`sequence`](sequence/README.md)), all in the original order. The
binder's `SequenceArgument`/`SequenceTemporary` keep regclass input,
prepared sequence-literal capture and DEFAULT TEMP validation here; the node
caches no session, scope or owner. Catalog-identity consumers keep explicit
`functions.SequenceCall` cases: stored DEFAULT SQL and replay eligibility,
`validateCookedDefaultTemporary`, `analyze.ClosedSequenceDefault`, dependency references,
view `Ref` remapping, COPY's nextval volatility and the identity DEFAULT
constructor in `BindInsertDefaults`.

Run `make check RUN='^Test(SerialStoredDefaultInvariants|SerialDefaultIdentityProviderTemporaryCollision|QueryParallelEligibility|TableLikeTemporary(DefaultAuthority|RetainedCopy)|Inheritance(MergeStableRemap|RetainedRowAuthority)|TableLikeCookedRowRemap|IdentityOwnCheckBindingAuthority)$'`
for the actual binder, stored-default, remap and parallel-safety units that use
the moved node. Node-only units and public selections are in the functions
README; generator and prepared-literal tests stay with their engine owners.

### Catalog write ownership

`catalogStore.advanceRevisionFrontier` is a pure, saturating calculation over a
descriptor revision and (for relations) its sequence plan stamp. It never reserves
the global allocator or touches live generator state. Publication accumulates a
local frontier and commits it only at the existing successful catalog/storage/
visibility installation under `Engine.mu`; ALTER SET SCHEMA uses descriptor-local
advancement and retains its distinct exhaustion diagnostic.

`CatalogStage` embeds `catalog.Candidate`, whose seven overlay maps are private.
Focused lookup/presence/count and iterator methods read overlay entries without
copies or base enumeration. Method-owned private completion, staged rename,
retirement and default-policy observation/delta writes preserve the same overlay
authority. Original Base observations are distinct from private output, including
nil tombstones. DDL authorization, lifecycle, rowtype synchronization, storage
coordination and engine-dependent validators remain here.

Selected internal contract selectors (historical proof commands; the latest
user instruction requires quickcheck only, with no further race execution):

```sh
make check RUN='^Test(CatalogStoreRevisionFrontier|AlterDropColumnRevisionFrontier(Failures|Terminal)?|AlterSchema(WholeCandidate|OverflowAndSkip))$'
make check RUN='^TestCandidate(PrivateUpdates|RenameRemovalReuse|DefaultObservations|RetainedState)$'
make check RUN='^Test(DefaultPrivileges(CloneStage|RetainedMessage|SchemaLifecycle|CreationFence)|SQLRoutineStagedLookup|CatalogCopiesStayIndependent|UserOperatorIndex(CopyOnWrite|PublicationAndOverlay)|BuiltinProcedureNamesCopyOnEdit)$'
make check RACE=1 RUN='^Test(Catalog(ConcurrentReplacement|CanceledPreparedPublication|SelfReplacementCanceledDuringPreparation|RelationsCapture)|DefaultPrivilegesCreationFence|ControlTransactionsReplacementCancellationBoundary)$'
```

Existing public reference definitions are unchanged. Root observed build,
focused native and pinned PostgreSQL 18.6 proof, selected publication races,
module-only dependencies and actual four-phase direct/prepared smoke passing.
Fresh post-rebase quickcheck and smoke passed against master
`9c6c4562326304fbe4cacff06b2b98b75fe03911`. Earlier full-race failures remain
failed evidence; further racecheck and race-containing finalcheck are explicitly
user-waived, not passing. Post-integration quickcheck replaces finalcheck.
The preexisting <1ms startup target miss remains; warm allocations stayed 5152.
No startup optimization or timing-equivalence claim is made.

### System catalog producer contract

These engine units cover producer/explicit-handler ownership and unknown
errors, independent Owner/root provenance, real runtime capture and ordered
descriptor metadata. Producer units reach producers through the real
`pgcatalog` registry (`catalog_source_producers_test.go`) and the production
wrapper. Engine-free units are in the
[`pgcatalog` README](pgcatalog/README.md#testing).

```sh
make check RUN='^(TestCatalogSourceProducerCoverage|TestCatalogSourceInputProvenance|TestCatalogSourceRuntimeCapture|TestCatalogSourceSpecMetadata)$'
make check RUN='^(TestPGAttributeCapture|TestPGTypeCaptureOwnership|TestDataTypePrivilegesCapture|TestPgClassACLTranslation)$'
make check RUN='^(TestPGIndexesCaptureRetentionAndStableColumnACL|TestPGIndexesSourceInvokerAndFreshAuthorization|TestPGParameterACLSnapshot|TestPGParameterACLCancellation)$'
make check RUN='^(TestReferencePGAttributeSmoke|TestReferencePGCursorsMetadata|TestReferencePGCursorsSourcesAndTime|TestReferenceDataTypePrivilegesKindsAndTemp)$'
make check REFERENCE=1 RUN='^(TestReferencePGAttributeSmoke|TestReferencePGCursorsMetadata|TestReferencePGCursorsSourcesAndTime|TestReferenceDataTypePrivilegesKindsAndTemp)$'
make check RACE=1 RUN='^(TestPGLocksWaitDependencies|TestPGLocksSnapshotOwnership|TestPGCursorsSource|TestPGSequencesCancellation)$'
make check RACE=1 RUN='^(TestReferenceViewLocksTransitions|TestReferenceViewLocksCancellation|TestReferencePGCursorsMetadata|TestReferencePGCursorsSourcesAndTime)$'
make check RACE=1 REFERENCE=1 RUN='^(TestReferenceViewLocksTransitions|TestReferenceViewLocksCancellation|TestReferencePGCursorsMetadata|TestReferencePGCursorsSourcesAndTime)$'
```

Use an external 900-second deadline for each selected check. The unchanged
reference definitions independently cover direct/new/SAME-held DDL metadata,
actual cursor lifecycle/time and physical TEMP visibility; selected races cover
real lock waits, ownership and cancellation. Full supplements remain in the
living implementation plan; these selectors are not a substitute for full gates.

Observed after cutover: the four new contract units passed, and the root's
outside-repository public `database/sql` smoke passed dispatch, direct/new/held
DDL metadata, table/view/index/type links and definition presentation, bounded
runtime rows, retained unadvanced Rows/ColumnTypes across Dump/Reset, and
Restore with compact live slots and data. Selected unchanged native and pinned
PostgreSQL18.6 definitions, selected races, build, dependencies and full quickcheck
also passed. Full racecheck and the race-containing post-integration finalcheck
were waived for this cutover; neither is claimed passing. Steady startup
allocations fell from 5152 to 5100. Latest current-base samples measured
880449–1214836ns/op: three below the 1ms target and two above it. Earlier
samples and overload failures remain in part evidence. These measurements
do not establish full compatibility, a controlled timing speedup, or
consistently sub-millisecond startup.

### Closed function-source protocol

`string_set_query.go` owns invocation scheduling, zip/lateral demand, layout,
ordinality and cleanup, not provider dispatch. `SourceAdmission` separates
closed FROM eligibility from target-set cardinality; OUT names do not control
record expansion. `openFunctionSource` requires explicit admission and opening.
Catalog username/linkage/object-address nodes and provider-local source hooks
live in `functions`; engine catalog/extension runtime operations retain their
original state/validation authority. Transaction inquiry nodes also live in
`functions`; their `TransactionInquiryRuntime` adapter retains live scope,
catalog fences, canonical visibility and lock-manager authority here. Unnest
retains real intagg procedure, object-ref, owner-domain/epoch and revision checks
in installed remap/retag, render, dependency and stored-admission routes.

Series/subscripts/unnest now expose immutable lexical children, substitution,
grouping, volatility, no-fold/preflight and rendering hooks. Equivalent stage
rewrites use `expr.Substitute` without provider re-resolution. Engine position
and both-set guards precede owner occurrence comparison: identityMembers remains
nonreflexive and RegexpSet remains concrete-family-only. Neither grouping nor
arbitrary function spelling replaces these rules.

Existing private source units remain engine-owned because they bind real calls,
query frames or catalog authority; their cursor checks now use Next/Close,
values/presence, typed EOF and released shared state, with no legacy union shim.
Marker child-mapping units use MapSubstituted. The substitution regression
retains a selected JSONRecord provider across an equivalent child's foreign
analysis descriptor, including actual nested-recordset/scalar survivor execution
through lowering and preflight. Only equivalent-child stage traversal enables
source substitution; catalog generation remapping still uses Rebuild/Retag.
New source seam units cover numeric independent ownership,
present scalar NULL/zero-width zip in both directions, partial-open failure,
precanceled/no-argument evaluation, exhausted cancellation and retained payload.

```sh
make check RUN='^TestSourceProtocol(Substitution|Occurrence|CursorOwnership|Presence|Cleanup|Cancellation|BorrowedLifetime)$'
make check RACE=1 RUN='^TestSourceProtocol(CursorOwnership|Presence|Cleanup|Cancellation|BorrowedLifetime)$'
make check RUN='^TestSeries(Binding|Cursor|Overflow|ExpressionHooks|Lowering|Cancellation)$'
make check RUN='^TestSubscripts(Binding|Cursor|Bounds|ExpressionHooks|Lowering|Cancellation|Finalization)$'
make check RUN='^TestNumericSeries(Binding|Invocation|ExpressionHooks)$'
make check RUN='^TestArrayRelational(UnnestCursor|CompositeNull|ZeroWidthComposite|ExpressionOwners|Reownership|Cancellation)$'
make check RUN='^TestReferenceSRFFrom(Consumers|Lifecycle|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceSRFFrom(Consumers|Lifecycle|Controls|FeatureSmoke)$'
```

Public reference outcomes remain unchanged. The native cancellation fixture
observes the new `SourceCursor.Next` advancement frame instead of the removed
cursor method; same-worker admission, cancellation, join and recovery assertions
are retained. Complete family selectors live in their owning reference READMEs.
Observed proof includes full quickcheck, real-driver direct/held/mixed zip/
per-left LATERAL and Dump/Reset/Restore smoke, and allocation/escape comparisons.
Selected lifecycle races passed before the user waived further tests except
quickcheck. Full racecheck was interrupted; post-change startup benchmarking and
finalcheck were waived, not passing. The refactor makes no new SQL admission or
startup-performance claim.
The latest quickcheck retry failed the unchanged LIKE cancellation observer
(`TestReferenceLikeHeldEnteredCancellation/normalizer`) before it issued
cancellation. The user explicitly waived that failure for integration; the
earlier post-rebase quickcheck passed. No passing latest retry is claimed.


Stored literal capture pairs raw normalized preorder with explicitly admitted
`expr.Capturable` written operands. Engine-owned wrappers run first; structural
query/clause/target/node/source/row keys, selected-parent argument annotations,
catalog identities, coercions, SQLJSON RETURNING/query construction and frame
ownership remain engine responsibilities. Hooks never evaluate or reconstruct
definitions; SQLJSON syntax itself comes from the functions rendering protocol.
Capture validates all operands before any annotation write. Source-aware
`Check`/`At` reuse the original boxed Capturable; fixed-array nodes use indexed
access instead of borrowing value-receiver array copies. Safe slices and two
inline operands retain their existing storage path.

SQL routine wrappers validate normalized syntax and captured procedure authority
before exposing written actuals through `CaptureFunction`. Their immutable slice
is borrowed without copying; selected implicit casts retain argument-indexed
annotations so stored calls preserve overload identity through Dump/Restore.

```sh
make check RUN='^TestViewLiteralProtocol(WrittenOperands|OccurrenceOwnership|Annotations|RejectsMismatch|QueryBoundaries|InstalledWrapper)$'
make check RUN='^TestCaptureOperands(WrittenSlots|RawKinds|SpecialAnnotations|NoAllocations)$'
```

The boundary test binds a real nested query/function source and remaps its frozen
presentation to fresh type descriptors. Existing stored-consumer reference tests
remain unchanged and require both selected native and real PostgreSQL proof.

SQLJSON display and stored replay interpret the same `expr.Rendering` as other
function nodes. The engine unwraps only a recognized formatted-input conversion,
supplies a RETURNING type usage only from its owning conversion, and retains
genuine outer casts and typed-TEXT transport. Raw view rendering still owns its
query/captured operands and type usages, sharing only the pure constructor syntax
recipe with functions. Catalog qualification, typmods/domains, descriptor
validation and coercion Rebind/Equal checks remain here.

`TestSQLJSONRendererBoundaries` constructs real constructors, plans and a
published domain without issuing public SQL. It checks exact display/replay,
pretty/nonpretty explicit/implicit cast boundaries, empty grammar, UTF8 suffixes,
ordinary call identity, immutable nodes and rendering a real volatile operand
without an evaluation context. The private matrix and identical public smoke
passed after cutover, including full emitted-definition/Dump byte equality and
independent values/types/defaults. Selected pinned18.6 dual-mode, selected race,
build/dependency and full quickcheck gates also passed. Whole-project race runs
hit their 900-second deadlines; the user then waived all remaining checks except
quickcheck, including post-integration finalcheck. No full-race pass is claimed.
An earlier documentation-only quickcheck rerun failed in the unchanged regexp
cancellation provider-observation test; the user explicitly waived that failure
for integration. No clean final quickcheck is claimed.

```sh
make check RUN='^Test(SQLJSONProtocol|JSONPredicateProtocol|SQLJSONRendererBoundaries|SQLJSONReturningOwnershipRebind)$'
make check RUN='^TestReferenceSQLJSON(Stored|Controls|EmptyObjectControls|AssignmentControls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceSQLJSON(Stored|Controls|EmptyObjectControls|AssignmentControls|FeatureSmoke)$'
make check RUN='^TestReferenceSQLJSON(Format|Returning|OutputEncoding|Diagnostics|Metadata)$'
make check REFERENCE=1 RUN='^TestReferenceSQLJSON(Format|Returning|OutputEncoding|Diagnostics|Metadata)$'
```

Dependency function diagnostics render every validated installed signature using
canonical builtin type names and the captured diagnostic path for other types,
without changing regprocedure output or function-name qualification. Internal
coverage preserves conversion `cstring`/`internal` spelling and generic extension
signatures. Prepared mixed routine/operator captures recover only after successful
atomic Execute reanalysis; restoring authority alone does not revive an invalid
shared analysis cell. Failure atomicity, exact datums, dependencies and result
signatures remain asserted.

```sh
make check RUN='^(TestConversionDescriptorNoopAndPinBoundaries|TestRoutineMixedOperatorCompoundCapture|TestExtensionDropDiagnostics)$'
```

Extension retirement captures the complete authentic target set, including
automatic aliases. The shared incoming-dependency preflight separately inspects
changed committed owners through their effective candidate descriptors: removed
or genuinely rewired owners and retained providers are not dangling edges.
Surviving column usages, domain base/array identities and bound domain CHECK
references join the existing expression/foreign/sequence fences. Rejection is
`catalog.Conflict` before mutation, never adoption or a second cascade; original,
epoch, storage and index predecessor fences remain independent.
Existing expression/attachment/ownership observations remain conservative
retirement fences during retained-message admission, before removals are
overlaid. Candidate target presence gates only the added type-usage and
domain-CHECK checks; it does not disable those established observations.

`TestExtensionDropPublication` exercises six genuine late-consumer schedules:
view, query_int column and domain CHECK at validated publication and outer
commit. Its two explicitly unsupported query_int[] column cells instead prove
native 0A000 admission and unchanged committed state; array storage is not added.
Candidate controls cover removal, real rewiring, dependent replacements, retained
providers and stale originals. Authentic LookupArray usage/retirement controls,
manifest retirement and supported domain-array lifetime tests supplement these
six fences without fabricating query_int[] consumers.

`TestPGDependDomainCheckDependencies` binds and publishes a real domain CHECK
with repeated installed-function calls. Existing stored dependencies project
one normal edge from its actual constraint OID, not from the domain type, through
the collector's pinning/cancellation policy; real CHECK pruning removes it while
retained snapshots keep it. The public domain witness covers CASCADE/rollback
identity and exact diagnostics.

```sh
make check RUN='^TestExtensionDropPublication(Candidate)?$'
make check RUN='^(TestPGDepend(Shape|ConstructionBags|DomainTopologyAndPinning|DomainCheckDependencies|CanceledCapture|CancellationDuringWalk|ExpressionReferences|QuerySourceSuppression|RegtypeValueReferences|SetArmCoercionReferences)|TestDomainCheck(BindingIdentityInternal|EvaluationFrameInternal|RetirementAndRemapInternal)|TestDomainPublicationFencesBaseAndConcurrentColumns|TestArrayDomainDependencyClosureAndLifetime|TestExtensionDropManifest)$'
make check RUN='^(TestCatalogDrop(PublicationAndRetainedReads|ReferenceAndConflictFences|ChangedDescriptorAndUnrelatedRemoval|CanceledGroupAndRestoreEpochFence)|TestDropOwned(UnionRestrictAndCascade|TypeACLAndCancellation))$'
make check RACE=1 RUN='^(TestExtensionDropPublication(Candidate)?|TestPGDependDomainCheckDependencies|TestReferenceExtensionDropTransactions|TestReferenceExtensionTransactions)$'
```

Transaction-versus-statement callback ordering is exercised by
`TestSessionTimeoutResidualBeforeStatement`: the actual engine, transaction,
command timer and blocked mutation live inside a `synctest` bubble. A 500ms
transaction aged 300ms must terminate the actual request at 500ms, before its
375ms statement budget reaches 675ms. Exact FATAL25P04, no effects, physical-end
and lock cleanup, and healthy peer mutation/readback are mandatory. This replaces
a public wall-clock callback-order assumption, not the intentional opposite case:
`TestSessionTimeoutPendingOrderMechanics` and `TestSessionTimeoutPendingLifecycle`
still require already-pending statement ERROR57014 priority.

`TestSessionTimeoutResidualBeforeIdle` covers the idle counterpart with actual
setting publication and ready-boundary arming: transaction500ms is unchanged
when idle375ms starts after300ms, so idle is due at675ms. A private INSERT and
the peer's exact queued lock remain through499ms; full location-free25P04 at500ms
must roll back the write, release the peer, end physical ownership and empty
locks before healthy-peer write/readback. The public wall-clock counterpart is
removed because both-already-due idle25P03 intentionally wins; that opposite
outcome remains strict in `TestSessionTimeoutPendingOrderMechanics`.

Both residual bubbles inspect settled state after `synctest.Wait` and fail
immediately on unexpected worker completion or a missing exact request; they
never spin while waiting for fake time to advance. The idle counterpart begins
a real explicit peer transaction for `LOCK TABLE`, observes the canonical
manager request (an untimed peer has no operation timeout-slot projection), and
rolls back that peer after the owner's timeout grants its lock.

```sh
make check RUN='^TestSessionTimeout(ResidualBefore(Statement|Idle)|PendingOrderMechanics|PendingLifecycle|PendingCompletedLifecycle)$'
make check RACE=1 RUN='^TestSessionTimeout(ResidualBefore(Statement|Idle)|PendingOrderMechanics|PendingLifecycle|PendingCompletedLifecycle)$'
```

Each selected invocation needs an external 900-second deadline. Public portable
timeout contracts remain dual-mode tests in `reference/session_state`.

Extension versions use one immutable catalog inventory and the existing
statement-owned catalog stage. Only base-reachable graph edges require typed
actions; fuzzy1.1→1.2 creates the real DM member. UPDATE retains exact extension
and predecessor-member read fences (including ACLs) from the staged snapshot,
not newer descriptors observed after outside-lock provider validation.
Old members are fences, not redundant replacements. CREATE/UPDATE share final
authority/name/epoch publication; no retry, second registry or script runner.

Selected unit proof:
```sh
make check RUN='^(TestExtensionVersion(Graph|Manifest|Publication|PathsConsumers)|TestExtensionOwnerManifestAdmission|TestAvailableExtensions(Projection|Capture|StatementFrontier))$'
make check RACE=1 RUN='^(TestExtensionVersionPublication|TestAvailableExtensionsStatementFrontier)$'
```
Use external900-second deadlines. Graph tests cover topology/detachment and
registration/action validation; manifest/owner tests use genuine providers
and version-specific nine/ten members. Publication tests gate genuine validation
outside the mutex, then perform real competing updates, ACL/role mutations,
Reset/replacement or cancellation, retaining exact winner consumers/data.
Public SQL/control proof stays in `reference/catalogs/extension_version_*`.

Provider records, member validation, owner metadata and remap live in
[`extension`](extension/README.md); `procedureProvider` owns the procedure
class and delegates installed members to `extension.CatalogProvider`. Each
`*_stage.go` declares an installer that embeds its stock provider and stages
it; the registry holds the installers and installed members hold the bare
provider. Run `make check RUN='^(TestExtensionMembersHoldBareProviders|TestInstalledCodecArrayUsesRegisteredTypeProvider|TestExtensionProviderValidatesOnlyInstalledProcedures)$'`.

### Expression node runtime and query executor

The plan IR and the expression node types live in
[`plan`](plan/README.md), which documents the `plan.Runtime` capability.
The query executor and `*exec.Context`, its only implementation, live in
[`exec`](exec/README.md): the shared prologue, the `Eval*` bodies, the node
openers, row-lock arbitration and the cursors. The engine implements
`exec.Host` on `statementHost` and installs `enginePreparation`; it names
the `exec` types directly, with no aliases. A data-modifying
CTE carries its binding as the opaque `plan.CTEMutation`, recovered only
through `analyze.MutationBinding`. The installed extension routine nodes and the
`pg_extension_update_paths` node live in `plan` too; their provider
validators (`validateInstalled*`) stay here; `exec` reaches
`analyze.ValidInstalledText` through the preparation value.

Run `make check RUN='^TestNodeRuntimePrologue$'` for the prologue seam; public
evaluation proof is the existing query, CTE, RETURNING, MERGE, window,
grouping, domain and generated-column reference selections.

### Relation lock timeout mechanics

Relation-lock timeout units observe completion and lock state together in short
repeated checks; a short SQL timeout need not remain queued until the parent is
scheduled. Exact diagnostics, holder preservation, empty final queues and session
recovery remain mandatory. Fairness uses the existing `Manager.ExpireWait` seam
rather than elapsed sleeps: its hour-long test policy prevents incidental
wall-clock expiry while an explicit deadline probe proves queue settlement.
The canonical per-acquisition proof lives in
[`lock/timeout_test.go`](lock/timeout_test.go), not a second engine duplicate.
It retains two successful grants across modeled cumulative waits, independently
brackets each fresh deadline and verifies expiry at a later request's own bound.

`TestRelationLockWaitsTimeoutCleanup` complements that canonical budget proof
with one real 200ms request expired at its own deadline. Exact55P03 and retired
callbacks are required even if its real timer already won. Before holder release,
it checks the unchanged exclusive holder, empty pending queue and removed waiter
journal entry; after release, the same ticket must acquire untimed successfully.
Complete teardown must leave no lock state.

```sh
make check RUN='^TestRelationLockWaits(Timeout|TimeoutPerAcquisition|TimeoutCleanup|Fairness)$'
make check RUN='^TestRelationLockWaits(Timeout|TimeoutPerAcquisition|TimeoutCleanup|Fairness)$' RACE=1
```

### Other engine invariants

Caller-interruption and row-view units observe the actual queued request before
triggering a controlled caller deadline. Untimed waits and delayed-callback
arbitration use logical expiration instants, not elapsed sleeps. Statement-timeout
recovery expires the exact admitted row wait and retains the timeout,
aborted-transaction, rollback-data and lock-cleanup assertions. Public SQL timeout
witnesses continue to use real timers.

```sh
make check RUN='^(TestRowLockViews|TestRowLockViewProvenance|TestRowLockViewStored)$' RACE=1
make check RUN='^(TestRelationLockWaitsCallerContext|TestRelationLockWaitsUntimedGrant|TestRelationLockWaitsDelayedDeadlines)$' RACE=1
make check RUN='^TestTransactionIsolationStatementTimeout$' RACE=1
```

Default-ACL catalog units use real producer ASTs and immutable captured catalog/
role authority, not public SQL. They cover stable/recreated identities, final-key
allocation, allocator collisions/exhaustion, private/remapped roots, exact typed
ACL masks/order, owned captures, invariant rejection, deterministic cancellation
and actual catalog ACL publication rechecked through direct/fresh/held execution.
Public dual-mode proof lives in
[`reference/catalogs`](../../reference/catalogs/README.md#pg_default_acl-selected-proof).

```sh
make check RUN='^TestPGDefaultACL(Identity|CaptureOwnership|AuthorityAndCancellation|PermissionPublication)$'
make check RUN='^TestPGDefaultACL(Identity|CaptureOwnership|AuthorityAndCancellation|PermissionPublication)$' RACE=1
make check RUN='^(TestDropOwnedDefaultCanonicalCleanup|TestDefaultPrivileges(CloneStage|RetainedMessage))$'
```

Transaction identity, commit order, commit timestamps, snapshot views and SSI
belong to `visibility.Registry` (`Engine.visibility`) under Engine.mu; see
[visibility/README.md](visibility/README.md), which also lists the identity
and commit-timestamp selectors.

Transaction identity/timestamp function-specific binders, immutable nodes,
evaluation, source operations and node protocols belong to
[`functions`](functions/README.md#transaction-inquiry-ownership-and-demand).
`transaction_inquiry_runtime.go` implements their narrow runtime capability;
`transaction_identity_store.go` and `commit_timestamp_store.go` retain their
existing domain/epoch fences. Identity context checks precede child evaluation;
timestamp strict NULL and xid0/1/2 paths precede capability demand. Member SQL
packing follows the manager snapshot clone outside locks; last XID/timestamp
are sampled together under `Engine.mu`. No allocator/history or publication
authority moved into a node.

Mixed real binding/lifecycle tests remain here rather than becoming leaf fakes.
`TestTransactionIdentityBinding` retains fixed provider IDs, dependency-manifest
admission, replay/grouping/traversal and fresh-owner retag/remap assertions.
`TestCommitTimeExpression` retains actual column-origin and slot remapping,
original immutability, shape/provider/properties and strict whole NULL checks.
Genuine demand/immutable-node units are listed in the functions README; no whole
existing definition was removed. Selected engine proof:

```sh
make check RUN='^TestTransactionIdentity(AgeArithmetic|Allocation|History|Completion|Binding)$'
make check RUN='^TestCommitTime(Assignment|Publication|Replacement|Expression)$'
make check RUN='^TestTransactionIdentity(MultiHistory|GrantActions|Savepoint)$'
make check RACE=1 RUN='^TestCommitTime(Assignment|Publication|Replacement)$'
```

The root observed the Item8 worktree build and standalone public-driver smoke
pass after source edits, including real member ownership, exact disabled
timestamp diagnostics, actual commit timestamps and byte-identical symbolic
Dump/Reset/Restore. That observation does not replace required selected
dual-mode, full-suite, race, review or post-integration proof.


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

`TestRowLockViews`, `TestRowLockViewProvenance` and `TestRowLockViewStored`
observe a nonterminal request for the exact requested mode on a physical row
held by the blocking transaction before triggering the controlled caller deadline
with `lockWaitCaller(t, true)`. These contention/provenance/strength witnesses
retain `context.DeadlineExceeded`, no result cursor and no affected rows without
racing a wall-clock deadline against enqueue. In particular, the restored-layout
case must still block `FOR KEY SHARE` behind the stored `FOR UPDATE` mark after
column rename/drop and real Dump/Restore. Queue observation, worker execution,
result collection and cleanup retain their existing five-second bounds. Worker
goroutines return outcomes; only the owning test goroutine makes fatal
assertions. Dedicated timeout-budget tests remain unchanged.

```sh
make check RUN='^(TestRowLockViews|TestRowLockView(Provenance|Stored|Recheck|Lifetime))$'
make check RUN='^(TestRowLockViews|TestRowLockView(Provenance|Stored|Recheck|Lifetime))$' RACE=1
```


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

Typed wire user cancellation is deferred only through genuine COPY receive
ownership and decoding/conversion. For native wire FROM, including binary input,
healthy admission establishes that owner before publishing CopyInResponse and
carries it through the unlocked Start return fence into the first decoder.
Pre-canceled admission still rejects before Start; callback errors and
owner/catalog fences remain authoritative. Embedded COPY and COPY OUT are
unchanged.
The operation selects an actual standard input context
without replacing its original/source context. Only after that cancellation is
pending does the lazy view detach its cancellation key and re-project the exact
captured absolute deadline; no budget is restarted. Required codec conversion
errors keep their genuine row/column context and take precedence. Successful
conversion observes pending cancellation before returning a row to assembly.
The hold is cleared before constraints/application and restored on every exit;
ordinary Go caller cancellation and native input policy remain unchanged.
The wire input caller arms the first-record ownership hold and installs its
cleanup before invoking Start, so immediate frontend cancellation cannot win
the callback resume check. The callback itself does not arm a second hold.
Cleanup covers failed Start, failed resume validation, decoder exit and panics
after ownership begins. `TestProtocolCopyInputStartOwnership` cancels inside the
actual callback without timing, covering valid/invalid input, embedded
cancellation, pre-start cancellation, failed publication and ownership cleanup.

At text-decoder entry and binary row-decoder entry after header initialization,
a typed user cancel arriving just after selection of a live context causes a
refresh through that same held input owner, followed by another cancellation
check. This decoder refresh does not alter the caller's pre-Start ownership.
This does not bypass an ordinary, terminal or deadline error, change source
cancellation, or promise that CopyInResponse alone establishes socket-read
admission. The complete record
still determines conversion errors; successful conversion must still observe
the pending-cancel veto.

`Session.EndRequested` lazily observes canonical ending before lease cleanup,
without acquiring a lease or changing `WaitEnd`. Session ending directly cancels
any held input view. The adapter's existing watcher requests ending on physical
shutdown/disconnect and observes this wake for later FATAL delivery, so an earlier
user-cancel cause cannot hide physical or session termination.

Session checks and operation admission observe due transaction/idle budgets
under `Session.Mu` before publishing a lease or invalidating the idle slot.
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

`TestProtocolCopyCursorCapturedCancellation` and
`TestProtocolCopyBinaryCursorCapturedCancellation` deterministically schedule
genuine source cancellation between input-context selection and the cursor's
first cancellation check. Real text/binary parsing and integer conversion
distinguish invalid input precedence from successful conversion's pending-cancel
veto; ordinary and terminal cancellation retain their own authority. Selected
owner-seam proof:

```sh
make check RUN='^TestProtocolCopy(Binary)?CursorCapturedCancellation$'
```

`TestProtocolCopyStartCancellation` retains the canonical simple/extended
valid/invalid first-record handoff proof. Its complementary
`TestProtocolCopyStartCancellationAuthority` checks pre-canceled admission,
ordinary/FATAL causes and primary callback failures without consuming input.
Both use actual COPY Start callbacks; neither infers a socket-read observer
or PostgreSQL admission equivalence.

```sh
make check RUN='^TestProtocolCopyStartCancellation(Authority)?$'
```

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
make check RUN='^TestProtocolCopyInputStartOwnership$'
make check RUN='^TestProtocolCopyInputStartOwnership$' RACE=1
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

### Built-in catalog seed

Every engine seeds the same built-in catalog from compiled tables; only the
current database descriptor depends on engine options. `builtin_catalog.go`
therefore validates the seed once per process (the current database on every
engine) and shares one procedure-name index, as `routine.SeededOperatorNames` does for
operators. A seed that depends on new engine options must be validated per
engine too. Startup and every publication scale with the seed size, so keep
seeding and whole-catalog copies free of per-entry rehashing and allocation;
check `make bench` allocations when adding built-ins. Seed values come from
pure constructors (`routine.BuiltinProcedures`, `typedef.BuiltinConversionProcedures`,
`typedef.BuiltinDomainCheckSeeds`, ...); the `install*` methods in `builtin_catalog.go`
write them into the catalog in `newEngine`'s order.

```sh
make check RUN='^(TestBuiltinCatalogValidatesForEveryEngineConfiguration|TestBuiltinProcedureNamesMatchSeed|TestOperatorDependsOnMatchesDependencyAddresses)$'
make check RUN='^TestBuiltinProcedureNamesCopyOnEdit$' RACE=1
make check RUN='^(TestCatalogCopiesStayIndependent|TestCatalogTypeJournalSlotsCopyOnWrite)$'
```

### SQL routine admission boundaries

SQL routines extend stored DEFAULT closure only when a real checked SQL call
occurs in that closure. They do not make an otherwise rejected volatile
expression admissible: bare `nextval(...) + 1` retains the existing sequence
default boundary, and the existing `currval`/dynamic-sequence restrictions remain.
The callee body is not executed while admitting a stored default.

An ordinary call with no native provider distinguishes a truly missing signature
(`42883`) from a viable released core signature whose native capability is
unavailable (`0A000`). The existing release identity index supplies diagnostic
evidence only; it never grants execution, adds catalog rows, or installs extension
functions. Names from uninstalled extensions remain missing. Argument count,
implicit coercion, exact quoted spelling and namespace visibility still apply.
Specialized executable providers continue to own their signature diagnostics.
Diagnostic arity also respects trailing defaults from the existing native
declaration inventory, supplemented by lookup-only release metadata where no
native declaration exists. For example,
[`pg_terminate_backend(pid integer, timeout int8 DEFAULT 0)`](https://github.com/postgres/postgres/blob/REL_18_6/src/backend/catalog/system_functions.sql#L401-L404)
has a genuine one-argument call shape. This never evaluates a default or
implements backend signaling; wrong supplied types and invalid arities still
produce missing-signature diagnostics.

Private binder/closure regressions:

```sh
make check RUN='^Test(SQLRoutineDefaultClosureBoundary|SQLRoutineStoredDefaultValidation|SerialStoredDefaultInvariants|UnavailableFunctionIdentityBoundary)$'
make check RUN='^Test(SQLRoutineDefaultClosureBoundary|UnavailableFunctionIdentityBoundary)$' RACE=1
```

These units supplement the public routine definitions in
[`reference/query_core`](../../reference/query_core/README.md) and the existing
native capability witnesses in the owning reference packages; they do not replace
selected dual-mode verification.
