//go:build reference

package reference_test

import "testing"

func TestReferenceIdentifierBaselineCastCalls(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(x INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(7)", 1))
	for _, tc := range []struct {
		q            string
		names, types []string
		row          []any
	}{
		{"SELECT int4(x) FROM t", []string{"int4"}, []string{"INT4"}, []any{int64(7)}},
		{"SELECT text(x) FROM t", []string{"text"}, []string{"TEXT"}, []any{"7"}},
		{"SELECT CAST(x AS INT) FROM t", []string{"x"}, []string{"INT4"}, []any{int64(7)}},
		{"SELECT x, CAST(x AS INT) FROM t", []string{"x", "x"}, []string{"INT4", "INT4"}, []any{int64(7), int64(7)}},
	} {
		for _, prepared := range []bool{false, true} {
			step := referenceSelect(tc.q, tc.names, tc.types, [][]any{tc.row})
			step.prepared = prepared
			p.step(t, step)
		}
	}
}
