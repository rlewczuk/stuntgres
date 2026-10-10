package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceUniqueIndexBuildLocks(t *testing.T) {
	for _, commit := range []bool{false, true} {
		p := newReferenceScenario(t)
		for _, b := range p.backends() {
			t.Run(fmt.Sprintf("%s/commit=%t", b.name, commit), func(t *testing.T) {
				a, builder := referenceLockConnections(t, p.ctx, b.db)
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
				run(referenceExec("CREATE TABLE ui_t(k INT)", 0))
				run(referenceExec("INSERT INTO ui_t VALUES(1)", 1))
				run(referenceExec("BEGIN", 0))
				run(referenceExec("INSERT INTO ui_t VALUES(1)", 1))
				for _, c := range []struct{ setting, code, message string }{
					{"lock_timeout", "55P03", "canceling statement due to lock timeout"},
					{"statement_timeout", "57014", "canceling statement due to statement timeout"},
				} {
					referenceLockExec(t, p.ctx, builder, "SET "+c.setting+"='100ms'", 0)
					s := referencePrepared(referenceImplicitError("CREATE UNIQUE INDEX ui_key ON ui_t(k)", referenceDiagnostic{Code: c.code, Message: c.message}))
					s.errorStage = "execute"
					referenceRunStep(t, p.ctx, builder, b.recorder, s)
					referenceLockExec(t, p.ctx, builder, "RESET "+c.setting, 0)
				}
				var ap, bp int
				if b.pg {
					if err := a.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&ap); err != nil {
						t.Fatal(err)
					}
					if err := builder.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&bp); err != nil {
						t.Fatal(err)
					}
				}
				done := referenceRowAsync(t, p.ctx, builder, "CREATE UNIQUE INDEX ui_key ON ui_t(k)", true, false)
				if b.pg {
					referenceRowWait(t, p.ctx, bp, ap)
				}
				// ACCESS SHARE reader is compatible with the pending ordinary build.
				referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT k FROM ui_t", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
				if commit {
					run(referenceExec("COMMIT", 0))
				} else {
					run(referenceExec("ROLLBACK", 0))
				}
				outcome := referenceLockResult(t, done)
				if commit {
					want := uniqueIndexReferenceBuildDiagnostic("ui_t", "ui_key", "Key (k)=(1) is duplicated.")
					referenceAssertDiagnostic(t, outcome.err, &want)
					if outcome.result != nil {
						t.Fatal("failed build returned result")
					}
					run(referenceSelect("SELECT relname FROM pg_class WHERE relname='ui_key'", []string{"relname"}, []string{"NAME"}, nil))
					run(referenceExec("DELETE FROM ui_t", 2))
					run(referenceExec("INSERT INTO ui_t VALUES(1)", 1))
					run(referenceExec("CREATE UNIQUE INDEX ui_key ON ui_t(k)", 0))
				} else {
					referenceState(t, outcome.err, "")
					referenceLockAffected(t, outcome.result, 0)
				}
				run(referenceErrorDiagnostic("INSERT INTO ui_t VALUES(1)", uniqueReferenceDuplicate("ui_t", "ui_key", "Key (k)=(1) already exists.")))
			})
		}
	}
}
