package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func geometryMeasureCreateConsumers(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE geom_measure_source(id integer NOT NULL,b box,p path,c circle,ab float8 GENERATED ALWAYS AS (pg_catalog.area(b)) STORED,ap float8 GENERATED ALWAYS AS (pg_catalog.area(p)) STORED,ac float8 GENERATED ALWAYS AS (pg_catalog.area(c)) STORED,bc point GENERATED ALWAYS AS (pg_catalog.center(b)) STORED,cc point GENERATED ALWAYS AS (pg_catalog.center(c)) STORED,d float8 GENERATED ALWAYS AS (pg_catalog.diameter(c)) STORED,r float8 GENERATED ALWAYS AS (pg_catalog.radius(c)) STORED,CONSTRAINT geom_measure_area CHECK(pg_catalog.area(b)<=100))", 0))
	run(fixture.Exec("CREATE TABLE geom_measure_defaults(id integer,v float8 DEFAULT pg_catalog.area(box '(2,2),(0,0)'))", 0))
	run(fixture.Exec("CREATE VIEW geom_measure_view AS SELECT id,pg_catalog.area(b) AS ab,pg_catalog.area(p) AS ap,pg_catalog.area(c) AS ac,pg_catalog.center(b) AS bc,pg_catalog.center(c) AS cc,pg_catalog.diameter(c) AS d,pg_catalog.radius(c) AS r FROM geom_measure_source", 0))
	run(fixture.Exec("CREATE VIEW geom_measure_nulls AS SELECT pg_catalog.area(NULL::path) AS ap,pg_catalog.area(NULL::box) AS ab,pg_catalog.center(NULL::circle) AS cc,pg_catalog.center(NULL::box) AS bc", 0))
	run(fixture.Exec("CREATE FUNCTION geom_measure_wrapper(box) RETURNS float8 LANGUAGE SQL AS 'SELECT pg_catalog.area($1)'", 0))
	run(fixture.Exec("INSERT INTO geom_measure_source(id,b,p,c) VALUES(1,box '(2,2),(0,0)',path '((0,0),(4,0),(0,3))',circle '<(3,-4),2>'),(2,box '(2,2),(0,0)',path '[(0,0),(4,0),(0,3)]',circle '<(3,-4),2>'),(3,NULL,NULL,NULL),(4,box '(0,0),(0,0)',path '((0,0))',circle '<(0,0),0>'),(5,NULL,NULL,circle '<(0.10000000000000002,5e-324),0>')", 5))
	run(fixture.Exec("INSERT INTO geom_measure_defaults(id) VALUES(1)", 1))
}

func geometryMeasureConsumerChecks(t *testing.T, b fixture.Backend, run func(fixture.Step)) {
	t.Helper()
	names := []string{"id", "ab", "ap", "ac", "bc", "cc", "d", "r"}
	types := []string{"INT4", "FLOAT8", "FLOAT8", "FLOAT8", "POINT", "POINT", "FLOAT8", "FLOAT8"}
	rows := [][]any{
		{int64(1), float64(4), float64(6), float64(12.566370614359172), "(1,1)", "(3,-4)", float64(4), float64(2)},
		{int64(2), float64(4), nil, float64(12.566370614359172), "(1,1)", "(3,-4)", float64(4), float64(2)},
		{int64(3), nil, nil, nil, nil, nil, nil, nil},
		{int64(4), float64(0), float64(0), float64(0), "(0,0)", "(0,0)", float64(0), float64(0)},
		{int64(5), nil, nil, float64(0), nil, "(0.10000000000000002,5e-324)", float64(0), float64(0)},
	}
	for _, s := range []fixture.Step{
		geometryMeasureSelect(b, "SELECT id,ab,ap,ac,bc,cc,d,r FROM geom_measure_source ORDER BY id", names, types, rows),
		geometryMeasureSelect(b, "SELECT id,ab,ap,ac,bc,cc,d,r FROM geom_measure_view ORDER BY id", names, types, rows),
		geometryMeasureSelect(b, "SELECT * FROM geom_measure_nulls", []string{"ap", "ab", "cc", "bc"}, []string{"FLOAT8", "FLOAT8", "POINT", "POINT"}, [][]any{{nil, nil, nil, nil}}),
		geometryMeasureSelect(b, "SELECT v FROM geom_measure_defaults WHERE id=1", []string{"v"}, []string{"FLOAT8"}, [][]any{{float64(4)}}),
		geometryMeasureSelect(b, "SELECT geom_measure_wrapper(b) AS v FROM geom_measure_source WHERE id=1", []string{"v"}, []string{"FLOAT8"}, [][]any{{float64(4)}}),
	} {
		run(s)
		run(fixture.Prepared(s))
	}
	// A rollback probe runs on every restored target too: definitions must still
	// execute for new rows, not merely retain previously materialized numbers.
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("INSERT INTO geom_measure_source(id,b,p,c) VALUES(80,box '(2,2),(0,0)',path '((0,0),(4,0),(0,3))',circle '<(3,-4),2>')", 1))
	run(fixture.Exec("INSERT INTO geom_measure_defaults(id) VALUES(80)", 1))
	run(geometryMeasureSelect(b, "SELECT id,ab,ap,ac,bc,cc,d,r FROM geom_measure_view WHERE id=80", names, types, [][]any{{int64(80), float64(4), float64(6), float64(12.566370614359172), "(1,1)", "(3,-4)", float64(4), float64(2)}}))
	run(geometryMeasureSelect(b, "SELECT v,geom_measure_wrapper(box '(3,2),(0,0)') AS wrapped FROM geom_measure_defaults WHERE id=80", []string{"v", "wrapped"}, []string{"FLOAT8", "FLOAT8"}, [][]any{{float64(4), float64(6)}}))
	run(fixture.Exec("ROLLBACK", 0))
	run(fixture.Exec("BEGIN", 0))
	d := fixture.ControlsReferenceDiagnostic("23514", "new row for relation \"geom_measure_source\" violates check constraint \"geom_measure_area\"")
	d.Schema, d.Table, d.Constraint = "public", "geom_measure_source", "geom_measure_area"
	d.Detail = "Failing row contains (81, (20,20),(0,0), null, null, 400, null, null, (10,10), null, null, null)."
	run(fixture.ErrorDiagnostic("INSERT INTO geom_measure_source(id,b) VALUES(81,box '(20,20),(0,0)')", d))
	run(fixture.Exec("ROLLBACK", 0))
	run(fixture.Select("SELECT count(*) AS n FROM geom_measure_source", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}))
}

func TestReferenceGeometryMeasureConsumers(t *testing.T) {
	geometryMeasureScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		geometryMeasureCreateConsumers(run)
		geometryMeasureConsumerChecks(t, b, run)
	})
}
