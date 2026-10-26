# internal/engine/pgcatalog

Row producers for the system relations: the `pg_catalog` tables and views and
the producer-backed `information_schema` tables and views (`sql_features`,
`sql_sizing`, `sql_implementation_info`, `sql_parts`, `table_privileges`,
`data_type_privileges`, `user_mappings`, `key_column_usage`,
`constraint_column_usage`, `column_column_usage`, `routine_sequence_usage`,
`character_sets`, `enabled_roles`, `view_table_usage`, `domain_constraints`,
`sequences`, `role_column_grants`, `role_table_grants`, `role_routine_grants`, `routine_privileges`,
`role_udt_grants`, `role_usage_grants`, `columns`, `domains`, `views`, `tables`, `routines`). Each producer is a pure
function from a captured catalog, a paired role read and a narrow runtime
capability to rows. Orchestration (per-OID catalog choice,
unlock timing, wrapping rows as query sources) stays in the engine; see the
[engine README](../README.md).

## Imports

Layer L5 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports `catalog`, `authorization`, `functions`, `largeobject`, `lock` (lock
records), `expr`, `ids`, `settings`, `sqltype`, `sqlstate`, `parser` (catalog
descriptors store parser enums) and the standard library. It never imports the
root engine, `access`, `storage` or `visibility`.

## Producer contract

```go
type Input struct {
    Context context.Context
    Catalog catalog.Read
    Roles   authorization.Read
    Owner   *catalog.Owner
    Ref     ids.ObjectRef
    Source  *catalog.Relation
    Runtime Runtime
    TypeNamePath []ids.ObjectAddress
}
type Producer func(Input) ([][]sqltype.Value, error)
```

- A producer returns rows and nothing else. It never builds a query source,
  keeps a statement scope or takes a lock; the engine wraps the rows.
- A successful capture with no rows returns a non-nil empty slice (an actual
  empty anonymous source); nil is never a substitute. Cancellation or invalid
  authority returns nil rows and the error, never a prefix.
- Rows follow the column order of `Input.Source`, each cell typed by that
  column's descriptor. Producers check the expected shape and types and fail
  otherwise.
- Producers check `Context` between descriptors and return its error.
- Rows are immutable once returned: the engine may share one capture across
  statements with the same inputs, so neither producers nor consumers write
  into returned rows.
- **Provenance.** `Input.Owner` is the engine's stable `*catalog.Owner`; its
  `Domain`, `Principals` and `ACLAuthority` are compared as the same pointers,
  never as equal names or numbers. `Catalog.Owner()`, `Ref.Domain()`,
  `Roles.BelongsTo(Owner.Principals)` and `Source` identity must agree before
  rows are produced.
- `pg_ts_parser` projects the immutable stock parser identity and its five
  internal handler references with installed OID/NAME/REGPROC descriptors.
  These rows are builtin metadata, not mutable parser definitions or ACLs.
  `pg_proc.proretset` comes from actual routine declaration metadata.
  Its condensed row includes FLOAT4 `prorows`, declaration `prosrc` and OIDVECTOR
  `proargtypes` built from each routine's input signature. Zero arguments produce
  an empty non-NULL vector. Selected internal parser and vector-I/O metadata
  supplements retain genuine identities without installing callable providers;
  represented descriptors take precedence. Installed codec source symbols stay
  independent of mutable public names.
- `pg_index` vector cells use the runtime's captured index metadata projection,
  then shared typed constructors: all key/INCLUDE attributes in `indkey`,
  key-only `indcollation`, `indclass` and `indoption`. No textual vector store or
  duplicate index-class/option derivation belongs to the catalog producer.
- Absent and explicit-empty ACLs stay distinct: `ClassACL` returns NULL for an
  absent ACL and `{}` for an empty one.
- Information-schema producers emit genuine source-owned domains, not bare
  primitive carriers. `role_column_grants`, `role_table_grants`,
  `role_routine_grants`, `routine_privileges`, `role_udt_grants` and `role_usage_grants` use `sql_identifier`,
  `character_data` and `yes_or_no` from the captured type catalog.
  Numeric metadata likewise retains genuine `cardinal_number` domain values.
- `role_column_grants` captures one catalog/role frontier and projects actual
  table/column ACL origins on live positive attributes. Its raw-origin UNION
  includes the grant-option bit before ownership determines grantability, so
  identical final rows can retain multiplicity. Visibility uses enabled grantor
  or enabled named grantee, including PUBLIC origins with an enabled grantor;
  it does not reuse table/column data-access predicates. Membership closures
  and grantee/owner authority are cached within that capture.
- `role_table_grants` projects only real relation ACL origins, including
  zero-column relations, with enabled grantor-or-grantee visibility and no
  schema-USAGE or data-readability filter. PUBLIC origins require an enabled
  grantor; an actual current superuser sees all origins. INHERIT closures and
  grantee/owner authority use one captured frontier. Absent ACLs supply seven
  owner origins; explicit-empty ACLs supply none. MAINTAIN, column ACLs and
  partition-parent expansion do not contribute rows. Grant options or immediately
  usable owner authority determine grantability; only SELECT has hierarchy.
- `routine_privileges` and `role_routine_grants` share one EXECUTE ACL projector
  over actual Procedure descriptors of every represented kind, without inspecting
  or executing bodies or requiring schema USAGE. Both expose enabled grantor or
  named-grantee origins; `routine_privileges` additionally exposes every PUBLIC
  origin. The role view retains PUBLIC origins when their grantor is enabled.
  Absent ACLs supply owner and PUBLIC origins; explicit-empty ACLs supply none.
  Grantability belongs to the named grantee through its grant option or immediately
  usable owner authority, never the observer's authority; PUBLIC is not grantable.
  One captured catalog/role frontier owns domains and lazy membership/ownership
  caches. Specific names retain the actual routine OID within NAME's UTF8 boundary.
- `role_udt_grants` projects raw type USAGE origins as `TYPE USAGE` for actual
  reciprocal composite row types, including lookup-only catalog rows. Arrays
  and domains do not contribute. Synthetic default ACLs retain raw absence;
  absent ACLs supply owner/PUBLIC origins and explicit-empty ACLs supply none.
  Enabled grantor or named grantee determines visibility, without schema or
  relation access filters. Grantability belongs to the named grantee, using
  captured grant options and immediately usable ownership authority.
- `role_usage_grants` projects eight nullable domain-typed columns from real
  domains, sequences, collations, foreign wrappers and foreign servers. It
  preserves USAGE origins and multiplicities, applying enabled grantor-or-named
  grantee visibility without schema, data-readability, TEMP or identity filters.
  Named-grantee grant options or immediately usable ownership determine
  grantability, never observer ownership. Raw-NULL domains supply owner/PUBLIC
  origins; wrappers and servers supply the owner origin. Raw-NULL sequences
  supply none, following the released table-default fallback; explicit-empty
  ACLs never create origins. Compatible collations supply owner-to-PUBLIC only.
  Foreign objects have empty schema names. Captured catalog, roles and domain
  authorities remain stable, with cancellation discarding the entire result.
- `pg_type` composes user-domain facts through exact captured immediate-base
  providers, including nominal-only domains and reciprocal arrays. Metadata
  admission does not grant executable codec or storage authority; foreign
  same-OID base generations are not interchangeable.
  Installed scalar I/O identities come from each exact scalar's own position in
  its provider-validated manifest, checked against formal argument/result types.
  Procedure language OIDs derive from the descriptor's existing declaration
  metadata; display language grants no execution capability.
- `routines` reads only actual captured Procedure descriptors. Immutable provider
  display facts are separate from execution-language authority. Enabled ownership
  or effective EXECUTE exposes the row; only enabled ownership exposes source,
  while C link symbols remain visible to executors. Declared result identity is
  not domain-unwrapped and does not admit a codec or execute a provider. All 82
  cells, including NULLs, retain their captured information-schema domains.

## SQL feature metadata

`info_sql_features.go` exposes the complete PostgreSQL 18 release conformance
matrix from `info_sql_features_data.go`, independently of native SQL execution
capabilities. Released support flags and comments are literal metadata, not
inferred from parser admission, catalog population or parent/child features.
The relation is a physical catalog-backed table with seven nullable
`character_data`/`yes_or_no` domain columns, typed NULL verification values,
genuine empty strings and PUBLIC SELECT. Ordinary schema/relation ACL admission
still applies; there is no per-feature identity or object visibility filter.
Each capture owns its output buffers and captured domain descriptors; process
data contains only strings. Cancellation or invalid capture authority returns
no partial matrix.

`info_sql_sizing.go` owns a small standardized sizing inventory and captures four
nullable `cardinal_number`/`character_data` columns. Positive values describe
enforced native bounds; zero is unlimited or undetermined, while typed NULL
marks unsupported sizing items. Character-set comments accompany only actual
identifier byte bounds. The physical catalog table uses ordinary schema/relation
admission and PUBLIC SELECT, without per-item identity or object filtering.
Captures validate both domains against the current base descriptor generations
and allocate detached rows; cancellation publishes no prefix.

`info_sql_parts.go` owns eleven fixed full-part conformance records, independently
of individual feature support and user objects. All support values are `NO`:
no entire SQL-standard part is fully implemented, including SQL/Foundation and
SQL/Schemata. Reading this table does not itself establish full-part support.
The physical table has five nullable `character_data`/`yes_or_no` domain columns,
typed NULL verification and non-NULL empty comments matching the release data.
Unlike the other conformance tables, its default ACL has no PUBLIC SELECT.
Ordinary schema USAGE and relation SELECT admission expose the complete matrix
to authorized identities, not an identity-filtered subset. Each capture validates
paired catalog/role authority and current domain/base identities, owns detached
row buffers and returns no prefix on cancellation or invalid authority.

## SQL implementation metadata

`info_sql_implementation_info.go` owns twelve stable initial product
characteristics, not feature-conformance population. The physical table has
five nullable `character_data`/`cardinal_number` domain columns. Integer zero,
empty character strings and typed NULLs remain distinct. DBMS NAME is
`stuntgres`; DBMS VERSION uses `functions.EmbeddedVersionBanner`, the same pure
identity as `version()`. Initial collation `C` and default isolation information
do not change with user collations or session settings.

Schema USAGE and relation SELECT admission apply normally; PUBLIC SELECT
exposes the complete bag without per-item identity filtering. Each capture
validates the catalog/role frontier and exact captured domains/base identities,
owns its row/cell buffers and publishes no prefix on cancellation or error.


## Registry

`catalog_source_producers.go` holds one process-initialized array of
`Registration{OID, Produce}`, sorted by relation OID and never changed.
`ProducerFor(oid)` finds a producer by binary search; `Registrations()` returns
a copy for coverage checks. `pg_locks` (`LocksOID`) and `pg_sequences` are not
registered: the engine handles those two itself.

`catalog_source_specs.go` holds `Specs`, the ordered descriptor metadata for
every system relation (OID, namespace, name, columns with type, typmod,
dimensions, nullability and collation, flags). Spec order is the descriptor
registration order and differs from the registry order.

### Adding a system relation

1. Add a `Spec` to `Specs` at the position that keeps registration order.
2. Write a `Producer` in its own file, named after the relation.
3. Add `{OID, producer}` to the registry, keeping it sorted.
4. If the producer needs engine state, add a value-taking method to `Runtime`
   and implement it on the engine's `EngineCatalogRuntime`. If the engine must
   choose a different catalog, role read or unlock policy for the OID, add a
   case to `openCatalogSourceLocked` in the engine.

## Runtime capability

`Runtime` is the narrow interface the engine implements per call
(`EngineCatalogRuntime`). Methods take and return values, never engine structs.
Building the adapter reads nothing; only the selected producer calls the
methods it needs. Rule facts, view options and policy conditions are read from
catalog payloads directly (`Relation.HasRules`, `Relation.VisitRules`,
`ViewPayload.Options`, `PolicyDefinition.VisitExpressions`), not through
`Runtime`. Maintenance-aware projections need an adapter backed by a real
engine; an empty adapter is not an initialized maintenance capture.

| Methods | Why they are engine-owned |
|---|---|
| `CursorCount`, `VisitCursors` | session cursor registry (`pg_cursors`) |
| `SessionSettings` | one coherent typed settings snapshot under the physical-session mutex (`pg_settings`) |
| `PreparedCount`, `VisitPreparedTransactions` | prepared-transaction registry, assigned XIDs, published role names |
| `TimezoneInput`, `DatabaseName`, `TemporaryOwner`, `CurrentPrincipal` | statement input environment, session database, physical TEMP owner, current user |
| `ValidateCapture`, `Effective`, `CanUse`, `SelectColumns`, `ForeignServerVisible` | error-bearing privilege checks through the statement's authorization view |
| `ValidateParameter` | settings registry validation of parameter ACL rows |
| `ValidateACL`, `ObjectACL` | ACL validation and raw object ACLs (`access` is not imported here) |
| `ValidateType` | type descriptor validation, including installed extension types |
| `TypeOutput` | regclass, regrole and aclitem formatters for type defaults |
| `Maintenance`, `DatabaseFrozenXID`, `VisitVacuumProgress` | immutable logical maintenance facts and authenticated active-work redaction |
| `ExpressionTree` | structural `pg_node_tree` output of bound expressions |
| `VisitCheckAttributes`, `ExpressionReferences`, `ViewReferences`, `IndexReferences`, `PartitionReferences`, `IndexSimpleAttribute`, `IndexKey` | walks over engine plan and expression nodes |
| `ForeignKeyComparisons` | the binder's foreign-key comparison operators (`pg_constraint`) |
| `ViewDefinition`, `IndexDefinition`, `VisitRuleDefinitions` | current-identity stored definition rendering; rule visitor omits exact `_RETURN` before rendering |
| `ColumnExpression`, `ColumnUpdatable` | captured stored-expression display and metadata-only view/rule/foreign capability |
| `ViewCapabilities` | structural event and actual trigger facts, bridging stored views and FDW handler interfaces without query execution |
| `Pinned`, `OperatorSeeded`, `OperatorDependencies`, `TypeProviderProcedure` | pin decisions, the builtin operator inventory and operator dependencies owned by `routine`, extension type providers |
| `VisitSequenceDefinitions` | sequence definitions with the statement's journal overlay (`pg_sequence`) |

## Exported helpers

The engine calls `ClassKind`, `ClassACL`, `AttributeMetadata`,
`HasSystemAttributes`, `AttributeNumber`, `InitializeAttributes`,
`CollationActualVersion`, `DatabaseCollationActualVersion`,
`DescriptorComment`, `ForeignDependencies`, `VirtualViewWriteError`,
`LockRows`, `CapturePGSequences`, `NewVacuumProgressViewer` (which progress
rows the current user reads in full: superuser, the worker's authenticated
role or `pg_read_all_stats`) and the OID constants (`LocksOID`,
`CursorsOID`, `SettingsOID`, `TypeOID`, ...). Engine unit tests also call row
helpers such as `CollectDepend`, `ClassRow`, `AttributeRow`, `TypeFacts` and
`ConstraintOperator`. `CollationActualVersion` reads actual provider
authority, never the recorded `Version`.
`CollectDepend` emits each direct extension-membership edge through
`catalog.ExtensionMembership`; per-class branches add only their other edges.

## Relations with special rules

- `pg_am`: live global method descriptors with owned typed cells per capture;
  family→method and method→handler dependencies use shared selectors, and
  references to pinned stock handlers are suppressed, not fabricated.
- `pg_depend`: repeated routine result/formal/support declarations share one
  normal declaration edge per referenced address. Checked standard-body and
  default-expression dependencies retain their separate capture-pass rows,
  including column/subobject identities. Materialization suppresses pinned
  prerequisites without globally deduplicating these independent passes.
- `pg_largeobject` (2613): actual immutable stored chunks, each borrowed chunk
  copied once into an owned BYTEA; holes are never materialized and per-object
  ACLs are not consulted (ordinary relation SELECT protects it).
- `pg_largeobject_metadata` (2995): object OID, creator's owner OID and raw
  `ClassACL`, without per-object filtering or redaction.
- `pg_rules`: projects represented stored relation rules as NAME/NAME/NAME/TEXT,
  excluding only the exact `_RETURN` name. Definitions are public metadata:
  relation ACLs, schema USAGE, search path and another session's TEMP ownership
  do not filter rows. Captured presentation controls current-name SQL and own
  TEMP spelling; reads never authorize or execute rule actions.
- `pg_transform` (3576): checks cancellation, then the five carrier types, and
  returns a non-nil empty source.
- `pg_settings`: projects registry metadata and one coherent typed snapshot;
  the engine filters restricted rows using captured effective-user INHERIT
  authority before acquiring the settings mutex. Boot/reset metadata preserves
  SQL NULL separately from empty current strings and simulated startup defaults.
  Explicit simulated startup assignments report `override`, not a loaded file;
  source locations remain NULL. The producer owns its enum-array buffers and
  never publishes partial canceled rows.
- Table-statistics and progress views: committed logical DML and manual VACUUM
  facts and real active phases with authenticated-owner visibility, captured
  under the engine capture lock; unknown metrics stay typed NULL.
- `information_schema.user_mappings`: real mapping identities; server owner or
  effective USAGE exposes all mappings. It never copies options or reuses
  `ForeignServerVisible`, which governs `pg_user_mappings` option redaction.
- `key_column_usage`: foreign-key positions follow the supporting index's
  ordered keys; visibility is owner inheritance or a column privilege.
- `constraint_column_usage`: CHECK columns come from `ExpressionReferences`,
  never a second walker; owner-role inheritance on the table is the only
  visibility predicate.
- `column_column_usage`: generated-column dependencies from the shared
  `ExpressionReferences` walker over analyzed (not folded) expressions,
  deduplicated per generation; enabled relation-owner membership is the only
  visibility predicate, evaluated once per relation.
- `routine_sequence_usage`: direct stored SQL-body relation references to real
  sequences, validated before duplicate suppression; sequence-owner role
  inheritance is the only visibility predicate.
- `enabled_roles` (16208): the paired catalog/role capture and
  `CurrentPrincipal`; an ordinary actor uses one cancellation-aware inherited
  `RoleClosure` (self, INHERIT paths, implicit database-owner hop), a current
  superuser enumerates live role entries. No copied membership graph or cached
  name set; partial rows are discarded on cancellation.
- `character_sets`: one current-database UTF8 row; encoding and locale facts
  are shared with `pg_database`.

## Domain constraints

`information_schema.domain_constraints` has exactly eight ordered nullable
domain columns: `constraint_catalog`, `constraint_schema`, `constraint_name`,
`domain_catalog`, `domain_schema`, `domain_name` are
`information_schema.sql_identifier` over NAME; `is_deferrable` and
`initially_deferred` are `information_schema.yes_or_no` over VARCHAR(3).
Logical attribute typmods are -1, with C collation; real rows have eight
non-NULL cells. Both catalogs name the current database, and both flags are NO
for current CHECK/NOT NULL definitions. Empty, false and NULL predicates retain
the same description without placeholder rows.

Rows project actual domain-owned CHECK and own NOT NULL definitions. Inherited
definitions remain on their original domain; unconstrained domains, table
CHECKs and name-like expression text manufacture no rows. Distinct domains can
share a constraint name. NOT VALID CHECKs appear normally; validation changes
`pg_constraint.convalidated`, not this view. The released view has no expression,
validation, enforcement or constraint-kind columns.

The PUBLIC-readable relation filters each domain by enabled owner membership
or effective type USAGE, including direct, inherited and PUBLIC grants and
actual superuser authority. Schema USAGE alone is insufficient, while type
USAGE without schema USAGE is sufficient for this metadata view. Search paths
and TEMP ownership add no filters. NOINHERIT/SET-only membership does not enable
ownership until SET ROLE. Hidden domains are omitted successfully, not exposed
as NULL identities or permission errors.

Fresh direct/prepared and held executions use current catalog/role captures
after supported ADD/DROP/VALIDATE, NOT NULL, rename, schema move, owner/ACL and
domain-drop changes. Failed DDL preserves prior rows. Native replay rebuilds
the metadata from real definitions; returned eager native rows retain their
original data/types. Existing ALTER DOMAIN explicit-transaction and validation
boundaries remain unchanged; no adjacent DDL or information-schema view is added.

`info_domain_constraints.go` reads captured type records directly, not a
reconstructed pg_constraint query or inherited recipe traversal. Source-owned
sql_identifier/yes_or_no cells retain canonical base-generation authority.
The engine pairs catalog/role/session authority, validates exact source
identity and wraps rows through the ordinary catalog source path. Successful
empty capture is nonnil; cancellation or malformed authority/shape returns no
partial rows. Native relation identity16211 is independent of PostgreSQL's
allocator-dependent view OID.
See [selected proof and smoke](../../../reference/information_schema/README.md#domain-constraints).

## Information-schema sequence definitions

`info_sequences.go` projects actual immutable SequenceOptions using one paired
catalog/role capture with the current principal, database and physical TEMP owner.
The normal catalog source path owns registration, PUBLIC SELECT, source-owned
domain carriers, origin attributes and existing row/array associations.
Visibility is enabled ownership OR any relation-level SELECT, UPDATE or USAGE;
schema USAGE and search_path are not filters. Identity sequences and foreign
physical TEMP objects are excluded; ordinary AUTO ownership is eligible.
The producer does not read generators or acquire sequence-value locks. Fresh
held executions capture current options and permissions; returned eager native
Rows remain unchanged. Errors and cancellation publish no partial prefix.

## Column metadata

`info_columns.go` owns the 44-column nullable domain projection and validates
its exact source identity, canonical domain usages and physical base generation
before enumeration. It reads the paired immutable catalog/role roots and source
attribute facts, not query results or stored rows. Real r/v/f/p descriptors
contribute their live positive stable attributes; missing provider attributes,
dropped columns and other relation kinds do not manufacture rows. Ordinal,
DTD identifier and column ACL address all use the same stable attribute.

Visibility is enabled ownership or effective SELECT, INSERT, UPDATE or REFERENCES
on that column, after excluding another physical session's TEMP namespace.
Schema USAGE, search path, row security and sequence ACLs do not filter metadata.
Type dimensions use the declared type's immediate domain base exactly once;
domain-owned NOT NULL is distinct from recursive conversion constraints.
Actual nondefault collations retain their catalog identities. Display uses the
captured stored deparser; identity options come from immutable definitions,
never generators. Metadata reads never evaluate default/generated expressions.

The runtime's updatability capability is structural and ACL-independent:
view/rule recursion maps frozen attributes to the current captured layout,
and foreign capability inspects registered interfaces without callbacks or
write binding. Successful empty captures remain typed, nonnil sources;
cancellation, stale authority and display errors discard every partial row.

## Domain definitions

`info_domains.go` projects genuine concrete domain descriptors through the
ordinary PUBLIC-readable catalog source and its 27 nullable logical-domain
columns. One paired catalog/role/session capture supplies enabled owner or
effective domain USAGE visibility, immediate base identity and typmod,
creation-time copied default text, and the domain's collation. Schema USAGE,
search paths and base-type visibility add no row filters. The immutable
effective type-name path from that same inquiry controls qualification of
nonspecial base names and array element names; parser-special spellings stay
stable. Predefined domains use the same authority as user domains; constraints
neither multiply rows nor add columns. Fresh held executions capture current
definitions, privileges and naming paths; returned rows retain their original
values and descriptor generations.
Malformed source/base/collation authority and cancellation return no prefix.

## Table metadata

`info_tables.go` uses the ordinary PUBLIC-readable source/type path and twelve
nullable source-owned domain columns. Capture validates the source layout,
domain/base generations and column origins before projecting actual represented
relations. Classification comes from `ClassKind`; a physical session's TEMP
namespace takes precedence, and peer TEMP namespaces are excluded.

Tables and views share enabled-ownership and effective relation/live-column
privilege visibility. Schema USAGE and search paths add no metadata filter;
MAINTAIN alone does not disclose a relation. Typed-table identity comes from
the captured `OfType` descriptor and namespace, not an implicit row type.
Insertability reuses structural view/rule capabilities and registered foreign
modification interfaces without executing relation bodies or foreign callbacks.
The PostgreSQL-unimplemented fields remain genuine typed NULLs.

Each held execution captures current definitions and authorization. Returned rows
retain their original values and domain authority; an empty capture is nonnil,
and invalid authority or cancellation publishes no partial result.

## Information-schema views

`info_views.go` projects ten nullable source-owned domain columns through the
ordinary source/type path: three `sql_identifier` identities, definition and
check option as `character_data`, then five `yes_or_no` flags. Empty results
retain the same description. Every represented `ClassKind` v relation is a
candidate except another physical session's TEMP relation.

Visibility is enabled owner membership or any supported relation DML/reference/
trigger privilege, or any supported privilege on a live positive attribute.
MAINTAIN alone does not disclose a view; schema USAGE and search_path do not
filter metadata. ACL-visible nonowners receive a typed NULL definition; enabled
owners receive the current definition. Query bodies are never evaluated.
Fresh held executions capture current definitions, options, role authority and
display settings together; failed capture or cancellation publishes no prefix.

Structural flags do not depend on the viewer's DML rights. The engine bridges
`view.Capabilities`, including nested output subsets, unconditional INSTEAD rules
and the actual registered foreign handler's modification interfaces. Rules are
not trigger facts. The current relation model has no INSTEAD OF triggers, so the
three trigger flags are NO.
Represented catalog relations classified as ordinary tables supply ordinary
base-table structural events; this does not change catalog write admission.

`builtin_views*.go` owns one closed inventory of builtin view definitions,
including `information_schema.columns`, `information_schema.domains`,
`information_schema.tables` and `pg_catalog.pg_rules`, with classification and
per-output capability facts.
Its canonical literal fragments contain explicit object-name and identifier
occurrences. `pg_rules` uses the same definition authority as both consumers;
its `pg_get_ruledef(oid)` reference captures stable routine identity, while
column and relation aliases remain identifier-only annotations. Its structural
update and insert flags are NO; this does not change rule visitation or DDL.
`SummaryViewDefinition` supplies canonical metadata; `BuiltinViewDefinition`
formats those occurrences using the engine's captured naming and identifier
formatters. The same runtime authority renders `pg_views` and `views`.
Routine occurrences capture their original OID from the existing released
identity registry. Represented targets use the current name, namespace and
signature from that same captured catalog before exact-signature qualification;
same-name replacements cannot retarget them. Missing current namespaces fail
the capture rather than inventing labels.
Released references absent from the native catalog remain display-only facts,
never descriptors, stored query payloads or executable providers. Captured
search_path, exact-signature routine shadows and quote_all_identifiers affect
presentation without rewriting column qualifiers as relation names.

## Testing

Package units here run without an engine. Producer behavior tests need an
engine-seeded catalog, so they run on a composed engine in `enginetests/`
(or in `core/` when they reach core's capture runtime) and reach producers
through the real registry; see the
[core Testing section](../core/README.md#system-catalog-producer-contract). Public
catalog SQL belongs in `reference/catalogs` and its sibling catalog packages
(large objects in `reference/object_grants`, settings in
`reference/session_state`); follow
[docs/TESTING.md](../../../docs/TESTING.md) and add `REFERENCE=1` for the oracle.

- Package units: `RUN='^Test(ProducerRegistryLookup|PgClassPartitionTree|CollationActualVersionProviders)$'`
- Registry coverage (engine): `RUN='^TestCatalogSourceProducerCoverage$'`
- Producer units (engine): `RUN='^Test(PGLargeobject|PGTransform|PGSettings|Info(UserMappings|KeyColumnUsage|ConstraintColumnUsage|RoutineSequenceUsage|CharacterSets))'`
- Information schema: `RUN='^TestReferenceInfo(UserMappings|KeyColumnUsage|ConstraintColumnUsage|RoutineSequenceUsage|CharacterSets)'`
- Large objects: `RUN='^TestReferenceLargeObject'`
- Settings view: `RUN='^TestReferenceStatisticsSettings'`
- Transforms: `RUN='^TestReferencePGTransform'`
- Sequence-definition producer units: `RUN='^TestInfoSequences'`
- Sequence-definition public scenarios: `RUN='^TestReferenceInfoSequences'`
- Column metadata producer units: `RUN='^TestInfoColumns'`
- Column metadata public scenarios: `RUN='^TestReferenceInfoColumns'`
- Domain-definition producer units: `RUN='^TestInfoDomains'`
- Domain-definition public scenarios: `RUN='^TestReferenceInfoDomains'`
- Table metadata producer units: `RUN='^TestInfoTables'`
- Table metadata public scenarios: `RUN='^TestReferenceInfoTables'`
- Routine metadata producer/identity units: `RUN='^TestInfoRoutines'`
- Routine metadata public scenarios: `RUN='^TestReferenceInfoRoutines'`
- Routine ACL producer/authority units: `RUN='^TestInfo(RoleRoutineGrants|RoutinePrivileges)'`
- Routine ACL public scenarios: `RUN='^TestReferenceInfo(RoleRoutineGrants|RoutinePrivileges)'`
- UDT grant producer/authority units: `RUN='^TestInfoRoleUdtGrants'`
- UDT grant public scenarios: `RUN='^TestReferenceInfoRoleUdtGrants'`
- SQL-feature producer/authority units: `RUN='^TestInfoSQLFeatures'`
- SQL-feature public scenarios: `RUN='^TestReferenceInfoSQLFeatures'`
- Usage grant producer/authority units: `RUN='^TestInfoRoleUsageGrants'`
- Usage grant public scenarios: `RUN='^TestReferenceInfoRoleUsageGrants'`
- SQL-sizing producer/authority units: `RUN='^TestInfoSQLSizing'`
- SQL-sizing public scenarios: `RUN='^TestReferenceInfoSQLSizing'`
- SQL-part producer/authority units: `RUN='^TestInfoSQLParts'`
- SQL-part public scenarios: `RUN='^TestReferenceInfoSQLParts'`

### Domain constraints

```sh
make check RUN='^TestInfoDomainConstraints(Producer|Capture|Cancellation)$'
make check RUN='^TestCatalogSourceProducerCoverage$'
make check RUN='^TestReferenceInfoDomainConstraints(Shape|Population|Visibility|Lifecycle|Diagnostics|NativeControls|FeatureSmoke)$'
make check RUN='^TestReferenceInfoDomainConstraints(Shape|Population|Visibility|Lifecycle|Diagnostics|NativeControls|FeatureSmoke)$' REFERENCE=1
```

