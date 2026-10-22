# internal/engine/pgcatalog

Row producers for the system relations: the `pg_catalog` tables and views and
the `information_schema` views `table_privileges`, `data_type_privileges`,
`user_mappings`, `key_column_usage` and `routine_sequence_usage`.
Each producer is a pure function from a captured catalog, a paired role read
and a narrow runtime capability to rows. The package sits at L5 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It
imports `catalog`, `authorization`, `largeobject`, `lock` (lock records), `expr`,
`ids`, `sqltype`, `sqlstate`, `parser` and the standard library. It never imports the
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
  Successful dynamic captures return a nonnil empty row slice when no rows
  exist. This denotes an actual empty anonymous source; nil is not a substitute
  for an absent retained data view. Maintenance progress/statistics follow the
  same convention without inventing a placeholder row.
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
  `pg_am` enumerates live global method descriptors (four bootstrap rows plus
  real custom definitions), with owned contiguous typed cells per capture.
  `pg_class.relam` retains declared custom index identity. Family→method AUTO
  and method→handler NORMAL dependencies use shared selectors; references to
  pinned stock handlers are suppressed, not fabricated as pg_depend rows.

`pg_largeobject` (2613) enumerates actual immutable LO stored chunks, not
zero-filled logical reads. Its producer validates source/descriptor provenance
and copies each borrowed chunk once into an owned BYTEA value; holes and short
tails are never materialized. It does not consult per-object ACLs: ordinary
table/column SELECT admission protects this non-public relation. The engine
selects only2613's active LO READ root, falling back to the statement inquiry
frontier, so fresh SQL bodies see argument effects while outer scans remain
frozen. Existing metadata2995 capture remains independent and unchanged.

Behavioral units cover retained/fresh captured roots, cancellation and ordinary
provenance errors; public SQL contracts and smoke belong in
[reference/catalogs](../../../reference/catalogs/README.md#pg_largeobject-page-projection).

```sh
make check RUN='^TestPGLargeobject(Projection|CapturedRoots|CanceledCapture)$'
make check RUN='^TestCatalogSourceProducerCoverage$'
```


## Maintenance projections

The released30-column table-statistics views project committed logical DML and
manual VACUUM facts, not query-time COUNT(*) or physical scan estimates. The
15-column progress view captures real active phases and authenticated-owner
visibility under the engine capture lock. Unsupported page/dead-item metrics,
scan/I/O/duration statistics and absent analyze timestamps remain typed NULL.
No row is created while waiting for a relation lock or after work completes.
These four sources use ordinary relation/row/array identities; their generated
PostgreSQL OIDs are not cross-backend numeric constants.
`pg_class`/`pg_database` expose genuine logical freezing bounds without changing
unimplemented physical page/map/MultiXact fields.
See [maintenance proof](../../../reference/catalogs/README.md#vacuum-oracle-first-contract).

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

Maintenance-aware `pg_class`/database/statistics projections require an adapter
backed by the actual engine, including direct package-seam fixtures. An empty
adapter is not an initialized maintenance capture; callers supply their owner
rather than masking missing authority with panic suppression or invented facts.

| Methods | Why they are engine-owned |
|---|---|
| `CursorCount`, `VisitCursors` | session cursor registry (`pg_cursors`) |
| `SessionSettings` | coherent registry-wide typed current/reset/source capture under one physical-session mutex (`pg_settings`) |
| `PreparedCount`, `VisitPreparedTransactions` | prepared-transaction registry, assigned XIDs, published role names |
| `TimezoneInput`, `DatabaseName`, `TemporaryOwner`, `CurrentPrincipal` | statement input environment, session database, physical TEMP owner, current user |
| `ValidateCapture`, `Effective`, `CanUse`, `SelectColumns`, `ForeignServerVisible` | error-bearing privilege checks through the statement's authorization view |
| `ValidateParameter` | setting registry validation of parameter ACL rows |
| `ValidateACL`, `ObjectACL` | ACL validation and raw object ACLs (`access` is not imported here) |
| `ValidateType` | type descriptor validation, including installed extension types |
| `TypeOutput` | regclass, regrole and aclitem formatters for type defaults |
| `Maintenance`, `DatabaseFrozenXID`, `VisitVacuumProgress` | immutable logical maintenance/statistics facts and authenticated active-work redaction |
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

`CollationActualVersion` reads actual provider authority, never recorded
`Version`: supported builtin locales return `1`; libc C/POSIX and unavailable
native ICU version data return nil (SQL TEXT NULL). Default delegates to the
native libc/C database, also versionless. Inquiry-only ICU NULL does not admit
ICU creation/execution or provide an installed system inventory. Other invalid
or unsupported executable definitions retain their semantic errors.

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
- The `Runtime` bodies over stored definitions, which forward to `stored`:
  `ViewDefinition` (`stored.ViewDefinitionSQL`), `ExpressionReferences`,
  `ViewReferences`, `IndexReferences` and `PartitionReferences`
  (`stored.*DependRefs`), and `Pinned` (`stored.DependPinned`, which also
  reads unpublished definition state).

## Large-object metadata

`pg_largeobject.go` projects genuine large-object descriptors into relation2995:
exact object OID, effective creator's owner OID and raw `ClassACL`. Absent ACLs
are NULL, explicit-empty ACLs are `{}`, and populated ACL order, grantors and
grant options are preserved. The ordinary PUBLIC-readable catalog exposes all
metadata rows without per-object privilege filtering or ACL redaction.
`ValidateACL` validates integrity; it does not authorize byte access.
The same transaction-visible descriptors drive real create, grant/revoke and
unlink operations; no metadata-only object store or payload relation is added.

## Transform metadata

`pg_transform.go` owns the relation3576 producer. No implemented transforms
exist: casts/codecs, SQL routines and extension availability are not providers.
It checks cancellation before shape, then the five catalog-owned carriers
OID/OID/OID/REGPROC/REGPROC, and returns a nonnil empty anonymous source.
`Specs` owns its nonnullable columns; ordinary registration reuses the existing
row/array descriptors. Index-presence and pg_tables inclusion remain separate
metadata predicates. No new runtime interface or provider registry is needed.

## Testing
Public metadata acceptance belongs in `reference/object_grants`, not internal
SQL tests. It checks empty/populated descriptors, unrelated-role visibility,
byte denials and one retained statement through drop/recreation. Existing
Transactions and Restore cover private visibility and native-control replay.
See the [owning Testing section](../../../reference/object_grants/README.md#large-object-metadata)
for the public feature smoke and independent outcomes. Selected proof:

```sh
make check RUN='^TestReferenceLargeObjectMetadata(Shape|Visibility|Lifecycle)$'
make check RUN='^TestReferenceLargeObjectMetadata(Shape|Visibility|Lifecycle)$' REFERENCE=1
make check RUN='^TestReferenceLargeObject(Privileges|Transactions|Prepared|Restore)$'
make check RUN='^TestReferenceLargeObject(Privileges|Transactions|Prepared|Restore)$' REFERENCE=1
```


Transform producer invariants and initialized ownership are covered by engine
units; public SQL/API coverage stays in reference/catalogs:

```sh
make check RUN='^TestPGTransform(Capture|Ownership)$'
make check RUN='^TestReferencePGTransform(Inventory|Shape|CatalogLinks|Prepared|Diagnostics|Permissions|Lifecycle)$'
make check RUN='^TestReferencePGTransform(Inventory|Shape|CatalogLinks|Prepared|Diagnostics|Permissions|Lifecycle)$' REFERENCE=1
```


`info_user_mappings.go` emits only real mapping identities from the captured
catalog and paired roles. Server inherited-owner OR effective-USAGE authority
exposes all attached mappings. It wraps inline NAME payloads in the genuine
`sql_identifier` domain; source metadata remains available for empty results.
Successful empty captures return a nonnil empty row slice so the executor
recognizes an anonymous source, not an absent physical source.
It never copies options or reuses `ForeignServerVisible`, which governs the
distinct `pg_user_mappings` option-redaction path.

```sh
make check RUN='^TestInfoUserMappingsProducer$'
make check RUN='^TestReferenceInfoUserMappings(Shape|Visibility|Lifecycle|Diagnostics|Smoke)$'
make check REFERENCE=1 RUN='^TestReferenceInfoUserMappings(Shape|Visibility|Lifecycle|Diagnostics|Smoke)$'
```

`key_column_usage.go` emits the nine documented columns from actual primary,
unique and foreign-key descriptors on ordinary and partitioned tables. Capture
uses coherent catalog/role/session authority and genuine `sql_identifier` and
`cardinal_number` domain values, including typed NULL unique positions for p/u.
Foreign-key positions follow the actual supporting index's ordered Keys, never
another equivalent index, constraint-name lookup or INCLUDE payload. Each
constraint occurrence survives duplicate names. Owner inheritance or effective
SELECT/INSERT/UPDATE/REFERENCES on each constrained column controls visibility;
partial keys retain their original ordinals. Other-session TEMP is excluded.
Successful empty captures are nonnil; cancellation and invalid authority expose
no prefix. Existing DDL, transactions and logical controls own all population.

```sh
make check RUN='^Test(InfoKeyColumnUsage(Producer|Capture|Cancellation)|CatalogSourceProducerCoverage)$'
make check RUN='^TestReferenceInfoKeyColumnUsageFeatureSmoke$'
make check RUN='^TestReferenceInfoKeyColumnUsageFeatureSmoke$' REFERENCE=1
```



`info_routine_sequence_usage.go` reads direct stored SQLBody relation references
from the captured procedure descriptors, resolves only real sequences in the
same catalog root, and validates nonzero sequence attributes with the captured
relation's `HasAttribute` authority. Distinct stored attribute ObjectRefs may
refer to one sequence, so a lazy per-routine sequence-identity set implements
distinct pairs. Every reference is validated before duplicate suppression;
invalid attributes and foreign-domain/stale-epoch refs remain errors.
Its sole visibility predicate is enabled sequence-owner role inheritance;
routine/schema/sequence ACL checks
are deliberately absent. Current descriptor names and a UTF8 prefix-clipped,
OID-suffix-preserving specific name form nine genuine `sql_identifier` values.
The engine supplies paired catalog/role provenance and the session database name.
Empty captures are nonnil; cancellation and invalid capture/dependency identities
return no prefix rows. No SQL-body parsing, recursive expansion, pg_depend
materialization or second lifecycle registry is involved.

```sh
make check RUN='^TestInfoRoutineSequenceUsage(Producer|SpecificName)$'
make check RUN='^TestReferenceInfoRoutineSequenceUsage(Shape|Population|SequenceColumns|Visibility|Lifecycle|Diagnostics|Controls)$'
make check RUN='^TestReferenceInfoRoutineSequenceUsage(Shape|Population|SequenceColumns|Visibility|Lifecycle|Diagnostics|Controls)$' REFERENCE=1
```

Package units run without an engine:

```sh
make check RUN='^(TestProducerRegistryLookup|TestPgClassPartitionTree)$'
make check RUN='^TestCollationActualVersionProviders$'
```

Producer behavior is tested in the engine package, because those tests need a
catalog seeded by the engine. They call producers through the real registry
(`internal/engine/catalog_source_producers_test.go`). Their selectors, the
reference selections and the race checks are in the
[engine README](../README.md#system-catalog-producer-contract).

The existing `pg_settings` producer projects registry metadata and one coherent
typed snapshot into the unchanged17-column schema/SettingsOID. It owns enum-array
buffers, releases no caller-owned locks, and publishes no partial canceled rows.
SQL mutation remains the existing read-only virtual-view boundary.

```sh
make check RUN='^TestPGSettings(ProviderCapture|CoherentCapture|CatalogACLRecheck)$'
make check RUN='^TestReferenceStatisticsSettings(Metadata|Consumers|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceStatisticsSettings(Metadata|Consumers|Controls|FeatureSmoke)$'
```

