package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestInsertSelectScalarSource(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "insert-select-scalar-source")
	execSQL(t, db, "CREATE TABLE s(i INT,v INT,b BOOL)")
	execSQL(t, db, "INSERT INTO s VALUES(1,NULL,TRUE),(2,3,FALSE)")
	execSQL(t, db, "CREATE TABLE t(i BIGINT,b BOOL)")
	q := "INSERT INTO t SELECT num_nulls(v),NOT b FROM s WHERE i>0 ORDER BY i"
	result, err := db.Exec(q)
	insertSelectCount(t, result, err, 2)
	stmt, err := db.Prepare(q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	result, err = stmt.Exec()
	insertSelectCount(t, result, err, 2)
	rows, err := db.Query("SELECT * FROM t")
	queryCoreRows(t, rows, err, []string{"i", "b"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}, {int64(0), true}, {int64(1), false}, {int64(0), true}}, true)
	result, err = db.Exec("INSERT INTO t SELECT num_nulls(v),NOT b FROM s WHERE FALSE")
	insertSelectCount(t, result, err, 0)
}
