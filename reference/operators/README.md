# reference/operators

Dual-mode tests for user-defined operators (CREATE/ALTER/DROP OPERATOR, shells
and commutator/negator links, option forms and validation order, resolution
precedence and polymorphic selection, comparison consumers, catalog rows,
dependencies and native round trips), routine parameter binding (named and mixed
notation, defaults, VARIADIC, OUT/INOUT, named-path selection against builtins)
and CREATE CAST definitions. This package also owns
`TestReferenceRegOperatorExternalBoundary`; the other operator OID alias
(`regoperator`) scenarios are in
[reference/type_resolution](../type_resolution/README.md#testing). Scalar
comparison, BETWEEN and conditional expressions are in
[reference/query_core](../query_core/README.md#testing) and
[reference/types](../types/README.md#testing); explicit casts and the
comparison resolution matrix are in `reference/type_resolution`.
BEFORE row, AFTER row/statement and view INSTEAD OF row trigger scenarios cover private PL frames,
ordered chains, query completion, zero-row events, TRUNCATE, permissions,
physical mutation producers and native post-data replay. The shared
`triggerScenario` helper follows the ordinary configured-backend loop.
Builtin trigger scenarios use the same helper for representation-sensitive
suppression, explicit and row-configured vector sources, continuous positions,
lexical UPDATE targets, current mappings, complete diagnostics and builtin catalog
identities. Raw TSVECTOR RETURNING checks retain descriptors on suppressed rows;
system-attribute diagnostics, constrained-domain replacement and declared binary
coercibility distinguish builtin tuple operations from ordinary SQL assignment.
Persistence retains original writers across native replacement rounds, including
literal configuration rename/name reuse, and replays actual emitted SQL on
PostgreSQL; external C ABI, heap/TOAST layout,
WAL and process loading are not native logical-control operations.
Trigger-depth scenarios retain live queries across nested user/RI invocation,
exception unwind and native consumer replay. They distinguish WHEN/default
evaluation, actual bodies and queued AFTER execution using ordered phase keys.
Queued AFTER function replacement is covered for ROW and STATEMENT firing through
direct and prepared commands: an earlier trigger replaces a later function before
its first invocation, with exact current-body audits and atomic rollback of a
failing replacement, its audit writes and the outer mutation.
Persistence checks execute restored INSERT/UPDATE/DELETE/TRUNCATE definitions
with exact audit values and metadata. Retained history distinguishes an old ROW
clone from a later attached leaf without one across two replay rounds.
PostgreSQL replay checks ordinary SQL firing; native-only phases check reverse
ROW replacement exactly once, dependency retirement and failed-import preservation
of live data, eager results and held writers.
Lifetime witnesses combine supported routine/relation/column rename with
dependency RESTRICT/CASCADE/drop-recreate routes. A competing function reusing
the original name must not retarget an installed trigger, including two native
replacement rounds and unchanged-stream PostgreSQL replay.
Trigger definition and lifecycle scenarios cover autocommit, SQL and Go
transactions through direct and held handles, private firing and savepoint
recovery, ownership/notices, automatic extension retirement and stable partition
clones. Lock witnesses cover descendant admission, cancellation and owner end;
native-control phases check committed-only Dump, replacement fencing and
unchanged-stream PostgreSQL replay.
Trigger protocol scenarios use the real server fixture for simple messages,
separate quiet Parse/Describe and Bind/Describe phases, utility Execute and
server-backed Go transactions. Held DML observes private and committed trigger
changes with exact completion/Ready states and atomic message rollback.
Generated partition CREATE/ATTACH boundaries are asserted explicitly; movement
semantics use nongenerated partitions, while ordinary-table Ordering covers
generation after BEFORE transformation.
Implicit TG type cases use exact overloads under shadow types; WHEN identity
cases combine overload competition, live column/table rename, two replay rounds,
portable PostgreSQL replay and one retained DML handle. RI authority cases
distinguish initiator, referencing owner and function owner through independently
granted helper/read permissions, including UPDATE and DELETE cascade rollback.
`PartitionReplace` distinguishes explicit parent replacement from ordinary CREATE
and ATTACH conflicts, including failed replacement preservation.
`GeneratedUpdateOf` checks conservative generated-column firing with a
column-specific BEFORE hook alone and with an earlier broad hook, using exact
direct/prepared final values rather than runtime changed-value inference.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is plain
`fixture.Main(m)`.

- Helpers: `user_operator_execution_test.go` (`userOperatorScenario`,
  `userOperatorExec`, `userOperatorValue`, `userOperatorError`),
  `routine_parameters_helpers_test.go` (`parametersScenario` and the `parameters*`
  held/metadata/error/replay helpers), and the cast-definition constants and
  helpers `castDefinitionSource`, `castDefinitionScore`, `castDefinitionEdge`,
  `castDefinitionCleanup`, `castDefinitionFrozenReplayChecks` and
  `castDefinitionReplaySQL`, which stay private to this package.
- Trigger helpers: `triggerScenario`/`triggerDDLScenario` own configured-backend
  loops, `triggerSetup` and `triggerDDLChain` provide shared definitions, and
  `triggerRows`, `triggerDDLHeld`, `triggerDDLError` and `triggerDDLX` assert
  metadata, actual retained-handle execution, diagnostics and extension edges.
- Trigger protocol helpers use `fixture.NewServerFixture` for process, backend
  and credential ownership. The local `triggerProtocolWire` uses public pgconn
  startup/authentication and typed pgproto3 messages; each received message is
  encoded into owned bytes before another receive can reuse its flyweight fields.
  Keep these helpers test-focused, without importing the sessions-private harness.
- `fixture/regoperator.go` (`RegOperatorRows`, `Metadata`, `DiagnosticRoutes`,
  `Error`, `Literal`, `Context`) is shared with `reference/type_resolution`; after
  changing it, run both packages' alias selectors.
- Casts are schema-less global objects that the `public` reset does not remove.
  A test that creates a cast between builtin types first asserts the pair is
  absent, registers pair cleanup before CREATE CAST, and drops pairs separately
  with fresh bounded contexts even after failed assertions (dependent casts
  before their adapter functions). Replay phases remove these pairs before the
  next backend phase.
  `DeclaredTypeNameAuthority` uses the same cleanup and held-statement seams to
  distinguish function-method CAST from binary/INOUT typename shortcuts,
  including consulted enum/domain base pairs and existing backend lifetimes.
- Cast transaction cases reuse those declarations with `SequenceCoreConn` and
  `HeldStatement` across autocommit, SQL BEGIN and Go BeginTx. Couple actual
  explicit/assignment/implicit coercion to directed-pair/function metadata,
  owner-private types, peer cache isolation and exact rollback restoration.
  Declaration authority is separate from current routine EXECUTE at use.
  Conflict workers use shared lock observers and joined cleanup; the function
  retirement witness pairs a private cast creator with an autocommit waiter.
  Native controls restore through unwrapped pools and keep replacement-fenced
  old handles distinct from fresh same-owner recovery.
- Retained aggregate binary lifecycle tests publish immutable source Dump bytes
  only after the source subtest and its cleanup succeed. Fresh-native and
  PostgreSQL replay consume those original bytes unchanged. A separate native
  replay owner restores the source, dumps its own state, then resets and restores
  that same engine from its own emitted Dump. The semantic lifecycle and full
  final assertions stay shared across owners.
- Never drop a released `pg_cast` row; declare an absent pair instead.
- On PostgreSQL the reset `public` schema (a plain CREATE SCHEMA) has no PUBLIC
  USAGE grant: authority cases GRANT schema USAGE to their reader explicitly.
- `TestReferenceUserOperatorRoutineAuthorization` uses authenticated raw login pools
  and checks their rows directly; those pools produce no middleware recordings.
- `TestReferenceCastDefinitionForeignDefaults` uses a metadata-only wrapper
  (`NO HANDLER NO VALIDATOR`) and server, registers no native foreign provider,
  and cleans global wrapper/schema state with fresh bounded contexts.

Add `REFERENCE=1` for the oracle.

- User-defined operators: `RUN='^TestReferenceUserOperator'`
- Transactional routine/operator/aggregate protocol: `RUN='^TestReferenceTransactionalRoutinesProtocol$'`.
  Typed wire scenarios reuse `triggerProtocol*` helpers; the database/sql route
  matrix lives in [query_core](../query_core/README.md#testing), not a second operator fixture.
- Operator option forms and checkpoints: `RUN='^TestReferenceUserOperator(Option|CreationCheckpoints|ParameterCheckpoints)'`
- Comparison consumers of custom operators: `RUN='^TestReferenceUserOperatorComparisonConsumer'`
- Polymorphic operator selection: `RUN='^TestReferenceUserOperatorPolymorphic'`
- `regoperator` external boundary: `RUN='^TestReferenceRegOperatorExternalBoundary$'`
- <a id="routine-parameter-binding"></a>Routine parameter binding: `RUN='^TestReferenceRoutineParameters'`
- Routine declaration-default attribute capture: `RUN='^TestReferenceRoutineDefaultAttribute'`
- <a id="cast-definitions"></a>CREATE CAST definitions: `RUN='^TestReferenceCastDefinition'`
- Declared CAST/typename authority: `RUN='^TestReferenceCastImplementedDeclaredTypeNameAuthority$'`
- Row-level triggers: `RUN='^TestReferenceTriggerCore'`
- Builtin trigger functions: `RUN='^TestReferenceTriggerBuiltins'`
- AFTER query completion and replay: `RUN='^TestReferenceTriggerAfter'`
- View INSTEAD OF triggers: `RUN='^TestReferenceTriggerInstead'`
- Trigger lifecycle: `RUN='^TestReferenceTriggerDDL'`
- Transactional trigger integration and protocol: `RUN='^TestReferenceTransactionalTrigger'`
- Live trigger nesting and consumers: `RUN='^TestReferenceTriggerDepth'`.

### PL/pgSQL core

Shared dual-mode cases cover lexical slots/aliases, all-formal `$n`, declarations,
fieldwise rows/RECORD, non-NULL VOID, lazy binding/full parsing, language USAGE
versus EXECUTE, stored consumers and two native/unchanged-PostgreSQL replay rounds.
Conditional scenarios additionally cover ordered IF/ELSIF/ELSEIF and CASE
branches, PL Boolean conversion, once-only simple searches, full diagnostics,
statement atomicity and stored callers with two unchanged-stream replay rounds.
Provenance cases include postorder datum numbering, syntactic record-field and
qualified-callee identity, scalar-shadow and competing record/label namespaces,
procedural delimiter depth and token-accurate comment boundaries.
Parameter-dependent WHEN failures retain execution-only context, contrasting
with constant-folding failures. Native cleanup-provider units also exercise
simple CASE WHEN through called PL and SQL wrappers without admitting OTHERS
after fatal cleanup failure.
Loop scenarios reuse the same direct/prepared, held and replay helpers for bounded
iteration, nested label transfers, reached condition evaluation, complete
diagnostics, protected effects and actively admitted cancellation. WHILE cases
also assert pre-entry/reevaluated NULL/false termination, volatile condition
counts and original held handles across native replacement rounds.
Integer-range scenarios additionally cover private INT4 names, entry-time bounds,
positive steps, inclusive overflow-safe traversal, labels/FOUND and retained
handles/native replay. Family selector: `RUN='^TestReferencePLpgSQLIntegerFor'`.
RAISE/ASSERT scenarios reuse the PL value/complete-diagnostic helpers and the
shared notice-enabled fixture. They cover ordered operands, lazy assertions,
effective settings, held calls and stored callers through unchanged replay.
Nested successful notices compare complete ordered records through PL and
non-inline SQL callers, including direct, fresh-prepared and original-held
execution, source-versus-body FOR ownership, and subsequent context-leak probes.
Family selector: `RUN='^TestReferencePLpgSQL(Raise|Assert)'`.
Diagnostics cases share `RUN='^TestReferencePLpgSQLDiagnostics'`, including
direct/prepared status, current versus caught context, exact error fields and
held-handle/native-control lifetime through the existing fixture.

```sh
make check RUN='^TestReferencePLpgSQLCore(Blocks|Parameters|Declarations|RowsRecords|Returns|LazyBinding|LanguageAuthority|Persistence|CompositeNull|RecordClear|CompositeDomain|CopiedCollation|InvocationCollation|FormalNamespace)$'
make check RUN='^TestReferencePLpgSQLCore(Blocks|Parameters|Declarations|RowsRecords|Returns|LazyBinding|LanguageAuthority|Persistence|CompositeNull|RecordClear|CompositeDomain|CopiedCollation|InvocationCollation|FormalNamespace)$' REFERENCE=1
make check RUN='^TestPLpgSQL(ParserFullConsumption|FormalSlots|FrameLifetime|FrameInputCollation|CheckedCallCollation|RowAssignment|CompositeAssignmentAtomicity|DiagnosticIdentity|ProcedureReturn|HandlerAuthority|RemapOnce|DepthUnwind|LocalUsageReplacement|LocalUsageEquivalence|FormalNamespaceCompiler|FormalNamespaceValidation)$' RACE=1
make check RUN='^TestSQLRoutineDumpEventsUseResolver$'
```

```sh
make check RUN='^TestReferencePLpgSQLConditional(If|Conversion|SearchedCase|SimpleCase|Nested|Diagnostics|Atomicity|Persistence|ReviewProvenance)$'
make check RUN='^TestReferencePLpgSQLLoop(Bounded|Continue|Conditions|Labels|Diagnostics|HiddenLabel|Atomicity|Cancellation|Persistence)$'
make check RUN='^TestReferencePLpgSQLWhile(Entry|Conditions|Control|Diagnostics|Atomicity|Cancellation|Persistence)$'
```

Static query-result loops reuse `plpgsqlScenario`, direct/prepared value and
complete-diagnostic helpers. Shared scenarios assert ordered duplicate rows,
ordinal/private target shapes, shaped empty rows, control transfers, tuple-source
effects, demand-sensitive CTEs, admitted cancellation and held/native replay.
Unordered sources assert counts and multiplicities, never traversal order.

```sh
make check RUN='^TestReferencePLpgSQLQueryLoops(Ordered|Targets|Empty|Control|Diagnostics|Lifecycle|Sources)$'
make check REFERENCE=1 RUN='^TestReferencePLpgSQLQueryLoops(Ordered|Targets|Empty|Control|Diagnostics|Lifecycle|Sources)$'
```

Array-loop scenarios share the same fixture and exact value/diagnostic helpers,
with ordered elements, bounded trailing slices, sequential list assignments,
domain identity, private control/FOUND and original-held/replay witnesses.
Family selector: `RUN='^TestReferencePLpgSQLForeach'`; add `REFERENCE=1` for the oracle.

Embedded SQL/PERFORM/PL CALL scenarios share the same value, complete-diagnostic,
metadata and lifecycle helpers, including held handles and native replay.
Family selector: `RUN='^TestReferencePLpgSQLSQLExecution'`; add `REFERENCE=1`
for the pinned oracle.

### PL/pgSQL exception blocks

The exception family reuses `plpgsqlScenario`, value/complete-diagnostic helpers
and the shared fixture. It covers protected rollback, matching/rethrow, intrinsic
scope, exact diagnostics, transaction/DO visibility, held/replay lifetimes and
command-owned immediate constraint completion. SQL DML helpers are called from
existing procedural assignment expressions.
`UtilityPublication` independently exercises direct/prepared SELECT and DO with a
SET helper inside the handler: protected put2 disappears, put1/put3/put4 commit,
and a peer sees exactly `[1,3,4]`. Each invocation has its own scenario.
`CallDiagnostics` asserts complete portable records through called PL and an
explicitly non-inline SQL wrapper at whole/element assignment, RETURN and initializer sites,
using direct and fresh-prepared routes. The SQL wrapper exercises its actual
statement-2 diagnostic boundary, not SQL-function inlining.
Native supplements exercise fatal modification cleanup across called PL and SQL
wrappers, completion-flag unwind and installed-root cancellation fences, forged
handler queries, and retained per-destination relevance with catalog invalidation.

Family selector: `RUN='^TestReferencePLpgSQLException'`; add `REFERENCE=1` for
the pinned oracle. Supplemental parser/program/checkpoint/participant/command
units use `RUN='^TestPLException'` with `RACE=1`.

### PL/pgSQL assignment

Shared assignment cases cover coercion selection, ordered field/subscript
targets, bounds, diagnostics, cardinality, evaluation order and held/native-control lifecycle.

```sh
make check RUN='^TestReferencePLpgSQLAssignment(Coercion|Targets|Bounds|Diagnostics|Order|Lifecycle|Cardinality)$'
make check RUN='^TestReferencePLpgSQLAssignment(Coercion|Targets|Bounds|Diagnostics|Order|Lifecycle|Cardinality)$' REFERENCE=1
make check RUN='^TestPLpgSQL(Assignment(Atomicity|TargetValidation|TargetUsage|BoundsOrder|PlanningOrigin|Parser)|RowAssignment|CompositeAssignmentAtomicity)$' RACE=1
```

### PL/pgSQL static capture

Static SELECT/DML RETURNING INTO scenarios reuse `plpgsqlScenario` and the
shared PL value/complete-diagnostic helpers. They cover STRICT cardinality,
ordinal RECORD/row/list movement, shaped empty records, FOUND publication,
sequential partial assignment, protected mutation rollback and retained handles
through two native replacement rounds. Demand witnesses distinguish one-row
SELECT from second-row STRICT/DML detection, including projection-error
precedence, no third mutation proposal and nested command isolation.

Family selector: `RUN='^TestReferencePLpgSQLSelectInto'`;
add `REFERENCE=1` for the pinned oracle. Supplemental parser, checked-program
and producer units use `RUN='^TestPLpgSQLSelectInto(Parser|Validation|Runtime)$'`.

Dynamic SQL scenarios share the PL helpers for data-only typed USING, computed
identifiers, complete SELECT/DML consumption, INTO cardinality, dynamic set
results, phased diagnostics, closed USING parameter bounds, caught and uncaught
same-held-handle recovery, and native replay. Family
selector: `RUN='^TestReferencePLpgSQLDynamicSQL'`; add `REFERENCE=1` for the
pinned oracle.

The diagnostics Status, Context and Lifecycle definitions also exercise supported
dynamic commands: complete final-command SELECT/DML counts (separate from INTO
cardinality), publication before late INTO errors, preserved ordinary EXECUTE
FOUND, instruction-local RETURN QUERY counts, and no success publication after
execution failure. Exact live stacks distinguish command/USING operand calls from
computed SQL execution's SQL statement frame and retain the suspended PL action;
actual cancellation checks recovery through direct and original-held handles.
Run `make check RUN='^TestReferencePLpgSQLDiagnostics(Status|Context|Lifecycle)$'`
in both native and `REFERENCE=1` modes.

Static diagnostics Context witnesses cover SELECT INTO's exact redacted spelling
(suffix, interior and multiline UTF8/comment forms), PERFORM's SELECT-head
translation, SQL-wrapper ancestry, and restoration after completion or failure.
Capture/cursor status witnesses retain late conversion/cardinality boundaries:
completed query frames are absent from those errors while status and target
semantics remain unchanged. Private live-owner coverage observes actual static
producer Begin/Next/End alongside dynamic execution and operand owners.
