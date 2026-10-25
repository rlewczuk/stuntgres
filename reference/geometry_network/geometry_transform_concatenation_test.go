package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryTransformConcatenation(t *testing.T) {
	geometryTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		q := "SELECT $1::path + $2::path AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ a, z, want any }{
			{"[(0,0),(1,1)]", "[(1,1),(0,0)]", "[(0,0),(1,1),(1,1),(0,0)]"},
			{"[(1,1),(0,0)]", "[(0,0),(1,1)]", "[(1,1),(0,0),(0,0),(1,1)]"},
			{"[(0,0)]", "[(0,0)]", "[(0,0),(0,0)]"},
			{"((0,0),(1,1))", "[(1,1),(0,0)]", nil},
			{"[(0,0),(1,1)]", "((1,1),(0,0))", nil},
			{"((0,0),(1,1))", "((1,1),(0,0))", nil},
			{nil, "[(0,0)]", nil}, {"[(0,0)]", nil, nil}, {nil, nil, nil},
			{"[(2,3)]", "[(4,5)]", "[(2,3),(4,5)]"},
		} {
			s := geometryTransformSelect(b, q, []string{"v"}, []string{"PATH"}, [][]any{{tc.want}}, tc.a, tc.z)
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			a, z := "NULL::path", "NULL::path"
			if tc.a != nil {
				a = "path " + boxQuote(tc.a.(string))
			}
			if tc.z != nil {
				z = "path " + boxQuote(tc.z.(string))
			}
			s = geometryTransformSelect(b, "SELECT "+a+" + "+z+" AS v", []string{"v"}, []string{"PATH"}, [][]any{{tc.want}})
			run(s)
			run(fixture.Prepared(s))
		}
		run(fixture.Exec("CREATE TABLE geom_concat_empty(a path,b path)", 0))
		run(geometryTransformSelect(b, "SELECT a+b AS v FROM geom_concat_empty", []string{"v"}, []string{"PATH"}, nil))
		for _, tc := range []struct {
			expr string
			want any
		}{
			{"path '[(0,0)]' + '(1,2)'", nil},
			{"path '[(0,0)]' + point '(1,2)'", "[(1,2)]"},
			{"path '[(1,2)]' * '(0,2)'", "[(-4,2)]"},
		} {
			s := geometryTransformSelect(b, "SELECT "+tc.expr+" AS v", []string{"v"}, []string{"PATH"}, [][]any{{tc.want}})
			run(s)
			run(fixture.Prepared(s))
		}
		for _, q := range []string{
			"SELECT path '((0,0))' + (path '[(0,0)]' / point '(0,0)')",
			"SELECT NULL::point + (point '(1,2)' / point '(0,0)')",
		} {
			for _, prepared := range []bool{false, true} {
				run(boxError(q, "22012", "division by zero", "", "", prepared, true))
			}
		}
	})
}
