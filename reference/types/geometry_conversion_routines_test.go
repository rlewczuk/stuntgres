package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryConversionRoutines(t *testing.T) {
	geometryConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		q := "SELECT oid::int4 AS id,oid::regprocedure::text AS signature,pronamespace::int4 AS ns,proowner::int4 AS owner,pronargs::int4 AS nargs,prorettype::int4 AS result,provolatile::text AS volatility,proisstrict AS strict,proparallel::text AS parallel FROM pg_proc WHERE oid IN (1421,1446,1473,1474,1479,1480,1493,4067,4091) ORDER BY oid"
		rows := [][]any{
			{int64(1421), "box(point,point)", int64(11), int64(10), int64(2), int64(603), "i", true, "s"},
			{int64(1446), "box(polygon)", int64(11), int64(10), int64(1), int64(603), "i", true, "s"},
			{int64(1473), "circle(point,double precision)", int64(11), int64(10), int64(2), int64(718), "i", true, "s"},
			{int64(1474), "circle(polygon)", int64(11), int64(10), int64(1), int64(718), "i", true, "s"},
			{int64(1479), "circle(box)", int64(11), int64(10), int64(1), int64(718), "i", true, "s"},
			{int64(1480), "box(circle)", int64(11), int64(10), int64(1), int64(603), "i", true, "s"},
			{int64(1493), "line(point,point)", int64(11), int64(10), int64(2), int64(628), "i", true, "s"},
			{int64(4067), "bound_box(box,box)", int64(11), int64(10), int64(2), int64(603), "i", true, "s"},
			{int64(4091), "box(point)", int64(11), int64(10), int64(1), int64(603), "i", true, "s"},
		}
		s := fixture.Select(q, []string{"id", "signature", "ns", "owner", "nargs", "result", "volatility", "strict", "parallel"}, []string{"INT4", "TEXT", "INT4", "INT4", "INT4", "INT4", "TEXT", "BOOL", "TEXT"}, rows)
		run(s)
		run(fixture.Prepared(s))
		for _, tc := range geometryConversionCases {
			s := geometryConversionSelect(b, "SELECT pg_catalog."+tc.expression+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{tc.want}})
			run(s)
			run(fixture.Prepared(s))
		}
		run(fixture.Exec("CREATE FUNCTION public.box(point) RETURNS box LANGUAGE SQL AS 'SELECT box ''(9,9),(8,8)'''", 0))
		run(fixture.Exec("SET search_path=public,pg_catalog", 0))
		run(geometryConversionSelect(b, "SELECT box(point '(1,0)') AS v", []string{"v"}, []string{"BOX"}, [][]any{{"(9,9),(8,8)"}}))
		run(geometryConversionSelect(b, "SELECT pg_catalog.box(point '(1,0)') AS v", []string{"v"}, []string{"BOX"}, [][]any{{"(1,0),(1,0)"}}))
		run(fixture.Exec("RESET search_path", 0))
	})
}
