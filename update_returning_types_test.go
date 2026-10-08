package stuntgres_test

import (
	"stuntgres"
	"testing"
	"time"
)

func TestUpdateReturningTypedVersions(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,d DATE,clock TIME(3),stamp TIMESTAMP(3),u UUID,n NUMERIC(8,2),b BYTEA)")
	execSQL(t, db, "INSERT INTO t VALUES(1,'2000-01-02','04:05:06.124','2000-01-02 04:05:06.124','a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11',12.30,'\\x0102'),(2,NULL,NULL,NULL,NULL,NULL,NULL)")
	q := "UPDATE t SET id=id RETURNING old.d,new.d,old.clock,new.clock,old.stamp,new.stamp,old.u,new.u,old.n,new.n,old.b,new.b"
	want := [][]any{{"2000-01-02", "2000-01-02", "04:05:06.124", "04:05:06.124", time.Date(2000, 1, 2, 4, 5, 6, 124000000, time.UTC), time.Date(2000, 1, 2, 4, 5, 6, 124000000, time.UTC), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "12.30", "12.30", []byte{1, 2}, []byte{1, 2}}, {nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil}}
	held, err := db.Prepare(q)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	rows, err := held.Query()
	queryCoreRows(t, rows, err, []string{"d", "d", "clock", "clock", "stamp", "stamp", "u", "u", "n", "n", "b", "b"}, []string{"DATE", "DATE", "TIME", "TIME", "TIMESTAMP", "TIMESTAMP", "UUID", "UUID", "NUMERIC", "NUMERIC", "BYTEA", "BYTEA"}, want, true)
	rows, err = db.Query("UPDATE t SET id=3 WHERE d=$1 RETURNING old.id,new.id", nil)
	queryCoreRows(t, rows, err, []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{}, false)
}
