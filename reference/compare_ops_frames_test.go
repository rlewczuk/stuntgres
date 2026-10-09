package reference_test

import "testing"

func TestReferenceCompareOpsConcurrentFrames(t *testing.T) {
	p := compareReferenceSetup(t)

	q := "SELECT $1<i FROM t ORDER BY id"
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
		for _, arg := range []any{7, nil, 7} {
			want := [][]any{{true}, {false}, {nil}, {false}}
			if arg == nil {
				want = [][]any{{nil}, {nil}, {nil}, {nil}}
			}
			step := referenceSelect(q, []string{"?column?"}, []string{"BOOL"}, want, arg)
			scalarRunStep(t, p.ctx, referenceHeldStatement{backend.db, held, q}, backend.recorder, step)
		}
		scalarConcurrentFrames(t, p, backend, held, q, 8, []string{"?column?"}, []string{"BOOL"}, func(n int) ([]any, [][]any) { return []any{7}, [][]any{{true}, {false}, {nil}, {false}} })
		if err := held.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
