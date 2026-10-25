# reference/dml

Data modification: INSERT (VALUES, SELECT, implicit short column lists,
RETURNING, ON CONFLICT DO NOTHING/DO UPDATE), UPDATE (core, FROM, RETURNING,
row-valued assignments), DELETE (core, USING, RETURNING), positioned
`WHERE CURRENT OF` UPDATE/DELETE, data-modifying CTEs, MERGE (all action kinds,
RETURNING), TRUNCATE (including RESTART IDENTITY), result-column nullability and
COPY (text, CSV, binary, WHERE/ON_ERROR filtering). Related families: COPY into
virtual generated columns (`GeneratedVirtualCopy*`) lives here to reuse
`copyTextScenario` but belongs to the sequences `GeneratedVirtual` family;
sequence and identity semantics in `reference/sequences`; row locking in
`reference/locking_transactions`.

Column expressions use the same explicit namespace rules as queries: target,
source and RETURNING visibility belong to their statement clause, not to the
number of written name components. Aliases hide base names; qualification does
not grant read privileges or expose an INSERT producer's destination.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses default options.
COPY transports and notice connections are documented in the
[fixture README](../fixture/README.md).
INSERT RETURNING observes final BEFORE replacement rows and omits suppressed
rows from both output and affected counts. Shared trigger cases cover expression
and conditional PL row replacement and suppression.

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
- MERGE matched-action order is unspecified, and source ORDER BY does not order
  an equality join; ordered witnesses need a non-equality join condition.
- COPY cancellation and timeouts on PostgreSQL: observe the backend in
  `ClientRead` (and a true `pg_cancel_backend` result, or server
  `clock_timestamp()` passing `query_start`/`xact_start` plus the timeout), then
  release and join the blocked reader so COPY can drain. Release a complete
  record (for CSV, the closing quote) so malformed input cannot outrank the
  interrupt. Canceling the client context while sending EOF races autocommit.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- <a id="result-nullability"></a>Result-column nullability: `RUN='^TestReferenceResultNullability'`
- Qualified column consumers: `RUN='^TestReferenceThreePart'`
- INSERT ... SELECT: `RUN='^TestReference(Smoke)?InsertSelect'`
- Implicit short INSERT: `RUN='^TestReferenceInsertImplicitShort'`
- INSERT RETURNING: `RUN='^TestReferenceInsertReturning'`
- <a id="insert-conflict-omission-acceptance"></a><a id="insert-conflict-update-acceptance"></a>INSERT ON CONFLICT: `RUN='^TestReferenceInsertConflict'`
- UPDATE core: `RUN='^TestReferenceUpdateCore'`
- UPDATE FROM: `RUN='^TestReferenceUpdateFrom'`
- UPDATE RETURNING: `RUN='^TestReferenceUpdateReturning'`
- Row-valued UPDATE assignments: `RUN='^TestReferenceUpdateRow'`
- DELETE core: `RUN='^TestReferenceDeleteCore'`
- DELETE USING: `RUN='^TestReferenceDeleteUsing'`
- DELETE RETURNING (with series and FK callers): `RUN='^TestReference[A-Za-z]*DeleteReturning'`
- Positioned UPDATE/DELETE: `RUN='^TestReference[A-Za-z]+CurrentOf'`
  `TestReferenceUpdateCurrentOfVersionsAndArbitration` preserves the predecessor,
  isolation and four cursor-lock lanes and adds NO KEY UPDATE peer-recheck
  schedules: actual blocked admission and owner commit before peer invocation,
  each with direct/fresh/held owner × peer routes. Held handles are reused after
  warmup rollback on the same pinned sessions. Private
  `TestUpdateCurrentOfPeerCommitRecheck` separately fixes the snapshot-selection /
  first-target-capture gap and inspects stable RowID, latest predecessor Base and
  private replacement publication; public ordering alone does not prove that gap.
- Data-modifying CTEs: `RUN='^TestReferenceCTEDML'`
- MERGE INSERT: `RUN='^TestReferenceMergeInsert'`
- MERGE matched UPDATE: `RUN='^TestReferenceMergeUpdate'`
- MERGE BY SOURCE UPDATE: `RUN='^TestReferenceMergeSourceUpdate'`
- MERGE DELETE: `RUN='^TestReferenceMergeDelete'`
- MERGE DO NOTHING: `RUN='^TestReferenceMergeNothing'`
- MERGE RETURNING: `RUN='^TestReferenceMergeReturning'`
- TRUNCATE (including RESTART IDENTITY): `RUN='^TestReferenceTruncate'`
- <a id="copy-text-selected-proof"></a>COPY text: `RUN='^TestReferenceCopyText'`
- COPY CSV: `RUN='^TestReferenceCopyCSV'`
- COPY binary: `RUN='^TestReferenceCopyBinary'`
- <a id="copy-filtering-and-conversion-error-policy"></a>COPY WHERE and ON_ERROR: `RUN='^TestReferenceCopyFilter'`
- <a id="installed-uuid-ossp-binary-copy"></a>uuid-ossp binary COPY: `RUN='^TestReferenceUUIDOsspCopyBinary$'`

Binary range transport over the COPY stream:
`make check RUN='^TestReference(DateRangeBinary|TimestampRangeComposition|TimestamptzRangeBinaryComposition)$'`
(`TimestampRangeComposition` also runs its `reference/ranges_text_search` namesake). COPY into
TEMP namespaces: `make check RUN='^TestReferencePGNamespaceCopyStoragePrecedence$'`.
Engine and parser units: `make check RUN='^Test(Merge|InsertConflict|InsertReturning|UpdateRow|CTEDML|Truncate|Copy|DeleteCurrentOf|UpdateCurrentOf|DeleteUsing)'`.
