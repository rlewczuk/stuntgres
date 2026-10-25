package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryConversionBRoutinesResolution(t *testing.T) {
	geometryConversionBScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		// Use the supported catalog projection, as in A. Canonical signatures
		// identify formals; declaration units additionally assert exact formal
		// OIDs, ReturnsSet=false, Defaults=0 and Variadic=0.
		q := "SELECT oid::int4 AS id,oid::regprocedure::text AS signature,pronamespace::int4 AS ns,proowner::int4 AS owner,pronargs::int4 AS nargs,prorettype::int4 AS result,provolatile::text AS volatility,proisstrict AS strict,proparallel::text AS parallel,prokind::text AS kind FROM pg_proc WHERE oid IN (993,1416,1440,1447,1532,1534,1540,1541) ORDER BY oid"
		rows := [][]any{
			{int64(993), "lseg(point,point)", int64(11), int64(10), int64(2), int64(601), "i", true, "s", "f"},
			{int64(1416), "point(circle)", int64(11), int64(10), int64(1), int64(600), "i", true, "s", "f"},
			{int64(1440), "point(double precision,double precision)", int64(11), int64(10), int64(2), int64(600), "i", true, "s", "f"},
			{int64(1447), "path(polygon)", int64(11), int64(10), int64(1), int64(602), "i", true, "s", "f"},
			{int64(1532), "point(lseg)", int64(11), int64(10), int64(1), int64(600), "i", true, "s", "f"},
			{int64(1534), "point(box)", int64(11), int64(10), int64(1), int64(600), "i", true, "s", "f"},
			{int64(1540), "point(polygon)", int64(11), int64(10), int64(1), int64(600), "i", true, "s", "f"},
			{int64(1541), "lseg(box)", int64(11), int64(10), int64(1), int64(601), "i", true, "s", "f"},
		}
		s := fixture.Select(q, []string{"id", "signature", "ns", "owner", "nargs", "result", "volatility", "strict", "parallel", "kind"}, []string{"INT4", "TEXT", "INT4", "INT4", "INT4", "INT4", "TEXT", "BOOL", "TEXT", "TEXT"}, rows)
		run(s)
		run(fixture.Prepared(s))
		for _, tc := range geometryConversionBCases {
			s := geometryConversionSelect(b, "SELECT pg_catalog."+tc.expression+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{tc.want}})
			run(s)
			run(fixture.Prepared(s))
		}
		for _, typ := range []string{"point", "lseg", "path", "text"} {
			run(fixture.Exec("CREATE DOMAIN geom_b_"+typ+" AS "+typ, 0))
		}
		shortcuts := func(phase string) {
			for _, tc := range []struct{ name, typ, text string }{
				{"point", "POINT", "(1,2)"}, {"lseg", "LSEG", "[(1,2),(3,4)]"}, {"path", "PATH", "[(1,2)]"},
			} {
				for _, operand := range []string{"'" + tc.text + "'", "'" + tc.text + "'::text", "'" + tc.text + "'::geom_b_text", "'" + tc.text + "'::" + tc.name, "'" + tc.text + "'::geom_b_" + tc.name, "$1::text", "$1::geom_b_" + tc.name, "NULL", "NULL::geom_b_" + tc.name} {
					t.Run(phase+"/"+tc.name+"/"+operand, func(t *testing.T) {
						var args []any
						var want any = tc.text
						if strings.Contains(operand, "$1") {
							args = []any{tc.text}
						}
						if strings.HasPrefix(operand, "NULL") {
							want = nil
						}
						s := geometryConversionSelect(b, "SELECT "+tc.name+"("+operand+") AS v", []string{"v"}, []string{tc.typ}, [][]any{{want}}, args...)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					})
				}
			}
		}
		shortcuts("builtin")
		for _, tc := range []struct {
			expr, typ, want string
			args            []any
		}{
			{"lseg($1)", "LSEG", "[(1,0),(-1,0)]", []any{"(1,0),(-1,0)"}},
			{"path($1)", "PATH", "((0,0),(1,1))", []any{"((0,0),(1,1))"}},
			{"point($1,$2)", "POINT", "(1,2)", []any{float64(1), float64(2)}},
			{"lseg($1,$2)", "LSEG", "[(-1,0),(1,0)]", []any{"(-1,0)", "(1,0)"}},
		} {
			t.Run("uncast/"+tc.expr, func(t *testing.T) {
				q := "SELECT " + tc.expr + " AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, null := range []bool{false, true, false} {
					args := append([]any(nil), tc.args...)
					var want any = tc.want
					if null {
						args[0] = nil
						want = nil
					}
					s := geometryConversionSelect(b, q, []string{"v"}, []string{tc.typ}, [][]any{{want}}, args...)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			})
		}
		run(fixture.Exec("CREATE FUNCTION public.point(box) RETURNS point LANGUAGE SQL AS 'SELECT point ''(9,9)'''", 0))
		run(fixture.Exec("SET search_path=public,pg_catalog", 0))
		shortcuts("sql_competitor")
		for _, tc := range []struct{ expr, want string }{{"point(box '(1,0),(-1,0)')", "(9,9)"}, {"pg_catalog.point(box '(1,0),(-1,0)')", "(0,0)"}} {
			s := geometryConversionSelect(b, "SELECT "+tc.expr+" AS v", []string{"v"}, []string{"POINT"}, [][]any{{tc.want}})
			run(s)
			run(fixture.Prepared(s))
		}
		// Persist an unknown-constant typename recipe, then introduce an exact TEXT
		// competitor. The original recipe must remain an IO conversion on replay.
		run(fixture.Exec("CREATE VIEW geom_b_recipe AS SELECT point('(1,2)') AS v", 0))
		for _, tc := range []struct{ name, typ, text, want string }{{"point", "POINT", "(1,2)", "(7,7)"}, {"lseg", "LSEG", "[(1,2),(3,4)]", "[(7,7),(6,6)]"}, {"path", "PATH", "[(1,2)]", "[(7,7)]"}} {
			run(fixture.Exec(fmt.Sprintf("CREATE FUNCTION public.%s(text) RETURNS %s LANGUAGE SQL AS 'SELECT %s ''%s'''", tc.name, tc.name, tc.name, tc.want), 0))
			for _, operand := range []string{"'" + tc.text + "'::text", "$1::text", "NULL::text", "'" + tc.text + "'", "NULL"} {
				var args []any
				var want any = tc.want
				if strings.Contains(operand, "$1") {
					args = []any{tc.text}
				}
				if operand == "'"+tc.text+"'" {
					want = tc.text
				}
				if operand == "NULL" {
					want = nil
				}
				s := geometryConversionSelect(b, "SELECT "+tc.name+"("+operand+") AS v", []string{"v"}, []string{tc.typ}, [][]any{{want}}, args...)
				run(s)
				run(fixture.Prepared(s))
			}
		}
		s = geometryConversionSelect(b, "SELECT v FROM geom_b_recipe", []string{"v"}, []string{"POINT"}, [][]any{{"(1,2)"}})
		run(s)
		run(fixture.Prepared(s))
		run(fixture.Exec("RESET search_path", 0))
	})
}
