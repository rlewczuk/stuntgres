package reference_test

import (
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArithmeticMigratedInsertSourceSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx, db := p.Ctx, backend.DB

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
			if err := referenceMigratedRows(rows, err, []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(3), int64(-1)}}); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				query string
				want  stuntgres.Error
			}{
				{"INSERT INTO d SELECT 1+2", stuntgres.Error{Code: "42804", Message: `column "f" is of type boolean but expression is of type integer`, Hint: "You will need to rewrite or cast the expression.", Position: "22"}},
				{"/*é*/ INSERT INTO d SELECT (1+2)::bigint", stuntgres.Error{Code: "42804", Message: `column "f" is of type boolean but expression is of type bigint`, Hint: "You will need to rewrite or cast the expression.", Position: "29"}},
				{"INSERT INTO d SELECT TRUE,(1+2)::bigint", stuntgres.Error{Code: "42601", Message: "INSERT has more expressions than target columns", Position: "28"}},
			} {
				result, err := db.ExecContext(ctx, tc.query)
				if result != nil {
					t.Fatal("analysis failure exposed result")
				}
				if err := referenceMigratedDiagnostic(t, err, tc.want); err != nil {
					t.Fatal(tc.query, err)
				}
				stmt, err := db.PrepareContext(ctx, tc.query)
				if stmt != nil {
					stmt.Close()
					t.Fatal("analysis failure exposed handle")
				}
				if err := referenceMigratedDiagnostic(t, err, tc.want); err != nil {
					t.Fatal(tc.query, err)
				}
				rows, err := db.QueryContext(ctx, "SELECT f FROM d")
				if err := referenceMigratedRows(rows, err, []string{"f"}, []string{"BOOL"}, [][]any{{true}}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
