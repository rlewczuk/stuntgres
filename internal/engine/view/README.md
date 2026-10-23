# internal/engine/view

The code that *uses* stored queries and needs no engine: view, rule and policy
DDL decisions, rule rewriting, writes through auto-updatable views, materialized
refresh and snapshots, RLS layer selection, execution-time query preflight, the
privilege and lock walks over view definitions and the read side of DROP.

## Imports

An upper feature in [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It imports
`access`, `analyze` (types and pure functions only), `authorization`, `catalog`,
`ddl`, `exec`, `expr`, `functions`, `ids`, `index`, `lock`, `pgcatalog`, `plan`,
`routine`, `sequence`, `session`, `storage`, `stored`, `table`, `typedef`,
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
  `policy_ddl.go`, `definition.go`): `Host` (stage-less: allocation, the access
  view, relation resolution, the temporary owner and the root-built rule and
  policy binders) and `StageHost` (`Host` plus `ddl.Host` with row-type staging),
  both implemented by the root's `ddlHost`. CREATE OR REPLACE VIEW is
  `CreateTarget`, `CheckCreate` and `StageCreate`; ALTER VIEW is `AlterChange`;
  DROP VIEW is `DropTargets`, then `RecheckDropTargets` after the waits. RULE and
  POLICY DDL is `PlanRule` and `PlanPolicy`, which return a `RelationChange` that
  the root stages and publishes. `FreezeDefinition` freezes view queries.
- **Query preflight and join legality** (`query_preflight.go`,
  `query_set_preflight.go`, `query_join_legality.go`): `PreflightQuery` copies
  the plan paths that hold folded coercions for one execution;
  `PrepareStoredExecutionRoot` preflights a stored view for its demanded outputs;
  the `Validate*JoinExecution` and `ValidateMarkedQueryJoin` checks. They run on
  execution-owned parameters, never at Prepare, and are the bodies behind the
  root's `exec` preparation forwarders.
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
- **Writes through views** (`write*.go`, `binding_slots.go`,
  `default_origin.go`): `Write` (base relation field `BaseRelation`, because
  `Base()` is the `analyze.ViewWriteSlot` method), `WriteLayer`, `BindViewWrite`,
  `FreezeWriteSources` and the `analyze` slot helpers (`ViewWriteOf`,
  `RuleCTEsOf`, ...). `StoredQueryImporter` and `Write.GraftQuery` share one
  frozen-occurrence graft: it translates frozen read attributes to live uses once
  per occurrence, keeps required view keys demanded after renames, and still
  prunes unrequested projections.
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
- **Stored view sources** (`stored_sources.go`): `FreezeStoredSources` installs
  the frozen-layout bridges and stored references of captured sources.
- **DROP read side** (`drop_*.go`, `dependents.go`): `DropReader` is the drop
  stage that the root's `dropProvider` implements. `DropClosure` is the
  dependency closure (edges are `table.DomainDependency`); `DependentViews`,
  `KeepRules`, `KeepPolicies` and the `Drop*Dependents` helpers find dependents;
  `TypeChangeError` rejects ALTER COLUMN TYPE under a rule or view.
- **Materialized views** (`materialized_*.go`): refresh scope and stage records,
  concurrent-refresh row matching and index eligibility, the replacement cores
  and the snapshot codec (`EmitMaterializedSnapshot`,
  `DecodeMaterializedSnapshot`, the `stuntgres-matview-v1` comment) with its
  import checks.
- **Policies and RLS** (`policy*.go`, `rls_base.go`, `row_security*.go`): policy
  validation and dependencies, `RowSecurityLayers`, the `row_security` setting
  walkers and the command state `RowSecurity`, which borrows OLD/NEW
  `exec.RLSProgram`s.
- **Target admission** (`target_admission.go`): `TargetAdmission`, the
  `table.TargetEntry` of RULE, POLICY and VIEW DDL.

## What stays in the engine

Dispatch, `Engine.mu`, lock waits (`view_locks.go`) and `prepareViewCreation`.
The handler skeletons (`executeCreateView`, `executeAlterView`,
`executeDropView`, `executeRule`, `executePolicy`) keep the statement authority,
the IF EXISTS NOTICEs, the stage lifecycle, the waits and publication. Also the
host bodies (`view_host.go`), rule program execution (it runs DML), view source
opening (`view_source.go`), refresh row production and staging,
`importMaterializedSnapshots`, the row-security authority wrappers and
`dropProvider`.

## Invariants

- Rule rewrite order: INSTEAD, ALSO and conditional INSTEAD rules expand in a
  fixed order, with recursion detection by object and event ancestry, a fixed
  command count and RETURNING provider. The composed action runs as the rule
  owner; the caller's authority is restored for the residual.
- Messages, SQLSTATEs, details and hints are stable, including the first error
  when several rules or policies fail.
- Concurrent refresh keeps the unique-index requirement, duplicate detection and
  source lineage checks. Snapshot comments (`stuntgres-matview-v1`) are
  byte-stable and decode the same way.
- `Host` and `StageHost` methods run with `Engine.mu` held, never take or release
  it and never wait; the decisions never begin, publish or discard a stage.
  `StageCreate` writes only to the stage it is given.
- The package holds no engine state and no mutex, and its functions never wait.
  `RuleRewriter.ExpandLocked` reads live storage through the rule authority, so
  its caller (and `NormalizeRuleCTEsLocked`'s) holds `Engine.mu`.
- **Snapshot import admission.** Import binds every scalar through the ordinary
  typed binder and accepts only the closed shapes the emitter writes, with a full
  destination-ordered column list; anything else is rejected. Admitted array
  calls must keep the genuine builtin provider identity (same-named overloads
  gain no codec authority), and only authentic stored domain NULLs authorize
  private cell transport.

## Testing

Package units cover the slot helpers and the target-admission entry. Every other
view, rule, policy, materialized-view and query preflight unit builds an engine
and stays in `internal/engine`, naming the code as `view.X`; `TestViewHostSeam`
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
```
