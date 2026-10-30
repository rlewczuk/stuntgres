# internal/engine/viewcmds

The view, rule, policy and trigger statement executors
([`docs/ENGINE-SPLIT.md`](../../../docs/ENGINE-SPLIT.md) §3, L16): CREATE
[OR REPLACE] VIEW, TEMP views included; ALTER and DROP [MATERIALIZED] VIEW;
REFRESH MATERIALIZED VIEW, ordinary and CONCURRENTLY; CREATE, ALTER and DROP
RULE, POLICY and TRIGGER; the materialized view snapshot import of a native
Restore; and the `ViewHost` family host. The decisions are
[`view`](../view/README.md)'s; this package orchestrates them over core's
runtime.

## Layering

It imports [`core`](../core/README.md),
[`tablecmds`](../tablecmds/README.md) (`TableHost`,
`SkipMissingRelationLocked`, and `RenameRelationLocked` and
`AlterSchemaLocked` for ALTER VIEW RENAME and SET SCHEMA),
[`typecmds`](../typecmds/README.md) (`CaptureDomainReplay`, for snapshot rows
paired with a native domain replay record) and the decision packages, never
the root. In production only the root composition imports it; no family
may (rule 6).

Stays in core: rule programs (rule expansion and execution, which runs DML),
the RLS runtime, trigger firing, CREATE MATERIALIZED VIEW and matview
population on the bound path, view creation preparation and view source
opening, ALTER VIEW OWNER TO (`ExecuteRelationOwnership`), trigger clone
synchronization in `ValidateCatalogStage`, and the view lock waits:
`StatementScope.AcquireBoundViewRelationModes` and `AcquireRelationTarget`,
whose unexported bodies keep the frames that read-only reference fixtures
match.

## Entry points

- Dispatch (`core.Statements.Execute`): `ExecuteCreateView`,
  `ExecuteAlterView`, `ExecuteDropView`, `ExecuteRule`, `ExecutePolicy` and
  `ExecuteTrigger` are entered with nothing held. They authorize and decide
  under `Engine.Mu` (the `view` decisions never wait), release it for lock
  waits and provider validation, recheck what they resolved, and stage,
  validate and publish one catalog stage. RULE interleaves unlocked provider
  admission with the binding of each action, fencing every provider consumed
  so far; RULE, POLICY and TRIGGER publish through one relation-change path,
  whose IF EXISTS skip ends in a fenced NOTICE.
- Dispatch (`core.Statements.ExecuteLocked`): `RefreshMaterializedLocked` is
  a lock-domain continuation: entered with `Engine.Mu` held and the view
  admitted, it releases it on every return. Rows are produced as the view
  owner, with the caller's catalog authority withheld, and the replacement is
  staged against the admitted storage.
- Restore hooks: `ImportMaterializedSnapshots` installs dumped materialized
  view rows into the private Restore engine in one publication; it admits
  only the trusted bootstrap import authority.
  `CaptureRestoredTriggerClone` captures a paired child CREATE's actual identity;
  `CheckRestoredTriggerCloneCreate` rejects subsequent same-target replacement,
  including OID-preserving replacement, without rejecting dependency-only ALTERs.
  `ImportTriggerCloneLinks` validates direct membership, names, row/statement
  kinds and imported owners before one checked native parent-link publication.
- For tests that import one snapshot row, here and in `schemacmds`:
  `ImportMaterializedSnapshotRow`.

Materialized snapshot row execution activates its supplied current invocation
after description/capture unlocks; import capture itself remains dormant.
Concurrent refresh key reconciliation uses only the eligible direct-column keys,
not arbitrary predicate/expression recipes, and preserves ordinary cancellation.

`ViewHost{tablecmds.TableHost}` (`view_host.go`, `policy_expression.go`)
implements `view.Host` and `view.StageHost`; only these executors build it.
The decisions made before a stage exists get a host with no stage.

## Triggers

Trigger definitions and lifecycle utilities publish immutable relation
payloads through the ordinary catalog stage and transaction journal.
Definitions, dependencies and row effects commit together or restore at
rollback and savepoints. CREATE and REPLACE admit ShareRowExclusive on the
root and descendants; rename admits AccessExclusive before mutation.
Addressed DROP and extension metadata use the generic AccessShare target
admission; `view.PlanTrigger` then decides the change under `Engine.Mu`.
Clone synchronization (`view.SynchronizeTriggersLocked`, called from core's
`ValidateCatalogStage`) preserves exact Parent/OID identity and fails a
descendant name collision before publication. Extension retirement uses the
shared dependency closure, including the partition-owner deletion guard;
CASCADE does not grant upward partition deletion authority.

## Testing

Tests are external (`package viewcmds_test`) and run on a composed engine:
the root's `engine.New`, `Reset`, `Dump` and `Restore` and the
[`enginetest`](../enginetest/README.md) helpers. `export_test.go` exports the
internals they call; add a name there only when the test cannot assert
through SQL or the exported API. Trigger DDL units live in
[`schemacmds`](../schemacmds/README.md#testing), since they stage extensions
directly. Public SQL lives in
[reference/views](../../../reference/views/README.md#testing), RLS policies
in [reference/roles](../../../reference/roles/README.md#testing) and trigger
DDL in [reference/operators](../../../reference/operators/README.md#testing).

```sh
make check RUN='^TestMaterialized'                         # matview capture, ALTER, REFRESH, snapshot import
make check RACE=1 RUN='^TestMaterializedRefreshConcurrent'  # concurrent refresh admission and publication races
make check RUN='^TestView'                                 # view DDL, replacement, lifecycle, the ViewHost seam
make check RUN='^Test(RLSPolicy|RuleView|RuleNamed|RuleInference)'  # policy and rule DDL, dumps
make check RUN='^TestVacuumMetadata'                       # VACUUM statistics of materialized views
```
