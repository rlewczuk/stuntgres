package reference_test

import "testing"

func TestReferenceQueryAliasesPostgresColumnACL(t *testing.T) {
	roleReferenceCleanup(t, []string{"alias_reader"})
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
	run := func(step referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, step) }
	run(referenceExec("CREATE ROLE alias_reader", 0))
	if backend.pg { run(referenceExec("GRANT USAGE ON SCHEMA public TO alias_reader",0)) }
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE acl_t(id INT,secret INT)", 0),
		referenceExec("CREATE TABLE acl_empty(id INT,secret INT)", 0),
		referenceExec("INSERT INTO acl_t VALUES(1,9),(2,8)", 2),
		referenceExec("GRANT SELECT(id) ON acl_t,acl_empty TO alias_reader", 0),
	} {
		run(step)
	}
	conn, err := backend.db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
	referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("SET SESSION AUTHORIZATION alias_reader", 0))
	q := "SELECT x.iid AS secret FROM acl_t x(iid) ORDER BY secret"
	stmt, err := conn.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal("authorized prepare failed", err)
	}
	defer stmt.Close()
	held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
	step := referenceSelect(q, []string{"secret"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
	referenceRunStep(t, p.ctx, conn, backend.recorder, step)
	referenceRunStep(t, p.ctx, held, backend.recorder, step)
	for _, table := range []string{"acl_t", "acl_empty"} {
		for _, query := range []string{
			"SELECT x.secret FROM " + table + " x(iid)",
			"SELECT x.iid AS secret FROM " + table + " x(iid) WHERE secret=9",
		} {
			step := referenceImplicitError(query, referenceDiagnostic{Code: "42501", Message: "permission denied for table " + table})
			step.selectRows, step.errorStage = true, "execute"
			referenceRunStep(t, p.ctx, conn, backend.recorder, step)
			// PostgreSQL analysis permits this Prepare; permission is checked
			// at execution, independently of empty/populated source rows.
			referenceRunStep(t, p.ctx, conn, backend.recorder, referencePrepared(step))
		}
	}
	if _, err := backend.db.ExecContext(p.ctx, "REVOKE SELECT(id) ON acl_t,acl_empty FROM alias_reader"); err != nil {
		t.Fatal(err)
	}
	denied := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table acl_t"})
	denied.selectRows, denied.errorStage = true, "execute"
	referenceRunStep(t, p.ctx, held, backend.recorder, denied)
	for _, table := range []string{"acl_t", "acl_empty"} {
		step := referenceImplicitError("SELECT 1 AS secret FROM "+table+" x(iid) WHERE FALSE", referenceDiagnostic{Code: "42501", Message: "permission denied for table " + table})
		step.selectRows, step.errorStage = true, "execute"
		referenceRunStep(t, p.ctx, conn, backend.recorder, step)
		referenceRunStep(t, p.ctx, conn, backend.recorder, referencePrepared(step))
	}
	}
}
