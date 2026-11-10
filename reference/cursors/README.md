# reference/cursors

Dual-mode scenarios for SQL and PL/pgSQL cursors, array consumers and JSON record
expansion. Wire portals belong to [sessions](../sessions/README.md#testing);
positioned DML to [dml](../dml/README.md#testing); general JSON operators to
[binary_json_arrays](../binary_json_arrays/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md).

- `TestMain` is `fixture.Main(m)`.
- Filter `pg_cursors` assertions to scenario-owned cursor names; PostgreSQL
  also lists observer and protocol portals.
- Create fixture tables before BEGIN on both backends.
- lib/pq keeps its old binary decoder after a same-name INT4→TEXT cursor rebind,
  and iterating the new TEXT rows panics. Inspect such a Query's metadata without
  calling `Next`, close the Rows, and check the values through a fresh direct
  FETCH.
- Role scenarios grant `USAGE ON SCHEMA public` explicitly on PostgreSQL: the
  oracle's recreated `public` has no PUBLIC USAGE.
- `fixture.SQLCursorCancelDemand` is shared by cursor FETCH, HOLD COMMIT and the
  SQL-prepared write cancellation in `sessions`. After changing it, run
  `RUN='^TestReference(SQLCursorCancellation|SQLCursorHoldCancellation|SQLPreparedCanceledWrite)$'`
  in both modes.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- SQL cursors: `RUN='^TestReferenceSQLCursor'`
- Cursor catalog: `RUN='^TestReferencePGCursors'`
- PL/pgSQL cursors: `RUN='^TestReferencePLpgSQLCursor'`
- Array families: `RUN='^TestReferenceArray'`
- JSON records: `RUN='^TestReferenceJSONB?Record'`

Array helpers stay in `array_*_helpers_test.go` and `array_search_fixture_test.go`;
record helpers stay in `json_record_helpers_test.go` and `jsonb_record_helpers_test.go`.
Shared metadata and direct/prepared runners use `fixture.ArrayAggBoth`,
`ArrayAggMetadata`, `JSONTypeMetadataCheck`, `JSONTypeWithMetadata`,
`JSONTypeSourceColumn`, `JSONEachBoth` and `JSONKeysError`.
`array_sample`/`array_shuffle` follow the shared random-property rule.
