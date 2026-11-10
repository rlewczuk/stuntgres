# reference/binary_json_arrays

Dual-mode scenarios for JSON/JSONB and SQL/JSON scalar processing, plus `intagg`.
Collection and record-producing consumers belong to [cursors](../cursors/README.md#testing),
JSONPath to [datetime_formatting](../datetime_formatting/README.md#testing),
relational JSON_TABLE to [query_outer](../query_outer/README.md#testing), and
JSON aggregates to [aggregates](../aggregates/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses the default `fixture.Main(m)`.

- Family-local helpers stay in `<family>_helpers_test.go`. Cross-family runners,
  quoting, metadata and cancellation helpers have one owner in
  [fixture](../fixture/README.md#helpers).
- Typed work-class admission proves entry at a real provider quantum.
  `OutputGateCancel` instead proves cancellation after completed output exists;
  provider-internal interruption belongs to private units.
- **Client metadata.** lib/pq reports an empty `DatabaseTypeName` for
  user-defined arrays, composites and JSONPATH; `interface{}` ScanType and byte
  payloads for JSON, JSONPATH and RECORD (native: type name, string); BYTEA
  `Length` `(MaxInt64, true)` (native `(0, false)`); unsized VARCHAR length -5
  (native MaxInt64). Native reports known nullability for stored table and view
  columns; computed projections are unknown on both. Assert each backend before
  normalization.

Add `REFERENCE=1` for the oracle.

- JSON/JSONB families: `RUN='^TestReferenceJSON'`
- SQL/JSON: `RUN='^TestReferenceSQLJSON'`
- `intagg`: `RUN='^TestReferenceIntagg'`
