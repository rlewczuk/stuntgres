package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
)

func TestSequenceDeleteCompositionSmoke(t *testing.T) {
	ctx := context.Background()
	connector,err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err!=nil { t.Fatal(err) }
	db := sql.OpenDB(connector)
	defer db.Close()
	c,err := db.Conn(ctx)
	if err!=nil { t.Fatal(err) }
	defer c.Close()
	exec := func(q string,want int64) {
		t.Helper()
		r,err := c.ExecContext(ctx,q)
		if err!=nil { t.Fatal(q,err) }
		if n,err := r.RowsAffected(); err!=nil || n!=want { t.Fatal(q,n,err) }
	}
	value := func(q string,want int64) {
		t.Helper()
		var n int64
		if err := c.QueryRowContext(ctx,q).Scan(&n); err!=nil || n!=want { t.Fatal(q,n,err) }
	}
	rows := func(want []int64) {
		t.Helper()
		r,err := c.QueryContext(ctx,"SELECT v FROM t ORDER BY v")
		if err!=nil { t.Fatal(err) }
		defer r.Close()
		types,err := r.ColumnTypes()
		if err!=nil || len(types)!=1 || types[0].Name()!="v" || types[0].DatabaseTypeName()!="INT4" { t.Fatal(types,err) }
		for _,n := range want {
			var got int64
			if !r.Next() { t.Fatal(r.Err()) }
			if err := r.Scan(&got); err!=nil || got!=n { t.Fatal(got,n,err) }
		}
		if r.Next() || r.Err()!=nil { t.Fatal(r.Err()) }
	}
	exec("CREATE TABLE t(v INT)",0)
	exec("INSERT INTO t VALUES(1),(1),(2)",3)
	exec("CREATE SEQUENCE s",0)
	value("SELECT nextval('s')",1)
	for _,commit := range []bool{false,true} {
		exec("BEGIN",0)
		exec("ALTER SEQUENCE s RESTART 100",0)
		exec("DELETE FROM t WHERE v=1",2)
		exec("INSERT INTO t VALUES(3)",1)
		rows([]int64{2,3})
		if commit { exec("COMMIT",0); rows([]int64{2,3}); value("SELECT nextval('s')",100) } else { exec("ROLLBACK",0); rows([]int64{1,1,2}); value("SELECT nextval('s')",2) }
	}
	exec("BEGIN",0)
	exec("DELETE FROM t WHERE nextval('s')>0",2)
	exec("ROLLBACK",0)
	rows([]int64{2,3})
	value("SELECT currval('s')",102)
	stmt,err := c.PrepareContext(ctx,"DELETE FROM s WHERE nextval('s')>0")
	if err!=nil { t.Fatal(err) }
	_,err=stmt.ExecContext(ctx)
	stmt.Close()
	var d *stuntgres.Error
	if !errors.As(err,&d) || d.Code!="42809" || d.Message!=`cannot change sequence "s"` || d.Position!="" || d.Detail!="" || d.Hint!="" { t.Fatal(err) }
	value("SELECT currval('s')",102)
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx,db,&dump); err!=nil { t.Fatal(err) }
	if err := stuntgres.Reset(ctx,db); err!=nil { t.Fatal(err) }
	if err := stuntgres.Restore(ctx,db,bytes.NewReader(dump.Bytes())); err!=nil { t.Fatal(err) }
	rows([]int64{2,3})
	value("SELECT nextval('s')",103)
}
