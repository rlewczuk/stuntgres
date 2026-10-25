# internal/engine/enginetest

Test helpers over the exported API of `internal/engine`: they build sessions,
parse and run statements, and read results the way the root tests' own
fixture (`engine_test.go`) does. The package is the test layer of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture): it
imports the root and lower packages, and nothing imports it except tests.

## Contract

- **Only `_test.go` files import it**, and only external tests
  (`package x_test`) can: the package imports the root, so an internal test of
  any engine subpackage would form an import cycle. Root `package engine` tests
  keep using `engine_test.go`.
- **Exported API only.** Helpers call exported root names and never reach
  unexported state. A helper that needs an internal is not added here.
- **Same behavior as the root fixture.** Each helper follows its root test
  original (`engine_test.go` and the helper files it names below), so a moved test keeps its body and only qualifies
  the names. Helpers take `testing.TB` and fail through it.
- **Sessions.** `Operation`, `RoleOperation` and `StartSession` start a
  session with an identifier unique within the test binary; the first two
  return a cleanup that releases the operation and ends the session, and
  `StartSession` ends it at test cleanup.

## Helpers

| Helper | Follows | Use |
|---|---|---|
| `Operation(e, ctx)` | `testOperation` | session plus acquired operation and cleanup |
| `Execute(e, ctx, stmt, args)` | `testExecute` | run a parsed statement in a fresh session |
| `ExecuteFrame(e, op, stmt, args)` | `testExecuteFrame` | run on op; positional args are read through each parameter type's input (0A000 for other Go types) |
| `Describe(e, stmt)` | `testDescribe` | parameter description in a fresh session |
| `Statement(t, sql)` | `statement` | parse exactly one statement |
| `Run(t, e, sql, args...)` | `run` | execute, fail on error, close rows at cleanup |
| `RoleOperation(t, e, name)`, `RoleExecute(t, e, op, sql)` | `roleOperation`, `roleExecute` | session authenticated as a login role, and execution on it |
| `Tuples(r)`, `IndependentFinalRows(rows)`, `Export(v)` | `tuples`, `independentFinalRows`, `testExport` | read rows through an independent cursor as driver values |
| `Usage(e, id)` | `testUsage` | usage of a published type |
| `State(t, err, code)` | `state` | require an error with SQLSTATE code |
| `RoleError`, `RoleErrorPosition` | `roleError`, `roleErrorPosition` | require an exact diagnostic from SQL on an operation |
| `StartSession(t, e, defaults)`, `SessionSQL(t, s, sql)` | `sessionStart`, `sessionSQL` | session ended at cleanup, and execution in one operation of it |
| `TransactionOperation`, `TransactionBegin`, `TransactionEnd`, `TransactionRows` | `transactionOperation`, `transactionBegin`, `transactionEnd`, `transactionRows` | explicit transactions on a session, and integer row checks |
| `LockEmpty`, `LockPending`, `LockAsync`, `LockAwait` | `lockEmpty`, `lockPending`, `lockAsync`, `lockAwait` | lock-manager inspection (`Engine.Locks`) and a statement run on its own goroutine |
| `CatalogAuthorization`, `CatalogRef`, `RoleTableRef` | `catalogAuthorization`, `catalogRef`, `roleTableRef` | bootstrap-principal authorization and relation references |
| `PublishCatalog`, `ValidateObjectRef` | `Engine.PublishCatalog`, `Engine.ValidateObjectRef` | catalog publication and reference checks outside an operation |
| `ExpressionBinder` | `newTestExpressionBinder` | scalar binder over a postgres session |
| `SetObjectACL(e, ctx, ref, acl)` | `Engine.SetObjectACL` | publish an object or column ACL directly, outside GRANT and REVOKE |
| `Comment(e, r, ref)` | `Engine.Comment` | comment metadata of a namespace, type, relation or column, without ACL |

## Stages

The split ([ENGINE-SPLIT.md §2.2](../../../docs/ENGINE-SPLIT.md#22-tests), T4)
fills the package in two stages:
1. **Stage 1:** the statement, operation and result helpers, over names the
   root already exported.
2. **Stage 2:** shared inspection helpers that tests in two or more
   destinations use (role errors, session SQL and transactions, lock and
   catalog inspection, table refs, binders), built on the names the export
   pass made public. The test waves add helpers their files share, such as
   `SetObjectACL` and `Comment`.

## Testing

The helpers have no tests of their own; the engine-fixture tests that use them
cover them. Placement of those tests is in
[docs/TESTING.md](../../../docs/TESTING.md#placement-policy).
