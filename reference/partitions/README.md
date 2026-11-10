# reference/partitions

Dual-mode scenarios for declarative partition definitions, routing, hierarchy
maintenance, constraints and source locking. Table core belongs to
[tables](../tables/README.md#testing), inheritance/typed/LIKE families to
[inheritance](../inheritance/README.md#testing), index definitions to
[indexes](../indexes/README.md#testing), and table actions to
[alter](../alter/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md) and the shared table rules in
[reference/tables](../tables/README.md#testing). `TestMain` uses default options.

- Scenario helpers: `referenceRangeScenario` (`range_partition_fixture_test.go`),
  `referencePruningScenario` (`partition_pruning_fixture_test.go`),
  `partitionConstraintCase` (`partition_constraints_fixture_test.go`),
  `partitionExclusionScenario`/`Case` (`partition_exclusion_fixture_test.go`) and
  `partitionInheritedScenario` (`partition_inherited_fixture_test.go`).
- Constraint/index cancellation prepares held native statements before taking the
  blocker and arming the 50 ms statement timeout. Its timed borrower is physically
  discarded, including on failure, never reset or returned for later assertions.
  PostgreSQL cancellation retains the admitted server wait and exact user-request
  diagnostic; post-cancellation checks run independently of the native timed owner.
  Exclusion-concurrency timeout probes use the same separate-borrower lifecycle;
  their direct route stays direct and their prepared route holds the INSERT before
  the blocker and timer. The unarmed peer retains the complete recovery checks.
- Query results alone do not prove pruning or scan elision. Keep physical
  execution observations separate from public result assertions.
- Concurrent-detach scenarios use the fixture's committed pending-edge and
  virtual-completion observers, not ordinary DETACH's parent-AX fairness probe.
  Held statements are prepared before admission; every cancellation is joined
  before inspecting the retained graph. Native replacement/replay controls stay
  explicit phases of the same shared SQL scenario.
- Retained rows and held statements follow the shared
  [driver lifetime contract](../../docs/DRIVER.md#statements-and-results).

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- RANGE partitions: `RUN='^TestReferenceRangePartition'`
- LIST partitions: `RUN='^TestReferenceListPartition'`
- HASH partitions: `RUN='^TestReferenceHashPartition'`
- Partition definition introspection: `RUN='^TestReferenceDeparsePartition'`
- Partition key catalog: `RUN='^TestReferencePGPartitionedTable'`
- Partition DML routing: `RUN='^TestReferencePartitionRouting'`
- ATTACH PARTITION: `RUN='^TestReferencePartitionAttach'`
- DETACH PARTITION: `RUN='^TestReferencePartitionDetach'`
- Partition pruning: `RUN='^TestReferencePartitionPruning'`
- Hierarchy column rename: `RUN='^TestReferencePartitionRenameColumn'`
- Partitioned UNIQUE/PRIMARY KEY: `RUN='^TestReferencePartitionConstraints'`
- Partitioned exclusion constraints: `RUN='^TestReferencePartitionExclusion'`
- Constraint timing propagation: `RUN='^TestReferenceDeferredConstraintPropagation'`
- Inherited partition constraints: `RUN='^TestReferencePartitionInherited'`

Engine and parser units: `make check RUN='^Test(PartitionPruning|RangePartition|HashPartition|ListPartition|PartitionAttach|PartitionDetach|PartitionConstraints)'`.
