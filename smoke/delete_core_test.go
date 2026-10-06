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
	"testing"
	"time"

	"stuntgres"
)

func TestDeleteCoreSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connector, err := stuntgres.NewDriver().OpenConnector("delete-core-smoke")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(x interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	}, q string, want int64, args ...any) {
		t.Helper()
		result, err := x.ExecContext(ctx, q, args...)
		if err != nil {
			t.Fatal(q, err)
		}
		n, err := result.RowsAffected()
		if err != nil || n != want {
			t.Fatal(q, n, err, want)
		}
	}
	check := func(want [][]any) {
		t.Helper()
		rows, err := db.QueryContext(ctx, "SELECT id,v,f FROM t ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		names, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(names, []string{"id", "v", "f"}) {
			t.Fatal(names, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 3 {
			t.Fatal(types, err)
		}
		for i, name := range []string{"INT4", "INT4", "BOOL"} {
			if types[i].DatabaseTypeName() != name {
				t.Fatal(types[i])
			}
		}
		got := make([][]any, 0)
		for rows.Next() {
			row := make([]any, 3)
			if err = rows.Scan(&row[0], &row[1], &row[2]); err != nil {
				t.Fatal(err)
			}
			got = append(got, row)
		}
		if err = rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(got, err, want)
		}
	}
	exec(db, "CREATE TABLE t(id INT NOT NULL,v INT,f BOOL)", 0)
	exec(db, "INSERT INTO t VALUES(1,10,TRUE),(2,NULL,NULL),(3,20,FALSE),(4,10,TRUE),(4,10,TRUE)", 5)
	raw, err := connector.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	saved, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT id,v,f FROM t ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer saved.Close()
	prepared, err := raw.(driver.ConnPrepareContext).PrepareContext(ctx, "DELETE FROM t AS d WHERE d.v=$1 OR d.v=$1")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.NumInput() != 1 {
		t.Fatal("inferred repeated INT4 parameter count", prepared.NumInput())
	}
	prepared.Close()
	exec(db, "DELETE FROM t AS d WHERE d.v=10", 3)
	survivors := [][]any{{int64(2), nil, nil}, {int64(3), int64(20), false}}
	check(survivors)
	held, err := db.PrepareContext(ctx, "DELETE FROM t d WHERE d.v=$1 OR d.v=$1")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, arg := range []any{10, nil} {
		result, err := held.ExecContext(ctx, arg)
		if err != nil {
			t.Fatal(err)
		}
		n, err := result.RowsAffected()
		if err != nil || n != 0 {
			t.Fatal(n, err)
		}
	}
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	tx, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exec(tx, "DELETE FROM t WHERE id=2", 1)
	var own int64
	if err = tx.QueryRowContext(ctx, "SELECT id FROM t").Scan(&own); err != nil || own != 3 {
		t.Fatal(own, err)
	}
	check(survivors) // peer sees committed originals, not private deletion.
	var private bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &private); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	check(survivors)
	canceled, stop := context.WithCancel(ctx)
	stop()
	result, err := db.ExecContext(canceled, "DELETE FROM t")
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(result, err)
	}
	check(survivors)
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if dump.String() != private.String() || strings.Contains(dump.String(), "DELETE") || !strings.Contains(dump.String(), "NOT NULL") {
		t.Fatal("dump must encode committed live survivors and constraints", dump.String(), private.String())
	}
	exec(db, "DELETE FROM t", 2)
	check([][]any{})
	exec(db, "DELETE FROM t", 0)
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check(survivors)
	if !reflect.DeepEqual(saved.Columns(), []string{"id", "v", "f"}) {
		t.Fatal(saved.Columns())
	}
	meta := saved.(driver.RowsColumnTypeDatabaseTypeName)
	for i, name := range []string{"INT4", "INT4", "BOOL"} {
		if meta.ColumnTypeDatabaseTypeName(i) != name {
			t.Fatal("retained cursor metadata")
		}
	}
	original := [][]driver.Value{{int64(1), int64(10), true}, {int64(2), nil, nil}, {int64(3), int64(20), false}, {int64(4), int64(10), true}, {int64(4), int64(10), true}}
	dst := make([]driver.Value, 3)
	for _, want := range original {
		if err = saved.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
			t.Fatal(dst, err, want)
		}
	}
	if err = saved.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
	// Restored metadata still enforces a real constraint after delete-all.
	_, err = db.ExecContext(ctx, "INSERT INTO t VALUES(NULL,1,TRUE)")
	if err = typeCoreDiagnostic(err, stuntgres.Error{Code: "23502", Message: `null value in column "id" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, 1, t).", Schema: "public", Table: "t", Column: "id"}); err != nil {
		t.Fatal(err)
	}
	check(survivors)
}
