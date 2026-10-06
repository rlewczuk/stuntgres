package stuntgres_test

import (
	"context"
	"stuntgres"
	"testing"
)

func TestSequenceDeleteComposition(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c := sequenceConn(t, db)
	ctx := context.Background()
	exec := func(q string, count int64) {
		t.Helper()
		r, err := c.ExecContext(ctx, q)
		if err != nil { t.Fatal(q, err) }
		if n, err := r.RowsAffected(); err != nil || n != count { t.Fatal(q,n,err) }
	}
	rows := func(want [][]any) {
		t.Helper()
		r, err := c.QueryContext(ctx,"SELECT v FROM t ORDER BY v")
		queryCoreRows(t,r,err,[]string{"v"},[]string{"INT4"},want,false)
	}
	exec("CREATE SEQUENCE s",0)
	exec("CREATE TABLE t(v INT)",0)
	exec("INSERT INTO t VALUES(1),(1),(2)",3)
	sequenceValue(t,c,"SELECT nextval('s')",1)
	held, err := c.QueryContext(ctx,"SELECT v FROM t ORDER BY v")
	if err != nil { t.Fatal(err) }
	defer held.Close()
	exec("BEGIN",0)
	exec("ALTER SEQUENCE s RESTART 100",0)
	exec("DELETE FROM t WHERE v=1",2)
	exec("INSERT INTO t VALUES(3)",1)
	rows([][]any{{int64(2)},{int64(3)}})
	exec("ROLLBACK",0)
	rows([][]any{{int64(1)},{int64(1)},{int64(2)}})
	sequenceValue(t,c,"SELECT nextval('s')",2)
	exec("BEGIN",0)
	exec("ALTER SEQUENCE s RESTART 200",0)
	exec("DELETE FROM t WHERE v=1",2)
	exec("INSERT INTO t VALUES(4)",1)
	exec("INSERT INTO t VALUES(5)",1)
	exec("DELETE FROM t WHERE v=5",1)
	exec("COMMIT",0)
	rows([][]any{{int64(2)},{int64(4)}})
	queryCoreRows(t,held,nil,[]string{"v"},[]string{"INT4"},[][]any{{int64(1)},{int64(1)},{int64(2)}},false)
	sequenceValue(t,c,"SELECT nextval('s')",200)
	exec("BEGIN",0)
	exec("DELETE FROM t WHERE nextval('s')>0",2)
	exec("ROLLBACK",0)
	rows([][]any{{int64(2)},{int64(4)}})
	sequenceValue(t,c,"SELECT currval('s')",202)
	stmt, err := c.PrepareContext(ctx,"DELETE FROM s WHERE nextval('s')>0")
	if err != nil { t.Fatal("sequence DELETE Prepare",err) }
	_, err = stmt.ExecContext(ctx)
	stmt.Close()
	typeCoreDiagnostic(t,err,stuntgres.Error{Code:"42809",Message:`cannot change sequence "s"`})
	sequenceValue(t,c,"SELECT currval('s')",202)
	exec("CREATE TABLE dst(v INT)",0)
	exec("INSERT INTO dst SELECT v FROM t ORDER BY v",2)
	r, err := c.QueryContext(ctx,"SELECT v FROM dst ORDER BY v")
	queryCoreRows(t,r,err,[]string{"v"},[]string{"INT4"},[][]any{{int64(2)},{int64(4)}},false)
}
