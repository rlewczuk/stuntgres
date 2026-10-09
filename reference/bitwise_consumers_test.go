package reference_test

import "testing"

func TestReferenceBitwiseParametersAndConsumers(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INTEGER,s SMALLINT,i INTEGER,b BIGINT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,-1,1,15),(2,-8,2,15),(3,0,3,15),(4,NULL,NULL,NULL)", 4))
	{
		step := referenceSelect("SELECT s & $1,b & $1 FROM t ORDER BY id", []string{"?column?", "?column?"}, []string{"INT2", "INT8"}, [][]any{{int64(3), int64(3)}, {int64(0), int64(3)}, {int64(0), int64(3)}, {nil, nil}}, int64(3))
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	{
		step := referenceSelect("SELECT b & $1,s & $1 FROM t ORDER BY id", []string{"?column?", "?column?"}, []string{"INT8", "INT8"}, [][]any{{int64(3), int64(3)}, {int64(3), int64(0)}, {int64(3), int64(0)}, {nil, nil}}, int64(3))
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	{
		step := referenceSelect("SELECT s << $1 FROM t WHERE id=1", []string{"?column?"}, []string{"INT2"}, [][]any{{int64(-2)}}, int64(1))
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	{
		step := referenceSelect("SELECT $1 << 1 FROM t WHERE id=1", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(6)}}, int64(3))
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	{
		step := referenceSelect("SELECT s & $1 FROM t WHERE id=1", []string{"?column?"}, []string{"INT2"}, [][]any{{nil}}, nil)
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	{
		step := referenceSelect("SELECT s & 1,s & 1::smallint FROM t WHERE id=1", []string{"?column?", "?column?"}, []string{"INT4", "INT2"}, [][]any{{int64(1), int64(1)}})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	{
		step := referenceSelect("SELECT ~s,i | 4,b # 5 FROM t WHERE FALSE", []string{"?column?", "?column?", "?column?"}, []string{"INT2", "INT4", "INT8"}, [][]any{})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	{
		step := referenceSelect("SELECT i & 1 AS mask,count(*),count(*) & 1 FROM t WHERE id<4 GROUP BY i & 1 HAVING count(*)>0", []string{"mask", "count", "?column?"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(0), int64(1), int64(1)}, {int64(1), int64(2), int64(0)}})
		step.bag = true
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	for _, tc := range []struct {
		query string
		row   []any
	}{
		{"SELECT i & 1 AS mask,count(*),count(*) & 1 FROM t WHERE id<4 GROUP BY i & 1 HAVING count(*)=1", []any{int64(0), int64(1), int64(1)}},
		{"SELECT i & 1 AS mask,count(*),count(*) & 1 FROM t WHERE id<4 GROUP BY i & 1 HAVING count(*)=2", []any{int64(1), int64(2), int64(0)}},
	} {
		step := referenceSelect(tc.query, []string{"mask", "count", "?column?"}, []string{"INT4", "INT8", "INT8"}, [][]any{tc.row})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	{
		step := referenceSelect("SELECT CASE WHEN i & 1=1 THEN ~s ELSE s END,COALESCE(s & 1::smallint,'2') FROM t ORDER BY id", []string{"s", "coalesce"}, []string{"INT2", "INT2"}, [][]any{{int64(0), int64(1)}, {int64(-8), int64(0)}, {int64(-1), int64(0)}, {nil, int64(2)}})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	p.step(t, referenceExec("CREATE TABLE dest(s SMALLINT,i INTEGER,b BIGINT)", 0))
	p.step(t, referenceExec("INSERT INTO dest VALUES(1::smallint<<15,~0,1::bigint<<63)", 1))
	p.step(t, referenceExec("INSERT INTO dest SELECT s & 15::smallint,i | 4,b # 5 FROM t WHERE id<4", 3))
	p.step(t, referenceExec("UPDATE t SET s=s # 1::smallint,i=i | 4 WHERE i & 1=1", 2))
	p.step(t, referenceExec("DELETE FROM t WHERE i & 1=0", 1))
	final := referenceSelect("SELECT id,s,i,b FROM t ORDER BY id", []string{"id", "s", "i", "b"}, []string{"INT4", "INT2", "INT4", "INT8"}, [][]any{{int64(1), int64(-2), int64(5), int64(15)}, {int64(3), int64(1), int64(7), int64(15)}, {int64(4), nil, nil, nil}})
	p.step(t, final)
	p.step(t, referencePrepared(final))
}
