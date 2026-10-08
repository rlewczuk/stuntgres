//go:build reference

package reference_test

import "testing"

func TestReferenceBooleanAggregateHeldPrepared(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(b BOOL)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(TRUE),(NULL)", 2))
	const q = "SELECT bool_and(b),bool_or(b),every(b) FROM t"
	native, err := p.memory.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	oracle, err := p.postgres.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer oracle.Close()
	check := func(want [][]any) {
		t.Helper()
		s := referenceSelect(q, []string{"bool_and", "bool_or", "every"}, []string{"BOOL", "BOOL", "BOOL"}, want)
		referenceRunStep(t, p.ctx, referenceHeldStatement{p.memory, native, q}, p.memoryRecord, s)
		referenceRunStep(t, p.ctx, referenceHeldStatement{p.postgres, oracle, q}, p.postgresRecord, s)
	}
	check([][]any{{true, true, true}})
	p.step(t, referenceExec("INSERT INTO t VALUES(FALSE)", 1))
	check([][]any{{false, true, false}})
	p.step(t, referenceExec("CREATE SEQUENCE s", 0))
	s := referenceSelect("SELECT bool_and(nextval('s')>0),every(nextval('s')>0),bool_or(nextval('s')>0) FROM t", []string{"bool_and", "every", "bool_or"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}})
	p.step(t, s)
	p.step(t, referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(9)}}))
	p.step(t, referencePrepared(s))
	p.step(t, referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(18)}}))
}
