package stuntgres_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
)

func TestBinarySliceReplayAndDeparse(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA,i INTEGER,n INTEGER,e BYTEA,CONSTRAINT valid CHECK(overlay(v PLACING s FROM i FOR n)=e))")
	execSQL(t, db, "INSERT INTO t VALUES(1,'abcdef','XY',3,2,'abXYef')")
	execSQL(t, db, `CREATE VIEW sliced AS SELECT overlay(v PLACING '\x0080ff' FROM i FOR n) AS o,substring(v FROM i FOR n) AS b,position(s IN v) AS p,trim('\x00' FROM v) AS t,pg_catalog.position(v,'\x63') AS c FROM t`)
	want := [][]any{{[]byte{'a', 'b', 0, 128, 255, 'e', 'f'}, []byte("cd"), int64(0), []byte("abcdef"), int64(3)}}
	wantTuples(t, db, "SELECT * FROM sliced", want)
	wantTuples(t, db, "SELECT pg_get_expr(conbin,conrelid),pg_get_expr(conbin,conrelid,true),pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='valid'", [][]any{{"(OVERLAY(v PLACING s FROM i FOR n) = e)", "OVERLAY(v PLACING s FROM i FOR n) = e", "CHECK ((OVERLAY(v PLACING s FROM i FOR n) = e))"}})
	held, err := db.Query("SELECT * FROM sliced")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT * FROM sliced", want)
	if !held.Next() {
		t.Fatal(held.Err())
	}
	got := make([]any, 5)
	dest := make([]any, 5)
	for i := range got {
		dest[i] = &got[i]
	}
	if err := held.Scan(dest...); err != nil || !reflect.DeepEqual(got, want[0]) {
		t.Fatal(got, err)
	}
	got[0].([]byte)[0] = 99
	held.Close()
	wantTuples(t, db, "SELECT * FROM sliced", want)
	_, err = db.Exec("INSERT INTO t VALUES(2,'abcdef','XY',3,2,'wrong')")
	var diagnostic *stuntgres.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23514" {
		t.Fatal(err)
	}
	before := dump.String()
	if err := stuntgres.Restore(ctx, db, bytes.NewBufferString("CREATE TABLE sentinel(v BYTEA); INSERT INTO sentinel VALUES('x'); SELECT substr('abc'::bytea,1,-1);")); err == nil {
		t.Fatal("failed restore accepted")
	}
	wantTuples(t, db, "SELECT * FROM sliced", want)
	var after bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &after); err != nil || after.String() != before {
		t.Fatal("failed restore changed catalog", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	rows, err := db.QueryContext(canceled, "SELECT substr(v,i,n) FROM t")
	if rows != nil {
		rows.Close()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT * FROM sliced", want)
}

func TestBinarySliceComposition(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA,i INTEGER,n INTEGER)")
	execSQL(t, db, "INSERT INTO t VALUES(1,'abcdef','XY',3,2),(2,'abcdef','XY',3,2)")
	wantTuples(t, db, "SELECT overlay(v,s,i,n),count(*) FROM t GROUP BY overlay(v,s,i,n) HAVING pg_catalog.position(overlay(v,s,i,n),'XY')=3", [][]any{{[]byte("abXYef"), int64(2)}})
	wantTuples(t, db, "SELECT id,CASE WHEN position(s IN v)=0 THEN substr(overlay(v,s,i,n),i,n) ELSE v END FROM t ORDER BY id", [][]any{{int64(1), []byte("XY")}, {int64(2), []byte("XY")}})
	execSQL(t, db, "CREATE TABLE dst(v BYTEA)")
	result, err := db.Exec("INSERT INTO dst SELECT substring(v FROM i FOR n) FROM t WHERE pg_catalog.position(v,'cd')=3")
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := result.RowsAffected(); n != 2 {
		t.Fatal(n)
	}
	wantTuples(t, db, "UPDATE dst SET v=overlay(v,'X',1,1) RETURNING v", [][]any{{[]byte("Xd")}, {[]byte("Xd")}})
	wantTuples(t, db, "DELETE FROM dst RETURNING substring(v FROM 2)", [][]any{{[]byte("d")}, {[]byte("d")}})
}
