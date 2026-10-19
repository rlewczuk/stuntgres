package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// R3/R4 diagnostics were observed in tests-46.md; bounded recursion fields
// in tests-47.md. Stack provenance is structural, not an incidental depth count.
func routineInvocationDiagnostic(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, code, message string, prepared bool) {
	t.Helper()
	var err error
	stage := "execute"
	if prepared {
		var stmt *sql.Stmt
		stmt, err = c.PrepareContext(p.Ctx, q)
		if err != nil {
			stage = "prepare"
		} else {
			_, err = stmt.ExecContext(p.Ctx)
			stmt.Close()
		}
	} else {
		_, err = c.ExecContext(p.Ctx, q)
	}
	if err == nil {
		t.Errorf("expected %s: %s", code, q)
		return
	}
	d := fixture.ExtractDiagnostic(t, err)
	if code == "54001" {
		want := fixture.ControlsReferenceDiagnostic(code, message)
		if b.PG {
			want.Hint = "Increase the configuration parameter \"max_stack_depth\" (currently 100kB), after ensuring the platform's stack depth limit is adequate."
		}
		lines := strings.Split(d.Context, "\n")
		mutual := strings.Contains(q, "mutual")
		for i, line := range lines {
			if mutual {
				a := "SQL function \"routine_review_mutual_a\" statement 1"
				b := "SQL function \"routine_review_mutual_b\" statement 1"
				if line != a && line != b || i > 0 && line == lines[i-1] {
					t.Fatalf("invalid mutual recursion provenance: %q", d.Context)
				}
			} else if line != "SQL function \"routine_review_recurse\" statement 1" {
				t.Fatalf("invalid direct recursion provenance: %q", d.Context)
			}
		}
		want.Context = d.Context // Every line was independently constrained above.
		fixture.CheckDiagnostic(t, err, fixture.Step{Diagnostic: &want, ErrorStage: "execute"}, stage)
		return
	}
	want := fixture.ControlsReferenceDiagnostic(code, message)
	if code == "42P02" {
		switch {
		case strings.Contains(q, "routine_review_slot("):
			want.Position = "87"
		case strings.Contains(q, "routine_review_zero("):
			want.Position = "80"
		default:
			want.Position = "97"
		}
	} else {
		want.Detail = "Actual return type is text."
		want.Context = "SQL function \"routine_review_result_read\" statement 1"
	}
	fixture.CheckDiagnostic(t, err, fixture.Step{Diagnostic: &want, ErrorStage: "execute"}, stage)
}

func TestReferenceRoutineReviewClosedFormals(t *testing.T) {
	for _, tc := range []struct {
		name, args, body string
		slot             int
	}{
		{"routine_review_slot", "integer", "SELECT $2+1", 2},
		{"routine_review_zero", "", "SELECT $1::integer", 1},
		{"routine_review_nested", "integer", "SELECT (SELECT $2+1)", 2},
		{"routine_review_cte", "integer", "WITH a AS (SELECT $2+1 AS x) SELECT x FROM a", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := fmt.Sprintf("CREATE FUNCTION %s(%s) RETURNS integer LANGUAGE SQL AS $$%s$$", tc.name, tc.args, tc.body)
				for _, prepared := range []bool{false, true} {
					routineInvocationDiagnostic(t, p, b, c, q, "42P02", fmt.Sprintf("there is no parameter $%d", tc.slot), prepared)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_proc WHERE proname='"+tc.name+"'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
				routineExec(t, p, b, c, fmt.Sprintf("CREATE FUNCTION %s(%s) RETURNS integer LANGUAGE SQL AS $$SELECT 7$$", tc.name, tc.args))
				call := "SELECT " + tc.name + "()"
				if tc.args != "" {
					call = "SELECT " + tc.name + "(4)"
				}
				routineValue(t, p, b, c, call, tc.name, "INT4", int64(7))
			})
		})
	}
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE FUNCTION routine_review_unused(integer,boolean) RETURNS integer LANGUAGE SQL AS $$SELECT 8$$",
			"CREATE FUNCTION routine_review_many(integer,integer) RETURNS integer LANGUAGE SQL AS $$WITH a AS (SELECT $1 AS x) SELECT (SELECT x+$2 FROM a)$$")
		routineValue(t, p, b, c, "SELECT routine_review_unused(4,true)", "routine_review_unused", "INT4", int64(8))
		routineValue(t, p, b, c, "SELECT routine_review_many(4,9)", "routine_review_many", "INT4", int64(13))
	})
}

func TestReferenceRoutineReviewReturnReanalysis(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, kind := range []string{"width", "type", "assignment"} {
			for _, populated := range []bool{false, true} {
				t.Run(fmt.Sprintf("held=%t/%s/rows=%t", held, kind, populated), func(t *testing.T) {
					routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						routineExec(t, p, b, c,
							"CREATE SEQUENCE routine_review_effect",
							"CREATE TABLE routine_review_result(x integer)",
							"CREATE FUNCTION routine_review_result_read() RETURNS integer LANGUAGE SQL AS $$SELECT * FROM routine_review_result WHERE nextval('routine_review_effect')>0$$")
						q := "SELECT routine_review_result_read() AS v"
						var stmt *sql.Stmt
						if held {
							var err error
							stmt, err = c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{nil}}))
						}
						routineExec(t, p, b, c, "DROP TABLE routine_review_result")
						declaration, values := "x bigint", "5"
						if kind == "type" {
							declaration, values = "x text", "'bad'"
						}
						if kind == "width" {
							declaration, values = "x integer,y integer", "5,6"
						}
						routineExec(t, p, b, c, "CREATE TABLE routine_review_result("+declaration+")")
						if populated {
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO routine_review_result VALUES ("+values+")", 1))
						}
						if kind == "assignment" {
							var want any
							if populated {
								want = int64(5)
							}
							var executor fixture.Executor = c
							if held {
								executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
							}
							fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{want}}))
						} else {
							want := fixture.ControlsReferenceDiagnostic("42P13", "return type mismatch in function declared to return integer")
							want.Detail = "Actual return type is text."
							if kind == "width" {
								want.Detail = "Final statement must return exactly one column."
							}
							want.Context = "SQL function \"routine_review_result_read\" statement 1"
							executor := fixture.Executor(c)
							if held {
								executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
							}
							fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.ImplicitError(q, want))
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT nextval('routine_review_effect') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
						}
					})
				})
			}
		}
	}
}

func TestReferenceRoutineReviewRecursion(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE FUNCTION routine_review_finite(integer) RETURNS integer LANGUAGE SQL AS $$SELECT CASE WHEN $1=0 THEN 0 ELSE 1+routine_review_finite($1-1) END$$",
			"CREATE FUNCTION routine_review_recurse() RETURNS integer LANGUAGE SQL AS $$SELECT routine_review_recurse()$$",
			"CREATE FUNCTION routine_review_recursive_sleep(integer) RETURNS integer LANGUAGE SQL AS $$SELECT CASE WHEN $1=0 THEN length(pg_sleep(30)::text) ELSE routine_review_recursive_sleep($1-1) END$$")
		routineValue(t, p, b, c, "SELECT routine_review_finite(12)", "routine_review_finite", "INT4", int64(12))
		// Oracle-only response-size bound: this never configures the native
		// invocation guard or asserts that native measures PostgreSQL stack bytes.
		if b.PG {
			routineExec(t, p, b, c, "SET max_stack_depth = '100kB'")
		}
		for _, prepared := range []bool{false, true} {
			routineInvocationDiagnostic(t, p, b, c, "SELECT routine_review_recurse()", "54001", "stack depth limit exceeded", prepared)
			routineValue(t, p, b, c, "SELECT routine_review_finite(3)", "routine_review_finite", "INT4", int64(3))
		}
		q := "SELECT routine_review_recursive_sleep(4)"
		pgContext := "SQL function \"routine_review_recursive_sleep\" statement 1\nSQL function \"routine_review_recursive_sleep\" statement 1"
		fixture.SleepActiveCancelWithContext(t, p, b, c, q, pgContext, nil, func(ctx context.Context) fixture.RelationOutcome {
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
		routineValue(t, p, b, c, "SELECT routine_review_finite(3)", "routine_review_finite", "INT4", int64(3))
	})
}

func TestReferenceRoutineReviewMutualRecursion(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		script := "SET check_function_bodies = false;\n" +
			"CREATE FUNCTION public.routine_review_mutual_a(integer) RETURNS integer LANGUAGE SQL AS $$SELECT CASE WHEN $1=0 THEN 0 ELSE 1+routine_review_mutual_b($1-1) END$$;\n" +
			"CREATE FUNCTION public.routine_review_mutual_b(integer) RETURNS integer LANGUAGE SQL AS $$SELECT CASE WHEN $1=0 THEN 0 ELSE 1+routine_review_mutual_a($1-1) END$$;\n"
		if b.PG {
			if _, err := c.ExecContext(p.Ctx, script); err != nil {
				t.Fatal(err)
			}
			routineExec(t, p, b, c, "SET check_function_bodies = true")
		} else {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
				t.Fatal(err)
			}
		}
		routineValue(t, p, b, c, "SELECT routine_review_mutual_a(12)", "routine_review_mutual_a", "INT4", int64(12))
		if b.PG {
			routineExec(t, p, b, c, "SET max_stack_depth = '100kB'")
		}
		for _, prepared := range []bool{false, true} {
			routineInvocationDiagnostic(t, p, b, c, "SELECT routine_review_mutual_a(-1)", "54001", "stack depth limit exceeded", prepared)
			routineValue(t, p, b, c, "SELECT routine_review_mutual_b(3)", "routine_review_mutual_b", "INT4", int64(3))
		}
	})
}
