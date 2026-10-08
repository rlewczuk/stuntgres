//go:build reference

package reference_test

import "testing"

// Native public GRANT is not added. TestQueryAliasesPhysicalACL independently
// checks the engine's physical attribute authority, including held-plan revoke.
func TestReferenceQueryAliasesPostgresColumnACL(t *testing.T) {
	p := newReferencePair(t)
	run := func(step referenceStep) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, step) }
	run(referenceExec("CREATE ROLE alias_reader", 0))
	t.Cleanup(func() {
		if _, err := referenceAdmin.ExecContext(p.ctx, "DROP OWNED BY alias_reader CASCADE; DROP ROLE alias_reader"); err != nil {
			t.Error(err)
		}
	})
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE acl_t(id INT,secret INT)", 0),
		referenceExec("CREATE TABLE acl_empty(id INT,secret INT)", 0),
		referenceExec("INSERT INTO acl_t VALUES(1,9),(2,8)", 2),
		referenceExec("GRANT USAGE ON SCHEMA public TO alias_reader", 0),
		referenceExec("GRANT SELECT(id) ON acl_t,acl_empty TO alias_reader", 0),
	} {
		run(step)
	}
	conn, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
	referenceRunStep(t, p.ctx, conn, p.postgresRecord, referenceExec("SET SESSION AUTHORIZATION alias_reader", 0))
	q := "SELECT x.iid AS secret FROM acl_t x(iid) ORDER BY secret"
	stmt, err := conn.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal("authorized prepare failed", err)
	}
	defer stmt.Close()
	held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
	step := referenceSelect(q, []string{"secret"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
	referenceRunStep(t, p.ctx, conn, p.postgresRecord, step)
	referenceRunStep(t, p.ctx, held, p.postgresRecord, step)
	for _, table := range []string{"acl_t", "acl_empty"} {
		for _, query := range []string{
			"SELECT x.secret FROM " + table + " x(iid)",
			"SELECT x.iid AS secret FROM " + table + " x(iid) WHERE secret=9",
		} {
			step := referenceImplicitError(query, referenceDiagnostic{Code: "42501", Message: "permission denied for table " + table})
			step.selectRows, step.errorStage = true, "execute"
			referenceRunStep(t, p.ctx, conn, p.postgresRecord, step)
			// PostgreSQL analysis permits this Prepare; permission is checked
			// at execution, independently of empty/populated source rows.
			referenceRunStep(t, p.ctx, conn, p.postgresRecord, referencePrepared(step))
		}
	}
	if _, err := referenceAdmin.ExecContext(p.ctx, "REVOKE SELECT(id) ON acl_t,acl_empty FROM alias_reader"); err != nil {
		t.Fatal(err)
	}
	denied := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table acl_t"})
	denied.selectRows, denied.errorStage = true, "execute"
	referenceRunStep(t, p.ctx, held, p.postgresRecord, denied)
	for _, table := range []string{"acl_t", "acl_empty"} {
		step := referenceImplicitError("SELECT 1 AS secret FROM "+table+" x(iid) WHERE FALSE", referenceDiagnostic{Code: "42501", Message: "permission denied for table " + table})
		step.selectRows, step.errorStage = true, "execute"
		referenceRunStep(t, p.ctx, conn, p.postgresRecord, step)
		referenceRunStep(t, p.ctx, conn, p.postgresRecord, referencePrepared(step))
	}
}
