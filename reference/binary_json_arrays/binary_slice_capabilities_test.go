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
					q, name, typ string
					value        any
				}{
					{"SELECT substring('abcdef' FROM 2 FOR 3) AS v", "substring", "TEXT", "bcd"},
					{"SELECT substring('abcdef','bcd') AS v", "substring", "TEXT", "bcd"},
					{"SELECT substr('abcdef',2,3) AS v", "substr", "TEXT", "bcd"},
					{"SELECT overlay('abcdef' PLACING 'XY' FROM 3) AS v", "overlay", "TEXT", "abXYef"},
					{"SELECT position('cd' IN 'abcdef') AS v", "position", "INT4", int64(3)},
					{"SELECT trim(' abc ') AS v", "btrim", "TEXT", "abc"},
				} {
					if backend.PG || tc.name == "btrim" || tc.name == "position" || tc.name == "overlay" {
						step := fixture.Select(tc.q, []string{"v"}, []string{tc.typ}, [][]any{{tc.value}})
						run(t, p.Ctx, backend.DB, backend.Recorder, step)
						run(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(step))
					} else {
						step := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "0A000", Message: "text overloads of " + tc.name + " are unsupported", Position: "8"})
						step.SelectRows = true
						step.ErrorStage = "execute"
						run(t, p.Ctx, backend.DB, backend.Recorder, step)
						step.ErrorStage = "prepare"
						run(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(step))
					}
				}
				// Only the three-TEXT SQL-regex overload crosses this boundary;
				// positional TEXT and the two-argument POSIX overload stay above.
				for _, q := range []string{`SELECT substring('abc','%','#') AS v`, `SELECT substring('abc' FROM '%' FOR '#') AS v`, `SELECT substring('abc' SIMILAR '%' ESCAPE '#') AS v`} {
					s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"abc"}})
					run(t, p.Ctx, backend.DB, backend.Recorder, s)
					run(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(s))
				}
			}
		})
	}
}
