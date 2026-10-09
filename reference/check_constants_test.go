package reference_test

import "testing"

func TestReferenceCheckScalarConstants(t *testing.T) {
	for _, expr := range []string{"NULL", "TRUE", "'true'"} {
		t.Run(expr, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					referenceCheckRun(t, p.ctx, b.db, b.recorder, referenceExec("CREATE TABLE t(a INT CHECK("+expr+"))", 0))
					referenceCheckRun(t, p.ctx, b.db, b.recorder, referenceExec("INSERT INTO t VALUES(NULL),(1),(2)", 3))
				})
			}
		})
	}
}
