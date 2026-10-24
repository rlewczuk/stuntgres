# Engine split: `internal/engine/proc`, sixth round

Session prompt for linear items 169 and 170 of the
[status page](ENGINE-SPLIT-STATUS.md). The rules are in
[ENGINE-SPLIT.md](ENGINE-SPLIT.md) §2, §3 and §5, and the package contract
is in [the proc README](../internal/engine/proc/README.md).

`proc` is L1. It imports only `parser`, `sqlstate` and `sqltype`. Everything
the moved code needs from `catalog`, `plan`, `exec` or `regobj` must
therefore come in as one of:
- a `proc` value;
- a callback;
- a marker interface matched with `errors.As`.

`proc` already owns the compiled program (`PLProgram`, `PLBlock`,
`PLInstruction`), the invocation frame (`PLFrame`, `EnterHandler`,
`SetFound`), exception matching (`(*PLBlock).MatchException`) and
`TriggerReturn`. The root's PL/pgSQL files hold the runtime walk over them.
Sizes were measured at `ea8c445fe`. Re-run `reach` with
`-files '^plpgsql_'` first.

## Item 169 — PL/pgSQL diagnostics, error phases and flow

Needs —. About 0.29k lines move; none of them needs a host.

**Moves to `proc`.**
- **The runtime diagnostic formatter** (`plpgsql_execute.go`):
  - `plDiagnosticCallback` and its constants, `plNoCallback` through
    `plQueryDetail`;
  - `plDiagnosticPosition`, `plDiagnosticQuery` and `plBodyDiagnostic`;
  - `plFormatError`.

  `plRuntimeError` and `plRuntimeQueryError` take an `identity func() string`
  instead of `catalog.Read` and `*catalog.Procedure`. One-line root wrappers
  keep today's call shape and pass `plRoutineDiagnosticIdentity`, which
  stays in the root because it uses `regobj`.
- **The phase error types:** `plExpressionBindingError`,
  `plExpressionPlanningError`, `plExpressionExecutionError` (with its
  `Simple` flag), `plCardinalityError` (`plpgsql_assignment.go`),
  `plExceptionCleanupError` (`plpgsql_checkpoint.go`) and the
  `plCaseExpressionError` wrapper (`plpgsql_conditional.go`). Also move the
  classifiers `plExpressionCallback` and `plTriggerExpressionBinding`.
  Export the names the root still constructs.
- **Flow** (`plpgsql_loop.go`): `plFlow` and `plFlowKind` with
  `plNormal`, `plExit`, `plContinue` and `plReturn`.
- **Cursor diagnostics.** `plCursorName` becomes `(*PLFrame).CursorName`.
  `plCursorExpressionDiagnostic` moves.
- **The trigger frame.** `newPLTriggerFrame` (`plpgsql_trigger.go`) becomes
  `proc.NewPLTriggerFrame`. It takes the `*proc.Definition`, the row shape
  and names, the relation OID and the arguments as values.
- **`plAssignmentError`.** It matches `plan.AssignmentDomainError`,
  `plan.PLAssignmentPlanningError` and `plan.PLAssignmentProjectionError`.
  Give those three types marker methods (`PLAssignmentDomain()`,
  `PLAssignmentPlanning()`, `PLAssignmentProjection()`). `proc` then matches
  them with `errors.As` on its own interfaces, and `plan` needs no new
  import.

**Moves to `plan`.** `plSimpleExpression` and `plAssignmentProjection`
(`plpgsql_assignment.go`), next to `plan/pl_assignment.go`.

**Stays root.**
- `plRoutineDiagnosticIdentity`;
- the checkpoint clone helpers (`exceptionRows`, `exceptionDeletes`,
  `exceptionIndexes`, `exceptionForeignActions`);
- `plTargetDatum` and `finishPLTarget`;
- every `StatementScope` method.

**Callers outside the PL files** (rewrite them to the new names):
- `execute.go`, which builds `plExpressionBindingError`;
- `execute_bound.go`, which uses `plAssignmentProjection`,
  `plExpressionExecutionError` and `plSimpleExpression`;
- `statement_host_trigger.go`;
- `sql_routine.go` and `routine_host.go`.

**Callers and locks.** Nothing is held; all of it is value code.

**Tests.**
- The root units that construct the moved types move to `proc` or `plan`,
  or are rewritten to the new names:
  - `plpgsql_cursor_provenance_test.go`;
  - `plpgsql_conditional_test.go`;
  - `plpgsql_cleanup_calls_test.go`;
  - `plpgsql_expression_phase_test.go`;
  - `plpgsql_test.go` (`plRuntimeError`);
  - `plpgsql_assignment_test.go` (`plAssignmentProjection`);
  - `trigger_core_test.go` (`newPLTriggerFrame`).
- New `proc` units pin `PLFormatError`'s position mapping and the
  `errors.As` marker matching.
- Quick and `REFERENCE=1` selection:
  `TestReferencePLpgSQL(Assignment|Conditional|Cursor|Exception)Diagnostics`,
  `TestReferencePLpgSQLCursorErrorProvenance` and
  `TestReferenceTriggerCoreFrame`.

**Docs.** Add the runtime diagnostics and phase errors to the proc README.
Move the matching "Procedural exception boundaries" sentences of the root
README with them.

## Item 170 — PL/pgSQL statement walk

Needs 169. About 0.45k lines move, and a root adapter of about 0.1k is
added.

**Moves to `proc`** (`plpgsql_execute.go`, `plpgsql_loop.go`,
`plpgsql_conditional.go`, `plpgsql_query_loop.go`, `plpgsql_trigger.go`), as
methods of a new `proc.PLRun{Host, Definition, Frame}`:
- `invokePLRoutine`;
- `executePLBlock`, `executePLStatements`, `executePLLoop`,
  `executePLTransfer` and `executePLConditional`;
- `assignPLVariable` and `preparePLVariable`;
- the walk half of `invokePLTrigger`;
- `assignPLRow` and `consumePLQuery`.

The walk takes `*proc.Definition` and the program. It never takes
`*catalog.Procedure`.

**Cut: one capability, `proc.PLHost`** (§2.2 rule 3). Every method takes
values and runs with nothing held. The adapter is the root `plHost`, over
`StatementScope`, with a compile-time assertion.
- `Context() context.Context`;
- `Identity() string`, called only on error paths;
- `LookupType(sqltype.ID) (*sqltype.Descriptor, bool)`, for Bool and Int4;
- `Evaluate(f *PLFrame, q *parser.Select) (sqltype.Value, sqltype.Usage, error)`;
- `Convert(v sqltype.Value, target sqltype.Usage) (sqltype.Value, error)`;
- `AssignTarget(f *PLFrame, t PLTarget, raw parser.Expr, q *parser.Select) error`.
  Publication stays in the root, before the activation restore, because an
  EXCEPTION handler can observe that value.
- `BeginException() (PLCheckpoint, error)` and
  `EndException(c PLCheckpoint, rollback bool, primary error) error`.
  `PLCheckpoint` is opaque (§3 rule 7); the root recovers `*plExceptionCheckpoint`
  with one checked helper.
- `OpenQuery(f *PLFrame, in PLInstruction) (PLRows, error)`. Its errors come
  back already formatted.
- `Cursor(r *PLRun, f *PLFrame, in PLInstruction) (PLFlow, error)`. It
  delegates to the root cursor code, which calls back into exported
  `PLRun` methods (`Block`, `AssignVariable`, `PrepareVariable`,
  `AssignRow`).

`PLRows` is the iterator `consumePLQuery` needs instead of `exec.RowResult`
and `catalog.Column`:
- `Width()`;
- `Column(i) (name, usage)`;
- `Next(ctx, dst)`;
- `Close(primary error) error`, which closes the producers, formats the
  error and ends the loop's activation, in today's order.

Re-check with `reach -cut` that `PLHost` keeps the walk clean. If it needs
more than about ten methods, stop and report: the item splits.

**Stays root.**
- the `plHost` and `PLRows` adapters;
- `evaluatePLExpression`, `executePLExpressionQuery`, `convertPLValue`,
  `convertPLTargetValue` and `finishPLComposite` (binder construction under
  `Engine.mu`, the `sqlRoutine` scratch, `executeStatement`);
- the `plpgsql_assignment.go` effects;
- the open half of `forPLQuery`;
- every cursor method (`plpgsql_cursor.go`);
- `beginPLException` and `endPLException` (`owner.Mu`, savepoints, slot
  snapshots, lock restore);
- `compilePLRoutineLocked`, `executeDO` and `installBuiltinLanguages`.

**Callers and locks.** The walk holds nothing. The host methods take
`Engine.mu` or `owner.Mu` themselves, as today. FOUND publication, handler
matching, error ordering and the per-iteration cancellation check stay
identical.

**Bench.** This is a per-statement path inside every PL invocation. Run
`make bench` and a PL loop benchmark before and after (add a root benchmark
if none exists). Allow no new allocation per statement or per iteration,
and at most one per invocation for `PLRun`. Check `-gcflags=-m` for a frame
or host that escapes.

**Tests.**
- New `proc` units drive the walk with a fake `PLHost`: blocks, loops,
  EXIT/CONTINUE/RETURN, CASE, handler matching and FOUND.
- Root units that called the moved methods are rewritten to go through
  `plHost` or deleted where `reference/` covers them (name the covering
  test):
  - `plpgsql_loop_test.go` and `plpgsql_participants_test.go`
    (`executePLBlock`);
  - `plpgsql_query_loop_test.go` and `plpgsql_query_loop_failure_test.go`
    (`consumePLQuery`);
  - `plpgsql_test.go` (`assignPLVariable`);
  - `plpgsql_cursor_test.go` and `plpgsql_cursor_portal_ownership_test.go`
    (`invokePLRoutine`).
- Quick and `REFERENCE=1` selection: `TestReferencePLpgSQL(Core|Loop|QueryLoops|Exception)`,
  `TestReferencePLpgSQLCursorFor` and `TestReferenceTriggerCoreReturns`.
  Use `RACE=1` on the exception and cancellation selections.

**Docs.** The proc README gains the walk and the `PLHost` contract. Its
"does not interpret expressions or own query execution" stays true: the
host does both. Move the walk paragraphs of the root README there.

## Status

- 169: done (`df6ca810d`, `6e0834bde`, `6b0a2f092`, `4b9cf5736`, docs commit). Deviations:
  - The phase errors export their fields (`Err`, `Simple`, `Primary`,
    `Cleanup`): a literal in the root cannot set an unexported embedded `error`.
  - `NewPLTriggerFrame` takes a `proc.PLTriggerRelation{Shape, OID, Name,
    Namespace}`, resolved by the root `plTriggerRelation`. The row-type and
    namespace lookups now run before the program check; only internal
    invariant errors can change order.
  - `plAssignmentError` keeps no root wrapper: its one caller calls
    `proc.PLAssignmentError` with `plIdentity(read, d)`.
  - Tests: `TestPLpgSQLCursorCurrentDiagnosticPhase` and the three pure
    `TestPLpgSQLConditionalFrame` diagnostic subtests (now
    `TestPLCaseExpressionError`) moved to `proc`; new
    `TestPLFormatErrorPositionMapping` and `TestPLAssignmentErrorPhaseMarkers`.

  Notes for 170: the walk uses the root wrappers `plRuntimeError`,
  `plRuntimeQueryError` and `plIdentity`; once it moves it can call
  `proc.PLRuntimeError` with `Host.Identity` directly, and the wrappers stay
  only for the root cursor and query-loop code. Its reference selection names
  test families: use `TestReferencePLpgSQL(Core|Loop|QueryLoops|Exception)`
  as prefixes, without `$`. Finding for 180: the feature commit `f3a050354`
  (transactional role DDL) added root production code, for example
  `transaction_roles.go`.
- 170: not started.
