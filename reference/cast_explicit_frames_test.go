package reference_test

import "testing"

func TestReferenceCastExplicitConcurrentFrames(t *testing.T) {
	p := castReferenceSetup(t)

	q := "SELECT CAST($1 AS bool) FROM t ORDER BY id"
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
		scalarConcurrentFrames(t, p, backend, held, q, 12, []string{"bool"}, []string{"BOOL"}, func(n int) ([]any, [][]any) {
			arg := []any{true, false, nil}[n%3]
			return []any{arg}, [][]any{{arg}, {arg}, {arg}}
		})
		if err := held.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
