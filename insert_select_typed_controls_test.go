package stuntgres_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
	"time"
)

func TestInsertSelectTypedControls(t *testing.T) {
	d := stuntgres.NewDriver()
	db := openLocal(t, d, "insert-select-typed-controls")
	execSQL(t, db, "CREATE TABLE s(n NUMERIC(5,2),stamp TIMESTAMP(3),clock TIME,b BIGINT,u UUID)")
	execSQL(t, db, "INSERT INTO s VALUES('1.234',TIMESTAMP '2000-01-01 00:00:00.1234',TIME '24:00',2147483648,'A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11'),(NULL,NULL,NULL,NULL,NULL)")
	execSQL(t, db, "CREATE TABLE t(n NUMERIC(5,2),stamp TIMESTAMP(3),clock TIME,b BIGINT,u UUID)")
	result, err := db.Exec("INSERT INTO t SELECT * FROM s")
	insertSelectCount(t, result, err, 2)
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	clone := openLocal(t, d, "insert-select-typed-clone")
	if err := stuntgres.Restore(context.Background(), clone, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	rows, err := clone.Query("SELECT * FROM t ORDER BY n")
	queryCoreRows(t, rows, err, []string{"n", "stamp", "clock", "b", "u"}, []string{"NUMERIC", "TIMESTAMP", "TIME", "INT8", "UUID"}, [][]any{{"1.23", time.Date(2000, 1, 1, 0, 0, 0, 123000000, time.UTC), "24:00:00", int64(2147483648), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"}, {nil, nil, nil, nil, nil}}, false)
}
