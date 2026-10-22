package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineDropIdentity(t *testing.T) {
	t.Run("overloads and retained statements", func(t *testing.T) {
		routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			routineExec(t, p, b, c,
				"CREATE FUNCTION rd_pick(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$",
				"CREATE FUNCTION rd_pick(text) RETURNS bigint LANGUAGE SQL AS $$SELECT 104::bigint$$")
			qi, qt := "SELECT rd_pick(4) AS v", "SELECT rd_pick('x'::text) AS v"
			i, err := c.PrepareContext(p.Ctx, qi)
			if err != nil {
				t.Fatal(err)
			}
			defer i.Close()
			s, err := c.PrepareContext(p.Ctx, qt)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			routineValue(t, p, b, c, qi, "v", "INT4", int64(5))
			routineValue(t, p, b, c, qt, "v", "INT8", int64(104))
			d := fixture.ControlsReferenceDiagnostic("42725", `function name "rd_pick" is not unique`)
			d.Hint = "Specify the argument list to select the function unambiguously."
			routineDropError(t, p, b, c, "DROP FUNCTION rd_pick", d)
			routineDropError(t, p, b, c, "DROP FUNCTION IF EXISTS rd_pick", d)
			drop, err := c.PrepareContext(p.Ctx, "DROP FUNCTION rd_pick(integer)")
			if err != nil {
				t.Fatal(err)
			}
			defer drop.Close()
			routineValue(t, p, b, c, qi, "v", "INT4", int64(5))
			routineDropNotices(t, notices)
			held, err := b.DB.QueryContext(p.Ctx, qi)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: drop, SQL: "DROP FUNCTION rd_pick(integer)"}, b.Recorder, fixture.Exec("DROP FUNCTION rd_pick(integer)", 0))
			var v int64
			if !held.Next() {
				t.Fatal("retained row", held.Err())
			}
			if err := held.Scan(&v); err != nil || v != 5 {
				t.Fatal(v, err)
			}
			if held.Next() || held.Err() != nil {
				t.Fatal("retained rows", held.Err())
			}
			held.Close()
			routineValue(t, p, b, c, qt, "v", "INT8", int64(104))
			routineDropHeld(t, p, b, c, s, qt, "INT8", int64(104))
			failedRows, err := i.QueryContext(p.Ctx)
			if failedRows != nil {
				failedRows.Close()
			}
			fixture.State(t, err, "42883")
			routineExec(t, p, b, c, "CREATE FUNCTION rd_pick(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+10$$")
			routineDropHeld(t, p, b, c, i, qi, "INT4", int64(14))
			routineExec(t, p, b, c, "DROP ROUTINE rd_pick(int)", "DROP FUNCTION rd_pick")
			routineDropNotices(t, notices)
		})
	})
	for _, target := range []string{"rd_form(IN integer)", "rd_form(IN ignored integer)", "rd_form(INOUT integer)", "rd_form(VARIADIC integer)", "rd_form(integer, OUT ignored rd_missing_schema.missing_type)"} {
		t.Run(target, func(t *testing.T) {
			routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
				routineExec(t, p, b, c, "CREATE FUNCTION rd_form(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$")
				routineDropError(t, p, b, c, "DROP FUNCTION rd_form()", fixture.ControlsReferenceDiagnostic("42883", "function rd_form() does not exist"))
				routineValue(t, p, b, c, "SELECT rd_form(7) AS v", "v", "INT4", int64(7))
				routineExec(t, p, b, c, "DROP FUNCTION "+target)
				routineDropNotices(t, notices)
			})
		})
	}
	t.Run("domain identity", func(t *testing.T) {
		routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			routineExec(t, p, b, c, "CREATE DOMAIN rd_domain AS integer", "CREATE FUNCTION rd_domain_pick(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 1$$", "CREATE FUNCTION rd_domain_pick(rd_domain) RETURNS integer LANGUAGE SQL AS $$SELECT 2$$", "DROP FUNCTION rd_domain_pick(rd_domain)")
			routineValue(t, p, b, c, "SELECT rd_domain_pick(4) AS v", "v", "INT4", int64(1))
			routineDropNotices(t, notices)
		})
	})
	t.Run("namespace masking and quoted names", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "rd_early", "rd_later", "Rd Quote")
		routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			routineExec(t, p, b, c, "CREATE SCHEMA rd_early", "CREATE SCHEMA rd_later", `CREATE SCHEMA "Rd Quote"`,
				"CREATE FUNCTION rd_early.path(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 1$$", "CREATE FUNCTION rd_later.path(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 2$$", "CREATE FUNCTION rd_later.path(text) RETURNS integer LANGUAGE SQL AS $$SELECT 3$$",
				`CREATE FUNCTION "Rd Quote"."Mixed Name"(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 9$$`, "SET search_path TO rd_early,rd_later,public")
			d := fixture.ControlsReferenceDiagnostic("42725", `function name "path" is not unique`)
			d.Hint = "Specify the argument list to select the function unambiguously."
			routineDropError(t, p, b, c, "DROP FUNCTION path", d)
			routineExec(t, p, b, c, "DROP FUNCTION path(integer)")
			routineValue(t, p, b, c, "SELECT path(4) AS v", "v", "INT4", int64(2))
			routineValue(t, p, b, c, "SELECT path('x'::text) AS v", "v", "INT4", int64(3))
			routineExec(t, p, b, c, `DROP ROUTINE "Rd Quote"."Mixed Name"(int)`)
			routineDropNotices(t, notices)
		})
	})
}

func TestReferenceRoutineDropDiagnostics(t *testing.T) {
	for _, kind := range []string{"FUNCTION", "PROCEDURE", "ROUTINE"} {
		for _, signature := range []bool{false, true} {
			name := "rd_absent"
			if signature {
				name += "(integer)"
			}
			t.Run(kind+" "+name, func(t *testing.T) {
				routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
					noun := "function"
					if kind == "PROCEDURE" {
						noun = "procedure"
					}
					message := "could not find a " + noun + ` named "rd_absent"`
					if signature {
						message = noun + " " + name + " does not exist"
					}
					routineDropError(t, p, b, c, "DROP "+kind+" "+name, fixture.ControlsReferenceDiagnostic("42883", message))
					if kind == "ROUTINE" {
						noun = "routine"
					}
					noticeName := "rd_absent()"
					if signature {
						noticeName = "rd_absent(pg_catalog.int4)"
					}
					for _, prepared := range []bool{false, true} {
						s := fixture.Exec("DROP "+kind+" IF EXISTS "+name, 0)
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						routineDropNotices(t, notices, routineDropNotice(noun+" "+noticeName+" does not exist, skipping"))
					}
				})
			})
		}
	}
	for _, tc := range []struct{ q, message string }{
		{"DROP FUNCTION IF EXISTS rd_no_schema.f(integer)", `schema "rd_no_schema" does not exist, skipping`},
		{"DROP FUNCTION IF EXISTS rd_absent(rd_no_type)", `type "rd_no_type" does not exist, skipping`},
		{"DROP FUNCTION IF EXISTS rd_absent(rd_no_schema.t)", `schema "rd_no_schema" does not exist, skipping`},
	} {
		t.Run(tc.q, func(t *testing.T) {
			routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, n *[]fixture.Diagnostic) {
				routineExec(t, p, b, c, tc.q)
				routineDropNotices(t, n, routineDropNotice(tc.message))
			})
		})
	}
	t.Run("kind and aggregate", func(t *testing.T) {
		routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, n *[]fixture.Diagnostic) {
			routineExec(t, p, b, c, "CREATE FUNCTION rd_kind(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$")
			d := fixture.ControlsReferenceDiagnostic("42809", "rd_kind(integer) is not a procedure")
			for _, q := range []string{"DROP PROCEDURE rd_kind(integer)", "DROP PROCEDURE IF EXISTS rd_kind(integer)"} {
				routineDropError(t, p, b, c, q, d)
			}
			routineDropError(t, p, b, c, "DROP PROCEDURE rd_kind", fixture.ControlsReferenceDiagnostic("42883", `could not find a procedure named "rd_kind"`))
			routineExec(t, p, b, c, "DROP PROCEDURE IF EXISTS rd_kind")
			routineDropNotices(t, n, routineDropNotice("procedure rd_kind() does not exist, skipping"))
			routineValue(t, p, b, c, "SELECT rd_kind(4) AS v", "v", "INT4", int64(5))
			d = fixture.ControlsReferenceDiagnostic("42809", `"pg_catalog.sum" is an aggregate function`)
			d.Hint = "Use DROP AGGREGATE to drop aggregate functions."
			routineDropError(t, p, b, c, "DROP FUNCTION pg_catalog.sum(integer)", d)
			routineDropError(t, p, b, c, "DROP ROUTINE pg_catalog.sum(integer)", fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop function sum(integer) because it is required by the database system"))
			routineExec(t, p, b, c, "DROP ROUTINE rd_kind(integer)")
		})
	})
	t.Run("extension membership fence", func(t *testing.T) {
		routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, n *[]fixture.Diagnostic) {
			if b.PG {
				defer func() {
					if _, err := c.ExecContext(p.Ctx, "DROP EXTENSION IF EXISTS intagg CASCADE"); err != nil {
						t.Error(err)
					}
				}()
			}
			routineExec(t, p, b, c, "CREATE EXTENSION intagg SCHEMA public VERSION '1.1'")
			d := fixture.ControlsReferenceDiagnostic("42809", `"public.int_array_aggregate" is an aggregate function`)
			d.Hint = "Use DROP AGGREGATE to drop aggregate functions."
			routineDropError(t, p, b, c, "DROP FUNCTION public.int_array_aggregate(integer)", d)
			d = fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop function int_array_aggregate(integer) because extension intagg requires it")
			d.Hint = "You can drop extension intagg instead."
			for _, q := range []string{"DROP ROUTINE public.int_array_aggregate(integer)", "DROP ROUTINE IF EXISTS public.int_array_aggregate(integer) CASCADE"} {
				routineDropError(t, p, b, c, q, d)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_extension WHERE extname='intagg'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			routineDropNotices(t, n)
		})
	})
}
