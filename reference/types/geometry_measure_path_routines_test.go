package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryMeasurePathRoutines(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "geom_measure_path_shadow")
	geometryMeasurePathScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		q := "SELECT oid::int4 AS id,oid::regprocedure::text AS signature,pronamespace::int4 AS ns,proowner::int4 AS owner,pronargs::int4 AS nargs,prorettype::int4 AS result,provolatile::text AS volatility,proisstrict AS strict,proparallel::text AS parallel,prokind::text AS kind FROM pg_proc WHERE oid IN (1430,1431,1433,1434,1530,1531,1545,1556) ORDER BY oid"
		s := fixture.Select(q, []string{"id", "signature", "ns", "owner", "nargs", "result", "volatility", "strict", "parallel", "kind"}, []string{"INT4", "TEXT", "INT4", "INT4", "INT4", "INT4", "TEXT", "BOOL", "TEXT", "TEXT"}, [][]any{
			{int64(1430), "isclosed(path)", int64(11), int64(10), int64(1), int64(16), "i", true, "s", "f"},
			{int64(1431), "isopen(path)", int64(11), int64(10), int64(1), int64(16), "i", true, "s", "f"},
			{int64(1433), "pclose(path)", int64(11), int64(10), int64(1), int64(602), "i", true, "s", "f"},
			{int64(1434), "popen(path)", int64(11), int64(10), int64(1), int64(602), "i", true, "s", "f"},
			{int64(1530), "length(lseg)", int64(11), int64(10), int64(1), int64(701), "i", true, "s", "f"},
			{int64(1531), "length(path)", int64(11), int64(10), int64(1), int64(701), "i", true, "s", "f"},
			{int64(1545), "npoints(path)", int64(11), int64(10), int64(1), int64(23), "i", true, "s", "f"},
			{int64(1556), "npoints(polygon)", int64(11), int64(10), int64(1), int64(23), "i", true, "s", "f"},
		})
		run(s)
		run(fixture.Prepared(s))
		// Capture selected builtin authority before introducing exact-signature shadows.
		run(fixture.Exec("CREATE VIEW geom_measure_path_authority AS SELECT pg_catalog.length(path '[(0,0),(4,0),(0,3)]') AS l,pg_catalog.isopen(path '[(0,0)]') AS o,pg_catalog.npoints(polygon '((0,0),(1,0),(0,1))') AS n", 0))
		run(fixture.Exec("CREATE SCHEMA geom_measure_path_shadow", 0))
		run(fixture.Exec("CREATE FUNCTION geom_measure_path_shadow.length(path) RETURNS float8 LANGUAGE SQL AS 'SELECT 99::float8'", 0))
		run(fixture.Exec("CREATE FUNCTION geom_measure_path_shadow.isopen(path) RETURNS boolean LANGUAGE SQL AS 'SELECT false'", 0))
		run(fixture.Exec("CREATE FUNCTION geom_measure_path_shadow.npoints(polygon) RETURNS integer LANGUAGE SQL AS 'SELECT 88'", 0))
		run(fixture.Exec("SET search_path=geom_measure_path_shadow,public,pg_catalog", 0))
		for _, s := range []fixture.Step{
			geometryMeasurePathSelect(b, "SELECT length(path '[(0,0),(4,0),(0,3)]') AS l,isopen(path '[(0,0)]') AS o,npoints(polygon '((0,0),(1,0),(0,1))') AS n", []string{"l", "o", "n"}, []string{"FLOAT8", "BOOL", "INT4"}, [][]any{{float64(99), false, int64(88)}}),
			geometryMeasurePathSelect(b, "SELECT pg_catalog.length(path '[(0,0),(4,0),(0,3)]') AS l,pg_catalog.isopen(path '[(0,0)]') AS o,pg_catalog.npoints(polygon '((0,0),(1,0),(0,1))') AS n", []string{"l", "o", "n"}, []string{"FLOAT8", "BOOL", "INT4"}, [][]any{{float64(9), true, int64(3)}}),
			geometryMeasurePathStoredSelect(b, "SELECT l,o,n FROM geom_measure_path_authority", []string{"l", "o", "n"}, []string{"FLOAT8", "BOOL", "INT4"}, [][]any{{float64(9), true, int64(3)}}),
			geometryMeasurePathSelect(b, "SELECT length('abc'::text) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}),
		} {
			run(s)
			run(fixture.Prepared(s))
		}
		run(fixture.Exec("RESET search_path", 0))
	})
}
