package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func geometryAlignmentScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, backend, c)
		})
	}
}

func TestReferenceGeometryAlignment(t *testing.T) {
	for _, tc := range []struct {
		name, typ, op string
		unary         bool
		yes, no       []any
	}{
		{"line_horizontal", "line", "?-", true, []any{"{0,1,3}"}, []any{"{1,1,3}"}},
		{"line_vertical", "line", "?|", true, []any{"{1,0,3}"}, []any{"{1,1,3}"}},
		{"lseg_horizontal", "lseg", "?-", true, []any{"[(0,0),(4,0)]"}, []any{"[(0,0),(4,4)]"}},
		{"lseg_vertical", "lseg", "?|", true, []any{"[(0,0),(0,4)]"}, []any{"[(0,0),(4,4)]"}},
		{"line_parallel", "line", "?||", false, []any{"{1,-1,0}", "{2,-2,4}"}, []any{"{1,-1,0}", "{1,1,7}"}},
		{"line_perpendicular", "line", "?-|", false, []any{"{1,-1,0}", "{1,1,7}"}, []any{"{1,-1,0}", "{1,-1,0}"}},
		{"lseg_parallel", "lseg", "?||", false, []any{"[(0,0),(2,2)]", "[(4,5),(6,7)]"}, []any{"[(0,0),(2,2)]", "[(0,0),(2,-2)]"}},
		{"lseg_perpendicular", "lseg", "?-|", false, []any{"[(0,0),(2,2)]", "[(0,0),(2,-2)]"}, []any{"[(0,0),(2,2)]", "[(4,5),(6,7)]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			geometryAlignmentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				q := "SELECT $1::" + tc.typ + " " + tc.op + " $2::" + tc.typ + " AS v"
				if tc.unary {
					q = "SELECT " + tc.op + " $1::" + tc.typ + " AS v"
				}
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				nulls := []any{nil}
				if !tc.unary {
					nulls = []any{tc.yes[0], nil}
				}
				for _, sample := range []struct {
					args []any
					want any
				}{{tc.yes, true}, {tc.no, false}, {nulls, nil}, {tc.yes, true}} {
					s := boxSelect(b, q, []string{"v"}, []string{"BOOL"}, [][]any{{sample.want}}, sample.args...)
					run(s)
					run(fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					operand := func(i int) string {
						if sample.args[i] == nil {
							return "NULL::" + tc.typ
						}
						return boxQuote(sample.args[i].(string)) + "::" + tc.typ
					}
					literal := tc.op + " " + operand(0)
					if !tc.unary {
						literal = operand(0) + " " + tc.op + " " + operand(1)
					}
					s = boxSelect(b, "SELECT "+literal+" AS v", []string{"v"}, []string{"BOOL"}, [][]any{{sample.want}})
					run(s)
					run(fixture.Prepared(s))
				}
				if !tc.unary {
					for _, args := range [][]any{{nil, tc.yes[1]}, {nil, nil}} {
						s := boxSelect(b, q, []string{"v"}, []string{"BOOL"}, [][]any{{nil}}, args...)
						run(s)
						run(fixture.Prepared(s))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					}
				}
			})
		})
	}
	t.Run("boundaries", func(t *testing.T) {
		geometryAlignmentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range []struct {
				expr string
				want bool
			}{
				{"?- line '{0.000001,1,0}'", true}, {"?- line '{0.001,1000,0}'", false},
				{"?- line '{NaN,1,0}'", false}, {"?| line '{1,Infinity,0}'", false}, {"?- line '{0,1,NaN}'", true},
				{"?- lseg '[(0,0),(0,0)]'", true}, {"?| lseg '[(0,0),(0,0)]'", true},
				{"?- lseg '[(0,0),(0.000001,0.000001)]'", true}, {"?| lseg '[(0,0),(0.000001,0.000001)]'", true},
				{"?- lseg '[(0,0),(0.000002,0.000002)]'", false}, {"?| lseg '[(0,0),(0.000002,0.000002)]'", false},
				{"line '{1,-1,0}' ?|| line '{1,-1,0}'", true},
				{"line '{0,1,0}' ?-| line '{1,0,7}'", true}, {"line '{1,0,7}' ?-| line '{0,1,0}'", true},
				{"line '{0,1,0}' ?-| line '{0,1,7}'", false}, {"line '{1,0,0}' ?-| line '{1,0,7}'", false},
				{"lseg '[(0,0),(0,0)]' ?|| lseg '[(2,0),(2,4)]'", true},
				{"lseg '[(0,0),(0,0)]' ?-| lseg '[(0,2),(4,2)]'", true},
				{"lseg '[(0,0),(0,0)]' ?-| lseg '[(0,0),(0,0)]'", false},
				{"lseg '[(0,0),(1,0)]' ?|| lseg '[(0,0),(1,0.000001)]'", true},
				{"lseg '[(0,0),(1,0)]' ?|| lseg '[(0,0),(1,0.000002)]'", false},
				{"lseg '[(1,0.000001),(0,0)]' ?|| lseg '[(1,0),(0,0)]'", true},
				{"point '(NaN,1)' ?- point '(0,1.0000005)'", true},
				{"point '(1,NaN)' ?| point '(1.0000005,0)'", true},
				{"point '(0,NaN)' ?- point '(0,NaN)'", false},
				{"point '(Infinity,0)' ?| point '(Infinity,2)'", true},
			} {
				s := boxSelect(b, "SELECT "+tc.expr+" AS v", []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			}
		})
	})
}
