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
make check RUN='^Test(Sequence|Identity|Serial)'           # sequence, identity and serial DDL
make check RUN='^TestVacuum'                               # VACUUM selection, phases, publication
make check RACE=1 RUN='^TestVacuum(PublicationFences|AuthorizationLocks)$$'  # VACUUM lock and publication races
```
