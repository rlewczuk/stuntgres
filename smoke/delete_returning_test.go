package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func returningSmokeRows(t *testing.T, rows *sql.Rows, err error, names, types []string, want [][]any) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	gotNames, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(gotNames, names) {
		t.Fatal(gotNames, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != len(types) {
		t.Fatal(meta, err)
	}
	for i, typ := range types {
		if meta[i].DatabaseTypeName() != typ {
			t.Fatal(meta[i], typ)
		}
	}
	got := [][]any{}
	for rows.Next() {
		row := make([]any, len(names))
		dst := make([]any, len(names))
		for i := range row {
			dst[i] = &row[i]
		}
		if err = rows.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err = rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, err, want)
	}
}

func TestDeleteReturningSmoke(t *testing.T) {
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	for _, q := range []string{"CREATE TABLE t(id INT NOT NULL,v TEXT)", "INSERT INTO t VALUES(1,'a'),(2,NULL),(2,NULL),(3,'c')", "CREATE SEQUENCE s START 101 CACHE 1"} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	query := "DELETE FROM t WHERE id=$1 RETURNING WITH (NEW AS n, OLD AS o) o.id,o.v,n.id,n.v,id+10 AS bumped,n.id IS NULL AS gone,COALESCE(n.id,o.id) AS kept"
	held, err := db.Prepare(query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	names := []string{"id", "v", "id", "v", "bumped", "gone", "kept"}
	types := []string{"INT4", "TEXT", "INT4", "TEXT", "INT4", "BOOL", "INT4"}
	for _, want := range [][][]any{{{int64(2), nil, nil, nil, int64(12), true, int64(2)}, {int64(2), nil, nil, nil, int64(12), true, int64(2)}}, {}} {
		rows, err := held.Query(2)
		returningSmokeRows(t, rows, err, names, types, want)
	}
	rows, err := db.Query("SELECT id,v FROM t ORDER BY id")
	returningSmokeRows(t, rows, err, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(3), "c"}})
	if _, err = db.Exec("INSERT INTO t VALUES(2,NULL),(2,NULL)"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []int64{2, 0} {
		result, err := db.Exec("DELETE FROM t WHERE id=2 RETURNING *,old.*,new.*")
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != want {
			t.Fatal(n, err, want)
		}
	}
	rows, err = held.Query(1)
	returningSmokeRows(t, rows, err, names, types, [][]any{{int64(1), "a", nil, nil, int64(11), true, int64(1)}})
	rows, err = db.Query("DELETE FROM t WHERE FALSE RETURNING nextval('s')")
	returningSmokeRows(t, rows, err, []string{"nextval"}, []string{"INT8"}, [][]any{})
	var called bool
	if err = db.QueryRow("SELECT is_called FROM s").Scan(&called); err != nil || called {
		t.Fatal(called, err)
	}
}

func TestDeleteReturningControlsSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	for _, q := range []string{"CREATE SEQUENCE s START 101 CACHE 1", "CREATE TABLE t(id INT NOT NULL DEFAULT 9,v TEXT)", "COMMENT ON TABLE t IS 'retained'", "CREATE VIEW survivors AS SELECT id,v FROM t", "INSERT INTO t VALUES(0,NULL),(1,'a'),(2,'b'),(2,'b')"} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	result, err := db.Exec("DELETE FROM t RETURNING 10/id")
	if result != nil {
		t.Fatal(result)
	}
	if err = typeCoreDiagnostic(err, stuntgres.Error{Code: "22012", Message: "division by zero"}); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	rows, err := db.QueryContext(canceled, "DELETE FROM t RETURNING *")
	if rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(rows, err)
	}
	rows, err = db.Query("SELECT id FROM t ORDER BY id")
	returningSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(2)}, {int64(2)}})
	raw, err := connector.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	exec := func(q string) {
		t.Helper()
		if _, err := raw.(driver.ExecerContext).ExecContext(ctx, q, nil); err != nil {
			t.Fatal(err)
		}
	}
	exec("BEGIN")
	retained, err := raw.(driver.QueryerContext).QueryContext(ctx, "DELETE FROM t WHERE id=2 RETURNING id,v,new.id,nextval('s')", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	var private bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &private); err != nil {
		t.Fatal(err)
	}
	exec("ROLLBACK")
	rows, err = db.Query("DELETE FROM t WHERE id=0 RETURNING id")
	returningSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}})
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "DELETE") || !strings.Contains(dump.String(), "retained") || !strings.Contains(dump.String(), "NOT NULL") {
		t.Fatal(dump.String())
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query("SELECT id,v FROM survivors ORDER BY id")
	returningSmokeRows(t, rows, err, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(2), "b"}})
	dst := make([]driver.Value, 4)
	for _, draw := range []int64{101, 102} {
		if err = retained.Next(dst); err != nil || !reflect.DeepEqual(dst, []driver.Value{int64(2), "b", nil, draw}) {
			t.Fatal(dst, err)
		}
	}
	if err = retained.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
	var next int64
	if err = db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 103 {
		t.Fatal(next, err)
	}
	if _, err = db.Exec("INSERT INTO t(v) VALUES('default')"); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, strings.NewReader("DELETE FROM t RETURNING *;")); err == nil {
		t.Fatal("Restore accepted DELETE history")
	}
	rows, err = db.Query("SELECT id FROM t ORDER BY id")
	returningSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {int64(9)}})
}
