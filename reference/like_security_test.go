package reference_test

import "testing"

func TestReferenceLikeOracleSecurity(t *testing.T) {
	roleReferenceCleanup(t, []string{"like_reader"})
	p := newReferenceScenario(t)
	schemaReferenceCleanup(t, "like_denied")
	for _, backend := range p.backends() {
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
	run(referenceExec("CREATE ROLE like_reader", 0))
	run(referenceExec("CREATE TABLE t(v TEXT)", 0))
	run(referenceExec("CREATE SCHEMA like_denied", 0))
	missing := referenceImplicitError("SELECT v OPERATOR(missing_like.~~) 'a%' FROM t", referenceDiagnostic{Code: "3F000", Message: "schema \"missing_like\" does not exist", Position: "10"})
	missing.selectRows = true
	missing.errorStage = "execute"
	run(missing)
	missing.errorStage = "prepare"
	run(referencePrepared(missing))
	run(referenceExec("CREATE TABLE acl_t(id INT,p TEXT,e TEXT)", 0))
	run(referenceExec("INSERT INTO acl_t VALUES(1,'a%','!')", 1))
	if backend.pg { run(referenceExec("GRANT USAGE ON SCHEMA public TO like_reader", 0)) }
	run(referenceExec("GRANT SELECT(id) ON acl_t TO like_reader", 0))
	conn, err := backend.db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
	reader := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, backend.recorder, s) }
	reader(referenceExec("SET SESSION AUTHORIZATION like_reader", 0))
	denied := referenceImplicitError("SELECT v OPERATOR(like_denied.~~) 'a%' FROM public.t", referenceDiagnostic{Code: "42501", Message: "permission denied for schema like_denied", Position: "10"})
	denied.selectRows = true
	denied.errorStage = "execute"
	reader(denied)
	denied.errorStage = "prepare"
	reader(referencePrepared(denied))
	for _, empty := range []bool{false, true} {
		if empty {
			run(referenceExec("DELETE FROM acl_t", 1))
		}
		for _, q := range []string{"SELECT id FROM acl_t WHERE 'a' LIKE p", "SELECT id FROM acl_t WHERE 'a' LIKE 'a' ESCAPE e", "SELECT id FROM acl_t WHERE FALSE AND 'a' LIKE p ESCAPE e"} {
			t.Run(q, func(t *testing.T) {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table acl_t"})
				s.selectRows = true
				s.errorStage = "execute"
				reader(s)
				reader(referencePrepared(s))
			})
		}
	}
	run(referenceExec("GRANT SELECT(p) ON acl_t TO like_reader", 0))
	reader(referenceSelect("SELECT id FROM acl_t WHERE 'a' LIKE p", []string{"id"}, []string{"INT4"}, nil))
	s := referenceImplicitError("SELECT id FROM acl_t WHERE 'a' LIKE p ESCAPE e", referenceDiagnostic{Code: "42501", Message: "permission denied for table acl_t"})
	s.selectRows = true
	s.errorStage = "execute"
	reader(s)
	reader(referencePrepared(s))
	run(referenceExec("GRANT SELECT(e) ON acl_t TO like_reader", 0))
	reader(referenceSelect("SELECT id FROM acl_t WHERE 'a' LIKE p ESCAPE e", []string{"id"}, []string{"INT4"}, nil))
	}
}
