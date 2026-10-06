package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
)

func TestArithmeticCastCompositionSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	for _, query := range []string{"CREATE TABLE t(id INT,i INT)", "INSERT INTO t VALUES(1,7),(2,NULL)", "CREATE TABLE e(i INT)"} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(query, err)
		}
	}
	query := "SELECT i::bigint+1,CAST(i+1 AS bigint),(i+1)::bigint FROM t ORDER BY id"
	for _, prepared := range []bool{false, true} {
		stmt, err := db.PrepareContext(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		var rows *sql.Rows
		if prepared {
			rows, err = stmt.QueryContext(ctx)
		} else {
			rows, err = db.QueryContext(ctx, query)
		}
		if err := identifierCheckRows(rows, err, []string{"?column?", "int8", "int8"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(8), int64(8), int64(8)}, {nil, nil, nil}}); err != nil {
			t.Fatal(err)
		}
		stmt.Close()
	}
	stmt, err := db.PrepareContext(ctx, "SELECT i+CAST($1::text AS int4) FROM e")
	if err != nil {
		t.Fatal("execution conversion moved into Prepare", err)
	}
	defer stmt.Close()
	for _, arg := range []any{"oops", "2", nil, "oops"} {
		rows, err := stmt.QueryContext(ctx, arg)
		if arg == "oops" {
			if rows != nil {
				rows.Close()
				t.Fatal("failed cast exposed cursor")
			}
			if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}); err != nil {
				t.Fatal(err)
			}
		} else if err := identifierCheckRows(rows, err, []string{"?column?"}, []string{"INT4"}, [][]any{}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.QueryContext(ctx, "SELECT i/$1 FROM t WHERE FALSE", 0)
	if err := identifierCheckRows(rows, err, []string{"?column?"}, []string{"INT4"}, [][]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO t VALUES(3,CAST(7/2 AS int4)),(4,(2147483648::bigint+0)::int4)"); err == nil {
		t.Fatal("late narrowing accepted")
	} else if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "22003", Message: "integer out of range"}); err != nil {
		t.Fatal(err)
	}
	rows, err = db.QueryContext(ctx, "SELECT id FROM t ORDER BY id")
	if err := identifierCheckRows(rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}); err != nil {
		t.Fatal(err)
	}
	rows, err = db.QueryContext(ctx, "SELECT num_nulls((+1)::int4,NULL)")
	if err := identifierCheckRows(rows, err, []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(1)}}); err != nil {
		t.Fatal(err)
	}
	rows, err = db.QueryContext(ctx, "SELECT num_nulls(CAST(1+2 AS int4)) FROM t")
	if rows != nil {
		rows.Close()
		t.Fatal("cast concealed unsupported call arithmetic")
	}
	if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "0A000", Message: "arithmetic in null-count arguments is unsupported", Position: "24"}); err != nil {
		t.Fatal(err)
	}
	err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE t(id INT,i INT); INSERT INTO t VALUES(1,CAST(7/2 AS int4)+1)"))
	if err != nil {
		t.Fatal(err)
	}
	var before, after bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &before); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, strings.NewReader(before.String())); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Dump(ctx, db, &after); err != nil || before.String() != after.String() {
		t.Fatal(before.String(), after.String(), err)
	}
	rows, err = db.QueryContext(ctx, "SELECT i::bigint+1 FROM t")
	if err := identifierCheckRows(rows, err, []string{"?column?"}, []string{"INT8"}, [][]any{{int64(5)}}); err != nil {
		t.Fatal(err)
	}
}
