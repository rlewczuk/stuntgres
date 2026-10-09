package reference_test

import "testing"

func TestReferenceLogicalComparisonComposition(t *testing.T) {
	p := logicalReferenceSetup(t)
	for _, step := range []referenceStep{
		referenceSelect("SELECT id,id>=2 AND a=b,NOT id<4 OR a<>b FROM t ORDER BY id", []string{"id", "?column?", "?column?"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{
			{int64(1), false, false}, {int64(2), false, true}, {int64(3), nil, nil}, {int64(4), false, true}, {int64(5), true, true}, {int64(6), nil, true}, {int64(7), nil, true}, {int64(8), nil, true}, {int64(9), nil, true},
		}),
		referenceSelect("SELECT id FROM t WHERE id>=2 AND a=b ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(5)}}),
		referenceSelect("SELECT id FROM t WHERE NOT id<4 OR a<>b ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(4)}, {int64(5)}, {int64(6)}, {int64(7)}, {int64(8)}, {int64(9)}}),
	} {
		scalarStep(t, p, step)
		scalarStep(t, p, referencePrepared(step))
	}
}
