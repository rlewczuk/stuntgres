package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryMeasureRoutines(t *testing.T) {
	geometryMeasureScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		q := "SELECT oid::int4 AS id,oid::regprocedure::text AS signature,pronamespace::int4 AS ns,proowner::int4 AS owner,pronargs::int4 AS nargs,prorettype::int4 AS result,provolatile::text AS volatility,proisstrict AS strict,proparallel::text AS parallel,prokind::text AS kind FROM pg_proc WHERE oid IN (975,979,1468,1469,1470,1542,1543) ORDER BY oid"
		s := fixture.Select(q, []string{"id", "signature", "ns", "owner", "nargs", "result", "volatility", "strict", "parallel", "kind"}, []string{"INT4", "TEXT", "INT4", "INT4", "INT4", "INT4", "TEXT", "BOOL", "TEXT", "TEXT"}, [][]any{
			{int64(975), "area(box)", int64(11), int64(10), int64(1), int64(701), "i", true, "s", "f"},
			{int64(979), "area(path)", int64(11), int64(10), int64(1), int64(701), "i", true, "s", "f"},
			{int64(1468), "area(circle)", int64(11), int64(10), int64(1), int64(701), "i", true, "s", "f"},
			{int64(1469), "diameter(circle)", int64(11), int64(10), int64(1), int64(701), "i", true, "s", "f"},
			{int64(1470), "radius(circle)", int64(11), int64(10), int64(1), int64(701), "i", true, "s", "f"},
			{int64(1542), "center(box)", int64(11), int64(10), int64(1), int64(600), "i", true, "s", "f"},
			{int64(1543), "center(circle)", int64(11), int64(10), int64(1), int64(600), "i", true, "s", "f"},
		})
		run(s)
		run(fixture.Prepared(s))
		run(fixture.Exec("CREATE VIEW geom_measure_builtin AS SELECT pg_catalog.area(box '(2,2),(0,0)') AS a,pg_catalog.center(circle '<(3,-4),2>') AS c", 0))
		run(fixture.Exec("CREATE FUNCTION public.area(box) RETURNS float8 LANGUAGE SQL AS 'SELECT 99::float8'", 0))
		run(fixture.Exec("CREATE FUNCTION public.center(circle) RETURNS point LANGUAGE SQL AS 'SELECT point ''(9,9)'''", 0))
		run(fixture.Exec("SET search_path=public,pg_catalog", 0))
		for _, s := range []fixture.Step{
			geometryMeasureSelect(b, "SELECT area(box '(2,2),(0,0)') AS v", []string{"v"}, []string{"FLOAT8"}, [][]any{{float64(99)}}),
			geometryMeasureSelect(b, "SELECT center(circle '<(3,-4),2>') AS v", []string{"v"}, []string{"POINT"}, [][]any{{"(9,9)"}}),
			geometryMeasureSelect(b, "SELECT pg_catalog.area(box '(2,2),(0,0)') AS v", []string{"v"}, []string{"FLOAT8"}, [][]any{{float64(4)}}),
			geometryMeasureSelect(b, "SELECT a,c FROM geom_measure_builtin", []string{"a", "c"}, []string{"FLOAT8", "POINT"}, [][]any{{float64(4), "(3,-4)"}}),
		} {
			run(s)
			run(fixture.Prepared(s))
		}
		run(fixture.Exec("RESET search_path", 0))
	})
}
