package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func inheritanceQueryRole(s *inheritanceCase, role, attributes string) {
	// This backend subtest owns the roles and releases dependencies before its
	// fixture pool/schema teardown; the ordinary role helper assumes the reverse.
	if s.b.PG {
		s.t.Cleanup(func() {
			if _, err := fixture.Admin.ExecContext(s.p.Ctx, "DROP OWNED BY "+role+" CASCADE"); err != nil {
				s.t.Error(err)
			}
			if _, err := fixture.Admin.ExecContext(s.p.Ctx, "DROP ROLE "+role); err != nil {
				s.t.Error(err)
			}
		})
	}
	s.exec("CREATE ROLE "+role+" NOSUPERUSER "+attributes, 0)
	if s.b.PG {
		s.exec("GRANT USAGE ON SCHEMA public TO "+role, 0)
	}
	s.t.Cleanup(func() { s.c.ExecContext(s.p.Ctx, "SET ROLE NONE") })
}

func inheritanceQueryDenied(s *inheritanceCase, query, table string) {
	step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table " + table})
	step.SelectRows = true
	s.run(step)
}

func TestReferenceInheritanceQueryAuthorization(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		inheritanceQueryMapped(s)
		inheritanceQueryRole(s, "inherit_query_reader", "NOBYPASSRLS")
		s.exec("GRANT SELECT ON p TO inherit_query_reader", 0)
		s.exec("SET ROLE inherit_query_reader", 0)
		s.values("SELECT * FROM p ORDER BY a", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "p"}, {int64(2), "c"}})
		inheritanceQueryDenied(s, "SELECT a FROM c", "c")
		inheritanceQueryDenied(s, "SELECT a FROM p WHERE a=2 FOR UPDATE", "p")
		s.exec("SET ROLE NONE", 0)
		s.exec("GRANT UPDATE ON p TO inherit_query_reader", 0)
		s.exec("SET ROLE inherit_query_reader", 0)
		s.values("SELECT a FROM p WHERE a=2 FOR UPDATE", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}})
		s.exec("SET ROLE NONE", 0)
		s.exec("REVOKE SELECT ON p FROM inherit_query_reader", 0)
		s.exec("GRANT SELECT ON c TO inherit_query_reader", 0)
		s.exec("SET ROLE inherit_query_reader", 0)
		s.values("SELECT a FROM c", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}})
		inheritanceQueryDenied(s, "SELECT a FROM p", "p")
		s.exec("SET ROLE NONE", 0)
		s.exec("GRANT SELECT(a) ON p TO inherit_query_reader", 0)
		s.exec("SET ROLE inherit_query_reader", 0)
		s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
		inheritanceQueryDenied(s, "SELECT x FROM p", "p")
		s.exec("SET ROLE NONE", 0)
	})
}

func TestReferenceInheritanceQueryRLS(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		inheritanceQueryRole(s, "inherit_query_owner", "NOBYPASSRLS")
		inheritanceQueryRole(s, "inherit_query_rls", "NOBYPASSRLS")
		inheritanceQueryRole(s, "inherit_query_bypass", "BYPASSRLS")
		s.exec("SET ROLE inherit_query_owner", 0)
		fixture.WithPublicSchemaOwner(s.t, s.p, s.b, "inherit_query_owner", func() { s.exec("CREATE TABLE p(a INT,x TEXT)", 0) })
		s.exec("INSERT INTO p VALUES(1,'p')", 1)
		s.exec("SET ROLE NONE", 0)
		s.exec("CREATE TABLE q(y BOOL,a INT)", 0)
		s.exec("CREATE TABLE c(z BIGINT) INHERITS(q,p)", 0)
		s.exec("INSERT INTO c VALUES(true,2,'c',9)", 1)
		s.exec("GRANT SELECT ON p,c TO inherit_query_rls,inherit_query_bypass", 0)
		s.exec("ALTER TABLE p ENABLE ROW LEVEL SECURITY", 0)
		read := func(rows [][]any) { s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, rows) }
		both := [][]any{{int64(1)}, {int64(2)}}
		s.exec("SET ROLE inherit_query_rls", 0)
		read(nil)
		s.exec("SET ROLE NONE", 0)
		s.exec("SET ROLE inherit_query_owner", 0)
		read(both)
		s.exec("ALTER TABLE p FORCE ROW LEVEL SECURITY", 0)
		read(nil)
		s.exec("SET ROLE NONE", 0)
		s.exec("SET ROLE inherit_query_bypass", 0)
		read(both)
		s.exec("SET ROLE NONE", 0)
		s.exec("ALTER TABLE p DISABLE ROW LEVEL SECURITY", 0)
		s.exec("ALTER TABLE c ENABLE ROW LEVEL SECURITY", 0)
		s.exec("SET ROLE inherit_query_rls", 0)
		read(both)
		s.values("SELECT a FROM c", []string{"a"}, []string{"INT4"}, nil)
		s.exec("SET ROLE NONE", 0)
		s.exec("ALTER TABLE p ENABLE ROW LEVEL SECURITY", 0)
		s.exec("CREATE POLICY keep_child ON p TO inherit_query_rls USING(a=2)", 0)
		s.exec("SET ROLE inherit_query_rls", 0)
		read([][]any{{int64(2)}})
		s.exec("SET ROLE NONE", 0)
	})
}
