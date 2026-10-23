# reference/cursors

SQL cursors: DECLARE/FETCH/MOVE/CLOSE for forward-only, SCROLL and WITH HOLD
cursors, cursor motion, holdable-cursor materialization at COMMIT, and the
`pg_cursors` view. Related families: wire portals and portal/cursor name
collisions in [sessions](../sessions/README.md#testing); the WITH HOLD/PREPARE
restriction (`TestReferencePreparedTransactionSessionBoundaries`) in
[locking_transactions](../locking_transactions/README.md#testing); positioned
`UPDATE`/`DELETE ... WHERE CURRENT OF` (`^TestReference(Delete|Update)CurrentOf`)
in [dml](../dml/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md).

- `TestMain` is `fixture.Main(m)`.
- Helpers: `sql_cursor_test.go` (`sqlCursorScenario`, `sqlCursorStep`,
  `sqlCursorSetup`, `sqlCursorError`, `sqlCursorBatch`) and
  `pg_cursors_helpers_test.go` (`pgcScenario`, `pgcRead`, `pgcClock`, ...).
- After changing an oracle expectation, run the whole `^TestReferenceSQLCursor`
  family: a driver panic truncates later tests, so a focused pass does not prove
  the family.
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
- `TestReferenceSQLCursorRegtypeWarnings` shares the direct-operation warning
  contract of `TestReferenceRegtypeWarnings` in
  [type_resolution](../type_resolution/README.md#testing); run both after
  changing either.
- After changing shared expression authority, also run
  `RUN='^TestReference(SQLCursor|AdvisorySession|PreparedTransactionSessionBoundaries)'`.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- All SQL cursors: `RUN='^TestReferenceSQLCursor'`
- WITH HOLD cursors: `RUN='^TestReferenceSQLCursorHold'`
- SCROLL and motion (FETCH/MOVE directions): `RUN='^TestReferenceSQLCursor(Scroll|Move|Motion|NoScroll|ZeroAndDirections)'`
- `pg_cursors`: `RUN='^TestReferencePGCursors'`
