# internal/engine

The root is the composition layer ([`docs/ENGINE-SPLIT.md`](../../docs/ENGINE-SPLIT.md)
§3, L17): the engine constructors, the family statement dispatch behind
[`core`](core/README.md)'s `Statements` hook, the engine factory, `Reset`,
`Dump` and `Restore` with the Restore hooks. The family statement code and
its hosts are the `*cmds` packages': table, sequence, identity and VACUUM
statements are [`tablecmds`](tablecmds/README.md)'s, index statements
[`indexcmds`](indexcmds/README.md)'s, ATTACH and DETACH PARTITION with the
partition and inheritance Restore repairs
[`partitioncmds`](partitioncmds/README.md)'s, ALTER DOMAIN, ALTER TYPE, enum
and range DDL and ALTER TABLE OF [`typecmds`](typecmds/README.md)'s,
view, REFRESH MATERIALIZED VIEW, rule, policy and trigger DDL with the
materialized snapshot import [`viewcmds`](viewcmds/README.md)'s, role,
GRANT and REVOKE, default privilege, ownership and CREATE DATABASE
statements [`rolecmds`](rolecmds/README.md)'s, and the schema-object,
routine, COMMENT, tablespace, LOAD, extension and foreign-data statements
with the extension owner import [`schemacmds`](schemacmds/README.md)'s.
Runtime state, admission, publication and the transaction lifecycle are
core's. No package imports the root except tests and `enginetest`.

## Composition

`newEngine` (`composition.go`, behind `New`, `NewWithSettings` and
`NewWithCatalogProviders`) seeds the state with `core.NewEngine` and then sets
`Engine.Statements` to `familyStatements` (`statements_dispatch.go`), which
implements `Execute` and `ExecuteLocked` in the historical case order, and
`Engine.Factory` to `newSettingsEngine`. `Reset(ctx, e)`, `Dump(ctx, e, dst)`
and `Restore(ctx, e, src)` (`control.go`) are composition functions over a
`*core.Engine`.

Trigger DDL is [`viewcmds`](viewcmds/README.md#triggers)'s. Each DML
activation captures the current owner-private trigger list, not a list cached
by its prepared handle. Dump emits current trigger definitions post-data
followed by local dependency metadata; Restore admits only qualified positive
trigger dependency ALTERs, never arbitrary rename/drop utilities.

## Hosts

The family hosts live with their families: `TableHost`, `AlterHost` and
`TypeHost` are [`tablecmds`](tablecmds/README.md#hosts)'s, `ViewHost` is
[`viewcmds`](viewcmds/README.md#entry-points)'s, `RoleHost`
[`rolecmds`](rolecmds/README.md#entry-points)'s and `RoutineHost`,
`SchemaHost` and `extensionHost` [`schemacmds`](schemacmds/README.md#hosts)'s.
The root keeps `restoreHost` (`restore_run.go`), which implements
`control.Host`.

## Row mutation

MultiXact records, counters and history belong to the lock manager. CHECK and
branch admission precede the physical event; index and RETURNING evaluation
follow it. Shared mutations journal successor/deletion events while private
roots are locked and flush after the engine/owner unlock. No `Engine.Mu` →
manager-mutex acquisition exists. Finish discards private stages on failure.
VACUUM is [`tablecmds`](tablecmds/README.md#vacuum)'s.

## Native Dump and Restore

Drivers, the Emit table and hook entry types live in [`control`](control/README.md);
the root keeps Dump's `Engine.Mu` window and Collect tables, the restore stage,
`restoreRun` with every `restoreHooks` entry, and the atomic `replaceCatalog`.

Private Restore definition lookups capture the live authenticated bootstrap
through the existing trusted-destination/import marker. Only the lookup permission
view is elevated: named owner, `$user`, session identity, creation checks and ACLs
remain separate. Ordinary operations and untrusted/foreign captures gain no lookup
authority.

## Testing

Table-metadata producer units reuse the engine-seeded catalog and authorization
fixture through the real producer registry. They assert source-owned domain
cells, retained captures, typed empty results and rejection of malformed or
foreign capture authority: `RUN='^TestInfoTables'`. Public SQL and native-control
witnesses belong to the corresponding `reference/information_schema` family.

Root tests name only exported core and family members, plus the inspection
accessors in core's `inspect.go`: the Restore hook units (the ISN type owner
replay stages through `schemacmds.StageSchemaLocked` and
`StageExtensionLocked`) and units that reach no family internals. Tests that
reach a family's internals live in its `*cmds` directory; tests that reach
only core live in `core/`.

```sh
make check RUN='^Test(CatalogSource|InfoUserMappingsProducer|PGSettingsCatalogACL)'  # producers; one catalog: ^TestPG<Name>
make check RUN='^Test(Ean13|Isbn|Ismn|Ismn13|Issn|Issn13)RestoreOwnerReplay|^TestUpcCoercion'  # Restore owner replay of installed ISN types
make check RUN='^TestTransactionalSchema(Admission|PrivateProviders|Publication|DropManifest)$'  # utility policy and private catalog stages
```
