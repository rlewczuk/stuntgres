# reference/sequences

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### Inherited generated-expression migration checkpoint

The complete ordinary-inheritance matrix in
`TestReferenceAlterGeneratedExpressionBoundaries` now shares released recursive/
ONLY SET values and DROP protections. The later partition, publication, UDF
and typed-table boundaries remain unchanged.

Root runs `make check REFERENCE=1 RUN='^TestReferenceAlterGeneratedExpressionBoundaries$'`
under an external 900-second deadline, then the identical selector without
`REFERENCE=1`. Both completed modes are required.

### Actual TEMP namespace identities

`TestReferenceSequenceTempCreationActualNamespace` and
`TestReferenceSequenceTempMoveNamespaceAuthority` share complete direct/prepared
creation, permission/persistence precedence, identity/cache and physical-lifetime
assertions across native and PostgreSQL. Actual generated names come from
`pg_class.relnamespace` joined to `pg_namespace`, not execution of lookup-only
`pg_my_temp_schema` or an invented alias identity. Failed CREATE and physical
retirement absence are exact bag/count checks of the same relation/namespace
join, not execution of unavailable `to_regclass`. Persistence reads the actual
CHAR field with an explicit REGCLASS-to-OID identity cast, without adding
unrelated character-cast or mixed-alias comparison capabilities.

```sh
make check RUN='^TestReferenceSequence(TempCreationActualNamespace|TempMoveNamespaceAuthority)$'
make check REFERENCE=1 RUN='^TestReferenceSequence(TempCreationActualNamespace|TempMoveNamespaceAuthority)$'
```

Use external900-second deadlines. Both backends retain empty table/toast
namespace identities after physical owner exit; sequences, generators and
session memories retire. Namespace owner10 is independent of object creator.
Native controls omit source TEMP tokens and retain the destination's exact pair,
live children and empty retired metadata. The catalog's complete contract and
new feature smoke are documented in [catalog selected proof](../catalogs/README.md#pg_namespace-selected-proof).
`TestReferenceTemporaryTableDiagnostics` additionally distinguishes successful
superuser qualified namespace lookup from unsupported foreign physical storage.
Native direct/prepared execution asserts full0A000/no-position; the pinned PG
small populated peer table retains its independently expected empty cursor.
The native guard does not alter metadata/REGCLASS lookup, ordinary schema42501,
retired-child42P01 or supported own-session storage/creation.


### Virtual generated columns

Virtual generated-column coverage uses the shared untagged
`TestReferenceGeneratedVirtual` family: lazy demand, joined/outer/RETURNING
frames, definition/security diagnostics, column grants, constraints/index
boundaries, metadata, native controls and kind-preserving copies. Selected
proof requires both modes, plus the existing STORED family for preservation
(external 900-second deadline for every command):

```sh
make check RUN='^TestReferenceGeneratedVirtual'
make check REFERENCE=1 RUN='^TestReferenceGeneratedVirtual'
make check RUN='^TestReferenceGeneratedStored'
make check REFERENCE=1 RUN='^TestReferenceGeneratedStored'
```

Focused outer-join absence and destination-owned copied CHECK regressions:
`make check RUN='^TestReferenceGeneratedVirtual(OuterJoins|CopiedChecks|CopyGeometry)$'`,
also with `REFERENCE=1`. Private occurrence/frame, grafted-alias, transient merge
and destination-window units are included by `make check RUN='^TestGenerated'`.
Focused proof supplements, never replaces, the complete families above.

Focused scheduled/window/correlated-DML/MERGE/stored-view consumer proof:
`make check RUN='^TestReferenceGeneratedVirtual(BoundedProjection|WindowIdentity|CorrelatedUpdateFrom|CorrelatedDeleteUsing|JoinedMerge|StoredViewGraft)$'`,
also with `REFERENCE=1`, each under an external900-second deadline. Independent
goldens distinguish absent operands from real NULL-input rows, window input
permutations and partition/order keys, scalar-only DML demand, joined MERGE
ON/WHEN/actions, compound/derived grafts and atomic22012 failures. The window
file is `generated_virtual_window_identity_test.go`, not the GOOS-filtered
`_windows_test.go` suffix. Private `TestGeneratedVirtual` consumer units assert
missing-identity failures, unused-definition demand exclusion, shared window
row/provenance/version/lineage translation, scheduled-cache and retained-view
projection evaluation, and saved MERGE identity after borrowed cursor close.
Original `TestReferenceGeneratedVirtualDemand` also covers the retained view's
aggregate consumer; keep it selected alongside the newer regressions. All these
private units are also selected by the complete `^TestGenerated` command above.

Focused ordinary ALTER TYPE USING owner-row proof and feature smoke:
`make check RUN='^TestReferenceGeneratedVirtualAlterTypeUsing(Atomicity)?$'`,
also with `REFERENCE=1`, each under an external900-second deadline. Independent
direct/prepared expectations are (2,3,3) with c INT8, present all-NULL owner
(NULL,9,9), table/schema-qualified reads, unchanged generated definitions and
demanded Execute22012 preserving original c INT4, rows, defaults and catalog OIDs.
Query reads still require real occurrence identity; no synthetic query stage or
missing-identity fallback is used for the physical owner rewrite. The changed
initial mixed-kind Dump assertion additionally requires
`make check RUN='^TestReferenceGeneratedVirtualControls$'`, also with `REFERENCE=1`.

Focused shared INSERT-query/COPY logical NOT NULL proof and feature smoke:
`make check RUN='^TestReferenceGeneratedVirtual(InsertSelectConstraints|InsertSelectAtomicity|CopyConstraints|CopyAtomicity)$'`,
also with `REFERENCE=1`, each under an external900-second deadline. Independent
direct/prepared INSERT SELECT goldens require (2,3), real NULL-owner constant
and COALESCE recipes yielding (NULL,9), exact23502 with literal `virtual` detail,
and demanded division22012. Late ordered-source failures preserve both seed rows.
The COPY definitions live in `reference/dml` and reuse `copyTextScenario` and its
existing public native CopyFrom/pinned PostgreSQL stream consumer; the complete
virtual-family selector above includes them. Assert actual COPY1 counts/logical
rows, zero failure results and exact line1/line2 Context, unchanged seed rows
after late failures, successful transaction rollback and failed-savepoint25P02
recovery. Both consumers accept nullable1/a with a=0 without generation demand;
a-only reads succeed and a later b read fails22012 without changing the base row.

The root-owned standalone virtual smoke source stays in the OMP session and
is temporarily materialized as `./.generated-virtual-feature-smoke/main.go`.
Run `go run ./.generated-virtual-feature-smoke` with a 900-second external
deadline, record actual typed rows, generated-write/lazy-read diagnostics,
metadata, b-only grants and two native Restore/redump cycles, then remove the
temporary source directory. Its shared SQL counterpart is selected with
`make check RUN='^TestReferenceGeneratedVirtualFeatureSmoke$'` and the same
command with `REFERENCE=1`. No discovery-only backend filter remains in the
completed shared family; discovery results do not replace dual-mode proof.

### Generated-expression ALTER

Generated-expression ALTER uses independently expected dual-mode definitions in
`reference/sequences/alter_generated_expression*_test.go`, reusing the generated
fixture and full logical diagnostics. Run each bounded group under an external
900-second deadline, both natively and against real pinned PostgreSQL:

```sh
make check RUN='^TestReferenceAlterGeneratedExpression(Stored|Drop|Virtual)$'
make check REFERENCE=1 RUN='^TestReferenceAlterGeneratedExpression(Stored|Drop|Virtual)$'
make check RUN='^TestReferenceAlterGeneratedExpression(Diagnostics|Atomicity|Catalog)$'
make check REFERENCE=1 RUN='^TestReferenceAlterGeneratedExpression(Diagnostics|Atomicity|Catalog)$'
make check RUN='^TestReferenceAlterGeneratedExpression(Transactions|Prepared|Authorization)$'
make check REFERENCE=1 RUN='^TestReferenceAlterGeneratedExpression(Transactions|Prepared|Authorization)$'
make check RUN='^TestReferenceAlterGeneratedExpression(Locking|Controls|Boundaries|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceAlterGeneratedExpression(Locking|Controls|Boundaries|FeatureSmoke)$'
make check RUN='^TestAlterGeneratedExpression'
make check RACE=1 RUN='^TestAlterGeneratedExpression(Cancellation|ProviderFences|Publication|ParserIsolation|AuthorizationAfterWait)$'
make check RACE=1 RUN='^TestReferenceAlterGeneratedExpression(Locking|Transactions|Controls)$'
make check REFERENCE=1 RACE=1 RUN='^TestReferenceAlterGeneratedExpression(Locking|Transactions|Controls)$'
```

The groups exercise stored/drop/virtual values; full ALTER-stage diagnostics and
late atomic constraints; current/new/unchanged pg_attrdef ownership, deparse and
dependency replacement; transaction/savepoint recovery and independently expected
direct/prepared metadata; ownership and read-only admission; real exclusive-lock
waits and cancellation; final-state native controls and explicit hierarchy, typed,
publication and user-function boundaries. Fresh peer reads wait while ALTER's AX
lock is held; transaction peer observations occur only after COMMIT/ROLLBACK.
INT4 native ScanType is int64 and pinned pq's is int32; scanned generic integer
cells remain int64 in both. Controls call the actual native Dump/Reset/Restore
twice, and PostgreSQL replays the generated SQL stream without inventing native APIs.
Package units call the actual parser, binder, row rewrite, catalog-stage/provider
and Finish paths, inspecting retained roots, immutable sources, revision/OID
behavior, effective own heads and late authority/cancellation vetoes.
Prepared proof includes preexisting direct and retained prepared virtual views
after replacement and ADD c DEFAULT5 plus SET b=a+c, beyond the frozen projection.
Native held old-view rows keep the original values/types. CapturedVirtual package
mechanics uses actual data-view capture, complete source tuples, a frozen two-slot
bridge, correlated frames and retained source snapshots across Reset; recipe
resolution is checked for zero allocation.
It also supplies a complete latest recheck tuple beyond that frozen projection and
checks that a correlated outer frame does not borrow unrelated inner recheck rows.
Existing inheritance assertions remain unchanged: the queried parent's virtual
recipe uses mapped child inputs and actual child tableoid/lock/provenance. The
execution-only borrowed occurrence-indexed current tuple is saved/restored around
recheck evaluation and never overwrites active inheritance mapping scratch.
Focused existing regression proof, each also required against real pinned PostgreSQL:
`make check RUN='^TestReferenceInheritanceQuery(Generated|LockRecheck)$'` and
`make check REFERENCE=1 RUN='^TestReferenceInheritanceQuery(Generated|LockRecheck)$'`.
PG original unique-expression collision cases remain independently expected23505;
native CREATE asserts its existing0A000 boundary and absent index. Portable
companions exercise nonunique b/(b-5) root22012 and column-key partial UNIQUE(k)
WHERE b<3 membership23505, preserving recipes, rows and roots. FK-parent uses
distinct replacement keys so incoming-FK23503 is not masked by a unique collision.
After stored DROP, direct/retained prepared PostgreSQL UPDATE DEFAULT yields NULL;
native asserts existing0A000 and unchanged cells. Common omitted and explicit
NULL/value writes and single SET DEFAULT7/omitted7 still run in both modes.


Run the affected complete stored/virtual families above after changing their
shared binder/rewrite consumers. The standalone public executable smoke is
root-owned OMP-session source, not a repository test or fixture. Run its exact
session path under a 900-second external deadline, asserting quiet success from
retained prepared reads/writes, native metadata, virtual diagnostics, late action
atomicity and two actual native Dump/Reset/Restore cycles plus failed/canceled
Restore preservation.
The smoke additionally retains a prepared preexisting virtual view through SET and
ADD-input replacement, checking its values/metadata again after both real control
cycles and failed Restore.
Readiness is not proof: record actual argv, exits and expected-versus-observed
results. No temporary PostgreSQL-only filter is present.

### Serial and identity inquiry

The serial/identity inquiry family is selected with
`RUN='^TestReferenceSerialIdentity(Ownership|Lifecycle|Names|Nulls|Diagnostics|Consumers|Controls|FeatureSmoke|SystemColumns)$'`.
Its shared public scenarios independently assert AUTO/INTERNAL/manual ownership,
ownership versus defaults, qualified-name parsing and literal column names,
strict NULL and TEXT metadata, complete diagnostic fields and binding/execution
phases, symbolic consumers, retained results, and ownership-preserving native
Dump/Restore with actual emitted-SQL replay on PostgreSQL. Native control phases
have no PostgreSQL API equivalent; the SQL witnesses are shared. The cohesive
feature smoke is `RUN='^TestReferenceSerialIdentityFeatureSmoke$'`. Run both
selectors in quick and pinned modes using the commands above.
System-column coverage independently asserts all six existing table system names
return TEXT NULL, while absent system names on views retain complete42703
diagnostics and held recovery. The feature smoke includes both boundaries.
