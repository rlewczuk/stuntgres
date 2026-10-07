package stuntgres_test

import "testing"

func TestBitwiseParametersAndConsumers(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE t(id INTEGER,s SMALLINT,i INTEGER,b BIGINT)", 0)
	arithmeticExec(t, db, "INSERT INTO t VALUES(1,-1,1,15),(2,-8,2,15),(3,0,3,15),(4,NULL,NULL,NULL)", 4)
	arithmeticRows(t, db, "SELECT s & $1,b & $1 FROM t ORDER BY id", []string{"INT2", "INT8"}, [][]any{{int64(3), int64(3)}, {int64(0), int64(3)}, {int64(0), int64(3)}, {nil, nil}}, int64(3))
	arithmeticRows(t, db, "SELECT b & $1,s & $1 FROM t ORDER BY id", []string{"INT8", "INT8"}, [][]any{{int64(3), int64(3)}, {int64(3), int64(0)}, {int64(3), int64(0)}, {nil, nil}}, int64(3))
	arithmeticRows(t, db, "SELECT s << $1 FROM t WHERE id=1", []string{"INT2"}, [][]any{{int64(-2)}}, int64(1))
	arithmeticRows(t, db, "SELECT $1 << 1 FROM t WHERE id=1", []string{"INT4"}, [][]any{{int64(6)}}, int64(3))
	arithmeticRows(t, db, "SELECT s & $1 FROM t WHERE id=1", []string{"INT2"}, [][]any{{nil}}, nil)
	arithmeticRows(t, db, "SELECT s & 1,s & 1::smallint FROM t WHERE id=1", []string{"INT4", "INT2"}, [][]any{{int64(1), int64(1)}})
	arithmeticRows(t, db, "SELECT ~s,i | 4,b # 5 FROM t WHERE FALSE", []string{"INT2", "INT4", "INT8"}, [][]any{})
	arithmeticRows(t, db, "SELECT i & 1 AS mask,count(*),count(*) & 1 FROM t WHERE id<4 GROUP BY i & 1 HAVING count(*)=1", []string{"INT4", "INT8", "INT8"}, [][]any{{int64(0), int64(1), int64(1)}})
	arithmeticRows(t, db, "SELECT i & 1 AS mask,count(*),count(*) & 1 FROM t WHERE id<4 GROUP BY i & 1 HAVING count(*)=2", []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(2), int64(0)}})
	arithmeticRows(t, db, "SELECT CASE WHEN i & 1=1 THEN ~s ELSE s END,COALESCE(s & 1::smallint,'2') FROM t ORDER BY id", []string{"INT2", "INT2"}, [][]any{{int64(0), int64(1)}, {int64(-8), int64(0)}, {int64(-1), int64(0)}, {nil, int64(2)}})
	arithmeticExec(t, db, "CREATE TABLE dest(s SMALLINT,i INTEGER,b BIGINT)", 0)
	arithmeticExec(t, db, "INSERT INTO dest VALUES(1::smallint<<15,~0,1::bigint<<63)", 1)
	arithmeticExec(t, db, "INSERT INTO dest SELECT s & 15::smallint,i | 4,b # 5 FROM t WHERE id<4", 3)
	arithmeticExec(t, db, "UPDATE t SET s=s # 1::smallint,i=i | 4 WHERE i & 1=1", 2)
	arithmeticExec(t, db, "DELETE FROM t WHERE i & 1=0", 1)
	arithmeticRows(t, db, "SELECT id,s,i,b FROM t ORDER BY id", []string{"INT4", "INT2", "INT4", "INT8"}, [][]any{{int64(1), int64(-2), int64(5), int64(15)}, {int64(3), int64(1), int64(7), int64(15)}, {int64(4), nil, nil, nil}})
}
