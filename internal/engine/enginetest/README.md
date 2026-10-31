# internal/engine/enginetest

Test helpers over the exported API of `internal/engine/core`: they build
sessions, parse and run statements, and read results. The package is the test
layer of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture):
it imports core and lower packages, and nothing imports it except tests.

## Contract

- **Only `_test.go` files import it**, and only external tests
  (`package x_test`) can: the package imports core, so an internal test of
  core or a package below it would form an import cycle. It never imports the
  root `internal/engine`, whose own tests import it; callers pass the engine
  (`engine.New()`).
- **Exported API only.** Helpers call exported core names and never reach
  unexported state. A helper that needs an accessor stays with its tests.
- **Shared, not copied.** A helper that tests in two or more destination
  packages need lives here, so a moved test keeps its body and only qualifies
  the names. Helpers take `testing.TB` and fail through it.
- **Sessions.** Every session a helper starts takes its identifier from
  `NextSessionID()`, one counter per test binary; tests that start sessions
  themselves use it too, so they never register a duplicate advisory session.
  `Operation` and `RoleOperation` return a cleanup that releases the operation
  and ends the session, and `StartSession` ends it at test cleanup.
  `OpenSession(ctx, e, start, principal...)` exercises real reservation and
  startup while returning errors to lifecycle tests. It selects e's exact
  database and defaults to the trusted postgres login; callers own successful
  session cleanup. `RestoreOperation` instead initializes an explicitly
  private replay owner without a physical database slot or persisted
  startup-default capture.

## Helpers

| Helper | Use |
|---|---|
| `Operation(e, ctx)` | session plus acquired operation and cleanup |
| `Execute(e, ctx, stmt, args)` | run a parsed statement in a fresh session |
| `ExecuteFrame(e, op, stmt, args)` | run on op; positional args are read through each parameter type's input (0A000 for other Go types) |
| `Describe(e, stmt)` | parameter description in a fresh session |
| `Statement(t, sql)` | parse exactly one statement |
| `Run(t, e, sql, args...)` | execute, fail on error, close rows at cleanup |
| `RoleOperation(t, e, name)`, `RoleExecute(t, e, op, sql)` | session authenticated as a login role, and execution on it |
| `Tuples(r)`, `IndependentFinalRows(rows)`, `Export(v)` | read rows through an independent cursor as driver values |
| `Usage(e, id)` | usage of a published type |
| `State(t, err, code)` | require an error with SQLSTATE code |
| `RoleError`, `RoleErrorPosition` | require an exact diagnostic from SQL on an operation |
| `NextSessionID()` | session identifier for a test that starts a session itself |
| `StartSession(t, e, defaults)`, `SessionSQL(t, s, sql)` | session ended at cleanup, and execution in one operation of it |
| `TransactionOperation`, `TransactionBegin`, `TransactionEnd`, `TransactionRows` | explicit transactions on a session, and integer row checks |
| `PreparedDescription(t, e, s, sql)`, `PreparedExecute(e, s, stmt, d)` | describe a statement, then execute it against that expected description |
| `ProtocolReader{Data: b}` | `session.ProtocolInput` over bytes for Bind and fastpath messages; `Position` counts consumed bytes |
| `QueryFixture(t, e, sql)` | table `t` with four rows, the query bound through `core.Describe`, and a captured source costed for the parallel worker alternative |
| `LockEmpty`, `LockPending`, `LockAsync`, `LockAwait` | lock-manager inspection (`Engine.Locks`) and a statement run on its own goroutine |
| `CatalogAuthorization`, `CatalogRef`, `RoleTableRef` | bootstrap-principal authorization and relation references |
| `PublishCatalog`, `ValidateObjectRef` | catalog publication and reference checks outside an operation |
| `ExpressionBinder` | scalar binder over a postgres session |
| `SetObjectACL(e, ctx, ref, acl)` | publish an object or column ACL directly, outside GRANT and REVOKE |
| `Comment(e, r, ref)` | comment metadata of a namespace, type, relation or column, without ACL |

## Stages

The split ([ENGINE-SPLIT.md §2.2](../../../docs/ENGINE-SPLIT.md#22-tests), T4)
fills the package in stages:
1. **Stage 1:** the statement, operation and result helpers.
2. **Stage 2:** shared inspection helpers that tests in two or more
   destinations use (role errors, session SQL and transactions, lock and
   catalog inspection, table refs, binders). The test waves add helpers their
   files share, such as `SetObjectACL` and `Comment`.
3. **Stage 3** (core split, item 207): `NextSessionID`, and the core test
   helpers that two or more census destinations need: `QueryFixture`,
   `PreparedDescription`, `PreparedExecute` and `ProtocolReader`. Core's
   copies of the session and transaction helpers were replaced by the ones
   above.

## Testing

The helpers have no tests of their own; the engine-fixture tests that use them
cover them. Placement of those tests is in
[docs/TESTING.md](../../../docs/TESTING.md#placement-policy).
