# Engine split: the `*cmds` family packages, seventh round

Session prompts for linear items 196–203 of the
[status page](ENGINE-SPLIT-STATUS.md). The rules are in
[ENGINE-SPLIT.md](ENGINE-SPLIT.md): §2, §3 (layer L16, rule 6 family arrows) and
§5. The core contract is in [ENGINE-SPLIT-core.md](ENGINE-SPLIT-core.md).

A family package holds the statement executors of one group of commands. They
are free functions over `*core.StatementScope` and `*core.Engine`, after items
191–192. A family package also holds the family host types (item 190), its
Restore repair functions, and its tests.

The root composition calls the family from both dispatch switches and the
Restore hooks. Families import core, the decision
packages and the families below them (rule 6). Sizes are from `closure` at
`26f8fea15` (`r2/assign.tsv`, declaration lines).

## How a family move works

1. **Re-run `closure`.** The move set is every declaration assigned to the
   family, plus the shared sets that R3 gives it.
2. **Move the code.**
   - Move the files (each is wholly one family after item 192) to
     `internal/engine/<family>`.
   - Export what the root composition or a later family calls.
   - Qualify the root call sites in the dispatch implementation and the
     Restore hooks.
3. **Move the tests.**
   - The family's root tests move: pure units as internal tests; engine tests as
     external tests in the family directory, with family internals reached
     through its `export_test.go`.
   - The tests that item 186 or 187 put in the directory change `engine.X` to
     `<family>.X`.
4. **Package README.** Write it with the contract, the imports, what stays in
   core, and `## Testing` selectors. Move the matching root README paragraphs.

**Mode S:** a replay script and a freeze window.

**Verify:**
- build and `go test -run '^$' ./...`;
- the family's selectors in their new packages;
- quickcheck;
- the §3 `go list` check;
- a `REFERENCE=1` selection for the family's statements;
- bench only if a moved function is on a per-statement path.

## Item 196 — `tablecmds`: tables

Needs 195. About 3.5k lines (0.13 MB) and 0.24 MB of family tests.

- **Entries:**
  - `alterTableLocked`, `alterDefaultLocked`, `createTableLocked`,
    `dropTableLocked` and `renameConstraintLocked`, all entered with `Engine.mu`
    held;
  - with them, everything only those entries reach (`alter_*`, `create_table*`,
    `drop_table*`, constraint, default, generated-column and FK DDL files).
- **Shared code it takes** (R3, because `tablecmds` is the lowest family):
  - renames: `renameRelationLocked`, `renameInheritanceColumnLocked`,
    `acquireInheritanceDefinitions`, 215 lines;
  - SET SCHEMA: `alterSchemaLocked`, 127 lines;
  - identity sequence options: `stageIdentitySequenceOptionsLocked` and its
    set, 133 lines;
  - typed sources: `acquireTypedSources`, 53 lines;
  - about 0.1k more shared with partitions, inheritance and types.
- **Stays core:** FK enforcement and actions, the DROP pipeline
  (`dropProvider`), TEMP preservation, and relation admission.
- **REFERENCE=1:** `^TestReferenceAlter(AddConstraint|Nullability|Default)`
  and `^TestReferenceTableCore`.

## Item 197 — `tablecmds`: sequences, identity, VACUUM

Needs 196. About 0.86k lines.

- **Entries:**
  - `executeSequenceDDL` and `executeSequenceComment` (CREATE, ALTER and DROP
    SEQUENCE);
  - `executeVacuum`.
- `closure` finds no shared code between these and the other families. They can
  join `tablecmds` without new arrows.
- **Watch:** the frame-name test `vacuum_concurrency` and
  `storage.InspectVacuum`.

## Item 198 — `indexcmds`

Needs 197. About 0.48k lines.

- **Entries:** `executeCreateIndex`, and `executeIndexMaintenance` (ALTER INDEX,
  DROP INDEX, REINDEX).
- **Imports** `tablecmds` for renames.
- **Stays core:** unique and exclusion probing, index publication, and the
  index runtime that `statementHost` reaches.
- **Watch:** the frame-name test `index_hash_cancellation`.

## Item 199 — `partitioncmds`

Needs 198. About 0.75k lines and 0.1 MB of family tests.

- **Entries:** `attachPartitionLocked` and `detachPartitionLocked`, entered
  with `Engine.mu` held. Also the inheritance Restore repairs the root's Restore
  hooks call: `restoreCheck`, `restoreRetiredConstraint`,
  `captureInheritedForeignKey`, `captureInheritedOwner` and
  `restoreInheritedOwner`.
- **Imports** `tablecmds` and `indexcmds`.
- **Stays core:** routing, pruning, partition-descendant admission
  (`acquirePartitionDescendants`), and the partition parts of the DML path.
- **Watch:** the frame-name test `range_partition_publication`
  (`(*pendingPartitionDefinition).finish`).

## Item 200 — `typecmds`

Needs 199. About 1.1k lines.

- **Entries:**
  - `executeAlterDomain` and `executeAlterComposite`;
  - `executeEnumDDL` (CREATE RANGE, CREATE ENUM, ALTER ENUM);
  - `alterTableOfLocked` (ALTER TABLE OF).
- **Takes** the code it shares with views (domain replay, 76 lines) along
  the `typecmds` → `viewcmds` arrow.
- **Imports** `tablecmds`. CREATE DOMAIN, TYPE, COLLATION and CONVERSION
  dispatch through `executeSchema`, so they belong to `schemacmds`.
- **Stays core:** domain checks on the row path (`StatementScope.CheckDomain`),
  and domain input decoding.

## Item 201 — `viewcmds`

Needs 200. About 1.0k lines and 0.12 MB of family tests.

- **Entries:**
  - `executeCreateView`, `executeAlterView` and `executeDropView`;
  - `executeRule`, `executePolicy` and `executeTrigger`;
  - `refreshMaterializedLocked`, entered with `Engine.mu` held.
- **Takes** the trigger and view shared code (44 lines).
- **Imports** `tablecmds` and `typecmds`.
- **Stays core:** rule programs, RLS runtime, trigger firing, and matview
  population on the bound path.

## Item 202 — `rolecmds`

Needs 201. About 1.1k lines and 0.14 MB of family tests.

- **Entries:**
  - `executeRole` and `executeRoleAttributes`;
  - `executeObjectGrant`, `executeDatabaseGrant` and
    `executeDefaultPrivileges`;
  - `executeOwned` and `executeOwnership`;
  - `executeCreateDatabase`, which uses the factory hook from item 188;
  - `acquireRoleRetirementLocks` (DROP ROLE).
- **Imports** no other family. The code it shares with `schemacmds` goes to core
  (R3, 110 lines): `executeRelationOwnership` and two role-installation
  helpers.
- **Stays core:** authorization capture, `AuthorizationView`, role locks on the
  statement path.

## Item 203 — `schemacmds`

Needs 202. About 1.5k lines and 1.59 MB of family tests.

- **Entries:**
  - `executeSchema`, the schema-element path: CREATE SCHEMA, TYPE, DOMAIN,
    COLLATION and CONVERSION, CREATE LANGUAGE, ALTER SCHEMA, and the matching
    ALTER and DROP. It takes its shared set with routines, 722 lines:
    `stageSchemaLocked`, `admitDefinitionMutation` and
    `admitDefinitionRetirement`;
  - `executeSQLRoutineDDL`;
  - the cast, operator, family, class, routine and access-method DDL cases;
  - `executeComment`, `executeCreateTablespace` and `alterAllTablespaceLocked`;
  - `executeExtension`, `executeDropExtension`, `executeExtensionUpdate` and
    `executeLoad`;
  - `executeForeignDDL` and `executeForeignGrant`.
- **Tests:** the extension-provider tests that call `stageExtensionLocked` or
  `extensionHost` directly (about 1.3 MB) move here, or to `extension`. They
  reach the family internals through `export_test.go`.
- **Imports** `tablecmds`.

## Status

- 196: done (`e558079d5`, `1510781fa`, `eda4d8a54`, `86f373ca2`). Generated by
  `replay.sh` (c1–c4) in `.cswd/tmp/PG18-ENGINE-SPLIT-196-TABLECMDS-TABLES/`
  with `cmdmv` there (item 195's `coremv` with `-dest PKG`: it also writes
  `export_test.go` aliases for the unexported package-level names that moved
  external tests use, and `-allow` lists sites fixed by hand after the run).
  Move set: `closure` (`-famseed=false`) files wholly `fam:table` or a shared
  set containing `table` (`mkprod.py`), measured before the export pass
  (exporting a shared declaration in a non-family file relabels it core), plus
  `alter_type_prepare.go` (`PrepareAlterTypeLocked`, fdw+typedef; it names
  `AlterHost`, so R3 puts it here) and `fdw_host.go` (the `TableHost` `fdw.Host`
  assertion). Exports (exportpass, 44): see the c2 commit;
  `pendingPartitionDefinition` became `PendingPartitionDefinition`, and the
  range partition publication test now matches
  `(*PendingPartitionDefinition).finish` in `tablecmds` (item 199: watch that
  frame there). `Engine` with `admitInheritanceAlter` moved here; its frame
  keeps the reference substring. Tests: 42 root files moved as external tests,
  by the T3 override (they reach unexported table code: also inheritance,
  ownership, rule, RLS, routine and index-prefix files) or by prefix (13
  table-prefix files); 6 root files keep reduced helper copies; the concurrent
  matview tests bind through `TableHost.BindIndexPredicate` and stay root;
  reused helper copies checked equal up to root fixture → `enginetest` names.
  Notes for later items: `TypeHost` and `domain_default_snapshot.go` (shared
  ddlcommon+routine+typedef) stay root, and by R3 only `tablecmds` can hold them
  (item 200 or 203); table-prefix root tests left for item 204:
  `query_table_create_descriptor`, `relation_*` and `table_view_summary` (core
  runtime) and helper-only files; sequence, identity and VACUUM tests wait for
  item 197.
- 197: done (`d3a613464`, `75f2d2cff`, `3fd44d15c`). Generated by `replay.sh`
  (c1–c4) in
  `.cswd/tmp/PG18-ENGINE-SPLIT-197-TABLECMDS-SEQUENCES-IDENTITY-VACUUM/` with
  item 196's `cmdmv` (copied there). Move set: `closure` (default and
  `-famseed=false` agree) files wholly `fam:sequence` or `fam:vacuum`; no shared
  set and no family edge. Exports (exportpass, 4): `ExecuteSequenceDDL`,
  `ExecuteSequenceComment` and `ExecuteVacuum` (with doc comments) and
  `ValidateRestoreIdentityConfiguration`; no `export_test.go` additions. Frames:
  `vacuumCompletion` stays unexported, so `tablecmds.vacuumCompletion` still
  matches the barrier and progress tests; the `storage` frames are unchanged; no
  reference fixture names a moved function. Tests: 9 root files moved as
  external tests: `vacuum_concurrency` by the T3 override (it reaches
  `executeVacuum`) with its helper files `vacuum_authorization` and
  `vacuum_lifecycle`, and by prefix `identity`, `serial` and `serial_dependency`
  (serial as in item 186), `vacuum_progress`, `vacuum_review_regressions` and
  `vacuum_visibility`; all 52 helper uses reuse copies already in `tablecmds/`
  (equal up to root fixture → `enginetest` names), and the moved `serialLeaf`
  and `vacuumStore` replace their earlier copies; the root keeps
  `vacuum_visibility_test.go` reduced to `vacuumStore`. Notes for later items:
  `vacuum_materialized`, `vacuum_metadata` and `vacuum_truncate` reach the view
  family's `stageMaterializedRows` and wait for item 201; `pg_sequences`
  (pgcatalog runtime) and the helper-only `sequence_*`, `identity_helpers`,
  `serial_publication` and `vacuum_indexes` files wait for item 204. Finding:
  core's `TestIdentityOwnCheckAtomicConstruction` fails when run alone or in a
  narrow selection, on master too ("duplicate advisory session": core's
  `sessionStart` and `enginetest.RoleOperation` keep separate counters and both
  mint session ID 1); it passes in the full package run.
- 198: done (`9ef4a85bd`, `82ba9ff17`, `7d9d9a388`). Generated by `replay.sh`
  (c1–c4) in `.cswd/tmp/PG18-ENGINE-SPLIT-198-INDEXCMDS/` with item 196's
  `cmdmv` (copied there, run with `-dest stuntgres/internal/engine/indexcmds`).
  Move set: `closure` (item 192 `areas.json`, default flags) files wholly
  `fam:index`: `index.go`, `index_concurrent.go`, `index_maintenance.go`,
  `index_partition_validity.go`, `index_snapshot_wait.go` and `index_wait.go`;
  no shared set and no family edge. Exports (exportpass, 2):
  `ExecuteCreateIndex` and `ExecuteIndexMaintenance` (with doc comments);
  `export_test.go` aliases `WaitOlderIndexSnapshots`. Frames: no reference
  fixture or engine test names a moved function; `index_hash_cancellation`
  (core) matches `index`'s `MergeHashIndexRoot`, which stays. Tests: 3 root
  files moved as external tests: `concurrent_index_visibility` by the T3
  override (it reaches `waitOlderIndexSnapshots`) with its helper-only file
  `concurrent_index_roots`, and `index_clauses` by prefix; 4 helper uses reuse
  copies already in `indexcmds/` (equal up to qualifiers), 9 helpers were
  copied. Notes for later items: `index_write_host` (core's statement write
  host) and the helper-only `concurrent_index_controls`,
  `index_expression_lifecycle`, `index_maintenance*`, `index_predicate`,
  `index_test`, `index_transactional` and `using_index` files stay in the root
  for item 204 (most have no root user left); core's `concurrent_index_*` tests
  drive CREATE INDEX CONCURRENTLY through SQL and could follow to `indexcmds`
  (item 204).
- 199: done (`54f05eb71`, `8a3c0d899`, `f67ce379a`). Generated by `replay.sh`
  (c1–c4) in `.cswd/tmp/PG18-ENGINE-SPLIT-199-PARTITIONCMDS/` with item 196's
  `cmdmv` (copied there, run with `-dest
  stuntgres/internal/engine/partitioncmds`). Move set: `closure` (item 192
  `areas.json`, default flags) files wholly `fam:partition`
  (`partition_attach.go`, `partition_attach_scan.go`,
  `partition_check_replay.go`, `partition_detach.go`,
  `partition_detach_restore.go`, `partition_unique_replay.go`,
  `partition_using_index_replay.go`) or `fam:inheritance`
  (`inheritance_foreign_key_restore.go`, `inheritance_owner_restore.go`,
  `inheritance_recipe_checks.go`, `inheritance_retired_restore.go`); no shared
  set and no family edge; the Restore hook files (`restore_steps_*.go`,
  `restore_admit.go`) stay root. Exports (exportpass, 15, with doc comments):
  `AttachPartitionLocked`, `DetachPartitionLocked`, the replays
  `BeginPartition(Check|Unique|UsingIndex)Replay`,
  `CapturePartitionReplayTeardown` and `ValidatePartitionReplayTeardown`, and
  the repairs `RestoreCheck`, `PairRestoredGeneration`,
  `CaptureRestoredGeneration`, `CloseRestoredRecipesForData`,
  `RestoreRetiredConstraint`, `CaptureInheritedForeignKey`,
  `CaptureInheritedOwner` and `RestoreInheritedOwner`; `export_test.go` aliases
  `LockPartitionAttachChild`. `partitioncmds` imports `tablecmds`, not
  `indexcmds`. Frames: no reference fixture or engine test names a moved
  function; `Engine.admitInheritanceAlter` (the reference
  `(*Engine).admitInheritanceAlter(` frame) and
  `(*PendingPartitionDefinition).finish` (`range_partition_publication`) were
  already in `tablecmds` and stay there. Tests: 11 root files moved as external
  tests: by the T3 override the eight that reach moved code
  (`inheritance_foreign_key_restore`, `inheritance_recipe_restore`,
  `inheritance_retired_restore`, `partition_attach_check_replay`,
  `partition_attach_growth`, `partition_detach_transaction`,
  `partition_unique_replay`, `partition_using_index_replay`), and by prefix
  `partition_attach_publication`, `partition_attach_transaction` and the
  helper-only `partition_detach`; 22 helper uses reuse copies already in
  `partitioncmds/` (equal up to root fixture → `enginetest` names), 15 helpers
  were copied, and the moved `retiredRaw`, `retiredRow` and `retiredRepairSQL`
  replace their earlier copies. Notes for later items: the helper-only root
  files `inheritance_alter`, `inheritance_query` and `table_inheritance`
  (typed-table root tests use them) and the dead `inheritance_dml_wait`,
  `inheritance_retired`, `partition_hash` and `range_partition_publication`
  (copies already in `partitioncmds/`) stay for item 204. Finding: `tablecmds`'
  `TestInheritanceAlterAdmission*` and `TestInheritanceMaintAdmission` fail when
  run alone or in a narrow selection, on master too ("duplicate advisory
  session", the session-ID counter clash item 197 found in core), so the
  `tablecmds` README inheritance selector fails; they pass in the full package
  run.
- 200: done (`c9eb2629e`, `d6bc0e809`, `88390718a`, `a6d5b288e`). Generated by
  `replay.sh` (c1–c5) in `.cswd/tmp/PG18-ENGINE-SPLIT-200-TYPECMDS/` with item
  196's `cmdmv` (copied there). Move set: `closure` (item 192 `areas.json`,
  default flags) files wholly `fam:typedef` or `shared:typedef+view`
  (`collation_seed_dump.go`, `domain_alter.go`, `domain_default_restore.go`,
  `enum_ddl.go`, `enum_generation.go`, `type_owner_restore.go`,
  `typed_mutation.go`, `typed_stage.go`, `typed_table.go`,
  `typed_type_alter.go`; the shared set is `CaptureDomainReplay` and
  `DomainReplayEnv`, 76 lines) → `typecmds`; no other shared set or family edge.
  `TypeHost` (`typedef_host.go`, `domain_default_snapshot.go`; closure labels it
  core only as a root export) moved to `tablecmds` in its own commit, by R3: it
  embeds `TableHost`, and `typecmds` and the schema statements both build it.
  Exports (exportpass, 19, with doc comments): the dispatch entries
  `ExecuteAlterDomain`, `ExecuteAlterComposite`, `ExecuteEnumDDL` and
  `AlterTableOfLocked`, Dump's `ValidateTemporaryCollationDump`, the Restore
  hooks `ExecuteDomainReplayInsert`, `CaptureRestoredTypeOwner` and
  `RestoreTypeOwner`, the view-shared `CaptureDomainReplay` and
  `DomainReplayEnv`, and, only for root tests that stay, `TypedStage`,
  `TypedMutation`, `TypedRelationMutation` (fields `Old`, `Next`, `Transforms`,
  `Additions`, `Checks`) and `TypeHost.BindRestoredDomainDefault`;
  `export_test.go` aliases `StageTypedRows`. Frames: no reference fixture or
  engine test names a moved function. Tests: 6 root files moved as external
  tests: by the T3 override `geometry_conversion_replay`,
  `typed_inheritance_authority`, `typed_source_admission`, `typed_table` and
  `typed_type_lifecycle`, and by prefix `typed_review_regression`. Deviation:
  `cmdmv` ran with item 195's `move.map`, so root fixture helpers became their
  `enginetest` names (one session-ID counter in the package; items 196–199
  copied them); 5 other helpers were copied, and the moved `typedUnitCatalog`
  replaces its earlier copy (equal up to the `enginetest` names). Notes for
  later items: root tests that stage through `domainUnitStage` (root
  `stageSchemaLocked`) and reach type code stay for item 203:
  `composite_alter_unit`, `composite_review_remedy`, `domain_default_restore`,
  `enum_preflight` and `domain_alter_default_nullability` (with the other
  `domain_*`/`enum_*` root tests); when they move, `TypedStage`,
  `TypedMutation`, `TypedRelationMutation`'s fields and
  `BindRestoredDomainDefault` can return to unexported names behind
  `export_test.go`. `geometry_conversion_mixed_replay` (it reaches
  `importMaterializedSnapshotRow`) waits for item 201; the root keeps
  `typedUnitCatalog` and `typedRenameStage` in `typed_table_test.go` for the
  composite tests.
- 201: done (`b6a1f136b`, `7a415d0bf`, `38ff8a53c`). Generated by `replay.sh`
  (c1–c4) in `.cswd/tmp/PG18-ENGINE-SPLIT-201-VIEWCMDS/` with item 196's `cmdmv`
  (copied there) and item 195's `move.map`. Move set: `closure` (item 192
  `areas.json`, default flags) files wholly `fam:view`, `fam:trigger` or
  `shared:trigger+view` (`materialized_refresh_execute.go`,
  `materialized_snapshot.go`, `policy_ddl.go`, `rule_ddl.go`, `trigger_ddl.go`,
  `view.go`, `view_alter.go`, `view_definition_locks.go`; the shared set is
  `publishRelationChangeLocked` and `finishPolicyNoticeLocked`, 44 lines), plus
  `ViewHost` (`view_host.go`, `policy_expression.go`; closure labels it core
  only as a root export, and only these executors build it) → `viewcmds`; no
  other shared set or family edge. Exports (exportpass, 9, with doc comments):
  the dispatch entries `ExecuteCreateView`, `ExecuteAlterView`,
  `ExecuteDropView`, `ExecuteRule`, `ExecutePolicy`, `ExecuteTrigger` and
  `RefreshMaterializedLocked`, the Restore hook `ImportMaterializedSnapshots`,
  and, only for a root test that stays, `ImportMaterializedSnapshotRow`;
  `export_test.go` aliases `MaterializedRefreshRowsLocked`, `MaterializedStage`
  and `StageMaterializedRows`. Frames: no reference fixture or engine test names
  a moved function; the matched `acquireBoundViewRelationModes` and
  `acquireRelationTarget` bodies stay in core. Tests: 22 root files moved as
  external tests: by the T3 override the twelve materialized-view files that
  reach moved code, and item 197's `vacuum_materialized`, `vacuum_metadata` and
  its helper file `vacuum_truncate`; by prefix `materialized_alter_review`,
  `materialized_refresh_queue_authority`, `materialized_view_authorization`,
  `materialized_view_capture`, `materialized_view_regtype`, `policy` and
  `view_lifecycle`; 27 helpers were copied (`testSessionID` among them, with
  `extensionTemporaryOperation` and `withPublicSchemaUnitOwner`; each moved test
  passes alone), `alterDropProvider` reuses the `viewcmds` copy, and the moved
  `policyUnitFixture` and `policyUnitBind` replace their earlier copies (equal
  up to keyed literals). The root keeps `refreshUnitRelation`,
  `refreshUnitRows`, `materializedUnitStage`, `policyUnitFixture` and
  `policyUnitBind` for `relation_validation` and `access_inquiry`. Notes for
  later items: `trigger_ddl` (it reaches `stageExtensionLocked`) and
  `geometry_conversion_mixed_replay` (`stageSchemaLocked` through
  `domainUnitStage`) stay for item 203, which can then return
  `ImportMaterializedSnapshotRow` to an unexported name behind `export_test.go`;
  the helper-only `rule_test` (`transactional_rules`), `trigger_fixture` and
  `trigger_review` (`trigger_ddl`) stay with their root users, and the dead
  `rls_base_lock`, `rule_cte` and `view_replace` (copies already in
  `tablecmds/`, `core/` and `viewcmds/`) stay for item 204.
- 202: done (`e9e537b7a`, `c4890f698`, `c240f4bcf`). Generated by `replay.sh`
  (c1–c4) in `.cswd/tmp/PG18-ENGINE-SPLIT-202-ROLECMDS/` with item 196's `cmdmv`
  (copied there) and item 195's `move.map`. Move set: `closure` (item 192
  `areas.json`, default flags) files wholly `fam:roles` (`database_create.go`,
  `database_grant.go`, `database_runtime.go`, `default_privileges_execute.go`,
  `owned_roles.go`, `ownership.go`, `privilege_admission.go`,
  `role_acl_revision.go`, `role_attributes.go`, `role_grant.go`,
  `role_locks.go`, `role_membership.go`, `roles.go`), plus `RoleHost`
  (`role_host.go`; closure labels it core only as a root export, and only these
  executors build it) → `rolecmds`; no shared set and no family edge: item 195
  had already put the code shared with the schema statements in core
  (`ExecuteRelationOwnership`, `OwnershipStage`, `PublishOwnership`,
  `InstallOperationRolesLocked`). Exports (exportpass, 9, with doc comments):
  the dispatch entries `ExecuteRole`, `ExecuteRoleAttributes`,
  `ExecuteObjectGrant`, `ExecuteDatabaseGrant`, `ExecuteDefaultPrivileges`,
  `ExecuteOwned`, `ExecuteOwnership`, `ExecuteCreateDatabase` and
  `AcquireRoleRetirementLocks`; `export_test.go` aliases
  `DefaultACLChangeLocked`, `NewDatabaseRuntime`, `PublishDatabaseTargetLocked`,
  `PublishDroppedRolesLocked`, `SwitchSessionRoleLocked` and
  `TouchOperationIdentityLocked`. Frames: no reference fixture or engine test
  names a moved function. Tests: 16 root files moved as external tests: by the
  T3 override the eleven that reach moved code (`alter_nullability_authority`,
  `database_create`, `default_privileges_catalog`, `driver_batch_identity`,
  `predefined_roles`, `role_attributes`, `role_password`, `role_switch`,
  `role_transaction_identity`, `role_transaction_publication`,
  `session_identity`), and by prefix `default_privileges_projection` and the
  four `ownership_*` files with tests; 19 helpers reuse copies already in
  `rolecmds/` (equal up to qualifiers), 11 were copied, and the moved
  `roleAttributeCreate` and `roleAttributeApply` replace their earlier copies.
  Deviation: `rolecmds`' `testSessionID` now starts at `1 << 20`, above the IDs
  `enginetest` mints: the moved `TestRoleAttributesValidityInput` uses both
  counters at once and, alone, registered a duplicate advisory session (the
  clash items 197 and 199 found); every `rolecmds` test passes alone. Notes for
  later items: `default_closure` (it reaches `fam:extension` code) stays for
  item 203; the column-default `default_arithmetic` and `default_catalog` (a
  `default_*` prefix false positive; they reach only core) and the helper-only
  `aclitem_identity`, `authorization_fixtures`, `authorization`,
  `default_privileges_helpers`, `ownership`, `role_grant`, `role_membership` and
  `roles` files (and the reduced `predefined_roles` and `role_attributes`) stay
  with their root users for item 204; core and `tablecmds` could take the same
  `testSessionID` offset for their alone-only failures.
- 203: done (`c23a576d8`, `9a52da016`, `c4bc8a1a5`). Generated by `replay.sh`
  (c1–c4) in `.cswd/tmp/PG18-ENGINE-SPLIT-203-SCHEMACMDS/` with item 196's
  `cmdmv` (copied there) and item 195's `move.map`. Move set: `closure` (item
  192 `areas.json`, default flags) files wholly `fam:ddlcommon`,
  `fam:extension`, `fam:fdw`, `fam:routine` or `shared:ddlcommon+routine` (the
  spec's shared set: `stageSchemaLocked`, `admitDefinitionMutation`,
  `admitDefinitionRetirement` and their callees), plus `RoutineHost`,
  `SchemaHost`, `BindSQLRoutineLocked` and `stageSchemaDomainsLocked` (closure
  labels them core only as root exports, reached from the hosts) → `schemacmds`,
  25 files, ~1.76k declaration lines; no other family edge;
  `sql_routine_restore.go` (a Restore hook) stays root. Exports (exportpass, 18,
  with doc comments): the dispatch entries `ExecuteSchema`,
  `ExecuteSQLRoutineDDL`, `ExecuteForeignDDL`, `ExecuteForeignGrant`,
  `ExecuteCreateTablespace`, `AlterAllTablespaceLocked`, `ExecuteLoad`,
  `ExecuteExtension`, `ExecuteDropExtension`, `ExecuteExtensionUpdate` and
  `ExecuteComment`, the Restore hook `ImportExtensionOwners`, and, for root
  tests that stay, `StageSchemaLocked` and `StageExtensionLocked`; the
  `extensionHost` fields `Engine`, `Op`, `Auth` and `Stage` (the provider tests
  build the host as a literal); `export_test.go` aliases
  `AdmitDefinitionMutation`, `DomainRestrictError`, `ExtensionHost`,
  `ExtensionNamespaceOf`, `StageDomainDependents`, `StageExtensionUpdateLocked`,
  `StageForeignLocked`, `StageOf` and `StageSchemaDropLocked`. Frames: no
  reference fixture or engine test names a moved function. Tests: 144 root files
  moved as external tests: by the T3 override the 139 that reach moved code
  directly or through helpers (the extension provider tests, the type, domain,
  enum and composite tests that stage through `stageSchemaLocked`, item 200's,
  201's and 202's leftovers, and catalog producer, COPY filter and runtime tests
  whose fixtures stage schema elements), and by prefix `comment_transaction`,
  `operator_family_internal_owner`, `regclass_dependency`,
  `routine_alter_identity` and `sql_routine_default`; 110 helpers were copied, 7
  reuse copies already in `schemacmds/` (equal up to the `enginetest` names);
  `regoperator_test` opens its inventory one directory further up; the package's
  `testSessionID` starts at `1 << 20` (item 202's offset); every `schemacmds`
  test passes alone. Deviations: the root keeps `ean13_restore_owner` and
  `upc_coercion`, which test the root's `admitRestoreAlterOwner` and stage
  through the reduced `isnUnitStage` and `domainUnitStage` copies, hence the
  production exports `StageSchemaLocked` and `StageExtensionLocked`; item 200's
  `TypedStage`, `TypedMutation`, `TypedRelationMutation` fields and
  `BindRestoredDomainDefault` and item 201's `ImportMaterializedSnapshotRow`
  stay exported: their users are now `schemacmds` tests (and the root's
  `typed_table_test`, and `viewcmds`' own external tests), which cannot reach
  another package's `export_test.go`. Finding: the escape diff against master
  shows one change outside the moved code: Dump's `captureDumpLocked` (root,
  unchanged) no longer inlines `fdw.(*Registry).NamespaceEntries` in its
  range-over-func loop once the fdw executors left the package, so each Dump
  allocates the loop closure and three captured variables (not a startup or
  per-statement path). Notes for item 204: `closure` finds no family code left
  in the root (1,299 composition declaration lines); the root keeps reduced
  helper copies in `available_extension_sources`, `domain_storage`,
  `domain_test`, `extension_provider`, `extension_temporary`,
  `extension_version_manifest`, `intagg_expression`, `intagg_provider` and
  `isn_provider` for its staying users, and the helper-only
  `extension_retirement` and `extension_version_frontier`; once
  `ean13_restore_owner` and `upc_coercion` install ISN through SQL,
  `StageSchemaLocked` and `StageExtensionLocked` can return to unexported names
  behind `export_test.go`.
