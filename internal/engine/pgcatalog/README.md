# internal/engine/pgcatalog

Row producers for the system relations: the `pg_catalog` tables and views and
the two `information_schema` views (`table_privileges`, `data_type_privileges`).
Each producer is a pure function from a captured catalog, a paired role read
and a narrow runtime capability to rows. The package sits at L5 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It
imports `catalog`, `authorization`, `lock` (lock records), `expr`, `ids`,
`sqltype`, `sqlstate`, `parser` and the standard library. It never imports the
engine, `access`, `storage` or `visibility`. `parser` is needed because
catalog descriptors store parser enums (foreign-key actions, identity mode,
partition bound values); it is an L0 leaf that `catalog` also imports.

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
  keeps a statement scope or takes a lock. The engine wraps the rows.
- Rows follow the column order of `Input.Source`, with each cell typed by that
  column's descriptor. Producers check that the source has the expected shape
  and types and fail otherwise.
- Producers check `Context` between descriptors and return its error.
- **Provenance.** `Input.Owner` is the engine's stable `*catalog.Owner`. Its
  `Domain`, `Principals` and `ACLAuthority` are compared as the same pointers,
  never as equal names or numbers. `Catalog.Owner()`, `Ref.Domain()`,
  `Roles.BelongsTo(Owner.Principals)` and `Source` identity must agree before
  rows are produced. Absent and explicit-empty ACLs stay different: `ClassACL`
  returns NULL for an absent ACL.

## Registry

`catalog_source_producers.go` holds one process-initialized array of
`Registration{OID, Produce}`, sorted by relation OID and never changed.
`ProducerFor(oid)` finds a producer by binary search; `Registrations()`
returns a copy for coverage checks. The array does not contain `pg_locks`
(`LocksOID`) or `pg_sequences` (12048): the engine handles those two itself.

`catalog_source_specs.go` holds `Specs`, the ordered descriptor metadata for
every system relation (OID, namespace, name, columns with type, typmod,
dimensions, nullability and collation, and flags). The spec order is the
descriptor registration order and differs from the registry order.

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
(`engineCatalogRuntime`, §4.1 pattern 3). Methods take and return values; none
return engine structs. Building the adapter reads nothing; only the selected
producer calls the methods it needs. Rule facts (`catalog.RuleFact`), view
options and policy conditions are read from the catalog payloads directly
(`Relation.HasRules`, `Relation.VisitRules`, `ViewPayload.Options`,
`PolicyDefinition.VisitExpressions`), not through `Runtime`.

| Methods | Why they are engine-owned |
|---|---|
| `CursorCount`, `VisitCursors` | session cursor registry (`pg_cursors`) |
| `SessionSettings` | one coherent physical-session `row_security` tuple (`pg_settings`) |
| `PreparedCount`, `VisitPreparedTransactions` | prepared-transaction registry, assigned XIDs, published role names |
| `TimezoneInput`, `DatabaseName`, `TemporaryOwner`, `CurrentPrincipal` | statement input environment, session database, physical TEMP owner, current user |
| `ValidateCapture`, `Effective`, `CanUse`, `SelectColumns`, `ForeignServerVisible` | error-bearing privilege checks through the statement's authorization view |
| `ValidateParameter` | setting registry validation of parameter ACL rows |
| `ValidateACL`, `ObjectACL` | ACL validation and raw object ACLs (`access` is not imported here) |
| `ValidateType` | type descriptor validation, including installed extension types |
| `TypeOutput` | regclass, regrole and aclitem formatters for type defaults |
| `ExpressionTree` | native structural `pg_node_tree` output of bound expressions |
| `VisitCheckAttributes`, `ExpressionReferences`, `ViewReferences`, `IndexReferences`, `PartitionReferences`, `IndexSimpleAttribute`, `IndexKey` | walks over engine plan and expression nodes |
| `ForeignKeyComparisons` | the binder's foreign-key comparison operators (`pg_constraint`) |
| `ViewDefinition`, `IndexDefinition` | view and index definition rendering |
| `Pinned`, `OperatorSeeded`, `OperatorDependencies`, `TypeProviderProcedure` | pin decisions; the builtin operator inventory and operator dependencies owned by `routine` (L10, above this package); extension type providers (`pg_depend`, `pg_operator`) |
| `VisitSequenceDefinitions` | sequence definitions with the statement's journal overlay (`pg_sequence`) |

## Exported helpers

The engine calls these directly: `ClassKind`, `ClassACL`, `AttributeMetadata`,
`HasSystemAttributes`, `AttributeNumber`, `InitializeAttributes`,
`CollationActualVersion`, `DatabaseCollationActualVersion`,
`DescriptorComment`, `ForeignDependencies`,
`VirtualViewWriteError`, `LockRows` and the OID constants (`LocksOID`,
`CursorsOID`, `SettingsOID`, `TypeOID`, ...). Engine unit tests also call
`CollectDepend`, `ClassRow`, `AttributeRow`, `TypeFacts`, `ConstraintOperator`
and a few other row helpers, because they check producer behavior against an
engine-seeded catalog.

## What stays in the engine

- `openRelationSourceLocked`, `openCatalogSourceLocked` (per-OID choice of
  catalog, roles, presentation and unlock timing) and `captureCatalogSourceRows`
  (the only place rows become a `querySource`).
- `engineCatalogRuntime`, the `Runtime` adapter.
- `pg_locks` capture (`pg_locks_capture.go`): it releases and retakes
  `Engine.mu` and never takes the lock manager mutex under it. Rows come from
  `LockRows`.
- `pg_sequences` lazy completion, which acquires sequence locks through the
  statement scope, and the `pg_sequence` journal overlay.
- Descriptor registration during catalog seeding (`initializeCatalogSources`,
  `catalogSourceDescriptor`, `registerCatalogSource`), which reads `Specs`.
- `pgDependPinned` (`catalog_pinning.go`), which also reads unpublished
  definition state; producers reach it through `Runtime.Pinned`.

## Testing

Package units run without an engine:

```sh
make check RUN='^(TestProducerRegistryLookup|TestPgClassPartitionTree)$'
```

Producer behavior is tested in the engine package, because those tests need a
catalog seeded by the engine. They call producers through the real registry
(`internal/engine/catalog_source_producers_test.go`). Their selectors, the
reference selections and the race checks are in the
[engine README](../README.md#system-catalog-producer-contract).
