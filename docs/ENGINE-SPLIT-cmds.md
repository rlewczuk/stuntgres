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
- 197: not started.
- 198: not started.
- 199: not started.
- 200: not started.
- 201: not started.
- 202: not started.
- 203: not started.
