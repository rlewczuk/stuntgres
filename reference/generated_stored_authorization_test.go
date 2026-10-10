package reference_test

import "testing"

func TestReferenceGeneratedStoredAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"generated_actor"})
	uniqueReferenceSchemaCleanup(t, "generated_guarded")
	generatedStoredScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, unused func(referenceStep)) {
		conn, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, backend.recorder, s) }
		run(referenceExec(`CREATE ROLE generated_actor`, 0))
		run(referenceExec(`CREATE SCHEMA generated_guarded`, 0))
		run(referenceExec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
		run(referenceExec(`INSERT INTO g(a) VALUES(2)`, 1))
		if backend.pg {
			run(referenceExec(`GRANT USAGE ON SCHEMA public,generated_guarded TO generated_actor`, 0))
		}
		run(referenceExec(`SET SESSION AUTHORIZATION generated_actor`, 0))
		for _, q := range []string{`INSERT INTO g(a) VALUES(4)`, `UPDATE g SET a=8`, `SELECT b FROM g`} {
			s := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table g"})
			s.selectRows = q == `SELECT b FROM g`
			run(s)
		}
		run(referenceImplicitError(`CREATE TABLE generated_guarded.denied(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, referenceDiagnostic{Code: "42501", Message: "permission denied for schema generated_guarded", Position: "14"}))
		run(referenceExec(`RESET SESSION AUTHORIZATION`, 0))
		run(referenceSelect(`SELECT * FROM g`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
			run(referenceExec(`GRANT SELECT(b),INSERT(a),UPDATE(a) ON g TO generated_actor`, 0))
			run(referenceExec(`SET SESSION AUTHORIZATION generated_actor`, 0))
			run(referenceSelect(`SELECT b FROM g`, []string{"b"}, []string{"INT4"}, [][]any{{int64(3)}}))
			run(referenceExec(`INSERT INTO g(a) VALUES(4)`, 1))
			run(referenceExec(`UPDATE g SET a=8`, 2))
			run(referenceImplicitError(`UPDATE g SET a=a+1`, referenceDiagnostic{Code: "42501", Message: "permission denied for table g"}))
			run(referenceExec(`RESET SESSION AUTHORIZATION`, 0))
	})
}
