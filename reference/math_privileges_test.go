package reference_test

import "testing"

// Native exercises its existing ACL provider in TestMathBinding; GRANT remains
// outside this part. PostgreSQL supplies the independent column-grant witness.
func referenceMathPrivileges(t *testing.T) {
	const reader = "math_column_reader"
	roleReferenceCleanup(t, []string{reader})
	p := newReferenceScenario(t)
	conn, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, p.postgresRecord, s) }
	for _, q := range []string{"CREATE ROLE " + reader, "CREATE TABLE m(n NUMERIC,unused NUMERIC)", "GRANT USAGE ON SCHEMA public TO " + reader, "GRANT SELECT(unused) ON m TO " + reader} {
		run(referenceExec(q, 0))
	}
	run(referenceExec("INSERT INTO m VALUES(1.25,9)", 1))
	run(referenceExec("SET SESSION AUTHORIZATION "+reader, 0))
	for _, q := range []string{"SELECT round(trim_scale(n),1) FROM m", "SELECT round(n,1),count(*) FROM m GROUP BY 1"} {
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table m"})
			s.selectRows = true
			s.errorStage = "execute"
			s.prepared = prepared
			run(s)
		}
	}
	run(referenceExec("RESET SESSION AUTHORIZATION", 0))
	run(referenceExec("GRANT SELECT(n) ON m TO "+reader, 0))
	run(referenceExec("SET SESSION AUTHORIZATION "+reader, 0))
	s := referenceSelect("SELECT round(trim_scale(n),1) FROM m", []string{"round"}, []string{"NUMERIC"}, [][]any{{"1.3"}})
	run(s)
	run(referencePrepared(s))
}
