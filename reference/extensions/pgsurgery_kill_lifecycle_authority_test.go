package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func ksLifeWaitAuthorityCases(t *testing.T) {
	t.Helper()
	t.Run("postwait_execute_revoke", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ks_wait_owner"})
		ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'target') RETURNING k,v,ctid,xmin,xmax")
			for _, q := range []string{"CREATE ROLE ks_wait_owner", "ALTER TABLE ks_part.heap OWNER TO ks_wait_owner", "GRANT USAGE ON SCHEMA ks_part TO ks_wait_owner", "GRANT EXECUTE ON FUNCTION ks_part.heap_force_kill(regclass,tid[]) TO ks_wait_owner"} {
				freezeRun(t, p, b, c, fixture.Exec(q, 0))
			}
			holder := freezeConn(t, p, b)
			defer holder.Close()
			defer holder.ExecContext(p.Ctx, "ROLLBACK")
			st, err := c.PrepareContext(p.Ctx, killSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			var oid int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'ks_part.heap'::regclass::oid").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			waiterPID, holderPID := 0, 0
			if b.PG {
				waiterPID, holderPID = fixture.LockPID(t, p.Ctx, c), fixture.LockPID(t, p.Ctx, holder)
			}
			freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION ks_wait_owner", 0))
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			freezeRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
			freezeRun(t, p, b, holder, fixture.Exec("LOCK TABLE ks_part.heap IN ACCESS EXCLUSIVE MODE", 0))
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			finished := make(chan struct{})
			await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
				defer close(finished)
				rows, err := st.QueryContext(ctx, "ks_part.heap", freezeTIDs(a.tid))
				return fixture.RelationOutcome{Rows: rows, Err: err}
			})
			joined := false
			defer func() {
				if !joined {
					cancel()
					out := await()
					if out.Rows != nil {
						out.Rows.Close()
					}
				}
			}()
			fixture.RelationLockPending(t, p, b, waiterPID, holderPID, oid, "RowExclusiveLock", finished)
			freezeRun(t, p, b, holder, fixture.Exec("REVOKE EXECUTE ON FUNCTION ks_part.heap_force_kill(regclass,tid[]) FROM ks_wait_owner", 0))
			freezeRun(t, p, b, holder, fixture.Exec("COMMIT", 0))
			out := await()
			joined = true
			want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function heap_force_kill")
			if b.PG {
				// Released C has already passed fmgr's EXECUTE check before its
				// relation_open wait. This phase-specific expectation is source-
				// derived, awaiting root's oracle, not native/PG parity.
				ksLifeVoidRows(t, out.Rows, out.Err)
				ksLifeRows(t, p, b, holder)
			} else {
				if out.Rows != nil {
					out.Rows.Close()
				}
				fixture.AssertDiagnostic(t, out.Err, &want)
				ksRows(t, p, b, holder, a)
			}
			// A subsequent non-NULL Execute is new admission on both backends.
			// Constant strict NULLs can be simplified before fmgr admission;
			// Permissions separately covers nonfoldable runtime NULL authority.
			held := fixture.HeldStatement{Executor: c, Statement: st, SQL: killSQL}
			step := fixture.ImplicitError(killSQL, want, "ks_part.heap", "{}")
			step.SelectRows = true
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		})
	})
	t.Run("peer_tuple_and_unrelated_contributions", func(t *testing.T) {
		ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'target') RETURNING k,v,ctid,xmin,xmax")
			freezeRun(t, p, b, c, fixture.Exec("CREATE TABLE ks_part.peer_unrelated(k integer)", 0))
			holder := freezeConn(t, p, b)
			defer holder.Close()
			defer holder.ExecContext(p.Ctx, "ROLLBACK")
			freezeRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
			freezeRun(t, p, b, holder, fixture.Exec("LOCK TABLE ks_part.peer_unrelated IN ACCESS EXCLUSIVE MODE", 0))
			freezeRun(t, p, b, holder, freezeVoid("SELECT pg_advisory_xact_lock(735194::bigint) AS repaired"))
			freezeRun(t, p, b, holder, fixture.Select("SELECT k FROM ks_part.heap WHERE k=1 FOR UPDATE", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
			// Surgery bypasses ordinary row ownership without releasing it or
			// the peer's unrelated relation/advisory lock contributions.
			ksLifeApply(t, p, b, c, a.tid)
			ksLifeRows(t, p, b, c)
			freezeRun(t, p, b, holder, fixture.Select("SELECT count(*) AS n FROM pg_locks WHERE pid=pg_backend_pid() AND granted AND ((locktype='relation' AND relation='ks_part.peer_unrelated'::regclass AND mode='AccessExclusiveLock') OR locktype='advisory')", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
			freezeRun(t, p, b, c, fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			freezeRun(t, p, b, c, fixture.ImplicitError("LOCK TABLE ks_part.peer_unrelated IN ACCESS SHARE MODE NOWAIT", fixture.ControlsReferenceDiagnostic("55P03", `could not obtain lock on relation "ks_part.peer_unrelated"`)))
			freezeRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
			freezeRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
			ksLifeRows(t, p, b, c)
			freezeRun(t, p, b, c, fixture.Exec("BEGIN", 0))
			freezeRun(t, p, b, c, fixture.Exec("LOCK TABLE ks_part.heap,ks_part.peer_unrelated IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
			freezeRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
		})
	})
}

func ksLifeRenameWaitCase(t *testing.T) {
	t.Helper()
	t.Run("genuine_routine_rename_during_wait", func(t *testing.T) {
		ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'target') RETURNING k,v,ctid,xmin,xmax")
			holder := freezeConn(t, p, b)
			defer holder.Close()
			defer holder.ExecContext(p.Ctx, "ROLLBACK")
			var oid int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'ks_part.heap'::regclass::oid").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			waiterPID, holderPID := 0, 0
			if b.PG {
				waiterPID, holderPID = fixture.LockPID(t, p.Ctx, c), fixture.LockPID(t, p.Ctx, holder)
			}
			freezeRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
			freezeRun(t, p, b, holder, fixture.Exec("LOCK TABLE ks_part.heap IN ACCESS EXCLUSIVE MODE", 0))
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			finished := make(chan struct{})
			await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
				defer close(finished)
				rows, err := c.QueryContext(ctx, killSQL, "ks_part.heap", freezeTIDs(a.tid))
				return fixture.RelationOutcome{Rows: rows, Err: err}
			})
			joined := false
			defer func() {
				if !joined {
					cancel()
					out := await()
					if out.Rows != nil {
						out.Rows.Close()
					}
				}
			}()
			fixture.RelationLockPending(t, p, b, waiterPID, holderPID, oid, "RowExclusiveLock", finished)
			// Name is not installed authority. Native SameRoutineDeclaration
			// excludes name/namespace; PG already admitted the genuine OID.
			freezeRun(t, p, b, holder, fixture.Exec("ALTER FUNCTION ks_part.heap_force_kill(regclass,tid[]) RENAME TO renamed_wait", 0))
			freezeRun(t, p, b, holder, fixture.Exec("COMMIT", 0))
			out := await()
			joined = true
			ksLifeVoidRows(t, out.Rows, out.Err)
			ksLifeRows(t, p, b, holder)
			q := "SELECT ks_part.renamed_wait($1::regclass,$2::tid[]) AS killed"
			freezeBoth(t, p, b, holder, fixture.Select(q, []string{"killed"}, []string{"VOID"}, [][]any{{""}}, "ks_part.heap", "{}"))
		})
	})
}
