# Engine split: `internal/engine/view`, sixth round

Session prompt for linear items 171 and 172 of the
[status page](ENGINE-SPLIT-STATUS.md). The rules are in
[ENGINE-SPLIT.md](ENGINE-SPLIT.md) §2, §3 and §5, and the package contract
is in [the view README](../internal/engine/view/README.md).

Trigger DDL has the shape of `view.PlanRule` and `view.PlanPolicy`:
- the same relation resolution;
- the same `view.RelationChange`;
- the same root `publishRelationChangeLocked` (`rule_ddl.go`).

So `view` owns its decisions. The trigger payload stays in `stored`
(`Trigger`, `TriggerList`, `CloneTrigger`) and `catalog` (`TriggersPayload`),
because `table`, `partition` and `view` all validate it. The firing contract
stays in `dml`. Sizes were measured at `ea8c445fe`; re-run `reach` with
`-files '^trigger_'` first.

## Item 171 — trigger DDL decisions

Needs —. About 0.25k lines move.

**Moves.** The decision bodies of `Engine.executeTrigger`
(`trigger_ddl.go`, CREATE [OR REPLACE] TRIGGER) and
`Engine.executeTriggerLifecycle` (`trigger_lifecycle.go`: ALTER TRIGGER
RENAME, ALTER TRIGGER ... DEPENDS ON EXTENSION and DROP TRIGGER) become one
`view.PlanTrigger(h Host, auth ddl.Authority, stmt parser.Statement, locked ids.ObjectRef, input sqltype.InputEnvironment) (RelationChange, error)`.
It covers function selection, WHEN binding, the payload build, the
replacement checks, rename and drop. An IF EXISTS miss is
`RelationChange.Skip`, as for rules and policies.

**Cut.**
- `view.Host` gains `SelectFunction` and `BindTriggerWhen`. `ddlHost`
  already implements both (`routine_host.go`, `view_host.go`), so the
  adapter gets no new code. Add the compile-time assertion if it is missing.
- Authorization checks go through the existing `h.AccessView(auth)` or
  `ddl.Check`.
- The search path comes from `auth.ReferencePath()`.
- `RelationChange` gains `Replacement` (the relation and OID of a CREATE OR
  REPLACE). `triggerReplacement` moves to `view` as that field's type, and
  `publishRelationChangeLocked` loses its fourth parameter.

**Stays root.**
- the lock acquisition, including `acquirePartitionDescendants` before the
  lock;
- the argument-count check;
- `statementAuthorizationLocked`;
- publication;
- `trigger_authorization.go`.

That is about 35 lines of skeleton.

**Callers and locks.** `PlanTrigger` runs under `Engine.mu`, like
`PlanRule`, and takes no lock. Keep today's error precedence exactly. Diff
the order of checks in the moved bodies against `trigger_ddl.go` and
`trigger_lifecycle.go` line by line.

**Tests.**
- Root units: `trigger_ddl_test.go` and `trigger_review_test.go` are
  rewritten to the new names where they reach the moved code. Fixture tests
  that need an engine stay.
- Quick and `REFERENCE=1` selection: `TestReferenceTriggerDDL` (all seven)
  and `TestReferenceTriggerCore(Definition|Diagnostics|Privileges|When)`.

**Docs.** Add trigger DDL to the view README's decision list. Move the
trigger-lifecycle paragraph of the root README there, leaving the lock and
publication sentences.

## Item 172 — trigger partition synchronization

Needs 171. About 0.18k lines move.

**Moves.** `Engine.synchronizeStageTriggersLocked` (`trigger_stage.go`)
becomes `view.SynchronizeTriggersLocked`, a free function. It maintains the
partition clone provenance at catalog publication: CREATE, ATTACH, DETACH
and parent replacement. Its inputs:
- the stage's `*catalog.Candidate`;
- a `read func() catalog.Read` callback over `stageRead`;
- a `ddl.Allocator` (`ddlHost{e: e}`);
- the stage's `view` replacement value.

The partition-clone provenance rules of the stored README stay there (they
describe the payload); the view README gains the synchronization steps.

**Cut.** Value inputs only (§2.2 rule 6). Check with `reach -cut` that the
receiver, `CatalogStage` and `stageRead` edges are the only ones.

**Stays root.** The call in `ValidateCatalogStage` (`catalog.go`).

**Callers and locks.** It runs under `Engine.mu` on every catalog
publication and takes no lock. The early "no relation has or inherits
triggers" exit must stay first, so publications without triggers pay
nothing.

**Bench.** `make bench` before and after, plus a DDL-heavy root benchmark if
one exists. Allow no new allocation on a publication without triggers.

**Tests.**
- Root units that call `synchronizeStageTriggersLocked`, or build its stage
  by hand, move to `view` over a builtin catalog or are rewritten.
- Quick and `REFERENCE=1` selection: `TestReferenceTriggerDDLPartition`,
  `TestReferenceTriggerCorePartitionReplace` and
  `TestReferenceTriggerDDLTransactions`.

**Docs.** The view README gains the synchronization entry point and its
invariants.

## Status

- 171: done (`72e54064b`, `8f6aeb486`, docs commit). `view.PlanTrigger`
  (`trigger_ddl.go`, `trigger_lifecycle.go`) decides all trigger DDL; the
  root keeps a 35-line `executeTrigger`. Deviations: `executeTrigger` and
  `executeTriggerLifecycle` merged into one root skeleton, and the CREATE
  argument-count check now runs before `Engine.mu` (a pure check, so error
  precedence is unchanged). `TriggerReplacement` sits in `host.go` beside
  `RelationChange`. No root unit named the moved methods; the
  `TestTriggerCoreWhenProvider` binder call now goes through `view.Host`.
  For 172: `CatalogStage.triggerReplacement` is now a
  `view.TriggerReplacement` value. For 180: the transactional trigger,
  policy and rule commits (`7ae826adb`, `cae4fce3f`, `6bd13811c`) added root
  production code, mostly to `rule_ddl.go`, `relation_scope.go` and
  `view_locks.go`.
- 172: done (`08398dbea`, `578a40386`, docs commit).
  `view.SynchronizeTriggersLocked` (`trigger_sync.go`) takes the stage's
  `*catalog.Candidate`, a stage-read callback, a `ddl.Allocator` and the
  `TriggerReplacement`. Deviation: the root passes `&e.catalog` rather than
  `ddlHost{e: e}`. `catalogStore` gains `AllocateOID` and `Revision`, so the
  interface holds a pointer. A three-word `ddlHost` leaks through the
  interface method calls and would allocate on every publication, including
  trigger-free ones. No root unit named the function or built its stage. For
  177: `catalog.Store` takes over the `ddl.Allocator` methods with the store.
