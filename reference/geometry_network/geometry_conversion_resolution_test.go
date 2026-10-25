package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryConversionResolution(t *testing.T) {
	geometryConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE DOMAIN geom_resolution_box AS box",
			"CREATE DOMAIN geom_resolution_circle AS circle",
			"CREATE DOMAIN geom_resolution_text AS text",
		} {
			run(fixture.Exec(q, 0))
		}
		values := func(phase string) {
			t.Helper()
			for _, tc := range []struct {
				expression, typ string
				want            any
				args            []any
			}{
				{"box('(2,3),(0,0)'::geom_resolution_box)", "BOX", "(2,3),(0,0)", nil},
				{"circle('<(2,3),4>'::geom_resolution_circle)", "CIRCLE", "<(2,3),4>", nil},
				{"box(NULL::geom_resolution_box)", "BOX", nil, nil},
				{"circle(NULL::geom_resolution_circle)", "CIRCLE", nil, nil},
				{"box($1::geom_resolution_box)", "BOX", "(2,3),(0,0)", []any{"(2,3),(0,0)"}},
				{"circle($1::geom_resolution_circle)", "CIRCLE", "<(2,3),4>", []any{"<(2,3),4>"}},
				{"box($1::geom_resolution_box)", "BOX", nil, []any{nil}},
				{"circle($1::geom_resolution_circle)", "CIRCLE", nil, []any{nil}},
				{"box(NULL)", "BOX", nil, nil},
				{"circle(NULL)", "CIRCLE", nil, nil},
				{"box('(2,3),(0,0)')", "BOX", "(2,3),(0,0)", nil},
				{"circle('<(2,3),4>')", "CIRCLE", "<(2,3),4>", nil},
				{"box('(2,3),(0,0)'::text)", "BOX", "(2,3),(0,0)", nil},
				{"circle('<(2,3),4>'::text)", "CIRCLE", "<(2,3),4>", nil},
				{"box('(2,3),(0,0)'::geom_resolution_text)", "BOX", "(2,3),(0,0)", nil},
				{"circle('<(2,3),4>'::geom_resolution_text)", "CIRCLE", "<(2,3),4>", nil},
				{"box($1::text)", "BOX", "(2,3),(0,0)", []any{"(2,3),(0,0)"}},
				{"circle($1::text)", "CIRCLE", "<(2,3),4>", []any{"<(2,3),4>"}},
				{"pg_catalog.box('(2,3),(0,0)')", "BOX", "(2,3),(0,0)", nil},
				{"pg_catalog.circle('<(2,3),4>')", "CIRCLE", "<(2,3),4>", nil},
			} {
				t.Run(phase+"/"+tc.expression+fmt.Sprint(tc.args), func(t *testing.T) {
					s := geometryConversionSelect(b, "SELECT "+tc.expression+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}, tc.args...)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				})
			}
			for _, name := range []string{"box", "circle"} {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s_unresolved/prepared=%t", phase, name, prepared), func(t *testing.T) {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError("SELECT "+name+"($1) AS v", "42725", "function "+name+"(unknown) is not unique", "8", "Could not choose a best candidate function. You might need to add explicit type casts.", prepared, false, nil))
					})
				}
			}
		}
		values("builtin_only")
		run(fixture.Exec("CREATE FUNCTION public.box(point) RETURNS box LANGUAGE SQL AS 'SELECT box ''(9,9),(8,8)'''", 0))
		run(fixture.Exec("CREATE FUNCTION public.circle(box) RETURNS circle LANGUAGE SQL AS 'SELECT ''<(9,9),8>''::circle'", 0))
		run(fixture.Exec("SET search_path=public,pg_catalog", 0))
		values("visible_geometry_sql_overloads")
		for _, tc := range []struct{ expression, typ, want string }{
			{"box(point '(1,0)')", "BOX", "(9,9),(8,8)"},
			{"pg_catalog.box(point '(1,0)')", "BOX", "(1,0),(1,0)"},
			{"circle(box '(6,8),(0,0)')", "CIRCLE", "<(9,9),8>"},
			{"pg_catalog.circle(box '(6,8),(0,0)')", "CIRCLE", "<(3,4),5>"},
		} {
			s := geometryConversionSelect(b, "SELECT "+tc.expression+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{tc.want}})
			run(s)
			run(fixture.Prepared(s))
		}
		// A real exact TEXT declaration must win before the typename shortcut;
		// unknown constants remain distinct from known TEXT and parameters.
		run(fixture.Exec("CREATE FUNCTION public.box(text) RETURNS box LANGUAGE SQL AS 'SELECT box ''(7,7),(6,6)'''", 0))
		run(fixture.Exec("CREATE FUNCTION public.circle(text) RETURNS circle LANGUAGE SQL AS 'SELECT ''<(7,7),6>''::circle'", 0))
		for _, tc := range []struct {
			expression, typ string
			want            any
			args            []any
		}{
			{"box('(2,3),(0,0)'::text)", "BOX", "(7,7),(6,6)", nil},
			{"circle('<(2,3),4>'::text)", "CIRCLE", "<(7,7),6>", nil},
			{"box(NULL::text)", "BOX", "(7,7),(6,6)", nil},
			{"circle(NULL::text)", "CIRCLE", "<(7,7),6>", nil},
			{"box($1::text)", "BOX", "(7,7),(6,6)", []any{"(2,3),(0,0)"}},
			{"circle($1::text)", "CIRCLE", "<(7,7),6>", []any{"<(2,3),4>"}},
			{"box('(2,3),(0,0)')", "BOX", "(2,3),(0,0)", nil},
			{"circle('<(2,3),4>')", "CIRCLE", "<(2,3),4>", nil},
			{"box(NULL)", "BOX", nil, nil},
			{"circle(NULL)", "CIRCLE", nil, nil},
		} {
			t.Run("exact_text/"+tc.expression, func(t *testing.T) {
				s := geometryConversionSelect(b, "SELECT "+tc.expression+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}, tc.args...)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			})
		}
		run(fixture.Exec("RESET search_path", 0))
	})
}
