package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTriggerInsteadPreparedLifetime(t *testing.T) {
	insteadAuthority(t)
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		insteadSetup(t, p, b, c)
		qs := []string{"INSERT INTO io_view VALUES($1,$2) RETURNING id,v", "UPDATE io_view SET v=$2 WHERE id=$1 RETURNING id,v", "DELETE FROM io_view WHERE id=$1 RETURNING id,v"}
		handles := make([]*sql.Stmt, len(qs))
		for i, q := range qs {
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			handles[i] = s
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		held := func(i int, rows [][]any, args ...any) {
			s := triggerRows(qs[i], rows...)
			s.Args = args
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: handles[i], SQL: qs[i]}, b.Recorder, s)
		}
		run(insteadLog("SELECT op,id,ov,nv FROM io_log"))
		held(0, [][]any{{int64(1), int64(110)}}, 1, 10)
		held(1, [][]any{{int64(1), int64(120)}}, 1, 20)
		held(2, [][]any{{int64(1), int64(21)}}, 1)
		userOperatorExec(t, p, b, c, "ALTER FUNCTION io_body() RENAME TO io_real", "CREATE FUNCTION io_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=9999; RETURN NEW; END$$")
		held(0, [][]any{{int64(2), int64(130)}}, 2, 30)
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE FUNCTION io_real() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF TG_OP='DELETE' THEN DELETE FROM io_base WHERE id=OLD.id; RETURN OLD; END IF; IF TG_OP='INSERT' THEN INSERT INTO io_base VALUES(NEW.id,NEW.v+2); ELSE UPDATE io_base SET v=NEW.v+2 WHERE id=OLD.id; END IF; NEW.v:=NEW.v+200; RETURN NEW; END$$")
		held(0, [][]any{{int64(3), int64(240)}}, 3, 40)
		held(1, [][]any{{int64(2), int64(250)}}, 2, 50)
		userOperatorExec(t, p, b, c, "BEGIN", "SAVEPOINT definitions", "CREATE OR REPLACE TRIGGER body INSTEAD OF INSERT OR UPDATE OR DELETE ON io_view FOR EACH ROW EXECUTE FUNCTION io_body()")
		defer c.ExecContext(context.Background(), "ROLLBACK")
		held(0, [][]any{{int64(4), int64(9999)}}, 4, 60)
		run(triggerRows("SELECT id,v FROM io_base ORDER BY id", []any{int64(2), int64(52)}, []any{int64(3), int64(42)}))
		userOperatorExec(t, p, b, c, "ROLLBACK TO definitions", "COMMIT")
		held(0, [][]any{{int64(4), int64(260)}}, 4, 60)
		userOperatorExec(t, p, b, c, "DROP TRIGGER body ON io_view")
		held(0, [][]any{{int64(5), int64(70)}}, 5, 70)
		held(1, [][]any{{int64(5), int64(80)}}, 5, 80)
		held(2, [][]any{{int64(5), int64(80)}}, 5)
		userOperatorExec(t, p, b, c, "CREATE TRIGGER body INSTEAD OF INSERT OR UPDATE OR DELETE ON io_view FOR EACH ROW EXECUTE FUNCTION io_real()")
		held(1, [][]any{{int64(4), int64(290)}}, 4, 90)
		held(2, [][]any{{int64(4), int64(92)}}, 4)
		run(triggerRows("SELECT id,v FROM io_base ORDER BY id", []any{int64(2), int64(52)}, []any{int64(3), int64(42)}))
		userOperatorExec(t, p, b, c, "CREATE VIEW io_nonauto AS SELECT id,v FROM io_base UNION ALL SELECT id,v FROM io_base", "CREATE TRIGGER body INSTEAD OF INSERT ON io_nonauto FOR EACH ROW EXECUTE FUNCTION io_real()")
		q := "INSERT INTO io_nonauto VALUES($1,$2) RETURNING id,v"
		s, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		userOperatorExec(t, p, b, c, "DROP TRIGGER body ON io_nonauto")
		d := fixture.ControlsReferenceDiagnostic("55000", `cannot insert into view "io_nonauto"`)
		d.Detail = "Views containing UNION, INTERSECT, or EXCEPT are not automatically updatable."
		d.Hint = "To enable inserting into the view, provide an INSTEAD OF INSERT trigger or an unconditional ON INSERT DO INSTEAD rule."
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: s, SQL: q}, b.Recorder, fixture.ErrorDiagnostic(q, d, 9, 9))
		if !b.PG {
			old, err := handles[0].QueryContext(p.Ctx, 10, 10)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			columns, err := old.Columns()
			if err != nil || len(columns) != 2 {
				t.Fatal(columns, err)
			}
			columns[0] = "mutated caller slice"
			held(0, [][]any{{int64(11), int64(220)}}, 11, 20)
			userOperatorExec(t, p, b, c, "DROP TRIGGER body ON io_view")
			columns, err = old.Columns()
			if err != nil || len(columns) != 2 || columns[0] != "id" || columns[1] != "v" {
				t.Fatal(columns, err)
			}
			types, err := old.ColumnTypes()
			if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "INT4" {
				t.Fatal(types, err)
			}
			if !old.Next() {
				t.Fatal(old.Err())
			}
			var id, v int64
			if err := old.Scan(&id, &v); err != nil || id != 10 || v != 210 {
				t.Fatal(id, v, err)
			}
			if old.Next() || old.Err() != nil {
				t.Fatal(old.Err())
			}
		}
	})
}

func TestReferenceTriggerInsteadConcurrency(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, drop := range []bool{false, true} {
			t.Run(fmt.Sprintf("commit=%t/drop=%t", commit, drop), func(t *testing.T) {
				triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					insteadSetup(t, p, b, c)
					peer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					defer c.ExecContext(context.Background(), "ROLLBACK")
					q := "INSERT INTO io_view VALUES($1,$2) RETURNING id,v"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					run := func(conn *sql.Conn, s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
					userOperatorExec(t, p, b, c, "CREATE FUNCTION io_replacement() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO io_base VALUES(NEW.id,NEW.v+2); INSERT INTO io_log VALUES('replacement',NEW.id,NULL,NEW.v); NEW.v:=NEW.v+200; RETURN NEW; END$$")
					run(c, fixture.Exec("BEGIN", 0))
					s := triggerRows(q, []any{int64(1), int64(110)})
					s.Args = []any{1, 10}
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
					ddl := "CREATE OR REPLACE TRIGGER body INSTEAD OF INSERT OR UPDATE OR DELETE ON io_view FOR EACH ROW EXECUTE FUNCTION io_replacement()"
					if drop {
						ddl = "DROP TRIGGER body ON io_view"
					}
					oid := int64(0)
					if b.PG {
						oid = fixture.LockOID(t, p.Ctx, "io_view")
					}
					mode := "ShareRowExclusiveLock"
					if drop {
						mode = "AccessExclusiveLock"
					}
					join := fixture.DefinitionLockOperation(t, p.Ctx, p, b, peer, c, ddl, "relation", mode, 0, oid)
					end := "ROLLBACK"
					if commit {
						end = "COMMIT"
					}
					run(c, fixture.Exec(end, 0))
					out := join()
					if out.Err != nil {
						t.Fatal(out.Err)
					}
					if out.Rows != nil {
						out.Rows.Close()
					}
					if out.Result == nil {
						t.Fatal("missing DDL completion")
					}
					n, err := out.Result.RowsAffected()
					if err != nil || n != 0 {
						t.Fatal(n, err)
					}
					v := int64(220)
					base := int64(22)
					if drop {
						v = 20
						base = 20
					}
					s = triggerRows(q, []any{int64(2), v})
					s.Args = []any{2, 20}
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
					rows := [][]any{}
					if commit {
						rows = append(rows, []any{int64(1), int64(11)})
					}
					rows = append(rows, []any{int64(2), base})
					run(c, triggerRows("SELECT id,v FROM io_base ORDER BY id", rows...))
					logs := [][]any{}
					if commit {
						logs = append(logs, []any{"INSERT", int64(1), nil, int64(10)})
					}
					if !drop {
						logs = append(logs, []any{"replacement", int64(2), nil, int64(20)})
					}
					run(c, insteadLog("SELECT op,id,ov,nv FROM io_log", logs...))
				})
			})
		}
	}
	t.Run("nested-row-cancel", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			insteadSetup(t, p, b, c)
			holder, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Close()
			defer holder.ExecContext(context.Background(), "ROLLBACK")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO io_base VALUES(1,10)", 1))
			fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
			fixture.LockExec(t, p.Ctx, holder, "UPDATE io_base SET v=20 WHERE id=1", 1)
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			pid := 0
			if b.PG {
				pid = fixture.LockPID(t, p.Ctx, c)
			}
			join := fixture.DefinitionLockOperation(t, ctx, p, b, c, holder, "UPDATE io_view SET v=30 WHERE id=1", "transactionid", "ShareLock", 0, 0)
			if b.PG {
				var ok bool
				if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&ok); err != nil || !ok {
					t.Fatal(ok, err)
				}
			} else {
				cancel()
			}
			out := join()
			if out.Result != nil || out.Rows != nil {
				if out.Rows != nil {
					out.Rows.Close()
				}
				t.Fatal("canceled mutation returned success")
			}
			if b.PG {
				d := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
				got := fixture.ExtractDiagnostic(t, out.Err)
				pattern := regexp.MustCompile(`^while updating tuple \([0-9]+,[1-9][0-9]*\) in relation "io_base"\nSQL statement "UPDATE io_base SET id=NEW.id,v=NEW.v\+1 WHERE id=OLD.id"\nPL/pgSQL function io_body\(\) line 3 at SQL statement$`)
				if !pattern.MatchString(got.Context) {
					t.Fatalf("predicted nested cancellation context: %q", got.Context)
				}
				d.Context = got.Context
				fixture.AssertDiagnostic(t, out.Err, &d)
			} else {
				fixture.LexicalOrdinary(t, out.Err, context.Canceled)
			}
			fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, triggerRows("SELECT id,v FROM io_base", []any{int64(1), int64(10)}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, insteadLog("SELECT op,id,ov,nv FROM io_log"))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE io_view SET v=40 WHERE id=1", 1))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, triggerRows("SELECT id,v FROM io_base", []any{int64(1), int64(41)}))
		})
	})
	t.Run("independent-frames", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			insteadSetup(t, p, b, c)
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			start := make(chan struct{})
			work := func(conn *sql.Conn, id, v int) func() fixture.RelationOutcome {
				return fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					select {
					case <-start:
					case <-ctx.Done():
						return fixture.RelationOutcome{Err: ctx.Err()}
					}
					rows, err := conn.QueryContext(ctx, "INSERT INTO io_view VALUES($1,$2) RETURNING v", id, v)
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
			}
			left, right := work(c, 1, 10), work(peer, 2, 20)
			close(start)
			fixture.LimitLockValues(t, left(), b.PG, 110)
			fixture.LimitLockValues(t, right(), b.PG, 120)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, triggerRows("SELECT id,v FROM io_base ORDER BY id", []any{int64(1), int64(11)}, []any{int64(2), int64(21)}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, insteadLog("SELECT op,id,ov,nv FROM io_log", []any{"INSERT", int64(1), nil, int64(10)}, []any{"INSERT", int64(2), nil, int64(20)}))
		})
	})
}
