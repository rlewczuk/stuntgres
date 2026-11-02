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
  Detached catalog fixture rows use `CatalogTransaction` for a genuine admitted
  source owner. This constructor never commits. After explicitly publishing the
  stage, `CommitCatalogTransaction` finishes its otherwise empty source owner,
  releasing its full-XID maintenance horizon without republishing rows. Failed
  or discarded stages leave their source owners to unconditional session cleanup.

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
| `CatalogTransaction`, `CommitCatalogTransaction` | admitted source ownership and explicit post-publication completion for detached catalog fixture rows |
| `PreparedDescription(t, e, s, sql)`, `PreparedExecute(e, s, stmt, d)` | describe a statement, then execute it against that expected description |
| `ProtocolReader{Data: b}` | `session.ProtocolInput` over bytes for Bind and fastpath messages; `Position` counts consumed bytes |
| `QueryFixture(t, e, sql)` | table `t` with four rows, the query bound through `core.Describe`, and a captured source costed for the parallel worker alternative |
| `LockEmpty`, `LockPending`, `LockAsync`, `LockAwait` | lock-manager inspection (`Engine.Locks`) and a statement run on its own goroutine |
| `LockStatement(q)` | parse the first statement of q off the test goroutine (panics on error) |
| `ExecuteAsync(t, e, s, ctx, q)` | execute q on its own goroutine; the returned function joins it, and cleanup cancels and joins |
| `RowLockPending(t, e, ref, row, worker, blocker, mode)` | wait for worker's live row-lock request behind blocker |
| `WaitDiagnostic(t, err, code, message, position...)` | require an exact ERROR diagnostic |
| `CatalogAuthorization`, `CatalogRef`, `RoleTableRef` | bootstrap-principal authorization and relation references |
| `PublishCatalog`, `ValidateObjectRef` | catalog publication and reference checks outside an operation |
| `ExpressionBinder` | scalar binder over a postgres session |
| `SetObjectACL(e, ctx, ref, acl)` | publish an object or column ACL directly, outside GRANT and REVOKE |
| `Comment(e, r, ref)` | comment metadata of a namespace, type, relation or column, without ACL |
| `SnapshotFoldDescription`, `SavedFoldDescription`, `CloneFoldDescription`, `FoldBackingReusable`, `PreparedFoldEntry`, `SamePublishedFold` | accepted prepared folds: capture, clone, backing reuse, entry lookup and payload comparison |
| `PreparedFoldExecute`, `PreparedFoldProtocolCommand`, `DictionaryFoldRuntimeExecute`, `ConvertOwnedExecute` | execute a described statement, or run a protocol command scope |
| `DictionaryFoldDependency`, `SnowballRejectedFold`, `ConvertRejectPublication` | operand dependencies, and canceled private fold publications that keep the accepted backing |
| `PublishStage`, `PublishOperationStage` | validate and publish a catalog stage, outside or through an operation |
| `SimpleDictionary`, `SnowballDictionary` | parsed CREATE TEXT SEARCH DICTIONARY statements |
| `BindSelect`, `QueryProbe`, `QueryProbeExpression`, `EvalContext` | bind a SELECT; probe expressions over the engine evaluation context |
| `FunctionCall`, `StringLiteral`, `TypedLiteral`, `HostOwnerDefinition` | parser expressions, and an hstore host language |
| `Diagnostic(t, err, want)` | require an exact `sqlstate.Error` with both severities ERROR |
| `FixtureRole`, `FixtureEdges`, `CloneRoleFixture`, `PutFixtureRole`, `SetFixtureEdges`, `AclUnitRoles`, `NamespaceCapturedAuthorization`, `DefaultUnitStatement` | role-graph fixtures on a private role record, login roles, a session's captured authorization, and ALTER DEFAULT PRIVILEGES statements |
| `SchemaRef`, `CatalogUnitOID`, `CatalogStateFixture`, `ConfigName`, `SequenceScalar`, `RegisteredCatalogProducer` | schema references, fresh OIDs, an unpublished catalog state, names, a sequence value check and a registered catalog producer |
| `AccessMethodUnit`, `AccessMethodPublish`, `InheritanceUnitRelation`, `PrimaryKeyUnitRelation`, `ReplacementProcedurePolicyFixture` | catalog descriptors and their publication, and the host procedure-remap policy |
| `ExtensionUnitExecute`, `WithPublicSchemaUnitOwner`, `AssertTemporaryNamespacePair` | an admitted extension command, a temporary public-schema owner, and a session's temporary namespace pair |
| `RuleUnitRef`, `RuleUnitInt`, `RuleUnitInsert` | rule references and stored rule actions |
| `StatisticsUnitCreate`, `StatisticsUnitStage`, `StatisticsUnitDescriptor`, `AnalyzeFacts`, `VacuumStore` | extended statistics stages, relation statistics and a table's storage |
| `DeleteCount`, `UniqueClaimsEmpty`, `UniqueKeyBucketsEmpty`, `BtreeKeys`, `HashCaptureQuery`, `PlannerCostCommand` | write counts, unique-claim and B-tree key inspection, a captured hash query, and an admitted planner command |
| `PreparedUnitStatement`, `PreparedBoundaryName`, `PreparedBoundaryInteger`, `PreparedFoldProtocolTry`, `PreparedFoldProtocolRows`, `FoldRootsReusable` | prepared statements and boundaries, protocol fold commands and their rows, and fold-root reuse |
| `CaptureHeapAbort`, `AssertHeapAbort`, `AssertJournalDiscards` | immutable committed heap roots and exact created-version disposal; journal comparisons preserve every non-disposal field |

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
4. **Item 208:** the prepared-fold, catalog-stage, dictionary, probe and
   expression helpers that its `core/enginetests` destinations share; core's
   copies were replaced, and its own users call them here.
5. **Item 209:** the role-fixture, catalog, rule, statistics, write and
   prepared helpers that its DDL-family destinations share. Helpers that
   build the composed engine through the root or reach a `*cmds` family,
   types whose callers use unexported fields, and grouped `var` blocks stay
   copied.

## Testing

The helpers have no tests of their own; the engine-fixture tests that use them
cover them. Placement of those tests is in
[docs/TESTING.md](../../../docs/TESTING.md#placement-policy).
