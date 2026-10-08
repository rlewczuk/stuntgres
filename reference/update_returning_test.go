//go:build reference

package reference_test

import "testing"

func TestReferenceUpdateReturningVersions(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT NOT NULL,a INT,b INT,v TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,10,20,'one'),(2,30,40,NULL),(2,30,40,NULL)", 3))
	q := "UPDATE t SET a=b,b=a WHERE id=$1 RETURNING id,a,b,old.a,new.a,new.a-old.a AS delta,v"
	s := referenceSelect(q, []string{"id", "a", "b", "a", "a", "delta", "v"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(2), int64(40), int64(30), int64(30), int64(40), int64(10), nil}, {int64(2), int64(40), int64(30), int64(30), int64(40), int64(10), nil}}, int64(2))
	s.bag = true
	p.step(t, s)
	p.step(t, referenceExec("UPDATE t SET a=b,b=a WHERE id=2", 2))
	p.step(t, referencePrepared(s))
	s = referenceSelect(q, []string{"id", "a", "b", "a", "a", "delta", "v"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "TEXT"}, [][]any{}, int64(99))
	p.step(t, referencePrepared(s))
	p.step(t, referencePrepared(referenceSelect("UPDATE t SET a=a+1 WHERE id=1 RETURNING WITH (OLD AS o,NEW AS n) o.a,n.a,t.a", []string{"a", "a", "a"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(10), int64(11), int64(11)}})))
	p.step(t, referenceExec("UPDATE t SET a=a RETURNING old.a,new.a", 3))
	s = referenceSelect("SELECT id,a,b,v FROM t", []string{"id", "a", "b", "v"}, []string{"INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(11), int64(20), "one"}, {int64(2), int64(40), int64(30), nil}, {int64(2), int64(40), int64(30), nil}})
	s.bag = true
	p.step(t, s)
}
