# reference/indexes

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### Partial-index type and window boundaries

`TestReferencePartialIndexTypeGeneration` composes the same supported domain/
associated-array displacement with a simple-key partial UNIQUE index whose
predicate is the sole changed stored type consumer. Direct and held routes assert
stable array OID, renamed current metadata, exact rows and member23505,
outside-array admission, failed UPDATE atomicity and retained native cursor
metadata/content. It supplements, not replaces, the complete Regtype family:

```sh
make check RUN='^TestReference(PartialIndexTypeGeneration|RegtypeArrayDisplacement.*)$'
make check REFERENCE=1 RUN='^TestReference(PartialIndexTypeGeneration|RegtypeArrayDisplacement.*)$'
make check RUN='^TestPartialIndexPredicateTypeGeneration'
```

`TestReferencePartialIndexWindowDiagnostics` retains direct/held and UTF8-position
routes for window functions in partial predicates: full42P20 predicate-context
diagnostics, no-OVER42809 precedence, no effects and same-name supported recovery.
Keep the complete QueryWindow family selected after shared-comparator/binder
changes. Broad structural pg_index fields remain internal; the narrow SQL
projection does not expose expression/predicate trees. Direct
`TestPartialIndexMetadataPredicate` consumers assert exact current/retained
truth/membership and original attribute dependencies despite simplification.

```sh
make check RUN='^TestReference(PartialIndexWindowDiagnostics|QueryWindow)'
make check REFERENCE=1 RUN='^TestReference(PartialIndexWindowDiagnostics|QueryWindow)'
make check RUN='^TestPartialIndexMetadataPredicate'
```

### Concurrent indexes

Concurrent index scenarios use `^TestReferenceConcurrentIndex` for the complete
writer/snapshot/lifecycle/diagnostic/control family, and
`^TestReferenceConcurrentIndexFeatureSmoke$` for the feature smoke.
`^TestReferenceConcurrentIndex(TypeReconstruction|KeyLockEligibility)$` selects
the added independent TYPE/private-prefix and row-key-lock consumer contracts
for the root's real-first observation pass.
`^TestReferenceConcurrentIndexHash$` selects inherited hash-method lifecycle,
first/later writer admission, actual flags and equality results, unsupported
hash UNIQUE full diagnostics, and emitted Dump/Restore/replay coverage. It is
also included in the concurrent feature smoke. Preserve and run the integrated
`^TestReferenceHashIndex` family alongside it; the hash merge cancellation
calibration migrates to the actual descriptor/readiness authority without
changing its expected results.
`^TestReferenceConcurrentIndexHashInvalidQuery$` exercises a canceled ready-invalid
hash build and a subsequent committed grandfathered row. Its independent INT4
equality/heap results and REINDEX recovery close query-consumer eligibility.
The supplemental `TestConcurrentIndexHashInvalidQueryCapture` uses actual
incomplete buckets and matching storage identity to prove invalid capture is
rejected, heap filtering returns the row, and healthy reconstruction is selected.
Both regressions preserve pre-ready admission rather than repairing the fixture.
Run each selector with both `make check RUN='...'`
and `make check REFERENCE=1 RUN='...'`.
Affected ordinary-route regression selectors additionally include
`^TestReferenceBtreeIndex(OrdinaryShareBuild|ExcludedDefinitionsDiagnostics)$`
and `^TestReferenceRelationDeadlock`.
PostgreSQL queue/progress observations use actual lock/PID identities. Native
ordinary pending-Share/later-writer queue ordering is proved by internal lock
units: no public native queue catalog or worker-start timing is substituted.
The discovery-only pg-only filter has been removed: every final shared backend
loop discovers native and PostgreSQL scenarios according to the standard mode.
Native U1–U7 use `make check RUN='^TestConcurrentIndex'`;
parser ordinary/concurrent heads use the same RUN selector.
Each selected check invocation has one 900-second deadline. Root owns parser
generation, formatting and verification after all implementation edits land;
agents do not run mid-flight checks. Final post-merge finalcheck uses one
1800-second deadline. These commands are required inputs, not reported passes.

The current-tip ordinary UNIQUE/NND integration retains its independent shared
`^TestReferenceUniqueIndex` family in both modes. The added
`^TestReferenceConcurrentIndexNullsNotDistinct$` definition covers first-build
NULL duplicates, real old-snapshot admission, Ready-before-Valid NULL enforcement,
atomic INSERT/UPDATE, actual emitted Dump/native Restore/unchanged PostgreSQL
replay and default/explicit DISTINCT controls. Run this exact selector real-first,
then natively, each under `timeout 900s make quickcheck`; it is also in feature
smoke. Public ordinary provider exclusions remain admission policy; isolated
Restore reconstructs emitted computed definitions using existing authenticated
destination authority, not a UNIQUE parser option.

### Index clauses

The index-clause feature smoke exercises mixed keys, an existing scalar expression
key with explicit owned collation/class and INCLUDE, prepared uppercase projection,
payload/derived-key maintenance, the total 32/33 boundary and native controls with
PostgreSQL replay of the actual emitted native SQL. Compact `pg_get_indexdef`
assertions cover default/provider spelling, the INCLUDE ordinal boundary and
post-TYPE state without changing canonical Dump/replay authority:

```sh
make check RUN='^TestReferenceIndexClausesFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceIndexClausesFeatureSmoke$'
```

This smoke does not replace the complete identical dual-mode family selection
documented in [the index-clause design](../../docs/design/PG18-INDEX-CLAUSES.md).

### Index properties

The index-property smoke checks the eleven independently expected BOOL values
for B-tree AM/index capabilities, mixed DESC/NULLS ordering and INCLUDE roles,
plus prepared true/false/NULL and explicit native Reset/Restore/foreign restore:

```sh
make check RUN='^TestReferenceIndexPropertiesFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceIndexPropertiesFeatureSmoke$'
```

Run both with a900-second deadline. The full `^TestReferenceIndexProperties`
selector covers every new definition; see [the inquiry design](../../docs/design/PG18-FUNC-INFO-INDEX-PROPERTIES.md)
for the shared-coercion consumer risk selectors. Smoke does not replace those
identical dual-mode selective runs, ordinary quickcheck or post-merge finalcheck.

### Hash indexes

The hash-index feature smoke includes duplicate/NULL/range/order results, genuine
held probes with fresh parameters, post-Prepare mutation, method capabilities and
full diagnostics. Its controls contract exercises actual native Dump/Restore,
self/foreign replacement, retained TEMP/eager rows, failed late replay and real
PostgreSQL consumption of the unchanged native SQL. Public results alone cannot
prove indexing, so also run the package-function smoke that asserts the captured
real root, duplicate bucket candidates and consumption by the original filter:

```sh
make check RUN='^TestReferenceHashIndexFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceHashIndexFeatureSmoke$'
make check RUN='^TestHashIndexLookupFeatureSmoke$'
```

Each selected check has a900-second deadline. Full changed-definition selectors must
run identically in both reference modes before review/integration:

```sh
make check RUN='^TestReference(HashIndex|Unique|IndexClauses|BtreeIndex|ExpressionIndex|IndexMaintenance|IndexProperties|DeparseRelationsIndexForms|AlterTypeCompatiblePlainIndexBoundary)'
make check REFERENCE=1 RUN='^TestReference(HashIndex|Unique|IndexClauses|BtreeIndex|ExpressionIndex|IndexMaintenance|IndexProperties|DeparseRelationsIndexForms|AlterTypeCompatiblePlainIndexBoundary)'
```

`TestReferenceHashIndexBuildAdmission` prepares the actual held CREATE INDEX
while both SQL timers are disabled, then arms the original50ms lock/statement
limits immediately before execution. Exact55P03/57014 diagnostics must occur at
Execute, never Prepare; failed builds leave no index and the same owner later
builds successfully after the writer commits or rolls back. This avoids making
host scheduling during Prepare part of the execution-admission witness.
Run this complete definition in both modes and with the native race detector:

```sh
make check RUN='^TestReferenceHashIndexBuildAdmission$'
make check RUN='^TestReferenceHashIndexBuildAdmission$' REFERENCE=1
make check RUN='^TestReferenceHashIndexBuildAdmission$' RACE=1
```

### Existing-index constraint attachment witnesses

The thirteen shared `TestReferenceUsingIndex` definitions use the existing fixture
and public interfaces in both modes, with direct/prepared routes and independent
values, counts, types, nullable metadata, complete logical diagnostics, notices
and relative real catalog identities. Original scalar `using_index` regression
coverage lives in `TestReferenceUsingIndexEligibility/original_using_index`;
other PRIMARY KEY boundary cases are unchanged. There are no discovery filters
or oracle loggers in the completed definitions.

`TestReferenceUsingIndexNames/foreign_key` composes an already-admitted populated
FK with both UNIQUE and PRIMARY KEY attachments, in direct/prepared routes.
It expects the full dynamic catalog23505 at Execute and one rename notice,
preserves the original FK/index identities, dependencies, data and nullable
metadata, then retries under a noncolliding name on the same connection.
The retry retains the FK and physical INCLUDE index, adds NN only to successful
PK keys, and accepts another independently expected valid child row.

Select bounded groups with the current make check contract, not dollar end anchors:

```sh
make check RUN='TestReferenceUsingIndex(Eligibility|Diagnostics|Names)'
make check RUN='TestReferenceUsingIndex(PrimaryNullability|AlterPasses|ImmediateAttributes)'
make check RUN='TestReferenceUsingIndex(Include|Ownership)'
make check RUN='TestReferenceUsingIndex(Transactions|Locks)'
make check RUN='TestReferenceUsingIndex(RetainedStatements|NativeControls|FeatureSmoke)'
```

Run each identical selector with `REFERENCE=1` for real pinned PostgreSQL18.0.
NativeControls explicitly exercises native Reset/Dump/Restore and replays the
actual portable emitted SQL in PostgreSQL; it is not a simulated server control.
The `NativeControls/provider_graph` direct/prepared subcases attach a populated
enum-key INCLUDE index with sequence and enum-provider defaults, a REGCLASS
default referencing that index, and a persistent view. They assert real default,
type, constraint/view definitions, dependency edges, values and held consumers
before/after native Restore and on actual emitted-SQL PostgreSQL replay; the next
default sequence value is independently9. View inquiry is consumed as real replay
SQL and checked against independent columns/values, not cosmetic whitespace or
dump-generated source aliases. Successful same-name attached Restore without
Reset retains the parent number and reconstructs a reciprocal constraint/index
graph in a fresh catalog generation. Both public controls diverge live data after
Dump, then prove Restore removes that divergence, rebinds held consumers and
preserves canonical redump; Reset and bad-replay controls remain separate.
`TestUsingIndexRestoreGeneration` additionally asserts exact epoch advancement
and distinct owner/epoch/address ObjectRefs, new descriptor/storage/root objects,
key-only NN/INCLUDE, immutable predecessor snapshots and retained-session writes
into the reconstructed root. Numeric definition OIDs may coincide: Restore uses
an isolated New-engine allocator, and import remaps candidate collisions rather
than reserving removed destination definitions.
Public native pre-cancellation is only a request-admission boundary.
`TestUsingIndexBlockedCancellation` starts live direct/prepared attachment work
behind a real conflicting grant, observes its native wait queue via the existing
lock seam, then cancels and joins it. It asserts no waiter/grant/NOTICE leak,
original catalog/storage/root and healthy same-session retry. These definitions
are required proof inputs, not claims of observed passing outcomes.
The NULL precedence subcases `AlterPasses/null_precedence/two_primary` and
`.../check_collision` assert runtime metadata failures before final NN scan,
both notices/one notice respectively, original complete state and healthy retry.
WHERE a>0 proves actual partial rejection; folded WHERE TRUE is a positive.
The genuine concurrent expression+partial PostgreSQL fixture has a native
expression-only setup counterpart because combined native setup is unavailable;
the separate actual partial negative and valid represented private composite
preserve both rejection obligations. Native index timing is inherently immediate;
no manufactured deferred or unique HASH provider fixture is permitted.
Index-comment SQL is PostgreSQL-only; private descriptor preservation is separate.
Namespace witnesses reuse `fixture.UniqueReferencePublicNamespace` once per case
and parameterized pg_class relnamespace restrictions, with constraint-to-parent
pg_class joins. They do not require native pg_namespace or regnamespace support.
Typed CreateIndex fixtures specify the real method explicitly: SQL's parser
supplies the B-tree default, not the package-entry executor or a new fallback.
Common catalog witnesses use the admitted pg_constraint oid/conname/contype/
conrelid/conbin surface, canonical definitions, pg_class backing identities,
actual internal pg_depend ownership and public ColumnTypes/data snapshots. Failure
snapshots also retain relation and constraint dependency edges. Native has no
pg_attribute relation: shared table metadata and NN/DML witnesses plus private
physical index descriptors retain that coverage. Exact pg_attribute, conindid,
conkey and timing inquiry are PostgreSQL-only extras; native key-only, timing and
reciprocal ownership facts also have typed descriptor proofs. SQL
selectors use supported OR comparisons rather than introducing IN inquiry work.
The semantic-Var fixture binds a real expression and publishes a validated catalog
stage: ordinary CREATE INDEX normalizes a bare Var to an attribute. TYPE rebuilds
must update the physical name-to-slot lookup when renamed key/INCLUDE labels
change; catalog identity validation remains enabled.
`TestUsingIndexCombinedTypeAttachment` covers key and INCLUDE TYPE with attachment
in both textual orders, UNIQUE/PRIMARY KEY, successful remint and renamed-target
collision. A TYPE-created index stays a private creation when attachment updates
its descriptor/name slot; existing base identities still use Replace. Original
removal, destination-name observations and publication fences remain intact.
The fixture asserts reciprocal identity/type/data, key-only NN, immutable original
descriptors/root and collision recovery through the original usable index.
Real OID-to-regclass catalog comparisons use the existing bigint casts. Absent
named ON CONSTRAINT targets fail INSERT Prepare42704 in both modes; ordinary
preattachment INSERT/SELECT and valid postattachment conflict handles are retained.
Restore requires a newly reconstructed reciprocal group/catalog generation,
not numeric OID inequality across isolated allocators or remint of a retained
same-name parent. Source authority is `restoreStage`, `remapDefinitionOIDsLocked`
and `installForeignCatalogLocked`; the last advances the catalog epoch and
remints actual storage/roots.
The PGDepend construction fixture includes a real reciprocal backing index and
INCLUDE auto dependency; semantic-Var ordering accepts only catalog-simple keys,
not arbitrary expressions, when rebuilding a unique backing group.

Affected and supplementary proof inputs:

```sh
make check RUN='TestReferencePrimaryKey(Boundaries|MalformedBoundarySuffix)'
make check RUN='TestReferenceUniqueIndex(Lifecycle|Type|NativeControls)'
make check RUN='TestReferencePrimaryKey(Lifecycle|DropRetainsNotNull|SchemaMove|IdentityGuardPrecedence)'
make check RUN='TestUsingIndex'
make check RUN='TestPrimaryKey(Attachments|CapabilityRecords|WholeInputValidation|ReservedSuffixConsumers)'
make check RUN='TestExclusion(Syntax|PropertySyntaxDiagnostics)'
make check RUN='Test(InsertConflictIndexPositionHints|PartialIndexUniqueType)'
make check RUN='TestReferenceExclusion(PropertyDiagnostics|Definitions)'
make check RUN='TestUsingIndex(BlockedCancellation|CombinedTypeAttachment|ImmutableGroup|PrivateOverlay|PublicationFences)' RACE=1
make check RUN='TestReferenceUsingIndex(Transactions|Locks)' RACE=1
```

The reference regression/lock selectors also require identical real mode,
adding `REFERENCE=1`. Package-entry tests use independently constructed ASTs and
real catalog/providers/storage/roots, not additional private copies of public SQL.
They prove immutable metadata-only vectors/root identity, existing NN/comment
retention, actual include dependencies and semantic-Var LIKE/TYPE remapping,
savepoint/private-root sharing, and validation/Finish cancellation, Reset/Restore,
name/descriptor/authorization fences with no leaked candidate. Successful Finish
cannot be retroactively vetoed by cancellation.

Root owns generation/formatting and all verification after edits. Give each
check/quickcheck/race/bench invocation one external 900-second deadline; whole
Docker-free verification is `make quickcheck`, not selected quickcheck runs.
Post-merge `make finalcheck` has an external 1800-second deadline.
These commands are proof inputs, not recorded passes.

The standalone public smoke uses NewDriver/Connector notices and database/sql:
create populated composite UNIQUE INCLUDE payload, attach as renamed PK with
stable index identity and one exact notice, verify key NN/payload metadata and
held duplicate/NULL errors, healthy writes and ordered rows, a quiet later-failing
ALTER with original catalog preserved, unnamed UNIQUE ownership, actual native
Dump/Reset/Restore with held-handle reuse, and DROP ownership/canonical redump.
Exit zero with no output is the expected observation; reference FeatureSmoke
and full native-control/fence families remain required independent proof.

### Logical GiST exclusion cutover

`TestReferenceExclusionMethodBoundaries` now exercises real INT4RANGE overlap in
both modes; missing scalar GiST classes report42704 with the independently
specified default-class hint. SP-GiST and POINT remain explicit native boundaries.
The partitioned-exclusion witness uses explicit INET `inet_ops WITH =` and
canonical anyrange `&&`, not a B-tree alias or extension. Selected proof:

```sh
make check RUN='^TestReferenceExclusion(MethodBoundaries|ConcurrencyCancellation|PreparedPeerOutcome)$'
make check RUN='^TestReferenceExclusion(MethodBoundaries|ConcurrencyCancellation|PreparedPeerOutcome)$' REFERENCE=1
```

The latter two definitions are included because the shared cancellation/peer
helpers migrated signatures. See [hierarchy proof](../tables/README.md#partitioned-exclusions)
for scoped providers, release18.0 fresh cloning, persistence and standalone smoke.

