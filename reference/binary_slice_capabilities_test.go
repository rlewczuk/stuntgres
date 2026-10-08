//go:build reference

package reference_test

import "testing"

func TestReferenceBinarySliceCapabilityBoundaries(t *testing.T) {
	p := newReferencePair(t)
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
			if backend.pg || tc.name == "btrim" {
				step := referenceSelect(tc.q, []string{"v"}, []string{tc.typ}, [][]any{{tc.value}})
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(step))
			} else {
				step := referenceImplicitError(tc.q, referenceDiagnostic{Code: "0A000", Message: "text overloads of " + tc.name + " are unsupported", Position: "8"})
				step.selectRows = true
				step.errorStage = "execute"
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
				step.errorStage = "prepare"
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(step))
			}
		}
	}
}
