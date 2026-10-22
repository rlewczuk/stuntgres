# reference/tables

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### Partition pruning and constraint exclusion

`TestReferencePartitionPruningRange`, `List`, `Hash`, `Nested`, `Expressions`,
`Inheritance`, `Prepared`, `Errors`, `Controls`, `FeatureSmoke` and `ComplexPredicate` share the
public fixture and direct/prepared bodies across native and pinned PostgreSQL18.6.
They independently prescribe typed bags (including duplicates), NULL/default
boundaries, lexicographic/nested keys, physical CHECK UNKNOWN/NOT VALID/NO INHERIT,
same-held rebinding, empty metadata and complete logical diagnostics.
Native-only phases exercise actual Dump/Reset/Restore, retained rows/types,
held rebinding after replacement, independent destination types and the actual
emitted SQL stream on PostgreSQL. Native policy-expression enforcement and
partition row-lock errors remain explicit boundaries, not fake PG parity.
`ComplexPredicate` uses twelve independent disjunctions and late key restrictions
over populated/empty RANGE branches and DEFAULT. Direct and same-held bindings
99,1,3,NULL,99 independently expect bags [99,99], [1], empty, empty, [99,99],
with k/INT4 metadata and a retained nonmatching row rejected by the residual.

Run each completed public group in both modes:

```sh
make check RUN='^TestReferencePartitionPruning(Range|List|Hash)$'
make check RUN='^TestReferencePartitionPruning(Range|List|Hash)$' REFERENCE=1
make check RUN='^TestReferencePartitionPruning(Nested|Expressions|Inheritance)$'
make check RUN='^TestReferencePartitionPruning(Nested|Expressions|Inheritance)$' REFERENCE=1
make check RUN='^TestReferencePartitionPruning(Prepared|Errors|Controls|FeatureSmoke)$'
make check RUN='^TestReferencePartitionPruning(Prepared|Errors|Controls|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferencePartitionPruningComplexPredicate$'
make check RUN='^TestReferencePartitionPruningComplexPredicate$' REFERENCE=1
```

The public results deliberately promise no optimizer evaluation schedule.
Internal actual-producer proof is the companion evidence that excluded physical
branches really are not demanded, rather than merely filtered after a full scan:

```sh
make check RUN='^Test(PartitionPruningProof|QueryBranchPruning|PartitionPruningExecution|PartitionPruningSafety)$'
make check RUN='^Test(PartitionPruningExecution|PartitionPruningSafety)$' RACE=1
make check RUN='^TestPartitionPruningSearch(Budget|Bound)$'
make check RUN='^TestPartitionPruningSearch(Budget|Bound)$' RACE=1
```

Feature smoke argv is `make check RUN='^TestReferencePartitionPruningFeatureSmoke$'`
and the identical command with `REFERENCE=1`. Expected observations are exact
duplicate1/boundary3/default5/NULL bags, explicit LIST NULL/default ownership,
complete HASH lookup, CHECK-permitted NULL/NO INHERIT descendants, same-held
rebinds and typed empty results; native controls repeat restored boundary/NULL
bags and replay the actual dump on PostgreSQL. Use external900-second deadlines
for selected commands; quickcheck is reserved for whole-project verification.

### Foreign writes

`TestReferenceFDWWriteActions`, `Identity`, `Returning`, `Batch`, `Copy`,
`Truncate`, `Constraints`, `Transactions`, `Diagnostics` and `Consumers` share
the same public SQL definitions across the native registered SQL writer and
genuine pinned PostgreSQL postgres_fdw loopback. Actual independent source SQL
defines/observes effects; no expected-row echo or compensation implements writes.
The native fixture owns actual `sql.Tx` handles, statement/public savepoints,
shared participating scans and real INSERT/UPDATE/DELETE/TRUNCATE.

Independent assertions cover exact typed rows/counts/RETURNING, original hidden
identity and duplicate-visible tuples, cached prepared execution, batch bounds/
remainder/fallback/suppression, text/CSV/binary COPY and terminal failures,
per-server grouped TRUNCATE and sequence restart, CHECK/NOT NULL nonenforcement,
STORED/VIRTUAL generation, savepoint/rollback/observer isolation, teardown and
conversion failure, missing callbacks, native contract refusal and partial commit.
Native provider SQL rewriting/suppression and PostgreSQL's genuine remote trigger
are distinct explicitly labeled phases. PostgreSQL postgres_fdw sends DEFAULT
for generated attributes; the native provider stores actual full callback inputs.
The shared parameterized STORED division-by-zero INSERT/UPDATE witnesses prove
core computation independently of that transport distinction.

View CHECK OPTION witnesses cover both proposed-pass/returned-fail rollback and
proposed-fail/returned-pass success through actual remote trigger/provider SQL
rewrite. WCO validates actual returned tuples before RETURNING and forces singles.
Native participating cursor FETCH requests establish their own statement marks
while retaining the same genuine provider transaction and scan lifecycle.

Run each selected group both natively and with `REFERENCE=1`, with an external
900-second deadline:

```sh
make check RUN='^TestReferenceFDWWrite(Actions|Identity|Returning)$'
make check RUN='^TestReferenceFDWWrite(Actions|Identity|Returning)$' REFERENCE=1
make check RUN='^TestReferenceFDWWrite(Batch|Copy|Truncate)$'
make check RUN='^TestReferenceFDWWrite(Batch|Copy|Truncate)$' REFERENCE=1
make check RUN='^TestReferenceFDWWrite(Constraints|Transactions|Diagnostics|Consumers)$'
make check RUN='^TestReferenceFDWWrite(Constraints|Transactions|Diagnostics|Consumers)$' REFERENCE=1
make check RUN='^TestFDW(ModifyCapabilities|ModifyIdentity|ModifyLifecycle|TransactionParticipants|ProviderContractErrors)$'
make check RUN='^TestFDW(ModifyLifecycle|TransactionParticipants|ProviderContractErrors)$' RACE=1
make check RUN='^TestReferenceFDWWrite(Transactions|Consumers)$' RACE=1
make check RUN='^TestReferenceTruncateProviderControls$'
make check RUN='^TestReferenceTruncateProviderControls$' REFERENCE=1
```

R3's shared `CombinedSourceFrame` subtest covers direct and prepared UPDATE FROM
and DELETE USING with target, matched-source, and OLD/NEW RETURNING projections.
Duplicate source matches have identical projected values, so no unspecified
PostgreSQL winner is asserted.

R3's shared `VirtualSourceIdentity` subtest isolates RETURNING-only,
predicate-only, assignment-only, JOIN-condition-only and array-target-only
virtual demand. Direct/prepared correlated LEFT JOIN cases distinguish present
virtual values from null-extended rows; demanded division-by-zero cases check
rollback of genuine provider mutations. Fresh source observations check the
successful committed effects and failure atomicity.
Run the complete changed definition with
`make check RUN='^TestReferenceFDWWriteReturning$'` and
`make check RUN='^TestReferenceFDWWriteReturning$' REFERENCE=1`.

R8's shared `PreparedOriginReuse` covers empty
and row-bearing detached entries while the origin owns a later genuine foreign
transaction, including independent concurrent commit/rollback completion.
The migrated TRUNCATE provider witness is
`TestReferenceTruncateProviderControls/ForeignTruncateProviderBoundary`;
`TestReferenceTruncateBoundaries` is a separate definition.
`TestFDWModifyIdentity/KeyedMembership` exercises typed hashing, repeated and
owned-byte identities, forced collisions, signed zero/NaN/time semantics, and
50,000 distinct collision-free buckets with a structural one-entry lookup bound.
No timing threshold or production comparison counter supplies the work oracle.
Driver-extension coverage distinguishes comparable decimal values/pointers from
noncomparable slice/struct/nested-interface values; native R2 checks rejection
before mutation with unchanged real source rows and no invented SQLSTATE.

R8's native `ProviderContractErrors` subtest injects direct/wrapped/joined
ErrBadConn/ErrSkip after genuine source mutation, source/modify End and genuine
remote commit, via direct and prepared database/sql. Each callback runs once:
precommit faults roll back actual source work; postcommit faults fail without
inventing undo or allowing replay. No PostgreSQL callback-sentinel parity is
claimed. Its focused selector is
`make check RUN='^TestReferenceFDWWriteTransactions/memory/ProviderContractErrors'`.

R8's shared `HeldCommit` subtest runs identical SQL against both real providers:
unfetched and partially fetched ordered cursors retain their remaining rows
after COMMIT. Focus it with
`make check RUN='^TestReferenceFDWWriteTransactions/postgres/HeldCommit' REFERENCE=1`
and `make check RUN='^TestReferenceFDWWriteTransactions/memory/HeldCommit'`.

R8's native `ParticipatingHoldSettlement` covers unfetched/partially fetched HOLD,
multiple cursors sharing one mark, native `sql.Tx.Commit`, SQL COMMIT/AND CHAIN,
and implicit HOLD. It checks source End → EndStatement → Commit ordering, real
rollback on drain/End/statement/preflight/cancellation failures, and removal of
only newly promoted cursors after unsuccessful publication.
Use `make check RUN='^TestReferenceFDWWriteTransactions/memory/ParticipatingHoldSettlement'`.
The `TestFDWTransactionParticipants/HoldSettlement` unit checks borrowed mark
ownership; `ChainReadmission` also retains a newly committed HOLD promotion when
the next transaction loses its catalog epoch. Both are included in the unit
group above; race mode is selected with `RACE=1`.

Feature smoke (repeat for `Copy`, `Truncate`, `Transactions`):

```sh
make check RUN='^TestReferenceFDWWriteActions/memory/Smoke'
make check RUN='^TestReferenceFDWWriteActions/postgres/Smoke' REFERENCE=1
```

Actions inserts4/updates2/deletes3, each count1, with source rows1/2/4. Copy
imports2 with `COPY 2` and late malformed input adds none. Truncate empties two
actual same-server sources/count0. Transactions sees its own staged9 while an
observer cannot, rolls9 back, then commits10 visible to the observer.
Backend selectors intentionally have no interior `$`, which Make would consume.
These commands specify proof; observed results belong to the verification report.
COPY uses the shared fixture transport adapter also used by existing dml
CopyText/CopyCSV/CopyBinary definitions; those callers retain dual-mode regression
coverage. Existing unconfigured wire CLI fixtures cannot inject a registered
provider: public CopyFrom plus direct engine CopyProtocol terminal-veto tests
cover the writable provider route without inventing server configuration.


### Foreign metadata and scans

The `fdw_*_test.go` scenarios independently prescribe SQL/MED catalogs, ordered
options/atomicity, transactional dependencies, mapping ownership/security,
typed scans, local rechecks, genuine correlated restart and real IMPORT.
Native scans read an independent in-process SQL database through a concrete
registered reader; each Iterate executes finite source SQL FETCH and owns no
first-request Rows/context across logical cursor requests. PostgreSQL uses actual
postgres_fdw loopback over committed source tables. Neither path uses canned rows.

`pg_user_mapping_test.go` adds focused exact raw-catalog shape, identity/options
and disclosure witnesses. The four attributes are OID/OID/OID/TEXT[]; only options
are nullable, PUBLIC has role OID0, and no-options SQL NULL differs from an
empty-string option value. Actual command-created mapping IDs are positive and
distinct, joined to exact server/role identities rather than pinned allocator
numbers. Direct and prepared reads retain ordered and escaped option elements.
Raw access uses ordinary captured relation SELECT ACLs, including same-held role
changes and empty-result denial with full42501 diagnostics. PostgreSQL additionally
exercises successful explicit grant/revoke transitions. Native catalog-source
GRANT/REVOKE retains its existing exact0A000 unsupported boundary: rejected changes
leave the stored ACL unchanged and direct/fresh-prepared/held reads denied, without
leaking options. This is not a special native superuser-only read gate. Companion-view
witnesses distinguish exact current user, direct/inherited USAGE, server owner,
PUBLIC and membership in another mapped role. These metadata-only fixtures use
NO HANDLER/NO VALIDATOR and assert no native provider activity; they neither
mock external connections nor add a native transport.

Focused mapping proof (run both commands under an external900-second deadline):

```sh
make check RUN='^TestReference(PGUserMapping(Shape|Metadata|Security)|FDW(MetadataLifecycle|Security|SourceSecurity|MappingOwnership|NativeControls))$'
make check RUN='^TestReference(PGUserMapping(Shape|Metadata|Security)|FDW(MetadataLifecycle|Security|SourceSecurity|MappingOwnership|NativeControls))$' REFERENCE=1
make check RUN='^(TestPGUserMappingAttributeFacts|TestPGAttribute(Shape|Capture)|TestCatalogSourceProducerCoverage)$'
```

Mapping feature smoke is the metadata scenario's own cohesive subtest:

```sh
make check RUN='^TestReferencePGUserMappingMetadata$'
make check RUN='^TestReferencePGUserMappingMetadata$' REFERENCE=1
```

Expected observations: four real mapping/server/role relationships, PUBLIC0,
exact quoted/escaped and empty-string option values, typed no-options NULL,
four attribute rows with nullable umoptions/collation950/dimension1, raw42501
and redacted companion options as the reader, then unredacted superuser options.
Metadata inspection does not enter the registered native reader. Pinned
PostgreSQL18.6 supplies the real oracle; whole-project Docker-free verification
uses `make quickcheck` rather than widening these focused selectors.

Selected proof groups (run each with and without `REFERENCE=1`):

```sh
make check RUN='^TestReferenceFDW(MetadataLifecycle|OptionsAtomicity|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferenceFDW(Transactions|Dependencies|Security|MappingSelection)$' REFERENCE=1
make check RUN='^TestReferenceFDW(Scan|ProjectionTypes|LocalRechecks|Rescan)$' REFERENCE=1
make check RUN='^TestReferenceFDW(Import|ImportAtomicity|MissingSupport|PreparedIdentity)$' REFERENCE=1
make check RUN='^TestReferenceFDW(MappingOwnership|CursorLifecycle)$' REFERENCE=1
make check RUN='^TestReferenceFDW(Registration|Validator|Lifecycle|Concurrency|NativeControls)$' REFERENCE=1
make check RUN='^TestReferenceFDW(LocalConsumers|Conversion|SourceSecurity|ImporterCapability|MetadataActions)$' REFERENCE=1
make check RUN='^TestReferenceFDW(WrapperAlterBoundaries|GrantDiagnostics)$' REFERENCE=1
make check RUN='^TestReferenceFDW(ObjectGrants|RoutineGrants)$' REFERENCE=1
make check RUN='^(TestFDWProcedureACLCOW|TestFDWProcedureIdentity|TestFDWRegistry)$' RACE=1
make check RUN='^TestReferenceFDWLateDependencies$' REFERENCE=1
make check RUN='^TestFDWLateDependency(Publication|OwnRewire)$'
make check RUN='^TestFDW(OptionsTransform|CatalogOverlay|Registry|ProcedureIdentity|PlanningSelection|ScanState)$'
make check RUN='^TestReferenceFDW(Lifecycle|Concurrency|Rescan|Security|SourceSecurity|PreparedIdentity|MappingOwnership|CursorLifecycle|LocalConsumers|NativeControls)$' RACE=1
```

Feature smoke selectors are `^TestReferenceFDWScan$`,
`^TestReferenceFDWRescan$` and `^TestReferenceFDWImport$`, each in both modes.
Expected data includes real rows `(1,'one',true),(2,NULL,false),(3,'three',NULL)`;
committed source updates become visible on later scans; correlated keys
`1,2,1` retain both occurrences of row1 and the NULL row2 label; import creates
only the selected actual source definitions and a later collision rolls back
earlier creations.
Mapping-selection, source-security and wrapper-boundary readers receive schema
USAGE explicitly in both modes. Table ownership and explicit SELECT ACL remain
independent; no schema-owner workaround supplies otherwise absent privileges.
Privileged PostgreSQL mapping setup sets `password_required 'false'` for the
trust-authenticated loopback. Both PUBLIC and exact mappings are configured by
the administrator; the reader still exercises fallback, override and removal.
The separate security scenario retains reader self-management permission proof.
Diagnostic discovery includes complete WARNING/NOTICE records and a Unicode
character-position syntax witness, not byte-offset or SQLSTATE-only parity.
Wrapper ALTER boundary regressions share direct/prepared bare and mixed
handler/validator-plus-RENAME/OWNER rejections, unchanged executable metadata and
legitimate inherited-owner RENAME. Invalid GRANT/REVOKE regressions distinguish
wrapper/server privilege names and column privileges, preserve ACLs, and exercise
the original USAGE grants through actual server/mapping operations afterward.
Object-grant scenarios pin raw NULL versus explicit owner-USAGE defaults,
owner self-grants/recovery, actual server/table creation denial, independent
grant origins and RESTRICT/CASCADE. Registered-routine grants retain same-held
raw ACL readers across ordinary/options/PUBLIC changes and class-specific schema
expansion. PostgreSQL additionally exercises its privilege inquiry functions;
native lookup-only inquiry identities retain explicit unsupported diagnostics.
Real callback validation and scans remain usable after ordinary PUBLIC EXECUTE
is revoked, matching SQL/MED's internal-dispatch contract.
`TestReferenceFDWObjectGrantCleanup` covers both wrapper/server received USAGE
and grant-option cleanup, dependent versus independently supported origins,
same-held raw ACL readers, unchanged identities/options, actual creation denial,
successful role retirement and late-target atomicity. Run
`make check RUN='^TestReferenceFDWObjectGrantCleanup$'` and the same command with
`REFERENCE=1`.
Native API phases distinguish registration/identity from transport, prove
validators receive full ordered options, and inject failures only around real
source operations. Resource ownership and stable actual data supplement counters.
Finite FETCH retains a live source; consuming/closing its public batch does no
I/O. CLOSE/EOF/savepoint/transaction/session disposal and HOLD promotion finish
the actual producer and surface End-only errors. Replacement preserves deferred
stale-owner cleanup rather than claiming all source resources close at Reset.
New or changed definitions require selected native and pinned real runs before
review; these commands are proof instructions, not claims of exercised passes.
The concrete SQL reader discovers source tables/attributes through supported
catalogs and obtains actual column codec metadata with `ColumnTypes` on a
zero-row SELECT. This discovery remains independent of type-catalog projection
and does not invent user-defined spellings. Unsupported codec metadata fails explicitly.
Rescan witnesses use the existing scalar-correlated SELECT seam, not an
otherwise unavailable LATERAL-derived-query capability. SQL/MED rollback uses
the common private catalog overlay; generic OWNED-in-transaction exclusions
are unchanged, with mapping rollback tested through DROP USER MAPPING.
Accepting a private catalog write assigns the transaction's canonical public XID
at the final overlay preflight boundary, preserving its existing completion lock
and commit/rollback ownership. It neither allocates a separate catalog identity
nor publishes the private metadata as a committed catalog.

`TestReferenceFDWLateDependencies` covers wrapper←server, server←PUBLIC mapping
and server←foreign table, each under RESTRICT and CASCADE. The pinned PostgreSQL
oracle deliberately rolls back session A's staged retirement after observing
either session B's actual CREATE completion or its real lock wait behind A.
It then joins B and asserts the complete preserved graph. This safe boundary
does not exercise PostgreSQL's dropped-parent COMMIT outcome or establish native
lock parity; quiet passing output does not classify which oracle branch ran.

The independent native schedule requires B to commit its new dependent while A
remains open, then requires A's COMMIT to fail with the ordinary publication
conflict and no SQLSTATE. Fresh catalog consumers must see the same provider
identities and complete new dependency edges/options after recovery, with no
source callbacks. Internal `TestFDWLateDependencyPublication` additionally uses
genuine catalog stages for registered handler/validator and transitive retirement
through direct, retained and eligible active publication; it does not pretend
public explicit-transaction DROP SCHEMA is supported.
Foreign CHECK definitions use the common descriptor/dependency validation without
making local scan-time CHECK enforcement a provider contract. Held result batches
retain a dedicated recorder, so repeated identical FETCH text cannot substitute a
later operation's recorded cells. Batch cleanup precedes SQL cleanup and connection
return, including assertion failures. Custom registration/control SQL pools use
the same recording middleware. Dump/Restore use a separate genuine native pool
over the same configured connector and engine, never a wrapped control bypass.
The raw session-close companion deliberately consumes actual unwrapped rows with
explicit native metadata/value checks.
Registered handler/validator procedures participate in schema retirement through
the existing dependency closure. Native controls move a real foreign table outside
its handler's schema, then assert CASCADE removes the callable, wrapper, server,
mapping and external table without invoking application callbacks. Generic routine
DROP now has its own catalog/dependency owner; this does not expand foreign
language declarations or execute external functions.
The RESTRICT companion freezes the complete dependency diagnostic and preserves
namespace/callable/wrapper/server/mapping/table/view identities plus row/array type
edges and actual rows. Finalized rows remain consumable after CASCADE. Warmed
prepared scans and wrapper creation cannot revive dropped identities, even after
same-name schema recreation; only a genuine native Reset reseeds registration and
permits real prepared execution again. The independent SQL source remains intact.
Qualified handler lookup preserves ordinary namespace precedence: a dropped
schema reports positioned `3F000`; recreating the schema without its callable
then reports `42883`. Named retirement/recreation/reseed subcases distinguish
these failures without relaxing the no-callback or no-resurrection assertions.

### Inherited DML and schema effects

The untagged `inheritance_dml_*` and `inheritance_alter_*` definitions specify
direct/prepared default, star and ONLY targets; physical INSERT; nonprefix
scatter and diamond bags; parent ACL/RLS; physical constraints/generation;
FROM/USING, modifying CTE and CURRENT OF; held topology/replacement; recursive
ALTER provenance and nonrecursive provider distinctions. Rows, affected counts,
metadata and complete English diagnostics are independent expectations, not
native-derived goldens or parity alone.

All definitions use the shared native/PostgreSQL fixture without availability
filters. Pinned oracle observations, native behavioral red and completed
dual-mode implementation proof remain separate.

`TestReferenceInheritanceAlterQualifiedUsing` asserts the pinned PG18 boundary:
`p.a` and `public.p.a` are valid on a childless target, but the same qualified
USING expression in inherited TYPE recursion fails with positioned42P01.
Unqualified USING rewrites differently ordered physical descendants, preserves
NULL and child-only data, and rolls back all descriptors/rows on child22003.
Run its exact selector in both modes; release-source cooked-expression mapping
alone does not establish qualified-syntax acceptance.


### Inheritance relationship maintenance

`TestReferenceInheritanceMaintAttach`, `Compatibility`, and `RetiredDetach`
share direct/prepared SQL through the existing inheritance fixture, without
backend filters. Attach asserts nonprefix child layout, local default9 versus
parent default7, unchanged CHECK/NN identities, differing generated recipes,
child/subtree visibility, and typed values/column metadata. Compatibility
specifies complete diagnostics, empty edges/counters and preserved child rows
after rejection.

```sh
make check RUN='^TestReferenceInheritanceMaint(Attach|Compatibility|RetiredDetach)$' REFERENCE=1
make check RUN='^TestReferenceInheritanceMaint(Attach|Compatibility|RetiredDetach)$'
```

Use an external 900-second deadline. These tests do not themselves establish
implemented native support; root records oracle and native-red results separately.
Pinned PostgreSQL18.6 discovery confirmed the independently specified history
expectations: last detach retains historical-only NN/CHECK count1, nonlocality,
identity, validity, definitions and enforcement; deleting the former parent
leaves them intact. Reattaching a parent without those facts leaves count1;
attaching a same-name new parent with recreated facts adds a new contribution
(count2), then detach returns to1. CHECK/NN DROP fails with exact42P16 and no
optional diagnostic fields. Detached recursive TYPE succeeds with affected0,
removes child and leaf constraint catalog facts, retains physical attnotnull,
and yields BIGINT values12,13 with unchanged b values20,30 and column locality.
Isolated subcases preserve each DROP/TYPE and parent-lifetime witness. Genuine
native replay remains in the later controls batch; these tests make no
persistence claim.

Additional maintenance definitions cover `Detach`, `DiamondOrdinals`,
`MixedLists`, `Propagation`, `DropOnlyCascade`, `OnlyMixedMigration`, `Syntax`,
`Admission`, `Lifecycle`, `Controls`, and `FeatureSmoke`. All use the shared
backend loop without discovery filters. Their SQL outcomes were independently
specified and pinned against PostgreSQL18.6; native implementation and actual
control phases require their own selected proof.

```sh
make check RUN='^TestReferenceInheritanceMaint(Detach|DiamondOrdinals|MixedLists|Propagation|DropOnlyCascade|OnlyMixedMigration|Syntax|Admission|Lifecycle|Controls|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceInheritanceMaint(Detach|DiamondOrdinals|MixedLists|Propagation|DropOnlyCascade|OnlyMixedMigration|Syntax|Admission|Lifecycle|Controls|FeatureSmoke)$'
make check RUN='^TestInheritanceAlter(OnlyMixedActions|CheckProvenanceAtomicity)$'
```

Use an external 900-second deadline for each invocation. The migrated public
ONLY mixed-action union includes generated child recipe identity, root-only
UNIQUE, complete rejection diagnostics/unchanged facts, and CHECK replacement
identity/locality/atomicity. Internal tests now invoke actual candidate/event/
stage seams without SQL execution, retaining immutable graph, generation and
provider-identity assertions. Unrelated internal admission/history fixture
migrations remain for their production-round seams.

Lifecycle cancellation distinguishes runtime contracts without dropping the
relationship witness: native direct/prepared precanceled calls return ordinary
context errors without mutation; PostgreSQL queues the actual ALTER behind a
parent AX hold, observes server admission, sends `pg_cancel_backend`, joins the
operation, and asserts complete57014 plus a nil result. Both routes assert
unchanged edges and reexecute the same preheld child consumer on the same owner.
No fast pq precancel outcome is treated as deterministic. Ownership scenarios
establish actual schema USAGE grants before switching roles.

The actual feature smoke selector is
`^TestReferenceInheritanceMaintFeatureSmoke$`, run once per mode. Expected
observations are attach parent rows1,2,3/ONLY1; missing-column42804 without an
edge; propagated flag7; inherited DROP CASCADE42P16; detach retaining child2,3;
later parent-only column; genuine native Dump/Reset/Restore and actual emitted
bytes replayed on PostgreSQL when available; retained native eager rows; future
child default9; surviving g→c edge after former-parent DROP. Controls separately
specify exact live sparse ordinals1,3 versus normalized logical replay1,2 and
detached historical constraints after former-parent removal. Invalid late and
canceled native Restore must preserve an independent sentinel and imported rows.
Native-control phases have no PostgreSQL API equivalent; their genuine emitted
bytes are replayed on PostgreSQL in real mode. Passing SQL-only oracle discovery
does not establish that native controls or emitted-byte replay have passed.

Review regressions are isolated in
`TestReferenceInheritanceMaintReviewTemporaryHistory`, `ReviewErrorOrder`, and
`ReviewDetachKinds`. TEMP history uses common direct/prepared SQL around a real
native Reset or Restore phase; constraint identities, locality/counters,
enforcement, rows and leaf ancestry survive last detach and former-parent DROP.
Native controls have no PostgreSQL equivalent. Mixed-list cases independently
specify earlier incompatibility/binding/drop errors versus later missing or
unauthorized parents, atomic rollback and released relation grants. Detach kind
cases distinguish typed/partitioned missing-parent relationships from actual
partition rejection. A mixed physical partition list retains the native
capability rejection while PostgreSQL independently reports its nonparent error.
Lock cleanup observes all participants' AX/SUE grants on the isolated scenario's
relations through supported `pg_locks` columns, without requiring backend-PID
function support.

All review definitions now use the unfiltered shared backend loop. Pinned
PostgreSQL discovery established the common SQL outcomes; native replacement,
immutable sources and final publication still require their own exercised proof.

```sh
make check RUN='^TestReferenceInheritanceMaintReview(TemporaryHistory|ErrorOrder|DetachKinds)$' REFERENCE=1
make check RUN='^TestReferenceInheritanceMaintReview(TemporaryHistory|ErrorOrder|DetachKinds)$'
make check RUN='^TestInheritanceMaintReview(TemporaryHistory|ContributionLimit)$'
make check RUN='^TestInheritanceMaintReview(AbsentEndpoint|MarkerAdmission)$' RACE=1
```

The package-level TEMP witness captures immutable source graphs across genuine
catalog replacement. The signed16 candidate witness separately admits32767 and
rejects32768 for column origins and live-plus-retired NN/CHECK totals, without
constructing32768 public SQL objects. Actual AX-wait absence→presence veto and
SUE-wait first-child marker reconciliation units both exercise final Finish,
preserve concurrent catalog work and join their workers on failure.
The TEMP dependency witness first establishes the real pre-detach child edge,
then rejects every residual dependency except the former parent's own automatic
row-type array. Automatic type ownership is not an external child/history edge.

Run each bounded group with `REFERENCE=1` and without it; each invocation has
an external 900-second deadline:

```sh
make check REFERENCE=1 RUN='^TestReferenceInheritanceDML(Forms|Diamond|InsertPhysical)$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceDML(Generated|Atomicity)$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceDML(Authority|RLS)$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceDML(Consumers|CTE|CurrentOf)$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceDMLPreparedControls$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceAlter(Columns|Origins)$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceAlter(NotNull|Checks|ConstraintOrigins)$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceAlter(Defaults|Rename|Generated)$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceAlter(PhysicalConstraints|PhysicalIdentity)$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceAlter(PhysicalSecurity|PhysicalNames|Atomicity)$'
make check REFERENCE=1 RUN='^TestReferenceInheritance(Boundaries|AlterBoundaries)$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceDMLFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceAlterFeatureSmoke$'
```

Smoke independently expects physical INSERT1, diamond UPDATE5, ONLY UPDATE2,
late-child23514 with no partial writes, held INT4/TEXT RETURNING, real native
pre-delete Dump/Reset/Restore, retained eager rows and DELETE2+3. ALTER smoke
expects recursive DEFAULT/ADD/TYPE/rename/CHECK/generation and ONLY localization
with genuine replayed final data and origin flags. Native controls are explicit
native phases; PostgreSQL asserts the prescribed live SQL state, not a fictitious
server Restore API. Root's standalone public process complements these tests.

### Inherited definition replay and merge-stop proof

`TestReferenceInheritanceAlterNoInheritControls` covers captured custom/quoted
NN names, childless NoInherit facts, actual native Dump/Reset/Restore and pinned
replay of its emitted bytes. NoInherit/PK incompatibility is a full 55000
rejection with unchanged state; compatible local NN/PK sharing and FK-to-child
data dependencies use separate genuine definitions. Native public catalog writes
and unpaired, mismatched, consumed, star-qualified and primary-backed repairs
remain rejected. Script replay requires existing superuser catalog-write
authority; it does not serialize roles or grant new SQL capabilities.
The FK-to-child dependencies are genuine validated qualified ONLY ADD events
after both physical row streams and actual target-key readiness, not an inline
forward reference or a NOT VALID workaround. Their native importer requires
fresh ordinary ancestor/descendant identities and captured owner/key/provider
observations; its incumbent pending binder validates loaded rows once before
publication. The replay script uses the same existing superuser reconstruction
context while preserving relation ownership.

`TestReferenceInheritanceAlterRecipeControls` covers nonlocal DEFAULT9, explicit
NULL/absence, retained ONLY-parent recipes, real deferred regclass dependencies,
STORED/VIRTUAL overrides, divergent multi-parent recipes, CHECK local/diamond/
other-parent origins and mixed owners. `GeneratedReplaySyntax` independently
constructs genuine compatible typed-NULL VARCHAR(5) declarations, then real
physical ONLY SET recipes and verifies full typmod/kind/nonlocal metadata.
No final placeholder, forced attribute localization or fictitious PG Restore
stands in for actual native dump-byte replay.
The mixed-owner case provisions the same owner-role inventory on both backends
before native emitted bytes are replayed, then uses that existing role in the
PostgreSQL callback. Roles are a replay prerequisite, not serialized role DDL.
Every fresh PostgreSQL public-schema reset also reapplies those roles' schema
USAGE before actual dump-byte replay; schema ACLs are not synthesized by Dump.
Cleanup runs after fixture borrowers close and its schema is reset.

`TestReferenceInheritanceAlterMergeStops` separates existing-fact localization,
visited-child ownership, below-merge recursion stops, validity compatibility,
no historical revalidation and inline NN/CHECK propagation after a compatible
column merge. Preparatory locks are independently exercised by the owning
locking package; a metadata stop alone is not evidence of lock elision.

Selected completed proof requires both modes, with external 900-second deadlines:

```sh
make check RUN='^TestReferenceInheritanceAlter(NoInheritControls|RecipeControls|GeneratedReplaySyntax|MergeStops)$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceAlter(NoInheritControls|RecipeControls|GeneratedReplaySyntax|MergeStops)$'
make check RUN='^TestInheritanceDML(Admission|Locator|Authority|Contention|Replacement|AtomicPublication)$'
make check RACE=1 RUN='^TestInheritanceDML(Contention|Replacement|AtomicPublication)$'
make check RUN='^TestInheritanceAlter(Origins|Atomicity|DefinitionAuthority)$'
make check RUN='^TestRestoreNotNullNoInherit(Syntax|Authority)$'
make check RUN='^Test(InheritanceForeignKeyForwardReplay|RestoreInheritedForeignKey(Boundary|Observations|Atomicity))$'
make check RUN='^TestInheritanceMutationTargetsAST$'
```

#### Constraint transitions and disconnected recipe components

`TestReferenceInheritanceAlterConstraintTransitions` specifies retained
local/shared columns' historical NN/CHECK metadata after parent-column DROP,
DROP/SET NN and DROP/ADD CHECK replacement identities and atomicity, existing
invalid NN completion, and a reached child's NO INHERIT SET rejection.
ONLY cannot validate an inheritable invalid NN while children remain; its
childless positive preserves identity and does not invent NO INHERIT status.

`TestReferenceInheritanceAlterRecipeComponents` uses two disconnected affected
generation graphs with actual cross-component regclass providers and cyclic
defaults requiring deferred definitions. Parent-named VIRTUAL projections use
the named recipe; ONLY child queries retain each physical child's own recipe.
Native controls replay actual dumped bytes. Both definitions are unfiltered;
run the following with and without REFERENCE=1 before review:

```sh
make check REFERENCE=1 RUN='^TestReferenceInheritanceAlterConstraintTransitions$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceAlterRecipeComponents$'
```

#### Retired constraint lineage

The `TestReferenceInheritanceAlterRetired` definitions independently cover
attribute-rank compaction, sibling type divergence, repeated source lifetimes
and mixed valid/invalid parent facts. Counts are live support plus actual archived
identities, not padded current origins or forced local declarations. TYPE removes
historical-only nonlocal facts; genuine later local constraints get new OIDs.
`TestReferenceInheritanceAlterRetiredPrimaryType` separately checks physical
primary-key not-null enforcement after the historical NN catalog row disappears.
Native controls replay the actual Dump bytes on PostgreSQL and repeat native
Dump/Reset/Restore, rather than substituting an equivalent reconstructed fixture.
Physical-NN source witnesses separately assert the actual PK/drop/later-TYPE
flag lifetime, canonical NN/dependency cleanup, and genuinely nullable new
children. Fresh childless ONLY SET creates an ordinary NN fact (NoInherit=false);
the guarded keyless constructor does not infer otherwise from retained facts
created while children existed.

The nine `PhysicalNotNull` definitions cover `Transitions`, `CatalogConstruction`,
`NewChild`, `KeylessConstruction`, `RestoreBoundary`, `ExistingIndex`,
`IndependentPrimary`, `SelfForeignKey` and `PermutedForeignKey`. Native positive
catalog-construction counterparts use genuine public Restore controls, not
unsupported public native catalog writes. Actual native dump bytes run through
two Reset/Restore cycles and real PostgreSQL replay. The canonical NN/pg_depend
CTE is a superuser script boundary with exact fresh constructor/identity/full-type
and single-use native authority; negative fixtures cover forged, mismatched,
reused, readonly, canceled and late-failing imports.

Keyless flag a is repaired before an unrelated real PK(b), retaining b's NN.
Separate backing shapes preserve KEY/INCLUDE/options/opclasses through genuine
USING INDEX. Self-FK proofs preserve actual PK support and original conkey/confkey;
the permutation fixture includes same-key UNIQUE and standalone competitors,
INCLUDE-payload TYPE remint and an external regclass view consumer. These are
actual dependency-order controls, not alternate FK or index algorithms.

Run these selected groups in both modes, with external 900-second deadlines:

```sh
make check REFERENCE=1 RUN='^TestReferenceInheritanceAlterRetired(Compaction|SiblingTypes)$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceAlterRetired(Lifetimes|MixedValidity)$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceAlterRetiredPrimaryType$'
make check REFERENCE=1 RUN='^TestReferenceForeignActionsValuesInheritance$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceAlterPhysicalNotNull(Transitions|CatalogConstruction)$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceAlterPhysicalNotNull(NewChild|KeylessConstruction)$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceAlterPhysicalNotNull(ExistingIndex|IndependentPrimary)$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceAlterPhysicalNotNull(RestoreBoundary|SelfForeignKey|PermutedForeignKey)$'
make check RUN='^TestReferenceInheritanceAlterPhysicalNotNull(Transitions|CatalogConstruction|NewChild|KeylessConstruction|RestoreBoundary|ExistingIndex|IndependentPrimary|SelfForeignKey|PermutedForeignKey)$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceAlterUsingIndexPrimary$'
```

Selected native lineage/importer proofs:

```sh
make check RUN='^TestInheritanceConstraintHistor(icalOrigins|yIssuer|yValidity)$'
make check RUN='^TestRestoreConstraintHistory(Syntax|Authority|Atomicity|PrimarySupport)$'
make check RUN='^TestInheritanceAlterAdmission(Growth|Replacement|Stops|PreparedEffects)$'
make check RUN='^TestInheritanceMetadataAdmission(Growth|Fences)$'
make check RUN='^TestInheritanceAlterOnlyMixedActions$'
make check RUN='^TestInheritanceUsingIndexPrepared(NotNull|Namespace|Diagnostics)$'
make check RUN='^TestPhysicalNotNull(CauseAndCurrentSupport|RetirementAndSupersession|RebuiltCompositeUsage)$'
```

Use `RACE=1` for the selected real admission/importer race proofs. The owning
internal tests exercise actual grants, provider preparation and atomic private
stages; a native completion alone is not evidence that a request queued.

`TestInheritanceMetadataAdmissionGrowth` covers SET/DROP DEFAULT and
column/constraint rename with genuine first-grandchild marker publication and
further growth after sticky subclass history. Real CREATE-INHERITS stages
construct fresh recipe/fact identities and rows while the metadata provider's
descendant AX request is observed queued. The tests reject partial metadata while
that request is blocked, then assert the complete consumer-visible utility result,
unchanged rows/storage and origins, both at provider completion and after statement
Finish. They do not impose shape-controller publication timing on metadata utilities.
`TestInheritanceMetadataAdmissionFences` retains new-descendant ownership,
cancellation and actual subsequent-definition replacement vetoes with atomic
rollback. Use the same selector with `RACE=1`. These private native admission
contracts do not claim a PostgreSQL concurrency outcome for that synthetic
publication interleaving; existing `TestReferenceInheritanceAlterDefaults` and
`TestReferenceInheritanceAlterRename` remain the dual-mode public SQL witnesses.

`TestReferenceInheritanceAlterUsingIndexPrimary` distinguishes root-only physical
attachment from KEY-only inherited NN propagation, fresh ONLY 42P16 rejection,
compatible existing-root stops and nonprefix KEY versus nullable INCLUDE. Native
`PreparedNotNull` additionally starts with an inherited-only NN and asserts actual
localization with stable OID/count and an untouched descendant. `PreparedNamespace`
binds a later CHECK through the genuinely renamed index in the same action list.
`PreparedDiagnostics` exercises actual rename NOTICE followed by name-collision
42P07, successful attachment NOTICE followed by a later CHECK binding 42703, and
an earlier missing-constraint skip NOTICE in the same list. Real child AX cancellation
emits no premature notices and publishes nothing; USING-only reaches its NN-prefix
wait after preparation, while a CHECK list can require preparatory grants first.
Frozen diagnostics and notices are consumed at their matching actual callback,
without suppressing earlier-phase failures or recooking later definitions.


### Integer series CHECK restriction

`TestReferenceSeriesCheckRestrictions` is an untagged shared native/PG witness.
It asserts the complete positioned CHECK SRF diagnostic and atomic noncreation
through direct and prepared DDL. The prepared utility errors at execute, not
prepare. Root runs both selected modes with external900-second deadlines:

```sh
make check RUN='^TestReferenceSeriesCheckRestrictions$'
make check REFERENCE=1 RUN='^TestReferenceSeriesCheckRestrictions$'
```

### Ordinary DETACH — oracle-first scenarios

The untagged `partition_detach_*_test.go` scenarios specify ordinary DETACH
independently: direct and held utility/reader/writer routes, retained reordered
child data/defaults, DEFAULT and nested roots, complete represented dependency
edge removal, stable relation/constraint/index identities, independent ownership,
full diagnostics, transactional writes/savepoints, actual queued AX admission,
cancellation, native catalog replacement, and canonical Dump/Restore.

All discovery filters are removed. `TestReferencePartitionDetachReplay` retains
the independently asserting pinned-oracle constructions as dual-mode coverage:
childless ONLY is valid, explicit index completion promotes, table reattachment
preserves invalidity, complete-invalid replay uses a real valid child, and nested
invalid ancestry uses bottom-up explicit index attachment without duplicate
backings. Controls test actual emitted streams, helper-name collision reservation,
no residual replay objects, failed/canceled replacement and bounded teardown
refusal. Nonowner ordinary-parent admission precedes kind rejection; schema and
DETACH timeout Positions are absent; late SELECT/INSERT observer Positions are
20/13 respectively. These contracts require completed-mode verification.

`TestReferencePartitionDetachReplayLocal` adds older equivalent local UNIQUE and
PRIMARY backings, native Dump/fresh Restore/replacement, unchanged PostgreSQL
replay, exact own constraint/index relationships, regclass/default/view consumers,
and independently expected duplicate-error winners before and after replay.

`TestReferencePartitionDetachReplayVirtual` covers UNIQUE/PRIMARY local virtual
trees competing with an attached plain tree: exact five-index inventory, real
constraint/backing ancestry, regclass default/view consumers and independent
future enforcing providers. Native controls use actual Dump/fresh and repeated
Restore, byte-stable redump and unchanged PostgreSQL replay. The nested replay
case now also exports its same combined six-index/valid-PK/invalid-extra state.
The temporary virtual recipe discovery file is removed after PostgreSQL40's
observed conventional-ADD outcomes; these completed definitions have no filters.

Dependency producers stay inside the admitted prerequisites: local exclusion is
created on the standalone child before ATTACH, and an unconstrained domain still
supplies real type/default/collation/rowtype dependencies. No hierarchy ADD or
domain-constraint capability is added. The dependent-abort transaction scenario
asserts native explicit-transaction DROP0A000 versus PostgreSQL2BP01 at execution,
including prepared DROP, then proves DETACH rollback and shared standalone
RESTRICT2BP01 protection. Private queued
replacement units use a real explicit holder and DDL worker, not an implicit
reader admission retained across Reset or a manually cleared admission flag.

```sh
make check REFERENCE=1 RUN='^TestReferencePartitionDetach(Diagnostics|Indexes)$'
make check REFERENCE=1 RUN='^TestReferencePartitionDetach(Values|NestedDefault|Dependencies)$'
make check REFERENCE=1 RUN='^TestReferencePartitionDetach(Transactions|Locks|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePartitionDetach(Replay|ReplayLocal|ReplayVirtual)$'
make check RUN='^TestPartitionDetach(Descriptor|Dependencies|Publication|TransactionOverlay|LockFences|VirtualRoots|ReplayEvents|ReplayLocalEvents|UsingIndexReplayAuthority|UniqueReplayAuthority|UniqueReplayPublication|UniqueReplayEvents|AST|ExcludedSyntax)$'
```

Repeat all reference selectors without `REFERENCE=1`; every new/changed definition
requires both completed modes before review/integration. Selected concurrency proof:
`make check RACE=1 RUN='^TestReferencePartitionDetachLocks$'`.
Use an external900-second deadline; quickcheck remains whole-project proof.

Feature smoke argv: `make check RUN='^TestReferencePartitionDetachFeatureSmoke$'`,
then the same with `REFERENCE=1`. Expected: detach0, parent `(3,c)`, child
`(1,a),(2,b)`, exact INT4/TEXT metadata, retained standalone uniqueness and OIDs,
child outside-old-range insert4, rollback restores attachment, and dropping the
former parent leaves child1,2,4. Native controls replay actual unchanged emitted
SQL on PostgreSQL, including invalid-but-complete virtual index flags; no
handwritten export substitute. Native Reset/Restore and eager retained cursors
have no PostgreSQL API counterpart. PostgreSQL-only late lock introspection
observes real default/candidate/descendant waits; native late-stage proof belongs
in actual private manager units, not fabricated SQL locks.

Hierarchy FKs, CONCURRENTLY/FINALIZE, trigger clones and inherited identity
capability remain outside this ordinary-detach feature. Supported views,
REGCLASS, sequence ownership, local checks/NN/exclusion and logical indexes are
real dependency witnesses, not a replacement claim of hierarchy-FK parity.

### RANGE table ATTACH — selected proof

The untagged `partition_attach_*_test.go` definitions share direct/prepared bodies
across native and pinned PostgreSQL. Diagnostics include eligibility, named
schema, IF EXISTS/ONLY, bounds and reached sequence effects. Values/Schema cover
reordered surviving columns, defaults, mapped checks, metadata and named-target
disclosure. Atomicity/Indexes/Transactions retain old rows/keys/topology on failure,
including current validation under an unchanged ordinary stable snapshot.

```sh
make check RUN='^TestReferencePartitionAttach(Diagnostics|Proof|Values|Schema|Atomicity)$'
make check RUN='^TestReferencePartitionAttach(Indexes|Locks|Transactions|Controls|FeatureSmoke)$'
make check RUN='^TestReferencePartitionAttachDefaultCreation$'
make check RUN='^TestPartitionAttach(Layout|Schema|Proof|IndexBuild|PublicationFences|Growth|TransactionOverlay|CheckReplayAuthority|Eligibility|PersistenceInvariant|NotNullValidity)$'
```

Repeat each reference command with `REFERENCE=1` for genuine pinned PostgreSQL
before review/integration of changed definitions. Apply an external900-second
deadline to check commands. Race proof selects the lock/publication families with
`RACE=1`; quickcheck remains the whole-project Docker-free check.

`TestReferencePartitionAttachPrivacy` independently covers restricted actors on
reordered and sparse attached candidates, direct and held prepared failures,
VALUES and INSERT SELECT. Named-target column grants and revocation govern
supplied-column/default disclosure; parent SELECT does not authorize physical
unique-index key disclosure. Leaf RLS suppresses routed index details, while
direct and parent-targeted default-deny routes reject without modifying data.
Applicable policy expressions retain the explicit native `0A000` boundary;
PostgreSQL checks actual policy-authorized constraint-detail suppression.
Run its focused smoke/proof selector in oracle-first order:

```sh
make check RUN='^TestReferencePartitionAttachPrivacy$' REFERENCE=1
make check RUN='^TestReferencePartitionAttachPrivacy$'
```

Expected observations are complete physical diagnostic identities, named-target
logical detail ordering, inaccessible omitted defaults hidden, physical key ACL
transitions and unchanged parent/child bags after every failure. These expectations
require both completed-mode runs; staging a fixture is not passing evidence.

Proof observes scans separately from locks. PostgreSQL physical-session sequence
scan counters are calibrated against no-proof ATTACH, flushed and snapshots
cleared on that session without intervening row reads. Native has no invented
statistics view. PostgreSQL uses actual pg_locks; native relies on the actual
admission manager and shared contention schedules. Candidate descendants retain
AX even when a validated proof skips scans; sufficient DEFAULT exclusion skips
descendant admission. Nullable bounds-only CHECK is not a candidate non-NULL proof;
NOT VALID cannot prove old rows, while valid local NO INHERIT can.

Feature smoke: `make check RUN='^TestReferencePartitionAttachFeatureSmoke$'` and
the same command with `REFERENCE=1`. Expected observations after all assertions:
attach0, root rows `(1,one),(2,two),(3,three)` in root order, candidate rows in
original physical order, routed inserts, genuine populated unique backings and
atomic rejection. Native controls perform actual Dump/Reset/Restore and replay the
complete unchanged emitted SQL on PostgreSQL; no hand-written substitute stream.
These are proof commands/expected outcomes, not claims that every final check ran.
The integer-array values case keeps genuine PostgreSQL attachment/routing and
an explicit native42704 missing-btree-provider diagnostic. Numeric bounds/data
use target-typed unknown inputs, not unrelated explicit-cast expansion.
Physical tableoid SQL remains a PostgreSQL-positive/native42703 boundary, limited
to those two query steps. Shared candidate pg_class OID preservation, complete
root/child values/layout/metadata and private capture provenance independently
prove retained identity; the boundary does not replace the portable assertions.
Controls include `TestReferencePartitionAttachControlIndexDependencies`: incomplete
ONLY indexes must remain incomplete after actual native Restore, including
reordered independently owned members and regclass data dependencies.

`TestReferencePartitionAttachDefaultCreation` covers the affected subsequent
CREATE PARTITION OF consumer after attaching a reordered or sparse DEFAULT.
Regular and nested layouts exercise scalar, expression and lexicographic keys
through direct execution and actual held prepared utilities. A logical conflict
must reject23514 with the pinned deciding relation identity; the inverse
payload-conflict/logical-key-outside case must succeed. Root/default bags, physical
layout, original OIDs, membership and absent new table/row-type are asserted after
rejection, followed by healthy creation and routing. Repeat its selected command
with `REFERENCE=1` before review; oracle discovery is not final dual-mode proof.
The pinned nested CREATE diagnostic identifies the DEFAULT root, not its scanned
leaf; this differs from ATTACH validation and is asserted with full diagnostic fields.

### Partitioned constraints — selected proof

The untagged `reference/tables/partition_constraints_*_test.go` definitions use
the shared case/direct-or-prepared/backend matrix. Native and genuine pinned
PostgreSQL18.0 execute the same definitions; independently observed PostgreSQL
diagnostics, catalog types and control replay witnesses are not native-derived.
Keep each group small, with an external900-second command deadline:

```sh
make check RUN='^TestReferencePartitionConstraints(Coverage|CoveragePrecedence|CompoundAdmission|Clones|Values|NestedValues|TypedEquality|PreparedMetadata)$'
make check RUN='^TestReferencePartitionConstraints(Attachment|AttachmentDiagnostics|AttachmentEquivalence|Admission|HeldCurrentRole|Atomicity)$'
make check RUN='^TestReferencePartitionConstraints(Drops|DropPrecedence|Transactions|Authorization|Privacy|Cancellation|IndexCancellation)$'
make check RUN='^TestReferencePartitionConstraints(Controls|ControlCollision|FeatureSmoke|NonuniqueCatalog|IncompleteReuse|CatalogMetadata)$'
make check RUN='^TestReferencePartitionConstraintsMultiKey(Collision|NestedOrder)'
make check RUN='^TestReferencePartitionConstraintsRegclass(References|Controls)$'
```

Repeat each command with `REFERENCE=1` before review/integration of changed
reference definitions. Retained generated/identity/serial/exclusion RANGE boundary
witnesses remain selected by `TestReferenceRangePartitionConstraintBoundaries`;
UNIQUE/PRIMARY KEY are positive partitioned-constraint witnesses, not boundaries.
Catalog OIDs are compared relationally; coninhcount is INT2 and virtual index kind
is internal CHAR I. Actual P/S edges and raw empty/nonempty metadata are asserted.
The Regclass definitions cover renamed root UNIQUE, inherited intermediate UNIQUE
and local intermediate PRIMARY KEY identities, actual local primary NULL errors,
scalar/opaque/whole-NULL stored arrays, raw metadata, inline and inherited definitions,
future defaults, two exact native redumps and unmodified PostgreSQL replay. Opaque
identity checks use every scalar OID subscript and NULL position; general array
comparison remains excluded. Selected race proof uses the same Regclass selector
with `RACE=1`.


Private structural and immutable-publication tests use the real catalog stage,
physical key roots, admitted locking and transaction owners. Include the selected
`TestPartitionConstraints` family in native proof. Race proof is separately:

```sh
make check RACE=1 RUN='^TestPartitionConstraints(AdmittedCancellation|TransactionOverlay|PublicationFences|PreservesNewestPhysicalRoot|FinalPublicationCancellation|DropFinalPublicationCancellation|RecursiveBuildFailure|IncompleteReplacementAdmission|OwnedProviderReplay)$'
```

For canonical scheduling remedies, retain the exact byte assertion in
ControlCollision and select the private source-priority/dependency regressions:

```sh
make check RUN='^(TestPartitionConstraintsDump(LogicalOrder|PriorityLifecycle|CollisionProvenance|MultiKeyNames|IndexDeclarationOrder|CollisionDefinitionReservation)|TestIndexPropertyDumpRemappedTopology|TestInheritanceControlsTopology|TestInheritanceControlsRetainedTemporaryCollision)$'
```

The private order witness remints only one root above unrelated graph nodes.
Sparse replay provenance retains that root's original priority while every dependency
continues to precede its consumer; unchanged addresses retain their original order.
MultiKeyCollision independently checks DISTINCT/NND keys sharing a generated stem,
actual selective earlier-backing TEMP remapping, unchanged noncolliding identities,
current-name NULL-duplicate diagnostics, two exact redumps and future cloning.
MultiKeyNestedOrder pins inherited DISTINCT before nested local NND. Both public
definitions require fresh selected native and pinned proof; their native controls
also replay the actual unmodified emitted SQL. DumpMultiKeyNames privately checks
whole-sibling generated-name reservation and parent-identity matching;
DumpIndexDeclarationOrder checks inherited/local/prebuilt categories and sparse
standalone priority without mutating captured constraint slices.
DumpCollisionDefinitionReservation exercises actual TEMP preservation with the
replacement allocator positioned at an imported leaf constraint OID, requiring
unchanged noncolliding definition identities, backing/parent synchronization and
immutable source/retained TEMP authority.

Blocked cancellation tests establish an actual wait before cancellation; measured
final-publication cancellation reaches the last genuine veto boundary rather
than entering with an already canceled context. Failed actions preserve graph,
rows and actual physical roots, followed by healthy retry. Controls cover renamed/
manual/partial/nested trees, local provenance, reminted references, live TEMP
collisions, deterministic repeated redump, retained cursors and failed/canceled
replacement. The separate public executable additionally replays the actual
unmodified emitted SQL into a clean isolated pinned PostgreSQL database.

Executable smoke source is the session evidence artifact
`local://partition-constraints-smoke.go`, not a staged package executable.
Materialize it outside repository package roots and launch from the worktree:
The executable retains both same-stem cases and the original PK/manual/partial/
rollback/metadata baseline. It measures actual TEMP sequence collisions with only
the earlier backing, preserves all noncolliding and TEMP identities, checks two
byte-equal redumps and future clones. Stored references cover all three renamed
UNIQUE/PRIMARY root/intermediate backings, inline/inherited definitions, late
defaults/data, populated native materialized rows and policy definitions. Native
policy-expression execution explicitly retains its0A000 boundary; PostgreSQL
independently checks actual filtering. PostgreSQL ignores native snapshot comments,
so replay explicitly REFRESHes the emitted materialized definition before checking
its rows. The actual collision-destination stream is submitted unmodified; these
instructions do not substitute for recorded executable observations.

```sh
go run /absolute/session/path/partition-constraints-smoke.go -mode native -dump /tmp/partition-constraints-smoke.sql
go run /absolute/session/path/partition-constraints-smoke.go -mode replay -dump /tmp/partition-constraints-smoke.sql -dsn "$PG_DSN"
```

Expected terminal observations, only after all assertions, are
`partition constraints native smoke passed` and
`partition constraints PostgreSQL replay smoke passed`. Root-observed selected
source/replay proof authorizes support documentation; these commands are
verification instructions, not claims that every listed group was exercised.
Reserve quickcheck for whole-project Docker-free verification. Root owns generation,
checks, review and integration; agents do not invoke refcheck.

### Typed-table selected proofs

Shared-body `TestReferenceTypedTable` covers creation/options, exact metadata and
diagnostics, attachment/dependencies, source/inherited lifecycle, authorization,
prepared signatures, failure/cancellation and independently specified native
Dump/Restore with actual emitted SQL replay on pinned PostgreSQL. Stage A's
temporary oracle-discovery filter was removed before production implementation.
Package-function `TestTyped` supplements identity mappings, provider validation,
source topology, target remapping and final publication/dependency/storage fences.
`TestTypedSameUsageStoredOwnRowPublicationGuard` executes actual same-usage source
TYPE against empty/populated own-row consumers and rejects before publication.
Public `TestReferenceTypedTableOwnRowStoredTypeGuard` keeps the identical-Usage
and widening cases on every route. Shared dependency proofs retain NORMAL edges
and distinct regtype identities; PG reciprocal metadata supplements package
provider authority without adding native pg_type/pg_class.reltype surfaces.
`TestReferenceTypedTableConstraintRetention` asserts surviving child CHECK/NOT NULL/
DEFAULT enforcement and native round trips. `TestReferenceTypedTableTemporary`
shares TEMP creation/attachment and literal state assertions with PostgreSQL;
native branches exercise rejected Reset/Restore, held results/prepares and the
attachment-during-reader-I/O final fence. Package TEMP-provider identity tests do
not introduce a TEMP standalone-type SQL feature.
The inherited propagation witness shares populated diamond leaf rows/metadata,
retains native recursive-parent0A000 observations and PG ONLY supplements.
`TestTypedInheritedParentAttributePublication` executes the real source lifecycle
and checks source/root/child names, concrete types and independent stable attributes.
`TestTypedStoredAssociatedCompositeArrayGuard` uses the actual associated provider;
`TestReferenceTypedTableNestedStoredBoundary` also checks executable array holders.
`TestTypedAttributeDependencyModeTerminators` checks generic type names and grouped
CASCADE/RESTRICT grammar without consuming dependency modes as a second type word.

Review-regression definitions exercise actual CREATE source grants (persistent
and TEMP), callback replacement during CREATE/ALTER/DROP in a bounded child test process,
late root/descendant own-row consumers, and immutable member/source type ACLs.
`TestReferenceTypedTableProviderACL` covers revoked-source CREATE and attachment,
foreign-owner and descendant CASCADE denial, member denial/grant ordering and grouped
owner preflight with a non-superuser source owner; session/role revision tests alone
are not its substitute. `TestReferenceTypedTableAuthorization` also covers attach
USAGE ordering against composite, inheritance and shape diagnostics.
`TestTypedAdmissionCreateSourceRevisionConflict` rejects a coherent source generation
change published while actual persistent/TEMP CREATE waits, then proves fresh
execution can use the new shape. Shared
`TestReferenceTypedTableAtomicityCreateSourceAdmission` runs without discovery
filters. Its retained reader and bounded AccessShare probe establish a queued writer,
then CREATE must time out without publishing an attachment. Cleanup cancels,
rolls back and joins the worker; recovery CREATE observes the completed source shape.
`TestReferenceTypedTableAttachmentSourceAdmission` independently pins ALTER OF's
execution-time source selection when an earlier-path shadow appears during a
target-lock wait. Shared direct/prepared selection, timeout and cancellation
phases prove the actual shadow's source exclusion, exact NORMAL attachment edge,
retained provider identities, unchanged rows/metadata after failure, and healthy
qualified retry followed by CASCADE and detach. Its public fairness probes use
retained readers rather than sleeps or an unsupported lock catalog.
`TestTypedPublicationCallbackReplacement/attachment` supplements that public
witness with real provider replacement during attachment validation in the
existing bounded child process, proving the incidental source reservation does
not strand callbacks or survive rejected publication.
Root-selected proof (external900-second deadlines):
`make check RUN='^TestReferenceTypedTableAttachmentSourceAdmission$'`,
the same command with `REFERENCE=1`, and
`make check RUN='^TestTypedPublicationCallbackReplacement$'`.
`TestTypedPublicationDefinitionProviderReservation` separately exercises persistent
and TEMP typed CREATE with a real incumbent sequence DEFAULT. Replacement must
queue behind its genuine unpublished-definition hold, then complete after canceled
publication drains the reservation. This existing provider policy is distinct from
the simple typed-source-only callback replacement cases; ordinary CREATE/LIKE/
serial reservation authority is not globally released.
`TestTypedPublicationCreateACLConflict` exercises descriptor-only source/member
ACL replacement in actual provider validation, with persistent CREATE as a control
and implicit/explicit TEMP statement Finish as distinct publication boundaries.
Its expected result retains the winning ACL and unchanged codec/backing/epoch/
role/session authority, with no candidate relation, attachment or store.
Fresh source OF and ordinary member declarations independently assert complete
42501 diagnostics; copied members do not acquire a new CREATE OF USAGE gate.
`TestTypedPublicationTemporaryAuthorityExpiresAtFinish` requires a successful
TEMP statement to survive later source revocation at outer COMMIT.
Root-observed regression-only proof reproduced TEMP publication after revocation;
historical H4 passing-after evidence does not replace fresh proof after changes.
`TestReferenceTypedTableTemporaryPrivateProviderComposition` covers ordinary and
typed TEMP roots created earlier in one explicit transaction, followed by direct
or prepared CREATE of a consumer of `pg_temp.root`. Exact root rows and literal
composite values `(7,x)`, SQL NULL and the non-NULL all-NULL-fields value `(,)`,
populated/empty metadata, own-row ownership, column-provider dependencies and
typed attachment identities must survive successful COMMIT unchanged.
Pinned PostgreSQL18.0 verified these expectations before the correction; the same
native body then failed all four root/route combinations with catalog publication
conflict. The correction uses the existing validated owner-private candidate and
its recorded installed predecessors without relaxing installed source/member ACL
identity fences or extending statement authority through outer COMMIT.

`TestReferenceTypedTableExclusionAttachmentControls` and
`TestReferenceTypedTableForeignKeyControls` cover retained local constraints on
attached tables, exact rows/metadata/definitions/identities/dependencies and full
23P01/23503 diagnostics. The matching `ExclusionCreate` and `ForeignKeyCreate`
definitions cover direct/prepared OF syntax and effect-free Prepare. The control
scenarios replay actual unmodified native OF dumps against PostgreSQL and assert
fresh native Restore, Reset/Restore and failed-Restore atomicity. PostgreSQL18.0
passed before the correction; the identical native bodies failed at EXCLUDE or
FOREIGN before reusing those existing table-element productions.

`TestReferenceTypedTableIdentityAttachmentControls` and
`TestReferenceTypedTableGeneratedAttachmentControls` independently verify retained
ordinary definitions through direct/prepared attachment, including sequence
options/counter/ownership, generated expressions, metadata and source/own-row
identities. Actual unmodified native dumps replay on PostgreSQL as ordinary CREATE
plus real ALTER TABLE OF; native fresh Restore and Reset/Restore retain future
default16 and generated9→18 behavior. Late failed imports assert full23502/428C9,
sentinel91, captured identities and unchanged state. Identity import uses the
existing required OVERRIDING SYSTEM VALUE, not a relaxed reconstruction guard.
Direct typed identity/generated creation retains its independent0A000 boundary.

```sh
make check RUN='^(TestReferenceTypedTable|TestTyped)'
make check REFERENCE=1 RUN='^TestReferenceTypedTable'
make check RACE=1 RUN='^(TestReferenceTypedTable(Atomicity|Temporary|Exclusion|ForeignKey|Identity|Generated)|TestTyped.*(Publication|Admission|Authorization|Remap|Retention))'
make check RUN='^(TestReferenceComposite(Declarations|DeclarationDiagnostics|TableShape|HeldShape|TypmodShape|Dependencies|CatalogRelationships|MixedControls|ControlLifetime|Authority)|TestReferenceTableLikeComposite(SourceACL|Sources)|TestTableLikeCompositeSourceACL|TestAlterDropColumnProviderCancellationAndReset|TestAlterNullabilityReviewOwnedSequenceReplacement)$'
make check REFERENCE=1 RUN='^(TestReferenceComposite(Declarations|DeclarationDiagnostics|TableShape|HeldShape|TypmodShape|Dependencies|CatalogRelationships|MixedControls|ControlLifetime|Authority)|TestReferenceTableLikeComposite(SourceACL|Sources))$'
make check RUN='^(TestSerialDefaultPublicationReplacementCancellation|TestSerialDefaultUnpublishedProviderHold|TestTypedPublication(DefinitionProviderReservation|CallbackReplacement)|TestTypedAdmissionCreateSourceRevisionConflict)$'
make check RUN='^TestTypedPublication(CreateACLConflict|TemporaryAuthorityExpiresAtFinish)$'
make check RACE=1 RUN='^TestTypedPublication(CreateACLConflict|TemporaryAuthorityExpiresAtFinish)$'
make check RUN='^TestReferenceTypedTableTemporaryPrivateProviderComposition$'
make check REFERENCE=1 RUN='^TestReferenceTypedTableTemporaryPrivateProviderComposition$'
make check RUN='^TestReferenceTypedTable(Exclusion(AttachmentControls|Create)|ForeignKey(Controls|Create))$'
make check REFERENCE=1 RUN='^TestReferenceTypedTable(Exclusion(AttachmentControls|Create)|ForeignKey(Controls|Create))$'
make check RUN='^TestReferenceTypedTable(Identity|Generated)AttachmentControls$'
make check REFERENCE=1 RUN='^TestReferenceTypedTable(Identity|Generated)AttachmentControls$'
```

Use external900s per command. Whole-project Docker-free proof is `make quickcheck`;
selected native/real modes remain mandatory. The root runs the temporary standalone
public Go typed-table smoke from its OMP session file using `go run <absolute-file>`
(external120s). Expected final line: `typed table lifecycle and native round-trip OK`.
The smoke independently asserts rows/metadata/source-versus-own-row identity,
detach, guarded direct changes, source propagation, native fresh+Reset/Restore,
failed Restore atomicity and real source DROP dependencies. It also composes a
prepared consumer of a transaction-private TEMP typed own-row provider and checks
literal composite/NULL values, rows, metadata and identities before/after COMMIT.
It is not a test wrapper or committed scaffold, and never substitutes for these
selected proofs.

Separate session-only public smokes exercise retained exclusion and bounded
foreign-key attachments, real OF dumps, fresh/ResetRestore, direct/prepared
CREATE OF and 23P01/23503 enforcement. Their expected final lines are
`retained typed exclusion and native round-trip OK` and
`retained typed foreign key and native round-trip OK`.

The fourth session-only public smoke independently completes both retained identity
and stored-generation native round-trips and subsequent writes. Expected final line:
`retained typed identity and generation round-trip OK`.

The fifth session-only public smoke, `local://typed-attachment-source-smoke.go`,
independently creates a shadow during a demonstrated target-lock wait. Qualified
CREATE OF probes the shadow's source exclusion before attachment timeout and
cancellation; ordinary rows/metadata/attachment survive, workers drain, and fresh
direct/prepared attachment, subsequent rows and detach succeed. Run its absolute
session path with `go run` from the worktree, external120s. Expected final line:
`typed attachment actual shadow source exclusion cancellation and fresh execution OK`.

### Inheritance creation and query coverage

CREATE inheritance coverage lives in the untagged `TestReferenceInheritance`
family. It independently asserts merges, diagnostics/notices, direct/prepared
metadata, nonpropagating keys, dependency/drop lifecycle, generated/serial/identity
composition, recursive modifying-target/ALTER boundaries and actual native controls.
Controls use native Dump output, including persistent graph replay beside observed
live TEMP address collisions and exact live TEMP-to-persistent replacement refusal.
The controls have no invented PostgreSQL API counterpart; PostgreSQL independently
executes shared SQL and consumes native emitted streams where applicable.

Inherited SELECT has untagged shared `TestReferenceInheritanceQuery` definitions.
Each body independently specifies direct/prepared bags, projection/full metadata,
supported consumers and frozen ONLY, parent ACL/RLS, generated mapping, physical
rowmarks/recheck, snapshot/TEMP, optimization safety and genuine native controls.
CTE ONLY/star forms retain ordinary CTE resolution (a/INT4,row1). No discovery
filter or deliberately failing PG observation remains.

Run each bounded expression below with both `make check RUN='<expression>'` and
`make check REFERENCE=1 RUN='<expression>'`, external 900-second deadlines:

|Group|Expression|
|---|---|
|Forms/bags|`^TestReferenceInheritanceQuery(Forms\|Diagnostics\|BagDiamond)$`|
|Consumers/views|`^TestReferenceInheritanceQuery(Consumers\|PreparedViews)$`|
|Authority|`^TestReferenceInheritanceQuery(Authorization\|RLS)$`|
|Recipes/eligibility|`^TestReferenceInheritanceQuery(Generated\|Optimization)$`|
|Physical rowmarks|`^TestReferenceInheritanceQuery(RowMarks\|LockRecheck)$`|
|Snapshots/TEMP|`^TestReferenceInheritanceQuery(Snapshots\|TemporaryVisibility)$`|
|Admission wait|`^TestReferenceInheritanceQueryRelationWait$`|
|Controls/smoke|`^TestReferenceInheritanceQuery(Controls\|FeatureSmoke)$`|
|Migrated scan boundaries|`^TestReferenceInheritance(Boundaries\|HeldPreparedBoundary\|BoundaryNoEffects\|OnCommitDeletePhysical)$`|
|Migrated COPY query|`^TestReferenceCopyTextQueryAndColumns$`|
|Migrated typed-parent scan boundaries|`^TestReferenceTypedTable(InheritedPropagation\|InheritedCollisionAtomicity\|OtherParentRetention)$`|

The typed-parent group retains its genuine ALTER TYPE propagation, collision
atomicity and other-parent/local-column retention assertions. Default inherited
scans and ONLY now have shared positive values/empty-row metadata expectations;
there is no native SELECT rejection or new typed-table production capability.
Run this changed group against pinned PostgreSQL first, then the same native definitions.

The wait definition retains real PG DROP/recreate with in-flight parent10 and no
replacement9, followed by fresh10,9. Native explicitly asserts its permanent
CREATE-in-transaction0A000 capability boundary, atomic rollback and recovery,
not an unreachable fake replacement. `TestInheritanceQueryAdmissionReplacement`
uses actual descendant grant admission and validated catalog Remove/Create with
allocated identities/distinct storage; the original waiter returns ErrCatalogReplaced
and a fresh retained scan sees sentinel9.
`TestInheritanceQueryAdmissionGrowth` adds a real late child while the scanner
waits on its original descendant, then proves both exact grants and one retained
snapshot containing10,20,9. This is graph reconciliation, not SQL replay or a
synthetic snapshot.

`TestInheritanceQueryStoredViewAdmission` uses real catalog publication, frozen
view binding and transaction tickets. Ordinary/ONLY/default-inherited/local and
outer-marked/overlapping occurrences assert exact physical grants and parent bags.
An actually queued peer Exclusive grant proves RowShare strength; an AccessShare-only
base permits that peer. A genuinely weaker admitted base is upgraded, and cleanup
releases both tickets without replacing logical view-source authority.

Root-run supplements:

```sh
timeout 900s make check RUN='^TestInheritanceQuery(Closure|Layout|Locator|Visibility|ScanMode)$'
timeout 900s make check RUN='^TestInheritanceQueryAdmissionReplacement$'
timeout 900s make check RUN='^TestInheritanceQueryAdmissionGrowth$'
timeout 900s make check RUN='^TestInheritanceQueryStoredViewAdmission$'
timeout 900s make check RUN='^TestGeneratedTableOIDStructuralEquality$'
timeout 900s make check RUN='^TestInheritanceGuards$'
timeout 900s make check RUN='^Test(MaterializedViewAuthorization|TemporaryViewRetainedSourceFrontier|UpdateFrom(FramesOriginsAndPhysicalTargets|LosingPairsDemandAndFailureCleanup|ImmediateGrantFreezesSource))$'
timeout 900s make check RACE=1 RUN='^TestReferenceInheritanceQuery(RowMarks|LockRecheck|RelationWait)$'
timeout 900s make check RACE=1 RUN='^TestInheritanceQueryAdmissionReplacement$'
timeout 900s make check RACE=1 RUN='^TestInheritanceQueryAdmissionGrowth$'
timeout 900s make check RACE=1 RUN='^TestInheritanceQueryStoredViewAdmission$'
```

`TestGeneratedTableOIDStructuralEquality` uses the real row-reference mapper:
equal mapped physical owner/full Usage compares equal; different owner, epoch,
address, subobject, Usage or expression kind does not. It never evaluates recipes.
The generated and locked-postwait reference expectations remain unchanged.
MERGE source consumer counts use the incumbent native actual-modification count
versus lib/pq's zero export; independent sink rows prove the same modifications.

The root materializes `local://inherit-query-smoke.go` as
`.inherit-query-smoke/main.go`, runs `timeout 60s go run ./.inherit-query-smoke`,
records stdout/stderr/exit and removes only that temporary source directory.
It must prove five-row mapped bags versus physical two, exact metadata, prepared
and frozen ONLY modes, parent authority/RLS, real child row locking and genuine
Dump/Reset/Restore with held Rows, not Ping or a synthetic stream.
The smoke's parent ACL/default-deny phase uses a separate disposable five-row
hierarchy, actually dropped CASCADE before Dump. REVOKE alone leaves explicit ACL
metadata outside native serialization. The original diamond/data/views used for
physical rowlocks and genuine controls are never rebuilt or replaced as cleanup.
Whole-project Docker-free `make quickcheck` and post-integration `make finalcheck` remain separate.

The retained-exclusion collision regression observes an actual imported parent
descriptor OID equal to a live TEMP child's local EXCLUDE constraint OID before
native Restore. Its bounded native-only fixture uses at most 64 real sequence
allocations and existing regclass OID queries, not allocator constants or an
unsupported catalog projection. Shared direct/prepared PostgreSQL SQL independently
asserts graph/default/CHECK/EXCLUDE behavior. After Restore, native assertions
retain exact TEMP relation/index/constraint identities and rows/enforcement, and
ordinary CREATE with DEFAULT/NOT NULL/CHECK plus INSERT/query returns INT4 row42.
The descriptor unit additionally asserts all inheritance-origin remapping, source
immutability, retained storage and complete combined definition-identity validity.
Run the completed body in both modes with no discovery filter (900 seconds each):
```sh
timeout 900s make check REFERENCE=1 RUN='^TestReferenceInheritanceRetainedExclusionDefinitionCollision$'
timeout 900s make check RUN='^TestReferenceInheritanceRetainedExclusionDefinitionCollision$'
timeout 900s make check RUN='^TestInheritanceControlsRetainedExclusionDefinitionCollision$'
```

Root-run selective gates (900 seconds each, after edits and generation finish):
```sh
timeout 900s make check RUN='^TestReference(Inheritance|NotNull|Check|TableLike)'
timeout 900s make check REFERENCE=1 RUN='^TestReference(Inheritance|NotNull|Check|TableLike)'
timeout 900s make check RUN='^(TestInheritance|TestNotNull|TestTableLike|TestAlterDefault)'
timeout 900s make check RACE=1 RUN='^TestInheritance(Admission|Graph|Controls)'
timeout 900s make check RUN='^TestReferenceInheritanceFeatureSmoke$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceInheritanceFeatureSmoke$'
```
The standalone public API smoke is supplied as temporary session material, not
committed scaffolding. Neither selected gates nor smoke replace full quickcheck
and the post-integration1800-second finalcheck.

The dependency-shape review regressions independently assert PostgreSQL provider
CASCADE column/definition removal and native atomic inheritance ALTER rejection,
including consumer recovery and graph enforcement. Run the same completed bodies
in both modes (900 seconds per invocation):
```sh
make check RUN='^TestReferenceInheritanceDependency(Shape|Definition)Boundary$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceDependency(Shape|Definition)Boundary$'
```

Retirement authority regressions cover provider schemas containing the sole child
or the parent, each with typed-column, DEFAULT and CHECK dependencies. Both routes
assert retained standalone physical rows/metadata/enforcement or complete outside
descendant retirement, without weakening the surviving-graph boundary above:
```sh
timeout 900s make check RUN='^TestReferenceInheritanceRetirementAuthority$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceInheritanceRetirementAuthority$'
timeout 900s make check RUN='^TestInheritanceDependencyAuthority$'
```

Prospective retirement regressions use a parent that is also a regclass provider
for the retained other parent's DEFAULT or CHECK. CASCADE discovers their shared
child; the retained parent becomes standalone before its definition is pruned.
Both direct/prepared routes assert removed relations/edges, retained rows and
metadata, consumed provider definitions and independent local definitions; native
held eager child results retain actual original values and metadata. The real-stage
units use the same q root with actual admission, not a pre-removed child:
```sh
timeout 900s make check RUN='^TestReferenceInheritanceProspectiveRetirement$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceInheritanceProspectiveRetirement$'
timeout 900s make check RUN='^TestInheritanceProspectiveRetirement$'
```

`TestReferenceInheritanceLocalExclusionReplay` covers a child with only inherited
columns and a first local EXCLUDE declaration. It consumes the actual native dump
through Reset/Restore and pinned PostgreSQL replay, then asserts rows, default
enforcement and graph edges. Native replacement also retains a live TEMP-to-TEMP
inheritance graph, its exclusion constraint OID, physical data and enforcement.
Include `Inheritance|Exclusion` when verifying this integrated composition; do not
replace these consumer outcomes with dump-string or canonical-byte assertions.

### Table definition copying

Table definition-copy coverage uses
`RUN='^TestReference(Table(Like|Core)|SchemaTableIntegration|IdentitySerialAndNativeBoundaries|NotNullExternalInheritance)'`
in both modes; feature smoke uses `RUN='^TestReferenceTableLikeFeatureSmoke$'`.
Oracle-first discovery fixes complete diagnostics/index-name expectations before
the native failing regression. Actual native Dump output is replayed on pinned
PostgreSQL with independent expected rows and future writes, not SQL-substring or
no-LIKE assertions. PostgreSQL-only structural inquiries for absent native catalog
sources/columns are paired with native descriptor proofs and shared pg_indexes,
pg_sequence/pg_depend inquiries and actual enforcement. Table/column GRANT/REVOKE
is now a shared public authority control; package-function ACL units supplement
those witnesses without duplicating public SQL tests outside reference.
Predicate-only `pg_depend` projection remains outside the bounded native catalog:
mixed key/INCLUDE/predicate inquiries independently expect native attributes2,3
versus PostgreSQL2,3,4, and predicate-only external counts0 versus1. Exact native
analyzed/execution manifests and shared real DROP/column lifecycle/controls prove
the underlying predicate dependencies; this inquiry boundary never skips a scenario.
The partial-copy subfamily is `RUN='^TestReferenceTableLikePartial'`; run
`make check RUN='^TestReferenceTableLikePartial'`
and `make check REFERENCE=1 RUN='^TestReferenceTableLikePartial'`.
Private cooked-tree, stable/dense attribute, fresh membership-root and actual
provider-admission witnesses use
`make check RUN='^TestTableLikePartial'`.
The expanded existing feature smoke includes actual partial-index Dump/Restore
and PostgreSQL replay with future membership, uniqueness and predicate-first
errors. The complete shared union also retains CoreNativeControlRoundTrip's
standalone typed/direct and quoted/prepared MAXINT8/O'Brien/TRUE rows and metadata
after actual Restore, plus CoreSemanticSkipNotices' quoted unknown `"integer"`
IF NOT EXISTS notice and unchanged-data witnesses. Each selected check invocation
has its own 900-second deadline.
TEMP composition uses `RUN='^TestReferenceTableLikeTemporary'` in both modes and
`RUN='^TestTableLikeTemporary'` for native engine units. Shared SQL covers
same-session cooked defaults, constant/permanent-sequence defaults from TEMP
sources, DEFAULT mask precedence, fresh destination-owned identity sequences,
and exact native0A000/PG-success permanent TEMP-provider differential. Native
control phases exercise actual Reset/Restore, retained rows/provider identity,
new copies from retained sources, failed/canceled replacement and actual permanent
stream replay on PostgreSQL. These phases have no PostgreSQL control API substitute.
The existing feature smoke includes both TEMP default and identity lifecycles.

Composite LIKE sources use `RUN='^TestReferenceTableLikeComposite'`, with ordinary
and pinned reference gates below. Plain/ALL/per-LIKE masks, nullable fields,
interleaved type usage, empty composites, source-type removal through the existing
dedicated-schema `DROP SCHEMA ... CASCADE` route, duplicate atomicity and actual
independent-table controls/replay share the existing fixtures. Native DROP TYPE
syntax is not expanded; all public copies must survive actual source retirement.
PostgreSQL GRANT/REVOKE setup verifies type USAGE rather than backing-relation
SELECT; `TestTableLikeCompositeSourceACL` exercises that distinction through the
native ACL seam, including namespace-first errors and reciprocal source captures.
Pinned PostgreSQL composite discovery expectations are encoded; the temporary
scenario-local backend filter is removed before the native regression.

```sh
make check RUN='^TestReferenceTableLikeComposite'
make check REFERENCE=1 RUN='^TestReferenceTableLikeComposite'
make check RUN='^TestTableLikeComposite'
```

The retained row-expression regression family is
`RUN='^TestReferenceTableLikeTemporaryRetained'`. Its SQL witnesses cover
GENERATED, CHECK, expression keys and partial predicates copied into permanent
and same-owner TEMP destinations, and a separate same-owner TEMP external-literal
fixture. Native phases perform actual persistent replacement before CREATE and
again after source removal: restored permanent and retained TEMP copies must
preserve full rows/metadata and future generation, CHECK and partial-index
enforcement even without Docker. Optional PostgreSQL replay consumes that actual
permanent Dump stream; it does not replace the second native Reset/Restore.
PostgreSQL exercises shared SQL without a fabricated control API.
`RUN='^TestTableLike(TemporaryRetained|CookedRowRemap)'` supplies descriptor
authority and real lock-manager contention witnesses: local row/tableoid refs
receive current destination authority, external stored references remain old,
and queued admission must preserve those manifests while canceling, rolling back,
publishing or rejecting a dead/recreated/same-address revised provider.
`TestTableLikeDefinitionDependencyOrder` additionally exercises the common DROP
dependency formatter through ordinary CREATE, not a LIKE special case: an earlier
view, generated column, two CHECKs whose definition-OID order opposes declaration
order, and a later index retain the exact report schedule. The shared retained
literal fixture asserts the full six-line PostgreSQL dependency diagnostic.
Pinned PostgreSQL discovery expectations are encoded and the scenario-local
backend filters removed. The native-red and final dual-mode gates use the
complete scenarios. Each invocation below has a900-second deadline:

```sh
make check RUN='^TestReferenceTableLikeTemporaryRetained'
make check REFERENCE=1 RUN='^TestReferenceTableLikeTemporaryRetained'
make check RUN='^TestTableLike(TemporaryRetained|CookedRowRemap)'
make check RACE=1 RUN='^TestTableLike(TemporaryRetained|CookedRowRemap)'
make check RUN='^TestTableLikeDefinitionDependencyOrder'
```

### Temporary tables

Temporary-table shared definitions cover physical owner isolation and shadowing,
pg_temp creation/resolution, direct/prepared metadata and complete diagnostics,
all commit policies, SQL/driver/CHAIN commit, rollback/savepoints, read-only DML,
dependency counters/index membership, physical close, completed utility invariants
and native persistent controls with exact retained stored-reference provenance.
Migrated sequence TEMP-table authority/control scenarios retain sequence memories,
held statements, peer ownership and persistent-control atomicity.
`TestReferenceTemporaryTableClosePermanentCheck` asserts actual permanent CHECK
enforcement/dependency identity, physical owner exit, removed constraint/provider,
surviving rows, held prepared peer INSERT and same-basename peer TEMP isolation.
The expression lifecycle family uses actual pg_class/pg_attrdef/pg_depend OIDs and
independent prospective rows for automatic, immediate, explicit committed/private
and physical cascades. It checks generated-column projection, surviving dense-slot
indexes, retained snapshots, pending writes/deletes, savepoint rollback, composed
ON COMMIT DELETE and native Dump/Restore. Package invariants cover late catalog and
row/index predecessor reprepare, unrelated current heads, genuine private absence,
terminal combined descriptor/store/index maxima, concurrent ready-invalid/not-ready
flags and unchanged ordinary counter-exhaustion rejection.
The index-composition definitions use a SERIAL TEMP owner, table/owned-sequence
generated dependencies and an expression index created before BEGIN. Immediate
and transactional/savepoint/full-rollback witnesses retain the actual index OID,
independent key definition, original/restored rows and coherent subsequent DML.
Their package supplement verifies original/intermediate/final derived layouts
and stable slots, immutable earlier candidates, genuine rebuilt key/row identity,
original publication predecessors, idempotent validation, ordinary second-pass
overflow and physical-terminal saturation.
The same fixtures retain an admitted B-tree partial expression index created
outside BEGIN, exact full `pg_temp` key/predicate rendering and positive→negative→positive
UPDATE membership. Actual `pg_tables.hasindexes=true` remains through compaction
and rollback; the narrower native `pg_class` projection is not expanded.
Genuine package deparse checks distinguish current physical owner from peer
same-name indexes and context-free canonical Dump.
The controlled sibling-provider cascade supplements this with actual table/index
generated consumers and an independent own-row `z+1` generation across Reset/Restore.
It asserts removed siblings, stable surviving definition/dependency identities,
dense rows, held INSERT/UPDATE and nonreused same-name provider recreation.
The package supplement also discovers the actual owned-index subtree after its
removal is already staged, asserting exact once-only sibling edges and unchanged
catalog/store/vector/end/index predecessors before the real SQL cascade.
The live-provider controls retain an actual same-owner TEMP table and index
through native Reset/Restore, then run unrelated CREATE, held INSERT, independent
ALTER, UPDATE and catalog-expression consumers. Shared direct/prepared SQL
witnesses assert exact provider/definition OIDs, dependency edges, independent
rows and peer same-name isolation; Dump excludes the retained TEMP objects.
The package supplement uses real sessions and catalog replacement, preserving
captured manifests, descriptors, stores and roots while rejecting foreign engine
or physical owner, future/zero epochs, invalid attributes, concrete type/row-origin
mismatches, user-domain/array-domain types and retired/same-name recreated providers.
`TestReferenceTemporaryTablePredicateOnlyControls` isolates a plain INT-key partial
index without another BOOL-bearing TEMP definition that could mask predicate-type
retention. Direct/prepared bodies assert exact identities/full definition and
positive/negative membership before and after native Reset/Restore plus held DML.
Its package supplement compares both predicate trees' concrete builtin generations
and exact unchanged descriptors, manifests, stores, vectors, ends and index roots.
The close witness also roundtrips native persistent output without the removed
CHECK/provider while retaining the peer TEMP serial counter and stored rows.

```sh
make check RUN='^TestReference(TemporaryTable|SequenceTemporaryTable|Unique(CreateTransactionBoundary|TemporaryLifecycle)|PreparedTransactionSessionBoundaries|VisibilityInquiry(Boundaries|UtilityBoundaries)|ArrayNativeControls|QueryTableCreate|PGDependTransactions)'
make check REFERENCE=1 RUN='^TestReference(TemporaryTable|SequenceTemporaryTable|Unique(CreateTransactionBoundary|TemporaryLifecycle)|PreparedTransactionSessionBoundaries|VisibilityInquiry(Boundaries|UtilityBoundaries)|ArrayNativeControls|QueryTableCreate|PGDependTransactions)'
make check RUN='^Test(TemporaryTable|TemporaryRelation|CatalogTemporary|SchemaSearchPathSessionDefaultsAndSpecials|SerializableEvidenceActualTemporaryNamespaceAndSequenceProviders|PGDependDefaultReplay)'
make check RUN='^TestReferenceTemporaryTableFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceTemporaryTableFeatureSmoke$'
```

Both selected modes use the same completed definitions under900-second deadlines;
ordinary quickcheck and the post-merge1800-second finalcheck remain required.

### HASH partition reference coverage

`hash_partition_*_test.go` uses the shared public dual-mode fixture with independent
release-derived integer/scalar leaf fixtures, direct/fresh-prepared/held routes,
actual result metadata, full English diagnostics and character positions, mixed
factor/overlap precedence, ACL/privacy, cancellation and atomic inserts.
Native control phases exercise fresh destinations, repeated Restore, retained
TEMP/cursors/statements and real enum/domain/regclass identity changes. Twin
destination previews independently measure reminted identity before sparse-bound
and local-CHECK conflict tests; failure must preserve target rows and dump bytes.
These controls are native-only, not a fabricated PostgreSQL Restore API.
Scalar family routes have per-type subtest names. Internal CHAR explicitly retains
the existing native column0A000 boundary while PostgreSQL exercises its real column
routes; signed-byte support2 vectors remain in provider units. XID/CID/XID8 keep
their admitted scalar columns and hash classes, including XID8's signed high-bit fold.
Failed definitions prove row-type namespace availability through genuine same-name
CREATE TYPE/DROP TYPE, not an unowned pg_type catalog query. Authorization setup
uses real PostgreSQL schema grants and native-only schema ownership transfer,
leaving bootstrap-owned parent/leaf tables and real table ACLs unchanged.
`Unique` independently covers HASH PRIMARY KEY/UNIQUE admission, omitted partition
keys, compatible explicit classes/collations and routed duplicate enforcement.
`FinalRemap` distinguishes initial replay from final retained-TEMP repair through
matched target allocation histories and an observed exact relation/rowtype collision.
A numeric-OID CASE key deterministically changes scalar key0 to2 (builtin residues
0 to2), without relying on OID hash low bits or changing leaf/index topology.
Both source and destination own physical UNIQUE indexes; exact emptied/populated
leaves, predicate reads, future duplicates/writes and emptied-source key reuse are
asserted. General replay-conflict tests do not claim final-stage-only failure proof.
`NotValidRestore` covers a populated ordinary leaf with a later NOT VALID CHECK
attached under HASH→RANGE: old violating data and the unvalidated definition
survive real repeated/fresh replay, while future violating writes still fail.
Private policy units distinguish unchanged and descriptor-retagged inputs from
relevant changed cells/literals and owner-sensitive values, without weakening
full destination checks for actual movement. All scenarios use one HASH runner.

Run each selected command in both modes, adding `REFERENCE=1` for pinned PostgreSQL,
under external 900-second deadlines:

```sh
make check RUN='^TestReferenceHashPartition(Definition|Routing|MixedModuli|Metadata)$'
make check RUN='^TestReferenceHashPartition(Types|Diagnostics)$'
make check RUN='^TestReferenceHashPartition(Atomicity|Authorization|Cancellation)$'
make check RUN='^TestReferenceHashPartition(Controls|IdentityReplay)$'
make check RUN='^TestReferenceHashPartition(Unique|FinalRemap)$'
make check RUN='^TestReferenceHashPartitionNotValidRestore$'
make check RUN='^(TestPartitionHash(Primitives|Types|Cancellation)|TestHashPartition(Bounds|Invariants|Remap|Grammar|IndexRepairTransport|RetainedCheckPolicy)|TestValueSamePayload)$'
make check RACE=1 RUN='^TestReferenceHashPartition(Cancellation|Atomicity|IdentityReplay|FinalRemap)$'
```

The root-run throwaway `hash-part-smoke.go` is a separate public Go process, not a
permanent test: mixed integer leaves, held RETURNING descriptors, affected counts,
atomic errors, real fresh-destination Dump/Restore and changed enum identity routing
must succeed before it prints its single success line. Keep its session-only source
outside the repository. Whole-project Docker-free proof remains `make quickcheck`.

### RANGE partition reference coverage

`reference/tables/range_partition_*_test.go` uses one shared scenario body in both modes.
Rebase consumer witnesses include genuine direct/prepared ON CONFLICT and MERGE
hierarchy boundaries (root/intermediate/physical member), full independently
expected errors, unchanged rows and undemanded defaults. A plain-table conflict
positive control retains skipped-row and affected-count authority. Genuine
transactional DECLARE/FETCH/CLOSE consumes the complete hierarchy provider.
Literal expectations cover scalar and multikey tuple boundaries, sentinels/NULL,
DEFAULT and nested routing, expression keys/coercion, exact errors and positions,
catalog identities/dependencies, ACLs, late multi-leaf atomicity, and controls.
Utility supplied-argument stages preserve zero slots and independently expected
adapter errors. Complete diagnostic records retain every optional field.
`TestReferenceRangePartitionACL` runs its entire named-target/leaf authorization
and missing-key disclosure matrix in both modes and both genuine direct/prepared
routes, including simple column SELECT, expression-key suppression/table SELECT
and qualified destination-schema precedence. SchemaGrantExpansion independently
covers current root/nested/leaf/table/view/materialized-view schema-wide grants,
explicit virtual grants removed by schema-wide REVOKE, denied subsequent writes,
future-table and sequence exclusion, complete errors and unchanged stored rows.
QuotedKeyPrivacy additionally covers mixed-case/space-containing, embedded-quote
and reserved-word simple keys at root and nested routing failures. Public
INSERT-only, column SELECT and revoked column SELECT transitions independently
assert hidden, correctly quoted and hidden-again complete diagnostics, payload
access denial and unchanged physical rows in direct/prepared execution.
`TestReferenceRangePartitionConstraintPrivacy` independently covers direct and
genuine prepared leaf/intermediate errors under INSERT-only, supplied-value,
column SELECT, all-column SELECT and table SELECT manifests. Both backends use the
existing public table/column GRANT routes; package ACL units supplement them. Errors
retain the actual named target, complete optional fields, correct labelled/full
row disclosure and unchanged data, through one shared ordinary NN/CHECK policy.
`TestReferenceRangePartitionPhysicalConstraintPrivacy` extends that independent
matrix to root, intermediate, leaf and ordinary NOT NULL/CHECK producers, with
single/batch VALUES and direct/prepared INSERT SELECT. It distinguishes named-target
SELECT authority from physical-leaf diagnostic identity and checks that omitted
secret defaults remain private, supplied values remain visible, and late failures
leave every affected leaf unchanged.
`TestReferenceCollationNativeCollision` additionally uses explicit RANGE-key user
collation under a real imported-provider/live-TEMP OID collision. Independent
parent/leaf bags, source immutability, retained TEMP authority, exact dependency
targets and direct/prepared routing are checked before and after replacement.
Real mode replays the actual emitted SQL on PostgreSQL and routes further values.
`TestReferenceRangePartitionCollationDependencies` covers key-only, effective and
combined collation authority with exact RESTRICT diagnostics, intact rows, normal
metadata edges and complete CASCADE hierarchy/owned closure. TemporaryDependencies
checks zero-output nonportable Dump rejection, physical provider cleanup, retained
rows, peer sentinels and fresh portable Restore.
`TestReferenceRangePartitionProviderRetirement` exercises full physical-close
retirement in both backends, with bounded PostgreSQL server-exit acknowledgement
before unchanged absence assertions. PostgreSQL also executes the full ON COMMIT
DROP composition; native explicitly asserts its existing transactional permanent
CREATE boundary and rollback atomicity. Private Retirement units exercise actual
nil-scope staging and COMMIT preparation/installation from validated owned catalog
state, two real leaf write roots and a committed provider deletion. They require
unchanged original roots and complete root/leaf-owned storage/index/generator cleanup.
`TestReferenceRangePartitionLeafExclusion` and the existing Controls body reuse
ordinary physical exclusion producers, enforcing exact direct/prepared conflicts,
late cross-leaf atomicity and genuine Dump/Restore/SQL replay. Virtual exclusions
and selected recursive EXCLUDE lifecycle are described below; adjacent hierarchy
DML remains outside that cutover.
`TestReferenceRangePartitionFailureOrder` composes both arity preflights, lower
NULL, lower/later volatile demand, malformed upper expressions and finite
transformation failures after MIN/MAX. Complete diagnostics, nonrewinding sequence
state/next draw, child/type/index/constraint absence and DEFAULT data are asserted.
`TestReferenceRangePartitionOrderedDiagnostics` deliberately reverses sibling
creation order and covers later-component reversal, full finite equality-last,
equal-lower overlap-first and spanning gap/inside overlaps. Witness and Position
follow the actual typed ordered endpoint search's last comparison; unchanged
topology/data and subsequent gap/sibling routing remain asserted.
PRIMARY KEY syntax now uses the integrated ordinary producer: virtual declarations
fail the execution-time constraint-combination boundary in both routes, not an
obsolete positioned parser/Prepare rejection. Physical leaf constraints use the
existing local construction/enforcement authority.
Named inherited CHECK cases distinguish structural equality, commuted operands,
added arithmetic, NO INHERIT conflicts and duplicate actual child declarations.
Inherited enum/domain USAGE and revoked type-schema USAGE are separate witnesses;
fresh qualified child expressions retain normal namespace denial.
Discovery uses admitted equality/OR predicates and explicit OID coercion, not
adjacent IN/operator capabilities. Inherited domain authority uses a plain domain;
its fresh USAGE and stored namespace identity are independent of domain constraints.
Schema and owner discovery uses the existing `pg_tables` provider (including
virtual roots/intermediates) with `pg_class`, without requiring the public
`pg_roles` view or the still-unavailable native `pg_namespace` catalog. Manual ownership combines
`pg_get_serial_sequence(...,'payload')` with exact `pg_depend.refobjsubid = 2`,
plus a separate manually owned root key sequence with stable subID1 and matching
column inquiry. This avoids ambiguous multiple sequences on the same column
without changing production lookup semantics. Exact schema/table identities
remain asserted; discovery does not require an unowned `pg_attribute`.
Internal catalog character fields are projected directly with CHAR metadata,
not unsupported internal-char-to-TEXT casts. Window context uses admitted
ROW_NUMBER with PARTITION BY/ORDER BY and independent ordinals1,2; standalone SUM
remains in the aggregate consumer witnesses.
Failed embedded CREATE SCHEMA additionally proves namespace absence by successful
recreation of that exact name, followed by cleanup.
Adjacent CREATE SCHEMA partition definitions assert PostgreSQL success versus native
positioned0A000 before any schema/table publication or bound sequence demand, on
both direct and genuinely prepared utility execution routes.
Manual root/intermediate sequence ownership independently asserts live column
inquiry, AUTO dependencies, routed defaults, actual unmodified Dump replay,
deterministic redump and complete subtree/owned-sequence DROP closure.

Native Reset/Dump/Restore phases use the public control APIs, guarded only because
PostgreSQL has no corresponding API. The actual emitted stream is consumed
unmodified by the real PostgreSQL connection, with typed rows, owners, dependencies
and current defaults asserted afterward. Cross-owner replay provisions only
temporary inherited role membership and importing namespace rights in PostgreSQL;
ordinary ownership denials execute before that provisioning. Deferred hierarchy
defaults exercise importer-only replay and failed replay preserving the old target.
The deferred graph is a genuine cycle of REGCLASS defaults on the virtual parent
and an ordinary table, using the existing ordinary SET DEFAULT producer rather
than unsupported ALTER ADD CHECK syntax. Mixed-owner schema setup is performed
under the genuine bootstrap principal with explicit AUTHORIZATION owners.
`TestReferenceRangePartitionSelfRestoreControls` additionally restores directly
into the same open database without Reset: held statements, retained cursors,
expression/nested keys, local UNIQUE, views, inherited CHECK, sequence ownership
and ordinary coupled defaults must retain their semantic contracts. Internal
self-catalog replacement also retains exact live TEMP sequence/table/index
descriptors, stores, index roots, generator/cache and held demand. Existing
TEMP DEFAULT/CHECK/generated/partial-index definitions retain their own authority;
the permanent candidate is fully remapped before the existing exact TEMP overlay.
The TEMP regression keeps both a supported column partial UNIQUE index (with
duplicate enforcement) and a separate ordinary expression/predicate index (with
exact descriptor/root retention). Childless and populated hierarchy sources pin
the same real catalog/data-view authority as ordinary retained query sources.
Cancellation/reset observers wrap the actual leased operation context: Acquire's
WithCancel wrapper does not forward the original caller's Err callbacks.
All temporary roles/schema rights are cleaned up under bounded deadlines.
Every actual native SQL replay on PostgreSQL registers bounded same-session
RESET ROLE / RESET SESSION AUTHORIZATION / RESET search_path cleanup before
replay, ahead of schema recreation or role-grant teardown. The shared PostgreSQL
body therefore retains its authenticated fixture owner, not the stream's final
named owner.

The former `smoke/table_test.go` behavioral/API union is now
`TestReferenceRangePartitionMigratedTableCoreSmoke`, not a native-only duplicate.
Integrated LIKE INCLUDING ALL / EXCLUDING CONSTRAINTS now share their successful
copy, empty typed/NOT NULL metadata, enforcement, DROP and absence assertions in
both modes. Actual native Dump must exist before the PostgreSQL replay lifecycle;
an earlier native failure is never treated as an empty successful replay stream.
The cohesive `TestReferenceRangePartitionFeatureSmoke` includes ordinary table
definitions, real routing, nested/default bounds, metadata and persistent controls.
No discovery filters belong in final definitions.
Raw sentinel scenarios cover mixed-case keywords, quoted lowercase names and
redundant parentheses; quoted uppercase, qualified/cast/operator wrappers retain
the independent column-reference bound error. Finite TEXT casts stay finite.
TEMP hierarchy definitions are a separately positioned native0A000 boundary,
including implicit qualified pg_temp destinations; ordinary TEMP/ON COMMIT
capabilities retain their integrated baseline implementation.

Run the same completed reference definitions in both modes, with a separate
900-second deadline for every invocation:

```sh
make check RUN='^TestReference(RangePartition|CollationNativeCollision|TableCore|AlterDropColumnExternalHierarchy)'
make check REFERENCE=1 RUN='^TestReference(RangePartition|CollationNativeCollision|TableCore|AlterDropColumnExternalHierarchy)'
make check RUN='^TestReferenceRangePartitionFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceRangePartitionFeatureSmoke$'
make check RUN='^TestReferenceRangePartitionMigratedTableCoreSmoke$'
make check REFERENCE=1 RUN='^TestReferenceRangePartitionMigratedTableCoreSmoke$'
make check RUN='^TestRangePartition'
make check RACE=1 RUN='^TestRangePartition(Concurrency|Publication|Capture|Remap)'
make check RUN='^TestReferenceRangePartition(ConstraintPrivacy|PhysicalConstraintPrivacy|FailureOrder|OrderedDiagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceRangePartition(ConstraintPrivacy|PhysicalConstraintPrivacy|FailureOrder|OrderedDiagnostics)$'
make check RUN='^Test(CheckInsertPrivilegeDisclosure|RangePartition(ConstraintPrivacy|PhysicalConstraintPrivacy|Retirement|RemapExplicitCollation))'
```

Package-function witnesses inspect owned descriptors, cooked type/row identities,
private importer provenance, real lock queues, multi-leaf transaction stages and
complete self-catalog expression remapping with retained old cursors. They supplement
the shared public reference routes. The root generates/formats the parser and runs
verification only after all edits pause; the post-integration finalcheck runs once
under a single 1800-second deadline, with actual failures remedied separately.

### Foreign-key proof selection — change note

Shared independently specified references use the same bodies for native and
pinned PostgreSQL, including all migrated FK boundary callers. Run each selected
group with `make check RUN='...'` and again with REFERENCE=1:

* `^TestReferenceForeignKey(Definition|DefinitionNames|DefinitionIneligibleKeys|DefinitionTypes|DefinitionProviders|NetworkSupportingKeys|Diagnostics|Values|Metadata|Boundaries|BoundariesModes|BoundariesPartition|BoundariesTemporal|BoundariesTemporary)$`
* `^TestReferenceForeignKey(Atomicity|Transactions|Producers|Authorization|Concurrent|ConcurrentCancellation)$`
* `^TestReferenceForeignKey(Lifecycle|RetainedStatements|NativeControls|NativeControlsTemporary|FeatureSmoke|DeleteReturningCaller|DropColumnCaller|SchemaCaller)$`
* `^(TestReferenceDeleteCoreForeignKeyBoundary|TestReferenceAlterTypeForeignKeyBoundary|TestReferenceAlterRenameExternal|TestReferenceInheritanceKeyBoundaries|TestReferenceTableCoreExternalDependencyBoundary)$`

The affected tagged remainder definitions are
TestReferenceDeleteReturningBoundaries,
TestReferenceAlterDropColumnExternalDependencies and
TestReferenceAlterSchemaPostgresClosure. Their unrelated PostgreSQL-only
assertions remain selected with REFERENCE=1; their extracted FK assertions are
now in the untagged caller definitions above. No discovery filter remains.
Native controls do not run on PostgreSQL; the permanent shared bodies retain
complete SQL assertions around their native-only Reset/Dump/Restore phases.

Internal `^TestForeignKey` exercises the real canonical K/U row requests,
non-key compatibility, queued cancellation/replacement, private row overlays,
definition/index authority and immutable TYPE identities. Race proof uses
`make check RACE=1 RUN='^TestForeignKey'`. Keep the shared concurrent references
selected too: internal manager evidence complements, not substitutes for, the
real PostgreSQL observed behavior.
The migrated parser witness is selected by
`make check RUN='^TestObjectGrantReferencesAndForeignKeyGrammar$'`.
The comment-only role-grant consumer remains
`make check RUN='^TestReferenceRoleGrantKinds$'` and its REFERENCE=1 counterpart;
its delegated privilege assertions are unchanged.

Feature smoke: `make check RUN='^TestReferenceForeignKeyFeatureSmoke$'`, plus
the identical selector with REFERENCE=1. Every external check invocation uses
a 900-second deadline. Quickcheck remains whole-project Docker-free proof.

Foreign action selections use64 independently expected public definitions:

```sh
make check RUN='^TestReferenceForeignActions(Definition|Values|Equality|Metadata)'
make check RUN='^TestReferenceForeignActions(Defaults|Constraints|Diagnostics|Authorization)'
make check RUN='^TestReferenceForeignActions(Graphs|Atomicity|Transactions|Producers)'
make check RUN='^TestReferenceForeignActions(Concurrent|Cancellation)'
make check RUN='^TestReferenceForeignActions(Prepared|Lifecycle|Temporary|FeatureSmoke)'
```

Run each identical selector with `REFERENCE=1` for real pinned PostgreSQL18.0.
The changed `TestReferenceForeignKeyBoundariesModes` retains its deferred
native capability boundary and real PostgreSQL assertions, while its admitted
actions now share positive/error effects. Sequence owner transitions provide
shared owner-sensitive DEFAULT proof without requiring native sequence GRANT.
Constrained domains retain the exact native0A000 boundary and PG23514; real
unconstrained-domain assignment plus table CHECK proves shared composition.
No new-only oracle discovery filter remains.
The inheritance action witness shares direct/prepared UPDATE ONLY and recursive
parent UPDATE while descendants survive, with independently unchanged parent
and child leaves. Referential actions still affect only their physical child;
the later parent UPDATE/DELETE lifecycle remains independently asserted.
Domain provider assignment uses a plain domain; the separate constrained-domain
declaration checks the complete positioned native boundary.
Captured-authority Restore uses named owner directives. Replacement proof uses
positive current catalog IDs and an actual pre-replacement transaction's prepared
statement/read epoch fence, not numeric OID inequality. Idle held analysis may
legitimately rebind afterward; both idle-held and newly prepared current action
effects are asserted. Allocator numbers may repeat after replacement.
PostgreSQL concurrent DDL uses a dedicated physical connection and real
`lock_timeout`, not pool exhaustion or caller-context timeout as a lock witness.
Ordinary native DROP has no AX admission and is not claimed to produce55P03.
Its explicit-transaction DROP capability diagnostic is asserted with savepoint
recovery; actual child RX, row waits, cancellation and Reset/Restore no-retarget
have deterministic private/public witnesses. Both modes retain the genuine
post-action DROP/recreate and held current-action checks. No general DROP
admission or adjacent partition/lock capability is added.

Private `^TestForeignActions(Definition|Equality|Admission|Closure|Defaults|Diagnostics|Prepared)`
checks canonical immutable metadata, physical predecessor/successor lineage,
unspoofed child admission, ordinal default demand and reporting authority.
`^TestForeignActions(Locks|Cancellation)` checks exact RX/read and row modes,
wait mutex release, latest payload/version and replacement/cancel cleanup.
`TestForeignActionsLocksTwoPhase` proves detached child grants and deletion
ownership through both prepared outcomes. `TestForeignActionsLocksSnapshot`
checks moved-child40001 from an admitted stable image even when its successor
commits before action discovery. The owner/FORCE reporting unit installs an
immutable owner descriptor through the existing validated catalog-stage API;
it does not imply public ALTER TABLE OWNER support.
Keep incumbent late-postpublication cancellation and conflict UPDATE regression
bodies unchanged and select
`make check RUN='^(TestDeleteCorePublicationCancellation|TestDeleteReturningCancellation|TestUpdateReturningCancellation|TestInsertConflictUpdateIdentity|TestInsertConflictUpdateDistinctStage|TestInsertConflictUpdateCursorFailure)'`.
Race proof selects the private groups and
`^TestReferenceForeignActions(Concurrent|Cancellation)` with `RACE=1`; shared
concurrent definitions also require `REFERENCE=1`. Parser proof remains
`^TestForeignKeyGrammar`. Feature smoke selects
`^TestReferenceForeignActionsFeatureSmoke` in both modes.
Native Dump/Reset/Restore phases are genuine public controls, not simulated PG
APIs; shared SQL assertions surround them. These selections describe proof
obligations, not recorded passing results.

Stable-isolation insertion/move-into-key regressions select
`make check RUN='^TestReferenceForeignActionsConcurrentNewRows$'` and the
identical `REFERENCE=1` command. These sixteen DELETE/UPDATE × insertion/move ×
RR/Serializable × direct/prepared schedules assert complete `40001` action
context, retained snapshots, aborted state/savepoint recovery and the peer's
committed child. The private empty-root crosscheck selects
`make check RUN='^TestForeignActionsLocksNewRows$'`.

Select the allocation/behavior complement explicitly:
`make check RUN='^TestForeignActionsExclusionCompletion$'`.
It is not covered by the older private prefix groups above. Real completion
with retained deferred events may add at most the incumbent remaining-event
vector allocation relative to a settled control; unrelated SELECT and unchanged
parent-key UPDATE must not add a second pass. Subsequent SET IMMEDIATE still
raises the genuine retained `23P01`. No production instrumentation or checker
call counter supplies that proof. Complete private coverage is
`make check RUN='^TestForeignActions'`; race proof additionally selects
`make check RACE=1 RUN='^TestForeignActionsLocksNewRows$'` and the public
ConcurrentNewRows command with `RACE=1` in native and real reference modes.


FK regression remediation keeps incumbent keyword and consumer semantics:
INITIALLY remains usable by EXCLUDE/PRIMARY KEY/UNIQUE controls, and FOREIGN
retains its keyword-label behavior. Supporting-index and referenced-column
RESTRICT retain complete dependency diagnostics; CASCADE prunes definitions,
not child data. The internal `TestForeignKeyQueuedNonKeyCompatibility` observes
the actual pending DELETE and compatible non-key grant on the same parent RowID.
`TestForeignKeyPrivateFinalImage` covers parent keys born in a prior own INSERT,
not merely replacements with a committed predecessor.

`TestReferenceForeignKeyNetworkSupportingKeys` independently checks primary
and standalone inet_ops providers, equivalent INET/CIDR spellings, duplicate
parent rejection, missing child rejection and unchanged typed rows. Its
supporting-key consumer reuses the existing comparator bucket, not a new
network grouping/hash provider. Select the following existing consumer
definitions in both native and real modes as well:
`make check RUN='^TestReferenceUnique(TypedEquality|IndexTypedEquality|IndexProviderBoundaries|IndexDeclaredTypes|IndexBuild|IndexAtomicity|IndexContention|IndexSupportedControlsPreserveState)$'`
and the identical command with REFERENCE=1.

Parser/internal regression selection:
`make check RUN='^(TestMappedKeywordLabels|TestExclusionSyntax|TestPrimaryKeyCapabilityRecords|TestUniqueDeferrabilityCapabilityBoundary|TestObjectGrantReferencesAndForeignKeyGrammar)$'`;
canonical lock compatibility selection: `make check RUN='^TestRowLock'`.
Existing reference controls remain selected in both modes with
`make check RUN='^(TestReferenceExclusion|TestReferencePrimaryKeyBoundaries|TestReferenceUniqueExcludedControls|TestReferenceInsertConflictProviderBoundaries|TestReferenceInsertConflictExcludedActions)'`.
All changed FK definitions above require fresh native and pinned evidence.
The expression-index, stored internal `"char"` and INSERT suffix witnesses
assert existing native capability diagnostics versus real PostgreSQL behavior;
they do not weaken the admitted FK consumer checks. Schema GRANT is only
PostgreSQL fixture setup; the table/column REFERENCES and redaction assertions
remain shared. No discovery filter or executable discovery scaffold is added.

The positioned DELETE FK caller is shared in
`TestReferenceDeleteCurrentOfUsingReturningAndConstraints`: its FK failure
preserves the parent row and positioned cursor, and removing the child permits
deleting that same physical parent occurrence. The unrelated USING/RETURNING,
constraint and cursor eligibility phases remain intact. Select the complete
definition with
`make check RUN='^TestReferenceDeleteCurrentOfUsingReturningAndConstraints$'`
and the identical command with REFERENCE=1.

DROP COLUMN regression selection preserves incumbent AUTO ownership, immutable
staged owners and exact default/CHECK/policy notice schedules. The shared
`TestReferenceForeignKeyDropColumnCaller` additionally observes mixed FK/view
ordered RESTRICT and CASCADE, child-data survival and local self-FK AUTO
retirement. `TestReferenceForeignKeyProducers` shares real23503 for the incoming
admitted MERGE MATCHED UPDATE; it keeps independent INSERT suffix boundaries.
Required fresh native and pinned selections (not observed passes):

```sh
make check RUN='^TestReferenceForeignKey(DropColumnCaller|DefinitionTypes|Producers)$'
make check REFERENCE=1 RUN='^TestReferenceForeignKey(DropColumnCaller|DefinitionTypes|Producers)$'
make check RUN='^(TestCheckReview2OrderOrdinalInvariants|TestRegclassDropColumnStagedOwnerDefinitions|TestTemporaryTableRetainedLiveRelationProviders)$'
make check RUN='^TestReference(RegclassDropColumnDependencies|RegclassFeatureSmoke|RLSPolicyDependencies|AlterNullabilityReviewRemainingDependency|AlterTypeCheckDependencies|HashIndexLifecycle|PartialIndexLifecycle|PartialIndexUniqueCurrentNames|PartialIndexUniqueDependencyClosure|ExpressionIndexLifecycle|LikeStoredDependencies|LikeStoredPatternEscapeDependencyRestrictionsSourceConsumerRoutes|CheckAlterOwnedSequenceAutoConstraint|CheckControls|CheckLifecycle|TableLikePartialProviders)'
make check REFERENCE=1 RUN='^TestReference(RegclassDropColumnDependencies|RegclassFeatureSmoke|RLSPolicyDependencies|AlterNullabilityReviewRemainingDependency|AlterTypeCheckDependencies|HashIndexLifecycle|PartialIndexLifecycle|PartialIndexUniqueCurrentNames|PartialIndexUniqueDependencyClosure|ExpressionIndexLifecycle|LikeStoredDependencies|LikeStoredPatternEscapeDependencyRestrictionsSourceConsumerRoutes|CheckAlterOwnedSequenceAutoConstraint|CheckControls|CheckLifecycle|TableLikePartialProviders)'
make check RUN='^TestReferenceTemporaryTableExpressionCommitDrop$'
make check REFERENCE=1 RUN='^TestReferenceTemporaryTableExpressionCommitDrop$'
```

The session-only public smoke uses finite context, a real prepared composite
child INSERT, exact nullable INT4/TEXT rows, full23503 fields, atomic parent
UPDATE/DELETE, savepoint recovery, actual Reset before persistent Restore,
SIMPLE NULL, cyclic replay/redump, invalid final import preserving sentinel91
and held Rows, and retained TEMP identity through Reset and Restore. Successful
stdout is empty; mismatches exit nonzero. It is not a staged deliverable.

Independent FK review remedies require these exact completed selectors in native
and real pinned PostgreSQL modes (external900-second deadlines):

```sh
make check RUN='^TestReferenceForeignKeySupportingFamily'
make check REFERENCE=1 RUN='^TestReferenceForeignKeySupportingFamily'
make check RUN='^TestReferenceForeignKey(ClauseStructure|NotNullNameCollision|TypeAtomicity)'
make check REFERENCE=1 RUN='^TestReferenceForeignKey(ClauseStructure|NotNullNameCollision|TypeAtomicity)'
make check RUN='^TestReferenceForeignKeyRestore(SkippedCreate|CapturedAuthority)'
make check REFERENCE=1 RUN='^TestReferenceForeignKeyRestore(SkippedCreate|CapturedAuthority)'
make check RUN='^TestForeignKey'
make check RACE=1 RUN='^TestForeignKey'
```

The eight new shared definitions are SupportingFamilyAdmission,
SupportingFamilyCoercion, SupportingFamilyRestore, ClauseStructure,
NotNullNameCollision, TypeAtomicity, RestoreSkippedCreate and RestoreCapturedAuthority
(each prefixed TestReferenceForeignKey). They independently assert full diagnostics,
metadata, stable/reminted identities, complete typed bags, recovery and failed
publication. All eight temporary PostgreSQL-only discovery returns are removed
from the four foreign_key_review_* reference files; native-control-only phases
remain backend-specific inside otherwise complete shared definitions.

Package seams TestForeignKeyIndexProbeVisitBounds, SelectedFamilyAuthority,
TypeFailedSnapshotAuthority, TrustedReplayNamespacePrincipal and CreateOutcomeIdentity
(each prefixed TestForeignKey) complement the shared observations. Count proof wraps
the actual selected-family ordered comparator and actual entry visitor over a
4096-entry native logical index; it asserts bounded lower-bound work, first-match
stop, exact source version locators and unchanged borrowed entries, not wiring counts.
Snapshot proof retains original metadata/storage/index authority through failed and
successful TYPE; namespace proof separates transient replay permissions from semantics.
The name_ops class uses its released text_ops family identity without adding a new
type/index capability; existing unique-index typed/provider/catalog assertions must
also run in both modes:

```sh
make check RUN='^TestReferenceUnique(TypedEquality|IndexTypedEquality|IndexProviderBoundaries|IndexDeclaredTypes|IndexBuild|IndexAtomicity|IndexContention|IndexSupportedControlsPreserveState)'
make check REFERENCE=1 RUN='^TestReferenceUnique(TypedEquality|IndexTypedEquality|IndexProviderBoundaries|IndexDeclaredTypes|IndexBuild|IndexAtomicity|IndexContention|IndexSupportedControlsPreserveState)'
```

These are required proof commands, not observed passes. Existing FK families,
migrated callers, parser controls and standalone session-only public smoke above
remain selected. Whole-project Docker-free verification remains quickcheck.

Post-rebase FK fixture remedies preserve raw NUMERIC transport and held pid INT4
metadata/nullability/content after failed Restore. Incoming ON CONFLICT target
DO UPDATE is admitted: Producers asserts its no-RETURNING23503 and unchanged
complete bags in both modes; only RETURNING supplies the positioned native suffix
exclusion for the combined query. Required fresh selections (900 seconds each):

```sh
make check RUN='^TestReferenceForeignKey(SupportingFamilyAdmission|RestoreSkippedCreate|Producers)$'
make check REFERENCE=1 RUN='^TestReferenceForeignKey(SupportingFamilyAdmission|RestoreSkippedCreate|Producers)$'
make check RUN='^TestEnum(GenerationPublicationAtomicity|OperationSafetyCapture)$'
```

Incoming enum staged-table fixture callers pass the empty FK reference-grant
inventory explicitly; their original provenance/publication/safety assertions
are unchanged.

### Partitioned exclusions

The release oracle is PostgreSQL18.0, not development-source index comparison.
`TestReferencePartitionExclusionDefinition`, `Hierarchy`, `Attachment`, `Rows`,
`Metadata`, `Deferral`, `Atomicity`, `Concurrency`, `Controls`, `Capabilities`,
`Collation`, `IncompleteControls` and `FeatureSmoke`
prescribe independent diagnostics, actual overlap, typed direct/prepared values,
virtual/physical provider ancestry, scoped local declarations, rollback and native
replacement. GiST membership uses the native logical exclusion provider, not a
physical PostgreSQL index or an extension-installed scalar operator class.

Run each group natively and with `REFERENCE=1`, under an external900-second deadline:

```sh
make check RUN='^TestReferencePartitionExclusion(Definition|Attachment|Metadata)$'
make check RUN='^TestReferencePartitionExclusion(Hierarchy|Rows|Deferral)$'
make check RUN='^TestReferencePartitionExclusion(Atomicity|Concurrency|Controls|FeatureSmoke)$'
make check RUN='^TestReferencePartitionExclusion(Capabilities|Collation)$'
make check RUN='^TestReferencePartitionExclusionIncompleteControls$'
make check RUN='^TestReferencePartitionExclusionConcurrency$' RACE=1
make check RUN='^TestReferenceRangePartitionLeafExclusion$'
make check RUN='^TestReferenceExclusion(MethodBoundaries|ConcurrencyCancellation|PreparedPeerOutcome)$'
```

Every changed preexisting definition above also requires both selected modes.
The LIST equality-family case uses the same PostgreSQL-valid hierarchy and
overlap assertions on both backends. Controls replay the actual emitted native
SQL on the pinned server, with no exclusion index ATTACH rewrite.

GiST capability assertions distinguish key and INCLUDE false/NULL results,
including clusterable true and backward scans false. TEXT B-tree exclusions
separately prove exact collation identity through rejected definitions,
descendant creation, populated subtree attachment and preserved provider graphs.

Incomplete controls cover ONLY gaps, partial and DEFAULT-gap trees, independently
constrained attached subtrees, complete-invalid and childless-invalid roots, and
nested local scope. Exact graph/validity, typed rows, canonical dump bytes,
unmodified emitted-SQL PostgreSQL replay and future enforcement are asserted.

For a standalone public smoke, open an isolated `stuntgres.NewDriver()` connector
with `sql.OpenDB`, create RANGE(INET) plus DEFAULT leaves and declare
`EXCLUDE USING gist(tenant inet_ops WITH =,during WITH &&)`. Insert tenant
10.0.0.1 `[1,5)`, require prepared same-tenant `[3,7)`23P01 from the low leaf,
then accept another tenant, another partition and adjacent `[5,8)`. Read original
INT4/INET/INT4RANGE values and metadata, inspect public x ancestry and GiST operator
identities, roll back a late multileaf conflict, then Dump→Reset→Restore→redump.
Reprepare after replacement and prove both restored conflict and new-tenant
admission. Keep the program independent of the reference fixture so it exercises
the embedding API directly.

Extend the standalone recipe with populated overlapping gaps before ONLY ADD,
later covered children, a populated attached subtree with an independent local
exclusion, and complete-invalid/childless-invalid roots. Recheck provider gaps and
invalid flags before and after native replay, require canonical bytes and prove
both unconstrained-gap acceptance and restored covered-provider rejection.


### LIST partition definitions

The shared LIST scenarios prescribe direct/prepared routing, NULL and DEFAULT
ownership, nesting, exact typed values, diagnostics, metadata and atomic failure.
Same-list duplicate values and NULLs normalize; sibling ownership conflicts fail.
Compound-bound ownership diagnostics point to the expression's first operand,
not its operator token: `IN(0+1)` overlap names the position of `0`. Scalar
operator errors retain their own expression-node diagnostic locations.
Native Reset/Dump/Restore are explicit native-only phases. Public LIST ATTACH
remains a native capability boundary; actual native dump fallback replay is not.

Run these small sets in both modes (add `REFERENCE=1` for pinned PostgreSQL18.6),
with an external 900-second deadline:

```sh
make check RUN='^TestReferenceListPartition(Definition|Routing|Nesting)$'
make check RUN='^TestReferenceListPartition(Ownership|Diagnostics)$'
make check RUN='^TestReferenceListPartition(Types|Metadata)$'
make check RUN='^TestReferenceListPartition(Atomicity|Cancellation|Authorization)$'
make check RUN='^TestReferenceListPartition(Controls|ReplayBoundary)$'
make check RUN='^TestReferencePartitionExclusionHierarchy$'
make check RUN='^TestReferenceListPartitionFeatureSmoke$'
```

The feature-smoke scenario checks direct and prepared writes to a nested LIST
tree, physical leaf contents, NULL/default routing, result metadata, exact direct
ancestry, overlap and occupied-DEFAULT rejection, reusable failed names, retained
rows/held statements and native reset/restore/fresh-destination replay. When the
oracle is enabled it consumes the actual owner-bearing native dump bytes too.

### Inherited partition constraints

PostgreSQL18.6 is the oracle for the shared direct/prepared partition CHECK and
NOT NULL scenarios. Independent expectations cover structural CHECK merge,
column-identity NN merge and operation-specific names, exact local/count/own-OID
metadata, recursive per-action stops, all three ONLY DROP forms, fresh versus
existing SET, leaf diagnostics/UNKNOWN evaluation, owner checks, late validation
atomicity and held prepared consumers. Mixed selected/unrelated ALTER is an
explicit native capability boundary, not partial execution.

Run each small group natively and with `REFERENCE=1`, under an external900-second
deadline; selected cancellation also requires `RACE=1` native proof:

```sh
make check RUN='^TestReferencePartitionInherited(Creation|Enforcement|Names|Methods)$'
make check RUN='^TestReferencePartitionInherited(Alter|Drop|Only|ExistingSet|AddNames|Consumers)$'
make check RUN='^TestReferencePartitionInherited(Atomicity|ConflictAtomicity|Diagnostics|Authorization|Boundary)$'
make check RUN='^TestReferencePartitionInheritedCancellation$'
make check RUN='^TestReferencePartitionInheritedCancellation$' RACE=1
make check RUN='^TestReferencePartitionInherited(Controls|Replay)$'
make check RUN='^TestReferencePartitionInheritedFeatureSmoke$'
```

Controls exercise native Dump/Reset/Restore, repeated roundtrip and failed or
canceled replacement preserving sentinel state and an already-open cursor.
Replay sends actual unchanged native dump SQL to PostgreSQL using the existing
owner/admin replay helpers; SQL histories alone do not establish emitted replay.
Native controls and eager cursor replacement are explicitly native-only, not
invented PostgreSQL APIs or physical durability claims.

The feature smoke adds a recursive CHECK to a two-leaf tree, proves physical
enforcement and typed rows/metadata, removes it without replacing unrelated
CHECK identities, then recursively drops parent NN. The inherited-only low leaf
becomes nullable while the high leaf's explicit local NN survives with its own
OID and continued enforcement. Native roundtrip preserves those final facts,
the three-row bag and renewed enforcement.
