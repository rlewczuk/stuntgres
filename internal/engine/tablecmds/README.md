# internal/engine/tablecmds

The table statement executors ([`docs/ENGINE-SPLIT.md`](../../../docs/ENGINE-SPLIT.md)
§3, L16): ALTER TABLE with its action passes (constraints, defaults,
generated columns, column types, foreign keys, NOT NULL, INHERIT and
NO INHERIT), ALTER TABLE ... ALTER COLUMN DEFAULT, CREATE TABLE (LIKE, typed,
PARTITION OF), DROP TABLE, RENAME of relations, columns and constraints, and
SET SCHEMA; the table Restore repairs; and the `TableHost` and `AlterHost`
family hosts. The decisions are [`table`](../table/README.md)'s, `index`'s and
`partition`'s; this package orchestrates them over core's runtime.

## Layering

It imports [`core`](../core/README.md) and the decision packages, never the
root or another family. The root composition (dispatch and Restore hooks) and
the families still in the root import it; by rule 6 every later family except
`rolecmds` may. Code that tables share with those families lives here:
renames, SET SCHEMA, typed sources, the IF EXISTS skip, the ALTER column
candidates and type rebinds, `PrepareAlterTypeLocked`, and the hosts.

Stays in core: FK enforcement and actions, the DROP pipeline (`DropProvider`),
TEMP preservation, relation admission and publication.

## Entry points

- Dispatch (`core.Statements.ExecuteLocked`): `AlterTableLocked`,
  `AlterDefaultLocked`, `CreateTableLocked`, `DropTableLocked`,
  `RenameConstraintLocked`, `RenameRelationLocked` and `AlterSchemaLocked`
  are lock-domain continuations: entered with `Engine.Mu` held, they release
  it on every return and run admission waits, provider work and row
  evaluation unlocked.
- Restore hooks: `CaptureRestoredForeignKeys`,
  `ResolveRestoredForeignKeyTargets`, `BindRestoredForeignKeys`,
  `PairRestoredNotNull`, `RestoreNoInheritNotNull`, `RestoreAttributeRank`,
  the physical NOT NULL constructors and foreign-key providers,
  `ValidateRestoredDefault` and `PublishRestoreRepairLocked`.
- Shared with other families: `SchemaTableLocked` (CREATE TABLE inside CREATE
  SCHEMA), `SkipMissingRelationLocked`, `AcquireTypedSources`,
  `AcquirePartitionValidationViewsLocked`, `PendingPartitionDefinition` and
  `PartitionBoundBinder` (ATTACH PARTITION).

`Engine` (`inheritance_alter_admission.go`) is a defined type over
`core.Engine` that carries only `admitInheritanceAlter`, whose
`(*Engine).admitInheritanceAlter(` frame a read-only reference cancellation
fixture matches. Keep that name.

## Hosts

Each embeds core's `DDLHost` with its `{E, Op, CatalogStage}` layout, carries
a compile-time assertion, and runs every method under `Engine.Mu` without
waiting.

| Type | Implements |
| --- | --- |
| `TableHost` (`table_host.go`, `index_host.go`, `partition_host.go`) | `table.Host`, `index.Host`, `partition.Host`, `fdw.Host` |
| `AlterHost{TableHost; Scope}` (`alter_host.go`) | `table.AlterHost`, one per call: the ALTER loop begins its stage lazily |

The root's `TypeHost` and `ViewHost` embed `TableHost`.

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
```
