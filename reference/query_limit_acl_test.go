//go:build reference

package reference_test

import "testing"

// Native public ACL mutation is not admitted. Stage2 engine regressions own
// native exact manifests; these use real PostgreSQL grants, not parity values.
func referenceLimitACL(t *testing.T) {
	p := newReferencePair(t)
	run := func(step referenceStep) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, step) }
	run(referenceExec("CREATE ROLE limit_reader", 0))
	t.Cleanup(func() {
		if _, err := referenceAdmin.ExecContext(p.ctx, "DROP OWNED BY limit_reader CASCADE; DROP ROLE limit_reader"); err != nil {
			t.Error(err)
		}
	})
	run(referenceExec("CREATE TABLE acl_t(id INT,secret INT)", 0))
	run(referenceExec("INSERT INTO acl_t VALUES(1,9),(2,8)", 2))
	run(referenceExec("GRANT USAGE ON SCHEMA public TO limit_reader", 0))
	run(referenceExec("GRANT SELECT(id) ON acl_t TO limit_reader", 0))
	conn, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
	referenceRunStep(t, p.ctx, conn, p.postgresRecord, referenceExec("SET SESSION AUTHORIZATION limit_reader", 0))
	for _, query := range []string{
		"SELECT secret FROM acl_t LIMIT 0",
		"SELECT id FROM acl_t ORDER BY secret LIMIT 0",
		"SELECT secret FROM acl_t OFFSET 99",
		"SELECT secret FROM acl_t WHERE FALSE LIMIT 1",
	} {
		step := referenceImplicitError(query, referenceDiagnostic{Code: "42501", Message: "permission denied for table acl_t"})
		step.selectRows = true
		referenceRunStep(t, p.ctx, conn, p.postgresRecord, step)
		referenceRunStep(t, p.ctx, conn, p.postgresRecord, referencePrepared(step))
	}
	step := referenceSelect("SELECT id FROM acl_t LIMIT 0", []string{"id"}, []string{"INT4"}, referenceLimitIDs())
	referenceRunStep(t, p.ctx, conn, p.postgresRecord, step)
}
