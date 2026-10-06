package stuntgres_test

import (
	"fmt"
	"stuntgres"
	"testing"
)

func TestByteaUTF8Diagnostics(t *testing.T) {
	for _, ch := range []string{"é", "界", "😀"} {
		for _, input := range []string{`\x` + ch + "0", `\x0` + ch} {
			t.Run(input, func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				execSQL(t, db, "CREATE TABLE t(v BYTEA)")
				q := fmt.Sprintf("/*é*/ INSERT INTO t VALUES('%s')", input)
				want := stuntgres.Error{Code: "22023", Message: fmt.Sprintf("invalid hexadecimal digit: \"%s\"", ch), Position: "28"}
				_, err := db.Exec(q)
				typeCoreDiagnostic(t, err, want)
				rows, err := db.Query(q)
				if rows != nil {
					rows.Close()
				}
				typeCoreDiagnostic(t, err, want)
				stmt, err := db.Prepare(q)
				if stmt != nil {
					stmt.Close()
				}
				typeCoreDiagnostic(t, err, want)
				rows, err = db.Query("SELECT v FROM t")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				if rows.Next() {
					t.Fatal("malformed input published a row")
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
