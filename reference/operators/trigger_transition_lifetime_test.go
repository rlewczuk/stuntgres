package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func transitionFailingInsert(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prefix string) {
	t.Helper()
	userOperatorExec(t, p, b, c, "CREATE OR REPLACE FUNCTION "+prefix+"tt_insert() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO "+prefix+"tt_audit SELECT TG_OP,'NEW',id,v,g FROM \"New ' Rows\"; IF EXISTS(SELECT 1 FROM \"New ' Rows\" WHERE id<0) THEN PERFORM 1/0; END IF; INSERT INTO "+prefix+"tt_count VALUES(TG_OP,NULL,(SELECT count(*) FROM \"New ' Rows\")); RETURN NULL; END$$")
}
func transitionFailure(prefix string) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
	d.Context = "SQL statement \"SELECT 1/0\"\nPL/pgSQL function " + prefix + "tt_insert() line 1 at PERFORM"
	return d
}

func TestReferenceTriggerTransitionRollback(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		transitionSetup(t, p, b, c, "")
		transitionFailingInsert(t, p, b, c, "")
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		failure := fixture.ErrorDiagnostic("INSERT INTO tt(id,v) VALUES(-1,10),(-2,20) RETURNING id,v,g", transitionFailure(""))
		failure.SelectRows = true
		run(failure)
		checkEmpty := func() {
			run(transitionRows("SELECT id,v,g FROM tt"))
			run(transitionAudit(""))
			run(transitionCounts(""))
		}
		checkEmpty()
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("SAVEPOINT before_transition", 0))
		run(failure)
		run(fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
		run(fixture.Exec("ROLLBACK TO SAVEPOINT before_transition", 0))
		checkEmpty()
		run(fixture.Exec("INSERT INTO tt(id,v) VALUES(2,20)", 1))
		run(transitionAudit("", []any{"INSERT", "NEW", int64(2), int64(21), int64(42)}))
		run(fixture.Exec("ROLLBACK", 0))
		checkEmpty()
		userOperatorExec(t, p, b, c,
			"CREATE TABLE tt_parent(id integer)", "CREATE TABLE tt_resume(o bigint,n bigint,caught integer)",
			"CREATE FUNCTION tt_parent_body() RETURNS trigger LANGUAGE plpgsql AS $$DECLARE before_n bigint; caught integer:=0; BEGIN SELECT count(*) INTO before_n FROM outer_rows; BEGIN INSERT INTO tt(id,v) VALUES(-3,30),(-4,40); EXCEPTION WHEN division_by_zero THEN caught:=1; END; INSERT INTO tt(id,v) VALUES(3,30); INSERT INTO tt_resume VALUES(before_n,(SELECT count(*) FROM outer_rows),caught); RETURN NULL; END$$",
			"CREATE TRIGGER i AFTER INSERT ON tt_parent REFERENCING NEW TABLE outer_rows EXECUTE FUNCTION tt_parent_body()")
		run(fixture.Exec("INSERT INTO tt_parent VALUES(10),(20)", 2))
		run(transitionRows("SELECT id,v,g FROM tt", []any{int64(3), int64(31), int64(62)}))
		run(transitionAudit("", []any{"INSERT", "NEW", int64(3), int64(31), int64(62)}))
		run(transitionCounts("", []any{"INSERT", nil, int64(1)}))
		run(fixture.Select("SELECT o,n,caught FROM tt_resume", []string{"o", "n", "caught"}, []string{"INT8", "INT8", "INT4"}, [][]any{{int64(2), int64(2), int64(1)}}))
		run(fixture.Exec("INSERT INTO tt(id,v) VALUES(4,40)", 1))
		run(transitionAudit("", []any{"INSERT", "NEW", int64(3), int64(31), int64(62)}, []any{"INSERT", "NEW", int64(4), int64(41), int64(82)}))
	})
}

func TestReferenceTriggerTransitionCancellation(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE tt(id integer)", "CREATE TABLE tt_gate(id integer)", "CREATE TABLE tt_done(n bigint)",
			"CREATE FUNCTION tt_cancel() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM count(*) FROM x; INSERT INTO tt_gate SELECT id FROM x; INSERT INTO tt_done VALUES((SELECT count(*) FROM x)); RETURN NULL; END$$",
			"CREATE TRIGGER i AFTER INSERT ON tt REFERENCING NEW TABLE x EXECUTE FUNCTION tt_cancel()")
		q := "INSERT INTO tt VALUES(1),(2) RETURNING id"
		var stmt *sql.Stmt
		if prepared {
			var err error
			stmt, err = c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
		}
		holder, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
		defer holder.ExecContext(p.Ctx, "ROLLBACK")
		fixture.LockExec(t, p.Ctx, holder, "LOCK TABLE tt_gate IN ACCESS EXCLUSIVE MODE", 0)
		waiterPID, holderPID, oid := 0, 0, int64(0)
		if b.PG {
			waiterPID = fixture.LockPID(t, p.Ctx, c)
			holderPID = fixture.LockPID(t, p.Ctx, holder)
			oid = fixture.LockOID(t, p.Ctx, "tt_gate")
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		defer cancel()
		finished := make(chan struct{})
		join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
			defer close(finished)
			var rows *sql.Rows
			var err error
			if stmt != nil {
				rows, err = stmt.QueryContext(ctx)
			} else {
				rows, err = c.QueryContext(ctx, q)
			}
			return fixture.RelationOutcome{Rows: rows, Err: err}
		})
		joined := false
		defer func() {
			cancel()
			if !joined {
				outcome := join()
				if outcome.Rows != nil {
					outcome.Rows.Close()
				}
			}
		}()
		fixture.RelationLockPending(t, p, b, waiterPID, holderPID, oid, "RowExclusiveLock", finished)
		if b.PG {
			var signaled bool
			if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", waiterPID).Scan(&signaled); err != nil || !signaled {
				t.Fatal(signaled, err)
			}
		} else {
			cancel()
		}
		outcome := join()
		joined = true
		if outcome.Rows != nil {
			outcome.Rows.Close()
			t.Fatal("canceled transition exposed RETURNING rows")
		}
		if outcome.Result != nil {
			t.Fatal("canceled transition exposed a command result")
		}
		if b.PG {
			d := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
			d.Context = "SQL statement \"INSERT INTO tt_gate SELECT id FROM x\"\nPL/pgSQL function tt_cancel() line 1 at SQL statement"
			fixture.AssertDiagnostic(t, outcome.Err, &d)
		} else {
			fixture.LexicalOrdinary(t, outcome.Err, context.Canceled)
		}
		fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
		fresh, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		run := func(s fixture.Step) { afterStep(t, p, b, fresh, prepared, s) }
		for _, table := range []string{"tt", "tt_gate", "tt_done"} {
			run(fixture.Select("SELECT count(*) AS n FROM "+table, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		d := fixture.ControlsReferenceDiagnostic("42P01", `relation "x" does not exist`)
		d.Position = "15"
		s := fixture.ErrorDiagnostic("SELECT * FROM x", d)
		s.SelectRows = true
		run(s)
		run(fixture.Exec("INSERT INTO tt VALUES(3),(4)", 2))
		run(fixture.Select("SELECT id FROM tt_gate ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(4)}}))
		run(fixture.Select("SELECT n FROM tt_done", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
	})
}
