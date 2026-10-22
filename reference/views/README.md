# reference/views

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### VIEW replacement consumer ownership

`view_replace_atomic_suffix_test.go` retains
`TestReferenceViewReplaceAtomicSuffixAndCancellation` as the single owner of the
plain `DB.Exec` Unicode binding errors, invalid-suffix atomicity, precancellation
and final unchanged-view read. Both `/* café */` vectors preserve complete
structured ERROR diagnostics: `42703` / `column "missing" does not exist` and
`42P02` / `there is no parameter $1`, with positions counted in Unicode characters,
not bytes. Each failure returns no result. The three replacement/rename/drop
suffix errors and three precanceled `DB.ExecContext` calls also require nil
results; cancellation must satisfy `errors.Is(err, context.Canceled)`.
The final actual plain `DB.Query` fully consumes exactly INT4 id7, checking names,
raw values, complete rows, Rows.Err and explicit Close to prove no failed
operation published a changed or missing view. Plain-Exec setup retains the
unsupported LastInsertId assertion.

`TestReferenceViewReplaceBinding` in `view_replace_binding_test.go` remains the
recorded Context binding companion, including surplus names and duplicate labels.
`view_replace_boundary_consumer_routes_test.go` keeps
`TestReferenceViewReplaceCapabilityBoundariesConsumerRoutes` and its independent
backend-specific expectations; only its redundant
`TestReferenceViewReplaceBindingAndAtomicSuffixConsumerRoutes` declaration is
removed after moving the two unique plain-Exec diagnostic vectors.

`view_replace_prepared_lifecycle_routes_test.go` and
`TestReferenceViewReplacePreparedTargetLifecycle` are unchanged. Existing
`SchemaReferenceCleanup(t, "s")` registration before scenario construction owns
nonpublic-schema teardown after fixture handles close. All five held
Prepare-before-create utility cases remain. A bounded temporary wrapper executed
the complete lifecycle twice in one pinned PostgreSQL18.6 oracle lifetime and in
native mode. After each PostgreSQL teardown, an independent administrative
`pg_namespace` query found count0 for `s`; the next lifecycle's literal
`CREATE SCHEMA s` succeeded. The wrapper was removed after proof. The existing
smoke-cycle companion uses `view_smoke_cycle`, not `s`.

Run these exact retained owners and companions in both modes, with an external
900-second deadline per command:

```sh
make check REFERENCE=1 RUN='^(TestReferenceViewReplaceAtomicSuffixAndCancellation|TestReferenceViewReplaceBinding|TestReferenceViewReplaceCapabilityBoundariesConsumerRoutes|TestReferenceViewReplacePreparedTargetLifecycle|TestReferenceViewReplaceNativeControls|TestReferenceViewReplaceCollationReplay|TestReferenceViewReplaceSmokeCycleReplayControls)$'
make check RUN='^(TestReferenceViewReplaceAtomicSuffixAndCancellation|TestReferenceViewReplaceBinding|TestReferenceViewReplaceCapabilityBoundariesConsumerRoutes|TestReferenceViewReplacePreparedTargetLifecycle|TestReferenceViewReplaceNativeControls|TestReferenceViewReplaceCollationReplay|TestReferenceViewReplaceSmokeCycleReplayControls)$'
make check REFERENCE=1 RACE=1 RUN='^(TestReferenceViewReplaceAtomicSuffixAndCancellation|TestReferenceViewReplacePreparedTargetLifecycle)$'
make check RACE=1 RUN='^(TestReferenceViewReplaceAtomicSuffixAndCancellation|TestReferenceViewReplacePreparedTargetLifecycle)$'
```

Public feature smoke selects
`^TestReferenceViewReplaceAtomicSuffixAndCancellation$` with `make check RUN='...'`,
first adding `REFERENCE=1`, then without it. Expect both complete Unicode
diagnostics, all three suffix errors and three precancels with nil results, and
the complete unchanged id7 read. Whole-project Docker-free verification uses
`make quickcheck`; it does not replace selected native and pinned PostgreSQL proof.

### Recursive view contract

The nine `TestReferenceRecursiveView` definitions cover mandatory column lists,
finite direct/prepared/held reads and empty metadata, full hidden recursive UNION
keys, complete positioned and synthetic diagnostics, suffix/SELECT INTO boundaries,
physical dependencies and canonical reconstruction, native owned replay, current
owner/original-invoker authority, active cancellation and failure publication.
Expectations are independently reasoned from PostgreSQL18 release semantics;
the pinned18.6 oracle groups passed before authorized production implementation.
Native Dump/Reset/Restore phases are explicit, not invented PostgreSQL APIs.

Diagnostic subtests name the exact SQL and direct/prepared route. Both backends
analyze CREATE VIEW utility bodies at execution. Native lexical recursion and
recursive-body INTO checks instead fail during preparation; PostgreSQL defers
those checks until execution. Raw grammar failures, including recursive
CHECK OPTION suffixes, fail preparation on both backends. INTO body errors use
the target relation's position; ordinary CREATE VIEW INTO is unpositioned
`0A000`. These diagnostic contracts were observed on pinned18.6 and native.
Authority subtests also identify the grant/revoke phase and retain
the SAME role-switched connection for preparation and held execution, never the
administrative pool. Denials remain required for empty/limited reads.

```sh
make check REFERENCE=1 RUN='^TestReferenceRecursiveView(Core|Columns|Boundaries|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceRecursiveView(Dependencies|Replay|Authority|Atomicity|FeatureSmoke)$'
make check RUN='^TestReferenceRecursiveView(Core|Columns|Boundaries|Diagnostics)$'
make check RUN='^TestReferenceRecursiveView(Dependencies|Replay|Authority|Atomicity|FeatureSmoke)$'
make check RACE=1 RUN='^TestReferenceRecursiveView(Atomicity|Authority|Replay)$'
```

Each selected command has an external900-second deadline. The feature-smoke
definition is a compact independent SQL/API witness; it does not replace the
outside-repository public consumer smoke or whole-project verification.

The outside-repository session consumer `recursive-view-smoke.go` uses only
public driver/connector and native controls. From the repository root run
`timeout 900s go run /absolute/session/local/recursive-view-smoke.go`, substituting
the actual session path supplied in the living plan. Exit0 quietly means finite
direct/held rows and metadata, hidden UNION keys, exact diagnostics, renamed
dynamic dependencies, executable canonical definition, same/fresh-owner replay,
joined blocking-read cancellation/recovery and atomic late-error INSERT passed.
The worker must report cancellation, not merely exhaust the command deadline.


### Deparse identity control fixture lifecycle

`TestReferenceDeparseConstraintsReviewIdentityControls` retains all 25 identity
cases under deterministic `00`–`24` subtests. Each independent case owns a fresh
shared fixture with the existing 30-second context, its original constraint
table, both independently deparsed/copied constraint tables and its data table.
The original case index remains in table names and held-statement relationships;
native current-database expectations use the actual per-case test-name DSN.
Direct/prepared deparse and structural metadata, SAME held statements through
native Dump/Reset/Restore, both identity frames, complete CHECK errors, data and
owner/cleanup assertions remain unchanged. No deadlines, retries, sleeps,
provider behavior or expected identity values are widened or suppressed.

```sh
make check REFERENCE=1 RUN='^TestReferenceDeparseConstraintsReviewIdentityControls$'
make check RUN='^TestReferenceDeparseConstraintsReviewIdentityControls$'
make check RACE=1 RUN='^TestReferenceDeparseConstraintsReviewIdentityControls$'
```

Run pinned mode first, with external 900-second deadlines for each selected
command. This fixture-lifecycle remedy does not replace whole-project or
post-integration verification.

### Rewrite rules

Rule regressions use public direct and prepared routes with independent literal
rows, command counts, empty-result metadata and complete structured diagnostics.
The rule and trigger CHECK OPTION scenarios are separate: user rewrite rules
are engine mechanisms; procedural row triggers remain an explicit boundary.

Pinned discovery distinguishes query producers from physical-row callbacks:
an action without NEW still retains two-row VALUES multiplicity. Separate
INSERT SELECT and zero-source witnesses cover the different query shape.
Without original RETURNING, direct Query exposes SELECT-action result sets; actual
Go-held Query retains the original INSERT's empty result description while
executing the action. With original RETURNING, both routes expose only its
descriptor/rows, including the empty schema; auxiliary SELECT output is suppressed,
not execution/errors. These routes must not be collapsed or labeled prepared merely
by repeating direct Query.

`ConditionsQueryShapes` pins source-clause survivor demand, supplied INSERT
SELECT output sharing versus VALUES/default macro occurrences, unused volatile
output retention, pure unused error pruning and computed post-substitution
ORDER scheduling. `RestrictionsAdmission` pins top action/immediate INSERT
source WITH OLD/NEW rejection without banning legal nested correlated WITH.
Layered view rule witnesses retain outer ACL/RETURNING authority, suppress
invalidated inherited CHECK groups only at an explicit rewrite boundary, and
preserve the fresh action target's own checks.


Oracle discovery runs pinned PostgreSQL first; remove any temporary local
backend filter before native proof. Each selected command has an external
900-second deadline. Run each selector below with `make check REFERENCE=1 RUN='…'`
first, then `make check RUN='…'`:

```text
^TestReferenceRule(DDL|DDLNotices|SelectReturn|SelectReturnAdmission|SelectReturnRecursion|Boundaries)$
^TestReferenceRule(Effects|Conditions|ConditionsQueryShapes|Ordering|OrderingCountAuthority)$
^TestReferenceRule(Returning|ReturningErrors|ReturningMutationProviders|ReturningSelectActions|Restrictions|RestrictionsAdmission|RestrictionsMergeMaterialized)$
^TestReferenceRule(Atomicity|AtomicitySavepoint|Privileges|Dependencies|DependenciesEdges)$
^TestReferenceRule(Prepared|Controls)$
^TestReferenceViewCheck(RuleBoundary|TriggerBoundary)$
^TestReferenceRuleFeatureSmoke$
```

Native controls in the shared scenario exercise genuine post-data Dump/Reset/
Restore, renamed targets and columns, future rewritten writes, and atomic invalid
or canceled import. PostgreSQL has no invented equivalent control API.
Diagnostic candidates and SELECT-action result-set behavior must be pinned by
the real oracle before implementation; fixture mismatches expose all fields,
including positions and contexts, rather than checking SQLSTATE alone.

Internal template/program/provider ownership is complementary proof:

```sh
make check RUN='^TestRule(TemplateSubstitution|Program(Ordering|Authority|Recursion|AtomicStage)|CTECommandNormalization|Definition(Dependencies|Remap)|ViewReturnReplacement)$'
make check RACE=1 RUN='^TestRule(TemplateSubstitution|ProgramAuthority|CTECommandNormalization|DefinitionRemap)$'
```

Focused WITH lexical proof, independently specified in the same shared public
definition (run both commands, not only the PostgreSQL discovery route):

```sh
make check REFERENCE=1 RUN='^TestReferenceRuleRestrictionsAdmission/with_(body_(new|old)|legal_outer_new|nested_correlated_new)/held=(false|true)/postgres$'
make check RUN='^TestReferenceRuleRestrictionsAdmission$'
```

Action-owned modifying-WITH persistence and the distinct SELECT-event exclusion
are shared direct/held scenarios; native controls additionally exercise renamed
auxiliary/unused target columns, post-data Restore and future writes:

```sh
make check REFERENCE=1 RUN='^TestReferenceRule(Controls|SelectReturnAdmission)/(action_with_modifying_cte|modifying_with)/held=(false|true)/postgres$'
make check RUN='^TestReferenceRule(Controls|SelectReturnAdmission)$'
```

Review regressions separately prove caller-owned input sources versus rule-owned
sinks, current conflict-row authority, forbidden OLD/NEW conflict namespaces,
EXCLUDED and bounded-VALUES action round trips, and whole-program static
preparation before sequence effects. Computed window schedules, result-signature
rejection, midstream reader failure/cancellation, eager rows across replacement,
and ordinary/TEMP rule identity collisions use actual public consumers and
native controls rather than structural wiring assertions.

Run every selector in both modes:

```text
^TestReferenceRuleReview(PrincipalSources|ConflictRecipes|ConflictAuthority)$
^TestReferenceRuleReview(ActionRoundTrip|ProgramPreflight|QualifiedSet)$
^TestReferenceRuleReview(WindowScheduling|ResultSignature)$
^TestReferenceRuleReview(ReplacementLifetime|TemporaryCollision|TemporaryProvenance)$
^TestReferenceRuleReview2Conflict(Reuse|Sessions|Layout|ColumnACL|ColumnDependencies|NothingDependencies)$
^TestReferenceRuleReview2(NestedPreparation|CTEPreparation|CombinedOutputs|MutationPreparation)$
^TestReferenceRuleReview3CTE(Reuse|Recovery|Sessions|Restore|Cardinality)$
^TestReferenceRuleReview3(IndependentAuthority|InheritedAuthority)$
^TestReferenceRuleReview4InvokerViewAuthority$
^TestReferenceRuleReview4Inference(Matching|Mismatch)$
^TestReferenceRuleReview5ViewProviderRetirement$
^TestReferenceRuleReview6Named(Dependency|Retirement)$
^TestReferenceRuleReview6Named(RenameRoundTrip|NameReuseRoundTrip)$
```

Static operator folding is local to rule-bearing program preparation, including
normalized CTE primaries and demanded derived/mutation producers. Supplied Params
specialize into private typed executable recipes each Execute; ordinary/mode-only
imported-parameter protection remains unchanged. Row-, stage-, checked-domain and
volatile expressions retain their runtime frontier. Non-evaluating conditional
demand and inherited/correlated frame closure preserve unused payload pruning
without dropping demanded supplier values or modifying stored graphs.

```sh
make check RUN='^TestRule(CTECommandNormalization|AssignmentMapping|StaticOperatorPreflight)$'
make check RACE=1 RUN='^TestReferenceRuleReview(ReplacementLifetime|TemporaryCollision|TemporaryProvenance)$'
```

Conflict review witnesses retain the same held consumer through repeated keys,
failure/recovery, two-session contention and legal post-definition ADD/DROP/ADD.
DO UPDATE's implicit initial EXCLUDED dependencies are distinct from actual
column-only ACL requirements; DO NOTHING separately permits unused initial DROP.
Native layout round trips use real Dump/Restore and future writes, not mock imports.

```sh
make check RUN='^TestRule(ConflictInstantiation|ConflictAttributeRefresh|IndependentActionProducer|StaticOperatorPreflight)$'
make check RACE=1 RUN='^TestReferenceRuleReview2Conflict(Reuse|Sessions|Layout|ColumnACL|ColumnDependencies|NothingDependencies)$'
make check RACE=1 RUN='^TestReferenceRuleReview2(NestedPreparation|CTEPreparation|CombinedOutputs|MutationPreparation)$'
```

Stored action-owned modifying-CTE conflicts have the same repeated-key,
failure/recovery, actual two-session and native renamed Dump/Restore coverage.
The cardinality witness independently retains `21000` and exact empty bags after
failure; it is not relaxed by fresh per-command workspaces. Complementary units
invoke actual composition, refresh and normalization, preserving stored recipes,
provider/scan coherence and command-private mutable state.

Independent UPDATE/DELETE authority witnesses retain the SAME held consumer
through owner transfer and disjoint caller/owner grants. Empty event tables still
produce rule-added lookup rows with original count0; revoking owner authority
fails atomically despite caller SELECT, while revoking caller lookup SELECT
does not deny owner-authorized actions. OLD-dependent FROM/USING remains caller
authorized. A successor lacking both privileges diagnoses log target permission
before lookup source permission, as independently observed on PostgreSQL18.0.

```sh
make check RUN='^(TestRuleCTE(ConflictInstantiation|CommandNormalization)|TestRuleExpansionSourceAuthority)$'
make check RACE=1 RUN='^(TestRuleCTE(ConflictInstantiation|CommandNormalization)|TestReferenceRuleReview3CTE(Reuse|Recovery|Sessions|Restore|Cardinality))$'
make check RACE=1 RUN='^(TestRuleExpansionSourceAuthority|TestReferenceRuleReview3(IndependentAuthority|InheritedAuthority))$'
```

Inherited security-invoker-view inputs retain caller authority through both
UPDATE and DELETE action recollection, not just composition/admission. The
same held query succeeds with caller input rights and owner sink rights, fails
atomically after caller revoke despite owner input grants, recovers after caller
grant, and still succeeds after owner input revoke. The canonical collection
unit also exercises nested invoker/definer, repeated occurrence, unset-principal
fallback and current-versus-retained paired catalog snapshots.

Decorated inference witnesses independently exercise matching/mismatching
operator classes and collations, alone and together, for immediate actions and
action-owned INSERT SELECT modifiers. Exact `42P10` diagnostics and no-effect
bags survive target-column rename, real native Dump/Restore, incompatible future
index replacement and repair through the SAME held consumer. The actual
export→parse→reanalysis unit retains child input versus outer NEW capture and
distinct SELECT frame ownership. Supported column indexes suffice for its
expression-key identity contrast; native UNIQUE expression indexes remain out
of scope. The combined standalone smoke runs native controls before explicit
ACL fixtures: arbitrary relation/column ACL export remains an existing refusal.

```sh
make check RUN='^TestRule(ViewSourceCollectionAuthority|InferenceDumpReanalysis)$'
make check RACE=1 RUN='^(TestRuleViewSourceCollectionAuthority|TestReferenceRuleReview4InvokerViewAuthority)$'
make check RACE=1 RUN='^(TestRuleInferenceDumpReanalysis|TestReferenceRuleReview4Inference(Matching|Mismatch))$'
```

View-attached ordinary-rule provider retirement uses a PostgreSQL-pinned domain
cast in NEW, not a table-column dependency. Direct and SAME held RESTRICT report
full `2BP01` diagnostics while preserving provider/rules/view/data and subsequent
execution. Native Dump/Restore retains that result; CASCADE emits the exact
single-rule notice and removes only the dependent DML rule, leaving `_RETURN`,
an unrelated ordinary rule and data intact. Future held INSERT uses ordinary
writable-view lowering after retirement. Supported typed cast behavior observes
provider presence/absence without requiring unimplemented `pg_type` enumeration.

```sh
make check RUN='^TestRuleViewExpressionDependencyClosure$'
make check RACE=1 RUN='^(TestRuleViewExpressionDependencyClosure|TestReferenceRuleReview5ViewProviderRetirement)$'
```

Named conflict targets have separate dependency and current-name witnesses.
The complete normal-edge bag references the actual class2606 constraint OID,
not a fixed allocator value or a normal backing-index edge. Direct/SAME held
RESTRICT preserves provider/rules/rows through native restoration; CASCADE
removes only the dependent rule and permits future original writes.
Rename/name-reuse cases cover DO NOTHING/EXCLUDED UPDATE, immediate actions and
real INSERT SELECT modifiers, retaining intended provider identity and exact
future bags through actual Dump/Restore.

Private units invoke canonical reference collection, closure/retirement and
actual export→parse→reanalysis plus collision reminting. They preserve recognized
exclusion and no-index diagnostics, ordinary inference's replaceable providers,
stored modifier/provider coherence and allocation-free unchanged paths.

```sh
make check RUN='^TestRuleNamedConstraint(Dependencies|Retirement|Kinds|Remap|DumpReanalysis)$'
make check RACE=1 RUN='^(TestRuleNamedConstraint(Dependencies|Retirement|Kinds|Remap)|TestReferenceRuleReview6Named(Dependency|Retirement))$'
make check RACE=1 RUN='^(TestRuleNamedConstraintDumpReanalysis|TestReferenceRuleReview6Named(RenameRoundTrip|NameReuseRoundTrip))$'
```



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
make check RUN='^TestReferenceViewCheck(Transactions|Cancellation|RuleBoundary|TriggerBoundary)$' REFERENCE=1
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
