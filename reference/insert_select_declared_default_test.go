//go:build reference

package reference_test

import "testing"

func TestReferenceInsertSelectDeclaredDefault(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(i INT,j INT DEFAULT 42)", 0))
	p.step(t, referenceExec("INSERT INTO t(i) SELECT 1", 1))
	p.step(t, referenceSelect("SELECT * FROM t", []string{"i", "j"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(42)}}))
}
