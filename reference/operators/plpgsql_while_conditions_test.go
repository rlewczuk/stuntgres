package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLWhileConditions(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int64
	}{
		{"null_transition", "DECLARE n integer:=0; keep boolean:=true; BEGIN WHILE keep LOOP n:=n+1; IF n=2 THEN keep:=NULL; END IF; END LOOP; RETURN n; END", 2},
		{"false_transition", "DECLARE n integer:=0; keep boolean:=true; BEGIN WHILE keep LOOP n:=n+1; IF n=2 THEN keep:=false; END IF; END LOOP; RETURN n; END", 2},
		{"text_yes", "DECLARE n integer:=0; BEGIN WHILE 'yes'::text LOOP n:=n+1; EXIT; END LOOP; RETURN n; END", 1},
		{"numeric_zero", "DECLARE n integer:=0; BEGIN WHILE 0 LOOP n:=n+1; EXIT; END LOOP; RETURN n; END", 0},
		{"numeric_one", "DECLARE n integer:=0; BEGIN WHILE 1 LOOP n:=n+1; EXIT; END LOOP; RETURN n; END", 1},
		{"projection_alias", "DECLARE n integer:=0; BEGIN WHILE n<3 admitted_alias LOOP n:=n+1; END LOOP; RETURN n; END", 3},
	} {
		t.Run(tc.name, func(t *testing.T) { whileBody(t, tc.body, tc.want) })
	}
	for _, route := range []string{"direct", "prepared", "held", "exit", "return", "continue"} {
		t.Run("volatile_"+route, func(t *testing.T) {
			whileScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				body := "n:=n+1;"
				visits, calls := int64(3), int64(4)
				if route == "exit" {
					body = "n:=n+1; EXIT;"
					visits, calls = 1, 1
				}
				if route == "return" {
					body = "RETURN 1;"
					visits, calls = 1, 1
				}
				if route == "continue" {
					body = "n:=n+1; CONTINUE; n:=while_missing();"
				}
				userOperatorExec(t, p, b, c,
					"CREATE SEQUENCE while_sequence",
					"CREATE FUNCTION while_volatile() RETURNS integer LANGUAGE plpgsql AS $$DECLARE n integer:=0; BEGIN WHILE nextval('while_sequence')<=3 LOOP "+body+" END LOOP; RETURN n; END$$")
				q := "SELECT while_volatile() AS v"
				var stmt *sql.Stmt
				if route == "prepared" || route == "held" {
					var err error
					stmt, err = c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				plpgsqlValue(t, p, b, c, "SELECT CASE WHEN is_called THEN 99 ELSE 0 END AS v FROM while_sequence", "INT4", int64(0))
				step := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{visits}})
				if route == "prepared" {
					step = fixture.Prepared(step)
				}
				if route == "held" {
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, step)
				} else {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				}
				plpgsqlValue(t, p, b, c, "SELECT last_value AS v FROM while_sequence", "INT8", calls)
			})
		})
	}
	whileScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE FUNCTION while_limit(x integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE n integer:=0; BEGIN WHILE n<x LOOP n:=n+1; END LOOP; RETURN n; END$$")
		parametersHeld(t, p, b, c, "SELECT while_limit($1) AS v", "INT4", [][]any{{0}, {1}, {4}, {0}}, []any{int64(0), int64(1), int64(4), int64(0)})
	})
	t.Run("reached_binding", func(t *testing.T) {
		whileScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE FUNCTION while_lazy(x boolean) RETURNS integer LANGUAGE plpgsql AS $$BEGIN IF x THEN WHILE while_missing() LOOP EXIT; END LOOP; END IF; RETURN 7; END$$")
			q := "SELECT while_lazy($1) AS v"
			parametersValue(t, p, b, c, q, "v", "INT4", int64(7), false)
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			want := plpgsqlRuntimeDiagnostic("42883", "function while_missing() does not exist", "while_lazy(boolean)", "WHILE")
			want.Hint, want.InternalPosition, want.InternalQuery = parametersNoMatchHint, "1", "while_missing()"
			for _, input := range []bool{false, true, false} {
				if !input {
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}, input))
					continue
				}
				rows, err := stmt.QueryContext(p.Ctx, input)
				if rows != nil {
					rows.Close()
					t.Fatal("missing condition function exposed rows")
				}
				if err == nil {
					t.Fatal("missing condition function succeeded")
				}
				fixture.AssertDiagnostic(t, err, &want)
			}
			plpgsqlError(t, p, c, "SELECT while_lazy(true) AS v", true, want)
		})
	})
	t.Run("selected_cast", func(t *testing.T) {
		whileScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TYPE while_bool_source AS ENUM ('yes')",
				"CREATE FUNCTION while_bool_fail(while_bool_source) RETURNS boolean LANGUAGE SQL AS 'SELECT 1/0=0'",
				"CREATE CAST (while_bool_source AS boolean) WITH FUNCTION while_bool_fail(while_bool_source) AS ASSIGNMENT",
				"CREATE FUNCTION while_cast() RETURNS integer LANGUAGE plpgsql AS $$BEGIN WHILE 'yes'::while_bool_source LOOP EXIT; END LOOP; RETURN 7; END$$")
			want := plpgsqlRuntimeDiagnostic("22012", "division by zero", "while_cast()", "WHILE")
			want.Context = "SQL function \"while_bool_fail\" statement 1\n" + want.Context
			plpgsqlError(t, p, c, "SELECT while_cast() AS v", true, want)
			plpgsqlValue(t, p, b, c, "SELECT 'yes'::while_bool_source::text AS v", "TEXT", "yes")
		})
	})
}
