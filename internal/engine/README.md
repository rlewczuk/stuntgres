# internal/engine

The root is the composition layer ([`docs/ENGINE-SPLIT.md`](../../docs/ENGINE-SPLIT.md)
§3, L17): the engine constructors, the family statement dispatch behind
[`core`](core/README.md)'s `Statements` hook, the engine factory, `Reset`,
`Dump` and `Restore` with the Restore hooks, and, until each moves to its
`*cmds` package, the family statement code and its hosts; table, sequence,
identity and VACUUM statements are [`tablecmds`](tablecmds/README.md)'s,
index statements [`indexcmds`](indexcmds/README.md)'s, ATTACH and DETACH
PARTITION with the partition and inheritance Restore repairs
[`partitioncmds`](partitioncmds/README.md)'s, ALTER DOMAIN, ALTER TYPE, enum
and range DDL and ALTER TABLE OF [`typecmds`](typecmds/README.md)'s, and
view, REFRESH MATERIALIZED VIEW, rule, policy and trigger DDL with the
materialized snapshot import [`viewcmds`](viewcmds/README.md)'s. Runtime
state, admission, publication and the transaction lifecycle are core's. No
package imports the root except tests and `enginetest`.

## Composition

`newEngine` (`composition.go`, behind `New`, `NewWithSettings` and
`NewWithCatalogProviders`) seeds the state with `core.NewEngine` and then sets
`Engine.Statements` to `familyStatements` (`statements_dispatch.go`), which
implements `Execute` and `ExecuteLocked` in the historical case order, and
`Engine.Factory` to `newSettingsEngine`. `Reset(ctx, e)`, `Dump(ctx, e, dst)`
and `Restore(ctx, e, src)` (`control.go`) are composition functions over a
`*core.Engine`.

Family statement code still in the root (roles, schema objects, routines,
extensions and FDW) is written as receiver-first functions over core types
(`executeSchema(e, ...)`, `executeRole(e, ...)`), so that it can leave with
its family; it names only exported core, `tablecmds` and `typecmds` members.
A root file holds one family's declarations and nothing else.

Trigger DDL is [`viewcmds`](viewcmds/README.md#triggers)'s. Each DML
activation captures the current owner-private trigger list, not a list cached
by its prepared handle. Dump emits current trigger definitions post-data
followed by local dependency metadata; Restore admits only qualified positive
trigger dependency ALTERs, never arbitrary rename/drop utilities.

## Family hosts

Each family host embeds core's `DDLHost` with its `{E, Op, CatalogStage}`
layout and is built by family code; each carries a compile-time assertion.
`TableHost`, `AlterHost` and `TypeHost` are [`tablecmds`](tablecmds/README.md#hosts)'s,
and `ViewHost` is [`viewcmds`](viewcmds/README.md#entry-points)'s.

| Root type | Implements |
| --- | --- |
| `RoutineHost` (`routine_host.go`, `operator_family_host.go`) | `routine.DDLHost`, `index.FamilyDDLHost` |
| `RoleHost` (`role_host.go`) | `role.Host`, `role.StageHost` |
| `SchemaHost` (`schema_host.go`) | `ddl.SchemaHost` |
| `extensionHost` (`extension_host.go`) | `extension.Host`, `extension.Stage` |
| `restoreHost` (`restore_run.go`) | `control.Host` |

## Extensions

Records, decisions and the root's orchestration are in
[`extension`](extension/README.md). UPDATE keeps its read fences (including
ACLs) from the staged snapshot, not from descriptors observed after outside-lock
provider validation; CREATE and UPDATE share one final publication. Retirement
captures the complete target set, including automatic aliases, and rejects
incoming dependencies with `catalog.Conflict` before mutation.

Identity ALTER remints citext/EAN13 values through a transient exact
source-descriptor-to-current-descriptor map, never an OID-only permission.
Generation reownership restores the prior import map before returning and leaves
retained catalog/storage roots unchanged. Base rename, move and ownership do not
change installed I/O routine namespaces or owners; canonical manifest roles own
portable ownership records and identity replay.

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

Trigger lifecycle identity, clone, source-fence, dependency and remap units use
`RUN='^TestTriggerDDL'` and construct metadata with the same trigger fixture.

Lo provider units use `RUN='^TestLo(Provider|OwnerMetadata|ManifestAdmission)'`.
They stage concrete domain/companion recipes through the shared extension host,
validate immutable manifests and symbolic ownership, and exercise genuine
collision/allocation and destination-role replacement fences without public SQL.

Table-metadata producer units reuse the engine-seeded catalog and authorization
fixture through the real producer registry. They assert source-owned domain
cells, retained captures, typed empty results and rejection of malformed or
foreign capture authority: `RUN='^TestInfoTables'`. Public SQL and native-control
witnesses belong to the corresponding `reference/information_schema` family.
Routine-metadata units use the same real capture fixture and staged extension
constructors for declaration completeness, independent source bags, redaction,
empty/canceled results and generation fences: `RUN='^TestInfoRoutines'`.

Root tests are family tests: they reach unexported family code and name only
exported core members, plus the inspection accessors in core's `inspect.go`.
Tests that reach only core live in `core/`.

```sh
make check RUN='^TestDatabaseCreate'                   # registry, template, publication, target authority
make check RUN='^Test(CatalogSource|InfoUserMappingsProducer|PGSettingsCatalogACL)'  # producers; one catalog: ^TestPG<Name>
make check RUN='^TestIsnProvider'                      # installed identifier authority and import
make check RUN='^TestTransactionalSchema(Admission|PrivateProviders|Publication|DropManifest)$'  # utility policy and private catalog stages
```
