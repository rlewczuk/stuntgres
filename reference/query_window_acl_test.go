//go:build reference

package reference_test

import "testing"

// Public GRANT is an intentional PostgreSQL-only API boundary. Native physical
// key manifests are checked by engine units in the implementation round.
func TestReferenceQueryWindowPostgresAuthorization(t *testing.T) {
	p := newReferenceScenario(t)
	if p.postgres == nil {
		t.Fatal("PostgreSQL authorization fixture required")
	}
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s) }
	run(referenceExec("CREATE ROLE window_reader", 0))
	t.Cleanup(func() {
		referenceAdmin.ExecContext(p.ctx, "DROP OWNED BY window_reader CASCADE; DROP ROLE window_reader")
	})
	run(referenceExec("CREATE TABLE t(id INT,p TEXT,k INT)", 0))
	run(referenceExec("GRANT USAGE ON SCHEMA public TO window_reader", 0))
	c, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
	referenceRunStep(t, p.ctx, c, p.postgresRecord, referenceExec("SET SESSION AUTHORIZATION window_reader", 0))
	denied := func(q string) {
		s := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table t"})
		s.selectRows = true
		for _, prepared := range []bool{false, true} {
			s.prepared = prepared
			s.errorStage = "execute" // ACL is checked on execution, not PG Prepare.
			referenceRunStep(t, p.ctx, c, p.postgresRecord, s)
		}
	}
	for _, q := range []string{"SELECT row_number() OVER() FROM t", "SELECT count(*) OVER() FROM t WHERE FALSE"} {
		denied(q)
	}
	run(referenceExec("GRANT SELECT(id) ON t TO window_reader", 0))
	q := "SELECT row_number() OVER(),count(*) OVER() FROM t"
	s := referenceSelect(q, []string{"row_number", "count"}, []string{"INT8", "INT8"}, nil)
	referenceRunStep(t, p.ctx, c, p.postgresRecord, s)
	referenceRunStep(t, p.ctx, c, p.postgresRecord, referencePrepared(s))
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
	referenceRunStep(t, p.ctx, held, p.postgresRecord, s)
	run(referenceExec("REVOKE SELECT(id) ON t FROM window_reader", 0))
	failure := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table t"})
	failure.selectRows = true
	failure.errorStage = "execute"
	referenceRunStep(t, p.ctx, held, p.postgresRecord, failure)
}
