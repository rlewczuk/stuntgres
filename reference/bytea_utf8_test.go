//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceByteaUTF8Diagnostics(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name string
		db   *sql.DB
	}{{"native", p.memory}, {"postgres", p.postgres}} {
		for _, ch := range []string{"é", "界", "😀"} {
			for _, input := range []string{`\x` + ch + "0", `\x0` + ch} {
				t.Run(backend.name+"/"+input, func(t *testing.T) {
					conn, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					if _, err := conn.ExecContext(p.ctx, "CREATE TABLE t(v BYTEA)"); err != nil {
						t.Fatal(err)
					}
					defer conn.ExecContext(p.ctx, "DROP TABLE t")
					q := fmt.Sprintf("/*é*/ INSERT INTO t VALUES('%s')", input)
					want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf("invalid hexadecimal digit: \"%s\"", ch), Position: "28"}
					_, err = conn.ExecContext(p.ctx, q)
					referenceAssertDiagnostic(t, err, &want)
					rows, err := conn.QueryContext(p.ctx, q)
					if rows != nil {
						rows.Close()
					}
					referenceAssertDiagnostic(t, err, &want)
					stmt, err := conn.PrepareContext(p.ctx, q)
					if stmt != nil {
						stmt.Close()
					}
					referenceAssertDiagnostic(t, err, &want)
					rows, err = conn.QueryContext(p.ctx, "SELECT v FROM t")
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					if rows.Next() {
						t.Fatal("malformed input published row")
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}
