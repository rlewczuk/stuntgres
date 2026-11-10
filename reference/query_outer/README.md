# query_outer — query sources and outer composition

Dual-mode scenarios for outer-join composition, relational SQL routine results
and XML/JSON table sources. Scalar SQL/JSON belongs to
[binary_json_arrays](../binary_json_arrays/README.md#testing), JSONPath to
[datetime_formatting](../datetime_formatting/README.md#testing), and scalar XML
predicates to [strings](../strings/README.md#testing).

## Testing

Follow [the shared testing policy](../../docs/TESTING.md). `main_test.go` owns
`fixture.Main`; scenarios use `fixture.NewScenario` and one shared backend body.

`resultsScenario` borrows one connection per backend. `resultsBoth` checks direct
and fresh prepared execution; `resultsHeld` exercises one real held statement.
`resultsMetadata` checks complete independently specified descriptors before any
row is read, including empty sets. `resultsError` checks the complete diagnostic
and prepare/execute boundary. Local native replay helpers call the public controls
on the fixture's unwrapped native pool; emitted SQL is replayed unchanged on the
oracle, without a second fixture implementation or cross-package helper imports.
`resultsBothMetadata` supplies explicit per-column native adapter profiles.
Profiles are caller choices rather than SQL-text inference, and do not change
pq expectations or omit metadata accessors.

Routine inventory facts come from released catalog/contrib sources.

XML node output is literal serialization, not normalized equivalence.
Node sets use bags or singleton selections, not portable traversal order.
Shared JSON table setup/runners and replay have one owner in
`reference/fixture`; family-local helpers do not duplicate that harness.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Outer joins: `RUN='^TestReferenceQueryOuterJoin'`
- Routine results: `RUN='^TestReferenceRoutineResults'`
- Procedures: `RUN='^TestReferenceRoutineProcedures'`
- Routine attributes: `RUN='^TestReferenceRoutineAttributes'`
- Routine inventories: `RUN='^TestReferencePGProcBuiltinInventory$'`
- Routine deparse: `RUN='^TestReferenceRoutineDeparse'`
- JSON tables: `RUN='^TestReferenceJSONTable'`
- XML tables and XPath: `RUN='^TestReferenceXML(Table|XPath)'`
