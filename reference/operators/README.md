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
- `fixture/regoperator.go` (`RegOperatorRows`, `Metadata`, `DiagnosticRoutes`,
  `Error`, `Literal`, `Context`) is shared with `reference/type_resolution`; after
  changing it, run both packages' alias selectors.
- Casts are schema-less global objects that the `public` reset does not remove.
  A test that creates a cast between builtin types first asserts the pair is
  absent, registers pair cleanup before CREATE CAST, and drops pairs separately
  with fresh bounded contexts even after failed assertions (dependent casts
  before their adapter functions). Replay phases remove these pairs before the
  next backend phase.
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
- Operator option forms and checkpoints: `RUN='^TestReferenceUserOperator(Option|CreationCheckpoints|ParameterCheckpoints)'`
- Comparison consumers of custom operators: `RUN='^TestReferenceUserOperatorComparisonConsumer'`
- Polymorphic operator selection: `RUN='^TestReferenceUserOperatorPolymorphic'`
- `regoperator` external boundary: `RUN='^TestReferenceRegOperatorExternalBoundary$'`
- <a id="routine-parameter-binding"></a>Routine parameter binding: `RUN='^TestReferenceRoutineParameters'`
- <a id="cast-definitions"></a>CREATE CAST definitions: `RUN='^TestReferenceCastDefinition'`

### PL/pgSQL core

Shared dual-mode cases cover lexical slots/aliases, all-formal `$n`, declarations,
fieldwise rows/RECORD, non-NULL VOID, lazy binding/full parsing, language USAGE
versus EXECUTE, stored consumers and two native/unchanged-PostgreSQL replay rounds.

```sh
make check RUN='^TestReferencePLpgSQLCore(Blocks|Parameters|Declarations|RowsRecords|Returns|LazyBinding|LanguageAuthority|Persistence|CompositeNull|RecordClear|CompositeDomain|CopiedCollation|InvocationCollation|FormalNamespace)$'
make check RUN='^TestReferencePLpgSQLCore(Blocks|Parameters|Declarations|RowsRecords|Returns|LazyBinding|LanguageAuthority|Persistence|CompositeNull|RecordClear|CompositeDomain|CopiedCollation|InvocationCollation|FormalNamespace)$' REFERENCE=1
make check RUN='^TestPLpgSQL(ParserFullConsumption|FormalSlots|FrameLifetime|FrameInputCollation|CheckedCallCollation|RowAssignment|CompositeAssignmentAtomicity|DiagnosticIdentity|ProcedureReturn|HandlerAuthority|RemapOnce|DepthUnwind|LocalUsageReplacement|LocalUsageEquivalence|FormalNamespaceCompiler|FormalNamespaceValidation)$' RACE=1
make check RUN='^TestSQLRoutineDumpEventsUseResolver$'
```
