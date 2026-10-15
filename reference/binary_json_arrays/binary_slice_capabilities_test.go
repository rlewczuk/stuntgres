package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBinarySliceCapabilityBoundaries(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := fixture.NewScenario(t)
			run := func(t *testing.T, ctx context.Context, x fixture.Executor, r *fixture.Recorder, s fixture.Step) {
				s.NonContext = nonContext
				fixture.RunStep(t, ctx, x, r, s)
			}
			for _, backend := range p.Backends() {
				for _, tc := range []struct {
					q, typ string
					value  any
				}{
					{"SELECT substring('abcdef' FROM 2 FOR 3) AS v", "TEXT", "bcd"},
					{"SELECT substring('abcdef','bcd') AS v", "TEXT", "bcd"},
					{"SELECT substr('abcdef',2,3) AS v", "TEXT", "bcd"},
					{"SELECT overlay('abcdef' PLACING 'XY' FROM 3) AS v", "TEXT", "abXYef"},
					{"SELECT position('cd' IN 'abcdef') AS v", "INT4", int64(3)},
					{"SELECT trim(' abc ') AS v", "TEXT", "abc"},
				} {
					step := fixture.Select(tc.q, []string{"v"}, []string{tc.typ}, [][]any{{tc.value}})
					run(t, p.Ctx, backend.DB, backend.Recorder, step)
					run(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(step))
				}
				// Positional and both matching substring overloads share the fixture.
				for _, q := range []string{`SELECT substring('abc','%','#') AS v`, `SELECT substring('abc' FROM '%' FOR '#') AS v`, `SELECT substring('abc' SIMILAR '%' ESCAPE '#') AS v`} {
					s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"abc"}})
					run(t, p.Ctx, backend.DB, backend.Recorder, s)
					run(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(s))
				}
			}
		})
	}
}
