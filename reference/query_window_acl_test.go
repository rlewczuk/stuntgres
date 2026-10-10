package reference_test

import "testing"

func TestReferenceQueryWindowPostgresAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"window_reader"})
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
	run(referenceExec("CREATE ROLE window_reader", 0))
	run(referenceExec("CREATE TABLE t(id INT,p TEXT,k INT)", 0))
	if backend.pg { run(referenceExec("GRANT USAGE ON SCHEMA public TO window_reader", 0)) }
	c, err := backend.db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
	referenceRunStep(t, p.ctx, c, backend.recorder, referenceExec("SET SESSION AUTHORIZATION window_reader", 0))
	denied := func(q string) {
		s := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table t"})
		s.selectRows = true
		for _, prepared := range []bool{false, true} {
			s.prepared = prepared
			s.errorStage = "execute" // ACL is checked on execution, not PG Prepare.
			referenceRunStep(t, p.ctx, c, backend.recorder, s)
		}
	}
	for _, q := range []string{"SELECT row_number() OVER() FROM t", "SELECT count(*) OVER() FROM t WHERE FALSE"} {
		denied(q)
	}
	run(referenceExec("GRANT SELECT(id) ON t TO window_reader", 0))
	q := "SELECT row_number() OVER(),count(*) OVER() FROM t"
	s := referenceSelect(q, []string{"row_number", "count"}, []string{"INT8", "INT8"}, nil)
	referenceRunStep(t, p.ctx, c, backend.recorder, s)
	referenceRunStep(t, p.ctx, c, backend.recorder, referencePrepared(s))
	for _, q := range []string{
		"SELECT row_number() OVER(PARTITION BY p) FROM t",
		"SELECT count(*) OVER(ORDER BY k) FROM t WHERE FALSE",
		"SELECT id FROM t ORDER BY row_number() OVER(ORDER BY k)",
		"SELECT id FROM t WINDOW unused AS(PARTITION BY p)",
	} {
		denied(q)
	}
	stmt, err := c.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := referenceHeldStatement{c, stmt, q}
	referenceRunStep(t, p.ctx, held, backend.recorder, s)
	run(referenceExec("REVOKE SELECT(id) ON t FROM window_reader", 0))
	failure := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table t"})
	failure.selectRows = true
	failure.errorStage = "execute"
	referenceRunStep(t, p.ctx, held, backend.recorder, failure)
	}
}
