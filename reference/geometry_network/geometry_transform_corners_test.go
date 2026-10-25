package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryTransformCorners(t *testing.T) {
	geometryTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, tc := range []struct{ expr, want string }{
			{"box '(2,1),(0,0)' * point '(1,1)'", "(1,3),(0,0)"},
			{"box '(2,1),(0,0)' * point '(0,1)'", "(0,2),(-1,0)"},
		} {
			s := geometryTransformSelect(b, "SELECT "+tc.expr+" AS v", []string{"v"}, []string{"BOX"}, [][]any{{tc.want}})
			run(s)
			run(fixture.Prepared(s))
		}
		run(fixture.Exec("CREATE TABLE geom_transform_corners(b box)", 0))
		run(fixture.Exec("INSERT INTO geom_transform_corners VALUES('(3,4),(1,2)')", 1))
		run(fixture.Exec("UPDATE geom_transform_corners SET b[0]=point '(-5,-6)'", 1))
		retained, err := c.QueryContext(p.Ctx, "SELECT b AS v FROM geom_transform_corners")
		if err != nil {
			t.Fatal(err)
		}
		// Native rows own a snapshot; PostgreSQL's streaming connection must be drained.
		if b.PG {
			retained.Close()
		} else {
			defer retained.Close()
		}
		for _, tc := range []struct{ op, point, want string }{
			{"+", "(2,3)", "(-3,-3),(3,5)"}, {"-", "(2,3)", "(-7,-9),(-1,-1)"},
			{"*", "(1,0)", "(1,2),(-5,-6)"}, {"/", "(1,0)", "(1,2),(-5,-6)"},
		} {
			s := geometryTransformSelect(b, "SELECT b "+tc.op+" point "+boxQuote(tc.point)+" AS v FROM geom_transform_corners", []string{"v"}, []string{"BOX"}, [][]any{{tc.want}})
			run(s)
			run(fixture.Prepared(s))
			run(geometryTransformSelect(b, "SELECT b AS v FROM geom_transform_corners", []string{"v"}, []string{"BOX"}, [][]any{{"(-5,-6),(1,2)"}}))
		}
		if !b.PG {
			fixture.BoxMetadata(t, false, retained, []string{"BOX"}, nil)
			if !retained.Next() {
				t.Fatal("lost retained BOX row")
			}
			var got string
			if err := retained.Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != "(-5,-6),(1,2)" {
				t.Fatalf("retained BOX %s", got)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("invalid retained cursor")
			}
		}
	})
}
