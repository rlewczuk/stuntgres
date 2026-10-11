package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLikeOracleSecurity(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"like_reader"})
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "like_denied")
	for _, backend := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE ROLE like_reader", 0))
		run(fixture.Exec("CREATE TABLE t(v TEXT)", 0))
		run(fixture.Exec("CREATE SCHEMA like_denied", 0))
		missing := fixture.ImplicitError("SELECT v OPERATOR(missing_like.~~) 'a%' FROM t", fixture.Diagnostic{Code: "3F000", Message: "schema \"missing_like\" does not exist", Position: "10"})
		missing.SelectRows = true
		missing.ErrorStage = "execute"
		run(missing)
		missing.ErrorStage = "prepare"
		run(fixture.Prepared(missing))
		run(fixture.Exec("CREATE TABLE acl_t(id INT,p TEXT,e TEXT)", 0))
		run(fixture.Exec("INSERT INTO acl_t VALUES(1,'a%','!')", 1))
		if backend.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO like_reader", 0))
		}
		run(fixture.Exec("GRANT SELECT(id) ON acl_t TO like_reader", 0))
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		reader := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
		reader(fixture.Exec("SET SESSION AUTHORIZATION like_reader", 0))
		denied := fixture.ImplicitError("SELECT v OPERATOR(like_denied.~~) 'a%' FROM public.t", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema like_denied", Position: "10"})
		denied.SelectRows = true
		denied.ErrorStage = "execute"
		reader(denied)
		denied.ErrorStage = "prepare"
		reader(fixture.Prepared(denied))
		for _, empty := range []bool{false, true} {
			if empty {
				run(fixture.Exec("DELETE FROM acl_t", 1))
			}
			for _, q := range []string{"SELECT id FROM acl_t WHERE 'a' LIKE p", "SELECT id FROM acl_t WHERE 'a' LIKE 'a' ESCAPE e", "SELECT id FROM acl_t WHERE FALSE AND 'a' LIKE p ESCAPE e"} {
				t.Run(q, func(t *testing.T) {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table acl_t"})
					s.SelectRows = true
					s.ErrorStage = "execute"
					reader(s)
					reader(fixture.Prepared(s))
				})
			}
		}
		run(fixture.Exec("GRANT SELECT(p) ON acl_t TO like_reader", 0))
		reader(fixture.Select("SELECT id FROM acl_t WHERE 'a' LIKE p", []string{"id"}, []string{"INT4"}, nil))
		s := fixture.ImplicitError("SELECT id FROM acl_t WHERE 'a' LIKE p ESCAPE e", fixture.Diagnostic{Code: "42501", Message: "permission denied for table acl_t"})
		s.SelectRows = true
		s.ErrorStage = "execute"
		reader(s)
		reader(fixture.Prepared(s))
		run(fixture.Exec("GRANT SELECT(e) ON acl_t TO like_reader", 0))
		reader(fixture.Select("SELECT id FROM acl_t WHERE 'a' LIKE p ESCAPE e", []string{"id"}, []string{"INT4"}, nil))
	}
}
