package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoutineSQLBodiesAtomicity(t *testing.T) {
	sqlBodiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE TABLE body_effect_log(v integer)",
			"CREATE TABLE body_zero(v integer)",
			"CREATE FUNCTION body_effect(x integer) RETURNS integer LANGUAGE SQL AS $$INSERT INTO body_effect_log VALUES(x); SELECT x$$",
			"CREATE FUNCTION body_late_error(x integer) RETURNS integer LANGUAGE SQL AS $$INSERT INTO body_effect_log VALUES(x); SELECT 10/x$$",
			"CREATE TABLE body_checked(v integer CHECK(v>0))",
			"CREATE FUNCTION body_checked_insert() RETURNS integer LANGUAGE SQL AS $$INSERT INTO body_checked VALUES(1),(0) RETURNING v$$")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO body_zero VALUES(0)", 1))
		d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
		d.Context = "SQL function \"body_late_error\" statement 2"
		sqlBodiesDiagnostic(t, p, c, "SELECT body_late_error(0)", true, d, false)
		d.Context = ""
		sqlBodiesDiagnostic(t, p, c, "SELECT body_effect(20),10/v FROM body_zero", true, d, false)
		routineValueWithNativeNullability(t, p, b, c, "SELECT count(*) AS v FROM body_effect_log", "v", "INT8", int64(0), false, true)
		for _, prepared := range []bool{false, true} {
			s := fixture.Error("SELECT body_checked_insert()", "23514")
			s.SelectRows, s.Prepared = true, prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		routineValueWithNativeNullability(t, p, b, c, "SELECT count(*) AS v FROM body_checked", "v", "INT8", int64(0), false, true)
		observer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer observer.Close()
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT body_effect(7) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT count(*) AS v FROM body_effect_log", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
		routineValueWithNativeNullability(t, p, b, observer, "SELECT count(*) AS v FROM body_effect_log", "v", "INT8", int64(0), false, true)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		routineValueWithNativeNullability(t, p, b, c, "SELECT count(*) AS v FROM body_effect_log", "v", "INT8", int64(0), false, true)
		routineExec(t, p, b, c, "CREATE FUNCTION body_cancel() RETURNS void LANGUAGE SQL AS $$INSERT INTO body_effect_log VALUES(30); SELECT pg_sleep(30)$$")
		q := "SELECT body_cancel()"
		fixture.SleepActiveCancelWithContext(t, p, b, c, q, "SQL function \"body_cancel\" statement 2", nil, func(ctx context.Context) fixture.RelationOutcome {
			rows, err := c.QueryContext(ctx, q)
			if err != nil {
				return fixture.RelationOutcome{Err: err}
			}
			for rows.Next() {
			}
			err = rows.Err()
			rows.Close()
			return fixture.RelationOutcome{Err: err}
		})
		routineValueWithNativeNullability(t, p, b, c, "SELECT count(*) AS v FROM body_effect_log", "v", "INT8", int64(0), false, true)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT body_effect(9) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
		routineValueWithNativeNullability(t, p, b, c, "SELECT count(*) AS v FROM body_effect_log", "v", "INT8", int64(1), false, true)
	})
}

func TestReferenceRoutineSQLBodiesPersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "body_saved")
	sqlBodiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE SCHEMA body_saved",
			"CREATE TABLE body_saved.source(v integer)",
			"CREATE FUNCTION body_saved.read_source() RETURNS integer BEGIN ATOMIC SELECT v FROM body_saved.source; END",
			"ALTER TABLE body_saved.source RENAME TO renamed",
			"CREATE FUNCTION body_saved.callee(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1+1000$$",
			"CREATE FUNCTION body_saved.selected() RETURNS bigint RETURN body_saved.callee(1)",
			"CREATE FUNCTION body_saved.callee(integer) RETURNS bigint LANGUAGE SQL AS $$SELECT $1+2000$$",
			"CREATE FUNCTION body_saved.named(IN delta integer) RETURNS integer LANGUAGE SQL AS $$SELECT delta+$1$$",
			"CREATE FUNCTION body_saved.empty_void() RETURNS void BEGIN ATOMIC END",
			"CREATE PROCEDURE body_saved.text_proc(IN x integer) LANGUAGE SQL AS $$INSERT INTO body_saved.renamed VALUES(x)$$",
			"CREATE PROCEDURE body_saved.standard_proc(x integer) BEGIN ATOMIC INSERT INTO body_saved.renamed VALUES(x); END")
		// Both named UPDATE parameters and the standard procedure's INSERT
		// formal must survive immutable recipe remapping on every replay.
		routineExec(t, p, b, c,
			"CREATE TABLE body_saved.counter(v integer)",
			"CREATE FUNCTION body_saved.write_counter(delta integer) RETURNS integer BEGIN ATOMIC UPDATE body_saved.counter SET v=delta; SELECT v FROM body_saved.counter; END")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO body_saved.counter VALUES(0)", 1))
		routineExec(t, p, b, c,
			"CREATE TABLE body_saved.merge_target(v integer)",
			"CREATE FUNCTION body_saved.merge_row(x integer) RETURNS integer BEGIN ATOMIC MERGE INTO body_saved.merge_target t USING (VALUES(x)) s(v) ON t.v=s.v WHEN MATCHED THEN UPDATE SET v=s.v WHEN NOT MATCHED THEN INSERT VALUES(s.v) RETURNING t.v; END")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO body_saved.renamed VALUES(17)", 1))
		check := func(conn *sql.Conn, backend fixture.Backend) {
			t.Helper()
			routineValue(t, p, backend, conn, "SELECT body_saved.read_source() AS v", "v", "INT4", int64(17))
			routineValue(t, p, backend, conn, "SELECT body_saved.selected() AS v", "v", "INT8", int64(1001))
			routineValue(t, p, backend, conn, "SELECT body_saved.callee(1) AS v", "v", "INT8", int64(2001))
			routineValue(t, p, backend, conn, "SELECT body_saved.named(4) AS v", "v", "INT4", int64(8))
			routineValue(t, p, backend, conn, "SELECT body_saved.merge_row(9) AS v", "v", "INT4", int64(9))
			routineValue(t, p, backend, conn, "SELECT body_saved.write_counter(21) AS v", "v", "INT4", int64(21))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT body_saved.empty_void() IS NULL AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT proname,prokind FROM pg_proc WHERE pronamespace='body_saved'::regnamespace AND prokind='p' ORDER BY proname", []string{"proname", "prokind"}, []string{"NAME", "CHAR"}, [][]any{{"standard_proc", "p"}, {"text_proc", "p"}}))
		}
		check(c, b)
		if b.PG {
			return
		}
		for range 2 {
			held, err := c.QueryContext(p.Ctx, "SELECT body_saved.read_source() AS v")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			var retained int64
			if !held.Next() {
				t.Fatal("retained standard-body row disappeared", held.Err())
			}
			if err := held.Scan(&retained); err != nil || retained != 17 {
				t.Fatal(retained, err)
			}
			if held.Next() || held.Err() != nil {
				t.Fatal("retained standard-body rows", held.Err())
			}
			held.Close()
			check(c, b)
			if p.Postgres != nil {
				pg, err := p.Postgres.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS body_saved CASCADE"); err != nil {
					pg.Close()
					t.Fatal(err)
				}
				if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
					pg.Close()
					t.Fatal("unchanged native stream replay", err)
				}
				check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
				if _, err := pg.ExecContext(p.Ctx, "SET check_function_bodies=true; DROP SCHEMA body_saved CASCADE"); err != nil {
					pg.Close()
					t.Fatal(err)
				}
				pg.Close()
			}
			assertUnchanged := func() {
				t.Helper()
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(after.Bytes(), dump.Bytes()) {
					t.Fatal("failed Restore changed live state")
				}
				check(c, b)
			}
			bad := "CREATE FUNCTION public.body_bad_restore() RETURNS integer BEGIN ATOMIC SELECT v FROM public.body_absent; END;"
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
				t.Fatal("invalid standard body published")
			}
			assertUnchanged()
			if err := stuntgres.Restore(p.Ctx, p.Native, io.MultiReader(bytes.NewReader(dump.Bytes()), routineReadFailure{})); err == nil {
				t.Fatal("read-failed Restore published")
			}
			assertUnchanged()
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err == nil {
				t.Fatal("canceled Restore published")
			}
			assertUnchanged()
		}
	})
}

func TestReferenceRoutineSQLBodiesFeatureSmoke(t *testing.T) {
	sqlBodiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE TABLE body_smoke(v integer)",
			"CREATE FUNCTION body_smoke_bump(delta integer) RETURNS integer LANGUAGE SQL AS $$UPDATE body_smoke SET v=v+delta; SELECT v FROM body_smoke$$",
			"CREATE FUNCTION body_smoke_return(x integer) RETURNS integer RETURN x+1",
			"CREATE FUNCTION body_smoke_void() RETURNS void BEGIN ATOMIC UPDATE body_smoke SET v=v+1; END")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO body_smoke VALUES(10)", 1))
		q := "SELECT body_smoke_bump($1) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, want := range []int64{15, 20} {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder,
				fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{want}}, 5))
		}
		routineValue(t, p, b, c, "SELECT body_smoke_return(4) AS v", "v", "INT4", int64(5))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT body_smoke_void() IS NULL AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
		routineValueWithNativeNullability(t, p, b, c, "SELECT v AS answer FROM body_smoke", "answer", "INT4", int64(21), true, true)
	})
}
