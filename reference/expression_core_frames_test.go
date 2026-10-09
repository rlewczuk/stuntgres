package reference_test

import "testing"

func TestReferenceExpressionCoreConcurrentFrames(t *testing.T) {
	p := expressionReferenceSetup(t)

	q := "SELECT id FROM t WHERE v=$1 ORDER BY id"
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
		for index, arg := range []any{1, 2, nil} {
			want := [][][]any{{{int64(1)}, {int64(4)}}, {{int64(3)}}, {}}[index]
			step := referenceSelect(q, []string{"id"}, []string{"INT4"}, want, arg)
			scalarRunStep(t, p.ctx, referenceHeldStatement{backend.db, held, q}, backend.recorder, step)
		}
		scalarConcurrentFrames(t, p, backend, held, q, 16, []string{"id"}, []string{"INT4"}, func(n int) ([]any, [][]any) {
			arg := 1
			want := [][]any{{int64(1)}, {int64(4)}}
			if n%2 != 0 {
				arg = 2
				want = [][]any{{int64(3)}}
			}
			return []any{arg}, want
		})
		if err := held.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
