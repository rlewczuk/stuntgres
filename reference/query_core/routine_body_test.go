package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineCoreBodyAndResults(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE TABLE routine_rows(x integer)",
			"CREATE SEQUENCE routine_seq",
			"CREATE FUNCTION routine_empty() RETURNS integer LANGUAGE SQL AS $$SELECT x FROM routine_rows WHERE false$$",
			"CREATE FUNCTION routine_first() RETURNS integer LANGUAGE SQL AS $$SELECT 10/x FROM (SELECT x FROM routine_rows ORDER BY x DESC) q$$",
			"CREATE FUNCTION routine_sort_target() RETURNS integer LANGUAGE SQL AS $$SELECT 10/x FROM routine_rows ORDER BY x DESC$$",
			"CREATE FUNCTION routine_null(integer) RETURNS integer LANGUAGE SQL AS $$SELECT coalesce($1,7)$$",
			"CREATE FUNCTION routine_assignment() RETURNS integer LANGUAGE SQL AS $$SELECT 5::bigint$$",
			"CREATE FUNCTION routine_float(integer) RETURNS double precision LANGUAGE SQL AS $$SELECT $1+1$$",
			"CREATE FUNCTION routine_divzero() RETURNS integer LANGUAGE SQL AS $$SELECT 1/0$$",
			"CREATE FUNCTION routine_next() RETURNS bigint LANGUAGE SQL AS $$SELECT nextval('routine_seq')$$",
			"CREATE FUNCTION routine_overflow() RETURNS integer LANGUAGE SQL AS $$SELECT 2147483648::bigint$$",
			"CREATE FUNCTION routine_bytes(bytea) RETURNS bytea LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION routine_cte(integer) RETURNS integer LANGUAGE SQL AS $$WITH a AS (SELECT $1 AS x) SELECT max(x) FROM a$$",
			"CREATE FUNCTION routine_join(integer) RETURNS integer LANGUAGE SQL AS $$SELECT a.x+b.x FROM routine_rows a JOIN routine_rows b ON a.x=b.x WHERE a.x=$1 ORDER BY a.x$$")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO routine_rows VALUES (0),(1)", 2))
		for _, tc := range []struct {
			q, name, typ string
			value        any
		}{
			{"SELECT routine_empty()", "routine_empty", "INT4", nil},
			{"SELECT routine_first()", "routine_first", "INT4", int64(10)},
			{"SELECT routine_null(NULL)", "routine_null", "INT4", int64(7)},
			{"SELECT routine_assignment()", "routine_assignment", "INT4", int64(5)},
			{"SELECT routine_float(4)", "routine_float", "FLOAT8", float64(5)},
			{"SELECT routine_cte(6)", "routine_cte", "INT4", int64(6)},
			{"SELECT routine_join(1)", "routine_join", "INT4", int64(2)},
		} {
			routineValue(t, p, b, c, tc.q, tc.name, tc.typ, tc.value)
		}
		// Validation must not advance the sequence. Test once, not both routes.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT routine_next() AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
		routineDiagnostic(t, p, c, "SELECT routine_divzero()", "22012", "division by zero", "", true)
		routineDiagnostic(t, p, c, "SELECT routine_overflow()", "22003", "integer out of range", "", true)
		routineDiagnostic(t, p, c, "SELECT routine_sort_target()", "22012", "division by zero", "", true)
		q := "SELECT routine_bytes($1) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		input := []byte{0, 1, 255}
		var held []byte
		if err := stmt.QueryRowContext(p.Ctx, input).Scan(&held); err != nil {
			t.Fatal(err)
		}
		input[1] = 9
		var later []byte
		if err := stmt.QueryRowContext(p.Ctx, []byte{2, 3}).Scan(&later); err != nil {
			t.Fatal(err)
		}
		if len(held) != 3 || held[0] != 0 || held[1] != 1 || held[2] != 255 {
			t.Fatalf("retained result mutated: %v", held)
		}
		held[0] = 8
		if len(later) != 2 || later[0] != 2 || later[1] != 3 {
			t.Fatalf("distinct invocation result mutated: %v", later)
		}
	})
	t.Run("statement effects", routineStatementEffects)
}

func TestReferenceRoutineCoreLifetime(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "routine_shadow")
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE SCHEMA routine_shadow",
			"CREATE TABLE routine_live(x integer)",
			"CREATE FUNCTION routine_read() RETURNS integer LANGUAGE SQL AS $$SELECT x FROM routine_live ORDER BY x$$",
			"SET search_path TO routine_shadow,public")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO routine_live VALUES (4)", 1))
		q := "SELECT routine_read() AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		value := func(want int64) {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder,
				fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{want}}))
		}
		value(4)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE public.routine_live SET x=9", 1))
		value(9)
		// An unchanged-path shadow does not displace retained body identity.
		routineExec(t, p, b, c, "CREATE TABLE routine_shadow.routine_live(x integer)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO routine_shadow.routine_live VALUES (77)", 1))
		value(9)
		// Relevant path change reanalyzes the whole SQL body.
		routineExec(t, p, b, c, "SET search_path TO public,routine_shadow")
		value(9)
		routineExec(t, p, b, c, "SET search_path TO routine_shadow,public")
		value(77)
		// Dependency destruction must not retain a detached table snapshot.
		routineExec(t, p, b, c, "DROP TABLE routine_shadow.routine_live")
		value(9)
		routineExec(t, p, b, c, "SET search_path TO public")
		// Failure leaves the same physical session usable.
		routineExec(t, p, b, c, "CREATE FUNCTION routine_fail() RETURNS integer LANGUAGE SQL AS $$SELECT 1/0$$")
		routineDiagnostic(t, p, c, "SELECT routine_fail()", "22012", "division by zero", "", true)
		value(9)
		routineExec(t, p, b, c, "CREATE FUNCTION routine_sleep(double precision) RETURNS text LANGUAGE SQL AS $$SELECT pg_sleep($1)::text$$")
		sleepSQL := "SELECT routine_sleep(30)"
		fixture.SleepActiveCancel(t, p, b, c, sleepSQL, nil, func(ctx context.Context) fixture.RelationOutcome {
			rows, err := c.QueryContext(ctx, sleepSQL)
			if err != nil {
				return fixture.RelationOutcome{Err: err}
			}
			for rows.Next() {
			}
			err = rows.Err()
			rows.Close()
			return fixture.RelationOutcome{Err: err}
		})
		value(9)
	})
}

func TestReferenceRoutineCoreFeatureSmoke(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE FUNCTION add1(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$",
			"CREATE TABLE routine_smoke_rows(x integer)",
			"CREATE FUNCTION routine_smoke_empty() RETURNS integer LANGUAGE SQL AS $$SELECT x FROM routine_smoke_rows WHERE false$$",
			"CREATE FUNCTION routine_smoke_first() RETURNS integer LANGUAGE SQL AS $$SELECT 10/x FROM (SELECT x FROM routine_smoke_rows ORDER BY x DESC) q$$",
			"CREATE FUNCTION routine_smoke_float(integer) RETURNS double precision LANGUAGE SQL AS $$SELECT $1+1$$")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO routine_smoke_rows VALUES (0),(1)", 2))
		routineValue(t, p, b, c, "SELECT add1(4)", "add1", "INT4", int64(5))
		q := "SELECT add1($1) AS answer"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, v := range []int64{4, 9} {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"answer"}, []string{"INT4"}, [][]any{{v + 1}}, v))
		}
		routineValue(t, p, b, c, "SELECT routine_smoke_empty()", "routine_smoke_empty", "INT4", nil)
		routineValue(t, p, b, c, "SELECT routine_smoke_first()", "routine_smoke_first", "INT4", int64(10))
		routineValue(t, p, b, c, "SELECT routine_smoke_float(4)", "routine_smoke_float", "FLOAT8", float64(5))
		routineDiagnostic(t, p, c, "SELECT routine_missing(1)", "42883", "function routine_missing(integer) does not exist", "", true)
		routineSmokeRoundtrip(t, p, b, c)
	})
}
