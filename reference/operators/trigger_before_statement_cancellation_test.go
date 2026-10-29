package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerBeforeStatementCancellation(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		beforeStatementSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, "CREATE TABLE bst_block(id integer)", "CREATE FUNCTION bst_block() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO bst_log(name) VALUES('entered'); INSERT INTO bst_block VALUES(1); RETURN NULL; END$$", "CREATE TRIGGER a BEFORE INSERT ON bst EXECUTE FUNCTION bst_block()")
		q := "INSERT INTO bst(id,v) VALUES(1,10) RETURNING id,v"
		var h *sql.Stmt
		if prepared {
			var err error
			h, err = c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
		}
		holder, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
		defer holder.ExecContext(p.Ctx, "ROLLBACK")
		fixture.LockExec(t, p.Ctx, holder, "LOCK TABLE bst_block IN ACCESS EXCLUSIVE MODE", 0)
		pid, hpid, oid := 0, 0, int64(0)
		if b.PG {
			pid = fixture.LockPID(t, p.Ctx, c)
			hpid = fixture.LockPID(t, p.Ctx, holder)
			oid = fixture.LockOID(t, p.Ctx, "bst_block")
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		defer cancel()
		finished := make(chan struct{})
		join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
			defer close(finished)
			var rows *sql.Rows
			var err error
			if h != nil {
				rows, err = h.QueryContext(ctx)
			} else {
				rows, err = c.QueryContext(ctx, q)
			}
			return fixture.RelationOutcome{Rows: rows, Err: err}
		})
		joined := false
		defer func() {
			cancel()
			if !joined {
				out := join()
				if out.Rows != nil {
					out.Rows.Close()
				}
			}
		}()
		fixture.RelationLockPending(t, p, b, pid, hpid, oid, "RowExclusiveLock", finished)
		if b.PG {
			var ok bool
			if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&ok); err != nil || !ok {
				t.Fatal(ok, err)
			}
		} else {
			cancel()
		}
		out := join()
		joined = true
		if out.Rows != nil {
			out.Rows.Close()
			t.Fatal("canceled statement returned rows")
		}
		if out.Result != nil {
			t.Fatal("canceled statement returned completion")
		}
		if b.PG {
			d := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
			d.Context = "SQL statement \"INSERT INTO bst_block VALUES(1)\"\nPL/pgSQL function bst_block() line 1 at SQL statement"
			fixture.AssertDiagnostic(t, out.Err, &d)
		} else {
			fixture.LexicalOrdinary(t, out.Err, context.Canceled)
		}
		fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
		fresh, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		for _, table := range []string{"bst", "bst_log", "bst_block"} {
			fixture.RunStep(t, p.Ctx, fresh, b.Recorder, beforeStatementCount(table, 0))
		}
		fixture.RunStep(t, p.Ctx, fresh, b.Recorder, fixture.Exec("INSERT INTO bst(id,v) VALUES(2,20)", 1))
		for _, table := range []string{"bst", "bst_log", "bst_block"} {
			fixture.RunStep(t, p.Ctx, fresh, b.Recorder, beforeStatementCount(table, 1))
		}
	})
}
