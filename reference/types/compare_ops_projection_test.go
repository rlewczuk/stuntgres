package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompareOpsProjectionCrossWidth(t *testing.T) {
	p := compareReferenceSetup(t)
	step := fixture.Select("SELECT i<j,i<>j,i!=j FROM t ORDER BY id", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}, {false, true, true}, {nil, nil, nil}, {false, false, false}})
	fixture.ScalarStep(t, p, step)
	fixture.ScalarStep(t, p, fixture.Prepared(step))
}
