package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceDMLAuthority(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		inheritanceQueryMapped(s)
		const role = "inherit_dml_actor"
		inheritanceQueryRole(s, role, "NOBYPASSRLS")
		s.exec("GRANT UPDATE,DELETE ON p TO "+role, 0)
		s.exec("GRANT SELECT(a) ON p TO "+role, 0)
		s.exec("SET ROLE "+role, 0)
		s.exec("UPDATE p SET a=a+10", 2)
		s.fail("UPDATE c SET a=0", fixture.Diagnostic{Code: "42501", Message: "permission denied for table c"})
		s.fail("DELETE FROM c", fixture.Diagnostic{Code: "42501", Message: "permission denied for table c"})
		s.fail("UPDATE p SET a=0 RETURNING x", fixture.Diagnostic{Code: "42501", Message: "permission denied for table p"})
		s.exec("SET ROLE NONE", 0)
		s.exec("REVOKE SELECT(a) ON p FROM "+role, 0)
		s.exec("SET ROLE "+role, 0)
		s.exec("UPDATE p SET a=5 WHERE TRUE", 2)
		s.fail("UPDATE p SET a=a+1", fixture.Diagnostic{Code: "42501", Message: "permission denied for table p"})
		s.fail("DELETE FROM p WHERE a=5", fixture.Diagnostic{Code: "42501", Message: "permission denied for table p"})
		s.fail("DELETE FROM p RETURNING a", fixture.Diagnostic{Code: "42501", Message: "permission denied for table p"})
		s.exec("DELETE FROM p WHERE FALSE", 0)
		s.exec("DELETE FROM p WHERE TRUE", 2)
		s.exec("SET ROLE NONE", 0)
		s.exec("INSERT INTO p VALUES(1,'p')", 1)
		s.exec("INSERT INTO c VALUES(true,2,'c',9)", 1)
		s.exec("GRANT SELECT(a) ON p TO "+role, 0)
		s.exec("SET ROLE "+role, 0)
		const query = "UPDATE p SET a=a+1"
		held, err := s.c.PrepareContext(s.p.Ctx, query)
		if err != nil {
			s.t.Fatal(err)
		}
		defer held.Close()
		run := func(step fixture.Step) {
			fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: held, SQL: query}, s.record, step)
		}
		run(fixture.Exec(query, 2))
		s.exec("SET ROLE NONE", 0)
		s.exec("REVOKE SELECT(a) ON p FROM "+role, 0)
		s.exec("SET ROLE "+role, 0)
		run(fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table p"}))
		s.exec("SET ROLE NONE", 0)
		s.exec("GRANT SELECT(a) ON p TO "+role, 0)
		s.exec("REVOKE UPDATE ON p FROM "+role, 0)
		s.exec("GRANT UPDATE,SELECT ON c TO "+role, 0)
		s.exec("SET ROLE "+role, 0)
		run(fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table p"}))
		s.exec("SET ROLE NONE", 0)
		s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}})
		s.exec("GRANT UPDATE ON p TO "+role, 0)
		s.exec("SET ROLE "+role, 0)
		admin, err := s.b.DB.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer admin.Close()
		s.exec("BEGIN", 0)
		s.exec("UPDATE p SET a=a+1", 2)
		// Revoke through a separate administrative connection outside the staged
		// transaction: native utility-in-explicit-Tx is an independent boundary.
		// Authorization belongs to execution, not a retroactive commit-time check.
		fixture.RunStep(s.t, s.p.Ctx, admin, s.record, fixture.Exec("REVOKE UPDATE ON p FROM "+role, 0))
		s.exec("COMMIT", 0)
		s.exec("SET ROLE NONE", 0)
		s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(4)}})
		s.exec("SET ROLE "+role, 0)
		run(fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table p"}))
		s.exec("SET ROLE NONE", 0)
	})
	for _, readChild := range []bool{false, true} {
		t.Run(map[bool]string{false: "parent_select_detail", true: "child_select_detail"}[readChild], func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				inheritanceQueryRole(s, "inherit_dml_disclosure", "NOBYPASSRLS")
				s.exec("CREATE TABLE p(a INT,x TEXT)", 0)
				s.exec("CREATE TABLE c(secret TEXT,CONSTRAINT child_positive CHECK(a>0)) INHERITS(p)", 0)
				s.exec("INSERT INTO p VALUES(1,'parent')", 1)
				s.exec("INSERT INTO c VALUES(2,'child','private')", 1)
				s.exec("GRANT UPDATE ON p TO inherit_dml_disclosure", 0)
				read := "p"
				detail := "Failing row contains (0, child)."
				if readChild {
					read = "c"
					detail = "Failing row contains (a) = (0)."
				}
				s.exec("GRANT SELECT ON "+read+" TO inherit_dml_disclosure", 0)
				s.exec("SET ROLE inherit_dml_disclosure", 0)
				// Pinned18.0 projects the named parent's row and checks its SELECT rights;
				// the physical child name remains the diagnostic subject, never its secret.
				s.fail("UPDATE p SET a=0", fixture.Diagnostic{Code: "23514", Message: `new row for relation "c" violates check constraint "child_positive"`, Detail: detail, Schema: "public", Table: "c", Constraint: "child_positive"})
				s.exec("SET ROLE NONE", 0)
				s.values("SELECT * FROM ONLY p", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "parent"}})
				s.values("SELECT * FROM ONLY c", []string{"a", "x", "secret"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(2), "child", "private"}})
			})
		})
	}
}

func TestReferenceInheritanceDMLRLS(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		inheritanceQueryRole(s, "inherit_dml_owner", "NOBYPASSRLS")
		inheritanceQueryRole(s, "inherit_dml_rls", "NOBYPASSRLS")
		inheritanceQueryRole(s, "inherit_dml_bypass", "BYPASSRLS")
		s.exec("SET ROLE inherit_dml_owner", 0)
		fixture.WithPublicSchemaOwner(s.t, s.p, s.b, "inherit_dml_owner", func() { s.exec("CREATE TABLE p(a INT)", 0) })
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("SET ROLE NONE", 0)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("INSERT INTO c VALUES(2)", 1)
		s.exec("GRANT SELECT,UPDATE,DELETE ON p,c TO inherit_dml_rls,inherit_dml_bypass", 0)
		s.exec("ALTER TABLE p ENABLE ROW LEVEL SECURITY", 0)
		s.exec("SET ROLE inherit_dml_rls", 0)
		s.exec("UPDATE p SET a=a+10", 0)
		s.exec("DELETE FROM p", 0)
		s.exec("SET ROLE NONE", 0)
		s.exec("SET ROLE inherit_dml_owner", 0)
		s.exec("UPDATE p SET a=a+1", 2)
		s.exec("ALTER TABLE p FORCE ROW LEVEL SECURITY", 0)
		s.exec("UPDATE p SET a=a+10", 0)
		s.exec("DELETE FROM p", 0)
		s.exec("SET ROLE NONE", 0)
		s.exec("SET ROLE inherit_dml_bypass", 0)
		s.exec("UPDATE p SET a=a+1", 2)
		s.exec("SET ROLE NONE", 0)
		s.exec("ALTER TABLE p DISABLE ROW LEVEL SECURITY", 0)
		s.exec("ALTER TABLE c ENABLE ROW LEVEL SECURITY", 0)
		s.exec("SET ROLE inherit_dml_rls", 0)
		s.exec("UPDATE p SET a=a+1", 2)
		s.exec("UPDATE c SET a=a+100", 0)
		s.exec("DELETE FROM c", 0)
		s.exec("SET ROLE NONE", 0)
		s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(4)}, {int64(5)}})
		s.exec("ALTER TABLE p ENABLE ROW LEVEL SECURITY", 0)
		s.exec("CREATE POLICY selected ON p TO inherit_dml_rls USING(a=5) WITH CHECK(a<10)", 0)
		s.exec("SET ROLE inherit_dml_rls", 0)
		s.fail("UPDATE p SET a=a+1", fixture.Diagnostic{Code: "42501", Message: `new row violates row-level security policy for table "p"`})
		// SELECT visibility also checks the proposed row.
		s.exec("UPDATE p SET a=a", 1)
		s.exec("SET ROLE NONE", 0)
		s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(4)}, {int64(5)}})
	})
}
