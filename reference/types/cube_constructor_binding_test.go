package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCubeConstructorBinding(t *testing.T) {
	t.Run("coercion_and_demand", func(t *testing.T) {
		cubeConstructorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			cubeInstall(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, tc := range []struct{ expr, text string }{
				// Type-name coercion (typeconv-func step3), not an implicit
				// string-to-constructor-formal conversion.
				{"cube('1')", "(1)"}, {"cube('1'::text)", "(1)"}, {"cube('1'::varchar)", "(1)"},
				{"cube(1::smallint)", "(1)"}, {"cube(2::bigint)", "(2)"}, {"cube(1.25::numeric)", "(1.25)"},
				{"cube(1.5::real,2.5::double precision)", "(1.5),(2.5)"},
				{"cube(ARRAY[1.25::numeric,2.5::numeric])", "(1.25, 2.5)"},
				{"cube(ARRAY[1::smallint,2::smallint],ARRAY[3::bigint,4::bigint])", "(1, 2),(3, 4)"},
				{"CASE WHEN true THEN cube(7) ELSE cube(ARRAY[1],ARRAY[2,3]) END", "(7)"},
			} {
				s := cubeConstructorSelect(b, "SELECT "+tc.expr+" AS alias", []string{"alias"}, []string{"CUBE"}, [][]any{{tc.text}})
				run(s)
				run(fixture.Prepared(s))
			}
			nullCall := cubeConstructorSelect(b, "SELECT cube(NULL) AS v", []string{"v"}, []string{"CUBE"}, [][]any{{nil}})
			run(nullCall)
			run(fixture.Prepared(nullCall))
			run(fixture.Exec("CREATE TABLE cube_construct_binding(id integer NOT NULL,v cube)", 0))
			run(fixture.Exec("INSERT INTO cube_construct_binding VALUES (1,cube(1)),(2,cube(2,3)),(3,NULL)", 3))
			for _, tc := range []struct {
				q    string
				rows [][]any
			}{
				{"SELECT cube(v,5) AS extended FROM cube_construct_binding WHERE cube_is_point(v)", [][]any{{"(1, 5)"}}},
				{"SELECT cube(v,5) AS extended FROM cube_construct_binding WHERE id=99", [][]any{}},
				{"SELECT cube(v,5) AS extended FROM cube_construct_binding WHERE id=3", [][]any{{nil}}},
			} {
				s := cubeConstructorSelect(b, tc.q, []string{"extended"}, []string{"CUBE"}, tc.rows)
				s.Bag = true
				run(s)
				run(fixture.Prepared(s))
			}
			for _, tc := range []struct {
				q    string
				args []any
				text any
			}{
				{"SELECT cube($1::double precision) AS v", []any{2.5}, "(2.5)"},
				{"SELECT cube($1::double precision[]) AS v", []any{"{{1,2},{3,4}}"}, "(1, 2, 3, 4)"},
				{"SELECT cube($1::double precision[],$2::double precision[]) AS v", []any{"{1,2}", "{3,4}"}, "(1, 2),(3, 4)"},
				{"SELECT cube($1::cube,$2::double precision) AS v", []any{"(1),(2)", 3}, "(1, 3),(2, 3)"},
				{"SELECT cube($1::cube,$2::double precision,$3::double precision) AS v", []any{"1", 2, 3}, "(1, 2),(1, 3)"},
			} {
				held := cubeConstructorHeld(t, p, c, tc.q)
				defer held.Statement.Close()
				s := cubeConstructorSelect(b, tc.q, []string{"v"}, []string{"CUBE"}, [][]any{{tc.text}}, tc.args...)
				run(s)
				run(fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				nulls := append([]any(nil), tc.args...)
				nulls[0] = nil
				fixture.RunStep(t, p.Ctx, held, b.Recorder, cubeConstructorSelect(b, tc.q, []string{"v"}, []string{"CUBE"}, [][]any{{nil}}, nulls...))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				held.Statement.Close()
			}
			q := "SELECT CASE WHEN $1::boolean THEN cube(7) ELSE cube($2::double precision[],$3::double precision[]) END AS v"
			held := cubeConstructorHeld(t, p, c, q)
			defer held.Statement.Close()
			fixture.RunStep(t, p.Ctx, held, b.Recorder, cubeConstructorSelect(b, q, []string{"v"}, []string{"CUBE"}, [][]any{{"(7)"}}, true, "{1}", "{2,3}"))
			// Group only the ordinary INT4 inquiry result, not cube values.
			// The row-dependent extension requires structural matching of the
			// repeated cube literal and its captured installed calls.
			grouped := fixture.Select("SELECT cube_dim(cube('(1,2)'::cube,id)) AS dim,count(*) AS n FROM cube_construct_binding WHERE id<=2 GROUP BY cube_dim(cube('(1,2)'::cube,id))", []string{"dim", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(3), int64(2)}})
			grouped.Bag = true
			run(grouped)
			run(fixture.Prepared(grouped))
		})
	})
	t.Run("qualified_namespace", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "cube_construct_binding_ns")
		cubeConstructorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE SCHEMA cube_construct_binding_ns", 0))
			run(fixture.Exec("CREATE EXTENSION cube SCHEMA cube_construct_binding_ns", 0))
			run(cubeConstructorSelect(b, "SELECT cube_construct_binding_ns.cube(ARRAY[1,2],ARRAY[3,4]) AS v", []string{"v"}, []string{"CUBE"}, [][]any{{"(1, 2),(3, 4)"}}))
			run(fixture.Exec("SET search_path=cube_construct_binding_ns,public", 0))
			run(cubeConstructorSelect(b, "SELECT cube(cube(1),2,3) AS v,cube_dim(cube(1)) AS dim,cube_is_point(cube(1)) AS point", []string{"v", "dim", "point"}, []string{"CUBE", "INT4", "BOOL"}, [][]any{{"(1, 2),(1, 3)", int64(1), true}}))
		})
	})
	for _, tc := range []struct{ suffix, decoration, kind string }{
		{"(DISTINCT 1::double precision)", "DISTINCT", "an aggregate function"},
		{"(1::double precision ORDER BY 1)", "ORDER BY", "an aggregate function"},
		{"(1::double precision) FILTER (WHERE true)", "FILTER", "an aggregate function"},
		{"(1::double precision) OVER ()", "OVER", "a window function nor an aggregate function"},
	} {
		t.Run(tc.decoration, func(t *testing.T) {
			cubeConstructorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				cubeInstall(t, p, b, c)
				want := cubeConstructorDiagnostic("42809", tc.decoration+" specified, but cube is not "+tc.kind, "")
				want.Position = "8"
				for _, prepared := range []bool{false, true} {
					cubeError(t, p, b, c, "SELECT cube"+tc.suffix, prepared, true, true, want)
				}
			})
		})
	}
	// These are independently predicted resolution diagnostics, not observed
	// oracle facts. Isolated subtests let discovery report every unknown route.
	for _, tc := range []struct {
		name, q, code, message, hint string
		installed                    bool
	}{
		{"missing", "SELECT cube(1::double precision)", "42883", "function cube(double precision) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", false},
		{"two_unknown", "SELECT cube(NULL,NULL)", "42725", "function cube(unknown, unknown) is not unique", "Could not choose a best candidate function. You might need to add explicit type casts.", true},
		{"typed_text_formal", "SELECT cube_dim('1'::text)", "42883", "function cube_dim(text) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", true},
		{"wrong_arity", "SELECT cube()", "42883", "function cube() does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cubeConstructorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				if tc.installed {
					cubeInstall(t, p, b, c)
				}
				want := cubeConstructorDiagnostic(tc.code, tc.message, "")
				want.Hint, want.Position = tc.hint, "8"
				for _, prepared := range []bool{false, true} {
					cubeError(t, p, b, c, tc.q, prepared, true, true, want)
				}
			})
		})
	}
}
