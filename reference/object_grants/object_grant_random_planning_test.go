package reference_test

import (
	"database/sql"
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

func objectGrantRandomPlanningError(query, code, message string, args ...any) fixture.Step {
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: code, Message: message}, args...)
	s.SelectRows = true
	s.ErrorStage = "execute"
	return s
}

func objectGrantRandomPlanningFinite(t *testing.T, p *fixture.Pair, executor fixture.Executor, query string) float64 {
	t.Helper()
	rows, err := executor.QueryContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil || len(cols) != 1 || cols[0] != "v" {
		t.Fatalf("columns %v: %v", cols, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "FLOAT8" {
		t.Fatalf("expected one FLOAT8 column: %v, %v", types, err)
	}
	if !rows.Next() {
		t.Fatalf("missing singleton: %v", rows.Err())
	}
	var v any
	if err := rows.Scan(&v); err != nil {
		t.Fatal(err)
	}
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		t.Fatalf("expected finite FLOAT8, got %#v", v)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("expected exactly one row: %v", rows.Err())
	}
	return f
}

func TestReferenceObjectGrantRandomPlanningChildren(t *testing.T) {
	cases := []struct{ name, target, query, typ string }{
		{"int4", "pg_catalog.abs(integer)", "SELECT random(NULL::integer,abs(-1::integer)) AS v", "INT4"},
		{"int8", "pg_catalog.abs(bigint)", "SELECT random(NULL::bigint,abs(-1::bigint)) AS v", "INT8"},
		{"numeric", "pg_catalog.abs(numeric)", "SELECT random(NULL::numeric,abs(-1::numeric)) AS v", "NUMERIC"},
		{"normal_mean_null", "pg_catalog.abs(integer)", "SELECT random_normal(NULL::double precision,abs(-1::integer)) AS v", "FLOAT8"},
		{"normal_stddev_null", "pg_catalog.abs(integer)", "SELECT random_normal(abs(-1::integer),NULL::double precision) AS v", "FLOAT8"},
		{"normal_default", "pg_catalog.abs(integer)", "SELECT random_normal(abs(-1::integer)) AS v", "FLOAT8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				defer objectGrantRoutineCleanup(t, p, c, []string{tc.target})
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.target+" FROM PUBLIC", 0))
				denied := objectGrantRandomPlanningError(tc.query, "42501", "permission denied for function abs")
				var stmt *sql.Stmt
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					var err error
					stmt, err = c.PrepareContext(p.Ctx, tc.query)
					if err != nil {
						t.Fatal("prepare denied child", err)
					}
					run(denied)
				})
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.query}
				fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RunStep(t, p.Ctx, held, b.Recorder, denied) })
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+tc.target+" TO og_object_reader", 0))
				want := fixture.Select(tc.query, []string{"v"}, []string{tc.typ}, [][]any{{nil}})
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					if tc.name == "normal_default" {
						var executor fixture.Executor = c
						if prepared {
							fresh, err := c.PrepareContext(p.Ctx, tc.query)
							if err != nil { t.Fatal(err) }
							defer fresh.Close()
							executor = fixture.HeldStatement{Executor: c, Statement: fresh, SQL: tc.query}
						}
						objectGrantRandomPlanningFinite(t, p, executor, tc.query)
						objectGrantRandomPlanningFinite(t, p, held, tc.query)
					} else {
						run(want)
						fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
					}
				})
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.target+" FROM og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					run(denied)
					// Source-derived PG warm-plan hypothesis; root must resolve in PG-first discovery.
					// Native intentionally replans against the current actor's authority.
					if b.PG {
						if tc.name == "normal_default" {
							objectGrantRandomPlanningFinite(t, p, held, tc.query)
						} else {
							fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
						}
					} else {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
					}
				})
			})
		})
	}
}

func TestReferenceObjectGrantRandomPlanningHeldImport(t *testing.T) {
	for _, tc := range []struct{ name, query, typ string }{
		{"int4", "SELECT random(NULL::integer,abs($1::integer)) AS v", "INT4"},
		{"normal", "SELECT random_normal(NULL::double precision,abs($1::integer)) AS v", "FLOAT8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				target := "pg_catalog.abs(integer)"
				defer objectGrantRoutineCleanup(t, p, c, []string{target})
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
				bad := objectGrantRandomPlanningError(tc.query, "22P02", `invalid input syntax for type integer: "bad"`, "bad")
				bad.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
				denied := objectGrantRandomPlanningError(tc.query, "42501", "permission denied for function abs", -1)
				want := fixture.Select(tc.query, []string{"v"}, []string{tc.typ}, [][]any{{nil}}, -1)
				var stmt *sql.Stmt
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					var err error
					stmt, err = c.PrepareContext(p.Ctx, tc.query)
					if err != nil { t.Fatal(err) }
					run(bad)
					run(denied)
				})
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.query}
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
				})
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					run(want)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
				})
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					run(bad)
					run(denied)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
					// Parameterized custom plans recheck child authority after revocation.
					fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
				})
			})
		})
	}
}
