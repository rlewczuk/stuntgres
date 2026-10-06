package stuntgres_test

import "testing"

func TestArithmeticRebasedBooleanPredicateGrouping(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE t(id INT,i INT)", 0)
	arithmeticExec(t, db, "INSERT INTO t VALUES(1,7),(2,0),(3,NULL)", 3)
	arithmeticRows(t, db, "SELECT ((i+1)*2),((i+1)=8 AND NOT (i=0)),(i+1) IS NULL FROM t ORDER BY id", []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(16), true, false}, {int64(2), false, false}, {nil, nil, true}})
	arithmeticRows(t, db, "SELECT (i+1)*2 FROM t WHERE ((i+1)=8 OR (i=0)) AND NOT ((i+1) IS NULL) ORDER BY id", []string{"INT4"}, [][]any{{int64(16)}, {int64(2)}})
}
