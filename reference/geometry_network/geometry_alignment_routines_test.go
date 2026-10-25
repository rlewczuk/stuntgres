package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryAlignmentRoutines(t *testing.T) {
	geometryAlignmentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		// Fixed REL_18_6 pg_proc declarations; metadata does not grant named-call
		// admission to these operator support routines.
		s := fixture.Select("SELECT oid::int4 AS id,'pg_catalog.' || oid::regprocedure::text AS signature,pronamespace::int4 AS ns,proowner::int4 AS owner,pronargs::int4 AS nargs,prorettype::int4 AS result,provolatile::text AS volatility,proisstrict AS strict,proparallel::text AS parallel,prokind::text AS kind FROM pg_proc WHERE oid IN (995,996,997,998,1496,1497,1498,1499) ORDER BY oid", []string{"id", "signature", "ns", "owner", "nargs", "result", "volatility", "strict", "parallel", "kind"}, []string{"INT4", "TEXT", "INT4", "INT4", "INT4", "INT4", "TEXT", "BOOL", "TEXT", "TEXT"}, [][]any{
			{int64(995), "pg_catalog.lseg_parallel(lseg,lseg)", int64(11), int64(10), int64(2), int64(16), "i", true, "s", "f"},
			{int64(996), "pg_catalog.lseg_perp(lseg,lseg)", int64(11), int64(10), int64(2), int64(16), "i", true, "s", "f"},
			{int64(997), "pg_catalog.lseg_vertical(lseg)", int64(11), int64(10), int64(1), int64(16), "i", true, "s", "f"},
			{int64(998), "pg_catalog.lseg_horizontal(lseg)", int64(11), int64(10), int64(1), int64(16), "i", true, "s", "f"},
			{int64(1496), "pg_catalog.line_parallel(line,line)", int64(11), int64(10), int64(2), int64(16), "i", true, "s", "f"},
			{int64(1497), "pg_catalog.line_perp(line,line)", int64(11), int64(10), int64(2), int64(16), "i", true, "s", "f"},
			{int64(1498), "pg_catalog.line_vertical(line)", int64(11), int64(10), int64(1), int64(16), "i", true, "s", "f"},
			{int64(1499), "pg_catalog.line_horizontal(line)", int64(11), int64(10), int64(1), int64(16), "i", true, "s", "f"},
		})
		run(s)
		run(fixture.Prepared(s))
	})
}
