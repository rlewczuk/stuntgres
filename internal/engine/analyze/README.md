# internal/engine/analyze

Statement binding. Since 12d.6 it holds the expression binder (`Binder`, its
field groups and the statement-kind binders), `Describe`, which binds one
statement, and `Host`, the engine services the binder calls. It also holds
the bound form of a statement (`Binding`),
its per-kind payloads and the capture groups that views, rules, policies and
prepared statements read, plus the vocabulary the binder and its host speak:
the binding authority, the held description's inputs, name-lookup kinds and
selections, and the retargeting of captured identifier inputs. It also
holds the clean analysis helpers that the binder and engine code share
(coercion and collation derivation, input error positions, the held
signature check), and since 12d.4 the statement's row-lock admission, the
query namespace types and the query helpers the binder shares with the
engine, and since 12d.3b the DEFAULT closure predicates, the retained-input
walk and the rule namespace. The binder names no engine type; it reaches engine state only
through `Host`. The package sits at layer L8 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports `plan`, `exec`, `catalog`, `storage`, `expr`, `functions`, `ids`,
`lock`, `access`, `authorization`, `pgcatalog`, the Part A content of
`routine`, `sequence`, `typedef` and `extension` (§3 rule 6),
`parser`, `sqlstate`, `sqltype` and the standard library, and never the
engine or `session`. See
[ENGINE-SPLIT-analyze.md](../../../docs/ENGINE-SPLIT-analyze.md) (step 12).

## What it owns

- **`Binder`** (`expression.go` and the other expression, function,
  operator, query, CTE, aggregate, window and DML binder files;
  `binder_groups.go`): the binder state for one statement, the host, the
  statement inputs and the field groups (`TargetContext`, `ParameterState`,
  `ConsumerContext`, `StoredSyntaxState`, `ClauseState`, `GroupingState`,
  `QueryScopeState`, `CTEState`). `NewBinder` builds one over a `Host`;
  `Describe` (`describe_bind.go`) binds one statement into a `Binding`, with
  the statement-kind binders (`*_bind.go`, `insert_binding.go`,
  `update_rows.go`, ...). The members engine DDL and the engine's tests use
  are exported; `functions` reaches the binder through `functions.Binder`.
- **`Host`** (`host.go`): the engine services the binder calls (name and
  type resolution, privileges, definition reads, captures, the retag walker,
  stored views and the view read graph). The engine implements it
  (`bindingHost`).
- **`Binding`** (`binding.go`): the header every statement kind may use
  (target relation, storage and ref, view write, targets, used columns, the
  query, CTEs and query sources, snapshot demands, CURRENT OF, predicate,
  RETURNING, dependencies, parameters and expressions), the kind payloads
  and the capture groups. `ParameterInfo` is the inferred parameter list;
  the driver names it directly (`engine.FrameImporter` takes it).
  `*Binding` implements
  `plan.CTEMutation`, so a data-modifying CTE carries its binding.
- **Kind payloads** (`binding_groups.go`, `payloads.go`, `create_table.go`):
  `InsertBinding`, `DeleteBinding`, `CreateTableBinding` (held by value),
  `UpdateBinding` with its `AssignmentGroup`s, `MergeBinding` with its
  `MergeArm`s, `Returning`, and the CREATE TABLE records
  (`ColumnDeclaration`, `InheritanceInput`, `LikeInput` with `LikeMask`,
  `UniqueConstraint`).
- **Capture groups** (`binding_groups.go`, `captures.go`): `ViewCapture` (the
  rendering captures keyed by `ViewLiteralPath`, with view columns,
  namespaces and composite occurrences), `PreparedCaptures` (temporal,
  composite, sequence and regtype constants a prepared statement
  re-resolves), `ResolverInputs` (the statement input resolvers, captured by
  `NewResolverInputs`), `RuleBinding`, and `DefaultOrigin` (where a default
  expression came from).
- **`Authority`** (`authority.go`): the catalog and search-path authority a
  binding runs under. The binder reads only its catalog (`ReadCatalog`) and
  hands it back to the host for names, privileges, definition reads and
  captures. The engine implements it over its authorization view (a stored
  authority) and over the same view read through the operation's current
  definition reader (a binding authority, from `BindingAuthority`).
- **`Expected`** (`expected.go`): what a held statement description gives its
  re-analysis (fixed parameters and results, catalog owner and epoch,
  temporal, composite, sequence and catalog-identifier captures, plan owner).
  The binder holds it by value; `Held` marks a held description.
- **Lookup vocabulary** (`lookup.go`): `LookupKind`, the provider family a
  search-path lookup serves, and `NamespaceSelection`, the namespaces a name
  lookup searches.
- **Captured input retargeting** (`captured_inputs.go`): `IdentifierTarget`
  and the `RegtypeRetarget`, `OperatorRetarget`, `RoutineRetarget` and
  `RegTSRetarget` interfaces, which point the engine's captured resolvers in
  an input environment at one literal's position and sinks.
- **ON CONFLICT clause** (`insert_conflict.go`): `InsertConflict`, the bound
  clause (inference `ConflictKey`s and predicate or named constraint, read
  manifests, the DO UPDATE action and its RETURNING), held by
  `InsertBinding.Conflict`. `Clone` copies the header for one command, as
  rule actions need; the analyzed expressions stay shared.
- **Slots** (`binding_groups.go`): `RuleCTEsSlot` (the rule commands of a rule
  action's CTEs) and `ViewWriteSlot` (the view-write lowering; `Base` is the
  base relation the write lands on). The engine owns these values and
  recovers them with one checked helper each (`binding_slots.go`).
- **Shared statement helpers**, used by the binder and by engine DDL:
  `create_table_columns.go` (CREATE TABLE column declarations, column index,
  NOT NULL and unique constraints, `LikeConstraint`, LIKE options) and
  `statement_checks.go` (view output checks, INSERT targets and value errors,
  generated-column and inheritance conflicts, `TableKindError`,
  `GuardedCatalogRelation`, MERGE reachability). `statement_errors.go` holds
  the package's own `sqlError` and `insertError`.
- **Binding privileges** (`binding_privileges.go`): `CheckBinding` and
  `CheckQuerySources`, the walkers that check a bound statement's target,
  column reads and query sources in PostgreSQL's order, each source under its
  own principal. They decide nothing: `Privileges`, which the engine's
  `AuthorizationView` implements, decides each object through `access` and
  pairs a principal switch with its search path. View-write layers and view
  row marks are checked through its `CheckViewWrite` and `CheckViewRowMarks`.
- **Held signature** (`signature.go`): `Signature` and `CheckSignature`, the
  0A000 cached-plan check of a held description's parameters, results and
  catalog lifetime; `Expected.CheckLifetime` applies it to a replaced
  catalog owner or epoch during re-analysis. `StatementCatalogType`,
  `SameStatementUsage`, `SameStatementResultUsage` and `RefreshableTemporal`
  (which captured temporals a refresh re-inputs) belong to it.
- **Shared analysis helpers** (`coercion.go`, `errors.go`, `record_types.go`,
  `operator_signature.go`): coercion properties and Info,
  collation derivation (`CollatedInfo`), the domain-cast annotation pair
  (`DomainCoercionInput`, `RestoreDomainCastCollations`), storage
  destinations, `ComparisonOperator`, literal and reg* input error positions
  (`InputErrorAt`, `RegtypeInputError`), `RecordTypeMapper` and
  `OperatorSignature`. Engine DDL, retag, remap and preflight code call them
  too; they read only their arguments. The OVERLAPS inline cost model is
  `exec.OverlapsInlineSafe`, shared with preflight.
- **Row-lock admission** (`lock_admission.go`): `LockAdmission`, computed
  by `NewLockAdmission` from a statement's syntax before binding (the row
  marks of each SELECT level, the locked table occurrences as
  `PreparedSource`s, forward CTE references, data-modifying CTE targets as
  `MutationTarget`s), and the row-lock clause helpers (`RowLockClause`,
  `RowLockMode`, `RowLockDiagnostic`, `RowLockTargets`, `RootRowMarks`,
  `FirstSetRowClause`, `FutureCTEError`). The engine holds one per statement
  scope and passes it to `describe`; the binder reads it per query level.
- **Query namespaces** (`namespace.go`): `QueryNamespace`, `QueryQualifier`
  and `QueryColumn`, what a FROM item makes visible. Engine consumers outside
  queries (checks, defaults, generated columns, index, partition and policy
  expressions) build one for their relation. `QueryTargetLeaf` marks a DML
  target column.
- **View namespace kinds** (`view_namespace.go`): the `View*Namespace` node
  kinds stored in `ViewNamespace.Node`, `FrozenNamespaceNode` and
  `ViewFromRoot`, which view rendering also uses.
- **Query helpers** (`query_walk.go`, `query_sets.go`,
  `query_diagnostics.go`): plan and expression walks over query frames,
  recursive worktable slots, CTE volatility, `MutationBinding` (the checked
  recovery of a data-modifying CTE's binding), subquery demand and the
  EXISTS presence rewrite; the set-returning lowering helpers that query
  preflight shares; and column-name hints and query diagnostics.
- **Query legality helpers** (`query_graph.go`, `query_window_prune.go`,
  `query_join_legality.go`, `query_lock_legality.go`): walks of the plan IR
  that freeze a stored query's frames (`CollectQueryFrames`), detect changed
  descriptors and routine carriers (`QueryDescriptorsChanged`,
  `QueryRoutineCarriersChanged`), map an occurrence's runtime reads
  (`QueryExecutionSourceReads`, `QuerySourceReadUses`) and drop unused window
  calls (`PruneProjectWindows`); the FULL JOIN condition facts
  (`FullJoinCondition`, `FullJoinFacts`) and nullable-side row marks
  (`NullableQueryRowMark`, `FirstNullableQueryMark`, `QueryOccurrenceMark`);
  and the row-lock legality of a query shape (`QueryRootRowMode`,
  `QueryHasSetOutput`, `QueryRowLockLegality`, `QueryRowMarkReachesSource`).
  They read only their arguments; the engine's executor walks, view
  rendering, remap and lock initialization call them.
- **Shared expression helpers** (`default_closure.go`, `retained_input.go`,
  `operator_names.go`, `procedure_lookup.go`, `rule_namespace.go`): the closed
  DEFAULT predicates (`ClosedPgcryptoDefault`, `ClosedSequenceDefault`,
  `ClosedRegclassDefault`, `ClosedTransactionTimeDefault`, `ClosedUUIDDefault`, `ClosedSQLRoutineDefault`
  with `SQLRoutineDefaultClosure`) and the installed-call shape checks they use
  (`CryptoExpressionSignature`, `ValidInstalledUUIDOsspShape`,
  `ValidInstalledText`); `NeedsTypeDiagnosticInput` and
  `CaptureRetainedExpressionInput`, the catalog input a retained expression
  needs; `ArrayMembershipName`, `BindExtremaComparison`,
  `ValidateRegroleStoredLiteral` and `ProcedureInNamespaces`; and
  `RuleNamespace` with `RulePseudoScope`, the OLD/NEW frame of rule binding.
  The binder and engine DDL, preflight, retag, rendering and execution
  preparation call them; they reach no engine state.

## Invariants

- **Value semantics.** A `Binding` is copied by value and the copies are
  mutated (preflight, rule remap and graft, MERGE arms, view-write INSERT).
  Payloads and groups are values; `UpdateBinding`, `MergeBinding`,
  `Returning` and the slot values are shared pointers, as before the move.
- **Nil slots mean absent.** The engine never stores a nil pointer in a slot,
  so `b.ViewWrite != nil` keeps its meaning; `b.Insert.Conflict` is a plain
  pointer, nil without ON CONFLICT.
- **One clause per command.** Execution writes the normalized predicate and
  the preflighted DO UPDATE action into the binding's clause, so a stored
  rule action clones it per command; arbiter and write state never live in
  the clause.
- **Capture maps** keep their contents and iteration rules: stored SQL
  (views, rules, policies, defaults, checks) deparses byte-identically.
- **No mutexes and no engine state.** The types are plain records and the
  binder reaches engine state only through `Host`; binding runs under
  `Engine.mu`, held by the engine caller.
- **Authorities.** A nil `Authority` means none is stored; the engine never
  stores a nil pointer. Only the host interprets a binding authority: engine
  code recovers stored authorities with `authorizationOf`, which rejects
  binding authorities. A retarget returns a copy; a nil sink in the target
  keeps the resolver's sink.

## What stays in the engine

The binder's construction point `newExpressionBinder`, the host
implementation (`bindingHost`, `Engine.analysisHost`, `analysis_host.go`) and
the root wrappers `describe`, `describeWithRule` and `describeScoped`; engine
DDL that drives a binder it builds (`bindRuleDefinitionLocked`,
`bindCreateDefaults`, checks, defaults, index, partition and policy
expressions); the authority adapters (`analysis_authority.go`:
`bindingAuthorityView`, `authorityOf`, `authorizationOf`), `expectedOf`
(`expected_inputs.go`), `signatureOf`/`checkStatementSignature` (the
`StatementDescription` wrappers of `CheckSignature`), the host forwarders to
the `stored` retag walker (`MapBoundExpression`, `MapBoundQueryExpression`),
query preflight (`query_preflight.go`; expression preflight is in `exec`)
and the captured input constructors (`captured_input.go`; the resolvers and
their retarget methods are in `regobj`); the host forwarders through which
the binder reads stored view definitions (`stored`): `StoredViewVolatile`,
`CaptureRetainedViewInput` and `ValidateViewReadGraph`; query set preflight
(`preflightQuerySet`, `query_set_preflight.go`); rule program execution and
admission (rule rewriting, its CTE commands and view-write lowering are in
[`view`](../view/README.md)); the privilege walkers over view
definitions, view writes and rule programs (`AuthorizationView.CheckViewWrite`,
`checkViewRowMarks*`, the `row_security_setting.go` walkers); and the
execution-side helpers over the binding types: `evalBindingDefault`,
`applyDefaultOrigin` and `capturedDomainReplayCell` (`dml` has the ON
CONFLICT execution state `InsertConflictState`, built per command over the
binding's clause, and `MergeHasVirtualReads` and `SelectMergeArm`, see
[`dml`](../dml/README.md); `view` has `PhysicalBinding`
and `BindingDefaultOrigin`; `stored` has
`ValidateCompositeOccurrence` and `RemapLikeExpression`). The executor-side legality walks stay too
(`validateQueryJoinExecution`, `validateMutationJoinExecution`,
`validateExpressionJoinExecution`, `validateMarkedQueryJoin`,
`admitQueryGroupings` in `query_join_legality.go`), with the row-mark readers
of stored views (`viewRowLockLegality`, `viewLeafRowMark`,
`viewHasStoredRowMarks`, `checkViewRowMarks*`, `query_lock_admission.go`),
which read `stored.View`. Since 12e the engine names these types directly
(`analyze.Binding`, `analyze.Binder`, ...); no aliases remain.

## Testing

`TestInsertConflictCloneOwnsTheClauseHeader` covers `InsertConflict.Clone`,
`TestExpectedCheckLifetimeFencesCatalogTypes` the held lifetime check, and
`TestCheckQuerySourcesChecksEachSourceAsItsPrincipal` and
`TestCheckBindingDeleteChecksDeleteBeforeReads` the binding privilege walkers
(per-source principals, validation and check order), and
`TestQueryOuterJoinsComparisonNegators` the FULL JOIN comparison negators.
The binder needs an engine host, so its unit tests stay in the engine and
name `analyze.Binder` and its exported members. The seams have engine unit tests: slots, binding
authorities, held inputs, captured input retargeting and the host's rule
check, expression and query mapping, stored-view reads and the view read
graph:

```sh
make check RUN='^(TestQueryOuterJoinsComparisonNegators|TestInsertConflictCloneOwnsTheClauseHeader|TestExpectedCheckLifetimeFencesCatalogTypes|TestCheckQuerySourcesChecksEachSourceAsItsPrincipal|TestCheckBindingDeleteChecksDeleteBeforeReads|TestBindingSlotsKeepNilAbsent|TestViewWriteSlotReportsBaseRelation|TestAnalysisHostReportsRelationRules|TestAnalysisHostMapsBoundExpressions|TestAnalysisHostMapsBoundQueryExpressions|TestAnalysisHostValidatesViewReadGraph|TestBindingAuthorityOverlaysOperationDefinition|TestExpectedOfCopiesHeldInputs|TestCapturedInputsRetargetOnlyTheirSinks|TestAnalysisHostReadsStoredViews)$'
```

ON CONFLICT execution state over the clause: `TestRuleConflictInstantiation`,
`TestRuleCTEConflictInstantiation` and the `TestInsertConflict` engine units.

Engine units that build bindings: grep `internal/engine/*_test.go` for
`binding{`, `describe(` and the payload names. Public proof is the reference
selection of step 12, in quick mode and with `REFERENCE=1`:

```sh
make check RUN='^TestReference(InsertConflict|InsertSelect|InsertReturning|Update|Delete|Merge|Inheritance|TableLike|TypedTable|Rule|View|Prepared|CTEDML|Polic|DumpRound|Restore)'
```

The query, FROM and CTE binding that uses the lock admission, the
namespaces and the query helpers:

```sh
make check RUN='^TestReference(Query|RowLock|PartialIndex|GeneratedVirtual|CompositeExpr)'
```

The query legality helpers need engine-built plans, so their unit tests stay
in the engine (`TestQueryOuterJoinsNegatedKeys`, `TestQueryWindow*`,
`TestQuerySet*`, `TestStringSplitRowMarks`, `TestQuerySubquery*`):

```sh
make check RUN='^(TestQueryOuterJoins|TestQueryWindow|TestQuerySet|TestStringSplit|TestQuerySubquery)'
```
