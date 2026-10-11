package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUniqueIndexBuildLocks(t *testing.T) {
	for _, commit := range []bool{false, true} {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(fmt.Sprintf("%s/commit=%t", b.Name, commit), func(t *testing.T) {
				a, builder := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE ui_t(k INT)", 0))
				run(fixture.Exec("INSERT INTO ui_t VALUES(1)", 1))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("INSERT INTO ui_t VALUES(1)", 1))
				for _, c := range []struct{ setting, code, message string }{
					{"lock_timeout", "55P03", "canceling statement due to lock timeout"},
					{"statement_timeout", "57014", "canceling statement due to statement timeout"},
				} {
					fixture.LockExec(t, p.Ctx, builder, "SET "+c.setting+"='100ms'", 0)
					s := fixture.Prepared(fixture.ImplicitError("CREATE UNIQUE INDEX ui_key ON ui_t(k)", fixture.Diagnostic{Code: c.code, Message: c.message}))
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, builder, b.Recorder, s)
					fixture.LockExec(t, p.Ctx, builder, "RESET "+c.setting, 0)
				}
				var ap, bp int
				if b.PG {
					if err := a.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&ap); err != nil {
						t.Fatal(err)
					}
					if err := builder.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&bp); err != nil {
						t.Fatal(err)
					}
				}
				done := fixture.RowAsync(t, p.Ctx, builder, "CREATE UNIQUE INDEX ui_key ON ui_t(k)", true, false)
				if b.PG {
					fixture.RowWait(t, p.Ctx, bp, ap)
				}
				// ACCESS SHARE reader is compatible with the pending ordinary build.
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT k FROM ui_t", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
				if commit {
					run(fixture.Exec("COMMIT", 0))
				} else {
					run(fixture.Exec("ROLLBACK", 0))
				}
				outcome := fixture.LockResult(t, done)
				if commit {
					want := uniqueIndexReferenceBuildDiagnostic("ui_t", "ui_key", "Key (k)=(1) is duplicated.")
					fixture.AssertDiagnostic(t, outcome.Err, &want)
					if outcome.Result != nil {
						t.Fatal("failed build returned result")
					}
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname='ui_key'", []string{"relname"}, []string{"NAME"}, nil))
					run(fixture.Exec("DELETE FROM ui_t", 2))
					run(fixture.Exec("INSERT INTO ui_t VALUES(1)", 1))
					run(fixture.Exec("CREATE UNIQUE INDEX ui_key ON ui_t(k)", 0))
				} else {
					fixture.State(t, outcome.Err, "")
					fixture.LockAffected(t, outcome.Result, 0)
				}
				run(fixture.ErrorDiagnostic("INSERT INTO ui_t VALUES(1)", fixture.UniqueReferenceDuplicate("ui_t", "ui_key", "Key (k)=(1) already exists.")))
			})
		}
	}
}
