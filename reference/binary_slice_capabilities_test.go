package reference_test

import (
	"context"
	"testing"
)

func TestReferenceBinarySliceCapabilityBoundaries(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := newReferenceScenario(t)
			run := func(t *testing.T, ctx context.Context, x referenceExecutor, r *referenceRecorder, s referenceStep) {
				s.nonContext = nonContext
				referenceRunStep(t, ctx, x, r, s)
			}
			for _, backend := range p.backends() {
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
					if backend.pg || tc.name == "btrim" || tc.name == "position" || tc.name == "overlay" {
						step := referenceSelect(tc.q, []string{"v"}, []string{tc.typ}, [][]any{{tc.value}})
						run(t, p.ctx, backend.db, backend.recorder, step)
						run(t, p.ctx, backend.db, backend.recorder, referencePrepared(step))
					} else {
						step := referenceImplicitError(tc.q, referenceDiagnostic{Code: "0A000", Message: "text overloads of " + tc.name + " are unsupported", Position: "8"})
						step.selectRows = true
						step.errorStage = "execute"
						run(t, p.ctx, backend.db, backend.recorder, step)
						step.errorStage = "prepare"
						run(t, p.ctx, backend.db, backend.recorder, referencePrepared(step))
					}
				}
				// Only the three-TEXT SQL-regex overload crosses this boundary;
				// positional TEXT and the two-argument POSIX overload stay above.
				for _, q := range []string{`SELECT substring('abc','%','#') AS v`, `SELECT substring('abc' FROM '%' FOR '#') AS v`, `SELECT substring('abc' SIMILAR '%' ESCAPE '#') AS v`} {
					s := referenceSelect(q, []string{"v"}, []string{"TEXT"}, [][]any{{"abc"}})
					run(t, p.ctx, backend.db, backend.recorder, s)
					run(t, p.ctx, backend.db, backend.recorder, referencePrepared(s))
				}
			}
		})
	}
}
