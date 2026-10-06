package stuntgres_test

import (
	"context"
	"stuntgres"
	"testing"
)

func TestSequenceMergedProducer(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c := sequenceConn(t, db)
	ctx := context.Background()
	sequenceExec(t, c, "CREATE SEQUENCE s")
	sequenceExec(t, c, "CREATE TABLE t(v BIGINT)")
	for _, q := range []string{
		"INSERT INTO t SELECT nextval('s'::regclass)::bigint",
		"INSERT INTO t SELECT last_value FROM s",
		"INSERT INTO t VALUES(nextval(CAST('s' AS regclass))) ORDER BY 1",
	} {
		stmt, err := c.PrepareContext(ctx, q)
		if err != nil { t.Fatal(q, err) }
		r, err := stmt.ExecContext(ctx)
		stmt.Close()
		if err != nil { t.Fatal(q, err) }
		n, err := r.RowsAffected()
		if err != nil || n != 1 { t.Fatal(q, n, err) }
	}
	rows, err := c.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
	queryCoreRows(t, rows, err, []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}}, false)
	sequenceValue(t, c, "SELECT currval('s')", 2)
	rows, err = c.QueryContext(ctx, "SELECT num_nulls(NULL), nextval('s')::bigint")
	queryCoreRows(t, rows, err, []string{"num_nulls", "nextval"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(3)}}, false)
	_, err = c.QueryContext(ctx, "SELECT setval('s',$1::text::bigint) FROM t WHERE v=99", "bad")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type bigint: "bad"`})
	sequenceValue(t, c, "SELECT currval('s')", 3)
	sequenceExec(t, c, "BEGIN")
	sequenceExec(t, c, "CREATE SEQUENCE private_s START 40")
	r, err := c.ExecContext(ctx, "INSERT INTO t SELECT last_value FROM private_s")
	if err != nil { t.Fatal(err) }
	if n, err := r.RowsAffected(); err != nil || n != 1 { t.Fatal(n, err) }
	sequenceExec(t, c, "ROLLBACK")
	rows, err = c.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
	queryCoreRows(t, rows, err, []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}}, false)
	sequenceExec(t, c, "CREATE TABLE src(id INT, day DATE, payload BYTEA, nullable BIGINT)")
	if _, err := c.ExecContext(ctx, "INSERT INTO src VALUES(1,DATE '2024-02-29','\\x01',NULL),(2,DATE '2024-03-01','\\x02',7)"); err != nil { t.Fatal(err) }
	sequenceExec(t, c, "CREATE TABLE dst(id BIGINT, n BIGINT, day DATE, payload BYTEA, nullable BIGINT)")
	stmt, err := c.PrepareContext(ctx, "INSERT INTO dst SELECT id::bigint,nextval('s'),day,payload,nullable FROM src ORDER BY id")
	if err != nil { t.Fatal(err) }
	sequenceValue(t, c, "SELECT currval('s')", 3)
	r, err = stmt.ExecContext(ctx)
	stmt.Close()
	if err != nil { t.Fatal(err) }
	if n, err := r.RowsAffected(); err != nil || n != 2 { t.Fatal(n, err) }
	rows, err = c.QueryContext(ctx, "SELECT * FROM dst ORDER BY id")
	queryCoreRows(t, rows, err, []string{"id","n","day","payload","nullable"}, []string{"INT8","INT8","DATE","BYTEA","INT8"}, [][]any{{int64(1),int64(4),"2024-02-29",[]byte{1},nil},{int64(2),int64(5),"2024-03-01",[]byte{2},int64(7)}}, false)
	r, err = c.ExecContext(ctx, "INSERT INTO dst(id,n) SELECT id::bigint,nextval('s') FROM src WHERE id=99")
	if err != nil { t.Fatal(err) }
	if n, err := r.RowsAffected(); err != nil || n != 0 { t.Fatal(n, err) }
	sequenceValue(t, c, "SELECT currval('s')", 5)
	sequenceExec(t, c, "CREATE TABLE bad(v TEXT)")
	if _, err := c.ExecContext(ctx, "INSERT INTO bad VALUES('bad')"); err != nil { t.Fatal(err) }
	_, err = c.ExecContext(ctx, "INSERT INTO dst(id,n) SELECT nextval('s'),v::bigint FROM bad")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type bigint: "bad"`})
	sequenceValue(t, c, "SELECT currval('s')", 6)
	rows, err = c.QueryContext(ctx, "SELECT id FROM dst ORDER BY id")
	queryCoreRows(t, rows, err, []string{"id"}, []string{"INT8"}, [][]any{{int64(1)},{int64(2)}}, false)
	rows, err = c.QueryContext(ctx, "SELECT num_nulls(nextval('s'))")
	queryCoreRows(t, rows, err, []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(0)}}, false)
	sequenceValue(t, c, "SELECT currval('s')", 7)
	rows, err = c.QueryContext(ctx, "SELECT 0 IS DISTINCT FROM nextval('s') FROM src WHERE id=1")
	queryCoreRows(t, rows, err, []string{"?column?"}, []string{"BOOL"}, [][]any{{true}}, false)
	sequenceValue(t, c, "SELECT currval('s')", 8)
}
