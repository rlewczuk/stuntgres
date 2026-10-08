//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceAlterTypePreparedDML(t *testing.T) {
	for _, narrow := range []bool{false, true} {
		t.Run(fmt.Sprint(narrow), func(t *testing.T) {
			p := newReferencePair(t)
			for _, b := range []struct {
				name   string
				db     *sql.DB
				record *referenceRecorder
			}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
				t.Run(b.name, func(t *testing.T) {
					conn, err := b.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.record, s) }
					before, after := "INT", "BIGINT"
					typ := "INT8"
					if narrow {
						before, after = "BIGINT", "INT"
						typ = "INT4"
					}
					run(referenceExec("CREATE TABLE t(c "+before+")", 0))
					old, err := conn.PrepareContext(p.ctx, "INSERT INTO t VALUES($1)")
					if err != nil {
						t.Fatal(err)
					}
					defer old.Close()
					explicit, err := conn.PrepareContext(p.ctx, "INSERT INTO t VALUES($1::"+before+")")
					if err != nil {
						t.Fatal(err)
					}
					defer explicit.Close()
					run(referenceExec("ALTER TABLE t ALTER c TYPE "+after, 0))
					result, err := old.ExecContext(p.ctx, int64(7))
					if err != nil {
						t.Fatal("retained parameter rejected", err)
					}
					if n, err := result.RowsAffected(); err != nil || n != 1 {
						t.Fatal(n, err)
					}
					if _, err = explicit.ExecContext(p.ctx, int64(7)); err != nil {
						t.Fatal("retained explicit parameter", err)
					}
					if narrow {
						_, err = old.ExecContext(p.ctx, int64(2147483648))
						want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "integer out of range"}
						if got := referenceExtractDiagnostic(t, err); got != want {
							t.Fatal(got, want)
						}
						_, err = explicit.ExecContext(p.ctx, int64(2147483648))
						if got := referenceExtractDiagnostic(t, err); got != want {
							t.Fatal(got, want)
						}
					}
					run(referencePrepared(referenceExec("INSERT INTO t VALUES($1)", 1, int64(9))))
					run(referenceSelect("SELECT c FROM t ORDER BY c", []string{"c"}, []string{typ}, [][]any{{int64(7)}, {int64(7)}, {int64(9)}}))
				})
			}
		})
	}
}
