# internal/engine/pgcatalog

Row producers for the system relations: the `pg_catalog` tables and views and
the producer-backed `information_schema` views (`table_privileges`,
`data_type_privileges`, `user_mappings`, `key_column_usage`,
`constraint_column_usage`, `routine_sequence_usage`, `character_sets`). Each
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
- Information-schema producers emit genuine `sql_identifier` and
  `cardinal_number` domain values, not bare NAME or INT4.

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
| `ViewDefinition`, `IndexDefinition` | view and index definition rendering |
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

## Relations with special rules

- `pg_am`: live global method descriptors with owned typed cells per capture;
  family→method and method→handler dependencies use shared selectors, and
  references to pinned stock handlers are suppressed, not fabricated.
- `pg_largeobject` (2613): actual immutable stored chunks, each borrowed chunk
  copied once into an owned BYTEA; holes are never materialized and per-object
  ACLs are not consulted (ordinary relation SELECT protects it).
- `pg_largeobject_metadata` (2995): object OID, creator's owner OID and raw
  `ClassACL`, without per-object filtering or redaction.
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
