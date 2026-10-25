# reference/partitions

Declarative partitioning: RANGE/LIST/HASH definitions, DML routing,
ATTACH/DETACH, pruning (including constraint exclusion over inheritance trees),
partitioned UNIQUE/PRIMARY KEY and exclusion constraints, and inherited partition
CHECK/NOT NULL constraints. Related families: table core, constraints, foreign
keys and foreign tables in [reference/tables](../tables/README.md); inheritance,
typed tables and LIKE in [reference/inheritance](../inheritance/README.md);
partitioned indexes in `reference/indexes`; ALTER TABLE actions in
`reference/alter`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md) and the shared table rules in
[reference/tables](../tables/README.md#testing). `TestMain` uses default options.

- Scenario helpers: `referenceRangeScenario` (`range_partition_fixture_test.go`),
  `referencePruningScenario` (`partition_pruning_fixture_test.go`),
  `partitionConstraintCase` (`partition_constraints_fixture_test.go`),
  `partitionExclusionScenario`/`Case` (`partition_exclusion_fixture_test.go`) and
  `partitionInheritedScenario` (`partition_inherited_fixture_test.go`).
- Query results do not prove that pruned branches or ATTACH validation scans
  were skipped. Engine units assert that natively; on PostgreSQL, ATTACH scan
  elision is read from `pg_stat_user_tables.seq_scan` on the same session after
  `pg_stat_force_next_flush()` and `pg_stat_clear_snapshot()`, calibrated against
  an ATTACH without proof (`partition_attach_proof_test.go`).

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- RANGE partitions: `RUN='^TestReferenceRangePartition'`
- <a id="list-partition-definitions"></a>LIST partitions: `RUN='^TestReferenceListPartition'`
- HASH partitions: `RUN='^TestReferenceHashPartition'`
- Partition DML routing: `RUN='^TestReferencePartitionRouting'`
- ATTACH PARTITION: `RUN='^TestReferencePartitionAttach'`
- DETACH PARTITION: `RUN='^TestReferencePartitionDetach'`
- Partition pruning: `RUN='^TestReferencePartitionPruning'`
- <a id="partitioned-constraints--selected-proof"></a>Partitioned UNIQUE/PRIMARY KEY: `RUN='^TestReferencePartitionConstraints'`
- Partitioned exclusion constraints: `RUN='^TestReferencePartitionExclusion'`
- <a id="inherited-partition-constraints"></a>Inherited partition CHECK/NOT NULL: `RUN='^TestReferencePartitionInherited'`

Engine and parser units: `make check RUN='^Test(PartitionPruning|RangePartition|HashPartition|ListPartition|PartitionAttach|PartitionDetach|PartitionConstraints)'`.
