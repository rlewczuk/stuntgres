package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func geometryMeasureBoxLineCreateConsumers(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE geom_box_line_source(id integer,b box,p point,q point,w float8 GENERATED ALWAYS AS (pg_catalog.width(b)) STORED,h float8 GENERATED ALWAYS AS (pg_catalog.height(b)) STORED,d lseg GENERATED ALWAYS AS (pg_catalog.diagonal(b)) STORED,s float8 GENERATED ALWAYS AS (pg_catalog.slope(p,q)) STORED,CONSTRAINT geom_box_line_width CHECK(pg_catalog.width(b)<=100),CONSTRAINT geom_box_line_slope CHECK(pg_catalog.slope(p,q)>=0))", 0))
	run(fixture.Exec("CREATE TABLE geom_box_line_defaults(id integer,v float8 DEFAULT pg_catalog.width(box '(4,6),(1,2)'))", 0))
	run(fixture.Exec("CREATE VIEW geom_box_line_view AS SELECT id,pg_catalog.width(b) AS w,pg_catalog.height(b) AS h,pg_catalog.diagonal(b) AS d,pg_catalog.slope(p,q) AS s FROM geom_box_line_source", 0))
	run(fixture.Exec("CREATE VIEW geom_box_line_constants AS SELECT pg_catalog.diagonal(NULL::box) AS d,pg_catalog.slope(NULL::point,point '(2,1)') AS s,pg_catalog.diagonal(box '(4,6),(1,2)') AS literal", 0))
	run(fixture.Exec("CREATE FUNCTION geom_box_line_diagonal(box) RETURNS lseg LANGUAGE SQL AS 'SELECT pg_catalog.diagonal($1)'", 0))
	run(fixture.Exec("CREATE FUNCTION geom_box_line_slope(point,point) RETURNS float8 LANGUAGE SQL AS 'SELECT pg_catalog.slope($1,$2)'", 0))
	run(fixture.Exec("INSERT INTO geom_box_line_source(id,b,p,q) VALUES(1,box '(4,6),(1,2)',point '(0,0)',point '(2,1)'),(2,NULL,NULL,NULL),(3,box '(1,2),(1,2)',point '(1,0)',point '(1,2)'),(4,box '(4,6),(1,2)',point '(0,2)',point '(3,2)')", 4))
	run(fixture.Exec("INSERT INTO geom_box_line_defaults(id) VALUES(1)", 1))
}

func geometryMeasureBoxLineConsumerChecks(t *testing.T, b fixture.Backend, run func(fixture.Step)) {
	t.Helper()
	names := []string{"id", "w", "h", "d", "s"}
	types := []string{"INT4", "FLOAT8", "FLOAT8", "LSEG", "TEXT"}
	wants := [][]any{
		{int64(1), float64(3), float64(4), "[(4,6),(1,2)]", "0.5"},
		{int64(2), nil, nil, nil, nil},
		{int64(3), float64(0), float64(0), "[(1,2),(1,2)]", "Infinity"},
		{int64(4), float64(3), float64(4), "[(4,6),(1,2)]", "0"},
	}
	for _, s := range []fixture.Step{
		geometryMeasureSelect(b, "SELECT id,w,h,d,s::text AS s FROM geom_box_line_source ORDER BY id", names, types, wants),
		geometryMeasureSelect(b, "SELECT id,w,h,d,s::text AS s FROM geom_box_line_view ORDER BY id", names, types, wants),
		geometryMeasureSelect(b, "SELECT * FROM geom_box_line_constants", []string{"d", "s", "literal"}, []string{"LSEG", "FLOAT8", "LSEG"}, [][]any{{nil, nil, "[(4,6),(1,2)]"}}),
		geometryMeasureSelect(b, "SELECT v FROM geom_box_line_defaults WHERE id=1", []string{"v"}, []string{"FLOAT8"}, [][]any{{float64(3)}}),
		geometryMeasureSelect(b, "SELECT geom_box_line_diagonal(b) AS d,geom_box_line_slope(p,q) AS s FROM geom_box_line_source WHERE id=1", []string{"d", "s"}, []string{"LSEG", "FLOAT8"}, [][]any{{"[(4,6),(1,2)]", float64(0.5)}}),
	} {
		run(s)
		run(fixture.Prepared(s))
	}
	// Every restored target must execute its definitions on new inputs.
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("INSERT INTO geom_box_line_source(id,b,p,q) VALUES(80,box '(8,9),(2,1)',point '(0,0)',point '(4,3)')", 1))
	run(fixture.Prepared(fixture.Exec("INSERT INTO geom_box_line_defaults(id) VALUES($1)", 1, 80)))
	s := geometryMeasureSelect(b, "SELECT id,w,h,d,s::text AS s FROM geom_box_line_view WHERE id=80", names, types, [][]any{{int64(80), float64(6), float64(8), "[(8,9),(2,1)]", "0.75"}})
	run(s)
	run(fixture.Prepared(s))
	run(geometryMeasureSelect(b, "SELECT v,geom_box_line_diagonal(box '(8,9),(2,1)') AS d,geom_box_line_slope(point '(0,0)',point '(4,3)') AS s FROM geom_box_line_defaults WHERE id=80", []string{"v", "d", "s"}, []string{"FLOAT8", "LSEG", "FLOAT8"}, [][]any{{float64(3), "[(8,9),(2,1)]", float64(0.75)}}))
	run(fixture.Exec("ROLLBACK", 0))
	for _, tc := range []struct{ query, constraint, detail string }{
		{"INSERT INTO geom_box_line_source(id,b) VALUES(81,box '(200,2),(0,0)')", "geom_box_line_width", "Failing row contains (81, (200,2),(0,0), null, null, 200, 2, [(200,2),(0,0)], null)."},
		{"INSERT INTO geom_box_line_source(id,p,q) VALUES(82,point '(0,1)',point '(2,0)')", "geom_box_line_slope", "Failing row contains (82, null, (0,1), (2,0), null, null, null, -0.5)."},
	} {
		run(fixture.Exec("BEGIN", 0))
		d := fixture.ControlsReferenceDiagnostic("23514", "new row for relation \"geom_box_line_source\" violates check constraint \""+tc.constraint+"\"")
		d.Schema, d.Table, d.Constraint, d.Detail = "public", "geom_box_line_source", tc.constraint, tc.detail
		run(fixture.ErrorDiagnostic(tc.query, d))
		run(fixture.Exec("ROLLBACK", 0))
	}
	run(fixture.Select("SELECT count(*) AS n FROM geom_box_line_source", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
}

func TestReferenceGeometryMeasureBoxLineConsumers(t *testing.T) {
	geometryMeasureScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		geometryMeasureBoxLineCreateConsumers(run)
		geometryMeasureBoxLineConsumerChecks(t, b, run)
	})
}
