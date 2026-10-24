package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryPositionResolution(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "position_shadow")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
			// Range codecs and column predicates are established capabilities;
			// range typed literals and explicit casts have separate admission.
			run(fixture.Exec("CREATE TABLE position_resolution_ranges(a int4range,b int4range)", 0))
			run(fixture.Exec("INSERT INTO position_resolution_ranges VALUES('[1,2)','[3,4)')", 1))
			s := fixture.Select("SELECT 8 << 1 AS left_shift,8 >> 1 AS right_shift,B'101' << 1 AS bits,a << b AS ranges,inet '10.1.0.0/16' << inet '10.0.0.0/8' AS network FROM position_resolution_ranges", []string{"left_shift", "right_shift", "bits", "ranges", "network"}, []string{"INT4", "INT4", "BIT", "BOOL", "BOOL"}, [][]any{{int64(16), int64(4), "010", true, true}})
			run(s)
			run(fixture.Prepared(s))
			run(fixture.Exec("CREATE SCHEMA position_shadow", 0))
			run(fixture.Exec("CREATE FUNCTION position_shadow.box_left(box,box) RETURNS boolean LANGUAGE SQL AS 'SELECT false'", 0))
			run(fixture.Exec("CREATE OPERATOR position_shadow.<< (LEFTARG=box,RIGHTARG=box,FUNCTION=position_shadow.box_left)", 0))
			run(fixture.Exec("CREATE FUNCTION position_shadow.box_below(box,box) RETURNS boolean LANGUAGE SQL AS 'SELECT false'", 0))
			run(fixture.Exec("CREATE OPERATOR position_shadow.<<| (LEFTARG=box,RIGHTARG=box,FUNCTION=position_shadow.box_below)", 0))
			run(fixture.Exec("SET search_path=position_shadow,pg_catalog,public", 0))
			defer c.ExecContext(p.Ctx, "RESET search_path")
			s = fixture.Select("SELECT box '(1,1),(0,0)' << box '(4,4),(3,3)' AS custom,box '(1,1),(0,0)' OPERATOR(pg_catalog.<<) box '(4,4),(3,3)' AS builtin,polygon '((0,0))' << polygon '((3,3))' AS polygon,point '(0,0)' << point '(3,3)' AS point,circle '<(0,0),0.5>' << circle '<(3,3),0.5>' AS circle", []string{"custom", "builtin", "polygon", "point", "circle"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, true, true, true}})
			run(s)
			run(fixture.Prepared(s))
			s = fixture.Select("SELECT box '(1,1),(0,0)' <<| box '(4,4),(3,3)' AS custom,box '(1,1),(0,0)' OPERATOR(pg_catalog.<<|) box '(4,4),(3,3)' AS builtin", []string{"custom", "builtin"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}})
			run(s)
			run(fixture.Prepared(s))
			const q = "SELECT $1::box OPERATOR(position_shadow.<<) $2::box AS custom,$1::box OPERATOR(pg_catalog.<<) $2::box AS builtin"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			const verticalQuery = "SELECT $1::box OPERATOR(position_shadow.<<|) $2::box AS custom,$1::box OPERATOR(pg_catalog.<<|) $2::box AS builtin"
			verticalStmt, err := c.PrepareContext(p.Ctx, verticalQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer verticalStmt.Close()
			verticalHeld := fixture.HeldStatement{Executor: c, Statement: verticalStmt, SQL: verticalQuery}
			for _, searchPath := range []string{"pg_catalog,public", "position_shadow,pg_catalog,public"} {
				run(fixture.Exec("SET search_path="+searchPath, 0))
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(q, []string{"custom", "builtin"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}, "(1,1),(0,0)", "(4,4),(3,3)"))
				fixture.RunStep(t, p.Ctx, verticalHeld, backend.Recorder, fixture.Select(verticalQuery, []string{"custom", "builtin"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}, "(1,1),(0,0)", "(4,4),(3,3)"))
			}
		})
	}
}
