# internal/engine/view

The code that *uses* stored queries and needs no engine: view, rule, policy and
trigger DDL decisions, rule rewriting, writes through auto-updatable views, materialized
refresh and snapshots, RLS layer selection, execution-time query preflight, the
privilege and lock walks over view definitions and the read side of DROP.

## Imports

An upper feature in [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It imports
`access`, `analyze` (types and pure functions only), `authorization`, `catalog`,
`ddl`, `exec`, `expr`, `functions`, `ids`, `index`, `lock`, `pgcatalog`, `plan`,
`regobj`, `routine`, `sequence`, `session`, `storage`, `stored`, `table`, `typedef`,
`visibility`, `parser`, `sqlstate`, `sqltype` and the standard library. It never
imports the engine, `fdw`, `partition`, `role`, `dml` or `control`, and it never
builds a binder.

The stored definitions (`stored.View`, `stored.Rule`, `stored.Policy`), their SQL
rendering, dumps and remap are in [`stored`](../stored/README.md). `analyze`
reaches views only through its host (`StoredViewVolatile`,
`ValidateViewReadGraph`, `CaptureRetainedViewInput`, `BindViewWrite`) and never
imports `view`.

## What it owns

- **DDL decisions** (`host.go`, `create.go`, `alter_drop.go`, `rule_ddl.go`,
  `policy_ddl.go`, `trigger_ddl.go`, `trigger_lifecycle.go`, `definition.go`):
  `Host` (stage-less: allocation, the access view, relation resolution, the
  temporary owner, function selection and the rule, policy and trigger WHEN
  binders) and `StageHost` (`Host` plus `ddl.Host` with row-type staging),
  both implemented by [`viewcmds`](../viewcmds/README.md)' `ViewHost`. CREATE
  OR REPLACE VIEW is
  `CreateTarget`, `CheckCreate` and `StageCreate`; ALTER VIEW is `AlterChange`;
  DROP VIEW is `DropTargets`, then `RecheckDropTargets` after the waits. RULE DDL
  uses `BeginRule` and `RulePlan.BindCondition`, `BindAction`, `Finish`; POLICY
  uses `PlanPolicy`. They produce a `RelationChange` for `viewcmds` staging
  and publication. Each rule phase holds Engine.Mu and never waits.
  `viewcmds.ExecuteRule` admits event AX first, binds the qualification, then interleaves unlocked provider admission
  with each action's canonical binding in order. TRIGGER DDL (CREATE [OR
  REPLACE], ALTER TRIGGER RENAME and [NO] DEPENDS ON EXTENSION, DROP) is
  `PlanTrigger`; its `RelationChange.Replacement` names the definition an OR
  REPLACE replaces. Creation checks table TRIGGER and function EXECUTE grants;
  lifecycle ownership requires table authority, which a rename checks before
  the trigger's existence. `FreezeDefinition` freezes view queries.
- **Trigger partition synchronization** (`trigger_sync.go`):
  `SynchronizeTriggersLocked` keeps partition trigger clones in step with
  their parents at every catalog publication (CREATE, ATTACH, DETACH and
  parent replacement). Its inputs are the stage's `*catalog.Candidate`, a
  callback that reads the candidate's current state, a `ddl.Allocator` for
  clone OIDs and revisions, and the stage's `TriggerReplacement`. The
  partition-clone provenance rules live in [`stored`](../stored/README.md).
- **Query preflight and join legality** (`query_preflight.go`,
  `query_set_preflight.go`, `query_join_legality.go`): `PreflightQuery` copies
  the plan paths that hold folded coercions for one execution;
  `PrepareStoredExecutionRoot` preflights a stored view for its demanded outputs;
  the `Validate*JoinExecution` and `ValidateMarkedQueryJoin` checks. They run on
  execution-owned parameters, never at Prepare, and are the bodies behind the
  root's `exec` preparation forwarders.
  Retained CTE definition edges are not separately admitted as invocations:
  referenced CTEs admit their actual preflighted roots, including arm elimination.
  Named-window key expressions still demand current-invoker EXECUTE even when
  the window is unused. Complete analysis graphs, original routine lifetimes,
  static diagnostics, dependencies and namespace fences remain independent of
  that invocation decision.
- **Privilege walkers** (`write_authority.go`, `source_authority.go`,
  `query_table_refs.go`): `CheckWrite` (a write through a view: the logical
  target, then each layer under its owner or the invoker), `AuthorizeSource`,
  `CheckRowMarks`, `QueryTableRefs` (authorizes every executable view occurrence,
  then expands bases, partitions and inheritance children into the relations to
  capture) and `CheckRowSecuritySource`. They take the captured `access.View` and
  its `access.Environment`; they check only relations and columns, so the
  caller's environment serves every nested principal.
- **Rules** (`rule_*.go`): `RuleRewriter.ExpandLocked` (INSTEAD, ALSO and
  conditional INSTEAD expansion with recursion detection), the programs
  `RuleProgram` and `RuleCommand`, `RuleAuthority` (the opaque statement
  authority a rewritten command carries, implemented by the engine's
  `AuthorizationView`), rule CTE normalization (`NormalizeRuleCTEsLocked`) and
  the rule DDL and dependency checks.
  Rule-added relation access uses the event relation owner, but callable actions
  retain the original routine invoker. A NEW graft loans only the containing
  DEFAULT's certified original callable references and effective revisions;
  source recipes, stored actions and their dependency slices remain immutable.
- **Writes through views** (`write*.go`, `binding_slots.go`,
  `default_origin.go`): `Write` (base relation field `BaseRelation`, because
  `Base()` is the `analyze.ViewWriteSlot` method), `WriteLayer`, `BindViewWrite`,
  `FreezeWriteSources` and the `analyze` slot helpers (`ViewWriteOf`,
  `RuleCTEsOf`, ...). `StoredQueryImporter` and `Write.GraftQuery` share one
  frozen-occurrence graft: it translates frozen read attributes to live uses once
  per occurrence, keeps required view keys demanded after renames, and still
  prunes unrequested projections.
  Frozen standard-routine actions use the same source-layout bridge as rules
  and view writes, including MERGE source openings with unchanged targets.
  Raw prefixes are selected before wrapping; executors close the returned bundle
  head, not only its pre-bridge input, after ordinary source finalization.
  Source openings consult each leaf's retained attributes, not rule-template
  status; nil/identical layouts allocate neither a bridge nor scratch.
  Routine creation also captures the actual modifying-CTE child bindings through
  the existing memoized immutable rule-tree mapper. Child executable source lists,
  declarations and scans stay connected without mutating a published graph.
  Routine presentations project all ten occurrence maps by checked original raw
  expression roots, using the stored validator's source enumeration. The
  synthetic target row is lexical authority, not a raw source owner; genuine
  frame-1 FROM/USING VALUES, function and JOIN-ON captures remain eligible.
  Shared SELECT-CTE owners need not be disjoint, and every original capture key
  must have an owner. Recursive path/count validation remains strict. Original
  captures, namespaces, dependencies and read/target authority stay intact.
  `VisitRuleModifiers` shares actual definition enumeration between freezing and
  validation; each routine presentation retains its exact `ActionSyntax`.
  Their original global source ordinals remain unchanged. Main and associated
  child roots jointly cover the full action source manifest; a child-only source
  is validated through its actual modifier graph, not a fabricated SELECT edge.
- **Column metadata** (`column_updatability.go`): `ColumnUpdatable` computes
  structural UPDATE plus DELETE capability from the supplied immutable catalog.
  It unions unconditional INSTEAD rules with automatic capability, maps direct
  outputs through frozen stable attributes and checks cancellation/recursion.
  It ignores triggers and caller ACLs, never binds or executes a mutation, and
  obtains foreign event capabilities through an effect-free callback rather
  than importing `fdw`.
- **Structural capabilities** (`capabilities.go`): `Capabilities` computes
  UPDATE/INSERT/DELETE bits from immutable relation identity, actual
  unconditional INSTEAD rules and stored write shape. Nil output subsets mean
  all; explicit empty subsets mean none. Direct outputs map frozen slots to
  current stable attributes before recursive base intersection. Recursion tracks
  ancestry, not a relation-only memo; stale identities/attributes are errors.
  The engine supplies builtin and foreign handler facts through `CapabilityLeaf`.
  No binder, provider callback, data read, permission probe or lock is involved.
- **View records and checks** (`view.go`, `alter.go`, `dependencies.go`,
  `options.go`, `query.go`): creation scope, output-name and collation checks,
  `ValidateViewDefinition`, `ViewDepends`, `TransformViewOptions` and
  `StoredProjectionDemand`.
- **Lock walks** (`source_locks.go`, `query_locks.go`): `SourceWalk.Closure`
  walks the view definitions reachable from a statement's targets and leaves and
  returns a `SourceClosure` (strongest row mode per relation, relations still to
  acquire, temporary views to revalidate); `QueryLocks` validates the read graph
  and row-lock legality and returns a `QueryLockDemand`; `CurrentQuerySource`
  revalidates an admitted leaf's exact identity. They read the statement's
  private `catalog.Read`; the root acquires the locks.
  New or changed stored views also admit their checked routine dependencies
  through the root's shared definition-provider collector: permanent views use
  the ordinary publication hook; explicit and inferred TEMP views admit before
  direct overlay publication. Actual procedure addresses retain AccessShare
  until transaction end, conflict with retirement, and are revalidated after a
  wait. Quoted source and unrelated providers add no locks.
- **Stored view sources** (`stored_sources.go`): `FreezeStoredSources` installs
  the frozen-layout bridges and stored references of captured sources.
- **DROP read side** (`drop_*.go`, `dependents.go`): `DropReader` is the drop
  stage that the root's `DropProvider` implements. `DropClosure` is the
  dependency closure (edges are `table.DomainDependency`); `DependentViews`,
  `KeepRules`, `KeepPolicies` and the `Drop*Dependents` helpers find dependents.
  Discovery admits each reached object's genuine bootstrap pin before ordinary
  RESTRICT reporting or CASCADE effects, using the current staged definition
  input and the shared pin authority. A pinned dependent cannot be retired even
  when its current dependency points to an unpinned user provider; rejection
  names the pinned object without ordinary dependency DETAIL or HINT.
  Ordinary routine edges come from the shared catalog definition visitor, not
  the complete runtime capture manifest. Exact positive relation-attribute roots,
  procedure-only roots and domains use descending discovery/reverse postorder:
  shared descendants retain their actual diagnostic parent and retire once.
  Column roots never widen to the containing relation or row type. Mixed
  containing-owner/member roots retain their owner's traversal; contained
  procedures never change extension/schema retirement ordering.
  `TypeChangeError` rejects ALTER COLUMN TYPE under a rule or view. For the
  root's expression-dependency pass, `DropRoots` adds the relations the drop
  already removed, `PlanDrop` classifies the closure into targets, surviving
  relations and inheritance owners (`DropPlan.LockRelations` are the ones a
  statement locks). `ColumnDropEffects` defers local AUTO payload for the original
  column and every captured surviving column; full edges and targets remain
  diagnostic, observation and admission authority. Indexes stay readable through
  metadata pruning. Owned-sequence adjacency uses the exact `table.SequenceOwnedBy`
  predicate when the current target or original traversal root is a positive
  relation attribute. Column effects preflight sequence descendants in the same
  closure; whole-provider/whole-target pairs retain the incumbent later owned
  phase. Captured trigger payload identities remain execution targets even for
  automatic-only retirement; their owners are not extra physical drop targets.
  Mixed siblings sort by genuine dependent identity; declaration ordering permutes
  only same-owner CHECK slots on their actual immediate target. Reverse-postorder
  families use inverse CHECK visitation without moving unrelated mixed siblings
  or changing reached parents.
  `IdentityObservationTargets` selects provider fences. These functions do not
  wait or publish; both provider adapters share the same effect-only executor.
- **Materialized views** (`materialized_*.go`): refresh scope and stage records,
  concurrent-refresh row matching and index eligibility, the replacement cores
  and the snapshot codec (`EmitMaterializedSnapshot`,
  `DecodeMaterializedSnapshot`, the `stuntgres-matview-v1` comment) with its
  import checks. For each bound snapshot row INSERT,
  `MaterializedSnapshotEnvironment` checks the binding shape and the builtin
  `array_fill`/`array_cat` providers and returns the input environment with
  the statement resolvers; `EvaluateMaterializedSnapshotRows` evaluates the
  rows into the column types and checks constructor array bounds.
- **Policies and RLS** (`policy*.go`, `rls_base.go`, `row_security*.go`): policy
  validation and dependencies, `RowSecurityLayers`, the `row_security` setting
  walkers and the command state `RowSecurity`, which borrows OLD/NEW
  `exec.RLSProgram`s. `ImportPolicyClause` (`policy_import.go`) grafts a
  selected policy clause onto its consumer and returns a `PolicyClauseImport`:
  the expression, its binding (sources run as the policy's user, without
  storage), the nested-view `SourceClosure` and the relations to acquire.
  `CheckPolicyAuthority` rechecks the policy's sources against the view's
  current catalog and rights, as its current user.
- **Target admission** (`target_admission.go`): `TargetAdmission`, the
  `table.TargetEntry` of RULE, POLICY and VIEW DDL.

## What stays in the engine

Dispatch is the root's. [`viewcmds`](../viewcmds/README.md) holds the
statement executors (`ExecuteCreateView`, `ExecuteAlterView`,
`ExecuteDropView`, `ExecuteRule`, `ExecutePolicy`, `ExecuteTrigger` and
`RefreshMaterializedLocked`), which keep the statement authority, the IF EXISTS
NOTICEs, the stage lifecycle, the waits and publication; the host bodies
(`view_host.go`); refresh row production and staging; and
`ImportMaterializedSnapshots` with the per-row statement scope and binder.
Core holds `Engine.Mu`, the lock waits (`view_locks.go`),
`prepareViewCreation`, rule program execution (it runs DML), view source
opening (`view_source.go`), the policy import's storage, lock waits, authority
refresh, snapshots and source preparation, the row-security authority wrappers
and `DropProvider`. The `SynchronizeTriggersLocked` call stays in core's
`ValidateCatalogStage` (`catalog.go`).

## Invariants

- Rule rewrite order: INSTEAD, ALSO and conditional INSTEAD rules expand in a
  fixed order, with recursion detection by object and event ancestry, a fixed
  command count and RETURNING provider. The composed action runs as the rule
  owner; the caller's authority is restored for the residual.
  Stored call lifetimes belong to each original condition/action presentation,
  checked against its actual live rule slot before composition. NEW substitution
  does not promote fresh producer calls into stored authority. Current generic
  rule-reference revisions serve invalidation, not historical call admission.
  Definition planning and rewriting use the owner's current private catalog and
  storage. Replacements retain exact typed action-provider dependencies; ordinary
  rules and the implicit view `_RETURN` payload remain separate. Savepoint
  restoration selects the restored live slot without borrowing recipe authority.
  Across unlocked definition admission, `viewcmds.ExecuteRule` retains the
  initial authorization epoch and event-name observation separately from the granted event identity.
  Later phases validate both through the existing publication and admission
  fences; refreshed private provider views cannot restamp earlier authority or
  retarget an event whose lexical resolution changed.
  Query-owned grouping and window peer-equality keys keep their own original
  declaration/reference/recipe even when NEW replaces the key's expression.
  Admission selects that exact occurrence's recipe on a local context copy and
  passes the original declaration to the shared executor fence. An unmarked
  fresh key cannot inherit enclosing graft authority. Private semantic-tree
  keys have no callable equality authority.
- Messages, SQLSTATEs, details and hints are stable, including the first error
  when several rules or policies fail.
- Concurrent refresh keeps the unique-index requirement, duplicate detection and
  source lineage checks. Snapshot comments (`stuntgres-matview-v1`) are
  byte-stable and decode the same way.
- `Host` and `StageHost` methods run with `Engine.Mu` held, never take or release
  it and never wait; the decisions never begin, publish or discard a stage.
  `StageCreate` writes only to the stage it is given.
- `SynchronizeTriggersLocked` runs under `Engine.Mu` on every publication and
  takes no lock. Its first step is the exit for publications in which no
  relation has or inherits triggers, so those publications pay nothing; the
  root passes a pointer-shaped allocator for the same reason. Clones keep
  their Parent and OID identity, a descendant name collision fails before
  publication, and an OR REPLACE displaces a local child definition only
  along the `TriggerReplacement` lineage.
- The package holds no engine state and no mutex, and its functions never wait.
  `RuleRewriter.ExpandLocked` reads live storage through the rule authority, so
  its caller (and `NormalizeRuleCTEsLocked`'s) holds `Engine.Mu`.
- **Snapshot import admission.** Import binds every scalar through the ordinary
  typed binder and accepts only the closed shapes the emitter writes, with a full
  destination-ordered column list; anything else is rejected. Admitted array
  calls must keep the genuine builtin provider identity (same-named overloads
  gain no codec authority), and only authentic stored domain NULLs authorize
  private cell transport.

## Testing

Package units cover the slot helpers and the target-admission entry. Every other
view, rule, policy, trigger DDL, materialized-view and query preflight unit builds an
engine and names the code as `view.X`: view, policy, rule DDL and
materialized-view units live in `internal/engine/viewcmds`, RLS, rule
expansion and row-lock view units in `internal/engine/core`, and trigger DDL
units in `internal/engine`. `TestViewHostSeam`
and `TestDropReaderSeam` pin the two seams, and the
`TestMaterializedViewSnapshot*` units cover snapshot import admission. Public SQL
lives in [reference/views](../../../reference/views/README.md#testing) and RLS in
[reference/roles](../../../reference/roles/README.md#testing); add `REFERENCE=1`
for the oracle.

```sh
make check RUN='^Test(ViewHostSeam|DropReaderSeam|ViewTargetAdmission|BindingSlots|ViewWriteSlot|MaterializedViewSnapshot)'
make check RACE=1 RUN='^TestMaterializedRefreshConcurrent'
make check RUN='^TestReference(View|Rule|Materialized)'
make check RUN='^TestReferenceRLS'
make check RUN='^TestReference(TriggerDDL|TriggerCore|TransactionalTrigger)'
make check RUN='^TestViewCapabilities(ColumnSubset|RuleEvents|ForeignEvents)$'
```
