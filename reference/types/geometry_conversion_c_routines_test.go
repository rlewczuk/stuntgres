package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryConversionCRoutinesResolution(t *testing.T) {
	geometryConversionCScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		q := "SELECT oid::int4 AS id,oid::regprocedure::text AS signature,pronamespace::int4 AS ns,proowner::int4 AS owner,pronargs::int4 AS nargs,prorettype::int4 AS result,provolatile::text AS volatility,proisstrict AS strict,proparallel::text AS parallel,prokind::text AS kind FROM pg_proc WHERE oid IN (1448,1449,1475,1544) ORDER BY oid"
		s := fixture.Select(q, []string{"id", "signature", "ns", "owner", "nargs", "result", "volatility", "strict", "parallel", "kind"}, []string{"INT4", "TEXT", "INT4", "INT4", "INT4", "INT4", "TEXT", "BOOL", "TEXT", "TEXT"}, [][]any{
			{int64(1448), "polygon(box)", int64(11), int64(10), int64(1), int64(604), "i", true, "s", "f"},
			{int64(1449), "polygon(path)", int64(11), int64(10), int64(1), int64(604), "i", true, "s", "f"},
			{int64(1475), "polygon(integer,circle)", int64(11), int64(10), int64(2), int64(604), "i", true, "s", "f"},
			{int64(1544), "polygon(circle)", int64(11), int64(10), int64(1), int64(604), "i", true, "s", "f"},
		})
		run(s)
		run(fixture.Prepared(s))
		for _, tc := range []struct{ expr, signature string }{
			{"polygon()", "polygon()"}, {"polygon(1)", "polygon(integer)"},
			{"polygon(point '(0,0)')", "polygon(point)"},
			{"polygon(4,point '(0,0)')", "polygon(integer, point)"},
			{"polygon(circle '<(0,0),1>',4)", "polygon(circle, integer)"},
			{"polygon(4::bigint,circle '<(0,0),1>')", "polygon(bigint, circle)"},
			{"polygon(4,circle '<(0,0),1>',4)", "polygon(integer, circle, integer)"},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError("SELECT "+tc.expr, "42883", "function "+tc.signature+" does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts.", prepared, false))
				}
			})
		}
		for _, prepared := range []bool{false, true} {
			run(boxError("SELECT polygon($1) AS v", "42725", "function polygon(unknown) is not unique", "8", "Could not choose a best candidate function. You might need to add explicit type casts.", prepared, false, nil))
		}
		run(fixture.Exec("CREATE DOMAIN geom_c_polygon AS polygon", 0))
		run(fixture.Exec("CREATE DOMAIN geom_c_text AS text", 0))
		shortcuts := func(phase string) {
			for _, operand := range []string{"'((1,2))'", "'((1,2))'::text", "'((1,2))'::geom_c_text", "'((1,2))'::polygon", "'((1,2))'::geom_c_polygon", "$1::text", "$1::geom_c_polygon", "NULL", "NULL::geom_c_polygon"} {
				t.Run(phase+"/"+operand, func(t *testing.T) {
					var args []any
					var want any = "((1,2))"
					if strings.Contains(operand, "$1") {
						args = []any{"((1,2))"}
					}
					if strings.HasPrefix(operand, "NULL") {
						want = nil
					}
					s := geometryConversionCSelect(b, "SELECT polygon("+operand+") AS v", want, args...)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				})
			}
		}
		shortcuts("builtin")
		run(fixture.Exec("CREATE FUNCTION public.polygon(box) RETURNS polygon LANGUAGE SQL AS 'SELECT polygon ''((9,9))'''", 0))
		run(fixture.Exec("SET search_path=public,pg_catalog", 0))
		shortcuts("sql_competitor")
		for _, tc := range []struct{ expr, want string }{
			{"polygon(box '(1,1),(0,0)')", "((9,9))"},
			{"pg_catalog.polygon(box '(1,1),(0,0)')", "((0,0),(0,1),(1,1),(1,0))"},
		} {
			s := geometryConversionCSelect(b, "SELECT "+tc.expr+" AS v", tc.want)
			run(s)
			run(fixture.Prepared(s))
		}
		run(fixture.Exec("CREATE VIEW geom_c_recipe AS SELECT polygon('((1,2))') AS v", 0))
		q = "SELECT polygon($1::text) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, geometryConversionCSelect(b, q, "((1,2))", "((1,2))"))
		run(fixture.Exec("CREATE FUNCTION public.polygon(text) RETURNS polygon LANGUAGE SQL AS 'SELECT polygon ''((7,7))'''", 0))
		for _, operand := range []string{"'((1,2))'::text", "$1::text", "NULL::text", "'((1,2))'", "NULL"} {
			var args []any
			var want any = "((7,7))"
			if strings.Contains(operand, "$1") {
				args = []any{"((1,2))"}
			}
			if operand == "'((1,2))'" {
				want = "((1,2))"
			}
			if operand == "NULL" {
				want = nil
			}
			s := geometryConversionCSelect(b, "SELECT polygon("+operand+") AS v", want, args...)
			run(s)
			run(fixture.Prepared(s))
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, geometryConversionCSelect(b, q, "((1,2))", "((1,2))"))
		s = geometryConversionCSelect(b, "SELECT v FROM geom_c_recipe", "((1,2))")
		run(s)
		run(fixture.Prepared(s))
		run(fixture.Exec("RESET search_path", 0))
	})
}
