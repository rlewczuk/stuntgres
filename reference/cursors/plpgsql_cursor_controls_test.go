package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCursorControls(t *testing.T) {
	t.Run("definition_replay", func(t *testing.T) {
		sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			plCursorRows(t, p, b, c)
			plCursorHelpers(t, p, b, c)
			plCursorFunction(t, p, b, c, "pc_bound", "a integer", "integer", "DECLARE c CURSOR(x integer) FOR SELECT i FROM pc_rows WHERE i=x; r record; BEGIN OPEN c(a); FETCH c INTO r; CLOSE c; RETURN r.i; END")
			plCursorFunction(t, p, b, c, "pc_dynamic", "a integer", "integer", "DECLARE c refcursor; x integer; BEGIN OPEN c FOR EXECUTE 'SELECT $1::integer+$2::integer' USING a,8; FETCH c INTO x; CLOSE c; RETURN x; END")
			plCursorFunction(t, p, b, c, "pc_loop", "", "integer", "DECLARE c CURSOR FOR SELECT i FROM pc_rows ORDER BY i; n integer:=0; BEGIN FOR r IN c LOOP n:=n+r.i; END LOOP; RETURN n; END")
			plCursorFunction(t, p, b, c, "pc_collation", "", "text", "DECLARE x text COLLATE pg_catalog.pg_unicode_fast:='Straße'; c CURSOR FOR SELECT upper(x) AS t; r record; BEGIN OPEN c; FETCH c INTO r; CLOSE c; RETURN r.t; END")
			check := func(owner *sql.Conn) {
				plCursorValue(t, p, b, owner, "SELECT pc_bound($1::integer) AS v", "INT4", int64(20), 20)
				plCursorValue(t, p, b, owner, "SELECT pc_dynamic($1::integer) AS v", "INT4", int64(15), 7)
				plCursorValue(t, p, b, owner, "SELECT pc_loop() AS v", "INT4", int64(60))
				plCursorValue(t, p, b, owner, "SELECT pc_collation() AS v", "TEXT", "STRASSE")
				plCursorValue(t, p, b, owner, "SELECT pc_open('pc_replayed'::refcursor) AS v", "REFCURSOR", "pc_replayed")
			}
			check(c)
			if b.PG {
				return
			}
			q := "SELECT pc_bound($1::integer) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			var original bytes.Buffer
			if err = stuntgres.Dump(p.Ctx, p.Native, &original); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(original.Bytes())); err != nil {
					t.Fatal(err)
				}
				fresh, e := b.DB.Conn(p.Ctx)
				if e != nil {
					t.Fatal(e)
				}
				check(fresh)
				fresh.Close()
				sqlCursorStep(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(30)}}, 30))
				var after bytes.Buffer
				if err = stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(original.Bytes(), after.Bytes()) {
					t.Fatal("definition replay mutated dump source")
				}
			}
			// A portal is execution state, never part of the definition graph.
			sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
			sqlCursorStep(t, p, b, c, fixture.Select("SELECT pc_open('pc_not_serialized'::refcursor) AS v", []string{"v"}, []string{"REFCURSOR"}, [][]any{{"pc_not_serialized"}}))
			var dump bytes.Buffer
			if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			_, err = c.ExecContext(p.Ctx, "SELECT pc_take('pc_not_serialized'::refcursor)")
			fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
			sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK", 0))
			plCursorError(t, p, b, c, "SELECT pc_take('pc_not_serialized'::refcursor) AS v", plCursorMissing("pc_not_serialized"), true, false)
		})
	})
	t.Run("found_invocation_isolation", func(t *testing.T) {
		sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			plCursorFunction(t, p, b, c, "pc_inner", "", "boolean", "DECLARE c refcursor; x integer; BEGIN OPEN c FOR SELECT 7; FETCH c INTO x; CLOSE c; RETURN FOUND; END")
			plCursorFunction(t, p, b, c, "pc_outer", "", "boolean", "DECLARE x boolean; BEGIN x:=pc_inner(); RETURN x AND NOT FOUND; END")
			plCursorValue(t, p, b, c, "SELECT pc_outer() AS v", "BOOL", true)
		})
	})
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run("cancel_loop_"+name, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plCursorRows(t, p, b, c)
				if !b.PG {
					plCursorFunction(t, p, b, c, "pc_cancel_loop", "", "integer", "DECLARE c CURSOR FOR SELECT i FROM pc_rows ORDER BY i; n integer:=0; BEGIN c:='pc_cancel_owned'; FOR r IN c LOOP n:=n+r.i; END LOOP; RETURN n; END")
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					if prepared {
						stmt, err := c.PrepareContext(ctx, "SELECT pc_cancel_loop() AS v")
						if stmt != nil {
							stmt.Close()
							t.Fatal("canceled prepare exposed statement")
						}
						if !errors.Is(err, context.Canceled) {
							t.Fatal(err)
						}
					} else {
						rows, err := c.QueryContext(ctx, "SELECT pc_cancel_loop() AS v")
						if rows != nil {
							rows.Close()
							t.Fatal("canceled call exposed rows")
						}
						if !errors.Is(err, context.Canceled) {
							t.Fatal(err)
						}
					}
					plCursorValue(t, p, b, c, "SELECT pc_cancel_loop() AS v", "INT4", int64(60))
				} else {
					// Server admission/cancellation has no native pg_sleep analogue.
					plCursorFunction(t, p, b, c, "pc_cancel_loop", "", "integer", "DECLARE c CURSOR FOR SELECT i FROM pc_rows ORDER BY i; n integer:=0; BEGIN c:='pc_cancel_owned'; FOR r IN c LOOP n:=r.i+pg_sleep(600)::text::integer; END LOOP; RETURN n; END")
					plCursorCancelLoop(t, p, b, c, prepared)
				}
				plCursorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_cursors WHERE name='pc_cancel_owned'", "INT8", int64(0))
			})
		})
	}
}

func plCursorCancelLoop(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
	t.Helper()
	q := "SELECT pc_cancel_loop() AS v"
	var stmt *sql.Stmt
	var err error
	if prepared {
		stmt, err = c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
	}
	ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
	defer cancel()
	var pid int
	if err = c.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		var rows *sql.Rows
		var e error
		if stmt != nil {
			rows, e = stmt.QueryContext(ctx)
		} else {
			rows, e = c.QueryContext(ctx, q)
		}
		if rows != nil {
			rows.Close()
			if e == nil {
				e = errors.New("canceled loop exposed rows")
			}
		}
		done <- e
	}()
	for {
		var active bool
		if err = fixture.Admin.QueryRowContext(ctx, "SELECT COALESCE((SELECT state='active' AND query=$2 AND wait_event='PgSleep' FROM pg_stat_activity WHERE pid=$1),FALSE)", pid, q).Scan(&active); err != nil {
			cancel()
			<-done
			t.Fatal(err)
		}
		if active {
			break
		}
		select {
		case e := <-done:
			t.Fatal("loop completed before admission", e)
		default:
		}
	}
	var signaled bool
	if err = fixture.Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
		cancel()
		<-done
		t.Fatal("server cancel", signaled, err)
	}
	err = <-done
	got := fixture.ExtractDiagnostic(t, err)
	want := plCursorRuntime("57014", "canceling statement due to user request", "pc_cancel_loop()", "assignment")
	if got != want {
		t.Errorf("cancellation diagnostic got=%#v want=%#v", got, want)
	}
}
