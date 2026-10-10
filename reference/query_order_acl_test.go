package reference_test

import "testing"

func TestReferenceQueryOrderPostgresColumnACL(t *testing.T) {
	roleReferenceCleanup(t, []string{"order_reader"})
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
	run := func(step referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, step) }
	run(referenceExec("CREATE ROLE order_reader", 0))
	run(referenceExec("CREATE TABLE acl_t(id INT,secret INT)", 0))
	run(referenceExec("INSERT INTO acl_t VALUES(1,9),(2,8)", 2))
	if backend.pg { run(referenceExec("GRANT USAGE ON SCHEMA public TO order_reader",0)) }
	for _, q := range []string{"GRANT SELECT(id) ON acl_t TO order_reader"} {
		run(referenceExec(q, 0))
	}
	conn, err := backend.db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
	referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("SET SESSION AUTHORIZATION order_reader", 0))
	for _, q := range []string{"SELECT id AS secret FROM acl_t ORDER BY secret", "SELECT id AS secret FROM acl_t ORDER BY 1"} {
		step := referenceSelect(q, []string{"secret"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
		referenceRunStep(t, p.ctx, conn, backend.recorder, step)
		referenceRunStep(t, p.ctx, conn, backend.recorder, referencePrepared(step))
	}
	for _, q := range []string{"SELECT id FROM acl_t ORDER BY secret+1", "SELECT id AS secret FROM acl_t ORDER BY acl_t.secret"} {
		step := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table acl_t"})
		step.selectRows = true
		referenceRunStep(t, p.ctx, conn, backend.recorder, step)
		referenceRunStep(t, p.ctx, conn, backend.recorder, referencePrepared(step))
	}
	}
}
