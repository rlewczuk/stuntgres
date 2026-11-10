# reference/dml

Dual-mode scenarios for data modification and streaming COPY.
Sequence/identity semantics belong to [sequences](../sequences/README.md#testing);
row locking to [locking_transactions](../locking_transactions/README.md#testing).
COPY into virtual generated columns lives here to reuse the COPY transport.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses default options.
COPY transports and notice connections are documented in the
[fixture README](../fixture/README.md).

- COPY helpers: `copyTextScenario` (`copy_text_helpers_test.go`) borrows a pinned
  connection per backend (native `p.Native` with the public `CopyFrom`/`CopyTo`;
  PostgreSQL `p.CopyPostgres`, the pgx stream) and wraps it in
  `copyTextConnection` (`from`, `to`, `exec`, `output`). `copyBinaryScenario` and
  the hand-built frame helpers `copyBinaryHeader`/`Row`/`Stream`/`16`/`32`/`64`
  live in `copy_binary_helpers_test.go`; `copyFilterScenario`
  (`copy_filter_helpers_test.go`) adds notice recording through
  `fixture.CopyNoticeConnection`. Use pgx only for COPY; other SQL uses the
  ordinary pools.
- COPY input and expected output bytes (binary frames, CSV/text payloads) are
  written by hand, never captured from native output. Query user type OIDs from
  the catalog (`regtype::bigint`) instead of hard-coding them.
- Other scenario helpers: `mergeInsertScenario` (`merge_insert_matching_test.go`,
  shared by the MERGE families), `truncateScenario`
  (`truncate_helpers_test.go`), `truncateRestartScenario`
  (`truncate_restart_helpers_test.go`), `updateFromReference`
  (`update_from_helpers_test.go`), `deleteUsingReference`
  (`delete_using_helpers_test.go`), `resultNullabilityPairs`
  (`result_nullability_helpers_test.go`) and `referenceInsertShortMetadata`
  (`insert_implicit_short_helpers_test.go`).
- Raw cells differ by client: pq returns NUMERIC and UUID as bytes (native:
  strings); pgx returns DATE as `time.Time` (native: string), XID as `int64`,
  an unregistered enum's type name as its decimal OID, and binary TIMESTAMPTZ in
  the host's local location. Assert these shapes before normalizing.
- On PostgreSQL the fixture's rebuilt `public` grants no USAGE to PUBLIC: grant it
  to restricted actors before asserting table- or column-level denial.
- To prove an expression is not evaluated, read the faulting operand (for example
  a zero denominator) from a table: PostgreSQL may fold constant VALUES
  expressions while planning.
- COPY cancellation and timeouts on PostgreSQL: observe the backend in
  `ClientRead` (and a true `pg_cancel_backend` result, or server
  `clock_timestamp()` passing `query_start`/`xact_start` plus the timeout), then
  release and join the blocked reader so COPY can drain. Release a complete
  record (for CSV, the closing quote) so malformed input cannot outrank the
  interrupt. Canceling the client context while sending EOF races autocommit.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- INSERT: `RUN='^TestReferenceInsert'`
- UPDATE: `RUN='^TestReferenceUpdate'`
- DELETE: `RUN='^TestReferenceDelete'`
- MERGE: `RUN='^TestReferenceMerge'`
- Data-modifying CTEs: `RUN='^TestReferenceCTEDML'`
- TRUNCATE: `RUN='^TestReferenceTruncate'`
- COPY: `RUN='^TestReferenceCopy'`
- Result metadata: `RUN='^TestReferenceResultNullability'`

Private engine units establish physical index use and exact lock scheduling;
public result rows alone do not prove either.
