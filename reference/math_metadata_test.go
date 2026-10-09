package reference_test

import "testing"

func TestReferenceMathMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE m(n NUMERIC(8,4))", 0))
	for _, tc := range []struct {
		query string
		rows  [][]any
	}{
		{"SELECT round(n,4),trim_scale(n),scale(n),round(4) FROM m", nil},
		{"SELECT round(NULL,4),trim_scale(NULL),scale(NULL),round(NULL)", [][]any{{nil, nil, nil, nil}}},
	} {
		referenceArithmeticMetadata(t, p, referenceSelect(tc.query, []string{"round", "trim_scale", "scale", "round"}, []string{"NUMERIC", "NUMERIC", "INT4", "FLOAT8"}, tc.rows))
	}
}
