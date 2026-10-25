package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryMeasureBoxLineRoutines(t *testing.T) {
	geometryMeasureScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		s := fixture.Select("SELECT oid::int4 AS id,oid::regprocedure::text AS signature,pronamespace::int4 AS ns,proowner::int4 AS owner,pronargs::int4 AS nargs,prorettype::int4 AS result,provolatile::text AS volatility,proisstrict AS strict,proparallel::text AS parallel,prokind::text AS kind FROM pg_proc WHERE oid IN (976,977,981,992) ORDER BY oid", []string{"id", "signature", "ns", "owner", "nargs", "result", "volatility", "strict", "parallel", "kind"}, []string{"INT4", "TEXT", "INT4", "INT4", "INT4", "INT4", "TEXT", "BOOL", "TEXT", "TEXT"}, [][]any{
			{int64(976), "width(box)", int64(11), int64(10), int64(1), int64(701), "i", true, "s", "f"},
			{int64(977), "height(box)", int64(11), int64(10), int64(1), int64(701), "i", true, "s", "f"},
			{int64(981), "diagonal(box)", int64(11), int64(10), int64(1), int64(601), "i", true, "s", "f"},
			{int64(992), "slope(point,point)", int64(11), int64(10), int64(2), int64(701), "i", true, "s", "f"},
		})
		run(s)
		run(fixture.Prepared(s))
		run(fixture.Exec("CREATE VIEW geom_box_line_builtin AS SELECT pg_catalog.width(box '(4,6),(1,2)') AS w,pg_catalog.height(box '(4,6),(1,2)') AS h,pg_catalog.diagonal(box '(4,6),(1,2)') AS d,pg_catalog.slope(point '(0,0)',point '(2,1)') AS s", 0))
		run(fixture.Exec("CREATE FUNCTION public.width(box) RETURNS float8 LANGUAGE SQL AS 'SELECT 99::float8'", 0))
		run(fixture.Exec("CREATE FUNCTION public.slope(point,point) RETURNS float8 LANGUAGE SQL AS 'SELECT 77::float8'", 0))
		run(fixture.Exec("SET search_path=public,pg_catalog", 0))
		for _, s := range []fixture.Step{
			geometryMeasureSelect(b, "SELECT width(box '(4,6),(1,2)') AS w,slope(point '(0,0)',point '(2,1)') AS s", []string{"w", "s"}, []string{"FLOAT8", "FLOAT8"}, [][]any{{float64(99), float64(77)}}),
			geometryMeasureSelect(b, "SELECT pg_catalog.width(box '(4,6),(1,2)') AS w,pg_catalog.slope(point '(0,0)',point '(2,1)') AS s", []string{"w", "s"}, []string{"FLOAT8", "FLOAT8"}, [][]any{{float64(3), float64(0.5)}}),
			geometryMeasureSelect(b, "SELECT w,h,d,s FROM geom_box_line_builtin", []string{"w", "h", "d", "s"}, []string{"FLOAT8", "FLOAT8", "LSEG", "FLOAT8"}, [][]any{{float64(3), float64(4), "[(4,6),(1,2)]", float64(0.5)}}),
		} {
			run(s)
			run(fixture.Prepared(s))
		}
		run(fixture.Exec("RESET search_path", 0))
	})
}
