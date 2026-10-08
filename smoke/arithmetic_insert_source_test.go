package smoke_test

import (
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestArithmeticInsertSourceContextualSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	for _, sql := range []string{"CREATE TABLE d(f BOOL)", "INSERT INTO d VALUES(TRUE)"} {
		if _, err := db.ExecContext(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE good(id INT,i INT,b BIGINT)"); err != nil {
		t.Fatal(err)
	}
	result, err := db.ExecContext(ctx, "INSERT INTO good SELECT 1,7/2,-7%2")
	if err != nil {
		t.Fatal(err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		t.Fatal(count, err)
	}
	rows, err := db.QueryContext(ctx, "SELECT id,i,b FROM good")
	if err := identifierCheckRows(rows, err, []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(3), int64(-1)}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query string
		want  stuntgres.Error
	}{
		{"INSERT INTO d SELECT 1+2", stuntgres.Error{Code: "42804", Message: `column "f" is of type boolean but expression is of type integer`, Hint: "You will need to rewrite or cast the expression.", Position: "22"}},
		{"/*é*/ INSERT INTO d SELECT (1+2)::bigint", stuntgres.Error{Code: "42804", Message: `column "f" is of type boolean but expression is of type bigint`, Hint: "You will need to rewrite or cast the expression.", Position: "29"}},
		{"INSERT INTO d VALUES(TRUE),((1+2)::bigint) ORDER BY 1", stuntgres.Error{Code: "42804", Message: "VALUES types boolean and bigint cannot be matched", Position: "30"}},
		{"INSERT INTO d SELECT TRUE,(1+2)::bigint", stuntgres.Error{Code: "42601", Message: "INSERT has more expressions than target columns", Position: "28"}},
		{"INSERT INTO d VALUES(TRUE),(1+2) LIMIT 1", stuntgres.Error{Code: "42804", Message: "VALUES types boolean and integer cannot be matched", Position: "29"}},
		{"/*é*/ INSERT INTO d VALUES(TRUE),(1+2) LIMIT 1", stuntgres.Error{Code: "42804", Message: "VALUES types boolean and integer cannot be matched", Position: "35"}},
	} {
		result, err := db.ExecContext(ctx, tc.query)
		if result != nil {
			t.Fatal("analysis failure exposed result")
		}
		if err := typeCoreDiagnostic(err, tc.want); err != nil {
			t.Fatal(tc.query, err)
		}
		stmt, err := db.PrepareContext(ctx, tc.query)
		if stmt != nil {
			stmt.Close()
			t.Fatal("analysis failure exposed handle")
		}
		if err := typeCoreDiagnostic(err, tc.want); err != nil {
			t.Fatal(tc.query, err)
		}
		rows, err := db.QueryContext(ctx, "SELECT f FROM d")
		if err := identifierCheckRows(rows, err, []string{"f"}, []string{"BOOL"}, [][]any{{true}}); err != nil {
			t.Fatal(err)
		}
	}
}
