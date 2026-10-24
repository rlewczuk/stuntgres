package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLSelectIntoLifecycle(t *testing.T) {
	t.Run("held_and_replay", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE si_saved_input(id integer)",
				"CREATE FUNCTION si_saved(n integer, exact boolean) RETURNS text LANGUAGE plpgsql AS $$DECLARE r record; BEGIN IF exact THEN SELECT id AS value INTO STRICT r FROM si_saved_input WHERE id<=n ORDER BY id DESC; ELSE SELECT id AS value INTO r FROM si_saved_input WHERE id<=n ORDER BY id DESC; END IF; RETURN coalesce(r.value::text,'NULL')||':'||FOUND; END$$",
				"CREATE FUNCTION si_saved_outer(n integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE x integer; nested text; BEGIN SELECT 1 INTO x; nested:=si_saved(n,false); RETURN nested||':'||FOUND; END$$")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO si_saved_input VALUES(1),(2),(3)", 3))
			q := "SELECT si_saved($1::integer,$2::boolean) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			// Preparation neither executes the query nor changes its source.
			plpgsqlValue(t, p, b, c, "SELECT sum(id) AS v FROM si_saved_input", "INT8", int64(6))
			check := func() {
				for _, tc := range []struct {
					n     int
					exact bool
					want  string
				}{{0, false, "NULL:false"}, {3, false, "3:true"}, {1, true, "1:true"}, {0, false, "NULL:false"}} {
					s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.n, tc.exact)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				for _, tc := range []struct {
					n                   int
					code, message, hint string
				}{
					{0, "P0002", "query returned no rows", ""},
					{3, "P0003", "query returned more than one row", "Make sure the query returns a single row, or use LIMIT 1."},
				} {
					want := plpgsqlRuntimeDiagnostic(tc.code, tc.message, "si_saved(integer,boolean)", "SQL statement")
					want.Hint = tc.hint
					s := fixture.ErrorDiagnostic(q, want, tc.n, true)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"1:true"}}, 1, true))
				}
				plpgsqlValue(t, p, b, c, "SELECT si_saved_outer(0) AS v", "TEXT", "NULL:false:true")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO si_saved_input VALUES(4)", 1))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"4:true"}}, 4, false))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM si_saved_input WHERE id=4", 1))
			}
			check()
			for range 2 {
				if !b.PG {
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
				}
				check()
			}
		})
	})
	t.Run("dml_projection_precedence", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE si_projection_sink(id integer)",
				"CREATE FUNCTION si_projection_failure() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer:=99; BEGIN INSERT INTO si_projection_sink VALUES(1),(2) RETURNING 10/(2-id) INTO x; RETURN x; END$$")
			for _, prepared := range []bool{false, true} {
				step := fixture.Error("SELECT si_projection_failure()", "22012")
				step.SelectRows = true
				if prepared {
					step = fixture.Prepared(step)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM si_projection_sink", "INT8", int64(0))
			}
		})
	})
	t.Run("dml_no_third_proposal", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE si_proposal_sink(id integer CHECK(id<3))",
				"CREATE FUNCTION si_proposal_limit() RETURNS text LANGUAGE plpgsql AS $$DECLARE x integer:=99; BEGIN BEGIN INSERT INTO si_proposal_sink VALUES(1),(2),(3) RETURNING id INTO x; EXCEPTION WHEN too_many_rows THEN RETURN x||':'||FOUND; END; RETURN 'missed'; END$$")
			plpgsqlValue(t, p, b, c, "SELECT si_proposal_limit() AS v", "TEXT", "99:true")
			plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM si_proposal_sink", "INT8", int64(0))
		})
	})
	t.Run("nested_command_capture_isolation", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE si_nested_log(id integer)", "CREATE TABLE si_nested_sink(id integer)",
				"CREATE FUNCTION si_nested_write() RETURNS integer LANGUAGE sql AS $$INSERT INTO si_nested_log VALUES(1),(2),(3) RETURNING id; SELECT 7$$",
				"CREATE FUNCTION si_nested_capture() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN INSERT INTO si_nested_sink VALUES(1) RETURNING si_nested_write() INTO x; RETURN x; END$$")
			for _, prepared := range []bool{false, true} {
				step := fixture.Select("SELECT si_nested_capture() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
				if prepared {
					step = fixture.Prepared(step)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM si_nested_log", "INT8", int64(3))
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM si_nested_sink", "INT8", int64(1))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM si_nested_log", 3))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM si_nested_sink", 1))
			}
		})
	})
	for _, strict := range []bool{false, true} {
		t.Run(fmt.Sprintf("projection_demand_strict_%t", strict), func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				clause := ""
				if strict {
					clause = "STRICT "
				}
				// generate_series supplies 1 then 2 without an eager sort of the
				// failing projection; the ignored row is genuinely demand-driven.
				userOperatorExec(t, p, b, c,
					"CREATE FUNCTION si_demand_run() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN SELECT 10/(2-id) INTO "+clause+"x FROM generate_series(1,2) AS g(id); RETURN x; END$$")
				if strict {
					// Pinned PostgreSQL 18.6 evaluates the second projection before
					// testing strict cardinality and reports its SQL statement.
					want := plpgsqlRuntimeDiagnostic("22012", "division by zero", "si_demand_run()", "SQL statement")
					want.Context = "SQL statement \"SELECT 10/(2-id)               FROM generate_series(1,2) AS g(id)\"\n" + want.Context
					plpgsqlError(t, p, c, "SELECT si_demand_run() AS v", true, want)
				} else {
					plpgsqlValue(t, p, b, c, "SELECT si_demand_run() AS v", "INT4", int64(10))
				}
			})
		})
	}
	for _, kind := range []string{"select", "strict_select", "dml"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("sequence_demand_%s/prepared_%t", kind, prepared), func(t *testing.T) {
				plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					query := "SELECT nextval('si_demand_seq') INTO x FROM si_demand_source ORDER BY id"
					want, draws := "ok:1:true", int64(1)
					if kind == "strict_select" {
						query = "SELECT nextval('si_demand_seq') INTO STRICT x FROM si_demand_source ORDER BY id"
						want, draws = "many:99:true", 2
					}
					// PostgreSQL detects excess rows after the second RETURNING
					// projection; protected rollback does not rewind those draws.
					if kind == "dml" {
						query = "INSERT INTO si_demand_sink SELECT id FROM si_demand_source RETURNING nextval('si_demand_seq') INTO x"
						want, draws = "many:99:true", 2
					}
					userOperatorExec(t, p, b, c, "CREATE SEQUENCE si_demand_seq", "CREATE TABLE si_demand_source(id integer)", "CREATE TABLE si_demand_sink(id integer)",
						"CREATE FUNCTION si_sequence_run() RETURNS text LANGUAGE plpgsql AS $$DECLARE x bigint:=99; status text:='ok'; BEGIN BEGIN "+query+"; EXCEPTION WHEN too_many_rows THEN status:='many'; END; RETURN status||':'||x||':'||FOUND; END$$")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO si_demand_source VALUES(1),(2),(3)", 3))
					q := "SELECT si_sequence_run() AS v"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					plpgsqlValue(t, p, b, c, "SELECT is_called AS v FROM si_demand_seq", "BOOL", false)
					executor := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{want}})
					if prepared {
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
					} else {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
					plpgsqlValue(t, p, b, c, "SELECT last_value AS v FROM si_demand_seq", "INT8", draws)
					plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM si_demand_sink", "INT8", int64(0))
				})
			})
		}
	}
}
