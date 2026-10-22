package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func datetimeSleepStoredExecution(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	// Assignment uses PostgreSQL's existing character CoerceViaIO fallback,
	// distinct from both explicit casts and implicit function resolution.
	run(fixture.Exec("CREATE TABLE sleep_assignment(id integer,v text)", 0))
	run(fixture.Exec("INSERT INTO sleep_assignment VALUES(1,pg_sleep(0)),(2,pg_sleep(NULL::float8))", 2))
	assignmentSQL := "INSERT INTO sleep_assignment VALUES($1,pg_sleep($2))"
	assignmentStmt, err := c.PrepareContext(p.Ctx, assignmentSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer assignmentStmt.Close()
	assignmentHeld := fixture.HeldStatement{Executor: c, Statement: assignmentStmt, SQL: assignmentSQL}
	fixture.RunStep(t, p.Ctx, assignmentHeld, b.Recorder, fixture.Exec(assignmentSQL, 1, 3, float64(0)))
	fixture.RunStep(t, p.Ctx, assignmentHeld, b.Recorder, fixture.Exec(assignmentSQL, 1, 4, nil))
	run(fixture.Exec("CREATE TABLE sleep_assignment_default(v text DEFAULT pg_sleep(0),n text DEFAULT pg_sleep(NULL::float8))", 0))
	run(fixture.Exec("INSERT INTO sleep_assignment_default DEFAULT VALUES", 1))
	for _, prepared := range []bool{false, true} {
		for _, expr := range []string{"pg_sleep(NULL)::text", "pg_sleep(NULL::float8)::text", "pg_sleep_for(NULL::interval)::text", "pg_sleep_until(NULL::timestamptz)::text"} {
			s := fixture.Select("SELECT "+expr+" AS v", []string{"v"}, []string{"TEXT"}, [][]any{{nil}})
			s.Prepared = prepared
			run(s)
		}
	}
	nullSQL := "SELECT pg_sleep($1)::text AS v"
	nullStmt, err := c.PrepareContext(p.Ctx, nullSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer nullStmt.Close()
	runNull := fixture.HeldStatement{Executor: c, Statement: nullStmt, SQL: nullSQL}
	fixture.RunStep(t, p.Ctx, runNull, b.Recorder, fixture.Select(nullSQL, []string{"v"}, []string{"TEXT"}, [][]any{{nil}}, nil))
	run(fixture.Exec("CREATE TABLE sleep_default_null(v text DEFAULT pg_sleep(NULL::float8)::text)", 0))
	run(fixture.Exec("INSERT INTO sleep_default_null DEFAULT VALUES", 1))
	run(fixture.Exec("CREATE VIEW sleep_null_text AS SELECT pg_sleep(NULL::float8)::text AS v", 0))
	run(fixture.Exec("INSERT INTO sleep_default_text DEFAULT VALUES", 1))
	run(fixture.Select("SELECT v FROM sleep_default_text", []string{"v"}, []string{"TEXT"}, [][]any{{""}}))
	run(fixture.Exec("INSERT INTO sleep_default_bool DEFAULT VALUES", 1))
	run(fixture.Select("SELECT v FROM sleep_default_bool", []string{"v"}, []string{"BOOL"}, [][]any{{false}}))
	run(fixture.Exec("INSERT INTO sleep_check VALUES(1)", 1))
	run(fixture.Select("SELECT v FROM sleep_check", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
	run(fixture.Exec("CREATE TABLE sleep_inputs(id integer,delay double precision)", 0))
	run(fixture.Exec("INSERT INTO sleep_inputs VALUES(1,0),(2,-0.1)", 2))
	for _, prepared := range []bool{false, true} {
		for _, s := range []fixture.Step{
			fixture.Select("SELECT count(pg_sleep(delay)) AS n FROM sleep_inputs", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}),
			fixture.Select("SELECT id,pg_sleep(delay)::text AS v FROM sleep_inputs GROUP BY id,delay ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), ""}, {int64(2), ""}}),
			fixture.Select("SELECT s.id,pg_sleep(s.delay)::text AS v FROM sleep_inputs s JOIN sleep_inputs p ON s.id=p.id ORDER BY s.id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), ""}, {int64(2), ""}}),
		} {
			s.Prepared = prepared
			run(s)
		}
	}
	run(fixture.Exec("CREATE VIEW sleep_rows AS SELECT id,pg_sleep(delay)::text AS v FROM sleep_inputs", 0))
	for _, q := range []string{
		"CREATE VIEW sleep_composed AS SELECT pg_sleep(0)::text AS v",
		"CREATE VIEW sleep_delayed AS SELECT pg_sleep(0.075) IS NULL AS v",
		"CREATE VIEW sleep_serialization AS SELECT pg_sleep(600) IS NULL AS v",
	} {
		run(fixture.Exec(q, 0))
	}
	checkDefinitions := func() {
		for _, oracle := range []struct{ view, definition string }{
			{"sleep_composed", " SELECT pg_sleep(0::double precision)::text AS v;"},
			{"sleep_delayed", " SELECT pg_sleep(0.075::double precision) IS NULL AS v;"},
			{"sleep_serialization", " SELECT pg_sleep(600::double precision) IS NULL AS v;"},
		} {
			t.Run(oracle.view+"-deparse-oracle", func(t *testing.T) {
				var definition string
				if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef($1::regclass,true)", oracle.view).Scan(&definition); err != nil {
					t.Fatal(err)
				}
				if definition != oracle.definition {
					t.Fatalf("view=%s definition=%q want=%q", oracle.view, definition, oracle.definition)
				}
			})
		}
	}
	checkDefinitions()
	checkState := func() {
		run(fixture.Select("SELECT id,v FROM sleep_assignment ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), ""}, {int64(2), nil}, {int64(3), ""}, {int64(4), nil}}))
		run(fixture.Select("SELECT v,n FROM sleep_assignment_default", []string{"v", "n"}, []string{"TEXT", "TEXT"}, [][]any{{"", nil}}))
		run(fixture.Select("SELECT v FROM sleep_null_text", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
		run(fixture.Select("SELECT v FROM sleep_default_null", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
		run(fixture.Select("SELECT v FROM sleep_composed", []string{"v"}, []string{"TEXT"}, [][]any{{""}}))
		run(fixture.Select("SELECT id,v FROM sleep_rows ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), ""}, {int64(2), ""}}))
		run(fixture.Select("SELECT v FROM sleep_default_text", []string{"v"}, []string{"TEXT"}, [][]any{{""}}))
		run(fixture.Select("SELECT v FROM sleep_default_bool", []string{"v"}, []string{"BOOL"}, [][]any{{false}}))
		run(fixture.Select("SELECT v FROM sleep_check", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(fixture.Select("SELECT id,delay FROM sleep_inputs ORDER BY id", []string{"id", "delay"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(0)}, {int64(2), -0.1}}))
	}
	check := func() {
		checkState()
		q := "SELECT v FROM sleep_delayed"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for range 2 {
			start := time.Now()
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{false}}))
			if time.Since(start) < 75*time.Millisecond {
				t.Fatal("stored sleep folded or returned early")
			}
		}
	}
	check()
	if !b.PG {
		datetimeSleepStoredControls(t, p, b, c, func() {
			checkState()
			checkDefinitions()
		})
		check()
		run(fixture.Exec("INSERT INTO sleep_default_null DEFAULT VALUES", 1))
		run(fixture.Select("SELECT v FROM sleep_default_null", []string{"v"}, []string{"TEXT"}, [][]any{{nil}, {nil}}))
		run(fixture.Exec("INSERT INTO sleep_assignment_default DEFAULT VALUES", 1))
		run(fixture.Select("SELECT v,n FROM sleep_assignment_default", []string{"v", "n"}, []string{"TEXT", "TEXT"}, [][]any{{"", nil}, {"", nil}}))
	}
}

func datetimeSleepVoidViewBoundary(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_views WHERE viewname='sleep_view'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	for _, prepared := range []bool{false, true} {
		route := "direct"
		if prepared {
			route = "prepared"
		}
		t.Run("void-view-publication/"+route, func(t *testing.T) {
			fresh, existing := "sleep_void_fresh_"+route, "sleep_void_existing_"+route
			fail := func(q, message string) {
				s := fixture.ErrorDiagnostic(q, datetimeSleepError("42P16", message))
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			// A long volatile operand must not execute during either rejected
			// definition; the existing SQL statement timeout bounds that mistake.
			fail("CREATE VIEW "+fresh+"(effective) AS SELECT pg_sleep(600) AS original", `column "effective" has pseudo-type void`)
			fail("CREATE OR REPLACE VIEW "+fresh+"(effective) AS SELECT pg_sleep(600) AS original", `column "effective" has pseudo-type void`)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_views WHERE viewname='"+fresh+"'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_class WHERE relname='"+fresh+"'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW "+existing+"(effective) AS SELECT 7 AS original", 0))
			fail("CREATE OR REPLACE VIEW "+existing+"(effective) AS SELECT pg_sleep(600) AS original", `cannot change data type of view column "effective" from integer to void`)
			// A valid existing prefix must not admit an appended VOID column.
			fail("CREATE OR REPLACE VIEW "+existing+"(effective,added) AS SELECT 7,pg_sleep(600)", `column "added" has pseudo-type void`)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT pg_get_viewdef($1::regclass,true) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{" SELECT 7 AS effective;"}}, existing))
			s := fixture.Select("SELECT effective FROM "+existing, []string{"effective"}, []string{"INT4"}, [][]any{{int64(7)}})
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		})
	}
}
