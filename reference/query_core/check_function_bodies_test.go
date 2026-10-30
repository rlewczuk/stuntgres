package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func checkBodiesMismatch(name string) string {
	return "CREATE FUNCTION " + name + "() RETURNS integer LANGUAGE SQL AS $$SELECT 'bad'::text$$"
}

func checkBodiesReturnDiagnostic(name string, runtime bool) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("42P13", "return type mismatch in function declared to return integer")
	d.Detail = "Actual return type is text."
	d.Context = "SQL function \"" + name + "\""
	if runtime {
		d.Context += " during inlining"
	}
	return d
}

// Explicit expectations constrain every portable diagnostic field, including
// absence. Fresh prepared errors must occur at execution, not at Prepare.
func checkBodiesFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, want fixture.Diagnostic, query bool) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, want)
	s.SelectRows, s.ErrorStage = query, "execute"
	t.Run("direct diagnostic", func(t *testing.T) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	})
	t.Run("prepared diagnostic", func(t *testing.T) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	})
}

func checkBodiesCount(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string, count int64) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(
		"SELECT count(*) AS n FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname=$1", []string{"n"}, []string{"INT8"}, [][]any{{count}}, name))
}

func checkBodiesReads(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, value string) {
	t.Helper()
	for _, q := range []string{"SHOW check_function_bodies", "SELECT current_setting('check_function_bodies') AS check_function_bodies"} {
		s := fixture.Select(q, []string{"check_function_bodies"}, []string{"TEXT"}, [][]any{{value}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	}
}

func TestReferenceCheckFunctionBodiesEffect(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		checkBodiesReads(t, p, b, c, "on")
		q := checkBodiesMismatch("cfb_mismatch")
		checkBodiesFailure(t, p, b, c, q, checkBodiesReturnDiagnostic("cfb_mismatch", false), false)
		checkBodiesCount(t, p, b, c, "cfb_mismatch", 0)
		routineExec(t, p, b, c, "SET check_function_bodies=off", q)
		checkBodiesReads(t, p, b, c, "off")
		metadata := fixture.Select("SELECT name,setting,vartype,context,boot_val,reset_val,source,pending_restart FROM pg_settings WHERE name='check_function_bodies'",
			[]string{"name", "setting", "vartype", "context", "boot_val", "reset_val", "source", "pending_restart"},
			[]string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL"},
			[][]any{{"check_function_bodies", "off", "bool", "user", "on", "on", "session", false}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, metadata)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(metadata))
		stored := fixture.Select("SELECT prosrc,prorettype::regtype::text,pronargs FROM pg_proc WHERE oid='cfb_mismatch()'::regprocedure",
			[]string{"prosrc", "prorettype", "pronargs"}, []string{"TEXT", "TEXT", "INT2"}, [][]any{{"SELECT 'bad'::text", "integer", int64(0)}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, stored)
		checkBodiesFailure(t, p, b, c, "SELECT cfb_mismatch()", checkBodiesReturnDiagnostic("cfb_mismatch", true), true)
		routineExec(t, p, b, c, "SET check_function_bodies=on")
		checkBodiesReads(t, p, b, c, "on")
		checkBodiesCount(t, p, b, c, "cfb_mismatch", 1)
		checkBodiesFailure(t, p, b, c, "SELECT cfb_mismatch()", checkBodiesReturnDiagnostic("cfb_mismatch", true), true)
		routineExec(t, p, b, c, "CREATE FUNCTION cfb_predecessor() RETURNS integer LANGUAGE SQL AS $$SELECT 17$$")
		replace := strings.Replace(checkBodiesMismatch("cfb_predecessor"), "CREATE FUNCTION", "CREATE OR REPLACE FUNCTION", 1)
		checkBodiesFailure(t, p, b, c, replace, checkBodiesReturnDiagnostic("cfb_predecessor", false), false)
		routineValue(t, p, b, c, "SELECT cfb_predecessor() AS v", "v", "INT4", int64(17))
	})
}

// Scalar inlining rejects non-simple query structure before checking the
// terminal return type. Parsing/name-analysis attempts remain a separate phase.
func TestReferenceCheckFunctionBodiesInitializationPhases(t *testing.T) {
	for _, tc := range []struct{ name, body, detail string }{
		{"limit", "SELECT 'bad'::text LIMIT 1", "Actual return type is text."},
		{"offset", "SELECT 'bad'::text OFFSET 0", "Actual return type is text."},
		{"where", "SELECT 'bad'::text WHERE true", "Actual return type is text."},
		{"order", "SELECT 'bad'::text ORDER BY 1", "Actual return type is text."},
		{"distinct", "SELECT DISTINCT 'bad'::text", "Actual return type is text."},
		{"multiple_targets", "SELECT 'bad'::text,'extra'::text", "Final statement must return exactly one column."},
		{"subquery", "SELECT (SELECT 'bad'::text)", "Actual return type is text."},
		{"cte", "WITH unused AS (SELECT 1) SELECT 'bad'::text", "Actual return type is text."},
		{"group", "SELECT 'bad'::text GROUP BY 1", "Actual return type is text."},
		{"having", "SELECT 'bad'::text HAVING true", "Actual return type is text."},
		{"aggregate", "SELECT min('bad'::text)", "Actual return type is text."},
		{"window", "SELECT first_value('bad'::text) OVER ()", "Actual return type is text."},
		{"set", "SELECT 'bad'::text UNION ALL SELECT 'bad'::text", "Actual return type is text."},
		{"values", "VALUES ('bad'::text)", "Actual return type is text."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				name := "cfb_phase_" + tc.name
				create := "CREATE FUNCTION " + name + "() RETURNS integer LANGUAGE SQL AS $$" + tc.body + "$$"
				routineExec(t, p, b, c, "SET check_function_bodies=off", create)
				checkBodiesCount(t, p, b, c, name, 1)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(
					"SELECT prosrc FROM pg_proc WHERE oid='"+name+"()'::regprocedure",
					[]string{"prosrc"}, []string{"TEXT"}, [][]any{{tc.body}}))
				want := checkBodiesReturnDiagnostic(name, false)
				want.Detail, want.Context = tc.detail, `SQL function "`+name+`" statement 1`
				checkBodiesFailure(t, p, b, c, "SELECT "+name+"()", want, true)
			})
		})
	}
	t.Run("name_analysis_during_inlining", func(t *testing.T) {
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			const body = "SELECT cfb_phase_absent() LIMIT 1"
			routineExec(t, p, b, c, "SET check_function_bodies=off",
				"CREATE FUNCTION cfb_phase_name() RETURNS integer LANGUAGE SQL AS $$"+body+"$$")
			want := fixture.ControlsReferenceDiagnostic("42883", "function cfb_phase_absent() does not exist")
			want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			want.InternalPosition, want.InternalQuery = "8", body
			want.Context = `SQL function "cfb_phase_name" during inlining`
			checkBodiesFailure(t, p, b, c, "SELECT cfb_phase_name()", want, true)
		})
	})
	t.Run("parsing_during_inlining", func(t *testing.T) {
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			const body = "SELECT 'bad'::text LIMIT"
			routineExec(t, p, b, c, "SET check_function_bodies=off",
				"CREATE FUNCTION cfb_phase_parse() RETURNS integer LANGUAGE SQL AS $$"+body+"$$")
			want := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
			want.InternalPosition, want.InternalQuery = strconv.Itoa(len(body)+1), body
			want.Context = `SQL function "cfb_phase_parse" during inlining`
			checkBodiesFailure(t, p, b, c, "SELECT cfb_phase_parse()", want, true)
		})
	})
}

func TestReferenceCheckFunctionBodiesPrepared(t *testing.T) {
	for _, tc := range []struct{ name, prepareValue, executeValue string }{
		{"deferred", "on", "off"}, {"checked", "off", "on"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				routineExec(t, p, b, c, "SET check_function_bodies="+tc.prepareValue)
				q := checkBodiesMismatch("cfb_prepared")
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("CREATE Prepare", err)
				}
				defer stmt.Close()
				checkBodiesCount(t, p, b, c, "cfb_prepared", 0)
				setQuery := "SET check_function_bodies=" + tc.executeValue
				set, err := c.PrepareContext(p.Ctx, setQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer set.Close()
				checkBodiesReads(t, p, b, c, tc.prepareValue)
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: set, SQL: setQuery}, b.Recorder, fixture.Exec(setQuery, 0))
				checkBodiesReads(t, p, b, c, tc.executeValue)
				s := fixture.Exec(q, 0)
				if tc.executeValue == "on" {
					s = fixture.ErrorDiagnostic(q, checkBodiesReturnDiagnostic("cfb_prepared", false))
					s.ErrorStage = "execute"
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
				count := int64(1)
				if tc.executeValue == "on" {
					count = 0
				}
				checkBodiesCount(t, p, b, c, "cfb_prepared", count)
			})
		})
	}
}

func TestReferenceCheckFunctionBodiesStandard(t *testing.T) {
	for _, value := range []string{"on", "off"} {
		for _, form := range []string{"return", "atomic"} {
			t.Run(value+"_"+form, func(t *testing.T) {
				routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					routineExec(t, p, b, c, "SET check_function_bodies="+value)
					body := "RETURN (SELECT x FROM cfb_standard_missing)"
					if form == "atomic" {
						body = "BEGIN ATOMIC SELECT x FROM cfb_standard_missing; END"
					}
					q := "CREATE FUNCTION cfb_standard() RETURNS integer LANGUAGE SQL " + body
					d := fixture.ControlsReferenceDiagnostic("42P01", `relation "cfb_standard_missing" does not exist`)
					d.Position = strconv.Itoa(strings.Index(q, "cfb_standard_missing") + 1)
					checkBodiesFailure(t, p, b, c, q, d, false)
					checkBodiesCount(t, p, b, c, "cfb_standard", 0)
					routineExec(t, p, b, c, "CREATE TABLE cfb_standard_missing(x integer)")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO cfb_standard_missing VALUES (9)", 1))
					routineExec(t, p, b, c, q, "ALTER TABLE cfb_standard_missing RENAME TO cfb_standard_renamed")
					routineValue(t, p, b, c, "SELECT cfb_standard() AS v", "v", "INT4", int64(9))
				})
			})
		}
	}
	for _, form := range []string{"return", "atomic"} {
		t.Run("return_mismatch_"+form, func(t *testing.T) {
			routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				body := "RETURN 'bad'::text"
				if form == "atomic" {
					body = "BEGIN ATOMIC SELECT 'bad'::text; END"
				}
				q := "CREATE FUNCTION cfb_standard_mismatch() RETURNS integer LANGUAGE SQL " + body
				checkBodiesFailure(t, p, b, c, q, checkBodiesReturnDiagnostic("cfb_standard_mismatch", false), false)
				checkBodiesCount(t, p, b, c, "cfb_standard_mismatch", 0)
				routineExec(t, p, b, c, "SET check_function_bodies=off", q)
				checkBodiesCount(t, p, b, c, "cfb_standard_mismatch", 1)
				checkBodiesFailure(t, p, b, c, "SELECT cfb_standard_mismatch()", checkBodiesReturnDiagnostic("cfb_standard_mismatch", true), true)
			})
		})
	}
}
