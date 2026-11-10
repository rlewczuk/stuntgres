# reference/inheritance

Dual-mode scenarios for inheritance, typed tables and CREATE TABLE LIKE.
The package also owns inheritance links and optimizer-statistics declarations,
collection, metadata and logical replay. Table core belongs to
[tables](../tables/README.md#testing), partitioning to
[partitions](../partitions/README.md#testing), and other catalogs to
[catalogs](../catalogs/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md) and the shared table rules in
[reference/tables](../tables/README.md#testing). `TestMain` uses default options.

- Scenario helpers: `inheritanceScenario` (`inheritance_helpers_test.go`) and
  `typedTableScenario` (`typed_helpers_test.go`).
- Roles named by a replayed native Dump are passed as `inheritanceScenario`
  replay roles so they exist on PostgreSQL before the replay.
- Catalog scenarios retain the shared catalog identity, cleanup and native-control
  rules in [reference/catalogs](../catalogs/README.md#testing).
- `pg_inherits_helpers_test.go` owns complete edge bags, independent identity
  lookup and held metadata reads. Statistics scenario and assertion helpers stay
  with their coherent families (`statistics_object_helpers_test.go`,
  `statistics_expression_helpers_test.go`, the ndistinct/dependencies/MCV family
  files and `pg_statistic_ext*_test.go`); shared catalog helpers remain in
  `reference/fixture`. Direct, fresh-prepared, held, native-control and actual
  PostgreSQL dump-replay routes use the same original assertions.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Inheritance: `RUN='^TestReferenceInheritance'`
- Typed tables: `RUN='^TestReferenceTypedTable'`
- LIKE: `RUN='^TestReferenceTableLike'`
- Inheritance links: `RUN='^TestReferencePGInherits'`
- Statistics catalogs: `RUN='^TestReferencePGStatisticExt'`
- Statistics lifecycle/collection: `RUN='^TestReferenceStatistics'`
- Attribute restore: `RUN='^TestReferenceAttributeStatistics'`
- Relation restore: `RUN='^TestReferenceRelationStatistics'`

Statistics expectations are independently prescribed from released sources and
real controlled populations. Decode physical test images independently of production
codecs. Native full-data facts, bounded sample estimates and PostgreSQL physical
measurements are distinct; raw images are not cross-backend byte goldens.

Replay actual emitted declaration SQL on PostgreSQL. Retained TEMP objects need
a live physical owner; independent destinations use their own recorded pool and recorder.

Establish namespace USAGE separately from MAINTAIN/ownership. Release canceled
workers and return their disposable connections before subsequent visibility
assertions. Complete warnings remain part of failed-step diagnostics.
