# query_outer — query sources and outer composition

Public database/sql reference scenarios cover outer-join composition, metadata,
restriction placement, diagnostics, persistence and source lifecycle. SQL routine
result sources share the same query composition and fixture entry points.

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
`resultsBothMetadata` supplies explicit per-column native adapter profiles for
catalog casts, stored catalog fields and null-extended lateral outputs.
`resultsCountBoth` checks COUNT's known-nonnullable native result. These profiles
do not change pq expectations or omit any metadata accessor.

Routine formal-catalog scenarios read the complete represented non-IN builtin
roster and explicit all-IN counterexamples through direct and prepared queries.
Declaration expectations come from released catalog/contrib sources. Installed
owners, canonical aggregate signatures and generated constructor relationships
reuse the native replay helpers; generated identities are checked within each
owner, not pinned to another backend's allocation sequence. Raw formal-array
checks retain oid[] and internal "char"[] metadata even for NULL values.

```sh
make check RUN='^TestReferenceRoutineResults'
make check RUN='^TestReferenceRoutineResults' REFERENCE=1
make check RUN='^TestReferenceQueryOuterJoin'
make check RUN='^TestReferenceQueryOuterJoin' REFERENCE=1
```
