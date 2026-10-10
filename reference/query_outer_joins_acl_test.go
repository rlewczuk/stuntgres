package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryOuterJoinsACL(t *testing.T) {
	roleReferenceCleanup(t, []string{"outer_join_reader"})
	outerJoinsBackendScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, backend referenceBackend) {
		for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
			outerJoinsCases(t, p, conn, backend.recorder, referenceSelect("SELECT 1 FROM a "+kind+" JOIN b USING(k) LIMIT 0", []string{"?column?"}, []string{"INT4"}, nil))
		}
		if backend.pg {
			referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
		}
			for _, step := range []referenceStep{referenceExec("CREATE ROLE outer_join_reader", 0), referenceExec("GRANT SELECT(k) ON a,b,empty_a,empty_b TO outer_join_reader", 0), referenceExec("SET SESSION AUTHORIZATION outer_join_reader", 0)} {
				referenceRunStep(t, p.ctx, conn, backend.recorder, step)
			}
			defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
			for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
				outerJoinsCases(t, p, conn, backend.recorder, referenceSelect("SELECT 1 FROM a "+kind+" JOIN b USING(k) LIMIT 0", []string{"?column?"}, []string{"INT4"}, nil))
			}
			if _, err := backend.db.ExecContext(p.ctx, "REVOKE SELECT(k) ON b,empty_b FROM outer_join_reader"); err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
				for _, table := range []string{"b", "empty_b"} {
					for _, condition := range []string{" USING(k)", " ON FALSE"} {
						for _, suffix := range []string{"", " LIMIT 0"} {
							query := "SELECT 1 FROM a " + kind + " JOIN " + table + condition + suffix
							step := referenceImplicitError(query, referenceDiagnostic{Code: "42501", Message: "permission denied for table " + table})
							step.selectRows, step.errorStage = true, "execute"
							referenceRunStep(t, p.ctx, conn, backend.recorder, step)
							referenceRunStep(t, p.ctx, conn, backend.recorder, referencePrepared(step))
						}
					}
				}
			}
	})
}
