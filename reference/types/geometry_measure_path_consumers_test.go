package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func geometryMeasurePathCreateConsumers(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE geom_measure_path_source(id integer,p path DEFAULT pg_catalog.pclose(path '[(0,0),(4,0),(0,3)]'),g float8 GENERATED ALWAYS AS (pg_catalog.length(pg_catalog.pclose(p))) STORED,CONSTRAINT geom_measure_path_limit CHECK(pg_catalog.npoints(p)<=4 AND pg_catalog.isclosed(pg_catalog.pclose(p)) AND pg_catalog.isopen(pg_catalog.popen(p)) AND pg_catalog.length(pg_catalog.popen(p))<=100))", 0))
	run(fixture.Exec("CREATE VIEW geom_measure_path_view AS SELECT id,pg_catalog.isopen(p) AS o,pg_catalog.isclosed(p) AS c,pg_catalog.npoints(p) AS n,pg_catalog.pclose(p) AS closed,pg_catalog.popen(p) AS opened,g,pg_catalog.length(pg_catalog.popen(p)) AS l,pg_catalog.area(pg_catalog.pclose(p)) AS a FROM geom_measure_path_source", 0))
	var literals, nulls []string
	for _, tc := range geometryMeasurePathCases {
		literals = append(literals, "pg_catalog."+tc.function+"("+tc.formal+" '"+tc.input+"') AS "+tc.name)
		nulls = append(nulls, "pg_catalog."+tc.function+"(NULL::"+tc.formal+") AS "+tc.name)
		result := map[string]string{"BOOL": "boolean", "PATH": "path", "FLOAT8": "float8", "INT4": "integer"}[tc.typ]
		run(fixture.Exec("CREATE FUNCTION geom_measure_path_"+tc.name+"("+tc.formal+") RETURNS "+result+" LANGUAGE SQL AS 'SELECT pg_catalog."+tc.function+"($1)'", 0))
	}
	run(fixture.Exec("CREATE VIEW geom_measure_path_literals AS SELECT "+strings.Join(literals, ","), 0))
	run(fixture.Exec("CREATE VIEW geom_measure_path_nulls AS SELECT "+strings.Join(nulls, ","), 0))
	run(fixture.Exec("INSERT INTO geom_measure_path_source(id,p) VALUES(1,path '[(0,0),(4,0),(0,3)]'),(2,NULL)", 2))
	run(fixture.Exec("INSERT INTO geom_measure_path_source(id) VALUES(3)", 1))
}

func geometryMeasurePathConsumerChecks(t *testing.T, b fixture.Backend, run func(fixture.Step)) {
	t.Helper()
	names := []string{"id", "o", "c", "n", "closed", "opened", "g", "l", "a"}
	types := []string{"INT4", "BOOL", "BOOL", "INT4", "PATH", "PATH", "FLOAT8", "FLOAT8", "FLOAT8"}
	rows := [][]any{
		{int64(1), true, false, int64(3), "((0,0),(4,0),(0,3))", "[(0,0),(4,0),(0,3)]", float64(12), float64(9), float64(6)},
		{int64(2), nil, nil, nil, nil, nil, nil, nil, nil},
		{int64(3), false, true, int64(3), "((0,0),(4,0),(0,3))", "[(0,0),(4,0),(0,3)]", float64(12), float64(9), float64(6)},
	}
	s := geometryMeasurePathStoredSelect(b, "SELECT id,o,c,n,closed,opened,g,l,a FROM geom_measure_path_view ORDER BY id", names, types, rows)
	run(s)
	run(fixture.Prepared(s))
	var matrixNames, matrixTypes []string
	var literalValues, nullValues []any
	for _, tc := range geometryMeasurePathCases {
		matrixNames = append(matrixNames, tc.name)
		matrixTypes = append(matrixTypes, tc.typ)
		literalValues = append(literalValues, tc.want)
		nullValues = append(nullValues, nil)
		for _, operand := range []string{tc.formal + " '" + tc.input + "'", "NULL::" + tc.formal} {
			want := tc.want
			if operand == "NULL::"+tc.formal {
				want = nil
			}
			s := geometryMeasurePathSelect(b, "SELECT geom_measure_path_"+tc.name+"("+operand+") AS v", []string{"v"}, []string{tc.typ}, [][]any{{want}})
			run(s)
			run(fixture.Prepared(s))
		}
	}
	for _, tc := range []struct {
		view   string
		values []any
	}{{"literals", literalValues}, {"nulls", nullValues}} {
		s := geometryMeasurePathStoredSelect(b, "SELECT "+strings.Join(matrixNames, ",")+" FROM geom_measure_path_"+tc.view, matrixNames, matrixTypes, [][]any{tc.values})
		run(s)
		run(fixture.Prepared(s))
	}
	// Every restored target must execute newly written defaults and generated values.
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("INSERT INTO geom_measure_path_source(id) VALUES(80)", 1))
	run(fixture.Exec("INSERT INTO geom_measure_path_source(id,p) VALUES(81,path '[(0,0),(3,4)]')", 1))
	run(geometryMeasurePathStoredSelect(b, "SELECT id,o,c,n,closed,opened,g,l,a FROM geom_measure_path_view WHERE id>=80 ORDER BY id", names, types, [][]any{
		{int64(80), false, true, int64(3), "((0,0),(4,0),(0,3))", "[(0,0),(4,0),(0,3)]", float64(12), float64(9), float64(6)},
		{int64(81), true, false, int64(2), "((0,0),(3,4))", "[(0,0),(3,4)]", float64(10), float64(5), float64(0)},
	}))
	run(fixture.Exec("ROLLBACK", 0))
	d := fixture.ControlsReferenceDiagnostic("23514", "new row for relation \"geom_measure_path_source\" violates check constraint \"geom_measure_path_limit\"")
	d.Schema, d.Table, d.Constraint = "public", "geom_measure_path_source", "geom_measure_path_limit"
	d.Detail = "Failing row contains (82, [(0,0),(0,0),(0,0),(0,0),(0,0)], 0)."
	run(fixture.Exec("BEGIN", 0))
	run(fixture.ErrorDiagnostic("INSERT INTO geom_measure_path_source(id,p) VALUES(82,path '[(0,0),(0,0),(0,0),(0,0),(0,0)]')", d))
	run(fixture.Exec("ROLLBACK", 0))
	run(fixture.Select("SELECT count(*) AS n FROM geom_measure_path_source", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
}

func TestReferenceGeometryMeasurePathConsumers(t *testing.T) {
	geometryMeasurePathScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		geometryMeasurePathCreateConsumers(run)
		geometryMeasurePathConsumerChecks(t, b, run)
	})
}
