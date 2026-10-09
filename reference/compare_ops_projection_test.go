package reference_test

import "testing"

func TestReferenceCompareOpsProjectionCrossWidth(t *testing.T) {
	p := compareReferenceSetup(t)
	step := referenceSelect("SELECT i<j,i<>j,i!=j FROM t ORDER BY id", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}, {false, true, true}, {nil, nil, nil}, {false, false, false}})
	scalarStep(t, p, step)
	scalarStep(t, p, referencePrepared(step))
}
