# internal/engine/indexcmds

The index statement executors
([`docs/ENGINE-SPLIT.md`](../../../docs/ENGINE-SPLIT.md) §3, L16): CREATE
INDEX with its CONCURRENTLY phases, ALTER INDEX, DROP INDEX and REINDEX. The
decisions are [`index`](../index/README.md)'s; this package orchestrates them
over core's runtime.

## Layering

It imports [`core`](../core/README.md), [`tablecmds`](../tablecmds/README.md)
(the `TableHost` family host, and `RenameRelationLocked` for ALTER INDEX
RENAME) and the decision packages, never the root. The root composition
imports it; by rule 6 `partitioncmds` may.

Stays in core: maintenance admission (the scope's index maintenance grant
plan), unique and exclusion probing, index publication and root candidates,
the build wait registry (`Engine.IndexWaits`, read by `InspectIndex`), and the
index runtime that `statementHost` reaches.

## Entry points

Dispatch (`core.Statements.Execute`): `ExecuteCreateIndex` and
`ExecuteIndexMaintenance` are entered with nothing held. They take
`Engine.Mu` in short windows, stage and publish through the ordinary catalog
stage, and release it around lock waits and provider validation.
DROP retains the admitted reference path before unlocked effects. Explicit
SQL targets alone receive ORIGINAL; expanded partition indexes still all
participate in admission, retirement and dependency traversal. Index-driven
backing-constraint retirement uses the shared projection owner and marks only
the actual retired constraint NORMAL.

## CONCURRENTLY

After the not-ready index is published, `index_concurrent.go` waits for the
writers that hold the parent (`waitIndexWriters`), builds and publishes the
ready root, waits for writers again, rebuilds the root under a fresh
snapshot and publishes it, waits for the snapshots older than that
snapshot's xmin (`waitOlderIndexSnapshots`), and publishes the index valid.
Each publication rechecks authority and the phase under `Engine.Mu` and
retries after a catalog conflict. Each wait freezes its cohort and registers
it in `Engine.IndexWaits` in one critical section, and clears it on every
return.

## Testing

Tests are external (`package indexcmds_test`) and run on a composed engine:
the root's `engine.New` and the [`enginetest`](../enginetest/README.md)
helpers. `export_test.go` exports the internals they call; add a name there
only when the test cannot assert through SQL or the exported API.

```sh
make check RUN='^Test(BtreeIndex|HashIndex|ExpressionIndex|UniqueIndex|IndexClauses|IndexDefault)'  # CREATE INDEX builds and clauses
make check RUN='^Test(IndexMaintenance|DomainCheckIndex)'  # ALTER INDEX, DROP INDEX, REINDEX
make check RUN='^Test(ConcurrentIndex|TransactionalIndex|Exclusion)'  # CONCURRENTLY waits, transactional DDL, exclusion builds
make check RACE=1 RUN='^Test(ConcurrentIndex|IndexMaintenanceExactLockModes|TransactionalIndexLocks)'  # snapshot cohorts, lock waits
```
