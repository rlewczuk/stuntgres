# reference/operators

Dual-mode scenarios for user operators, routine parameter binding, cast
definitions, PL/pgSQL, DML triggers and registered host routines. Explicit builtin
casts and alias inputs belong to [type_resolution](../type_resolution/README.md#testing);
SQL routine/query lifecycle to [query_core](../query_core/README.md#testing);
trigger catalogs and event triggers to [triggers](../triggers/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` opts into
`fixture.WithHostPython()` for genuine PL/Python oracle consumers.

- Family-local scenario, value, diagnostic, held-statement and replay helpers
  share the configured backend loop. `plpgsqlScenario` owns PL consumers;
  `triggerScenario`/`triggerDDLScenario` own DML-trigger setup and execution.
- Native host programs are explicitly registered compiled Go with real
  directional converters, not a Python interpreter. Shared oracle programs use
  actual PL/Python and TRANSFORM opt-in. Keep SQL/language diagnostics separate
  from native Go ABI/carrier errors.
- Raw native pools own public controls; SQL assertions use recorded pools over
  the same connector with their own recorder, including independent destinations.
- Trigger wire consumers use `fixture.NewServerFixture` and public pgconn/pgproto3.
  Copy received flyweight fields before the next receive. Keep Parse/Describe,
  Bind and Execute observations distinct; embedded Prepare is not prior wire Bind.
- Casts between builtin types are schema-less global state. Assert an absent
  pair, register fresh bounded pair cleanup before CREATE, and drop dependent
  casts before adapter functions. Never drop a released `pg_cast` seed.
- Retained replay consumes actual immutable source Dump bytes only after source
  cleanup succeeds. Each replay owner uses its own recorded executor.
- Native logical controls do not serialize C ABI, heap/TOAST layout, WAL, physical
  tuplestores, process loading or live session resources.
- Grant reader schema USAGE explicitly after PostgreSQL's public-schema reset.
  Shared regoperator helpers have one owner in `fixture/regoperator.go`.
- Unordered PL query sources assert multiplicities, not traversal order. Active
  cancellation must observe real execution, cancel and join before handle cleanup.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- User operators: `RUN='^TestReferenceUserOperator'`
- Routine parameters: `RUN='^TestReferenceRoutineParameters'`
- Cast definitions: `RUN='^TestReferenceCastDefinition'`
- DML triggers: `RUN='^TestReferenceTrigger'`
- PL/pgSQL: `RUN='^TestReferencePLpgSQL'`
- Host routines: `RUN='^TestReferenceRoutineHost'`
- Transforms: `RUN='^TestReferenceRoutineTransform'`
- Transactional wire routines: `RUN='^TestReferenceTransactionalRoutine'`

Known release-specific PL/Python converter lifetime differences are documented in
[POSTGRES_REGRESSION.md](../../docs/POSTGRES_REGRESSION.md#plpython-retains-transform-converters-until-its-procedure-cache-rebuilds).
