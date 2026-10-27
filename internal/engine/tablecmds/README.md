# internal/engine/tablecmds

The table, sequence and VACUUM statement executors
([`docs/ENGINE-SPLIT.md`](../../../docs/ENGINE-SPLIT.md) §3, L16): ALTER TABLE
with its action passes (constraints, defaults, generated columns, column
types, identity, foreign keys, NOT NULL, INHERIT and NO INHERIT), ALTER
TABLE ... ALTER COLUMN DEFAULT, CREATE TABLE (LIKE, typed, PARTITION OF),
DROP TABLE, RENAME of relations, columns and constraints, and SET SCHEMA;
CREATE, ALTER and DROP SEQUENCE with OWNED BY, and COMMENT ON SEQUENCE;
VACUUM; the table Restore repairs and the Restore identity check; and the
`TableHost`, `AlterHost` and `TypeHost` family hosts. The decisions are
[`table`](../table/README.md)'s, [`sequence`](../sequence/README.md)'s,
`index`'s and `partition`'s; this package orchestrates them over core's
runtime.

## Layering

It imports [`core`](../core/README.md) and the decision packages, never the
root or another family. The root composition (dispatch and Restore hooks),
[`indexcmds`](../indexcmds/README.md),
[`partitioncmds`](../partitioncmds/README.md),
[`typecmds`](../typecmds/README.md), [`viewcmds`](../viewcmds/README.md) and
[`schemacmds`](../schemacmds/README.md) import it; by rule 6 every family
except `rolecmds` may. Code that tables share with those families lives here:
renames, SET SCHEMA, typed sources, the IF EXISTS skip, the ALTER column
candidates and type rebinds, `PrepareAlterTypeLocked`, and the hosts,
`TypeHost` among them (`typecmds` and `schemacmds` build it, with no arrow
between them).

Stays in core: FK enforcement and actions, the DROP pipeline (`DropProvider`),
TEMP preservation, relation admission and publication.

## Entry points

- Dispatch (`core.Statements.ExecuteLocked`): `AlterTableLocked`,
  `AlterDefaultLocked`, `CreateTableLocked`, `DropTableLocked`,
  `RenameConstraintLocked`, `RenameRelationLocked` and `AlterSchemaLocked`
  are lock-domain continuations: entered with `Engine.Mu` held, they release
  it on every return and run admission waits, provider work and row
  evaluation unlocked.
  `AlterTableLocked` takes a nil physical-work collector for an ordinary
  statement; inheritance supplies its command-local collector so every target's
  metadata is installed privately before any rewrite callback or heap work.
  Physical work retains its original source layout and storage fences, and
  merges only into the latest private journal.
  Sequence prephase admission checks exact grant-time originals without
  transferring effects. Ordinary metadata transfer installs sequence descriptors,
  generators and retirements once before identity or volatile physical recipes;
  AS validation and cache-tail demand retain their earlier ordering.
  VALIDATE resolves after deferred ADD FOREIGN KEY, carries only selected
  identities into unlocked effective-row scans, and publishes through the same
  fenced Finish. Eligible valid roots return without a stage or heap work.
  Historical FK work is one ordered payload: ADD-time history-validating keys
  precede action-ordered MISC selections. ADD NOT VALID is not retroactively
  enrolled in the ADD prefix when MISC later promotes it. Resolution retains the
  incumbent changed-value traversal as an earlier prefix, then deduplicates stable
  child/OID identities without resorting scheduled work into descriptor order.
  Recursive CHECK/NOT NULL validation admits the closure through the incumbent
  non-pure provider path, then uses one metadata/physical/final-validation stage;
  already-valid intermediates relay intent without scanning or replacement.
  Only propagated validity-only actions omit descendant-owner checks.
  Pure validation performs no heap-shape or index-root publication and preserves
  earlier own transaction shape authorities. Its source fences protect heap
  pointer identity while enforced DML may advance tuple generations and roots.
  Descendant NOT NULL names resolve by logical column at actual misc, after that
  descendant's ADD phases, rather than against its old descriptor.
  These are logical row/catalog/transaction guarantees. External server trigger,
  transport and physical-page/WAL/crash-recovery mechanisms remain outside this
  executor; native persistence uses the incumbent logical Dump/Restore controls.
- Dispatch (`core.Statements.Execute`): `ExecuteSequenceDDL`,
  `ExecuteSequenceComment` and `ExecuteVacuum` are entered with nothing held;
  they take `Engine.Mu` in short windows and release it around lock waits.
- Restore hooks: `CaptureRestoredForeignKeys`,
  `ResolveRestoredForeignKeyTargets`, `BindRestoredForeignKeys`,
  `PairRestoredNotNull`, `RestoreNoInheritNotNull`, `RestoreAttributeRank`,
  the physical NOT NULL constructors and foreign-key providers,
  `ValidateRestoredDefault` and `PublishRestoreRepairLocked`;
  `ValidateRestoreIdentityConfiguration` admits only the parameter-free
  configuration of an existing internal sequence.
- Shared with other families: `SchemaTableLocked` (CREATE TABLE inside CREATE
  SCHEMA), `SkipMissingRelationLocked`, `AcquireTypedSources`,
  `AcquirePartitionValidationViewsLocked`, `PendingPartitionDefinition` and
  `PartitionBoundBinder` (`partitioncmds`' ATTACH PARTITION), and the ALTER
  column candidates, `AlterRowEvaluation`, the check and index captures and
  type rebinds (`typecmds`' ALTER TYPE on composites); `RenameRelationLocked`
  and `AlterSchemaLocked` also serve `viewcmds`' ALTER VIEW.

`Engine` (`inheritance_alter_admission.go`) is a defined type over
`core.Engine` that carries only `admitInheritanceAlter`, whose
`(*Engine).admitInheritanceAlter(` frame a read-only reference cancellation
fixture matches. Keep that name.

## VACUUM

`vacuum.go` runs in one `StatementScope` and transaction/ticket; per-target
holds return to lock manager checkpoints outside engine and session locks.
`vacuum_publication.go` rechecks root, storage, shape, authority and gate
tokens before atomic installation; peer DML causes recapture, never SQL
replay. The VACUUM barrier and progress tests park the statement at the
`vacuumCompletion` frame and at `storage`'s scan frames: keep those names.

## ANALYZE

`ExecuteAnalyze` shares positioned maintenance syntax and lock acquisition, not
VACUUM's transaction-block prohibition or row retirement. Relation names resolve
before work; columns validate per target. Each target acquires a real
ShareUpdateExclusive lock, captures statement-visible immutable row roots and
stable attribute/layout authority, then collects outside `Engine.Mu`. Final
publication rechecks epoch, catalog, store, shape, rights and cancellation.
Notices and comparisons execute without engine locks. Virtual generated columns
are skipped without evaluation. ONLY suppresses separate descendant refresh,
not the parent's inherited population.
Each target owns an independent capture at the same statement frontier, rather
than releasing or replacing the statement's query-cache view. Collection uses
the executor's visible-head selection once per physical segment, including own
replacement substitution; a raw union filtered only by LiveVersion would count
replaced committed predecessors twice.

Standalone column facts install per completed target; explicit transactions
record immutable selected-attribute Journal deltas. Maintenance row estimates
and actual analyze count/time update independently and survive rollback for
existing committed relations. No row rewrite or DML counters accompany a
statistics-only publication. Valid BUFFER_USAGE_LIMIT controls are physically
inapplicable; ordinary execution invents no physical progress.
A last-child removal followed by local-only collection clears obsolete inherited
population authority and records inherited-column deletions through the ordinary
canonical/Journal paths, independent of the selected column list. When scanning
a private TRUNCATE successor, ANALYZE also records the scan on that actual heap;
this preserves maintenance ordering through commit and savepoint rollback while
column deltas remain transactional.
Requested ndistinct definitions are captured with the same relation/selection
authority. Scalar and tuple collectors consume one traversal per population.
Every key must occur in the current column selection; ineligible definitions
warn without combining old column facts. Object target zero skips rebuilding
without clearing its payload. Empty populations retain prior payloads. Ordinary
parents collect separate local/inherited facts; partitioned parents collect only
inherited facts and never gain synthetic storage. Tuple values use typed NULL-
inclusive grouping and canonical stable-attnum subsets, not independent-column
products. Selected extended deltas follow the same transactional Journal path.
The full-data tuple scan is deterministic. Relative to PostgreSQL's potentially
sampled estimator, only the numeric technical payload in stxdndistinct is an
approximation on larger populations; small complete populations have exact
counts. Definition identity, built-row existence, inheritance, visibility,
privileges, types and NULL presence are not approximate. The dependencies, MCV
and expression fields remain typed NULL for explicit simple-column ndistinct
objects; this collector does not synthesize those independently owned kinds.



## Hosts

Each embeds core's `DDLHost` with its `{E, Op, CatalogStage}` layout, carries
a compile-time assertion, and runs every method under `Engine.Mu` without
waiting.

| Type | Implements |
| --- | --- |
| `TableHost` (`table_host.go`, `index_host.go`, `partition_host.go`) | `table.Host`, `index.Host`, `partition.Host`, `fdw.Host` |
| `AlterHost{TableHost; Scope}` (`alter_host.go`) | `table.AlterHost`, one per call: the ALTER loop begins its stage lazily |
| `TypeHost{TableHost}` (`typedef_host.go`, `domain_default_snapshot.go`) | `typedef.StageHost`, built by `typecmds` and `schemacmds`; `BindRestoredDomainDefault` binds a trusted Restore domain default snapshot |

`viewcmds`' `ViewHost` embeds `TableHost` too, and `schemacmds` builds
`TableHost` for the foreign-data DDL and `AlterHost` for ALTER FOREIGN TABLE.

## Testing

Tests are external (`package tablecmds_test`) and run on a composed engine:
the root's `engine.New`, `Reset`, `Dump` and `Restore` and the
[`enginetest`](../enginetest/README.md) helpers. `export_test.go` exports the
internals they inspect; add a name there only when the test cannot assert
through SQL or the exported API.

```sh
make check RUN='^TestAlter'                                # ALTER TABLE passes, defaults, types, schema
make check RUN='^Test(InheritanceAlter|InheritanceMaint|InheritanceMetadata|TableInheritance)'  # inheritance ALTER admission
make check RUN='^Test(TableLike|TableCore|PartitionConstraints|UsingIndex)'  # CREATE TABLE forms and constraints
make check RUN='^Test(TableHost|AlterHost|IndexHost)'      # the hosts
make check RUN='^TestAlterValidateConstraint'               # selection, publication, admission and parser invariants
make check RUN='^Test(Sequence|Identity|Serial)'           # sequence, identity and serial DDL
make check RUN='^TestVacuum'                               # VACUUM selection, phases, publication
make check RACE=1 RUN='^TestVacuum(PublicationFences|AuthorizationLocks)$$'  # VACUUM lock and publication races
```
