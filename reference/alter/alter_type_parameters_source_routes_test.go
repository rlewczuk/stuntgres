package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceAlterTypePreparedDMLSourceConsumerRoutes(t *testing.T) {
	for _, narrow := range []bool{false, true} {
		t.Run(fmt.Sprint(narrow), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { s.NonContext = !s.Prepared; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					before, after := "INT", "BIGINT"
					typ := "INT8"
					if narrow {
						before, after = "BIGINT", "INT"
						typ = "INT4"
					}
					run(fixture.Exec("CREATE TABLE t(c "+before+")", 0))
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					old, err := conn.PrepareContext(p.Ctx, "INSERT INTO t VALUES($1)")
					if err != nil {
						t.Fatal(err)
					}
					defer old.Close()
					explicit, err := conn.PrepareContext(p.Ctx, "INSERT INTO t VALUES($1::"+before+")")
					if err != nil {
						t.Fatal(err)
					}
					defer explicit.Close()
					update, err := conn.PrepareContext(p.Ctx, "UPDATE t SET c=$1")
					if err != nil {
						t.Fatal(err)
					}
					defer update.Close()
					run(fixture.Exec("ALTER TABLE t ALTER c TYPE "+after, 0))
					result, err := old.ExecContext(p.Ctx, int64(7))
					if err != nil {
						t.Fatal("retained parameter rejected", err)
					}
					if n, err := result.RowsAffected(); err != nil || n != 1 {
						t.Fatal(n, err)
					}
					if _, err = explicit.ExecContext(p.Ctx, int64(7)); err != nil {
						t.Fatal("retained explicit parameter", err)
					}
					if narrow {
						_, err = old.ExecContext(p.Ctx, int64(2147483648))
						want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "integer out of range"}
						if got := fixture.ExtractDiagnostic(t, err); got != want {
							t.Fatal(got, want)
						}
						_, err = explicit.ExecContext(p.Ctx, int64(2147483648))
						if got := fixture.ExtractDiagnostic(t, err); got != want {
							t.Fatal(got, want)
						}
					}
					run(fixture.Select("SELECT c FROM t ORDER BY c", []string{"c"}, []string{typ}, [][]any{{int64(7)}, {int64(7)}}))
					run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES($1)", 1, int64(9))))
					run(fixture.Select("SELECT c FROM t ORDER BY c", []string{"c"}, []string{typ}, [][]any{{int64(7)}, {int64(7)}, {int64(9)}}))
					run(fixture.Exec("DELETE FROM t WHERE c=9", 1))

					runUpdate := fixture.HeldStatement{Executor: conn, Statement: update, SQL: "UPDATE t SET c=$1"}
					fixture.RunStep(t, p.Ctx, runUpdate, b.Recorder, fixture.Exec("UPDATE t SET c=$1", 2, int64(8)))
					run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES($1)", 1, int64(9))))
					run(fixture.Select("SELECT c FROM t ORDER BY c", []string{"c"}, []string{typ}, [][]any{{int64(8)}, {int64(8)}, {int64(9)}}))
				})
			}
		})
	}
}
