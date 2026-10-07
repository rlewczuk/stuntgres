//go:build reference

package reference_test

import "testing"

// SQL/public ACL mutation is outside the native surface. Native exact physical
// manifests are asserted by TestQueryOrderACL using the engine's ACL authority.
func TestReferenceQueryOrderPostgresColumnACL(t *testing.T) {
	p := newReferencePair(t)
	run := func(step referenceStep) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, step) }
	run(referenceExec("CREATE ROLE order_reader", 0))
	run(referenceExec("CREATE TABLE acl_t(id INT,secret INT)", 0))
	run(referenceExec("INSERT INTO acl_t VALUES(1,9),(2,8)", 2))
	t.Cleanup(func() {
		if _, err := referenceAdmin.ExecContext(p.ctx, "DROP OWNED BY order_reader CASCADE; DROP ROLE order_reader"); err != nil {
			t.Error(err)
		}
	})
	for _, q := range []string{"GRANT USAGE ON SCHEMA public TO order_reader", "GRANT SELECT(id) ON acl_t TO order_reader"} {
		run(referenceExec(q, 0))
	}
	conn, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
	referenceRunStep(t, p.ctx, conn, p.postgresRecord, referenceExec("SET SESSION AUTHORIZATION order_reader", 0))
	for _, q := range []string{"SELECT id AS secret FROM acl_t ORDER BY secret", "SELECT id AS secret FROM acl_t ORDER BY 1"} {
		step := referenceSelect(q, []string{"secret"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
		referenceRunStep(t, p.ctx, conn, p.postgresRecord, step)
		referenceRunStep(t, p.ctx, conn, p.postgresRecord, referencePrepared(step))
	}
	for _, q := range []string{"SELECT id FROM acl_t ORDER BY secret+1", "SELECT id AS secret FROM acl_t ORDER BY acl_t.secret"} {
		step := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table acl_t"})
		step.selectRows = true
		referenceRunStep(t, p.ctx, conn, p.postgresRecord, step)
		referenceRunStep(t, p.ctx, conn, p.postgresRecord, referencePrepared(step))
	}
}
