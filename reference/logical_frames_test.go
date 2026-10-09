package reference_test

import "testing"

func TestReferenceLogicalConcurrentPreparedFrames(t *testing.T) {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE t(id INT)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO t VALUES(1)", 1))
	q := "SELECT $1 AND $1,NOT $1 FROM t"
	for _, backend := range p.backends() {
		held, err := backend.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := held.Close(); err != nil {
				t.Error(err)
			}
		}()
		scalarConcurrentFrames(t, p, backend, held, q, 12, []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, func(n int) ([]any, [][]any) { arg := n%2 == 0; return []any{arg}, [][]any{{arg, !arg}} })
		if err := held.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
