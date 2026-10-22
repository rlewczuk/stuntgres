# reference/catalogs

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### Independently owned pg_attribute shape matrices

`TestReferencePGAttributeShape` owns ten fixtures, created before backend work:
two empty shapes, populated metadata, four ANYARRAY NULL compositions, NULL
aggregates, rejected CTAS plus absence, and builtin identities. No live context
is refreshed. Each empty shape retains direct/fresh-prepared Context and actual
plain methods plus two reads through the SAME held statement. Literal25-column
labels/types, raw descriptors and native label ownership remain exact.
Each NULL composition keeps direct/fresh-prepared/held routes; count0/isnulltrue,
CTAS42P16/no publication and builtin1249/75/270 remain independent expectations.

```sh
make check RUN='^TestReferencePGAttributeShape$' REFERENCE=1
make check RUN='^TestReferencePGAttributeShape$'
make check RUN='^TestReferencePGAttributeShape$' RACE=1
```

Use external900-second deadlines. Standalone public smoke should inspect actual
empty raw metadata, scalar/LEFT JOIN ANYARRAY NULL,0/true and rejected CTAS
absence, not infer them from source structure or backend parity.

### MACADDR8 catalog admission

The independent supported-operator TSV admits only released MACADDR8 identities
3362–3367, with operands774, BOOL16 results, routines4113–4118 and exact released
commutator/negator/estimator fields. Equality's merge/hash flags remain catalog
facts, not native default ordering, grouping or indexing providers. Existing
complete bags remain unfiltered on native; no other type's rows are removed.
`TestReferencePGTypeAdmission` adds scalar774 and lookup-only companion775.
`TestAnyValueProviderDatums` now exercises concrete eight-byte MACADDR8 carriers,
six-byte expansion, zero, high-bit and all-one addresses; `TestOrderingUsage`
continues to reject default MACADDR8 ordering.

Changed shared expectations affect `TestPGOperatorCaptureOwnership` and these
reference definitions: `TestReferencePGOperatorInventory`, `Permissions`,
`Lifecycle`, `FeatureSmoke`, `PolymorphicRelationships`, `PolymorphicIdentity`,
and `References` (all suffixes use the `TestReferencePGOperator` prefix).
The fixed-signature ResolverAgreement and focused LSEG consumers retain their
existing expected rows; FeatureSmoke still exercises them as dependencies.

```sh
make check RUN='^TestReference(PGOperator(Inventory|Permissions|Lifecycle|FeatureSmoke|PolymorphicRelationships|PolymorphicIdentity|References)|PGTypeAdmission)$' REFERENCE=1
make check RUN='^TestReference(PGOperator(Inventory|Permissions|Lifecycle|FeatureSmoke|PolymorphicRelationships|PolymorphicIdentity|References)|PGTypeAdmission)$'
make check RUN='^(TestPGOperatorCaptureOwnership|TestAnyValueProviderDatums|TestOrderingUsage)$'
```

Use external900-second deadlines. Both selected public modes are required after
these shared expectation changes. For the feature smoke, select
`make check RUN='^TestReferencePGOperatorFeatureSmoke$'` with and without
`REFERENCE=1`; expect exit0/package `ok`, complete exact inventory and symbolic
references, and the existing lifecycle replay/permissions/polymorphic assertions.
These commands describe required proof, not verification already performed.

### PATH catalog admission

PATH scalar602 and lookup-only companion1019 join the complete independent
type-admission bag. The supported-operator TSV includes released OIDs792–796:
five point-count comparisons with no negators or merge/hash authority. Procedure
identities are984/982/983/985/986; only equality has eqsel/eqjoinsel estimators.
These facts come from REL_18_6 pg_operator.dat, not native capture output.
Existing capture, inventory, references, polymorphic, permission and lifecycle
assertions retain complete bags and consume these additional released rows.
The datum-carrier unit matrix includes open/closed paths, repeats, precise
coordinates, signed zero, subnormals and nonfinite values; ordering admission
continues to reject PATH.

```sh
make check RUN='^(TestReferencePGOperator.*|TestReferencePGTypeAdmission)$'
make check RUN='^(TestReferencePGOperator.*|TestReferencePGTypeAdmission)$' REFERENCE=1
make check RUN='^(TestPGOperator(CaptureOwnership|ProviderAgreement)|TestAnyValueProviderDatums|TestOrderingUsage)$'
```

Use external900-second deadlines. Every public definition consuming the changed
shared TSV requires both modes; complete bags are not narrowed to PATH rows.

### LINE catalog admission

LINE scalar628 and lookup-only companion629 join the independent type-admission
expectations. Released equality operator1616 uses routine1492, self-commutation,
no negator, and no merge/hash class flags. The independent supported-operator
TSV includes that released row; provider/capture and public inventory, reference,
polymorphic, permission and lifecycle assertions consume it without deriving
expected facts from production metadata.

```sh
make check RUN='^(TestReferencePGOperator.*|TestReferencePGTypeAdmission)$'
make check RUN='^(TestReferencePGOperator.*|TestReferencePGTypeAdmission)$' REFERENCE=1
make check RUN='^(TestPGOperator(ProviderAgreement|CaptureOwnership)|TestAnyValueProviderDatums|TestOrderingUsage)$'
```

Use external900-second deadlines; both public modes are required after changing
the independent shared release inventory.

### LOAD native-library boundary

The six shared `TestReferenceLoad` definitions cover syntax/string decoding,
unreserved identifiers, direct/prepared authorization, lexical filename rules,
held execution-time role and SUPERUSER changes, real lookup versus the embedded
boundary, repeated installed-library requests, read-only/savepoint recovery,
sentinel and installed-extension preservation, and unexpected notices.
`TestReferencePGExtensionCommandBoundaries` also checks LOAD's execution-time
boundary instead of the obsolete native parser rejection.

Expectations originate in PostgreSQL 18 `LOAD`, dynamic-loading documentation,
and released `REL_18_6` `dfmgr.c`/`gram.y`; the selected oracle is the repository's
pinned PostgreSQL 18.6 UTF8/C container. Each backend is asserted independently:

| Request | Native | PostgreSQL |
|---|---|---|
| Ordinary role, bare name or nested/traversing plugin path | `42501`, `access to library "<filename>" is not allowed` | Same |
| Superuser, `$bad/missing` | `42602`, `invalid macro name in path: $bad/missing` | Same |
| Authorized absent bare/plugin/absolute/relative filename | `0A000`, `LOAD native libraries are unsupported in embedded mode` | `58P01`, `could not access file "<filename>": No such file or directory` |
| Superuser, installed `plpgsql`, including repeated attempts | Same `0A000` | Success, affected count zero |

Valid protocol Prepare succeeds before authorization/loading; malformed grammar
fails at Prepare (or direct execution). SQL `PREPARE ... AS LOAD` is not valid.
The fixture checks missing-file candidates and `.so` retries **inside the oracle
container only**, requires `dynamic_library_path=$libdir`, and proves the
installed `plpgsql.so` witness. Unexpected files, a missing library, Docker or
image failures are failed preconditions, never skips or manufactured fixtures.
No SQL sentinel alone proves the absence of dynamic loading: the native
implementation deliberately contains only authority and lexical checks.

Cancellation checks are native-only: pre-canceled Prepare/Execute require
`context.Canceled` with no published handle/result. Pinned pq pre-canceled
Prepare showed varying handle/connection outcomes during oracle runs, including
connection closure; this adapter lifecycle is not LOAD behavior. The reference
scenario does not exercise pq pre-cancellation or impose a driver outcome.
Ordinary non-canceled direct/prepared/held LOAD behavior remains shared.

```sh
timeout 900s make check REFERENCE=1 RUN='^TestReference(Load(Syntax|Authorization|Resolution|Prepared|Transactions|FeatureSmoke)|PGExtensionCommandBoundaries)$'
timeout 900s make check RUN='^TestReference(Load(Syntax|Authorization|Resolution|Prepared|Transactions|FeatureSmoke)|PGExtensionCommandBoundaries)$'
timeout 900s make check RUN='^TestLoad(Grammar|Syntax|Validation|ExecutorNoPublication)$'
timeout 900s make check RACE=1 RUN='^(TestLoadExecutorNoPublication|TestReferenceLoadPrepared)$'
```

Bounded public feature smoke:

```sh
timeout 900s make check RUN='^TestReferenceLoadFeatureSmoke$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceLoadFeatureSmoke$'
```

Expected observations: exact `42501`/`42602`, native `0A000` versus genuine oracle
`58P01`, held authority changes, read-only savepoint recovery, sentinel `7`,
unchanged extension identities/rows, and no notices. Native never opens host
files or loads process code. These selectors state the proof contract, not an
unexercised claim of passing verification.


### pg_largeobject page projection

The shared `TestReferencePGLargeobject` scenarios specify actual sparse page
bytes, qualified/unqualified metadata, catalog identity links, independent table
and large-object ACLs, execution-time held-statement authorization, transaction
and savepoint publication, retained buffers, and fresh VOLATILE SQL-body versus
outer-query snapshots. Native-only Dump/Reset/Restore phases reuse the same SQL
baseline and check exact page keys, owners and NULL/empty/populated raw LO ACLs.
These definitions are contracts, not a claim that implementation or verification
has completed. Oracle discovery follows the shared testing guide.

```sh
make check RUN='^TestReferencePGLargeobject(Shape|Pages|Authorization|Transactions|Prepared|Snapshots|Controls|Diagnostics|Smoke)$' REFERENCE=1
make check RUN='^TestReferencePGLargeobject(Shape|Pages|Authorization|Transactions|Prepared|Snapshots|Controls|Diagnostics|Smoke)$'
make check RUN='^TestReferencePGLargeobject(Transactions|Snapshots)$' RACE=1
```

Root owns execution with an external900-second deadline. Standalone public-API
smoke source is the session artifact `local://pg-largeobject-projection-smoke.go`,
not repository scaffolding. Materialize it as an absolute temporary `SMOKE_GO`
and run `timeout 900s go run "$SMOKE_GO"` from the worktree. It checks split and
sparse bytes, protected-table and independent LO authority, held revocation,
rollback, native replay and the `aa00,bbff,aa00` outer/body/outer witness. Expected
stdout is
`pages=split-exact,sparse-exact acl=independent held=rechecked tx=rollback-exact replay=exact snapshots=aa00,bbff,aa00`.


### pg_transform selected proof

The seven `TestReferencePGTransform` definitions independently specify complete
empty qualified/unqualified inventories and exact OID/OID/OID/REGPROC/REGPROC
metadata, retained descriptors, class/attribute/row-array links, the four declared
catalog foreign keys, pg_tables inclusion and pg_views exclusion. No implemented
transform provider exists; unaccent and ordinary SQL routines do not create one.
Empty joins witness usable SQL paths, not populated transform identity fidelity.

Prepared coverage uses direct, fresh-prepared and the same physically held
statements for exact OID/type-language probes, NULL and maximum OID arguments,
unrelated DDL and native precancellation. Complete diagnostics cover missing
columns (including multibyte character positions), ordinary-role zero-row mutation
guards and execution-time authority after SET ROLE. Native superuser immutability
and row-lock boundaries are explicitly distinct from PostgreSQL behavior.
Lifecycle coverage retains a stored view, sentinel7, held read and old empty
metadata across actual native Dump/Reset/Restore.

The oracle-first definitions were observed against the pinned PostgreSQL18.6
fixture before native implementation. pq reports interface{} ScanType for all
five columns; native OID uses int64 and REGPROC uses string. Row/array checks use
reciprocal links rather than assuming PostgreSQL's allocated rowtype numbers.
Runtime proof after implementation is root-owned, not implied by this contract.

Run each changed reference definition in both modes before review:

```sh
make check RUN='^TestReferencePGTransform(Inventory|Shape|CatalogLinks|Prepared|Diagnostics|Permissions|Lifecycle)$'
make check RUN='^TestReferencePGTransform(Inventory|Shape|CatalogLinks|Prepared|Diagnostics|Permissions|Lifecycle)$' REFERENCE=1
make check RUN='^TestReferencePGTableViewsBuiltins$'
make check RUN='^TestReferencePGTableViewsBuiltins$' REFERENCE=1
make check RUN='^TestPGTransform(Capture|Ownership)$'
make check RUN='^Test(PgClassBuiltinIndexMetadata|TableViewSummaryBuiltinIdentity)$'
```

Root's standalone public-API smoke source lives outside the repository in the
OMP session, resolved to `PG_TRANSFORM_SMOKE`. From the repository root:

```sh
go run "$PG_TRANSFORM_SMOKE"
```

Expected exit0: exact five-column typed empty inventory; a real held pair probe
with ordinary and NULL arguments; exact missing-column error; stored view and
sentinel7; actual Dump/Reset/Restore preserving catalog, view, sentinel and saved
metadata. This is an independent consumer exercise, not a permanent aggregate
test that reruns other definitions. Root records literal argv and observations
alongside selected dual-mode proof and whole-project checks.

### pg_ts_config selected proof

The seven `TestReferencePGTSConfig` scenarios independently specify the empty
native configuration inventory and the PostgreSQL18.6 builtin witness
`(3748,"simple",11,10,3722)`. They cover exact five-column descriptors, direct,
fresh-prepared and physically held name/OID predicates, NULL/absent identities,
relation/attribute/row-array discovery, namespace/owner/parser links and declared
foreign keys. Native unaccent installation must not fabricate configurations.
No oracle-discovery filters remain.

Diagnostics assert SQLSTATE, English text, character positions and all logical
optional fields through the shared fixture. Ordinary-role DML/CTE/MERGE/TRUNCATE
denials preserve sentinel and sequence state; a superuser-prepared write must
use fresh role authority. PostgreSQL zero-row superuser writes run in rollback;
native mutations explicitly remain unsupported. Native-only phases exercise
precancellation and Dump→Reset→Restore with a restored view, sentinel, held read
and retained empty-cursor metadata. Symbolic REGCONFIG remains unsupported.

Run each public selector both as written and with `REFERENCE=1`, under an
external900-second deadline:

```sh
make check RUN='^TestReferencePGTSConfig(Inventory|Shape|CatalogLinks)$'
make check RUN='^TestReferencePGTSConfig(Diagnostics|Permissions|Prepared|Lifecycle)$'
make check RUN='^TestReferencePGTableViewsBuiltins$'
make check RUN='^TestReferenceRegTSDiagnostics$'
```

Package-only invariants supplement the public scenarios:

```sh
make check RUN='^TestPGTSConfig(Capture|Ownership)$'
make check RUN='^Test(PgClassBuiltinIndexMetadata|TableViewSummaryBuiltinIdentity)$'
```

The standalone public smoke supplied in the part plan runs from the repository
root as `go run "$PG_TS_CONFIG_SMOKE"` using its OMP session-file path. Expected
output is `pg_ts_config: empty inventory; exact shape; guarded writes; restored metadata`
and exit0. It composes exact shape, empty inventory after unaccent, held reads,
ordinary-role errors and native control replay. These are expected observations,
not a claim of executed verification. Root owns selected dual-mode proof,
whole-project quickcheck and post-integration finalcheck.

### Information-schema user mappings

The complete untagged `TestReferenceInfoUserMappings` scenarios prescribe the
three-column `sql_identifier`/NAME projection, nullable catalog/domain metadata
and populated/empty direct, prepared and held results. Actual named/PUBLIC
mappings use independent database names; options never add columns or leak
secrets. Visibility is inherited server ownership OR effective server USAGE,
not mapping ownership: authorized readers see other roles and PUBLIC too.
The matrix includes inherited/PUBLIC grants, SET-only membership, wrapper-USAGE
independence, hidden servers and held grant/revoke/role transitions.
Lifecycle covers mapping creation/drop, unconditional server rename,
rollback/savepoint restoration and server CASCADE. PostgreSQL role rename updates
the exact held rows; native actual Prepare asserts the complete existing
`ERROR`/`0A000`/`statement is unsupported`/Position 1 diagnostic and unchanged rows,
then continues the same lifecycle with its original role name. A producer unit
independently replaces the role descriptor at the same PrincipalRef, proving
current-name resolution while retaining the old root, rows and mapping descriptors.
Lifecycle and Smoke assert the complete PostgreSQL single-PUBLIC-dependency
CASCADE NOTICE versus explicitly zero native notices, then exact empty held rows.
Diagnostics retain exact PostgreSQL English records and SQLSTATE for missing
columns and protected base-catalog reads. No adjacent role DDL or notice emission
implementation is introduced.

Run each selected command with an external 900-second deadline:

```sh
make check RUN='^TestReferenceInfoUserMappings(Shape|Visibility|Lifecycle|Diagnostics|Smoke)$'
make check REFERENCE=1 RUN='^TestReferenceInfoUserMappings(Shape|Visibility|Lifecycle|Diagnostics|Smoke)$'
make check RUN='^TestReferenceFDW(MetadataLifecycle|Security|MappingOwnership|SourceSecurity)$'
make check REFERENCE=1 RUN='^TestReferenceFDW(MetadataLifecycle|Security|MappingOwnership|SourceSecurity)$'
make check RUN='^TestReferenceInfoSQLIdentifier(Metadata|Identity)$'
make check REFERENCE=1 RUN='^TestReferenceInfoSQLIdentifier(Metadata|Identity)$'
```

Standalone feature smoke:

```sh
make check RUN='^TestReferenceInfoUserMappingsSmoke$'
make check REFERENCE=1 RUN='^TestReferenceInfoUserMappingsSmoke$'
```

Expected exit zero with quiet package summaries. Assertions exercise exact
identity rows without options, held empty→all→empty grant/revoke transitions,
rollback restoration and committed CASCADE retirement. Setup is metadata-only:
no foreign provider, remote connection or external FDW service is involved.
This feature neither adds `user_mapping_options` nor changes existing
`pg_user_mappings` option redaction or SQL/MED native-control boundaries.

### Extension removal

The nine new removal definitions independently specify PostgreSQL18.6
installed-manifest retirement, missing/owner/read-only diagnostics, name-list
atomicity, RESTRICT/CASCADE closure, rollback/savepoint callable identity,
generic installed types/arrays/aggregates, automatic materialized attachments
and survivor native control replay. Native controls are explicitly native-only;
the SQL scenarios share one body. No discovery filters remain.

The domain CHECK witness additionally requires the actual constraint-to-function
normal pg_depend edge, exact RESTRICT/23514/CASCADE diagnostics, Hotel/NULL
enforcement, identity restoration after rollback and surviving domain/table/rows
after committed CHECK pruning. The normal edge is owned by the constraint OID,
not inferred from the domain name or assigned to its type OID.

Late-consumer scheduling belongs to private engine seams, not a claimed public
PostgreSQL locking schedule. Six real publication/outer-commit cells cover views,
query_int columns and domain CHECKs; two explicitly unsupported query_int[] cells
assert the native 0A000 admission boundary with no mutation. Genuine alias
predicate/retirement and supported domain-array lifetime controls supplement,
but do not replace, those six fences.

Run every selector below both as written and with `REFERENCE=1`; use an
external900-second deadline for each command:

```sh
make check RUN='^TestReferenceExtensionDrop(Basic|Dependencies|Diagnostics|Lists)$'
make check RUN='^TestReferenceExtensionDrop(Transactions|Controls|Providers|DomainChecks)$'
make check RUN='^TestReferenceMaterializedExtensionDrop$'
make check RUN='^(TestReferencePGExtensionCommandBoundaries|TestReferencePGAggregateIntaggIdentity|TestReferencePGTypeFamilies)$'
make check RUN='^TestReferenceRegProc(SearchPath|PreparedAuthority|Persistence)$'
make check RUN='^(TestReferenceExtension(VersionSchemaAndPrivilege|Transactions|Controls)|TestReferencePgcryptoRandomIdentity)$'
make check RUN='^(TestReferenceDomainCheck(Metadata|Diagnostics|ConversionsNull|Ancestors)|TestReferencePGTypeInstalledDomains|TestReferenceExtensionSchemaDependencies|TestReferenceRoleOwnershipExtensionDrop)$'
```

Package-only invariant and selected deterministic interleaving/race proof:

```sh
make check RUN='^TestExtensionDrop(Manifest|Publication|PublicationCandidate|RollbackIdentity|Diagnostics)$'
make check RUN='^TestPGDependDomainCheckDependencies$'
make check RACE=1 RUN='^(TestExtensionDropPublication(Candidate)?|TestPGDependDomainCheckDependencies|TestReferenceExtensionDropTransactions|TestReferenceExtensionTransactions)$'
```

Feature smoke is the first two removal selectors plus Controls: expect Hotel
and typed NULL before removal/after rollback; RESTRICT2BP01 with intact
identities; CASCADE pruning defaults/checks and views while preserving rows,
schemas and a bound soundex survivor; native Dump/Reset/Restore and independent
Driver import keep A500 callable without resurrecting unaccent. The existing
public standalone smoke supplied with the part plan exercises the same lifecycle.

The focused domain smoke is `make check RUN='^TestReferenceExtensionDropDomainChecks$'`
in both modes: expect the authentic edge count1 before RESTRICT, count0 after
private/committed CHECK pruning, the same edge/identities after rollback,
unchanged Hotel/NULL rows and Hôtel acceptance only after CHECK removal.

### Extension version selected proof

The six untagged `TestReferenceExtensionVersion...` definitions independently
specify CREATE1.1/1.2/default and genuine fuzzy1.1→1.2 DM creation, unchanged
old-nine identities/ownership/EXECUTE privileges, direct/new/held statements,
strict NULL/empty arrays, exact metadata and six directed graph pairs.
They retain complete English diagnostics/notices, authority and transaction
rollback, real PostgreSQL loose-member conflict, native control replay of both
versions with stored consumers/data/named owners, and zero-byte TEMP portability.
PostgreSQL supplies broader OS inventories; native current-only unaccent paths
are intentionally empty. Fuzzy1.0 is graph-only and never installable.
Missing-provider path queries require the same complete `0A000` diagnostic on
both backends, including PostgreSQL's system-installation Hint and empty Detail;
no host-specific file path is fabricated.
The genuine native competing-update/ACL/role/namespace/epoch/cancellation
counterparts live in package units, not fake loose provider setup.
Native Restore conflict proof recreates an actually captured named extension
owner during external input and verifies exact incumbent1.1 extension/member
identities, stored consumers/data and DM absence after rejection. Restore
captures principal incarnations, not a global all-DDL catalog predecessor;
the fixture does not invent an arbitrary concurrent-CREATE rejection contract.
The SRF's set-returning contract is asserted through exact rows, RECORD values,
ordinality and strict-NULL results, not an unsupported native pg_proc column.

```sh
make check RUN='^TestReferenceExtensionVersion(InstallAndUpdate|PathsAndMetadata)$'
make check REFERENCE=1 RUN='^TestReferenceExtensionVersion(InstallAndUpdate|PathsAndMetadata)$'
make check RUN='^TestReferenceExtensionVersion(DiagnosticsAndConflict|TransactionsAndAuthority|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceExtensionVersion(DiagnosticsAndConflict|TransactionsAndAuthority|Controls|FeatureSmoke)$'
make check RUN='^TestReference(AvailableExtensionsFiltering|PGExtensionCommandBoundaries)$'
make check REFERENCE=1 RUN='^TestReference(AvailableExtensionsFiltering|PGExtensionCommandBoundaries)$'
```

Use external900-second deadlines. Oracle-first discovery establishes independent
expectations; both selected native and real modes must pass again after affected
definition or implementation edits. Discovery is not final verification.
Feature smoke selector: `^TestReferenceExtensionVersionFeatureSmoke$`.
The standalone public consumer source is session evidence
`local://ext-version-smoke.go`, materialized as `/tmp/pg18-ext-version-smoke.go`
and invoked from the repository root with
`timeout 900s go run /tmp/pg18-ext-version-smoke.go`. It stays quiet on success
and exits nonzero on any identity/result/path/control mismatch.


### Type catalog metadata and proof

`TestReferencePGTypeShape`, `TestReferencePGTypeBuiltins`,
`TestReferencePGTypeFamilies` and `TestReferencePGTypeDiagnostics` define the
32-column PostgreSQL18 `pg_type` contract independently of native production
metadata. The builtin matrix contains114 scalar/special/companion rows derived
from REL_18_6 `pg_type.dat`, `pg_type.h`, `pg_proc.dat` and `Catalog.pm`;
it is a supported positive subset, not PostgreSQL's complete type inventory.
Tests preserve raw NAME/CHAR/REGPROC/tree/ACL representations, full descriptors,
literal numeric procedure identities, actual held statements, empty results,
immediate domain metadata, genuine relation links and ordinary-role errors.
Generated object identities come from independent REGTYPE/REGCLASS authorities.
Seeded `time_stamp` defaults assert the exact expression observed by the pinned
PostgreSQL18.6 discovery: `CURRENT_TIMESTAMP(2)` and SQLVALUEFUNCTION
op4/type1184/typmod2/location-1. Failure diagnostics retain the full raw
default/tree and cursor metadata rather than accepting NULL or a substring.
`TestReferencePGTypeAdmission` includes the selected `information_schema.yes_or_no`
and `information_schema.cardinal_number` domains and companions in the native
complete inventory. Its shared SQL independently checks domain kind and reciprocal
companion ownership on both backends: yes_or_no uses VARCHAR1043/typmod7/C950;
cardinal_number uses INT4/base23/typmod−1/no collation. Catalog presence does not
enable array storage. Selected proof in both modes is
`make check RUN='^TestReferencePGTypeAdmission$' REFERENCE=1` and the same command
without `REFERENCE=1`, each with an external900-second deadline.


Small discovery/proof selectors (external900-second deadline per command):

```sh
make check RUN='^TestReferencePGType(Shape|Builtins)$' REFERENCE=1
make check RUN='^TestReferencePGType(Families|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferencePGType(Shape|Builtins)$'
make check RUN='^TestReferencePGType(Families|Diagnostics)$'
```

Admission, lifecycle and feature-smoke definitions additionally cover the exact
native eligible bag, absent unsupported families, actual catalog-row links,
transaction rollback, relation/schema/owner edits, physical TEMP retirement,
native Dump/Reset/Restore with receiving identities and retained cursor metadata.
These controls do not serialize pg_type rows or invent a PostgreSQL Reset API.
Committed other-session TEMP rowtype/array identities remain public inventory
until actual owner-backend retirement. The lifetime test reuses the existing
physical-exit barrier: PostgreSQL must leave `pg_stat_activity` after the client
transport closes before asserting both identities absent; native retirement is
synchronous. This does not add a session-visibility filter to `pg_type`.
Both adapters assert the same held OID-input parameter Context,
`unnamed portal parameter $1 = '...'`. Transaction-private TEMP table rowtypes
and their arrays are checked before and after rollback in both modes.
PostgreSQL additionally exercises transactional domain creation; native direct
and actual Prepare attempts preserve the existing explicit-transaction utility
0A000 boundary and leave no domain identity behind.
Installed `query_int` retains its full32 goldens across namespace changes:
PostgreSQL uses `ALTER EXTENSION SET SCHEMA`, while native uses supported
`ALTER SCHEMA` and asserts the unsupported extension-alter boundary without
changing either type row. Common SQL DROP/reinstall removes the old pair,
establishes new independent REGTYPE/REGPROC identities and preserves an unread
pre-removal catalog row.
`TestReferencePGTypeInstalledDomains` independently specifies composition over
the installed type: a domain and nested domain inherit the actual `bqarr_out`
identity, retain immediate bases, use domain_in2597/domain_recv2598/send0, and
own exact canonical companion metadata. Full32 numeric and symbolic REGPROC
expectations run direct, actual Prepare and retained statements. A pinned
physical connection with explicit search_path checks qualification after shared
schema rename/back; native Reset or PostgreSQL extension DROP CASCADE followed
by real reinstall/recreation checks current authorities and unread old metadata.
The retained cursor is opened with the shipped fixture recorder context, so
post-replacement checks cover its actual middleware-owned row and closure as
well as values and saved descriptors; they do not reissue the old query.
Numeric OID inequality and wider array codecs are not requirements.
Oracle-first discovery must precede native-red and production remediation;
after discovery-filter removal, selected dual-mode proof is:

```sh
make check RUN='^TestReferencePGTypeInstalledDomains$'
make check RUN='^TestReferencePGTypeInstalledDomains$' REFERENCE=1
```

Supplemental `TestPGTypeBuiltinFacts` and `TestPGTypeCaptureOwnership` exercise
actual family/capture authority, NULL/tree/ACL payloads, foreign generations,
cancellation, independent backing and retained immutable roots without SQL.
`TestPGTypeDomainComposition` checks the single family override/inheritance seam
with supplied complete facts. `TestPGTypeInstalledDomainAuthority` uses real
engine installation/domain creation and captures to check complete inherited
routines, nested immediate bases, canonical arrays, missing/foreign same-ID base
generations and actual Reset/reinstall with retained old roots.

```sh
make check RUN='^TestPGType(BuiltinFacts|CaptureOwnership)$'
make check RUN='^TestPGType(DomainComposition|InstalledDomainAuthority)$'
make check RUN='^TestReferencePGType(Shape|Builtins|Admission)$'
make check RUN='^TestReferencePGType(Shape|Builtins|Admission)$' REFERENCE=1
make check RUN='^TestReferencePGType(Families|Lifecycle|Diagnostics)$'
make check RUN='^TestReferencePGType(Families|Lifecycle|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferencePGTypeFeatureSmoke$'
make check RUN='^TestReferencePGTypeFeatureSmoke$' REFERENCE=1
```

Smoke expects exact primitive rows/raw metadata and repeated held/empty reads,
42703 diagnostics with valid recovery, ordinary-role PUBLIC SELECT, genuine
enum/table data and reciprocal row/array links, plus native logical replacement
with preserved unread rows/descriptors and actual receiving user identities.
Reset/Restore may reuse numeric OIDs; numeric inequality is not generation
provenance. Held reads must observe Reset absence and restored publication,
with canonical arrays independently resolved by REGTYPE, stored enum-column
types checked through pg_attribute, actual table data and reciprocal links.
Private capture ownership coverage checks fresh source/type authority and
retained old roots without depending on the allocator sequence.
The contract is bounded by deliberate family opt-in, not the wider REGTYPE
inventory: temporal multirange identities remain lookup-only, unsupported
families are not advertised, and visible companion arrays do not widen codecs.
NAME/CHAR casts and virtual mutation/locking retain their independent exclusions.
These definitions and contracts are not a claim of completed verification.


### Boolean call predicates

The four `TestReferenceBooleanCall*` definitions independently cover all four
visibility inquiries qualified/unqualified, bare and composed predicates,
three-valued results and BOOL metadata, direct/fresh/held statements and current
search_path/effective-role refresh. Identities come from qualified reg-object
casts and conversion object-address lookup, never the inquiry under test.
The nonexistent controls are positive identities discovered before dropping a
dedicated `bcp_pred_gone` schema with CASCADE. This preserves a real created enum
control without requiring unsupported standalone enum `DROP TYPE`; surviving
visible, shadowed and offpath objects remain in their own schemas.
Diagnostics retain complete records and Prepare/Execute phases; unknown names
explicitly retain the native unsupported-call boundary beside PostgreSQL42883.
The relation-list witness requires the original SQL unchanged, with both LEFT
JOINs, exact ordered visible rows and NAME/NAME/TEXT/NAME empty-result metadata.
The prerequisite simple-CASE internal `"char"` equality defect is resolved
upstream. Recovery evidence records the unchanged embedded relation-list witness
passing natively; final selected proof, quickcheck and review remain separate
gates, not implied by this behavior description.

Run each selector separately in both modes, with external900-second deadlines.
`REFERENCE=1` uses the approved pinned PostgreSQL18.6 UTF8/C oracle; earlier
18.0 observations remain historical evidence, not newly observed18.6 results.
See [oracle provenance](../fixture/README.md#postgresql-oracle-policy-and-provenance).

```sh
make check RUN='^TestReferenceBooleanCallPredicates$' REFERENCE=1
make check RUN='^TestReferenceBooleanCallPredicates$'
make check RUN='^TestReferenceBooleanCallHeldPaths$' REFERENCE=1
make check RUN='^TestReferenceBooleanCallHeldPaths$'
make check RUN='^TestReferenceBooleanCallDiagnostics$' REFERENCE=1
make check RUN='^TestReferenceBooleanCallDiagnostics$'
make check RUN='^TestReferenceBooleanCallRelationList$' REFERENCE=1
make check RUN='^TestReferenceBooleanCallRelationList$'
```

The last selector is the embedded feature smoke. Its rebuilt-server/actual-psql
counterpart is documented in [sessions Testing](../sessions/README.md#boolean-call-relation-list-smoke).


### pg_am selected proof

The five `pg_am*_test.go` files define nine permanent public scenarios:
`TestReferencePGAMInventory`, `TestReferencePGAMShape`,
`TestReferencePGAMLinkage`, `TestReferencePGAMDiagnostics`,
`TestReferencePGAMPermissions`, `TestReferencePGAMHeldExecution`,
`TestReferencePGAMLifecycle`, `TestReferencePGAMCancellation` and
`TestReferencePGAMFeatureSmoke`. All use the same body in native and pinned
PostgreSQL modes, with independent expectations before comparison.

Released [pg_am.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_am.dat),
[pg_am.h](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_am.h)
and [pg_proc.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_proc.dat)
specify identities; the [manual](https://www.postgresql.org/docs/18/catalog-pg-am.html)
specifies the four fields. The fixture remains PostgreSQL18.0 UTF8/C, while the
manual currently serves18.6; local20devel sources are research only. Oracle-first
discovery observed all nine scenarios passing before production implementation,
then native Inventory/Shape failed on missing pg_am relations.

Native must return the entire four-row inventory heap2/handler3/type`t`,
btree403/330/`i`, hash405/331/`i`, GiST783/332/`i`. PostgreSQL independently
returns those plus gin2742/333, brin3580/335 and spgist4000/334, all type`i`.
Shared supported-subset queries do not conceal extra native rows. Separate
numeric and symbolic handler assertions preserve REGPROC identity. Methods
describe implemented logical execution, not physical storage algorithms or
callable handlers; GIN/SP-GiST/BRIN are not native capabilities.

Direct, freshly prepared and SAME held OID/NAME readers retain all four
descriptors for populated, unknown, false and NULL results. Names/types are
`oid OID`, `amname NAME`, `amhandler REGPROC`, `amtype CHAR` (internal `"char"`).
Native raw cells are int64/string/string/string, ScanTypes the same, and
Nullable(false,true). Pinned pq raw cells are []byte/[]byte/[]byte/string;
ScanTypes are interface{} and Nullable(false,false). Length/DecimalSize are
unavailable. Metadata is checked before iteration and saved across Close.
Relation2601, attributes1–4/types26,19,24,18/C950 NAME, row10015/array10014,
pg_class/pg_tables discovery and absence from pg_views are independently checked.
Public namespace discovery uses pg_namespace names, not an allocator-pinned OID.

Real btree/hash indexes and an INET/range GiST exclusion witness link relam and
opclass method identities; exact23P01 rejection preserves existing data.
Ordinary roles read PUBLIC metadata and receive full42501 records for DML,
false predicates, MERGE, CTE UPDATE and TRUNCATE. Owner-prepared held commands
recheck the execution-time role. Missing columns/invalid OID/overflow assert
complete diagnostics and actual prepare/execute stages. Parameter
errors carry `unnamed portal parameter $1 = '...'` in both modes.
Native superuser mutations/virtual row locking retain explicit0A000 boundaries,
never real-PG superuser catalog writes.

Held readers and unread results survive index/domain/enum DDL; native-only
Dump/Reset/Restore phases retain old rows/metadata, recreate pg_am immediately,
restore real user views/data/indexes, reject failed/canceled staged replay
without effects and emit no catalog INSERTs. PostgreSQL shares the actual
SQL/view/data phases, not an invented native-control equivalent. Precanceled
native direct/Prepare/held APIs preserve ordinary context.Canceled without a
SQLSTATE. `TestPGAMCaptureOwnership` supplements these public witnesses with
package-function descriptor/ACL/type/row-array generation ownership, independent
capture storage and deterministic entry/mid-materialization cancellation.

Run each selected group in both modes, with an external900-second deadline:

```sh
make check RUN='^TestReferencePGAM(Inventory|Shape|Linkage)$'
make check RUN='^TestReferencePGAM(Inventory|Shape|Linkage)$' REFERENCE=1
make check RUN='^TestReferencePGAM(Diagnostics|Permissions)$'
make check RUN='^TestReferencePGAM(Diagnostics|Permissions)$' REFERENCE=1
make check RUN='^TestReferencePGAM(HeldExecution|Lifecycle|Cancellation|FeatureSmoke)$'
make check RUN='^TestReferencePGAM(HeldExecution|Lifecycle|Cancellation|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestPGAMCaptureOwnership$'
make check RUN='^TestReferencePGAggregate(Permissions|Prepared|MutationPreflight)$'
make check RUN='^TestReferencePGAggregate(Permissions|Prepared|MutationPreflight)$' REFERENCE=1
```

The aggregate selector preserves all existing callers of the narrowly generalized
pg_aggregate/pg_am mutation-admission helper. Actual feature smoke composes the
same inventory/typed empty-held reads, index/exclusion conflict/linkage,
effective-role rejection and real control/view/data/retained-row helpers:

```sh
make check RUN='^TestReferencePGAMFeatureSmoke$'
make check RUN='^TestReferencePGAMFeatureSmoke$' REFERENCE=1
```

Expected exit0 with independent native4/PG7 inventory and all assertions above;
native controls are explicitly native-only. Whole-project Docker-free verification
uses `make quickcheck`; selected checks do not replace it or post-merge finalcheck.

### Catalog relations oracle-first contract

`catalog_relations_*_test.go` independently specifies
`pg_get_catalog_foreign_keys()` and `pg_get_userbyid(oid)`. The literal
bag contains all **219** PostgreSQL REL_18_0 links, numeric REGCLASS identities,
ordered TEXT column arrays and both flags. Expectations come from all 64 release
catalog headers and Catalog.pm's implicit/explicit annotation rules, excluding
only the synthetic `encoding` target as genbki.pl does. All ordered column pairs
and multiplicities were separately cross-checked against release `oidjoins.out`;
flags are source-derived, not learned from native output. `is_opt` permits zero
references, not SQL NULL, and `is_array` applies to the last source column.
Unsupported native catalog providers must not filter this bag. Unsorted scans
assert bags, never a tuple-to-ordinality mapping.

The seven shared scenarios cover complete direct/prepared inventory and user-FK
independence; typed empty/expanded/aliased/ordinal/zipped records; exact known,
unknown and NULL NAME values with implicit OID inputs; structured diagnostics
and actual prepare/execute stages; one physical held scalar across role
publication, rollback and effective-role changes; and symbolic view/default
consumers with native same/fresh-driver Dump/Reset/Restore. Roles use their own
queried backend identities, not allocator constants or assumed login names.
Native retained cursors and ordinary Go cancellation are explicit native
contracts; PostgreSQL does not receive invented host-control APIs.

Transactional CREATE TABLE/CREATE ROLE are not prerequisites of these
inquiries. PostgreSQL exercises their actual successful utility rollback;
native independently asserts its existing `0A000` explicit-transaction utility
boundary, rolls back and checks unchanged complete linkage/role-existence/name
results. Both backends also exercise real supported DML and SET ROLE rollback.
Autocommit role drop/recreate still uses the SAME held inquiry and actual new
role OIDs. No adjacent transactional utility capability is introduced.

The initial pinned PostgreSQL discovery is root-recorded; all temporary
discovery filters, switches and logging have been removed. Both available
backends now run unconditionally. The final PostgreSQL lifecycle phase requires
nonempty actual native dump bytes and cannot fall back to source declarations.
`catalogRelationsMetadata` now always asserts the pinned PostgreSQL accessor
observations: BOOL ScanType is bool; NAME, REGCLASS, TEXT[] and RECORD ScanTypes
are interface{}; Nullable is false/unknown, Length is zero/unknown and
DecimalSize is zero,zero/unknown. Native independently requires its public typed
string/bool ScanTypes and conservative unknown nullability/length/decimal
descriptors, not fabricated NOT NULL information from nonnull result data.
Temporary capture logging has been removed.

Diagnostic expectations incorporate the root-recorded pinned observations:
parameter Context redacts the value as `unnamed portal parameter $1 = '...'`,
and the nested generate_series example fails signature resolution with 42883
at character15 before SRF placement analysis. Fixture checks also require
complete values, labels, SQL types, raw codecs, middleware row ownership and
closure. All checks remain root-owned; no check was run by the test author.

Run from the repository root, each with an **external 900-second deadline**:

```sh
make check RUN='^TestReferenceCatalogRelations(Inventory|Shape|Roles)$' REFERENCE=1
make check RUN='^TestReferenceCatalogRelations(Diagnostics|Held|Lifecycle|FeatureSmoke)$' REFERENCE=1
```
Initial PG-only lifecycle discovery exercised independently authored SQL
declarations. Final unfiltered proof additionally requires native lifecycle
controls to produce actual dump SQL which PostgreSQL replays and checks
independently. That final replay is not claimed by the initial discovery.

The root recorded the unguarded native-red gate after pinned discovery and
before production implementation. Selected Docker-free proof uses:

```sh
make check RUN='^TestReferenceCatalogRelations(Inventory|Shape|Roles)$'
make check RUN='^TestReferenceCatalogRelations(Diagnostics|Held|Lifecycle|FeatureSmoke)$'
```

Both command groups in both modes must pass on the completed definitions;
discovery/native-red evidence is not final proof. Internal supplemental units
exercise authentic typed binding, immutable role capture/publication, metadata
ownership, retag/remap/symbolic rendering and lazy cursor EOF/close/cancellation:

```sh
make check RUN='^TestCatalogRelations(Binding|Inventory|Capture|Cursor|Consumers)$'
make check RUN='^Test(CatalogRelations(Capture|Cursor)|ReferenceCatalogRelations(Held|Lifecycle))$' RACE=1
```

The actual feature smoke reuses the complete expected bag, six-field empty
metadata, unknown/NULL scalar lookup, same-held role drop/recreate/rollback and
symbolic view/default replay consumers:

```sh
make check RUN='^TestReferenceCatalogRelationsFeatureSmoke$'
make check RUN='^TestReferenceCatalogRelationsFeatureSmoke$' REFERENCE=1
```

Expected final observations are exact 219-row six-field bags; types
`REGCLASS,_TEXT,REGCLASS,_TEXT,BOOL,BOOL`; raw NAME role spelling and
`unknown (OID=0)`/typed NULL; old dropped role identity becoming unknown while
the recreated actual identity resolves; and preserved dynamic calls after
replacement and real emitted-SQL replay. These are prescribed observations,
not claims of verification. Root owns discovery, native red and all proof.

### pg_parameter_acl selected proof

The six `pg_parameter_acl_*_test.go` files define eight shared scenarios for the
actual sparse catalog, not setting-value approximations. Relation6243 is a
bootstrap-owned, indexed, permanent shared table in `pg_global`, with columns
`oid OID NOT NULL`, `parname TEXT NOT NULL`, nullable `paracl ACLITEM[]`.
Native origins are relation6243/attributes1–3; parname uses C collation and
paracl has one declared dimension. Row/array ownership is asserted through
initialized native descriptors and relational public identities, not portable
allocator numbers or invented backing index/toast relations.

Independently expected ACL elements include bootstrap `sA`, recipient SET/ALTER
SYSTEM, separate grant options, PUBLIC and delegated grantor provenance.
The bootstrap display is independently checked at role10 (postgres native,
stuntgres in the pinned fixture). Both backends use real lc_messages,
application_name and unique dotted custom GRANT targets. SET/SHOW does not create
ACL rows; canonicalization does not make textual predicates case-insensitive.
Updates/no-ops preserve the allocated entry OID; returning to baseline removes
it, and regrant receives a fresh identity. The explicit-empty witness first
grants SET to PUBLIC, then revokes owner and PUBLIC rights: the same entry
becomes nonnull `{}` before restoring owner ALL removes it. An unseeded absent
custom REVOKE is not an empty-ACL witness.

Shape/metadata covers direct/context/noncontext/prepared/held routes and retained
descriptions, including initialized empty catalog sources before grants and after
default-elision or Reset. pq TEXT ScanType is string and TEXT Length is MaxInt64/known;
OID/ACL raw byte carriers and unknown pq nullability remain distinct from the
native contract. OID normalization does not replace independently expected
identity relationships. Diagnostics prescribe complete fields and actual
prepare/execute stages, invalid OID binds/recovery and ordinary pre-cancellation.
Observer reads are unfiltered under PUBLIC SELECT. Native component tests publish
real catalog table/column ACLs for current denial, empty/FALSE/LIMIT0 and retained
descriptions; PostgreSQL additionally executes its real catalog GRANT/REVOKE
counterpart. No native catalog-grant implementation is implied.

Capture units use real grant-owner operations, typed independently specified
ACLITEM masks/principals, immutable old catalog/role roots and a distinguishing
update between statement snapshot and source opening. Cancellation checks
enumeration of unrelated descriptors without partial output. Invalid owner or
foreign role provenance fails rather than formatting invented data. Retained
ACL output survives removal and Reset. Native controls assert Dump rejection
before writer callbacks and unchanged reads, then Reset emptiness with retained
old rows/metadata; there is no positive parameter ACL serialization contract.

`TestReferencePGParameterACLControls` partitions its independent metadata facts
into four serial fixtures: `relation` (pg_class identity and reltype),
`row_array_types` (row/array regtype identities and reciprocal pg_type links),
`discovery` (pg_tables inclusion and pg_views exclusion), and `attributes`
(all three exact pg_attribute rows). Each fixture has the existing 30-second
context from birth through cleanup; it does not renew a live lifecycle's
deadline. Every family keeps the complete grant/read/Dump-rejection/read/Reset
sequence, same held-query Reset emptiness, freshly executed Reset emptiness,
and the original old row and saved metadata retained across that same Reset.
All seven fact queries and their exact expected rows, columns, types and raw
carriers still run directly, fresh-prepared and twice on the same held statement,
both before and after native Reset. PostgreSQL retains the ordinary grant,
complete family facts, revoke and empty-read route in each independent fixture.
The standalone `TestReferencePGParameterACLMetadata` still checks all seven
facts together through its unchanged four execution routes.

Selected workload-remedy proof and control smoke (run each command under an
external 900-second deadline):

```sh
make check RUN='^TestReferencePGParameterACL(Controls|Metadata)$' REFERENCE=1
make check RUN='^TestReferencePGParameterACL(Controls|Metadata)$'
make check RUN='^TestReferencePGParameterACLControls$' RACE=1
```

Expected observations are the original canonical PUBLIC SET ACL and positive
uint32 OID, exact catalog facts unchanged by native Reset, native Dump's ordinary
`stuntgres: dump cannot serialize parameter ACL` error with zero writer calls
and no SQLSTATE, fresh/held empty rows after Reset, and the retained original
OID/name/ACL plus complete saved descriptor. These commands describe required
proof, not an assertion that the new fixture boundaries have been verified.

```sh
make check RUN='^TestReferencePGParameterACL(Shape|Metadata|GrantedEntries|IdentityAndLifecycle|ExplicitEmpty|VisibilityAndDiagnostics|Controls|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferencePGParameterACL(Shape|Metadata|GrantedEntries|IdentityAndLifecycle|ExplicitEmpty|VisibilityAndDiagnostics|Controls|FeatureSmoke)$'
make check RUN='^TestPGParameterACL(ShapeAndOwnership|Capture|Snapshot|Authorization|Cancellation)$' RACE=1
make check RUN='^TestReferencePGParameterACLFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferencePGParameterACLFeatureSmoke$'
```

The feature smoke performs absence -> real SET grant -> exact canonical
name/ACL/nonzero identity -> non-grantee visibility -> held-query ALTER SYSTEM
expansion with stable identity -> real final revoke/default removal. Unique
cluster-shared entries and roles are cleaned with real REVOKE/DROP OWNED and
bootstrap-right restoration, never system-catalog DML. These selected commands
and expected observations are instructions, not claims of passing verification;
whole-project quickcheck and post-integration finalcheck remain separate gates.

### pg_default_acl selected proof

The seven `TestReferencePGDefaultACL` definitions read actual implemented policies,
not inferred object privileges. Relation826 has OID/OID/OID/CHAR/_ACLITEM columns,
all nonnullable, with exact attributes and reciprocal row/array ownership.
Independent expectations cover all six released class codes, global namespace0
versus real scoped OIDs, full owner masks without invented stars, PUBLIC and named
recipients, quoted ACL names, numeric item ordering, nonnull empty `{}`, baseline
deletion and stable-versus-recreated identities. Generated IDs are checked for
positive uint32 range, uniqueness and real role/schema relationships, never
cross-server allocator equality. Relation rows are bags unless explicitly ordered.

Shape checks direct and actual prepared context/plain APIs, held execution and
retained metadata. PostgreSQL OID/ACL arrays use raw bytes, internal CHAR uses
string, and pq nullability is unavailable; native OIDs use int64, CHAR/ACL use
string and all five columns have known nonnull metadata. Visibility is public
inventory even for an unrelated observer without schema USAGE. Schema rename/
ownership and REASSIGN OWNED do not change the original policy creator.
Lifecycle and controls retain actual unread rows across edits/Reset; fresh held
executions observe changes. Diagnostics assert complete English records and
prepare/execute stages, UTF8 positions, invalid OID binds and ordinary cancellation.
PostgreSQL catalog ACL revocation is a real permission witness; native equivalent
publication is an internal component test, not new catalog GRANT SQL.

Native-only phases preserve read-only DML/row-lock boundaries, prewriter ordinary
Dump rejection, failed Restore atomicity and empty policies after Reset/pristine
Restore. The pinned oracle is PostgreSQL18.6 under the shared fixture policy;
no native control API is invented for PostgreSQL.

Run each selector natively and with `REFERENCE=1`, with an external900-second
deadline. Commands prescribe proof, not passing claims:

```sh
make check RUN='^TestReferencePGDefaultACL(Shape|Metadata)$'
make check RUN='^TestReferencePGDefaultACL(Entries|VisibilityAndScope)$'
make check RUN='^TestReferencePGDefaultACL(Lifecycle|Diagnostics|NativeControls)$'
make check RUN='^TestPGDefaultACL(Identity|CaptureOwnership|AuthorityAndCancellation|PermissionPublication)$' RACE=1
```

Root's standalone public-path smoke is temporarily materialized from the OMP
session as `/tmp/stuntgres-pg-default-acl-smoke.go`, then run from the worktree with
`go run /tmp/stuntgres-pg-default-acl-smoke.go` and removed. Expected exit0 emits
actual identities/counts/ACL observations and requires
empty five-column metadata, actual global/scoped grants, exact ACL arrays and
distinct positive IDs, direct/prepared/held freshness, unrelated-role visibility,
stable edits and baseline deletion, detached old rows, Dump rejection and Reset
emptiness. Existing focused lifecycle/control scenarios remain permanent; no
duplicate permanent smoke scenario is added.


### pg_range selected proof

`pg_range_test.go`, `pg_range_linkage_test.go`, `pg_range_diagnostics_test.go`
and `pg_range_lifecycle_test.go` define the seven-column PostgreSQL18 catalog
and complete six-row bag independently from released catalog data. Inventory
checks numeric identities and symbolic REGPROC names (including nonnull zero
rendered `-`), direct/new-prepared/held reads, unordered multiplicities, and
empty/NULL parameter metadata. Shape checks raw native int64/string versus
pq bytes, ScanType/nullability/length/precision, saved metadata after Close,
pg_class, all seven pg_attribute facts, row/array identities and pg_tables.
Both backends supply pg_type reciprocal ownership SQL; private native
`TestPGRangeCaptureOwnership` additionally supplies descriptor ownership,
foreign-generation/shape rejection, independent storage and cancellation.
`TestRangeMetadata` independently checks six exact owner facts and admission,
including preserved lookup-only temporal companions and absent domain rows.

Linkage checks every subtype btree class, symbolic type identity and all nine
REGPROCEDURE signatures, plus actual discrete/continuous six-family storage
and absence of extra rows for domains, columns, companions and arrays. Linked
canonical/subdiff routines are lookup-only natively: left-join witnesses are
NULL there, while PostgreSQL additionally asserts real pg_proc signatures.
Routine-provider joins explicitly cast REGPROC links to OID, preserving the
identity witness without requiring mixed OID/REGPROC comparison operators.
Nonzero temporal companion identities do not enable temporal multirange codecs.
Diagnostics assert complete logical English records and stages; native catalog
mutation/locking rejection preserves a sentinel and the entire bag. PostgreSQL
ordinary-role writes assert ACL denial rather than superuser catalog mutation.
Lifecycle consumes retained unread cursors after DDL and native Dump/Reset/
Restore, checks held/view recovery and exact logical redump. Cancellation is
ordinary context.Canceled natively, not a fabricated server SQLSTATE.

Run each bounded selector real-first, then quick, with external
900-second deadlines; repeat both modes after implementation:

```sh
make check RUN='^TestReferencePGRange(Inventory|Shape|Linkage)$' REFERENCE=1
make check RUN='^TestReferencePGRange(Inventory|Shape|Linkage)$'
make check RUN='^TestReferencePGRange(Diagnostics|Permissions)$' REFERENCE=1
make check RUN='^TestReferencePGRange(Diagnostics|Permissions)$'
make check RUN='^TestReferencePGRange(HeldExecution|Lifecycle|Cancellation)$' REFERENCE=1
make check RUN='^TestReferencePGRange(HeldExecution|Lifecycle|Cancellation)$'
make check RUN='^TestReferencePGTableViewsBuiltins$' REFERENCE=1
make check RUN='^TestReferencePGTableViewsBuiltins$'
make check RUN='^TestReferencePGRangeFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferencePGRangeFeatureSmoke$'
make check RUN='^Test(RangeMetadata|PGRangeCaptureOwnership|PgClassBuiltinIndexMetadata|TableViewSummaryBuiltinIdentity)$'
make check RUN='^TestReferencePGRange(HeldExecution|Lifecycle|Cancellation)$' RACE=1
```

FeatureSmoke composes the same cohesive helpers: full inventory, prepared
empty/NULL metadata, provider links, real range values, complete diagnostics,
native protected mutation/sentinel/held recovery, ordinary-role reads and
native control round trip with retained unread cursor. Expected success is
quiet exit0/package `ok` with these independent assertions. Selected proof
supplements, never substitutes for, whole-project quickcheck.

### pg_operator selected proof

The `TestReferencePGOperator` scenarios use the independent
[released supported inventory](../../docs/design/pg-operator-pg18-supported.tsv)
and [all-family admission/provenance note](../../docs/design/pg-operator-pg18-oracle.md).
All 15 fields are asserted, including numeric implementation/estimator references;
native inventory reads are unfiltered so extra records fail. This does not
relabel the complete 799 REGOPERATOR alias inventory as executable metadata.
The JSONB JSONPath part adds only released4012/4013 with actual two-argument
implementation routines4010/4011 and estimators5040/5041. The existing shared
inventory and reference checks assert all fifteen fields for these rows; no
additional projection columns or text-search execution are implied.
BOX promotion adds only499 and501–505, with false merge/hash flags, exact
commutator/negator relationships and released implementation/estimator identities.
The six independent rows require fresh selected inventory/reference/lifecycle
proof in both modes; BOX's fuzzy scalar comparisons do not enable default
ordering, grouping or indexing. `TestReferencePGTypeAdmission` additionally
checks600/603 and lookup-only companion metadata1017/1020, including raw typelem
versus SQL-array relationships.

Circle admission adds only the19 released circle×circle signatures
1500–1515/1520/2589/2590, independently specified from PostgreSQL18.6 catalog
sources. The same full-field inventory and symbolic-reference scenarios cover
them; no merge/hash flag or default comparison class is implied.
`TestReferencePGTypeAdmission` also checks circle718's reciprocal companion719
metadata in both modes. Companion catalog presence remains independent of
native array-datum admission, which stays lookup-only.

LSEG scalar admission adds OID601 to the native complete pg_type inventory.
`TestReferencePGTypeAdmission` checks its POINT typelem600 and reciprocal
lookup-only companion1018 on both backends alongside BOX/POINT. This does not
grant SQL LSEG arrays or default ordering/grouping authority.

POINT predicate admission adds ten independent15-field rows:
506–510/713/808/809/4161/4162. These cover eight predicates plus deprecated
vertical aliases; SQL `!=` shares713 rather than adding a catalog identity.
The rows retain released routine/estimator identities, commutator/negator
relationships and false merge/hash flags. They are source-grounded expectations,
not records copied from native output. The native complete inventory remains
unfiltered, and PostgreSQL independently validates the admitted rows.

After this shared inventory changes, use these bounded selectors in both modes:

```sh
make check RUN='^TestReferencePGOperator(Inventory|Permissions|References)$' REFERENCE=1
make check RUN='^TestReferencePGOperator(Lifecycle|PolymorphicRelationships|PolymorphicIdentity)$' REFERENCE=1
make check RUN='^TestReferencePGOperator(FeatureSmoke|ResolverAgreement)$' REFERENCE=1
make check RUN='^TestReferenceLsegOperatorCatalog$' REFERENCE=1
# Repeat each selector without REFERENCE=1 for native proof.
make check RUN='^TestPGOperator(CaptureOwnership|PolymorphicRelationships)$'
```

The shared facts reach Inventory, Permissions, Lifecycle, FeatureSmoke,
PolymorphicRelationships, PolymorphicIdentity and References with the expanded
complete expected bag. ResolverAgreement and LsegOperatorCatalog also read the
file but deliberately select unchanged signatures. Native package-function
capture/relationship units consume it independently; no public SQL witness is
moved outside reference.

Polygon admission adds only491 (`polygon ~= polygon`),756 (`point <@ polygon`)
and757 (`polygon @> point`) to the independent15-field operator inventory.
Released PostgreSQL18.6 catalog facts specify their procedures339/1429/1428,
self/reciprocal commutators, zero negators and nonmerge/nonhash flags.
`TestReferencePGTypeAdmission` checks scalar604, zero typelem and reciprocal
companion1027; path remains distinct and polygon arrays remain lookup-only.
The shared-inventory consumers and metadata admission require both selected modes:

```sh
make check RUN='^TestReference(PGOperator(Inventory|Permissions|Lifecycle|FeatureSmoke|PolymorphicRelationships|PolymorphicIdentity|References|ResolverAgreement)|LsegOperatorCatalog|PGTypeAdmission)$'
make check RUN='^TestReference(PGOperator(Inventory|Permissions|Lifecycle|FeatureSmoke|PolymorphicRelationships|PolymorphicIdentity|References|ResolverAgreement)|LsegOperatorCatalog|PGTypeAdmission)$' REFERENCE=1
```

Use external900-second deadlines. These are verification obligations, not a
claim that the extended definitions have passed.

```sh
make check RUN='^TestReferencePGTypeAdmission$'
make check RUN='^TestReferencePGTypeAdmission$' REFERENCE=1
```


Root completed pinned PostgreSQL18.0 discovery and observed genuine native
missing-source failure before production. Temporary discovery filters are gone.
CHAR/REGPROC metadata and English diagnostic contracts are independently fixed;
pq reports an empty DatabaseTypeName for the unmapped concrete multirange result
OID, while native retains INT4MULTIRANGE. Declared anymultirange4537 is unchanged.
Lifecycle retains actual native Rows and saved metadata across failed/canceled/
successful replacement, verifies the user view and sentinel, and replays actual
native dump bytes on PostgreSQL. No fake PostgreSQL control API is supplied.
Private capture ownership/cancellation, standalone polymorphic binding and
actual exclusion-predicate units supplement, not replace, the public scenarios.
The former complete799-candidate provider-partition audit is historical, not
current coverage. `TestReferenceLsegOperatorCatalog` checks the focused consumer
contract: all six released LSEG operator records, symbolic procedures/estimators,
and typed-column evaluations joined to their corresponding real catalog rows.
The four length predicates have zero estimator OIDs; equality/inequality retain
their released estimator links. Merge/hash flags remain false.

Independent LSEG catalog/evaluation expectations passed pinned PostgreSQL oracle
discovery, then exposed the empty native six-row catalog. Discovery filters are
removed. Fresh both-mode proof is required after publication changes.

```sh
make check RUN='^TestReferenceLsegOperatorCatalog$'
make check RUN='^TestReferenceLsegOperatorCatalog$' REFERENCE=1
make check RUN='^TestReferencePGOperator.*$'
make check RUN='^TestReferencePGOperator.*$' REFERENCE=1
```


Range/multirange and BIT/VARBIT witnesses use supported typed-column input and
raw results, not unavailable scalar casts. Genuine enum and builtin range
execution is shared; custom range DDL/execution is PostgreSQL-only with native's
existing0A000 boundary asserted explicitly. Zero REGOPERATOR references display
`0`, unlike zero REGPROC's `-`; nonzero symbolic references use the independent
complete alias evidence.

`PolymorphicRelationships` independently covers mixed SMALLINT[]/INTEGER[]
concat/append/prepend promotion to INTEGER[], fixed anycompatible declarations,
simple-array/enum/range relationship rejection, and nonarray text concatenation
versus real array dispatch. Its newly specified diagnostics receive a temporary
PG-only discovery pass before the filter is removed for native/final proof.
The resolver, polymorphic and relationship scenarios execute uncast nonnull
results directly, through a new Prepare, and twice through the same held
statement, inspecting concrete result metadata on every route.

Run each bounded group in both modes with an external 900-second deadline:

```sh
make check RUN='^TestReferencePGOperator(Shape|Metadata|CatalogLinks|Inventory)$' REFERENCE=1
make check RUN='^TestReferencePGOperator(Shape|Metadata|CatalogLinks|Inventory)$'
make check RUN='^TestReferencePGOperator(ResolverAgreement|PolymorphicIdentity|PolymorphicRelationships|References|Boundaries)$' REFERENCE=1
make check RUN='^TestReferencePGOperator(ResolverAgreement|PolymorphicIdentity|PolymorphicRelationships|References|Boundaries)$'
make check RUN='^TestReferencePGOperator(Diagnostics|Permissions|HeldExecution|Lifecycle)$' REFERENCE=1
make check RUN='^TestReferencePGOperator(Diagnostics|Permissions|HeldExecution|Lifecycle)$'
make check RUN='^Test(PGOperator(CaptureOwnership|ProviderAgreement)|PgClassBuiltinIndexMetadata)$'
make check RUN='^TestReferencePgClassBuiltinIndexHistory$' REFERENCE=1
make check RUN='^TestReferencePgClassBuiltinIndexHistory$'
make check RUN='^TestReferenceRegOperator(Inventory|ValuesMetadata|Signatures|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferenceRegOperator(Inventory|ValuesMetadata|Signatures|Diagnostics)$'
make check RUN='^TestReferencePGOperator(HeldExecution|Lifecycle)$' RACE=1
```

Permanent feature smoke reuses the same scenario entrypoints:

```sh
make check RUN='^TestReferencePGOperatorFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferencePGOperatorFeatureSmoke$'
```

Expected success is quiet exit0/package `ok` with full raw schema, exact mixed
and prefix signatures/execution, polymorphic identity stability, numeric/symbolic
references, unsupported alias-but-no-row boundaries, PUBLIC reads, held hit/miss/
nil, full diagnostics and genuine control/replay observations. These selected
commands supplement whole-project quickcheck and post-integration finalcheck;
documentation of commands is not passing evidence.

### pg_aggregate oracle-first contract

The shared `TestReferencePGAggregate` definitions prescribe all 22 PostgreSQL 18
columns and a separate explicit 144-by-22 released expectation fixture in
`pg_aggregate_fixture_test.go`. Its values come from REL_18_0 catalog data and
header defaults, resolved against released routine/type/operator identities;
they are not generated from native output or imported production records.
Full-record checks numerically cast the nine REGPROC fields, while separate
raw checks retain symbolic REGPROC, zero `-`, OID carriers, and SQL NULL seeds.
PostgreSQL membership queries restrict to the selected 144 implemented builtin
identities; native inventory is unfiltered. Installing scenario-owned intagg
adds exactly one aggregate, with distinct extension-local state/final helpers,
not the builtin kernels' identities. Native still advertises only two callable
intagg members; metadata helper identities do not grant execution.

Selected groups (each invocation needs an external 900-second deadline):

```sh
make check RUN='^TestReferencePGAggregate(Shape|Metadata|CatalogLinks|Inventory|Definitions)$' REFERENCE=1
make check RUN='^TestReferencePGAggregate(Shape|Metadata|CatalogLinks|Inventory|Definitions)$'
make check RUN='^TestReferencePGAggregate(Diagnostics|Permissions|Prepared)$' REFERENCE=1
make check RUN='^TestReferencePGAggregate(Diagnostics|Permissions|Prepared)$'
make check RUN='^TestReferencePGAggregateMutationPreflight$' REFERENCE=1
make check RUN='^TestReferencePGAggregateMutationPreflight$'
make check RUN='^TestReferencePGAggregate(IntaggIdentity|Lifecycle|IdentityPersistence)$' REFERENCE=1
make check RUN='^TestReferencePGAggregate(IntaggIdentity|Lifecycle|IdentityPersistence)$'
make check RUN='^TestReferencePGAggregateFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferencePGAggregateFeatureSmoke$'
```

`Shape`, `Metadata`, and `CatalogLinks` cover empty/populated/direct/fresh/held
descriptors, raw carriers, first-20 NOT NULL versus nullable seeds, C collation
for TEXT, relation2600, ordinal/type vectors, row/array links and table-not-view
discovery. Both native and pinned pq expose the two unbounded TEXT lengths as
MaxInt64/known. `Inventory` and `Definitions` assert exact independent bags, every
field, unsupported overload exclusions, scalar/window/helper negatives and
the count/array/ordered/hypothetical/statistical definitions without pretending
their moving or parallel metadata implies added execution support.

`Diagnostics`, `Permissions`, and `Prepared` exercise exact English diagnostics,
analysis versus execution stages, nil publication on errors, PUBLIC catalog
reads, no-op/real-target DML and existing CTE/TRUNCATE/MERGE routes, current-role
held commands, parameter-frame recovery and native ordinary precancellation.
`MutationPreflight` independently requires nested constant and imported-frame
integer conversion errors before ordinary-role catalog permission denial for
INSERT, UPDATE, DELETE, MERGE and writable UPDATE/DELETE CTEs. Direct, fresh
prepared and superuser-prepared/reader-executed held commands share the same
diagnostics; repeated bad/good/NULL/bad parameter frames must not retain a prior
conversion outcome. Valid frames still deny before a volatile nextval sibling,
with the actual sequence uncalled and sentinel7 unchanged. Completed definitions
exercise both available backends without a discovery filter.
The strict context.Canceled/nil-output precancel contract is not imposed on
race-prone fast pq commands. Reader sentinel access uses real PostgreSQL schema
USAGE or native namespace ownership, without transferring table ownership.
The side-effect sequence receives real USAGE/SELECT through supported creation-time
default privileges in both modes, with the default grant revoked after creation;
the sequence's actual ACL and the sentinel table's bootstrap ownership remain intact.
Identity joins use the admitted comma-FROM/WHERE form with explicit
`pg_proc.oid=aggfnoid::oid` and namespace equalities in both modes. The metadata
REGPROCEDURE witness also casts both operands to OID. These preserve relational
identity assertions without new mixed-alias operators or cast/JOIN grammar.
Installed intagg records have exactly 22 fields; ordinals16–22 are
`0,2281,0,0,0,NULL,NULL`, with no surplus state-space field. Ordinary-role writes
must yield 42501 `permission denied for table pg_aggregate` for DML. TRUNCATE
has its independently observed command-specific 42501 primary:
`permission denied: "pg_aggregate" is a system catalog`. Alias parameter input
errors include the redacted unnamed-portal Context, including overflow frames.
Stored routine aliases/arrays use the existing explicit raw string/bytes helper,
not generic OID conversion or weakened shared fixture checks. Superuser zero-row
PostgreSQL updates/deletes are successful, whereas native virtual-catalog
immutability explicitly remains 0A000; these are not claimed equal boundaries.

`IntaggIdentity`, `Lifecycle`, and `IdentityPersistence` cover real pg_proc joins,
qualified helper identities, owner removal/reinstallation, eager retained Rows
and metadata, held reader recapture, actual native Dump/Reset/Restore,
failure/cancellation atomicity, semantic repeated dump replay, displaced receiving identities,
Driver/opaque-DSN isolation, and actual emitted-stream PostgreSQL replay.
Persisted scalar/domain/array/composite helper aliases, DEFAULT, ordinary view
and opaque-composite materialized consumers must retain symbolic ownership,
including restored INSERT and REFRESH behavior and unrelated numeric aliases.
Native controls are explicit phases, never fictitious PostgreSQL APIs.
Typed helper-call probes retain native's existing `internal` pseudo-type SQL
admission rejection (0A000 `unsupported column type`), rather than inventing a
later function-resolution error or adding pseudo-type execution. Dump ownership
and ordering are proved by actual native Restore and PostgreSQL replay plus
receiving helper-identity, DEFAULT and REFRESH outcomes, not serialization
substring, qualification or recipe-wording assertions. The actual second dump
is restored into a fresh receiving engine whose pinned physical connection owns
actual retained TEMP sequences. Bounded allocations must first demonstrate
REGCLASS OID collisions with every current aggregate/helper identity captured
after the first Restore; ordinary receiver tables are not reservations. Replay
must remap those imported identities while preserving each collider's actual
TEMP OID and nextval state, then pass the full semantic consumer checks.
View cast/deparse formatting is not byte-pinned. Failed/canceled controls retain
the actual aggregate/helper identities and independently expected user results.
The installed-identity test SQL filters independent routine/namespace inputs
inside derived relations before joining, avoiding broad late-WHERE Cartesian
products. All owner/namespace equalities and zero/multiple-row checks remain;
the namespace membership witness still includes every routine, so extra
aggregate membership cannot be hidden by an expected-name filter. This changes
only the fixture SQL shape, not production join or helper-lookup behavior.

Standalone feature smoke is the `FeatureSmoke` selector above. It expects real
count/array/percentile/rank results `3`, `{1,2,3}`, `2`, `2`, their complete
independent definitions, the exact empty schema, installed intagg aggregation
and identity joins, same-held-reader authority and denied mutation with sentinel7
retained, followed by the helper-persistence native control/replay cycle.

The completed shared definitions run both available backends without a discovery
filter. PostgreSQL replay requires a nonempty stream emitted by the scenario's
actual native Dump; missing output fails rather than bypassing replay. Initial
PG-only SQL discovery established expectations before production, but did not
exercise native controls or emitted-stream replay. Final selected dual-mode
proof must exercise those phases too. Root owns formatting and verification;
definition readiness and initial oracle discovery are not production passes.

Source/component proof covers exact capture ownership and type generations,
144 owned released signatures, cancellation/no partial publication, independent
row slots, unchanged observation frontiers, helper validation/allocation
exhaustion, owner retirement, immutable remapping and retained-TEMP ID collisions.
The existing catalog index/table-summary and extension ownership/remap matrices
include the aggregate source and its metadata-only identities.

```sh
make check RUN='^Test(PGAggregate.*|PgClassBuiltinIndexMetadata|TableViewSummaryBuiltinIdentity|IntaggIdentityRemap|IntaggProviderManifest|ExtensionOwnerManifestAdmission)$'
make check RUN='^TestReference(PgClassBuiltinIndexHistory|PGTableViewsBuiltins)$'
make check RUN='^TestReference(PgClassBuiltinIndexHistory|PGTableViewsBuiltins)$' REFERENCE=1
make check RUN='^TestReferenceIntagg(Controls|UnqualifiedRecipes|ValuesDiagnostics|TemporaryControls)$'
make check RUN='^TestReferenceIntagg(Controls|UnqualifiedRecipes|ValuesDiagnostics|TemporaryControls)$' REFERENCE=1
make check RUN='^TestReferenceRegProc(PreparedAuthority|Persistence|MaterializedPersistence)$'
make check RUN='^TestReferenceRegProc(PreparedAuthority|Persistence|MaterializedPersistence)$' REFERENCE=1
make check RUN='^TestReferencePGAggregate(Lifecycle|Prepared)$' RACE=1
```

### Installed uuid-ossp version 4

`TestReferenceUUIDOsspGeneration`, `Identity`, `Diagnostics`, `Consumers`,
`Controls`, `GroupedControls` and `FeatureSmoke` share public direct/held
scenarios across native and PostgreSQL (historical version4 evidence:18.0).
Native installs eight selected members, version1.1: uuid_generate_v4(), the
two time generators and five constants; name-based v3/v5 remain excluded.
Independent canonical decoding checks non-NULL values, version4 and RFC variant bits,
while transport metadata is checked before iteration, including empty results.
Bounded distinctness is a freshness witness, not proof of cryptographic security.
Production uses the shared standard-library crypto/rand kernel.

Identity assertions cover pronargs0/result2950, strict/volatile/parallel-safe
properties, real membership, separate installer/bootstrap-member ownership,
search_path and actual schema authority. Diagnostics assert complete records
for wrong overloads (NULL and bound nil included), UTF8 positions and call
decorations. Defaults/views re-evaluate; immutable-only consumers reject the call.
Native control phases preserve saved values, retained rows, held statements,
qualified provider recipes and independent-driver replay, with atomic failed
and canceled Restore. No PostgreSQL control API is invented.
GroupedControls independently expects count2 beside each valid UUID draw.
Grouped view leaves retain legitimate execution-stage dependence through held
reads, native Restore and independent-driver reconstruction. This does not
admit staged/row-dependent expressions as DEFAULTs or relax UUID provider/type
identity and volatile-kernel validation; separate random draws are never equal
goldens.

`TestReferenceObjectGrantUUIDOsspAuthority` checks the installed member's default
PUBLIC EXECUTE and explicit deny/grant/revoke lifecycle through direct, fresh
prepared and the same held statements. View/table ownership does not license a
different invoker; demanded DEFAULT calls require its current EXECUTE, and a
denied late DEFAULT publishes no earlier row. Positive calls check actual UUID
type/transport and version4/RFC bits, not random goldens. Core UUID routines have
independent ACLs. Native raw default ACL and original carrier/manifest/remap
provenance are private descriptor tests; native pg_proc does not project proacl.
An actual public-API smoke additionally exercised held read/write transitions,
separate view-owner authority and complete mixed-row write atomicity.

Selected proof (900-second external deadline each):

```sh
make check RUN='^TestReference(UUIDOssp(Generation|Identity|Diagnostics|Consumers|Controls|GroupedControls|CopyBinary|FeatureSmoke)|AvailableExtensionsFiltering)$'
make check REFERENCE=1 RUN='^TestReference(UUIDOssp(Generation|Identity|Diagnostics|Consumers|Controls|GroupedControls|CopyBinary|FeatureSmoke)|AvailableExtensionsFiltering)$'
make check RUN='^(TestUUIDOssp(ProviderIdentity|ProviderCollision|ZeroArity|ExpressionIdentity|GroupedExpressionIdentity|Controls|KernelDemand)|TestUUIDGenerationPacking|TestIntaggProviderManifest|TestAvailableExtensions(ShapeAndOwnership|Projection|MetadataValues|Capture|StatementFrontier))$'
make check RUN='^(TestReferenceObjectGrantUUIDOsspAuthority|TestReferenceUUIDOsspIdentity)$'
make check REFERENCE=1 RUN='^(TestReferenceObjectGrantUUIDOsspAuthority|TestReferenceUUIDOsspIdentity)$'
make check RACE=1 RUN='^TestUUIDOssp(RoutineOriginalAuthority|RoutineDefaultACL|ExpressionIdentity|GroupedExpressionIdentity|ProviderIdentity|ProviderCollision)$'
```

Actual feature smoke is `make check RUN='^TestReferenceUUIDOsspFeatureSmoke$'`.
Its assertions exercise alternate-schema installation, real identity, fresh
direct/held/default/view generation, UUID metadata, exact NULL-overload failure
and native control reconstruction. The complementary
[binary smoke](../dml/README.md#installed-uuid-ossp-binary-copy) exports and
reimports the same saved generated UUID, not a different random call.

### Installed uuid-ossp namespace constants

`TestReferenceUUIDOsspNamespaceValues`, `Bindings`, `Identity`, `Diagnostics`,
`Authority`, `Consumers`, `Controls` and `FeatureSmoke` independently specify the
five RFC9562 constants and released PostgreSQL18.6 declarations. Oracle-first
discovery on the pinned18.6 fixture established their complete diagnostics,
computed UUID metadata and immutable EXECUTE behavior before native implementation.

| Function | Canonical UUID |
|---|---|
| uuid_nil | 00000000-0000-0000-0000-000000000000 |
| uuid_ns_dns | 6ba7b810-9dad-11d1-80b4-00c04fd430c8 |
| uuid_ns_url | 6ba7b811-9dad-11d1-80b4-00c04fd430c8 |
| uuid_ns_oid | 6ba7b812-9dad-11d1-80b4-00c04fd430c8 |
| uuid_ns_x500 | 6ba7b814-9dad-11d1-80b4-00c04fd430c8 |

All five are zero-argument UUID IMMUTABLE STRICT PARALLEL SAFE functions, not
builtins or SQL NULL. Tests cover direct/fresh/repeated held calls, raw UUID
metadata before populated and empty iteration, declared storage, textual binds,
NULL propagation, extension/schema/member identities and wrong-overload errors.
Current-invoker EXECUTE remains required for held calls, views and defaults;
revoked late defaults publish no partial writes. Immutable generated/index and
grouped expressions are admitted without converting installed recipes to literals.
Native-only phases preserve values/NULL, retained rows/metadata, held statements,
eight-member owner recipes and independent-driver replay with atomic failed/canceled
Restore. Existing V4 freshness and volatile-consumer rejection remain unchanged.

Run each selected command with a900-second external deadline:

```sh
make check RUN='^TestReferenceUUIDOsspNamespace(Values|Bindings|Identity|Diagnostics|Authority|Consumers|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceUUIDOsspNamespace(Values|Bindings|Identity|Diagnostics|Authority|Consumers|Controls|FeatureSmoke)$'
make check RUN='^TestUUIDOsspNamespace(Constants|ExpressionIdentity|Manifest|Collision)$'
make check RACE=1 RUN='^TestUUIDOsspNamespace(Constants|ExpressionIdentity|Manifest|Collision)$'
```

The permanent shared-body smoke is `TestReferenceUUIDOsspNamespaceFeatureSmoke`.
The catalog part additionally supplies a standalone public-API Go smoke as a
session artifact; materialize it at `/tmp/uuid-namespace-smoke.go` and run
`go run /tmp/uuid-namespace-smoke.go` from the repository root, then remove that
temporary source. Successful smoke is silent and failures exit nonzero.

### Installed uuid-ossp time generators

The shared completed `TestReferenceUUIDOsspTime` family has eight definitions:
`Identity`, `Generation`, `Diagnostics`, `Consumers`, `Authority`, `Controls`,
`Concurrency`, `FeatureSmoke`. They independently decode RFC4122 time/sequence,
version1/variant bits and v1mc multicast/local node bits, bracket demand-time
timestamps and extract the saved UUID's timestamp. Direct/fresh/SAME held calls,
empty/populated UUID metadata, exact diagnostics including NULL-argument
overload failure, defaults/views and ACL revocation retain actual installed
identities. Native controls preserve saved values and qualified consumers,
without serializing or rewinding generation state. Concurrent borrowed-session
and DB-prepared routes require full cardinality, uniqueness and zero errors.
No stable PostgreSQL host node or real MAC is assumed.

Native uses a nontransactional physical-session coupled random fallback
node/time/sequence allocator. V1 keeps its fallback node until sequence wrap;
v1mc draws a fresh multicast/local node per demand. Equal microsecond buckets
provide ten100ns slots then wait cancellation-aware outside the allocator lock.
Rollback increments the14-bit sequence; wrap stages a fresh fallback node.
Entropy failures and cancellation leave state unchanged. There is no host-MAC
discovery, C UUID ABI, uuidd, clock file or durable coordination; uniqueness
across random epochs/sessions/Drivers/processes/restarts is probabilistic.
Private deterministic allocator tests supplement, not replace, public evidence.

Use900-second external deadlines, with the same completed definitions in both modes:

```sh
make check RUN='^TestReferenceUUIDOsspTime(Identity|Generation|Diagnostics|Consumers|Authority|Controls|Concurrency|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceUUIDOsspTime(Identity|Generation|Diagnostics|Consumers|Authority|Controls|Concurrency|FeatureSmoke)$'
make check RUN='^TestUUIDOsspTime(Packing|Clock|Sequence|Entropy|Cancellation|Concurrent|KernelDemand)$'
make check RACE=1 RUN='^(TestUUIDOsspTimeConcurrent|TestReferenceUUIDOsspTimeConcurrency)$'
```

Feature smoke:

```sh
make check RUN='^TestReferenceUUIDOsspTimeFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceUUIDOsspTimeFeatureSmoke$'
```

Expected observations: both installed functions produce fresh valid time UUIDs
through direct/SAME held/default/view calls; saved timestamp extraction agrees
with independent decoding, mc node bits are set, empty metadata stays UUID,
wrong arity yields the complete42883 diagnostic without effects, and native
Dump/Reset/Restore preserves saved values while subsequent calls remain fresh.



### pg_language selected proof

The `TestReferencePGLanguage` definitions specify the complete native
internal-only bag `(12,internal,10,false,false,0,0,2246,NULL)` independently.
The pinned PostgreSQL18.0 oracle separately asserts the released bootstrap
`internal`, `c` and `sql` tuples; native c/sql/plpgsql absence is a capability
boundary, not an assertion about PostgreSQL's installed languages. Handler and
validator IDs remain OID metadata; no language DDL or execution is exercised.

Shape/Metadata/CatalogLinks cover all nine columns, backend raw codecs,
ScanType/nullability/length/decimal metadata before iteration on populated and
empty cursors, owner/attribute/class/table-summary relationships and
direct/new-prepared/actual-held name/OID predicates. Both backends assert
`pg_class.reltype` resolves to the pg_language REGTYPE identity. Both backends
join `pg_type` to assert reciprocal row/array relationships; private native
coverage additionally checks descriptor ownership before and after Reset.
Diagnostics and Permissions
assert complete English error records and stages, native immutable mutation/
row-lock boundaries, preserved rows/sentinel and ordinary-role PUBLIC reads.
HeldExecution adds native ordinary-context cancellation and recovery.
Lifecycle retains a populated native cursor across staged-failure/canceled/
successful Reset/Restore, checks fresh held queries, and replays an actual
native user dump on PostgreSQL with exact sentinel and language properties.
The private capture-ownership test supplements these public witnesses with
typed NULL ownership, foreign-generation rejection and independent row storage.

Run each bounded group in both modes, with an external 900-second deadline:

```sh
make check REFERENCE=1 RUN='^TestReferencePGLanguage(Inventory|Shape|Metadata|CatalogLinks)$'
make check RUN='^TestReferencePGLanguage(Inventory|Shape|Metadata|CatalogLinks)$'
make check REFERENCE=1 RUN='^TestReferencePGLanguage(Diagnostics|Permissions|HeldExecution|Lifecycle)$'
make check RUN='^TestReferencePGLanguage(Diagnostics|Permissions|HeldExecution|Lifecycle)$'
make check RUN='^Test(PGLanguageCaptureOwnership|PgClassBuiltinIndexMetadata|TableViewSummaryBuiltinIdentity)$'
make check REFERENCE=1 RUN='^TestReferencePGTableViewsBuiltins$'
make check RUN='^TestReferencePGTableViewsBuiltins$'
make check RACE=1 RUN='^TestReferencePGLanguage(HeldExecution|Lifecycle)$'
```

Permanent feature smoke reuses these scenario helpers, not a duplicate test body:

```sh
make check REFERENCE=1 RUN='^TestReferencePGLanguageFeatureSmoke$'
make check RUN='^TestReferencePGLanguageFeatureSmoke$'
```

Expected success is quiet exit0/package `ok` with exact internal row and metadata,
held empty-to-populated rebinding, PUBLIC reads, catalog linkage, exact
missing-column diagnostic, native availability/immutability and a real
Dump/Reset/Restore sentinel round trip with a retained populated language row.
PostgreSQL independently checks its supported bootstrap subset and user-dump
replay; it does not inherit native-only control or immutability assertions.
Selected dual-mode proof and smoke supplement whole-project quickcheck.

### Data type privilege descriptors

`TestReferenceDataTypePrivileges` scenarios independently assert the five-column
usage projection, native primitive carriers versus PostgreSQL declared domains,
stable table/composite attribute identities, typmods/arrays, builtin domains,
owner/ACL/type visibility, kinds and physical-session TEMP behavior. Lifecycle
checks cover held execution, cancellation, real native Dump/Reset/Restore,
failed staged replacement and retained rows. Diagnostics assert SQLSTATE and
English records, including the explicit native row-lock capability boundary.
No routine descriptors or neighboring information-schema views are synthesized.

PostgreSQL declared domains are checked through its real `pg_type` join; native
carrier OIDs, positive attribute numbers, nullability and collation are checked
through `pg_attribute`; other information-schema domains remain lookup-only. Composite
attribute edits share both backends; whole-type RENAME TO retains its native
syntax-error boundary and unchanged descriptors while PostgreSQL performs the
rename. Pre-canceled pool queries test the ordinary adapter boundary without
canceling or reusing the scenario's physical connection.
`SchemaRename` checks that the five seeded domains remain visible by stable
namespace identity after renaming information_schema, reports the current schema
name, and leaves unrelated TABLE descriptors readable. Guaranteed cleanup
restores the PostgreSQL bootstrap namespace before fixture teardown.

Run each selected group in both modes with an external 900-second deadline:

```sh
make check RUN='^TestReferenceDataTypePrivileges(Shape|Descriptors|Identity|BuiltinDomains|SchemaRename)$'
make check REFERENCE=1 RUN='^TestReferenceDataTypePrivileges(Shape|Descriptors|Identity|BuiltinDomains|SchemaRename)$'
make check RUN='^TestReferenceDataTypePrivileges(Visibility|TypeVisibility|KindsAndTemp)$'
make check REFERENCE=1 RUN='^TestReferenceDataTypePrivileges(Visibility|TypeVisibility|KindsAndTemp)$'
make check RUN='^TestReferenceDataTypePrivileges(Lifecycle|Controls|Diagnostics|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceDataTypePrivileges(Lifecycle|Controls|Diagnostics|FeatureSmoke)$'
make check RUN='^TestDataTypePrivileges(Capture|Visibility)$'
make check RACE=1 RUN='^TestReferenceDataTypePrivileges(Lifecycle|Controls|KindsAndTemp)$'
```

Feature smoke selects `^TestReferenceDataTypePrivilegesFeatureSmoke$` in both
modes. Expect TABLE/DOMAIN/USER-DEFINED TYPE bags, a column-only reader transition,
stable gaps, genuine builtin domain visibility and an exact missing-column
diagnostic. These commands are required proof instructions, not passing claims.

### pg_opclass selected proof

The `TestReferencePGOpclass` scenarios specify the nine released columns,
OID/NAME/BOOL raw codecs and metadata, fixed class/method/family/input identities,
default and key-storage distinctions, PUBLIC reads, held execution, diagnostics,
index/data agreement and native Dump/Reset/Restore. Expected tuples are independent
of native lookup. Native coverage requires the complete supported63-row seed bag
(31 btree,30 hash and two feature-local GiST classes); PostgreSQL permits others.
GiST inet_ops is explicitly nondefault18.0; range_ops admits canonical anyrange.
Other array/record/range classes remain unadvertised unless actually executable.

The oracle-first checkpoint confirmed the released identities using the existing
pinned PostgreSQL 18.0 UTF8/C fixture before production changes.
`JSONBIdentity/admission` creates a real JSONB hash index, checks equality,
prepared fresh parameters and INSERT/UPDATE/DELETE before reading pg_opclass.
The private supplemental `TestPGOpclassJSONBHashIdentity` checks the existing
root, actual candidate probe and exact stored class identity without new APIs.
The native identity must be hash10089, not inherited btree10088.

Class DDL now makes current descriptors the sole live metadata/default/name
authority. Custom classes and seed rename/schema/owner deltas supplement the
historical seed proof rather than replacing it. Run the seven shared-body
`TestReferenceOperatorClass` definitions in
[`reference/indexes`](../indexes/README.md#operator-class-definitions) in both
modes; their discovery is grounded on pinned PostgreSQL18.6. In particular,
FeatureSmoke proves real custom family/class/index creation and maintained
INSERT, and Roundtrip replays actual native-emitted SQL on PostgreSQL.


Run each bounded group in both modes with an external 900-second deadline:

```sh
make check REFERENCE=1 RUN='^TestReferencePGOpclass(Inventory|Shape|Metadata|JSONBIdentity)$'
make check RUN='^TestReferencePGOpclass(Inventory|Shape|Metadata|JSONBIdentity)$'
make check REFERENCE=1 RUN='^TestReferencePGOpclass(IndexSemantics|Diagnostics)$'
make check RUN='^TestReferencePGOpclass(IndexSemantics|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferencePGOpclass(HeldExecution|Lifecycle|Cancellation|FeatureSmoke)$'
make check RUN='^TestReferencePGOpclass(HeldExecution|Lifecycle|Cancellation|FeatureSmoke)$'
make check RUN='^TestPGOpclassJSONBHashIdentity$'
make check RUN='^Test(IndexClassMetadata(Inventory|Providers)|PGOpclass(CaptureOwnership|IndexIdentity)|HashIndexClassProviders)$'
make check RUN='^TestReferenceIndexMaintenanceDecoratedMetadataOracle$'
make check REFERENCE=1 RUN='^TestReferenceIndexMaintenanceDecoratedMetadataOracle$'
make check RACE=1 RUN='^TestReferencePGOpclass(HeldExecution|Lifecycle|Cancellation)$'
```

Standalone permanent feature smoke:

```sh
make check REFERENCE=1 RUN='^TestReferencePGOpclassFeatureSmoke$'
make check RUN='^TestReferencePGOpclassFeatureSmoke$'
```

Expected success is quiet exit0 after exact INT4 btree/hash tuples and nine-column
metadata; default/nondefault VARCHAR, pattern and NAME keytype distinctions;
direct/held index creation, method/deparse agreement and maintained data;
complete missing-class diagnostics with no residue; native array exclusion versus
real PostgreSQL admission; and a persistent catalog view/indexed-data native
Dump/Reset/Restore roundtrip retaining old rows and refreshing held reads.
PostgreSQL lifecycle SQL asserts the independently selected supported subset of
its broader catalog. Native controls have no invented PostgreSQL API equivalent.
Smoke supplements, not replaces, selected dual-mode proof and whole quickcheck.

### Object ACL inquiry contract

The `TestReferenceObjectACL...` scenarios independently specify the single
PostgreSQL 18 signature `pg_get_acl(oid, oid, integer) -> aclitem[]`, including
formal names `classid`, `objid`, `objsubid`. They retrieve stored ACLs, not
effective permissions or synthesized defaults. Column addresses use `pg_class`
1259 and stable attribute numbers; column grants do not change the table ACL or
other columns. NULL ACL and explicit empty `{}` are distinct.
Owner/grantor labels use independently configured fixture identities: native
`postgres`, pinned PostgreSQL `stuntgres` (`fixture/lifecycle.go` username).
For example, the single PUBLIC column grant is `{=r/postgres}` natively and
`{=r/stuntgres}` on the oracle; no actual result is normalized to hide this.

Selected tests:

- `TestReferenceObjectACLSignatures`: direct/newly prepared calls, parameter
  inference, qualified/quoted/named notation, scalar composition, grouping,
  joins and stored views.
- `TestReferenceObjectACLNulls`: each strict NULL slot, inferred nil parameters,
  typed subscripting/empty expansion and demanded argument errors.
- `TestReferenceObjectACLValues`: exact column SELECT expansion, grant options,
  revokes, table/column separation, unrelated ACL preservation and explicit empty.
- `TestReferenceObjectACLAddresses`: missing/ACL-less classes, dropped/system
  attributes, signed AttrNumber wrapping, nonrelation subids and sequence ACLs.
  Both backends exercise real sequence GRANT USAGE and independently specified
  owner `rwU` and PUBLIC `U` ACLITEM rows through direct/prepared getter reads.
- `TestReferenceObjectACLTypes`: fresh domain/enum/composite/row/array raw NULLs,
  custom type creation defaults and subsequent baseline restoration.
- `TestReferenceObjectACLDatabase`: independently asserted raw NULL for the
  default database ACL in both modes; catalog consistency supplements, never
  supplies, that expectation. Effective owner/PUBLIC rights remain separate.
- `TestReferenceObjectACLDiagnostics`: complete error fields, direct/prepare/
  execute stages, nil outputs, named labels, scalar decorations, input ranges,
  unknown classes and a multibyte diagnostic position.
- `TestReferenceObjectACLMetadata`: raw array/scalar values, empty/NULL/explicit
  empty results and descriptors before iteration, including backend-specific
  ScanType/nullability and absence of length/precision.
  Native direct inquiry arrays are known nullable; expanded unnest ACLITEM
  scalars preserve the existing unknown computed-expression nullability.
- `TestReferenceObjectACLHeldExecution`: SAME prepared calls across grants,
  revokes, role/column rename and dropped-column gaps, raw inquiry without table
  SELECT, and retained rows/metadata across mutation.
  The observer receives real schema USAGE on both backends without transferring
  either schema or table ownership, and still receives exact 42501 for ordinary
  table SELECT.
  Role RENAME is a PostgreSQL-positive/native-exact-0A000 producer boundary:
  native retains the same reader identity/spelling and both modes preserve
  preexisting Rows, fresh held grant/revoke and column identity transitions.
- `TestReferenceObjectACLLifecycle`: actual returned ACL array data and stored
  regclass inquiry recipes, supported native Reset/Dump/Restore, retained rows
  and destination role-identity remapping.
- `TestReferenceObjectACLControls`: unchanged explicit authorization ACL Dump
  rejection before any writer call and exact atomic Restore-GRANT rejection.
- `TestReferenceObjectACLCancellation`: native direct/held precancel, pinned
  observed server cancellation using the existing fixture, and SAME-owner/
  preheld inquiry continuation.
- `TestReferenceObjectACLFeatureSmoke`: standalone NULL -> column grant ->
  one-row expansion -> revoke witness, untouched table/column, typed ACL-less/
  NULL calls, returned data/recipe native roundtrip and atomic control rejection.

The database difference is intentional; see [native database catalog
policy](../../docs/DIALECT.md). Native lifecycle does not persist arbitrary
authorization grants. The lifecycle test stores function-produced ACLITEM[]
**data**, removes the source column grant, then serializes data and recipes.
It does not whitelist GRANT replay or claim persisted authorization.

Oracle-first selected groups (external deadline 900 seconds per invocation):

```sh
make check REFERENCE=1 RUN='^TestReferenceObjectACL(Signatures|Nulls|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceObjectACL(Values|Addresses|Types|Database)$'
make check REFERENCE=1 RUN='^TestReferenceObjectACL(Metadata|HeldExecution)$'
make check REFERENCE=1 RUN='^TestReferenceObjectACL(Lifecycle|Controls|Cancellation)$'
make check REFERENCE=1 RUN='^TestReferenceObjectACLFeatureSmoke$'
```

Repeat each selector without `REFERENCE=1` after oracle discovery, with the same
completed definitions and no temporary PG-only selection. Before integration,
both selected modes must pass. Selected race proof:

```sh
make check RACE=1 RUN='^TestReferenceObjectACL(HeldExecution|Cancellation|Controls)$'
```

Expectations originate in the PostgreSQL 18 release contract and the pinned
18.0 oracle-discovery runs. They include exact diagnostics/stages, named/scalar
decorations, signed-int16 subid wrapping, raw type ACLs, last-column-revoke NULL,
VARIADIC admission and pq metadata. They are not learned from native output.
Both unfiltered modes must be rerun after implementation changes.

#### Standalone public API smoke

Besides the permanent reference smoke, save the following program as `main.go`
in a fresh temporary directory outside the checkout. From the repository root
run `go run /absolute/temporary/directory/main.go` with an external 900-second
deadline, then remove that temporary directory. Expected output is exactly
`object ACL public smoke passed`; any contract mismatch panics/exits nonzero.
The program uses only the public driver, SQL and native-control APIs.

```go
package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"stuntgres"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connector, err := stuntgres.NewDriver().OpenConnector("object-acl-public-smoke")
	must(err)
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(q string) { _, err := db.ExecContext(ctx, q); must(err) }
	check := func(q, want string, valid bool) {
		var got sql.NullString
		must(db.QueryRowContext(ctx, q).Scan(&got))
		if got.Valid != valid || valid && got.String != want {
			panic(fmt.Sprintf("%s: got %#v want %q/%v", q, got, want, valid))
		}
	}
	exec("CREATE TABLE acl_smoke(a int,b int)")
	exec("INSERT INTO acl_smoke VALUES (7,9)")
	exec("CREATE TABLE acl_saved(acl aclitem[])")
	exec("CREATE VIEW acl_recipe AS SELECT pg_get_acl(1259,'acl_smoke'::regclass,1) AS acl")
	var oid int64
	must(db.QueryRowContext(ctx, "SELECT 'acl_smoke'::regclass::oid").Scan(&oid))
	stmt, err := db.PrepareContext(ctx, "SELECT pg_get_acl($1,$2,$3)")
	must(err)
	defer stmt.Close()
	held := func(want string, valid bool) {
		var got sql.NullString
		must(stmt.QueryRowContext(ctx, int64(1259), oid, int64(1)).Scan(&got))
		if got.Valid != valid || valid && got.String != want {
			panic(fmt.Sprintf("held ACL %#v want %q/%v", got, want, valid))
		}
	}
	held("", false)
	exec("GRANT SELECT(a) ON acl_smoke TO PUBLIC")
	held("{=r/postgres}", true)
	check("SELECT unnest(pg_get_acl(1259,'acl_smoke'::regclass,1))", "=r/postgres", true)
	check("SELECT pg_get_acl(1259,'acl_smoke'::regclass,0)", "", false)
	check("SELECT pg_get_acl(1259,'acl_smoke'::regclass,2)", "", false)
	exec("INSERT INTO acl_saved SELECT pg_get_acl(1259,'acl_smoke'::regclass,1)")
	exec("REVOKE SELECT(a) ON acl_smoke FROM PUBLIC")
	held("", false)
	must(stmt.Close())
	var dump bytes.Buffer
	must(stuntgres.Dump(ctx, db, &dump))
	must(stuntgres.Reset(ctx, db))
	must(stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())))
	check("SELECT acl FROM acl_saved", "{=r/postgres}", true)
	check("SELECT acl FROM acl_recipe", "", false)
	exec("GRANT SELECT(a) ON acl_smoke TO PUBLIC")
	var rejected bytes.Buffer
	err = stuntgres.Dump(ctx, db, &rejected)
	if err == nil || err.Error() != "stuntgres: dump cannot serialize explicit relation or column ACL" || rejected.Len() != 0 {
		panic(fmt.Sprintf("Dump boundary: %v/%q", err, rejected.String()))
	}
	err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE acl_replacement(a int); GRANT SELECT ON acl_replacement TO PUBLIC"))
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "0A000" {
		panic(fmt.Sprintf("Restore boundary: %v", err))
	}
	check("SELECT pg_get_acl(1259,'acl_smoke'::regclass,1)", "{=r/postgres}", true)
	check("SELECT a::text FROM acl_smoke", "7", true)
	check("SELECT count(*)::text FROM pg_class WHERE relname='acl_replacement' AND relnamespace=(SELECT oid FROM pg_namespace WHERE nspname='public')", "0", true)
	fmt.Println("object ACL public smoke passed")
}
```

### pg_db_role_setting selected proof

`TestReferencePGDBRoleSetting` independently specifies relation2964 and its
exact three columns: NOT NULL OID `setdatabase` and `setrole`, nullable TEXT[]
`setconfig`, attribute origins1–3, typmods-1 and array collation C950.
The catalog is an indexed, cluster-shared table in `pg_global`, not a view,
with linked row/array type identities and no fourth row `oid` column.
Direct Context/plain calls, newly prepared calls and repeated held executions
check exact empty metadata and OID0/4294967295/NULL binds; retained ColumnTypes
remain inspectable after EOF/Close. ScanTypes/nullability intentionally distinguish
native adapters from pq, rather than pretending pq exposes native-only metadata.
Shared public identity witnesses compare `pg_class.reltype` with the resolved
row REGTYPE, require positive distinct row/array identities, and equate bracket
array lookup with the initialized `_pg_db_role_setting` array name. They prescribe
relationships, not PostgreSQL's allocated OID sequence.
Exact `pg_attribute` rows now run on both backends through the existing native
attribute catalog. Both backends also assert the genuine pg_type reciprocal
row/array relationships.
Native component coverage independently asserts the exact descriptor attributes,
row/array owner and owning-relation links, associated array and element reciprocity.
Public native Rows metadata remains checked exactly in the shared shape scenario.
This distinction does not narrow the new readable `pg_db_role_setting` catalog.


There is no native persistent scoped-settings owner. Empty results are the
current real projection, not synthesized session defaults. Registered and
connection defaults, SET/custom placeholders, SET LOCAL, commit/rollback/savepoint
changes and ordinary-role switching must leave it empty on both physical owners.
ALTER ROLE/DATABASE SET/RESET population, nonempty scope keys/ordered stored arrays
and new-login application belong to future settings/command owners and are not
claimed by these tests. Future rows must represent actual stored defaults, never
effective SHOW values or a filter to only the current role/database.

OID parameter-input failures have the same full unnamed-portal Context on
both adapters: nonempty input is redacted as `'...'`, empty input as `''`.
Row-type relationship queries explicitly convert REGTYPE to OID before
comparison; they do not require an additional mixed-type operator.

Full diagnostics cover missing columns/relation, literal and parameter OID errors,
analysis before empty execution and successful held retries. PostgreSQL-only
catalog ACL revocation asserts42501 even for an empty source; native component
coverage publishes actual relation ACLs through CatalogStage. Both witnesses
grant SELECT on only stable attribute1 (`setdatabase`) with table SELECT absent:
that typed empty projection succeeds, another column and star fail42501, and
the same held column analysis observes grant/revoke/regrant. PostgreSQL catalog
column and table ACLs are restored on success and failure cleanup.
Public native catalog ACL SQL is not added.
Pre-canceled QueryContext, PrepareContext and held QueryContext run in the shared
body on both backends, require ordinary context.Canceled with no SQLSTATE or
published Rows/Stmt, and then reuse the same held statement successfully.
Native-only mutation/locking guards and acquired-Rows cancellation remain
explicitly distinct from PostgreSQL catalog DML/server cancellation.
Reset/Dump/Restore witnesses check failed/canceled replacement atomicity,
sentinel18 preservation, held recapture, retained empty metadata, deterministic
round trips and destination attachment isolation. No backing index/TOAST
relations or scoped-default serialization are fabricated.

```sh
make check RUN='^TestReference(PGDBRoleSetting(Shape|Metadata|SessionVisibility|Diagnostics|Controls|Smoke)|PGTableViewsBuiltins)$' REFERENCE=1
make check RUN='^TestReference(PGDBRoleSetting(Shape|Metadata|SessionVisibility|Diagnostics|Controls|Smoke)|PGTableViewsBuiltins)$'
make check RUN='^TestPGDBRoleSetting(ShapeAndOwnership|Capture|SessionDefaultsExcluded|Authorization)$'
# Permanent feature smoke, reusing the full scenario bodies:
make check RUN='^TestReferencePGDBRoleSettingSmoke$'
make check RUN='^TestReferencePGDBRoleSettingSmoke$' REFERENCE=1
```

Smoke expects exact typed empty reads/count0, shared table discovery, repeated
held execution, session/ordinary-reader exclusion, and native replacement
preserving catalog metadata and sentinel atomicity. Selected native and pinned
real proof are both required; smoke does not replace whole-project quickcheck.

### pg_collation selected proof

`TestReferencePGCollation` specifies the complete PostgreSQL18 twelve-column
metadata contract, including populated and empty direct/prepared/held results,
Context/plain routes, raw carriers, retained ColumnType descriptors and native
known versus pq unavailable nullability. The PG-only `pg_attribute` witness and
private `TestPGCollationShapeAndOwnership` independently require relation3456,
attributes1–12, the release type vector and C950 on NAME/five TEXT attributes.
The implicit catalog-C versus user-POSIX comparison rejects42P22 with its complete
English diagnostic; explicit POSIX annotation succeeds. This observes catalog
metadata through existing comparison semantics, not a new comparison provider.

Seven selected fixed OIDs are asserted without claiming whole-host inventory
parity. Provider/encoding and libc categories versus provider locale are exact.
The pinned PostgreSQL18.0 fixture initializes `unicode.collversion` to `153.120`;
native explicitly reports unavailable ICU version as NULL instead of inventing
provider facts. Configured empty recorded versions remain nonnull empty strings.
Fresh held reads cover CREATE, rename, namespace/owner changes and DROP;
already returned native rows retain old values. PUBLIC catalog reads include
another owner's off-path collation while name-discovery visibility remains false.
Generated identities use positive distinct identities and actual namespace/owner
relationships, not allocator goldens.

Diagnostics cover missing columns/relation, malformed/out-of-range OIDs,
analysis before an empty source, held malformed parameter failure and successful
reuse, and native row-lock exclusions on populated/empty sources. Both adapters
report the same unnamed-portal OID parameter Context. Existing
`TestReferenceCollationAuthorization` supplies the readonly mutation witness.
Private capture units additionally assert actual descriptor membership, typed
NULL/empty distinctions, immutable old captures, unchanged catalog/OID/revision
frontiers and cancellation without partial publication.

```sh
make check REFERENCE=1 RUN='^TestReferencePGCollation(Shape|Seeds|AttributeCollation|FreshMetadata|PublicRead|Diagnostics|FeatureSmoke)$'
make check RUN='^TestReferencePGCollation(Shape|Seeds|AttributeCollation|FreshMetadata|PublicRead|Diagnostics|FeatureSmoke)$'
make check RUN='^TestPGCollation(ShapeAndOwnership|Capture)$'
make check RACE=1 RUN='^TestPGCollation(ShapeAndOwnership|Capture)$'
# Permanent feature smoke:
make check RUN='^TestReferencePGCollationFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGCollationFeatureSmoke$'
```

Smoke expects exact full `pg_c_utf8` metadata, fresh held lifecycle results,
immutable native captured rows, implicit-comparison42P22, an explicit POSIX true
control, and exact versus unavailable Unicode recorded versions. Locale
population, ICU execution and arbitrary libc semantics remain outside this part.

### pg_attribute selected proof

The shared `TestReferencePGAttribute` definitions independently assert all 25
released fields, native/pq raw metadata, canonical nullable ANYARRAY transport,
relation-local identity, exact builtin/type/system facts, syntactic rank, flags,
column ACLs, dropped history and current DDL publication. Direct/plain, fresh
prepared and SAME held readers use the existing fixture and middleware.
Transactions/temporary cases retain explicit owner/observer boundaries; Controls
exercises real native Dump/Reset/Restore and actual emitted SQL replay on PostgreSQL.
TypedControls additionally preserves altered typed rank3 beside later-created
rank0, using ordinary CREATE followed by the existing ALTER TABLE OF attachment.
Native materialized ADD fills differ explicitly from PostgreSQL fast-default
physical history, and logical dumps do not replay dropped-column tombstones.

CopiedRoutes pins partition rank0, initial typed rank0, and explicit typed
ALTER/ADD CASCADE ranks2/3. The merged explicit inherited declaration is also
oracle-pinned rank0. Unconstrained and NOT NULL domains share the ordinary metadata
matrix: a domain's NOT NULL does not set its column's `attnotnull`, while an
explicit column NOT NULL does.
RepresentedBoundary independently checks the existing primitive native
table_privileges schema against PostgreSQL's actual domain-backed view, without
fabricating domain execution or relabeling native descriptor types.
Committed TEMP descriptors are catalog-visible across sessions in both backends;
peer execution by the TEMP name remains forbidden. Controls separately asserts
old literal-held catalog fencing, numeric-parameter held refresh and immutable
eager captures. SQL REVOKE empties column ACLs to NULL without discarding the
private explicit-empty `{}` representation.
Array btree keys and partitioned index creation retain their exact native
`42704`/`0A000` admission boundaries and no-residue checks; full PostgreSQL rows
remain asserted, alongside shared scalar-key/array-INCLUDE metadata. Existing
NAME casts and mixed OID/regclass comparisons remain explicit exclusions, not
scope expansion; role/group/user and JSONPath migrations also check admitted
raw-NAME/numeric-OID inquiries in both modes.
ConstraintAttributes independently checks all 28 represented constraint fields,
including seven nullable array fields plus nullable `conbin`.
ControlsPartitionRank distinguishes minimal attached rank3 from copied rank0.
ControlsRankHistory and RankRepairSQL preserve inherited/merged ranks, local ADD
merges, nested descendants, source2/root3 divergence and independently aged zero
siblings through two Restore/redump cycles and actual emitted PostgreSQL SQL.
RankRepairGraphs covers multiple columns/parents and a nonbootstrap owner with
unchanged implicit ACLs. Repair SQL runs after `SET SESSION AUTHORIZATION DEFAULT`;
real PostgreSQL replay requires superuser catalog-update authority.
Native public UPDATE remains exactly forbidden. Strict malformed repair AST,
late failure after a valid repair, missing/dead targets, cancellation and retained
eager captures supplement full metadata/default/data/relationship checks.

The shape follows the [PostgreSQL 18 catalog contract](https://www.postgresql.org/docs/18/catalog-pg-attribute.html).
Run each public definition in both modes after affected changes:

```sh
make check RUN='^TestReferencePGAttribute(Shape|Identity|Builtins|Types|Declarations|DeclarationsCopiedRoutes)$'
make check REFERENCE=1 RUN='^TestReferencePGAttribute(Shape|Identity|Builtins|Types|Declarations|DeclarationsCopiedRoutes)$'
make check RUN='^TestReferencePGAttribute(Flags|ACL|System|Kinds|Dropped)$'
make check REFERENCE=1 RUN='^TestReferencePGAttribute(Flags|ACL|System|Kinds|Dropped)$'
make check RUN='^TestReferencePGAttribute(Lifecycle|Transactions|Temporary|Diagnostics|Controls|Smoke)$'
make check REFERENCE=1 RUN='^TestReferencePGAttribute(Lifecycle|Transactions|Temporary|Diagnostics|Controls|Smoke)$'
make check RUN='^TestReferencePGAttribute(RepresentedBoundary|TypedControls)$'
make check REFERENCE=1 RUN='^TestReferencePGAttribute(RepresentedBoundary|TypedControls)$'
make check RUN='^TestReferencePG(ExtensionShape|EnumShape|RolesAttributeCatalog|GroupAttributeCatalog|UserAttributeCatalog)$'
make check REFERENCE=1 RUN='^TestReferencePG(ExtensionShape|EnumShape|RolesAttributeCatalog|GroupAttributeCatalog|UserAttributeCatalog)$'
make check RUN='^TestReference(AvailableExtensionsMetadata|ExtensionMemberNullability|PGRolesOracleCatalog|PGGroupOracleCatalog|PGUserOracleCatalog)$'
make check REFERENCE=1 RUN='^TestReference(AvailableExtensionsMetadata|ExtensionMemberNullability|PGRolesOracleCatalog|PGGroupOracleCatalog|PGUserOracleCatalog)$'
make check RUN='^(TestReferencePgClassBuiltinIndexHistory|TestReferencePGTableViewsBuiltins)$'
make check REFERENCE=1 RUN='^(TestReferencePgClassBuiltinIndexHistory|TestReferencePGTableViewsBuiltins)$'
make check RUN='^TestReferencePGAttribute(ConstraintAttributes|ControlsRankHistory|ControlsPartitionRank|RankRepairSQL|RankRepairGraphs)$'
make check REFERENCE=1 RUN='^TestReferencePGAttribute(ConstraintAttributes|ControlsRankHistory|ControlsPartitionRank|RankRepairSQL|RankRepairGraphs)$'
make check RUN='^TestReferenceJSONPath(Representation|Composition)$'
make check REFERENCE=1 RUN='^TestReferenceJSONPath(Representation|Composition)$'
make check RUN='^TestPGAttribute(Shape|Capture|Publication|DeclaredRank|DroppedHistory|Carrier)$'
make check RACE=1 RUN='^TestPGAttribute(Capture|Publication|DroppedHistory)$'
make check RUN='^TestAttribute(TypeFacts|CatalogNullCarrier)$'
make check RUN='^(TestPgClassBuiltinIndexMetadata|TestTableViewSummaryBuiltinIdentity)$'
make check RUN='^TestObject(Grant|Revoke)'
make check RUN='^TestAttributeRankRestore(Syntax|Publication)$'
make check RACE=1 RUN='^TestAttributeRankRestorePublication$'
# Permanent feature smoke:
make check RUN='^TestReferencePGAttributeSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGAttributeSmoke$'
```

Attribute assertions formerly guarded as PostgreSQL-only are now shared in the
owning extension/enum/available-extension tests and the named role/group/user
AttributeCatalog definitions. Genuine pg_type row/array observations are shared;
unrelated feature-specific boundaries remain. Private units supplement SQL with capture, descriptor,
mutation, publication, type-fact and adapter-export functions, including foreign
authority rejection, checked INT2 narrowing and immutable scalar tombstones.

### pg_namespace selected proof

The ten shared `TestReferencePGNamespace` families cover exact shape/raw
metadata, pristine seeds, user identity, visibility, ACLs, TEMP pair lifecycle,
diagnostics, native controls and feature smoke. Native OID/NAME/ACL carriers
are int64/string/string; pq uses bytes for these catalog types. Expectations
are independently specified, including exact stored ACL array order.
Visibility additionally proves the shared public-schema ownership fixture restores
actual prior owner and caller identity after ordinary return or panic; the package
component counterpart is `TestPGNamespaceFixtureOwnership`. Neither fixture changes
pristine seeds or globally grants schema CREATE.
The `Controls/nominal_replay` phase independently creates public domains/enums/composites
under scoped actual ownership, restores pristine namespace authority before any Dump,
and preserves complete native streams, owner-only domain/composite DROP authority
and stored values across genuine Reset/Restore. PostgreSQL also drops the owned
enum directly; native pins the existing enum DROP TYPE0A000/Position1 boundary
through direct/prepared calls, proves no effect and uses supported exact-owner
DROP OWNED cleanup. The shared ordinary value/schema-denial contract runs on both
backends; native control APIs have no fictitious PostgreSQL copy.
`TestNominalTypeRestorePublicNamespaceAuthority` and `DestinationFence` supplement
that surface with real package-level stream reconstruction and forged/ordinary
authenticator rejection. `TestPGNamespaceTemporaryViewCreationAuthority` pins first
TEMP view staging/execution under an ordinary creator; physical cleanup retains the
pair, not its children or namespace-scoped default directives.
`TestReferencePGNamespaceTemporarySuperuserAuthority` distinguishes captured true
bootstrap superusers from ordinary SET ROLE actors, live qualified REGCLASS lookup
from own-session creation, and retired-child42P01 from ordinary schema42501.
Its direct/prepared shared body retains exact pair metadata and zero-child facts,
foreign/retired CREATE42P16 and new-session nonadoption; it does not scan another
backend's physical TEMP table. `TestPGNamespaceTemporarySuperuserAuthority` additionally
proves immutable captured actors across live role switches and unchanged own-TEMP
authority. Existing bootstrap retirement readers use common42P01, not a native
database-owner explanation or diagnostic override.
Native physical foreign-TEMP storage remains unavailable after valid qualified
namespace lookup. `TestPGNamespaceForeignTemporaryStorageCapture`, `Execution`
and `Targets` exercise actual roots, direct/ONLY and recursive stored-view denial,
metadata-only/preseeded-reference admission, persistent/own inheritance and no-effect
DML/COPY/TRUNCATE. Full0A000 is an execution boundary, not a Prepare/schema error.
`TestReferenceTemporaryTableDiagnostics` preserves the independently observed PG
small-peer empty cursor while asserting that honest native storage exclusion.
The shared `TestReferencePGNamespaceViewStoragePrecedence` and
`TestReferencePGNamespaceCopyStoragePrecedence` (in `reference/dml`) preserve
source/table/schema42501 and COPY read-only25006 ahead of physical admission.
Actual revocation, nested owner/invoker views, paired snapshots, storage roots,
TEMP footprint and denied stream I/O are supplemented by
`TestPGNamespaceViewStoragePrecedence` and `TestPGNamespaceCopyStoragePrecedence`.

Shared public relation/namespace identities and existing REGTYPE lookup prove
the registered source relationship. Both backends execute the pg_type reciprocal
query, supplemented by private generation/ownership checks.
ACL ownership transfer/merge remains real shared SQL. Role rename remains a
PostgreSQL SQL phase: native direct and actual Prepare attempts assert the
existing 0A000/Position1 exclusion, check no effect, and retain eager ACL rows across
supported schema ownership transfer. No adjacent catalog or role capability is
implemented to make an incidental observation succeed.

```sh
make check RUN='^TestReferencePGNamespace(Shape|Builtins|Identity|Visibility|ACL|Temporary|TemporarySuperuserAuthority|Lifecycle|Diagnostics|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePGNamespace(Shape|Builtins|Identity|Visibility|ACL|Temporary|TemporarySuperuserAuthority|Lifecycle|Diagnostics|Controls|FeatureSmoke)$'
make check RUN='^TestReferencePGNamespace(ViewStoragePrecedence|CopyStoragePrecedence)$'
make check REFERENCE=1 RUN='^TestReferencePGNamespace(ViewStoragePrecedence|CopyStoragePrecedence)$'
make check RACE=1 RUN='^TestPGNamespace(ViewStoragePrecedence|CopyStoragePrecedence)$'
make check RACE=1 RUN='^TestPGNamespace(Shape|Capture|ACL|Authority|Cancellation|TemporarySuperuserAuthority)$'
make check RUN='^TestPGNamespaceFixtureOwnership$'
make check RUN='^TestReferencePGNamespaceFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGNamespaceFeatureSmoke$'
```

Use external900-second deadlines. These are selected proofs, not substitutes
for whole-project quickcheck or finalcheck. No discovery backend filter remains.
Pristine builtin expectations use the existing exported oracle DSN with its
database path set to `/postgres`, never the reset fixture's reconstructed
public schema. Builtins reads are read-only. The first-use TEMP transaction
witness alone uses `/postgres` for TEMP-only rollback/commit isolation on the
same physical creator before its first commit; it does not mutate permanent
schemas or shared infrastructure. Other TEMP scenarios use the normal fixture
and permit PostgreSQL backend-slot reuse of preexisting namespace tuples.

Controls are explicit native-only phases; there is no fake PostgreSQL API.
The session-only standalone public smoke is not committed. It asserts real
four-field prepared reads, seeds, user identity/rename, schema denial, TEMP
owner separation/pair and Dump/Reset/Restore; maintained feature smoke exercises
these surfaces on both backends with the fixture's recorders.


### pg_database selected proof

`TestReferencePGDatabase` independently specifies the PostgreSQL 18 eighteen-column
schema, raw driver metadata, current identity and cluster inventory, accessible
UTF8/C facts, owner and stored database ACL publication, ordinary-role visibility,
search-path shadowing, complete diagnostics and native-only attachment/control
lifetime boundaries. The initial NULL ACL means owner CREATE/TEMPORARY/CONNECT
and PUBLIC TEMPORARY/CONNECT; explicit empty and existing explicit policies remain
distinct. Unavailable native template/admission metadata/limit/horizon/placement
properties are typed NULL, not copied PostgreSQL physical values. Physical oracle
XIDs and generated database OIDs are checked by carrier and identity relationships,
not allocator goldens.
Unconstrained TEXT columns expose `ColumnType.Length()=(math.MaxInt64,true)` on
both native and pq adapters, including empty and NULL-only results. The other
pg_database types report unavailable length; all report unavailable decimal size.

Native opaque attachments and Reset/Dump/Restore are explicit native-only phases;
the shared SQL definitions run on every available backend.

The provider reads one immutable database descriptor with paired catalog/role
authority, projects actual stored ACLs through the existing ACLITEM codec, and
never scans other engines or manufactures physical horizons/placement.
Native Reset/Restore retain the destination database owner and exact ACL policy,
including NULL. Dumps do not transport source database policy. Physical CONNECT
and TEMP use enforcement is covered by `TestReferenceObjectGrantDatabaseAdmission`
in `reference/object_grants`; existing sessions survive CONNECT revocation.
`TestPGDatabaseShapeAndOwnership`, `Capture` and `Publication` supplement public
SQL with exact descriptor ownership/origins, concrete typed NULLs, cancellation,
foreign/stale authority rejection, transaction-private candidate isolation and
retained capture lifetime. New relation metadata extends only the represented
indexed/shared inventories and the bounded pg_tables inventory.

```sh
make check REFERENCE=1 RUN='^TestReferencePGDatabase(Shape|Identity|Metadata|Visibility|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferencePGDatabase(ACL|Controls|Smoke)$'
make check RUN='^TestReferencePGDatabase(Shape|Identity|Metadata|Visibility|Diagnostics)$'
make check RUN='^TestReferencePGDatabase(ACL|Controls|Smoke)$'
# Permanent feature smoke:
make check RUN='^TestReferencePGDatabaseSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGDatabaseSmoke$'
make check RUN='^TestPGDatabase(ShapeAndOwnership|Capture|Publication)$'
make check RACE=1 RUN='^TestPGDatabase(Capture|Publication)$'
make check RUN='^(TestPgClass(Projection|BuiltinIndexMetadata)|TestTableViewSummaryBuiltinIdentity)$'
make check RUN='^(TestReferencePgClassBuiltinIndexHistory|TestReferencePGTableViewsBuiltins)$'
make check REFERENCE=1 RUN='^(TestReferencePgClassBuiltinIndexHistory|TestReferencePGTableViewsBuiltins)$'
```

ACL cases restore the captured real database owner and exact nullable ACL through
the administrative fixture after borrowers close, before dropping roles. The
parameterized current-database-only catalog UPDATE is teardown, not a public
catalog mutation compatibility claim. Every cleanup failure is reported.

### pg_class selected proof

The shared `TestReferencePgClass` definitions cover the PostgreSQL 18 34-column
schema before iteration, exact existing relation identities/kinds, row types,
attribute high-water, CHECK/RLS/population facts, sticky index/subclass history,
ordered stored ACLs and grant options, installed lexical view options and typed
RANGE/default bounds. Direct, actual plain, fresh prepared and SAME held readers
retain independently specified metadata, raw codecs, rows and diagnostics.
Case-specific names scope database-wide catalog bags; table permission does not
filter metadata discovery.

`BuiltinIndexHistory` checks represented indexed table catalogs, including pg_aggregate, and
every represented view negative through direct, fresh and SAME prepared readers,
without inventing native backing-index rows. `FirstChildRollback` starts with a
childless RANGE parent: successful first ATTACH sets history only in the owner's
private catalog, then savepoint/full rollback restores false for both owner and
peer, including SAME prepared readers. `InvalidIndexHistory` retains an actual
live/unready/invalid index after a duplicate-data concurrent unique build fails,
asserts its index kind and parent history, then drops it while history stays true.

Native physical fields are explicit
[logical boundaries](../../docs/DIALECT.md#pg_class-logical-relation-metadata),
not parity goldens derived from native output. PostgreSQL observes real
sequence/partitioned-index initialization and ANALYZE; native tuples remain
unknown (-1) except fixed singleton sequences (1). Native structural bound output
differs intentionally from recorded released nodeToString bytes.

PostgreSQL performs actual member RENAME and catalog-source REVOKE/GRANT.
Native independently asserts their existing complete 0A000 capability errors;
retained-bound lifetime uses admitted member DROP and actual Dump/Restore.
`TestPgClassCatalogACLRecheck` publishes real native catalog ACLs with SetObjectACL,
checks denied/restored current permission through the same operation, and verifies
that retained authorization roots are unchanged. No schema/catalog privilege SQL
or hierarchy capability is added merely to produce metadata witnesses.

Run each public group in both modes after changing its definitions or owning
implementation:

```sh
make check RUN='^TestReferencePgClass(Shape|Relations|Builtins)$'
make check REFERENCE=1 RUN='^TestReferencePgClass(Shape|Relations|Builtins)$'
make check RUN='^TestReferencePgClass(Flags|PhysicalBoundaries|Partitions)$'
make check REFERENCE=1 RUN='^TestReferencePgClass(Flags|PhysicalBoundaries|Partitions)$'
make check RUN='^TestReferencePgClass(ACL|Options)$'
make check REFERENCE=1 RUN='^TestReferencePgClass(ACL|Options)$'
make check RUN='^TestReferencePgClass(Lifecycle|Transactions|Temporary|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferencePgClass(Lifecycle|Transactions|Temporary|Diagnostics)$'
make check RUN='^TestReferencePgClass(Controls|Smoke)$'
make check REFERENCE=1 RUN='^TestReferencePgClass(Controls|Smoke)$'
make check RUN='^TestReferencePgClassBuiltinIndexHistory$'
make check REFERENCE=1 RUN='^TestReferencePgClassBuiltinIndexHistory$'
make check RUN='^TestReferencePgClass(FirstChildRollback|InvalidIndexHistory)$'
make check REFERENCE=1 RUN='^TestReferencePgClass(FirstChildRollback|InvalidIndexHistory)$'
make check RUN='^TestPgClassBuiltinIndexMetadata$'
make check RUN='^TestPgClass(Projection|Kinds|ACLTranslation|CatalogACLRecheck)$'
make check RACE=1 RUN='^TestPgClass(SubclassPublication|SubclassReplacement|OptionHistory|PartitionTree|CatalogACLRecheck)$'
make check RUN='^TestPG(CastShapeAndOwnership|DescriptionShape|ExtensionShapeAndOwnership|SequenceShape|ShDescriptionDescriptor)$'
make check RACE=1 RUN='^(TestInheritanceQueryStoredViewAdmission|TestTypedPublicationLateOwnRowConsumer)$'
```

Affected private catalog provider consumers use named pg_class fields, retaining
their exact identity/kind/attribute-count assertions rather than the obsolete
ten-column offsets. Inheritance/stored-view and typed late-consumer fixtures
assert the immutable false-to-true subclass publication, then capture their real
post-successful-child baseline. Their later lock-wait/admission and rejected
mutation descriptor/storage invariants remain unchanged.

`Controls` replays actual emitted native Dump SQL on PostgreSQL, asserting no
invented absent options. Native failed/canceled replay and dependency/prepared
transaction fences retain the current bag, held readers and captured bytes;
successful replacement preserves valid live TEMP identities/options/history
and remaps persistent row types relationally. Active graph history reconstructs
on replay; deleted-only persistent history is not promised. Arbitrary explicit
relation ACL Dump rejection stays intact.

`Smoke` is the permanent public end-to-end witness. The part also supplies an
external public-only standalone module/program: root runs
`timeout 900s go run -mod=mod .` in its temporary module, expecting
`pg_class smoke OK` after complete shape/logical/tree/options assertions, one
retained prepared reader, explicit ACL/Dump rejection, baseline Restore,
invalid Restore atomicity and Reset. No standalone smoke scaffold is committed.
The historical standalone reader checked the then-eighteen indexed builtin table
facts and represented view negatives initially and after successful/failed Restore
and Reset. The maintained `BuiltinIndexHistory` definition includes pg_database,
pg_attribute, pg_namespace, pg_db_role_setting and pg_aggregate.
Selected proof and standalone smoke do not replace whole-project quickcheck.

### Tablespace inquiry selected proof

`tablespace_info_*_test.go` covers the two sole OID signatures, direct/plain/
prepared/held calls, complete diagnostics and warnings, metadata before iteration,
strict scalar NULL versus an empty SRF, live EXECUTE privileges, LATERAL/
ordinality/ROWS FROM, atomic consumers, retained rows and native controls.
Native pg_default occupancy is the real current attachment OID once because
nonshared catalogs exist, not a user row count or simulated cluster inventory.
The real oracle independently checks the full default database-name bag.
Unbounded TEXT uses the existing MaxInt64/known length contract for scalar and
FROM routes; these computed columns retain unknown nullability. Native's absent
builtin ACL, bootstrap owner and freshly created NOSUPERUSER establish no CREATE
grant; the additional `has_tablespace_privilege` inquiry probe is PostgreSQL-only,
not a prerequisite implementation added by this feature.

The physical scenario owns a real custom PostgreSQL directory: initially empty,
then occupied by a zero-row table, deduplicated across relations and callable by
an ordinary role without CREATE or table SELECT. Chmod of its own version
directory proves real 42501 inspection failure while location still reads the
symlink target. Permissions are restored before relation/tablespace/directory
cleanup. Immediate final-table DROP still reports physical occupancy on the
pinned oracle; no polling or physical cleanup emulation is implied. Native
physical creation/inspection independently refuses with 0A000.

Revoked location EXECUTE with the tested NULL argument returns TEXT NULL on
both backends through existing strict-scalar NULL pruning before admission.
Non-NULL location and strict NULL databases retain the common denial contract.
Exact diagnostics/warning demand counts come from pinned18.6 observations;
physical-directory boundaries have independent expectations rather than parity alone.

```sh
make check RUN='^TestReferenceTablespaceInfo(Values|Metadata|Occupancy|Warnings|PhysicalBoundary|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceTablespaceInfo(Values|Metadata|Occupancy|Warnings|PhysicalBoundary|Diagnostics)$'
make check RUN='^TestReferenceTablespaceInfo(Privileges|Physical|Composition|Lifecycle)$'
make check REFERENCE=1 RUN='^TestReferenceTablespaceInfo(Privileges|Physical|Composition|Lifecycle)$'
make check RUN='^TestTablespaceInfo(Binding|Protocol|Authority|Cursor|Stored)$'
make check RACE=1 RUN='^TestReferenceTablespaceInfo(Lifecycle|Privileges)$'
make check REFERENCE=1 RACE=1 RUN='^TestReferenceTablespaceInfo(Lifecycle|Privileges)$'
```

Use an external900-second command deadline. Full Docker-free quickcheck and
post-integration finalcheck remain separate obligations. Lifecycle PostgreSQL
replay requires the actual dump bytes produced by its native phase: no
handcrafted dump or replacement SQL fallback is accepted.

The root-run standalone public smoke is an external Go program, not an aggregate
repository test. With `TABLESPACE_INFO_SMOKE` naming its absolute source path,
run `go run "$TABLESPACE_INFO_SMOKE"` from the repository root under the same
external900-second deadline. Expected output is exactly
`tablespace info public smoke: PASS` after builtin/zero paths, typed NULL/empty
SRF, actual attachment OID, held reuse, complete warnings, explicit physical
refusal, retained rows and actual Dump/Reset/Restore/default/view assertions.
Real custom paths and directory permission errors remain mandatory oracle
proof above, not a native smoke claim.

### pg_tablespace and CREATE TABLESPACE selected proof

The shared `tablespace_*_test.go` scenarios independently specify builtin
`pg_tablespace` rows and ownership, named projection types, direct/prepared
syntax and diagnostics, owner/path/name precedence, fresh effective authority,
read-only/explicit/implicit transaction admission and failed-state recovery.
Real PostgreSQL creates a nonreserved tablespace in an actual container-owned
directory before asserting duplicate 42710, and explicitly drops its cluster
objects. Embedded creation must instead refuse physical locations with 0A000,
including repeated attempts, without publishing a user row or changing sentinels.
Reserved builtin names are not mislabeled as duplicates.

Both backends expose `oid,spcname,spcowner,spcacl,spcoptions`, with types
OID/NAME/OID/ACLITEM[]/TEXT[] and exact builtin rows
`(1663,pg_default,10,NULL,NULL)` and `(1664,pg_global,10,NULL,NULL)` before grants.
The two array NULLs retain distinct types; they are not empty arrays or an
invented owner ACL. Ordinary roles see both complete rows, including through
explicit qualification without pg_catalog in the explicit search_path.
Attribute numbers 1..5, array dimensions, nullability and collations are
independently specified; native origins remain relation1213/attributes1..5.
Direct, actual prepared, stored-view, empty and NULL-filtered reads retain the
five-column shape. Held name/OID binds cover builtin, absent, NULL and reuse.
Native Reset/Dump/Restore and retained-row witnesses are explicit control phases,
not fictitious PostgreSQL controls.

Tablespace CREATE grants project the owning descriptors' explicit `spcacl`
through the same captured immutable catalog root. An absent ACL remains raw NULL,
distinct from an explicit default-equivalent ACL; grant options and role cleanup
retain their ordinary authorization semantics. ACL metadata does not enable
physical tablespace creation or table placement.

Catalog read errors assert complete diagnostics and actual preparation/execution
stages. Invalid held OID binds use the shared Context
`unnamed portal parameter $1 = '...'`. Safe mutation witnesses use an ordinary
PostgreSQL role (42501); native mutation and virtual-catalog row locking refuse
with 0A000. Native immutability is not a universal PostgreSQL catalog restriction.
Valid WITH syntax reaches the physical boundary after earlier validation;
option-value compatibility is not claimed. Physical native refusal is 0A000,
where the absent-directory PostgreSQL counterpart is 58P01.
Canonical path byte boundaries are 970 accepted lexically and 971 rejected;
short-component and multibyte witnesses distinguish filesystem component limits
and rune counts from the SQL validation contract. The public OID codec outcomes
are independently specified (pq normalized strings, native int64), not compared
as if the drivers had identical raw carriers. Native Nullable is known while pq
Nullable is generally unknown. These instructions are not evidence.

```sh
make check REFERENCE=1 RUN='^(TestReferenceTablespace(Metadata|RealDuplicate|PathNamePrecedence|OwnerAuthority|Syntax|Transactions|Controls|FeatureSmoke|CatalogDiagnostics)|TestReferencePGAttributeBuiltins|TestReferencePgClass(Builtins|BuiltinIndexHistory))$'
make check RUN='^(TestReferenceTablespace(Metadata|RealDuplicate|PathNamePrecedence|OwnerAuthority|Syntax|Transactions|Controls|FeatureSmoke|CatalogDiagnostics)|TestReferencePGAttributeBuiltins|TestReferencePgClass(Builtins|BuiltinIndexHistory))$'
make check RUN='^Test(Tablespace|CreateTablespace|PGTablespace)'
make check RUN='^TestReferenceTablespaceFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceTablespaceFeatureSmoke$'
```

The durable smoke checks five-field builtin rows/types and both NULL arrays,
ordinary-role visibility, nullable attribute4/type1034/collation0 and held
builtin/absent/NULL/builtin reads. It retains owner and superuser errors, relative
and reserved-name errors, transaction admission, native mutation refusal,
physical refusal and unchanged sentinel19/no user rows. The real-directory
duplicate is covered by the selected family, not by reserved-name smoke assertions.

### pg_enum selected proof

The shared `TestReferencePGEnum` family specifies the four-column
`pg_catalog.pg_enum` contract independently of native output: raw metadata,
actual owning regtype identities, definition-only labels, generated member OID
relationships, initial/negative/fractional positions, stable ALTER identities,
dense-gap renumbering, ordinary query consumers, transaction publication and
retained Rows, logical controls, exact diagnostics and PUBLIC metadata access.
`FeatureSmoke` creates and evolves an actual enum and compares stored enum order
with catalog order; it is not a baseline catalog-registration test.

```sh
make check RUN='^TestReferencePGEnum(Shape|DefinitionsAndQueries|AlterPositions|Renumber)$' REFERENCE=1
make check RUN='^TestReferencePGEnum(VisibilityAndLifetime|Controls|DiagnosticsAndPrivileges|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferencePGEnum(Shape|DefinitionsAndQueries|AlterPositions|Renumber)$'
make check RUN='^TestReferencePGEnum(VisibilityAndLifetime|Controls|DiagnosticsAndPrivileges|FeatureSmoke)$'
make check RUN='^TestReferencePGEnumFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferencePGEnumFeatureSmoke$'
```

Logical Reset/Restore and native readonly mutation/locking boundaries are explicit
native-only phases within the shared scenarios. `Controls` produces a real native
logical dump using delivered enum DDL in an independent fixture-owned Driver for
PostgreSQL replay, avoiding state left by the preceding native backend.
PG-only pg_attribute shape inspection supplements the oracle without requiring
an adjacent catalog implementation. Raw FLOAT4 cells remain checked as float64;
logical position comparisons explicitly round through binary32 because pq parses
PostgreSQL's shortest decimal FLOAT4 text into float64.
Direct catalog origins retain the NOT NULL metadata contract; stored view
consumers use the existing native nullable projection metadata while asserting
the same exact column types, raw codecs and rows. Native schema/type privilege
SQL is outside scope: PostgreSQL alone performs the schema/type REVOKE setup and
supplemental privilege predicates. Both backends prove denied private-schema
type resolution followed by successful PUBLIC catalog metadata discovery.
Catalog-source SQL GRANT/REVOKE is also outside the delivered native privilege
syntax. PostgreSQL retains the complete SQL revocation diagnostic witness;
`TestPGEnumShapeAndCapture` exercises actual native PUBLIC access, explicit ACL
revocation/restoration through SetObjectACL, and retained authorization lifetime.

Private metadata/allocator/capture/import proof and affected enum callers:

```sh
make check RUN='^TestPGEnum(DefinitionMetadata|AllocatorBounds|SortInsertion|ShapeAndCapture|ImportMetadata)$'
make check RUN='^TestEnum'
make check RUN='^TestQueryFilterProvenEmptyDemand$'
make check RUN='^TestReferenceEnumAlter(Order|Controls|SavepointProvenance|VisibilityLocks|PreparedIdentity)$' REFERENCE=1
make check RUN='^TestReferenceEnumAlter(Order|Controls|SavepointProvenance|VisibilityLocks|PreparedIdentity)$'
make check RUN='^TestReferenceEnum(Controls|RestoreAtomicity|OwnerAndTemporaryRemapping|Diagnostics|PreparedLifecycle)$' REFERENCE=1
make check RUN='^TestReferenceEnum(Controls|RestoreAtomicity|OwnerAndTemporaryRemapping|Diagnostics|PreparedLifecycle)$'
```

During part verification, the root additionally runs the supplied standalone
public-API smoke from an external session temporary file:
`timeout 900s go run "$SESSION_TMP/pg-enum-smoke.go"`. It creates an independent
Driver/database, checks raw metadata and actual owner/member identities, holds
old Rows and a prepared catalog query, evolves z/q into first/z/a/end with
positions 0/1/1.5/2, and checks stored enum order. Dump/Reset/Restore must recreate
positions 1/2/3/4 with fresh member identities while old eager Rows remain
unchanged. A failed catalog mutation must return the full native 0A000 record
and leave rows unchanged. Success is quiet exit 0; any failed assertion exits
nonzero. This standalone observation supplements, not replaces, selected
dual-mode proof and whole-project quickcheck.

### Encoding conversion oracle-first definitions

`conversion_*_test.go` owns the shared conversion lifecycle, complete DDL
diagnostics/Prepare phases/notices, ownership, default lookup, duplicate-default
schema moves, actual encoding outputs, retained results, released catalog
associations, native controls and feature smoke. The expected handler and seed
inventory is literal REL_18_0 data, independent of the native implementation.
REGPROC output and its OID/TEXT casts are checked separately from handler names.

Initial pinned discovery is followed by native failure observation and selected
dual-mode proof using the same definitions; no backend-discovery filter remains.
Diagnostics encode the pinned oracle contract rather than native failure output.
PostgreSQL schema ACL phases use actual GRANT/REVOKE. Native public setup uses
explicit schema-owner transfers for reachability/CREATE, preserving conversion
ownership independently; actual native ACL denial remains private-seam coverage.
Namespace metadata uses independently captured namespace OIDs from uniquely
named ordinary tables resolved through existing qualified REGCLASS/pg_class.
Each witness is dropped before conversion owner/dependency assertions; no
pg_namespace or REGNAMESPACE capability is assumed. Seed repair reads the pinned
conversion's name and namespace ID, requiring the known hidden namespace or
pg_catalog identity. After native Restore remints a user namespace, cleanup
independently re-resolves only the known hidden schema under a fresh bounded
context and requires an exact ID match before repair.
Native private consumer units acquire real statement snapshots for conversion
evaluation and construct unsigned REGPROC carriers. General routine-alias input/
storage is covered by the [type-resolution family](../type_resolution/README.md#testing),
not by expanding conversion providers. Control units assert actual lossless replay and preservation of
existing namespace collisions, not incidental temporary-helper spellings;
the shared reference definitions separately retain exact public CREATE output.

Small selected commands (repeat each with and without `REFERENCE=1`):

```sh
make check RUN='^TestReferenceConversion(Lifecycle|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferenceConversion(Ownership|DefaultsAndLookup|DefaultSchemaMove)$' REFERENCE=1
make check RUN='^TestReferenceConversion(Metadata|ExecutionAndRetained|Controls)$' REFERENCE=1
make check RUN='^TestReferenceConversionFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceConversionCancellation$' REFERENCE=1
make check RUN='^TestReferenceConversionCancellation$' RACE=1
make check RUN='^TestReferenceConversion(Transactions|Temporary)$' REFERENCE=1
make check RUN='^TestReferenceVisibilityInquiry(Boundaries|UtilityBoundaries)$' REFERENCE=1
```

The affected visibility-boundary definitions now consume accepted conversion
CREATE through direct utility Query and genuine prepared execution. They assert
no Prepare publication, actual user conversion metadata/visibility, DROP removal
and unchanged sentinel rows; obsolete native conversion syntax exclusions are
not retained as compatibility shims.

Feature smoke expects raw LATIN1 `e9`, full `22P05` for Euro and SAME held
statement recovery, stable conversion identity across rename/schema move, exact
native quoted dump definition/replay and `42883` after dropping the reachable
default. Native controls are explicitly native-only phases in the same shared
body; Controls replays the actual native dump on the pinned PostgreSQL backend
and an independent native Driver. Seed repair is registered before mutation and
uses an independent bounded cleanup context before nonpublic schema teardown.

`TestReferenceConversionCancellation` selects a real custom LATIN1-to-UTF8
default after hiding the seed, observes actual provider/server admission for a
32MiB stored payload, cancels active reads and staged writes, joins every worker,
and recovers on the SAME held owner/statements with exact UTF8 bytes and no
published write prefix. The existing real admission helper remains unchanged.

`TestReferenceConversionTransactions` compares PostgreSQL transactional DDL with
the retained native Execute-phase `0A000` boundary, including prepared statements
and shared read-only `25006` diagnostics. `TestReferenceConversionTemporary`
uses an actual physical-session retirement barrier and verifies TEMP conversion
omission from portable Dump, retention across live native Reset/Restore, and
removal when the owning physical session exits. Controls also exercise named
owner replay, independently pre-created target principals, and missing-owner
failure without destination replacement.


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
dictionary creation unavailable natively. Schema ACL grants themselves are
supported; genuine package units stage corresponding member descriptors and actual ACL consumers. Concurrent
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
Unchanged ALTER/LOAD command boundaries remain separately asserted; missing DROP
now asserts common installed-catalog resolution diagnostics.

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

The complete native inventory includes cube1.5, unaccent1.1, intagg1.1, intarray1.5,
fuzzystrmatch1.1/1.2, pgcrypto1.4, pgrowlocks1.2 and uuid-ossp1.1. Intagg reports the released comment
`integer aggregator and enumerator (obsolete)`, superuser=true, trusted=false
and relocatable=true; availability does not imply installation.

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

### Pgcrypto hashing, passwords, raw AES/BF and random/FIPS selected proof

#### SHA password oracle-first checkpoint

The `TestReferencePgcryptoPasswordSHA*` definitions now contain permanent shared
assertions; the PG-only filter and intentional discovery failures are removed.
Root's pinned18.6 UTF8/C checkpoint established the release vectors, generated
salt boundaries, complete errors/notices, parser/password-boundary hashes and
PostgreSQL consumers/cancellation/smoke. Native failing proof preceded the
implementation; final selected dual-mode proof remains mandatory.

`SHAParserAndPasswordBoundaries` fixes literal oracle hashes for empty and
leading-dollar salts, raw16-byte salt scanning, ignored ordinary suffixes,
numeric normalization, UTF8 passwords and digest/block boundaries. `SHANotices`
asserts observed signed32 narrowing and signed64 overflow saturation, including
all empty diagnostic fields; it never hashes a potentially enormous selection.

Independent release/Drepper vectors cover both formats at default/explicit5000,
10000 and1000, empty passwords, truncation and stored-hash verification.
Generation covers default/zero/minimum/maximum selection and signed invalid
counts; maximum salts are never passed to successful hashing. Flat parser
errors assert all logical diagnostic fields, and notices cover clamp-before-error,
strict NULL, held executions and client_min_messages filtering. Consumers use
the ordinary installed identities and native control paths; cancellation uses
the shared admitted-and-joined helper with the native `pgcryptoSHA` frame.
The implementation uses standard-library SHA, fixed per-call scratch, streamed P
and cancellation in both DP input work and rounds; generator validation precedes
its16-byte entropy request. Provider1.4 and the fourteen-member manifest are unchanged.

Selected proof commands, each with an external900-second deadline; run both modes
for all affected definitions before review/integration.

```sh
make check RUN='^TestReferencePgcryptoPasswordSHA(Vectors|SaltGeneration|ParserAndPasswordBoundaries)$'
make check RUN='^TestReferencePgcryptoPasswordSHA(ParsingAndDiagnostics|Notices)$'
make check REFERENCE=1 RUN='^TestReferencePgcryptoPasswordSHA(Vectors|SaltGeneration|ParserAndPasswordBoundaries)$'
make check REFERENCE=1 RUN='^TestReferencePgcryptoPasswordSHA(ParsingAndDiagnostics|Notices)$'
make check RUN='^TestReferencePgcryptoPasswordSHA(ConsumersAndControls|Cancellation|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePgcryptoPasswordSHA(ConsumersAndControls|Cancellation|FeatureSmoke)$'
make check RUN='^TestReferencePgcryptoPassword(SaltGeneration|Identity|Relocation)$'
make check REFERENCE=1 RUN='^TestReferencePgcryptoPassword(SaltGeneration|Identity|Relocation)$'
```

The feature smoke includes literal hashes, safe generated-salt usability,
maximum salt shape, strict NULL, clamp notice and native control replay phases.
Feature smoke:

```sh
make check RUN='^TestReferencePgcryptoPasswordSHAFeatureSmoke$'
```

It asserts actual installed SHA direct/prepared hashes, stored-hash validation,
min/default generated salt usability, max salt shape, lower-clamp public notice,
NULL TEXT and new execution after native replay with retained old Rows/metadata.
The standalone public-API companion is session-owned, outside the checkout;
run `go run "$SESSION_TMP/password-sha-smoke.go"` under the same900-second deadline.

The native registry contains unaccent1.1, the two-wrapper intagg1.1,
selected-type intarray1.5, selected-function fuzzystrmatch1.2, bounded
pgcrypto1.4 and selected-function uuid-ossp1.1. Pgcrypto installs fourteen
genuine parallel-safe overloads: four immutable BYTEA digest/HMAC members,
immutable crypt(TEXT,TEXT), volatile gen_salt(TEXT[,INT4]) returning TEXT,
immutable encrypt/decrypt(BYTEA,BYTEA,TEXT) plus
encrypt_iv/decrypt_iv(BYTEA,BYTEA,BYTEA,TEXT) returning BYTEA,
volatile gen_random_bytes(INT4) returning BYTEA, gen_random_uuid() returning UUID
and fips_mode() returning BOOL. All are STRICT except the non-STRICT UUID wrapper.
Exact name/signature/result/volatility/strictness pairs are asserted using public
OID→REGPROCEDURE text identity, not OIDVECTOR/proargtypes. Namespace, positive
distinct IDs, owner and membership edges remain independently asserted;
PostgreSQL's full stock inventory is not equated to native's selected fourteen.

Fixed RFC/independently calculated digest/HMAC vectors cover empty, binary,
UTF8, exact-block and oversized keys through direct/new/held prepared frames.
Full logical diagnostics, NULL and demanded failing/effectful siblings are
asserted before backend comparison. Native-only controls exercise executable
default/CHECK/generated/index/view consumers, named owners with changed
destination attributes, held rebinding, retained old Rows/metadata, and failed
replay atomicity through public APIs.

Stored generation dependencies admit the actual installed procedure identity,
alongside the existing type/collation/operator/namespace providers; generated
columns and expression indexes remain full consumer witnesses through replay.
InstalledIdentity retains real PostgreSQL REVOKE/GRANT USAGE phases. Native SQL
has no schema GRANT/REVOKE: the ordinary private schema initially denies the
reader, then explicit ALTER SCHEMA OWNER enables both unqualified path lookup
and qualified calls without changing the installed procedures' owners.

Restore's strict owner manifest admits pgcrypto1.4 only with the complete fourteen
procedure name/signature pairs; dictionary/type members, duplicates, missing or
extra members and foreign signatures are rejected. The existing unaccent1.1
and intarray1.5 manifests retain their separate validation. Actual dumped
metadata is corrupted in the native control witness to check rejection without
changing live data, identities or executable consumers, including missing and
inconsistent member owners. Positive replay preserves named destination
principal identities even when role attributes differ; no provider fallback or
owner substitution is used.

Control replacement remaps source references before retagging against destination
types/collations, exactly once each. The supplementary `TestPgcryptoControlRemap`
component regression retains its manually mapped descriptor coverage; it is not
evidence of actual Restore collision handling.

`TestReferencePgcryptoForcedRelocation` owns independent native-only real-pipeline
relocation scenarios. An ordinary independent-Driver replay establishes candidate
procedure identities. A second identically initialized destination allocates
live TEMP sequences until all fourteen procedure OIDs have actual cross-class
collisions. Public Restore must relocate every imported procedure while retaining
the TEMP OIDs, rows and generator capture. Fixed digest/HMAC, crypt and AES results,
plus salt format/cost, random bytes/UUID bits and actual FIPS assertions, cover all
fourteen overloads after ordinary and collision replay. Defaults, CHECKs, generated
columns, expression indexes, stored views and a nested scalar-subquery view
remain executable. Procedure dependencies compare complete seven-field addresses
resolved independently from the named default/generated attrdefs, CHECKs, index
and view/nested rewrite rules, not class totals. Attrdef owning-column edges
also identify default column3 (`a`) and generated column4 (`i`).
Both persistent view relation OIDs are resolved separately from their `_RETURN`
rule OIDs, with exact internal rewrite-to-view ownership edges.
Source identities, rows and unread result/metadata captures remain usable.
Each independent destination has its own RawNative middleware recorder. Its SQL
pool wraps the exact configured connector shared with the raw native-control
pool, preserving Driver/DSN identity without recording control operations as SQL
witnesses. The collision destination keeps its wrapped physical connection
pinned from TEMP creation through Restore and refusal checks; source and baseline
assertions continue to use their respective recorders.
The collision destination captures its actual TEMP namespace/relation/rowtype,
and every retained sequence's relation/namespace association. Native verifies
both captured pg_class relation and reltype OIDs with REGCLASS and REGTYPE
resolution qualified by the captured actual TEMP namespace, before and after replacement.
The PostgreSQL-specific TEMP SQL phase asserts its public pg_type links without
simulating native Restore; shared PGType lifetime/control scenarios cover native
type metadata publication independently. The unread
INT4/BYTEA cursor and TEMP prepared SELECT are opened before Restore. The same
unread cursor's current Columns/ColumnTypes are checked before replacement,
after successful publication and after every refusal against independent names,
both scan types and Nullable(value,known) tuples and the saved snapshot.
Saved column metadata is rechecked after consumption without querying metadata
on the exhausted cursor. The deliberately blocking final TEMP view adds its
namespace/relation/rewrite identities, internal ownership edge and persistent
projected-column dependency edges to the final refusal snapshot.
Fourteen held crypto statements are prepared after relocation. Every wrong-version,
unknown-provider, malformed-manifest, late-SQL, cancellation, private-TEMP-placement
and retained-dependent-TEMP-view refusal uses the same postcondition: unchanged
persistent dump, procedure/consumer/owner identities, exact dependency tuples,
TEMP identities/data/metadata, fixed held/direct results, and fresh rolled-back
default/generated/CHECK/index/view/nested evaluation plus failed-write atomicity.
Provider validation rejects simultaneous crypto and fuzzy markers before provider
dispatch; the private mutation unit checks both descriptor and parent validation.
These are source assertions requiring fresh selected native and pinned-reference
proof; test definitions alone are not passing evidence.

#### Raw BF oracle-first proof

The catalog oracle opts into the test-only default-plus-legacy OpenSSL setup
described in [fixture testing](../fixture/README.md#test-only-legacy-crypto-oracle).
The new BF scenarios use only public APIs and the same bodies in both modes.
Published PostgreSQL release/Schneier vectors independently specify ECB,
CBC/PKCS, short/full56-byte keys and the complete29-byte CFB64 vector, in both
directions. Pinned18.6 observations supply literal key/IV/padding boundaries,
corrupted-CFB plaintext and full diagnostics/stages, including the consumer
CHECK's computed-row Detail. Empty raw keys agree with a single zero byte.

The erroneous assumption that `0000000001030303` has invalid padding was
corrected from the oracle: the trailing three03 bytes are valid and preceding01
is data. The permanent positive regression decrypts literal `cfe05dc46975bc21`
to `0000000001`; separate first/middle-run mismatches still fail. The first-byte
CBC witness uses observed zero-block ciphertext and an explicit XOR IV, not a
native-computed expected ciphertext.

Raw BF shares bcrypt's actual Blowfish primitive/constants, with distinct unsigned
raw key packing and reverse-round decryption. Per-call CBC/ECB/CFB64 loops keep
fixed scratch and one owned output; no new member or algorithm-specific ACL
identity is installed. AESUnsupported retains native AES-CFB/DES/3DES/CAST raw
exclusions and literal PG DES/CAST positives under legacy. Discovery probes and
backend filters are removed. Private primitive/key/padding/cancellation/isolation
and allocation tests supplement the public SQL; no startup-wiring test substitutes
for actual BF and AES outputs. Definitions and implementation still require
root-owned verification; earlier discovery/native-red failures are not passes.

BF reader SELECT grants belong to two transient forwarding views over the
actual stored table and decrypt view, not to the persistent consumers. The raw
encrypt call stays outside its forwarding view so direct/new/held EXECUTE
revocation checks retain genuine caller authority, including NULL/bad payload
and same-held regrant recovery. The held role statement closes first, then
teardown drops the forwarding views, removes explicit reader schema/function
privileges, restores PUBLIC EXECUTE and drops the reader.
All native Dump/Reset/Restore, retained rows/metadata, fresh recipe and refused
replay witnesses run BEFORE this reader phase. The reader witness is last in
both backend flows, and no Dump follows it: ordinary REVOKE leaves relation and
routine ACLs explicitly marked, which native Dump intentionally cannot serialize.
This sequencing retains all actual BF consumers/rows/dependencies and permission
assertions without adding ACL serialization or invoking DROP OWNED's unrelated
whole-catalog cleanup path.

Selected public proof (external900-second deadline per command):

```sh
make check REFERENCE=1 RUN='^TestReferencePgcryptoBF(Vectors|CFB64|KeyAndIV|PaddingAndDiagnostics|TypeGrammar)$'
make check RUN='^TestReferencePgcryptoBF(Vectors|CFB64|KeyAndIV|PaddingAndDiagnostics|TypeGrammar)$'
make check REFERENCE=1 RUN='^TestReferencePgcrypto(BF(OverloadsAndFrames|ConsumersAndControls|FeatureSmoke)|AESUnsupported)$'
make check RUN='^TestReferencePgcrypto(BF(OverloadsAndFrames|ConsumersAndControls|FeatureSmoke)|AESUnsupported)$'
make check REFERENCE=1 RUN='^TestReferencePgcryptoAES(NISTVectors|PostgresFixtures|KeyAndIV|OverloadsAndFrames|PaddingAndDiagnostics|TypeGrammar|NullAndDemand)$'
make check RUN='^TestReferencePgcryptoAES(NISTVectors|PostgresFixtures|KeyAndIV|OverloadsAndFrames|PaddingAndDiagnostics|TypeGrammar|NullAndDemand)$'
make check REFERENCE=1 RUN='^TestReferencePgcrypto(AES(Identity|ConsumersAndControls|RetainedControls|ReplayAtomicity|ReplayLifecycle|Relocation|FeatureSmoke)|InstalledIdentity)$'
make check RUN='^TestReferencePgcrypto(AES(Identity|ConsumersAndControls|RetainedControls|ReplayAtomicity|ReplayLifecycle|Relocation|FeatureSmoke)|InstalledIdentity)$'
```

Actual standalone BF feature smoke, not a startup/dependency-only substitute:

```sh
make check RUN='^TestReferencePgcryptoBFFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePgcryptoBFFeatureSmoke$'
```

It exercises fixed ECB/CBC/IV/fullCFB bytes, NULL,22023/39000 and held recovery;
the native phase additionally owns Dump/Reset/Restore, retained rows/metadata,
fresh DEFAULT/generated writes and the same fourteen-member inventory.
Every changed public definition requires both selected modes before review or
integration, plus whole-project Docker-free quickcheck. Private proof:

```sh
make check RUN='^TestPgcrypto(BF(Vectors|KeySchedule|Padding|Cancellation|IsolationAndAllocations)|AES(Vectors|KeyIV|Padding|Parser|Cancellation|Carrier)|Bcrypt(Vectors|PasswordProtocol|SaltParsing|SaltEncoding|Allocations|Cancellation))$'
make check RACE=1 RUN='^TestPgcryptoBF(Cancellation|IsolationAndAllocations)$'
```

Private resource witnesses assert deterministic context cancellation before/mid/
final publication in both directions/all modes, per-call/concurrent schedule
isolation and output-plus32KiB/16-allocation ceilings. Calibrated16-evaluation
batches normalize bytes per call; warmed AllocsPerRun retains exact key allocation
count equality. Data/key byte differentials use the same fixed32KiB allowance,
not exact equality between noisy process-wide samples. A1.5MiB key copy still
fails the independent absolute/differential bounds. Existing bcrypt regressions
retain password protocol semantics.

Hashing and AES control workloads are deliberately separate fixture owners.
The hashing `pgcryptoConsumerCase` helper creates the real SQL seed and supplies
literal proof steps without repeating all consumer observations in every case.
`TestReferencePgcryptoConsumersAndControls` checks the original inventory,
direct/prepared stored/view/nested results, exact CHECK diagnostic/atomicity,
procedure dependencies and real PostgreSQL TEMP links once.
`TestReferencePgcryptoControlRetention` owns unread rows/metadata and held calls
across real Dump/Reset/Restore on the same owner.
`TestReferencePgcryptoReplayAtomicity` gives each of the thirteen original bad
scripts its own changed-target scenario, preserving the complete unchanged dump,
owner inventory and digest postconditions. `TestReferencePgcryptoReplayLifecycle`
retains ordinary cancellation and missing-owner sentinel-target atomicity.
`TestReferencePgcryptoControlDestination` retains positive independent-Driver
import with changed owner attributes and all stored/view/nested/overload results.
`TestReferencePgcryptoForcedRelocation` reconstructs the real collision pipeline
for each refusal, retaining complete held/direct, identity, dependency, TEMP,
fresh-write and failed-write postconditions.

Each leaf uses the existing unchanged30-second fixture. No scenario renews a
deadline or retries an expired operation. All nine malformed-owner forms remain
covered both before and after forced relocation. Native control phases remain
explicit, not simulated in PostgreSQL. The seed-only helper has six top-level
consumers: ConsumersAndControls, ControlRetention, ReplayAtomicity,
ReplayLifecycle, ControlDestination and ForcedRelocation. The relocation helper
has one top-level caller, ForcedRelocation.

ForcedRelocation has fifteen named cells: `version`, `manifest_version`,
`unknown_provider`, `late_sql`, `private_temp_schema`, `owner_missing`,
`owner_extra`, `owner_duplicate`, `owner_name`, `owner_signature`,
`owner_dictionary`, `owner_type`, `owner_empty_owner`, `owner_missing_owner`
and `cancellation_then_stale_view`. Each retains real source/baseline replay,
all fourteen actual procedure-OID collisions, successful Restore, held statements,
TEMP identities and unread captures, full refusal postconditions, sequence73→74,
final source count3/baseline checks and original TEMP-cursor consumption.
The final cell preserves cancellation followed by the exact stale-TEMP-view
refusal on the SAME owner/handles, with both complete postconditions,
generator74 before stale-view creation, and the exact late error/projected rows.
The independent cells repeat required real preparation rather than borrowing
an earlier cell's remaining context; no variant or consumer assertion is removed.

`pgcryptoInvalidOwnerKinds` is the single nine-kind inventory and
`pgcryptoInvalidOwnerDump` implements all named owner mutations against the
actual emitted manifest, rejecting unknown names. `pgcryptoReplayFailure`
shares those mutations with hashing ReplayAtomicity and AES ReplayAtomicity/
Relocation, with no plural-script positional lookup. ForcedRelocation also
reuses the same named mutations and the four common replay-failure transforms.
The obsolete plural mutation helper and competing setup framework are removed.

AES retains its independently integrated ConsumersAndControls, RetainedControls,
ReplayAtomicity, ReplayLifecycle and Relocation definitions and workload split.
Shared mutation changes specifically affect AESReplayAtomicity and AESRelocation;
the complete focused proof below also retains the other integrated AES cases.
Oracle first, then the same completed native definitions and race;
external900-second deadline per command:

```sh
make check RUN='^TestReferencePgcrypto(ConsumersAndControls|ControlRetention|ReplayAtomicity|ReplayLifecycle|ControlDestination|ForcedRelocation)$' REFERENCE=1
make check RUN='^TestReferencePgcrypto(ConsumersAndControls|ControlRetention|ReplayAtomicity|ReplayLifecycle|ControlDestination|ForcedRelocation)$'
make check RUN='^TestReferencePgcrypto(ConsumersAndControls|ControlRetention|ReplayAtomicity|ReplayLifecycle|ControlDestination|ForcedRelocation)$' RACE=1
make check RUN='^TestReferencePgcryptoAES(ConsumersAndControls|RetainedControls|ReplayAtomicity|ReplayLifecycle|Relocation)$' REFERENCE=1
make check RUN='^TestReferencePgcryptoAES(ConsumersAndControls|RetainedControls|ReplayAtomicity|ReplayLifecycle|Relocation)$'
make check RUN='^TestReferencePgcryptoAES(ConsumersAndControls|RetainedControls|ReplayAtomicity|ReplayLifecycle|Relocation)$' RACE=1
```

Feature smoke remains `make check RUN='^TestReferencePgcryptoFeatureSmoke$'`
(also with `REFERENCE=1`): expect the independently specified owner/member
inventory, fixed text/binary digest and HMAC vectors, exact unsupported-hash
diagnostic and typed NULL. Smoke does not replace lifecycle proof.

```sh
make check RUN='^TestReferencePgcrypto(Password(Identity|Vectors|SaltGeneration|Diagnostics|OverloadsAndNull|ConsumersAndControls|Relocation|Cancellation|FeatureSmoke)|DigestVectors|HMACVectors|OverloadsAndParameters|DiagnosticsAndNull|InstalledIdentity|ConsumersAndControls|ControlRetention|ReplayAtomicity|ReplayLifecycle|ControlDestination|ForcedRelocation|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePgcrypto(Password(Identity|Vectors|SaltGeneration|Diagnostics|OverloadsAndNull|ConsumersAndControls|Relocation|Cancellation|FeatureSmoke)|DigestVectors|HMACVectors|OverloadsAndParameters|DiagnosticsAndNull|InstalledIdentity|ConsumersAndControls|ControlRetention|ReplayAtomicity|ReplayLifecycle|ControlDestination|ForcedRelocation|FeatureSmoke)$'
make check RUN='^TestReferenceAvailableExtensionsFiltering$'
make check REFERENCE=1 RUN='^TestReferenceAvailableExtensionsFiltering$'
make check RUN='^TestReferenceBinaryHash(Vectors|MD5Overloads|Consumers|Controls|Diagnostics|Quoted|Metadata|Inference|PreparedOwnership|Smoke)$'
make check REFERENCE=1 RUN='^TestReferenceBinaryHash(Vectors|MD5Overloads|Consumers|Controls|Diagnostics|Quoted|Metadata|Inference|PreparedOwnership|Smoke)$'
make check RUN='^TestPgcrypto(Bcrypt(Vectors|PasswordProtocol|SaltParsing|SaltEncoding|Allocations|Cancellation)|ProviderIdentity|ExpressionIdentity|OwnerMetadata|ControlRemap|OverloadResolution|RawVectors|StrictDemand|Cancellation|Password(Entropy|Volatility))$'
make check RUN='^(TestExtensionOwner(Metadata|ManifestAdmission)|TestIntarrayProviderIdentity)$'
```

The random/FIPS scenarios independently assert1/16/1023/1024-byte outputs, typed
BYTEA NULL, direct and reused prepared frames, metadata/raw adapter types, strong
entropy independent of setseed, and exact39000 `Length not in range` diagnostics.
Coercion failures retain their full logical diagnostics and literal UTF8 character
positions; range failures occur on execution, including prepared literal calls.
Bound invalid integer inputs share the exact parameter-import Context
`unnamed portal parameter $1 = '...'` in native and pinned PostgreSQL modes.
Later-row INSERT failure is statement-atomic and explicit transactions abort.
Precancelled direct/prepared calls test only the native adapter guarantee:
a fast PostgreSQL result can win lib/pq's cancellation race, so this is not a
PostgreSQL server-cancellation or pinned-connection-health assertion.

UUIDs are independently decoded to16 octets and checked for V4/variant bits,
canonical text and native string versus PostgreSQL []byte representation.
Core pg_catalog.gen_random_uuid remains available without installation. Ordered
implicit/explicit catalog paths and qualified calls prove actual installed
binding through default/view procedure dependencies, not just UUID value shape.
All three installed random/FIPS members are volatile through reconstruction:
defaults/views are valid, generated columns/expression indexes reject42P17.
Native controls retain stored bytes/UUIDs without regeneration, old Rows/metadata,
zero-argument recipes, actual installed dependencies, real held-statement rebinding
and independent-driver collision relocation. Failed/cancelled replay is atomic;
missing named destination owners are not silently substituted.
Held UUID calls validate raw UUID values and metadata rather than introducing
an unrelated closed-source UUID→TEXT cast. Relocation retains genuine unread
eager database/sql.Rows and independently checks their value/type/metadata
through successful and refused replay. This does not rebind SQL HOLD portals:
their old FETCH/MOVE execution remains fenced by native catalog replacement.
Trusted installer coverage uses common DROP EXTENSION before reinstalling,
preserving unrelated schema/data state and genuine native replacement-control
phases. Schema visibility uses actual GRANT USAGE, not ownership transfer.

fips_mode returns actual active Go crypto/fips140.Enabled() in native mode, not
algorithm availability, certification, host flags or query-time GODEBUG parsing.
No OpenSSL installation is required. Pinned PostgreSQL's ordinary OpenSSL mode
was observed false; native mode is intentionally not forced to match it.
Use separate startup processes for native off/on proof, narrowly selected because
older digest/password cases intentionally exercise non-FIPS algorithms. Preserve
other necessary GODEBUG options explicitly if the environment requires them.
The new feature smoke exercises installation, prepared byte frames/NULL/bounds,
UUID raw bits/type and path-dependent procedure binding, FIPS BOOL, volatile
consumers and native stored-value/recipe replay with old Rows. The native-control
phases are not represented as PostgreSQL control operations.

```sh
make check RUN='^TestReferencePgcryptoRandom(Bytes|Diagnostics|Identity|UUID|FIPSMode|ConsumersAndControls|Relocation|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePgcryptoRandom(Bytes|Diagnostics|Identity|UUID|FIPSMode|ConsumersAndControls|Relocation|FeatureSmoke)$'
make check RUN='^TestReferencePgcryptoRandomFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePgcryptoRandomFeatureSmoke$'
env GODEBUG=fips140=off make check RUN='^TestReferencePgcryptoRandomFIPSMode$'
env GODEBUG=fips140=on make check RUN='^TestReferencePgcryptoRandomFIPSMode$'
make check RACE=1 RUN='^TestReferencePgcryptoRandom(ConsumersAndControls|Relocation)$'
make check RUN='^TestReferencePgcrypto(InstalledIdentity|ConsumersAndControls|RetainedControls|ReplayAtomicity|ReplayLifecycle|Relocation|FeatureSmoke|Password(Identity|ConsumersAndControls|Relocation|FeatureSmoke))$'
make check REFERENCE=1 RUN='^TestReferencePgcrypto(InstalledIdentity|ConsumersAndControls|RetainedControls|ReplayAtomicity|ReplayLifecycle|Relocation|FeatureSmoke|Password(Identity|ConsumersAndControls|Relocation|FeatureSmoke))$'
make check RUN='^TestReferenceUUIDGeneration(Signatures|BitsMetadataAndOrder|StoredConsumersAndControls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceUUIDGeneration(Signatures|BitsMetadataAndOrder|StoredConsumersAndControls|FeatureSmoke)$'
make check RUN='^(TestPgcrypto(Random(Resolution|Evaluation|Cancellation|Binding)|ProviderIdentity|ExpressionIdentity|OwnerMetadata|ControlRemap|OverloadResolution|StrictDemand|Cancellation)|TestExtensionOwnerManifestAdmission|TestUUIDGenerationPacking)$'
```


Password witnesses use published PostgreSQL/Openwall vectors plus pinned18.0
UTF8/C observations:2a versus historical2x octet expansion, 71/72/73-byte
truncation including split UTF8, stored hashes, arbitrary ignored suffixes and
noncanonical salt padding. Bf salt generation tests omitted/zero default6,
cost4 and cheap31, strong entropy independent of SQL reseeding, repeated held
frames/rows/sessions, strict TEXT NULLs and exact coercion/failure stages.
DES/xdes/MD5 witnesses additionally use REL_18_6 published constants and pinned
PostgreSQL18.6 UTF8/C edge observations: byte-eight DES truncation, extended DES
full-password folding and even crypt counts, MD5 eight-byte/empty/nonalphabet
salts, and generator defaults25/725/1000 with full24-bit odd xdes counts
through16777215. New algorithms share direct, newly prepared and same-held
frames with bcrypt, NULL and failure recovery. The valid UTF8 MD5 salt
`$1$aaaaaaé` occupies exactly eight bytes and has an exact public vector.
`$1$aaaaaaaé` truncates inside é and the oracle returns invalid UTF8 bytes;
`TestPgcryptoMD5Salt` pins that raw result internally, without changing SQL
TEXT admission or normalizing/replacing bytes. Only SHA crypt/generator
algorithms retain the intentional native0A000 boundary. No temporary
oracle-discovery test or PostgreSQL-only scenario gate remains.
Generator syntax is independently decoded: des2, xdes9, md5 11 (no trailing `$`)
and bf29 characters. Signed count validation precedes2/3/6/16-byte entropy
requests, pinned by package units, with no entropy for invalid/NULL frames.
`$2b$`/`$2y$` witnesses are exact DES fallback hashes, never extra bcrypt variants.
`PasswordIdentity` uses the same real schema REVOKE/GRANT USAGE on both backends:
the denied reader becomes a non-owner grantee, while the installer retains the
schema and installed procedure ownership.
`PasswordRelocation` additionally forces all fourteen actual OID collisions while
preserving stored random salts/hashes, crypt generated-column/view recipes and
gen_salt defaults, exact dependency addresses, held members, future writes and
failed replay atomicity. `PasswordConsumersAndControls` retains old TEXT Rows,
metadata, held rebinding and same/independent Driver replay without regenerating
stored salts. Volatile generated-column/index refusals include complete fields.
`PasswordCancellation` observes the real `pgcryptoBcrypt` worker/server query at
cost31 before cancellation. Its multirow source is explicitly ordered, despite
reverse seed order: the first row supplies the independently published completed
foox hash without bcrypt, and only the later row runs row-dependent parameterized
cost31 crypt. A nontransactional sequence marks both the successful prefix and
later-row crypto arguments. The shared admission helper waits for exact progress2
(autocommit) or4 (transaction) through a separate observer before canceling; this
also prevents PG active-query observation from canceling before that boundary.
Same-owner currval confirms the progress after cancellation/rollback. No cursor,
result or prefix rows may publish; sentinel data, held-statement continuation and
explicit transaction abort/rollback recovery remain independently asserted.
Private calibrated allocation budgets cover input size and cost independently.

Standalone feature smoke (same public scenario, no duplicate runner):

```sh
make check RUN='^TestReferencePgcryptoPasswordFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePgcryptoPasswordFeatureSmoke$'
make check RUN='^(TestPgcrypto(DES(Vectors|Protocol|Cancellation)|MD5(Vectors|Salt|Cancellation)|Salt(Parameters|Encoding)|Password(Dispatch|Entropy|Volatility)|Bcrypt(Vectors|PasswordProtocol|SaltParsing|SaltEncoding|Allocations|Cancellation)))$'
make check RACE=1 RUN='^(TestPgcrypto(DES(Protocol|Cancellation)|MD5Cancellation|PasswordEntropy|BcryptCancellation)|TestReferencePgcryptoPasswordCancellation)$'
```

Expected smoke observations: exact DES `NB53EGGqrrb5E`, xdes
`_J9..j2zz4BeseiQNwUg`, MD5 `$1$Szzz0yzz$IYL49cd3t9bllsA7Jmz1M1`
and existing60-character bcrypt hashes/TEXT metadata; legal2/9/11/29-character
salts, low-count usability, wrong-password inequality, DES truncation and MD5
salt truncation; full invalid-salt22023 diagnostic, immutable crypt versus
volatile gen_salt metadata, and native stored
salt/hash preservation plus executable defaults/views after Reset/Restore.
These descriptions are assertions awaiting root verification, not proof results.

Raw AES witnesses use all six published NIST SP800-38A CBC/ECB key-size vectors,
all four blocks in both directions, and fixed PostgreSQL regression bytes.
Pinned18.0 oracle-first observations supply explicit key/IV boundary ciphertexts,
binary PKCS length frames, nopad ending preservation, empty results, aliases,
type-option precedence and complete logical diagnostics. Discovery filters and
unknown-value logging are removed before native regression and final proof.
Direct/new/held calls preserve BYTEA metadata and strict source-order demand.
The AES consumer/control scenarios independently resolve actual procedure dependencies
for defaults/generated/CHECK/index/view/nested recipes, exercise all fourteen
held/direct calls through Reset/Restore and force actual cross-class collisions.
Named owners, unread bytes/metadata, detached output ownership, live fresh writes
and refused replay atomicity are public API witnesses, not serialized-cipher-state
tests. Native AES-CFB/DES/3DES/CAST raw exclusions assert0A000 separately from
pinned OpenSSL positive outputs under the catalog's test-only legacy setup;
these intentional capability differences do not exclude BF CBC/ECB/CFB64.

Each new or changed definition and helper caller needs both selected modes:

```sh
make check RUN='^TestReferencePgcryptoAES(NISTVectors|PostgresFixtures|KeyAndIV|OverloadsAndFrames)$'
make check REFERENCE=1 RUN='^TestReferencePgcryptoAES(NISTVectors|PostgresFixtures|KeyAndIV|OverloadsAndFrames)$'
make check RUN='^TestReferencePgcryptoAES(PaddingAndDiagnostics|TypeGrammar|NullAndDemand|Unsupported)$'
make check REFERENCE=1 RUN='^TestReferencePgcryptoAES(PaddingAndDiagnostics|TypeGrammar|NullAndDemand|Unsupported)$'
make check RUN='^TestReferencePgcryptoAES(Identity|ConsumersAndControls|RetainedControls|ReplayAtomicity|ReplayLifecycle|Relocation|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePgcryptoAES(Identity|ConsumersAndControls|RetainedControls|ReplayAtomicity|ReplayLifecycle|Relocation|FeatureSmoke)$'
make check RUN='^TestReferencePgcrypto(InstalledIdentity|ConsumersAndControls|RetainedControls|ReplayAtomicity|ReplayLifecycle|Relocation|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePgcrypto(InstalledIdentity|ConsumersAndControls|RetainedControls|ReplayAtomicity|ReplayLifecycle|Relocation|FeatureSmoke)$'
make check RUN='^TestReferencePgcryptoPassword(Identity|ConsumersAndControls|Relocation|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePgcryptoPassword(Identity|ConsumersAndControls|Relocation|FeatureSmoke)$'
make check RUN='^TestPgcryptoAES(Vectors|KeyIV|Padding|Parser|Cancellation|Carrier)$'
make check RACE=1 RUN='^(TestPgcryptoAESCancellation|TestReferencePgcryptoAES(OverloadsAndFrames|ConsumersAndControls|RetainedControls|ReplayAtomicity|ReplayLifecycle|Relocation))$'
```

Raw AES feature smoke is `make check RUN='^TestReferencePgcryptoAESFeatureSmoke$'`,
also with `REFERENCE=1`. Expected results are fixed f397…/2c24… ciphertexts and
ASCII foo plaintext from independently fixed decrypt inputs, all BYTEA.
The separately supplied public standalone smoke additionally exercises NIST
CBC/ECB frames, exact diagnostics, stored expressions, retained rows, owned
outputs and held rebinding through native controls. Its source belongs in the
session, never as a repository scaffold. These definitions are not passing
verification evidence until root observes them.



Selected checks use external900-second deadlines; the root alone runs these
after edits. Whole-project Docker-free verification remains `make quickcheck`.
The standalone public smoke source is supplied in the OMP session; it is not a
test fixture import or a duplicate committed runner.


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
shared pg_type structural witness, separate pg_attribute cast boundary and native-only
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
actual emitted Dump replay and cancellation cleanup. Shared pg_type identity
links supplement native descriptor/type-link units; the TEMP namespace is
independently obtained from the public pg_sequences row. The
session namespace inquiry scenarios additionally cover pg_my_temp_schema and
physical TEMP/TOAST ownership. Shared direct/prepared behavior is not skipped.

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
specified70-row supported boundary, including explicit xid8→xid (function5071),
exact six-column shape and raw codecs,
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


### Inheritance metadata contract

The eight `TestReferencePGInherits` scenarios independently specify PostgreSQL
18's exact four-column `pg_catalog.pg_inherits` shape: nonnullable OID/OID/INT4/BOOL,
with no row OID. Endpoints are captured independently through `pg_class`, never
derived from the catalog under test or compared to another allocator's sequence.
OID-aware assertions check native int64 versus pq decimal-byte carriers before
normalizing them. Native nullability is known/nonnullable; pq reports unknown.

Coverage includes declared multiple-parent order and direct-only ancestry,
LIKE/view/typed/local-index negatives, nested table and real index partitions,
invalid/incomplete index parents, repeated manual index attachment, future
partitions, owner/peer savepoints and held reexecution, detach/drop/rename/schema
move/recreation, PUBLIC discovery without endpoint SELECT, TEMP visibility and
catalog name shadowing. Native controls assert logical replay, atomic failed
Restore and retained result values. Diagnostics retain full English messages,
positions, details/hints and parameter portal context, not SQLSTATE alone.
PostgreSQL ordinary-role mutation permission errors are separate from native
virtual-catalog mutation/row-lock `0A000` boundaries.

Fixture details matter: a root UNIQUE constraint uses a matching named child
UNIQUE constraint when asserting reuse of its captured backing-index identity.
An ordinary reader receives actual schema USAGE, but not endpoint SELECT.
Empty row collectors are nonnil to match the shared ordered comparator contract.
All bodies run on both available backends; there are no discovery filters.

Run each selected definition in both modes, with an external900-second deadline:

```sh
make check RUN='^TestReferencePGInherits(Shape|Inheritance|PartitionsAndIndexes)$' REFERENCE=1
make check RUN='^TestReferencePGInherits(Shape|Inheritance|PartitionsAndIndexes)$'
make check RUN='^TestReferencePGInherits(Transactions|IdentityAndDrop)$' REFERENCE=1
make check RUN='^TestReferencePGInherits(Transactions|IdentityAndDrop)$'
make check RUN='^TestReferencePGInherits(Visibility|Controls|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferencePGInherits(Visibility|Controls|Diagnostics)$'
make check RUN='^TestReferenceInheritance(GraphLifecycle|AlterPhysicalNames)$' REFERENCE=1
make check RUN='^TestReferenceInheritance(GraphLifecycle|AlterPhysicalNames)$'
make check RUN='^TestPGInherits(ShapeAndOwnership|DescriptorEdges|Cancellation)$'
```

Private units exercise the actual projection directly, including relation2611,
native rowtype10005/array10004 ownership, column origins, PUBLIC SELECT, typed
values, generation retention and deterministic cancellation. These native seeded
type IDs are not asserted as generated PostgreSQL identities. Whole-project
quickcheck and post-merge finalcheck remain separate gates.

For a small standalone public-API smoke outside the test runner, save this program
as `.tmp-pg-inherits-smoke/main.go` and run
`go run ./.tmp-pg-inherits-smoke/main.go` from the repository root with an external
900-second deadline. It checks ordered numeric identities and logical replay,
prints `pg_inherits standalone: ok` only after all checks, and exits nonzero on
failure. Remove the temporary source afterward.

```go
package main

import (
    "bytes"
    "context"
    "database/sql"
    "fmt"
    "os"
    "time"
    "stuntgres"
)

func run() error {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    connector, err := stuntgres.NewDriver().OpenConnector("inherits-standalone")
    if err != nil { return err }
    db := sql.OpenDB(connector)
    defer db.Close()
    for _, q := range []string{
        "CREATE TABLE pi_smoke_p(a INT)",
        "CREATE TABLE pi_smoke_q(a INT)",
        "CREATE TABLE pi_smoke_c() INHERITS(pi_smoke_q,pi_smoke_p)",
    } {
        if _, err := db.ExecContext(ctx, q); err != nil { return err }
    }
    check := func(empty bool) error {
        rows, err := db.QueryContext(ctx, "SELECT c.relname,p.relname,i.inhseqno,i.inhdetachpending,c.oid=i.inhrelid AS child_identity,p.oid=i.inhparent AS parent_identity FROM pg_catalog.pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid JOIN pg_class p ON p.oid=i.inhparent WHERE c.relname='pi_smoke_c' ORDER BY i.inhseqno")
        if err != nil { return err }
        defer rows.Close()
        n := 0
        for rows.Next() {
            var child, parent string
            var ordinal int64
            var pending, childIdentity, parentIdentity bool
            if err := rows.Scan(&child,&parent,&ordinal,&pending,&childIdentity,&parentIdentity); err != nil { return err }
            if empty || n >= 2 || child != "pi_smoke_c" || parent != []string{"pi_smoke_q","pi_smoke_p"}[n] || ordinal != int64(n+1) || pending || !childIdentity || !parentIdentity {
                return fmt.Errorf("unexpected edge %s %s %d", child,parent,ordinal)
            }
            n++
        }
        if err := rows.Err(); err != nil { return err }
        want := 2
        if empty { want = 0 }
        if n != want { return fmt.Errorf("edge count %d, want %d", n,want) }
        return nil
    }
    if err := check(false); err != nil { return err }
    var dump bytes.Buffer
    if err := stuntgres.Dump(ctx, db, &dump); err != nil { return err }
    if err := stuntgres.Reset(ctx, db); err != nil { return err }
    if err := check(true); err != nil { return err }
    if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil { return err }
    if err := check(false); err != nil { return err }
    fmt.Println("pg_inherits standalone: ok")
    return nil
}

func main() {
    if err := run(); err != nil { fmt.Fprintln(os.Stderr,err); os.Exit(1) }
}
```

The catalog-part implementation handoff additionally supplies a standalone
acceptance program covering the complete empty/populated metadata, held
transaction rollback, explicit partition-index attachment, logical replay and
missing-column diagnostic. Its expected stdout is
`pg_inherits smoke: ordered=2 partition_table=1 partition_index=1 rollback=ok restore=ok diagnostics=42703`.

### Constraint metadata contract

The `TestReferencePGConstraint` scenarios specify the released 28-column
`pg_constraint` shape independently, including actual n/c/p/u/f/x definitions,
stable sparse keys, CHECK encounter order, FK operator and action vectors,
partition provenance, identity-preserving lifecycle changes, transactional
visibility, logical native controls, and complete SQL diagnostics.
Generated object identities are checked relationally, not against PostgreSQL's
allocator. CHECK `conbin` is a typed captured expression: its internal tree bytes
are backend-specific; presence and deparsed semantics remain asserted.
The adjacent `pg_attribute` scan is an explicit PostgreSQL-only validation phase;
native exact column ordinals, origins, types and nullability are independently
covered by `TestPGConstraintShapeAndOwnership`, while all 28 driver fields remain
shared. Operator signatures and exact builtin identities deliberately use the
existing shared `regoperator` scalar identity API; those constraint tests do not
depend on scanning executable `pg_operator` metadata. Namespace movement uses a
separately named destination relation's `pg_class.relnamespace`,
with nonzero/distinct-from-public identity and SAME-held constraint reexecution;
it does not depend on native `pg_namespace` or regnamespace implementation.

Run these unfiltered selected definitions in both modes with an external
900-second deadline:

```sh
make check REFERENCE=1 RUN='^TestReferencePGConstraint(Shape|Kinds|ForeignKeys)$'
make check RUN='^TestReferencePGConstraint(Shape|Kinds|ForeignKeys)$'
make check REFERENCE=1 RUN='^TestReferencePGConstraint(Provenance|Lifecycle|Transactions)$'
make check RUN='^TestReferencePGConstraint(Provenance|Lifecycle|Transactions)$'
make check REFERENCE=1 RUN='^TestReferencePGConstraint(Controls|Diagnostics|FeatureSmoke)$'
make check RUN='^TestReferencePGConstraint(Controls|Diagnostics|FeatureSmoke)$'
```

`TestReferencePGConstraintExclusionSignatures` independently specifies canonical
exclusion equality identities: CIDR/INET and CIDR domains use INET operator1201;
OID and every admitted OID alias (including alias domains) use OID operator607;
VARCHAR uses TEXT operator98 and enum uses anyenum operator3516. Direct,
fresh-prepared and SAME-held reads assert exact key/operator vectors and real backing identities. A retained reader
prepared before each exclusion is created must still read unrelated CHECK
metadata afterward; a valid exclusion cannot poison catalog-wide discovery.
The complete scenario is unfiltered; run both modes:

```sh
make check REFERENCE=1 RUN='^TestReferencePGConstraintExclusionSignatures$'
make check RUN='^TestReferencePGConstraintExclusionSignatures$'
```


Feature smoke: `make check RUN='^TestReferencePGConstraintFeatureSmoke$'`
and `make check REFERENCE=1 RUN='^TestReferencePGConstraintFeatureSmoke$'`.
It creates all six admitted kinds, asserts the exact empty star description,
CHECK keys and semantics, actual FK supporting-index/operator relationships,
held-reader rename freshness, and rollback of actual ADD/DROP operations.
Component proof and selected race coverage exercise catalog-owned descriptors,
actual projection/capture/publication, and comparison-family metadata directly:

```sh
make check RUN='^TestPGConstraint(ShapeAndOwnership|Projection|Capture|Publication|OperatorMetadata|ExclusionMetadata)$'
make check RACE=1 RUN='^TestPGConstraint(Capture|Publication)$'
```

Promoted owner assertions and retained lifecycle/provenance coverage require
both selected modes as well:

```sh
make check RUN='^TestReferenceAlterAddConstraint(Kinds|ExistingData|Diagnostics|NotValid|Controls|Provenance|Transactions|PrimaryPrerequisite|SuccessorWitness|ValidityAttributes|FreshLikeValidity)$'
make check REFERENCE=1 RUN='^TestReferenceAlterAddConstraint(Kinds|ExistingData|Diagnostics|NotValid|Controls|Provenance|Transactions|PrimaryPrerequisite|SuccessorWitness|ValidityAttributes|FreshLikeValidity)$'
make check RUN='^TestReferenceInheritance(Constraints|LocalConstraints)$'
make check REFERENCE=1 RUN='^TestReferenceInheritance(Constraints|LocalConstraints)$'
make check RUN='^TestReferenceConstraintRename(Transactions|DeferredExclusion)$'
make check REFERENCE=1 RUN='^TestReferenceConstraintRename(Transactions|DeferredExclusion)$'
make check RUN='^TestReferencePartitionConstraints(NonuniqueCatalog|IncompleteReuse)$'
make check REFERENCE=1 RUN='^TestReferencePartitionConstraints(NonuniqueCatalog|IncompleteReuse)$'
```

Selected proof does not replace whole-project Docker-free `make quickcheck`
or the root-owned post-integration finalcheck.

### Daitch-Mokotoff selected proof

`TestReferenceDaitchMokotoffCodes` specifies every published PostgreSQL18 manual
and REL_18_0 regression array, preserving complete alternative element order.
Direct literal/parameter, newly prepared and repeatedly held calls assert `_TEXT`
metadata and raw carriers. Boundaries independently require SQL NULL for NULL,
empty and unencodable input, six zero digits for A, skipped punctuation inside
phonemes, narrow Latin1/special-letter handling and no 255-character limit.
Diagnostics prescribe complete English fields for absent installation, signatures,
quoted names, missing schema, off-path lookup and recognized scalar decorations.
The Public and Relocated diagnostic cases retain `public.daitch_mokotoff` and
`dm_addon.daitch_mokotoff` in DISTINCT/ORDER BY/FILTER/OVER primary messages.
Direct Query and Prepare failures require whole42809 records with Position8,
ERROR severities and empty Hint/Detail and other unsourced fields.

Installation checks fuzzystrmatch1.2 availability, real extension/procedure
identity relationships, immutable/strict/parallel-safe text→text[] metadata,
transaction/savepoint rollback, duplicate notices and invalid-version atomicity.
Trusted ordinary installation independently checks SAME-connection current_user
and session_user before installation and after reset. The extension remains
installer-owned (`dm_installer`); the procedure belongs to fixed bootstrap
superuserOID10. Its exact declared name is native `postgres` versus pinned
fixture initdb `stuntgres`, not the fixture's separate native-replay postgres role.
Native fuzzystrmatch membership has ten selected overloads: DM, four
Levenshtein signatures, three Metaphone signatures and two Soundex signatures.
The upstream-only undocumented `text_soundex` alias is not falsely asserted
absent on PostgreSQL. Selected Soundex calls now execute: qualified
soundex('John') is J500 and difference('Ann','Anne') is4.
Native pg_proc does not expose proargtypes: its exact argument signature25 is
checked against the real installed descriptor in the provider identity unit,
while an explicit PostgreSQL-only metadata phase asserts proargtypes25. The
remaining exposed identity/properties and callable overload outcomes are shared.
Stored expressions consume the function in views, defaults, CHECK constraints
and STORED generated columns; a late failed multirow insertion preserves all
prior rows. Controls share those SQL outcomes and separately exercise native
Dump/Reset/Restore, retained array cursors, independent Driver attachment,
symbolic named-owner redump and failed/canceled replacement preserving executable
consumers. Shared named-owner and schema-DROP/CASCADE SQL outcomes are distinct
from native missing-owner replay and staged-prefix I/O/cancellation. The same
held DM statement returns its complete array before and after replacement,
using existing same-shaped builtin-array reanalysis; no stale-alias capture or
universal stale-statement fence is claimed.

```sh
make check REFERENCE=1 RUN='^TestReferenceDaitchMokotoff(Codes|Boundaries|Diagnostics|Installation|StoredExpressions|Controls|FeatureSmoke)$'
make check RUN='^TestReferenceDaitchMokotoff(Codes|Boundaries|Diagnostics|Installation|StoredExpressions|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceAvailableExtensionsFiltering$'
make check RUN='^TestReferenceAvailableExtensionsFiltering$'
make check RUN='^TestDaitchMokotoff(PublishedCodes|Boundaries|Cancellation|ProviderIdentity|ExpressionIdentity|Remap)$'
```

The feature smoke expects complete John/Bierschbach/brzęczy arrays, NULL/empty,
qualified off-path resolution, stored generated/view values and reusable held
execution. Precanceled no-output/ordinary context-error assertions are native-only;
pq fast precancellation has no stable nil-output guarantee. Selected proof does
not replace whole-project quickcheck, standalone feature smoke or finalcheck.

### Levenshtein selected proof

The nine `TestReferenceLevenshtein` definitions independently specify distances,
weighted substitutions, bounded exact/greater-than behavior, signed release
int64 cases and the 22003 range error, Unicode character limits and empty bypass, strict NULL/demand,
complete diagnostics and installed identity. Direct literals/binds, fresh
Prepare and SAME held frames assert INT4 metadata/raw values and recovery after
execution errors. Invalid INT4 bind failures retain the pinned PostgreSQL portal
Context on both adapters through the existing parameter-input annotation policy.

Diagnostics includes positional-prefix/named-suffix syntax in all four selected
signatures and public/relocated qualification. The unnamed installed overloads
still fail normal missing-signature resolution; carrying labels through the
ordinary call grammar does not enable named matching. Parser units retain the
prefix's empty label slots, suffix labels, value order and source coordinates.
Bounds/ReleaseBoundedInt64 specifies the observed PostgreSQL 18.6 results of
the six nonempty/max0 cost cases that wrapped in 18.0 (no wrapping; one
out-of-range error) plus 18.6's own regression case. These pathological
outputs are release witnesses, not ordinary mathematical bound or sentinel
guarantees.

Installation requires four genuine selected identities/extension edges on both
backends, with total native membership10 including retained DM, Metaphone and Soundex. Stored SQL uses a
relocated provider in defaults/generated/CHECK/view, nested/CASE, grouping and
join expressions. Failed atomic inserts leave original rows unchanged. Controls
share the SQL setup, then use real native APIs for Dump ordering, exact
ten-member symbolic manifests, retained INT4 rows, held reanalysis, independent
Driver/named-owner replay and failed/I/O/canceled Restore atomicity. PostgreSQL
has no invented control equivalent.

Run each small set in both modes after changes:
```sh
make check RUN='^TestReferenceLevenshtein(Distances|Costs|Bounds)$'
make check REFERENCE=1 RUN='^TestReferenceLevenshtein(Distances|Costs|Bounds)$'
make check RUN='^TestReferenceLevenshtein(UnicodeAndLength|NullAndDemand|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceLevenshtein(UnicodeAndLength|NullAndDemand|Diagnostics)$'
make check RUN='^TestReferenceLevenshtein(Installation|StoredExpressions|Controls)$'
make check REFERENCE=1 RUN='^TestReferenceLevenshtein(Installation|StoredExpressions|Controls)$'
make check RUN='^TestReferenceDaitchMokotoff(Installation|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceDaitchMokotoff(Installation|Controls|FeatureSmoke)$'
make check RUN='^TestLevenshtein(Distance|Weights|Bounds|ReleaseInt64|Unicode|Cancellation|ExpressionIdentity|Remap)$'
make check RACE=1 RUN='^TestReferenceLevenshtein(NullAndDemand|Controls)$'
```

Use external900-second deadlines. The standalone public-consumer smoke is
materialized outside the checkout from the session's
`local://fuzzy-levenshtein-smoke.go` and run from the repository root:
```sh
timeout 900s go run "$SESSION_TMP/fuzzy-levenshtein-smoke.go"
```
It must exit silently0 after independently checking distance3, weighted3,
bounded exact3/greater-than2, Unicode255/nonempty256 error/empty256 bypass,
typed NULL, held weighted/bounded frames, retained DM codes and membership10,
stored recipes, Dump/Reset/Restore, independent Driver replay and failed-Restore
preservation. Selected proof and smoke do not replace whole-project Docker-free
quickcheck or post-integration finalcheck.

### Metaphone selected proof

The seven `TestReferenceMetaphone` definitions independently specify release
codes, primary/alternate differences,255-byte source/output boundaries, empty
cap bypass, strict NULL, punctuation and multibyte limits. Codes and boundaries
exercise literals, binds, fresh Prepare and SAME held frames; descriptors and raw
TEXT values remain checked for NULL and empty results. Diagnostics checks all
structured fields, function visibility, named/VARIADIC/decoration rejection,
literal and portal INT4 input, and held execute-error recovery.
Late full-input Germanic classification is independently checked with `SIO`,
300 punctuation bytes and a trailing `W`; `bagallegos` checks continued alternate
generation after the primary code fills, yielding `PKLK` / `PKKS`.

Installation checks three real procedure identities, TEXT metadata,
immutable/strict/parallel-safe flags, extension/namespace links, superuser and
trusted bootstrap ownership. Native total membership is8; the oracle's extra
functions are not asserted absent. Private-schema visibility recovery uses the
same genuine `GRANT USAGE ON SCHEMA` on both backends, without transferring schema
ownership.

StoredExpressions checks relocated defaults, generated codes, CHECK, views,
WHERE, CASE/nesting, grouping, joins and atomic late write failure. A shared
literal-call view independently returns AK/SM0/SMT before and after Restore,
including independent Driver replay; this proves persisted call arguments and
qualified invocation without depending on identifier quoting in serialized SQL.
Controls shares the SQL phase, then separately checks native Dump ordering,
exact10-member owner manifest, Reset/Restore, old TEXT rows/metadata,
SAME held reanalysis, independent Driver replay and failed/canceled restore
atomicity. PostgreSQL has no fictitious equivalent of these native APIs.

```sh
make check REFERENCE=1 RUN='^TestReferenceMetaphone(Codes|Boundaries|Diagnostics)$'
make check RUN='^TestReferenceMetaphone(Codes|Boundaries|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceMetaphone(Installation|StoredExpressions|Controls|FeatureSmoke)$'
make check RUN='^TestReferenceMetaphone(Installation|StoredExpressions|Controls|FeatureSmoke)$'
make check RACE=1 RUN='^TestReferenceMetaphone(Boundaries|Controls)$'
```

Use external900-second deadlines. FeatureSmoke expects KM/KMP/KMP, AX cap2 AK,
Smith SM0/XMT, Schmidt XMT/SMT, typed NULL/empty/empty-cap bypass, generated/view
rows, WHERE id2, SAME held reuse and cap0 error followed by cap1 success. Native
phases actually restore and reinvoke consumers/functions/held statements,
validate membership10 and reject a failed replacement without losing invocation.
The standalone public-consumer program is retained in session
`local://fuzzy-metaphone-smoke.go`, materialized outside the checkout and run as:

```sh
timeout 900s go run "$SESSION_TMP/fuzzy-metaphone-smoke.go"
```

Expected success is quiet exit0 only after independent rows/type/diagnostic/
held/generated/WHERE/manifest/restore assertions. Selected proof does not replace
whole-project Docker-free quickcheck or post-integration finalcheck.


### Soundex selected proof

The seven `TestReferenceSoundex` definitions independently specify only
`soundex(text) RETURNS text` and `difference(text,text) RETURNS int4`.
Manual18.6 F.16.1 and unchanged REL_18_0/REL_18_6 source ground the expectations;
the runtime remains pinned PostgreSQL18.0 UTF8/C. Initial oracle discovery
confirmed Codes/Boundaries/Diagnostics/Installation before native implementation,
including full metadata and diagnostics. No discovery-only backend filter remains.

Ann/Anne are unequal spellings sharing A500/difference4; Anne/Andrew score2
and Anne/Margaret0. Original-byte adjacency is deliberate: Ashcraft A226,
BHB/B2B B100, B1B B000, D3D D000. Empty and no-ASCII-letter codes are empty TEXT,
not NULL or0000; two such buffers agree4. Inputs have no255-byte limit.
PostgreSQL's documented multibyte limitation is retained, not approximated by
Unicode normalization/transliteration: Émile M400 versus Emile E540.

Direct literals/binds, fresh Prepare and SAME held name/NULL/empty/name frames
assert independent values and complete TEXT/INT4 metadata even with zero rows.
Both runtime-row NULL and the exact literal NULL/division witness demand22012.
Failure expectations include exact English primary, all exposed logical fields
including empties, nil error cursors and preparation/execution stages.
Installation checks genuine namespace/owner/procedure/dependency identities,
while stored views/defaults/generated/CHECKs exercise both result types.
Controls share real SQL setup with PostgreSQL and separately exercise native
Dump ordering, exact10-member named-owner manifest, retained old rows/detached
metadata, held reanalysis, independent Driver replay and failed/canceled Restore
preservation. Native control APIs are not claimed as PostgreSQL facilities.

Run every changed definition in both modes, with external900-second deadlines:

```sh
make check RUN='^TestReferenceSoundex(Codes|Boundaries|Diagnostics|Installation|StoredExpressions|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceSoundex(Codes|Boundaries|Diagnostics|Installation|StoredExpressions|Controls|FeatureSmoke)$'
make check RUN='^(TestReferenceDaitchMokotoff(Installation|Controls)|TestReferenceLevenshtein(Installation|Controls)|TestReferenceMetaphone(Installation|Controls|FeatureSmoke))$'
make check REFERENCE=1 RUN='^(TestReferenceDaitchMokotoff(Installation|Controls)|TestReferenceLevenshtein(Installation|Controls)|TestReferenceMetaphone(Installation|Controls|FeatureSmoke))$'
make check RUN='^(TestSoundex(Kernel|Expression|ExpressionIdentity|ExpressionRemap|ExpressionProperties)|TestDaitchMokotoffProviderIdentity|TestMetaphoneExpressionIdentity|TestLevenshteinExpressionIdentity)$'
```

The standalone public-consumer source is session evidence at
`local://fuzzy-soundex-smoke.go`, materialized outside the checkout:

```sh
timeout 900s go run "$SESSION_TMP/fuzzy-soundex-smoke.go"
```

Expected success is quiet exit0 only after actual independent phonetic/lexical,
byte/NULL/metadata/held/stored/identity/manifest/replacement assertions.
Selected proof and smoke do not replace whole-project Docker-free quickcheck
or post-integration finalcheck.


The partitioned-exclusion class cutover changes the shared inventory consumed by:

```sh
make check RUN='^TestReferencePGOpclass(Inventory|IndexSemantics|Diagnostics|HeldExecution|Lifecycle|FeatureSmoke)$'
make check RUN='^TestReferencePGOpclass(Inventory|IndexSemantics|Diagnostics|HeldExecution|Lifecycle|FeatureSmoke)$' REFERENCE=1
```

These selected proofs require independent pinned18.0 confirmation of GiST
inet_ops10017/network_ops3550/input869/nondefault and
range_ops10078/family3919/input3831/default. No native-derived golden is used.

