package smoke_test

import (
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestArithmeticDeleteCompositionSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	for _, sql := range []string{"CREATE TABLE t(id INT,i INT,b BIGINT)", "INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL),(4,7,7)"} {
		if _, err := db.ExecContext(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.QueryContext(ctx, "SELECT i+1,DATE '2000-01-01' FROM t WHERE id=1")
	if err := identifierCheckRows(rows, err, []string{"?column?", "date"}, []string{"INT4", "DATE"}, [][]any{{int64(8), "2000-01-01"}}); err != nil {
		t.Fatal(err)
	}
	stmt, err := db.PrepareContext(ctx, "DELETE FROM t WHERE i+$1=9")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for index, arg := range []any{2, 16, nil} {
		result, err := stmt.ExecContext(ctx, arg)
		if err != nil {
			t.Fatal(err)
		}
		count, err := result.RowsAffected()
		if err != nil || count != []int64{2, 1, 0}[index] {
			t.Fatal(count, err)
		}
	}
	rows, err = db.QueryContext(ctx, "SELECT id,i,b FROM t ORDER BY id")
	if err := identifierCheckRows(rows, err, []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(3), nil, nil}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE fail(id INT,i INT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO fail VALUES(1,7),(2,0),(3,NULL)"); err != nil {
		t.Fatal(err)
	}
	result, err := db.ExecContext(ctx, "DELETE FROM fail WHERE 14/i=2")
	if result != nil {
		t.Fatal("failed deletion exposed result")
	}
	if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "22012", Message: "division by zero"}); err != nil {
		t.Fatal(err)
	}
	rows, err = db.QueryContext(ctx, "SELECT id,i FROM fail ORDER BY id")
	if err := identifierCheckRows(rows, err, []string{"id", "i"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(0)}, {int64(3), nil}}); err != nil {
		t.Fatal(err)
	}
	result, err = db.ExecContext(ctx, "DELETE FROM fail WHERE fail.i+1=8")
	if result != nil {
		t.Fatal("qualified arithmetic exposed result")
	}
	if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "42601", Message: `syntax error at or near "+"`, Position: "30"}); err != nil {
		t.Fatal(err)
	}
}
