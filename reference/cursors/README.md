# reference/cursors

SQL cursors: DECLARE/FETCH/MOVE/CLOSE for forward-only, SCROLL and WITH HOLD
cursors, cursor motion, holdable-cursor materialization at COMMIT, and the
`pg_cursors` view. Related families: wire portals and portal/cursor name
collisions in [sessions](../sessions/README.md#testing); the WITH HOLD/PREPARE
restriction (`TestReferencePreparedTransactionSessionBoundaries`) in
[locking_transactions](../locking_transactions/README.md#testing); positioned
`UPDATE`/`DELETE ... WHERE CURRENT OF` (`^TestReference(Delete|Update)CurrentOf`)
in [dml](../dml/README.md#testing).

PL/pgSQL cursor scenarios cover genuine refcursor values, static and dynamic OPEN,
bound arguments, FETCH targets and motion, FOUND, cursor FOR, physical-session
identity, transaction lifetime and native definition replay. Omitted SCROLL follows
query implementation capability; explicit option witnesses cover guaranteed
backward behavior independently of cost-selected PostgreSQL strategies.
The contextual SLICE token remains an ordinary count-variable name in
FORWARD/BACKWARD MOVE, preserving ordered scroll-cursor positions.
Ownership scenarios exercise promoted FETCH/MOVE inside enclosing writes, retained
FOR identity under variable reassignment, and pinned-portal CLOSE errors with
cleanup. User drop protection is distinct from terminal transaction/session cleanup.
Loop-transfer scenarios observe owned portal cleanup before transaction end,
including outward EXIT/CONTINUE, cursor-FOR label targets, FOUND publication and
initially NULL cursor restoration.
Source/phase scenarios compare complete diagnostics for static, bound and dynamic
OPEN plus count expressions, including Unicode/grouping and independent action
lines. Bound arguments bind/evaluate as a whole first-row projection before formal
conversion; empty and NULL-row FOUND controls remain distinct. Missing-portal
counts prove precedence with nontransactional sequence effects. Nested unnamed
opens must retain two distinct usable identities. CASE integration checks the
existing diagnostic datum ledger's private cursor/FETCH ROWs and lexical field
identity without changing executable variable/search slots.
Publication scenarios inspect handler-visible FOUND before bound-formal conversion
and preserve its previous value when the argument query fails. Initially NULL
constant cursors fail only at generated-name assignment, after earlier query and
USING work; non-NULL constant names remain usable. Complete diagnostics, prepared
reuse, sequence effects and portal cleanup are asserted before healthy recovery.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md).

- `TestMain` is `fixture.Main(m)`.
- Helpers: `sql_cursor_test.go` (`sqlCursorScenario`, `sqlCursorStep`,
  `sqlCursorSetup`, `sqlCursorError`, `sqlCursorBatch`) and
  `pg_cursors_helpers_test.go` (`pgcScenario`, `pgcRead`, `pgcClock`, ...).
- PL helpers reuse `sqlCursorScenario`/`sqlCursorStep`; value witnesses include
  direct, fresh prepared and held reuse. Native controls remain explicit native
  phases, and PostgreSQL server cancellation has a real admission barrier.
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
- PL/pgSQL cursors: `RUN='^TestReferencePLpgSQLCursor'`
- PL publication/loop ownership: `RUN='^TestReferencePLpgSQLCursor(For|PortalOwnership|LoopTransfers)$'`
- PL source/phase, count precedence and nested identities: `RUN='^TestReferencePLpgSQLCursor(ErrorProvenance|CountPrecedence|NestedOpenNames)$'`
- PL cursor/CASE diagnostic ledger: `RUN='^TestReferencePLpgSQLCursorDiagnosticLedger$'`
- PL FOUND/name publication: `RUN='^TestReferencePLpgSQLCursor(FoundPublication|ConstantOpen)$'`
- Contextual FOREACH keyword in counted MOVE: `RUN='^TestReferencePLpgSQLForeachCursorKeyword$'`
