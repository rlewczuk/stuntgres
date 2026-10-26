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
core's. No package imports the root except tests and `enginetest`; the driver
imports it only for the constructors and the controls, and names core types
for everything else.

## Composition

`newEngine` (`composition.go`, behind `New`, `NewWithSettings` and
`NewWithCatalogProviders`) seeds the state with `core.NewEngine` and then sets
`Engine.Statements` to `familyStatements` (`statements_dispatch.go`), which
implements `Execute` and `ExecuteLocked` in the historical case order, and
`Engine.Factory` to `newSettingsEngine`. `NewWithCatalogProviders` registers
feature-owned catalog providers before the builtin namespace descriptors.
`Reset(ctx, e)`, `Dump(ctx, e, dst)` and `Restore(ctx, e, src)` (`control.go`)
are composition functions over a `*core.Engine`; `Reset` and `Restore`
publish through core's catalog replacement (`ReplaceCatalog`,
`ReplaceCatalogInOperation`).

## Hosts

The family hosts live with their families: `TableHost`, `AlterHost` and
`TypeHost` are [`tablecmds`](tablecmds/README.md#hosts)'s, `ViewHost` is
[`viewcmds`](viewcmds/README.md#entry-points)'s, `RoleHost`
[`rolecmds`](rolecmds/README.md#entry-points)'s and `RoutineHost`,
`SchemaHost` and `extensionHost` [`schemacmds`](schemacmds/README.md#hosts)'s.
The root keeps `restoreHost` (`restore_run.go`), which implements
`control.Host`.

## Native Dump and Restore

Drivers, the Emit table and hook entry types live in [`control`](control/README.md).
The root keeps Dump's `Engine.Mu` window and Collect inputs
(`captureDumpLocked`, `dump_capture.go`), the restore stage, and `restoreRun`
with every `restoreHooks` entry (`restore_hooks.go`; an entry's position
fixes when it runs within its phase).

Dump emits current trigger definitions post-data followed by local dependency
metadata; Restore admits only qualified positive trigger dependency ALTERs,
never arbitrary rename/drop utilities.

Private Restore definition lookups capture the live authenticated bootstrap
through the existing trusted-destination/import marker. Only the lookup permission
view is elevated: named owner, `$user`, session identity, creation checks and ACLs
remain separate. Ordinary operations and untrusted/foreign captures gain no lookup
authority.

## Testing

The root holds only tests of the composition: the controls' round trip and
failure contract (`control_test.go`), constructor composition
(`composition_test.go`) and the Restore hooks (`restore_hooks_test.go`, and
the ISN type owner replay in `ean13_restore_owner_test.go` and
`upc_coercion_test.go`, which installs the extension and moves its types
through SQL). They use the fixture helpers in `engine_test.go`. Every other
engine test lives beside its owner ([docs/TESTING.md](../../docs/TESTING.md)):
core runtime tests in `core/`, family tests in their `*cmds` directory and
lower-package tests in `<package>/enginetests/`.

```sh
make check RUN='^Test(ControlsRoundTripAndFailurePreservation|CatalogProviderRegistersBeforeInitialization|RestoreAdmitsForeignDefaultRepair)$'  # controls, constructors, Restore hooks
make check RUN='^Test(Ean13|Isbn|Ismn|Ismn13|Issn|Issn13)RestoreOwnerReplay|^TestUpcCoercion'  # Restore owner replay of installed ISN types
```
