package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
)

func TestSequenceMergedProducerSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil { t.Fatal(err) }
	db := sql.OpenDB(connector)
	defer db.Close()
	c, err := db.Conn(ctx)
	if err != nil { t.Fatal(err) }
	defer c.Close()
	exec := func(q string, want int64) {
		t.Helper()
		r, err := c.ExecContext(ctx,q)
		if err != nil { t.Fatal(q,err) }
		if n, err := r.RowsAffected(); err != nil || n != want { t.Fatal(q,n,err) }
	}
	exec("CREATE SEQUENCE s",0)
	exec("CREATE TABLE src(id INT,day DATE,payload BYTEA)",0)
	exec("INSERT INTO src VALUES(1,DATE '2024-02-29','\x01'),(2,DATE '2024-03-01','\x02')",2)
	exec("CREATE TABLE dst(id BIGINT,n BIGINT,day DATE,payload BYTEA)",0)
	stmt, err := c.PrepareContext(ctx,"INSERT INTO dst SELECT id::bigint,nextval('s'::regclass),day,payload FROM src ORDER BY id")
	if err != nil { t.Fatal(err) }
	r, err := stmt.ExecContext(ctx)
	stmt.Close()
	if err != nil { t.Fatal(err) }
	if n, err := r.RowsAffected(); err != nil || n != 2 { t.Fatal(n,err) }
	rows, err := c.QueryContext(ctx,"SELECT * FROM dst ORDER BY id")
	if err != nil { t.Fatal(err) }
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil || len(types)!=4 { t.Fatal(types,err) }
	for i, want := range []string{"INT8","INT8","DATE","BYTEA"} { if types[i].DatabaseTypeName()!=want { t.Fatal(types[i]) } }
	for i, day := range []string{"2024-02-29","2024-03-01"} {
		if !rows.Next() { t.Fatal(rows.Err()) }
		var id,n int64
		var date string
		var payload []byte
		if err := rows.Scan(&id,&n,&date,&payload); err != nil { t.Fatal(err) }
		if id!=int64(i+1) || n!=id || date!=day || !bytes.Equal(payload,[]byte{byte(i+1)}) { t.Fatal(id,n,date,payload) }
	}
	if rows.Next() || rows.Err()!=nil { t.Fatal(rows.Err()) }
	rows.Close()
	exec("INSERT INTO dst(id,n) SELECT id::bigint,nextval('s') FROM src WHERE id=99",0)
	_, err = c.QueryContext(ctx,"SELECT setval('s',$1::text::bigint) FROM src WHERE id=99","bad")
	var diagnostic *stuntgres.Error
	if !errors.As(err,&diagnostic) || diagnostic.Code!="22P02" || diagnostic.Message!=`invalid input syntax for type bigint: "bad"` || diagnostic.Position!="" { t.Fatal(err) }
	var last int64
	if err := c.QueryRowContext(ctx,"SELECT currval('s')").Scan(&last); err != nil || last!=2 { t.Fatal(last,err) }
	exec("INSERT INTO dst(n) SELECT last_value FROM s",1)
}
