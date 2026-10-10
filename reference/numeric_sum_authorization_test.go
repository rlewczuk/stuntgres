package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceNumericSumAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"numeric_sum_reader"})
	numericSumScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
		run(referenceExec("CREATE USER numeric_sum_reader", 0))
		run(referenceExec("CREATE TABLE source(v INTEGER,secret INTEGER,keep BOOLEAN)", 0))
		run(referenceExec("INSERT INTO source VALUES (1,9,TRUE),(3,8,FALSE)", 2))
		if pg {
			run(referenceExec("GRANT USAGE ON SCHEMA public TO numeric_sum_reader", 0))
		}
		defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
		q := "SELECT sum(v) FROM source"
		stmt, err := conn.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
		run(referenceExec("SET SESSION AUTHORIZATION numeric_sum_reader", 0))
		denied := func(q string) {
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table source"})
				s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
				run(s)
			}
		}
		for _, q := range []string{"SELECT sum(v) FROM source", "SELECT sum(v) FROM source WHERE FALSE", "SELECT sum(v) FILTER(WHERE keep) FROM source", "SELECT sum(v ORDER BY secret) FROM source", "SELECT sum(v) FROM source HAVING sum(secret)>0", "SELECT sum(v) FROM source ORDER BY sum(secret)"} {
			denied(q)
		}
		s := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table source"})
		s.selectRows, s.errorStage = true, "execute"
		referenceRunStep(t, p.ctx, held, recorder, s)
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		referenceRunStep(t, p.ctx, held, recorder, referenceSelect(q, []string{"sum"}, []string{"INT8"}, [][]any{{int64(4)}}))
		run(referenceExec("GRANT SELECT(v) ON source TO numeric_sum_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION numeric_sum_reader", 0))
		numericSumBoth(t, p, conn, recorder, referenceSelect(q, []string{"sum"}, []string{"INT8"}, [][]any{{int64(4)}}))
		for _, q := range []string{"SELECT sum(secret) FROM source WHERE FALSE", "SELECT sum(v) FILTER(WHERE keep) FROM source", "SELECT sum(v ORDER BY secret) FROM source", "SELECT sum(v) FROM source HAVING sum(secret)>0", "SELECT sum(v) FROM source ORDER BY sum(secret)"} {
			denied(q)
		}
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("GRANT SELECT(keep) ON source TO numeric_sum_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION numeric_sum_reader", 0))
		numericSumBoth(t, p, conn, recorder, referenceSelect("SELECT sum(v) FILTER(WHERE keep) FROM source", []string{"sum"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("REVOKE SELECT(v) ON source FROM numeric_sum_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION numeric_sum_reader", 0))
		referenceRunStep(t, p.ctx, held, recorder, s)
	})
}
