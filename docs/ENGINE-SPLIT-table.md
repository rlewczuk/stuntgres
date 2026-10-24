# Engine split: `internal/engine/table`, sixth round

Session prompt for linear item 175 of the
[status page](ENGINE-SPLIT-STATUS.md). The rules are in
[ENGINE-SPLIT.md](ENGINE-SPLIT.md) §2, §3 and §5, and the package contract
is in [the table README](../internal/engine/table/README.md).

`dropProvider` (`drop_provider.go`, 57 methods) runs in two modes:
- **Stage mode** reads and writes a `CatalogStage`.
- **Prepared mode** is used before the stage exists, for ALTER TABLE DROP
  COLUMN over an inheritance tree. It records ordered effects in a
  `preparedDrop` log that `StatementScope.consumePreparedDrop` replays into
  the stage later.

`table` already owns the DROP decisions (`table/drop*.go`) and the
`PreparedDrop` and `DefinitionDelta` interfaces
(`table/inheritance_prepare_host.go`). The log itself is still in the root.
It counts as dirty only because `dropProvider`'s fields name `CatalogStage`,
`Engine` and `StatementScope`. Sizes were measured at `ea8c445fe`; re-run
`reach` with `-files '^drop_provider\.go$' -cut 'dropProvider>CatalogStage,dropProvider>Engine,dropProvider>StatementScope'`
first.

## Item 175 — prepared DROP log

Needs —. About 0.37k lines move.

**Moves.**
- `preparedDrop`, `dropEffect` and `dropEffectSlot`. `preparedDrop`
  becomes `table.DropPreparation`; `table.PreparedDrop` is already taken as
  the interface name. Its methods move with it:
  - `Descriptor`, `Lookup`, `Descriptors` and `ProceduresNamed`;
  - `HasDynamicProcedureName` and `HasProviderProcedureOverride`;
  - `recordEffect` and `indexEffect`;
  - `Next`, `DefinitionDelta`, `LayoutDelta` and `IndexLayout`.
- The prepared-mode halves of the dual-mode `dropProvider` methods, as
  `DropPreparation` methods: `observe`, `readInputDescriptor` and the
  prepared branches of `Replacement`, `Removed`, `Descriptors`,
  `DescriptorCount`, `IndexLayout` and `rewindNotices`.
- The body of `StatementScope.validateColumnDropObservationsLocked`, as a
  method over the log and a `catalog.Read`.
- If `reach -cut` confirms it is clean, also move the cascade column-drop pass
  and the retire loops of `dropProvider.ExpressionDependencies`
  (`domain_dependencies.go`). They become
  `table.CascadeColumnDrops(r, relations, edges, targets, project)`.

**Cut.**
- Each dual-mode `dropProvider` method keeps its stage branch and otherwise
  delegates to `p.prepared` (§2.2 rule 1, state ownership in place).
- Add a view type `table.DropPreparationView{Base, Preparation, IdentitySource}`
  for the reads that today build a `dropProvider` just to reach the
  prepared branches.
- Export the effect fields, so that the root replay switch in
  `consumePreparedDrop` keeps working unchanged.
- The preparing statement identity stays an opaque `any`.

**Stays root.**
- `Acquire`, `lockInheritanceClosure` and `Remove`;
- `ApplyViewDependents` and `projectColumn` (storage under `Engine.mu`);
- the `consumePreparedDrop` replay, `prepareDropDefinitionProvider`, the
  `ExpressionDependencies` skeleton (lock waits) and `removeObjectLocked`;
- `clearOperatorLinks`, `removePartitionHierarchyLocked` and
  `synchronizeRowType`.

**Callers and locks.** Prepared mode runs under `Engine.mu` and takes no
lock. These must stay identical:
- observe-before-read: `observe` captures the original descriptor before
  any read returns it;
- the effect order;
- the notice-rewind indexing (positions index effects, not notices).

**Tests.**
- Rewrite the root units that name `preparedDrop` or `dropEffect` to the
  `table` names: `drop_reader_test.go`,
  `inheritance_alter_admission_test.go` and `sql_routine_lookup_test.go`.
- Pure log tests move to `table`.
- New `table` units pin the effect order and the notice rewind.
- Quick and `REFERENCE=1` selection:
  `TestReferenceAlterDropColumn(ExternalHierarchy|ExternalDependencies|DependentIndexAndView|Notices|PreparedLifetime|TransactionalDDL)`
  and `TestReferencePGDependDropDiagnostics`. Use `RACE=1` on the
  transactional selection.

**Docs.** Add the prepared DROP log to the table README's DROP section.

## Status

- 175: not started.
