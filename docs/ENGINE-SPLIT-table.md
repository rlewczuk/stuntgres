# Engine split: `internal/engine/table`, fifth round

Session prompt for linear items 154, 156 and 158 of the
[status page](ENGINE-SPLIT-STATUS.md). The rules are in
[ENGINE-SPLIT.md](ENGINE-SPLIT.md) §2, §3 and §5, and the package contract
is in [the table README](../internal/engine/table/README.md). Sizes were
measured at `fe0683b68`; re-run `reach` on the move set first.

## Item 154 — DROP dependency decisions

Needs 153, because both edit `catalog.go`. About 0.37k lines move: to
`table`, plus small pieces to `view`, `typedef` and `ids`. This is not the
rejected host for the whole DROP pipeline: `table` already calls these
passes through `table.DropDependencyReader`, whose callers hold nothing.

**Moves.**
- `dropProvider.CompositeFieldDependencies` and `typedFieldCheckOwner`
  (`composite_field_dependency.go`), and
  `dropProvider.OwnedExpressionDependencies`
  (`sequence_dependencies.go`). These are two reader methods; they become
  `table` functions over the reader.
- `removeDomainCheck` (`domain_dependencies.go`) becomes
  `typedef.WithoutDomainCheck`. `typedef` also gets `TypedFieldCheckOwner`.
- The removability switch of `dropProvider.removeObjectLocked`
  (`catalog.go`, about L264-330) becomes a `table` function. It runs
  under `Engine.mu`, is pure and takes no lock.
- The pure plan inside `dropProvider.ExpressionDependencies` goes to `view`,
  next to `DropClosure` and `RestrictError`.
- The target selection of `observeIdentityDependencies`
  (`drop_provider.go`).
- Delete `stageCompositeFieldDependencies`, which only tests call.

**Cut and interface changes.**
- `table.DropDependencyReader` gains `Descriptors`, `RemovalDescriptors` and
  `TypedFieldCheckOwner`. It loses `CompositeFieldDependencies` and
  `OwnedExpressionDependencies`, and its doc comment is updated.
- `base.Owner().Provider(class)` replaces `e.catalog.Provider` and
  `role.ParameterProvider`. The check is the same: `validateObjectRefLocked`
  pins the owner, and `engine.go` registers the same
  `role.ParameterProvider`.
- The two inline class-ID lists become `table` predicates.
- `fdw.IsCatalogClass` moves to `ids`, because `table` and `view` cannot
  import `fdw`. Update its `fdw` callers.

**Stays root.** The lock waits and the projection of `ExpressionDependencies`,
`dropProvider` itself and the prepared-drop effect log.

**Risks.**
- `compositeFieldNotice` drops the notice when there is no scope; `Notice`
  would panic there. Make `Notice` safe to call with a nil scope. That
  changes only a path that panics today.
- The reader calls keep their order, so the prepared-drop effect order and
  the notice order stay the same.

**Tests.**
- Root units: `composite_review_remedy_test.go`,
  `domain_alter_constraint_test.go`, `extension_drop_test.go` and
  `pg_depend_test.go` (they name `removeDomainCheck`), plus a grep for the
  other moved names.
- Quick and `REFERENCE=1` selections: `TestReferencePGDepend`,
  `TestReferenceTypedTable`, `TestReferenceAlterDropColumn`,
  `TestReferenceExtensionDropDomainChecks` and
  `TestReferenceCheckSequenceDropTransactionalCandidates`.

**Docs.** Update the table README (the reader methods and the new
functions), the typedef README and the ids README (`IsCatalogClass`).

## Item 156 — VACUUM decisions

Needs —. Item 153 also edits the `initializeMaintenanceLocked` call sites
in `catalog.go`, so rebase over it if it lands first. About 0.49k of VACUUM's
0.89k root lines move: to `table`, which already owns relation options
(including the autovacuum parameters), LOCK TABLE and target admission,
and to `visibility`, `storage` and `pgcatalog`.

**Moves to `table`.**
- All of `vacuum_options.go` (`vacuumOptions`, `normalizeVacuum`,
  `vacuumBoolean`, `vacuumBuffer` and `vacuumOptionError`), already clean,
  with `vacuum_options_test.go`.
- The entry checks of `executeVacuum`: inside a transaction block, FULL and
  ANALYZE.
- The work pass of `vacuumRelation` (`vacuum_publication.go`, about
  L151-212) becomes `table.PrepareVacuumPass(ctx, capture, store, phase)`:
  inspect, materialize, bump the generation, remap the indexes and clone the
  catalog's HasIndexes. `phase` is a root callback that takes `Engine.mu`
  itself to update progress, and it is called with nothing held.
- These Locked functions:
  - `vacuumAllowed`, rewritten over `access.View` and `access.Environment`;
  - `vacuumHeap`, `vacuumChildren`, the breadth-first child expansion and the
    whole-database candidate list of `vacuumTargets`;
  - `vacuumForeignTemp`, with the session token as a value;
  - `captureVacuumIndexesLocked`, over a catalog read and the index roots;
  - the same-capture check, the database frozen-XID bound and
    `vacuumDatabaseOID`.

**Moves to other packages.**
- `visibility.Registry` gets `VacuumCutoffLocked`,
  `VacuumRetirementAllowedLocked` and `InitializeMaintenanceLocked(store)`.
  The last has 7 call sites, 5 in `catalog.go` and 2 in
  `transaction_publication.go`, all under `Engine.mu`; it must not allocate.
- `storage.Maintenance` gets `RecordVacuum` for the statistics update.
  Delete `storage.PrepareVacuum`, which only root tests use, and adapt
  `vacuum_indexes_test.go` and `vacuum_visibility_test.go`.
- `pgcatalog` gets the progress-row visibility rule: the role closure or
  `pg_read_all_stats`.

**Cut.**
- `vacuumNotice` reaches `StatementScope`. The `table` functions instead
  return their warnings, and the root emits them after unlocking, in the
  same order.
- `AuthorizationView.session` comes in as a value.

**Stays root (about 0.4k).**
- `executeVacuum`, the lock loop of `vacuumTargets`, `vacuumAcquire` and
  `resolveVacuumReference`;
- the lock windows, retry and install of `vacuumRelation`;
- `captureVacuumLocked`, `vacuumDatabasePreflightLocked`, the progress map
  and `vacuumCompletion`.

**Risks.**
- The progress phase sequence stays exactly the same, including the
  per-index "processed" updates.
- Notices are still emitted only after unlocking.
- `table` already imports `storage`, `visibility`, `pgcatalog`, `settings`
  and `access`.

**Tests.**
- Root units: the `vacuum_*_test.go` files that use an engine fixture stay
  and are rewritten to the new names. Pure option tests move.
- Quick and `REFERENCE=1` selection: `TestReferenceVacuum`. Add `RACE=1` for
  `TestVacuumPublicationFences` and `TestVacuumAuthorizationLocks`.

**Docs.** Add VACUUM to the table README, the three registry methods to the
visibility README and `RecordVacuum` to the storage README.

## Item 158 — ALTER TABLE pass helpers and the IF EXISTS no-op

Needs —. About 0.16k lines move, and about 0.12k root lines go as
duplicates. All the moved pieces run in the unlocked parts of the
`alterTableLocked` pass loop and take value inputs.

**Moves out of `alter_table.go`** (line numbers are approximate):
- the pass-4 NOT NULL application (L374-426) → `table.AlterNotNullPass`;
- the after-loop finish (L439-469) → `table.FinishAlterCandidate`;
- the relation-options-only path (L131-143);
- the revision bump, written twice (L465-468, L586-590), → one helper;
- the rewrite and row-rewrite flags (L514-517, L625-634) → one `table`
  function;
- the final storage-shape bookkeeping (L830-839);
- the dropped-unique lookup (L710-721) → an `index` helper;
- the added-column fill merge (L655-668, and again in
  `column_projection_change.go`) → `session.StorageChange.MergeFills`.

**Inputs.**
- `inheritancePlan()` may be nil; its methods handle nil.
- Whether the stage is empty comes in as a `bool`.
- `stageDefinitionReader{stage}` is passed as a `catalog.DefinitionReader`.

**Root dedupes.**
- One helper for the stage-to-journal merge loops with the identity-original
  check (L473-511 and L802-829).
- One scope helper for the CHECK comment and visibility snapshot pairs.
- One helper for the lazy stage start, but only where its fields match
  `authorizeLocked`: L281 and L431 set only three of them, so check that
  first.
- Fold the always-nil `project` parameter of the free `dropColumnCandidate`
  into the method.
- The IF EXISTS "relation … does not exist, skipping" block repeats in
  `alter_table.go`, `relation_set_schema.go`, `rename.go`,
  `partition_attach.go`, `partition_detach.go` and `view_alter.go`. Replace
  it with:
  - `session.MissingRelation`, next to `session.ExistingRelation`;
  - a root no-op completion helper, with a flag for the partition variants,
    which unlock before checking the error and skip the context check.

**Rejected.** Turning the whole 10-pass loop into a `table` state machine
would move about 0.25k more lines, but needs about 14 host methods and a
hidden `CatalogStage`: a service locator.

**Tests.**
- Root units: `alter_tablespace_prefix_test.go`,
  `partition_unique_replay_test.go` and the ALTER tests that name the moved
  pieces.
- Quick and `REFERENCE=1` selections: `TestReferenceAlterNullability`,
  `TestReferenceAlterAddColumn`, `TestReferenceAlterDropColumn` and
  `TestReferenceAlterRenameIfExists`, plus one ATTACH/DETACH IF EXISTS
  selection.

**Docs.** Update the table README (the ALTER pass helpers) and the session
README (`MissingRelation`, `MergeFills`).

## Status

- 154: done (`c0aea647f`, `b8912be99`, `ecd030b6e`, then the docs commit).
  - Moved: `CompositeFieldDependencies`, `RemoveDomainCheck`,
    `OwnedExpressionDependencies`, `CheckRemovalLocked` and the
    `ClosureTargetClass`/`RetiredProviderClass` predicates to `table`;
    `DropRoots`, `PlanDrop` (`DropPlan.LockRelations`) and
    `IdentityObservationTargets` to `view`; `WithoutDomainCheck` and
    `TypedFieldCheckOwner` to `typedef`; the SQL/MED class predicate to `ids`.
  - Deviations: the predicate is `ids.IsForeignCatalogClass`, not
    `IsCatalogClass` (every class ID is a catalog class); `fdw` had no other
    callers. `table.RemoveDomainCheck` (Replacement, `WithoutDomainCheck`,
    Revision, Replace over the reader) is new: the composite pass, the root
    expression pass and `ddlHost.RemoveDomainCheck` share it. The retirement
    roots of `ExpressionDependencies` moved too (`view.DropRoots`), and the
    partition-drop lock loop iterates `DropPlan.Hierarchy`, which holds the
    same edges in the same order. The Parameter case uses the registered
    provider of `ids.ParameterClassID`.
  - `dropProvider.Notice` now drops a notice when the provider has neither a
    preparation nor a scope, as `compositeFieldNotice` did; that path
    panicked before.
  - Tests: the four root units that named `removeDomainCheck` call
    `ddlHost.RemoveDomainCheck`; `composite_review_remedy_test.go` calls
    `table.CompositeFieldDependencies` instead of the deleted
    `stageCompositeFieldDependencies`. New `typedef` unit
    `TestWithoutDomainCheck`.
- 156: done (`990a57741`, `c37b29f2a`, `afda91bc2`, then the docs commit).
  - Moved: the options, entry checks, target selection (`VacuumSelection`),
    work gates and the detached work pass to `table`; `VacuumCutoffLocked`,
    `VacuumRetirementAllowedLocked` and `InitializeMaintenanceLocked` to
    `visibility.Registry`; `Maintenance.RecordVacuum` to `storage`
    (`PrepareVacuum` deleted); the progress-row rule to
    `pgcatalog.VacuumProgressViewer`.
  - Deviations: `InitializeMaintenanceLocked` had 6 call sites (item 153
    merged two). Two pure helpers beyond the list moved too: `VacuumWork` (the
    post-grant gates of `vacuumRelation`) and `VacuumReport` (the VERBOSE
    text). The pass also covers the "scanning heap" and "performing final
    cleanup" phase updates around today's L151-212, so the phase sequence is
    unchanged. `vacuumCompletion` keeps its name: test barriers match it.
  - Tests: `vacuum_options_test.go` moved; `vacuum_indexes_test.go` and
    `vacuum_visibility_test.go` compose `InspectVacuum` and
    `MaterializeVacuum` in a local `vacuumScan` helper instead of
    `storage.PrepareVacuum`.
  - Note: `AuthorizationView` is now boxed into `access.Environment` at each
    `table` call, as LOCK TABLE does, so a superuser's VACUUM makes about one
    small allocation per target that it did not make before; other users
    already paid it inside `Effective`. Startup and ordinary statements are
    unchanged.
- 158: done (`6e5d68df3`, `87059b4e9`, `484e930e7`, then the docs commit).
  - Moved: `AlterNotNullPass`, `FinishAlterCandidate` (`AlterFinishInput`,
    `AlterFinish`), `BumpAlterRevision`, `RelationOptionsOnlyCandidate`,
    `AlterRewrites` and `AlterStorageShape` to `table`
    (`alter_table_finish.go`); `DroppedUniqueOIDs` to `index`;
    `StorageChange.MergeFills` and `MissingRelation` to `session`.
    `alter_table.go` 899 → 701 lines.
  - Root dedupes: `mergeStageJournalLocked` (both stage-to-journal loops),
    `captureCheckSnapshots` (the three ALTER pairs and the one in
    `typed_mutation.go`), `authorizedStage` (five lazy stage starts) and
    `skipMissingRelationLocked` (`relation_if_exists.go`, six IF EXISTS
    blocks). The free `dropColumnCandidate` folded into the method.
  - Deviations: `FinishAlterCandidate` takes the inheritance finish as a
    callback (`scope.finishInheritanceAlterCandidate`) instead of the plan,
    because the root reads the inheritance stage only when the node
    recurses. The `op.Definition` restore now precedes the hierarchy
    relation-options error, which is equivalent because a deferred restore
    covers every error. `ownership.go` and `sequence.go` reuse
    `MissingRelation` but keep their own control flow.
  - Finding for 167: the ADD UNIQUE and pass-6 CHECK lazy stages set only
    the session, authorization and role revisions, not the database
    authority of `authorizeLocked`, and the USING INDEX stage at pass 1 sets
    none. Unifying them would add the database-compatibility fence, which is
    a behaviour change, so they stay as they are.
  - Tests: `pg_attribute_test.go` calls the method. New units:
    `TestStorageChangeMergeFills*` (session) and
    `TestDroppedUniqueOIDsFollowActionOrder` (index).
