# reference/catalogs

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### CREATE TABLESPACE selected proof

The shared `tablespace_*_test.go` scenarios independently specify builtin
`pg_tablespace` rows and ownership, named projection types, direct/prepared
syntax and diagnostics, owner/path/name precedence, fresh effective authority,
read-only/explicit/implicit transaction admission and failed-state recovery.
Real PostgreSQL creates a nonreserved tablespace in an actual container-owned
directory before asserting duplicate 42710, and explicitly drops its cluster
objects. Embedded creation must instead refuse physical locations with 0A000,
including repeated attempts, without publishing a user row or changing sentinels.
Reserved builtin names are not mislabeled as duplicates.

The native projection deliberately omits unsupported `spcacl`; supported
columns are `oid,spcname,spcowner,spcoptions`. Native Reset/Dump/Restore and
retained-row witnesses are explicit control phases in the shared scenario,
not fictitious PostgreSQL controls. Valid WITH syntax reaches the physical
boundary after earlier validation; option-value compatibility is not claimed.
Canonical path byte boundaries are 970 accepted lexically and 971 rejected;
short-component and multibyte witnesses distinguish filesystem component limits
and rune counts from the SQL validation contract. The public OID codec outcomes
are independently specified (pq normalized strings, native int64), not compared
as if the drivers had identical raw carriers. These instructions are not evidence.

```sh
make check REFERENCE=1 RUN='^TestReferenceTablespace'
make check RUN='^TestReferenceTablespace'
make check RUN='^Test(Tablespace|CreateTablespace|PGTablespace)'
make check RUN='^TestReferenceTablespaceFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceTablespaceFeatureSmoke$'
```

The durable smoke checks builtin rows/types, owner and superuser errors,
relative and reserved-name errors, transaction admission, physical refusal
and unchanged sentinel/no user rows. The real-directory duplicate is covered
by the selected family, not by reserved-name smoke assertions.

### Registered extension selected proof

The untagged shared `reference/catalogs/extension_*_test.go` scenarios independently
assert direct/prepared TEXT/NULL/rules, namespace and trusted ownership, exact
full diagnostics/notices, atomic failures, transaction/savepoint/publication
behavior and native control round trips. Controls additionally import named
extension/member owners into an independent Driver with different role attributes,
check strict metadata and target-principal fences, retained Rows/metadata,
committed-only Dump and deterministic redump. Native ownership comments are not
claimed as PostgreSQL SQL owner-restoration commands.

The disclosed PostgreSQL-only OracleSetup uses real loose FUNCTION/template/
dictionary and schema-GRANT setup unavailable natively; genuine package units
stage corresponding member descriptors and actual ACL consumers. Concurrent
oracle synchronization uses pg_stat_activity solely to observe the genuine
unique-index wait; native private transactions exercise the same complete-or-none
publication outcome at commit. No sleep or worker-order assumption identifies
the loser. Added helper Context expectations are source-grounded independently
and require confirmation in the selected pinned run.

Permanent TEMP definitions cover the independently observed own-template helper
failure, authorized foreign installation, exact namespace edge, transactional
DISCARD/savepoint recovery, bounded physical owner close and native portability/
retention. Peer assertions during tentative DISCARD deliberately read committed
catalog identities only: executable consumers can wait on the owner's real locks.
After rollback, both owner and authorized peer execute the restored provider/views.
Physical close means lease **and** pool close, not lease return; all close workers
join within5s. PostgreSQL's advisory exit barrier observes cleanup completion before
catalog assertions, without polling or assuming asynchronous socket close is exit.
Mixed collations, private maintenance, late-root/row/index publication, exhausted
metadata revisions, retained old Rows, source-token rejection, imported OID/name
collisions and named retained owner DROP dependencies have genuine engine units.
TemporaryReplacement also captures a genuine TEMP view procedure manifest and
TEMP DEFAULT/CHECK consumer before replacement, executes them afterward, and
rejects same-address procedure substitution, foreign physical tokens and ordinary
stale ObjectRefs. Schema retirement uses successful same-name CREATE SCHEMA rather
than adding an unrelated native pg_namespace projection.
SchemaDependencies independently specifies normal extension-to-schema RESTRICT
diagnostics and CASCADE notices/consumer closure. Confirm these independently
specified records against the pinned oracle before accepting native behavior.

The integrated `pg_extension_*` definitions use the same single live
descriptor-derived catalog source as extension installation, not a second empty
provider. Fresh native catalogs are genuinely empty; installed descriptors supply
actual identity/owner/schema/version rows. Engine witnesses preserve strict
column shape/type-generation ownership, cancellation and retained capture
semantics, and additionally compare an installed row with its published descriptor.
Missing-provider CREATE prepares successfully and fails at execution with
`0A000` on both backends. PostgreSQL's system-installation Hint is pinned only on
PostgreSQL; native exposes its truthful registered-only availability boundary.
Unchanged ALTER/DROP/LOAD command boundaries remain separately asserted.

Review2 tests-first checkpoint pins eighteen member-catalog attribute nullability
records as named bags, not physical attnum order. PostgreSQL's pq Nullable
capability remains unknown; native empty and populated direct/prepared queries
require known NOT NULL for all exposed procedure/template fields and dictionary
first five fields, with nullable dictinitoption. The actual standalone smoke
asserts those same native metadata contracts.

TemporaryCleanup's actual-session-end preparation_stale and
postdrain_late_consumer subcases delegate genuine outside-lock relation-provider
validation, publish an independent late procedure-dependent view and durable
row, and require internal fresh retirement. Ordinary cleanup validates before
index preparation; validated candidates are not mutated by later index metadata
evolution. Failed attempts unwind their scope/operation/stage before recapture,
and unchanged structural errors escape without retry. Mixed TEMP collation plus
saturated DEFAULT/CHECK metadata uses terminal-only fresh-descriptor semantics;
actual ordinary/postdrain malformed-survivor units require prompt visible error
and intact committed state. No user DDL overflow policy is relaxed.

Run each command with an external900s deadline from the part worktree:

```sh
make check RUN='^TestReferenceExtension(InstallAndProvider|IdentityAndRepeat|VersionSchemaAndPrivilege|Transactions|Controls|OracleSetup|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceExtension(InstallAndProvider|IdentityAndRepeat|VersionSchemaAndPrivilege|Transactions|Controls|OracleSetup|FeatureSmoke)$'
make check RUN='^TestReferenceExtension(NamespaceDependency|TemporaryOwnInstall|TemporaryLookup|TemporaryDiscard|TemporaryRollback|TemporaryClose|TemporaryControls|SchemaDependencies)$'
make check REFERENCE=1 RUN='^TestReferenceExtension(NamespaceDependency|TemporaryOwnInstall|TemporaryLookup|TemporaryDiscard|TemporaryRollback|TemporaryClose|TemporaryControls|SchemaDependencies)$'
make check RUN='^TestExtension(ProviderIdentity|Rules|ExpressionIdentity|ProviderConflicts|SchemaACL|ProviderPublication|DatabaseCreateAuthority|OwnerMetadata|TemporaryClosure|TemporaryCleanup|TemporaryLookup|TemporaryReplacement|SchemaClosure)$'
make check RUN='^TestTemporaryTable(CleanupClosure|CleanupPublication|TerminalRevisionPreparation|TerminalMaximumPublication|CascadeRevisionBounds)$'
make check RUN='^TestReferencePGExtension(CommandBoundaries|Diagnostics|Shape|Installed|ResolutionAndPrepared|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePGExtension(CommandBoundaries|Diagnostics|Shape|Installed|ResolutionAndPrepared|Controls|FeatureSmoke)$'
make check RUN='^TestPGExtension(ShapeAndOwnership|Capture|CaptureInstalled)$'
make check RUN='^TestReferenceExtensionMemberNullability$'
make check REFERENCE=1 RUN='^TestReferenceExtensionMemberNullability$'
make check RUN='^TestCollation(PrivateSessionIdentity|TemporaryCleanupAX|TemporaryCleanupCompletenessFence|TemporaryCleanupExhaustion)$'
make check RACE=1 RUN='^TestReferenceExtension(Transactions|Controls|TemporaryDiscard|TemporaryRollback|TemporaryClose|TemporaryControls|SchemaDependencies)$'
make check RACE=1 RUN='^TestExtension(ProviderPublication|DatabaseCreateAuthority|OwnerMetadata|TemporaryClosure|TemporaryCleanup|TemporaryLookup|TemporaryReplacement|SchemaClosure)$'
```

Select top-level definitions, not a backend slash filter. The same completed
reference definitions require both native and real pinned modes before review.
Root runs whole-project Docker-free quickcheck after synchronized edits, not
mid-flight. Standalone feature smoke is the complete session artifact
`local://ext-registry-smoke.go`, materialized outside the checkout and run with
`timeout 900s go run \"$SESSION_TMP/ext-registry-smoke.go\"` from the worktree;
quiet exit0 requires actual installation, TEXT/NULL, exact repeat/notice,
member relationships, rollback isolation, trusted privileges and control/import
ownership witnesses. It additionally performs actual own-TEMP42883 atomicity,
foreign installation and owner-vs-peer lookup, zero-output Dump failure, tentative
catalog-only DISCARD visibility, executable rollback recovery, committed full
consumer closure/surviving rows, fresh reinstall, bounded physical retirement
with exit barrier, and persistent schema RESTRICT/CASCADE closure. A test named
FeatureSmoke does not replace that program. Post-merge finalcheck uses an external
1800s deadline; targets themselves introduce no hidden deadlines.

### Available extension views selected proof

The untagged `reference/catalogs/available_extensions_*_test.go` definitions
independently assert both released shapes, raw transports/nullability, registry
universe, installed/default distinction, exact pg_views reconstruction, predicates,
held prepared freshness, joins/stored views, full42703 diagnostics, low-privilege/
off-path/foreign-TEMP metadata and transaction publication. Pinned discovery ran
first with a temporary native filter; that filter was removed before genuine
native missing-relation red and production authorization. No discovery filter or
observation-file probe survives. Oracle-only attribute/type grounding does not
add native catalog producers. Unknown installed-version mutation is real
PostgreSQL-only UPDATE in a transaction always rolled back; native covers the
same projection with a private authentic descriptor copy, not new installation.

Controls exercise actual public native Dump/Reset/Restore, committed-only Dump,
independent Driver restoration and retained rows/types; PostgreSQL's phase uses
real installation/rollback, not native APIs on pq. Component units assert all
thirteen origins, generation-owned types, reciprocal row/array ownership,
collations, immutable metadata conversion and real cancellation.
JoinConsistency independently asserts five fields from both availability views
and pg_extension in both availability opening orders: initially
`[unaccent,NULL,1.1,false,NULL]`, installed `[unaccent,1.1,1.1,true,1.1]`.
Direct/new prepared/already-held statements on separate physical connections
cover self-private/peer-committed state, savepoint/full rollback and commit.
StatementFrontier is a native component witness: capture the real statement
catalog, open one actual source, genuinely publish in its unlocked opening gap,
then open the other sources. All three first-source choices must retain the old
state; a fresh scope sees the installation. This deterministic schedule is not
claimed for the public PostgreSQL matrix, and does not widen all catalog sources.
Root runs these bounded commands in both modes where shown, each under an
external900-second deadline:

```sh
make check RUN='^TestReferenceAvailableExtensions(Shape|Metadata|Filtering|Installed|Diagnostics|JoinConsistency)$'
make check REFERENCE=1 RUN='^TestReferenceAvailableExtensions(Shape|Metadata|Filtering|Installed|Diagnostics|JoinConsistency)$'
make check RUN='^TestReferenceAvailableExtensions(Visibility|Transactions|UnknownInstalledVersion|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceAvailableExtensions(Visibility|Transactions|UnknownInstalledVersion|Controls|FeatureSmoke)$'
make check RUN='^TestAvailableExtensions(ShapeAndOwnership|Projection|MetadataValues|Capture|StatementFrontier)$'
make check RACE=1 RUN='^(TestAvailableExtensionsStatementFrontier|TestReferenceAvailableExtensions(Transactions|Controls|JoinConsistency))$'
```

Whole-project Docker-free proof uses quickcheck only after edits stop. The actual
standalone public source `local://available-extensions-smoke.go` is an OMP session
artifact, not a committed executable. Root materializes it outside the repository
at absolute S, runs `timeout 60s go run "$S"` from the assigned worktree, records
resolved argv/cwd/exit/stdout/stderr and removes the materialization afterward.
Quiet exit0 requires exact A0/V0 and A1/V1 metadata/rows, absent typed results,
held statements through real rollback/installation, callable Hotel, exact full
direct/Prepare42703 without handles and genuine retained native Rows through
Dump/Reset/Restore. The actual five-field three-source join must yield J0/J1
through private installation, peer isolation, rollback, commit, Reset and Restore,
including both availability orders and peer handles prepared before commit.
Fresh handles are prepared after generation-replacing controls. FeatureSmoke
supplements rather than replaces that source; the component alone forces the
mid-opening publication boundary.

### Visibility utility boundaries

The affected visibility utility-boundary scenarios now assert supported MV
creation/count/stored rows and cleanup in both backends, instead of obsolete
native syntax rejection. Validate those changed contracts in both modes:
`make check RUN='^TestReferenceVisibilityInquiry(Boundaries|UtilityBoundaries)$'`
and
`make check REFERENCE=1 RUN='^TestReferenceVisibilityInquiry(Boundaries|UtilityBoundaries)$'`
(900 seconds each).

### Timezone lookup views

Timezone lookup coverage uses `RUN='^TestReferenceTimezoneViews'` in both
`make check` and
`make check REFERENCE=1` (900 seconds per invocation).
Its smoke selector is `RUN='^TestReferenceTimezoneViewsFeatureSmoke$'` in both
modes. Independent rows/types, full55000/42703/42804/42883 diagnostics, declared
metadata, nil/typed NULL predicate parameters, session/transaction capture,
PUBLIC SELECT and symbolic native controls are asserted before backend comparison.
WriteAnalysis and InsertAnalysis preserve existing supported semantic errors before
read-only rejection; VALUES column-reference0A000 is an explicit native capability
boundary, not a PostgreSQL parity claim. Smoke includes nil-bound empty metadata
and semantic-error savepoint recovery. Provider units exercise DST transitions,
negative DST, historical matching, inventory and cancellation without public SQL.

### PG_USER coverage

PG_USER coverage uses `RUN='^TestReferencePGUser'` in both
`make check` and
`make check REFERENCE=1`, with a 900-second deadline
per invocation. The smoke selector is `RUN='^TestReferencePGUserFeatureSmoke$'`.
The shared scenarios independently specify the nine-column LOGIN projection,
OID identity through a separately owned sequence, direct privilege flags,
PUBLIC readability, constant eight-star masking, typed NULL configuration,
finite/infinite expiry, full diagnostics and direct/prepared error stages,
retained snapshots, composition, cancellation and native-control boundaries.
Password setup is deliberately excluded from the recorder and diagnostic
failure text. Native Dump replay uses the actual emitted local SQL, not a
PostgreSQL control substitute.
Supplemental typed provider/saved-root/cancellation checks use
`make check RUN='^(TestPGUser|TestTableViewSummaryBuiltinIdentity)'`.
Related reference regression uses
`RUN='^(TestReferencePGUser|TestReferenceTimezoneViews|TestReferenceRegtype|TestReferencePGTableViews)'`
in both quick and REFERENCE=1 modes (900 seconds per invocation). This preserves
existing timezone routing, allocated regtype identity expectations and scoped
summary inventories while checking pg_user view classification.

For initial oracle discovery, temporarily filter native backends in the common
scenario helper as described above, then run
`make check REFERENCE=1 RUN='^TestReferencePGUser'`.
The exact postgres-only subtest selector is
`RUN='^TestReferencePGUser[^/]*$/^postgres$'`; full diagnostics can be selected
with `RUN='^TestReferencePGUserDiagnostics$/^postgres$/./^(direct|prepared)$'`.
`TestReferencePGUserOracleCatalog/postgres` additionally checks release attribute
types, typmods, collations and relation/rowtype identities, and independently
asserts the exact `pg_views` definition (differences print under).
These allocator assertions are
release-only research, not cross-backend role or relation allocator parity.
Remove the temporary backend filter before recording the missing-feature quick
regression and before all final dual-mode runs. Preserve the permanent
PostgreSQL-only `pg_attribute` research guard and native-only control phases.
Docker/image failures in an explicitly enabled real run MUST fail visibly, not
be skipped or mocked. Fix slow/hanging tests rather than increasing deadlines.

### Legacy pg_group verification

Use these small selectors in **both** Docker-free and pinned PostgreSQL modes,
with an external900-second deadline for each command:

```sh
timeout 900s make check RUN='^TestReferencePGGroup(Shape|OracleCatalog|Diagnostics)$'
timeout 900s make check RUN='^TestReferencePGGroup(Membership|GrantorMultiplicity)$'
timeout 900s make check RUN='^TestReferencePGGroup(Freshness|Snapshots|Visibility)$'
timeout 900s make check RUN='^TestReferencePGGroup(Controls|FeatureSmoke)$'
# Repeat each selected command above with REFERENCE=1.
timeout 900s make check RUN='^TestReferencePGTableViews(Builtins|Controls)$'
timeout 900s make check REFERENCE=1 RUN='^TestReferencePGTableViews(Builtins|Controls)$'
timeout 900s make check RUN='^Test(PGGroup(Descriptor|Projection|RoleRoot|Cancellation)|TableViewSummaryBuiltinIdentity)$'
timeout 900s make check RACE=1 RUN='^TestPGGroup(RoleRoot|Cancellation)$'
timeout 900s make check RUN='^TestReferencePGGroupFeatureSmoke$'
timeout 900s make check REFERENCE=1 RUN='^TestReferencePGGroupFeatureSmoke$'
```

Shared public scenarios independently specify the exact NAME/OID/_OID fields,
current NOLOGIN predicate, real pg_roles identity relationships, direct-edge
element bags preserving grantor multiplicities, all-false membership options,
nonnull empty arrays, role defaults/overrides, unrelated-reader disclosure,
credential-column diagnostics, full diagnostic fields/positions and direct/
prepared error phases. Generated OIDs are backend-local, not allocator goldens.
The group helper validates raw consumer and middleware-owned buffers, preserves
element multiplicity and ignores only unpromised row/array order. Password setup
never enters the recorder or diagnostic failure output.

Oracle-first discovery may temporarily filter native backends **only** in the
new group scenario helper under the policy above. Remove the filter before
native-red and every completed dual-mode verification. Keep the permanent
PostgreSQL-only pg_attribute/pg_type structural witness and actual native-only
retained Rows/metadata, catalog-write/row-lock exclusions, cancellation and
Dump/Reset/Restore phases. Native controls preserve role identities/edges and
stored symbolic consumers; real mode replays the **actual native-emitted SQL**
on PostgreSQL using its own group/member identities, never a substitute Reset API.
Valid native writes fail0A000, not an invented PostgreSQL55000 join-view error.

The standalone public Driver/database/sql smoke program in the PG18-VIEW-GROUP
living plan is materialized temporarily at `/tmp/pg18-view-group-smoke.go`.
From the worktree, run `timeout 900s go run /tmp/pg18-view-group-smoke.go`.
Expected exit0 with exactly `pg_group public smoke: PASS`: three-column metadata,
empty→direct all-false member→empty, held unrelated-reader freshness, LOGIN hides/
NOLOGIN restores the same OID/list, and credential42703. Record observations and
remove the temporary source; it is not a committed test or production dependency.
Whole-project Docker-free verification remains `make quickcheck`; selected
dual-mode proof and the smoke are not replaced by quickcheck or finalcheck.

### Size parsing and formatting

Size parsing/formatting coverage uses the untagged `TestReferenceSize` family:
independent exact binary/fractional/signed-limit vectors, every promotion
boundary, numeric scale/specials, full diagnostics and stages, actual held
statements and recovery, NULL-only/empty raw metadata, expression consumers and
write/transaction atomicity. Stored overloads use compact/pretty semantic deparse
replay rather than incidental formatting goldens. Native controls preserve
retained rows/metadata and failed replacement atomicity; pinned PostgreSQL
consumes the actual native-emitted Dump SQL. Every changed definition must run
in both modes before review, each within the 900-second deadline:

```sh
make check RUN='^TestReferenceSize'
make check REFERENCE=1 RUN='^TestReferenceSize'
make check RUN='^TestReferenceSizeFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceSizeFeatureSmoke$'
make check RUN='^Test(Size|Math|Numeric)'
make check RUN='^TestSize'
```

The smoke expects 1048576 INT8, `1024 bytes`, `-11 kB`, `10.5000 bytes`,
beyond-BIGINT `10240 PB`, typed TEXT NULL metadata, exact malformed-unit and
overflow records, held recovery, no failed-write prefix, and faithful stored
overloads/control replay. The shared MathPlan and numeric codec seams also
require existing math/numeric regression coverage; selected checks do not
replace ordinary quickcheck or post-merge finalcheck.

Size consumer witnesses retain the explicit native internal-CHAR coercion
boundary alongside independent PostgreSQL results; shared COUNT and SUM
arguments, INSERT affected counts/state reads and UPDATE RETURNING prove the
supported composition paths. Deparse catalog-query metadata independently checks
native known-nullable TEXT versus PostgreSQL unknown nullability, without changing
size-result metadata. Retained descriptors are captured before cursor exhaustion
and remain asserted after replacement/consumption.

### Shared-comment catalog coverage

The untagged `TestReferencePGShDescription` family independently asserts the
three-column relation2396 contract, shared ROLE comment identities/contents,
local catalog separation, ordinary readability, full diagnostics, transactions,
held freshness, detached rows/metadata and stored-view/composition behavior.
Role IDs come from a separately owned helper relation's `pg_class.relowner`;
schema IDs come from that relation's `relnamespace`, not an adjacent catalog.
Native bootstrap has no seeded shared comments, while PostgreSQL can have
legitimate global rows: scenarios filter exact object/class pairs, never assert
whole-cluster row-count parity. Native-only Reset/Dump/Restore phases preserve
shared roles outside local replay and feed actual emitted local SQL to PostgreSQL.
Pure `TestPGShDescription` units cover descriptor/PUBLIC ACL, uint32 maximum and
uint64 non-aliasing, detached captures, inquiry agreement, saved-scope nested
opening and entry/in-loop cancellation without private public-SQL duplicates.

Run the same completed definitions in both modes, each within900 seconds:

```sh
make check RUN='^TestReferencePGShDescription'
make check REFERENCE=1 RUN='^TestReferencePGShDescription'
make check RUN='^TestReferencePGShDescriptionFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGShDescriptionFeatureSmoke$'
make check RUN='^TestPGShDescription'
```

### Sequence value view

`TestReferencePGSequencesShape`, `Definitions`, `Values`, `Privileges`, `Temp`,
`Demand`, `Transactions`, `Diagnostics`, `CTE` and `Controls` prescribe independent
pinned PostgreSQL18.0 outcomes, including exact cache high-water/NULL masks,
all eleven descriptors, full English diagnostic records, physical TEMP exit,
actual emitted Dump replay and cancellation cleanup. PostgreSQL-only pg_type
identity links supplement native actual descriptor/type-link units; native TEMP
namespace is obtained from the actual public pg_sequences row, not an unsupported
pg_my_temp_schema function. Shared direct/prepared behavior is not skipped.

```sh
make check RUN='^TestReferencePGSequences'
make check RUN='^TestReferencePGSequences' REFERENCE=1
make check RUN='^TestPGSequences(Descriptors|Provider|ACL|Demand|Concurrency|Cancellation|CTE)'
make check RUN='^(TestPGSequences(Concurrency|Cancellation|CTE)|TestReferencePGSequences(Demand|Transactions|Temp|CTE))' RACE=1
```

Use external900s deadlines. Changed reference definitions require both native
and real pinned modes before review. Whole-project Docker-free `make quickcheck`
and post-merge `make finalcheck` do not replace selected proof or the standalone
public smoke. Demand units inspect real manager holders/queues, including
definition/count/stored/join RX, rejected-name/FALSE no RX and terminal release.

### Sequence definition catalog discovery

The shared `TestReferencePGSequence` family covers the eight-column
`pg_catalog.pg_sequence` contract, exact definition options rather than generator
state, catalog identities, transactional visibility, repeatable-read peer
ALTER/CREATE/DROP, first-Prepare versus first-Execute timing, privileges and
other-session temporary definitions, full diagnostics, cancellation and actual
native Dump replay. `TestReferencePGSequenceFeatureSmoke` selects the integrated
definition lifecycle and control replay witness.

The privilege fixture keeps PostgreSQL's four false `has_schema_privilege` /
`has_sequence_privilege` assertions. Native mode does not implement those
predicates: a qualified `nextval` must report private-schema denial, while
numeric-regclass `nextval`, `currval` and `setval` must independently report
sequence denial with exact existing diagnostics before the same metadata read.
Denied `nextval` excludes USAGE/UPDATE and denied `currval` excludes USAGE/SELECT.
Failed native Restore appends a supported `CREATE SEQUENCE ... CACHE 0` to the
actual dump, expects option-binding SQLSTATE 22023, and preserves the complete
original definition bag; arbitrary ALTER is outside Restore's accepted input.

The oracle discovery observed retained RR definitions and row bags. Prepare
inside BEGIN retained start15/cache9 after peer ALTER; a statement prepared
before BEGIN captured start21/cache4 at its first execution. Discovery filters
are removed: both commands run the shared completed scenario. Private-schema
fixtures use default absent PUBLIC privileges with the independent false
predicates or denied-operation witnesses above, without native REVOKE support.

```sh
make check REFERENCE=1 RUN='^TestReferencePGSequence'
make check RUN='^TestReferencePGSequence'
```

After implementation, include all migrated definition probes in both modes:

```sh
make check RUN='^TestReference(PGSequence|SequenceCacheBoundsRow|SequenceHugeCacheArithmetic|SequenceCreateRestart|AlterTypeIdentityTargets|AlterTypeIdentityFailuresAndPrephase)'
make check REFERENCE=1 RUN='^TestReference(PGSequence|SequenceCacheBoundsRow|SequenceHugeCacheArithmetic|SequenceCreateRestart|AlterTypeIdentityTargets|AlterTypeIdentityFailuresAndPrephase)'
make check RUN='^TestReferencePGSequenceFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGSequenceFeatureSmoke$'
make check RUN='^TestPGSequence'
```

Each selected check invocation has a 900-second deadline. The finalcheck runs
after merge under its single 1800-second deadline; neither gate replaces the
selective dual-mode runs.

### Sequence metadata view oracle

The `TestReferencePGSequences` family defines the eleven-column `pg_sequences`
oracle independently of backend comparison. `Shape`, `Demand`, `Transactions`,
`Diagnostics`, `Frames` and `CTE` cover exact types and fixed catalog identities,
admitted-row volatile helper locks, retained RR definitions with live
generator/ACL state, complete diagnostics, lexical-frame-safe correlated
projections and volatile CTE producer frontiers.
`Definitions`, `Values`, `Privileges`, `Temp` and
`Controls` cover options, cache high-water values, unreadable/uncalled NULL,
physical TEMP visibility and actual native control replay. These are test
expectations, not a claim that the native view implementation has passed.

RR drop/recreate diagnostics interpolate the original sequence OID captured
before peer DDL, never a hard-coded allocated OID or the replacement identity.
Even a definition-only admitted read expects `XX000`; the subsequent query
expects `25P02`. Namespace and sequence definitions, including the owner identity,
remain from the RR definition snapshot. The owner name is presented from the
current statement role root rather than a retained role-name snapshot. Current
role membership produces NULL/value/NULL, and current generator state is read
independently.

The Recreate witness issues DROP and CREATE as separate commands on the same
peer connection; it tests real identity replacement, not multi-statement SQL.
Full, definition-only and FALSE `FOR UPDATE` queries must succeed directly and
through retained prepared execution. Their SQL rowmark admission still matches
the bound manifest; the represented view has no physical tuple marks, while
admitted rows retain the normal per-sequence helper locks.

Ordinary derived sources capture the caller's lexical frame after nested binding
restores that scope. `TestPGSequencesAdmissionDerivedRowLockFrame` binds the actual
derived `FOR UPDATE` shape and proves that its local residual predicate crosses
the row-lock boundary into direct child projection columns in the child's exact
frame. The filter uses one captured evaluation frame for both admission and
residual evaluation. Foreign, zero or mixed frames remain whole-expression
barriers; CTE boundaries remain distinct and are not admitted by this mapping.
The existing Demand witness still requires a-only helper RX, with no b helper RX.

`TestPGSequencesConcurrency` observes the actual manager request on the exact
sequence identity: RESTART queues ShareRowExclusive behind the view helper's
transaction-owned RowExclusive. Closing Rows does not release that lock;
rollback admits RESTART, whose fresh last_value is NULL and nextval is 19.
Cancellation/join cleanup runs even when the queue assertion fails.

`TestReferencePGSequencesCTE` and `TestPGSequencesCTE` exercise direct,
ordinary-derived, stored-view and nested stored-view sources with default,
`MATERIALIZED` and `NOT MATERIALIZED` hints. A consumed volatile CTE completes
both sequence rows behind one producer even when an outer restriction returns
only one name; shared references retain that producer rather than independently
folding its restrictions. The tests observe helper RX holders and a real blocked
helper queue on the sequence absent from the final rows, cancellation and terminal
cleanup/recovery. Prepare opens no helper, and unused CTE bodies acquire no helper
locks. `TestPGSequencesCTEStoredGraph` and `TestPGSequencesCTEStoredGraphAuthority`
inspect real immutable stored definitions without opening providers; shared
acyclic sources are allowed, while invalid references and active-path cycles use
the existing catalog/view graph errors.

Malformed inferred REGTYPE input is an explicit PostgreSQL wire-only control:
OID-input `22P02` includes `unnamed portal parameter $1 = '...'`. The native
driver has no PostgreSQL parameter input codec and must not fabricate that
context. Shared direct and retained prepared routes still assert numeric
inferred REGTYPE inputs 20/23/21 and bigint/integer/smallint outputs. Explicit
symbolic input through `text::regtype` independently expects full `42704`
diagnostics and recovery using the same retained statement.

```sh
make check RUN='^TestReferencePGSequences(Shape|Demand|Transactions|Diagnostics|Frames|CTE)$'
make check REFERENCE=1 RUN='^TestReferencePGSequences(Shape|Demand|Transactions|Diagnostics|Frames|CTE)$'
make check RUN='^TestReferencePGSequences(Definitions|Values|Privileges|Temp|Controls)$'
make check REFERENCE=1 RUN='^TestReferencePGSequences(Definitions|Values|Privileges|Temp|Controls)$'
```

Each command requires an external 900-second deadline. Before production
authorization, use the same reference selector with `/postgres$` appended to
exercise only the pinned oracle; unqualified definitions include native mode
and supply its missing-view behavioral red. No discovery backend filter or
deliberately failing observation helper remains.

### Dependency catalog

The dependency catalog family independently specifies exact represented edge bags,
all seven result descriptors, owner-specific diagnostics, supported private
sequence changes, retained cursors and native controls with actual SQL replay.
Run the identical complete family in both modes, each with a900-second deadline:

```sh
make check RUN='^TestReferencePGDepend'
make check REFERENCE=1 RUN='^TestReferencePGDepend'
make check RUN='^TestReferencePGDependFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGDependFeatureSmoke$'
```
Changed existing default definitions must also run in both modes; the family
prefix does not select them. They preserve genuine direct and prepared CREATE,
default INSERT, data/provider sentinels and existing excluded sequence-expression
controls without separate duplicated PostgreSQL-only success bodies:

```sh
make check RUN='^TestReference(SequenceLatestIntegrationDefaultBoundary|StringCoreRegclassDefaultSourceConsumerRoutes|StringCoreRegclassDefaultBoundary)$'
make check REFERENCE=1 RUN='^TestReference(SequenceLatestIntegrationDefaultBoundary|StringCoreRegclassDefaultSourceConsumerRoutes|StringCoreRegclassDefaultBoundary)$'
```

Each selective invocation has a900-second deadline. Source-route migration
definitions are additional named selections, not replacements for this family
or these changed existing definitions.


Supplemental collector/walker units use
`make check RUN='^TestPGDepend'`.
These do not replace the dual-mode runs, ordinary quickcheck or post-merge
finalcheck. Public capability boundaries remain explicit: no REGCLASS column
storage, set-operation query rollout, transactional CREATE TABLE/ALTER DEFAULT
or native TEMP-table creation is implied by metadata reading.

### Table and index summary views

The `TestReferencePGTableViews` family independently pins exact eight/four-field
summaries, raw metadata, current/quoted ownership and names, unfiltered denied
object disclosure, immutable captures/held refresh, sticky index history and
failed ordinary/concurrent publication, reconstructed user-definition replay,
represented builtin9/4 membership and full released definitions, diagnostics,
transaction overlays/RR differences and native logical control normalization.
The controls replay actual native SQL Dump on PostgreSQL. Column GRANT/REVOKE
fixture setup remains a separate phase from ACL-neutral native Dump replay:
explicit ACLs are not serializable. Internal units exercise source capture and
descriptor replacement without weakening that control boundary.
Run every new/migrated reference definition in both modes, each with a900-second
deadline, before review:

```sh
make check RUN='^(TestReferencePGTableViews.*|TestReferenceViewSmokeCatalogReplayControls)$'
make check REFERENCE=1 RUN='^(TestReferencePGTableViews.*|TestReferenceViewSmokeCatalogReplayControls)$'
make check RUN='^Test(TableSummary.*|TableViewSummaryBuiltinIdentity)$'
make check RUN='^TestReferencePGTableViews(Lifecycle|IndexHistory|FailedIndexHistory|Visibility|Builtins|Definitions|Controls)$'
make check REFERENCE=1 RUN='^TestReferencePGTableViews(Lifecycle|IndexHistory|FailedIndexHistory|Visibility|Builtins|Definitions|Controls)$'
```

Expected feature observations include DROP-last-index preserving true, a fresh
table starting false, denied-schema/table metadata disclosed on both backends,
four nonNULL released builtin SELECT definitions, executable dynamic user-view
copies after renames/replacement and Restore producing false/true/false for
never/currently/formerly indexed tables. No discovery gate remains in final tests.

The `TestReferencePGIndexes` acceptance family pins the five-column view shape,
compact definitions, held execution, lifecycle, supported transactional overlays,
repeatable-read peer CREATE boundary, independent disclosure policy and full
diagnostics. Controls replay actual native Dump SQL on PostgreSQL; the existing
ready-invalid concurrent hash witness also checks descriptor enumeration.
Run the complete affected selector in both modes (900 seconds per invocation):

```sh
make check REFERENCE=1 RUN='^(TestReferencePGIndexes.*|TestReferenceIndexMaintenanceMetadataOracle|TestReferenceDropViewIndexWrongKind|TestReferenceConcurrentIndexHashInvalidQuery|TestReferenceConcurrentIndexFeatureSmoke)$'
make check RUN='^(TestReferencePGIndexes.*|TestReferenceIndexMaintenanceMetadataOracle|TestReferenceDropViewIndexWrongKind|TestReferenceConcurrentIndexHashInvalidQuery|TestReferenceConcurrentIndexFeatureSmoke)$'
make check RUN='^TestReferencePGIndexesFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGIndexesFeatureSmoke$'
```

Initial pinned oracle discovery passed before production. All scenario-local
discovery filters are removed; both commands now execute the same shared bodies,
including native behavioral-red before implementation. A filtered native run
is not verification. PostgreSQL exposes denied underlying objects here;
native acceptance intentionally requires schema USAGE and parent table SELECT
or SELECT on any live column. No global fixture behavior is changed.
Private `TestPGIndexes` units exercise real SetObjectACL/AuthorizationView
captures: table versus live non-indexed column grants, PUBLIC, ordinary owner,
superuser, schema denial, dropped stable attributes, revocation, retained old
captures and original invoker dispatch. Structural metadata units remain
ACL-neutral. Run alongside the shared reference gate:

```sh
make check RUN='^(TestPGIndexes.*|TestIndexMaintenanceMetadata.*)$'
```

### Index catalog provider boundaries

PG-only relam/vector/type-provider catalog controls are genuine boundary coverage,
not native compatibility passes or skipped implementation. Native reduced-catalog
metadata is asserted independently through projectIndexMetadata package functions;
do not add unowned SQL catalogs to make a discovery query run. See
[hash design](../../docs/design/PG18-INDEX-HASH.md). Generate retained parser productions
before selective checks; generation/formatting/verification belong to the root.

### Visibility source-route isolation

extra identities. The visibility source-route scenario keeps the complete
unfiltered two-peer bag and creates an unrelated `public.t` to exercise
isolation from the old generic basename.

### Cast metadata catalog discovery and smoke

The shared `TestReferencePGCast` family asserts the complete independently
specified69-row supported boundary, exact six-column shape and raw codecs,
fixed type/function identities, generated row identity invariants, direct/
prepared/held consumers, full diagnostics, omissions versus automatic coercion,
PUBLIC SELECT and native retained-row/Reset/Restore controls. PostgreSQL reads
are restricted to the same candidate pairs; additional PostgreSQL catalog rows
are legitimate. Domain identity via pg_typeof is a PostgreSQL-only phase;
native domain creation/conversion plus its unchanged exact complete bag excludes
domain rows without a new type identity API.

The genuine feature smoke combines empty shape, representative eight-row
metadata, held valid/missing/NULL/error/recovery, signed OID conversion, BOOL
text, JSON I/O, VARCHAR sizing/atomicity, UTF8 diagnostic and actual native user
Dump replay against PostgreSQL. Discovery-only filters have been removed.
Run the same completed definitions in both modes before review/integration:

```sh
make check RUN='^TestReferencePGCast'
make check REFERENCE=1 RUN='^TestReferencePGCast'
make check RUN='^TestReferencePGCastFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGCastFeatureSmoke$'
make check RUN='^TestCastCatalog'
make check RUN='^TestPGCast'
make check RUN='^TestReference(OID|CastExplicit|Character|JSON|JSONB|PGSequence|PGDepend|PGDescription|QueryOrderCatalogChar)'
make check REFERENCE=1 RUN='^TestReference(OID|CastExplicit|Character|JSON|JSONB|PGSequence|PGDepend|PGDescription|QueryOrderCatalogChar)'
```

Every quickcheck invocation has a900-second deadline. Private units exercise
owner planning/application in all claimed and excluded contexts and projection
ownership/cancellation/generation retention, without issuing SQL. Selective
runs and smoke do not replace ordinary quickcheck or the post-merge finalcheck
under its single1800-second deadline.
