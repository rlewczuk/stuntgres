package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryRelationClosest(t *testing.T) {
	geometryRelationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Candidate order, near-parallel lines and NaN are source-derived
		// expectations requiring the initial pinned oracle observation.
		geometryRelationEdges(t, p, b, c, []geometryRelationCase{
			{"##", "point", "lseg", grP, "[(2,0),(0,2)]", "POINT", "(1,1)"},
			{"##", "point", "lseg", "(4,1)", grS, "POINT", "(2,0)"},
			{"##", "point", "lseg", "(-1,1)", grS, "POINT", "(0,0)"},
			{"##", "point", "lseg", grR, "[(2,0),(0,0)]", "POINT", "(1,0)"},
			{"##", "point", "lseg", grP, "[(2,2),(2,2)]", "POINT", "(2,2)"},
			{"##", "point", "box", "(0.5,1.5)", grB, "POINT", "(0.5,1.5)"},
			{"##", "point", "box", "(-1,1)", grB, "POINT", "(0,1)"},
			{"##", "point", "box", "(3,3)", grB, "POINT", "(2,2)"},
			{"##", "point", "line", grR, "{1,0,-3}", "POINT", "(3,1)"},
			{"##", "lseg", "box", grV, grB, "POINT", "(1,1)"},
			{"##", "lseg", "box", grU, grB, "POINT", "(0,2)"},
			{"##", "lseg", "box", "[(3,3),(4,4)]", grB, "POINT", "(2,2)"},
			{"##", "lseg", "lseg", grS, grT, "POINT", "(1,0)"},
			{"##", "lseg", "lseg", grS, grU, "POINT", nil},
			{"##", "lseg", "lseg", grS, "[(1,0),(3,0)]", "POINT", nil},
			{"##", "lseg", "lseg", grS, "[(3,1),(3,2)]", "POINT", "(3,1)"},
			{"##", "line", "lseg", grL, grT, "POINT", "(1,0)"},
			{"##", "line", "lseg", grL, grU, "POINT", nil},
			{"<->", "line", "lseg", grL, grU, "FLOAT8", float64(3)},
			// Pinned 18.6 returns NULL for both near-horizontal witnesses.
			{"##", "line", "lseg", grL, "[(0,1),(2,1.0000005)]", "POINT", nil},
			{"##", "line", "lseg", "{0,1,1e16}", "[(0,0),(2,0.0000005)]", "POINT", nil},
			{"##", "point", "line", "(NaN,0)", grL, "POINT", nil},
			{"##", "point", "lseg", "(NaN,0)", grS, "POINT", nil},
			{"##", "point", "box", "(NaN,0)", grB, "POINT", nil},
			{"##", "point", "lseg", "(-0,-0)", grS, "POINT", "(0,0)"},
		})
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, tc := range []struct{ q, want string }{
			{"SELECT (point '(NaN,0)' <-> lseg '[(0,0),(2,0)]')::text AS v", "NaN"},
			{"SELECT (point '(Infinity,NaN)' <-> point '(0,0)')::text AS v", "Infinity"},
		} {
			s := fixture.Select(tc.q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}})
			run(s)
			run(fixture.Prepared(s))
		}
		// Checked arithmetic errors must not poison the SAME held handle.
		for _, tc := range []struct {
			q, message string
			bad, good  []any
			typ        string
			want       any
		}{
			{"SELECT $1::point <-> $2::point AS v", "overflow", []any{"(1e308,0)", "(-1e308,0)"}, []any{grP, "(3,4)"}, "FLOAT8", float64(5)},
			{"SELECT $1::point <@ $2::line AS v", "underflow", []any{"(1e-200,1)", "{1e-200,1,0}"}, []any{grP, grL}, "BOOL", true},
		} {
			t.Run(tc.message, func(t *testing.T) {
				stmt, err := c.PrepareContext(p.Ctx, tc.q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}
				for _, prepared := range []bool{false, true} {
					run(boxError(tc.q, "22003", "value out of range: "+tc.message, "", "", prepared, true, tc.bad...))
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, boxError(tc.q, "22003", "value out of range: "+tc.message, "", "", false, true, tc.bad...))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, boxSelect(b, tc.q, []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}, tc.good...))
			})
		}
	})
}
