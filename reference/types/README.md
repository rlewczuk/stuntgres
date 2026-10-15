# reference/types

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### Multirange constructor and expansion selected proof

The untagged `TestReferenceRangeMore` definitions specify unary generic
`multirange(anyrange)` and `unnest(anymultirange)` for the three executable
integer/numeric companion families. Independent expectations cover canonical
values, ascending raw member order, numeric gaps/scale, NULL/empty/universal,
direct/prepared/held typed binds, raw metadata on empty results, domains,
target/FROM/lateral/ordinality/zip/nesting, full English diagnostics, ACL
rechecks, stored recipes, statement atomicity and native control ownership.

```sh
make check RUN='^TestReferenceRangeMore(Values|Metadata|Binds|Composition|Variadic)$'
make check RUN='^TestReferenceRangeMore(Diagnostics|DiagnosticContracts|Authorization)$'
make check RUN='^TestReferenceRangeMore(Stored|Controls|Cancellation|FeatureSmoke)$'
make check RUN='^TestRangeMore'
```

Run each reference selector again with `REFERENCE=1`; all checks need external
900-second deadlines. Selected held-bind/control/cancellation race proof uses
`RACE=1`. Reserve quickcheck for whole-project Docker-free verification.
The oracle is the shared pinned PostgreSQL18.0 fixture, never a local server.
No discovery filters or observers belong in completed definitions.

The public executable feature smoke is maintained as the task's session source,
not a production helper or a second SQL harness. Run `go run` on its absolute
session path from the repository root. Quiet success requires the canonical
three-family constructor results, ascending prepared expansion, strict
NULL/empty distinctions, fresh held cursor state, exact diagnostics and
actual native controls; a mismatch exits nonzero.

### UUID extraction selected proof

The untagged `TestReferenceUUIDExtract` families specify PostgreSQL 18.0
`uuid_extract_timestamp(uuid)` / `uuid_extract_version(uuid)` outcomes.
Only RFC variant prefixes 8/9/a/b are accepted; all version nibbles 0–15 are
returned as INT2. Timestamp extraction supports v1 (100-ns ticks truncated
to microseconds) and v7 (Unix milliseconds), never v6; other versions and
unsupported variants return typed NULL. Timestamp metadata is TIMESTAMPTZ.
Shared scenarios cover direct/prepared/held binds, malformed-input recovery,
full independent English diagnostics, relation authorization, raw metadata,
generated/default/check/view/index recipes and native replacement controls.

```sh
make check RUN='^TestReferenceUUIDExtract(Values|Versions|Timestamps|Metadata|Binds)$' REFERENCE=1
make check RUN='^TestReferenceUUIDExtract(Diagnostics|Authorization|Cancellation)$' REFERENCE=1
make check RUN='^TestReferenceUUIDExtract(Stored|Controls|FeatureSmoke)$' REFERENCE=1
```

Run identical selectors without `REFERENCE=1` for native proof, with an external
900-second deadline for every invocation. Feature smoke is selected with
`make check RUN='^TestReferenceUUIDExtractFeatureSmoke$'` (also `REFERENCE=1`).
It expects four independently specified source rows, correct held-bind recovery,
timestamp boundary vectors, version/variant outcomes, denied reader access and
stored recipe round trips. Native-only phases exercise Dump/Reset/Restore and
actual emitted-SQL replay on PostgreSQL; they are not PostgreSQL control APIs.

### Range inspection selected proof

The shared untagged `TestReferenceRangeInspect` definitions cover the six
executable ranges and three integer/numeric multiranges: canonical bounds,
empty/omitted/explicit infinity/NULL distinctions, outer normalized members,
gap-filling hulls, numeric endpoint scale, exact scalar/range metadata,
direct/prepared/contextual/held binds, independent complete diagnostics,
table/column/schema authorization, ordinary cancellation and staged controls.
Inputs use existing declared-column and INSERT-destination authority, not
unsupported explicit range casts. Unknown string `lower`/`upper` remain TEXT;
all selected range overloads are strict and immutable.
`TestReferenceRangeInspectDiagnosticOrdering` pins direct/prepared lower/upper
named-argument and scalar-decoration errors for both text and typed range
overloads. Ordinary argument and FILTER children are transformed before
count/label checks, namespace lookup and signature/decorator rejection.
Malformed FILTER and child diagnostics are not discarded or rebound; ordinary
TEXT literal/NULL/parameter preference remains unchanged.

```sh
make check RUN='^TestReferenceRangeInspect(Ranges|Multiranges|Merge|Metadata|Resolution|Diagnostics|DiagnosticOrdering|Binds|Authorization|Controls|Cancellation|FeatureSmoke)$'
make check RUN='^TestReferenceRangeInspect(Ranges|Multiranges|Merge|Metadata|Resolution|Diagnostics|DiagnosticOrdering|Binds|Authorization|Controls|Cancellation|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestRangeInspect'
make check RUN='^TestReferenceRangeInspectFeatureSmoke$'
make check RUN='^TestReferenceRangeInspectFeatureSmoke$' REFERENCE=1
```

Use an external900-second deadline for selected checks; reserve `make quickcheck`
for whole-project Docker-free verification. Real mode uses the existing pinned
PostgreSQL18.0 fixture. Native-only control phases run real Dump/Reset/Restore
on same/fresh Drivers, hold old rows/metadata across successful/failed replacement,
and replay the actual emitted SQL on PostgreSQL when present.
Executable stored recipes cover CHECK/generated/views and nested inspectors.
Range DEFAULT expressions remain unavailable under existing cast/constructor
and default-column/parameter/subquery boundaries; no prerequisite is widened.
Leaf units cover exact generation authority, strict literal/runtime demand,
fresh retag/reconstruction, sealed members and allocation-free evaluation.
Computed call targets require unknown nullability; selected stored view,
generated and INSERT-SELECT destination columns independently require native
known nullable metadata. Pinned pq keeps unknown nullability in both cases.
These explicit source contracts apply during live reads and actual dump replay.
Feature smoke completes cancellation and authorization on their actual owned
relations before native control replacement. The ACL fixture is dropped before
Dump; shape-changing Restore and retained-reader assertions use the control
helper's separate target Driver, not an assumed relation in the source Driver.

### query_int selected proof

The seven untagged `TestReferenceQueryInt` definitions share public SQL/API
fixtures across native and pinned PostgreSQL. They independently assert
installation and generated identity relationships, canonical integer Boolean
query text, direct/prepared/held values, owned byte bindings, nil and typed-nil
NULLs, empty-byte failures, explicit character I/O, directional query_int-to-
TEXT/VARCHAR assignment (VALUES/SELECT/UPDATE) and rejected typed character
assignment into query_int, full diagnostics/stages/locations, raw metadata,
and statement atomicity.
Controls include pending readers/metadata, committed-only dumps, atomic
same/fresh-driver restore, SQL extension recreation and actual emitted-SQL
oracle replay. Native controls are explicit native-only subphases.

Run each selection from the repository root with an external900-second deadline:

```sh
make check RUN='^TestReferenceQueryInt(Installation|Grammar|Assignment|Diagnostics|Metadata|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceQueryInt(Installation|Grammar|Assignment|Diagnostics|Metadata|Controls|FeatureSmoke)$'
make check RUN='^(TestQueryInt(Input|Output|Identity|Coercion|Cancellation)|TestIntarrayProvider(Identity|Conflicts|Controls))$'
make check RUN='^TestReferenceAvailableExtensionsFiltering$'
make check REFERENCE=1 RUN='^TestReferenceAvailableExtensionsFiltering$'
make check RACE=1 RUN='^(TestQueryIntCancellation|TestIntarrayProviderControls|TestReferenceQueryInt(Assignment|Controls|Metadata))$'
```

The existing availability witness now asserts both actual native registered
providers (intarray and unaccent), not PostgreSQL's larger package inventory.
The genuine LANGUAGE internal/cstring helper conflict remains a PG native-ABI
witness; native provider units retain strict reciprocal membership and prove
published type conflicts, invalid-manifest rejection, nonaliasing remapping
and actual TEMP validation/retirement. No arbitrary SQL loader is introduced.
The declaration string-modifier syntax boundary is documented in
[DIALECT](../../docs/DIALECT.md#query_int-extension-type), distinct from query
text diagnostics. Flat OR15/16/17/32 succeeds;16 NOTs succeeds and17 fails.
Generated OIDs are checked relationally, not pinned to another allocator.

The standalone public executable is transient session material
`local://query-int-smoke.go`, not a parent deliverable or committed helper.
The root materializes it outside the checkout and runs:

```sh
timeout 900s go run "$SESSION_TMP/query-int-smoke.go"
```

Expected quiet exit0 with no stdout/stderr. It exercises literal and held
bindings (including buffer ownership and both NULL forms), raw direct/prepared
metadata, complete atomic parameter diagnostics, same-type copies, directional
TEXT/VARCHAR assignment through direct/prepared VALUES/SELECT/UPDATE (including
typed NULL), extension dump ordering, retained readers, Reset/Restore,
independent destination restore, failed replacement and coexistence with
unaccent. A passing test or smoke is
evidence only when the root records its actual execution.

### ACL item oracle-first selected proof

The ACL definitions share one SQL body across native and pinned PostgreSQL,
with backend subtests directly beneath each top-level family. Independent
expectations cover all fifteen privilege letters and grant options, canonical
quoting, PUBLIC versus NULL, scalar and array storage/binds/raw descriptors,
equality-only scalar admission, array bounds/shape/NULL equality, complete
input diagnostics and default-grantor notice phases, and retained role identity
through DROP/recreate. Native-only control phases check live-role lossless
Dump/Restore, independently advanced target role identities, atomic staged
failures, retained rows, ordinary cancellation/I/O failures, and target-role
replacement fencing. Stale ACL Dump rejects before writer I/O; ordinary stale
output and equality remain distinct from a numeric-looking live role.
`Definitions` exercises persisted defaults, checks, views and expression indexes,
including prepared future writes into an independently advanced restore target.
`StaleDefinitions` supplies no-row default/view/check/index/derived-partition
preflight witnesses. `DumpSnapshot` blocks a real writer, changes roles after
capture, and requires the original coherent dump followed by zero-I/O rejection
of a later stale snapshot. Native-control branches are explicitly native-only.
`Casts` covers explicit sized-character conversions, NULL, source/destination
domains and assignment-length rejection without mutation. `Deparse` pins public
scalar/NULL view definitions through direct/prepared, pretty/nonpretty and
literal/bound lookup routes; its SQL expectations require pinned oracle proof.

Completed definitions require selected pinned-oracle and native proof after
affected edits; selected proof does not replace whole-project `quickcheck`.
Generic component coverage retains checked ACL scalar/array carriers through
`any_value` and explicitly rejects ACL ordering rather than assuming every
concrete scalar is sortable. Grant options follow the preceding letter:
`wr**` canonicalizes to `r*w`, not `rw*`. Omitted-grantor results use the
independently specified bootstrap spelling (PostgreSQL fixture `stuntgres`,
native fixture `postgres`), without changing either backend's role setup.
Literal default-grantor warnings carry the enclosing literal Position;
bound-input warnings carry parameter Context instead. With an external
900-second deadline per command:

```sh
# Initial oracle-only discovery: /postgres selects immediate backend children.
make check REFERENCE=1 RUN='^TestReferenceACLItem(Values|Binds|Metadata|Comparison|Diagnostics|Notices|Identity|NativeControls|StaleControls|Definitions|StaleDefinitions|DumpSnapshot|Boundaries|Casts|Deparse|Smoke)/postgres$'
# The completed definitions must pass both modes after implementation.
make check RUN='^TestReferenceACLItem(Values|Binds|Metadata|Comparison|Diagnostics|Notices|Identity|NativeControls|StaleControls|Definitions|StaleDefinitions|DumpSnapshot|Boundaries|Casts|Deparse|Smoke)$'
make check REFERENCE=1 RUN='^TestReferenceACLItem(Values|Binds|Metadata|Comparison|Diagnostics|Notices|Identity|NativeControls|StaleControls|Definitions|StaleDefinitions|DumpSnapshot|Boundaries|Casts|Deparse|Smoke)$'
make check RACE=1 RUN='^TestACLItem(Identity|Codec|Equality|Constructor|RetainedIdentity|PrincipalRemap|RestoreFence|EqualityAdmission|StaleDump|RenderingAuthority)$'
make check RUN='^Test(AnyValueProviderDatums|OrderingUsage)$'
# Review regressions: oracle first, then observed native regression before remedy.
make check REFERENCE=1 RUN='^TestReferenceACLItem(Casts|Deparse)/postgres$'
make check RUN='^TestReferenceACLItem(Casts|Deparse)$'
# Feature smoke, not baseline-only success.
make check RUN='^TestReferenceACLItemSmoke$'
make check REFERENCE=1 RUN='^TestReferenceACLItemSmoke$'
```

Smoke requires scalar/array CREATE, literal and actual prepared bound INSERT,
canonical ordered values and metadata, scalar option-sensitive equality,
array equality/inequality, complete unknown-role diagnostics with unchanged
rows, and native Dump/Reset/Restore with held rows and deterministic redump.
Success is quiet exit 0 and the ordinary package summary after those assertions.

### Domain CHECK selected proof

Run each small group first against pinned PostgreSQL with `REFERENCE=1`, then
in native mode, with an external 900-second deadline per invocation:

```sh
make check RUN='^TestReferenceDomainCheck(Truth|NamesOrder)$' REFERENCE=1
make check RUN='^TestReferenceDomainCheck(Truth|NamesOrder)$'
make check RUN='^TestReferenceDomainCheckDiagnostics$' REFERENCE=1
make check RUN='^TestReferenceDomainCheckDiagnostics$'
make check RUN='^TestReferenceDomainCheck(ConversionsNull|Ancestors)$' REFERENCE=1
make check RUN='^TestReferenceDomainCheck(ConversionsNull|Ancestors)$'
make check RUN='^TestReferenceDomainCheck(InputRoutes|Metadata|NativeControls)$' REFERENCE=1
make check RUN='^TestReferenceDomainCheck(InputRoutes|Metadata|NativeControls)$'
make check RUN='^TestReferenceDomainCheckInputOrdering$' REFERENCE=1
make check RUN='^TestReferenceDomainCheckInputOrdering$'
make check RUN='^TestReferenceDomainCheckSQLExecute$' REFERENCE=1
make check RUN='^TestReferenceDomainCheckSQLExecute$'
make check RUN='^TestReferenceDomainCheckDemand$' REFERENCE=1
make check RUN='^TestReferenceDomainCheckDemand$'
make check RUN='^TestReferenceDomainCheckDemandWrappers$' REFERENCE=1
make check RUN='^TestReferenceDomainCheckDemandWrappers$'
make check RUN='^TestReferenceDomainCheck(PlanningDemand|RecipeInitialization)$' REFERENCE=1
make check RUN='^TestReferenceDomainCheck(PlanningDemand|RecipeInitialization)$'
make check RUN='^TestReferenceDomainCheck(ConflictProofDemand|AddColumnDemand|StoredDemand)$' REFERENCE=1
make check RUN='^TestReferenceDomainCheck(ConflictProofDemand|AddColumnDemand|StoredDemand)$'
make check RUN='^TestReferenceDomainCheckIndex(RuntimeDemand|LifecycleDemand|FailureDemand|ConflictDemand)$' REFERENCE=1
make check RUN='^TestReferenceDomainCheckIndex(RuntimeDemand|LifecycleDemand|FailureDemand|ConflictDemand)$'
make check RUN='^TestReferenceDomainCheckIndex(ProducerDemand|ProducerFailureDemand|ReturningProducerDemand|MutationProducerDemand|MergeProducerDemand|CopyProducerDemand|CopyInsertionOrderDemand|InsertionOrderDemand)$' REFERENCE=1
make check RUN='^TestReferenceDomainCheckIndex(ProducerDemand|ProducerFailureDemand|ReturningProducerDemand|MutationProducerDemand|MergeProducerDemand|CopyProducerDemand|CopyInsertionOrderDemand|InsertionOrderDemand)$'
make check RUN='^TestReferenceDomainCheckIndexCopyFlushDemand$' REFERENCE=1
make check RUN='^TestReferenceDomainCheckIndexCopyFlushDemand$'
make check RUN='^TestReferenceDomainDiagnostics$' REFERENCE=1
make check RUN='^TestReferenceDomainDiagnostics$'
```

The scenarios assert named/generated identities, three-valued CHECK truth,
ancestor-first and per-domain alphabetical order, declaration diagnostics,
conversion versus already-domain NULL bypass, atomic assignments, COPY and
nested input, direct/prepared base metadata, and CHECK-owned dependency
retirement. Native-only control phases additionally exercise Dump/Reset/Restore,
fresh-driver remapping, retained eager rows, and failed/canceled replay atomicity.
These definitions are contracts, not passing-verification claims.
Demand separates domain expression initialization from truth consumption:
invalid scalar casts behind false filters/empty sources remain unconsumed,
empty omitted-default INSERT affects no rows, and each consumed row executes
its volatile CHECK. Real SQL sequence rows/state and late-error atomicity are
the witnesses, not a mocked evaluator. Constant CHECK recipe errors still
initialize at Execute for an empty consumer; inferred domain input remains
immediate while explicit-base parameters and already-domain NULL preserve their
separate conversion/bypass contracts. The private supplement is selected by
`make check RUN='^TestDomainCheckPreflightDemandInternal$'`.
DemandWrappers exercises public direct/prepared abs, COALESCE, CASE and ANY
consumers behind false filters and empty sources, plus demanded failure/success
and already-domain NULL bypass. The shared conditional constant eligibility
used by these providers must preserve live checked conversions and enclosing
runtime barriers; a literal physical child is not a completed CHECK event.
PlanningDemand covers empty keyed FULL JOIN/MERGE scalar, array and conditional
casts without truth consumption, real FULL JOIN sequence demand, and MERGE's
separate ordinary BY SOURCE action-stage ON evaluation. After the earlier
observable nextval1, one FULL JOIN match ends at nextval3, while a matched
BY SOURCE MERGE ends at nextval4 and deletes its target. MERGE command counts
retain the existing lib/pq adapter contract (PG0/native actual1).
RecipeInitialization requires retained CASE/COALESCE/ancestor/array and stored
default recipes to report22012 at Execute even for false/empty consumers; removed
dead arms return1 without initializing their eliminated conversion.
ConflictProofDemand proves empty partial-index inference consumes no truth,
while an actual inserted index row fails23514 atomically.
AddColumnDemand proves fresh omitted NULL and invalid defaults are checked only
on actual old rows, failed schema/data publication permits same-column retry,
three checked rows draw three sequence values, an empty ADD draws none, and
retained recipe1/0 still initializes with22012 on an empty table.
StoredDemand checks generated and RANGE-partition initialization and actual input:
retained division-by-zero recipes reject CREATE with22012, publish no partial
table, and permit a same-name valid retry. IndexRuntimeDemand requires empty CREATE
to consume no truth, one INSERT effect and no COMMIT replay. IndexLifecycleDemand
covers B-tree/hash predicate/key and existing unique-predicate INSERT/UPDATE/DELETE,
duplicate23505 and rollback with retained sequence effects. IndexFailureDemand
requires failed UPDATE to draw only its genuine new-value effect before23514,
preserve old index/row data after rollback, and permit a later valid update.
IndexConflictDemand preserves distinct accepted ON CONFLICT precheck/insertion
events and duplicate existing-key rechecks: after initial sequence1, ordinary
INSERT ends at2, accepted ON CONFLICT at4, and duplicate DO NOTHING at5 for
predicate-only or6 for key/zero-width witnesses. COMMIT and zero-width DELETE
do not replay these effects. All SQL expectations are strict direct/prepared
dual-mode definitions; they are not proof that the current implementation passes.
HASH partial-predicate and UNIQUE expression-key/zero-width behaviors remain
PostgreSQL witnesses: the same definitions independently assert native0A000
with exact existing diagnostics and no partial row/effect publication. Native
artifact behavior uses admitted HASH derived nonpartial keys and UNIQUE simple
keys with a predicate; these definitions do not expand index providers.
ProducerDemand and ProducerFailureDemand distinguish actual per-row VALUES/query
insertion from later batch publication, with and without RETURNING. Mutation and
MERGE companions retain genuine new-version events and first-failure atomicity.
CopyProducerDemand independently retains PG18's eligible nextval-default COPY
flush phase: buffered defaults precede insertion checks; this is not the ordinary
INSERT producer schedule. InsertionOrderDemand uses reverse lexical index names
to prove creation/ObjectID order, exact first domain/constraint identity and
pre-error sequence effects. These strict expectations require pinned oracle and
native proof; equal final event counts alone do not establish correct ordering.
ReturningProducerDemand also makes RETURNING consume nextval, distinguishing
source/index/RETURNING events before the next source: stored IDs1,4 and returned
(1,3),(4,6), ending at6. CopyInsertionOrderDemand rejects tuple2's first index
after buffered defaults1,2 and tuple1's ordered checks3,4; exact curr5 and line2
prove row-outer/index-inner flush order and actual tuple diagnostic provenance.
CopyFlushDemand distinguishes the finite COPY tuple and server raw text-byte
boundaries: binary tuple1001 stores ID2001 after the preceding1000 checks, while
the third32768-byte text row stores ID5 after the preceding two checks. These
source-grounded strict traces also require their selected pinned/native proofs.
Native `TestDomainCheckIndexSnapshot` supplements exercise produced B-tree/hash
entry transport, selected live identities/positions, unchanged CHECK sequence
state, source immutability and missing/malformed/canceled snapshot rejection.

`TestReferenceDomainCheckIndexAliasSnapshotCollision` shares ordinary SQL for
extension-owned unaccent dictionaries and real `unaccent(text)` routine aliases
(REGPROC and REGPROCEDURE). Domain-wrapped keys and NULLs exercise a UNIQUE
B-tree and a separate HASH-only table, including direct/prepared equality,
UPDATE/DELETE/reinsertion and complete duplicate diagnostics. Domain-wrapped
oid_ops Detail contains the independently captured numeric represented identity,
not its symbolic spelling or a pinned allocator value. Its explicit
native control phase retains a TEMP sequence at the observed represented source
OID, restores the actual dump into another Driver and requires a reminted
dictionary/procedure identity while the retained sequence keeps its original
identity and state. The same SQL checks must still hold after replacement;
CHECK sequence frontier and byte-identical source dump guard against replay or
source mutation. Recursive private units distinguish equal numeric identities
in procedure/dictionary/unrelated catalog classes, nested domains, arrays,
composites, bounds and NULLs without adding compound index providers.
The key-output supplement requires physical OID diagnostics without changing
logical alias/domain keys, including the provider's unsigned INT4 bit semantics.

```sh
make check RUN='^TestReferenceDomainCheckIndexAliasSnapshotCollision$' REFERENCE=1
make check RUN='^TestReferenceDomainCheckIndexAliasSnapshotCollision$'
make check RUN='^TestDomainCheckIndexSnapshotAlias(Recursive|KeyOutput)$'
```



Private package supplements use
`make check RUN='^TestDomainCheck'` and
`make check RUN='^(TestDomain(ExcludedClausesNeverPublishInternal|BaseUsageAndOwnerAuthorizationInternal)|TestArrayCatalogAssociationPublicationAndOrigin|TestEnumCatalogStagingAndAuthority)$'`.
They exercise actual catalog staging, frame invariants, recipe/ancestry remapping,
fresh-input versus exact-identity coercion, nested codec completion, typmod
precedence and the allocation-free unconstrained path. They do not replace
selected public SQL proof in both modes. The Diagnostics group also checks
direct/prepared modifier failure stages, not just SQLSTATE.
InputOrdering checks lexical codec/CHECK precedence across multiple parameters
and array/composite members, including NULL. Member literal input fails at the
actual Prepare boundary, whereas inferred parameters and an outer domain
conversion fail at Execute. The literal decoder releases only its owning
binder's mutex around the captured codec/evaluator and revalidates the admitted
provider graph before resuming; it does not replay CHECKs or resolve new names.
SQL EXECUTE actual expressions enter and leave their frame importer unlocked.
The importer owns its narrow binding/capture lock, evaluates in the current
statement scope outside that lock and validates captured providers before
returning; failed CHECKs cannot strand the mutex or poison a later frame.
Former CHECK capability exclusions now exercise shared datetime extraction,
regexp conversion, binary COPY, foreign actions and composite member consumers:

```sh
make check RUN='^TestReference(DatetimeExtractDomainConstraintBoundary|RegexpStoredDefinitions|ForeignActionsConstraintsDomain|CompositeConstrainedDomainBoundary)$' REFERENCE=1
make check RUN='^TestReference(DatetimeExtractDomainConstraintBoundary|RegexpStoredDefinitions|ForeignActionsConstraintsDomain|CompositeConstrainedDomainBoundary)$'
make check RUN='^TestReferenceCopyBinaryCodecsNested$' REFERENCE=1
make check RUN='^TestReferenceCopyBinaryCodecsNested$'
make check RUN='^TestPreparedSQLActualNoSnapshotDemand$'
```


Feature smoke uses `make check RUN='^TestReferenceDomainCheck(Truth|ConversionsNull|Ancestors)$'`
and `make check RUN='^TestReferenceDomainCheckNativeControls$'`. Expected
observations include positive 1 accepted, 0 rejected with exact 23514 constraint
identity, UNKNOWN admitted, fresh NULL rejected by `VALUE IS NOT NULL` while
already-domain subquery NULL bypasses conversion, unchanged rows after failed
assignment, and identical post-roundtrip constraint behavior. A CHECK-only
provider cascade preserves the domain/data and removes only the dependent check.
Whole-project Docker-free verification remains `make quickcheck`.

### Composite expression selected proof

Run each small group in native mode and with `REFERENCE=1`, using an external
900-second deadline per invocation:

```sh
make check RUN='^TestReferenceCompositeExpr(Constructors|ConstructorCoercion|ConstructorDemand|Fields|Metadata|NullSemantics)$'
make check RUN='^TestReferenceCompositeExpr(ScopeAmbiguity|FunctionalNotation|Expansion|ValuesAndInsertExpansion|NestedStars|Returning|Diagnostics)$'
make check RUN='^TestReferenceCompositeExpr(Assignments|MixedArrayFields|MergeUpdate|AssignmentDiagnostics|AtomicityAndACL|Boundaries)$'
make check RUN='^TestReferenceCompositeExpr(StoredConsumers|ControlsAndLifetime|ControlRejection|FeatureSmoke)$'
make check RUN='^TestReferenceCompositeExprWholeRowAbsence$'
make check RUN='^TestReferenceMergeUpdateFields$'
make check RUN='^TestReferenceCompositeExpr(StoredFieldPruning|CorrelatedReturning|FunctionAlias)$'
make check RUN='^TestReferenceCompositeExpr(ZeroWidthValues|NamedAssignmentIdentity)$'
make check RUN='^TestReferenceCompositeExpr(VirtualBoundUpdate|VirtualBoundMerge|ViewTargetPaths|JoinTargetPaths)$'
make check RUN='^TestReferenceCompositeExprNamedTargetDiagnostics$'
make check RUN='^TestReferenceCompositeExprFunctionSourceCutover$'
make check RUN='^TestReferenceCompositeExprFunctionSourceRestore$'
make check RUN='^TestCompositeExpr(RecordShape|FieldAndNull|AssignmentPaths|VisitorsAndRetag|Parser)$'
make check RACE=1 RUN='^TestReferenceCompositeExpr(AtomicityAndACL|ControlsAndLifetime|ControlRejection)$'
```

The ACL witnesses keep actual column UPDATE/INSERT grants and denied explicit
field reads in both modes. Stored-field policy filtering is PostgreSQL-only;
native asserts the full existing unsupported policy-enforcement diagnostic,
then disables RLS to prove actual reader ACL projection. Schema USAGE grants/revokes are
PostgreSQL-only; the stored-reader native setup transfers public-schema
ownership for namespace visibility without transferring the table, then restores
ownership before dropping the role. Native schema-grant syntax stays unsupported.
Native control rejection asserts every diagnostic field using the actual appended
failing occurrence, never an earlier matching Dump substring. Stored computed
shapes retain anonymous occurrence ownership, contextual domain/typmod coercions
and exact leaf providers through genuine Dump/Restore; no generic RECORD storage
is admitted. INSERT VALUES expansion covers direct/prepared named targets and
identity-bearing destinations as well as INSERT SELECT.
Stored dot-builtin defaults retain their admitted notation through replay,
including composite/scalar operands and NULL `num_nonnulls`. Ordinary function
notation and generic DEFAULT capability boundaries remain unchanged.
Whole-row demand uses the existing copy-on-change expression visitor and the
side-aware RETURNING column wrapper; child binding errors propagate before
provider usage inspection. Retained consumer regression:
`make check RUN='^TestReferenceUpdateReturningExpressionsConsumerRoutes$'`
(also run with `REFERENCE=1`); its independent values/metadata remain unchanged.
StoredFieldPruning asserts direct/prepared generated/index admission, every
write's rows and untouched sequence, selected-volatility/immutable-preflight
errors, failed-name reuse, two native replacements and actual PostgreSQL SQL
replay with future writes. CorrelatedReturning asserts child/grandchild
transition frames, scalar stars/ROW, default versus member projections,
explicit aliases, nearer shadows, ambiguity, masked-name diagnostics and
mutation atomicity. INSERT field expansion retains the synchronized base's
already-supported INSERT RETURNING authority rather than an obsolete exclusion.
FunctionAlias distinguishes declared scalar function aliases from ordinality/
multiple-call tuples, actual NULL cells from missing tuples, top/scalar/nested
stars, explicit ROW, ordinary derived records and nearer column shadowing.
It asserts full scalar diagnostics and five actual frozen scalar/tuple views
through direct/prepared reads, two native replacements, emitted PostgreSQL SQL
replay and future null-extension rows. No new FROM provider is implemented.
ZeroWidthValues covers both width directions on direct Exec/Query and Prepare,
full positioned/unpositioned errors, unchanged destination/sentinel, same-connection
recovery and valid zero-column rows. NamedAssignmentIdentity distinguishes named
identity from contextual anonymous conversion, projected INSERT SELECT RECORD,
nested diagnostics, same-base/domain coercion and NULL versus anonymous
all-NULL reconstruction. No oracle-discovery filter remains in either definition.
VirtualBoundUpdate/Merge isolate virtual source reads solely in nested bounds,
with fresh held executions, missing-source NULL and recipe-error atomicity.
ViewTargetPaths independently asserts logical/physical slot and source-offset
remapping, ordered original-frame writes and recovered held handles.
JoinTargetPaths asserts planned FULL/nullable-rowmark errors even on empty
UPDATE/INSERT/conflict/MERGE input and valid fresh joined-bound writes.
NamedTargetDiagnostics independently checks full UPDATE/INSERT VALUES/INSERT
SELECT partial-target records for visible, hidden and shadowed quoted named
types and domains on direct Exec/Query and failed Prepare, unchanged nested
rows and a preheld valid handle. Invalid subscripting of selected composite/domain
members has no Position; plain scalar-column targets retain their independently
pinned head Position. Coercion failures retain the assignment head. Both backends
assert the same full diagnostic records without backend overrides or normalization.
FunctionSourceCutover applies the existing scalar-alias/true-tuple contract to
the admitted generic function stage: numeric, boolean, text and text-array
results, explicit ordinal/multiple-call tuples and nearer alias-column shadow.
No new function FROM provider is added.
Nested correlated sources retain packed occurrence layout and invocation flags,
including whole values consumed solely as function arguments and nullable
inherited prefixes; an empty physical source does not evaluate failing arguments.
FunctionSourceRestore independently checks declared scalar/tuple types and
NULL-versus-all-NULL values before genuine two-cycle native replacement, a fresh
driver with actual wrapped reads/unwrapped controls, retained original rows,
actual emitted PostgreSQL SQL replay and future matching/nonmatching writes.
The session-only public executable smoke separately exercises actual Dump,
Reset and Restore after constructors, partial writes, fields, prepared inputs,
stored-field admissions, correlated RETURNING transitions and declared function
alias/tuple consumers, with future generated writes, sequence-state checks and
frozen scalar-star/tuple replay. Root runs it as
`go run "$SESSION_TMP/composite-expr-smoke.go"`;
expected stdout is `composite expressions public smoke: PASS` and exit0.
Whole-project Docker-free verification remains `make quickcheck`, after edits;
selected checks and smoke readiness are not observed verification.

### Multirange selected proof

Permanent untagged `reference/types/multirange_*_test.go` scenarios cover all
three concrete companions through shared direct/prepared SQL, raw transports,
metadata, full diagnostics, ACLs and nested domains/column NOT NULL.
Oracle discovery filters are removed before native-red; they are not a final
capability distinction. Native process-local control subphases additionally
execute the actual emitted SQL on pinned PostgreSQL18.0.
Pinned lib/pq exposes blank multirange type names (not uppercase range names);
the helper retains logical family identity for raw/metadata checks, and public
pg_type/pg_range witnesses independently verify released OIDs/companion links.
Scale-boundary unions follow released strict selection rather than a containment
shortcut; fully comparator-equal different-scale duplicates have no stable tie
ordering promise.
The permanent `TestReferenceMultirangeComparisonBoundary` uses all three
companions and plain/nested domains with unequal ascending/reverse, equal and
NULL operands through direct/prepared SELECT. PostgreSQL's six supported scalar
comparison result matrices are independently specified; native must reject with
full42883/logical operand names/Hint/Position, at direct execution or Prepare.
No discovery filter survives native-red or final selected evidence.

Run each bounded group in both modes, pinned first for new expectations, under
an external900-second deadline:

```sh
make check REFERENCE=1 RUN='^TestReferenceMultirange(Values|Binds|Metadata)$'
make check RUN='^TestReferenceMultirange(Values|Binds|Metadata)$'
make check REFERENCE=1 RUN='^TestReferenceMultirange(Diagnostics|Modifiers|InvalidSQL|Authorization)$'
make check RUN='^TestReferenceMultirange(Diagnostics|Modifiers|InvalidSQL|Authorization)$'
make check REFERENCE=1 RUN='^TestReferenceMultirange(Domains|FeatureSmoke)$'
make check RUN='^TestReferenceMultirange(Domains|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceMultirange(Controls|DomainControls)$'
make check RUN='^TestReferenceMultirange(Controls|DomainControls)$'
make check REFERENCE=1 RUN='^TestReferenceMultirangeComparisonBoundary$'
make check RUN='^TestReferenceMultirangeComparisonBoundary$'
make check RUN='^TestMultirange'
make check RUN='^TestComparison'
make check RUN='^TestAnyValueProviderDatums$'
make check RACE=1 RUN='^TestReferenceMultirange(Binds|Controls|DomainControls|ComparisonBoundary)$'
make check REFERENCE=1 RUN='^TestReferenceIntegerRange'
make check RUN='^TestReferenceIntegerRange'
make check REFERENCE=1 RUN='^TestReferenceNumericRange'
make check RUN='^TestReferenceNumericRange'
make check RUN='^Test(IntegerRange|NumericRange)'
```

The public standalone source `local://PG18-TYPE-MULTIRANGE-smoke.go` is an OMP
session artifact, not a committed executable. Root materializes
`.multirange-feature-smoke/main.go`, runs
`timeout 60s go run ./.multirange-feature-smoke`, records argv/cwd/exit and exact
observations, then removes the temporary materialization. It asserts three-family
canonical unions, gaps/NULL/empty/universal, binds/raw metadata/diagnostics/
atomicity, real same/fresh Driver controls and nested-domain constrained replay
with held-reader shape retention and direct/prepared scalar-comparison admission
rejection for builtin/plain/nested-domain operands. Success is quiet exit0, not a printed label or
test-shelling. Root runs whole-project Docker-free quickcheck after edits and
post-integration finalcheck; those do not replace selected dual-mode evidence.

### Enum support functions

Enum support-function public witnesses use the shared direct/prepared definition:
`make check RUN='^TestReferenceEnumFunctions'` and
`make check REFERENCE=1 RUN='^TestReferenceEnumFunctions'`.
Standalone feature smoke uses `RUN='^TestReferenceEnumFunctionsFeatureSmoke$'`
in both modes. It checks typed NULL extrema, inclusive/NULL-bounded computed
arrays, metadata/raw transport, exact mismatch diagnostics and genuine native
stored-definition/eager-value controls. Private provider/retag units supplement
these witnesses with
`RUN='^(TestEnumSupportFunctions|TestEnumFunctionRetagAndStoredProviders|TestQueryTableCreateEnumArrayOutputBoundary|TestQueryTableCreateStructuralOutputWrappers)$'`.
The selected admission regressions use
`RUN='^TestReferenceEnumFunctions(StableAdmission|StorageBoundaries)$'` in
both modes. They independently assert STABLE generated-column/index-expression/
index-predicate rejection and the bounded native enum-array storage exclusion.
PostgreSQL storage succeeds, including query-derived and explicit enum arrays;
its successful stored values and NO DATA behavior are asserted separately from
native capability diagnostics. Computed ranges, ordinary views, projected
domain-over-enum arrays and non-enum array storage remain positive controls.
Derived native storage and explicit annotation/domain exclusions exercise direct
and prepared routes with full diagnostics and no partial publication. The
unpopulated PostgreSQL materialized view uses the shared full55000 diagnostic
and REFRESH hint, then refreshes successfully. Positive view metadata retains
declared native nullability rather than computed-expression nullability.

### Predefined timestamp domain

The shared predefined-domain witnesses select
`RUN='^TestReferenceInfoTimeStamp(Values|Metadata|Diagnostics|Identity|NativeControls|FeatureSmoke)$'`
with both `make check` and `make check REFERENCE=1` (900 seconds each).
Private descriptor/catalog/remap units select
`RUN='^TestInfoTimeStamp(BuiltinDescriptor|CatalogIdentity|CatalogRemap)$'`.
Consumer carrier coverage also selects `RUN='^TestAnyValueProviderDatums$'`:
the newly concrete builtin domain supplies finite p2/carry and nonfinite datums,
checks its domain/base tags and retains the exact chosen datum through aggregation.
Feature smoke selects `RUN='^TestReferenceInfoTimeStampFeatureSmoke$'` in both modes.
Pinned PostgreSQL18.0 supplies independent p2 values, TIMESTAMPTZ transport and
metadata, complete base diagnostics and catalog/default controls. Native APIs
add actual qualified Dump/Reset/Restore, retained cursors and failed replacement
atomicity; PostgreSQL has no corresponding process-local Restore API.
Cache ColumnTypes while a cursor is open to check retained descriptors after
EOF/Close. Pre-canceled pq reads can race database/sql's watcher: validate available
metadata/emitted ordered prefixes, permit ordinary cancellation/ErrBadConn, and
require full rows on clean EOF. Native pre-cancellation requires context.Canceled
without a cursor. Separate cancellation handles preserve an untouched write
atomicity observer. This adapter distinction never weakens domain SQL diagnostics.
Native inherited-domain defaults, sibling-domain/implicit-array executability
and general information-schema/pg_type views are explicit exclusions.

### INET and CIDR

INET/CIDR scalar acceptance uses `make check RUN='^TestReferenceINET.*$'`
and `make check REFERENCE=1 RUN='^TestReferenceINET.*$'` (external900-second
deadlines), plus codec/coercion/ordering units with
`make check RUN='^TestNetwork.*$'`.
Feature smoke selects `^TestReferenceINETFeatureSmoke$` in both modes.
The definitions independently assert exact values/types/counts, full
diagnostics/stages, raw transport and empty/all-NULL metadata, held binds,
statement atomicity and real native Dump/Reset/Restore with PostgreSQL replay.
Native control API/cursor phases are explicitly native-only, not a fabricated
PostgreSQL equivalent. The oracle-first discovery filter is not permanent.
A separate throwaway public executable additionally exercises prepared binds,
prefix-aware ordering/casts, diagnostics, metadata and real control round trips;
its temporary main package is not committed or substituted by a test.

### Bitstrings

The bitstring family covers independently pinned BIT/VARBIT values, raw scanner
and modifier diagnostics, direct/held parameters, assignment atomicity, complete
metadata and retained ownership, plus native Dump/Reset/Restore with PostgreSQL
replay of the actual emitted SQL. The expanded feature smoke combines the compact
scenario with stored-format replay/default demand, compound/simple-parenthesized
modifiers, differing-width repeated slots and held parameterized cursor snapshots.
Run each invocation under the quickcheck900-second deadline, quietly:

```sh
make check RUN='^TestReferenceBitstring'
make check REFERENCE=1 RUN='^TestReferenceBitstring'
make check RUN='^TestReferenceBitstring(FeatureSmoke|StoredFormatReplay|ModifierExpressions|RepeatedVaryingWidths|HeldSelectArguments)$'
make check REFERENCE=1 RUN='^TestReferenceBitstring(FeatureSmoke|StoredFormatReplay|ModifierExpressions|RepeatedVaryingWidths|HeldSelectArguments)$'
```

Both modes are required before review/integration. Finalcheck remains one
1800-second gate after merge. Discovery-only deliberate observation failures
and PG-only filters must be removed before the native failing proof and gates.

### NUMRANGE oracle-first contract

`TestReferenceNumericRange` definitions independently specify continuous numeric
bounds, exact scale-preserving output, empty/unbounded/NULL distinctions, explicit
numeric infinities and NaN, direct/prepared binds, metadata, full diagnostics,
statement atomicity and already-admitted plain/nested domain composition.
The control scenarios use public native Dump/Reset/Restore and replay actual
emitted SQL on pinned PostgreSQL; PostgreSQL has no native-control API analogue.
The feature smoke is `TestReferenceNumericRangeFeatureSmoke`.

The oracle-first Round A definitions were introduced before production changes.
The temporary discovery filter was removed before native red. Completed definitions
now share `reference/types/range_helpers_test.go` with the integer-range scenarios:
actual NUMRANGE/INT4RANGE/INT8RANGE raw values and metadata are asserted before
normalization. Pinned PostgreSQL18.0 grounds expected diagnostics and phases;
never learn golden values from native output.

Run from the assigned worktree, with an external900-second deadline per command:

```sh
make check RUN='^TestReferenceNumericRange' REFERENCE=1
make check RUN='^TestReferenceNumericRange'
make check RUN='^TestReferenceNumericRangeFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceNumericRangeFeatureSmoke$'
```

Because common range helpers are consolidated, both selected modes must additionally
cover `^TestReferenceIntegerRange`. Reserve `make quickcheck` for completed
whole-project Docker-free verification. Standalone smoke source
is supplied in the OMP session as `local://numrange-smoke.go`: materialize it
temporarily as `.numrange-feature-smoke/main.go`, run
`timeout 60s go run ./.numrange-feature-smoke`, record observations and remove
the materialized source. Success is quiet exit0; failure reports an actual mismatch.

### DATERANGE contract and selected proof

Untagged `TestReferenceDateRange` definitions independently assert civil-day
canonicalization, finite DATE extrema/BC/leap boundaries, explicit infinity
versus omission/empty/NULL, input and held binds, raw string/byte transport and
metadata lifetime, complete diagnostics/phases and statement atomicity.
DateStyle-sensitive input/output and retained contextual literal capture reuse
the DATE environment. Relative endpoint expectations derive day D from scalar
DATE in the same transaction, without a second wall-clock observation.
Ordinary/nested domains, column NOT NULL and named-composite fields retain the
same datum. Native control phases perform actual same/fresh Driver cycles,
deterministic redumps, failed staged replacement and populated-reader shape
replacement, then replay the actual emitted SQL on pinned PostgreSQL.
PostgreSQL has no native Reset/Dump/Restore API; those phases are explicit.
No discovery filters remain. No timestamp range, datemultirange, SQL comparison,
range array or new aggregate admission is implied.
Computed composite-field projections independently assert unknown native
nullability (false/false), not their member declarations; native ANY_VALUE
remains known nullable. String-literal modifiers retain the existing native
grammar rejection at the string token (Prepare for prepared execution), while
PostgreSQL rejects unsupported daterange modifiers at execution with the exact
type-name diagnostic. Supported numeric/raw modifier forms retain descriptor
rejection before conversion. The declaration resolver retains qualified type-name
spelling for structured no-modifier diagnostics only when the declaration has an
explicit schema. Unqualified aliases and lowered declarations retain descriptor
canonical diagnostic names, including SERIAL's integer/bigint primitives. The
descriptor remains the support authority. This adds no modifier grammar.

Each command has an external900-second deadline; run each group in both modes:

```sh
make check RUN='^TestReferenceDateRange(Values|CanonicalBoundaries|Infinity)$' REFERENCE=1
make check RUN='^TestReferenceDateRange(Values|CanonicalBoundaries|Infinity)$'
make check RUN='^TestReferenceDateRange(Inputs|Binds|Metadata)$' REFERENCE=1
make check RUN='^TestReferenceDateRange(Inputs|Binds|Metadata)$'
make check RUN='^TestReferenceDateRange(Diagnostics|Modifiers|InputBoundaries)$' REFERENCE=1
make check RUN='^TestReferenceDateRange(Diagnostics|Modifiers|InputBoundaries)$'
make check RUN='^TestReferenceDateRange(Environments|RelativeCapture)$' REFERENCE=1
make check RUN='^TestReferenceDateRange(Environments|RelativeCapture)$'
make check RUN='^TestReferenceDateRange(Controls|DomainControls|Composition)$' REFERENCE=1
make check RUN='^TestReferenceDateRange(Controls|DomainControls|Composition)$'
make check RUN='^TestReferenceDateRangeFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceDateRangeFeatureSmoke$'
make check RUN='^TestDateRange'
make check RUN='^TestAnyValueProviderDatums$'
```

Shared range adapter/codec signatures also require the selected integer,
numeric-range and multirange value/bind/metadata/domain-control definitions in
both modes. Binary transport proof belongs to `reference/dml` and reuses its
existing stream fixture (`^TestReferenceDateRangeBinary$`, both modes).
Reserve quickcheck for the whole-project Docker-free gate, not selected proof.

The additive standalone public-API smoke is `local://daterange-smoke.go`, outside
the parent deliverables. Root materializes it in an owned temporary .go file
outside the module, then runs from the worktree:
`go run /tmp/PG18-TYPE-RANGE-DATE-daterange-smoke.go` (or its allocated absolute
path), with an external900-second deadline. Expected exit0 and stdout:
`daterange smoke: canonical days, infinity/unbounded, NULL, metadata, atomic diagnostics, nested domains, Dump/Reset/Restore and held snapshots passed`.
Record actual argv/cwd/exit/output and remove only the owned temporary file.


### TSRANGE oracle-first contract

The timestamp-range definitions specify continuous timestamp-without-time-zone
bounds: finite output is quoted, open adjacent-microsecond bounds stay nonempty,
and explicit infinity/inclusivity remains distinct from omitted bounds, empty
and NULL. Independently specified expectations cover scalar endpoint rounding and
extrema, ignored-but-validated written zones, DateStyle, frozen prepared literals
versus fresh relative/ambiguous binds, raw builtin/plain/nested-domain metadata,
all logical error fields and stages, failed-handle behavior and late-tuple
atomicity. Named-composite projections and the deterministic one-nonNULL
ANY_VALUE carrier retain existing composition semantics.

Run these small selections with an external900-second deadline each:

```sh
make check RUN='^TestReferenceTimestampRange(Values|Metadata|Diagnostics|Modifiers|Environments|RelativeCapture)$' REFERENCE=1
make check RUN='^TestReferenceTimestampRange(Binds|DomainControls|Composition)$' REFERENCE=1
make check RUN='^TestReferenceTimestampRange(Values|Metadata|Diagnostics|Modifiers|Environments|RelativeCapture)$'
make check RUN='^TestReferenceTimestampRange(Binds|DomainControls|Composition)$'
make check RUN='^TestReferenceTimestampRange(Controls|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceTimestampRange(Controls|FeatureSmoke)$'
make check RUN='^TestReferenceDateRange(Controls|DomainControls|Composition|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceDateRange(Controls|DomainControls|Composition|FeatureSmoke)$'
make check RUN='^Test(TimestampRange|DateRangeCanonicalDatum|AnyValueProviderDatums)'
```

`Composition` also selects the binary COPY definition in `reference/dml`,
which checks handwritten network bytes, flags and timestamp microseconds using
the existing stream fixture. These are permanent shared scenarios, not native
goldens. Pinned18.0 discovery confirmed the value, diagnostic, stage, environment,
bind and composition expectations. Builtin/plain/nested-domain pq metadata uses
TSRANGE, interface{} ScanType and unavailable length/precision/nullability.
The empty held-result expectation uses an empty row slice, not nil; this corrects
the discovery harness without changing SQL expectations. All scenarios visit both
backends without discovery logging or filters. Full-record/stage mismatches
remain nonfatal within each diagnostic route, preserving complete diagnostics.
`Controls`, `DomainControls` and `FeatureSmoke` share `range_control_helpers_test.go`
with date ranges: actual same/fresh Dump/Reset/Restore, canonical ISO under a
nonISO session, actual emitted PostgreSQL SQL replay, byte-identical redump,
failed/canceled replacement and retained populated builtin/nested-domain readers
and metadata through a TEXT-shaped replacement. Native-only controls are explicit
subphases; ordinary SQL and independent assertions remain shared across modes.

The standalone public executable is session material
`local://tsrange-feature-smoke.go`, not a parent deliverable or committed wrapper.
Root materializes it as `./.tsrange-feature-smoke/main.go` and runs
`timeout 60s go run ./.tsrange-feature-smoke` from the worktree. Expected exit0,
stdout exactly `tsrange public smoke: PASS`, and no stderr. It checks new continuous
timestamp behavior, binds/metadata/capture/full atomic failures and nested-domain
controls with held readers, not merely startup. Remove the owned temporary source
after recording actual observations. No passing native proof is claimed here.

`TestReferenceTimestampRangeComparisonBoundary` distinguishes storage from SQL
operator authority. PostgreSQL's independently specified Boolean/NULL results
cover all six comparisons, unequal/equal finite ranges and empty ranges,
builtin/plain/nested-domain columns, contextual strings and NULL/binds in either
operand position, zero-row/all-NULL analysis, and a WHERE equality predicate.
Native must reject analysis with the established full42883 unsupported-provider
diagnostic, not silently compare distinct datums as equal. Direct execution and
Prepare are both covered. Pinned18.0 confirmed the independent PostgreSQL results;
the completed definition visits both backends without a discovery filter.

```sh
make check RUN='^TestReferenceTimestampRangeComparisonBoundary$' REFERENCE=1
make check RUN='^TestReferenceTimestampRangeComparisonBoundary$'
make check RUN='^TestTimestampRangeComparisonAdmission$'
```

The standalone smoke also asserts complete42883 diagnostics for all six scalar
comparisons on builtin/plain/nested-domain storage, contextual strings/binds and
NULL/zero-row analysis. Direct Exec/Query and failed Prepare expose no result,
cursor or statement. Rejected UPDATE predicates preserve rows and a byte-identical
Dump, proving the boundary rejects before mutation rather than evaluating a
constant comparison result.


### TSTZRANGE oracle-first contract

`TestReferenceTimestamptzRange` scenarios independently assert continuous UTC
endpoint ordering, equal-instant singleton/empty normalization, inclusivity,
adjacent microseconds, infinity versus omission and NULL. They exercise direct
and held text/byte binds, raw TSTZRANGE metadata for builtin/plain/nested domains,
complete grammar/subtype diagnostics and eager failure/late-tuple atomicity.
TimeZone/DateStyle capture includes frozen literals versus fresh binds, DST gap
and fold choices, offset seconds, all four output styles and transaction-relative
input. Named composites and handwritten binary COPY reuse existing providers.

Native control phases reuse the shared cycle: source New York/German presentation,
target Tokyo/SQL presentation, actual ISO UTC Dump replay, deterministic redump,
same/fresh Driver Restore, cancellation/I/O and failed staging, retained populated
readers/metadata and successful TEXT-shaped replacement. Actual emitted SQL is
replayed on pinned PostgreSQL in real mode. No discovery filters remain.
Comparison scenarios independently assert real PostgreSQL BOOL results and the
documented native42883 admission boundary, including NULL and empty relations.

```sh
make check RUN='^TestReferenceTimestamptzRange' REFERENCE=1
make check RUN='^TestReferenceTimestamptzRange'
make check RUN='^TestTimestamptzRange'
make check RUN='^TestReferenceTimestamptzRangeFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceTimestamptzRangeFeatureSmoke$'
```

Use external900-second deadlines. Feature smoke asserts UTC ordering, continuous
binds/NULL, frozen literals/fresh NY binds, exact malformed/reversed/NOTNULL atomic
errors, nested-domain metadata and actual nonUTC control lifecycle. Pinned18.0
initial discovery passed before implementation; native/final dual-mode proof is
root-owned and is not implied by these commands or readiness.


### Composite attribute evolution selected proof

Run each selected group without and with `REFERENCE=1` (external 900-second
deadline); real mode also executes unchanged actual native Dump bytes on PostgreSQL:

```sh
make check RUN='^TestReferenceCompositeAlter(Values|Arrays|Descriptor)$'
make check RUN='^TestReferenceCompositeAlter(StoredDependencies|Diagnostics|Authorization)$'
make check RUN='^TestReferenceCompositeAlter(Prepared|Atomicity|Controls|DumpReplay)$'
make check RUN='^TestReferenceCompositeAlter(FunctionalPrepared|MaterializedDependencies|DomainChecks|InheritedChecks)'
make check RUN='^TestCompositeAlter(FunctionalRetainedFields|MaterializedStoredGuard|DomainCheckPruning|InheritedCheckStageAuthority)'
make check RUN='^TestReferenceTypedTableCompositeAlter(Conversions|Modes)$'
make check RUN='^TestReferenceCompositeExprBoundaries$'
make check RUN='^TestReferenceTypedTableNestedStoredBoundary$'
make check RUN='^TestComposite(ArrayEvolution|ArrayCodec|ArrayStorageAdmission|DomainArrayGenerationOwnership|AlterDescriptorCandidate|AlterCancellationAndReplacement)$'
make check RACE=1 RUN='^TestCompositeAlterCancellationAndReplacement$'
```

Independent observations: appended scalar/array members are NULL, rename retains
identity, drop/re-add never revives a value, dimensions/lower bounds and NULL
elements survive nested/domain generation mapping. Domain-array units stage and
publish actual declarations/holder rows, then verify stable identities and
reciprocal codecs through evolution and catalog replacement. Malformed bound array
input asserts the exact portal parameter Context in both modes. Storage-free
descriptor TYPE does not require Assignment, but actual typed roots/descendants still do.
RESTRICT and stored TYPE fences preserve the entire old state; actual dependent
field CHECKs are diagnosed or removed by CASCADE. Held field/expanded queries keep
original slots/names and return NULL for a dropped slot; named outputs use the
current descriptor, held literals retain original input shape and current binds
decode freshly. Eager rows remain immutable. Native controls retain final fields,
arrays, constraints, typed attachment and future writes; malformed/canceled imports
cannot publish a prefix. Composite comparison/hash/index providers remain absent.
The index-consumer native phase asserts the existing qualified type-name provider
boundary, while PostgreSQL creates a real dependent expression index.

Review-regression feature smoke uses the FunctionalPrepared, MaterializedDependencies,
DomainChecks and InheritedChecks selector above in both native and `REFERENCE=1`
modes. Expected observations: original held dot/functional TEXT slots become NULL
after DROP and stay NULL after re-add, including zero-member composites; actual
materialized scalar/array/domain/nested storage rejects explicit identity/widening
TYPE even empty/all-NULL and with CASCADE. Domain field CHECK RESTRICT diagnostics
and CASCADE notices name the actual constraint; unrelated CHECK enforcement and
outermost nested-domain diagnostics survive. PostgreSQL asserts its exact domain
constraint catalog bag; native independently asserts the documented absent domain
projection and real before/after enforcement. Internal units prove actual OID
pruning and immutable descriptors, not an empty-catalog proxy. Typed child/diamond
CHECK removal survives later propagation, fresh/Reset Restore and actual unchanged
native Dump replay on pinned PostgreSQL, with future accepted/rejected writes.
Ordinary inherited ALTER and absent comparison/index providers remain unchanged.


