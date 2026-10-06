//go:build reference

package reference_test

import "testing"

func TestReferenceArithmeticRebasedBooleanPredicateGrouping(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,i INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,7),(2,0),(3,NULL)", 3))
	for _, step := range []referenceStep{
		referenceSelect("SELECT ((i+1)*2),((i+1)=8 AND NOT (i=0)),(i+1) IS NULL FROM t ORDER BY id", []string{"?column?", "?column?", "?column?"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(16), true, false}, {int64(2), false, false}, {nil, nil, true}}),
		referenceSelect("SELECT (i+1)*2 FROM t WHERE ((i+1)=8 OR (i=0)) AND NOT ((i+1) IS NULL) ORDER BY id", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(16)}, {int64(2)}}),
	} {
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}
