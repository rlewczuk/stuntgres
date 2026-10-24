# Engine split: `internal/engine/exec`, fifth round

Session prompt for linear item 163 of the
[status page](ENGINE-SPLIT-STATUS.md). The rules are in
[ENGINE-SPLIT.md](ENGINE-SPLIT.md) §2, §3 and §5, and the package contract
is in [the exec README](../internal/engine/exec/README.md). Sizes were
measured at `fe0683b68`; re-run `reach` on the move set first.

## Item 163 — SQL cursor positioning

Needs —. About 0.2k lines move. Item 165 also edits `protocol_portal.go`,
so whichever of the two lands second rebases over the other.

**Moves.**
- All of `sql_cursor_motion.go` (`rewind`, `run`, `motion`).
- The `cursorPosition` type and its constants, and
  `forwardSQLCursorError` (`sql_cursor.go`).

**Cut.**
- `*StatementScope` is used only for the context, `openProducer` and
  `finishForeignOwner`. Replace it with an `exec.CursorProducer` capability
  with `Open()` and `Exhausted()`; the root cursor implements it.
- The position fields become an exported value embedded in `sqlCursor`.
  That changes about 80 field uses in 6 files: `current_of.go`, `discard.go`,
  `sql_cursor_hold.go`, `protocol_portal.go`, `statement_host_dml.go` and
  `sql_cursor.go`.

**Callers and locks.** FETCH, MOVE, portal Execute, COPY (query) rows and
HOLD materialization all call with nothing held.

**Stays root.** The cursor registry, admission, HOLD materialization,
disposal and the `sqlCursor` struct itself, which holds `AuthorizationView`s
and row-security programs.

**Risks.**
- The FETCH and MOVE row counts, the boundary positions, the error text and
  the lock-timeout behaviour of a second fetch stay identical.
- No allocation is added per fetched row.

**Tests.**
- Root units: grep `*_test.go` for the moved names; pure motion tests move to
  `exec`.
- Quick and `REFERENCE=1` selections: `TestReferenceSQLCursor`,
  `TestReferenceDeleteCurrentOf` and `TestReferenceServerPortal`.

**Docs.** The exec README lists "SQL cursors" among what stays in the
engine; narrow that to the cursor registry, admission and HOLD, and add
cursor positioning to what the package owns.

## Status

- 163: done (`a172d7daf`, `36806a268`, then the docs commit).
  - On `exec` (`cursor_motion.go`): `CursorPlace` with `CursorBefore`,
    `CursorOn`, `CursorAfter` and `CursorFailed`; `CursorPosition` (`Place`,
    `OrdinalPosition`, `AtEnd`, `CurrentSource`, `CurrentIndex`), embedded in
    the root `sqlCursor`, with `Move` and `Rewind`; the `CursorProducer`
    capability and the `CursorInput` value. The forward-only error is the
    unexported `forwardCursorError`.
  - The root implements the capability with `sqlCursorProducer` (the cursor
    and its admitting scope) and builds the input in `motionInput`; FETCH,
    MOVE and HOLD materialization call `Move` or `Rewind`.
    `sql_cursor_motion.go` is gone.
  - Deviations: the capability also has `Context()`, because motion read
    `s.op.Context()` at each use and `Operation.Context` changes while COPY
    input is held. The capability is a two-pointer value rather than
    `*sqlCursor` reading the scope back from `authority.Host`, so the motion
    never depends on the host field a nested statement could clear. Boxing it
    costs one 16-byte allocation per live FETCH or MOVE statement, none per
    row. `statement_host_dml.go` had no position-field uses.
  - Root 401 → 400 files and 49.5k → 49.3k lines; `exec` gains 0.26k
    production lines.
  - Tests: `TestSQLCursorRetainedProducerReset` moved to `exec` as
    `TestCursorRetainedProducerReset`; new `TestCursorStoreMotion` and
    `TestCursorForwardProducerMotion` pin the seam. Root cursor, CURRENT OF,
    `pg_cursors`, PL cursor and truncate units were adapted to the field
    names only.
