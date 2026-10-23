# internal/engine/pgcatalog

Row producers for the system relations: the `pg_catalog` tables and views and
the producer-backed `information_schema` views (`table_privileges`,
`data_type_privileges`, `user_mappings`, `key_column_usage`,
`constraint_column_usage`, `column_column_usage`, `routine_sequence_usage`,
`character_sets`, `enabled_roles`, `view_table_usage`, `domain_constraints`,
`sequences`, `role_column_grants`, `columns`, `domains`). Each
producer is a pure function from a captured catalog, a paired role read and a
narrow runtime capability to rows. Orchestration (per-OID catalog choice,
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
- **Provenance.** `Input.Owner` is the engine's stable `*catalog.Owner`; its
  `Domain`, `Principals` and `ACLAuthority` are compared as the same pointers,
  never as equal names or numbers. `Catalog.Owner()`, `Ref.Domain()`,
  `Roles.BelongsTo(Owner.Principals)` and `Source` identity must agree before
  rows are produced.
- Absent and explicit-empty ACLs stay distinct: `ClassACL` returns NULL for an
  absent ACL and `{}` for an empty one.
- Information-schema producers emit genuine source-owned domains, not bare
  primitive carriers. `role_column_grants` uses `sql_identifier`,
  `character_data` and `yes_or_no` from the captured type catalog.
  Numeric metadata likewise retains genuine `cardinal_number` domain values.
- `role_column_grants` captures one catalog/role frontier and projects actual
  table/column ACL origins on live positive attributes. Its raw-origin UNION
  includes the grant-option bit before ownership determines grantability, so
  identical final rows can retain multiplicity. Visibility uses enabled grantor
  or enabled named grantee, including PUBLIC origins with an enabled grantor;
  it does not reuse table/column data-access predicates. Membership closures
  and grantee/owner authority are cached within that capture.
- `pg_type` composes user-domain facts through exact captured immediate-base
  providers, including nominal-only domains and reciprocal arrays. Metadata
  admission does not grant executable codec or storage authority; foreign
  same-OID base generations are not interchangeable.

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
   and implement it on the engine's `engineCatalogRuntime`. If the engine must
   choose a different catalog, role read or unlock policy for the OID, add a
   case to `openCatalogSourceLocked` in the engine.

## Runtime capability

`Runtime` is the narrow interface the engine implements per call
(`engineCatalogRuntime`). Methods take and return values, never engine structs.
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
| `Pinned`, `OperatorSeeded`, `OperatorDependencies`, `TypeProviderProcedure` | pin decisions, the builtin operator inventory and operator dependencies owned by `routine`, extension type providers |
| `VisitSequenceDefinitions` | sequence definitions with the statement's journal overlay (`pg_sequence`) |

## Exported helpers

The engine calls `ClassKind`, `ClassACL`, `AttributeMetadata`,
`HasSystemAttributes`, `AttributeNumber`, `InitializeAttributes`,
`CollationActualVersion`, `DatabaseCollationActualVersion`,
`DescriptorComment`, `ForeignDependencies`, `VirtualViewWriteError`,
`LockRows`, `CapturePGSequences` and the OID constants (`LocksOID`,
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
  owns its enum-array buffers and never publishes partial canceled rows.
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
See [selected proof and smoke](../../../reference/catalogs/README.md#domain-constraints).

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

## Testing

Package units here run without an engine. Producer behavior tests need an
engine-seeded catalog, so they live in `internal/engine` and reach producers
through the real registry (`catalog_source_producers_test.go`); see the
[engine Testing section](../README.md#system-catalog-producer-contract). Public
catalog SQL belongs in `reference/catalogs` (large objects in
`reference/object_grants`, settings in `reference/session_state`); follow
[docs/TESTING.md](../../../docs/TESTING.md) and add `REFERENCE=1` for the oracle.

- Package units: `RUN='^Test(ProducerRegistryLookup|PgClassPartitionTree|CollationActualVersionProviders|InfoRoutineSequenceUsageSpecificName)$'`
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

### Domain constraints

```sh
make check RUN='^TestInfoDomainConstraints(Producer|Capture|Cancellation)$'
make check RUN='^TestCatalogSourceProducerCoverage$'
make check RUN='^TestReferenceInfoDomainConstraints(Shape|Population|Visibility|Lifecycle|Diagnostics|NativeControls|FeatureSmoke)$'
make check RUN='^TestReferenceInfoDomainConstraints(Shape|Population|Visibility|Lifecycle|Diagnostics|NativeControls|FeatureSmoke)$' REFERENCE=1
```

